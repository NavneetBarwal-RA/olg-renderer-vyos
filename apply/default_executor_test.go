package apply

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeVyosRunner struct {
	calls              []string
	outputs            map[string]string
	errors             map[string]error
	afterCall          func(string)
	contextCanceled    map[string]bool
	contextHasDeadline map[string]bool
}

func (f *fakeVyosRunner) Begin(ctx context.Context) (string, error) {
	return f.record(ctx, "begin")
}

func (f *fakeVyosRunner) End(ctx context.Context) (string, error) {
	return f.record(ctx, "end")
}

func (f *fakeVyosRunner) RunConfigCommand(ctx context.Context, command string) (string, error) {
	return f.record(ctx, "cmd:"+command)
}

func (f *fakeVyosRunner) Commit(ctx context.Context) (string, error) {
	return f.record(ctx, "commit")
}

func (f *fakeVyosRunner) Save(ctx context.Context) (string, error) {
	return f.record(ctx, "save")
}

func (f *fakeVyosRunner) Discard(ctx context.Context) (string, error) {
	return f.record(ctx, "discard")
}

func (f *fakeVyosRunner) record(ctx context.Context, call string) (string, error) {
	f.calls = append(f.calls, call)
	if f.contextCanceled == nil {
		f.contextCanceled = map[string]bool{}
	}
	if f.contextHasDeadline == nil {
		f.contextHasDeadline = map[string]bool{}
	}
	f.contextCanceled[call] = ctx.Err() != nil
	_, hasDeadline := ctx.Deadline()
	f.contextHasDeadline[call] = hasDeadline
	if f.afterCall != nil {
		f.afterCall(call)
	}
	if f.outputs != nil {
		if output, ok := f.outputs[call]; ok {
			if f.errors != nil {
				return output, f.errors[call]
			}
			return output, nil
		}
	}
	if f.errors != nil {
		if err, ok := f.errors[call]; ok {
			return "", err
		}
	}
	return "", nil
}

func executorTestPlan(save bool) Plan {
	return Plan{
		Target:         "vyos",
		ConfigUUID:     "cfg-123",
		DeleteCommands: []string{"delete interfaces bridge", "delete nat source"},
		SetCommands: []string{
			"set interfaces bridge br0 address dhcp",
			"set interfaces ethernet eth0 description 'WAN uplink'",
		},
		Commit: true,
		Save:   save,
	}
}

/*
TC-VYOS-SESSION-001
Type: Positive
Title: Session call order on success
Summary:
Executes a successful plan and records every runner call in order.
The default executor must begin a wrapper session, apply commands, commit once,
skip save by default, and always end the session.

Validates:
  - begin runs before delete/set/commit
  - Config operations happen in expected order and commit runs once
  - end runs after commit on success
*/
func TestDefaultExecutorUsesSessionLifecycleAndCommandOrder(t *testing.T) {
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertNoApplyError(t, err)
	if !result.Applied || result.Saved {
		t.Fatalf("unexpected result: %#v", result)
	}

	assertStringSlicesEqual(t, runner.calls, []string{
		"begin",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
		"end",
	})
}

/*
TC-VYOS-SESSION-002
Type: Positive
Title: Save disabled by default
Summary:
Runs the default execution plan with save disabled.
The executor should commit successfully and never call save.

Validates:
  - Save is skipped when Plan.Save is false
  - Applied is true after commit
  - End still runs
*/
func TestDefaultExecutorSkipsSaveWhenPlanSaveFalse(t *testing.T) {
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertNoApplyError(t, err)
	if !result.Applied || result.Saved {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, call := range runner.calls {
		if call == "save" {
			t.Fatalf("save was called with Plan.Save=false: %#v", runner.calls)
		}
	}
}

/*
TC-VYOS-SESSION-003
Type: Positive
Title: Save runs after successful commit
Summary:
Runs execution with save enabled and a successful save result.
Save must execute after commit and before end.

Validates:
  - Save is called only when Plan.Save is true
  - Save output is propagated
  - Saved is true on success
*/
func TestDefaultExecutorRunsSaveAfterCommitWhenEnabled(t *testing.T) {
	runner := &fakeVyosRunner{outputs: map[string]string{"save": "save ok"}}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(true))
	assertNoApplyError(t, err)
	if !result.Applied || !result.Saved || result.SaveOutput != "save ok" {
		t.Fatalf("unexpected result: %#v", result)
	}

	assertStringSlicesEqual(t, runner.calls, []string{
		"begin",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
		"save",
		"end",
	})
}

/*
TC-VYOS-SESSION-004
Type: Negative
Title: Delete failure discards and tears down
Summary:
Forces the first delete command to fail.
The executor must attempt discard, return delete_failed, and end the session.

Validates:
  - Delete failure returns delete_failed
  - Discard is attempted on failure
  - End still runs
*/
func TestDefaultExecutorDeleteFailureAttemptsDiscardAndEnd(t *testing.T) {
	failCall := "cmd:delete interfaces bridge"
	runner := &fakeVyosRunner{
		outputs: map[string]string{"discard": "discard ok"},
		errors:  map[string]error{failCall: errors.New("delete failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeDeleteFailed)
	if result.Applied || result.DiscardOutput != "discard ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertStringSlicesEqual(t, runner.calls, []string{
		"begin",
		"cmd:delete interfaces bridge",
		"discard",
		"end",
	})
}

/*
TC-VYOS-SESSION-005
Type: Negative
Title: Set failure discards and tears down
Summary:
Forces the first set command to fail after delete succeeds.
The executor must attempt discard, return set_failed, skip commit, and end.

Validates:
  - Set failure returns set_failed
  - Discard is attempted on failure
  - Commit is not called
*/
func TestDefaultExecutorSetFailureAttemptsDiscardAndEnd(t *testing.T) {
	failCall := "cmd:set interfaces bridge br0 address dhcp"
	runner := &fakeVyosRunner{errors: map[string]error{failCall: errors.New("set failed")}}
	executor := newDefaultExecutorWithRunner(runner)

	_, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeSetFailed)
	assertStringSlicesEqual(t, runner.calls, []string{
		"begin",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"discard",
		"end",
	})
}

/*
TC-VYOS-SESSION-006
Type: Negative
Title: Commit failure discards and tears down
Summary:
Forces commit to fail after all commands execute successfully.
The executor must return commit_failed, attempt discard, and end the session.

Validates:
  - Commit failure returns commit_failed
  - Discard is attempted
  - End still runs
*/
func TestDefaultExecutorCommitFailureAttemptsDiscardAndEnd(t *testing.T) {
	runner := &fakeVyosRunner{
		outputs: map[string]string{"commit": "commit failed", "discard": "discard ok"},
		errors:  map[string]error{"commit": errors.New("commit failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeCommitFailed)
	if result.Applied || result.CommitOutput != "commit failed" || result.DiscardOutput != "discard ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertStringSlicesEqual(t, runner.calls, []string{
		"begin",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
		"discard",
		"end",
	})
}

/*
TC-VYOS-SESSION-007
Type: Negative
Title: Save failure after successful commit
Summary:
Forces save to fail after commit succeeds.
The result must preserve applied=true and saved=false.

Validates:
  - Save failure returns save_failed
  - Applied remains true because commit succeeded
  - Saved remains false
*/
func TestDefaultExecutorSaveFailureReturnsAppliedTrue(t *testing.T) {
	runner := &fakeVyosRunner{
		outputs: map[string]string{"save": "save failed"},
		errors:  map[string]error{"save": errors.New("save failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(true))
	assertApplyCode(t, err, CodeSaveFailed)
	if !result.Applied || result.Saved || result.SaveOutput != "save failed" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

/*
TC-VYOS-SESSION-008
Type: Negative
Title: Discard failure preserves primary code
Summary:
Forces a set failure and a discard failure in the same execution.
The executor should keep set_failed as primary and include discard detail.

Validates:
  - Primary failure code remains set_failed
  - Discard failure detail is included
  - End still runs
*/
func TestDefaultExecutorDiscardFailureDoesNotHidePrimaryCode(t *testing.T) {
	failCall := "cmd:set interfaces bridge br0 address dhcp"
	runner := &fakeVyosRunner{
		errors: map[string]error{
			failCall:  errors.New("set failed"),
			"discard": errors.New("discard failed"),
		},
	}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeSetFailed)
	if result.Applied || !strings.Contains(result.DiscardOutput, "discard failed") {
		t.Fatalf("discard failure detail missing: result=%#v err=%v", result, err)
	}
}

/*
TC-VYOS-SESSION-009
Type: Negative
Title: End failure after commit
Summary:
Forces end to fail after a successful commit.
The executor should return executor_failed while preserving Applied=true.

Validates:
  - End failure returns executor_failed
  - Applied remains true after successful commit
  - Saved remains false when save is disabled
*/
func TestDefaultExecutorEndFailureAfterCommitReturnsExecutorFailed(t *testing.T) {
	runner := &fakeVyosRunner{
		outputs: map[string]string{"end": "end failed output"},
		errors:  map[string]error{"end": errors.New("end failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeExecutorFailed)
	if !result.Applied || result.Saved {
		t.Fatalf("unexpected result: %#v", result)
	}
}

/*
TC-VYOS-SESSION-010
Type: Negative
Title: Context cancellation stops execution early
Summary:
Executes with an already canceled context.
No runner operations should be attempted.

Validates:
  - Canceled context returns executor_failed
  - No runner calls are made
  - Context error is wrapped
*/
func TestDefaultExecutorContextCancellationStopsExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)

	_, err := executor.Execute(ctx, executorTestPlan(false))
	assertApplyCode(t, err, CodeExecutorFailed)
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called after cancellation: %#v", runner.calls)
	}
}

/*
TC-VYOS-SESSION-011
Type: Negative
Title: End cleanup ignores caller cancellation
Summary:
Cancels the caller context after begin succeeds.
The executor should still call end with a non-canceled bounded cleanup context.

Validates:
  - End is attempted after caller cancellation
  - End receives a non-canceled context
  - End cleanup context has a deadline
*/
func TestDefaultExecutorEndUsesNonCanceledCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeVyosRunner{}
	runner.afterCall = func(call string) {
		if call == "begin" {
			cancel()
		}
	}
	executor := newDefaultExecutorWithRunner(runner)

	_, err := executor.Execute(ctx, executorTestPlan(false))
	assertApplyCode(t, err, CodeExecutorFailed)
	if runner.contextCanceled["end"] {
		t.Fatalf("end received canceled caller context: %#v", runner.contextCanceled)
	}
	if !runner.contextHasDeadline["end"] {
		t.Fatalf("end cleanup context did not have a deadline: %#v", runner.contextHasDeadline)
	}
}

/*
TC-VYOS-SESSION-012
Type: Negative
Title: Discard cleanup ignores caller cancellation
Summary:
Cancels the caller context as a set command fails.
Discard should still run with a non-canceled bounded cleanup context.

Validates:
  - Discard is attempted after set failure
  - Discard receives a non-canceled context
  - Discard cleanup context has a deadline
*/
func TestDefaultExecutorDiscardUsesNonCanceledCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	failCall := "cmd:set interfaces bridge br0 address dhcp"
	runner := &fakeVyosRunner{errors: map[string]error{failCall: errors.New("set failed")}}
	runner.afterCall = func(call string) {
		if call == failCall {
			cancel()
		}
	}
	executor := newDefaultExecutorWithRunner(runner)

	_, err := executor.Execute(ctx, executorTestPlan(false))
	assertApplyCode(t, err, CodeSetFailed)
	if runner.contextCanceled["discard"] {
		t.Fatalf("discard received canceled caller context: %#v", runner.contextCanceled)
	}
	if !runner.contextHasDeadline["discard"] {
		t.Fatalf("discard cleanup context did not have a deadline: %#v", runner.contextHasDeadline)
	}
	if runner.contextCanceled["end"] {
		t.Fatalf("end received canceled caller context after discard: %#v", runner.contextCanceled)
	}
	if !runner.contextHasDeadline["end"] {
		t.Fatalf("end cleanup context did not have a deadline: %#v", runner.contextHasDeadline)
	}
}

/*
TC-VYOS-SESSION-013
Type: Positive
Title: Cleanup context timeout is bounded
Summary:
Creates a cleanup context from a canceled parent context.
The cleanup context should ignore caller cancellation but still have a deadline.

Validates:
  - Cleanup context is not immediately canceled
  - Cleanup context has a deadline
  - Cleanup deadline is bounded by defaultCleanupTimeout
*/
func TestCleanupContextIsBounded(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := cleanupContext(parent)
	defer cancel()

	if err := ctx.Err(); err != nil {
		t.Fatalf("cleanup context inherited cancellation: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("cleanup context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > defaultCleanupTimeout {
		t.Fatalf("cleanup deadline outside expected bound: %s", remaining)
	}
}
