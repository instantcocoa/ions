package workflow

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	return filepath.Join(dir, "..", "..", "testdata", "workflows", name)
}

func TestParse_HelloWorld(t *testing.T) {
	w, err := ParseFile(testdataPath("hello-world.yml"))
	require.NoError(t, err)

	assert.Equal(t, "Hello World", w.Name)

	// Trigger
	assert.Contains(t, w.On.Events, "push")

	// Jobs
	require.Contains(t, w.Jobs, "greet")
	job := w.Jobs["greet"]
	assert.Equal(t, []string{"ubuntu-latest"}, job.RunsOn.Labels)

	// Steps
	require.Len(t, job.Steps, 2)
	assert.Equal(t, "Say hello", job.Steps[0].Name)
	assert.Equal(t, `echo "Hello, World!"`, job.Steps[0].Run)
	assert.Equal(t, "Say goodbye", job.Steps[1].Name)
	assert.Equal(t, `echo "Goodbye!"`, job.Steps[1].Run)

	// Validate
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_MultiJob(t *testing.T) {
	w, err := ParseFile(testdataPath("multi-job.yml"))
	require.NoError(t, err)

	assert.Equal(t, "Multi Job", w.Name)

	// Triggers
	assert.Contains(t, w.On.Events, "push")
	assert.Contains(t, w.On.Events, "pull_request")
	push := w.On.Events["push"]
	require.NotNil(t, push)
	assert.Equal(t, []string{"main"}, push.Branches)

	// Global env
	assert.Equal(t, "global-value", w.Env["GLOBAL_VAR"])

	// Build job
	require.Contains(t, w.Jobs, "build")
	build := w.Jobs["build"]
	assert.Equal(t, []string{"ubuntu-latest"}, build.RunsOn.Labels)
	assert.Equal(t, "production", build.Env["BUILD_ENV"])
	require.Contains(t, build.Outputs, "artifact-path")
	assert.Equal(t, "${{ steps.build-step.outputs.path }}", build.Outputs["artifact-path"].Value)

	require.Len(t, build.Steps, 1)
	assert.Equal(t, "build-step", build.Steps[0].ID)
	assert.Contains(t, build.Steps[0].Run, "Building...")

	// Test job
	require.Contains(t, w.Jobs, "test")
	test := w.Jobs["test"]
	assert.Equal(t, StringOrSlice{"build"}, test.Needs)

	// Deploy job
	require.Contains(t, w.Jobs, "deploy")
	deploy := w.Jobs["deploy"]
	assert.Equal(t, StringOrSlice{"build", "test"}, deploy.Needs)
	assert.Equal(t, "github.ref == 'refs/heads/main'", deploy.If)
	require.NotNil(t, deploy.Environment)
	assert.Equal(t, "production", deploy.Environment.Name)
	assert.Equal(t, "https://example.com", deploy.Environment.URL)

	// Validate
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_Matrix(t *testing.T) {
	w, err := ParseFile(testdataPath("matrix.yml"))
	require.NoError(t, err)

	assert.Equal(t, "Matrix Build", w.Name)

	require.Contains(t, w.Jobs, "test")
	job := w.Jobs["test"]

	// RunsOn is an expression
	assert.Equal(t, []string{"${{ matrix.os }}"}, job.RunsOn.Labels)

	// Strategy
	require.NotNil(t, job.Strategy)
	require.NotNil(t, job.Strategy.FailFast)
	assert.False(t, *job.Strategy.FailFast)

	require.NotNil(t, job.Strategy.Matrix)
	m := job.Strategy.Matrix
	assert.Contains(t, m.Dimensions, "os")
	assert.Contains(t, m.Dimensions, "node")
	assert.Len(t, m.Dimensions["os"], 2)
	assert.Len(t, m.Dimensions["node"], 4)

	assert.Empty(t, m.Include)
	assert.Empty(t, m.Exclude)

	// Steps
	require.Len(t, job.Steps, 2)
	assert.Equal(t, "actions/setup-node@v4", job.Steps[0].Uses)
	assert.Equal(t, "${{ matrix.node }}", job.Steps[0].With["node-version"])

	// Validate
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_ComplexTriggers(t *testing.T) {
	w, err := ParseFile(testdataPath("complex-triggers.yml"))
	require.NoError(t, err)

	assert.Equal(t, "Complex Triggers", w.Name)

	// Push trigger
	push := w.On.Events["push"]
	require.NotNil(t, push)
	assert.Equal(t, []string{"main", "release/**"}, push.Branches)
	assert.Equal(t, []string{"v*"}, push.Tags)
	assert.Equal(t, []string{"**.md"}, push.PathsIgnore)

	// Pull request trigger
	pr := w.On.Events["pull_request"]
	require.NotNil(t, pr)
	assert.Equal(t, []string{"opened", "synchronize", "reopened"}, pr.Types)

	// Workflow dispatch
	wd := w.On.Events["workflow_dispatch"]
	require.NotNil(t, wd)
	require.Contains(t, wd.Inputs, "environment")
	envInput := wd.Inputs["environment"]
	assert.Equal(t, "Target environment", envInput.Description)
	assert.True(t, envInput.Required)
	assert.Equal(t, "staging", envInput.Default)
	assert.Equal(t, "choice", envInput.Type)
	assert.Equal(t, []string{"staging", "production"}, envInput.Options)

	require.Contains(t, wd.Inputs, "debug")
	assert.Equal(t, "boolean", wd.Inputs["debug"].Type)

	// Schedule
	sched := w.On.Events["schedule"]
	require.NotNil(t, sched)
	assert.Equal(t, "0 2 * * 1", sched.Cron)

	// Permissions
	require.NotNil(t, w.Permissions)
	assert.Equal(t, PermissionRead, w.Permissions.Scopes["contents"])
	assert.Equal(t, PermissionWrite, w.Permissions.Scopes["issues"])

	// Concurrency
	require.NotNil(t, w.Concurrency)
	assert.Equal(t, "${{ github.workflow }}-${{ github.ref }}", w.Concurrency.Group)
	assert.True(t, w.Concurrency.CancelInProgress)

	// Job permissions
	check := w.Jobs["check"]
	require.NotNil(t, check)
	require.NotNil(t, check.Permissions)
	assert.Equal(t, PermissionRead, check.Permissions.Scopes["contents"])

	// Validate
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_ReusableWorkflow(t *testing.T) {
	w, err := ParseFile(testdataPath("reusable-workflow.yml"))
	require.NoError(t, err)

	assert.Equal(t, "Reusable Caller", w.Name)

	// Reusable job
	require.Contains(t, w.Jobs, "call-reusable")
	reusable := w.Jobs["call-reusable"]
	assert.Equal(t, "octo-org/example-repo/.github/workflows/reusable.yml@main", reusable.Uses)
	assert.Equal(t, "production", reusable.With["environment"])
	assert.Equal(t, "inherit", reusable.Secrets)

	// After reusable job
	require.Contains(t, w.Jobs, "after-reusable")
	after := w.Jobs["after-reusable"]
	assert.Equal(t, StringOrSlice{"call-reusable"}, after.Needs)
	require.Len(t, after.Steps, 1)

	// Validate
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_Services(t *testing.T) {
	w, err := ParseFile(testdataPath("services.yml"))
	require.NoError(t, err)

	assert.Equal(t, "Service Containers", w.Name)

	require.Contains(t, w.Jobs, "integration")
	job := w.Jobs["integration"]

	// Services
	require.Contains(t, job.Services, "postgres")
	pg := job.Services["postgres"]
	assert.Equal(t, "postgres:15", pg.Image)
	assert.Equal(t, "postgres", pg.Env["POSTGRES_PASSWORD"])
	assert.Equal(t, "test", pg.Env["POSTGRES_DB"])
	assert.Equal(t, []string{"5432:5432"}, pg.Ports)
	assert.Contains(t, pg.Options, "--health-cmd pg_isready")

	require.Contains(t, job.Services, "redis")
	redis := job.Services["redis"]
	assert.Equal(t, "redis:7", redis.Image)
	assert.Equal(t, []string{"6379:6379"}, redis.Ports)

	// Container
	require.NotNil(t, job.Container)
	assert.Equal(t, "node:20", job.Container.Image)
	assert.Equal(t, "postgres://postgres:postgres@postgres:5432/test", job.Container.Env["DATABASE_URL"])

	// Defaults
	require.NotNil(t, job.Defaults)
	require.NotNil(t, job.Defaults.Run)
	assert.Equal(t, "bash", job.Defaults.Run.Shell)
	assert.Equal(t, "./src", job.Defaults.Run.WorkingDirectory)

	// Steps
	require.Len(t, job.Steps, 2)
	assert.Equal(t, "actions/checkout@v4", job.Steps[0].Uses)
	assert.Equal(t, "npm test", job.Steps[1].Run)
	assert.Equal(t, "redis://redis:6379", job.Steps[1].Env["REDIS_URL"])

	// Validate
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_FromReader(t *testing.T) {
	input := `
name: Simple
on: push
jobs:
  hello:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, "Simple", w.Name)
	assert.Contains(t, w.On.Events, "push")
	require.Contains(t, w.Jobs, "hello")
}

func TestParse_InvalidYAML(t *testing.T) {
	input := `
name: Bad
on: push
jobs:
  hello:
    runs-on: [
`
	_, err := Parse(strings.NewReader(input))
	assert.Error(t, err)
}

func TestParse_NonexistentFile(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/to/file.yml")
	assert.Error(t, err)
}

func TestParse_EmptyWorkflow(t *testing.T) {
	input := `
name: Empty
on: push
jobs: {}
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	assert.Empty(t, w.Jobs)

	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "at least one job")
}

func TestParse_StepWithContinueOnError(t *testing.T) {
	input := `
name: COE Test
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    continue-on-error: true
    steps:
      - run: echo 1
        continue-on-error: true
      - run: echo 2
        continue-on-error: ${{ matrix.experimental }}
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	job := w.Jobs["test"]
	assert.True(t, job.ContinueOnError.Value)
	assert.False(t, job.ContinueOnError.IsExpr)

	assert.True(t, job.Steps[0].ContinueOnError.Value)
	assert.False(t, job.Steps[0].ContinueOnError.IsExpr)

	assert.True(t, job.Steps[1].ContinueOnError.IsExpr)
	assert.Equal(t, "${{ matrix.experimental }}", job.Steps[1].ContinueOnError.Expression)
}

func TestParse_TimeoutMinutes(t *testing.T) {
	input := `
name: Timeout Test
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - run: echo 1
        timeout-minutes: 5
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	job := w.Jobs["test"]
	require.NotNil(t, job.TimeoutMinutes)
	assert.Equal(t, 30, *job.TimeoutMinutes)

	require.NotNil(t, job.Steps[0].TimeoutMinutes)
	assert.Equal(t, 5, *job.Steps[0].TimeoutMinutes)
}
