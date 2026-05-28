package apply

import (
	"context"
	"testing"
)

/*
TC-APPLY-POLICY-001
Type: Positive
Title: Default reset delete commands
Summary:
Checks the MVP default reset policy and generated delete commands.
The order is part of the deterministic apply contract.

Validates:
  - Default roots include interfaces bridge
  - Default roots include nat source
  - Delete command order is deterministic
*/
func TestDefaultResetPolicyBuildsDeterministicDeleteCommands(t *testing.T) {
	policy := DefaultResetPolicy()
	assertStringSlicesEqual(t, policy.ResetRoots, []string{"interfaces bridge", "nat source"})
	assertStringSlicesEqual(t, buildDeleteCommands(policy), []string{"delete interfaces bridge", "delete nat source"})
}

/*
TC-APPLY-POLICY-002
Type: Positive
Title: Custom policy replaces default
Summary:
Creates an engine with a single allowed reset root.
The resulting plan should use only the caller-provided policy.

Validates:
  - WithResetPolicy replaces default roots
  - Caller order is preserved
  - Default nat source delete is omitted when not configured
*/
func TestWithResetPolicyReplacesDefaultPolicy(t *testing.T) {
	engine, err := New(WithResetPolicy(ResetPolicy{ResetRoots: []string{"nat source"}}))
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(contextBackground(), baseInput("set nat source rule 100 translation address masquerade\n"))
	assertNoApplyError(t, err)
	assertStringSlicesEqual(t, plan.DeleteCommands, []string{"delete nat source"})
}

/*
TC-APPLY-POLICY-003
Type: Positive
Title: Allowed roots and whitespace normalization
Summary:
Validates allowed reset roots with irregular caller whitespace.
The normalized order should be preserved exactly in generated deletes.

Validates:
  - interfaces bridge is accepted
  - interfaces bridge br0 is accepted for targeted lab smoke
  - nat source is accepted
  - Internal whitespace is normalized
*/
func TestWithResetPolicyAcceptsAllowedRoots(t *testing.T) {
	engine, err := New(WithResetPolicy(ResetPolicy{ResetRoots: []string{" nat   source ", "interfaces\tbridge", "interfaces bridge   br0"}}))
	assertNoApplyError(t, err)

	plan, err := engine.Prepare(contextBackground(), baseInput(sampleCommands()))
	assertNoApplyError(t, err)
	assertStringSlicesEqual(t, plan.DeleteCommands, []string{"delete nat source", "delete interfaces bridge", "delete interfaces bridge br0"})
}

/*
TC-APPLY-POLICY-004
Type: Negative
Title: Unsafe reset roots
Summary:
Attempts to configure broad or protected roots as reset roots.
The engine must reject every root outside the exact MVP allowlist.

Validates:
  - Protected roots are rejected
  - Broad interfaces and nat roots are rejected
  - Invalid policies return invalid_input
*/
func TestWithResetPolicyRejectsUnsafeRoots(t *testing.T) {
	tests := []string{
		"system",
		"service",
		"users",
		"protocols",
		"container",
		"interfaces ethernet",
		"interfaces",
		"nat",
	}

	for _, root := range tests {
		_, err := New(WithResetPolicy(ResetPolicy{ResetRoots: []string{root}}))
		assertApplyCode(t, err, CodeInvalidInput)
	}
}

/*
TC-APPLY-POLICY-005
Type: Negative
Title: Empty and full reset roots
Summary:
Attempts to configure empty or full-config reset roots.
These roots are rejected before an engine can be constructed.

Validates:
  - Empty roots are rejected
  - Whitespace-only roots are rejected
  - Root delete slash is rejected
*/
func TestWithResetPolicyRejectsEmptyAndRootDeletes(t *testing.T) {
	for _, root := range []string{"", "   ", "/"} {
		_, err := New(WithResetPolicy(ResetPolicy{ResetRoots: []string{root}}))
		assertApplyCode(t, err, CodeInvalidInput)
	}
}

func contextBackground() context.Context {
	return context.Background()
}
