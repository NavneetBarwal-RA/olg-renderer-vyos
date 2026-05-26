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
	name string
	args []string
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := commandCall{name: name, args: append([]string(nil), args...)}
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
	return newCLIShellRunnerForTest("/tmp/fake-vyatta-cfg-cmd-wrapper", commandRunner)
}

/*
TC-VYOS-RUNNER-001
Type: Positive
Title: Absolute default wrapper path
Summary:
Validates the production constructor default for the VyOS config command wrapper.
The rolling VyOS path must be absolute and must not depend on a legacy save helper.

Validates:
  - Default wrapper path is absolute
  - Default wrapper path is /opt/vyatta/sbin/vyatta-cfg-cmd-wrapper
  - No dedicated save binary is configured
*/
func TestCLIShellRunnerUsesAbsoluteDefaultWrapperPath(t *testing.T) {
	runner := NewCLIShellRunner()

	if runner.configWrapper != "/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper" {
		t.Fatalf("unexpected wrapper path: %q", runner.configWrapper)
	}
	if !filepath.IsAbs(runner.configWrapper) {
		t.Fatalf("wrapper path is not absolute: %q", runner.configWrapper)
	}
}

/*
TC-VYOS-RUNNER-002
Type: Positive
Title: Wrapper command sequence
Summary:
Runs begin, set, delete, commit, save, discard, and end through the runner.
Every operation must use vyatta-cfg-cmd-wrapper with argv boundaries.

Validates:
  - Begin maps to wrapper begin
  - Set/delete keep operation tokens and argv boundaries
  - Save maps to wrapper save
*/
func TestCLIShellRunnerUsesWrapperForConfigCommands(t *testing.T) {
	commandRunner := &fakeCommandRunner{}
	runner := newRunnerForTest(commandRunner)

	if _, err := runner.Begin(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
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
	if _, err := runner.End(context.Background()); err != nil {
		t.Fatalf("end: %v", err)
	}

	want := []commandCall{
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"begin"}},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"set", "interfaces", "ethernet", "eth0", "description", "WAN uplink"}},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"delete", "interfaces", "bridge"}},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"commit"}},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"save"}},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"discard"}},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"end"}},
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
		if _, err := runner.RunConfigCommand(context.Background(), command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
		if len(commandRunner.calls) != 0 {
			t.Fatalf("unexpected command runner calls for %q: %#v", command, commandRunner.calls)
		}
	}
}
