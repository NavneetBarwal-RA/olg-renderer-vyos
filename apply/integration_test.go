package apply

import (
	"context"
	"testing"
)

/*
TC-APPLY-INTEGRATION-001
Type: Positive
Title: First-time configure apply
Summary:
Exercises parser, planner, engine, and executor boundary together.
A valid rendered command payload should produce reset deletes plus set commands.

Validates:
  - Default reset roots are executed
  - Rendered set commands are passed through
  - Apply reports success through fake executor
*/
func TestApplyIntegrationFirstTimeConfigurePlanAndExecute(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true, CommitOutput: "commit ok"}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	result, err := engine.Apply(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)

	if !result.Applied || exec.calls != 1 {
		t.Fatalf("expected successful single apply, result=%#v calls=%d", result, exec.calls)
	}
	assertStringSlicesEqual(t, exec.plans[0].DeleteCommands, []string{"delete interfaces bridge", "delete nat source"})
	if len(exec.plans[0].SetCommands) != 3 {
		t.Fatalf("unexpected set command count: %#v", exec.plans[0].SetCommands)
	}
}

/*
TC-APPLY-INTEGRATION-002
Type: Positive
Title: NAT stale rule reset
Summary:
Applies a desired payload with only the current NAT rule.
The plan still deletes nat source so stale cloud-owned NAT rules are removed.

Validates:
  - nat source reset is present
  - Current NAT set command is preserved
  - Apply succeeds through fake executor
*/
func TestApplyIntegrationNATRuleRemovalUsesNatSourceReset(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	commands := stringsJoinLines(
		"set nat source rule 100 outbound-interface name br0",
		"set nat source rule 100 translation address masquerade",
	)
	_, err = engine.Apply(context.Background(), baseInput(commands))
	assertNoApplyError(t, err)

	assertStringSlicesEqual(t, exec.plans[0].DeleteCommands, []string{"delete interfaces bridge", "delete nat source"})
	assertStringSlicesEqual(t, exec.plans[0].SetCommands, []string{
		"set nat source rule 100 outbound-interface name br0",
		"set nat source rule 100 translation address masquerade",
	})
}

/*
TC-APPLY-INTEGRATION-003
Type: Positive
Title: VLAN stale config reset
Summary:
Applies a bridge payload that omits a previously configured VLAN.
The plan deletes interfaces bridge so stale bridge and VIF config is removed.

Validates:
  - interfaces bridge reset is present
  - Current bridge set command is preserved
  - Apply succeeds through fake executor
*/
func TestApplyIntegrationVLANRemovalUsesBridgeReset(t *testing.T) {
	exec := &fakeExecutor{result: ExecutionResult{Applied: true}}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	commands := stringsJoinLines(
		"set interfaces bridge br1 address 192.168.60.1/24",
		"set interfaces bridge br1 member interface eth1",
	)
	_, err = engine.Apply(context.Background(), baseInput(commands))
	assertNoApplyError(t, err)

	assertStringSlicesEqual(t, exec.plans[0].DeleteCommands, []string{"delete interfaces bridge", "delete nat source"})
	assertStringSlicesEqual(t, exec.plans[0].SetCommands, []string{
		"set interfaces bridge br1 address 192.168.60.1/24",
		"set interfaces bridge br1 member interface eth1",
	})
}

/*
TC-APPLY-INTEGRATION-004
Type: Negative
Title: Commit failure with discard output
Summary:
Simulates an executor commit failure after a valid end-to-end plan.
The result should keep command context and discard output for caller reporting.

Validates:
  - Commit failures return commit_failed
  - Applied remains false
  - Discard output is preserved
*/
func TestApplyIntegrationCommitFailureReturnsTypedErrorAndDiscardOutput(t *testing.T) {
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
	if result.DiscardOutput != "discard ok" {
		t.Fatalf("discard output not preserved: %#v", result)
	}
	if len(result.DeleteCommands) == 0 || len(result.SetCommands) == 0 {
		t.Fatalf("partial result missing command context: %#v", result)
	}
}
