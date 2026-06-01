package normalize

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultDHCPLeaseSecs  = 21600
	defaultDHCPLeaseFirst = 10
	defaultDHCPLeaseCount = 100
	defaultSSHPort        = 22
)

type rawServices struct {
	SSH rawSSHService `json:"ssh"`
}

type rawSSHService struct {
	Port json.RawMessage `json:"port"`
}

type sortableServiceLAN struct {
	lan      ServiceLAN
	inputIdx int
	isVLAN   bool
	vlanID   int
}

func normalizeServices(root map[string]json.RawMessage) (ServiceSection, error) {
	sshPort, err := normalizeSSHPort(root)
	if err != nil {
		return ServiceSection{}, err
	}

	lans, err := normalizeServiceLANs(root)
	if err != nil {
		return ServiceSection{}, err
	}

	return ServiceSection{LANs: lans, SSHPort: sshPort}, nil
}

func normalizeSSHPort(root map[string]json.RawMessage) (int, error) {
	rawRootServices, ok := root["services"]
	if !ok {
		return defaultSSHPort, nil
	}

	var services rawServices
	if err := json.Unmarshal(rawRootServices, &services); err != nil {
		return 0, newError(CodeNormalizeFailed, "services must be an object", err)
	}
	if len(services.SSH.Port) == 0 {
		return defaultSSHPort, nil
	}

	port, err := parseJSONInt(services.SSH.Port)
	if err != nil {
		return 0, newError(CodeNormalizeFailed, "services.ssh.port must be an integer", err)
	}
	if port < 1 || port > 65535 {
		return 0, newError(CodeNormalizeFailed, "services.ssh.port must be in range 1..65535", nil)
	}
	return port, nil
}

func normalizeServiceLANs(root map[string]json.RawMessage) ([]ServiceLAN, error) {
	rawInterfaces, ok := root["interfaces"]
	if !ok {
		return nil, nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(rawInterfaces, &entries); err != nil {
		return nil, newError(CodeNormalizeFailed, "interfaces must be an array", err)
	}

	lans := make([]sortableServiceLAN, 0)
	for idx, rawEntry := range entries {
		var entry rawInterface
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return nil, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d] is invalid", idx), err)
		}
		if entry.Role != "downstream" || entry.IPv4.Addressing != "static" {
			continue
		}
		if entry.IPv4.Subnet == "" {
			continue
		}

		lan, err := normalizeServiceLAN(entry, idx)
		if err != nil {
			return nil, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d]: %v", idx, err), nil)
		}
		isVLAN := entry.VLAN != nil && entry.VLAN.ID > 0
		vlanID := 0
		if isVLAN {
			vlanID = entry.VLAN.ID
		}
		lans = append(lans, sortableServiceLAN{
			lan:      lan,
			inputIdx: idx,
			isVLAN:   isVLAN,
			vlanID:   vlanID,
		})
	}

	sort.SliceStable(lans, func(i, j int) bool {
		left, right := lans[i], lans[j]
		if left.isVLAN != right.isVLAN {
			return !left.isVLAN
		}
		if !left.isVLAN {
			if left.inputIdx != right.inputIdx {
				return left.inputIdx < right.inputIdx
			}
		} else if left.vlanID != right.vlanID {
			return left.vlanID < right.vlanID
		}
		if left.lan.Name != right.lan.Name {
			return left.lan.Name < right.lan.Name
		}
		return left.lan.NetIPPrefix < right.lan.NetIPPrefix
	})

	out := make([]ServiceLAN, 0, len(lans))
	for _, lan := range lans {
		out = append(out, lan.lan)
	}
	return out, nil
}

func normalizeServiceLAN(entry rawInterface, inputIdx int) (ServiceLAN, error) {
	if err := validateToken(entry.Name, "name", false); err != nil {
		return ServiceLAN{}, err
	}
	if err := validateAddressToken(entry.IPv4.Subnet, "ipv4.subnet"); err != nil {
		return ServiceLAN{}, err
	}

	prefix, err := netip.ParsePrefix(entry.IPv4.Subnet)
	if err != nil {
		return ServiceLAN{}, fmt.Errorf("ipv4.subnet must be an IPv4 prefix")
	}
	if !prefix.Addr().Is4() {
		return ServiceLAN{}, fmt.Errorf("ipv4.subnet must be an IPv4 prefix")
	}
	lanIP := prefix.Addr()
	prefix = prefix.Masked()

	leaseSecs, err := parseLeaseTime(entry.IPv4.DHCP.LeaseTime)
	if err != nil {
		return ServiceLAN{}, fmt.Errorf("invalid ipv4.dhcp.lease-time: %v", err)
	}
	leaseFirst, err := parseOptionalPositiveInt(entry.IPv4.DHCP.LeaseFirst, defaultDHCPLeaseFirst, "ipv4.dhcp.lease-first")
	if err != nil {
		return ServiceLAN{}, err
	}
	leaseCount, err := parseOptionalPositiveInt(entry.IPv4.DHCP.LeaseCount, defaultDHCPLeaseCount, "ipv4.dhcp.lease-count")
	if err != nil {
		return ServiceLAN{}, err
	}

	rangeStart, err := ipv4Add(prefix.Addr(), leaseFirst)
	if err != nil {
		return ServiceLAN{}, fmt.Errorf("invalid DHCP range start: %v", err)
	}
	rangeStop, err := ipv4Add(rangeStart, leaseCount-1)
	if err != nil {
		return ServiceLAN{}, fmt.Errorf("invalid DHCP range stop: %v", err)
	}
	if !prefix.Contains(rangeStart) || !prefix.Contains(rangeStop) {
		return ServiceLAN{}, fmt.Errorf("DHCP range is outside ipv4.subnet")
	}

	subnetID := 4051 + inputIdx
	if entry.VLAN != nil && entry.VLAN.ID > 0 {
		subnetID = entry.VLAN.ID
	}

	return ServiceLAN{
		Name:        entry.Name,
		LANIP:       lanIP.String(),
		NetIPPrefix: prefix.String(),
		LeaseSecs:   leaseSecs,
		RangeStart:  rangeStart.String(),
		RangeStop:   rangeStop.String(),
		SubnetID:    subnetID,
	}, nil
}

func parseLeaseTime(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return defaultDHCPLeaseSecs, nil
	}
	if secs, err := parseJSONInt(raw); err == nil {
		if secs < 1 {
			return 0, fmt.Errorf("must be positive")
		}
		return secs, nil
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("must be an integer, numeric string, or duration string")
	}
	secs, err := parseLeaseTimeString(value)
	if err != nil {
		return 0, err
	}
	if secs < 1 {
		return 0, fmt.Errorf("must be positive")
	}
	return secs, nil
}

func parseLeaseTimeString(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("must not be empty")
	}

	multiplier := 1
	last := value[len(value)-1]
	switch last {
	case 's':
		value = value[:len(value)-1]
	case 'm':
		value = value[:len(value)-1]
		multiplier = 60
	case 'h':
		value = value[:len(value)-1]
		multiplier = 60 * 60
	case 'd':
		value = value[:len(value)-1]
		multiplier = 24 * 60 * 60
	}

	base, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration")
	}
	if base > int(^uint(0)>>1)/multiplier {
		return 0, fmt.Errorf("duration overflows integer")
	}
	return base * multiplier, nil
}

func parseOptionalPositiveInt(raw json.RawMessage, defaultValue int, path string) (int, error) {
	if len(raw) == 0 {
		return defaultValue, nil
	}
	value, err := parseJSONIntOrString(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", path)
	}
	if value < 1 {
		return 0, fmt.Errorf("%s must be positive", path)
	}
	return value, nil
}

func parseJSONIntOrString(raw json.RawMessage) (int, error) {
	if value, err := parseJSONInt(raw); err == nil {
		return value, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	return parsed, nil
}

func ipv4Add(addr netip.Addr, offset int) (netip.Addr, error) {
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("address is not IPv4")
	}
	if offset < 0 {
		return netip.Addr{}, fmt.Errorf("negative offset")
	}
	bytes := addr.As4()
	value := binary.BigEndian.Uint32(bytes[:])
	if uint64(value)+uint64(offset) > uint64(^uint32(0)) {
		return netip.Addr{}, fmt.Errorf("address overflow")
	}
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], value+uint32(offset))
	return netip.AddrFrom4(out), nil
}
