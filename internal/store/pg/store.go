// Package pg 是服务端的 PostgreSQL 存储实现（阶段 2）。
//
// sqlc 生成代码放 sqlcgen/ 子包，不手改；本包包装一层，handler 只认这里的接口。
// 约束（见 docs/ARCHITECTURE.md）：server 只依赖本包，不依赖 store/sqlite。
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // 注册 "pgx" database/sql 驱动，goose 需要
	"github.com/pressly/goose/v3"

	"knot/migrations"
)

// migrationsDir 是 migrations.FS 下的服务端子目录。
const migrationsDir = "pg"

// Store 是 PG 存储，查询方法在后续步骤基于 sqlcgen 补全。
type Store struct {
	pool *pgxpool.Pool
}

// Open 建立连接池并 ping 一次。
//
// schema 迁移不走这里，由 `knot-server migrate` 单独执行；
// 迁移失败不该影响已在运行的旧版本服务。
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("pg: 空 DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg: 创建连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg: 连接数据库: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Pool 暴露底层连接池，供 sqlc 生成的查询使用。
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping 检查数据库是否可达（healthz 用）。
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close 关闭连接池。
func (s *Store) Close() { s.pool.Close() }

// Migrate 执行 migrations/pg/ 下所有未应用的迁移。
//
// 迁移文件内嵌在二进制里（//go:embed），服务器不需要装 goose 命令。
// 0002 依赖 pg_trgm 扩展，需事先由管理员 CREATE EXTENSION pg_trgm。
func Migrate(ctx context.Context, dsn string) error {
	if dsn == "" {
		return fmt.Errorf("pg: 空 DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("pg: 打开连接: %w", err)
	}
	defer func() { _ = db.Close() }()

	// goose 在 PG 上会取会话级咨询锁，避免多个实例并发迁移。
	sub, err := fs.Sub(migrations.FS, migrationsDir)
	if err != nil {
		return fmt.Errorf("pg: 读取内嵌迁移: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		return fmt.Errorf("pg: 初始化迁移: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("pg: 执行迁移: %w", err)
	}
	return nil
}
