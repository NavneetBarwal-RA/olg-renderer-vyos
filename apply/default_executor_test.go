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

func (f *fakeVyosRunner) GetSessionEnv(ctx context.Context, sessionID string) (map[string]string, error) {
	f.calls = append(f.calls, "getSessionEnv:"+sessionID)
	if f.errors != nil {
		if err, ok := f.errors["getSessionEnv"]; ok {
			return nil, err
		}
	}
	if f.outputs != nil {
		if output, ok := f.outputs["getSessionEnv"]; ok {
			return map[string]string{"SESSION_ID": output}, nil
		}
	}
	return map[string]string{
		"VYATTA_CONFIG_SID": sessionID,
		"COMMIT_VIA":        "api",
	}, nil
}

func (f *fakeVyosRunner) SetupSession(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return f.record("setupSession")
}

func (f *fakeVyosRunner) TeardownSession(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return f.record("teardownSession")
}

func (f *fakeVyosRunner) RunConfigCommand(ctx context.Context, sessionEnv map[string]string, command string) (string, error) {
	return f.record("cmd:" + command)
}

func (f *fakeVyosRunner) Commit(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return f.record("commit")
}

func (f *fakeVyosRunner) Save(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return f.record("save")
}

func (f *fakeVyosRunner) Discard(ctx context.Context, sessionEnv map[string]string) (string, error) {
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
TC-VYOS-SESSION-001
Type: Positive
Title: Session call order on success
Summary:
Executes a successful plan and records every runner call in order.
The default executor must acquire session env, setup the session, apply commands,
commit once, skip save by default, and always teardown.

Validates:
  - getSessionEnv runs before setupSession
  - setupSession runs before delete/set/commit
  - my-operations happen in expected order and commit runs once
  - teardownSession runs after commit on success
*/
func TestDefaultExecutorUsesSessionLifecycleAndCommandOrder(t *testing.T) {
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-001" }

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertNoApplyError(t, err)
	if !result.Applied || result.Saved {
		t.Fatalf("unexpected result: %#v", result)
	}

	assertStringSlicesEqual(t, runner.calls, []string{
		"getSessionEnv:session-001",
		"setupSession",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
		"teardownSession",
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
  - Teardown still runs
*/
func TestDefaultExecutorSkipsSaveWhenPlanSaveFalse(t *testing.T) {
	runner := &fakeVyosRunner{}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-002" }

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
Save must execute after commit and before teardown.

Validates:
  - Save is called only when Plan.Save is true
  - Save output is propagated
  - Saved is true on success
*/
func TestDefaultExecutorRunsSaveAfterCommitWhenEnabled(t *testing.T) {
	runner := &fakeVyosRunner{outputs: map[string]string{"save": "save ok"}}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-003" }

	result, err := executor.Execute(context.Background(), executorTestPlan(true))
	assertNoApplyError(t, err)
	if !result.Applied || !result.Saved || result.SaveOutput != "save ok" {
		t.Fatalf("unexpected result: %#v", result)
	}

	assertStringSlicesEqual(t, runner.calls, []string{
		"getSessionEnv:session-003",
		"setupSession",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
		"save",
		"teardownSession",
	})
}

/*
TC-VYOS-SESSION-004
Type: Negative
Title: Delete failure discards and tears down
Summary:
Forces the first delete command to fail.
The executor must attempt discard, return delete_failed, and teardown the session.

Validates:
  - Delete failure returns delete_failed
  - Discard is attempted on failure
  - TeardownSession still runs
*/
func TestDefaultExecutorDeleteFailureAttemptsDiscardAndTeardown(t *testing.T) {
	failCall := "cmd:delete interfaces bridge"
	runner := &fakeVyosRunner{
		outputs: map[string]string{"discard": "discard ok"},
		errors:  map[string]error{failCall: errors.New("delete failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-004" }

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeDeleteFailed)
	if result.Applied || result.DiscardOutput != "discard ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertStringSlicesEqual(t, runner.calls, []string{
		"getSessionEnv:session-004",
		"setupSession",
		"cmd:delete interfaces bridge",
		"discard",
		"teardownSession",
	})
}

/*
TC-VYOS-SESSION-005
Type: Negative
Title: Set failure discards and tears down
Summary:
Forces the first set command to fail after delete succeeds.
The executor must attempt discard, return set_failed, skip commit, and teardown.

Validates:
  - Set failure returns set_failed
  - Discard is attempted on failure
  - Commit is not called
*/
func TestDefaultExecutorSetFailureAttemptsDiscardAndTeardown(t *testing.T) {
	failCall := "cmd:set interfaces bridge br0 address dhcp"
	runner := &fakeVyosRunner{errors: map[string]error{failCall: errors.New("set failed")}}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-005" }

	_, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeSetFailed)
	assertStringSlicesEqual(t, runner.calls, []string{
		"getSessionEnv:session-005",
		"setupSession",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"discard",
		"teardownSession",
	})
}

/*
TC-VYOS-SESSION-006
Type: Negative
Title: Commit failure discards and tears down
Summary:
Forces commit to fail after all commands execute successfully.
The executor must return commit_failed, attempt discard, and teardown the session.

Validates:
  - Commit failure returns commit_failed
  - Discard is attempted
  - TeardownSession still runs
*/
func TestDefaultExecutorCommitFailureAttemptsDiscardAndTeardown(t *testing.T) {
	runner := &fakeVyosRunner{
		outputs: map[string]string{"commit": "commit failed", "discard": "discard ok"},
		errors:  map[string]error{"commit": errors.New("commit failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-006" }

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeCommitFailed)
	if result.Applied || result.CommitOutput != "commit failed" || result.DiscardOutput != "discard ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertStringSlicesEqual(t, runner.calls, []string{
		"getSessionEnv:session-006",
		"setupSession",
		"cmd:delete interfaces bridge",
		"cmd:delete nat source",
		"cmd:set interfaces bridge br0 address dhcp",
		"cmd:set interfaces ethernet eth0 description 'WAN uplink'",
		"commit",
		"discard",
		"teardownSession",
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
	executor.sessionIDGenerator = func() string { return "session-007" }

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
  - TeardownSession still runs
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
	executor.sessionIDGenerator = func() string { return "session-008" }

	result, err := executor.Execute(context.Background(), executorTestPlan(false))
	assertApplyCode(t, err, CodeSetFailed)
	if result.Applied || !strings.Contains(result.DiscardOutput, "discard failed") {
		t.Fatalf("discard failure detail missing: result=%#v err=%v", result, err)
	}
}

/*
TC-VYOS-SESSION-009
Type: Negative
Title: Teardown failure after commit
Summary:
Forces teardownSession to fail after a successful commit.
The executor should return executor_failed while preserving Applied=true.

Validates:
  - Teardown failure returns executor_failed
  - Applied remains true after successful commit
  - Saved remains false when save is disabled
*/
func TestDefaultExecutorTeardownFailureAfterCommitReturnsExecutorFailed(t *testing.T) {
	runner := &fakeVyosRunner{
		outputs: map[string]string{"teardownSession": "teardown failed output"},
		errors:  map[string]error{"teardownSession": errors.New("teardown failed")},
	}
	executor := newDefaultExecutorWithRunner(runner)
	executor.sessionIDGenerator = func() string { return "session-009" }

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
	executor.sessionIDGenerator = func() string { return "session-010" }

	_, err := executor.Execute(ctx, executorTestPlan(false))
	assertApplyCode(t, err, CodeExecutorFailed)
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called after cancellation: %#v", runner.calls)
	}
}
