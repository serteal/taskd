package query

import (
	"fmt"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// EvalProgram compiles an arbitrary CEL expression (any result type — this
// is the display-contribution path, where a column expression may yield a
// string, timestamp, int, or bool) and returns a per-item evaluator
// producing the native Go value. Same environment, variables, and clock
// semantics as filters.
func (e *Engine) EvalProgram(expr string) (func(item *taskcorev1.Item) (any, error), error) {
	env := e.celEnv()
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("invalid expression: %w", iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("invalid expression: %w", err)
	}
	return func(item *taskcorev1.Item) (any, error) {
		out, _, err := prg.Eval(e.activation(item, e.now()))
		if err != nil {
			return nil, fmt.Errorf("expression evaluation failed: %w", err)
		}
		return out.Value(), nil
	}, nil
}
