package context

import (
	"github.com/emaland/ions/internal/expression"
)

// EnvContext builds the "env" context object.
// Merges with precedence: step > job > workflow (step overrides job overrides workflow).
func EnvContext(workflowEnv, jobEnv, stepEnv map[string]string) expression.Value {
	merged := make(map[string]expression.Value)

	// Apply in order of increasing precedence: workflow, then job, then step.
	for k, v := range workflowEnv {
		merged[k] = expression.String(v)
	}
	for k, v := range jobEnv {
		merged[k] = expression.String(v)
	}
	for k, v := range stepEnv {
		merged[k] = expression.String(v)
	}

	return expression.Object(merged)
}
