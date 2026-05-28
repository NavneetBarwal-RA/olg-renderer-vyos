package vyos

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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
	env  []string
}

func (f *fakeCommandRunner) Run(ctx context.Context, env []string, name string, args ...string) (string, error) {
	call := commandCall{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)}
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
	return newCLIShellRunnerForTest(runnerPaths{
		shellAPI:      "/tmp/fake-cli-shell-api",
		configWrapper: "/tmp/fake-vyatta-cfg-cmd-wrapper",
		mySet:         "/tmp/fake-my_set",
		myDelete:      "/tmp/fake-my_delete",
		myCommit:      "/tmp/fake-my_commit",
		myDiscard:     "/tmp/fake-my_discard",
	}, commandRunner, func() string { return "12345" })
}

/*
TC-VYOS-RUNNER-001
Type: Positive
Title: Absolute default session paths
Summary:
Validates the production constructor defaults for the VyOS CLI shell API session.

Validates:
  - Default command paths are absolute
  - Default shell API and my_* paths are configured
  - No dedicated save binary is configured
*/
func TestCLIShellRunnerUsesAbsoluteDefaultSessionPaths(t *testing.T) {
	runner := NewCLIShellRunner()

	if runner.paths.shellAPI != "/usr/bin/cli-shell-api" {
		t.Fatalf("unexpected shell api path: %q", runner.paths.shellAPI)
	}
	if runner.paths.configWrapper != "/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper" {
		t.Fatalf("unexpected wrapper path: %q", runner.paths.configWrapper)
	}
	for _, path := range []string{
		runner.paths.shellAPI,
		runner.paths.configWrapper,
		runner.paths.mySet,
		runner.paths.myDelete,
		runner.paths.myCommit,
		runner.paths.myDiscard,
	} {
		if !filepath.IsAbs(path) {
			t.Fatalf("path is not absolute: %q", path)
		}
	}
}

/*
TC-VYOS-RUNNER-002
Type: Positive
Title: Persistent session command sequence
Summary:
Runs begin, set, delete, commit, save, discard, and close through one session.
Every operation after setup must reuse the environment from getSessionEnv.

Validates:
  - Begin calls cli-shell-api getSessionEnv and setupSession
  - Set/delete map to my_set/my_delete without operation tokens
  - Save maps to wrapper save with the session environment
*/
func TestCLIShellRunnerUsesOneSessionForConfigCommands(t *testing.T) {
	commandRunner := &fakeCommandRunner{
		outputs: map[string]string{
			"/tmp/fake-cli-shell-api getSessionEnv 12345": "VYATTA_CONFIG_SID='abc123'\nVYATTA_CONFIG_TMP=/tmp/vyos\n",
		},
	}
	runner := newRunnerForTest(commandRunner)

	session, err := runner.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := session.Set(context.Background(), "set interfaces ethernet eth0 description 'WAN uplink'"); err != nil {
		t.Fatalf("run set command: %v", err)
	}
	if _, err := session.Delete(context.Background(), "delete interfaces bridge"); err != nil {
		t.Fatalf("run delete command: %v", err)
	}
	if _, err := session.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := session.Save(context.Background()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := session.Discard(context.Background()); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := session.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	want := []commandCall{
		{name: "/tmp/fake-cli-shell-api", args: []string{"getSessionEnv", "12345"}},
		{name: "/tmp/fake-cli-shell-api", args: []string{"setupSession"}},
		{name: "/tmp/fake-my_set", args: []string{"interfaces", "ethernet", "eth0", "description", "WAN uplink"}},
		{name: "/tmp/fake-my_delete", args: []string{"interfaces", "bridge"}},
		{name: "/tmp/fake-my_commit"},
		{name: "/tmp/fake-vyatta-cfg-cmd-wrapper", args: []string{"save"}},
		{name: "/tmp/fake-my_discard"},
		{name: "/tmp/fake-cli-shell-api", args: []string{"teardownSession"}},
	}
	gotComparable := make([]commandCall, len(commandRunner.calls))
	for i, call := range commandRunner.calls {
		gotComparable[i] = commandCall{name: call.name, args: call.args}
	}
	if !reflect.DeepEqual(gotComparable, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", commandRunner.calls, want)
	}
	for _, call := range commandRunner.calls[1:] {
		if !envContains(call.env, "VYATTA_CONFIG_SID=abc123") || !envContains(call.env, "VYATTA_CONFIG_TMP=/tmp/vyos") {
			t.Fatalf("call did not receive session env: %#v", call)
		}
	}
}

/*
TC-VYOS-RUNNER-003
Type: Negative
Title: Invalid command guardrails
Summary:
Passes invalid command strings to session Set.
The runner should reject these before invoking the command runner.

Validates:
  - Empty and newline commands are rejected
  - Shell/control metacharacter input is rejected
  - Unsupported operations are rejected
*/
func TestSessionRejectsInvalidInputBeforeExecution(t *testing.T) {
	tests := []string{
		"",
		"  ",
		"set interfaces bridge br0 address dhcp\ncommit",
		"set interfaces bridge br0 description LAN; commit",
		"commit",
		"show configuration commands",
	}

	for _, command := range tests {
		commandRunner := &fakeCommandRunner{
			outputs: map[string]string{
				"/tmp/fake-cli-shell-api getSessionEnv 12345": "VYATTA_CONFIG_SID=abc123\n",
			},
		}
		runner := newRunnerForTest(commandRunner)
		session, err := runner.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := session.Set(context.Background(), command); err == nil {
			t.Fatalf("expected command %q to be rejected", command)
		}
		if len(commandRunner.calls) != 2 {
			t.Fatalf("unexpected command runner calls for %q: %#v", command, commandRunner.calls)
		}
	}
}

func TestParseSessionEnvRejectsUnsafeOutput(t *testing.T) {
	tests := []string{
		"",
		"1BAD=value",
		"GOOD='unterminated",
		"GOOD=value\nBAD-KEY=value",
	}
	for _, test := range tests {
		if _, err := parseSessionEnv(test); err == nil {
			t.Fatalf("expected session env parse to fail for %q", test)
		}
	}
}

func TestParseSessionEnvAcceptsVyOSDeclareOutput(t *testing.T) {
	output := `declare -x VYATTA_EDIT_LEVEL=/; declare -x VYATTA_TEMPLATE_LEVEL=/; umask 002; { declare -x -r VYATTA_ACTIVE_CONFIGURATION_DIR=/opt/vyatta/config/active declare -x -r VYATTA_CHANGES_ONLY_DIR=/opt/vyatta/config/tmp/changes_only_4174;
declare -x VYATTA_CONFIG_SID='abc123';
declare -x -r VYATTA_TEMP_CONFIG_DIR=/opt/vyatta/config/tmp/new_config_4174; declare -x -r VYATTA_CONFIG_TMP=/opt/vyatta/config/tmp/tmp_4174; declare -x -r VYATTA_CONFIG_TEMPLATE=/opt/vyatta/share/vyatta-cfg/templates; } >&/dev/null || true`

	env, err := parseSessionEnv(output)
	if err != nil {
		t.Fatalf("parseSessionEnv: %v", err)
	}
	want := map[string]string{
		"VYATTA_ACTIVE_CONFIGURATION_DIR": "/opt/vyatta/config/active",
		"VYATTA_CHANGES_ONLY_DIR":         "/opt/vyatta/config/tmp/changes_only_4174",
		"VYATTA_CONFIG_TEMPLATE":          "/opt/vyatta/share/vyatta-cfg/templates",
		"VYATTA_CONFIG_TMP":               "/opt/vyatta/config/tmp/tmp_4174",
		"VYATTA_EDIT_LEVEL":               "/",
		"VYATTA_CONFIG_SID":               "abc123",
		"VYATTA_TEMP_CONFIG_DIR":          "/opt/vyatta/config/tmp/new_config_4174",
		"VYATTA_TEMPLATE_LEVEL":           "/",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("unexpected env:\n got: %#v\nwant: %#v", env, want)
	}
}

func TestBeginFailsWhenSetupSessionFails(t *testing.T) {
	commandRunner := &fakeCommandRunner{
		outputs: map[string]string{
			"/tmp/fake-cli-shell-api getSessionEnv 12345": "VYATTA_CONFIG_SID=abc123\n",
			"/tmp/fake-cli-shell-api setupSession":        "setup failed",
		},
		errors: map[string]error{
			"/tmp/fake-cli-shell-api setupSession": errors.New("setup failed"),
		},
	}
	runner := newRunnerForTest(commandRunner)

	session, err := runner.Begin(context.Background())
	if err == nil || session != nil {
		t.Fatalf("expected setup failure, got session=%#v err=%v", session, err)
	}
	for _, want := range []string{"setupSession failed", "/tmp/fake-cli-shell-api", "setupSession", "setup failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("setup error missing %q: %v", want, err)
		}
	}
}

func envContains(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
