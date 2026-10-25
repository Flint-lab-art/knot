package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/model"
	"knot/internal/store/sqlite"
)

// fakeServer 是内存版 knot-server，只实现 CLI 用到的私有接口。
type fakeServer struct {
	mu    sync.Mutex
	notes map[uuid.UUID]dto.NoteDetail
	seq   int
	// updates 记录 PUT 请求体，便于断言「无变化不提交」。
	updates []dto.UpdateNoteRequest
	// puts 是 PUT 次数。
	puts int
	// afterGet 在第一次 GET 单篇笔记返回前调用（模拟并发修改），只触发一次。
	afterGet func()
	t        *testing.T
}

func newFakeServer(t *testing.T) (*fakeServer, *httptest.Server) {
	t.Helper()
	fs := &fakeServer{notes: map[uuid.UUID]dto.NoteDetail{}, t: t}
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	return fs, srv
}

// reply 是测试用 JSON 响应助手（cli 自己的 writeJSON 签名不同）。
func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	reply(w, http.StatusNotFound, map[string]any{"code": 404, "msg": "not found"})
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++

	if r.Header.Get("Authorization") != "Bearer test-token" {
		reply(w, http.StatusUnauthorized, map[string]any{"code": 401, "msg": "unauthorized"})
		return
	}
	// 新建笔记：直接落进内存表。
	if r.Method == http.MethodPost && r.URL.Path == "/api/notes" {
		var req dto.CreateNoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, http.StatusBadRequest, map[string]any{"code": 400, "msg": "bad"})
			return
		}
		n := f.addLocked(req.Title, req.RawMD, req.Tags...)
		reply(w, http.StatusCreated, n.Note)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	idPart, rest, _ := strings.Cut(path, "/")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/notes":
		notes := make([]dto.Note, 0, len(f.notes))
		for _, n := range f.notes {
			notes = append(notes, n.Note)
		}
		reply(w, http.StatusOK, dto.NoteList{Notes: notes})
	case r.Method == http.MethodGet && rest == "":
		id, err := uuid.Parse(idPart)
		if err != nil || f.notes[id].ID == uuid.Nil {
			notFound(w)
			return
		}
		detail := f.notes[id]
		if f.afterGet != nil {
			hook := f.afterGet
			f.afterGet = nil
			f.mu.Unlock()
			hook()
			f.mu.Lock()
		}
		reply(w, http.StatusOK, detail)
	case r.Method == http.MethodPut && rest == "":
		id, _ := uuid.Parse(idPart)
		var req dto.UpdateNoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, http.StatusBadRequest, map[string]any{"code": 400, "msg": "bad"})
			return
		}
		f.puts++
		f.updates = append(f.updates, req)
		cur, ok := f.notes[id]
		if !ok {
			notFound(w)
			return
		}
		if cur.Version != req.BaseVersion {
			reply(w, http.StatusConflict, map[string]any{"code": 409, "msg": "version conflict"})
			return
		}
		cur.RawMD = req.RawMD
		cur.Version++
		cur.ModifiedAt = cur.ModifiedAt.Add(time.Minute)
		f.notes[id] = cur
		reply(w, http.StatusOK, cur.Note)
	case r.Method == http.MethodDelete && rest == "":
		id, _ := uuid.Parse(idPart)
		delete(f.notes, id)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/api/sync/snapshot":
		notes := make([]dto.Note, 0, len(f.notes))
		tags := []dto.Tag{}
		for _, n := range f.notes {
			notes = append(notes, n.Note)
			for _, name := range n.Tags {
				tags = append(tags, dto.Tag{ID: uuid.New(), Name: name})
			}
		}
		reply(w, http.StatusOK, dto.SyncSnapshot{Notes: notes, Tags: tags, SyncedAt: time.Now().UTC()})
	default:
		notFound(w)
	}
}

func (f *fakeServer) add(title, rawMD string, tags ...string) dto.NoteDetail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(title, rawMD, tags...)
}

// addLocked 要求调用方已持锁。
func (f *fakeServer) addLocked(title, rawMD string, tags ...string) dto.NoteDetail {
	if tags == nil {
		tags = []string{}
	}
	now := time.Now().UTC().Truncate(time.Second)
	n := dto.NoteDetail{
		Note: dto.Note{
			ID: uuid.New(), Title: title, Version: 1, Visibility: string(model.VisibilityPrivate),
			CreatedAt: now, ModifiedAt: now, Tags: tags,
		},
		RawMD: rawMD,
	}
	f.notes[n.ID] = n
	return n
}

// testEnv 准备一套「配置 + 缓存库 + 假服务 + 假编辑器」。
type testEnv struct {
	t      *testing.T
	out    bytes.Buffer
	errOut bytes.Buffer
	fs     *fakeServer
	cfg    string
	cache  string
}

func newTestEnv(t *testing.T, editorScript string) *testEnv {
	t.Helper()
	fs, srv := newFakeServer(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	cache := filepath.Join(dir, "cache.db")
	editor := filepath.Join(dir, "editor.sh")
	if editorScript == "" {
		editorScript = "#!/bin/sh\nexit 0\n"
	}
	if err := os.WriteFile(editor, []byte(editorScript), 0o700); err != nil {
		t.Fatalf("写假编辑器: %v", err)
	}
	content := "server_url = \"" + srv.URL + "\"\n" +
		"token = \"test-token\"\n\n" +
		"[editor]\ncommand = \"" + editor + "\"\n\n" +
		"[cache]\npath = \"" + cache + "\"\n"
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	return &testEnv{t: t, fs: fs, cfg: cfg, cache: cache}
}

// run 执行一次 CLI 命令，返回 err。
func (e *testEnv) run(args ...string) error {
	e.out.Reset()
	e.errOut.Reset()
	cmd := NewRootCmd(&e.out, &e.errOut)
	cmd.SetArgs(append([]string{"--config", e.cfg}, args...))
	return cmd.Execute()
}

// syncCache 让本地缓存与假服务一致（模拟 `knot sync`）。
func (e *testEnv) syncCache() {
	e.t.Helper()
	if err := e.run("sync"); err != nil {
		e.t.Fatalf("sync: %v\n%s", err, e.errOut.String())
	}
}

func (e *testEnv) db() *sqlite.DB {
	e.t.Helper()
	db, err := sqlite.Open(context.Background(), e.cache)
	if err != nil {
		e.t.Fatalf("打开缓存库: %v", err)
	}
	e.t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---------------- 命令行为 ----------------

func TestNewThenListAndShow(t *testing.T) {
	e := newTestEnv(t, "")
	if err := e.run("new", "我的笔记", "--tags", "go,rust", "--body", "# 正文"); err != nil {
		t.Fatalf("new: %v\n%s", err, e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "我的笔记") {
		t.Fatalf("new 输出应含标题: %q", e.out.String())
	}
	if err := e.run("list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(e.out.String(), "我的笔记") || !strings.Contains(e.out.String(), "#go") {
		t.Fatalf("list 输出不符: %q", e.out.String())
	}
	if err := e.run("show", "我的笔记"); err != nil {
		t.Fatalf("show: %v\n%s", err, e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "# 正文") {
		t.Fatalf("show 未输出正文: %q", e.out.String())
	}
}

func TestNewRejectsBadVisibility(t *testing.T) {
	e := newTestEnv(t, "")
	if err := e.run("new", "x", "--visibility", "unlisted"); err == nil {
		t.Fatal("非法 visibility 应报错")
	}
}

func TestNewRequiresTitle(t *testing.T) {
	e := newTestEnv(t, "")
	if err := e.run("new"); err == nil {
		t.Fatal("缺标题应报错")
	}
}

// 编辑器未改动内容 → 不提交。
func TestEditNoChangeDoesNotSubmit(t *testing.T) {
	e := newTestEnv(t, "#!/bin/sh\nexit 0\n")
	n := e.fs.add("笔记", "原正文")
	e.syncCache()

	if err := e.run("edit", "笔记"); err != nil {
		t.Fatalf("edit: %v\n%s", err, e.errOut.String())
	}
	if e.fs.puts != 0 {
		t.Fatalf("无变化时不应 PUT，实际 %d 次", e.fs.puts)
	}
	if !strings.Contains(e.out.String(), "没有变化") {
		t.Fatalf("输出不符: %q", e.out.String())
	}
	_ = n
}

// 编辑器只改了行尾空白/换行 → 归一化后视为无变化。
func TestEditWhitespaceOnlyDoesNotSubmit(t *testing.T) {
	// 在每行末尾加空格，并额外加一个换行。
	e := newTestEnv(t, "#!/bin/sh\nsed -i 's/$/   /' \"$1\"\nprintf '\\n\\n' >> \"$1\"\n")
	e.fs.add("笔记", "第一行\n第二行\n")
	e.syncCache()

	if err := e.run("edit", "笔记"); err != nil {
		t.Fatalf("edit: %v\n%s", err, e.errOut.String())
	}
	if e.fs.puts != 0 {
		t.Fatalf("仅空白差异不应 PUT，实际 %d 次：%+v", e.fs.puts, e.fs.updates)
	}
}

// 编辑器改了内容 → PUT 带正确 base_version。
func TestEditSubmitsChanges(t *testing.T) {
	e := newTestEnv(t, "#!/bin/sh\nprintf '新内容\\n' > \"$1\"\n")
	n := e.fs.add("笔记", "原正文")
	e.syncCache()

	if err := e.run("edit", "笔记"); err != nil {
		t.Fatalf("edit: %v\n%s", err, e.errOut.String())
	}
	if e.fs.puts != 1 {
		t.Fatalf("应 PUT 一次，实际 %d", e.fs.puts)
	}
	got := e.fs.updates[0]
	if got.RawMD != "新内容\n" {
		t.Fatalf("raw_md = %q", got.RawMD)
	}
	if got.BaseVersion != n.Version {
		t.Fatalf("base_version = %d, want %d", got.BaseVersion, n.Version)
	}
	if !strings.Contains(e.out.String(), "已保存") {
		t.Fatalf("输出不符: %q", e.out.String())
	}
}

// 编辑器失败 → 保留临时文件并在错误里给出路径。
func TestEditEditorFailureKeepsFile(t *testing.T) {
	e := newTestEnv(t, "#!/bin/sh\nexit 3\n")
	e.fs.add("笔记", "正文")
	e.syncCache()

	err := e.run("edit", "笔记")
	if err == nil {
		t.Fatal("编辑器失败应报错")
	}
	if !strings.Contains(err.Error(), "内容保留在") {
		t.Fatalf("错误应提示临时文件位置: %v", err)
	}
	// 清理测试留下的临时文件。
	path := err.Error()
	if i := strings.Index(path, "内容保留在 "); i >= 0 {
		_ = os.Remove(strings.TrimSuffix(path[i+len("内容保留在 "):], ")"))
	}
}

// 版本冲突（服务端在 GET 之后被改了）→ 提示重新拉取，保留内容。
func TestEditConflict(t *testing.T) {
	e := newTestEnv(t, "")
	n := e.fs.add("笔记", "原正文")
	e.syncCache()
	// 假编辑器先写内容，再把服务端版本推高：编辑流程拿到的是旧版本，
	// PUT 时就会撞 409。
	editor := filepath.Join(filepath.Dir(e.cfg), "editor.sh")
	script := "#!/bin/sh\n" +
		"printf '改了\\n' > \"$1\"\n" +
		"touch '" + filepath.Join(filepath.Dir(e.cfg), "marker") + "'\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatalf("写编辑器: %v", err)
	}
	// 在编辑器执行「之前」推高版本：用 hook 在 GET 之后触发。
	e.fs.afterGet = func() {
		e.fs.mu.Lock()
		cur := e.fs.notes[n.ID]
		cur.Version++
		e.fs.notes[n.ID] = cur
		e.fs.mu.Unlock()
	}

	err := e.run("edit", "笔记")
	if err == nil {
		t.Fatal("应报版本冲突")
	}
	if !strings.Contains(err.Error(), "版本冲突") {
		t.Fatalf("错误信息不符: %v", err)
	}
	if i := strings.Index(err.Error(), "内容保留在 "); i >= 0 {
		_ = os.Remove(strings.TrimSuffix(err.Error()[i+len("内容保留在 "):], ")"))
	}
}

// 服务端已删除笔记 → 本地缓存清理。
func TestEditNoteDeletedOnServer(t *testing.T) {
	e := newTestEnv(t, "#!/bin/sh\nexit 0\n")
	n := e.fs.add("笔记", "正文")
	e.syncCache()
	e.fs.mu.Lock()
	delete(e.fs.notes, n.ID)
	e.fs.mu.Unlock()

	err := e.run("edit", "笔记")
	if err == nil || !strings.Contains(err.Error(), "已在服务端删除") {
		t.Fatalf("应提示已删除: %v", err)
	}
	db := e.db()
	if _, err := db.GetNote(context.Background(), n.ID); err == nil {
		t.Fatal("本地缓存应已清理")
	}
}

func TestRmDeletesRemotelyAndLocally(t *testing.T) {
	e := newTestEnv(t, "")
	n := e.fs.add("要删的", "正文")
	e.syncCache()

	if err := e.run("rm", "-y", "要删的"); err != nil {
		t.Fatalf("rm: %v\n%s", err, e.errOut.String())
	}
	e.fs.mu.Lock()
	_, stillThere := e.fs.notes[n.ID]
	e.fs.mu.Unlock()
	if stillThere {
		t.Fatal("服务端笔记应已删除")
	}
	db := e.db()
	if _, err := db.GetNote(context.Background(), n.ID); err == nil {
		t.Fatal("本地缓存应已清理")
	}
}

// 多次匹配时拒绝执行，并列出候选。
func TestSelectorAmbiguous(t *testing.T) {
	e := newTestEnv(t, "")
	e.fs.add("会议记录 1", "")
	e.fs.add("会议记录 2", "")
	e.syncCache()

	err := e.run("show", "会议记录")
	if err == nil {
		t.Fatal("多条匹配应报错")
	}
	if !strings.Contains(err.Error(), "匹配到 2 篇") {
		t.Fatalf("错误信息不符: %v", err)
	}
}

func TestSelectorNoMatch(t *testing.T) {
	e := newTestEnv(t, "")
	e.syncCache()
	err := e.run("show", "不存在")
	if err == nil || !strings.Contains(err.Error(), "没有匹配") {
		t.Fatalf("应提示没有匹配: %v", err)
	}
}

// UUID selector 直接走服务端，不依赖本地缓存。
func TestSelectorByUUID(t *testing.T) {
	e := newTestEnv(t, "")
	n := e.fs.add("只在服务端", "正文")
	if err := e.run("show", n.ID.String()); err != nil {
		t.Fatalf("show by uuid: %v\n%s", err, e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "只在服务端") {
		t.Fatalf("输出不符: %q", e.out.String())
	}
}

func TestCacheClearRequiresFlag(t *testing.T) {
	e := newTestEnv(t, "")
	if err := e.run("cache", "clear"); err == nil {
		t.Fatal("未指定 --content/--all 应报错")
	}
}

func TestSyncReportsChanges(t *testing.T) {
	e := newTestEnv(t, "")
	e.fs.add("一", "")
	e.fs.add("二", "")
	if err := e.run("sync"); err != nil {
		t.Fatalf("sync: %v\n%s", err, e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "写入 2") {
		t.Fatalf("同步输出不符: %q", e.out.String())
	}
	// 再次 sync 应为空。
	if err := e.run("sync"); err != nil || !strings.Contains(e.out.String(), "已是最新") {
		t.Fatalf("二次 sync: %v %q", err, e.out.String())
	}
}

// ---------------- 纯函数 ----------------

func TestNormalizeMarkdown(t *testing.T) {
	tests := map[string]string{
		"a\n":          "a\n",
		"a\r\nb\r\n":   "a\nb\n",
		"a\n\n\n":      "a\n",
		"a \nb\t\n":    "a\nb\n",
		"":             "\n",
		"  a  \n\n b ": "  a\n\n b\n",
	}
	for in, want := range tests {
		if got := normalizeMarkdown(in); got != want {
			t.Fatalf("normalizeMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatListLine(t *testing.T) {
	n := dto.Note{ID: uuid.MustParse("12345678-1234-1234-1234-123456789abc"), Title: "标题", Tags: []string{"go", "rust"}}
	if got := formatListLine(n, true); got != "12345678-1234-1234-1234-123456789abc  标题" {
		t.Fatalf("short = %q", got)
	}
	if got := formatListLine(n, false); got != "12345678  标题  #go #rust" {
		t.Fatalf("line = %q", got)
	}
}

// list --json 不应输出本地缓存没有的字段（如恒为 0 的 version）。
func TestListJSONOmitsVersion(t *testing.T) {
	e := newTestEnv(t, "")
	e.fs.add("笔记", "", "go")
	e.syncCache()
	if err := e.run("list", "--json"); err != nil {
		t.Fatalf("list --json: %v\n%s", err, e.errOut.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &out); err != nil {
		t.Fatalf("解析输出: %v\n%s", err, e.out.String())
	}
	if len(out) != 1 {
		t.Fatalf("条数 = %d", len(out))
	}
	if _, ok := out[0]["version"]; ok {
		t.Fatalf("本地列表不应输出 version: %v", out[0])
	}
	for _, k := range []string{"id", "title", "tags", "created_at", "modified_at", "visibility"} {
		if _, ok := out[0][k]; !ok {
			t.Fatalf("缺少字段 %q: %v", k, out[0])
		}
	}
}
