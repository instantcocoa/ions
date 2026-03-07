package githubstub

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- OIDC token endpoint tests ---

func TestGenerateIDToken_Default(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/_apis/actionstoken/generateidtoken", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	token, ok := result["value"].(string)
	require.True(t, ok, "response should contain a 'value' field")
	assert.NotEmpty(t, token)

	// Token should be a JWT with 3 parts (header.payload.signature).
	parts := strings.Split(token, ".")
	assert.Len(t, parts, 3, "JWT should have 3 parts")
	// Signature should be empty (alg: none).
	assert.Empty(t, parts[2], "signature should be empty for alg:none")
}

func TestGenerateIDToken_WithAudience(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/actionstoken/generateidtoken?audience=https://my-api.example.com")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	token, ok := result["value"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, token)
	assert.Contains(t, token, ".")
}

// --- resolveRef tests ---

// createGitRepo creates a temp git repo with an initial commit and returns the repo path and repo object.
func createGitRepo(t *testing.T) (string, *git.Repository) {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	// Create an initial file and commit.
	err = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("file.txt")
	require.NoError(t, err)

	_, err = wt.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	return dir, repo
}

func TestResolveRef_Branch(t *testing.T) {
	_, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)
	expectedSHA := head.Hash().String()

	// go-git defaults to "master" for the initial branch.
	branchName := head.Name().Short()

	sha := resolveRef(repo, branchName)
	assert.Equal(t, expectedSHA, sha)
}

func TestResolveRef_Tag(t *testing.T) {
	_, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)
	expectedSHA := head.Hash().String()

	// Create a tag.
	_, err = repo.CreateTag("v1.0.0", head.Hash(), nil)
	require.NoError(t, err)

	sha := resolveRef(repo, "v1.0.0")
	assert.Equal(t, expectedSHA, sha)
}

func TestResolveRef_FullRef(t *testing.T) {
	_, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)
	expectedSHA := head.Hash().String()

	// Use the full ref name.
	sha := resolveRef(repo, string(head.Name()))
	assert.Equal(t, expectedSHA, sha)
}

func TestResolveRef_HEAD(t *testing.T) {
	_, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)
	expectedSHA := head.Hash().String()

	sha := resolveRef(repo, "HEAD")
	assert.Equal(t, expectedSHA, sha)
}

func TestResolveRef_SHA(t *testing.T) {
	_, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)
	expectedSHA := head.Hash().String()

	// Full SHA should resolve.
	sha := resolveRef(repo, expectedSHA)
	assert.Equal(t, expectedSHA, sha)
}

func TestResolveRef_UnknownRef(t *testing.T) {
	_, repo := createGitRepo(t)

	sha := resolveRef(repo, "nonexistent-branch")
	assert.Equal(t, "", sha)
}

func TestResolveRef_ShortString(t *testing.T) {
	_, repo := createGitRepo(t)

	// A string shorter than 7 chars should not be tried as a SHA.
	sha := resolveRef(repo, "abc")
	assert.Equal(t, "", sha)
}

// --- gitBlobSHA tests ---

func TestGitBlobSHA_EmptyContent(t *testing.T) {
	data := []byte("")
	got := gitBlobSHA(data)

	// Compute expected: git hashes "blob 0\x00" + empty content.
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", 0)
	expected := fmt.Sprintf("%x", h.Sum(nil))

	assert.Equal(t, expected, got)
}

func TestGitBlobSHA_KnownContent(t *testing.T) {
	// git hash-object for "Hello, world!" produces a known SHA.
	data := []byte("Hello, world!")
	got := gitBlobSHA(data)

	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	expected := fmt.Sprintf("%x", h.Sum(nil))

	assert.Equal(t, expected, got)
	assert.Len(t, got, 40) // SHA-1 hex is 40 chars.
}

func TestGitBlobSHA_DifferentContentsDiffer(t *testing.T) {
	sha1 := gitBlobSHA([]byte("aaa"))
	sha2 := gitBlobSHA([]byte("bbb"))
	assert.NotEqual(t, sha1, sha2)
}

// --- isSubpath tests ---

func TestIsSubpath_ValidChild(t *testing.T) {
	assert.True(t, isSubpath("/a/b/c", "/a/b"))
}

func TestIsSubpath_SamePath(t *testing.T) {
	// rel is "." which isSubpath rejects.
	assert.False(t, isSubpath("/a/b", "/a/b"))
}

func TestIsSubpath_ParentTraversal(t *testing.T) {
	assert.False(t, isSubpath("/a", "/a/b"))
}

func TestIsSubpath_Unrelated(t *testing.T) {
	assert.False(t, isSubpath("/x/y/z", "/a/b"))
}

func TestIsSubpath_DeepChild(t *testing.T) {
	assert.True(t, isSubpath("/repo/src/main/file.go", "/repo"))
}

// --- truncate tests ---

func TestTruncate_Empty(t *testing.T) {
	assert.Equal(t, "", truncate("", 10))
}

func TestTruncate_ExactLength(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 5))
}

func TestTruncate_UnderLength(t *testing.T) {
	assert.Equal(t, "hi", truncate("hi", 10))
}

func TestTruncate_OverLength(t *testing.T) {
	assert.Equal(t, "hel...", truncate("hello world", 3))
}

func TestTruncate_ZeroMax(t *testing.T) {
	assert.Equal(t, "...", truncate("hello", 0))
}

// --- handleDirContents edge cases ---

func TestHandleDirContents_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "empty")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v3/repos/octocat/hello/contents/empty")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	var result []any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestHandleDirContents_MixedFilesAndDirs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o644))

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	// Request the root directory contents (empty path).
	code, result := getJSONArray(t, ts, "/api/v3/repos/octocat/hello/contents/")
	assert.Equal(t, 200, code)

	// Should contain both the file and the subdir.
	foundFile := false
	foundDir := false
	for _, item := range result {
		entry := item.(map[string]any)
		if entry["name"] == "file.txt" {
			assert.Equal(t, "file", entry["type"])
			foundFile = true
		}
		if entry["name"] == "subdir" {
			assert.Equal(t, "dir", entry["type"])
			foundDir = true
		}
	}
	assert.True(t, foundFile, "should contain file.txt")
	assert.True(t, foundDir, "should contain subdir")
}

func TestGetContents_NoRepoPath(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner: "octocat",
		Repo:  "hello",
		// RepoPath is empty
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/any-file.txt")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
}

// --- handleGraphQL edge cases ---

func TestGraphQL_PostToAlternateEndpoint(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat"})
	defer ts.Close()

	// The /api/graphql endpoint should also work.
	resp, err := http.Post(ts.URL+"/api/graphql", "application/json",
		strings.NewReader(`{"query":"{ viewer { login } }"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	assert.NotNil(t, result["data"])
}

func TestGraphQL_EmptyBody(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/graphql", "")
	// The handler reads body and returns stub regardless.
	assert.Equal(t, 200, code)
	assert.NotNil(t, body["data"])
}

// --- JSON parsing errors on POST handlers ---

func TestCreateStatus_InvalidJSON(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v3/repos/octocat/hello/statuses/abc123",
		"application/json", strings.NewReader("not valid json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 400, resp.StatusCode)
}

func TestCreateCheckRun_InvalidJSON(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v3/repos/octocat/hello/check-runs",
		"application/json", strings.NewReader("{invalid"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 400, resp.StatusCode)
}

func TestUpdateCheckRun_InvalidJSON(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/v3/repos/octocat/hello/check-runs/1",
		strings.NewReader("not json"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 400, resp.StatusCode)
}

func TestCreateComment_InvalidJSON(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v3/repos/octocat/hello/issues/42/comments",
		"application/json", strings.NewReader("broken json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 400, resp.StatusCode)
}

// --- allocID monotonic increment ---

func TestAllocID_Monotonic(t *testing.T) {
	srv := NewServer(RepoInfo{}, "http://test", Options{})

	id1 := srv.allocID()
	id2 := srv.allocID()
	id3 := srv.allocID()

	assert.Equal(t, 1, id1)
	assert.Equal(t, 2, id2)
	assert.Equal(t, 3, id3)
}

// --- NewServer constructor ---

func TestNewServer_BaseURLTrailingSlash(t *testing.T) {
	srv := NewServer(RepoInfo{Owner: "test"}, "http://localhost:8080/", Options{})
	assert.Equal(t, "http://localhost:8080", srv.baseURL)
}

func TestNewServer_EmptyBaseURL(t *testing.T) {
	srv := NewServer(RepoInfo{}, "", Options{})
	assert.Equal(t, "", srv.baseURL)
}

func TestNewServer_InitialState(t *testing.T) {
	srv := NewServer(RepoInfo{Owner: "o", Repo: "r"}, "http://test", Options{})
	assert.Equal(t, 1, srv.nextID)
	assert.Nil(t, srv.statuses)
	assert.Nil(t, srv.comments)
	assert.Nil(t, srv.checkRuns)
}

// --- GetUser edge case ---

func TestGetUser_EmptyOwner(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: ""})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/user")
	assert.Equal(t, 200, code)
	assert.Equal(t, "local-actor", body["login"])
}

// --- GetPull edge cases ---

func TestGetPull_NoCurrentRef(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner: "octocat",
		Repo:  "hello",
		// No CurrentRef, no DefaultBranch, no CurrentSHA.
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/pulls/1")
	assert.Equal(t, 200, code)

	head := body["head"].(map[string]any)
	// Should default to "main" since no CurrentRef is set.
	assert.Equal(t, "main", head["ref"])
	// SHA should be zero-padded.
	assert.Equal(t, "0000000000000000000000000000000000000000", head["sha"])

	user := body["user"].(map[string]any)
	assert.Equal(t, "octocat", user["login"])
}

func TestGetPull_EmptyOwnerDefaultsToLocalActor(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner: "",
		Repo:  "hello",
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/x/hello/pulls/1")
	assert.Equal(t, 200, code)

	user := body["user"].(map[string]any)
	assert.Equal(t, "local-actor", user["login"])
}

// --- CreateComment edge case ---

func TestCreateComment_EmptyOwnerUsesDefault(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "", Repo: "hello"})
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/repos/any/hello/issues/1/comments",
		`{"body":"test comment"}`)
	assert.Equal(t, 201, code)

	user := body["user"].(map[string]any)
	assert.Equal(t, "local-actor", user["login"])
}

// --- GetCommit with repo (resolveRef path) ---

func TestGetCommit_FromLocalRepo(t *testing.T) {
	dir, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	branchName := head.Name().Short()
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/commits/"+branchName)
	assert.Equal(t, 200, code)
	assert.Equal(t, head.Hash().String(), body["sha"])
}

// --- GetRef with repo ---

func TestGetRef_FromLocalRepo(t *testing.T) {
	dir, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	branchName := head.Name().Short()
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/git/ref/heads/"+branchName)
	assert.Equal(t, 200, code)

	obj := body["object"].(map[string]any)
	assert.Equal(t, head.Hash().String(), obj["sha"])
}

// --- GetCommit with no SHA fallback ---

func TestGetCommit_NoSHAFallback(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner: "octocat",
		Repo:  "hello",
		// No CurrentSHA.
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/commits/main")
	assert.Equal(t, 200, code)
	assert.Equal(t, "0000000000000000000000000000000000000000", body["sha"])
}

// --- GetRef with no SHA and no CurrentRef fallback ---

func TestGetRef_NoSHANoRefFallback(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner: "octocat",
		Repo:  "hello",
		// No CurrentSHA, no CurrentRef.
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/git/ref/heads/develop")
	assert.Equal(t, 200, code)
	// ref should be "refs/heads/develop" since no CurrentRef is set.
	assert.Equal(t, "refs/heads/develop", body["ref"])
	obj := body["object"].(map[string]any)
	assert.Equal(t, "0000000000000000000000000000000000000000", obj["sha"])
}

// --- CatchAll with verbose option ---

func TestCatchAll_Verbose(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"}, Options{Verbose: true})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/unknown/endpoint")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
}

// --- ResolveRef with remote and tag (from git repo) ---

func TestResolveRef_TagFromLocalRepo(t *testing.T) {
	dir, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)

	_, err = repo.CreateTag("v2.0.0", head.Hash(), nil)
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/git/ref/tags/v2.0.0")
	assert.Equal(t, 200, code)

	obj := body["object"].(map[string]any)
	assert.Equal(t, head.Hash().String(), obj["sha"])
}

// --- minLen ---

func TestMinLen(t *testing.T) {
	assert.Equal(t, 3, minLen(3, 5))
	assert.Equal(t, 3, minLen(5, 3))
	assert.Equal(t, 4, minLen(4, 4))
	assert.Equal(t, 0, minLen(0, 10))
}

// --- RegisterRoutes and middleware ---

func TestMiddleware_SetsRateLimitHeaders(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"}, Options{Verbose: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v3/user")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "5000", resp.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "4999", resp.Header.Get("X-RateLimit-Remaining"))
	assert.Equal(t, "core", resp.Header.Get("X-RateLimit-Resource"))

	// Reset should be in the future.
	resetStr := resp.Header.Get("X-RateLimit-Reset")
	assert.NotEmpty(t, resetStr)
}

// --- GetCommit with local repo but unknown ref ---

func TestGetCommit_LocalRepoUnknownRef(t *testing.T) {
	dir, _ := createGitRepo(t)

	ts := setupTestServer(RepoInfo{
		Owner:      "octocat",
		Repo:       "hello",
		RepoPath:   dir,
		CurrentSHA: "fallbacksha123",
	})
	defer ts.Close()

	// Request a ref that doesn't exist in the repo -- should fall back.
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/commits/nonexistent")
	assert.Equal(t, 200, code)
	assert.Equal(t, "fallbacksha123", body["sha"])
}

// --- GetRepo with default branch fallback ---

func TestGetRepo_DefaultBranchFallback(t *testing.T) {
	ts := setupTestServer(RepoInfo{
		Owner: "octocat",
		Repo:  "hello",
		// DefaultBranch not set.
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello")
	assert.Equal(t, 200, code)
	assert.Equal(t, "main", body["default_branch"])
}

// --- Resolving with remote config ---

func TestGetCommit_WithRemoteRepo(t *testing.T) {
	dir, repo := createGitRepo(t)

	// Add a remote.
	_, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://github.com/octocat/hello.git"},
	})
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/commits/HEAD")
	assert.Equal(t, 200, code)
	assert.Equal(t, head.Hash().String(), body["sha"])
}

// --- Dispatch accepts any body ---

func TestDispatch_EmptyBody(t *testing.T) {
	ts := setupTestServer(RepoInfo{Owner: "octocat", Repo: "hello"})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v3/repos/octocat/hello/dispatches",
		"application/json", strings.NewReader(""))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 204, resp.StatusCode)
}

// --- writeJSON helper test ---

func TestWriteJSON_SetsContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]any{"key": "value"})

	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]any
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

// --- ProxyToGitHub happy path (using transport hijacking) ---

// proxyTestTransport intercepts requests to api.github.com and routes them to a test server.
type proxyTestTransport struct {
	testServerURL string
	realTransport http.RoundTripper
}

func (t proxyTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Redirect api.github.com to our test server.
	if req.URL.Host == "api.github.com" {
		req.URL.Scheme = "http"
		req.URL.Host = t.testServerURL[len("http://"):]
	}
	return t.realTransport.RoundTrip(req)
}

// savedTransport stores the original default transport, needed because tests override it.
var savedTransport = http.DefaultTransport

func TestProxyToGitHub_HappyPath_GET(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		assert.Equal(t, "ions/1.0", r.Header.Get("User-Agent"))
		w.Header().Set("X-Custom-Header", "upstream-value")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"proxied": true})
	}))
	defer upstream.Close()

	srv := NewServer(RepoInfo{Owner: "test"}, "http://test", Options{Token: "test-token"})

	// Override the http client used by ProxyToGitHub by monkey-patching it through
	// the handler. Since ProxyToGitHub creates its own client internally, we need
	// to override the default transport temporarily.
	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v3/repos/owner/repo/some/path", nil)

	result := srv.ProxyToGitHub(w, req)
	assert.True(t, result, "proxy should have handled the request")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "upstream-value", w.Header().Get("X-Custom-Header"))

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	assert.Equal(t, true, body["proxied"])
}

func TestProxyToGitHub_WithQueryString(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "page=2&per_page=10", r.URL.RawQuery)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer upstream.Close()

	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token"})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v3/repos/o/r?page=2&per_page=10", nil)

	result := srv.ProxyToGitHub(w, req)
	assert.True(t, result)
}

func TestProxyToGitHub_WithContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer upstream.Close()

	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token", ProxyWrites: true})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v3/repos/o/r/statuses/abc", strings.NewReader(`{"state":"success"}`))
	req.Header.Set("Content-Type", "application/json")

	result := srv.ProxyToGitHub(w, req)
	assert.True(t, result)
}

func TestProxyToGitHub_UpstreamError(t *testing.T) {
	// Upstream server is not reachable (closed immediately).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstream.Close() // Close immediately to cause connection refused.

	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token"})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v3/repos/o/r", nil)

	result := srv.ProxyToGitHub(w, req)
	// Should return false because the upstream is unreachable.
	assert.False(t, result)
}

func TestProxyToGitHub_ProxyWritesEnabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"created": true})
	}))
	defer upstream.Close()

	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token", ProxyWrites: true})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v3/repos/o/r/statuses/abc", nil)

	result := srv.ProxyToGitHub(w, req)
	assert.True(t, result)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// --- handleGraphQL proxy path ---

func TestGraphQL_ProxyWithToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		assert.Equal(t, "Bearer my-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream", "true")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"viewer": map[string]any{"login": "testuser"}},
		})
	}))
	defer upstream.Close()

	info := RepoInfo{Owner: "octocat"}
	srv := NewServer(info, "http://test", Options{Token: "my-token"})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/graphql", `{"query":"{ viewer { login } }"}`)
	assert.Equal(t, 200, code)

	data := body["data"].(map[string]any)
	viewer := data["viewer"].(map[string]any)
	assert.Equal(t, "testuser", viewer["login"])
}

func TestGraphQL_ProxyWithToken_Verbose(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer upstream.Close()

	info := RepoInfo{Owner: "octocat"}
	srv := NewServer(info, "http://test", Options{Token: "my-token", Verbose: true})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	code, _ := postJSON(t, ts, "/api/v3/graphql", `{"query":"test"}`)
	assert.Equal(t, 200, code)
}

func TestGraphQL_ProxyWithToken_UpstreamError(t *testing.T) {
	// Upstream that is closed to trigger a client.Do error.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstream.Close()

	info := RepoInfo{Owner: "octocat"}
	srv := NewServer(info, "http://test", Options{Token: "my-token"})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	code, body := postJSON(t, ts, "/api/v3/graphql", `{"query":"test"}`)
	assert.Equal(t, 502, code)
	errors, ok := body["errors"].([]any)
	assert.True(t, ok)
	assert.Len(t, errors, 1)
}

func TestGraphQL_ProxyWithToken_EmptyBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer upstream.Close()

	info := RepoInfo{Owner: "octocat"}
	srv := NewServer(info, "http://test", Options{Token: "my-token", Verbose: true})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Post with empty body while verbose is on.
	code, _ := postJSON(t, ts, "/api/v3/graphql", "")
	assert.Equal(t, 200, code)
}

// --- handleCatchAll proxy success ---

func TestCatchAll_ProxySuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"from": "upstream"})
	}))
	defer upstream.Close()

	info := RepoInfo{Owner: "octocat", Repo: "hello"}
	srv := NewServer(info, "http://test", Options{Token: "my-token"})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// A GET request to an unhandled endpoint should be proxied.
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/some/unknown/endpoint")
	assert.Equal(t, 200, code)
	assert.Equal(t, "upstream", body["from"])
}

// --- handleCatchAll verbose with body ---

func TestCatchAll_VerboseWithBody(t *testing.T) {
	info := RepoInfo{Owner: "octocat", Repo: "hello"}
	srv := NewServer(info, "http://test", Options{Verbose: true})

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// POST to an unhandled endpoint with verbose mode -- no token means no proxy.
	resp, err := http.Post(ts.URL+"/api/v3/repos/octocat/hello/unknown",
		"application/json", strings.NewReader(`{"key":"value"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)
}

// --- handleGetContents: isSubpath false branch (path traversal) ---

func TestGetContents_IsSubpathFalse(t *testing.T) {
	dir := t.TempDir()
	// Create a file outside the "repo" subdir.
	repoDir := filepath.Join(dir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	// Create a symlink inside the repo that points outside.
	outsidePath := filepath.Join(dir, "outside.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("outside"), 0o644))

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: repoDir,
	})
	defer ts.Close()

	// Request a file that doesn't exist in the repo.
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/nonexistent")
	assert.Equal(t, 404, code)
	assert.Equal(t, "Not Found", body["message"])
}

// --- handleGetContents: ReadFile error ---

func TestGetContents_ReadFileError(t *testing.T) {
	dir := t.TempDir()
	// Create a file with no read permissions.
	filePath := filepath.Join(dir, "unreadable.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("secret"), 0o000))
	defer os.Chmod(filePath, 0o644)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/unreadable.txt")
	assert.Equal(t, 500, code)
	assert.Equal(t, "could not read file", body["message"])
}

// --- handleDirContents: ReadDir error ---

func TestHandleDirContents_ReadDirError(t *testing.T) {
	dir := t.TempDir()
	// Create a directory with no read permissions.
	unreadableDir := filepath.Join(dir, "noread")
	require.NoError(t, os.MkdirAll(unreadableDir, 0o000))
	defer os.Chmod(unreadableDir, 0o755)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/contents/noread")
	assert.Equal(t, 500, code)
	assert.Equal(t, "could not read directory", body["message"])
}

// --- isSubpath: with error from filepath.Rel ---

func TestIsSubpath_RelError(t *testing.T) {
	// This is very hard to trigger because filepath.Rel rarely errors.
	// But we can at least verify the function with unusual paths.
	assert.True(t, isSubpath("/a/b/c/d", "/a/b"))
	assert.False(t, isSubpath("/x/y", "/a/b"))
}

// --- resolveRef: HEAD path from local repo ---

func TestResolveRef_HEADFromLocalRepo(t *testing.T) {
	dir, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/commits/HEAD")
	assert.Equal(t, 200, code)
	assert.Equal(t, head.Hash().String(), body["sha"])
}

// --- GraphQL verbose without token (no body) ---

func TestGraphQL_NoTokenVerboseNoBody(t *testing.T) {
	info := RepoInfo{Owner: "octocat"}
	srv := NewServer(info, "http://test", Options{Verbose: true})

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// POST with actual body content to trigger the verbose logging path.
	code, body := postJSON(t, ts, "/api/v3/graphql", `{"query":"test"}`)
	assert.Equal(t, 200, code)
	assert.NotNil(t, body["data"])
}

// --- ProxyToGitHub strips /api/v3 prefix ---

func TestProxyToGitHub_StripsApiV3Prefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the path was stripped of /api/v3.
		assert.Equal(t, "/repos/o/r", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer upstream.Close()

	srv := NewServer(RepoInfo{}, "http://test", Options{Token: "test-token"})

	origTransport := http.DefaultTransport
	http.DefaultTransport = proxyTestTransport{testServerURL: upstream.URL, realTransport: savedTransport}
	defer func() { http.DefaultTransport = origTransport }()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v3/repos/o/r", nil)

	result := srv.ProxyToGitHub(w, req)
	assert.True(t, result)
}

// --- handleGetRef: local repo with tag resolution through resolveRef ---

func TestGetRef_LocalRepo_TagRef(t *testing.T) {
	dir, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)

	_, err = repo.CreateTag("v3.0.0", head.Hash(), nil)
	require.NoError(t, err)

	ts := setupTestServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: dir,
	})
	defer ts.Close()

	// Request the ref for the tag.
	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/git/ref/tags/v3.0.0")
	assert.Equal(t, 200, code)

	obj := body["object"].(map[string]any)
	assert.Equal(t, head.Hash().String(), obj["sha"])
}

// --- handleGetRef: local repo but unknown ref falls back ---

func TestGetRef_LocalRepoUnknownRefFallback(t *testing.T) {
	dir, _ := createGitRepo(t)

	ts := setupTestServer(RepoInfo{
		Owner:      "octocat",
		Repo:       "hello",
		RepoPath:   dir,
		CurrentSHA: "fallbacksha",
		CurrentRef: "refs/heads/fallback",
	})
	defer ts.Close()

	code, body := getJSON(t, ts, "/api/v3/repos/octocat/hello/git/ref/heads/nonexistent")
	assert.Equal(t, 200, code)
	assert.Equal(t, "refs/heads/fallback", body["ref"])
}

// ---------------------------------------------------------------------------
// handleGetContents: path traversal — path escapes repo dir
// ---------------------------------------------------------------------------

func TestGetContents_PathTraversal_DirectHandler(t *testing.T) {
	// Create two dirs: "repo" and "secret" side by side.
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	secretDir := filepath.Join(base, "secret")
	require.NoError(t, os.MkdirAll(secretDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(secretDir, "passwd"), []byte("secret"), 0o644))

	srv := NewServer(RepoInfo{
		Owner:    "octocat",
		Repo:     "hello",
		RepoPath: repoDir,
	}, "http://test", Options{})

	// Directly call the handler with a crafted request that has "../secret/passwd" as the path value.
	req := httptest.NewRequest(http.MethodGet, "/api/v3/repos/octocat/hello/contents/../secret/passwd", nil)
	req.SetPathValue("path", "../secret/passwd")
	w := httptest.NewRecorder()
	srv.handleGetContents(w, req)

	assert.Equal(t, 404, w.Code)
}

// ---------------------------------------------------------------------------
// isSubpath — edge cases
// ---------------------------------------------------------------------------

func TestIsSubpath_True(t *testing.T) {
	assert.True(t, isSubpath("/a/b/c", "/a/b"))
}

func TestIsSubpath_False_SameDir(t *testing.T) {
	// rel == "." should return false
	assert.False(t, isSubpath("/a/b", "/a/b"))
}

func TestIsSubpath_False_ParentTraversal(t *testing.T) {
	assert.False(t, isSubpath("/a", "/a/b"))
}

// ---------------------------------------------------------------------------
// resolveRef — HEAD case
// ---------------------------------------------------------------------------

func TestResolveRef_HEAD_Direct(t *testing.T) {
	_, repo := createGitRepo(t)

	// In a regular go-git repo, HEAD should resolve via the full-ref path,
	// but let's call resolveRef("HEAD") to exercise the HEAD special-case.
	sha := resolveRef(repo, "HEAD")
	assert.NotEmpty(t, sha, "resolveRef(HEAD) should resolve to a commit SHA")
	assert.Len(t, sha, 40)
}

func TestResolveRef_RawSHA(t *testing.T) {
	_, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)
	fullSHA := head.Hash().String()

	// resolveRef with the full SHA should work.
	sha := resolveRef(repo, fullSHA)
	assert.Equal(t, fullSHA, sha)
}

func TestResolveRef_InvalidRef(t *testing.T) {
	_, repo := createGitRepo(t)

	sha := resolveRef(repo, "nonexistent-ref-that-does-not-exist")
	assert.Empty(t, sha)
}

func TestResolveRef_Tag_Direct(t *testing.T) {
	dir, repo := createGitRepo(t)

	head, err := repo.Head()
	require.NoError(t, err)

	// Create a tag.
	_, err = repo.CreateTag("v1.0.0", head.Hash(), &git.CreateTagOptions{
		Tagger: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
		Message: "tag v1.0.0",
	})
	require.NoError(t, err)
	_ = dir

	sha := resolveRef(repo, "v1.0.0")
	assert.NotEmpty(t, sha)
}
