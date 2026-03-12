package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emaland/ions/internal/expression"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- readGitRef edge cases ---

func TestReadGitRef_DetachedHEAD(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	// Create initial commit.
	err = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("file.txt")
	require.NoError(t, err)
	commitHash, err := wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	// Detach HEAD by checking out the commit directly.
	err = wt.Checkout(&git.CheckoutOptions{
		Hash: commitHash,
	})
	require.NoError(t, err)

	ref, sha, refName := readGitRef(repo)
	assert.Equal(t, commitHash.String(), sha)
	assert.Equal(t, commitHash.String(), ref)
	assert.Equal(t, commitHash.String(), refName)
}

func TestReadGitRef_NoCommits(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	// No commits yet -- Head() will error.
	ref, sha, refName := readGitRef(repo)
	assert.Equal(t, "", ref)
	assert.Equal(t, "", sha)
	assert.Equal(t, "", refName)
}

func TestReadGitRef_OnBranch(t *testing.T) {
	repoPath := createTestRepo(t, "https://github.com/test/repo.git")
	repo, err := git.PlainOpen(repoPath)
	require.NoError(t, err)

	ref, sha, refName := readGitRef(repo)
	assert.NotEmpty(t, sha)
	assert.Contains(t, ref, "refs/heads/")
	assert.NotContains(t, refName, "refs/heads/")
	assert.NotEmpty(t, refName)
}

func TestReadGitRef_OnTag(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("file.txt")
	require.NoError(t, err)
	commitHash, err := wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	// Create and checkout a tag.
	tagRef := plumbing.NewTagReferenceName("v1.0.0")
	err = repo.Storer.SetReference(plumbing.NewHashReference(tagRef, commitHash))
	require.NoError(t, err)

	// Checkout the tag (detaches HEAD to a tag-like state).
	// go-git doesn't natively checkout tags to HEAD as a tag ref,
	// but we can simulate by setting HEAD directly.
	headRef := plumbing.NewSymbolicReference(plumbing.HEAD, tagRef)
	err = repo.Storer.SetReference(headRef)
	require.NoError(t, err)

	ref, sha, refName := readGitRef(repo)
	assert.Equal(t, commitHash.String(), sha)
	assert.Equal(t, string(tagRef), ref)
	assert.Equal(t, "v1.0.0", refName)
}

// --- tokenValue helper ---

func TestTokenValue_WithToken(t *testing.T) {
	result := tokenValue(BuilderOptions{GitHubToken: "ghp_realtoken123"})
	assert.Equal(t, "ghp_realtoken123", result)
}

func TestTokenValue_EmptyToken(t *testing.T) {
	result := tokenValue(BuilderOptions{})
	assert.Equal(t, "ions-dummy-token", result)
}

func TestTokenValue_EmptyStringToken(t *testing.T) {
	result := tokenValue(BuilderOptions{GitHubToken: ""})
	assert.Equal(t, "ions-dummy-token", result)
}

// --- apiURL and graphqlURL ---

func TestApiURL_Default(t *testing.T) {
	result := apiURL(BuilderOptions{})
	assert.Equal(t, "https://api.github.com", result)
}

func TestApiURL_Override(t *testing.T) {
	result := apiURL(BuilderOptions{APIBaseURL: "http://localhost:9999/api/v3"})
	assert.Equal(t, "http://localhost:9999/api/v3", result)
}

func TestGraphqlURL_Default(t *testing.T) {
	result := graphqlURL(BuilderOptions{})
	assert.Equal(t, "https://api.github.com/graphql", result)
}

func TestGraphqlURL_Override(t *testing.T) {
	result := graphqlURL(BuilderOptions{APIBaseURL: "http://localhost:9999/api/v3"})
	assert.Equal(t, "http://localhost:9999/api/v3/graphql", result)
}

func TestGraphqlURL_TrimsTrailingSlash(t *testing.T) {
	result := graphqlURL(BuilderOptions{APIBaseURL: "http://localhost:9999/api/v3/"})
	assert.Equal(t, "http://localhost:9999/api/v3/graphql", result)
}

// --- BuildRunnerContext OS/arch combinations (via mapOS/mapArch) ---

func TestRunnerContext_ContainsAllExpectedKeys(t *testing.T) {
	ctx := RunnerContext()
	fields := ctx.ObjectFields()

	expectedKeys := []string{"os", "arch", "name", "temp", "tool_cache", "workspace"}
	for _, key := range expectedKeys {
		_, ok := fields[key]
		assert.True(t, ok, "runner context should have key %q", key)
	}
}

func TestRunnerContext_NameIsIonsLocal(t *testing.T) {
	ctx := RunnerContext()
	assert.Equal(t, "ions-local", getField(t, ctx, "name").StringVal())
}

func TestRunnerContext_TempIsNonEmpty(t *testing.T) {
	ctx := RunnerContext()
	temp := getField(t, ctx, "temp").StringVal()
	assert.NotEmpty(t, temp)
}

func TestRunnerContext_WorkspaceIsCurrentDir(t *testing.T) {
	ctx := RunnerContext()
	workspace := getField(t, ctx, "workspace").StringVal()
	cwd, _ := os.Getwd()
	assert.Equal(t, cwd, workspace)
}

// --- GitHubContext token handling ---

func TestGitHubContext_CustomToken(t *testing.T) {
	opts := BuilderOptions{
		GitHubToken: "custom-token-abc",
	}
	ctx := GitHubContext(opts)
	assert.Equal(t, "custom-token-abc", getField(t, ctx, "token").StringVal())
}

func TestGitHubContext_DummyToken(t *testing.T) {
	opts := BuilderOptions{}
	ctx := GitHubContext(opts)
	assert.Equal(t, "ions-dummy-token", getField(t, ctx, "token").StringVal())
}

// --- GitHubContext with custom event name ---

func TestGitHubContext_CustomEventName(t *testing.T) {
	opts := BuilderOptions{
		EventName: "workflow_dispatch",
	}
	ctx := GitHubContext(opts)
	assert.Equal(t, "workflow_dispatch", getField(t, ctx, "event_name").StringVal())
}

// --- GitHubContext run_id auto-generation ---

func TestGitHubContext_RunIDAutoGenerated(t *testing.T) {
	opts := BuilderOptions{}
	ctx := GitHubContext(opts)
	runID := getField(t, ctx, "run_id").StringVal()
	assert.NotEmpty(t, runID)
	assert.Len(t, runID, 10, "auto-generated run_id should be 10 digits")
}

func TestGitHubContext_RunIDExplicit(t *testing.T) {
	opts := BuilderOptions{RunID: "42"}
	ctx := GitHubContext(opts)
	assert.Equal(t, "42", getField(t, ctx, "run_id").StringVal())
}

// --- GitHubContext ref defaults when repo has no commits ---

func TestGitHubContext_DefaultRef(t *testing.T) {
	dir := t.TempDir()
	// Init a repo with no commits.
	_, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	opts := BuilderOptions{RepoPath: dir}
	ctx := GitHubContext(opts)

	// readGitRef returns empty for repos with no commits;
	// GitHubContext should default ref to refs/heads/main.
	assert.Equal(t, "refs/heads/main", getField(t, ctx, "ref").StringVal())
	assert.Equal(t, "main", getField(t, ctx, "ref_name").StringVal())
}

// --- toExpressionValue edge cases ---

func TestToExpressionValue_UnsupportedType(t *testing.T) {
	// A struct is not a recognized type -- converted to string via fmt.Sprintf.
	type custom struct{ X int }
	result := toExpressionValue(custom{X: 42})
	assert.Equal(t, expression.KindString, result.Kind())
	assert.Equal(t, "{42}", result.StringVal())
}

func TestToExpressionValue_Int8(t *testing.T) {
	result := toExpressionValue(int8(127))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(127), result.NumberVal())
}

func TestToExpressionValue_Int16(t *testing.T) {
	result := toExpressionValue(int16(32000))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(32000), result.NumberVal())
}

func TestToExpressionValue_Int32(t *testing.T) {
	result := toExpressionValue(int32(100000))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(100000), result.NumberVal())
}

func TestToExpressionValue_Uint8(t *testing.T) {
	result := toExpressionValue(uint8(255))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(255), result.NumberVal())
}

func TestToExpressionValue_Uint16(t *testing.T) {
	result := toExpressionValue(uint16(65535))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(65535), result.NumberVal())
}

func TestToExpressionValue_Uint32(t *testing.T) {
	result := toExpressionValue(uint32(4294967295))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(4294967295), result.NumberVal())
}

func TestToExpressionValue_Uint64(t *testing.T) {
	result := toExpressionValue(uint64(1234567890))
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, float64(1234567890), result.NumberVal())
}

func TestToExpressionValue_NestedArray(t *testing.T) {
	input := []any{[]any{"a", "b"}, "c"}
	result := toExpressionValue(input)

	assert.Equal(t, expression.KindArray, result.Kind())
	items := result.ArrayItems()
	require.Len(t, items, 2)
	assert.Equal(t, expression.KindArray, items[0].Kind())
	assert.Equal(t, expression.KindString, items[1].Kind())
}

// --- parseRemoteURL additional edge cases ---

func TestParseRemoteURL_GitLabHTTPS(t *testing.T) {
	result := parseRemoteURL("https://gitlab.com/group/project.git")
	assert.Equal(t, "group/project", result)
}

func TestParseRemoteURL_CustomDomainSSH(t *testing.T) {
	result := parseRemoteURL("git@my-git.example.com:team/project.git")
	assert.Equal(t, "team/project", result)
}

func TestParseRemoteURL_JustHost(t *testing.T) {
	result := parseRemoteURL("https://github.com/")
	assert.Equal(t, "", result)
}

// --- NeedsContext with nil JobResult ---

func TestNeedsContext_NilJobResult(t *testing.T) {
	results := map[string]*JobResult{
		"build": nil,
	}

	ctx := NeedsContext(results, []string{"build"})
	fields := ctx.ObjectFields()
	// nil result should be skipped.
	assert.NotContains(t, fields, "build")
}

// --- StepsContext with empty outputs ---

func TestStepsContext_EmptyOutputs(t *testing.T) {
	results := map[string]*StepResult{
		"check": {
			Outcome:    "success",
			Conclusion: "success",
			Outputs:    map[string]string{},
		},
	}

	ctx := StepsContext(results)
	check := getField(t, ctx, "check")
	outputs := getField(t, check, "outputs")
	assert.Equal(t, expression.KindObject, outputs.Kind())
	assert.Empty(t, outputs.ObjectFields())
}

// --- FullContext includes strategy and job keys ---

func TestFullContext_HasStrategyAndJobKeys(t *testing.T) {
	builder := NewBuilder(BuilderOptions{})
	ctx := builder.FullContext()

	stratVal, ok := ctx.Lookup("strategy")
	assert.True(t, ok)
	assert.Equal(t, expression.KindObject, stratVal.Kind())

	jobVal, ok := ctx.Lookup("job")
	assert.True(t, ok)
	assert.Equal(t, expression.KindObject, jobVal.Kind())
}

// --- Builder with all defaults ---

func TestNewBuilder_Defaults(t *testing.T) {
	builder := NewBuilder(BuilderOptions{})
	ctx := builder.FullContext()

	ghCtx, ok := ctx.Lookup("github")
	assert.True(t, ok)
	assert.Equal(t, "push", getField(t, ghCtx, "event_name").StringVal())
	assert.Equal(t, "local-actor", getField(t, ghCtx, "actor").StringVal())
	assert.Equal(t, "local/repo", getField(t, ghCtx, "repository").StringVal())
}

// --- readGitActor edge case ---

func TestGitHubContext_ActorFromGitConfig(t *testing.T) {
	repoPath := createTestRepo(t, "https://github.com/org/repo.git")
	opts := BuilderOptions{RepoPath: repoPath}
	ctx := GitHubContext(opts)
	assert.Equal(t, "Test User", getField(t, ctx, "actor").StringVal())
}

func TestGitHubContext_ActorFallback(t *testing.T) {
	// With no repo, actor should be "local-actor".
	opts := BuilderOptions{}
	ctx := GitHubContext(opts)
	assert.Equal(t, "local-actor", getField(t, ctx, "actor").StringVal())
}

// --- EnvContext with all three levels ---

func TestEnvContext_AllLevelsOverlap(t *testing.T) {
	wf := map[string]string{"A": "wf", "B": "wf", "C": "wf"}
	job := map[string]string{"A": "job", "B": "job"}
	step := map[string]string{"A": "step"}

	ctx := EnvContext(wf, job, step)
	assert.Equal(t, "step", getField(t, ctx, "A").StringVal())
	assert.Equal(t, "job", getField(t, ctx, "B").StringVal())
	assert.Equal(t, "wf", getField(t, ctx, "C").StringVal())
}

func TestToExpressionValue_JSONNumber(t *testing.T) {
	n := json.Number("42.5")
	result := toExpressionValue(n)
	assert.Equal(t, expression.KindNumber, result.Kind())
	assert.Equal(t, 42.5, result.NumberVal())
}

func TestToExpressionValue_MapInterfaceInterface(t *testing.T) {
	// YAML sometimes produces map[interface{}]interface{} instead of map[string]any.
	m := map[interface{}]interface{}{
		"key": "value",
		42:    "num-key",
	}
	result := toExpressionValue(m)
	assert.Equal(t, expression.KindObject, result.Kind())
	fields := result.ObjectFields()
	assert.Equal(t, "value", fields["key"].StringVal())
	assert.Equal(t, "num-key", fields["42"].StringVal())
}
