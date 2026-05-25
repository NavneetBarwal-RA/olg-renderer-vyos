package apply

import (
	"context"
	"errors"
	"fmt"

	"github.com/routerarchitects/olg-renderer-vyos/internal/vyos"
)

type vyosRunner interface {
	Configure(ctx context.Context) (string, error)
	RunConfigCommand(ctx context.Context, command string) (string, error)
	Commit(ctx context.Context) (string, error)
	Save(ctx context.Context) (string, error)
	Discard(ctx context.Context) (string, error)
}

type defaultExecutor struct {
	runner vyosRunner
}

func newDefaultExecutor() Executor {
	return &defaultExecutor{runner: vyos.NewCLIShellRunner()}
}

func newDefaultExecutorWithRunner(runner vyosRunner) *defaultExecutor {
	return &defaultExecutor{runner: runner}
}

func (e *defaultExecutor) Execute(ctx context.Context, plan Plan) (ExecutionResult, error) {
	var result ExecutionResult
	if err := checkExecutionContext(ctx); err != nil {
		return result, err
	}
	if e == nil || e.runner == nil {
		return result, newError(CodeExecutorFailed, "vyos executor is not initialized", nil)
	}

	if _, err := e.runner.Configure(ctx); err != nil {
		return result, newError(CodeExecutorFailed, "failed to enter VyOS configure mode", err)
	}

	for _, command := range plan.DeleteCommands {
		if err := checkExecutionContext(ctx); err != nil {
			return result, err
		}
		if _, err := e.runner.RunConfigCommand(ctx, command); err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, err)
			return result, newError(CodeDeleteFailed, fmt.Sprintf("delete command failed: %s", command), err)
		}
	}

	for _, command := range plan.SetCommands {
		if err := checkExecutionContext(ctx); err != nil {
			return result, err
		}
		if _, err := e.runner.RunConfigCommand(ctx, command); err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, err)
			return result, newError(CodeSetFailed, fmt.Sprintf("set command failed: %s", command), err)
		}
	}

	if plan.Commit {
		if err := checkExecutionContext(ctx); err != nil {
			return result, err
		}
		output, err := e.runner.Commit(ctx)
		result.CommitOutput = output
		if err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, err)
			return result, newError(CodeCommitFailed, "commit failed", err)
		}
	}

	result.Applied = true
	if !plan.Save {
		return result, nil
	}

	if err := checkExecutionContext(ctx); err != nil {
		result.Applied = true
		return result, err
	}
	output, err := e.runner.Save(ctx)
	result.SaveOutput = output
	if err != nil {
		if isContextError(err) {
			return result, newError(CodeExecutorFailed, "context canceled during execution", err)
		}
		result.Saved = false
		return result, newError(CodeSaveFailed, "save failed after successful commit", err)
	}
	result.Saved = true
	return result, nil
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (e *defaultExecutor) discardAfterFailure(ctx context.Context, primary error) (string, error) {
	output, discardErr := e.runner.Discard(ctx)
	if discardErr != nil {
		if output == "" {
			output = fmt.Sprintf("discard failed: %v", discardErr)
		} else {
			output = fmt.Sprintf("%s\ndiscard failed: %v", output, discardErr)
		}
		return output, fmt.Errorf("%w; discard failed: %v", primary, discardErr)
	}
	return output, primary
}

func checkExecutionContext(ctx context.Context) error {
	if ctx == nil {
		return newError(CodeExecutorFailed, "context is required", nil)
	}
	select {
	case <-ctx.Done():
		return newError(CodeExecutorFailed, "context canceled during execution", ctx.Err())
	default:
		return nil
	}
}
