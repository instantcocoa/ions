package expression

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// IsTruthy determines the truthiness of a Value following GitHub Actions rules.
// Falsy: false, 0, -0, "", null
// Truthy: everything else (including empty array, empty object)
func IsTruthy(v Value) bool {
	switch v.kind {
	case KindNull:
		return false
	case KindBool:
		return v.boolVal
	case KindNumber:
		return v.numberVal != 0 && !math.IsNaN(v.numberVal)
	case KindString:
		return v.stringVal != ""
	case KindArray:
		return true
	case KindObject:
		return true
	}
	return false
}

// CoerceToNumber converts a Value to a float64 following GitHub Actions coercion rules.
// null -> 0, false -> 0, true -> 1, "" -> 0, string -> parse float (NaN if non-numeric),
// array/object -> NaN
func CoerceToNumber(v Value) float64 {
	switch v.kind {
	case KindNull:
		return 0
	case KindBool:
		if v.boolVal {
			return 1
		}
		return 0
	case KindNumber:
		return v.numberVal
	case KindString:
		s := strings.TrimSpace(v.stringVal)
		if s == "" {
			return 0
		}
		// Try hex
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			n, err := strconv.ParseInt(s, 0, 64)
			if err == nil {
				return float64(n)
			}
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return n
	case KindArray, KindObject:
		return math.NaN()
	}
	return math.NaN()
}

// CoerceToString converts a Value to a string following GitHub Actions coercion rules.
// null -> "", true -> "true", false -> "false", number -> formatted (no trailing zeros),
// string -> identity, array/object -> JSON
func CoerceToString(v Value) string {
	switch v.kind {
	case KindNull:
		return ""
	case KindBool:
		if v.boolVal {
			return "true"
		}
		return "false"
	case KindNumber:
		return formatNumber(v.numberVal)
	case KindString:
		return v.stringVal
	case KindArray, KindObject:
		b, err := json.Marshal(v.toGoValue())
		if err != nil {
			return ""
		}
		return string(b)
	}
	return ""
}

// formatNumber formats a number without unnecessary trailing zeros.
// Integers are formatted without a decimal point.
func formatNumber(n float64) string {
	if math.IsNaN(n) {
		return "NaN"
	}
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	// Check if it's an integer
	if n == math.Trunc(n) && !math.IsInf(n, 0) && math.Abs(n) < 1e15 {
		return fmt.Sprintf("%d", int64(n))
	}
	// Format with enough precision and strip trailing zeros
	s := strconv.FormatFloat(n, 'f', -1, 64)
	return s
}

// PropertyAccess accesses a property on a Value by name.
// For objects, returns the field value (case-insensitive key lookup).
// For non-objects, returns Null.
func PropertyAccess(v Value, name string) Value {
	if v.kind != KindObject {
		return Null()
	}
	// Case-insensitive lookup
	for k, val := range v.objectFields {
		if strings.EqualFold(k, name) {
			return val
		}
	}
	return Null()
}

// IndexAccess accesses an element by index on a Value.
// For arrays: if index is a number, use as array index.
// For objects: if index is a string, use as key (case-insensitive).
// Returns Null for missing or out-of-bounds access.
func IndexAccess(v Value, index Value) Value {
	switch v.kind {
	case KindArray:
		if index.kind == KindNumber {
			idx := int(index.numberVal)
			if float64(idx) != index.numberVal {
				return Null()
			}
			if idx < 0 || idx >= len(v.arrayItems) {
				return Null()
			}
			return v.arrayItems[idx]
		}
		return Null()
	case KindObject:
		key := CoerceToString(index)
		return PropertyAccess(v, key)
	default:
		return Null()
	}
}
