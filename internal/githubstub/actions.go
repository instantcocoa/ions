package githubstub

import (
	"log"
	"net/http"
	"time"
)

// handleListVariables handles GET /api/v3/repos/{owner}/{repo}/actions/variables.
// Returns the --var values passed to ions.
func (s *Server) handleListVariables(w http.ResponseWriter, r *http.Request) {
	var variables []map[string]any
	for name, value := range s.opts.Vars {
		variables = append(variables, map[string]any{
			"name":       name,
			"value":      value,
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
	if variables == nil {
		variables = []map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_count": len(variables),
		"variables":   variables,
	})
}

// handleGetVariable handles GET /api/v3/repos/{owner}/{repo}/actions/variables/{name}.
func (s *Server) handleGetVariable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	value, ok := s.opts.Vars[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"message": "Not Found",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":       name,
		"value":      value,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleDispatch handles POST /api/v3/repos/{owner}/{repo}/dispatches.
func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	log.Printf("[github-stub] repository dispatch received")
	w.WriteHeader(http.StatusNoContent)
}
