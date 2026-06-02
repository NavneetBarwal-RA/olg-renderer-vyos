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
	minServicePort = 1
	maxServicePort = 65535
)

type rawSSHService struct {
	Port json.RawMessage `json:"port"`
}

type sortableServiceLAN struct {
	lan      ServiceLAN
	inputIdx int
	isVLAN   bool
	vlanID   int
}

func normalizeServices(root map[string]json.RawMessage, inputs []ServiceLANInput) (ServiceSection, error) {
	sshPort, sshEnabled, err := normalizeSSHPort(root)
	if err != nil {
		return ServiceSection{}, err
	}

	lans, err := normalizeServiceLANs(inputs)
	if err != nil {
		return ServiceSection{}, err
	}

	return ServiceSection{LANs: lans, SSHEnabled: sshEnabled, SSHPort: sshPort}, nil
}

func normalizeSSHPort(root map[string]json.RawMessage) (int, bool, error) {
	rawRootServices, ok := root["services"]
	if !ok {
		return 0, false, nil
	}

	var services map[string]json.RawMessage
	if err := json.Unmarshal(rawRootServices, &services); err != nil {
		return 0, false, newError(CodeNormalizeFailed, "services must be an object", err)
	}

	rawSSH, ok := services["ssh"]
	if !ok {
		return 0, false, nil
	}
	if string(rawSSH) == "null" {
		return 0, false, newError(CodeNormalizeFailed, "services.ssh must be an object", nil)
	}

	var ssh rawSSHService
	if err := json.Unmarshal(rawSSH, &ssh); err != nil {
		return 0, false, newError(CodeNormalizeFailed, "services.ssh must be an object", err)
	}
	if len(ssh.Port) == 0 {
		return 0, false, nil
	}

	port, err := parseJSONInt(ssh.Port)
	if err != nil {
		return 0, false, newError(CodeNormalizeFailed, "services.ssh.port must be an integer", err)
	}
	if port < minServicePort || port > maxServicePort {
		return 0, false, newError(CodeNormalizeFailed, "services.ssh.port must be in range 1..65535", nil)
	}
	return port, true, nil
}

func normalizeServiceLANs(inputs []ServiceLANInput) ([]ServiceLAN, error) {
	lans := make([]sortableServiceLAN, 0)
	for _, input := range inputs {
		lan, err := normalizeServiceLAN(input)
		if err != nil {
			return nil, newError(CodeNormalizeFailed, fmt.Sprintf("interfaces[%d]: %v", input.InputIndex, err), nil)
		}
		lans = append(lans, sortableServiceLAN{
			lan:      lan,
			inputIdx: input.InputIndex,
			isVLAN:   input.IsVLAN,
			vlanID:   input.VLANID,
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

func normalizeServiceLAN(input ServiceLANInput) (ServiceLAN, error) {
	if err := validateToken(input.Name, "name", false); err != nil {
		return ServiceLAN{}, err
	}
	if err := validateAddressToken(input.Subnet, "ipv4.subnet"); err != nil {
		return ServiceLAN{}, err
	}

	prefix, err := netip.ParsePrefix(input.Subnet)
	if err != nil {
		return ServiceLAN{}, fmt.Errorf("ipv4.subnet must be an IPv4 prefix")
	}
	if !prefix.Addr().Is4() {
		return ServiceLAN{}, fmt.Errorf("ipv4.subnet must be an IPv4 prefix")
	}
	lanIP := prefix.Addr()
	prefix = prefix.Masked()

	leaseSecs, err := parseLeaseTime(input.DHCP.LeaseTime)
	if err != nil {
		return ServiceLAN{}, fmt.Errorf("invalid ipv4.dhcp.lease-time: %v", err)
	}
	leaseFirst, err := parseRequiredPositiveInt(input.DHCP.LeaseFirst, "ipv4.dhcp.lease-first")
	if err != nil {
		return ServiceLAN{}, err
	}
	leaseCount, err := parseRequiredPositiveInt(input.DHCP.LeaseCount, "ipv4.dhcp.lease-count")
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
	if err := validateDHCPRange(prefix, lanIP, rangeStart, rangeStop); err != nil {
		return ServiceLAN{}, err
	}

	subnetID := 4051 + input.InputIndex
	if input.IsVLAN && input.VLANID > 0 {
		subnetID = input.VLANID
	}

	return ServiceLAN{
		Name:        input.Name,
		LANIP:       lanIP.String(),
		NetIPPrefix: prefix.String(),
		LeaseSecs:   leaseSecs,
		RangeStart:  rangeStart.String(),
		RangeStop:   rangeStop.String(),
		SubnetID:    subnetID,
	}, nil
}

func validateDHCPRange(prefix netip.Prefix, lanIP, rangeStart, rangeStop netip.Addr) error {
	network := prefix.Masked().Addr()
	broadcast, err := ipv4Broadcast(prefix)
	if err != nil {
		return err
	}
	startValue := ipv4ToUint32(rangeStart)
	stopValue := ipv4ToUint32(rangeStop)
	for _, reserved := range []struct {
		name string
		addr netip.Addr
	}{
		{name: "network address", addr: network},
		{name: "broadcast address", addr: broadcast},
		{name: "LAN IP", addr: lanIP},
	} {
		value := ipv4ToUint32(reserved.addr)
		if startValue <= value && value <= stopValue {
			return fmt.Errorf("DHCP range includes %s %s", reserved.name, reserved.addr)
		}
	}
	return nil
}

func parseLeaseTime(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("must be specified")
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

func parseRequiredPositiveInt(raw json.RawMessage, path string) (int, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("%s is required", path)
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

func ipv4Broadcast(prefix netip.Prefix) (netip.Addr, error) {
	if !prefix.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("prefix is not IPv4")
	}
	network := ipv4ToUint32(prefix.Masked().Addr())
	bits := prefix.Bits()
	if bits < 0 || bits > 32 {
		return netip.Addr{}, fmt.Errorf("invalid IPv4 prefix length")
	}
	hostBits := 32 - bits
	hostMask := uint32(0)
	if hostBits == 32 {
		hostMask = ^uint32(0)
	} else if hostBits > 0 {
		hostMask = (uint32(1) << uint(hostBits)) - 1
	}
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], network|hostMask)
	return netip.AddrFrom4(out), nil
}

func ipv4ToUint32(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:])
}
