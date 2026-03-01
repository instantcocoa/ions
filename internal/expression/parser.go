package expression

import (
	"fmt"
	"strconv"
)

// Parser is a recursive descent parser for GitHub Actions expressions.
type Parser struct {
	tokens []Token
	pos    int
}

// Parse parses a GitHub Actions expression string into an AST.
func Parse(input string) (Node, error) {
	tokens, err := Lex(input)
	if err != nil {
		return nil, err
	}
	p := &Parser{tokens: tokens, pos: 0}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Type != TokenEOF {
		return nil, fmt.Errorf("unexpected token %q at position %d, expected end of expression", p.peek().Value, p.peek().Pos)
	}
	return node, nil
}

func (p *Parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) advance() Token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) expect(typ TokenType) (Token, error) {
	tok := p.peek()
	if tok.Type != typ {
		return Token{}, fmt.Errorf("expected %s but got %s (%q) at position %d", typ, tok.Type, tok.Value, tok.Pos)
	}
	return p.advance(), nil
}

// Grammar (low to high precedence):
// expr       = logicalOr
// logicalOr  = logicalAnd ( "||" logicalAnd )*
// logicalAnd = equality ( "&&" equality )*
// equality   = comparison ( ("==" | "!=") comparison )*
// comparison = unary ( ("<" | "<=" | ">" | ">=") unary )*
// unary      = "!" unary | postfix
// postfix    = primary ( "." IDENT | "[" expr "]" | "(" args ")" )*
// primary    = NULL | TRUE | FALSE | NUMBER | STRING | IDENT | "(" expr ")"

func (p *Parser) parseExpr() (Node, error) {
	return p.parseLogicalOr()
}

func (p *Parser) parseLogicalOr() (Node, error) {
	left, err := p.parseLogicalAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TokenOr {
		p.advance()
		right, err := p.parseLogicalAnd()
		if err != nil {
			return nil, err
		}
		left = &LogicalOp{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseLogicalAnd() (Node, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TokenAnd {
		p.advance()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = &LogicalOp{Op: "&&", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseEquality() (Node, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TokenEqEq || p.peek().Type == TokenBangEq {
		op := p.advance()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op.Value, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseComparison() (Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TokenLess || p.peek().Type == TokenLessEq ||
		p.peek().Type == TokenGreater || p.peek().Type == TokenGreaterEq {
		op := p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op.Value, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseUnary() (Node, error) {
	if p.peek().Type == TokenBang {
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Op: "!", Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (Node, error) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Type {
		case TokenDot:
			p.advance()
			tok, err := p.expect(TokenIdent)
			if err != nil {
				// Allow keywords after dot (e.g., steps.null is unlikely but be safe)
				if p.peek().Type == TokenNull || p.peek().Type == TokenTrue || p.peek().Type == TokenFalse {
					tok = p.advance()
				} else {
					return nil, fmt.Errorf("expected identifier after '.' at position %d", p.peek().Pos)
				}
			}
			node = &DotAccess{Object: node, Field: tok.Value}
		case TokenLBracket:
			p.advance()
			index, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			_, err = p.expect(TokenRBracket)
			if err != nil {
				return nil, err
			}
			node = &IndexAccessNode{Object: node, Index: index}
		case TokenLParen:
			// Function call — the node must be an Ident
			ident, ok := node.(*Ident)
			if !ok {
				return nil, fmt.Errorf("cannot call non-identifier at position %d", p.peek().Pos)
			}
			p.advance()
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			_, err = p.expect(TokenRParen)
			if err != nil {
				return nil, err
			}
			node = &FunctionCall{Name: ident.Name, Args: args}
		default:
			return node, nil
		}
	}
}

func (p *Parser) parseArgs() ([]Node, error) {
	var args []Node
	if p.peek().Type == TokenRParen {
		return args, nil
	}
	arg, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	args = append(args, arg)
	for p.peek().Type == TokenComma {
		p.advance()
		arg, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

func (p *Parser) parsePrimary() (Node, error) {
	tok := p.peek()
	switch tok.Type {
	case TokenNull:
		p.advance()
		return &Literal{Val: Null()}, nil
	case TokenTrue:
		p.advance()
		return &Literal{Val: Bool(true)}, nil
	case TokenFalse:
		p.advance()
		return &Literal{Val: Bool(false)}, nil
	case TokenNumber:
		p.advance()
		n, err := parseNumber(tok.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q at position %d: %w", tok.Value, tok.Pos, err)
		}
		return &Literal{Val: Number(n)}, nil
	case TokenString:
		p.advance()
		return &Literal{Val: String(tok.Value)}, nil
	case TokenIdent:
		p.advance()
		return &Ident{Name: tok.Value}, nil
	case TokenLParen:
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		_, err = p.expect(TokenRParen)
		if err != nil {
			return nil, err
		}
		return &Grouping{Expr: expr}, nil
	default:
		return nil, fmt.Errorf("unexpected token %s (%q) at position %d", tok.Type, tok.Value, tok.Pos)
	}
}

// parseNumber parses a number token value into a float64.
func parseNumber(s string) (float64, error) {
	// Try hex first
	if len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		n, err := strconv.ParseInt(s, 0, 64)
		if err != nil {
			return 0, err
		}
		return float64(n), nil
	}
	// Negative hex
	if len(s) > 3 && s[0] == '-' && s[1] == '0' && (s[2] == 'x' || s[2] == 'X') {
		n, err := strconv.ParseInt(s, 0, 64)
		if err != nil {
			return 0, err
		}
		return float64(n), nil
	}
	return strconv.ParseFloat(s, 64)
}
