package githubstub

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// handleCreateStatus handles POST /api/v3/repos/{owner}/{repo}/statuses/{sha}.
func (s *Server) handleCreateStatus(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	state, _ := body["state"].(string)
	context, _ := body["context"].(string)
	log.Printf("[github-stub] status created: %s for %s (context=%s)", state, sha[:minLen(len(sha), 8)], context)

	id := s.allocID()
	resp := map[string]any{
		"id":          id,
		"state":       state,
		"context":     context,
		"description": body["description"],
		"target_url":  body["target_url"],
		"created_at":  time.Now().UTC().Format(time.RFC3339),
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
		"creator": map[string]any{
			"login": s.info.Owner,
			"id":    1,
			"type":  "User",
		},
	}
	s.statuses = append(s.statuses, resp)

	writeJSON(w, http.StatusCreated, resp)
}

// handleCreateCheckRun handles POST /api/v3/repos/{owner}/{repo}/check-runs.
func (s *Server) handleCreateCheckRun(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name, _ := body["name"].(string)
	status, _ := body["status"].(string)
	log.Printf("[github-stub] check run created: %s (status=%s)", name, status)

	id := s.allocID()
	resp := map[string]any{
		"id":           id,
		"name":         name,
		"status":       status,
		"conclusion":   body["conclusion"],
		"head_sha":     body["head_sha"],
		"external_id":  body["external_id"],
		"started_at":   time.Now().UTC().Format(time.RFC3339),
		"completed_at": body["completed_at"],
		"output":       body["output"],
	}
	s.checkRuns = append(s.checkRuns, resp)

	writeJSON(w, http.StatusCreated, resp)
}

// handleUpdateCheckRun handles PATCH /api/v3/repos/{owner}/{repo}/check-runs/{id}.
func (s *Server) handleUpdateCheckRun(w http.ResponseWriter, r *http.Request) {
	checkRunID := r.PathValue("id")

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	status, _ := body["status"].(string)
	conclusion, _ := body["conclusion"].(string)
	log.Printf("[github-stub] check run updated: id=%s status=%s conclusion=%s", checkRunID, status, conclusion)

	resp := map[string]any{
		"id":           checkRunID,
		"status":       status,
		"conclusion":   conclusion,
		"completed_at": body["completed_at"],
		"output":       body["output"],
	}

	writeJSON(w, http.StatusOK, resp)
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}
