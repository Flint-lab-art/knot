// Package config 负责加载客户端与服务端的 TOML 配置。
//
// 加载顺序：默认值 → 配置文件 → 环境变量覆盖（KNOT_SERVER_URL / KNOT_TOKEN）。
// 字段说明见 config.example.toml 与 docs/CLIENT.md。
package config

import (
	"fmt"
	"os"

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
