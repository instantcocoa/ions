package expression

import (
	"fmt"
	"strings"
	"unicode"
)

// Lexer tokenizes a GitHub Actions expression string.
type Lexer struct {
	input []rune
	pos   int
}

// Lex tokenizes the given input string into a slice of Tokens.
func Lex(input string) ([]Token, error) {
	l := &Lexer{
		input: []rune(input),
		pos:   0,
	}
	return l.tokenize()
}

func (l *Lexer) tokenize() ([]Token, error) {
	var tokens []Token
	for {
		l.skipWhitespace()
		if l.pos >= len(l.input) {
			tokens = append(tokens, Token{Type: TokenEOF, Pos: l.pos})
			break
		}

		tok, err := l.nextToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
	}
	return tokens, nil
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) && unicode.IsSpace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *Lexer) peekAt(offset int) rune {
	pos := l.pos + offset
	if pos >= len(l.input) {
		return 0
	}
	return l.input[pos]
}

func (l *Lexer) advance() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	ch := l.input[l.pos]
	l.pos++
	return ch
}

func (l *Lexer) nextToken() (Token, error) {
	startPos := l.pos
	ch := l.peek()

	// Single-quoted strings
	if ch == '\'' {
		return l.lexString()
	}

	// Numbers: digits or negative sign before digit
	if isDigit(ch) || (ch == '-' && l.pos+1 < len(l.input) && isDigit(l.peekAt(1))) {
		return l.lexNumber()
	}

	// Two-character operators
	if l.pos+1 < len(l.input) {
		two := string(l.input[l.pos : l.pos+2])
		switch two {
		case "==":
			l.pos += 2
			return Token{Type: TokenEqEq, Value: "==", Pos: startPos}, nil
		case "!=":
			l.pos += 2
			return Token{Type: TokenBangEq, Value: "!=", Pos: startPos}, nil
		case "<=":
			l.pos += 2
			return Token{Type: TokenLessEq, Value: "<=", Pos: startPos}, nil
		case ">=":
			l.pos += 2
			return Token{Type: TokenGreaterEq, Value: ">=", Pos: startPos}, nil
		case "&&":
			l.pos += 2
			return Token{Type: TokenAnd, Value: "&&", Pos: startPos}, nil
		case "||":
			l.pos += 2
			return Token{Type: TokenOr, Value: "||", Pos: startPos}, nil
		}
	}

	// Single-character operators and punctuation
	switch ch {
	case '.':
		l.advance()
		return Token{Type: TokenDot, Value: ".", Pos: startPos}, nil
	case '[':
		l.advance()
		return Token{Type: TokenLBracket, Value: "[", Pos: startPos}, nil
	case ']':
		l.advance()
		return Token{Type: TokenRBracket, Value: "]", Pos: startPos}, nil
	case '(':
		l.advance()
		return Token{Type: TokenLParen, Value: "(", Pos: startPos}, nil
	case ')':
		l.advance()
		return Token{Type: TokenRParen, Value: ")", Pos: startPos}, nil
	case ',':
		l.advance()
		return Token{Type: TokenComma, Value: ",", Pos: startPos}, nil
	case '!':
		l.advance()
		return Token{Type: TokenBang, Value: "!", Pos: startPos}, nil
	case '<':
		l.advance()
		return Token{Type: TokenLess, Value: "<", Pos: startPos}, nil
	case '>':
		l.advance()
		return Token{Type: TokenGreater, Value: ">", Pos: startPos}, nil
	}

	// Identifiers and keywords
	if isIdentStart(ch) {
		return l.lexIdentOrKeyword()
	}

	return Token{}, fmt.Errorf("unexpected character %q at position %d", string(ch), startPos)
}

func (l *Lexer) lexString() (Token, error) {
	startPos := l.pos
	l.advance() // consume opening '

	var sb strings.Builder
	for {
		if l.pos >= len(l.input) {
			return Token{}, fmt.Errorf("unterminated string starting at position %d", startPos)
		}
		ch := l.advance()
		if ch == '\'' {
			// Check for escaped single quote ''
			if l.pos < len(l.input) && l.peek() == '\'' {
				sb.WriteRune('\'')
				l.advance() // consume second '
			} else {
				// End of string
				break
			}
		} else {
			sb.WriteRune(ch)
		}
	}
	return Token{Type: TokenString, Value: sb.String(), Pos: startPos}, nil
}

func (l *Lexer) lexNumber() (Token, error) {
	startPos := l.pos
	var sb strings.Builder

	// Optional negative sign
	if l.peek() == '-' {
		sb.WriteRune(l.advance())
	}

	// Check for hex prefix
	if l.peek() == '0' && l.pos+1 < len(l.input) && (l.peekAt(1) == 'x' || l.peekAt(1) == 'X') {
		sb.WriteRune(l.advance()) // '0'
		sb.WriteRune(l.advance()) // 'x' or 'X'
		if l.pos >= len(l.input) || !isHexDigit(l.peek()) {
			return Token{}, fmt.Errorf("expected hex digit after '0x' at position %d", l.pos)
		}
		for l.pos < len(l.input) && isHexDigit(l.peek()) {
			sb.WriteRune(l.advance())
		}
		return Token{Type: TokenNumber, Value: sb.String(), Pos: startPos}, nil
	}

	// Integer part
	for l.pos < len(l.input) && isDigit(l.peek()) {
		sb.WriteRune(l.advance())
	}

	// Decimal part
	if l.pos < len(l.input) && l.peek() == '.' && l.pos+1 < len(l.input) && isDigit(l.peekAt(1)) {
		sb.WriteRune(l.advance()) // '.'
		for l.pos < len(l.input) && isDigit(l.peek()) {
			sb.WriteRune(l.advance())
		}
	}

	// Exponent part
	if l.pos < len(l.input) && (l.peek() == 'e' || l.peek() == 'E') {
		sb.WriteRune(l.advance()) // 'e' or 'E'
		if l.pos < len(l.input) && (l.peek() == '+' || l.peek() == '-') {
			sb.WriteRune(l.advance())
		}
		if l.pos >= len(l.input) || !isDigit(l.peek()) {
			return Token{}, fmt.Errorf("expected digit in exponent at position %d", l.pos)
		}
		for l.pos < len(l.input) && isDigit(l.peek()) {
			sb.WriteRune(l.advance())
		}
	}

	return Token{Type: TokenNumber, Value: sb.String(), Pos: startPos}, nil
}

func (l *Lexer) lexIdentOrKeyword() (Token, error) {
	startPos := l.pos
	var sb strings.Builder

	// First char is already validated as isIdentStart
	sb.WriteRune(l.advance())

	// Continue with ident chars (including hyphens)
	for l.pos < len(l.input) && isIdentContinue(l.peek()) {
		sb.WriteRune(l.advance())
	}

	word := sb.String()

	// Check for keywords (case-sensitive)
	switch word {
	case "null":
		return Token{Type: TokenNull, Value: word, Pos: startPos}, nil
	case "true":
		return Token{Type: TokenTrue, Value: word, Pos: startPos}, nil
	case "false":
		return Token{Type: TokenFalse, Value: word, Pos: startPos}, nil
	default:
		return Token{Type: TokenIdent, Value: word, Pos: startPos}, nil
	}
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

func isHexDigit(ch rune) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func isIdentStart(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentContinue(ch rune) bool {
	return isIdentStart(ch) || isDigit(ch) || ch == '-'
}
