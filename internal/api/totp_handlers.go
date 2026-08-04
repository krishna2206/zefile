package api

import (
	"net/http"

	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/auth"
)

// Two-factor authentication is enrolled in three steps: enroll mints a candidate
// secret and the QR the app scans; the client confirms with a code; enable saves
// the secret only once that code proves the user holds it. disable turns it off
// again, checking a code first so a hijacked session cannot remove it silently.

type totpEnrollResponse struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

func (s *Server) handleTOTPEnroll(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, totpEnrollResponse{
		Secret: secret,
		URI:    auth.TOTPProvisioningURI(secret, c.user.Username),
	})
}

type totpEnableRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	var body totpEnableRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.auth.EnableTOTP(r.Context(), c.user.ID, body.Secret, body.Code); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionTOTPEnabled, "", nil)
	writeJSON(w, r, http.StatusNoContent, nil)
}

type totpDisableRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	var body totpDisableRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.auth.DisableTOTP(r.Context(), c.user.ID, body.Code); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionTOTPDisabled, "", nil)
	writeJSON(w, r, http.StatusNoContent, nil)
}
