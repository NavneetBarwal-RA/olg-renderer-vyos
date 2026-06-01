package vyos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	defaultShellAPI          = "/usr/bin/cli-shell-api"
	defaultConfigWrapper     = "/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper"
	defaultMySet             = "/opt/vyatta/sbin/my_set"
	defaultMyDelete          = "/opt/vyatta/sbin/my_delete"
	defaultMyCommit          = "/opt/vyatta/sbin/my_commit"
	defaultMyDiscard         = "/opt/vyatta/sbin/my_discard"
	defaultConfigWrapperName = "vyatta-cfg-cmd-wrapper"
	forbiddenConfigCmdChars  = ";|&`$><"
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Session is one VyOS candidate configuration session.
type Session interface {
	Delete(ctx context.Context, command string) (string, error)
	Set(ctx context.Context, command string) (string, error)
	Commit(ctx context.Context) (string, error)
	Save(ctx context.Context) (string, error)
	Discard(ctx context.Context) (string, error)
	Close(ctx context.Context) (string, error)
}

// Runner opens a single VyOS configuration session for an apply operation.
type Runner interface {
	Begin(ctx context.Context) (Session, error)
}

type commandRunner interface {
	Run(ctx context.Context, env []string, name string, args ...string) (string, error)
}

type osCommandRunner struct{}

func (r osCommandRunner) Run(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

type runnerPaths struct {
	shellAPI      string
	configWrapper string
	mySet         string
	myDelete      string
	myCommit      string
	myDiscard     string
}

// CLIShellRunner opens a documented VyOS CLI Shell API session and reuses its
// environment for every config operation in the apply.
type CLIShellRunner struct {
	paths     runnerPaths
	runner    commandRunner
	sessionID func() string
}

// NewCLIShellRunner returns a runner using absolute modern VyOS command paths.
func NewCLIShellRunner() *CLIShellRunner {
	return &CLIShellRunner{
		paths: runnerPaths{
			shellAPI:      defaultShellAPI,
			configWrapper: defaultConfigWrapper,
			mySet:         defaultMySet,
			myDelete:      defaultMyDelete,
			myCommit:      defaultMyCommit,
			myDiscard:     defaultMyDiscard,
		},
		runner:    osCommandRunner{},
		sessionID: func() string { return fmt.Sprintf("%d", os.Getpid()) },
	}
}

func newCLIShellRunnerForTest(paths runnerPaths, runner commandRunner, sessionID func() string) *CLIShellRunner {
	return &CLIShellRunner{paths: paths, runner: runner, sessionID: sessionID}
}

// Begin opens one VyOS candidate configuration session.
func (r *CLIShellRunner) Begin(ctx context.Context) (Session, error) {
	if r == nil || r.runner == nil {
		return nil, fmt.Errorf("vyos runner is not initialized")
	}
	if err := validatePaths(r.paths); err != nil {
		return nil, err
	}
	sessionID := r.newSessionID()
	output, err := r.runner.Run(ctx, nil, r.paths.shellAPI, "getSessionEnv", sessionID)
	if err != nil {
		return nil, commandFailure("getSessionEnv", r.paths.shellAPI, []string{"getSessionEnv", sessionID}, output, err)
	}
	env, err := parseSessionEnv(output)
	if err != nil {
		return nil, fmt.Errorf("parse getSessionEnv output failed: %w", err)
	}
	sessionEnv := mergeEnv(os.Environ(), env)
	if output, err = r.runner.Run(ctx, sessionEnv, r.paths.shellAPI, "setupSession"); err != nil {
		return nil, commandFailure("setupSession", r.paths.shellAPI, []string{"setupSession"}, output, err)
	}
	return &cliSession{paths: r.paths, runner: r.runner, env: sessionEnv}, nil
}

func (r *CLIShellRunner) newSessionID() string {
	if r.sessionID != nil {
		if id := strings.TrimSpace(r.sessionID()); id != "" {
			return id
		}
	}
	return fmt.Sprintf("%d", os.Getpid())
}

type cliSession struct {
	paths  runnerPaths
	runner commandRunner
	env    []string
}

func (s *cliSession) Delete(ctx context.Context, command string) (string, error) {
	operation, args, err := splitCLICommand(command)
	if err != nil {
		return "", err
	}
	if operation != "delete" {
		return "", fmt.Errorf("delete session command received %q operation", operation)
	}
	return s.run(ctx, s.paths.myDelete, args...)
}

func (s *cliSession) Set(ctx context.Context, command string) (string, error) {
	operation, args, err := splitCLICommand(command)
	if err != nil {
		return "", err
	}
	if operation != "set" {
		return "", fmt.Errorf("set session command received %q operation", operation)
	}
	return s.run(ctx, s.paths.mySet, args...)
}

func (s *cliSession) Commit(ctx context.Context) (string, error) {
	return s.run(ctx, s.paths.myCommit)
}

func (s *cliSession) Save(ctx context.Context) (string, error) {
	return s.run(ctx, s.paths.configWrapper, "save")
}

func (s *cliSession) Discard(ctx context.Context) (string, error) {
	return s.run(ctx, s.paths.myDiscard)
}

func (s *cliSession) Close(ctx context.Context) (string, error) {
	return s.run(ctx, s.paths.shellAPI, "teardownSession")
}

func (s *cliSession) run(ctx context.Context, name string, args ...string) (string, error) {
	if s == nil || s.runner == nil {
		return "", fmt.Errorf("vyos session is not initialized")
	}
	output, err := s.runner.Run(ctx, s.env, name, args...)
	if err != nil {
		return output, commandFailure(operationName(name, s.paths), name, args, output, err)
	}
	return output, nil
}

func validatePaths(paths runnerPaths) error {
	for name, path := range map[string]string{
		"cli-shell-api":          paths.shellAPI,
		defaultConfigWrapperName: paths.configWrapper,
		"my_set":                 paths.mySet,
		"my_delete":              paths.myDelete,
		"my_commit":              paths.myCommit,
		"my_discard":             paths.myDiscard,
	} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%s path must be absolute", name)
		}
	}
	return nil
}

func operationName(name string, paths runnerPaths) string {
	switch name {
	case paths.myDelete:
		return "delete"
	case paths.mySet:
		return "set"
	case paths.myCommit:
		return "commit"
	case paths.myDiscard:
		return "discard"
	case paths.configWrapper:
		return "save"
	case paths.shellAPI:
		return "teardownSession"
	default:
		return filepath.Base(name)
	}
}

// CommandError records a failed VyOS command invocation with enough detail to
// diagnose target-side CLI output without manually replaying the command.
type CommandError struct {
	Operation string
	Path      string
	Args      []string
	Output    string
	Err       error
}

func (e *CommandError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	if e.Operation != "" {
		b.WriteString(e.Operation)
		b.WriteString(" failed")
	} else {
		b.WriteString("command failed")
	}
	if e.Path != "" {
		b.WriteString(": command=")
		b.WriteString(strconv.Quote(e.Path))
	}
	if len(e.Args) > 0 {
		b.WriteString(" args=")
		b.WriteString(formatArgs(e.Args))
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	if detail := strings.TrimSpace(e.Output); detail != "" {
		b.WriteString(": output=")
		b.WriteString(strconv.Quote(detail))
	}
	return b.String()
}

func (e *CommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func commandFailure(operation, path string, args []string, output string, err error) error {
	return &CommandError{
		Operation: operation,
		Path:      path,
		Args:      append([]string(nil), args...),
		Output:    output,
		Err:       err,
	}
}

func formatArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = strconv.Quote(arg)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func parseSessionEnv(output string) (map[string]string, error) {
	env := map[string]string{}
	words, err := shellWords(output)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(words); i++ {
		word := words[i]
		if word == "declare" || word == "export" {
			for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") {
				i++
			}
			if i+1 >= len(words) {
				continue
			}
			i++
			if err := addEnvAssignment(env, words[i]); err != nil {
				return nil, err
			}
			continue
		}
		if strings.Contains(word, "=") {
			if err := addEnvAssignment(env, word); err != nil {
				return nil, err
			}
		}
	}
	if len(env) == 0 {
		return nil, fmt.Errorf("cli-shell-api returned no session environment")
	}
	return env, nil
}

func addEnvAssignment(env map[string]string, assignment string) error {
	key, value, ok := strings.Cut(assignment, "=")
	if !ok {
		return nil
	}
	key = strings.TrimSpace(key)
	if !envKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid session env key %q", key)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("invalid control character in session env value for %s", key)
	}
	env[key] = value
	return nil
}

func shellWords(input string) ([]string, error) {
	var words []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	tokenStarted := false
	escaped := false

	flush := func() {
		if tokenStarted {
			words = append(words, current.String())
			current.Reset()
			tokenStarted = false
		}
	}

	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}
		switch {
		case r == '\\' && !inSingle:
			escaped = true
			tokenStarted = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			tokenStarted = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			tokenStarted = true
		case (unicode.IsSpace(r) || r == ';') && !inSingle && !inDouble:
			flush()
		case (r == '{' || r == '}') && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(r)
			tokenStarted = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unbalanced quote")
	}
	flush()
	return words, nil
}

func mergeEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	seen := map[string]bool{}
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if value, exists := overrides[key]; exists {
			out = append(out, key+"="+value)
			seen[key] = true
			continue
		}
		out = append(out, item)
	}
	for key, value := range overrides {
		if !seen[key] {
			out = append(out, key+"="+value)
		}
	}
	return out
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
