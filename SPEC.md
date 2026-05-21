# olg-renderer-vyos SPEC

## 1. Overview

`olg-renderer-vyos` converts validated OLG/uCentral JSON configuration into deterministic VyOS CLI `set` commands.

The renderer is a public Go library imported by `olg-server-vyos-client-natagent`.

The renderer does not apply commands. It only produces command text.

```text
OLG/uCentral JSON
  -> renderer metadata validation
  -> normalization
  -> templates
  -> VyOS set-command text
```

---

## 2. Design Goals

The renderer must be:

```text
- deterministic
- side-effect free
- schema-version aware
- small enough to reason about
- explicit in translation behavior
- easy to test with golden fixtures
- safe to call from olg-server-vyos-client-natagent
```

For the same input payload, renderer version, schema version, and target rules, output must be identical.

---

## 3. Permanent Boundaries

`olg-renderer-vyos` must not implement:

```text
- NATS connection
- JetStream KV read/write
- configure/action handler registration
- result/status publishing
- local applied UUID state
- VyOS delete/reconcile planning
- VyOS apply/commit/save/discard
- shell command execution
- live device inspection
- runtime schema fetching
```

These responsibilities belong to `nats-agent-core`, `olg-server-vyos-client-natagent`, and the agent's internal apply/VyOS packages.

---

## 4. Public Renderer Facade

The renderer exposes a public Go facade used by `olg-server-vyos-client-natagent`.

The renderer package must not import `nats-agent-core` and must not accept `agentcore.StoredDesiredConfig` directly. The agent owns the adapter from its desired-config record to renderer input.

Expected public API direction:

```go
package renderer

func New(opts ...Option) (*Renderer, error)

func (r *Renderer) Render(ctx context.Context, input Input) (Output, error)

func GetInfo() Info

func (r *Renderer) Info() Info
```

Expected public input shape:

```go
type Input struct {
    Target        string
    ConfigUUID    string
    SchemaName    string
    SchemaVersion string
    PayloadJSON   json.RawMessage
}
```

Expected public output shape:

```go
type Output struct {
    Target        string
    ConfigUUID    string
    SchemaName    string
    SchemaVersion string
    RenderedText  string
}
```

Expected renderer info shape:

```go
type Info struct {
    Name                    string
    Version                 string
    Target                  string
    SupportedSchemaName     string
    SupportedSchemaVersions []string
}
```

Go API note:

```text
- The renderer exposes GetInfo() and (*Renderer).Info().
- Go does not allow both type Info and func Info() in the same package namespace.
- Documentation and callers should use GetInfo() for the package-level metadata accessor.
```

Facade rules:

```text
- PayloadJSON must be the actual OLG/uCentral desired config object, not the full NATS KV record.
- RenderedText must be VyOS CLI set-command text only.
- RenderedText must not include configure/commit/save/delete/show commands.
- The agent adapter maps agentcore.StoredDesiredConfig into renderer.Input.
```

Agent adapter mapping:

```text
desired.Record.Target  -> renderer.Input.Target
desired.Record.UUID    -> renderer.Input.ConfigUUID
desired.Record.Payload -> renderer.Input.PayloadJSON
configured schema name -> renderer.Input.SchemaName
configured schema ver. -> renderer.Input.SchemaVersion
```

---

## 5. Renderer Input Contract

The renderer input contains metadata plus the desired JSON payload.

Required metadata:

```text
target
config_uuid
schema_name
schema_version
payload_json
```

Expected MVP metadata values:

```text
target = vyos
schema_name = olg-ucentral
schema_version = first explicitly supported OLG/uCentral schema version
```

### Canonical `payload_json`

For the MVP, `payload_json` is the raw OLG/uCentral config object.

Example shape:

```json
{
  "interfaces": [],
  "nat": {},
  "services": {},
  "uuid": 1770891457
}
```

The renderer does not expect the NATS KV record shape.

If the desired config is wrapped by another component, the agent adapter must pass the actual OLG/uCentral config object to the renderer.

### Optional payload metadata

If payload metadata exists, it must not contradict renderer input metadata.

Accepted optional payload fields:

```text
target
schema_name
schema_version
schema.name
schema.version
uuid
```

Rules:

```text
- renderer input metadata is authoritative
- payload metadata is optional
- if payload metadata exists and conflicts with input metadata, return metadata_mismatch
- payload uuid may be used for traceability, but config_uuid remains authoritative
```

---

## 6. Renderer Output Contract

The renderer output must include:

```text
target
config_uuid
schema_name
schema_version
rendered_text
```

`rendered_text` must be VyOS CLI set-command text.

Output format:

```text
- UTF-8
- one command per line
- LF line endings
- trailing newline when output is non-empty
- deterministic command order
```

Renderer output must not include:

```text
configure
commit
save
discard
exit
delete
show
```

The renderer describes desired configuration only. Delete/reconcile behavior is handled by the agent apply layer.

---

## 7. Rendering Scope for MVP

MVP supported sections:

```text
interfaces
explicit source NAT
```

Out of MVP scope but valid future renderer sections:

```text
DHCP
DNS
firewall
routing
system
service
PKI
```

---

## 8. Interface Input Contract

The renderer reads interface data from:

```text
payload_json.interfaces
```

`interfaces` must be an array when present.

Supported interface roles:

```text
upstream
downstream
```

Unsupported roles are ignored for MVP rendering.

### Interface fields

Supported fields:

```text
interfaces[].name
interfaces[].role
interfaces[].ethernet[].select-ports[]
interfaces[].ethernet[].vlan-tag
interfaces[].ipv4.addressing
interfaces[].ipv4.subnet
interfaces[].vlan.id
```

### Interface alias policy

No interface field aliases are accepted in the MVP.

Accepted interface field names are exactly:

```text
interfaces[].ethernet
interfaces[].ethernet[].select-ports
interfaces[].ethernet[].vlan-tag
interfaces[].ipv4.addressing
interfaces[].ipv4.subnet
interfaces[].role
interfaces[].name
interfaces[].vlan.id
```

The renderer should not document or silently depend on snake_case aliases such as:

```text
select_ports
vlan_tag
```

Required missing fields should return a typed normalization error. Optional unsupported fields may be ignored.

### Required fields by case

For any renderable interface:

```text
role
ethernet[].select-ports[]
```

For dynamic IPv4:

```text
ipv4.addressing = dynamic
```

For static IPv4:

```text
ipv4.addressing = static
ipv4.subnet
```

For VLAN/VIF rendering:

```text
role = downstream
vlan.id
ipv4.subnet
```

### Interface normalization rules

```text
- upstream non-VLAN interface maps to bridge br0
- first downstream non-VLAN interface maps to bridge br1
- additional downstream non-VLAN interfaces map to br2, br3, ...
- top-level VLAN interfaces are not rendered as separate bridges
- downstream VLAN interfaces are rendered as VIFs on the downstream bridge
- static subnet values from the cloud payload must be preserved exactly
- physical interface mapping must be deterministic
```

Example physical mapping for MVP fixtures:

```text
WAN* -> eth0
LAN* -> eth1
LAN1 -> eth1
LAN2 -> eth1
```

If production mapping differs, it must be provided by renderer configuration or agent adapter input. The renderer must not discover live device interfaces.

---

## 9. Canonical Interface Example

Input:

```json
{
  "interfaces": [
    {
      "ethernet": [
        {
          "select-ports": ["WAN*"]
        }
      ],
      "ipv4": {
        "addressing": "dynamic"
      },
      "name": "WAN",
      "role": "upstream"
    },
    {
      "ethernet": [
        {
          "select-ports": ["LAN*"]
        }
      ],
      "ipv4": {
        "addressing": "static",
        "subnet": "192.168.60.1/24"
      },
      "name": "LAN",
      "role": "downstream"
    },
    {
      "ethernet": [
        {
          "select-ports": ["LAN2"],
          "vlan-tag": "auto"
        }
      ],
      "ipv4": {
        "addressing": "static",
        "subnet": "192.168.10.1/24"
      },
      "name": "LAN.10",
      "role": "downstream",
      "vlan": {
        "id": 10
      }
    }
  ]
}
```

Expected output:

```text
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'WAN'
set interfaces bridge br0 member interface eth0
set interfaces bridge br1 address 192.168.60.1/24
set interfaces bridge br1 description 'LAN'
set interfaces bridge br1 enable-vlan
set interfaces bridge br1 member interface eth1 allowed-vlan 10
set interfaces bridge br1 vif 10 address 192.168.10.1/24
set interfaces bridge br1 vif 10 description 'LAN.10'
set interfaces ethernet eth0 description 'WAN'
set interfaces ethernet eth1 description 'LAN'
```

Ordering requirements:

```text
- bridge commands before ethernet commands
- br0 before br1
- VIFs sorted by VLAN ID
- ethernet interfaces sorted by interface name
```

---

## 10. NAT Input Contract

The renderer reads explicit source NAT data from:

```text
payload_json.nat.snat.rules
```

`rules` must be an array when present.

NAT is optional. If absent, renderer must not generate NAT commands.

### Required NAT fields

Each rule must include:

```text
rule-id or rule_id
out-interface.name or out_interface.name
source.address
translation.address
```

Accepted aliases:

```text
rule-id        or rule_id
out-interface  or out_interface
```

### NAT normalization rules

```text
- rules are explicit only
- no auto-NAT generation
- output rules sorted by numeric rule ID
- cloud-provided source and translation values are preserved exactly
```

---

## 11. Canonical NAT Example

Input:

```json
{
  "nat": {
    "snat": {
      "rules": [
        {
          "rule-id": 100,
          "out-interface": {
            "name": "br0"
          },
          "source": {
            "address": "192.168.60.0/24"
          },
          "translation": {
            "address": "masquerade"
          }
        },
        {
          "rule_id": 110,
          "out_interface": {
            "name": "br0"
          },
          "source": {
            "address": "192.168.10.0/24"
          },
          "translation": {
            "address": "masquerade"
          }
        }
      ]
    }
  }
}
```

Expected output:

```text
set nat source rule 100 outbound-interface name br0
set nat source rule 100 source address 192.168.60.0/24
set nat source rule 100 translation address masquerade
set nat source rule 110 outbound-interface name br0
set nat source rule 110 source address 192.168.10.0/24
set nat source rule 110 translation address masquerade
```

---

## 12. Rendering Pipeline

The renderer must follow this pipeline:

```text
render
  -> validate context and input metadata
  -> check target/schema/schema_version compatibility
  -> decode payload_json
  -> check optional payload metadata
  -> normalize supported sections
  -> execute templates in fixed order
  -> return rendered_text
```

Initial render order:

```text
interfaces
nat
```

---

## 13. Normalization Layer

Normalization converts raw JSON into template-friendly render data.

Normalization owns:

```text
- payload parsing
- optional metadata checks
- field alias handling for NAT
- interface role filtering
- bridge naming
- VLAN/VIF grouping
- physical port mapping
- NAT rule normalization
- deterministic sorting
```

Templates should not contain business mapping decisions.

---

## 14. Template Layer

Templates format normalized data into set-command text.

Templates are implementation assets and should be added when renderer implementation begins.

Initial structure:

```text
internal/templates/
  templates.go
  interface.tmpl
  nat.tmpl

  interface/
    bridge.tmpl
    ethernet.tmpl
    vlan.tmpl
```

Responsibilities:

```text
templates.go
  embeds templates, executes top-level sections, joins output

interface.tmpl
  renders interface-related command groups

interface/bridge.tmpl
  renders bridge commands

interface/ethernet.tmpl
  renders ethernet commands

interface/vlan.tmpl
  renders VIF commands

nat.tmpl
  renders source NAT commands
```

Templates must not depend on Go map iteration order.

---

## 15. Schema Usage

Schemas are used as contracts and guardrails.

### OLG/uCentral schema

Used for:

```text
- defining supported input schema version
- validating fixtures in CI/build
- avoiding drift with olg-ucentral-client
```

The renderer should not fetch this schema at runtime.

### VyOS config schema

Used for:

```text
- validating generated command paths in tests or CI
- identifying supported target command paths for a specific VyOS build
- future compatibility checks
```

For MVP, the VyOS schema may be stored as a checked-in snapshot or documented external build artifact. Later, the VyOS build system can generate or provide the exact schema snapshot used by the target image.

### Manual translation

The schemas do not replace translation logic.

Manual renderer logic defines mappings such as:

```text
upstream -> br0
downstream -> br1
VLAN downstream -> bridge VIF
WAN* -> eth0
LAN* -> eth1
```

---

## 16. Version Compatibility

The renderer must expose supported schema metadata.

MVP compatibility should use an exact supported-version list:

```text
supported_schema_versions = ["4.2.0"]
```

The MVP supported version must match the checked-in OLG/uCentral schema metadata in:

```text
schemas/ucentral/schema.json
```

Do not use broad min/max ranges until compatibility across versions is verified.

Future compatibility can be implemented using:

```text
- explicit supported version list
- version-specific normalizers
- schema fixture validation per version
```

---

## 17. Error Model

The renderer must return typed errors with stable categories.

Required categories:

```text
invalid_input
invalid_json
unsupported_target
unsupported_schema
unsupported_schema_version
metadata_mismatch
missing_config
normalize_failed
template_failed
render_failed
```

Rules:

```text
- invalid JSON returns invalid_json
- unsupported target returns unsupported_target
- unsupported schema name returns unsupported_schema
- unsupported schema version returns unsupported_schema_version
- payload/input metadata conflict returns metadata_mismatch
- missing required config object for a supported section returns missing_config or normalize_failed
```

The agent may map all renderer errors to a single wire error code such as `render_failed`, while preserving the typed renderer error internally for logging/debugging.

---

## 18. Deterministic Output Requirements

The renderer must avoid:

```text
- random map iteration
- timestamps
- generated comments
- environment-specific values
- live-device-specific values
```

Sorting requirements:

```text
- sections sorted by fixed render order
- bridges sorted by generated bridge order
- VLANs sorted by VLAN ID
- ethernet interfaces sorted by interface name
- NAT rules sorted by numeric rule ID
```

---

## 19. Testing Requirements

### Unit tests

Required:

```text
- input metadata validation
- schema compatibility checks
- optional payload metadata mismatch
- interface normalization
- NAT alias handling
- error categories
```

### Golden tests

Required:

```text
testdata/valid/*.json
  -> render
  -> compare exactly with testdata/golden/*.set
```

Required fixtures:

```text
interface-basic.json
interface-vlan.json
nat-explicit.json
nat-absent.json
full-mvp.json
```

Required golden outputs:

```text
interface-basic.set
interface-vlan.set
nat-explicit.set
nat-absent.set
full-mvp.set
```

### Determinism test

Render the same fixture repeatedly and assert identical output.

### Schema compatibility tests

For MVP, use checked-in fixtures aligned with the supported OLG/uCentral schema version.

Later CI may:

```text
- fetch OLG/uCentral schema from pinned tag/commit
- validate fixtures against schema
- compute schema hash
- validate set-command paths against VyOS schema snapshot
```

---

## 20. Repository Layout

```text
olg-renderer-vyos/
  go.mod
  README.md
  SPEC.md

  renderer/
    renderer.go
    types.go
    errors.go

  internal/
    normalize/
      normalize.go
      interface.go
      nat.go

    templates/
      templates.go
      interface.tmpl
      nat.tmpl

      interface/
        bridge.tmpl
        ethernet.tmpl
        vlan.tmpl

  testdata/
    valid/
      interface-basic.json
      interface-vlan.json
      nat-explicit.json
      nat-absent.json
      full-mvp.json

    golden/
      interface-basic.set
      interface-vlan.set
      nat-explicit.set
      nat-absent.set
      full-mvp.set

  schemas/
    README.md
    manifest.example.json

    ucentral/
      SOURCE.md
      schema.json
      ucentral.schema.full.json

    vyos/
      SOURCE.md
      vyos-config-schema.json
```

Add future files only when implementation requires them.

---

## 21. Agent Integration Contract

The agent adapter should:

```text
- load desired config using nats-agent-core
- extract payload JSON
- provide target/config_uuid/schema_name/schema_version to renderer
- call renderer
- pass rendered_text to internal apply/reconcile engine
```

The renderer must not import agent internals.

The agent must not expose renderer internals over NATS. NATS subjects trigger handlers; handlers call internal services.

---

## 22. Apply/Reconcile Boundary

Renderer output is desired `set` commands only.

The agent apply layer owns:

```text
- comparing old desired state and new desired state
- deciding delete commands
- preserving protected config
- executing set/delete commands
- commit
- save
- discard on failure
```

The renderer should not generate delete commands in the MVP.

---

## 23. Future Roadmap

Future renderer work:

```text
- DHCP rendering
- DNS rendering
- firewall rendering
- routing rendering
- service rendering
- system rendering
- PKI rendering
- multiple schema versions
- schema fixture validation in CI
- VyOS schema command-path validation
```

Future agent-side work:

```text
- last-applied plan storage
- diff/reconcile apply engine
- protected config policy
- live config drift detection
- non-interactive VyOS operation wrapper
```

---

## 24. Acceptance Criteria for MVP

```text
- go build ./... succeeds
- go test ./... succeeds
- canonical interface example renders expected set commands
- canonical NAT example renders expected set commands
- NAT absent does not generate NAT
- unsupported target/schema/version returns typed errors
- metadata mismatch returns typed error
- output is deterministic
- no NATS/KV/apply/shell/device execution logic is added
```

---

## 25. Summary

`olg-renderer-vyos` is a pure renderer.

It receives:

```text
metadata + OLG/uCentral JSON
```

It returns:

```text
VyOS CLI set-command text
```

It does not apply, delete, reconcile, connect to NATS, or inspect the device.
