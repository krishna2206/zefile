package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/krishna2206/zefile/internal/share"
)

type createShareRequest struct {
	Path           string `json:"path"`
	ExpiresInHours int    `json:"expires_in_hours,omitempty"` // 0 means never
	Password       string `json:"password,omitempty"`         // empty means no password
}

type shareResponse struct {
	ID            int64  `json:"id"`
	URL           string `json:"url,omitempty"` // present only at creation — the token is shown once
	Path          string `json:"path"`
	Name          string `json:"name"`
	HasPassword   bool   `json:"has_password"`
	DownloadCount int64  `json:"download_count"`
	CreatedAt     string `json:"created_at"`
	ExpiresAt     string `json:"expires_at,omitempty"`
}

func (s *Server) toShareResponse(sh share.Share, token string) shareResponse {
	resp := shareResponse{
		ID:            sh.ID,
		Path:          sh.Path.String(),
		Name:          sh.Path.Name(),
		HasPassword:   sh.HasPassword,
		DownloadCount: sh.DownloadCount,
		CreatedAt:     sh.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !sh.ExpiresAt.IsZero() {
		resp.ExpiresAt = sh.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if token != "" {
		// Just the token: the browser and download managers name the file from
		// the Content-Disposition header, so the path needs no filename.
		resp.URL = s.contentBase + "/s/" + token
	}
	return resp
}

func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	var body createShareRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	p, ok := parsePath(w, r, body.Path)
	if !ok {
		return
	}

	opts := share.CreateOptions{Password: body.Password}
	if body.ExpiresInHours > 0 {
		opts.ExpiresAt = time.Now().Add(time.Duration(body.ExpiresInHours) * time.Hour)
	}

	token, sh, err := s.shares.Create(r.Context(), c.user.ID, p, opts)
	if err != nil {
		if errors.Is(err, share.ErrNotFile) {
			writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
				"Cannot share this", "Only files can be shared, not folders.")
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, s.toShareResponse(sh, token))
}

func (s *Server) handleShareList(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	shares, err := s.shares.List(r.Context(), c.user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]shareResponse, 0, len(shares))
	for _, sh := range shares {
		out = append(out, s.toShareResponse(sh, ""))
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"shares": out})
}

func (s *Server) handleShareRevoke(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Invalid id", "The share id must be a positive integer.")
		return
	}
	if err := s.shares.Revoke(r.Context(), c.user.ID, id); err != nil {
		if errors.Is(err, share.ErrNotFound) {
			writeProblem(w, r, http.StatusNotFound, CodeNotFound,
				"No such link", "This share does not exist or is not yours.")
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}
