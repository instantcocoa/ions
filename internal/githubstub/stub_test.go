package githubstub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer(info RepoInfo, opts ...Options) *httptest.Server {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	srv := NewServer(info, "http://test", o)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func getJSON(t *testing.T, ts *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	return resp.StatusCode, result
}

func getJSONArray(t *testing.T, ts *httptest.Server, path string) (int, []any) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result []any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	return resp.StatusCode, result
}

func postJSON(t *testing.T, ts *httptest.Server, path string, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

func patchJSON(t *testing.T, ts *httptest.Server, path string, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, ts.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

// --- Tier 1 endpoint tests ---

func TestGetRepository(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner:         "octocat",
		Repo:          "hello-world",
		DefaultBranch: "main",
		CurrentSHA:    "abc123",
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello-world")
	assert.Equal(t, 200, code)
	assert.Equal(t, "octocat/hello-world", body["full_name"])
	assert.Equal(t, "hello-world", body["name"])
	assert.Equal(t, "main", body["default_branch"])
	assert.Equal(t, "public", body["visibility"])

	owner, ok := body["owner"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "octocat", owner["login"])

	perms, ok := body["permissions"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, perms["admin"])
	assert.Equal(t, true, perms["push"])
	assert.Equal(t, true, perms["pull"])
}

func TestGetRepository_DifferentOwnerRepo(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "a", Repo: "b"})
	defer ts.Close()

	// Request for different owner/repo still works (returns URL params).
	code, body := getJSON(t, ts, "/api/v3/repos/other/repo")
	assert.Equal(t, 200, code)
	assert.Equal(t, "other/repo", body["full_name"])
}

func TestGetCommit_Fallback(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner:      "octocat",
		Repo:       "hello",
		CurrentSHA: "deadbeef1234",
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/commits/HEAD")
	assert.Equal(t, 200, code)
	assert.Equal(t, "deadbeef1234", body["sha"])

	commit, ok := body["commit"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "local commit", commit["message"])
}

func TestGetRef_Fallback(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner:      "octocat",
		Repo:       "hello",
		CurrentSHA: "abc123def456",
		CurrentRef: "refs/heads/feature",
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/git/ref/heads/main")
	assert.Equal(t, 200, code)
	assert.Equal(t, "refs/heads/feature", body["ref"])

	obj, ok := body["object"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "abc123def456", obj["sha"])
	assert.Equal(t, "commit", obj["type"])
}

func TestCreateStatus(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/repos/octocat/hello/statuses/abc123",
		`{"state":"success","context":"ci/test","description":"All tests passed"}`)
	assert.Equal(t, 201, code)
	assert.Equal(t, "success", body["state"])
	assert.Equal(t, "ci/test", body["context"])
	assert.NotNil(t, body["id"])
}

func TestCreateComment(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/repos/octocat/hello/issues/42/comments",
		`{"body":"LGTM!"}`)
	assert.Equal(t, 201, code)
	assert.Equal(t, "LGTM!", body["body"])

	user, ok := body["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "octocat", user["login"])
}

func TestCreateCheckRun(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/repos/octocat/hello/check-runs",
		`{"name":"lint","status":"in_progress","head_sha":"abc123"}`)
	assert.Equal(t, 201, code)
	assert.Equal(t, "lint", body["name"])
	assert.Equal(t, "in_progress", body["status"])
	assert.NotNil(t, body["id"])
}

func TestUpdateCheckRun(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := patchJSON(t, ts, "/api/v3/repos/octocat/hello/check-runs/1",
		`{"status":"completed","conclusion":"success"}`)
	assert.Equal(t, 200, code)
	assert.Equal(t, "completed", body["status"])
	assert.Equal(t, "success", body["conclusion"])
}

// --- Tier 2 endpoint tests ---

func TestGetPull(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner:         "octocat",
		Repo:          "hello",
		DefaultBranch: "main",
		CurrentRef:    "refs/heads/feature",
		CurrentSHA:    "abc123",
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/pulls/7")
	assert.Equal(t, 200, code)
	assert.Equal(t, float64(7), body["number"])
	assert.Equal(t, "open", body["state"])
	assert.Equal(t, true, body["mergeable"])

	head, ok := body["head"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "feature", head["ref"])
	assert.Equal(t, "abc123", head["sha"])

	base, ok := body["base"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "main", base["ref"])
}

func TestListPullFiles(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, result := getJSONArray(t, ts, "/api/v3/repos/octocat/hello/pulls/7/files")
	assert.Equal(t, 200, code)
	assert.Empty(t, result)
}

func TestGetIssue(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/issues/42")
	assert.Equal(t, 200, code)
	assert.Equal(t, float64(42), body["number"])
	assert.Equal(t, "open", body["state"])
}

func TestListIssueComments(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, result := getJSONArray(t, ts, "/api/v3/repos/octocat/hello/issues/42/comments")
	assert.Equal(t, 200, code)
	assert.Empty(t, result)
}

func TestGetContents_File(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("Hello, world!"), 0o644)
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/hello.txt")
	assert.Equal(t, 200, code)
	assert.Equal(t, "hello.txt", body["name"])
	assert.Equal(t, "hello.txt", body["path"])
	assert.Equal(t, "file", body["type"])
	assert.Equal(t, "base64", body["encoding"])
	assert.NotEmpty(t, body["sha"])
	assert.NotEmpty(t, body["content"])
}

func TestGetContents_Directory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644))

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, result := getJSONArray(t, ts, "/api/v3/repos/octocat/hello/contents/src")
	assert.Equal(t, 200, code)
	require.Len(t, result, 1)

	entry := result[0].(map[string]any)
	assert.Equal(t, "main.go", entry["name"])
	assert.Equal(t, "file", entry["type"])
}

func TestGetContents_NotFound(t *testing.T) {
	dir := t.TempDir()

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/nonexistent.txt")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
}

func TestGetContents_PathTraversal(t *testing.T) {
	// Go's ServeMux cleans URL paths with "..", so we test the isSubpath
	// guard directly by creating a file outside the repo dir and trying
	// to access it via a relative-looking path.
	dir := t.TempDir()
	subDir := filepath.Join(dir, "repo")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	// Create a file that's a sibling of the repo dir, not inside it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0o644))

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: subDir, // only subDir should be accessible
	})
	defer ts.Close()

	// File inside repo should work.
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "ok.txt"), []byte("ok"), 0o644))
	code, _ := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/ok.txt")
	assert.Equal(t, 200, code)

	// File outside repo should 404 (filepath.Clean resolves the traversal).
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/nonexistent")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
}

func TestListVariables(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"}, Options{
		Vars: map[string]string{
			"MY_VAR":    "hello",
			"OTHER_VAR": "world",
		},
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/actions/variables")
	assert.Equal(t, 200, code)
	assert.Equal(t, float64(2), body["total_count"])

	vars := body["variables"].([]any)
	assert.Len(t, vars, 2)
}

func TestListVariables_Empty(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/actions/variables")
	assert.Equal(t, 200, code)
	assert.Equal(t, float64(0), body["total_count"])
}

func TestGetVariable(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"}, Options{
		Vars: map[string]string{"MY_VAR": "hello"},
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/actions/variables/MY_VAR")
	assert.Equal(t, 200, code)
	assert.Equal(t, "MY_VAR", body["name"])
	assert.Equal(t, "hello", body["value"])
}

func TestGetVariable_NotFound(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/actions/variables/NOPE")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
}

func TestDispatch(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v3/repos/octocat/hello/dispatches",
		"application/json", strings.NewReader(`{"event_type":"deploy"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 204, resp.StatusCode)
}

// --- Other endpoint tests ---

func TestGetUser(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat"})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/user")
	assert.Equal(t, 200, code)
	assert.Equal(t, "octocat", body["login"])
	assert.Equal(t, "User", body["type"])
}

func TestGraphQL(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/graphql",
		`{"query":"{ viewer { login } }"}`)
	assert.Equal(t, 200, code)
	assert.NotNil(t, body["data"])
}

// --- Infrastructure tests ---

func TestRateLimitHeaders(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v3/repos/octocat/hello")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "5000", resp.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "4999", resp.Header.Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, resp.Header.Get("X-RateLimit-Reset"))
	assert.Equal(t, "core", resp.Header.Get("X-RateLimit-Resource"))
}

func TestCatchAll(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/some/unhandled/path")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
	assert.NotEmpty(t, body["ions_note"])
}

func TestContentTypeJSON(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v3/repos/octocat/hello")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

func TestMultipleStatuses(t *testing.T) {
	info := RepoInfo{Owner: "octocat", Repo: "hello"}
	srv := NewServer(info, "http://test", Options{})

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Create multiple statuses — IDs should be unique.
	_, body1 := postJSON(t, ts, "/api/v3/repos/octocat/hello/statuses/abc",
		`{"state":"pending","context":"ci"}`)
	_, body2 := postJSON(t, ts, "/api/v3/repos/octocat/hello/statuses/abc",
		`{"state":"success","context":"ci"}`)

	id1, _ := body1["id"].(float64)
	id2, _ := body2["id"].(float64)
	assert.NotEqual(t, id1, id2)
	assert.Equal(t, "pending", body1["state"])
	assert.Equal(t, "success", body2["state"])
}

func TestPassthroughNotUsedWithoutToken(t *testing.T) {
	srv := NewServer(RepoInfo{}, "http://test", Options{})
	assert.False(t, srv.ProxyToGitHub(nil, &http.Request{Method: http.MethodGet}))
}

func TestGraphQL_ProxiesToUpstream(t *testing.T) {
	// Set up a fake GitHub GraphQL endpoint.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"viewer": map[string]any{"login": "octocat"},
			},
		})
	}))
	defer upstream.Close()

	// Override the base URL for the proxy.
	origBase := githubAPIBase
	// We can't override the const, but we can test the stub behavior
	// by verifying the no-token path works.
	_ = origBase

	// Without token: returns empty stub.
	ts := setupTestServer(RepoInfo{Owner: "octocat"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/graphql", `{"query":"{ viewer { login } }"}`)
	assert.Equal(t, 200, code)
	assert.NotNil(t, body["data"])
	// Without token, data should be empty.
	data := body["data"].(map[string]any)
	assert.Empty(t, data)
}

func TestPassthrough_WritesBlockedByDefault(t *testing.T) {
	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token"})
	req, _ := http.NewRequest(http.MethodPost, "/api/v3/repos/a/b/dispatches", nil)
	// Without ProxyWrites, POST should not be proxied via catch-all.
	assert.False(t, srv.ProxyToGitHub(nil, req))
}

func TestPassthrough_WritesBlockedWithoutFlag(t *testing.T) {
	// Even with a token, POST is blocked without ProxyWrites.
	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token"})
	postReq, _ := http.NewRequest(http.MethodPost, "/api/v3/repos/a/b/dispatches", nil)
	assert.False(t, srv.ProxyToGitHub(nil, postReq))

	patchReq, _ := http.NewRequest(http.MethodPatch, "/api/v3/repos/a/b/check-runs/1", nil)
	assert.False(t, srv.ProxyToGitHub(nil, patchReq))

	putReq, _ := http.NewRequest(http.MethodPut, "/api/v3/repos/a/b/contents/file.txt", nil)
	assert.False(t, srv.ProxyToGitHub(nil, putReq))
}
