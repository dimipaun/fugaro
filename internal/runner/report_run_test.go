package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
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
