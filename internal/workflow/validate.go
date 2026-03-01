package workflow

import (
	"fmt"
	"regexp"
)

// validJobNameRe matches valid job identifiers: start with letter or _, contain only alphanum, -, _
var validJobNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// Validate performs structural validation on a parsed workflow.
// It returns a slice of all errors found (does not stop at first).
func Validate(w *Workflow) []error {
	var errs []error

	// Workflow must have at least one job
	if len(w.Jobs) == 0 {
		errs = append(errs, fmt.Errorf("workflow must have at least one job"))
	}

	// Collect all job names for reference validation
	jobNames := make(map[string]bool)
	for name := range w.Jobs {
		jobNames[name] = true
	}

	for name, job := range w.Jobs {
		// Job names must be valid identifiers
		if !validJobNameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("job %q: name is not a valid identifier (must start with letter or underscore, contain only alphanumeric, hyphens, or underscores)", name))
		}

		// Each job must have either steps or uses (reusable workflow), not both, not neither
		hasSteps := len(job.Steps) > 0
		hasUses := job.Uses != ""

		if hasSteps && hasUses {
			errs = append(errs, fmt.Errorf("job %q: cannot have both 'steps' and 'uses'", name))
		}
		if !hasSteps && !hasUses {
			errs = append(errs, fmt.Errorf("job %q: must have either 'steps' or 'uses'", name))
		}

		// Validate steps
		stepIDs := make(map[string]bool)
		for i, step := range job.Steps {
			// Each step must have either run or uses, not both, not neither
			hasRun := step.Run != ""
			hasStepUses := step.Uses != ""

			if hasRun && hasStepUses {
				errs = append(errs, fmt.Errorf("job %q, step %d: cannot have both 'run' and 'uses'", name, i+1))
			}
			if !hasRun && !hasStepUses {
				errs = append(errs, fmt.Errorf("job %q, step %d: must have either 'run' or 'uses'", name, i+1))
			}

			// Step IDs within a job must be unique
			if step.ID != "" {
				if stepIDs[step.ID] {
					errs = append(errs, fmt.Errorf("job %q: duplicate step id %q", name, step.ID))
				}
				stepIDs[step.ID] = true
			}
		}

		// Job needs must reference jobs that exist
		for _, need := range job.Needs {
			if !jobNames[need] {
				errs = append(errs, fmt.Errorf("job %q: needs references unknown job %q", name, need))
			}
		}
	}

	return errs
}
