# olg-renderer-vyos SPEC

## 1. Overview

`olg-renderer-vyos` is the VyOS render/apply library used by `olg-server-vyos-client-natagent`.

The repository has two intended public responsibilities:

```text
renderer:
  OLG/uCentral JSON -> deterministic VyOS CLI set commands

apply:
  deterministic VyOS CLI set commands -> managed reset -> commit runtime config
```

The repository must remain independent of NATS. NATS command handling, JetStream KV access, and result/status publishing belong to `nats-agent-core` and `olg-server-vyos-client-natagent`.

End-to-end system flow:

```text
cmd.configure.vyos
  -> vyos-nats-agent loads latest desired config from KV
  -> renderer.Render(...)
  -> apply.Engine.Apply(...)
  -> vyos-nats-agent publishes result/status
```

---

## 2. Design Goals

The renderer must be:

```text
- deterministic
- side-effect free
- schema-version aware
- explicit in translation behavior
- easy to test with golden fixtures
- safe to call from olg-server-vyos-client-natagent
```

The apply engine must be:

```text
- explicit and allowlist-based
- safe against full device config deletion
- independent of NATS
- testable without real VyOS
- commit-only by default
- able to remove stale cloud-owned config without old-vs-new diff logic
- simple enough to implement as one coherent MVP change
```

For the same input payload, renderer version, schema version, target rules, and port mapping, renderer output must be identical.

For the same rendered command text and apply policy, apply planning must be deterministic.

---

## 3. Permanent Boundaries

This repository must not implement:

```text
- NATS connection
- JetStream KV read/write
- configure/action handler registration
- result/status publishing
- cloud command envelopes
- cloud-facing command APIs
- runtime schema fetching
- live device inventory discovery
```

The renderer package must not implement:

```text
- delete planning
- apply
- commit
- save
- discard
- shell execution
- NATS integration
```

The apply package must not implement:

```text
- OLG/uCentral JSON rendering
- schema-driven source translation
- NATS integration
- KV access
- result/status publishing
```

The VyOS NATS client owns orchestration:

```text
LoadDesiredConfig
  -> adapt to renderer.Input
  -> renderer.Render
  -> adapt to apply.Input
  -> apply.Engine.Apply
  -> publish result/status
```

---

## 4. Package Responsibilities

Recommended package split:

```text
renderer/
  public renderer API
  metadata validation
  OLG/uCentral payload normalization
  template execution
  render errors

apply/
  public apply engine API
  rendered command validation
  managed reset planning
  executor interface
  apply errors

internal/normalize/
  renderer normalization internals

internal/templates/
  renderer templates

internal/vyos/
  real VyOS executor implementation
```

The public packages should expose stable APIs. Internal packages can evolve as needed.

---

## 5. Public Renderer Facade

The renderer exposes a public Go facade used by `olg-server-vyos-client-natagent`.

The renderer package must not import `nats-agent-core` and must not accept `agentcore.StoredDesiredConfig` directly.

Expected public API direction:

```go
package renderer

func New(opts ...Option) (*Renderer, error)
func WithPortMap(map[string][]string) Option
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
```

Facade rules:

```text
- PayloadJSON must be the actual OLG/uCentral desired config object.
- PayloadJSON must not be the full NATS KV record.
- PayloadJSON must not be a wrapper object containing the desired config under $.config.
- The renderer must not automatically unwrap $.config.
- RenderedText must be VyOS CLI set-command text only.
- RenderedText must not include configure/commit/save/delete/show commands.
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

## 6. Renderer Input Contract

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
schema_version = 4.2.0
```

Canonical `payload_json` is the raw OLG/uCentral config object.

Example shape:

```json
{
  "interfaces": [],
  "nat": {},
  "services": {},
  "uuid": 1770891457
}
```

The renderer does not expect the NATS KV record shape and does not unwrap `$.config`.

If the desired config is wrapped by another component, the agent adapter must pass only the actual OLG/uCentral config object to the renderer.

Optional payload metadata:

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

## 7. Renderer Output Contract

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

The renderer describes desired configuration only. Delete, apply, commit, save, discard, and device execution are handled by the apply package.

---

## 8. Rendering Scope for MVP

MVP supported renderer sections:

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

## 9. Interface Input Contract

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

No interface field aliases are accepted in the MVP.

The renderer should not document or silently depend on snake_case aliases such as:

```text
select_ports
vlan_tag
```

Required fields by case:

```text
For any renderable interface:
  role
  ethernet[].select-ports[]

For dynamic IPv4:
  ipv4.addressing = dynamic

For static IPv4:
  ipv4.addressing = static
  ipv4.subnet

For VLAN/VIF rendering:
  role = downstream
  vlan.id
  ipv4.subnet
```

Interface normalization rules:

```text
- upstream non-VLAN interface maps to bridge br0
- first downstream non-VLAN interface maps to bridge br1
- additional downstream non-VLAN interfaces map to br2, br3, ...
- top-level VLAN interfaces are not rendered as separate bridges
- downstream VLAN interfaces are rendered as VIFs on the downstream bridge
- allowed-vlan lines are derived from VIF IDs and VIF member-interface membership
- static subnet values from the cloud payload must be preserved exactly
- physical interface mapping must be deterministic
- ethernet descriptions prefer base non-VLAN interface names over VLAN/VIF names
```

Default physical mapping for MVP fixtures:

```text
WAN* -> eth0
LAN* -> eth1, eth2
LAN1 -> eth1
LAN2 -> eth2
```

Production mapping should be resolved by the agent or device-profile layer and passed with `renderer.WithPortMap`.

The renderer must not discover live device interfaces, read mapping files, or add runtime filesystem dependencies.

---

## 10. Canonical Interface Example

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
set interfaces bridge br1 member interface eth1
set interfaces bridge br1 member interface eth2 allowed-vlan 10
set interfaces bridge br1 member interface eth2 native-vlan 1
set interfaces bridge br1 vif 10 address 192.168.10.1/24
set interfaces bridge br1 vif 10 description 'LAN.10'
set interfaces ethernet eth0 description 'WAN'
set interfaces ethernet eth1 description 'LAN'
set interfaces ethernet eth2 description 'LAN'
```

Ordering requirements:

```text
- bridge commands before ethernet commands
- br0 before br1
- VIFs sorted by VLAN ID
- bridge member interfaces sorted by interface name
- allowed-vlan lines sorted by member interface, then VLAN ID
- ethernet interfaces sorted by interface name
```

---

## 11. NAT Input Contract

The renderer reads explicit source NAT data from:

```text
payload_json.nat.snat.rules
```

`rules` must be an array when present.

NAT is optional. If absent, renderer must not generate NAT commands.

Each rule must include:

```text
rule-id
out-interface.name
source.address
translation.address
```

Alias policy:

```text
The renderer uses schema-defined field names only. MVP does not define or document renderer-level aliases.
```

NAT normalization rules:

```text
- rules are explicit only
- no auto-NAT generation
- output rules sorted by numeric rule ID
- cloud-provided source and translation values are preserved exactly
```

`translation.address` is a string from the OLG/uCentral schema. It may be a concrete translated source address or a schema-supported keyword such as `masquerade`.

---

## 12. Canonical NAT Example

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

## 13. Rendering Pipeline

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

## 14. Normalization and Template Layers

Normalization converts raw JSON into template-friendly render data.

Normalization owns:

```text
- payload parsing
- optional metadata checks
- schema-defined field handling for NAT
- interface role filtering
- bridge naming
- VLAN/VIF grouping
- renderer-configured physical port mapping
- NAT rule normalization
- deterministic sorting
```

Templates format normalized data into set-command text.

Templates must not contain business mapping decisions and must not depend on Go map iteration order.

Port mapping ownership:

```text
- renderer.New() installs the default MVP fixture mapping
- renderer.WithPortMap(map[string][]string) extends or overrides that mapping
- WithPortMap defensively copies caller map and slice input
- WithPortMap deduplicates and sorts physical interfaces per selector
- future agent/device-profile code may load mapping from files or inventory
- loaded mapping must be passed into the renderer; the renderer must not load it itself
```

---

## 15. Schema Usage

Schemas are contracts and guardrails.

OLG/uCentral schema is used for:

```text
- defining supported input schema version
- validating fixtures in CI/build
- avoiding drift with olg-ucentral-client
```

VyOS config schema is used for:

```text
- validating generated command paths in tests or CI
- identifying supported target command paths for a specific VyOS build
- future compatibility checks
```

The renderer should not fetch schemas at runtime.

Manual renderer logic still defines semantic mappings such as:

```text
upstream -> br0
downstream -> br1
VLAN downstream -> bridge VIF
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

## 17. Renderer Error Model

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

The agent may map renderer errors to a wire error code such as `render_failed`, while preserving the typed renderer error internally for logs.

---

## 18. Public Apply Engine Facade

The apply engine exposes a public Go facade used by `olg-server-vyos-client-natagent`.

The apply package must not import `nats-agent-core` and must not publish result/status messages.

Expected public API direction:

```go
package apply

func New(opts ...Option) (*Engine, error)
func (e *Engine) Plan(ctx context.Context, input Input) (Plan, error)
func (e *Engine) Apply(ctx context.Context, input Input) (Result, error)
func GetInfo() Info
```

Expected public input shape:

```go
type Input struct {
    Target          string
    ConfigUUID      string
    DesiredCommands string
}
```

Expected public result shape:

```go
type Result struct {
    Target         string
    ConfigUUID     string
    Applied        bool
    Changed        bool
    Saved          bool
    DeleteCommands []string
    SetCommands    []string
    CommitOutput   string
    SaveOutput     string
    DiscardOutput  string
}
```

Expected options:

```go
func WithExecutor(exec Executor) Option
func WithSaveAfterCommit(enabled bool) Option
func WithManagedPolicy(policy ManagedPolicy) Option
```

API rules:

```text
- Plan validates and returns deterministic delete/set operations only.
- Plan must not execute commands, commit, save, discard, or update state.
- Apply validates, plans, executes delete/set commands, commits, optionally saves if enabled, and returns Result.
- Apply always executes the validated/planned commands it is given; it must not short-circuit on UUID comparison.
- Apply does not compare desired UUID with applied UUID.
- Apply does not persist applied UUID state.
- ConfigUUID is metadata for traceability/result context only.
- Avoid duplicate public apply APIs with overlapping meaning.
```

---

## 19. Apply Engine MVP Strategy

The MVP apply strategy is managed reset.

```text
managed reset:
  delete known cloud-owned VyOS sections
  apply all rendered set commands
  commit once
```

The MVP must not implement old-vs-new diff logic.

The MVP must not delete full VyOS config.

Rationale:

```text
- cloud sends full desired config
- set-only apply leaves stale deleted cloud config behind
- full device delete is unsafe
- managed reset removes stale cloud-owned sections while preserving unknown/protected config
```

Execution must use one candidate configuration transaction:

```text
configure
  delete managed sections
  set rendered desired commands
  commit
```

Do not commit after delete and before set.

---

## 20. Apply Managed Policy

Deletion must be allowlist-based.

Default MVP managed roots:

```text
interfaces bridge <cloud bridge>
nat source rule <cloud-managed rule>
```

Default cloud bridge set:

```text
br0
br1
br2
br3
br4
br5
br6
br7
br8
br9
```

Default NAT source rule handling may start with either:

```text
- configured managed rule IDs, or
- managed rule ID range reserved for cloud-owned source NAT
```

The chosen policy must be explicit in code and tests.

Never delete by default:

```text
system
service ssh
protocols
interfaces ethernet
container
users
agent bootstrap config
local recovery config
unknown future config
```

Future renderer sections must extend managed roots deliberately. For example, if DHCP rendering is added later, DHCP delete roots must be added explicitly at that time.

---

## 21. Apply Command Validation

Apply input must be renderer-generated set-command text.

Allowed command form:

```text
set ...
```

Reject these command roots or operations:

```text
configure
commit
save
discard
exit
delete
show
run
sudo
```

Reject shell execution hazards in rendered commands:

```text
;
|
&
`
$
>
<
```

The apply engine should not expose a generic arbitrary command execution API.

Validation errors must happen before executor invocation.

---

## 22. VyOS NATS Client Temporary Applied State

The temporary applied-config state is owned by `olg-server-vyos-client-natagent`, not by the renderer or apply package.

The controller guarantees that every desired config write to KV receives a new UUID. Therefore, the VyOS NATS client can use UUID comparison as the current-boot duplicate-work guard.

Recommended path:

```text
/run/olg-vyos-client/applied-state.json
```

State fields:

```json
{
  "target": "vyos",
  "applied_uuid": "cfg-123",
  "applied_at": "2026-05-22T00:00:00Z"
}
```

Rules:

```text
- if state is missing, treat config as not applied
- if applied_uuid equals desired.Record.UUID, the agent may skip renderer and apply
- the state is updated only after render and apply both succeed
- the state is not updated after render, validation, execution, commit, or save failure
- state disappearance after reboot is expected
- renderer and apply packages may receive ConfigUUID as metadata but must not own this comparison
```

---

## 23. Commit and Save Policy

Default behavior:

```text
commit = enabled
save = disabled
```

The MVP must not save by default.

Reason:

```text
- KV remains the source of truth
- saved VyOS config remains bootstrap/recovery config
- after reboot, agent rehydrates runtime config from KV
```

Critical deployment requirement:

```text
Bootstrap saved config must provide enough connectivity for the agent to reach NATS/KV after reboot.
```

Optional future behavior:

```text
WithSaveAfterCommit(true)
  commit and then save after successful commit
```

If save is enabled and save fails, behavior must be explicit and tested. The default MVP can avoid save-failure complexity by keeping save disabled.

---

## 24. Apply Executor Boundary

The apply package must execute through an interface.

Expected executor direction:

```go
type Executor interface {
    Execute(ctx context.Context, plan Plan) (ExecutionResult, error)
}
```

Executor responsibilities:

```text
- open/enter VyOS configure context
- apply delete commands
- apply set commands
- commit
- optionally save if configured
- discard candidate config on failure where possible
```

Testing executor:

```text
fake executor records plan and simulates success/failure
```

Real executor:

```text
internal/vyos executor performs local non-interactive VyOS operations
```

Unit tests must not require real VyOS.

---

## 25. Apply Flow

Apply must follow this flow:

```text
1. Validate input metadata.
2. Parse DesiredCommands into command lines.
3. Reject unsafe or non-set commands.
4. Build managed reset plan.
5. Execute delete commands and set commands in one candidate session.
6. Commit.
7. Save only if explicitly enabled.
8. Return Result.
```

Failure flow:

```text
- discard candidate config where possible
- do not update any applied UUID state because that state is owned by the VyOS NATS client
- return typed apply error
```

---

## 26. Apply Error Model

The apply engine must return typed errors with stable categories.

Required categories:

```text
invalid_input
invalid_command
plan_failed
executor_failed
delete_failed
set_failed
commit_failed
save_failed
discard_failed
apply_failed
```

The agent may map apply errors to a wire error code such as `apply_failed`, while preserving the typed apply error internally for logs.

---

## 27. VyOS NATS Client Integration Contract

`olg-server-vyos-client-natagent` must orchestrate render and apply.

Configure handler flow:

```text
1. Receive configure notification.
2. Serialize configure processing with a mutex or equivalent.
3. Publish received/loading status.
4. Load latest desired config from KV.
5. Validate target and UUID against notification.
6. Load temporary applied UUID state.
7. If applied_uuid == desired.Record.UUID:
     publish already_in_sync status and success result
     skip renderer and apply
8. Build renderer.Input.
9. Call renderer.Render.
10. Build apply.Input from renderer.Output.RenderedText.
11. Call apply.Engine.Apply.
12. Update temporary applied UUID state only after render and apply both succeed.
13. Publish success/failure result.
```

The apply package must not publish NATS messages.

Applied UUID state ownership remains outside the renderer/apply packages.

---

## 28. Configure Use Cases

### UC-01: First-time configure apply

Goal:

```text
Apply a new desired VyOS config from KV.
```

Flow:

```text
1. Controller submits configure for target vyos.
2. nats-agent-core stores desired config in KV and publishes cmd.configure.vyos.
3. vyos-nats-agent receives the notification.
4. Agent loads latest desired config from KV.
5. Agent builds renderer.Input.
6. Renderer returns set commands.
7. Apply engine performs managed reset and applies set commands.
8. Apply engine commits and does not save by default.
9. Agent updates temporary applied UUID state after render and apply both succeed.
10. Agent publishes success result.
```

### UC-02: Same config received again during same boot

Goal:

```text
Avoid unnecessary render/apply when the same UUID is already applied during the current boot.
```

Flow:

```text
1. Agent receives configure notification for same UUID.
2. Agent loads desired config from KV.
3. Agent loads temporary applied UUID state.
4. applied_uuid == desired.Record.UUID.
5. Agent skips renderer and apply.
6. Agent publishes already_in_sync status and success result.
```

### UC-03: NAT rule removed from desired config

Goal:

```text
Remove stale cloud-owned NAT rules without old-vs-new diff logic.
```

Old desired:

```text
set nat source rule 100 ...
set nat source rule 110 ...
```

New desired:

```text
set nat source rule 100 ...
```

Managed reset behavior:

```text
delete nat source rule 100
delete nat source rule 110
set nat source rule 100 ...
commit
```

Result:

```text
rule 110 is removed and rule 100 is recreated from current desired config.
```

### UC-04: VLAN removed from desired config

Goal:

```text
Remove stale cloud-owned bridge VIF config.
```

Old desired contains:

```text
set interfaces bridge br1 vif 10 ...
```

New desired omits that VLAN.

Managed reset behavior:

```text
delete interfaces bridge br1
set interfaces bridge br1 ...
commit
```

Result:

```text
stale br1 vif 10 config is removed.
```

### UC-05: Reboot with KV as source of truth

Goal:

```text
After reboot, restore desired runtime config from KV without saving cloud config into VyOS saved config.
```

Flow:

```text
1. VyOS boots with saved bootstrap/recovery config.
2. Temporary applied UUID state is absent.
3. Agent connects to NATS using bootstrap connectivity.
4. Agent loads latest desired config from KV.
5. Renderer renders latest desired payload.
6. Apply engine applies and commits runtime config.
7. Agent writes temporary applied UUID state after render/apply success.
```

### UC-06: Render failure

Goal:

```text
Do not apply if desired config cannot be rendered.
```

Flow:

```text
1. Agent loads desired config.
2. Renderer returns a typed render error.
3. Agent does not call apply.
4. Temporary applied UUID state is not updated.
5. Agent publishes failure result with render_failed category.
```

### UC-07: Apply or commit failure

Goal:

```text
Do not mark config applied if VyOS apply fails.
```

Flow:

```text
1. Renderer succeeds.
2. Apply engine starts managed reset apply.
3. Delete, set, or commit fails.
4. Apply engine discards candidate config where possible.
5. Temporary applied UUID state is not updated.
6. Agent publishes failure result with apply_failed category.
```

### UC-08: Missed configure notification

Goal:

```text
Recover through KV source of truth even if transient notification is missed.
```

Flow:

```text
1. Desired config is stored in KV.
2. Configure notification is missed or agent is offline.
3. Agent starts or reconnects.
4. Startup reconcile loads latest desired config from KV.
5. Agent renders and applies if runtime state does not match.
```

---

## 29. Deterministic Output Requirements

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

Apply planning must avoid:

```text
- random map iteration
- live-device-dependent delete plan generation
- hidden default deletes outside managed policy
```

---

## 30. Testing Requirements

### Renderer unit tests

Required:

```text
- input metadata validation
- schema compatibility checks
- optional payload metadata mismatch
- interface normalization
- NAT canonical field handling
- renderer error categories
```

### Renderer golden tests

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

### Apply unit tests

Required:

```text
- Plan rejects unsafe commands
- Plan creates managed reset delete commands
- Apply first config executes managed reset and commit
- NAT removal is handled by managed reset
- VLAN removal is handled by managed reset
- unknown/protected paths are not deleted
- commit failure returns typed error and attempts discard
- save disabled by default
```

NATS client integration acceptance (external to this repo):

```text
- VyOS NATS client skips render/apply when applied_uuid equals desired.Record.UUID
- VyOS NATS client updates temporary applied UUID only after render and apply both succeed
```

### Integration tests

Real VyOS tests can come later. MVP unit tests must use a fake executor.

---

## 31. Repository Layout

Desired layout after adding apply:

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

## 32. Single-Phase Implementation Target

The apply engine MVP can be implemented in one coherent phase because it has a small scope.

Single-phase target:

```text
- public apply package
- typed apply errors
- Plan and Apply APIs
- rendered set-command parser/validator
- managed reset policy
- fake executor tests
- real executor boundary
- no apply-owned temporary applied UUID state
- no NATS integration inside this repo
- no old-vs-new diff mode
- no live config parsing
- no save by default
```

Review should still keep the internal files separated by responsibility.

---

## 33. Acceptance Criteria

Renderer acceptance:

```text
- go build ./... succeeds
- go test ./... succeeds
- canonical interface example renders expected set commands
- canonical NAT example renders expected set commands
- NAT absent does not generate NAT
- unsupported target/schema/version returns typed errors
- metadata mismatch returns typed error
- output is deterministic
```

Apply acceptance:

```text
- public apply package exists
- Plan validates commands and returns deterministic managed reset plan
- Apply uses executor interface and fake executor tests pass
- full config deletion is impossible by default
- unknown/protected config is preserved by default
- commit-only is default
- save is disabled by default
- no NATS/KV/result/status logic is added to this repo
```

NATS integration acceptance belongs to `olg-server-vyos-client-natagent`:

```text
- configure handler loads desired from KV
- configure handler calls renderer then apply
- render failure publishes failure result and skips apply
- apply failure publishes failure result and does not mark config applied
- missed notification is recovered by startup reconcile
- temporary applied UUID state is updated only after render and apply both succeed
- failed render/apply does not update temporary applied UUID state
- same UUID skips render/apply before renderer is called
```

---

## 34. Future Roadmap

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

Future apply work:

```text
- configurable managed path policy loaded by caller
- optional hooks for externally owned applied-state workflows
- optional save-after-commit mode
- commit-confirm support
- old-vs-new diff apply mode
- live config drift detection
- expanded managed roots for future rendered sections
```

---

## 35. Summary

`olg-renderer-vyos` should evolve from a pure renderer into a small VyOS render/apply library.

It receives:

```text
metadata + OLG/uCentral JSON
```

It renders:

```text
VyOS CLI set-command text
```

It applies:

```text
managed reset of cloud-owned sections + rendered set commands + commit
```

It does not:

```text
connect to NATS
read/write KV
publish results
expose cloud commands
inspect live device inventory
save by default
```
