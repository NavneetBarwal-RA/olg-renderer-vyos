// Package apply validates renderer-generated VyOS set commands, prepares a
// deterministic cloud-authoritative reset plan with protected roots, and applies
// that plan through a caller-provided executor.
//
// The MVP reset strategy deletes only explicit cloud-controlled reset roots,
// then applies the validated rendered set commands and commits once. The default
// reset roots are "interfaces bridge" and "nat source". Roots such as system,
// service, users, protocols, container, broad interfaces, and interfaces
// ethernet are protected from broad deletion by omission from the reset policy.
// Specific preserved-root commands may still be allowlisted, such as renderer
// emitted ethernet description commands.
//
// Prepare validates input and returns a non-executing Plan. Apply uses the same
// preparation logic and then calls the configured Executor with structured delete
// and set command slices. ConfigUUID is traceability metadata only; the package
// never uses it for duplicate detection or applied-state comparison.
//
// This package does not render OLG/uCentral JSON, connect to NATS, read or write
// JetStream KV, publish result or status messages, own applied UUID state, or
// expose arbitrary shell execution. Those responsibilities belong to the caller,
// such as olg-server-vyos-client-natagent.
package apply
