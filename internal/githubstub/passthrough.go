package githubstub

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Passthrough proxies requests to the real api.github.com when a token is
// available and the stub doesn't handle the endpoint. By default only GET
// requests are proxied; when ProxyWrites is enabled, POST/PATCH/PUT are
// also forwarded.

const githubAPIBase = "https://api.github.com"

// ProxyToGitHub forwards a request to the real GitHub API.
// Returns true if the request was proxied, false if it should be handled locally.
func (s *Server) ProxyToGitHub(w http.ResponseWriter, r *http.Request) bool {
	if s.opts.Token == "" {
		return false
	}

	// Only proxy GET by default. Proxy writes only when explicitly enabled.
	if r.Method != http.MethodGet && !s.opts.ProxyWrites {
		return false
	}

	// Strip the /api/v3 prefix to get the real GitHub API path.
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/v3")

	targetURL := githubAPIBase + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	log.Printf("[github-stub] proxying to GitHub: %s %s", r.Method, path)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		log.Printf("[github-stub] proxy error creating request: %v", err)
		return false
	}

	req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ions/1.0")
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[github-stub] proxy error: %v", err)
		return false
	}
	defer resp.Body.Close()

	// Copy response headers.
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	return true
}
