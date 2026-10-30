package policy

import "errors"

// The policy half of governance (plan task E1.2, design ee-boundary-moves.md
// §5.2). Governance is not a package to move: the vocabulary, the wire and the
// mechanism stay in the core, because strict decoding means an untagged runner
// that did not know the block would refuse the file and stop every repository
// of the project (F9, R7). What moves to ee/governance is the policy itself —
// the values Fugaro ships and the rule that a layer may only tighten them.
//
// This file is the seam between the two, and it is deliberately narrow: the
// core's only reach for the shipped values is Defaults, and the only reach for
// the tighten-only rule is Tighten. WithDefaults and DefaultGovernanceKeys go
// through the first, Merge through the second, so the values and the rule
// living in ee/governance needs no other core edit.

// GovernanceFuncs is the policy half of governance: what Fugaro ships and how
// a layer tightens it. The core default is the zero pair — no shipped values
// and a rule that tightens nothing — so an untagged build decodes, validates
// and then ignores a governance: block (D522, G2).
type GovernanceFuncs struct {
	// Defaults is the governance every run starts under.
	Defaults func() Governance
	// Tighten applies one layer's governance limits to the running effective
	// ones, recording a looser value in Effective.Ignored.
	Tighten func(e *Effective, g Governance, src string)
}

// governanceHooks is the installed pair. Only ee/register may call
// SetGovernance: the core never sets it, so an untagged build reads and
// validates a governance: block and holds no run to any of it.
var governanceHooks = coreGovernanceFuncs()

// governanceHooksSet records that a caller installed a pair, which only
// SetGovernance does.
var governanceHooksSet bool

// SetGovernance installs the policy half of governance, refusing a second
// call: what a build enforces is decided once, by ee/register, and a hook that
// could be replaced after it would be a way to loosen governance in a tagged
// binary. A pair with a nil function is refused for the same reason — the
// merge calls both, and a nil one is a panic rather than a decision.
//
// Nothing calls it in an untagged build, and the core default is deliberately
// not nil: the merge and the two readers call through the pair on every run,
// so the default has to answer. It answers the untagged build's own truth —
// nothing shipped, nothing tightened (D522).
func SetGovernance(f GovernanceFuncs) error {
	if governanceHooksSet {
		return errors.New("policy: the governance hook is already set")
	}
	if f.Defaults == nil || f.Tighten == nil {
		return errors.New("policy: the governance hook needs both Defaults and Tighten")
	}
	governanceHooks, governanceHooksSet = f, true
	return nil
}

// governanceHook is the installed pair.
func governanceHook() GovernanceFuncs { return governanceHooks }

// coreGovernanceFuncs is the untagged build's policy: no governance, and a
// rule that tightens nothing. A build that links ee/register replaces the pair
// with the one ee/governance carries, and the difference between the two is
// the whole of the edition boundary for these keys (D521, D522, G10).
func coreGovernanceFuncs() GovernanceFuncs {
	return GovernanceFuncs{
		Defaults: func() Governance { return Governance{} },
		Tighten:  func(*Effective, Governance, string) {},
	}
}
