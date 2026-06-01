package apply

import (
	"fmt"
	"net/netip"
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
		return validateServiceDHCPSetPath(tokens)
	case "dns":
		return validateServiceDNSSetPath(tokens)
	case "ssh":
		if len(tokens) != 5 || tokens[3] != "port" {
			return fmt.Errorf("service ssh supports only port")
		}
		port, err := parsePositiveIntToken(tokens[4])
		if err != nil || port > 65535 {
			return fmt.Errorf("service ssh port must be in range 1..65535")
		}
		return nil
	default:
		return fmt.Errorf("service path %q is not supported", tokens[2])
	}
}

func validateServiceDHCPSetPath(tokens []string) error {
	if len(tokens) < 8 || tokens[3] != "shared-network-name" || tokens[4] == "" || tokens[5] != "subnet" {
		return fmt.Errorf("service dhcp-server path must use shared-network-name <name> subnet <cidr>")
	}
	if err := validateIPv4PrefixToken(tokens[6]); err != nil {
		return err
	}

	switch tokens[7] {
	case "lease":
		if len(tokens) != 9 {
			return fmt.Errorf("service dhcp-server lease command has invalid shape")
		}
		if _, err := parsePositiveIntToken(tokens[8]); err != nil {
			return fmt.Errorf("service dhcp-server lease must be positive integer")
		}
		return nil
	case "option":
		if len(tokens) != 10 {
			return fmt.Errorf("service dhcp-server option command has invalid shape")
		}
		switch tokens[8] {
		case "default-router", "name-server":
			return validateIPv4AddrToken(tokens[9])
		case "domain-name":
			if tokens[9] != "vyos.net" {
				return fmt.Errorf("service dhcp-server domain-name must be vyos.net")
			}
			return nil
		default:
			return fmt.Errorf("service dhcp-server option %q is not supported", tokens[8])
		}
	case "range":
		if len(tokens) != 11 || tokens[8] != "0" {
			return fmt.Errorf("service dhcp-server range command has invalid shape")
		}
		if tokens[9] != "start" && tokens[9] != "stop" {
			return fmt.Errorf("service dhcp-server range supports only start or stop")
		}
		return validateIPv4AddrToken(tokens[10])
	case "subnet-id":
		if len(tokens) != 9 {
			return fmt.Errorf("service dhcp-server subnet-id command has invalid shape")
		}
		if _, err := parsePositiveIntToken(tokens[8]); err != nil {
			return fmt.Errorf("service dhcp-server subnet-id must be positive integer")
		}
		return nil
	default:
		return fmt.Errorf("service dhcp-server command %q is not supported", tokens[7])
	}
}

func validateServiceDNSSetPath(tokens []string) error {
	if len(tokens) != 6 || tokens[3] != "forwarding" {
		return fmt.Errorf("service dns path must be dns forwarding")
	}
	switch tokens[4] {
	case "allow-from":
		return validateIPv4PrefixToken(tokens[5])
	case "cache-size":
		if tokens[5] != "0" {
			return fmt.Errorf("service dns forwarding cache-size must be 0")
		}
		return nil
	case "listen-address":
		return validateIPv4AddrToken(tokens[5])
	default:
		return fmt.Errorf("service dns forwarding command %q is not supported", tokens[4])
	}
}

func validateIPv4PrefixToken(token string) error {
	prefix, err := netip.ParsePrefix(token)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("expected IPv4 prefix")
	}
	return nil
}

func validateIPv4AddrToken(token string) error {
	addr, err := netip.ParseAddr(token)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("expected IPv4 address")
	}
	return nil
}

func parsePositiveIntToken(token string) (int, error) {
	value, err := strconv.Atoi(token)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("expected positive integer")
	}
	return value, nil
}
