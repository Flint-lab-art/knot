// Package migrations 内嵌服务端与本地缓存的 goose 迁移文件，供
// knot-server migrate 与 SQLite 启动迁移使用。
//
// 两端各自 //go:embed 需要的子目录，避免把对方的 SQL 打进自己的二进制。
// 迁移只加不删；文件名用 goose 的 `0001_init.sql` 形式。
package migrations

import "embed"

// FS 收录 PG 与 SQLite 两套迁移，子目录分别对应：
//   - pg/     服务端，由 knot-server migrate 执行
//   - sqlite/ 本地缓存，由 CLI 启动时执行
//
//go:embed pg/*.sql sqlite/*.sql
var FS embed.FS
