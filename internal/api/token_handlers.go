package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/krishna2206/zefile/internal/audit"
	"github.com/krishna2206/zefile/internal/auth"
)

// API tokens are long-lived bearer credentials a user creates for programmatic
// access — a backup script, a CI job, an integration. A token acts with the
// full authority of the account that owns it, so each user manages their own;
// there is no admin-over-others surface here.

const maxTokenNameLen = 100

type apiTokenResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type apiTokenListResponse struct {
	Tokens []apiTokenResponse `json:"tokens"`
}

// createTokenRequest asks for a name and, optionally, a lifetime in days. A
// missing or zero ExpiresInDays means the token never expires — the common case
// for an unattended script, where revocation is the intended off switch.
type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// createTokenResponse returns the plaintext exactly once. It is never stored and
// cannot be shown again, so the interface makes the user copy it now.
type createTokenResponse struct {
	Token string           `json:"token"`
	Info  apiTokenResponse `json:"info"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	tokens, err := s.auth.ListAPITokens(r.Context(), c.user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	out := make([]apiTokenResponse, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toTokenResponse(t))
	}
	writeJSON(w, r, http.StatusOK, apiTokenListResponse{Tokens: out})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	var body createTokenRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeFieldProblem(w, r, map[string]string{"name": "Give the token a name so you can recognise it."})
		return
	}
	if len(name) > maxTokenNameLen {
		writeFieldProblem(w, r, map[string]string{"name": "That name is too long."})
		return
	}
	if body.ExpiresInDays < 0 {
		writeFieldProblem(w, r, map[string]string{"expires_in_days": "A lifetime cannot be negative."})
		return
	}

	var expiresAt *time.Time
	if body.ExpiresInDays > 0 {
		exp := time.Now().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &exp
	}

	plaintext, token, err := s.auth.CreateAPIToken(r.Context(), c.user.ID, name, expiresAt)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionTokenCreated, token.Prefix, map[string]any{"name": name})

	writeJSON(w, r, http.StatusCreated, createTokenResponse{
		Token: plaintext,
		Info:  toTokenResponse(token),
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "No such token", "")
		return
	}

	if err := s.auth.RevokeAPIToken(r.Context(), c.user.ID, id); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, audit.ActionTokenRevoked, strconv.FormatInt(id, 10), nil)

	writeJSON(w, r, http.StatusNoContent, nil)
}

func toTokenResponse(t auth.APIToken) apiTokenResponse {
	return apiTokenResponse{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		CreatedAt:  t.CreatedAt,
		LastUsedAt: t.LastUsedAt,
		ExpiresAt:  t.ExpiresAt,
	}
}
