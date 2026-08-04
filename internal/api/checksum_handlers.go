package api

import (
	"net/http"

	"github.com/krishna2206/zefile/internal/checksum"
	"github.com/krishna2206/zefile/internal/job"
)

type checksumResponse struct {
	Checksum *checksum.Checksum `json:"checksum,omitempty"`
	Job      *jobResponse       `json:"job,omitempty"`
}

// handleChecksum returns a file's SHA-256. A cached, still-current digest comes
// back at once; otherwise a background job is enqueued and returned for the
// client to poll — after which the same call returns the cached digest. Hashing
// runs in the background so a very large file never blocks the request.
func (s *Server) handleChecksum(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok {
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "Not signed in", "")
		return
	}
	p, ok := pathParam(w, r)
	if !ok {
		return
	}

	cs, cached, err := s.checksums.Cached(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if cached {
		writeJSON(w, r, http.StatusOK, checksumResponse{Checksum: &cs})
		return
	}

	if s.jobs == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, CodeInternal,
			"No worker", "Checksums need the background worker.")
		return
	}
	j, err := s.jobs.Enqueue(r.Context(), job.TypeChecksum, checksum.Payload{
		Path:    p.String(),
		UserID:  c.user.ID,
		IsAdmin: c.user.IsAdmin,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	jr := toJobResponse(j)
	writeJSON(w, r, http.StatusAccepted, checksumResponse{Job: &jr})
}
