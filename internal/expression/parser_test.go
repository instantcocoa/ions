package expression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLiterals(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{"null", "null", Null()},
		{"true", "true", Bool(true)},
		{"false", "false", Bool(false)},
		{"integer", "42", Number(42)},
		{"float", "3.14", Number(3.14)},
		{"hex", "0xff", Number(255)},
		{"scientific", "1e3", Number(1000)},
		{"negative", "-5", Number(-5)},
		{"string", "'hello'", String("hello")},
		{"empty string", "''", String("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			lit, ok := node.(*Literal)
			require.True(t, ok, "expected Literal, got %T", node)
			assert.True(t, tt.want.Equals(lit.Val), "expected %v, got %v", tt.want.GoString(), lit.Val.GoString())
		})
	}
}

func TestParseIdent(t *testing.T) {
	node, err := Parse("github")
	require.NoError(t, err)
	ident, ok := node.(*Ident)
	require.True(t, ok, "expected Ident, got %T", node)
	assert.Equal(t, "github", ident.Name)
}

func TestParsePropertyAccessChain(t *testing.T) {
	node, err := Parse("github.event.pull_request.head.ref")
	require.NoError(t, err)

	// Should be: DotAccess(DotAccess(DotAccess(DotAccess(Ident("github"), "event"), "pull_request"), "head"), "ref")
	d1, ok := node.(*DotAccess)
	require.True(t, ok, "expected DotAccess, got %T", node)
	assert.Equal(t, "ref", d1.Field)

	d2, ok := d1.Object.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "head", d2.Field)

	d3, ok := d2.Object.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "pull_request", d3.Field)

	d4, ok := d3.Object.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "event", d4.Field)

	ident, ok := d4.Object.(*Ident)
	require.True(t, ok)
	assert.Equal(t, "github", ident.Name)
}

func TestParseIndexAccess(t *testing.T) {
	node, err := Parse("matrix['os']")
	require.NoError(t, err)

	idx, ok := node.(*IndexAccessNode)
	require.True(t, ok, "expected IndexAccessNode, got %T", node)

	ident, ok := idx.Object.(*Ident)
	require.True(t, ok)
	assert.Equal(t, "matrix", ident.Name)

	lit, ok := idx.Index.(*Literal)
	require.True(t, ok)
	assert.Equal(t, String("os"), lit.Val)
}

func TestParseIndexAccessNumeric(t *testing.T) {
	node, err := Parse("arr[0]")
	require.NoError(t, err)

	idx, ok := node.(*IndexAccessNode)
	require.True(t, ok)

	lit, ok := idx.Index.(*Literal)
	require.True(t, ok)
	assert.True(t, Number(0).Equals(lit.Val))
}

func TestParseFunctionCall(t *testing.T) {
	node, err := Parse("contains('hello world', 'hello')")
	require.NoError(t, err)

	fn, ok := node.(*FunctionCall)
	require.True(t, ok, "expected FunctionCall, got %T", node)
	assert.Equal(t, "contains", fn.Name)
	require.Len(t, fn.Args, 2)

	arg1, ok := fn.Args[0].(*Literal)
	require.True(t, ok)
	assert.Equal(t, String("hello world"), arg1.Val)

	arg2, ok := fn.Args[1].(*Literal)
	require.True(t, ok)
	assert.Equal(t, String("hello"), arg2.Val)
}

func TestParseFunctionCallNoArgs(t *testing.T) {
	node, err := Parse("success()")
	require.NoError(t, err)

	fn, ok := node.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "success", fn.Name)
	assert.Len(t, fn.Args, 0)
}

func TestParseNestedFunctionCall(t *testing.T) {
	node, err := Parse("contains(toJSON(github), 'main')")
	require.NoError(t, err)

	fn, ok := node.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "contains", fn.Name)
	require.Len(t, fn.Args, 2)

	inner, ok := fn.Args[0].(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "toJSON", inner.Name)
}

func TestParseUnaryNot(t *testing.T) {
	node, err := Parse("!true")
	require.NoError(t, err)

	un, ok := node.(*UnaryOp)
	require.True(t, ok, "expected UnaryOp, got %T", node)
	assert.Equal(t, "!", un.Op)

	lit, ok := un.Operand.(*Literal)
	require.True(t, ok)
	assert.Equal(t, Bool(true), lit.Val)
}

func TestParseDoubleNegation(t *testing.T) {
	node, err := Parse("!!false")
	require.NoError(t, err)

	un1, ok := node.(*UnaryOp)
	require.True(t, ok)
	un2, ok := un1.Operand.(*UnaryOp)
	require.True(t, ok)
	lit, ok := un2.Operand.(*Literal)
	require.True(t, ok)
	assert.Equal(t, Bool(false), lit.Val)
}

func TestParseBinaryComparison(t *testing.T) {
	tests := []struct {
		input string
		op    string
	}{
		{"a == b", "=="},
		{"a != b", "!="},
		{"a < b", "<"},
		{"a <= b", "<="},
		{"a > b", ">"},
		{"a >= b", ">="},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			bin, ok := node.(*BinaryOp)
			require.True(t, ok, "expected BinaryOp, got %T", node)
			assert.Equal(t, tt.op, bin.Op)
		})
	}
}

func TestParseLogicalOperators(t *testing.T) {
	// a && b || c should parse as (a && b) || c
	node, err := Parse("a && b || c")
	require.NoError(t, err)

	or, ok := node.(*LogicalOp)
	require.True(t, ok, "expected LogicalOp, got %T", node)
	assert.Equal(t, "||", or.Op)

	and, ok := or.Left.(*LogicalOp)
	require.True(t, ok, "expected LogicalOp for left of ||, got %T", or.Left)
	assert.Equal(t, "&&", and.Op)
}

func TestParsePrecedence(t *testing.T) {
	// a || b && c == d should parse as a || (b && (c == d))
	node, err := Parse("a || b && c == d")
	require.NoError(t, err)

	or, ok := node.(*LogicalOp)
	require.True(t, ok)
	assert.Equal(t, "||", or.Op)

	and, ok := or.Right.(*LogicalOp)
	require.True(t, ok)
	assert.Equal(t, "&&", and.Op)

	eq, ok := and.Right.(*BinaryOp)
	require.True(t, ok)
	assert.Equal(t, "==", eq.Op)
}

func TestParseGrouping(t *testing.T) {
	node, err := Parse("(a || b) && c")
	require.NoError(t, err)

	and, ok := node.(*LogicalOp)
	require.True(t, ok)
	assert.Equal(t, "&&", and.Op)

	grp, ok := and.Left.(*Grouping)
	require.True(t, ok)

	or, ok := grp.Expr.(*LogicalOp)
	require.True(t, ok)
	assert.Equal(t, "||", or.Op)
}

func TestParseComplexExpression(t *testing.T) {
	input := "github.event_name == 'push' && contains(github.ref, 'main')"
	node, err := Parse(input)
	require.NoError(t, err)

	and, ok := node.(*LogicalOp)
	require.True(t, ok)
	assert.Equal(t, "&&", and.Op)

	// Left: github.event_name == 'push'
	eq, ok := and.Left.(*BinaryOp)
	require.True(t, ok)
	assert.Equal(t, "==", eq.Op)

	// Right: contains(github.ref, 'main')
	fn, ok := and.Right.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "contains", fn.Name)
	require.Len(t, fn.Args, 2)
}

func TestParseStepsExpression(t *testing.T) {
	input := "steps.build.outputs.version"
	node, err := Parse(input)
	require.NoError(t, err)

	// Should be a chain of DotAccess
	d1, ok := node.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "version", d1.Field)
}

func TestParseMixedAccess(t *testing.T) {
	input := "matrix['os']"
	node, err := Parse(input)
	require.NoError(t, err)
	_, ok := node.(*IndexAccessNode)
	require.True(t, ok)
}

func TestParseErrorUnexpectedToken(t *testing.T) {
	_, err := Parse("==")
	assert.Error(t, err)
}

func TestParseErrorUnclosedParen(t *testing.T) {
	_, err := Parse("(a == b")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected )")
}

func TestParseErrorUnclosedBracket(t *testing.T) {
	_, err := Parse("a[0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected ]")
}

func TestParseErrorTrailingToken(t *testing.T) {
	_, err := Parse("a b")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected token")
}

func TestParseErrorEmptyInput(t *testing.T) {
	_, err := Parse("")
	assert.Error(t, err)
}

func TestParseFunctionWithDottedArg(t *testing.T) {
	node, err := Parse("startsWith(github.ref, 'refs/heads/')")
	require.NoError(t, err)

	fn, ok := node.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "startsWith", fn.Name)
	require.Len(t, fn.Args, 2)

	dot, ok := fn.Args[0].(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "ref", dot.Field)
}

func TestParseNotWithComparison(t *testing.T) {
	node, err := Parse("!cancelled()")
	require.NoError(t, err)

	un, ok := node.(*UnaryOp)
	require.True(t, ok)

	fn, ok := un.Operand.(*FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "cancelled", fn.Name)
}

func TestParseMultipleEqualities(t *testing.T) {
	// a == b != c should parse as (a == b) != c
	node, err := Parse("a == b != c")
	require.NoError(t, err)

	ne, ok := node.(*BinaryOp)
	require.True(t, ok)
	assert.Equal(t, "!=", ne.Op)

	eq, ok := ne.Left.(*BinaryOp)
	require.True(t, ok)
	assert.Equal(t, "==", eq.Op)
}
