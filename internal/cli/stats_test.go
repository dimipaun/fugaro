package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro stats runs (website plan task W0.3). The command's whole value is
// what it does not print, so these tests hold three properties: the output is
// exactly the aggregates and no identifier can reach a field of it
// (TestStatsRunsJSONCarriesNoIdentifiers is the load-bearing one), a thin
// group is suppressed rather than approximated, and an installation must be
// named -- never guessed -- before anything is read.
//
// No fixture here names an installation other than the synthetic one the
// cloud fixture builds (reconciliation Global Constraint 10).

// statsIdentifierShapes are the shapes a string value in the output may never
// have: a repository slug, a run ID, a branch or path (anything with a slash),
// a person (anything with an at-sign) and a URL. None of the fields the
// command publishes -- a recipe name, a date, an installation's own name, the
// disclaimer -- can legitimately carry one.
var statsIdentifierShapes = map[string]*regexp.Regexp{
	"a repository slug":  regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`),
	"a run ID":           regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`),
	"a commit":           regexp.MustCompile(`^[0-9a-f]{40}$`),
	"a branch or a path": regexp.MustCompile(`/`),
	"a person":           regexp.MustCompile(`@`),
	"a URL":              regexp.MustCompile(`://`),
}

// statsSeed is one run's record, as seedStatsRuns writes it.
type statsSeed struct {
	recipe       string
	duration     time.Duration
	cost         float64
	notEstimated bool
	// noFigure seeds a record the aggregation can draw nothing from.
	noFigure bool
}

// seedStatsRuns writes n records of one recipe into the fixture's runs bucket.
// Every record carries the identifiers a real one carries -- the repository,
// the run ID, the branch, the pull request, the commit and the oauth account
// -- and a task.json beside it carrying the task's own text and the person who
// asked for it, so the tests prove that neither reaches the output: the
// command reads result.json and nothing else.
func seedStatsRuns(t *testing.T, f *cloudFixture, seeds []statsSeed, start time.Time) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for i, s := range seeds {
		id := fmt.Sprintf("%s-1200%02d-abcd", start.Format("20060102"), i)
		st := runstore.Open(b, appSlug, id)
		if err := st.CreateTask(ctx, &task.Spec{
			Version: 1, RunID: id, Repo: statsFixtureRepo, Ref: "main", Workflow: "web",
			Task: statsFixtureTask, RequestedBy: statsFixturePerson,
		}); err != nil {
			t.Fatal(err)
		}
		rec := runstore.Record{
			Version: 1, RunID: id, Repo: statsFixtureRepo, Workflow: "web",
			OAuthAccount: statsFixturePerson, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
			Branch: statsFixtureBranch, PushedHead: statsFixtureCommit,
			PR:        &runstore.PRRef{Number: 7, URL: statsFixturePRURL},
			Recipe:    &runstore.RecipeRecord{Name: s.recipe, Source: "catalog", SHA256: strings.Repeat("ab", 32)},
			StartedAt: start.Add(-2 * time.Hour),
			Cost: &runstore.Cost{
				TotalUSD: s.cost, ComputeEstimated: !s.notEstimated,
				ModelUSD: 0, Estimate: true, ModelBasis: runstore.BasisAPIList,
			},
		}
		if !s.noFigure {
			done := rec.StartedAt.Add(s.duration)
			rec.FinishedAt = &done
		}
		if err := st.WriteRecord(ctx, &rec); err != nil {
			t.Fatal(err)
		}
	}
}

// The identifiers every fixture record carries, so the test can prove none of
// them reaches the output. Synthetic, and named nowhere else.
const (
	statsFixtureRepo   = "acme/example"
	statsFixtureBranch = "fugaro/stats-fixture-abcd"
	statsFixturePRURL  = "https://github.com/acme/example/pull/7"
	statsFixturePerson = "someone@example.com"
	statsFixtureTask   = "rewrite the billing pipeline for the acme rollout"
	statsFixtureCommit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
)

// statsFixtureIDs are the strings of the fixtures above that the output must
// never carry: the fixture's own repository slug and the bucket directory it
// names, plus everything else one record knows about itself.
func statsFixtureIDs() []string {
	return []string{
		statsFixtureRepo, statsFixtureBranch, statsFixturePRURL,
		statsFixturePerson, statsFixtureTask, statsFixtureCommit,
		appSlug,
	}
}

// TestStatsRunsJSONCarriesNoIdentifiers is the load-bearing test of the task
// (reconciliation D96, plan P2): the aggregation's output carries no
// repository slug, run ID, branch, pull request, task text, person or commit,
// over a fixture whose records deliberately carry every one of them -- so a
// future field that leaks one fails here.
func TestStatsRunsJSONCarriesNoIdentifiers(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	var seeds []statsSeed
	for i := range 21 {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i*60) * time.Second, cost: 1 + float64(i)/10})
	}
	seedStatsRuns(t, f, seeds, time.Now().UTC())

	out, _, err := execute(t, "stats", "runs", "--project", "aurora", "--json")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	groups, _ := doc["recipes"].([]any)
	if len(groups) != 1 {
		t.Fatalf("fixture published %d groups, want 1: %s", len(groups), out)
	}
	for path, val := range statsStrings(t, doc) {
		for _, id := range statsFixtureIDs() {
			if strings.Contains(val, id) {
				t.Errorf("stats output carries identifier %q in %s", id, path)
			}
		}
		for shape, re := range statsIdentifierShapes {
			if re.MatchString(val) {
				t.Errorf("stats output value %q in %s is %s", val, path, shape)
			}
		}
	}
	if strings.Contains(out, statsFixtureTask) || strings.Contains(out, statsFixturePerson) {
		t.Errorf("stats output carries the task's own text or its requester: %s", out)
	}
}

// statsStrings walks a decoded JSON document and returns every string value
// with the path it sits at.
func statsStrings(t *testing.T, v any) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, sub := range x {
				walk(path+"."+k, sub)
			}
		case []any:
			for i, sub := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), sub)
			}
		case string:
			out[path] = x
		}
	}
	walk("$", v)
	return out
}

// TestStatsRunsOutputShapeIsExactlyTheAggregates: the document is the
// aggregates and nothing else -- the exact key set at every level, the
// published figures for a known fixture, and no key anywhere that is a mean.
func TestStatsRunsOutputShapeIsExactlyTheAggregates(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	// 20 runs: durations 100..2000s in steps of 100, costs $0.10..$2.00 in
	// steps of $0.10, and three whose compute was not estimated.
	var seeds []statsSeed
	for i := 1; i <= 20; i++ {
		seeds = append(seeds, statsSeed{
			recipe: "standard", duration: time.Duration(i*100) * time.Second,
			cost: float64(i) * 0.1, notEstimated: i <= 3,
		})
	}
	seedStatsRuns(t, f, seeds, time.Now().UTC())

	out, _, err := execute(t, "stats", "runs", "--project", "aurora", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "$", doc, "min_runs", "not_a_promise", "project", "recipes", "since", "totals", "until")
	recipes, ok := doc["recipes"].([]any)
	if !ok || len(recipes) != 1 {
		t.Fatalf("recipes = %v, want one group", doc["recipes"])
	}
	group, _ := recipes[0].(map[string]any)
	assertKeys(t, "$.recipes[0]", group, "compute_not_estimated", "cost_usd", "duration_s", "recipe", "runs")
	assertKeys(t, "$.recipes[0].duration_s", group["duration_s"], "median", "min", "p90")
	assertKeys(t, "$.recipes[0].cost_usd", group["cost_usd"], "median", "min", "p90")
	totals, _ := doc["totals"].(map[string]any)
	assertKeys(t, "$.totals", totals, "recipes", "runs", "runs_below_min_runs", "runs_not_estimated", "runs_unknown_recipe", "runs_without_a_figure")

	if group["recipe"] != "standard" || group["runs"] != float64(20) || group["compute_not_estimated"] != float64(3) {
		t.Errorf("group = %v", group)
	}
	for what, want := range map[string]map[string]float64{
		"duration_s": {"min": 100, "median": 1050, "p90": 1810},
		"cost_usd":   {"min": 0.10, "median": 1.05, "p90": 1.81},
	} {
		got, _ := group[what].(map[string]any)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s.%s = %v, want %v", what, k, got[k], v)
			}
		}
	}
	for k, want := range map[string]float64{
		"runs": 20, "recipes": 1, "runs_below_min_runs": 0,
		"runs_unknown_recipe": 0, "runs_not_estimated": 3, "runs_without_a_figure": 0,
	} {
		if totals[k] != want {
			t.Errorf("totals.%s = %v, want %v", k, totals[k], want)
		}
	}
	if doc["min_runs"] != float64(20) || doc["not_a_promise"] == "" || doc["project"] != "aurora" {
		t.Errorf("document = %v", doc)
	}
	// No field of the aggregate is a mean, at any level.
	for path := range statsKeys(t, doc) {
		last := path[strings.LastIndex(path, ".")+1:]
		if strings.Contains(last, "mean") || strings.Contains(last, "avg") || strings.Contains(last, "average") {
			t.Errorf("%s is a mean: a range is published, never an average", path)
		}
	}
	// The human output carries the same figures and the disclaimer.
	table, _, err := execute(t, "stats", "runs", "--project", "aurora")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"standard", "100 1050 1810", "0.10 1.05 1.81", "at least 20 runs per recipe", "not a promise", "observe", "enforce", "20 runs with a figure"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table lacks %q:\n%s", want, table)
		}
	}
}

// statsKeys walks a decoded JSON document and returns every key with its path.
func statsKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, sub := range x {
				out[path+"."+k] = true
				walk(path+"."+k, sub)
			}
		case []any:
			for i, sub := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), sub)
			}
		}
	}
	walk("$", v)
	return out
}

// assertKeys fails unless v is an object whose keys are exactly want.
func assertKeys(t *testing.T, path string, v any, want ...string) {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want an object", path, v)
	}
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s keys = %v, want exactly %v", path, got, want)
	}
}

// TestStatsRunsSuppressesSmallSamples: a recipe below the threshold is
// suppressed -- absent rather than approximated -- and --min-runs lowers the
// threshold deliberately.
func TestStatsRunsSuppressesSmallSamples(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	var seeds []statsSeed
	for i := range 4 {
		seeds = append(seeds, statsSeed{recipe: "premium", duration: time.Duration(600+i) * time.Second, cost: 1})
	}
	seedStatsRuns(t, f, seeds, time.Now().UTC())

	out, _, err := execute(t, "stats", "runs", "--project", "aurora", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	recipes, _ := doc["recipes"].([]any)
	totals, _ := doc["totals"].(map[string]any)
	if len(recipes) != 0 {
		t.Errorf("a 4-run recipe was published: %v", recipes)
	}
	if totals["runs_below_min_runs"] != float64(4) || totals["runs"] != float64(4) {
		t.Errorf("totals = %v, want the 4 runs counted and no range", totals)
	}
	if strings.Contains(out, "premium") {
		t.Errorf("the suppressed recipe is named in the output: %s", out)
	}
	table, _, err := execute(t, "stats", "runs", "--project", "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table, "no recipe reached 20 runs") {
		t.Errorf("the table does not say the window is too thin:\n%s", table)
	}

	out, _, err = execute(t, "stats", "runs", "--project", "aurora", "--json", "--min-runs", "4")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	recipes, _ = doc["recipes"].([]any)
	if len(recipes) != 1 {
		t.Fatalf("--min-runs 4 published %d groups, want 1", len(recipes))
	}
	if group, _ := recipes[0].(map[string]any); group["recipe"] != "premium" || group["runs"] != float64(4) {
		t.Errorf("group = %v", recipes[0])
	}
}

// TestStatsRunsRequiresOneNamedProject: the command refuses until exactly one
// installation is named. --project has no default, the environment is not one
// either, an empty name is refused, and there is no --all.
func TestStatsRunsRequiresOneNamedProject(t *testing.T) {
	noAgentSession(t)
	newCloudFixture(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no --project at all", []string{"stats", "runs", "--json"}},
		{"an empty --project", []string{"stats", "runs", "--project", "", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := execute(t, tc.args...); err == nil || !strings.Contains(err.Error(), "project") {
				t.Fatalf("%v = %v, want a refusal naming --project", tc.args, err)
			} else if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit code = %d, want %d", got, ExitUserError)
			}
		})
	}
	// The environment is not a default either: only naming it counts.
	t.Setenv("FUGARO_PROJECT", "aurora")
	if _, _, err := execute(t, "stats", "runs", "--json"); err == nil {
		t.Fatal("$FUGARO_PROJECT selected an installation: the command must refuse")
	}
	t.Setenv("FUGARO_PROJECT", "")
	if _, _, err := execute(t, "stats", "runs", "--gcp-project", "proj-1234", "--json"); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("a gcp project alone selected an installation: %v", err)
	}
	// There is no --all: one installation per invocation, always.
	cmd, _, err := NewRootCmd().Find([]string{"stats", "runs"})
	if err != nil || cmd == nil || cmd.Name() != "runs" {
		t.Fatalf("fugaro stats runs: %v", err)
	}
	if f := cmd.Flags().Lookup("all"); f != nil {
		t.Error("fugaro stats runs has --all: the aggregation reads one named installation")
	}
}

// TestStatsRunsHelpNamesNoOtherInstallation: the command's own words, and its
// flags, name no installation, repository, person or URL -- one is reachable
// only through --project, which has no default. The test names no
// installation itself, by design (reconciliation Global Constraint 10).
func TestStatsRunsHelpNamesNoOtherInstallation(t *testing.T) {
	noAgentSession(t)
	cmd, _, err := NewRootCmd().Find([]string{"stats", "runs"})
	if err != nil || cmd == nil {
		t.Fatal(err)
	}
	p := cmd.Flags().Lookup("project")
	if p == nil {
		t.Fatal("fugaro stats runs has no --project")
	}
	if p.DefValue != "" {
		t.Errorf("--project has a default (%q): an installation must be named, never guessed", p.DefValue)
	}
	if _, ok := p.Annotations[cobra.BashCompOneRequiredFlag]; !ok {
		t.Error("--project is not a required flag")
	}
	if !strings.Contains(p.Usage, "required") {
		t.Errorf("--project's help does not say it is required: %q", p.Usage)
	}
	// The local flags are the shared cloud flags and this command's own: no
	// other flag can carry a target.
	allowed := []string{"config", "gcp-project", "min-runs", "json", "project", "region", "since"}
	var local []string
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) { local = append(local, f.Name) })
	slices.Sort(local)
	slices.Sort(allowed)
	if !slices.Equal(local, allowed) {
		t.Errorf("fugaro stats runs' flags = %v, want exactly %v", local, allowed)
	}
	// The command's own text names no identifier shape; the shared cloud
	// flags' usage (--config's config paths among them) is every cloud
	// command's and is not this command's own text.
	own := []string{cmd.Use, cmd.Short, cmd.Long}
	for _, name := range []string{"project", "since", "min-runs", "json"} {
		own = append(own, cmd.Flags().Lookup(name).Usage)
	}
	for _, text := range own {
		for shape, re := range statsIdentifierShapes {
			if re.MatchString(strings.TrimSpace(text)) || re.MatchString(text) {
				t.Errorf("fugaro stats runs' own help text is %s: %q", shape, text)
			}
		}
	}
	out, _, err := execute(t, "stats", "runs", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "@") || strings.Contains(out, "://") {
		t.Errorf("the help names a person or a URL:\n%s", out)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("the help does not say --project is required:\n%s", out)
	}
}

// TestStatsRunsRefusedInAgentSession: the records are an installation's own
// data, so reading them is refused under the marker every owner-only path
// uses (plan W0.3 step 2).
func TestStatsRunsRefusedInAgentSession(t *testing.T) {
	newCloudFixture(t)
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			noAgentSession(t)
			t.Setenv(marker, "1")
			_, _, err := execute(t, "stats", "runs", "--project", "aurora")
			if err == nil || !strings.Contains(err.Error(), "coding agent") || !strings.Contains(err.Error(), "your own terminal window") {
				t.Fatalf("err = %v, want the coding-agent refusal", err)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit code = %d, want %d", got, ExitUserError)
			}
		})
	}
}
