package workflow

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Parse reads a workflow from an io.Reader and returns a parsed Workflow.
func Parse(r io.Reader) (*Workflow, error) {
	var w Workflow
	decoder := yaml.NewDecoder(r)
	if err := decoder.Decode(&w); err != nil {
		return nil, fmt.Errorf("failed to parse workflow YAML: %w", err)
	}
	return &w, nil
}

// ParseFile reads a workflow from a file path and returns a parsed Workflow.
func ParseFile(path string) (*Workflow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open workflow file %q: %w", path, err)
	}
	defer f.Close()

	w, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("error parsing %q: %w", path, err)
	}
	return w, nil
}
