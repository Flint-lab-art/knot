package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/reconcile"
)

var (
	syncAt = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	nID    = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	n2ID   = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	tgID   = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tg2ID  = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
)

func snapNote(id uuid.UUID, title string, tags ...string) dto.Note {
	if tags == nil {
		tags = []string{}
	}
	return dto.Note{
		ID: id, Title: title, Version: 1, Visibility: "private",
		CreatedAt: syncAt, ModifiedAt: syncAt, Tags: tags,
	}
}

func snapOf(notes []dto.Note, tags []dto.Tag) dto.SyncSnapshot {
	return dto.SyncSnapshot{Notes: notes, Tags: tags, SyncedAt: syncAt}
}

func tag(id uuid.UUID, name string) dto.Tag { return dto.Tag{ID: id, Name: name} }

func noteCount(t *testing.T, db *DB) int { t.Helper(); return count(t, db, "note_meta") }

// TestApplySnapshotInsert 首次同步：建标签、建笔记、建绑定、写 last_sync_at。
func TestApplySnapshotInsert(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	plan, err := db.ApplySnapshot(ctx, snapOf(
		[]dto.Note{snapNote(nID, "标题", "go")},
		[]dto.Tag{tag(tgID, "go")},
	), syncAt)
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if plan.Stats[reconcile.ActionInsert] != 2 {
		t.Fatalf("应插入标签+笔记共 2 条: %v", plan.Stats)
	}
	if got := noteCount(t, db); got != 1 {
		t.Fatalf("note_meta = %d", got)
	}
	if got := count(t, db, "note_tag_meta"); got != 1 {
		t.Fatalf("note_tag_meta = %d", got)
	}
	got, err := db.GetNote(ctx, nID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.Title != "标题" || len(got.Tags) != 1 || got.Tags[0] != "go" {
		t.Fatalf("本地笔记不符: %+v", got)
	}
	last, ok, err := db.LastSyncAt(ctx)
	if err != nil || !ok || !last.Equal(syncAt) {
		t.Fatalf("last_sync_at = %v %v %v", last, ok, err)
	}
}

// 第二次同步内容相同 → 无变更；LON 无变化不应反复写库。
func TestApplySnapshotIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	snap := snapOf([]dto.Note{snapNote(nID, "标题", "go")}, []dto.Tag{tag(tgID, "go")})

	if _, err := db.ApplySnapshot(ctx, snap, syncAt); err != nil {
		t.Fatalf("首次同步: %v", err)
	}
	plan, err := db.ApplySnapshot(ctx, snap, syncAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("二次同步: %v", err)
	}
	if !plan.IsEmpty() {
		t.Fatalf("相同快照不应产生变更: %+v", plan.Changes)
	}
}

// 删除：服务端消失的笔记与标签在本地清理，正文缓存随级联消失。
func TestApplySnapshotDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "a", "go")}, []dto.Tag{tag(tgID, "go")}), syncAt); err != nil {
		t.Fatalf("首次同步: %v", err)
	}
	if err := db.SaveContent(ctx, nID, "正文", syncAt); err != nil {
		t.Fatalf("SaveContent: %v", err)
	}

	if _, err := db.ApplySnapshot(ctx, snapOf(nil, nil), syncAt); err != nil {
		t.Fatalf("删除同步: %v", err)
	}
	for _, table := range []string{"note_meta", "tag_meta", "note_tag_meta", "note_content_cache"} {
		if got := count(t, db, table); got != 0 {
			t.Errorf("%s = %d, 期望 0", table, got)
		}
	}
}

// 标签重命名：旧名消失、新名出现，笔记绑定跟着换。
func TestApplySnapshotTagRename(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "a", "go")}, []dto.Tag{tag(tgID, "go")}), syncAt); err != nil {
		t.Fatalf("首次同步: %v", err)
	}

	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "a", "golang")}, []dto.Tag{tag(tgID, "golang")}), syncAt); err != nil {
		t.Fatalf("重命名同步: %v", err)
	}
	got, err := db.GetNote(ctx, nID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "golang" {
		t.Fatalf("标签未更新: %v", got.Tags)
	}
	if n := count(t, db, "tag_meta"); n != 1 {
		t.Fatalf("tag_meta = %d，旧标签应被清理", n)
	}
}

// 解绑：笔记保留，但少了一个标签。
func TestApplySnapshotUnbind(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.ApplySnapshot(ctx, snapOf(
		[]dto.Note{snapNote(nID, "a", "go", "rust")},
		[]dto.Tag{tag(tgID, "go"), tag(tg2ID, "rust")},
	), syncAt); err != nil {
		t.Fatalf("首次同步: %v", err)
	}

	if _, err := db.ApplySnapshot(ctx, snapOf(
		[]dto.Note{snapNote(nID, "a", "go")},
		[]dto.Tag{tag(tgID, "go")},
	), syncAt); err != nil {
		t.Fatalf("解绑同步: %v", err)
	}
	got, err := db.GetNote(ctx, nID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "go" {
		t.Fatalf("绑定未清理: %v", got.Tags)
	}
}

// 失败回滚：快照里引用了未声明标签（服务端异常），本地不得留下半成品。
func TestApplySnapshotRollback(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	// 先有一个本地笔记。
	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "原有", "go")}, []dto.Tag{tag(tgID, "go")}), syncAt); err != nil {
		t.Fatalf("首次同步: %v", err)
	}

	// 快照里笔记引用了标签 "missing"，但 Tags 列表没给出 → 执行到一半失败。
	bad := snapOf([]dto.Note{snapNote(n2ID, "坏的", "missing")}, []dto.Tag{})
	_, err := db.ApplySnapshot(ctx, bad, syncAt.Add(time.Hour))
	if err == nil {
		t.Fatal("应因缺标签而失败")
	}

	// 原有数据完好，新笔记没有落进来。
	if got := noteCount(t, db); got != 1 {
		t.Fatalf("回滚后 note_meta = %d, 期望 1", got)
	}
	if _, err := db.GetNote(ctx, n2ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("失败事务不应留下新笔记: %v", err)
	}
	last, _, err := db.LastSyncAt(ctx)
	if err != nil {
		t.Fatalf("LastSyncAt: %v", err)
	}
	if !last.Equal(syncAt) {
		t.Fatalf("失败不应推进 last_sync_at: %v", last)
	}
}

// 本地写后同步：UpsertNote 更新的标签集合，DeleteNoteCached 级联清正文。
func TestLocalUpsertAndDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	n := snapNote(nID, "本地", "go", "rust")
	if err := db.UpsertNote(ctx, n); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	if err := db.SaveContent(ctx, nID, "正文", syncAt); err != nil {
		t.Fatalf("SaveContent: %v", err)
	}

	// 再次 upsert 不同标签集合：旧绑定应被替换，不累积。
	n.Tags = []string{"go"}
	if err := db.UpsertNote(ctx, n); err != nil {
		t.Fatalf("UpsertNote 二次: %v", err)
	}
	got, err := db.GetNote(ctx, nID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "go" {
		t.Fatalf("标签未替换: %v", got.Tags)
	}

	if raw, ok, err := db.LoadContent(ctx, nID, syncAt); err != nil || !ok || raw != "正文" {
		t.Fatalf("LoadContent = %q %v %v", raw, ok, err)
	}

	if err := db.DeleteNoteCached(ctx, nID); err != nil {
		t.Fatalf("DeleteNoteCached: %v", err)
	}
	if _, err := db.GetNote(ctx, nID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound: %v", err)
	}
	if _, ok, _ := db.LoadContent(ctx, nID, syncAt); ok {
		t.Fatal("删笔记后正文缓存应级联删除")
	}
	// 删除不存在的笔记不报错（幂等）。
	if err := db.DeleteNoteCached(ctx, nID); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
}

// ListNotes 支持按标签过滤，且带出标签。
func TestListNotes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, n := range []dto.Note{
		snapNote(nID, "a", "go"),
		snapNote(n2ID, "b", "rust"),
	} {
		if err := db.UpsertNote(ctx, n); err != nil {
			t.Fatalf("UpsertNote: %v", err)
		}
	}
	all, err := db.ListNotes(ctx, "", 0)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("list = %d", len(all))
	}
	only, err := db.ListNotes(ctx, "rust", 0)
	if err != nil {
		t.Fatalf("ListNotes(tag): %v", err)
	}
	if len(only) != 1 || only[0].Title != "b" || len(only[0].Tags) != 1 {
		t.Fatalf("按标签过滤失败: %+v", only)
	}
}

// ClearAll / ClearContent 与 Stats。
func TestCacheClearAndStats(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "a", "go")}, []dto.Tag{tag(tgID, "go")}), syncAt); err != nil {
		t.Fatalf("同步: %v", err)
	}
	if err := db.SaveContent(ctx, nID, "正文", syncAt); err != nil {
		t.Fatalf("SaveContent: %v", err)
	}

	s, err := db.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.Notes != 1 || s.Tags != 1 || s.Bindings != 1 || s.Contents != 1 || !s.HasSynced {
		t.Fatalf("统计不符: %+v", s)
	}

	if n, err := db.ClearContent(ctx); err != nil || n != 1 {
		t.Fatalf("ClearContent = %d %v", n, err)
	}
	if s, _ := db.Stats(ctx); s.Contents != 0 || s.Notes != 1 {
		t.Fatalf("清正文后元数据应保留: %+v", s)
	}

	if err := db.ClearAll(ctx); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if s, _ := db.Stats(ctx); s.Notes|s.Tags|s.Bindings|s.Contents != 0 || s.HasSynced {
		t.Fatalf("清空后应为空: %+v", s)
	}
}

// 服务端同名标签换了 id：本地要按新 id 改写，且绑定仍指向正确的标签。
func TestApplySnapshotTagIDChange(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "a", "go")}, []dto.Tag{tag(tgID, "go")}), syncAt); err != nil {
		t.Fatalf("首次同步: %v", err)
	}
	if _, err := db.ApplySnapshot(ctx,
		snapOf([]dto.Note{snapNote(nID, "a", "go")}, []dto.Tag{tag(tg2ID, "go")}), syncAt); err != nil {
		t.Fatalf("换 id 同步: %v", err)
	}
	if n := count(t, db, "tag_meta"); n != 1 {
		t.Fatalf("tag_meta = %d, 期望 1（同名标签不应重复）", n)
	}
	var gotID string
	if err := db.SQL().QueryRow(`SELECT id FROM tag_meta WHERE name = 'go'`).Scan(&gotID); err != nil {
		t.Fatalf("查标签: %v", err)
	}
	if gotID != tg2ID.String() {
		t.Fatalf("标签 id = %s, 期望 %s", gotID, tg2ID)
	}
	got, err := db.GetNote(ctx, nID)
	if err != nil || len(got.Tags) != 1 {
		t.Fatalf("绑定应仍在: %+v %v", got.Tags, err)
	}
}
