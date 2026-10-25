package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

// openTestDB 返回一个已跑完迁移的内存库。
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed 写入一篇笔记、一个标签、一条绑定、一份正文缓存，返回 note id 与 tag id。
func seed(t *testing.T, db *DB) (noteID, tagID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	noteID = "11111111-1111-1111-1111-111111111111"
	tagID = "22222222-2222-2222-2222-222222222222"

	mustExec(t, tx, `INSERT INTO note_meta(id, title, created_at, modified_at, visibility)
		VALUES (?, ?, ?, ?, ?)`, noteID, "t", "2026-10-25T00:00:00Z", "2026-10-25T00:00:00Z", "private")
	mustExec(t, tx, `INSERT INTO tag_meta(id, name) VALUES (?, ?)`, tagID, "go")
	mustExec(t, tx, `INSERT INTO note_tag_meta(note_id, tag_id) VALUES (?, ?)`, noteID, tagID)
	mustExec(t, tx, `INSERT INTO note_content_cache(note_id, raw_md, cached_at, last_access_at)
		VALUES (?, ?, ?, ?)`, noteID, "# body", "2026-10-25T00:00:00Z", "2026-10-25T00:00:00Z")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return noteID, tagID
}

func mustExec(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func count(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	//nolint:gosec // 表名来自测试内部的固定常量
	if err := db.SQL().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestMigrateCreatesSchema 验证迁移建出全部表，且可重复执行（幂等）。
func TestMigrateCreatesSchema(t *testing.T) {
	db := openTestDB(t)

	want := []string{"note_meta", "tag_meta", "note_tag_meta", "note_content_cache", "sync_state"}
	for _, table := range want {
		var name string
		err := db.SQL().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("表 %s 不存在: %v", table, err)
		}
	}

	if err := Migrate(context.Background(), db.SQL()); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
}

// TestForeignKeysEnabled 验证每个连接都开启了外键，否则级联删除会静默失效。
func TestForeignKeysEnabled(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SQL().Exec(`INSERT INTO note_tag_meta(note_id, tag_id) VALUES ('missing', 'missing')`); err == nil {
		t.Fatal("插入孤儿绑定成功了，外键未生效")
	}
}

// TestCascadeDelete 验证删笔记与删标签都能级联清理绑定和正文缓存。
func TestCascadeDelete(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		delete  string
		arg     func(noteID, tagID string) string
		wantTag int
		wantTx  int
	}{
		{
			name:    "删笔记清理绑定与正文缓存，标签保留",
			delete:  `DELETE FROM note_meta WHERE id = ?`,
			arg:     func(noteID, _ string) string { return noteID },
			wantTag: 1,
			wantTx:  0,
		},
		{
			name:    "删标签清理绑定，笔记与正文缓存保留",
			delete:  `DELETE FROM tag_meta WHERE id = ?`,
			arg:     func(_, tagID string) string { return tagID },
			wantTag: 0,
			wantTx:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			noteID, tagID := seed(t, db)

			if _, err := db.SQL().ExecContext(ctx, tt.delete, tt.arg(noteID, tagID)); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if got := count(t, db, "note_tag_meta"); got != 0 {
				t.Errorf("note_tag_meta = %d, 期望 0", got)
			}
			if got := count(t, db, "tag_meta"); got != tt.wantTag {
				t.Errorf("tag_meta = %d, 期望 %d", got, tt.wantTag)
			}
			if got := count(t, db, "note_content_cache"); got != tt.wantTx {
				t.Errorf("note_content_cache = %d, 期望 %d", got, tt.wantTx)
			}
		})
	}
}

// TestDeleteNoteCascadeWithinTx 验证同一事务里删笔记后，绑定与正文缓存一并消失。
func TestDeleteNoteCascadeWithinTx(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	noteID, _ := seed(t, db)

	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_meta WHERE id = ?`, noteID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, table := range []string{"note_meta", "note_tag_meta", "note_content_cache"} {
		if got := count(t, db, table); got != 0 {
			t.Errorf("%s = %d, 期望 0", table, got)
		}
	}
}

// TestRollback 验证事务回滚后缓存保持原样，不留半成品。
func TestRollback(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	noteID, _ := seed(t, db)

	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_meta`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO note_meta(id, title, created_at, modified_at, visibility)
		VALUES (?, ?, ?, ?, 'bogus')`, "33333333-3333-3333-3333-333333333333", "t", "x", "y"); err == nil {
		t.Fatal("非法 visibility 写入成功了，CHECK 约束未生效")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if got := count(t, db, "note_meta"); got != 1 {
		t.Errorf("回滚后 note_meta = %d, 期望 1", got)
	}
	if got := count(t, db, "note_tag_meta"); got != 1 {
		t.Errorf("回滚后 note_tag_meta = %d, 期望 1", got)
	}
	var title string
	if err := db.SQL().QueryRow(`SELECT title FROM note_meta WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("回滚后读取原笔记: %v", err)
	}
}

// TestTagNameUnique 验证标签名唯一约束。
func TestTagNameUnique(t *testing.T) {
	db := openTestDB(t)
	_, tagID := seed(t, db)
	_, err := db.SQL().Exec(`INSERT INTO tag_meta(id, name) VALUES (?, 'go')`, "44444444-4444-4444-4444-444444444444")
	if err == nil {
		t.Fatalf("重复标签名写入成功（tag %s）", tagID)
	}
}
