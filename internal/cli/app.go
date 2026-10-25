package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"knot/internal/client"
	"knot/internal/config"
	"knot/internal/dto"
	"knot/internal/store/sqlite"
)

// app 是子命令共享的运行时上下文。
//
// 懒加载：`knot completion` 之类不需要网络/数据库的命令不应因为配置缺失而失败，
// 所以 client 与 db 在真正用到时才创建，并在 Close 时清理。
type app struct {
	out    io.Writer
	errOut io.Writer

	// flagConfig / flagCacheDB 由根命令全局参数绑定，PersistentPreRun 写入
	// configPath / cachePath（子命令只用后者）。
	flagConfig  string
	flagCacheDB string

	configPath string
	cachePath  string

	cfg    *config.Client
	cli    *client.Client
	db     *sqlite.DB
	cfgErr error
	dbErr  error
}

// newApp 构造上下文；配置在需要时才读。
func newApp(out, errOut io.Writer) *app {
	return &app{out: out, errOut: errOut}
}

// Config 返回客户端配置（读文件 + 环境变量，含 editor LookPath 校验）。
func (a *app) Config() (config.Client, error) {
	if a.cfgErr != nil {
		return config.Client{}, a.cfgErr
	}
	if a.cfg != nil {
		return *a.cfg, nil
	}
	cfg, err := config.LoadClient(a.configPath)
	if err != nil {
		a.cfgErr = err
		return config.Client{}, err
	}
	a.cfg = &cfg
	return cfg, nil
}

// Client 返回 HTTP 客户端，要求配置里有 server_url。
func (a *app) Client() (*client.Client, error) {
	if a.cli != nil {
		return a.cli, nil
	}
	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}
	if cfg.ServerURL == "" {
		return nil, errors.New("未配置 server_url（config.toml 或 KNOT_SERVER_URL）")
	}
	a.cli = client.New(cfg.ServerURL, cfg.Token, client.Options{})
	return a.cli, nil
}

// DB 打开本地缓存库（自动跑迁移）。
func (a *app) DB(ctx context.Context) (*sqlite.DB, error) {
	if a.dbErr != nil {
		return nil, a.dbErr
	}
	if a.db != nil {
		return a.db, nil
	}
	path := a.cachePath
	if path == "" {
		cfg, err := a.Config()
		if err != nil {
			return nil, err
		}
		path = cfg.CacheDBPath()
	}
	if path == "" {
		return nil, errors.New("无法确定缓存库路径（请设置 [cache].path）")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建缓存目录: %w", err)
	}
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		a.dbErr = err
		return nil, err
	}
	a.db = db
	return db, nil
}

// Close 释放资源，可重复调用。
func (a *app) Close() {
	if a.db != nil {
		_ = a.db.Close()
		a.db = nil
	}
}

// syncCache 用服务端快照刷新本地缓存；失败只提示到 errOut，不让写操作失败。
//
// 写操作成功后服务端已是真值，本地缓存落后可以由下次 `knot sync` 修复，
// 因此这里不把同步错误当成命令失败（docs/SYNC_CACHE.md「本地缓存一致性」）。
func (a *app) syncCache(ctx context.Context) {
	cli, err := a.Client()
	if err != nil {
		return
	}
	db, err := a.DB(ctx)
	if err != nil {
		return
	}
	snap, err := cli.Snapshot(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(a.errOut, "⚠ 本地缓存未更新（%v），稍后可用 `knot sync` 修复\n", err)
		return
	}
	if _, err := db.ApplySnapshot(ctx, snap, snap.SyncedAt); err != nil {
		_, _ = fmt.Fprintf(a.errOut, "⚠ 本地缓存未更新（%v），稍后可用 `knot sync` 修复\n", err)
	}
}

// resolveNote 把 selector（UUID 或关键词）解析成笔记元数据。
//
// 完整 UUID 直接查服务端；否则在本地缓存里做不区分大小写的子串匹配。
// 多于一条匹配时报错并列出候选，避免误操作删除/覆盖错笔记。
func (a *app) resolveNote(ctx context.Context, selector string) (dto.Note, error) {
	if id, ok := parseUUID(selector); ok {
		cli, err := a.Client()
		if err != nil {
			return dto.Note{}, err
		}
		detail, err := cli.GetNote(ctx, id)
		if err != nil {
			return dto.Note{}, err
		}
		return detail.Note, nil
	}
	db, err := a.DB(ctx)
	if err != nil {
		return dto.Note{}, err
	}
	matches, err := matchNotes(ctx, db, selector)
	if err != nil {
		return dto.Note{}, err
	}
	switch len(matches) {
	case 0:
		return dto.Note{}, fmt.Errorf("没有匹配 %q 的笔记（先跑一次 `knot sync`？）", selector)
	case 1:
		return matches[0], nil
	default:
		return dto.Note{}, ambiguous(selector, matches)
	}
}
