package apply

import (
	"context"
	"strings"
	"testing"
)

/*
TC-APPLY-PLANNER-001
Type: Positive
Title: Default deterministic plan
Summary:
Builds a plan from mixed renderer-style interface and NAT commands.
The plan should contain default reset deletes, input-order sets, commit enabled,
and save disabled by default.

Validates:
  - Default delete commands are present
  - Set command order is preserved
  - Commit and save defaults are correct
*/
func TestPrepareReturnsDefaultDeterministicPlan(t *testing.T) {
	engine, err := New()
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)

	assertPlanEqual(t, plan, Plan{
		Target:         "vyos",
		ConfigUUID:     "cfg-123",
		DeleteCommands: defaultDeleteCommands(),
		SetCommands: []string{
			"set interfaces bridge br0 address dhcp",
			"set interfaces ethernet eth0 description 'WAN uplink'",
			"set service ssh port 22",
			"set nat source rule 100 translation address masquerade",
		},
		Commit: true,
		Save:   false,
	})
	if strings.Contains(strings.Join(plan.DeleteCommands, "\n"), "system") {
		t.Fatalf("plan deletes protected system root: %#v", plan.DeleteCommands)
	}
}

/*
TC-APPLY-PLANNER-002
Type: Positive
Title: Custom reset policy
Summary:
Prepares a plan with a caller-supplied allowed reset policy.
The custom policy should determine the delete list completely.

Validates:
  - Custom reset roots are used
  - Caller root order is preserved
  - Plan remains deterministic
*/
func TestPrepareUsesCustomResetPolicy(t *testing.T) {
	engine, err := New(WithResetPolicy(ResetPolicy{ResetRoots: []string{"nat source", "interfaces bridge"}}))
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)
	assertStringSlicesEqual(t, plan.DeleteCommands, []string{"delete nat source", "delete interfaces bridge"})
}

/*
TC-APPLY-PLANNER-003
Type: Negative
Title: Empty desired commands
Summary:
Attempts to prepare an apply with blank DesiredCommands.
The engine must fail before creating any reset-root delete plan.

Validates:
  - Empty DesiredCommands are rejected
  - Failure uses empty_desired_commands
  - Returned plan has no delete commands
*/
func TestPrepareRejectsEmptyDesiredCommandsWithoutDeletePlan(t *testing.T) {
	engine, err := New()
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput(" \n\t "))
	assertApplyCode(t, err, CodeEmptyDesiredCommands)
	if len(plan.DeleteCommands) != 0 || len(plan.SetCommands) != 0 {
		t.Fatalf("empty desired commands produced plan: %#v", plan)
	}
}

/*
TC-APPLY-PLANNER-004
Type: Negative
Title: Missing metadata
Summary:
Checks required apply metadata before command parsing.
Target and ConfigUUID are required for traceability and target compatibility.

Validates:
  - Missing target is rejected
  - Unsupported target is rejected
  - Missing ConfigUUID is rejected
*/
func TestPrepareRejectsMissingMetadata(t *testing.T) {
	engine, err := New()
	assertNoApplyError(t, err)

	tests := []Input{
		{Target: "", ConfigUUID: "cfg-123", DesiredCommands: sampleCommands()},
		{Target: "ios", ConfigUUID: "cfg-123", DesiredCommands: sampleCommands()},
		{Target: "vyos", ConfigUUID: "", DesiredCommands: sampleCommands()},
	}

	for _, input := range tests {
		_, err := engine.Prepare(context.Background(), input)
		assertApplyCode(t, err, CodeInvalidInput)
	}
}

/*
TC-APPLY-PLANNER-005
Type: Positive
Title: Preserved ethernet description
Summary:
Prepares a plan containing a renderer-emitted ethernet description command.
The command is allowed even though interfaces ethernet is not broadly reset.

Validates:
  - Ethernet description command is accepted
  - Preserved roots can have specific allowlisted set paths
  - The command is preserved in SetCommands
*/
func TestPrepareAllowsRendererEthernetDescription(t *testing.T) {
	engine, err := New()
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput("set interfaces ethernet eth0 description 'WAN'\n"))
	assertNoApplyError(t, err)
	assertStringSlicesEqual(t, plan.SetCommands, []string{"set interfaces ethernet eth0 description 'WAN'"})
}

/*
TC-APPLY-PLANNER-006
Type: Negative
Title: Unsupported preserved-root path
Summary:
Attempts to prepare an ethernet command outside the description allowlist.
The engine should reject it before planning reset-root deletes.

Validates:
  - Ethernet address commands are rejected
  - Validation failure uses invalid_command
  - No delete plan is returned on failure
*/
func TestPrepareRejectsUnsupportedPreservedRootPath(t *testing.T) {
	engine, err := New()
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput("set interfaces ethernet eth0 address dhcp\n"))
	assertApplyCode(t, err, CodeInvalidCommand)
	if len(plan.DeleteCommands) != 0 {
		t.Fatalf("invalid preserved-root path produced deletes: %#v", plan.DeleteCommands)
	}
}

/*
TC-APPLY-PLANNER-007
Type: Positive
Title: Prepare does not execute
Summary:
Configures an executor and calls Prepare with valid input.
Prepare must only validate and plan; it must not call the executor.

Validates:
  - Prepare succeeds with an executor configured
  - Executor call count remains zero
  - Plan contains validated commands
*/
func TestPrepareDoesNotInvokeExecutor(t *testing.T) {
	exec := &fakeExecutor{}
	engine, err := New(WithExecutor(exec))
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)
	if exec.calls != 0 {
		t.Fatalf("Prepare invoked executor %d times", exec.calls)
	}
	if len(plan.SetCommands) == 0 {
		t.Fatalf("expected set commands in plan")
	}
}

/*
TC-APPLY-PLANNER-008
Type: Positive
Title: Save option in plan
Summary:
Constructs an engine with save-after-commit enabled.
Prepare should reflect the save policy without executing anything.

Validates:
  - WithSaveAfterCommit enables Plan.Save
  - Plan.Commit remains true
  - Save is controlled by engine option
*/
func TestPrepareHonorsSaveAfterCommitOption(t *testing.T) {
	engine, err := New(WithSaveAfterCommit(true))
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(context.Background(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)
	if !plan.Commit || !plan.Save {
		t.Fatalf("unexpected commit/save flags: commit=%v save=%v", plan.Commit, plan.Save)
	}
}
