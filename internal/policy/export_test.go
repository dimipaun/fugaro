package policy

import "testing"

// SetCore makes the policy hook the pair an untagged build has after the move
// to ee/governance (E2.3): no shipped defaults and a tighten rule that does
// nothing, so a governance: block is decoded, validated and then ignored —
// which is the whole of what an untagged build does with it (D522, G2). The
// strict decoding that lets the block through is the core's and stays
// (F9).
//
// It lives here rather than in production code because it is the pair that
// switches enforcement off, and only a test may do that: a production seam
// would be a way for a core caller to loosen governance in a tagged binary
// (design §5.5).
func SetCore(t *testing.T) {
	prev, wasSet := governanceHooks, governanceHooksSet
	governanceHooks = GovernanceFuncs{
		Defaults: func() Governance { return Governance{} },
		Tighten:  func(*Effective, Governance, string) {},
	}
	governanceHooksSet = false
	t.Cleanup(func() { governanceHooks, governanceHooksSet = prev, wasSet })
}

// SetHooks installs f as the policy hook for the rest of t, so a test can
// watch what a merger or a default read is asked for. It restores the previous
// pair whatever f is, which SetGovernance does not: the rule that a second
// caller is refused is SetGovernance's, and a test's probe is not a caller.
func SetHooks(t *testing.T, f GovernanceFuncs) {
	prev := governanceHooks
	governanceHooks = f
	t.Cleanup(func() { governanceHooks = prev })
}
