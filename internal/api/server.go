package api

import (
	"net/http"
	"strings"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/content"
	"github.com/krishna2206/zefile/internal/share"
	"github.com/krishna2206/zefile/internal/storage"
	"github.com/krishna2206/zefile/internal/trash"
	"github.com/krishna2206/zefile/internal/upload"
	"github.com/krishna2206/zefile/internal/web"
)

// Server holds everything the handlers need. It owns nothing: the storage
// layer, the ACL engine and the authentication service are built at startup and
// shared, so a handler cannot construct a differently configured one.
type Server struct {
	fs            storage.FS
	auth          *auth.Service
	acl           *acl.Engine
	signer        *content.Signer
	uploads       *upload.Service
	trash         *trash.Service
	shares        *share.Service
	contentBase   string
	singleOrigin  bool
	secureCookies bool
}

// Options configures a [Server].
type Options struct {
	FS      storage.FS
	Auth    *auth.Service
	ACL     *acl.Engine
	Signer  *content.Signer
	Uploads *upload.Service
	Trash   *trash.Service
	Shares  *share.Service

	// ContentBase is the public origin serving files, without a trailing
	// slash. In single-origin mode it is the application's own address.
	ContentBase string

	// SingleOrigin reports that files are served from the application origin,
	// which forces every download to be an attachment — so nothing renders in
	// place, and the interface must not offer an inline preview it cannot show.
	SingleOrigin bool

	SecureCookies bool
}

// New builds the application-origin handler.
func New(opts Options) *Server {
	return &Server{
		fs:            opts.FS,
		auth:          opts.Auth,
		acl:           opts.ACL,
		signer:        opts.Signer,
		uploads:       opts.Uploads,
		trash:         opts.Trash,
		shares:        opts.Shares,
		contentBase:   opts.ContentBase,
		singleOrigin:  opts.SingleOrigin,
		secureCookies: opts.SecureCookies,
	}
}

// Handler returns the routed, wrapped handler.
//
// Routing uses the standard library's pattern matching, so there is no router
// dependency on the request path — the busiest code in the process.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Open endpoints. Setup is reachable without a session by necessity: it is
	// how the first account comes to exist.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetupComplete)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	// Everything else needs a caller.
	authed := http.NewServeMux()
	authed.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	authed.HandleFunc("GET /api/v1/auth/me", s.handleMe)

	authed.HandleFunc("GET /api/v1/fs", s.handleList)
	authed.HandleFunc("GET /api/v1/fs/search", s.handleSearch)
	authed.HandleFunc("GET /api/v1/fs/stat", s.handleStat)
	authed.HandleFunc("DELETE /api/v1/fs", s.handleDelete)
	authed.HandleFunc("POST /api/v1/fs/dirs", s.handleMkdir)
	authed.HandleFunc("POST /api/v1/fs/move", s.handleMove)
	authed.HandleFunc("POST /api/v1/fs/copy", s.handleCopy)
	authed.HandleFunc("GET /api/v1/fs/link", s.handleLink)
	authed.HandleFunc("GET /api/v1/fs/thumb", s.handleThumb)
	authed.HandleFunc("GET /api/v1/fs/space", s.handleSpace)
	authed.HandleFunc("GET /api/v1/config", s.handleConfig)

	authed.HandleFunc("GET /api/v1/trash", s.handleTrashList)
	authed.HandleFunc("DELETE /api/v1/trash", s.handleTrashEmpty)
	authed.HandleFunc("POST /api/v1/trash/{id}/restore", s.handleTrashRestore)
	authed.HandleFunc("DELETE /api/v1/trash/{id}", s.handleTrashPurge)

	authed.HandleFunc("POST /api/v1/shares", s.handleShareCreate)
	authed.HandleFunc("GET /api/v1/shares", s.handleShareList)
	authed.HandleFunc("DELETE /api/v1/shares/{id}", s.handleShareRevoke)

	authed.HandleFunc("OPTIONS /api/v1/uploads", s.handleUploadOptions)
	authed.HandleFunc("POST /api/v1/uploads", s.handleUploadCreate)
	authed.HandleFunc("HEAD /api/v1/uploads/{token}", s.handleUploadOffset)
	authed.HandleFunc("PATCH /api/v1/uploads/{token}", s.handleUploadWrite)
	authed.HandleFunc("DELETE /api/v1/uploads/{token}", s.handleUploadCancel)

	// The interface is served from the root, so anything the API does not claim
	// falls through to it. Registered last, and only when the binary was built
	// with one: a backend-only build still serves the API rather than 404s.
	if ui, ok := web.Handler(); ok {
		mux.Handle("/", s.apiOrInterface(s.requireAuth(authed), ui))
	} else {
		mux.Handle("/", s.requireAuth(authed))
	}

	// Outermost first: recovery has to wrap the logger so that a panic is still
	// logged as a request, and the request id has to exist before either runs.
	return withRequestID(recoverPanics(logRequests(mux)))
}

// apiOrInterface sends API paths to the authenticated handler and everything
// else to the interface.
//
// The split is on the path prefix rather than on content negotiation: an
// unknown /api/v1 path must answer as the API, with a machine-readable error,
// not silently return the HTML shell for a client to choke on.
func (s *Server) apiOrInterface(api, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}
