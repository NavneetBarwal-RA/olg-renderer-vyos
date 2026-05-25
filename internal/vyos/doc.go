// Package vyos contains the controlled VyOS command runner used by the public
// apply package's default executor.
//
// The package uses /usr/bin/cli-shell-api by default and assumes those
// invocations participate in the intended VyOS candidate configuration
// transaction on the target image. That transaction/session behavior must be
// validated on the deployed VyOS version before production rollout.
//
// The runner accepts only set/delete configuration commands from a validated
// apply.Plan, preserves command boundaries by invoking cli-shell-api with an
// argument vector for each configuration operation, and rejects obvious internal
// misuse. It does not expose a public raw shell API and it does not concatenate
// rendered configuration into a shell script.
package vyos
