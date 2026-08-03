package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/auth"
)

type invitationResponse struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Token is the invite secret, returned only when the invitation is created.
	// The interface builds the shareable link from it and its own origin; the
	// server stores only its hash and can never show it again.
	Token string `json:"token,omitempty"`
}

func toInvitationResponse(inv auth.Invitation) invitationResponse {
	return invitationResponse{
		ID:        inv.ID,
		Email:     inv.Email,
		CreatedAt: inv.CreatedAt.UTC(),
		ExpiresAt: inv.ExpiresAt.UTC(),
	}
}

type createInvitationRequest struct {
	Email string `json:"email"`
}

// handleInvitationCreate mints an invite link. Admin only: inviting is how the
// set of accounts grows, which is an administrative act.
func (s *Server) handleInvitationCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}

	var body createInvitationRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	token, inv, err := s.auth.Invite(r.Context(), c.user.ID, strings.TrimSpace(body.Email))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := toInvitationResponse(inv)
	out.Token = token
	s.audit(r, audit.ActionInvitationCreate, inv.Email, nil)
	writeJSON(w, r, http.StatusCreated, out)
}

type invitationListResponse struct {
	Invitations []invitationResponse `json:"invitations"`
}

// handleInvitationList returns the open invitations. Admin only.
func (s *Server) handleInvitationList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}

	invs, err := s.auth.ListInvitations(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]invitationResponse, 0, len(invs))
	for _, inv := range invs {
		out = append(out, toInvitationResponse(inv))
	}
	writeJSON(w, r, http.StatusOK, invitationListResponse{Invitations: out})
}

// handleInvitationRevoke cancels an unused invitation. Admin only.
func (s *Server) handleInvitationRevoke(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The invitation id must be a number.")
		return
	}
	if err := s.auth.RevokeInvitation(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionInvitationRevoke, "", map[string]any{"invitation_id": id})
	writeJSON(w, r, http.StatusNoContent, nil)
}

type invitationCheckResponse struct {
	Valid bool   `json:"valid"`
	Email string `json:"email,omitempty"`
}

// handleInvitationCheck reports whether an invite token is still usable, so the
// public accept page can show a dead-link message instead of a form. It is
// unauthenticated by necessity: the invitee has no account yet.
func (s *Server) handleInvitationCheck(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	inv, err := s.auth.CheckInvitation(r.Context(), token)
	if err != nil {
		// A bad token is not an error to the caller — the page simply learns the
		// invite is not valid.
		writeJSON(w, r, http.StatusOK, invitationCheckResponse{Valid: false})
		return
	}
	writeJSON(w, r, http.StatusOK, invitationCheckResponse{Valid: true, Email: inv.Email})
}

type acceptInvitationRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleInvitationAccept consumes an invite and signs the new account in, the
// same shape as completing first-run setup.
func (s *Server) handleInvitationAccept(w http.ResponseWriter, r *http.Request) {
	var body acceptInvitationRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	user, err := s.auth.AcceptInvitation(r.Context(), body.Token, body.Username, body.Password)
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
	s.auditAs(r, user.ID, audit.ActionUserJoined, user.Username, nil)

	codes, _ := s.auth.GenerateRecoveryCodes(r.Context(), user.ID)
	writeJSON(w, r, http.StatusCreated, sessionResponse{
		User:          toUserResponse(user),
		ExpiresAt:     session.ExpiresAt,
		Token:         token,
		RecoveryCodes: codes,
	})
}

// requireAdmin resolves the caller and refuses a non-administrator. It answers
// the response itself on failure and reports whether the handler may continue.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (caller, bool) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return caller{}, false
	}
	if !c.user.IsAdmin {
		writeProblem(w, r, http.StatusForbidden, CodePermissionDenied,
			"Admins only", "This action is restricted to administrators.")
		return caller{}, false
	}
	return c, true
}
