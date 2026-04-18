package workflow

import (
	"fmt"
	"strings"

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

// EnvMap is the value of an `env:` block. GitHub permits either a mapping
// of literal KEY=VALUE pairs, or a single `${{ ... }}` expression that
// evaluates to such a mapping at runtime (e.g. reusable-workflow inputs
// that serialize env as JSON via `fromJson(inputs.env-vars)`).
type EnvMap struct {
	Values     map[string]string
	Expression string
}

// Map returns the resolved key/value pairs. When the env block is an
// unresolved expression this is nil; callers that need the expression can
// read it from Expression.
func (e EnvMap) Map() map[string]string { return e.Values }

// Len returns the number of resolved entries. An unresolved expression
// reports zero, matching the zero-value semantics of `map[string]string`.
func (e EnvMap) Len() int { return len(e.Values) }

// UnmarshalYAML accepts either a mapping form or a scalar expression.
func (e *EnvMap) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		v := strings.TrimSpace(value.Value)
		if strings.HasPrefix(v, "${{") && strings.HasSuffix(v, "}}") {
			e.Expression = value.Value
			return nil
		}
		return fmt.Errorf("env: expected mapping or ${{ expression }}, got scalar %q", value.Value)
	case yaml.MappingNode:
		m := make(map[string]string)
		if err := value.Decode(&m); err != nil {
			return fmt.Errorf("env: failed to decode mapping: %w", err)
		}
		e.Values = m
		return nil
	default:
		return fmt.Errorf("env: expected mapping or expression, got %v", value.Kind)
	}
}

// MarshalYAML emits the mapping form (or the raw expression when unresolved).
// Emitting nil-wrapped-in-empty would break round-trip tests.
func (e EnvMap) MarshalYAML() (interface{}, error) {
	if e.Expression != "" && len(e.Values) == 0 {
		return e.Expression, nil
	}
	return e.Values, nil
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
