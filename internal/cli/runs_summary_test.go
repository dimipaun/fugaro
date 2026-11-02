package cli

// fugaro runs summary: the run counts and the spend of one window, read from
// the budget database and the spend history and from no run object at all.
//
// The document's shape is the contract (schemas/summary.schema.json). The
// tests below pin the three properties the command exists for: it reads no run
// object, its counts agree with runs ls over the same fixture window, and a day
// neither source answered for is named as unknown rather than counted as zero.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// summaryOut is the document fugaro runs summary --json prints, decoded into
// the command's own types: the wire names are pinned by
// schemas/summary.schema.json and by the contract section that documents it,
// not by a second copy here.
type summaryOut = summaryDoc

// summaryRecordUSD is what every run record the fixture writes says the run
// cost. The budget database never agrees with it, so a summary that reached
// for a run object would be caught reporting this instead.
const summaryRecordUSD = 999

// summaryFixture is one installation as the summary's two readers see it: the
// budget database's day nodes and the spend history. The runs bucket is the
// cloud fixture's, so the same fixture drives runs ls for the comparison.
type summaryFixture struct {
	*cloudFixture
	fs  *gcpfake.Firestore
	db  *gcpfake.RTDB
	now time.Time
}

// summaryNow is the instant the fixture's clocks are pinned to: the UTC day its
// window ends on and the day its run IDs are minted against.
var summaryNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func newSummaryFixture(t *testing.T) *summaryFixture {
	t.Helper()
	fs := gcpfake.NewFirestore(t)
	f := &summaryFixture{cloudFixture: newCloudFixture(t, "firestore: "+fs.URL), fs: fs, db: gcpfake.NewRTDB(t), now: summaryNow}
	f.appendConfig(t, "budget: { mode: observe, firebase_project: proj-1234, rtdb_url: "+f.db.URL+" }\n")
	f.db.Set("fugaro/project", "aurora")
	// The history's own mark: a reader checks it before trusting the database,
	// exactly as fugaro report does.
	f.fs.Set(budget.MetaCollection, budget.MarkDocument, map[string]any{"project": "aurora", "version": int64(1)})
	oldBudget, oldLs := budgetNow, lsNow
	budgetNow, lsNow = func() time.Time { return f.now }, func() time.Time { return f.now }
	t.Cleanup(func() { budgetNow, lsNow = oldBudget, oldLs })
	return f
}

// day is the fixture's UTC day n days back from its pinned day.
func (f *summaryFixture) day(n int) int64 { return budget.Day(f.now) - int64(n) }

// addRepo adds a repository to the fixture's config, as a second repos: entry.
func (f *summaryFixture) addRepo(t *testing.T, entry string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "repos:\n", "repos:\n"+entry, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noBucket points the fixture's config at a runs bucket nothing can open, so
// every read of a run object fails. It is how a test proves a reader answered
// without one: the fixture's own runs ls cannot answer at all afterwards.
func (f *summaryFixture) noBucket(t *testing.T) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(b), "bucket_url: "+f.bucket, "bucket_url: nosuchscheme://runs", 1)
	if text == string(b) {
		t.Fatalf("the fixture's config names no bucket_url %s", f.bucket)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// summaryRun is one run as the fixture writes it into both readers.
type summaryRun struct {
	day    int64
	slug   string
	repo   string
	status string
	spent  budget.Micros
	// notional is the part of the run's model figure a subscription paid: it
	// counts toward the plan's limits and is no one's bill.
	notional budget.Micros
	seq      int
}

// summaryRunID is the run ID of the seq'th run the fixture mints for day: a
// run ID starts with its UTC mint time, so runs ls lists it in the window the
// summary's day is in.
func summaryRunID(day int64, seq int) string {
	t := time.UnixMilli(day * 86_400_000).UTC().Add(12*time.Hour + time.Duration(seq)*time.Minute)
	return t.Format("20060102-150405") + fmt.Sprintf("-%04x", seq)
}

// seedDB writes one finished run into the budget database: the day's run
// share, the repository's day counter (which every run of the day shares), the
// run's lifetime ledger and its outcome.
func (f *summaryFixture) seedDB(t *testing.T, r summaryRun) string {
	t.Helper()
	id := summaryRunID(r.day, r.seq)
	// The repository's display name, as `fugaro budget set --repo` writes it:
	// the day records carry it, and a repository without one reads as its slug.
	f.db.Set(budget.PathCapsRepo(r.slug), map[string]any{"repo": r.repo})
	spent, notional := r.spent+f.counterOf(r.day, r.slug), r.notional+f.notionalOf(r.day, r.slug)
	f.db.Set(budget.PathSpendRepo(r.day, r.slug), map[string]any{
		"spent": int64(spent), "counted": int64(spent), "notional": int64(notional)})
	f.db.Set(budget.PathSpendRun(r.day, r.slug, id), map[string]any{
		"spent": int64(r.spent), "notional": int64(r.notional), "reserved": int64(r.spent), "released": int64(r.spent)})
	f.db.Set(budget.PathRun(r.slug, id), map[string]any{
		"spent": int64(r.spent), "notional": int64(r.notional), "reserved": int64(r.spent), "released": int64(r.spent)})
	f.db.Set(budget.PathOutcome(r.day, r.slug, id), map[string]any{
		"status": r.status, "requestedBy": "someone@example.com"})
	return id
}

// counterOf and notionalOf are the day counter's figures as the fixture has
// written them so far: the runs of one day accumulate onto the one node.
func (f *summaryFixture) counterOf(day int64, slug string) budget.Micros {
	return f.counterField(day, slug, "spent")
}

func (f *summaryFixture) notionalOf(day int64, slug string) budget.Micros {
	return f.counterField(day, slug, "notional")
}

func (f *summaryFixture) counterField(day int64, slug, field string) budget.Micros {
	node, _ := f.db.Value(budget.PathSpendRepo(day, slug)).(map[string]any)
	n, _ := node[field].(json.Number)
	m, _ := n.Int64()
	return budget.Micros(m)
}

// seed writes one finished run into both readers: the database above, and the
// runs bucket, where runs ls reads it.
func (f *summaryFixture) seed(t *testing.T, r summaryRun) string {
	t.Helper()
	ctx := context.Background()
	id := f.seedDB(t, r)
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, r.slug, id)
	if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: r.repo, Ref: "main",
		Workflow: "web", Task: "x", RequestedBy: "someone@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: r.repo, Workflow: "web",
		Status: runstore.Status(r.status), CostUSD: summaryRecordUSD}); err != nil {
		t.Fatal(err)
	}
	return id
}

// spent writes one spendDaily document, as the rollover does for a day that
// has left the database's window.
func (f *summaryFixture) spent(t *testing.T, date, slug, repo string, spent budget.Micros) {
	t.Helper()
	r := budget.DayRecord{Repo: repo, Slug: slug, Date: date, Version: budget.DayRecordVersion, Final: true,
		SpentMicros: spent, Runs: 1, Outcomes: map[string]int64{"succeeded": 1}}
	f.fs.Set(budget.SpendCollection, r.DocID(), r.ToFields())
}

// spentPriced writes one spendDaily document for a day whose compute was
// priced, which is the only way a figure reaches the document's compute
// column: the live half opens no runs bucket, so it never estimates one.
func (f *summaryFixture) spentPriced(t *testing.T, date, slug, repo string, spent, compute budget.Micros) {
	t.Helper()
	r := budget.DayRecord{Repo: repo, Slug: slug, Date: date, Version: budget.DayRecordVersion, Final: true,
		SpentMicros: spent, ComputeMicros: compute, ComputeEstimatedRuns: 1, Runs: 1,
		Outcomes: map[string]int64{"succeeded": 1}}
	f.fs.Set(budget.SpendCollection, r.DocID(), r.ToFields())
}

// summaryJSON runs the command and decodes its document.
func summaryJSON(t *testing.T, args ...string) (summaryOut, string) {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "summary", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("runs summary: %v (%s)", err, errOut)
	}
	var got summaryOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got, out
}

// TestSummaryReadsNoRunObjects is the command's reason to exist: the counts
// and the spend come from the budget database and the spend history, so a
// window whose runs bucket cannot be opened at all is answered like any other.
func TestSummaryReadsNoRunObjects(t *testing.T) {
	f := newSummaryFixture(t)
	d := f.day(0)
	f.seedDB(t, summaryRun{day: d, slug: appSlug, repo: "acme/app", status: "succeeded", spent: 2_000_000, seq: 1})
	f.noBucket(t)

	// The same fixture's listing cannot answer: every run object read fails,
	// so the summary below answered without one.
	if _, _, err := execute(t, "runs", "ls", "--json"); err == nil {
		t.Fatal("runs ls answered over an unopenable runs bucket, so this test would prove nothing")
	}

	got, _ := summaryJSON(t)
	if len(got.Repositories) != 1 {
		t.Fatalf("repositories = %+v", got.Repositories)
	}
	if r := got.Repositories[0]; r.Runs != 1 || r.SpentMicros != 2_000_000 || r.States["succeeded"] != 1 {
		t.Fatalf("repositories[0] = %+v", r)
	}
	// The record beside the database's node says the run cost
	// summaryRecordUSD, and nothing read it.
	if got.Repositories[0].SpentMicros == summaryRecordUSD*1_000_000 {
		t.Fatalf("the spend is the run record's figure: %+v", got.Repositories[0])
	}
}

// TestSummaryCountsMatchRunsLs pins the counts against the other reader of the
// same window: per repository and per state, the summary says what runs ls's
// rows say.
func TestSummaryCountsMatchRunsLs(t *testing.T) {
	f := newSummaryFixture(t)
	other := mustSlug("bitbucket", "acme/other")
	f.addRepo(t, "  acme/other: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	today, yesterday := f.day(0), f.day(1)
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_000_000, seq: 1})
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "failed", spent: 2_000_000, seq: 2})
	f.seed(t, summaryRun{day: yesterday, slug: appSlug, repo: "acme/app", status: "halted", spent: 3_000_000, seq: 3})
	f.seed(t, summaryRun{day: yesterday, slug: other, repo: "acme/other", status: "succeeded", spent: 4_000_000, seq: 4})
	f.seed(t, summaryRun{day: yesterday, slug: other, repo: "acme/other", status: "cancelled", notional: 500_000, seq: 5})

	// The same window, from the runs bucket: runs ls's rows are the runs
	// themselves, so their states are the comparison's other half.
	rows, _ := lsJSON(t)
	fromLs := map[string]map[string]int64{}
	for _, row := range rows.Runs {
		if fromLs[row.Repo] == nil {
			fromLs[row.Repo] = map[string]int64{}
		}
		fromLs[row.Repo][row.Status]++
	}
	got, _ := summaryJSON(t)
	if len(got.Repositories) != 2 {
		t.Fatalf("repositories = %+v", got.Repositories)
	}
	// The row is keyed by the storage slug (what the database holds) and named
	// by the display name (what a reader recognizes).
	wantSlug := map[string]string{"acme/app": appSlug, "acme/other": other}
	var runs int64
	for _, r := range got.Repositories {
		if wantSlug[r.Repo] != r.Slug {
			t.Errorf("%s: slug = %q, want %q", r.Repo, r.Slug, wantSlug[r.Repo])
		}
		want := fromLs[r.Repo]
		if len(want) == 0 {
			t.Fatalf("runs ls listed no run of %s, and the summary reports %+v", r.Repo, r)
		}
		var sum int64
		for state, n := range want {
			if r.States[state] != n {
				t.Errorf("%s: summary says %d %s, runs ls says %d", r.Slug, r.States[state], state, n)
			}
			sum += n
		}
		if r.Runs != sum {
			t.Errorf("%s: summary counts %d runs, runs ls lists %d", r.Slug, r.Runs, sum)
		}
		runs += r.Runs
	}
	if got.Totals.Runs != int64(len(rows.Runs)) || got.Totals.Runs != runs {
		t.Errorf("totals: summary counts %d runs, its rows %d, runs ls %d", got.Totals.Runs, runs, len(rows.Runs))
	}
}

// TestSummarySpendIsTheHistorys pins the money: a window holding an archived
// day and live days reports the history's figure for the one and the
// database's for the others, and the two are added per repository.
func TestSummarySpendIsTheHistorys(t *testing.T) {
	f := newSummaryFixture(t)
	today, yesterday, archived := f.day(0), f.day(1), f.day(2)
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_500_000, seq: 1})
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "failed", spent: 500_000, seq: 2})
	f.seed(t, summaryRun{day: yesterday, slug: appSlug, repo: "acme/app", status: "succeeded", spent: 2_000_000, seq: 3})
	// The archived day: the rollover has moved it to the history, and the
	// database holds no node of it any more.
	f.spent(t, budget.DayDate(archived), appSlug, "acme/app", 4_000_000)

	got, _ := summaryJSON(t)
	if len(got.Repositories) != 1 {
		t.Fatalf("repositories = %+v", got.Repositories)
	}
	r := got.Repositories[0]
	if r.SpentMicros != 8_000_000 || r.Runs != 4 {
		t.Fatalf("repositories[0] = %+v, want the history's 4.00 plus the database's 4.00 over 4 runs", r)
	}
	if got.Totals.SpentMicros != 8_000_000 || got.Totals.Runs != 4 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if r.Repo != "acme/app" {
		t.Errorf("repo = %q, want the display name the day records carry", r.Repo)
	}
	// No run object holds a compute estimate: the summary says n/a rather than
	// counting the runs it cannot price as free.
	if r.ComputeMicros != nil || r.ComputeRuns != 0 {
		t.Errorf("compute = %v over %d runs, want unknown", r.ComputeMicros, r.ComputeRuns)
	}
	if len(got.DaysUnknown) != 0 {
		t.Errorf("days_unknown = %v, and both sources answered every day of the window", got.DaysUnknown)
	}
}

// TestSummaryNamesTheDaysNothingAnswered is the "unknown, never zero" rule: a
// window reaching past the days the database holds, on an installation with no
// spend history to answer for them, names those days and counts nothing on
// them.
func TestSummaryNamesTheDaysNothingAnswered(t *testing.T) {
	f := newSummaryFixture(t)
	// No Firestore database at all: the spend history cannot answer for any
	// day, which is the shape of a project that never set one up.
	f.fs.RemoveDatabase()
	f.seed(t, summaryRun{day: f.day(0), slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_000_000, seq: 1})

	got, out := summaryJSON(t, "--since", "30d")
	// The database holds the days it has not pruned (today and the eight
	// before it); every older day of the window is named, not counted as a
	// day that spent nothing.
	if len(got.DaysUnknown) != 21 {
		t.Fatalf("days_unknown = %d days (%v), want the 21 the history would have answered", len(got.DaysUnknown), got.DaysUnknown)
	}
	if got.DaysUnknown[0] != budget.DayDate(f.day(29)) || got.DaysUnknown[20] != budget.DayDate(f.day(9)) {
		t.Errorf("days_unknown = %v, want %s to %s", got.DaysUnknown, budget.DayDate(f.day(29)), budget.DayDate(f.day(9)))
	}
	if got.Totals.Runs != 1 || got.Totals.SpentMicros != 1_000_000 {
		t.Fatalf("totals = %+v, want the live day's run alone", got.Totals)
	}
	if !strings.Contains(out, budget.DayDate(f.day(29))) {
		t.Errorf("the document does not name the unknown day:\n%s", out)
	}
	if !strings.Contains(strings.Join(got.Notes, "\n"), "history") {
		t.Errorf("notes = %v, want the line saying the history answered nothing", got.Notes)
	}
}

// TestSummaryPrintsTheTable pins the half of the output the document tests
// never look at: the human table. The header's columns and their order, one row
// per repository, the TOTAL row that adds them up, a compute cell that is n/a
// where nothing priced the run and a figure where the history did, and the
// notes printed above it all.
func TestSummaryPrintsTheTable(t *testing.T) {
	f := newSummaryFixture(t)
	other := mustSlug("bitbucket", "acme/other")
	f.addRepo(t, "  acme/other: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	today := f.day(0)
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_500_000, seq: 1})
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "failed", spent: 500_000, seq: 2})
	// An archived day whose compute the rollover priced: the row's COMPUTE is a
	// figure, while the live repository's is n/a.
	f.spentPriced(t, budget.DayDate(f.day(1)), other, "acme/other", 1_000_000, 500_000)

	out, errOut, err := execute(t, "runs", "summary")
	if err != nil {
		t.Fatalf("runs summary: %v (%s)", err, errOut)
	}
	// The rows by their first cell, so the tabwriter's own padding is not what
	// is pinned here.
	rows := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if fs := strings.Fields(line); len(fs) > 0 {
			if _, ok := rows[fs[0]]; !ok {
				rows[fs[0]] = fs
			}
		}
	}
	wantHeader := "REPOSITORY RUNS SUCCEEDED FAILED HALTED CANCELLED INFRA ERROR OTHER SPENT NOTIONAL COMPUTE"
	if got := strings.Join(rows["REPOSITORY"], " "); got != wantHeader {
		t.Errorf("header = %q, want %q\n%s", got, wantHeader, out)
	}
	for _, tc := range []struct {
		first string
		want  string
	}{
		// The repository the database answered for: two runs, no compute
		// estimate, so n/a rather than a zero that would read as free.
		{"acme/app", "acme/app 2 1 1 0 0 0 0 $2 $0 n/a"},
		// The repository the history answered for, one run and its compute.
		{"acme/other", "acme/other 1 1 0 0 0 0 0 $1 $0 $0.50"},
		// The TOTAL row: the rows above added up, compute included.
		{"TOTAL", "TOTAL 3 2 1 0 0 0 0 $3 $0 $0.50"},
	} {
		got := strings.Join(rows[tc.first], " ")
		if got != tc.want {
			t.Errorf("the %s row = %q, want %q\n%s", tc.first, got, tc.want, out)
		}
	}
	// The window and the notes come before the table, one line each: a person
	// reading it reads what it counts and what it could not cover first.
	if !strings.HasPrefix(out, "project: ") || !strings.HasPrefix(strings.SplitN(out, "\n", 3)[1], "window:") {
		t.Errorf("the table does not open with the project and the window:\n%s", out)
	}
	if i, j := strings.Index(out, "no run object was read"), strings.Index(out, "REPOSITORY"); i < 0 || j < 0 || i > j {
		t.Errorf("the notes are not printed above the table:\n%s", out)
	}
}

// TestSummaryPrintsAnEmptyWindow: a window nothing ran in is said in words, not
// drawn as a table of zeros a reader could take for a failure.
func TestSummaryPrintsAnEmptyWindow(t *testing.T) {
	newSummaryFixture(t)
	out, errOut, err := execute(t, "runs", "summary")
	if err != nil {
		t.Fatalf("runs summary: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "no run and no spend in this window") {
		t.Errorf("the empty window is not said plainly:\n%s", out)
	}
	if strings.Contains(out, "REPOSITORY") {
		t.Errorf("the empty window is drawn as a table:\n%s", out)
	}
}

// TestSummaryFiltersToTheNamedRepository: --repo narrows the read to one
// repository, and the totals follow the rows rather than the window. The filter
// is the storage slug the sources file a repository under, so the name the
// caller spells is resolved to it first, and a name nothing resolves is refused
// rather than answered as an empty window.
func TestSummaryFiltersToTheNamedRepository(t *testing.T) {
	f := newSummaryFixture(t)
	other := mustSlug("bitbucket", "acme/other")
	f.addRepo(t, "  acme/other: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	today := f.day(0)
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_000_000, seq: 1})
	f.seed(t, summaryRun{day: today, slug: other, repo: "acme/other", status: "failed", spent: 2_000_000, seq: 2})

	all, _ := summaryJSON(t)
	if len(all.Repositories) != 2 || all.Totals.Runs != 2 {
		t.Fatalf("the unfiltered window holds %+v, so this test would prove nothing", all)
	}

	got, out := summaryJSON(t, "--repo", "acme/other")
	if len(got.Repositories) != 1 {
		t.Fatalf("repositories = %+v, want acme/other alone", got.Repositories)
	}
	r := got.Repositories[0]
	if r.Slug != other || r.Repo != "acme/other" || r.Runs != 1 || r.States["failed"] != 1 || r.SpentMicros != 2_000_000 {
		t.Errorf("repositories[0] = %+v", r)
	}
	if got.Totals.Runs != 1 || got.Totals.SpentMicros != 2_000_000 || got.Totals.States["succeeded"] != 0 {
		t.Errorf("totals = %+v, want the filtered repository's figures alone", got.Totals)
	}
	if strings.Contains(out, appSlug) || strings.Contains(out, "acme/app") {
		t.Errorf("the document still carries the repository --repo left out:\n%s", out)
	}

	if _, errOut, err := execute(t, "runs", "summary", "--repo", "acme/nosuch", "--json"); err == nil {
		t.Errorf("--repo acme/nosuch was answered rather than refused: %s", errOut)
	}
}

// TestSummarySaturatesASumThatWouldWrap: the money is added as micro-dollar
// integers, and a long window over many repositories must not wrap into a
// negative figure a reader would take for a real one. The budget database's own
// rollup sums its counters the same way (budget's satAdd), so the summary
// saturates at the largest figure the integer holds.
func TestSummarySaturatesASumThatWouldWrap(t *testing.T) {
	f := newSummaryFixture(t)
	other := mustSlug("bitbucket", "acme/other")
	f.addRepo(t, "  acme/other: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	today := f.day(0)
	f.seed(t, summaryRun{day: today, slug: appSlug, repo: "acme/app", status: "succeeded", spent: math.MaxInt64, seq: 1})
	f.seed(t, summaryRun{day: today, slug: other, repo: "acme/other", status: "succeeded", spent: math.MaxInt64, seq: 2})

	got, out := summaryJSON(t)
	if got.Totals.SpentMicros != math.MaxInt64 {
		t.Errorf("totals.spent_micros = %d, want the sum saturated at %d:\n%s", got.Totals.SpentMicros, int64(math.MaxInt64), out)
	}
	if len(got.Repositories) != 2 {
		t.Fatalf("repositories = %+v", got.Repositories)
	}
	for _, r := range got.Repositories {
		if r.SpentMicros != math.MaxInt64 {
			t.Errorf("%s: spent_micros = %d, want %d", r.Slug, r.SpentMicros, int64(math.MaxInt64))
		}
	}
}

// TestSummaryRefusesAnUnboundedWindow: a summary is per UTC day, so a window
// with no lower bound has no days to report and is refused rather than
// answered with a figure that means something else.
func TestSummaryRefusesAnUnboundedWindow(t *testing.T) {
	newSummaryFixture(t)
	_, errOut, err := execute(t, "runs", "summary", "--since", "0")
	if err == nil {
		t.Fatal("--since 0 was accepted")
	}
	if !strings.Contains(errOut+err.Error(), "lower bound") {
		t.Errorf("the refusal does not say why: %v (%s)", err, errOut)
	}
}

// TestSummaryRefusesAForeignBudgetDatabase: the live half reads the database's
// own day nodes, so a budget.rtdb_url pointing at another project's database
// would print that project's counts and spend under this project's name — a
// wrong answer, not a missing one. The /fugaro/project mark is the ownership
// question every other reader of the database asks (checkDBProject), and the
// summary asks it too: the refusal names the project the database belongs to
// and prints no figure.
func TestSummaryRefusesAForeignBudgetDatabase(t *testing.T) {
	f := newSummaryFixture(t)
	f.seed(t, summaryRun{day: f.day(0), slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_000_000, seq: 1})
	// The same fixture, with the database marked as another project's.
	f.db.Set(budget.PathProject, "someone-else")

	out, errOut, err := execute(t, "runs", "summary", "--json")
	if err == nil {
		t.Fatalf("runs summary answered over another project's budget database: %s", out)
	}
	if got := exitCode(t, err); got != ExitUserError {
		t.Errorf("the refusal is exit %d, want %d: %v", got, ExitUserError, err)
	}
	if !strings.Contains(errOut+err.Error(), "someone-else") {
		t.Errorf("the refusal does not name the project the database belongs to: %v (%s)", err, errOut)
	}
	if strings.Contains(out, "spent_micros") || strings.Contains(out, "repositories") {
		t.Errorf("the refusal printed figures:\n%s", out)
	}
}

// TestSummaryDocMatchesSchema validates the document the command writes against
// schemas/summary.schema.json, the contract a consumer reads it by (D784).
func TestSummaryDocMatchesSchema(t *testing.T) {
	f := newSummaryFixture(t)
	f.seed(t, summaryRun{day: f.day(0), slug: appSlug, repo: "acme/app", status: "succeeded", spent: 1_000_000, seq: 1})
	_, out := summaryJSON(t)

	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "summary.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/summary.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/summary.schema.json: %v\n%s", err, out)
	}
	// The members a consumer reads without a nil check are written on every
	// run, empty included: a null array would be a shape the schema's own
	// type does not allow and a consumer's loop would crash on.
	for _, key := range []string{"repositories", "days_unknown", "notes"} {
		if !strings.Contains(out, `"`+key+`": []`) && !strings.Contains(out, `"`+key+`": [`) {
			t.Errorf("the document carries no %s array:\n%s", key, out)
		}
	}
}
