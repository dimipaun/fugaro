package verify

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type fixture struct {
	stateDir, repoDir, failsFile string
}

func setup(t *testing.T) fixture {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, testutil.FixtureFiles(t))
	parent := t.TempDir()
	repoDir := filepath.Join(parent, "repo")
	testutil.Git(t, parent, "clone", "--quiet", remote, repoDir)
	f := fixture{stateDir: t.TempDir(), repoDir: repoDir, failsFile: filepath.Join(t.TempDir(), "fails")}
	t.Setenv("FIXTURE_FAILS_FILE", f.failsFile)
	err := WriteSettings(f.stateDir, Settings{
		RepoDir:     repoDir,
		Build:       "sh build.sh",
		Test:        "sh test.sh",
		RerunFailed: &config.RerunFailed{Command: "sh test.sh", Each: "{id}"},
		Reports:     []string{"build/test-results/*.xml"},
		TimeoutS:    60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) fails(t *testing.T, names string) {
	t.Helper()
	if err := os.WriteFile(f.failsFile, []byte(names), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, f fixture, kind Kind, rerun bool) Record {
	t.Helper()
	rec, err := Run(context.Background(), Options{StateDir: f.stateDir, Kind: kind, Rerun: rerun, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestBuildPassAndFail(t *testing.T) {
	f := setup(t)
	if rec := run(t, f, KindBuild, false); !rec.Passed || rec.N != 1 {
		t.Fatalf("passing build = %+v", rec)
	}
	t.Setenv("FIXTURE_BUILD_EXIT", "1")
	if rec := run(t, f, KindBuild, false); rec.Passed || rec.ExitCode != 1 || rec.N != 2 {
		t.Fatalf("failing build = %+v", rec)
	}
}

func TestTestParsesReports(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	rec := run(t, f, KindTest, false)
	if rec.Passed || rec.Tests != 3 || rec.Failures != 1 || !slices.Equal(rec.Failed, []string{"pkg.Suite.beta"}) {
		t.Fatalf("record = %+v", rec)
	}
	head := testutil.Git(t, f.repoDir, "rev-parse", "HEAD")
	if rec.HeadSHA != head || !rec.CleanTree {
		t.Fatalf("head=%s clean=%v, want %s true (build/ is ignored)", rec.HeadSHA, rec.CleanTree, head)
	}
	if strings.Contains(rec.Summary(), "no JUnit reports") {
		t.Fatalf("summary warns although reports were found: %s", rec.Summary())
	}
}

func TestRerunMarksFlaky(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	f.fails(t, "")
	rec := run(t, f, KindTest, true)
	if !rec.Passed || !rec.Rerun || rec.Tests != 3 || len(rec.Failed) != 0 || !slices.Equal(rec.Flaky, []string{"pkg.Suite.beta"}) {
		t.Fatalf("rerun record = %+v", rec)
	}
}

func TestRerunStillFailing(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	rec := run(t, f, KindTest, true)
	if rec.Passed || len(rec.Flaky) != 0 || !slices.Equal(rec.Failed, []string{"pkg.Suite.beta"}) {
		t.Fatalf("rerun record = %+v", rec)
	}
}

func TestRerunRequiresSameCommit(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	testutil.WriteFiles(t, f.repoDir, map[string]string{"new.txt": "x\n"})
	testutil.Git(t, f.repoDir, "add", "-A")
	testutil.Git(t, f.repoDir, "commit", "--quiet", "-m", "change")
	_, err := Run(context.Background(), Options{StateDir: f.stateDir, Kind: KindTest, Rerun: true, Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "current commit") {
		t.Fatalf("err = %v", err)
	}
}

func TestTestIgnoresStaleReports(t *testing.T) {
	f := setup(t)
	stale := filepath.Join(f.repoDir, "build", "test-results", "TEST-old.xml")
	testutil.WriteFiles(t, f.repoDir, map[string]string{
		"build/test-results/TEST-old.xml": `<testsuite><testcase classname="old" name="broken"><failure/></testcase></testsuite>`,
	})
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if rec := run(t, f, KindTest, false); !rec.Passed || rec.Tests != 3 {
		t.Fatalf("stale report was counted: %+v", rec)
	}
}

func TestDirtyTreeRecorded(t *testing.T) {
	f := setup(t)
	testutil.WriteFiles(t, f.repoDir, map[string]string{"untracked.txt": "x\n"})
	rec := run(t, f, KindTest, false)
	if rec.CleanTree {
		t.Fatal("CleanTree = true with an untracked file")
	}
	if !strings.Contains(rec.Summary(), "not clean") {
		t.Fatalf("summary does not warn about the dirty tree: %s", rec.Summary())
	}
}

func TestRecordsInOrder(t *testing.T) {
	f := setup(t)
	run(t, f, KindBuild, false)
	run(t, f, KindTest, false)
	recs, err := Records(f.stateDir)
	if err != nil || len(recs) != 2 || recs[0].N != 1 || recs[0].Kind != KindBuild || recs[1].Kind != KindTest {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestRerunCommandQuotes(t *testing.T) {
	got := RerunCommand(config.RerunFailed{Command: "./gradlew test", Each: "--tests {id}"}, []string{"a.B.c", "it's"})
	want := `./gradlew test --tests 'a.B.c' --tests 'it'\''s'`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// rerunWith replaces the fixture's rerun command with script, a shell script
// stored outside the checkout so the tree stays clean.
func rerunWith(t *testing.T, f fixture, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rerun.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.RerunFailed = &config.RerunFailed{Command: "sh " + path, Each: "{id}"}
	if err := WriteSettings(f.stateDir, s); err != nil {
		t.Fatal(err)
	}
}

// TestRerunSkippedIsNotFlaky: a previously failed test that the rerun
// reports as skipped never actually re-ran, so it must stay failed rather
// than be counted as a flaky pass.
func TestRerunSkippedIsNotFlaky(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	rerunWith(t, f, `mkdir -p build/test-results
echo '<testsuite><testcase classname="pkg.Suite" name="beta"><skipped/></testcase></testsuite>' > build/test-results/TEST-suite.xml
`)
	rec := run(t, f, KindTest, true)
	if rec.Passed || len(rec.Flaky) != 0 || !slices.Equal(rec.Failed, []string{"pkg.Suite.beta"}) {
		t.Fatalf("rerun record = %+v", rec)
	}
}

// TestRerunWithoutReportsFailsClosed: a rerun that exits 0 but leaves no
// fresh reports proves nothing, so every previous failure stays failed.
func TestRerunWithoutReportsFailsClosed(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	rerunWith(t, f, "exit 0\n")
	rec := run(t, f, KindTest, true)
	if rec.Passed || len(rec.Flaky) != 0 || !slices.Equal(rec.Failed, []string{"pkg.Suite.beta"}) {
		t.Fatalf("rerun record = %+v", rec)
	}
}

// TestTestWithoutReportsWarns: a test run that leaves no fresh reports says
// so in its summary, since its counts are then meaningless.
func TestTestWithoutReportsWarns(t *testing.T) {
	f := setup(t)
	s, err := LoadSettings(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Test = "true"
	if err := WriteSettings(f.stateDir, s); err != nil {
		t.Fatal(err)
	}
	rec := run(t, f, KindTest, false)
	if want := "no JUnit reports found under build/test-results/*.xml"; !strings.Contains(rec.Summary(), want) {
		t.Fatalf("summary %q lacks %q", rec.Summary(), want)
	}
}

// TestTimeoutKillsWithinGrace: a verify command that ignores SIGTERM is
// SIGKILLed after a grace shorter than the agent group's 10s, so that when
// the agent's stage is killed, `fugaro verify` still has time to kill its own
// test group before it is itself SIGKILLed.
func TestTimeoutKillsWithinGrace(t *testing.T) {
	f := setup(t)
	s, err := LoadSettings(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Test, s.TimeoutS = "trap '' TERM; sleep 30", 1
	if err := WriteSettings(f.stateDir, s); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rec := run(t, f, KindTest, false)
	if d := time.Since(start); d >= 10*time.Second {
		t.Fatalf("verify took %s to kill a SIGTERM-ignoring command; want under the agent's 10s grace", d)
	}
	if !rec.TimedOut || rec.Passed {
		t.Fatalf("record = %+v", rec)
	}
}

func TestLogTailKeepsLastOutput(t *testing.T) {
	f := setup(t)
	err := WriteSettings(f.stateDir, Settings{
		RepoDir: f.repoDir, Test: "sh test.sh", Reports: []string{"build/test-results/*.xml"}, TimeoutS: 60,
		Build: `i=0; while [ $i -lt 100 ]; do echo "step $i"; i=$((i+1)); done; echo "error: boom" >&2; exit 1`,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := run(t, f, KindBuild, false)
	tail, err := LogTail(f.stateDir, rec.N)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(tail, "\n")
	if len(lines) != 40 || lines[len(lines)-1] != "error: boom" || lines[0] != "step 61" {
		t.Fatalf("tail has %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	// The tail is stored beside the records, not among them, so numbering
	// and Records are unaffected.
	if rec2 := run(t, f, KindBuild, false); rec2.N != 2 {
		t.Fatalf("second record N = %d, want 2", rec2.N)
	}
	if recs, err := Records(f.stateDir); err != nil || len(recs) != 2 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestLogTailMissingIsEmpty(t *testing.T) {
	if tail, err := LogTail(t.TempDir(), 7); err != nil || tail != "" {
		t.Fatalf("tail = %q, %v", tail, err)
	}
}

func TestClearStateRemovesLogs(t *testing.T) {
	f := setup(t)
	rec := run(t, f, KindBuild, false)
	if err := ClearState(f.stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath(f.stateDir, rec.N)); !os.IsNotExist(err) {
		t.Fatalf("verify log survived ClearState: %v", err)
	}
}
