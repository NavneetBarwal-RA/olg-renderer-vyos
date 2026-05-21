package renderer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
TC-INFO-001
Type: Positive
Title: Renderer info metadata
Summary:
Verifies that the exported renderer metadata matches the MVP contract.
This covers both the package-level metadata accessor and the instance-level
metadata accessor to ensure they stay consistent.

Validates:
  - Renderer name, target, and schema metadata are correct
  - Supported schema versions match the MVP contract
  - Instance metadata matches package metadata
*/
func TestGetInfoMetadata(t *testing.T) {
	info := GetInfo()
	if info.Name != "olg-renderer-vyos" {
		t.Fatalf("unexpected name: %q", info.Name)
	}
	if info.Target != "vyos" {
		t.Fatalf("unexpected target: %q", info.Target)
	}
	if info.SupportedSchemaName != "olg-ucentral" {
		t.Fatalf("unexpected schema name: %q", info.SupportedSchemaName)
	}
	if len(info.SupportedSchemaVersions) != 1 || info.SupportedSchemaVersions[0] != "4.2.0" {
		t.Fatalf("unexpected supported versions: %#v", info.SupportedSchemaVersions)
	}

	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	if r.Info().Target != info.Target {
		t.Fatalf("instance info mismatch")
	}
}

/*
TC-INPUT-001
Type: Negative
Title: Invalid render inputs
Summary:
Exercises the renderer's required input validation for context, metadata,
and payload presence. Each subtest passes one invalid condition and expects
the stable invalid_input error code.

Validates:
  - Nil and canceled contexts are rejected
  - Required metadata fields must be non-empty
  - Payload JSON must be present and non-blank
*/
func TestRenderInvalidInput(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	base := Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   []byte(`{"interfaces":[]}`),
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name  string
		ctx   context.Context
		input Input
	}{
		{name: "nil context", ctx: nil, input: base},
		{name: "canceled context", ctx: canceledCtx, input: base},
		{name: "missing target", ctx: context.Background(), input: withInput(base, func(in *Input) { in.Target = "" })},
		{name: "missing config uuid", ctx: context.Background(), input: withInput(base, func(in *Input) { in.ConfigUUID = "" })},
		{name: "missing schema name", ctx: context.Background(), input: withInput(base, func(in *Input) { in.SchemaName = "" })},
		{name: "missing schema version", ctx: context.Background(), input: withInput(base, func(in *Input) { in.SchemaVersion = "" })},
		{name: "missing payload", ctx: context.Background(), input: withInput(base, func(in *Input) { in.PayloadJSON = nil })},
		{name: "blank payload", ctx: context.Background(), input: withInput(base, func(in *Input) { in.PayloadJSON = []byte("  \n\t") })},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Render(tc.ctx, tc.input)
			assertErrorCode(t, err, CodeInvalidInput)
		})
	}
}

/*
TC-RENDER-001
Type: Negative
Title: Uninitialized renderer instance
Summary:
Confirms that calling Render on a zero-value renderer does not panic and
instead returns a typed renderer error. This protects callers from misuse
while keeping failure behavior deterministic.

Validates:
  - Zero-value renderer does not panic
  - Render returns a typed render_failed error
*/
func TestRenderUninitializedRenderer(t *testing.T) {
	var r Renderer

	_, err := r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   []byte(`{"interfaces":[]}`),
	})
	assertErrorCode(t, err, CodeRenderFailed)
}

/*
TC-COMPAT-001
Type: Negative
Title: Unsupported compatibility metadata
Summary:
Verifies that target, schema name, and schema version compatibility checks
fail with the correct stable error codes. Each subtest mutates one field
while keeping the rest of the input valid.

Validates:
  - Unsupported target returns unsupported_target
  - Unsupported schema name returns unsupported_schema
  - Unsupported schema version returns unsupported_schema_version
*/
func TestRenderCompatibilityChecks(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	input := Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   []byte(`{"interfaces":[]}`),
	}

	_, err = r.Render(context.Background(), withInput(input, func(in *Input) { in.Target = "ios" }))
	assertErrorCode(t, err, CodeUnsupportedTarget)

	_, err = r.Render(context.Background(), withInput(input, func(in *Input) { in.SchemaName = "other" }))
	assertErrorCode(t, err, CodeUnsupportedSchema)

	_, err = r.Render(context.Background(), withInput(input, func(in *Input) { in.SchemaVersion = "9.9.9" }))
	assertErrorCode(t, err, CodeUnsupportedSchemaVer)
}

/*
TC-INPUT-002
Type: Negative
Title: Invalid payload JSON
Summary:
Ensures that malformed JSON in PayloadJSON is detected during decoding.
The renderer should reject the payload before normalization and return
the stable invalid_json error code.

Validates:
  - Malformed JSON is rejected
  - Decoding failures return invalid_json
*/
func TestRenderInvalidJSON(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	_, err = r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   []byte(`{"interfaces":`),
	})
	assertErrorCode(t, err, CodeInvalidJSON)
}

/*
TC-META-001
Type: Negative
Title: Payload metadata mismatch
Summary:
Checks that optional metadata embedded inside the payload cannot contradict
the authoritative renderer input metadata. The test uses a mismatched target
inside the payload and expects a metadata_mismatch error.

Validates:
  - Optional payload metadata is inspected when present
  - Conflicting payload metadata is rejected
  - Mismatch failures return metadata_mismatch
*/
func TestRenderPayloadMetadataMismatch(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	payload := []byte(`{
		"target": "not-vyos",
		"schema": {
			"name": "olg-ucentral",
			"version": "4.2.0"
		},
		"interfaces": []
	}`)

	_, err = r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   payload,
	})
	assertErrorCode(t, err, CodeMetadataMismatch)
}

/*
TC-OPTION-001
Type: Positive
Title: Custom port map override
Summary:
Verifies that clients can override the default MVP selector mapping through
WithPortMap without changing payload shape. The test renders the basic
interface fixture with WAN* and LAN* mapped to custom ethernet interfaces.

Validates:
  - WithPortMap overrides default selector mappings
  - Rendered bridge member commands use the custom interfaces
  - Renderer defensively copies caller-provided map values
*/
func TestRenderWithCustomPortMap(t *testing.T) {
	portMap := map[string]string{
		"WAN*": "eth10",
		"LAN*": "eth9",
	}

	r, err := New(WithPortMap(portMap))
	if err != nil {
		t.Fatalf("new renderer with port map: %v", err)
	}

	portMap["WAN*"] = "eth99"

	payload := mustReadFile(t, filepath.Join("..", "testdata", "valid", "interface-basic.json"))
	out, err := r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-123",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   payload,
	})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	expectedLines := []string{
		"set interfaces bridge br0 member interface eth10",
		"set interfaces bridge br1 member interface eth9",
		"set interfaces ethernet eth10 description 'WAN'",
		"set interfaces ethernet eth9 description 'LAN'",
	}
	for _, line := range expectedLines {
		if !strings.Contains(out.RenderedText, line) {
			t.Fatalf("expected rendered output to contain %q\nactual:\n%s", line, out.RenderedText)
		}
	}
	if strings.Contains(out.RenderedText, "eth99") {
		t.Fatalf("renderer used mutated caller map value:\n%s", out.RenderedText)
	}
}

/*
TC-OPTION-002
Type: Negative
Title: Invalid custom port map
Summary:
Ensures WithPortMap rejects invalid caller-provided mapping entries during
renderer construction. Invalid maps should fail before rendering starts so
callers get a stable typed input error.

Validates:
  - Nil port maps are rejected
  - Empty selectors and empty interface names are rejected
  - Unsafe interface token values are rejected
*/
func TestNewRejectsInvalidPortMap(t *testing.T) {
	tests := []struct {
		name    string
		portMap map[string]string
	}{
		{name: "nil map", portMap: nil},
		{name: "empty selector", portMap: map[string]string{"": "eth0"}},
		{name: "empty interface", portMap: map[string]string{"WAN*": ""}},
		{name: "unsafe interface", portMap: map[string]string{"WAN*": "eth0;delete"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(WithPortMap(tc.portMap))
			assertErrorCode(t, err, CodeInvalidInput)
		})
	}
}

/*
TC-NORMALIZE-001
Type: Negative
Title: Duplicate NAT rule IDs
Summary:
Ensures explicit source NAT rules cannot reuse the same rule ID. Duplicate
IDs would render repeated set-command blocks for the same VyOS rule and create
ambiguous configuration intent.

Validates:
  - Duplicate NAT rule IDs are rejected
  - Duplicate detection happens before rendering
  - Failure returns normalize_failed
*/
func TestRenderRejectsDuplicateNATRuleIDs(t *testing.T) {
	payload := []byte(`{
		"nat": {
			"snat": {
				"rules": [
					{
						"rule-id": 100,
						"out-interface": {"name": "br0"},
						"source": {"address": "192.168.60.0/24"},
						"translation": {"address": "masquerade"}
					},
					{
						"rule_id": 100,
						"out_interface": {"name": "br0"},
						"source": {"address": "192.168.10.0/24"},
						"translation": {"address": "masquerade"}
					}
				]
			}
		}
	}`)

	_, err := renderPayload(t, payload)
	assertErrorCode(t, err, CodeNormalizeFailed)
}

/*
TC-NORMALIZE-002
Type: Negative
Title: Unsafe interface descriptions
Summary:
Verifies that interface names used as rendered descriptions cannot contain
characters that would break VyOS single-quoted command formatting. The test
uses a single quote because it would terminate the rendered description.

Validates:
  - Unsafe description values are rejected
  - Single quotes are not rendered into command descriptions
  - Failure returns normalize_failed
*/
func TestRenderRejectsUnsafeInterfaceDescriptions(t *testing.T) {
	payload := []byte(`{
		"interfaces": [
			{
				"ethernet": [{"select-ports": ["WAN*"]}],
				"ipv4": {"addressing": "dynamic"},
				"name": "W'AN",
				"role": "upstream"
			}
		]
	}`)

	_, err := renderPayload(t, payload)
	assertErrorCode(t, err, CodeNormalizeFailed)
}

/*
TC-NORMALIZE-003
Type: Negative
Title: Unsafe interface address values
Summary:
Checks that interface subnet values rendered as unquoted command tokens reject
whitespace and command-sensitive characters. Static subnet values are preserved
exactly in valid output, so invalid tokens must fail before templating.

Validates:
  - Unsafe static interface subnets are rejected
  - Address validation runs before template rendering
  - Failure returns normalize_failed
*/
func TestRenderRejectsUnsafeInterfaceAddressValues(t *testing.T) {
	payload := []byte(`{
		"interfaces": [
			{
				"ethernet": [{"select-ports": ["LAN*"]}],
				"ipv4": {"addressing": "static", "subnet": "192.168.60.1/24 delete"},
				"name": "LAN",
				"role": "downstream"
			}
		]
	}`)

	_, err := renderPayload(t, payload)
	assertErrorCode(t, err, CodeNormalizeFailed)
}

/*
TC-NORMALIZE-004
Type: Negative
Title: Unsafe NAT values
Summary:
Checks that NAT fields rendered as unquoted command tokens reject whitespace,
quotes, and other unsafe characters. These values are preserved exactly in
valid output, so unsafe values must be stopped during normalization.

Validates:
  - Unsafe outbound interface names are rejected
  - Unsafe source or translation addresses are rejected
  - Failures return normalize_failed
*/
func TestRenderRejectsUnsafeNATValues(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name: "unsafe outbound interface",
			payload: []byte(`{
				"nat": {
					"snat": {
						"rules": [{
							"rule-id": 100,
							"out-interface": {"name": "br0;delete"},
							"source": {"address": "192.168.60.0/24"},
							"translation": {"address": "masquerade"}
						}]
					}
				}
			}`),
		},
		{
			name: "unsafe source address",
			payload: []byte(`{
				"nat": {
					"snat": {
						"rules": [{
							"rule-id": 100,
							"out-interface": {"name": "br0"},
							"source": {"address": "192.168.60.0/24 delete"},
							"translation": {"address": "masquerade"}
						}]
					}
				}
			}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := renderPayload(t, tc.payload)
			assertErrorCode(t, err, CodeNormalizeFailed)
		})
	}
}

/*
TC-NORMALIZE-005
Type: Negative
Title: NAT alias conflicts
Summary:
Ensures accepted NAT aliases cannot provide contradictory values in the same
rule. The renderer accepts hyphen and snake_case aliases, but conflicting
values must be rejected to keep normalization deterministic.

Validates:
  - Conflicting rule-id aliases are rejected
  - Conflicting out-interface aliases are rejected
  - Failures return normalize_failed
*/
func TestRenderRejectsNATAliasConflicts(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name: "rule ID conflict",
			payload: []byte(`{
				"nat": {
					"snat": {
						"rules": [{
							"rule-id": 100,
							"rule_id": 110,
							"out-interface": {"name": "br0"},
							"source": {"address": "192.168.60.0/24"},
							"translation": {"address": "masquerade"}
						}]
					}
				}
			}`),
		},
		{
			name: "out interface conflict",
			payload: []byte(`{
				"nat": {
					"snat": {
						"rules": [{
							"rule-id": 100,
							"out-interface": {"name": "br0"},
							"out_interface": {"name": "br1"},
							"source": {"address": "192.168.60.0/24"},
							"translation": {"address": "masquerade"}
						}]
					}
				}
			}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := renderPayload(t, tc.payload)
			assertErrorCode(t, err, CodeNormalizeFailed)
		})
	}
}

/*
TC-NORMALIZE-006
Type: Negative
Title: VLAN without downstream bridge
Summary:
Verifies that downstream VLAN interfaces require a downstream non-VLAN bridge
to attach to. A VLAN-only payload has no bridge target for VIF rendering and
must fail deterministically instead of producing incomplete commands.

Validates:
  - VLAN-only interface payloads are rejected
  - Missing downstream bridge returns missing_config
  - No partial interface output is rendered
*/
func TestRenderRejectsVLANWithoutDownstreamBridge(t *testing.T) {
	payload := []byte(`{
		"interfaces": [
			{
				"ethernet": [{"select-ports": ["LAN2"], "vlan-tag": "auto"}],
				"ipv4": {"addressing": "static", "subnet": "192.168.10.1/24"},
				"name": "LAN.10",
				"role": "downstream",
				"vlan": {"id": 10}
			}
		]
	}`)

	_, err := renderPayload(t, payload)
	assertErrorCode(t, err, CodeMissingConfig)
}

/*
TC-NORMALIZE-007
Type: Positive
Title: Allowed VLANs derived from VIF IDs
Summary:
Renders duplicate VLAN IDs to verify allowed-vlan output is derived from
unique sorted VIF IDs. The VIF commands remain deterministic, while the bridge
member receives only one allowed-vlan line for the duplicated VLAN ID.

Validates:
  - VIFs are the single source of truth for VLAN IDs
  - Duplicate VIF IDs generate one allowed-vlan line
  - VIF rendering remains deterministic
*/
func TestRenderDerivesAllowedVLANsFromVIFIDs(t *testing.T) {
	payload := []byte(`{
		"interfaces": [
			{
				"ethernet": [{"select-ports": ["LAN*"]}],
				"ipv4": {"addressing": "static", "subnet": "192.168.60.1/24"},
				"name": "LAN",
				"role": "downstream"
			},
			{
				"ethernet": [{"select-ports": ["LAN2"], "vlan-tag": "auto"}],
				"ipv4": {"addressing": "static", "subnet": "192.168.10.1/24"},
				"name": "LAN.10A",
				"role": "downstream",
				"vlan": {"id": 10}
			},
			{
				"ethernet": [{"select-ports": ["LAN1"], "vlan-tag": "auto"}],
				"ipv4": {"addressing": "static", "subnet": "192.168.10.2/24"},
				"name": "LAN.10B",
				"role": "downstream",
				"vlan": {"id": 10}
			}
		]
	}`)

	out, err := renderPayload(t, payload)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if count := strings.Count(out.RenderedText, "set interfaces bridge br1 member interface eth1 allowed-vlan 10\n"); count != 1 {
		t.Fatalf("expected one allowed-vlan 10 line, got %d\n%s", count, out.RenderedText)
	}

	expectedOrder := []string{
		"set interfaces bridge br1 member interface eth1 allowed-vlan 10",
		"set interfaces bridge br1 member interface eth1 native-vlan 1",
		"set interfaces bridge br1 vif 10 address 192.168.10.1/24",
		"set interfaces bridge br1 vif 10 description 'LAN.10A'",
		"set interfaces bridge br1 vif 10 address 192.168.10.2/24",
		"set interfaces bridge br1 vif 10 description 'LAN.10B'",
	}
	last := -1
	for _, line := range expectedOrder {
		idx := strings.Index(out.RenderedText, line)
		if idx < 0 {
			t.Fatalf("expected output to contain %q\n%s", line, out.RenderedText)
		}
		if idx <= last {
			t.Fatalf("expected %q after previous line\n%s", line, out.RenderedText)
		}
		last = idx
	}
}

/*
TC-GOLDEN-001
Type: Positive
Title: Golden fixture rendering
Summary:
Renders each canonical MVP input fixture and compares the output byte-for-byte
with the expected golden set-command file. This verifies deterministic command
ordering, exact formatting, and section rendering behavior.

Validates:
  - Interface-only fixtures match expected output
  - NAT-only and NAT-absent fixtures match expected output
  - Masquerade translation.address keyword renders as translation address masquerade
  - Combined full MVP fixture matches expected output exactly
*/
func TestRenderGoldenFixtures(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	fixtures := []string{
		"interface-basic",
		"interface-vlan",
		"nat-explicit",
		"nat-absent",
		"full-mvp",
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			payload := mustReadFile(t, filepath.Join("..", "testdata", "valid", fixture+".json"))
			expected := mustReadFile(t, filepath.Join("..", "testdata", "golden", fixture+".set"))

			out, err := r.Render(context.Background(), Input{
				Target:        "vyos",
				ConfigUUID:    "cfg-123",
				SchemaName:    "olg-ucentral",
				SchemaVersion: "4.2.0",
				PayloadJSON:   payload,
			})
			if err != nil {
				t.Fatalf("render failed: %v", err)
			}

			if out.RenderedText != string(expected) {
				t.Fatalf("golden mismatch\nexpected:\n%s\nactual:\n%s", string(expected), out.RenderedText)
			}
		})
	}
}

/*
TC-DETERMINISM-001
Type: Positive
Title: Deterministic repeated rendering
Summary:
Renders the same full MVP fixture multiple times and compares every result
against the first output. This protects against accidental nondeterminism
from ordering, template behavior, or internal normalization changes.

Validates:
  - Repeated renders produce identical text
  - Output ordering is stable across runs
  - No hidden nondeterministic behavior leaks into rendering
*/
func TestRenderDeterministic(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	payload := mustReadFile(t, filepath.Join("..", "testdata", "valid", "full-mvp.json"))
	input := Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-123",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   payload,
	}

	first, err := r.Render(context.Background(), input)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	for i := 0; i < 20; i++ {
		next, err := r.Render(context.Background(), input)
		if err != nil {
			t.Fatalf("render repeat %d failed: %v", i, err)
		}
		if next.RenderedText != first.RenderedText {
			t.Fatalf("non-deterministic output at iteration %d", i)
		}
	}
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", code)
	}
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("expected renderer error, got %T (%v)", err, err)
	}
	if rerr.Code != code {
		t.Fatalf("expected error code %q, got %q (%v)", code, rerr.Code, err)
	}
}

func withInput(base Input, mutate func(*Input)) Input {
	clone := base
	mutate(&clone)
	return clone
}

func renderPayload(t *testing.T, payload []byte) (Output, error) {
	t.Helper()
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	return r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "4.2.0",
		PayloadJSON:   payload,
	})
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
