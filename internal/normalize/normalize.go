package normalize

import (
	"encoding/json"
	"fmt"
)

const (
	CodeMissingConfig   = "missing_config"
	CodeNormalizeFailed = "normalize_failed"
)

// Error is a typed normalization error consumed by renderer.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		if e.Err != nil {
			return fmt.Sprintf("%s: %v", e.Code, e.Err)
		}
		return e.Code
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newError(code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}

// RenderData is the normalized structure used by templates.
type RenderData struct {
	Interfaces InterfacesSection
	NAT        NATSection
}

// InterfacesSection contains normalized interface render data.
type InterfacesSection struct {
	Bridges   []Bridge
	Ethernets []Ethernet
}

// Bridge describes one normalized VyOS bridge.
type Bridge struct {
	Name            string
	Address         string
	Description     string
	MemberInterface string
	EnableVLAN      bool
	AllowedVLANs    []int
	VIFs            []VIF
}

// VIF describes one normalized bridge VIF.
type VIF struct {
	ID          int
	Address     string
	Description string
}

// Ethernet describes one normalized ethernet interface.
type Ethernet struct {
	Name        string
	Description string
}

// NATSection contains normalized source NAT rules.
type NATSection struct {
	Rules []NATRule
}

// NATRule describes one normalized source NAT rule.
type NATRule struct {
	RuleID             int
	OutboundInterface  string
	SourceAddress      string
	TranslationAddress string
}

// Normalize converts decoded payload fields into template-ready data.
func Normalize(root map[string]json.RawMessage) (RenderData, error) {
	interfaces, err := normalizeInterfaces(root)
	if err != nil {
		return RenderData{}, err
	}

	nat, err := normalizeNAT(root)
	if err != nil {
		return RenderData{}, err
	}

	return RenderData{Interfaces: interfaces, NAT: nat}, nil
}
