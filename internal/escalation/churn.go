package escalation

import "github.com/dimipaun/fugaro/internal/rigor"

// The churn comparator (spec.md "Churn detection", F11, D2036): it reads the
// findings of an item's review rounds and answers what the rounds look like
// beside each other. Decide reads that answer and never re-derives it
// (Facts.Churn), so "the reviewer is churning" is stated once, here, and the
// decision stays a pure function of a value a test can build.
//
// It is off in POC, Critical and Hotfix, and the reason is a fact about the
// records rather than a preference: a finding has no identity of its own, and
// the one it has — the opaque key the triage stage writes — is filled in
// Startup and Pro only (F11, D1850). Comparing unkeyed findings would be a
// guess about which of them are the same, and both of this comparator's
// answers escalate (KindSame to rung 2 and then rung 4, KindNewMinor straight
// to rung 3), so a guess here is a guess that spends a run. A mode it does not
// read, and a round it cannot place, answer KindNone — rung 1, today's
// behaviour, the normal path (G5).

// Finding is one finding as the comparator reads it: the identity the triage
// stage wrote, the file and class D1246's fallback compares, and the score the
// mode's own table reads to answer "minor".
//
// It is this package's own type rather than runner.Finding or runstore's
// entry, because internal/escalation reads no record (its package doc): the
// daemon gathers the item's rounds from its own run records (E10a) and
// presents them here, which is what keeps Churn testable without a store, a
// repository or a model.
type Finding struct {
	// Key identifies the finding across rounds: the opaque key the triage
	// stage writes for it (runstore.TriageEntry.Key, T-TR4), the same key the
	// follow-up task carries. It is empty for a finding no triage stage
	// placed, and a round holding one is a round Churn refuses to read — the
	// key is the identity, so a comparison made without it would be the guess
	// above.
	Key string
	// File is the path the reviewer put the finding in. It is the first half
	// of D1246's fallback identity, which is what catches a finding reworded
	// between rounds: the key is a hash of the file and the summary, so a
	// reworded finding keeps its file and its class and loses its key.
	File string
	// Score is the finding's class and three axes — the input rigor.Blocks
	// reads. "Minor" is therefore the mode's own answer for the finding and
	// never a second scale this package invents (F11).
	Score rigor.Score
}

// Round is one review round's findings, in the reviewer's own order: the
// item's first review is rounds[0], and Churn reads each round against the
// ones before it. An empty round is kept as one — a round the reviewer raised
// nothing in is a fact about the item, and not the same thing as a round that
// was never recorded.
type Round []Finding

// Churn answers what the item's review rounds look like compared with each
// other (spec.md "Churn detection", D2036). Every rule below is one the spec
// states, read in the order it is written:
//
//   - A round this code cannot place ends the comparison before it starts, and
//     the item keeps the normal path. A mode the key is not filled in, fewer
//     than two rounds to compare, a round with no findings in it, and a
//     finding with no key are all that shape: the spec's rule is about what
//     each round raises, so a round that raised nothing is not a round that
//     raised something new, and a finding with no identity is one this code
//     cannot place in any round.
//   - The same unresolved issue repeating is KindSame, and it wins over
//     everything else: a round that repeats one finding and raises three new
//     ones is the reviewer coming back to the same issue, which is what
//     D2036 sends to rung 2 once and to D1246's split if it repeats again.
//     "The same" is a match on the key, or on the file and the class — the
//     latter is D1246's own rule, and it is what keeps a reworded finding from
//     reading as a new one (F11).
//   - Otherwise, every round after the first has to be new minor findings for
//     the answer to be KindNewMinor: every finding in it new — absent from
//     every round before it — and every one of them minor, which is
//     rigor.Blocks' answer for the mode and not a second scale. A new finding
//     the mode blocks on is the reviewer doing its job, so it answers
//     KindNone: the spec's rule is about the reviewer raising new *minor*
//     findings instead of converging, not about it raising anything at all.
//
// A comparison the spec's rule does not reach is KindNone, which is rung 1 —
// the item's own recipe loop, today's behaviour. That is the one answer here
// that spends nothing, so it is the answer for everything this code cannot
// place.
func Churn(mode rigor.Mode, rounds []Round) Kind {
	if !churnOn(mode) || len(rounds) < 2 {
		return KindNone
	}
	for _, round := range rounds {
		if len(round) == 0 {
			return KindNone
		}
		for _, f := range round {
			if f.Key == "" {
				return KindNone
			}
		}
	}

	churning := true
	for i := 1; i < len(rounds); i++ {
		for _, f := range rounds[i] {
			if repeated(f, rounds[:i]) {
				return KindSame
			}
			if rigor.Blocks(mode, f.Score) {
				churning = false
			}
		}
	}
	if churning {
		return KindNewMinor
	}
	return KindNone
}

// churnOn reports whether mode is one the comparator reads at all. The key is
// the identity every comparison here is made by and the triage stage writes it
// in Startup and Pro only (F11, D1850), so those two are the modes whose
// records can answer the question. Anything else — the other three modes, and
// a value outside the vocabulary — is off, which is the same answer for the
// same reason and not a separate rule.
func churnOn(mode rigor.Mode) bool { return mode == rigor.Startup || mode == rigor.Pro }

// repeated reports whether a finding is one an earlier round already raised.
func repeated(f Finding, earlier []Round) bool {
	for _, round := range earlier {
		for _, g := range round {
			if sameFinding(f, g) {
				return true
			}
		}
	}
	return false
}

// sameFinding reports whether two findings are the same one raised twice: the
// key matches, or the file and the class do (D1246's rule, F11).
//
// The file has to be a real path for the fallback to apply, because a finding
// the reviewer named no file for sits nowhere, and two of them would otherwise
// read as the same finding in the same nowhere — the fail-open direction, on
// the half of this predicate that has no key to check. The class has to be one
// of rigor's for the same reason: an unscored finding carries no class, so
// "the same file and class" is not something two of them can be.
//
// The keys compared here are never both empty — Churn refuses a round holding
// one — so a key match is a real identity and not two absences agreeing.
func sameFinding(a, b Finding) bool {
	if a.Key == b.Key {
		return true
	}
	if a.File == "" || a.File != b.File {
		return false
	}
	return rigor.KnownClass(a.Score.Class) && a.Score.Class == b.Score.Class
}
