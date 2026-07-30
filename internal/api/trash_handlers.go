package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/krishna2206/zefile/internal/storage"
	"github.com/krishna2206/zefile/internal/trash"
)

type trashItemResponse struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	OriginalPath string `json:"original_path"`
	IsDir        bool   `json:"is_dir"`
	Size         int64  `json:"size"`
	DeletedAt    string `json:"deleted_at"`
}

func toTrashResponse(it trash.Item) trashItemResponse {
	return trashItemResponse{
		ID:           it.ID,
		Name:         it.Name,
		OriginalPath: it.OriginalPath,
		IsDir:        it.IsDir,
		Size:         it.Size,
		DeletedAt:    it.DeletedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	items, err := s.trash.List(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]trashItemResponse, 0, len(items))
	for _, it := range items {
		out = append(out, toTrashResponse(it))
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// handleTrashRestore puts a trashed entry back and re-establishes ownership at
// the destination for the caller who restored it.
func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	id, ok := trashIDParam(w, r)
	if !ok {
		return
	}
	dest, err := s.trash.Restore(r.Context(), id)
	if err != nil {
		writeTrashError(w, r, err)
		return
	}
	if c, ok := callerFrom(r.Context()); ok {
		if err := s.acl.SetOwner(r.Context(), dest, c.user.ID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	id, ok := trashIDParam(w, r)
	if !ok {
		return
	}
	if err := s.trash.Purge(r.Context(), id); err != nil {
		writeTrashError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if err := s.trash.Empty(r.Context()); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

func trashIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Invalid id", "The trash item id must be a positive integer.")
		return 0, false
	}
	return id, true
}

func writeTrashError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, trash.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, CodeNotFound,
			"No such item", "This item is not in the trash, or was already removed.")
	case errors.Is(err, storage.ErrExist):
		writeProblem(w, r, http.StatusConflict, CodeConflict,
			"Already exists", "Something already occupies the original location; move or rename it first.")
	case errors.Is(err, storage.ErrNotExist):
		writeProblem(w, r, http.StatusConflict, CodeConflict,
			"Folder gone", "The folder this item came from no longer exists; recreate it, then restore.")
	default:
		writeError(w, r, err)
	}
}
