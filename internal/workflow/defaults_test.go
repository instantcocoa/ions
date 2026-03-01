package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDefaults_Full(t *testing.T) {
	input := `
run:
  shell: bash
  working-directory: ./src
`
	var d Defaults
	err := yaml.Unmarshal([]byte(input), &d)
	require.NoError(t, err)
	require.NotNil(t, d.Run)
	assert.Equal(t, "bash", d.Run.Shell)
	assert.Equal(t, "./src", d.Run.WorkingDirectory)
}

func TestDefaults_ShellOnly(t *testing.T) {
	input := `
run:
  shell: pwsh
`
	var d Defaults
	err := yaml.Unmarshal([]byte(input), &d)
	require.NoError(t, err)
	require.NotNil(t, d.Run)
	assert.Equal(t, "pwsh", d.Run.Shell)
	assert.Empty(t, d.Run.WorkingDirectory)
}

func TestDefaults_WorkingDirectoryOnly(t *testing.T) {
	input := `
run:
  working-directory: ./app
`
	var d Defaults
	err := yaml.Unmarshal([]byte(input), &d)
	require.NoError(t, err)
	require.NotNil(t, d.Run)
	assert.Empty(t, d.Run.Shell)
	assert.Equal(t, "./app", d.Run.WorkingDirectory)
}

func TestDefaults_Empty(t *testing.T) {
	input := `{}`
	var d Defaults
	err := yaml.Unmarshal([]byte(input), &d)
	require.NoError(t, err)
	assert.Nil(t, d.Run)
}

func TestDefaults_InStruct(t *testing.T) {
	input := `
defaults:
  run:
    shell: bash
    working-directory: ./src
`
	type wrapper struct {
		Defaults *Defaults `yaml:"defaults"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	require.NoError(t, err)
	require.NotNil(t, w.Defaults)
	require.NotNil(t, w.Defaults.Run)
	assert.Equal(t, "bash", w.Defaults.Run.Shell)
	assert.Equal(t, "./src", w.Defaults.Run.WorkingDirectory)
}

func TestDefaults_NilWhenOmitted(t *testing.T) {
	input := `
name: test
`
	type wrapper struct {
		Name     string    `yaml:"name"`
		Defaults *Defaults `yaml:"defaults,omitempty"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	require.NoError(t, err)
	assert.Nil(t, w.Defaults)
}
