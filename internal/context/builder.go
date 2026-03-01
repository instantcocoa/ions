package context

import (
	"github.com/emaland/ions/internal/expression"
)

// StepResult holds the result of a completed step.
type StepResult struct {
	Outcome    string            // "success", "failure", "cancelled", "skipped"
	Conclusion string            // same as outcome unless continue-on-error changes it
	Outputs    map[string]string
}

// JobResult holds the result of a completed job.
type JobResult struct {
	Result  string            // "success", "failure", "cancelled", "skipped"
	Outputs map[string]string
}

// BuilderOptions configures what context to build.
type BuilderOptions struct {
	// Git repo path (for github context)
	RepoPath string

	// Workflow-level env
	WorkflowEnv map[string]string
	// Job-level env
	JobEnv map[string]string
	// Step-level env (highest precedence)
	StepEnv map[string]string

	// Event name override
	EventName string
	// Workflow name
	WorkflowName string
	// Run ID
	RunID string
	// Run number
	RunNumber int

	// Secrets
	Secrets map[string]string
	// Vars
	Vars map[string]string
	// Inputs (workflow_dispatch)
	Inputs map[string]string

	// Matrix values for current job
	MatrixValues map[string]any

	// Step results accumulated so far
	StepResults map[string]*StepResult
	// Job results accumulated so far (for needs context)
	JobResults map[string]*JobResult
	// Which jobs this job depends on
	JobNeeds []string

	// APIBaseURL overrides the default https://api.github.com for the
	// github.api_url and github.graphql_url context fields.
	// Set by the orchestrator to route API calls through the local stub.
	APIBaseURL string

	// GitHubToken overrides the default dummy token in github.token.
	GitHubToken string
}

// Builder constructs expression contexts.
type Builder struct {
	opts BuilderOptions
}

// NewBuilder creates a new Builder with the given options.
func NewBuilder(opts BuilderOptions) *Builder {
	return &Builder{opts: opts}
}

// FullContext builds a complete MapContext with all sub-contexts.
// Returns a MapContext where top-level keys are "github", "env", "steps", "needs",
// "matrix", "secrets", "inputs", "vars", "runner".
func (b *Builder) FullContext() expression.MapContext {
	return expression.MapContext{
		"github":   GitHubContext(b.opts),
		"env":      EnvContext(b.opts.WorkflowEnv, b.opts.JobEnv, b.opts.StepEnv),
		"runner":   RunnerContext(),
		"steps":    StepsContext(b.opts.StepResults),
		"needs":    NeedsContext(b.opts.JobResults, b.opts.JobNeeds),
		"matrix":   MatrixContext(b.opts.MatrixValues),
		"secrets":  SecretsContext(b.opts.Secrets),
		"inputs":   InputsContext(b.opts.Inputs),
		"vars":     VarsContext(b.opts.Vars),
		"strategy": expression.Object(map[string]expression.Value{}),
		"job":      expression.Object(map[string]expression.Value{}),
	}
}
