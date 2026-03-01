package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Strategy holds the execution strategy for a job.
type Strategy struct {
	Matrix      *Matrix `yaml:"matrix,omitempty"`
	FailFast    *bool   `yaml:"fail-fast,omitempty"`
	MaxParallel *int    `yaml:"max-parallel,omitempty"`
}

// Matrix has special handling: "include" and "exclude" are reserved keys.
// All other keys are dimension lists. Values can also be an expression string.
type Matrix struct {
	Dimensions map[string][]interface{} // each value is a list of values
	Include    []map[string]interface{}
	Exclude    []map[string]interface{}
	Expression string // if the entire matrix is an expression like ${{ fromJSON(...) }}
}

// UnmarshalYAML handles both expression string and mapping forms.
func (m *Matrix) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Expression form
		m.Expression = value.Value
		return nil
	case yaml.MappingNode:
		m.Dimensions = make(map[string][]interface{})
		// Iterate key/value pairs
		for i := 0; i < len(value.Content)-1; i += 2 {
			keyNode := value.Content[i]
			valNode := value.Content[i+1]
			key := keyNode.Value

			switch key {
			case "include":
				entries, err := decodeListOfMaps(valNode)
				if err != nil {
					return fmt.Errorf("Matrix: failed to decode include: %w", err)
				}
				m.Include = entries
			case "exclude":
				entries, err := decodeListOfMaps(valNode)
				if err != nil {
					return fmt.Errorf("Matrix: failed to decode exclude: %w", err)
				}
				m.Exclude = entries
			default:
				// Dimension: a list of values
				vals, err := decodeDimensionValues(valNode)
				if err != nil {
					return fmt.Errorf("Matrix: failed to decode dimension %q: %w", key, err)
				}
				m.Dimensions[key] = vals
			}
		}
		return nil
	default:
		return fmt.Errorf("Matrix: expected string or mapping, got %v", value.Kind)
	}
}

// decodeListOfMaps decodes a YAML sequence of mappings into []map[string]interface{}.
func decodeListOfMaps(node *yaml.Node) ([]map[string]interface{}, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected sequence, got %v", node.Kind)
	}
	var result []map[string]interface{}
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("expected mapping in sequence, got %v", item.Kind)
		}
		m := make(map[string]interface{})
		for j := 0; j < len(item.Content)-1; j += 2 {
			k := item.Content[j].Value
			v := decodeScalarValue(item.Content[j+1])
			m[k] = v
		}
		result = append(result, m)
	}
	return result, nil
}

// decodeDimensionValues decodes a YAML sequence of scalar values into []interface{}.
func decodeDimensionValues(node *yaml.Node) ([]interface{}, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected sequence, got %v", node.Kind)
	}
	var result []interface{}
	for _, item := range node.Content {
		result = append(result, decodeScalarValue(item))
	}
	return result, nil
}

// decodeScalarValue converts a YAML scalar node to a Go value.
func decodeScalarValue(node *yaml.Node) interface{} {
	if node.Kind != yaml.ScalarNode {
		// For non-scalar values, just return the string representation
		var v interface{}
		_ = node.Decode(&v)
		return v
	}

	// Use yaml.v3's own type detection via tags
	var v interface{}
	if err := node.Decode(&v); err != nil {
		return node.Value
	}
	return v
}
