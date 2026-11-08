package orchestrator_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The O6 tests plan over one clock and one set of caps wide enough to hold
// every item they put in front of them, so a test that means to pin a cap
// narrows that one field and what stops a spawn there is that cap alone.
func o6Now() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) }

func o6Caps() orchestrator.Caps {
	return orchestrator.Caps{
		MaxRunning:     8,
		MaxPerCycle:    4,
		SpawnPerMinute: 4,
		SpawnPerHour:   16,
		SpawnPerDay:    64,
	}
}

// withCaps is those caps with one field narrowed by the caller.
func withCaps(narrow func(*orchestrator.Caps)) orchestrator.Caps {
	c := o6Caps()
	narrow(&c)
	return c
}

// o6Item is a work item of the shape these tests plan over: the standard
// declaration, the phase the caller names, and a person's standing approval
// whose digest is the item's own.
func o6Item(id string, phase orchestrator.Phase, approvedAt time.Time) orchestrator.WorkItem {
	w := workItemOf(id)
	w.Phase = phase
	return o6ApprovedBy(w, approvedAt)
}

// o6ApprovedBy is the item with a person's standing approval over it as it now
// stands. A test that changes a field and then approves is a person approving
// the item they read; one that approves first and changes a field after is the
// case the launcher exists to refuse.
func o6ApprovedBy(w orchestrator.WorkItem, approvedAt time.Time) orchestrator.WorkItem {
	w.Approved = &orchestrator.Approval{
		By:         "owner",
		At:         approvedAt.UTC(),
		SpecDigest: orchestrator.SpecDigest(w),
		Attempts:   orchestrator.ApprovalAttempts,
	}
	return w
}

// o6Node is a node carrying exactly the items a test hands it.
func o6Node(t *testing.T, now time.Time, items ...orchestrator.WorkItem) orchestrator.State {
	t.Helper()
	s := stateWithAQueuedItem(t, now)
	s.WorkItems = items
	return s
}

// o6FailedItem is the position the one automatic retry is planned from: an
// item whose first attempt under the standing approval has failed.
func o6FailedItem(id string, approvedAt, at time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	w := o6Item(id, orchestrator.PhaseFailed, approvedAt)
	w.RunID = "20261107-090000-abcd"
	w.Attempts = 1
	it := orchestrator.Intent{
		ID:         orchestrator.IntentID(id, 1),
		RunID:      w.RunID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: approvedAt.UTC(),
		At:         at.UTC(),
		Outcome:    orchestrator.OutcomeFailed,
	}
	return w, it
}

// o6OpenItem is an item whose attempt is committed and has not reported: a run
// the daemon believes is going, which is what the crash that committed the
// intent without starting it leaves behind.
func o6OpenItem(id, runID string, approvalAt, at time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	w := o6Item(id, orchestrator.PhaseRunning, approvalAt)
	w.RunID = runID
	w.Attempts = 1
	it := orchestrator.Intent{
		ID:         orchestrator.IntentID(id, 1),
		RunID:      runID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: approvalAt.UTC(),
		At:         at.UTC(),
	}
	return w, it
}

// onlyAction is the one action a plan must hold for a step that is about one.
func onlyAction(t *testing.T, got []orchestrator.Action) orchestrator.Action {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("the plan holds %d action(s) where this step is about one: %+v", len(got), got)
	}
	return got[0]
}

// o6Launcher records what it was asked to start and starts nothing: a run id
// in started is one the daemon believes is going, and specs is what each of
// those starts was handed. The spec is recorded because it is what the run is
// built from, and a launcher that ignores it would let a forged one through.
type o6Launcher struct {
	asked   []string
	specs   []orchestrator.Spec
	started []string
	refuse  error
	wrongID string
}

func (l *o6Launcher) Launch(_ context.Context, spec orchestrator.Spec, runID string) (string, error) {
	l.asked = append(l.asked, runID)
	l.specs = append(l.specs, spec)
	if l.wrongID != "" {
		return l.wrongID, nil
	}
	if l.refuse != nil {
		return "", l.refuse
	}
	l.started = append(l.started, runID)
	return runID, nil
}

// o6Mint mints the run ids a test named, in order, and counts the calls: a
// recovery that minted a second run id is exactly what the count catches.
type o6Mint struct {
	t     *testing.T
	runs  []string
	calls int
}

func (m *o6Mint) mint(time.Time) (string, error) {
	m.calls++
	if m.calls > len(m.runs) {
		m.t.Fatalf("the starter minted %d run ids and the test gave it %d", m.calls, len(m.runs))
	}
	return m.runs[m.calls-1], nil
}

// o6Starter is the Starter a test drives: a launcher, a node holding the state
// the commits land in, and a mint handing out the run ids the test named.
type o6Starter struct {
	t     *testing.T
	s     orchestrator.State
	l     *o6Launcher
	m     *o6Mint
	now   time.Time
	fail  error
	wrote bool
}

func newO6Starter(t *testing.T, s orchestrator.State, now time.Time, runs ...string) *o6Starter {
	t.Helper()
	return &o6Starter{t: t, s: s, l: &o6Launcher{}, m: &o6Mint{t: t, runs: runs}, now: now}
}

// run starts one action the way a tick does. A start that fails is a fact
// these tests assert on rather than a reason to stop, so it is returned.
func (o *o6Starter) run(a orchestrator.Action) error {
	o.t.Helper()
	o.wrote = false
	d := orchestrator.Starter{Launcher: o.l, Commit: o.commit, Mint: o.m.mint}
	next, err := d.Start(context.Background(), o.s, a, o.now)
	if o.wrote {
		o.s = next
	}
	return err
}

// commit is the node's write. A commit that failed wrote nothing — which is
// what PutIfMatch gives a caller — so the state stays as it was.
func (o *o6Starter) commit(_ context.Context, s orchestrator.State) error {
	if o.fail != nil {
		return o.fail
	}
	o.s = s
	o.wrote = true
	return nil
}

// o6Report folds a run's outcome into the state the way a tick does: the
// intent records how the attempt went and the item takes the phase that goes
// with it, in one write.
func o6Report(t *testing.T, s orchestrator.State, itemID, intentID string, outcome orchestrator.Outcome, phase orchestrator.Phase, at time.Time) orchestrator.State {
	t.Helper()
	op := orchestrator.WorkItemOp{ID: itemID, Phase: &phase, Intent: &orchestrator.IntentOp{ID: intentID, Outcome: &outcome}}
	return o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &op}, orchestrator.Actor{Class: orchestrator.Daemon, Now: at})
}

// o6Approve is a person's go for the item as it now stands.
func o6Approve(t *testing.T, s orchestrator.State, id string, at time.Time) orchestrator.State {
	t.Helper()
	w, ok := s.Item(id)
	if !ok {
		t.Fatalf("no work item %q to approve", id)
	}
	op := orchestrator.Op{Op: orchestrator.OpWorkItemApprove, Approve: &orchestrator.ApproveOp{ID: id, SpecDigest: orchestrator.SpecDigest(w)}}
	return o6Apply(t, s, op, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: at})
}

// o6Edit is a session rewriting a declaration, which is how an approved spec
// stops being the one the person approved.
func o6Edit(t *testing.T, s orchestrator.State, id, brief string, at time.Time) orchestrator.State {
	t.Helper()
	op := orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: id, Brief: &brief}}
	return o6Apply(t, s, op, orchestrator.Actor{Class: orchestrator.Session, Now: at})
}

// o6Apply is a write these tests expect the state to accept: one it refuses is
// the test's own mistake.
func o6Apply(t *testing.T, s orchestrator.State, op orchestrator.Op, a orchestrator.Actor) orchestrator.State {
	t.Helper()
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{op}}, a)
	if err != nil {
		t.Fatalf("the write was refused: %v", err)
	}
	return next
}

// TestSpawnNeverLeavesTheApprovedItem is the first half of the money rule: a
// launch carries the spec of the item the person approved — every digested
// field of it, and the digest the approval names — and never a spec of its
// own, a stale one, or one belonging to the item beside it.
func TestSpawnNeverLeavesTheApprovedItem(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)
	first := o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)
	second := o6Item("wi-2", orchestrator.PhaseQueued, approvedAt)
	second.Title = "Another title"
	second.Repo = "other/place"
	second.Recipe = "other"
	second.RigorTier = "large"
	second.Brief = "Another brief entirely."
	second = o6ApprovedBy(second, approvedAt)
	s := o6Node(t, now, first, second)

	got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now)
	if len(got) != 2 {
		t.Fatalf("the plan holds %d action(s), not the launch of each approved item: %+v", len(got), got)
	}
	for i, w := range []orchestrator.WorkItem{first, second} {
		a := got[i]
		if a.Kind != orchestrator.ActionLaunch || a.ItemID != w.ID {
			t.Fatalf("action %d is %s for %q, not a launch of %q", i+1, a.Kind, a.ItemID, w.ID)
		}
		if a.Attempt != 1 || a.IntentID != orchestrator.IntentID(w.ID, 1) {
			t.Errorf("the launch of %q is attempt %d under the intent %q", w.ID, a.Attempt, a.IntentID)
		}
		if a.Spec != orchestrator.SpecOf(w) {
			t.Errorf("the launch of %q carries the spec %+v, not the item's own %+v", w.ID, a.Spec, orchestrator.SpecOf(w))
		}
		if a.Digest != orchestrator.SpecDigest(w) || a.Digest != w.Approved.SpecDigest {
			t.Errorf("the launch of %q carries the digest %q, not the one the person approved (%q)", w.ID, a.Digest, w.Approved.SpecDigest)
		}
		if !a.ApprovalAt.Equal(approvedAt) {
			t.Errorf("the launch of %q spends the approval from %s, and the standing one is from %s", w.ID, a.ApprovalAt, approvedAt)
		}
	}
	// Two items, two launches, and nothing of one on the other's.
	if got[0].Spec.Repo == got[1].Spec.Repo || got[0].Spec.Brief == got[1].Spec.Brief || got[0].Spec.Title == got[1].Spec.Title {
		t.Errorf("the two launches carry each other's specs: %+v and %+v", got[0].Spec, got[1].Spec)
	}

	// A launcher that comes back with a run id of its own is refused: the state
	// records the id before anything starts, so a second one is a run nothing
	// here knows the outcome of, and the attempt it was asked for stays the
	// record the next tick re-issues.
	forged := newO6Starter(t, s, now, "20261107-120000-aaaa")
	forged.l.wrongID = "20261107-120000-ffff"
	if err := forged.run(got[0]); err == nil {
		t.Fatal("a launcher that started another run id was not refused")
	}
	if !slices.Equal(forged.l.started, []string{}) {
		t.Errorf("the launcher reported starting %v for a launch it answered with another id", forged.l.started)
	}
	if len(forged.s.Intents) != 1 || forged.s.Intents[0].RunID != "20261107-120000-aaaa" || forged.s.Intents[0].Outcome != "" {
		t.Errorf("the refused launch left %+v, and what it committed is the attempt recovery re-issues", forged.s.Intents)
	}

	// An action is a proposal, and what a start is handed is the spec the state
	// carries rather than the one the action names: a stale plan, or a field
	// somebody assembled by hand, may not start a run on a brief, a repo or a
	// recipe the person never approved. The digest the action commits is the
	// item's either way, so this read is the one thing that catches it.
	fresh := newO6Starter(t, s, now, "20261107-120000-bbbb")
	doctored := got[0]
	doctored.Spec.Brief = "A brief nobody approved."
	doctored.Spec.Repo = "elsewhere/other"
	if err := fresh.run(doctored); err != nil {
		t.Fatalf("the launch was refused: %v", err)
	}
	if len(fresh.l.specs) != 1 {
		t.Fatalf("the launcher was handed %d spec(s) for one start", len(fresh.l.specs))
	}
	if want := orchestrator.SpecOf(first); fresh.l.specs[0] != want {
		t.Errorf("the launcher started %+v, and the item the attempt was committed for is %+v", fresh.l.specs[0], want)
	}
}

// TestUnapprovedItemIsNeverLaunched is the other half: nothing but a person's
// standing approval over the spec as it is now makes an item launchable, and
// the approval is the only road in — no class of actor but a person can give
// one.
func TestUnapprovedItemIsNeverLaunched(t *testing.T) {
	now := o6Now()
	queued := workItemOf("wi-1")
	s := o6Node(t, now, queued)

	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("a queued item nobody approved planned %+v", got)
	}

	// A session may read the item and compute its digest, and that is not an
	// approval: the launcher's whole fence is that the approval is a person's.
	forged, err := orchestrator.Apply(s, orchestrator.Patch{
		BaseRev: s.Rev,
		Ops: []orchestrator.Op{{Op: orchestrator.OpWorkItemApprove, Approve: &orchestrator.ApproveOp{
			ID:         "wi-1",
			SpecDigest: orchestrator.SpecDigest(queued),
		}}},
	}, orchestrator.Actor{Class: orchestrator.Session, Name: "session-a", Now: now})
	if err == nil {
		t.Fatalf("a session approved a work item: %+v", forged.WorkItems[0])
	}

	approved := o6Approve(t, s, "wi-1", now)
	a := onlyAction(t, orchestrator.Plan(approved, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionLaunch || a.ItemID != "wi-1" {
		t.Fatalf("the plan over an approved item is %+v", a)
	}
}

// TestRecoveryReissuesUnderTheRecordedRunID is the red step's first half, and
// the money rule itself: the run id is minted once, committed with the intent
// before anything starts, and a daemon that died in between re-issues that
// same launch rather than spending a second one.
func TestRecoveryReissuesUnderTheRecordedRunID(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const first = "20261107-120000-aaaa"
	o := newO6Starter(t, o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)), now, first)

	if err := o.run(onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))); err != nil {
		t.Fatalf("the launch was refused: %v", err)
	}
	if !slices.Equal(o.l.started, []string{first}) {
		t.Fatalf("the launcher started %v", o.l.started)
	}

	// The daemon died here: the intent is committed, the run id is recorded,
	// and nothing in the process remembers the launch.
	mid := o.s
	if len(mid.Intents) != 1 {
		t.Fatalf("the cycle committed %d intents, not the one it started", len(mid.Intents))
	}
	recorded := mid.Intents[0]
	if recorded.RunID != first || recorded.Outcome != "" {
		t.Fatalf("the intent reads %+v", recorded)
	}

	// Inside the claim window the attempt is left alone — the platform may
	// still be starting the run the state recorded.
	if got := orchestrator.Plan(mid, orchestrator.RunView{}, o6Caps(), now.Add(orchestrator.ClaimTTL-time.Second)); len(got) != 0 {
		t.Fatalf("the plan acted on an attempt inside its claim window: %+v", got)
	}
	// A run that started is in flight however long it takes, and no window
	// makes it a second launch.
	if got := orchestrator.Plan(mid, orchestrator.RunView{Runs: []orchestrator.Run{{RunID: recorded.RunID, Started: true}}}, o6Caps(), now.Add(orchestrator.RetentionWindow)); len(got) != 0 {
		t.Fatalf("the plan re-issued a run that had started: %+v", got)
	}

	// Past the window, the attempt is re-issued under the run id it recorded.
	later := now.Add(orchestrator.ClaimTTL)
	reissue := onlyAction(t, orchestrator.Plan(mid, orchestrator.RunView{}, o6Caps(), later))
	if reissue.Kind != orchestrator.ActionReissue || reissue.RunID != recorded.RunID || reissue.IntentID != recorded.ID {
		t.Fatalf("recovery planned %+v, not a re-issue of %q under %q", reissue, recorded.ID, recorded.RunID)
	}
	if reissue.Spec != orchestrator.SpecOf(mid.WorkItems[0]) || reissue.Digest != recorded.SpecDigest {
		t.Errorf("the re-issue carries %+v / %q, not the spec the intent was committed for", reissue.Spec, reissue.Digest)
	}

	o.now = later
	if err := o.run(reissue); err != nil {
		t.Fatalf("the re-issue was refused: %v", err)
	}
	if o.m.calls != 1 {
		t.Errorf("recovery minted %d run ids; the attempt it re-issued already had one", o.m.calls)
	}
	if !slices.Equal(o.l.asked, []string{first, first}) {
		t.Errorf("the launcher was asked for %v, and the run id is minted once", o.l.asked)
	}
	if len(o.s.Intents) != 1 || orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt) != 1 {
		t.Errorf("the re-issue left %+v: one attempt, counted once", o.s.Intents)
	}

	// A re-issue is checked against the state in hand rather than trusted, and
	// a start is the one thing here that cannot be taken back. Every way the
	// state can stop carrying the attempt is refused, one case each: a run id
	// no intent records, a digest the intent was not committed for, an item
	// edited since the intent, an item a person stopped, and an approval the
	// intent never spent. What a refusal leaves behind is the committed attempt
	// itself, still waiting for a tick that can re-issue it.
	edited := o6Edit(t, mid, "wi-1", "A different brief.", now.Add(time.Minute))

	stopped := o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)
	stopped.Approved = nil
	stopped.SpentApprovalAt = &approvedAt
	stopped.RunID = recorded.RunID
	stopped.Attempts = 1
	stoppedState := o6Node(t, now, stopped)
	stoppedState.Intents = []orchestrator.Intent{recorded}
	moved := orchestrator.PhaseCancelled
	stoppedState = o6Apply(t, stoppedState, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: "wi-1", Phase: &moved}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now.Add(time.Minute)})

	elsewhere := o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)
	elsewhere.Brief = "A brief this attempt was never committed for."

	replaced := o6Item("wi-1", orchestrator.PhaseQueued, now.Add(time.Minute))
	replaced.RunID = recorded.RunID
	replaced.Attempts = 1
	replacedState := o6Node(t, now, replaced)
	replacedState.Intents = []orchestrator.Intent{recorded}

	for _, tc := range []struct {
		why   string
		state orchestrator.State
		forge func(a orchestrator.Action) orchestrator.Action
	}{
		{
			why:   "a run id no intent records",
			state: mid,
			forge: func(a orchestrator.Action) orchestrator.Action { a.RunID = "20261107-120000-bbbb"; return a },
		},
		{
			why:   "a digest the intent was not committed for",
			state: mid,
			forge: func(a orchestrator.Action) orchestrator.Action {
				a.Digest = orchestrator.SpecDigest(elsewhere)
				return a
			},
		},
		{
			why:   "an item edited since the intent",
			state: edited,
		},
		{
			why:   "an item a person stopped",
			state: stoppedState,
		},
		{
			why:   "an approval the intent never spent",
			state: replacedState,
		},
	} {
		forged := reissue
		if tc.forge != nil {
			forged = tc.forge(forged)
		}
		c := newO6Starter(t, tc.state, later)
		if err := c.run(forged); err == nil {
			t.Errorf("the starter started a run over %s", tc.why)
		}
		if len(c.l.asked) != 0 {
			t.Errorf("the launcher was asked for %v over %s", c.l.asked, tc.why)
		}
		if len(c.s.Intents) != 1 || c.s.Intents[0].Outcome != "" {
			t.Errorf("the refused re-issue over %s left %+v", tc.why, c.s.Intents)
		}
	}

	// A starter with no launcher refuses instead of panicking: a re-issue has
	// no commit behind it to fail on, so the missing piece is the launcher's to
	// report like every other misconfiguration here.
	naked := orchestrator.Starter{Commit: o.commit}
	if _, err := naked.Start(context.Background(), o.s, reissue, later); err == nil {
		t.Error("a starter with no launcher started a run")
	}
}

// TestEditedSpecIsNeverLaunched: what the person approved is the spec they
// read. An edit since is another spec, so the item is not one to start — and a
// person who reads it again and approves it is what starts it.
func TestEditedSpecIsNeverLaunched(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)
	s := o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt))
	if len(orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now)) != 1 {
		t.Fatalf("the approved item is not launchable to begin with")
	}

	edited := o6Edit(t, s, "wi-1", "A different brief.", now)
	if w, _ := edited.Item("wi-1"); w.Approved != nil {
		t.Fatal("the edit left the approval standing")
	}
	if got := orchestrator.Plan(edited, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("an item edited since its approval planned %+v", got)
	}

	again := o6Approve(t, edited, "wi-1", now.Add(time.Minute))
	a := onlyAction(t, orchestrator.Plan(again, orchestrator.RunView{}, o6Caps(), now.Add(time.Minute)))
	w, _ := again.Item("wi-1")
	if a.Spec != orchestrator.SpecOf(w) || a.Digest != orchestrator.SpecDigest(w) {
		t.Fatalf("the launch after a re-approval carries %+v / %q, not the item as it now stands", a.Spec, a.Digest)
	}
	if a.Spec.Brief != "A different brief." {
		t.Errorf("the launch carries the brief %q", a.Spec.Brief)
	}

	// The state a document may hold and ValidateState deliberately allows: a
	// standing approval whose digest is not the item's current one. The approve
	// op refuses a digest that has moved, so a person never writes one, and the
	// refusal belongs here rather than in the validator — an item whose
	// digested fields a later release digests differently must still load, and
	// what must not happen is starting it under an approval nobody gave for
	// this spec. Nothing else stops it: the item is queued, approved, and its
	// attempt is unspent.
	stale := o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)
	stale.Brief = "A brief the approval never saw."
	if stale.Approved.SpecDigest == orchestrator.SpecDigest(stale) {
		t.Fatal("the item under test is approved over the spec it has")
	}
	if got := orchestrator.Plan(o6Node(t, now, stale), orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("an item whose approval digest has moved planned %+v", got)
	}

	// A person who reads it and approves the spec it now holds is what starts
	// it, and the digest the launch carries is that one.
	fresh := o6Approve(t, o6Node(t, now, stale), "wi-1", now)
	w, _ = fresh.Item("wi-1")
	if w.Approved == nil || w.Approved.SpecDigest != orchestrator.SpecDigest(w) {
		t.Fatalf("the re-approval left %+v", w.Approved)
	}
	launch := onlyAction(t, orchestrator.Plan(fresh, orchestrator.RunView{}, o6Caps(), now))
	if launch.Kind != orchestrator.ActionLaunch || launch.Digest != orchestrator.SpecDigest(w) {
		t.Errorf("the launch under the re-approval is %+v", launch)
	}
}

// TestOneAutomaticRetryUnderTheSameApproval is the red step's second half: one
// approval covers the first attempt and at most one automatic retry, and a
// second failure is where the automatic part stops.
func TestOneAutomaticRetryUnderTheSameApproval(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const first, retry = "20261107-120000-aaaa", "20261107-120000-bbbb"
	o := newO6Starter(t, o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)), now, first, retry)

	if err := o.run(onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))); err != nil {
		t.Fatalf("the first attempt was refused: %v", err)
	}
	// The first attempt leaves the approval standing: the retry is what spends
	// it, and the retry has not happened yet.
	if w, _ := o.s.Item("wi-1"); w.Approved == nil || w.SpentApprovalAt != nil {
		t.Fatalf("the first attempt spent the approval: %+v", w)
	}

	o.s = o6Report(t, o.s, "wi-1", "wi-1:1", orchestrator.OutcomeFailed, orchestrator.PhaseFailed, now.Add(time.Minute))

	// The one retry, under a run id of its own.
	a := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now.Add(2*time.Minute)))
	if a.Kind != orchestrator.ActionRetry || a.Attempt != 2 || a.IntentID != "wi-1:2" {
		t.Fatalf("the plan after a failed first attempt is %+v, not the one retry", a)
	}
	if a.ApprovalAt != approvedAt {
		t.Errorf("the retry spends the approval from %s, and the person's is from %s", a.ApprovalAt, approvedAt)
	}
	o.now = now.Add(2 * time.Minute)
	if err := o.run(a); err != nil {
		t.Fatalf("the retry was refused: %v", err)
	}
	if !slices.Equal(o.l.started, []string{first, retry}) {
		t.Fatalf("the launcher started %v: a retry is its own run", o.l.started)
	}
	// Both starts were handed the item's own spec: a retry is the same work
	// under the same approval, so the brief the first attempt ran is the brief
	// the second one runs.
	approvedSpec := orchestrator.SpecOf(o6Item("wi-1", orchestrator.PhaseQueued, approvedAt))
	if len(o.l.specs) != 2 || o.l.specs[0] != approvedSpec || o.l.specs[1] != approvedSpec {
		t.Errorf("the launcher was handed %+v, and the item's spec is %+v", o.l.specs, approvedSpec)
	}
	// Committing the retry's intent spends the approval in the same write.
	w, _ := o.s.Item("wi-1")
	if w.Approved != nil {
		t.Error("committing the retry left the approval standing")
	}
	if w.SpentApprovalAt == nil || !w.SpentApprovalAt.Equal(approvedAt) {
		t.Errorf("the spent marker reads %v, not the approval the retry spent", w.SpentApprovalAt)
	}
	if n := orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt); n != 2 {
		t.Errorf("%d attempts are committed under the approval, and it covers 2", n)
	}

	// The retry failed too: that is the second failure, and there is no third
	// attempt to be had without a person.
	o.s = o6Report(t, o.s, "wi-1", "wi-1:2", orchestrator.OutcomeFailed, orchestrator.PhaseFailed, now.Add(3*time.Minute))
	if got := orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now.Add(4*time.Minute)); len(got) != 0 {
		t.Fatalf("the plan after the retry failed is %+v, and the approval is spent", got)
	}

	// The retry whose intent was committed in a write whose start never
	// happened: that write spent the approval, so `Approved` is gone and only
	// the spent marker says the attempt was ever allowed. It is the case that
	// re-issued "the approval still stands" alone would abandon, and what
	// recovery does instead is re-issue it under the run id that write
	// recorded.
	item, firstAttempt := o6FailedItem("wi-1", approvedAt, now.Add(-30*time.Minute))
	crash := o6Node(t, now, item)
	crash.Intents = []orchestrator.Intent{firstAttempt}
	crashed := newO6Starter(t, crash, now, retry)
	crashed.l.refuse = errors.New("the platform refused the start")
	if err := crashed.run(onlyAction(t, orchestrator.Plan(crashed.s, orchestrator.RunView{}, o6Caps(), now.Add(time.Minute)))); err == nil {
		t.Fatal("the retry the platform refused reported no error")
	}
	if w, _ := crashed.s.Item("wi-1"); w.Approved != nil || w.SpentApprovalAt == nil {
		t.Fatalf("the refused retry left %+v: the write that recorded it spent the approval", w)
	}

	crashed.l.refuse = nil
	later := now.Add(orchestrator.ClaimTTL)
	again := onlyAction(t, orchestrator.Plan(crashed.s, orchestrator.RunView{}, o6Caps(), later))
	if again.Kind != orchestrator.ActionReissue || again.RunID != retry || again.IntentID != "wi-1:2" {
		t.Fatalf("recovery planned %+v, and the retry that spent the approval is the one to re-issue", again)
	}
	if err := crashed.run(again); err != nil {
		t.Fatalf("re-issuing the spent retry was refused: %v", err)
	}
	if !slices.Equal(crashed.l.asked, []string{retry, retry}) || crashed.m.calls != 1 {
		t.Errorf("recovery asked for %v and minted %d run ids, and the attempt already had one", crashed.l.asked, crashed.m.calls)
	}
	if len(crashed.s.Intents) != 2 || crashed.s.Intents[1].Outcome != "" {
		t.Errorf("the re-issued retry left %+v: the attempt is open, and no third one was committed", crashed.s.Intents)
	}
}

// TestRetryRefusedWhenTheSpecChanged: the retry is the approval's, and the
// approval covers the spec that was read. An edit since makes the item another
// one, and what starts under a new approval is the first attempt under it.
func TestRetryRefusedWhenTheSpecChanged(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	item, first := o6FailedItem("wi-1", approvedAt, now.Add(-30*time.Minute))
	s := o6Node(t, now, item)
	s.Intents = []orchestrator.Intent{first}

	edited := o6Edit(t, s, "wi-1", "A different brief.", now)
	if got := orchestrator.Plan(edited, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("the retry of an edited item planned %+v", got)
	}

	again := o6Approve(t, edited, "wi-1", now.Add(time.Minute))
	a := onlyAction(t, orchestrator.Plan(again, orchestrator.RunView{}, o6Caps(), now.Add(time.Minute)))
	if a.Kind != orchestrator.ActionLaunch || a.Attempt != 2 {
		t.Fatalf("the plan over the new approval is %+v: a new approval is a new allowance, and its first attempt is the item's next", a)
	}
	w, _ := again.Item("wi-1")
	if a.Digest != orchestrator.SpecDigest(w) || a.Digest == first.SpecDigest {
		t.Errorf("the launch carries the digest %q: the edited spec is %q and the failed attempt carried %q", a.Digest, orchestrator.SpecDigest(w), first.SpecDigest)
	}
	if a.Spec.Brief != "A different brief." {
		t.Errorf("the launch carries the brief %q, not the one the person approved", a.Spec.Brief)
	}
}

// TestRefusalIsNotRetried: a launch that never started is not a failure to
// retry — a retry is another attempt, and nothing failed. It is the same
// attempt, re-issued, and what a refusal burns is nothing at all.
func TestRefusalIsNotRetried(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)

	t.Run("a launcher that refused before the start", func(t *testing.T) {
		const run = "20261107-120000-aaaa"
		o := newO6Starter(t, o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)), now, run)
		o.l.refuse = errors.New("the budget pre-check refused the launch")

		if err := o.run(onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))); err == nil {
			t.Fatal("a launch the launcher refused reported no error")
		}
		if len(o.l.started) != 0 {
			t.Fatalf("the launcher started %v", o.l.started)
		}
		if n := orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt); n != 1 {
			t.Errorf("the refused start left %d attempts committed: the one it minted a run id for", n)
		}
		if w, _ := o.s.Item("wi-1"); w.Approved == nil || w.SpentApprovalAt != nil {
			t.Errorf("the refused start spent the approval: %+v", w)
		}
		// What the plan holds next is that same attempt, not a second one.
		later := now.Add(orchestrator.ClaimTTL)
		next := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), later))
		if next.Kind != orchestrator.ActionReissue || next.IntentID != "wi-1:1" || next.RunID != run {
			t.Fatalf("the plan's next move is %+v, not a re-issue of the attempt that never started", next)
		}
	})

	t.Run("the kill switch", func(t *testing.T) {
		s := o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt))
		off := withCaps(func(c *orchestrator.Caps) { c.KillSwitch = true })
		if got := orchestrator.Plan(s, orchestrator.RunView{}, off, now); len(got) != 0 {
			t.Fatalf("the plan with the kill switch on is %+v", got)
		}
		a := onlyAction(t, orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now))
		if a.Kind != orchestrator.ActionLaunch || a.Attempt != 1 {
			t.Fatalf("the plan with the kill switch off is %+v: a switch that stopped a spawn spent nothing", a)
		}
	})

	t.Run("a cap with no room", func(t *testing.T) {
		s := o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt))
		if got := orchestrator.Plan(s, orchestrator.RunView{}, withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 0 }), now); len(got) != 0 {
			t.Fatalf("the plan with no room under the running cap is %+v", got)
		}
		a := onlyAction(t, orchestrator.Plan(s, orchestrator.RunView{}, withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 1 }), now))
		if a.Kind != orchestrator.ActionLaunch {
			t.Fatalf("the plan with room under the running cap is %+v", a)
		}
	})

	t.Run("a commit the node refused", func(t *testing.T) {
		o := newO6Starter(t, o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)), now, "20261107-120000-aaaa", "20261107-120000-bbbb")
		a := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))
		o.fail = orchestrator.ErrStaleRev
		if err := o.run(a); !errors.Is(err, orchestrator.ErrStaleRev) {
			t.Fatalf("the refused commit reported %v", err)
		}
		// Nothing was recorded and nothing started, so the item is exactly as
		// it was: a write that did not land started no run.
		if len(o.s.Intents) != 0 || len(o.l.asked) != 0 {
			t.Fatalf("a commit that failed left %d intents and %v launches", len(o.s.Intents), o.l.asked)
		}
		if w, _ := o.s.Item("wi-1"); w.Approved == nil || w.Attempts != 0 || w.Phase != orchestrator.PhaseQueued {
			t.Errorf("the item after a failed commit is %+v", w)
		}
		o.fail = nil
		if err := o.run(a); err != nil {
			t.Fatalf("the launch after the node came back was refused: %v", err)
		}
		if !slices.Equal(o.l.started, []string{"20261107-120000-bbbb"}) {
			t.Errorf("the launcher started %v", o.l.started)
		}
	})
}

// TestAPreStartRefusalReissuesTheSameIntent: a refusal before the start burns
// no attempt — but the intent is committed, because the run id is minted
// before anything starts, and it is that one attempt that goes out again.
func TestAPreStartRefusalReissuesTheSameIntent(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const run = "20261107-120000-aaaa"
	o := newO6Starter(t, o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)), now, run)
	o.l.refuse = errors.New("the credentials service is unreachable")

	if err := o.run(onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))); err == nil {
		t.Fatal("a launcher that refused reported no error")
	}
	if len(o.s.Intents) != 1 {
		t.Fatalf("the refused start committed %d intents, not the one it minted a run id for", len(o.s.Intents))
	}
	if n := orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt); n != 1 {
		t.Errorf("the attempt is counted %d times, and a pre-start refusal counts once", n)
	}
	if c := orchestrator.CountsOf(o.s, now); c.Running != 1 || c.Hour != 1 || c.Day != 1 {
		t.Errorf("the counts read %+v, and the attempt that never started is one run spent", c)
	}
	committed := o.s.Rev

	// Past the claim window the same intent goes out again: no new intent, no
	// new run id, and nothing written.
	o.l.refuse = nil
	o.now = now.Add(orchestrator.ClaimTTL)
	again := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), o.now))
	if again.Kind != orchestrator.ActionReissue {
		t.Fatalf("the plan's move is %+v, not a re-issue", again)
	}
	if err := o.run(again); err != nil {
		t.Fatalf("the re-issue was refused: %v", err)
	}
	if o.m.calls != 1 {
		t.Errorf("recovery minted %d run ids; the attempt it re-issued already had one", o.m.calls)
	}
	if !slices.Equal(o.l.asked, []string{run, run}) {
		t.Errorf("the launcher was asked for %v, and it is one launch asked twice", o.l.asked)
	}
	if o.s.Rev != committed {
		t.Errorf("the re-issue wrote revision %d over %d: a re-issue commits nothing", o.s.Rev, committed)
	}
	if len(o.s.Intents) != 1 || orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt) != 1 {
		t.Errorf("the re-issue left %+v: one attempt, counted once", o.s.Intents)
	}
	if w, _ := o.s.Item("wi-1"); w.Approved == nil {
		t.Error("the re-issue spent the approval, and no attempt has ended under it")
	}
}

// TestAStartedRunsRefusalCounts: a run that started and reported a refusal is
// an attempt like any other. It spends the run it started, and it is not
// retried — a refusal is not a failure, so what starts the next attempt is a
// person's new approval.
func TestAStartedRunsRefusalCounts(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)

	t.Run("a refusal that ends the first attempt", func(t *testing.T) {
		o := newO6Starter(t, o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)), now, "20261107-120000-aaaa")
		if err := o.run(onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))); err != nil {
			t.Fatalf("the launch was refused: %v", err)
		}
		o.s = o6Report(t, o.s, "wi-1", "wi-1:1", orchestrator.OutcomeRefused, orchestrator.PhaseFailed, now.Add(time.Minute))

		if got := orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now.Add(2*time.Minute)); len(got) != 0 {
			t.Fatalf("a run that reported a refusal planned %+v, and a refusal is never retried", got)
		}
		w, _ := o.s.Item("wi-1")
		if w.Approved == nil {
			t.Error("the refused attempt spent the approval, and only the retry spends it")
		}
		if n := orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt); n != 1 {
			t.Errorf("%d attempts are committed under the approval, and the refusal is one of them", n)
		}
		// What a person has is a new approval, and it starts the next attempt.
		later := now.Add(3 * time.Minute)
		again := o6Approve(t, o.s, "wi-1", later)
		a := onlyAction(t, orchestrator.Plan(again, orchestrator.RunView{}, o6Caps(), later))
		if a.Kind != orchestrator.ActionLaunch || a.Attempt != 2 {
			t.Fatalf("the plan over a new approval is %+v", a)
		}
	})

	t.Run("a refusal that ends the retry", func(t *testing.T) {
		item, first := o6FailedItem("wi-1", approvedAt, now.Add(-30*time.Minute))
		o := newO6Starter(t, o6Node(t, now, item), now, "20261107-120000-bbbb")
		o.s.Intents = []orchestrator.Intent{first}
		if err := o.run(onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))); err != nil {
			t.Fatalf("the retry was refused: %v", err)
		}
		w, _ := o.s.Item("wi-1")
		if w.Approved != nil || w.SpentApprovalAt == nil {
			t.Fatalf("committing the retry did not spend the approval: %+v", w)
		}
		o.s = o6Report(t, o.s, "wi-1", "wi-1:2", orchestrator.OutcomeRefused, orchestrator.PhaseFailed, now.Add(time.Minute))

		if got := orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now.Add(2*time.Minute)); len(got) != 0 {
			t.Fatalf("the plan after a refused retry is %+v, and the approval is spent", got)
		}
		if n := orchestrator.AttemptsUnder(o.s, "wi-1", approvedAt); n != 2 {
			t.Errorf("%d attempts are committed under the approval, and it covers 2", n)
		}
	})
}

// TestAFailedAttemptNeedsANewApproval: the approval covers two attempts, and
// two failures spend it. Nothing the daemon does starts a third.
func TestAFailedAttemptNeedsANewApproval(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	item, first := o6FailedItem("wi-1", approvedAt, now.Add(-30*time.Minute))
	o := newO6Starter(t, o6Node(t, now, item), now, "20261107-120000-aaaa", "20261107-120000-bbbb")
	o.s.Intents = []orchestrator.Intent{first}

	a := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionRetry {
		t.Fatalf("the plan is %+v, not the retry", a)
	}
	if err := o.run(a); err != nil {
		t.Fatalf("the retry was refused: %v", err)
	}
	o.s = o6Report(t, o.s, "wi-1", "wi-1:2", orchestrator.OutcomeFailed, orchestrator.PhaseFailed, now.Add(time.Minute))

	// However many ticks pass, and however eligible the item looks, the
	// allowance is spent and only a person opens another one.
	for _, at := range []time.Time{now.Add(2 * time.Minute), now.Add(orchestrator.ClaimTTL), now.Add(orchestrator.RetentionWindow)} {
		if got := orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), at); len(got) != 0 {
			t.Fatalf("the plan at %s is %+v, and the approval covers two attempts", at, got)
		}
	}

	againAt := now.Add(orchestrator.RetentionWindow)
	o.s = o6Approve(t, o.s, "wi-1", againAt)
	next := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), againAt))
	if next.Kind != orchestrator.ActionLaunch || next.Attempt != 3 || next.IntentID != "wi-1:3" {
		t.Fatalf("the plan over the new approval is %+v: its first attempt is the item's third", next)
	}
	o.now = againAt
	if err := o.run(next); err != nil {
		t.Fatalf("the third attempt was refused: %v", err)
	}
	if n := orchestrator.AttemptsUnder(o.s, "wi-1", againAt); n != 1 {
		t.Errorf("%d attempts are committed under the new approval, and 1 of its 2 is spent", n)
	}
	if w, _ := o.s.Item("wi-1"); w.Approved == nil || w.SpentApprovalAt != nil {
		t.Errorf("the first attempt under the new approval spent it: %+v", w)
	}
}

// TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent: when the crash fell
// before anything could be started and the item is no longer the spec the
// intent was committed for, nothing may be started under it. The intent is
// closed as abandoned, and the item is left where a person can see it rather
// than waiting forever in running.
func TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	item, intent := o6OpenItem("wi-1", "20261107-120000-aaaa", approvedAt, now)
	o := newO6Starter(t, o6Node(t, now, item), now)
	o.s.Intents = []orchestrator.Intent{intent}

	o.s = o6Edit(t, o.s, "wi-1", "A different brief.", now.Add(time.Minute))

	later := now.Add(orchestrator.ClaimTTL)
	a := onlyAction(t, orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), later))
	if a.Kind != orchestrator.ActionAbandon || a.IntentID != intent.ID {
		t.Fatalf("recovery planned %+v, and a spec edited under an approval may not be started", a)
	}
	o.now = later
	if err := o.run(a); err != nil {
		t.Fatalf("abandoning the intent was refused: %v", err)
	}
	if len(o.l.asked) != 0 {
		t.Fatalf("abandoning the intent asked the launcher for %v", o.l.asked)
	}
	if len(o.s.Intents) != 1 || o.s.Intents[0].Outcome != orchestrator.OutcomeAbandoned {
		t.Fatalf("the intent after the abandon is %+v", o.s.Intents)
	}
	// What the intent keeps is the run id that was minted for it and never
	// started: that is the record of the launch nothing made.
	if o.s.Intents[0].RunID != intent.RunID {
		t.Errorf("the abandoned intent lost its run id: %+v", o.s.Intents[0])
	}
	w, _ := o.s.Item("wi-1")
	if w.Phase != orchestrator.PhaseBlocked {
		t.Errorf("the item is %s after its only attempt was abandoned, and a run in running is one no tick picks up", w.Phase)
	}
	if w.Note == "" {
		t.Error("the abandoned attempt left no note saying why")
	}
	editNote := w.Note
	if next := orchestrator.Plan(o.s, orchestrator.RunView{}, o6Caps(), later.Add(time.Hour)); len(next) != 0 {
		t.Fatalf("the plan over an abandoned attempt is %+v", next)
	}

	// The other attempt nothing may start is one whose item a person has
	// stopped. A cancellation is bookkeeping and not part of the spec, so the
	// digest a re-issue reads still matches and the approval the attempt spent
	// is still spent: the phase is what settles it, because a cancelled item is
	// one somebody called the work off and one that never leaves its phase.
	spent := o6Item("wi-2", orchestrator.PhaseQueued, approvedAt)
	spent.RunID = "20261107-120000-cccc"
	spent.Attempts = 1
	// What committing a retry's intent leaves behind: the approval spent and
	// cleared in that same write.
	spent.Approved = nil
	spent.SpentApprovalAt = &approvedAt
	stopped := o6Node(t, now, spent)
	stopped.Intents = []orchestrator.Intent{{
		ID:         orchestrator.IntentID("wi-2", 1),
		RunID:      spent.RunID,
		SpecDigest: orchestrator.SpecDigest(spent),
		ApprovalAt: approvedAt.UTC(),
		At:         now.UTC(),
	}}
	cancelled := orchestrator.PhaseCancelled
	stopped = o6Apply(t, stopped, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: "wi-2", Phase: &cancelled}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: later})

	a = onlyAction(t, orchestrator.Plan(stopped, orchestrator.RunView{}, o6Caps(), later))
	if a.Kind != orchestrator.ActionAbandon {
		t.Fatalf("recovery planned %+v for an item a person stopped, and no window makes that a launch", a)
	}
	o2 := newO6Starter(t, stopped, later)
	if err := o2.run(a); err != nil {
		t.Fatalf("closing the attempt of a cancelled item was refused: %v", err)
	}
	if len(o2.l.asked) != 0 {
		t.Fatalf("the cancelled item asked the launcher for %v", o2.l.asked)
	}
	if w, _ := o2.s.Item("wi-2"); w.Phase != orchestrator.PhaseCancelled {
		t.Errorf("the item is %s after its attempt was closed, and a cancelled item never leaves its phase", w.Phase)
	} else if w.Note == "" || w.Note == editNote {
		t.Errorf("the closed attempt of a cancelled item says %q, and why it was closed is not why an edited one was", w.Note)
	}
	if o2.s.Intents[0].Outcome != orchestrator.OutcomeAbandoned {
		t.Errorf("the intent of the cancelled item reads %+v", o2.s.Intents[0])
	}

	// The other phase an unstarted attempt can be met in is failed, and the
	// phase table has no edge from failed to blocked. The attempt is recorded
	// where the item stands instead: failed is a phase a person reads, and an
	// abandon refused for its phase would leave the attempt open on every tick
	// forever.
	strandedItem := o6Item("wi-3", orchestrator.PhaseFailed, approvedAt)
	strandedItem.RunID = "20261107-120000-dddd"
	strandedItem.Attempts = 1
	stranded := o6Node(t, now, strandedItem)
	stranded.Intents = []orchestrator.Intent{{
		ID:         orchestrator.IntentID("wi-3", 1),
		RunID:      strandedItem.RunID,
		SpecDigest: orchestrator.SpecDigest(strandedItem),
		ApprovalAt: approvedAt.UTC(),
		At:         now.UTC(),
	}}
	stranded = o6Edit(t, stranded, "wi-3", "A different brief.", now.Add(time.Minute))

	a = onlyAction(t, orchestrator.Plan(stranded, orchestrator.RunView{}, o6Caps(), later))
	if a.Kind != orchestrator.ActionAbandon || a.ItemID != "wi-3" {
		t.Fatalf("recovery planned %+v for a failed item with an unstarted attempt", a)
	}
	o3 := newO6Starter(t, stranded, later)
	if err := o3.run(a); err != nil {
		t.Fatalf("closing the attempt of a failed item was refused: %v", err)
	}
	if w, _ := o3.s.Item("wi-3"); w.Phase != orchestrator.PhaseFailed {
		t.Errorf("the item is %s after its attempt was closed, and failed is where a person reads it", w.Phase)
	}
	if o3.s.Intents[0].Outcome != orchestrator.OutcomeAbandoned {
		t.Errorf("the intent of the failed item reads %+v", o3.s.Intents[0])
	}

	// The third reason an unstarted attempt is closed: the item is the spec the
	// intent was committed for, and the approval behind it is not the one the
	// item carries any more — a person gave the item a new go while the attempt
	// had not reported. Nothing may start under the old approval, and the note
	// has to say that rather than blame an edit nobody made.
	reapproved, replaced := o6OpenItem("wi-4", "20261107-120000-eeee", approvedAt, now)
	again := o6Node(t, now, reapproved)
	again.Intents = []orchestrator.Intent{replaced}
	again = o6Approve(t, again, "wi-4", now.Add(2*time.Minute))

	a = onlyAction(t, orchestrator.Plan(again, orchestrator.RunView{}, o6Caps(), later))
	if a.Kind != orchestrator.ActionAbandon || a.ItemID != "wi-4" {
		t.Fatalf("recovery planned %+v for an attempt whose approval a person has replaced", a)
	}
	o4 := newO6Starter(t, again, later)
	if err := o4.run(a); err != nil {
		t.Fatalf("closing the attempt a new approval replaced was refused: %v", err)
	}
	if len(o4.l.asked) != 0 {
		t.Fatalf("the replaced approval asked the launcher for %v", o4.l.asked)
	}
	w, _ = o4.s.Item("wi-4")
	if w.Note == "" || w.Note == editNote {
		t.Errorf("the closed attempt says %q, and the spec the intent was committed for was never edited", w.Note)
	}
	if !strings.Contains(w.Note, "approv") {
		t.Errorf("the closed attempt says %q, and what closed it is a person's new approval", w.Note)
	}
	if w.Approved == nil {
		t.Error("closing the attempt under the old approval took the new one with it")
	}
	// And the person's new go is not lost with the old attempt: what the next
	// tick plans is the first attempt under the approval they gave just now.
	after := onlyAction(t, orchestrator.Plan(o4.s, orchestrator.RunView{}, o6Caps(), later.Add(time.Hour)))
	if after.Kind != orchestrator.ActionLaunch || after.IntentID != orchestrator.IntentID("wi-4", 2) {
		t.Errorf("the plan over the closed attempt is %+v, and the new approval opens an attempt of its own", after)
	}
}

// TestDependencyOrderIsRespected: an item waits for the work it depends on,
// and a dependency the list no longer carries holds it rather than being read
// as done.
func TestDependencyOrderIsRespected(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)

	build := func(t *testing.T, dep orchestrator.WorkItem) orchestrator.State {
		t.Helper()
		dependent := o6Item("wi-2", orchestrator.PhaseQueued, approvedAt)
		dependent.DependsOn = []string{"wi-1"}
		dependent = o6ApprovedBy(dependent, approvedAt)
		return o6Node(t, now, dep, dependent)
	}

	t.Run("a dependency that is done", func(t *testing.T) {
		done := workItemOf("wi-1")
		done.Phase = orchestrator.PhaseDone
		a := onlyAction(t, orchestrator.Plan(build(t, done), orchestrator.RunView{}, o6Caps(), now))
		if a.ItemID != "wi-2" || a.Kind != orchestrator.ActionLaunch {
			t.Fatalf("the plan is %+v, and wi-1 is done", a)
		}
	})

	t.Run("a dependency still going", func(t *testing.T) {
		ready := o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)
		got := orchestrator.Plan(build(t, ready), orchestrator.RunView{}, o6Caps(), now)
		if len(got) != 1 || got[0].ItemID != "wi-1" {
			t.Fatalf("the plan is %+v, and wi-2 waits for wi-1", got)
		}
		// The dependency itself was never approved: nothing starts at all, and
		// wi-2 is not started in its place.
		queued := workItemOf("wi-1")
		if got := orchestrator.Plan(build(t, queued), orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
			t.Fatalf("the plan is %+v, and neither item may start", got)
		}
	})

	t.Run("a dependency the list does not carry", func(t *testing.T) {
		ready := o6Item("wi-1", orchestrator.PhaseQueued, approvedAt)
		dependent := o6Item("wi-2", orchestrator.PhaseQueued, approvedAt)
		dependent.DependsOn = []string{"wi-9"}
		dependent = o6ApprovedBy(dependent, approvedAt)
		s := o6Node(t, now, ready, dependent)
		got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now)
		if len(got) != 1 || got[0].ItemID != "wi-1" {
			t.Fatalf("the plan is %+v, and wi-2 waits for an item the list does not carry", got)
		}
	})
}
