// Package reconcile 实现全量快照对账的纯函数（删除 / 重命名 / 解绑 / 新增）。
//
// 包名刻意不叫 sync：那会遮蔽标准库 sync。约束见 docs/ARCHITECTURE.md，
// 流程见 docs/SYNC_CACHE.md。
package reconcile

// Action 是对账后要对本地 SQLite 执行的动作类型。
type Action string

const (
	// ActionInsert 服务端有、本地没有 → 插入。
	ActionInsert Action = "insert"
	// ActionUpdate 两边都有但元数据不同 → 更新（含标签重命名）。
	ActionUpdate Action = "update"
	// ActionDelete 本地有、服务端没有 → 删除。
	ActionDelete Action = "delete"
	// ActionUnlink 笔记仍在但标签解绑 → 删关联行。
	ActionUnlink Action = "unlink"
)
