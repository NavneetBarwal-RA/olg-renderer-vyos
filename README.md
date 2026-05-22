# olg-renderer-vyos

`olg-renderer-vyos` is the VyOS configuration library for OLG/uCentral desired configuration.

The repository currently provides the renderer that converts validated OLG/uCentral JSON into deterministic VyOS CLI `set` commands. The next intended repository addition is a public apply engine that consumes those rendered commands and applies them to VyOS through cloud-authoritative reset with protected roots.

```text
OLG/uCentral JSON
  -> renderer
  -> deterministic VyOS set commands
  -> apply engine
  -> cloud-authoritative reset of cloud-controlled roots
  -> set rendered desired config
  -> commit runtime config
```

The repository must remain independent of NATS. `olg-server-vyos-client-natagent` owns NATS command handling, KV loading, result/status publishing, and orchestration.

---

## Current State and Intended Direction

Current implemented capability:

```text
renderer:
  OLG/uCentral JSON -> VyOS CLI set-command text
```

Intended next capability:

```text
apply:
  VyOS set-command text -> cloud-authoritative reset with protected roots -> commit
```

The renderer and apply engine should live in the same repository but remain separate public packages.

Recommended package split:

```text
renderer/
  public renderer API

apply/
  public apply engine API

internal/vyos/
  real VyOS command execution support
```

---

## Responsibility

`olg-renderer-vyos` owns, or is intended to own after the apply engine is added:

```text
- OLG/uCentral JSON to VyOS set-command rendering
- public renderer facade used by olg-server-vyos-client-natagent
- schema/version compatibility checks for rendering
- normalization into render-friendly data
- deterministic command ordering
- typed render errors
- renderer fixtures and golden tests
- public apply engine facade used by olg-server-vyos-client-natagent
- validation of renderer-generated set commands before apply
- cloud-authoritative reset-root apply planning for cloud-controlled reset roots
- commit-only runtime apply by default
- fake executor tests and real VyOS executor boundary
```

`olg-renderer-vyos` does not own:

```text
- NATS or JetStream connections
- KV read/write
- temporary applied UUID state persistence/comparison
- configure/action handler registration
- result/status publishing
- cloud command envelopes
- target command authorization over NATS
- live device inventory discovery
- runtime schema fetching
- cloud/client-facing APIs
```

Package-level boundaries:

```text
renderer package:
  must not apply commands, delete config, commit, save, discard, or know about NATS

apply package:
  must not render OLG/uCentral JSON and must not know about NATS subjects or KV

olg-server-vyos-client-natagent:
  loads desired config from KV
  owns temporary applied UUID state for current boot optimization
  compares desired UUID with applied UUID before renderer/apply
  performs startup reconcile from KV
  publishes already_in_sync/success when UUID matches
  updates applied UUID only after renderer and apply both succeed
  calls renderer, calls apply, and publishes results
```

---

## Related Components

```text
olg-ucentral-schema
  defines the source configuration schema and validation rules

olg-ucentral-client
  validates desired config and sends configure/action commands through the NATS flow

nats-agent-core
  provides common NATS, KV, command, result, and status behavior

olg-server-vyos-client-natagent
  handles NATS lifecycle, loads desired config, calls renderer/apply APIs, and publishes results

olg-renderer-vyos
  renders supported OLG/uCentral config and applies rendered VyOS commands through a local apply engine
```

---

## Runtime Flow

```text
olg-server-vyos-client-natagent
  -> receive cmd.configure.vyos
  -> LoadDesiredConfig(ctx, target)
  -> compare desired UUID with temporary applied UUID
  -> if same UUID: publish already_in_sync/success and skip render/apply
  -> if different UUID: build renderer input metadata
  -> call renderer.Render(...)
  -> receive rendered VyOS set commands
  -> call apply.Engine.Prepare(...) or apply.Engine.Apply(...)
  -> if Apply is called: delete cloud-controlled reset roots
  -> if Apply is called: apply rendered set commands
  -> if Apply is called: commit once
  -> update temporary applied UUID after successful apply
  -> publish configure result/status
```

The renderer receives only the desired payload and metadata provided by the caller. It does not read from NATS KV directly.

The apply engine receives only rendered VyOS set-command text and apply metadata. It does not publish NATS results and does not load desired config from KV.
`ConfigUUID` is metadata for traceability and result shaping; duplicate-detection ownership remains in the VyOS NATS client.

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
func WithPortMap(map[string][]string) Option
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

The renderer does not consume the full NATS/KV record and does not unwrap wrapper fields such as `$.config`.

If an upstream component stores or transports the desired config inside a wrapper such as:

```json
{
  "config": {
    "interfaces": [],
    "nat": {}
  }
}
```

the agent adapter must pass only the inner config object to `renderer.Input.PayloadJSON`.

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

The renderer describes the desired configuration only. Delete, apply, commit, save, discard, and device execution are apply-engine responsibilities.

---

## Public Apply Engine Facade

The apply engine is the intended next public package in this repository.

At a high level, the public package should provide:

```text
- constructor for creating an apply engine
- Prepare API for validation and pre-apply planning
- Apply API for real execution and commit
- package or instance metadata accessor if needed
```

Expected public API direction:

```go
func New(opts ...Option) (*Engine, error)
func (e *Engine) Prepare(ctx context.Context, input Input) (Plan, error)
func (e *Engine) Apply(ctx context.Context, input Input) (Result, error)
func GetInfo() Info
```

Prepare is the non-executing pre-apply API.

It validates renderer-generated set-command text and returns the deterministic cloud-authoritative reset plan that Apply would execute.

Prepare must not touch VyOS. It must not call the executor, commit, save, discard, publish status, or update any state.

Prepare exists for tests, debugging, safety review, and dry-run style inspection.

The APIs should be atomic and unique:

```text
Prepare:
  validate rendered commands and return planned delete/set operations only

Apply:
  validate, prepare, execute delete/set commands, commit, optionally save if enabled, and return execution result
```

Dry-run behavior is represented by Prepare, not by a DryRun field on Apply input.

Avoid overlapping public methods such as `ApplyCommands`, `CommitCommands`, `ApplyRenderedText`, or `PreviewApply`.

---

## Apply Engine Behavior

The initial apply mode is cloud-authoritative reset with protected roots.

Cloud desired config is the production source of truth for cloud-controlled roots.

```text
cloud-authoritative reset with protected roots:
  delete only explicit cloud-controlled reset roots
  apply rendered set commands (if any)
  commit once
```

The apply engine must not delete the full VyOS configuration.
The apply engine should always apply the commands it is given unless validation, planning, execution, commit, or save fails.

For the renderer MVP, reset roots are:

```text
- interfaces bridge
- nat source
```

Protected/default/bootstrap roots are not deleted by default:

```text
- system
- service
- interfaces ethernet
- protocols
- container
- users
- agent/bootstrap/recovery config
- any root not explicitly listed as reset-owned
```

Protected roots are preserved from blind deletion because they may contain bootstrap, management, recovery, or device-default configuration.

Preserved roots may still receive specific `set` commands from renderer output if required. Preservation only prevents broad delete operations.

Empty rendered command text is valid.

If the renderer returns no set commands, the apply engine should still delete the configured reset roots and commit. This removes all cloud-controlled config for currently supported sections while preserving protected/default/bootstrap config.

For MVP, cloud owns the `nat source` tree. The apply engine may delete `nat source` before applying rendered source NAT rules.

Because `nat source` is reset as a cloud-controlled root, a cloud-managed NAT rule ID range is not required for MVP.

Manual/debug NAT source rules are not guaranteed to survive a cloud configure apply.

Manual/debug configuration under any cloud-controlled reset root is not guaranteed to survive a cloud configure apply.

Final runtime config after successful apply:

```text
preserved protected/default/bootstrap config
  + rendered cloud desired config
```

If rendered command text is empty, the final runtime config after apply is only the preserved protected/default/bootstrap config.

---

## Commit and Save Policy

Default behavior:

```text
commit = yes
save = no
```

The KV desired config is the source of truth. VyOS saved config should remain bootstrap/recovery config by default.

After reboot:

```text
VyOS boots from saved bootstrap config
  -> agent connects to NATS
  -> agent loads latest desired config from KV
  -> renderer renders
  -> apply engine applies and commits runtime config
```

Critical requirement:

```text
The saved bootstrap config must be sufficient for the agent to reconnect to NATS/KV after reboot.
```

Saving after commit may be supported later as an explicit option, but it must not be the default MVP behavior.

---

## VyOS NATS Client Temporary Applied State

The temporary applied state belongs to `olg-server-vyos-client-natagent`.

Recommended default path:

```text
/run/olg-vyos-client/applied-state.json
```

It stores the latest desired config UUID that was successfully rendered and applied during the current boot.

Recommended state fields:

```json
{
  "target": "vyos",
  "applied_uuid": "cfg-123",
  "applied_at": "2026-05-22T00:00:00Z"
}
```

Rules:

```text
- state is updated only after renderer.Render and apply.Engine.Apply both succeed
- state is not updated after render/apply/commit failure
- state is temporary and may disappear after reboot
- if state is missing after reboot, the agent re-applies latest desired config from KV
- renderer and apply packages do not own this state
```

---

## Public Client Usage

`olg-server-vyos-client-natagent` or any other caller should interact with this library through public packages only.

Typical render flow:

```text
- build renderer.Input from desired-config metadata plus raw OLG/uCentral payload
- call renderer.New(), optionally with renderer.WithPortMap(...)
- call Render(ctx, input)
- consume output.RenderedText as deterministic VyOS set-command text
```

Example renderer usage:

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

Typical apply flow:

```text
- create apply engine with executor and reset-root/protected-root policy
- pass renderer output into apply.Input, even if RenderedText is empty
- call Prepare for validation/safety preview
- call Apply for real reset-root deletion and commit
- publish result/status from the caller, not from the apply package
```

Example apply usage after implementation:

```go
applier, err := apply.New(
  apply.WithExecutor(vyosExecutor),
  apply.WithSaveAfterCommit(false),
)

plan, err := applier.Prepare(ctx, apply.Input{
  Target:          rendered.Target,
  ConfigUUID:      rendered.ConfigUUID,
  DesiredCommands: rendered.RenderedText,
})
if err != nil {
  log.Fatal(err)
}
_ = plan

result, err := applier.Apply(ctx, apply.Input{
  Target:          rendered.Target,
  ConfigUUID:      rendered.ConfigUUID,
  DesiredCommands: rendered.RenderedText,
})
```

---

## Port Mapping

```go
r, err := renderer.New(renderer.WithPortMap(map[string][]string{
  "WAN*": {"eth10"},
  "LAN*": {"eth8", "eth9"},
}))
```

`WithPortMap` extends or overrides the default MVP fixture mapping. A selector can resolve to one or more physical interfaces; for example, `LAN*` is a group/wildcard selector.

The renderer only consumes the provided mapping. It does not read mapping files, inspect live interfaces, or fetch device inventory.

Production VyOS agent or device-profile code may load mapping data from files, inventory, or another source and pass the resolved map into the renderer.

---

## Initial Renderer MVP Scope

The implemented renderer MVP supports:

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
- allowed-vlan lines are derived from VIF IDs and VIF member-interface membership
- static cloud subnet values are preserved exactly
- physical interface mapping is deterministic and does not inspect the live device
- ethernet descriptions prefer base non-VLAN interface names over VLAN/VIF names
```

Example output:

```text
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'WAN'
set interfaces bridge br0 member interface eth0
set interfaces bridge br1 address 192.168.60.1/24
set interfaces bridge br1 description 'LAN'
set interfaces bridge br1 enable-vlan
set interfaces bridge br1 member interface eth1
set interfaces bridge br1 member interface eth2 allowed-vlan 10
set interfaces bridge br1 member interface eth2 native-vlan 1
set interfaces bridge br1 vif 10 address 192.168.10.1/24
set interfaces bridge br1 vif 10 description 'LAN.10'
set interfaces ethernet eth0 description 'WAN'
set interfaces ethernet eth1 description 'LAN'
set interfaces ethernet eth2 description 'LAN'
```

Default MVP fixture mapping:

```text
WAN* -> eth0
LAN* -> eth1, eth2
LAN1 -> eth1
LAN2 -> eth2
```

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
          "rule-id": 110,
          "out-interface": {
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

NAT field policy:

```text
The renderer uses schema-defined field names only. MVP does not define or document renderer-level aliases.
```

MVP NAT translation support:

```text
- explicit source NAT uses nat.snat.rules[].translation.address
- translation.address may be a concrete translated address
- translation.address may be a schema-supported keyword such as masquerade
```

Example output:

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

---

## Repository Layout

Desired layout after adding the apply engine:

```text
olg-renderer-vyos/
  go.mod
  README.md
  SPEC.md

  renderer/
    renderer.go
    types.go
    errors.go

  apply/
    engine.go
    types.go
    options.go
    errors.go
    parser.go
    policy.go
    planner.go

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

    vyos/
      executor.go

  testdata/
    valid/
    golden/

  schemas/
    ucentral/
    vyos/
```

Add files only when real implementation requires them.

---

## Testing

Renderer tests:

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

Apply tests:

```text
- first apply executes cloud-authoritative reset with protected roots and commit
- Prepare includes `delete interfaces bridge`
- Prepare includes `delete nat source`
- Prepare does not include delete commands for protected roots
- Prepare allows set commands under preserved roots when emitted by renderer
- empty DesiredCommands deletes reset roots and commits with no set commands
- Apply sends delete commands before set commands
- Apply performs delete + set + commit in one candidate session
- NAT rule removal is handled by deleting `nat source`
- invalid rendered command is rejected
- Prepare validates rendered commands and returns a deterministic reset-root delete/set plan
- Prepare does not execute, commit, save, discard, or update state
- commit failure returns typed error and attempts discard
- save is disabled by default
```

NATS client integration responsibility:

```text
- VyOS NATS client skips render/apply when desired UUID equals temporary applied UUID
```

Golden renderer test flow:

```text
testdata/valid/*.json
  -> render
  -> compare exactly with testdata/golden/*.set
```

For a full end-to-end renderer sample using the canonical `full-mvp` fixture, run:

```bash
go test -run TestPublicClientRenderFullMVPFlow -v ./renderer
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

Future apply work may include:

```text
- configurable reset roots
- configurable protected roots
- optional baseline set commands if required later
- optional hooks for externally owned applied-state workflows
- optional save-after-commit mode
- commit-confirm support
- live config drift inspection
- old-vs-new diff mode
- VyOS schema validation for generated command paths
```

---

## Summary

`olg-renderer-vyos` should remain:

```text
small
explicit
deterministic
schema-aware
side-effect free in renderer
safe and allowlist-based in apply
independent of NATS
```

Final repository model:

```text
renderer:
  OLG/uCentral JSON -> VyOS set commands

apply:
  VyOS set commands -> cloud-authoritative reset with protected roots -> commit runtime config

vyos-nats-agent:
  NATS lifecycle, KV loading, renderer/apply orchestration, result publishing
```
