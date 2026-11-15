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
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: repo, Workflow: "web",
		Status:    runstore.StatusSucceeded,
		StartedAt: fin.Add(-time.Hour), FinishedAt: &fin,
	}
	if err := runstore.Open(f.gcs.Bucket(t, reportsBucket).Bucket, f.slugs[repo], id).WriteRecord(context.Background(), rec); err != nil {
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
	// No aggregate: the project's own document beside these is R8's, a step of
	// its own.
	if _, _, ok := f.object(t, "reports/2026/2026-11-01.md"); ok {
		t.Error("the run wrote a project aggregate, which is not its work")
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
// neither implies the other.
func TestReportsSinceIsTheReleaseThatShipsTheCommand(t *testing.T) {
	if reportsSince != "0.25.0" {
		t.Errorf("reportsSince = %q, want 0.25.0, the release the reports job's command ships in", reportsSince)
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
