package context

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/emaland/ions/internal/expression"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helper to access object fields ---

func getField(t *testing.T, obj expression.Value, key string) expression.Value {
	t.Helper()
	require.Equal(t, expression.KindObject, obj.Kind(), "expected object value")
	fields := obj.ObjectFields()
	val, ok := fields[key]
	if !ok {
		t.Fatalf("field %q not found in object (available: %v)", key, fieldKeys(fields))
	}
	return val
}

func fieldKeys(fields map[string]expression.Value) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	return keys
}

// --- GitHubContext tests ---

// createTestRepo creates a temporary git repo with an initial commit and origin remote.
// Returns the repo path.
func createTestRepo(t *testing.T, remoteURL string) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	// Set user config
	cfg, err := repo.Config()
	require.NoError(t, err)
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"
	err = repo.SetConfig(cfg)
	require.NoError(t, err)

	// Create an initial file and commit
	testFile := filepath.Join(dir, "README.md")
	err = os.WriteFile(testFile, []byte("# Test\n"), 0644)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("README.md")
	require.NoError(t, err)

	_, err = wt.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	// Add origin remote
	if remoteURL != "" {
		_, err = repo.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{remoteURL},
		})
		require.NoError(t, err)
	}

	return dir
}

func TestGitHubContext_WithRepo(t *testing.T) {
	repoPath := createTestRepo(t, "https://github.com/myorg/myrepo.git")

	opts := BuilderOptions{
		RepoPath:     repoPath,
		EventName:    "pull_request",
		WorkflowName: "CI",
		RunID:        "12345",
		RunNumber:    7,
	}

	ctx := GitHubContext(opts)
	require.Equal(t, expression.KindObject, ctx.Kind())

	// Check event_name
	assert.Equal(t, "pull_request", getField(t, ctx, "event_name").StringVal())

	// Check workflow
	assert.Equal(t, "CI", getField(t, ctx, "workflow").StringVal())

	// Check run_id
	assert.Equal(t, "12345", getField(t, ctx, "run_id").StringVal())

	// Check run_number
	assert.Equal(t, "7", getField(t, ctx, "run_number").StringVal())

	// Check actor (from git config)
	assert.Equal(t, "Test User", getField(t, ctx, "actor").StringVal())

	// Check repository (from origin remote)
	assert.Equal(t, "myorg/myrepo", getField(t, ctx, "repository").StringVal())

	// Check repository_owner
	assert.Equal(t, "myorg", getField(t, ctx, "repository_owner").StringVal())

	// Check SHA is a 40-char hex string
	sha := getField(t, ctx, "sha").StringVal()
	assert.Len(t, sha, 40, "SHA should be 40 characters")

	// Check ref is refs/heads/master or refs/heads/main (go-git defaults to master)
	ref := getField(t, ctx, "ref").StringVal()
	assert.Contains(t, ref, "refs/heads/", "ref should be a branch ref")

	// Check ref_name is the short name
	refName := getField(t, ctx, "ref_name").StringVal()
	assert.NotEmpty(t, refName)
	assert.NotContains(t, refName, "refs/heads/", "ref_name should be the short name")

	// Check head_ref equals ref
	assert.Equal(t, ref, getField(t, ctx, "head_ref").StringVal())

	// Check base_ref is empty
	assert.Equal(t, "", getField(t, ctx, "base_ref").StringVal())

	// Check static URLs
	assert.Equal(t, "https://github.com", getField(t, ctx, "server_url").StringVal())
	assert.Equal(t, "https://api.github.com", getField(t, ctx, "api_url").StringVal())
	assert.Equal(t, "https://api.github.com/graphql", getField(t, ctx, "graphql_url").StringVal())

	// Check workspace is a directory
	workspace := getField(t, ctx, "workspace").StringVal()
	assert.NotEmpty(t, workspace)

	// Check event is an empty object
	event := getField(t, ctx, "event")
	assert.Equal(t, expression.KindObject, event.Kind())

	// Check empty string fields
	assert.Equal(t, "ions-dummy-token", getField(t, ctx, "token").StringVal())
	assert.Equal(t, "", getField(t, ctx, "job").StringVal())
	assert.Equal(t, "", getField(t, ctx, "action").StringVal())
	assert.Equal(t, "", getField(t, ctx, "action_path").StringVal())
	assert.Equal(t, "", getField(t, ctx, "action_ref").StringVal())
	assert.Equal(t, "", getField(t, ctx, "action_repository").StringVal())
}

func TestGitHubContext_SSHRemote(t *testing.T) {
	repoPath := createTestRepo(t, "git@github.com:owner/repo.git")

	opts := BuilderOptions{
		RepoPath: repoPath,
	}

	ctx := GitHubContext(opts)

	assert.Equal(t, "owner/repo", getField(t, ctx, "repository").StringVal())
	assert.Equal(t, "owner", getField(t, ctx, "repository_owner").StringVal())
}

func TestGitHubContext_NoRepo(t *testing.T) {
	opts := BuilderOptions{
		RepoPath: "", // no repo
	}

	ctx := GitHubContext(opts)
	require.Equal(t, expression.KindObject, ctx.Kind())

	// Should fall back to defaults
	assert.Equal(t, "push", getField(t, ctx, "event_name").StringVal())
	assert.Equal(t, "local-actor", getField(t, ctx, "actor").StringVal())
	assert.Equal(t, "local/repo", getField(t, ctx, "repository").StringVal())
	assert.Equal(t, "local", getField(t, ctx, "repository_owner").StringVal())
	assert.Equal(t, "refs/heads/main", getField(t, ctx, "ref").StringVal())
	assert.Equal(t, "", getField(t, ctx, "sha").StringVal())
	assert.Equal(t, "1", getField(t, ctx, "run_number").StringVal())
}

func TestGitHubContext_InvalidRepoPath(t *testing.T) {
	opts := BuilderOptions{
		RepoPath: "/nonexistent/path/that/does/not/exist",
	}

	// Should not panic; should use defaults
	ctx := GitHubContext(opts)
	require.Equal(t, expression.KindObject, ctx.Kind())

	assert.Equal(t, "local-actor", getField(t, ctx, "actor").StringVal())
	assert.Equal(t, "local/repo", getField(t, ctx, "repository").StringVal())
}

func TestGitHubContext_DefaultsWhenFieldsEmpty(t *testing.T) {
	opts := BuilderOptions{
		RepoPath: "",
		// EventName, RunID, RunNumber all zero/empty
	}

	ctx := GitHubContext(opts)

	assert.Equal(t, "push", getField(t, ctx, "event_name").StringVal())
	assert.Equal(t, "1", getField(t, ctx, "run_number").StringVal())

	// run_id should be auto-generated (non-empty)
	runID := getField(t, ctx, "run_id").StringVal()
	assert.NotEmpty(t, runID)
}

func TestGitHubContext_NoRemote(t *testing.T) {
	repoPath := createTestRepo(t, "") // no remote URL

	opts := BuilderOptions{
		RepoPath: repoPath,
	}

	ctx := GitHubContext(opts)

	// Should fall back to defaults for repository
	assert.Equal(t, "local/repo", getField(t, ctx, "repository").StringVal())
	assert.Equal(t, "local", getField(t, ctx, "repository_owner").StringVal())

	// But should still read SHA and ref from the repo
	assert.NotEmpty(t, getField(t, ctx, "sha").StringVal())
	assert.NotEmpty(t, getField(t, ctx, "ref").StringVal())

	// Without an APIBaseURL, server_url stays as github.com
	assert.Equal(t, "https://github.com", getField(t, ctx, "server_url").StringVal())
}

func TestGitHubContext_NoRemoteWithBroker(t *testing.T) {
	repoPath := createTestRepo(t, "") // no remote URL

	opts := BuilderOptions{
		RepoPath:   repoPath,
		APIBaseURL: "http://localhost:12345/api/v3",
	}

	ctx := GitHubContext(opts)

	// With no remote and an API base URL, server_url should point at
	// the broker so actions/checkout hits the local stub instead of
	// trying to clone from github.com/local/repo.
	assert.Equal(t, "http://localhost:12345", getField(t, ctx, "server_url").StringVal())
	assert.Equal(t, "http://localhost:12345/api/v3", getField(t, ctx, "api_url").StringVal())
}

func TestGitHubContext_WithRemoteAndBroker(t *testing.T) {
	repoPath := createTestRepo(t, "https://github.com/real/repo.git")

	opts := BuilderOptions{
		RepoPath:   repoPath,
		APIBaseURL: "http://localhost:12345/api/v3",
	}

	ctx := GitHubContext(opts)

	// With a real remote, server_url stays as github.com even when
	// a broker is available — checkout can reach github.com.
	assert.Equal(t, "https://github.com", getField(t, ctx, "server_url").StringVal())
}

// --- parseRemoteURL tests ---

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "HTTPS with .git",
			url:      "https://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS without .git",
			url:      "https://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "SSH with .git",
			url:      "git@github.com:owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "SSH without .git",
			url:      "git@github.com:owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "HTTP (not HTTPS) with .git",
			url:      "http://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "Different host SSH",
			url:      "git@gitlab.com:group/project.git",
			expected: "group/project",
		},
		{
			name:     "Invalid URL",
			url:      "not-a-url",
			expected: "",
		},
		{
			name:     "Empty string",
			url:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseRemoteURL(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- RunnerContext tests ---

func TestRunnerContext(t *testing.T) {
	ctx := RunnerContext()
	require.Equal(t, expression.KindObject, ctx.Kind())

	// Check OS is one of the expected mapped values
	osVal := getField(t, ctx, "os").StringVal()
	expectedOS := map[string]string{
		"darwin":  "macOS",
		"linux":   "Linux",
		"windows": "Windows",
	}
	if expected, ok := expectedOS[runtime.GOOS]; ok {
		assert.Equal(t, expected, osVal)
	} else {
		assert.NotEmpty(t, osVal)
	}

	// Check arch is one of the expected mapped values
	archVal := getField(t, ctx, "arch").StringVal()
	expectedArch := map[string]string{
		"amd64": "X64",
		"arm64": "ARM64",
	}
	if expected, ok := expectedArch[runtime.GOARCH]; ok {
		assert.Equal(t, expected, archVal)
	} else {
		assert.NotEmpty(t, archVal)
	}

	// Check name
	assert.Equal(t, "ions-local", getField(t, ctx, "name").StringVal())

	// Check temp is populated
	assert.NotEmpty(t, getField(t, ctx, "temp").StringVal())

	// Check tool_cache
	toolCache := getField(t, ctx, "tool_cache").StringVal()
	assert.Contains(t, toolCache, ".ions")
	assert.Contains(t, toolCache, "tool-cache")

	// Check workspace is populated
	assert.NotEmpty(t, getField(t, ctx, "workspace").StringVal())
}

func TestMapOS(t *testing.T) {
	assert.Equal(t, "macOS", mapOS("darwin"))
	assert.Equal(t, "Linux", mapOS("linux"))
	assert.Equal(t, "Windows", mapOS("windows"))
	assert.Equal(t, "freebsd", mapOS("freebsd")) // unknown passes through
}

func TestMapArch(t *testing.T) {
	assert.Equal(t, "X64", mapArch("amd64"))
	assert.Equal(t, "ARM64", mapArch("arm64"))
	assert.Equal(t, "386", mapArch("386")) // unknown passes through
}

// --- EnvContext tests ---

func TestEnvContext_MergePrecedence(t *testing.T) {
	workflow := map[string]string{
		"A": "workflow-a",
		"B": "workflow-b",
		"C": "workflow-c",
	}
	job := map[string]string{
		"B": "job-b",
		"D": "job-d",
	}
	step := map[string]string{
		"C": "step-c",
		"D": "step-d",
		"E": "step-e",
	}

	ctx := EnvContext(workflow, job, step)
	require.Equal(t, expression.KindObject, ctx.Kind())

	// A: only in workflow
	assert.Equal(t, "workflow-a", getField(t, ctx, "A").StringVal())

	// B: job overrides workflow
	assert.Equal(t, "job-b", getField(t, ctx, "B").StringVal())

	// C: step overrides workflow
	assert.Equal(t, "step-c", getField(t, ctx, "C").StringVal())

	// D: step overrides job
	assert.Equal(t, "step-d", getField(t, ctx, "D").StringVal())

	// E: only in step
	assert.Equal(t, "step-e", getField(t, ctx, "E").StringVal())
}

func TestEnvContext_NilMaps(t *testing.T) {
	ctx := EnvContext(nil, nil, nil)
	require.Equal(t, expression.KindObject, ctx.Kind())

	fields := ctx.ObjectFields()
	assert.Empty(t, fields)
}

func TestEnvContext_SingleLevel(t *testing.T) {
	workflow := map[string]string{
		"FOO": "bar",
	}

	ctx := EnvContext(workflow, nil, nil)
	assert.Equal(t, "bar", getField(t, ctx, "FOO").StringVal())
}

func TestEnvContext_StepOverridesAll(t *testing.T) {
	workflow := map[string]string{"X": "1"}
	job := map[string]string{"X": "2"}
	step := map[string]string{"X": "3"}

	ctx := EnvContext(workflow, job, step)
	assert.Equal(t, "3", getField(t, ctx, "X").StringVal())
}

// --- StepsContext tests ---

func TestStepsContext_Empty(t *testing.T) {
	ctx := StepsContext(nil)
	require.Equal(t, expression.KindObject, ctx.Kind())

	fields := ctx.ObjectFields()
	assert.Empty(t, fields)
}

func TestStepsContext_WithResults(t *testing.T) {
	results := map[string]*StepResult{
		"build": {
			Outcome:    "success",
			Conclusion: "success",
			Outputs: map[string]string{
				"artifact": "build.zip",
				"version":  "1.2.3",
			},
		},
		"test": {
			Outcome:    "failure",
			Conclusion: "success", // continue-on-error changed it
			Outputs:    map[string]string{},
		},
	}

	ctx := StepsContext(results)
	require.Equal(t, expression.KindObject, ctx.Kind())

	// Check build step
	build := getField(t, ctx, "build")
	assert.Equal(t, "success", getField(t, build, "outcome").StringVal())
	assert.Equal(t, "success", getField(t, build, "conclusion").StringVal())

	buildOutputs := getField(t, build, "outputs")
	assert.Equal(t, "build.zip", getField(t, buildOutputs, "artifact").StringVal())
	assert.Equal(t, "1.2.3", getField(t, buildOutputs, "version").StringVal())

	// Check test step
	test := getField(t, ctx, "test")
	assert.Equal(t, "failure", getField(t, test, "outcome").StringVal())
	assert.Equal(t, "success", getField(t, test, "conclusion").StringVal())
}

func TestStepsContext_NilResultIgnored(t *testing.T) {
	results := map[string]*StepResult{
		"good": {
			Outcome:    "success",
			Conclusion: "success",
			Outputs:    nil,
		},
		"bad": nil, // nil result should be skipped
	}

	ctx := StepsContext(results)
	fields := ctx.ObjectFields()

	assert.Contains(t, fields, "good")
	assert.NotContains(t, fields, "bad")
}

func TestStepsContext_NilOutputs(t *testing.T) {
	results := map[string]*StepResult{
		"step1": {
			Outcome:    "success",
			Conclusion: "success",
			Outputs:    nil,
		},
	}

	ctx := StepsContext(results)
	step1 := getField(t, ctx, "step1")

	// outputs should be an empty object, not null
	outputs := getField(t, step1, "outputs")
	assert.Equal(t, expression.KindObject, outputs.Kind())
}

// --- NeedsContext tests ---

func TestNeedsContext_Empty(t *testing.T) {
	ctx := NeedsContext(nil, nil)
	require.Equal(t, expression.KindObject, ctx.Kind())

	fields := ctx.ObjectFields()
	assert.Empty(t, fields)
}

func TestNeedsContext_WithResults(t *testing.T) {
	results := map[string]*JobResult{
		"build": {
			Result: "success",
			Outputs: map[string]string{
				"image": "myapp:latest",
			},
		},
		"lint": {
			Result:  "success",
			Outputs: map[string]string{},
		},
		"deploy": {
			Result:  "skipped",
			Outputs: nil,
		},
	}

	// Only include build and lint in needs
	ctx := NeedsContext(results, []string{"build", "lint"})
	fields := ctx.ObjectFields()

	// Should include build and lint
	assert.Contains(t, fields, "build")
	assert.Contains(t, fields, "lint")

	// Should NOT include deploy (not in jobNeeds)
	assert.NotContains(t, fields, "deploy")

	// Check build
	build := getField(t, ctx, "build")
	assert.Equal(t, "success", getField(t, build, "result").StringVal())
	buildOutputs := getField(t, build, "outputs")
	assert.Equal(t, "myapp:latest", getField(t, buildOutputs, "image").StringVal())
}

func TestNeedsContext_MissingJobResult(t *testing.T) {
	results := map[string]*JobResult{
		"build": {
			Result:  "success",
			Outputs: map[string]string{},
		},
	}

	// Request a job that doesn't exist in results
	ctx := NeedsContext(results, []string{"build", "nonexistent"})
	fields := ctx.ObjectFields()

	assert.Contains(t, fields, "build")
	assert.NotContains(t, fields, "nonexistent")
}

func TestNeedsContext_OnlyJobNeeds(t *testing.T) {
	results := map[string]*JobResult{
		"a": {Result: "success", Outputs: map[string]string{}},
		"b": {Result: "success", Outputs: map[string]string{}},
		"c": {Result: "success", Outputs: map[string]string{}},
	}

	// Only need "a" and "c"
	ctx := NeedsContext(results, []string{"a", "c"})
	fields := ctx.ObjectFields()

	assert.Len(t, fields, 2)
	assert.Contains(t, fields, "a")
	assert.Contains(t, fields, "c")
	assert.NotContains(t, fields, "b")
}

// --- MatrixContext tests ---

func TestMatrixContext_StringValues(t *testing.T) {
	values := map[string]any{
		"os": "ubuntu-latest",
	}

	ctx := MatrixContext(values)
	assert.Equal(t, "ubuntu-latest", getField(t, ctx, "os").StringVal())
}

func TestMatrixContext_IntValues(t *testing.T) {
	values := map[string]any{
		"node": 18,
	}

	ctx := MatrixContext(values)
	assert.Equal(t, float64(18), getField(t, ctx, "node").NumberVal())
}

func TestMatrixContext_BoolValues(t *testing.T) {
	values := map[string]any{
		"experimental": true,
	}

	ctx := MatrixContext(values)
	assert.Equal(t, true, getField(t, ctx, "experimental").BoolVal())
}

func TestMatrixContext_NilValue(t *testing.T) {
	values := map[string]any{
		"optional": nil,
	}

	ctx := MatrixContext(values)
	assert.Equal(t, expression.KindNull, getField(t, ctx, "optional").Kind())
}

func TestMatrixContext_MixedTypes(t *testing.T) {
	values := map[string]any{
		"os":           "ubuntu-latest",
		"node":         16,
		"experimental": false,
		"nothing":      nil,
	}

	ctx := MatrixContext(values)
	assert.Equal(t, expression.KindString, getField(t, ctx, "os").Kind())
	assert.Equal(t, expression.KindNumber, getField(t, ctx, "node").Kind())
	assert.Equal(t, expression.KindBool, getField(t, ctx, "experimental").Kind())
	assert.Equal(t, expression.KindNull, getField(t, ctx, "nothing").Kind())
}

func TestMatrixContext_Empty(t *testing.T) {
	ctx := MatrixContext(nil)
	require.Equal(t, expression.KindObject, ctx.Kind())
	assert.Empty(t, ctx.ObjectFields())
}

func TestMatrixContext_FloatValue(t *testing.T) {
	values := map[string]any{
		"ratio": 3.14,
	}

	ctx := MatrixContext(values)
	assert.Equal(t, expression.KindNumber, getField(t, ctx, "ratio").Kind())
	assert.InDelta(t, 3.14, getField(t, ctx, "ratio").NumberVal(), 0.001)
}

// --- SecretsContext tests ---

func TestSecretsContext(t *testing.T) {
	secrets := map[string]string{
		"GITHUB_TOKEN": "ghp_abc123",
		"NPM_TOKEN":    "npm_xyz",
	}

	ctx := SecretsContext(secrets)
	require.Equal(t, expression.KindObject, ctx.Kind())

	assert.Equal(t, "ghp_abc123", getField(t, ctx, "GITHUB_TOKEN").StringVal())
	assert.Equal(t, "npm_xyz", getField(t, ctx, "NPM_TOKEN").StringVal())
}

func TestSecretsContext_Empty(t *testing.T) {
	ctx := SecretsContext(nil)
	assert.Equal(t, expression.KindObject, ctx.Kind())
	assert.Empty(t, ctx.ObjectFields())
}

// --- InputsContext tests ---

func TestInputsContext(t *testing.T) {
	inputs := map[string]string{
		"environment": "staging",
		"dry_run":     "true",
	}

	ctx := InputsContext(inputs)
	require.Equal(t, expression.KindObject, ctx.Kind())

	assert.Equal(t, "staging", getField(t, ctx, "environment").StringVal())
	assert.Equal(t, "true", getField(t, ctx, "dry_run").StringVal())
}

func TestInputsContext_Empty(t *testing.T) {
	ctx := InputsContext(nil)
	assert.Equal(t, expression.KindObject, ctx.Kind())
	assert.Empty(t, ctx.ObjectFields())
}

// --- VarsContext tests ---

func TestVarsContext(t *testing.T) {
	vars := map[string]string{
		"DEPLOY_URL": "https://example.com",
	}

	ctx := VarsContext(vars)
	assert.Equal(t, "https://example.com", getField(t, ctx, "DEPLOY_URL").StringVal())
}

func TestVarsContext_Empty(t *testing.T) {
	ctx := VarsContext(nil)
	assert.Equal(t, expression.KindObject, ctx.Kind())
	assert.Empty(t, ctx.ObjectFields())
}

// --- toExpressionValue tests ---

func TestToExpressionValue(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected expression.Value
	}{
		{"nil", nil, expression.Null()},
		{"string", "hello", expression.String("hello")},
		{"bool true", true, expression.Bool(true)},
		{"bool false", false, expression.Bool(false)},
		{"int", 42, expression.Number(42)},
		{"int64", int64(100), expression.Number(100)},
		{"float64", 3.14, expression.Number(3.14)},
		{"float32", float32(2.5), expression.Number(float64(float32(2.5)))},
		{"uint", uint(7), expression.Number(7)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toExpressionValue(tt.input)
			assert.True(t, result.Equals(tt.expected),
				"expected %#v, got %#v", tt.expected, result)
		})
	}
}

func TestToExpressionValue_Array(t *testing.T) {
	input := []any{"a", "b", "c"}
	result := toExpressionValue(input)

	assert.Equal(t, expression.KindArray, result.Kind())
	items := result.ArrayItems()
	require.Len(t, items, 3)
	assert.Equal(t, "a", items[0].StringVal())
	assert.Equal(t, "b", items[1].StringVal())
	assert.Equal(t, "c", items[2].StringVal())
}

func TestToExpressionValue_NestedMap(t *testing.T) {
	input := map[string]any{
		"key": "value",
		"num": 42,
	}
	result := toExpressionValue(input)

	assert.Equal(t, expression.KindObject, result.Kind())
	fields := result.ObjectFields()
	assert.Equal(t, "value", fields["key"].StringVal())
	assert.Equal(t, float64(42), fields["num"].NumberVal())
}

// --- FullContext tests ---

func TestFullContext_AllKeysPresent(t *testing.T) {
	builder := NewBuilder(BuilderOptions{})
	ctx := builder.FullContext()

	expectedKeys := []string{
		"github", "env", "runner", "steps", "needs",
		"matrix", "secrets", "inputs", "vars",
	}

	for _, key := range expectedKeys {
		val, ok := ctx.Lookup(key)
		assert.True(t, ok, "expected key %q in full context", key)
		assert.Equal(t, expression.KindObject, val.Kind(),
			"expected %q to be an object", key)
	}
}

func TestFullContext_WithPopulatedOptions(t *testing.T) {
	repoPath := createTestRepo(t, "https://github.com/test/project.git")

	builder := NewBuilder(BuilderOptions{
		RepoPath:     repoPath,
		WorkflowEnv:  map[string]string{"CI": "true"},
		JobEnv:       map[string]string{"JOB_NAME": "build"},
		StepEnv:      map[string]string{"STEP_VAR": "hello"},
		EventName:    "push",
		WorkflowName: "Build",
		RunID:        "99",
		RunNumber:    3,
		Secrets:      map[string]string{"TOKEN": "secret123"},
		Vars:         map[string]string{"DEPLOY_ENV": "prod"},
		Inputs:       map[string]string{"version": "1.0.0"},
		MatrixValues: map[string]any{"os": "ubuntu-latest", "node": 18},
		StepResults: map[string]*StepResult{
			"setup": {
				Outcome:    "success",
				Conclusion: "success",
				Outputs:    map[string]string{"cache-hit": "true"},
			},
		},
		JobResults: map[string]*JobResult{
			"lint": {
				Result:  "success",
				Outputs: map[string]string{"passed": "true"},
			},
		},
		JobNeeds: []string{"lint"},
	})

	ctx := builder.FullContext()

	// Verify github context
	ghCtx, ok := ctx.Lookup("github")
	assert.True(t, ok)
	assert.Equal(t, "push", getField(t, ghCtx, "event_name").StringVal())
	assert.Equal(t, "Build", getField(t, ghCtx, "workflow").StringVal())

	// Verify env context with merge
	envCtx, ok := ctx.Lookup("env")
	assert.True(t, ok)
	assert.Equal(t, "true", getField(t, envCtx, "CI").StringVal())
	assert.Equal(t, "build", getField(t, envCtx, "JOB_NAME").StringVal())
	assert.Equal(t, "hello", getField(t, envCtx, "STEP_VAR").StringVal())

	// Verify runner context
	runnerCtx, ok := ctx.Lookup("runner")
	assert.True(t, ok)
	assert.Equal(t, "ions-local", getField(t, runnerCtx, "name").StringVal())

	// Verify secrets context
	secretsCtx, ok := ctx.Lookup("secrets")
	assert.True(t, ok)
	assert.Equal(t, "secret123", getField(t, secretsCtx, "TOKEN").StringVal())

	// Verify matrix context
	matrixCtx, ok := ctx.Lookup("matrix")
	assert.True(t, ok)
	assert.Equal(t, "ubuntu-latest", getField(t, matrixCtx, "os").StringVal())
	assert.Equal(t, float64(18), getField(t, matrixCtx, "node").NumberVal())

	// Verify steps context
	stepsCtx, ok := ctx.Lookup("steps")
	assert.True(t, ok)
	setupStep := getField(t, stepsCtx, "setup")
	assert.Equal(t, "success", getField(t, setupStep, "outcome").StringVal())

	// Verify needs context
	needsCtx, ok := ctx.Lookup("needs")
	assert.True(t, ok)
	lintJob := getField(t, needsCtx, "lint")
	assert.Equal(t, "success", getField(t, lintJob, "result").StringVal())

	// Verify inputs context
	inputsCtx, ok := ctx.Lookup("inputs")
	assert.True(t, ok)
	assert.Equal(t, "1.0.0", getField(t, inputsCtx, "version").StringVal())

	// Verify vars context
	varsCtx, ok := ctx.Lookup("vars")
	assert.True(t, ok)
	assert.Equal(t, "prod", getField(t, varsCtx, "DEPLOY_ENV").StringVal())
}

// --- StrategyContext tests ---

func TestStrategyContext_Defaults(t *testing.T) {
	ctx := StrategyContext(BuilderOptions{})
	require.Equal(t, expression.KindObject, ctx.Kind())

	assert.Equal(t, true, getField(t, ctx, "fail-fast").BoolVal())
	assert.Equal(t, float64(0), getField(t, ctx, "job-index").NumberVal())
	assert.Equal(t, float64(0), getField(t, ctx, "job-total").NumberVal())
	assert.Equal(t, float64(0), getField(t, ctx, "max-parallel").NumberVal())
}

func TestStrategyContext_WithValues(t *testing.T) {
	ff := false
	mp := 2
	ctx := StrategyContext(BuilderOptions{
		FailFast:    &ff,
		JobIndex:    1,
		JobTotal:    3,
		MaxParallel: &mp,
	})

	assert.Equal(t, false, getField(t, ctx, "fail-fast").BoolVal())
	assert.Equal(t, float64(1), getField(t, ctx, "job-index").NumberVal())
	assert.Equal(t, float64(3), getField(t, ctx, "job-total").NumberVal())
	assert.Equal(t, float64(2), getField(t, ctx, "max-parallel").NumberVal())
}

func TestStrategyContext_MaxParallelDefaultsToJobTotal(t *testing.T) {
	ctx := StrategyContext(BuilderOptions{
		JobTotal: 5,
	})
	assert.Equal(t, float64(5), getField(t, ctx, "max-parallel").NumberVal())
}

// --- JobContext tests ---

func TestJobContext_Default(t *testing.T) {
	ctx := JobContext(BuilderOptions{})
	require.Equal(t, expression.KindObject, ctx.Kind())
	assert.Equal(t, "success", getField(t, ctx, "status").StringVal())
}

func TestJobContext_WithStatus(t *testing.T) {
	ctx := JobContext(BuilderOptions{JobStatus: "failure"})
	assert.Equal(t, "failure", getField(t, ctx, "status").StringVal())
}

func TestFullContext_CaseInsensitiveLookup(t *testing.T) {
	builder := NewBuilder(BuilderOptions{})
	ctx := builder.FullContext()

	// MapContext.Lookup is case-insensitive
	_, ok := ctx.Lookup("GITHUB")
	assert.True(t, ok, "should find 'GITHUB' case-insensitively")

	_, ok = ctx.Lookup("Runner")
	assert.True(t, ok, "should find 'Runner' case-insensitively")
}
