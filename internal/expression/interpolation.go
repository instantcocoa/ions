package expression

import (
	"fmt"
	"strings"
)

// InterpolationPart represents either a literal string or an expression.
type InterpolationPart struct {
	Literal    string
	Expression string // raw expression text (without ${{ }})
	IsExpr     bool
}

// ParseInterpolation parses a string containing ${{ }} expressions into parts.
func ParseInterpolation(s string) ([]InterpolationPart, error) {
	var parts []InterpolationPart
	remaining := s
	pos := 0

	for len(remaining) > 0 {
		// Find next ${{
		idx := strings.Index(remaining, "${{")
		if idx == -1 {
			// No more expressions; rest is literal
			if len(remaining) > 0 {
				parts = append(parts, InterpolationPart{Literal: remaining})
			}
			break
		}

		// Everything before ${{ is literal
		if idx > 0 {
			parts = append(parts, InterpolationPart{Literal: remaining[:idx]})
		}

		// Find matching }}
		exprStart := idx + 3 // skip "${{""
		endIdx := findClosingBraces(remaining[exprStart:])
		if endIdx == -1 {
			return nil, fmt.Errorf("unclosed expression starting at position %d", pos+idx)
		}

		expr := remaining[exprStart : exprStart+endIdx]
		expr = strings.TrimSpace(expr)

		parts = append(parts, InterpolationPart{
			Expression: expr,
			IsExpr:     true,
		})

		// Move past the }}
		advance := exprStart + endIdx + 2
		remaining = remaining[advance:]
		pos += advance
	}

	return parts, nil
}

// findClosingBraces finds the index of the first }} in the string.
// Returns -1 if not found.
func findClosingBraces(s string) int {
	// We need to handle string literals inside expressions that might contain }}
	inString := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			if ch == '\'' {
				// Check for escaped quote ''
				if i+1 < len(s) && s[i+1] == '\'' {
					i++ // skip escaped quote
				} else {
					inString = false
				}
			}
			continue
		}
		if ch == '\'' {
			inString = true
			continue
		}
		if ch == '}' && i+1 < len(s) && s[i+1] == '}' {
			return i
		}
	}
	return -1
}

// EvalInterpolation evaluates a string with ${{ }} expressions.
// Each expression is evaluated with the given context, coerced to string,
// and concatenated with the literal parts.
func EvalInterpolation(s string, ctx Context) (string, error) {
	parts, err := ParseInterpolation(s)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, part := range parts {
		if !part.IsExpr {
			sb.WriteString(part.Literal)
			continue
		}

		node, err := Parse(part.Expression)
		if err != nil {
			return "", fmt.Errorf("error parsing expression %q: %w", part.Expression, err)
		}

		eval := NewEvaluator(ctx)
		result, err := eval.Eval(node)
		if err != nil {
			return "", fmt.Errorf("error evaluating expression %q: %w", part.Expression, err)
		}

		sb.WriteString(CoerceToString(result))
	}

	return sb.String(), nil
}
