package context

import (
	"github.com/emaland/ions/internal/expression"
)

// StepsContext builds the "steps" context object from accumulated step results.
func StepsContext(results map[string]*StepResult) expression.Value {
	fields := make(map[string]expression.Value)

	for id, result := range results {
		if result == nil {
			continue
		}

		outputs := make(map[string]expression.Value)
		for k, v := range result.Outputs {
			outputs[k] = expression.String(v)
		}

		stepFields := map[string]expression.Value{
			"outcome":    expression.String(result.Outcome),
			"conclusion": expression.String(result.Conclusion),
			"outputs":    expression.Object(outputs),
		}

		fields[id] = expression.Object(stepFields)
	}

	return expression.Object(fields)
}
