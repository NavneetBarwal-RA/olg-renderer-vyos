# olg-renderer-vyos

`olg-renderer-vyos` is a Go library that renders OLG/uCentral JSON into VyOS-style configuration text.

This repository is intentionally narrow: it owns rendering logic only.

## Purpose

```text
JSON input
  -> normalize supported sections
  -> render templates in fixed order
  -> return deterministic VyOS-style text
```

The rendered text is consumed by `olg-server-vyos-client-natagent`, which handles apply/state/result orchestration.

## Scope

`olg-renderer-vyos` owns:

```text
- JSON-to-VyOS rendering
- schema compatibility checks (target/schema_name/schema_version)
- normalization for template-friendly data
- deterministic text output
- typed render errors
```

`olg-renderer-vyos` does not own:

```text
- NATS/JetStream/KV access
- configure/action orchestration
- apply/commit/save/discard behavior
- local state tracking
- result/status publishing
- shell/device lifecycle operations
```

## Integration Context

High-level flow with `olg-server-vyos-client-natagent`:

```text
LoadDesiredConfig
  -> build renderer input
  -> render with olg-renderer-vyos
  -> on success: pass text to apply engine
  -> on render failure: publish configure failure, skip apply
```

## Canonical Input/Output Fields

Use these names consistently:

```text
- target
- config_uuid
- schema_name
- schema_version
- payload_json
```

Output includes:

```text
- target
- config_uuid
- schema_name
- schema_version
- rendered_text
```

## MVP Rendering Scope

Initial supported sections:

```text
- interface
- explicit NAT
```

NAT rule remains explicit and optional; renderer must not auto-generate NAT unless explicitly required.

## Repository Layout (MVP)

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
    golden/
```

## Spec

Normative behavior, contracts, failure handling, and acceptance criteria are defined in `SPEC.md`.

- Read [SPEC.md](./SPEC.md) before implementing or integrating renderer changes.
