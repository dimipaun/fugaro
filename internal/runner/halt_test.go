package runner_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// handleBox collects the run's handle for the agent steps of a test.
type handleBox struct{ h *runner.RunHandle }

func newHandleBox(t *testing.T) *handleBox {
	b := &handleBox{}
	runner.OnRun(t, func(h *runner.RunHandle) { b.h = h })
	return b
}

var runCapHalt = runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC),
	Detail: "run spent $5.00 of $5.00"}

// haltAfter runs s, then halts the run, as the gateway's callback would.
func haltAfter(b *handleBox, s step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := s(t, ctx, req)
		if !b.h.HaltNow(runCapHalt) {
			t.Error("HaltNow was refused")
		}
		return res, err
	}
}

// leaveFile writes a file and leaves it uncommitted.
func leaveFile(name string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo partial > "+name)
		return agent.Result{CostUSD: 1}, nil
	}
}

func lastComment(t *testing.T, h *harness) string {
	t.Helper()
	pr := onlyPR(t, h.provider)
	if len(pr.Comments) == 0 {
		t.Fatal("no comment on the PR")
	}
	return pr.Comments[len(pr.Comments)-1]
}

func TestHaltMidImplementOpensDraft(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	rec, err := h.run(t, haltAfter(b, leaveFile("partial.txt")))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("record = %+v", rec)
	}
	if want := "halted: run_cap: run spent $5.00 of $5.00"; rec.Reason != want {
		t.Fatalf("reason = %q, want %q", rec.Reason, want)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.agent.calls))
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
	lines := strings.Split(lastComment(t, h), "\n")
	if !strings.HasPrefix(lines[0], "### Fugaro run `"+runID+"`") || lines[1] != "" || !strings.HasPrefix(lines[2], "**Halted:** the per-run dollar cap was reached (run spent $5.00 of $5.00) at 2026-09-30 10:00:00 UTC — this run spent $1.00.") {
		t.Fatalf("report starts:\n%s", strings.Join(lines[:4], "\n"))
	}
	if !strings.Contains(lastComment(t, h), "budget.per_run_usd") || !strings.Contains(lastComment(t, h), "fugaro run --pr 1") {
		t.Fatalf("report does not say how to continue:\n%s", lastComment(t, h))
	}
	if strings.Contains(lastComment(t, h), "Log tail") {
		t.Fatalf("a halt carries a log tail:\n%s", lastComment(t, h))
	}
}

// TestGovernanceHaltWritesCheckpoint: a governance stop is a halt that leaves
// a checkpoint report on the record — the trigger the runner set from its own
// constants, the runner's words for why the run is over, and the model spend
// the record had reached — and the three fields the stopping agent answers
// (done, next, hard_instructions) are empty until the checkpoint stage is
// wired (T7.3). The record is saved with the report, not only returned, and
// the report is on the pull request before the halted stage has returned.
func TestGovernanceHaltWritesCheckpoint(t *testing.T) {
	b := newHandleBox(t)
	h := prHarness(t, prCfg(t, 2, ""))
	var during string
	halt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.GovernanceHalt(ctx, runstore.HaltFanout, "review spawned 7 sub-agents of 5") {
			t.Error("GovernanceHalt was refused")
			return agent.Result{}, nil
		}
		// Inside the halt itself: a checkpoint that only finalize published
		// would not be here, and "the stop leaves a person something to read"
		// is about the moment the run stops.
		during = onlyPR(t, h.provider).Body
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), halt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltFanout || rec.Halt.Scope != "run" {
		t.Fatalf("halt = %+v", rec.Halt)
	}
	cp := rec.Checkpoint
	if cp == nil {
		t.Fatal("a governance halt left no checkpoint")
	}
	if cp.Trigger != string(runstore.HaltFanout) {
		t.Errorf("trigger = %q, want %q", cp.Trigger, runstore.HaltFanout)
	}
	if cp.WhyOver == "" {
		t.Error("why_over is empty: the runner's own words for the stop are its to fill")
	}
	// implement reported $1.00 and the review stage has not finished, so the
	// record's figure at the halt is that stage's alone.
	if cp.EstimateUSD != 1 {
		t.Errorf("estimate_usd = %v, want the record's 1", cp.EstimateUSD)
	}
	if cp.Done != "" || cp.Next != "" || cp.HardInstructions != "" {
		t.Errorf("checkpoint = %+v, want the agent's three fields empty (T7.3)", cp)
	}
	for _, want := range []string{"**Halted**", "checkpoint `fanout`", "a stage used more sub-agents than the maximum"} {
		if !strings.Contains(during, want) {
			t.Errorf("the section the halt wrote lacks %q:\n%s", want, during)
		}
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Checkpoint == nil || *stored.Checkpoint != *cp {
		t.Fatalf("stored checkpoint = %+v, want %+v", stored.Checkpoint, cp)
	}
}

// TestGovernanceHaltDraftsPR is the headline: a governance halt opens or
// updates the pull request as a draft carrying the checkpoint, and never asks
// for it to be made ready — the reviewers and labels a ready PR carries are
// never applied, whatever the run found before it stopped. Both sections the
// halt writes are checked: the one it writes itself, while the stage it
// stopped is still unwinding, and the run's final one, which replaces it.
func TestGovernanceHaltDraftsPR(t *testing.T) {
	b := newHandleBox(t)
	h := prHarness(t, prCfg(t, 2, ""))
	halt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.GovernanceHalt(ctx, runstore.HaltFanout, "review spawned 7 sub-agents of 5") {
			t.Error("GovernanceHalt was refused")
		}
		during := onlyPR(t, h.provider)
		if !during.Draft {
			t.Error("the halt wrote to a pull request that is ready")
		}
		for _, want := range []string{"**Halted**", "checkpoint during stage `review`", "checkpoint `fanout`", "estimate $1.00"} {
			if !strings.Contains(during.Body, want) {
				t.Errorf("the section the halt wrote lacks %q:\n%s", want, during.Body)
			}
		}
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), halt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Fatal("a governance halt's pull request is ready")
	}
	if len(pr.Reviewers) != 0 || len(pr.Labels) != 0 {
		t.Fatalf("a governance halt's draft carries reviewers %v / labels %v", pr.Reviewers, pr.Labels)
	}
	if n := count(ops(h), "ApplyReady", ""); n != 0 {
		t.Fatalf("ApplyReady was called %d times on a governance halt", n)
	}
	if strings.Contains(pr.Body, "**Ready for review**") {
		t.Fatalf("the pull request says it is ready:\n%s", pr.Body)
	}
	if !strings.Contains(pr.Body, "**Halted**") {
		t.Fatalf("the pull request does not say the run halted:\n%s", pr.Body)
	}
	for _, want := range []string{
		"checkpoint `fanout`",
		"more sub-agents than the maximum",
		"estimate $1.00",
		"review spawned 7 sub-agents of 5",
	} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the pull request's status section lacks %q:\n%s", want, pr.Body)
		}
	}
}

// TestGovernanceHaltOpensTheDraft: when the run's branch is pushed but the
// draft never opened — the boundary's own open failed — the halt opens it, so
// the stop reaches a person even if finalize then cannot (a push that fails
// after an expired token ends the run with no pull request at all). The open
// is asserted inside the halt: an open left to finalize would pass an
// assertion made after the run, which is why this one is made here.
func TestGovernanceHaltOpensTheDraft(t *testing.T) {
	b := newHandleBox(t)
	h := prHarness(t, prCfg(t, 2, ""))
	h.provider.FailEnsure = runner.PRAttempts // the boundary's open fails; the halt's succeeds
	var during runstore.PRRef
	halt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.GovernanceHalt(ctx, runstore.HaltFanout, "review spawned 9 sub-agents of 5") {
			t.Error("GovernanceHalt was refused")
			return agent.Result{}, nil
		}
		pr := onlyPR(t, h.provider)
		if !pr.Draft {
			t.Error("the halt opened a ready pull request")
		}
		during = runstore.PRRef{Number: pr.Number}
		for _, want := range []string{"**Halted**", "checkpoint `fanout`", "estimate $1.00"} {
			if !strings.Contains(pr.Body, want) {
				t.Errorf("the pull request the halt opened lacks %q:\n%s", want, pr.Body)
			}
		}
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), halt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Checkpoint == nil || during.Number == 0 {
		t.Fatalf("record = %+v, the halt's own open = %+v", rec, during)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || pr.Number != during.Number {
		t.Fatalf("the halt opened PR #%d, the run ended with %+v", during.Number, pr)
	}
	for _, want := range []string{"**Halted**", "checkpoint `fanout`", "estimate $1.00"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the pull request the halt opened lacks %q:\n%s", want, pr.Body)
		}
	}
}

// TestGovernanceHaltOpensNoDraftWithEarlyDraftOff: a run whose git.pr.
// early_draft is false has no mid-run pull request by design, and a governance
// halt must not open one for it — not even to carry the checkpoint. The stop
// reaches a person through the run's final pull request, which finalize opens
// the way it opens every early_draft-off PR: with no status section, and the
// checkpoint report where that run's report has always carried it, in the
// record and the stored report.
func TestGovernanceHaltOpensNoDraftWithEarlyDraftOff(t *testing.T) {
	b := newHandleBox(t)
	h := prHarness(t, prCfg(t, 2, ", early_draft: false"))
	halt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.GovernanceHalt(ctx, runstore.HaltFanout, "review spawned 9 sub-agents of 5") {
			t.Error("GovernanceHalt was refused")
			return agent.Result{}, nil
		}
		if prs := h.provider.Snapshot().PRs; len(prs) != 0 {
			t.Errorf("the halt opened a pull request with early_draft off: %+v", prs)
		}
		// The branch is pushed all the same: the halt's draft would have had
		// something to open on, so "no PR" below is a decision and not an
		// accident of nothing being pushed yet.
		if pushedHead(t, h) == "" {
			t.Error("nothing was pushed, so the halt had nothing to open a draft on")
		}
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), halt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Checkpoint == nil {
		t.Fatalf("record = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || strings.Contains(pr.Body, statusBegin) {
		t.Fatalf("finalize's PR for a halted early_draft-off run = %+v", pr)
	}
	if !strings.Contains(rec.Checkpoint.WhyOver, "sub-agents") {
		t.Fatalf("the record's checkpoint = %+v", rec.Checkpoint)
	}
}

// TestGovernanceHaltWhileCheckpointPollRuns: a tripwire fires while the
// stage's checkpoint poll goroutine is alive and mid-poll — the case in which
// the halt and the poll would otherwise both write the pull request's state
// and the run's record (the poll writes pushed_head and saves, then renders a
// status section, from its own goroutine). The halt must leave exactly one
// writer: it stops that poll and waits for it, so everything the poll writes
// is ordered before the halt's own record save and pull-request write. Under
// -race this is the test that pins it.
func TestGovernanceHaltWhileCheckpointPollRuns(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	b := newHandleBox(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	// The poll is held between its push and the pushed_head it saves, which is
	// the window the halt's own save would race.
	runner.SetCheckpointPushedSeam(t, func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	})
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.tick() // sees the tip; nothing is due yet
		g.clock.advance(runner.CheckpointMinGap)
		go g.tick() // this poll pushes, then waits in the seam
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the checkpoint never pushed")
		}
		// The halt, on a goroutine of its own — a tripwire's halt does not
		// arrive on the run goroutine — but one this stage joins before it
		// returns, which is the ordering governanceHalt requires of a caller
		// that is not the run goroutine itself (halt.go). It stops the poll
		// below before it writes.
		halted := make(chan bool, 1)
		go func() { halted <- b.h.GovernanceHalt(ctx, runstore.HaltFanout, "review spawned 9 sub-agents of 5") }()
		// The halt is recorded before the poll is released, so the poll's tick
		// overlaps the halt from there — the overlap this test is about.
		// Waiting on the halt's own answer as well means a refused halt fails
		// here instead of spinning forever, and the deadline bounds the wait.
		poll := time.NewTicker(time.Millisecond)
		defer poll.Stop()
		deadline := time.After(10 * time.Second)
		for !b.h.Halted() {
			select {
			case ok := <-halted:
				t.Fatalf("GovernanceHalt returned %v, so the run never halted", ok)
			case <-deadline:
				t.Fatal("the halt was never recorded")
			case <-poll.C:
			}
		}
		// The poll goes on while the halt waits for it: it writes the pushed
		// head and saves, both before the halt's own writes, so the halt's
		// section is the one that stands on the pull request — a poll's status
		// render returns on the stage context the halt cancelled.
		unblock()
		if !<-halted {
			t.Error("GovernanceHalt was refused")
		}
		return agent.Result{}, nil
	}
	rec, err := g.run(t, implement("feature"), work)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Checkpoint == nil {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(g.logs.String(), "checkpoint pushed") {
		t.Fatalf("no checkpoint pushed, so the halt never overlapped the poll:\n%s", g.logs)
	}
	pr := onlyPR(t, g.provider)
	if !pr.Draft || !strings.Contains(pr.Body, "**Halted**") || !strings.Contains(pr.Body, "checkpoint `fanout`") {
		t.Fatalf("the pull request after the halt and the poll:\n%s", pr.Body)
	}
}

// TestGovernanceHaltOnThePanicPathFindsTheStageStop: the stage panics while its
// checkpoint poll is alive and mid-poll, and the defer that ends the stage
// (runner.go, stageOnce) must stop that poll before it unregisters the stop a
// mid-stage halt reaches for. A defer that unregistered first would leave the
// halt reading no stop: it would skip the wait and write the record and the
// pull request while the poll's in-flight tick still was. The panic path is the
// only one where that defer runs with the poll alive.
//
// The halt is not called here: its own record and pull-request writes are only
// ordered when its caller is the run goroutine or one it joins before them
// (governanceHalt, halt.go), and the panic path joins nothing — a real halt on
// a goroutine of its own races finalize. What the order is about is the read
// the halt makes first, so the test probes that read instead, and asserts the
// defer stopped the poll itself.
func TestGovernanceHaltOnThePanicPathFindsTheStageStop(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	b := newHandleBox(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	// The poll is held between its push and the pushed_head it saves, which is
	// the window a halt's own save would race.
	runner.SetCheckpointPushedSeam(t, func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	})
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	probe := make(chan bool, 1)
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.tick() // sees the tip; nothing is due yet
		g.clock.advance(runner.CheckpointMinGap)
		go g.tick() // this poll pushes, then waits in the seam
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the checkpoint never pushed")
		}
		// What a mid-stage halt reads before it stops the poll, on a goroutine
		// of its own — a tripwire's halt does not arrive on the run goroutine —
		// held back until the panic has reached the stage's own defer, the defer
		// this test is about. The poll is released once the probe has read, so
		// the defer is still inside its own stop when the read lands.
		go func() {
			time.Sleep(100 * time.Millisecond)
			probe <- b.h.StageCkptStopRegistered()
			unblock()
		}()
		panic("boom")
	}
	rec, err := g.run(t, implement("feature"), work)
	if err != nil {
		t.Fatal(err)
	}
	if !<-probe {
		t.Error("the stage's stop was unregistered before the poll it stops was stopped")
	}
	if b.h.StageCkptStopRegistered() {
		t.Error("the stage's stop was still registered after the panic")
	}
	if n := runner.CheckpointersRunning(); n != 0 {
		t.Errorf("%d checkpoint goroutines still alive after the panic", n)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || !strings.Contains(rec.Reason, "panicked") {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(g.logs.String(), "checkpoint pushed") {
		t.Fatalf("no checkpoint pushed, so the panic never overlapped the poll:\n%s", g.logs)
	}
}

func TestHaltLeftoversCommitMessage(t *testing.T) {
	b := newHandleBox(t)
	// The fixture's own 30s reserve is still a wall clock finalize races on a
	// loaded machine, and nothing here is about how long the leftover commit
	// takes.
	cfg := stageTimeoutCfg(t, "timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 2m }", "")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, haltAfter(b, leaveFile("partial.txt")))
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	got := testutil.Git(t, h.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID)
	if want := "fugaro: uncommitted work at halt (the stage was stopped; files may be incomplete)"; got != want {
		t.Fatalf("leftover commit = %q, want %q", got, want)
	}
}

func TestHaltNeverReady(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	// A passing verify and a ship verdict, then the halt.
	rec, err := h.run(t, implement("feature"), haltAfter(b, review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("a halted run's PR is ready")
	}
}

func TestHaltAfterCancelKeepsCancelled(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	var halted bool
	cancelThenHalt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.MarkCancelled() {
			t.Error("the cancel was refused")
		}
		halted = b.h.HaltNow(runCapHalt)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, cancelThenHalt, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if halted || rec.Status != runstore.StatusCancelled || rec.Halt != nil {
		t.Fatalf("halted = %v, record = %+v", halted, rec)
	}
}

func TestCancelAfterHaltKeepsHalted(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	haltThenCancel := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b.h.HaltNow(runCapHalt)
		// The cancel marker lands during the halt grace.
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, haltThenCancel)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || b.h.IsCancelled() {
		t.Fatalf("record = %+v, cancelled = %v", rec, b.h.IsCancelled())
	}
}

func TestHaltWinsOverAgentError(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	failing := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b.h.HaltNow(runCapHalt)
		return agent.Result{IsError: true, Subtype: "error_during_execution", ExitCode: 1}, errors.New("claude exited with code 1")
	}
	rec, err := h.run(t, failing)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || !strings.HasPrefix(rec.Reason, "halted: run_cap") {
		t.Fatalf("record = %+v", rec)
	}
	if strings.Contains(lastComment(t, h), "Log tail") {
		t.Fatal("a halted stage keeps a log tail")
	}
}

func TestHaltRaceFree(t *testing.T) {
	b := newHandleBox(t)
	for i := 0; i < 25; i++ {
		h := newHarness(t, "", nil)
		var haltWon, cancelWon bool
		race := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); haltWon = b.h.HaltNow(runCapHalt) }()
			go func() { defer wg.Done(); cancelWon = b.h.MarkCancelled() }()
			wg.Wait()
			if cancelWon {
				// What the watcher does next: the run's context is cancelled.
				if err := h.store.RequestCancel(context.Background()); err != nil {
					t.Error(err)
				}
				return blockUntilDone(t, ctx, req)
			}
			return agent.Result{}, nil
		}
		rec, err := h.run(t, race)
		if err != nil {
			t.Fatal(err)
		}
		if haltWon == cancelWon {
			t.Fatalf("iteration %d: halt won %v, cancel won %v", i, haltWon, cancelWon)
		}
		switch {
		case haltWon && (rec.Status != runstore.StatusHalted || rec.Halt == nil):
			t.Fatalf("iteration %d: halt won, record = %+v", i, rec)
		case cancelWon && (rec.Status != runstore.StatusCancelled || rec.Halt != nil):
			t.Fatalf("iteration %d: cancel won, record = %+v", i, rec)
		}
	}
}

func TestCancelRecordedAtWatchTime(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	cancelThenHalt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		// The watcher sees the marker (t0) before the halt (t1) while the
		// agent is still exiting.
		for !b.h.IsCancelled() {
			time.Sleep(5 * time.Millisecond)
		}
		if b.h.HaltNow(runCapHalt) {
			t.Error("a halt after a seen cancel was recorded")
		}
		<-ctx.Done()
		return agent.Result{}, ctx.Err()
	}
	rec, err := h.run(t, cancelThenHalt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Halt != nil {
		t.Fatalf("record = %+v", rec)
	}
}

func TestPostLoopCancelDoesNotOverrideHalt(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	last := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, _ := review("ship", 0)(t, ctx, req)
		b.h.HaltNow(runCapHalt)
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * h.deps.CancelPoll) // the run's context is cancelled before the stage returns
		return res, nil
	}
	rec, err := h.run(t, implement("feature"), last)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || b.h.IsCancelled() {
		t.Fatalf("record = %+v, cancelled = %v", rec, b.h.IsCancelled())
	}
}

func TestBootstrapCancelThroughMarkCancelled(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	h.deps.CancelPoll = time.Hour
	if err := h.store.RequestCancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t)
	if err == nil || rec == nil || rec.Status != runstore.StatusCancelled || rec.Stage != "bootstrap" {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !b.h.IsCancelled() {
		t.Fatal("the cancel did not go through markCancelled")
	}
}

func TestFinalizeAssertsOneOfHaltAndCancel(t *testing.T) {
	b := newHandleBox(t)
	runner.SetStrictHaltCheck(t)
	h := newHarness(t, "", nil)
	both := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b.h.ForceHaltAndCancel(runCapHalt)
		return agent.Result{}, nil
	}
	_, err := h.run(t, both)
	if err == nil || !strings.Contains(err.Error(), "both a halt and a cancel") {
		t.Fatalf("err = %v", err)
	}
}

func TestViolationReasonNamesModel(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	const violation = "model claude-x is not pinned for stage review"
	setup := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b.h.SetStageExtra(func(stage string) ([]string, int64) {
			if stage == "review" {
				return []string{violation, "another"}, 0
			}
			return nil, 0
		})
		return implement("feature")(t, ctx, req)
	}
	failing := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{IsError: true, Subtype: "error_during_execution"}, nil
	}
	rec, err := h.run(t, setup, failing)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "stage review: "+violation {
		t.Fatalf("record = %+v", rec)
	}
}

// tokenCfg is the fixture config with agent.max_run_tokens set.
func tokenCfg(t *testing.T, limit string) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	out := strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: 2\n  max_run_tokens: "+limit+"\n", 1)
	if out == cfg {
		t.Fatal("fixture has no review_rounds line")
	}
	return out
}

// withUsage runs s and reports usage.
func withUsage(s step, u agent.Usage, mu map[string]agent.Usage) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := s(t, ctx, req)
		res.Usage, res.ModelUsage = u, mu
		return res, err
	}
}

func TestTokenCapUsesModelUsageWhenLarger(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "100"), nil)
	impl := withUsage(implement("feature"), agent.Usage{Input: 10},
		map[string]agent.Usage{"claude-a": {Input: 10}, "claude-b": {Output: 200}})
	rec, err := h.run(t, impl)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap || rec.Halt.Scope != "run" {
		t.Fatalf("record = %+v", rec)
	}
	if want := "run used 210 tokens of 100"; rec.Halt.Detail != want {
		t.Fatalf("detail = %q, want %q", rec.Halt.Detail, want)
	}
}

func TestTokenCapHaltsAtStageBoundary(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "100"), nil)
	impl := withUsage(implement("feature"), agent.Usage{Input: 40, Output: 20}, nil)
	rev := withUsage(review("changes", 1), agent.Usage{Input: 30, Output: 20}, nil)
	rec, err := h.run(t, impl, rev) // a third (fix) stage would be an unexpected call
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Halt.Reason != runstore.HaltTokenCap {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt.Detail != "run used 110 tokens of 100" || len(rec.Reviews) != 1 || len(h.agent.calls) != 2 {
		t.Fatalf("detail = %q, reviews = %+v, calls = %d", rec.Halt.Detail, rec.Reviews, len(h.agent.calls))
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

func TestTokenCapZeroMeansNone(t *testing.T) {
	h := newHarness(t, "", nil)
	big := agent.Usage{Input: 1 << 40}
	rec, err := h.run(t, withUsage(implement("feature"), big, nil), withUsage(review("ship", 0), big, nil))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestBootstrapHaltIsHaltedExitZero(t *testing.T) {
	// The real trigger: an api-key run that enforces and has no cap.
	g := newGW(t, "", "enforce", "")
	h := g.harness
	b := withBucket(h)
	rec, err := h.run(t) // no stage may run
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap ||
		!strings.HasPrefix(rec.Reason, "halted: no_cap: ") || rec.FinishedAt == nil {
		t.Fatalf("record = %+v", rec)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Status != runstore.StatusHalted || stored.Halt == nil {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("a halted bootstrap opened a PR")
	}
	if refs := testutil.Git(t, h.remote, "for-each-ref", "refs/heads/fugaro/"); refs != "" {
		t.Fatalf("a branch was pushed: %s", refs)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key(repoSlug, "fugaro/"+runID)); ok {
		t.Fatal("the branch lock was taken")
	}
}

func TestHaltDetailIsRedacted(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b.h.HaltNow(runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: runCapHalt.At, Detail: "upstream said test-key"})
		return agent.Result{}, nil
	}
	rec, err := h.run(t, leak)
	if err != nil || rec.Halt == nil || strings.Contains(rec.Halt.Detail, "test-key") || strings.Contains(rec.Reason, "test-key") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestHaltReportAdvice(t *testing.T) {
	at := runCapHalt.At
	rec := &runstore.Record{RunID: runID, Status: runstore.StatusHalted, Outcome: runstore.OutcomeNone, CostUSD: 3.5,
		Halt: &runstore.Halt{Reason: runstore.HaltNoCap, Scope: "run", At: at, Detail: "no cap"}}
	got := runner.Report(rec, "runs/x/", nil)
	if strings.Contains(got, "--pr N") || !strings.Contains(got, "start the run again") || !strings.Contains(got, "this run spent $3.50") {
		t.Fatalf("a bootstrap halt's report:\n%s", got)
	}
	// A cascade run's model figure holds both: its basis is api-list for the
	// whole run, and only the money part of it was spent (C1.16, P12).
	mixed := runstore.ModelOnlyCost(3.5, runstore.BasisAPIList).WithNotional(2.3)
	rec.Cost = &mixed
	got = runner.Report(rec, "runs/x/", nil)
	if !strings.Contains(got, "this run spent $1.20") {
		t.Fatalf("a mixed run's report bills its subscription's dollars:\n%s", got)
	}
	// And one the plan served in full: api-list by basis too, and its whole
	// model figure is the plan's, so it spent nothing either.
	all := runstore.ModelOnlyCost(3.5, runstore.BasisAPIList).WithNotional(3.5)
	rec.Cost = &all
	got = runner.Report(rec, "runs/x/", nil)
	if strings.Contains(got, "this run spent") {
		t.Fatalf("an all-notional cascade run's report bills the plan's dollars:\n%s", got)
	}
	// A subscription run's model figure is notional: never "spent".
	c := runstore.ModelOnlyCost(3.5, runstore.BasisSubscription)
	rec.Cost = &c
	rec.Halt = &runstore.Halt{Reason: runstore.HaltTokenCap, Scope: "run", At: at, Detail: "run used 9 tokens of 5"}
	rec.Outcome, rec.PR = runstore.OutcomeDraft, &runstore.PRRef{Number: 42}
	got = runner.Report(rec, "runs/x/", nil)
	if strings.Contains(got, "spent") || !strings.Contains(got, "fugaro run --pr 42") {
		t.Fatalf("a subscription run's report:\n%s", got)
	}
}

func TestFollowUpHaltBeforePushIsNone(t *testing.T) {
	h := followUpHarness(t, "", nil)
	b := newHandleBox(t)
	h.followUp(t, followID, runID, "Tidy up.")
	closeAndHalt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		h.editState(t, func(st *fake.State) { st.PRs[0].State = gitprov.PRClosed })
		b.h.HaltNow(runCapHalt)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, closeAndHalt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil ||
		!strings.HasPrefix(rec.Reason, "halted: run_cap") || !strings.Contains(rec.Reason, "nothing was pushed") {
		t.Fatalf("record = %+v", rec)
	}
}

func TestHaltRecordedInResult(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	if _, err := h.run(t, haltAfter(b, implement("feature"))); err != nil {
		t.Fatal(err)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Status != runstore.StatusHalted || stored.Halt == nil || *stored.Halt != runCapHalt {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestHaltAdviceNamesLimitSource(t *testing.T) {
	at := runCapHalt.At
	cases := []struct {
		name    string
		reason  runstore.HaltReason
		key     string
		source  string
		want    []string
		notWant []string
	}{
		{"token ceiling", runstore.HaltTokenCap, "max_run_tokens", "ceiling", []string{"project config", "fugaro update", "ignored", "`budget.max_run_tokens`"}, []string{"`agent.max_run_tokens`"}},
		{"token policy", runstore.HaltTokenCap, "max_run_tokens", "policy", []string{"`budget.max_run_tokens`", "project's policy"}, []string{"`agent.max_run_tokens`", "fugaro update"}},
		{"token default branch", runstore.HaltTokenCap, "max_run_tokens", "default-branch", []string{"default branch", "merge", "ignored", "`agent.max_run_tokens`"}, []string{"`budget.max_run_tokens`"}},
		{"token branch", runstore.HaltTokenCap, "max_run_tokens", "branch", []string{"`agent.max_run_tokens`", "this run's branch"}, []string{"`budget.max_run_tokens`"}},
		{"cap ceiling", runstore.HaltRunCap, "per_run_usd", "ceiling", []string{"project config", "fugaro update", "ignored"}, nil},
		{"cap default branch", runstore.HaltRunCap, "per_run_usd", "default-branch", []string{"default branch", "merge", "ignored"}, nil},
		{"nocap default branch", runstore.HaltNoCap, "per_run_usd", "default-branch", []string{"default branch", "merge"}, nil},
	}
	for _, tc := range cases {
		rec := &runstore.Record{RunID: runID, Status: runstore.StatusHalted, Outcome: runstore.OutcomeNone,
			Halt:   &runstore.Halt{Reason: tc.reason, Scope: "run", At: at},
			Policy: &runstore.PolicyRecord{Sources: map[string]string{tc.key: tc.source}}}
		got := runner.Report(rec, "runs/x/", nil)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: report lacks %q:\n%s", tc.name, w, got)
			}
		}
		for _, w := range tc.notWant {
			if strings.Contains(got, w) {
				t.Errorf("%s: report names %s, the spelling of another layer:\n%s", tc.name, w, got)
			}
		}
		if tc.source != "ceiling" && strings.Contains(got, "fugaro update") {
			t.Errorf("%s: points at fugaro update:\n%s", tc.name, got)
		}
	}
}

func TestPolicyLineQuotesIgnoredModels(t *testing.T) {
	rec := &runstore.Record{RunID: runID, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		Policy: &runstore.PolicyRecord{Ignored: []runstore.PolicyIgnored{
			{Key: "allowed_models", Value: "[x](http://evil),@all", Effective: "claude-haiku-4-5", From: "ceiling"}}}}
	got := runner.Report(rec, "runs/x/", nil)
	if !strings.Contains(got, "`[x](http://evil),@all` -> `claude-haiku-4-5`") {
		t.Fatalf("report:\n%s", got)
	}
}

// TestHaltBeforeTheStageIsRegisteredStopsIt: a halt recorded while the stage
// is being set up (here during its credential refresh) finds no running stage
// to cancel; the stage must not then run on until its timeout, and an oauth
// run has no gateway to refuse its calls.
func TestHaltBeforeTheStageIsRegisteredStopsIt(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
	h.provider.Auth = func(time.Duration) gitprov.GitAuth {
		if b.h != nil && b.h.Stage() == "implement" {
			b.h.HaltNow(runCapHalt)
		}
		return gitprov.GitAuth{}
	}
	stopped := false
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo partial > partial.txt && git add -A && git commit -qm partial")
		select {
		case <-ctx.Done():
			stopped = true
		case <-time.After(10 * time.Second):
		}
		return agent.Result{}, ctx.Err()
	}
	rec, err := h.run(t, step)
	if err != nil {
		t.Fatal(err)
	}
	if !stopped || rec.Status != runstore.StatusHalted || rec.Halt == nil {
		t.Fatalf("stopped = %v, record = %+v", stopped, rec)
	}
}
