package expression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLexSimpleExpression(t *testing.T) {
	tokens, err := Lex("github.ref")
	require.NoError(t, err)

	require.Len(t, tokens, 4) // ident, dot, ident, eof
	assert.Equal(t, TokenIdent, tokens[0].Type)
	assert.Equal(t, "github", tokens[0].Value)
	assert.Equal(t, TokenDot, tokens[1].Type)
	assert.Equal(t, TokenIdent, tokens[2].Type)
	assert.Equal(t, "ref", tokens[2].Value)
	assert.Equal(t, TokenEOF, tokens[3].Type)
}

func TestLexStringLiteral(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "'hello'", "hello"},
		{"empty", "''", ""},
		{"with spaces", "'hello world'", "hello world"},
		{"escaped quote", "'it''s'", "it's"},
		{"double escaped", "'a''''b'", "a''b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(tokens), 2)
			assert.Equal(t, TokenString, tokens[0].Type)
			assert.Equal(t, tt.want, tokens[0].Value)
		})
	}
}

func TestLexUnterminatedString(t *testing.T) {
	_, err := Lex("'hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated string")
}

func TestLexNumbers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"integer", "42", "42"},
		{"zero", "0", "0"},
		{"float", "3.14", "3.14"},
		{"hex lowercase", "0xff", "0xff"},
		{"hex uppercase", "0XFF", "0XFF"},
		{"scientific", "1e10", "1e10"},
		{"scientific with plus", "1e+10", "1e+10"},
		{"scientific with minus", "1e-5", "1e-5"},
		{"decimal scientific", "2.5e3", "2.5e3"},
		{"negative", "-42", "-42"},
		{"negative float", "-3.14", "-3.14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(tokens), 2)
			assert.Equal(t, TokenNumber, tokens[0].Type)
			assert.Equal(t, tt.want, tokens[0].Value)
		})
	}
}

func TestLexAllOperators(t *testing.T) {
	tests := []struct {
		input string
		want  TokenType
	}{
		{"==", TokenEqEq},
		{"!=", TokenBangEq},
		{"<=", TokenLessEq},
		{">=", TokenGreaterEq},
		{"&&", TokenAnd},
		{"||", TokenOr},
		{"<", TokenLess},
		{">", TokenGreater},
		{"!", TokenBang},
		{".", TokenDot},
		{"[", TokenLBracket},
		{"]", TokenRBracket},
		{"(", TokenLParen},
		{")", TokenRParen},
		{",", TokenComma},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(tokens), 2)
			assert.Equal(t, tt.want, tokens[0].Type)
		})
	}
}

func TestLexKeywords(t *testing.T) {
	tests := []struct {
		input string
		want  TokenType
	}{
		{"null", TokenNull},
		{"true", TokenTrue},
		{"false", TokenFalse},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(tokens), 2)
			assert.Equal(t, tt.want, tokens[0].Type)
		})
	}
}

func TestLexKeywordsAreCaseSensitive(t *testing.T) {
	// "Null", "True", "False" should be identifiers, not keywords
	tests := []string{"Null", "True", "False", "NULL", "TRUE", "FALSE"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			tokens, err := Lex(input)
			require.NoError(t, err)
			assert.Equal(t, TokenIdent, tokens[0].Type)
			assert.Equal(t, input, tokens[0].Value)
		})
	}
}

func TestLexIdentifiers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github", "github"},
		{"_private", "_private"},
		{"step_1", "step_1"},
		{"my-action", "my-action"},
		{"setup-node", "setup-node"},
		{"abc123", "abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			require.NoError(t, err)
			assert.Equal(t, TokenIdent, tokens[0].Type)
			assert.Equal(t, tt.want, tokens[0].Value)
		})
	}
}

func TestLexComplexExpression(t *testing.T) {
	input := "github.event_name == 'push' && contains(github.ref, 'main')"
	tokens, err := Lex(input)
	require.NoError(t, err)

	expected := []struct {
		typ TokenType
		val string
	}{
		{TokenIdent, "github"},
		{TokenDot, "."},
		{TokenIdent, "event_name"},
		{TokenEqEq, "=="},
		{TokenString, "push"},
		{TokenAnd, "&&"},
		{TokenIdent, "contains"},
		{TokenLParen, "("},
		{TokenIdent, "github"},
		{TokenDot, "."},
		{TokenIdent, "ref"},
		{TokenComma, ","},
		{TokenString, "main"},
		{TokenRParen, ")"},
		{TokenEOF, ""},
	}

	require.Len(t, tokens, len(expected))
	for i, exp := range expected {
		assert.Equal(t, exp.typ, tokens[i].Type, "token %d type", i)
		assert.Equal(t, exp.val, tokens[i].Value, "token %d value", i)
	}
}

func TestLexWhitespace(t *testing.T) {
	tokens, err := Lex("  true  ==   false  ")
	require.NoError(t, err)
	require.Len(t, tokens, 4) // true, ==, false, EOF
	assert.Equal(t, TokenTrue, tokens[0].Type)
	assert.Equal(t, TokenEqEq, tokens[1].Type)
	assert.Equal(t, TokenFalse, tokens[2].Type)
}

func TestLexEmptyInput(t *testing.T) {
	tokens, err := Lex("")
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, TokenEOF, tokens[0].Type)
}

func TestLexPositionTracking(t *testing.T) {
	tokens, err := Lex("a == b")
	require.NoError(t, err)
	assert.Equal(t, 0, tokens[0].Pos) // a
	assert.Equal(t, 2, tokens[1].Pos) // ==
	assert.Equal(t, 5, tokens[2].Pos) // b
}

func TestLexErrorUnexpectedChar(t *testing.T) {
	_, err := Lex("@")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected character")
	assert.Contains(t, err.Error(), "position 0")
}

func TestLexBangNotFollowedByEquals(t *testing.T) {
	tokens, err := Lex("!true")
	require.NoError(t, err)
	require.Len(t, tokens, 3)
	assert.Equal(t, TokenBang, tokens[0].Type)
	assert.Equal(t, TokenTrue, tokens[1].Type)
}

func TestLexBracketExpression(t *testing.T) {
	tokens, err := Lex("matrix['os']")
	require.NoError(t, err)
	require.Len(t, tokens, 5) // matrix [ 'os' ] EOF
	assert.Equal(t, TokenIdent, tokens[0].Type)
	assert.Equal(t, "matrix", tokens[0].Value)
	assert.Equal(t, TokenLBracket, tokens[1].Type)
	assert.Equal(t, TokenString, tokens[2].Type)
	assert.Equal(t, "os", tokens[2].Value)
	assert.Equal(t, TokenRBracket, tokens[3].Type)
}

func TestLexMultipleOperators(t *testing.T) {
	tokens, err := Lex("a < b && c >= d || e != f")
	require.NoError(t, err)

	types := make([]TokenType, 0)
	for _, tok := range tokens {
		types = append(types, tok.Type)
	}
	expected := []TokenType{
		TokenIdent, TokenLess, TokenIdent, TokenAnd,
		TokenIdent, TokenGreaterEq, TokenIdent, TokenOr,
		TokenIdent, TokenBangEq, TokenIdent, TokenEOF,
	}
	assert.Equal(t, expected, types)
}

func TestLexNegativeNumberVsSubtraction(t *testing.T) {
	// Negative number at start
	tokens, err := Lex("-5")
	require.NoError(t, err)
	assert.Equal(t, TokenNumber, tokens[0].Type)
	assert.Equal(t, "-5", tokens[0].Value)
}

func TestTokenTypeString(t *testing.T) {
	assert.Equal(t, "EOF", TokenEOF.String())
	assert.Equal(t, "null", TokenNull.String())
	assert.Equal(t, "true", TokenTrue.String())
	assert.Equal(t, "false", TokenFalse.String())
	assert.Equal(t, "number", TokenNumber.String())
	assert.Equal(t, "string", TokenString.String())
	assert.Equal(t, "ident", TokenIdent.String())
	assert.Equal(t, ".", TokenDot.String())
	assert.Equal(t, "==", TokenEqEq.String())
	assert.Equal(t, "&&", TokenAnd.String())
	assert.Equal(t, "||", TokenOr.String())
}
