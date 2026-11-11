// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package ab scores the A/B pairs a sampled launch produces, from the two
// runs' own records and nothing else (docs/model-ab-testing, D559, D561): no
// model call, no bucket listing, no figure a second read of the same records
// could answer differently (D563).
//
// It is the paid half of the work (D550): the sampler, the challenger
// admission and the pair launch sit beside it, and everything this package
// reads was written by the runner before it.
package ab

import (
	"cmp"
	"slices"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The two words a review round answers with (the runner's verdict schema). Only
// the first decides readiness here; the second is the vocabulary's other
// answer, and a round that answered anything else — "none" for a review whose
// call died or whose reply could not be read — is not a verdict about the
// change at all.
const (
	verdictShip    = "ship"
	verdictChanges = "changes"
)

// fixStage is the stage that fixes what a review asked for, as the runner
// dispatches it; it is one of D561's iterations.
const fixStage = "fix"

// Winner is which arm of a pair took it. A tie is a pair that decided
// nothing, never half a win for either arm: the report counts the three apart
// and A4.2's sign test reads only the decisive ones.
type Winner string

const (
	// WinChampion is the pair the arm the developer sees took (D554).
	WinChampion Winner = "champion"
	// WinChallenger is the pair the shadow took.
	WinChallenger Winner = "challenger"
	// WinTie is a pair whose arms are equal on every signal the mode reads.
	WinTie Winner = "tie"
)

// Pair is one sampled launch's two arms (R1): the run the developer sees and
// the shadow that publishes nothing (D554), joined by the pair ID both records
// carry in their ab block (A1.1).
//
// The records travel whole rather than as signals because a pair is read more
// than once and each reader needs something else of it: the report groups by
// the mode and tier the arms resolved, the cost ratio A4.2 medians is their
// own recorded dollars, and the patch objects the judge is handed are theirs
// (A5.1, D559).
type Pair struct {
	// ID is the pair both arms' records carry (task.AB.PairID).
	ID string
	// Champion is the arm that reaches the developer (D554).
	Champion runstore.Record
	// Challenger is the shadow arm.
	Challenger runstore.Record
	// ChampionFollowUps is how many runs continue the champion's pull
	// request (D562). The champion is the first run of its pull request and
	// every follow-up names the run it continues, so the chain is walked
	// rather than its direct successors counted: the second follow-up of a
	// pull request names the first, not the run that opened it. It is context
	// and never a signal — the shadow opens no pull request and so can have
	// no follow-up at all, which is why rework cannot enter a paired
	// comparison (F12) — and Score reads none of it.
	ChampionFollowUps int
}

// Pairs groups records into the valid pairs among them, in pair-ID order.
//
// The order is part of the contract and not an accident of the input: the same
// records must give the same report (D563), and a bucket listing hands its
// records in whatever order it likes. The arms are told apart by the arm their
// ab block names, never by the order they arrived in.
//
// A pair R2 makes invalid is left out rather than marked, because every figure
// the report draws is drawn from the pairs this returns and leaving one out is
// what "excluded from every figure" means. A pair is invalid when the two
// arms' resolved rigor mode, tier, recipe or StartSHA differ, and when either
// arm never started — its record carries no start commit, which is an arm
// whose job died before bootstrap wrote one, and an arm that never ran is one
// whose other half has nothing to be compared with.
//
// A record that carries no ab block is no arm of a sampled launch at all (a
// first run of an ineligible task, a follow-up, a review run; D558), and
// neither is one whose arm is outside the closed vocabulary A1.1 refuses at
// the task: the record is an object the job's service account can write, so
// the block is read as a claim, and a claim of an arm nothing launches takes
// the record out of every pair rather than into one.
func Pairs(records []runstore.Record) []Pair {
	byID := make(map[string][]runstore.Record)
	for _, rec := range records {
		if !isArm(rec) {
			continue
		}
		byID[rec.AB.PairID] = append(byID[rec.AB.PairID], rec)
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	edges := followEdges(records)
	out := make([]Pair, 0, len(ids))
	for _, id := range ids {
		arms := byID[id]
		slices.SortFunc(arms, func(a, b runstore.Record) int { return cmp.Compare(a.RunID, b.RunID) })
		var p Pair
		p.ID = id
		champions, challengers := 0, 0
		for _, rec := range arms {
			switch rec.AB.Arm {
			case task.ABArmChampion:
				champions++
				p.Champion = rec
			case task.ABArmChallenger:
				challengers++
				p.Challenger = rec
			}
		}
		// Two records of one arm under one pair ID is not a launch this
		// launcher can make; scoring whichever came first would make a figure
		// depend on the order the records were read in.
		if champions != 1 || challengers != 1 {
			continue
		}
		if !sameArmInputs(p.Champion, p.Challenger) {
			continue
		}
		p.ChampionFollowUps = championFollowUps(p.Champion.RunID, edges)
		out = append(out, p)
	}
	return out
}

// isArm reports whether the record is one arm of a sampled launch.
func isArm(rec runstore.Record) bool {
	if rec.AB == nil || rec.AB.PairID == "" {
		return false
	}
	return rec.AB.Arm == task.ABArmChampion || rec.AB.Arm == task.ABArmChallenger
}

// sameArmInputs is R2 asked of the two arms: the four facts a pair's fairness
// rests on, plus the start commit that says both runs began.
//
// Mode, tier and recipe are compared as the records state them, absent and
// all: two arms that both carry no tier are one pair in the untiered group
// D564 names, while an arm that resolved a mode and one that resolved none are
// not a pair at all. The recipe is compared by its name and by the hash of the
// bytes it came from, so two arms that ran different recipes of one name — a
// recipe edited between the two launches — are not one pair either.
func sameArmInputs(champion, challenger runstore.Record) bool {
	if champion.StartSHA == "" || champion.StartSHA != challenger.StartSHA {
		return false
	}
	return recordMode(champion) == recordMode(challenger) &&
		recordTier(champion) == recordTier(challenger) &&
		recordRecipe(champion) == recordRecipe(challenger)
}

// recordMode is the mode the record resolved, "" for one that recorded none —
// a run that died before bootstrap. The block is read as the arm's own account
// of its mode: Bounded refuses a block whose layer or moment is unreadable,
// which is a question about the block's honesty, while R2 asks only whether
// the two arms agree.
func recordMode(rec runstore.Record) rigor.Mode {
	if rec.Rigor == nil {
		return ""
	}
	return rec.Rigor.Mode
}

// recordTier is the tier the record carries, "" for one that carried none:
// most runs carry no tier (F17), and the untiered group is the one D564
// groups them in rather than a pair nobody may score.
func recordTier(rec runstore.Record) tier.Tier {
	if rec.Tier == nil {
		return ""
	}
	return rec.Tier.Tier
}

// recordRecipe is the run's recipe as one comparable string: its name and the
// hash of the bytes it came from, with a separator neither can carry — a name
// is letters, digits and dashes, a hash is hex — so two recipes cannot spell
// one string.
func recordRecipe(rec runstore.Record) string {
	if rec.Recipe == nil {
		return ""
	}
	return rec.Recipe.Name + "\x00" + rec.Recipe.SHA256
}

// followEdges maps each run to the runs that continue it (FollowUp.PreviousRun
// read backwards), the whole set read once so that counting each pair's
// follow-ups stays one pass over the records however many pairs there are.
func followEdges(records []runstore.Record) map[string][]string {
	edges := make(map[string][]string, len(records))
	for _, rec := range records {
		if rec.FollowUp == nil || rec.RunID == "" {
			continue
		}
		previous := rec.FollowUp.PreviousRun
		edges[previous] = append(edges[previous], rec.RunID)
	}
	return edges
}

// championFollowUps counts the runs that continue the champion's pull request
// (D562). The champion is the first run of its pull request and every
// follow-up names the run it continues, so the chain is walked from the
// champion's own ID: the second follow-up of a pull request names the first and
// not the run that opened it. A run already counted is never walked again, so a
// chain that loops back — or a set of records no launcher wrote — ends the walk
// rather than repeating it.
func championFollowUps(runID string, edges map[string][]string) int {
	onPR := map[string]bool{runID: true}
	count := 0
	for queue := []string{runID}; len(queue) > 0; {
		next := queue[0]
		queue = queue[1:]
		for _, run := range edges[next] {
			if onPR[run] {
				continue
			}
			onPR[run] = true
			count++
			queue = append(queue, run)
		}
	}
	return count
}

// Signals is one arm's hard signals as D561 reads them: facts about the run,
// in the order the decision ranks them, every one of them read from the arm's
// own record. None of them is a judgement about the change — that is what the
// judge A5 adds — and none of them needs a model call, which is what lets v1
// answer "cheaper and not worse on tests, review and halts" without one
// (D551, D552).
type Signals struct {
	// Halted is a run a limit stopped (Record.Halt), the per-run cap both
	// arms share included. A halt is a loss, and the halt rate is reported
	// per arm, so the arm that halts more is the arm that does not work
	// within what a run is allowed to cost (D567, F16).
	Halted bool
	// FinalHead is the commit the arm ended at (Record.HeadSHA), "" for an
	// arm that reached none — a run that died before its first commit. D561
	// puts it beside the halt: an arm with no head has nothing to compare.
	FinalHead string
	// Verified is build, test and lint passing at FinalHead, read the way the
	// runner reads it before it publishes a commit (a repository's type check
	// runs inside its build or lint command): of each kind's runs on a clean
	// tree at that head, the last one passed, and a test is among them — a run
	// whose tree was dirty is no evidence about the commit.
	Verified bool
	// ReviewReady is that round shipping (decidingRound). A record whose
	// deciding level left no round, or whose last one answered nothing
	// parseable, is not ready: a review nobody held, and a round nobody could
	// read, are no approval (D4's rule).
	ReviewReady bool
	// Untolerated is the findings of that round the record does not account
	// for as let through: the round's own count less the findings the mode
	// tolerated, the stop's acceptance of a run that merged at its stop
	// included (untoleratedFindings). It is D561's fourth signal, and it is
	// what ranks a run that ended on findings it would not fix below one that
	// ended on fewer.
	Untolerated int
	// Iterations is D561's last signal: the review rounds the record holds,
	// the fix stages it ran and the cheap loops its reviews followed — the
	// same work counted once, so two arms can be told apart on how much of
	// the run the change cost rather than on what it ended with.
	Iterations int
}

// Result is one pair scored at one mode: the arm that took it and both arms'
// signals, so a report can show why. D561 is a ranking and not a number, so
// the signals are what a reader checks the ranking against.
type Result struct {
	// PairID is the pair this result belongs to (Pair.ID).
	PairID string
	// Mode is the mode the signals were read under: the pair's own mode, R2
	// having refused a pair whose arms resolved different ones, and the mode
	// the report groups by.
	Mode rigor.Mode
	// Winner is the arm that took the pair on the signals this mode reads.
	Winner Winner
	// Champion and Challenger are the two arms' signals, whether or not the
	// arm won: the halt rate is counted per arm, and a reader asking why the
	// shadow lost reads them here.
	Champion   Signals
	Challenger Signals
}

// Score reads one pair at one mode (D561).
//
// The signals are ranked, never weighted: the mode's first signal decides
// unless the arms are equal on it, and so on down the list — POC on the halt
// and the tests, Startup adding the review verdict, Pro adding the findings
// the mode does not tolerate, Critical adding the iterations. A mode outside
// the four — Hotfix, which D558 never samples, and every name the vocabulary
// does not hold — is read through the whole list, the strictest reading, so a
// pair nobody should have launched is decided on everything rather than on the
// least.
//
// The pair's validity is not re-asked here: Pairs is where R2 is read, and a
// Pair that reached this function is one it made. The kept arm's follow-up
// count is context and decides nothing (D562), as is everything else the
// records hold; there is no weight to argue about and no model to call (D561).
func Score(p Pair, mode rigor.Mode) Result {
	res := Result{
		PairID:     p.ID,
		Mode:       mode,
		Champion:   armSignals(p.Champion, mode),
		Challenger: armSignals(p.Challenger, mode),
	}
	switch compareVectors(signalVector(res.Champion, mode), signalVector(res.Challenger, mode)) {
	case -1:
		res.Winner = WinChampion
	case 1:
		res.Winner = WinChallenger
	default:
		res.Winner = WinTie
	}
	return res
}

// armSignals reads one arm's record into D561's signals. Nothing is inferred
// from the other arm, and no field of the record is asked twice.
func armSignals(rec runstore.Record, mode rigor.Mode) Signals {
	s := Signals{
		Halted:    rec.Halt != nil,
		FinalHead: rec.HeadSHA,
	}
	s.Verified = verifiedAtHead(rec.Verify, s.FinalHead)
	if round := decidingRound(rec, mode); round != nil {
		s.ReviewReady = round.Verdict == verdictShip
		s.Untolerated = untoleratedFindings(rec, *round)
	}
	s.Iterations = len(rec.Reviews) + fixStages(rec.Stages) + bounceCount(rec.Reviews)
	return s
}

// verifiedAtHead reports D561's second signal for one head.
//
// Only a run on a clean tree is evidence about the commit, and a kind is read
// by its last such run at that head: that is the runner's own reading of a
// commit before it publishes one (latestVerifiedTest, which takes the last test
// on the head with a clean tree, and Decide, which requires it to have passed),
// and a test that failed and then passed on a `--rerun-failed` is the flaky
// case this repository already reads that way. A run whose tree was dirty says
// nothing about the head — including one that failed — so it is skipped rather
// than taken as the last word; while a build that failed on a clean tree and
// was never run again is the failure it is.
//
// A head with no test on a clean tree is not verified, whatever its build and
// lint runs say: the runner publishes a commit only with a verified test
// behind it, so an arm without one ended a draft in both editions; a
// repository whose test command runs nothing is a repository whose pairs tie
// there rather than a point either arm takes.
func verifiedAtHead(records []verify.Record, head string) bool {
	if head == "" {
		return false
	}
	last := make(map[verify.Kind]verify.Record, len(records))
	for _, r := range records {
		if r.HeadSHA == head && r.CleanTree {
			last[r.Kind] = r
		}
	}
	test, ok := last[verify.KindTest]
	if !ok || !test.Passed {
		return false
	}
	for _, r := range last {
		if !r.Passed {
			return false
		}
	}
	return true
}

// decidingRound is the review round an arm's readiness is read from: the last
// round of the level the run decided on, whatever it answered. Readiness is
// never read backwards to a lower level — the level above leaving no round at
// all is a run whose decider never read it, which the runner keeps a draft for,
// so the arm is reported not ready rather than stood on the round below it —
// and a round that answered nothing parseable ("none", after the retry the loop
// gives it) is a deciding round like any other, so readiness read backwards
// from it to an earlier verdict about a commit the run did not ship would call
// a draft ready.
//
// The level is what the runner decides on (decidingReview, D723) as far as the
// record allows: decidingReview walks level 3, 2, 1 against the run's *resolved*
// levels, and the record carries no resolved levels (the result schema has no
// such property, and neither the policy record nor anything else on it holds
// them), so deciderRank reads the highest of the three levels the record does
// answer for. That is close and not equal, and the two differences are worth
// naming rather than the parity being claimed:
//
//   - A level a layer turned on — the task's review_levels, a repository or
//     project layer, the owner's floor (D722) — is read from the rounds the run
//     wrote: a round is written by the level that ran it, so a round above the
//     mode's own default is level that ran and decided. A level like that which
//     left no round at all is invisible here, and an arm it would have refused
//     is read at the level below it: this is the one direction that can call a
//     draft ready.
//   - A stop that cut an enabled level above the one that ran (D723's exception
//     for a mode that accepts at its stop) is read as a level the run never
//     held, and the arm is reported not ready where the runner published it. A
//     record holds nothing that tells the two apart.
//
// Both need a field the record does not have. Until the format carries the
// resolved levels, this is the reading a record can support, and it errs
// towards the draft.
func decidingRound(rec runstore.Record, mode rigor.Mode) *runstore.ReviewSummary {
	decider := deciderRank(rec, mode)
	for i := len(rec.Reviews) - 1; i >= 0; i-- {
		if reviewRank(rec.Reviews[i].Tier) == decider {
			return &rec.Reviews[i]
		}
	}
	return nil
}

// The review levels as the record's rounds answer for them, low to high. Level
// three's rank is also what a mode outside the vocabulary is read at.
const (
	rankFirstLine = 1
	rankSenior    = 2
	rankAdversary = 3
)

// reviewRank is the level a round ran at, in the record's own vocabulary: an
// empty tier is the senior review of a run that had no first line. A tier no
// level writes — the independent review's display-only round — is 0, which no
// decider is, so it is never the round readiness is read from.
func reviewRank(tier string) int {
	switch tier {
	case runstore.TierFirst:
		return rankFirstLine
	case "", runstore.TierSenior:
		return rankSenior
	case runstore.TierAdversarial:
		return rankAdversary
	}
	return 0
}

// deciderRank is the level an arm's readiness is decided on: the highest of the
// mode's own default (deciderDefault), the highest level a round in the record
// shows ran (a level above the mode's default is one a layer turned on, and the
// round it wrote is the round it decided on), and level 3 where the security
// escalation took effect.
func deciderRank(rec runstore.Record, mode rigor.Mode) int {
	decider := deciderDefault(mode)
	for _, round := range rec.Reviews {
		if rank := reviewRank(round.Tier); rank > decider {
			decider = rank
		}
	}
	if escalated(rec) {
		decider = rankAdversary
	}
	return decider
}

// escalated reports whether the record shows the security escalation taking
// effect: the run's diff matched the sensitivity list, so review level 3 was
// turned on for the run while the rigor mode deliberately stayed where it was
// (escalateSecurity, D1508/D1509). A refused escalation (Refused) changed
// nothing — the levels stayed where they were and level 3 never ran — so it is
// not an escalation anything is decided on.
func escalated(rec runstore.Record) bool {
	return rec.Security != nil && !rec.Security.Refused
}

// deciderDefault is the highest review level the mode's own defaults turn on
// (rigor.DefaultLevels), before any layer or escalation moves it. POC runs
// level 1 alone, so its first-line round decides; Startup, Pro and Hotfix run
// level 2; Critical runs level 2 and level 3, so its adversarial round is the
// one that decides and an arm holding none is not ready.
//
// A mode outside the vocabulary is read as the strictest set — level 3's —
// which is the reading signalVector gives it too.
func deciderDefault(mode rigor.Mode) int {
	levels, err := rigor.DefaultLevels(mode)
	if err != nil {
		return rankAdversary
	}
	switch {
	case levels.L3 == rigor.LevelOn:
		return rankAdversary
	case levels.L2 == rigor.LevelOn:
		return rankSenior
	default:
		return rankFirstLine
	}
}

// untoleratedFindings is D561's fourth signal: what the deciding round left
// that the record does not account for as let through.
//
// Tolerated is the whole of that account. The runner writes the entries the
// mode let through there, and a run that stopped at its stop writes the same
// entries again into Accepted beside the round they were left by and the half
// of the stop rule that ended the run (toleratedRecord, acceptedRecord, both
// from the same policy and the same round): Accepted never holds a finding
// Tolerated does not, so reading it as well could only count one twice. A
// record carrying an Accepted block that outruns its Tolerated one — which
// nothing here writes, and which the job's service account could — is read at
// Tolerated's smaller figure, so it is reported worse than its own block
// claims: a block like that cannot make a round look cleaner than the count the
// mode kept.
func untoleratedFindings(rec runstore.Record, round runstore.ReviewSummary) int {
	let := 0
	if rec.Tolerated != nil {
		let = rec.Tolerated.Total
	}
	return max(0, round.Findings-let)
}

// fixStages counts the stages that fixed what a review asked for.
func fixStages(stages []runstore.StageTiming) int {
	n := 0
	for _, s := range stages {
		if s.Name == fixStage {
			n++
		}
	}
	return n
}

// bounceCount counts the cheap loops the record's reviews followed: a round
// whose Bounce is above zero is a first-line re-read bought after a rejection,
// and D561 counts it as an iteration.
func bounceCount(records []runstore.ReviewSummary) int {
	n := 0
	for _, r := range records {
		if r.Bounce > 0 {
			n++
		}
	}
	return n
}

// signalVector is the arm's signals as the mode's ranked list, most
// significant first, every entry ordered so that the lower one wins. It is
// what makes the comparison lexicographic without a weight: a difference on
// the first entry ends the question, and two arms equal on every entry are a
// tie (D561).
func signalVector(s Signals, mode rigor.Mode) []int {
	v := []int{signalFlag(s.Halted || s.FinalHead == ""), signalFlag(!s.Verified)}
	switch mode {
	case rigor.POC:
		return v
	case rigor.Startup:
		return append(v, signalFlag(!s.ReviewReady))
	case rigor.Pro:
		return append(v, signalFlag(!s.ReviewReady), s.Untolerated)
	default:
		return append(v, signalFlag(!s.ReviewReady), s.Untolerated, s.Iterations)
	}
}

// signalFlag is a signal a mode reads as true or false, as the value the vector
// orders.
func signalFlag(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compareVectors orders two signal vectors: -1 for a, 1 for b, 0 for a tie.
func compareVectors(a, b []int) int {
	for i := range a {
		if a[i] != b[i] {
			return cmp.Compare(a[i], b[i])
		}
	}
	return 0
}
