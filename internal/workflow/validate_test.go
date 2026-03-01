package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_ValidWorkflow(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{Run: "echo hello"},
				},
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_NoJobs(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "at least one job")
}

func TestValidate_JobWithStepsAndUses(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Uses: "org/repo/.github/workflows/reusable.yml@main",
				Steps: []Step{
					{Run: "echo hello"},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "cannot have both 'steps' and 'uses'")
}

func TestValidate_JobWithNeitherStepsNorUses(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"empty": {},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "must have either 'steps' or 'uses'")
}

func TestValidate_StepWithBothRunAndUses(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{
						Run:  "echo hello",
						Uses: "actions/checkout@v4",
					},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "cannot have both 'run' and 'uses'")
}

func TestValidate_StepWithNeitherRunNorUses(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{Name: "empty step"},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "must have either 'run' or 'uses'")
}

func TestValidate_DuplicateStepIDs(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{ID: "step1", Run: "echo 1"},
					{ID: "step1", Run: "echo 2"},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "duplicate step id")
}

func TestValidate_NeedsReferencesUnknownJob(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"deploy": {
				Needs: StringOrSlice{"nonexistent"},
				Steps: []Step{
					{Run: "echo deploy"},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "unknown job")
	assert.Contains(t, errs[0].Error(), "nonexistent")
}

func TestValidate_NeedsReferencesExistingJob(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{Run: "echo build"},
				},
			},
			"deploy": {
				Needs: StringOrSlice{"build"},
				Steps: []Step{
					{Run: "echo deploy"},
				},
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_InvalidJobName(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"123-invalid": {
				Steps: []Step{
					{Run: "echo hello"},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "not a valid identifier")
}

func TestValidate_ValidJobNames(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build":      {Steps: []Step{{Run: "echo"}}},
			"_private":   {Steps: []Step{{Run: "echo"}}},
			"test-suite": {Steps: []Step{{Run: "echo"}}},
			"build_v2":   {Steps: []Step{{Run: "echo"}}},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_MultipleErrors(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{ID: "s1", Run: "echo 1"},
					{ID: "s1", Run: "echo 2"},                  // duplicate ID
					{Name: "empty"},                             // no run or uses
					{Run: "echo", Uses: "actions/checkout@v4"},  // both run and uses
				},
			},
			"deploy": {
				Needs: StringOrSlice{"nonexistent"},
				Steps: []Step{
					{Run: "echo deploy"},
				},
			},
		},
	}
	errs := Validate(w)
	// Should find: duplicate step ID, step with neither, step with both, unknown needs
	assert.Len(t, errs, 4)
}

func TestValidate_ReusableWorkflow(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"call-reusable": {
				Uses: "org/repo/.github/workflows/reusable.yml@main",
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_StepIDsCanBeEmptyWithoutConflict(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{Run: "echo 1"},  // no ID
					{Run: "echo 2"},  // no ID
					{Run: "echo 3"},  // no ID
				},
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}
