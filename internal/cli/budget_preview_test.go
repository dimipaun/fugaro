package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// usd is a dollar amount as a database cap node stores it.
func usd(v float64) budget.Micros {
	m, err := pricing.FromUSD(v)
	if err != nil {
		panic(err)
	}
	return m
}

// SetCaps seeds the database's caps: the project's daily and per-run caps in
// /config/caps/global and the repository's in Defaults (there is no
// repository node of its own, as in a project that sets --defaults only).
func (f *budgetFixture) SetCaps(globalDaily, globalPerRun, repoDaily, repoPerRun float64) {
	f.db.Set(budget.PathCapsGlobal, map[string]any{
		"dailyMicros": usd(globalDaily), "perRunMicros": usd(globalPerRun)})
	f.db.Set(budget.PathCapsDefaults, map[string]any{
		"repoDailyMicros": usd(repoDaily), "repoPerRunMicros": usd(repoPerRun)})
}

// SetSpentToday seeds today's counters, project-wide and for acme/app.
func (f *budgetFixture) SetSpentToday(v float64) {
	d := today()
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": usd(v)})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": usd(v)})
}

// NoPerRunCap drops the owner's budget.per_run_usd from the project config,
// so only the database's own caps (when any) bound a run.
func (f *budgetFixture) NoPerRunCap(t *testing.T) {
	t.Helper()
	f.rewriteConfig(t, "per_run_usd: 5, ", "")
}

// SetGlobalCapsOnly seeds the project-wide caps alone: no repository and no
// default node, as an installation that ran set --global only.
func (f *budgetFixture) SetGlobalCapsOnly(daily, perRun float64) {
	f.db.Set(budget.PathCapsGlobal, map[string]any{
		"dailyMicros": usd(daily), "perRunMicros": usd(perRun)})
}

// DefaultRunsBucket names the runs bucket fugaro-runs-<gcp_project>, which is
// the one project recipes are published in: with it, projectRecipesNote is
// empty and the project layer is really read.
func (f *budgetFixture) DefaultRunsBucket(t *testing.T) {
	t.Helper()
	f.rewriteConfig(t, "runs_bucket: unused-bucket", "runs_bucket: fugaro-runs-proj-1234")
}

// DropMarker removes the project marker every cloud command checks, so an
// open of the runs bucket fails: a command that still succeeds never opened
// it.
func (f *budgetFixture) DropMarker(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.runsDir(), filepath.FromSlash(infra.ProjectMarkerObject))); err != nil {
		t.Fatal(err)
	}
}

// rewriteConfig replaces old with new ("" deletes) in the project config.
func (f *budgetFixture) rewriteConfig(t *testing.T, old, new string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("%s does not hold %q", path, old)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(data, []byte(old), []byte(new)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Checkout makes the working directory a checkout of acme/app whose
// fugaro.yaml carries agentYAML under agent:, so the preview reads the
// repository's knobs the way run does.
func (f *budgetFixture) Checkout(t *testing.T, agentYAML string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
		"agent:\n  auth: api-key\n" + agentYAML +
		"workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// previewStage is one stage of a seeded result.json: its kind, the model it
// was pinned to, and what its calls cost.
type previewStage struct {
	kind, model string
	usd         float64
	// notional is the part of usd a subscription paid (C1.16, P12): a
	// cascade stage's figure holds both, and only the rest is a real dollar.
	notional float64
}

// SeedRuns writes n runs of acme/app into the runs bucket, each with the
// same stages, as the runner records them (generic-tool Task 24's
// stages[].model_usd and stages[].model). The runs are minted one hour
// apart, the first firstHourAgo hours back, so a test can seed more than
// one batch. A stage with usd 0 writes no model_usd, as a record from
// before that key existed does.
func (f *budgetFixture) SeedRuns(t *testing.T, firstHourAgo, n int, stages []previewStage) {
	t.Helper()
	f.SeedRunsBasis(t, firstHourAgo, n, stages, "")
}

// SeedRunsBasis is SeedRuns with the run's cost basis (result.json's
// cost.model_basis): runstore.BasisSubscription is an oauth run, whose
// model_usd is notional; "" is the record of a run that does not say, which
// the loader reads as real dollars. The run's own cost carries the stages'
// notional part summed, as the runner records it: a cascade run whose every
// call the plan served has a whole notional model figure under an api-list
// basis, which is the row a basis test cannot read (C1.16, P12).
func (f *budgetFixture) SeedRunsBasis(t *testing.T, firstHourAgo, n int, stages []previewStage, basis string) {
	t.Helper()
	now := time.Now().UTC()
	for i := range n {
		id := now.Add(-time.Duration(firstHourAgo+i)*time.Hour).Format("20060102-150405") + "-aaaa"
		var ss []map[string]any
		var total, notional float64
		for _, s := range stages {
			st := map[string]any{"name": s.kind, "started_at": now.UTC().Format(time.RFC3339), "duration_s": 1.0}
			if s.usd > 0 {
				st["model_usd"] = s.usd
				total += s.usd
			}
			if s.notional > 0 {
				st["model_notional_usd"] = s.notional
				notional += s.notional
			}
			if s.model != "" {
				st["model"] = s.model
			}
			ss = append(ss, st)
		}
		rec := map[string]any{
			"version": 1, "run_id": id, "repo": "acme/app", "status": "succeeded", "stages": ss}
		if basis != "" {
			cost := map[string]any{"model_usd": total, "model_basis": basis}
			if notional > 0 {
				cost["model_notional_usd"] = notional
			}
			rec["cost"] = cost
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(f.runsDir(), "runs", appSlug, id, "result.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// SeedUnreadableRun writes a result.json that cannot be read, hoursAgo hours
// back: a damaged object, as a run whose record was lost mid-write leaves.
func (f *budgetFixture) SeedUnreadableRun(t *testing.T, hoursAgo int) {
	t.Helper()
	id := time.Now().UTC().Add(-time.Duration(hoursAgo)*time.Hour).Format("20060102-150405") + "-aaaa"
	p := filepath.Join(f.runsDir(), "runs", appSlug, id, "result.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"version": 1, "stages": [`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// previewRunStages is the shape a run of a first_line + review recipe
// records: the implementation, the first line's review and its fix, the
// senior review, and its fix.
func previewRunStages(implement, reviewFirst, fix, review float64) []previewStage {
	return []previewStage{
		{kind: "implement", usd: implement},
		{kind: "review_first", usd: reviewFirst},
		{kind: "fix", usd: fix},
		{kind: "review", usd: review},
		{kind: "fix", usd: fix},
	}
}

// TestBudgetPreviewTypical: with enough recorded runs, the preview says what
// a run of the recipe typically costs: the mean recorded cost of each stage
// kind, at the recipe's typical stage count (one round each).
func TestBudgetPreviewTypical(t *testing.T) {
	for name, tc := range map[string]struct {
		recipe string
		want   string
	}{
		// standard bounces (bounce: first_line), so one round each is
		// implement 1, review_first 2, fix 3, review 1 = 7 stages:
		// $0.60 + 2×$0.10 + 3×$0.20 + $1.00 = $2.40.
		"standard": {recipe: "standard", want: "typical: $2.40 a run (7 stages, from 6 runs of the last 30 days)"},
		// solo has no first_line step: implement 1, review 1, fix 1 = 3
		// stages; $0.60 + $1.00 + $0.20 = $1.80.
		"solo": {recipe: "solo", want: "typical: $1.80 a run (3 stages, from 6 runs of the last 30 days)"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBudgetFixture(t, "")
			f.SetCaps(100, 3, 20, 3)
			f.SetSpentToday(0)
			f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
			f.SeedRuns(t, 0, 6, previewRunStages(0.60, 0.10, 0.20, 1.00))
			out, _, err := execute(t, "budget", "preview", "--recipe", tc.recipe, "--runs", "6")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the typical line is missing or wrong, want %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestBudgetPreviewTypicalNeedsFiveRuns: fewer than five recorded runs say
// nothing at all, as the design asks.
func TestBudgetPreviewTypicalNeedsFiveRuns(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 4, previewRunStages(0.60, 0.10, 0.20, 1.00))
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "typical") || strings.Contains(errOut, "typical") {
		t.Errorf("four runs are fewer than the five a typical line needs:\n%s%s", out, errOut)
	}
}

// TestBudgetPreviewTypicalSkipsOldRecords: a record from before result.json
// carried a cost per stage has nothing to average, so it is skipped and
// said to be skipped -- never counted as a run that cost nothing.
func TestBudgetPreviewTypicalSkipsOldRecords(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 4, previewRunStages(0.60, 0.10, 0.20, 1.00))
	f.SeedRuns(t, 4, 3, []previewStage{{kind: "implement"}, {kind: "review"}})
	note := func(runs int) string {
		return fmt.Sprintf("3 of the %d runs of acme/app in the last 30 days recorded no cost for any stage (a result.json from before the runner kept one, or stages that all cost nothing); they are not counted", runs)
	}
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// Four priced runs are still fewer than five, and the skipped ones must
	// not make up the difference.
	if strings.Contains(out, "typical:") {
		t.Errorf("four priced runs printed a typical line:\n%s", out)
	}
	if !strings.Contains(errOut, note(7)) {
		t.Errorf("the skipped records are not explained:\n%s", errOut)
	}
	// One priced run more: the line appears, over the priced runs alone.
	f.SeedRuns(t, 7, 1, previewRunStages(0.60, 0.10, 0.20, 1.00))
	out, errOut, err = execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, from 5 runs of the last 30 days)") {
		t.Errorf("the typical line does not stop at the priced runs:\n%s", out)
	}
	if !strings.Contains(errOut, note(8)) {
		t.Errorf("the skipped records are no longer explained:\n%s", errOut)
	}
}

// TestBudgetPreviewTypicalWindowIsThirtyDays: the typical line is the
// repository's last 30 days (previewHistoryWindow, the design's window), not
// everything the runs bucket still holds, and a run older than the window
// must not reach the figure or the counts beside it — an old run is not a
// run of the window that cost nothing.
//
// Mutation (run, restore): previewHistoryWindow to 60*24*time.Hour — the
// 31-day run is read, and both assertions here fail: the figure is taken
// from 6 runs, and the 40-day record is reported as a run of the window.
func TestBudgetPreviewTypicalWindowIsThirtyDays(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 5, previewRunStages(0.60, 0.10, 0.20, 1.00))
	// One priced run outside the window (31 days), and one plain record
	// outside it too (40 days), which the notes would count as unpriced if
	// the window reached it.
	f.SeedRuns(t, 31*24, 1, previewRunStages(9.00, 9.00, 9.00, 9.00))
	f.SeedRuns(t, 40*24, 1, []previewStage{{kind: "implement"}, {kind: "review"}})
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, from 5 runs of the last 30 days)") {
		t.Errorf("a run older than the window reached the figure:\n%s", out)
	}
	if strings.Contains(errOut, "recorded no cost for any stage") {
		t.Errorf("a run older than the window was counted among the runs of the window:\n%s", errOut)
	}
}

// TestBudgetPreviewTypicalSkipsUnreadableRecords: a run whose result.json is
// missing or damaged is not an old record, and the note must not call it one:
// each group of runs the figure cannot use is named for what it is.
func TestBudgetPreviewTypicalSkipsUnreadableRecords(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 5, previewRunStages(0.60, 0.10, 0.20, 1.00))
	f.SeedUnreadableRun(t, 6)
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, from 5 runs of the last 30 days)") {
		t.Errorf("the readable runs have no figure:\n%s", out)
	}
	const want = "1 of the 6 runs of acme/app in the last 30 days have no readable result.json; they are not counted"
	if !strings.Contains(errOut, want) {
		t.Errorf("the unreadable record is not named as one:\n%s", errOut)
	}
	if strings.Contains(errOut, "before the runner kept one") {
		t.Errorf("an unreadable record is reported as an old one:\n%s", errOut)
	}
}

// TestBudgetPreviewTypicalExcludesSubscriptionRuns: an oauth run records
// model_usd as what it counted against the plan, not as dollars charged
// (design §10.1), so it must never be averaged into a figure printed beside
// real caps -- and the noting of what was left out must say why.
func TestBudgetPreviewTypicalExcludesSubscriptionRuns(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	// An oauth-only window: notional costs cannot make a real-dollar figure,
	// so there is no line at all, and the note says why.
	f.SeedRunsBasis(t, 0, 6, previewRunStages(9.00, 9.00, 9.00, 9.00), runstore.BasisSubscription)
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "typical:") {
		t.Errorf("notional costs printed a real-dollar typical line:\n%s", out)
	}
	const only = "6 of the 6 runs of acme/app in the last 30 days ran on a subscription (agent.auth oauth, or a cascade run the plan served in full): their model_usd is notional and is not averaged into a real-dollar figure"
	if !strings.Contains(errOut, only) {
		t.Errorf("the oauth runs are not explained:\n%s", errOut)
	}
	// Five real-dollar runs beside the six notional ones, costing a tenth as
	// much: the line comes from the five, and the note says how many it left
	// out.
	f.SeedRuns(t, 6, 5, previewRunStages(0.60, 0.10, 0.20, 1.00))
	out, errOut, err = execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, from 5 runs of the last 30 days)") {
		t.Errorf("the notional runs are mixed into the figure:\n%s", out)
	}
	const mixed = "6 of the 11 runs of acme/app in the last 30 days ran on a subscription (agent.auth oauth, or a cascade run the plan served in full): their model_usd is notional and is not averaged into a real-dollar figure"
	if !strings.Contains(errOut, mixed) {
		t.Errorf("the excluded oauth runs are not counted:\n%s", errOut)
	}
}

// TestBudgetPreviewTypicalCountsAMixedStageAtItsMoney (C1.16, P12, D140): the
// window's runs are cascade ones, whose stage figures hold the calls the
// subscription paid as well as the billed ones. The mean a real-dollar line is
// built from is the money part of each stage: counting the notional dollars in
// it would print a typical run priced mostly by the plan.
func TestBudgetPreviewTypicalCountsAMixedStageAtItsMoney(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	stages := previewRunStages(0.60, 0.10, 0.20, 1.00)
	for i, n := range []float64{0.50, 0.05, 0, 0.50, 0} {
		stages[i].notional = n
	}
	f.SeedRuns(t, 0, 6, stages)
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// Money only: implement $0.10, review_first $0.05, fix $0.20, review
	// $0.50 -- 1 + 2×$0.05 + 3×$0.20 + $0.50 = $1.30, not the $2.40 the whole
	// figures would make.
	if !strings.Contains(out, "typical: $1.30 a run") {
		t.Errorf("the notional part of the stages is in the figure:\n%s", out)
	}
}

// TestBudgetPreviewTypicalCountsAnAllNotionalCascadeRunAsNotional (C1.16, P12,
// D140): a cascade run the plan served in full is api-list by basis -- the
// billed part's -- and notional in its whole model figure. The window's note
// must count it among the notional runs: read off the basis it is a run whose
// record held no per-stage cost at all, which is a different and false
// statement about it, and the one a reader takes for "the runner kept none".
func TestBudgetPreviewTypicalCountsAnAllNotionalCascadeRunAsNotional(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	// An all-notional window: every stage's whole figure is the plan's, so
	// there is no real-dollar line at all, and the note says why.
	stages := previewRunStages(9.00, 9.00, 9.00, 9.00)
	for i := range stages {
		stages[i].notional = stages[i].usd
	}
	f.SeedRunsBasis(t, 0, 6, stages, runstore.BasisAPIList)
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "typical:") {
		t.Errorf("a plan-paid cascade window printed a real-dollar typical line:\n%s", out)
	}
	const only = "6 of the 6 runs of acme/app in the last 30 days ran on a subscription (agent.auth oauth, or a cascade run the plan served in full): their model_usd is notional and is not averaged into a real-dollar figure"
	if !strings.Contains(errOut, only) {
		t.Errorf("the plan-paid cascade runs are not explained:\n%s", errOut)
	}
	if strings.Contains(errOut, "recorded no cost for any stage") {
		t.Errorf("a plan-paid cascade run is reported as a record with no per-stage cost:\n%s", errOut)
	}
}

// TestBudgetPreviewTypicalJSONSubscription: --json says the same, in its own
// fields: the runs the figure was taken from, and of those it left out, the
// notional ones.
func TestBudgetPreviewTypicalJSONSubscription(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 5, previewRunStages(0.60, 0.10, 0.20, 1.00))
	f.SeedRunsBasis(t, 5, 2, previewRunStages(9.00, 9.00, 9.00, 9.00), runstore.BasisSubscription)
	f.SeedUnreadableRun(t, 8)
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Typical struct {
			USD        float64 `json:"usd"`
			Runs       int     `json:"runs"`
			Skipped    int     `json:"skipped"`
			Notional   int     `json:"notional"`
			Unreadable int     `json:"unreadable"`
			Unpriced   int     `json:"unpriced"`
		} `json:"typical"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if doc.Typical.USD != 2.40 || doc.Typical.Runs != 5 {
		t.Errorf("the figure is not the five real-dollar runs: %+v", doc.Typical)
	}
	if doc.Typical.Skipped != 3 || doc.Typical.Notional != 2 || doc.Typical.Unreadable != 1 || doc.Typical.Unpriced != 0 {
		t.Errorf("the runs left out are not split by why: %+v", doc.Typical)
	}
}

// TestBudgetPreviewTypicalCountsMatchTheBound: the typical stage count is
// the recipe's own bound with one round of each loop, so the kinds the
// history is averaged over must sum to it for every catalog recipe and every
// way the round knobs can leave a loop out: the mirror of the runner's loop
// and StageBound must never drift apart.
func TestBudgetPreviewTypicalCountsMatchTheBound(t *testing.T) {
	for _, name := range recipe.CatalogNames() {
		t.Run(name, func(t *testing.T) {
			text, ok := recipe.CatalogText(name)
			if !ok {
				t.Fatalf("no catalog recipe %s", name)
			}
			rp, problems := recipe.Parse(text)
			if len(problems) > 0 {
				t.Fatalf("%s: %s", name, recipe.ProblemsText(problems))
			}
			for _, f := range []int{0, 1} {
				for _, n := range []int{0, 1} {
					sum := 0
					for _, c := range previewTypicalCounts(rp, f, n) {
						sum += c
					}
					if want := recipe.StageBound(rp, f, n); sum != want {
						t.Errorf("first_line_rounds %d, review_rounds %d: the typical shape has %d stages, the bound has %d", f, n, sum, want)
					}
				}
			}
		})
	}
}

// TestBudgetPreviewDigestCountsNoImplementStage: run-digest's first step is
// the runner's own work, which writes the run reports with no model call, so
// the typical run the preview prices has no implement stage to average --
// counting one would quote a stage the run never starts, on the coder's
// model, at the recipe's own bound. The catalog-wide pin above keeps this
// and recipe.StageBound moving together; this test names the shape.
func TestBudgetPreviewDigestCountsNoImplementStage(t *testing.T) {
	text, ok := recipe.CatalogText("run-digest")
	if !ok {
		t.Fatal("no catalog recipe run-digest")
	}
	rp, problems := recipe.Parse(text)
	if len(problems) > 0 {
		t.Fatalf("run-digest: %s", recipe.ProblemsText(problems))
	}
	counts := previewTypicalCounts(rp, 3, 10)
	if n := counts["implement"]; n != 0 {
		t.Errorf("implement stages = %d, want none", n)
	}
	sum := 0
	for _, c := range counts {
		sum += c
	}
	if sum != 2 { // the senior review and its fix
		t.Errorf("typical stages = %d, want 2: %+v", sum, counts)
	}
}

// TestBudgetPreviewAdversarialStages: the level-3 step is a review round of
// its own, so a recipe holding it prices one more review and its fix, plus --
// when the step bounces, as the senior review's does -- one more pair of cheap
// stages. No catalog recipe carries the step, so the catalog-wide pin above
// never reaches this arm; these shapes are its own. The mirror is the bound
// where the step caps itself at the one round a typical run spends, which is
// where that pin reads it too; the parser's default of two rounds is the
// bound's and not the typical run's, exactly as review_rounds 2 is standard's.
func TestBudgetPreviewAdversarialStages(t *testing.T) {
	parse := func(t *testing.T, step string) *recipe.Recipe {
		t.Helper()
		rp, problems := recipe.Parse([]byte("version: 1\nname: level3\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - " + step + "\n"))
		if len(problems) > 0 {
			t.Fatalf("level3: %s", recipe.ProblemsText(problems))
		}
		return rp
	}
	// One round of each loop: without the step this is the catalog's standard
	// (implement, two cheap stages, one review and its fix), so the counts
	// below are what level 3 adds to it.
	for name, tc := range map[string]struct {
		step string
		want map[string]int
		// bound is StageBound(rp, 1, 1) here, and mirrors whether the typical
		// count must be it: it must, where the step's own cap is the one
		// round a typical run spends.
		bound   int
		mirrors bool
	}{
		"bouncing": {step: "adversarial: { max_rounds: 1, bounce: first_line }",
			want: map[string]int{"implement": 1, "review_first": 3, "review": 2, "fix": 5}, bound: 11, mirrors: true},
		"no bounce": {step: "adversarial: { max_rounds: 1 }",
			want: map[string]int{"implement": 1, "review_first": 2, "review": 2, "fix": 4}, bound: 9, mirrors: true},
		"the parser's default rounds": {step: "adversarial: { bounce: first_line }",
			want: map[string]int{"implement": 1, "review_first": 3, "review": 2, "fix": 5}, bound: 15},
	} {
		t.Run(name, func(t *testing.T) {
			rp := parse(t, tc.step)
			counts := previewTypicalCounts(rp, 1, 1)
			if !maps.Equal(counts, tc.want) {
				t.Errorf("counts = %v, want %v", counts, tc.want)
			}
			if bound := recipe.StageBound(rp, 1, 1); bound != tc.bound {
				t.Errorf("StageBound(rp, 1, 1) = %d, want %d", bound, tc.bound)
			}
			sum := 0
			for _, c := range counts {
				sum += c
			}
			if tc.mirrors && sum != tc.bound {
				t.Errorf("typical stages = %d, the bound = %d", sum, tc.bound)
			}
		})
	}
}

// TestBudgetPreviewTypicalCountsHonourTheRoundKnobs: the typical run is one
// round of each loop, but a knob at 0 leaves a loop out of the run
// altogether, so its stages leave the count too rather than being counted at
// one round. config.validate refuses a knob below 1 today, so this cannot be
// read from a checkout; the count stays the bound's mirror, so a knob that
// ever does reach 0 (a project layer, say) can never make the preview price
// stages the runner would not run. A knob above 1 is one round here: the
// typical run is the one that does not loop.
func TestBudgetPreviewTypicalCountsHonourTheRoundKnobs(t *testing.T) {
	text, ok := recipe.CatalogText("standard")
	if !ok {
		t.Fatal("no catalog recipe standard")
	}
	rp, problems := recipe.Parse(text)
	if len(problems) > 0 {
		t.Fatalf("standard: %s", recipe.ProblemsText(problems))
	}
	for name, tc := range map[string]struct {
		first, rounds int
		want          map[string]int
	}{
		// No first-line loop: implement, the senior review and its fix.
		"first_line_rounds 0": {first: 0, rounds: 2, want: map[string]int{
			"implement": 1, "review_first": 0, "review": 1, "fix": 1}},
		// No senior round, so nothing bounces back to the cheap loop either.
		"review_rounds 0": {first: 1, rounds: 0, want: map[string]int{
			"implement": 1, "review_first": 1, "review": 0, "fix": 1}},
		// The knobs at their usual values: one round each, the bounce back
		// counted, the two fix stages one kind.
		"both usual": {first: 1, rounds: 2, want: map[string]int{
			"implement": 1, "review_first": 2, "review": 1, "fix": 3}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := previewTypicalCounts(rp, tc.first, tc.rounds); !maps.Equal(got, tc.want) {
				t.Errorf("previewTypicalCounts(standard, %d, %d) = %v, want %v", tc.first, tc.rounds, got, tc.want)
			}
		})
	}
}

// TestBudgetPreviewTypicalNeedsEveryStageKind: a kind the recipe would run
// that the window has no cost for leaves the line out and says which one,
// rather than printing a sum that leaves that stage's cost at nothing. The
// same history answers a recipe that would not run the kind.
func TestBudgetPreviewTypicalNeedsEveryStageKind(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 6, []previewStage{
		{kind: "implement", usd: 0.60}, {kind: "fix", usd: 0.20}, {kind: "review", usd: 1.00}})
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "typical:") {
		t.Errorf("a kind with no recorded cost still printed a figure:\n%s", out)
	}
	const want = "no typical line: no review_first stage of acme/app recorded a cost in the last 30 days"
	if !strings.Contains(errOut, want) {
		t.Errorf("the absent kind is not named:\n%s", errOut)
	}
	// solo runs no review_first stage: the same history gives its figure.
	out, _, err = execute(t, "budget", "preview", "--recipe", "solo", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $1.80 a run (3 stages, from 6 runs of the last 30 days)") {
		t.Errorf("the recipe that runs no missing kind has no figure:\n%s", out)
	}
}

// TestBudgetPreviewTypicalUsesThePinnedModel: the mean is per stage kind and
// model, so history on the models this checkout pins is what counts, not
// what an earlier configuration ran on.
func TestBudgetPreviewTypicalUsesThePinnedModel(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  models: { coder: fast-1, reviewer: smart-2 }\n  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 5, []previewStage{
		{kind: "implement", model: "fast-1", usd: 0.60},
		{kind: "review_first", model: "fast-1", usd: 0.10},
		{kind: "fix", model: "fast-1", usd: 0.20},
		{kind: "fix", model: "fast-1", usd: 0.20},
		{kind: "review", model: "smart-2", usd: 1.00},
	})
	f.SeedRuns(t, 5, 5, previewRunStages(9.00, 9.00, 9.00, 9.00))
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// The pinned models' means only, at standard's 7 stages: $0.60 + 2×$0.10
	// + 3×$0.20 + $1.00 = $2.40, never anything the other model cost.
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, from 10 runs of the last 30 days)") {
		t.Errorf("the typical line is not the pinned models' costs:\n%s", out)
	}
}

// TestBudgetPreviewTypicalAppliesTheRecipeRoles: a recipe that maps one role
// onto the other's model makes its runs record the mapped model, so the pin
// the preview looks a kind's mean up on must be the mapped one -- premium
// (coder: reviewer) runs its whole loop on the reviewer's model, solo
// (reviewer: coder) its review on the coder's, whatever agent.models says.
func TestBudgetPreviewTypicalAppliesTheRecipeRoles(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  models: { coder: fast-1, reviewer: smart-2 }\n  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	// The window holds runs of both models, the strong one's stages costing
	// several times the cheap one's, so a pin on the wrong model is no near
	// miss: only the pinned model's mean answers.
	f.SeedRuns(t, 0, 5, []previewStage{
		{kind: "implement", model: "smart-2", usd: 3.00},
		{kind: "review_first", model: "smart-2", usd: 0.50},
		{kind: "fix", model: "smart-2", usd: 2.00},
		{kind: "review", model: "smart-2", usd: 1.00},
	})
	f.SeedRuns(t, 5, 5, []previewStage{
		{kind: "implement", model: "fast-1", usd: 0.30},
		{kind: "review_first", model: "fast-1", usd: 0.05},
		{kind: "fix", model: "fast-1", usd: 0.20},
		{kind: "review", model: "fast-1", usd: 0.10},
	})
	// premium maps coder onto reviewer, so its 3 stages all run on smart-2:
	// $3.00 + $2.00 + $1.00 = $6.00.
	out, _, err := execute(t, "budget", "preview", "--recipe", "premium", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $6.00 a run (3 stages, from 10 runs of the last 30 days)") {
		t.Errorf("premium's stages are not priced on the reviewer's model:\n%s", out)
	}
	// solo maps reviewer onto coder, so its review runs on fast-1 too:
	// $0.30 + $0.20 + $0.10 = $0.60.
	out, _, err = execute(t, "budget", "preview", "--recipe", "solo", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $0.60 a run (3 stages, from 10 runs of the last 30 days)") {
		t.Errorf("solo's review is not priced on the coder's model:\n%s", out)
	}
}

// TestBudgetPreviewTypicalNeedsThePinnedModel: a kind the window holds no
// cost for on the model the checkout pins has no figure of its own, and
// another model's mean is not a stand-in: pricing the model the run will use
// with what a cheaper one cost would understate the run, against the help's
// own promise. The note names the model the window is short of.
func TestBudgetPreviewTypicalNeedsThePinnedModel(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  models: { coder: fast-1, reviewer: smart-2 }\n  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	// Enough runs, all of the coder's stages on a model this checkout no
	// longer pins: only the reviewer's stages match the pin.
	f.SeedRuns(t, 0, 6, []previewStage{
		{kind: "implement", model: "old-3", usd: 0.05},
		{kind: "review_first", model: "old-3", usd: 0.01},
		{kind: "fix", model: "old-3", usd: 0.02},
		{kind: "review", model: "smart-2", usd: 1.00},
	})
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "typical:") {
		t.Errorf("the cheap model's mean priced the pinned one:\n%s", out)
	}
	const want = "no typical line: no implement stage of acme/app on fast-1 recorded a cost in the last 30 days"
	if !strings.Contains(errOut, want) {
		t.Errorf("the model the window is short of is not named:\n%s", errOut)
	}
	// The same window, with the coder's stages on the pinned model too: the
	// figure is the pinned models' costs only, never the old model's.
	f.SeedRuns(t, 6, 6, []previewStage{
		{kind: "implement", model: "fast-1", usd: 0.60},
		{kind: "review_first", model: "fast-1", usd: 0.10},
		{kind: "fix", model: "fast-1", usd: 0.20},
		{kind: "review", model: "smart-2", usd: 1.00},
	})
	out, errOut, err = execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, from 12 runs of the last 30 days)") {
		t.Errorf("the pinned models' stages have no figure:\n%s", out)
	}
	if strings.Contains(errOut, "no typical line") {
		t.Errorf("the missing model is still reported missing:\n%s", errOut)
	}
}

// TestBudgetPreviewTypicalJSON: --json carries the history figure and what it
// was taken from.
func TestBudgetPreviewTypicalJSON(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 5, previewRunStages(0.60, 0.10, 0.20, 1.00))
	f.SeedRuns(t, 5, 2, []previewStage{{kind: "implement"}})
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Typical *struct {
			USD      float64 `json:"usd"`
			Stages   int     `json:"stages"`
			Runs     int     `json:"runs"`
			Skipped  int     `json:"skipped"`
			Unpriced int     `json:"unpriced"`
		} `json:"typical"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Typical == nil || doc.Typical.USD != 2.40 || doc.Typical.Stages != 7 || doc.Typical.Runs != 5 ||
		doc.Typical.Skipped != 2 || doc.Typical.Unpriced != 2 {
		t.Fatalf("typical = %+v", doc.Typical)
	}
}

// TestBudgetPreviewCeiling: the ceiling is the smaller of the per-run cap and
// the recipe's stage bound times the per-stage cap.
func TestBudgetPreviewCeiling(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(5)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// standard with f=1, n=2: 1+2+2*(2+2) = 11 stages × $0.50 = $5.50, capped
	// by the $3 per-run cap: 6 × $3 = $18 ceiling, $15 headroom.
	// The database's cap is the one that binds, and the line names the scope
	// it came from: the default for repositories without one, not the
	// repository's own and not the global fallback (plan Task 0.3 step 7).
	for _, want := range []string{"11 stages", "rough worst case: $18.00", "6 runs × $3.00 (per-run cap: default)",
		"per-run caps found: budget.per_run_usd $5.00, the database's per-run cap (default) $3.00; the smallest counts",
		"may exceed the repository's daily cap ($15.00 left today)"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
}

// TestBudgetPreviewStageBoundBinds: when the recipe's own stage bound is the
// smaller side, it is the one named.
func TestBudgetPreviewStageBoundBinds(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.NoPerRunCap(t)
	f.SetCaps(100, 50, 100, 50)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "3")
	if err != nil {
		t.Fatal(err)
	}
	// 11 stages × $0.50 = $5.50 a run, under the $50 per-run cap: $16.50.
	for _, want := range []string{"rough worst case: $16.50", "agent.max_budget_usd", "$5.50"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
}

// TestBudgetPreviewUnbounded: with neither a per-run cap nor a readable
// per-stage cap, there is no ceiling, and the preview says what would bound
// it.
func TestBudgetPreviewUnbounded(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.NoPerRunCap(t)
	// No checkout: agent.max_budget_usd is unknown. No caps in the database
	// and no budget.per_run_usd either.
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "budget", "preview", "--repo", "acme/app", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unbounded: set budget.per_run_usd") {
		t.Fatalf("preview does not say it is unbounded:\n%s", out)
	}
}

// TestBudgetPreviewFitsAndExceeds: the verdict line against today's headroom.
func TestBudgetPreviewFitsAndExceeds(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fits the repository's daily cap ($20.00 left today)") {
		t.Fatalf("a $18 ceiling under a $20 cap does not fit:\n%s", out)
	}
	// The project's own cap, lower than the repository's: that one is over.
	f.SetCaps(10, 3, 20, 3)
	out, _, err = execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exceeds the project's daily cap ($10.00 left today)") {
		t.Fatalf("a $18 ceiling over a $10 project cap does not exceed it:\n%s", out)
	}
}

// TestBudgetPreviewJSON: the machine-readable form carries the figures the
// text prints.
func TestBudgetPreviewJSON(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(5)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Project    string   `json:"project"`
		Repo       string   `json:"repo"`
		Recipe     string   `json:"recipe"`
		Runs       int      `json:"runs"`
		Stages     int      `json:"stages"`
		KnobsFrom  string   `json:"knobs_from"`
		PerRunUSD  *float64 `json:"per_run_usd"`
		CeilingUSD *float64 `json:"ceiling_usd"`
		Verdict    string   `json:"verdict"`
		Headroom   struct {
			Repo   *float64 `json:"repo_usd"`
			Global *float64 `json:"global_usd"`
		} `json:"headroom"`
		Bounded bool `json:"bounded"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Project != "aurora" || doc.Repo != "acme/app" || doc.Recipe != "standard" || doc.Runs != 6 || doc.Stages != 11 {
		t.Errorf("doc = %+v", doc)
	}
	if doc.KnobsFrom != "checkout" || !doc.Bounded || doc.PerRunUSD == nil || *doc.PerRunUSD != 3 ||
		doc.CeilingUSD == nil || *doc.CeilingUSD != 18 || doc.Verdict != "may_exceed_repo" {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Headroom.Repo == nil || *doc.Headroom.Repo != 15 || doc.Headroom.Global == nil || *doc.Headroom.Global != 95 {
		t.Errorf("headroom = %+v", doc.Headroom)
	}
	// The unbounded form: no ceiling, and the note says why.
	newBudgetFixture(t, "").NoPerRunCap(t)
	t.Chdir(t.TempDir())
	out, _, err = execute(t, "budget", "preview", "--repo", "acme/app", "--recipe", "standard", "--runs", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Bounded || doc.CeilingUSD != nil || doc.KnobsFrom != "defaults" {
		t.Fatalf("unbounded doc = %+v", doc)
	}
}

// TestBudgetPreviewRecipeLayers: --recipe resolves the three layers, as run
// does: the repository's .fugaro/recipes first.
func TestBudgetPreviewRecipeLayers(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	testutil.WriteFiles(t, ".", map[string]string{
		".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nsteps:\n  - review: {}\n"})
	out, _, err := execute(t, "budget", "preview", "--recipe", "mine", "--runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	// solo-shaped: implement + review rounds (1+2*2 = 5 stages).
	if !strings.Contains(out, "recipe mine (repo)") || !strings.Contains(out, "5 stages") {
		t.Fatalf("the repository's own recipe is not the one previewed:\n%s", out)
	}
}

// TestBudgetPreviewReadsOnly: the preview writes nothing at all -- not to one
// of the paths it reads, and not to any other: every request it makes is a
// read, and the whole database is the same tree afterwards. It runs the
// history path too (recorded runs), since that is the preview's other read.
func TestBudgetPreviewReadsOnly(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(5)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.SeedRuns(t, 0, 6, previewRunStages(0.60, 0.10, 0.20, 1.00))
	before, err := json.Marshal(f.db.Value(""))
	if err != nil {
		t.Fatal(err)
	}
	sent := len(f.db.Requests())
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "typical:") {
		t.Fatalf("the reads this test watches were not made:\n%s", out)
	}
	for _, r := range f.db.Requests()[sent:] {
		if r.Method != http.MethodGet {
			t.Errorf("the preview wrote to the database: %s %s", r.Method, r.Path)
		}
	}
	after, err := json.Marshal(f.db.Value(""))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the preview changed the database:\nbefore %s\nafter  %s", before, after)
	}
}

// TestBudgetPreviewRefusesBadArguments: the flags it needs, and the shapes it
// refuses. Each refusal says what is wrong, not only that something is.
func TestBudgetPreviewRefusesBadArguments(t *testing.T) {
	newBudgetFixture(t, "")
	t.Chdir(t.TempDir())
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		// cobra names every flag the command needs and the run did not set.
		"no recipe":      {[]string{"budget", "preview", "--runs", "2", "--repo", "acme/app"}, `required flag(s) "recipe" not set`},
		"no runs":        {[]string{"budget", "preview", "--recipe", "standard", "--repo", "acme/app"}, `required flag(s) "runs" not set`},
		"zero runs":      {[]string{"budget", "preview", "--recipe", "standard", "--runs", "0", "--repo", "acme/app"}, "--runs must be more than 0"},
		"negative runs":  {[]string{"budget", "preview", "--recipe", "standard", "--runs", "-1", "--repo", "acme/app"}, "--runs must be more than 0"},
		"too many runs":  {[]string{"budget", "preview", "--recipe", "standard", "--runs", "100001", "--repo", "acme/app"}, "is more than 100000 runs; estimate a batch, not a project's lifetime"},
		"unknown recipe": {[]string{"budget", "preview", "--recipe", "nope", "--runs", "2", "--repo", "acme/app"}, "recipe nope is not in .fugaro/recipes"},
		"bad repo":       {[]string{"budget", "preview", "--recipe", "standard", "--runs", "2", "--repo", "not-a-repo"}, "--repo: the repository must look like owner/name"},
	} {
		_, _, err := execute(t, tc.args...)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q never says %q", name, err, tc.want)
		}
	}
}

// TestBudgetPreviewEnvCapSupersedesTheConfig: $FUGARO_MAX_RUN_USD is the cap
// a job carries, so when it is set it is the one that counts -- not the
// smaller of it and the project config's budget.per_run_usd, which is what
// the runner would ignore.
func TestBudgetPreviewEnvCapSupersedesTheConfig(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	// The config says $5, the environment $2: the runner would use $2.
	t.Setenv(runner.MaxRunUSDEnv, "2")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"6 runs × $2.00 (per-run cap: FUGARO_MAX_RUN_USD)", "FUGARO_MAX_RUN_USD $2.00", "the database's per-run cap (default) $3.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "budget.per_run_usd") {
		t.Errorf("the config's cap is named although the environment supersedes it:\n%s", out)
	}
}

// TestBudgetPreviewRefusesABadEnvCap: the runner refuses a cap that is not a
// positive number or that rounds to nothing, so the preview must not price a
// run at it.
func TestBudgetPreviewRefusesABadEnvCap(t *testing.T) {
	for name, tc := range map[string]struct{ value, want string }{
		"not a number":      {"abc", "must be a number of US dollars"},
		"zero":              {"0", "more than 0"},
		"rounds to nothing": {"0.0000004", "it rounds to nothing"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBudgetFixture(t, "")
			f.Checkout(t, "  max_budget_usd: 0.5\n")
			t.Setenv(runner.MaxRunUSDEnv, tc.value)
			_, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "1")
			if err == nil {
				t.Fatalf("%s: accepted", tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q never says %q", err, tc.want)
			}
		})
	}
}

// TestBudgetPreviewProjectRecipe: the recipe is read from the project's
// published recipes in the runs bucket, the layer between the checkout and
// the catalog.
func TestBudgetPreviewProjectRecipe(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.DefaultRunsBucket(t)
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	testutil.WriteFiles(t, f.runsDir(), map[string]string{
		"fugaro/recipes/proj.yaml": "version: 1\nname: proj\nsteps:\n  - review:\n      max_rounds: 1\n"})
	out, _, err := execute(t, "budget", "preview", "--recipe", "proj", "--runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	// One senior review round with its fix: 1 + 2 = 3 stages.
	if !strings.Contains(out, "recipe proj (project)") || !strings.Contains(out, "3 stages") {
		t.Fatalf("the project's recipe is not the one previewed:\n%s", out)
	}
}

// TestBudgetPreviewRepoRecipeSkipsTheBucket: a recipe the checkout holds is
// resolved without the runs bucket, whose read is not every Viewer's to do
// (init takes it away), so the preview still answers with the project marker
// gone -- where a recipe the checkout does not hold needs the project's own
// copy there and fails. The typical line's read of that same bucket is the
// one thing the failure costs: it is a note, and the ceiling stands without
// it.
func TestBudgetPreviewRepoRecipeSkipsTheBucket(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.DefaultRunsBucket(t)
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.DropMarker(t)
	testutil.WriteFiles(t, ".", map[string]string{
		".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nsteps:\n  - review: {}\n"})
	out, errOut, err := execute(t, "budget", "preview", "--recipe", "mine", "--runs", "1")
	if err != nil {
		t.Fatalf("the recipe the checkout holds was looked for in the runs bucket: %v", err)
	}
	if !strings.Contains(out, "recipe mine (repo)") {
		t.Fatalf("the repository's own recipe is not the one previewed:\n%s", out)
	}
	// The ceiling is stated all the same, and the bucket the typical line
	// reads is named as the one thing that was lost.
	if !strings.Contains(out, "rough worst case") {
		t.Errorf("the ceiling is gone with the bucket:\n%s", out)
	}
	if !strings.Contains(errOut, "no typical line: ") || !strings.Contains(errOut, "no project name yet") {
		t.Errorf("the typical line's own open failure is not said:\n%s", errOut)
	}
}

// TestBudgetPreviewGlobalCapsOnly: an installation with only the global caps
// has no repository headroom at all, and the global per-run cap alone bounds
// every run that has no per-run cap of its own (D6), so the smaller of it and
// the config's cap is the ceiling.
func TestBudgetPreviewGlobalCapsOnly(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetGlobalCapsOnly(10, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"rough worst case: $3.00",
		"1 runs × $3.00 (per-run cap: global)",
		"per-run caps found: budget.per_run_usd $5.00, the database's per-run cap (global) $3.00; the smallest counts",
		"headroom left today: repository not set, project $10.00",
		"fits the project's daily cap ($10.00 left today)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
}

// TestBudgetPreviewRepoCapIsNamed: with a per-run cap on the repository's own
// node, the preview names that scope -- "repo" -- rather than the default or
// the global fallback, so Check 48 step 5 can tell the three apart from the
// output alone (plan Task 0.3 step 7).
func TestBudgetPreviewRepoCapIsNamed(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app", "perRunMicros": usd(2)})
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1 runs × $2.00 (per-run cap: repo)",
		"the database's per-run cap (repo) $2.00",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
}

// TestBudgetPreviewStageShapes: the stage count follows the recipe's own
// knobs, not only the catalog's two.
func TestBudgetPreviewStageShapes(t *testing.T) {
	for name, tc := range map[string]struct{ recipe, yaml, want string }{
		"mode review": {recipe: "pick", want: "at most 1 stages"},
		"max_rounds": {recipe: "shapes", want: "at most 13 stages",
			yaml: "version: 1\nname: shapes\nsteps:\n  - first_line: {}\n  - review:\n      max_rounds: 5\n"},
		"bounce": {recipe: "shapes", want: "at most 7 stages",
			yaml: "version: 1\nname: shapes\nsteps:\n  - first_line: {}\n  - review:\n      bounce: first_line\n      max_rounds: 1\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBudgetFixture(t, "")
			f.SetCaps(100, 3, 20, 3)
			f.SetSpentToday(0)
			f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
			if tc.yaml != "" {
				testutil.WriteFiles(t, ".", map[string]string{".fugaro/recipes/shapes.yaml": tc.yaml})
			}
			out, _, err := execute(t, "budget", "preview", "--recipe", tc.recipe, "--runs", "1")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("preview never says %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestBudgetPreviewUsesTheProjectsRigorMode: a repository that sets no round
// knob is previewed at the mode's own row, which is what a run of it spends
// (the owner's ruling of 2026-10-31, rigor.DefaultRounds) — the project
// layer's default where it sets one, the repository's own rigor default where
// it does not, and pro where neither does. Before the ruling the preview
// counted every repository at one flat 1/2; the stage figures below are the
// same standard recipe at each row, so a reader sees both the numbers and the
// mode they came from.
//
// One case has no checkout at all (--repo alone, as fugaro run --repo does):
// there the project layer is still read — it is the installation's own, which
// is the layer a run without a checkout resolves (embedProjectLayer) — so the
// mode, and the stage count that follows from it, is the run's.
func TestBudgetPreviewUsesTheProjectsRigorMode(t *testing.T) {
	// standard is a first_line loop under a bouncing review: the bound moves
	// with both knobs at once.
	const standard = "at most %d stages (mode %s, agent.first_line_rounds %d, agent.review_rounds %d)"
	for _, tc := range []struct {
		name       string
		layer      string
		repoRigor  string
		noCheckout bool
		mode       string
		first      int
		rounds     int
		stages     int
	}{
		{name: "no layer and no repository value: pro", mode: "pro", first: 3, rounds: 3, stages: 31},
		{name: "the repository's own rigor default", repoRigor: "rigor: { default: poc }\n", mode: "poc", first: 1, rounds: 1, stages: 7},
		{name: "the project layer's default", layer: "critical", mode: "critical", first: 3, rounds: 5, stages: 47},
		{name: "no checkout: the project layer's default", layer: "startup", noCheckout: true, mode: "startup", first: 2, rounds: 2, stages: 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBudgetFixture(t, "")
			f.SetCaps(100, 3, 20, 3)
			f.SetSpentToday(0)
			if tc.layer != "" {
				f.DefaultRunsBucket(t)
				testutil.WriteFiles(t, f.runsDir(), map[string]string{config.LayerKey: previewLayer(tc.layer)})
			}
			args := []string{"budget", "preview", "--recipe", "standard", "--runs", "1"}
			agent := "  max_budget_usd: 0.5\n"
			if tc.repoRigor != "" {
				agent += tc.repoRigor
			}
			if tc.noCheckout {
				// No checkout of acme/app anywhere: the file that would
				// anchor the layer is not here, so the layer the project
				// published under its own name is the one that applies.
				t.Chdir(t.TempDir())
				args = append(args, "--repo", "acme/app")
			} else {
				f.Checkout(t, agent)
				if tc.layer != "" {
					// A layer is read for a repository whose file is anchored:
					// the project's own name and GCP project, which Checkout's
					// fugaro.yaml does not carry (minimalAnchored's shape).
					testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": previewCheckoutYAML(agent)})
				}
			}
			out, _, err := execute(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(standard, tc.stages, tc.mode, tc.first, tc.rounds)
			if !strings.Contains(out, want) {
				t.Fatalf("preview never says %q:\n%s", want, out)
			}
		})
	}
}

// previewCheckoutYAML is Checkout's fugaro.yaml with the project anchor a
// layer is resolved against (minimalAnchored): without project and
// gcp_project an unanchored file reads as "no layer applies".
func previewCheckoutYAML(agentYAML string) string {
	return "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github, base_branch: main }\n" +
		"agent:\n  auth: api-key\n" + agentYAML +
		"workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"
}

// previewLayer is the project layer these preview cases read a rigor default
// from: testProjectLayer's shape with defaults.rigor.default set to mode, at
// the project and GCP project the budget fixture names.
func previewLayer(mode string) string {
	return "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n" +
		"  git: { provider: github }\n  agent: { auth: api-key }\n  rigor: { default: " + mode + " }\n" +
		"profiles:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh }\ndefault_profile: svc\n"
}

// TestBudgetPreviewRecipeNote: resolveRecipe's note explains figures that did
// not come from the layer they should have, so the preview prints it, as run
// does, and carries it in --json.
func TestBudgetPreviewRecipeNote(t *testing.T) {
	f := newBudgetFixture(t, "") // runs_bucket: unused-bucket, the project layer is skipped
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	const want = "project recipes need the default runs bucket name"
	if _, errOut, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "1"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(errOut, want) {
		t.Errorf("the skipped project layer is not explained: %q", errOut)
	}
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(doc.Notes) == 0 || !strings.Contains(strings.Join(doc.Notes, " "), want) {
		t.Errorf("notes = %q", doc.Notes)
	}
}

// checkoutWorkflows makes the working directory a checkout of acme/app whose
// fugaro.yaml declares the given workflows block, as Checkout does with its
// own one: the compute ceiling is the workflow's own machine and clock, so a
// case that needs two of them writes its own file.
func checkoutWorkflows(t *testing.T, agentYAML, workflows string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
		"agent:\n  auth: api-key\n" + agentYAML + "workflows:\n" + workflows
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// TestBudgetPreviewPrintsTheComputeCeiling: the ceiling's second half is the
// machine, not the model. An artifact run's render is a runner command with
// its own timeout, so it is outside every model stage and every model cap
// (design artifact-runs.md §6, D4), and the preview says what a run of the
// workflow can bill for the resources it asked for over timeouts.total.
func TestBudgetPreviewPrintsTheComputeCeiling(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.Checkout(t, "  max_budget_usd: 0.5\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// web is the checkout's one workflow, at the defaults: 4 vCPU, 8Gi and
	// timeouts.total 90m. The fixture's region is us-east5, Cloud Run's tier 1
	// ($0.000018 a vCPU-second, $0.000002 a GiB-second), so the ceiling is
	// 5400 s × (4×0.000018 + 8×0.000002) = $0.4752.
	const want = "compute ceiling (workflow web): 4 vCPU x 8 GiB x 1h30m0s = $0.48 at this installation's compute prices — NOT CAPPED"
	if !strings.Contains(out, want) {
		t.Errorf("preview never says %q:\n%s", want, out)
	}
}

// TestBudgetPreviewSaysTheCeilingIsNotCapped: the line says out loud that no
// cap covers compute (D4), and the model ceiling above it does not move -- the
// compute figure is never folded into the rough worst case, which stays model
// dollars.
func TestBudgetPreviewSaysTheCeilingIsNotCapped(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.NoPerRunCap(t)
	f.SetCaps(100, 50, 100, 50)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"— NOT CAPPED", "bounded by timeouts.total, not by any budget cap"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
	// The ruling is stated and not cited: SECURITY.md carries no such
	// paragraph until the plan's Task 9 writes one, and a pointer to a
	// document that does not make the claim reads to the reader as a
	// document that says the opposite. The pointer returns with the
	// paragraph, and this line fails until both are true together.
	if strings.Contains(out, "SECURITY.md") {
		t.Errorf("the line cites a document that does not carry the ruling:\n%s", out)
	}
	// The line the CLI prints is documented where its other lines are: the
	// help names the compute ceiling and the NOT CAPPED that makes it the
	// one figure sitting outside the model ceiling above it.
	for _, want := range []string{"compute ceiling", "NOT CAPPED", "timeouts.total"} {
		if !strings.Contains(newBudgetPreviewCmd().Long, want) {
			t.Errorf("budget preview --help never says %q", want)
		}
	}
	// 11 stages × $0.50 = $5.50 a run, $33.00 for six, and the compute ceiling
	// is not added to it.
	if !strings.Contains(out, "rough worst case: $33.00") {
		t.Errorf("the model ceiling moved:\n%s", out)
	}
}

// TestBudgetPreviewComputeCeilingFollowsTheWorkflowsResources: the figure is
// the workflow's own requested machine over its own timeouts.total, so a
// heavier workflow costs more and the preview prints one line for each.
func TestBudgetPreviewComputeCeilingFollowsTheWorkflowsResources(t *testing.T) {
	newBudgetFixture(t, "")
	checkoutWorkflows(t, "  max_budget_usd: 0.5\n",
		"  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"+
			"  render: { commands: { build: sh build.sh, test: sh test.sh }, resources: { cpu: 8, memory: 32Gi }, timeouts: { total: 2h } }\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// web: 5400 s × (4×0.000018 + 8×0.000002) = $0.4752.
	// render: 7200 s × (8×0.000018 + 32×0.000002) = $1.4976.
	for _, want := range []string{
		"compute ceiling (workflow web): 4 vCPU x 8 GiB x 1h30m0s = $0.48",
		"compute ceiling (workflow render): 8 vCPU x 32 GiB x 2h0m0s = $1.50",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
}

// TestBudgetPreviewWithoutPricesNamesTheSourceInsteadOfGuessing: a price book
// with no table for the region (the zero Prices; cloudEnv.prices answers that
// way for an execution no table prices) leaves the ceiling without a dollar
// figure and names what is missing, rather than printing $0.00 as though
// compute were free.
func TestBudgetPreviewWithoutPricesNamesTheSourceInsteadOfGuessing(t *testing.T) {
	cfg := &config.Config{Workflows: map[string]config.Workflow{
		"web": {Resources: config.Resources{CPU: 8, Memory: "32Gi"},
			Timeouts: config.Timeouts{Total: config.Duration{Duration: 2 * time.Hour}}},
	}}
	got := previewComputeCeilings(cfg, backend.Prices{}, "us-east5")
	if len(got) != 1 {
		t.Fatalf("ceilings = %+v", got)
	}
	c := got[0]
	if c.USD != nil || !strings.Contains(c.Unpriced, "us-east5") {
		t.Fatalf("a ceiling with no price table = %+v", c)
	}
	var buf bytes.Buffer
	printComputeCeiling(&buf, c)
	line := buf.String()
	if strings.Contains(line, "$") {
		t.Errorf("a dollar figure with no price table:\n%s", line)
	}
	if !strings.Contains(line, "8 vCPU x 32 GiB x 2h0m0s") || !strings.Contains(line, "NOT CAPPED") {
		t.Errorf("the unpriced ceiling does not say what it could not price:\n%s", line)
	}
}

// TestBudgetPreviewArtifactRecipeDoesNotInflateTheModelCeiling: a mode:
// artifact recipe has no implement loop and no review loop, so it can start
// one model stage (the plan) and not 1 + 2*rounds. Without the artifact arm
// in StageBound, an artifact run's model ceiling is five stages at this
// repository's defaults while its render -- the expensive half -- is the
// compute ceiling beside it (design §6, D4).
func TestBudgetPreviewArtifactRecipeDoesNotInflateTheModelCeiling(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	testutil.WriteFiles(t, ".", map[string]string{
		".fugaro/recipes/sizzle.yaml": "version: 1\nname: sizzle\nmode: artifact\n" +
			"steps:\n  - plan: {}\n  - render: {}\noutputs: { globs: [out/*.mp4] }\n"})
	out, _, err := execute(t, "budget", "preview", "--recipe", "sizzle", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "at most 1 stages") {
		t.Errorf("an artifact recipe is not one model stage:\n%s", out)
	}
	if strings.Contains(out, "at most 5 stages") {
		t.Errorf("the artifact recipe took the implement arithmetic:\n%s", out)
	}
	// One stage × the $0.50 per-stage cap is what a run is bounded by, and
	// six of them are the ceiling: the render is not a fifth of it.
	if !strings.Contains(out, "6 runs × $0.50 (1 stages × $0.50 (agent.max_budget_usd))") {
		t.Errorf("one stage × $0.50 is not the model ceiling:\n%s", out)
	}
	// The render is priced as compute, beside it, and not as a model stage.
	if !strings.Contains(out, "compute ceiling (workflow web): 4 vCPU x 8 GiB x 1h30m0s = $0.48") {
		t.Errorf("the artifact run's compute ceiling is missing:\n%s", out)
	}
}

// TestBudgetPreviewComputeCeilingJSON: the ceiling the text prints rides in
// --json too, priced and unpriced alike, and a preview with no checkout to
// take workflows from carries an empty list and not a null -- a consumer that
// ranges over the field and one that checks it for absence read those two
// differently.
func TestBudgetPreviewComputeCeilingJSON(t *testing.T) {
	type ceiling struct {
		Workflow string   `json:"workflow"`
		VCPU     int      `json:"vcpu"`
		Memory   string   `json:"memory"`
		GiB      float64  `json:"memory_gib"`
		TotalS   float64  `json:"total_s"`
		USD      *float64 `json:"usd"`
		Source   string   `json:"price_source"`
		Unpriced string   `json:"unpriced"`
	}
	var doc struct {
		ComputeCeiling []ceiling `json:"compute_ceiling"`
	}

	// Priced: web at the checkout's defaults, 5400 s × (4×0.000018 +
	// 8×0.000002) = $0.4752 at the fixture's region (us-east5, tier 1).
	f := newBudgetFixture(t, "")
	f.Checkout(t, "  max_budget_usd: 0.5\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(doc.ComputeCeiling) != 1 {
		t.Fatalf("compute_ceiling = %+v", doc.ComputeCeiling)
	}
	c := doc.ComputeCeiling[0]
	if c.Workflow != "web" || c.VCPU != 4 || c.Memory != "8Gi" || c.GiB != 8 || c.TotalS != 5400 {
		t.Errorf("ceiling = %+v", c)
	}
	if c.USD == nil || *c.USD < 0.4752-1e-9 || *c.USD > 0.4752+1e-9 {
		t.Errorf("usd = %v, want 0.4752", c.USD)
	}
	if c.Source != "Cloud Run jobs list price, tier 1 (2026-09)" || c.Unpriced != "" {
		t.Errorf("source = %q, unpriced = %q", c.Source, c.Unpriced)
	}

	// Unpriced: usd is null -- present and not omitted -- and unpriced says
	// why, with no price_source beside it. This shape cannot come through the
	// command (config validation refuses a resources.memory that is not a
	// memory size, the defaults fill timeouts.total, and every region has a
	// list price), so it is pinned on the document the command encodes, which
	// is what a consumer reads either way.
	raw, err := json.Marshal(previewDoc{ComputeCeiling: previewComputeCeilings(
		&config.Config{Workflows: map[string]config.Workflow{
			"web": {Resources: config.Resources{CPU: 8, Memory: "32Gi"},
				Timeouts: config.Timeouts{Total: config.Duration{Duration: 2 * time.Hour}}},
		}}, backend.Prices{}, "us-east5")})
	if err != nil {
		t.Fatal(err)
	}
	doc.ComputeCeiling = nil
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if len(doc.ComputeCeiling) != 1 {
		t.Fatalf("compute_ceiling = %+v", doc.ComputeCeiling)
	}
	u := doc.ComputeCeiling[0]
	if u.USD != nil || !strings.Contains(u.Unpriced, "us-east5") || u.Source != "" {
		t.Errorf("an unpriced ceiling = %+v", u)
	}
	if !strings.Contains(string(raw), `"usd":null`) {
		t.Errorf("usd is omitted rather than null:\n%s", raw)
	}

	// No checkout: an empty list, not a null.
	newBudgetFixture(t, "")
	t.Chdir(t.TempDir())
	out, _, err = execute(t, "budget", "preview", "--repo", "acme/app", "--recipe", "standard", "--runs", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	// The spaces are the encoder's indentation, not the shape: what matters
	// is that the list is there and empty, since a nil slice would encode as
	// null and read to a consumer as "this build does not have the field".
	if !strings.Contains(strings.ReplaceAll(out, " ", ""), `"compute_ceiling":[]`) {
		t.Errorf("a preview with no checkout does not carry an empty ceiling list:\n%s", out)
	}
}
