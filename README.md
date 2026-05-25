# olg-renderer-vyos

`olg-renderer-vyos` is the VyOS configuration library for OLG/uCentral desired configuration.

The repository provides a renderer that converts validated OLG/uCentral JSON into deterministic VyOS CLI `set` commands, plus an apply engine that consumes those rendered commands and applies them through the default VyOS CLI-shell executor using cloud-authoritative reset with protected roots.

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

For detailed implementation contracts and acceptance requirements, see `SPEC.md`.

---

## Current State and Intended Direction

Current implemented capabilities:

```text
renderer:
  OLG/uCentral JSON -> VyOS CLI set-command text

apply:
  VyOS set-command text -> cloud-authoritative reset with protected roots -> default VyOS executor -> commit
```

The renderer and apply engine should live in the same repository but remain separate public packages.

Recommended package split:

```text
renderer/
  public renderer API

apply/
  public apply engine API

internal/vyos:
  controlled VyOS cli-shell-api runner used by the default apply executor
```

---

## Responsibility

High-level responsibility split:

```text
renderer package:
  converts OLG/uCentral desired config into deterministic VyOS set commands

apply package:
  validates rendered commands, prepares cloud-authoritative reset operations,
  executes through the default controlled VyOS executor, and commits

olg-server-vyos-client-natagent:
  owns NATS, KV loading, temporary applied UUID state, startup reconcile,
  result/status publishing, and orchestration
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
  -> load desired config from KV
  -> compare desired UUID with temporary applied UUID
  -> if same UUID: publish already_in_sync/success and skip render/apply
  -> if different UUID: call renderer.Render(...)
  -> call apply.Engine.Apply(...)
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

## Apply package

The public `apply` package safely applies renderer-generated VyOS `set` commands using a cloud-authoritative reset model with protected roots. It validates the rendered command text, builds a deterministic reset plan, then executes that structured plan through the default VyOS CLI-shell executor unless an advanced caller overrides the executor.

### Public API

| API | Purpose |
| --- | --- |
| `apply.New(opts ...Option)` | Creates an apply engine. |
| `(*Engine).Prepare(ctx, input)` | Validates input and returns a deterministic non-executing plan. |
| `(*Engine).Apply(ctx, input)` | Validates, plans, executes through an executor, and commits. |
| `apply.GetInfo()` | Returns package metadata and defaults. |
| `(*Engine).Info()` | Returns the same metadata from an engine instance. |
| `apply.WithExecutor(exec)` | Overrides the default executor for tests or advanced controlled integrations. |
| `apply.WithSaveAfterCommit(enabled)` | Controls optional save after commit. |
| `apply.WithResetPolicy(policy)` | Replaces the reset policy after validating every root. |

### Input

`Target`, `ConfigUUID`, and `DesiredCommands` are required. For the MVP, `Target` must be `vyos`.

`ConfigUUID` is metadata only. The apply package preserves it in `Plan` and `Result`, but it does not use it for duplicate detection or applied-state comparison.

`DesiredCommands` must be non-empty renderer-generated VyOS CLI `set` command text. Empty desired commands are rejected and never produce reset-root delete commands. Empty configure payloads must not be used as reset-to-default.

### Plan and result

`Prepare` returns a `Plan` with:

```text
Target
ConfigUUID
DeleteCommands
SetCommands
Commit
Save
```

`DeleteCommands` come from the reset policy. `SetCommands` are the validated renderer commands in input order. `Commit` is true for the configure apply path. `Save` is false by default and true only when `WithSaveAfterCommit(true)` is configured.

`Apply` is the production execution API. Production callers normally call `apply.New()` and then `Apply()` directly; `Prepare()` is optional preview only and is not required before `Apply()`.

`Apply` returns a `Result` with:

```text
Target
ConfigUUID
Applied
Saved
DeleteCommands
SetCommands
CommitOutput
SaveOutput
DiscardOutput
```

On delete, set, or commit failure, the default executor attempts `discard` and returns the best partial result available with `Applied=false`. If save fails after commit, `Applied=true`, `Saved=false`, and the error code is `save_failed`.

### Reset policy

Default reset roots are:

```text
interfaces bridge
nat source
```

The default plan deletes:

```text
delete interfaces bridge
delete nat source
```

Protected roots such as `system`, `service`, `users`, `protocols`, `container`, broad `interfaces`, broad `nat`, and `interfaces ethernet` are not broadly deleted. For the MVP, custom reset policies may include only the exact allowed roots `interfaces bridge` and `nat source`; all other roots are rejected.

### Command validation

The apply parser is line-based and quote-aware. It normalizes CRLF, trims each line, ignores blank lines, rejects comment lines, rejects unclosed quotes, rejects shell/control metacharacters, requires every command to start with `set`, and allowlists only:

```text
set interfaces bridge ...
set nat source ...
set interfaces ethernet <name> description ...
```

Commands such as `commit`, `save`, `delete`, `show`, `set system ...`, `set service ...`, and unsupported ethernet changes are rejected before any executor call.

### Executor contract

`Apply` uses this boundary internally:

```go
type Executor interface {
    Execute(ctx context.Context, plan apply.Plan) (apply.ExecutionResult, error)
}
```

The default executor uses the internal VyOS runner, which invokes `/usr/bin/cli-shell-api` with one argument vector per configure operation. It enters configure mode, runs delete commands in order, runs set commands in order, commits once, and saves only when `WithSaveAfterCommit(true)` is configured.

The default executor assumes those `cli-shell-api` invocations participate in the intended VyOS candidate configuration transaction on the target image. That session behavior must be validated on the deployed VyOS version before production rollout.

The executor receives structured `DeleteCommands` and `SetCommands`; it does not receive one concatenated shell string and the apply package does not expose arbitrary command execution. `WithExecutor(...)` remains available for tests and advanced controlled integrations. Custom executors receive already validated `Plan` data from `Apply`, but they can bypass runtime execution safety if implemented incorrectly, so they must not execute concatenated shell strings or expose arbitrary command execution.

The apply package intentionally does not expose generic raw APIs such as `Run`, `Shell`, `Set`, `Commit`, `Save`, or `Show`. Separate operational action APIs for showconfig, show, save, or discard can be added later if needed, but they are not part of configure apply.

### NAT client integration

`olg-server-vyos-client-natagent` should load desired config from KV, call `renderer.Render(...)`, verify `RenderedText` is non-empty, then call `apply.Engine.Apply(...)`. The agent does not call or import executor internals separately.

The NAT client owns KV loading, NATS command handling, applied UUID state, duplicate detection, startup reconcile, and result/status publishing.

Example `Prepare` usage:

```go
engine, err := apply.New()
if err != nil {
    // handle error
}

plan, err := engine.Prepare(ctx, apply.Input{
    Target:          "vyos",
    ConfigUUID:      "cfg-123",
    DesiredCommands: rendered.RenderedText,
})
```

Example end-to-end render and apply usage:

```go
rendererEngine, err := renderer.New()
if err != nil {
    // handle error
}

rendered, err := rendererEngine.Render(ctx, renderer.Input{
    Target:        "vyos",
    ConfigUUID:    "cfg-123",
    SchemaName:    "olg-ucentral",
    SchemaVersion: "4.2.0",
    PayloadJSON:   payload,
})
if err != nil {
    // handle error
}

applier, err := apply.New()
if err != nil {
    // handle error
}

result, err := applier.Apply(ctx, apply.Input{
    Target:          "vyos",
    ConfigUUID:      rendered.ConfigUUID,
    DesiredCommands: rendered.RenderedText,
})
```

---

## Apply Engine Behavior

The initial apply mode is cloud-authoritative reset with protected roots.

At a high level, apply deletes explicit cloud-controlled reset roots, applies rendered set commands, and commits once.
It must not delete the full VyOS config and does not save by default.

Detailed reset roots, protected roots, empty command behavior, executor safety, and acceptance criteria are defined in `SPEC.md`.

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

It stores the latest desired config UUID successfully rendered/applied during the current boot and is updated only after successful render+apply.

Detailed integration and state contracts are defined in `SPEC.md`.

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
- create apply engine with apply.New()
- pass non-empty renderer output into apply.Input
- optionally call Prepare for validation/safety preview
- call Apply for real reset-root deletion and commit
- publish result/status from the caller, not from the apply package
```

Example apply usage:

```go
applier, err := apply.New()
if err != nil {
  log.Fatal(err)
}

result, err := applier.Apply(ctx, apply.Input{
  Target:          rendered.Target,
  ConfigUUID:      rendered.ConfigUUID,
  DesiredCommands: rendered.RenderedText,
})
```

Empty desired config is not a valid configure input. The renderer must not produce empty `RenderedText` for a successful configure render, and the apply package must reject empty `DesiredCommands`.
Reset/default behavior should be implemented as a separate explicit action, not by sending an empty configure payload.

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

Renderer tests include golden output comparison and deterministic rendering checks.

Apply tests cover Prepare planning, command validation, rejection of empty `DesiredCommands`, reset-root deletion for valid rendered commands, executor ordering, commit failure behavior, and save-disabled default.

Detailed test matrix and acceptance criteria are defined in `SPEC.md`.

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
- DHCP, DNS, firewall, routing, service, system, and PKI rendering
- multiple schema versions and stronger schema validation in CI
```

Future apply work may include:

```text
- configurable reset/protected roots
- commit-confirm support
- optional save-after-commit mode
- live drift inspection
- old-vs-new diff mode if required
```

---

## Detailed Design

The full implementation specification is in `SPEC.md`.

`SPEC.md` defines:

```text
- renderer contracts
- apply API contracts
- reset policy
- protected roots
- empty DesiredCommands rejection behavior
- executor safety rules
- VyOS NATS client integration contract
- use cases
- testing and acceptance criteria
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
