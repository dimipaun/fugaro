package runner_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// watchRun reaches the run from a test's agent steps: OnRun hands over the
// handle as the run starts, which is before any step runs.
func watchRun(t *testing.T) func() *runner.RunHandle {
	t.Helper()
	var mu sync.Mutex
	var handle *runner.RunHandle
	runner.OnRun(t, func(rh *runner.RunHandle) {
		mu.Lock()
		handle = rh
		mu.Unlock()
	})
	return func() *runner.RunHandle {
		mu.Lock()
		defer mu.Unlock()
		if handle == nil {
			t.Fatal("the run has not started")
		}
		return handle
	}
}

// waitFor polls cond for up to ten seconds and reports a timeout through
// t.Errorf, so it may run on the run's own goroutine — a credential wait's
// stub does, where t.Fatal is not allowed.
func waitFor(t *testing.T, what string, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("timed out waiting for %s", what)
	return false
}

// exhaustedStage is a stage the credential service interrupts: the typed
// answer arrives, the stage ends on it, and the agent reports only what a
// process killed before its result event reports — the session it was
// running in.
func exhaustedStage(t *testing.T, handle func() *runner.RunHandle, reset time.Time) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		handle().NoteExhausted(reset)
		<-ctx.Done() // the answer ends the stage
		return initOnly(t, req.SessionID), ctx.Err()
	}
}

// TestStageWaitsAndResumes is C1.17's red step: the credentials service's
// typed exhausted_until does not fail the stage — the runner ends it, waits
// for the reset the answer names inside the job's own timeout, and runs the
// same stage again on the session that stage had started, with --resume.
func TestStageWaitsAndResumes(t *testing.T) {
	h := newHarness(t, "", nil)
	handle := watchRun(t)
	var waits []time.Duration
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	})
	// Inside the fixture workflow's own timeout (total 5m, reserve 30s).
	reset := time.Now().Add(3 * time.Minute)
	resumed := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		want := h.agent.calls[0].SessionID
		if !req.Resume || req.SessionID != want {
			t.Errorf("the stage resumed %q (resume %v); want the session the interrupted stage ran in, %q", req.SessionID, req.Resume, want)
		}
		if req.Prompt != h.agent.calls[0].Prompt {
			t.Errorf("the resumed stage's prompt changed: %q, was %q", req.Prompt, h.agent.calls[0].Prompt)
		}
		// --resume is the local file Claude Code wrote, not the copy the run
		// uploads at writeback: the wait must leave it where it was.
		if _, err := os.Stat(sessionPath(t, req, req.SessionID)); err != nil {
			t.Errorf("the session file the resumed stage needs: %v", err)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, withSession(exhaustedStage(t, handle, reset), "", "interrupted\n"), resumed, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(waits) != 1 || waits[0] <= 2*time.Minute || waits[0] > 3*time.Minute {
		t.Fatalf("waits = %v; want one wait, for the reset the service named (about 3m)", waits)
	}
	if n := len(h.agent.calls); n != 3 {
		t.Fatalf("%d agent calls; want the implement stage twice (before and after the wait) and the review", n)
	}
}

// TestOAuthOnlyIgnoresThreshold: with no credentials block the project's
// fallback is oauth-only, which ignores the wait-or-switch threshold (D152,
// spec §"Paid fallback policy"): there is nothing to fail over to, so the
// run waits for the earliest reset whatever it says. That decision is the
// service's — it is the service that answers exhausted-until, and no
// threshold may be applied a second time here: a runner that refused the
// wait below would halt a run the service meant to be waited out.
//
// The default threshold is fifteen minutes (projectpolicy.DefaultWaitThreshold)
// and the reset here is forty minutes away, inside a job whose own timeout
// is two hours.
func TestOAuthOnlyIgnoresThreshold(t *testing.T) {
	if got := projectpolicy.DefaultWaitThreshold; got != 15*time.Minute {
		t.Fatalf("the default wait threshold moved to %s; this test's reset must be past it", got)
	}
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 2h, stage: 30m, verify: 5m, finalize_reserve: 5m }", 1)
	h := newHarness(t, cfg, nil)
	handle := watchRun(t)
	var waits []time.Duration
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	})
	reset := time.Now().Add(40 * time.Minute)
	rec, err := h.run(t, exhaustedStage(t, handle, reset),
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if !req.Resume {
				t.Error("the stage did not resume the interrupted session")
			}
			return implement("feature")(t, ctx, req)
		}, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("rec = %+v, err = %v; want a run that waited out the reset", rec, err)
	}
	if len(waits) != 1 || waits[0] <= 39*time.Minute || waits[0] > 40*time.Minute {
		t.Fatalf("waits = %v; want the wait for a reset past the 15m threshold to be taken, whatever its length", waits)
	}
}

// TestWaitBeyondTimeoutHalts: a reset that is not inside the job's own
// remaining time resumes nothing, so the run halts named rather than idling
// into a stage timeout that would blame the wrong limit. The wait itself is
// never taken.
func TestWaitBeyondTimeoutHalts(t *testing.T) {
	h := newHarness(t, "", nil)
	handle := watchRun(t)
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		t.Errorf("the runner waited %s for a reset beyond the job's own timeout", d)
		return nil
	})
	reset := time.Now().Add(24 * time.Hour)
	rec, err := h.run(t, exhaustedStage(t, handle, reset))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
		t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
	}
	for _, want := range []string{reset.UTC().Format(time.RFC3339), "fugaro accounts ls", "credentials.fallback"} {
		if !strings.Contains(rec.Halt.Detail, want) {
			t.Errorf("the halt's detail lacks %q: %q", want, rec.Halt.Detail)
		}
	}
	if n := len(h.agent.calls); n != 1 {
		t.Fatalf("%d agent calls; the stage must not run again", n)
	}
	// The stored record of this halt is what `fugaro diagnose` and every other
	// consumer validates: the reason must be in the schema's enum (C1.17).
	assertStoredRecordValidates(t, h.store)
}

// TestHaltDuringTheInterruptedStageStopsTheWaitBeforeItStarts: a halt
// recorded between the stage's own halt check and the wait — a kill switch,
// a cap or the backend's grace, all of them other goroutines — must end the
// wait before it begins. The sleep is the reset the service named, hours
// away at worst, and no stage may run again after it.
//
// The window is reached deterministically through the record write the
// interrupted stage makes on its way out (stageOnce's own r.save, after the
// check that would have caught a halt recorded any earlier).
func TestHaltDuringTheInterruptedStageStopsTheWaitBeforeItStarts(t *testing.T) {
	h := newHarness(t, "", nil)
	handle := watchRun(t)
	interrupted := false
	runner.SetWriteRecord(t, func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error {
		if interrupted {
			interrupted = false // once: the halt is recorded at this one write
			handle().HaltNow(runstore.Halt{Reason: runstore.HaltKillSwitch, Scope: "run", At: time.Now().UTC()})
		}
		return s.WriteRecord(ctx, rec)
	})
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		t.Errorf("the runner waited %s for a credential after a halt was recorded", d)
		return nil
	})
	reset := time.Now().Add(3 * time.Minute)
	stage := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		interrupted = true
		handle().NoteExhausted(reset)
		<-ctx.Done()
		return initOnly(t, req.SessionID), ctx.Err()
	}
	rec, err := h.run(t, stage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltKillSwitch {
		t.Fatalf("rec = %+v, halt = %+v; want the run halted by the kill switch", rec, rec.Halt)
	}
	if n := len(h.agent.calls); n != 1 {
		t.Fatalf("%d agent calls; the stage must not run again after a halt", n)
	}
}

// TestWaitingUntilWrittenAndCleared: while a cascade run waits, its own
// registry entry carries the instant it expects to resume at — the
// dashboard's "waiting for a credential, expected resume T" (C1.3, D152) —
// and resuming clears it.
func TestWaitingUntilWrittenAndCleared(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "observe", "")
	handle := watchRun(t)
	entry := budget.PathAgent(repoSlug, runID)
	reset := time.Now().Add(3 * time.Minute)
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		// The write rides the heartbeat, so wait for a beat to carry it.
		waitFor(t, "waiting_until on the run's registry entry", func() bool {
			return b.num(entry+"/waiting_until") == reset.UnixMilli()
		})
		return nil
	})
	resumed := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !req.Resume {
			t.Error("the stage did not resume the interrupted session")
		}
		// Resuming clears it: the run is not waiting any more. waitFor, not
		// waitUntil: this runs on the run's own goroutine, where t.Fatal is
		// not the test's to call.
		waitFor(t, "the entry's waiting_until to be cleared", func() bool { return b.entry()["waiting_until"] == nil })
		return implement("feature")(t, ctx, req)
	}
	rec, err := b.run(t, exhaustedStage(t, handle, reset), resumed, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestDeniedCascadeWriteHaltsNamed: the rules downgrade C1.3's marker cannot
// catch — an older Fugaro redeployed a document without the cascade's
// children — meets the run as a permission denial on its registry write. The
// run halts named, with the redeploy that fixes it, and the write is not
// retried (C1.17).
func TestDeniedCascadeWriteHaltsNamed(t *testing.T) {
	// The session's reason is the run's reason: the runner maps a session
	// halt to a run record by the reason string alone (onBudgetHalt).
	if string(budget.ReasonCredsRulesDenied) != string(runstore.HaltCredsRulesDenied) {
		t.Fatalf("budget.ReasonCredsRulesDenied = %q, runstore.HaltCredsRulesDenied = %q: the two must be one reason",
			budget.ReasonCredsRulesDenied, runstore.HaltCredsRulesDenied)
	}
	b := newBK(t, gwConfig(t, ""), "observe", "")
	handle := watchRun(t)
	// Rules deployed before the children existed refuse any write whose value
	// carries one of them, the whole entry included.
	b.db.DenyKeys("waiting_until")
	// The wait holds until the refusal has been answered, and bounded: a run
	// that went on waiting is the bug this test exists for, and it must say
	// so rather than hang.
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			t.Error("the run was still waiting for a credential five seconds on; the refused write must halt it")
			return nil
		}
	})
	reset := time.Now().Add(3 * time.Minute)
	rec, err := b.run(t, exhaustedStage(t, handle, reset))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltCredsRulesDenied || rec.Halt.Scope != "run" {
		t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltCredsRulesDenied)
	}
	if !strings.Contains(rec.Halt.Detail, "fugaro init --firebase") {
		t.Errorf("the halt's detail does not name the redeploy: %q", rec.Halt.Detail)
	}
	if out := b.outcome(); out == nil || out["status"] != "halted" {
		t.Fatalf("outcome = %v; want a halted run", out)
	}
	// The reason is one the published schema knows: a consumer validating
	// result.json must not refuse the record of this halt (C1.17).
	assertStoredRecordValidates(t, b.store)
}
