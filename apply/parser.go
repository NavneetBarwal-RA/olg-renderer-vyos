package apply

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const forbiddenCommandChars = ";|&`$><"

func parseCommands(text string) ([]string, error) {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	lines := strings.Split(normalized, "\n")
	commands := make([]string, 0, len(lines))
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return nil, newError(CodeInvalidCommand, fmt.Sprintf("line %d: comments are not allowed", i+1), nil)
		}
		if strings.ContainsAny(line, forbiddenCommandChars) {
			return nil, newError(CodeInvalidCommand, fmt.Sprintf("line %d: command contains forbidden character", i+1), nil)
		}

		tokens, err := tokenizeCommand(line)
		if err != nil {
			return nil, newError(CodeInvalidCommand, fmt.Sprintf("line %d: invalid quoting", i+1), err)
		}
		if len(tokens) == 0 {
			continue
		}
		if isForbiddenOperation(tokens[0]) {
			return nil, newError(CodeInvalidCommand, fmt.Sprintf("line %d: operation %q is not allowed", i+1, tokens[0]), nil)
		}
		if tokens[0] != "set" {
			return nil, newError(CodeInvalidCommand, fmt.Sprintf("line %d: command must start with set", i+1), nil)
		}
		if err := validateSetPath(tokens); err != nil {
			return nil, newError(CodeInvalidCommand, fmt.Sprintf("line %d: unsupported set path", i+1), err)
		}
		commands = append(commands, line)
	}
	if len(commands) == 0 {
		return nil, newError(CodeEmptyDesiredCommands, "desired_commands must contain at least one set command", nil)
	}
	return commands, nil
}

func isForbiddenOperation(token string) bool {
	switch token {
	case "configure", "commit", "save", "discard", "exit", "delete", "show", "run", "sudo":
		return true
	default:
		return false
	}
}

func tokenizeCommand(line string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	tokenStarted := false

	for _, r := range line {
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
	return tokens, nil
}

func validateSetPath(tokens []string) error {
	if len(tokens) < 4 {
		return fmt.Errorf("set path is too short")
	}
	switch tokens[1] {
	case "interfaces":
		return validateInterfacesSetPath(tokens)
	case "nat":
		if tokens[2] == "source" && len(tokens) >= 4 {
			return nil
		}
		return fmt.Errorf("nat path must be nat source")
	case "service":
		return validateServiceSetPath(tokens)
	default:
		return fmt.Errorf("root %q is not supported", tokens[1])
	}
}

func validateInterfacesSetPath(tokens []string) error {
	switch tokens[2] {
	case "bridge":
		if len(tokens) >= 4 {
			return nil
		}
		return fmt.Errorf("interfaces bridge path is too short")
	case "ethernet":
		if len(tokens) >= 6 && tokens[3] != "" && tokens[4] == "description" {
			return nil
		}
		return fmt.Errorf("only interfaces ethernet <name> description is supported")
	default:
		return fmt.Errorf("interfaces path %q is not supported", tokens[2])
	}
}

func validateServiceSetPath(tokens []string) error {
	switch tokens[2] {
	case "dhcp-server":
		if len(tokens) >= 4 {
			return nil
		}
		return fmt.Errorf("service dhcp-server path is too short")
	case "dns":
		if len(tokens) >= 5 && tokens[3] == "forwarding" {
			return nil
		}
		return fmt.Errorf("service dns path must be dns forwarding")
	case "ssh":
		if len(tokens) != 5 || tokens[3] != "port" {
			return fmt.Errorf("service ssh supports only port")
		}
		port, err := strconv.Atoi(tokens[4])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("service ssh port must be in range 1..65535")
		}
		return nil
	default:
		return fmt.Errorf("service path %q is not supported", tokens[2])
	}
}
