package runner_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// A run that has spent the share of its budget it may spend on review stops
// buying rounds and finalizes with what the last round left open, rather than
// failing the stage it would have run next with "time budget exhausted"
// (BF1).
//
// The fixture's workflow is the clock these tests move: timeouts.total is 5m
// and review_budget_pct is unset, so the share is 80% of it — 4m — and the
// finalize reserve (30s) is not what stops anything here. Each step below
// advances the run's clock by the time named beside it, and the one review
// round lands past the share, so the loop asks the budget between the round it
// just read and the fix round it would otherwise buy. The steps scripted are
// the steps allowed: the harness fails the run on an agent call a test did not
// script, which is what makes "no fix round ran" an assertion and not a hope.

// timestopCfg is the fixture config with review_rounds raised: with the
// fixture's own 2, the round limit ends the loop at round 2 and the stop is
// never the reason the run ended. review_budget_pct is written when pct is
// non-zero and left out otherwise, so a case can take the default.
func timestopCfg(t *testing.T, rounds, pct int) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	cfg = strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: "+strconv.Itoa(rounds)+"\n", 1)
	if pct != 0 {
		cfg = strings.Replace(cfg,
			"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
			"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s, review_budget_pct: "+strconv.Itoa(pct)+" }", 1)
	}
	return cfg
}

// tickFor runs s and then moves the run's clock on by d, as afterStep does for
// the checkpoint tests: the runner reads the budget between two stages, so the
// time a stage took is only visible once it has returned.
func tickFor(c *testClock, d time.Duration, s step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := s(t, ctx, req)
		c.advance(d)
		return res, err
	}
}

// reviewBudgets is the clock a run spends: a test clock in the harness, and
// the share of the run's budget crossed between two rounds.
type reviewBudgets struct {
	h     *harness
	clock *testClock
}

func newReviewBudgets(t *testing.T, cfg string) *reviewBudgets {
	t.Helper()
	h := newHarness(t, cfg, nil)
	clock := newClock()
	h.deps.Now = clock.Now
	return &reviewBudgets{h: h, clock: clock}
}

// stoppedForTime runs implement, then one senior review that asks for changes
// and lands at 4m20s of the fixture's 5m — past the run's 4m review share, so
// the loop stops there instead of starting the fix round. findings are the
// review's own.
func stoppedForTime(t *testing.T, findings ...map[string]any) (*harness, *runstore.Record) {
	t.Helper()
	b := newReviewBudgets(t, "")
	rec, err := b.h.run(t,
		tickFor(b.clock, 30*time.Second, implement("feature")),
		tickFor(b.clock, 3*time.Minute+50*time.Second, reviewScored("changes", findings...)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return b.h, rec
}

// lowRisk is a finding no readiness rule blocks: risk 2×2, so its label is
// low, and the path beside it is not one of the repository's own.
func lowRisk(file string) map[string]any {
	return map[string]any{"summary": "the retry backoff is a fixed second", "file": file,
		"likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness"}
}

// TestReviewBudgetStopsBeforeTheFix is BF1's whole point: the run read its
// round and, finding its share spent, buys neither the fix that round asks for
// nor the review that fix would be followed by. It ends completed with the
// finding still open — not failed with "time budget exhausted during fix" —
// and the pull request is ready, because nothing the round left is what nobody
// merges.
func TestReviewBudgetStopsBeforeTheFix(t *testing.T) {
	h, rec := stoppedForTime(t, lowRisk("internal/runner/prflow.go"))
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %q outcome %q reason %q; want a completed run whose pull request is ready",
			rec.Status, rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, "review budget ran out") || !strings.Contains(rec.Reason, "1 finding") {
		t.Errorf("reason = %q; want the round's own count and the stop named", rec.Reason)
	}
	if len(h.agent.calls) != 2 {
		t.Fatalf("the run made %d agent calls; want 2 — implement and the one review round, and no fix past the share", len(h.agent.calls))
	}
	if pr := onlyPR(t, h.provider); pr.Draft {
		t.Errorf("the pull request is a draft: %+v", pr)
	}
}

// TestReviewBudgetOpenFindingInTheRecord is BF1's machine-readable half: the
// record carries the findings the run finalized with, one entry each, with the
// word its score reads and the reviewer's own path and sentence, so a
// coordinator can triage them without reading the pull request's prose.
func TestReviewBudgetOpenFindingInTheRecord(t *testing.T) {
	_, rec := stoppedForTime(t, lowRisk("internal/runner/prflow.go"))
	of := rec.OpenFindings
	if of == nil {
		t.Fatal("the record carries no open_findings block")
	}
	if of.Round != 1 || of.ReviewPct != 80 || of.Total != 1 || len(of.Findings) != 1 {
		t.Fatalf("open findings = %+v; want round 1, the 80%% default and the one finding", of)
	}
	f := of.Findings[0]
	if f.Title != "the retry backoff is a fixed second" || f.File != "internal/runner/prflow.go" {
		t.Errorf("finding = %+v; want the reviewer's own sentence and path", f)
	}
	if f.Label != "low" || f.Sensitive {
		t.Errorf("finding = %+v; want the label its score reads and no security path", f)
	}
	if f.Likelihood != 2 || f.Impact != 2 || f.FixCost != 1 || f.Class != "correctness" {
		t.Errorf("finding = %+v; want the score the reviewer wrote, so a reader can read a threshold against it", f)
	}
}

// TestReviewBudgetStopsOnABlocker: a blocker is not something a follow-up
// triages later. The run still ends completed — it pushed its work and opened
// a pull request — but the pull request is a draft, with no failure class
// beside it, and the reason names the finding rather than a stage that ran out
// of time.
func TestReviewBudgetStopsOnABlocker(t *testing.T) {
	blocker := map[string]any{"summary": "the token is written to the log", "file": "internal/runner/prflow.go",
		"likelihood": 5, "impact": 5, "fix_cost": 3, "class": "security"}
	h, rec := stoppedForTime(t, blocker)
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %q outcome %q reason %q; want a completed run whose pull request is a draft",
			rec.Status, rec.Outcome, rec.Reason)
	}
	if rec.FailureClass != "" {
		t.Errorf("failure_class = %q; want none: the run completed and the pull request is a draft", rec.FailureClass)
	}
	if !strings.Contains(rec.Reason, "blocker finding in internal/runner/prflow.go") {
		t.Errorf("reason = %q; want the finding named rather than the stage", rec.Reason)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Errorf("the pull request is not a draft: %+v", pr)
	}
	// The section lists what is open, with the word its score reads and the
	// path the reviewer named, so a reader of the pull request sees the finding
	// the draft is for.
	for _, want := range []string{"**Still open**", "blocker", "`internal/runner/prflow.go`", "the token is written to the log"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the status section does not carry %q:\n%s", want, pr.Body)
		}
	}
}

// TestReviewBudgetHighFindingInASecurityPath: a high finding keeps the pull
// request a draft only where it is one the repository's own security floor
// matches. The same finding elsewhere in the tree is a follow-up — the review
// budget running out is no reason to merge a high finding in a path the run's
// own guards live in, and no reason to hold one back somewhere else.
func TestReviewBudgetHighFindingInASecurityPath(t *testing.T) {
	// "high" is risk 4×4 and not the catastrophic band: an impact of 5, or a
	// security or data-loss class, reads as a blocker whatever this case is
	// about.
	high := func(file string) map[string]any {
		return map[string]any{"summary": "the check can be bypassed", "file": file,
			"likelihood": 4, "impact": 4, "fix_cost": 3, "class": "correctness"}
	}
	for _, tc := range []struct {
		name, file, floor string
		ready             bool
	}{
		{"in a security path", "internal/creds/token.go", "matches the floor's internal/creds/**", false},
		{"elsewhere in the tree", "internal/runner/prflow.go", "is no path the floor names", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rec := stoppedForTime(t, high(tc.file))
			if rec.OpenFindings == nil || len(rec.OpenFindings.Findings) != 1 {
				t.Fatalf("open findings = %+v", rec.OpenFindings)
			}
			f := rec.OpenFindings.Findings[0]
			if f.Label != "high" {
				t.Fatalf("label = %q, want high: the case is about the high band", f.Label)
			}
			if f.Sensitive != !tc.ready {
				t.Errorf("sensitive = %v; want %v: %s %s", f.Sensitive, !tc.ready, tc.file, tc.floor)
			}
			if ready := rec.Outcome == runstore.OutcomeReady; ready != tc.ready {
				t.Errorf("outcome = %q with reason %q; want ready = %v", rec.Outcome, rec.Reason, tc.ready)
			}
		})
	}
}

// TestReviewBudgetAnUnscoredFindingBlocks: a reviewer that stopped scoring must
// not turn the stop into a merge. The word is derived from the axes and is fail
// closed, so a finding whose score cannot be read is a blocker here exactly as
// it is at every other gate.
func TestReviewBudgetAnUnscoredFindingBlocks(t *testing.T) {
	_, rec := stoppedForTime(t, map[string]any{"summary": "something is off here"})
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %q reason = %q; want a draft: an unscored finding blocks", rec.Outcome, rec.Reason)
	}
	if of := rec.OpenFindings; of == nil || len(of.Findings) != 1 || of.Findings[0].Label != "blocker" {
		t.Fatalf("open findings = %+v; want the one finding labelled a blocker", of)
	}
}

// TestReviewBudgetStopsTheSecondRound: a run whose first fix was bought — the
// round landed inside the share — still stops at the second review, so what a
// coordinator reads is the last round's findings and not the first's. The fix
// commits and verifies, which is also what leaves the run's work verified.
func TestReviewBudgetStopsTheSecondRound(t *testing.T) {
	b := newReviewBudgets(t, timestopCfg(t, 5, 0))
	rec, err := b.h.run(t,
		tickFor(b.clock, 30*time.Second, implement("feature")),
		tickFor(b.clock, time.Minute, reviewScored("changes", lowRisk("a.go"))),
		tickFor(b.clock, time.Minute, fixVerified),
		tickFor(b.clock, 2*time.Minute, reviewScored("changes", lowRisk("b.go"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.h.agent.calls) != 4 {
		t.Fatalf("the run made %d agent calls; want 4 — the first fix was bought inside the share and the round after the second was not", len(b.h.agent.calls))
	}
	of := rec.OpenFindings
	if of == nil || of.Round != 2 || of.Total != 1 || len(of.Findings) != 1 || of.Findings[0].File != "b.go" {
		t.Fatalf("open findings = %+v; want the second round's own finding", of)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %q outcome %q reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
}

// TestReviewBudgetShareIsConfigurable: the share is the repository's to set. A
// workflow that raises it to 90% buys the fix round the default 80% would not
// have bought: the round landed at 4m20s, which is past the fixture's own 4m
// share and inside this workflow's 4m30s one, and the run then stops at the
// round that fix bought.
//
// The fix is short and the round it bought is long, because the run's next
// stage has to be able to start for the stop to be the loop's and not the
// stage machinery's: past the run's finalize reserve (4m30s of the fixture's
// 5m) no stage starts at all and the run fails with "time budget exhausted
// before stage review" instead, which is the stage-cut case BF1 leaves alone.
func TestReviewBudgetShareIsConfigurable(t *testing.T) {
	b := newReviewBudgets(t, timestopCfg(t, 5, 90))
	rec, err := b.h.run(t,
		tickFor(b.clock, 30*time.Second, implement("feature")),
		tickFor(b.clock, 3*time.Minute+50*time.Second, reviewScored("changes", lowRisk("a.go"))),
		tickFor(b.clock, 2*time.Second, fixVerified),
		tickFor(b.clock, time.Minute, reviewScored("changes", lowRisk("b.go"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.h.agent.calls) != 4 {
		t.Fatalf("the run made %d agent calls; want 4: at 90%% the fix the default would have refused is bought", len(b.h.agent.calls))
	}
	if of := rec.OpenFindings; of == nil || of.Round != 2 || of.ReviewPct != 90 {
		t.Fatalf("open findings = %+v; want the second round and the share the workflow set", of)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = status %q outcome %q reason %q", rec.Status, rec.Outcome, rec.Reason)
	}
}

// TestReviewBudgetBlockIsStoredAsWritten reads the block back out of the stored
// result.json as JSON, because the member's name and shape are the contract a
// reader outside this binary holds. It is additive, so an older reader sees the
// member and skips it and the record's version stays 1.
func TestReviewBudgetBlockIsStoredAsWritten(t *testing.T) {
	blocker := map[string]any{"summary": "the token is written to the log", "file": "internal/creds/token.go",
		"likelihood": 5, "impact": 5, "fix_cost": 3, "class": "security"}
	h, _ := stoppedForTime(t, blocker)
	data, err := h.store.ReadFile(context.Background(), "result.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version      int `json:"version"`
		OpenFindings *struct {
			Round     int `json:"round"`
			ReviewPct int `json:"review_pct"`
			Total     int `json:"total"`
			Findings  []struct {
				Title     string `json:"title"`
				Label     string `json:"label"`
				File      string `json:"file"`
				Sensitive bool   `json:"sensitive"`
				Class     string `json:"class"`
			} `json:"findings"`
		} `json:"open_findings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenFindings == nil {
		t.Fatalf("result.json carries no open_findings member:\n%s", data)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d; want 1: the member is additive", doc.Version)
	}
	got := doc.OpenFindings
	if got.Round != 1 || got.ReviewPct != 80 || got.Total != 1 || len(got.Findings) != 1 {
		t.Fatalf("open_findings = %+v", got)
	}
	f := got.Findings[0]
	if f.Title != "the token is written to the log" || f.Label != "blocker" || f.File != "internal/creds/token.go" ||
		!f.Sensitive || f.Class != "security" {
		t.Errorf("open_findings.findings[0] = %+v; want the reviewer's finding, its label and its security path", f)
	}
}

// TestStageCutMidRoundStillFails: BF1 moves where the loop stops, not what a
// stage that already started does. This run's share is not what ends it — the
// fix round had started inside the share — and the stage the finalize reserve
// cut mid-round is still the failure it always was, with the sentence the owner
// complained about, no open-findings block (the loop never stopped; a stage
// did) and no completed record. The timeouts are seconds rather than the
// fixture's minutes because this deadline is the real one: the stage's own
// context is what expires, not the clock the other tests move. The seconds are
// wide enough for the stages before the cut to run on a loaded machine, and the
// three the reserve leaves are the wait this test pays.
func TestStageCutMidRoundStillFails(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 8s, stage: 8s, verify: 4s, finalize_reserve: 3s }", 1)
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, implement("feature"), reviewScored("changes", lowRisk("a.go")), blockUntilDone)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = status %q outcome %q reason %q; want the stage cut mid-round to fail the run",
			rec.Status, rec.Outcome, rec.Reason)
	}
	if rec.Reason != "time budget exhausted during fix" {
		t.Errorf("reason = %q; want the stage that was cut named, as it is for every stage a deadline ends", rec.Reason)
	}
	if rec.OpenFindings != nil {
		t.Errorf("open findings = %+v; want none: the loop stopped for no share, a stage ran out of time", rec.OpenFindings)
	}
	if pr := onlyPR(t, h.provider); !pr.Draft {
		t.Errorf("the pull request is not a draft: %+v", pr)
	}
}

// TestNoOpenFindingsWhenTheRoundsConverge: a run whose review shipped carries
// no block at all — a different fact from a block that lists none — which is
// what leaves every record written before the member existed reading exactly as
// it did.
func TestNoOpenFindingsWhenTheRoundsConverge(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.OpenFindings != nil {
		t.Fatalf("open findings = %+v; want none: the review shipped", rec.OpenFindings)
	}
	if body := onlyPR(t, h.provider).Body; strings.Contains(body, "Still open") {
		t.Errorf("the status section lists findings for a run whose review shipped:\n%s", body)
	}
}
