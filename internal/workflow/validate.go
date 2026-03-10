package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

// validJobNameRe matches valid job identifiers: start with letter or _, contain only alphanum, -, _
var validJobNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// validStepIDRe matches valid step IDs: alphanumeric, hyphens, underscores.
var validStepIDRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

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

		// Validate reusable workflow uses format.
		if hasUses {
			if err := validateJobUses(name, job.Uses); err != nil {
				errs = append(errs, err)
			}
		}

		// Validate matrix dimensions are non-empty.
		if job.Strategy != nil && job.Strategy.Matrix != nil {
			for dim, vals := range job.Strategy.Matrix.Dimensions {
				if len(vals) == 0 {
					errs = append(errs, fmt.Errorf("job %q: matrix dimension %q has no values", name, dim))
				}
			}
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

			// Validate step uses format.
			if hasStepUses {
				if err := validateStepUses(name, i+1, step.Uses); err != nil {
					errs = append(errs, err)
				}
			}

			// Step IDs within a job must be unique and valid.
			if step.ID != "" {
				if !validStepIDRe.MatchString(step.ID) {
					errs = append(errs, fmt.Errorf("job %q, step %d: id %q is not a valid identifier", name, i+1, step.ID))
				}
				if stepIDs[step.ID] {
					errs = append(errs, fmt.Errorf("job %q: duplicate step id %q", name, step.ID))
				}
				stepIDs[step.ID] = true
			}
		}

		// Job needs must reference jobs that exist and not self-reference.
		for _, need := range job.Needs {
			if need == name {
				errs = append(errs, fmt.Errorf("job %q: cannot depend on itself", name))
			} else if !jobNames[need] {
				errs = append(errs, fmt.Errorf("job %q: needs references unknown job %q", name, need))
			}
		}
	}

	return errs
}

// validateJobUses checks that a reusable workflow reference is well-formed.
// Valid formats: "./path/to/workflow.yml", "owner/repo/.github/workflows/file.yml@ref"
func validateJobUses(jobName, uses string) error {
	if strings.HasPrefix(uses, "./") {
		// Local reusable workflow — must end in .yml or .yaml.
		if !strings.HasSuffix(uses, ".yml") && !strings.HasSuffix(uses, ".yaml") {
			return fmt.Errorf("job %q: local reusable workflow %q must end in .yml or .yaml", jobName, uses)
		}
		return nil
	}
	// Remote: owner/repo/path@ref
	atIdx := strings.LastIndex(uses, "@")
	if atIdx < 0 {
		return fmt.Errorf("job %q: reusable workflow %q missing @ref", jobName, uses)
	}
	nameWithPath := uses[:atIdx]
	ref := uses[atIdx+1:]
	if ref == "" {
		return fmt.Errorf("job %q: reusable workflow %q has empty ref", jobName, uses)
	}
	parts := strings.SplitN(nameWithPath, "/", 3)
	if len(parts) < 3 {
		return fmt.Errorf("job %q: reusable workflow %q must be owner/repo/path@ref", jobName, uses)
	}
	if !strings.HasSuffix(parts[2], ".yml") && !strings.HasSuffix(parts[2], ".yaml") {
		return fmt.Errorf("job %q: reusable workflow %q path must end in .yml or .yaml", jobName, uses)
	}
	return nil
}

// validateStepUses checks that a step action reference is well-formed.
// Valid formats: "./path", "docker://image", "owner/repo@ref", "owner/repo/path@ref"
func validateStepUses(jobName string, stepNum int, uses string) error {
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "../") {
		return nil // local action
	}
	if strings.HasPrefix(uses, "docker://") {
		image := strings.TrimPrefix(uses, "docker://")
		if image == "" {
			return fmt.Errorf("job %q, step %d: docker action %q has empty image", jobName, stepNum, uses)
		}
		return nil
	}
	// Remote: owner/repo@ref or owner/repo/path@ref
	atIdx := strings.LastIndex(uses, "@")
	if atIdx < 0 {
		return fmt.Errorf("job %q, step %d: action %q missing @version", jobName, stepNum, uses)
	}
	ref := uses[atIdx+1:]
	if ref == "" {
		return fmt.Errorf("job %q, step %d: action %q has empty version", jobName, stepNum, uses)
	}
	nameWithPath := uses[:atIdx]
	parts := strings.Split(nameWithPath, "/")
	if len(parts) < 2 {
		return fmt.Errorf("job %q, step %d: action %q must be owner/repo@ref", jobName, stepNum, uses)
	}
	return nil
}
