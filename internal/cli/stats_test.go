package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
	recipe string
	// source is the recipe's own source: "catalog" (the default here) or the
	// "repo" a repository's recipe of its own has.
	source       string
	duration     time.Duration
	cost         float64
	notEstimated bool
	// noFigure seeds a record the aggregation can draw nothing from.
	noFigure bool
}

// seedStatsRuns writes one record per seed into the fixture's runs bucket.
// Every record carries the identifiers and the free text a real one carries --
// the repository, the run ID, the branch, the pull request, the commit, the
// oauth account, and the model and the path a halt's own reason and detail
// hold -- and a task.json beside it carrying the task's own text and the
// person who asked for it, so the tests prove that none of it reaches the
// output: the command reads result.json, and reads nothing out of it but a
// recipe, a duration and a cost.
func seedStatsRuns(t *testing.T, f *cloudFixture, seeds []statsSeed, start time.Time) {
	t.Helper()
	ctx := context.Background()
	b := statsBucket(t, f)
	for i, s := range seeds {
		id := fmt.Sprintf("%s-1200%02d-abcd", start.Format("20060102"), i)
		st := runstore.Open(b, appSlug, id)
		if err := st.CreateTask(ctx, &task.Spec{
			Version: 1, RunID: id, Repo: statsFixtureRepo, Ref: "main", Workflow: "web",
			Task: statsFixtureTask, RequestedBy: statsFixturePerson,
		}); err != nil {
			t.Fatal(err)
		}
		source := s.source
		if source == "" {
			source = "catalog"
		}
		rec := runstore.Record{
			Version: 1, RunID: id, Repo: statsFixtureRepo, Workflow: "web",
			OAuthAccount: statsFixturePerson, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
			Branch: statsFixtureBranch, PushedHead: statsFixtureCommit,
			PR:     &runstore.PRRef{Number: 7, URL: statsFixturePRURL},
			Halt:   &runstore.Halt{Reason: runstore.HaltReason(statsFixtureModel), Scope: "run", At: start, Detail: statsFixtureHaltDetail},
			Recipe: &runstore.RecipeRecord{Name: s.recipe, Source: source, SHA256: strings.Repeat("ab", 32)},
			// StartedAt is set below, once the duration is known.
			Cost: &runstore.Cost{
				TotalUSD: s.cost, ComputeEstimated: !s.notEstimated,
				ModelUSD: 0, Estimate: true, ModelBasis: runstore.BasisAPIList,
			},
		}
		rec.StartedAt = start.Add(-2 * time.Hour)
		if !s.noFigure {
			done := rec.StartedAt.Add(s.duration)
			rec.FinishedAt = &done
		}
		if err := st.WriteRecord(ctx, &rec); err != nil {
			t.Fatal(err)
		}
	}
}

// seedStatsUnreadable writes one more run into the bucket whose result.json
// cannot be read: a file the process has no permission for, which is a read
// failure that is neither a missing record nor a corrupt one, so it is the one
// the command must report without naming the run.
func seedStatsUnreadable(t *testing.T, f *cloudFixture, start time.Time, n int) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the file is readable despite mode 0 (running as root)")
	}
	ctx := context.Background()
	id := fmt.Sprintf("%s-1200%02d-abcd", start.Format("20060102"), n)
	st := runstore.Open(statsBucket(t, f), appSlug, id)
	if err := st.PutFile(ctx, "result.json", []byte(`{"version":1}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	// The fixture's runs bucket is <dir>/runs, and a run sits under runs/.
	path := filepath.Join(f.dir, "runs", "runs", appSlug, id, "result.json")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

// statsBucket opens the fixture's runs bucket directly, for the seeds that
// write an object the command must survive rather than a well-formed record.
func statsBucket(t *testing.T, f *cloudFixture) *blob.Bucket {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// seedStatsCorrupt writes one more run into the bucket whose result.json
// cannot be read: the object runs ls and budget preview count as unreadable
// rather than fail on.
func seedStatsCorrupt(t *testing.T, f *cloudFixture, start time.Time, n int) {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("%s-1200%02d-abcd", start.Format("20060102"), n)
	if err := runstore.Open(statsBucket(t, f), appSlug, id).
		PutFile(ctx, "result.json", []byte(`{"version":1,"run_id":`), "application/json"); err != nil {
		t.Fatal(err)
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
	// The free text a record can hold besides: the model a run was halted
	// over, the path a halt's detail names, the person a task was asked for
	// by, and a recipe of a repository's own.
	statsFixtureModel      = "acme-org/llm-internal-v9"
	statsFixtureHaltDetail = "/var/lib/fugaro/runs/20261017-120000-abcd/transcripts/agent-1.jsonl"
	statsFixtureName       = "Ada Example-Lovelace"
	statsFixtureCustom     = "acme-house-style"
)

// statsFixtureIDs are the strings of the fixtures above that the output must
// never carry: the fixture's own repository slug and the bucket directory it
// names, plus everything else one record knows about itself.
func statsFixtureIDs() []string {
	return []string{
		statsFixtureRepo, statsFixtureBranch, statsFixturePRURL,
		statsFixturePerson, statsFixtureTask, statsFixtureCommit,
		statsFixtureModel, statsFixtureHaltDetail, statsFixtureName,
		statsFixtureCustom, appSlug,
	}
}

// statsIdentifierREs are what an identifier looks like anywhere in a line of
// text -- unlike the anchored shapes above, these are for the table and for
// stderr, where a value sits inside a sentence.
var statsIdentifierREs = map[string]*regexp.Regexp{
	"a repository slug, branch or path": regexp.MustCompile(`[A-Za-z0-9._-]+/[A-Za-z0-9._-]+`),
	"a run ID":                          regexp.MustCompile(`\b[0-9]{8}-[0-9]{6}-[0-9a-f]{4}\b`),
	"a commit":                          regexp.MustCompile(`\b[0-9a-f]{40}\b`),
	"a person":                          regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+`),
	"a URL":                             regexp.MustCompile(`://`),
}

// assertNoIdentifiers fails if text carries any identifier of the fixture, or
// anything that looks like one. what names the output it is: the table the
// command printed, or what it wrote to stderr.
func assertNoIdentifiers(t *testing.T, what, text string) {
	t.Helper()
	for _, id := range statsFixtureIDs() {
		if strings.Contains(text, id) {
			t.Errorf("%s carries the identifier %q:\n%s", what, id, text)
		}
	}
	for shape, re := range statsIdentifierREs {
		if m := re.FindString(text); m != "" {
			t.Errorf("%s carries what looks like %s: %q\n%s", what, shape, m, text)
		}
	}
}

// TestStatsRunsJSONCarriesNoIdentifiers is the load-bearing test of the task
// (reconciliation D96, plan P2): the output carries no repository slug, run ID,
// branch, pull request, task text, person, commit, model or path, over a
// fixture whose records deliberately carry every one of them -- so a future
// field that leaks one fails here. The table and stderr are held to the same
// rule as --json: they are printed output too.
func TestStatsRunsJSONCarriesNoIdentifiers(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	var seeds []statsSeed
	for i := range 21 {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i*60) * time.Second, cost: 1 + float64(i)/10})
	}
	// A repository's recipe of its own, dropped by the aggregation: its name
	// must not appear either.
	seeds = append(seeds, statsSeed{recipe: statsFixtureCustom, source: "repo", duration: time.Minute, cost: 2})
	seedStatsRuns(t, f, seeds, time.Now().UTC())

	out, errOut, err := execute(t, "stats", "runs", "--project", "aurora", "--json")
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
	assertNoIdentifiers(t, "the JSON output", out)
	assertNoIdentifiers(t, "stderr", errOut)

	// The same fixture, without --json: the table is printed output too, and
	// so is anything the command warns with.
	table, tableErr, err := execute(t, "stats", "runs", "--project", "aurora")
	if err != nil {
		t.Fatal(err)
	}
	assertNoIdentifiers(t, "the table", table)
	assertNoIdentifiers(t, "stderr", tableErr)
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

// TestStatsRunsWindowIsTheOneWindowAskedFor: the window is a published fact,
// not an implementation detail -- the two dates, that a run minted before
// --since is not read at all, that --since 0 means the whole bucket, and that
// an unusable --since or --min-runs is refused rather than guessed at.
func TestStatsRunsWindowIsTheOneWindowAskedFor(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	now := time.Now().UTC()
	var seeds []statsSeed
	for i := range 21 {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i) * time.Second, cost: 1})
	}
	seedStatsRuns(t, f, seeds, now)
	// A second batch, older than every window below: it must not be read.
	seedStatsRuns(t, f, seeds, now.AddDate(0, 0, -200))

	doc := statsRunsJSON(t, f, "aurora")
	if doc["since"] == "" {
		t.Fatalf("--since 90d published no start date: %v", doc)
	}
	since := statsDate(t, "$.since", doc["since"])
	until := statsDate(t, "$.until", doc["until"])
	if got := until.Sub(since); got != 90*24*time.Hour {
		t.Errorf("the window is %v, want 90 days", got)
	}
	// until is the day the command ran -- not a day the fixture chose.
	if drift := now.Sub(until); drift < 0 || drift > 24*time.Hour {
		t.Errorf("$.until = %v, want the day of the run (%v)", doc["until"], now)
	}
	if runs := statsTotals(t, doc)["runs"]; runs != float64(21) {
		t.Errorf("totals.runs = %v, want the 21 runs in the window, not the 21 minted 200 days ago", runs)
	}

	// --since 0: no bound at all, which the table says in words.
	doc = statsRunsJSON(t, f, "aurora", "--since", "0")
	if doc["since"] != "" {
		t.Errorf("--since 0 published a start date: %v", doc["since"])
	}
	if runs := statsTotals(t, doc)["runs"]; runs != float64(42) {
		t.Errorf("--since 0 counted %v runs, want all 42", runs)
	}
	table, _, err := execute(t, "stats", "runs", "--project", "aurora", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table, "the whole bucket") {
		t.Errorf("--since 0 does not say the window is unbounded:\n%s", table)
	}

	// A window of the operator's own choosing is published as given.
	doc = statsRunsJSON(t, f, "aurora", "--since", "30d")
	if got := statsDate(t, "$.until", doc["until"]).Sub(statsDate(t, "$.since", doc["since"])); got != 30*24*time.Hour {
		t.Errorf("--since 30d published a %v window", got)
	}
	table, _, err = execute(t, "stats", "runs", "--project", "aurora", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table, "(30d)") {
		t.Errorf("the table does not print --since as it was written:\n%s", table)
	}

	// A window that cannot be meant, and a threshold that cannot be meant.
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"--since", "--since", []string{"stats", "runs", "--project", "aurora", "--since", "yesterday"}},
		{"--since", "--since", []string{"stats", "runs", "--project", "aurora", "--since", "-1d"}},
		{"--min-runs", "--min-runs", []string{"stats", "runs", "--project", "aurora", "--min-runs", "0"}},
	} {
		t.Run(tc.want+" "+tc.args[len(tc.args)-1], func(t *testing.T) {
			_, _, err := execute(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v = %v, want a refusal naming %s", tc.args, err, tc.want)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit code = %d, want %d", got, ExitUserError)
			}
		})
	}
}

// statsRunsJSON runs the command over the fixture and decodes its document.
func statsRunsJSON(t *testing.T, f *cloudFixture, project string, extra ...string) map[string]any {
	t.Helper()
	args := append([]string{"stats", "runs", "--project", project, "--json"}, extra...)
	out, _, err := execute(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// statsDate reads one of the document's dates.
func statsDate(t *testing.T, path string, v any) time.Time {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s is %T, want a date", path, v)
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("%s = %q: %v", path, s, err)
	}
	return d
}

// statsTotals reads the document's totals object.
func statsTotals(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	totals, ok := doc["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals = %v, want an object", doc["totals"])
	}
	return totals
}

// TestStatsRunsSurvivesARecordItCannotRead: a corrupt or oversized result.json
// is one run's problem, not the window's -- runs ls and budget preview count
// such an object as unreadable and go on, and so do the figures: the group is
// still published and the record is counted as one with no figure, rather than
// the whole aggregation failing with a remote error.
func TestStatsRunsSurvivesARecordItCannotRead(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	now := time.Now().UTC()
	var seeds []statsSeed
	for i := range 21 {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i) * time.Second, cost: 1})
	}
	seedStatsRuns(t, f, seeds, now)
	seedStatsCorrupt(t, f, now, 40)

	doc := statsRunsJSON(t, f, "aurora")
	recipes, _ := doc["recipes"].([]any)
	if len(recipes) != 1 {
		t.Fatalf("recipes = %v, want the one group of 21 runs the corrupt record does not belong to", doc["recipes"])
	}
	group, _ := recipes[0].(map[string]any)
	if group["runs"] != float64(21) {
		t.Errorf("group runs = %v, want 21", group["runs"])
	}
	totals := statsTotals(t, doc)
	if totals["runs"] != float64(21) || totals["runs_without_a_figure"] != float64(1) {
		t.Errorf("totals = %v, want the corrupt record counted and not grouped", totals)
	}
}

// TestStatsRunsDropsARepoRecipeThatShadowsTheCatalog: a repository (or project)
// recipe may reuse a built-in name -- resolveRecipe lets it shadow the catalog
// one -- so grouping by the record's name alone would publish one repository's
// own loop under a built-in label, beside the catalog's runs and with its
// figures mixed in. Only a catalog recipe is grouped; 20 runs of a repo's
// "standard" at about $500, next to 20 catalog ones, must leave the published
// p90 at the catalog's $1.81 and count the repo's 20 as unknown.
func TestStatsRunsDropsARepoRecipeThatShadowsTheCatalog(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	var seeds []statsSeed
	for i := 1; i <= 20; i++ {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(i*100) * time.Second, cost: float64(i) * 0.1})
	}
	shadows := 0
	for i := 1; i <= 20; i++ {
		seeds = append(seeds, statsSeed{recipe: "standard", source: "repo", duration: 15 * time.Minute, cost: float64(i) * 500})
		shadows++
	}
	for range 20 {
		seeds = append(seeds, statsSeed{recipe: "standard", source: "project", duration: 15 * time.Minute, cost: 500})
		shadows++
	}
	seedStatsRuns(t, f, seeds, time.Now().UTC())

	doc := statsRunsJSON(t, f, "aurora")
	recipes, _ := doc["recipes"].([]any)
	if len(recipes) != 1 {
		t.Fatalf("recipes = %v, want only the catalog's standard", doc["recipes"])
	}
	group, _ := recipes[0].(map[string]any)
	if group["recipe"] != "standard" || group["runs"] != float64(20) {
		t.Fatalf("group = %v, want the catalog's 20 runs", group)
	}
	cost, _ := group["cost_usd"].(map[string]any)
	if cost["p90"] != 1.81 || cost["min"] != 0.10 {
		t.Errorf("cost_usd = %v, want the catalog's own range: a repository's loop must not be published as the built-in standard", cost)
	}
	if totals := statsTotals(t, doc); totals["runs_unknown_recipe"] != float64(shadows) {
		t.Errorf("totals = %v, want the %d shadowing runs counted as unknown", totals, shadows)
	}
}

// TestStatsRunsReportsAReadFailureWithoutNamingTheRun: a record that cannot be
// read for a reason that is not its corruption -- permission denied on a real
// installation -- fails the command, and the failure names no run: no repository
// slug, no run ID, no object key, no local path, and not the underlying error.
func TestStatsRunsReportsAReadFailureWithoutNamingTheRun(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	now := time.Now().UTC()
	var seeds []statsSeed
	for i := range 21 {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i) * time.Second, cost: 1})
	}
	seedStatsRuns(t, f, seeds, now)
	seedStatsUnreadable(t, f, now, 40)

	out, errOut, err := execute(t, "stats", "runs", "--project", "aurora", "--json")
	if err == nil {
		t.Fatalf("an unreadable record was not reported: %s", out)
	}
	if got := ExitCode(err); got != ExitRemoteError {
		t.Errorf("exit code = %d, want %d", got, ExitRemoteError)
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("err = %v, want it to say a record could not be read", err)
	}
	// The detail goes nowhere: not in the error, not on either stream.
	for _, text := range []string{err.Error(), out, errOut} {
		if strings.Contains(text, "permission denied") || strings.Contains(text, "result.json") {
			t.Errorf("the failure carries the underlying error: %q", text)
		}
	}
	assertNoIdentifiers(t, "the failure", err.Error())
	assertNoIdentifiers(t, "stdout", out)
	assertNoIdentifiers(t, "stderr", errOut)
}

// TestStatsRunsWarnsBelowTheThreshold: --min-runs 1 is allowed -- lowering the
// threshold deliberately is the operator's call (D97) -- but a range from one
// run is one run's exact figures, so the command says so on stderr rather than
// printing them quietly.
func TestStatsRunsWarnsBelowTheThreshold(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	seedStatsRuns(t, f, []statsSeed{{recipe: "standard", duration: time.Minute, cost: 1}}, time.Now().UTC())

	out, errOut, err := execute(t, "stats", "runs", "--project", "aurora", "--min-runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "warning") || !strings.Contains(errOut, "--min-runs 1") || !strings.Contains(errOut, "20") {
		t.Errorf("stderr = %q, want a warning naming --min-runs 1 and the 20 it is below", errOut)
	}
	if !strings.Contains(out, "one run's own exact figure") {
		t.Errorf("the output does not say a thin range is one run's exact figures:\n%s", out)
	}
	// At the threshold there is nothing to warn about.
	if _, errOut, err = execute(t, "stats", "runs", "--project", "aurora", "--min-runs", "1", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "warning") {
		t.Errorf("stderr = %q, want the warning on --json too", errOut)
	}
}

// TestStatsRunDropsAnImpossibleFigure: a figure that cannot be one -- a
// negative cost or duration, or a value that is not finite -- is no figure at
// all. Two of them in one window are enough to make the interpolation produce
// Inf, and --json then fails outright with "json: unsupported value: -Inf".
func TestStatsRunDropsAnImpossibleFigure(t *testing.T) {
	rec := func(cost float64, d time.Duration) *runstore.Record {
		start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
		done := start.Add(d)
		return &runstore.Record{
			RunID: "20261017-120000-abcd", Recipe: &runstore.RecipeRecord{Name: "standard", Source: "catalog"},
			StartedAt: start, FinishedAt: &done, Cost: &runstore.Cost{TotalUSD: cost},
		}
	}
	for _, tc := range []struct {
		name string
		rec  *runstore.Record
		want bool // want a figure
	}{
		{"a plain figure", rec(1, time.Minute), true},
		{"a zero cost", rec(0, time.Minute), true},
		{"a negative cost", rec(-5, time.Minute), false},
		{"a cost past any figure", rec(math.MaxFloat64, time.Minute), false},
		{"a NaN cost", rec(math.NaN(), time.Minute), false},
		{"an infinite cost", rec(math.Inf(-1), time.Minute), false},
		{"a negative duration", rec(1, -time.Minute), false},
		{"no cost breakdown", &runstore.Record{StartedAt: time.Now()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := statsRun(tc.rec)
			if got.NoFigure == tc.want {
				t.Errorf("statsRun = %+v, want a figure: %v", got, tc.want)
			}
			if !tc.want && got.CostUSD != 0 {
				t.Errorf("statsRun kept the impossible figure %v", got.CostUSD)
			}
		})
	}
}

// TestStatsRunsCountsARecordWithAnImpossibleFigure: such a record is counted
// with the ones that carry no figure, and the figures that are left are still
// published -- --json must decode, which it cannot if one of them is Inf.
func TestStatsRunsCountsARecordWithAnImpossibleFigure(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	var seeds []statsSeed
	for i := range 21 {
		seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i) * time.Second, cost: 1})
	}
	seeds = append(seeds,
		statsSeed{recipe: "standard", duration: time.Minute, cost: -5},
		statsSeed{recipe: "standard", duration: -time.Minute, cost: 1},
		// 1e308 is a cost a bad record can carry, and rounding it to the cent
		// multiplies it by 100 first.
		statsSeed{recipe: "standard", duration: time.Minute, cost: 1e308},
	)
	seedStatsRuns(t, f, seeds, time.Now().UTC())

	doc := statsRunsJSON(t, f, "aurora")
	recipes, _ := doc["recipes"].([]any)
	if len(recipes) != 1 {
		t.Fatalf("recipes = %v, want the one group of 21", doc["recipes"])
	}
	group, _ := recipes[0].(map[string]any)
	if group["runs"] != float64(21) {
		t.Errorf("group runs = %v, want 21", group["runs"])
	}
	if cost, _ := group["cost_usd"].(map[string]any); cost["min"] != float64(1) {
		t.Errorf("cost_usd = %v, want the finite figures only", cost)
	}
	if totals := statsTotals(t, doc); totals["runs_without_a_figure"] != float64(3) {
		t.Errorf("totals = %v, want the three impossible figures counted", totals)
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
