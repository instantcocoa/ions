package expression

import (
	"fmt"
	"math"
	"strings"
)

// Context provides variable lookup for expression evaluation.
type Context interface {
	Lookup(name string) (Value, bool)
}

// MapContext is a simple map-based Context implementation.
type MapContext map[string]Value

// Lookup finds a value by name (case-insensitive).
func (m MapContext) Lookup(name string) (Value, bool) {
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return Null(), false
}

// Evaluator evaluates expression AST nodes.
type Evaluator struct {
	Context     Context
	Functions   map[string]Function
	JobStatus   string // "success", "failure", "cancelled"
	StepOutcome string // outcome of the current step's if condition context
}

// NewEvaluator creates a new Evaluator with the given context and built-in functions.
func NewEvaluator(ctx Context) *Evaluator {
	return &Evaluator{
		Context:   ctx,
		Functions: BuiltinFunctions(),
		JobStatus: "success", // default
	}
}

// Eval evaluates an AST node and returns the resulting Value.
func (e *Evaluator) Eval(node Node) (Value, error) {
	switch n := node.(type) {
	case *Literal:
		return n.Val, nil
	case *Ident:
		return e.evalIdent(n)
	case *DotAccess:
		return e.evalDotAccess(n)
	case *IndexAccessNode:
		return e.evalIndexAccess(n)
	case *FunctionCall:
		return e.evalFunctionCall(n)
	case *UnaryOp:
		return e.evalUnaryOp(n)
	case *BinaryOp:
		return e.evalBinaryOp(n)
	case *LogicalOp:
		return e.evalLogicalOp(n)
	case *Grouping:
		return e.Eval(n.Expr)
	default:
		return Null(), fmt.Errorf("unknown node type: %T", node)
	}
}

func (e *Evaluator) evalIdent(n *Ident) (Value, error) {
	if e.Context == nil {
		return String(""), nil
	}
	val, ok := e.Context.Lookup(n.Name)
	if !ok {
		// GitHub's behavior: unknown context refs return empty string
		return String(""), nil
	}
	return val, nil
}

func (e *Evaluator) evalDotAccess(n *DotAccess) (Value, error) {
	obj, err := e.Eval(n.Object)
	if err != nil {
		return Null(), err
	}
	return PropertyAccess(obj, n.Field), nil
}

func (e *Evaluator) evalIndexAccess(n *IndexAccessNode) (Value, error) {
	obj, err := e.Eval(n.Object)
	if err != nil {
		return Null(), err
	}
	index, err := e.Eval(n.Index)
	if err != nil {
		return Null(), err
	}
	return IndexAccess(obj, index), nil
}

func (e *Evaluator) evalFunctionCall(n *FunctionCall) (Value, error) {
	// Check for status functions first
	nameLower := strings.ToLower(n.Name)
	switch nameLower {
	case "success":
		if len(n.Args) != 0 {
			return Null(), fmt.Errorf("success() takes no arguments, got %d", len(n.Args))
		}
		return Bool(e.JobStatus == "success" || e.JobStatus == ""), nil
	case "failure":
		if len(n.Args) != 0 {
			return Null(), fmt.Errorf("failure() takes no arguments, got %d", len(n.Args))
		}
		return Bool(e.JobStatus == "failure"), nil
	case "always":
		if len(n.Args) != 0 {
			return Null(), fmt.Errorf("always() takes no arguments, got %d", len(n.Args))
		}
		return Bool(true), nil
	case "cancelled":
		if len(n.Args) != 0 {
			return Null(), fmt.Errorf("cancelled() takes no arguments, got %d", len(n.Args))
		}
		return Bool(e.JobStatus == "cancelled"), nil
	}

	// Look up built-in function
	fn, ok := LookupFunction(e.Functions, n.Name)
	if !ok {
		return Null(), fmt.Errorf("unknown function %q", n.Name)
	}

	// Evaluate arguments
	args := make([]Value, len(n.Args))
	for i, argNode := range n.Args {
		val, err := e.Eval(argNode)
		if err != nil {
			return Null(), fmt.Errorf("error evaluating argument %d of %s(): %w", i, n.Name, err)
		}
		args[i] = val
	}

	return fn(args)
}

func (e *Evaluator) evalUnaryOp(n *UnaryOp) (Value, error) {
	if n.Op != "!" {
		return Null(), fmt.Errorf("unknown unary operator %q", n.Op)
	}
	operand, err := e.Eval(n.Operand)
	if err != nil {
		return Null(), err
	}
	return Bool(!IsTruthy(operand)), nil
}

func (e *Evaluator) evalBinaryOp(n *BinaryOp) (Value, error) {
	left, err := e.Eval(n.Left)
	if err != nil {
		return Null(), err
	}
	right, err := e.Eval(n.Right)
	if err != nil {
		return Null(), err
	}

	switch n.Op {
	case "==":
		return Bool(equalCompare(left, right)), nil
	case "!=":
		return Bool(!equalCompare(left, right)), nil
	case "<", "<=", ">", ">=":
		return e.evalComparisonOp(n.Op, left, right)
	default:
		return Null(), fmt.Errorf("unknown binary operator %q", n.Op)
	}
}

// equalCompare implements GitHub Actions equality semantics.
// If types match: compare directly (string comparison is case-insensitive).
// If types differ: coerce both to numbers and compare.
// null == null is true. null == anything_else uses numeric coercion.
func equalCompare(a, b Value) bool {
	// Same type — direct comparison
	if a.Kind() == b.Kind() {
		switch a.Kind() {
		case KindNull:
			return true
		case KindBool:
			return a.BoolVal() == b.BoolVal()
		case KindNumber:
			if math.IsNaN(a.NumberVal()) && math.IsNaN(b.NumberVal()) {
				return false // NaN != NaN in comparison
			}
			return a.NumberVal() == b.NumberVal()
		case KindString:
			return strings.EqualFold(a.StringVal(), b.StringVal())
		case KindArray, KindObject:
			// Arrays and objects are compared by reference equivalence in GitHub,
			// but since we don't have references, compare structurally
			return a.Equals(b)
		}
	}

	// null comparisons
	if a.Kind() == KindNull || b.Kind() == KindNull {
		// null only equals null
		return a.Kind() == KindNull && b.Kind() == KindNull
	}

	// Different types — coerce both to numbers
	an := CoerceToNumber(a)
	bn := CoerceToNumber(b)
	if math.IsNaN(an) || math.IsNaN(bn) {
		return false
	}
	return an == bn
}

func (e *Evaluator) evalComparisonOp(op string, left, right Value) (Value, error) {
	ln := CoerceToNumber(left)
	rn := CoerceToNumber(right)

	// If either is NaN, all comparisons are false
	if math.IsNaN(ln) || math.IsNaN(rn) {
		return Bool(false), nil
	}

	switch op {
	case "<":
		return Bool(ln < rn), nil
	case "<=":
		return Bool(ln <= rn), nil
	case ">":
		return Bool(ln > rn), nil
	case ">=":
		return Bool(ln >= rn), nil
	default:
		return Null(), fmt.Errorf("unknown comparison operator %q", op)
	}
}

func (e *Evaluator) evalLogicalOp(n *LogicalOp) (Value, error) {
	left, err := e.Eval(n.Left)
	if err != nil {
		return Null(), err
	}

	switch n.Op {
	case "||":
		// If truthy, return left (short-circuit); else return right
		if IsTruthy(left) {
			return left, nil
		}
		return e.Eval(n.Right)
	case "&&":
		// If falsy, return left (short-circuit); else return right
		if !IsTruthy(left) {
			return left, nil
		}
		return e.Eval(n.Right)
	default:
		return Null(), fmt.Errorf("unknown logical operator %q", n.Op)
	}
}

// EvalExpression is a convenience function that parses and evaluates an expression string.
func EvalExpression(input string, ctx Context) (Value, error) {
	node, err := Parse(input)
	if err != nil {
		return Null(), fmt.Errorf("parse error: %w", err)
	}
	eval := NewEvaluator(ctx)
	return eval.Eval(node)
}
