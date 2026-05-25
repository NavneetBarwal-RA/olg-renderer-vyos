package apply

// Option mutates apply engine construction behavior.
type Option func(*Engine) error

// WithExecutor overrides the default executor used by Apply.
//
// Normal production callers should use New without this option so Apply uses
// the controlled internal VyOS executor. WithExecutor is intended for tests and
// advanced controlled integrations. Custom executors receive validated Plan
// data, but can bypass runtime execution safety if implemented incorrectly; they
// must not execute concatenated shell strings or expose arbitrary command
// execution.
func WithExecutor(exec Executor) Option {
	return func(e *Engine) error {
		if exec == nil {
			return newError(CodeInvalidInput, "executor must not be nil", nil)
		}
		e.executor = exec
		return nil
	}
}

// WithSaveAfterCommit enables or disables saving after a successful commit.
func WithSaveAfterCommit(enabled bool) Option {
	return func(e *Engine) error {
		e.saveAfterCommit = enabled
		return nil
	}
}

// WithResetPolicy replaces the engine's reset policy after validating every root.
func WithResetPolicy(policy ResetPolicy) Option {
	return func(e *Engine) error {
		normalized, err := validateResetPolicy(policy)
		if err != nil {
			return err
		}
		e.resetPolicy = normalized
		return nil
	}
}

func wrapOptionError(err error) error {
	if err == nil {
		return nil
	}
	if CodeOf(err) != "" {
		return err
	}
	return newError(CodeInvalidInput, "invalid apply option", err)
}
