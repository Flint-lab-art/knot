// Package config 负责加载客户端与服务端的 TOML 配置。
//
// 加载顺序：默认值 → 配置文件 → 环境变量覆盖（KNOT_SERVER_URL / KNOT_TOKEN）。
// 字段说明见 config.example.toml 与 docs/CLIENT.md。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Client 是 knot CLI / TUI 的配置。
type Client struct {
	ServerURL string `toml:"server_url"`
	Token     string `toml:"token"`
	Editor    Editor `toml:"editor"`
	Cache     Cache  `toml:"cache"`
}

// Editor 是外部编辑器设置；editor.command 加载时做 LookPath 校验。
type Editor struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// Cache 是本地 SQLite 缓存设置。Path 留空则用 os.UserCacheDir()/knot/cache.db。
type Cache struct {
	Path string `toml:"path"`
}

// Server 是 knot-server 的配置。
type Server struct {
	Listen string `toml:"listen"`
	PGDSN  string `toml:"pg_dsn"`
	// Token 是私有接口的 Bearer Token；为空时所有 /api/* 私有请求都被拒绝。
	Token string `toml:"token"`
}

// DefaultClient 返回客户端默认配置。
func DefaultClient() Client {
	return Client{
		Editor: Editor{Command: "nvim"},
	}
}

// DefaultServer 返回服务端默认配置（仅监听本机，由 Caddy 反代）。
func DefaultServer() Server {
	return Server{Listen: "127.0.0.1:8080"}
}

// LoadServer 读取服务端配置：默认值 → path（可为空）→ 环境变量。
//
// 环境变量用 KNOT_PG_DSN / KNOT_LISTEN / KNOT_TOKEN 覆盖，便于容器或临时调试；
// 生产环境建议只放在 config.toml（权限 600）。
func LoadServer(path string) (Server, error) {
	cfg := DefaultServer()
	if path != "" {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return Server{}, fmt.Errorf("读取配置 %s: %w", path, err)
		}
	}
	if v := os.Getenv("KNOT_PG_DSN"); v != "" {
		cfg.PGDSN = v
	}
	if v := os.Getenv("KNOT_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("KNOT_TOKEN"); v != "" {
		cfg.Token = v
	}
	return cfg, nil
}

// DefaultClientConfigPath 返回默认配置文件路径：$XDG_CONFIG_HOME/knot/config.toml。
// 取不到用户配置目录时返回空串（调用方视为「没有配置文件」）。
func DefaultClientConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "knot", "config.toml")
}

// DefaultCachePath 返回默认缓存库路径：$XDG_CACHE_HOME/knot/cache.db。
// 取不到用户缓存目录时返回空串，由调用方报错（不静默落到当前目录）。
func DefaultCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "knot", "cache.db")
}

// LoadClient 读取客户端配置：默认值 → path（可为空）→ 环境变量。
//
// path 为空时依次尝试 KNOT_CONFIG 与默认路径；文件不存在不算错误（首次使用）。
// 环境变量 KNOT_SERVER_URL / KNOT_TOKEN 覆盖同名字段。
// editor.command 会做 LookPath 校验，找不到时返回错误而不是等到编辑时才失败 ——
// 但仅当配置里显式指定了命令；默认的 nvim 在无 GUI/终端环境缺失时也报错，
// 因为此时 `knot edit` 根本无法工作。
func LoadClient(path string) (Client, error) {
	cfg := DefaultClient()
	if path == "" {
		path = os.Getenv("KNOT_CONFIG")
	}
	if path == "" {
		path = DefaultClientConfigPath()
	}
	if path != "" {
		if _, err := toml.DecodeFile(path, &cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Client{}, fmt.Errorf("读取配置 %s: %w", path, err)
		}
	}
	if v := os.Getenv("KNOT_SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("KNOT_TOKEN"); v != "" {
		cfg.Token = v
	}
	// 注意：环境变量可以只覆盖其一，另一个仍可来自文件。

	if cfg.Editor.Command == "" {
		return Client{}, fmt.Errorf("未配置 editor.command")
	}
	if _, err := exec.LookPath(cfg.Editor.Command); err != nil {
		return Client{}, fmt.Errorf("editor.command %q 不可执行: %w", cfg.Editor.Command, err)
	}
	return cfg, nil
}

// CacheDBPath 返回实际使用的缓存库路径：配置值优先，否则用默认路径。
func (c Client) CacheDBPath() string {
	if c.Cache.Path != "" {
		return c.Cache.Path
	}
	return DefaultCachePath()
}
