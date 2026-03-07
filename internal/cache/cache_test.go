package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- Store unit tests ---

func TestStoreLookup(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
		keys    []string
		version string
		wantKey string // empty means nil expected
	}{
		{
			name: "exact match by key and version",
			entries: []Entry{
				{ID: 1, Key: "go-cache-abc123", Version: "v1", Committed: true, CreatedAt: time.Now()},
			},
			keys:    []string{"go-cache-abc123"},
			version: "v1",
			wantKey: "go-cache-abc123",
		},
		{
			name: "no match returns nil",
			entries: []Entry{
				{ID: 1, Key: "go-cache-abc123", Version: "v1", Committed: true, CreatedAt: time.Now()},
			},
			keys:    []string{"node-cache-xyz"},
			version: "v1",
			wantKey: "",
		},
		{
			name: "version mismatch skips exact match but tries prefix",
			entries: []Entry{
				{ID: 1, Key: "go-cache-abc123", Version: "v1", Committed: true, CreatedAt: time.Now()},
			},
			keys:    []string{"go-cache-abc123"},
			version: "v2",
			wantKey: "go-cache-abc123", // prefix "go-cache-abc123" matches itself
		},
		{
			name: "prefix match with restore keys",
			entries: []Entry{
				{ID: 1, Key: "go-cache-abc123", Version: "v1", Committed: true, CreatedAt: time.Now()},
			},
			keys:    []string{"go-cache-xyz", "go-cache-"},
			version: "v1",
			wantKey: "go-cache-abc123",
		},
		{
			name: "most recent prefix match wins",
			entries: []Entry{
				{ID: 1, Key: "go-cache-old", Version: "v1", Committed: true, CreatedAt: time.Now().Add(-2 * time.Hour)},
				{ID: 2, Key: "go-cache-new", Version: "v1", Committed: true, CreatedAt: time.Now()},
			},
			keys:    []string{"go-cache-"},
			version: "v2", // no exact match
			wantKey: "go-cache-new",
		},
		{
			name: "uncommitted entries are ignored",
			entries: []Entry{
				{ID: 1, Key: "go-cache-abc", Version: "v1", Committed: false, CreatedAt: time.Now()},
			},
			keys:    []string{"go-cache-abc"},
			version: "v1",
			wantKey: "",
		},
		{
			name:    "empty keys returns nil",
			entries: []Entry{},
			keys:    []string{},
			version: "v1",
			wantKey: "",
		},
		{
			name: "exact match takes priority over newer prefix match",
			entries: []Entry{
				{ID: 1, Key: "go-cache-exact", Version: "v1", Committed: true, CreatedAt: time.Now().Add(-time.Hour)},
				{ID: 2, Key: "go-cache-exact-newer", Version: "v1", Committed: true, CreatedAt: time.Now()},
			},
			keys:    []string{"go-cache-exact"},
			version: "v1",
			wantKey: "go-cache-exact", // exact match, not the newer prefix
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := NewStore(dir, 10)
			if err != nil {
				t.Fatal(err)
			}
			s.index = tt.entries

			got := s.Lookup(tt.keys, tt.version)
			if tt.wantKey == "" {
				if got != nil {
					t.Errorf("expected nil, got entry with key %q", got.Key)
				}
			} else {
				if got == nil {
					t.Fatalf("expected entry with key %q, got nil", tt.wantKey)
				}
				if got.Key != tt.wantKey {
					t.Errorf("expected key %q, got %q", tt.wantKey, got.Key)
				}
			}
		})
	}
}

func TestStoreReserveAndCommit(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}

	id, err := s.Reserve("test-key", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("expected id 1, got %d", id)
	}

	// Entry exists but not committed.
	entry := s.Lookup([]string{"test-key"}, "v1")
	if entry != nil {
		t.Error("uncommitted entry should not be found")
	}

	// Commit.
	if err := s.Commit(id, 1024); err != nil {
		t.Fatal(err)
	}

	entry = s.Lookup([]string{"test-key"}, "v1")
	if entry == nil {
		t.Fatal("committed entry should be found")
	}
	if entry.Size != 1024 {
		t.Errorf("expected size 1024, got %d", entry.Size)
	}
}

func TestStoreEviction(t *testing.T) {
	dir := t.TempDir()
	// 1 byte max to force immediate eviction.
	s, err := NewStore(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.maxSize = 100 // 100 bytes for testing

	id1, _ := s.Reserve("key1", "v1")
	_ = s.Commit(id1, 60)

	id2, _ := s.Reserve("key2", "v1")
	_ = s.Commit(id2, 60)

	// Total would be 120 > 100, so oldest (key1) should be evicted.
	if s.Lookup([]string{"key1"}, "v1") != nil {
		t.Error("key1 should have been evicted")
	}
	if s.Lookup([]string{"key2"}, "v1") == nil {
		t.Error("key2 should still exist")
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s1.Reserve("persist-key", "v1")
	_ = s1.Commit(id, 512)

	// Create a new store instance from the same dir.
	s2, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	entry := s2.Lookup([]string{"persist-key"}, "v1")
	if entry == nil {
		t.Fatal("entry should persist across store instances")
	}

	// Next ID should be higher than existing.
	id2, _ := s2.Reserve("another-key", "v1")
	if id2 <= id {
		t.Errorf("new id %d should be greater than previous %d", id2, id)
	}
}

// --- HTTP handler tests ---

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	dir := t.TempDir()
	mux := http.NewServeMux()
	// We need the baseURL before creating the server, use a placeholder then update.
	cs, err := NewServer(dir, "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	cs.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	cs.baseURL = ts.URL
	t.Cleanup(ts.Close)
	return ts, cs
}

func TestHTTPLookupMiss(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/_apis/artifactcache/cache?keys=nonexistent&version=v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestHTTPRoundTrip(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// 1. Reserve.
	reserveBody, _ := json.Marshal(map[string]string{"key": "go-test-abc", "version": "v1"})
	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("reserve: expected 201, got %d", resp.StatusCode)
	}
	var reserveResp struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&reserveResp)
	resp.Body.Close()
	cacheID := reserveResp.CacheID

	// 2. Upload blob.
	blobData := []byte("hello world cache content")
	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, cacheID), bytes.NewReader(blobData))
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/*", len(blobData)-1))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("upload: expected 204, got %d", resp.StatusCode)
	}

	// 3. Commit.
	commitBody, _ := json.Marshal(map[string]int64{"size": int64(len(blobData))})
	resp, err = client.Post(fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, cacheID), "application/json", bytes.NewReader(commitBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("commit: expected 204, got %d", resp.StatusCode)
	}

	// 4. Lookup.
	resp, err = client.Get(ts.URL + "/_apis/artifactcache/cache?keys=go-test-abc&version=v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup: expected 200, got %d", resp.StatusCode)
	}

	var lookupResp struct {
		CacheKey        string `json:"cacheKey"`
		Scope           string `json:"scope"`
		ArchiveLocation string `json:"archiveLocation"`
	}
	json.NewDecoder(resp.Body).Decode(&lookupResp)

	if lookupResp.CacheKey != "go-test-abc" {
		t.Errorf("expected cacheKey go-test-abc, got %q", lookupResp.CacheKey)
	}
	if lookupResp.ArchiveLocation == "" {
		t.Error("archiveLocation should not be empty")
	}

	// 5. Download.
	resp, err = client.Get(lookupResp.ArchiveLocation)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: expected 200, got %d", resp.StatusCode)
	}
	downloaded, _ := io.ReadAll(resp.Body)
	if string(downloaded) != string(blobData) {
		t.Errorf("downloaded content mismatch: got %q, want %q", downloaded, blobData)
	}
}

func TestHTTPLookupPrefixMatch(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Create two entries with the same prefix.
	for i, key := range []string{"go-cache-aaa", "go-cache-bbb"} {
		reserveBody, _ := json.Marshal(map[string]string{"key": key, "version": "v1"})
		resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
		var rr struct {
			CacheID int64 `json:"cacheId"`
		}
		json.NewDecoder(resp.Body).Decode(&rr)
		resp.Body.Close()

		commitBody, _ := json.Marshal(map[string]int64{"size": int64(100 + i)})
		resp, _ = client.Post(fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, rr.CacheID), "application/json", bytes.NewReader(commitBody))
		resp.Body.Close()

		// Small delay to ensure distinct CreatedAt.
		time.Sleep(time.Millisecond)
	}

	// Lookup with prefix — should return most recent (go-cache-bbb).
	resp, err := client.Get(ts.URL + "/_apis/artifactcache/cache?keys=go-cache-&version=v2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var lookupResp struct {
		CacheKey string `json:"cacheKey"`
	}
	json.NewDecoder(resp.Body).Decode(&lookupResp)

	if lookupResp.CacheKey != "go-cache-bbb" {
		t.Errorf("expected most recent key go-cache-bbb, got %q", lookupResp.CacheKey)
	}
}

func TestHTTPChunkedUpload(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Reserve.
	reserveBody, _ := json.Marshal(map[string]string{"key": "chunked-test", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Upload in two chunks.
	chunk1 := []byte("AAAA")
	chunk2 := []byte("BBBB")

	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, rr.CacheID), bytes.NewReader(chunk1))
	req.Header.Set("Content-Range", "bytes 0-3/*")
	resp, _ = client.Do(req)
	resp.Body.Close()

	req, _ = http.NewRequest("PATCH", fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, rr.CacheID), bytes.NewReader(chunk2))
	req.Header.Set("Content-Range", "bytes 4-7/*")
	resp, _ = client.Do(req)
	resp.Body.Close()

	// Commit.
	commitBody, _ := json.Marshal(map[string]int64{"size": 8})
	resp, _ = client.Post(fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, rr.CacheID), "application/json", bytes.NewReader(commitBody))
	resp.Body.Close()

	// Lookup and download.
	resp, _ = client.Get(ts.URL + "/_apis/artifactcache/cache?keys=chunked-test&version=v1")
	var lookupResp struct {
		ArchiveLocation string `json:"archiveLocation"`
	}
	json.NewDecoder(resp.Body).Decode(&lookupResp)
	resp.Body.Close()

	resp, _ = client.Get(lookupResp.ArchiveLocation)
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(data) != "AAAABBBB" {
		t.Errorf("chunked upload result: got %q, want %q", data, "AAAABBBB")
	}
}

// --- Twirp v2 API tests ---

func TestTwirpCreateAndFinalize(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	srv.baseURL = ts.URL
	client := ts.Client()

	// Create entry.
	body, _ := json.Marshal(map[string]string{"key": "twirp-test", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader(body),
	)
	var createResp struct {
		OK              bool   `json:"ok"`
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()

	if !createResp.OK {
		t.Fatal("expected ok=true from CreateCacheEntry")
	}
	if createResp.SignedUploadURL == "" {
		t.Fatal("expected non-empty signedUploadUrl")
	}

	// Upload data to the signed URL.
	data := []byte("twirp cache data")
	req, _ := http.NewRequest("PUT", createResp.SignedUploadURL, bytes.NewReader(data))
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	resp, _ = client.Do(req)
	resp.Body.Close()

	// Finalize.
	body, _ = json.Marshal(map[string]string{
		"key":       "twirp-test",
		"version":   "v1",
		"sizeBytes": fmt.Sprintf("%d", len(data)),
	})
	resp, _ = client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload",
		"application/json",
		bytes.NewReader(body),
	)
	var finalResp struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp.Body).Decode(&finalResp)
	resp.Body.Close()

	if !finalResp.OK {
		t.Fatal("expected ok=true from FinalizeCacheEntry")
	}

	// Lookup via twirp.
	body, _ = json.Marshal(map[string]interface{}{
		"key":         "twirp-test",
		"restoreKeys": []string{},
		"version":     "v1",
	})
	resp, _ = client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL",
		"application/json",
		bytes.NewReader(body),
	)
	var lookupResp struct {
		OK                bool   `json:"ok"`
		MatchedKey        string `json:"matchedKey"`
		SignedDownloadURL string `json:"signedDownloadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&lookupResp)
	resp.Body.Close()

	if !lookupResp.OK {
		t.Fatal("expected ok=true from GetCacheEntryDownloadURL")
	}
	if lookupResp.MatchedKey != "twirp-test" {
		t.Errorf("matched key: got %q, want %q", lookupResp.MatchedKey, "twirp-test")
	}

	// Download.
	resp, _ = client.Get(lookupResp.SignedDownloadURL)
	downloaded, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(downloaded) != string(data) {
		t.Errorf("downloaded: got %q, want %q", downloaded, data)
	}
}

func TestTwirpGetDownloadURL_Miss(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	srv.baseURL = ts.URL
	client := ts.Client()

	body, _ := json.Marshal(map[string]interface{}{
		"key":     "nonexistent",
		"version": "v1",
	})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL",
		"application/json",
		bytes.NewReader(body),
	)
	var lookupResp struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp.Body).Decode(&lookupResp)
	resp.Body.Close()

	if lookupResp.OK {
		t.Fatal("expected ok=false for nonexistent key")
	}
}

func TestFindByKeyVersion(t *testing.T) {
	store, err := NewStore(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	id, _ := store.Reserve("mykey", "v1")
	// Write data to the blob file directly (Store has no Upload method; the HTTP handler writes to BlobPath).
	os.MkdirAll(filepath.Dir(store.BlobPath(id)), 0o755)
	os.WriteFile(store.BlobPath(id), []byte("data"), 0o644)
	store.Commit(id, 4)

	entry := store.FindByKeyVersion("mykey", "v1")
	if entry == nil {
		t.Fatal("expected to find entry")
	}
	if entry.Key != "mykey" {
		t.Errorf("key: got %q, want %q", entry.Key, "mykey")
	}

	// Non-existent key/version.
	entry2 := store.FindByKeyVersion("mykey", "v2")
	if entry2 != nil {
		t.Fatal("expected nil for non-matching version")
	}
}

func TestParseCacheID(t *testing.T) {
	tests := []struct {
		path   string
		prefix string
		want   int64
		err    bool
	}{
		{"/_apis/artifactcache/caches/42", "/_apis/artifactcache/caches/", 42, false},
		{"/_apis/results/caches/7", "/_apis/results/caches/", 7, false},
		{"/_apis/artifactcache/caches/abc", "/_apis/artifactcache/caches/", 0, true},
		{"/_apis/artifactcache/caches/", "/_apis/artifactcache/caches/", 0, true},
	}

	for _, tt := range tests {
		got, err := parseCacheID(tt.path, tt.prefix)
		if tt.err {
			if err == nil {
				t.Errorf("parseCacheID(%q, %q): expected error", tt.path, tt.prefix)
			}
		} else {
			if err != nil {
				t.Errorf("parseCacheID(%q, %q): unexpected error: %v", tt.path, tt.prefix, err)
			}
			if got != tt.want {
				t.Errorf("parseCacheID(%q, %q): got %d, want %d", tt.path, tt.prefix, got, tt.want)
			}
		}
	}
}

// --- Additional coverage tests ---

func TestHTTPLookupMissingKeys(t *testing.T) {
	ts, _ := newTestServer(t)

	// Missing keys parameter
	resp, err := http.Get(ts.URL + "/_apis/artifactcache/cache?version=v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing keys, got %d", resp.StatusCode)
	}
}

func TestHTTPReserveInvalidJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestHTTPUploadInvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	req, _ := http.NewRequest("PATCH", ts.URL+"/_apis/artifactcache/caches/abc", bytes.NewReader([]byte("data")))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid cache ID, got %d", resp.StatusCode)
	}
}

func TestHTTPUploadNoContentRange(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Reserve an entry first
	id, _ := cs.store.Reserve("upload-test", "v1")

	// Upload without Content-Range header (offset defaults to 0)
	data := []byte("hello world")
	req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, id), bytes.NewReader(data))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for upload without Content-Range, got %d", resp.StatusCode)
	}

	// Verify data was written
	blobPath := cs.store.BlobPath(id)
	content, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world" {
		t.Errorf("expected 'hello world', got %q", content)
	}
}

func TestHTTPCommitInvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	commitBody, _ := json.Marshal(map[string]int64{"size": 100})
	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches/abc", "application/json", bytes.NewReader(commitBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid cache ID, got %d", resp.StatusCode)
	}
}

func TestHTTPCommitInvalidJSON(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	id, _ := cs.store.Reserve("commit-test", "v1")
	resp, err := client.Post(fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, id), "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestHTTPCommitNonexistentEntry(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	commitBody, _ := json.Marshal(map[string]int64{"size": 100})
	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches/99999", "application/json", bytes.NewReader(commitBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for nonexistent entry, got %d", resp.StatusCode)
	}
}

func TestHTTPDownloadInvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Get(ts.URL + "/_apis/artifactcache/artifacts/abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid cache ID, got %d", resp.StatusCode)
	}
}

func TestHTTPDownloadNonexistentBlob(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Get(ts.URL + "/_apis/artifactcache/artifacts/99999")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// ServeFile returns 404 for missing file
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent blob, got %d", resp.StatusCode)
	}
}

func TestTwirpCreateEntryInvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader([]byte("not json")),
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestTwirpFinalizeEntryInvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload",
		"application/json",
		bytes.NewReader([]byte("not json")),
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestTwirpFinalizeEntryMissing(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	body, _ := json.Marshal(map[string]string{
		"key":       "nonexistent",
		"version":   "v1",
		"sizeBytes": "100",
	})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload",
		"application/json",
		bytes.NewReader(body),
	)
	defer resp.Body.Close()

	var finalResp struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp.Body).Decode(&finalResp)
	if finalResp.OK {
		t.Error("expected ok=false for nonexistent entry")
	}
}

func TestTwirpGetDownloadURLInvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL",
		"application/json",
		bytes.NewReader([]byte("not json")),
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestTwirpUploadInvalidID(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	req, _ := http.NewRequest("PUT", ts.URL+"/_apis/results/caches/abc", bytes.NewReader([]byte("data")))
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid cache ID, got %d", resp.StatusCode)
	}
}

func TestTwirpUploadSuccessful(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	srv.baseURL = ts.URL
	client := ts.Client()

	// Reserve an entry
	id, _ := srv.store.Reserve("twirp-upload-test", "v1")

	data := []byte("twirp upload data")
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/_apis/results/caches/%d", ts.URL, id), bytes.NewReader(data))
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for successful upload, got %d", resp.StatusCode)
	}

	// Verify data was written
	content, err := os.ReadFile(srv.store.BlobPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "twirp upload data" {
		t.Errorf("expected 'twirp upload data', got %q", content)
	}
}

func TestTwirpUploadWithContentRange(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	srv.baseURL = ts.URL
	client := ts.Client()

	id, _ := srv.store.Reserve("twirp-chunked-test", "v1")

	// Upload first chunk
	chunk1 := []byte("CCCC")
	req1, _ := http.NewRequest("PUT", fmt.Sprintf("%s/_apis/results/caches/%d", ts.URL, id), bytes.NewReader(chunk1))
	req1.Header.Set("Content-Range", "bytes 0-3/*")
	resp1, _ := client.Do(req1)
	resp1.Body.Close()

	// Upload second chunk
	chunk2 := []byte("DDDD")
	req2, _ := http.NewRequest("PUT", fmt.Sprintf("%s/_apis/results/caches/%d", ts.URL, id), bytes.NewReader(chunk2))
	req2.Header.Set("Content-Range", "bytes 4-7/*")
	resp2, _ := client.Do(req2)
	resp2.Body.Close()

	content, err := os.ReadFile(srv.store.BlobPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "CCCCDDDD" {
		t.Errorf("expected 'CCCCDDDD', got %q", content)
	}
}

func TestTwirpDownloadInvalidID(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	resp, _ := client.Get(ts.URL + "/_apis/results/caches/abc")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid cache ID, got %d", resp.StatusCode)
	}
}

func TestTwirpDownloadNonexistentBlob(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	resp, _ := client.Get(ts.URL + "/_apis/results/caches/99999")
	defer resp.Body.Close()
	// ServeFile returns 404 for missing file
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent blob, got %d", resp.StatusCode)
	}
}

func TestTwirpDownloadSuccessful(t *testing.T) {
	mux := http.NewServeMux()
	srv, err := NewServer(t.TempDir(), "http://placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	srv.baseURL = ts.URL
	client := ts.Client()

	// Reserve and write blob
	id, _ := srv.store.Reserve("twirp-dl-test", "v1")
	os.WriteFile(srv.store.BlobPath(id), []byte("twirp blob data"), 0o644)

	resp, _ := client.Get(fmt.Sprintf("%s/_apis/results/caches/%d", ts.URL, id))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "twirp blob data" {
		t.Errorf("expected 'twirp blob data', got %q", data)
	}
}

func TestStoreCommitNonexistent(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Commit(99999, 100)
	if err == nil {
		t.Error("expected error committing nonexistent entry")
	}
}

func TestStoreLoadIndexCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	// Create blobs dir
	os.MkdirAll(filepath.Join(dir, "blobs"), 0o755)
	// Write corrupt index
	os.WriteFile(filepath.Join(dir, "index.json"), []byte("not json"), 0o644)

	// Should not error, just start with empty index
	s, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.index) != 0 {
		t.Errorf("expected empty index, got %d entries", len(s.index))
	}
}

func TestStoreSaveAndLoadIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Add entries
	s.Reserve("key-a", "v1")
	s.Reserve("key-b", "v2")

	// Verify index file exists
	indexPath := filepath.Join(dir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("index.json should not be empty")
	}

	// Load into new store
	s2, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.index) != 2 {
		t.Errorf("expected 2 entries, got %d", len(s2.index))
	}
}

func TestStoreEvictionRemovesBlobFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.maxSize = 50 // 50 bytes

	id1, _ := s.Reserve("evict-key1", "v1")
	// Create a blob file for id1
	os.WriteFile(s.BlobPath(id1), []byte("data1"), 0o644)
	_ = s.Commit(id1, 30)

	id2, _ := s.Reserve("evict-key2", "v1")
	os.WriteFile(s.BlobPath(id2), []byte("data2"), 0o644)
	_ = s.Commit(id2, 30)

	// key1 should be evicted and blob removed
	if _, err := os.Stat(s.BlobPath(id1)); !os.IsNotExist(err) {
		t.Error("blob file for evicted entry should be removed")
	}
	// key2 should still exist
	if _, err := os.Stat(s.BlobPath(id2)); os.IsNotExist(err) {
		t.Error("blob file for remaining entry should exist")
	}
}

func TestStoreEvictionNoEvictionNeeded(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}

	id1, _ := s.Reserve("no-evict-1", "v1")
	_ = s.Commit(id1, 100)

	id2, _ := s.Reserve("no-evict-2", "v1")
	_ = s.Commit(id2, 100)

	// Both should still exist (maxSize is 10GB)
	entry1 := s.Lookup([]string{"no-evict-1"}, "v1")
	entry2 := s.Lookup([]string{"no-evict-2"}, "v1")
	if entry1 == nil || entry2 == nil {
		t.Error("no eviction should have occurred")
	}
}

func TestHTTPLookupNoVersion(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Create a committed entry
	id, _ := cs.store.Reserve("ver-test", "v1")
	_ = cs.store.Commit(id, 10)

	// Lookup without version param (empty version won't exact-match but may prefix-match)
	resp, err := client.Get(ts.URL + "/_apis/artifactcache/cache?keys=ver-test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// With empty version, exact match on key+version fails but prefix match might succeed
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for prefix match, got %d", resp.StatusCode)
	}
}

func TestHTTPLookupEmptyKeys(t *testing.T) {
	ts, _ := newTestServer(t)

	// Empty keys param value (keys=) is treated as keysParam=="" which returns 400
	resp, err := http.Get(ts.URL + "/_apis/artifactcache/cache?keys=&version=v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty keys value, got %d", resp.StatusCode)
	}
}

func TestHTTPReserveAndLookup(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Reserve
	reserveBody, _ := json.Marshal(map[string]string{"key": "reserve-lookup", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("reserve: expected 201, got %d", resp.StatusCode)
	}
	if rr.CacheID == 0 {
		t.Fatal("expected non-zero cache ID")
	}
}

func TestNewServerCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "cache")
	srv, err := NewServer(dir, "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	// Verify the blobs directory was created
	info, err := os.Stat(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("expected blobs directory")
	}
}

func TestLoadIndexSetsNextID(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewStore(dir, 10)

	// Reserve several entries to advance nextID
	s1.Reserve("a", "v1")
	s1.Reserve("b", "v1")
	s1.Reserve("c", "v1")

	// Load from the same dir
	s2, _ := NewStore(dir, 10)

	// Next reservation should get ID > 3
	id, _ := s2.Reserve("d", "v1")
	if id <= 3 {
		t.Errorf("expected next ID > 3, got %d", id)
	}
}

func TestTwirpGetDownloadURLWithRestoreKeys(t *testing.T) {
	mux := http.NewServeMux()
	srv, _ := NewServer(t.TempDir(), "http://placeholder")
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	srv.baseURL = ts.URL
	client := ts.Client()

	// Create and commit an entry
	id, _ := srv.store.Reserve("go-cache-abc123", "v1")
	srv.store.Commit(id, 100)

	// Lookup using restore keys
	body, _ := json.Marshal(map[string]interface{}{
		"key":         "go-cache-xyz",
		"restoreKeys": []string{"go-cache-"},
		"version":     "v2",
	})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL",
		"application/json",
		bytes.NewReader(body),
	)
	var lookupResp struct {
		OK         bool   `json:"ok"`
		MatchedKey string `json:"matchedKey"`
	}
	json.NewDecoder(resp.Body).Decode(&lookupResp)
	resp.Body.Close()

	if !lookupResp.OK {
		t.Fatal("expected ok=true with restore key prefix match")
	}
	if lookupResp.MatchedKey != "go-cache-abc123" {
		t.Errorf("expected matched key 'go-cache-abc123', got %q", lookupResp.MatchedKey)
	}
}

func TestFindByKeyVersionUncommitted(t *testing.T) {
	store, err := NewStore(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}

	store.Reserve("pending-key", "v1")

	// FindByKeyVersion should find uncommitted entries too
	entry := store.FindByKeyVersion("pending-key", "v1")
	if entry == nil {
		t.Fatal("expected to find uncommitted entry")
	}
	if entry.Committed {
		t.Error("entry should not be committed")
	}
}

func TestFindByKeyVersionNonExistent(t *testing.T) {
	store, err := NewStore(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}

	entry := store.FindByKeyVersion("nonexistent", "v1")
	if entry != nil {
		t.Error("expected nil for nonexistent key/version")
	}
}

func TestNewServerError(t *testing.T) {
	// Use a path that cannot be created (file as parent)
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "blockfile")
	os.WriteFile(filePath, []byte("x"), 0o644)

	_, err := NewServer(filepath.Join(filePath, "caches"), "http://localhost")
	if err == nil {
		t.Error("expected error when cache dir cannot be created")
	}
}

func TestNewStoreError(t *testing.T) {
	// MkdirAll fails when a file is in the path
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "blockfile")
	os.WriteFile(filePath, []byte("x"), 0o644)

	_, err := NewStore(filepath.Join(filePath, "caches"), 10)
	if err == nil {
		t.Error("expected error when store dir cannot be created")
	}
}

func TestHTTPUploadNoContentRangeVerifyWrite(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Reserve an entry first
	reserveBody, _ := json.Marshal(map[string]string{"key": "upload-test-verify", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Upload without Content-Range header (offset stays 0)
	body := bytes.NewReader([]byte("hello cache"))
	req, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), body)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp2.StatusCode)
	}

	// Verify blob was written
	data, err := os.ReadFile(cs.store.BlobPath(rr.CacheID))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello cache" {
		t.Errorf("expected 'hello cache', got %q", string(data))
	}
}

func TestHTTPUploadBlobDirRemoved(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Reserve an entry
	reserveBody, _ := json.Marshal(map[string]string{"key": "upload-fail-test", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Remove the blobs directory so OpenFile fails
	os.RemoveAll(filepath.Join(cs.store.dir, "blobs"))

	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), body)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 when blob dir removed, got %d", resp2.StatusCode)
	}
}

func TestHTTPUploadBlobIsDirectory(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Reserve an entry
	reserveBody, _ := json.Marshal(map[string]string{"key": "upload-dir-test", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Create a directory at the blob path so OpenFile fails
	blobPath := cs.store.BlobPath(rr.CacheID)
	os.MkdirAll(blobPath, 0o755)

	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), body)
	resp2, _ := client.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 when blob path is a directory, got %d", resp2.StatusCode)
	}
}

func TestHTTPUploadWithContentRangeOffset(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Reserve
	reserveBody, _ := json.Marshal(map[string]string{"key": "offset-test", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Upload first chunk
	chunk1 := bytes.NewReader([]byte("AAAA"))
	req1, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), chunk1)
	req1.Header.Set("Content-Range", "bytes 0-3/*")
	resp1, _ := client.Do(req1)
	resp1.Body.Close()

	// Upload second chunk at offset
	chunk2 := bytes.NewReader([]byte("BBBB"))
	req2, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), chunk2)
	req2.Header.Set("Content-Range", "bytes 4-7/*")
	resp2, _ := client.Do(req2)
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp2.StatusCode)
	}

	data, _ := os.ReadFile(cs.store.BlobPath(rr.CacheID))
	if string(data) != "AAAABBBB" {
		t.Errorf("expected 'AAAABBBB', got %q", string(data))
	}
}

func TestTwirpUploadBlobDirRemoved(t *testing.T) {
	ts, cs := newTestServer(t)
	client := ts.Client()

	// Create entry via Twirp
	createBody, _ := json.Marshal(map[string]string{"key": "twirp-upload-fail", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader(createBody),
	)
	var cr struct {
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()

	// Remove blobs dir to cause OpenFile failure
	os.RemoveAll(filepath.Join(cs.store.dir, "blobs"))

	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PUT", cr.SignedUploadURL, body)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 when blob dir removed, got %d", resp2.StatusCode)
	}
}

func TestTwirpUploadWithContentRangeOffset(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Create entry via Twirp
	createBody, _ := json.Marshal(map[string]string{"key": "twirp-offset-test", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader(createBody),
	)
	var cr struct {
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()

	// Upload first chunk
	chunk1 := bytes.NewReader([]byte("CCCC"))
	req1, _ := http.NewRequest("PUT", cr.SignedUploadURL, chunk1)
	resp1, _ := client.Do(req1)
	resp1.Body.Close()

	// Upload second chunk at offset (non-zero offset path)
	chunk2 := bytes.NewReader([]byte("DDDD"))
	req2, _ := http.NewRequest("PUT", cr.SignedUploadURL, chunk2)
	req2.Header.Set("Content-Range", "bytes 4-7/*")
	resp2, _ := client.Do(req2)
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestHTTPUploadWriteErrorWithDevFull(t *testing.T) {
	// On Linux, /dev/full causes writes to fail with ENOSPC.
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("skipping: /dev/full not available")
	}

	ts, cs := newTestServer(t)
	client := ts.Client()

	// Reserve an entry
	reserveBody, _ := json.Marshal(map[string]string{"key": "devfull-test", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Replace the blob path with a symlink to /dev/full
	blobPath := cs.store.BlobPath(rr.CacheID)
	os.Remove(blobPath)
	os.Symlink("/dev/full", blobPath)

	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), body)
	req.Header.Set("Content-Range", "bytes 0-3/*")
	resp2, _ := client.Do(req)
	resp2.Body.Close()
	// Should get 500 because write to /dev/full returns ENOSPC
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for /dev/full write error, got %d", resp2.StatusCode)
	}
}

func TestTwirpUploadWriteErrorWithDevFull(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("skipping: /dev/full not available")
	}

	ts, cs := newTestServer(t)
	client := ts.Client()

	// Create entry via Twirp
	createBody, _ := json.Marshal(map[string]string{"key": "twirp-devfull", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader(createBody),
	)
	var cr struct {
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()

	// Parse the cache ID from the upload URL
	parts := strings.Split(cr.SignedUploadURL, "/")
	idStr := parts[len(parts)-1]
	cacheID, _ := strconv.ParseInt(idStr, 10, 64)
	blobPath := cs.store.BlobPath(cacheID)
	os.Remove(blobPath)
	os.Symlink("/dev/full", blobPath)

	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PUT", cr.SignedUploadURL, body)
	resp2, _ := client.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for /dev/full write error, got %d", resp2.StatusCode)
	}
}

func TestHTTPUploadBadContentRange(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Reserve
	reserveBody, _ := json.Marshal(map[string]string{"key": "bad-range", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Upload with badly formed Content-Range (no dash separator)
	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PATCH", ts.URL+fmt.Sprintf("/_apis/artifactcache/caches/%d", rr.CacheID), body)
	req.Header.Set("Content-Range", "bytes nodash")
	resp2, _ := client.Do(req)
	resp2.Body.Close()
	// Should still succeed because offset defaults to 0 when parsing fails
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp2.StatusCode)
	}
}

func TestTwirpUploadBadContentRange(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Create entry
	createBody, _ := json.Marshal(map[string]string{"key": "twirp-bad-range", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader(createBody),
	)
	var cr struct {
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()

	// Upload with badly formed Content-Range
	body := bytes.NewReader([]byte("data"))
	req, _ := http.NewRequest("PUT", cr.SignedUploadURL, body)
	req.Header.Set("Content-Range", "bytes nodash")
	resp2, _ := client.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestHTTPReserveStoreError(t *testing.T) {
	// This tests the Reserve error path - which is hard to trigger since Reserve always succeeds.
	// But we can test a valid reserve + upload + commit flow to improve branch coverage
	// for the reserve success path with proper verification.
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Valid reserve
	reserveBody, _ := json.Marshal(map[string]string{"key": "test-key", "version": "v1"})
	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches", "application/json", bytes.NewReader(reserveBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	if rr.CacheID <= 0 {
		t.Errorf("expected positive cacheId, got %d", rr.CacheID)
	}
}

func TestTwirpCreateEntryAndFullFlow(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Create entry
	createBody, _ := json.Marshal(map[string]string{"key": "twirp-full-flow", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json",
		bytes.NewReader(createBody),
	)
	var cr struct {
		OK              bool   `json:"ok"`
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()

	if !cr.OK {
		t.Fatal("expected ok=true from CreateCacheEntry")
	}

	// Upload data
	data := []byte("twirp cache content")
	req, _ := http.NewRequest("PUT", cr.SignedUploadURL, bytes.NewReader(data))
	resp2, _ := client.Do(req)
	resp2.Body.Close()

	// Finalize
	finalBody, _ := json.Marshal(map[string]string{
		"key":       "twirp-full-flow",
		"version":   "v1",
		"sizeBytes": fmt.Sprintf("%d", len(data)),
	})
	resp3, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload",
		"application/json",
		bytes.NewReader(finalBody),
	)
	var fr struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp3.Body).Decode(&fr)
	resp3.Body.Close()
	if !fr.OK {
		t.Fatal("expected ok=true from Finalize")
	}

	// Download via Twirp GetDownloadURL
	downloadBody, _ := json.Marshal(map[string]interface{}{
		"key":     "twirp-full-flow",
		"version": "v1",
	})
	resp4, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL",
		"application/json",
		bytes.NewReader(downloadBody),
	)
	var dr struct {
		OK                bool   `json:"ok"`
		SignedDownloadURL string `json:"signedDownloadUrl"`
	}
	json.NewDecoder(resp4.Body).Decode(&dr)
	resp4.Body.Close()
	if !dr.OK {
		t.Fatal("expected ok=true from GetDownloadURL")
	}

	// Actually download
	resp5, _ := client.Get(dr.SignedDownloadURL)
	downloaded, _ := io.ReadAll(resp5.Body)
	resp5.Body.Close()
	if string(downloaded) != "twirp cache content" {
		t.Errorf("expected 'twirp cache content', got %q", string(downloaded))
	}
}

// --- Targeted coverage tests ---

func TestHandleReserve_BadJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches",
		"application/json", strings.NewReader("not valid json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleUpload_InvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	req, _ := http.NewRequest("PATCH", ts.URL+"/_apis/artifactcache/caches/notanumber",
		strings.NewReader("data"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleUpload_NoContentRange(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Reserve first.
	reserveBody, _ := json.Marshal(map[string]string{"key": "no-range-test", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches",
		"application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	// Upload with no Content-Range header — should still work (offset=0).
	req, _ := http.NewRequest("PATCH",
		fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, rr.CacheID),
		strings.NewReader("data without range"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestHandleTwirpCreateEntry_BadJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json", strings.NewReader("{invalid"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleTwirpUpload_InvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	req, _ := http.NewRequest("PUT", ts.URL+"/_apis/results/caches/notanumber",
		strings.NewReader("data"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleTwirpUpload_WithContentRange(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Reserve via twirp.
	body, _ := json.Marshal(map[string]string{"key": "twirp-range", "version": "v1"})
	resp, _ := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry",
		"application/json", bytes.NewReader(body))
	var createResp struct {
		SignedUploadURL string `json:"signedUploadUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()

	// Upload with Content-Range to exercise the offset path.
	req, _ := http.NewRequest("PUT", createResp.SignedUploadURL,
		strings.NewReader("chunk1"))
	req.Header.Set("Content-Range", "bytes 0-5/*")
	resp, _ = client.Do(req)
	resp.Body.Close()

	// Second chunk with non-zero offset.
	req2, _ := http.NewRequest("PUT", createResp.SignedUploadURL,
		strings.NewReader("chunk2"))
	req2.Header.Set("Content-Range", "bytes 6-11/*")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestHandleCommit_InvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	commitBody, _ := json.Marshal(map[string]int64{"size": 100})
	resp, err := client.Post(ts.URL+"/_apis/artifactcache/caches/notanumber",
		"application/json", bytes.NewReader(commitBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleCommit_BadJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	// Reserve first.
	reserveBody, _ := json.Marshal(map[string]string{"key": "bad-commit", "version": "v1"})
	resp, _ := client.Post(ts.URL+"/_apis/artifactcache/caches",
		"application/json", bytes.NewReader(reserveBody))
	var rr struct {
		CacheID int64 `json:"cacheId"`
	}
	json.NewDecoder(resp.Body).Decode(&rr)
	resp.Body.Close()

	resp, err := client.Post(fmt.Sprintf("%s/_apis/artifactcache/caches/%d", ts.URL, rr.CacheID),
		"application/json", strings.NewReader("bad json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleDownload_InvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Get(ts.URL + "/_apis/artifactcache/artifacts/notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleTwirpDownload_InvalidCacheID(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Get(ts.URL + "/_apis/results/caches/notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleTwirpFinalizeEntry_BadJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload",
		"application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleTwirpGetDownloadURL_BadJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL",
		"application/json", strings.NewReader("invalid"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleLookup_MissingKeys(t *testing.T) {
	ts, _ := newTestServer(t)
	client := ts.Client()

	resp, err := client.Get(ts.URL + "/_apis/artifactcache/cache?version=v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}
