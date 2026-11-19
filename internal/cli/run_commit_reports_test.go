package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// commitRunID is the run every test here launches into, and the two mirror
// names they hand in: a repository's own report and the project's aggregate
// (digest's mirror shape, <slug>/<YYYY>/<key>.md and <YYYY>/<key>.md).
const commitRunID = "20261119-100000-c4a1"

// releaseCommitReports is a job image whose runner reads the reports a task
// hands in: the first release that does (commitReportsSince).
const releaseCommitReports = "ghcr.io/dimipaun/fugaro-base:" + commitReportsSince

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

// inNameOrder is the reports a launch should hand in, in the order the task
// carries them: by name, so two launches of one store write one task. The
// order is the names' own and not the order the test listed them in — the
// project's aggregate (`<YYYY>/<key>.md`) sorts before a repository's own
// report (`<slug>/<YYYY>/<key>.md`) because a digit sorts before a letter.
func inNameOrder(files ...task.InputFile) []task.InputFile {
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
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

	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "commit-reports",
		"--run-id", commitRunID, "--commit-reports", "Commit the stored reports")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the commit run to have started", n)
	}
	// The person is told what left the machine and whose account read it: the
	// reports are the project's, the read is theirs, and a launch that says
	// nothing hands them over silently.
	if !strings.Contains(errOut, "handing 2 stored report(s)") || !strings.Contains(errOut, "read with your own account") {
		t.Fatalf("the launch said %q, want the count and whose account read the store", errOut)
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

// TestRunCommitReportsHandsInEveryStoredReport: the launcher hands in every
// report the store holds, and the checkout's folder withholds nothing.
//
// The folder this fixture writes is not committed anywhere — appRepoCheckout
// writes it into a fresh repository with no commit at all — and the run
// commits onto a fresh clone of the ref it was launched on, so the folder's
// copy is exactly the copy the run will NOT see. A launcher that compared the
// store against it would withhold the identical report, and nothing would
// ever name it: the clone holds no row for it either, so no step downloads it
// and no step logs it as left out. The comparison belongs to the commit step,
// which makes it against the clone it really has
// (internal/runner/reports_commit.go).
//
// Mutation (skip a report whose bytes match the checkout folder's, which is
// the comparison the plan asked the launcher for): the identical report is
// not uploaded and the task does not name it, and this test fails on both.
func TestRunCommitReportsHandsInEveryStoredReport(t *testing.T) {
	day, week := commitDayName(), commitWeekName()
	f := commitReportsFixture(t, map[string]string{day: "the same bytes in both places"})
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "the same bytes in both places")
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+week, "the aggregate the folder does not hold")

	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "commit-reports",
		"--run-id", commitRunID, "--commit-reports", "Commit the stored reports")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "handing 2 stored report(s)") {
		t.Fatalf("the launch said %q, want both stored reports handed in", errOut)
	}
	for name, body := range map[string]string{day: "the same bytes in both places", week: "the aggregate the folder does not hold"} {
		if got := string(object(t, f, commitReportKey(commitRunID, name))); got != body {
			t.Fatalf("%s = %q, want %q", name, got, body)
		}
	}
	spec := readSpec(t, f, commitRunID)
	want := inNameOrder(inputOf(day, "the same bytes in both places"), inputOf(week, "the aggregate the folder does not hold"))
	if len(spec.Reports) != len(want) {
		t.Fatalf("the task names %+v, want every stored report", spec.Reports)
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
	want := inNameOrder(inputOf(day, "the day's report"), inputOf(week, "the week's aggregate"))
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

// TestRunCommitReportsGatesOnTheImagesRelease: a task carrying the reports
// member is refused outright by a runner older than commitReportsSince
// (task.Parse decodes with DisallowUnknownFields, so every unknown field is
// fatal), so a --commit-reports launch must not reach such a job at all — and
// the refusal is made before the first byte, so the run id it never started
// holds no object either.
//
// Mutation (drop the gate from commitReportsLaunch): the launch starts a job
// whose runner refuses the whole task at bootstrap and this test fails on the
// missing refusal.
func TestRunCommitReportsGatesOnTheImagesRelease(t *testing.T) {
	f := commitReportsFixture(t, nil)
	below := shiftRelease(t, commitReportsSince, -1)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+below)
	day := commitDayName()
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "a stored report")
	wantRefused(t, f, "--commit-reports needs a job image whose runner knows the stored reports a task hands in (fugaro "+commitReportsSince+" or later)",
		"run", "--repo", "acme/app", "--recipe", "commit-reports", "--run-id", commitRunID, "--commit-reports", "Commit the stored reports")
	if hasObject(t, f, commitReportKey(commitRunID, day)) {
		t.Fatal("the gate is before the upload, and a refused launch left a report under a run id that never got a task")
	}
	if hasObject(t, f, taskKey(commitRunID)) {
		t.Fatal("the refused launch wrote a task")
	}
	// The constant is a release a build record's base_ref can carry, which is
	// what the gate reads: a value that is not one is a constant no image can
	// ever satisfy.
	if rel, ok := releaseOf("ghcr.io/dimipaun/fugaro-base:" + commitReportsSince); !ok || rel != commitReportsSince {
		t.Fatalf("commitReportsSince = %q is not the X.Y.Z release a build record's base_ref carries", commitReportsSince)
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

// TestRunCommitReportsUploadsCreateOnly: every object the launcher hands in is
// a create-only write, so a second launcher that won the run id in the
// meantime cannot overwrite the reports of a run whose task is already there.
// The deterministic refusal is the freshness check above; this is the arm that
// classifies the race the other one cannot see, and the message names the
// report and the run, because the launcher lost a race it cannot observe.
//
// Mutation (pass a replacing write, or drop the blobx.ErrExists arm): the
// second launcher's object replaces the first and this test fails on the
// object's bytes.
func TestRunCommitReportsUploadsCreateOnly(t *testing.T) {
	day := commitDayName()
	f := commitReportsFixture(t, nil)
	writeBucketFile(t, f, digest.ReportsPrefix+"/"+day, "the store's bytes")
	writeBucketFile(t, f, commitReportKey(commitRunID, day), "a second launcher's bytes")
	spec := &task.Spec{Version: 1, RunID: commitRunID, Repo: "acme/app", Ref: "main", Task: "Commit the stored reports",
		Recipe: &task.Recipe{Name: "commit-reports"}}
	err := commitReportsUpload(context.Background(), fileEnv(t, f), spec, "runs/"+appSlug+"/"+commitRunID+"/", io.Discard)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is already in run") {
		t.Fatalf("commitReportsUpload = %v, want a refusal naming the report and the run", err)
	}
	if got := string(object(t, f, commitReportKey(commitRunID, day))); got != "a second launcher's bytes" {
		t.Fatalf("the existing object was overwritten: %q", got)
	}
	if len(spec.Reports) != 0 {
		t.Fatalf("a failed upload filled in %+v", spec.Reports)
	}
}
