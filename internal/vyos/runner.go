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
	defaultCLIShellAPI       = "/usr/bin/cli-shell-api"
	forbiddenConfigCmdChars  = ";|&`$><"
	defaultCLIShellAPIDocRef = "cli-shell-api"
)

// Runner is the domain-specific VyOS configuration command boundary.
type Runner interface {
	Configure(ctx context.Context) (string, error)
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

// CLIShellRunner invokes VyOS cli-shell-api with one argument vector per operation.
//
// It is intentionally domain-specific: RunConfigCommand must only receive
// delete/set commands from a validated apply.Plan. The defensive checks here are
// last-resort guardrails for internal misuse, not a replacement for apply input
// validation.
type CLIShellRunner struct {
	binary string
	runner commandRunner
}

// NewCLIShellRunner returns a runner using the absolute VyOS cli-shell-api path.
func NewCLIShellRunner() *CLIShellRunner {
	return &CLIShellRunner{
		binary: defaultCLIShellAPI,
		runner: osCommandRunner{},
	}
}

func newCLIShellRunnerForTest(binary string, runner commandRunner) *CLIShellRunner {
	return &CLIShellRunner{binary: binary, runner: runner}
}

// Configure enters a VyOS candidate configuration session.
func (r *CLIShellRunner) Configure(ctx context.Context) (string, error) {
	return r.run(ctx, "configure")
}

// RunConfigCommand runs one validated set or delete configuration command.
func (r *CLIShellRunner) RunConfigCommand(ctx context.Context, command string) (string, error) {
	args, err := splitCLICommand(command)
	if err != nil {
		return "", err
	}
	return r.run(ctx, args...)
}

// Commit commits the current VyOS candidate configuration.
func (r *CLIShellRunner) Commit(ctx context.Context) (string, error) {
	return r.run(ctx, "commit")
}

// Save saves the committed VyOS configuration.
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
	binary := r.binary
	if binary == "" {
		binary = defaultCLIShellAPI
	}
	if !filepath.IsAbs(binary) {
		return "", fmt.Errorf("%s path must be absolute", defaultCLIShellAPIDocRef)
	}
	return r.runner.Run(ctx, binary, args...)
}

func splitCLICommand(command string) ([]string, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil, fmt.Errorf("command must not be empty")
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return nil, fmt.Errorf("command must not contain newlines")
	}
	if strings.ContainsAny(trimmed, forbiddenConfigCmdChars) {
		return nil, fmt.Errorf("command contains forbidden character")
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
		return nil, fmt.Errorf("unclosed quote")
	}
	if tokenStarted {
		tokens = append(tokens, current.String())
	}
	if tokens[0] != "set" && tokens[0] != "delete" {
		return nil, fmt.Errorf("command operation %q is not allowed", tokens[0])
	}
	return tokens, nil
}
