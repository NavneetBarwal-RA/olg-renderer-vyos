# Schema Files

This directory is for schema snapshots and schema metadata used by `olg-renderer-vyos`.

Schemas are not fetched at runtime. They are checked in or generated/copied during CI/build from pinned sources.

## Layout

```text
schemas/
  manifest.example.json
  ucentral/
    schema.json
    ucentral.schema.full.json
    SOURCE.md
  vyos/
    vyos-config-schema.json
    SOURCE.md
  examples/
    renderer-input-wrapper.example.json
```

## What belongs here

### `schemas/ucentral/schema.json`

Version metadata for the supported OLG/uCentral schema.

This should come from the same source ref as `ucentral.schema.full.json`.

### `schemas/ucentral/ucentral.schema.full.json`

The full OLG/uCentral JSON schema used to validate renderer fixtures in CI/build.

For MVP, this can be copied manually from a pinned tag or commit.

### `schemas/vyos/vyos-config-schema.json`

A snapshot of the target VyOS configuration schema.

For MVP, this can be the manually prepared schema snapshot. Later, the VyOS build system should provide or generate this file for the exact target image.

### `schemas/manifest.example.json`

A small manifest recording schema names, versions, source refs, and hashes.

The manifest helps track exactly which schema snapshots the renderer was tested against.

## Runtime rule

The renderer must not fetch schema files from Git or the network at runtime.
