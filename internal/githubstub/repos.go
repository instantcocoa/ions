package githubstub

import (
	"net/http"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// handleGetRepo handles GET /api/v3/repos/{owner}/{repo}.
func (s *Server) handleGetRepo(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	repo := r.PathValue("repo")

	defaultBranch := s.info.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        1,
		"name":      repo,
		"full_name": owner + "/" + repo,
		"private":   false,
		"owner": map[string]any{
			"login": owner,
			"id":    1,
			"type":  "User",
		},
		"html_url":       "https://github.com/" + owner + "/" + repo,
		"default_branch": defaultBranch,
		"visibility":     "public",
		"permissions": map[string]any{
			"admin": true,
			"push":  true,
			"pull":  true,
		},
		"clone_url": s.info.CloneURL,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleGetCommit handles GET /api/v3/repos/{owner}/{repo}/commits/{ref}.
func (s *Server) handleGetCommit(w http.ResponseWriter, r *http.Request) {
	refStr := r.PathValue("ref")

	// Try to resolve from the local git repo.
	if s.info.RepoPath != "" {
		repo, err := git.PlainOpenWithOptions(s.info.RepoPath, &git.PlainOpenOptions{
			DetectDotGit: true,
		})
		if err == nil {
			sha := resolveRef(repo, refStr)
			if sha != "" {
				commit, err := repo.CommitObject(plumbing.NewHash(sha))
				if err == nil {
					writeJSON(w, http.StatusOK, map[string]any{
						"sha": sha,
						"commit": map[string]any{
							"message": commit.Message,
							"author": map[string]any{
								"name":  commit.Author.Name,
								"email": commit.Author.Email,
								"date":  commit.Author.When.UTC().Format(time.RFC3339),
							},
							"committer": map[string]any{
								"name":  commit.Committer.Name,
								"email": commit.Committer.Email,
								"date":  commit.Committer.When.UTC().Format(time.RFC3339),
							},
						},
						"author": map[string]any{
							"login": commit.Author.Name,
						},
					})
					return
				}
			}
		}
	}

	// Fallback: return stub commit using known SHA.
	sha := s.info.CurrentSHA
	if sha == "" {
		sha = "0000000000000000000000000000000000000000"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sha": sha,
		"commit": map[string]any{
			"message": "local commit",
			"author": map[string]any{
				"name":  s.info.Owner,
				"email": s.info.Owner + "@local",
				"date":  time.Now().UTC().Format(time.RFC3339),
			},
		},
		"author": map[string]any{
			"login": s.info.Owner,
		},
	})
}

// handleGetRef handles GET /api/v3/repos/{owner}/{repo}/git/ref/{ref...}.
func (s *Server) handleGetRef(w http.ResponseWriter, r *http.Request) {
	refPath := r.PathValue("ref")

	// Try to resolve from the local git repo.
	if s.info.RepoPath != "" {
		repo, err := git.PlainOpenWithOptions(s.info.RepoPath, &git.PlainOpenOptions{
			DetectDotGit: true,
		})
		if err == nil {
			fullRef := "refs/" + refPath
			ref, err := repo.Reference(plumbing.ReferenceName(fullRef), true)
			if err == nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"ref": fullRef,
					"object": map[string]any{
						"sha":  ref.Hash().String(),
						"type": "commit",
					},
				})
				return
			}
		}
	}

	// Fallback to current ref info.
	sha := s.info.CurrentSHA
	if sha == "" {
		sha = "0000000000000000000000000000000000000000"
	}
	ref := s.info.CurrentRef
	if ref == "" {
		ref = "refs/" + refPath
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ref": ref,
		"object": map[string]any{
			"sha":  sha,
			"type": "commit",
		},
	})
}

// resolveRef tries to resolve a ref string to a SHA.
// Handles branch names, tag names, and full/short SHAs.
func resolveRef(repo *git.Repository, refStr string) string {
	// Try as branch.
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(refStr), true)
	if err == nil {
		return ref.Hash().String()
	}

	// Try as tag.
	ref, err = repo.Reference(plumbing.NewTagReferenceName(refStr), true)
	if err == nil {
		return ref.Hash().String()
	}

	// Try as full ref.
	ref, err = repo.Reference(plumbing.ReferenceName(refStr), true)
	if err == nil {
		return ref.Hash().String()
	}

	// Try HEAD.
	if refStr == "HEAD" {
		ref, err = repo.Head()
		if err == nil {
			return ref.Hash().String()
		}
	}

	// Try as a raw SHA (full or partial).
	if len(refStr) >= 7 {
		hash := plumbing.NewHash(refStr)
		if _, err := repo.CommitObject(hash); err == nil {
			return hash.String()
		}
	}

	return ""
}
