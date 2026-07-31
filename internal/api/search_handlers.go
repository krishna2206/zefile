package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/krishna2206/zefile/internal/storage"
)

// Search walks the live tree rather than consulting the FTS index.
//
// The file_index table exists but nothing populates it yet — that waits for the
// background job queue. Until then a recursive walk over the storage layer is
// correct by construction: it can never drift from what is actually on disk, and
// it reuses the same per-entry authorisation every listing goes through. It is
// bounded so a pathological tree cannot turn one request into an unbounded scan.
const (
	searchDefaultLimit = 200
	searchMaxLimit     = 500

	// searchMaxDirs caps how many directories one search descends into. A tree
	// larger than this returns partial results marked truncated rather than
	// tying up a request walking forever.
	searchMaxDirs = 20000
)

type searchResponse struct {
	Query   string          `json:"query"`
	Results []entryResponse `json:"results"`
	// Truncated reports that the walk hit a limit and more matches may exist.
	Truncated bool `json:"truncated"`
}

// handleSearch finds entries whose name contains the query, under a root folder
// (the whole tree by default), matching case- and position-insensitively.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		// An empty query is not an error — it simply matches nothing, so the
		// interface can clear results without special-casing the call.
		writeJSON(w, r, http.StatusOK, searchResponse{Results: []entryResponse{}})
		return
	}

	root, ok := pathParam(w, r) // reuses ?path=, defaulting to the tree root
	if !ok {
		return
	}

	limit := searchDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, searchMaxLimit)
		}
	}

	needle := strings.ToLower(query)
	results := make([]entryResponse, 0, limit)
	truncated := false

	// Breadth-first so shallower matches — the ones nearer where the search
	// started — are found first, before the limit is reached.
	queue := []storage.Path{root}
	dirs := 0
	for len(queue) > 0 && len(results) < limit {
		if dirs >= searchMaxDirs {
			truncated = true
			break
		}
		dir := queue[0]
		queue = queue[1:]
		dirs++

		entries, err := s.fs.List(r.Context(), dir)
		if err != nil {
			// A directory that vanished or turned unreadable mid-walk is skipped,
			// not fatal: search is best-effort over a live tree.
			continue
		}
		for _, entry := range entries {
			if entry.IsDir {
				queue = append(queue, entry.Path)
			}
			if strings.Contains(strings.ToLower(entry.Name), needle) {
				results = append(results, toEntryResponse(entry))
				if len(results) >= limit {
					truncated = true
					break
				}
			}
		}
	}

	// Directories lead, then by name — the same order a listing uses, so results
	// read the way the rest of the interface does.
	sort.Slice(results, func(i, j int) bool {
		if results[i].IsDir != results[j].IsDir {
			return results[i].IsDir
		}
		return results[i].Name < results[j].Name
	})

	writeJSON(w, r, http.StatusOK, searchResponse{Query: query, Results: results, Truncated: truncated})
}
