package expression

import (
	"encoding/json"
	"fmt"
	"math"
)

// Kind represents the type of a Value.
type Kind int

const (
	KindNull   Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// String returns the string representation of a Kind.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	default:
		return "unknown"
	}
}

// Value is the fundamental data type in the expression evaluator.
// It uses a discriminated union pattern based on Kind.
type Value struct {
	kind         Kind
	boolVal      bool
	numberVal    float64
	stringVal    string
	arrayItems   []Value
	objectFields map[string]Value
}

// Null creates a null Value.
func Null() Value {
	return Value{kind: KindNull}
}

// Bool creates a boolean Value.
func Bool(b bool) Value {
	return Value{kind: KindBool, boolVal: b}
}

// Number creates a numeric Value.
func Number(n float64) Value {
	return Value{kind: KindNumber, numberVal: n}
}

// String creates a string Value.
func String(s string) Value {
	return Value{kind: KindString, stringVal: s}
}

// Array creates an array Value.
func Array(items []Value) Value {
	return Value{kind: KindArray, arrayItems: items}
}

// Object creates an object Value.
func Object(fields map[string]Value) Value {
	return Value{kind: KindObject, objectFields: fields}
}

// Kind returns the Kind of this Value.
func (v Value) Kind() Kind {
	return v.kind
}

// BoolVal returns the boolean value. Returns false if not a bool.
func (v Value) BoolVal() bool {
	return v.boolVal
}

// NumberVal returns the numeric value. Returns 0 if not a number.
func (v Value) NumberVal() float64 {
	return v.numberVal
}

// StringVal returns the string value. Returns "" if not a string.
func (v Value) StringVal() string {
	return v.stringVal
}

// ArrayItems returns the array items. Returns nil if not an array.
func (v Value) ArrayItems() []Value {
	return v.arrayItems
}

// ObjectFields returns the object fields. Returns nil if not an object.
func (v Value) ObjectFields() map[string]Value {
	return v.objectFields
}

// Equals checks if two Values are equal.
func (v Value) Equals(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindNull:
		return true
	case KindBool:
		return v.boolVal == other.boolVal
	case KindNumber:
		if math.IsNaN(v.numberVal) && math.IsNaN(other.numberVal) {
			return true
		}
		return v.numberVal == other.numberVal
	case KindString:
		return v.stringVal == other.stringVal
	case KindArray:
		if len(v.arrayItems) != len(other.arrayItems) {
			return false
		}
		for i, item := range v.arrayItems {
			if !item.Equals(other.arrayItems[i]) {
				return false
			}
		}
		return true
	case KindObject:
		if len(v.objectFields) != len(other.objectFields) {
			return false
		}
		for k, val := range v.objectFields {
			otherVal, ok := other.objectFields[k]
			if !ok {
				return false
			}
			if !val.Equals(otherVal) {
				return false
			}
		}
		return true
	}
	return false
}

// GoString returns a Go-syntax representation of the Value for debugging.
func (v Value) GoString() string {
	switch v.kind {
	case KindNull:
		return "Null()"
	case KindBool:
		return fmt.Sprintf("Bool(%v)", v.boolVal)
	case KindNumber:
		return fmt.Sprintf("Number(%v)", v.numberVal)
	case KindString:
		return fmt.Sprintf("String(%q)", v.stringVal)
	case KindArray:
		return fmt.Sprintf("Array(%v items)", len(v.arrayItems))
	case KindObject:
		return fmt.Sprintf("Object(%v fields)", len(v.objectFields))
	}
	return "Value{?}"
}

// toGoValue converts a Value to a Go native type for JSON marshaling.
func (v Value) toGoValue() interface{} {
	switch v.kind {
	case KindNull:
		return nil
	case KindBool:
		return v.boolVal
	case KindNumber:
		if math.IsNaN(v.numberVal) || math.IsInf(v.numberVal, 0) {
			return nil
		}
		return v.numberVal
	case KindString:
		return v.stringVal
	case KindArray:
		items := make([]interface{}, len(v.arrayItems))
		for i, item := range v.arrayItems {
			items[i] = item.toGoValue()
		}
		return items
	case KindObject:
		fields := make(map[string]interface{}, len(v.objectFields))
		for k, val := range v.objectFields {
			fields[k] = val.toGoValue()
		}
		return fields
	}
	return nil
}

// MarshalJSON implements json.Marshaler for Value.
func (v Value) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.toGoValue())
}
