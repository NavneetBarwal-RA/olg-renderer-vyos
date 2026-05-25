package apply

import (
	"errors"
	"fmt"
)

// Code identifies a stable apply error category.
type Code string

const (
	// CodeInvalidInput indicates invalid apply metadata or construction input.
	CodeInvalidInput Code = "invalid_input"
	// CodeEmptyDesiredCommands indicates no rendered set commands were supplied.
	CodeEmptyDesiredCommands Code = "empty_desired_commands"
	// CodeInvalidCommand indicates DesiredCommands contained an unsafe or unsupported command.
	CodeInvalidCommand Code = "invalid_command"
	// CodePlanFailed indicates deterministic plan construction failed.
	CodePlanFailed Code = "plan_failed"
	// CodeExecutorFailed indicates no executor was configured or execution failed unexpectedly.
	CodeExecutorFailed Code = "executor_failed"
	// CodeDeleteFailed indicates executor delete-command execution failed.
	CodeDeleteFailed Code = "delete_failed"
	// CodeSetFailed indicates executor set-command execution failed.
	CodeSetFailed Code = "set_failed"
	// CodeCommitFailed indicates executor commit failed.
	CodeCommitFailed Code = "commit_failed"
	// CodeSaveFailed indicates executor save failed.
	CodeSaveFailed Code = "save_failed"
	// CodeDiscardFailed indicates executor discard failed.
	CodeDiscardFailed Code = "discard_failed"
	// CodeApplyFailed indicates an uncategorized apply failure.
	CodeApplyFailed Code = "apply_failed"
)

// Error is a typed apply error.
type Error struct {
	Code    Code
	Message string
	Err     error
}

// Error returns a useful text form containing the stable code and message.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		if e.Err != nil {
			return fmt.Sprintf("%s: %v", e.Code, e.Err)
		}
		return string(e.Code)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the wrapped root cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is allows errors.Is with code-level matching.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	if t.Code != "" {
		return e.Code == t.Code
	}
	if t.Message != "" {
		return e.Message == t.Message
	}
	return false
}

// CodeOf returns the first apply error code found in err.
func CodeOf(err error) Code {
	var applyErr *Error
	if errors.As(err, &applyErr) {
		return applyErr.Code
	}
	return ""
}

// IsCode reports whether err contains an apply Error with code.
func IsCode(err error, code Code) bool {
	return CodeOf(err) == code
}

func newError(code Code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}
