package normalize

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type rawInterface struct {
	Name     string        `json:"name"`
	Role     string        `json:"role"`
	Ethernet []rawEthernet `json:"ethernet"`
	IPv4     rawIPv4       `json:"ipv4"`
	VLAN     *rawVLAN      `json:"vlan"`
}

type rawEthernet struct {
	SelectPorts []string `json:"select-ports"`
	VLANTag     string   `json:"vlan-tag"`
}

type rawIPv4 struct {
	Addressing string  `json:"addressing"`
	DHCP       rawDHCP `json:"dhcp"`
	Subnet     string  `json:"subnet"`
}

type rawDHCP struct {
	LeaseTime  json.RawMessage `json:"lease-time"`
	LeaseFirst json.RawMessage `json:"lease-first"`
	LeaseCount json.RawMessage `json:"lease-count"`
}

type rawVLAN struct {
	ID int `json:"id"`
}

type pendingVLAN struct {
	ID               int
	Address          string
	Description      string
	MemberInterfaces []string
}

type ethernetDescription struct {
	description string
	priority    int
}

const (
	ethernetDescriptionPriorityVLAN = iota
	ethernetDescriptionPriorityBase
)

func normalizeInterfaces(root map[string]json.RawMessage, portMap map[string][]string) (InterfacesSection, error) {
	rawInterfaces, ok := root["interfaces"]
	if !ok {
		return InterfacesSection{}, nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(rawInterfaces, &entries); err != nil {
		return InterfacesSection{}, newError(CodeNormalizeFailed, "interfaces must be an array", err)
	}

	bridges := make([]Bridge, 0)
	ethDescriptions := make(map[string]ethernetDescription)
	pendingVLANs := make([]pendingVLAN, 0)

	hasUpstreamBridge := false
	firstDownstreamIdx := -1
	downstreamCount := 0

	for idx, rawEntry := range entries {
		if err := rejectInterfaceAliases(rawEntry); err != nil {
			return InterfacesSection{}, err
		}

		var entry rawInterface
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return InterfacesSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d] is invalid", idx), err)
		}

		if entry.Role != "upstream" && entry.Role != "downstream" {
			continue
		}

		if err := validateDescription(entry.Name, fmt.Sprintf("interfaces[%d].name", idx)); err != nil {
			return InterfacesSection{}, newError(CodeNormalizeFailed, err.Error(), nil)
		}

		portNames, err := resolveMappedPorts(entry.Ethernet, portMap)
		if err != nil {
			return InterfacesSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d]: %v", idx, err), nil)
		}

		isVLAN := entry.VLAN != nil && entry.VLAN.ID > 0

		if isVLAN {
			if entry.Role != "downstream" {
				continue
			}
			if entry.IPv4.Subnet == "" {
				return InterfacesSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d]: vlan interface requires ipv4.subnet", idx), nil)
			}
			if err := validateAddressToken(entry.IPv4.Subnet, fmt.Sprintf("interfaces[%d].ipv4.subnet", idx)); err != nil {
				return InterfacesSection{}, newError(CodeNormalizeFailed, err.Error(), nil)
			}
			pendingVLANs = append(pendingVLANs, pendingVLAN{
				ID:               entry.VLAN.ID,
				Address:          entry.IPv4.Subnet,
				Description:      entry.Name,
				MemberInterfaces: portNames,
			})
			for _, portName := range portNames {
				updateEthernetDescription(ethDescriptions, portName, entry.Name, ethernetDescriptionPriorityVLAN)
			}
			continue
		}

		address, err := resolveInterfaceAddress(entry.IPv4)
		if err != nil {
			return InterfacesSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d]: %v", idx, err), nil)
		}

		switch entry.Role {
		case "upstream":
			if hasUpstreamBridge {
				return InterfacesSection{}, newError(CodeNormalizeFailed, "multiple upstream non-VLAN interfaces are not supported in MVP", nil)
			}
			hasUpstreamBridge = true
			bridges = append(bridges, Bridge{
				Name:             "br0",
				Address:          address,
				Description:      entry.Name,
				MemberInterfaces: portNames,
			})
		case "downstream":
			bridgeName := "br" + strconv.Itoa(downstreamCount+1)
			bridge := Bridge{
				Name:             bridgeName,
				Address:          address,
				Description:      entry.Name,
				MemberInterfaces: portNames,
			}
			bridges = append(bridges, bridge)
			if firstDownstreamIdx < 0 {
				firstDownstreamIdx = len(bridges) - 1
			}
			downstreamCount++
		}

		for _, portName := range portNames {
			updateEthernetDescription(ethDescriptions, portName, entry.Name, ethernetDescriptionPriorityBase)
		}
	}

	if len(pendingVLANs) > 0 {
		if firstDownstreamIdx < 0 {
			return InterfacesSection{}, newError(CodeMissingConfig, "downstream VLAN interfaces require at least one downstream non-VLAN interface", nil)
		}

		sort.Slice(pendingVLANs, func(i, j int) bool {
			if pendingVLANs[i].ID == pendingVLANs[j].ID {
				if pendingVLANs[i].Description == pendingVLANs[j].Description {
					return strings.Join(pendingVLANs[i].MemberInterfaces, ",") < strings.Join(pendingVLANs[j].MemberInterfaces, ",")
				}
				return pendingVLANs[i].Description < pendingVLANs[j].Description
			}
			return pendingVLANs[i].ID < pendingVLANs[j].ID
		})

		bridge := &bridges[firstDownstreamIdx]
		for _, vlan := range pendingVLANs {
			bridge.MemberInterfaces = mergeSortedUniqueStrings(bridge.MemberInterfaces, vlan.MemberInterfaces)
			bridge.VIFs = append(bridge.VIFs, VIF{
				ID:               vlan.ID,
				Address:          vlan.Address,
				Description:      vlan.Description,
				MemberInterfaces: vlan.MemberInterfaces,
			})
		}
	}

	sort.Slice(bridges, func(i, j int) bool {
		return bridgeOrder(bridges[i].Name) < bridgeOrder(bridges[j].Name)
	})

	ethernets := make([]Ethernet, 0, len(ethDescriptions))
	for name, description := range ethDescriptions {
		ethernets = append(ethernets, Ethernet{Name: name, Description: description.description})
	}
	sort.Slice(ethernets, func(i, j int) bool {
		return ethernets[i].Name < ethernets[j].Name
	})

	return InterfacesSection{Bridges: bridges, Ethernets: ethernets}, nil
}

func rejectInterfaceAliases(rawEntry json.RawMessage) error {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(rawEntry, &entry); err != nil {
		return newError(CodeNormalizeFailed, "invalid interface entry", err)
	}

	rawEthernet, ok := entry["ethernet"]
	if !ok {
		return nil
	}

	var ethernetItems []map[string]json.RawMessage
	if err := json.Unmarshal(rawEthernet, &ethernetItems); err != nil {
		return newError(CodeNormalizeFailed, "interfaces[].ethernet must be an array", err)
	}

	for _, eth := range ethernetItems {
		if _, found := eth["select_ports"]; found {
			return newError(CodeNormalizeFailed, "interfaces[].ethernet[].select_ports alias is not supported in MVP", nil)
		}
		if _, found := eth["vlan_tag"]; found {
			return newError(CodeNormalizeFailed, "interfaces[].ethernet[].vlan_tag alias is not supported in MVP", nil)
		}
	}

	return nil
}

func resolveMappedPorts(ethernet []rawEthernet, portMap map[string][]string) ([]string, error) {
	if len(ethernet) == 0 {
		return nil, fmt.Errorf("missing ethernet[]")
	}

	seen := make(map[string]struct{})
	ports := make([]string, 0)
	for _, eth := range ethernet {
		for _, selector := range eth.SelectPorts {
			mapped, ok := portMap[selector]
			if !ok {
				continue
			}
			for _, port := range mapped {
				if _, exists := seen[port]; exists {
					continue
				}
				seen[port] = struct{}{}
				ports = append(ports, port)
			}
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("missing or unsupported ethernet[].select-ports[]")
	}
	sort.Strings(ports)
	return ports, nil
}

func updateEthernetDescription(descriptions map[string]ethernetDescription, portName, description string, priority int) {
	if description == "" {
		return
	}
	current, exists := descriptions[portName]
	// Base/non-VLAN descriptions intentionally outrank VLAN descriptions.
	// For equal priority, keep the first stable input-order value.
	if !exists || priority > current.priority {
		descriptions[portName] = ethernetDescription{description: description, priority: priority}
	}
}

func mergeSortedUniqueStrings(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	merged := make([]string, 0, len(left)+len(right))
	for _, value := range left {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	for _, value := range right {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return merged
}

func resolveInterfaceAddress(ipv4 rawIPv4) (string, error) {
	switch ipv4.Addressing {
	case "dynamic":
		return "dhcp", nil
	case "static":
		if ipv4.Subnet == "" {
			return "", fmt.Errorf("static ipv4 requires ipv4.subnet")
		}
		if err := validateAddressToken(ipv4.Subnet, "ipv4.subnet"); err != nil {
			return "", err
		}
		return ipv4.Subnet, nil
	case "":
		return "", fmt.Errorf("missing ipv4.addressing")
	default:
		return "", fmt.Errorf("unsupported ipv4.addressing value %q", ipv4.Addressing)
	}
}

func bridgeOrder(name string) int {
	if len(name) < 3 || name[:2] != "br" {
		return 1 << 30
	}
	n, err := strconv.Atoi(name[2:])
	if err != nil {
		return 1 << 30
	}
	return n
}
