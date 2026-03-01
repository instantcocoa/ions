package githubstub

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RepoInfo holds repository metadata extracted from the local git state.
type RepoInfo struct {
	Owner         string
	Repo          string
	DefaultBranch string
	CurrentSHA    string
	CurrentRef    string // e.g. "refs/heads/main"
	CloneURL      string
	RepoPath      string // local filesystem path for contents endpoint
}

// Options configures the GitHub API stub server.
type Options struct {
	Token   string            // optional real GitHub token for passthrough
	Verbose bool              // log all requests
	Vars    map[string]string // --var values for actions/variables endpoint
}

// Server implements a local stub of the GitHub REST API.
type Server struct {
	info    RepoInfo
	baseURL string
	opts    Options

	// In-memory stores for write endpoints.
	statuses  []map[string]any
	comments  []map[string]any
	checkRuns []map[string]any

	nextID int
}

// NewServer creates a new GitHub API stub server.
func NewServer(info RepoInfo, baseURL string, opts Options) *Server {
	return &Server{
		info:    info,
		baseURL: strings.TrimRight(baseURL, "/"),
		opts:    opts,
		nextID:  1,
	}
}

// RegisterRoutes registers all GitHub API stub routes on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Wrap all handlers with rate-limit header middleware and logging.
	wrap := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.middleware(handler)
	}

	// Tier 1: High impact endpoints.
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}", wrap(s.handleGetRepo))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/commits/{ref}", wrap(s.handleGetCommit))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/git/ref/{ref...}", wrap(s.handleGetRef))
	mux.HandleFunc("POST /api/v3/repos/{owner}/{repo}/statuses/{sha}", wrap(s.handleCreateStatus))
	mux.HandleFunc("POST /api/v3/repos/{owner}/{repo}/issues/{number}/comments", wrap(s.handleCreateComment))
	mux.HandleFunc("POST /api/v3/repos/{owner}/{repo}/check-runs", wrap(s.handleCreateCheckRun))
	mux.HandleFunc("PATCH /api/v3/repos/{owner}/{repo}/check-runs/{id}", wrap(s.handleUpdateCheckRun))

	// Tier 2: Medium impact endpoints.
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/pulls/{number}", wrap(s.handleGetPull))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/pulls/{number}/files", wrap(s.handleListPullFiles))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/issues/{number}", wrap(s.handleGetIssue))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/issues/{number}/comments", wrap(s.handleListIssueComments))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/contents/{path...}", wrap(s.handleGetContents))
	mux.HandleFunc("POST /api/v3/repos/{owner}/{repo}/dispatches", wrap(s.handleDispatch))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/actions/variables", wrap(s.handleListVariables))
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/actions/variables/{name}", wrap(s.handleGetVariable))

	// User endpoint.
	mux.HandleFunc("GET /api/v3/user", wrap(s.handleGetUser))

	// GraphQL stub.
	mux.HandleFunc("POST /api/v3/graphql", wrap(s.handleGraphQL))
	mux.HandleFunc("POST /api/graphql", wrap(s.handleGraphQL))

	// Catch-all for unhandled /api/v3/ and /api/ routes.
	mux.HandleFunc("/api/v3/", wrap(s.handleCatchAll))
	mux.HandleFunc("/api/graphql", wrap(s.handleGraphQL))
}

// middleware adds rate-limit headers and optional request logging.
func (s *Server) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Add rate limit headers that some actions check.
		resetTime := time.Now().Add(1 * time.Hour).Unix()
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))
		w.Header().Set("X-RateLimit-Resource", "core")

		if s.opts.Verbose {
			log.Printf("[github-stub] %s %s", r.Method, r.URL.Path)
		}

		next(w, r)
	}
}

// handleCatchAll responds to any unhandled API endpoint.
func (s *Server) handleCatchAll(w http.ResponseWriter, r *http.Request) {
	log.Printf("[github-stub] unhandled: %s %s", r.Method, r.URL.Path)
	if s.opts.Verbose {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			log.Printf("[github-stub] request body: %s", string(body))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]any{
		"message":           "Not Found",
		"documentation_url": "https://docs.github.com/rest",
		"ions_note":         fmt.Sprintf("ions does not yet stub %s %s — consider filing an issue", r.Method, r.URL.Path),
	})
}

// handleGetUser returns the current actor as the authenticated user.
func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	actor := s.info.Owner
	if actor == "" {
		actor = "local-actor"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"login":      actor,
		"id":         1,
		"type":       "User",
		"site_admin": false,
	})
}

// handleGraphQL returns a minimal stub for GraphQL requests.
func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	log.Printf("[github-stub] GraphQL request received (stub — returning empty data)")
	if s.opts.Verbose {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			log.Printf("[github-stub] GraphQL query: %s", string(body))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{},
	})
}

// allocID returns a monotonically increasing ID for stub resources.
func (s *Server) allocID() int {
	id := s.nextID
	s.nextID++
	return id
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
