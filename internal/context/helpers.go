package context

import (
	"github.com/emaland/ions/internal/expression"
)

// MatrixContext builds the "matrix" context from matrix values.
// Converts Go native types to expression Values.
func MatrixContext(values map[string]any) expression.Value {
	fields := make(map[string]expression.Value)

	for k, v := range values {
		fields[k] = toExpressionValue(v)
	}

	return expression.Object(fields)
}

// SecretsContext builds the "secrets" context from secrets.
func SecretsContext(secrets map[string]string) expression.Value {
	fields := make(map[string]expression.Value)
	for k, v := range secrets {
		fields[k] = expression.String(v)
	}
	return expression.Object(fields)
}

// InputsContext builds the "inputs" context.
func InputsContext(inputs map[string]string) expression.Value {
	fields := make(map[string]expression.Value)
	for k, v := range inputs {
		fields[k] = expression.String(v)
	}
	return expression.Object(fields)
}

// VarsContext builds the "vars" context.
func VarsContext(vars map[string]string) expression.Value {
	fields := make(map[string]expression.Value)
	for k, v := range vars {
		fields[k] = expression.String(v)
	}
	return expression.Object(fields)
}

// toExpressionValue converts a Go native value to an expression.Value.
func toExpressionValue(v any) expression.Value {
	if v == nil {
		return expression.Null()
	}
	switch val := v.(type) {
	case string:
		return expression.String(val)
	case bool:
		return expression.Bool(val)
	case int:
		return expression.Number(float64(val))
	case int8:
		return expression.Number(float64(val))
	case int16:
		return expression.Number(float64(val))
	case int32:
		return expression.Number(float64(val))
	case int64:
		return expression.Number(float64(val))
	case uint:
		return expression.Number(float64(val))
	case uint8:
		return expression.Number(float64(val))
	case uint16:
		return expression.Number(float64(val))
	case uint32:
		return expression.Number(float64(val))
	case uint64:
		return expression.Number(float64(val))
	case float32:
		return expression.Number(float64(val))
	case float64:
		return expression.Number(val)
	case []any:
		items := make([]expression.Value, len(val))
		for i, item := range val {
			items[i] = toExpressionValue(item)
		}
		return expression.Array(items)
	case map[string]any:
		fields := make(map[string]expression.Value, len(val))
		for k, item := range val {
			fields[k] = toExpressionValue(item)
		}
		return expression.Object(fields)
	default:
		// Fallback: convert to string
		return expression.Null()
	}
}
