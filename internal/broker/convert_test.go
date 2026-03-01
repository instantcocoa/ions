package broker

import (
	"testing"

	"github.com/emaland/ions/internal/expression"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValueToPipelineContextData_Null(t *testing.T) {
	d := ValueToPipelineContextData(expression.Null())
	assert.Equal(t, 0, d.Type)
	require.NotNil(t, d.StringValue)
	assert.Equal(t, "", *d.StringValue)
}

func TestValueToPipelineContextData_String(t *testing.T) {
	d := ValueToPipelineContextData(expression.String("hello"))
	assert.Equal(t, 0, d.Type)
	require.NotNil(t, d.StringValue)
	assert.Equal(t, "hello", *d.StringValue)
}

func TestValueToPipelineContextData_Bool(t *testing.T) {
	tests := []struct {
		name string
		val  bool
	}{
		{"true", true},
		{"false", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ValueToPipelineContextData(expression.Bool(tt.val))
			assert.Equal(t, 3, d.Type)
			require.NotNil(t, d.BoolValue)
			assert.Equal(t, tt.val, *d.BoolValue)
		})
	}
}

func TestValueToPipelineContextData_Number(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"integer", 42},
		{"float", 3.14},
		{"zero", 0},
		{"negative", -1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ValueToPipelineContextData(expression.Number(tt.val))
			assert.Equal(t, 4, d.Type)
			require.NotNil(t, d.NumberValue)
			assert.Equal(t, tt.val, *d.NumberValue)
		})
	}
}

func TestValueToPipelineContextData_Array(t *testing.T) {
	arr := expression.Array([]expression.Value{
		expression.String("a"),
		expression.Number(1),
		expression.Bool(true),
	})
	d := ValueToPipelineContextData(arr)

	assert.Equal(t, 1, d.Type)
	require.Len(t, d.ArrayValue, 3)
	assert.Equal(t, 0, d.ArrayValue[0].Type)
	assert.Equal(t, "a", *d.ArrayValue[0].StringValue)
	assert.Equal(t, 4, d.ArrayValue[1].Type)
	assert.Equal(t, 1.0, *d.ArrayValue[1].NumberValue)
	assert.Equal(t, 3, d.ArrayValue[2].Type)
	assert.True(t, *d.ArrayValue[2].BoolValue)
}

func TestValueToPipelineContextData_Object(t *testing.T) {
	obj := expression.Object(map[string]expression.Value{
		"name": expression.String("test"),
	})
	d := ValueToPipelineContextData(obj)

	assert.Equal(t, 2, d.Type)
	require.Len(t, d.DictValue, 1)
	assert.Equal(t, "name", d.DictValue[0].Key)
	assert.Equal(t, "test", *d.DictValue[0].Value.StringValue)
}

func TestValueToPipelineContextData_NestedObject(t *testing.T) {
	obj := expression.Object(map[string]expression.Value{
		"inner": expression.Object(map[string]expression.Value{
			"key": expression.String("value"),
		}),
	})
	d := ValueToPipelineContextData(obj)

	assert.Equal(t, 2, d.Type)
	require.Len(t, d.DictValue, 1)

	inner := d.DictValue[0].Value
	assert.Equal(t, 2, inner.Type)
	require.Len(t, inner.DictValue, 1)
	assert.Equal(t, "key", inner.DictValue[0].Key)
	assert.Equal(t, "value", *inner.DictValue[0].Value.StringValue)
}

func TestPipelineContextDataToValue_String(t *testing.T) {
	s := "hello"
	d := PipelineContextData{Type: 0, StringValue: &s}
	v := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindString, v.Kind())
	assert.Equal(t, "hello", v.StringVal())
}

func TestPipelineContextDataToValue_NilString(t *testing.T) {
	d := PipelineContextData{Type: 0}
	v := PipelineContextDataToValue(d)
	assert.Equal(t, expression.KindNull, v.Kind())
}

func TestPipelineContextDataToValue_Bool(t *testing.T) {
	b := true
	d := PipelineContextData{Type: 3, BoolValue: &b}
	v := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindBool, v.Kind())
	assert.True(t, v.BoolVal())
}

func TestPipelineContextDataToValue_Number(t *testing.T) {
	n := 3.14
	d := PipelineContextData{Type: 4, NumberValue: &n}
	v := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindNumber, v.Kind())
	assert.Equal(t, 3.14, v.NumberVal())
}

func TestPipelineContextDataToValue_Array(t *testing.T) {
	s1, s2 := "x", "y"
	d := PipelineContextData{
		Type: 1,
		ArrayValue: []PipelineContextData{
			{Type: 0, StringValue: &s1},
			{Type: 0, StringValue: &s2},
		},
	}
	v := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindArray, v.Kind())
	items := v.ArrayItems()
	require.Len(t, items, 2)
	assert.Equal(t, "x", items[0].StringVal())
	assert.Equal(t, "y", items[1].StringVal())
}

func TestPipelineContextDataToValue_Dict(t *testing.T) {
	val := "myval"
	d := PipelineContextData{
		Type: 2,
		DictValue: []DictEntry{
			{
				Key:   "mykey",
				Value: PipelineContextData{Type: 0, StringValue: &val},
			},
		},
	}
	v := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindObject, v.Kind())
	fields := v.ObjectFields()
	require.Contains(t, fields, "mykey")
	assert.Equal(t, "myval", fields["mykey"].StringVal())
}

func TestPipelineContextDataToValue_Expression(t *testing.T) {
	expr := "github.ref"
	d := PipelineContextData{Type: 5, ExprValue: &expr}
	v := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindString, v.Kind())
	assert.Equal(t, "github.ref", v.StringVal())
}

func TestPipelineContextDataToValue_UnknownType(t *testing.T) {
	d := PipelineContextData{Type: 99}
	v := PipelineContextDataToValue(d)
	assert.Equal(t, expression.KindNull, v.Kind())
}

func TestRoundTrip_String(t *testing.T) {
	original := expression.String("round-trip")
	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)

	assert.True(t, original.Equals(result))
}

func TestRoundTrip_Bool(t *testing.T) {
	for _, b := range []bool{true, false} {
		original := expression.Bool(b)
		d := ValueToPipelineContextData(original)
		result := PipelineContextDataToValue(d)
		assert.True(t, original.Equals(result), "round-trip failed for bool %v", b)
	}
}

func TestRoundTrip_Number(t *testing.T) {
	for _, n := range []float64{0, 1, -1, 3.14, 1e10} {
		original := expression.Number(n)
		d := ValueToPipelineContextData(original)
		result := PipelineContextDataToValue(d)
		assert.True(t, original.Equals(result), "round-trip failed for number %v", n)
	}
}

func TestRoundTrip_Null(t *testing.T) {
	original := expression.Null()
	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)
	// Null maps to empty string in the protocol, so round-trip yields String("")
	assert.Equal(t, expression.KindString, result.Kind())
	assert.Equal(t, "", result.StringVal())
}

func TestRoundTrip_Array(t *testing.T) {
	original := expression.Array([]expression.Value{
		expression.String("a"),
		expression.Number(2),
		expression.Bool(false),
	})
	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)

	assert.True(t, original.Equals(result))
}

func TestRoundTrip_NestedObject(t *testing.T) {
	original := expression.Object(map[string]expression.Value{
		"name": expression.String("test"),
		"nested": expression.Object(map[string]expression.Value{
			"x": expression.Number(1),
			"y": expression.Number(2),
		}),
	})
	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindObject, result.Kind())
	fields := result.ObjectFields()
	assert.Equal(t, "test", fields["name"].StringVal())

	nested := fields["nested"]
	assert.Equal(t, expression.KindObject, nested.Kind())
	assert.Equal(t, 1.0, nested.ObjectFields()["x"].NumberVal())
	assert.Equal(t, 2.0, nested.ObjectFields()["y"].NumberVal())
}

func TestRoundTrip_EmptyArray(t *testing.T) {
	original := expression.Array([]expression.Value{})
	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindArray, result.Kind())
	assert.Len(t, result.ArrayItems(), 0)
}

func TestRoundTrip_EmptyObject(t *testing.T) {
	original := expression.Object(map[string]expression.Value{})
	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindObject, result.Kind())
	assert.Len(t, result.ObjectFields(), 0)
}

func TestMapContextToPipelineContextData(t *testing.T) {
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/main"),
		}),
		"env": expression.Object(map[string]expression.Value{
			"CI": expression.String("true"),
		}),
	}

	result := MapContextToPipelineContextData(ctx)

	require.Contains(t, result, "github")
	require.Contains(t, result, "env")

	// github should be a dict
	assert.Equal(t, 2, result["github"].Type)
	// env should be a dict
	assert.Equal(t, 2, result["env"].Type)
}

func TestRoundTrip_DeeplyNested(t *testing.T) {
	original := expression.Object(map[string]expression.Value{
		"level1": expression.Object(map[string]expression.Value{
			"level2": expression.Object(map[string]expression.Value{
				"level3": expression.Array([]expression.Value{
					expression.String("deep"),
					expression.Number(42),
				}),
			}),
		}),
	})

	d := ValueToPipelineContextData(original)
	result := PipelineContextDataToValue(d)

	assert.Equal(t, expression.KindObject, result.Kind())
	l1 := result.ObjectFields()["level1"]
	assert.Equal(t, expression.KindObject, l1.Kind())
	l2 := l1.ObjectFields()["level2"]
	assert.Equal(t, expression.KindObject, l2.Kind())
	l3 := l2.ObjectFields()["level3"]
	assert.Equal(t, expression.KindArray, l3.Kind())
	require.Len(t, l3.ArrayItems(), 2)
	assert.Equal(t, "deep", l3.ArrayItems()[0].StringVal())
	assert.Equal(t, 42.0, l3.ArrayItems()[1].NumberVal())
}
