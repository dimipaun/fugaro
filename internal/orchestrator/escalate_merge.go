// The ladder's executor past rung 2 (E10b): rung 3's merge under D1245's gate,
// and the seams rung 4's split hangs off.
//
// Rung 3 is where the ladder stops spending runs and starts deciding about the
// pull request itself, and the decision is not this file's: escalation.Decide
// answered rung 3, and everything the gate reads is a fact a caller gathered.
// What is here is the acting — the two runs a rung 3 spends (a review of the
// head it judges, then the merge judge), the two host calls a landing makes, the
// work a landing leaves behind, the line the log keeps and the question a
// refusal raises for a person.
//
// Four properties carry the weight, and each of them is a decision this file
// follows rather than one it takes.
//
// The head is judged once and merged once (D1243, G2). Every read, every run
// and the merge itself name one head SHA: the review-only run D2043 requires is
// launched at it, the judge reads it, the host's own answer is compared with it
// (gitprov.SameCommit) and the merge refuses a head that moved under the gate. A
// push after the gate therefore refuses the merge rather than landing a commit
// nobody read, and the transition's own head is what a reader sees the rung was
// spent by.
//
// Every way of not knowing is a refusal (G5, D2044). A reading that could not be
// made, a record that is not terminal, a changed-path set that is missing,
// truncated or not Routine (D2047), CI that is not green, a finding that blocks
// a landing, and a judge that answered nothing or answered anything but
// merge_with_follow_ups: each of them raises D1247's waiting_on: person with the
// judge's own words fenced into the question, and none of them merges. A rung 3
// that acted on an input it could not read would be the merge the gate exists to
// refuse.
//
// The mode decides who is told, and never whether the gate applies (D2030,
// D2037). Pro's and Hotfix's ceiling merges under the same gate and says so in a
// notification; every other ceiling, and every item the gate refused, waits for
// a person. Nothing here reads the mode to decide whether to merge — only
// whether to tell somebody.
//
// Nothing here can ask for a bypass (D2045, G14). The host is four calls — the
// pull request, its checks, a ready call and a merge — and the merge names an
// expected head and no other argument, so an admin merge or a forced one is not
// a discipline a caller keeps but a call this interface does not have.
//
// The hook is level-triggered and re-entrant: it is entered once per cycle for
// an item the decision put at rung 3, and each entry re-derives what is left to
// do from the reading and from the log's own lines, so a daemon that died
// between two of its steps picks the same sequence up rather than repeating one
// (G4). Nothing it writes is a counter that only grows: the ledger's transition
// is written when the rung moves, each line is written under a name derived from
// the rung, the step and the head, and a question whose id the node already
// carries is not asked again.
//
// The judge's verdict is read by the caller and not here. internal/runner is on
// the list of packages a cycle may not import (D1221, TestTickMakesNoModelCall),
// and MergeJudgeOf — the reader that turns an absent, failed, late or malformed
// verdict into the veto D2044 states — lives there, so MergeReading.Verdict is
// that reader's answer and the gate below re-reads it fail-closed anyway.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The two recipes rung 3 launches beside the item's own (E5, E6). RecipeReview
// is D2043's: a head that only a rebase run has touched has been written by an
// agent, so it is read by a run whose own work is reading it. RecipeJudge is
// E6's, and its launcher leaves the brief at runstore.MergeJudgeInputName for
// the judge stage to read.
const (
	RecipeReviewOnly = "review-only"
	RecipeMergeJudge = "merge-judge"
)

// BeyondSeams is what rungs 3 and 4 act through: the reading the gate is decided
// on, the host a landing is made through, the node the moves are written to, the
// launcher a rung 3 run starts through, the comment a move posts and the log
// every move leaves a line in.
//
// Everything but the notification sink and the warning sink is required. A rung
// 3 built with a missing seam would decide a merge it could not carry out, and
// the direction an unwired seam has to fail in is "nothing happens" — which is
// what Set and Beyond between them give: no seam, no hook, and the item is left
// exactly where E10a's default put it.
type BeyondSeams struct {
	// Read gathers what the gate is decided on for one item and one head.
	Read BeyondReader
	// Host is the provider slice a landing needs (E8).
	Host MergeHost
	// Node is the state the hook reads and the one write path it lands its
	// moves through: the node the store holds, read and written around the
	// revision it is at (patch.go's Apply is bound to BaseRev and store.go's
	// Save to the ETag it read). It is not a store of the hook's own, but the
	// cycle is not handed the hook either, so what keeps the two in step is on
	// the caller's side: the executor re-reads the node once the hook returns
	// (escalate_launch.go's ladderBeyond, tick.go's reread), and the rest of
	// that Step's writes derive from the document the hook left rather than
	// from the one the cycle read before it.
	Node MergeNode
	// Launch starts one run of another recipe, as the rungs below do.
	Launch LadderLauncher
	// Comment is where one move is posted, on the pull request it moved.
	Comment LadderCommenter
	// Log is the line every move is appended under, and the place a line
	// already written is read back from.
	Log BeyondLog
	// Notify is where the merge a Pro or Hotfix ceiling makes is told, or nil
	// for a daemon that tells nobody.
	Notify Notifier
	// Now is the database server's clock: every stamp a move writes comes from
	// it, and a hook that cannot read it writes nothing (D1230).
	Now ServerNow
	// Mint mints the id a rung 3 run is recorded under.
	Mint MintFunc
	// ReadyDrafts is `escalation.ready_drafts` (D2041, D2046): off, a stalled
	// draft whose gate passes waits for a person; it ships on, and the owner's
	// written false is what turns it off.
	ReadyDrafts bool
	// Warn is where a failure that must not end the cycle goes: a comment a
	// host would not take, a notification no sink carried. Nil drops it.
	Warn func(error)
}

// Set reports whether every seam but the two optional ones is there.
func (s BeyondSeams) Set() bool {
	return s.Read != nil && s.Host != nil && s.Node != nil && s.Launch != nil &&
		s.Comment != nil && s.Log != nil && s.Now != nil && s.Mint != nil
}

// Beyond is the hook rungs 3 and 4 attach through (E10a's LadderBeyond). A seam
// set that is not complete answers no hook at all, which leaves the item where
// the decision put it — the same place a release without this file leaves it —
// rather than a rung 3 that launched a run nothing recorded.
func (s BeyondSeams) Beyond() LadderBeyond {
	if !s.Set() {
		return nil
	}
	return s.beyond
}

func (s BeyondSeams) beyond(ctx context.Context, item WorkItem, step escalation.Step, reason escalation.Reason, read LadderReading) (LadderAct, error) {
	if read.Running != "" {
		// A run of the ladder's is under way for this item and has not
		// reported. What it answers with is the next cycle's, and acting over
		// it would judge a head that run's own push may be about to move. The
		// wait is the run's and not this hook's — the executor reads a ladder
		// run that has not reported before it calls any hook at all (ladderItem)
		// — so there is nothing here for the cycle's count to carry.
		return LadderIdle, nil
	}
	switch step.Rung {
	case escalation.R3:
		return s.merge(ctx, item, step, reason, read)
	case escalation.R4:
		return s.split(ctx, item, step, reason, read)
	}
	return LadderIdle, nil
}

// BeyondReader gathers what the rung 3 gate is decided on for one item at one
// head: the run behind that head, the findings open at it — the run's own and
// the review's — the pull request it belongs to, and the two ladder runs at that
// head: the review D2043 requires and the judge whose answer may veto.
//
// The review's findings are read from the review-only run's own artifact and
// never from the run behind the record's: D2043 lands a rebased head under a
// review's verdict **and its severities**, and a reader that carried the verdict
// as a flag alone would leave the gate's no-critical-finding clause fed by the
// run's own account of itself.
//
// A reading that could not be made is Read false, which the gate refuses on
// (G5); an error is a read that did not happen, and it ends the item's turn for
// the next cycle to retry.
type BeyondReader interface {
	MergeAt(ctx context.Context, item WorkItem, head string) (MergeReading, error)
}

// MergeReading is one head as the daemon reads it.
type MergeReading struct {
	// Read is whether the reading was made at all.
	Read bool
	// Number is the pull request the head belongs to, as the record of the run
	// behind it answers. 0 is a run with no pull request, and there is nothing
	// for a ladder to merge.
	Number int
	// Record is the run whose head this is: its status, the commit it pushed
	// and what it says it changed.
	Record *runstore.Record
	// Paths is the changed-path set the host reports for this head, and nil
	// where that read was not made. D1245's Routine clause is read on this set
	// and never on the record's (see mergeGate): a record is the run's own
	// account of its diff, and the clause is a rule about what may be merged
	// unread.
	Paths *runstore.ChangedFiles
	// Findings is that run's open findings and the state of the read that
	// produced them (BF1) — the same input the triage reads, so the two rules
	// refuse on the same evidence.
	Findings Findings
	// ReviewFindings is the review-only run's own open findings at this head —
	// the read D2043 requires — and the state of the read that produced them.
	// The gate reads them beside the run's (see headFindings): a landing makes
	// this commit durable, and the severities that decide whether it may are the
	// review's, so a review that reported a critical finding stops the merge
	// however clean the run behind the record reported itself to be.
	ReviewFindings Findings
	// ReviewRun is the review-only run at this head, or "".
	ReviewRun string
	// Scored is whether a review at this head has a verdict. A head with none
	// has not been read by anything, and D2043 merges only under a scored
	// review of the head under judgement.
	Scored bool
	// JudgeRun is the merge judge's run for this head, or "".
	JudgeRun string
	// JudgeScored is whether the judge at this head has answered, and it is the
	// judge's half of what Scored is to the review. A judge that has not
	// answered yet — no run of its own in the reading, or a run that has not
	// reported — is a wait, which the gate retries rather than reading as the
	// veto D2044 makes of an absent verdict: a veto raised for a judge that has
	// simply not spoken yet spends the item's one question, and the judge's real
	// answer then reaches nobody (D2037).
	//
	// A judge whose run has ended without an answer has answered with nothing,
	// and the caller that read the run's own end says so: Scored, with the zero
	// verdict D2044 makes a veto. That is not a distinction the gate can draw
	// for itself — both arrive as a run id with no verdict beside it — and only
	// the reader that read the run knows whether it has ended.
	JudgeScored bool
	// Verdict is the judge's answer, as the caller read it out of that run. The
	// zero value is a judge that answered nothing, which is a veto (D2044);
	// where the judge has not answered at all, JudgeScored is false and the gate
	// waits rather than reading the zero value.
	Verdict runstore.MergeJudge
}

// MergeHost is the slice of the provider a landing needs (E8). It is four calls
// and no more, and the merge names an expected head and nothing else: D2045's
// "never an admin or bypass merge" is a property of this interface rather than
// of a caller's discipline, and gitprov.Provider is what satisfies it.
type MergeHost interface {
	PullRequest(ctx context.Context, number int) (gitprov.PRInfo, error)
	Checks(ctx context.Context, number int, sha string) ([]gitprov.CheckRun, error)
	MarkReady(ctx context.Context, number int, expectedHeadSHA string) error
	Merge(ctx context.Context, number int, expectedHeadSHA string) error
}

// The provider is what a landing is made through, checked here rather than
// wherever the daemon is wired: a provider that grew a parameter this interface
// cannot name would fail to build instead of quietly widening what a daemon may
// ask a host for.
var _ MergeHost = gitprov.Provider(nil)

// MergeNode is the state a hook reads and the one write path it lands its moves
// through: the cycle's own, or the moves are written over a revision the cycle
// has already moved past. See BeyondSeams.Node.
type MergeNode interface {
	State(ctx context.Context) (State, error)
	Apply(ctx context.Context, ops []Op) error
}

// BeyondLog is the log seam: the append the rungs below use, and the two reads
// that answer what is already written down.
//
// The first read is what makes a launch safe to retry. A run's record appears
// when its run does, so the window between asking a launcher for a review and
// the record existing is a window in which the reading alone cannot tell a head
// that has had its review from one that has not — and D2043 allows one review
// per head. The line the launch itself writes is what closes it (G4).
//
// The second is when a line was written, which is what bounds a wait the ladder
// keeps retrying (boundRetry): such a wait looks like movement, and no reading
// says how long it has looked that way, so the cycle that starts one writes a
// line of its own and later cycles read its stamp back. That line is written
// once and never rewritten, because a log keeps the latest write under an id and
// a stamp that moved with every cycle would bound nothing.
type BeyondLog interface {
	LadderLog
	Logged(ctx context.Context, id string) (bool, error)
	LoggedAt(ctx context.Context, id string) (time.Time, bool, error)
}

// MergeLaunches is whether a rung 3 run has already been asked for at the head
// under judgement, read from the log's own lines. See BeyondLog.
type MergeLaunches struct {
	Review bool
	Judge  bool
}

// MergeKind is which of the rule's outcomes the gate reached.
type MergeKind string

const (
	// MergeAlready is a pull request the host answers as merged. Nothing merges
	// it again and no clause of the gate is read for it — except one: where the
	// ladder's own landing is behind this head's merge (landed) and nothing has
	// said so yet, the cycle is re-entering a landing whose write did not land,
	// and what a landing owes is carried out here (see Replay).
	MergeAlready MergeKind = "already-merged"
	// MergeLaunchReview is D2043's first step: a `review-only` run at the head
	// under judgement, because a rebase run is an agent and can push anything.
	MergeLaunchReview MergeKind = "launch-review"
	// MergeLaunchJudge is the merge judge's run (E6): the head is covered by a
	// scored review, so the judge may read it, and it may only veto.
	MergeLaunchJudge MergeKind = "launch-judge"
	// MergeLand is the gate passed and the merge is the ladder's.
	MergeLand MergeKind = "land"
	// MergeWait is the gate refused, or the mode's ceiling is a person's: the
	// item waits on a person with the judge's verdict attached (D1247, D2037).
	MergeWait MergeKind = "wait"
	// MergeSplit is rung 4: the item is cancelled and the work continues as a
	// scope a person approves.
	MergeSplit MergeKind = "split"
)

// GateReason is why the gate reached the outcome it did, in a vocabulary of its
// own: the line the log keeps carries it verbatim and never a sentence of a
// host's or a model's (G8).
//
// D1245's own clauses keep the triage's codes (TriageReason) rather than
// restating them here, because they are the same clauses read on the same
// evidence — a person reading either line reads the same word for a record that
// is not over, a path set that is truncated and CI nobody could read. What is
// here is only what the triage has no reason to name.
type GateReason string

const (
	// GateUnreadable is a reading the daemon could not make.
	GateUnreadable GateReason = "unreadable"
	// GateNoHead is an item with no head to judge.
	GateNoHead GateReason = "no_head"
	// GateNoPullRequest is a head with no pull request behind it.
	GateNoPullRequest GateReason = "no_pull_request"
	// GateNotOpen is a pull request that is neither open nor merged.
	GateNotOpen GateReason = "pull_request_not_open"
	// GateHeadMoved is a head the pull request has moved past since the gate
	// was decided, which is the push D1243 refuses to merge over.
	GateHeadMoved GateReason = "head_moved"
	// GateUncovered is a head no scored review covers (D2043).
	GateUncovered GateReason = "uncovered"
	// GateReviewSpent is a head whose review-only run was already launched and
	// has not answered: there is one review per head, and the next cycle reads
	// what it said.
	GateReviewSpent GateReason = "review_spent"
	// GateJudgeAbsent is a head no judge has been asked about yet (E6).
	GateJudgeAbsent GateReason = "judge_absent"
	// GateJudgeSpent is a head whose judge has been launched and has not
	// answered: there is one judge per head, and the next cycle reads what it
	// said.
	GateJudgeSpent GateReason = "judge_spent"
	// GateLaunchUncovered is a rung 3 run the ladder may not start: an item no
	// approval covers, one a dependency still holds, one a run is moving
	// through, or one whose ladder runs are all spent. The rungs below leave
	// such an item where it is, and rung 3 says so — a review-only run and a
	// judge are runs of the item's own work, spent under a person's approval
	// like every other (D2042, G15).
	GateLaunchUncovered GateReason = "launch_uncovered"
	// GateJudgeVeto is the judge's no: it blocked the merge, said something the
	// reader could not place, or ended without answering (D2031, D2044). A judge
	// that has not answered yet is not this — the gate retries that wait, and
	// only a head whose judge has stopped speaking is a person's (GateJudgeSpent).
	GateJudgeVeto GateReason = "judge_veto"
	// GateCeilingUnset is a decision with no ceiling action for this rung, which
	// is a threshold nobody set and never a default (G5).
	GateCeilingUnset GateReason = "ceiling_unset"
	// GateModeWaits is the ceiling D2037 gives Critical: the ladder will not
	// merge this item in any mode.
	GateModeWaits GateReason = "mode_waits"
	// GateDraftHeld is a stalled draft with `escalation.ready_drafts` off
	// (D2041, D2046).
	GateDraftHeld GateReason = "draft_held"
	// GateRunRunning is a run behind the head that has not ended: what it pushes
	// is not what the gate read (D1245).
	GateRunRunning GateReason = "run_running"
	// GateHeadUnpushed is a record whose own pushed head is not the head under
	// judgement, so the merge would land a commit the record does not claim.
	GateHeadUnpushed GateReason = "head_unpushed"
	// GateCIRed is CI that concluded something other than green.
	GateCIRed GateReason = "ci_red"
	// GatePathsNotRoutine is a changed-path set the classifier does not call
	// Routine: D1245's clause, and the one D2047 keeps narrow.
	GatePathsNotRoutine GateReason = "paths_not_routine"
	// GatePathsUncorroborated is a head whose changed-path set nobody read from
	// the host. The record's own list is the run's account of its diff, and a
	// clause that decides whether a merge may land unread does not rest on the
	// diff's own opinion of itself (D1245, D2047).
	GatePathsUncorroborated GateReason = "paths_uncorroborated"
	// GateMergeRefused is a merge the host would not take. The refusal's own
	// closed code sits beside it (MergeOutcome.Refusal).
	GateMergeRefused GateReason = "merge_refused"
	// GateScoped is rung 4's split scoped to the blocker the judge named.
	GateScoped GateReason = "scoped"
	// GateUnscoped is rung 4 with no blocker to scope a split to, which is a
	// person's question rather than an invented scope (G5).
	GateUnscoped GateReason = "unscoped"
)

// MergeOutcome is what the gate reached, and everything the acting needs to
// carry it out: nothing here is a store, a host or a clock, so an outcome is a
// value a test compares.
type MergeOutcome struct {
	// Kind is the outcome.
	Kind MergeKind
	// Reasons are D1245's clauses in the triage's own codes: the clause that
	// refused, or — when the gate passed — the whole of the evidence it passed
	// on, which is what the log's line carries for a landing.
	Reasons []TriageReason
	// Facts are the ladder's own codes for what refused, or for what the gate
	// read that the triage has no word for.
	Facts []GateReason
	// Launch is the recipe a launch outcome asks for.
	Launch string
	// Ready is whether the pull request has to be marked ready before it is
	// merged (D2041).
	Ready bool
	// Replay is an already-merged outcome that is this cycle's to finish: the
	// ladder's own landing is behind this head's merge and the cycle that made
	// it did not get as far as filing what the landing left behind, so the
	// findings are filed and the person a Pro or Hotfix merge is owed is told
	// here instead.
	Replay bool
	// Retry is a wait on something that is still moving: a ladder run at this
	// head that has not ended, a review that has been asked for and not yet
	// recorded, a judge that has been asked for and not yet answered, checks
	// that have not concluded. The next cycle reads the same head again and the
	// answer may well be different, so none of it is a person's to act on and no
	// question is raised for it (see merge).
	//
	// It is set where the reading itself moves, and nowhere else: everything
	// else the gate waits on has stopped — no checks reported at all, a check
	// that finished unreadably, a check nobody has placed, a review that ended
	// without a score, a judge whose run ended without an answer, a run whose
	// head is not the one it pushed — and a person is asked about each of those,
	// once, in the item's own name.
	//
	// The gate does not bound this, because what it reads cannot say how long a
	// reading has looked like movement: a commit status a dead job left at
	// "pending" and a job really running read the same. merge bounds it instead,
	// on the log's own stamp of the first cycle that retried the wait at this
	// head (boundRetry), and past that bound the wait is a person's after all.
	Retry bool
	// Refusal is the closed code of a merge a host refused, and "" otherwise.
	Refusal string
	// Verdict is the judge's answer, attached to the question a refusal raises
	// and used to rank the follow-ups a landing files.
	Verdict runstore.MergeJudge
	// FollowUps is the work a landing files: one entry per finding the merge
	// did not block.
	FollowUps []MergeFollowUp
}

// MergeFollowUp is one finding a landing left open, as the work it files.
type MergeFollowUp struct {
	// Key is the finding's identity across rounds, and the name its item is
	// derived from.
	Key string
	// Severity is what the runner scored it at.
	Severity Severity
	// Importance is the judge's ranking of it, or D2040's rule for a finding
	// the judge said nothing about.
	Importance runstore.MergeJudgeImportance
}

// MergeInput is everything the gate is decided on, and nothing else.
type MergeInput struct {
	Item     WorkItem
	Step     escalation.Step
	Head     string
	Read     MergeReading
	Launches MergeLaunches
	// PR is the pull request as the host answered it, and CI the state of its
	// checks at Head. Both are nil and unknown when there is nothing to read
	// them for.
	PR *gitprov.PRInfo
	// CI is the state of those checks, and CIPending whether any of them is
	// still moving. ChecksState answers CIUnknown for three different readings —
	// nothing reported at all, a check that has not concluded, and a finished
	// check whose conclusion this code cannot read — and the gate waits on one
	// of them (see mergeGate), so the reading beside the state is what tells
	// them apart.
	CI          gitprov.CIState
	CIPending   bool
	ReadyDrafts bool
	// Launchable is whether the ladder may start a run of this item at all:
	// the same four questions every launch is held to (ladderLaunchable), read
	// here because the two launch clauses below are launches.
	Launchable bool
	// Landed is whether this ladder is behind the merge at Head, read from the
	// marker a landing writes before it asks the host to merge (landed), and
	// Said whether the log already carries a line for this head's landing or for
	// its already-merged outcome. Together they are what tells a cycle
	// re-entering a landing whose write was lost from a cycle reading a pull
	// request a person merged while the item stood at the rung (see Replay).
	Landed bool
	Said   bool
}

// mergeGate is D1245's gate for one head, in the order the clauses have to be
// read in: what the head is and whether it is still the head, then the review
// that covers it, then the deterministic evidence, then the judge, then what the
// mode does with a gate that passed.
//
// The order is not cosmetic. The head is settled before anything is read about
// it, because every clause below is about that commit and a clause read on a
// head the pull request has moved past is a fact about a commit nobody will
// merge. The review clause comes before the deterministic one because a rebase
// run is an agent and nothing but a scored review of the head makes acting on
// its record acceptable at all. And the judge comes last of the readings, so a
// head the deterministic gate refuses never spends a model call.
//
// The deterministic clauses read two documents and are careful about which says
// what. The record is the run's: whether it ended, that it pushed this head, and
// that it changed something are facts about the run, and the run is the only
// thing that can answer them. What it changed is not: D1245's Routine clause is
// read on the host's own report of the head's changed paths (MergeReading.Paths)
// and refuses when that read was not made, because a merge that lands unread
// rests on it, and the diff's own account of itself is not corroboration (D2047;
// triage reads the same rule as a reading nothing corroborated).
//
// The findings clauses are read on two readings of the one commit and never on
// one: the run behind the record's, which is what the triage escalates on, and
// the review-only run's, because D2043 lands a rebased head under a scored
// review's severities — a head whose review reported a critical finding, or a
// high one on a security path, is one a person has to read first, whatever the
// run said about itself (headFindings).
func mergeGate(in MergeInput) MergeOutcome {
	switch {
	case !in.Read.Read:
		return waitFact(GateUnreadable)
	case recordableHead(in.Head) == "":
		return waitFact(GateNoHead)
	case in.Read.Number == 0 || in.PR == nil:
		return waitFact(GateNoPullRequest)
	case in.PR.State == gitprov.PRMerged:
		// A merge that already stands. Where the ladder's own landing is behind
		// this head's merge and nothing has said so, the cycle is finishing a
		// landing whose write was lost between the host's merge and the node —
		// the findings the landing did not block are filed and the person is
		// told, which is what D2040 owes and what no later cycle would ever do.
		// What is not readable is not invented: a findings read that failed or
		// that found a severity nothing can place files nothing, and the head
		// still waits for nobody because the merge is the host's fact, not the
		// ladder's.
		if in.Landed && !in.Said && findingsReadable(in.Read) {
			return MergeOutcome{
				Kind:      MergeAlready,
				Replay:    true,
				Verdict:   in.Read.Verdict,
				FollowUps: mergeFollowUps(headFindings(in.Read), in.Read.Verdict),
			}
		}
		return MergeOutcome{Kind: MergeAlready}
	case in.PR.State != gitprov.PROpen:
		return waitFact(GateNotOpen)
	case !gitprov.SameCommit(in.PR.HeadSHA, in.Head):
		return waitFact(GateHeadMoved)
	}
	if !in.Read.Scored {
		if in.Read.ReviewRun == "" {
			if !in.Launches.Review {
				if !in.Launchable {
					return waitFact(GateLaunchUncovered)
				}
				return MergeOutcome{Kind: MergeLaunchReview, Launch: RecipeReviewOnly, Facts: []GateReason{GateUncovered}}
			}
			// A review has been asked for at this head and its run is not in
			// the reading yet: a run's record appears when its run does, and the
			// review that was launched is a cycle or two behind its own line
			// (BeyondLog). That is a wait on something moving.
			return retry(waitFact(GateReviewSpent))
		}
		// The review at this head is recorded and carried no score. D2043 allows
		// one review per head, so no later cycle produces another one, and a
		// head whose review answered nothing is a person's to read.
		return waitFact(GateReviewSpent)
	}
	if reason, unreadable := headFindingsUnreadable(in.Read); unreadable {
		return waitReason(reason)
	}
	rec := in.Read.Record
	if !readableRecord(rec) {
		return waitReason(ReasonRecordUnreadable)
	}
	if !rec.Status.Terminal() {
		return retry(waitFact(GateRunRunning))
	}
	paths := rec.ChangedFiles.Bounded()
	switch {
	case paths == nil:
		return waitReason(ReasonPathsMissing)
	case paths.Truncated:
		return waitReason(ReasonPathsTruncated)
	case len(paths.Paths) == 0 || rec.PushedHead == "":
		return waitReason(ReasonNoCommits)
	case !gitprov.SameCommit(rec.PushedHead, in.Head):
		return waitFact(GateHeadUnpushed)
	}
	if !knownCI(in.CI) {
		if in.CIPending {
			// Checks that have not concluded: the commit's checks have no
			// answer yet, and the next cycle reads what they said.
			return retry(waitReason(ReasonCIUnknown))
		}
		// Nothing was reported at all, or a check that finished concluded
		// something this code cannot read (F25): no cycle reads anything else
		// here, so a person has to look at a head nothing will ever pass.
		return waitReason(ReasonCIUnknown)
	}
	if in.CI != gitprov.CIGreen {
		return waitFact(GateCIRed)
	}
	// The paths D1245 classifies are the host's at this head and not the
	// record's: the record says the run changed something and pushed this
	// commit, which is the run's own business, and the host says what the commit
	// really touched, which is what a merge rests on. A set nobody read is a
	// refusal and not a reading (G5), and one the host could not show whole is
	// the same refusal the triage has a code for.
	host := in.Read.Paths.Bounded()
	switch {
	case host == nil:
		return waitFact(GatePathsUncorroborated)
	case host.Truncated:
		return waitReason(ReasonPathsTruncated)
	case len(host.Paths) == 0:
		return waitReason(ReasonPathsMissing)
	}
	if sensitivity.Classify(host.Paths) != sensitivity.Routine {
		return waitFact(GatePathsNotRoutine)
	}
	findings := headFindings(in.Read)
	if blocksALanding(findings) {
		return waitReason(blockingCode(findings))
	}
	if in.Read.JudgeRun == "" && !in.Launches.Judge {
		if !in.Launchable {
			return waitFact(GateLaunchUncovered)
		}
		return MergeOutcome{Kind: MergeLaunchJudge, Launch: RecipeMergeJudge, Facts: []GateReason{GateJudgeAbsent}}
	}
	if !in.Read.JudgeScored {
		// The judge has been asked about this head and has not answered yet: the
		// run's record appears when its run does, and its verdict when the run
		// has read the head, so the cycle after the launch — and any cycle while
		// the run is going — reads a judge that has not spoken. That is the same
		// wait the review clause above retries, and it must not be read as the
		// veto D2044 makes of an absent verdict: a veto raised here spends the
		// item's one question on a judge that is still reading, and the answer
		// the question was being kept for (D2037) then reaches no person at all.
		return retry(waitFact(GateJudgeSpent))
	}
	if judgeVetoes(in.Read.Verdict) {
		return MergeOutcome{Kind: MergeWait, Facts: []GateReason{GateJudgeVeto}, Verdict: in.Read.Verdict}
	}
	if !escalation.KnownCeilingAction(in.Step.AtCeiling) {
		return vetoed(in, GateCeilingUnset)
	}
	if in.Step.AtCeiling == escalation.CeilingWaitPerson {
		return vetoed(in, GateModeWaits)
	}
	if in.PR.Draft && !in.ReadyDrafts {
		return vetoed(in, GateDraftHeld)
	}
	return MergeOutcome{
		Kind:      MergeLand,
		Ready:     in.PR.Draft,
		Reasons:   []TriageReason{ReasonRunOver, ReasonPullRequestReady, ReasonCommitsPushed, ReasonCIGreen, ReasonPathsRoutine, ReasonFindingsClear},
		Verdict:   in.Read.Verdict,
		FollowUps: mergeFollowUps(findings, in.Read.Verdict),
	}
}

// findingsReadable is the findings clause as the boolean a replay needs: the
// same rule the gate refuses on, read where the refusal is not available
// because the merge has been made already and nothing is left to refuse.
func findingsReadable(r MergeReading) bool {
	_, unreadable := headFindingsUnreadable(r)
	return !unreadable
}

// headFindings is every open finding the gate reads at one head, the review's
// own first: D2043's read of the head and D1245's read of the run behind it are
// two readings of one commit, and both are read because a landing makes the
// commit durable — a critical finding either of them reported is one a person
// has to read before it does.
//
// One entry per key stands in it, and the review's: a key is the finding's
// identity across rounds and the name its follow-up is derived from
// (mergeFollowUpID), so two readings of one finding are one finding, and the
// severity an independent reader put on it is the one kept.
func headFindings(r MergeReading) []Finding {
	out := make([]Finding, 0, len(r.ReviewFindings.List)+len(r.Findings.List))
	seen := make(map[string]bool, cap(out))
	for _, list := range [][]Finding{r.ReviewFindings.List, r.Findings.List} {
		for _, f := range list {
			if f.Key != "" {
				if seen[f.Key] {
					continue
				}
				seen[f.Key] = true
			}
			out = append(out, f)
		}
	}
	return out
}

// headFindingsUnreadable names why a findings read the gate makes at this head
// is not one it can read, or reports that they all are. Every read is required
// and each refuses with its own code: a review nobody could read the findings
// of is not a review that found nothing, and what an unreadable read is not is
// an empty one.
func headFindingsUnreadable(r MergeReading) (TriageReason, bool) {
	if reason, unreadable := unreadableFindings(r.Findings); unreadable {
		return reason, true
	}
	return unreadableFindings(r.ReviewFindings)
}

// judgeVetoes reports whether the judge's answer stops this head. It is D2044's
// rule read on the caller's answer: a verdict the reader could not place — an
// absent one, a failed run's, a late one, a malformed one — is a veto, and so is
// a block. D2031 makes the judge advisory in the other direction only: its yes
// adds nothing the gate did not already have.
func judgeVetoes(v runstore.MergeJudge) bool {
	return v.Unreadable || v.Verdict != runstore.MergeJudgeMergeWithFollowUps
}

// blockingCode names the clause a blocked landing is blocked by, in the codes
// the triage escalates the same two findings on. Whether a landing is blocked is
// blocksALanding's decision and not this function's; this only says which word
// the refusal is recorded under.
func blockingCode(findings []Finding) TriageReason {
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			return ReasonCriticalFinding
		case SeverityHigh:
			if !routinePath(f.Path) {
				return ReasonSecurityHighFinding
			}
		case SeverityMedium, SeverityLow:
		default:
			return ReasonUnknownSeverity
		}
	}
	return ReasonUnknownSeverity
}

// waitFact and waitReason are the two ways a clause refuses: a refusal the
// triage has a code for, and one only the ladder names.
func waitFact(f GateReason) MergeOutcome {
	return MergeOutcome{Kind: MergeWait, Facts: []GateReason{f}}
}

func waitReason(r TriageReason) MergeOutcome {
	return MergeOutcome{Kind: MergeWait, Reasons: []TriageReason{r}}
}

// retry marks a wait as one on something that is still moving, which is what
// keeps it away from a person (MergeOutcome.Retry). It wraps the two above so a
// clause reads as the thing it refuses on, and whether anybody is asked is
// decided in one place rather than at each clause.
//
// What it says is that the reading may be different next cycle, and not that it
// will be: a reading can look like movement for ever, so the cycles that retry
// one are bounded (boundRetry) and the wait becomes a person's once the bound is
// passed.
func retry(out MergeOutcome) MergeOutcome {
	out.Retry = true
	return out
}

// mergeRetryWindow is how long a rung 3 retries a wait on a reading that looks
// like it is still moving before it stops believing it (MergeOutcome.Retry,
// boundRetry).
//
// A wait is retried because the reading may be different next cycle, and that is
// true only while something really is moving. Some readings look like movement
// and never move: a commit status a dead job left at "pending" never concludes, a
// launcher that took a request whose job never wrote a record leaves a review
// launched and never recorded, and a run whose job died leaves a record that
// never reaches a terminal status. Retrying those asks nobody and names no item,
// and nothing else ends them: the cycles that retry one are cycles in flight and
// not empty ones, so the breaker does not reach them either (outcome,
// Report.Waited) — that pause is the daemon's answer to a loop that spins, and
// this is a daemon waiting on a reading. So the wait is bounded in time instead,
// and the item's own question is what a person gets when the bound is past.
//
// The window is measured from the first cycle that retried the wait at the head
// under judgement, which is what the line mergeRetryTransition names records —
// not from the item's arrival at the rung, and not from the head's age, because
// neither is the wait a person is being told about. Six hours is longer than any
// one run may last under a repository's own timeouts, so a real wait (checks
// running, a run that has not ended, a review that is a cycle or two behind its
// launch) is never cut short by it, and it is far short of for ever, which is the
// direction the alternative fails in.
const mergeRetryWindow = 6 * time.Hour

// boundRetry is the bound on a retried wait: it stamps the cycle that first found
// a wait at this head looking like movement, and past mergeRetryWindow it answers
// the same outcome with Retry cleared — which is what makes the item's question
// reachable, since merge asks a person for every wait that is not retried.
//
// The stamp is a line of its own and not the wait's own line, because the wait's
// line is rewritten every cycle with the reading that cycle made and a log keeps
// the latest write under an id: its stamp is the last cycle's, and a bound read
// off it would never be reached. The line written here is written once, so the
// stamp it carries is when the waiting began and does not move.
func (s BeyondSeams) boundRetry(ctx context.Context, item WorkItem, head string, out MergeOutcome, now time.Time) (MergeOutcome, error) {
	id := EntryID(item.ID, mergeRetryTransition(head))
	began, ok, err := s.Log.LoggedAt(ctx, id)
	switch {
	case err != nil:
		return out, err
	case !ok:
		return out, s.Log.Append(ctx, Entry{
			Subject:    item.ID,
			Transition: mergeRetryTransition(head),
			Text:       "escalation: " + mergeRetryPhase + ": " + mergeLine(out),
		})
	case now.Sub(began) > mergeRetryWindow:
		out.Retry = false
	}
	return out, nil
}

// mergeRetryPhase is the word the bound on a retried wait is recorded under.
const mergeRetryPhase = "retry"

// mergeRetryTransition is the audit id's transition half for that line, under
// the head it names for the same reason an outcome line is: what is bounded is a
// wait at one head, and a head that moves is a wait of its own.
//
// It is deliberately not one of MergeKind's outcomes. The gate has reached
// nothing by writing it — the wait it stamps is still a wait, and the cycle goes
// on retrying it — and a reader that took it for an outcome would read a cycle
// that decided something.
func mergeRetryTransition(head string) string {
	name := "ladder-r3-" + mergeRetryPhase
	if h := recordableHead(head); h != "" {
		name += "-" + h[:min(len(h), questionHashHex)]
	}
	return name
}

// vetoed is a refusal taken with the judge's verdict in hand: the verdict is the
// reader's, so the verdict dangles only while there is no judge to have answered
// — a malformed one that is present is still a veto, and judgeVetoes has already
// refused it by the time anything here is reached. Keeping it beside these three
// clauses is what D2037 asks for: whoever reads the question reads the judge.
func vetoed(in MergeInput, f GateReason) MergeOutcome {
	out := waitFact(f)
	out.Verdict = in.Read.Verdict
	return out
}

// merged is a landing a host would not take: it is a person's question, with the
// evidence the gate passed on kept beside the refusal — the gate's reading is
// still what the daemon knows, and it is the host that would not act on it — and
// with nothing filed, because no landing happened.
func merged(out MergeOutcome, code string) MergeOutcome {
	return MergeOutcome{
		Kind:    MergeWait,
		Reasons: out.Reasons,
		Facts:   []GateReason{GateMergeRefused},
		Refusal: code,
		Verdict: out.Verdict,
	}
}

// merge is rung 3 for one item: read, decide, act, write, and say.
//
// The reading and the host are read before anything is written, because a hook
// that acted on half a reading would be the guess the gate refuses. Every write
// after that is one Apply of one op list — the ledger, the question or the
// follow-ups — and then the line, which is the order E10a uses and the one a
// re-run can repeat: the state write is what a second cycle sees, and the line
// is written under a name derived from the head and the step. The one write
// after the line is the stop a merge leaves behind (see the end of the
// function): it is the write that ends this hook's own recoverability, so it
// comes once everything a cycle after it would have to redo has landed.
//
// A wait the gate reads as movement writes one line before any of that: the
// stamp the bound on retrying it is read from (boundRetry). It is written first
// because it is about the reading and not about the outcome — a cycle whose op
// list or outcome line fails after it still leaves the wait's age recorded — and
// because it is written once, so a cycle that finds it already there reads the
// age off it rather than writing a stamp of its own.
func (s BeyondSeams) merge(ctx context.Context, item WorkItem, step escalation.Step, reason escalation.Reason, read LadderReading) (LadderAct, error) {
	now, ok := s.Now()
	if !ok {
		return LadderIdle, ErrNoServerTime
	}
	st, err := s.Node.State(ctx)
	if err != nil {
		return LadderIdle, err
	}
	if cur, ok := st.Item(item.ID); ok {
		item = cur
	}
	launches, err := s.launches(ctx, item.ID, read.HeadSHA)
	if err != nil {
		return LadderIdle, err
	}
	said, err := s.said(ctx, item.ID, read.HeadSHA)
	if err != nil {
		return LadderIdle, err
	}
	mr, err := s.Read.MergeAt(ctx, item, read.HeadSHA)
	if err != nil {
		return LadderIdle, err
	}
	landed, err := s.landed(ctx, item.ID, read.HeadSHA)
	if err != nil {
		return LadderIdle, err
	}
	in := MergeInput{
		Item: item, Step: step, Head: read.HeadSHA, Read: mr, Launches: launches,
		ReadyDrafts: s.ReadyDrafts, Landed: landed, Said: said,
		// The hook reads one node's items (MergeNode) and no other
		// orchestrator's, so it holds no reading of a cross reference to hand
		// here (D3208): a reference it cannot resolve holds the item, which is
		// the direction a launch fails in, and the item is left to a cycle whose
		// own node can resolve it.
		Launchable: ladderLaunchable(st, nil, item),
	}
	if mr.Read && mr.Number != 0 {
		// The pull request and its checks are read at the head under
		// judgement, not at whatever the pull request is at now: the two calls
		// are the whole of what the gate is decided on besides the record, and
		// a host that will not answer ends the turn for the next cycle to
		// retry — a read that did not happen is not a refusal to merge.
		pr, err := s.Host.PullRequest(ctx, mr.Number)
		if err != nil {
			return LadderIdle, err
		}
		checks, err := s.Host.Checks(ctx, mr.Number, in.Head)
		if err != nil {
			return LadderIdle, err
		}
		in.PR, in.CI, in.CIPending = &pr, gitprov.ChecksState(checks), gitprov.ChecksPending(checks)
	}
	out := mergeGate(in)
	if out.Retry {
		// A wait the gate reads as movement is retried, and a reading that never
		// moves has to stop looking like one: the bound is what turns it into the
		// item's question (boundRetry, mergeRetryWindow).
		out, err = s.boundRetry(ctx, item, in.Head, out, now)
		if err != nil {
			return LadderIdle, err
		}
	}

	runID, launched := item.RunID, false
	var told *Notification
	switch out.Kind {
	case MergeLaunchReview, MergeLaunchJudge:
		id, err := s.launch(ctx, now, item, step, out, in.Head)
		if err != nil {
			return LadderIdle, err
		}
		runID, launched = id, true
	case MergeLand:
		// The marker goes down before the host is asked for anything, which is
		// what makes it the record of a landing whose outcome line never
		// reached the log: a cycle that finds this head's pull request merged
		// reads the marker and finishes the landing's work, where the ledger it
		// replaced cannot tell this ladder's merge from a person's (see landed).
		if err := s.Log.Append(ctx, Entry{
			Subject:    item.ID,
			Transition: mergeLandingTransition(in.Head),
			Text:       "escalation: " + mergeLandingPhase,
		}); err != nil {
			return LadderIdle, err
		}
		code, err := s.land(ctx, in)
		if err != nil {
			return LadderIdle, err
		}
		if code != "" {
			out = merged(out, code)
		}
	}

	moved := ladderReached(item) != step.Rung
	var ops []Op
	if op, ok := mergeLedgerOp(item, step, reason, in.Head, runID, launched, moved, now); ok {
		ops = append(ops, op)
	}
	switch out.Kind {
	case MergeLand, MergeAlready:
		// A landing files what it did not block (D2040), and a replay files the
		// same work for a merge this cycle did not make: the findings are open
		// either way, and mergeFollowUpOps files an item only where the node
		// does not already carry it, so the window between the host's merge and
		// this write is safe to re-run.
		ops = append(ops, mergeFollowUpOps(st, item, out)...)
	case MergeWait:
		// A wait on something still moving asks nobody (MergeOutcome.Retry): the
		// item has one question and it is asked once, so a question raised for a
		// run that has not ended or checks that have not concluded would spend it
		// — the judge's later veto (D2037) would reach no person, and the stale
		// question would sit open over a landing nobody is waiting on. The wait
		// is retried next cycle instead, and the question is kept for the
		// refusal that is a person's: the one read off a reading that has
		// stopped moving, which is the reading no later cycle changes.
		//
		// A retried wait is one the gate read as movement, and movement that never
		// arrives is what boundRetry bounds: past mergeRetryWindow the wait stops
		// being retried and is a person's like any other refusal, which is what
		// keeps a stuck reading from cycling for ever and naming nobody. Nothing
		// else ends such a wait: the cycles that retry one read as in flight and
		// not as cycles that did nothing, so the breaker is not what reaches them
		// (outcome, Report.Waited).
		if !out.Retry {
			if op, n, ok := mergeQuestionOp(st, item, mergeQuestion(item, out)); ok {
				ops, told = append(ops, op), &n
			}
		}
	}
	if len(ops) > 0 {
		if err := s.Node.Apply(ctx, ops); err != nil {
			return LadderIdle, err
		}
	}
	line := "escalation: " + ladderMove(step, reason, runID) + " " + mergeLine(out)
	entry := Entry{Subject: item.ID, Transition: mergeTransition(step.Rung, out.Kind, in.Head), Text: line}
	if err := s.Log.Append(ctx, entry); err != nil {
		return LadderIdle, err
	}
	if moved {
		// One comment per move and not one per cycle: the move is what the
		// pull request is told about, and a rung 3 that re-enters this hook
		// every cycle would otherwise post the same sentence until the head
		// moved. The marker E8 reads back names the run the move started, or
		// the item's own run where the move started none.
		if err := s.Comment.LadderComment(ctx, item, ladderComment(step, ladderMove(step, reason, runID), runID)); err != nil {
			s.warn(err)
		}
	}
	switch {
	case (out.Kind == MergeLand || out.Replay) && step.AtCeiling == escalation.CeilingAutoMergeNotify:
		// The ceiling decides who is told, and a replay is a landing the person
		// was never told about: the cycle that merged it did not reach the
		// notification. What a replay says is the state of things rather than
		// this cycle's act — the host holds the merge, and the findings are
		// being filed now — and it is said only where the ladder's own landing
		// is behind the merge, so a pull request nobody's ladder touched is
		// never reported as one this ladder merged.
		text := mergeSaid(item, out)
		if out.Replay {
			text = mergeAlreadySaid(item, out)
		}
		s.say(ctx, Notification{Kind: NotifyMergedNeedsReview, Subject: item.ID, Text: text})
	case told != nil:
		s.say(ctx, *told)
	}
	if out.Kind == MergeLand || out.Kind == MergeAlready {
		// The item is over: the merge at its head is a fact of the host's, and
		// the ladder has nothing left to do for an item whose work has landed —
		// or for one it finds landed, whether the landing was its own replay's
		// or a person's.
		//
		// Without this the item is walked for ever: the ledger keeps it at rung
		// 3 (a rung passed is a rung the item has spent, and neither the
		// decision nor this file walks back off it), escalation.Decide answers an
		// item at rung 3 with rung 3, and ladderReentrant enters that rung every
		// cycle because the sequence is one — so the hook reads the same merged
		// pull request on every tick of the daemon's life, polling the host and
		// holding the breaker's streak at progress (D1228). Cancelled and not
		// done because the state's table has no failed or blocked item reaching
		// done (state.go's CanMoveTo, the spec's own table): queued is the only
		// other phase it allows from there, and that one would hand the merged
		// work back to the item's own loop.
		//
		// A write of its own, and the last of the three, because stopping the
		// item is what ends the landing's recoverability: the marker this head
		// carries is what a cycle after a lost outcome line finishes the landing
		// from (landed, Replay), and that cycle is a cycle this hook is entered
		// on — an item already stopped is one the ladder reads no further rungs
		// for. So the line, the comment and the person a ceiling tells all land
		// first, and the write that cannot be taken back comes after them.
		if err := s.Node.Apply(ctx, []Op{cancelItemOp(item.ID)}); err != nil {
			return LadderIdle, err
		}
	}
	// What this cycle moved, read off the outcome rather than off the writes:
	// the two launches are a run started, the landing is the merge itself, and
	// a replay is the work a landing whose write was lost still owed. Everything
	// else the gate reaches — a wait, a veto, a launch it may not make, a
	// refusal a host answered — leaves the item where it stands and is the next
	// cycle's, and a merge this ladder did not make with nothing left to file
	// moved nothing at all.
	switch {
	case launched || out.Kind == MergeLand || out.Replay:
		return LadderMoved, nil
	case out.Kind == MergeWait && out.Retry:
		// The wait the gate retried, read off the outcome the bound left: a
		// wait that has passed mergeRetryWindow is no longer retried, asks the
		// item's question above, and is an idle cycle here like every other
		// refusal a person has to read.
		return LadderWaiting, nil
	}
	return LadderIdle, nil
}

// launch starts one rung 3 run and answers the id it was minted under. The id is
// minted here and committed nowhere before the launch, exactly as the rungs
// below do it: what records the launch is the transition this cycle writes once
// the start has come back, and the window in between is the log's line, which
// the launch writes through the seam it was started through.
func (s BeyondSeams) launch(ctx context.Context, now time.Time, item WorkItem, step escalation.Step, out MergeOutcome, head string) (string, error) {
	runID, err := s.Mint(now)
	if err != nil {
		return "", err
	}
	req := LadderRequest{Item: item, RunID: runID, Recipe: out.Launch, Rung: step.Rung, HeadSHA: head}
	if err := s.Launch.LaunchLadder(ctx, req); err != nil {
		return "", err
	}
	return runID, nil
}

// land is the landing itself, at the head the gate judged and no other: the
// ready call D2041 asks for first — a draft the ladder merged is one no person's
// review ever saw — and then the merge, both bound to the head as the provider
// demands.
//
// What comes back is the closed code of a refusal, and an error only for an
// outage. A refusal is a person's to act on: the head moved under the call, the
// provider's merge cannot be bound to an expected head at all, it holds no merge
// credential, or the pull request is no longer open (mergeRefusalCode). A host
// that did not answer is the next cycle's to retry.
func (s BeyondSeams) land(ctx context.Context, in MergeInput) (string, error) {
	if in.PR.Draft {
		if err := s.Host.MarkReady(ctx, in.PR.Number, in.Head); err != nil {
			if code := mergeRefusalCode(err); code != "" {
				return code, nil
			}
			return "", err
		}
	}
	if err := s.Host.Merge(ctx, in.PR.Number, in.Head); err != nil {
		if code := mergeRefusalCode(err); code != "" {
			return code, nil
		}
		return "", err
	}
	return "", nil
}

// launches is whether a rung 3 run has already been asked for at this head, read
// from the log by the audit id of its own line.
func (s BeyondSeams) launches(ctx context.Context, itemID, head string) (MergeLaunches, error) {
	if recordableHead(head) == "" {
		return MergeLaunches{}, nil
	}
	review, err := s.Log.Logged(ctx, EntryID(itemID, mergeTransition(escalation.R3, MergeLaunchReview, head)))
	if err != nil {
		return MergeLaunches{}, err
	}
	judge, err := s.Log.Logged(ctx, EntryID(itemID, mergeTransition(escalation.R3, MergeLaunchJudge, head)))
	if err != nil {
		return MergeLaunches{}, err
	}
	return MergeLaunches{Review: review, Judge: judge}, nil
}

// said is whether the log already carries something about this head being
// merged: the line the landing wrote, or the one an earlier cycle wrote for the
// already-merged outcome. It is what keeps a replay from being repeated — the
// cycle that made the landing writes the first, and the cycle that finished a
// lost one writes the second, so every cycle after either of them finds this
// true and does nothing.
func (s BeyondSeams) said(ctx context.Context, itemID, head string) (bool, error) {
	if recordableHead(head) == "" {
		return false, nil
	}
	landed, err := s.Log.Logged(ctx, EntryID(itemID, mergeTransition(escalation.R3, MergeLand, head)))
	if err != nil {
		return false, err
	}
	if landed {
		return true, nil
	}
	return s.Log.Logged(ctx, EntryID(itemID, mergeTransition(escalation.R3, MergeAlready, head)))
}

// landed is whether this ladder's own landing is behind a pull request the host
// reports merged at this head, read from the marker a landing writes before it
// asks the host for anything (mergeLandingTransition). It is a log line and not
// a fact of the item's ledger because the ledger cannot answer this question:
// the transition that put the item at rung 3 names the head it was made at and
// says nothing about who merged, so it reads the same whether the ladder merged
// that head, or the gate refused it and a person merged it by hand afterwards.
// Nor is the ledger's head always the head under judgement — the gate compares
// the host's head with the head it read — so a landing at a later head whose
// line was lost is one only the marker points at.
//
// The write happens before the merge, so a head the host refused also leaves a
// marker standing, and a person who then merges that head themselves is read as
// finishing what this ladder started. That is the direction to fail in: the
// work a landing owes is filed and the person is told, where the other reading
// loses D2040's follow-ups to a merge that really happened.
func (s BeyondSeams) landed(ctx context.Context, itemID, head string) (bool, error) {
	if recordableHead(head) == "" {
		return false, nil
	}
	return s.Log.Logged(ctx, EntryID(itemID, mergeLandingTransition(head)))
}

// mergeLandingPhase is the word the marker is recorded and written under.
const mergeLandingPhase = "landing"

// mergeLandingTransition is the audit id's transition half for the marker, under
// the head it names for the same reason an outcome line is: one marker per head,
// so a retry of the same landing rewrites the key rather than adding a line, and
// a second head is a marker of its own.
//
// It is deliberately not one of MergeKind's outcomes. The gate has reached
// nothing by writing it — the host has not been asked yet — and a reader that
// took it for an outcome would read a landing that may never have happened.
func mergeLandingTransition(head string) string {
	name := "ladder-r3-" + mergeLandingPhase
	if h := recordableHead(head); h != "" {
		name += "-" + h[:min(len(h), questionHashHex)]
	}
	return name
}

// mergeLedgerOp is the daemon's own write of the move: the rung, the count of
// the runs the ladder has spent and the transition that says why (G12, D2042).
//
// The transition is written when the rung moves and never twice at one rung. A
// hook that re-enters rung 3 every cycle while the item is already there is a
// hook re-reading a head, not a move: a transition per cycle would fill the
// ledger MaxEscalationTransitions bounds, and the reason a cycle has to give
// would be one the decision was never asked for — escalation.Decide answers an
// item already at its rung with no reason at all, and the state refuses a
// transition that names none.
//
// The count moves with a launch, which is the only thing a rung 3 spends, and it
// is left as it stands at the validator's own cap rather than written over it:
// the ladder spends six runs at most, so a ledger at that cap was written by
// something else, and a cycle that kept adding one would fail on the write every
// time instead of leaving the item to a person who can read it.
//
// The whole block is replaced rather than patched, so the two names a reader
// outside the daemon filters and joins on — the item's repository and the run
// this move was about — are written here from the item and the move, exactly as
// ladderRecord writes them: read off the block they stand on instead, a move
// would empty them, and the item would drop out of its repository's digest with
// the chain's run behind it.
func mergeLedgerOp(item WorkItem, step escalation.Step, reason escalation.Reason, head, runID string, launched, moved bool, now time.Time) (Op, bool) {
	if !moved && !launched {
		return Op{}, false
	}
	held := Escalation{}
	if item.Escalation != nil {
		held = *item.Escalation
	}
	next := Escalation{
		Repo:          item.Repo,
		RunID:         runID,
		Rung:          held.Rung,
		EnteredAt:     held.EnteredAt,
		OuterAttempts: held.OuterAttempts,
		Transitions:   append([]Transition{}, held.Transitions...),
	}
	if moved {
		next.Rung = step.Rung
		next.EnteredAt = now
		next.Transitions = append(next.Transitions, Transition{
			From: held.Rung, To: step.Rung, Reason: reason, At: now, HeadSHA: recordableHead(head),
		})
	}
	if launched && held.OuterAttempts < MaxOuterAttempts {
		next.OuterAttempts++
	}
	return Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{ID: item.ID, Escalation: &next}}, true
}

// mergeQuestionOp is the one question a rung 3 owes a person, under the name
// ladderQuestionID derives from the item: a decision whose id the node already
// carries is the question already asked, and a cycle that re-runs after its
// write landed asks nothing more. Nothing new is asked while a rung 3 waits
// either — the answer is what moves the item, and the ladder's reading of the
// next cycle is what it acts on — and a wait on something still moving is not
// asked about in the first place (merge, MergeOutcome.Retry), nor after the
// bound on one is passed (boundRetry).
func mergeQuestionOp(st State, item WorkItem, question string) (Op, Notification, bool) {
	id := ladderQuestionID(item.ID)
	if _, ok := st.Decision(id); ok {
		return Op{}, Notification{}, false
	}
	op := Op{Op: OpDecisionAdd, Decision: &DecisionOp{ID: id, Question: question, WaitingOn: OnPerson}}
	return op, Notification{Kind: NotifyDecisionNeeded, Subject: id, Text: question}, true
}

// mergeQuestion is what a person is asked about a rung 3 the ladder will not
// land by itself (D1247): the outcome and the clauses it was read on, and the
// judge's own words where there are any — fenced, cleaned and clipped, because
// they are a record's text and not this program's.
func mergeQuestion(item WorkItem, out MergeOutcome) string {
	text := "The escalation ladder will not merge the pull request of " + item.ID + " by itself (" + mergeLine(out) + ")."
	if verdict := mergeVerdictText(out.Verdict); verdict != "" {
		text += " The merge judge said:\n" + verdict + "\n"
	}
	if out.Refusal != "" {
		return text + " The host would not take the merge, so the pull request is still open: read it and decide what happens to it."
	}
	return text + " Read the pull request and decide what happens to it."
}

// mergeSaid is what a Pro or Hotfix landing tells a person (D2030): that the
// ladder merged under its gate, and how much work the landing left behind. It is
// this program's own words and a count, and nothing the judge wrote.
func mergeSaid(item WorkItem, out MergeOutcome) string {
	text := "the ladder merged the pull request of " + item.ID + " under the merge gate"
	switch n := len(out.FollowUps); {
	case n == 1:
		text += ", leaving one finding for a follow-up"
	case n > 1:
		text += ", leaving " + strconv.Itoa(n) + " findings for follow-ups"
	}
	return text + "."
}

// mergeAlreadySaid is what a Pro or Hotfix cycle tells a person about a landing
// it found already made: that the merge is in the host, that this ladder's own
// landing is behind it, and how much the landing left open. It claims no merge
// this cycle did not make, because the cycle that made it is not the one saying
// this.
func mergeAlreadySaid(item WorkItem, out MergeOutcome) string {
	text := "the pull request of " + item.ID + " is already merged, and the ladder's rung 3 was on it at this head"
	switch n := len(out.FollowUps); {
	case n == 1:
		text += "; one finding the landing did not block is filed as a follow-up"
	case n > 1:
		text += "; " + strconv.Itoa(n) + " findings the landing did not block are filed as follow-ups"
	}
	return text + "."
}

// mergeLine is one outcome as one line of closed codes: the outcome, the clauses
// the gate read and the refusal a host gave where there was one. Nothing here is
// a sentence, a path, a finding or a model (G8), which is what lets the same
// line go into the log and onto the pull request.
func mergeLine(out MergeOutcome) string {
	codes := make([]string, 0, len(out.Reasons)+len(out.Facts)+1)
	for _, r := range out.Reasons {
		codes = append(codes, string(r))
	}
	for _, f := range out.Facts {
		codes = append(codes, string(f))
	}
	if out.Refusal != "" {
		codes = append(codes, out.Refusal)
	}
	line := string(out.Kind)
	if len(codes) > 0 {
		line += ": " + strings.Join(codes, " ")
	}
	return line
}

// mergeTransition is the audit id's transition half for one outcome at one head:
// the rung, what the outcome was, and the head it was read at. The head is what
// makes a rebased head a line of its own rather than a rewrite of the one before
// it, and what keeps a cycle that re-reads the same head from appending a second
// line beside the first (G4).
func mergeTransition(rung escalation.Rung, kind MergeKind, head string) string {
	name := "ladder-" + mergePhase(rung, kind)
	if h := recordableHead(head); h != "" {
		name += "-" + h[:min(len(h), questionHashHex)]
	}
	return name
}

// mergePhase is the word one outcome is recorded under, prefixed by the rung it
// belongs to: rung 3 has four steps a person reads apart — the review it
// launched, the judge it launched, the merge it made and the wait it took — and
// rung 4 has its one.
func mergePhase(rung escalation.Rung, kind MergeKind) string {
	prefix := "r3"
	if rung == escalation.R4 {
		prefix = "r4"
	}
	switch kind {
	case MergeLaunchReview:
		return prefix + "-review"
	case MergeLaunchJudge:
		return prefix + "-judge"
	case MergeLand:
		return prefix + "-land"
	case MergeWait:
		return prefix + "-wait"
	case MergeAlready:
		return prefix + "-merged"
	case MergeSplit:
		return prefix + "-split"
	}
	return prefix + "-none"
}

// mergeFollowUps is the list a landing files: every finding the merge did not
// block, with the importance the judge gave it, or D2040's rule where the judge
// said nothing (D2040, D2031).
//
// The judge's list is keyed by the finding's own key, which is what matches the
// two readings of one finding; a key the judge named that is malformed, or that
// names no open finding, is dropped — the verdict is a model's answer about a
// brief, and the brief's findings are what is open.
func mergeFollowUps(findings []Finding, v runstore.MergeJudge) []MergeFollowUp {
	given := map[string]runstore.MergeJudgeImportance{}
	for _, f := range v.FollowUps {
		if runstore.ValidFindingKey(f.Key) && importedImportance(f.Importance) {
			given[f.Key] = f.Importance
		}
	}
	out := make([]MergeFollowUp, 0, len(findings))
	for _, f := range findings {
		imp, ok := given[f.Key]
		if !ok {
			imp = mergeImportance(f.Severity)
		}
		out = append(out, MergeFollowUp{Key: f.Key, Severity: f.Severity, Importance: imp})
	}
	return out
}

// importedImportance reports whether i is one of D2040's two words. The
// validator's own predicate is package-private to runstore, and a word outside
// the set is one no reader wrote.
func importedImportance(i runstore.MergeJudgeImportance) bool {
	return i == runstore.MergeJudgeImportant || i == runstore.MergeJudgeMinor
}

// mergeImportance is D2040's rule for a finding the judge did not rank: the two
// severities a person has to look at go ahead of new work, and the other two
// into the ordinary queue. A severity outside the vocabulary cannot be ranked
// down by a silence, so it is filed important.
func mergeImportance(s Severity) runstore.MergeJudgeImportance {
	switch s {
	case SeverityMedium, SeverityLow:
		return runstore.MergeJudgeMinor
	}
	return runstore.MergeJudgeImportant
}

// mergeFollowUpOps is the work a landing leaves behind: one queued item per
// finding the merge did not block, each waiting for a person's approval.
//
// The items are smaller work of the merged item and never a re-run of it. They
// are filed through newItemOp, which copies the repo, the recipe and the tier and
// hands on no spec — and no spec is the whole of D1246's structural half: a
// follow-up of an escalation cannot relaunch the spec the ladder just decided
// about. An item whose name the node already carries is not filed again, which is
// what makes a cycle that re-runs after a committed write add nothing.
func mergeFollowUpOps(st State, item WorkItem, out MergeOutcome) []Op {
	var ops []Op
	for i, f := range out.FollowUps {
		id := mergeFollowUpID(item.ID, f, i)
		if _, ok := st.Item(id); ok {
			continue
		}
		ops = append(ops, newItemOp(id, mergeFollowUpTitle(item), mergeFollowUpBrief(f), item))
	}
	return ops
}

// mergeFollowUpID is the name one follow-up is written under: the finding's own
// key where the runner wrote one this tree accepts, and its place in the list
// where it did not. Two cycles of the same head file the same names, and a
// finding is the same item across the rounds it survives.
func mergeFollowUpID(itemID string, f MergeFollowUp, i int) string {
	if runstore.ValidFindingKey(f.Key) {
		return mergeName(itemID, "-fu-"+f.Key[:16])
	}
	return mergeName(itemID, "-fu-"+strconv.Itoa(i+1))
}

func mergeFollowUpTitle(item WorkItem) string {
	return "a finding left open by " + item.Title
}

// mergeFollowUpBrief is what the person who picks the item up reads: the
// finding's severity, its importance and its key, and the sentence that says the
// item waits for an approval. The severity and the importance are closed words —
// the runner's and the judge's — and the key is a digest, so nothing here is a
// finding's own text.
func mergeFollowUpBrief(f MergeFollowUp) string {
	key := "none recorded"
	if runstore.ValidFindingKey(f.Key) {
		key = f.Key
	}
	return "The escalation ladder merged the pull request of this work with a finding still open: " +
		string(f.Severity) + ", filed as " + string(f.Importance) + " (finding " + key + "). " +
		"Read the finding and decide what to do about it; this item waits for a person's approval."
}

// mergeVerdictText is the judge's answer as a person reads it: its own reason
// and the fundamental issue it named, which are two fields of a record and not
// this program's words.
func mergeVerdictText(v runstore.MergeJudge) string {
	text := strings.TrimSpace(v.Reason)
	if blocker := strings.TrimSpace(v.Blocker); blocker != "" {
		if text != "" {
			text += "\n\n"
		}
		text += "blocker: " + blocker
	}
	if text == "" {
		return ""
	}
	return Fenced(text, maxJudgeFenceRunes)
}

// Fenced is a value out of a record as a block of a sentence this program
// writes. safetext drops the controls, escape sequences, bidi and zero-width
// characters — and never a backtick: a value that carried the fence's own three
// would end the block and write the words after it as this program's, so the
// fence is opened one backtick longer than any run of them inside.
//
// It is exported because more than one program writes such a block and the rule
// is the same one: the judge's verdict and a split's scope here, and the ticket
// an issue item came from in the brief of the run a launcher starts
// (internal/cli, B9).
func Fenced(text string, room int) string {
	body := safetext.Clip(safetext.MultiLine(text), room)
	fence := "```"
	for strings.Contains(body, fence) {
		fence += "`"
	}
	return fence + "\n" + body + "\n" + fence
}

// mergeName is one name derived from an item's id and a suffix: the two joined
// while they fit the 64 characters a name may be (idRE), and otherwise the joined
// name cut to fit with a digest of the whole of it behind. It is the rule
// ladderQuestionID states, for the names rungs 3 and 4 derive — a follow-up's,
// the split's — because the ladder's own question already owns its name and the
// two must not collide.
func mergeName(itemID, suffix string) string {
	name := itemID + suffix
	if len(name) <= maxNameRunes {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	tail := "-" + hex.EncodeToString(sum[:])[:questionHashHex]
	return name[:maxNameRunes-len(tail)] + tail
}

const (
	// maxJudgeFenceRunes caps the judge's own words a question carries. A
	// question is a glance at a pull request, and a longer reason is a document
	// the run's record already holds.
	maxJudgeFenceRunes = 240
)

// recordableHead is a head as the ledger may carry one: a git object name is
// lowercase hex of 7 to 64 characters (headRE). A rung that judged a head this
// cannot record still records the rung — the transition is why the item moved,
// and a head no reader can place is left out of it rather than refusing the
// write the ladder needs.
func recordableHead(h string) string {
	h = strings.ToLower(h)
	if !headRE.MatchString(h) {
		return ""
	}
	return h
}

// say hands one notification to the sink, and a sink that will not take it is
// reported and never fails the cycle: what a cycle must not lose is the write
// behind the notification.
func (s BeyondSeams) say(ctx context.Context, n Notification) {
	if s.Notify == nil {
		return
	}
	if err := s.Notify.Notify(ctx, n); err != nil {
		s.warn(err)
	}
}

func (s BeyondSeams) warn(err error) {
	if err != nil && s.Warn != nil {
		s.Warn(err)
	}
}
