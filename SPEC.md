# olg-renderer-vyos SPEC

## 1. Overview

`olg-renderer-vyos` is the VyOS render/apply library used by `olg-server-vyos-client-natagent`.

This document is implementation-oriented and authoritative for design/contracts. `README.md` is the high-level state/usage overview.

The repository has two intended public responsibilities:

```text
renderer:
  OLG/uCentral JSON -> deterministic VyOS CLI set commands

apply:
  deterministic VyOS CLI set commands -> managed-root reconciliation -> default VyOS executor -> commit runtime config
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
- cloud-authoritative for managed roots
- managed-root based, not full-config delete
- default/bootstrap/unmanaged roots preserved
- safe one-transaction apply
- safe against full device config deletion
- independent of NATS
- testable without real VyOS
- commit-only by default
- directly executable through its default persistent VyOS session executor
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
  managed-root reconciliation planning
  executor interface
  apply errors

internal/normalize/
  renderer normalization internals

internal/templates/
  renderer templates

internal/vyos/
  controlled VyOS session runner used by the default apply executor
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

Renderer empty-output rule:

```text
- For the configure path, successful render output must be non-empty.
- The renderer must reject empty or non-operational desired config rather than returning empty RenderedText.
- If supported sections are present but invalid, malformed, incomplete, or cannot be rendered, renderer must return a typed render error rather than empty RenderedText.
- If desired config contains no renderable production config for the current renderer scope, renderer must return a typed error such as missing_config or invalid_input.
- For the VyOS configure MVP, successful render should require at least one renderable upstream interface.
```

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

## Service Rendering

Supported MVP service output:

```text
service dhcp-server
service dns forwarding
service ssh port
```

Service DHCP and DNS forwarding LANs are derived from each interface entry satisfying:

```text
role == downstream
ipv4.addressing == static
ipv4.subnet is a non-empty IPv4 prefix
```

Service LAN derivation follows normalized interface handling. A LAN is available to service rendering only if the same interface entry was accepted by interface normalization.

IPv6 service subnets are not supported for MVP and must fail normalization when selected as service LANs.

For `ipv4.subnet` such as `192.168.50.1/24`, normalization computes:

```text
lan_ip        = 192.168.50.1
net_ip_prefix = 192.168.50.0/24
```

DHCP defaults:

```text
lease_secs  = 21600
lease_first = 10
lease_count = 100
```

Optional DHCP input fields:

```text
interfaces[].ipv4.dhcp.lease-time
interfaces[].ipv4.dhcp.lease-first
interfaces[].ipv4.dhcp.lease-count
```

`lease-time` accepts numeric seconds, numeric string seconds, or duration strings with `s`, `m`, `h`, or `d` suffix. `lease-first` and `lease-count` must parse to positive integers; zero and negative values are rejected.

DHCP range:

```text
range_start = net_ip + lease_first
range_stop  = range_start + lease_count - 1
```

The range must remain inside the IPv4 subnet and must not include the LAN/router IP, network address, or broadcast address.

Subnet ID:

```text
if interface has vlan.id > 0:
  subnet_id = vlan.id
else:
  subnet_id = 4051 + original zero-based interfaces[] index
```

For one LAN, rendered DHCP/DNS/SSH output is:

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

If `subnet_id == 1`, DHCP output also includes:

```text
set service dhcp-server shared-network-name <name> subnet <net_ip_prefix> option domain-name vyos.net
```

For multiple LANs, DNS forwarding renders all `allow-from` lines in deterministic LAN order, one `cache-size 0` line, then all `listen-address` lines in deterministic LAN order.

SSH rendering is explicit-only:

```text
absent services.ssh       -> no SSH output
services.ssh: {}          -> set service ssh port 22
services.ssh.port: 2222   -> set service ssh port 2222
```

Valid SSH ports are integers in `1..65535`.

HTTPS, API keys, certificates, allow-client, broad service reset, and `service ssh` reset are out of scope.

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
service
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
- service LAN derivation from downstream static IPv4 interfaces
- DHCP lease/range normalization
- SSH port normalization
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
func (e *Engine) Prepare(ctx context.Context, input Input) (Plan, error)
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

Rule:

```text
ConfigUUID is metadata for traceability/result context only. The apply package must not use ConfigUUID for duplicate detection.
DesiredCommands is required and must be non-empty for the configure apply path.
```

Expected public result shape:

```go
type Result struct {
    Target         string
    ConfigUUID     string
    Applied        bool
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
func WithResetPolicy(policy ResetPolicy) Option
```

API rules:

```text
- Prepare is optional preview and is the only non-executing apply-engine API.
- Prepare returns what Apply would execute for the same input and policy.
- Production callers may call Apply directly; they do not need to call Prepare first.
- Apply validates, prepares a fresh plan, executes through the default executor unless overridden, and commits.
- There is no DryRun field in Input.
- Dry-run style inspection must use Prepare.
- Avoid duplicate public apply APIs with overlapping meaning.
```

### Prepare semantics

Prepare is the non-executing pre-apply API.

Prepare must:

```text
- validate input metadata
- parse DesiredCommands into command lines
- reject unsafe or non-set commands
- build delete commands for all managed roots from ResetPolicy
- append rendered desired set commands
- mark commit required when there are changes
- mark save based on the requested save flag
- return a deterministic Plan
```

Prepare must reject empty DesiredCommands.

Example plan:

```text
DeleteCommands:
  delete interfaces bridge
  delete nat source
  delete service dhcp-server
  delete service dns forwarding

SetCommands:
  <renderer output>

Commit:
  true

Save:
  false
```

Prepare must not:

```text
- call the executor
- enter/setup a VyOS CLI session
- delete config
- set config
- commit
- save
- discard
- publish status/result
- update any local or external state
```

### Apply semantics

Apply is the production executing API.

Apply must:

```text
- perform the same validation and plan preparation as Prepare
- acquire the local agent apply lock
- open one persistent VyOS CLI Shell API configuration session
- execute delete commands for managed roots
- execute desired set commands
- commit the candidate configuration
- run save through vyatta-cfg-cmd-wrapper only when save=true
- discard candidate config on failure where possible
- teardown the session and release the apply lock
- return structured Result
```

Apply must reject empty DesiredCommands.

Apply must not:

```text
- publish NATS status/result
- read or write NATS KV
- perform UUID-based duplicate detection
- update VyOS NATS client applied UUID state
```

### Plan shape

Plan is data, not an executing operation.

Recommended shape:

```go
type Plan struct {
    Target         string
    ConfigUUID     string
    DeleteCommands []string
    SetCommands    []string
    Commit         bool
    Save           bool
}
```

### Result shape

Result represents actual Apply execution output.

Recommended shape:

```go
type Result struct {
    Target         string
    ConfigUUID     string
    Applied        bool
    Saved          bool
    DeleteCommands []string
    SetCommands    []string
    DeleteOutput   string
    SetOutput      string
    CommitOutput   string
    SaveOutput     string
    DiscardOutput  string
}
```

---

## 19. Apply Engine MVP Strategy

The MVP apply strategy is managed-root reconciliation.

Managed-root reconciliation is also managed-subtree replacement: the renderer/apply engine owns only declared managed roots, deletes those roots during apply, and recreates desired configuration under them. Configuration outside managed roots is preserved by default.

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

Cloud desired config is the production source of truth for the managed roots it controls.

The MVP must not implement old-vs-new diff logic.

The MVP must not delete the full VyOS config.

The MVP must not rely on NAT rule ID range ownership because `nat source` is reset as a cloud-controlled root.

Empty configure payload must not be used as reset-to-default. Reset-to-default or clear-cloud-config behavior must be a separate explicit action with its own authorization and safety policy.

Rationale:

```text
- cloud sends full desired config
- set-only apply leaves stale deleted cloud config behind
- full device delete is unsafe
- managed-root deletion removes stale cloud-owned sections while preserving unmanaged roots
```

Execution must use one persistent CLI Shell API candidate configuration session:

```text
acquire /run/lock/olg-vyos-apply.lock
cli-shell-api getSessionEnv <apply-process-id>
cli-shell-api setupSession
  my_delete managed roots
  my_set rendered desired commands
  my_commit
  optional vyatta-cfg-cmd-wrapper save
  my_discard on failure before commit
cli-shell-api teardownSession
release /run/lock/olg-vyos-apply.lock
```

Do not commit after delete and before set.

`apply.New()` constructs an engine with the default internal VyOS session executor. `WithExecutor(...)` remains available for tests and advanced controlled integrations, but `olg-server-vyos-client-natagent` should normally call `apply.New()` and then `Apply()` directly.

The default session identifier is the apply process ID. The default executor must not run independent stateless wrapper `begin`, `delete`, `set`, `commit`, and `end` calls after opening a session. It must reuse the session environment returned by `getSessionEnv` for every operation. This transaction/session behavior must be validated on the deployed VyOS version or image before production rollout.

---

## 20. Managed Roots And Unmanaged Config

Managed roots are VyOS config roots controlled by cloud desired config. Implementation code calls them reset roots because `Prepare` emits delete commands for them before applying desired set commands.

Unmanaged roots are not blindly deleted. They contain default, bootstrap, recovery, agent, management, or operator configuration.

Reset policy data shape:

```go
type ResetPolicy struct {
    ResetRoots []string
}
```

MVP default policy, implemented by `apply.DefaultResetPolicy()` in `apply/policy.go`:

```go
DefaultResetPolicy := ResetPolicy{
    ResetRoots: []string{
        "interfaces bridge",
        "nat source",
        "service dhcp-server",
        "service dns forwarding",
    },
}
```

Currently managed roots:

```text
- interfaces bridge
- nat source
- service dhcp-server
- service dns forwarding
```

Unmanaged examples that must be preserved by default:

```text
- system
- system login
- system config-management
- service
- service ssh
- service ntp
- interfaces loopback
- interfaces ethernet
- interfaces ethernet / WAN access
- protocols
- container
- users
- agent/bootstrap/recovery config
- any root not explicitly listed as reset-owned
- other non-owned configuration
```

Rules:

```text
- Delete only managed roots.
- ResetRoots are explicit implementation names for managed roots.
- ResetPolicy roots must be validated against an apply package managed-root allowlist.
- Never delete the full config.
- Anything not listed in ResetRoots is preserved from broad deletion by default.
- Manual/debug config under a managed root may be removed by cloud apply.
- Unmanaged roots may still receive specific allowlisted rendered set commands.
- Future renderer sections must add matching managed roots explicitly.
- WithResetPolicy replaces the default reset policy for that engine instance.
- Every root supplied to WithResetPolicy must be validated.
- An empty reset root must be rejected.
- A root representing full config or `/` must be rejected.
- Unsafe unmanaged roots must be rejected even if passed through WithResetPolicy.
- For MVP, roots outside the allowed managed-root list must be rejected.
```

For MVP, `nat source` is a managed root.

Apply may delete `nat source` before applying rendered source NAT rules.

Therefore, a reserved cloud-managed NAT rule ID range is not required for MVP.

For MVP, `service dhcp-server` and `service dns forwarding` are managed roots.

Apply may delete these service roots before applying rendered DHCP and DNS forwarding rules. Broad `service` and `service ssh` are not managed roots by default.

For MVP, allowed managed roots are exactly:

```text
interfaces bridge
nat source
service dhcp-server
service dns forwarding
```

For MVP, these managed roots must be rejected:

```text
system
service
service ssh
users
protocols
container
interfaces ethernet
interfaces
nat
service dns
<empty>
/
```

Manual/debug NAT source rules are not guaranteed to survive cloud apply.

Future versions may add `ProtectedRoots` if explicit preservation documentation or validation becomes useful, but MVP preservation is defined by omission from `ResetRoots`.

Invalid ResetPolicy should cause `apply.New(...)` to return an error when `WithResetPolicy` is applied.

Safety boundary:

```text
- whole-device reconciliation is not the current design.
- delete-everything-except-a-preserve-whitelist is not the current design.
- whole-device reconciliation risks deleting SSH, login, WAN, NTP, or system config if the preserve list is incomplete.
- managed-root reconciliation is safer for this phase because the renderer only owns OLG/VyOS-rendered roots.
- full-device reconciliation, if ever added, must be a separate explicit mode with stronger safeguards.
```

---

## 21. Apply Command Validation

Apply input must be renderer-generated set-command text.

Validation must be line-based and quote-aware.

Validation must:

```text
- normalize CRLF to LF before parsing
- split DesiredCommands into lines
- trim leading/trailing whitespace per line
- ignore empty lines
- reject comment lines because renderer output must not contain comments
- parse non-empty lines using quote-aware tokenization
- reject unclosed quotes
- require the first token to be `set`
- reject non-set operations
- reject forbidden shell/control metacharacters
- reject validation failures before executor invocation
```

Renderer output must not contain comments. Comment lines in DesiredCommands must be rejected, not ignored.

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

Reject shell/control hazards in rendered commands:

```text
;
|
&
`
$
>
<
```

MVP command path allowlist:

```text
set interfaces bridge ...
set interfaces ethernet <name> description ...
set nat source ...
set service dhcp-server shared-network-name <name> subnet <cidr> lease <seconds>
set service dhcp-server shared-network-name <name> subnet <cidr> option default-router <ipv4>
set service dhcp-server shared-network-name <name> subnet <cidr> option name-server <ipv4>
set service dhcp-server shared-network-name <name> subnet <cidr> option domain-name vyos.net
set service dhcp-server shared-network-name <name> subnet <cidr> range 0 start <ipv4>
set service dhcp-server shared-network-name <name> subnet <cidr> range 0 stop <ipv4>
set service dhcp-server shared-network-name <name> subnet <cidr> subnet-id <positive-int>
set service dns forwarding allow-from <cidr>
set service dns forwarding cache-size 0
set service dns forwarding listen-address <ipv4>
set service ssh port <port>
```

Rules:

```text
- `set interfaces bridge ...` is allowed because `interfaces bridge` is a managed root.
- `set nat source ...` is allowed because `nat source` is a managed root.
- `set interfaces ethernet <name> description ...` is allowed because renderer may set ethernet descriptions while `interfaces ethernet` is unmanaged and preserved from broad deletion.
- Exact renderer-emitted `set service dhcp-server ...` forms are allowed because `service dhcp-server` is a managed root.
- Exact renderer-emitted `set service dns forwarding ...` forms are allowed because `service dns forwarding` is a managed root.
- `set service ssh port <port>` is allowed as a narrow management setting, but `service ssh` is not reset by default.
- Other `set ...` roots must be rejected until explicitly supported by renderer/apply policy.
```

The apply engine must not reject a valid renderer-emitted set command only because its root is unmanaged, but unmanaged-root set paths must still be explicitly allowlisted.

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

Optional explicit behavior:

```text
WithSaveAfterCommit(true)
  commit and then save after successful commit
```

If save is enabled and save fails, behavior must be explicit and tested. The default MVP can avoid save-failure complexity by keeping save disabled.

Save behavior:

```text
- save=false means runtime commit only.
- save=false must not require or call any save helper.
- save=true persists configuration after commit only after manual validation on the target VyOS image.
- Modern VyOS rolling images may not have /opt/vyatta/sbin/vyatta-save-config.pl.
- The renderer/apply engine must not depend on /opt/vyatta/sbin/vyatta-save-config.pl.
- Persistence is performed by passing `save` through /opt/vyatta/sbin/vyatta-cfg-cmd-wrapper.
```

Recommended agent apply setting:

```yaml
agent:
  apply:
    save_after_commit: false
```

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
- acquire local apply lock
- initialize one VyOS CLI Shell API config session
- apply delete commands
- apply set commands
- commit with my_commit
- optionally save with wrapper save if configured
- discard candidate config on failure where possible
- always run cli-shell-api teardownSession after setup
- release local apply lock
```

Executor safety requirements:

```text
- The real VyOS executor must not execute rendered command text through unsafe shell string interpolation.
- Validated delete/set commands should be treated as VyOS configuration commands and passed to the VyOS configuration mechanism in a controlled way.
- Avoid patterns such as `sh -c "<rendered command string>"`.
- Do not concatenate untrusted rendered command text into an arbitrary shell script.
- The executor should receive structured command lists from Plan:
  - DeleteCommands
  - SetCommands
- The executor must not receive one concatenated arbitrary shell command string.
- The executor must not expose a generic arbitrary command execution API.
- Validation must complete before executor invocation.
```

If a shell wrapper is unavoidable for the target VyOS environment, it must only receive commands that passed apply validation, must preserve command boundaries, must not allow arbitrary command injection, and must be covered by tests for command rejection and command ordering.

Default executor:

```text
apply.New()
  -> default internal VyOS session executor
  -> internal/vyos runner
  -> cli-shell-api getSessionEnv/setupSession
  -> my_delete/my_set/my_commit
  -> optional vyatta-cfg-cmd-wrapper save
  -> my_discard on failure
  -> cli-shell-api teardownSession
```

The default runner invokes `/usr/bin/cli-shell-api` for session setup/teardown, `/opt/vyatta/sbin/my_delete`, `/opt/vyatta/sbin/my_set`, `/opt/vyatta/sbin/my_commit`, `/opt/vyatta/sbin/my_discard`, and `/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper save` with argv boundaries. It must reuse the session environment returned by `getSessionEnv`. It must not rely on PATH lookup, use `sh -c`, concatenate rendered commands into a shell string, or expose generic raw public APIs such as `Run`, `Shell`, `Set`, `Commit`, `Save`, or `Show`.

`internal/vyos` is not a generic command runner. It must only receive commands from a validated `apply.Plan`, and it includes last-resort guards that reject empty commands, newlines, obvious shell/control metacharacters, and operations other than `set` or `delete`.

`WithExecutor(...)` is for tests and advanced controlled integrations. Custom executors receive validated `Plan` data, but can bypass runtime execution safety if implemented incorrectly; they must preserve command boundaries and must not expose arbitrary command execution.

Failure behavior:

```text
- begin failure returns executor_failed
- delete failure attempts discard and returns delete_failed
- set failure attempts discard and returns set_failed
- commit failure attempts discard and returns commit_failed
- save failure after successful commit returns save_failed with Applied=true and Saved=false
- discard failure must not hide the primary failure code
- end failure after a primary failure preserves the primary failure code and appends end detail
- end failure after successful commit returns executor_failed while preserving Applied=true
- context cancellation stops execution and returns a typed executor_failed/apply_failed error
```

Testing executor:

```text
fake executor records plan and simulates success/failure
fake VyOS runner records default executor operations
```

Real executor:

```text
internal/vyos runner performs local non-interactive VyOS session operations
```

Unit tests must not require real VyOS.

---

## 25. Apply Flow

Prepare performs steps 1-5 only and returns Plan.

Apply performs steps 1-11:

```text
1. Validate input metadata.
2. Reject empty DesiredCommands.
3. Parse DesiredCommands into command lines; parsed command list must be non-empty.
4. Reject unsafe or non-set commands.
5. Build delete commands for all managed roots.
6. Enter one VyOS candidate configuration session.
7. Execute managed-root delete commands.
8. Execute rendered desired set commands.
9. Commit once.
10. Run save through `vyatta-cfg-cmd-wrapper` only if explicitly enabled.
11. Run end/discard cleanup as needed and return Result.
```

Delete + set + commit must be one transaction.

Do not commit after delete and before set.

Failure flow:

```text
- discard candidate config where possible
- discard is attempted for delete, set, and commit failures
- primary errors such as delete_failed, set_failed, and commit_failed are preserved even if discard also fails
- save failure after commit does not discard and keeps Applied=true
- do not update any applied UUID state because that state is owned by the VyOS NATS client
- return typed apply error
```

---

## 26. Apply Error Model

The apply engine must return typed errors with stable categories.

Required categories:

```text
invalid_input
empty_desired_commands
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

`empty_desired_commands` means apply received no rendered set commands for a configure apply operation.

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
10. Build apply.Input from non-empty renderer.Output.RenderedText.
11. Call apply.Engine.Apply.
12. Update temporary applied UUID state only after render and apply both succeed.
13. Publish success/failure result.
```

The apply package must not publish NATS messages.

Applied UUID state ownership remains outside the renderer/apply packages.

The VyOS NATS client must call apply only after renderer succeeds and returns non-empty RenderedText.
If renderer returns empty output or a missing_config/invalid_input error, the agent must not call apply and must not update temporary applied UUID state.

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
7. Apply engine performs managed-root reconciliation and applies set commands.
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
Remove stale cloud-owned NAT source rules by resetting the cloud-owned `nat source` root.
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

Cloud-authoritative reset behavior:

```text
delete nat source
set nat source rule 100 ...
commit
```

Result:

```text
rule 110 is removed because `nat source` is reset, and rule 100 is recreated from current desired config.
```

### UC-04: VLAN removed from desired config

Goal:

```text
Remove stale cloud-owned bridge/VIF config by resetting the cloud-owned `interfaces bridge` root.
```

Old desired contains:

```text
set interfaces bridge br1 vif 10 ...
```

New desired omits that VLAN.

Cloud-authoritative reset behavior:

```text
delete interfaces bridge
set current rendered bridge commands
commit
```

Result:

```text
stale bridge/VIF config is removed because `interfaces bridge` is reset.
current desired bridge config is recreated from renderer output.
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
2. Apply engine starts managed-root reconciliation.
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

### UC-09: Empty desired config is rejected

Goal:

```text
Prevent accidental destructive managed-root deletion from an empty configure payload.
```

Input:

```text
desired config has UUID but no renderable production config
```

Renderer output:

```text
typed render error (for example missing_config or invalid_input)
```

Apply behavior:

```text
apply is not called
```

Result:

```text
managed roots are not deleted
temporary applied UUID state is not updated
agent publishes configure failure
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
- service LANs sorted with non-VLAN LANs in input-derived order followed by VLAN LANs by VLAN ID
- ethernet interfaces sorted by interface name
- NAT rules sorted by numeric rule ID
```

Apply planning must avoid:

```text
- random map iteration
- live-device-dependent delete plan generation
- hidden default deletes outside managed-root policy
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
- service DHCP/DNS/SSH normalization
- service SSH is emitted only when `services.ssh` is explicit
- DHCP ranges overlapping router, network, or broadcast addresses are rejected
- NAT canonical field handling
- renderer rejects empty desired config with UUID-only payload
- renderer rejects desired config with no renderable upstream interface
- renderer returns typed error (not empty RenderedText) for invalid supported sections
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
service-basic.json
nat-explicit.json
nat-absent.json
full-mvp.json
```

Required golden outputs:

```text
interface-basic.set
interface-vlan.set
service-basic.set
nat-explicit.set
nat-absent.set
full-mvp.set
```

### Apply unit tests

Required:

```text
- command parser handles quoted values with spaces
- command parser rejects unclosed quotes
- command parser normalizes CRLF input
- command parser handles blank lines and trailing spaces
- comment lines are rejected
- non-allowlisted set paths are rejected
- service command parser accepts only exact renderer-emitted DHCP/DNS/SSH forms
- executor is not called when command validation fails
- Prepare rejects unsafe commands
- Prepare returns deterministic managed-root delete/set plan
- Prepare includes `delete interfaces bridge`
- Prepare includes `delete nat source`
- Prepare includes `delete service dhcp-server`
- Prepare includes `delete service dns forwarding`
- Prepare does not include `delete service ssh`
- Prepare does not include broad `delete service`
- Prepare does not include delete commands for unmanaged roots
- Prepare uses DefaultResetPolicy when no override is provided
- WithResetPolicy replaces the default reset policy
- WithResetPolicy accepts allowed roots: `interfaces bridge`, `nat source`, `service dhcp-server`, `service dns forwarding`
- WithResetPolicy rejects empty root
- WithResetPolicy rejects full/root delete (`/`)
- WithResetPolicy rejects `system`, `service`, `service ssh`, `users`, `protocols`, `container`, `interfaces`, `interfaces ethernet`, and broad `nat`
- invalid ResetPolicy causes `apply.New(...)` to return an error
- Prepare allows set commands under unmanaged roots only when explicitly allowlisted
- Prepare rejects empty DesiredCommands
- Apply rejects empty DesiredCommands
- empty DesiredCommands does not produce managed-root delete commands
- empty DesiredCommands does not invoke executor
- empty DesiredCommands does not commit
- Apply invokes executor with the prepared plan
- executor receives structured DeleteCommands and SetCommands, not one arbitrary shell command string
- real executor implementation avoids unsafe shell interpolation
- Apply sends delete commands before set commands
- Apply sends service delete commands before service set commands
- Apply performs delete + set + commit in one candidate session
- Apply commits through executor
- Apply returns structured result
- NAT rule removal is handled by deleting `nat source`
- VLAN removal is handled by resetting `interfaces bridge`
- unmanaged roots are not deleted
- commit failure returns typed error and attempts discard
- save disabled by default
- apply.New installs the default persistent VyOS session executor
- WithExecutor(fake) overrides the default executor for tests
- default executor runs begin, deletes, sets, commit, optional save, and end in order
- default executor attempts discard on delete, set, and commit failure
- save failure after commit returns save_failed with Applied=true
- GitHub Actions CI runs gofmt check, go test ./..., and go test -race ./...
```

NATS client integration acceptance (external to this repo):

```text
- VyOS NATS client skips render/apply when applied_uuid equals desired.Record.UUID
- VyOS NATS client updates temporary applied UUID only after render and apply both succeed
- VyOS NATS client does not call apply when renderer fails or returns empty output
```

### Integration tests

Real VyOS tests can come later. MVP unit tests must use a fake executor.

### Lab-only VyOS smoke test

This repository includes a manual smoke command:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal-targeted \
  --save=false
```

Preview without applying:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal-targeted \
  --skip-apply
```

The command is lab-only and must not run in normal CI. It compiles during `go test ./...`, but real VyOS operations execute only when a user runs it manually with the explicit safety flag.

The smoke command must use the public apply path:

```text
apply.New()
  -> Prepare(ctx, input)
  -> Apply(ctx, input)
  -> default internal VyOS executor
```

It must not import `internal/vyos`, call executor internals, expose raw public `Run`/`Shell`/`Set`/`Commit`/`Save`/`Show` APIs, or add NATS/KV/result-publishing behavior.

Expected output excerpt:

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

Smoke result interpretation:

```text
delete interfaces bridge br0
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
commit=true
save=false
```

This means the smoke command will replace only the targeted smoke node `interfaces bridge br0`, then recreate it with DHCP, the smoke description, and `eth0` membership. All other VyOS config remains untouched unless it is under that targeted smoke path. The smoke payload intentionally does not change `interfaces ethernet eth0`. Use `--mode minimal-managed` to exercise the normal managed-root policy, which emits `delete interfaces bridge`, `delete nat source`, `delete service dhcp-server`, and `delete service dns forwarding`. Manual changes under `interfaces bridge br0`, such as changing the description, are expected to be overwritten on the next targeted smoke apply.

Expected verification commands:

```bash
show configuration commands | match "interfaces bridge"
show configuration commands | match "OLG_APPLY_SMOKE_TEST"
show configuration commands | match "interfaces ethernet eth0 description"
```

The ethernet description should remain whatever it was before the smoke test.

Manual mutation test:

```bash
configure
set interfaces bridge br0 description 'MANUAL_BR0_DESCRIPTION_TEST'
commit
exit
```

Rerun smoke and verify it returns to:

```text
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
```

Cleanup guidance:

```text
- The default smoke mode deletes and recreates interfaces bridge br0 with DHCP and eth0 membership.
- The minimal-managed smoke mode may delete interfaces bridge, nat source, service dhcp-server, and service dns forwarding through normal apply policy.
- Run only on a disposable/lab VyOS VM or router.
- Management networking can briefly flap during commit; prefer console access.
- Restore with known-good desired config through the normal NATS agent path, a lab snapshot/backup, or console recovery.
```

Minimal disposable-lab cleanup:

```bash
configure
delete interfaces bridge br0
commit
exit
```

Fake executor and fake runner tests remain required because a real smoke run validates only one image and cannot deterministically cover parser rejection, reset policy guards, command ordering, discard failure, save failure, close failure, context cancellation, session environment reuse, or command-boundary behavior.

---

## 31. Repository Layout

Current implementation layout:

```text
olg-renderer-vyos/
  go.mod
  README.md
  SPEC.md

  cmd/
    vyos-apply-smoke/
      main.go
      main_test.go

  docs/
    vyos-apply-smoke.md

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
    clones.go
    default_executor.go
    doc.go

    engine_test.go
    planner_test.go
    parser_test.go
    policy_test.go
    default_executor_test.go
    integration_test.go
    test_helpers_test.go

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
      doc.go
      runner.go
      runner_test.go

  testdata/
    valid/
      interface-basic.json
      interface-vlan.json
      service-basic.json
      nat-explicit.json
      nat-absent.json
      full-mvp.json

    golden/
      interface-basic.set
      interface-vlan.set
      service-basic.set
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

Layout notes:

```text
- renderer/ is the public render package that produces deterministic VyOS set commands.
- apply/ is the public apply engine. Prepare is optional non-executing preview; Apply is the production execution API.
- apply/default_executor.go installs and owns the default internal VyOS session executor used by apply.New().
- internal/vyos/ contains the controlled VyOS session runner used by the default executor. It is not a public executor package.
- cmd/vyos-apply-smoke/ is an opt-in lab-only command that uses apply.New(), Prepare(), and Apply(); it does not call internal/vyos directly.
- docs/vyos-apply-smoke.md documents manual smoke-test usage, expected logs, and cleanup guidance.
- NATS, KV, result/status publishing, and applied UUID state remain outside this repository.
```

Add future files only when implementation requires them.

---

## 32. Single-Phase Implementation Target

The apply engine MVP can be implemented in one coherent phase because it has a small scope.

Single-phase target:

```text
- public apply package
- typed apply errors
- Prepare and Apply APIs
- rendered set-command parser/validator
- managed-root reconciliation policy
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
- public Prepare API exists
- ResetPolicy is documented
- default managed roots are documented
- command validation is line-based and quote-aware
- command validation rejects comments, unclosed quotes, unsafe characters, and unsupported set paths
- Prepare validates commands and returns deterministic managed-root plan
- Prepare never invokes executor or changes device state
- Apply uses the same preparation logic and executes through executor
- no DryRun field exists in apply.Input
- Apply uses executor interface and fake executor tests pass
- real executor boundary is documented
- executor does not expose arbitrary shell command execution
- validated commands are passed as structured VyOS config operations
- Prepare returns managed-root delete commands for MVP roots: `delete interfaces bridge`, `delete nat source`, `delete service dhcp-server`, `delete service dns forwarding`
- Prepare generates delete commands from ResetPolicy
- roots outside ResetPolicy are preserved from broad deletion
- ResetPolicy roots are validated against the allowed managed-root list
- unsafe managed roots are rejected
- Prepare includes rendered set commands after delete commands
- empty DesiredCommands is rejected
- empty DesiredCommands never deletes managed roots
- empty DesiredCommands never invokes executor
- successful renderer output for configure is non-empty
- empty or non-operational desired config fails before apply
- reset-to-default is not represented by empty configure payload
- executor is not invoked when validation fails
- Apply executes delete and set commands in one candidate session
- Apply commits once
- Apply does not delete full config
- Apply does not delete unmanaged roots
- NAT source stale rules are removed by deleting `nat source`, not NAT rule range diff
- manual/debug config under managed roots is not guaranteed to survive
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
- firewall rendering
- routing rendering
- additional service rendering
- system rendering
- PKI rendering
- multiple schema versions
- schema fixture validation in CI
- VyOS schema command-path validation
```

Future apply work:

```text
- configurable managed roots
- explicit unmanaged-root boundaries
- optional baseline set commands if required later
- optional hooks for externally owned applied-state workflows
- optional save-after-commit mode
- commit-confirm support
- live config drift inspection
- performance measurement for render/prepare/apply/commit/save latency on larger configs
- old-vs-new diff mode if manual/local config preservation becomes a requirement
- expanded managed roots for future rendered sections
- explicit reset/default actions (for example `factory_reset`, `reset_to_bootstrap`, `clear_cloud_config`) with separate authorization and safety policy
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
managed-root reconciliation + rendered set commands + commit
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
