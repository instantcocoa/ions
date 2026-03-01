package expression

import (
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

func TestHashFiles(t *testing.T) {
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
