package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// updateSpendGolden rewrites testdata/spend_accuracy_result.golden.json.
var updateSpendGolden = flag.Bool("update", false, "rewrite testdata/spend_accuracy_result.golden.json")

// spendGoldenPath is the record the two halves of SA1.4 share: the runner
// writes it and the CLI reads it, so the figures one side records are the
// figures the other prints.
var spendGoldenPath = filepath.Join("testdata", "spend_accuracy_result.golden.json")

// TestSpendFieldsEndToEnd (SA1.4, F7, D2650) drives a stage's calls through
// the fake upstream with a provider cost on some of them and none on the
// others, and reads the run's own result.json back: the report by serving
// model, the table's band over the calls that reported one, and the count of
// the calls that did not are all on the record, and the record is the file
// the CLI's own readers are handed.
//
// The band is over the reported calls alone and carries no route fee, which
// is what makes it comparable with reported_usd: a call the table settled
// itself is the table's own figure, so counting it would make the band agree
// with itself.
func TestSpendFieldsEndToEnd(t *testing.T) {
	use := pricing.Usage{Input: 4000, Output: 2000}
	// Two calls report what they cost, the third reports none: the split, the
	// band and the unreported count are then three different figures of the
	// same stage.
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{
			anthropicfake.Compat{Cost: 0.0021}.MessageOK(deepseek, use),
			anthropicfake.Compat{Cost: 0.0004}.MessageOK(deepseek, use),
			anthropicfake.Compat{}.MessageOK(deepseek, use),
		}, "acme/app")
	prov := g.deps.Providers["openrouter"]
	prov.RouteFeePct = 10
	g.deps.Providers["openrouter"] = prov

	var statuses []int
	rec, err := g.run(t,
		calling(&statuses, implement("feature"), call{deepseek, 1000}, call{deepseek, 1000}, call{deepseek, 1000}),
		review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for i, got := range statuses {
		if got != 200 {
			t.Fatalf("provider call %d got %d", i, got)
		}
	}
	if rec.Cost == nil {
		t.Fatal("the run recorded no cost")
	}

	m, ok := g.deps.Spend.Prices.Lookup(deepseek)
	if !ok {
		t.Fatal("the run's table has no row for the model it routed")
	}
	// The band is the row's own charge at each end over the usage the report
	// is over, summed as whole micros and with no route fee.
	lo, hi := m.Lo.Cost(use), m.Hi.Cost(use)
	if lo == hi {
		t.Fatalf("the fixture needs a row whose two ends differ: %+v", m)
	}
	band := func(end pricing.Micros) float64 { return (2 * end).USD() }
	if want := map[string]float64{deepseek: 0.0021 + 0.0004}; !maps.Equal(rec.Cost.ReportedBy, want) {
		t.Errorf("reported_by = %v, want the two reported calls %v", rec.Cost.ReportedBy, want)
	}
	if !near(rec.Cost.ReportedUSD, 0.0025) {
		t.Errorf("reported_usd = %v, want the reports summed with no fee", rec.Cost.ReportedUSD)
	}
	if !near(rec.Cost.TableLoUSD, band(lo)) || !near(rec.Cost.TableHiUSD, band(hi)) {
		t.Errorf("band = $%v-$%v, want $%v-$%v over the reported calls alone",
			rec.Cost.TableLoUSD, rec.Cost.TableHiUSD, band(lo), band(hi))
	}
	if rec.Cost.UnreportedCalls != 1 {
		t.Errorf("unreported_calls = %d, want the one call that reported no cost", rec.Cost.UnreportedCalls)
	}
	// The run's model figure is the settled ledger's, and the ledger holds
	// every call the run made: three calls here, of which one reported no cost.
	//
	// What "settled" is, is SA2.1's rule and not this task's: SA1.4 pins that
	// the ledger's figure reaches the record, so this line holds whichever rule
	// the gateway settles by. Today's is the table's price for all three calls
	// plus the route fee, which is why the figure equals the charge and differs
	// from reported_usd; under SA2.1 it becomes the report plus the fee on the
	// two calls that carried one, floored by the table on the third, and
	// reported_usd stops being a different number (D2640, F16). SA2.1 is not in
	// this tree, so the assertion below is written to the rule in force and
	// moves with it rather than pinning the old one.
	if !near(rec.Cost.ModelUSD, rec.CostUSD) || rec.Cost.ModelSource != "gateway" {
		t.Errorf("model_usd = %v (source %q), want the gateway's settled %v", rec.Cost.ModelUSD, rec.Cost.ModelSource, rec.CostUSD)
	}
	if !near(rec.Cost.ModelBy[deepseek], rec.Cost.ModelUSD) {
		t.Errorf("model_by[%s] = %v, want the run's whole settled %v", deepseek, rec.Cost.ModelBy[deepseek], rec.Cost.ModelUSD)
	}

	// The record the CLI's readers are handed is the run's own, written to
	// testdata by -update.
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, spendGoldenPath, spendGoldenRecord(t, stored))
}

// spendGoldenRecord is the run's record as the golden holds it: the facts a
// wall clock and a git checkout decide are the run's and not the format's, so
// they are left out and two runs of the same fixture write the same file. The
// figures under test are untouched.
func spendGoldenRecord(t *testing.T, rec *runstore.Record) []byte {
	t.Helper()
	out := *rec
	out.StartedAt, out.FinishedAt, out.Deadline = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil, nil
	out.Branch, out.BaseBranch, out.StartSHA, out.HeadSHA, out.PushedHead = "", "", "", "", ""
	out.Execution, out.Verify, out.Switches = "", nil, nil
	out.Cost = nil
	if out.PR != nil {
		pr := *out.PR
		pr.StatusAt = nil
		out.PR = &pr
	}
	for i := range out.Reviews {
		out.Reviews[i].Head = ""
	}
	if rec.Cost != nil {
		cost := *rec.Cost
		cost.ComputeUSD, cost.ComputeEstimated, cost.ComputeSource = 0, false, ""
		out.Cost = &cost
	}
	for i := range out.Stages {
		out.Stages[i].StartedAt, out.Stages[i].DurationS = time.Time{}, 0
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// compareGolden writes want to path under -update and compares it otherwise.
func compareGolden(t *testing.T, path string, want []byte) {
	t.Helper()
	if *updateSpendGolden {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale (rerun with -update if the change is intended):\n%s", path, firstDiff(got, want))
	}
}

// firstDiff names the first line that differs, so a golden mismatch reads as
// the field that moved rather than as two hundred lines of JSON.
func firstDiff(got, want []byte) string {
	g, w := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
	for i := 0; i < len(g) && i < len(w); i++ {
		if !bytes.Equal(g[i], w[i]) {
			return fmt.Sprintf("line %d:\n  golden: %s\n  record: %s", i+1, g[i], w[i])
		}
	}
	return fmt.Sprintf("the files differ in length: golden %d lines, record %d", len(g), len(w))
}
