package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/krishna2206/zefile/internal/audit"
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

type extractRequest struct {
	Archive string `json:"archive"`
	// Dest is where the new directory is created. Empty means beside the
	// archive, which is what a "extract here" action wants.
	Dest string `json:"dest"`
}

// handleExtract unpacks a ZIP archive into a new directory. Extraction always
// runs as a background job — an archive may be large and its expansion larger —
// so the response is the job to follow, never the finished tree. The worker
// enforces every safety limit; this handler only checks the archive exists and
// hands the work off with the caller's authority recorded on it.
func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	var body extractRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	archive, ok := parsePath(w, r, body.Archive)
	if !ok {
		return
	}

	dest := archive.Parent()
	if body.Dest != "" {
		dest, ok = parsePath(w, r, body.Dest)
		if !ok {
			return
		}
	}

	// Fail obvious mistakes now rather than after a poll: a missing archive, or
	// one that is a directory, should answer immediately.
	info, err := s.fs.Stat(r.Context(), archive)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if info.IsDir {
		writeProblem(w, r, http.StatusBadRequest, CodeIsDirectory,
			"Not an archive", "A directory cannot be extracted.")
		return
	}

	if s.jobs == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, CodeInternal,
			"No worker", "Extraction needs the background worker.")
		return
	}
	j, err := s.jobs.Enqueue(r.Context(), job.TypeExtract, job.ExtractPayload{
		Archive: archive.String(),
		Dest:    dest.String(),
		UserID:  c.user.ID,
		IsAdmin: c.user.IsAdmin,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusAccepted, copyJobResponse{Job: toJobResponse(j)})
}

type fetchRequest struct {
	URL string `json:"url"`
	// Dir is the folder the file lands in. Empty means the root.
	Dir string `json:"dir"`
	// Name overrides the filename; empty derives it from the URL.
	Name string `json:"name"`
}

// handleFetch downloads a URL into storage from the server's own network. It
// always runs as a background job — the file may be very large — so the
// response is the job to follow. The scheme is checked here for an immediate
// rejection; every network-level defence lives in the fetcher the worker runs.
func (s *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	var body fetchRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	u, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Invalid URL", "Provide an http or https URL.")
		return
	}

	dir := storage.Root
	if body.Dir != "" {
		dir, ok = parsePath(w, r, body.Dir)
		if !ok {
			return
		}
	}

	if s.jobs == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, CodeInternal,
			"No worker", "Downloads need the background worker.")
		return
	}
	j, err := s.jobs.Enqueue(r.Context(), job.TypeFetch, job.FetchPayload{
		URL:     u.String(),
		Dir:     dir.String(),
		Name:    strings.TrimSpace(body.Name),
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
	s.audit(r, audit.ActionFileTrashed, p.String(), nil)
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

// maxBundlePaths bounds a single zip request. A larger selection should download
// its containing folder instead, which is one path and walks the same tree.
const maxBundlePaths = 1000

type bundleRequest struct {
	Paths []string `json:"paths"`
}

// handleBundleLink mints a short-lived link to a zip of several items or a
// folder. The archive is streamed by the content origin; this only signs which
// paths it may contain, for which account.
func (s *Server) handleBundleLink(w http.ResponseWriter, r *http.Request) {
	c, found := callerFrom(r.Context())
	if !found {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	var body bundleRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Paths) == 0 {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Nothing selected", "Choose at least one item to download.")
		return
	}
	if len(body.Paths) > maxBundlePaths {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, CodeTooLarge,
			"Too many items", "Select fewer items, or download the folder that contains them.")
		return
	}

	paths := make([]storage.Path, 0, len(body.Paths))
	for _, raw := range body.Paths {
		p, ok := parsePath(w, r, raw)
		if !ok {
			return
		}
		// Authorise read now so an unreadable selection fails fast with a clean
		// status, rather than producing a zip with holes. Stat also confirms the
		// path exists.
		if _, err := s.fs.Stat(r.Context(), p); err != nil {
			writeError(w, r, err)
			return
		}
		paths = append(paths, p)
	}

	token := s.signer.SignBundle(paths, c.user.ID)
	writeJSON(w, r, http.StatusOK, linkResponse{
		URL:       s.contentBase + "/z/" + token + "/" + url.PathEscape(bundleName(paths)),
		ExpiresAt: time.Now().Add(content.DefaultTTL).UTC(),
	})
}

// bundleName is the archive's filename: the single item's name when there is one,
// otherwise a generic name.
func bundleName(paths []storage.Path) string {
	if len(paths) == 1 {
		return paths[0].Name() + ".zip"
	}
	return "zefile.zip"
}

// maxTextPreviewBytes bounds a text preview. Enough for a source file or a log
// tail; a caller wanting the whole of a huge file downloads it.
const maxTextPreviewBytes = 2 << 20 // 2 MiB

type textResponse struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// handleText returns a file's content as text for the preview, same-origin and
// size-capped. It reads through the storage layer, so it authorises like any
// other read and never serves a reserved path.
func (s *Server) handleText(w http.ResponseWriter, r *http.Request) {
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

	file, err := s.fs.Open(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer file.Close()

	// Read one byte past the cap so a file exactly at the cap is not mislabelled
	// truncated, and anything larger is.
	buf, err := io.ReadAll(io.LimitReader(file, maxTextPreviewBytes+1))
	if err != nil {
		writeError(w, r, err)
		return
	}
	truncated := len(buf) > maxTextPreviewBytes
	if truncated {
		buf = buf[:maxTextPreviewBytes]
	}

	// Invalid UTF-8 becomes U+FFFD in the JSON string rather than corrupting the
	// response; the interface only asks for this on text-shaped extensions.
	writeJSON(w, r, http.StatusOK, textResponse{Content: string(buf), Truncated: truncated})
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
