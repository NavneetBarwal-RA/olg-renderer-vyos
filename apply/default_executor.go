package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/routerarchitects/olg-renderer-vyos/internal/vyos"
)

type vyosRunner interface {
	Begin(ctx context.Context) (string, error)
	End(ctx context.Context) (string, error)
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

func (e *defaultExecutor) Execute(ctx context.Context, plan Plan) (result ExecutionResult, err error) {
	if err := checkExecutionContext(ctx); err != nil {
		return result, err
	}
	if e == nil || e.runner == nil {
		return result, newError(CodeExecutorFailed, "vyos executor is not initialized", nil)
	}

	if _, err = e.runner.Begin(ctx); err != nil {
		return result, newError(CodeExecutorFailed, "failed to begin VyOS configuration session", err)
	}

	defer func() {
		output, endErr := e.runner.End(ctx)
		if endErr == nil {
			return
		}
		endDetail := strings.TrimSpace(output)
		if endDetail != "" {
			endErr = fmt.Errorf("%w (output: %s)", endErr, endDetail)
		}
		if err != nil {
			err = fmt.Errorf("%w; end failed: %v", err, endErr)
			return
		}
		if result.Applied {
			err = newError(CodeExecutorFailed, "session end failed after successful commit", endErr)
			return
		}
		err = newError(CodeExecutorFailed, "session end failed", endErr)
	}()

	for _, command := range plan.DeleteCommands {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		if _, err = e.runner.RunConfigCommand(ctx, command); err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, err)
			return result, newError(CodeDeleteFailed, fmt.Sprintf("delete command failed: %s", command), err)
		}
	}

	for _, command := range plan.SetCommands {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		if _, err = e.runner.RunConfigCommand(ctx, command); err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, err)
			return result, newError(CodeSetFailed, fmt.Sprintf("set command failed: %s", command), err)
		}
	}

	if plan.Commit {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		output, commitErr := e.runner.Commit(ctx)
		result.CommitOutput = output
		if commitErr != nil {
			if isContextError(commitErr) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", commitErr)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, commitErr)
			return result, newError(CodeCommitFailed, "commit failed", err)
		}
	}

	result.Applied = true
	if !plan.Save {
		return result, nil
	}

	if err = checkExecutionContext(ctx); err != nil {
		result.Applied = true
		return result, err
	}
	output, saveErr := e.runner.Save(ctx)
	result.SaveOutput = output
	if saveErr != nil {
		if isContextError(saveErr) {
			return result, newError(CodeExecutorFailed, "context canceled during execution", saveErr)
		}
		result.Saved = false
		return result, newError(CodeSaveFailed, "save failed after successful commit", saveErr)
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
