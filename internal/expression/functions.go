package expression

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
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
		"min":        fnMin,
		"max":        fnMax,
		"keys":       fnKeys,
		"values":     fnValues,
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

// fnHashFiles implements hashFiles(patterns...) as a stub when no workDir is set.
// It hashes the pattern strings to produce deterministic output for testing.
func fnHashFiles(args []Value) (Value, error) {
	if len(args) < 1 {
		return Null(), fmt.Errorf("hashFiles() requires at least 1 argument, got %d", len(args))
	}
	h := sha256.New()
	for _, arg := range args {
		s := CoerceToString(arg)
		h.Write([]byte(s))
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))
	return String(hash), nil
}

// SetHashFilesWorkDir replaces the stub hashFiles in fns with a real
// implementation that globs files relative to workDir and hashes their contents.
func SetHashFilesWorkDir(fns map[string]Function, workDir string) {
	if workDir == "" {
		return
	}
	fns["hashfiles"] = makeHashFilesFunc(workDir)
}

// makeHashFilesFunc creates a hashFiles function that resolves glob patterns
// against the given working directory and hashes matched file contents.
func makeHashFilesFunc(workDir string) Function {
	return func(args []Value) (Value, error) {
		if len(args) < 1 {
			return Null(), fmt.Errorf("hashFiles() requires at least 1 argument, got %d", len(args))
		}

		// Collect all matching files across all patterns.
		seen := make(map[string]bool)
		var allFiles []string

		for _, arg := range args {
			pattern := CoerceToString(arg)
			matches, err := globFiles(workDir, pattern)
			if err != nil {
				return Null(), fmt.Errorf("hashFiles() glob error: %w", err)
			}
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					allFiles = append(allFiles, m)
				}
			}
		}

		if len(allFiles) == 0 {
			return String(""), nil
		}

		// Sort for deterministic output.
		sort.Strings(allFiles)

		// Hash all file contents: compute per-file SHA-256 then hash
		// the concatenation of all file hashes.
		h := sha256.New()
		for _, relPath := range allFiles {
			data, err := os.ReadFile(filepath.Join(workDir, relPath))
			if err != nil {
				return Null(), fmt.Errorf("hashFiles() read error for %s: %w", relPath, err)
			}
			fileHash := sha256.Sum256(data)
			h.Write(fileHash[:])
		}

		return String(fmt.Sprintf("%x", h.Sum(nil))), nil
	}
}

// globFiles walks workDir and returns relative paths of files matching pattern.
// Supports ** for recursive directory matching.
func globFiles(root, pattern string) ([]string, error) {
	// Normalize pattern separators.
	pattern = filepath.ToSlash(pattern)

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if d.IsDir() {
			return nil // only match files
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchDoublestar(pattern, rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	return matches, err
}

// matchDoublestar matches a path against a pattern that may contain **
// for recursive directory matching, plus standard glob characters (*, ?, [...]).
func matchDoublestar(pattern, name string) bool {
	patParts := strings.Split(pattern, "/")
	nameParts := strings.Split(name, "/")
	return matchParts(patParts, nameParts)
}

// matchParts recursively matches pattern segments against path segments.
func matchParts(pattern, path []string) bool {
	for len(pattern) > 0 {
		seg := pattern[0]

		if seg == "**" {
			// ** matches zero or more path segments.
			rest := pattern[1:]

			// Skip consecutive ** segments.
			for len(rest) > 0 && rest[0] == "**" {
				rest = rest[1:]
			}

			// Try matching rest against every suffix of path.
			for i := 0; i <= len(path); i++ {
				if matchParts(rest, path[i:]) {
					return true
				}
			}
			return false
		}

		if len(path) == 0 {
			return false
		}

		// Match single segment using filepath.Match.
		matched, err := filepath.Match(seg, path[0])
		if err != nil || !matched {
			return false
		}

		pattern = pattern[1:]
		path = path[1:]
	}

	return len(path) == 0
}

// fnMin implements min(a, b) — returns the smaller of two numeric values.
func fnMin(args []Value) (Value, error) {
	if len(args) != 2 {
		return Null(), fmt.Errorf("min() requires exactly 2 arguments, got %d", len(args))
	}
	a := CoerceToNumber(args[0])
	b := CoerceToNumber(args[1])
	if math.IsNaN(a) || math.IsNaN(b) {
		return Number(math.NaN()), nil
	}
	if a <= b {
		return Number(a), nil
	}
	return Number(b), nil
}

// fnMax implements max(a, b) — returns the larger of two numeric values.
func fnMax(args []Value) (Value, error) {
	if len(args) != 2 {
		return Null(), fmt.Errorf("max() requires exactly 2 arguments, got %d", len(args))
	}
	a := CoerceToNumber(args[0])
	b := CoerceToNumber(args[1])
	if math.IsNaN(a) || math.IsNaN(b) {
		return Number(math.NaN()), nil
	}
	if a >= b {
		return Number(a), nil
	}
	return Number(b), nil
}

// fnKeys implements keys(obj) — returns a sorted array of object keys.
func fnKeys(args []Value) (Value, error) {
	if len(args) != 1 {
		return Null(), fmt.Errorf("keys() requires exactly 1 argument, got %d", len(args))
	}
	obj := args[0]
	if obj.Kind() != KindObject {
		return Array(nil), nil
	}
	fields := obj.ObjectFields()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := make([]Value, len(keys))
	for i, k := range keys {
		items[i] = String(k)
	}
	return Array(items), nil
}

// fnValues implements values(obj) — returns an array of object values in key-sorted order.
func fnValues(args []Value) (Value, error) {
	if len(args) != 1 {
		return Null(), fmt.Errorf("values() requires exactly 1 argument, got %d", len(args))
	}
	obj := args[0]
	if obj.Kind() != KindObject {
		return Array(nil), nil
	}
	fields := obj.ObjectFields()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := make([]Value, len(keys))
	for i, k := range keys {
		items[i] = fields[k]
	}
	return Array(items), nil
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
