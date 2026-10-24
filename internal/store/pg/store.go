// Package pg 是服务端的 PostgreSQL 存储实现（阶段 2）。
//
// sqlc 生成代码放 sqlcgen/ 子包，不手改；本包包装一层，handler 只认这里的接口。
// 约束（见 docs/ARCHITECTURE.md）：server 只依赖本包，不依赖 store/sqlite。
package pg

import "errors"

// ErrNotImplemented 表示 PG 存储尚未实现（阶段 2）。
var ErrNotImplemented = errors.New("store/pg 尚未实现")
