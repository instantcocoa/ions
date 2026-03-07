package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Triggers can be: string ("push"), []string (["push", "pull_request"]),
// or map[string]EventConfig (push: {branches: [main]}).
type Triggers struct {
	Events map[string]*EventConfig
}

// UnmarshalYAML handles all three forms of triggers.
func (t *Triggers) UnmarshalYAML(value *yaml.Node) error {
	t.Events = make(map[string]*EventConfig)

	switch value.Kind {
	case yaml.ScalarNode:
		// Single event name: "push"
		t.Events[value.Value] = nil
		return nil

	case yaml.SequenceNode:
		// List of event names: ["push", "pull_request"]
		var items []string
		if err := value.Decode(&items); err != nil {
			return fmt.Errorf("Triggers: failed to decode sequence: %w", err)
		}
		for _, item := range items {
			t.Events[item] = nil
		}
		return nil

	case yaml.MappingNode:
		// Map of event → config
		// We need to iterate key/value pairs in the mapping node
		for i := 0; i < len(value.Content)-1; i += 2 {
			keyNode := value.Content[i]
			valNode := value.Content[i+1]
			eventName := keyNode.Value

			if eventName == "schedule" {
				// schedule is an array of {cron: "..."} entries
				ec, err := parseSchedule(valNode)
				if err != nil {
					return fmt.Errorf("Triggers: failed to parse schedule: %w", err)
				}
				t.Events[eventName] = ec
			} else if valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null" {
				// Event with no config: pull_request:
				t.Events[eventName] = nil
			} else if valNode.Kind == yaml.MappingNode {
				var ec EventConfig
				if err := valNode.Decode(&ec); err != nil {
					return fmt.Errorf("Triggers: failed to decode event config for %q: %w", eventName, err)
				}
				t.Events[eventName] = &ec
			} else {
				// Null or empty value
				t.Events[eventName] = nil
			}
		}
		return nil

	default:
		return fmt.Errorf("Triggers: expected string, sequence, or mapping, got %v", value.Kind)
	}
}

func parseSchedule(node *yaml.Node) (*EventConfig, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("schedule: expected sequence, got %v", node.Kind)
	}

	// schedule is a list of {cron: "..."} objects
	// We take the first cron entry (or the last, GitHub uses all of them but we store one for simplicity)
	var schedules []struct {
		Cron string `yaml:"cron"`
	}
	if err := node.Decode(&schedules); err != nil {
		return nil, fmt.Errorf("schedule: failed to decode: %w", err)
	}
	if len(schedules) == 0 {
		return nil, nil
	}
	// Store the first cron expression
	return &EventConfig{
		Cron: schedules[0].Cron,
	}, nil
}

// EventConfig holds the configuration for a specific trigger event.
type EventConfig struct {
	Branches       []string                `yaml:"branches,omitempty"`
	BranchesIgnore []string                `yaml:"branches-ignore,omitempty"`
	Tags           []string                `yaml:"tags,omitempty"`
	TagsIgnore     []string                `yaml:"tags-ignore,omitempty"`
	Paths          []string                `yaml:"paths,omitempty"`
	PathsIgnore    []string                `yaml:"paths-ignore,omitempty"`
	Types          []string                `yaml:"types,omitempty"`
	Inputs         map[string]DispatchInput `yaml:"inputs,omitempty"`
	Cron           string                  `yaml:"-"` // set programmatically for schedule
	Secrets        map[string]SecretDef    `yaml:"secrets,omitempty"`
	Outputs        map[string]OutputDef    `yaml:"outputs,omitempty"`
}

// DispatchInput represents an input for workflow_dispatch.
type DispatchInput struct {
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     string   `yaml:"default"`
	Type        string   `yaml:"type"`
	Options     []string `yaml:"options"`
}

// WorkflowCallInputs returns the input definitions from the workflow_call trigger,
// or nil if this workflow doesn't have a workflow_call trigger.
func (t *Triggers) WorkflowCallInputs() map[string]DispatchInput {
	ec, ok := t.Events["workflow_call"]
	if !ok || ec == nil {
		return nil
	}
	return ec.Inputs
}

// WorkflowCallOutputs returns the output definitions from the workflow_call trigger,
// or nil if this workflow doesn't have a workflow_call trigger.
func (t *Triggers) WorkflowCallOutputs() map[string]OutputDef {
	ec, ok := t.Events["workflow_call"]
	if !ok || ec == nil {
		return nil
	}
	return ec.Outputs
}

// SecretDef represents a secret definition for workflow_call.
type SecretDef struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

// OutputDef represents an output definition for workflow_call.
type OutputDef struct {
	Description string `yaml:"description"`
	Value       string `yaml:"value"`
}
