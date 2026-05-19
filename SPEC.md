# olg-renderer-vyos SPEC

## 1. Overview

`olg-renderer-vyos` is a Go library that converts OLG/uCentral JSON configuration into VyOS-style text configuration.

The renderer is specific to VyOS.

It is designed to be called by `olg-server-vyos-client-natagent` after the agent has loaded desired config from NATS KV using `nats-agent-core`.

This repository owns only:

```text
JSON input -> normalized render data -> templates -> VyOS-style text output
```

---

## 2. Design Goals

The renderer must be:

```text
- small
- deterministic
- side-effect free
- template-driven
- schema-version aware
- easy to test
- easy to integrate into olg-server-vyos-client-natagent
```

For the same input JSON and same renderer version, the renderer must produce the same output text.

---

## 3. Permanent Boundaries

`olg-renderer-vyos` must not own:

```text
- NATS connection
- JetStream KV access
- configure/action handler registration
- result/status publishing
- local applied UUID state
- VyOS apply/commit/save/discard
- shell command execution
- live device lifecycle management
```

Those responsibilities belong to `nats-agent-core`, `olg-server-vyos-client-natagent`, and the VyOS apply engine.

---

## 4. Related Components

Only high-level relationship is required.

```text
olg-ucentral-schema
  schema files and validation rules

olg-ucentral-client
  validates and submits desired config

nats-agent-core
  NATS, KV, subjects, desired config storage, result/status envelopes

olg-server-vyos-client-natagent
  loads desired config, calls renderer, calls apply engine, publishes result/status

olg-renderer-vyos
  renders JSON config into VyOS-style text
```

The renderer does not read directly from the KV bucket.

---

## 5. Input Contract

Canonical field names used in this SPEC:

```text
- target
- config_uuid
- schema_name
- schema_version
- payload_json
```

The renderer input must include:

```text
- target
- config_uuid
- schema_name
- schema_version
- payload_json
```

Expected values for the first MVP:

```text
target = vyos
schema_name = olg-ucentral
schema_version = first supported OLG/uCentral schema version
```

`payload_json` should contain the desired configuration object and schema metadata.

Metadata precedence and consistency rules:

```text
1. Top-level renderer input metadata (target/config_uuid/schema_name/schema_version) is required.
2. Payload metadata is optional.
3. If payload metadata exists, it must match top-level metadata exactly.
4. Any mismatch must return metadata_mismatch.
```

Payload extraction path contract for `payload_json` (MVP):

```text
1. config object:
   - primary: $.config
   - required for MVP
2. schema_name:
   - primary: $.schema.name
   - accepted alias: $.schema_name
3. schema_version:
   - primary: $.schema.version
   - accepted alias: $.schema_version
4. target (optional):
   - accepted path: $.target
   - if present, it must match top-level target
```

If a required path is missing, renderer must return `missing_config` or `metadata_mismatch` (as appropriate) and must not continue to rendering.

---

## 6. Output Contract

The renderer output must include:

```text
- target
- config_uuid
- schema_name
- schema_version
- rendered_text
```

The renderer returns text only.

It must not write the rendered text to disk.

Rendered text output contract (MVP):

```text
1. Output is UTF-8 VyOS CLI set-command text.
2. One command per line.
3. Line endings are \n (LF), including a trailing newline at EOF.
4. Deterministic ordering is required:
   - top-level section order: interface, nat
   - stable deterministic sorting within each section.
5. Renderer output must not include operational/session commands:
   - configure
   - commit
   - save
   - discard
   - exit
6. If no supported section is present, renderer may return empty rendered_text.
```

This section defines renderer output shape only. Execution/apply behavior remains the responsibility of `olg-server-vyos-client-natagent` and its apply engine.

---

## 7. Public API Requirement

The public package should expose a minimal API.

At a high level, it must support:

```text
- creating a renderer instance
- rendering JSON payload into VyOS text
- returning renderer metadata such as target and supported schema versions
```

The public package should not expose internal template or normalization details.

Exact Go structs and function signatures should be finalized during implementation, not over-specified in this SPEC.

---

## 8. Schema Compatibility

The renderer must check compatibility before rendering.

Required checks:

```text
- target is supported
- schema_name is supported
- schema_version is supported
```

The renderer must not use `agentcore.Config.Version` or NATS envelope version as the OLG/uCentral schema version.

These are different version concepts.

Unsupported `target`, `schema_name`, or `schema_version` must return typed renderer errors.

---

## 9. Rendering Pipeline

The renderer must follow this pipeline:

```text
render
  -> validate context/input metadata
  -> check schema compatibility
  -> decode JSON payload
  -> extract config object
  -> normalize supported sections
  -> render templates in fixed order
  -> return rendered output
```

Initial render order:

```text
interface
nat
```

Future render sections can be added later without changing the public API.

---

## 10. Normalization Layer

The renderer must use a small normalization layer.

Purpose:

```text
payload_json
  -> template-friendly data
```

The normalizer should handle:

```text
- payload wrapper parsing
- config object extraction
- field alias handling
- interface grouping
- bridge/VLAN data preparation
- NAT rule preparation
- deterministic sorting
```

Templates should not contain complex decision logic.

---

## 11. Template Layer

Templates produce the final VyOS-style text.

Initial template structure:

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

`templates.go` should:

```text
- embed templates
- execute templates
- control top-level render order
- join non-empty rendered sections
```

No root template is required initially.

The rule is:

```text
templates.go controls render order.
top-level templates render major config sections.
nested templates render sub-objects.
```

---

## 12. Interface Rendering Requirement

Initial interface rendering should support the useful subset required for the first VyOS output.

It should handle:

```text
- interface configuration from the payload
- upstream/downstream roles
- bridge data preparation
- ethernet member data preparation
- VLAN/VIF data preparation where supported
- deterministic ordering
```

The renderer must not inspect the live VyOS device to discover interfaces.

Device-specific discovery, if needed later, must be handled outside this renderer or passed in as explicit input.

---

## 13. NAT Rendering Requirement

Initial NAT rendering should support explicit NAT rules.

It should handle:

```text
- explicit NAT rules from the payload
- required NAT rule fields
- accepted field aliases if needed
- deterministic rule ordering
```

Important rule:

```text
NAT is explicit and optional.
```

If NAT is absent, the renderer must not invent NAT behavior.

Auto-generated NAT must not be added unless it becomes an explicit product requirement.

---

## 14. Deterministic Output

Rendered output must be stable.

The renderer must avoid:

```text
- random map iteration
- timestamps
- environment-specific values
- live-device-specific values
- generated comments that change between runs
```

Golden tests should compare exact output.

---

## 15. Error Model

The renderer must return typed errors with stable error codes.

Suggested error categories:

```text
invalid_input
invalid_json
unsupported_schema
unsupported_schema_version
unsupported_target
metadata_mismatch
missing_config
normalize_failed
render_failed
template_failed
```

`olg-server-vyos-client-natagent` can map these codes into configure result errors.

---

## 16. Context Handling

The render operation must accept a context.

Rules:

```text
- nil context is invalid
- canceled context must stop rendering before meaningful work continues
- internal operations should check context at safe boundaries
```

The renderer must not store caller contexts.

---

## 17. Logging

The renderer should not log by default.

The caller owns operational logging.

If logging is added later, it should be optional and must not log:

```text
- full payload
- secrets
- rendered config text by default
```

---

## 18. Minimal Repository Structure

Initial structure:

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
      basic.json
      explicit-nat.json
      vlan.json

    golden/
      basic.vyos
      explicit-nat.vyos
      vlan.vyos
```

Add new folders/files only when there is real renderer logic for them.

---

## 19. Integration With olg-server-vyos-client-natagent

The agent should integrate this renderer through an adapter.

The adapter converts from the agent's desired config record into the renderer input.

Required adapter contract for current placeholder replacement:

```go
// Existing natagent-side contract to preserve:
Render(ctx context.Context, desired agentcore.StoredDesiredConfig) (renderer.Output, error)

type Output struct {
    Target string
    UUID   string
    Text   string
}
```

Adapter behavior requirements:

```text
1. Read desired metadata from desired.Record and payload_json.
2. Build renderer input with target/config_uuid/schema_name/schema_version/payload_json.
3. Call olg-renderer-vyos render method.
4. Return natagent renderer.Output where:
   - Output.Target = desired.Record.Target
   - Output.UUID   = desired.Record.UUID
   - Output.Text   = rendered_text from olg-renderer-vyos
5. Do not perform NATS/KV/apply logic in the adapter.
```

Recommended mapping:

```text
desired target  -> renderer target
desired UUID    -> renderer config_uuid
desired payload -> renderer payload_json
payload schema  -> renderer schema_name
payload version -> renderer schema_version
```

The agent configure flow should remain:

```text
LoadDesiredConfig
  -> compare desired UUID with local state
  -> call renderer
  -> call apply engine
  -> update state on success
  -> publish result/status
```

Only the renderer implementation changes:

```text
placeholder renderer
  -> olg-renderer-vyos adapter
```

---

## 20. Failure Behavior During Agent Integration

If rendering fails:

```text
1. renderer returns typed error.
2. olg-server-vyos-client-natagent does not call apply engine.
3. olg-server-vyos-client-natagent does not update applied UUID state.
4. olg-server-vyos-client-natagent publishes configure failure status/result.
```

Examples:

```text
unsupported_schema_version -> configure failure
invalid_json               -> configure failure
render_failed              -> configure failure
```

Recommended typed-error mapping from `olg-renderer-vyos` to natagent configure result `error_code`:

```text
unsupported_schema           -> render_failed
unsupported_schema_version   -> render_failed
unsupported_target           -> render_failed
metadata_mismatch            -> render_failed
missing_config               -> render_failed
invalid_json                 -> render_failed
normalize_failed             -> render_failed
template_failed              -> render_failed
render_failed                -> render_failed
```

Implementation note:

```text
- natagent can keep a single public failure code (render_failed) for wire compatibility.
- typed renderer codes should still be logged and preserved in wrapped internal errors for debugging.
```

---

## 21. Testing Requirements

### Unit tests

Required:

```text
- input validation
- schema compatibility
- payload parsing
- normalization behavior
- typed error codes
```

### Golden tests

Required:

```text
testdata/valid/*.json
  -> render
  -> compare with testdata/golden/*.vyos
```

### Compatibility tests

Use fixtures aligned with `olg-ucentral-schema`.

Initial approach:

```text
copy fixed fixtures into testdata
```

Later CI may pin `olg-ucentral-schema` and validate fixtures with schema tooling.

The renderer must not depend on schema validation scripts at runtime.

---

## 22. Single MVP Development Scope

The first implementation should be one complete MVP.

Implement:

```text
- Go module
- public renderer package
- typed error model
- schema compatibility check
- JSON payload parsing
- minimal normalization
- interface rendering
- explicit NAT rendering
- templates grouped by config object
- golden tests
```

Acceptance criteria:

```text
- go build ./... succeeds
- go test ./... succeeds
- valid JSON fixtures render expected VyOS text
- unsupported schema/version/target returns typed errors
- invalid JSON returns typed error
- output is deterministic
- no NATS/KV/apply/shell/device lifecycle logic is added
```

---

## 23. Future Renderer Scope

Future renderer sections may include:

```text
- DHCP
- DNS
- firewall
- routing
- system
- service
- PKI
```

Each future section should follow the same pattern:

```text
raw JSON section
  -> normalize
  -> template
  -> golden test
```

These are valid renderer features, but they are not required for the initial MVP.

---

## 24. Design Summary

`olg-renderer-vyos` must remain a pure renderer.

It should:

```text
- receive JSON payload and metadata
- check compatibility
- normalize supported fields
- render deterministic VyOS text
- return output or typed error
```

It should not:

```text
- connect to NATS
- read KV
- apply config
- execute commands
- manage state
- publish status/result
```
