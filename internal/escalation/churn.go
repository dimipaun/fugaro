package escalation

import "github.com/dimipaun/fugaro/internal/rigor"

// The churn comparator (spec.md "Churn detection", E9): what the findings of an
// item's review rounds look like beside the rounds before them, read from the
// records alone and answered as one of Kind's three words.
//
// It is the second thing in this package that reads no store: the daemon pairs
// each round's findings with the keys its triage stage wrote and hands the
// rounds here (E10a), so the comparison is a pure function of what a caller can
// build in a test and the ladder's own answer to it (Decide) is testable
// without a repository.

// Round is one review round of an item's records as the comparator reads it:
// the rigor mode in force at it, and the findings its review raised, oldest
// round first.
//
// It is a round and not a run: a run's own loop reviews the same head several
// times, and D1246's rule is about the same finding looping across the record's
// Reviews, so a caller feeding one entry per run would compare the wrong pairs.
type Round struct {
	// Mode is the mode reached at this round (F14): the one its reviewer was
	// asked from, and the one its findings are read at. It is the round's own
	// and not the item's, because both the key and the answer to "does this
	// finding block" are the mode's — a mode raised mid-run is the mode the
	// later round was judged under.
	Mode rigor.Mode
	// Findings is what the round's review raised, in the reviewer's order. A
	// round that raised nothing is carried as it is rather than dropped,
	// because its emptiness is one of the facts the comparator reads.
	Findings []Finding
}

// Finding is one finding as the comparator reads it: the identity two rounds
// are matched by, and the score minor is read from.
//
// It is neither runner.Finding nor runstore.TriageEntry, deliberately: the
// comparator reads four fields and nothing else, and the key is the one the
// triage stage wrote into the record (D1850), so a caller that built this shape
// out of the record's entries could not smuggle in a field no rule here reads.
type Finding struct {
	// Key identifies the finding across rounds: the opaque key the triage entry
	// and the follow-up task carry (D1850, T-TR3). A finding without one is one
	// this code cannot compare, and a round holding one makes the whole answer
	// KindNone — the normal path, never a guess (F11).
	Key string
	// File is the path the reviewer named the finding in. It is the second way
	// two findings are held to be the same one, and it is the one that survives
	// a reworded summary: the key is a hash of the file and the summary, so a
	// paraphrase keeps the file and the class and loses the key (D1246, D2036).
	// A finding that names no file is matched by its key alone — a comparison
	// of one empty path with another is no evidence that two findings are one.
	File string
	// Score is the three anchored axes and the class, which are what the mode's
	// table reads (rigor.Score). Minor is Blocks' answer for it and never a
	// scale of this file's own; an unscored finding blocks in every mode, so an
	// unscored finding is never minor. Its class is the other half of the
	// file-and-class rule, and a class the vocabulary does not know — the zero
	// Class of a reviewer that named none (D3) — is no half of it: like a
	// finding with no file, one with no class is matched by its key alone.
	Score rigor.Score
}

// Churn compares the item's review rounds and answers whether its reviewer is
// churning: a reviewer raising new, minor findings every round instead of
// converging is the spec's case for rung 3 "whatever the clock says"
// (ReasonChurnNew), and the same unresolved finding repeating is rung 2's and
// then D1246's split (ReasonChurnSame, D2036).
//
// The rules, in the order they are read:
//
//   - Fewer than two rounds answer KindNone: one round is compared with
//     nothing, and an item reviewed once has not churned.
//
//   - A round in a mode whose triage stage fills no key answers KindNone.
//     D1851 runs the stage for Startup and Pro only, so POC, Critical and
//     Hotfix — and every mode outside the vocabulary — are off, which is the
//     spec's fallback to the normal path rather than an identity guessed from a
//     key that was never written (F11).
//
//   - A round holding a finding with no key answers KindNone for the same
//     reason: a record older than the key, or a finding the stage could not
//     place, is not one this code compares.
//
//   - "Same" is the latest round raising a finding an earlier round already
//     raised: the keys are equal, or the file and the class are. The second
//     half is D1246's rule and what keeps a reviewer's paraphrase of the finding
//     it keeps raising from reading as new work; it is asked of every earlier
//     round and not only of the one before, because the issue need not repeat
//     in consecutive rounds; and it needs a file and a class the vocabulary
//     knows on both findings, because a path or a class neither of them carries
//     is no evidence that they are one finding.
//
//     It is the latest round alone that is asked, because that is the round the
//     ladder is acting on: a repeat the history holds while the latest round
//     has moved past it is an issue the item left behind, and answering same
//     there would take an item to D2036's split on the tick after the rung 2
//     run that cleared the finding — ReasonR2Clean and ReasonR2Findings would
//     never be reached for an item the comparator had once called churning.
//
//   - Otherwise "new, minor" is every round — the first included — raising at
//     least one finding and none of them blocking at its own round's mode, with
//     every round after the first raising only findings no earlier round
//     raised. A round that raised nothing is a round the reviewer converged,
//     and a finding the mode blocks is a reviewer doing its job, so either one
//     makes the answer KindNone: the ladder's normal path, never an escalation
//     on a pattern this code did not see (G5). The first round is read at the
//     table like the rest, because "each round raises new, minor findings" is
//     the spec's sentence: a first round that raised a blocker is not a reviewer
//     churning, and answering new-minor there would merge at rung 3 an item
//     whose own first review left a finding the mode blocks.
//
// Same wins over new-minor, because a latest round raising both is a round where
// the unresolved finding is still there — the spec's "same unresolved issue" —
// and a comparison that answered new-minor there would take the item to rung 3
// while its rung 2 had not run (D2036).
func Churn(rounds []Round) Kind {
	if len(rounds) < 2 {
		return KindNone
	}
	for _, r := range rounds {
		if !churnKeyed(r.Mode) {
			return KindNone
		}
		for _, f := range r.Findings {
			if f.Key == "" {
				return KindNone
			}
		}
	}

	latest, history := rounds[len(rounds)-1], rounds[:len(rounds)-1]
	for _, f := range latest.Findings {
		if sameAsAny(history, f) {
			return KindSame
		}
	}

	newMinor := true
	for i, r := range rounds {
		if len(r.Findings) == 0 {
			newMinor = false
			continue
		}
		for _, f := range r.Findings {
			if i > 0 && sameAsAny(rounds[:i], f) {
				newMinor = false
			}
			if rigor.Blocks(r.Mode, f.Score) {
				newMinor = false
			}
		}
	}
	if newMinor {
		return KindNewMinor
	}
	return KindNone
}

// churnKeyed reports whether m is a mode whose triage stage fills a finding's
// key. It is Startup and Pro, the pair internal/triage has personas for
// (D1851), and it is stated here as its own rule rather than inferred from the
// keys a caller happened to hand over: a record whose keys are missing for
// another reason is a record this code cannot compare either, and the two
// readings agree on KindNone.
func churnKeyed(m rigor.Mode) bool { return m == rigor.Startup || m == rigor.Pro }

// sameAsAny reports whether f is a finding an earlier round already raised:
// the same key, or the same file and class (D1246's rule, D2036).
//
// Both halves of D1246's rule are asked only of two findings the reviewer gave
// the fact they match on. A path neither named is no evidence that they are one
// finding, and neither is a class neither stated: two findings the vocabulary
// cannot read are two findings, not one unresolved issue, and a match on their
// shared silence would spend rung 2 and D1246's split on a guess. The table
// answers those rounds on its own terms instead — an unscored finding blocks in
// every mode, so a comparison that reached them would have answered KindNone
// anyway.
//
// The class is the score's own, so the comparison never widens to the axes,
// which a re-review re-scores and which therefore say nothing about identity.
func sameAsAny(earlier []Round, f Finding) bool {
	for _, r := range earlier {
		for _, g := range r.Findings {
			if g.Key == f.Key {
				return true
			}
			if f.File != "" && g.File == f.File &&
				rigor.KnownClass(f.Score.Class) && g.Score.Class == f.Score.Class {
				return true
			}
		}
	}
	return false
}
