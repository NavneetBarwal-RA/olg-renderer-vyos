# VyOS Config Schema Source

Place the target VyOS config schema snapshot here:

```text
schemas/vyos/vyos-config-schema.json
```

MVP approach:

```text
- keep the manually prepared schema snapshot here
- use it for documentation and optional path validation tests
```

Future approach:

```text
- generate or copy this file from the VyOS build system
- ensure it matches the exact VyOS image where the agent will run
- record the schema hash in schemas/manifest.json
```
