package expression

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainsString(t *testing.T) {
	tests := []struct {
		name   string
		search Value
		item   Value
		want   bool
	}{
		{"substring match", String("Hello World"), String("lo Wo"), true},
		{"case-insensitive", String("Hello World"), String("hello"), true},
		{"no match", String("Hello World"), String("xyz"), false},
		{"empty item", String("Hello"), String(""), true},
		{"empty search", String(""), String("x"), false},
		{"number coercion", String("value is 42"), Number(42), true},
		{"bool coercion", String("true story"), Bool(true), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnContains([]Value{tt.search, tt.item})
			require.NoError(t, err)
			assert.Equal(t, Bool(tt.want), result)
		})
	}
}

func TestContainsArray(t *testing.T) {
	arr := Array([]Value{String("apple"), String("Banana"), Number(42)})

	tests := []struct {
		name string
		item Value
		want bool
	}{
		{"exact match", String("apple"), true},
		{"case-insensitive", String("APPLE"), true},
		{"case-insensitive banana", String("banana"), true},
		{"number match", Number(42), true},
		{"number as string", String("42"), true},
		{"no match", String("cherry"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnContains([]Value{arr, tt.item})
			require.NoError(t, err)
			assert.Equal(t, Bool(tt.want), result)
		})
	}
}

func TestContainsArgError(t *testing.T) {
	_, err := fnContains([]Value{String("a")})
	assert.Error(t, err)
}

func TestStartsWith(t *testing.T) {
	tests := []struct {
		name   string
		str    Value
		prefix Value
		want   bool
	}{
		{"match", String("Hello World"), String("Hello"), true},
		{"case-insensitive", String("Hello World"), String("hello"), true},
		{"no match", String("Hello World"), String("World"), false},
		{"empty prefix", String("Hello"), String(""), true},
		{"number coercion", String("42 things"), Number(42), true},
		{"full match", String("abc"), String("abc"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnStartsWith([]Value{tt.str, tt.prefix})
			require.NoError(t, err)
			assert.Equal(t, Bool(tt.want), result)
		})
	}
}

func TestStartsWithArgError(t *testing.T) {
	_, err := fnStartsWith([]Value{String("a")})
	assert.Error(t, err)
}

func TestEndsWith(t *testing.T) {
	tests := []struct {
		name   string
		str    Value
		suffix Value
		want   bool
	}{
		{"match", String("Hello World"), String("World"), true},
		{"case-insensitive", String("Hello World"), String("WORLD"), true},
		{"no match", String("Hello World"), String("Hello"), false},
		{"empty suffix", String("Hello"), String(""), true},
		{"number coercion", String("count is 42"), Number(42), true},
		{"full match", String("abc"), String("abc"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnEndsWith([]Value{tt.str, tt.suffix})
			require.NoError(t, err)
			assert.Equal(t, Bool(tt.want), result)
		})
	}
}

func TestEndsWithArgError(t *testing.T) {
	_, err := fnEndsWith([]Value{String("a")})
	assert.Error(t, err)
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		args []Value
		want string
	}{
		{
			"basic replacement",
			[]Value{String("Hello, {0}!"), String("world")},
			"Hello, world!",
		},
		{
			"multiple replacements",
			[]Value{String("{0} and {1}"), String("foo"), String("bar")},
			"foo and bar",
		},
		{
			"repeated index",
			[]Value{String("{0} {0} {0}"), String("ha")},
			"ha ha ha",
		},
		{
			"escape open brace",
			[]Value{String("{{0}}")},
			"{0}",
		},
		{
			"escape close brace",
			[]Value{String("{0} }}"), String("x")},
			"x }",
		},
		{
			"double escape",
			[]Value{String("{{{{0}}}}")},
			"{{0}}",
		},
		{
			"number arg",
			[]Value{String("count: {0}"), Number(42)},
			"count: 42",
		},
		{
			"bool arg",
			[]Value{String("is {0}"), Bool(true)},
			"is true",
		},
		{
			"no replacements",
			[]Value{String("plain text")},
			"plain text",
		},
		{
			"out of bounds keeps placeholder",
			[]Value{String("{0} {5}"), String("hello")},
			"hello {5}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnFormat(tt.args)
			require.NoError(t, err)
			assert.Equal(t, String(tt.want), result)
		})
	}
}

func TestFormatArgError(t *testing.T) {
	_, err := fnFormat([]Value{})
	assert.Error(t, err)
}

func TestJoin(t *testing.T) {
	tests := []struct {
		name string
		args []Value
		want string
	}{
		{
			"array with default separator",
			[]Value{Array([]Value{String("a"), String("b"), String("c")})},
			"a,b,c",
		},
		{
			"array with custom separator",
			[]Value{Array([]Value{String("a"), String("b"), String("c")}), String(", ")},
			"a, b, c",
		},
		{
			"array with numbers",
			[]Value{Array([]Value{Number(1), Number(2), Number(3)})},
			"1,2,3",
		},
		{
			"empty array",
			[]Value{Array([]Value{})},
			"",
		},
		{
			"single element",
			[]Value{Array([]Value{String("solo")})},
			"solo",
		},
		{
			"string passthrough",
			[]Value{String("already a string")},
			"already a string",
		},
		{
			"mixed types in array",
			[]Value{Array([]Value{String("a"), Number(1), Bool(true), Null()})},
			"a,1,true,",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnJoin(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.want, CoerceToString(result))
		})
	}
}

func TestJoinArgError(t *testing.T) {
	_, err := fnJoin([]Value{})
	assert.Error(t, err)
	_, err = fnJoin([]Value{String("a"), String("b"), String("c")})
	assert.Error(t, err)
}

func TestToJSON(t *testing.T) {
	tests := []struct {
		name string
		val  Value
		want string
	}{
		{"string", String("hello"), `"hello"`},
		{"number", Number(42), "42"},
		{"bool", Bool(true), "true"},
		{"null", Null(), "null"},
		{"array", Array([]Value{Number(1), Number(2)}), "[\n  1,\n  2\n]"},
		{"object", Object(map[string]Value{"a": Number(1)}), "{\n  \"a\": 1\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnToJSON([]Value{tt.val})
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.StringVal())
		})
	}
}

func TestToJSONArgError(t *testing.T) {
	_, err := fnToJSON([]Value{})
	assert.Error(t, err)
}

func TestFromJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{"string", `"hello"`, String("hello")},
		{"number", `42`, Number(42)},
		{"float", `3.14`, Number(3.14)},
		{"bool true", `true`, Bool(true)},
		{"bool false", `false`, Bool(false)},
		{"null", `null`, Null()},
		{"array", `[1,2,3]`, Array([]Value{Number(1), Number(2), Number(3)})},
		{"object", `{"a":1}`, Object(map[string]Value{"a": Number(1)})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnFromJSON([]Value{String(tt.input)})
			require.NoError(t, err)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestFromJSONInvalidJSON(t *testing.T) {
	_, err := fnFromJSON([]Value{String("not json")})
	assert.Error(t, err)
}

func TestFromJSONArgError(t *testing.T) {
	_, err := fnFromJSON([]Value{})
	assert.Error(t, err)
}

func TestToJSONFromJSONRoundtrip(t *testing.T) {
	original := Object(map[string]Value{
		"name":    String("test"),
		"count":   Number(42),
		"enabled": Bool(true),
		"items":   Array([]Value{String("a"), String("b")}),
	})

	jsonResult, err := fnToJSON([]Value{original})
	require.NoError(t, err)

	parsed, err := fnFromJSON([]Value{jsonResult})
	require.NoError(t, err)

	assert.True(t, original.Equals(parsed), "roundtrip failed: expected %v, got %v", original.GoString(), parsed.GoString())
}

func TestHashFiles_Stub(t *testing.T) {
	result, err := fnHashFiles([]Value{String("**/*.go")})
	require.NoError(t, err)
	assert.Equal(t, KindString, result.Kind())
	assert.Len(t, result.StringVal(), 64) // SHA256 hex string

	// Same input should produce same hash
	result2, err := fnHashFiles([]Value{String("**/*.go")})
	require.NoError(t, err)
	assert.Equal(t, result.StringVal(), result2.StringVal())

	// Different input should produce different hash
	result3, err := fnHashFiles([]Value{String("**/*.ts")})
	require.NoError(t, err)
	assert.NotEqual(t, result.StringVal(), result3.StringVal())
}

func TestHashFilesArgError(t *testing.T) {
	_, err := fnHashFiles([]Value{})
	assert.Error(t, err)
}

func TestHashFiles_RealFilesystem(t *testing.T) {
	dir := t.TempDir()

	// Create test files.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "util.go"), []byte("package main\nfunc util() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644))

	fn := makeHashFilesFunc(dir)

	t.Run("matches go files recursively", func(t *testing.T) {
		result, err := fn([]Value{String("**/*.go")})
		require.NoError(t, err)
		assert.Len(t, result.StringVal(), 64)
		assert.NotEmpty(t, result.StringVal())
	})

	t.Run("deterministic output", func(t *testing.T) {
		r1, err := fn([]Value{String("**/*.go")})
		require.NoError(t, err)
		r2, err := fn([]Value{String("**/*.go")})
		require.NoError(t, err)
		assert.Equal(t, r1.StringVal(), r2.StringVal())
	})

	t.Run("different patterns produce different hashes", func(t *testing.T) {
		r1, err := fn([]Value{String("**/*.go")})
		require.NoError(t, err)
		r2, err := fn([]Value{String("**/*.md")})
		require.NoError(t, err)
		assert.NotEqual(t, r1.StringVal(), r2.StringVal())
	})

	t.Run("no match returns empty string", func(t *testing.T) {
		result, err := fn([]Value{String("**/*.xyz")})
		require.NoError(t, err)
		assert.Equal(t, "", result.StringVal())
	})

	t.Run("specific file pattern", func(t *testing.T) {
		result, err := fn([]Value{String("go.mod")})
		require.NoError(t, err)
		assert.Len(t, result.StringVal(), 64)
	})

	t.Run("multiple patterns", func(t *testing.T) {
		result, err := fn([]Value{String("**/*.go"), String("**/*.md")})
		require.NoError(t, err)
		assert.Len(t, result.StringVal(), 64)

		// Should include more files than either pattern alone.
		rGo, _ := fn([]Value{String("**/*.go")})
		rMd, _ := fn([]Value{String("**/*.md")})
		assert.NotEqual(t, result.StringVal(), rGo.StringVal())
		assert.NotEqual(t, result.StringVal(), rMd.StringVal())
	})

	t.Run("content change changes hash", func(t *testing.T) {
		r1, err := fn([]Value{String("go.mod")})
		require.NoError(t, err)

		// Modify file content.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0o644))

		r2, err := fn([]Value{String("go.mod")})
		require.NoError(t, err)
		assert.NotEqual(t, r1.StringVal(), r2.StringVal())
	})

	t.Run("arg error", func(t *testing.T) {
		_, err := fn([]Value{})
		assert.Error(t, err)
	})
}

func TestSetHashFilesWorkDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644))

	fns := BuiltinFunctions()

	// Before: stub returns hash of pattern string.
	stubResult, err := fns["hashfiles"]([]Value{String("test.txt")})
	require.NoError(t, err)
	assert.Len(t, stubResult.StringVal(), 64)

	// After: real implementation hashes file contents.
	SetHashFilesWorkDir(fns, dir)
	realResult, err := fns["hashfiles"]([]Value{String("test.txt")})
	require.NoError(t, err)
	assert.Len(t, realResult.StringVal(), 64)

	// They should differ because one hashes "test.txt" string, the other hashes file contents.
	assert.NotEqual(t, stubResult.StringVal(), realResult.StringVal())
}

func TestMatchDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "src/main.go", true},
		{"**/*.go", "src/pkg/main.go", true},
		{"**/*.go", "main.txt", false},
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", false},
		{"src/**/*.go", "src/main.go", true},
		{"src/**/*.go", "src/pkg/main.go", true},
		{"src/**/*.go", "main.go", false},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/pkg/main.go", false},
		{"**/package-lock.json", "package-lock.json", true},
		{"**/package-lock.json", "frontend/package-lock.json", true},
		{"**/package-lock.json", "a/b/c/package-lock.json", true},
		{"**", "anything", true},
		{"**", "a/b/c", true},
		{"go.mod", "go.mod", true},
		{"go.mod", "src/go.mod", false},
		{"src/[mt]*.go", "src/main.go", true},
		{"src/[mt]*.go", "src/test.go", true},
		{"src/[mt]*.go", "src/util.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"/"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchDoublestar(tt.pattern, tt.name),
				"matchDoublestar(%q, %q)", tt.pattern, tt.name)
		})
	}
}

// ---------------------------------------------------------------------------
// min / max tests
// ---------------------------------------------------------------------------

func TestMin(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want float64
	}{
		{"smaller first", Number(1), Number(5), 1},
		{"smaller second", Number(10), Number(3), 3},
		{"equal", Number(7), Number(7), 7},
		{"negative", Number(-3), Number(2), -3},
		{"zero", Number(0), Number(5), 0},
		{"floats", Number(1.5), Number(2.5), 1.5},
		{"string coercion", String("3"), Number(5), 3},
		{"bool coercion", Bool(true), Number(5), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnMin([]Value{tt.a, tt.b})
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.NumberVal())
		})
	}
}

func TestMinArgError(t *testing.T) {
	_, err := fnMin([]Value{Number(1)})
	assert.Error(t, err)
}

func TestMax(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want float64
	}{
		{"larger first", Number(10), Number(5), 10},
		{"larger second", Number(1), Number(5), 5},
		{"equal", Number(7), Number(7), 7},
		{"negative", Number(-3), Number(2), 2},
		{"zero", Number(0), Number(-5), 0},
		{"floats", Number(1.5), Number(2.5), 2.5},
		{"string coercion", String("10"), Number(5), 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fnMax([]Value{tt.a, tt.b})
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.NumberVal())
		})
	}
}

func TestMaxArgError(t *testing.T) {
	_, err := fnMax([]Value{Number(1)})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// keys / values tests
// ---------------------------------------------------------------------------

func TestKeys(t *testing.T) {
	obj := Object(map[string]Value{
		"c": Number(3),
		"a": Number(1),
		"b": Number(2),
	})
	result, err := fnKeys([]Value{obj})
	require.NoError(t, err)
	assert.Equal(t, KindArray, result.Kind())
	items := result.ArrayItems()
	require.Len(t, items, 3)
	// Keys should be sorted
	assert.Equal(t, "a", items[0].StringVal())
	assert.Equal(t, "b", items[1].StringVal())
	assert.Equal(t, "c", items[2].StringVal())
}

func TestKeysEmptyObject(t *testing.T) {
	result, err := fnKeys([]Value{Object(map[string]Value{})})
	require.NoError(t, err)
	assert.Equal(t, KindArray, result.Kind())
	assert.Empty(t, result.ArrayItems())
}

func TestKeysNonObject(t *testing.T) {
	result, err := fnKeys([]Value{String("not an object")})
	require.NoError(t, err)
	assert.Equal(t, KindArray, result.Kind())
	assert.Empty(t, result.ArrayItems())
}

func TestKeysArgError(t *testing.T) {
	_, err := fnKeys([]Value{})
	assert.Error(t, err)
}

func TestValues(t *testing.T) {
	obj := Object(map[string]Value{
		"c": Number(3),
		"a": Number(1),
		"b": Number(2),
	})
	result, err := fnValues([]Value{obj})
	require.NoError(t, err)
	assert.Equal(t, KindArray, result.Kind())
	items := result.ArrayItems()
	require.Len(t, items, 3)
	// Values should be in key-sorted order (a, b, c)
	assert.Equal(t, float64(1), items[0].NumberVal())
	assert.Equal(t, float64(2), items[1].NumberVal())
	assert.Equal(t, float64(3), items[2].NumberVal())
}

func TestValuesNonObject(t *testing.T) {
	result, err := fnValues([]Value{Number(42)})
	require.NoError(t, err)
	assert.Equal(t, KindArray, result.Kind())
	assert.Empty(t, result.ArrayItems())
}

func TestValuesArgError(t *testing.T) {
	_, err := fnValues([]Value{})
	assert.Error(t, err)
}

func TestLookupFunction(t *testing.T) {
	fns := BuiltinFunctions()

	tests := []struct {
		name  string
		found bool
	}{
		{"contains", true},
		{"Contains", true},
		{"CONTAINS", true},
		{"startsWith", true},
		{"startswith", true},
		{"endsWith", true},
		{"format", true},
		{"join", true},
		{"toJSON", true},
		{"tojson", true},
		{"fromJSON", true},
		{"fromjson", true},
		{"hashFiles", true},
		{"hashfiles", true},
		{"min", true},
		{"max", true},
		{"Min", true},
		{"MAX", true},
		{"keys", true},
		{"values", true},
		{"nonexistent", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, ok := LookupFunction(fns, tt.name)
			assert.Equal(t, tt.found, ok, "lookup %q", tt.name)
			if tt.found {
				assert.NotNil(t, fn)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// EvalExpressionWithStatus tests
// ---------------------------------------------------------------------------

func TestEvalExpressionWithStatus_SuccessWhenAllSucceeded(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithStatus("success()", MapContext{}, fns, "success")
	require.NoError(t, err)
	assert.True(t, IsTruthy(result))
}

func TestEvalExpressionWithStatus_SuccessWhenFailed(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithStatus("success()", MapContext{}, fns, "failure")
	require.NoError(t, err)
	assert.False(t, IsTruthy(result))
}

func TestEvalExpressionWithStatus_FailureWhenFailed(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithStatus("failure()", MapContext{}, fns, "failure")
	require.NoError(t, err)
	assert.True(t, IsTruthy(result))
}

func TestEvalExpressionWithStatus_FailureWhenSucceeded(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithStatus("failure()", MapContext{}, fns, "success")
	require.NoError(t, err)
	assert.False(t, IsTruthy(result))
}

func TestEvalExpressionWithStatus_CancelledWhenCancelled(t *testing.T) {
	fns := BuiltinFunctions()
	result, err := EvalExpressionWithStatus("cancelled()", MapContext{}, fns, "cancelled")
	require.NoError(t, err)
	assert.True(t, IsTruthy(result))
}

func TestEvalExpressionWithStatus_AlwaysRegardlessOfStatus(t *testing.T) {
	fns := BuiltinFunctions()
	for _, status := range []string{"success", "failure", "cancelled"} {
		result, err := EvalExpressionWithStatus("always()", MapContext{}, fns, status)
		require.NoError(t, err)
		assert.True(t, IsTruthy(result), "always() should be true for status %q", status)
	}
}

func TestEvalExpressionWithStatus_NeedsResultWithContext(t *testing.T) {
	fns := BuiltinFunctions()
	ctx := MapContext{
		"needs": Object(map[string]Value{
			"build": Object(map[string]Value{
				"result": String("success"),
			}),
		}),
	}
	result, err := EvalExpressionWithStatus("needs.build.result == 'success'", ctx, fns, "success")
	require.NoError(t, err)
	assert.True(t, IsTruthy(result))

	result2, err := EvalExpressionWithStatus("needs.build.result == 'failure'", ctx, fns, "success")
	require.NoError(t, err)
	assert.False(t, IsTruthy(result2))
}
