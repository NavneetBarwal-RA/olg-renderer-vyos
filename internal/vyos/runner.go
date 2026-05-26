package vyos

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	defaultConfigWrapper     = "/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper"
	forbiddenConfigCmdChars  = ";|&`$><"
	defaultConfigWrapperName = "vyatta-cfg-cmd-wrapper"
)

// Runner is the domain-specific VyOS configuration command boundary.
type Runner interface {
	Begin(ctx context.Context) (string, error)
	End(ctx context.Context) (string, error)
	RunConfigCommand(ctx context.Context, command string) (string, error)
	Commit(ctx context.Context) (string, error)
	Save(ctx context.Context) (string, error)
	Discard(ctx context.Context) (string, error)
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type osCommandRunner struct{}

func (r osCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// CLIShellRunner invokes the VyOS config command wrapper with argv boundaries.
//
// It is intentionally domain-specific: RunConfigCommand must only receive
// delete/set commands from a validated apply.Plan. The defensive checks here are
// last-resort guardrails for internal misuse, not a replacement for apply input
// validation.
type CLIShellRunner struct {
	configWrapper string
	runner        commandRunner
}

// NewCLIShellRunner returns a runner using the absolute modern VyOS wrapper path.
func NewCLIShellRunner() *CLIShellRunner {
	return &CLIShellRunner{
		configWrapper: defaultConfigWrapper,
		runner:        osCommandRunner{},
	}
}

func newCLIShellRunnerForTest(configWrapper string, runner commandRunner) *CLIShellRunner {
	return &CLIShellRunner{configWrapper: configWrapper, runner: runner}
}

// Begin starts a VyOS candidate configuration session through the wrapper.
func (r *CLIShellRunner) Begin(ctx context.Context) (string, error) {
	return r.run(ctx, "begin")
}

// End closes the VyOS candidate configuration session through the wrapper.
func (r *CLIShellRunner) End(ctx context.Context) (string, error) {
	return r.run(ctx, "end")
}

// RunConfigCommand runs one validated set or delete configuration command.
func (r *CLIShellRunner) RunConfigCommand(ctx context.Context, command string) (string, error) {
	operation, args, err := splitCLICommand(command)
	if err != nil {
		return "", err
	}
	return r.run(ctx, append([]string{operation}, args...)...)
}

// Commit commits the current VyOS candidate configuration.
func (r *CLIShellRunner) Commit(ctx context.Context) (string, error) {
	return r.run(ctx, "commit")
}

// Save persists committed configuration through the same generic wrapper.
func (r *CLIShellRunner) Save(ctx context.Context) (string, error) {
	return r.run(ctx, "save")
}

// Discard discards the current VyOS candidate configuration.
func (r *CLIShellRunner) Discard(ctx context.Context) (string, error) {
	return r.run(ctx, "discard")
}

func (r *CLIShellRunner) run(ctx context.Context, args ...string) (string, error) {
	if r == nil || r.runner == nil {
		return "", fmt.Errorf("vyos runner is not initialized")
	}
	if !filepath.IsAbs(r.configWrapper) {
		return "", fmt.Errorf("%s path must be absolute", defaultConfigWrapperName)
	}
	return r.runner.Run(ctx, r.configWrapper, args...)
}

func splitCLICommand(command string) (string, []string, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", nil, fmt.Errorf("command must not be empty")
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return "", nil, fmt.Errorf("command must not contain newlines")
	}
	if strings.ContainsAny(trimmed, forbiddenConfigCmdChars) {
		return "", nil, fmt.Errorf("command contains forbidden character")
	}

	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	tokenStarted := false

	for _, r := range trimmed {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			tokenStarted = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			tokenStarted = true
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			if tokenStarted {
				tokens = append(tokens, current.String())
				current.Reset()
				tokenStarted = false
			}
		default:
			current.WriteRune(r)
			tokenStarted = true
		}
	}

	if inSingle || inDouble {
		return "", nil, fmt.Errorf("unclosed quote")
	}
	if tokenStarted {
		tokens = append(tokens, current.String())
	}
	if len(tokens) == 0 {
		return "", nil, fmt.Errorf("command must not be empty")
	}
	operation := tokens[0]
	if operation != "set" && operation != "delete" {
		return "", nil, fmt.Errorf("command operation %q is not allowed", operation)
	}
	if len(tokens) < 2 {
		return "", nil, fmt.Errorf("%s command path must not be empty", operation)
	}
	return operation, tokens[1:], nil
}
