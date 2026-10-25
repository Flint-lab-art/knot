package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/model"
)

// newTestClient 起一个 httptest 服务并把请求转录到 handler。
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "secret", Options{})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func TestBearerAndHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "knot-cli" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		writeJSON(w, http.StatusOK, []any{})
	})
	if _, err := c.ListTags(context.Background()); err != nil {
		t.Fatalf("ListTags: %v", err)
	}
}

// baseURL 末尾带 / 不应产生双斜杠。
func TestTrailingSlashBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeJSON(w, http.StatusOK, []any{})
	}))
	defer srv.Close()
	c := New(srv.URL+"/", "t", Options{})
	if _, err := c.ListTags(context.Background()); err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if gotPath != "/api/tags" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"401", http.StatusUnauthorized, `{"code":401,"msg":"invalid token"}`, ErrUnauthorized},
		{"404", http.StatusNotFound, `{"code":404,"msg":"not found"}`, ErrNotFound},
		{"409", http.StatusConflict, `{"code":409,"msg":"version conflict"}`, ErrConflict},
		{"429", http.StatusTooManyRequests, `{"code":429,"msg":"rate limit exceeded"}`, ErrRateLimited},
		{"400 不映射成哨兵", http.StatusBadRequest, `{"code":400,"msg":"bad"}`, nil},
		{"500 不映射成哨兵", http.StatusInternalServerError, `{"code":500,"msg":"internal"}`, nil},
		{"非 JSON body 仍按状态码", http.StatusBadGateway, `oops`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.ListTags(context.Background())
			if err == nil {
				t.Fatal("应返回错误")
			}
			code := errorCode(t, err)
			if code != tt.status {
				t.Fatalf("code = %d, want %d", code, tt.status)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("errors.Is(%v) 失败: %v", tt.wantErr, err)
			}
			if tt.wantErr == nil && errors.Is(err, ErrNotFound) {
				t.Fatalf("非 404 不应匹配 ErrNotFound: %v", err)
			}
		})
	}
}

// errorCode 取出任意错误类型里的 HTTP 状态码。
func errorCode(t *testing.T, err error) int {
	t.Helper()
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return rl.Code
	}
	t.Fatalf("无法识别的错误类型: %T", err)
	return 0
}

func TestRateLimitRetryAfter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": 429, "msg": "slow down"})
	})
	_, err := c.ListTags(context.Background())
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("应为 *RateLimitError: %T", err)
	}
	if rl.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", rl.RetryAfter)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("应匹配 ErrRateLimited")
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := map[string]time.Duration{
		"":            0,
		"5":           5 * time.Second,
		" 3 ":         3 * time.Second,
		"-1":          0,
		"0":           0,
		"Wed, 21 Oct": 0, // HTTP 日期格式不支持，退化为「尽快重试」
	}
	for in, want := range tests {
		if got := parseRetryAfter(in); got != want {
			t.Fatalf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNetworkError(t *testing.T) {
	// 关掉的服务器 → 连接被拒。
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := New(url, "t", Options{HTTP: &http.Client{Timeout: time.Second}})
	_, err := c.ListTags(context.Background())
	var ne *NetworkError
	if !errors.As(err, &ne) {
		t.Fatalf("应为 *NetworkError: %T (%v)", err, err)
	}
	if !strings.Contains(err.Error(), "无法连接") {
		t.Fatalf("错误信息应提示连接失败: %v", err)
	}
}

func TestCreateNoteBody(t *testing.T) {
	var got dto.CreateNoteRequest
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/notes" {
			t.Errorf("意外请求 %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": uuid.New(), "title": got.Title, "version": 1,
			"visibility": got.Visibility, "tags": got.Tags,
		})
	})
	note, err := c.CreateNote(context.Background(), "标题", "# 正文", nil, model.VisibilityPrivate)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if got.Title != "标题" || got.RawMD != "# 正文" || got.Visibility != "private" {
		t.Fatalf("请求体不符: %+v", got)
	}
	// nil tags 应序列化成 []，而不是 null。
	if note.Tags == nil {
		t.Fatal("tags 应为空数组")
	}
}

func TestUpdateNoteConflict(t *testing.T) {
	id := uuid.New()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body dto.UpdateNoteRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.BaseVersion != 3 {
			t.Errorf("base_version = %d, want 3", body.BaseVersion)
		}
		writeJSON(w, http.StatusConflict, map[string]any{"code": 409, "msg": "version conflict"})
	})
	_, err := c.UpdateNote(context.Background(), id, "t", "m", model.VisibilityPublic, 3)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("应 ErrConflict: %v", err)
	}
}

// 标签名可能含空格/斜杠，必须做路径转义。
func TestTagNamePathEscaping(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		writeJSON(w, http.StatusOK, []string{})
	})
	if _, err := c.UnbindTag(context.Background(), uuid.New(), "go lang"); err != nil {
		t.Fatalf("UnbindTag: %v", err)
	}
	if !strings.Contains(gotPath, "go%20lang") {
		t.Fatalf("标签名未转义: %q", gotPath)
	}
}

func TestListNotesQuery(t *testing.T) {
	var q map[string]string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": []any{}, "next_cursor": "abc"})
	})
	list, err := c.ListNotes(context.Background(), "cur", 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if q["cursor"] != "cur" || q["limit"] != "10" {
		t.Fatalf("query = %v", q)
	}
	if list.NextCursor != "abc" {
		t.Fatalf("NextCursor = %q", list.NextCursor)
	}
}

func TestSearchNotesQuery(t *testing.T) {
	var q map[string]string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": []any{}})
	})
	if _, err := c.SearchNotes(context.Background(), "关键词", "go", 5, 20); err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if q["q"] != "关键词" || q["tag"] != "go" || q["limit"] != "5" || q["offset"] != "20" {
		t.Fatalf("query = %v", q)
	}
}

func TestSnapshot(t *testing.T) {
	id := uuid.New()
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"notes":     []any{map[string]any{"id": id, "title": "t", "tags": []string{"go"}}},
			"tags":      []any{map[string]any{"id": uuid.New(), "name": "go"}},
			"synced_at": "2026-10-24T10:00:00Z",
		})
	})
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Notes) != 1 || snap.Notes[0].ID != id || len(snap.Tags) != 1 {
		t.Fatalf("快照解析不符: %+v", snap)
	}
}

// 私有客户端即使拿到 404 也不应把正文吞掉（此处只验证错误语义）。
func TestGetNoteNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "msg": "not found"})
	})
	_, err := c.GetNote(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("应 ErrNotFound: %v", err)
	}
}
