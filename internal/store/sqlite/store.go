// Package sqlite 是本地 SQLite 缓存实现（阶段 2）。
//
// 数据可重建：真值在服务端 PostgreSQL，本地不回写。必须开启
// PRAGMA foreign_keys = ON；迁移用 goose + //go:embed（migrations/sqlite）。
// 约束（见 docs/ARCHITECTURE.md）：cli / tui 只依赖本包与 client。
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，注册 "sqlite"

	"knot/migrations"
)

// 驱动与目录名只在这里出现，避免各处写字符串。
const (
	driverName    = "sqlite"
	migrationsDir = "sqlite" // migrations.FS 下的子目录
)

// DB 是本地缓存库。
type DB struct {
	db *sql.DB
}

// Open 打开（必要时创建）本地缓存库并执行未应用的迁移。
//
// path 为 ":memory:" 时得到一次性的内存库，仅用于测试；此时连接池
// 固定为单连接，否则每个连接各自拥有一个空库。
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite: 空路径")
	}
	inMemory := isMemoryPath(path)

	dsn := "file:" + path
	// 外键必须在每个连接上开启；_pragma 由 modernc 驱动在连接建立时执行。
	pragmas := "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	if inMemory {
		// shared cache 让同一进程内的连接看到同一份内存库。
		dsn = "file::memory:?cache=shared&" + pragmas
	} else {
		dsn += "?" + pragmas
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 打开 %s: %w", path, err)
	}
	if inMemory {
		db.SetMaxOpenConns(1)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: 连接 %s: %w", path, err)
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Migrate 执行 migrations/ 下所有未应用的迁移（CLI 启动时自动调用）。
func Migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrations.FS, migrationsDir)
	if err != nil {
		return fmt.Errorf("sqlite: 读取内嵌迁移: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("sqlite: 初始化迁移: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("sqlite: 执行迁移: %w", err)
	}
	return nil
}

// Close 关闭底层连接。
func (d *DB) Close() error { return d.db.Close() }

// SQL 暴露底层连接，供同包与测试使用。
func (d *DB) SQL() *sql.DB { return d.db }

func isMemoryPath(path string) bool {
	return path == ":memory:" || filepath.Base(path) == ":memory:"
}
