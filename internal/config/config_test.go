package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp 写一个临时 TOML 文件并返回路径。
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写临时配置: %v", err)
	}
	return path
}

func TestLoadServerDefaults(t *testing.T) {
	// 确保不受开发机环境影响。
	t.Setenv("KNOT_PG_DSN", "")
	t.Setenv("KNOT_LISTEN", "")
	// t.Setenv 设为空串时 os.Getenv 返回空，加载逻辑把它当未设置处理。

	cfg, err := LoadServer("")
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("默认监听地址错误: %q", cfg.Listen)
	}
	if cfg.PGDSN != "" {
		t.Fatalf("默认不应有 DSN，实际 %q", cfg.PGDSN)
	}
}

func TestLoadServerFromFile(t *testing.T) {
	t.Setenv("KNOT_PG_DSN", "")
	t.Setenv("KNOT_LISTEN", "")
	path := writeTemp(t, `
listen = "0.0.0.0:9000"
pg_dsn = "postgres://knot@localhost/knot"
`)
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9000" || cfg.PGDSN != "postgres://knot@localhost/knot" {
		t.Fatalf("文件配置未生效: %+v", cfg)
	}
}

// TestLoadServerEnvOverridesFile 是关键优先级：环境变量 > 文件 > 默认值。
func TestLoadServerEnvOverridesFile(t *testing.T) {
	path := writeTemp(t, `
listen = "0.0.0.0:9000"
pg_dsn = "postgres://from-file"
`)
	t.Setenv("KNOT_LISTEN", "127.0.0.1:1234")
	// 只覆盖 DSN 时，另一个字段应保留文件值。
	t.Setenv("KNOT_PG_DSN", "postgres://from-env")

	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Listen != "127.0.0.1:1234" {
		t.Fatalf("KNOT_LISTEN 未覆盖文件: %q", cfg.Listen)
	}
	if cfg.PGDSN != "postgres://from-env" {
		t.Fatalf("KNOT_PG_DSN 未覆盖文件: %q", cfg.PGDSN)
	}
}

// 文件缺字段时保留默认值，而不是被零值覆盖。
func TestLoadServerPartialFileKeepsDefaults(t *testing.T) {
	t.Setenv("KNOT_LISTEN", "")
	t.Setenv("KNOT_PG_DSN", "")
	path := writeTemp(t, `pg_dsn = "postgres://x"`)
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Listen != DefaultServer().Listen {
		t.Fatalf("未指定 listen 时应保留默认值，实际 %q", cfg.Listen)
	}
	if cfg.PGDSN != "postgres://x" {
		t.Fatalf("pg_dsn 未生效: %q", cfg.PGDSN)
	}
}

func TestLoadServerBadFile(t *testing.T) {
	if _, err := LoadServer(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("配置文件不存在应报错")
	}
	path := writeTemp(t, "listen = [not toml")
	if _, err := LoadServer(path); err == nil {
		t.Fatal("非法 TOML 应报错")
	}
}

// 客户端配置示例要能被解析，避免 config.example.toml 与结构体漂移。
func TestExampleConfigParses(t *testing.T) {
	cfg, err := LoadServer("../../config.example.toml")
	if err != nil {
		t.Fatalf("解析 config.example.toml: %v", err)
	}
	if cfg.Listen == "" || cfg.PGDSN == "" {
		t.Fatalf("示例配置缺少服务端字段: %+v", cfg)
	}
}
