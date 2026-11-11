package cli

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// estimateSeed is one run the estimate tests seed: the stored cost F8 puts on
// the record, the pull request it is on, and the execution it is following.
type estimateSeed struct {
	pr       int
	tokens   map[string]runstore.ModelTokenCounts
	modelBy  map[string]float64
	modelUSD float64
	// noRecord seeds a run that never wrote result.json: no tokens to
	// reprice and nothing to read.
	noRecord bool
	// launched starts an execution and writes launch.json naming it, so a
	// reader that joins the run with the backend (runview) has a row to read
	// instead of the record.
	launched bool
}

// seedEstimateRun writes a run whose record carries per-model tokens and a
// model figure, the way a gateway or subscription run records them (G1.1).
// The record goes through runstore, so the estimate reads the bytes a real run
// leaves.
func seedEstimateRun(t *testing.T, f *cloudFixture, id string, s estimateSeed) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	st := runstore.Open(b, appSlug, id)
	// task.json is what makes the run locatable (runstore.Locate) and what
	// carries its pull request for a run whose record predates the field.
	if err := st.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", Title: "reprice me", PR: s.pr}); err != nil {
		t.Fatal(err)
	}
	if s.launched {
		exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, id)
		if err := st.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Backend: "cloud-run", Job: gcp.JobName(appSlug, "web"), Execution: exec, LaunchedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if s.noRecord {
		return
	}
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, StartedAt: time.Now().UTC().Add(-time.Hour),
		CostUSD: s.modelUSD,
		Cost: &runstore.Cost{
			ModelUSD: s.modelUSD, TotalUSD: s.modelUSD, Estimate: true,
			ModelBasis: runstore.BasisAPIList, ModelBy: s.modelBy, ModelTokens: s.tokens,
		},
	}
	if s.pr != 0 {
		rec.PR = &runstore.PRRef{Number: s.pr}
	}
	if err := st.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// estimateJSON is the document `fugaro estimate --json` prints.
type estimateJSON struct {
	Version          int     `json:"version"`
	Estimate         bool    `json:"estimate"`
	Run              string  `json:"run"`
	Repo             string  `json:"repo"`
	RunID            string  `json:"run_id"`
	PR               int     `json:"pr"`
	Model            string  `json:"model"`
	USD              float64 `json:"usd"`
	RecordedModelUSD float64 `json:"recorded_model_usd"`
	Models           []struct {
		Model        string  `json:"model"`
		Input        int64   `json:"input"`
		Output       int64   `json:"output"`
		CacheRead    int64   `json:"cache_read"`
		CacheWrite5m int64   `json:"cache_write_5m"`
		CacheWrite1h int64   `json:"cache_write_1h"`
		USD          float64 `json:"usd"`
	} `json:"models"`
	Catalog struct {
		Source    string `json:"source"`
		CheckedAt string `json:"checked_at"`
		AgeDays   int    `json:"age_days"`
		Stale     bool   `json:"stale"`
	} `json:"catalog"`
}

// decodeEstimate parses the document an estimate printed.
func decodeEstimate(t *testing.T, out string) estimateJSON {
	t.Helper()
	var doc estimateJSON
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("estimate --json is not JSON (%v):\n%s", err, out)
	}
	return doc
}

// estimateUSD runs the command and returns the document it printed. A test
// whose invocation is what the run-ID guard has to read (a windowed --pr
// lookup) calls execute itself: this helper's own command line is the
// forwarded one, not the caller's.
func estimateUSD(t *testing.T, args ...string) estimateJSON {
	t.Helper()
	out, _, err := execute(t, append([]string{"estimate", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("estimate %v: %v", args, err)
	}
	return decodeEstimate(t, out)
}

// TestEstimateReprices: the figure is the run's stored tokens priced at the
// named model's own rates -- cache writes and reads included -- and it is not
// the figure the run recorded. Mutation caught: an estimate that scaled the
// run's own dollars instead of pricing its tokens, or that dropped a cache
// column.
func TestEstimateReprices(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	id := recentRunID(t, time.Hour, "abcd")
	seedEstimateRun(t, f, id, estimateSeed{
		tokens: map[string]runstore.ModelTokenCounts{
			"claude-sonnet-5": {
				Input: 1_000_000, Output: 100_000, CacheRead: 2_000_000, CacheWrite5m: 500_000,
			},
			// A model the price table has no row for: the run's own path
			// keyed usage under it (F8), and repricing needs only the
			// target model's rates.
			"anthropic/claude-sonnet-5.5": {
				Input: 10_000, Output: 1_000, CacheRead: 1_000_000, CacheWrite1h: 1_000,
			},
		},
		modelBy:  map[string]float64{"claude-sonnet-5": 7.5},
		modelUSD: 7.5,
	})
	ref := appSlug + "/" + id
	// claude-opus-5-5 is $4/M in and $20/M out, cache writes at 1.25x (5m)
	// and 2x (1h) of the input price and cache reads at 0.05x: 8.9 + 0.268.
	doc := estimateUSD(t, "--run", ref, "--model", "claude-opus-5-5")
	if math.Abs(doc.USD-9.168) > 1e-9 {
		t.Errorf("estimate = $%v, want $9.168\n%+v", doc.USD, doc)
	}
	if doc.Version != 1 || !doc.Estimate {
		t.Errorf("the document does not carry its version and its estimate label: %+v", doc)
	}
	if doc.Run != ref || doc.Repo != "acme/app" || doc.RunID != id || doc.Model != "claude-opus-5-5" {
		t.Errorf("the document names something else: %+v", doc)
	}
	if math.Abs(doc.RecordedModelUSD-7.5) > 1e-9 {
		t.Errorf("recorded_model_usd = %v, want 7.5", doc.RecordedModelUSD)
	}
	if len(doc.Models) != 2 || doc.Models[0].Model != "anthropic/claude-sonnet-5.5" {
		t.Fatalf("models = %+v, want both of the run's models, sorted", doc.Models)
	}
	if got := doc.Models[1]; got.Input != 1_000_000 || got.CacheWrite5m != 500_000 {
		t.Errorf("the run's stored tokens are not the ones priced: %+v", got)
	}

	human, _, err := execute(t, "estimate", "--run", ref, "--model", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "$9.168") || !strings.Contains(human, "claude-opus-5-5") ||
		!strings.Contains(human, "claude-sonnet-5") {
		t.Errorf("the human output does not carry the figure and both models:\n%s", human)
	}
}

// TestEstimateIgnoresLongContextTiers: a tier is decided per call, and a run's
// summed input passes almost any threshold one is set above, so pricing the
// whole run against the row as it stands would charge the tier's rate to every
// token — which is the figure the command's own note says it does not compute
// (F9). The row here is an owner's model_prices override with a tier above 200k
// at twice the flat rate, and the run's million input tokens are priced flat.
func TestEstimateIgnoresLongContextTiers(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	f.appendConfig(t, "model_prices:\n"+
		"  claude-opus-5-5:\n"+
		"    input_per_m: 3\n"+
		"    output_per_m: 15\n"+
		"    long_context: { above_input_tokens: 200000, input_per_m: 6, output_per_m: 22.5 }\n")
	id := recentRunID(t, time.Hour, "abcd")
	seedEstimateRun(t, f, id, estimateSeed{tokens: map[string]runstore.ModelTokenCounts{
		"claude-sonnet-5": {Input: 1_000_000, Output: 100_000},
	}})
	doc := estimateUSD(t, "--run", appSlug+"/"+id, "--model", "claude-opus-5-5")
	// The override's flat rates: 1M in at $3/M and 100k out at $15/M. The
	// tier's $6/M and $22.5/M would give $8.25, and the embedded row the
	// override replaced would give $6.
	if math.Abs(doc.USD-4.5) > 1e-9 {
		t.Errorf("estimate = $%v, want the override's flat $4.50 and not the long-context tier's $8.25\n%+v", doc.USD, doc)
	}
	human, _, err := execute(t, "estimate", "--run", appSlug+"/"+id, "--model", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "$4.50") {
		t.Errorf("the human figure is not the flat one:\n%s", human)
	}
}

// TestEstimateRefusesRunWithoutTokens: a run this build cannot reprice is
// refused by name, not estimated from nothing. A run whose record predates
// per-model tokens and one that never wrote a record are the two shapes.
func TestEstimateRefusesRunWithoutTokens(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	empty := recentRunID(t, time.Hour, "abcd")
	unfinalized := recentRunID(t, 2*time.Hour, "beef")
	seedEstimateRun(t, f, empty, estimateSeed{modelUSD: 3, modelBy: map[string]float64{"claude-sonnet-5": 3}})
	seedEstimateRun(t, f, unfinalized, estimateSeed{noRecord: true})
	for _, ref := range []string{appSlug + "/" + empty, appSlug + "/" + unfinalized} {
		out, _, err := execute(t, "estimate", "--run", ref, "--model", "claude-opus-5-5")
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("estimate %s: err = %v, want a user error", ref, err)
		}
		if out != "" {
			t.Errorf("estimate %s printed %q on stdout, want nothing", ref, out)
		}
		if !strings.Contains(err.Error(), ref) || !strings.Contains(err.Error(), "tokens") {
			t.Errorf("the refusal does not name the run and what is missing: %v", err)
		}
	}
}

// TestEstimateRefusesUnknownModel: the model is a row of the price table or
// nothing. No alias table of its own (F7), so a nickname is refused with the
// IDs that work.
func TestEstimateRefusesUnknownModel(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	id := recentRunID(t, time.Hour, "abcd")
	seedEstimateRun(t, f, id, estimateSeed{tokens: map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1000}}})
	for _, model := range []string{"opus-4.5", "claude-opus-5.5", "sonnet"} {
		_, _, err := execute(t, "estimate", "--run", appSlug+"/"+id, "--model", model)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("--model %s: err = %v, want a user error", model, err)
		}
		// The known IDs, so the reader can pick one: the rows the table has.
		for _, known := range []string{"claude-opus-5-5", "claude-sonnet-5", "claude-haiku-4-5"} {
			if !strings.Contains(err.Error(), known) {
				t.Errorf("--model %s: the refusal does not name the known ID %s: %v", model, known, err)
			}
		}
	}
}

// TestEstimateLabelsEstimate: every figure is labelled an estimate in both
// readings, with the reason it cannot be exact (F9).
func TestEstimateLabelsEstimate(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	id := recentRunID(t, time.Hour, "abcd")
	seedEstimateRun(t, f, id, estimateSeed{tokens: map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1_000_000}}})
	ref := appSlug + "/" + id
	if doc := estimateUSD(t, "--run", ref, "--model", "claude-opus-5-5"); !doc.Estimate {
		t.Error("--json does not label the figure an estimate")
	}
	human, _, err := execute(t, "estimate", "--run", ref, "--model", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"estimate", "other token counts and retries", "cache ratio"} {
		if !strings.Contains(human, want) {
			t.Errorf("the output does not say %q:\n%s", want, human)
		}
	}
}

// TestEstimateStaleCatalog: the figure names the price table it was priced
// from, with its date, and says so when that table is past its own check
// window -- a stale table is the way an old estimate reads as a firm number
// (F9).
func TestEstimateStaleCatalog(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	old := budgetNow
	t.Cleanup(func() { budgetNow = old })
	budgetNow = func() time.Time { return time.Date(2027, 1, 25, 0, 0, 0, 0, time.UTC) }
	id := recentRunID(t, time.Hour, "abcd")
	seedEstimateRun(t, f, id, estimateSeed{tokens: map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1_000_000}}})
	ref := appSlug + "/" + id
	doc := estimateUSD(t, "--run", ref, "--model", "claude-opus-5-5")
	if doc.Catalog.CheckedAt != "2026-10-20" || doc.Catalog.Source == "" || doc.Catalog.AgeDays != 97 || !doc.Catalog.Stale {
		t.Errorf("catalog = %+v, want the table's own source and 2026-10-20, 97 days old and stale", doc.Catalog)
	}
	out, errOut, err := execute(t, "estimate", "--run", ref, "--model", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "checked 2026-10-20") {
		t.Errorf("the output does not name the table's date:\n%s", out)
	}
	if !strings.Contains(errOut, "older than 90 days") {
		t.Errorf("a stale table is not reported: stderr = %q", errOut)
	}
}

// TestEstimateByPR: --pr finds the run the way `runs ls --pr` does, over one
// repository and its own window (F16).
func TestEstimateByPR(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	id := recentRunID(t, time.Hour, "abcd")
	seedEstimateRun(t, f, id, estimateSeed{
		pr:       123,
		tokens:   map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1_000_000, Output: 100_000}},
		modelBy:  map[string]float64{"claude-sonnet-5": 4},
		modelUSD: 4,
	})
	// A run of this repository on another pull request, and one on this pull
	// request outside the window: neither is the run priced.
	seedEstimateRun(t, f, recentRunID(t, 2*time.Hour, "beef"), estimateSeed{pr: 124, tokens: map[string]runstore.ModelTokenCounts{"claude-opus-5": {Input: 1}}})
	seedEstimateRun(t, f, recentRunID(t, 100*24*time.Hour, "cafe"), estimateSeed{pr: 123, tokens: map[string]runstore.ModelTokenCounts{"claude-opus-5": {Input: 1}}})

	out, _, err := execute(t, "estimate", "--json", "--pr", "123", "--repo", "acme/app", "--model", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeEstimate(t, out)
	if doc.RunID != id || doc.PR != 123 {
		t.Errorf("estimate --pr 123 priced %+v, want run %s", doc, id)
	}
	// 1M in and 100k out at claude-opus-5-5: $4 + $2.
	if math.Abs(doc.USD-6) > 1e-9 {
		t.Errorf("estimate = $%v, want $6\n%+v", doc.USD, doc)
	}

	// The one-repository rule is runs ls's: a second repository in the config
	// makes --repo mandatory.
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: [web] }\n")
	if _, _, err := execute(t, "estimate", "--pr", "123", "--model", "claude-opus-5-5"); err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("estimate --pr without --repo: err = %v, want a refusal naming --repo", err)
	}
}

// TestEstimateSeveralRunsOnAPR: a pull request can have several runs (the
// first and its follow-ups) and they are not one figure: the command lists
// them and asks for --run (F16).
func TestEstimateSeveralRunsOnAPR(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	tokens := map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1_000_000}}
	first, second := recentRunID(t, 2*time.Hour, "aaaa"), recentRunID(t, time.Hour, "bbbb")
	seedEstimateRun(t, f, first, estimateSeed{pr: 123, tokens: tokens})
	seedEstimateRun(t, f, second, estimateSeed{pr: 123, tokens: tokens})
	_, _, err := execute(t, "estimate", "--pr", "123", "--repo", "acme/app", "--model", "claude-opus-5-5")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("two runs on one pull request: err = %v, want a user error", err)
	}
	for _, want := range []string{appSlug + "/" + first, appSlug + "/" + second, "--run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s:\n%v", want, err)
		}
	}
}

// TestEstimateReadsStoredCost: the tokens come from the record's own cost and
// never from runview's rebuilt row, which drops ModelTokens (F8) -- a reader
// of the row would refuse a run this command can price. Mutation caught:
// pricing the joined row instead of the stored record.
func TestEstimateReadsStoredCost(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	id := recentRunID(t, time.Hour, "abcd")
	// A launched run with an execution behind it, so the row a runview reader
	// would use exists: it carries the record's dollars and its cache totals
	// and neither ModelBy nor ModelTokens.
	seedEstimateRun(t, f, id, estimateSeed{
		launched: true,
		tokens:   map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1_000_000}},
		modelBy:  map[string]float64{"claude-sonnet-5": 4},
		modelUSD: 4,
	})
	doc := estimateUSD(t, "--run", appSlug+"/"+id, "--model", "claude-opus-5-5")
	if math.Abs(doc.USD-4) > 1e-9 {
		t.Errorf("estimate = $%v, want the stored tokens priced ($4)\n%+v", doc.USD, doc)
	}
}

// TestEstimateRefusesBadInvocations: the flag combinations that cannot mean
// anything are refused before a project, a bucket or a backend is opened,
// which is why this test needs no fixture.
func TestEstimateRefusesBadInvocations(t *testing.T) {
	const someRun = "acme/app/not-a-run-id"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no run":           {[]string{"--model", "claude-opus-5-5"}, "--run"},
		"run and pr":       {[]string{"--run", someRun, "--pr", "123", "--model", "claude-opus-5-5"}, "one of them"},
		"no model":         {[]string{"--run", someRun}, "--model"},
		"pr zero":          {[]string{"--pr", "0", "--repo", "acme/app", "--model", "claude-opus-5-5"}, "pull request number"},
		"since with a run": {[]string{"--run", someRun, "--since", "36h", "--model", "claude-opus-5-5"}, "--since"},
		"repo with a run":  {[]string{"--run", someRun, "--repo", "acme/app", "--model", "claude-opus-5-5"}, "--repo"},
		"a bad since":      {[]string{"--pr", "123", "--repo", "acme/app", "--since", "-3d", "--model", "claude-opus-5-5"}, "--since"},
		"an argument":      {[]string{someRun, "--model", "claude-opus-5-5"}, "unknown command"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := execute(t, append([]string{"estimate"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("estimate %v: err = %v, want a refusal naming %q", tc.args, err, tc.want)
			}
		})
	}
}
