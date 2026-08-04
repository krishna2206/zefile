package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/auth"
)

type requestIDKey struct{}

// RequestID returns the identifier assigned to the request, for correlating a
// log line with what a user saw.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// withRequestID tags every request so that a log line can be tied to a report.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			// Not worth failing a request over; correlation is a convenience.
			raw = []byte("unknown-")
		}
		id := base64.RawURLEncoding.EncodeToString(raw)

		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// recoverPanics turns a panic into a 500 instead of a dropped connection.
//
// Without it, a panic in one handler kills the whole process, taking every
// other in-flight request — including uploads — with it.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// http.ErrAbortHandler is the documented way to drop a
				// connection deliberately, so it is re-raised rather than
				// reported as a failure.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				slog.ErrorContext(r.Context(), "handler panicked",
					"panic", recovered, "method", r.Method, "path", r.URL.Path,
					"request_id", RequestID(r.Context()))
				writeProblem(w, r, http.StatusInternalServerError, CodeInternal,
					"Internal error", "Something went wrong. The details are in the server log.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// logRequests records one line per request.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		// The query string is deliberately omitted: paths travel in it, and a
		// filename is often the very thing a user considers private.
		slog.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"request_id", RequestID(r.Context()),
			"ip", clientIP(r),
		)
	})
}

// requireAuth resolves the caller and refuses anonymous requests.
//
// Two credentials are accepted and resolve to the same subject: the session
// cookie a browser sends, and a bearer token for programmatic clients. Sharing
// one path means a permission check cannot behave differently depending on how
// the caller arrived.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
				token = cookie.Value
			}
		}
		if token == "" {
			writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated,
				"Not signed in", "This endpoint needs a session cookie or a bearer token.")
			return
		}

		// An API token carries its namespace in the value itself, so the kind
		// of credential is known before any lookup. This keeps the two paths
		// from ever being confused: a session token cannot be presented as a
		// bearer API token, nor the reverse.
		if strings.HasPrefix(token, auth.APIPrefix) {
			s.serveWithAPIToken(w, r, token, next)
			return
		}

		session, user, err := s.auth.Lookup(r.Context(), token)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidSession) {
				// Clear the browser's copy so a dead cookie stops being resent
				// on every request for the next thirty days.
				http.SetCookie(w, auth.ClearSessionCookie(s.secureCookies))
			}
			writeError(w, r, err)
			return
		}

		subject, err := s.acl.LoadSubject(r.Context(), user.ID, user.IsAdmin)
		if err != nil {
			writeError(w, r, err)
			return
		}

		ctx := acl.NewContext(r.Context(), subject)
		ctx = withUser(ctx, user, session)

		s.auth.Touch(ctx, session.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// serveWithAPIToken authenticates a request bearing a zefile_live_ token.
//
// It resolves to the same subject a session would, so every downstream
// permission check behaves identically no matter how the caller arrived: a
// token inherits the full authority of the account that owns it, including its
// file and folder access. There is no session row, so the caller carries a
// zero Session — the session-management endpoints a token might reach then act
// on id 0, which matches nothing.
func (s *Server) serveWithAPIToken(w http.ResponseWriter, r *http.Request, token string, next http.Handler) {
	apiToken, user, err := s.auth.LookupAPIToken(r.Context(), token)
	if err != nil {
		writeError(w, r, err)
		return
	}

	subject, err := s.acl.LoadSubject(r.Context(), user.ID, user.IsAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}

	ctx := acl.NewContext(r.Context(), subject)
	ctx = withUser(ctx, user, auth.Session{})

	s.auth.TouchAPIToken(ctx, apiToken.ID)
	next.ServeHTTP(w, r.WithContext(ctx))
}

type userKey struct{}

type caller struct {
	user    auth.User
	session auth.Session
}

func withUser(ctx context.Context, u auth.User, sess auth.Session) context.Context {
	return context.WithValue(ctx, userKey{}, caller{user: u, session: sess})
}

func callerFrom(ctx context.Context) (caller, bool) {
	c, ok := ctx.Value(userKey{}).(caller)
	return c, ok
}

// bearerToken extracts a token from the Authorization header.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

// clientIP reports the address to record and to throttle against.
//
// A forwarded header is trusted only when the direct peer is a private or
// loopback address — the signature of a reverse proxy on the same trusted
// network, which is how nearly every self-hosted instance runs. There, the peer
// is the proxy, not the client, and the header it set carries the real address.
// A direct connection from a public address is used as-is, and its forwarded
// headers ignored, since a client could forge them to spoof an address or slip
// past the login rate limit. This needs no configuration: the private peer is
// the tell.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		if forwarded := forwardedClientIP(r); forwarded != "" {
			return forwarded
		}
	}
	return host
}

// forwardedClientIP extracts the real client address from the headers a reverse
// proxy sets. It prefers X-Real-Ip, then the rightmost entry of X-Forwarded-For
// — the address the trusted proxy itself saw connect, which a client cannot
// forge past a single proxy (the proxy appends it after any spoofed values).
func forwardedClientIP(r *http.Request) string {
	if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); net.ParseIP(real) != nil {
		return real
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if net.ParseIP(last) != nil {
			return last
		}
	}
	return ""
}
