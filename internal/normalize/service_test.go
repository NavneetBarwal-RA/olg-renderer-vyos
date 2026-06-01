package normalize

import (
	"encoding/json"
	"reflect"
	"testing"
)

/*
TC-SERVICE-NORMALIZE-001
Type: Mixed
Title: Service LAN derivation
Summary:
Builds service LANs from downstream static IPv4 interfaces while ignoring
interfaces that do not participate in DHCP and DNS forwarding. The test covers
default lease/range math and subnet ID selection for base and VLAN LANs.

Validates:
  - Upstream, dynamic, and subnetless downstream interfaces are ignored
  - LAN IP, network prefix, default lease, and range values are computed
  - Subnet IDs use 4051 + input index for base LANs and VLAN ID for VLAN LANs
*/
func TestNormalizeServicesDerivesLANs(t *testing.T) {
	root := mustRoot(t, `{
		"interfaces": [
			{"name": "WAN", "role": "upstream", "ipv4": {"addressing": "static", "subnet": "203.0.113.2/24"}},
			{"name": "LAN-DHCP", "role": "downstream", "ipv4": {"addressing": "dynamic"}},
			{"name": "LAN-MISSING", "role": "downstream", "ipv4": {"addressing": "static"}},
			{"name": "LAN", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "192.168.50.1/24"}},
			{"name": "LAN.10", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "192.168.10.1/24"}, "vlan": {"id": 10}}
		]
	}`)

	services, err := normalizeServices(root)
	if err != nil {
		t.Fatalf("normalize services: %v", err)
	}

	want := []ServiceLAN{
		{
			Name:        "LAN",
			LANIP:       "192.168.50.1",
			NetIPPrefix: "192.168.50.0/24",
			LeaseSecs:   21600,
			RangeStart:  "192.168.50.10",
			RangeStop:   "192.168.50.109",
			SubnetID:    4054,
		},
		{
			Name:        "LAN.10",
			LANIP:       "192.168.10.1",
			NetIPPrefix: "192.168.10.0/24",
			LeaseSecs:   21600,
			RangeStart:  "192.168.10.10",
			RangeStop:   "192.168.10.109",
			SubnetID:    10,
		},
	}
	if !reflect.DeepEqual(services.LANs, want) {
		t.Fatalf("unexpected LANs:\n got: %#v\nwant: %#v", services.LANs, want)
	}
}

/*
TC-SERVICE-NORMALIZE-002
Type: Positive
Title: DHCP lease parsing
Summary:
Verifies supported lease-time encodings and custom DHCP range inputs.
The renderer accepts numeric seconds, numeric strings, and duration strings
with s, m, h, and d suffixes.

Validates:
  - lease-time numbers and numeric strings parse as seconds
  - duration suffixes m, h, and d are converted to seconds
  - lease-first and lease-count override default range math
*/
func TestNormalizeServicesParsesLeaseTimes(t *testing.T) {
	tests := []struct {
		name      string
		leaseTime string
		want      int
	}{
		{name: "number", leaseTime: `3600`, want: 3600},
		{name: "numeric string", leaseTime: `"3600"`, want: 3600},
		{name: "seconds string", leaseTime: `"30s"`, want: 30},
		{name: "minutes string", leaseTime: `"30m"`, want: 1800},
		{name: "hours string", leaseTime: `"6h"`, want: 21600},
		{name: "days string", leaseTime: `"1d"`, want: 86400},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := mustRoot(t, `{
				"interfaces": [{
					"name": "LAN",
					"role": "downstream",
					"ipv4": {
						"addressing": "static",
						"subnet": "192.168.50.1/24",
						"dhcp": {
							"lease-time": `+tc.leaseTime+`,
							"lease-first": "20",
							"lease-count": 2
						}
					}
				}]
			}`)

			services, err := normalizeServices(root)
			if err != nil {
				t.Fatalf("normalize services: %v", err)
			}
			got := services.LANs[0]
			if got.LeaseSecs != tc.want || got.RangeStart != "192.168.50.20" || got.RangeStop != "192.168.50.21" {
				t.Fatalf("unexpected lease/range: %#v", got)
			}
		})
	}
}

/*
TC-SERVICE-NORMALIZE-003
Type: Negative
Title: Invalid DHCP LAN inputs
Summary:
Passes malformed service LAN fields that would otherwise produce unsafe or
invalid VyOS service commands. Normalization must fail before templating.

Validates:
  - Invalid and IPv6 service subnets are rejected
  - Invalid lease-time values are rejected
  - Unsafe LAN names and out-of-subnet DHCP ranges are rejected
*/
func TestNormalizeServicesRejectsInvalidLANInputs(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "invalid ipv4 subnet",
			payload: `{"interfaces": [{"name": "LAN", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "192.168.50.1"}}]}`,
		},
		{
			name:    "ipv6 subnet",
			payload: `{"interfaces": [{"name": "LAN", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "2001:db8::1/64"}}]}`,
		},
		{
			name:    "invalid lease time",
			payload: `{"interfaces": [{"name": "LAN", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "192.168.50.1/24", "dhcp": {"lease-time": "soon"}}}]}`,
		},
		{
			name:    "unsafe LAN name",
			payload: `{"interfaces": [{"name": "LAN bad", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "192.168.50.1/24"}}]}`,
		},
		{
			name:    "range outside subnet",
			payload: `{"interfaces": [{"name": "LAN", "role": "downstream", "ipv4": {"addressing": "static", "subnet": "192.168.50.1/30", "dhcp": {"lease-first": 10}}}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeServices(mustRoot(t, tc.payload))
			if err == nil {
				t.Fatalf("expected normalize error")
			}
		})
	}
}

/*
TC-SERVICE-NORMALIZE-004
Type: Mixed
Title: SSH port normalization
Summary:
Checks the default SSH port behavior and explicit services.ssh.port parsing.
Invalid types and out-of-range values fail with normalize errors.

Validates:
  - Missing services.ssh.port defaults to 22
  - Explicit valid SSH port is used
  - Invalid type and range are rejected
*/
func TestNormalizeServicesSSHPort(t *testing.T) {
	defaultServices, err := normalizeServices(mustRoot(t, `{}`))
	if err != nil {
		t.Fatalf("normalize default ssh: %v", err)
	}
	if defaultServices.SSHPort != 22 {
		t.Fatalf("expected default SSH port 22, got %d", defaultServices.SSHPort)
	}

	explicitServices, err := normalizeServices(mustRoot(t, `{"services": {"ssh": {"port": 2222}}}`))
	if err != nil {
		t.Fatalf("normalize explicit ssh: %v", err)
	}
	if explicitServices.SSHPort != 2222 {
		t.Fatalf("expected explicit SSH port 2222, got %d", explicitServices.SSHPort)
	}

	for _, payload := range []string{
		`{"services": {"ssh": {"port": "22"}}}`,
		`{"services": {"ssh": {"port": 0}}}`,
		`{"services": {"ssh": {"port": 65536}}}`,
	} {
		if _, err := normalizeServices(mustRoot(t, payload)); err == nil {
			t.Fatalf("expected invalid SSH port to fail for %s", payload)
		}
	}
}

func mustRoot(t *testing.T, payload string) map[string]json.RawMessage {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return root
}
