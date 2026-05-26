package vyos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	defaultCLIShellAPI       = "/usr/bin/cli-shell-api"
	defaultMySetBinary       = "/opt/vyatta/sbin/my_set"
	defaultMyDeleteBinary    = "/opt/vyatta/sbin/my_delete"
	defaultMyCommitBinary    = "/opt/vyatta/sbin/my_commit"
	defaultMyDiscardBinary   = "/opt/vyatta/sbin/my_discard"
	defaultSaveConfigBinary  = "/opt/vyatta/sbin/vyatta-save-config.pl"
	forbiddenConfigCmdChars  = ";|&`$><"
	defaultCLIShellAPIDocRef = "cli-shell-api"
	defaultMySetDocRef       = "my_set"
	defaultMyDeleteDocRef    = "my_delete"
	defaultMyCommitDocRef    = "my_commit"
	defaultMyDiscardDocRef   = "my_discard"
	defaultSaveConfigDocRef  = "save-config"
)

// Runner is the domain-specific VyOS configuration command boundary.
type Runner interface {
	GetSessionEnv(ctx context.Context, sessionID string) (map[string]string, error)
	SetupSession(ctx context.Context, sessionEnv map[string]string) (string, error)
	TeardownSession(ctx context.Context, sessionEnv map[string]string) (string, error)
	RunConfigCommand(ctx context.Context, sessionEnv map[string]string, command string) (string, error)
	Commit(ctx context.Context, sessionEnv map[string]string) (string, error)
	Save(ctx context.Context, sessionEnv map[string]string) (string, error)
	Discard(ctx context.Context, sessionEnv map[string]string) (string, error)
}

type commandRunner interface {
	Run(ctx context.Context, sessionEnv map[string]string, name string, args ...string) (string, error)
}

type osCommandRunner struct{}

func (r osCommandRunner) Run(ctx context.Context, sessionEnv map[string]string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = buildCommandEnv(os.Environ(), sessionEnv)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

var shellKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CLIShellRunner invokes documented VyOS session and mutation binaries with argv boundaries.
//
// It is intentionally domain-specific: RunConfigCommand must only receive
// delete/set commands from a validated apply.Plan. The defensive checks here are
// last-resort guardrails for internal misuse, not a replacement for apply input
// validation.
type CLIShellRunner struct {
	cliShellAPI string
	mySet       string
	myDelete    string
	myCommit    string
	myDiscard   string
	saveConfig  string
	runner      commandRunner
}

// NewCLIShellRunner returns a runner using absolute documented VyOS binary paths.
func NewCLIShellRunner() *CLIShellRunner {
	return &CLIShellRunner{
		cliShellAPI: defaultCLIShellAPI,
		mySet:       defaultMySetBinary,
		myDelete:    defaultMyDeleteBinary,
		myCommit:    defaultMyCommitBinary,
		myDiscard:   defaultMyDiscardBinary,
		saveConfig:  defaultSaveConfigBinary,
		runner:      osCommandRunner{},
	}
}

func newCLIShellRunnerForTest(cliShellAPI, mySet, myDelete, myCommit, myDiscard, saveConfig string, runner commandRunner) *CLIShellRunner {
	return &CLIShellRunner{
		cliShellAPI: cliShellAPI,
		mySet:       mySet,
		myDelete:    myDelete,
		myCommit:    myCommit,
		myDiscard:   myDiscard,
		saveConfig:  saveConfig,
		runner:      runner,
	}
}

// GetSessionEnv reads session environment variables for a session ID.
func (r *CLIShellRunner) GetSessionEnv(ctx context.Context, sessionID string) (map[string]string, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, fmt.Errorf("session id must not be empty")
	}
	if strings.ContainsAny(id, "\r\n\t ") {
		return nil, fmt.Errorf("session id contains invalid whitespace")
	}
	output, err := r.run(ctx, nil, r.cliShellAPI, defaultCLIShellAPIDocRef, "getSessionEnv", id)
	if err != nil {
		return nil, err
	}
	sessionEnv, err := parseSessionEnv(output)
	if err != nil {
		return nil, err
	}
	return sessionEnv, nil
}

// SetupSession initializes the session environment for subsequent my_* commands.
func (r *CLIShellRunner) SetupSession(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return r.run(ctx, sessionEnv, r.cliShellAPI, defaultCLIShellAPIDocRef, "setupSession")
}

// TeardownSession always closes the session environment.
func (r *CLIShellRunner) TeardownSession(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return r.run(ctx, sessionEnv, r.cliShellAPI, defaultCLIShellAPIDocRef, "teardownSession")
}

// RunConfigCommand runs one validated set or delete configuration command.
func (r *CLIShellRunner) RunConfigCommand(ctx context.Context, sessionEnv map[string]string, command string) (string, error) {
	operation, args, err := splitCLICommand(command)
	if err != nil {
		return "", err
	}
	switch operation {
	case "set":
		return r.run(ctx, sessionEnv, r.mySet, defaultMySetDocRef, args...)
	case "delete":
		return r.run(ctx, sessionEnv, r.myDelete, defaultMyDeleteDocRef, args...)
	default:
		return "", fmt.Errorf("command operation %q is not allowed", operation)
	}
}

// Commit commits the current VyOS candidate configuration using my_commit.
func (r *CLIShellRunner) Commit(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return r.run(ctx, sessionEnv, r.myCommit, defaultMyCommitDocRef)
}

// Save persists committed configuration to disk.
func (r *CLIShellRunner) Save(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return r.run(ctx, sessionEnv, r.saveConfig, defaultSaveConfigDocRef)
}

// Discard discards the current VyOS candidate configuration using my_discard.
func (r *CLIShellRunner) Discard(ctx context.Context, sessionEnv map[string]string) (string, error) {
	return r.run(ctx, sessionEnv, r.myDiscard, defaultMyDiscardDocRef)
}

func (r *CLIShellRunner) run(ctx context.Context, sessionEnv map[string]string, binary, docRef string, args ...string) (string, error) {
	if r == nil || r.runner == nil {
		return "", fmt.Errorf("vyos runner is not initialized")
	}
	if !filepath.IsAbs(binary) {
		return "", fmt.Errorf("%s path must be absolute", docRef)
	}
	return r.runner.Run(ctx, sessionEnv, binary, args...)
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

func parseSessionEnv(output string) (map[string]string, error) {
	fragments, err := splitShellAssignmentFragments(output)
	if err != nil {
		return nil, err
	}
	if len(fragments) == 0 {
		return nil, fmt.Errorf("getSessionEnv returned no environment assignments")
	}

	sessionEnv := make(map[string]string, len(fragments))
	for _, fragment := range fragments {
		key, value, err := parseSessionEnvAssignment(fragment)
		if err != nil {
			return nil, err
		}
		sessionEnv[key] = value
	}
	return sessionEnv, nil
}

func splitShellAssignmentFragments(output string) ([]string, error) {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	var fragments []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	for _, r := range normalized {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteRune(r)
		case (r == '\n' || r == ';') && !inSingle && !inDouble:
			if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
				fragments = append(fragments, trimmed)
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}

	if inSingle || inDouble {
		return nil, fmt.Errorf("getSessionEnv output contains unclosed quote")
	}
	if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
		fragments = append(fragments, trimmed)
	}
	return fragments, nil
}

func parseSessionEnvAssignment(fragment string) (string, string, error) {
	line := strings.TrimSpace(fragment)
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}

	idx := strings.Index(line, "=")
	if idx <= 0 {
		return "", "", fmt.Errorf("invalid getSessionEnv assignment: %q", fragment)
	}
	key := strings.TrimSpace(line[:idx])
	if !shellKeyPattern.MatchString(key) {
		return "", "", fmt.Errorf("invalid getSessionEnv key: %q", key)
	}

	value := strings.TrimSpace(line[idx+1:])
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", "", fmt.Errorf("invalid getSessionEnv value for key %q", key)
	}
	if len(value) >= 1 {
		if (strings.HasPrefix(value, "'") && !strings.HasSuffix(value, "'")) ||
			(strings.HasPrefix(value, "\"") && !strings.HasSuffix(value, "\"")) {
			return "", "", fmt.Errorf("unbalanced quoted getSessionEnv value for key %q", key)
		}
		if len(value) >= 2 {
			if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
				value = value[1 : len(value)-1]
			}
			if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
				value = value[1 : len(value)-1]
			}
		}
	}
	return key, value, nil
}

func buildCommandEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overrides))
	for _, env := range base {
		idx := strings.Index(env, "=")
		if idx <= 0 {
			continue
		}
		merged[env[:idx]] = env[idx+1:]
	}
	for key, value := range overrides {
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+merged[key])
	}
	return result
}
