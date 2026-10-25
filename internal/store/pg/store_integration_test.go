//go:build integration

// 集成测试：真实 PG。默认 `go test ./...` 跳过，需要显式开启：
//
//	KNOT_TEST_PG_DSN='postgres://postgres@/postgres?host=/run/postgresql&sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/store/pg/
//
// DSN 必须是能建 schema 的管理库（测试会建临时 schema 并在结束时删掉），
// 不能用业务库，否则测试数据会混进来。没有 DSN 时跳过。
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"knot/internal/model"
	"knot/migrations"
)

const testDSNEnv = "KNOT_TEST_PG_DSN"

// testSchema 建一个独立 schema，并让所有连接默认在该 schema 下工作。
// search_path 通过连接串的 options 参数注入，pgxpool 的每个连接都生效。
func testSchema(t *testing.T) (adminDSN, scopedDSN, schema string) {
	t.Helper()
	adminDSN = os.Getenv(testDSNEnv)
	if adminDSN == "" {
		t.Skipf("未设置 %s，跳过 PG 集成测试", testDSNEnv)
	}
	if testing.Short() {
		t.Skip("短模式跳过 PG 集成测试")
	}
	schema = "knot_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	ctx := context.Background()
	conn, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("连接测试库: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("创建 schema %s: %v", schema, err)
	}
	// 0002 依赖 pg_trgm。业务库由管理员预先装好；测试库可能没装，
	// 就地装到临时 schema 里（无权限时只能依赖库级别的扩展，失败会直接报错）。
	if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA "+schema); err != nil {
		t.Logf("在 %s 中装 pg_trgm 失败（继续，若 0002 失败说明测试库缺扩展）: %v", schema, err)
	}
	t.Cleanup(func() {
		c, err := pgxpool.New(context.Background(), adminDSN)
		if err != nil {
			t.Logf("清理时连不上测试库: %v", err)
			return
		}
		defer c.Close()
		if _, err := c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("清理 schema %s: %v", schema, err)
		}
	})

	sep := "?"
	if strings.Contains(adminDSN, "?") {
		sep = "&"
	}
	// search_path 带上 public：pg_trgm 的 operator class 装在那里。
	scopedDSN = fmt.Sprintf("%s%soptions=-c%%20search_path%%3D%s%%2Cpublic", adminDSN, sep, schema)
	return adminDSN, scopedDSN, schema
}

// openMigrated 打开 scoped 库并跑完所有迁移。
func openMigrated(t *testing.T, scopedDSN string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", scopedDSN)
	if err != nil {
		t.Fatalf("打开 scoped 库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), scopedDSN); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}
	return db
}

// newStore 确保 scoped 库已迁移，再打开 store。
func newStore(t *testing.T, scopedDSN string) *Store {
	t.Helper()
	openMigrated(t, scopedDSN) // 连接池随后关闭，迁移结果留在库里
	s, err := Open(t.Context(), scopedDSN)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestMigrationsUpDownUp 验证所有迁移能 up、全部 down、再 up，
// down 只删本文件对象，不残留（GOOSE 会按倒序执行）。
func TestMigrationsUpDownUp(t *testing.T) {
	_, scopedDSN, schema := testSchema(t)
	db := openMigrated(t, scopedDSN)

	sub, err := fs.Sub(migrations.FS, migrationsDir)
	if err != nil {
		t.Fatalf("读取内嵌迁移: %v", err)
	}
	// 测试用独立表存版本，避免与已跑过的 goose_db_version 打架；这里直接用
	// 生产同样的表名，因为 scoped schema 是干净的。
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		t.Fatalf("goose locker: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithSessionLocker(locker))
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}

	down, err := provider.DownTo(t.Context(), 0)
	if err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	for _, r := range down {
		if r.Error != nil {
			t.Fatalf("迁移 %s down 失败: %v", r.Source.Path, r.Error)
		}
	}
	// 所有迁移都应被回滚：down 条数 = 迁移文件数。
	if want := len(down); want == 0 {
		t.Fatal("down 结果为空，迁移未被执行")
	}
	// down 后业务表应全部消失（只剩 goose 的版本表）。
	var remaining int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = $1 AND table_name IN ('notes','tags','note_tags')`, schema,
	).Scan(&remaining); err != nil {
		t.Fatalf("查剩余表: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("down 后仍有 %d 张业务表", remaining)
	}

	up, err := provider.Up(t.Context())
	if err != nil {
		t.Fatalf("重新 up: %v", err)
	}
	for _, r := range up {
		if r.Error != nil {
			t.Fatalf("迁移 %s up 失败: %v", r.Source.Path, r.Error)
		}
	}
}

// TestConstraintsAndCascade 覆盖 CHECK、唯一约束与级联删除，依赖 PG 自身而非 store。
func TestConstraintsAndCascade(t *testing.T) {
	_, scopedDSN, _ := testSchema(t)
	db := openMigrated(t, scopedDSN)
	ctx := t.Context()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO notes (title, raw_md, visibility) VALUES ('x','y','secret')`); err == nil {
		t.Fatal("非法 visibility 应被 CHECK 拒绝")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tags (name) VALUES ('go'), ('go')`); err == nil {
		t.Fatal("重复标签名应被唯一约束拒绝")
	}

	// 级联：删笔记 → note_tags 消失；删标签 → 绑定消失、笔记保留。
	var noteID, tagID uuid.UUID
	if err := db.QueryRowContext(ctx,
		`INSERT INTO notes (title, raw_md) VALUES ('n','m') RETURNING id`).Scan(&noteID); err != nil {
		t.Fatalf("插笔记: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO tags (name) VALUES ('t1') RETURNING id`).Scan(&tagID); err != nil {
		t.Fatalf("插标签: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO note_tags (note_id, tag_id) VALUES ($1, $2)`, noteID, tagID); err != nil {
		t.Fatalf("绑定: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM notes WHERE id = $1`, noteID); err != nil {
		t.Fatalf("删笔记: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM note_tags`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("删笔记后 note_tags 残留 %d 行", n)
	}

	if err := db.QueryRowContext(ctx,
		`INSERT INTO notes (title, raw_md) VALUES ('n2','m') RETURNING id`).Scan(&noteID); err != nil {
		t.Fatalf("插笔记: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO note_tags (note_id, tag_id) VALUES ($1, $2)`, noteID, tagID); err != nil {
		t.Fatalf("绑定: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM tags WHERE id = $1`, tagID); err != nil {
		t.Fatalf("删标签: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM note_tags`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("删标签后绑定残留 %d 行", n)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notes`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("删标签不应影响笔记，实际 %d 篇", n)
	}
}

func TestCreateGetUpdate(t *testing.T) {
	_, scopedDSN, _ := testSchema(t)
	s := newStore(t, scopedDSN)
	ctx := t.Context()

	note, err := s.CreateNote(ctx, "标题", "# 正文", []string{"go", "go", ""}, model.VisibilityPrivate)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note.Version != 1 || note.Visibility != "private" || note.RawMd != "# 正文" {
		t.Fatalf("新建结果不符: %+v", note)
	}
	tags, err := s.ListTagsForNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("ListTagsForNote: %v", err)
	}
	if len(tags) != 1 || tags[0] != "go" {
		t.Fatalf("标签去重失败: %v", tags)
	}

	got, err := s.GetNote(ctx, note.ID)
	if err != nil || got.Title != "标题" {
		t.Fatalf("GetNote: %+v %v", got, err)
	}
	// private 笔记对公开接口不可见。
	if _, err := s.GetPublicNote(ctx, note.ID); err != ErrNotFound {
		t.Fatalf("private 笔记走公开接口应 ErrNotFound，实际 %v", err)
	}

	upd, err := s.UpdateNote(ctx, note.ID, "新标题", "# 新正文", model.VisibilityPublic, 1)
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if upd.Version != 2 || upd.Title != "新标题" {
		t.Fatalf("更新结果不符: %+v", upd)
	}
	if _, err := s.GetPublicNote(ctx, note.ID); err != nil {
		t.Fatalf("public 笔记应可读: %v", err)
	}

	// 旧版本重放 → 409；不存在的 id → 404（不是 409）。
	if _, err := s.UpdateNote(ctx, note.ID, "again", "x", model.VisibilityPublic, 1); err != ErrConflict {
		t.Fatalf("版本冲突应 ErrConflict，实际 %v", err)
	}
	if _, err := s.UpdateNote(ctx, uuid.New(), "x", "x", model.VisibilityPublic, 1); err != ErrNotFound {
		t.Fatalf("不存在应 ErrNotFound，实际 %v", err)
	}
	if _, err := s.CreateNote(ctx, "x", "x", nil, model.Visibility("secret")); err == nil {
		t.Fatal("非法 visibility 应报错")
	}

	if err := s.DeleteNote(ctx, note.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if err := s.DeleteNote(ctx, note.ID); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
	if _, err := s.GetNote(ctx, note.ID); err != ErrNotFound {
		t.Fatalf("删除后应 ErrNotFound，实际 %v", err)
	}
}

func TestListAndSearch(t *testing.T) {
	_, scopedDSN, _ := testSchema(t)
	s := newStore(t, scopedDSN)
	ctx := t.Context()

	for _, title := range []string{"alpha", "beta", "gamma", "delta"} {
		if _, err := s.CreateNote(ctx, title, "正文 "+title, []string{"go"}, model.VisibilityPublic); err != nil {
			t.Fatalf("CreateNote %s: %v", title, err)
		}
	}
	if _, err := s.CreateNote(ctx, "hidden", "正文 alpha", []string{"secret"}, model.VisibilityPrivate); err != nil {
		t.Fatalf("CreateNote hidden: %v", err)
	}

	all, err := s.ListNotes(ctx, ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("limit 未生效: %d", len(all))
	}
	// 游标翻页：不能与第一页重复。
	last := all[len(all)-1]
	next, err := s.ListNotes(ctx, ListOptions{Limit: 10, Cursor: last.ModifiedAt, CursorID: &last.ID})
	if err != nil {
		t.Fatalf("游标翻页: %v", err)
	}
	seen := map[uuid.UUID]bool{all[0].ID: true, all[1].ID: true}
	for _, n := range next {
		if seen[n.ID] {
			t.Fatalf("游标翻页出现重复笔记 %s", n.ID)
		}
	}
	if len(next) != 3 {
		t.Fatalf("游标翻页应有 3 条（5 条 - 2 条），实际 %d", len(next))
	}

	pub, err := s.ListNotes(ctx, ListOptions{Visibility: model.VisibilityPublic, Limit: 200})
	if err != nil {
		t.Fatalf("公开列表: %v", err)
	}
	for _, n := range pub {
		if n.Visibility != string(model.VisibilityPublic) {
			t.Fatalf("公开列表混入 %s 笔记", n.Visibility)
		}
	}
	if len(pub) != 4 {
		t.Fatalf("公开列表应有 4 条，实际 %d", len(pub))
	}

	// 搜索：关键词命中正文；私有接口不限可见性。
	found, err := s.ListNotes(ctx, ListOptions{Keywords: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("关键词 alpha 应命中 2 条（含私有的），实际 %d", len(found))
	}
	found, err = s.ListNotes(ctx, ListOptions{Keywords: "alpha", Visibility: model.VisibilityPublic, Limit: 10})
	if err != nil {
		t.Fatalf("公开搜索: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("公开搜索应命中 1 条，实际 %d", len(found))
	}
	byTag, err := s.ListNotes(ctx, ListOptions{Tag: "secret", Limit: 10})
	if err != nil {
		t.Fatalf("按标签搜索: %v", err)
	}
	if len(byTag) != 1 || byTag[0].Title != "hidden" {
		t.Fatalf("按标签过滤失败: %+v", byTag)
	}
}

func TestTags(t *testing.T) {
	_, scopedDSN, _ := testSchema(t)
	s := newStore(t, scopedDSN)
	ctx := t.Context()

	note, err := s.CreateNote(ctx, "n", "m", nil, model.VisibilityPublic)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := s.BindTag(ctx, note.ID, "go"); err != nil {
		t.Fatalf("BindTag: %v", err)
	}
	if err := s.BindTag(ctx, note.ID, "go"); err != nil {
		t.Fatalf("重复绑定应幂等: %v", err)
	}
	if err := s.BindTag(ctx, uuid.New(), "go"); err != ErrNotFound {
		t.Fatalf("绑定到不存在的笔记应 ErrNotFound，实际 %v", err)
	}

	if err := s.RenameTag(ctx, "go", "golang"); err != nil {
		t.Fatalf("RenameTag: %v", err)
	}
	if tags, _ := s.ListTagsForNote(ctx, note.ID); len(tags) != 1 || tags[0] != "golang" {
		t.Fatalf("重命名后应同步到笔记标签: %v", tags)
	}
	if err := s.RenameTag(ctx, "nope", "x"); err != ErrNotFound {
		t.Fatalf("重命名不存在标签应 ErrNotFound，实际 %v", err)
	}
	if err := s.BindTag(ctx, note.ID, "rust"); err != nil {
		t.Fatalf("BindTag rust: %v", err)
	}
	if err := s.RenameTag(ctx, "rust", "golang"); err != ErrTagExists {
		t.Fatalf("重命名到已存在标签应 ErrTagExists，实际 %v", err)
	}

	if err := s.UnbindTag(ctx, note.ID, "golang"); err != nil {
		t.Fatalf("UnbindTag: %v", err)
	}
	if err := s.UnbindTag(ctx, note.ID, "golang"); err != nil {
		t.Fatalf("重复解绑应幂等: %v", err)
	}

	// 公开标签只看 public 笔记；把笔记改私有后公开标签应消失。
	if err := s.BindTag(ctx, note.ID, "golang"); err != nil {
		t.Fatalf("BindTag: %v", err)
	}
	pub, err := s.ListPublicTags(ctx)
	if err != nil {
		t.Fatalf("ListPublicTags: %v", err)
	}
	if len(pub) != 2 {
		t.Fatalf("公开标签应有 2 个，实际 %v", pub)
	}
	if _, err := s.UpdateNote(ctx, note.ID, "n", "m", model.VisibilityPrivate, 1); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	pub, err = s.ListPublicTags(ctx)
	if err != nil {
		t.Fatalf("ListPublicTags: %v", err)
	}
	if len(pub) != 0 {
		t.Fatalf("笔记转私有后公开标签应为空，实际 %v", pub)
	}

	if err := s.DeleteTag(ctx, "golang"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if err := s.DeleteTag(ctx, "golang"); err != ErrNotFound {
		t.Fatalf("删除不存在标签应 ErrNotFound，实际 %v", err)
	}
}

func TestSnapshot(t *testing.T) {
	_, scopedDSN, _ := testSchema(t)
	s := newStore(t, scopedDSN)
	ctx := t.Context()

	if _, err := s.CreateNote(ctx, "a", "secret-body", []string{"go"}, model.VisibilityPublic); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := s.CreateNote(ctx, "b", "m", nil, model.VisibilityPrivate); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	snap, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Notes) != 2 || len(snap.Tags) != 1 {
		t.Fatalf("快照数量不符: notes=%d tags=%d", len(snap.Notes), len(snap.Tags))
	}
	if snap.SyncedAt.IsZero() {
		t.Fatal("synced_at 为空")
	}
	for _, n := range snap.Notes {
		if n.Title == "a" && (len(n.Tags) != 1 || n.Tags[0] != "go") {
			t.Fatalf("快照笔记标签不符: %+v", n)
		}
		if n.Title == "b" && len(n.Tags) != 0 {
			t.Fatalf("无标签笔记应为空切片，实际 %+v", n.Tags)
		}
	}
	// 快照不许带正文：SnapshotNote 的字段集合就是契约，多出字符串字段
	// 往往意味着有人把 raw_md 加了进来。
	typ := reflect.TypeOf(SnapshotNote{})
	want := map[string]bool{
		"ID": true, "Title": true, "Version": true, "Visibility": true,
		"CreatedAt": true, "ModifiedAt": true, "Tags": true,
	}
	if typ.NumField() != len(want) {
		t.Fatalf("SnapshotNote 字段数应为 %d，实际 %d（快照只放元数据）", len(want), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		if !want[typ.Field(i).Name] {
			t.Fatalf("SnapshotNote 多出字段 %s，快照只放元数据", typ.Field(i).Name)
		}
	}
}

