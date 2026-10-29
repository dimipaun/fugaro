// Package rigor is the rigor mode's vocabulary and its one resolver: how
// much cost a run accepts to reduce risk, and which of the layers that may
// set it won (docs/rigor-modes/spec.md, docs/rigor-modes/reconciliation.md
// D6, D19–D23).
//
// It is the only place that orders modes. No other package may compare two
// of them: the precedence area > repo > project > Fugaro's own default is
// stated once, in the resolver below, and once more for a reader in
// docs/project-layer.md §2.
package rigor

import "slices"

// Mode is a rigor mode: one point on the axis of how much cost a run
// accepts to reduce risk.
type Mode string

// The five modes, as a configuration value spells them (D20). The four
// on-axis modes are ordered by Rank; Hotfix is off the axis.
const (
	// POC is the happy path only.
	POC Mode = "poc"
	// Startup is the happy path plus common error paths.
	Startup Mode = "startup"
	// Pro is established-company quality.
	Pro Mode = "pro"
	// Critical is maximum care about everything.
	Critical Mode = "critical"
	// Hotfix is production on fire: time is the dominant constraint.
	Hotfix Mode = "hotfix"
)

// Modes is the whole vocabulary, on-axis first and Hotfix last. It is
// closed: a value outside it is refused where it is written, never matched
// as an unknown mode at resolution time.
var Modes = []Mode{POC, Startup, Pro, Critical, Hotfix}

// Default is the mode a run resolves to when no layer and no request sets
// one: Pro (D19). It is therefore the behaviour of every repository that
// configures nothing, and `rigor.default: critical` is the one line that
// puts a repository back on the behaviour of the releases before this one.
//
// It is only the mode's *value*. Acting on it is a later slice's: until
// then a run resolves, records and prints its mode, and decides readiness
// exactly as it did before.
const Default = Pro

// Known reports whether m is one of Modes. The zero Mode (what a layer that
// sets nothing carries) is not.
func Known(m Mode) bool { return slices.Contains(Modes, m) }

// Parse is the mode a configuration value names; ok is false for a name
// outside the vocabulary. It is the only way a written value becomes a Mode,
// so a near miss ("Poc", "PRO", "critical ") is a load error rather than a
// rule that silently never fires.
func Parse(s string) (Mode, bool) {
	m := Mode(s)
	return m, Known(m)
}

// Rank orders the on-axis modes by how much they block: POC 1 < Startup 2 <
// Pro 3 < Critical 4.
//
// Hotfix ranks 0 — below every on-axis mode — because it is off the axis
// (spec.md): it is the mode that blocks the least, since only a finding that
// would make the fix wrong or unsafe blocks in it. Ranking it high would let
// a Hotfix rule lower a Critical one, which is the fail-open direction; this
// way any on-axis mode a run resolves beside a Hotfix one wins. The zero
// Mode ranks 0 too, and Strictest skips it rather than comparing it.
//
// A mode that is not in the vocabulary also ranks 0, so it never wins a
// comparison against a real one. Config refuses such a value at load, so
// this is the fail-closed reading of a value that reached here without
// having been parsed.
func (m Mode) Rank() int {
	switch m {
	case POC:
		return 1
	case Startup:
		return 2
	case Pro:
		return 3
	case Critical:
		return 4
	}
	return 0
}

// Strictest is the strictest of ms: the highest Rank wins, and a tie keeps
// the first, so Strictest is stable and commutative over the order it is
// given. This is the only-tightens rule the modes share with the floors
// (D6, D23) — a run that touches two areas resolves by the strictest of
// them, and a floor no layer may go under is the same comparison one step
// further out.
//
// The zero Mode contributes nothing — a layer that sets no mode is not a
// value to compare — and is never the answer; a call with no mode at all, or
// only zero Modes, returns the zero Mode. Applying Default is Resolve's, not
// this function's.
//
// A mode that is not in the vocabulary ranks 0, so it is as weak as Hotfix
// and never wins a comparison against a real mode; Resolve checks Known
// before it reads a layer or a request, so a caller of that entry point
// never sees one.
func Strictest(ms ...Mode) Mode {
	out := Mode("")
	for _, m := range ms {
		if m == "" {
			continue
		}
		if out == "" || m.Rank() > out.Rank() {
			out = m
		}
	}
	return out
}
