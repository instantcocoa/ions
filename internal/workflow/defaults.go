package workflow

// Defaults holds default settings for a workflow or job.
type Defaults struct {
	Run *RunDefaults `yaml:"run,omitempty"`
}

// RunDefaults holds default settings for run steps.
type RunDefaults struct {
	Shell            string `yaml:"shell,omitempty"`
	WorkingDirectory string `yaml:"working-directory,omitempty"`
}
