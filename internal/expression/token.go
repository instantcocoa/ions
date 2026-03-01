package expression

import "fmt"

// TokenType represents the type of a lexer token.
type TokenType int

const (
	TokenEOF     TokenType = iota
	TokenNull              // null
	TokenTrue              // true
	TokenFalse             // false
	TokenNumber            // 42, 3.14, 0xff, 1e10
	TokenString            // 'hello'
	TokenIdent             // identifier
	TokenDot               // .
	TokenLBracket          // [
	TokenRBracket          // ]
	TokenLParen            // (
	TokenRParen            // )
	TokenComma             // ,
	TokenBang              // !
	TokenEqEq              // ==
	TokenBangEq            // !=
	TokenLess              // <
	TokenLessEq            // <=
	TokenGreater           // >
	TokenGreaterEq         // >=
	TokenAnd               // &&
	TokenOr                // ||
)

// String returns the string representation of a TokenType.
func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenNull:
		return "null"
	case TokenTrue:
		return "true"
	case TokenFalse:
		return "false"
	case TokenNumber:
		return "number"
	case TokenString:
		return "string"
	case TokenIdent:
		return "ident"
	case TokenDot:
		return "."
	case TokenLBracket:
		return "["
	case TokenRBracket:
		return "]"
	case TokenLParen:
		return "("
	case TokenRParen:
		return ")"
	case TokenComma:
		return ","
	case TokenBang:
		return "!"
	case TokenEqEq:
		return "=="
	case TokenBangEq:
		return "!="
	case TokenLess:
		return "<"
	case TokenLessEq:
		return "<="
	case TokenGreater:
		return ">"
	case TokenGreaterEq:
		return ">="
	case TokenAnd:
		return "&&"
	case TokenOr:
		return "||"
	default:
		return fmt.Sprintf("TokenType(%d)", int(t))
	}
}

// Token represents a lexer token with type, value, and position.
type Token struct {
	Type  TokenType
	Value string
	Pos   int
}
