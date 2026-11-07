package cli

// report_run_test.go is CL7c: the run and runs writers -- `run`'s launch
// lines, `cancel`, `reaper` and the `backlog` writers -- print their steps
// through the reporter (D1354's second stage), each opening its own log
// (D1352). What these commands own and must keep exactly as it was is here
// too: `run --json`'s document, which stays byte-for-byte one (G2), the
// reaper's report, which is the audit trail and leads stdout line for line,
// and the launch's own branch and cloud-log lines, which the step carries
// under it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backlog"
)

// isolateState points the state directory -- where a run's log is filed
// (D1351) -- at a directory of the test's own: a test that reads back the log
// its command left needs one log of that command's name and no other test's
// under it, and no test writes the developer's own logs.
func isolateState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
}

// TestATestNeverWritesTheDeveloperStateHome: the commands this batch moves
// onto the reporter file a log as they run, so the suite must never file one
// in the developer's own state directory -- and prune it there, 50 files per
// command name. TestMain is the backstop that makes it impossible; a test
// that reads a log back points the state home at its own (isolateState).
//
// Mutation (run, restore): delete the XDG_STATE_HOME block from TestMain and
// this fails: testStateRoot is empty, and the logs of every migrated command
// land in the developer's ~/.local/state/fugaro/logs.
func TestATestNeverWritesTheDeveloperStateHome(t *testing.T) {
	if os.Getenv("XDG_STATE_HOME") != testStateRoot || testStateRoot == "" {
		t.Fatalf("XDG_STATE_HOME = %q, TestMain's own = %q: a command that keeps a log would file it in the developer's own state home", os.Getenv("XDG_STATE_HOME"), testStateRoot)
	}
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(testStateRoot, home+string(filepath.Separator)) {
		t.Fatalf("TestMain's state home %q is inside the developer's home %q", testStateRoot, home)
	}
}

// closingLine is the reporter's last line for a run of n steps: the elapsed
// time is the run's own, so only its shape is pinned.
func closingLine(t *testing.T, out string, n int) {
	t.Helper()
	lines := reportLines(out)
	last := lines[len(lines)-1]
	want := " steps"
	if n == 1 {
		want = " step"
	}
	if !strings.HasPrefix(last, "Done in ") || !strings.HasSuffix(last, want) {
		t.Errorf("the closing line is %q, want the reporter's summary ending in %q:\n%s", last, want, out)
	}
}

// TestRunDefaultIsShort: `fugaro run`'s default view is the launch as one
// step, the branch it cut and the run's own cloud log under it, and the
// closing line (D1352, D1354). The launch's own words are unchanged: the same
// slug, the same follow-up note, the same branch and logs lines.
func TestRunDefaultIsShort(t *testing.T) {
	newCloudFixture(t)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	lines := reportLines(out)
	if got, want := lines[0], markDone+" launch "+appSlug+"/20260927-100000-abcd"; got != want {
		t.Errorf("the launch's first line is %q, want the step %q:\n%s", got, want, out)
	}
	for _, want := range []string{
		"  branch fugaro/20260927-100000-abcd cut from main\n",
		"  logs https://",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch no longer prints %q:\n%s", want, out)
		}
	}
	if n := len(stepNames(out)); n != 1 {
		t.Errorf("the run printed %d steps, want the launch's one:\n%s", n, out)
	}
	closingLine(t, out, 1)
}

// TestRunAttemptsIsOneStepPerAttempt: a best-of-N batch's default view is one
// step per attempt -- each naming the attempt and the run it launched, with
// the attempt's own branch and cloud-log lines under it -- and the one closing
// line of the batch.
func TestRunAttemptsIsOneStepPerAttempt(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 5)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "fix the flaky test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{markDone + " launch 1 of 2 ", markDone + " launch 2 of 2 "} {
		if !strings.Contains(out, want) {
			t.Errorf("the batch printed %q, want the attempt's step %q:\n%s", out, want, out)
		}
	}
	if i, j := strings.Index(out, " launch 1 of 2 "), strings.Index(out, " launch 2 of 2 "); i > j {
		t.Errorf("the attempts' steps are not in the order they ran:\n%s", out)
	}
	closingLine(t, out, 2)
}

// TestRunJSONIsUntouched: --json is a contract (G2). stdout is one document
// and nothing else, and the account the run now prints as a step goes to
// stderr, where the launcher's own notes already went.
func TestRunJSONIsUntouched(t *testing.T) {
	newCloudFixture(t)
	out, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	var doc launchResult
	if json.Unmarshal([]byte(out), &doc) != nil || !json.Valid([]byte(out)) || doc.RunID != "20260927-100000-abcd" {
		t.Fatalf("stdout is not the launch's one document:\n%s", out)
	}
	if strings.ContainsRune(out, 0x1b) || len(stepNames(out)) != 0 {
		t.Errorf("--json stdout carries a human line or an escape byte:\n%s", out)
	}
	if !strings.Contains(errOut, markDone+" launch "+appSlug+"/20260927-100000-abcd") {
		t.Errorf("the launch's step is not on stderr:\n%s", errOut)
	}
}

// TestCancelDefaultIsShort: cancel's default view keeps its progress line and
// its one-line result, and the cancel itself is one step that ends before the
// result line, so the verdict is read above the message rather than after it.
func TestCancelDefaultIsShort(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "implement")
	out, _, err := execute(t, "cancel", "--now", "20260927-100000-abcd")
	if err != nil {
		t.Fatal(err)
	}
	step := markDone + " cancel " + appSlug + "/20260927-100000-abcd"
	result := "cancelled the execution."
	for _, want := range []string{"cancel requested for " + appSlug + "/20260927-100000-abcd", step, result} {
		if !strings.Contains(out, want) {
			t.Errorf("cancel printed %q, want it to say %q", out, want)
		}
	}
	if i, j := strings.Index(out, step), strings.Index(out, result); i > j {
		t.Errorf("the result line runs before the step's own line:\n%s", out)
	}
	closingLine(t, out, 1)
}

// TestReaperDefaultIsShort: the report is the whole of what this command
// prints -- SECURITY.md calls it the audit trail and the pass's own Cloud Run
// job logs it -- so it leads stdout exactly as it did, with the pass itself
// as one step and the closing line after it.
func TestReaperDefaultIsShort(t *testing.T) {
	rig := newReaperRig(t, true)
	job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)
	out, _, err := execute(t, "reaper")
	if err != nil {
		t.Fatalf("fugaro reaper: %v", err)
	}
	lines := reportLines(out)
	if !strings.HasPrefix(lines[0], "reaped projects/proj-1234/locations/us-east5: queue deadline ") {
		t.Errorf("the report no longer leads stdout: %q\n%s", lines[0], out)
	}
	if !strings.Contains(out, "deleted job projects/proj-1234/locations/us-east5/jobs/"+job) {
		t.Errorf("the report lost the action the pass took:\n%s", out)
	}
	if !strings.Contains(out, markDone+" sweep proj-1234/us-east5") {
		t.Errorf("the pass is not reported as a step:\n%s", out)
	}
	closingLine(t, out, 1)
}

// TestBacklogAddDefaultIsShort: filing an item is two steps -- the item and
// the index it regenerates beside it -- and the closing line, where the two
// lines it printed were the same two facts.
func TestBacklogAddDefaultIsShort(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	out, _, err := execute(t, addArgs(dir)...)
	if err != nil {
		t.Fatal(err)
	}
	item := filepath.Join(dir, "20261024-01-review-round-2-left-3-findings-open.md")
	index := filepath.Join(dir, backlog.IndexName)
	filed, wrote := markDone+" filed "+item, markDone+" wrote "+index
	if !strings.Contains(out, filed) || !strings.Contains(out, wrote) {
		t.Errorf("filing printed %q, want %q and %q", out, filed, wrote)
	}
	if strings.Index(out, filed) > strings.Index(out, wrote) {
		t.Errorf("the index is reported before the item it lists:\n%s", out)
	}
	closingLine(t, out, 2)
}

// TestBacklogAddFailureIsAFailedStep: a filing the folder refuses (the same
// title filed twice on one day) is the run's failure block (G8) -- the step
// that failed with the reason it did, the path of the log this run's output is
// in, and the closing line under it -- where it printed the refusal alone.
//
// The item it refuses is written through backlog.Write and not by running
// `backlog add`: this test's one run of the command is the refused one, and a
// second run a second later in this process would want the log name of the
// first (CL3 names a log after the command, the second and the pid).
func TestBacklogAddFailureIsAFailedStep(t *testing.T) {
	isolateState(t)
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	if _, err := backlog.Write(dir, backlog.New{
		Title: "Review round 2 left 3 findings open", Mode: "none", Area: "repo",
		FiledBy: "octocat", FiledIn: "hand", Date: "2026-10-24",
		Body: backlog.Body("Review round 2 left 3 findings open", "the round's findings are worth addressing, not blocking", "", nil),
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, addArgs(dir)...)
	if err == nil {
		t.Fatal("a second filing of the same title and day was accepted")
	}
	if !strings.Contains(out, markFailed+" filed: ") {
		t.Errorf("the refusal is not the step that failed:\n%s", out)
	}
	path, _ := onlyLog(t, "backlog-add")
	if !strings.Contains(out, "full output: "+path) {
		t.Errorf("the failure does not name the log this run's output is in:\n%s", out)
	}
	if lines := reportLines(out); !strings.HasPrefix(lines[len(lines)-1], "Failed in ") {
		t.Errorf("the run's closing line is missing, or is not the failure it was:\n%s", out)
	}
}

// TestBacklogIndexDefaultIsShort: regenerating the index alone is one step
// and the closing line.
func TestBacklogIndexDefaultIsShort(t *testing.T) {
	dir := backlogFixture(t)
	out, _, err := execute(t, "backlog", "index", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	lines := reportLines(out)
	if want := markDone + " wrote " + filepath.Join(dir, backlog.IndexName); len(lines) != 2 || lines[0] != want {
		t.Errorf("regenerating the index printed %q, want %q and the closing line", out, want)
	}
	closingLine(t, out, 1)
}

// TestRunWritesALog: a launch keeps a log (D1352), and the log holds what the
// launch printed: the branch it cut and where the run's own cloud log is.
func TestRunWritesALog(t *testing.T) {
	isolateState(t)
	newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "Add a feature"); err != nil {
		t.Fatal(err)
	}
	if _, log := onlyLog(t, "run"); !strings.Contains(log, "branch fugaro/20260927-100000-abcd cut from main") {
		t.Errorf("the launch's log does not hold the branch it cut:\n%s", log)
	}
}

// TestCancelWritesALog: a cancel keeps a log, and it holds the progress line
// and the result the person read on the screen.
func TestCancelWritesALog(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	isolateState(t)
	out, _, err := execute(t, "cancel", "--now", "20260927-100000-abcd")
	if err != nil {
		t.Fatal(err)
	}
	ref := appSlug + "/20260927-100000-abcd"
	_, log := onlyLog(t, "cancel")
	for _, want := range []string{"cancel requested for " + ref, "cancelled the execution."} {
		if !strings.Contains(log, want) || !strings.Contains(out, want) {
			t.Errorf("cancel said %q on the screen and logged %q, want both to carry %q", out, log, want)
		}
	}
}

// TestReaperWritesALog: a pass keeps a log, and the log holds the report --
// the audit trail, which is the whole of what this command prints.
func TestReaperWritesALog(t *testing.T) {
	isolateState(t)
	rig := newReaperRig(t, true)
	job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	if _, _, err := execute(t, "reaper"); err != nil {
		t.Fatalf("fugaro reaper: %v", err)
	}
	want := "deleted job projects/proj-1234/locations/us-east5/jobs/" + job
	if _, log := onlyLog(t, "reaper"); !strings.Contains(log, want) {
		t.Errorf("the reaper's log does not hold its report:\n%s", log)
	}
}

// TestBacklogAddWritesALog and TestBacklogIndexWritesALog: filing writes
// files and regenerating writes one, so both keep a log of their own run
// (D1352) -- named after the command, 0600 in the 0700 directory (G5). The
// two facts they print are the reporter's step lines, which the screen has
// and the log does not: the log is the run's own stream, and neither command
// has one.
func TestBacklogAddWritesALog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	isolateState(t)
	if _, _, err := execute(t, addArgs(dir)...); err != nil {
		t.Fatal(err)
	}
	assertLogMode(t, "backlog-add")
}

func TestBacklogIndexWritesALog(t *testing.T) {
	dir := backlogFixture(t)
	isolateState(t)
	if _, _, err := execute(t, "backlog", "index", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	assertLogMode(t, "backlog-index")
}

// assertLogMode is the one log a command of this name left, 0600 (G5).
func assertLogMode(t *testing.T, cmdName string) {
	t.Helper()
	path, _ := onlyLog(t, cmdName)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the run's log is not there: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("%s is mode %o, want 600", path, got)
	}
}

// TestARefusedCommandKeepsNoLog: a command that refuses before its run begins
// files no log — not even an empty one. The log is the run's own output and a
// refusal has none, so the reporter opens where the run's work does: after the
// launch's own task is built, after the batch's pre-checks, after a cancel's
// ref resolved, and after the reaper's pass resolved — never at the command's
// door, where every refused invocation would file a file (D1352). The cancel
// case is why it is not merely tidy: a refusal's file is one of the 50 the
// command's own name keeps (CL3's pruneLogs), so enough refused cancels would
// evict the newest real one an operator is pointed at.
func TestARefusedCommandKeepsNoLog(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{"a launch whose task is refused", func(t *testing.T) error {
			newCloudFixture(t)
			_, _, err := execute(t, "run", "--retry", "20260927-000000-0000")
			return err
		}},
		{"a batch its own pre-checks refuse", func(t *testing.T) error {
			newCloudFixture(t)
			_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--recipe", "review-only", "fix the flaky test")
			return err
		}},
		{"a cancel whose ref is refused", func(t *testing.T) error {
			newCloudFixture(t)
			_, _, err := execute(t, "cancel", "not-a-run-id")
			return err
		}},
		{"a pass the selected project refuses", func(t *testing.T) error {
			newReaperRig(t, false)
			_, _, err := execute(t, "reaper")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateState(t)
			if err := tc.run(t); err == nil {
				t.Fatal("the command ran, want the refusal this case is about")
			}
			assertNoLog(t)
		})
	}
}

// assertNoLog is what a refused run must leave behind: nothing. The log
// directory is one isolateState's own, so a directory that is not there at all
// is no log either.
func assertNoLog(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(logDir(t))
	if err != nil {
		return
	}
	if len(entries) != 0 {
		t.Errorf("a refused run filed %d logs, want none: %v", len(entries), entries)
	}
}

// TestNoStepCommandPrintsAnsiOffATerminal: a command that reports no step --
// a listing, and --json, whose document no human line may touch -- prints no
// escape byte off a terminal and no step line where its output goes (G2, G4).
func TestNoStepCommandPrintsAnsiOffATerminal(t *testing.T) {
	dir := backlogFixture(t)
	out, errOut, err := execute(t, "backlog", "list", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out+errOut, 0x1b) {
		t.Errorf("a listing printed an escape byte off a terminal:\n%s", out)
	}
	if n := len(stepNames(out)); n != 0 {
		t.Errorf("a listing printed %d step lines:\n%s", n, out)
	}

	newCloudFixture(t)
	out, errOut, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out+errOut, 0x1b) {
		t.Errorf("--json printed an escape byte off a terminal:\n%s%s", out, errOut)
	}
	if n := len(stepNames(out)); n != 0 {
		t.Errorf("--json printed %d step lines on stdout:\n%s", n, out)
	}
}
