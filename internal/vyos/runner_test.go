package vyos

import (
	"context"
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
TC-VYOS-EXECUTOR-011
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
	runner := newCLIShellRunnerForTest("cli-shell-api", commandRunner)

	if _, err := runner.Configure(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := runner.RunConfigCommand(context.Background(), "set interfaces ethernet eth0 description 'WAN uplink'"); err != nil {
		t.Fatalf("run config command: %v", err)
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
		{name: "cli-shell-api", args: []string{"configure"}},
		{name: "cli-shell-api", args: []string{"set", "interfaces", "ethernet", "eth0", "description", "WAN uplink"}},
		{name: "cli-shell-api", args: []string{"commit"}},
		{name: "cli-shell-api", args: []string{"save"}},
		{name: "cli-shell-api", args: []string{"discard"}},
	}
	if !reflect.DeepEqual(commandRunner.calls, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", commandRunner.calls, want)
	}
}

/*
TC-VYOS-EXECUTOR-012
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
	runner := newCLIShellRunnerForTest("cli-shell-api", commandRunner)

	if _, err := runner.RunConfigCommand(context.Background(), "set interfaces bridge br0 description 'LAN"); err == nil {
		t.Fatalf("expected unclosed quote error")
	}
	if len(commandRunner.calls) != 0 {
		t.Fatalf("command runner was called after tokenization failure: %#v", commandRunner.calls)
	}
}
