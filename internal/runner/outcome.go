package runner

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// latestVerifiedTest returns the last test record on sha with a clean tree.
func latestVerifiedTest(records []verify.Record, sha string) *verify.Record {
	var latest *verify.Record
	for i := range records {
		r := &records[i]
		if r.Kind == verify.KindTest && r.HeadSHA == sha && r.CleanTree {
			latest = r
		}
	}
	return latest
}

// ReasonNoVerifiedTest is Decide's reason when no clean-tree test record
// exists for the final commit.
const ReasonNoVerifiedTest = "no verified test run on the final commit"

// Decide applies the PR outcome rule (design §4.2): ready only if the latest
// test record on finalSHA with a clean tree passed and the last review either
// shipped or left nothing the run's mode blocks on (T3.1, D7). A clean or
// fixed check gate (gate.go) never reaches Decide: a clean gate ends the run
// as succeeded with outcome none before any PR exists, and a fixed one goes on
// to review as usual.
//
// pol is the mode's findings policy, and the one answer it can change is a
// `changes` verdict whose every finding it tolerates (D9). Every other answer
// is the one this function gave before modes existed, reason string included:
// the reasons above the tolerance are the gates, and a mode cannot make an
// unverified or unreviewed run ready.
//
// A mode raised after the review round the verdict came from is the one
// exception to that tolerance (T3.5): a round the raised mode tolerates is
// still a draft when no review was held at the mode the run ends at, and the
// reason names the raise — the mode, the rule that raised it and the moment
// (D22's third moment is the case a run cannot buy rounds with).
//
// owner is what the run's review left for the repository's owner to settle
// (OD1, runstore.OwnerDecisions), nil for a run that left none. It is checked
// above the mode's tolerance and above every review verdict: a finding whose
// only remedies are the owner's is not a finding a mode may let through, and
// the round it came from is not one the run was read by — the run is waiting
// for a person, which no configuration can answer.
//
// The class is returned beside the reason and never instead of it: it is why
// the run did not become ready, as one of the vocabulary's values, and this is
// the site that knows which refusal the run carries — nothing downstream reads
// the sentence back to work one out (design §4.2). A run Decide makes ready
// carries no class, the way a succeeded record carries none.
func Decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary, pol FindingsPolicy, owner *runstore.OwnerDecisions) (bool, string, failure.Class) {
	latest := latestVerifiedTest(records, finalSHA)
	switch {
	case latest == nil:
		return false, ReasonNoVerifiedTest, failure.ClassForVerify(verify.Record{}, finalSHA)
	case !latest.Passed:
		return false, "tests failing on the final commit", failure.ClassForVerify(*latest, finalSHA)
	case pendingOwner(owner) > 0:
		// Above the verdicts, and above the tolerance below: the round that
		// left these did not fail, it asked a question the run cannot answer,
		// and the pull request stays a draft until someone does (OD1). The
		// tests are read first because a failing suite is a thing the run can
		// still act on; nothing here can act on an owner's decision.
		//
		// It carries no class of its own: the run is waiting, not stopped for a
		// cause the vocabulary holds — a person is what comes next, and the
		// design's §3 has no row for it, which is what unclassified says. A
		// halt on the same decisions reads its class off the halt's own reason
		// instead (haltClass), where no family holds it either.
		return false, ownerWaitingReason(pendingOwner(owner)), failure.Unclassified
	case last == nil:
		return false, "no review verdict", failure.ReviewUnread
	case last.Verdict == "none":
		// A review round that produced no parseable verdict, after the retry
		// the loop gives it (seniorLoop), is not a tolerance of findings: it
		// asked for nothing, so nothing is fabricated into a blocker, no fix
		// round is spent on it and the mode has no say in it. It is not a
		// review either, and a run nobody read is not one this rule marks
		// ready — the round's answer comes from a diff the run's own coder
		// wrote, and a coder that can make the reviewer answer nothing twice
		// would otherwise have its work shipped unread. The pull request stays
		// a draft and the reason says which round it was (reviewsPart).
		return false, fmt.Sprintf("review round %d returned no parseable verdict after a retry", last.Round), failure.ReviewUnread
	case last.Verdict != "ship":
		if len(pol.StopBlocking) > 0 {
			// The stop accepts what a run's rounds left it, and the one thing it
			// accepts nothing of is a catastrophic finding — wherever in the run
			// it was left. The round Decide is reading is the highest level's,
			// so a level under it that the stop does not take is a reason this
			// sentence has to name: a reader who went to the round the count's
			// own reason points at would find nothing blocking there.
			return false, stopBlockingReason(pol.StopBlocking), failure.ReviewNotConverged
		}
		// The mode takes this round whole — the stop accepts it, or its own row
		// tolerates it — and the round read an earlier commit than the one the
		// run publishes: the fix its verdict bought is on the branch and no
		// review round read it (the time half of the stop is asked at the top of
		// a loop, after the fix the round before it bought, and the cheap loop
		// buys one after its last round too). Both of the mode's answers are
		// about the round, and neither reaches the commit that ships, so the
		// pull request stays a draft whose reason names the round (BF1m).
		if pol.UnreadFix != nil && pol.tolerates(last) && !pol.raisedAfterReview() {
			return false, unreadFixReason(*pol.UnreadFix), failure.ReviewUnread
		}
		if pol.acceptsAtStop(last) {
			// The run stopped without shipping a round, and what the round
			// left is what the mode accepts at the stop (the owner's ruling
			// of 2026-11-08): the pull request goes ready rather than
			// staying a draft, and the reason names the half of the stop
			// rule that ended the rounds — the record's own two words
			// (runstore.StopRoundsSpent, StopTimeThreshold).
			return true, fmt.Sprintf("review round %d left %s, accepted at %s (%s)",
				last.Round, Plural(len(pol.Tolerated), "finding"), pol.Mode, pol.AtStop), ""
		}
		if pol.tolerates(last) && !pol.raisedAfterReview() {
			return true, fmt.Sprintf("review round %d left %s, tolerated at %s", last.Round, Plural(len(pol.Tolerated), "finding"), pol.Mode), ""
		}
		if pol.Raised != nil {
			return false, pol.raiseReason(last), failure.ReviewNotConverged
		}
		return false, fmt.Sprintf("review round %d still has %s", last.Round, Plural(last.Findings, "finding")), failure.ReviewNotConverged
	case pol.HeldBack != nil:
		// The level that decided shipped, and an enabled level above it did
		// not: readiness is decided by the highest level that ran, and only
		// when every enabled outer level's last verdict is `ship` (D727, G4).
		// The case sits below the tolerance on purpose — a round the mode
		// tolerates is not a ship, and the level it came from is the one
		// Decide is reading, not one held back beside it.
		return false, heldBackReason(*pol.HeldBack), failure.ReviewNotConverged
	}
	return true, "", ""
}

// heldBackReason is Decide's reason for a run whose deciding level shipped and
// whose other enabled level did not: the level, its last verdict and round, and
// the head that verdict read (D727). The head is named because telling the two
// apart is the whole point of the rule — the level approved an earlier commit
// than the one shipped — and a reader has to be able to see which.
func heldBackReason(rs runstore.ReviewSummary) string {
	reason := fmt.Sprintf("review %s last shipped nothing: round %d left %s", levelNames[reviewLevelOf(rs)], rs.Round, rs.Verdict)
	if rs.Head != "" {
		reason += fmt.Sprintf(" on %s", shortGitHead(rs.Head))
	}
	return reason + ", so the pull request is a draft until every enabled review level ships"
}

// stopBlockingReason is Decide's reason for a run the stop kept a draft on
// because a level other than the deciding one left a finding no mode accepts at
// its stop: the levels, their rounds and the heads those rounds read (D727's
// formatting), and the fact that the stop is what does not take them. The
// finding itself is not named here — the pull request's own levels line and
// review section carry the round — but the level is, because the round Decide
// otherwise reports is not the one a reader has to go and look at.
//
// It says the stop does not accept them rather than counting them: the count on
// the record is the whole round's, and one blocking finding beside two accepted
// ones is a round the level's own line already accounts for.
func stopBlockingReason(rs []runstore.ReviewSummary) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		part := fmt.Sprintf("review %s left a finding the stop does not accept in round %d", levelNames[reviewLevelOf(r)], r.Round)
		if r.Head != "" {
			part += fmt.Sprintf(" on %s", shortGitHead(r.Head))
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " and ") + ", so the pull request is a draft"
}

// unreadFixReason is Decide's reason for a run whose last review round read an
// earlier commit than the one it publishes: the round whose fix no review read.
// It is the round rather than the count that is named because the count is the
// round's own and the run is not a draft on what the round found — the mode
// takes that — but on the commit the round never saw, which is the head the
// pull request's own review line prints and the record's last round.
func unreadFixReason(rs runstore.ReviewSummary) string {
	return fmt.Sprintf("the fix after review round %d was not reviewed", rs.Round)
}

// reviewLevelOf is the level a recorded round is a round of: its tier says
// which. A tier of "" is the senior review of a run that had no first line
// (ReviewSummary.Tier), and a tier this build does not know is read as the
// senior level too, which is what such a round recorded before the levels
// existed.
func reviewLevelOf(rs runstore.ReviewSummary) policy.Level {
	switch rs.Tier {
	case runstore.TierFirst:
		return policy.LevelL1
	case runstore.TierAdversarial:
		return policy.LevelL3
	}
	return policy.LevelL2
}

// lastRoundOf is the level's last round in the record, nil for a level that
// never ran.
func lastRoundOf(rs []runstore.ReviewSummary, level policy.Level) *runstore.ReviewSummary {
	for i := len(rs) - 1; i >= 0; i-- {
		if reviewLevelOf(rs[i]) == level {
			return &rs[i]
		}
	}
	return nil
}

// levelsResolved reports whether this run resolved its review levels at all
// (resolveLevels). A run that did not — one built by hand, and every run the
// unit tests build without a mode — reads readiness the way it did before the
// levels existed (decidingReview, seniorReview).
func (r *run) levelsResolved() bool {
	return rigor.Known(r.reachedRigor())
}

// decidingReview is the round readiness is decided on: the last round of the
// highest enabled review level (D723, G4) — level 3's where it is on, level 2's
// where level 3 is not, level 1's in a run whose level 2 does not run at all
// (POC). nil when that level left no round, which is a run that stopped before
// its decider ran: the mode asked for a level the run never held, nothing
// decided, and Decide keeps the pull request a draft (G4's "every enabled outer
// level's last verdict is ship" has no verdict at all to read).
//
// At the stop (r.stopAt) an enabled level that left no round is a level the
// stop cut, not one the run failed to reach: no new round is started past the
// stop, so the level above the last one that ran holds none by construction,
// and readiness reads the highest level that did run. Without that, turning
// level 3 on would end a Startup run at the time threshold a draft where the
// same run with level 3 off is ready — the strictness the owner's ruling of
// 2026-11-08 forbids. A run that stopped for any other reason keeps the answer
// it always had.
//
// A run that resolved no levels falls back to the last senior review, which is
// what every such run recorded before the levels existed and what readiness
// read then.
func (r *run) decidingReview() *runstore.ReviewSummary {
	if !r.levelsResolved() {
		return seniorReview(r.rec.Reviews)
	}
	for _, l := range []policy.Level{policy.LevelL3, policy.LevelL2, policy.LevelL1} {
		if !levelOn(r.levels, l) {
			continue
		}
		if rs := lastRoundOf(r.rec.Reviews, l); rs != nil {
			return rs
		}
		if r.stopAt == "" {
			return nil
		}
	}
	return nil
}

// heldBackLevel is the enabled outer level, other than the one that decided,
// whose last round left the run something the mode blocks on: the pull request
// is a draft when there is one (D727, G4). It is level 2 when level 3 decided,
// and nothing when level 2 or level 1 decided — level 3 is the only level above
// either of them.
//
// A level's round is read through the same findings policy the deciding round
// is read through (levelTolerated), which is what keeps a mode from becoming
// stricter as levels are turned on: a round whose every finding the mode
// tolerates counts as shipped, so a level 3 run is not held back by a level 2
// round level 2 alone would have been ready on. A round the mode blocks on is
// still the reason, whichever level it is.
//
// Level 1 is never one of them. It advises the levels above it and never
// decides on its own (seniorReview), so a cheap loop that ran out of rounds
// asking for changes is not what keeps a shipped senior review a draft; that is
// what the bounce is for. And a level whose round the run never held is not
// read here either: decidingReview answers nil for it, and the decider's own
// absence is the reason.
//
// It is read only where the deciding round itself shipped. Where it did not,
// that round's verdict is the reason Decide gives, and a second level's would
// only be noise beside it.
func (r *run) heldBackLevel() *runstore.ReviewSummary {
	if !r.levelsResolved() {
		return nil
	}
	decided := r.decidingReview()
	if decided == nil || decided.Verdict != "ship" {
		return nil
	}
	// Level 1 is skipped by name rather than by leaving it out of the slice
	// walked here: it is never a candidate whatever order the levels are read
	// in (below), and the code says so rather than the reader having to know
	// that level 2 is the only level it sits under.
	for _, l := range []policy.Level{policy.LevelL1, policy.LevelL2, policy.LevelL3} {
		if l == policy.LevelL1 || !levelOn(r.levels, l) || l == reviewLevelOf(*decided) {
			continue
		}
		rs := lastRoundOf(r.rec.Reviews, l)
		if rs == nil || rs.Verdict == "ship" {
			continue
		}
		if !r.levelTolerated(l, *rs) {
			return rs
		}
	}
	return nil
}

// levelTolerated reports whether the run's own findings policy lets this
// level's last round through whole: the question tolerates asks of the deciding
// round, asked of any enabled level's (D727, G4). The policy is built for that
// level's round — the same mode, the same gate and the same rule the deciding
// round was read through (findingsPolicyOf), the stop's own row included when
// the run stopped at it — so no level can be judged by a rule another level's
// round was not.
//
// It answers false for a round the run did not keep: the record's summary
// counts the findings, and the split needs their scores, so a level whose round
// the loop did not keep beside its own is a level this cannot read — and one
// that cannot be read is not tolerated (D4).
func (r *run) levelTolerated(l policy.Level, rs runstore.ReviewSummary) bool {
	if rs.Verdict == "ship" {
		return true
	}
	findings, ok := r.levelRound(l, rs.Round)
	if !ok {
		return false
	}
	pol := findingsPolicyOf(r.rigorCfg, r.reachedRigor(), rs.Round, scoresOf(findings), r.stopAt)
	return pol.tolerates(&rs)
}

// stopBlockingLevels is every enabled level other than the deciding one whose
// last round the stop does not accept whole (FindingsPolicy.StopBlocking): the
// second half of the ruling's "every remaining finding that is not
// catastrophic", asked of the run rather than of one round.
//
// It is asked only of a run that stopped, and only where the mode's row names a
// stop (rigor.AcceptsAtStop): those are the runs whose acceptance could
// otherwise flip a ready pull request whose deciding round did not carry the
// blocking finding itself. A mode whose stop is a draft — Pro, Critical, the
// zero mode — keeps Decide's own reasons, unchanged. Level 1 is not read, the
// way it is not read in heldBackLevel: it advises and never decides, and its
// round is the one the level above it reviewed.
//
// A level's round is read through the stop's own policy (levelTolerated with
// r.stopAt), so "the stop does not accept it" is the same rule the deciding
// round is read through, and a round the mode's rounds would have blocked on but
// the stop takes is not read here at all.
func (r *run) stopBlockingLevels() []runstore.ReviewSummary {
	if r.stopAt == "" || !r.levelsResolved() || !rigor.AcceptsAtStop(r.reachedRigor()) {
		return nil
	}
	decided := r.decidingReview()
	if decided == nil {
		return nil
	}
	var out []runstore.ReviewSummary
	for _, l := range []policy.Level{policy.LevelL2, policy.LevelL3} {
		if !levelOn(r.levels, l) || l == reviewLevelOf(*decided) {
			continue
		}
		rs := lastRoundOf(r.rec.Reviews, l)
		if rs == nil || rs.Verdict == "ship" {
			continue
		}
		if !r.levelTolerated(l, *rs) {
			out = append(out, *rs)
		}
	}
	return out
}

// levelRound is the level's last round as the loop kept it: the findings the
// record's summary only counts, for the two outer levels the loop keeps a round
// of — level 3 in lastSenior and level 2 in seniorLevelLast, which is the copy
// level 3 would otherwise overwrite. The round numbers of the two levels are
// not comparable (each loop counts from 1), which is why the level decides
// which of the two is read and the number only has to agree with the record's.
//
// Level 1 is not one of them: where the cheap loop decides readiness it writes
// lastSenior itself (firstLine), so the round is already there, and where a
// stop ended the run before level 2 started, the copy it left is what the stop
// puts in lastSenior (firstLine's stop paths) — one field either way, which is
// what the policy and the record are read off.
func (r *run) levelRound(l policy.Level, round int) ([]Finding, bool) {
	switch l {
	case policy.LevelL2:
		if r.seniorLevelLast.Round == round {
			return r.seniorLevelLast.Findings, true
		}
	case policy.LevelL3:
		if r.lastSenior.Round == round {
			return r.lastSenior.Findings, true
		}
	}
	return nil, false
}

// FindingsPolicy is the mode's findings policy as Decide is given it
// (docs/rigor-modes/implementation-plan.md T3.1): the last senior review
// round's findings, split by the mode's threshold table (rigor.Split, D2,
// D7a), and the round they were split from.
//
// It is how the mode's thresholds act without readiness gaining a second
// answer (F4): the split is computed at the call site, from the table in
// internal/rigor, and Decide is the only code that reads it.
type FindingsPolicy struct {
	// Mode is the mode the run resolved (D22's fold). A tolerated round's
	// reason names it, so the record says which mode let the findings through.
	Mode rigor.Mode
	// Round is the review round the split was made from. Decide checks it
	// against the summary it was given, so a split from another round is a
	// policy it cannot read — and a policy that cannot decide blocks (F3).
	Round int
	// Blocking and Tolerated are rigor.Split's two halves of that round's
	// findings, in the reviewer's own order. Both are empty for a run that may
	// tolerate nothing at all: one that configured no rigor, or a mode whose
	// rigor.Files row files no tolerated finding (D9).
	Blocking, Tolerated []rigor.Score
	// Raised is the raise the run's mode took during the run, nil when it never
	// rose (T3.5, D19). It is what makes a raised mode decide readiness and
	// name itself in the reason.
	Raised *RigorRaise
	// HeldBack is an enabled outer level's last round whose verdict is not
	// `ship`, where the level that decided shipped (heldBackLevel, D727, G4).
	// Decide blocks on it: the run's highest level approved the change, and a
	// level the run's mode required did not.
	HeldBack *runstore.ReviewSummary
	// AtStop is the half of the stop rule that ended the run's rounds, "" for a
	// run whose decider stopped for a reason of its own — a shipped round, a
	// failed stage, a halt. It is not "" exactly when the split above is
	// rigor.SplitAtStop's rather than rigor.Split's, so the policy says which
	// rule read the round: Decide's reason names the acceptance instead of the
	// tolerance, and a run whose round a stop cut is reported as the ruling of
	// 2026-11-08 has it — merged as-is with what it left.
	AtStop string
	// StopBlocking is every enabled level other than the deciding one whose last
	// round the stop does not accept whole, in the order the levels run
	// (stopBlockingLevels). It is what the ruling's "every remaining finding that
	// is not catastrophic" makes of a run whose deciding round is not the only
	// round the stop found: the stop accepts what any level left it but the
	// catastrophic finding no mode's row takes, so a level under the decider that
	// left one is a reason Decide has to name — the round it is reading would not
	// show it. Empty for every run whose row names no stop, and for every run
	// that did not stop.
	StopBlocking []runstore.ReviewSummary
	// UnreadFix is the last review round the run's record holds, set only where
	// the commit the run publishes is not the one that round read: the fix the
	// round's verdict bought — or the work a later stage did — is on the branch
	// and no review round read it. Both answers the mode gives about a round,
	// the stop's acceptance and its own tolerance, are answers about the commit
	// that round read, and neither reaches code no review read: a pull request is
	// never ready on it (BF1m). nil wherever the last round read the commit that
	// ships, which is every run whose loop bought no fix after its last round.
	UnreadFix *runstore.ReviewSummary
}

// RigorRaise is the raise a run's mode took during the run (T3.5, D22): the
// mode it rose to, the rule the moment that raised it matched when it matched
// one, and D22's moment it rose at. The moment that took it records it
// (noteRigorRaise, rigor.go), because the block keeps no per-moment history and
// the moment is what says whether any review round was held at the mode the run
// ends at. A run whose mode never rose has none, which is D19.
type RigorRaise struct {
	// Mode is the mode this raise reached, which is the mode the reason names.
	Mode rigor.Mode
	// Area is the rule the raising moment matched — its own strictest, which
	// D23 makes the deciding one — and "" when the layer that decided it is not
	// an area: a raise by a layer, or by a built-in floor outside every layer
	// (D18), has no rule to name.
	Area string
	// At is D22's moment the raise happened at: a round, or finalize, the one
	// moment no review round follows.
	At string
}

// raisedAfterReview reports whether the mode rose after the last review round:
// D22's third moment, before the pull request is marked ready. Tolerating that
// round's findings would make the pull request ready on a review nobody held at
// the mode the run is judged by, which is what D22's "too late to buy rounds"
// forbids.
func (pol FindingsPolicy) raisedAfterReview() bool {
	return pol.Raised != nil && pol.Raised.At == runstore.RigorAtFinalize
}

// raiseReason is Decide's reason for a run its raised mode keeps a draft: the
// raise first — the mode, the rule that raised it and the moment — and, when
// the raised mode does not tolerate the round either, the round's own count
// ahead of it. A reader of the reason can go and look at the rule the run's own
// work matched rather than guess what changed.
func (pol FindingsPolicy) raiseReason(last *runstore.ReviewSummary) string {
	reason := fmt.Sprintf("the mode was raised to %s", pol.Raised.Mode)
	if pol.Raised.Area != "" {
		reason += fmt.Sprintf(" by area %s", pol.Raised.Area)
	}
	if pol.Raised.At == runstore.RigorAtFinalize {
		reason += " at finalize, after the last review round"
	} else {
		reason += " after a review round"
	}
	if !pol.tolerates(last) {
		reason = fmt.Sprintf("review round %d still has %s, and %s", last.Round, Plural(last.Findings, "finding"), reason)
	}
	return reason
}

// tolerates reports whether the mode lets the whole round through: the split
// Decide was given is the one that round produced, every finding the record
// counts sits in its tolerated half, and there is at least one.
//
// The count is checked against the record rather than trusted, and a round
// that left no finding is not tolerated: a `changes` verdict that names
// nothing is a review which failed to say what it wanted, and D4's rule is
// that a policy which cannot decide blocks.
func (pol FindingsPolicy) tolerates(last *runstore.ReviewSummary) bool {
	return last.Findings > 0 &&
		pol.Round == last.Round &&
		len(pol.Blocking) == 0 &&
		len(pol.Tolerated) == last.Findings
}

// acceptsAtStop reports whether the stop is what makes this run ready: the
// policy is the stop's own split of the deciding round (AtStop, and the split
// taken from rigor.SplitAtStop), that round did not ship — a shipped round is
// ready on its own verdict and the stop has nothing to accept for it — the
// round is one the policy takes whole, no enabled level under the decider left
// a finding the stop does not take (StopBlocking), no raise after the last
// round withdrew the mode the split was read under (D22, raisedAfterReview), and
// the commit the run publishes is the one the run's last review round read
// (UnreadFix, BF1m): the acceptance is an answer about a round, and a round
// whose fix nobody read is an answer about code the run did not publish.
//
// It is Decide's ready answer and the record's Accepted block stated once: the
// block says the run merged with what it left, and a record that claimed one
// where Decide kept the pull request a draft would report an acceptance the run
// never made.
func (pol FindingsPolicy) acceptsAtStop(last *runstore.ReviewSummary) bool {
	return pol.AtStop != "" && last != nil && last.Verdict != "ship" &&
		len(pol.StopBlocking) == 0 &&
		pol.UnreadFix == nil &&
		!pol.raisedAfterReview() &&
		pol.tolerates(last)
}

// findingsPolicyOf is the policy Decide is given for one review round: the
// mode's split of that round's findings, or the zero policy where the run may
// not tolerate anything at all.
//
// D9 is the bound on an ordinary round. A mode may not let a finding through
// where the repository has not asked for the mode, so the split is taken only
// where both hold: the repository opted in (rigor.Config.Configured — an opt-in
// read from the base branch, D25) and the mode's rigor.Files row tolerates.
// Every other run gets the zero policy and decides by the count on the record,
// exactly as it did before modes existed — the unconfigured repository, which
// D19 resolves Pro for its budget, and POC, whose account of what it left open
// is its pull request body's own line (D30). A tolerated finding is recorded in
// the run's record (Tolerated, T3.4) and named in the pull request's status
// section, which is all the run does with it: nothing is written into the
// repository for it (finalize).
//
// atStop is the half of the stop rule that ended the run's rounds, "" for a run
// that stopped for a reason of its own. It is asked last, and only of a round
// the mode's own rule blocks on: the stop changes what a run *accepts* — the
// findings its rounds were gone before it could buy a fix for — and it renames
// nothing the mode already let through. A round below Pro's floor is tolerated
// at the stop exactly as it is tolerated in a round, and its reason says so.
//
// Where the round is one the mode's own rule blocks on, the mode's row answers
// what it accepts at the stop (rigor.SplitAtStop, the owner's ruling of
// 2026-11-08), and only for a mode whose row names a stop (rigor.AcceptsAtStop):
// POC and Startup accept everything but a catastrophic finding, Hotfix keeps its
// own row, and Pro, Critical and every name the table does not hold keep the
// draft, which is the fail-closed zero value. D9's bound is not asked here and
// does not need to be: it is about filing an item, and the stop accepts
// findings into the run record and the pull request's body alone. A run whose
// repository never opted in resolves the default mode (D19), whose row names no
// stop, so it is a draft at the stop whatever this branch does; a task that
// named a mode is a run in that mode (rigor.Request.Mode), and the ruling's
// answer is the mode's.
func findingsPolicyOf(cfg rigor.Config, mode rigor.Mode, round int, findings []rigor.Score, atStop string) FindingsPolicy {
	// D9 first, and the mode's own round rule with it: a mode may not let a
	// finding through where the repository has not asked for the mode, so the
	// split is taken only where both hold — the repository opted in
	// (rigor.Config.Configured — an opt-in read from the base branch, D25) and
	// the mode's rigor.Files row tolerates. Every other run gets the zero
	// policy and decides by the count on the record, exactly as it did before
	// modes existed — the unconfigured repository, which D19 resolves Pro for
	// its budget, and POC, whose account of what it left open is its pull
	// request body's own line (D30). A tolerated finding is recorded in the
	// run's record (Tolerated, T3.4) and named in the pull request's status
	// section, which is all the run does with it: nothing is written into the
	// repository for it (finalize).
	ordinary := FindingsPolicy{}
	if cfg.Configured() && rigor.Files(mode).Tolerated {
		blocking, tolerated := rigor.Split(mode, findings)
		ordinary = FindingsPolicy{Mode: mode, Round: round, Blocking: blocking, Tolerated: tolerated}
		if len(blocking) == 0 {
			return ordinary
		}
	}
	if atStop != "" && rigor.AcceptsAtStop(mode) {
		blocking, accepted := rigor.SplitAtStop(mode, findings)
		return FindingsPolicy{Mode: mode, Round: round, Blocking: blocking, Tolerated: accepted, AtStop: atStop}
	}
	return ordinary
}

// findingsPolicy is findingsPolicyOf for this run: the last review round the
// loop read as the decider's, the mode the run resolved, the rigor
// configuration that mode was read from, the raise the run's mode took during
// it (noteRigorRaise, rigor.go), the outer level the deciding one held back,
// if any, and the half of the stop rule the run's rounds ended on, if any.
func (r *run) findingsPolicy() FindingsPolicy {
	pol := findingsPolicyOf(r.rigorCfg, r.reachedRigor(), r.lastSenior.Round, scoresOf(r.lastSenior.Findings), r.stopAt)
	pol.Raised = r.rigorRaise
	pol.HeldBack = r.heldBackLevel()
	pol.StopBlocking = r.stopBlockingLevels()
	pol.UnreadFix = r.unreadFix()
	return pol
}

// unreadFix is the last review round the run's record holds where the commit
// the run publishes is not the one that round read: the fix the round's verdict
// bought, or the work a stage after it did, is on the branch and no review
// round read it. It is what the mode's two answers about a round cannot cover
// (FindingsPolicy.UnreadFix, BF1m).
//
// The record's *last* round is the one compared, and not the deciding round the
// policy is otherwise built from, because a review round moves the head only
// where a fix follows it: where some round read the commit that ships, every
// round after it read that same commit, so the last entry is the round that
// would have had to read it — and in a bounce it is the cheap loop's round,
// which reads the head level 2's round bought the fix for, while the deciding
// round's head is the one before that fix by construction.
//
// A round that recorded no head — the read of the repository's head failed —
// is not shown to have read the commit that ships, so it is read as a fix
// nobody reviewed: the fail-closed answer the table gives a finding it cannot
// read (D4). Every stop the run did not reach before that round is a draft the
// same way, and its reason names the round instead of the count.
func (r *run) unreadFix() *runstore.ReviewSummary {
	rs := r.rec.Reviews
	if len(rs) == 0 {
		return nil
	}
	last := &rs[len(rs)-1]
	if last.Head != "" && last.Head == r.rec.HeadSHA {
		return nil
	}
	return last
}

// toleratedRecord is what the run's record keeps of the findings the mode let
// through (docs/rigor-modes/implementation-plan.md T3.4, D9, F19): one entry
// per tolerated finding, with the reviewer's own words for it. It is nil when
// the policy tolerates nothing, so the record carries no block at all rather
// than an empty one. A run that stopped at the stop keeps the findings its stop
// accepted here too, because that is what this field is for: the findings the
// run's mode decided not to fix, whichever moment of the run it decided at.
func (r *run) toleratedRecord(pol FindingsPolicy) *runstore.ToleratedFindings {
	return runstore.NewTolerated(r.toleratedEntries(pol, r.lastSenior.Findings))
}

// acceptedRecord is what the run's record keeps of a stop's acceptance (the
// owner's ruling of 2026-11-08): the findings the mode accepted because the run
// had no round left to spend on them, with the round they were left by and the
// half of the stop rule that ended the run. It is a block of its own rather
// than a flag on Tolerated's entries because the two facts differ: Tolerated
// says what the mode would not fix, and this says that the run stopped, why,
// and what that left open — a reader that only saw the first would read a run
// merged at its time threshold as a run whose review let the finding go.
//
// nil for every run whose stop did not accept the round it ended on: one that
// stopped for a reason of its own, and one whose stop found a finding the mode
// blocks on (which is a draft). It is written beside Tolerated and never
// instead of it, so the pull request's body, the report and the filing path
// read the accepted findings exactly as they read a tolerated one — and on the
// same terms: like Tolerated, the block is the mode's own reading of the round
// the run ended on, and the reasons a pull request is still a draft — no
// verified test on the final commit, tests failing on it, a decision the run is
// waiting on the owner for — are Decide's, checked above the verdict and stated
// in the run's own reason rather than here.
func (r *run) acceptedRecord(pol FindingsPolicy, last *runstore.ReviewSummary) *runstore.AcceptedFindings {
	if !pol.acceptsAtStop(last) {
		return nil
	}
	return runstore.NewAccepted(pol.Mode, last.Round, pol.AtStop, r.toleratedEntries(pol, r.lastSenior.Findings))
}

// toleratedEntries pairs the policy's tolerated scores back with the round's
// findings, as the record's entries.
//
// The pairing is the split's own, without asking the table a second time.
// rigor.Split appends a finding's score to Tolerated in the reviewer's order,
// and whether a finding blocks is a function of its score alone (rigor.Blocks
// reads nothing else), so a tolerated score is tolerated at every finding that
// carries it: walking the round's findings and taking each tolerated score at
// the next finding it equals pairs the two lists exactly, and the record
// cannot disagree with the readiness rule about which findings were tolerated.
func (r *run) toleratedEntries(pol FindingsPolicy, findings []Finding) []runstore.ToleratedFinding {
	if len(pol.Tolerated) == 0 {
		return nil
	}
	entries := make([]runstore.ToleratedFinding, 0, len(pol.Tolerated))
	next := 0
	for _, f := range findings {
		if next == len(pol.Tolerated) {
			break
		}
		s := scoreOf(f)
		if s != pol.Tolerated[next] {
			continue
		}
		entries = append(entries, runstore.ToleratedFinding{
			Title:      r.redact(f.Summary),
			Mode:       pol.Mode,
			Likelihood: s.Likelihood,
			Impact:     s.Impact,
			FixCost:    s.FixCost,
			Class:      s.Class,
		})
		next++
	}
	return entries
}

// seniorRound is one senior review round as the loop read it: the round number
// the record carries, and the findings the summary beside it only counts. The
// findings are kept whole because the mode's threshold table reads their
// scores, and a count is not enough for it (T3.1, D7a).
type seniorRound struct {
	Round    int
	Findings []Finding
}

// scoresOf presents the reviewer's findings to the threshold table: the three
// anchored axes and the class it reads, never the free-form severity string
// beside them (D2). A finding with no score reads as the zero Score, which
// blocks in every mode (D4).
func scoresOf(findings []Finding) []rigor.Score {
	out := make([]rigor.Score, 0, len(findings))
	for _, f := range findings {
		out = append(out, scoreOf(f))
	}
	return out
}

// scoreOf is one finding as the table reads it.
func scoreOf(f Finding) rigor.Score {
	return rigor.Score{Likelihood: f.Likelihood, Impact: f.Impact, FixCost: f.FixCost, Class: rigor.Class(f.Class)}
}

// Plural formats "1 finding" / "3 findings".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
