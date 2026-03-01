package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPermissions_ReadAll(t *testing.T) {
	var p Permissions
	err := yaml.Unmarshal([]byte(`read-all`), &p)
	require.NoError(t, err)
	assert.True(t, p.ReadAll)
	assert.False(t, p.WriteAll)
	assert.Nil(t, p.Scopes)
}

func TestPermissions_WriteAll(t *testing.T) {
	var p Permissions
	err := yaml.Unmarshal([]byte(`write-all`), &p)
	require.NoError(t, err)
	assert.False(t, p.ReadAll)
	assert.True(t, p.WriteAll)
	assert.Nil(t, p.Scopes)
}

func TestPermissions_Scopes(t *testing.T) {
	input := `
contents: read
issues: write
pull-requests: read
`
	var p Permissions
	err := yaml.Unmarshal([]byte(input), &p)
	require.NoError(t, err)
	assert.False(t, p.ReadAll)
	assert.False(t, p.WriteAll)
	require.NotNil(t, p.Scopes)
	assert.Equal(t, PermissionRead, p.Scopes["contents"])
	assert.Equal(t, PermissionWrite, p.Scopes["issues"])
	assert.Equal(t, PermissionRead, p.Scopes["pull-requests"])
}

func TestPermissions_ScopesWithNone(t *testing.T) {
	input := `
contents: read
actions: none
`
	var p Permissions
	err := yaml.Unmarshal([]byte(input), &p)
	require.NoError(t, err)
	assert.Equal(t, PermissionRead, p.Scopes["contents"])
	assert.Equal(t, PermissionNone, p.Scopes["actions"])
}

func TestPermissions_InvalidString(t *testing.T) {
	var p Permissions
	err := yaml.Unmarshal([]byte(`invalid-value`), &p)
	assert.Error(t, err)
}

func TestPermissions_SingleScope(t *testing.T) {
	input := `
contents: write
`
	var p Permissions
	err := yaml.Unmarshal([]byte(input), &p)
	require.NoError(t, err)
	assert.Equal(t, PermissionWrite, p.Scopes["contents"])
}

func TestPermissions_InvalidType(t *testing.T) {
	var p Permissions
	err := yaml.Unmarshal([]byte(`[read, write]`), &p)
	assert.Error(t, err)
}

func TestPermissions_InStruct(t *testing.T) {
	input := `
permissions:
  contents: read
  issues: write
`
	type wrapper struct {
		Permissions *Permissions `yaml:"permissions"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	require.NoError(t, err)
	require.NotNil(t, w.Permissions)
	assert.Equal(t, PermissionRead, w.Permissions.Scopes["contents"])
	assert.Equal(t, PermissionWrite, w.Permissions.Scopes["issues"])
}

func TestPermissions_InStructReadAll(t *testing.T) {
	input := `
permissions: read-all
`
	type wrapper struct {
		Permissions *Permissions `yaml:"permissions"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	require.NoError(t, err)
	require.NotNil(t, w.Permissions)
	assert.True(t, w.Permissions.ReadAll)
}
