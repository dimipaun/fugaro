package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// release090 is a job image new enough to read a task's title (titlesSince).
const release090 = "ghcr.io/dimipaun/fugaro-base:0.9.0"

// runTitle launches one run of acme/app under run id id, with the given
// extra flags, and returns its stored task.
func runTitle(t *testing.T, f *cloudFixture, id string, extra []string, text string) *task.Spec {
	t.Helper()
	writeBuildRecord(t, f, appSlug, "web", release090)
	args := append([]string{"run", "--repo", "acme/app", "--run-id", id}, extra...)
	args = append(args, text)
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return readSpec(t, f, id)
}

// taskJSON is run id's raw task.json, so a test can see the wire and not
// only the decoded spec.
func taskJSON(t *testing.T, f *cloudFixture, id string) string {
	t.Helper()
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	data, err := b.ReadAll(context.Background(), "runs/"+appSlug+"/"+id+"/task.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The title flag is the run's job title, stored once in its task and read
// from there by every surface (watch, the registry entry, the pull request,
// runs ls): a brief whose first line is a shared preamble still has a title
// of its own.
//
// Mutation (drop Title from buildAttemptSpec's spec literal): the stored
// task carries no title, TitleLine falls back to the preamble's first line,
// and this test fails on its first assertion.
func TestRunTitleIsStoredInTheTask(t *testing.T) {
	f := newCloudFixture(t)
	spec := runTitle(t, f, "20261018-120000-abcd",
		[]string{"--title", "Ship the flaky-test fix"},
		"Read CLAUDE.md and the plan first.\n\nThen fix the flaky test.")
	if spec.Title != "Ship the flaky-test fix" {
		t.Fatalf("title = %q, want it stored", spec.Title)
	}
	if got := spec.TitleLine(); got != "Ship the flaky-test fix" {
		t.Fatalf("TitleLine() = %q, want the flag's title", got)
	}
	// The task itself is untouched: a title is a title, never a rewrite of
	// the brief the agent reads.
	if !strings.HasPrefix(spec.Task, "Read CLAUDE.md") {
		t.Fatalf("task = %q", spec.Task)
	}
	// And it is on the wire under "title": the runner reads it from there.
	var doc map[string]any
	if err := json.Unmarshal([]byte(taskJSON(t, f, "20261018-120000-abcd")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["title"] != "Ship the flaky-test fix" {
		t.Fatalf("task.json title = %v", doc["title"])
	}
}

// Without --title nothing changes: no title field is written at all, so the
// spec is byte-identical to what the previous release wrote and a runner
// that does not know the field can still read it.
func TestRunWithoutTitleWritesNoTitleField(t *testing.T) {
	f := newCloudFixture(t)
	spec := runTitle(t, f, "20261018-120000-abcd", nil, "Add a feature\nand more")
	if spec.Title != "" {
		t.Fatalf("title = %q, want none", spec.Title)
	}
	if got := spec.TitleLine(); got != "Add a feature" {
		t.Fatalf("TitleLine() = %q, want the task's first line", got)
	}
	if data := taskJSON(t, f, "20261018-120000-abcd"); strings.Contains(data, "title") {
		t.Fatalf("task.json carries a title field:\n%s", data)
	}
}

// The title is refused, before anything is written, unless it is one line
// of at most task.MaxTitleRunes runes, non-empty, with no control
// character: it becomes a terminal line and a pull request's title.
//
// Mutation (drop the TitleProblem call from titleFlag, storing the raw
// value): the newline case is accepted, the launch goes through and those
// subtests fail; mutation (check the title before trimming it instead of
// after): "   " is stored as a title of spaces.
func TestRunTitleValidation(t *testing.T) {
	for _, tc := range []struct{ name, title, want string }{
		{"empty", "", "is empty"},
		{"whitespace only", "   ", "is empty"},
		{"a second line", "Fix it\nand ship it", "must be one line"},
		{"a CR", "Fix\rit now", "must be one line"},
		{"a line separator", "Fix it ship it", "must be one line"},
		{"a control character", "Fix \x1b[31mit", "holds a control character"},
		{"a tab", "Fix\tit now", "holds a control character"},
		{"a NUL", "Fix it\x00", "holds a control character"},
		{"too long", strings.Repeat("x", task.MaxTitleRunes+1), "longer than 100 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261018-120000-abcd", "--title", tc.title, "Add a feature")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want exit 1 with %q", err, tc.want)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Fatalf("%d executions, want none", n)
			}
		})
	}
	// A title at exactly the cap is accepted, and the cap counts runes, not
	// bytes: 100 three-byte characters are 300 bytes and 100 runes.
	f := newCloudFixture(t)
	long := strings.Repeat("日", task.MaxTitleRunes)
	if spec := runTitle(t, f, "20261018-120000-abcd", []string{"--title", long}, "Add a feature"); spec.Title != long {
		t.Fatalf("title = %q", spec.Title)
	}
}

// A title is sanitised as every other string this program shows is: the
// characters that would let it read differently from what it is (bidi
// overrides, zero-width joiners) are stripped, and runs of whitespace
// collapse to one space, so the terminal row and the pull request's title
// carry exactly the characters the operator meant.
//
// Mutation (store the trimmed title without safetext.Field): the bidi
// override survives into task.json, where it can reorder the title it sits
// in, and this test fails.
func TestRunTitleIsSanitised(t *testing.T) {
	f := newCloudFixture(t)
	spec := runTitle(t, f, "20261018-120000-abcd",
		[]string{"--title", "Fix  the flaky‮test​  here"},
		"Add a feature")
	// The override and the zero-width space are dropped (so "flaky" and
	// "test" join), and the doubled spaces collapse to one.
	if want := "Fix the flakytest here"; spec.Title != want {
		t.Fatalf("title = %q, want %q", spec.Title, want)
	}
}

// --retry relaunches the stored task as it is, and a title is part of it:
// the flag is refused rather than silently ignored.
func TestRunTitleRefusedWithRetry(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "it takes no --title", "run", "--retry", "20261018-120000-abcd", "--title", "Late title")
}

// Every job of a best-of-N batch carries the same title: the brief is the
// same brief, and its attempts are told apart by their run ids, not by five
// titles for one task.
//
// Mutation (drop Title from buildAttemptSpec): no attempt stores a title and
// each falls back to the task's first line, which is the shared preamble.
func TestRunAttemptsCarryTheTitleOnEveryAttempt(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release090)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--title", "Ship the flaky-test fix", "--json",
		"Read the plan first.\n\nThen fix it.")
	if err != nil {
		t.Fatal(err)
	}
	var batch batchResult
	if err := json.Unmarshal([]byte(out), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Runs) != 2 {
		t.Fatalf("launched %d attempts, want 2 (%s)", len(batch.Runs), out)
	}
	for _, r := range batch.Runs {
		if got := readSpec(t, f, r.RunID).Title; got != "Ship the flaky-test fix" {
			t.Fatalf("attempt %s: title = %q", r.RunID, got)
		}
	}
}

// A follow-up keeps its run's title: --pr without --title inherits the title
// the previous run of the chain carries, so a chain of follow-ups stays one
// job in fugaro watch. An explicit --title overrides it.
//
// Mutation (drop the inherited title from newFollowUpSpec): the follow-up
// falls back to its own instructions' first line, which is what the first
// assertion fails on.
func TestFollowUpInheritsTheRunsTitle(t *testing.T) {
	f := newCloudFixture(t)
	root := firstRunSpec(rootID)
	root.Title = "Ship the flaky-test fix"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)

	writeBuildRecord(t, f, appSlug, "web", release090)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).TitleLine(); got != "Ship the flaky-test fix" {
		t.Fatalf("follow-up title = %q, want the run's title", got)
	}
}

// An explicit --title on a follow-up replaces the inherited one.
func TestFollowUpTitleFlagOverridesTheInheritedOne(t *testing.T) {
	f := newCloudFixture(t)
	root := firstRunSpec(rootID)
	root.Title = "Ship the flaky-test fix"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)

	writeBuildRecord(t, f, appSlug, "web", release090)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--title", "Rename it properly", "also rename x"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).Title; got != "Rename it properly" {
		t.Fatalf("follow-up title = %q, want the flag's", got)
	}
}

// A chain whose first run carried no title stays exactly as it was: the
// follow-up stores no title, and its title is its own instructions' first
// line (which is what a follow-up has always been listed by).
func TestFollowUpWithoutATitleToInherit(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release090)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, fuID)
	if spec.Title != "" || spec.TitleLine() != "also rename x" {
		t.Fatalf("task = %+v, want no title and the first line", spec)
	}
}

// A repeated --run-id names a stored task, and a title is part of that
// task: asking for the same run id with a different title is a different
// task, refused like any other difference.
//
// Mutation (drop the title from storedFollowUp's comparison): the second
// launch reports the first as if it were the same task, silently keeping
// the old title.
func TestStoredFollowUpRefusesADifferentTitle(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release090)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--title", "First title", "also rename x"); err != nil {
		t.Fatal(err)
	}
	// wantRefused is not used here: this fixture has already launched the
	// run, so its "no execution started" half would fail for the wrong
	// reason.
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--title", "Another title", "also rename x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v, want the different task refusal", err)
	}
	// The same title is the same task: the repeated launch reports the
	// existing one instead of starting another.
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--title", "First title", "also rename x")
	if err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// A task carrying a title is refused outright by a runner older than
// titlesSince (task.Parse decodes with DisallowUnknownFields), so the
// launch must not reach such a job at all — and a launch with no title is
// never gated, since its spec carries no new field.
//
// Mutation (drop the spec.Title != "" gate from runRun's gates): the 0.8.0
// job is launched, and the run then fails at bootstrap on a field nobody
// typed.
func TestRunTitleGatesOnTheImageThatKnowsIt(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release080)
	wantRefused(t, f, "needs a job image whose runner knows the job title (fugaro "+titlesSince+" or later)",
		"run", "--repo", "acme/app", "--title", "Ship the flaky-test fix", "Add a feature")
	// The same image, a launch with no title: not gated, because the spec
	// has no such field in it.
	writeBuildRecord(t, f, appSlug, "web", release080)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261018-120000-abcd", "Add a feature"); err != nil {
		t.Fatalf("a launch with no --title must not be gated: %v", err)
	}
}

// A checkout: clone workflow has no build record to judge, so the same gate
// is judged against base_images.base itself: 0.7.5 is cloneSince, so the
// clone floor passes and the title floor (0.9.0) is what refuses.
//
// Mutation (drop the spec.Title != "" block from finishLaunch's clone
// branch): the launch goes through on an image whose runner refuses the
// whole task at bootstrap.
func TestRunTitleGatesOnACloneWorkflow(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "base_images: {base: ghcr.io/dimipaun/fugaro-base:0.7.5}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n" +
		"  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "--title", "Ship the flaky-test fix", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--title needs a job image whose runner knows the job title (fugaro "+titlesSince+" or later)") ||
		!strings.Contains(err.Error(), "ghcr.io/dimipaun/fugaro-base:0.7.5, release 0.7.5") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// runs ls reads the title from the one source too: its rows carry the run's
// job title (the task's own, else the first line), which is what the
// desktop console and --json readers list a run by.
//
// Mutation (drop the row.Title assignment from runview.Join): both rows'
// titles are empty and this test fails.
func TestRunsLsRowsCarryTheJobTitle(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	titled := firstRunSpec(today + "-090000-aaaa")
	titled.Title = "Ship the flaky-test fix"
	titled.Task = "Read the plan first.\n\nThen fix it."
	seedSpec(t, f, titled, false)
	seeded := firstRunSpec(today + "-091000-bbbb")
	seeded.Task = "Add a feature"
	seedSpec(t, f, seeded, false)

	got, _ := lsJSON(t)
	titles := map[string]string{}
	for _, r := range got.Runs {
		titles[r.RunID] = r.Title
	}
	if titles[titled.RunID] != "Ship the flaky-test fix" {
		t.Fatalf("titles = %v, want the stored title", titles)
	}
	if titles[seeded.RunID] != "Add a feature" {
		t.Fatalf("titles = %v, want the task's first line", titles)
	}
	// The human table keeps its columns: a title is not one of them (fugaro
	// watch is where a run is listed by title).
	human, _, err := execute(t, "runs", "ls")
	if err != nil || strings.Contains(human, "TITLE") {
		t.Fatalf("human ls = %q, %v", human, err)
	}
}
