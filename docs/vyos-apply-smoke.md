# Lab-Only VyOS Apply Smoke Test

`cmd/vyos-apply-smoke` is a manual validation tool for a disposable or lab VyOS VM/device. It exercises the public apply path:

```text
apply.New()
  -> Prepare(ctx, input)
  -> Apply(ctx, input)
  -> default internal VyOS executor
  -> cli-shell-api getSessionEnv/setupSession
  -> my_delete/my_set/my_commit
  -> optional vyatta-cfg-cmd-wrapper save
  -> my_discard on failure
  -> cli-shell-api teardownSession
```

It is not part of CI and it is not a replacement for the fake executor and fake runner tests. Those tests remain necessary for deterministic parser, planner, failure-path, discard, save, close, session environment, and command-boundary coverage.

## Warning

This command modifies VyOS runtime configuration when `--skip-apply=false`.

The default smoke mode is `minimal-targeted`, which deletes and recreates:

```text
interfaces bridge br0
```

It recreates `br0` with DHCP, adds `eth0` as a bridge member, and sets the smoke description. Even though the final config recreates `br0`, the commit can briefly flap management networking. Prefer console access or a disposable VM.

The `minimal-managed` smoke mode uses the normal managed-root reconciliation policy and may delete and recreate these managed roots:

```text
interfaces bridge
nat source
service dhcp-server
service dns forwarding
service ssh
```

Run it only on a disposable/lab VyOS VM or router with console/recovery access. Do not run it on a production device. The smoke payload does not recreate SSH, so this mode may remove SSH access.

## Prerequisites

Run the command on a VyOS image where these binaries exist and are executable:

```text
/usr/bin/cli-shell-api
/opt/vyatta/sbin/my_set
/opt/vyatta/sbin/my_delete
/opt/vyatta/sbin/my_commit
/opt/vyatta/sbin/my_discard
```

When `--save=true`, the command also requires `/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper` because save runs as `vyatta-cfg-cmd-wrapper save`. `--save=false` does not require the wrapper. Use `--save=true` only after manually validating that save mechanism on the specific target VyOS image.

Modern VyOS rolling images may not have `/opt/vyatta/sbin/vyatta-save-config.pl`. The smoke command and default apply executor do not depend on that legacy helper.

The apply executor reuses one CLI Shell API session environment for all operations in a run and holds `/run/lock/olg-vyos-apply.lock` while the session is active. The lock path is under the normal runtime lock directory so the `vyos` user can run lab smoke tests without `sudo` on typical VyOS images. On normal failures or cancellation, it attempts discard and teardown with a bounded cleanup context. `SIGKILL`, VM crash, or power loss cannot be handled; if VyOS reports stale candidate overlays afterward, use console recovery, startup cleanup, or reboot the lab VM.

## Preview Only

Preview the apply input and plan without checking real VyOS binaries or applying changes:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal-targeted \
  --skip-apply
```

## Real Smoke Command

Run this only on a lab VyOS target:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal-targeted \
  --save=false
```

Optional modes:

```text
minimal-targeted  delete interfaces bridge br0, then recreate br0 with DHCP and eth0 membership
minimal-managed   normal managed-root policy, then recreate br0 with DHCP and eth0 membership
minimal           compatibility alias for minimal-targeted
bridge            compatibility alias for minimal-targeted
```

`nat` mode is intentionally disabled for now. The validated smoke path is the minimal bridge payload; a NAT smoke mode should only be added later if it is complete, explicit, and documented around managed-root implications.

`--save=false` is the default and recommended smoke setting. Use `--save=true` only when you intentionally want the committed runtime config persisted and have validated save on that VyOS image.

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
[smoke] building apply input
[smoke] target=vyos config_uuid=smoke-20260526T010203Z mode=minimal-targeted save=false skip_apply=false
[smoke] desired commands:
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
[smoke] creating apply engine
[smoke] previewing plan with Prepare
[smoke] plan delete_count=1 set_count=3 commit=true save=false
[smoke] delete[0]=delete interfaces bridge br0
[smoke] set[0]=set interfaces bridge br0 address dhcp
[smoke] set[1]=set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
[smoke] set[2]=set interfaces bridge br0 member interface eth0
[smoke] applying plan through Apply
[smoke] result applied=true saved=false
[smoke] delete_output=<empty>
[smoke] set_output=<empty>
[smoke] commit_output=<empty>
[smoke] save_output=<empty>
[smoke] discard_output=<empty>
[smoke] completed successfully
```

On failure, the command prints the typed apply error code when available, the partial result, discard output, and cleanup guidance.

## Smoke Result Interpretation

The default minimal-targeted preview plan:

```text
delete interfaces bridge br0
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
commit=true
save=false
```

means:

```text
- the smoke command will replace only the targeted smoke bridge node
- interfaces bridge br0 is deleted/recreated with DHCP and eth0 membership
- nat source, service dhcp-server, service dns forwarding, and service ssh are not touched by the default smoke mode
- all other VyOS config remains untouched unless it is under the targeted smoke path
```

After a successful runtime-only smoke apply on VyOS rolling, expected config includes:

```text
interfaces {
    bridge br0 {
        address dhcp
        description OLG_APPLY_SMOKE_TEST
        member {
            interface eth0 {
            }
        }
    }
}
```

Manual changes under `interfaces bridge br0` are expected to be overwritten on next targeted smoke apply because that node is reset by the smoke policy. The smoke test intentionally does not change `interfaces ethernet eth0`.

The managed-root smoke mode preview:

```text
delete interfaces bridge
delete nat source
delete service dhcp-server
delete service dns forwarding
delete service ssh
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
commit=true
save=false
```

means the normal apply policy will replace only managed roots. `interfaces bridge` is managed and therefore deleted/recreated, and the smoke payload recreates `br0` with DHCP and eth0 membership. `nat source`, `service dhcp-server`, `service dns forwarding`, and `service ssh` are managed and therefore deleted/recreated. Broad `service` is not reset by default. All other VyOS config remains untouched unless it is under a managed root.

This is safer than whole-device reconciliation for the current phase. The smoke test does not delete everything except a preserve whitelist; it deletes only declared managed roots. A future full-device reconciliation mode would need to be separate, explicit, and protected by stronger safeguards because an incomplete preserve list could remove SSH, login, WAN, NTP, or system configuration.

## Verification Commands

Preview:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal-targeted \
  --skip-apply
```

Apply runtime config only:

```bash
~/vyos-apply-smoke \
  --i-understand-this-modifies-vyos \
  --mode minimal-targeted \
  --save=false
```

Verify:

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

Rerun the smoke apply and verify the bridge config returns to:

```text
set interfaces bridge br0 address dhcp
set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'
set interfaces bridge br0 member interface eth0
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
