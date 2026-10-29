package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// showComputeRow is what a test reads back out of one repos[] row. The compute
// key is kept raw so a test can tell null (present, unknown) from an absent key
// (no --compute at all): the document's own reader treats the two the same way,
// and this command must still write the one the flag asked for.
type showComputeRow struct {
	Slug       string          `json:"slug"`
	ComputeUSD json.RawMessage `json:"compute_usd"`
}

type showComputeDoc struct {
	Repos    []showComputeRow `json:"repos"`
	Warnings []string         `json:"warnings"`
}

func showCompute(t *testing.T, args ...string) (showComputeDoc, string) {
	t.Helper()
	out, _, err := execute(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	var doc showComputeDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return doc, out
}

// computeOf is one row's compute_usd: nil when the key is absent or null, the
// figure otherwise.
func computeOf(t *testing.T, doc showComputeDoc, slug string) *float64 {
	t.Helper()
	for _, r := range doc.Repos {
		if r.Slug != slug {
			continue
		}
		if len(r.ComputeUSD) == 0 || string(r.ComputeUSD) == "null" {
			return nil
		}
		var v float64
		if err := json.Unmarshal(r.ComputeUSD, &v); err != nil {
			t.Fatalf("repos[%s].compute_usd = %s: %v", slug, r.ComputeUSD, err)
		}
		return &v
	}
	t.Fatalf("no repos[] row for %s: %+v", slug, doc.Repos)
	return nil
}

// seedComputeRun puts one run's result.json in the fixture's runs bucket,
// which is the only place a run's compute figure lives.
func seedComputeRun(t *testing.T, f *budgetFixture, slug, id string, rec runstore.Record) {
	t.Helper()
	b, err := blobx.Open(context.Background(), "file://"+f.runsDir())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rec.RunID = id
	if err := runstore.Open(b.Bucket, slug, id).WriteRecord(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
}

// computeRecord is one run of a day: started at start, finished an hour later,
// with the compute figure it recorded.
func computeRecord(slug, id string, start time.Time, model, compute float64) runstore.Record {
	fin := start.Add(time.Hour)
	c := runstore.NewCost(model, compute, runstore.BasisAPIList)
	return runstore.Record{RunID: id, Repo: slug, StartedAt: start, FinishedAt: &fin, Cost: &c}
}

// computeDay is a UTC day at noon, so a run started on it is on it.
func computeDay(offsetDays int) time.Time {
	d := time.Now().UTC().AddDate(0, 0, offsetDays)
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.UTC)
}

// computeRunID mints a run ID the way the runner does: its UTC mint time and four
// hex digits. ListRunIDs reads the prefix back as the mint time.
func computeRunID(at time.Time, suffix string) string {
	return at.UTC().Format("20060102-150405") + "-" + suffix
}

// blockRunsBucket makes the fixture's runs bucket unreadable by pointing it
// under a regular file, so every read of it fails.
func blockRunsBucket(t *testing.T, f *budgetFixture) {
	t.Helper()
	block := filepath.Join(f.dir, "blocked")
	if err := os.WriteFile(block, []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.rewriteConfig(t, "file://"+f.runsDir(), "file://"+filepath.Join(block, "runs"))
}

// seedCorruptRecord leaves a result.json the record reader has to refuse under
// one run of a repository.
func seedCorruptRecord(t *testing.T, f *budgetFixture, slug, id string) {
	t.Helper()
	p := filepath.Join(f.runsDir(), "runs", slug, id, "result.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// computeWarnings is the warnings the --compute read added: the fixture's caps
// and counters carry warnings of their own, and a record it could not use is
// reported in the facts reader's own words ("skipping <slug>/<run>: its
// result.json cannot be used"), which name the file rather than compute.
func computeWarnings(doc showComputeDoc) []string {
	var out []string
	for _, w := range doc.Warnings {
		if strings.Contains(w, "compute") || strings.Contains(w, "result.json") {
			out = append(out, w)
		}
	}
	return out
}

// computeLineOf is one repository's compute line in the text view, "" when its
// block has none.
func computeLineOf(out, slug string) string {
	for _, block := range strings.Split(out, "\nrepository ") {
		head, _, _ := strings.Cut(block, "\n")
		if !strings.Contains(head, slug) {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "compute:") {
				return line
			}
		}
	}
	return ""
}

// --compute is opt-in: without it the document carries no compute_usd at all,
// with it every listed row carries the key (null where the figure is unknown).
func TestShowComputeIsOptIn(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	other := mustSlug("github", "acme/other")
	f.db.Set(budget.PathSpendRepo(today(), other), map[string]any{"counted": usd1})
	f.db.Set(budget.PathSpendGlobal(today()), map[string]any{"counted": usd1})
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})
	at := computeDay(0)
	id := computeRunID(at.Add(-2*time.Hour), "aaaa")
	seedComputeRun(t, f, appSlug, id, computeRecord("acme/app", id, at.Add(-2*time.Hour), 1.5, 0.25))

	plain, out := showCompute(t, "budget", "show", "--all", "--json")
	if len(plain.Repos) < 2 {
		t.Fatalf("want both repositories listed, got %+v", plain.Repos)
	}
	for _, r := range plain.Repos {
		if len(r.ComputeUSD) != 0 {
			t.Errorf("without --compute repos[%s] carries compute_usd %s: the flag is what pays for the read", r.Slug, r.ComputeUSD)
		}
	}
	if len(computeWarnings(plain)) != 0 {
		t.Errorf("without --compute the document warns %v: nothing was read", plain.Warnings)
	}
	if strings.Contains(out, "compute") {
		t.Errorf("without --compute the document names compute:\n%s", out)
	}

	asked, _ := showCompute(t, "budget", "show", "--all", "--json", "--compute")
	if v := computeOf(t, asked, appSlug); v == nil || *v != 0.25 {
		t.Errorf("with --compute repos[%s].compute_usd = %v, want 0.25", appSlug, v)
	}
	// A repository the day recorded nothing for is null, never 0. The key is
	// there in the JSON the flag asked for, which is what says "unknown"
	// rather than "no such figure".
	var sawNull bool
	for _, r := range asked.Repos {
		if r.Slug != other {
			continue
		}
		if len(r.ComputeUSD) == 0 {
			t.Fatalf("with --compute repos[%s] has no compute_usd key at all: with the flag an unknown figure is null", other)
		}
		if string(r.ComputeUSD) != "null" {
			t.Errorf("repos[%s].compute_usd = %s, want null: it has no run of the day", other, r.ComputeUSD)
		}
		sawNull = true
	}
	if !sawNull {
		t.Fatalf("the second repository was not listed: %+v", asked.Repos)
	}
}

// Without --compute the document is byte for byte what the command has always
// written: the same project, read twice, once around a runs bucket full of
// records and once around a bucket no read can reach, writes the same bytes.
// A read that leaked past the flag would show up as a warning and a different
// document.
func TestShowComputeOffPaysForNoRead(t *testing.T) {
	withRuns := newBudgetFixture(t, "")
	seedCaps(withRuns, 100, 20)
	withRuns.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 2 * usd1, "spent": usd1})
	at := computeDay(0)
	id := computeRunID(at.Add(-3*time.Hour), "aaaa")
	seedComputeRun(t, withRuns, appSlug, id, computeRecord("acme/app", id, at.Add(-3*time.Hour), 1, 0.5))
	_, full := showCompute(t, "budget", "show", "--all", "--json")

	// Same project and same counters, no readable bucket: newBudgetFixture
	// replaces FUGARO_CONFIG, so this is the document the same command writes
	// for the other fixture.
	broken := newBudgetFixture(t, "")
	seedCaps(broken, 100, 20)
	broken.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 2 * usd1, "spent": usd1})
	blockRunsBucket(t, broken)
	_, blocked := showCompute(t, "budget", "show", "--all", "--json")

	if full != blocked {
		t.Errorf("without --compute the document depends on the runs bucket:\n%s\n%s", full, blocked)
	}
}

// The figure is the day's runs summed per repository, attributed to the day
// each run started on: yesterday's runs do not count towards today, and a run
// that recorded no estimate (compute_estimated false) adds nothing rather than
// a free 0.
func TestShowComputeSumsPerRepoAndDay(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	todayNoon, yestNoon := computeDay(0), computeDay(-1)
	other := mustSlug("github", "acme/other")
	f.db.Set(budget.PathSpendRepo(today(), other), map[string]any{"counted": usd1})
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})

	first, second, old := todayNoon.Add(-4*time.Hour), todayNoon.Add(-3*time.Hour), yestNoon
	seedComputeRun(t, f, appSlug, computeRunID(first, "aaaa"), computeRecord("acme/app", computeRunID(first, "aaaa"), first, 1, 0.25))
	seedComputeRun(t, f, appSlug, computeRunID(second, "bbbb"), computeRecord("acme/app", computeRunID(second, "bbbb"), second, 2, 0.10))
	seedComputeRun(t, f, appSlug, computeRunID(old, "cccc"), computeRecord("acme/app", computeRunID(old, "cccc"), old, 1, 9))

	// Today, on the other repository, with no estimate: its figure is unknown,
	// not 0.
	noEst := computeRunID(todayNoon.Add(-time.Hour), "dddd")
	start := todayNoon.Add(-time.Hour)
	fin := start.Add(20 * time.Minute)
	un := runstore.ModelOnlyCost(1, runstore.BasisAPIList)
	seedComputeRun(t, f, other, noEst, runstore.Record{RunID: noEst, Repo: "acme/other", StartedAt: start, FinishedAt: &fin, Cost: &un})

	doc, _ := showCompute(t, "budget", "show", "--all", "--json", "--compute")
	if v := computeOf(t, doc, appSlug); v == nil || *v != 0.35 {
		t.Errorf("repos[%s].compute_usd = %v, want 0.35 (0.25 + 0.10 today, yesterday's 9 left out)", appSlug, v)
	}
	if v := computeOf(t, doc, other); v != nil {
		t.Errorf("repos[%s].compute_usd = %v, want null: its only run of the day recorded no estimate", other, *v)
	}
	if w := computeWarnings(doc); len(w) != 0 {
		t.Errorf("a readable bucket warns about compute: %v", w)
	}
}

// A result.json that cannot be read is skipped with the warning the facts
// reader writes about it, and the command still answers with the runs it could
// read.
func TestShowComputeSkipsAnUnreadableRecord(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})
	at := computeDay(0)
	good, bad := at.Add(-4*time.Hour), at.Add(-3*time.Hour)
	seedComputeRun(t, f, appSlug, computeRunID(good, "aaaa"), computeRecord("acme/app", computeRunID(good, "aaaa"), good, 1, 0.4))
	seedCorruptRecord(t, f, appSlug, computeRunID(bad, "bbbb"))

	doc, _ := showCompute(t, "budget", "show", "--all", "--json", "--compute")
	if v := computeOf(t, doc, appSlug); v == nil || *v != 0.4 {
		t.Errorf("repos[%s].compute_usd = %v, want 0.4: the readable run still counts", appSlug, v)
	}
	if w := computeWarnings(doc); len(w) != 1 || !strings.Contains(w[0], computeRunID(bad, "bbbb")) {
		t.Errorf("compute warnings = %v, want one naming the unreadable record %s", w, computeRunID(bad, "bbbb"))
	}
}

// A bucket that cannot be read leaves every figure null and says so once: the
// command the console's other screens call succeeds either way, and an unknown
// figure is never a 0 that reads as free.
func TestShowComputeReadFailureIsAWarningAndNull(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})
	blockRunsBucket(t, f)

	doc, _ := showCompute(t, "budget", "show", "--all", "--json", "--compute")
	if len(doc.Repos) == 0 {
		t.Fatal("no rows to check")
	}
	for _, r := range doc.Repos {
		if len(r.ComputeUSD) == 0 || string(r.ComputeUSD) != "null" {
			t.Errorf("repos[%s].compute_usd = %s, want null: the bucket could not be read", r.Slug, r.ComputeUSD)
		}
	}
	if w := computeWarnings(doc); len(w) != 1 || !strings.Contains(w[0], "compute is unavailable") {
		t.Errorf("compute warnings = %v, want one saying compute is unavailable", w)
	}

	// The text view succeeds too, with the same warning and the figures it
	// always printed.
	out, _, err := execute(t, "budget", "show", "--all", "--compute")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "compute is unavailable") {
		t.Errorf("the text view does not warn that compute is unavailable:\n%s", out)
	}
	if !strings.Contains(out, "headroom: $") {
		t.Errorf("the text view stopped printing the figures it always did:\n%s", out)
	}
}

// --compute earns its cost in the text view too: a person who asks for the
// figure sees it, and `n/a` where the day recorded none. A flag that only ever
// changes --json and otherwise buys a bucket read nobody can see is a flag to
// refuse without --json, not to leave silent.
func TestShowComputePrintsTheFigureInText(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	other := mustSlug("github", "acme/other")
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})
	f.db.Set(budget.PathSpendRepo(today(), other), map[string]any{"counted": usd1})
	at := computeDay(0)
	start := at.Add(-2 * time.Hour)
	seedComputeRun(t, f, appSlug, computeRunID(start, "aaaa"), computeRecord("acme/app", computeRunID(start, "aaaa"), start, 1, 0.25))

	out, _, err := execute(t, "budget", "show", "--all", "--compute")
	if err != nil {
		t.Fatal(err)
	}
	if got := computeLineOf(out, appSlug); got != "compute: $0.25" {
		t.Errorf("repos[%s] text line = %q, want %q:\n%s", appSlug, got, "compute: $0.25", out)
	}
	// The repository the day recorded no estimate for is n/a, never $0.00.
	if got := computeLineOf(out, other); got != "compute: n/a" {
		t.Errorf("repos[%s] text line = %q, want %q:\n%s", other, got, "compute: n/a", out)
	}
	// And without the flag the text view is what it always was.
	plain, _, err := execute(t, "budget", "show", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(plain), "compute") {
		t.Errorf("the text view names compute without --compute:\n%s", plain)
	}
}

// The read is narrowed to the repositories the document lists: a repository
// this command does not show keeps its records to itself, so --repo pays for
// one repository's runs and an unreadable record elsewhere is not reported.
func TestShowComputeReadsOnlyTheListedRepositories(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	other := mustSlug("github", "acme/other")
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})
	f.db.Set(budget.PathSpendRepo(today(), other), map[string]any{"counted": usd1})
	at := computeDay(0)
	start := at.Add(-3 * time.Hour)
	bad := at.Add(-2 * time.Hour)
	seedComputeRun(t, f, appSlug, computeRunID(start, "aaaa"), computeRecord("acme/app", computeRunID(start, "aaaa"), start, 1, 0.25))
	seedCorruptRecord(t, f, other, computeRunID(bad, "bbbb"))

	one, _ := showCompute(t, "budget", "show", "--repo", "acme/app", "--json", "--compute")
	if v := computeOf(t, one, appSlug); v == nil || *v != 0.25 {
		t.Errorf("repos[%s].compute_usd = %v, want 0.25", appSlug, v)
	}
	if w := computeWarnings(one); len(w) != 0 {
		t.Errorf("--repo read a repository it does not list: %v", w)
	}

	// --all does list it, and then its unreadable record is a warning like any
	// other.
	all, _ := showCompute(t, "budget", "show", "--all", "--json", "--compute")
	if w := computeWarnings(all); len(w) != 1 || !strings.Contains(w[0], computeRunID(bad, "bbbb")) {
		t.Errorf("--all compute warnings = %v, want one naming %s", w, computeRunID(bad, "bbbb"))
	}
}

// A figure that cannot be added up is no figure. Two runs whose own compute
// figures are finite but whose sum is not leave the row unknown with a warning,
// rather than a number json.Marshal refuses -- which would fail a command the
// console's other screens call and --compute may never fail.
func TestShowComputeRefusesAFigureItCannotAddUp(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": usd1})
	at := computeDay(0)
	first, second := at.Add(-3*time.Hour), at.Add(-2*time.Hour)
	// runstore.NewCost rounds TotalUSD from compute*100, which is refused past
	// about 1.8e306, so the huge figure is put on the record after it is built:
	// each record is finite on its own and only their sum is not.
	huge := func(start time.Time, id string) runstore.Record {
		c := runstore.NewCost(1, 1e306, runstore.BasisAPIList)
		c.ComputeUSD = 1e308
		fin := start.Add(time.Hour)
		return runstore.Record{RunID: id, Repo: "acme/app", StartedAt: start, FinishedAt: &fin, Cost: &c}
	}
	seedComputeRun(t, f, appSlug, computeRunID(first, "aaaa"), huge(first, computeRunID(first, "aaaa")))
	seedComputeRun(t, f, appSlug, computeRunID(second, "bbbb"), huge(second, computeRunID(second, "bbbb")))

	doc, out := showCompute(t, "budget", "show", "--all", "--json", "--compute")
	if v := computeOf(t, doc, appSlug); v != nil {
		t.Errorf("repos[%s].compute_usd = %v, want null: the day's figures do not add up to a number", appSlug, *v)
	}
	if !strings.Contains(out, `"compute_usd": null`) {
		t.Errorf("the row does not carry null for a figure that cannot be added up:\n%s", out)
	}
	if w := computeWarnings(doc); len(w) != 1 || !strings.Contains(w[0], "do not add up to a number") {
		t.Errorf("compute warnings = %v, want one saying the figures do not add up", w)
	}
}
