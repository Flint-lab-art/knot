package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"knot/internal/model"
	"knot/internal/server/handler"
	"knot/internal/store/pg"
)

const testToken = "test-token-please-ignore"

// mockStore 是可编程的内存 store，只实现 handler 需要的接口。
// 不做真实持久化：这些测试关心的是 HTTP 语义（状态码、字段裁剪、限流）。
type mockStore struct {
	notes map[uuid.UUID]pg.Note
	tags  map[uuid.UUID][]string
	// err 非 nil 时所有方法直接返回它，用于模拟 DB 故障。
	err error
	// publicOnly 为 true 时 ListNotes 忽略调用方参数，只返回 public ——
	// 用来验证 handler 确实传了 Visibility=public。
	lastListOpt pg.ListOptions
}

func newMockStore() *mockStore {
	return &mockStore{notes: map[uuid.UUID]pg.Note{}, tags: map[uuid.UUID][]string{}}
}

func (m *mockStore) add(n pg.Note, tags ...string) pg.Note {
	m.notes[n.ID] = n
	if tags != nil {
		m.tags[n.ID] = tags
	}
	return n
}

func (m *mockStore) CreateNote(_ context.Context, title, rawMD string, tags []string, vis model.Visibility) (pg.Note, error) {
	if m.err != nil {
		return pg.Note{}, m.err
	}
	now := time.Now().UTC()
	n := pg.Note{
		ID: uuid.New(), Title: title, RawMD: rawMD, Version: 1,
		Visibility: vis, CreatedAt: now, ModifiedAt: now,
	}
	m.add(n, tags...)
	return n, nil
}

func (m *mockStore) GetNote(_ context.Context, id uuid.UUID) (pg.Note, error) {
	if m.err != nil {
		return pg.Note{}, m.err
	}
	n, ok := m.notes[id]
	if !ok {
		return pg.Note{}, pg.ErrNotFound
	}
	return n, nil
}

func (m *mockStore) GetPublicNote(ctx context.Context, id uuid.UUID) (pg.Note, error) {
	n, err := m.GetNote(ctx, id)
	if err != nil {
		return pg.Note{}, err
	}
	// 真实实现在 SQL 层过滤；mock 在这里复刻同样的语义。
	if n.Visibility != model.VisibilityPublic {
		return pg.Note{}, pg.ErrNotFound
	}
	return n, nil
}

func (m *mockStore) UpdateNote(_ context.Context, id uuid.UUID, title, rawMD string, vis model.Visibility, base int64) (pg.Note, error) {
	if m.err != nil {
		return pg.Note{}, m.err
	}
	n, ok := m.notes[id]
	if !ok {
		return pg.Note{}, pg.ErrNotFound
	}
	if n.Version != base {
		return pg.Note{}, pg.ErrConflict
	}
	n.Title, n.RawMD, n.Visibility, n.Version = title, rawMD, vis, n.Version+1
	n.ModifiedAt = n.ModifiedAt.Add(time.Second)
	m.notes[id] = n
	return n, nil
}

func (m *mockStore) DeleteNote(_ context.Context, id uuid.UUID) error {
	if m.err != nil {
		return m.err
	}
	delete(m.notes, id) // 不存在也成功，与 DELETE 幂等一致
	return nil
}

func (m *mockStore) ListNotes(_ context.Context, opt pg.ListOptions) ([]pg.Note, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.lastListOpt = opt
	out := make([]pg.Note, 0, len(m.notes))
	for _, n := range m.notes {
		if opt.Visibility != "" && n.Visibility != opt.Visibility {
			continue
		}
		out = append(out, n)
	}
	// 稳定排序，避免 map 遍历顺序影响分页断言。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ModifiedAt.After(out[j-1].ModifiedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if opt.Limit > 0 && len(out) > opt.Limit {
		out = out[:opt.Limit]
	}
	return out, nil
}

func (m *mockStore) ListTagsForNote(_ context.Context, id uuid.UUID) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	if _, ok := m.notes[id]; !ok {
		return nil, pg.ErrNotFound
	}
	tags := m.tags[id]
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}

func (m *mockStore) TagsByNote(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make(map[uuid.UUID][]string, len(ids))
	for _, id := range ids {
		if tags := m.tags[id]; tags != nil {
			out[id] = tags
		}
	}
	return out, nil
}

func (m *mockStore) BindTag(_ context.Context, noteID uuid.UUID, name string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.notes[noteID]; !ok {
		return pg.ErrNotFound
	}
	m.tags[noteID] = append(m.tags[noteID], name)
	return nil
}

func (m *mockStore) UnbindTag(_ context.Context, noteID uuid.UUID, name string) error {
	if m.err != nil {
		return m.err
	}
	kept := m.tags[noteID][:0]
	for _, t := range m.tags[noteID] {
		if t != name {
			kept = append(kept, t)
		}
	}
	m.tags[noteID] = kept
	return nil
}

func (m *mockStore) ListTags(_ context.Context) ([]pg.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	return allTags(m.tags), nil
}

func (m *mockStore) ListPublicTags(_ context.Context) ([]pg.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	names := map[string]bool{}
	for id, tags := range m.tags {
		if m.notes[id].Visibility != model.VisibilityPublic {
			continue
		}
		for _, t := range tags {
			names[t] = true
		}
	}
	out := make([]pg.Tag, 0, len(names))
	for name := range names {
		out = append(out, pg.Tag{ID: uuid.New(), Name: name})
	}
	return out, nil
}

func (m *mockStore) RenameTag(_ context.Context, oldName, newName string) error {
	if m.err != nil {
		return m.err
	}
	found := false
	for id, tags := range m.tags {
		for i, t := range tags {
			if t == oldName {
				m.tags[id][i] = newName
				found = true
			}
		}
	}
	if !found {
		return pg.ErrNotFound
	}
	return nil
}

func (m *mockStore) DeleteTag(_ context.Context, name string) error {
	if m.err != nil {
		return m.err
	}
	for id, tags := range m.tags {
		m.tags[id] = remove(tags, name)
	}
	return nil
}

func (m *mockStore) Snapshot(_ context.Context) (pg.Snapshot, error) {
	if m.err != nil {
		return pg.Snapshot{}, m.err
	}
	snap := pg.Snapshot{SyncedAt: time.Now().UTC()}
	for _, n := range m.notes {
		tags := m.tags[n.ID]
		if tags == nil {
			tags = []string{}
		}
		snap.Notes = append(snap.Notes, pg.SnapshotNote{
			ID: n.ID, Title: n.Title, Version: int32(n.Version),
			Visibility: n.Visibility, CreatedAt: n.CreatedAt,
			ModifiedAt: n.ModifiedAt, Tags: tags,
		})
	}
	snap.Tags = allTags(m.tags)
	return snap, nil
}

func allTags(tags map[uuid.UUID][]string) []pg.Tag {
	seen := map[string]bool{}
	out := []pg.Tag{}
	for _, ts := range tags {
		for _, t := range ts {
			if !seen[t] {
				seen[t] = true
				out = append(out, pg.Tag{ID: uuid.New(), Name: t})
			}
		}
	}
	return out
}

func remove(ss []string, s string) []string {
	out := ss[:0]
	for _, v := range ss {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// newTestRouter 构造带 mock store 与测试 token 的路由，限流给足额度。
func newTestRouter(st handler.Store) http.Handler {
	return NewRouter(Deps{
		Store:           st,
		Token:           testToken,
		PublicPerSecond: 1000,
		PublicBurst:     1000,
		SearchPerSecond: 1000,
		SearchBurst:     1000,
	})
}

// do 发一个请求，默认带 Bearer token。
func do(t *testing.T, h http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if auth {
		r.Header.Set("Authorization", "Bearer "+testToken)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("解析响应体失败: %v\n%s", err, rec.Body.String())
	}
	return v
}

// ---------------- 鉴权 ----------------

func TestAuthRejectsMissingOrWrongToken(t *testing.T) {
	h := newTestRouter(newMockStore())
	tests := []struct {
		name string
		auth string
		want int
	}{
		{"缺失", "", http.StatusUnauthorized},
		{"错误", "Bearer nope", http.StatusUnauthorized},
		{"空 token", "Bearer ", http.StatusUnauthorized},
		{"大小写不敏感 scheme", "bearer " + testToken, http.StatusOK},
		{"正确", "Bearer " + testToken, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
			if tt.auth != "" {
				r.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 应带 WWW-Authenticate")
			}
		})
	}
}

// 未配置 token 时不能变成「无鉴权」。
func TestAuthUnconfiguredTokenRejectsEverything(t *testing.T) {
	h := NewRouter(Deps{Store: newMockStore(), Token: ""})
	rec := do(t, h, http.MethodGet, "/api/tags", "", true)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("空 token 配置应 401，实际 %d", rec.Code)
	}
}

// 公开接口不需要 token。
func TestPublicRoutesNeedNoToken(t *testing.T) {
	st := newMockStore()
	st.add(pg.Note{ID: uuid.New(), Title: "p", Visibility: model.VisibilityPublic, CreatedAt: time.Now()})
	h := newTestRouter(st)
	for _, path := range []string{"/api/public/notes", "/api/public/tags"} {
		rec := do(t, h, http.MethodGet, path, "", false)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 无 token 应 200，实际 %d", path, rec.Code)
		}
	}
}

// ---------------- 版本冲突 ----------------

func TestUpdateNoteVersionConflict(t *testing.T) {
	st := newMockStore()
	note := st.add(pg.Note{ID: uuid.New(), Title: "t", RawMD: "m", Version: 1, Visibility: model.VisibilityPrivate,
		CreatedAt: time.Now(), ModifiedAt: time.Now()})
	h := newTestRouter(st)

	// 成功的更新：base_version=1 → 版本 2。
	rec := do(t, h, http.MethodPut, "/api/notes/"+note.ID.String(),
		`{"title":"new","raw_md":"x","visibility":"private","base_version":1}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body)
	}
	got := decodeBody[struct {
		Version int64 `json:"version"`
	}](t, rec)
	if got.Version != 2 {
		t.Fatalf("version = %d, want 2", got.Version)
	}

	// 用旧版本重放 → 409。
	rec = do(t, h, http.MethodPut, "/api/notes/"+note.ID.String(),
		`{"title":"again","raw_md":"x","visibility":"private","base_version":1}`, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("重放应 409，实际 %d", rec.Code)
	}
	e := decodeBody[struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}](t, rec)
	if e.Code != 409 || e.Msg != "version conflict" {
		t.Fatalf("409 响应体不符: %+v", e)
	}

	// base_version 缺失 → 400（不是 409）。
	rec = do(t, h, http.MethodPut, "/api/notes/"+note.ID.String(),
		`{"title":"x","raw_md":"x","visibility":"private"}`, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 base_version 应 400，实际 %d", rec.Code)
	}

	// 不存在的笔记 → 404（不能误报 409）。
	rec = do(t, h, http.MethodPut, "/api/notes/"+uuid.New().String(),
		`{"title":"x","raw_md":"x","visibility":"private","base_version":1}`, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404，实际 %d", rec.Code)
	}
}

func TestUpdateNoteRejectsUnknownFields(t *testing.T) {
	st := newMockStore()
	note := st.add(pg.Note{ID: uuid.New(), Title: "t", Version: 1, Visibility: model.VisibilityPrivate})
	h := newTestRouter(st)
	rec := do(t, h, http.MethodPut, "/api/notes/"+note.ID.String(),
		`{"title":"x","raw_md":"x","visibility":"private","base_version":1,"baseVersion":1}`, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，实际 %d", rec.Code)
	}
}

// ---------------- 公开接口过滤 ----------------

func TestPublicNoteFiltering(t *testing.T) {
	st := newMockStore()
	pub := st.add(pg.Note{ID: uuid.New(), Title: "公开", RawMD: "公开正文", Version: 7,
		Visibility: model.VisibilityPublic, CreatedAt: time.Now(), ModifiedAt: time.Now()}, "go")
	priv := st.add(pg.Note{ID: uuid.New(), Title: "私密", RawMD: "私密正文", Version: 3,
		Visibility: model.VisibilityPrivate, CreatedAt: time.Now(), ModifiedAt: time.Now()}, "secret")
	h := newTestRouter(st)

	// 私有笔记走公开接口 → 404，且不能泄露正文。
	rec := do(t, h, http.MethodGet, "/api/public/notes/"+priv.ID.String(), "", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("私密笔记应 404，实际 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "私密正文") {
		t.Fatal("404 响应泄露了正文")
	}

	// 公开笔记正常返回，但不含 version / modified_at / visibility。
	rec = do(t, h, http.MethodGet, "/api/public/notes/"+pub.ID.String(), "", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"version", "modified_at", "visibility"} {
		if _, ok := m[forbidden]; ok {
			t.Fatalf("公开笔记不应返回 %q: %s", forbidden, rec.Body)
		}
	}
	if m["raw_md"] != "公开正文" {
		t.Fatalf("公开笔记应含正文: %s", rec.Body)
	}

	// 公开列表只含 public，并且 handler 确实向 store 传了 public 过滤。
	rec = do(t, h, http.MethodGet, "/api/public/notes", "", false)
	list := decodeBody[struct {
		Notes []struct {
			ID    uuid.UUID `json:"id"`
			Title string    `json:"title"`
		} `json:"notes"`
	}](t, rec)
	if len(list.Notes) != 1 || list.Notes[0].Title != "公开" {
		t.Fatalf("公开列表混入私密笔记: %+v", list.Notes)
	}
	if st.lastListOpt.Visibility != model.VisibilityPublic {
		t.Fatalf("公开列表必须传 Visibility=public，实际 %q", st.lastListOpt.Visibility)
	}

	// 公开标签只来自 public 笔记。
	rec = do(t, h, http.MethodGet, "/api/public/tags", "", false)
	tags := decodeBody[[]struct {
		Name string `json:"name"`
	}](t, rec)
	if len(tags) != 1 || tags[0].Name != "go" {
		t.Fatalf("公开标签泄露了私密标签: %+v", tags)
	}
}

// ---------------- 快照 ----------------

func TestSnapshotHasNoRawMD(t *testing.T) {
	st := newMockStore()
	st.add(pg.Note{ID: uuid.New(), Title: "t", RawMD: "secret-body", Visibility: model.VisibilityPrivate,
		CreatedAt: time.Now(), ModifiedAt: time.Now()}, "go")
	h := newTestRouter(st)
	rec := do(t, h, http.MethodGet, "/api/sync/snapshot", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-body") || strings.Contains(rec.Body.String(), "raw_md") {
		t.Fatalf("快照不应包含正文: %s", rec.Body)
	}
}

// ---------------- 删除幂等 ----------------

func TestDeleteIsIdempotent(t *testing.T) {
	st := newMockStore()
	note := st.add(pg.Note{ID: uuid.New(), Title: "t", Visibility: model.VisibilityPrivate})
	h := newTestRouter(st)
	for i := 0; i < 2; i++ {
		rec := do(t, h, http.MethodDelete, "/api/notes/"+note.ID.String(), "", true)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("第 %d 次删除 status = %d，want 204", i+1, rec.Code)
		}
	}
}

// ---------------- 错误与边界 ----------------

func TestBadInputs(t *testing.T) {
	st := newMockStore()
	note := st.add(pg.Note{ID: uuid.New(), Title: "t", Visibility: model.VisibilityPrivate})
	h := newTestRouter(st)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"非法 id", http.MethodGet, "/api/notes/not-a-uuid", "", http.StatusBadRequest},
		{"非法 visibility", http.MethodPost, "/api/notes", `{"title":"x","raw_md":"","visibility":"unlisted"}`, http.StatusBadRequest},
		{"缺 title", http.MethodPost, "/api/notes", `{"raw_md":"x"}`, http.StatusBadRequest},
		{"非法 JSON", http.MethodPost, "/api/notes", `{`, http.StatusBadRequest},
		{"非法 limit", http.MethodGet, "/api/notes?limit=0", "", http.StatusBadRequest},
		{"非法 cursor", http.MethodGet, "/api/notes?cursor=@@@", "", http.StatusBadRequest},
		{"空标签名", http.MethodPost, "/api/notes/" + note.ID.String() + "/tags", `{"name":""}`, http.StatusBadRequest},
		{"重命名缺 name", http.MethodPut, "/api/tags/go", `{}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.path, tt.body, true)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

// store 内部错误只能变成 500，且不把原始错误透给客户端。
func TestInternalErrorIsMasked(t *testing.T) {
	st := newMockStore()
	st.err = errors.New("pq: relation \"notes\" does not exist")
	h := newTestRouter(st)
	rec := do(t, h, http.MethodGet, "/api/notes", "", true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "relation") {
		t.Fatalf("500 响应泄露了内部错误: %s", rec.Body)
	}
}

func TestNotFoundRoutes(t *testing.T) {
	h := newTestRouter(newMockStore())
	rec := do(t, h, http.MethodGet, "/api/nope", "", true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// 正文超限返回 413（MaxBytesReader）。
func TestBodyTooLarge(t *testing.T) {
	h := newTestRouter(newMockStore())
	r := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(
		`{"title":"x","raw_md":"`+strings.Repeat("a", 9<<20)+`"}`))
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

// ---------------- 限流 ----------------

func TestRateLimitPublic(t *testing.T) {
	h := NewRouter(Deps{
		Store: newMockStore(), Token: testToken,
		// 极低额度：突发 1，几乎立即恢复。
		PublicPerSecond: 0.001,
		PublicBurst:     1,
		SearchPerSecond: 0.001,
		SearchBurst:     1,
	})
	first := do(t, h, http.MethodGet, "/api/public/tags", "", false)
	if first.Code != http.StatusOK {
		t.Fatalf("首次请求应 200，实际 %d", first.Code)
	}
	second := do(t, h, http.MethodGet, "/api/public/tags", "", false)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("超限应 429，实际 %d", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("429 应带 Retry-After")
	}
	e := decodeBody[struct {
		Code int `json:"code"`
	}](t, second)
	if e.Code != 429 {
		t.Fatalf("429 响应体 code = %d", e.Code)
	}
}

// 公开列表比普通公开接口额度更低：同样的突发量下先被限流。
func TestRateLimitSearchTighterThanPublic(t *testing.T) {
	h := NewRouter(Deps{
		Store: newMockStore(), Token: testToken,
		PublicPerSecond: 1000, PublicBurst: 1000,
		SearchPerSecond: 0.001, SearchBurst: 1,
	})
	if rec := do(t, h, http.MethodGet, "/api/public/notes", "", false); rec.Code != http.StatusOK {
		t.Fatalf("首次列表应 200，实际 %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/public/notes", "", false); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("列表超限应 429，实际 %d", rec.Code)
	}
	// 普通公开接口不受列表档影响。
	if rec := do(t, h, http.MethodGet, "/api/public/tags", "", false); rec.Code != http.StatusOK {
		t.Fatalf("tags 不应被列表限流影响，实际 %d", rec.Code)
	}
}

// 时间必须按 docs/API.md 输出 UTC RFC3339，不能带服务端本地时区。
func TestTimestampsAreUTC(t *testing.T) {
	st := newMockStore()
	cst := time.FixedZone("CST", 8*3600)
	at := time.Date(2026, 10, 24, 18, 0, 0, 0, cst)
	note := st.add(pg.Note{ID: uuid.New(), Title: "t", Version: 1, Visibility: model.VisibilityPublic,
		CreatedAt: at, ModifiedAt: at})
	h := newTestRouter(st)

	for _, path := range []string{"/api/public/notes/" + note.ID.String(), "/api/public/notes"} {
		rec := do(t, h, http.MethodGet, path, "", false)
		if strings.Contains(rec.Body.String(), "+08:00") {
			t.Fatalf("%s 返回了非 UTC 时间: %s", path, rec.Body)
		}
	}
	rec := do(t, h, http.MethodGet, "/api/sync/snapshot", "", true)
	if strings.Contains(rec.Body.String(), "+08:00") {
		t.Fatalf("快照返回了非 UTC 时间: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "2026-10-24T10:00:00Z") {
		t.Fatalf("快照时间应为 UTC: %s", rec.Body)
	}
}
