package orchestrator_test

import (
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// The ladder tests (E11) plan over the same clock, caps and items the launcher
// tests do (launch_test.go): what is new here is the follow-up a person filed
// the item from, which the daemon reads beside the runs (RunView.FollowUps),
// and the spend a rung's launch is bounded by.

// follower is the view with one item's follow-up in it. A view that says
// nothing about an item is a view of an item that is not a follow-up at all,
// which is what a new work item is.
func follower(id string, f orchestrator.FollowUp) orchestrator.RunView {
	return orchestrator.RunView{FollowUps: map[string]orchestrator.FollowUp{id: f}}
}

// finding is a scored finding in the shape the reviewer writes one.
func finding(l, i, f int, c rigor.Class) rigor.Score {
	return rigor.Score{Likelihood: l, Impact: i, FixCost: f, Class: c}
}

// atRung1 is the ledger of an item the ladder has walked to rung 1 — the
// ladder's floor, where the item goes back to its normal recipe loop — with the
// outer runs the executor has already spent on it.
func atRung1(now time.Time, outer int) *orchestrator.Escalation {
	e := ledger(escalation.R1, outer, step("", escalation.R1, escalation.ReasonAge, now.Add(-2*time.Hour), ""))
	return &e
}

// twoAttempts is the intents of an item whose approval covers two attempts and
// spent both: the first attempt and the retry it covered, each one a run of the
// spec the approval was given over.
func twoAttempts(id string, approvalAt, at time.Time, w orchestrator.WorkItem) []orchestrator.Intent {
	digest := orchestrator.SpecDigest(w)
	return []orchestrator.Intent{
		{
			ID:         orchestrator.IntentID(id, 1),
			RunID:      "20261107-090000-aaaa",
			SpecDigest: digest,
			ApprovalAt: approvalAt.UTC(),
			At:         at.UTC(),
			Outcome:    orchestrator.OutcomeFailed,
		},
		{
			ID:         orchestrator.IntentID(id, 2),
			RunID:      "20261107-100000-bbbb",
			SpecDigest: digest,
			ApprovalAt: approvalAt.UTC(),
			At:         at.Add(time.Minute).UTC(),
			Outcome:    orchestrator.OutcomeFailed,
		},
	}
}

// TestImportanceNeverApproves is G6 and F21: importance sorts and notifies, and
// it approves nothing. An item no person has approved is one the launcher holds
// nothing for however important the follow-up behind it is — the approval is
// the only road in — and the same item approved is launched, so what the test
// pins is the approval and not an importance the launcher failed to read.
//
// Mutation: let an important follow-up stand in for a standing approval (plan
// the launch when Approved is nil and the reading is important) and the first
// plan below holds a launch.
func TestImportanceNeverApproves(t *testing.T) {
	now := o6Now()
	at := now.Add(-time.Hour)
	security := orchestrator.FollowUp{
		Importance: task.ImportanceImportant,
		Scores:     []rigor.Score{finding(1, 5, 4, rigor.Security)},
	}
	pending := workItemOf("wi-1")
	view := follower("wi-1", security)

	s := o6Node(t, now, pending)
	if got := orchestrator.Plan(s, view, o6Caps(), now); len(got) != 0 {
		t.Fatalf("the plan holds %+v for an item nobody approved, and importance is not an approval", got)
	}
	queue := orchestrator.Queue(s, view)
	if len(queue) != 1 || queue[0].ID != "wi-1" {
		t.Fatalf("the person's queue is %+v, and the important follow-up is one of the items waiting on them", queue)
	}

	s = o6Node(t, now, o6ApprovedBy(pending, at))
	a := onlyAction(t, orchestrator.Plan(s, view, o6Caps(), now))
	if a.Kind != orchestrator.ActionLaunch || a.ItemID != "wi-1" {
		t.Fatalf("the plan over the approved item is %+v, not the launch of it", a)
	}
	if got := orchestrator.Queue(s, view); len(got) != 0 {
		t.Fatalf("the queue holds %+v, and an item a person has approved is not one waiting for them", got)
	}
}

// TestLauncherOrdersImportantFirst is D2040 read at the launcher: an important
// follow-up is planned ahead of new work and ahead of a minor one, so a cycle
// that may start one run spends it on the work that matters most rather than on
// whichever item the state happens to declare first. Everything that is not an
// important follow-up keeps the state's own order, which is a person's.
//
// The queue a person reads is the same order over the items waiting on them, so
// that what they are asked for first is what the daemon would start first.
//
// Mutation: drop the sort from planOrder and the plan holds the item declared
// first, which is new work.
func TestLauncherOrdersImportantFirst(t *testing.T) {
	now := o6Now()
	at := now.Add(-time.Hour)
	items := []orchestrator.WorkItem{
		o6Item("wi-new", orchestrator.PhaseQueued, at),
		o6Item("wi-minor", orchestrator.PhaseQueued, at),
		o6Item("wi-important", orchestrator.PhaseQueued, at),
		o6Item("wi-other", orchestrator.PhaseQueued, at),
	}
	s := o6Node(t, now, items...)
	view := follower("wi-minor", orchestrator.FollowUp{Importance: task.ImportanceMinor})
	view.FollowUps["wi-important"] = orchestrator.FollowUp{
		Importance: task.ImportanceImportant,
		Scores:     []rigor.Score{finding(1, 5, 3, rigor.DataLoss)},
	}

	want := []string{"wi-important", "wi-new", "wi-minor", "wi-other"}
	got := orchestrator.Plan(s, view, o6Caps(), now)
	if ids := actionIDs(got); !slices.Equal(ids, want) {
		t.Fatalf("the plan is %v, and the important follow-up goes first: %v", ids, want)
	}

	one := orchestrator.Plan(s, view, withCaps(func(c *orchestrator.Caps) { c.MaxPerCycle = 1 }), now)
	if ids := actionIDs(one); !slices.Equal(ids, want[:1]) {
		t.Fatalf("the cycle's one run went to %v, and it belongs to the important follow-up", ids)
	}

	pending := o6Node(t, now,
		workItemOf("wi-p-minor"),
		workItemOf("wi-p-important"),
		o6Item("wi-p-approved", orchestrator.PhaseQueued, at),
	)
	waiting := follower("wi-p-important", orchestrator.FollowUp{Importance: task.ImportanceImportant})
	waiting.FollowUps["wi-p-minor"] = orchestrator.FollowUp{Importance: task.ImportanceMinor}
	queue := orchestrator.Queue(pending, waiting)
	if ids := itemIDs(queue); !slices.Equal(ids, []string{"wi-p-important", "wi-p-minor"}) {
		t.Fatalf("the person's queue is %v, and it is the items waiting on them, important first", ids)
	}
}

// TestImportanceRuleSecurityOrImpact5IsImportant is D2040's deterministic rule,
// the one the launcher reads where no judge classified the follow-up: a finding
// of the security or data-loss class, or one at the top of the impact scale.
// The judge's own mark wins where there is one — it is the classification
// D2040 says the rule stands in for — and a follow-up nobody classified and
// nobody could score is important too: the rule cannot place it, and the strict
// answer is the one that puts it in front of a person rather than behind new
// work. Nothing here approves anything: the answer is a sorting key (G6).
func TestImportanceRuleSecurityOrImpact5IsImportant(t *testing.T) {
	cases := []struct {
		name string
		f    orchestrator.FollowUp
		want bool
	}{
		{
			name: "the judge said important",
			f:    orchestrator.FollowUp{Importance: task.ImportanceImportant},
			want: true,
		},
		{
			name: "the judge said minor, whatever the score",
			f: orchestrator.FollowUp{
				Importance: task.ImportanceMinor,
				Scores:     []rigor.Score{finding(1, 5, 4, rigor.Security)},
			},
			want: false,
		},
		{
			name: "security",
			f:    orchestrator.FollowUp{Scores: []rigor.Score{finding(1, 2, 5, rigor.Security)}},
			want: true,
		},
		{
			name: "data loss",
			f:    orchestrator.FollowUp{Scores: []rigor.Score{finding(1, 3, 1, rigor.DataLoss)}},
			want: true,
		},
		{
			name: "an impact of five",
			f:    orchestrator.FollowUp{Scores: []rigor.Score{finding(1, 5, 1, rigor.Correctness)}},
			want: true,
		},
		{
			name: "a scored finding below both",
			f:    orchestrator.FollowUp{Scores: []rigor.Score{finding(2, 2, 1, rigor.Maintainability)}},
			want: false,
		},
		{
			name: "one important finding among mild ones",
			f: orchestrator.FollowUp{Scores: []rigor.Score{
				finding(1, 1, 1, rigor.Cosmetic),
				finding(3, 5, 2, rigor.Correctness),
			}},
			want: true,
		},
		{
			name: "no classification at all",
			f:    orchestrator.FollowUp{},
			want: true,
		},
		{
			name: "unscored",
			f:    orchestrator.FollowUp{Scores: []rigor.Score{{Class: rigor.Security}}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.Important(); got != c.want {
				t.Fatalf("Important() is %v for %+v, and this release reads it as %v", got, c.f, c.want)
			}
		})
	}
}

// TestApprovalAttemptsCountNormalRunsOnly is G15 at the launcher: the runs the
// ladder spends are counted apart from the attempts a person's approval covers.
// A run of another recipe the ladder launched — rung 0's rebase, the one rung
// 2, the judge — is the ledger's count and never one of the approval's two, so
// it neither spends the allowance nor answers the retry; and the attempt that
// does answer it is the item's own last run of the spec the approval covers.
//
// Mutation: count every intent under the approval (AttemptsUnder) where the
// allowance is read, and the item whose rebase run came back is one the plan
// holds nothing for.
func TestApprovalAttemptsCountNormalRunsOnly(t *testing.T) {
	now := o6Now()
	at := now.Add(-time.Hour)
	item, first := o6FailedItem("wi-1", at, now.Add(-30*time.Minute))
	item.Escalation = atRung1(now.Add(-2*time.Hour), 3)
	s := o6Node(t, now, item)
	s.Intents = []orchestrator.Intent{first}

	a := onlyAction(t, orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionRetry || a.Attempt != 2 {
		t.Fatalf("the plan is %+v, and the ladder's three runs are not the person's two attempts", a)
	}
	if got := orchestrator.LadderRuns(s, item); got != 3 {
		t.Fatalf("LadderRuns is %d, and the ledger's three runs of another recipe are the ladder's", got)
	}
	if got := orchestrator.AttemptsUnder(s, "wi-1", at); got != 1 {
		t.Fatalf("AttemptsUnder is %d, and one attempt of the approval's two is committed", got)
	}

	// The same item with a ladder run the daemon recorded as an intent: a rebase
	// the executor launched under the same approval. It is not a run of the spec
	// the approval covers, so the person's second attempt is still what the plan
	// holds — the approval's allowance is read off its own runs (the write side
	// has to count the same way for the retry to land: E10a records the run).
	rebase := item
	rebase.Recipe = "rebase"
	s.Intents = append(s.Intents, orchestrator.Intent{
		ID:         orchestrator.IntentID("wi-1", 2),
		RunID:      "20261107-105000-cccc",
		SpecDigest: orchestrator.SpecDigest(rebase),
		ApprovalAt: at.UTC(),
		At:         now.Add(-20 * time.Minute),
		Outcome:    orchestrator.OutcomeSucceeded,
	})
	if got := orchestrator.LadderRuns(s, item); got != 4 {
		t.Fatalf("LadderRuns is %d, and the rebase beside the ledger's three is the ladder's too", got)
	}
	a = onlyAction(t, orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionRetry || a.Attempt != 3 {
		t.Fatalf("the plan is %+v, and the approval's own last run is the one a retry answers", a)
	}

	// And a rung 1 resume: a run of the item's own recipe past the two the
	// approval covers is the ladder's, which is what makes the third normal run
	// of an item a launch of the ladder and not a third attempt of a person's.
	resume := s
	resume.Intents = append(append([]orchestrator.Intent{}, twoAttempts("wi-1", at, now.Add(-50*time.Minute), item)...),
		orchestrator.Intent{
			ID:         orchestrator.IntentID("wi-1", 3),
			RunID:      "20261107-110000-dddd",
			SpecDigest: orchestrator.SpecDigest(item),
			ApprovalAt: at.UTC(),
			At:         now.Add(-10 * time.Minute),
			Outcome:    orchestrator.OutcomeFailed,
		})
	if got := orchestrator.LadderRuns(resume, item); got != 4 {
		t.Fatalf("LadderRuns is %d, and the third run of the item's own recipe is the ladder's", got)
	}
}

// TestThirdNormalRunIsCoveredByLadder is D2042 at the launcher: with the two
// attempts a person's approval covers spent, the item's third normal run is the
// ladder's rung 1 — a resume of the item's own recipe, covered by the approval
// that is already gone, which the spent marker names. It is planned under that
// approval's At and counted apart from its two, and what stops it is the
// ladder's own bound, never ApprovalAttempts.
//
// It is the executor that asks for this (E10a): the plan the launcher makes for
// the item holds nothing, because a person is what opens another attempt of
// their own, and a rung 1 resume is not one of theirs.
func TestThirdNormalRunIsCoveredByLadder(t *testing.T) {
	now := o6Now()
	at := now.Add(-2 * time.Hour)
	item := spentItem(t, "wi-1", now, at)
	item.Phase = orchestrator.PhaseFailed
	item.Escalation = atRung1(now.Add(-90*time.Minute), 0)
	s := o6Node(t, now, item)
	s.Intents = twoAttempts("wi-1", at, now.Add(-100*time.Minute), item)

	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("the plan holds %+v, and the person's two attempts are spent", got)
	}

	a, ok := orchestrator.LadderLaunch(s, item, orchestrator.RunView{})
	if !ok {
		t.Fatal("the ladder launches nothing for an item at rung 1 whose approval is spent, and its third normal run is the ladder's")
	}
	if a.Kind != orchestrator.ActionLadder || a.Attempt != 3 || a.IntentID != "wi-1:3" {
		t.Fatalf("the ladder's launch is %+v, and the item's next attempt is its third", a)
	}
	if !a.ApprovalAt.Equal(at) {
		t.Fatalf("the launch spends the approval from %s, and the one the item spent is from %s", a.ApprovalAt, at)
	}
	if a.Digest != orchestrator.SpecDigest(item) || a.Spec.ItemID != "wi-1" {
		t.Fatalf("the launch carries %+v, and it starts the item the person approved", a)
	}
	if got := orchestrator.LadderRuns(s, item); got != 0 {
		t.Fatalf("LadderRuns is %d, and the two runs the approval covers are the person's and not the ladder's", got)
	}

	// And the resume that launch records is the ladder's own run: the third of
	// the item's recipe, which is past the two the approval covers.
	resumed := s
	resumed.Intents = append(append([]orchestrator.Intent{}, s.Intents...), orchestrator.Intent{
		ID:         a.IntentID,
		RunID:      "20261107-120000-ffff",
		SpecDigest: a.Digest,
		ApprovalAt: at.UTC(),
		At:         now,
	})
	if got := orchestrator.LadderRuns(resumed, item); got != 1 {
		t.Fatalf("LadderRuns is %d, and the resume the ladder launched is the run it spent", got)
	}

	// The ladder's own bound, and not the approval's two: an item whose ladder
	// runs are spent launches nothing more, and a fourth attempt is never
	// ApprovalAttempts read twice.
	spent := item
	spent.Escalation = atRung1(now.Add(-90*time.Minute), orchestrator.MaxOuterAttempts)
	if a, ok := orchestrator.LadderLaunch(s, spent, orchestrator.RunView{}); ok {
		t.Fatalf("the ladder launches %+v with %d outer runs spent", a, orchestrator.MaxOuterAttempts)
	}

	// An edit after the approval was spent makes the item another spec, and the
	// ladder resumes only what was approved: nothing starts under the old go.
	edited := item
	edited.Brief = "a brief nobody approved"
	if a, ok := orchestrator.LadderLaunch(s, edited, orchestrator.RunView{}); ok {
		t.Fatalf("the ladder launches %+v over a spec no approval covers", a)
	}

	// The ladder launches on top of the item's own loop and never beside it: an
	// approval that still has an attempt to give the item is the loop's to spend,
	// and an attempt committed and not reported is the recovery's to re-issue.
	looping, first := o6FailedItem("wi-1", at, now.Add(-30*time.Minute))
	looping.Escalation = atRung1(now.Add(-90*time.Minute), 3)
	loop := o6Node(t, now, looping)
	loop.Intents = []orchestrator.Intent{first}
	if a, ok := orchestrator.LadderLaunch(loop, looping, orchestrator.RunView{}); ok {
		t.Fatalf("the ladder launches %+v while the person's approval still plans the item's retry", a)
	}

	// And a run the loop itself refuses to retry: a refusal is the loop's own
	// answer, and the person's one remaining attempt is not the ladder's to
	// spend on work a run has already declined.
	refused, was := o6FailedItem("wi-1", at, now.Add(-30*time.Minute))
	refused.Escalation = atRung1(now.Add(-90*time.Minute), 3)
	was.Outcome = orchestrator.OutcomeRefused
	hold := o6Node(t, now, refused)
	hold.Intents = []orchestrator.Intent{was}
	if got := orchestrator.Plan(hold, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("the plan holds %+v for a refused run, and a refusal is not one a retry answers", got)
	}
	if a, ok := orchestrator.LadderLaunch(hold, refused, orchestrator.RunView{}); ok {
		t.Fatalf("the ladder launches %+v over a run the item's own loop refuses to retry", a)
	}

	inFlight := workItemOf("wi-1")
	inFlight.Phase = orchestrator.PhaseFailed
	inFlight.Attempts = 3
	inFlight.Escalation = atRung1(now.Add(-90*time.Minute), 1)
	atSpent := at.UTC()
	inFlight.SpentApprovalAt = &atSpent
	crash := o6Node(t, now, inFlight)
	crash.Intents = append(twoAttempts("wi-1", at, now.Add(-100*time.Minute), inFlight), orchestrator.Intent{
		ID:         orchestrator.IntentID("wi-1", 3),
		RunID:      "20261107-110000-ffff",
		SpecDigest: orchestrator.SpecDigest(inFlight),
		ApprovalAt: atSpent,
		At:         now.Add(-time.Minute),
	})
	if a, ok := orchestrator.LadderLaunch(crash, inFlight, orchestrator.RunView{}); ok {
		t.Fatalf("the ladder launches %+v while the item has an attempt committed and not reported", a)
	}
}

// TestLadderLaunchStopsAtCostCeiling is G15's optional extra, read as G9 states
// it: the ladder's launches stop when the sum of the item's own run records
// reaches escalation.cost_ceiling, whatever the rung's counts still allow; a
// ceiling nobody set bounds nothing, and the counts are what bounds the ladder
// then.
func TestLadderLaunchStopsAtCostCeiling(t *testing.T) {
	now := o6Now()
	at := now.Add(-2 * time.Hour)
	item := spentItem(t, "wi-1", now, at)
	item.Phase = orchestrator.PhaseFailed
	item.Escalation = atRung1(now.Add(-90*time.Minute), 1)
	s := o6Node(t, now, item)
	s.Intents = twoAttempts("wi-1", at, now.Add(-100*time.Minute), item)

	view := orchestrator.RunView{Spend: map[string]orchestrator.LadderSpend{"wi-1": {USD: 4.5, Ceiling: 5}}}
	if _, ok := orchestrator.LadderLaunch(s, item, view); !ok {
		t.Fatal("the ladder launches nothing below the ceiling, and 4.50 of 5.00 is below it")
	}

	view.Spend["wi-1"] = orchestrator.LadderSpend{USD: 5, Ceiling: 5}
	if a, ok := orchestrator.LadderLaunch(s, item, view); ok {
		t.Fatalf("the ladder launches %+v at the ceiling, and the ceiling is what a person set to stop it", a)
	}

	view.Spend["wi-1"] = orchestrator.LadderSpend{USD: 5}
	if _, ok := orchestrator.LadderLaunch(s, item, view); !ok {
		t.Fatal("the ladder launches nothing with no ceiling set, and with none the counts are the bound")
	}

	// The ceiling is the item's own spend and not the node's: another item's
	// runs do not stop this one, and a restart cannot reset it — what the view
	// carries is the sum of the item's own run records, which is the state's.
	view.Spend["wi-1"] = orchestrator.LadderSpend{USD: 4.5, Ceiling: 5}
	view.Spend["wi-2"] = orchestrator.LadderSpend{USD: 400, Ceiling: 5}
	if _, ok := orchestrator.LadderLaunch(s, item, view); !ok {
		t.Fatal("another item's spend stopped this item's launch, and the ceiling reads the item's own runs")
	}
}

// TestLadderIntentSurvivesCrashAndRetry is the other half of D2042: the intent
// of a rung 1 resume that crashed before its run started is re-issued under the
// run id the state recorded — the same launch, and never a second one — even
// though the approval it names is gone. It is the spent marker that covers it,
// because the write that committed the retry cleared Approved in the same
// write, and an attempt nothing re-issues is an item stuck in running forever.
//
// Mutation: read recovery's coverage off the standing approval alone (drop
// spentBy) and the crashed resume is abandoned with the note a person reads.
func TestLadderIntentSurvivesCrashAndRetry(t *testing.T) {
	now := o6Now()
	at := now.Add(-2 * time.Hour)
	item := spentItem(t, "wi-1", now, at)
	item.Escalation = atRung1(now.Add(-90*time.Minute), 1)
	resume := orchestrator.Intent{
		ID:         orchestrator.IntentID("wi-1", 3),
		RunID:      "20261107-110000-eeee",
		SpecDigest: orchestrator.SpecDigest(item),
		ApprovalAt: at.UTC(),
		At:         now.Add(-orchestrator.ClaimTTL - time.Minute),
	}
	// The state a committed resume leaves: the item carries the third attempt,
	// which is the one its intent recorded, and the run it was committed for
	// never started.
	item.Attempts = 3
	s := o6Node(t, now, item)
	s.Intents = append(twoAttempts("wi-1", at, now.Add(-100*time.Minute), item), resume)

	a := onlyAction(t, orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionReissue || a.RunID != resume.RunID || a.IntentID != resume.ID {
		t.Fatalf("the plan is %+v, and the crashed resume is re-issued under the run id it recorded", a)
	}
	if !a.ApprovalAt.Equal(at) {
		t.Fatalf("the re-issue spends the approval from %s, and the resume was committed under %s", a.ApprovalAt, at)
	}

	o := newO6Starter(t, s, now)
	if err := o.run(a); err != nil {
		t.Fatalf("the re-issue was refused: %v", err)
	}
	if len(o.l.started) != 1 || o.l.started[0] != resume.RunID {
		t.Fatalf("the launcher started %v, and a re-issue starts the attempt the state already recorded", o.l.started)
	}
	if o.m.calls != 0 {
		t.Fatalf("the re-issue minted %d run ids, and minting one would make it a second launch", o.m.calls)
	}

	// The run the re-issue started reported: the item moves on, and the attempt
	// that was in flight is history like any other.
	s = o6Report(t, s, "wi-1", resume.ID, orchestrator.OutcomeFailed, orchestrator.PhaseFailed, now.Add(time.Minute))
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now.Add(2*time.Minute)); len(got) != 0 {
		t.Fatalf("the plan holds %+v after the resume reported, and a person is what opens another attempt", got)
	}
}

// actionIDs is the items a plan acts on, in the order it acts on them.
func actionIDs(actions []orchestrator.Action) []string {
	ids := make([]string, 0, len(actions))
	for _, a := range actions {
		ids = append(ids, a.ItemID)
	}
	return ids
}

// itemIDs is a list of items as the ids they carry.
func itemIDs(items []orchestrator.WorkItem) []string {
	ids := make([]string, 0, len(items))
	for _, w := range items {
		ids = append(ids, w.ID)
	}
	return ids
}
