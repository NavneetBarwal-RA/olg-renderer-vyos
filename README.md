# olg-renderer-vyos

`olg-renderer-vyos` is the VyOS configuration library for OLG/uCentral desired configuration.

The repository provides a renderer that converts validated OLG/uCentral JSON into deterministic VyOS CLI `set` commands, plus an apply engine that consumes those rendered commands and applies them through the default persistent VyOS CLI Shell API session executor using managed-root reconciliation.

```text
OLG/uCentral JSON
  -> renderer
  -> deterministic VyOS set commands
  -> apply engine
  -> managed-root reconciliation
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
  VyOS set-command text -> managed-root reconciliation -> default VyOS executor -> commit
```

The renderer and apply engine should live in the same repository but remain separate public packages.

Recommended package split:

```text
renderer/
  public renderer API

apply/
  public apply engine API

internal/vyos:
  controlled VyOS session runner used by the default apply executor
```

---

## Responsibility

High-level responsibility split:

```text
renderer package:
  converts OLG/uCentral desired config into deterministic VyOS set commands

apply package:
  validates rendered commands, prepares managed-root reconciliation operations,
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
set service dhcp-server shared-network-name LAN subnet 192.168.60.0/24 lease 21600
set service dns forwarding cache-size 0
set service ssh port 22
```

The renderer describes the desired configuration only. Delete, apply, commit, save, discard, and device execution are apply-engine responsibilities.

---

## Apply package

The public `apply` package safely applies renderer-generated VyOS `set` commands using managed-root reconciliation. It validates the rendered command text, builds a deterministic plan that replaces only explicitly managed roots, then executes that structured plan through the default internal VyOS CLI Shell API session executor unless an advanced caller overrides the executor.

Current default behavior:

```text
- explicit managed roots
- delete managed roots
- recreate desired config under managed roots
- commit runtime config
- save only if requested
- preserve everything outside managed roots
```

Not current behavior:

```text
- whole-device reconciliation is not current behavior
- delete everything except a preserve whitelist is not current behavior
- dependency on vyatta-save-config.pl is not current behavior
```

Managed-root reconciliation is also managed-subtree replacement: if a root is managed, apply deletes that root and recreates the desired subtree under it. If a root is not managed, apply leaves it alone by default.

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

`DesiredCommands` must be non-empty renderer-generated VyOS CLI `set` command text. Empty desired commands are rejected and never produce managed-root delete commands. Empty configure payloads must not be used as reset-to-default.

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
DeleteOutput
SetOutput
CommitOutput
SaveOutput
DiscardOutput
```

On delete, set, or commit failure, the default executor attempts `discard` and returns the best partial result available with `Applied=false`. If save fails after commit, `Applied=true`, `Saved=false`, and the error code is `save_failed`.

### Apply Ownership Model

The renderer/apply engine does not own the whole VyOS device configuration. It owns only declared managed roots. During apply, it deletes those managed roots, recreates desired configuration under those roots, and preserves configuration outside managed roots by default.

Absence of a config item from desired state deletes it only when that item is inside a managed root. Manual changes under a managed root are expected to be overwritten on the next apply; manual changes outside managed roots are preserved by default.

Currently managed roots are defined by `apply.DefaultResetPolicy()` in [apply/policy.go](apply/policy.go):

```text
interfaces bridge
nat source
service dhcp-server
service dns forwarding
```

The default plan deletes:

```text
delete interfaces bridge
delete nat source
delete service dhcp-server
delete service dns forwarding
```

Unmanaged VyOS config must not be deleted by the default apply path, including `system login`, `system config-management`, broad `service`, `service ssh`, `service ntp`, `interfaces loopback`, `interfaces ethernet` / WAN access, and other non-owned configuration. For this phase, whole-device reconciliation is not the design because an incomplete preserve list could delete SSH, login, WAN, NTP, or other system config. Full-device reconciliation, if ever added, must be a separate explicit mode with stronger safeguards.

For the MVP, custom reset policies may include only the exact allowed roots `interfaces bridge`, `interfaces bridge br0` for targeted lab smoke, `nat source`, `service dhcp-server`, and `service dns forwarding`; all other roots are rejected. `service ssh` is intentionally not a default reset root.

### Command validation

The apply parser is line-based and quote-aware. It normalizes CRLF, trims each line, ignores blank lines, rejects comment lines, rejects unclosed quotes, rejects shell/control metacharacters, requires every command to start with `set`, and allowlists only:

```text
set interfaces bridge ...
set nat source ...
set interfaces ethernet <name> description ...
set service dhcp-server shared-network-name <name> subnet <cidr> <renderer-emitted DHCP leaf>
set service dns forwarding <allow-from|cache-size|listen-address> <value>
set service ssh port <port>
```

Service command validation is intentionally narrow: unsupported DHCP/DNS/SSH subcommands and broad service paths are rejected before any executor call. Commands such as `commit`, `save`, `delete`, `show`, `set system ...`, broad or unsupported `set service ...`, and unsupported ethernet changes are also rejected.

### Executor contract

`Apply` uses this boundary internally:

```go
type Executor interface {
    Execute(ctx context.Context, plan apply.Plan) (apply.ExecutionResult, error)
}
```

The default executor uses one persistent VyOS CLI Shell API configuration session:

```text
acquire /run/lock/olg-vyos-apply.lock
/usr/bin/cli-shell-api getSessionEnv <apply-process-id>
/usr/bin/cli-shell-api setupSession
/opt/vyatta/sbin/my_delete ...
/opt/vyatta/sbin/my_set ...
/opt/vyatta/sbin/my_commit
optional /opt/vyatta/sbin/vyatta-cfg-cmd-wrapper save when save is enabled
/opt/vyatta/sbin/my_discard on failure before commit
/usr/bin/cli-shell-api teardownSession
release /run/lock/olg-vyos-apply.lock
```

The default session identifier is the apply process ID. The session environment returned by `getSessionEnv` is reused for every delete, set, commit, save, discard, and teardown operation in that apply. The executor does not run independent stateless wrapper `begin`, `set`, `commit`, and `end` calls after opening a session.

On delete, set, or commit failure, the executor attempts `/opt/vyatta/sbin/my_discard` before returning the typed apply error. Cleanup uses a bounded context that ignores caller cancellation so discard and teardown are still attempted after cancellation. Save remains disabled by default unless `WithSaveAfterCommit(true)` is configured. `save=false` is runtime commit only and does not require or call any save helper. `save=true` persists configuration by passing `save` through `/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper` with the same session environment; enable it only after manually validating that save mechanism on the target VyOS image. Modern VyOS rolling images may not have `/opt/vyatta/sbin/vyatta-save-config.pl`, and this repository must not depend on it.

The local apply lock prevents two agent apply operations from running concurrently in this process/device path. It does not prevent a human from opening `configure`. Hard process kills, VM crashes, or `SIGKILL` can still interrupt cleanup; startup cleanup or a reboot may be required if VyOS reports stale candidate overlays.

The executor receives structured `DeleteCommands` and `SetCommands`; it does not receive one concatenated shell string and the apply package does not expose arbitrary command execution. `WithExecutor(...)` remains available for tests and advanced controlled integrations. Custom executors receive already validated `Plan` data from `Apply`, but they can bypass runtime execution safety if implemented incorrectly, so they must not execute concatenated shell strings or expose arbitrary command execution.

The apply package intentionally does not expose generic raw APIs such as `Run`, `Shell`, `Set`, `Commit`, `Save`, or `Show`. Separate operational action APIs for showconfig, show, save, or discard can be added later if needed, but they are not part of configure apply.

### NAT client integration

`olg-server-vyos-client-natagent` should load desired config from KV, call `renderer.Render(...)`, verify `RenderedText` is non-empty, then call `apply.Engine.Apply(...)`. The agent does not call or import executor internals separately.

The NAT client owns KV loading, NATS command handling, applied UUID state, duplicate detection, startup reconcile, and result/status publishing.

Lab-only smoke test guidance for manual VyOS validation is documented in [docs/vyos-apply-smoke.md](docs/vyos-apply-smoke.md).

### Lab-only VyOS smoke test

The repository includes an opt-in manual smoke command for disposable/lab VyOS targets. It is not run by CI and it is not a replacement for the fake executor/fake runner unit tests.

Preview without applying:

```bash
go run ./cmd/vyos-apply-smoke --i-understand-this-modifies-vyos --mode minimal-targeted --skip-apply
```

Run on a lab VyOS VM/device:

```bash
go run ./cmd/vyos-apply-smoke --i-understand-this-modifies-vyos --mode minimal-targeted --save=false
```

The command logs required binary checks, apply input, `Prepare` plan details, `Apply` result fields, errors, and cleanup guidance with a `[smoke]` prefix. Expected output includes:

```text
[smoke] starting VyOS apply smoke test
[smoke] checking required binaries
[smoke] found /usr/bin/cli-shell-api
[smoke] found /opt/vyatta/sbin/my_set
[smoke] found /opt/vyatta/sbin/my_delete
[smoke] found /opt/vyatta/sbin/my_commit
[smoke] found /opt/vyatta/sbin/my_discard
[smoke] previewing plan with Prepare
[smoke] plan delete_count=1 set_count=3 commit=true save=false
[smoke] applying plan through Apply
[smoke] result applied=true saved=false
[smoke] completed successfully
```

Smoke interpretation: the default `minimal-targeted` preview plan deletes `interfaces bridge br0`, then recreates it with DHCP, the smoke description, and `eth0` membership:

```text
delete interfaces bridge br0
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
```

Use `--mode minimal-managed` when you intentionally want the smoke command to exercise the normal managed-root policy with `delete interfaces bridge`, `delete nat source`, `delete service dhcp-server`, and `delete service dns forwarding`. The smoke payload intentionally does not change `interfaces ethernet eth0`. Manual changes under `interfaces bridge br0` are expected to be overwritten on the next targeted smoke apply because that node is reset by the smoke policy.

Verification commands for a lab VyOS VM:

```bash
show configuration commands | match "interfaces bridge"
show configuration commands | match "OLG_APPLY_SMOKE_TEST"
show configuration commands | match "interfaces ethernet eth0 description"
```

The ethernet description should remain whatever it was before the smoke test.

Warning: `minimal-targeted` deletes and recreates `interfaces bridge br0` with DHCP and `eth0` membership; management networking can briefly flap during commit. `minimal-managed` uses the normal apply policy and may delete `interfaces bridge`, `nat source`, `service dhcp-server`, and `service dns forwarding`. Restore by re-applying known-good desired config through the normal NATS agent path, restoring a lab snapshot/backup, or using console recovery. NATS, KV, result/status publishing, and applied UUID state remain outside this repo.

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

The initial apply mode is managed-root reconciliation.

At a high level, apply deletes explicit managed roots, applies rendered set commands under those roots, and commits once. It must not delete the full VyOS config and does not save by default.

Detailed managed roots, empty command behavior, executor safety, and acceptance criteria are defined in `SPEC.md`.

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

Saving after commit is available as an explicit option, but it is not the default behavior. `save=false` means runtime commit only. `save=true` persists configuration by running `save` through `/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper`; enable it only after manual validation on the specific target VyOS image. The apply engine must not require `/opt/vyatta/sbin/vyatta-save-config.pl`.

Recommended agent apply setting:

```yaml
agent:
  apply:
    save_after_commit: false
```

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
- call Apply for managed-root replacement and commit
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

## Service Rendering

The renderer emits VyOS service commands for DHCP server, DNS forwarding, and SSH port.

DHCP and DNS forwarding LANs are derived from interfaces where:

```text
role == downstream
ipv4.addressing == static
ipv4.subnet is an IPv4 prefix
```

The service LAN list is carried forward from normalized interface handling so service rendering cannot accept a LAN that interface rendering rejected.

For each LAN, `ipv4.subnet` such as `192.168.50.1/24` is normalized to:

```text
lan_ip        = 192.168.50.1
net_ip_prefix = 192.168.50.0/24
```

DHCP defaults are:

```text
lease-time  = 21600 seconds
lease-first = 10
lease-count = 100
```

Optional DHCP inputs are read from `interfaces[].ipv4.dhcp.lease-time`, `lease-first`, and `lease-count`. `lease-time` accepts seconds as a number, a numeric string, or duration strings with `s`, `m`, `h`, or `d` suffixes. `lease-first` and `lease-count` must be positive integers.

DHCP ranges must stay inside the IPv4 subnet and must not include the LAN/router IP, network address, or broadcast address.

Rendered service commands include:

```text
set service dhcp-server shared-network-name LAN subnet 192.168.50.0/24 lease 21600
set service dhcp-server shared-network-name LAN subnet 192.168.50.0/24 option default-router 192.168.50.1
set service dhcp-server shared-network-name LAN subnet 192.168.50.0/24 option name-server 192.168.50.1
set service dhcp-server shared-network-name LAN subnet 192.168.50.0/24 range 0 start 192.168.50.10
set service dhcp-server shared-network-name LAN subnet 192.168.50.0/24 range 0 stop 192.168.50.109
set service dhcp-server shared-network-name LAN subnet 192.168.50.0/24 subnet-id 4052
set service dns forwarding allow-from 192.168.50.0/24
set service dns forwarding cache-size 0
set service dns forwarding listen-address 192.168.50.1
set service ssh port 22
```

An explicit `services.ssh` object controls SSH port rendering. If `services.ssh` is present without `port`, the renderer emits `set service ssh port 22`. If `services.ssh` is absent, no SSH command is emitted. Valid SSH ports are integers in `1..65535`.

Service render order is after interfaces and before NAT:

```text
interfaces
service
nat
```

HTTPS, API keys, certificates, broad service reset, and `service ssh` reset are intentionally out of scope.

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
      service.go
      nat.go

    templates/
      templates.go
      interface.tmpl
      service.tmpl
      nat.tmpl

      interface/
        bridge.tmpl
        ethernet.tmpl
        vlan.tmpl

      service/
        dhcp-server.tmpl
        dns-forwarding.tmpl
        ssh.tmpl

    vyos/
      executor.go

  testdata/
    valid/
      service-basic.json
    golden/
      service-basic.set

  schemas/
    ucentral/
    vyos/
```

Add files only when real implementation requires them.

---

## Testing

Renderer tests include golden output comparison and deterministic rendering checks.

Apply tests cover Prepare planning, command validation, rejection of empty `DesiredCommands`, managed-root deletion for valid rendered commands, executor ordering, commit failure behavior, and save-disabled default.

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
- firewall, routing, additional service, system, and PKI rendering
- multiple schema versions and stronger schema validation in CI
```

Future apply work may include:

```text
- configurable managed roots
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
- managed-root policy
- unmanaged configuration boundaries
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
  VyOS set commands -> managed-root reconciliation -> commit runtime config

vyos-nats-agent:
  NATS lifecycle, KV loading, renderer/apply orchestration, result publishing
```
