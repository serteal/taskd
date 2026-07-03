package query

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// Compiled is a compiled filter: a SQL narrowing over the store's indexed
// columns plus an optional per-item residual. Where "" and a nil Residual
// together mean match-everything.
type Compiled struct {
	// Where is SQL over the indexed columns (see store.Query), "" if none
	// of the filter could be pushed down.
	Where string
	Args  []any
	// Residual evaluates the full filter against one item; nil when the
	// whole filter was pushed down.
	Residual func(item *taskcorev1.Item) (bool, error)
}

// Compile parses and type-checks the filter and splits it into a SQL
// pushdown plus a residual evaluator. Parse and type errors are returned
// with user-facing messages (the server maps them to InvalidArgument). An
// empty filter compiles to match-everything.
//
// Pushdown recognizes only top-level && conjuncts of the forms
// `completed`, `!completed`, `kind == "lit"`, and `project == "lit"`
// (either operand order for ==). When every conjunct is recognized the
// residual is dropped; otherwise the residual evaluates the FULL original
// expression. Re-checking pushed conjuncts in the residual is deliberately
// redundant: pushdown is only a narrowing optimization, and evaluating the
// whole filter per row keeps correctness independent of the pushdown and
// the implementation simple. The Where can therefore never exclude an item
// the full expression would match.
func (e *Engine) Compile(filter string) (*Compiled, error) {
	ast, prg, err := e.compileFilter(filter)
	if err != nil {
		return nil, err
	}
	if prg == nil { // empty filter
		return &Compiled{}, nil
	}
	where, args, fullyPushed := pushdown(ast.NativeRep().Expr())
	c := &Compiled{Where: where, Args: args}
	if !fullyPushed {
		c.Residual = e.evaluator(prg)
	}
	return c, nil
}

// Matcher returns a full-expression evaluator for the filter against a
// single item — the same environment, variables, and clock semantics as
// Compile's residual (it shares the compilation path, so the two can never
// diverge). An empty filter matches everything. Used by Watch, which has
// no SQL to push down to.
func (e *Engine) Matcher(filter string) (func(item *taskcorev1.Item) (bool, error), error) {
	_, prg, err := e.compileFilter(filter)
	if err != nil {
		return nil, err
	}
	if prg == nil { // empty filter
		return func(*taskcorev1.Item) (bool, error) { return true, nil }, nil
	}
	return e.evaluator(prg), nil
}

// compileFilter parses, type-checks, and plans the filter. An empty (or
// all-whitespace) filter returns (nil, nil, nil).
func (e *Engine) compileFilter(filter string) (*cel.Ast, cel.Program, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, nil, nil
	}
	ast, iss := e.env.Compile(filter)
	if iss != nil && iss.Err() != nil {
		return nil, nil, fmt.Errorf("invalid filter: %w", iss.Err())
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, nil, fmt.Errorf("invalid filter: expression must evaluate to a boolean, but evaluates to %s", ast.OutputType())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid filter: %w", err)
	}
	return ast, prg, nil
}

// evaluator wraps a compiled program as a per-item predicate. The `now`
// variable is taken from the injected clock at each call, not at compile
// time.
func (e *Engine) evaluator(prg cel.Program) func(item *taskcorev1.Item) (bool, error) {
	return func(item *taskcorev1.Item) (bool, error) {
		out, _, err := prg.Eval(e.activation(item, e.now()))
		if err != nil {
			return false, fmt.Errorf("filter evaluation failed: %w", err)
		}
		b, ok := out.Value().(bool)
		if !ok {
			return false, fmt.Errorf("filter evaluation produced %T, want bool", out.Value())
		}
		return b, nil
	}
}

// pushdown inspects the checked expression's top-level && conjuncts and
// translates the recognized ones to SQL over the indexed columns. It
// reports whether every conjunct was recognized (in which case the SQL is
// exact and no residual is needed).
func pushdown(expr celast.Expr) (where string, args []any, fullyPushed bool) {
	var frags []string
	fullyPushed = true
	for _, c := range conjuncts(expr) {
		frag, fragArgs, ok := recognizeConjunct(c)
		if !ok {
			fullyPushed = false
			continue
		}
		frags = append(frags, frag)
		args = append(args, fragArgs...)
	}
	return strings.Join(frags, " AND "), args, fullyPushed
}

// conjuncts flattens nested top-level && calls into a list of conjuncts.
// Any non-&& node (including a top-level ||) is a single conjunct.
func conjuncts(expr celast.Expr) []celast.Expr {
	if expr.Kind() == celast.CallKind {
		call := expr.AsCall()
		if call.FunctionName() == operators.LogicalAnd {
			var out []celast.Expr
			for _, arg := range call.Args() {
				out = append(out, conjuncts(arg)...)
			}
			return out
		}
	}
	return []celast.Expr{expr}
}

// recognizeConjunct translates a single conjunct to SQL if it is one of
// the exact supported patterns: `completed`, `!completed`,
// `kind == "lit"`, `project == "lit"` (== operands in either order).
func recognizeConjunct(expr celast.Expr) (frag string, args []any, ok bool) {
	switch expr.Kind() {
	case celast.IdentKind:
		if expr.AsIdent() == "completed" {
			return "completed = 1", nil, true
		}
	case celast.CallKind:
		call := expr.AsCall()
		switch call.FunctionName() {
		case operators.LogicalNot:
			cargs := call.Args()
			if len(cargs) == 1 && cargs[0].Kind() == celast.IdentKind && cargs[0].AsIdent() == "completed" {
				return "completed = 0", nil, true
			}
		case operators.Equals:
			cargs := call.Args()
			if len(cargs) != 2 {
				return "", nil, false
			}
			col, lit, matched := identStringPair(cargs[0], cargs[1])
			if matched && (col == "kind" || col == "project") {
				return col + " = ?", []any{lit}, true
			}
		}
	}
	return "", nil, false
}

// identStringPair matches an (identifier, string literal) pair in either
// order.
func identStringPair(a, b celast.Expr) (ident, lit string, ok bool) {
	if a.Kind() == celast.LiteralKind {
		a, b = b, a
	}
	if a.Kind() != celast.IdentKind || b.Kind() != celast.LiteralKind {
		return "", "", false
	}
	s, isStr := b.AsLiteral().Value().(string)
	if !isStr {
		return "", "", false
	}
	return a.AsIdent(), s, true
}
