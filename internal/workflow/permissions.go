package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PermissionLevel represents a permission level for a scope.
type PermissionLevel string

const (
	PermissionRead  PermissionLevel = "read"
	PermissionWrite PermissionLevel = "write"
	PermissionNone  PermissionLevel = "none"
)

// Permissions can be "read-all", "write-all", or a map of scope to level.
type Permissions struct {
	ReadAll  bool
	WriteAll bool
	Scopes   map[string]PermissionLevel
}

// UnmarshalYAML handles string ("read-all", "write-all") and mapping forms.
func (p *Permissions) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		switch value.Value {
		case "read-all":
			p.ReadAll = true
		case "write-all":
			p.WriteAll = true
		default:
			return fmt.Errorf("Permissions: unknown permission level %q, expected 'read-all' or 'write-all'", value.Value)
		}
		return nil
	case yaml.MappingNode:
		var scopes map[string]PermissionLevel
		if err := value.Decode(&scopes); err != nil {
			return fmt.Errorf("Permissions: failed to decode mapping: %w", err)
		}
		p.Scopes = scopes
		return nil
	default:
		return fmt.Errorf("Permissions: expected string or mapping, got %v", value.Kind)
	}
}
