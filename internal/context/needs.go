package context

import (
	"github.com/emaland/ions/internal/expression"
)

// NeedsContext builds the "needs" context object from completed job results.
// Only includes jobs listed in jobNeeds.
func NeedsContext(results map[string]*JobResult, jobNeeds []string) expression.Value {
	fields := make(map[string]expression.Value)

	for _, jobID := range jobNeeds {
		result, ok := results[jobID]
		if !ok || result == nil {
			continue
		}

		outputs := make(map[string]expression.Value)
		for k, v := range result.Outputs {
			outputs[k] = expression.String(v)
		}

		jobFields := map[string]expression.Value{
			"result":  expression.String(result.Result),
			"outputs": expression.Object(outputs),
		}

		fields[jobID] = expression.Object(jobFields)
	}

	return expression.Object(fields)
}
