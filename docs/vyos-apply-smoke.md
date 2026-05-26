# Lab-Only VyOS Apply Smoke Test

`cmd/vyos-apply-smoke` is a manual validation tool for a disposable or lab VyOS VM/device. It exercises the public apply path:

```text
apply.New()
  -> Prepare(ctx, input)
  -> Apply(ctx, input)
  -> default internal VyOS executor
  -> vyatta-cfg-cmd-wrapper begin/delete/set/commit/end
  -> optional vyatta-cfg-cmd-wrapper save
  -> vyatta-cfg-cmd-wrapper discard on failure
```

It is not part of CI and it is not a replacement for the fake executor and fake runner tests. Those tests remain necessary for deterministic parser, planner, failure-path, discard, save, end, and wrapper command coverage.

## Warning

This command modifies VyOS runtime configuration when `--skip-apply=false`.

The normal managed-root reconciliation policy may delete and recreate these managed roots:

```text
interfaces bridge
nat source
```

Run it only on a disposable/lab VyOS VM or router with console/recovery access. Do not run it on a production device.

## Prerequisites

Run the command on a VyOS image where this binary exists and is executable:

```text
/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper
```

The command checks this path before calling `Apply`. Save does not require a separate helper; when `--save=true`, save runs as `vyatta-cfg-cmd-wrapper save`.

Modern VyOS rolling images may not have `/opt/vyatta/sbin/vyatta-save-config.pl`. The smoke command and default apply executor do not depend on that legacy helper.

## Preview Only

Preview the apply input and plan without checking real VyOS binaries or applying changes:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal \
  --skip-apply
```

## Real Smoke Command

Run this only on a lab VyOS target:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal \
  --save=false
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
[smoke] found /opt/vyatta/sbin/vyatta-cfg-cmd-wrapper
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

## Smoke Result Interpretation

The minimal preview plan:

```text
delete interfaces bridge
delete nat source
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
commit=true
save=false
```

means:

```text
- the renderer/apply engine will replace only managed roots
- interfaces bridge is managed and therefore deleted/recreated
- nat source is managed and therefore deleted/recreated
- all other VyOS config remains untouched unless it is under a managed root
```

After a successful runtime-only smoke apply on VyOS rolling, expected config includes:

```text
interfaces {
    bridge br0 {
        description OLG_APPLY_SMOKE_TEST
    }
}
```

Manual changes under `interfaces bridge` are expected to be overwritten on next apply because `interfaces bridge` is a managed root.

This is safer than whole-device reconciliation for the current phase. The smoke test does not delete everything except a preserve whitelist; it deletes only declared managed roots. A future full-device reconciliation mode would need to be separate, explicit, and protected by stronger safeguards because an incomplete preserve list could remove SSH, login, WAN, NTP, or system configuration.

## Verification Commands

Preview:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal \
  --skip-apply
```

Apply runtime config only:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal \
  --save=false
```

Verify:

```bash
show configuration commands | match "interfaces bridge"
show configuration commands | match "OLG_APPLY_SMOKE_TEST"
```

Manual mutation test:

```bash
configure
set interfaces bridge br0 description 'MANUAL_BR0_DESCRIPTION_TEST'
commit
exit
```

Rerun the smoke apply and verify the bridge description returns to:

```text
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
```

## Cleanup And Rollback

The smoke command does not automatically run cleanup. Cleanup itself can be destructive, and the correct rollback depends on the lab topology.

Recommended rollback options:

```text
- Re-apply known-good desired config through the normal NATS agent path.
- Restore the lab VM/router from snapshot or backup.
- Use VyOS console/recovery access to restore config manually.
```

Minimal smoke cleanup for a disposable lab VM:

```bash
configure
delete interfaces bridge br0
commit
exit
```

NATS, KV access, result/status publishing, and applied UUID state are intentionally outside this repository.
