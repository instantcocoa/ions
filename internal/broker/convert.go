package broker

import (
	"github.com/emaland/ions/internal/expression"
)

// ValueToPipelineContextData converts an expression.Value to the runner's PipelineContextData format.
func ValueToPipelineContextData(v expression.Value) PipelineContextData {
	switch v.Kind() {
	case expression.KindNull:
		s := ""
		return PipelineContextData{Type: 0, StringValue: &s}
	case expression.KindString:
		s := v.StringVal()
		return PipelineContextData{Type: 0, StringValue: &s}
	case expression.KindBool:
		b := v.BoolVal()
		return PipelineContextData{Type: 3, BoolValue: &b}
	case expression.KindNumber:
		n := v.NumberVal()
		return PipelineContextData{Type: 4, NumberValue: &n}
	case expression.KindArray:
		items := v.ArrayItems()
		arr := make([]PipelineContextData, len(items))
		for i, item := range items {
			arr[i] = ValueToPipelineContextData(item)
		}
		return PipelineContextData{Type: 1, ArrayValue: arr}
	case expression.KindObject:
		fields := v.ObjectFields()
		entries := make([]DictEntry, 0, len(fields))
		for k, val := range fields {
			entries = append(entries, DictEntry{
				Key:   k,
				Value: ValueToPipelineContextData(val),
			})
		}
		return PipelineContextData{Type: 2, DictValue: entries}
	default:
		s := ""
		return PipelineContextData{Type: 0, StringValue: &s}
	}
}

// PipelineContextDataToValue converts a PipelineContextData back to an expression.Value.
func PipelineContextDataToValue(d PipelineContextData) expression.Value {
	switch d.Type {
	case 0: // string
		if d.StringValue == nil {
			return expression.Null()
		}
		return expression.String(*d.StringValue)
	case 1: // array
		items := make([]expression.Value, len(d.ArrayValue))
		for i, item := range d.ArrayValue {
			items[i] = PipelineContextDataToValue(item)
		}
		return expression.Array(items)
	case 2: // dictionary
		fields := make(map[string]expression.Value, len(d.DictValue))
		for _, entry := range d.DictValue {
			val := PipelineContextDataToValue(entry.Value)
			fields[entry.Key] = val
		}
		return expression.Object(fields)
	case 3: // boolean
		if d.BoolValue == nil {
			return expression.Bool(false)
		}
		return expression.Bool(*d.BoolValue)
	case 4: // number
		if d.NumberValue == nil {
			return expression.Number(0)
		}
		return expression.Number(*d.NumberValue)
	case 5: // expression
		if d.ExprValue == nil {
			return expression.String("")
		}
		return expression.String(*d.ExprValue)
	default:
		return expression.Null()
	}
}

// MapContextToPipelineContextData converts an expression.MapContext to a map of PipelineContextData.
func MapContextToPipelineContextData(ctx expression.MapContext) map[string]PipelineContextData {
	result := make(map[string]PipelineContextData, len(ctx))
	for k, v := range ctx {
		result[k] = ValueToPipelineContextData(v)
	}
	return result
}
