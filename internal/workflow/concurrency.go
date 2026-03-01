package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Concurrency can be a string (group name) or object {group, cancel-in-progress}.
type Concurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
}

// UnmarshalYAML handles both string and mapping forms.
func (c *Concurrency) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		c.Group = value.Value
		return nil
	case yaml.MappingNode:
		// Use alias to avoid infinite recursion
		type concurrencyAlias Concurrency
		var alias concurrencyAlias
		if err := value.Decode(&alias); err != nil {
			return fmt.Errorf("Concurrency: failed to decode mapping: %w", err)
		}
		*c = Concurrency(alias)
		return nil
	default:
		return fmt.Errorf("Concurrency: expected string or mapping, got %v", value.Kind)
	}
}
