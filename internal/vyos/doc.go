// Package vyos contains the controlled VyOS command runner used by the public
// apply package's default executor.
//
// The package follows the documented VyOS CLI Shell API session model:
// cli-shell-api getSessionEnv/setupSession, my_delete/my_set/my_commit,
// optional wrapper save, my_discard on failure, and cli-shell-api
// teardownSession. The default session identifier is the apply process ID. The
// session environment returned by getSessionEnv is reused for every operation
// in a single apply.
//
// The runner accepts only set/delete configuration commands from a validated
// apply.Plan, preserves command boundaries by invoking binaries with an argument
// vector per operation, and rejects obvious internal misuse. It does not expose
// a public raw shell API and it does not concatenate rendered configuration into
// a shell script.
package vyos
