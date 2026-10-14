package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
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
}

// SeedRuns writes n runs of acme/app into the runs bucket, each with the
// same stages, as the runner records them (generic-tool Task 24's
// stages[].model_usd and stages[].model). The runs are minted one hour
// apart, the first firstHourAgo hours back, so a test can seed more than
// one batch. A stage with usd 0 writes no model_usd, as a record from
// before that key existed does.
func (f *budgetFixture) SeedRuns(t *testing.T, firstHourAgo, n int, stages []previewStage) {
	t.Helper()
	now := time.Now().UTC()
	for i := range n {
		id := now.Add(-time.Duration(firstHourAgo+i)*time.Hour).Format("20060102-150405") + "-aaaa"
		var ss []map[string]any
		for _, s := range stages {
			st := map[string]any{"name": s.kind, "started_at": now.UTC().Format(time.RFC3339), "duration_s": 1.0}
			if s.usd > 0 {
				st["model_usd"] = s.usd
			}
			if s.model != "" {
				st["model"] = s.model
			}
			ss = append(ss, st)
		}
		data, err := json.Marshal(map[string]any{
			"version": 1, "run_id": id, "repo": "acme/app", "status": "succeeded", "stages": ss})
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
		"standard": {recipe: "standard", want: "typical: $2.40 a run (7 stages, mean of 6 runs in the last 30 days)"},
		// solo has no first_line step: implement 1, review 1, fix 1 = 3
		// stages; $0.60 + $1.00 + $0.20 = $1.80.
		"solo": {recipe: "solo", want: "typical: $1.80 a run (3 stages, mean of 6 runs in the last 30 days)"},
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
		return fmt.Sprintf("3 of the %d runs of acme/app in the last 30 days recorded no per-stage cost (result.json from before it was kept); they are not counted", runs)
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
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, mean of 5 runs in the last 30 days)") {
		t.Errorf("the typical line does not stop at the priced runs:\n%s", out)
	}
	if !strings.Contains(errOut, note(8)) {
		t.Errorf("the skipped records are no longer explained:\n%s", errOut)
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
	if !strings.Contains(out, "typical: $1.80 a run (3 stages, mean of 6 runs in the last 30 days)") {
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
	if !strings.Contains(out, "typical: $2.40 a run (7 stages, mean of 10 runs in the last 30 days)") {
		t.Errorf("the typical line is not the pinned models' costs:\n%s", out)
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
			USD     float64 `json:"usd"`
			Stages  int     `json:"stages"`
			Runs    int     `json:"runs"`
			Skipped int     `json:"skipped"`
		} `json:"typical"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Typical == nil || doc.Typical.USD != 2.40 || doc.Typical.Stages != 7 || doc.Typical.Runs != 5 || doc.Typical.Skipped != 2 {
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
	for _, want := range []string{"11 stages", "rough worst case: $18.00", "6 runs × $3.00 (per-run cap)", "may exceed the repository's daily cap ($15.00 left today)"} {
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
	for _, want := range []string{"6 runs × $2.00 (per-run cap)", "FUGARO_MAX_RUN_USD $2.00", "the database's per-run cap $3.00"} {
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

// TestBudgetPreviewRepoRecipeSkipsTheBucket: a recipe the checkout already
// holds is never looked for in the runs bucket, whose read is not every
// Viewer's to do. The marker is gone, so the bucket could not be opened.
func TestBudgetPreviewRepoRecipeSkipsTheBucket(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.DefaultRunsBucket(t)
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	f.DropMarker(t)
	testutil.WriteFiles(t, ".", map[string]string{
		".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nsteps:\n  - review: {}\n"})
	out, _, err := execute(t, "budget", "preview", "--recipe", "mine", "--runs", "1")
	if err != nil {
		t.Fatalf("the runs bucket was opened for a recipe the checkout holds: %v", err)
	}
	if !strings.Contains(out, "recipe mine (repo)") {
		t.Fatalf("the repository's own recipe is not the one previewed:\n%s", out)
	}
}

// TestBudgetPreviewProjectCapOnly: an installation with only the global caps
// has no repository headroom at all, and a global per-run cap alone bounds no
// run (the rules need both sides), so the config's cap is what is left.
func TestBudgetPreviewProjectCapOnly(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetGlobalCapsOnly(10, 3)
	f.SetSpentToday(0)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"rough worst case: $5.00",
		"per-run caps found: budget.per_run_usd $5.00; the smallest counts",
		"headroom left today: repository not set, project $10.00",
		"fits the project's daily cap ($10.00 left today)",
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
