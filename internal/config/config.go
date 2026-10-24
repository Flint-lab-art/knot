// Package config 负责加载客户端与服务端的 TOML 配置。
//
// 加载顺序：默认值 → 配置文件 → 环境变量覆盖（KNOT_SERVER_URL / KNOT_TOKEN）。
// 字段说明见 config.example.toml 与 docs/CLIENT.md。
package config

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
