package runner_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// firstLineCfg is the fixture config with a coder and a reviewer model and
// extra agent lines (the first line is on unless extra says otherwise).
func firstLineCfg(t *testing.T, rounds int, extra string) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	out := strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: "+string(rune('0'+rounds))+"\n  models: { coder: coder-model, reviewer: senior-model }\n"+extra, 1)
	if out == cfg {
		t.Fatal("fixture has no review_rounds line")
	}
	return out
}

func fixStep(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
	verifyTest(t, ctx, req)
	return agent.Result{}, nil
}

func stageNames(rec *runstore.Record) []string {
	var out []string
	for _, s := range rec.Stages {
		out = append(out, s.Name)
	}
	return out
}

func tiers(rec *runstore.Record) []string {
	var out []string
	for _, r := range rec.Reviews {
		out = append(out, r.Tier+":"+r.Verdict)
	}
	return out
}

func eq(a []string, b ...string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// TestFirstLineStageOrder: implement, review_first, fix, then the senior
// review on the reviewer's model, with first-line findings kept from it.
func TestFirstLineStageOrder(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, "  first_line_review: on\n"), nil)
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review") {
		t.Fatalf("stages = %v", got)
	}
	if got := tiers(rec); !eq(got, "first:changes", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	c := h.agent.calls
	if c[0].Model != "coder-model" || c[1].Model != "coder-model" || c[2].Model != "coder-model" || c[3].Model != "senior-model" {
		t.Fatalf("models = %q %q %q %q", c[0].Model, c[1].Model, c[2].Model, c[3].Model)
	}
	if c[1].JSONSchema == "" || c[1].Prompt != c[3].Prompt || c[1].SessionID == c[0].SessionID || c[3].SessionID == c[1].SessionID {
		t.Fatalf("first-line request = %+v, senior = %+v", c[1], c[3])
	}
	if !c[2].Resume || c[2].SessionID != c[0].SessionID || !strings.Contains(c[2].Prompt, "- fix it") {
		t.Fatalf("fix request = %+v", c[2])
	}
	// The senior reviewer is not told what the first line found.
	if c[3].Resume || strings.Contains(c[3].Prompt, "fix it") || strings.Contains(c[3].AppendSystemPrompt, "fix it") {
		t.Fatalf("senior request = %+v", c[3])
	}
	for _, name := range []string{"review_first-1", "review-1", "fix-1"} {
		if _, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"transcripts/"+name+".jsonl"); err != nil {
			t.Errorf("transcript %s: %v", name, err)
		}
	}
	if rec.CostUSD != 1+0.5+0+0.5 {
		t.Errorf("cost = %v: every tier's stage counts", rec.CostUSD)
	}
}

// TestFirstLineVerdictNeverMakesReady: a first-line ship does not make the
// run ready when the senior review wants changes.
func TestFirstLineVerdictNeverMakesReady(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 1, "  first_line_review: on\n"), nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0), review("changes", 2))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review round 1 still has 2 findings" {
		t.Fatalf("rec = %+v", rec)
	}
	if got := tiers(rec); !eq(got, "first:ship", "senior:changes") {
		t.Fatalf("reviews = %v", got)
	}
	// The senior review ran although the first line shipped.
	if len(h.agent.calls) != 3 || h.agent.calls[2].Model != "senior-model" {
		t.Fatalf("calls = %d", len(h.agent.calls))
	}
}

// TestFirstLineChangesGoThroughFix: first-line changes, a fix, a senior ship.
func TestFirstLineChangesGoThroughFix(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, "  first_line_review: on\n"), nil)
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// The ready verdict is the senior's: the rule reads the last senior review.
	if got := tiers(rec); !eq(got, "first:changes", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
}

// TestSeniorReviewChangesStillGoThroughFix: after the first line the senior
// loop runs as it does today.
func TestSeniorReviewChangesStillGoThroughFix(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, "  first_line_review: on\n"), nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0), review("changes", 1), fixStep, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review") {
		t.Fatalf("stages = %v", got)
	}
	if got := tiers(rec); !eq(got, "first:ship", "senior:changes", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	if rec.Reviews[1].Round != 1 || rec.Reviews[2].Round != 2 {
		t.Fatalf("senior rounds = %+v", rec.Reviews)
	}
}

// TestFirstLineFailureSkipped: a first-line stage that errors, reports an
// error or has no verdict is recorded and skipped; the senior review runs and
// decides, and the run does not fail.
func TestFirstLineFailureSkipped(t *testing.T) {
	broken := map[string]step{
		"agent error": func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			return agent.Result{}, errors.New("upstream unavailable")
		},
		"is_error result": func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			return agent.Result{IsError: true, Subtype: "error_during_execution"}, nil
		},
		"no verdict": func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			return agent.Result{Text: "looks fine to me"}, nil
		},
	}
	for name, first := range broken {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, firstLineCfg(t, 1, "  first_line_review: on\n  first_line_rounds: 2\n"), nil)
			rec, err := h.run(t, implement("feature"), first, review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if got := tiers(rec); !eq(got, "first:none", "senior:ship") {
				t.Fatalf("reviews = %v", got)
			}
			if rec.Reviews[0].Findings != 0 {
				t.Errorf("a skipped first line has no findings to record: %+v", rec.Reviews[0])
			}
			// No fix, and no second first-line round after a failure.
			if len(h.agent.calls) != 3 || h.agent.calls[2].Model != "senior-model" {
				t.Fatalf("calls = %d", len(h.agent.calls))
			}
		})
	}
}

// TestFirstLineRoundsBounded: first_line_rounds bounds the first-line
// review/fix cycles, apart from review_rounds, and ship stops them early.
func TestFirstLineRoundsBounded(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 1, "  first_line_review: on\n  first_line_rounds: 2\n"), nil)
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("changes", 1), fixStep, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review_first", "fix", "review") {
		t.Fatalf("stages = %v", got)
	}
	if got := tiers(rec); !eq(got, "first:changes", "first:changes", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}

	h = newHarness(t, firstLineCfg(t, 1, "  first_line_review: on\n  first_line_rounds: 3\n"), nil)
	rec, err = h.run(t, implement("feature"), review("ship", 0), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(h.agent.calls) != 3 {
		t.Fatalf("rec = %+v, err = %v, calls = %d", rec, err, len(h.agent.calls))
	}
}

// TestFirstLineOffAndClaudeOnlyUnchanged: off, and auto with a Claude coder,
// run the stages of old and write no tier.
func TestFirstLineOffAndClaudeOnlyUnchanged(t *testing.T) {
	for name, extra := range map[string]string{"off": "  first_line_review: off\n", "auto": ""} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, firstLineCfg(t, 2, extra), nil)
			rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("ship", 0))
			if err != nil || rec.Outcome != runstore.OutcomeReady {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
				t.Fatalf("stages = %v", got)
			}
			for _, r := range rec.Reviews {
				if r.Tier != "" {
					t.Errorf("review %+v has a tier", r)
				}
			}
		})
	}
}

// TestFirstLineUsesCoderPins: the first-line stage runs with the coder's
// model and the one stage budget, the senior with the reviewer's.
func TestFirstLineUsesCoderPins(t *testing.T) {
	g := providerRun(t, providerFirstLine(t, "on"), "observe", "", nil, "acme/app")
	rec, err := g.run(t, implement("feature"), review("ship", 0), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	c := g.agent.calls
	if len(c) != 3 || c[1].Model != deepseek || c[2].Model != sonnet {
		t.Fatalf("calls = %+v", c)
	}
	if c[1].MaxBudgetUSD != c[0].MaxBudgetUSD || c[1].MaxBudgetUSD != c[2].MaxBudgetUSD {
		t.Errorf("stage budgets differ: %v %v %v", c[0].MaxBudgetUSD, c[1].MaxBudgetUSD, c[2].MaxBudgetUSD)
	}
	// The pins are the coder's: its model and its output limit.
	if got := envValue(c[1].Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != deepseek {
		t.Errorf("first-line pin = %q, want the coder's model", got)
	}
	if got := envValue(c[2].Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != sonnet {
		t.Errorf("senior pin = %q, want the reviewer's model", got)
	}
}

// TestFirstLineAutoOnlyForProviderCoder: with no setting, a provider coder
// gets the first line and a Claude coder does not.
func TestFirstLineAutoOnlyForProviderCoder(t *testing.T) {
	g := providerRun(t, providerFirstLine(t, ""), "observe", "", nil, "acme/app")
	rec, err := g.run(t, implement("feature"), review("ship", 0), review("ship", 0))
	if err != nil || !eq(stageNames(rec), "implement", "review_first", "review") {
		t.Fatalf("provider coder: stages = %v, err = %v", stageNames(rec), err)
	}
	g = newGW(t, gwConfig(t, ""), "observe", "")
	rec, err = g.run(t, implement("feature"), review("ship", 0))
	if err != nil || !eq(stageNames(rec), "implement", "review") {
		t.Fatalf("Claude coder: stages = %v, err = %v", stageNames(rec), err)
	}
}

// TestFirstLineCostCountsToCap: both tiers draw on the one token cap, so a
// first-line stage can halt the run before the senior review.
func TestFirstLineCostCountsToCap(t *testing.T) {
	cfg := firstLineCfg(t, 1, "  first_line_review: on\n  max_run_tokens: 100\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, big(implement("feature"), 40), big(review("ship", 0), 70))
	tokenHalt(t, rec, err, "run used 110 tokens of 100")
	if len(h.agent.calls) != 2 {
		t.Fatalf("the senior review ran after the cap: %d calls", len(h.agent.calls))
	}
	if rec.Outcome == runstore.OutcomeReady {
		t.Fatalf("a halted run is ready: %+v", rec)
	}
	// A halted run is not ready, whatever the first line said.
	if got := tiers(rec); !eq(got, "first:ship") {
		t.Fatalf("reviews = %v", got)
	}
}

// withFirstLine turns a config with a draft PR (prCfg or prFollowYAML) into
// one with a coder, a senior reviewer and the first line on.
func withFirstLine(t *testing.T, cfg string) string {
	t.Helper()
	out := strings.Replace(cfg, "review_rounds: 2", "review_rounds: 2\n  models: { coder: coder-model, reviewer: senior-model }\n  first_line_review: on", 1)
	if out == cfg {
		t.Fatal("no review_rounds: 2 in the config")
	}
	return out
}

// TestFirstLineStatusSectionAndNoReviewersUntilReady: with the early draft
// PR, the status section names the first-line review (as such) while the
// senior review runs, the draft has no reviewers or labels, and they come
// only with the flip to ready.
func TestFirstLineStatusSectionAndNoReviewersUntilReady(t *testing.T) {
	clk := newClock()
	h := prHarness(t, withFirstLine(t, prCfg(t, 2, "")))
	h.deps.Now = clk.Now
	var body string
	var draft bool
	var reviewers, labels []string
	rec, err := h.run(t,
		afterStep(clk, implement("feature")),
		afterStep(clk, review("changes", 1)), // first line
		probe(func(t *testing.T) { // the fix that follows the first-line review
			pr := h.provider.State.PRs[0]
			body, draft, reviewers, labels = pr.Body, pr.Draft, pr.Reviewers, pr.Labels
		}, afterStep(clk, fixVerified)),
		review("ship", 0))
	mustReady(t, rec, err)
	if !draft || len(reviewers) != 0 || len(labels) != 0 {
		t.Fatalf("after the first-line review: draft=%v reviewers=%v labels=%v", draft, reviewers, labels)
	}
	if !strings.Contains(body, "first-line review round 1: 1 finding") || strings.Contains(body, "senior review") {
		t.Fatalf("the status section after the first-line review:\n%s", body)
	}
	if got := tiers(rec); !eq(got, "first:changes", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || len(pr.Reviewers) != 1 || !strings.Contains(pr.Body, "**Ready for review**") || !strings.Contains(pr.Body, "senior review: ship") {
		t.Fatalf("final PR = %+v", pr)
	}
}

// TestCancelDuringFirstLineReviewEndsCancelled: a cancel while the first-line
// review runs ends the run cancelled with the draft left as it is, never
// ready, with no senior review.
func TestCancelDuringFirstLineReviewEndsCancelled(t *testing.T) {
	h := prHarness(t, withFirstLine(t, prCfg(t, 2, "")))
	cancelThenBlock := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if req.JSONSchema == "" {
			t.Errorf("the cancelling stage is not a review: %+v", req)
		}
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, implement("feature"), cancelThenBlock)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v", rec)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first") {
		t.Fatalf("stages = %v", got)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || len(pr.Reviewers) != 0 || !strings.Contains(pr.Body, "**Cancelled**") {
		t.Fatalf("PR = %+v", pr)
	}
	if seniorReview := len(h.agent.calls); seniorReview != 2 {
		t.Fatalf("%d agent calls, want implement and the first-line review only", seniorReview)
	}
}

// TestFollowUpRunWithFirstLineReview: a --pr follow-up with the first line on
// runs it before the senior review, records both tiers and flips the draft.
func TestFollowUpRunWithFirstLineReview(t *testing.T) {
	cfg := withFirstLine(t, prFollowYAML(t, ""))
	h := followUpHarness(t, cfg, nil, implement("feature"), review("changes", 1), fixVerified, review("changes", 1), fixVerified, review("changes", 1))
	st := h.state(t)
	if !st.PRs[0].Draft || len(st.PRs[0].Reviewers) != 0 {
		t.Fatalf("first run's PR = %+v", st.PRs[0])
	}
	h.followUp(t, followID, runID, "")
	rec, err := h.run(t, implement("again"), review("ship", 0), review("ship", 0))
	mustReady(t, rec, err)
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review") {
		t.Fatalf("stages = %v", got)
	}
	if got := tiers(rec); !eq(got, "first:ship", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	st = h.state(t)
	if pr := st.PRs[0]; len(st.PRs) != 1 || pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) {
		t.Fatalf("PR = %+v", pr)
	}
}
