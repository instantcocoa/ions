package githubstub

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleGetPull handles GET /api/v3/repos/{owner}/{repo}/pulls/{number}.
func (s *Server) handleGetPull(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	repo := r.PathValue("repo")
	number := r.PathValue("number")
	num, _ := strconv.Atoi(number)

	currentBranch := s.info.DefaultBranch
	if currentBranch == "" {
		currentBranch = "main"
	}
	// If CurrentRef is a branch ref, extract the branch name.
	if strings.HasPrefix(s.info.CurrentRef, "refs/heads/") {
		currentBranch = strings.TrimPrefix(s.info.CurrentRef, "refs/heads/")
	}

	baseBranch := s.info.DefaultBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	sha := s.info.CurrentSHA
	if sha == "" {
		sha = "0000000000000000000000000000000000000000"
	}

	actor := s.info.Owner
	if actor == "" {
		actor = "local-actor"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     s.allocID(),
		"number": num,
		"state":  "open",
		"title":  "Local PR",
		"body":   "",
		"head": map[string]any{
			"ref": currentBranch,
			"sha": sha,
		},
		"base": map[string]any{
			"ref": baseBranch,
		},
		"user": map[string]any{
			"login": actor,
			"id":    1,
			"type":  "User",
		},
		"mergeable":  true,
		"html_url":   "https://github.com/" + owner + "/" + repo + "/pull/" + number,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleListPullFiles handles GET /api/v3/repos/{owner}/{repo}/pulls/{number}/files.
func (s *Server) handleListPullFiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}
