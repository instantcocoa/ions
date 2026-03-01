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
	"testing"
)

// --- Store tests ---

func TestStoreCreateAndGet(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	a, err := store.Create("my-artifact", "run-1")
	if err != nil {
		t.Fatal(err)
	}

	if a.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if a.Name != "my-artifact" {
		t.Fatalf("expected name my-artifact, got %s", a.Name)
	}
	if a.WorkflowRunID != "run-1" {
		t.Fatalf("expected workflowRunId run-1, got %s", a.WorkflowRunID)
	}
	if a.Finalized {
		t.Fatal("expected not finalized")
	}

	got := store.Get(a.ID)
	if got == nil {
		t.Fatal("expected to find artifact by ID")
	}
	if got.Name != "my-artifact" {
		t.Fatalf("expected name my-artifact, got %s", got.Name)
	}

	// Metadata file should exist on disk.
	metaPath := filepath.Join(dir, a.ID, "metadata.json")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Fatal("expected metadata.json to exist")
	}
}

func TestStoreGetNotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	if got := store.Get("nonexistent"); got != nil {
		t.Fatal("expected nil for nonexistent ID")
	}
}

func TestStoreListWithFilters(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	a1, _ := store.Create("art-a", "run-1")
	a2, _ := store.Create("art-b", "run-1")
	a3, _ := store.Create("art-a", "run-2")

	// Nothing finalized yet — list should be empty.
	if got := store.List("", ""); len(got) != 0 {
		t.Fatalf("expected 0 finalized, got %d", len(got))
	}

	store.Finalize(a1.ID, 100)
	store.Finalize(a2.ID, 200)
	store.Finalize(a3.ID, 300)

	// List all.
	all := store.List("", "")
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}

	// Filter by run.
	byRun := store.List("run-1", "")
	if len(byRun) != 2 {
		t.Fatalf("expected 2 for run-1, got %d", len(byRun))
	}

	// Filter by name.
	byName := store.List("", "art-a")
	if len(byName) != 2 {
		t.Fatalf("expected 2 for art-a, got %d", len(byName))
	}

	// Filter by both.
	byBoth := store.List("run-1", "art-b")
	if len(byBoth) != 1 {
		t.Fatalf("expected 1, got %d", len(byBoth))
	}
	if byBoth[0].Name != "art-b" {
		t.Fatalf("expected art-b, got %s", byBoth[0].Name)
	}
}

func TestStoreFinalize(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	a, _ := store.Create("test", "run-1")
	if a.Finalized {
		t.Fatal("should not be finalized yet")
	}

	if err := store.Finalize(a.ID, 42); err != nil {
		t.Fatal(err)
	}

	got := store.Get(a.ID)
	if !got.Finalized {
		t.Fatal("expected finalized")
	}
	if got.Size != 42 {
		t.Fatalf("expected size 42, got %d", got.Size)
	}
}

func TestStoreFinalizeNotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Finalize("999", 0); err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
}

// --- HTTP handler tests ---

func setupTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()

	mux := http.NewServeMux()
	// We need the baseURL before creating the server, but httptest gives it after.
	// Use a wrapper to set it up properly.
	ts := httptest.NewServer(mux)

	srv, err := NewServer(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterRoutes(mux)

	return srv, ts
}

func TestV4RoundTrip(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	payload := []byte("hello artifact content")

	// 1. CreateArtifact
	createBody := `{"workflow_run_backend_id":"run-1","workflow_job_run_backend_id":"job-1","name":"my-artifact","version":4}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", createBody)

	var createResp struct {
		OK              bool   `json:"ok"`
		SignedUploadURL string `json:"signed_upload_url"`
	}
	decodeJSON(t, resp, &createResp)

	if !createResp.OK {
		t.Fatal("expected ok=true")
	}
	if createResp.SignedUploadURL == "" {
		t.Fatal("expected signed_upload_url")
	}

	// 2. Upload blob via PUT.
	uploadReq, _ := http.NewRequest(http.MethodPut, createResp.SignedUploadURL, bytes.NewReader(payload))
	uploadResp, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		t.Fatal(err)
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", uploadResp.StatusCode)
	}

	// 3. FinalizeArtifact
	finalizeBody := fmt.Sprintf(`{"workflow_run_backend_id":"run-1","workflow_job_run_backend_id":"job-1","name":"my-artifact","size":"%d"}`, len(payload))
	resp = doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", finalizeBody)

	var finalizeResp struct {
		OK         bool   `json:"ok"`
		ArtifactID string `json:"artifact_id"`
	}
	decodeJSON(t, resp, &finalizeResp)

	if !finalizeResp.OK {
		t.Fatal("expected ok=true on finalize")
	}
	if finalizeResp.ArtifactID == "" {
		t.Fatal("expected artifact_id")
	}

	// 4. ListArtifacts
	listBody := `{"workflow_run_backend_id":"run-1","name_filter":{"value":"my-artifact"}}`
	resp = doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts", listBody)

	var listResp struct {
		Artifacts []struct {
			DatabaseID string `json:"database_id"`
			Name       string `json:"name"`
			Size       string `json:"size"`
		} `json:"artifacts"`
	}
	decodeJSON(t, resp, &listResp)

	if len(listResp.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(listResp.Artifacts))
	}
	if listResp.Artifacts[0].Name != "my-artifact" {
		t.Fatalf("expected name my-artifact, got %s", listResp.Artifacts[0].Name)
	}
	if listResp.Artifacts[0].Size != fmt.Sprintf("%d", len(payload)) {
		t.Fatalf("expected size %d, got %s", len(payload), listResp.Artifacts[0].Size)
	}

	// 5. GetSignedArtifactURL
	getURLBody := `{"workflow_run_backend_id":"run-1","name":"my-artifact"}`
	resp = doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL", getURLBody)

	var urlResp struct {
		SignedURL string `json:"signed_url"`
	}
	decodeJSON(t, resp, &urlResp)

	if urlResp.SignedURL == "" {
		t.Fatal("expected signed_url")
	}

	// 6. Download
	dlResp, err := http.Get(urlResp.SignedURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()

	body, _ := io.ReadAll(dlResp.Body)
	if string(body) != string(payload) {
		t.Fatalf("expected %q, got %q", payload, body)
	}
}

func TestV3RoundTrip(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	payload := []byte("v3 artifact data")

	// 1. Create container.
	createBody := `{"type":"actions_storage","name":"v3-artifact"}`
	resp := doPost(t, ts, "/_apis/pipelines/workflows/run-1/artifacts?artifactName=v3-artifact", createBody)

	var createResp struct {
		ContainerID              int    `json:"containerId"`
		Name                     string `json:"name"`
		FileContainerResourceURL string `json:"fileContainerResourceUrl"`
		Size                     int    `json:"size"`
		Type                     string `json:"type"`
	}
	decodeJSON(t, resp, &createResp)

	if createResp.Name != "v3-artifact" {
		t.Fatalf("expected name v3-artifact, got %s", createResp.Name)
	}
	if createResp.FileContainerResourceURL == "" {
		t.Fatal("expected fileContainerResourceUrl")
	}
	if createResp.Type != "actions_storage" {
		t.Fatalf("expected type actions_storage, got %s", createResp.Type)
	}

	containerID := createResp.ContainerID

	// 2. Upload blob via PUT.
	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=v3-artifact/data.txt", ts.URL, containerID)
	uploadReq, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(payload))
	uploadResp, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		t.Fatal(err)
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload status %d", uploadResp.StatusCode)
	}

	// 3. Finalize.
	finalizeResp := doPatch(t, ts, "/_apis/pipelines/workflows/run-1/artifacts?artifactName=v3-artifact", "{}")

	var finResp struct {
		ContainerID int    `json:"containerId"`
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		Type        string `json:"type"`
	}
	decodeJSON(t, finalizeResp, &finResp)

	if finResp.Name != "v3-artifact" {
		t.Fatalf("expected name v3-artifact, got %s", finResp.Name)
	}
	if finResp.Size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d", len(payload), finResp.Size)
	}

	// 4. List artifacts.
	listResp, err := http.Get(ts.URL + "/_apis/pipelines/workflows/run-1/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()

	var lr struct {
		Count int `json:"count"`
		Value []struct {
			ContainerID int    `json:"containerId"`
			Name        string `json:"name"`
			Size        int64  `json:"size"`
		} `json:"value"`
	}
	json.NewDecoder(listResp.Body).Decode(&lr)

	if lr.Count != 1 {
		t.Fatalf("expected count 1, got %d", lr.Count)
	}
	if lr.Value[0].Name != "v3-artifact" {
		t.Fatalf("expected v3-artifact, got %s", lr.Value[0].Name)
	}

	// 5. Download.
	dlResp, err := http.Get(fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=v3-artifact/data.txt", ts.URL, containerID))
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()

	body, _ := io.ReadAll(dlResp.Body)
	if string(body) != string(payload) {
		t.Fatalf("expected %q, got %q", payload, body)
	}
}

func TestV4ListEmpty(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	listBody := `{"workflow_run_backend_id":"run-empty"}`
	resp := doPost(t, ts, "/twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts", listBody)

	var listResp struct {
		Artifacts []any `json:"artifacts"`
	}
	decodeJSON(t, resp, &listResp)

	if listResp.Artifacts == nil {
		t.Fatal("expected empty array, got nil")
	}
	if len(listResp.Artifacts) != 0 {
		t.Fatalf("expected 0 artifacts, got %d", len(listResp.Artifacts))
	}
}

func TestV3ListEmpty(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/pipelines/workflows/run-empty/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var lr struct {
		Count int   `json:"count"`
		Value []any `json:"value"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	if lr.Count != 0 {
		t.Fatalf("expected count 0, got %d", lr.Count)
	}
	if lr.Value == nil {
		t.Fatal("expected empty array, got nil")
	}
}

func TestV3ChunkedUpload(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	// Create artifact.
	createBody := `{"type":"actions_storage","name":"chunked"}`
	resp := doPost(t, ts, "/_apis/pipelines/workflows/run-1/artifacts?artifactName=chunked", createBody)

	var createResp struct {
		ContainerID int `json:"containerId"`
	}
	decodeJSON(t, resp, &createResp)

	containerID := createResp.ContainerID

	chunk1 := []byte("AAAA") // bytes 0-3
	chunk2 := []byte("BBBB") // bytes 4-7

	// Upload chunk 1.
	uploadURL := fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=chunked/file.bin", ts.URL, containerID)
	req1, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(chunk1))
	req1.Header.Set("Content-Range", "bytes 0-3/8")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("chunk1 status %d", resp1.StatusCode)
	}

	// Upload chunk 2.
	req2, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(chunk2))
	req2.Header.Set("Content-Range", "bytes 4-7/8")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("chunk2 status %d", resp2.StatusCode)
	}

	// Finalize.
	doPatch(t, ts, "/_apis/pipelines/workflows/run-1/artifacts?artifactName=chunked", "{}")

	// Download and verify.
	dlResp, err := http.Get(fmt.Sprintf("%s/_apis/resources/Containers/%d?itemPath=chunked/file.bin", ts.URL, containerID))
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()

	body, _ := io.ReadAll(dlResp.Body)
	if string(body) != "AAAABBBB" {
		t.Fatalf("expected AAAABBBB, got %q", body)
	}
}

// --- Helpers ---

func doPost(t *testing.T, ts *httptest.Server, path, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("POST %s returned %d: %s", path, resp.StatusCode, b)
	}
	return resp
}

func doPatch(t *testing.T, ts *httptest.Server, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("PATCH %s returned %d: %s", path, resp.StatusCode, b)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
}
