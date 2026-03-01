package internal_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/workflow"
)

func TestIntegration_HelloWorld(t *testing.T) {
	w, err := workflow.ParseFile("../testdata/workflows/hello-world.yml")
	require.NoError(t, err)
	assert.Equal(t, "Hello World", w.Name)

	errs := workflow.Validate(w)
	assert.Empty(t, errs)

	g, err := graph.Build(w)
	require.NoError(t, err)
	require.NoError(t, g.Validate())

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)
	assert.Len(t, sorted, 1)
	assert.Equal(t, "greet", sorted[0].NodeID)

	groups, err := g.ParallelGroups()
	require.NoError(t, err)
	assert.Len(t, groups, 1)
	assert.Len(t, groups[0].Nodes, 1)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	assert.Len(t, plan.Groups, 1)
	assert.Empty(t, plan.Skipped)
}

func TestIntegration_MultiJob(t *testing.T) {
	w, err := workflow.ParseFile("../testdata/workflows/multi-job.yml")
	require.NoError(t, err)
	assert.Equal(t, "Multi Job", w.Name)

	errs := workflow.Validate(w)
	assert.Empty(t, errs)

	// Check workflow env
	assert.Equal(t, "global-value", w.Env["GLOBAL_VAR"])

	// Check job dependencies
	buildJob := w.Jobs["build"]
	require.NotNil(t, buildJob)
	assert.Empty(t, buildJob.Needs)

	testJob := w.Jobs["test"]
	require.NotNil(t, testJob)
	assert.Equal(t, []string{"build"}, []string(testJob.Needs))

	deployJob := w.Jobs["deploy"]
	require.NotNil(t, deployJob)
	assert.Contains(t, []string(deployJob.Needs), "build")
	assert.Contains(t, []string(deployJob.Needs), "test")

	// Build and validate graph
	g, err := graph.Build(w)
	require.NoError(t, err)
	require.NoError(t, g.Validate())

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)
	assert.Len(t, sorted, 3)
	// build must come before test and deploy
	buildIdx, testIdx, deployIdx := -1, -1, -1
	for i, n := range sorted {
		switch n.NodeID {
		case "build":
			buildIdx = i
		case "test":
			testIdx = i
		case "deploy":
			deployIdx = i
		}
	}
	assert.Less(t, buildIdx, testIdx)
	assert.Less(t, buildIdx, deployIdx)
	assert.Less(t, testIdx, deployIdx)

	// Parallel groups
	groups, err := g.ParallelGroups()
	require.NoError(t, err)
	assert.Len(t, groups, 3) // build, then test, then deploy
	assert.Equal(t, "build", groups[0].Nodes[0].NodeID)
	assert.Equal(t, "test", groups[1].Nodes[0].NodeID)
	assert.Equal(t, "deploy", groups[2].Nodes[0].NodeID)

	// Plan with deploy if: condition
	// The deploy job has `if: github.ref == 'refs/heads/main'`
	// With a matching context, deploy should be included
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/main"),
		}),
	}
	plan, err := g.Plan(ctx)
	require.NoError(t, err)
	assert.Len(t, plan.Groups, 3)
	assert.Empty(t, plan.Skipped)

	// With a non-matching context, deploy should be skipped
	ctx2 := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/feature"),
		}),
	}
	plan2, err := g.Plan(ctx2)
	require.NoError(t, err)
	assert.Len(t, plan2.Skipped, 1)
	assert.Equal(t, "deploy", plan2.Skipped[0].NodeID)
}

func TestIntegration_Matrix(t *testing.T) {
	w, err := workflow.ParseFile("../testdata/workflows/matrix.yml")
	require.NoError(t, err)
	assert.Equal(t, "Matrix Build", w.Name)

	errs := workflow.Validate(w)
	assert.Empty(t, errs)

	testJob := w.Jobs["test"]
	require.NotNil(t, testJob)
	require.NotNil(t, testJob.Strategy)
	require.NotNil(t, testJob.Strategy.Matrix)
	require.NotNil(t, testJob.Strategy.FailFast)
	assert.False(t, *testJob.Strategy.FailFast)

	// Matrix: 2 OS x 4 node = 8
	g, err := graph.Build(w)
	require.NoError(t, err)
	require.NoError(t, g.Validate())

	nodes := g.NodesByJobID("test")
	assert.Equal(t, 8, len(nodes))

	// All matrix nodes should be in parallel (single stage, no deps)
	groups, err := g.ParallelGroups()
	require.NoError(t, err)
	assert.Len(t, groups, 1)
	assert.Equal(t, 8, len(groups[0].Nodes))
}

func TestIntegration_ComplexTriggers(t *testing.T) {
	w, err := workflow.ParseFile("../testdata/workflows/complex-triggers.yml")
	require.NoError(t, err)
	assert.Equal(t, "Complex Triggers", w.Name)

	errs := workflow.Validate(w)
	assert.Empty(t, errs)

	// Check triggers
	require.NotNil(t, w.On.Events["push"])
	assert.Contains(t, w.On.Events["push"].Branches, "main")
	assert.Contains(t, w.On.Events["push"].Tags, "v*")
	assert.Contains(t, w.On.Events["push"].PathsIgnore, "**.md")

	require.NotNil(t, w.On.Events["pull_request"])
	assert.Contains(t, w.On.Events["pull_request"].Types, "opened")

	require.NotNil(t, w.On.Events["workflow_dispatch"])
	assert.Contains(t, w.On.Events["workflow_dispatch"].Inputs, "environment")
	envInput := w.On.Events["workflow_dispatch"].Inputs["environment"]
	assert.True(t, envInput.Required)
	assert.Equal(t, "staging", envInput.Default)

	require.NotNil(t, w.On.Events["schedule"])
	assert.Equal(t, "0 2 * * 1", w.On.Events["schedule"].Cron)

	// Permissions
	require.NotNil(t, w.Permissions)
	assert.Equal(t, workflow.PermissionRead, w.Permissions.Scopes["contents"])
	assert.Equal(t, workflow.PermissionWrite, w.Permissions.Scopes["issues"])

	// Concurrency
	require.NotNil(t, w.Concurrency)
	assert.True(t, w.Concurrency.CancelInProgress)
}

func TestIntegration_ReusableWorkflow(t *testing.T) {
	w, err := workflow.ParseFile("../testdata/workflows/reusable-workflow.yml")
	require.NoError(t, err)

	errs := workflow.Validate(w)
	assert.Empty(t, errs)

	callJob := w.Jobs["call-reusable"]
	require.NotNil(t, callJob)
	assert.Equal(t, "octo-org/example-repo/.github/workflows/reusable.yml@main", callJob.Uses)
	assert.Equal(t, "inherit", callJob.Secrets)

	afterJob := w.Jobs["after-reusable"]
	require.NotNil(t, afterJob)
	assert.Contains(t, []string(afterJob.Needs), "call-reusable")

	g, err := graph.Build(w)
	require.NoError(t, err)
	require.NoError(t, g.Validate())

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)
	assert.Equal(t, "call-reusable", sorted[0].NodeID)
	assert.Equal(t, "after-reusable", sorted[1].NodeID)
}

func TestIntegration_Services(t *testing.T) {
	w, err := workflow.ParseFile("../testdata/workflows/services.yml")
	require.NoError(t, err)

	errs := workflow.Validate(w)
	assert.Empty(t, errs)

	job := w.Jobs["integration"]
	require.NotNil(t, job)

	// Container
	require.NotNil(t, job.Container)
	assert.Equal(t, "node:20", job.Container.Image)

	// Services
	require.NotNil(t, job.Services["postgres"])
	assert.Equal(t, "postgres:15", job.Services["postgres"].Image)
	assert.Equal(t, "postgres", job.Services["postgres"].Env["POSTGRES_PASSWORD"])

	require.NotNil(t, job.Services["redis"])
	assert.Equal(t, "redis:7", job.Services["redis"].Image)

	// Defaults
	require.NotNil(t, job.Defaults)
	require.NotNil(t, job.Defaults.Run)
	assert.Equal(t, "bash", job.Defaults.Run.Shell)
	assert.Equal(t, "./src", job.Defaults.Run.WorkingDirectory)
}

func TestIntegration_ExpressionEvalInContext(t *testing.T) {
	// Test that expressions used in workflow conditions can be evaluated
	// against a context built from our context package
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref":        expression.String("refs/heads/main"),
			"event_name": expression.String("push"),
			"actor":      expression.String("test-user"),
		}),
		"env": expression.Object(map[string]expression.Value{
			"NODE_ENV": expression.String("production"),
		}),
		"needs": expression.Object(map[string]expression.Value{
			"build": expression.Object(map[string]expression.Value{
				"result": expression.String("success"),
				"outputs": expression.Object(map[string]expression.Value{
					"version": expression.String("1.0.0"),
				}),
			}),
		}),
	}

	// Test typical workflow expressions
	tests := []struct {
		expr   string
		truthy bool
	}{
		{"github.ref == 'refs/heads/main'", true},
		{"github.ref == 'refs/heads/develop'", false},
		{"github.event_name == 'push'", true},
		{"needs.build.result == 'success'", true},
		{"needs.build.result == 'failure'", false},
		{"contains(github.ref, 'main')", true},
		{"startsWith(github.ref, 'refs/heads/')", true},
		{"github.actor == 'test-user' && github.event_name == 'push'", true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := expression.EvalExpression(tt.expr, ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.truthy, expression.IsTruthy(result))
		})
	}

	// Test interpolation
	s, err := expression.EvalInterpolation("version-${{ needs.build.outputs.version }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "version-1.0.0", s)
}

// fmtSprint wraps fmt.Sprintf for matrix value comparison.
var fmtSprint = fmt.Sprint
