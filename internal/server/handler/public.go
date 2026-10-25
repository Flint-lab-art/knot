package handler

import (
	"net/http"

	"knot/internal/dto"
	"knot/internal/model"
	"knot/internal/store/pg"
)

// ListPublicNotes 处理 GET /api/public/notes。
//
// 查询层强制 Visibility=public；不返回 version/modified_at。
func (a *API) ListPublicNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, page := parsePaging(w, r)
	if limit < 1 {
		return
	}
	notes, err := a.store.ListNotes(r.Context(), pg.ListOptions{
		Visibility: model.VisibilityPublic,
		Tag:        q.Get("tag"),
		Limit:      limit,
		Cursor:     page.cursorAt,
		CursorID:   page.cursorID,
	})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	out := make([]dto.PublicNote, 0, len(notes))
	tagMap, err := a.store.TagsByNote(r.Context(), noteIDs(notes))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}

	for _, n := range notes {
		out = append(out, publicNoteDTO(n, tagMap[n.ID]))
	}
	writeJSON(w, http.StatusOK, dto.PublicNoteList{Notes: out, NextCursor: nextCursor(notes, limit)})
}

// GetPublicNote 处理 GET /api/public/notes/:id。非 public 与不存在都是 404。
func (a *API) GetPublicNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	note, err := a.store.GetPublicNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	tags, err := a.store.ListTagsForNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.PublicNoteDetail{
		PublicNote: publicNoteDTO(note, tags),
		RawMD:      note.RawMD,
	})
}

// ListPublicTags 处理 GET /api/public/tags：只返回至少关联一篇 public 笔记的标签。
func (a *API) ListPublicTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.store.ListPublicTags(r.Context())
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tagDTOs(tags))
}
