package apply

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

/*
TC-APPLY-ENGINE-001
Type: Positive
Title: Executor receives prepared plan
Summary:
Runs Apply with a fake executor and verifies the exact structured plan.
The executor should receive separate delete and set command slices.

Validates:
  - Apply invokes executor once
  - Executor receives structured command lists
  - Commit and save flags match the prepared plan
*/
func TestApplyInvokesExecutorWithPreparedPlan(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	_, err = engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)

	if exec.calls != 1 {
		t.Fatalf("expected one executor call, got %d", exec.calls)
	}
	want := Plan{
		Target:         "vyos",
		ConfigUUID:     "cfg-123",
		DeleteCommands: []string{"delete interfaces bridge", "delete nat source"},
		SetCommands: []string{
			"set interfaces bridge br0 address dhcp",
			"set interfaces ethernet eth0 description 'WAN uplink'",
			"set nat source rule 100 translation address masquerade",
		},
		Commit: true,
		Save:   false,
	}
	assertPlanEqual(t, exec.plans[0], want)
}

/*
TC-APPLY-ENGINE-002
Type: Positive
Title: Structured result
Summary:
Configures the fake executor to return commit output and success.
Apply should echo plan commands and execution outputs in the result.

Validates:
  - Result preserves target and ConfigUUID
  - Result echoes delete and set commands
  - Commit output is returned
*/
func TestApplyReturnsStructuredResult(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true, CommitOutput: "commit ok"}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)

	if !result.Applied || result.Saved {
		t.Fatalf("unexpected applied/saved flags: %#v", result)
	}
	if result.Target != "vyos" || result.ConfigUUID != "cfg-123" {
		t.Fatalf("metadata mismatch in result: %#v", result)
	}
	if result.CommitOutput != "commit ok" {
		t.Fatalf("unexpected commit output: %q", result.CommitOutput)
	}
	assertStringSlicesEqual(t, result.DeleteCommands, []string{"delete interfaces bridge", "delete nat source"})
}

/*
TC-APPLY-ENGINE-003
Type: Negative
Title: Empty commands avoid executor
Summary:
Calls Apply with blank DesiredCommands and a configured executor.
The engine must reject input before executor invocation.

Validates:
  - Empty DesiredCommands return empty_desired_commands
  - Executor is not called
  - No partial command result is returned
*/
func TestApplyRejectsEmptyDesiredCommandsWithoutExecutorCall(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(" \n "))
	assertApplyCode(t, err, CodeEmptyDesiredCommands)
	if exec.calls != 0 {
		t.Fatalf("executor was called for empty commands")
	}
	if len(result.DeleteCommands) != 0 || len(result.SetCommands) != 0 {
		t.Fatalf("empty commands returned partial result: %#v", result)
	}
}

/*
TC-APPLY-ENGINE-004
Type: Negative
Title: Validation failure avoids executor
Summary:
Calls Apply with an unsupported set path and a configured executor.
Validation should stop execution before any target interaction.

Validates:
  - Unsupported command returns invalid_command
  - Executor is not called
  - Apply returns a zero result on planning failure
*/
func TestApplyDoesNotInvokeExecutorWhenValidationFails(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput("set system host-name router1\n"))
	assertApplyCode(t, err, CodeInvalidCommand)
	if exec.calls != 0 {
		t.Fatalf("executor was called for invalid command")
	}
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("expected zero result, got %#v", result)
	}
}

/*
TC-APPLY-ENGINE-005
Type: Positive
Title: Save disabled by default
Summary:
Runs Apply with default engine options.
The executor plan should not request save and the result should not report saved.

Validates:
  - Plan.Save defaults to false
  - Result.Saved is false by default
  - Apply still commits through executor semantics
*/
func TestApplySaveDisabledByDefault(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true, Saved: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)
	if exec.plans[0].Save {
		t.Fatalf("default plan requested save")
	}
	if result.Saved {
		t.Fatalf("result reported saved with save disabled")
	}
}

/*
TC-APPLY-ENGINE-006
Type: Positive
Title: Save enabled by option
Summary:
Runs Apply with save-after-commit enabled.
The save flag should be passed to the executor and reflected in the result.

Validates:
  - WithSaveAfterCommit enables Plan.Save
  - Executor receives save-enabled plan
  - Result.Saved requires executor save success
*/
func TestApplySaveEnabledByOption(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true, Saved: true, SaveOutput: "save ok"}}
	engine, err := New(WithExecutor(exec), WithSaveAfterCommit(true))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)
	if !exec.plans[0].Save || !result.Saved {
		t.Fatalf("save was not enabled through plan/result: plan=%#v result=%#v", exec.plans[0], result)
	}
	if result.SaveOutput != "save ok" {
		t.Fatalf("unexpected save output: %q", result.SaveOutput)
	}
}

/*
TC-APPLY-ENGINE-007
Type: Negative
Title: Nil executor option
Summary:
Attempts to override the default executor with nil.
The constructor should reject nil executors before an engine is returned.

Validates:
  - WithExecutor(nil) is rejected
  - Failure uses invalid_input
  - Default executor cannot be removed with nil
*/
func TestNewRejectsNilExecutorOverride(t *testing.T) {
	_, err := New(WithExecutor(nil))
	assertApplyCode(t, err, CodeInvalidInput)
}

/*
TC-APPLY-ENGINE-008
Type: Negative
Title: Preserve typed executor errors
Summary:
Configures the fake executor to return an apply Error directly.
Apply should preserve the executor's typed code instead of replacing it.

Validates:
  - Typed executor errors are preserved
  - Partial result is returned
  - Applied is forced false on failure
*/
func TestApplyPreservesTypedExecutorError(t *testing.T) {
	exec := &fakeExecutor{
		result: ExecutionResult{Applied: true, DiscardOutput: "discard ok"},
		err:    newError(CodeSetFailed, "set command failed", errors.New("bad command")),
	}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertApplyCode(t, err, CodeSetFailed)
	if result.Applied {
		t.Fatalf("failed executor result reported applied")
	}
	if result.DiscardOutput != "discard ok" {
		t.Fatalf("discard output was not preserved: %#v", result)
	}
}

/*
TC-APPLY-ENGINE-009
Type: Negative
Title: Commit failure partial result
Summary:
Simulates a commit failure from the executor with discard output.
Apply should return the typed commit failure and preserve execution output.

Validates:
  - Commit failure returns commit_failed
  - Partial result includes commands
  - Discard output is preserved
*/
func TestApplyReturnsPartialResultOnCommitFailure(t *testing.T) {
	exec := &fakeExecutor{
		result: ExecutionResult{Applied: false, CommitOutput: "commit failed", DiscardOutput: "discard ok"},
		err:    newError(CodeCommitFailed, "commit failed", nil),
	}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertApplyCode(t, err, CodeCommitFailed)
	if result.Applied {
		t.Fatalf("commit failure reported applied")
	}
	if result.CommitOutput != "commit failed" || result.DiscardOutput != "discard ok" {
		t.Fatalf("execution outputs not preserved: %#v", result)
	}
	if len(result.DeleteCommands) != 2 || len(result.SetCommands) != 3 {
		t.Fatalf("partial result missing commands: %#v", result)
	}
}

/*
TC-APPLY-ENGINE-010
Type: Positive
Title: ConfigUUID is metadata only
Summary:
Applies the same ConfigUUID twice with a fake executor.
The engine should not perform duplicate detection or skip executor calls.

Validates:
  - ConfigUUID is preserved in results
  - Same UUID does not skip work
  - Executor call count increments for each Apply call
*/
func TestApplyPreservesConfigUUIDWithoutDuplicateDetection(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	for i := 0; i < 2; i++ {
		result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
		assertNoApplyError(t, err)
		if result.ConfigUUID != "cfg-123" {
			t.Fatalf("unexpected config uuid: %q", result.ConfigUUID)
		}
	}
	if exec.calls != 2 {
		t.Fatalf("expected duplicate UUID to execute twice, got %d calls", exec.calls)
	}
}

/*
TC-APPLY-ENGINE-011
Type: Positive
Title: New installs default executor
Summary:
Constructs an engine without options and inspects the configured backend.
Normal production callers should be able to call Apply without supplying an executor.

Validates:
  - New configures a default executor
  - The default executor is VyOS-backed
  - WithExecutor is not required for construction
*/
func TestNewInstallsDefaultExecutor(t *testing.T) {
	engine, err := New()
	assertNoApplyError(t, err)
	if _, ok := engine.executor.(*defaultExecutor); !ok {
		t.Fatalf("expected default executor, got %T", engine.executor)
	}
}

/*
TC-APPLY-API-001
Type: Positive
Title: Input has no DryRun field
Summary:
Reflects over the public Input type to document the API surface.
Dry-run style behavior must use Prepare instead of an input flag.

Validates:
  - Input exposes Target
  - Input exposes ConfigUUID and DesiredCommands
  - Input does not expose DryRun
*/
func TestInputDoesNotExposeDryRunField(t *testing.T) {
	inputType := reflect.TypeOf(Input{})
	if _, ok := inputType.FieldByName("Target"); !ok {
		t.Fatalf("Input.Target is missing")
	}
	if _, ok := inputType.FieldByName("ConfigUUID"); !ok {
		t.Fatalf("Input.ConfigUUID is missing")
	}
	if _, ok := inputType.FieldByName("DesiredCommands"); !ok {
		t.Fatalf("Input.DesiredCommands is missing")
	}
	if _, ok := inputType.FieldByName("DryRun"); ok {
		t.Fatalf("Input unexpectedly exposes DryRun")
	}
}

/*
TC-APPLY-API-002
Type: Mixed
Title: Info and error helpers
Summary:
Checks package metadata and the public typed error helper functions.
The helpers are part of the caller-facing API for stable error handling.

Validates:
  - Info reports apply package defaults
  - CodeOf extracts apply error codes
  - IsCode matches wrapped apply errors
*/
func TestInfoAndErrorHelpers(t *testing.T) {
	info := GetInfo()
	if info.Name != "olg-renderer-vyos/apply" || info.Target != "vyos" {
		t.Fatalf("unexpected info: %#v", info)
	}
	assertStringSlicesEqual(t, info.DefaultResetRoots, []string{"interfaces bridge", "nat source"})
	if info.SaveDefault {
		t.Fatalf("save default should be false")
	}

	err := newError(CodeCommitFailed, "commit failed", errors.New("boom"))
	if CodeOf(err) != CodeCommitFailed || !IsCode(err, CodeCommitFailed) {
		t.Fatalf("error helpers did not match commit_failed: %v", err)
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("error text should be useful")
	}
}
