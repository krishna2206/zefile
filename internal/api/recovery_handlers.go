package api

import "net/http"

// Recovery codes are the emailless way to reset a forgotten password. A fresh
// set is issued when an account is created; a user can regenerate the set from
// Settings, and use one code to reset their password from the sign-in screen.

type resetPasswordRequest struct {
	Username    string `json:"username"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

// handleResetPassword sets a new password given a username and a recovery code.
// It is public, like sign-in, and deliberately vague about why it failed so it
// cannot be used to tell which usernames exist.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body resetPasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.auth.ResetPasswordWithCode(r.Context(), body.Username, body.Code, body.NewPassword, clientIP(r)); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

type recoveryStatusResponse struct {
	Remaining int `json:"remaining"`
}

// handleRecoveryStatus reports how many unused recovery codes the caller has.
func (s *Server) handleRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	n, err := s.auth.RecoveryCodesRemaining(r.Context(), c.user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, recoveryStatusResponse{Remaining: n})
}

type recoveryCodesResponse struct {
	Codes []string `json:"codes"`
}

// handleRegenerateRecovery replaces the caller's recovery codes and returns the
// new set once. Any previous codes stop working immediately.
func (s *Server) handleRegenerateRecovery(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	codes, err := s.auth.GenerateRecoveryCodes(r.Context(), c.user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, recoveryCodesResponse{Codes: codes})
}
