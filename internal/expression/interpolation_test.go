package expression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInterpolationNoExpressions(t *testing.T) {
	parts, err := ParseInterpolation("hello world")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.False(t, parts[0].IsExpr)
	assert.Equal(t, "hello world", parts[0].Literal)
}

func TestParseInterpolationSingleExpression(t *testing.T) {
	parts, err := ParseInterpolation("${{ github.ref }}")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "github.ref", parts[0].Expression)
}

func TestParseInterpolationExpressionWithSurrounding(t *testing.T) {
	parts, err := ParseInterpolation("Branch: ${{ github.ref }} is ready")
	require.NoError(t, err)
	require.Len(t, parts, 3)
	assert.False(t, parts[0].IsExpr)
	assert.Equal(t, "Branch: ", parts[0].Literal)
	assert.True(t, parts[1].IsExpr)
	assert.Equal(t, "github.ref", parts[1].Expression)
	assert.False(t, parts[2].IsExpr)
	assert.Equal(t, " is ready", parts[2].Literal)
}

func TestParseInterpolationMultipleExpressions(t *testing.T) {
	parts, err := ParseInterpolation("${{ a }}-${{ b }}")
	require.NoError(t, err)
	require.Len(t, parts, 3)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "a", parts[0].Expression)
	assert.False(t, parts[1].IsExpr)
	assert.Equal(t, "-", parts[1].Literal)
	assert.True(t, parts[2].IsExpr)
	assert.Equal(t, "b", parts[2].Expression)
}

func TestParseInterpolationExpressionAtStart(t *testing.T) {
	parts, err := ParseInterpolation("${{ x }} rest")
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "x", parts[0].Expression)
	assert.False(t, parts[1].IsExpr)
	assert.Equal(t, " rest", parts[1].Literal)
}

func TestParseInterpolationExpressionAtEnd(t *testing.T) {
	parts, err := ParseInterpolation("start ${{ x }}")
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.False(t, parts[0].IsExpr)
	assert.Equal(t, "start ", parts[0].Literal)
	assert.True(t, parts[1].IsExpr)
	assert.Equal(t, "x", parts[1].Expression)
}

func TestParseInterpolationAdjacentExpressions(t *testing.T) {
	parts, err := ParseInterpolation("${{ a }}${{ b }}")
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "a", parts[0].Expression)
	assert.True(t, parts[1].IsExpr)
	assert.Equal(t, "b", parts[1].Expression)
}

func TestParseInterpolationComplexExpression(t *testing.T) {
	parts, err := ParseInterpolation("${{ github.event_name == 'push' && contains(github.ref, 'main') }}")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "github.event_name == 'push' && contains(github.ref, 'main')", parts[0].Expression)
}

func TestParseInterpolationStringWithBraces(t *testing.T) {
	// String containing '}}' inside the expression should be handled
	parts, err := ParseInterpolation("${{ format('{0}', 'hello') }}")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.True(t, parts[0].IsExpr)
}

func TestParseInterpolationUnclosed(t *testing.T) {
	_, err := ParseInterpolation("${{ github.ref")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed expression")
}

func TestParseInterpolationEmptyString(t *testing.T) {
	parts, err := ParseInterpolation("")
	require.NoError(t, err)
	assert.Len(t, parts, 0)
}

func TestParseInterpolationDollarSignNotExpression(t *testing.T) {
	// $ not followed by {{ should be literal
	parts, err := ParseInterpolation("$100")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.False(t, parts[0].IsExpr)
	assert.Equal(t, "$100", parts[0].Literal)
}

func TestParseInterpolationExpressionWithWhitespace(t *testing.T) {
	parts, err := ParseInterpolation("${{   github.ref   }}")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "github.ref", parts[0].Expression) // trimmed
}

func TestEvalInterpolationNoExpressions(t *testing.T) {
	result, err := EvalInterpolation("hello world", MapContext{})
	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestEvalInterpolationSingleExpression(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"ref": String("refs/heads/main"),
		}),
	}
	result, err := EvalInterpolation("${{ github.ref }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/main", result)
}

func TestEvalInterpolationMixed(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"actor": String("octocat"),
			"ref":   String("main"),
		}),
	}
	result, err := EvalInterpolation("Hello ${{ github.actor }}, branch is ${{ github.ref }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "Hello octocat, branch is main", result)
}

func TestEvalInterpolationBoolCoercion(t *testing.T) {
	ctx := MapContext{}
	result, err := EvalInterpolation("${{ true }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "true", result)
}

func TestEvalInterpolationNumberCoercion(t *testing.T) {
	ctx := MapContext{}
	result, err := EvalInterpolation("count: ${{ 42 }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "count: 42", result)
}

func TestEvalInterpolationNullCoercion(t *testing.T) {
	ctx := MapContext{}
	result, err := EvalInterpolation("value: ${{ null }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "value: ", result)
}

func TestEvalInterpolationWithExpression(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"ref": String("refs/heads/main"),
		}),
	}
	result, err := EvalInterpolation("is_main: ${{ github.ref == 'refs/heads/main' }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "is_main: true", result)
}

func TestEvalInterpolationWithFunction(t *testing.T) {
	ctx := MapContext{
		"matrix": Object(map[string]Value{
			"os": String("ubuntu-latest"),
		}),
	}
	result, err := EvalInterpolation("${{ format('os={0}', matrix.os) }}", ctx)
	require.NoError(t, err)
	assert.Equal(t, "os=ubuntu-latest", result)
}

func TestEvalInterpolationParseError(t *testing.T) {
	_, err := EvalInterpolation("${{ (((( }}", MapContext{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error parsing expression")
}

func TestEvalInterpolationUnclosedError(t *testing.T) {
	_, err := EvalInterpolation("${{ github.ref", MapContext{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed expression")
}

func TestParseInterpolationStringLiteralWithBraces(t *testing.T) {
	// Expression containing a string with }} inside quotes should not end the expression
	input := "${{ contains('a}}b', 'x') }}"
	parts, err := ParseInterpolation(input)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.True(t, parts[0].IsExpr)
	assert.Equal(t, "contains('a}}b', 'x')", parts[0].Expression)
}
