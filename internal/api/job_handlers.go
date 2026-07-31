package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/krishna2206/zefile/internal/job"
)

type jobResponse struct {
	ID       int64   `json:"id"`
	Type     string  `json:"type"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Error    string  `json:"error,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func toJobResponse(j job.Job) jobResponse {
	out := jobResponse{
		ID:        j.ID,
		Type:      j.Type,
		Status:    j.Status,
		Progress:  j.Progress,
		Error:     j.Error,
		CreatedAt: j.CreatedAt.UTC(),
	}
	if !j.FinishedAt.IsZero() {
		finished := j.FinishedAt.UTC()
		out.FinishedAt = &finished
	}
	return out
}

type jobListResponse struct {
	Jobs []jobResponse `json:"jobs"`
}

// handleJobList returns the most recent background jobs, newest first.
func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobs.List(r.Context(), 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]jobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobResponse(j))
	}
	writeJSON(w, r, http.StatusOK, jobListResponse{Jobs: out})
}

// handleJobGet returns one job, the endpoint the interface polls to follow a
// copy's progress.
func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The job id must be a number.")
		return
	}
	j, err := s.jobs.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, job.ErrNotFound) {
			writeProblem(w, r, http.StatusNotFound, CodeNotFound, "No such job", "This job is unknown.")
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toJobResponse(j))
}
