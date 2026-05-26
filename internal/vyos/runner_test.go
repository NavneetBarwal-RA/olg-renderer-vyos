package vyos

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeCommandRunner struct {
	calls   []commandCall
	outputs map[string]string
	errors  map[string]error
}

type commandCall struct {
	name       string
	args       []string
	sessionEnv map[string]string
}

func (f *fakeCommandRunner) Run(ctx context.Context, sessionEnv map[string]string, name string, args ...string) (string, error) {
	copiedEnv := make(map[string]string, len(sessionEnv))
	for key, value := range sessionEnv {
		copiedEnv[key] = value
	}
	call := commandCall{name: name, args: append([]string(nil), args...), sessionEnv: copiedEnv}
	f.calls = append(f.calls, call)
	key := name + " " + joinArgs(args)
	if f.outputs != nil {
		if output, ok := f.outputs[key]; ok {
			if f.errors != nil {
				return output, f.errors[key]
			}
			return output, nil
		}
	}
	if f.errors != nil {
		if err, ok := f.errors[key]; ok {
			return "", err
		}
	}
	return "", nil
}

func joinArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := args[0]
	for i := 1; i < len(args); i++ {
		out += " " + args[i]
	}
	return out
}

func newRunnerForTest(commandRunner commandRunner) *CLIShellRunner {
	return newCLIShellRunnerForTest(
		"/tmp/fake-cli-shell-api",
		"/tmp/fake-my_set",
		"/tmp/fake-my_delete",
		"/tmp/fake-my_commit",
		"/tmp/fake-my_discard",
		"/tmp/fake-save-config",
		commandRunner,
	)
}

/*
TC-VYOS-RUNNER-001
Type: Positive
Title: Absolute documented default binaries
Summary:
Validates the production constructor defaults for all binaries used by the
session-based runner. Paths must be absolute and match documented VyOS paths.

Validates:
  - Default cli-shell-api path is absolute
  - Default my_set/my_delete/my_commit/my_discard paths are absolute
  - Default save binary path is absolute and explicit
*/
func TestCLIShellRunnerUsesAbsoluteDocumentedDefaultPaths(t *testing.T) {
	runner := NewCLIShellRunner()

	if runner.cliShellAPI != "/usr/bin/cli-shell-api" {
		t.Fatalf("unexpected cli-shell-api path: %q", runner.cliShellAPI)
	}
	if runner.mySet != "/opt/vyatta/sbin/my_set" {
		t.Fatalf("unexpected my_set path: %q", runner.mySet)
	}
	if runner.myDelete != "/opt/vyatta/sbin/my_delete" {
		t.Fatalf("unexpected my_delete path: %q", runner.myDelete)
	}
	if runner.myCommit != "/opt/vyatta/sbin/my_commit" {
		t.Fatalf("unexpected my_commit path: %q", runner.myCommit)
	}
	if runner.myDiscard != "/opt/vyatta/sbin/my_discard" {
		t.Fatalf("unexpected my_discard path: %q", runner.myDiscard)
	}
	if runner.saveConfig != "/opt/vyatta/sbin/vyatta-save-config.pl" {
		t.Fatalf("unexpected save binary path: %q", runner.saveConfig)
	}

	for _, path := range []string{
		runner.cliShellAPI,
		runner.mySet,
		runner.myDelete,
		runner.myCommit,
		runner.myDiscard,
		runner.saveConfig,
	} {
		if !filepath.IsAbs(path) {
			t.Fatalf("path is not absolute: %q", path)
		}
	}
}

/*
TC-VYOS-SESSION-011
Type: Positive
Title: getSessionEnv parsing and invocation
Summary:
Executes getSessionEnv through the CLI shell binary and parses the returned
assignments into a map used by later session calls.

Validates:
  - cli-shell-api getSessionEnv is invoked with provided session ID
  - export and quoted values are parsed correctly
  - Parsed env map can be consumed by setupSession
*/
func TestCLIShellRunnerGetSessionEnvParsesOutput(t *testing.T) {
	commandRunner := &fakeCommandRunner{
		outputs: map[string]string{
			"/tmp/fake-cli-shell-api getSessionEnv session-011": "export VYATTA_CONFIG_SID='session-011'; COMMIT_VIA=\"api\"",
		},
	}
	runner := newRunnerForTest(commandRunner)

	sessionEnv, err := runner.GetSessionEnv(context.Background(), "session-011")
	if err != nil {
		t.Fatalf("get session env: %v", err)
	}
	wantEnv := map[string]string{
		"VYATTA_CONFIG_SID": "session-011",
		"COMMIT_VIA":        "api",
	}
	if !reflect.DeepEqual(sessionEnv, wantEnv) {
		t.Fatalf("unexpected session env:\n got: %#v\nwant: %#v", sessionEnv, wantEnv)
	}
	if len(commandRunner.calls) != 1 {
		t.Fatalf("unexpected call count: %#v", commandRunner.calls)
	}
	if commandRunner.calls[0].name != "/tmp/fake-cli-shell-api" ||
		!reflect.DeepEqual(commandRunner.calls[0].args, []string{"getSessionEnv", "session-011"}) {
		t.Fatalf("unexpected getSessionEnv call: %#v", commandRunner.calls[0])
	}
}

/*
TC-VYOS-RUNNER-002
Type: Positive
Title: RunConfigCommand maps to my_set and my_delete
Summary:
Runs one set and one delete command through the runner.
Leading operation tokens must be stripped before invoking my_* binaries.

Validates:
  - set maps to my_set without leading set token
  - delete maps to my_delete without leading delete token
  - Quoted value with spaces is preserved as one argv element
*/
func TestRunConfigCommandMapsOperationsToMyBinaries(t *testing.T) {
	commandRunner := &fakeCommandRunner{}
	runner := newRunnerForTest(commandRunner)
	sessionEnv := map[string]string{"VYATTA_CONFIG_SID": "session-012"}

	if _, err := runner.RunConfigCommand(context.Background(), sessionEnv, "set interfaces ethernet eth0 description 'WAN uplink'"); err != nil {
		t.Fatalf("run set command: %v", err)
	}
	if _, err := runner.RunConfigCommand(context.Background(), sessionEnv, "delete interfaces bridge"); err != nil {
		t.Fatalf("run delete command: %v", err)
	}

	want := []commandCall{
		{
			name:       "/tmp/fake-my_set",
			args:       []string{"interfaces", "ethernet", "eth0", "description", "WAN uplink"},
			sessionEnv: map[string]string{"VYATTA_CONFIG_SID": "session-012"},
		},
		{
			name:       "/tmp/fake-my_delete",
			args:       []string{"interfaces", "bridge"},
			sessionEnv: map[string]string{"VYATTA_CONFIG_SID": "session-012"},
		},
	}
	if !reflect.DeepEqual(commandRunner.calls, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", commandRunner.calls, want)
	}
}

/*
TC-VYOS-RUNNER-003
Type: Negative
Title: Invalid command guardrails
Summary:
Passes invalid command strings to RunConfigCommand.
The runner should reject these before invoking the command runner.

Validates:
  - Empty and newline commands are rejected
  - Shell/control metacharacter input is rejected
  - Unsupported operations are rejected
*/
func TestRunConfigCommandRejectsInvalidInputBeforeExecution(t *testing.T) {
	tests := []string{
		"",
		"  ",
		"set interfaces bridge br0 address dhcp\ncommit",
		"set interfaces bridge br0 description LAN; commit",
		"commit",
		"show configuration commands",
	}

	for _, command := range tests {
		commandRunner := &fakeCommandRunner{}
		runner := newRunnerForTest(commandRunner)
		if _, err := runner.RunConfigCommand(context.Background(), map[string]string{}, command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
		if len(commandRunner.calls) != 0 {
			t.Fatalf("unexpected command runner calls for %q: %#v", command, commandRunner.calls)
		}
	}
}

/*
TC-VYOS-SESSION-012
Type: Negative
Title: Session env parser rejects malformed output
Summary:
Supplies malformed getSessionEnv assignment output to the parser.
The parser must fail safely rather than guessing ambiguous assignments.

Validates:
  - Invalid shell keys are rejected
  - Unclosed quoted values are rejected
  - Missing assignment separator is rejected
*/
func TestParseSessionEnvRejectsMalformedOutput(t *testing.T) {
	tests := []string{
		"1INVALID=value",
		"VYATTA_CONFIG_SID='unclosed",
		"export ONLYKEY",
	}

	for _, output := range tests {
		if _, err := parseSessionEnv(output); err == nil {
			t.Fatalf("expected output to be rejected: %q", output)
		}
	}
}
