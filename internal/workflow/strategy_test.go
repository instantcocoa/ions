package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestMatrix_Expression(t *testing.T) {
	var m Matrix
	err := yaml.Unmarshal([]byte(`${{ fromJSON(needs.setup.outputs.matrix) }}`), &m)
	require.NoError(t, err)
	assert.Equal(t, "${{ fromJSON(needs.setup.outputs.matrix) }}", m.Expression)
	assert.Nil(t, m.Dimensions)
	assert.Nil(t, m.Include)
	assert.Nil(t, m.Exclude)
}

func TestMatrix_SimpleDimensions(t *testing.T) {
	input := `
os: [ubuntu-latest, macos-latest]
node: [16, 18, 20]
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	assert.Empty(t, m.Expression)

	require.Contains(t, m.Dimensions, "os")
	assert.Equal(t, []interface{}{"ubuntu-latest", "macos-latest"}, m.Dimensions["os"])

	require.Contains(t, m.Dimensions, "node")
	// YAML integers decode as int
	assert.Equal(t, []interface{}{16, 18, 20}, m.Dimensions["node"])
}

func TestMatrix_WithIncludeExclude(t *testing.T) {
	input := `
os: [ubuntu-latest, macos-latest, windows-latest]
node: [16, 18, 20]
include:
  - os: ubuntu-latest
    node: 20
    coverage: true
exclude:
  - os: windows-latest
    node: 16
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)

	assert.Len(t, m.Dimensions, 2)
	assert.Contains(t, m.Dimensions, "os")
	assert.Contains(t, m.Dimensions, "node")

	require.Len(t, m.Include, 1)
	assert.Equal(t, "ubuntu-latest", m.Include[0]["os"])
	assert.Equal(t, 20, m.Include[0]["node"])
	assert.Equal(t, true, m.Include[0]["coverage"])

	require.Len(t, m.Exclude, 1)
	assert.Equal(t, "windows-latest", m.Exclude[0]["os"])
	assert.Equal(t, 16, m.Exclude[0]["node"])
}

func TestMatrix_SingleDimension(t *testing.T) {
	input := `
python: ["3.9", "3.10", "3.11"]
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)

	require.Contains(t, m.Dimensions, "python")
	// Quoted strings remain strings
	assert.Equal(t, []interface{}{"3.9", "3.10", "3.11"}, m.Dimensions["python"])
}

func TestMatrix_BooleanValues(t *testing.T) {
	input := `
experimental: [true, false]
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	require.Contains(t, m.Dimensions, "experimental")
	assert.Equal(t, []interface{}{true, false}, m.Dimensions["experimental"])
}

func TestMatrix_IncludeOnly(t *testing.T) {
	input := `
include:
  - os: ubuntu-latest
    version: "1.0"
  - os: macos-latest
    version: "2.0"
`
	var m Matrix
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	assert.Empty(t, m.Dimensions)
	require.Len(t, m.Include, 2)
	assert.Equal(t, "ubuntu-latest", m.Include[0]["os"])
	assert.Equal(t, "1.0", m.Include[0]["version"])
}

func TestStrategy_Full(t *testing.T) {
	input := `
fail-fast: false
max-parallel: 3
matrix:
  os: [ubuntu-latest, macos-latest]
  node: [16, 18]
`
	var s Strategy
	err := yaml.Unmarshal([]byte(input), &s)
	require.NoError(t, err)

	require.NotNil(t, s.FailFast)
	assert.False(t, *s.FailFast)

	require.NotNil(t, s.MaxParallel)
	assert.Equal(t, 3, *s.MaxParallel)

	require.NotNil(t, s.Matrix)
	assert.Len(t, s.Matrix.Dimensions, 2)
}

func TestStrategy_DefaultFailFast(t *testing.T) {
	input := `
matrix:
  os: [ubuntu-latest]
`
	var s Strategy
	err := yaml.Unmarshal([]byte(input), &s)
	require.NoError(t, err)
	assert.Nil(t, s.FailFast) // not set means default
	assert.Nil(t, s.MaxParallel)
	require.NotNil(t, s.Matrix)
}

func TestStrategy_MatrixExpression(t *testing.T) {
	input := `
matrix: ${{ fromJSON(needs.setup.outputs.matrix) }}
`
	var s Strategy
	err := yaml.Unmarshal([]byte(input), &s)
	require.NoError(t, err)
	require.NotNil(t, s.Matrix)
	assert.Equal(t, "${{ fromJSON(needs.setup.outputs.matrix) }}", s.Matrix.Expression)
}

func TestMatrix_InvalidType(t *testing.T) {
	var m Matrix
	err := yaml.Unmarshal([]byte(`[1, 2, 3]`), &m)
	assert.Error(t, err)
}
