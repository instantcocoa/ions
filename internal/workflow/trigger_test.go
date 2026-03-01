package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTriggers_String(t *testing.T) {
	var tr Triggers
	err := yaml.Unmarshal([]byte(`push`), &tr)
	require.NoError(t, err)
	assert.Contains(t, tr.Events, "push")
	assert.Nil(t, tr.Events["push"])
}

func TestTriggers_Sequence(t *testing.T) {
	var tr Triggers
	err := yaml.Unmarshal([]byte(`[push, pull_request]`), &tr)
	require.NoError(t, err)
	assert.Contains(t, tr.Events, "push")
	assert.Contains(t, tr.Events, "pull_request")
	assert.Nil(t, tr.Events["push"])
	assert.Nil(t, tr.Events["pull_request"])
}

func TestTriggers_MappingWithBranches(t *testing.T) {
	input := `
push:
  branches: [main, develop]
  tags:
    - 'v*'
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	require.Contains(t, tr.Events, "push")
	push := tr.Events["push"]
	require.NotNil(t, push)
	assert.Equal(t, []string{"main", "develop"}, push.Branches)
	assert.Equal(t, []string{"v*"}, push.Tags)
}

func TestTriggers_MappingNullEvent(t *testing.T) {
	input := `
push:
  branches: [main]
pull_request:
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	assert.Contains(t, tr.Events, "push")
	assert.Contains(t, tr.Events, "pull_request")
	assert.NotNil(t, tr.Events["push"])
	assert.Nil(t, tr.Events["pull_request"])
}

func TestTriggers_WorkflowDispatch(t *testing.T) {
	input := `
workflow_dispatch:
  inputs:
    environment:
      description: 'Target environment'
      required: true
      default: 'staging'
      type: choice
      options:
        - staging
        - production
    debug:
      description: 'Enable debug mode'
      type: boolean
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	require.Contains(t, tr.Events, "workflow_dispatch")
	wd := tr.Events["workflow_dispatch"]
	require.NotNil(t, wd)
	require.Contains(t, wd.Inputs, "environment")
	assert.Equal(t, "Target environment", wd.Inputs["environment"].Description)
	assert.True(t, wd.Inputs["environment"].Required)
	assert.Equal(t, "staging", wd.Inputs["environment"].Default)
	assert.Equal(t, "choice", wd.Inputs["environment"].Type)
	assert.Equal(t, []string{"staging", "production"}, wd.Inputs["environment"].Options)

	require.Contains(t, wd.Inputs, "debug")
	assert.Equal(t, "boolean", wd.Inputs["debug"].Type)
}

func TestTriggers_Schedule(t *testing.T) {
	input := `
schedule:
  - cron: '0 2 * * 1'
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	require.Contains(t, tr.Events, "schedule")
	sched := tr.Events["schedule"]
	require.NotNil(t, sched)
	assert.Equal(t, "0 2 * * 1", sched.Cron)
}

func TestTriggers_PullRequestTypes(t *testing.T) {
	input := `
pull_request:
  types: [opened, synchronize, reopened]
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	require.Contains(t, tr.Events, "pull_request")
	pr := tr.Events["pull_request"]
	require.NotNil(t, pr)
	assert.Equal(t, []string{"opened", "synchronize", "reopened"}, pr.Types)
}

func TestTriggers_PushPathsIgnore(t *testing.T) {
	input := `
push:
  paths-ignore:
    - '**.md'
    - 'docs/**'
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	require.Contains(t, tr.Events, "push")
	push := tr.Events["push"]
	require.NotNil(t, push)
	assert.Equal(t, []string{"**.md", "docs/**"}, push.PathsIgnore)
}

func TestTriggers_BranchesIgnore(t *testing.T) {
	input := `
push:
  branches-ignore:
    - 'feature/**'
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	push := tr.Events["push"]
	require.NotNil(t, push)
	assert.Equal(t, []string{"feature/**"}, push.BranchesIgnore)
}

func TestTriggers_ComplexMultiEvent(t *testing.T) {
	input := `
push:
  branches: [main]
pull_request:
  types: [opened]
workflow_dispatch:
schedule:
  - cron: '0 0 * * *'
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	assert.Len(t, tr.Events, 4)
	assert.Contains(t, tr.Events, "push")
	assert.Contains(t, tr.Events, "pull_request")
	assert.Contains(t, tr.Events, "workflow_dispatch")
	assert.Contains(t, tr.Events, "schedule")
}

func TestTriggers_ScalarNumber(t *testing.T) {
	// YAML treats 123 as a scalar, which gets interpreted as event name "123"
	var tr Triggers
	err := yaml.Unmarshal([]byte(`123`), &tr)
	require.NoError(t, err)
	assert.Contains(t, tr.Events, "123")
}

func TestTriggers_WorkflowCallSecrets(t *testing.T) {
	input := `
workflow_call:
  secrets:
    api-key:
      description: 'The API key'
      required: true
  outputs:
    result:
      description: 'The result'
      value: ${{ jobs.build.outputs.result }}
`
	var tr Triggers
	err := yaml.Unmarshal([]byte(input), &tr)
	require.NoError(t, err)
	require.Contains(t, tr.Events, "workflow_call")
	wc := tr.Events["workflow_call"]
	require.NotNil(t, wc)
	require.Contains(t, wc.Secrets, "api-key")
	assert.Equal(t, "The API key", wc.Secrets["api-key"].Description)
	assert.True(t, wc.Secrets["api-key"].Required)
	require.Contains(t, wc.Outputs, "result")
	assert.Equal(t, "${{ jobs.build.outputs.result }}", wc.Outputs["result"].Value)
}
