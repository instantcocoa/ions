package expression

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstructorsAndKind(t *testing.T) {
	tests := []struct {
		name     string
		value    Value
		wantKind Kind
	}{
		{"null", Null(), KindNull},
		{"bool true", Bool(true), KindBool},
		{"bool false", Bool(false), KindBool},
		{"number zero", Number(0), KindNumber},
		{"number positive", Number(42.5), KindNumber},
		{"number negative", Number(-3.14), KindNumber},
		{"string empty", String(""), KindString},
		{"string non-empty", String("hello"), KindString},
		{"array empty", Array([]Value{}), KindArray},
		{"array with items", Array([]Value{Number(1), Number(2)}), KindArray},
		{"object empty", Object(map[string]Value{}), KindObject},
		{"object with fields", Object(map[string]Value{"a": Number(1)}), KindObject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantKind, tt.value.Kind())
		})
	}
}

func TestAccessors(t *testing.T) {
	t.Run("BoolVal", func(t *testing.T) {
		assert.True(t, Bool(true).BoolVal())
		assert.False(t, Bool(false).BoolVal())
		assert.False(t, Null().BoolVal())
	})

	t.Run("NumberVal", func(t *testing.T) {
		assert.Equal(t, 42.0, Number(42).NumberVal())
		assert.Equal(t, 0.0, Null().NumberVal())
	})

	t.Run("StringVal", func(t *testing.T) {
		assert.Equal(t, "hello", String("hello").StringVal())
		assert.Equal(t, "", Null().StringVal())
	})

	t.Run("ArrayItems", func(t *testing.T) {
		items := []Value{Number(1), Number(2)}
		assert.Equal(t, items, Array(items).ArrayItems())
		assert.Nil(t, Null().ArrayItems())
	})

	t.Run("ObjectFields", func(t *testing.T) {
		fields := map[string]Value{"x": Number(1)}
		assert.Equal(t, fields, Object(fields).ObjectFields())
		assert.Nil(t, Null().ObjectFields())
	})
}

func TestValueEquals(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"null == null", Null(), Null(), true},
		{"true == true", Bool(true), Bool(true), true},
		{"true != false", Bool(true), Bool(false), false},
		{"number == number", Number(42), Number(42), true},
		{"number != number", Number(42), Number(43), false},
		{"string == string", String("abc"), String("abc"), true},
		{"string != string", String("abc"), String("def"), false},
		{"empty string == empty string", String(""), String(""), true},
		{"null != bool", Null(), Bool(false), false},
		{"null != number", Null(), Number(0), false},
		{"null != string", Null(), String(""), false},
		{"NaN == NaN", Number(math.NaN()), Number(math.NaN()), true},
		{"array equal", Array([]Value{Number(1)}), Array([]Value{Number(1)}), true},
		{"array different length", Array([]Value{Number(1)}), Array([]Value{Number(1), Number(2)}), false},
		{"array different content", Array([]Value{Number(1)}), Array([]Value{Number(2)}), false},
		{"object equal", Object(map[string]Value{"a": Number(1)}), Object(map[string]Value{"a": Number(1)}), true},
		{"object different", Object(map[string]Value{"a": Number(1)}), Object(map[string]Value{"a": Number(2)}), false},
		{"object missing key", Object(map[string]Value{"a": Number(1)}), Object(map[string]Value{"b": Number(1)}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.a.Equals(tt.b))
		})
	}
}

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		name string
		val  Value
		want bool
	}{
		{"null is falsy", Null(), false},
		{"false is falsy", Bool(false), false},
		{"true is truthy", Bool(true), true},
		{"0 is falsy", Number(0), false},
		{"-0 is falsy", Number(math.Copysign(0, -1)), false},
		{"NaN is falsy", Number(math.NaN()), false},
		{"1 is truthy", Number(1), true},
		{"-1 is truthy", Number(-1), true},
		{"0.5 is truthy", Number(0.5), true},
		{"empty string is falsy", String(""), false},
		{"non-empty string is truthy", String("hello"), true},
		{"string 'false' is truthy", String("false"), true},
		{"string '0' is truthy", String("0"), true},
		{"empty array is truthy", Array([]Value{}), true},
		{"non-empty array is truthy", Array([]Value{Null()}), true},
		{"empty object is truthy", Object(map[string]Value{}), true},
		{"non-empty object is truthy", Object(map[string]Value{"a": Null()}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsTruthy(tt.val))
		})
	}
}

func TestCoerceToNumber(t *testing.T) {
	tests := []struct {
		name string
		val  Value
		want float64
		nan  bool
	}{
		{"null -> 0", Null(), 0, false},
		{"false -> 0", Bool(false), 0, false},
		{"true -> 1", Bool(true), 1, false},
		{"number passthrough", Number(42.5), 42.5, false},
		{"empty string -> 0", String(""), 0, false},
		{"whitespace string -> 0", String("  "), 0, false},
		{"numeric string", String("42"), 42, false},
		{"float string", String("3.14"), 3.14, false},
		{"negative string", String("-10"), -10, false},
		{"scientific notation", String("1e5"), 100000, false},
		{"hex string", String("0xff"), 255, false},
		{"non-numeric string -> NaN", String("abc"), 0, true},
		{"mixed string -> NaN", String("12abc"), 0, true},
		{"array -> NaN", Array([]Value{}), 0, true},
		{"object -> NaN", Object(map[string]Value{}), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CoerceToNumber(tt.val)
			if tt.nan {
				assert.True(t, math.IsNaN(result), "expected NaN")
			} else {
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestCoerceToString(t *testing.T) {
	tests := []struct {
		name string
		val  Value
		want string
	}{
		{"null -> empty", Null(), ""},
		{"true -> 'true'", Bool(true), "true"},
		{"false -> 'false'", Bool(false), "false"},
		{"integer", Number(42), "42"},
		{"negative integer", Number(-10), "-10"},
		{"zero", Number(0), "0"},
		{"float", Number(3.14), "3.14"},
		{"large number", Number(1000000), "1000000"},
		{"NaN", Number(math.NaN()), "NaN"},
		{"Infinity", Number(math.Inf(1)), "Infinity"},
		{"-Infinity", Number(math.Inf(-1)), "-Infinity"},
		{"string passthrough", String("hello"), "hello"},
		{"empty string passthrough", String(""), ""},
		{"array", Array([]Value{Number(1), Number(2)}), "[1,2]"},
		{"empty array", Array([]Value{}), "[]"},
		{"object", Object(map[string]Value{"a": Number(1)}), `{"a":1}`},
		{"empty object", Object(map[string]Value{}), "{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CoerceToString(tt.val))
		})
	}
}

func TestPropertyAccess(t *testing.T) {
	obj := Object(map[string]Value{
		"name":  String("test"),
		"Count": Number(42),
	})

	tests := []struct {
		name     string
		val      Value
		prop     string
		want     Value
	}{
		{"existing key", obj, "name", String("test")},
		{"case-insensitive key", obj, "NAME", String("test")},
		{"case-insensitive mixed", obj, "count", Number(42)},
		{"missing key", obj, "missing", Null()},
		{"non-object", String("hello"), "length", Null()},
		{"null", Null(), "anything", Null()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PropertyAccess(tt.val, tt.prop)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestIndexAccess(t *testing.T) {
	arr := Array([]Value{String("a"), String("b"), String("c")})
	obj := Object(map[string]Value{
		"key": String("value"),
	})

	tests := []struct {
		name  string
		val   Value
		index Value
		want  Value
	}{
		{"array index 0", arr, Number(0), String("a")},
		{"array index 2", arr, Number(2), String("c")},
		{"array out of bounds", arr, Number(5), Null()},
		{"array negative index", arr, Number(-1), Null()},
		{"array float index", arr, Number(1.5), Null()},
		{"array string index", arr, String("0"), Null()},
		{"object string index", obj, String("key"), String("value")},
		{"object case-insensitive", obj, String("KEY"), String("value")},
		{"object missing key", obj, String("missing"), Null()},
		{"null value", Null(), Number(0), Null()},
		{"string value", String("hi"), Number(0), Null()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IndexAccess(tt.val, tt.index)
			assert.True(t, tt.want.Equals(result), "expected %v, got %v", tt.want.GoString(), result.GoString())
		})
	}
}

func TestKindString(t *testing.T) {
	assert.Equal(t, "null", KindNull.String())
	assert.Equal(t, "bool", KindBool.String())
	assert.Equal(t, "number", KindNumber.String())
	assert.Equal(t, "string", KindString.String())
	assert.Equal(t, "array", KindArray.String())
	assert.Equal(t, "object", KindObject.String())
}
