package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/krishna2206/zefile/internal/auth"
)

// maxBodyBytes bounds a JSON request. None of these endpoints takes anything
// large, and an unbounded body is a way to make the server allocate on demand.
const maxBodyBytes = 64 << 10

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email,omitempty"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type sessionResponse struct {
	User      userResponse `json:"user"`
	ExpiresAt time.Time    `json:"expires_at"`

	// Token is returned so a programmatic client has something to send as a
	// bearer credential. A browser ignores it and uses the cookie, which is
	// what keeps the value out of reach of any script on the page.
	Token string `json:"token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	user, err := s.auth.Authenticate(r.Context(), body.Username, body.Password, clientIP(r))
	if err != nil {
		writeError(w, r, err)
		return
	}

	token, session, err := s.auth.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		writeError(w, r, err)
		return
	}

	http.SetCookie(w, auth.SessionCookie(token, session.ExpiresAt, s.secureCookies))
	writeJSON(w, r, http.StatusOK, sessionResponse{
		User:      toUserResponse(user),
		ExpiresAt: session.ExpiresAt,
		Token:     token,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	// Revoke by identifier rather than by token: the caller may have arrived
	// with a bearer credential, and the row is what actually ends the session.
	if err := s.auth.RevokeSession(r.Context(), c.session.ID); err != nil {
		writeError(w, r, err)
		return
	}

	http.SetCookie(w, auth.ClearSessionCookie(s.secureCookies))
	writeJSON(w, r, http.StatusNoContent, nil)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	writeJSON(w, r, http.StatusOK, toUserResponse(c.user))
}

type setupStatusResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

// handleSetupStatus lets the interface decide between a sign-in form and a
// first-run form. It reveals only whether any account exists, which is already
// evident from the fact that signing in is impossible.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	needed, err := s.auth.NeedsSetup(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, setupStatusResponse{NeedsSetup: needed})
}

type setupRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	var body setupRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	user, err := s.auth.CompleteSetup(r.Context(), body.Token, body.Username, body.Password)
	if err != nil {
		if isValidationError(err) {
			writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Invalid account details", err.Error())
			return
		}
		writeError(w, r, err)
		return
	}

	token, session, err := s.auth.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		writeError(w, r, err)
		return
	}

	http.SetCookie(w, auth.SessionCookie(token, session.ExpiresAt, s.secureCookies))
	writeJSON(w, r, http.StatusCreated, sessionResponse{
		User:      toUserResponse(user),
		ExpiresAt: session.ExpiresAt,
		Token:     token,
	})
}

// isValidationError separates a rejected password from a broken setup token.
// Both come back from CompleteSetup, and answering 403 to "your password is too
// short" would send the user looking for the wrong problem.
func isValidationError(err error) bool {
	return !errors.Is(err, auth.ErrInvalidSetupToken) &&
		!errors.Is(err, auth.ErrAlreadySetUp) &&
		!errors.Is(err, auth.ErrRateLimited)
}

func toUserResponse(u auth.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		IsAdmin:   u.IsAdmin,
		CreatedAt: u.CreatedAt,
	}
}

// decodeJSON reads a request body, answering the client on failure. It reports
// whether the caller should continue.
func decodeJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	decoder := json.NewDecoder(r.Body)
	// Unknown fields are refused rather than ignored: a client sending
	// "recursive" where the field is called "recurse" should be told, not
	// silently given the default.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, CodeTooLarge,
				"Request too large", "The request body is larger than this endpoint accepts.")
			return false
		}
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Malformed request", "The request body is not valid JSON for this endpoint.")
		return false
	}
	if decoder.More() {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Malformed request", "The request body holds more than one JSON value.")
		return false
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return true // Body already decoded; a drain failure changes nothing.
	}
	return true
}
