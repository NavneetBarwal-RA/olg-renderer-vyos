package apply

import (
	"context"
	"fmt"
	"strings"
)

func (e *Engine) prepare(ctx context.Context, input Input) (Plan, error) {
	if e == nil {
		return Plan{}, newError(CodePlanFailed, "apply engine is not initialized", nil)
	}
	if err := validatePrepareInput(ctx, input); err != nil {
		return Plan{}, err
	}

	setCommands, err := parseCommands(input.DesiredCommands)
	if err != nil {
		return Plan{}, err
	}

	return Plan{
		Target:         input.Target,
		ConfigUUID:     input.ConfigUUID,
		DeleteCommands: buildDeleteCommands(e.resetPolicy),
		SetCommands:    setCommands,
		Commit:         true,
		Save:           e.saveAfterCommit,
	}, nil
}

func validatePrepareInput(ctx context.Context, input Input) error {
	if ctx == nil {
		return newError(CodeInvalidInput, "context is required", nil)
	}
	select {
	case <-ctx.Done():
		return newError(CodeInvalidInput, "context already canceled", ctx.Err())
	default:
	}

	if strings.TrimSpace(input.Target) == "" {
		return newError(CodeInvalidInput, "target is required", nil)
	}
	if input.Target != applyTarget {
		return newError(CodeInvalidInput, fmt.Sprintf("unsupported target %q", input.Target), nil)
	}
	if strings.TrimSpace(input.ConfigUUID) == "" {
		return newError(CodeInvalidInput, "config_uuid is required", nil)
	}
	if strings.TrimSpace(input.DesiredCommands) == "" {
		return newError(CodeEmptyDesiredCommands, "desired_commands is required", nil)
	}
	return nil
}
