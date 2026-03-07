package workflow

// Workflow represents a parsed GitHub Actions workflow file.
type Workflow struct {
	Name        string            `yaml:"name"`
	RunName     string            `yaml:"run-name,omitempty"`
	On          Triggers          `yaml:"on"`
	Env         map[string]string `yaml:"env,omitempty"`
	Defaults    *Defaults         `yaml:"defaults,omitempty"`
	Concurrency *Concurrency      `yaml:"concurrency,omitempty"`
	Permissions *Permissions      `yaml:"permissions,omitempty"`
	Jobs        map[string]*Job   `yaml:"jobs"`
}

// Job represents a single job within a workflow.
type Job struct {
	Name            string                 `yaml:"name,omitempty"`
	RunsOn          RunsOn                 `yaml:"runs-on"`
	Needs           StringOrSlice          `yaml:"needs,omitempty"`
	If              string                 `yaml:"if,omitempty"`
	Permissions     *Permissions           `yaml:"permissions,omitempty"`
	Environment     *Environment           `yaml:"environment,omitempty"`
	Concurrency     *Concurrency           `yaml:"concurrency,omitempty"`
	Outputs         map[string]JobOutput   `yaml:"outputs,omitempty"`
	Env             map[string]string      `yaml:"env,omitempty"`
	Defaults        *Defaults              `yaml:"defaults,omitempty"`
	Strategy        *Strategy              `yaml:"strategy,omitempty"`
	Container       *Container             `yaml:"container,omitempty"`
	Services        map[string]*Container  `yaml:"services,omitempty"`
	Steps           []Step                 `yaml:"steps"`
	TimeoutMinutes  *int                   `yaml:"timeout-minutes,omitempty"`
	ContinueOnError ExprBool              `yaml:"continue-on-error,omitempty"`
	// For reusable workflows
	Uses    string                 `yaml:"uses,omitempty"`
	With    map[string]interface{} `yaml:"with,omitempty"`
	Secrets interface{}            `yaml:"secrets,omitempty"` // "inherit" or map
}

// Step represents a single step within a job.
type Step struct {
	ID               string            `yaml:"id,omitempty"`
	Name             string            `yaml:"name,omitempty"`
	If               string            `yaml:"if,omitempty"`
	Uses             string            `yaml:"uses,omitempty"`
	Run              string            `yaml:"run,omitempty"`
	Shell            string            `yaml:"shell,omitempty"`
	With             map[string]string `yaml:"with,omitempty"`
	Env              map[string]string `yaml:"env,omitempty"`
	ContinueOnError  ExprBool          `yaml:"continue-on-error,omitempty"`
	TimeoutMinutes   *int              `yaml:"timeout-minutes,omitempty"`
	WorkingDirectory string            `yaml:"working-directory,omitempty"`
	Entrypoint       string            `yaml:"entrypoint,omitempty"`
	Args             string            `yaml:"args,omitempty"`
}

// JobOutput represents an output from a job.
// Supports both short form (plain string) and long form (object with value/description).
//
// Short form (regular jobs):
//
//	outputs:
//	  version: ${{ steps.v.outputs.version }}
//
// Long form (reusable workflows):
//
//	outputs:
//	  version:
//	    description: The version
//	    value: ${{ steps.v.outputs.version }}
type JobOutput struct {
	Description string `yaml:"description,omitempty"`
	Value       string `yaml:"value"`
}

// UnmarshalYAML handles both plain string and object formats for job outputs.
func (j *JobOutput) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try plain string first (short form).
	var s string
	if err := unmarshal(&s); err == nil {
		j.Value = s
		return nil
	}
	// Try object form.
	type jobOutputAlias JobOutput
	var obj jobOutputAlias
	if err := unmarshal(&obj); err != nil {
		return err
	}
	*j = JobOutput(obj)
	return nil
}
