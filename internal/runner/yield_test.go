package runner_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runmsg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is RM4's own: `--now` and `--wrap-up` inside a running coder stage
// (docs/run-messages, rules 2 and 3). The message channel's own tests are in
// messages_test.go — what a message reaches at a stage boundary — and the
// report's Messages section is pinned in report_test.go; what these tests pin
// is the stop, the resume that follows it, and what the record says about it.

// tellFlag writes one message carrying flags into run's prefix, which is what
// `fugaro tell --now` and `--wrap-up` do from the launcher's side (RM1).
func (h *harness) tellFlag(t *testing.T, run, text string, flags ...string) int {
	t.Helper()
	seq, err := runmsg.Put(context.Background(), blobx.Wrap(h.bucket), repoSlug, run,
		runmsg.Message{Version: runmsg.Format, Text: text, SentBy: "dimi", Flags: flags})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// stoppedAt is the step a coder stage runs as when a message stops it: the
// message is written as the stage runs, the step waits for the runner's own
// ask, and reports the stop the agent reports from it — the same shape RM3
// gives a real stage, without a process to signal.
func (h *harness) stoppedAt(run, text, reason string, flags ...string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		t.Helper()
		h.tellFlag(t, run, text, flags...)
		if req.Yield == nil {
			t.Fatal("the coder stage was given no yield request")
		}
		if req.Yield.Bound != 10*time.Minute {
			t.Errorf("the yield's bound is %v, want rule 2's ten minutes", req.Yield.Bound)
		}
		select {
		case <-req.Yield.Ask:
		case <-ctx.Done():
			t.Fatal("the stage ended before a message stopped it")
		case <-time.After(10 * time.Second):
			t.Fatal("the message never stopped the stage")
		}
		return agent.Result{Yielded: true, YieldReason: reason, SessionID: req.SessionID, CostUSD: 1}, nil
	}
}

// duringFlag is during (messages_test.go) with flags on the message.
func (h *harness) duringFlag(run, text string, flags []string, wrap step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		h.tellFlag(t, run, text, flags...)
		return wrap(t, ctx, req)
	}
}

// storedReport is the report the run stored at its end (report.md under the
// run's prefix), which is what diagnose and the follow-up machinery read.
func storedReport(t *testing.T, h *harness) string {
	t.Helper()
	b, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatalf("reading the stored report: %v", err)
	}
	return string(b)
}

// TestNowResumesSameSession: a `now` message that reaches a coder stage stops
// it at a quiet point and the stage goes on in the same session with the
// message in its prompt — `--resume`, not a fresh turn (rule 2, D2945). The
// record says the message was delivered into the stage it stopped.
func TestNowResumesSameSession(t *testing.T) {
	const text = "use the existing helper in internal/foo"
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		h.stoppedAt(runID, text, agent.YieldAtQuietPoint, runmsg.FlagNow),
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if !req.Resume {
				t.Error("the segment a message stopped was not resumed: the stage started over")
			}
			if req.SessionID != h.agent.calls[0].SessionID {
				t.Errorf("the resumed segment's session is %q, want the stopped one's %q",
					req.SessionID, h.agent.calls[0].SessionID)
			}
			if !strings.Contains(req.Prompt, text) {
				t.Errorf("the resumed prompt carries no message:\n%s", req.Prompt)
			}
			if !strings.Contains(req.Prompt, "Add a feature") {
				t.Errorf("the resumed prompt is not the brief plus the message:\n%s", req.Prompt)
			}
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	delivered(t, rec, 1, "implement")
	if d := delivery(t, rec, 1); d.Reason != "" {
		t.Errorf("the record's message 1 carries the reason %q: this stop found a quiet point", d.Reason)
	}
	if !eq(stageNames(rec), "implement", "implement", "review") {
		t.Fatalf("the run's stages are %v, want the stopped segment and its resume", stageNames(rec))
	}
}

// TestNowCarriesPendingMessages: the turn a `now` resumes is one of rule 1's
// delivery points, so a message already waiting when the stop fires is carried
// into it too, in number order behind nothing and ahead of no one — the stop's
// own message last, not first. It would otherwise wait for a boundary a stage
// that ends on its next stop may never reach, and be recorded undelivered
// although a coder turn resumed after it arrived.
func TestNowCarriesPendingMessages(t *testing.T) {
	const (
		first  = "the fixture's build.sh wants a --strict flag"
		second = "use the existing helper in internal/foo"
	)
	h := newHarness(t, "", nil)
	h.messagesOn()
	var resumed string
	rec, err := h.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			h.tell(t, runID, first)
			return h.stoppedAt(runID, second, agent.YieldAtQuietPoint, runmsg.FlagNow)(t, ctx, req)
		},
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			resumed = req.Prompt
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	i, j := strings.Index(resumed, first), strings.Index(resumed, second)
	if i < 0 || j < 0 {
		t.Fatalf("the resumed prompt carries messages at %d and %d, want both the waiting one and the stop's:\n%s",
			i, j, resumed)
	}
	if i > j {
		t.Errorf("the resumed prompt puts the stop's message ahead of the one that was already waiting:\n%s", resumed)
	}
	delivered(t, rec, 1, "implement")
	delivered(t, rec, 2, "implement")
	if !eq(stageNames(rec), "implement", "implement", "review") {
		t.Fatalf("the run's stages are %v, want the stopped segment and its resume", stageNames(rec))
	}
}

// TestNowDuringReviewWaits: a `now` message that arrives while a stage that
// carries no messages runs — a review — waits for the next coder stage, which
// is what makes the flag a request and not an interrupt (rule 2). The review
// is handed no ask at all.
func TestNowDuringReviewWaits(t *testing.T) {
	const text = "the fixture's build.sh wants a --strict flag"
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		implement("feature"),
		h.duringFlag(runID, text, []string{runmsg.FlagNow}, review("changes", 1)),
		fixStep,
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.agent.calls) < 2 || h.agent.calls[1].Yield != nil {
		t.Fatalf("the review stage was given a yield request: %+v", h.agent.calls[1].Yield)
	}
	if !strings.Contains(promptOf(t, h, "asked for these fixes"), text) {
		t.Errorf("the fix prompt carries no message:\n%s", promptOf(t, h, "asked for these fixes"))
	}
	delivered(t, rec, 1, "fix")
}

// TestWrapUpEndsCoderStage: a `wrap_up` message reaches the coder stage the
// same way, with rule 3's instruction, and the stage ends when that turn ends:
// the run goes on to its reviews and its finalize and opens the pull request
// (D2946).
func TestWrapUpEndsCoderStage(t *testing.T) {
	const text = "wrap it up, I need the PR now"
	h := newHarness(t, "", nil)
	h.messagesOn()
	var resumed string
	rec, err := h.run(t,
		h.stoppedAt(runID, text, agent.YieldAtQuietPoint, runmsg.FlagWrapUp),
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			resumed = req.Prompt
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	for _, want := range []string{text, "Wrap up now", "write `pr.md`"} {
		if !strings.Contains(resumed, want) {
			t.Errorf("the wrap-up turn's prompt lacks %q:\n%s", want, resumed)
		}
	}
	d := delivery(t, rec, 1)
	if d.State != runmsg.StateDelivered || d.Stage != "implement" || len(d.Flags) != 1 || d.Flags[0] != runmsg.FlagWrapUp {
		t.Fatalf("the record's message 1 = %+v, want it delivered into implement with its flag", d)
	}
	if !eq(stageNames(rec), "implement", "implement", "review") {
		t.Fatalf("the run's stages are %v", stageNames(rec))
	}
}

// TestNowYieldIsNotAFailure: the stop is the runner's own doing and not a
// stage that failed — no failure reason, no halt, and a run that ends the way
// it would have without the message.
func TestNowYieldIsNotAFailure(t *testing.T) {
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		h.stoppedAt(runID, "use the helper", agent.YieldAtQuietPoint, runmsg.FlagNow),
		implement("feature"),
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	if rec.Halt != nil {
		t.Fatalf("the stop halted the run: %+v", rec.Halt)
	}
	for _, s := range rec.Stages {
		if s.Name == "implement" && s.ModelUSD != 0 && s.ModelUSD != 1 {
			t.Errorf("an implement row carries %v, which is neither the stop's $1 nor the resume's", s.ModelUSD)
		}
	}
}

// TestYieldSpendCounted: the segment a message stopped is charged for what it
// spent and what it used, exactly as a stage attempt is: the run's own token
// ceiling reads it, so a message cannot buy the stage a second free run
// (rule 2).
func TestYieldSpendCounted(t *testing.T) {
	// The oauth credential: the dollars a segment spent are its own report,
	// which is the run's figure only where no gateway's ledger is (AB1b,
	// D3009, C1.16).
	h := oauthHarness(t, tokenCfg(t, "100"), nil)
	h.messagesOn()
	rec, err := h.run(t,
		withUsage(h.stoppedAt(runID, "use the helper", agent.YieldAtQuietPoint, runmsg.FlagNow),
			agent.Usage{Input: 80}, nil),
		withUsage(implement("feature"), agent.Usage{Input: 30}, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	// 80 from the stopped segment and 30 from the one that carried the
	// message: the ceiling is the run's own, and it fires on both.
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap {
		t.Fatalf("record = %+v, want the run halted on its token ceiling", rec)
	}
	if want := "run used 110 tokens of 100"; rec.Halt.Detail != want {
		t.Fatalf("halt detail = %q, want %q", rec.Halt.Detail, want)
	}
	// Both segments' dollars are the run's: $1 the stop reported and $1 the
	// resume, and no review ran.
	if rec.CostUSD != 2 {
		t.Fatalf("cost_usd = %v, want both segments' $2", rec.CostUSD)
	}
}

// TestResumedBudgetIsRemainder: the segment a message resumed runs under what
// is left of the stage's model budget and not a second full one (rule 2).
func TestResumedBudgetIsRemainder(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "  review_rounds: 2\n", "  review_rounds: 2\n  max_budget_usd: 25\n", 1)
	// The remainder is read off what the run has spent, so the stages' own
	// reports have to be the run's: oauth, as above.
	h := oauthHarness(t, cfg, nil)
	h.messagesOn()
	var first, resumed float64
	rec, err := h.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			first = req.MaxBudgetUSD
			return h.stoppedAt(runID, "use the helper", agent.YieldAtQuietPoint, runmsg.FlagNow)(t, ctx, req)
		},
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			resumed = req.MaxBudgetUSD
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != 25 {
		t.Fatalf("the stage's first segment ran under --max-budget-usd %v, want the configured 25", first)
	}
	// The stopped segment reported $1 of the $25, so the resumed one has $24.
	if resumed != 24 {
		t.Fatalf("the resumed segment ran under --max-budget-usd %v, want the stage's remainder 24", resumed)
	}
	if !eq(stageNames(rec), "implement", "implement", "review") {
		t.Fatalf("the run's stages are %v", stageNames(rec))
	}
}

// TestSpentBudgetEndsTheStage: a stage with nothing left of its model budget
// does not resume on a stop. The remainder is what the resumed segment would
// run under, and one `--max-budget-usd` cannot state is not a cap at all —
// the flag is left off the command when it is zero (internal/agent/agent.go),
// so the segment would run uncapped, which is worse for the run than not
// resuming it (rule 2). The coder stage ends there, and the message waits for
// a coder stage that never comes, which the record says.
//
// The stopped segment's own usage is charged exactly once (it reports 60 into
// a ceiling of 100, and the run goes on): a segment the stage does not resume
// from is counted by the stage's caller, which is the only count it gets.
func TestSpentBudgetEndsTheStage(t *testing.T) {
	for _, tt := range []struct {
		name   string
		budget float64
		spent  float64
	}{
		{"the whole budget spent", 1, 1},
		{"less left than a cap can state", 10, 9.996},
	} {
		t.Run(tt.name, func(t *testing.T) {
			budget := strconv.FormatFloat(tt.budget, 'f', -1, 64)
			cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "  review_rounds: 2\n",
				"  review_rounds: 2\n  max_budget_usd: "+budget+"\n  max_run_tokens: 100\n", 1)
			// What is left of the stage's budget is read off what the run has
			// spent, so the segment's own figure must be the run's: oauth, as
			// above.
			h := oauthHarness(t, cfg, nil)
			h.messagesOn()
			var capped float64
			rec, err := h.run(t,
				func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
					capped = req.MaxBudgetUSD
					res, err := implement("feature")(t, ctx, req)
					if err != nil {
						return res, err
					}
					res.Usage = agent.Usage{Input: 60}
					if req.Yield == nil {
						t.Fatal("the coder stage was given no yield request")
					}
					h.tellFlag(t, runID, "use the helper", runmsg.FlagNow)
					select {
					case <-req.Yield.Ask:
					case <-ctx.Done():
						t.Fatal("the stage ended before a message stopped it")
					}
					res.CostUSD, res.Yielded, res.YieldReason = tt.spent, true, agent.YieldAtQuietPoint
					return res, nil
				},
				review("ship", 0),
			)
			if err != nil {
				t.Fatal(err)
			}
			mustReady(t, rec, err)
			if capped != tt.budget {
				t.Fatalf("the stage ran under --max-budget-usd %v, want the configured %v", capped, tt.budget)
			}
			if got := stageNames(rec); !eq(got, "implement", "review") {
				t.Fatalf("the run's stages are %v, want the stopped stage and its review, with no segment resumed after it", got)
			}
			if d := delivery(t, rec, 1); d.State != runmsg.StateUndelivered {
				t.Fatalf("the record's message 1 is %q, want undelivered: there was no budget left for a coder stage to resume under",
					d.State)
			}
		})
	}
}

// TestResumeKeepsDeadline: a stop does not restart the stage's clock — the
// segment a message resumed runs under the deadline the stage's first segment
// was given (rule 2).
func TestResumeKeepsDeadline(t *testing.T) {
	h := newHarness(t, "", nil)
	h.messagesOn()
	var first, resumed time.Time
	deadlineOf := func(t *testing.T, ctx context.Context) time.Time {
		t.Helper()
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("the stage has no deadline: the workflow's timeouts.stage does not bind it")
		}
		return dl
	}
	rec, err := h.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			first = deadlineOf(t, ctx)
			return h.stoppedAt(runID, "use the helper", agent.YieldAtQuietPoint, runmsg.FlagNow)(t, ctx, req)
		},
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			resumed = deadlineOf(t, ctx)
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Equal(first) {
		t.Fatalf("the resumed segment's deadline is %v, want the stage's own %v", resumed, first)
	}
	if !eq(stageNames(rec), "implement", "implement", "review") {
		t.Fatalf("the run's stages are %v", stageNames(rec))
	}
}

// TestWrapUpBounded: a wrap-up turn that goes on is stopped at rule 3's bound,
// and the coder stage ends there as succeeded — the run goes on to its reviews
// and its finalize rather than waiting for the turn (D2946). The turn is also
// given no quiet-point wait of its own: its fifteen minutes are the whole of
// its clock, and rule 2's ten on top of them would make twenty-five.
func TestWrapUpBounded(t *testing.T) {
	const bound = 200 * time.Millisecond
	h := newHarness(t, "", nil)
	h.messagesOn()
	h.deps.WrapUpBound = bound
	var waited time.Duration
	rec, err := h.run(t,
		h.stoppedAt(runID, "wrap it up", agent.YieldAtQuietPoint, runmsg.FlagWrapUp),
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if req.Yield == nil {
				t.Fatal("the wrap-up turn was given no yield request")
			}
			if req.Yield.Bound != time.Nanosecond {
				t.Errorf("the wrap-up turn waits %v for a quiet point, want it stopped at once: its own bound is the whole of its clock",
					req.Yield.Bound)
			}
			// The work a wrap-up turn does before it goes on for too long: it
			// commits what it has, and the bound then ends the turn and the
			// stage with it (rule 3).
			res, err := implement("feature")(t, ctx, req)
			if err != nil {
				return res, err
			}
			start := time.Now()
			select {
			case <-req.Yield.Ask:
				waited = time.Since(start)
				res.Yielded, res.YieldReason = true, agent.YieldAtBound
				return res, nil
			case <-ctx.Done():
				t.Fatal("the wrap-up turn was not stopped at its bound")
			}
			return res, nil
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	if waited < bound/2 || waited > 5*time.Second {
		t.Fatalf("the wrap-up turn ran %v before it was stopped, want its bound (%v)", waited, bound)
	}
	delivered(t, rec, 1, "implement")
}

// TestForcedStopStartsFresh: a stop that reached the bound may leave a session
// the agent cannot resume. When it cannot, the stage starts again in a fresh
// session with the message and the instruction to read the repository first,
// and the record says why the message reached that session (rule 2's
// fallback).
func TestForcedStopStartsFresh(t *testing.T) {
	const text = "use the helper in internal/foo"
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		h.stoppedAt(runID, text, agent.YieldAtBound, runmsg.FlagNow),
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			return agent.Result{}, fmt.Errorf("%w: claude exited with code 1, saying it has no session %s",
				agent.ErrNoSession, req.SessionID)
		},
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if req.Resume {
				t.Error("the fresh attempt resumed the session the stop lost")
			}
			if req.SessionID == h.agent.calls[0].SessionID {
				t.Error("the fresh attempt reused the session the stop lost")
			}
			for _, want := range []string{text, "`git log`"} {
				if !strings.Contains(req.Prompt, want) {
					t.Errorf("the fresh attempt's prompt lacks %q:\n%s", want, req.Prompt)
				}
			}
			return implement("feature")(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	d := delivery(t, rec, 1)
	if d.State != runmsg.StateDelivered || d.Stage != "implement" || d.Reason != "session lost at forced stop" {
		t.Fatalf("the record's message 1 = %+v, want it delivered with the reason the session was lost", d)
	}
	if !eq(stageNames(rec), "implement", "implement", "implement", "review") {
		t.Fatalf("the run's stages are %v", stageNames(rec))
	}
}

// TestYieldNeverCrossesAccounts: A7b's rule — a session is never resumed
// across accounts — holds for the resume a message asks for, because that
// resume is r.stage's own in-stage attempt and not a second stage: the segment
// the message resumed ran on the credential the stopped one ran on, in the
// session it left, with the record's two rows naming one served-by. A resume
// built as a second stage would re-resolve the stage's account at its start
// and could land the same session on another credential, which is the shape
// §11.3 keeps out of the record.
func TestYieldNeverCrossesAccounts(t *testing.T) {
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		h.stoppedAt(runID, "use the helper", agent.YieldAtQuietPoint, runmsg.FlagNow),
		implement("feature"),
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	var rows []runstore.StageTiming
	for _, s := range rec.Stages {
		if s.Name == "implement" {
			rows = append(rows, s)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("the record has %d implement rows, want the stopped segment and its resume: %v", len(rows), stageNames(rec))
	}
	if rows[0].ServedBy == "" || rows[0].ServedBy != rows[1].ServedBy {
		t.Fatalf("the two segments were served by %q and %q, want one credential", rows[0].ServedBy, rows[1].ServedBy)
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if a, b := envValue(h.agent.calls[0].Env, k), envValue(h.agent.calls[1].Env, k); a != b {
			t.Errorf("the resumed segment runs with %s=%q, the stopped one with %q", k, b, a)
		}
	}
	if h.agent.calls[1].SessionID != h.agent.calls[0].SessionID {
		t.Fatalf("the resumed segment's session is %q, want the stopped one's %q",
			h.agent.calls[1].SessionID, h.agent.calls[0].SessionID)
	}
}

// TestNowInFixWidens: a `now` message that resumes a fix carries rule 4's
// widening sentence with the message — a message may ask for more than the
// findings do (D2943).
func TestNowInFixWidens(t *testing.T) {
	const text = "while you are there, rename the helper in internal/foo too"
	h := newHarness(t, "", nil)
	h.messagesOn()
	var resumed string
	rec, err := h.run(t,
		implement("feature"),
		review("changes", 1),
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			res, err := h.stoppedAt(runID, text, agent.YieldAtQuietPoint, runmsg.FlagNow)(t, ctx, req)
			return res, err
		},
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			resumed = req.Prompt
			return fixStep(t, ctx, req)
		},
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{text, "may ask for more than the findings do"} {
		if !strings.Contains(resumed, want) {
			t.Errorf("the resumed fix's prompt lacks %q:\n%s", want, resumed)
		}
	}
	delivered(t, rec, 1, "fix")
}

// TestEndingsReadOnceMore: an ending that does not reach finalize reads the
// message prefix once more before it writes its report, so a message written
// after the last poll is recorded pending rather than lost (rule 5, F18). A
// clean check gate on a first run is such an ending: it ends before any stage,
// with nothing that would have read the queue.
func TestEndingsReadOnceMore(t *testing.T) {
	const text = "stop what you are doing and tell me what you found"
	gated := embeddedTask("gated", "version: 1\nname: gated\nsteps:\n  - check: { command: test }\n  - review: {}\n")
	h := newHarness(t, "", recipeSpec(gated))
	h.messagesOn()
	// No poll at all: what the record holds at the end is the ending's own
	// read and nothing else.
	h.deps.CancelPoll = time.Hour
	h.tellFlag(t, runID, text, runmsg.FlagNow)
	rec, err := h.run(t)
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Gate != "clean" {
		t.Fatalf("gate = %q, want the run ended by its clean check gate", rec.Gate)
	}
	d := delivery(t, rec, 1)
	if d.State != runmsg.StateUndelivered {
		t.Fatalf("the record's message 1 = %+v, want it recorded undelivered by the ending's own read", d)
	}
	if report := storedReport(t, h); !strings.Contains(report, text) || !strings.Contains(report, "#### Messages") {
		t.Errorf("the stored report does not carry the message:\n%s", report)
	}
}

// TestHaltReportCarriesMessages: a run that halts reads once more at its end
// and its report carries what became of every message — here one that arrived
// with no coder stage left to take it (rule 5, D2944).
func TestHaltReportCarriesMessages(t *testing.T) {
	const text = "add the flag to build.sh as well"
	h := newHarness(t, tokenCfg(t, "100"), nil)
	h.messagesOn()
	h.deps.CancelPoll = time.Hour
	rec, err := h.run(t,
		withUsage(h.during(runID, text, implement("feature")), agent.Usage{Input: 200}, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap {
		t.Fatalf("record = %+v, want the run halted on its token ceiling", rec)
	}
	d := delivery(t, rec, 1)
	if d.State != runmsg.StateUndelivered || d.Reason != "no coder stage was left when the run ended" {
		t.Fatalf("the record's message 1 = %+v, want it undelivered with rule 5's reason", d)
	}
	report := storedReport(t, h)
	for _, want := range []string{"#### Messages", text, "`undelivered`"} {
		if !strings.Contains(report, want) {
			t.Errorf("the stored report lacks %q:\n%s", want, report)
		}
	}
}

// TestFlagsListedAfterRM4: the run's record lists the flags this runner
// honours, which is what `fugaro tell` reads before it sends one (F25).
func TestFlagsListedAfterRM4(t *testing.T) {
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Messages == nil {
		t.Fatal("the record carries no messages block at all")
	}
	if !eq(rec.Messages.Flags, string(runmsg.FlagNow), string(runmsg.FlagWrapUp)) {
		t.Fatalf("the record's messages.flags = %v, want both flags this runner honours", rec.Messages.Flags)
	}
}

// TestCancelWinsOverNow: a cancel that lands while the stage waits for its
// quiet point wins over the message — the run ends cancelled, the message
// reached no resumed turn, and the record says it was never delivered
// (rule 2).
func TestCancelWinsOverNow(t *testing.T) {
	const text = "use the helper"
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			h.tellFlag(t, runID, text, runmsg.FlagNow)
			select {
			case <-req.Yield.Ask:
			case <-time.After(10 * time.Second):
				t.Fatal("the message never reached the stage")
			}
			if err := h.store.RequestCancel(context.Background()); err != nil {
				t.Fatal(err)
			}
			// The agent is told to stop and the run is cancelled in the same
			// breath: the cancel decides, and the stage ends as it does.
			<-ctx.Done()
			return agent.Result{Yielded: true, YieldReason: agent.YieldAtQuietPoint, SessionID: req.SessionID}, ctx.Err()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled {
		t.Fatalf("record = %+v, want the run cancelled", rec)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("the run made %d agent calls, want the cancelled stage's one", len(h.agent.calls))
	}
	if d := delivery(t, rec, 1); d.State != runmsg.StateUndelivered {
		t.Fatalf("the record's message 1 = %+v, want it undelivered: the cancel won", d)
	}
}

// TestNowBoundRecorded: a stop that reached rule 2's bound with no quiet point
// stops the segment where it stands, and the record says so beside the message
// it delivered.
func TestNowBoundRecorded(t *testing.T) {
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		h.stoppedAt(runID, "use the helper", agent.YieldAtBound, runmsg.FlagNow),
		implement("feature"),
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	d := delivery(t, rec, 1)
	if d.State != runmsg.StateDelivered || d.Stage != "implement" || d.Reason != "no quiet point in 10m" {
		t.Fatalf("the record's message 1 = %+v, want it delivered with the bound's reason", d)
	}
}

// TestFinalizeReportCarriesMessages: the report the run posts on its pull
// request carries the Messages section, with the text of each message from the
// runner's memory and, for one that never reached a stage, the line that sends
// it in a new run — the text and the pull request's number both out of that
// memory and never out of the record (F19).
func TestFinalizeReportCarriesMessages(t *testing.T) {
	const (
		early = "keep the console out of it"
		late  = "and update the docs while you are there"
	)
	h := newHarness(t, "", nil)
	h.messagesOn()
	// No poll: the first message is read at implement's boundary and the
	// second only by the final read, which is what leaves it undelivered.
	h.deps.CancelPoll = time.Hour
	h.tell(t, runID, early)
	rec, err := h.run(t,
		implement("feature"),
		h.during(runID, late, review("ship", 0)),
	)
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	posted := onlyPR(t, h.provider).Comments
	if len(posted) == 0 {
		t.Fatal("the run posted no report")
	}
	report := posted[len(posted)-1]
	for _, want := range []string{
		"#### Messages",
		"| 1 | `dimi` | — | `delivered` | `implement` |",
		"| 2 | `dimi` | — | `undelivered` | — |",
		early, late,
		"Send it in a new run:",
		"fugaro run --pr 1 --repo acme/app -- '" + late + "'",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the posted report lacks %q:\n%s", want, report)
		}
	}
}
