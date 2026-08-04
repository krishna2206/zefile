package api

import (
	"errors"
	"net/http"

	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/settings"
)

// Retention is configured at runtime rather than through the environment, so an
// administrator can change how long the audit log and trash are kept without a
// redeploy. Both are admin-only.

func (s *Server) handleGetRetention(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	policy, err := s.settings.Retention(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, policy)
}

func (s *Server) handleSetRetention(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}

	var body settings.Retention
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.settings.SetRetention(r.Context(), body); err != nil {
		if errors.Is(err, settings.ErrInvalid) {
			writeProblem(w, r, http.StatusBadRequest, CodeValidation,
				"Invalid value", "Retention must be zero or a positive number of days.")
			return
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionSettingsUpdated, "retention", map[string]any{
		"audit_days": body.AuditDays, "trash_days": body.TrashDays,
	})
	writeJSON(w, r, http.StatusOK, body)
}
