# Lab-Only VyOS Apply Smoke Test

`cmd/vyos-apply-smoke` is a manual validation tool for a disposable or lab VyOS VM/device. It exercises the public apply path:

```text
apply.New()
  -> Prepare(ctx, input)
  -> Apply(ctx, input)
  -> default internal VyOS executor
  -> cli-shell-api getSessionEnv/setupSession/teardownSession
  -> my_delete/my_set/my_commit/my_discard
```

It is not part of CI and it is not a replacement for the fake executor and fake runner tests. Those tests remain necessary for deterministic parser, planner, failure-path, discard, save, teardown, and session-env coverage.

## Warning

This command modifies VyOS runtime configuration when `--skip-apply=false`.

The normal apply policy may delete and recreate these reset roots:

```text
interfaces bridge
nat source
```

Run it only on a disposable/lab VyOS VM or router with console/recovery access. Do not run it on a production device.

## Prerequisites

Run the command on a VyOS image where these binaries exist and are executable:

```text
/usr/bin/cli-shell-api
/opt/vyatta/sbin/my_set
/opt/vyatta/sbin/my_delete
/opt/vyatta/sbin/my_commit
/opt/vyatta/sbin/my_discard
/opt/vyatta/sbin/vyatta-save-config.pl
```

The command checks these paths before calling `Apply`.

## Preview Only

Preview the apply input and plan without checking real VyOS binaries or applying changes:

```bash
go run ./cmd/vyos-apply-smoke --i-understand-this-modifies-vyos --mode minimal --skip-apply
```

## Real Smoke Command

Run this only on a lab VyOS target:

```bash
go run ./cmd/vyos-apply-smoke --i-understand-this-modifies-vyos --mode minimal --save=false
```

Optional modes:

```text
minimal  set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
bridge   same as minimal
nat      set nat source rule 9999 translation address masquerade
```

`--save=false` is the default. Use `--save=true` only when you intentionally want the committed runtime config persisted.

## Expected Output Excerpt

```text
[smoke] starting VyOS apply smoke test
[smoke] warning: this modifies VyOS runtime configuration
[smoke] checking required binaries
[smoke] found /usr/bin/cli-shell-api
[smoke] found /opt/vyatta/sbin/my_set
[smoke] found /opt/vyatta/sbin/my_delete
[smoke] found /opt/vyatta/sbin/my_commit
[smoke] found /opt/vyatta/sbin/my_discard
[smoke] found /opt/vyatta/sbin/vyatta-save-config.pl
[smoke] building apply input
[smoke] target=vyos config_uuid=smoke-20260526T010203Z mode=minimal save=false skip_apply=false
[smoke] desired commands:
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
[smoke] creating apply engine
[smoke] previewing plan with Prepare
[smoke] plan delete_count=2 set_count=1 commit=true save=false
[smoke] delete[0]=delete interfaces bridge
[smoke] delete[1]=delete nat source
[smoke] set[0]=set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
[smoke] applying plan through Apply
[smoke] result applied=true saved=false
[smoke] commit_output=<empty>
[smoke] save_output=<empty>
[smoke] discard_output=<empty>
[smoke] completed successfully
```

On failure, the command prints the typed apply error code when available, the partial result, discard output, and cleanup guidance.

## Cleanup And Rollback

The smoke command does not automatically run cleanup. Cleanup itself can be destructive, and the correct rollback depends on the lab topology.

Recommended rollback options:

```text
- Re-apply known-good desired config through the normal NATS agent path.
- Restore the lab VM/router from snapshot or backup.
- Use VyOS console/recovery access to restore config manually.
```

NATS, KV access, result/status publishing, and applied UUID state are intentionally outside this repository.
