package normalize

import (
	"fmt"
	"strings"
	"unicode"
)

func validateDescription(value, path string) error {
	if value == "" {
		return nil
	}
	for _, r := range value {
		if r == '\'' {
			return fmt.Errorf("%s contains unsupported single quote", path)
		}
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return fmt.Errorf("%s contains unsupported control character", path)
		}
	}
	return nil
}

func ValidateInterfaceToken(value, path string) error {
	return validateToken(value, path, false)
}

func validateAddressToken(value, path string) error {
	return validateToken(value, path, true)
}

func validateToken(value, path string, allowSlash bool) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", path)
	}
	for _, r := range value {
		if r > unicode.MaxASCII {
			return fmt.Errorf("%s contains non-ASCII character", path)
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '_', '-', ':':
			continue
		case '/':
			if allowSlash {
				continue
			}
		}
		if unicode.IsSpace(r) || strings.ContainsRune("'\"`;$|&<>\\", r) {
			return fmt.Errorf("%s contains unsafe character %q", path, r)
		}
		return fmt.Errorf("%s contains unsupported character %q", path, r)
	}
	return nil
}
