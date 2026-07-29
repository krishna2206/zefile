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
// Forwarded headers are deliberately ignored. They are trivially forged, and
// trusting them without knowing which proxy sits in front would let anyone
// reset their own rate limit by inventing an address.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
