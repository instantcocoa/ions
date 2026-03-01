package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// StringOrSlice can unmarshal from "foo" or ["foo", "bar"].
type StringOrSlice []string

// UnmarshalYAML handles both scalar string and sequence forms.
func (s *StringOrSlice) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*s = []string{value.Value}
		return nil
	case yaml.SequenceNode:
		var items []string
		if err := value.Decode(&items); err != nil {
			return fmt.Errorf("StringOrSlice: failed to decode sequence: %w", err)
		}
		*s = items
		return nil
	default:
		return fmt.Errorf("StringOrSlice: expected string or sequence, got %v", value.Kind)
	}
}

// RunsOn represents the runs-on field which can be a string, []string, or object {group, labels}.
type RunsOn struct {
	Labels []string
	Group  string
}

// UnmarshalYAML handles string, sequence, and mapping forms.
func (r *RunsOn) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		r.Labels = []string{value.Value}
		return nil
	case yaml.SequenceNode:
		var items []string
		if err := value.Decode(&items); err != nil {
			return fmt.Errorf("RunsOn: failed to decode sequence: %w", err)
		}
		r.Labels = items
		return nil
	case yaml.MappingNode:
		var obj struct {
			Group  string   `yaml:"group"`
			Labels []string `yaml:"labels"`
		}
		if err := value.Decode(&obj); err != nil {
			return fmt.Errorf("RunsOn: failed to decode mapping: %w", err)
		}
		r.Group = obj.Group
		r.Labels = obj.Labels
		return nil
	default:
		return fmt.Errorf("RunsOn: expected string, sequence, or mapping, got %v", value.Kind)
	}
}

// ExprBool can be a boolean or an expression string like "${{ inputs.debug }}".
type ExprBool struct {
	Expression string
	Value      bool
	IsExpr     bool
}

// UnmarshalYAML handles both boolean and expression string forms.
func (e *ExprBool) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Try boolean first
		var b bool
		if err := value.Decode(&b); err == nil && (value.Tag == "!!bool" || value.Value == "true" || value.Value == "false") {
			e.Value = b
			e.IsExpr = false
			return nil
		}
		// Otherwise treat as expression string
		e.Expression = value.Value
		e.IsExpr = true
		return nil
	default:
		return fmt.Errorf("ExprBool: expected bool or string, got %v", value.Kind)
	}
}

// Environment can be a string (environment name) or object {name, url}.
type Environment struct {
	Name string
	URL  string
}

// UnmarshalYAML handles both string and mapping forms.
func (e *Environment) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		e.Name = value.Value
		return nil
	case yaml.MappingNode:
		var obj struct {
			Name string `yaml:"name"`
			URL  string `yaml:"url"`
		}
		if err := value.Decode(&obj); err != nil {
			return fmt.Errorf("Environment: failed to decode mapping: %w", err)
		}
		e.Name = obj.Name
		e.URL = obj.URL
		return nil
	default:
		return fmt.Errorf("Environment: expected string or mapping, got %v", value.Kind)
	}
}
