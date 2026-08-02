package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/krishna2206/zefile/internal/acl"
	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/auth"
)

type updateUserRequest struct {
	// Pointers so "not mentioned" is distinct from "set to false": a request may
	// change the admin flag, the disabled flag, either, or neither.
	IsAdmin  *bool `json:"is_admin"`
	Disabled *bool `json:"disabled"`
}

// handleUpdateUser promotes/demotes or disables/enables an account. Admin only,
// and never your own account: the rule that you cannot touch yourself here is
// also what guarantees the last administrator can never lock themselves out.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := userIDParam(w, r)
	if !ok {
		return
	}
	if id == c.user.ID {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Not your own account", "You cannot change your own account here.")
		return
	}

	var body updateUserRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	if _, err := s.auth.GetUser(r.Context(), id); err != nil {
		writeUserError(w, r, err)
		return
	}

	if body.IsAdmin != nil {
		if err := s.auth.SetUserAdmin(r.Context(), id, *body.IsAdmin); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if body.Disabled != nil {
		if err := s.auth.SetUserDisabled(r.Context(), id, *body.Disabled); err != nil {
			writeError(w, r, err)
			return
		}
	}

	updated, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		writeUserError(w, r, err)
		return
	}
	s.audit(r, audit.ActionUserUpdated, updated.Username, map[string]any{
		"is_admin": updated.IsAdmin, "disabled": updated.Disabled,
	})
	writeJSON(w, r, http.StatusOK, userSummary{
		ID: updated.ID, Username: updated.Username, IsAdmin: updated.IsAdmin, Disabled: updated.Disabled,
	})
}

// handleDeleteUser removes an account and everything that hangs off it. Admin
// only, and never your own account.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := userIDParam(w, r)
	if !ok {
		return
	}
	if id == c.user.ID {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest,
			"Not your own account", "You cannot delete your own account here.")
		return
	}

	target, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		writeUserError(w, r, err)
		return
	}

	// The row goes first — sessions and file ownership cascade with it — then the
	// ACL rules it held, which have no foreign key to cascade through.
	if err := s.auth.DeleteUser(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.acl.RevokeAllForSubject(r.Context(), acl.SubjectUser, id); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionUserDeleted, target.Username, map[string]any{"user_id": id})
	writeJSON(w, r, http.StatusNoContent, nil)
}

func userIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The user id must be a number.")
		return 0, false
	}
	return id, true
}

func writeUserError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, auth.ErrUserNotFound) {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "No such user", "This account does not exist.")
		return
	}
	writeError(w, r, err)
}
