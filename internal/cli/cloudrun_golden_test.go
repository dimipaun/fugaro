package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// updateCloudRunGolden rewrites testdata/cloudrun_cost.golden. It is a flag
// of its own: the package's other goldens already own -update, -update-review
// and -update-retire, and a second flag.Bool of the same name panics.
var updateCloudRunGolden = flag.Bool("update-cloudrun", false, "rewrite testdata/cloudrun_cost.golden")

// The byte-for-byte guarantee for Cloud Run's cost output is a standing test,
// not a one-off reading: TestCloudRunCostGolden renders every case below
// through the functions the CLI itself renders with -- runview.Join for the
// row (ls --json prints exactly this document), runner.CostLine and
// diagnose's own cost block, the history record fugaro report rolls a run
// into, and json.Marshal of the cost object result.json carries -- and
// compares the lot with a committed golden.
//
// The cases are the ones that could move without a line of output changing
// visibly: both Cloud Run pricing tiers, an execution whose name the backend
// could not parse (no region), limits the execution did not report, a run
// under a minute, inverted times, subscription and model-only stored costs, a
// stored estimate's carried-over source line, a price book that prices
// nothing, and an override of zero rates. Every one of them is a Cloud Run
// execution: MachineType is empty, so nothing here is a VM estimate.
func TestCloudRunCostGolden(t *testing.T) {
	newCloudFixture(t) // the project config openCloud reads
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	// The override is for us-west1 alone, which only the zero-rate case uses:
	// localcfg refuses a zero price (ValidPrice), so this one is set on the
	// loaded config directly -- it is the shape a caller can still hand the
	// price book, and the row must not read $0.00 as if it were free.
	env.lc.ComputePrices = map[string]localcfg.Price{"us-west1": {}}
	book := env.prices(nil)

	costRec := func(c runstore.Cost) *runstore.Record {
		r := &runstore.Record{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Workflow: "web",
			Status: runstore.StatusSucceeded, CostUSD: c.ModelUSD}
		r.Cost = &c
		return r
	}
	started := now.Add(-30 * time.Minute)
	exec := func(name, region string, cpu, mem float64, d time.Duration) *backend.Execution {
		e := &backend.Execution{Name: name, Job: "j", State: backend.StateSucceeded, Region: region,
			CPU: cpu, MemoryGiB: mem, Started: started, Completed: started.Add(d)}
		return e
	}

	cases := []struct {
		name string
		exec *backend.Execution
		rec  *runstore.Record
	}{
		{"tier-1 region, 4 vCPU / 8 GiB, 30 min",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 4, 8, 30*time.Minute), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"tier-2 region, same limits",
			exec("projects/p/locations/europe-west2/jobs/j/executions/j-1", "europe-west2", 4, 8, 30*time.Minute), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"no region: the name did not parse",
			exec("exec-1", "", 4, 8, 30*time.Minute), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"limits the execution did not report",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 0, 0, 30*time.Minute), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"a run under a minute: Cloud Run has no floor",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 4, 8, 30*time.Second), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"inverted times",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 4, 8, -time.Minute), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"subscription model spend",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 4, 8, 30*time.Minute), costRec(runstore.NewCost(3.2, 0, runstore.BasisSubscription))},
		{"a stored model-only cost",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 4, 8, 30*time.Minute), costRec(runstore.ModelOnlyCost(3.2, runstore.BasisAPIList))},
		{"a stored compute estimate with its source line",
			exec("projects/p/locations/us-east5/jobs/j/executions/j-1", "us-east5", 4, 8, 30*time.Minute),
			costRec(runstore.Cost{ModelUSD: 2, ComputeUSD: 1.5, TotalUSD: 3.5, ComputeEstimated: true, ModelBasis: runstore.BasisAPIList,
				ComputeSource: "Compute Engine VM list price, e2-standard family, us-east1 (2026-10-14, UNVERIFIED)"})},
		{"an override of zero rates",
			exec("projects/p/locations/us-west1/jobs/j/executions/j-1", "us-west1", 4, 8, 30*time.Minute), costRec(runstore.NewCost(2, 0, runstore.BasisAPIList))},
		{"a stored compute estimate and no execution to re-price it",
			nil,
			costRec(runstore.Cost{ModelUSD: 2, ComputeUSD: 1.5, TotalUSD: 3.5, ComputeEstimated: true, ModelBasis: runstore.BasisAPIList,
				ComputeSource: "Compute Engine VM list price, e2-standard family, us-east1 (2026-10-14, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"})},
	}

	var b strings.Builder
	for _, c := range cases {
		row := runview.Join(runview.Input{Slug: appSlug, RunID: "20260927-100000-abcd", Record: c.rec, Exec: c.exec}, book, now)
		fmt.Fprintf(&b, "== %s\n", c.name)
		doc, err := json.MarshalIndent(lsDoc{Project: "proj-1234", Runs: []runview.Row{row}, Totals: runview.Sum([]runview.Row{row}), Warnings: nil}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "--- ls --json\n%s\n", doc)
		// result.json carries the record's cost object, which is what the
		// runner writes and schemas/result.schema.json pins.
		rec, err := json.Marshal(c.rec)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "--- result.json\n%s\n", rec)
		fmt.Fprintf(&b, "--- diagnose\n%s\n", strings.Join(costLines(row.Cost), "\n"))
		day := recordOfRun(row)
		fmt.Fprintf(&b, "--- history\ncompute_micros=%d compute_estimated_runs=%d spent_micros=%d notional_micros=%d runs=%d\n",
			int64(day.ComputeMicros), day.ComputeEstimatedRuns, int64(day.SpentMicros), int64(day.NotionalMicros), day.Runs)
		fmt.Fprintf(&b, "--- report source\n%s\n", strings.Join(computeSources([]runview.Row{row}), " | "))
	}

	path := filepath.Join("testdata", "cloudrun_cost.golden")
	if *updateCloudRunGolden {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("no golden file: run go test ./internal/cli -run TestCloudRunCostGolden -update-cloudrun")
		}
		t.Fatal(err)
	}
	if string(want) != b.String() {
		t.Fatalf("Cloud Run's cost output changed (go test -update-cloudrun to accept):\n%s\nwant:\n%s", b.String(), want)
	}
}

// The two mutations the review found surviving are killed here rather than by
// the golden alone, because a golden says "this changed", not "this is
// wrong": (M8) a Cloud Run row must not gain a source line even when the
// price book it was priced with carries one -- Cloud Run's rows are unchanged
// and a line would be a VM estimate's -- and (M9) a price book that prices
// nothing must read "compute not estimated", never "$0.00 estimated", for an
// execution whose window IS known (the case the missing-boot-window check
// used to swallow).
func TestCloudRunRowsNeverCarryASourceLine(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	// A price book whose source is not empty, which is what a VM table's is:
	// Cloud Run's own list price names itself too, so the row must ignore it.
	sourced := backend.Prices{VCPUSecondUSD: 1e-5, GiBSecondUSD: 2e-6, Source: "Cloud Run jobs list price, tier 1 (2026-09)"}
	e := &backend.Execution{Name: "exec-1", State: backend.StateSucceeded, Region: "us-east5", CPU: 4, MemoryGiB: 8,
		Started: now.Add(-10 * time.Minute), Completed: now.Add(-5 * time.Minute)}
	rec := &runstore.Record{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Workflow: "web", Status: runstore.StatusSucceeded}
	row := runview.Join(runview.Input{Slug: appSlug, RunID: "20260927-100000-abcd", Record: rec, Exec: e},
		func(*backend.Execution) backend.Prices { return sourced }, now)
	if !row.Cost.ComputeEstimated || row.Cost.ComputeUSD == 0 {
		t.Fatalf("compute = %v estimated=%v, want an estimate", row.Cost.ComputeUSD, row.Cost.ComputeEstimated)
	}
	if row.Cost.ComputeSource != "" {
		t.Errorf("a Cloud Run row's compute source = %q, want none: only a VM estimate names its table", row.Cost.ComputeSource)
	}
	if strings.Contains(costLines(row.Cost)[0], "Compute source") || len(costLines(row.Cost)) != 1 {
		t.Errorf("diagnose's cost block = %q, want the one line it always printed", costLines(row.Cost))
	}
}

// A price book that prices nothing leaves compute unestimated even when the
// window IS known: the estimate must never read $0.00, which a reader cannot
// tell from free (design compute-backend.md §5.2). The zero-rate book is the
// case that reaches the check -- it names itself, so a test looking only for
// an empty book would let it through to a "$0.00 estimated" line.
func TestAPriceBookThatPricesNothingIsNotZeroed(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	e := &backend.Execution{Name: "exec-1", State: backend.StateSucceeded, Region: "us-east5", CPU: 4, MemoryGiB: 8,
		Started: now.Add(-10 * time.Minute), Completed: now.Add(-5 * time.Minute)}
	rec := &runstore.Record{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Workflow: "web", Status: runstore.StatusSucceeded}
	books := map[string]backend.Prices{
		"the zero book":           {},
		"a sourced zero-rate one": {VCPUSecondUSD: 0, GiBSecondUSD: 0, Source: "a table that prices nothing"},
	}
	for name, book := range books {
		row := runview.Join(runview.Input{Slug: appSlug, RunID: "20260927-100000-abcd", Record: rec, Exec: e},
			func(*backend.Execution) backend.Prices { return book }, now)
		if row.Cost.ComputeEstimated || row.Cost.ComputeUSD != 0 {
			t.Errorf("%s: compute = %v estimated=%v, want not estimated", name, row.Cost.ComputeUSD, row.Cost.ComputeEstimated)
		}
		if line := costLines(row.Cost)[0]; !strings.Contains(line, "compute not estimated") {
			t.Errorf("%s: diagnose's cost line = %q, want it to say compute is not estimated", name, line)
		}
		if row.Cost.ComputeSource != "" {
			t.Errorf("%s: compute source = %q, want none for a figure that was not estimated", name, row.Cost.ComputeSource)
		}
	}
}
