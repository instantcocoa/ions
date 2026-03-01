package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConcurrency_String(t *testing.T) {
	var c Concurrency
	err := yaml.Unmarshal([]byte(`my-group`), &c)
	require.NoError(t, err)
	assert.Equal(t, "my-group", c.Group)
	assert.False(t, c.CancelInProgress)
}

func TestConcurrency_Mapping(t *testing.T) {
	input := `
group: ci-${{ github.ref }}
cancel-in-progress: true
`
	var c Concurrency
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "ci-${{ github.ref }}", c.Group)
	assert.True(t, c.CancelInProgress)
}

func TestConcurrency_MappingNoCancelInProgress(t *testing.T) {
	input := `
group: deploy
`
	var c Concurrency
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "deploy", c.Group)
	assert.False(t, c.CancelInProgress)
}

func TestConcurrency_MappingCancelFalse(t *testing.T) {
	input := `
group: deploy
cancel-in-progress: false
`
	var c Concurrency
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "deploy", c.Group)
	assert.False(t, c.CancelInProgress)
}

func TestConcurrency_ExpressionGroup(t *testing.T) {
	var c Concurrency
	err := yaml.Unmarshal([]byte(`${{ github.workflow }}-${{ github.ref }}`), &c)
	require.NoError(t, err)
	assert.Equal(t, "${{ github.workflow }}-${{ github.ref }}", c.Group)
}

func TestConcurrency_InvalidType(t *testing.T) {
	var c Concurrency
	err := yaml.Unmarshal([]byte(`[a, b]`), &c)
	assert.Error(t, err)
}

func TestConcurrency_InStruct(t *testing.T) {
	input := `
concurrency:
  group: test-group
  cancel-in-progress: true
`
	type wrapper struct {
		Concurrency *Concurrency `yaml:"concurrency"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	require.NoError(t, err)
	require.NotNil(t, w.Concurrency)
	assert.Equal(t, "test-group", w.Concurrency.Group)
	assert.True(t, w.Concurrency.CancelInProgress)
}

func TestConcurrency_InStructString(t *testing.T) {
	input := `
concurrency: simple-group
`
	type wrapper struct {
		Concurrency *Concurrency `yaml:"concurrency"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	require.NoError(t, err)
	require.NotNil(t, w.Concurrency)
	assert.Equal(t, "simple-group", w.Concurrency.Group)
}
