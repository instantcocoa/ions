package broker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// actionExprPattern matches ${{ ... }} expression tokens in YAML content.
// Uses non-greedy matching to find the shortest match between ${{ and }}.
var actionExprPattern = regexp.MustCompile(`\$\{\{.*?\}\}`)

// handleActionTarball proxies action tarball downloads from GitHub and patches
// action.yml/action.yaml to resolve ${{ }} expression tokens in default input
// values. The runner's legacy ActionManifestManager cannot parse BasicExpressionToken
// in default values — it expects plain StringToken. By resolving expressions to
// literal values before the runner sees them, defaults become plain strings.
func (s *Server) handleActionTarball(w http.ResponseWriter, r *http.Request) {
	// Path: /_actions/tarball/{owner}/{repo}/{ref}
	path := strings.TrimPrefix(r.URL.Path, "/_actions/tarball/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	owner, repo, ref := parts[0], parts[1], parts[2]

	apiBase := s.actionAPIBase
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	upstreamURL := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", apiBase, owner, repo, ref)
	if s.verbose {
		log.Printf("[broker] proxying action tarball: %s/%s@%s", owner, repo, ref)
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		log.Printf("[broker] tarball request error: %v", err)
		http.Error(w, "upstream request failed", http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "ions/1.0")
	if s.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.githubToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[broker] tarball fetch error: %v", err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[broker] tarball upstream status: %d for %s", resp.StatusCode, upstreamURL)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	if err := patchActionTarball(resp.Body, w, s.exprDefaults); err != nil {
		log.Printf("[broker] tarball patch error: %v", err)
	}
}

// patchActionTarball reads a gzipped tarball from src, patches any top-level
// action.yml or action.yaml files to resolve expression tokens, and writes the
// modified tarball to dst.
func patchActionTarball(src io.Reader, dst io.Writer, defaults map[string]string) error {
	gzr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	// Buffer the output since tar headers need correct sizes.
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Read entry content.
		var data []byte
		if header.Size > 0 {
			data, err = io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("read entry %s: %w", header.Name, err)
			}
		}

		// Patch top-level action.yml or action.yaml.
		// GitHub tarballs have structure: owner-repo-sha/action.yml
		if shouldPatchActionYAML(header.Name) {
			data = patchActionYAML(data, defaults)
			header.Size = int64(len(data))
		}

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write header %s: %w", header.Name, err)
		}
		if len(data) > 0 {
			if _, err := tw.Write(data); err != nil {
				return fmt.Errorf("write data %s: %w", header.Name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}

	_, err = io.Copy(dst, &buf)
	return err
}

// shouldPatchActionYAML returns true if the tar entry is a top-level action.yml
// or action.yaml (depth 1 in the tarball, i.e., directly inside the root directory).
func shouldPatchActionYAML(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 2 {
		return false
	}
	return parts[1] == "action.yml" || parts[1] == "action.yaml"
}

// patchActionYAML resolves ${{ }} expression tokens in action.yml content by
// replacing them with literal values from the defaults map. Expressions that
// match a key (e.g. "github.token") are replaced with the corresponding value.
// Unknown expressions are replaced with an empty string.
func patchActionYAML(data []byte, defaults map[string]string) []byte {
	return actionExprPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		// Extract expression: "${{ github.token }}" → "github.token"
		inner := strings.TrimSpace(string(match[3 : len(match)-2]))
		if val, ok := defaults[inner]; ok {
			return []byte(val)
		}
		return nil // strip unknown expressions to empty
	})
}
