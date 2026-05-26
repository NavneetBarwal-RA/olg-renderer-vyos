package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/routerarchitects/olg-renderer-vyos/apply"
)

/*
TC-VYOS-SMOKE-001
Type: Mixed
Title: Smoke command mode selection
Summary:
Builds smoke payload commands for supported modes and an unsupported mode.
The helper must keep defaults small and reject unknown mode values before apply.

Validates:
  - minimal mode returns a bridge description command
  - bridge mode returns the validated bridge command
  - unsupported modes are rejected
*/
func TestBuildSmokeCommandsSelectsSupportedModes(t *testing.T) {
	tests := []struct {
		mode string
		want []string
	}{
		{mode: "minimal", want: []string{"set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'"}},
		{mode: "bridge", want: []string{"set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'"}},
	}

	for _, test := range tests {
		got, err := buildSmokeCommands(test.mode)
		if err != nil {
			t.Fatalf("buildSmokeCommands(%q): %v", test.mode, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("unexpected commands for %q:\n got: %#v\nwant: %#v", test.mode, got, test.want)
		}
	}

	for _, mode := range []string{"nat", "system"} {
		if _, err := buildSmokeCommands(mode); err == nil {
			t.Fatalf("expected unsupported mode %q to fail", mode)
		}
	}
}

/*
TC-VYOS-SMOKE-007
Type: Negative
Title: NAT smoke mode is disabled
Summary:
Checks that nat mode is rejected until it can be made complete and explicit.
The validated smoke path currently covers minimal bridge reconciliation.

Validates:
  - nat mode is rejected
  - The error mentions supported modes
  - NAT smoke cannot accidentally delete bridge without recreating it
*/
func TestBuildSmokeCommandsRejectsNatMode(t *testing.T) {
	_, err := buildSmokeCommands("nat")
	if err == nil {
		t.Fatalf("expected unsupported mode error")
	}
	if !strings.Contains(err.Error(), "minimal or bridge") {
		t.Fatalf("unexpected nat mode error: %v", err)
	}
}

/*
TC-VYOS-SMOKE-002
Type: Negative
Title: Safety confirmation required
Summary:
Validates the confirmation helper used by the smoke command.
The command must fail before constructing apply when the explicit flag is absent.

Validates:
  - Missing confirmation returns an error
  - Present confirmation is accepted
  - The helper is independent of real VyOS binaries
*/
func TestValidateConfirmationFlagRequiresExplicitOptIn(t *testing.T) {
	if err := validateConfirmationFlag(false); err == nil {
		t.Fatalf("expected missing confirmation to fail")
	}
	if err := validateConfirmationFlag(true); err != nil {
		t.Fatalf("expected confirmation to pass: %v", err)
	}
}

/*
TC-VYOS-SMOKE-003
Type: Mixed
Title: Required binary checks use injected paths
Summary:
Creates temporary executable and non-executable files for binary validation.
The test exercises file mode checks without requiring real VyOS paths on CI.

Validates:
  - Executable files pass
  - Non-executable files fail
  - Missing files fail
*/
func TestCheckRequiredBinariesUsesInjectedPaths(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "fake-vyatta-cfg-cmd-wrapper")
	notExecutable := filepath.Join(dir, "fake-not-executable")

	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}

	if err := checkRequiredBinaries([]string{executable}); err != nil {
		t.Fatalf("expected executable to pass: %v", err)
	}
	if err := checkRequiredBinaries([]string{notExecutable}); err == nil {
		t.Fatalf("expected non-executable to fail")
	}
	if err := checkRequiredBinaries([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatalf("expected missing file to fail")
	}
}

/*
TC-VYOS-SMOKE-006
Type: Positive
Title: Required binaries use wrapper for save modes
Summary:
Builds the smoke binary list for save disabled and save enabled.
Both modes should require only the modern generic config wrapper.

Validates:
  - save=false requires vyatta-cfg-cmd-wrapper
  - save=true requires vyatta-cfg-cmd-wrapper
  - No legacy save helper is required
*/
func TestRequiredBinariesForSmokeUsesWrapperForSaveModes(t *testing.T) {
	for _, save := range []bool{false, true} {
		got := requiredBinariesForSmoke(save)
		want := []string{"/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected required binaries for save=%t:\n got: %#v\nwant: %#v", save, got, want)
		}
		for _, path := range got {
			if strings.Contains(path, "vyatta-"+"save-config.pl") {
				t.Fatalf("legacy save helper should not be required for save=%t: %#v", save, got)
			}
		}
	}
}

/*
TC-VYOS-SMOKE-004
Type: Positive
Title: Plan log formatting
Summary:
Formats a prepared plan into smoke logs.
The output should include counts and each delete/set command for manual review.

Validates:
  - Plan counts are logged
  - Delete command entries are logged
  - Set command entries are logged
*/
func TestLogPlanIncludesCountsAndCommands(t *testing.T) {
	var out strings.Builder
	plan := apply.Plan{
		DeleteCommands: []string{"delete interfaces bridge", "delete nat source"},
		SetCommands:    []string{"set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'"},
		Commit:         true,
		Save:           false,
	}

	logPlan(&out, plan)
	got := out.String()
	for _, want := range []string{
		"[smoke] plan delete_count=2 set_count=1 commit=true save=false",
		"[smoke] delete[0]=delete interfaces bridge",
		"[smoke] delete[1]=delete nat source",
		"[smoke] set[0]=set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected log %q in:\n%s", want, got)
		}
	}
}

/*
TC-VYOS-SMOKE-005
Type: Positive
Title: Skip apply previews without real binaries
Summary:
Runs the smoke command in preview-only mode with the safety flag present.
The command should Prepare and print a plan without checking real VyOS binaries.

Validates:
  - skip-apply exits successfully
  - Prepare plan details are logged
  - Real binary checks are skipped
*/
func TestRunSkipApplyPreviewsWithoutRealBinaries(t *testing.T) {
	var out strings.Builder
	code := run([]string{
		"--i-understand-this-modifies-vyos",
		"--skip-apply",
		"--mode", "minimal",
	}, &out, func() time.Time {
		return time.Date(2026, 5, 26, 1, 2, 3, 0, time.UTC)
	})

	if code != 0 {
		t.Fatalf("unexpected exit code %d:\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"[smoke] skip_apply=true; skipping required binary checks and Apply",
		"[smoke] target=vyos config_uuid=smoke-20260526T010203Z",
		"[smoke] previewing plan with Prepare",
		"[smoke] completed preview without Apply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected log %q in:\n%s", want, got)
		}
	}
}
