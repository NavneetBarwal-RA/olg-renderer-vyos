// Package vyos contains the controlled VyOS command runner used by the public
// apply package's default executor.
//
// The package assumes the target VyOS image provides cli-shell-api on PATH and
// accepts configure, set/delete path arguments, commit, save, and discard as
// separate argv-style invocations. It preserves command boundaries by invoking
// cli-shell-api with an argument vector for each configuration operation. It
// does not expose a public raw shell API and it does not concatenate rendered
// configuration into a shell script.
package vyos
