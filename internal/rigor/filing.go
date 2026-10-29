package rigor

// What each mode files of its own (docs/rigor-modes/implementation-plan.md
// T4.6, reconciliation.md D30): the per-mode table, held beside the resolver
// so the rule is one table rather than a condition inside the runner.
//
// It exists because the mode already decides which findings a run tolerates,
// so it is also what decides which of them become items (D30). The table is
// the whole answer: a rule that varied per mode beyond this one, or that
// routed a finding to the pull request body instead of to an item, would be a
// second record of one finding — the moment "one backlog and not two" breaks
// (D32).
//
// Nothing here gates an item a person asked for. `fugaro backlog add` writes
// what it is given and reads no mode; this table is read only where a run
// decides whether to file its own, which is why no mode can suppress an item
// the owner filed by hand.
//
// The table is read only for a repository that opted in (Config.Configured):
// an unconfigured repository resolves Default for its budget and files
// nothing, whatever Default's row says (the owner's ruling of 2026-10-24).

// Filing is what one mode files of its own: one row of D30's table.
type Filing struct {
	// Tolerated is whether the mode files the findings it tolerated, one item
	// per finding. It is Startup's, Pro's and Hotfix's row.
	Tolerated bool
	// Deferred is whether the mode files an item for what it deferred even
	// when it tolerated nothing. It is Hotfix's alone: the fix a hotfix ships
	// is the expedient one, so the proper one is owed whatever the review
	// round did (spec.md, D15).
	Deferred bool
}

// Any reports whether the mode files anything of its own.
func (f Filing) Any() bool { return f.Tolerated || f.Deferred }

// Files is D30's table: what m files of its own.
//
// POC and Critical file nothing — POC's account is its pull request body's
// line and the run record, and Critical tolerates nothing, so it has nothing
// to file (D7). Startup and Pro file one item per finding they tolerated; the
// two differ in their thresholds, not in how they file. Hotfix files one for
// what it deferred whatever else it left open, and its tolerated findings
// beside it.
//
// A mode that is not a row — the zero Mode, and a value outside the vocabulary
// — gets the default's row (D19). A run resolves Default before it reaches
// here, so this is the reading of a value that arrived unparsed, and it files
// rather than drops: a finding that disappears is the failure this feature
// risks (plan §7.5).
func Files(m Mode) Filing {
	switch m {
	case POC, Critical:
		return Filing{}
	case Startup, Pro:
		return Filing{Tolerated: true}
	case Hotfix:
		return Filing{Tolerated: true, Deferred: true}
	}
	return Files(Default)
}
