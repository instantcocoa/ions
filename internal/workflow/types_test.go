package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestStringOrSlice_String(t *testing.T) {
	var s StringOrSlice
	err := yaml.Unmarshal([]byte(`"hello"`), &s)
	require.NoError(t, err)
	assert.Equal(t, StringOrSlice{"hello"}, s)
}

func TestStringOrSlice_Slice(t *testing.T) {
	var s StringOrSlice
	err := yaml.Unmarshal([]byte(`["foo", "bar", "baz"]`), &s)
	require.NoError(t, err)
	assert.Equal(t, StringOrSlice{"foo", "bar", "baz"}, s)
}

func TestStringOrSlice_SingleElementSlice(t *testing.T) {
	var s StringOrSlice
	err := yaml.Unmarshal([]byte(`["only"]`), &s)
	require.NoError(t, err)
	assert.Equal(t, StringOrSlice{"only"}, s)
}

func TestStringOrSlice_EmptySlice(t *testing.T) {
	var s StringOrSlice
	err := yaml.Unmarshal([]byte(`[]`), &s)
	require.NoError(t, err)
	assert.Equal(t, StringOrSlice{}, s)
}

func TestStringOrSlice_InvalidType(t *testing.T) {
	var s StringOrSlice
	err := yaml.Unmarshal([]byte(`{foo: bar}`), &s)
	assert.Error(t, err)
}

func TestRunsOn_String(t *testing.T) {
	var r RunsOn
	err := yaml.Unmarshal([]byte(`ubuntu-latest`), &r)
	require.NoError(t, err)
	assert.Equal(t, []string{"ubuntu-latest"}, r.Labels)
	assert.Empty(t, r.Group)
}

func TestRunsOn_Sequence(t *testing.T) {
	var r RunsOn
	err := yaml.Unmarshal([]byte(`[self-hosted, linux, x64]`), &r)
	require.NoError(t, err)
	assert.Equal(t, []string{"self-hosted", "linux", "x64"}, r.Labels)
	assert.Empty(t, r.Group)
}

func TestRunsOn_Mapping(t *testing.T) {
	input := `
group: my-group
labels: [self-hosted, linux]
`
	var r RunsOn
	err := yaml.Unmarshal([]byte(input), &r)
	require.NoError(t, err)
	assert.Equal(t, "my-group", r.Group)
	assert.Equal(t, []string{"self-hosted", "linux"}, r.Labels)
}

func TestRunsOn_MappingGroupOnly(t *testing.T) {
	input := `
group: my-group
`
	var r RunsOn
	err := yaml.Unmarshal([]byte(input), &r)
	require.NoError(t, err)
	assert.Equal(t, "my-group", r.Group)
	assert.Nil(t, r.Labels)
}

func TestRunsOn_Expression(t *testing.T) {
	var r RunsOn
	err := yaml.Unmarshal([]byte(`${{ matrix.os }}`), &r)
	require.NoError(t, err)
	assert.Equal(t, []string{"${{ matrix.os }}"}, r.Labels)
}

func TestExprBool_True(t *testing.T) {
	var e ExprBool
	err := yaml.Unmarshal([]byte(`true`), &e)
	require.NoError(t, err)
	assert.True(t, e.Value)
	assert.False(t, e.IsExpr)
	assert.Empty(t, e.Expression)
}

func TestExprBool_False(t *testing.T) {
	var e ExprBool
	err := yaml.Unmarshal([]byte(`false`), &e)
	require.NoError(t, err)
	assert.False(t, e.Value)
	assert.False(t, e.IsExpr)
}

func TestExprBool_Expression(t *testing.T) {
	var e ExprBool
	err := yaml.Unmarshal([]byte(`${{ inputs.debug }}`), &e)
	require.NoError(t, err)
	assert.True(t, e.IsExpr)
	assert.Equal(t, "${{ inputs.debug }}", e.Expression)
}

func TestExprBool_ArbitraryString(t *testing.T) {
	var e ExprBool
	err := yaml.Unmarshal([]byte(`"some-string"`), &e)
	require.NoError(t, err)
	assert.True(t, e.IsExpr)
	assert.Equal(t, "some-string", e.Expression)
}

func TestEnvironment_String(t *testing.T) {
	var e Environment
	err := yaml.Unmarshal([]byte(`production`), &e)
	require.NoError(t, err)
	assert.Equal(t, "production", e.Name)
	assert.Empty(t, e.URL)
}

func TestEnvironment_Mapping(t *testing.T) {
	input := `
name: production
url: https://example.com
`
	var e Environment
	err := yaml.Unmarshal([]byte(input), &e)
	require.NoError(t, err)
	assert.Equal(t, "production", e.Name)
	assert.Equal(t, "https://example.com", e.URL)
}

func TestEnvironment_MappingNameOnly(t *testing.T) {
	input := `
name: staging
`
	var e Environment
	err := yaml.Unmarshal([]byte(input), &e)
	require.NoError(t, err)
	assert.Equal(t, "staging", e.Name)
	assert.Empty(t, e.URL)
}

func TestEnvironment_InvalidType(t *testing.T) {
	var e Environment
	err := yaml.Unmarshal([]byte(`[a, b]`), &e)
	assert.Error(t, err)
}

func TestStringOrSlice_InStruct(t *testing.T) {
	type wrapper struct {
		Needs StringOrSlice `yaml:"needs"`
	}

	tests := []struct {
		name     string
		input    string
		expected StringOrSlice
	}{
		{
			name:     "single string",
			input:    "needs: build",
			expected: StringOrSlice{"build"},
		},
		{
			name:     "list",
			input:    "needs: [build, test]",
			expected: StringOrSlice{"build", "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrapper
			err := yaml.Unmarshal([]byte(tt.input), &w)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, w.Needs)
		})
	}
}
