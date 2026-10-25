package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ListTags 处理 GET /api/tags。
func (a *API) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.store.ListTags(r.Context())
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tagDTOs(tags))
}

// BindTag 处理 POST /api/notes/:id/tags，body 为 {"name":"go"}。
func (a *API) BindTag(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	if err := a.store.BindTag(r.Context(), id, name); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	tags, err := a.store.ListTagsForNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

// UnbindTag 处理 DELETE /api/notes/:id/tags/:tag_name。幂等。
func (a *API) UnbindTag(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "tag_name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, msgMissingName)
		return
	}
	if err := a.store.UnbindTag(r.Context(), id, name); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	tags, err := a.store.ListTagsForNote(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

// RenameTag 处理 PUT /api/tags/:old_name，body 为 {"name":"new"}。
func (a *API) RenameTag(w http.ResponseWriter, r *http.Request) {
	oldName := chi.URLParam(r, "old_name")
	if oldName == "" {
		writeErr(w, http.StatusBadRequest, msgMissingName)
		return
	}
	req, ok := decodeRename(w, r)
	if !ok {
		return
	}
	if err := a.store.RenameTag(r.Context(), oldName, req.Name); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}
