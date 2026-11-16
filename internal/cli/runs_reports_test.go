package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// The project's reports job (E1): one run of the project's own Cloud Run job
// writes every repository's closed periods into reports/ of the runs bucket,
// from a spec in its environment and from nothing else.

// reportsBucket is the runs bucket the fixture's job writes into.
const reportsBucket = "fugaro-runs-proj-1234"

// reportsJob is the job's world: the runs bucket, the document its environment
// carries, and the repository slugs that document names.
type reportsJob struct {
	gcs   *gcpfake.GCS
	doc   infra.ReportsJobDoc
	slugs map[string]string // repository -> slug
}

// newReportsJob installs the project's reports job for repos, closing periods
// of the kinds given: the environment fugaro update sets on it, over a GCS
// fake.
func newReportsJob(t *testing.T, repos []string, periods ...string) *reportsJob {
	t.Helper()
	f := &reportsJob{gcs: gcpfake.NewGCS(t), slugs: map[string]string{}}
	doc := infra.ReportsJobDoc{
		Version: infra.ReportsJobDocVersion, Project: "proj-1234", Bucket: reportsBucket, Periods: periods,
	}
	for _, repo := range repos {
		slug := mustSlug("github", repo)
		doc.Repositories = append(doc.Repositories, slug)
		f.slugs[repo] = slug
	}
	f.doc = doc

	old := reportsOpenBucket
	t.Cleanup(func() { reportsOpenBucket = old })
	reportsOpenBucket = func(_ context.Context, url string) (*blobx.Bucket, error) {
		if url != "gs://"+reportsBucket {
			t.Errorf("the reports job opened %s, want the runs bucket", url)
		}
		return f.gcs.Bucket(t, reportsBucket), nil
	}
	t.Setenv("CLOUD_RUN_JOB", infra.ReportsJobName)
	t.Setenv("CLOUD_RUN_EXECUTION", infra.ReportsJobName+"-abc12")
	f.setSpec(t, doc)
	return f
}

// setSpec writes the document into the job's environment, as the installation
// renders it.
func (f *reportsJob) setSpec(t *testing.T, doc infra.ReportsJobDoc) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(reportsJobSpecEnv, string(data))
}

// seed writes one finished run of repo into the bucket, under the repository's
// own slug prefix.
func (f *reportsJob) seed(t *testing.T, repo, id string, fin time.Time) {
	t.Helper()
	f.seedUnder(t, repo, repo, id, fin)
}

// seedUnder writes one finished run under prefix's own slug whose record names
// repo, which may be another repository or none at all: a record's repo field is
// a word the record chose, and G2 is exactly that only the records deriving the
// slug whose prefix they were found under are that repository's.
func (f *reportsJob) seedUnder(t *testing.T, prefix, repo, id string, fin time.Time) {
	t.Helper()
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: repo, Workflow: "web",
		Status:    runstore.StatusSucceeded,
		StartedAt: fin.Add(-time.Hour), FinishedAt: &fin,
	}
	if err := runstore.Open(f.gcs.Bucket(t, reportsBucket).Bucket, f.slugs[prefix], id).WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// put stores data at key directly, for a report the fixture's job did not
// write: a stored report a previous run left, or one a person overwrote.
func (f *reportsJob) put(t *testing.T, key string, data []byte) {
	t.Helper()
	f.gcs.Put(reportsBucket, key, data)
}

// object is the bytes of the object at key, and false when there is none.
func (f *reportsJob) object(t *testing.T, key string) ([]byte, int64, bool) {
	t.Helper()
	data, gen, err := f.gcs.Bucket(t, reportsBucket).Read(context.Background(), key)
	if errors.Is(err, blobx.ErrNotExist) {
		return nil, 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return data, gen, true
}

// reportKey is one repository's report of one period, where the layout puts it.
func (f *reportsJob) reportKey(repo, period string) string {
	return "reports/" + f.slugs[repo] + "/" + digest.PeriodPath(period)
}

// aggregateKey is the project's own report of one period: the layout's own path
// with no slug in it, which is the document R8 adds beside the repositories'.
func (f *reportsJob) aggregateKey(period string) string {
	return "reports/" + digest.PeriodPath(period)
}

// seedHalt writes one halted run of repo into the bucket: a record carrying a
// halt reason raises the period's halt flag, which is a flag the rules read.
func (f *reportsJob) seedHalt(t *testing.T, repo, id string, fin time.Time) {
	t.Helper()
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: repo, Workflow: "web",
		Status:    runstore.StatusHalted,
		Halt:      &runstore.Halt{Reason: runstore.HaltBudgetUnavailable, At: fin},
		StartedAt: fin.Add(-time.Hour), FinishedAt: &fin,
	}
	if err := runstore.Open(f.gcs.Bucket(t, reportsBucket).Bucket, f.slugs[repo], id).WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// launch writes one run's launch and task records: the identity its launcher
// recorded and the task it was handed. Both sit in the bucket beside the
// records a report is built from, and neither is a report's own (G1, D323).
func (f *reportsJob) launch(t *testing.T, repo, id, by, taskText string) {
	t.Helper()
	store := runstore.Open(f.gcs.Bucket(t, reportsBucket).Bucket, f.slugs[repo], id)
	ctx := context.Background()
	if err := store.WriteLaunch(ctx, &runstore.Launch{LaunchedBy: by, LaunchedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: repo, Task: taskText}); err != nil {
		t.Fatal(err)
	}
}

// putAggregate stores one project aggregate carrying s's summary block, as a
// run before this one would have left it: the stored series a year's aggregate
// adds up, and the series the rules would read if an aggregate rendered them.
// It returns the block as a reader of the stored object gets it back.
func (f *reportsJob) putAggregate(t *testing.T, dim digest.Dim, period string, s digest.Summary) digest.Summary {
	t.Helper()
	return f.putBlock(t, f.aggregateKey(period), dim, period, s)
}

// putBlock is putAggregate at any path: one stored report whose whole body is
// s's summary block, which is all a later reader of a series reads.
func (f *reportsJob) putBlock(t *testing.T, key string, dim digest.Dim, period string, s digest.Summary) digest.Summary {
	t.Helper()
	p, err := digest.PeriodOfKey(dim, period)
	if err != nil {
		t.Fatal(err)
	}
	s.Period, s.Start, s.End = p.Key, p.Start, p.End
	block, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	file := []byte("```json\n" + string(block) + "\n```\n")
	f.put(t, key, file)
	got, err := digest.ReadSummary(file)
	if err != nil {
		t.Fatalf("the fixture's own report at %s is not one this package reads: %v", key, err)
	}
	return got
}

// sums is a fixture summary this package reads back: the counts a test sets,
// and the figures a block must carry to be one at all.
func sums(runs int, flags map[string]int) digest.Summary {
	return digest.Summary{
		Runs: runs, Shown: runs, Billed: "1.000000", Notional: "2.000000", Review: "0.000000",
		Flags: flags,
	}
}

// TestRunsReportsWritesEachRepositorysReportIntoTheBucket is the headline: one
// run of the job writes each repository's report of the closed period into its
// own prefix of the runs bucket, at the layout's path, from the records under
// that prefix and from nothing else.
func TestRunsReportsWritesEachRepositorysReportIntoTheBucket(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app", "acme/other"}, string(digest.ByDay))
	digestClock(t, "2026-11-02") // the closed day is 2026-11-01
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	f.seed(t, "acme/other", "20261101-100000-bbbb", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	f.seed(t, "acme/other", "20261101-110000-cccc", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC))

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	for _, repo := range []string{"acme/app", "acme/other"} {
		key := f.reportKey(repo, "2026-11-01")
		if !strings.Contains(out, "wrote "+key+"\n") {
			t.Errorf("the run printed no line for %s: %q", key, out)
		}
		data, _, ok := f.object(t, key)
		if !ok {
			t.Fatalf("the run wrote no report at %s", key)
		}
		body := string(data)
		if !strings.Contains(body, "# Run digest: 2026-11-01") {
			t.Errorf("%s does not hold the day's report:\n%s", key, body)
		}
		// The figures are the records under the repository's own prefix, so the
		// two repositories' reports are not the same one written twice.
		want := 1
		if repo == "acme/other" {
			want = 2
		}
		if !strings.Contains(body, `"runs":`+itoa(want)) {
			t.Errorf("%s holds other than the %d runs of %s:\n%s", key, want, repo, body)
		}
	}
	// The project's own document of the period is written beside them, after
	// the loop: which repositories it names and what it adds up is the
	// aggregate's own test.
	if _, _, ok := f.object(t, f.aggregateKey("2026-11-01")); !ok {
		t.Errorf("the run wrote no project aggregate: %q", out)
	}
}

// TestRunsReportsRefusesToStartWithoutItsSpec: the job's whole assignment is
// the document in its environment, and a run without one refuses rather than
// falling back to a checkout's own config -- the job has no checkout, and a
// fallback would write reports into a bucket nobody named.
func TestRunsReportsRefusesToStartWithoutItsSpec(t *testing.T) {
	// A checkout and a local config are both in reach here, which is the whole
	// point: the command must not read either.
	digestCheckout(t)
	fixture := newCloudFixture(t)
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByDay))
	t.Setenv(reportsJobSpecEnv, "")

	_, _, err := execute(t, "runs", "reports", "--job")
	if err == nil {
		t.Fatal("the run started without its spec, and it has no assignment to fall back on")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal is exit %d, want the user error %d: %v", ExitCode(err), ExitUserError, err)
	}
	if !strings.Contains(err.Error(), reportsJobSpecEnv) {
		t.Errorf("the refusal does not name %s: %v", reportsJobSpecEnv, err)
	}
	// Nothing was written, anywhere: not into the job's own bucket, and not
	// into the checkout's config's bucket, which is where a fallback would have
	// written. The fixture's bucket holds its project marker and nothing else.
	if _, _, ok := f.object(t, f.reportKey("acme/app", "2026-11-01")); ok {
		t.Error("the refused run wrote a report into the job's bucket")
	}
	root := strings.TrimPrefix(fixture.bucket, "file://")
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(filepath.ToSlash(path), "/reports/") {
			t.Errorf("the refused run wrote %s: it fell back to the checkout's own config", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRunsReportsSkipsAnIdenticalStoredReport: a rerun of one night is
// harmless -- a report that is already stored and equal is left exactly as it
// is, so a re-fired schedule writes no object at all.
func TestRunsReportsSkipsAnIdenticalStoredReport(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByDay))
	digestClock(t, "2026-11-02")
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))

	if _, _, err := execute(t, "runs", "reports", "--job"); err != nil {
		t.Fatal(err)
	}
	key := f.reportKey("acme/app", "2026-11-01")
	first, gen, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the first run wrote no report at %s", key)
	}

	out, _, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatal(err)
	}
	second, gen2, _ := f.object(t, key)
	if gen2 != gen {
		t.Errorf("the second run wrote %s again (generation %d -> %d), and its bytes were already there", key, gen, gen2)
	}
	if string(second) != string(first) {
		t.Errorf("the second run changed the report's bytes:\n%s", second)
	}
	if !strings.Contains(out, "unchanged "+key+"\n") {
		t.Errorf("the second run did not say the report was left as it is: %q", out)
	}
}

// TestRunsReportsOverwritesAReportWhosePeriodChanged: a stored report that is
// not the one this run computes is overwritten, so a period whose records
// changed -- or a file someone overwrote by hand -- is repaired.
func TestRunsReportsOverwritesAReportWhosePeriodChanged(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByDay))
	digestClock(t, "2026-11-02")
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	key := f.reportKey("acme/app", "2026-11-01")
	f.put(t, key, []byte("<!-- hand-written, and not a report -->\n"))

	out, _, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatal(err)
	}
	data, _, ok := f.object(t, key)
	if !ok {
		t.Fatalf("no report at %s", key)
	}
	if strings.Contains(string(data), "hand-written") {
		t.Errorf("the run left the stored report as it was:\n%s", data)
	}
	if !strings.Contains(string(data), "# Run digest: 2026-11-01") || !strings.Contains(out, "wrote "+key+"\n") {
		t.Errorf("the run did not overwrite %s with the period's report:\n%s\n%q", key, data, out)
	}
	// The overwritten report is one this package reads back: the series the
	// next period's rules read is not left with a file nobody can read.
	if _, err := digest.ReadSummary(data); err != nil {
		t.Errorf("the report the run wrote is not one a reader can read: %v", err)
	}
}

// TestRunsReportsWritesTheYearFromTheStoredMonths: a yearly report has no runs
// of its own to read -- the records are kept 90 days -- so it is the stored
// monthly reports of that year added up, and the year says how many of the
// twelve it added up.
func TestRunsReportsWritesTheYearFromTheStoredMonths(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByYear))
	digestClock(t, "2027-01-01") // the closed year is 2026
	for i, month := range []struct {
		key  string
		runs int
	}{{"2026-01", 2}, {"2026-02", 3}} {
		p, err := digest.NewPeriod(digest.ByMonth, month.key+"-01")
		if err != nil {
			t.Fatal(err)
		}
		recs := make([]runstore.Record, 0, month.runs)
		for n := 0; n < month.runs; n++ {
			fin := time.Date(2026, time.Month(i+1), 10+n, 9, 0, 0, 0, time.UTC)
			recs = append(recs, runstore.Record{
				Version: 1, RunID: fmt.Sprintf("%s%02d-0900%02d-abcd", month.key[:4]+month.key[5:], 10+n, i), Repo: "acme/app", Workflow: "web",
				Status: runstore.StatusSucceeded, StartedAt: fin.Add(-time.Hour), FinishedAt: &fin,
			})
		}
		d := digest.Build(recs, p)
		f.put(t, f.reportKey("acme/app", p.Key), digest.FileBody(digest.Markdown(d), nil, p, d.Summary()))
	}

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	key := f.reportKey("acme/app", "2026")
	data, _, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the run wrote no yearly report at %s: %q", key, out)
	}
	body := string(data)
	if !strings.Contains(body, "A yearly report is the monthly reports of 2026") {
		t.Errorf("%s is not a yearly report:\n%s", key, body)
	}
	// Two of the twelve months are stored and they hold five runs between
	// them: the year is their sum, and it says how many months it added up.
	if !strings.Contains(body, "2 of the year's twelve are committed, and they hold 5 runs in all") {
		t.Errorf("%s does not roll the stored months up:\n%s", key, body)
	}
}

// TestRunsReportsRefusesToRunOutsideItsJob: the job is the one writer of
// reports/ in the project, so the command that writes them runs as the job and
// nowhere else.
func TestRunsReportsRefusesToRunOutsideItsJob(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByDay))
	t.Setenv("CLOUD_RUN_JOB", "")
	t.Setenv("CLOUD_RUN_EXECUTION", "")

	_, _, err := execute(t, "runs", "reports", "--job")
	if err == nil {
		t.Fatal("the run started outside a Cloud Run job")
	}
	if !strings.Contains(err.Error(), "CLOUD_RUN_JOB") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
	if _, _, ok := f.object(t, f.reportKey("acme/app", "2026-11-01")); ok {
		t.Error("the refused run wrote a report")
	}

	// Without --job there is nothing to report at all: the command is the
	// job's, and a person's own report of one period is runs digest's.
	_, _, err = execute(t, "runs", "reports")
	if err == nil || !strings.Contains(err.Error(), "runs digest") {
		t.Errorf("fugaro runs reports without --job: err = %v, want a refusal naming runs digest", err)
	}
}

// TestRunsReportsCountsOnlyTheRepositorysOwnRecords: a report is built from the
// records that are the repository's own, and which records those are is decided
// by the slug they were found under rather than by the word a record chose for
// itself (G2). A record under a repository's prefix that names another
// repository, or none at all, is in no report of that prefix -- and the
// repository it does name still gets its own, since the guard drops records and
// not repositories.
func TestRunsReportsCountsOnlyTheRepositorysOwnRecords(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app", "acme/other"}, string(digest.ByDay))
	digestClock(t, "2026-11-02")
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	// Both of these sit under acme/app's own prefix and are not acme/app's: one
	// names another repository, the other names no repository at all.
	f.seedUnder(t, "acme/app", "acme/other", "20261101-100000-bbbb", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	f.seedUnder(t, "acme/app", "", "20261101-110000-cccc", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC))
	// The repository the foreign record names has its own prefix and its own
	// record: the guard drops records, and does not leave a repository out.
	f.seed(t, "acme/other", "20261101-120000-dddd", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	for repo, want := range map[string]int{"acme/app": 1, "acme/other": 1} {
		key := f.reportKey(repo, "2026-11-01")
		data, _, ok := f.object(t, key)
		if !ok {
			t.Fatalf("the run wrote no report at %s: %q", key, out)
		}
		summary, err := digest.ReadSummary(data)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Runs != want {
			t.Errorf("%s counts %d runs, want %d: only the records deriving the slug they were found under are the repository's", key, summary.Runs, want)
		}
	}
}

// TestRunsReportsRefusesASpecItCannotActOn: the document is the whole
// assignment, so every way it can fail to be one is a refusal that writes
// nothing -- a document that does not decode, one that is incomplete, a bucket
// name the URL this run opens cannot carry, a repository entry that is not one
// path segment (the one piece of it that becomes a key), and a period kind this
// build does not know. A refusal is the user error with the fix named in it, not
// a run that reports some of what the document asked for.
func TestRunsReportsRefusesASpecItCannotActOn(t *testing.T) {
	valid := func() infra.ReportsJobDoc {
		return infra.ReportsJobDoc{
			Version: infra.ReportsJobDocVersion, Project: "proj-1234", Bucket: reportsBucket,
			Repositories: []string{mustSlug("github", "acme/app")},
			Periods:      []string{string(digest.ByDay)},
		}
	}
	for _, tc := range []struct {
		name string
		doc  func(*infra.ReportsJobDoc)
		raw  string
		want string
	}{
		{name: "a document that does not decode", raw: "{not json at all", want: "FUGARO_REPORTS_JOB_SPEC"},
		{name: "no version", doc: func(d *infra.ReportsJobDoc) { d.Version = 0 }, want: "incomplete"},
		{name: "no project", doc: func(d *infra.ReportsJobDoc) { d.Project = "" }, want: "incomplete"},
		{name: "no bucket", doc: func(d *infra.ReportsJobDoc) { d.Bucket = "" }, want: "incomplete"},
		{name: "no repository", doc: func(d *infra.ReportsJobDoc) { d.Repositories = nil }, want: "incomplete"},
		{name: "no period kind", doc: func(d *infra.ReportsJobDoc) { d.Periods = nil }, want: "incomplete"},
		{name: "a bucket name that is not one", doc: func(d *infra.ReportsJobDoc) { d.Bucket = "NOT A BUCKET" }, want: "NOT A BUCKET"},
		{name: "an empty repository entry", doc: func(d *infra.ReportsJobDoc) { d.Repositories = append(d.Repositories, "") }, want: "not one repository's prefix"},
		{name: "a repository entry that is a path", doc: func(d *infra.ReportsJobDoc) { d.Repositories = []string{"acme/app"} }, want: "not one repository's prefix"},
		{name: "a repository entry that climbs", doc: func(d *infra.ReportsJobDoc) { d.Repositories = []string{".."} }, want: "not one repository's prefix"},
		{name: "a repository entry carrying a backslash", doc: func(d *infra.ReportsJobDoc) { d.Repositories = []string{`acme\app`} }, want: "not one repository's prefix"},
		{name: "a period kind this build does not know", doc: func(d *infra.ReportsJobDoc) { d.Periods = []string{"fortnight"} }, want: "fortnight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReportsJob(t, []string{"acme/app"}, string(digest.ByDay))
			digestClock(t, "2026-11-02")
			doc := valid()
			if tc.doc != nil {
				tc.doc(&doc)
			}
			if tc.raw != "" {
				t.Setenv(reportsJobSpecEnv, tc.raw)
			} else {
				f.setSpec(t, doc)
			}

			out, _, err := execute(t, "runs", "reports", "--job")
			if err == nil {
				t.Fatalf("the run accepted a document it cannot act on and printed %q", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("the refusal is exit %d, want the user error %d: %v", ExitCode(err), ExitUserError, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if _, _, ok := f.object(t, f.reportKey("acme/app", "2026-11-01")); ok {
				t.Error("the refused run wrote a report")
			}
		})
	}
}

// TestReportsJobSpecRoundTripsThroughItsReader: the document the installation
// renders into the job's environment is the document that job reads. Both
// halves declare the shape themselves -- internal/infra renders it and
// readReportsJobEnv parses it -- so this round trip is the only thing that
// keeps the two in step, and without it a scheduled run refuses to start on
// bytes the installation wrote.
func TestReportsJobSpecRoundTripsThroughItsReader(t *testing.T) {
	lc, err := localcfg.Parse([]byte(catalogProducerLocal))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := infra.Installation(lc, infra.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vars, err := infra.InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Reports *infra.ReportsJobSpec `json:"reports"`
	}
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Reports == nil {
		t.Fatal("the installation tfvars carry no reports job, and this installation records a base image to run it FROM: nothing would write the stored reports at all")
	}
	raw := doc.Reports.Env[infra.ReportsJobDocEnv]
	if raw == "" {
		t.Fatalf("the reports job's environment carries no %s: the job refuses to start without it", infra.ReportsJobDocEnv)
	}
	t.Setenv(reportsJobSpecEnv, raw)

	got, err := readReportsJobEnv()
	if err != nil {
		t.Fatalf("the installed reports job would refuse the document this installation writes: %v", err)
	}
	if got.Version != infra.ReportsJobDocVersion {
		t.Fatalf("the reader read version %d, and this release's document is version %d (infra.ReportsJobDocVersion): an installation and the job it deploys disagree about the bytes between them", got.Version, infra.ReportsJobDocVersion)
	}
	if got.Project != lc.GCPProject || got.Bucket != lc.RunsBucket {
		t.Errorf("the reader read project %q and bucket %q, want the installation's %q and %q", got.Project, got.Bucket, lc.GCPProject, lc.RunsBucket)
	}
	// Every repository the installation records a provider for is in the
	// document, by the slug this build derives for it -- which is what the
	// job's own per-repository guard reads the records back with.
	want := []string{mustSlug("bitbucket", "acme/app"), mustSlug("github", "acme/other")}
	if strings.Join(got.Repositories, ",") != strings.Join(want, ",") {
		t.Errorf("the reader read the repositories %v, want %v", got.Repositories, want)
	}
	if len(got.Periods) == 0 {
		t.Error("the document names no period to close")
	}
	for _, kind := range got.Periods {
		if dim, err := digest.ParseDim(kind); err != nil || !dim.IsPeriod() {
			t.Errorf("the document names %q as a period, which is not one this reader knows", kind)
		}
	}
}

// TestReportsSinceIsTheReleaseThatShipsTheCommand pins the constant a skill or
// recipe that names fugaro runs reports is gated at: the release that ships it,
// which is not digestSince's -- the two are the two halves of one series and
// neither implies the other. It is the release after the 0.25.0 cut: v0.25.0 is
// tagged with no internal/cli/runs_reports.go in its tree, and its own notes
// say the command the reports job runs lands in a later release.
func TestReportsSinceIsTheReleaseThatShipsTheCommand(t *testing.T) {
	if reportsSince != "0.26.0" {
		t.Errorf("reportsSince = %q, want 0.26.0, the release the reports job's command ships in -- not 0.25.0, which is tagged with no such command in it", reportsSince)
	}
	if reportsSince == digestSince {
		t.Errorf("reportsSince = digestSince = %q: one constant for the job's command and the digest step, so a release can no longer ship one without the other", reportsSince)
	}
	if !cmdAnswers(t, "runs", "reports") {
		t.Error("this build carries no fugaro runs reports, and reportsSince names the release that ships it")
	}
}

// cmdAnswers is whether this build's command tree answers the command at path.
func cmdAnswers(t *testing.T, path ...string) bool {
	t.Helper()
	found, _, err := NewRootCmd().Find(path)
	return err == nil && found != nil && found.Name() == path[len(path)-1]
}

// The project's own report (R8): one document per period beside the
// repositories' reports, which names every repository of the document and adds
// the figures of their stored reports up, and names no rule of its own (E2,
// E7).

// TestRunsReportsWritesTheProjectAggregateBesideTheRepositoryReports: the run's
// own step after the loop is the period's document at reports/<YYYY>/<key>.md
// -- D321's path with no slug in it -- naming every repository the document
// names and carrying their runs added up.
func TestRunsReportsWritesTheProjectAggregateBesideTheRepositoryReports(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app", "acme/other"}, string(digest.ByDay))
	digestClock(t, "2026-11-02") // the closed day is 2026-11-01
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	f.seed(t, "acme/other", "20261101-100000-bbbb", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	f.seed(t, "acme/other", "20261101-110000-cccc", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC))
	// A record of a repository the document does not name, who ran there and
	// how much: what the aggregate adds up is the document's repositories and
	// never whatever the bucket happens to hold (D1433, D1448).
	f.slugs["acme/elsewhere"] = mustSlug("github", "acme/elsewhere")
	f.seed(t, "acme/elsewhere", "20261101-120000-dddd", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	key := f.aggregateKey("2026-11-01")
	if !strings.Contains(out, "wrote "+key+"\n") {
		t.Errorf("the run printed no line for %s: %q", key, out)
	}
	data, _, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the run wrote no project aggregate at %s: %q", key, out)
	}
	body := string(data)
	// The header names the period the report closed (D1434).
	if !strings.Contains(body, "# Project digest: 2026-11-01") {
		t.Errorf("%s does not name the period it closed:\n%s", key, body)
	}
	// One line per repository, with that repository's own runs: the two are not
	// the same line twice, so the figures are the reports read back rather than
	// a total divided up.
	for repo, want := range map[string]int{"acme/app": 1, "acme/other": 2} {
		if !strings.Contains(body, "| "+f.slugs[repo]+" | "+itoa(want)+" |") {
			t.Errorf("%s does not carry %s's own %d runs:\n%s", key, repo, want, body)
		}
	}
	summary, err := digest.ReadSummary(data)
	if err != nil {
		t.Fatalf("the aggregate is not one this package reads back: %v", err)
	}
	if summary.Period != "2026-11-01" {
		t.Errorf("the aggregate's block says the period is %q, want 2026-11-01", summary.Period)
	}
	if summary.Runs != 3 {
		t.Errorf("the aggregate counts %d runs, want the 3 of the two repositories of the document:\n%s", summary.Runs, body)
	}
	// The reports it added up are beside it: the aggregate is written after
	// them and is no substitute for one of them.
	for _, repo := range []string{"acme/app", "acme/other"} {
		if _, _, ok := f.object(t, f.reportKey(repo, "2026-11-01")); !ok {
			t.Errorf("the run wrote no report for %s, and the aggregate names it as one of the period's", repo)
		}
	}
}

// TestTheAggregateNamesEveryRepositoryAndNoPerson: the one document that names
// every repository names nothing else -- no person, no address, no task's own
// words (E2, G1, D323). The records the period's reports are built from carry
// all three.
func TestTheAggregateNamesEveryRepositoryAndNoPerson(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app", "acme/other"}, string(digest.ByDay))
	digestClock(t, "2026-11-02")
	for i, repo := range []string{"acme/app", "acme/other"} {
		id := fmt.Sprintf("20261101-1%d0000-abcd", i)
		fin := time.Date(2026, 11, 1, 10+i, 0, 0, 0, time.UTC)
		f.seed(t, repo, id, fin)
		f.launch(t, repo, id, "ada@example.com", taskTextFor(repo))
	}

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	data, _, ok := f.object(t, f.aggregateKey("2026-11-01"))
	if !ok {
		t.Fatalf("the run wrote no project aggregate: %q", out)
	}
	body := string(data)
	for _, repo := range []string{"acme/app", "acme/other"} {
		if !strings.Contains(body, f.slugs[repo]) {
			t.Errorf("the aggregate does not name %s, and naming every repository is the one thing it adds:\n%s", repo, body)
		}
	}
	for _, leak := range []string{"ada@example.com", "grace@example.com", "@", "flux capacitor", "wm_gadget"} {
		if strings.Contains(body, leak) {
			t.Errorf("the aggregate carries %q, which is a person or a task's own words (G1, D323):\n%s", leak, body)
		}
	}
	// The launch and the task are in the bucket beside the records, which is
	// what makes this the aggregate's own property and not one of a bucket
	// nobody wrote them into.
	if _, _, ok := f.object(t, f.reportKey("acme/app", "2026-11-01")); !ok {
		t.Fatal("no report was written, so the aggregate was built from nothing")
	}
}

// taskTextFor is one repository's task brief for the fixture: two words no
// report may carry, one of them a value of no closed vocabulary.
func taskTextFor(repo string) string {
	if repo == "acme/app" {
		return "# Task: fix the flux capacitor\n\nIt is the one in acme/app.\n"
	}
	return "# Task: rename wm_gadget\n\nIt is the one in acme/other.\n"
}

// TestTheAggregateCarriesNoPatternRules: the aggregate keeps no rule and shows
// no pattern (E7) -- there is no threshold for a series across repositories, so
// the document holds no Patterns section and names no rule. A repository's
// report of the same period is the control: the same series, read by the writer
// that does render the rules.
func TestTheAggregateCarriesNoPatternRules(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByWeek))
	digestClock(t, "2026-11-02")
	closed, err := digest.NewPeriod(digest.ByWeek, "2026-11-01")
	if err != nil {
		t.Fatal(err)
	}
	// The weeks before the closed one, each of them a report of the repository
	// carrying the halt flag: four weeks running with it raised is what the
	// flag_recurs rule fires over.
	var prev []digest.Summary
	for i := 3; i >= 1; i-- {
		back := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -7*i)
		w, err := digest.NewPeriod(digest.ByWeek, back.Format("2006-01-02"))
		if err != nil {
			t.Fatal(err)
		}
		prev = append(prev, f.putBlock(t, f.reportKey("acme/app", w.Key), digest.ByWeek, w.Key, sums(1, map[string]int{digest.FlagHalt: 1})))
	}
	// The closed week's own run, halted: the fourth week of the series.
	f.seedHalt(t, "acme/app", "20261028-090000-aaaa", time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC))

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	key := f.aggregateKey(closed.Key)
	data, _, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the run wrote no project aggregate at %s: %q", key, out)
	}
	repoReport, _, ok := f.object(t, f.reportKey("acme/app", closed.Key))
	if !ok {
		t.Fatalf("the run wrote no report for acme/app: %q", out)
	}
	// The control first, so a series that fires no rule at all fails this test
	// rather than passing it for the wrong reason.
	cur, err := digest.ReadSummary(repoReport)
	if err != nil {
		t.Fatal(err)
	}
	fired := digest.Patterns(prev, cur)
	if len(fired) == 0 {
		t.Fatal("the series beside the aggregate fires no rule, so this test would pass with the rules rendered too")
	}
	if !strings.Contains(string(repoReport), "## Patterns") {
		t.Errorf("the repository's report of %s renders no Patterns section, and its series fires %d rules:\n%s", closed.Key, len(fired), repoReport)
	}
	body := string(data)
	if strings.Contains(body, "## Patterns") || strings.Contains(body, "No pattern rule fired.") {
		t.Errorf("%s carries a Patterns section, and the project's aggregate names no rule (E7):\n%s", key, body)
	}
	for _, rule := range digest.RuleNames {
		if strings.Contains(body, rule) {
			t.Errorf("%s names the rule %s (E7):\n%s", key, rule, body)
		}
	}
}

// TestTheAggregateIsSkippedWhenItIsUnchanged: the aggregate is written by the
// same comparison a repository's report is, so a rerun of one night leaves it
// exactly as it is and writes no object at all.
func TestTheAggregateIsSkippedWhenItIsUnchanged(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app", "acme/other"}, string(digest.ByDay))
	digestClock(t, "2026-11-02")
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	f.seed(t, "acme/other", "20261101-100000-bbbb", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	if _, _, err := execute(t, "runs", "reports", "--job"); err != nil {
		t.Fatal(err)
	}
	key := f.aggregateKey("2026-11-01")
	first, gen, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the first run wrote no aggregate at %s", key)
	}

	out, _, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatal(err)
	}
	second, gen2, _ := f.object(t, key)
	if gen2 != gen {
		t.Errorf("the second run wrote %s again (generation %d -> %d), and its bytes were already there", key, gen, gen2)
	}
	if string(second) != string(first) {
		t.Errorf("the second run changed the aggregate's bytes:\n%s", second)
	}
	if !strings.Contains(out, "unchanged "+key+"\n") {
		t.Errorf("the second run did not say the aggregate was left as it is: %q", out)
	}
}

// TestRunsReportsWritesTheYearAggregateFromTheStoredMonthlyAggregates: a year
// has no runs of its own to read -- the records are kept 90 days -- so the
// project's yearly aggregate is the stored monthly aggregates of that year
// added up, which is the shape a repository's yearly report already has (F24,
// D1443). Which months it added up is the report's own answer, so a partial
// year is not read as a whole one.
func TestRunsReportsWritesTheYearAggregateFromTheStoredMonthlyAggregates(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app"}, string(digest.ByYear))
	digestClock(t, "2027-01-01") // the closed year is 2026
	for _, m := range []struct {
		key  string
		runs int
	}{{"2026-01", 2}, {"2026-02", 3}, {"2027-01", 4}} {
		f.putAggregate(t, digest.ByMonth, m.key, sums(m.runs, nil))
	}

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	key := f.aggregateKey("2026")
	data, _, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the run wrote no yearly project aggregate at %s: %q", key, out)
	}
	body := string(data)
	if !strings.Contains(body, "# Project digest: 2026") || !strings.Contains(body, "A yearly aggregate is the monthly aggregates of 2026") {
		t.Errorf("%s is not the year's aggregate:\n%s", key, body)
	}
	for _, month := range []string{"| 2026-01 | 2 |", "| 2026-02 | 3 |"} {
		if !strings.Contains(body, month) {
			t.Errorf("%s does not carry the stored month %q:\n%s", key, month, body)
		}
	}
	if strings.Contains(body, "2027-01") {
		t.Errorf("%s added up a month of another year:\n%s", key, body)
	}
	summary, err := digest.ReadSummary(data)
	if err != nil {
		t.Fatalf("the yearly aggregate is not one this package reads back: %v", err)
	}
	if summary.Runs != 5 || summary.Months != 2 {
		t.Errorf("the yearly aggregate holds %d runs over %d months, want the stored 5 over 2:\n%s", summary.Runs, summary.Months, body)
	}
}

// TestTheAggregateCarriesThePeriodsFailuresByClass: the aggregate carries the
// period's failures by class beside its figures (D1432) -- the classes the
// repositories' own reports hold, added up. The table has no money column and no
// example run, which is not a rendering choice: an aggregate adds summary blocks
// up, and a block carries a class's runs and nothing else.
func TestTheAggregateCarriesThePeriodsFailuresByClass(t *testing.T) {
	f := newReportsJob(t, []string{"acme/app", "acme/other"}, string(digest.ByDay))
	digestClock(t, "2026-11-02")
	f.seed(t, "acme/app", "20261101-090000-aaaa", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	f.seedHalt(t, "acme/other", "20261101-100000-bbbb", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	f.seedHalt(t, "acme/other", "20261101-110000-cccc", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC))
	// The class a halted run of this reason carries, read the way the report
	// reads it rather than spelled out here.
	class := runview.ClassOf(&runstore.Record{
		Status: runstore.StatusHalted,
		Halt:   &runstore.Halt{Reason: runstore.HaltBudgetUnavailable},
	})
	if class == "" {
		t.Fatal("a halted record of this reason carries no class, so the table this test looks for would hold nothing")
	}

	out, errOut, err := execute(t, "runs", "reports", "--job")
	if err != nil {
		t.Fatalf("fugaro runs reports --job: %v\nstderr: %s", err, errOut)
	}
	key := f.aggregateKey("2026-11-01")
	data, _, ok := f.object(t, key)
	if !ok {
		t.Fatalf("the run wrote no project aggregate at %s: %q", key, out)
	}
	body := string(data)
	if !strings.Contains(body, "## Failures") || !strings.Contains(body, "| class | runs |\n| --- | --- |\n| `"+string(class)+"` | 2 |") {
		t.Errorf("%s does not carry the period's two failures as the class %s:\n%s", key, class, body)
	}
	summary, err := digest.ReadSummary(data)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failures[string(class)] != 2 {
		t.Errorf("the aggregate's block counts %d runs of the class %s, want the 2 in acme/other's report:\n%s", summary.Failures[string(class)], class, body)
	}
	// The class is one a report of the same period holds, so the table is the
	// reports' own classes added up and not a second derivation.
	report, _, ok := f.object(t, f.reportKey("acme/other", "2026-11-01"))
	if !ok {
		t.Fatalf("the run wrote no report for acme/other: %q", out)
	}
	repo, err := digest.ReadSummary(report)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Failures[string(class)] != 2 {
		t.Errorf("acme/other's own report counts %d runs of the class %s, want 2: the aggregate adds the reports up\n%s", repo.Failures[string(class)], class, report)
	}
}
