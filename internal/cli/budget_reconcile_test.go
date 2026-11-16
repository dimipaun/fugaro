package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// budget_reconcile_test.go drives `fugaro budget reconcile` over hand-written
// run records, which is the whole point of the command: the figures it prints
// are the records' own, so a test states a record and reads back what the
// reading makes of it. The window is passed explicitly and both dates are in
// the past of every run ID below, so nothing here ages with the calendar.

// reconcileWindowArgs is the window the tests read, both ends inclusive.
func reconcileWindowArgs() []string {
	return []string{"--from", "2026-10-12", "--to", "2026-11-16"}
}

// reconcileRecord writes one run of the fixture's repository with the cost
// breakdown the test states.
func reconcileRecord(t *testing.T, f *cloudFixture, id string, cost *runstore.Cost) {
	t.Helper()
	seedRun(t, f, id, "", "someone@example.com", true)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Repo: appSlug, Status: runstore.StatusSucceeded, Cost: cost})
}

// reconcileProvider appends a provider route with a route fee to the fixture's
// config, so a charge recorded through one is read back with the fee the
// totals take out again.
func reconcileProvider(t *testing.T, f *cloudFixture) {
	t.Helper()
	f.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n"+
		"    auth: bearer\n    secret: openrouter-api-key\n    route_fee_pct: 5\n"+
		"    models: [\"deepseek/*\"]\n    allow_data_to: [acme/app]\n")
}

// budgetReconcileJSON is `budget reconcile --json`'s document, as the tests
// read it back.
type budgetReconcileJSON struct {
	Project   string `json:"project"`
	From      string `json:"from"`
	To        string `json:"to"`
	Repo      string `json:"repo"`
	All       bool   `json:"all"`
	MaxRuns   int    `json:"max_runs"`
	Truncated bool   `json:"truncated"`
	Models    []struct {
		Model            string   `json:"model"`
		ChargeUSD        float64  `json:"charge_usd"`
		ReportUSD        float64  `json:"report_usd"`
		BandLoUSD        float64  `json:"band_lo_usd"`
		BandHiUSD        float64  `json:"band_hi_usd"`
		InputTokens      int64    `json:"input_tokens"`
		OutputTokens     int64    `json:"output_tokens"`
		CacheReadTokens  int64    `json:"cache_read_tokens"`
		CacheWriteTokens int64    `json:"cache_write_tokens"`
		Runs             int      `json:"runs"`
		RunsWithReport   int      `json:"runs_with_report"`
		ServiceTiers     []string `json:"service_tiers"`
	} `json:"models"`
	Totals struct {
		Runs                int     `json:"runs"`
		NoRecord            int     `json:"no_record"`
		RecordCorrupt       int     `json:"record_corrupt"`
		NoCost              int     `json:"no_cost"`
		Unlaunched          int     `json:"unlaunched"`
		RunsWithReport      int     `json:"runs_with_report"`
		ChargeUSD           float64 `json:"charge_usd"`
		ChargeWithoutFeeUSD float64 `json:"charge_without_fee_usd"`
		NotionalUSD         float64 `json:"notional_usd"`
		ReportUSD           float64 `json:"report_usd"`
		BandLoUSD           float64 `json:"band_lo_usd"`
		BandHiUSD           float64 `json:"band_hi_usd"`
		UnreportedCalls     int     `json:"unreported_calls"`
	} `json:"totals"`
	Warnings []string `json:"warnings"`
}

// reconcileModel is one row of the document, by name: a reading that names no
// row for a model it read a figure from has lost the figure.
func reconcileModel(t *testing.T, doc budgetReconcileJSON, model string) int {
	t.Helper()
	for i, m := range doc.Models {
		if m.Model == model {
			return i
		}
	}
	t.Fatalf("no row for %q: %+v", model, doc.Models)
	return -1
}

// reconcileWalk runs the reading over one repository and takes its document
// apart.
func reconcileWalk(t *testing.T, args ...string) budgetReconcileJSON {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"budget", "reconcile"}, append(reconcileWindowArgs(), append(args, "--json")...)...)...)
	if err != nil {
		t.Fatalf("budget reconcile --json: %v (%s)", err, errOut)
	}
	var got budgetReconcileJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

// reconcileTableRow is the table row whose first cell is name, whitespace
// split so that a test reads the cells and not the tabwriter's padding.
func reconcileTableRow(t *testing.T, out, name string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == name {
			return f
		}
	}
	t.Fatalf("no table row for %q:\n%s", name, out)
	return nil
}

// TestBudgetReconcileByModel is the reading's whole shape over one window: a
// run charged through a provider route with a fee, its report and its band by
// serving model, the unattributed residual as a row of its own, and the totals
// with the fee and without it.
func TestBudgetReconcileByModel(t *testing.T) {
	f := newCloudFixture(t)
	reconcileProvider(t, f)
	// The model's calls cost the provider $1.00; the route's 5% took the
	// charge to $1.05, and a call that settled after its stage's wait ran out
	// is the record's own unattributed $0.05.
	reconcileRecord(t, f, "20261014-090000-aaaa", &runstore.Cost{
		ModelUSD: 1.10, ModelBasis: runstore.BasisAPIList,
		ModelBy: map[string]float64{"deepseek/deepseek-v4.1-flash": 1.05, budgetReconcileUnattributed: 0.05},
		RouteBy: map[string]float64{"openrouter": 1.05, budgetReconcileUnattributed: 0.05},
		// The provider's own figure and the table's band over the calls it
		// reported a cost for: both without the route fee.
		ReportedUSD: 0.90, ReportedBy: map[string]float64{"deepseek/deepseek-v4.1-flash": 0.90},
		TableLoUSD: 0.80, TableHiUSD: 1.00, UnreportedCalls: 2,
		ModelTokens: map[string]runstore.ModelTokenCounts{
			"deepseek/deepseek-v4.1-flash": {Input: 1000, Output: 200, CacheRead: 100, CacheWrite5m: 50},
		},
		ServiceTiers: []string{"priority"},
	})

	doc := reconcileWalk(t, "--repo", "acme/app")
	if doc.Project != "aurora" || doc.From != "2026-10-12" || doc.To != "2026-11-16" || doc.Repo != "acme/app" || doc.All {
		t.Fatalf("window = %+v", doc)
	}
	i := reconcileModel(t, doc, "deepseek/deepseek-v4.1-flash")
	if m := doc.Models[i]; m.ChargeUSD != 1.05 || m.ReportUSD != 0.9 || m.BandLoUSD != 0.8 || m.BandHiUSD != 1.0 {
		t.Errorf("row = %+v", m)
	}
	if m := doc.Models[i]; m.InputTokens != 1000 || m.OutputTokens != 200 || m.CacheReadTokens != 100 || m.CacheWriteTokens != 50 {
		t.Errorf("tokens = %+v", m)
	}
	if m := doc.Models[i]; m.Runs != 1 || m.RunsWithReport != 1 || len(m.ServiceTiers) != 1 || m.ServiceTiers[0] != "priority" {
		t.Errorf("counts = %+v", m)
	}
	u := reconcileModel(t, doc, budgetReconcileUnattributed)
	if m := doc.Models[u]; m.ChargeUSD != 0.05 || m.ReportUSD != 0 || m.Runs != 1 || m.RunsWithReport != 0 {
		t.Errorf("unattributed row = %+v", m)
	}
	if t2 := doc.Totals; t2.ChargeUSD != 1.10 || t2.ChargeWithoutFeeUSD != 1.05 || t2.ReportUSD != 0.9 || t2.BandLoUSD != 0.8 || t2.BandHiUSD != 1.0 {
		t.Errorf("totals = %+v", t2)
	}
	if t2 := doc.Totals; t2.Runs != 1 || t2.RunsWithReport != 1 || t2.UnreportedCalls != 2 || t2.NoRecord != 0 {
		t.Errorf("totals counts = %+v", t2)
	}
	if len(doc.Warnings) != 0 {
		t.Errorf("warnings = %v", doc.Warnings)
	}

	out, errOut, err := execute(t, append([]string{"budget", "reconcile"}, append(reconcileWindowArgs(), "--repo", "acme/app")...)...)
	if err != nil {
		t.Fatalf("budget reconcile: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "project: aurora\nwindow:  2026-10-12 to 2026-11-16, acme/app\n") {
		t.Errorf("the reading does not open with what it read:\n%s", out)
	}
	if got, want := strings.Join(reconcileTableRow(t, out, "deepseek/deepseek-v4.1-flash"), " "),
		"deepseek/deepseek-v4.1-flash $1.05 $0.90 $0.80-$1 1000 200 100 50 1 1 priority"; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
	// The residual is a row of its own and is never folded into a model's.
	// The tier is the run's own fact and is listed on every row the run
	// touched, this one included.
	if got, want := strings.Join(reconcileTableRow(t, out, budgetReconcileUnattributed), " "),
		"unattributed $0.05 - - 0 0 0 0 1 0 priority"; got != want {
		t.Errorf("unattributed row = %q, want %q", got, want)
	}
	if got, want := strings.Join(reconcileTableRow(t, out, "TOTAL"), " "),
		"TOTAL $1.10 $0.90 $0.80-$1 1000 200 100 50 1 1"; got != want {
		t.Errorf("total row = %q, want %q", got, want)
	}
	for _, want := range []string{
		"runs:     1 with a cost breakdown, 0 with a record and none, 0 with no readable record, 0 that never started\n",
		"charge:   $1.10 with the route fee, $1.05 without it (what the providers themselves charged)\n",
		"report:   $0.90 reported by the providers over 1 of 1 runs; the table band over those calls is $0.80-$1\n",
		"calls:    2 settled calls carried no provider cost, so the report and the band are short of them\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary carries no %q:\n%s", want, out)
		}
	}
}

// TestBudgetReconcileTierCellIsOneLine: a service tier is a value the
// provider's own response carried, which the run's record keeps as it came, so
// the table's last cell holds text nothing here wrote. A newline in one would
// forge a line of the reading's own output -- the row would end mid-cell and
// what followed would read as a line of the table -- and an escape sequence
// would reach the terminal and rewrite what is on it.
func TestBudgetReconcileTierCellIsOneLine(t *testing.T) {
	f := newCloudFixture(t)
	reconcileRecord(t, f, "20261014-090000-aaaa", &runstore.Cost{
		ModelUSD: 1.00, ModelBasis: runstore.BasisAPIList,
		ModelBy:      map[string]float64{"vendor/model-x": 1.00},
		ServiceTiers: []string{"prio\nrity", "flex\x1b[31m", "tier\ttab"},
	})

	out, errOut, err := execute(t, append([]string{"budget", "reconcile"}, append(reconcileWindowArgs(), "--repo", "acme/app")...)...)
	if err != nil {
		t.Fatalf("budget reconcile: %v (%s)", err, errOut)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("the table carries the escape sequence a tier name held:\n%q", out)
	}
	// The whole row is one line and the cell is one cell: the newline and the
	// tab are marked, and the escape sequence goes the way every other value
	// of that origin goes.
	if got, want := strings.Join(reconcileTableRow(t, out, "vendor/model-x"), " "),
		"vendor/model-x $1 - - 0 0 0 0 1 0 flex, prio?rity, tier?tab"; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

// TestBudgetReconcileOldRecordsMixed is the rule for a record written before
// reported_by existed: its reported_usd is one figure for the whole run, so it
// lands on the model's row when the run charged on exactly one, and under
// `mixed` -- with its band, which the record keeps whole the same way -- when
// the run's charge names several.
func TestBudgetReconcileOldRecordsMixed(t *testing.T) {
	f := newCloudFixture(t)
	reconcileRecord(t, f, "20261014-090000-aaaa", &runstore.Cost{
		ModelUSD: 2.00, ModelBasis: runstore.BasisAPIList,
		ModelBy:     map[string]float64{"vendor/model-x": 1.00, "vendor/model-y": 1.00},
		ReportedUSD: 1.80, TableLoUSD: 1.60, TableHiUSD: 2.00,
	})
	reconcileRecord(t, f, "20261015-090000-bbbb", &runstore.Cost{
		ModelUSD: 0.50, ModelBasis: runstore.BasisAPIList,
		ModelBy:     map[string]float64{"vendor/model-x": 0.50},
		ReportedUSD: 0.40, TableLoUSD: 0.30, TableHiUSD: 0.45,
	})

	doc := reconcileWalk(t, "--repo", "acme/app")
	mixed := reconcileModel(t, doc, budgetReconcileMixed)
	if m := doc.Models[mixed]; m.ReportUSD != 1.80 || m.BandLoUSD != 1.60 || m.BandHiUSD != 2.00 || m.ChargeUSD != 0 || m.Runs != 1 {
		t.Errorf("mixed = %+v: a whole-run figure of a run charged on two models belongs on no model's row", m)
	}
	x := reconcileModel(t, doc, "vendor/model-x")
	if m := doc.Models[x]; m.ChargeUSD != 1.50 || m.ReportUSD != 0.40 || m.BandLoUSD != 0.30 || m.BandHiUSD != 0.45 {
		t.Errorf("vendor/model-x = %+v: the single-model run's report belongs on its own model's row", m)
	}
	if m := doc.Models[x]; m.Runs != 2 || m.RunsWithReport != 1 {
		t.Errorf("vendor/model-x counts = %+v", m)
	}
	y := reconcileModel(t, doc, "vendor/model-y")
	if m := doc.Models[y]; m.ChargeUSD != 1.00 || m.ReportUSD != 0 || m.Runs != 1 || m.RunsWithReport != 0 {
		t.Errorf("vendor/model-y = %+v: a row of its own charge and nothing else", m)
	}
	if t2 := doc.Totals; t2.ReportUSD != 2.20 || t2.ChargeUSD != 2.50 || t2.RunsWithReport != 2 || t2.Runs != 2 {
		t.Errorf("totals = %+v: the rows are the total, mixed included", t2)
	}
	// The order puts the models by charge and the reading's own buckets after
	// every one of them, so a model is never read beside a bucket.
	if got := []string{doc.Models[0].Model, doc.Models[1].Model, doc.Models[2].Model}; got[0] != "vendor/model-x" || got[1] != "vendor/model-y" || got[2] != budgetReconcileMixed {
		t.Errorf("row order = %v", got)
	}
}

// TestBudgetReconcileNoSplitCharge is the record that names no model for its
// money: a run with no gateway -- a subscription's plan, or Claude Code's own
// total -- carries model_usd and no model_by at all. That charge is still the
// window's spend, so it is read on `mixed` rather than dropped from the rows
// while the notional line counts all of it.
func TestBudgetReconcileNoSplitCharge(t *testing.T) {
	f := newCloudFixture(t)
	reconcileRecord(t, f, "20261014-090000-aaaa", &runstore.Cost{
		ModelUSD: 3.00, ModelBasis: runstore.BasisSubscription,
		ModelTokens: map[string]runstore.ModelTokenCounts{"vendor/model-x": {Input: 10, Output: 5}},
	})

	doc := reconcileWalk(t, "--repo", "acme/app")
	mixed := reconcileModel(t, doc, budgetReconcileMixed)
	if m := doc.Models[mixed]; m.ChargeUSD != 3.00 || m.Runs != 1 {
		t.Errorf("mixed = %+v, want the record's whole model figure", m)
	}
	// The tokens are the run's own by model and the record names no dollars
	// for that model: that row carries the counts and no charge of its own.
	x := reconcileModel(t, doc, "vendor/model-x")
	if m := doc.Models[x]; m.ChargeUSD != 0 || m.InputTokens != 10 || m.OutputTokens != 5 || m.Runs != 1 {
		t.Errorf("vendor/model-x = %+v", m)
	}
	if t2 := doc.Totals; t2.ChargeUSD != 3.00 || t2.NotionalUSD != 3.00 || t2.Runs != 1 || t2.NoRecord != 0 {
		t.Errorf("totals = %+v, want the charge whole and the notional part inside it", t2)
	}
	out, _, err := execute(t, append([]string{"budget", "reconcile"}, append(reconcileWindowArgs(), "--repo", "acme/app")...)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"charge:   $3 with the route fee, $3 without it (what the providers themselves charged)\n",
		"notional: $3 of the charge was a subscription's plan, which the provider reports and no invoice bills\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary carries no %q:\n%s", want, out)
		}
	}
}

// TestBudgetReconcileReportShare is what the acceptance asks a reader to be
// able to say: of the window's runs, how many carry a provider report at all.
// The share is per row and over the window, and the runs the reading could not
// take a figure from are counted apart rather than read as zeros.
func TestBudgetReconcileReportShare(t *testing.T) {
	f := newCloudFixture(t)
	// Three runs on one model: two carry a report, one was settled by the
	// table with no provider figure at all.
	for i, reported := range []bool{true, true, false} {
		id := []string{"20261014-090000-aaaa", "20261015-090000-bbbb", "20261016-090000-cccc"}[i]
		cost := &runstore.Cost{ModelUSD: 1.00, ModelBasis: runstore.BasisAPIList, ModelBy: map[string]float64{"vendor/model-x": 1.00}}
		if reported {
			cost.ReportedUSD, cost.ReportedBy = 0.90, map[string]float64{"vendor/model-x": 0.90}
		}
		reconcileRecord(t, f, id, cost)
	}
	// A run that launched and wrote no record is counted, and in no money
	// column; one that never started is the reader's own count.
	seedRun(t, f, "20261017-090000-dddd", "", "someone@example.com", true)
	seedRun(t, f, "20261018-090000-eeee", "", "someone@example.com", false)

	doc := reconcileWalk(t, "--repo", "acme/app")
	x := reconcileModel(t, doc, "vendor/model-x")
	if m := doc.Models[x]; m.Runs != 3 || m.RunsWithReport != 2 || m.ReportUSD != 1.80 || m.ChargeUSD != 3.00 {
		t.Errorf("row = %+v, want 3 runs of which 2 carried a report", m)
	}
	if t2 := doc.Totals; t2.Runs != 3 || t2.RunsWithReport != 2 || t2.ReportUSD != 1.80 {
		t.Errorf("totals = %+v, want 2 of the 3 runs with a cost breakdown to carry a report", t2)
	}
	if t2 := doc.Totals; t2.NoRecord != 1 || t2.Unlaunched != 1 || t2.NoCost != 0 || t2.RecordCorrupt != 0 {
		t.Errorf("totals = %+v, want the run with no record and the run that never started counted apart", t2)
	}
	out, _, err := execute(t, append([]string{"budget", "reconcile"}, append(reconcileWindowArgs(), "--repo", "acme/app")...)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"report:   $1.80 reported by the providers over 2 of 3 runs\n",
		"runs:     3 with a cost breakdown, 0 with a record and none, 1 with no readable record, 1 that never started\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary carries no %q:\n%s", want, out)
		}
	}
	// A window of records written before the band existed says nothing about
	// one rather than printing a dash beside the report it would be read
	// against.
	if strings.Contains(out, "the table band over those calls is -") {
		t.Errorf("the summary reads a band the records do not carry:\n%s", out)
	}
}

// TestBudgetReconcileTruncated is the bound's half of the reading: a window
// holding more runs than one read opens says so rather than publishing figures
// over part of itself silently. The bound is lowered here rather than seeded to
// five thousand runs; what it does to a window is the same either way.
func TestBudgetReconcileTruncated(t *testing.T) {
	restore := budgetReconcileMaxRuns
	budgetReconcileMaxRuns = 2
	t.Cleanup(func() { budgetReconcileMaxRuns = restore })

	f := newCloudFixture(t)
	for _, id := range []string{"20261014-090000-aaaa", "20261015-090000-bbbb", "20261016-090000-cccc"} {
		reconcileRecord(t, f, id, &runstore.Cost{
			ModelUSD: 1.00, ModelBasis: runstore.BasisAPIList, ModelBy: map[string]float64{"vendor/model-x": 1.00},
		})
	}
	doc := reconcileWalk(t, "--repo", "acme/app")
	if !doc.Truncated || doc.MaxRuns != 2 {
		t.Fatalf("document = %+v, want a truncated read of the newest 2", doc)
	}
	if doc.Totals.Runs != 2 || doc.Totals.ChargeUSD != 2.00 {
		t.Errorf("totals = %+v, want the newest 2 runs alone", doc.Totals)
	}
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], "the window holds more runs than the 2 this reading opens") {
		t.Errorf("warnings = %v, want the truncation stated", doc.Warnings)
	}
	out, errOut, err := execute(t, append([]string{"budget", "reconcile"}, append(reconcileWindowArgs(), "--repo", "acme/app")...)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "window:  2026-10-12 to 2026-11-16, acme/app, truncated to the newest 2 runs\n") {
		t.Errorf("the window line does not say the read was truncated:\n%s", out)
	}
	if !strings.Contains(errOut, "warning: the window holds more runs than the 2 this reading opens") {
		t.Errorf("the truncation is not warned on stderr: %q", errOut)
	}
}
