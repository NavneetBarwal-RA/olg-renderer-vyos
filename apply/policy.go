package apply

import (
	"fmt"
	"strings"
)

const (
	resetRootInterfacesBridge    = "interfaces bridge"
	resetRootInterfacesBridgeBr0 = "interfaces bridge br0"
	resetRootNatSource           = "nat source"
	resetRootServiceDHCPServer   = "service dhcp-server"
	resetRootServiceDNSForward   = "service dns forwarding"
	resetRootServiceSSH          = "service ssh"
)

// DefaultResetPolicy returns the MVP cloud-controlled reset roots.
func DefaultResetPolicy() ResetPolicy {
	return ResetPolicy{ResetRoots: defaultResetRootList()}
}

func normalizeResetRoot(root string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(root)), " ")
}

func validateResetPolicy(policy ResetPolicy) (ResetPolicy, error) {
	roots := make([]string, 0, len(policy.ResetRoots))
	for _, root := range policy.ResetRoots {
		normalized := normalizeResetRoot(root)
		if normalized == "" {
			return ResetPolicy{}, newError(CodeInvalidInput, "reset root must not be empty", nil)
		}
		if normalized == "/" {
			return ResetPolicy{}, newError(CodeInvalidInput, "reset root must not be full config", nil)
		}
		if !isAllowedResetRoot(normalized) {
			return ResetPolicy{}, newError(CodeInvalidInput, fmt.Sprintf("unsupported reset root %q", normalized), nil)
		}
		roots = append(roots, normalized)
	}
	return ResetPolicy{ResetRoots: roots}, nil
}

func defaultResetRootList() []string {
	return []string{resetRootInterfacesBridge, resetRootNatSource, resetRootServiceDHCPServer, resetRootServiceDNSForward, resetRootServiceSSH}
}

func isAllowedResetRoot(root string) bool {
	switch root {
	case resetRootInterfacesBridge, resetRootInterfacesBridgeBr0, resetRootNatSource, resetRootServiceDHCPServer, resetRootServiceDNSForward, resetRootServiceSSH:
		return true
	default:
		return false
	}
}

func buildDeleteCommands(policy ResetPolicy) []string {
	deletes := make([]string, 0, len(policy.ResetRoots))
	for _, root := range policy.ResetRoots {
		deletes = append(deletes, "delete "+root)
	}
	return deletes
}
