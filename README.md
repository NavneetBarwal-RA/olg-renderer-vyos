# olg-renderer-vyos

`olg-renderer-vyos` is a public Go renderer library that converts validated OLG/uCentral configuration JSON into deterministic VyOS CLI `set` commands.

The renderer is intentionally narrow. It translates desired configuration data into command text only.

```text
OLG/uCentral JSON
  -> normalize supported sections
  -> render VyOS CLI set commands
  -> return deterministic command text
```

The rendered commands are consumed by `olg-server-vyos-client-natagent`. The agent is responsible for loading desired config from NATS KV, adapting the payload for this renderer, reconciling device state, applying commands, committing, saving, and publishing configure results.

---

## Responsibility

`olg-renderer-vyos` owns:

```text
- OLG/uCentral JSON to VyOS set-command rendering
- public renderer facade used by olg-server-vyos-client-natagent
- schema/version compatibility checks
- normalization into render-friendly data
- deterministic command ordering
- typed render errors
- renderer fixtures and golden tests
```

`olg-renderer-vyos` does not own:

```text
- NATS or JetStream connections
- KV read/write
- configure/action handler registration
- result/status publishing
- local applied UUID state
- VyOS delete/reconcile planning
- VyOS apply/commit/save/discard
- shell command execution
- live device inspection
- runtime schema fetching
```

---

## Related Components

```text
olg-ucentral-schema
  defines the source configuration schema and validation rules

olg-ucentral-client
  validates desired config and sends it over NATS

nats-agent-core
  provides common NATS, KV, command, result, and status behavior

olg-server-vyos-client-natagent
  loads desired config, calls the renderer, reconciles/applies commands, and publishes results

olg-renderer-vyos
  renders supported OLG/uCentral config into VyOS CLI set commands
```

---

## Runtime Flow

```text
olg-server-vyos-client-natagent
  -> LoadDesiredConfig(ctx, target)
  -> extract desired payload
  -> build renderer input metadata
  -> call olg-renderer-vyos
  -> receive rendered VyOS set commands
  -> pass commands to internal apply/reconcile engine
```

The renderer receives only the desired payload and metadata provided by the caller. It does not read from NATS KV directly.

---

## Public Renderer Facade

The renderer exposes a small public Go API for `olg-server-vyos-client-natagent`.

At a high level, the public package provides:

```text
- constructor for creating a renderer instance
- render method that accepts metadata and OLG/uCentral payload JSON
- package-level metadata accessor via GetInfo()
- instance metadata accessor via (*Renderer).Info()
```

The renderer package must not import `nats-agent-core` and must not accept `agentcore.StoredDesiredConfig` directly. The agent owns the adapter from its desired-config record to renderer input.

Expected public API:

```go
func New(opts ...Option) (*Renderer, error)
func WithPortMap(map[string]string) Option
func (r *Renderer) Render(ctx context.Context, input Input) (Output, error)
func GetInfo() Info
func (r *Renderer) Info() Info
```

Go API note:

```text
- Go does not allow both type Info and func Info() in the same package namespace.
- The package-level metadata accessor is therefore GetInfo(), not Info().
```

---

## Canonical Renderer Input

The renderer receives metadata separately from the desired config payload.

Metadata:

```text
target
config_uuid
schema_name
schema_version
```

Payload:

```text
payload_json
```

For the MVP, `payload_json` is the raw OLG/uCentral config object, for example:

```json
{
  "interfaces": [],
  "nat": {},
  "services": {},
  "uuid": 1770891457
}
```

If an upstream component stores or transports the desired config inside a wrapper, the agent adapter should unwrap it before calling the renderer.

---

## Canonical Renderer Output

The renderer returns UTF-8 VyOS CLI `set` commands.

Output rules:

```text
- one command per line
- LF line endings
- trailing newline when output is non-empty
- deterministic command order
- no configure/commit/save/discard/exit commands
- no delete commands
- no show commands
```

Example:

```text
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'WAN'
set interfaces bridge br0 member interface eth0
```

The renderer describes the desired configuration only. Delete, reconcile, apply, commit, save, discard, and device execution are agent responsibilities.

---

## Public Client Usage

`olg-server-vyos-client-natagent` or any other caller should interact with this library through the public `renderer` package only.

Typical client flow:

```text
- build renderer.Input from desired-config metadata plus the raw OLG/uCentral payload
- call renderer.New(), optionally with renderer.WithPortMap(...)
- call Render(ctx, input)
- consume output.RenderedText as deterministic VyOS set-command text
```

Example:

```go
package main

import (
  "context"
  "encoding/json"
  "log"

  "github.com/routerarchitects/olg-renderer-vyos/renderer"
)

func main() {
  r, err := renderer.New()
  if err != nil {
    log.Fatal(err)
  }

  payload := json.RawMessage(`{
    "interfaces": [
      {
        "ethernet": [{"select-ports": ["WAN*"]}],
        "ipv4": {"addressing": "dynamic"},
        "name": "WAN",
        "role": "upstream"
      }
    ],
    "nat": {
      "snat": {
        "rules": [
          {
            "rule-id": 100,
            "out-interface": {"name": "br0"},
            "source": {"address": "192.168.60.0/24"},
            "translation": {"address": "masquerade"}
          }
        ]
      }
    }
  }`)

  input := renderer.Input{
    Target:        "vyos",
    ConfigUUID:    "cfg-123",
    SchemaName:    "olg-ucentral",
    SchemaVersion: "4.2.0",
    PayloadJSON:   payload,
  }

  out, err := r.Render(context.Background(), input)
  if err != nil {
    log.Fatal(err)
  }

  log.Printf("rendered config %s for target %s", out.ConfigUUID, out.Target)
  log.Print(out.RenderedText)
}
```

Port mapping:

```go
r, err := renderer.New(renderer.WithPortMap(map[string]string{
  "WAN*": "eth10",
  "LAN*": "eth9",
}))
```

`WithPortMap` extends or overrides the default MVP fixture mapping. The renderer only consumes the provided mapping; it does not read mapping files, inspect live interfaces, or fetch device inventory. Production VyOS agent or device-profile code may load mapping data from files, inventory, or another source and pass the resolved map into the renderer.

For a full end-to-end sample using the canonical `full-mvp` fixture, run:

```bash
go test -run TestPublicClientRenderFullMVPFlow -v ./renderer
```

That sample test prints:

```text
- the input fixture JSON
- the rendered VyOS set-command output
- PASS/FAIL status against testdata/golden/full-mvp.set
```

This sample is best treated as an integration-style library contract test because it exercises the public API, normalization, templates, and golden fixtures together.

---

## Initial MVP Scope

The first implementation should support:

```text
- interface rendering
- explicit source NAT rendering
- target/schema/version compatibility checks
- deterministic set-command output
- golden tests
```

NAT is explicit and optional. If NAT is absent from the input, the renderer must not generate NAT rules.

---

## Interface Rendering Example

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

Expected behavior:

```text
- upstream non-VLAN interface becomes br0
- first downstream non-VLAN interface becomes br1
- downstream VLAN interfaces become VIFs on the downstream bridge
- static cloud subnet values are preserved exactly
- physical interface mapping is deterministic and does not inspect the live device
```

Example output:

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

Default MVP fixture mapping:

```text
WAN* -> eth0
LAN* -> eth1
LAN1 -> eth1
LAN2 -> eth1
```

Production mapping should be resolved by the agent or device-profile layer and passed into the renderer with `WithPortMap`. The renderer must remain side-effect free and must not load mapping files or inspect the live device.

---

## Explicit NAT Rendering Example

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

Accepted NAT aliases:

```text
rule-id        or rule_id
out-interface  or out_interface
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

## Schema Usage

Schemas are guardrails, not automatic renderer generators.

```text
OLG/uCentral schema
  source-side input contract and fixture validation

manual renderer logic
  semantic translation from OLG/uCentral intent to VyOS set commands

VyOS config schema
  target-side command path validation and build compatibility guardrail

golden tests
  deterministic proof of expected rendering
```

The renderer must not fetch schemas from Git or the network at runtime.

For MVP:

```text
- support one agreed OLG/uCentral schema version
- keep renderer compatibility metadata in code/docs
- validate examples and fixtures through tests
- keep rendering logic manual and explicit
```

Later:

```text
- fetch pinned OLG/uCentral schema files during CI/build
- include schema hashes in renderer metadata
- use VyOS schema snapshots from the VyOS build system
- validate generated set-command paths against the VyOS schema
```

---

## Version Compatibility

The MVP should use exact schema-version support.

Example:

```text
supported schema versions:
- 4.2.0
```

The MVP supported version matches the checked-in OLG/uCentral schema metadata in `schemas/ucentral/schema.json`.

Avoid broad min/max ranges until compatibility across versions is understood.

Future support can be added as:

```text
supported schema versions:
- 4.2.0
- 4.3.0
```

or through version-specific normalizers when schema changes require different translation behavior.

---

## Repository Layout

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
      nat-explicit.json
      interface-vlan.json
      nat-absent.json
      full-mvp.json

    golden/
      interface-basic.set
      nat-explicit.set
      interface-vlan.set
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

Add files only when real renderer logic requires them.

---

## Template Direction

Templates are implementation assets. They should be added when renderer implementation begins.

Templates should format already-normalized render data.

```text
normalize/
  decides what should be rendered

templates/
  decides how normalized data should look as VyOS set-command text
```

Planned MVP template files:

```text
internal/templates/interface.tmpl
internal/templates/interface/bridge.tmpl
internal/templates/interface/ethernet.tmpl
internal/templates/interface/vlan.tmpl
internal/templates/nat.tmpl
```

`templates.go` should control top-level order:

```text
interfaces
nat
```

No root template is required for the MVP.

---

## Testing

Required tests:

```text
- valid interface rendering
- valid VLAN rendering
- valid explicit NAT rendering
- NAT absent behavior
- invalid JSON
- unsupported target
- unsupported schema name
- unsupported schema version
- metadata mismatch
- deterministic output
```

Golden test flow:

```text
testdata/valid/*.json
  -> render
  -> compare exactly with testdata/golden/*.set
```

---

## Future Roadmap

Future renderer work may include:

```text
- DHCP set-command rendering
- DNS set-command rendering
- firewall set-command rendering
- routing set-command rendering
- system set-command rendering
- service set-command rendering
- PKI set-command rendering
- multiple schema versions
- schema fixture validation in CI
- VyOS schema command-path validation
```

Future agent-side work belongs outside this repo:

```text
- delete/reconcile planning
- last-applied state comparison
- protected device config policy
- VyOS commit/save/discard handling
- show/get operational commands
```

---

## Summary

`olg-renderer-vyos` should remain:

```text
small
pure
deterministic
schema-aware
template-driven
focused only on JSON-to-set-command rendering
```
