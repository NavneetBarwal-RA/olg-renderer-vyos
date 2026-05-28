package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/routerarchitects/olg-renderer-vyos/internal/vyos"
)

const (
	defaultApplyLockPath  = "/run/lock/olg-vyos-apply.lock"
	defaultCleanupTimeout = 5 * time.Second
	defaultLockRetryDelay = 10 * time.Millisecond
)

type vyosSessionRunner interface {
	Begin(ctx context.Context) (vyos.Session, error)
}

type applyLocker interface {
	Lock(ctx context.Context) (func() error, error)
}

type defaultExecutor struct {
	runner vyosSessionRunner
	locker applyLocker
}

func newDefaultExecutor() Executor {
	return &defaultExecutor{
		runner: vyos.NewCLIShellRunner(),
		locker: fileApplyLocker{path: defaultApplyLockPath},
	}
}

func newDefaultExecutorWithRunner(runner vyosSessionRunner) *defaultExecutor {
	return &defaultExecutor{runner: runner, locker: noopApplyLocker{}}
}

func newDefaultExecutorWithRunnerAndLocker(runner vyosSessionRunner, locker applyLocker) *defaultExecutor {
	return &defaultExecutor{runner: runner, locker: locker}
}

func (e *defaultExecutor) Execute(ctx context.Context, plan Plan) (result ExecutionResult, err error) {
	if err := checkExecutionContext(ctx); err != nil {
		return result, err
	}
	if e == nil || e.runner == nil {
		return result, newError(CodeExecutorFailed, "vyos executor is not initialized", nil)
	}

	locker := e.locker
	if locker == nil {
		locker = noopApplyLocker{}
	}
	release, err := locker.Lock(ctx)
	if err != nil {
		return result, newError(CodeExecutorFailed, "failed to acquire VyOS apply lock", err)
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; lock release failed: %v", err, releaseErr)
				return
			}
			err = newError(CodeExecutorFailed, "failed to release VyOS apply lock", releaseErr)
		}
	}()

	session, err := e.runner.Begin(ctx)
	if err != nil {
		return result, newError(CodeExecutorFailed, "failed to begin VyOS configuration session", err)
	}

	committed := false
	discarded := false
	defer func() {
		cleanupCtx, cancel := cleanupContext(ctx)
		defer cancel()
		// Any pre-commit exit path, including context cancellation after
		// delete/set mutations, must discard the candidate session before
		// teardown.
		if !committed && !discarded {
			output, discardErr := session.Discard(cleanupCtx)
			result.DiscardOutput = appendOutput(result.DiscardOutput, output)
			discarded = true
			if discardErr != nil && err != nil {
				err = fmt.Errorf("%w; discard failed: %v", err, discardErr)
			} else if discardErr != nil {
				err = newError(CodeExecutorFailed, "discard failed during cleanup", discardErr)
			}
		}

		output, closeErr := session.Close(cleanupCtx)
		if closeErr == nil {
			return
		}
		closeErr = withOptionalOutput(closeErr, output)
		if err != nil {
			err = fmt.Errorf("%w; close failed: %v", err, closeErr)
			return
		}
		if result.Applied {
			err = newError(CodeExecutorFailed, "session close failed after successful commit", closeErr)
			return
		}
		err = newError(CodeExecutorFailed, "session close failed", closeErr)
	}()

	for _, command := range plan.DeleteCommands {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		output, deleteErr := session.Delete(ctx, command)
		result.DeleteOutput = appendOutput(result.DeleteOutput, output)
		if deleteErr != nil {
			if isMissingDeleteOutput(output) {
				continue
			}
			if isContextError(deleteErr) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", deleteErr)
			}
			result.DiscardOutput, err = discardAfterFailure(ctx, session, deleteErr)
			discarded = true
			return result, newError(CodeDeleteFailed, fmt.Sprintf("delete command failed: %s", command), withOptionalOutput(err, output))
		}
	}

	for _, command := range plan.SetCommands {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		output, setErr := session.Set(ctx, command)
		result.SetOutput = appendOutput(result.SetOutput, output)
		if setErr != nil {
			if isContextError(setErr) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", setErr)
			}
			result.DiscardOutput, err = discardAfterFailure(ctx, session, setErr)
			discarded = true
			return result, newError(CodeSetFailed, fmt.Sprintf("set command failed: %s", command), withOptionalOutput(err, output))
		}
	}

	if plan.Commit {
		if err = checkExecutionContext(ctx); err != nil {
			return result, err
		}
		output, commitErr := session.Commit(ctx)
		result.CommitOutput = output
		if commitErr != nil {
			if isContextError(commitErr) {
				return result, newError(CodeExecutorFailed, "context canceled during execution", commitErr)
			}
			result.DiscardOutput, err = discardAfterFailure(ctx, session, commitErr)
			discarded = true
			return result, newError(CodeCommitFailed, "commit failed", withOptionalOutput(err, output))
		}
	}

	committed = true
	result.Applied = true
	if !plan.Save {
		return result, nil
	}

	if err = checkExecutionContext(ctx); err != nil {
		result.Applied = true
		return result, err
	}
	output, saveErr := session.Save(ctx)
	result.SaveOutput = output
	if saveErr != nil {
		if isContextError(saveErr) {
			return result, newError(CodeExecutorFailed, "context canceled during execution", saveErr)
		}
		result.Saved = false
		return result, newError(CodeSaveFailed, "save failed after successful commit", withOptionalOutput(saveErr, output))
	}
	result.Saved = true
	return result, nil
}

func discardAfterFailure(ctx context.Context, session vyos.Session, primary error) (string, error) {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	output, discardErr := session.Discard(cleanupCtx)
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

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func isMissingDeleteOutput(output string) bool {
	normalized := strings.ToLower(output)
	return strings.Contains(normalized, "nothing to delete") ||
		strings.Contains(normalized, "specified node does not exist") ||
		strings.Contains(normalized, "does not exist")
}

func appendOutput(existing, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return next
	}
	return existing + "\n" + next
}

func withOptionalOutput(err error, output string) error {
	detail := strings.TrimSpace(output)
	if err == nil || detail == "" {
		return err
	}
	var commandErr *vyos.CommandError
	if errors.As(err, &commandErr) && strings.TrimSpace(commandErr.Output) != "" {
		return err
	}
	return fmt.Errorf("%w (output: %s)", err, detail)
}

func cleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	return context.WithTimeout(base, defaultCleanupTimeout)
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

type noopApplyLocker struct{}

func (noopApplyLocker) Lock(ctx context.Context) (func() error, error) {
	return func() error { return nil }, nil
}

type fileApplyLocker struct {
	path string
}

func (l fileApplyLocker) Lock(ctx context.Context) (func() error, error) {
	if ctx == nil {
		return nil, newError(CodeExecutorFailed, "context is required", nil)
	}
	if strings.TrimSpace(l.path) == "" {
		return nil, fmt.Errorf("apply lock path is required")
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, err
	}
	_ = file.Chmod(0666)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				closeErr := file.Close()
				if unlockErr != nil {
					return unlockErr
				}
				return closeErr
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(defaultLockRetryDelay):
		}
	}
}
