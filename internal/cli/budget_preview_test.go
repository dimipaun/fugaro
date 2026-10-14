package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/pricing"
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
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("per_run_usd: 5, "), nil)
	if err := os.WriteFile(path, data, 0o600); err != nil {
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

// TestBudgetPreviewReadsOnly: the preview never writes to the database.
func TestBudgetPreviewReadsOnly(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.SetCaps(100, 3, 20, 3)
	f.SetSpentToday(5)
	f.Checkout(t, "  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	before := f.db.Value(budget.PathCapsGlobal)
	if _, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6"); err != nil {
		t.Fatal(err)
	}
	if got := f.db.Value(budget.PathCapsGlobal); got == nil || got.(map[string]any)["dailyMicros"].(json.Number).String() != before.(map[string]any)["dailyMicros"].(json.Number).String() {
		t.Fatalf("the preview wrote to the database: %v", got)
	}
}

// TestBudgetPreviewRefusesBadArguments: the flags it needs, and the shapes it
// refuses.
func TestBudgetPreviewRefusesBadArguments(t *testing.T) {
	newBudgetFixture(t, "")
	t.Chdir(t.TempDir())
	for name, args := range map[string][]string{
		"no recipe":      {"budget", "preview", "--runs", "2", "--repo", "acme/app"},
		"no runs":        {"budget", "preview", "--recipe", "standard", "--repo", "acme/app"},
		"zero runs":      {"budget", "preview", "--recipe", "standard", "--runs", "0", "--repo", "acme/app"},
		"negative runs":  {"budget", "preview", "--recipe", "standard", "--runs", "-1", "--repo", "acme/app"},
		"unknown recipe": {"budget", "preview", "--recipe", "nope", "--runs", "2", "--repo", "acme/app"},
		"bad repo":       {"budget", "preview", "--recipe", "standard", "--runs", "2", "--repo", "not-a-repo"},
	} {
		if _, _, err := execute(t, args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
