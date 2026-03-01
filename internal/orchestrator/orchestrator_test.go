package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "testdata/hello-world.yml",
		DryRun:       true,
	})
	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.NotNil(t, o.masker)
	assert.NotNil(t, o.logger)
	// In dry-run mode, runner manager is nil.
	assert.Nil(t, o.runnerMgr)
}

func TestNew_WithSecrets(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "test.yml",
		DryRun:       true,
		Secrets:      map[string]string{"TOKEN": "secret123"},
	})
	require.NoError(t, err)
	// Verify masker was created with the secret.
	assert.Equal(t, "value is ***", o.masker.Mask("value is secret123"))
}

func TestDryRun_HelloWorld(t *testing.T) {
	// Find testdata relative to project root.
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "hello-world.yml")

	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Skip("testdata not found")
	}

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

func TestDryRun_MultiJob(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "multi-job.yml")

	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Skip("testdata not found")
	}

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

func TestDryRun_JobFilter(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "multi-job.yml")

	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Skip("testdata not found")
	}

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
		JobFilter:    "build",
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDryRun_JobFilter_NotFound(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "hello-world.yml")

	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Skip("testdata not found")
	}

	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
		JobFilter:    "nonexistent",
	})
	require.NoError(t, err)

	_, err = o.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no jobs match filter")
}

func TestFilterPlan(t *testing.T) {
	// Build a simple graph with two jobs.
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "multi-job.yml")

	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Skip("testdata not found")
	}

	// Use internal logic: filterPlan should keep only matching jobs.
	// Test via the orchestrator dry-run path.
	o, err := New(Options{
		WorkflowPath: workflowPath,
		DryRun:       true,
		RepoPath:     projectRoot,
		JobFilter:    "test",
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestMergeEnv(t *testing.T) {
	result := mergeEnv(
		map[string]string{"A": "1", "B": "2"},
		map[string]string{"B": "3", "C": "4"},
	)
	assert.Equal(t, "1", result["A"])
	assert.Equal(t, "3", result["B"]) // overridden
	assert.Equal(t, "4", result["C"])
}
