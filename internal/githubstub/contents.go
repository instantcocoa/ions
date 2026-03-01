package githubstub

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleGetContents handles GET /api/v3/repos/{owner}/{repo}/contents/{path...}.
// Reads file content from the local working directory.
func (s *Server) handleGetContents(w http.ResponseWriter, r *http.Request) {
	filePath := r.PathValue("path")

	if s.info.RepoPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"message": "Not Found",
		})
		return
	}

	fullPath := filepath.Join(s.info.RepoPath, filepath.FromSlash(filePath))

	// Prevent path traversal.
	cleanPath := filepath.Clean(fullPath)
	repoClean := filepath.Clean(s.info.RepoPath)
	if cleanPath != repoClean && !isSubpath(cleanPath, repoClean) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"message": "Not Found",
		})
		return
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"message": "Not Found",
		})
		return
	}

	if info.IsDir() {
		s.handleDirContents(w, cleanPath, filePath)
		return
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"message": "could not read file",
		})
		return
	}

	// Compute a fake blob SHA (matches git's blob hashing).
	blobSHA := gitBlobSHA(data)

	writeJSON(w, http.StatusOK, map[string]any{
		"name":     filepath.Base(filePath),
		"path":     filePath,
		"sha":      blobSHA,
		"size":     len(data),
		"type":     "file",
		"content":  base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	})
}

// handleDirContents returns a directory listing.
func (s *Server) handleDirContents(w http.ResponseWriter, dirPath, relPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"message": "could not read directory",
		})
		return
	}

	var items []map[string]any
	for _, entry := range entries {
		entryType := "file"
		if entry.IsDir() {
			entryType = "dir"
		}
		entryPath := relPath
		if entryPath != "" {
			entryPath += "/"
		}
		entryPath += entry.Name()

		items = append(items, map[string]any{
			"name": entry.Name(),
			"path": entryPath,
			"type": entryType,
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// gitBlobSHA computes the SHA1 hash matching git's blob object format.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// isSubpath checks if child is under parent directory.
func isSubpath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}
