package apply

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeVyosRunner struct {
	calls   []string
	outputs map[string]string
	errors  map[string]error
}

func (f *fakeVyosRunner) Configure(ctx context.Context) (string, error) {
	return f.record("configure")
}

func (f *fakeVyosRunner) RunConfigCommand(ctx context.Context, command string) (string, error) {
	return f.record("cmd:" + command)
}

func (f *fakeVyosRunner) Commit(ctx context.Context) (string, error) {
	return f.record("commit")
}

func (f *fakeVyosRunner) Save(ctx context.Context) (string, error) {
	return f.record("save")
}

func (f *fakeVyosRunner) Discard(ctx context.Context) (string, error) {
	return f.record("discard")
}

func (f *fakeVyosRunner) record(call string) (string, error) {
	f.calls = append(f.calls, call)
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
TC-VYOS-EXECUTOR-001
Type: Positive
Title: Configure transaction order
Summary:
Runs the default executor with a fake VyOS runner.
Successful execution should enter configure, apply deletes, apply sets, and commit once.

Validates:
  - Configure runs first
  - Delete and set commands keep their order
  - Commit runs once after commands
*/
func TestDefaultExecutorRunsConfigureCommandsAndCommitInOrder(t *testing.T) {
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertNoApplyError(t, err)
	if !result.Applied || result.Saved {
		t.Fatalf("unexpected result: %#v", result)
	}

	assertStringSlicesEqual(t, runner.calls, []string{
		"configure",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
	})
}

/*
TC-VYOS-EXECUTOR-002
Type: Positive
Title: Save skipped by default
Summary:
Executes a plan with Save disabled.
The executor should commit the candidate configuration without calling save.

Validates:
  - Save is not called when Plan.Save is false
  - Applied is true after commit
  - Saved remains false
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
			t.Fatalf("save was called with Plan.Save=false")
		}
	}
}

/*
TC-VYOS-EXECUTOR-003
Type: Positive
Title: Save after commit
Summary:
Executes a plan with Save enabled.
The executor should call save only after a successful commit.

Validates:
  - Save runs after commit
  - Save output is preserved
  - Result.Saved is true on save success
*/
func TestDefaultExecutorRunsSaveAfterCommitWhenEnabled(t *testing.T) {
	runner := &fakeVyosRunner{outputs: map[string]string{"save": "save ok"}}
	executor := newDefaultExecutorWithRunner(runner)

	result, err := executor.Execute(context.Background(), executorTestPlan(true))
	assertNoApplyError(t, err)
	if !result.Applied || !result.Saved || result.SaveOutput != "save ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if runner.calls[len(runner.calls)-1] != "save" {
		t.Fatalf("save did not run last: %#v", runner.calls)
	}
}

/*
TC-VYOS-EXECUTOR-004
Type: Negative
Title: Delete failure discards
Summary:
Forces the first delete command to fail.
The executor should attempt discard and return delete_failed as the primary code.

Validates:
  - Delete failure returns delete_failed
  - Discard is attempted
  - Applied remains false
*/
func TestDefaultExecutorDeleteFailureAttemptsDiscard(t *testing.T) {
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
	if runner.calls[len(runner.calls)-1] != "discard" {
		t.Fatalf("discard was not attempted: %#v", runner.calls)
	}
}

/*
TC-VYOS-EXECUTOR-005
Type: Negative
Title: Set failure discards
Summary:
Forces a set command to fail after delete commands have succeeded.
The executor should discard the candidate and return set_failed.

Validates:
  - Set failure returns set_failed
  - Discard is attempted
  - Commit is not called
*/
func TestDefaultExecutorSetFailureAttemptsDiscard(t *testing.T) {
	failCall := "cmd:set interfaces bridge br0 address dhcp"
	runner := &fakeVyosRunner{errors: map[string]error{failCall: errors.New("set failed")}}
	executor := newDefaultExecutorWithRunner(runner)

	_, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeSetFailed)
	for _, call := range runner.calls {
		if call == "commit" {
			t.Fatalf("commit ran after set failure: %#v", runner.calls)
		}
	}
	if runner.calls[len(runner.calls)-1] != "discard" {
		t.Fatalf("discard was not attempted: %#v", runner.calls)
	}
}

/*
TC-VYOS-EXECUTOR-006
Type: Negative
Title: Commit failure discards
Summary:
Forces commit to fail after all commands have been applied.
The executor should attempt discard and preserve commit and discard outputs.

Validates:
  - Commit failure returns commit_failed
  - Discard is attempted
  - Commit and discard output are preserved
*/
func TestDefaultExecutorCommitFailureAttemptsDiscard(t *testing.T) {
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
}

/*
TC-VYOS-EXECUTOR-007
Type: Negative
Title: Save failure after commit
Summary:
Forces save to fail after a successful commit.
The executor should keep Applied true because runtime config was committed.

Validates:
  - Save failure returns save_failed
  - Applied remains true
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
TC-VYOS-EXECUTOR-008
Type: Negative
Title: Discard failure preserves primary code
Summary:
Forces a set failure and a discard failure.
The returned error should keep set_failed as the primary code while exposing discard detail.

Validates:
  - Primary failure code is preserved
  - Discard failure detail is retained
  - Applied remains false
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
TC-VYOS-EXECUTOR-009
Type: Negative
Title: Context cancellation stops execution
Summary:
Runs the executor with an already canceled context.
No configure or configuration commands should be attempted.

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
TC-VYOS-EXECUTOR-010
Type: Positive
Title: Commands remain separate operations
Summary:
Runs a plan containing multiple delete and set commands.
The fake runner should see each command as its own operation.

Validates:
  - Commands are not joined into one string
  - Delete commands remain separate
  - Set commands remain separate
*/
func TestDefaultExecutorPreservesCommandBoundaries(t *testing.T) {
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)

	_, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertNoApplyError(t, err)

	commandCalls := 0
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "cmd:") {
			commandCalls++
			if strings.Contains(call, "\n") {
				t.Fatalf("command operation contains joined commands: %q", call)
			}
		}
	}
	if commandCalls != 4 {
		t.Fatalf("expected four separate command operations, got %d: %#v", commandCalls, runner.calls)
	}
}
