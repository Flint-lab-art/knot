package handler

import (
	"net/http"

	"knot/internal/dto"
)

// Snapshot 处理 GET /api/sync/snapshot。响应不含 raw_md。
func (a *API) Snapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := a.store.Snapshot(r.Context())
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	notes := make([]dto.Note, 0, len(snap.Notes))
	for _, n := range snap.Notes {
		tags := n.Tags
		if tags == nil {
			tags = []string{}
		}
		notes = append(notes, dto.Note{
			ID:         n.ID,
			Title:      n.Title,
			Version:    int64(n.Version),
			Visibility: string(n.Visibility),
			CreatedAt:  n.CreatedAt.UTC(),
			ModifiedAt: n.ModifiedAt.UTC(),
			Tags:       tags,
		})
	}
	writeJSON(w, http.StatusOK, dto.SyncSnapshot{
		Notes:    notes,
		Tags:     tagDTOs(snap.Tags),
		SyncedAt: snap.SyncedAt.UTC(),
	})
}
