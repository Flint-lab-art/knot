package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/model"
	"knot/internal/store/pg"
)

// maxBodyBytes 是请求体上限（docs/SECURITY.md：请求体大小限制）。
// 正文是 Markdown，8 MiB 足够；超出返回 413。
const maxBodyBytes = 8 << 20

// 错误消息。对外文案固定，不含 SQL/内部细节。
const (
	msgBadBody     = "invalid request body"
	msgBadID       = "invalid note id"
	msgBadVis      = "invalid visibility"
	msgBadVersion  = "invalid base_version"
	msgMissingName = "missing name"
	msgNotFound    = "not found"
	msgConflict    = "version conflict"
	msgTagExists   = "tag already exists"
	msgInternal    = "internal error"
	msgTooLarge    = "request body too large"
	msgBadCursor   = "invalid cursor"
)

// CreateNote 处理 POST /api/notes。
func (a *API) CreateNote(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateNoteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	vis, ok := parseVisibility(w, req.Visibility, model.VisibilityPrivate)
	if !ok {
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "missing title")
		return
	}

	note, err := a.store.CreateNote(r.Context(), req.Title, req.RawMD, req.Tags, vis)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	tags, err := a.store.ListTagsForNote(r.Context(), note.ID)
	if err != nil {
		// 笔记已写入，标签读取失败只影响响应体里的 tags；返回空列表并记日志。
		slog.Error("list tags after create", "note_id", note.ID, "err", err)
		tags = []string{}
	}
	writeJSON(w, http.StatusCreated, noteDTO(note, tags))
}

// GetNote 处理 GET /api/notes/:id，返回含 raw_md 的完整笔记。
func (a *API) GetNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	note, err := a.store.GetNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	tags, err := a.store.ListTagsForNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.NoteDetail{Note: noteDTO(note, tags), RawMD: note.RawMD})
}

// UpdateNote 处理 PUT /api/notes/:id。base_version 不匹配返回 409。
func (a *API) UpdateNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	var req dto.UpdateNoteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// base_version 必须显式给出且 >= 1：缺省等同于 0，容易掩盖客户端 bug，
	// 也会与「版本从 1 开始」的语义冲突。
	if req.BaseVersion < 1 {
		writeErr(w, http.StatusBadRequest, msgBadVersion)
		return
	}
	vis, ok := parseVisibility(w, req.Visibility, "")
	if !ok {
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "missing title")
		return
	}

	note, err := a.store.UpdateNote(r.Context(), id, req.Title, req.RawMD, vis, req.BaseVersion)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	tags, err := a.store.ListTagsForNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, noteDTO(note, tags))
}

// DeleteNote 处理 DELETE /api/notes/:id。不存在也返回 204（幂等）。
func (a *API) DeleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteNote(r.Context(), id); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListNotes 处理 GET /api/notes，游标分页，不含 raw_md。
func (a *API) ListNotes(w http.ResponseWriter, r *http.Request) {
	limit, page := parsePaging(w, r)
	if limit < 1 {
		return
	}
	notes, err := a.store.ListNotes(r.Context(), pg.ListOptions{
		Limit:    limit,
		Cursor:   page.cursorAt,
		CursorID: page.cursorID,
	})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	a.writeNoteList(w, r, notes, limit)
}

// SearchNotes 处理 GET /api/search?q=&tag=。搜索用 offset 分页。
func (a *API) SearchNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, ok := parseLimitOffset(w, q.Get("limit"), q.Get("offset"))
	if !ok {
		return
	}
	notes, err := a.store.ListNotes(r.Context(), pg.ListOptions{
		Keywords: q.Get("q"),
		Tag:      q.Get("tag"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	a.writeNoteList(w, r, notes, limit)
}

// writeNoteList 补标签并输出分页列表，同时给出下一页游标。
func (a *API) writeNoteList(w http.ResponseWriter, r *http.Request, notes []pg.Note, limit int) {
	tagMap, err := a.store.TagsByNote(r.Context(), noteIDs(notes))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	out := make([]dto.Note, 0, len(notes))
	for _, n := range notes {
		out = append(out, noteDTO(n, tagMap[n.ID]))
	}
	writeJSON(w, http.StatusOK, dto.NoteList{Notes: out, NextCursor: nextCursor(notes, limit)})
}

func noteDTO(n pg.Note, tags []string) dto.Note {
	if tags == nil {
		tags = []string{}
	}
	return dto.Note{
		ID:         n.ID,
		Title:      n.Title,
		Version:    n.Version,
		Visibility: string(n.Visibility),
		CreatedAt:  n.CreatedAt.UTC(),
		ModifiedAt: n.ModifiedAt.UTC(),
		Tags:       tags,
	}
}

func publicNoteDTO(n pg.Note, tags []string) dto.PublicNote {
	if tags == nil {
		tags = []string{}
	}
	return dto.PublicNote{ID: n.ID, Title: n.Title, CreatedAt: n.CreatedAt.UTC(), Tags: tags}
}

func tagDTOs(tags []pg.Tag) []dto.Tag {
	out := make([]dto.Tag, len(tags))
	for i, t := range tags {
		out[i] = dto.Tag{ID: t.ID, Name: t.Name}
	}
	return out
}

// parseVisibility 解析可见性；空串用 def（创建时默认 private，更新时必须显式给出）。
func parseVisibility(w http.ResponseWriter, raw string, def model.Visibility) (model.Visibility, bool) {
	if raw == "" {
		if def == "" {
			writeErr(w, http.StatusBadRequest, msgBadVis)
			return "", false
		}
		return def, true
	}
	v := model.Visibility(raw)
	if !v.Valid() {
		writeErr(w, http.StatusBadRequest, msgBadVis)
		return "", false
	}
	return v, true
}

// noteID 解析路径里的 UUID，非法时写 400。
func noteID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, msgBadID)
		return uuid.Nil, false
	}
	return id, true
}

// decodeJSON 解码请求体；失败时写 400/413 并返回 false。
// 拒绝未知字段：拼错的字段（如 base_version 写成 baseVersion）会静默失效。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, msgTooLarge)
			return false
		}
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return false
	}
	// 多余内容（第二个 JSON 值）同样是客户端 bug。
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return false
	}
	return true
}

// nameBody 是标签操作的请求体。
type nameBody struct {
	Name string `json:"name"`
}

func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var b nameBody
	if !decodeJSON(w, r, &b) {
		return "", false
	}
	if b.Name == "" {
		writeErr(w, http.StatusBadRequest, msgMissingName)
		return "", false
	}
	return b.Name, true
}

func decodeRename(w http.ResponseWriter, r *http.Request) (nameBody, bool) {
	var b nameBody
	if !decodeJSON(w, r, &b) {
		return nameBody{}, false
	}
	if b.Name == "" {
		writeErr(w, http.StatusBadRequest, msgMissingName)
		return nameBody{}, false
	}
	return b, true
}

// parseLimitOffset 解析 limit/offset，非法值写 400 并返回 ok=false。
func parseLimitOffset(w http.ResponseWriter, rawLimit, rawOffset string) (limit, offset int, ok bool) {
	limit = pg.DefaultLimit
	if rawLimit != "" {
		v, err := strconv.Atoi(rawLimit)
		if err != nil || v < 1 {
			writeErr(w, http.StatusBadRequest, "invalid limit")
			return 0, 0, false
		}
		limit = pg.NormalizeLimit(v)
	}
	if rawOffset != "" {
		v, err := strconv.Atoi(rawOffset)
		if err != nil || v < 0 {
			writeErr(w, http.StatusBadRequest, "invalid offset")
			return 0, 0, false
		}
		offset = v
	}
	return limit, offset, true
}

// parsePaging 解析游标分页参数；limit 非法时已写响应，返回 limit=0 表示中止。
func parsePaging(w http.ResponseWriter, r *http.Request) (int, cursorPage) {
	q := r.URL.Query()
	limit, _, ok := parseLimitOffset(w, q.Get("limit"), "")
	if !ok {
		return 0, cursorPage{}
	}
	page := cursorPage{}
	if raw := q.Get("cursor"); raw != "" {
		at, id, err := decodeCursor(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, msgBadCursor)
			return 0, cursorPage{}
		}
		page.cursorAt, page.cursorID = at, &id
	}
	return limit, page
}

// writeStoreErr 把 store 错误映射成统一错误响应，并记录 5xx 的原始错误。
func writeStoreErr(w http.ResponseWriter, r *http.Request, err error) {
	status := statusFor(err)
	if status == http.StatusInternalServerError {
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, msgInternal)
		return
	}
	msg := msgNotFound
	switch status {
	case http.StatusConflict:
		if errors.Is(err, pg.ErrTagExists) {
			msg = msgTagExists
		} else {
			msg = msgConflict
		}
	}
	writeErr(w, status, msg)
}

// writeErr 输出统一错误结构。
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, dto.Error{Code: code, Msg: msg})
}

// writeJSON 输出 JSON 响应。内部错误由调用方先记日志。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write json response", "err", err)
	}
}

// noteIDs 抽出笔记 id，供批量取标签。
func noteIDs(notes []pg.Note) []uuid.UUID {
	ids := make([]uuid.UUID, len(notes))
	for i, n := range notes {
		ids[i] = n.ID
	}
	return ids
}

// nextCursor 在本页恰好取满 limit 条时给出下一页游标。
//
// 没有多取一条来判断「是否真的有下一页」：那需要改查询与 limit 语义，
// 且最末页多一次空查询的代价可以忽略（客户端拿到空 next 就停）。
func nextCursor(notes []pg.Note, limit int) string {
	if len(notes) == 0 || len(notes) < limit {
		return ""
	}
	last := notes[len(notes)-1]
	return encodeCursor(last.ModifiedAt, last.ID)
}
