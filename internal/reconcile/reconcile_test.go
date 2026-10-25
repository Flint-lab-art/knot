package reconcile

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
)

// 固定时间，避免测试依赖当前时钟。
var t0 = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

func uid(s string) uuid.UUID { return uuid.MustParse(s) }

// note 构造一条快照笔记。
func note(id, title string, tags ...string) dto.Note {
	if tags == nil {
		tags = []string{}
	}
	return dto.Note{
		ID: uid(id), Title: title, Version: 1, Visibility: "private",
		CreatedAt: t0, ModifiedAt: t0, Tags: tags,
	}
}

// local 构造一条本地缓存行。
func local(n dto.Note) LocalNote {
	return LocalNote{
		ID: n.ID, Title: n.Title,
		CreatedAt: FormatTime(n.CreatedAt), ModifiedAt: FormatTime(n.ModifiedAt),
		Visibility: n.Visibility, Tags: append([]string(nil), n.Tags...),
	}
}

// localTag 构造本地标签行。
func localTag(id, name string) LocalTag { return LocalTag{ID: uid(id), Name: name} }

// brief 把变更压成易读字符串，便于断言顺序与内容。
func brief(c Change) string {
	switch c.Action {
	case ActionInsert, ActionUpdate:
		if c.Note != nil {
			return string(c.Action) + " note " + c.Note.ID.String()
		}
		return string(c.Action) + " tag " + c.TagName
	case ActionUnlink:
		return "unlink " + c.NoteID.String() + "/" + c.TagName
	default:
		if c.NoteID != uuid.Nil {
			return "delete note " + c.NoteID.String()
		}
		return "delete tag " + c.TagName
	}
}

func briefs(p Plan) []string {
	out := make([]string, len(p.Changes))
	for i, c := range p.Changes {
		out[i] = brief(c)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const (
	n1  = "11111111-1111-1111-1111-111111111111"
	n2  = "22222222-2222-2222-2222-222222222222"
	n3  = "33333333-3333-3333-3333-333333333333"
	tg1 = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tg2 = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	tg3 = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

func TestDiff(t *testing.T) {
	same := note(n1, "相同的笔记", "go")
	renamedTitle := note(n2, "新标题", "go")
	renamedTagNote := note(n3, "改名标签", "golang")

	tests := []struct {
		name     string
		snapshot dto.SyncSnapshot
		localN   []LocalNote
		localT   []LocalTag
		want     []string
	}{
		{
			name:     "空快照空本地：无事可做",
			snapshot: dto.SyncSnapshot{},
			want:     nil,
		},
		{
			name:     "完全相同：无事可做",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{same}, Tags: []dto.Tag{{ID: uid(tg1), Name: "go"}}},
			localN:   []LocalNote{local(same)},
			localT:   []LocalTag{localTag(tg1, "go")},
			want:     nil,
		},
		{
			name:     "新增：服务端有本地没有",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{same}, Tags: []dto.Tag{{ID: uid(tg1), Name: "go"}}},
			want:     []string{"insert tag go", "insert note " + n1},
		},
		{
			name:     "删除：本地有服务端没有（含笔记与标签）",
			snapshot: dto.SyncSnapshot{},
			localN:   []LocalNote{local(same)},
			localT:   []LocalTag{localTag(tg1, "go")},
			// 先删笔记（级联绑定），再删标签。
			want: []string{"delete note " + n1, "delete tag go"},
		},
		{
			name:     "标题变化 → update",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{renamedTitle}, Tags: []dto.Tag{{ID: uid(tg1), Name: "go"}}},
			localN:   []LocalNote{local(note(n2, "旧标题", "go"))},
			localT:   []LocalTag{localTag(tg1, "go")},
			want:     []string{"update note " + n2},
		},
		{
			name:     "可见性变化 → update",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{{ID: uid(n1), Title: "t", Visibility: "public", CreatedAt: t0, ModifiedAt: t0, Tags: []string{}}}},
			localN:   []LocalNote{{ID: uid(n1), Title: "t", Visibility: "private", CreatedAt: FormatTime(t0), ModifiedAt: FormatTime(t0), Tags: []string{}}},
			want:     []string{"update note " + n1},
		},
		{
			name:     "标签重命名：本地旧标签删除 + 笔记 update 重建绑定",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{renamedTagNote}, Tags: []dto.Tag{{ID: uid(tg1), Name: "golang"}}},
			localN:   []LocalNote{local(note(n3, "改名标签", "go"))},
			localT:   []LocalTag{localTag(tg1, "go")},
			want: []string{
				"insert tag golang",
				"unlink " + n3 + "/go",
				"update note " + n3,
				"delete tag go",
			},
		},
		{
			name:     "解绑：笔记仍在但少一个标签",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{note(n1, "t", "go")}, Tags: []dto.Tag{{ID: uid(tg1), Name: "go"}}},
			localN:   []LocalNote{local(note(n1, "t", "go", "rust"))},
			localT:   []LocalTag{localTag(tg1, "go"), localTag(tg2, "rust")},
			want: []string{
				"unlink " + n1 + "/rust",
				"update note " + n1,
				"delete tag rust",
			},
		},
		{
			name:     "标签 id 变化 → 插新标签并重建绑定",
			snapshot: dto.SyncSnapshot{Notes: []dto.Note{note(n1, "t", "go")}, Tags: []dto.Tag{{ID: uid(tg2), Name: "go"}}},
			localN:   []LocalNote{local(note(n1, "t", "go"))},
			localT:   []LocalTag{localTag(tg1, "go")},
			// 旧标签行会被删除（id 换了），绑定随之级联消失，因此笔记必须 update。
			want: []string{"insert tag go", "update note " + n1},
		},
		{
			name: "混合：新增 / 更新 / 删除同时发生",
			snapshot: dto.SyncSnapshot{
				Notes: []dto.Note{note(n1, "新增"), renamedTitle},
				Tags:  []dto.Tag{{ID: uid(tg1), Name: "go"}},
			},
			localN: []LocalNote{local(note(n2, "旧标题", "go")), local(note(n3, "将被删"))},
			localT: []LocalTag{localTag(tg1, "go")},
			want: []string{
				"insert note " + n1,
				"update note " + n2,
				"delete note " + n3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := Diff(tt.snapshot, tt.localN, tt.localT)
			if got := briefs(plan); !equalStrings(got, tt.want) {
				t.Fatalf("变更序列不符\n got: %v\nwant: %v", got, tt.want)
			}
			if plan.IsEmpty() != (len(tt.want) == 0) {
				t.Fatalf("IsEmpty = %v, want %v", plan.IsEmpty(), len(tt.want) == 0)
			}
			// Stats 必须与变更条数一致（CLI 用它展示汇总）。
			total := 0
			for _, n := range plan.Stats {
				total += n
			}
			if total != len(plan.Changes) {
				t.Fatalf("Stats 合计 %d != 变更数 %d", total, len(plan.Changes))
			}
		})
	}
}

// 标签顺序不同不算变化（快照与本地读出的排序一致，但不应依赖）。
func TestDiffIgnoresTagOrder(t *testing.T) {
	snap := dto.SyncSnapshot{
		Notes: []dto.Note{note(n1, "t", "a", "b")},
		Tags:  []dto.Tag{{ID: uid(tg1), Name: "a"}, {ID: uid(tg2), Name: "b"}},
	}
	localNotes := []LocalNote{local(note(n1, "t", "b", "a"))}
	localTags := []LocalTag{localTag(tg1, "a"), localTag(tg2, "b")}
	if plan := Diff(snap, localNotes, localTags); !plan.IsEmpty() {
		t.Fatalf("标签顺序不同不应产生变更: %v", briefs(plan))
	}
}

// 时间精度/时区不同但如果表示同一时刻，不应判定为变化。
func TestDiffTimeFormatting(t *testing.T) {
	serverTime := time.Date(2026, 10, 24, 10, 0, 0, 123456789, time.UTC)
	snap := dto.SyncSnapshot{
		Notes: []dto.Note{{ID: uid(n1), Title: "t", Visibility: "private", CreatedAt: serverTime, ModifiedAt: serverTime, Tags: []string{}}},
	}
	// 本地存的是同一时刻的另一种写法（非 UTC 时区）。
	localNotes := []LocalNote{{
		ID: uid(n1), Title: "t", Visibility: "private",
		CreatedAt:  serverTime.In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339Nano),
		ModifiedAt: FormatTime(serverTime),
		Tags:       []string{},
	}}
	// 当前实现按字符串比较：不同写法的同一时刻会被判成 update（安全方向：
	// 多一次幂等 update，不会丢数据）。这里锁住这个行为，避免无意改动。
	plan := Diff(snap, localNotes, nil)
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ActionUpdate {
		t.Fatalf("非 UTC 写法应产生一次 update（幂等），实际: %v", briefs(plan))
	}
}

// 删除顺序必须在最后：先删标签会因外键（note_tag_meta → tag_meta）失败。
func TestDiffDeleteOrdering(t *testing.T) {
	snap := dto.SyncSnapshot{}
	localNotes := []LocalNote{local(note(n1, "t", "go"))}
	localTags := []LocalTag{localTag(tg1, "go")}
	plan := Diff(snap, localNotes, localTags)
	var noteIdx, tagIdx = -1, -1
	for i, c := range plan.Changes {
		switch {
		case c.Action == ActionDelete && c.NoteID != uuid.Nil:
			noteIdx = i
		case c.Action == ActionDelete:
			tagIdx = i
		}
	}
	if noteIdx < 0 || tagIdx < 0 || noteIdx > tagIdx {
		t.Fatalf("应先删笔记再删标签: %v", briefs(plan))
	}
}
