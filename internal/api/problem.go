// Package api serves the HTTP interface on the application origin.
//
// The web interface has no privileged endpoint: it speaks the same API as any
// other client. Two surfaces would be two things to secure, and it is always
// the one believed to be internal that receives fewer checks.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/storage"
)

// Problem is an error response in the shape of RFC 9457.
//
// Code carries the machine-readable meaning. Clients must branch on it rather
// than on Title or Detail, which are written for people and will be reworded;
// a mobile app parsing English prose to tell "denied" from "not found" breaks
// the first time someone improves a message.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// Stable error codes. They are part of the API contract: they may be added to,
// never renamed.
const (
	CodeBadRequest         = "bad_request"
	CodeInvalidPath        = "invalid_path"
	CodeUnauthenticated    = "unauthenticated"
	CodeInvalidCredentials = "invalid_credentials"
	CodePermissionDenied   = "permission_denied"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeNotDirectory       = "not_a_directory"
	CodeIsDirectory        = "is_a_directory"
	CodeNotEmpty           = "directory_not_empty"
	CodeTooLarge           = "too_large"
	CodeReadOnly           = "read_only"
	CodeNoSpace            = "no_space"
	CodeRateLimited        = "rate_limited"
	CodeSetupClosed        = "setup_closed"
	CodeInvalidSetupToken  = "invalid_setup_token"
	CodeAmbiguous          = "ambiguous_name"
	CodeInternal           = "internal_error"
)

// problemTypeBase namespaces the type URIs. They are documentation links, not
// endpoints, which is why they point at the repository rather than the API.
const problemTypeBase = "https://github.com/krishna2206/zefile/blob/main/docs/errors.md#"

// writeProblem sends an error response.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	// An error page must never be cached: a 403 kept by a proxy would outlive
	// the permission change that fixed it.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	body := Problem{
		Type:   problemTypeBase + code,
		Title:  title,
		Status: status,
		Code:   code,
		Detail: detail,
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.ErrorContext(r.Context(), "failed to write error response", "error", err)
	}
}

// writeError maps a domain error to a response.
//
// The mapping lives in one place so that no handler can invent its own status
// for a condition — and, more importantly, so that no handler can accidentally
// leak the difference between "denied" and "does not exist". The storage layer
// authorises before it looks, so a caller without permission always gets the
// same answer whether or not the path is there.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		// The client hung up. Nobody is listening, but the status keeps logs
		// and metrics honest about what happened.
		w.WriteHeader(499)

	case errors.Is(err, storage.ErrPermission), errors.Is(err, storage.ErrReserved):
		// Reserved paths answer as denied rather than as their own condition:
		// telling a caller that a path is internal is telling them it exists.
		writeProblem(w, r, http.StatusForbidden, CodePermissionDenied,
			"Permission denied", "You do not have access to this path.")

	case errors.Is(err, storage.ErrNotExist):
		writeProblem(w, r, http.StatusNotFound, CodeNotFound,
			"Not found", "No file or directory exists at this path.")

	case errors.Is(err, storage.ErrExist):
		writeProblem(w, r, http.StatusConflict, CodeConflict,
			"Already exists", "Something already exists at the destination.")

	case errors.Is(err, storage.ErrNotDir):
		writeProblem(w, r, http.StatusBadRequest, CodeNotDirectory,
			"Not a directory", "This path is a file, and the operation needs a directory.")

	case errors.Is(err, storage.ErrIsDir):
		writeProblem(w, r, http.StatusBadRequest, CodeIsDirectory,
			"Is a directory", "This path is a directory, and the operation needs a file.")

	case errors.Is(err, storage.ErrNotEmpty):
		writeProblem(w, r, http.StatusConflict, CodeNotEmpty,
			"Directory not empty", "Delete its contents first, or ask for a recursive delete.")

	case errors.Is(err, storage.ErrAmbiguous):
		writeProblem(w, r, http.StatusConflict, CodeAmbiguous,
			"Ambiguous name", "Two entries here differ only in how their name is encoded; rename one to continue.")

	case errors.Is(err, storage.ErrTooLarge):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, CodeTooLarge,
			"Too large", "This is beyond what can be done inside a request.")

	case errors.Is(err, storage.ErrReadOnly):
		writeProblem(w, r, http.StatusServiceUnavailable, CodeReadOnly,
			"Read-only", "The instance is serving in read-only mode.")

	case errors.Is(err, storage.ErrNoSpace):
		writeProblem(w, r, http.StatusInsufficientStorage, CodeNoSpace,
			"Out of space", "Free space is below the reserve, so writes are refused. Reads and deletions still work.")

	case errors.Is(err, storage.ErrInvalidPath):
		writeProblem(w, r, http.StatusBadRequest, CodeInvalidPath,
			"Invalid path", err.Error())

	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, http.StatusUnauthorized, CodeInvalidCredentials,
			"Invalid credentials", "The username or password is wrong.")

	case errors.Is(err, auth.ErrRateLimited):
		writeProblem(w, r, http.StatusTooManyRequests, CodeRateLimited,
			"Too many attempts", "Too many recent failures. Try again later.")

	case errors.Is(err, auth.ErrInvalidSession):
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated,
			"Not signed in", "Your session is no longer valid.")

	case errors.Is(err, auth.ErrAlreadySetUp):
		writeProblem(w, r, http.StatusConflict, CodeSetupClosed,
			"Already set up", "This instance already has an account.")

	case errors.Is(err, auth.ErrInvalidSetupToken):
		writeProblem(w, r, http.StatusForbidden, CodeInvalidSetupToken,
			"Invalid setup token", "This setup link is unknown, already used, or expired.")

	default:
		// Nothing internal reaches the client. The detail goes to the log,
		// where an operator can find it, and the response says only that
		// something failed.
		slog.ErrorContext(r.Context(), "unhandled error", "error", err, "path", r.URL.Path)
		writeProblem(w, r, http.StatusInternalServerError, CodeInternal,
			"Internal error", "Something went wrong. The details are in the server log.")
	}
}

// writeJSON sends a successful response.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so this cannot become an error
		// response; it can only be recorded.
		slog.ErrorContext(r.Context(), "failed to write response body", "error", err)
	}
}
