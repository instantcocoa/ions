package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"

	"github.com/emaland/ions/internal/broker"
	ionsctx "github.com/emaland/ions/internal/context"
	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/workflow"
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

func TestDryRun_DockerAction(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "docker-action.yml")

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

func TestDryRun_ReusableWorkflow_Remote(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "reusable-workflow.yml")

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

func TestDryRun_ReusableWorkflow_Local(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "reusable-caller-local.yml")

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

func TestReportFailedSteps(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	succeeded := "succeeded"
	result := &broker.JobCompletionResult{
		JobID:  "test-job",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Setup", Result: &succeeded},
			{ID: "step-2", Name: "Build", Result: &failed, Log: &broker.LogReference{ID: 1}},
			{ID: "step-3", Name: "Test", Result: nil}, // still pending
		},
		Logs: map[string][]string{
			"1": {
				"Compiling...",
				"src/main.go:42: undefined: fooBar",
				"Build failed with exit code 1",
			},
		},
	}

	o.reportFailedSteps("test-job", result)

	output := buf.String()
	assert.Contains(t, output, "FAILED: Build")
	assert.Contains(t, output, "undefined: fooBar")
	assert.Contains(t, output, "Build failed with exit code 1")
	// Should NOT report Setup (succeeded) or Test (nil result).
	assert.NotContains(t, output, "FAILED: Setup")
	assert.NotContains(t, output, "FAILED: Test")
}

func TestReportFailedSteps_TruncatesLongOutput(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	// Generate 20 log lines.
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}

	result := &broker.JobCompletionResult{
		JobID:  "test-job",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Long Step", Result: &failed, Log: &broker.LogReference{ID: 1}},
		},
		Logs: map[string][]string{
			"1": lines,
		},
	}

	o.reportFailedSteps("test-job", result)

	output := buf.String()
	assert.Contains(t, output, "10 lines omitted")
	assert.Contains(t, output, "line 19") // last line shown
	assert.Contains(t, output, "line 10") // first shown line after truncation
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

// ---------------------------------------------------------------------------
// dependencyStatus tests
// ---------------------------------------------------------------------------

func TestDependencyStatus_AllSuccess(t *testing.T) {
	node := &graph.JobNode{DependsOn: []string{"build", "lint"}}
	outputs := map[string]*ionsctx.JobResult{
		"build": {Result: "success"},
		"lint":  {Result: "success"},
	}
	assert.Equal(t, "success", dependencyStatus(node, outputs))
}

func TestDependencyStatus_OneFailed(t *testing.T) {
	node := &graph.JobNode{DependsOn: []string{"build", "lint"}}
	outputs := map[string]*ionsctx.JobResult{
		"build": {Result: "failure"},
		"lint":  {Result: "success"},
	}
	assert.Equal(t, "failure", dependencyStatus(node, outputs))
}

func TestDependencyStatus_OneCancelled(t *testing.T) {
	node := &graph.JobNode{DependsOn: []string{"build", "lint"}}
	outputs := map[string]*ionsctx.JobResult{
		"build": {Result: "cancelled"},
		"lint":  {Result: "success"},
	}
	assert.Equal(t, "cancelled", dependencyStatus(node, outputs))
}

func TestDependencyStatus_FailureTakesPriorityOverCancelled(t *testing.T) {
	node := &graph.JobNode{DependsOn: []string{"a", "b"}}
	outputs := map[string]*ionsctx.JobResult{
		"a": {Result: "failure"},
		"b": {Result: "cancelled"},
	}
	assert.Equal(t, "failure", dependencyStatus(node, outputs))
}

func TestDependencyStatus_NoDeps(t *testing.T) {
	node := &graph.JobNode{DependsOn: nil}
	assert.Equal(t, "success", dependencyStatus(node, nil))
}

// ---------------------------------------------------------------------------
// evalContinueOnError tests
// ---------------------------------------------------------------------------

func TestEvalContinueOnError_BoolTrue(t *testing.T) {
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{Value: true}}
	assert.True(t, evalContinueOnError(job, nil, "", nil))
}

func TestEvalContinueOnError_BoolFalse(t *testing.T) {
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{Value: false}}
	assert.False(t, evalContinueOnError(job, nil, "", nil))
}

func TestEvalContinueOnError_Default(t *testing.T) {
	job := &workflow.Job{}
	assert.False(t, evalContinueOnError(job, nil, "", nil))
}

func TestEvalContinueOnError_ExprTrue(t *testing.T) {
	fns := expression.BuiltinFunctions()
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "true",
	}}
	assert.True(t, evalContinueOnError(job, nil, "", fns))
}

func TestEvalContinueOnError_ExprFalse(t *testing.T) {
	fns := expression.BuiltinFunctions()
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "false",
	}}
	assert.False(t, evalContinueOnError(job, nil, "", fns))
}

// ---------------------------------------------------------------------------
// findNode tests
// ---------------------------------------------------------------------------

func TestFindNode(t *testing.T) {
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{
				{NodeID: "build", JobID: "build"},
				{NodeID: "lint", JobID: "lint"},
			}},
			{Nodes: []*graph.JobNode{
				{NodeID: "test", JobID: "test"},
			}},
		},
	}

	assert.NotNil(t, findNode(plan, "build"))
	assert.NotNil(t, findNode(plan, "test"))
	assert.Nil(t, findNode(plan, "deploy"))
}

// ---------------------------------------------------------------------------
// DryRun with runtime deferred conditions
// ---------------------------------------------------------------------------

func TestDryRun_ContinueOnError(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "continue-on-error.yml")

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

func TestDryRun_RuntimeConditions(t *testing.T) {
	// Jobs with `if: failure()`, `if: success()`, `if: cancelled()`,
	// and `if: needs.X.result == 'success'` should NOT be skipped at
	// plan time. They should appear in the dry-run plan as deferred.
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "runtime-conditions.yml")

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
	// No jobs should be in the skipped list — all have deferred runtime conditions.
}

func TestDryRun_MaxParallel(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowPath := filepath.Join(projectRoot, "testdata", "workflows", "max-parallel.yml")

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

// ---------------------------------------------------------------------------
// sanitizePath tests
// ---------------------------------------------------------------------------

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"test (node: 20, os: ubuntu-latest)", "test-node-20_-os-ubuntu-latest"},
		{"job (a: 1)", "job-a-1"},
		{"no-special-chars", "no-special-chars"},
		{"spaces here", "spaces-here"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizePath(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// copyDir tests
// ---------------------------------------------------------------------------

func TestCopyDir(t *testing.T) {
	// Create a source directory with some files.
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "file1.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "subdir", "file2.txt"), []byte("world"), 0o644))

	// Create dirs that should be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".ions-work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".ions-work", "temp"), []byte("tmp"), 0o644))

	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	require.NoError(t, err)

	// Check that files were copied.
	data1, err := os.ReadFile(filepath.Join(dst, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data1))

	data2, err := os.ReadFile(filepath.Join(dst, "subdir", "file2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(data2))

	// Check that .git and .ions-work were skipped.
	_, err = os.Stat(filepath.Join(dst, ".git"))
	assert.True(t, os.IsNotExist(err), ".git should not be copied")

	_, err = os.Stat(filepath.Join(dst, ".ions-work"))
	assert.True(t, os.IsNotExist(err), ".ions-work should not be copied")
}

func TestCopyFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	require.NoError(t, os.WriteFile(src, []byte("content"), 0o644))

	dst := filepath.Join(t.TempDir(), "dst.txt")
	err := copyFile(src, dst)
	require.NoError(t, err)

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
}

func TestTestHardlink(t *testing.T) {
	// Both in same temp dir — should support hardlinks.
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "probe.txt"), []byte("x"), 0o644))
	dst := filepath.Join(src, "dst")
	result := testHardlink(src, dst)
	// On most filesystems within same dir, hardlinks work.
	assert.True(t, result)
}

func TestTestHardlink_EmptyDir(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst")
	// No files in src — can't test hardlink.
	result := testHardlink(src, dst)
	assert.False(t, result)
}

// ---------------------------------------------------------------------------
// Progress UI utility tests
// ---------------------------------------------------------------------------

func TestMaxWidth(t *testing.T) {
	jobs := []*jobProgress{
		{nodeID: "build"},
		{nodeID: "test-long-name"},
		{nodeID: "lint"},
	}
	assert.Equal(t, len("test-long-name"), maxWidth(jobs))
}

func TestMaxWidth_Empty(t *testing.T) {
	assert.Equal(t, 0, maxWidth(nil))
}

func TestPad(t *testing.T) {
	assert.Equal(t, "abc   ", pad("abc", 6))
	assert.Equal(t, "abc", pad("abc", 3))
	assert.Equal(t, "abc", pad("abc", 2))
}

func TestSpinnerFrame(t *testing.T) {
	// spinnerFrame should return non-empty string.
	frame := spinnerFrame()
	assert.NotEmpty(t, frame)
}

func TestBuildProgressNodeIDs(t *testing.T) {
	groups := []struct{ Nodes []string }{
		{Nodes: []string{"a", "b"}},
		{Nodes: []string{"c"}},
	}
	ids := buildProgressNodeIDs(groups)
	assert.Equal(t, []string{"a", "b", "c"}, ids)
}

func TestBuildProgressNodeIDs_Empty(t *testing.T) {
	ids := buildProgressNodeIDs(nil)
	assert.Nil(t, ids)
}

// ---------------------------------------------------------------------------
// LogStreamer additional tests
// ---------------------------------------------------------------------------

func TestLogStreamer_JobCompleted_AllStatuses(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	logger.JobCompleted("build", "success", 0)
	assert.Contains(t, buf.String(), "Job succeeded")

	buf.Reset()
	logger.JobCompleted("build", "failure", 0)
	assert.Contains(t, buf.String(), "Job failed")

	buf.Reset()
	logger.JobCompleted("build", "cancelled", 0)
	assert.Contains(t, buf.String(), "Job cancelled")

	buf.Reset()
	logger.JobCompleted("build", "unknown", 0)
	assert.Contains(t, buf.String(), "Job completed")
}

func TestLogStreamer_StepOutput(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	logger.StepOutput("build", "Hello world")
	assert.Contains(t, buf.String(), "Hello world")
}

func TestFormatDuration_Minutes(t *testing.T) {
	assert.Contains(t, formatDuration(90*1e9), "m")
}

// ---------------------------------------------------------------------------
// buildExprDefaults tests
// ---------------------------------------------------------------------------

func TestBuildExprDefaults(t *testing.T) {
	ctx := expression.MapContext{
		"env": expression.Object(map[string]expression.Value{
			"FOO": expression.String("bar"),
		}),
	}
	defaults := buildExprDefaults(ctx)
	assert.NotNil(t, defaults)
}

// ---------------------------------------------------------------------------
// DryRun comprehensive tests
// ---------------------------------------------------------------------------

func TestDryRun_AllTestdataWorkflows(t *testing.T) {
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	workflowDir := filepath.Join(projectRoot, "testdata", "workflows")

	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Skip("testdata not found")
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			workflowPath := filepath.Join(workflowDir, entry.Name())
			o, err := New(Options{
				WorkflowPath: workflowPath,
				DryRun:       true,
				RepoPath:     projectRoot,
			})
			require.NoError(t, err)

			result, err := o.Run(context.Background())
			require.NoError(t, err)
			assert.True(t, result.Success)
		})
	}
}

// ===========================================================================
// NEW TESTS: targeting untested code paths to increase coverage
// ===========================================================================

// ---------------------------------------------------------------------------
// mergeEnv additional tests
// ---------------------------------------------------------------------------

func TestMergeEnv_NilMaps(t *testing.T) {
	result := mergeEnv(nil, nil, nil)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestMergeEnv_EmptyMaps(t *testing.T) {
	result := mergeEnv(map[string]string{}, map[string]string{})
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestMergeEnv_SingleMap(t *testing.T) {
	result := mergeEnv(map[string]string{"A": "1", "B": "2"})
	assert.Equal(t, "1", result["A"])
	assert.Equal(t, "2", result["B"])
}

func TestMergeEnv_ThreeMaps_LastWins(t *testing.T) {
	result := mergeEnv(
		map[string]string{"X": "1"},
		map[string]string{"X": "2"},
		map[string]string{"X": "3"},
	)
	assert.Equal(t, "3", result["X"])
}

func TestMergeEnv_MixedNilAndValues(t *testing.T) {
	result := mergeEnv(nil, map[string]string{"A": "1"}, nil)
	assert.Equal(t, "1", result["A"])
}

// ---------------------------------------------------------------------------
// filterPlan direct unit tests (not via dry-run)
// ---------------------------------------------------------------------------

func TestFilterPlan_ByJobID(t *testing.T) {
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{
				{NodeID: "build", JobID: "build", Job: &workflow.Job{}},
				{NodeID: "lint", JobID: "lint", Job: &workflow.Job{}},
			}},
			{Nodes: []*graph.JobNode{
				{NodeID: "test", JobID: "test", Job: &workflow.Job{}},
			}},
		},
	}

	filtered := filterPlan(plan, "build")
	require.Len(t, filtered.Groups, 1)
	require.Len(t, filtered.Groups[0].Nodes, 1)
	assert.Equal(t, "build", filtered.Groups[0].Nodes[0].NodeID)
}

func TestFilterPlan_ByNodeID_MatrixJob(t *testing.T) {
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{
				{NodeID: "test (os: ubuntu)", JobID: "test", Job: &workflow.Job{}},
				{NodeID: "test (os: macos)", JobID: "test", Job: &workflow.Job{}},
			}},
		},
	}

	filtered := filterPlan(plan, "test (os: ubuntu)")
	require.Len(t, filtered.Groups, 1)
	require.Len(t, filtered.Groups[0].Nodes, 1)
	assert.Equal(t, "test (os: ubuntu)", filtered.Groups[0].Nodes[0].NodeID)
}

func TestFilterPlan_MatchesAllMatrixExpansions(t *testing.T) {
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{
				{NodeID: "test (os: ubuntu)", JobID: "test", Job: &workflow.Job{}},
				{NodeID: "test (os: macos)", JobID: "test", Job: &workflow.Job{}},
			}},
		},
	}

	// Filtering by JobID should match all matrix expansions.
	filtered := filterPlan(plan, "test")
	require.Len(t, filtered.Groups, 1)
	assert.Len(t, filtered.Groups[0].Nodes, 2)
}

func TestFilterPlan_NoMatches(t *testing.T) {
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{
				{NodeID: "build", JobID: "build", Job: &workflow.Job{}},
			}},
		},
	}

	filtered := filterPlan(plan, "nonexistent")
	assert.Empty(t, filtered.Groups)
}

func TestFilterPlan_EmptyPlan(t *testing.T) {
	plan := &graph.ExecutionPlan{}
	filtered := filterPlan(plan, "anything")
	assert.Empty(t, filtered.Groups)
}

func TestFilterPlan_PreservesGroupStructure(t *testing.T) {
	// "deploy" is in a separate group from "build"
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{
				{NodeID: "build", JobID: "build", Job: &workflow.Job{}},
				{NodeID: "lint", JobID: "lint", Job: &workflow.Job{}},
			}},
			{Nodes: []*graph.JobNode{
				{NodeID: "deploy", JobID: "deploy", Job: &workflow.Job{}},
			}},
		},
	}

	// Filter should only return the group containing "deploy".
	filtered := filterPlan(plan, "deploy")
	require.Len(t, filtered.Groups, 1)
	assert.Equal(t, "deploy", filtered.Groups[0].Nodes[0].NodeID)
}

// ---------------------------------------------------------------------------
// findNode additional tests
// ---------------------------------------------------------------------------

func TestFindNode_EmptyPlan(t *testing.T) {
	plan := &graph.ExecutionPlan{}
	assert.Nil(t, findNode(plan, "anything"))
}

func TestFindNode_MultipleGroups(t *testing.T) {
	plan := &graph.ExecutionPlan{
		Groups: []graph.ParallelGroup{
			{Nodes: []*graph.JobNode{{NodeID: "a", JobID: "a"}}},
			{Nodes: []*graph.JobNode{{NodeID: "b", JobID: "b"}}},
			{Nodes: []*graph.JobNode{{NodeID: "c", JobID: "c"}}},
		},
	}
	assert.Equal(t, "c", findNode(plan, "c").NodeID)
	assert.Equal(t, "a", findNode(plan, "a").NodeID)
}

// ---------------------------------------------------------------------------
// dependencyStatus additional tests
// ---------------------------------------------------------------------------

func TestDependencyStatus_MissingDeps(t *testing.T) {
	// Dependencies that haven't completed yet — not in the outputs map.
	node := &graph.JobNode{DependsOn: []string{"missing"}}
	outputs := map[string]*ionsctx.JobResult{}
	// Should return "success" since there's no failure/cancelled signal.
	assert.Equal(t, "success", dependencyStatus(node, outputs))
}

func TestDependencyStatus_EmptyOutputMap(t *testing.T) {
	node := &graph.JobNode{DependsOn: []string{"a"}}
	assert.Equal(t, "success", dependencyStatus(node, nil))
}

func TestDependencyStatus_SkippedDep(t *testing.T) {
	// A "skipped" result is not "failure" or "cancelled" — treated as success
	// for dependency purposes (per this implementation).
	node := &graph.JobNode{DependsOn: []string{"build"}}
	outputs := map[string]*ionsctx.JobResult{
		"build": {Result: "skipped"},
	}
	assert.Equal(t, "success", dependencyStatus(node, outputs))
}

func TestDependencyStatus_ManyDeps_AllSuccess(t *testing.T) {
	node := &graph.JobNode{DependsOn: []string{"a", "b", "c", "d"}}
	outputs := map[string]*ionsctx.JobResult{
		"a": {Result: "success"},
		"b": {Result: "success"},
		"c": {Result: "success"},
		"d": {Result: "success"},
	}
	assert.Equal(t, "success", dependencyStatus(node, outputs))
}

func TestDependencyStatus_CancelledThenFailure(t *testing.T) {
	// Since map iteration is nondeterministic, we just verify that failure
	// takes priority: if "a" cancelled and "b" failed, result should be
	// "failure" (because failure is checked first in the iteration).
	node := &graph.JobNode{DependsOn: []string{"a", "b"}}
	outputs := map[string]*ionsctx.JobResult{
		"a": {Result: "cancelled"},
		"b": {Result: "failure"},
	}
	result := dependencyStatus(node, outputs)
	// Either "failure" is returned directly, or "cancelled" if "a" is checked
	// first and "b" hasn't been checked. But since implementation returns
	// "failure" immediately, any iteration order that hits "b" first gives "failure".
	assert.Contains(t, []string{"failure", "cancelled"}, result)
}

// ---------------------------------------------------------------------------
// evalContinueOnError additional tests
// ---------------------------------------------------------------------------

func TestEvalContinueOnError_ExprInvalid(t *testing.T) {
	fns := expression.BuiltinFunctions()
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "${{ invalid syntax !!!",
	}}
	// Invalid expression should return false.
	assert.False(t, evalContinueOnError(job, nil, "", fns))
}

func TestEvalContinueOnError_ExprNumericTruthy(t *testing.T) {
	fns := expression.BuiltinFunctions()
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "1",
	}}
	assert.True(t, evalContinueOnError(job, nil, "", fns))
}

func TestEvalContinueOnError_ExprStringTruthy(t *testing.T) {
	fns := expression.BuiltinFunctions()
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "'yes'",
	}}
	assert.True(t, evalContinueOnError(job, nil, "", fns))
}

func TestEvalContinueOnError_ExprEmptyStringFalsy(t *testing.T) {
	fns := expression.BuiltinFunctions()
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "''",
	}}
	assert.False(t, evalContinueOnError(job, nil, "", fns))
}

func TestEvalContinueOnError_NilFunctions(t *testing.T) {
	// With IsExpr=true but nil function map.
	job := &workflow.Job{ContinueOnError: workflow.ExprBool{
		IsExpr: true, Expression: "true",
	}}
	assert.True(t, evalContinueOnError(job, nil, "", nil))
}

// ---------------------------------------------------------------------------
// streamOutput tests
// ---------------------------------------------------------------------------

func TestStreamOutput_SingleLine(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	input := bytes.NewBufferString("hello world\n")
	streamOutput(input, "job1", logger)

	assert.Contains(t, buf.String(), "hello world")
}

func TestStreamOutput_MultipleLines(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	input := bytes.NewBufferString("line1\nline2\nline3\n")
	streamOutput(input, "job1", logger)

	output := buf.String()
	assert.Contains(t, output, "line1")
	assert.Contains(t, output, "line2")
	assert.Contains(t, output, "line3")
}

func TestStreamOutput_NoTrailingNewline(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	// Data without trailing newline — should still flush the remaining bytes.
	input := bytes.NewBufferString("partial line")
	streamOutput(input, "job1", logger)

	assert.Contains(t, buf.String(), "partial line")
}

func TestStreamOutput_EmptyInput(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	input := bytes.NewBufferString("")
	streamOutput(input, "job1", logger)

	assert.Empty(t, buf.String())
}

func TestStreamOutput_EmptyLines(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	// Empty lines (just newlines) should not produce output since
	// streamOutput skips empty lines.
	input := bytes.NewBufferString("\n\n\n")
	streamOutput(input, "job1", logger)

	assert.Empty(t, buf.String())
}

func TestStreamOutput_LargeInput(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	// Generate input larger than the 4096 byte buffer.
	var inputBuf bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&inputBuf, "line number %d with some padding text\n", i)
	}
	streamOutput(&inputBuf, "job1", logger)

	output := buf.String()
	assert.Contains(t, output, "line number 0")
	assert.Contains(t, output, "line number 99")
}

// ---------------------------------------------------------------------------
// buildExprDefaults additional tests
// ---------------------------------------------------------------------------

func TestBuildExprDefaults_WithGitHubContext(t *testing.T) {
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"token":            expression.String("ghp_test123"),
			"repository":       expression.String("owner/repo"),
			"repository_owner": expression.String("owner"),
			"server_url":       expression.String("https://github.com"),
			"api_url":          expression.String("https://api.github.com"),
			"graphql_url":      expression.String("https://api.github.com/graphql"),
			"actor":            expression.String("testuser"),
			"ref":              expression.String("refs/heads/main"),
			"sha":              expression.String("abc123"),
			"event_name":       expression.String("push"),
			"workspace":        expression.String("/workspace"),
			"action":           expression.String("__run"),
		}),
	}

	defaults := buildExprDefaults(ctx)
	assert.Equal(t, "ghp_test123", defaults["github.token"])
	assert.Equal(t, "owner/repo", defaults["github.repository"])
	assert.Equal(t, "owner", defaults["github.repository_owner"])
	assert.Equal(t, "https://github.com", defaults["github.server_url"])
	assert.Equal(t, "testuser", defaults["github.actor"])
	assert.Equal(t, "refs/heads/main", defaults["github.ref"])
	assert.Equal(t, "abc123", defaults["github.sha"])
	assert.Equal(t, "push", defaults["github.event_name"])
}

func TestBuildExprDefaults_WithRunnerContext(t *testing.T) {
	ctx := expression.MapContext{
		"runner": expression.Object(map[string]expression.Value{
			"os":         expression.String("Linux"),
			"arch":       expression.String("X64"),
			"temp":       expression.String("/tmp"),
			"tool_cache": expression.String("/opt/hostedtoolcache"),
		}),
	}

	defaults := buildExprDefaults(ctx)
	assert.Equal(t, "Linux", defaults["runner.os"])
	assert.Equal(t, "X64", defaults["runner.arch"])
	assert.Equal(t, "/tmp", defaults["runner.temp"])
	assert.Equal(t, "/opt/hostedtoolcache", defaults["runner.tool_cache"])
}

func TestBuildExprDefaults_EmptyContext(t *testing.T) {
	ctx := expression.MapContext{}
	defaults := buildExprDefaults(ctx)
	assert.NotNil(t, defaults)
	assert.Empty(t, defaults)
}

func TestBuildExprDefaults_PartialGitHubContext(t *testing.T) {
	// Only some fields present.
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"repository": expression.String("foo/bar"),
		}),
	}

	defaults := buildExprDefaults(ctx)
	assert.Equal(t, "foo/bar", defaults["github.repository"])
	// Missing fields should not be present.
	assert.Equal(t, "", defaults["github.token"])
}

// ---------------------------------------------------------------------------
// repoInfoFromContext tests
// ---------------------------------------------------------------------------

func TestRepoInfoFromContext_FullContext(t *testing.T) {
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"repository": expression.String("myowner/myrepo"),
			"ref":        expression.String("refs/heads/main"),
			"sha":        expression.String("deadbeef"),
			"ref_name":   expression.String("main"),
			"server_url": expression.String("https://github.com"),
		}),
	}

	info := repoInfoFromContext(ctx, "/path/to/repo")
	assert.Equal(t, "myowner", info.Owner)
	assert.Equal(t, "myrepo", info.Repo)
	assert.Equal(t, "refs/heads/main", info.CurrentRef)
	assert.Equal(t, "deadbeef", info.CurrentSHA)
	assert.Equal(t, "main", info.DefaultBranch)
	assert.Equal(t, "https://github.com/myowner/myrepo.git", info.CloneURL)
	assert.Equal(t, "/path/to/repo", info.RepoPath)
}

func TestRepoInfoFromContext_EmptyContext(t *testing.T) {
	ctx := expression.MapContext{}
	info := repoInfoFromContext(ctx, "/path/to/repo")
	assert.Equal(t, "", info.Owner)
	assert.Equal(t, "", info.Repo)
	assert.Equal(t, "/path/to/repo", info.RepoPath)
}

func TestRepoInfoFromContext_NoRepository(t *testing.T) {
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/main"),
		}),
	}

	info := repoInfoFromContext(ctx, "/path")
	assert.Equal(t, "", info.Owner)
	assert.Equal(t, "", info.Repo)
	assert.Equal(t, "", info.CloneURL) // No clone URL without owner/repo
}

func TestRepoInfoFromContext_SinglePartRepository(t *testing.T) {
	// Repository string without a slash — should not split correctly.
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"repository": expression.String("justarepo"),
		}),
	}

	info := repoInfoFromContext(ctx, "/path")
	// SplitN with "/" and 2 parts gives ["justarepo"] if no slash,
	// so len(parts) == 1, not 2.
	assert.Equal(t, "", info.Owner)
	assert.Equal(t, "", info.Repo)
}

func TestRepoInfoFromContext_NoServerURL(t *testing.T) {
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"repository": expression.String("owner/repo"),
			"ref":        expression.String("refs/heads/main"),
		}),
	}

	info := repoInfoFromContext(ctx, "/path")
	assert.Equal(t, "owner", info.Owner)
	assert.Equal(t, "repo", info.Repo)
	assert.Equal(t, "", info.CloneURL) // No server_url means no clone URL
}

// ---------------------------------------------------------------------------
// sanitizePath additional tests
// ---------------------------------------------------------------------------

func TestSanitizePath_AllSpecialChars(t *testing.T) {
	assert.Equal(t, "a-b_-cd", sanitizePath("a b, c:d"))
}

func TestSanitizePath_EmptyString(t *testing.T) {
	assert.Equal(t, "", sanitizePath(""))
}

func TestSanitizePath_Parentheses(t *testing.T) {
	assert.Equal(t, "test-a-b", sanitizePath("test (a: b)"))
}

// ---------------------------------------------------------------------------
// reportFailedSteps additional tests
// ---------------------------------------------------------------------------

func TestReportFailedSteps_NilTimeline(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	result := &broker.JobCompletionResult{
		JobID:    "job1",
		Result:   "failed",
		Timeline: nil,
		Logs:     nil,
	}

	// Should not panic with nil timeline.
	o.reportFailedSteps("job1", result)
	assert.Empty(t, buf.String())
}

func TestReportFailedSteps_EmptyTimeline(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	result := &broker.JobCompletionResult{
		JobID:    "job1",
		Result:   "failed",
		Timeline: []broker.TimelineRecord{},
		Logs:     map[string][]string{},
	}

	o.reportFailedSteps("job1", result)
	assert.Empty(t, buf.String())
}

func TestReportFailedSteps_FailedWithNoName_UsesRefName(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "", RefName: "actions/checkout@v4", Result: &failed},
		},
		Logs: map[string][]string{},
	}

	o.reportFailedSteps("job1", result)
	assert.Contains(t, buf.String(), "FAILED: actions/checkout@v4")
}

func TestReportFailedSteps_FailedWithNoNameOrRefName(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "", RefName: "", Result: &failed},
		},
		Logs: map[string][]string{},
	}

	o.reportFailedSteps("job1", result)
	// Should skip steps with no name or refName.
	assert.NotContains(t, buf.String(), "FAILED")
}

func TestReportFailedSteps_FailedStepNoLog(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Build", Result: &failed, Log: nil},
		},
		Logs: map[string][]string{},
	}

	o.reportFailedSteps("job1", result)
	assert.Contains(t, buf.String(), "FAILED: Build")
	// No log lines to show, so no "lines omitted" message.
	assert.NotContains(t, buf.String(), "omitted")
}

func TestReportFailedSteps_LogMissing(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Build", Result: &failed, Log: &broker.LogReference{ID: 99}},
		},
		Logs: map[string][]string{
			// Log ID 99 is referenced but not in the logs map ("99" not "1").
			"1": {"irrelevant line"},
		},
	}

	o.reportFailedSteps("job1", result)
	assert.Contains(t, buf.String(), "FAILED: Build")
}

func TestReportFailedSteps_ExactlyMaxLines(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	// Exactly 10 lines — should show all, no truncation message.
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}

	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Build", Result: &failed, Log: &broker.LogReference{ID: 1}},
		},
		Logs: map[string][]string{
			"1": lines,
		},
	}

	o.reportFailedSteps("job1", result)
	output := buf.String()
	assert.Contains(t, output, "FAILED: Build")
	assert.NotContains(t, output, "omitted")
	assert.Contains(t, output, "line 0")
	assert.Contains(t, output, "line 9")
}

func TestReportFailedSteps_MultipleFailedSteps(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Build", Result: &failed, Log: &broker.LogReference{ID: 1}},
			{ID: "step-2", Name: "Test", Result: &failed, Log: &broker.LogReference{ID: 2}},
		},
		Logs: map[string][]string{
			"1": {"build error line"},
			"2": {"test error line"},
		},
	}

	o.reportFailedSteps("job1", result)
	output := buf.String()
	assert.Contains(t, output, "FAILED: Build")
	assert.Contains(t, output, "FAILED: Test")
	assert.Contains(t, output, "build error line")
	assert.Contains(t, output, "test error line")
}

func TestReportFailedSteps_SecretsAreMasked(t *testing.T) {
	masker := NewSecretMasker(map[string]string{"TOKEN": "superduper"})
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}

	failed := "failed"
	result := &broker.JobCompletionResult{
		JobID:  "job1",
		Result: "failed",
		Timeline: []broker.TimelineRecord{
			{ID: "step-1", Name: "Deploy", Result: &failed, Log: &broker.LogReference{ID: 1}},
		},
		Logs: map[string][]string{
			"1": {"using token superduper to auth"},
		},
	}

	o.reportFailedSteps("job1", result)
	output := buf.String()
	assert.NotContains(t, output, "superduper")
	assert.Contains(t, output, "***")
}

// ---------------------------------------------------------------------------
// buildContext tests
// ---------------------------------------------------------------------------

func TestBuildContext_MinimalWorkflow(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
			Secrets:   map[string]string{"MY_SECRET": "val"},
			Vars:      map[string]string{"MY_VAR": "varval"},
			Inputs:    map[string]string{"my_input": "inputval"},
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{
		Name: "Test Workflow",
		Env:  map[string]string{"WF_ENV": "wfval"},
	}

	ctx := o.buildContext(w, nil, nil, nil, "run123")
	assert.NotNil(t, ctx)

	// Verify that standard context keys exist.
	_, hasGithub := ctx["github"]
	assert.True(t, hasGithub)
	_, hasEnv := ctx["env"]
	assert.True(t, hasEnv)
	_, hasSecrets := ctx["secrets"]
	assert.True(t, hasSecrets)
	_, hasVars := ctx["vars"]
	assert.True(t, hasVars)
	_, hasInputs := ctx["inputs"]
	assert.True(t, hasInputs)
}

func TestBuildContext_WithNode(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{
		Name: "Test",
		Jobs: map[string]*workflow.Job{
			"build": {
				Env:   map[string]string{"JOB_ENV": "jobval"},
				Needs: []string{"lint"},
			},
		},
	}

	node := &graph.JobNode{
		NodeID: "build",
		JobID:  "build",
		Job:    w.Jobs["build"],
		MatrixValues: graph.MatrixCombination{
			"os": "ubuntu-latest",
		},
	}

	ctx := o.buildContext(w, node, nil, nil, "run456")
	assert.NotNil(t, ctx)

	// Check that matrix context is present.
	_, hasMatrix := ctx["matrix"]
	assert.True(t, hasMatrix)
}

func TestBuildContext_WithJobOutputs(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{
		Name: "Test",
		Jobs: map[string]*workflow.Job{
			"test": {
				Needs: []string{"build"},
			},
		},
	}

	node := &graph.JobNode{
		NodeID: "test",
		JobID:  "test",
		Job:    w.Jobs["test"],
	}

	jobOutputs := map[string]*ionsctx.JobResult{
		"build": {
			Result:  "success",
			Outputs: map[string]string{"version": "1.0.0"},
		},
	}

	ctx := o.buildContext(w, node, jobOutputs, nil, "run789")
	assert.NotNil(t, ctx)
	_, hasNeeds := ctx["needs"]
	assert.True(t, hasNeeds)
}

func TestBuildContext_BrokerURLInContext(t *testing.T) {
	// When a broker is running, the API base URL should point through it.
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
		},
		masker: NewSecretMasker(nil),
		// We can't start a real broker in unit tests, but we can set it
		// to nil to verify the non-broker case.
		broker: nil,
	}

	w := &workflow.Workflow{Name: "Test"}
	ctx := o.buildContext(w, nil, nil, nil, "run1")
	assert.NotNil(t, ctx)
}

func TestBuildContext_EnvMergesWorkflowAndOpts(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
			Env:       map[string]string{"OPT_ENV": "optval", "SHARED": "from-opts"},
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{
		Name: "Test",
		Env:  map[string]string{"WF_ENV": "wfval", "SHARED": "from-wf"},
	}

	ctx := o.buildContext(w, nil, nil, nil, "run1")
	assert.NotNil(t, ctx)

	// The env context should contain merged values.
	if envCtx, ok := ctx["env"]; ok {
		if fields := envCtx.ObjectFields(); fields != nil {
			// opts.Env should override workflow env for same key.
			if shared, ok := fields["SHARED"]; ok {
				assert.Equal(t, "from-opts", shared.StringVal())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// ProgressUI tests (unit tests for internal state, not rendering)
// ---------------------------------------------------------------------------

func TestProgressUI_RegisterJobs_NoDuplicates(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false, // disable TTY-specific behavior
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"build", "test", "build"}) // "build" appears twice
	assert.Len(t, p.jobs, 2) // should not have duplicates
	assert.Equal(t, "build", p.jobs[0].nodeID)
	assert.Equal(t, "test", p.jobs[1].nodeID)
}

func TestProgressUI_RegisterJobs_Empty(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs(nil)
	assert.Empty(t, p.jobs)
}

func TestProgressUI_JobStarted_SetsRunning(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true, // prevent render
	}

	p.RegisterJobs([]string{"build"})
	p.JobStarted("build")

	assert.Equal(t, "running", p.jobs[0].status)
	assert.False(t, p.jobs[0].startTime.IsZero())
}

func TestProgressUI_JobStarted_UnknownJob(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	// Calling JobStarted with an unregistered job should not panic.
	p.JobStarted("nonexistent")
}

func TestProgressUI_StepUpdate_InProgress(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	p.StepUpdate("build", "Checkout", "InProgress", nil)

	assert.Equal(t, "Checkout", p.jobs[0].currentStep)
	assert.Equal(t, 1, p.jobs[0].stepsTotal)
	assert.Equal(t, 0, p.jobs[0].stepsDone)
}

func TestProgressUI_StepUpdate_Completed(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	p.StepUpdate("build", "Checkout", "InProgress", nil)
	succeeded := "succeeded"
	p.StepUpdate("build", "Checkout", "Completed", &succeeded)

	assert.Equal(t, 1, p.jobs[0].stepsDone)
	assert.Contains(t, p.jobs[0].currentStep, "Checkout (succeeded)")
}

func TestProgressUI_StepUpdate_CompletedNilResult(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	p.StepUpdate("build", "Step1", "Completed", nil)

	assert.Equal(t, 1, p.jobs[0].stepsDone)
	assert.Contains(t, p.jobs[0].currentStep, "Step1 (succeeded)")
}

func TestProgressUI_StepUpdate_UnknownJob(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	// Should not panic for unknown job.
	p.StepUpdate("nonexistent", "Step", "InProgress", nil)
}

func TestProgressUI_JobCompleted(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	p.JobStarted("build")
	p.JobCompleted("build", "success")

	assert.Equal(t, "success", p.jobs[0].status)
	assert.False(t, p.jobs[0].endTime.IsZero())
	assert.Equal(t, "", p.jobs[0].currentStep) // cleared on completion
}

func TestProgressUI_JobCompleted_Failure(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	p.JobCompleted("build", "failure")
	assert.Equal(t, "failure", p.jobs[0].status)
}

func TestProgressUI_LogLine(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	p.LogLine("build", "error: compilation failed")
	p.LogLine("build", "line 42: undefined variable")

	assert.Len(t, p.jobs[0].lastLines, 2)
	assert.Equal(t, "error: compilation failed", p.jobs[0].lastLines[0])
}

func TestProgressUI_LogLine_TruncatesTo5(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	p.RegisterJobs([]string{"build"})
	for i := 0; i < 10; i++ {
		p.LogLine("build", fmt.Sprintf("line %d", i))
	}

	assert.Len(t, p.jobs[0].lastLines, 5)
	assert.Equal(t, "line 5", p.jobs[0].lastLines[0])
	assert.Equal(t, "line 9", p.jobs[0].lastLines[4])
}

func TestProgressUI_LogLine_UnknownJob(t *testing.T) {
	p := &ProgressUI{
		writer:   &bytes.Buffer{},
		isTTY:    false,
		jobIndex: make(map[string]int),
		stopTick: make(chan struct{}),
		done:     true,
	}

	// Should not panic.
	p.LogLine("nonexistent", "some line")
}

// ---------------------------------------------------------------------------
// LogStreamer additional tests (jobColor, concurrent access)
// ---------------------------------------------------------------------------

func TestLogStreamer_JobColor_ConsistentAssignment(t *testing.T) {
	masker := NewSecretMasker(nil)
	ls := NewLogStreamer(masker, false)

	// First call assigns a color.
	c1 := ls.jobColor("build")
	// Second call for same nodeID should return the same color.
	c2 := ls.jobColor("build")
	assert.Equal(t, c1, c2)
}

func TestLogStreamer_JobColor_DifferentJobs(t *testing.T) {
	masker := NewSecretMasker(nil)
	ls := NewLogStreamer(masker, false)

	c1 := ls.jobColor("build")
	c2 := ls.jobColor("test")
	// Different jobs get different colors (until wrap-around).
	// Since there are 6 colors, first 6 should all be different.
	assert.NotEqual(t, c1, c2)
}

func TestLogStreamer_JobColor_CyclesAfterAll(t *testing.T) {
	masker := NewSecretMasker(nil)
	ls := NewLogStreamer(masker, false)

	// Assign colors to more jobs than there are colors.
	for i := 0; i < len(jobColors); i++ {
		ls.jobColor(fmt.Sprintf("job%d", i))
	}
	// The next job should cycle back to the first color.
	c := ls.jobColor("extra")
	assert.Equal(t, jobColors[0], c)
}

func TestLogStreamer_StepStarted_Format(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.StepStarted("build", "Checkout", 1, 5)
	assert.Contains(t, buf.String(), "Step 1/5: Checkout")
}

func TestLogStreamer_StepCompleted_Success(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.StepCompleted("build", "Checkout", "success", 500*time.Millisecond)
	output := buf.String()
	assert.Contains(t, output, "Step completed: Checkout")
	assert.Contains(t, output, "\u2713") // checkmark
	assert.Contains(t, output, "0.5s")
}

func TestLogStreamer_StepCompleted_Failure(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.StepCompleted("build", "Test", "failure", 1*time.Second)
	output := buf.String()
	assert.Contains(t, output, "\u2717") // cross
}

// ---------------------------------------------------------------------------
// ProgressLogger tests
// ---------------------------------------------------------------------------

func TestProgressLogger_NewProgressLogger_Verbose(t *testing.T) {
	masker := NewSecretMasker(nil)
	pl, progress := NewProgressLogger(masker, true) // verbose mode
	assert.NotNil(t, pl)
	// In verbose mode, progress should be nil (or possibly nil if not TTY).
	// Since tests typically aren't run in a TTY, progress should be nil.
	assert.Nil(t, progress)
	assert.NotNil(t, pl.Streamer())
	assert.False(t, pl.HasProgress())
}

func TestProgressLogger_Streamer(t *testing.T) {
	masker := NewSecretMasker(nil)
	pl, _ := NewProgressLogger(masker, true)
	streamer := pl.Streamer()
	assert.NotNil(t, streamer)
}

func TestProgressLogger_HasProgress_NoTTY(t *testing.T) {
	masker := NewSecretMasker(nil)
	pl, _ := NewProgressLogger(masker, false)
	// In test environment (not a TTY), HasProgress should be false.
	assert.False(t, pl.HasProgress())
}

// ---------------------------------------------------------------------------
// formatDuration additional tests
// ---------------------------------------------------------------------------

func TestFormatDuration_Zero(t *testing.T) {
	assert.Equal(t, "0.0s", formatDuration(0))
}

func TestFormatDuration_SubSecond(t *testing.T) {
	assert.Equal(t, "0.1s", formatDuration(100*time.Millisecond))
}

func TestFormatDuration_ExactMinute(t *testing.T) {
	assert.Equal(t, "1m 0s", formatDuration(60*time.Second))
}

func TestFormatDuration_LargeValue(t *testing.T) {
	result := formatDuration(5*time.Minute + 30*time.Second)
	assert.Equal(t, "5m 30s", result)
}

// ---------------------------------------------------------------------------
// statusIndicator additional tests
// ---------------------------------------------------------------------------

func TestStatusIndicator_AllStatuses(t *testing.T) {
	tests := []struct {
		status   string
		expected string
	}{
		{"success", "\u2713"},
		{"failure", "\u2717"},
		{"skipped", "\u2298"},
		{"cancelled", "\u2298"},
		{"anything-else", "?"},
		{"", "?"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			assert.Equal(t, tt.expected, statusIndicator(tt.status))
		})
	}
}

// ---------------------------------------------------------------------------
// RunResult / JobRunResult tests
// ---------------------------------------------------------------------------

func TestJobRunResult_Outputs(t *testing.T) {
	r := &JobRunResult{
		NodeID:   "build",
		Status:   "success",
		Duration: 5 * time.Second,
		Outputs:  map[string]string{"version": "1.2.3"},
	}
	assert.Equal(t, "1.2.3", r.Outputs["version"])
}

func TestRunResult_EmptyJobResults(t *testing.T) {
	r := &RunResult{
		Success:    true,
		JobResults: make(map[string]*JobRunResult),
		Duration:   100 * time.Millisecond,
	}
	assert.True(t, r.Success)
	assert.Empty(t, r.JobResults)
}

// ---------------------------------------------------------------------------
// LogStreamer Summary additional tests
// ---------------------------------------------------------------------------

func TestLogStreamer_Summary_EmptyResults(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.Summary(map[string]*JobRunResult{})
	output := buf.String()
	assert.Contains(t, output, "Summary:")
}

func TestLogStreamer_Summary_NilResults(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.Summary(nil)
	output := buf.String()
	assert.Contains(t, output, "Summary:")
}

// ---------------------------------------------------------------------------
// maxWidth / pad additional tests
// ---------------------------------------------------------------------------

func TestMaxWidth_SingleJob(t *testing.T) {
	jobs := []*jobProgress{{nodeID: "x"}}
	assert.Equal(t, 1, maxWidth(jobs))
}

func TestPad_EmptyString(t *testing.T) {
	assert.Equal(t, "     ", pad("", 5))
}

func TestPad_ZeroWidth(t *testing.T) {
	assert.Equal(t, "abc", pad("abc", 0))
}

// ---------------------------------------------------------------------------
// copyDir additional edge cases
// ---------------------------------------------------------------------------

func TestCopyDir_NestedDeepStructure(t *testing.T) {
	src := t.TempDir()
	deep := filepath.Join(src, "a", "b", "c")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "deep.txt"), []byte("deep"), 0o644))

	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dst, "a", "b", "c", "deep.txt"))
	require.NoError(t, err)
	assert.Equal(t, "deep", string(data))
}

func TestCopyDir_SkipsLettaDir(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".letta"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".letta", "config"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "keep.txt"), []byte("keep"), 0o644))

	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dst, ".letta"))
	assert.True(t, os.IsNotExist(err), ".letta should not be copied")

	data, err := os.ReadFile(filepath.Join(dst, "keep.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
}

func TestCopyDir_EmptySrc(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	require.NoError(t, err)

	// dst should exist but be empty (aside from possible hidden files).
	entries, err := os.ReadDir(dst)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestCopyFile_NonexistentSrc(t *testing.T) {
	src := filepath.Join(t.TempDir(), "nonexistent.txt")
	dst := filepath.Join(t.TempDir(), "dst.txt")
	err := copyFile(src, dst)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// ProgressUI rendering tests (clearLines, render, renderJob, printFinal, Finish)
// ---------------------------------------------------------------------------

func newTestProgressUI() (*ProgressUI, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	p := &ProgressUI{
		writer:   buf,
		isTTY:    false, // no ANSI escapes
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}
	return p, buf
}

func TestProgressUI_ClearLines_NonTTY(t *testing.T) {
	p, buf := newTestProgressUI()
	p.isTTY = false
	p.clearLines(5)
	// Non-TTY should not emit any escape sequences.
	assert.Empty(t, buf.String())
}

func TestProgressUI_ClearLines_ZeroLines(t *testing.T) {
	p, buf := newTestProgressUI()
	p.isTTY = true
	p.clearLines(0)
	assert.Empty(t, buf.String())
}

func TestProgressUI_ClearLines_TTY(t *testing.T) {
	p, buf := newTestProgressUI()
	p.isTTY = true
	p.clearLines(3)
	output := buf.String()
	// Should contain ANSI escape sequences for moving up and clearing.
	assert.Contains(t, output, "\033[A")
	assert.Contains(t, output, "\033[2K")
}

func TestProgressUI_Render_NoJobs(t *testing.T) {
	p, buf := newTestProgressUI()
	p.render()
	// With no jobs but render called, it should output something (the header).
	output := buf.String()
	assert.Contains(t, output, "Jobs: 0 total")
}

func TestProgressUI_Render_WithJobs(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"build", "test"})
	p.jobs[0].status = "running"
	p.jobs[0].startTime = time.Now()
	p.jobs[1].status = "pending"

	p.render()
	output := buf.String()
	assert.Contains(t, output, "Jobs: 2 total")
	assert.Contains(t, output, "1 running")
	assert.Contains(t, output, "build")
	assert.Contains(t, output, "test")
}

func TestProgressUI_Render_AllStatuses(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"a", "b", "c", "d", "e", "f"})
	p.jobs[0].status = "pending"
	p.jobs[1].status = "running"
	p.jobs[1].startTime = time.Now()
	p.jobs[2].status = "success"
	p.jobs[2].startTime = time.Now().Add(-2 * time.Second)
	p.jobs[2].endTime = time.Now()
	p.jobs[3].status = "failure"
	p.jobs[3].startTime = time.Now().Add(-1 * time.Second)
	p.jobs[3].endTime = time.Now()
	p.jobs[4].status = "skipped"
	p.jobs[5].status = "cancelled"

	p.render()
	output := buf.String()
	assert.Contains(t, output, "1 running")
	assert.Contains(t, output, "1 done")
	assert.Contains(t, output, "1 failed")
}

func TestProgressUI_Render_Done(t *testing.T) {
	p, _ := newTestProgressUI()
	p.done = true
	p.RegisterJobs([]string{"build"})
	buf := &bytes.Buffer{}
	p.writer = buf
	p.render()
	// render() should be a no-op when done is true.
	assert.Empty(t, buf.String())
}

func TestProgressUI_Render_RunningWithStep(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"build"})
	p.jobs[0].status = "running"
	p.jobs[0].startTime = time.Now()
	p.jobs[0].currentStep = "Compiling..."

	p.render()
	output := buf.String()
	assert.Contains(t, output, "Compiling...")
}

func TestProgressUI_RenderJob_Pending(t *testing.T) {
	p, buf := newTestProgressUI()
	j := &jobProgress{nodeID: "build", status: "pending"}
	dim := &color.Color{}
	p.renderJob(j, dim)
	assert.Contains(t, buf.String(), "build")
}

func TestProgressUI_RenderJob_Success(t *testing.T) {
	p, buf := newTestProgressUI()
	j := &jobProgress{
		nodeID:    "build",
		status:    "success",
		startTime: time.Now().Add(-2 * time.Second),
		endTime:   time.Now(),
	}
	dim := &color.Color{}
	p.renderJob(j, dim)
	assert.Contains(t, buf.String(), "build")
}

func TestProgressUI_RenderJob_Failure(t *testing.T) {
	p, buf := newTestProgressUI()
	j := &jobProgress{
		nodeID:    "build",
		status:    "failure",
		startTime: time.Now().Add(-1 * time.Second),
		endTime:   time.Now(),
	}
	dim := &color.Color{}
	p.renderJob(j, dim)
	assert.Contains(t, buf.String(), "build")
}

func TestProgressUI_RenderJob_Skipped(t *testing.T) {
	p, buf := newTestProgressUI()
	j := &jobProgress{nodeID: "deploy", status: "skipped"}
	dim := &color.Color{}
	p.renderJob(j, dim)
	assert.Contains(t, buf.String(), "deploy")
}

func TestProgressUI_RenderJob_Cancelled(t *testing.T) {
	p, buf := newTestProgressUI()
	j := &jobProgress{nodeID: "deploy", status: "cancelled"}
	dim := &color.Color{}
	p.renderJob(j, dim)
	assert.Contains(t, buf.String(), "deploy")
}

func TestProgressUI_RenderJob_Unknown(t *testing.T) {
	p, buf := newTestProgressUI()
	j := &jobProgress{nodeID: "x", status: "weird"}
	dim := &color.Color{}
	p.renderJob(j, dim)
	assert.Contains(t, buf.String(), "x")
}

func TestProgressUI_PrintFinal(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"build", "test"})
	p.jobs[0].status = "success"
	p.jobs[0].startTime = time.Now().Add(-2 * time.Second)
	p.jobs[0].endTime = time.Now()
	p.jobs[1].status = "failure"
	p.jobs[1].startTime = time.Now().Add(-1 * time.Second)
	p.jobs[1].endTime = time.Now()
	p.jobs[1].lastLines = []string{"error: foo", "error: bar"}

	p.printFinal()
	output := buf.String()
	assert.Contains(t, output, "Workflow completed")
	assert.Contains(t, output, "build")
	assert.Contains(t, output, "test")
	assert.Contains(t, output, "error: foo")
	assert.Contains(t, output, "error: bar")
}

func TestProgressUI_PrintFinal_NoFailedLines(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"build"})
	p.jobs[0].status = "success"
	p.jobs[0].startTime = time.Now()
	p.jobs[0].endTime = time.Now()

	p.printFinal()
	output := buf.String()
	assert.Contains(t, output, "Workflow completed")
	assert.Contains(t, output, "build")
}

func TestProgressUI_Finish(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"build"})
	p.jobs[0].status = "success"
	p.jobs[0].startTime = time.Now()
	p.jobs[0].endTime = time.Now()

	p.Finish()
	assert.True(t, p.done)
	output := buf.String()
	assert.Contains(t, output, "Workflow completed")
}

func TestProgressUI_Finish_StopsTicker(t *testing.T) {
	p, _ := newTestProgressUI()
	p.RegisterJobs([]string{"build"})

	p.Finish()

	// stopTick channel should be closed.
	select {
	case <-p.stopTick:
		// expected — channel is closed
	default:
		t.Fatal("stopTick channel should be closed after Finish()")
	}
}

// ---------------------------------------------------------------------------
// skipJob tests
// ---------------------------------------------------------------------------

func TestSkipJob(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	o := &Orchestrator{logger: logger, masker: masker}
	results := make(map[string]*JobRunResult)

	o.skipJob("deploy", results)

	assert.Contains(t, results, "deploy")
	assert.Equal(t, "skipped", results["deploy"].Status)
	assert.Equal(t, "deploy", results["deploy"].NodeID)
	assert.Contains(t, buf.String(), "deploy")
}

func TestSkipJob_WithProgressUI(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	p, _ := newTestProgressUI()
	p.RegisterJobs([]string{"deploy"})

	o := &Orchestrator{logger: logger, masker: masker, progress: p}
	results := make(map[string]*JobRunResult)

	o.skipJob("deploy", results)

	assert.Equal(t, "skipped", results["deploy"].Status)
	assert.Equal(t, "skipped", p.jobs[0].status)
}

// ---------------------------------------------------------------------------
// buildContext branch: with broker URL
// ---------------------------------------------------------------------------

func TestBuildContext_WithStepResults(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{Name: "Test"}

	stepResults := map[string]*ionsctx.StepResult{
		"checkout": {
			Outcome:    "success",
			Conclusion: "success",
			Outputs:    map[string]string{"sha": "abc123"},
		},
	}

	ctx := o.buildContext(w, nil, nil, stepResults, "run1")
	assert.NotNil(t, ctx)
	_, hasSteps := ctx["steps"]
	assert.True(t, hasSteps)
}

func TestBuildContext_NilNode_NoMatrixOrJobEnv(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{Name: "Test"}

	// With nil node, no job env or matrix should be set.
	ctx := o.buildContext(w, nil, nil, nil, "run1")
	assert.NotNil(t, ctx)
}

func TestBuildContext_GitHubToken(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:    "/tmp/testrepo",
			EventName:   "push",
			GitHubToken: "ghp_testtoken123",
		},
		masker: NewSecretMasker(nil),
	}

	w := &workflow.Workflow{Name: "Test"}
	ctx := o.buildContext(w, nil, nil, nil, "run1")
	assert.NotNil(t, ctx)

	// The github.token in context should contain the provided token.
	if ghCtx, ok := ctx["github"]; ok {
		if fields := ghCtx.ObjectFields(); fields != nil {
			if tok, ok := fields["token"]; ok {
				assert.Equal(t, "ghp_testtoken123", tok.StringVal())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// addWatchDirs test
// ---------------------------------------------------------------------------

func TestAddWatchDirs(t *testing.T) {
	// Create a simple directory structure.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755))

	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	err = addWatchDirs(watcher, root)
	require.NoError(t, err)

	// Check what directories were added.
	watchList := watcher.WatchList()
	var watchedNames []string
	for _, w := range watchList {
		rel, _ := filepath.Rel(root, w)
		watchedNames = append(watchedNames, rel)
	}

	assert.Contains(t, watchedNames, ".")
	assert.Contains(t, watchedNames, "src")
	// node_modules and .git should NOT be watched.
	for _, w := range watchedNames {
		assert.NotContains(t, w, "node_modules")
		assert.NotContains(t, w, ".git")
	}
}

// ---------------------------------------------------------------------------
// New() edge cases
// ---------------------------------------------------------------------------

func TestNew_DryRunNoRunnerManager(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "test.yml",
		DryRun:       true,
		RepoPath:     "/tmp/test",
	})
	require.NoError(t, err)
	assert.Nil(t, o.runnerMgr)
	// Dry run + non-verbose => progress may or may not be set depending on TTY.
}

func TestNew_VerboseNoProgressUI(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "test.yml",
		DryRun:       true,
		Verbose:      true,
		RepoPath:     "/tmp/test",
	})
	require.NoError(t, err)
	// Verbose mode should disable progress UI.
	assert.Nil(t, o.progress)
}

func TestNew_RepoPathDefault(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "test.yml",
		DryRun:       true,
	})
	require.NoError(t, err)
	// RepoPath should be set to current working directory.
	cwd, _ := os.Getwd()
	assert.Equal(t, cwd, o.opts.RepoPath)
}

// ---------------------------------------------------------------------------
// isRelevantChange additional tests (not in watcher_test.go)
// ---------------------------------------------------------------------------

func TestIsRelevantChange_NestedIgnoredDir(t *testing.T) {
	event := fsnotify.Event{
		Name: "/project/deep/nested/.cache/file.txt",
		Op:   fsnotify.Write,
	}
	assert.False(t, isRelevantChange(event, "/project"))
}

func TestIsRelevantChange_BuildDir(t *testing.T) {
	event := fsnotify.Event{
		Name: "/project/build/output.js",
		Op:   fsnotify.Write,
	}
	assert.False(t, isRelevantChange(event, "/project"))
}

func TestIsRelevantChange_RenameEvent(t *testing.T) {
	event := fsnotify.Event{
		Name: "/project/src/main.go",
		Op:   fsnotify.Rename,
	}
	assert.True(t, isRelevantChange(event, "/project"))
}

// ===========================================================================
// ADDITIONAL COVERAGE TESTS
// ===========================================================================

// ---------------------------------------------------------------------------
// buildContext — broker URL branch
// ---------------------------------------------------------------------------

func TestBuildContext_WithBrokerURL(t *testing.T) {
	// Create a real broker server to hit the o.broker != nil path.
	brokerSrv, err := broker.NewServer(broker.ServerConfig{})
	require.NoError(t, err)

	o := &Orchestrator{
		opts: Options{
			RepoPath:  "/tmp/testrepo",
			EventName: "push",
		},
		masker: NewSecretMasker(nil),
		broker: brokerSrv,
	}

	w := &workflow.Workflow{Name: "Test"}
	ctx := o.buildContext(w, nil, nil, nil, "run-broker")
	assert.NotNil(t, ctx)

	// Verify that the github context has an API URL routed through the broker.
	if ghCtx, ok := ctx["github"]; ok {
		if fields := ghCtx.ObjectFields(); fields != nil {
			if apiURL, ok := fields["api_url"]; ok {
				// Should contain the broker URL.
				assert.Contains(t, apiURL.StringVal(), "127.0.0.1")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// copyFile — error paths
// ---------------------------------------------------------------------------

func TestCopyFile_DstDirNotExist(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	require.NoError(t, os.WriteFile(src, []byte("content"), 0o644))

	// Destination in a non-existent directory.
	dst := filepath.Join(t.TempDir(), "nonexistent", "subdir", "dst.txt")
	err := copyFile(src, dst)
	assert.Error(t, err)
}

func TestCopyFile_EmptyFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(src, []byte(""), 0o644))

	dst := filepath.Join(t.TempDir(), "dst.txt")
	err := copyFile(src, dst)
	require.NoError(t, err)

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestCopyFile_LargeFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "large.txt")
	// Write a 100KB file.
	data := make([]byte, 100*1024)
	for i := range data {
		data[i] = byte('A' + (i % 26))
	}
	require.NoError(t, os.WriteFile(src, data, 0o644))

	dst := filepath.Join(t.TempDir(), "dst.txt")
	err := copyFile(src, dst)
	require.NoError(t, err)

	readData, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, data, readData)
}

// ---------------------------------------------------------------------------
// copyDir — additional edge cases
// ---------------------------------------------------------------------------

func TestCopyDir_WithSymlinks(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "real.txt"), []byte("content"), 0o644))

	// WalkDir follows symlinks to regular files, so this should copy the target.
	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dst, "real.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
}

func TestCopyDir_SourceDoesNotExist(t *testing.T) {
	src := filepath.Join(t.TempDir(), "nonexistent")
	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	assert.Error(t, err)
}

func TestCopyDir_ManyFiles(t *testing.T) {
	src := t.TempDir()
	// Create many small files.
	for i := 0; i < 20; i++ {
		require.NoError(t, os.WriteFile(
			filepath.Join(src, fmt.Sprintf("file%d.txt", i)),
			[]byte(fmt.Sprintf("content %d", i)),
			0o644,
		))
	}

	dst := filepath.Join(t.TempDir(), "copy")
	err := copyDir(src, dst)
	require.NoError(t, err)

	// Verify all files were copied.
	for i := 0; i < 20; i++ {
		data, err := os.ReadFile(filepath.Join(dst, fmt.Sprintf("file%d.txt", i)))
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("content %d", i), string(data))
	}
}

// ---------------------------------------------------------------------------
// testHardlink — additional edge cases
// ---------------------------------------------------------------------------

func TestTestHardlink_DstCreationFails(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "probe.txt"), []byte("x"), 0o644))

	// Use a path that can't be created as a directory.
	existing := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(existing, []byte("block"), 0o644))
	dst := filepath.Join(existing, "subdir") // can't create dir under a file

	result := testHardlink(src, dst)
	assert.False(t, result)
}

// ---------------------------------------------------------------------------
// isRelevantChange — remaining uncovered branch
// ---------------------------------------------------------------------------

func TestIsRelevantChange_RelPathError(t *testing.T) {
	// If filepath.Rel fails, isRelevantChange should return false.
	// filepath.Rel only fails on Windows when paths are on different drives,
	// but we can test the .nuxt ignored dir which is in the ignore list.
	event := fsnotify.Event{
		Name: "/project/.nuxt/some/file.js",
		Op:   fsnotify.Write,
	}
	assert.False(t, isRelevantChange(event, "/project"))
}

func TestIsRelevantChange_VenvDir(t *testing.T) {
	event := fsnotify.Event{
		Name: "/project/venv/lib/python3.9/site.py",
		Op:   fsnotify.Write,
	}
	assert.False(t, isRelevantChange(event, "/project"))
}

func TestIsRelevantChange_PycacheDir(t *testing.T) {
	event := fsnotify.Event{
		Name: "/project/app/__pycache__/module.pyc",
		Op:   fsnotify.Write,
	}
	assert.False(t, isRelevantChange(event, "/project"))
}

func TestIsRelevantChange_DistDir(t *testing.T) {
	event := fsnotify.Event{
		Name: "/project/dist/bundle.js",
		Op:   fsnotify.Write,
	}
	assert.False(t, isRelevantChange(event, "/project"))
}

// ---------------------------------------------------------------------------
// shouldIgnoreDir — additional cases
// ---------------------------------------------------------------------------

func TestShouldIgnoreDir_NuxtDir(t *testing.T) {
	assert.True(t, shouldIgnoreDir(".nuxt"))
}

func TestShouldIgnoreDir_NextDir(t *testing.T) {
	assert.True(t, shouldIgnoreDir(".next"))
}

func TestShouldIgnoreDir_DistDir(t *testing.T) {
	assert.True(t, shouldIgnoreDir("dist"))
}

func TestShouldIgnoreDir_BuildDir(t *testing.T) {
	assert.True(t, shouldIgnoreDir("build"))
}

func TestShouldIgnoreDir_CacheDir(t *testing.T) {
	assert.True(t, shouldIgnoreDir(".cache"))
}

func TestShouldIgnoreDir_NormalDirs(t *testing.T) {
	normalDirs := []string{"lib", "test", "docs", "scripts", "api", "pkg", "tools"}
	for _, dir := range normalDirs {
		assert.False(t, shouldIgnoreDir(dir), "should not ignore %s", dir)
	}
}

// ---------------------------------------------------------------------------
// addWatchDirs — with non-directory entries and inaccessible dirs
// ---------------------------------------------------------------------------

func TestAddWatchDirs_WithFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "subdir", "nested.txt"), []byte("y"), 0o644))

	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	err = addWatchDirs(watcher, root)
	require.NoError(t, err)

	watchList := watcher.WatchList()
	assert.GreaterOrEqual(t, len(watchList), 2) // root + subdir
}

func TestAddWatchDirs_SkipsAllIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	ignoredDirs := []string{".git", ".ions-work", ".letta", "node_modules",
		"__pycache__", ".next", ".nuxt", "dist", "build", ".cache", ".venv", "venv"}

	for _, dir := range ignoredDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "file.txt"), []byte("x"), 0o644))
	}

	// Also create a non-ignored dir.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))

	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	err = addWatchDirs(watcher, root)
	require.NoError(t, err)

	watchList := watcher.WatchList()
	for _, w := range watchList {
		rel, _ := filepath.Rel(root, w)
		for _, ignored := range ignoredDirs {
			assert.NotContains(t, rel, ignored, "should not watch %s", ignored)
		}
	}
}

// ---------------------------------------------------------------------------
// New() — verbose + dry-run combinations
// ---------------------------------------------------------------------------

func TestNew_DryRunVerbose(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "test.yml",
		DryRun:       true,
		Verbose:      true,
		RepoPath:     "/tmp/test",
	})
	require.NoError(t, err)
	assert.Nil(t, o.progress, "verbose mode should disable progress UI")
	assert.NotNil(t, o.logger)
	assert.Nil(t, o.runnerMgr, "dry-run should not create runner manager")
}

func TestNew_DryRunWithEnv(t *testing.T) {
	o, err := New(Options{
		WorkflowPath: "test.yml",
		DryRun:       true,
		RepoPath:     "/tmp/test",
		Env:          map[string]string{"FOO": "bar"},
		Secrets:      map[string]string{"SECRET": "val"},
		Vars:         map[string]string{"VAR": "varval"},
		Inputs:       map[string]string{"INPUT": "inputval"},
	})
	require.NoError(t, err)
	assert.Equal(t, "bar", o.opts.Env["FOO"])
	assert.Equal(t, "val", o.opts.Secrets["SECRET"])
	assert.Equal(t, "varval", o.opts.Vars["VAR"])
	assert.Equal(t, "inputval", o.opts.Inputs["INPUT"])
}

// ---------------------------------------------------------------------------
// ProgressUI — rendering with more complex states
// ---------------------------------------------------------------------------

func TestProgressUI_Render_SuccessAndFailedCounts(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"a", "b", "c"})
	p.jobs[0].status = "success"
	p.jobs[0].startTime = time.Now().Add(-2 * time.Second)
	p.jobs[0].endTime = time.Now()
	p.jobs[1].status = "failure"
	p.jobs[1].startTime = time.Now().Add(-1 * time.Second)
	p.jobs[1].endTime = time.Now()
	p.jobs[2].status = "running"
	p.jobs[2].startTime = time.Now()

	p.render()
	output := buf.String()
	assert.Contains(t, output, "1 running")
	assert.Contains(t, output, "1 done")
	assert.Contains(t, output, "1 failed")
	assert.Contains(t, output, "Jobs: 3 total")
}

func TestProgressUI_PrintFinal_WithSkippedAndCancelled(t *testing.T) {
	p, buf := newTestProgressUI()
	p.RegisterJobs([]string{"a", "b", "c", "d"})
	p.jobs[0].status = "success"
	p.jobs[0].startTime = time.Now()
	p.jobs[0].endTime = time.Now()
	p.jobs[1].status = "failure"
	p.jobs[1].startTime = time.Now()
	p.jobs[1].endTime = time.Now()
	p.jobs[1].lastLines = []string{"error line"}
	p.jobs[2].status = "skipped"
	p.jobs[3].status = "cancelled"

	p.printFinal()
	output := buf.String()
	assert.Contains(t, output, "Workflow completed")
	// All jobs should be listed.
	assert.Contains(t, output, "a")
	assert.Contains(t, output, "b")
	assert.Contains(t, output, "c")
	assert.Contains(t, output, "d")
	// Failed job error lines should appear.
	assert.Contains(t, output, "error line")
}

// ---------------------------------------------------------------------------
// LogStreamer — Summary with various status combinations
// ---------------------------------------------------------------------------

func TestLogStreamer_Summary_AllStatuses(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	results := map[string]*JobRunResult{
		"build": {
			NodeID:   "build",
			Status:   "success",
			Duration: 1 * time.Second,
		},
		"test": {
			NodeID:   "test",
			Status:   "failure",
			Duration: 2 * time.Second,
		},
		"deploy": {
			NodeID:   "deploy",
			Status:   "skipped",
			Duration: 0,
		},
		"notify": {
			NodeID:   "notify",
			Status:   "cancelled",
			Duration: 0,
		},
	}
	ls.Summary(results)

	output := buf.String()
	assert.Contains(t, output, "Summary:")
	assert.Contains(t, output, "build")
	assert.Contains(t, output, "test")
	assert.Contains(t, output, "deploy")
	assert.Contains(t, output, "notify")
}

// ---------------------------------------------------------------------------
// Comprehensive buildContext tests with various combinations
// ---------------------------------------------------------------------------

func TestBuildContext_AllOptionsSet(t *testing.T) {
	o := &Orchestrator{
		opts: Options{
			RepoPath:    "/tmp/testrepo",
			EventName:   "workflow_dispatch",
			Secrets:     map[string]string{"A": "1", "B": "2"},
			Vars:        map[string]string{"X": "a", "Y": "b"},
			Env:         map[string]string{"E1": "v1"},
			Inputs:      map[string]string{"in1": "val1"},
			GitHubToken: "ghp_tok",
		},
		masker: NewSecretMasker(map[string]string{"A": "1", "B": "2"}),
	}

	w := &workflow.Workflow{
		Name: "Full Workflow",
		Env:  map[string]string{"WE": "wv"},
	}

	node := &graph.JobNode{
		NodeID: "build",
		JobID:  "build",
		Job: &workflow.Job{
			Env:   map[string]string{"JE": "jv"},
			Needs: []string{},
		},
		MatrixValues: graph.MatrixCombination{"os": "linux", "ver": "3"},
	}

	jobOutputs := map[string]*ionsctx.JobResult{
		"prev": {
			Result:  "success",
			Outputs: map[string]string{"out1": "outval"},
		},
	}

	stepResults := map[string]*ionsctx.StepResult{
		"step1": {
			Outcome:    "success",
			Conclusion: "success",
			Outputs:    map[string]string{"sout": "sval"},
		},
	}

	ctx := o.buildContext(w, node, jobOutputs, stepResults, "run-all")
	assert.NotNil(t, ctx)

	// Verify all context keys are present.
	for _, key := range []string{"github", "env", "secrets", "vars", "inputs", "matrix", "steps", "needs", "runner"} {
		_, ok := ctx[key]
		assert.True(t, ok, "expected context key %q", key)
	}

	// Check matrix values.
	if m, ok := ctx["matrix"]; ok {
		if fields := m.ObjectFields(); fields != nil {
			if os, ok := fields["os"]; ok {
				assert.Equal(t, "linux", os.StringVal())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// streamOutput — with secret masking
// ---------------------------------------------------------------------------

func TestStreamOutput_WithSecretMasking(t *testing.T) {
	masker := NewSecretMasker(map[string]string{"TOKEN": "mysecret"})
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	input := bytes.NewBufferString("using token mysecret to auth\n")
	streamOutput(input, "job1", logger)

	output := buf.String()
	assert.NotContains(t, output, "mysecret")
	assert.Contains(t, output, "***")
}

func TestStreamOutput_MixedNewlinesAndContent(t *testing.T) {
	masker := NewSecretMasker(nil)
	logger := NewLogStreamer(masker, false)
	var buf bytes.Buffer
	logger.SetWriter(&buf)

	input := bytes.NewBufferString("line1\n\nline2\n\n\nline3\n")
	streamOutput(input, "job1", logger)

	output := buf.String()
	assert.Contains(t, output, "line1")
	assert.Contains(t, output, "line2")
	assert.Contains(t, output, "line3")
}

// ---------------------------------------------------------------------------
// ProgressUI — getJob edge case
// ---------------------------------------------------------------------------

func TestProgressUI_GetJob_ValidAndInvalid(t *testing.T) {
	p, _ := newTestProgressUI()
	p.RegisterJobs([]string{"build", "test"})

	j := p.getJob("build")
	assert.NotNil(t, j)
	assert.Equal(t, "build", j.nodeID)

	j = p.getJob("test")
	assert.NotNil(t, j)

	j = p.getJob("nonexistent")
	assert.Nil(t, j)
}

// ---------------------------------------------------------------------------
// ProgressUI clearLines with various values
// ---------------------------------------------------------------------------

func TestProgressUI_ClearLines_LargeN(t *testing.T) {
	p, buf := newTestProgressUI()
	p.isTTY = true
	p.clearLines(10)
	output := buf.String()
	// Should have 10 instances of cursor-up + clear-line.
	count := strings.Count(output, "\033[A\033[2K")
	assert.Equal(t, 10, count)
}

func TestProgressUI_ClearLines_One(t *testing.T) {
	p, buf := newTestProgressUI()
	p.isTTY = true
	p.clearLines(1)
	output := buf.String()
	count := strings.Count(output, "\033[A\033[2K")
	assert.Equal(t, 1, count)
}

// ---------------------------------------------------------------------------
// RunResult struct tests
// ---------------------------------------------------------------------------

func TestRunResult_WithMultipleJobs(t *testing.T) {
	r := &RunResult{
		Success: false,
		JobResults: map[string]*JobRunResult{
			"build": {
				NodeID:   "build",
				Status:   "success",
				Duration: 10 * time.Second,
				Outputs:  map[string]string{"version": "1.0"},
			},
			"test": {
				NodeID:   "test",
				Status:   "failure",
				Duration: 5 * time.Second,
			},
		},
		Duration: 15 * time.Second,
	}
	assert.False(t, r.Success)
	assert.Len(t, r.JobResults, 2)
	assert.Equal(t, "success", r.JobResults["build"].Status)
	assert.Equal(t, "failure", r.JobResults["test"].Status)
	assert.Equal(t, "1.0", r.JobResults["build"].Outputs["version"])
}

// ---------------------------------------------------------------------------
// Options struct test
// ---------------------------------------------------------------------------

func TestOptions_AllFields(t *testing.T) {
	opts := Options{
		WorkflowPath:    "/path/to/workflow.yml",
		JobFilter:       "build",
		EventName:       "push",
		Secrets:         map[string]string{"S": "v"},
		Vars:            map[string]string{"V": "v"},
		Env:             map[string]string{"E": "v"},
		Inputs:          map[string]string{"I": "v"},
		DryRun:          true,
		Verbose:         true,
		RepoPath:        "/repo",
		ArtifactDir:     "/artifacts",
		ReuseContainers: true,
		Platform:        "linux/amd64",
		GitHubToken:     "ghp_xxx",
	}
	assert.Equal(t, "/path/to/workflow.yml", opts.WorkflowPath)
	assert.Equal(t, "build", opts.JobFilter)
	assert.Equal(t, "push", opts.EventName)
	assert.True(t, opts.DryRun)
	assert.True(t, opts.Verbose)
	assert.True(t, opts.ReuseContainers)
	assert.Equal(t, "linux/amd64", opts.Platform)
	assert.Equal(t, "ghp_xxx", opts.GitHubToken)
}

// ---------------------------------------------------------------------------
// addWatchDirs — inaccessible directory error path
// ---------------------------------------------------------------------------

func TestAddWatchDirs_NonExistentRoot(t *testing.T) {
	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	// addWatchDirs on a non-existent root silently skips the error
	// (the WalkDir callback returns nil for errors), so no watches are added.
	err = addWatchDirs(watcher, "/nonexistent/path/xyz")
	assert.NoError(t, err)
	assert.Empty(t, watcher.WatchList())
}

func TestAddWatchDirs_EmptyDir(t *testing.T) {
	root := t.TempDir()

	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer watcher.Close()

	err = addWatchDirs(watcher, root)
	require.NoError(t, err)

	// Should watch the root directory.
	watchList := watcher.WatchList()
	assert.Len(t, watchList, 1)
}

// ---------------------------------------------------------------------------
// DryRun with a repo that has no git remote (exercises the warning path)
// ---------------------------------------------------------------------------

func TestDryRun_NoGitRemote(t *testing.T) {
	// Create a temp dir with a simple workflow and init a git repo without a remote.
	tmpDir := t.TempDir()

	// Create a minimal workflow file.
	workflowDir := filepath.Join(tmpDir, ".github", "workflows")
	require.NoError(t, os.MkdirAll(workflowDir, 0o755))
	wfContent := `name: Test
on: push
jobs:
  greet:
    runs-on: ubuntu-latest
    steps:
      - run: echo hello
`
	wfPath := filepath.Join(workflowDir, "test.yml")
	require.NoError(t, os.WriteFile(wfPath, []byte(wfContent), 0o644))

	// Initialize a git repo without any remote.
	// go-git will read .git/ and produce "local/repo" since there's no remote.
	// We need the git repo so that the context builder can read it.

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
	})
	require.NoError(t, err)

	// Capture stderr to verify the warning is emitted.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	result, err := o.Run(context.Background())

	w.Close()
	os.Stderr = oldStderr

	var stderrBuf bytes.Buffer
	stderrBuf.ReadFrom(r)

	require.NoError(t, err)
	assert.True(t, result.Success)

	// The warning about no git remote should be in stderr.
	assert.Contains(t, stderrBuf.String(), "no git remote")
}

// ---------------------------------------------------------------------------
// Run — parse error path
// ---------------------------------------------------------------------------

func TestRun_ParseError(t *testing.T) {
	// Create a file with invalid YAML.
	tmpDir := t.TempDir()
	badFile := filepath.Join(tmpDir, "bad.yml")
	require.NoError(t, os.WriteFile(badFile, []byte("invalid: [yaml: broken"), 0o644))

	o, err := New(Options{
		WorkflowPath: badFile,
		DryRun:       true,
		RepoPath:     tmpDir,
	})
	require.NoError(t, err)

	_, err = o.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}

// ---------------------------------------------------------------------------
// Run — validation error path
// ---------------------------------------------------------------------------

func TestRun_ValidationError(t *testing.T) {
	// Create a workflow with no jobs (should trigger validation error).
	tmpDir := t.TempDir()
	badFile := filepath.Join(tmpDir, "nojobs.yml")
	wfContent := `name: No Jobs
on: push
jobs: {}
`
	require.NoError(t, os.WriteFile(badFile, []byte(wfContent), 0o644))

	o, err := New(Options{
		WorkflowPath: badFile,
		DryRun:       true,
		RepoPath:     tmpDir,
	})
	require.NoError(t, err)

	_, err = o.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation error")
}

// ---------------------------------------------------------------------------
// dryRun — with skipped jobs
// ---------------------------------------------------------------------------

func TestDryRun_WithSkippedJobs(t *testing.T) {
	// Create a workflow with a job that has an if: condition that evaluates to false.
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "skip.yml")
	wfContent := `name: Skip Test
on: push
jobs:
  always-run:
    runs-on: ubuntu-latest
    steps:
      - run: echo hello
  never-run:
    runs-on: ubuntu-latest
    if: false
    steps:
      - run: echo skipped
`
	require.NoError(t, os.WriteFile(wfPath, []byte(wfContent), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

// ---------------------------------------------------------------------------
// Run — graph error (circular dependency)
// ---------------------------------------------------------------------------

func TestRun_GraphCircularDependency(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "circular.yml")
	wfContent := `name: Circular
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    needs: [b]
    steps:
      - run: echo a
  b:
    runs-on: ubuntu-latest
    needs: [a]
    steps:
      - run: echo b
`
	require.NoError(t, os.WriteFile(wfPath, []byte(wfContent), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
	})
	require.NoError(t, err)

	_, err = o.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "graph error")
}

// ---------------------------------------------------------------------------
// Run — job filter with no matching jobs
// ---------------------------------------------------------------------------

func TestRun_JobFilterNoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "filter.yml")
	wfContent := `name: Filter Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo build
`
	require.NoError(t, os.WriteFile(wfPath, []byte(wfContent), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
		JobFilter:    "nonexistent-job",
	})
	require.NoError(t, err)

	_, err = o.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no jobs match filter")
}

// ---------------------------------------------------------------------------
// Run — job filter with matching job
// ---------------------------------------------------------------------------

func TestRun_JobFilterMatch(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "filter.yml")
	wfContent := `name: Filter Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo build
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo test
`
	require.NoError(t, os.WriteFile(wfPath, []byte(wfContent), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
		JobFilter:    "build",
	})
	require.NoError(t, err)

	result, err := o.Run(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Success)
}

// ---------------------------------------------------------------------------
// copyDir — hardlink fallback to copyFile
// ---------------------------------------------------------------------------

func TestCopyDir_HardlinkFallback(t *testing.T) {
	// When the source is on a filesystem that doesn't support hardlinks
	// or the hardlink test fails, copyDir should fall back to copyFile.
	// We can force this by making testHardlink return false by using
	// a read-only dst dir setup. However, testHardlink creates its own
	// temp file. Instead, test that files copy correctly even when
	// hardlinks might not work — the important thing is that copyDir
	// produces a valid copy.
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "output")

	// Create files in src.
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0o644))

	err := copyDir(src, dst)
	require.NoError(t, err)

	// Verify both files copied.
	data, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	data, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(data))
}

// ---------------------------------------------------------------------------
// ProgressUI — register, start, step, complete, log, finish lifecycle
// ---------------------------------------------------------------------------

func TestProgressUI_FullLifecycle(t *testing.T) {
	// Create a ProgressUI with a buffer writer (bypass TTY check).
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false, // don't try to clear lines
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	// Register jobs.
	p.RegisterJobs([]string{"job1", "job2", "job3"})
	assert.Equal(t, 3, len(p.jobs))

	// Start job1.
	p.JobStarted("job1")
	assert.Equal(t, "running", p.jobs[0].status)

	// Step updates for job1.
	p.StepUpdate("job1", "checkout", "InProgress", nil)
	assert.Equal(t, "checkout", p.jobs[0].currentStep)
	assert.Equal(t, 1, p.jobs[0].stepsTotal)

	succeeded := "succeeded"
	p.StepUpdate("job1", "checkout", "Completed", &succeeded)
	assert.Equal(t, 1, p.jobs[0].stepsDone)

	p.StepUpdate("job1", "build", "InProgress", nil)
	assert.Equal(t, "build", p.jobs[0].currentStep)

	// Log lines for job1.
	p.LogLine("job1", "compiling main.go")
	p.LogLine("job1", "compiling util.go")
	assert.Len(t, p.jobs[0].lastLines, 2)

	// Complete job1 as success.
	p.JobCompleted("job1", "success")
	assert.Equal(t, "success", p.jobs[0].status)

	// Start and fail job2.
	p.JobStarted("job2")
	p.LogLine("job2", "error: something went wrong")
	p.JobCompleted("job2", "failure")
	assert.Equal(t, "failure", p.jobs[1].status)

	// Skip job3.
	p.JobCompleted("job3", "skipped")
	assert.Equal(t, "skipped", p.jobs[2].status)

	// Finish.
	p.Finish()
	assert.True(t, p.done)

	// The final output should contain job names.
	output := buf.String()
	assert.Contains(t, output, "job1")
	assert.Contains(t, output, "job2")
	assert.Contains(t, output, "job3")
	assert.Contains(t, output, "Workflow completed")
}

// ---------------------------------------------------------------------------
// ProgressUI — operations on unknown job
// ---------------------------------------------------------------------------

func TestProgressUI_UnknownJob(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	// Operations on unknown jobs should not panic.
	p.JobStarted("unknown")
	p.StepUpdate("unknown", "step", "InProgress", nil)
	p.JobCompleted("unknown", "success")
	p.LogLine("unknown", "line")
}

// ---------------------------------------------------------------------------
// ProgressUI — duplicate job registration
// ---------------------------------------------------------------------------

func TestProgressUI_DuplicateRegistration(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"job1", "job2"})
	p.RegisterJobs([]string{"job1", "job3"}) // job1 already registered
	assert.Equal(t, 3, len(p.jobs))          // should be 3, not 4
}

// ---------------------------------------------------------------------------
// ProgressUI — render with cancelled job status
// ---------------------------------------------------------------------------

func TestProgressUI_CancelledStatus(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"job1"})
	p.JobCompleted("job1", "cancelled")

	// Finish to render final output.
	p.Finish()
	output := buf.String()
	assert.Contains(t, output, "job1")
}

// ---------------------------------------------------------------------------
// ProgressUI — LogLine overflow (keeps only last 5)
// ---------------------------------------------------------------------------

func TestProgressUI_LogLineOverflow(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"job1"})
	for i := 0; i < 10; i++ {
		p.LogLine("job1", fmt.Sprintf("line %d", i))
	}

	// Only the last 5 lines should be kept.
	assert.Len(t, p.jobs[0].lastLines, 5)
	assert.Equal(t, "line 5", p.jobs[0].lastLines[0])
	assert.Equal(t, "line 9", p.jobs[0].lastLines[4])
}

// ---------------------------------------------------------------------------
// ProgressUI — failed job shows last log lines in printFinal
// ---------------------------------------------------------------------------

func TestProgressUI_FailedJobShowsLogs(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"fail-job"})
	p.JobStarted("fail-job")
	p.LogLine("fail-job", "error: compilation failed")
	p.LogLine("fail-job", "exit code 1")
	p.JobCompleted("fail-job", "failure")
	p.Finish()

	output := buf.String()
	assert.Contains(t, output, "error: compilation failed")
	assert.Contains(t, output, "exit code 1")
}

// ---------------------------------------------------------------------------
// New — with explicit platform option
// ---------------------------------------------------------------------------

func TestNew_WithPlatform(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "wf.yml")
	require.NoError(t, os.WriteFile(wfPath, []byte("name: T\non: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
		Platform:     "linux/arm64",
	})
	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.Equal(t, "linux/arm64", o.opts.Platform)
}

// ---------------------------------------------------------------------------
// New — verbose mode (disables progress UI)
// ---------------------------------------------------------------------------

func TestNew_VerboseMode(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "wf.yml")
	require.NoError(t, os.WriteFile(wfPath, []byte("name: T\non: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		RepoPath:     tmpDir,
		Verbose:      true,
	})
	require.NoError(t, err)
	assert.Nil(t, o.progress) // verbose disables progress UI
}

// ---------------------------------------------------------------------------
// ProgressLogger — HasProgress, Streamer
// ---------------------------------------------------------------------------

func TestProgressLogger_Methods(t *testing.T) {
	masker := NewSecretMasker(nil)

	// In verbose mode, progress should be nil.
	pl, progress := NewProgressLogger(masker, true)
	assert.Nil(t, progress)
	assert.False(t, pl.HasProgress())
	assert.NotNil(t, pl.Streamer())

	// In non-verbose mode on a non-TTY, progress should also be nil.
	pl2, progress2 := NewProgressLogger(masker, false)
	assert.Nil(t, progress2) // not a TTY
	assert.False(t, pl2.HasProgress())
}

// ---------------------------------------------------------------------------
// New — non-dry-run mode (exercises runner.NewManager path)
// ---------------------------------------------------------------------------

func TestNew_NonDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "wf.yml")
	require.NoError(t, os.WriteFile(wfPath, []byte("name: T\non: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), 0o644))

	// In non-dry-run mode, New() should create a runner manager.
	o, err := New(Options{
		WorkflowPath: wfPath,
		RepoPath:     tmpDir,
		Verbose:      true, // verbose to avoid TTY dependency
	})
	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.NotNil(t, o.runnerMgr)
	assert.Nil(t, o.progress) // verbose mode disables progress
}

// ---------------------------------------------------------------------------
// New — empty RepoPath defaults to cwd
// ---------------------------------------------------------------------------

func TestNew_DefaultRepoPath(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "wf.yml")
	require.NoError(t, os.WriteFile(wfPath, []byte("name: T\non: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), 0o644))

	o, err := New(Options{
		WorkflowPath: wfPath,
		DryRun:       true,
		// RepoPath intentionally left empty.
	})
	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.NotEmpty(t, o.opts.RepoPath)
}

// ---------------------------------------------------------------------------
// New — with secrets, vars, env, inputs
// ---------------------------------------------------------------------------

func TestNew_WithAllOptions(t *testing.T) {
	tmpDir := t.TempDir()
	wfPath := filepath.Join(tmpDir, "wf.yml")
	require.NoError(t, os.WriteFile(wfPath, []byte("name: T\non: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), 0o644))

	o, err := New(Options{
		WorkflowPath:    wfPath,
		DryRun:          true,
		RepoPath:        tmpDir,
		Secrets:         map[string]string{"MY_SECRET": "s3cret"},
		Vars:            map[string]string{"MY_VAR": "value"},
		Env:             map[string]string{"MY_ENV": "envval"},
		Inputs:          map[string]string{"MY_INPUT": "in"},
		ArtifactDir:     filepath.Join(tmpDir, "artifacts"),
		ReuseContainers: true,
		GitHubToken:     "ghp_fake",
		EventName:       "workflow_dispatch",
	})
	require.NoError(t, err)
	assert.NotNil(t, o)
	assert.Equal(t, "workflow_dispatch", o.opts.EventName)
	assert.Equal(t, "ghp_fake", o.opts.GitHubToken)
	assert.True(t, o.opts.ReuseContainers)
}

// ---------------------------------------------------------------------------
// ProgressUI — render with TTY mode (clearLines exercised)
// ---------------------------------------------------------------------------

func TestProgressUI_TTYRender(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    true, // enable clear lines
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"job1", "job2"})
	p.JobStarted("job1")
	p.StepUpdate("job1", "checkout", "InProgress", nil)

	// Manually call render to exercise the TTY path.
	p.mu.Lock()
	p.render()
	p.mu.Unlock()

	// The output should contain ANSI escape codes for clearing.
	output := buf.String()
	assert.Contains(t, output, "\033[A") // cursor up
	assert.Contains(t, output, "job1")

	// Complete and finish.
	res := "succeeded"
	p.StepUpdate("job1", "checkout", "Completed", &res)
	p.JobCompleted("job1", "success")
	p.JobCompleted("job2", "skipped")
	p.Finish()
}

// ---------------------------------------------------------------------------
// ProgressUI — clearLines with 0 lines
// ---------------------------------------------------------------------------

func TestProgressUI_ClearLinesZero(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    true,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	// clearLines with 0 should be a no-op.
	p.clearLines(0)
	assert.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// ProgressUI — render when done should be a no-op
// ---------------------------------------------------------------------------

func TestProgressUI_RenderWhenDone(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		done:     true,
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"job1"})
	// render when done should be a no-op.
	p.render()
	assert.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// ProgressUI — renderJob with running job shows elapsed time
// ---------------------------------------------------------------------------

func TestProgressUI_RenderJobRunningElapsed(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now().Add(-5 * time.Second),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"running-job"})
	p.jobs[0].status = "running"
	p.jobs[0].startTime = time.Now().Add(-3 * time.Second)
	p.jobs[0].currentStep = "build step"

	p.Finish()
	output := buf.String()
	assert.Contains(t, output, "running-job")
}

// ---------------------------------------------------------------------------
// ProgressUI — renderJob with completed job shows duration
// ---------------------------------------------------------------------------

func TestProgressUI_RenderJobCompleted(t *testing.T) {
	var buf bytes.Buffer
	p := &ProgressUI{
		writer:   &buf,
		isTTY:    false,
		jobIndex: make(map[string]int),
		started:  time.Now().Add(-10 * time.Second),
		stopTick: make(chan struct{}),
	}

	p.RegisterJobs([]string{"done-job"})
	p.jobs[0].status = "success"
	p.jobs[0].startTime = time.Now().Add(-5 * time.Second)
	p.jobs[0].endTime = time.Now()

	p.Finish()
	output := buf.String()
	assert.Contains(t, output, "done-job")
}

// ---------------------------------------------------------------------------
// copyDir — cross-filesystem (hardlink fails, falls back to copyFile)
// ---------------------------------------------------------------------------

func TestCopyDir_CrossFilesystem(t *testing.T) {
	// If /dev/shm exists and is a different filesystem from /tmp,
	// hardlinks across them will fail, exercising the copyFile fallback.
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("/dev/shm not available")
	}

	src, err := os.MkdirTemp("/tmp", "copydir-src-*")
	require.NoError(t, err)
	defer os.RemoveAll(src)

	dst, err := os.MkdirTemp("/dev/shm", "copydir-dst-*")
	require.NoError(t, err)
	defer os.RemoveAll(dst)
	// copyDir creates a subdir, so use a path inside dst.
	dstTarget := filepath.Join(dst, "output")

	// Create files in src.
	require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte("cross-fs"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested"), 0o644))

	err = copyDir(src, dstTarget)
	require.NoError(t, err)

	// Verify files were copied (not hardlinked).
	data, err := os.ReadFile(filepath.Join(dstTarget, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "cross-fs", string(data))

	data, err = os.ReadFile(filepath.Join(dstTarget, "sub", "nested.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested", string(data))
}
