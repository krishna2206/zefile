package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"
)

// audit records an action, attributing it to the request's caller. It is a
// no-op when no audit service is wired, and never fails the request.
func (s *Server) audit(r *http.Request, action, target string, details map[string]any) {
	if s.auditLog == nil {
		return
	}
	var actorID int64
	if c, ok := callerFrom(r.Context()); ok {
		actorID = c.user.ID
	}
	s.auditLog.Record(r.Context(), actorID, clientIP(r), action, target, details)
}

// auditAs records an action attributed to a specific account, for the endpoints
// where the actor is not yet the request's caller — signing in, or accepting an
// invitation, where the session is only being created.
func (s *Server) auditAs(r *http.Request, actorID int64, action, target string, details map[string]any) {
	if s.auditLog == nil {
		return
	}
	s.auditLog.Record(r.Context(), actorID, clientIP(r), action, target, details)
}

type auditEntryResponse struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor,omitempty"`
	ActorID int64           `json:"actor_id,omitempty"`
	IP      string          `json:"ip,omitempty"`
	Action  string          `json:"action"`
	Target  string          `json:"target,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

type auditListResponse struct {
	Entries []auditEntryResponse `json:"entries"`
	// NextBefore is the cursor for the next page, or 0 when the list is exhausted.
	NextBefore int64 `json:"next_before"`
}

// handleAuditList returns the activity log, newest first, keyset-paginated.
// Admin only: it is a record of everyone's actions on the instance.
func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.auditLog == nil {
		writeJSON(w, r, http.StatusOK, auditListResponse{Entries: []auditEntryResponse{}})
		return
	}

	before := int64(math.MaxInt64)
	if raw := r.URL.Query().Get("before"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			before = n
		}
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, 200)
		}
	}

	entries, err := s.auditLog.List(r.Context(), before, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}

	out := make([]auditEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditEntryResponse{
			ID:      e.ID,
			At:      e.At,
			Actor:   e.ActorName,
			ActorID: e.ActorID,
			IP:      e.ActorIP,
			Action:  e.Action,
			Target:  e.Target,
			Details: e.Details,
		})
	}

	// A full page implies there may be more; the cursor is the last id seen.
	next := int64(0)
	if len(entries) == limit {
		next = entries[len(entries)-1].ID
	}
	writeJSON(w, r, http.StatusOK, auditListResponse{Entries: out, NextBefore: next})
}
