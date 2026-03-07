package reusable

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emaland/ions/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReference_Local(t *testing.T) {
	ref, err := ParseReference("./.github/workflows/deploy.yml")
	require.NoError(t, err)
	assert.True(t, ref.Local)
	assert.Equal(t, ".github/workflows/deploy.yml", ref.Path)
	assert.Empty(t, ref.Owner)
	assert.Empty(t, ref.Repo)
	assert.Empty(t, ref.Ref)
}

func TestParseReference_Remote(t *testing.T) {
	ref, err := ParseReference("octo-org/example-repo/.github/workflows/reusable.yml@main")
	require.NoError(t, err)
	assert.False(t, ref.Local)
	assert.Equal(t, "octo-org", ref.Owner)
	assert.Equal(t, "example-repo", ref.Repo)
	assert.Equal(t, ".github/workflows/reusable.yml", ref.Path)
	assert.Equal(t, "main", ref.Ref)
}

func TestParseReference_RemoteSHA(t *testing.T) {
	ref, err := ParseReference("owner/repo/.github/workflows/ci.yml@abc123def456")
	require.NoError(t, err)
	assert.Equal(t, "owner", ref.Owner)
	assert.Equal(t, "repo", ref.Repo)
	assert.Equal(t, ".github/workflows/ci.yml", ref.Path)
	assert.Equal(t, "abc123def456", ref.Ref)
}

func TestParseReference_MissingRef(t *testing.T) {
	_, err := ParseReference("owner/repo/.github/workflows/ci.yml")
	assert.ErrorContains(t, err, "missing @ref")
}

func TestParseReference_InvalidFormat(t *testing.T) {
	_, err := ParseReference("invalid@ref")
	assert.ErrorContains(t, err, "expected owner/repo/path@ref")
}

func TestResolveLocal(t *testing.T) {
	dir := t.TempDir()

	wfDir := filepath.Join(dir, ".github", "workflows")
	require.NoError(t, os.MkdirAll(wfDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wfDir, "deploy.yml"), []byte(`
name: Deploy
on:
  workflow_call:
    inputs:
      environment:
        description: Target environment
        required: true
        type: string
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - run: echo "Deploying to ${{ inputs.environment }}"
`), 0o644))

	resolver := NewResolver(ResolverOptions{RepoPath: dir})
	ref := Reference{Local: true, Path: ".github/workflows/deploy.yml"}

	w, err := resolver.Resolve(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "Deploy", w.Name)
	require.Contains(t, w.Jobs, "deploy")
	require.Len(t, w.Jobs["deploy"].Steps, 1)

	// Verify workflow_call inputs were parsed.
	inputs := w.On.WorkflowCallInputs()
	require.Contains(t, inputs, "environment")
	assert.True(t, inputs["environment"].Required)
}

func TestResolveLocal_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	resolver := NewResolver(ResolverOptions{RepoPath: dir})
	ref := Reference{Local: true, Path: "../../etc/passwd"}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.ErrorContains(t, err, "escapes repository root")
}

func TestResolveLocal_NotFound(t *testing.T) {
	dir := t.TempDir()
	resolver := NewResolver(ResolverOptions{RepoPath: dir})
	ref := Reference{Local: true, Path: ".github/workflows/nonexistent.yml"}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
}

func TestResolveRemote(t *testing.T) {
	workflowContent := `
name: Remote Workflow
on:
  workflow_call:
    inputs:
      version:
        type: string
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "Building version ${{ inputs.version }}"
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/owner/repo/main/.github/workflows/build.yml", r.URL.Path)
		w.Write([]byte(workflowContent))
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/build.yml",
		Ref:   "main",
	}

	w, err := resolver.Resolve(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "Remote Workflow", w.Name)
	require.Contains(t, w.Jobs, "build")
}

func TestResolveRemote_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/missing.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.ErrorContains(t, err, "HTTP 404")
}

func TestMapInputs(t *testing.T) {
	callerWith := map[string]interface{}{
		"environment": "production",
		"version":     "1.2.3",
	}

	triggers := workflow.Triggers{
		Events: map[string]*workflow.EventConfig{
			"workflow_call": {
				Inputs: map[string]workflow.DispatchInput{
					"environment": {Default: "staging"},
					"version":     {},
					"debug":       {Default: "false"},
				},
			},
		},
	}

	inputs := MapInputs(callerWith, triggers)
	assert.Equal(t, "production", inputs["environment"]) // caller overrides default
	assert.Equal(t, "1.2.3", inputs["version"])
	assert.Equal(t, "false", inputs["debug"]) // default applied
}

func TestMapInputs_NoCallerWith(t *testing.T) {
	triggers := workflow.Triggers{
		Events: map[string]*workflow.EventConfig{
			"workflow_call": {
				Inputs: map[string]workflow.DispatchInput{
					"environment": {Default: "staging"},
				},
			},
		},
	}

	inputs := MapInputs(nil, triggers)
	assert.Equal(t, "staging", inputs["environment"])
}

func TestMapInputs_NoWorkflowCall(t *testing.T) {
	triggers := workflow.Triggers{
		Events: map[string]*workflow.EventConfig{
			"push": nil,
		},
	}

	callerWith := map[string]interface{}{"key": "value"}
	inputs := MapInputs(callerWith, triggers)
	assert.Equal(t, "value", inputs["key"])
}

func TestMapSecrets_Inherit(t *testing.T) {
	callerSecrets := map[string]string{
		"TOKEN":  "abc",
		"SECRET": "xyz",
	}

	result := MapSecrets(callerSecrets, "inherit")
	assert.Equal(t, callerSecrets, result)
}

func TestMapSecrets_Explicit(t *testing.T) {
	callerSecrets := map[string]string{
		"DEPLOY_KEY": "secret-123",
		"OTHER":      "other-val",
	}

	jobSecrets := map[string]interface{}{
		"DEPLOY_KEY": "${{ secrets.DEPLOY_KEY }}",
	}

	result := MapSecrets(callerSecrets, jobSecrets)
	assert.Equal(t, "secret-123", result["DEPLOY_KEY"])
	assert.NotContains(t, result, "OTHER")
}

func TestMapSecrets_Nil(t *testing.T) {
	result := MapSecrets(map[string]string{"KEY": "val"}, nil)
	assert.Empty(t, result)
}

// rewriteTransport redirects all requests to a test server.
type rewriteTransport struct {
	baseURL string
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.baseURL[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}

// --- Additional coverage tests ---

// Test resolveRemote with GitHub token (covers the Authorization header path).
func TestResolveRemote_WithToken(t *testing.T) {
	workflowContent := `
name: Token Workflow
on:
  workflow_call:
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "hello"
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the Authorization header is set.
		assert.Equal(t, "Bearer test-token-123", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github.v3.raw", r.Header.Get("Accept"))
		assert.Equal(t, "ions/1.0", r.Header.Get("User-Agent"))
		w.Write([]byte(workflowContent))
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{GitHubToken: "test-token-123"})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/build.yml",
		Ref:   "main",
	}

	w, err := resolver.Resolve(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "Token Workflow", w.Name)
}

// Test resolveRemote connection error.
func TestResolveRemote_ConnectionError(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: "http://localhost:1"}, // invalid port
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/build.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download reusable workflow")
}

// Test resolveRemote with invalid workflow YAML content.
func TestResolveRemote_InvalidYAML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return something that is not valid workflow YAML.
		w.Write([]byte(`{{{invalid yaml that is totally broken`))
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/bad.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
}

// Test MapSecrets with an explicit map containing a non-matching expression.
func TestMapSecrets_ExplicitNonMatchingExpression(t *testing.T) {
	callerSecrets := map[string]string{
		"KEY": "value",
	}

	jobSecrets := map[string]interface{}{
		"MY_SECRET": "${{ env.SOMETHING }}", // not a secrets.X expression
	}

	result := MapSecrets(callerSecrets, jobSecrets)
	assert.Equal(t, "${{ env.SOMETHING }}", result["MY_SECRET"])
}

// Test MapSecrets with an explicit map where the secret key doesn't exist in caller.
func TestMapSecrets_ExplicitMissingSecret(t *testing.T) {
	callerSecrets := map[string]string{
		"OTHER": "value",
	}

	jobSecrets := map[string]interface{}{
		"MY_SECRET": "${{ secrets.NONEXISTENT }}", // references a secret not in callerSecrets
	}

	result := MapSecrets(callerSecrets, jobSecrets)
	// Should fall through to the raw value since the secret doesn't exist.
	assert.Equal(t, "${{ secrets.NONEXISTENT }}", result["MY_SECRET"])
}

// Test MapSecrets with a literal (non-expression) value in the map.
func TestMapSecrets_ExplicitLiteralValue(t *testing.T) {
	callerSecrets := map[string]string{
		"KEY": "value",
	}

	jobSecrets := map[string]interface{}{
		"MY_SECRET": "literal-value",
	}

	result := MapSecrets(callerSecrets, jobSecrets)
	assert.Equal(t, "literal-value", result["MY_SECRET"])
}

// Test MapSecrets with an unsupported type (not string, not map).
func TestMapSecrets_UnsupportedType(t *testing.T) {
	callerSecrets := map[string]string{"KEY": "value"}

	// Pass an integer — not "inherit" and not a map.
	result := MapSecrets(callerSecrets, 42)
	assert.Empty(t, result)
}

// Test MapInputs with extra caller values not in input defs.
func TestMapInputs_ExtraCallerValues(t *testing.T) {
	callerWith := map[string]interface{}{
		"defined":   "value1",
		"undefined": "value2", // not in input defs
	}

	triggers := workflow.Triggers{
		Events: map[string]*workflow.EventConfig{
			"workflow_call": {
				Inputs: map[string]workflow.DispatchInput{
					"defined": {},
				},
			},
		},
	}

	inputs := MapInputs(callerWith, triggers)
	assert.Equal(t, "value1", inputs["defined"])
	assert.Equal(t, "value2", inputs["undefined"])
}

// Test MapInputs where caller overrides a default and another input uses its default.
func TestMapInputs_MixedDefaultsAndOverrides(t *testing.T) {
	callerWith := map[string]interface{}{
		"env": "prod",
	}

	triggers := workflow.Triggers{
		Events: map[string]*workflow.EventConfig{
			"workflow_call": {
				Inputs: map[string]workflow.DispatchInput{
					"env":     {Default: "staging"},
					"verbose": {Default: "false"},
					"extra":   {}, // no default, no caller value
				},
			},
		},
	}

	inputs := MapInputs(callerWith, triggers)
	assert.Equal(t, "prod", inputs["env"])      // caller override
	assert.Equal(t, "false", inputs["verbose"])  // default
	assert.NotContains(t, inputs, "extra")       // no value or default
}

// Test resolveLocal with a workflow that doesn't exist (file not found).
func TestResolveLocal_InvalidWorkflow(t *testing.T) {
	dir := t.TempDir()

	wfDir := filepath.Join(dir, ".github", "workflows")
	require.NoError(t, os.MkdirAll(wfDir, 0o755))
	// Write an invalid YAML file.
	require.NoError(t, os.WriteFile(filepath.Join(wfDir, "bad.yml"), []byte(`{{{invalid`), 0o644))

	resolver := NewResolver(ResolverOptions{RepoPath: dir})
	ref := Reference{Local: true, Path: ".github/workflows/bad.yml"}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
}

// Test ParseReference with a tag that contains @.
func TestParseReference_TagWithAt(t *testing.T) {
	ref, err := ParseReference("actions/checkout/.github/workflows/ci.yml@v4.0.0")
	require.NoError(t, err)
	assert.Equal(t, "actions", ref.Owner)
	assert.Equal(t, "checkout", ref.Repo)
	assert.Equal(t, ".github/workflows/ci.yml", ref.Path)
	assert.Equal(t, "v4.0.0", ref.Ref)
}

// Test MapSecrets with expression that has secrets. prefix but expression is malformed.
func TestMapSecrets_ExplicitPartialExpression(t *testing.T) {
	callerSecrets := map[string]string{
		"KEY": "value",
	}

	jobSecrets := map[string]interface{}{
		// This starts with ${{ and ends with }} but references an env, not secrets.
		"MY_SECRET": "${{ secrets.KEY }}",
	}

	result := MapSecrets(callerSecrets, jobSecrets)
	// Should resolve to the caller secret value.
	assert.Equal(t, "value", result["MY_SECRET"])
}

// Test Resolver options.
func TestNewResolver_DefaultOptions(t *testing.T) {
	resolver := NewResolver(ResolverOptions{
		RepoPath:    "/tmp/repo",
		GitHubToken: "token",
		APIBaseURL:  "http://localhost:8080",
	})
	assert.NotNil(t, resolver)
	assert.Equal(t, "/tmp/repo", resolver.opts.RepoPath)
	assert.Equal(t, "token", resolver.opts.GitHubToken)
	assert.Equal(t, "http://localhost:8080", resolver.opts.APIBaseURL)
}

// Test resolveRemote with a response body that causes io.Copy to succeed
// but produces invalid YAML (covers the ParseFile error after download).
func TestResolveRemote_ParseFileError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return content that writes successfully but isn't valid workflow YAML.
		w.Write([]byte("- - - not: [valid: workflow"))
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/bad.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
}

// Test resolveRemote with context timeout to trigger httpClient.Do error.
func TestResolveRemote_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/build.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(ctx, ref)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download reusable workflow")
}

// Test MapInputs with non-string caller values (e.g., int, bool).
func TestMapInputs_NonStringValues(t *testing.T) {
	callerWith := map[string]interface{}{
		"count":   42,
		"enabled": true,
	}

	triggers := workflow.Triggers{
		Events: map[string]*workflow.EventConfig{
			"workflow_call": {
				Inputs: map[string]workflow.DispatchInput{
					"count":   {},
					"enabled": {},
				},
			},
		},
	}

	inputs := MapInputs(callerWith, triggers)
	assert.Equal(t, "42", inputs["count"])
	assert.Equal(t, "true", inputs["enabled"])
}

// TestResolveRemote_CreateTempError covers resolve.go:149-151 where os.CreateTemp fails.
func TestResolveRemote_CreateTempError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`name: Test
on: workflow_call
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`))
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	// Override TMPDIR to a non-existent directory so os.CreateTemp fails.
	origTmpDir := os.Getenv("TMPDIR")
	os.Setenv("TMPDIR", "/nonexistent/path/that/does/not/exist")
	defer func() {
		if origTmpDir == "" {
			os.Unsetenv("TMPDIR")
		} else {
			os.Setenv("TMPDIR", origTmpDir)
		}
	}()

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/build.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create temp file")
}

// TestResolveRemote_CopyError covers resolve.go:155-157 where io.Copy fails
// during download of the workflow file.
func TestResolveRemote_CopyError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set a Content-Length that is much larger than what we actually send,
		// then close the connection. This causes io.Copy to get an unexpected EOF.
		w.Header().Set("Content-Length", "100000")
		w.Write([]byte("partial"))
		// The handler returns, closing the connection before Content-Length is met.
		// This causes the client's io.Copy to fail with an unexpected EOF.
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer ts.Close()

	resolver := NewResolver(ResolverOptions{})
	resolver.httpClient = &http.Client{
		Transport: rewriteTransport{baseURL: ts.URL},
	}

	ref := Reference{
		Owner: "owner",
		Repo:  "repo",
		Path:  ".github/workflows/build.yml",
		Ref:   "main",
	}

	_, err := resolver.Resolve(context.Background(), ref)
	assert.Error(t, err)
	// Should fail with either "write temp file" (from io.Copy) or parse error.
}

// Test MapSecrets with multiple secrets in an explicit map.
func TestMapSecrets_MultipleExplicit(t *testing.T) {
	callerSecrets := map[string]string{
		"TOKEN":  "abc",
		"SECRET": "xyz",
		"OTHER":  "123",
	}

	jobSecrets := map[string]interface{}{
		"TOKEN":    "${{ secrets.TOKEN }}",
		"SECRET":   "${{ secrets.SECRET }}",
		"MISSING":  "${{ secrets.MISSING }}", // not in caller
		"LITERAL":  "plain-value",
		"ENV_EXPR": "${{ env.SOMETHING }}",
	}

	result := MapSecrets(callerSecrets, jobSecrets)
	assert.Equal(t, "abc", result["TOKEN"])
	assert.Equal(t, "xyz", result["SECRET"])
	assert.Equal(t, "${{ secrets.MISSING }}", result["MISSING"])
	assert.Equal(t, "plain-value", result["LITERAL"])
	assert.Equal(t, "${{ env.SOMETHING }}", result["ENV_EXPR"])
}
