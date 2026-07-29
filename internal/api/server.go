package api

import (
	"net/http"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/storage"
)

// Server holds everything the handlers need. It owns nothing: the storage
// layer, the ACL engine and the authentication service are built at startup and
// shared, so a handler cannot construct a differently configured one.
type Server struct {
	fs            storage.FS
	auth          *auth.Service
	acl           *acl.Engine
	secureCookies bool
}

// Options configures a [Server].
type Options struct {
	FS            storage.FS
	Auth          *auth.Service
	ACL           *acl.Engine
	SecureCookies bool
}

// New builds the application-origin handler.
func New(opts Options) *Server {
	return &Server{
		fs:            opts.FS,
		auth:          opts.Auth,
		acl:           opts.ACL,
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
	authed.HandleFunc("GET /api/v1/fs/stat", s.handleStat)
	authed.HandleFunc("DELETE /api/v1/fs", s.handleDelete)
	authed.HandleFunc("POST /api/v1/fs/dirs", s.handleMkdir)
	authed.HandleFunc("POST /api/v1/fs/move", s.handleMove)
	authed.HandleFunc("POST /api/v1/fs/copy", s.handleCopy)
	authed.HandleFunc("GET /api/v1/fs/space", s.handleSpace)

	mux.Handle("/", s.requireAuth(authed))

	// Outermost first: recovery has to wrap the logger so that a panic is still
	// logged as a request, and the request id has to exist before either runs.
	return withRequestID(recoverPanics(logRequests(mux)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}
