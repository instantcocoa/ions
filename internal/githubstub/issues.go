package githubstub

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"
)

// handleGetIssue handles GET /api/v3/repos/{owner}/{repo}/issues/{number}.
func (s *Server) handleGetIssue(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	repo := r.PathValue("repo")
	number := r.PathValue("number")
	num, _ := strconv.Atoi(number)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     s.allocID(),
		"number": num,
		"state":  "open",
		"title":  "Local issue stub",
		"body":   "",
		"user": map[string]any{
			"login": s.info.Owner,
			"id":    1,
			"type":  "User",
		},
		"labels":     []any{},
		"assignees":  []any{},
		"comments":   0,
		"html_url":   "https://github.com/" + owner + "/" + repo + "/issues/" + number,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleCreateComment handles POST /api/v3/repos/{owner}/{repo}/issues/{number}/comments.
func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")

	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("[github-stub] comment created on issue #%s: %s", number, truncate(body.Body, 100))

	actor := s.info.Owner
	if actor == "" {
		actor = "local-actor"
	}

	id := s.allocID()
	resp := map[string]any{
		"id":   id,
		"body": body.Body,
		"user": map[string]any{
			"login": actor,
			"id":    1,
			"type":  "User",
		},
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	s.comments = append(s.comments, resp)

	writeJSON(w, http.StatusCreated, resp)
}

// handleListIssueComments handles GET /api/v3/repos/{owner}/{repo}/issues/{number}/comments.
func (s *Server) handleListIssueComments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
