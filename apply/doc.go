// Package apply validates renderer-generated VyOS set commands, prepares a
// deterministic managed-root reconciliation plan, and applies that plan through
// the default VyOS executor unless a caller overrides it.
//
// The MVP managed-root strategy deletes only explicit cloud-controlled managed
// roots, then applies the validated rendered set commands and commits once. The
// default managed roots are "interfaces bridge", "nat source",
// "service dhcp-server", and "service dns forwarding". Roots such as system,
// broad service, service ssh, users, protocols, container, broad interfaces,
// and interfaces ethernet are preserved from broad deletion by omission from
// the reset policy. Specific unmanaged-root commands may still be allowlisted,
// such as renderer emitted ethernet description and SSH port commands.
//
// Prepare validates input and returns a non-executing Plan. Apply is the
// production execution API: it uses the same preparation logic, calls the default
// controlled VyOS executor with structured delete and set command slices,
// enters one persistent VyOS CLI Shell API session, mutates config through
// my_delete/my_set/my_commit, discards on failure, tears down the session, and
// saves through wrapper save only when configured and manually validated for
// the target image. ConfigUUID is traceability metadata only; the package never
// uses it for duplicate detection or applied-state comparison.
//
// WithExecutor is available for tests and advanced controlled integrations, but
// custom executors can bypass runtime execution safety if implemented
// incorrectly. Normal production callers should use New without overriding the
// executor.
//
// This package does not render OLG/uCentral JSON, connect to NATS, read or write
// JetStream KV, publish result or status messages, own applied UUID state, or
// expose arbitrary shell execution. Those responsibilities belong to the caller,
// such as olg-server-vyos-client-natagent.
package apply
