package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestReportCarriesWhatTheRunTolerated: the report a real run stores carries
// the mode it resolved and the findings that mode let through (T4.2) — the
// wiring and not only the renderer: the tolerated block has to be on the
// record by the time finalize builds the report, and a unit test over a
// hand-built record cannot see that.
func TestReportCarriesWhatTheRunTolerated(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n", "rigor:\n  default: startup\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	// Startup's floor is above a 2x2 correctness finding, so the round this
	// run leaves open is one the mode tolerates.
	tolerated := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop | in the fetch", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness",
	})
	rec, err := h.run(t, implement("feature"), tolerated, fix, tolerated)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tolerated == nil || len(rec.Tolerated.Findings) != 1 {
		t.Fatalf("the record's tolerated findings = %+v; want the round startup let through", rec.Tolerated)
	}
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"**Rigor:** startup, from repo, at bootstrap, read from origin/main",
		"#### Tolerated",
		"| Label | Likelihood | Impact | Fix cost | Class | Mode | Finding |",
		"| low | 2 | 2 | 1 | correctness | startup | `unbounded retry loop \\| in the fetch` |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the run's report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tolerated in all") {
		t.Errorf("the run's report calls its complete list truncated:\n%s", got)
	}
}

// TestReportCarriesNoToleratedForARoundTheModeBlocked: the round a run ends on
// can hold a finding the mode tolerated beside one it blocked on, and the
// record keeps the first either way (D9). The report must not, because the
// round as a whole is not one the mode let through: the pull request's own
// body names no tolerated finding for it, and the table would claim a decision
// Decide did not make.
func TestReportCarriesNoToleratedForARoundTheModeBlocked(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n", "rigor:\n  default: startup\nagent:\n", 1)
	h := newHarness(t, cfg, nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	// Below startup's floor and above it: the round is one the mode half let
	// through, which is the round Decide blocks whatever it tolerated.
	mixed := reviewScored("changes",
		map[string]any{"summary": "unbounded retry loop", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness"},
		map[string]any{"summary": "no bound on the cache", "likelihood": 4, "impact": 4, "fix_cost": 2, "class": "correctness"},
	)
	rec, err := h.run(t, implement("feature"), mixed, fix, mixed)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review round 2 still has 2 findings" {
		t.Fatalf("record = %+v; want the round the mode blocked on its second finding", rec)
	}
	if rec.Tolerated == nil || rec.Tolerated.Total != 1 || len(rec.Tolerated.Findings) != 1 {
		t.Fatalf("the record's tolerated findings = %+v; want the finding the split let through (D9)", rec.Tolerated)
	}
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, claim := range []string{"#### Tolerated", "| Label |", "unbounded retry loop"} {
		if strings.Contains(got, claim) {
			t.Errorf("the run's report claims %q for a round the mode blocked:\n%s", claim, got)
		}
	}
	if !strings.Contains(got, "round 2: changes (2 findings)") {
		t.Errorf("the run's report lost the round's own count:\n%s", got)
	}
}
