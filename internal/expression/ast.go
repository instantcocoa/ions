package expression

// Node represents a node in the expression AST.
type Node interface {
	nodeType() string
}

// Literal represents a literal value (null, true, false, number, string).
type Literal struct {
	Val Value
}

func (n *Literal) nodeType() string { return "Literal" }

// Ident represents an identifier reference (e.g., "github", "env").
type Ident struct {
	Name string
}

func (n *Ident) nodeType() string { return "Ident" }

// DotAccess represents property access with dot notation (e.g., github.ref).
type DotAccess struct {
	Object Node
	Field  string
}

func (n *DotAccess) nodeType() string { return "DotAccess" }

// IndexAccess represents bracket-based index/key access (e.g., matrix['os']).
type IndexAccessNode struct {
	Object Node
	Index  Node
}

func (n *IndexAccessNode) nodeType() string { return "IndexAccess" }

// FunctionCall represents a function invocation (e.g., contains(x, y)).
type FunctionCall struct {
	Name string
	Args []Node
}

func (n *FunctionCall) nodeType() string { return "FunctionCall" }

// UnaryOp represents a unary operation (e.g., !expr).
type UnaryOp struct {
	Op      string
	Operand Node
}

func (n *UnaryOp) nodeType() string { return "UnaryOp" }

// BinaryOp represents a binary comparison or equality operation.
type BinaryOp struct {
	Op    string
	Left  Node
	Right Node
}

func (n *BinaryOp) nodeType() string { return "BinaryOp" }

// LogicalOp represents a logical && or || operation with short-circuit semantics.
type LogicalOp struct {
	Op    string
	Left  Node
	Right Node
}

func (n *LogicalOp) nodeType() string { return "LogicalOp" }

// Grouping represents a parenthesized expression.
type Grouping struct {
	Expr Node
}

func (n *Grouping) nodeType() string { return "Grouping" }
