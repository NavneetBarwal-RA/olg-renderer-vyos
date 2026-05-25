package vyos

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeCommandRunner struct {
	calls []commandCall
}

type commandCall struct {
	name string
	args []string
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, commandCall{name: name, args: append([]string(nil), args...)})
	return "", nil
}

/*
TC-VYOS-RUNNER-001
Type: Positive
Title: Absolute default binary path
Summary:
Checks the production default cli-shell-api binary path.
The default runner must avoid PATH lookup when Apply uses the internal executor.

Validates:
  - Default cli-shell-api path is absolute
  - Default path is /usr/bin/cli-shell-api
  - Production construction avoids PATH lookup by default
*/
func TestCLIShellRunnerUsesAbsoluteDefaultPath(t *testing.T) {
	runner := NewCLIShellRunner()
	if runner.binary != "/usr/bin/cli-shell-api" {
		t.Fatalf("unexpected default binary: %q", runner.binary)
	}
	if !filepath.IsAbs(runner.binary) {
		t.Fatalf("default binary path is not absolute: %q", runner.binary)
	}
}

/*
TC-VYOS-RUNNER-002
Type: Positive
Title: Test binary override
Summary:
Uses the internal test constructor to override the cli-shell-api path.
Tests can inject a controlled binary path without changing production defaults.

Validates:
  - Test constructor overrides binary path
  - Overridden path is passed to command runner
  - Override does not require PATH lookup
*/
func TestCLIShellRunnerTestConstructorOverridesBinaryPath(t *testing.T) {
	commandRunner := &fakeCommandRunner{}
	runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)

	if _, err := runner.Configure(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if len(commandRunner.calls) != 1 || commandRunner.calls[0].name != "/tmp/fake-cli-shell-api" {
		t.Fatalf("unexpected runner calls: %#v", commandRunner.calls)
	}
}

/*
TC-VYOS-RUNNER-003
Type: Negative
Title: Empty and newline commands
Summary:
Passes empty and newline-containing config commands to the runner.
The internal guard should reject them before cli-shell-api is invoked.

Validates:
  - Empty commands are rejected
  - Newline-containing commands are rejected
  - Rejected commands do not invoke the command runner
*/
func TestRunConfigCommandRejectsEmptyAndNewlineCommands(t *testing.T) {
	tests := []string{
		"",
		"   ",
		"set interfaces bridge br0 address dhcp\ncommit",
		"set interfaces bridge br0 address dhcp\r\ncommit",
	}

	for _, command := range tests {
		commandRunner := &fakeCommandRunner{}
		runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)
		if _, err := runner.RunConfigCommand(context.Background(), command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
		if len(commandRunner.calls) != 0 {
			t.Fatalf("command runner was called for rejected command %q: %#v", command, commandRunner.calls)
		}
	}
}

/*
TC-VYOS-RUNNER-004
Type: Negative
Title: Shell metacharacter guard
Summary:
Passes commands containing obvious shell and control metacharacters.
The runner should reject these as a last-resort guard against internal misuse.

Validates:
  - Shell separators are rejected
  - Command substitution markers are rejected
  - Rejected commands do not invoke cli-shell-api
*/
func TestRunConfigCommandRejectsShellMetacharacters(t *testing.T) {
	tests := []string{
		"set interfaces bridge br0 description LAN; commit",
		"set interfaces bridge br0 description LAN | commit",
		"set interfaces bridge br0 description LAN & commit",
		"set interfaces bridge br0 description `whoami`",
		"set interfaces bridge br0 description $(whoami)",
		"set interfaces bridge br0 description LAN > /tmp/out",
		"set interfaces bridge br0 description LAN < /tmp/in",
	}

	for _, command := range tests {
		commandRunner := &fakeCommandRunner{}
		runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)
		if _, err := runner.RunConfigCommand(context.Background(), command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
		if len(commandRunner.calls) != 0 {
			t.Fatalf("command runner was called for rejected command %q: %#v", command, commandRunner.calls)
		}
	}
}

/*
TC-VYOS-RUNNER-005
Type: Mixed
Title: Config operation allowlist
Summary:
Checks the internal runner operation guard.
RunConfigCommand should accept only set and delete operations and reject control operations.

Validates:
  - set commands are accepted
  - delete commands are accepted
  - configure, commit, save, discard, show, run, and sudo are rejected
*/
func TestRunConfigCommandAllowsOnlySetAndDeleteOperations(t *testing.T) {
	for _, command := range []string{
		"set interfaces bridge br0 address dhcp",
		"delete interfaces bridge",
	} {
		commandRunner := &fakeCommandRunner{}
		runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)
		if _, err := runner.RunConfigCommand(context.Background(), command); err != nil {
			t.Fatalf("expected command %q to be accepted: %v", command, err)
		}
		if len(commandRunner.calls) != 1 {
			t.Fatalf("expected one call for command %q, got %#v", command, commandRunner.calls)
		}
	}

	for _, command := range []string{
		"configure",
		"commit",
		"save",
		"discard",
		"show configuration",
		"run show configuration",
		"sudo cli-shell-api commit",
	} {
		commandRunner := &fakeCommandRunner{}
		runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)
		if _, err := runner.RunConfigCommand(context.Background(), command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
		if len(commandRunner.calls) != 0 {
			t.Fatalf("command runner was called for rejected command %q: %#v", command, commandRunner.calls)
		}
	}
}

/*
TC-VYOS-RUNNER-006
Type: Positive
Title: CLI shell argument boundaries
Summary:
Runs each domain operation through CLIShellRunner using a fake command runner.
Quoted command values should become one argv element without using a shell string.

Validates:
  - cli-shell-api is invoked with argv boundaries
  - Quoted spaces are preserved as one argument
  - Configure, command, commit, save, and discard are separate calls
*/
func TestCLIShellRunnerPreservesArgumentBoundaries(t *testing.T) {
	commandRunner := &fakeCommandRunner{}
	runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)

	if _, err := runner.Configure(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := runner.RunConfigCommand(context.Background(), "set interfaces ethernet eth0 description 'WAN uplink'"); err != nil {
		t.Fatalf("run set command: %v", err)
	}
	if _, err := runner.RunConfigCommand(context.Background(), "delete interfaces bridge"); err != nil {
		t.Fatalf("run delete command: %v", err)
	}
	if _, err := runner.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := runner.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := runner.Discard(context.Background()); err != nil {
		t.Fatalf("discard: %v", err)
	}

	want := []commandCall{
		{name: "/tmp/fake-cli-shell-api", args: []string{"configure"}},
		{name: "/tmp/fake-cli-shell-api", args: []string{"set", "interfaces", "ethernet", "eth0", "description", "WAN uplink"}},
		{name: "/tmp/fake-cli-shell-api", args: []string{"delete", "interfaces", "bridge"}},
		{name: "/tmp/fake-cli-shell-api", args: []string{"commit"}},
		{name: "/tmp/fake-cli-shell-api", args: []string{"save"}},
		{name: "/tmp/fake-cli-shell-api", args: []string{"discard"}},
	}
	if !reflect.DeepEqual(commandRunner.calls, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", commandRunner.calls, want)
	}
}

/*
TC-VYOS-RUNNER-007
Type: Negative
Title: CLI shell rejects invalid command tokenization
Summary:
Passes an unclosed quoted command to the runner.
The runner should fail before invoking cli-shell-api.

Validates:
  - Unclosed quotes are rejected
  - No command runner call is made
  - Tokenization errors happen before execution
*/
func TestCLIShellRunnerRejectsUnclosedQuoteBeforeExecution(t *testing.T) {
	commandRunner := &fakeCommandRunner{}
	runner := newCLIShellRunnerForTest("/tmp/fake-cli-shell-api", commandRunner)

	if _, err := runner.RunConfigCommand(context.Background(), "set interfaces bridge br0 description 'LAN"); err == nil {
		t.Fatalf("expected unclosed quote error")
	}
	if len(commandRunner.calls) != 0 {
		t.Fatalf("command runner was called after tokenization failure: %#v", commandRunner.calls)
	}
}
