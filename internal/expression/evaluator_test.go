package expression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalLiterals(t *testing.T) {
	eval := NewEvaluator(MapContext{})

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
		{"string", "'hello'", String("hello")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestEvalSimpleLookup(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"ref":        String("refs/heads/main"),
			"event_name": String("push"),
			"actor":      String("octocat"),
		}),
		"env": Object(map[string]Value{
			"CI":   String("true"),
			"HOME": String("/home/runner"),
		}),
	}
	eval := NewEvaluator(ctx)

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{"top level", "github", ctx["github"]},
		{"dot access", "github.ref", String("refs/heads/main")},
		{"dot access event_name", "github.event_name", String("push")},
		{"env", "env.CI", String("true")},
		{"missing context returns empty string", "unknown", String("")},
		{"missing property returns null", "github.missing", Null()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestEvalNestedPropertyAccess(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"event": Object(map[string]Value{
				"pull_request": Object(map[string]Value{
					"head": Object(map[string]Value{
						"ref": String("feature-branch"),
					}),
				}),
			}),
		}),
	}
	eval := NewEvaluator(ctx)

	node, err := Parse("github.event.pull_request.head.ref")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, String("feature-branch"), result)
}

func TestEvalCaseInsensitiveLookup(t *testing.T) {
	ctx := MapContext{
		"GitHub": Object(map[string]Value{
			"Ref": String("main"),
		}),
	}
	eval := NewEvaluator(ctx)

	node, err := Parse("github.ref")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, String("main"), result)
}

func TestEvalIndexAccess(t *testing.T) {
	ctx := MapContext{
		"matrix": Object(map[string]Value{
			"os": String("ubuntu-latest"),
		}),
		"arr": Array([]Value{String("first"), String("second")}),
	}
	eval := NewEvaluator(ctx)

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{"object string index", "matrix['os']", String("ubuntu-latest")},
		{"array numeric index", "arr[0]", String("first")},
		{"array index 1", "arr[1]", String("second")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestEvalFunctionCallInExpression(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"ref":        String("refs/heads/main"),
			"event_name": String("push"),
		}),
	}
	eval := NewEvaluator(ctx)

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{"contains", "contains(github.ref, 'main')", Bool(true)},
		{"contains negative", "contains(github.ref, 'develop')", Bool(false)},
		{"startsWith", "startsWith(github.ref, 'refs/heads')", Bool(true)},
		{"endsWith", "endsWith(github.ref, 'main')", Bool(true)},
		{"format", "format('Hello, {0}!', 'world')", String("Hello, world!")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestEvalComparisonOperators(t *testing.T) {
	ctx := MapContext{}
	eval := NewEvaluator(ctx)

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"number eq", "42 == 42", true},
		{"number neq", "42 != 43", true},
		{"string eq case insensitive", "'abc' == 'ABC'", true},
		{"string neq", "'abc' != 'def'", true},
		{"less", "1 < 2", true},
		{"less false", "2 < 1", false},
		{"less eq", "2 <= 2", true},
		{"greater", "2 > 1", true},
		{"greater false", "1 > 2", false},
		{"greater eq", "2 >= 2", true},
		{"bool eq", "true == true", true},
		{"bool neq", "true != false", true},
		{"null eq null", "null == null", true},
		{"null neq string", "null != ''", true},
		// Cross-type comparison: coerce to numbers
		{"true eq 1", "true == 1", true},
		{"false eq 0", "false == 0", true},
		{"string number eq", "'42' == 42", true},
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

func TestEvalUnaryNot(t *testing.T) {
	eval := NewEvaluator(MapContext{})

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"not true", "!true", false},
		{"not false", "!false", true},
		{"not null", "!null", true},
		{"not empty string", "!''", true},
		{"not non-empty string", "!'hello'", false},
		{"not zero", "!0", true},
		{"not one", "!1", false},
		{"double not", "!!true", true},
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

func TestEvalLogicalShortCircuit(t *testing.T) {
	eval := NewEvaluator(MapContext{})

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		// || returns the first truthy value, or the last value
		{"or truthy left", "true || false", Bool(true)},
		{"or falsy left", "false || true", Bool(true)},
		{"or both falsy", "false || ''", String("")},
		{"or both truthy", "'a' || 'b'", String("a")},
		{"empty string or fallback", "'' || 'fallback'", String("fallback")},
		{"null or fallback", "null || 'default'", String("default")},
		// && returns the first falsy value, or the last value
		{"and both truthy", "true && 'yes'", String("yes")},
		{"and falsy left", "false && true", Bool(false)},
		{"and falsy right", "true && false", Bool(false)},
		{"and both falsy", "null && false", Null()},
		{"and truthy result", "'a' && 'b'", String("b")},
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

func TestEvalStatusFunctions(t *testing.T) {
	tests := []struct {
		name      string
		jobStatus string
		input     string
		want      bool
	}{
		{"success default", "", "success()", true},
		{"success explicit", "success", "success()", true},
		{"success when failure", "failure", "success()", false},
		{"failure when failure", "failure", "failure()", true},
		{"failure when success", "success", "failure()", false},
		{"always", "failure", "always()", true},
		{"always when success", "success", "always()", true},
		{"cancelled when cancelled", "cancelled", "cancelled()", true},
		{"cancelled when success", "success", "cancelled()", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eval := NewEvaluator(MapContext{})
			eval.JobStatus = tt.jobStatus
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.Equal(t, Bool(tt.want), result)
		})
	}
}

func TestEvalComplexRealWorld(t *testing.T) {
	ctx := MapContext{
		"github": Object(map[string]Value{
			"ref":        String("refs/heads/main"),
			"event_name": String("push"),
		}),
		"needs": Object(map[string]Value{
			"build": Object(map[string]Value{
				"result": String("success"),
				"outputs": Object(map[string]Value{
					"version": String("1.2.3"),
				}),
			}),
		}),
		"steps": Object(map[string]Value{
			"test": Object(map[string]Value{
				"outputs": Object(map[string]Value{
					"passed": String("true"),
				}),
				"outcome":    String("success"),
				"conclusion": String("success"),
			}),
		}),
	}

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{
			"needs check and ref check",
			"needs.build.result == 'success' && github.ref == 'refs/heads/main'",
			Bool(true),
		},
		{
			"event name check",
			"github.event_name == 'push' && contains(github.ref, 'main')",
			Bool(true),
		},
		{
			"step outputs",
			"steps.test.outputs.passed",
			String("true"),
		},
		{
			"needs outputs",
			"needs.build.outputs.version",
			String("1.2.3"),
		},
		{
			"complex conditional",
			"(github.event_name == 'push' || github.event_name == 'pull_request') && needs.build.result == 'success'",
			Bool(true),
		},
		{
			"negated condition",
			"!contains(github.ref, 'develop')",
			Bool(true),
		},
		{
			"format with context",
			"format('Version: {0}', needs.build.outputs.version)",
			String("Version: 1.2.3"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eval := NewEvaluator(ctx)
			node, err := Parse(tt.input)
			require.NoError(t, err)
			result, err := eval.Eval(node)
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestEvalExpression(t *testing.T) {
	ctx := MapContext{
		"env": Object(map[string]Value{
			"CI": String("true"),
		}),
	}
	result, err := EvalExpression("env.CI", ctx)
	require.NoError(t, err)
	assert.Equal(t, String("true"), result)
}

func TestEvalExpressionError(t *testing.T) {
	_, err := EvalExpression("((((", MapContext{})
	assert.Error(t, err)
}

func TestEvalUnknownFunction(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("nonexistent()")
	require.NoError(t, err)
	_, err = eval.Eval(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown function")
}

func TestEvalNullContext(t *testing.T) {
	eval := &Evaluator{
		Context:   nil,
		Functions: BuiltinFunctions(),
	}
	node, err := Parse("anything")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, String(""), result)
}

func TestEvalGrouping(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("(1 == 1)")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, Bool(true), result)
}

func TestEvalNullEquality(t *testing.T) {
	eval := NewEvaluator(MapContext{})

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"null == null", "null == null", true},
		{"null != ''", "null != ''", true},
		{"null != 0", "null != 0", true},
		{"null != false", "null != false", true},
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

func TestEvalMatrixContext(t *testing.T) {
	ctx := MapContext{
		"matrix": Object(map[string]Value{
			"os":           String("ubuntu-latest"),
			"node-version": Number(16),
		}),
	}
	eval := NewEvaluator(ctx)

	// Dot access with hyphen in identifier
	node, err := Parse("matrix.os")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, String("ubuntu-latest"), result)

	// Bracket access for hyphenated key
	node, err = Parse("matrix['node-version']")
	require.NoError(t, err)
	result, err = eval.Eval(node)
	require.NoError(t, err)
	assert.True(t, Number(16).Equals(result))
}

func TestEvalFromJSONInContext(t *testing.T) {
	ctx := MapContext{
		"steps": Object(map[string]Value{
			"data": Object(map[string]Value{
				"outputs": Object(map[string]Value{
					"json": String(`{"key":"value","count":42}`),
				}),
			}),
		}),
	}
	eval := NewEvaluator(ctx)

	node, err := Parse("fromJSON(steps.data.outputs.json)")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, KindObject, result.Kind())
	assert.Equal(t, String("value"), PropertyAccess(result, "key"))
	assert.True(t, Number(42).Equals(PropertyAccess(result, "count")))
}

func TestEvalComparisonWithNaN(t *testing.T) {
	eval := NewEvaluator(MapContext{})

	// Comparing non-numeric strings produces NaN, all comparisons should be false
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"nan lt", "'abc' < 'def'", false},
		{"nan gt", "'abc' > 'def'", false},
		{"nan le", "'abc' <= 'def'", false},
		{"nan ge", "'abc' >= 'def'", false},
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

func TestEvalStatusFunctionArgError(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("success(true)")
	require.NoError(t, err)
	_, err = eval.Eval(node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no arguments")
}

func TestEvalStatusFunctionArgErrors_AllFunctions(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	for _, fn := range []string{"failure(true)", "always(true)", "cancelled(true)"} {
		node, err := Parse(fn)
		require.NoError(t, err)
		_, err = eval.Eval(node)
		assert.Error(t, err, "expected error for %s", fn)
	}
}

func TestEvalExpressionWithFunctions(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithFunctions("contains('hello', 'ell')", MapContext{}, fns)
	require.NoError(t, err)
	assert.True(t, IsTruthy(result))
}

func TestEvalExpressionWithFunctions_ParseError(t *testing.T) {
	fns := BuiltinFunctions()
	_, err := EvalExpressionWithFunctions("invalid $$", MapContext{}, fns)
	assert.Error(t, err)
}

func TestEvalExpressionWithStatus_ParseError(t *testing.T) {
	fns := BuiltinFunctions()
	_, err := EvalExpressionWithStatus("invalid $$", MapContext{}, fns, "success")
	assert.Error(t, err)
}

func TestEvalExpression_ParseError(t *testing.T) {
	_, err := EvalExpression("invalid $$", MapContext{})
	assert.Error(t, err)
}

func TestEvalDotAccess_Missing(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	// Accessing property on null context returns null
	node, err := Parse("missing.field")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, Null(), result)
}

func TestEqualCompare_NullVsNull(t *testing.T) {
	assert.True(t, equalCompare(Null(), Null()))
}

func TestEqualCompare_NullVsString(t *testing.T) {
	assert.False(t, equalCompare(Null(), String("x")))
	assert.False(t, equalCompare(String("x"), Null()))
}

func TestEqualCompare_NaNValues(t *testing.T) {
	// NaN != NaN in number comparison
	eval := NewEvaluator(MapContext{})
	node, err := Parse("'abc' == 'def'")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	// String comparison is case-insensitive, these are different strings
	assert.Equal(t, Bool(false), result)
}

func TestEvalLogicalOr_ShortCircuit(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("true || false")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, Bool(true), result)
}

func TestEvalLogicalAnd_ShortCircuit(t *testing.T) {
	eval := NewEvaluator(MapContext{})
	node, err := Parse("false && true")
	require.NoError(t, err)
	result, err := eval.Eval(node)
	require.NoError(t, err)
	assert.Equal(t, Bool(false), result)
}

func TestNodeType_Methods(t *testing.T) {
	// Ensure all AST node types implement Node interface
	assert.Equal(t, "Literal", (&Literal{}).nodeType())
	assert.Equal(t, "Ident", (&Ident{}).nodeType())
	assert.Equal(t, "DotAccess", (&DotAccess{}).nodeType())
	assert.Equal(t, "IndexAccess", (&IndexAccessNode{}).nodeType())
	assert.Equal(t, "FunctionCall", (&FunctionCall{}).nodeType())
	assert.Equal(t, "UnaryOp", (&UnaryOp{}).nodeType())
	assert.Equal(t, "BinaryOp", (&BinaryOp{}).nodeType())
	assert.Equal(t, "LogicalOp", (&LogicalOp{}).nodeType())
	assert.Equal(t, "Grouping", (&Grouping{}).nodeType())
}

func TestCoerceToString_AllTypes(t *testing.T) {
	assert.Equal(t, "", CoerceToString(Null()))
	assert.Equal(t, "true", CoerceToString(Bool(true)))
	assert.Equal(t, "false", CoerceToString(Bool(false)))
	assert.Equal(t, "42", CoerceToString(Number(42)))
	assert.Equal(t, "hello", CoerceToString(String("hello")))
}

func TestIsTruthy_AllTypes(t *testing.T) {
	assert.False(t, IsTruthy(Null()))
	assert.True(t, IsTruthy(Bool(true)))
	assert.False(t, IsTruthy(Bool(false)))
	assert.True(t, IsTruthy(Number(1)))
	assert.False(t, IsTruthy(Number(0)))
	assert.True(t, IsTruthy(String("x")))
	assert.False(t, IsTruthy(String("")))
	// Arrays and objects are truthy
	assert.True(t, IsTruthy(Array([]Value{String("a")})))
	assert.True(t, IsTruthy(Object(map[string]Value{"k": String("v")})))
}

func TestValue_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		val  Value
		want string
	}{
		{"null", Null(), "null"},
		{"true", Bool(true), "true"},
		{"false", Bool(false), "false"},
		{"number", Number(42), "42"},
		{"string", String("hi"), `"hi"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.val.MarshalJSON()
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(data))
		})
	}
}

func TestLookupFunction_CaseInsensitive(t *testing.T) {
	fns := BuiltinFunctions()
	fn, ok := LookupFunction(fns, "CONTAINS")
	assert.True(t, ok)
	assert.NotNil(t, fn)

	fn2, ok2 := LookupFunction(fns, "Contains")
	assert.True(t, ok2)
	assert.NotNil(t, fn2)

	_, ok3 := LookupFunction(fns, "nonexistent")
	assert.False(t, ok3)

	// nil map
	_, ok4 := LookupFunction(nil, "contains")
	assert.False(t, ok4)
}

func TestSetHashFilesWorkDir_EmptyDir(t *testing.T) {
	fns := BuiltinFunctions()
	SetHashFilesWorkDir(fns, "")
	// hashfiles should exist but return empty for empty dir
	fn, ok := LookupFunction(fns, "hashfiles")
	assert.True(t, ok)
	assert.NotNil(t, fn)
}

func TestGoString_Value(t *testing.T) {
	assert.Equal(t, "Null()", Null().GoString())
	assert.Equal(t, "Bool(true)", Bool(true).GoString())
	assert.Contains(t, Number(3.14).GoString(), "3.14")
	assert.Contains(t, String("test").GoString(), "test")
	assert.Contains(t, Array([]Value{}).GoString(), "0 items")
	assert.Contains(t, Object(map[string]Value{}).GoString(), "0 fields")
}

func TestValue_Equals(t *testing.T) {
	assert.True(t, Null().Equals(Null()))
	assert.True(t, Bool(true).Equals(Bool(true)))
	assert.False(t, Bool(true).Equals(Bool(false)))
	assert.True(t, Number(42).Equals(Number(42)))
	assert.False(t, Number(42).Equals(Number(43)))
	assert.True(t, String("a").Equals(String("a")))
	assert.False(t, String("a").Equals(String("b")))

	arr1 := Array([]Value{String("a"), Number(1)})
	arr2 := Array([]Value{String("a"), Number(1)})
	arr3 := Array([]Value{String("b")})
	assert.True(t, arr1.Equals(arr2))
	assert.False(t, arr1.Equals(arr3))

	obj1 := Object(map[string]Value{"k": String("v")})
	obj2 := Object(map[string]Value{"k": String("v")})
	obj3 := Object(map[string]Value{"k": String("x")})
	assert.True(t, obj1.Equals(obj2))
	assert.False(t, obj1.Equals(obj3))
}

