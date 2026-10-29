package policy

import "testing"

// SetHooks installs f as the policy hook for the rest of t, so a test can
// watch what a merger or a default read is asked for. It restores the previous
// pair whatever f is, which SetGovernance does not: the rule that a second
// caller is refused is SetGovernance's, and a test's probe is not a caller.
func SetHooks(t *testing.T, f GovernanceFuncs) {
	prev := governanceHooks
	governanceHooks = f
	t.Cleanup(func() { governanceHooks = prev })
}

// SetGovernanceHook is SetGovernance, so a test can exercise the refusals that
// keep a tagged binary's enforcement from being replaced or halved (§5.5).
var SetGovernanceHook = SetGovernance

// ResetHooks puts the policy hook back to the core default and clears the
// "already set" flag, before and after t: SetGovernance refuses a second call
// for the life of the process, so a test that calls it would otherwise leave
// the whole package hooked.
func ResetHooks(t *testing.T) {
	t.Helper()
	governanceHooks, governanceHooksSet = coreGovernanceFuncs(), false
	t.Cleanup(func() { governanceHooks, governanceHooksSet = coreGovernanceFuncs(), false })
}
