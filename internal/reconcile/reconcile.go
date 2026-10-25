// Package reconcile 实现全量快照对账的纯函数（删除 / 重命名 / 解绑 / 新增）。
//
// 包名刻意不叫 sync：那会遮蔽标准库 sync。约束见 docs/ARCHITECTURE.md，
// 流程见 docs/SYNC_CACHE.md。
//
// 这里只计算「服务端快照 vs 本地缓存」的差异，不碰数据库；SQLite 落地在
// store/sqlite.ApplySnapshot，两者分开便于表驱动测试。
package reconcile

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
)

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

// LocalNote 是本地 note_meta 的一行（不含正文）。
type LocalNote struct {
	ID         uuid.UUID
	Title      string
	CreatedAt  string // RFC3339Nano，与 SQLite 存储格式一致
	ModifiedAt string
	Visibility string
	Tags       []string
}

// LocalTag 是本地 tag_meta 的一行。
type LocalTag struct {
	ID   uuid.UUID
	Name string
}

// Change 是一条待执行的本地变更。
type Change struct {
	Action Action
	// NoteID / TagID / TagName 按 Action 取值：
	//   insert/update：Note（整行 + 标签名集合）
	//   delete：NoteID
	//   unlink：NoteID + TagName
	//   tag 相关：TagID + TagName
	NoteID  uuid.UUID
	Note    *dto.Note
	TagID   uuid.UUID
	TagName string
}

// Plan 是对账结果：按固定顺序排列的变更列表。
//
// 顺序保证执行语义正确且可断言：
//  1. 标签 insert（先有 tag 才能建绑定）
//  2. 残留绑定 unlink（快照里笔记不再带的标签）
//  3. 笔记 insert / update（update 按快照重建绑定）
//  4. 笔记 delete（级联清 note_tag_meta / note_content_cache）
//  5. 标签 delete（必须在笔记与解绑之后，避免外键先拒）
type Plan struct {
	Changes []Change
	// Stats 便于 CLI 展示「新增 3 / 更新 1 / 删除 2」。
	Stats map[Action]int
}

// Diff 比较服务端快照与本地缓存，返回需要执行的变更。
//
// 关键语义：
//   - 服务端是唯一真值：本地多出来的笔记/标签/绑定一律删除；
//   - 标题、时间、可见性、标签集合任一不同即 update（正文缓存不动 —— 懒缓存
//     只在 show/edit 时刷新，元数据变化不代表本地正文失效）；
//   - 标签按名字匹配：服务端改名后本地旧名标签会变成「本地多出来」而被删除，
//     笔记同时 update 重建绑定，因此重命名天然被覆盖；
//   - 标签 id 以服务端为准（本地若同名不同 id，按 insert 改写）。
//
// 返回的 Changes 已按执行顺序排好（见 Plan 的顺序说明）。
func Diff(snapshot dto.SyncSnapshot, localNotes []LocalNote, localTags []LocalTag) Plan {
	plan := Plan{Stats: map[Action]int{}}

	serverTags := make(map[string]uuid.UUID, len(snapshot.Tags))
	for _, t := range snapshot.Tags {
		serverTags[t.Name] = t.ID
	}
	localTagByName := make(map[string]LocalTag, len(localTags))
	for _, t := range localTags {
		localTagByName[t.Name] = t
	}

	var tagInserts, noteInsertOrUpdate, unlinks, noteDeletes, tagDeletes []Change

	for _, name := range sortedKeys(serverTags) {
		id := serverTags[name]
		if lt, ok := localTagByName[name]; ok && lt.ID == id {
			continue
		}
		tagInserts = append(tagInserts, Change{Action: ActionInsert, TagID: id, TagName: name})
	}
	for _, lt := range localTags {
		if _, ok := serverTags[lt.Name]; !ok {
			tagDeletes = append(tagDeletes, Change{Action: ActionDelete, TagID: lt.ID, TagName: lt.Name})
		}
	}

	localByID := make(map[uuid.UUID]LocalNote, len(localNotes))
	for _, n := range localNotes {
		localByID[n.ID] = n
	}

	serverIDs := make(map[uuid.UUID]struct{}, len(snapshot.Notes))
	for _, n := range snapshot.Notes {
		serverIDs[n.ID] = struct{}{}
		ln, exists := localByID[n.ID]
		if !exists {
			noteInsertOrUpdate = append(noteInsertOrUpdate, Change{Action: ActionInsert, NoteID: n.ID, Note: &n})
			continue
		}
		// 标签名的 id 变了也算变化：本地那行的绑定会随旧标签删除而级联消失，
		// 必须重建，否则「名字没变但 id 换了」会静默丢绑定。
		tagIDsChanged := tagIDChanged(ln.Tags, n.Tags, localTagByName, serverTags)
		if !sameMeta(ln, n) || !sameTagSet(ln.Tags, n.Tags) || tagIDsChanged {
			noteInsertOrUpdate = append(noteInsertOrUpdate, Change{Action: ActionUpdate, NoteID: n.ID, Note: &n})
			// 快照里不再带的标签 → unlink（单独成条，便于执行层先删后建）。
			for _, gone := range missing(ln.Tags, n.Tags) {
				unlinks = append(unlinks, Change{Action: ActionUnlink, NoteID: n.ID, TagName: gone})
			}
		}
	}

	for _, ln := range localNotes {
		if _, ok := serverIDs[ln.ID]; !ok {
			noteDeletes = append(noteDeletes, Change{Action: ActionDelete, NoteID: ln.ID})
		}
	}

	// 固定执行顺序：标签 → 解绑 → 笔记 → 删笔记 → 删标签。
	//
	// 重命名时服务端复用同一个 tag id，本地会出现「同名不同名、id 相同」的两行
	// 冲突；applyChange 按 id 原地改名（而不是删了再插），所以插入必须
	// 早于删除，且删除只针对真正消失的标签。
	for _, group := range [][]Change{tagInserts, unlinks, noteInsertOrUpdate, noteDeletes, tagDeletes} {
		for _, c := range group {
			plan.add(c)
		}
	}
	return plan
}

// add 追加变更并累计统计。
func (p *Plan) add(c Change) {
	p.Changes = append(p.Changes, c)
	p.Stats[c.Action]++
}

// sameMeta 比较元数据字段。时间按字符串比较：两边都来自 API 的 RFC3339
// 输出，格式一致；本地写入时用同一函数格式化，避免解析误差。
func sameMeta(local LocalNote, server dto.Note) bool {
	return local.Title == server.Title &&
		local.Visibility == server.Visibility &&
		local.CreatedAt == FormatTime(server.CreatedAt) &&
		local.ModifiedAt == FormatTime(server.ModifiedAt)
}

// sameTagSet 比较标签集合（顺序无关；快照按名字排序，本地也从 DB 读排序后结果）。
func sameTagSet(local, server []string) bool {
	if len(local) != len(server) {
		return false
	}
	l := append([]string(nil), local...)
	s := append([]string(nil), server...)
	sort.Strings(l)
	sort.Strings(s)
	for i := range l {
		if l[i] != s[i] {
			return false
		}
	}
	return true
}

// missing 返回 in 有、keep 没有的元素（去重后按字典序）。
func missing(in, keep []string) []string {
	keepSet := make(map[string]struct{}, len(keep))
	for _, k := range keep {
		keepSet[k] = struct{}{}
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := keepSet[v]; ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]uuid.UUID) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IsEmpty 报告对账是否无事可做。
func (p Plan) IsEmpty() bool { return len(p.Changes) == 0 }

// FormatTime 是与 SQLite 存储格式一致的 RFC3339Nano（UTC）字符串。
// 对账比较与本地写入都必须用它，否则时区/精度差异会被误判成「有变化」。
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// tagIDChanged 报告笔记引用的标签里是否有「同名字但 id 变了」的（任一侧）。
//
// 本地 tag_meta 的 id 由服务端下发；服务端重建标签（例如误删后重加）会让同名
// 标签拿到新 id，本地旧行被删时绑定会级联消失，因此必须重建该笔记的绑定。
func tagIDChanged(localTags, serverTags []string, local map[string]LocalTag, server map[string]uuid.UUID) bool {
	for _, name := range serverTags {
		lt, ok := local[name]
		if !ok {
			continue // 新标签，绑定必然要新建
		}
		if sid, ok := server[name]; ok && lt.ID != sid {
			return true
		}
	}
	return false
}
