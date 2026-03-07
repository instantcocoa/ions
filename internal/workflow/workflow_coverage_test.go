package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- JobOutput UnmarshalYAML tests ---

func TestJobOutput_ShortForm(t *testing.T) {
	input := `version: ${{ steps.v.outputs.version }}`
	var outputs map[string]JobOutput
	err := yaml.Unmarshal([]byte(input), &outputs)
	require.NoError(t, err)
	assert.Equal(t, "${{ steps.v.outputs.version }}", outputs["version"].Value)
	assert.Empty(t, outputs["version"].Description)
}

func TestJobOutput_LongForm(t *testing.T) {
	input := `
version:
  description: The version
  value: ${{ steps.v.outputs.version }}
`
	var outputs map[string]JobOutput
	err := yaml.Unmarshal([]byte(input), &outputs)
	require.NoError(t, err)
	assert.Equal(t, "${{ steps.v.outputs.version }}", outputs["version"].Value)
	assert.Equal(t, "The version", outputs["version"].Description)
}

func TestJobOutput_InvalidType(t *testing.T) {
	input := `
version:
  - not
  - valid
`
	var outputs map[string]JobOutput
	err := yaml.Unmarshal([]byte(input), &outputs)
	assert.Error(t, err)
}

// --- Trigger edge cases ---

func TestTriggers_InvalidType(t *testing.T) {
	// Triggers only accept string, sequence, or mapping
	var tr Triggers
	err := yaml.Unmarshal([]byte(`123.456`), &tr)
	// YAML scalar node but not sequence or mapping -- this is the scalar case
	require.NoError(t, err)
	assert.Contains(t, tr.Events, "123.456")
}

func TestTriggers_ScheduleNotSequence(t *testing.T) {
	input := `
schedule: "not a sequence"
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	assert.Error(t, err)
}

func TestTriggers_MappingEventWithSequenceValue(t *testing.T) {
	// An event config that is a non-null, non-mapping scalar
	input := `
workflow_dispatch:
pull_request:
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	assert.Nil(t, tr.Events["workflow_dispatch"])
	assert.Nil(t, tr.Events["pull_request"])
}

// --- Matrix edge cases ---

func TestMatrix_EmptyMapping(t *testing.T) {
	input := `{}`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	assert.Empty(t, m.Dimensions)
	assert.Nil(t, m.Include)
	assert.Nil(t, m.Exclude)
}

func TestMatrix_IncludeNotSequence(t *testing.T) {
	input := `
include: "not a sequence"
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	assert.Error(t, err)
}

func TestMatrix_ExcludeNotSequence(t *testing.T) {
	input := `
exclude: "not a sequence"
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	assert.Error(t, err)
}

func TestMatrix_DimensionNotSequence(t *testing.T) {
	input := `
os: "not a sequence"
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	assert.Error(t, err)
}

func TestMatrix_IncludeWithNonMappingItem(t *testing.T) {
	input := `
include:
  - "just a string"
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	assert.Error(t, err)
}

// --- decodeScalarValue edge cases ---

func TestDecodeScalarValue_NonScalar(t *testing.T) {
	// Test decodeScalarValue with a non-scalar node (sequence)
	input := `
data:
  - hello
  - world
`
	var node yaml.Node
	err := yaml.Unmarshal([]byte(input), &node)
	require.NoError(t, err)
	// Navigate to the sequence node: root -> mapping -> value
	require.NotNil(t, node.Content)
	mapping := node.Content[0]
	valNode := mapping.Content[1] // the sequence
	result := decodeScalarValue(valNode)
	// Non-scalar should decode via node.Decode
	assert.NotNil(t, result)
}

// --- RunsOn edge cases ---

func TestRunsOn_InvalidType(t *testing.T) {
	// A type that isn't scalar, sequence, or mapping
	var r RunsOn
	// YAML integers decode as scalar, so use a mapping with an invalid field
	err := yaml.Unmarshal([]byte(`123`), &r)
	require.NoError(t, err)
	assert.Equal(t, []string{"123"}, r.Labels)
}

// --- ExprBool edge cases ---

func TestExprBool_InvalidType(t *testing.T) {
	var e ExprBool
	err := yaml.Unmarshal([]byte(`[a, b]`), &e)
	assert.Error(t, err)
}

// --- Permissions edge cases ---

func TestPermissions_EmptyMapping(t *testing.T) {
	input := `{}`
	var p Permissions
	err := yaml.Unmarshal([]byte(input), &p)
	require.NoError(t, err)
	assert.False(t, p.ReadAll)
	assert.False(t, p.WriteAll)
	// Scopes is an empty map
	assert.Empty(t, p.Scopes)
}

// --- Concurrency edge cases ---

func TestConcurrency_MappingInvalidField(t *testing.T) {
	input := `
group: test
cancel-in-progress: true
extra: field
`
	var c Concurrency
	err := yaml.Unmarshal([]byte(input), &c)
	// Extra fields are ignored in YAML decoding
	require.NoError(t, err)
	assert.Equal(t, "test", c.Group)
}

// --- Validate edge cases ---

func TestValidate_ReusableWorkflowWithNeeds(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {
				Steps: []Step{{Run: "echo build"}},
			},
			"deploy": {
				Uses:  "org/repo/.github/workflows/deploy.yml@main",
				Needs: StringOrSlice{"build"},
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_MultipleInvalidJobNames(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"123bad":  {Steps: []Step{{Run: "echo"}}},
			"-start":  {Steps: []Step{{Run: "echo"}}},
			"good_ok": {Steps: []Step{{Run: "echo"}}},
		},
	}
	errs := Validate(w)
	// 123bad and -start are invalid
	invalidCount := 0
	for _, e := range errs {
		if strings.Contains(e.Error(), "not a valid identifier") {
			invalidCount++
		}
	}
	assert.Equal(t, 2, invalidCount)
}

func TestValidate_EmptyStepIDsDoNotConflict(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"test": {
				Steps: []Step{
					{Run: "echo 1"},
					{Run: "echo 2"},
				},
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_DuplicateStepIDsAndOtherErrors(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"test": {
				Steps: []Step{
					{ID: "dup", Run: "echo 1"},
					{ID: "dup", Uses: "actions/checkout@v4"},
					{Name: "no run or uses"},
				},
			},
		},
	}
	errs := Validate(w)
	// Errors: duplicate step ID, step 3 has neither run nor uses
	assert.GreaterOrEqual(t, len(errs), 2)
}

// --- Full workflow parsing edge cases ---

func TestParse_WorkflowWithAllFields(t *testing.T) {
	input := `
name: Full Workflow
run-name: "Build ${{ github.ref_name }}"
on:
  push:
    branches: [main]
  pull_request:
env:
  GLOBAL_KEY: value
defaults:
  run:
    shell: bash
    working-directory: ./src
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
permissions:
  contents: read
  issues: write
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    continue-on-error: false
    environment:
      name: production
      url: https://example.com
    concurrency: deploy-group
    permissions: read-all
    defaults:
      run:
        shell: pwsh
    outputs:
      version: ${{ steps.ver.outputs.version }}
    env:
      JOB_KEY: job-value
    steps:
      - id: ver
        name: Get version
        run: echo "version=1.0" >> $GITHUB_OUTPUT
        shell: bash
        env:
          STEP_KEY: step-value
        working-directory: ./app
        timeout-minutes: 5
        continue-on-error: true
      - uses: actions/checkout@v4
        with:
          fetch-depth: "0"
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	assert.Equal(t, "Full Workflow", w.Name)
	assert.Equal(t, "Build ${{ github.ref_name }}", w.RunName)
	assert.Equal(t, "value", w.Env["GLOBAL_KEY"])
	require.NotNil(t, w.Defaults)
	assert.Equal(t, "bash", w.Defaults.Run.Shell)
	require.NotNil(t, w.Concurrency)
	assert.True(t, w.Concurrency.CancelInProgress)
	require.NotNil(t, w.Permissions)
	assert.Equal(t, PermissionRead, w.Permissions.Scopes["contents"])

	job := w.Jobs["build"]
	require.NotNil(t, job)
	require.NotNil(t, job.TimeoutMinutes)
	assert.Equal(t, 30, *job.TimeoutMinutes)
	assert.False(t, job.ContinueOnError.Value)
	assert.False(t, job.ContinueOnError.IsExpr)
	require.NotNil(t, job.Environment)
	assert.Equal(t, "production", job.Environment.Name)
	require.NotNil(t, job.Concurrency)
	assert.Equal(t, "deploy-group", job.Concurrency.Group)
	require.NotNil(t, job.Permissions)
	assert.True(t, job.Permissions.ReadAll)
	require.NotNil(t, job.Defaults)
	assert.Equal(t, "pwsh", job.Defaults.Run.Shell)
	assert.Equal(t, "job-value", job.Env["JOB_KEY"])

	step := job.Steps[0]
	assert.Equal(t, "ver", step.ID)
	assert.Equal(t, "bash", step.Shell)
	assert.Equal(t, "./app", step.WorkingDirectory)
	assert.Equal(t, "step-value", step.Env["STEP_KEY"])
	require.NotNil(t, step.TimeoutMinutes)
	assert.Equal(t, 5, *step.TimeoutMinutes)
	assert.True(t, step.ContinueOnError.Value)

	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_WorkflowConcurrencyString(t *testing.T) {
	input := `
name: Test
on: push
concurrency: simple-group
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, w.Concurrency)
	assert.Equal(t, "simple-group", w.Concurrency.Group)
	assert.False(t, w.Concurrency.CancelInProgress)
}

func TestParse_WorkflowPermissionsWriteAll(t *testing.T) {
	input := `
name: Test
on: push
permissions: write-all
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, w.Permissions)
	assert.True(t, w.Permissions.WriteAll)
}

func TestParse_EnvironmentString(t *testing.T) {
	input := `
name: Test
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: staging
    steps:
      - run: echo deploying
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, w.Jobs["deploy"].Environment)
	assert.Equal(t, "staging", w.Jobs["deploy"].Environment.Name)
	assert.Empty(t, w.Jobs["deploy"].Environment.URL)
}

func TestParse_MatrixWithIncludeExclude(t *testing.T) {
	input := `
name: Matrix
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest]
        node: [16, 18]
        include:
          - os: ubuntu-latest
            node: 20
            experimental: true
        exclude:
          - os: macos-latest
            node: 16
    steps:
      - run: echo ${{ matrix.os }} ${{ matrix.node }}
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	m := w.Jobs["test"].Strategy.Matrix
	require.NotNil(t, m)
	assert.Len(t, m.Dimensions, 2)
	assert.Len(t, m.Include, 1)
	assert.Len(t, m.Exclude, 1)
	assert.Equal(t, true, m.Include[0]["experimental"])
}

func TestParse_StringOrSliceInNeeds(t *testing.T) {
	input := `
name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo build
  test:
    runs-on: ubuntu-latest
    needs: build
    steps:
      - run: echo test
  deploy:
    runs-on: ubuntu-latest
    needs: [build, test]
    steps:
      - run: echo deploy
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	assert.Equal(t, StringOrSlice{"build"}, w.Jobs["test"].Needs)
	assert.Equal(t, StringOrSlice{"build", "test"}, w.Jobs["deploy"].Needs)

	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestParse_ContainerString(t *testing.T) {
	input := `
name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    container: node:18
    steps:
      - run: echo hi
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, w.Jobs["build"].Container)
	assert.Equal(t, "node:18", w.Jobs["build"].Container.Image)
}

func TestParse_WorkflowCallWithAllFields(t *testing.T) {
	input := `
name: Called Workflow
on:
  workflow_call:
    inputs:
      version:
        description: 'Version to deploy'
        required: true
        type: string
    secrets:
      api-key:
        description: 'API key'
        required: true
    outputs:
      result:
        description: 'Build result'
        value: ${{ jobs.build.outputs.result }}
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo building
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	inputs := w.On.WorkflowCallInputs()
	require.NotNil(t, inputs)
	assert.Contains(t, inputs, "version")
	assert.True(t, inputs["version"].Required)

	outputs := w.On.WorkflowCallOutputs()
	require.NotNil(t, outputs)
	assert.Contains(t, outputs, "result")
}

func TestParse_ScheduleMultiple(t *testing.T) {
	input := `
schedule:
  - cron: '0 2 * * 1'
  - cron: '0 8 * * 5'
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	// Only first cron is stored
	assert.Equal(t, "0 2 * * 1", tr.Events["schedule"].Cron)
}

// --- Container edge cases ---

func TestContainer_MappingWithEntrypointArgs(t *testing.T) {
	input := `
image: custom:latest
entrypoint: /bin/sh
args: -c echo hello
`
	var c Container
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "custom:latest", c.Image)
	assert.Equal(t, "/bin/sh", c.Entrypoint)
	assert.Equal(t, "-c echo hello", c.Args)
}

// --- StringOrSlice edge cases ---

func TestStringOrSlice_InvalidSequenceContent(t *testing.T) {
	// Sequence containing non-string elements
	var s StringOrSlice
	err := yaml.Unmarshal([]byte(`[1, 2, 3]`), &s)
	// YAML will coerce numbers to strings
	require.NoError(t, err)
	assert.Equal(t, StringOrSlice{"1", "2", "3"}, s)
}

// --- RunsOn mapping error ---

func TestRunsOn_InvalidMappingField(t *testing.T) {
	// Mapping with invalid labels type
	input := `
group: my-group
labels: "not-a-list"
`
	var r RunsOn
	err := yaml.Unmarshal([]byte(input), &r)
	// "labels" as a string should fail to decode to []string
	assert.Error(t, err)
}

// --- Additional Validate test ---

func TestValidate_AllStepFieldsCombinations(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"test": {
				Steps: []Step{
					{ID: "a", Run: "echo a"},
					{ID: "b", Uses: "actions/checkout@v4"},
					{Run: "echo no id"},
				},
			},
		},
	}
	errs := Validate(w)
	assert.Empty(t, errs)
}

func TestValidate_NeedsRefsExistingAndNonExisting(t *testing.T) {
	w := &Workflow{
		Jobs: map[string]*Job{
			"build": {Steps: []Step{{Run: "echo build"}}},
			"test": {
				Needs: StringOrSlice{"build", "nonexistent"},
				Steps: []Step{{Run: "echo test"}},
			},
		},
	}
	errs := Validate(w)
	assert.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "nonexistent")
}

func TestParse_WorkflowDefaults(t *testing.T) {
	input := `
name: Defaults Test
on: push
defaults:
  run:
    shell: pwsh
    working-directory: ./app
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hello
`
	w, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, w.Defaults)
	require.NotNil(t, w.Defaults.Run)
	assert.Equal(t, "pwsh", w.Defaults.Run.Shell)
	assert.Equal(t, "./app", w.Defaults.Run.WorkingDirectory)
}

// ============================================================
// Tests to cover remaining uncovered regions for >95% coverage
// ============================================================

// --- parse.go:30-32: ParseFile where file opens OK but Parse fails ---

func TestParseFile_ValidFileInvalidWorkflowYAML(t *testing.T) {
	// Create a temp file with YAML that opens successfully but fails during Parse.
	// Using a truncated/malformed YAML that the decoder will choke on.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")
	err := os.WriteFile(path, []byte("name: Bad\non: push\njobs:\n  build:\n    runs-on: [\n"), 0644)
	require.NoError(t, err)

	_, err = ParseFile(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error parsing")
}

// --- concurrency.go:25-27: Concurrency mapping Decode error ---

func TestConcurrency_MappingDecodeError(t *testing.T) {
	// Build a YAML node that is a MappingNode but has content that
	// cannot decode into the concurrencyAlias struct.
	// cancel-in-progress expects a bool; give it a sequence to force decode error.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "group", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "mygroup", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "cancel-in-progress", Tag: "!!str"},
			{Kind: yaml.SequenceNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "not-a-bool", Tag: "!!str"},
			}},
		},
	}
	var c Concurrency
	err := c.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Concurrency: failed to decode mapping")
}

// --- concurrency.go:31: Concurrency default (non-string, non-mapping) ---

func TestConcurrency_UnexpectedNodeType(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
	}
	var c Concurrency
	err := c.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string or mapping")
}

// --- container.go:37-39: Container mapping Decode error ---

func TestContainer_MappingDecodeError(t *testing.T) {
	// MappingNode where "ports" expects []string but gets a mapping.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "image", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "node:18", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "ports", Tag: "!!str"},
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "not", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: "a-list", Tag: "!!str"},
			}},
		},
	}
	var c Container
	err := c.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Container: failed to decode mapping")
}

// --- container.go:43: Container default (non-string, non-mapping) ---

func TestContainer_UnexpectedNodeType(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
	}
	var c Container
	err := c.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string or mapping")
}

// --- permissions.go:40-42: Permissions mapping Decode error ---

func TestPermissions_MappingDecodeError(t *testing.T) {
	// Map values should be PermissionLevel (string), give a sequence instead.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "contents", Tag: "!!str"},
			{Kind: yaml.SequenceNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "read", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: "write", Tag: "!!str"},
			}},
		},
	}
	var p Permissions
	err := p.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Permissions: failed to decode mapping")
}

// --- permissions.go:46: Permissions default (non-string, non-mapping) ---

func TestPermissions_UnexpectedNodeType(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
	}
	var p Permissions
	err := p.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string or mapping")
}

// --- strategy.go:112-114: decodeScalarValue Decode error ---

func TestDecodeScalarValue_DecodeError(t *testing.T) {
	// A scalar node with an invalid tag that causes Decode to fail.
	// Use a tag that yaml.v3 doesn't know how to decode.
	node := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: "not-a-valid-timestamp",
		Tag:   "!!timestamp",
	}
	// decodeScalarValue should fall back to returning node.Value
	result := decodeScalarValue(node)
	assert.Equal(t, "not-a-valid-timestamp", result)
}

// --- trigger.go:28-30: Triggers sequence Decode error ---

func TestTriggers_SequenceDecodeError(t *testing.T) {
	// A SequenceNode with items that cannot decode into []string
	// (e.g., a sequence containing a mapping).
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
		Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "key", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: "val", Tag: "!!str"},
			}},
		},
	}
	var tr Triggers
	err := tr.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Triggers: failed to decode sequence")
}

// --- trigger.go:56-58: Triggers event config Decode error ---

func TestTriggers_EventConfigDecodeError(t *testing.T) {
	// A MappingNode event whose value is a MappingNode but with fields
	// that cannot be decoded into EventConfig (e.g. branches as a mapping).
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "push", Tag: "!!str"},
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "branches", Tag: "!!str"},
				{Kind: yaml.MappingNode, Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Value: "not", Tag: "!!str"},
					{Kind: yaml.ScalarNode, Value: "a-list", Tag: "!!str"},
				}},
			}},
		},
	}
	var tr Triggers
	err := tr.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Triggers: failed to decode event config")
}

// --- trigger.go:60-63: Triggers event with non-null, non-mapping value ---

func TestTriggers_EventNonNullNonMappingValue(t *testing.T) {
	// Event value that is a ScalarNode but not !!null -- falls into else branch.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "push", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "some-value", Tag: "!!str"},
		},
	}
	var tr Triggers
	err := tr.UnmarshalYAML(node)
	require.NoError(t, err)
	// Falls into else branch: sets to nil
	assert.Nil(t, tr.Events["push"])
}

// --- trigger.go:67-68: Triggers default (not scalar, sequence, or mapping) ---

func TestTriggers_UnexpectedNodeType(t *testing.T) {
	// Use an AliasNode (kind 0 or something unexpected).
	// yaml.Node Kind values: 0=none, 1=DocumentNode, 2=SequenceNode,
	// 4=MappingNode, 8=ScalarNode, 16=AliasNode
	node := &yaml.Node{
		Kind: yaml.AliasNode,
	}
	var tr Triggers
	err := tr.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string, sequence, or mapping")
}

// --- trigger.go:82-84: parseSchedule Decode error ---

func TestTriggers_ScheduleDecodeError(t *testing.T) {
	// Schedule sequence with items that cannot decode into struct{Cron string}.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "schedule", Tag: "!!str"},
			{Kind: yaml.SequenceNode, Content: []*yaml.Node{
				// Each item should be a mapping with "cron" key.
				// Give a sequence of scalars to fail decode.
				{Kind: yaml.ScalarNode, Value: "not-a-mapping", Tag: "!!str"},
			}},
		},
	}
	var tr Triggers
	err := tr.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "schedule")
}

// --- types.go:20-22: StringOrSlice sequence Decode error ---

func TestStringOrSlice_SequenceDecodeError(t *testing.T) {
	// A SequenceNode with items that can't decode to []string
	// (e.g., sequence containing a mapping).
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
		Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "key", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: "val", Tag: "!!str"},
			}},
		},
	}
	var s StringOrSlice
	err := s.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StringOrSlice: failed to decode sequence")
}

// --- types.go:26: StringOrSlice default (unexpected node type) ---

func TestStringOrSlice_UnexpectedNodeType(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "key", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "val", Tag: "!!str"},
		},
	}
	var s StringOrSlice
	err := s.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string or sequence")
}

// --- types.go:44-46: RunsOn sequence Decode error ---

func TestRunsOn_SequenceDecodeError(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
		Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "key", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: "val", Tag: "!!str"},
			}},
		},
	}
	var r RunsOn
	err := r.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "RunsOn: failed to decode sequence")
}

// --- types.go:60-61: RunsOn mapping Decode error ---

func TestRunsOn_MappingDecodeError(t *testing.T) {
	// "labels" expects []string, give it a mapping to trigger decode error.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "group", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "my-group", Tag: "!!str"},
			{Kind: yaml.ScalarNode, Value: "labels", Tag: "!!str"},
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "not", Tag: "!!str"},
				{Kind: yaml.ScalarNode, Value: "a-list", Tag: "!!str"},
			}},
		},
	}
	var r RunsOn
	err := r.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "RunsOn: failed to decode mapping")
}

// --- types.go:61: RunsOn default (unexpected node type) ---

func TestRunsOn_UnexpectedNodeType(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.AliasNode,
	}
	var r RunsOn
	err := r.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string, sequence, or mapping")
}

// --- types.go:109-111: Environment mapping Decode error ---

func TestEnvironment_MappingDecodeError(t *testing.T) {
	// "name" expects string, give a sequence to trigger decode error.
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"},
			{Kind: yaml.SequenceNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "not-a-string", Tag: "!!str"},
			}},
		},
	}
	var e Environment
	err := e.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Environment: failed to decode mapping")
}

// --- types.go:116: Environment default (unexpected node type) ---

func TestEnvironment_UnexpectedNodeType(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.SequenceNode,
	}
	var e Environment
	err := e.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected string or mapping")
}

// --- Permissions: unknown permission string ---

func TestPermissions_UnknownPermissionString(t *testing.T) {
	var p Permissions
	node := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: "invalid-perm",
		Tag:   "!!str",
	}
	err := p.UnmarshalYAML(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown permission level")
}
