package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/auth"
)

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword changes the caller's own password. The current password
// is checked first; on success every other session is ended, so a leaked
// password cannot keep a session alive elsewhere, while the caller stays signed
// in on the device they changed it from.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	var body changePasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	if err := s.auth.ChangePassword(r.Context(), c.user.ID, body.CurrentPassword, body.NewPassword); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			// The wrong current password is about one field, so it is reported
			// beside it rather than as a generic sign-in failure.
			writeFieldProblem(w, r, map[string]string{"current_password": "This is not your current password."})
			return
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionPasswordChanged, "", nil)

	if err := s.auth.RevokeOtherSessions(r.Context(), c.user.ID, c.session.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

type sessionSummary struct {
	ID         int64     `json:"id"`
	Current    bool      `json:"current"`
	UserAgent  string    `json:"user_agent,omitempty"`
	IP         string    `json:"ip,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type sessionListResponse struct {
	Sessions []sessionSummary `json:"sessions"`
}

// handleListSessions returns the caller's live sessions, the current one marked.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	sessions, err := s.auth.ListSessions(r.Context(), c.user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]sessionSummary, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sessionSummary{
			ID:         sess.ID,
			Current:    sess.ID == c.session.ID,
			UserAgent:  sess.UserAgent,
			IP:         sess.IP,
			CreatedAt:  sess.CreatedAt,
			LastSeenAt: sess.LastSeenAt,
		})
	}
	writeJSON(w, r, http.StatusOK, sessionListResponse{Sessions: out})
}

// handleRevokeSession ends one of the caller's sessions. Ending the current one
// is allowed — it is another way to sign out.
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The session id must be a number.")
		return
	}

	if err := s.auth.RevokeSessionForUser(r.Context(), c.user.ID, id); err != nil {
		if errors.Is(err, auth.ErrInvalidSession) {
			writeProblem(w, r, http.StatusNotFound, CodeNotFound, "No such session", "This session is not one of yours.")
			return
		}
		writeError(w, r, err)
		return
	}

	// If they ended the session they are on, clear the cookie too.
	if id == c.session.ID {
		http.SetCookie(w, auth.ClearSessionCookie(s.secureCookies))
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

// handleRevokeOtherSessions signs the caller out of every other device.
func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	if err := s.auth.RevokeOtherSessions(r.Context(), c.user.ID, c.session.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}
