package content

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"sort"
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
	// A zip of several items, or of a folder, streamed on the fly.
	mux.HandleFunc("GET /z/{token}/{name}", s.handleZip)
	mux.HandleFunc("HEAD /z/{token}/{name}", s.handleZip)
	// A share link carries only its token: the filename would just make the URL
	// long and awkward to paste, and the download's name comes from the
	// Content-Disposition header, not the path. The /{name} form is kept so
	// links already handed out, which included the name, still resolve.
	mux.HandleFunc("GET /s/{token}", s.handleShare)
	mux.HandleFunc("HEAD /s/{token}", s.handleShare)
	mux.HandleFunc("POST /s/{token}", s.handleShare)
	mux.HandleFunc("GET /s/{token}/{name}", s.handleShare)
	mux.HandleFunc("HEAD /s/{token}/{name}", s.handleShare)
	mux.HandleFunc("POST /s/{token}/{name}", s.handleShare)
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

// handleZip streams a zip of the bundle a token names — several files, a folder,
// or a mix. It is generated on the fly: nothing is written to disk and memory
// stays flat whatever the total size.
//
// Compression is off (Store). The content of a file server is mostly
// already-compressed media, where Deflate spends a CPU core to save nothing and
// caps the download at compression speed instead of disk speed — which is what
// makes on-the-fly zips feel broken elsewhere. As a container rather than a
// compressor, the archive streams at the speed the disk and the link allow.
func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	paths, userID, err := s.signer.VerifyBundle(r.PathValue("token"))
	if err != nil {
		http.Error(w, "link is invalid or has expired", http.StatusForbidden)
		return
	}

	ctx, err := s.subject.ContextFor(r.Context(), userID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		name = "zefile.zip"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+encodeFilename(name))
	if s.singleOrigin {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}

	// A zip made on the fly has no length to declare and cannot be ranged into,
	// so a HEAD gets the headers and nothing else.
	if r.Method == http.MethodHead {
		return
	}

	zw := zip.NewWriter(w)
	for _, p := range paths {
		if err := s.addToZip(ctx, zw, p); err != nil {
			// The response has already begun, so the status cannot change. Stop
			// and let the client retry rather than pretend the archive is whole.
			slog.ErrorContext(ctx, "zip: aborted", "path", p.String(), "error", err)
			break
		}
	}
	if err := zw.Close(); err != nil {
		slog.ErrorContext(ctx, "zip: close failed", "error", err)
	}
}

// addToZip writes one selected path into the archive. A file keeps its own name;
// a directory keeps its name as the prefix of everything inside it, so the tree
// unpacks with the same shape it was downloaded from.
func (s *Server) addToZip(ctx context.Context, zw *zip.Writer, sel storage.Path) error {
	info, err := s.fs.Stat(ctx, sel)
	if err != nil {
		return err
	}
	if info.IsDir {
		return s.zipDir(ctx, zw, sel, sel.Name())
	}
	return s.zipFile(ctx, zw, sel, sel.Name(), info)
}

func (s *Server) zipDir(ctx context.Context, zw *zip.Writer, dir storage.Path, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := s.fs.List(ctx, dir) // filtered to what the account may read
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		// Record the empty directory so it is not silently dropped.
		_, err := zw.CreateHeader(&zip.FileHeader{Name: prefix + "/", Method: zip.Store})
		return err
	}
	for _, e := range entries {
		name := prefix + "/" + e.Name
		if e.IsDir {
			if err := s.zipDir(ctx, zw, e.Path, name); err != nil {
				return err
			}
			continue
		}
		if err := s.zipFile(ctx, zw, e.Path, name, e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) zipFile(ctx context.Context, zw *zip.Writer, p storage.Path, name string, info storage.FileInfo) error {
	file, err := s.fs.Open(ctx, p)
	if err != nil {
		// A file that vanished or turned unreadable between listing and now is
		// skipped rather than failing the whole archive.
		if errors.Is(err, storage.ErrNotExist) || errors.Is(err, storage.ErrPermission) {
			return nil
		}
		return err
	}
	defer file.Close()

	part, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: info.ModTime})
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}

// handleShare serves a public share link. Unlike a signed download it needs no
// account: the token itself is the capability, and the share carries its own
// expiry and revocation.
//
// A password-protected link takes two requests without any cookie: a GET shows
// an HTML form, and the form POSTs the password back to the same URL, which then
// streams the file in the response. A download manager cannot fill a form, which
// is why a password link is a "open in a browser" one — the plain, unprotected
// link is the download-manager path.
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	password := ""
	if r.Method == http.MethodPost {
		password = r.PostFormValue("password")
	}

	grant, err := s.shares.Resolve(r.Context(), token, password)
	if err != nil {
		if errors.Is(err, share.ErrPasswordRequired) {
			// A blank form when first met (GET); an error on a wrong attempt.
			if r.Method == http.MethodPost {
				renderPasswordForm(w, "Wrong password.", http.StatusUnauthorized)
			} else {
				renderPasswordForm(w, "", http.StatusOK)
			}
			return
		}
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

	// A folder share can browse into its subtree: the `p` parameter names the
	// path within it. It is confined to the share's root, so no `p` can reach a
	// file the owner has that the share was not meant to expose.
	target := grant.Path
	if raw := r.URL.Query().Get("p"); raw != "" {
		tp, err := storage.ParsePath(raw)
		if err != nil || !withinShare(grant.Path, tp) {
			http.Error(w, "this link is invalid or has expired", http.StatusNotFound)
			return
		}
		target = tp
	}

	info, err := s.fs.Stat(ctx, target)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	if info.IsDir {
		s.serveShareBrowse(w, r, ctx, grant.Path, target)
		return
	}
	s.serveShareFile(w, r, ctx, grant.ID, target)
}

// serveShareFile streams one file of a share, counting the download.
func (s *Server) serveShareFile(w http.ResponseWriter, r *http.Request, ctx context.Context, shareID int64, p storage.Path) {
	file, info, err := s.open(ctx, p)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer file.Close()

	// Count and log the download, but never let a bookkeeping failure deny a
	// legitimate one. A HEAD is a client checking, not a download.
	if r.Method != http.MethodHead {
		_ = s.shares.RecordDownload(ctx, shareID, clientIP(r), r.UserAgent())
	}

	s.setHeaders(w, p, info.Size)
	http.ServeContent(w, r.WithContext(ctx), p.Name(), info.ModTime, file)
}

// serveShareBrowse renders the public listing of a shared folder.
func (s *Server) serveShareBrowse(w http.ResponseWriter, r *http.Request, ctx context.Context, root, dir storage.Path) {
	entries, err := s.fs.List(ctx, dir)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	renderBrowse(w, root, dir, entries)
}

// withinShare reports whether target is the shared root or somewhere beneath it.
func withinShare(root, target storage.Path) bool {
	if root.IsRoot() || target.String() == root.String() {
		return true
	}
	return strings.HasPrefix(target.String(), root.String()+"/")
}

// unlockFormHTML is the whole page shown for a protected link: a single form,
// no scripts, no external resources, so it renders under a strict policy and
// works with the browser's own password manager. {{ERR}} is an optional error
// line, substituted by plain replacement — the CSS holds a "100%", which a
// printf format string would choke on.
const unlockFormHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Protected file · Zefile</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f4f5f2;
font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:#1a1c1b}
.card{background:#fff;border:1px solid #dde1de;border-radius:12px;padding:32px;
width:min(90vw,360px);box-shadow:0 8px 30px rgba(0,0,0,.06)}
h1{font-size:1.15rem;margin:0 0 6px}
p{margin:0 0 16px;color:#5b625e;font-size:.9rem}
.error{color:#b3261e}
input,button{width:100%;box-sizing:border-box;height:40px;border-radius:8px;font-size:1rem}
input{border:1px solid #dde1de;padding:0 12px;margin-bottom:12px;background:#fff;color:inherit}
button{border:0;background:#2f5d50;color:#fff;font-weight:600;cursor:pointer}
button:hover{background:#284f45}
@media(prefers-color-scheme:dark){
body{background:#0f1412;color:#e6e9e6}
.card{background:#171d1a;border-color:#2a322d;box-shadow:none}
p{color:#9aa39d}
input{background:#0f1412;border-color:#2a322d}
button{background:#7fd0bb;color:#06231b}
button:hover{background:#6fbfaa}}
</style></head>
<body><main class="card">
<h1>Protected file</h1>
<p>This link is password-protected.</p>
{{ERR}}<form method="post" action="">
<input type="password" name="password" placeholder="Password" autocomplete="current-password" autofocus required>
<button type="submit">Open</button>
</form>
</main></body></html>`

func renderPasswordForm(w http.ResponseWriter, message string, status int) {
	errLine := ""
	if message != "" {
		errLine = `<p class="error">` + template.HTMLEscapeString(message) + `</p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	// No scripts, only inline styles and a same-origin form post.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(strings.Replace(unlockFormHTML, "{{ERR}}", errLine, 1)))
}

// browseTmpl renders a shared folder's listing. html/template escapes the names
// (which are user-controlled) and the ?p link values in their contexts, so a
// filename cannot break out of the page.
var browseTmpl = template.Must(template.New("browse").Parse(browseHTML))

const browseHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · Zefile</title>
<style>
:root{color-scheme:light dark}
body{margin:0;background:#f4f5f2;color:#1a1c1b;
font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
.wrap{max-width:720px;margin:0 auto;padding:24px 16px 64px}
header{display:flex;align-items:baseline;gap:12px;margin-bottom:16px;
padding-bottom:12px;border-bottom:1px solid #dde1de}
h1{font-size:1.2rem;margin:0;overflow-wrap:anywhere}
.up{margin-left:auto;font-size:.85rem;color:#2f5d50;text-decoration:none;white-space:nowrap}
.up:hover{text-decoration:underline}
.list{list-style:none;margin:0;padding:0}
.list li{display:flex;align-items:center;gap:12px;padding:10px 8px;
border-bottom:1px solid #eceee9}
.list a{color:inherit;text-decoration:none;overflow-wrap:anywhere;flex:1}
.list a:hover{text-decoration:underline}
.list .dir a{color:#2f5d50;font-weight:600}
.size{font-size:.8rem;color:#79837e;white-space:nowrap}
.empty{color:#79837e;font-size:.9rem}
@media(prefers-color-scheme:dark){
body{background:#0f1412;color:#e6e9e6}
header{border-color:#2a322d}.list li{border-color:#1f2723}
.up,.list .dir a{color:#7fd0bb}.size,.empty{color:#9aa39d}}
</style></head>
<body><main class="wrap">
<header><h1>{{.Title}}</h1>{{if .Up}}<a class="up" href="?p={{.Up}}">&uarr; Up</a>{{end}}</header>
<ul class="list">
{{range .Entries}}<li class="{{if .IsDir}}dir{{else}}file{{end}}"><a href="?p={{.P}}">{{.Name}}{{if .IsDir}}/{{end}}</a>{{if .Size}}<span class="size">{{.Size}}</span>{{end}}</li>
{{else}}<li class="empty">This folder is empty.</li>
{{end}}</ul>
</main></body></html>`

type browseView struct {
	Title   string
	Up      string
	Entries []browseRow
}

type browseRow struct {
	Name  string
	P     string
	Size  string
	IsDir bool
}

func renderBrowse(w http.ResponseWriter, root, dir storage.Path, entries []storage.FileInfo) {
	view := browseView{Title: dir.Name()}
	if view.Title == "" {
		view.Title = "Shared folder"
	}
	if !dir.IsRoot() && dir.String() != root.String() {
		view.Up = dir.Parent().String()
	}

	sorted := make([]storage.FileInfo, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].IsDir != sorted[j].IsDir {
			return sorted[i].IsDir // folders first
		}
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	for _, e := range sorted {
		row := browseRow{Name: e.Name, P: e.Path.String(), IsDir: e.IsDir}
		if !e.IsDir {
			row.Size = humanSize(e.Size)
		}
		view.Entries = append(view.Entries, row)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_ = browseTmpl.Execute(w, view)
}

// humanSize renders a byte count the way a file manager does.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
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
