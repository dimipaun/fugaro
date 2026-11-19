package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// commitRunID is the run every test here launches into, and the two mirror
// names they hand in: a repository's own report and the project's aggregate
// (digest's mirror shape, <slug>/<YYYY>/<key>.md and <YYYY>/<key>.md).
const commitRunID = "20261119-100000-cr01"

// releaseCommitReports is a job image whose runner reads the reports a task
// hands in: the first release that does (commitReportsSince).
const releaseCommitReports = "ghcr.io/dimipaun/fugaro-base:0.27.0"

func commitDayName() string  { return appSlug + "/2026/2026-10-20.md" }
func commitWeekName() string { return "2026/2026-W43.md" }

// commitReportKey is where the launcher uploads one handed-in report: the
// run's own prefix, "reports/", and the report's mirror-relative name. It is
// the same leaf internal/runner/reports_commit.go reads back
// (Store.Prefix() + digest.ReportsPrefix + "/" + name), which is the whole
// point of the member: one spelling of where a report goes.
func commitReportKey(id, name string) string {
	return "runs/" + appSlug + "/" + id + "/" + digest.ReportsPrefix + "/" + name
}

// commitReportsFixture is this file's common setup: the catalog's
// commit-reports recipe launched on acme/app, from a checkout of that
// repository whose mirror folder holds the caller's own files, with a job
// image whose runner reads the reports a task hands in.
func commitReportsFixture(t *testing.T, folder map[string]string) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseCommitReports)
	files := make(map[string]string, len(folder))
	for name, body := range folder {
		files[projectpolicy.DefaultReportsCommitDir+"/"+name] = body
	}
	appRepoCheckout(t, files)
	return f
}

// inputOf is one report as the task should name it: the size and the sha256
// of the bytes that went up.
func inputOf(name, body string) task.InputFile {
	sum := sha256.Sum256([]byte(body))
	return task.InputFile{Name: name, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
}

// TestRunCommitReportsUploadsTheStoreIntoTheRun is R16's own point (D2754):
// the LAUNCHER reads the project's stored reports with the person's own
// credential and hands them to the commit run by uploading each object under
// the run's own prefix, so no job account ever reads reports/ and the reports
// job stays its one writer.
//
// The bytes that go up are the STORE's and never the checkout's: the folder
// holds a different report of the same name, and what the run receives is
// what the store says — the run compares again against the clone it makes, so
// the store's bytes are what reach the branch.
//
// Mutation (read the folder's copy instead of the object's): the first
// assertion fails on the checkout's words.
func TestRunCommitReportsUploadsTheStoreIntoTheRun(t *testing.T) {
	day, week := commitDayName(), commitWeekName()
	f := commitReportsFixture(t, map[string]string{day: "the checkout's stale copy of the day"})
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "the store's own day report")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+week, "the project's weekly aggregate")
	// A file a person put in the store, and the index a writer leaves beside
	// the reports: neither is a report of the mirror's shape, and neither is
	// anything a run could write into a folder (digest.MirrorRows reads the
	// folder the same way).
	writeBucketFile(t, f, digest.ReportsPrefix+"/notes.md", "a file a person put in the store")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+appSlug+"/2026/README.md", "an index is not a report")

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "commit-reports",
		"--run-id", commitRunID, "--commit-reports", "Commit the stored reports"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the commit run to have started", n)
	}
	if got := string(object(t, f, commitReportKey(commitRunID, day))); got != "the store's own day report" {
		t.Fatalf("the day report the run was handed = %q, want the store's bytes rather than the checkout's", got)
	}
	if got := string(object(t, f, commitReportKey(commitRunID, week))); got != "the project's weekly aggregate" {
		t.Fatalf("the aggregate the run was handed = %q", got)
	}
	for _, name := range []string{"notes.md", appSlug + "/2026/README.md"} {
		if hasObject(t, f, commitReportKey(commitRunID, name)) {
			t.Fatalf("%s is no report of the mirror's shape and was handed in anyway", name)
		}
	}
}

// TestRunCommitReportsHandsInOnlyWhatTheFolderLacks: the comparison is a
// transfer bound and never a decision. A report the checkout's folder already
// holds byte for byte is not uploaded at all (the run needs no copy of what
// is already committed), and one the folder does not hold — or holds with
// different bytes — is.
//
// Mutation (hand in every listed object): the identical report is uploaded
// and the second half of this test fails on the object. Mutation (compare the
// name alone, without the bytes): a report the store has replaced is left out
// and the first half fails.
func TestRunCommitReportsHandsInOnlyWhatTheFolderLacks(t *testing.T) {
	day, week := commitDayName(), commitWeekName()
	month := appSlug + "/2026/2026-10.md"
	f := commitReportsFixture(t, map[string]string{
		day:   "the same bytes in both places",
		month: "the checkout's stale month",
	})
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "the same bytes in both places")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+week, "the aggregate the folder does not hold")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+month, "the store's newer month")

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "commit-reports",
		"--run-id", commitRunID, "--commit-reports", "Commit the stored reports"); err != nil {
		t.Fatal(err)
	}
	if hasObject(t, f, commitReportKey(commitRunID, day)) {
		t.Fatal("a report the folder already holds byte for byte was handed in")
	}
	for name, body := range map[string]string{week: "the aggregate the folder does not hold", month: "the store's newer month"} {
		if got := string(object(t, f, commitReportKey(commitRunID, name))); got != body {
			t.Fatalf("%s = %q, want %q", name, got, body)
		}
	}
	spec := readSpec(t, f, commitRunID)
	want := []task.InputFile{inputOf(month, "the store's newer month"), inputOf(week, "the aggregate the folder does not hold")}
	if len(spec.Reports) != len(want) {
		t.Fatalf("the task names %+v, want the two reports the folder lacks", spec.Reports)
	}
	for i := range want {
		if spec.Reports[i] != want[i] {
			t.Fatalf("task report %d = %+v, want %+v", i, spec.Reports[i], want[i])
		}
	}
}

// TestRunCommitReportsNamesEveryObjectInTheTask: what the task carries is the
// launcher's own account of the bytes it wrote — the name, the size and the
// sha256 of the object that went up, in name order, which is the order the
// runner's step reads them in. A task that named a report whose object is not
// in the bucket, or named it with a size or a hash that is not what is there,
// is a run that fails at bootstrap with the bytes already up, which is the
// failure the member's shape exists to make impossible.
//
// Mutation (write spec.Reports before the upload, from the folder's files):
// the size and the hash are the checkout's and this test fails on both.
func TestRunCommitReportsNamesEveryObjectInTheTask(t *testing.T) {
	day, week := commitDayName(), commitWeekName()
	f := commitReportsFixture(t, nil)
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+week, "the week's aggregate")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "the day's report")

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "commit-reports",
		"--run-id", commitRunID, "--commit-reports", "Commit the stored reports"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, commitRunID)
	want := []task.InputFile{inputOf(day, "the day's report"), inputOf(week, "the week's aggregate")}
	if len(spec.Reports) != len(want) {
		t.Fatalf("task reports = %+v, want %+v", spec.Reports, want)
	}
	for i := range want {
		if spec.Reports[i] != want[i] {
			t.Fatalf("task report %d = %+v, want %+v", i, spec.Reports[i], want[i])
		}
	}
	// The step's own rule, read from the task's side: every name the task
	// carries is the leaf of an object that is really in the run's prefix.
	for _, rf := range spec.Reports {
		if got := len(object(t, f, commitReportKey(commitRunID, rf.Name))); int64(got) != rf.Bytes {
			t.Fatalf("object %s = %d bytes, and the task names %d", rf.Name, got, rf.Bytes)
		}
	}
}

// TestRunCommitReportsRefusesTheFlagOnAnotherRecipe: --commit-reports hands
// the project's stored reports to the run, and the run's step is the only
// reader of them — a task carrying them under a recipe that never commits one
// names objects nothing reads (task.Spec.Validate's own rule). So the refusal
// is the launcher's, made before the first byte: the object is not uploaded
// at all.
//
// Mutation (drop checkCommitReportsRecipe): the launch uploads a report for a
// recipe with no commit_reports step and this test fails on the missing
// refusal and on the object.
func TestRunCommitReportsRefusesTheFlagOnAnotherRecipe(t *testing.T) {
	day := commitDayName()
	f := commitReportsFixture(t, nil)
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "a stored report")
	wantRefused(t, f, "--commit-reports hands the project's stored reports to the run, and recipe default has no commit_reports step",
		"run", "--repo", "acme/app", "--recipe", "default", "--run-id", commitRunID, "--commit-reports", "Commit the stored reports")
	if hasObject(t, f, commitReportKey(commitRunID, day)) {
		t.Fatal("a refused launch handed a report in anyway")
	}
}

// TestRunCommitReportsRefusesAnExistingRunIdBeforeUploading is --attach's own
// freshness rule, read for the reports: a run id whose task.json is already
// there is refused BEFORE the first byte, whether the stored task differs
// from this one or is byte-identical to it. --commit-reports does not adopt:
// the stored task already names its reports and those objects are already in
// the bucket, so the upload this launch would do is a create-only write
// against objects that are there — a failure, not a no-op — and a mistyped
// --run-id must not have written over a finished run's reports before anyone
// looked at its task.
//
// Mutation (drop commitReportsRefusesStoredRun from finishLaunch): the upload
// runs first and this test fails on the stored object's bytes.
func TestRunCommitReportsRefusesAnExistingRunIdBeforeUploading(t *testing.T) {
	day := commitDayName()
	f := commitReportsFixture(t, nil)
	writeBuildRecord(t, f, appSlug, "web", releaseCommitReports)
	writeBucketFile(t, f, taskKey(commitRunID), `{"version":1,"run_id":"`+commitRunID+`","repo":"acme/app","ref":"main","workflow":"web","task":"a different task"}`)
	writeBucketFile(t, f, commitReportKey(commitRunID, day), "the report a finished run already holds")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "the store's newer bytes")
	wantRefused(t, f, "already holds a task", "run", "--repo", "acme/app", "--recipe", "commit-reports",
		"--run-id", commitRunID, "--commit-reports", "Commit the stored reports")
	if got := string(object(t, f, commitReportKey(commitRunID, day))); got != "the report a finished run already holds" {
		t.Fatalf("the finished run's report was overwritten: %q", got)
	}
}
