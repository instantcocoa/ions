package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Dry-run tests for newly added workflow features
// ---------------------------------------------------------------------------

func TestDryRun_EnvironmentSecrets(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "environment-secrets.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
		Secrets:      map[string]string{"GLOBAL_TOKEN": "tok123"},
		EnvSecrets: map[string]map[string]string{
			"staging":    {"DEPLOY_KEY": "stg-key"},
			"production": {"DEPLOY_KEY": "prd-key"},
		},
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_ConcurrencyCancel(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "concurrency-cancel.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_MatrixComplex(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "matrix-complex.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_ReusableCall(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "reusable-call.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_ExpressionComplex(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "expression-complex.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_FailFast(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "fail-fast.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_Timeout(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "timeout.yml")

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}
