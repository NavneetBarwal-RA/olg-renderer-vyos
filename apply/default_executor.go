package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/routerarchitects/olg-renderer-vyos/internal/vyos"
)

type vyosRunner interface {
	GetSessionEnv(ctx context.Context, sessionID string) (map[string]string, error)
	SetupSession(ctx context.Context, sessionEnv map[string]string) (string, error)
	TeardownSession(ctx context.Context, sessionEnv map[string]string) (string, error)
	RunConfigCommand(ctx context.Context, sessionEnv map[string]string, command string) (string, error)
	Commit(ctx context.Context, sessionEnv map[string]string) (string, error)
	Save(ctx context.Context, sessionEnv map[string]string) (string, error)
	Discard(ctx context.Context, sessionEnv map[string]string) (string, error)
}

type defaultExecutor struct {
	runner             vyosRunner
	sessionIDGenerator func() string
}

var sessionCounter uint64

func newDefaultExecutor() Executor {
	return &defaultExecutor{
		runner:             vyos.NewCLIShellRunner(),
		sessionIDGenerator: defaultSessionID,
	}
}

func newDefaultExecutorWithRunner(runner vyosRunner) *defaultExecutor {
	return &defaultExecutor{
		runner:             runner,
		sessionIDGenerator: defaultSessionID,
	}
}

func defaultSessionID() string {
	next := atomic.AddUint64(&sessionCounter, 1)
	return fmt.Sprintf("olg-apply-%d-%d", time.Now().UTC().UnixNano(), next)
}

func (e *defaultExecutor) Execute(ctx context.Context, plan Plan) (result ExecutionResult, err error) {
	if err := checkExecutionContext(ctx); err != nil {
		return result, err
	}
	if e == nil || e.runner == nil {
		return result, newError(CodeExecutorFailed, "vyos executor is not initialized", nil)
	}
	if e.sessionIDGenerator == nil {
		e.sessionIDGenerator = defaultSessionID
	}

	sessionID := strings.TrimSpace(e.sessionIDGenerator())
	if sessionID == "" {
		sessionID = defaultSessionID()
	}

	sessionEnv, err := e.runner.GetSessionEnv(ctx, sessionID)
	if err != nil {
		return result, newError(CodeExecutorFailed, "failed to get VyOS session environment", err)
	}
	if _, err = e.runner.SetupSession(ctx, sessionEnv); err != nil {
		return result, newError(CodeExecutorFailed, "failed to set up VyOS session", err)
	}

	defer func() {
		output, teardownErr := e.runner.TeardownSession(ctx, sessionEnv)
		if teardownErr == nil {
			return
		}
		teardownDetail := strings.TrimSpace(output)
		if teardownDetail != "" {
			teardownErr = fmt.Errorf("%w (output: %s)", teardownErr, teardownDetail)
		}
		if err != nil {
			err = fmt.Errorf("%w; teardownSession failed: %v", err, teardownErr)
			return
		}
		if result.Applied {
			err = newError(CodeExecutorFailed, "session teardown failed after successful commit", teardownErr)
			return
		}
		err = newError(CodeExecutorFailed, "session teardown failed", teardownErr)
	}()

	for _, command := range plan.DeleteCommands {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		if _, err = e.runner.RunConfigCommand(ctx, sessionEnv, command); err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, sessionEnv, err)
			return result, newError(CodeDeleteFailed, fmt.Sprintf("delete command failed: %s", command), err)
		}
	}

	for _, command := range plan.SetCommands {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		if _, err = e.runner.RunConfigCommand(ctx, sessionEnv, command); err != nil {
			if isContextError(err) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", err)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, sessionEnv, err)
			return result, newError(CodeSetFailed, fmt.Sprintf("set command failed: %s", command), err)
		}
	}

	if plan.Commit {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		output, commitErr := e.runner.Commit(ctx, sessionEnv)
		result.CommitOutput = output
		if commitErr != nil {
			if isContextError(commitErr) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", commitErr)
			}
			result.DiscardOutput, err = e.discardAfterFailure(ctx, sessionEnv, commitErr)
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
	output, saveErr := e.runner.Save(ctx, sessionEnv)
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

func (e *defaultExecutor) discardAfterFailure(ctx context.Context, sessionEnv map[string]string, primary error) (string, error) {
	output, discardErr := e.runner.Discard(ctx, sessionEnv)
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
