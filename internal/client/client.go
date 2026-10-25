// Package client 封装对 knot-server 的 HTTP 调用（Bearer 鉴权、错误码映射、429 重试提示）。
//
// 约束（见 docs/ARCHITECTURE.md）：只依赖 dto，不依赖 store/pg。
// 接口映射见 docs/API.md 的「CLI-API 映射」。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/model"
)

// ErrNotImplemented 保留给后续扩展点；阶段 4 起 client 已全部实现。
var ErrNotImplemented = errors.New("client 尚未实现")

// 错误码（docs/API.md）。用哨兵错误让 CLI 用 errors.Is 分支，不必比数字。
var (
	// ErrUnauthorized token 缺失或错误。
	ErrUnauthorized = errors.New("client: token 无效")
	// ErrNotFound 资源不存在（含已在服务端删除）。
	ErrNotFound = errors.New("client: 资源不存在")
	// ErrConflict base_version 与服务器不一致（或标签重名）。
	ErrConflict = errors.New("client: 版本冲突")
	// ErrRateLimited 触发限流；RateLimitError.RetryAfter 给出建议等待时长。
	ErrRateLimited = errors.New("client: 请求过于频繁")
)

// APIError 是服务端返回的业务错误，对应 docs/API.md 的 {"code":..,"msg":..}。
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("服务端返回错误 %d", e.Code)
	}
	return fmt.Sprintf("%d: %s", e.Code, e.Msg)
}

// Unwrap 把状态码映射成哨兵错误，便于 errors.Is。
func (e *APIError) Unwrap() error {
	switch e.Code {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// RateLimitError 是 429 的详细错误，带建议等待时长。
type RateLimitError struct {
	APIError
	RetryAfter time.Duration
}

// NetworkError 表示请求没到服务端（DNS/连接/超时），与 4xx/5xx 区分开：
// CLI 对前者提示检查网络，对后者提示服务端问题。
type NetworkError struct{ Err error }

func (e *NetworkError) Error() string { return "无法连接 knot-server: " + e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

// Client 是 knot-server 的 HTTP 客户端。
type Client struct {
	baseURL   string
	token     string
	http      *http.Client
	userAgent string
}

// Options 是构造 Client 的可选参数。
type Options struct {
	// HTTP 是底层 http.Client；为 nil 时用 30s 超时的默认客户端。
	HTTP *http.Client
	// UserAgent 默认 "knot-cli"。
	UserAgent string
}

// New 构造客户端。baseURL 末尾的 / 会被去掉。
func New(baseURL, token string, opt Options) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		http:      opt.HTTP,
		userAgent: opt.UserAgent,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.userAgent == "" {
		c.userAgent = "knot-cli"
	}
	return c
}

// do 发请求并解码响应到 out（out 可为 nil）。
//
// 非 2xx 统一转成 *APIError / *RateLimitError；网络错误转成 *NetworkError。
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: 编码请求体: %w", err)
		}
		rd = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return fmt.Errorf("client: 构造请求: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return &NetworkError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// 限流时优先看响应头，避免为了解析 body 读满上限。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &NetworkError{Err: err}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newAPIError(resp, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("client: 解析响应: %w", err)
	}
	return nil
}

// newAPIError 把错误响应转成 APIError；429 额外解析 Retry-After。
func newAPIError(resp *http.Response, raw []byte) error {
	apiErr := APIError{Code: resp.StatusCode}
	// body 解析失败不阻塞错误上报：状态码本身已经够用。
	var e dto.Error
	if json.Unmarshal(raw, &e) == nil {
		apiErr.Code = e.Code
		apiErr.Msg = e.Msg
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{APIError: apiErr, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return &apiErr
}

// parseRetryAfter 支持秒数与 HTTP 日期两种格式（后者少见，按 0 处理）。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// ---------------- 笔记 ----------------

// CreateNote 对应 POST /api/notes。
func (c *Client) CreateNote(ctx context.Context, title, rawMD string, tags []string, vis model.Visibility) (dto.Note, error) {
	if tags == nil {
		tags = []string{}
	}
	body := dto.CreateNoteRequest{Title: title, RawMD: rawMD, Tags: tags, Visibility: string(vis)}
	var out dto.Note
	if err := c.do(ctx, http.MethodPost, "/api/notes", nil, body, &out); err != nil {
		return dto.Note{}, err
	}
	return out, nil
}

// GetNote 对应 GET /api/notes/:id，返回含正文的完整笔记（懒加载入口）。
func (c *Client) GetNote(ctx context.Context, id uuid.UUID) (dto.NoteDetail, error) {
	var out dto.NoteDetail
	if err := c.do(ctx, http.MethodGet, "/api/notes/"+id.String(), nil, nil, &out); err != nil {
		return dto.NoteDetail{}, err
	}
	return out, nil
}

// UpdateNote 对应 PUT /api/notes/:id。版本冲突返回 ErrConflict。
func (c *Client) UpdateNote(ctx context.Context, id uuid.UUID, title, rawMD string, vis model.Visibility, baseVersion int64) (dto.Note, error) {
	body := dto.UpdateNoteRequest{
		Title: title, RawMD: rawMD, Visibility: string(vis), BaseVersion: baseVersion,
	}
	var out dto.Note
	if err := c.do(ctx, http.MethodPut, "/api/notes/"+id.String(), nil, body, &out); err != nil {
		return dto.Note{}, err
	}
	return out, nil
}

// DeleteNote 对应 DELETE /api/notes/:id，服务端幂等（不存在也成功）。
func (c *Client) DeleteNote(ctx context.Context, id uuid.UUID) error {
	return c.do(ctx, http.MethodDelete, "/api/notes/"+id.String(), nil, nil, nil)
}

// ListNotes 对应 GET /api/notes，游标分页。cursor 为空表示第一页。
func (c *Client) ListNotes(ctx context.Context, cursor string, limit int) (dto.NoteList, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var out dto.NoteList
	if err := c.do(ctx, http.MethodGet, "/api/notes", q, nil, &out); err != nil {
		return dto.NoteList{}, err
	}
	return out, nil
}

// SearchNotes 对应 GET /api/search（pg_trgm 全文检索，offset 分页）。
func (c *Client) SearchNotes(ctx context.Context, keyword, tag string, limit, offset int) (dto.NoteList, error) {
	q := url.Values{}
	if keyword != "" {
		q.Set("q", keyword)
	}
	if tag != "" {
		q.Set("tag", tag)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	var out dto.NoteList
	if err := c.do(ctx, http.MethodGet, "/api/search", q, nil, &out); err != nil {
		return dto.NoteList{}, err
	}
	return out, nil
}

// ---------------- 标签 ----------------

// ListTags 对应 GET /api/tags。
func (c *Client) ListTags(ctx context.Context) ([]dto.Tag, error) {
	var out []dto.Tag
	if err := c.do(ctx, http.MethodGet, "/api/tags", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BindTag 对应 POST /api/notes/:id/tags，返回笔记的最新标签集合。
func (c *Client) BindTag(ctx context.Context, id uuid.UUID, name string) ([]string, error) {
	var out []string
	if err := c.do(ctx, http.MethodPost, "/api/notes/"+id.String()+"/tags", nil, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UnbindTag 对应 DELETE /api/notes/:id/tags/:tag_name，返回最新标签集合。
func (c *Client) UnbindTag(ctx context.Context, id uuid.UUID, name string) ([]string, error) {
	var out []string
	if err := c.do(ctx, http.MethodDelete, "/api/notes/"+id.String()+"/tags/"+url.PathEscape(name), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RenameTag 对应 PUT /api/tags/:old_name。目标名已存在时返回 ErrConflict。
func (c *Client) RenameTag(ctx context.Context, oldName, newName string) error {
	return c.do(ctx, http.MethodPut, "/api/tags/"+url.PathEscape(oldName), nil,
		map[string]string{"name": newName}, nil)
}

// ---------------- 同步 ----------------

// Snapshot 对应 GET /api/sync/snapshot，不含正文。
func (c *Client) Snapshot(ctx context.Context) (dto.SyncSnapshot, error) {
	var out dto.SyncSnapshot
	if err := c.do(ctx, http.MethodGet, "/api/sync/snapshot", nil, nil, &out); err != nil {
		return dto.SyncSnapshot{}, err
	}
	return out, nil
}

// ---------------- 公开接口（只读；CLI 一般用私有接口） ----------------

// ListPublicNotes 对应 GET /api/public/notes。
func (c *Client) ListPublicNotes(ctx context.Context, tag, cursor string, limit int) (dto.PublicNoteList, error) {
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out dto.PublicNoteList
	if err := c.do(ctx, http.MethodGet, "/api/public/notes", q, nil, &out); err != nil {
		return dto.PublicNoteList{}, err
	}
	return out, nil
}

// GetPublicNote 对应 GET /api/public/notes/:id，非 public 返回 ErrNotFound。
func (c *Client) GetPublicNote(ctx context.Context, id uuid.UUID) (dto.PublicNoteDetail, error) {
	var out dto.PublicNoteDetail
	if err := c.do(ctx, http.MethodGet, "/api/public/notes/"+id.String(), nil, nil, &out); err != nil {
		return dto.PublicNoteDetail{}, err
	}
	return out, nil
}
