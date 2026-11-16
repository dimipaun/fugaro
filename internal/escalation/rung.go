// Package escalation is the outer loop's ladder as pure code: the rungs a
// stalled pull request can be walked through, the thresholds each rigor mode
// gives them, and the one function that answers which rung comes next
// (docs/outer-loop-escalation/spec.md, implementation plan E1).
//
// It decides and does nothing else. It reads no record, launches no run,
// writes no ledger entry, holds no credential and takes no clock: the daemon
// gathers the facts (internal/orchestrator, E10a) and acts on the answer. That
// is what makes a rung transition replayable — the same facts always answer
// the same step, so a decision can be tested without a cloud, a repository or
// a waiting period, and a daemon that crashed mid-rung can re-ask.
//
// Nothing in the daemon calls any of it yet (Slice A of the plan).
package escalation

import "slices"

// Rung is one step of the ladder, cheapest first: rebase, resume, one stronger
// pass, merge with follow-ups, split. It is "rung" and not "tier" because
// internal/tier already means trivial/small/medium/large (D2033, F5).
type Rung string

const (
	// R0 is the cheap first step: rebase onto the latest base, resolve the
	// conflicts, re-run the tests (spec.md "Tier 0"). It never merges
	// directly: the head its run pushes is reviewed first, because the rebase
	// run is an agent and can push anything (D2043, G13).
	R0 Rung = "R0"
	// R1 is today's behaviour: the item goes back to its normal recipe loop
	// (spec.md "Tier 1"). It is the ladder's floor and not an escalation.
	R1 Rung = "R1"
	// R2 is the one stronger single pass: the `escalate` recipe, launched at
	// most once per item, on a model grade above the item's own (spec.md
	// "Tier 2", E5).
	R2 Rung = "R2"
	// R3 is the merge with follow-ups, and it is a merge only under D1245's
	// deterministic gate: a rung 3 that the gate refuses waits for a person
	// (D2030, D2037, E10b).
	R3 Rung = "R3"
	// R4 is D1246's split: the branch is kept, the blocker the judge named
	// becomes the scope of smaller tasks, and the same spec is never
	// relaunched (spec.md "Tier 4").
	R4 Rung = "R4"
)

// Rungs is the whole vocabulary, cheapest first. It is closed: a value outside
// it is refused where it is written, never matched as an unknown rung later.
var Rungs = []Rung{R0, R1, R2, R3, R4}

// KnownRung reports whether r is one of Rungs. The zero Rung — an item that
// has entered none — is not.
func KnownRung(r Rung) bool { return slices.Contains(Rungs, r) }

// Rank orders the rungs by how far along the ladder they are: R0 0, R1 1, R2
// 2, R3 3, R4 4. A rung outside the vocabulary — the zero Rung an item that
// has entered none carries — ranks -1, so it is below every real one and a
// comparison ("is the item past rung 2?") never reads it as the far end.
func (r Rung) Rank() int {
	switch r {
	case R0:
		return 0
	case R1:
		return 1
	case R2:
		return 2
	case R3:
		return 3
	case R4:
		return 4
	}
	return -1
}

// MaxRebases is how many rung 0 runs one item may spend (D2042, G4). The rung
// is also spent once per head (Facts.RebasedHeads), so the count is what makes
// the rebase loop terminate: a rebase run pushes a new head, and without the
// cap an item could rebase forever.
const MaxRebases = 2

// Grade is a model's name in the ladder's own vocabulary: "strong" and
// "strongest", the two the project layer defines (D2039, G11). A rung names a
// grade and never a model or a credential — what a grade resolves to is the
// project layer's answer, read by the runner (E2, E4) — so a repository that
// runs the ladder has no way to name one from here.
type Grade string

const (
	// Strong is the grade a rung 2 run starts from: the spec's "project-level
	// strong model".
	Strong Grade = "strong"
	// Strongest is the grade a critical-path item, Critical and Hotfix run
	// rung 2 at (spec.md's table and "Urgency modifiers").
	Strongest Grade = "strongest"
)

// Grades is the whole vocabulary, in order. It is closed, the way Rungs is.
var Grades = []Grade{Strong, Strongest}

// KnownGrade reports whether g is one of Grades. The zero Grade — a threshold
// row that names no model — is not, and a rung 2 this code would launch at no
// grade refuses instead of picking one (D2039, G5).
func KnownGrade(g Grade) bool { return slices.Contains(Grades, g) }
