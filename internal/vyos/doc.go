// Package vyos contains the controlled VyOS command runner used by the public
// apply package's default executor.
//
// The package follows the modern VyOS configuration wrapper model:
// vyatta-cfg-cmd-wrapper begin/delete/set/commit/save/discard/end. It uses an
// absolute wrapper path by default and assumes this model matches the deployed
// VyOS image. Real target-image validation remains required before production
// rollout.
//
// The runner accepts only set/delete configuration commands from a validated
// apply.Plan, preserves command boundaries by invoking binaries with an argument
// vector per operation, and rejects obvious internal misuse. It does not expose
// a public raw shell API and it does not concatenate rendered configuration into
// a shell script.
package vyos
