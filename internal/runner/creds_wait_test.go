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
	"github.com/dimipaun/fugaro/internal/gateway"
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

// serviceExhaustedStage is exhaustedStage with the answer arriving the way it
// does in a real run: not as the runner's own note, but as internal/gateway's
// hand-off of the credentials service's typed answer (C1.15's OnServiceAnswer
// hook). The tier and the credential's name ride along as the gateway reads
// them from the service's headers, so a test that uses this drives the run
// through the wired path rather than around it.
func serviceExhaustedStage(t *testing.T, handle func() *runner.RunHandle, reset time.Time) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		handle().ServiceAnswer(gateway.ServiceAnswer{
			Tier:       gateway.TierB,
			Credential: "repo-shared-b",
			Exhausted: &gateway.Exhausted{ResetAt: reset, Tried: []gateway.Tried{
				{Name: "repo-shared-b", Tier: gateway.TierB, Reason: "exhausted"},
			}},
		})
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

// TestServiceAnswerStartsTheWait: the credentials service's answer reaches the
// run through the gateway and nothing else (C1.15), and what the runner does
// with it is C1.17's wait. The two halves meet here: the answer arrives as the
// gateway hands it over -- the typed exhausted_until, with the tier and the
// credential's name the service sent -- and the run ends the stage in flight,
// waits for the reset inside the job's own timeout, resumes the interrupted
// stage's session, and, when the reset is not inside that timeout, halts named
// rather than idling into a stage timeout that would blame the wrong limit.
func TestServiceAnswerStartsTheWait(t *testing.T) {
	t.Run("a reset inside the job's own timeout waits and resumes", func(t *testing.T) {
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
			if !req.Resume || req.SessionID != h.agent.calls[0].SessionID {
				t.Errorf("the stage resumed %q (resume %v); want the session the interrupted stage ran in, %q",
					req.SessionID, req.Resume, h.agent.calls[0].SessionID)
			}
			return implement("feature")(t, ctx, req)
		}
		rec, err := h.run(t, withSession(serviceExhaustedStage(t, handle, reset), "", "interrupted\n"), resumed, review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Halt != nil {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if len(waits) != 1 || waits[0] <= 2*time.Minute || waits[0] > 3*time.Minute {
			t.Fatalf("waits = %v; want one wait, for the reset the service named (about 3m)", waits)
		}
		if n := len(h.agent.calls); n != 3 {
			t.Fatalf("%d agent calls; want the implement stage twice (before and after the wait) and the review", n)
		}
	})

	t.Run("a reset past the job's own timeout halts with no_credential", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
			t.Errorf("the runner waited %s for a reset beyond the job's own timeout", d)
			return nil
		})
		reset := time.Now().Add(24 * time.Hour)
		rec, err := h.run(t, serviceExhaustedStage(t, handle, reset))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
			rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
			t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
		}
		if !strings.Contains(rec.Halt.Detail, reset.UTC().Format(time.RFC3339)) {
			t.Errorf("the halt's detail lacks the reset the service named, %s: %q", reset.UTC().Format(time.RFC3339), rec.Halt.Detail)
		}
		if n := len(h.agent.calls); n != 1 {
			t.Fatalf("%d agent calls; the stage must not run again", n)
		}
	})
}

// TestInterruptedAttemptsCountTowardsTheRunTotal: an attempt the credentials
// service's exhausted answer ended is a whole agent process that spent what it
// spent, and the run's own token total has to carry it. The caller's own
// countTokens only ever sees the last attempt of a stage, so the interrupted
// ones are counted where they end (stage, C1.15's owed fix from C1.17's
// review). Without that, a run could spend maxCredentialResumes attempts on
// one stage and have the ceiling count one, reaching it a whole stage late.
//
// The figures are chosen so the under-count changes the outcome: the
// interrupted attempt and the resumed one each report 40 tokens and the review
// reports 40, so the run's total is 120 and the 100-token ceiling halts it. A
// total that lost the interrupted attempt's 40 stops at 80 and the run
// succeeds.
func TestInterruptedAttemptsCountTowardsTheRunTotal(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "100"), nil)
	handle := watchRun(t)
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error { return nil })
	reset := time.Now().Add(3 * time.Minute)
	interrupted := withUsage(withSession(serviceExhaustedStage(t, handle, reset), "", "interrupted\n"),
		agent.Usage{Input: 40}, nil)
	resumed := withUsage(implement("feature"), agent.Usage{Input: 40}, nil)
	rev := withUsage(review("ship", 0), agent.Usage{Input: 40}, nil)
	rec, err := h.run(t, interrupted, resumed, rev)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap || rec.Halt.Scope != "run" {
		t.Fatalf("rec = %+v, halt = %+v; want the run halted by its token ceiling", rec, rec.Halt)
	}
	if want := "run used 120 tokens of 100"; rec.Halt.Detail != want {
		t.Fatalf("detail = %q, want %q: the interrupted attempt's tokens are not in the run's total", rec.Halt.Detail, want)
	}
	if n := len(h.agent.calls); n != 3 {
		t.Fatalf("%d agent calls; want the interrupted implement, the resumed one and the review", n)
	}
}

// TestViolationWinsOverTheExhaustedAnswer: what the gateway saw decides the
// stage before the credentials service's answer does. A stage in which a call
// was refused as a violation — a model that is not pinned for the stage, a
// tool the policy forbids, a request shape the gateway will not forward — is a
// stage that must not run again, and a resume would both lose the violation
// from the record and make the same refused call a second time. So the
// violation fails the stage, named, whatever else arrived during it (C1.17).
func TestViolationWinsOverTheExhaustedAnswer(t *testing.T) {
	const violation = "model claude-x is not pinned for stage implement"
	var mu sync.Mutex
	var handle *runner.RunHandle
	runner.OnRun(t, func(rh *runner.RunHandle) {
		mu.Lock()
		handle = rh
		mu.Unlock()
		rh.SetStageExtra(func(stage string) ([]string, int64) {
			if stage == "implement" {
				return []string{violation, "and another"}, 0
			}
			return nil, 0
		})
	})
	h := newHarness(t, "", nil)
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		t.Errorf("the runner waited %s for a credential after a violation; the violation decides the stage", d)
		return nil
	})
	stage := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		mu.Lock()
		rh := handle
		mu.Unlock()
		if rh == nil {
			t.Error("the run has not started")
			return agent.Result{}, nil
		}
		rh.NoteExhausted(time.Now().Add(3 * time.Minute))
		<-ctx.Done() // the answer ends the stage
		return initOnly(t, req.SessionID), ctx.Err()
	}
	rec, err := h.run(t, stage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement: "+violation {
		t.Fatalf("rec = %+v; want the stage failed by the violation", rec)
	}
	if n := len(h.agent.calls); n != 1 {
		t.Fatalf("%d agent calls; a stage with a violation must not run again", n)
	}
}

// TestLateExhaustedAnswerDoesNotRerunAFinishedStage: the answer ends a stage
// the exhaustion stopped, and nothing else. A stage whose agent exited with a
// result event and no error had already done its work when the answer landed,
// and running the same prompt again would spend a whole stage re-doing it
// (C1.17) — so the answer is left to the stage that meets the exhaustion
// itself, and this one is over.
func TestLateExhaustedAnswerDoesNotRerunAFinishedStage(t *testing.T) {
	h := newHarness(t, "", nil)
	handle := watchRun(t)
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		t.Errorf("the runner waited %s for a credential after a stage that finished", d)
		return nil
	})
	late := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		// The agent's own work is done — it commits and writes its session —
		// and only then does the answer land, before the process exits.
		res, err := withSession(implement("feature"), "", "implemented\n")(t, ctx, req)
		handle().NoteExhausted(time.Now().Add(3 * time.Minute))
		return res, err
	}
	rec, err := h.run(t, late, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := len(h.agent.calls); n != 2 {
		t.Fatalf("%d agent calls; want the implement and the review, with no second implement", n)
	}
}

// TestResumeLoopIsBounded: a reset the answer names that has already passed
// resumes the stage at once, so a service that kept answering that way would
// run the stage again and again with no wait between — each attempt a whole
// agent process. The loop is bounded, and the answer past the bound ends the
// run named rather than spinning.
func TestResumeLoopIsBounded(t *testing.T) {
	h := newHarness(t, "", nil)
	handle := watchRun(t)
	waits := 0
	runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		waits++
		if d > 0 {
			t.Errorf("the runner waited %s; the answer named an instant already past", d)
		}
		return nil
	})
	past := time.Now().Add(-time.Minute)
	steps := make([]step, 0, runner.MaxCredentialResumes+1)
	for i := 0; i <= runner.MaxCredentialResumes; i++ {
		// One step per attempt the bound allows; a further call is reported
		// by the harness as an unexpected agent call.
		steps = append(steps, exhaustedStage(t, handle, past))
	}
	rec, err := h.run(t, steps...)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
		t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
	}
	if n := len(h.agent.calls); n != runner.MaxCredentialResumes+1 {
		t.Fatalf("%d agent calls; want the stage run once and resumed %d times, then the run halted", n, runner.MaxCredentialResumes)
	}
	if waits != runner.MaxCredentialResumes {
		t.Fatalf("%d waits; want one for each resume the bound allows", waits)
	}
	for _, want := range []string{"implement", past.UTC().Format(time.RFC3339), "fugaro accounts ls"} {
		if !strings.Contains(rec.Halt.Detail, want) {
			t.Errorf("the halt's detail lacks %q: %q", want, rec.Halt.Detail)
		}
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
