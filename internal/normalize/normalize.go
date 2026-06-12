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
	Services   ServiceSection
}

// InterfacesSection contains normalized interface render data.
type InterfacesSection struct {
	Bridges          []Bridge
	Ethernets        []Ethernet
	ServiceLANInputs []ServiceLANInput
}

// Bridge describes one normalized VyOS bridge.
type Bridge struct {
	Name             string
	Address          string
	Description      string
	MemberInterfaces []string
	VIFs             []VIF
}

// VIF describes one normalized bridge VIF. Allowed VLAN output is derived from unique sorted VIF IDs and member-interface membership.
type VIF struct {
	ID               int
	Address          string
	Description      string
	MemberInterfaces []string
}

// Ethernet describes one normalized ethernet interface.
type Ethernet struct {
	Name        string
	Description string
}

// ServiceLANInput describes an accepted downstream static IPv4 interface used
// for current DHCP and DNS forwarding service normalization. The current OLG
// schema has no separate DHCP or DNS service objects, so this inference is
// intentional current-schema behavior rather than renderer defaulting.
// TODO: When the schema adds explicit DHCP/DNS services, switch service
// rendering from interface inference to explicit schema-driven inputs.
type ServiceLANInput struct {
	InputIndex int
	Name       string
	Subnet     string
	DHCP       rawDHCP
	IsVLAN     bool
	VLANID     int
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

// ServiceSection contains normalized service render data.
type ServiceSection struct {
	LANs       []ServiceLAN
	SSHEnabled bool
	SSHPort    int
}

// ServiceLAN describes one downstream IPv4 LAN currently used by inferred DHCP
// and DNS forwarding service rendering.
type ServiceLAN struct {
	Name        string
	LANIP       string
	NetIPPrefix string
	LeaseSecs   int
	RangeStart  string
	RangeStop   string
	SubnetID    int
}

// Normalize converts decoded payload fields into template-ready data.
func Normalize(root map[string]json.RawMessage, portMap map[string][]string) (RenderData, error) {
	interfaces, err := normalizeInterfaces(root, portMap)
	if err != nil {
		return RenderData{}, err
	}

	nat, err := normalizeNAT(root)
	if err != nil {
		return RenderData{}, err
	}

	services, err := normalizeServices(root, interfaces.ServiceLANInputs)
	if err != nil {
		return RenderData{}, err
	}

	return RenderData{Interfaces: interfaces, NAT: nat, Services: services}, nil
}
