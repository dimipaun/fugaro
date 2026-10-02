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

func TestHaltLeftoversCommitMessage(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, "", nil)
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
	if ok, _ := b.Exists(context.Background(), lock.Key("acme-app", "fugaro/"+runID)); ok {
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
		name   string
		reason runstore.HaltReason
		key    string
		source string
		want   []string
	}{
		{"token ceiling", runstore.HaltTokenCap, "max_run_tokens", "ceiling", []string{"project config", "fugaro init --repo", "ignored"}},
		{"token default branch", runstore.HaltTokenCap, "max_run_tokens", "default-branch", []string{"default branch", "merge", "ignored"}},
		{"token branch", runstore.HaltTokenCap, "max_run_tokens", "branch", []string{"`agent.max_run_tokens`", "this run's branch"}},
		{"cap ceiling", runstore.HaltRunCap, "per_run_usd", "ceiling", []string{"project config", "fugaro init --repo", "ignored"}},
		{"cap default branch", runstore.HaltRunCap, "per_run_usd", "default-branch", []string{"default branch", "merge", "ignored"}},
		{"nocap default branch", runstore.HaltNoCap, "per_run_usd", "default-branch", []string{"default branch", "merge"}},
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
		if tc.source != "ceiling" && strings.Contains(got, "fugaro init --repo") {
			t.Errorf("%s: points at init --repo:\n%s", tc.name, got)
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
