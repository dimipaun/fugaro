// Package rigor is the rigor level's vocabulary and its one resolver: how
// much cost a run accepts to reduce risk, and which of the layers that may
// set it won (docs/rigor-modes/spec.md, docs/rigor-modes/reconciliation.md
// D6, D19–D23).
//
// It is the only place that orders levels. No other package may compare two
// of them: the order — Fugaro's own default, the project layer's default, a
// project area, the project's entry for the repository, the repository's own
// default, one of its areas, and the run's own request — is stated once, in
// the resolver below, and once more for a reader in docs/project-layer.md §2.
package rigor

import "slices"

// Rigor is a rigor level: one point on the axis of how much cost a run
// accepts to reduce risk. The type is Rigor rather than Level because this
// package already spells the three review levels Level, Levels and
// LevelState (levels.go), and one word for two axes would be one word too
// few; the configuration key and everything a person reads say `level`.
type Rigor string

// The five levels, as a configuration value spells them (D20). The four
// on-axis levels are ordered by Rank; Hotfix is off the axis.
const (
	// POC is the happy path only.
	POC Rigor = "poc"
	// Startup is the happy path plus common error paths.
	Startup Rigor = "startup"
	// Pro is established-company quality.
	Pro Rigor = "pro"
	// Critical is maximum care about everything.
	Critical Rigor = "critical"
	// Hotfix is production on fire: time is the dominant constraint.
	Hotfix Rigor = "hotfix"
)

// Rigors is the whole vocabulary, on-axis first and Hotfix last. It is
// closed: a value outside it is refused where it is written, never matched
// as an unknown level at resolution time.
var Rigors = []Rigor{POC, Startup, Pro, Critical, Hotfix}

// Default is the level a run resolves to when no layer and no request sets
// one: Pro (D19). It is therefore the behaviour of every repository that
// configures nothing, and `rigor.default: critical` is the one line that
// puts a repository back on the behaviour of the releases before this one.
//
// It is only the level's *value*. Acting on it is a later slice's: until
// then a run resolves, records and prints its level, and decides readiness
// exactly as it did before.
const Default = Pro

// Known reports whether m is one of Rigors. The zero Rigor (what a layer that
// sets nothing carries) is not.
func Known(m Rigor) bool { return slices.Contains(Rigors, m) }

// Parse is the level a configuration value names; ok is false for a name
// outside the vocabulary. It is the only way a written value becomes a Rigor,
// so a near miss ("Poc", "PRO", "critical ") is a load error rather than a
// rule that silently never fires.
func Parse(s string) (Rigor, bool) {
	m := Rigor(s)
	return m, Known(m)
}

// Rank orders the on-axis levels by how much they block: POC 1 < Startup 2 <
// Pro 3 < Critical 4.
//
// Hotfix ranks 0 — below every on-axis level — because it is off the axis
// (spec.md): it is the level that blocks the least, since only a finding that
// would make the fix wrong or unsafe blocks in it. Ranking it high would let
// a Hotfix rule lower a Critical one, which is the fail-open direction; this
// way any on-axis level a run resolves beside a Hotfix one wins. The zero
// Rigor ranks 0 too, and Strictest skips it rather than comparing it.
//
// A level that is not in the vocabulary also ranks 0, so it never wins a
// comparison against a real one. Config refuses such a value at load, so
// this is the fail-closed reading of a value that reached here without
// having been parsed.
func (m Rigor) Rank() int {
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

// PerRun reports whether m is a level a run's own request takes a run to
// outright, rather than by outranking what the layers resolved.
//
// Hotfix is the one such level, and the owner's ruling of 2026-11-13 is why
// (docs/rigor-command/implementation-plan.md R7): it is off the axis and
// ranks below every on-axis level, so a rank comparison would always discard
// a request for it — while an operator who types `fugaro run --mode hotfix`
// is saying production is on fire, and there is no way to predict when that
// will be. The gate D12 named is dropped rather than built, and this method
// is that decision in one place: for every other level the request is the
// strongest rank of all, and Resolve settles the level whether it raises or
// lowers what the layers set — what holds a lowering back is a floor, which
// sits outside every layer (LF5), not a rank comparison.
func (m Rigor) PerRun() bool { return m == Hotfix }

// Strictest is the strictest of ms: the highest Rank wins, and a tie keeps
// the first, so Strictest is stable and commutative over the order it is
// given. This is the only-tightens rule the levels share with the floors
// (D6, D23) — a run that touches two areas resolves by the strictest of
// them, and a floor no layer may go under is the same comparison one step
// further out.
//
// It is not the rule a run's own request is held to for Hotfix, which
// PerRun takes whole: Strictest would drop it, as it drops any level whose
// rank is below the one it is compared against.
//
// The zero Rigor contributes nothing — a layer that sets no level is not a
// value to compare — and is never the answer; a call with no level at all, or
// only zero Rigors, returns the zero Rigor. Applying Default is Resolve's, not
// this function's.
//
// A level that is not in the vocabulary ranks 0, so it is as weak as Hotfix
// and never wins a comparison against a real level; Resolve checks Known
// before it reads a layer or a request, so a caller of that entry point
// never sees one.
func Strictest(ms ...Rigor) Rigor {
	out := Rigor("")
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
