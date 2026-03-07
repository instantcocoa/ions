package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Pure utility function tests ---

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantStart int64
		wantEnd   int64
		wantErr   bool
	}{
		{
			name:      "standard range with total",
			header:    "bytes 0-1023/4096",
			wantStart: 0,
			wantEnd:   1023,
		},
		{
			name:      "range with unknown total",
			header:    "bytes 0-1023/*",
			wantStart: 0,
			wantEnd:   1023,
		},
		{
			name:      "mid-file range",
			header:    "bytes 1024-2047/4096",
			wantStart: 1024,
			wantEnd:   2047,
		},
		{
			name:      "single byte range",
			header:    "bytes 0-0/1",
			wantStart: 0,
			wantEnd:   0,
		},
		{
			name:      "large range",
			header:    "bytes 0-1048575/10485760",
			wantStart: 0,
			wantEnd:   1048575,
		},
		{
			name:    "missing dash - no range separator",
			header:  "bytes 01023/4096",
			wantErr: true,
		},
		{
			name:    "invalid start - not a number",
			header:  "bytes abc-1023/4096",
			wantErr: true,
		},
		{
			name:    "invalid end - not a number",
			header:  "bytes 0-xyz/4096",
			wantErr: true,
		},
		{
			name:    "empty string",
			header:  "",
			wantErr: true,
		},
		{
			name:    "just bytes prefix",
			header:  "bytes ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := parseContentRange(tt.header)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got start=%d end=%d", start, end)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if start != tt.wantStart {
				t.Errorf("start: got %d, want %d", start, tt.wantStart)
			}
			if end != tt.wantEnd {
				t.Errorf("end: got %d, want %d", end, tt.wantEnd)
			}
		})
	}
}

func TestExtractPathSegment(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		prefix string
		want   string
	}{
		{
			name:   "simple id",
			path:   "/_apis/results/upload/123",
			prefix: "/_apis/results/upload/",
			want:   "123",
		},
		{
			name:   "id with query params stripped",
			path:   "/_apis/results/upload/456?sig=unused",
			prefix: "/_apis/results/upload/",
			want:   "456",
		},
		{
			name:   "id with trailing slash",
			path:   "/_apis/results/download/789/extra",
			prefix: "/_apis/results/download/",
			want:   "789",
		},
		{
			name:   "empty after prefix",
			path:   "/_apis/results/upload/",
			prefix: "/_apis/results/upload/",
			want:   "",
		},
		{
			name:   "prefix not matching - returns full path trimmed",
			path:   "/other/path/123",
			prefix: "/_apis/results/upload/",
			want:   "",
		},
		{
			name:   "id with both query and trailing path",
			path:   "/_apis/results/upload/abc/def?key=val",
			prefix: "/_apis/results/upload/",
			want:   "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathSegment(tt.path, tt.prefix)
			if got != tt.want {
				t.Errorf("extractPathSegment(%q, %q) = %q, want %q", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestParseBlockListXML(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "Latest tags",
			body: `<BlockList><Latest>block1</Latest><Latest>block2</Latest></BlockList>`,
			want: []string{"block1", "block2"},
		},
		{
			name: "Committed tags",
			body: `<BlockList><Committed>c1</Committed><Committed>c2</Committed></BlockList>`,
			want: []string{"c1", "c2"},
		},
		{
			name: "Uncommitted tags",
			body: `<BlockList><Uncommitted>u1</Uncommitted><Uncommitted>u2</Uncommitted></BlockList>`,
			want: []string{"u1", "u2"},
		},
		{
			name: "mixed tags",
			body: `<BlockList><Latest>l1</Latest><Committed>c1</Committed><Uncommitted>u1</Uncommitted></BlockList>`,
			want: []string{"l1", "c1", "u1"},
		},
		{
			name: "whitespace in block ids",
			body: `<BlockList><Latest>  block1  </Latest></BlockList>`,
			want: []string{"block1"},
		},
		{
			name: "empty block list",
			body: `<BlockList></BlockList>`,
			want: nil,
		},
		{
			name: "malformed XML - no closing Latest",
			body: `<BlockList><Latest>block1`,
			want: nil,
		},
		{
			name: "malformed XML - no closing Committed",
			body: `<BlockList><Committed>block1`,
			want: nil,
		},
		{
			name: "malformed XML - no closing Uncommitted",
			body: `<BlockList><Uncommitted>block1`,
			want: nil,
		},
		{
			name: "empty string",
			body: "",
			want: nil,
		},
		{
			name: "single Latest block",
			body: `<BlockList><Latest>only-one</Latest></BlockList>`,
			want: []string{"only-one"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBlockListXML(tt.body)
			if len(got) != len(tt.want) {
				t.Fatalf("parseBlockListXML: got %d blocks %v, want %d blocks %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMustAtoi(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"42", 42},
		{"0", 0},
		{"-1", -1},
		{"not-a-number", 0},
		{"", 0},
		{"999999", 999999},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mustAtoi(tt.input)
			if got != tt.want {
				t.Errorf("mustAtoi(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- Store method tests ---

func TestStoreFindByName(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Create and finalize an artifact.
	a, err := store.Create("find-me", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	store.Finalize(a.ID, 100)

	// Create another, non-finalized.
	store.Create("not-finalized", "run-1")

	// FindByName should find the finalized one.
	found := store.FindByName("run-1", "find-me")
	if found == nil {
		t.Fatal("expected to find artifact")
	}
	if found.ID != a.ID {
		t.Fatalf("expected ID %s, got %s", a.ID, found.ID)
	}

	// FindByName should not find non-finalized.
	notFound := store.FindByName("run-1", "not-finalized")
	if notFound != nil {
		t.Fatal("expected nil for non-finalized artifact")
	}

	// FindByName with wrong run ID.
	notFound = store.FindByName("run-999", "find-me")
	if notFound != nil {
		t.Fatal("expected nil for wrong run ID")
	}

	// FindByName with wrong name.
	notFound = store.FindByName("run-1", "nonexistent")
	if notFound != nil {
		t.Fatal("expected nil for wrong name")
	}
}

func TestStoreBlobPath(t *testing.T) {
	store := NewStore("/tmp/test-artifacts")
	got := store.BlobPath("42")
	want := filepath.Join("/tmp/test-artifacts", "42", "blob")
	if got != want {
		t.Errorf("BlobPath = %q, want %q", got, want)
	}
}

func TestStoreBlockPath(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// BlockPath should create the blocks directory and return the right path.
	got := store.BlockPath("1", "blockA")
	want := filepath.Join(dir, "1", "blocks", "blockA")
	if got != want {
		t.Errorf("BlockPath = %q, want %q", got, want)
	}

	// Verify the blocks directory was created.
	blocksDir := filepath.Join(dir, "1", "blocks")
	info, err := os.Stat(blocksDir)
	if err != nil {
		t.Fatalf("blocks directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("blocks path is not a directory")
	}
}

func TestStoreListReturnsOnlyFinalized(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	a1, _ := store.Create("art1", "run-1")
	store.Create("art2", "run-1") // not finalized

	store.Finalize(a1.ID, 50)

	result := store.List("run-1", "")
	if len(result) != 1 {
		t.Fatalf("expected 1 finalized artifact, got %d", len(result))
	}
	if result[0].Name != "art1" {
		t.Fatalf("expected art1, got %s", result[0].Name)
	}
}

// --- NewServer tests ---

func TestNewServerInvalidDir(t *testing.T) {
	// Use a path that cannot be created (inside a file, not a directory).
	tmpFile := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(tmpFile, []byte("x"), 0o644)

	_, err := NewServer(filepath.Join(tmpFile, "subdir"), "http://localhost")
	if err == nil {
		t.Fatal("expected error when artifact dir cannot be created")
	}
}

func TestNewServerTrimsTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir, "http://localhost:8080/")
	if err != nil {
		t.Fatal(err)
	}
	if srv.baseURL != "http://localhost:8080" {
		t.Errorf("baseURL = %q, want no trailing slash", srv.baseURL)
	}
}

// --- V4 handler edge case tests ---

func TestV4CreateArtifactInvalidJSON(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact",
		"application/json",
		strings.NewReader("not valid json{{{"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV4UploadMissingArtifactID(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// PUT to upload path with no artifact ID segment.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/_apis/results/upload/", strings.NewReader("data"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV4UploadArtifactNotFound(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/_apis/results/upload/99999?sig=unused", strings.NewReader("data"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestV4UploadBlockFlow(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// 1. Create artifact.
	createBody := `{"workflow_run_backend_id":"run-blk","workflow_job_run_backend_id":"job-blk","name":"block-art","version":4}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", createBody)

	var createResp struct {
		SignedUploadURL string `json:"signed_upload_url"`
	}
	decodeJSON(t, resp, &createResp)

	// Extract the artifact ID from the upload URL.
	// URL looks like: http://host/_apis/results/upload/ID?sig=unused
	uploadBase := createResp.SignedUploadURL
	// Strip the ?sig=unused to get the base, then add block params.
	baseURL := strings.SplitN(uploadBase, "?", 2)[0]

	// 2. Upload two blocks.
	block1Data := []byte("BLOCK1DATA")
	block2Data := []byte("BLOCK2DATA")

	req1, _ := http.NewRequest(http.MethodPut, baseURL+"?comp=block&blockid=blk1", bytes.NewReader(block1Data))
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("block1 upload status %d", resp1.StatusCode)
	}
	if resp1.Header.Get("x-ms-request-id") != "ions" {
		t.Error("expected x-ms-request-id header")
	}

	req2, _ := http.NewRequest(http.MethodPut, baseURL+"?comp=block&blockid=blk2", bytes.NewReader(block2Data))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("block2 upload status %d", resp2.StatusCode)
	}

	// 3. Commit block list.
	blockListXML := `<BlockList><Latest>blk1</Latest><Latest>blk2</Latest></BlockList>`
	req3, _ := http.NewRequest(http.MethodPut, baseURL+"?comp=blocklist", strings.NewReader(blockListXML))
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusCreated {
		t.Fatalf("blocklist commit status %d", resp3.StatusCode)
	}
	if resp3.Header.Get("ETag") == "" {
		t.Error("expected ETag header on blocklist commit")
	}

	// 4. Finalize.
	finalizeBody := fmt.Sprintf(`{"workflow_run_backend_id":"run-blk","workflow_job_run_backend_id":"job-blk","name":"block-art","size":"%d"}`, len(block1Data)+len(block2Data))
	resp4 := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", finalizeBody)
	var finResp struct {
		OK bool `json:"ok"`
	}
	decodeJSON(t, resp4, &finResp)
	if !finResp.OK {
		t.Fatal("expected finalize ok=true")
	}

	// 5. Download and verify blocks were assembled correctly.
	getURLBody := `{"workflow_run_backend_id":"run-blk","name":"block-art"}`
	resp5 := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL", getURLBody)
	var urlResp struct {
		SignedURL string `json:"signed_url"`
	}
	decodeJSON(t, resp5, &urlResp)

	dlResp, err := http.Get(urlResp.SignedURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()
	body, _ := io.ReadAll(dlResp.Body)

	expected := "BLOCK1DATABLOCK2DATA"
	if string(body) != expected {
		t.Fatalf("expected %q, got %q", expected, body)
	}
}

func TestV4FinalizeArtifactInvalidJSON(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		"application/json",
		strings.NewReader("{bad json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV4FinalizeArtifactNotFound(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		"application/json",
		strings.NewReader(`{"workflow_run_backend_id":"no-run","workflow_job_run_backend_id":"no-job","name":"no-art","size":"0"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestV4ListArtifactsInvalidJSON(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts",
		"application/json",
		strings.NewReader("{{invalid"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV4ListArtifactsWithNameFilterString(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create and finalize an artifact.
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact",
		`{"workflow_run_backend_id":"run-f","workflow_job_run_backend_id":"job-f","name":"target","version":4}`)

	// We need to finalize through the unfinalized path to test name_filter as a plain string.
	// The artifact was created but not finalized, so finalize it.
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		`{"workflow_run_backend_id":"run-f","workflow_job_run_backend_id":"job-f","name":"target","size":"0"}`)

	// Also create a second artifact that should NOT match the filter.
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact",
		`{"workflow_run_backend_id":"run-f","workflow_job_run_backend_id":"job-f","name":"other","version":4}`)
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		`{"workflow_run_backend_id":"run-f","workflow_job_run_backend_id":"job-f","name":"other","size":"0"}`)

	// List with name_filter as a plain JSON string (protobuf style).
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts",
		`{"workflow_run_backend_id":"run-f","name_filter":"target"}`)

	var listResp struct {
		Artifacts []struct {
			Name string `json:"name"`
		} `json:"artifacts"`
	}
	decodeJSON(t, resp, &listResp)

	if len(listResp.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(listResp.Artifacts))
	}
	if listResp.Artifacts[0].Name != "target" {
		t.Fatalf("expected 'target', got %s", listResp.Artifacts[0].Name)
	}
}

func TestV4GetSignedURLInvalidJSON(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL",
		"application/json",
		strings.NewReader("{broken"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV4GetSignedURLNotFound(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL",
		"application/json",
		strings.NewReader(`{"workflow_run_backend_id":"no-run","name":"no-art"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestV4DownloadMissingID(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/results/download/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV4DownloadNonexistentArtifact(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/results/download/99999?sig=unused")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// http.ServeFile returns 404 when the file doesn't exist.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent blob, got %d", resp.StatusCode)
	}
}

func TestV4UploadSimplePutBlob(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create artifact.
	createBody := `{"workflow_run_backend_id":"run-simple","workflow_job_run_backend_id":"job-simple","name":"simple-blob","version":4}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", createBody)

	var createResp struct {
		SignedUploadURL string `json:"signed_upload_url"`
	}
	decodeJSON(t, resp, &createResp)

	// Upload via simple PUT (no comp= query param).
	content := []byte("simple blob content here")
	req, _ := http.NewRequest(http.MethodPut, createResp.SignedUploadURL, bytes.NewReader(content))
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", uploadResp.StatusCode)
	}
	if uploadResp.Header.Get("x-ms-request-id") != "ions" {
		t.Error("expected x-ms-request-id header")
	}
	if uploadResp.Header.Get("ETag") == "" {
		t.Error("expected ETag header")
	}
}

// --- V3 handler edge case tests ---

func TestV3CreateArtifactInvalidJSON(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/_apis/pipelines/workflows/run-1/artifacts",
		"application/json",
		strings.NewReader("not json!"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV3CreateArtifactNameFromBody(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// When artifactName query param is missing, the name should come from the JSON body.
	createBody := `{"type":"actions_storage","name":"from-body"}`
	resp := doPost(t, ts, "/_apis/pipelines/workflows/run-1/artifacts", createBody)

	var createResp struct {
		Name string `json:"name"`
	}
	decodeJSON(t, resp, &createResp)

	if createResp.Name != "from-body" {
		t.Fatalf("expected name 'from-body', got %s", createResp.Name)
	}
}

func TestV3UploadMissingContainerID(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/_apis/resources/Containers/", strings.NewReader("data"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV3UploadArtifactNotFound(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/_apis/resources/Containers/99999?itemPath=test/file.txt", strings.NewReader("data"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestV3UploadWithContentRange(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create artifact.
	createBody := `{"type":"actions_storage","name":"ranged"}`
	resp := doPost(t, ts, "/_apis/pipelines/workflows/run-1/artifacts?artifactName=ranged", createBody)

	var createResp struct {
		ContainerID int `json:"containerId"`
	}
	decodeJSON(t, resp, &createResp)

	containerID := createResp.ContainerID

	// Upload with Content-Range header.
	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=ranged/file.txt", ts.URL, containerID)
	req, _ := http.NewRequest(http.MethodPut, uploadURL, strings.NewReader("CHUNK1"))
	req.Header.Set("Content-Range", "bytes 0-5/12")
	resp1, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp1.StatusCode)
	}

	// Upload second chunk.
	req2, _ := http.NewRequest(http.MethodPut, uploadURL, strings.NewReader("CHUNK2"))
	req2.Header.Set("Content-Range", "bytes 6-11/12")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for chunk2, got %d", resp2.StatusCode)
	}
}

func TestV3UploadInvalidContentRange(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create artifact.
	createBody := `{"type":"actions_storage","name":"bad-range"}`
	resp := doPost(t, ts, "/_apis/pipelines/workflows/run-1/artifacts?artifactName=bad-range", createBody)

	var createResp struct {
		ContainerID int `json:"containerId"`
	}
	decodeJSON(t, resp, &createResp)

	containerID := createResp.ContainerID

	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=bad-range/file.txt", ts.URL, containerID)
	req, _ := http.NewRequest(http.MethodPut, uploadURL, strings.NewReader("data"))
	req.Header.Set("Content-Range", "bytes INVALID")
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid Content-Range, got %d", uploadResp.StatusCode)
	}
}

func TestV3FinalizeArtifactNotFound(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/_apis/pipelines/workflows/run-missing/artifacts?artifactName=nonexistent",
		strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestV3DownloadMissingContainerID(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/resources/Containers/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestV3DownloadNonexistentArtifact(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/resources/Containers/99999?itemPath=test/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// http.ServeFile returns 404 for nonexistent files.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestV3ListArtifactsWithNameFilter(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create two artifacts.
	doPost(t, ts, "/_apis/pipelines/workflows/run-list/artifacts?artifactName=alpha",
		`{"type":"actions_storage","name":"alpha"}`)
	doPost(t, ts, "/_apis/pipelines/workflows/run-list/artifacts?artifactName=beta",
		`{"type":"actions_storage","name":"beta"}`)

	// Finalize both.
	doPatch(t, ts, "/_apis/pipelines/workflows/run-list/artifacts?artifactName=alpha", "{}")
	doPatch(t, ts, "/_apis/pipelines/workflows/run-list/artifacts?artifactName=beta", "{}")

	// List with name filter.
	resp, err := http.Get(ts.URL + "/_apis/pipelines/workflows/run-list/artifacts?artifactName=alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var lr struct {
		Count int `json:"count"`
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	if lr.Count != 1 {
		t.Fatalf("expected count 1, got %d", lr.Count)
	}
	if lr.Value[0].Name != "alpha" {
		t.Fatalf("expected 'alpha', got %s", lr.Value[0].Name)
	}
}

func TestV3FullLifecycleWithDownload(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	payload := []byte("v3 full lifecycle data")

	// Create.
	createBody := `{"type":"actions_storage","name":"lifecycle"}`
	resp := doPost(t, ts, "/_apis/pipelines/workflows/run-lc/artifacts?artifactName=lifecycle", createBody)

	var cr struct {
		ContainerID              int    `json:"containerId"`
		FileContainerResourceURL string `json:"fileContainerResourceUrl"`
		Type                     string `json:"type"`
	}
	decodeJSON(t, resp, &cr)

	if cr.Type != "actions_storage" {
		t.Fatalf("expected type actions_storage, got %s", cr.Type)
	}

	// Upload without Content-Range.
	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=lifecycle/data.bin", ts.URL, cr.ContainerID)
	req, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(payload))
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload status %d", uploadResp.StatusCode)
	}

	// Verify upload response body.
	// Re-do the upload to parse the response.
	req2, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(payload))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	var uploadBody struct {
		ContainerID int    `json:"containerId"`
		ItemPath    string `json:"itemPath"`
		IsFile      bool   `json:"isFile"`
	}
	json.NewDecoder(resp2.Body).Decode(&uploadBody)
	resp2.Body.Close()

	if !uploadBody.IsFile {
		t.Error("expected isFile=true")
	}
	if uploadBody.ItemPath != "lifecycle/data.bin" {
		t.Errorf("expected itemPath 'lifecycle/data.bin', got %s", uploadBody.ItemPath)
	}

	// Finalize.
	doPatch(t, ts, "/_apis/pipelines/workflows/run-lc/artifacts?artifactName=lifecycle", "{}")

	// Download.
	dlResp, err := http.Get(fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=lifecycle/data.bin", ts.URL, cr.ContainerID))
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()

	body, _ := io.ReadAll(dlResp.Body)
	if string(body) != string(payload) {
		t.Fatalf("expected %q, got %q", payload, body)
	}
}

func TestV3FinalizeAlreadyFinalized(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create and finalize.
	doPost(t, ts, "/_apis/pipelines/workflows/run-dup/artifacts?artifactName=dup-art",
		`{"type":"actions_storage","name":"dup-art"}`)
	doPatch(t, ts, "/_apis/pipelines/workflows/run-dup/artifacts?artifactName=dup-art", "{}")

	// Finalize again — should find via FindByName and succeed.
	resp := doPatch(t, ts, "/_apis/pipelines/workflows/run-dup/artifacts?artifactName=dup-art", "{}")
	var finResp struct {
		Name string `json:"name"`
	}
	decodeJSON(t, resp, &finResp)
	if finResp.Name != "dup-art" {
		t.Fatalf("expected name dup-art, got %s", finResp.Name)
	}
}

func TestV4ListArtifactsNoFilter(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create and finalize two artifacts in the same run.
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact",
		`{"workflow_run_backend_id":"run-nf","workflow_job_run_backend_id":"job-nf","name":"art-a","version":4}`)
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		`{"workflow_run_backend_id":"run-nf","workflow_job_run_backend_id":"job-nf","name":"art-a","size":"0"}`)

	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact",
		`{"workflow_run_backend_id":"run-nf","workflow_job_run_backend_id":"job-nf","name":"art-b","version":4}`)
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		`{"workflow_run_backend_id":"run-nf","workflow_job_run_backend_id":"job-nf","name":"art-b","size":"0"}`)

	// List without name_filter.
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts",
		`{"workflow_run_backend_id":"run-nf"}`)

	var listResp struct {
		Artifacts []struct {
			Name string `json:"name"`
		} `json:"artifacts"`
	}
	decodeJSON(t, resp, &listResp)

	if len(listResp.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(listResp.Artifacts))
	}
}

func TestFindUnfinalized(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}

	// Create an artifact through the store directly.
	a, err := srv.store.Create("unfinished", "run-u")
	if err != nil {
		t.Fatal(err)
	}

	// findUnfinalized should find it.
	found := srv.findUnfinalized("run-u", "unfinished")
	if found == nil {
		t.Fatal("expected to find unfinalized artifact")
	}
	if found.ID != a.ID {
		t.Fatalf("expected ID %s, got %s", a.ID, found.ID)
	}

	// Not found for wrong run.
	if srv.findUnfinalized("wrong-run", "unfinished") != nil {
		t.Fatal("expected nil for wrong run ID")
	}

	// Not found for wrong name.
	if srv.findUnfinalized("run-u", "wrong-name") != nil {
		t.Fatal("expected nil for wrong name")
	}

	// After finalizing, findUnfinalized should NOT find it.
	srv.store.Finalize(a.ID, 0)
	if srv.findUnfinalized("run-u", "unfinished") != nil {
		t.Fatal("expected nil after finalization")
	}
}

func TestV4FinalizeViaFindByNamePath(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create, upload, and finalize an artifact.
	createBody := `{"workflow_run_backend_id":"run-fn","workflow_job_run_backend_id":"job-fn","name":"find-name","version":4}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", createBody)
	var cr struct {
		SignedUploadURL string `json:"signed_upload_url"`
	}
	decodeJSON(t, resp, &cr)

	// Upload content.
	req, _ := http.NewRequest(http.MethodPut, cr.SignedUploadURL, strings.NewReader("data"))
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	uploadResp.Body.Close()

	// Finalize — first time uses findUnfinalized path.
	finalizeBody := `{"workflow_run_backend_id":"run-fn","workflow_job_run_backend_id":"job-fn","name":"find-name","size":"4"}`
	resp2 := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", finalizeBody)
	var finResp struct {
		OK bool `json:"ok"`
	}
	decodeJSON(t, resp2, &finResp)
	if !finResp.OK {
		t.Fatal("expected ok=true")
	}

	// Finalize again — this time FindByName should find the already-finalized artifact.
	resp3 := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", finalizeBody)
	var finResp2 struct {
		OK bool `json:"ok"`
	}
	decodeJSON(t, resp3, &finResp2)
	if !finResp2.OK {
		t.Fatal("expected ok=true on re-finalize")
	}
}

func TestV4UploadBlockListWithMissingBlocks(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create artifact.
	createBody := `{"workflow_run_backend_id":"run-miss","workflow_job_run_backend_id":"job-miss","name":"miss-blocks","version":4}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", createBody)

	var createResp struct {
		SignedUploadURL string `json:"signed_upload_url"`
	}
	decodeJSON(t, resp, &createResp)

	baseURL := strings.SplitN(createResp.SignedUploadURL, "?", 2)[0]

	// Upload only block1, NOT block2.
	block1Data := []byte("BLOCK1ONLY")
	req1, _ := http.NewRequest(http.MethodPut, baseURL+"?comp=block&blockid=blk1", bytes.NewReader(block1Data))
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("block1 upload status %d", resp1.StatusCode)
	}

	// Commit block list referencing both blk1 and blk2 (blk2 is missing).
	// This should succeed but only assemble blk1 data (blk2 is logged and skipped).
	blockListXML := `<BlockList><Latest>blk1</Latest><Latest>blk2</Latest></BlockList>`
	req2, _ := http.NewRequest(http.MethodPut, baseURL+"?comp=blocklist", strings.NewReader(blockListXML))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("blocklist commit status %d", resp2.StatusCode)
	}

	// Finalize.
	finalizeBody := `{"workflow_run_backend_id":"run-miss","workflow_job_run_backend_id":"job-miss","name":"miss-blocks","size":"10"}`
	doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", finalizeBody)

	// Download and verify only block1 data is present.
	getURLBody := `{"workflow_run_backend_id":"run-miss","name":"miss-blocks"}`
	resp3 := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL", getURLBody)
	var urlResp struct {
		SignedURL string `json:"signed_url"`
	}
	decodeJSON(t, resp3, &urlResp)

	dlResp, err := http.Get(urlResp.SignedURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()
	body, _ := io.ReadAll(dlResp.Body)
	if string(body) != "BLOCK1ONLY" {
		t.Fatalf("expected 'BLOCK1ONLY', got %q", body)
	}
}

func TestV4UploadBlockListReadAllError(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create artifact.
	createBody := `{"workflow_run_backend_id":"run-ble","workflow_job_run_backend_id":"job-ble","name":"empty-blocklist","version":4}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", createBody)

	var createResp struct {
		SignedUploadURL string `json:"signed_upload_url"`
	}
	decodeJSON(t, resp, &createResp)

	baseURL := strings.SplitN(createResp.SignedUploadURL, "?", 2)[0]

	// Commit empty block list — should still succeed with empty blob.
	req, _ := http.NewRequest(http.MethodPut, baseURL+"?comp=blocklist", strings.NewReader(`<BlockList></BlockList>`))
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("empty blocklist commit status %d", resp2.StatusCode)
	}
}

func TestStoreCreateMultipleIDs(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	a1, _ := store.Create("a", "r")
	a2, _ := store.Create("b", "r")
	a3, _ := store.Create("c", "r")

	if a1.ID == a2.ID || a2.ID == a3.ID || a1.ID == a3.ID {
		t.Fatalf("expected unique IDs, got %s, %s, %s", a1.ID, a2.ID, a3.ID)
	}
}

// --- Tests targeting specific uncovered error branches ---

// TestStoreCreateMkdirError covers store.go:51-53 — os.MkdirAll failure in Create.
func TestStoreCreateMkdirError(t *testing.T) {
	// Create a file where a directory is expected, so MkdirAll fails.
	dir := t.TempDir()
	store := NewStore(dir)

	// Write a file at the path where the artifact directory would be created.
	// The idCounter starts at 0, so the next ID will be "1".
	blockingFile := filepath.Join(dir, "1")
	os.WriteFile(blockingFile, []byte("not a directory"), 0o644)

	_, err := store.Create("test", "run-1")
	if err == nil {
		t.Fatal("expected error when artifact dir cannot be created")
	}
	if !strings.Contains(err.Error(), "create artifact dir") {
		t.Fatalf("expected 'create artifact dir' error, got: %v", err)
	}
}

// TestStoreCreateWriteMetadataError covers store.go:55-57 — writeMetadata failure in Create.
func TestStoreCreateWriteMetadataError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Make the artifact directory first, then make it read-only so writeMetadata fails.
	// The next ID will be "1".
	artDir := filepath.Join(dir, "1")
	os.MkdirAll(artDir, 0o755)
	// Place a directory at the metadata path so WriteFile fails.
	metaPath := filepath.Join(artDir, "metadata.json")
	os.MkdirAll(metaPath, 0o755)

	_, err := store.Create("test", "run-1")
	if err == nil {
		t.Fatal("expected error when writeMetadata fails")
	}
}

// TestWriteMetadataMarshalError covers store.go:141-143 — json.Marshal error in writeMetadata.
// This is extremely hard to trigger since Artifact has only basic types.
// Instead, we test writeMetadata with a valid artifact to ensure the non-error path
// and test the marshal error indirectly through an invalid directory.
func TestWriteMetadataFileError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	a, err := store.Create("meta-test", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Now corrupt the directory: replace the artifact dir with a file.
	artDir := filepath.Join(dir, a.ID)
	os.RemoveAll(artDir)
	os.WriteFile(artDir, []byte("not a dir"), 0o644)

	// Finalize will call writeMetadata which should fail.
	err = store.Finalize(a.ID, 100)
	if err == nil {
		t.Fatal("expected error from writeMetadata when artifact dir is corrupted")
	}
}

// TestV4CreateArtifactStoreError covers artifacts.go:64-67 — store.Create error in handleV4CreateArtifact.
func TestV4CreateArtifactStoreError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	// Corrupt the artifact directory so os.MkdirAll fails for the next artifact.
	// Next ID will be "1", so place a file there.
	os.WriteFile(filepath.Join(dir, "1"), []byte("blocker"), 0o644)

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact",
		"application/json",
		strings.NewReader(`{"workflow_run_backend_id":"run-1","workflow_job_run_backend_id":"job-1","name":"test","version":4}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV4UploadBlockCreateFileError covers artifacts.go:98-101 — os.Create(blockPath) error.
func TestV4UploadBlockCreateFileError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	// Create artifact normally.
	a, err := srv.store.Create("block-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Make the blocks directory a file so os.Create inside it fails.
	blocksDir := filepath.Join(dir, a.ID, "blocks")
	os.MkdirAll(filepath.Dir(blocksDir), 0o755)
	os.WriteFile(blocksDir, []byte("not-a-dir"), 0o644)

	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/_apis/results/upload/%s?comp=block&blockid=blk1", ts.URL, a.ID),
		strings.NewReader("data"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV4UploadBlockListReadError covers artifacts.go:114-117 — io.ReadAll(r.Body) error.
// This is hard to trigger since httptest handles bodies well. We test the blocklist
// os.Create error path instead.

// TestV4UploadBlockListCreateBlobError covers artifacts.go:122-125 — os.Create(blobPath) in blocklist case.
func TestV4UploadBlockListCreateBlobError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	a, err := srv.store.Create("blocklist-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Place a directory at the blob path so os.Create fails.
	blobPath := srv.store.BlobPath(a.ID)
	os.MkdirAll(blobPath, 0o755)

	blockListXML := `<BlockList><Latest>blk1</Latest></BlockList>`
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/_apis/results/upload/%s?comp=blocklist", ts.URL, a.ID),
		strings.NewReader(blockListXML))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV4UploadSimplePutCreateBlobError covers artifacts.go:149-152 — os.Create(blobPath) error in default case.
func TestV4UploadSimplePutCreateBlobError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	a, err := srv.store.Create("put-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Place a directory at the blob path so os.Create fails.
	blobPath := srv.store.BlobPath(a.ID)
	os.MkdirAll(blobPath, 0o755)

	// Simple PUT (no comp= parameter).
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/_apis/results/upload/%s?sig=unused", ts.URL, a.ID),
		strings.NewReader("data"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV4FinalizeStoreError covers artifacts.go:194-197 — store.Finalize error in handleV4FinalizeArtifact.
func TestV4FinalizeStoreError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	// Create an artifact.
	a, err := srv.store.Create("fin-err", "run-err")
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the artifact directory so writeMetadata (called by Finalize) fails.
	artDir := filepath.Join(dir, a.ID)
	os.RemoveAll(artDir)
	os.WriteFile(artDir, []byte("corrupted"), 0o644)

	resp, err := http.Post(
		ts.URL+"/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact",
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"workflow_run_backend_id":"run-err","workflow_job_run_backend_id":"job","name":"fin-err","size":"0"}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV3CreateArtifactStoreError covers artifacts.go:311-314 — store.Create error in handleV3CreateArtifact.
func TestV3CreateArtifactStoreError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	// Corrupt: place a file where the next artifact directory would be.
	os.WriteFile(filepath.Join(dir, "1"), []byte("blocker"), 0o644)

	resp, err := http.Post(
		ts.URL+"/_apis/pipelines/workflows/run-1/artifacts?artifactName=test",
		"application/json",
		strings.NewReader(`{"type":"actions_storage","name":"test"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV3UploadWithContentRangeOpenFileError covers artifacts.go:356-359 — os.OpenFile error.
func TestV3UploadWithContentRangeOpenFileError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	a, err := srv.store.Create("range-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Place a directory at the blob path so os.OpenFile fails.
	blobPath := srv.store.BlobPath(a.ID)
	os.MkdirAll(blobPath, 0o755)

	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%s?itemPath=range-err/file.txt", ts.URL, a.ID)
	req, _ := http.NewRequest(http.MethodPut, uploadURL, strings.NewReader("data"))
	req.Header.Set("Content-Range", "bytes 0-3/4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV3UploadNoContentRangeCreateError covers artifacts.go:373-376 — os.Create error without Content-Range.
func TestV3UploadNoContentRangeCreateError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	a, err := srv.store.Create("nocr-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Place a directory at the blob path so os.Create fails.
	blobPath := srv.store.BlobPath(a.ID)
	os.MkdirAll(blobPath, 0o755)

	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%s?itemPath=nocr-err/file.txt", ts.URL, a.ID)
	req, _ := http.NewRequest(http.MethodPut, uploadURL, strings.NewReader("data"))
	// No Content-Range header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV3FinalizeStoreError covers artifacts.go:415-418 — store.Finalize error in handleV3FinalizeArtifact.
func TestV3FinalizeStoreError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	// Create an artifact.
	a, err := srv.store.Create("v3-fin-err", "run-fin")
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the artifact directory so writeMetadata (called by Finalize) fails.
	artDir := filepath.Join(dir, a.ID)
	os.RemoveAll(artDir)
	os.WriteFile(artDir, []byte("corrupted"), 0o644)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/_apis/pipelines/workflows/run-fin/artifacts?artifactName=v3-fin-err",
		strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// TestV4UploadBlockCopyError covers artifacts.go:103-106 — io.Copy error in block upload.
// We use an errorReader to simulate a read error from the request body.
type errorReader struct{}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("simulated read error")
}

func TestV4UploadBlockCopyError(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	a, err := srv.store.Create("copy-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// We need to trigger an io.Copy error. One way is to make the block file read-only
	// after creating it. But os.Create truncates, so the file is fresh.
	// Instead, make the blocks directory, then make the block file a directory.
	blocksDir := filepath.Join(dir, a.ID, "blocks")
	os.MkdirAll(blocksDir, 0o755)
	// Create a directory at the block ID path — os.Create will succeed but writing will work...
	// Actually, os.Create on a directory path will fail. Let's use a different approach.
	// Make the block path a directory so os.Create fails (covered by TestV4UploadBlockCreateFileError).
	// For io.Copy error, we need the file to be created but writes to fail.
	// This is tricky in a test. Let's use a pipe with a closed writer instead, via direct handler call.

	// Direct handler call with an errorReader body.
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("/_apis/results/upload/%s?comp=block&blockid=blk-err", a.ID),
		&errorReader{})
	w := httptest.NewRecorder()
	srv.handleV4Upload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestV4UploadBlockListBodyReadError covers artifacts.go:114-117 — io.ReadAll error in blocklist.
func TestV4UploadBlockListBodyReadError(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}

	a, err := srv.store.Create("readall-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("/_apis/results/upload/%s?comp=blocklist", a.ID),
		&errorReader{})
	w := httptest.NewRecorder()
	srv.handleV4Upload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestV4UploadSimplePutCopyError covers artifacts.go:156-159 — io.Copy error in simple PUT.
func TestV4UploadSimplePutCopyError(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}

	a, err := srv.store.Create("put-copy-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// Simple PUT (no comp= parameter) with an errorReader body.
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("/_apis/results/upload/%s?sig=unused", a.ID),
		&errorReader{})
	w := httptest.NewRecorder()
	srv.handleV4Upload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestV3UploadContentRangeCopyError covers artifacts.go:367-370 — io.Copy error with Content-Range.
func TestV3UploadContentRangeCopyError(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}

	a, err := srv.store.Create("v3-copy-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("/_apis/resources/Containers/%s?itemPath=test/file.txt", a.ID),
		&errorReader{})
	req.Header.Set("Content-Range", "bytes 0-3/4")
	// Need to set the path such that the handler can extract the ID.
	// The handler does: strings.TrimPrefix(r.URL.Path, "/_apis/resources/Containers/")
	w := httptest.NewRecorder()
	srv.handleV3Upload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestV3UploadNoContentRangeCopyError covers artifacts.go:379-382 — io.Copy error without Content-Range.
func TestV3UploadNoContentRangeCopyError(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}

	a, err := srv.store.Create("v3-nocr-copy-err", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("/_apis/resources/Containers/%s?itemPath=test/file.txt", a.ID),
		&errorReader{})
	// No Content-Range header.
	w := httptest.NewRecorder()
	srv.handleV3Upload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestV3UploadContentRangeSeekError covers artifacts.go:362-365 — f.Seek error.
// This is extremely hard to trigger with a real file. We test it by ensuring the
// Content-Range with a valid file works, and instead trigger the OpenFile error path
// more thoroughly. The seek error is practically unreachable on a normal filesystem,
// but we can attempt it with a nonsensical seek position if the file doesn't support it.
// In practice, the OpenFile error covers the most likely failure mode.

// TestParseContentRangeNoSlash covers artifacts.go:510-512 — len(parts) < 1 branch.
// SplitN with n=2 on a non-empty string always returns at least 1 part,
// but the code has a guard for it. We test with a string that has no "/" at all.
func TestParseContentRangeNoSlash(t *testing.T) {
	// "bytes 0-1023" with no "/" — SplitN returns ["0-1023"], len == 1, which is >= 1.
	// The code checks len(parts) < 1 which is never true with SplitN(..., 2).
	// But the range parsing will still work since parts[0] = "0-1023".
	start, end, err := parseContentRange("bytes 0-1023")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if start != 0 || end != 1023 {
		t.Fatalf("expected 0-1023, got %d-%d", start, end)
	}
}

func TestV3ListMultipleArtifacts(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create and finalize three artifacts.
	for _, name := range []string{"a1", "a2", "a3"} {
		body := fmt.Sprintf(`{"type":"actions_storage","name":"%s"}`, name)
		doPost(t, ts, fmt.Sprintf("/_apis/pipelines/workflows/run-multi/artifacts?artifactName=%s", name), body)
		doPatch(t, ts, fmt.Sprintf("/_apis/pipelines/workflows/run-multi/artifacts?artifactName=%s", name), "{}")
	}

	// List all.
	resp, err := http.Get(ts.URL + "/_apis/pipelines/workflows/run-multi/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var lr struct {
		Count int `json:"count"`
		Value []struct {
			Name                     string `json:"name"`
			Type                     string `json:"type"`
			FileContainerResourceURL string `json:"fileContainerResourceUrl"`
		} `json:"value"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	if lr.Count != 3 {
		t.Fatalf("expected count 3, got %d", lr.Count)
	}

	// Verify all entries have correct type and non-empty URLs.
	for _, v := range lr.Value {
		if v.Type != "actions_storage" {
			t.Errorf("expected type actions_storage, got %s", v.Type)
		}
		if v.FileContainerResourceURL == "" {
			t.Errorf("expected non-empty fileContainerResourceUrl for %s", v.Name)
		}
	}
}
