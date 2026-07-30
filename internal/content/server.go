package content

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/krishna2206/zefile/internal/share"
	"github.com/krishna2206/zefile/internal/storage"
)

// SubjectLoader turns an account identifier into a context carrying its
// authorisation subject.
//
// It is an interface so that this package does not depend on the ACL engine or
// on the account store: the content origin needs to know who a link was minted
// for, not how permissions are modelled.
type SubjectLoader interface {
	ContextFor(ctx context.Context, userID int64) (context.Context, error)
}

// Server handles requests on the content origin.
type Server struct {
	fs      storage.FS
	signer  *Signer
	subject SubjectLoader
	shares  share.Resolver

	// singleOrigin hardens every response when the instance serves content from
	// the application origin. See [Options].
	singleOrigin bool
}

// Options configures a [Server].
type Options struct {
	FS      storage.FS
	Signer  *Signer
	Subject SubjectLoader
	Shares  share.Resolver

	// SingleOrigin reports that no separate content host is configured.
	//
	// It turns on the degradation described in the design document: everything
	// is sent as an attachment inside a sandbox, so a file that would otherwise
	// render — an HTML page, an SVG — cannot execute in the origin that holds
	// the session.
	SingleOrigin bool
}

// New builds the content-origin handler.
func New(opts Options) *Server {
	return &Server{fs: opts.FS, signer: opts.Signer, subject: opts.Subject, shares: opts.Shares, singleOrigin: opts.SingleOrigin}
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// The trailing filename is decoration for the client: it is what a download
	// manager uses to name the file on disk. Only the token is authoritative.
	mux.HandleFunc("GET /d/{token}/{name}", s.handleDownload)
	mux.HandleFunc("HEAD /d/{token}/{name}", s.handleDownload)
	mux.HandleFunc("GET /s/{token}/{name}", s.handleShare)
	mux.HandleFunc("HEAD /s/{token}/{name}", s.handleShare)
	return mux
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	p, userID, err := s.signer.Verify(r.PathValue("token"))
	if err != nil {
		// One answer for a forged link and an expired one. Distinguishing them
		// would confirm that a path existed at some point.
		http.Error(w, "link is invalid or has expired", http.StatusForbidden)
		return
	}

	// The permission check runs again here, against the account the link was
	// minted for. A right withdrawn in the meantime therefore takes effect, and
	// this origin gets no ability to read that the application origin lacks.
	ctx, err := s.subject.ContextFor(r.Context(), userID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	file, info, err := s.open(ctx, p)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer file.Close()

	s.setHeaders(w, p, info.Size)

	// ServeContent handles conditional requests and the whole of RFC 9110's
	// range grammar: open ranges, suffix ranges, unsatisfiable ranges, and the
	// 206 and 416 responses. Reimplementing that is how servers end up with
	// download managers that half work.
	//
	// It also streams: memory stays flat whether the file is one kilobyte or
	// forty gigabytes, and on Linux the copy goes through the kernel.
	http.ServeContent(w, r.WithContext(ctx), p.Name(), info.ModTime, file)
}

// handleShare serves a public share link. Unlike a signed download it needs no
// account: the token itself is the capability, and the share carries its own
// expiry, revocation and download limit.
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	grant, err := s.shares.Resolve(r.Context(), r.PathValue("token"))
	if err != nil {
		writeShareError(w, err)
		return
	}

	// The file is read as the account that made the link — a right the owner has
	// lost since takes effect, exactly as for a signed link.
	ctx, err := s.subject.ContextFor(r.Context(), grant.OwnerID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	file, info, err := s.open(ctx, grant.Path)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer file.Close()

	// Count and log the download, but never let a bookkeeping failure deny a
	// legitimate one. A HEAD is a client checking, not a download.
	if r.Method == http.MethodGet {
		_ = s.shares.RecordDownload(ctx, grant.ID, clientIP(r), r.UserAgent())
	}

	s.setHeaders(w, grant.Path, info.Size)
	http.ServeContent(w, r.WithContext(ctx), grant.Path.Name(), info.ModTime, file)
}

// writeShareError answers a token that will not serve. Expired, revoked and
// exhausted are 410 Gone — the holder had a real link that has ended; anything
// else is a 404, so a guessed token cannot be told apart from one that expired.
func writeShareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, share.ErrExpired), errors.Is(err, share.ErrRevoked):
		http.Error(w, "this link is no longer available", http.StatusGone)
	default:
		http.Error(w, "this link is invalid or has expired", http.StatusNotFound)
	}
}

// clientIP is the caller's address for the access log, honouring a reverse
// proxy's X-Forwarded-For before falling back to the connection.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) open(ctx context.Context, p storage.Path) (storage.File, storage.FileInfo, error) {
	info, err := s.fs.Stat(ctx, p)
	if err != nil {
		return nil, storage.FileInfo{}, err
	}
	if info.IsDir {
		return nil, storage.FileInfo{}, storage.ErrIsDir
	}
	file, err := s.fs.Open(ctx, p)
	if err != nil {
		return nil, storage.FileInfo{}, err
	}
	return file, info, nil
}

// setHeaders decides how a browser is allowed to treat the file.
func (s *Server) setHeaders(w http.ResponseWriter, p storage.Path, size int64) {
	// Without this, a browser may ignore the declared type and guess from the
	// bytes — which is how a file uploaded as text ends up executed as script.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Private: the response is addressed to one holder of one short-lived link,
	// and a shared cache keeping it would serve it to whoever asks next.
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")

	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(p.Name())))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	if s.inlineAllowed(contentType) {
		w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+encodeFilename(p.Name()))
	} else {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+encodeFilename(p.Name()))
	}

	if s.singleOrigin {
		// The sandbox strips scripts, forms and same-origin privileges from
		// whatever is rendered, which is what makes serving content from the
		// application origin survivable rather than merely inadvisable.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}

	_ = size // ServeContent sets Content-Length itself, from the seeker.
}

// inlineTypes are the types a browser may render in place.
//
// The list is an allowlist rather than a denylist, and it holds only formats
// the browser isolates itself. SVG is absent deliberately: it is an image that
// executes script, and treating it like the other images is a recurring source
// of cross-site scripting in file managers.
var inlineTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"image/avif":      true,
	"video/mp4":       true,
	"video/webm":      true,
	"audio/mpeg":      true,
	"audio/ogg":       true,
	"audio/wav":       true,
	"audio/flac":      true,
	"application/pdf": true,
	"text/plain":      true,
}

func (s *Server) inlineAllowed(contentType string) bool {
	if s.singleOrigin {
		// Nothing renders in place when content shares the application origin.
		return false
	}
	base, _, _ := strings.Cut(contentType, ";")
	return inlineTypes[strings.TrimSpace(base)]
}

// encodeFilename escapes a name for the RFC 5987 form of Content-Disposition,
// which is what lets an accented or non-Latin filename survive the trip.
func encodeFilename(name string) string {
	var b strings.Builder
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteString("%")
			const hex = "0123456789ABCDEF"
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return // The client hung up; there is nobody to answer.
	case errors.Is(err, storage.ErrNotExist), errors.Is(err, storage.ErrPermission),
		errors.Is(err, storage.ErrReserved), errors.Is(err, storage.ErrIsDir),
		errors.Is(err, ErrUnknownSubject):
		// A link that resolves to something unreadable answers the same way as
		// a forged one. The holder of a link proved nothing about who they are,
		// so they learn nothing about what is there.
		http.Error(w, "link is invalid or has expired", http.StatusForbidden)
	default:
		slog.ErrorContext(r.Context(), "content download failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
