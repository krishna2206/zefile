package api

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/krishna2206/zefile/internal/content"
	"github.com/krishna2206/zefile/internal/job"
	"github.com/krishna2206/zefile/internal/storage"
)

type entryResponse struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
	Symlink bool      `json:"symlink,omitempty"`
}

type listResponse struct {
	Path    string          `json:"path"`
	Entries []entryResponse `json:"entries"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(w, r)
	if !ok {
		return
	}

	entries, err := s.fs.List(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Sorted here rather than left to the filesystem's order, which is
	// arbitrary and differs between systems. Directories lead, then names,
	// which is the ordering every file manager uses.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	out := make([]entryResponse, 0, len(entries))
	for _, entry := range entries {
		out = append(out, toEntryResponse(entry))
	}
	writeJSON(w, r, http.StatusOK, listResponse{Path: p.String(), Entries: out})
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(w, r)
	if !ok {
		return
	}

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toEntryResponse(info))
}

type pathRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var body pathRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	p, ok := parsePath(w, r, body.Path)
	if !ok {
		return
	}

	// MkdirAll rather than Mkdir: creating a nested path in one call is what
	// clients want, and the alternative is a client loop that races itself.
	if err := s.fs.MkdirAll(r.Context(), p); err != nil {
		writeError(w, r, err)
		return
	}

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toEntryResponse(info))
}

type moveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var body moveRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	from, ok := parsePath(w, r, body.From)
	if !ok {
		return
	}
	to, ok := parsePath(w, r, body.To)
	if !ok {
		return
	}

	if err := s.fs.Move(r.Context(), from, to); err != nil {
		writeError(w, r, err)
		return
	}

	// Ownership and permission rules are keyed by path, so a rename has to carry
	// them across or ownership is orphaned and a shared folder silently loses the
	// rules that granted access to it.
	if err := s.acl.MoveOwner(r.Context(), from, to); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.acl.MoveRules(r.Context(), from, to); err != nil {
		writeError(w, r, err)
		return
	}

	info, err := s.fs.Stat(r.Context(), to)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toEntryResponse(info))
}

func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	var body moveRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	from, ok := parsePath(w, r, body.From)
	if !ok {
		return
	}
	to, ok := parsePath(w, r, body.To)
	if !ok {
		return
	}

	err := s.fs.Copy(r.Context(), from, to)

	// A directory or a very large file cannot be copied inside a request. Rather
	// than refuse, hand it to the background worker and answer with the job the
	// interface can follow.
	if errors.Is(err, storage.ErrIsDir) || errors.Is(err, storage.ErrTooLarge) {
		s.enqueueCopy(w, r, from, to)
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}

	// The copy is a new file, and whoever made it owns it — not whoever owned
	// the original.
	if c, found := callerFrom(r.Context()); found {
		if err := s.acl.SetOwner(r.Context(), to, c.user.ID); err != nil {
			writeError(w, r, err)
			return
		}
	}

	info, err := s.fs.Stat(r.Context(), to)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toEntryResponse(info))
}

// copyJobResponse wraps the job so the interface can tell an accepted background
// copy apart from a synchronous one, which returns the created entry instead.
type copyJobResponse struct {
	Job jobResponse `json:"job"`
}

// enqueueCopy records a background copy job and answers 202 with it. The caller
// is recorded on the job so the worker copies with the same authority.
func (s *Server) enqueueCopy(w http.ResponseWriter, r *http.Request, from, to storage.Path) {
	c, found := callerFrom(r.Context())
	if !found {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	if s.jobs == nil {
		writeError(w, r, storage.ErrIsDir) // no queue wired: report the original refusal
		return
	}

	j, err := s.jobs.Enqueue(r.Context(), job.TypeCopy, job.CopyPayload{
		From:    from.String(),
		To:      to.String(),
		UserID:  c.user.ID,
		IsAdmin: c.user.IsAdmin,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusAccepted, copyJobResponse{Job: toJobResponse(j)})
}

// handleDelete moves an entry to the trash rather than erasing it.
//
// A directory and everything under it go together in a single rename, so the
// `recursive` flag the API once required no longer means anything — trashing a
// tree is atomic, and a client that still sends the flag is simply ignored.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(w, r)
	if !ok {
		return
	}
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	if err := s.trash.Trash(r.Context(), c.user.ID, p); err != nil {
		writeError(w, r, err)
		return
	}

	// Ownership is keyed on the path, which the entry no longer occupies.
	// Restoring re-establishes it at the destination.
	if err := s.acl.ClearOwner(r.Context(), p); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

type linkResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleLink mints a short-lived download URL on the content origin.
//
// The caller's read permission is checked here, by opening the file through the
// storage layer rather than by asking the ACL engine directly — that way the
// answer cannot drift from what an actual download would do.
func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(w, r)
	if !ok {
		return
	}

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if info.IsDir {
		writeError(w, r, storage.ErrIsDir)
		return
	}

	c, found := callerFrom(r.Context())
	if !found {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	token := s.signer.Sign(p, c.user.ID)
	writeJSON(w, r, http.StatusOK, linkResponse{
		// The filename rides at the end of the path because that is what a
		// download manager names the saved file from.
		URL:       s.contentBase + "/d/" + token + "/" + url.PathEscape(p.Name()),
		ExpiresAt: time.Now().Add(content.DefaultTTL).UTC(),
	})
}

type spaceResponse struct {
	Available uint64 `json:"available"`
	Total     uint64 `json:"total"`
	Reserve   uint64 `json:"reserve"`
	ReadOnly  bool   `json:"read_only"`
}

func (s *Server) handleSpace(w http.ResponseWriter, r *http.Request) {
	info, err := s.fs.Space(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, spaceResponse{
		Available: info.Available,
		Total:     info.Total,
		Reserve:   info.Reserve,
		ReadOnly:  info.ReadOnly,
	})
}

type configResponse struct {
	// InlinePreview reports whether files are served inline, so the interface
	// can render an image or PDF in place instead of only downloading it.
	InlinePreview bool `json:"inline_preview"`

	// Version is the running build, shown in the interface.
	Version string `json:"version"`
}

// handleConfig reports the instance capabilities the interface has to adapt to.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, configResponse{InlinePreview: !s.singleOrigin, Version: s.version})
}

// pathParam reads and validates the path query parameter, defaulting to the
// root so that listing the top of the tree needs no argument.
func pathParam(w http.ResponseWriter, r *http.Request) (storage.Path, bool) {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		raw = "/"
	}
	return parsePath(w, r, raw)
}

func parsePath(w http.ResponseWriter, r *http.Request, raw string) (storage.Path, bool) {
	p, err := storage.ParsePath(raw)
	if err != nil {
		writeError(w, r, err)
		return storage.Path{}, false
	}
	return p, true
}

func toEntryResponse(info storage.FileInfo) entryResponse {
	return entryResponse{
		Path:    info.Path.String(),
		Name:    info.Name,
		Size:    info.Size,
		ModTime: info.ModTime.UTC(),
		IsDir:   info.IsDir,
		Symlink: info.Symlink,
	}
}
