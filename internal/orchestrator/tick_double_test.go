package orchestrator_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// Double handling is a no-op (E4, D2726).
//
// A tick is level-triggered (D1229): everything it does is derived from the
// node in front of it and from the runs the node recorded, and nothing it does
// depends on what brought it there. One finished run has two roads to a cycle —
// the wait's wake (E1) and the ceiling the daemon's interval elapses into — and
// two daemons of one project can be brought to the same node at once. What they
// all read is one document, so what they all decide is one decision, and the
// second write of it is refused rather than acted on twice.
//
// The two tests here pin exactly that and add no behaviour: one state change
// and one launch of the dependent, whichever road woke the cycle, and one
// launch when two daemons race for one id. E1's wait and G3's fold are what
// they hold to, and O3's lease is the fence in the second.
//
// Red step: none expected — this task pins behaviour the merged tasks leave in
// place. The two mutations that were run and restored are named on the tests
// themselves.

const (
	// doubleSlug is the repository the state's items name, and so the node the
	// wait listens to on the run side.
	doubleSlug = "owner/repo"
	// doubleRun is the finished run behind the dependency, and doubleNext the
	// attempt the dependent's own launch mints.
	doubleRun  = "20261107-110000-aaaa"
	doubleNext = "20261107-110100-bbbb"
	// doubleDebounce is the wait's debounce shortened, as the wakes' own tests
	// shorten it: two seconds of real time per assertion is not a test's to
	// spend.
	doubleDebounce = 20 * time.Millisecond
)

// TestEventAndPollMakeOneDecision is the first half of D2726: the wait's wake
// and the ceiling catch the same finished run, and the two cycles they run are
// one decision — one state change and one launch of the item that waited.
//
// The wake is the wait's own (E1): the run's outcome lands under the day's
// outcomes node, which is a node this daemon listens to because a run writes
// nothing under `orchestrator/`, and the write is what ends the wait. The
// ceiling is nothing at all — the interval elapsing — which is why the second
// cycle is run directly and not from an event.
//
// Mutation (run, restored): drop the outcome guard from both ends of the loop
// — openRunIDs, which asks the reader about every recorded attempt and not only
// the unreported ones, and foldOps, which folds what it is answered — so the
// ceiling's cycle has no record of what the wake's cycle already folded. It
// re-folds the finished run, and the write is refused because the node already
// carries that outcome: the cycle fails instead of writing nothing.
func TestEventAndPollMakeOneDecision(t *testing.T) {
	now := o6Now()
	approved := now.Add(-time.Hour)

	// The position both roads meet: wi-1's run has finished and wi-2 waits on
	// it. The run left no pull request, so the fold leaves wi-1 done rather
	// than in `review` (G3, D2705) and wi-2 is free to start in the same cycle
	// that folds it.
	w1, it1 := o6OpenItem("wi-1", doubleRun, approved, now.Add(-30*time.Minute))
	w2 := o6Item("wi-2", orchestrator.PhaseQueued, approved)
	w2.DependsOn = []string{"wi-1"}
	w2 = o6ApprovedBy(w2, approved)

	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w1, w2}, []orchestrator.Intent{it1}), now, doubleNext)
	h.reader.reports = []orchestrator.RunReport{{RunID: doubleRun, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded}}

	// The wait, over the daemon's own node and the run-side nodes of the
	// repository the state names (E1, D2708).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := rtdbClient(t, h.db)
	wakes := orchestrator.NewWakes(orchestrator.WakeOptions{
		DB:       db,
		Own:      []string{h.node.Path()},
		Agents:   []string{doubleSlug},
		Now:      func() time.Time { return now },
		Debounce: doubleDebounce,
	}).Run(ctx)
	waitForStreams(t, h.db, 3)

	// The run's end, as the registry writes it: one outcome under the day,
	// which is the write E1 exists for.
	outcome := budget.PathOutcome(budget.Day(now), doubleSlug, doubleRun)
	if err := db.Patch(ctx, "", map[string]any{outcome: map[string]any{"status": "succeeded"}}); err != nil {
		t.Fatalf("writing the outcome a run leaves as it ends: %v", err)
	}
	select {
	case <-wakes:
	case <-time.After(10 * time.Second):
		t.Fatal("a run's outcome was written and the wait did not wake, so the cycle below is the ceiling's and not the wake's")
	}

	// The cycle the wake asks for.
	woken := h.step()
	if woken.Folded != 1 || woken.Carried != 1 {
		t.Fatalf("the wake's cycle folded %d runs and carried %d actions, and it was to fold one finished run and start the one item that waited on it", woken.Folded, woken.Carried)
	}
	if got := slices.Clone(h.launcher.started); len(got) != 1 || got[0] != doubleNext {
		t.Fatalf("the wake's cycle started %v, and the dependent's one attempt is %s", got, doubleNext)
	}
	if h.mint.calls != 1 {
		t.Fatalf("the wake's cycle minted %d run ids for one attempt", h.mint.calls)
	}
	after := h.rev()

	// The decision, as the node carries it: the dependency is done and the
	// dependent is running under the attempt the same cycle recorded.
	decided := h.state()
	if w := item(t, decided, "wi-1"); w.Phase != orchestrator.PhaseDone {
		t.Errorf("the finished run left wi-1 %s, and it left no pull request to wait on", w.Phase)
	}
	if w := item(t, decided, "wi-2"); w.Phase != orchestrator.PhaseRunning || w.RunID != doubleNext {
		t.Errorf("wi-2 is %+v, and the cycle that folded its dependency started it", w)
	}

	// The ceiling's road: the interval elapses and the cycle runs over the same
	// node, with nothing new to fold — the run this cycle is about has already
	// reported. The attempt the wake's cycle started is a run that is going, so
	// that is what the reader it asks answers now; the finished run it asks
	// about is the same one the wake's cycle read.
	h.reader.reports = append(h.reader.reports, orchestrator.RunReport{RunID: doubleNext, Started: true})
	second := h.step()
	if second.Folded != 0 || second.Carried != 0 {
		t.Errorf("the ceiling's cycle folded %d runs and carried %d actions over a finished run it had already folded", second.Folded, second.Carried)
	}
	if h.rev() != after {
		t.Errorf("the ceiling's cycle wrote the node, and a level-triggered tick with nothing left to do writes nothing")
	}
	if got := slices.Clone(h.launcher.started); len(got) != 1 {
		t.Errorf("the dependent was started %d times over one finished run: %v", len(got), got)
	}
	if h.mint.calls != 1 {
		t.Errorf("the daemon minted %d run ids for one attempt", h.mint.calls)
	}
	if second.Outcome != orchestrator.CycleInFlight {
		t.Errorf("the ceiling's cycle ended %q, and its only news is a run still going", second.Outcome)
	}
}

// TestTwoDaemonsOneLaunch is the second half of D2726: two daemons of one
// project can be brought to the same node at once, and they make one decision —
// one of them holds the id, and one attempt is started.
//
// The race is the one that matters: the losing daemon reads the node first and
// keeps that reading while the winning one runs a whole cycle, so both decide
// the launch from the same document. What stops the second decision is the
// write: the losing daemon holds no lease, so the intent it recorded is refused
// before anything is started (O3, G9, D1232). The loser mints its run id and
// stops there, which is why its own count is asserted: the decision really was
// made and the fence is what answered it.
//
// Mutation (run, restored): make Node.checkLease a no-op, and the losing
// daemon's write is refused by the state's own ETag instead of by the lease
// (ErrStateMoved) — the launch is still one, and it is this test's reading of
// the refusal that the lease holds.
func TestTwoDaemonsOneLaunch(t *testing.T) {
	now := o6Now()
	approved := now.Add(-time.Hour)

	// The document both daemons read: the dependency's run has ended and has
	// been folded, so the one thing left is the launch the fold freed.
	done := workItemOf("wi-1")
	done.Phase = orchestrator.PhaseDone
	done.RunID = doubleRun
	done.Attempts = 1
	spent := orchestrator.Intent{
		ID:         orchestrator.IntentID("wi-1", 1),
		RunID:      doubleRun,
		SpecDigest: orchestrator.SpecDigest(done),
		ApprovalAt: approved.UTC(),
		At:         now.Add(-30 * time.Minute).UTC(),
		Outcome:    orchestrator.OutcomeSucceeded,
	}
	dependent := o6Item("wi-2", orchestrator.PhaseQueued, approved)
	dependent.DependsOn = []string{"wi-1"}
	dependent = o6ApprovedBy(dependent, approved)

	f := gcpfake.NewRTDB(t)
	ctx := context.Background()
	db := rtdbClient(t, f)
	seed := mustNode(t, db, nil)
	_, etag, err := seed.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty node: %v", err)
	}
	if err := seed.Save(ctx, tickState(t, now, []orchestrator.WorkItem{done, dependent}, []orchestrator.Intent{spent}), etag); err != nil {
		t.Fatalf("seeding the node with the document both daemons read: %v", err)
	}

	// One id, two daemons: both claim it, and one claim lands.
	winnerLease := mustLease(t, db, winnerHolder, clockAt(now))
	loserLease := mustLease(t, db, loserHolder, clockAt(now))
	if won, err := winnerLease.Acquire(ctx); err != nil || !won.Held {
		t.Fatalf("the first daemon's claim is %+v (%v), and it read the id as free", won, err)
	}
	if lost, err := loserLease.Acquire(ctx); err != nil || lost.Held {
		t.Fatalf("the second daemon's claim is %+v (%v), and the id was already taken", lost, err)
	}

	// The loser's read of the node is held open, so it reads the document as it
	// stands and keeps that reading while the winner runs its whole cycle.
	reading := make(chan struct{})
	release := make(chan struct{})
	loserDB := &countingCAS{inner: db, afterRead: func() { close(reading); <-release }}
	winnerNode := mustNode(t, db, winnerLease)
	loserNode := mustNode(t, loserDB, loserLease)

	launcher := &o6Launcher{}
	reports := &tickReader{}
	winnerMint := &o6Mint{t: t, runs: []string{doubleNext}}
	loserMint := &o6Mint{t: t, runs: []string{doubleNext}}
	winner := daemonTick(winnerNode, launcher, reports, winnerMint, now)
	loser := daemonTick(loserNode, launcher, reports, loserMint, now)

	lost := make(chan error, 1)
	go func() {
		_, err := loser.Step(ctx)
		lost <- err
	}()
	<-reading

	rep, err := winner.Step(ctx)
	if err != nil {
		t.Fatalf("the daemon that holds the id ended its cycle as %v", err)
	}
	if rep.Carried != 1 {
		t.Fatalf("the daemon that holds the id carried %d actions, and the one thing to do was start the dependent", rep.Carried)
	}
	close(release)

	var loserErr error
	select {
	case loserErr = <-lost:
	case <-time.After(30 * time.Second):
		t.Fatal("the losing daemon's cycle never finished: a refused write must not spin")
	}
	if !errors.Is(loserErr, orchestrator.ErrLeaseLost) {
		t.Errorf("the losing daemon's cycle ended %v, and a daemon without the lease is refused rather than waited on", loserErr)
	}
	if loserMint.calls != 1 {
		t.Errorf("the losing daemon minted %d run ids, and it decided the same launch the winner did", loserMint.calls)
	}
	if got := slices.Clone(launcher.started); len(got) != 1 || got[0] != doubleNext {
		t.Errorf("two daemons of one project started %v, and one attempt is one run", got)
	}
	if len(launcher.asked) != 1 {
		t.Errorf("the launcher was asked for %v, and the losing daemon's decision must not reach it", launcher.asked)
	}

	held, err := winnerLease.Read(ctx)
	if err != nil {
		t.Fatalf("reading the lease node: %v", err)
	}
	if held.Holder != winnerHolder {
		t.Errorf("the id is held by %q, and the daemon that won it is %q", held.Holder, winnerHolder)
	}
	after, _, err := winnerNode.Load(ctx)
	if err != nil {
		t.Fatalf("reading the node back: %v", err)
	}
	if w := item(t, after, "wi-2"); w.Phase != orchestrator.PhaseRunning || w.RunID != doubleNext {
		t.Errorf("the node carries wi-2 as %+v, and the one launch is what recorded it", w)
	}
}

// daemonTick is one daemon's cycle over one node: the launcher and the run view
// the two daemons share, since what they started and what they read about a run
// are the machine's and not one daemon's, and the mint that names its own run.
func daemonTick(store orchestrator.Store, launcher orchestrator.Launcher, runs orchestrator.RunViewReader, mint *o6Mint, now time.Time) *orchestrator.Tick {
	return &orchestrator.Tick{
		Store:    store,
		Runs:     runs,
		Launcher: launcher,
		Mint:     mint.mint,
		Caps:     o6Caps(),
		Now:      clockAt(now),
	}
}

// waitForStreams waits for the wait to have opened n streams against the fake
// database. A stream registers with the node as it stands and delivers that
// snapshot first, so a write made after this returns is delivered after the
// snapshot and is news rather than part of it.
func waitForStreams(t *testing.T, f *gcpfake.RTDB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.Streams() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the wait opened %d streams and this test writes to the %dth", f.Streams(), n)
		}
		time.Sleep(time.Millisecond)
	}
}
