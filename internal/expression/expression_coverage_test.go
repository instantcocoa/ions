package expression

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// token.go — cover all TokenType.String() branches
// ---------------------------------------------------------------------------

func TestTokenTypeStringAllBranches(t *testing.T) {
	tests := []struct {
		tok  TokenType
		want string
	}{
		{TokenEOF, "EOF"},
		{TokenNull, "null"},
		{TokenTrue, "true"},
		{TokenFalse, "false"},
		{TokenNumber, "number"},
		{TokenString, "string"},
		{TokenIdent, "ident"},
		{TokenDot, "."},
		{TokenLBracket, "["},
		{TokenRBracket, "]"},
		{TokenLParen, "("},
		{TokenRParen, ")"},
		{TokenComma, ","},
		{TokenBang, "!"},
		{TokenEqEq, "=="},
		{TokenBangEq, "!="},
		{TokenLess, "<"},
		{TokenLessEq, "<="},
		{TokenGreater, ">"},
		{TokenGreaterEq, ">="},
		{TokenAnd, "&&"},
		{TokenOr, "||"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.tok.String())
		})
	}
}

func TestTokenTypeStringDefault(t *testing.T) {
	// Unknown token type should hit the default case
	unknown := TokenType(999)
	result := unknown.String()
	assert.Contains(t, result, "999")
}

// ---------------------------------------------------------------------------
// value.go — cover GoString default, toGoValue NaN/Inf, Kind.String default,
// Equals default return false
// ---------------------------------------------------------------------------

func TestKindStringDefault(t *testing.T) {
	// Unknown Kind should return "unknown"
	unknown := Kind(99)
	assert.Equal(t, "unknown", unknown.String())
}

func TestGoStringDefault(t *testing.T) {
	// A Value with an unknown kind should hit the default GoString case
	v := Value{kind: Kind(99)}
	assert.Equal(t, "Value{?}", v.GoString())
}

func TestToGoValueNaNInf(t *testing.T) {
	// NaN and Inf numbers should produce nil in toGoValue
	nan := Number(math.NaN())
	assert.Nil(t, nan.toGoValue())

	inf := Number(math.Inf(1))
	assert.Nil(t, inf.toGoValue())

	negInf := Number(math.Inf(-1))
	assert.Nil(t, negInf.toGoValue())
}

func TestToGoValueArray(t *testing.T) {
	arr := Array([]Value{String("a"), Number(1), Bool(true), Null()})
	goVal := arr.toGoValue()
	slice, ok := goVal.([]interface{})
	require.True(t, ok)
	assert.Equal(t, 4, len(slice))
	assert.Equal(t, "a", slice[0])
	assert.Equal(t, 1.0, slice[1])
	assert.Equal(t, true, slice[2])
	assert.Nil(t, slice[3])
}

func TestToGoValueObject(t *testing.T) {
	obj := Object(map[string]Value{"key": String("val")})
	goVal := obj.toGoValue()
	m, ok := goVal.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "val", m["key"])
}

func TestValueEqualsDefaultKind(t *testing.T) {
	// Two Values with unknown kind -- hits the default false branch in Equals
	v1 := Value{kind: Kind(99)}
	v2 := Value{kind: Kind(99)}
	assert.False(t, v1.Equals(v2))
}

// ---------------------------------------------------------------------------
// coercion.go — cover IsTruthy default, CoerceToString array/object
// ---------------------------------------------------------------------------

func TestIsTruthyDefaultKind(t *testing.T) {
	// Unknown kind should return false
	v := Value{kind: Kind(99)}
	assert.False(t, IsTruthy(v))
}

func TestCoerceToStringArrayObject(t *testing.T) {
	arr := Array([]Value{String("x"), Number(1)})
	assert.Equal(t, `["x",1]`, CoerceToString(arr))

	obj := Object(map[string]Value{"k": Bool(true)})
	assert.Equal(t, `{"k":true}`, CoerceToString(obj))
}

func TestCoerceToStringDefaultKind(t *testing.T) {
	// Unknown kind should return ""
	v := Value{kind: Kind(99)}
	assert.Equal(t, "", CoerceToString(v))
}

func TestCoerceToNumberDefaultKind(t *testing.T) {
	// Unknown kind should return NaN
	v := Value{kind: Kind(99)}
	assert.True(t, math.IsNaN(CoerceToNumber(v)))
}

func TestCoerceToNumberHexString(t *testing.T) {
	assert.Equal(t, 255.0, CoerceToNumber(String("0xff")))
	assert.Equal(t, 255.0, CoerceToNumber(String("0XFF")))
}

// ---------------------------------------------------------------------------
// parser.go — cover parseNumber negative hex path
// ---------------------------------------------------------------------------

func TestParseNumberNegativeHex(t *testing.T) {
	// Negative hex number should parse correctly
	n, err := parseNumber("-0xff")
	require.NoError(t, err)
	assert.Equal(t, float64(-255), n)

	n2, err := parseNumber("-0X1A")
	require.NoError(t, err)
	assert.Equal(t, float64(-26), n2)
}

func TestParseNumberRegularFloat(t *testing.T) {
	n, err := parseNumber("3.14159")
	require.NoError(t, err)
	assert.InDelta(t, 3.14159, n, 0.0001)
}

func TestParseNumberHex(t *testing.T) {
	n, err := parseNumber("0xff")
	require.NoError(t, err)
	assert.Equal(t, float64(255), n)
}

// ---------------------------------------------------------------------------
// parser.go — cover parsePostfix keyword-after-dot, non-ident function call
// ---------------------------------------------------------------------------

func TestParsePostfixKeywordAfterDot(t *testing.T) {
	// e.g., steps.null — keyword "null" after dot
	node, err := Parse("steps.null")
	require.NoError(t, err)
	da, ok := node.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "null", da.Field)

	// steps.true
	node2, err := Parse("steps.true")
	require.NoError(t, err)
	da2, ok := node2.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "true", da2.Field)

	// steps.false
	node3, err := Parse("steps.false")
	require.NoError(t, err)
	da3, ok := node3.(*DotAccess)
	require.True(t, ok)
	assert.Equal(t, "false", da3.Field)
}

func TestParsePostfixNonIdentFunctionCall(t *testing.T) {
	// Trying to call something that isn't an identifier as a function
	// The parser sees 42 then ( and tries to call a non-Ident node as a function
	_, err := Parse("42()")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot call non-identifier")
}

func TestParsePostfixDotWithoutIdentAfter(t *testing.T) {
	// Dot followed by something that isn't an identifier or keyword
	_, err := Parse("a.42")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected identifier after '.'")
}

// ---------------------------------------------------------------------------
// evaluator.go — cover evalDotAccess error, evalIndexAccess error,
// evalUnaryOp unknown, evalBinaryOp unknown, evalLogicalOp unknown,
// Eval default node type
// ---------------------------------------------------------------------------

func TestEvalDotAccessErrorPropagation(t *testing.T) {
	// DotAccess whose Object fails to evaluate
	eval := NewEvaluator(MapContext{})
	// FunctionCall that doesn't exist, as the object of a DotAccess
	badNode := &DotAccess{
		Object: &FunctionCall{Name: "nonexistent", Args: nil},
		Field:  "test",
	}
	_, err := eval.Eval(badNode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown function")
}

func TestEvalIndexAccessObjectError(t *testing.T) {
	// IndexAccessNode whose Object fails to evaluate
	eval := NewEvaluator(MapContext{})
	badNode := &IndexAccessNode{
		Object: &FunctionCall{Name: "nonexistent", Args: nil},
		Index:  &Literal{Val: Number(0)},
	}
	_, err := eval.Eval(badNode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown function")
}

func TestEvalIndexAccessIndexError(t *testing.T) {
	// IndexAccessNode whose Index fails to evaluate
	eval := NewEvaluator(MapContext{})
	badNode := &IndexAccessNode{
		Object: &Literal{Val: Array([]Value{String("x")})},
		Index:  &FunctionCall{Name: "nonexistent", Args: nil},
	}
	_, err := eval.Eval(badNode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown function")
}

func TestEvalUnaryOpUnknownOperator(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &UnaryOp{Op: "+", Operand: &Literal{Val: Number(1)}}
	_, err := eval.Eval(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown unary operator")
}

func TestEvalBinaryOpUnknownOperator(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &BinaryOp{
		Op:    "+",
		Left:  &Literal{Val: Number(1)},
		Right: &Literal{Val: Number(2)},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown binary operator")
}

func TestEvalBinaryOpLeftError(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &BinaryOp{
		Op:    "==",
		Left:  &FunctionCall{Name: "nonexistent", Args: nil},
		Right: &Literal{Val: Number(1)},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
}

func TestEvalBinaryOpRightError(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &BinaryOp{
		Op:    "==",
		Left:  &Literal{Val: Number(1)},
		Right: &FunctionCall{Name: "nonexistent", Args: nil},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
}

func TestEvalLogicalOpUnknownOperator(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &LogicalOp{
		Op:    "^^",
		Left:  &Literal{Val: Bool(true)},
		Right: &Literal{Val: Bool(false)},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown logical operator")
}

func TestEvalLogicalOpLeftError(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &LogicalOp{
		Op:    "||",
		Left:  &FunctionCall{Name: "nonexistent", Args: nil},
		Right: &Literal{Val: Bool(true)},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
}

func TestEvalLogicalOpRightErrorOr(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &LogicalOp{
		Op:    "||",
		Left:  &Literal{Val: Bool(false)}, // falsy, so evaluates Right
		Right: &FunctionCall{Name: "nonexistent", Args: nil},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
}

func TestEvalLogicalOpRightErrorAnd(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &LogicalOp{
		Op:    "&&",
		Left:  &Literal{Val: Bool(true)}, // truthy, so evaluates Right
		Right: &FunctionCall{Name: "nonexistent", Args: nil},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
}

func TestEvalUnknownNodeType(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	// Create a custom node type that isn't any known type
	_, err := eval.Eval(customNode{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown node type")
}

type customNode struct{}

func (c customNode) nodeType() string { return "custom" }

func TestEvalFunctionCallArgError(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &FunctionCall{
		Name: "contains",
		Args: []Node{
			&FunctionCall{Name: "nonexistent", Args: nil}, // will error
			&Literal{Val: String("x")},
		},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error evaluating argument")
}

// ---------------------------------------------------------------------------
// evaluator.go — cover equalCompare array/object comparison paths
// ---------------------------------------------------------------------------

func TestEqualCompareArrays(t *testing.T) {
	a := Array([]Value{String("x")})
	b := Array([]Value{String("x")})
	c := Array([]Value{String("y")})
	assert.True(t, equalCompare(a, b))
	assert.False(t, equalCompare(a, c))
}

func TestEqualCompareObjects(t *testing.T) {
	a := Object(map[string]Value{"k": String("v")})
	b := Object(map[string]Value{"k": String("v")})
	c := Object(map[string]Value{"k": String("w")})
	assert.True(t, equalCompare(a, b))
	assert.False(t, equalCompare(a, c))
}

func TestEqualCompareNaNNumbers(t *testing.T) {
	a := Number(math.NaN())
	b := Number(math.NaN())
	// NaN != NaN in equalCompare
	assert.False(t, equalCompare(a, b))
}

func TestEqualCompareCrossTypeNaN(t *testing.T) {
	// Cross-type: string "abc" vs number 42
	// "abc" coerces to NaN, comparison should be false
	assert.False(t, equalCompare(String("abc"), Number(42)))
}

func TestEqualCompareCrossTypeBoolNumber(t *testing.T) {
	// Bool vs Number: coerce both to numbers
	assert.True(t, equalCompare(Bool(true), Number(1)))
	assert.True(t, equalCompare(Bool(false), Number(0)))
}

func TestEqualCompareNullVsNotNull(t *testing.T) {
	// null only equals null
	assert.False(t, equalCompare(Null(), Number(0)))
	assert.False(t, equalCompare(Number(0), Null()))
	assert.False(t, equalCompare(Null(), Bool(false)))
	assert.False(t, equalCompare(Bool(false), Null()))
	assert.False(t, equalCompare(Null(), String("")))
	assert.False(t, equalCompare(String(""), Null()))
}

// ---------------------------------------------------------------------------
// evaluator.go — cover evalComparisonOp default branch
// ---------------------------------------------------------------------------

func TestEvalComparisonOpUnknownDefault(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	// Construct a BinaryOp with an unknown comparison operator that would
	// reach evalComparisonOp. Since this goes through evalBinaryOp which
	// routes to evalComparisonOp, we use the internal path directly.
	// This should not normally happen, but tests the default case.
	_, err := eval.evalComparisonOp("~=", Number(1), Number(2))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown comparison operator")
}

// ---------------------------------------------------------------------------
// evaluator.go — cover evalUnaryOp error in operand
// ---------------------------------------------------------------------------

func TestEvalUnaryOpOperandError(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node := &UnaryOp{
		Op:      "!",
		Operand: &FunctionCall{Name: "nonexistent", Args: nil},
	}
	_, err := eval.Eval(node)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// functions.go — cover LookupFunction NaN path
// ---------------------------------------------------------------------------

func TestLookupFunctionNaN(t *testing.T) {
	fns := BuiltinFunctions()
	fn, ok := LookupFunction(fns, "NaN")
	assert.True(t, ok)
	assert.NotNil(t, fn)

	result, err := fn(nil)
	require.NoError(t, err)
	assert.True(t, math.IsNaN(result.NumberVal()))
}

func TestLookupFunctionNaNCaseInsensitive(t *testing.T) {
	fns := BuiltinFunctions()
	fn, ok := LookupFunction(fns, "nan")
	assert.True(t, ok)
	assert.NotNil(t, fn)
}

// ---------------------------------------------------------------------------
// functions.go — cover fnJoin with non-array non-string argument
// ---------------------------------------------------------------------------

func TestJoinNonArrayNonString(t *testing.T) {
	// Passing a number to join should coerce to string
	result, err := fnJoin([]Value{Number(42)})
	require.NoError(t, err)
	assert.Equal(t, String("42"), result)

	// Passing a bool
	result2, err := fnJoin([]Value{Bool(true)})
	require.NoError(t, err)
	assert.Equal(t, String("true"), result2)

	// Passing null
	result3, err := fnJoin([]Value{Null()})
	require.NoError(t, err)
	assert.Equal(t, String(""), result3)
}

// ---------------------------------------------------------------------------
// functions.go — cover fnFormat unmatched brace edge cases
// ---------------------------------------------------------------------------

func TestFormatUnmatchedOpenBrace(t *testing.T) {
	// A single { without matching digits or }
	result, err := fnFormat([]Value{String("{abc}")})
	require.NoError(t, err)
	assert.Equal(t, String("{abc}"), result)
}

func TestFormatSingleOpenBrace(t *testing.T) {
	// A lone { at end of string
	result, err := fnFormat([]Value{String("end{")})
	require.NoError(t, err)
	assert.Equal(t, String("end{"), result)
}

func TestFormatSingleCloseBrace(t *testing.T) {
	// A lone } not followed by another }
	result, err := fnFormat([]Value{String("a}b")})
	require.NoError(t, err)
	assert.Equal(t, String("a}b"), result)
}

func TestFormatOpenBraceWithNoDigits(t *testing.T) {
	// { followed by something that isn't a digit
	result, err := fnFormat([]Value{String("{x}")})
	require.NoError(t, err)
	assert.Equal(t, String("{x}"), result)
}

// ---------------------------------------------------------------------------
// lexer.go — cover lexNumber negative hex, exponent edge cases
// ---------------------------------------------------------------------------

func TestLexNegativeHex(t *testing.T) {
	tokens, err := Lex("-0xff")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(tokens), 2)
	assert.Equal(t, TokenNumber, tokens[0].Type)
	assert.Equal(t, "-0xff", tokens[0].Value)
}

func TestLexHexInvalidDigit(t *testing.T) {
	// "0x" followed by a non-hex char
	_, err := Lex("0xG")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected hex digit")
}

func TestLexExponentMissingDigit(t *testing.T) {
	_, err := Lex("1e")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected digit in exponent")
}

func TestLexExponentWithSignMissingDigit(t *testing.T) {
	_, err := Lex("1e+")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected digit in exponent")
}

// ---------------------------------------------------------------------------
// lexer.go — cover edge cases in peek/advance past end
// ---------------------------------------------------------------------------

func TestLexEmptyString(t *testing.T) {
	tokens, err := Lex("")
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, TokenEOF, tokens[0].Type)
}

func TestLexSingleChar(t *testing.T) {
	tokens, err := Lex("!")
	require.NoError(t, err)
	assert.Equal(t, TokenBang, tokens[0].Type)
}

// ---------------------------------------------------------------------------
// parser.go — parseArgs error paths, parseLogicalOr/And error paths
// ---------------------------------------------------------------------------

func TestParseArgsError(t *testing.T) {
	// Function with bad arg expression
	_, err := Parse("contains(==, 'b')")
	assert.Error(t, err)
}

func TestParseLogicalOrRightError(t *testing.T) {
	_, err := Parse("a || ==")
	assert.Error(t, err)
}

func TestParseLogicalAndRightError(t *testing.T) {
	_, err := Parse("a && ==")
	assert.Error(t, err)
}

func TestParseEqualityRightError(t *testing.T) {
	_, err := Parse("a == ==")
	assert.Error(t, err)
}

func TestParseComparisonRightError(t *testing.T) {
	_, err := Parse("a < ==")
	assert.Error(t, err)
}

func TestParseUnaryError(t *testing.T) {
	_, err := Parse("!==")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Complete operator combination coverage
// ---------------------------------------------------------------------------

func TestAllComparisonOperatorCombinations(t *testing.T) {
	eval := NewEvaluator(MapContext{})

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// < with numbers
		{"1 < 2", "1 < 2", true},
		{"2 < 1", "2 < 1", false},
		{"1 < 1", "1 < 1", false},
		// <= with numbers
		{"1 <= 2", "1 <= 2", true},
		{"2 <= 1", "2 <= 1", false},
		{"1 <= 1", "1 <= 1", true},
		// > with numbers
		{"2 > 1", "2 > 1", true},
		{"1 > 2", "1 > 2", false},
		{"1 > 1", "1 > 1", false},
		// >= with numbers
		{"2 >= 1", "2 >= 1", true},
		{"1 >= 2", "1 >= 2", false},
		{"1 >= 1", "1 >= 1", true},
		// == and != with various types
		{"bool true == true", "true == true", true},
		{"bool false == false", "false == false", true},
		{"string case insensitive", "'ABC' == 'abc'", true},
		{"null == null", "null == null", true},
		{"null != true", "null != true", true},
		{"null != 0", "null != 0", true},
		// Cross-type numeric coercion
		{"true == 1", "true == 1", true},
		{"false == 0", "false == 0", true},
		{"string 1 == 1", "'1' == 1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.Equal(t, Bool(tt.want), result)
		})
	}
}

// ---------------------------------------------------------------------------
// Type coercion edge cases
// ---------------------------------------------------------------------------

func TestTypeCrossCoecion(t *testing.T) {
	eval := NewEvaluator(MapContext{})

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		// null coercion
		{"null to number comparison", "null < 1", Bool(true)},
		// Bool coercion
		{"true > false numeric", "true > false", Bool(true)},
		// String to number
		{"string numeric comparison", "'10' > '2'", Bool(true)},
		{"empty string == 0", "'' == 0", Bool(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "%s: expected %v, got %v", tt.name, tt.want.GoString(), result.GoString())
		})
	}
}

// ---------------------------------------------------------------------------
// Deeply nested property access and function calls
// ---------------------------------------------------------------------------

func TestDeeplyNestedPropertyAccess(t *testing.T) {
	ctx := MapContext{
		"a": Object(map[string]Value{
			"b": Object(map[string]Value{
				"c": Object(map[string]Value{
					"d": Object(map[string]Value{
						"e": String("deep"),
					}),
				}),
			}),
		}),
	}
	eval := NewEvaluator(ctx)
	node, err := Parse("a.b.c.d.e")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, String("deep"), result)
}

func TestNestedFunctionCalls(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("contains(format('{0} world', 'hello'), 'hello world')")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, Bool(true), result)
}

func TestTripleNestedFunction(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("startsWith(format('{0}', join(fromJSON('[\"a\",\"b\"]'), '-')), 'a-b')")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, Bool(true), result)
}

// ---------------------------------------------------------------------------
// MarshalJSON edge cases
// ---------------------------------------------------------------------------

func TestValueMarshalJSONArray(t *testing.T) {
	arr := Array([]Value{Number(1), String("two"), Bool(true), Null()})
	data, err := arr.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `[1,"two",true,null]`, string(data))
}

func TestValueMarshalJSONObject(t *testing.T) {
	obj := Object(map[string]Value{"key": String("value")})
	data, err := obj.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"key":"value"`)
}

func TestValueMarshalJSONNaN(t *testing.T) {
	nan := Number(math.NaN())
	data, err := nan.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

// ---------------------------------------------------------------------------
// EvalInterpolation edge cases
// ---------------------------------------------------------------------------

func TestEvalInterpolationEvalError(t *testing.T) {
	// Expression that parses OK but errors during eval
	_, err := EvalInterpolation("${{ nonexistent() }}", MapContext{})
	assert.Error(t, err)
	// The error should indicate an eval issue
	assert.Contains(t, err.Error(), "error")
}

// ---------------------------------------------------------------------------
// formatNumber edge cases
// ---------------------------------------------------------------------------

func TestFormatNumberEdgeCases(t *testing.T) {
	assert.Equal(t, "NaN", formatNumber(math.NaN()))
	assert.Equal(t, "Infinity", formatNumber(math.Inf(1)))
	assert.Equal(t, "-Infinity", formatNumber(math.Inf(-1)))
	assert.Equal(t, "0", formatNumber(0))
	assert.Equal(t, "1000000", formatNumber(1000000))
	assert.Equal(t, "3.14", formatNumber(3.14))
}

// ---------------------------------------------------------------------------
// Eval with complex expression trees
// ---------------------------------------------------------------------------

func TestEvalExpressionWithStatusSuccess(t *testing.T) {
	fns := BuiltinFunctions()
	ctx := MapContext{
		"github": Object(map[string]Value{
			"event_name": String("push"),
		}),
	}
	result, err := EvalExpressionWithStatus(
		"success() && github.event_name == 'push'",
		ctx, fns, "success",
	)
	require.NoError(t, err)
	assert.Equal(t, Bool(true), result)
}

func TestEvalExpressionWithStatusCancelled(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithStatus(
		"cancelled()",
		MapContext{}, fns, "cancelled",
	)
	require.NoError(t, err)
	assert.True(t, IsTruthy(result))
}

// ---------------------------------------------------------------------------
// Cover parsePostfix bracket error in index expression
// ---------------------------------------------------------------------------

func TestParsePostfixBadIndexExpression(t *testing.T) {
	_, err := Parse("a[==]")
	assert.Error(t, err)
}

func TestParsePostfixMissingCloseBracket(t *testing.T) {
	_, err := Parse("a[0")
	assert.Error(t, err)
}

func TestParseGroupingMissingCloseParen(t *testing.T) {
	_, err := Parse("(a")
	assert.Error(t, err)
}

func TestParseFunctionMissingCloseParen(t *testing.T) {
	_, err := Parse("contains('a', 'b'")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Cover findClosingBraces edge cases
// ---------------------------------------------------------------------------

func TestFindClosingBracesEscapedQuote(t *testing.T) {
	// String literal with escaped quote before }}
	s := " 'it''s' }}"
	idx := findClosingBraces(s)
	assert.Equal(t, 9, idx)
}

func TestFindClosingBracesNoClose(t *testing.T) {
	idx := findClosingBraces(" something without closing")
	assert.Equal(t, -1, idx)
}

// ---------------------------------------------------------------------------
// Lexer edge cases — peek/peekAt/advance at end-of-input
// ---------------------------------------------------------------------------

func TestLexer_PeekAtEndOfInput(t *testing.T) {
	l := &Lexer{input: []rune(""), pos: 0}
	assert.Equal(t, rune(0), l.peek())
}

func TestLexer_PeekAtBeyondEnd(t *testing.T) {
	l := &Lexer{input: []rune("a"), pos: 0}
	assert.Equal(t, rune(0), l.peekAt(5))
}

func TestLexer_AdvanceAtEnd(t *testing.T) {
	l := &Lexer{input: []rune(""), pos: 0}
	assert.Equal(t, rune(0), l.advance())
}

// ---------------------------------------------------------------------------
// Parser edge cases — peek at end, error propagation
// ---------------------------------------------------------------------------

func TestParser_PeekAtEnd(t *testing.T) {
	p := &Parser{tokens: []Token{}, pos: 0}
	tok := p.peek()
	assert.Equal(t, TokenEOF, tok.Type)
}

func TestParseUnary_ErrorPropagation(t *testing.T) {
	// "! (" without closing paren — should error in the nested parse
	_, err := EvalExpression("!(", MapContext{})
	assert.Error(t, err)
}

func TestParseArgs_ErrorInSecondArg(t *testing.T) {
	// contains('a', ) — missing second argument should error
	_, err := EvalExpression("contains('a', )", MapContext{})
	assert.Error(t, err)
}

func TestParsePrimary_NumberError(t *testing.T) {
	// parseNumber error: invalid hex literal
	_, err := parseNumber("0xZZZZ")
	assert.Error(t, err)
}

func TestParseNumber_NegativeHexError(t *testing.T) {
	_, err := parseNumber("-0xZZZZ")
	assert.Error(t, err)
}

func TestParsePrimary_UnexpectedToken(t *testing.T) {
	// Just a closing paren is unexpected
	_, err := EvalExpression(")", MapContext{})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// CoerceToString — default kind case (should be unreachable but defensive)
// ---------------------------------------------------------------------------

func TestCoerceToString_UnknownKind(t *testing.T) {
	// Create a Value with an unknown kind directly
	v := Value{kind: Kind(99)}
	s := CoerceToString(v)
	assert.Equal(t, "", s)
}

// ---------------------------------------------------------------------------
// fnToJSON — json.MarshalIndent error (hard to trigger normally)
// ---------------------------------------------------------------------------

func TestFnToJSON_NullValue(t *testing.T) {
	result, err := fnToJSON([]Value{Null()})
	require.NoError(t, err)
	assert.Equal(t, "null", result.stringVal)
}

func TestFnToJSON_ArrayValue(t *testing.T) {
	arr := Array([]Value{Number(1), String("hi")})
	result, err := fnToJSON([]Value{arr})
	require.NoError(t, err)
	assert.Contains(t, result.stringVal, "1")
	assert.Contains(t, result.stringVal, "hi")
}

func TestFnToJSON_ObjectValue(t *testing.T) {
	obj := Object(map[string]Value{"key": String("val")})
	result, err := fnToJSON([]Value{obj})
	require.NoError(t, err)
	assert.Contains(t, result.stringVal, "key")
	assert.Contains(t, result.stringVal, "val")
}

// ---------------------------------------------------------------------------
// goToValue default case
// ---------------------------------------------------------------------------

func TestGoToValue_DefaultType(t *testing.T) {
	// An unexpected type (like a struct) should return Null
	type custom struct{ x int }
	v := goToValue(custom{42})
	assert.Equal(t, KindNull, v.Kind())
}

// ---------------------------------------------------------------------------
// Value.Equals — object key mismatch
// ---------------------------------------------------------------------------

func TestValueEquals_ObjectKeyMismatch(t *testing.T) {
	a := Object(map[string]Value{"x": Number(1)})
	b := Object(map[string]Value{"y": Number(1)})
	assert.False(t, a.Equals(b))
}

func TestValueEquals_ObjectDifferentLength(t *testing.T) {
	a := Object(map[string]Value{"x": Number(1)})
	b := Object(map[string]Value{"x": Number(1), "y": Number(2)})
	assert.False(t, a.Equals(b))
}

func TestValueEquals_ObjectValueMismatch(t *testing.T) {
	a := Object(map[string]Value{"x": Number(1)})
	b := Object(map[string]Value{"x": Number(2)})
	assert.False(t, a.Equals(b))
}

// ---------------------------------------------------------------------------
// toGoValue default kind
// ---------------------------------------------------------------------------

func TestToGoValue_UnknownKind(t *testing.T) {
	v := Value{kind: Kind(99)}
	assert.Nil(t, v.toGoValue())
}

// ---------------------------------------------------------------------------
// makeHashFilesFunc — glob error and read error paths
// ---------------------------------------------------------------------------

func TestMakeHashFilesFunc_GlobNoMatch(t *testing.T) {
	dir := t.TempDir()
	fns := BuiltinFunctions()
	SetHashFilesWorkDir(fns, dir)
	// Pattern that matches nothing — should return empty string, no error.
	result, err := fns["hashfiles"]([]Value{String("*.nonexistent")})
	require.NoError(t, err)
	assert.Equal(t, KindString, result.Kind())
	assert.Equal(t, "", result.stringVal)
}

func TestMakeHashFilesFunc_HashesFileContents(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("content-a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("content-b"), 0o644))

	fns := BuiltinFunctions()
	SetHashFilesWorkDir(fns, dir)

	result, err := fns["hashfiles"]([]Value{String("*.txt")})
	require.NoError(t, err)
	assert.Equal(t, KindString, result.Kind())
	assert.Len(t, result.stringVal, 64) // SHA-256 hex
}

func TestMakeHashFilesFunc_ReadError(t *testing.T) {
	dir := t.TempDir()
	// Create a file then make it unreadable.
	f := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(f, []byte("data"), 0o644))

	fns := BuiltinFunctions()
	SetHashFilesWorkDir(fns, dir)

	// Make file unreadable
	require.NoError(t, os.Chmod(f, 0o000))
	defer os.Chmod(f, 0o644)

	_, err := fns["hashfiles"]([]Value{String("*.txt")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read error")
}

// ---------------------------------------------------------------------------
// globFiles — WalkDir error (inaccessible dir), filepath.Rel error paths
// ---------------------------------------------------------------------------

func TestGlobFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	matches, err := globFiles(dir, "**/*.go")
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestGlobFiles_MatchesFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "test.go"), []byte("package main"), 0o644))

	matches, err := globFiles(dir, "**/*.go")
	require.NoError(t, err)
	assert.Equal(t, []string{"sub/test.go"}, matches)
}

// ---------------------------------------------------------------------------
// matchParts — consecutive ** segments
// ---------------------------------------------------------------------------

func TestMatchDoublestar_ConsecutiveStars(t *testing.T) {
	// Pattern "**/**/*.go" should match "a/b/c.go"
	assert.True(t, matchDoublestar("**/**/*.go", "a/b/c.go"))
}

func TestMatchDoublestar_SingleStarSegment(t *testing.T) {
	// Pattern "*/*.go" should match "dir/file.go"
	assert.True(t, matchDoublestar("*/*.go", "dir/file.go"))
	// But not nested
	assert.False(t, matchDoublestar("*/*.go", "a/b/file.go"))
}
