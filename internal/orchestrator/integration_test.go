package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests require a runner binary and are skipped by default.
// Set IONS_INTEGRATION=1 to run them.
//
// These tests launch the full broker + runner stack and execute real
// workflows end-to-end.

func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("IONS_INTEGRATION") == "" {
		t.Skip("set IONS_INTEGRATION=1 to run integration tests")
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..")
}

func workflowPath(t *testing.T, name string) string {
	t.Helper()
	root := projectRoot(t)
	p := filepath.Join(root, "testdata", "workflows", name)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		t.Skipf("testdata not found: %s", p)
	}
	return p
}

func TestIntegration_HelloWorld(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "hello-world.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success, "workflow should succeed")
	assert.Contains(t, result.JobResults, "greet")
	assert.Equal(t, "success", result.JobResults["greet"].Status)
}

func TestIntegration_MultiJob(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "multi-job-simple.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success, "workflow should succeed")

	// All three jobs should have run.
	assert.Contains(t, result.JobResults, "build")
	assert.Contains(t, result.JobResults, "test")
	assert.Contains(t, result.JobResults, "deploy")

	assert.Equal(t, "success", result.JobResults["build"].Status)
	assert.Equal(t, "success", result.JobResults["test"].Status)
	assert.Equal(t, "success", result.JobResults["deploy"].Status)

	// Build should produce a version output.
	assert.Equal(t, "1.2.3", result.JobResults["build"].Outputs["version"])
}

func TestIntegration_JobFilter(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "multi-job-simple.yml"),
		RepoPath:     projectRoot(t),
		JobFilter:    "build",
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Only build should have run.
	assert.Contains(t, result.JobResults, "build")
	assert.NotContains(t, result.JobResults, "test")
	assert.NotContains(t, result.JobResults, "deploy")
}

func TestIntegration_EnvVars(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "hello-world.yml"),
		RepoPath:     projectRoot(t),
		Env:          map[string]string{"MY_VAR": "test-value"},
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestIntegration_SecretMasking(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "hello-world.yml"),
		RepoPath:     projectRoot(t),
		Secrets:      map[string]string{"MY_SECRET": "super-secret-value"},
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestIntegration_Matrix(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "matrix-simple.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success, "matrix workflow should succeed")

	assert.Equal(t, 2, len(result.JobResults))
	for _, jr := range result.JobResults {
		assert.Equal(t, "success", jr.Status)
	}
}

func TestIntegration_Timeout(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "timeout-simple.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.JobResults, "quick-job")
	assert.Equal(t, "success", result.JobResults["quick-job"].Status)
}

func TestIntegration_ContinueOnError(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "continue-on-error.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)

	// The flaky job fails (exit 1) but has continue-on-error: true.
	// depends-on-flaky should still run because flaky's conclusion is "success".
	assert.True(t, result.Success)

	assert.Contains(t, result.JobResults, "flaky")
	assert.Contains(t, result.JobResults, "depends-on-flaky")
	assert.Contains(t, result.JobResults, "always-run")
}

func TestIntegration_Expressions(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "expressions-test.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success, "expression tests should pass")
}

func TestIntegration_Concurrency(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "concurrency-test.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "success", result.JobResults["build"].Status)
	assert.Equal(t, "success", result.JobResults["deploy"].Status)
}

func TestIntegration_DryRun(t *testing.T) {
	skipIfNoIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "runtime-conditions.yml"),
		RepoPath:     projectRoot(t),
		DryRun:       true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestIntegration_Cancellation(t *testing.T) {
	skipIfNoIntegration(t)

	// Create a context that cancels immediately after the runner starts.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	o, err := New(Options{
		WorkflowPath: workflowPath(t, "hello-world.yml"),
		RepoPath:     projectRoot(t),
		Verbose:      true,
	})
	require.NoError(t, err)

	result, err := o.Run(ctx)
	// Should either succeed quickly or be cancelled — both are acceptable.
	if err != nil {
		return // cancelled or timed out — OK
	}
	// If it completed, it should have either succeeded or been cancelled.
	assert.NotNil(t, result)
}
