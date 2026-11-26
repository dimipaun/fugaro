package runner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// answeredStage is a stage the credentials service's exhausted answer ends,
// with the action the service decided on it (C3, R19): the answer arrives the
// way a real one does, through the gateway's hand-off, and resumeAt is the
// instant only a pause carries.
func answeredStage(t *testing.T, handle func() *runner.RunHandle, reset time.Time, action string, resumeAt time.Time) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		ex := &gateway.Exhausted{ResetAt: reset, Action: action}
		if !resumeAt.IsZero() {
			ex.ResumeAt = resumeAt
		}
		handle().ServiceAnswer(gateway.ServiceAnswer{Exhausted: ex})
		<-ctx.Done() // the answer ends the stage
		return initOnly(t, req.SessionID), ctx.Err()
	}
}

// noWait is the wait injection every case that must not wait uses: a wait here
// is the bug the action exists to prevent.
func noWait(t *testing.T) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		t.Errorf("the runner waited %s for a reset the answer said not to wait for", d)
		return nil
	}
}

// TestSuspendFollowsTheAnswer: the action the credentials service decided is
// what the run does about an exhausted chain, and the run decides nothing
// about time itself (R19). wait is the in-job wait the runner has always taken
// (D3184); halt ends the run with no_credential and the reset the service
// named; pause is the suspension the service resumes, and until a run can
// suspend itself it ends the run the way halt does, named; and a hotfix run
// never waits and never pauses (R10).
//
// Every case is about the answer and not about the clock: the halt and pause
// cases name a reset inside the job's own timeout, so a run that halted on its
// own clock rather than on the action would wait and be red.
func TestSuspendFollowsTheAnswer(t *testing.T) {
	t.Run("wait waits the reset out in the job", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		var waits []time.Duration
		runner.SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		})
		reset := time.Now().Add(3 * time.Minute)
		resumed := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if !req.Resume {
				t.Error("the stage did not resume the session the answer interrupted")
			}
			return implement("feature")(t, ctx, req)
		}
		rec, err := h.run(t, answeredStage(t, handle, reset, gateway.ActionWait, time.Time{}), resumed, review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Halt != nil {
			t.Fatalf("rec = %+v, err = %v; want a run that waited the reset out", rec, err)
		}
		if len(waits) != 1 {
			t.Fatalf("waits = %v; want one wait, for the reset the service named", waits)
		}
		if n := len(h.agent.calls); n != 3 {
			t.Fatalf("%d agent calls; want the implement stage twice (before and after the wait) and the review", n)
		}
	})

	t.Run("halt ends the run with no_credential", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		runner.SetCredsWait(t, noWait(t))
		reset := time.Now().Add(3 * time.Minute)
		rec, err := h.run(t, answeredStage(t, handle, reset, gateway.ActionHalt, time.Time{}))
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
		assertStoredRecordValidates(t, h.store)
	})

	// The pause the service decided: this release records the answer and ends
	// the run with it, naming the instant the service will resume it at. The
	// suspension itself — save the session, push the checkpoint, write
	// pause.json, tell the service, end paused — is the next release's, and
	// this case is the red step it turns into a suspending run.
	t.Run("pause ends the run until a run can suspend itself", func(t *testing.T) {
		h := newHarness(t, "", nil)
		handle := watchRun(t)
		runner.SetCredsWait(t, noWait(t))
		reset := time.Now().Add(3 * time.Minute)
		resumeAt := reset.Add(30 * time.Minute)
		rec, err := h.run(t, answeredStage(t, handle, reset, gateway.ActionPause, resumeAt))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
			rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
			t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
		}
		if !strings.Contains(rec.Halt.Detail, resumeAt.UTC().Format(time.RFC3339)) {
			t.Errorf("the halt's detail lacks the resume instant the pause carried, %s: %q",
				resumeAt.UTC().Format(time.RFC3339), rec.Halt.Detail)
		}
		if n := len(h.agent.calls); n != 1 {
			t.Fatalf("%d agent calls; the run must not run the stage again", n)
		}
		assertStoredRecordValidates(t, h.store)
	})

	// R10: a hotfix is the run with the least room to spend, so neither a wait
	// that spends the job nor a pause that hands the work to a run of tomorrow
	// is open to it. Whatever the service decided, this run ends — and the halt
	// it would have taken anyway is left to speak for itself.
	t.Run("a hotfix run never waits and never pauses", func(t *testing.T) {
		cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
			"agent:\n", "rigor:\n  default: hotfix\nagent:\n", 1)
		for _, tc := range []struct {
			name, action   string
			resumeAt       func(reset time.Time) time.Time
			theRuleIsNamed bool
		}{
			{"wait", gateway.ActionWait, func(time.Time) time.Time { return time.Time{} }, true},
			{"pause", gateway.ActionPause, func(reset time.Time) time.Time { return reset.Add(30 * time.Minute) }, true},
			{"halt", gateway.ActionHalt, func(time.Time) time.Time { return time.Time{} }, false},
			{"no action at all", "", func(time.Time) time.Time { return time.Time{} }, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t, cfg, nil)
				handle := watchRun(t)
				runner.SetCredsWait(t, noWait(t))
				reset := time.Now().Add(3 * time.Minute)
				rec, err := h.run(t, answeredStage(t, handle, reset, tc.action, tc.resumeAt(reset)))
				if err != nil {
					t.Fatal(err)
				}
				if rec.Status != runstore.StatusHalted || rec.Halt == nil ||
					rec.Halt.Reason != runstore.HaltNoCredential || rec.Halt.Scope != "run" {
					t.Fatalf("rec = %+v, halt = %+v; want a halted run with reason %q", rec, rec.Halt, runstore.HaltNoCredential)
				}
				if got := strings.Contains(rec.Halt.Detail, "hotfix"); got != tc.theRuleIsNamed {
					t.Errorf("the halt's detail names the hotfix rule (%v), want %v: %q",
						got, tc.theRuleIsNamed, rec.Halt.Detail)
				}
				if n := len(h.agent.calls); n != 1 {
					t.Fatalf("%d agent calls; the stage must not run again", n)
				}
				assertStoredRecordValidates(t, h.store)
			})
		}
	})
}
