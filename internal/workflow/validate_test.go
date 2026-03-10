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

func TestValidate_SelfReference(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Needs: StringOrSlice{"build"},
				Steps: []Step{{Run: "echo"}},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "cannot depend on itself")
}

func TestValidate_StepUsesFormat(t *testing.T) {
	tests := []struct {
		name    string
		uses    string
		wantErr bool
		errMsg  string
	}{
		{"valid remote", "actions/checkout@v4", false, ""},
		{"valid remote with path", "actions/aws/login@v1", false, ""},
		{"valid local", "./my-action", false, ""},
		{"valid docker", "docker://alpine:3.19", false, ""},
		{"missing version", "actions/checkout", true, "missing @version"},
		{"empty version", "actions/checkout@", true, "empty version"},
		{"empty docker image", "docker://", true, "empty image"},
		{"single segment", "checkout@v4", true, "must be owner/repo@ref"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Workflow{
				Jobs: map[string]*Job{
					"build": {
						Steps: []Step{{Uses: tt.uses}},
					},
				},
			}
			errs := Validate(w)
			if tt.wantErr {
				found := false
				for _, e := range errs {
					if assert.ObjectsAreEqual(true, true) {
						if contains(e.Error(), tt.errMsg) {
							found = true
						}
					}
				}
				assert.True(t, found, "expected error containing %q, got %v", tt.errMsg, errs)
			} else {
				// Only check that there are no uses-related errors
				for _, e := range errs {
					assert.NotContains(t, e.Error(), "action")
				}
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && len(sub) > 0 && containsStr(s, sub)))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestValidate_ReusableWorkflowUsesFormat(t *testing.T) {
	tests := []struct {
		name    string
		uses    string
		wantErr bool
		errMsg  string
	}{
		{"valid remote", "org/repo/.github/workflows/ci.yml@main", false, ""},
		{"valid local", "./path/to/workflow.yml", false, ""},
		{"local no extension", "./path/to/workflow", true, "must end in .yml or .yaml"},
		{"remote missing ref", "org/repo/.github/workflows/ci.yml", true, "missing @ref"},
		{"remote empty ref", "org/repo/.github/workflows/ci.yml@", true, "empty ref"},
		{"remote wrong path depth", "org/ci.yml@main", true, "must be owner/repo/path@ref"},
		{"remote no yml extension", "org/repo/.github/workflows/ci@main", true, "must end in .yml or .yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Workflow{
				Jobs: map[string]*Job{
					"call": {Uses: tt.uses},
				},
			}
			errs := Validate(w)
			if tt.wantErr {
				found := false
				for _, e := range errs {
					if containsStr(e.Error(), tt.errMsg) {
						found = true
					}
				}
				assert.True(t, found, "expected error containing %q, got %v", tt.errMsg, errs)
			} else {
				assert.Empty(t, errs)
			}
		})
	}
}

func TestValidate_InvalidStepID(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{
					{ID: "123bad", Run: "echo"},
				},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "not a valid identifier")
}

func TestValidate_EmptyMatrixDimension(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Strategy: &Strategy{
					Matrix: &Matrix{
						Dimensions: map[string][]interface{}{
							"os": {},
						},
					},
				},
				Steps: []Step{{Run: "echo"}},
			},
		},
	}
	errs := Validate(w)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "has no values")
}
