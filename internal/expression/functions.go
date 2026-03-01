package expression

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Function is a built-in function that takes arguments and returns a value.
type Function func(args []Value) (Value, error)

// BuiltinFunctions returns the map of all built-in functions.
// Function lookup is case-insensitive; all keys are lowercase.
func BuiltinFunctions() map[string]Function {
	return map[string]Function{
		"contains":   fnContains,
		"startswith": fnStartsWith,
		"endswith":   fnEndsWith,
		"format":     fnFormat,
		"join":       fnJoin,
		"tojson":     fnToJSON,
		"fromjson":   fnFromJSON,
		"hashfiles":  fnHashFiles,
	}
}

// fnContains implements contains(search, item).
// If search is a string: case-insensitive substring match.
// If search is an array: check if any element equals item (coerced to string, case-insensitive).
func fnContains(args []Value) (Value, error) {
	if len(args) != 2 {
		return Null(), fmt.Errorf("contains() requires exactly 2 arguments, got %d", len(args))
	}
	search := args[0]
	item := args[1]

	if search.Kind() == KindArray {
		itemStr := strings.ToLower(CoerceToString(item))
		for _, elem := range search.ArrayItems() {
			elemStr := strings.ToLower(CoerceToString(elem))
			if elemStr == itemStr {
				return Bool(true), nil
			}
		}
		return Bool(false), nil
	}

	// String search: case-insensitive substring
	searchStr := strings.ToLower(CoerceToString(search))
	itemStr := strings.ToLower(CoerceToString(item))
	return Bool(strings.Contains(searchStr, itemStr)), nil
}

// fnStartsWith implements startsWith(str, prefix).
// Case-insensitive. Both coerced to string.
func fnStartsWith(args []Value) (Value, error) {
	if len(args) != 2 {
		return Null(), fmt.Errorf("startsWith() requires exactly 2 arguments, got %d", len(args))
	}
	str := strings.ToLower(CoerceToString(args[0]))
	prefix := strings.ToLower(CoerceToString(args[1]))
	return Bool(strings.HasPrefix(str, prefix)), nil
}

// fnEndsWith implements endsWith(str, suffix).
// Case-insensitive. Both coerced to string.
func fnEndsWith(args []Value) (Value, error) {
	if len(args) != 2 {
		return Null(), fmt.Errorf("endsWith() requires exactly 2 arguments, got %d", len(args))
	}
	str := strings.ToLower(CoerceToString(args[0]))
	suffix := strings.ToLower(CoerceToString(args[1]))
	return Bool(strings.HasSuffix(str, suffix)), nil
}

// fnFormat implements format(template, args...).
// Replace {0}, {1}, etc. with args. {{ escapes to {, }} escapes to }.
func fnFormat(args []Value) (Value, error) {
	if len(args) < 1 {
		return Null(), fmt.Errorf("format() requires at least 1 argument, got %d", len(args))
	}
	template := CoerceToString(args[0])
	replacements := args[1:]

	var sb strings.Builder
	runes := []rune(template)
	i := 0

	for i < len(runes) {
		ch := runes[i]
		if ch == '{' {
			if i+1 < len(runes) && runes[i+1] == '{' {
				// Escaped {{ -> {
				sb.WriteRune('{')
				i += 2
				continue
			}
			// Look for {N}
			j := i + 1
			for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
				j++
			}
			if j < len(runes) && runes[j] == '}' && j > i+1 {
				// Parse the index
				numStr := string(runes[i+1 : j])
				idx := 0
				for _, d := range numStr {
					idx = idx*10 + int(d-'0')
				}
				if idx < len(replacements) {
					sb.WriteString(CoerceToString(replacements[idx]))
				} else {
					// Out of bounds — keep the placeholder
					sb.WriteString(string(runes[i : j+1]))
				}
				i = j + 1
				continue
			}
			sb.WriteRune(ch)
			i++
		} else if ch == '}' {
			if i+1 < len(runes) && runes[i+1] == '}' {
				// Escaped }} -> }
				sb.WriteRune('}')
				i += 2
				continue
			}
			sb.WriteRune(ch)
			i++
		} else {
			sb.WriteRune(ch)
			i++
		}
	}

	return String(sb.String()), nil
}

// fnJoin implements join(array, separator?).
// Join array elements (coerced to string) with separator (default ",").
// If first arg is a string, return it as-is.
func fnJoin(args []Value) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Null(), fmt.Errorf("join() requires 1 or 2 arguments, got %d", len(args))
	}

	sep := ","
	if len(args) == 2 {
		sep = CoerceToString(args[1])
	}

	first := args[0]
	if first.Kind() == KindString {
		return first, nil
	}

	if first.Kind() != KindArray {
		// Coerce to string
		return String(CoerceToString(first)), nil
	}

	parts := make([]string, len(first.ArrayItems()))
	for i, item := range first.ArrayItems() {
		parts[i] = CoerceToString(item)
	}
	return String(strings.Join(parts, sep)), nil
}

// fnToJSON implements toJSON(value).
// Returns pretty-printed JSON string of the value.
func fnToJSON(args []Value) (Value, error) {
	if len(args) != 1 {
		return Null(), fmt.Errorf("toJSON() requires exactly 1 argument, got %d", len(args))
	}
	goVal := args[0].toGoValue()
	b, err := json.MarshalIndent(goVal, "", "  ")
	if err != nil {
		return Null(), fmt.Errorf("toJSON() failed: %w", err)
	}
	return String(string(b)), nil
}

// fnFromJSON implements fromJSON(str).
// Parse JSON string into Value.
func fnFromJSON(args []Value) (Value, error) {
	if len(args) != 1 {
		return Null(), fmt.Errorf("fromJSON() requires exactly 1 argument, got %d", len(args))
	}
	s := CoerceToString(args[0])
	return parseJSONToValue(s)
}

// parseJSONToValue parses a JSON string into a Value.
func parseJSONToValue(s string) (Value, error) {
	var raw interface{}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return Null(), fmt.Errorf("fromJSON() failed to parse: %w", err)
	}
	return goToValue(raw), nil
}

// goToValue converts a Go interface{} (from JSON) to a Value.
func goToValue(v interface{}) Value {
	if v == nil {
		return Null()
	}
	switch val := v.(type) {
	case bool:
		return Bool(val)
	case float64:
		return Number(val)
	case string:
		return String(val)
	case []interface{}:
		items := make([]Value, len(val))
		for i, item := range val {
			items[i] = goToValue(item)
		}
		return Array(items)
	case map[string]interface{}:
		fields := make(map[string]Value, len(val))
		for k, item := range val {
			fields[k] = goToValue(item)
		}
		return Object(fields)
	default:
		return Null()
	}
}

// fnHashFiles implements hashFiles(patterns...).
// Stub implementation that returns a deterministic placeholder hash.
// Real implementation needs filesystem access that will be wired up later.
func fnHashFiles(args []Value) (Value, error) {
	if len(args) < 1 {
		return Null(), fmt.Errorf("hashFiles() requires at least 1 argument, got %d", len(args))
	}
	// Create a deterministic hash from the patterns so tests are stable
	h := sha256.New()
	for _, arg := range args {
		s := CoerceToString(arg)
		h.Write([]byte(s))
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))
	return String(hash), nil
}

// LookupFunction looks up a function by name (case-insensitive).
func LookupFunction(functions map[string]Function, name string) (Function, bool) {
	fn, ok := functions[strings.ToLower(name)]
	if !ok {
		// Check if it's NaN (special number)
		if strings.EqualFold(name, "NaN") {
			return func(args []Value) (Value, error) {
				return Number(math.NaN()), nil
			}, true
		}
	}
	return fn, ok
}
