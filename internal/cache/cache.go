package cache

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Server serves the GitHub Actions cache API.
type Server struct {
	store   *Store
	baseURL string
}

// NewServer creates a cache server backed by the given directory.
func NewServer(cacheDir, baseURL string) (*Server, error) {
	store, err := NewStore(cacheDir, 10)
	if err != nil {
		return nil, err
	}
	return &Server{store: store, baseURL: baseURL}, nil
}

// RegisterRoutes adds cache API routes to the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Legacy v1 API (used by @actions/cache v3 and earlier)
	mux.HandleFunc("GET /_apis/artifactcache/cache", s.handleLookup)
	mux.HandleFunc("POST /_apis/artifactcache/caches", s.handleReserve)
	mux.HandleFunc("PATCH /_apis/artifactcache/caches/", s.handleUpload)
	mux.HandleFunc("POST /_apis/artifactcache/caches/", s.handleCommit)
	mux.HandleFunc("GET /_apis/artifactcache/artifacts/", s.handleDownload)

	// Twirp v2 API (used by @actions/cache v4+)
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry", s.handleTwirpCreateEntry)
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload", s.handleTwirpFinalizeEntry)
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL", s.handleTwirpGetDownloadURL)
	mux.HandleFunc("PUT /_apis/results/caches/", s.handleTwirpUpload)
	mux.HandleFunc("GET /_apis/results/caches/", s.handleTwirpDownload)
}

// handleLookup handles GET /_apis/artifactcache/cache?keys=...&version=...
func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	keysParam := r.URL.Query().Get("keys")
	version := r.URL.Query().Get("version")

	if keysParam == "" {
		http.Error(w, "missing keys parameter", http.StatusBadRequest)
		return
	}

	keys := strings.Split(keysParam, ",")

	entry := s.store.Lookup(keys, version)
	if entry == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"cacheKey":        entry.Key,
		"scope":           "refs/heads/main",
		"archiveLocation": fmt.Sprintf("%s/_apis/artifactcache/artifacts/%d", s.baseURL, entry.ID),
	})
}

// handleReserve handles POST /_apis/artifactcache/caches
func (s *Server) handleReserve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	id, err := s.store.Reserve(req.Key, req.Version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"cacheId": id,
	})
}

// handleUpload handles PATCH /_apis/artifactcache/caches/{cacheId}
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	id, err := parseCacheID(r.URL.Path, "/_apis/artifactcache/caches/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse Content-Range: bytes {start}-{end}/*
	var offset int64
	if cr := r.Header.Get("Content-Range"); cr != "" {
		cr = strings.TrimPrefix(cr, "bytes ")
		parts := strings.SplitN(cr, "-", 2)
		if len(parts) == 2 {
			startStr := parts[0]
			offset, _ = strconv.ParseInt(startStr, 10, 64)
		}
	}

	blobPath := s.store.BlobPath(id)
	f, err := os.OpenFile(blobPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[cache] open blob %d: %v", id, err)
		http.Error(w, "failed to open blob", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		log.Printf("[cache] seek blob %d to %d: %v", id, offset, err)
		http.Error(w, "seek failed", http.StatusInternalServerError)
		return
	}

	if _, err := io.Copy(f, r.Body); err != nil {
		log.Printf("[cache] write blob %d: %v", id, err)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleCommit handles POST /_apis/artifactcache/caches/{cacheId}
func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	id, err := parseCacheID(r.URL.Path, "/_apis/artifactcache/caches/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Size int64 `json:"size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.store.Commit(id, req.Size); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDownload handles GET /_apis/artifactcache/artifacts/{cacheId}
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id, err := parseCacheID(r.URL.Path, "/_apis/artifactcache/artifacts/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	blobPath := s.store.BlobPath(id)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, blobPath)
}

// parseCacheID extracts a numeric ID from the end of a URL path after the given prefix.
func parseCacheID(path, prefix string) (int64, error) {
	rest := strings.TrimPrefix(path, prefix)
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cache id: %q", rest)
	}
	return id, nil
}

// --- Twirp v2 API handlers (used by actions/cache@v4) ---

// handleTwirpCreateEntry handles POST /twirp/.../CreateCacheEntry
func (s *Server) handleTwirpCreateEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	id, err := s.store.Reserve(req.Key, req.Version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	uploadURL := fmt.Sprintf("%s/_apis/results/caches/%d", s.baseURL, id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":              true,
		"signedUploadUrl": uploadURL,
	})
}

// handleTwirpFinalizeEntry handles POST /twirp/.../FinalizeCacheEntryUpload
func (s *Server) handleTwirpFinalizeEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key       string `json:"key"`
		Version   string `json:"version"`
		SizeBytes string `json:"sizeBytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	size, _ := strconv.ParseInt(req.SizeBytes, 10, 64)

	// Find the entry by key+version (may be uncommitted) and commit it.
	entry := s.store.FindByKeyVersion(req.Key, req.Version)
	if entry != nil {
		s.store.Commit(entry.ID, size)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"entryId": fmt.Sprintf("%d", entry.ID),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": false,
		})
	}
}

// handleTwirpGetDownloadURL handles POST /twirp/.../GetCacheEntryDownloadURL
func (s *Server) handleTwirpGetDownloadURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key         string   `json:"key"`
		RestoreKeys []string `json:"restoreKeys"`
		Version     string   `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	keys := append([]string{req.Key}, req.RestoreKeys...)
	entry := s.store.Lookup(keys, req.Version)
	if entry == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": false,
		})
		return
	}

	downloadURL := fmt.Sprintf("%s/_apis/results/caches/%d", s.baseURL, entry.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":                true,
		"matchedKey":        entry.Key,
		"signedDownloadUrl": downloadURL,
	})
}

// handleTwirpUpload handles PUT /_apis/results/caches/{id}
func (s *Server) handleTwirpUpload(w http.ResponseWriter, r *http.Request) {
	id, err := parseCacheID(r.URL.Path, "/_apis/results/caches/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	blobPath := s.store.BlobPath(id)
	f, err := os.OpenFile(blobPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[cache] open blob %d: %v", id, err)
		http.Error(w, "failed to open blob", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Handle Content-Range for chunked uploads.
	var offset int64
	if cr := r.Header.Get("Content-Range"); cr != "" {
		cr = strings.TrimPrefix(cr, "bytes ")
		parts := strings.SplitN(cr, "-", 2)
		if len(parts) == 2 {
			offset, _ = strconv.ParseInt(parts[0], 10, 64)
		}
	}

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			http.Error(w, "seek failed", http.StatusInternalServerError)
			return
		}
	}

	if _, err := io.Copy(f, r.Body); err != nil {
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleTwirpDownload handles GET /_apis/results/caches/{id}
func (s *Server) handleTwirpDownload(w http.ResponseWriter, r *http.Request) {
	id, err := parseCacheID(r.URL.Path, "/_apis/results/caches/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	blobPath := s.store.BlobPath(id)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, blobPath)
}
