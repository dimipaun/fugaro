package rigor

// What each level hands back into the account it publishes
// (docs/rigor-modes/levels-implementation-plan.md LV4.3, levels-spec.md §2,
// levels-reconciliation.md LD5 and LD7): the per-level table, held beside the
// resolver so the rule is one table rather than a condition inside the runner.
//
// It exists because the level already decides what its coder is told to write
// down (LV2.1's persona says it in the agent's own words) and because the
// persona's word is the agent's promise, not the runner's record: this table
// is what the runner asks when it decides which of the sections the agent
// handed back it carries into the body a person reads. A level's row is
// therefore the *whole* of the rule about which sections its account shows —
// a level that grew a second, differently-worded rule in the runner would be
// two answers to one question, which is what the table is here to prevent.
//
// It is deliberately not D30's filing table (filing.go beside it): a run files
// no backlog item any more (the owner's rulings of 2026-11-01 and, later,
// D2716 of docs/orchestrator-graph-estimates, which retires the Markdown
// backlog folder), so what a level *handed back* is carried in the pull
// request's own account instead. The two tables are about two verbs and are
// not rows of one another: `Files` still answers the one question a run asks
// of it — whether the level may tolerate a finding at all (D9) — and this one
// answers what the level owes the reader.
//
// The row is read only where a level resolved: a run whose record carries no
// level block has no account to write, and HandBackOf answers the default's
// row for a value that arrived unparsed, as Files does (D19).

// HandBack is what one level hands back into the account it publishes: one
// row of LD5's and LD7's per-level rule.
//
// Each field names one of the sections the agent's hand-back file may carry
// (internal/runner's handback.go is the parser), and the level's own standing
// line where the level owes one the agent cannot write.
type HandBack struct {
	// Prototype is POC's own line, "this is a prototype; do not ship it".
	// It is the runner's to write rather than the agent's: the level's whole
	// account is that its work is not for production, and a line that
	// depended on the agent remembering to write it would be missing exactly
	// when it matters most. It is POC's alone.
	Prototype bool
	// Deferred is the agent's `## Deferred` list: what it consciously
	// deferred, and why. Startup's and Pro's row.
	Deferred bool
	// Verification is the agent's `## Verification`: the reasoning that shows
	// the change is verified. Critical's row.
	Verification bool
	// OpenRisks is the agent's `## Open risks`: what it could not fully
	// cover, flagged rather than deferred quietly. Critical's row, and the
	// one thing LD5 gives Critical of its own (LF9).
	OpenRisks bool
	// RevertPlan is the agent's `## Revert plan`: how to undo the change.
	// Hotfix's row (LD7).
	RevertPlan bool
	// MorningNote is the agent's `## Morning note`: what broke, what was
	// done, what needs a proper fix, and how the service is verified
	// restored. Hotfix's row (LD7).
	MorningNote bool
}

// Any reports whether the level hands back anything of its own.
func (h HandBack) Any() bool {
	return h.Prototype || h.Deferred || h.Verification || h.OpenRisks || h.RevertPlan || h.MorningNote
}

// HandBackOf is the table: what the level m hands back.
//
// POC hands back its prototype line and nothing else — it keeps no deferred
// list, because it does not know what it skipped. Startup and Pro hand back
// the agent's deferred list; the two differ in their thresholds, not in what
// they owe the reader. Critical hands back its verification and its open
// risks: D7's Critical tolerates nothing, so what it owes is not a list of
// what it let through but the proof it did the work and the risks it could
// not close. Hotfix hands back the revert plan and the morning note — the two
// things the person who finds the run the next morning needs (LD7).
//
// A level that is not a row — the zero Rigor, and a value outside the
// vocabulary — gets the default's row (D19), as Files does: the answer is the
// agent's deferred list, which is the widest of the five, so a value that
// arrived unparsed shows the reader more of what the agent wrote rather than
// less.
func HandBackOf(m Rigor) HandBack {
	switch m {
	case POC:
		return HandBack{Prototype: true}
	case Startup, Pro:
		return HandBack{Deferred: true}
	case Critical:
		return HandBack{Verification: true, OpenRisks: true}
	case Hotfix:
		return HandBack{RevertPlan: true, MorningNote: true}
	}
	return HandBackOf(Default)
}
