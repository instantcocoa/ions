package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
