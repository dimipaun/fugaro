// The pre-tag rehearsal: scripts/release-rehearsal.sh, and the step of
// scripts/release.sh that runs it before the tag.
//
// The rehearsal is the one billable step of this package's runs and it never
// happens here: RELEASE_REHEARSAL_FUGARO names a fake CLI (scripts/testdata/
// fake-fugaro.sh), so what is exercised is the ladder around the build -- the
// invocation the rehearsal makes, the record it writes, the guard that keeps it
// out of the owner's project, and the gate that reads the record -- and not the
// Cloud Build itself. TestReleaseRehearsalBuildsTheCLIFromTheCommit covers the
// one arm that is about building at all, with a fake `go`.
package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// runRehearsal runs scripts/release-rehearsal.sh from the fixture checkout with
// the fake fugaro named as the CLI and the record in r.recordDir, and returns
// what it printed and how it ended. extra is appended last, so it overrides
// what rehearsalEnv sets (a test that wants another project name, a red fake, or
// no fake at all).
func runRehearsal(t *testing.T, r *releaseRepo, extra []string, args ...string) runResult {
	t.Helper()
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", append([]string{filepath.Join(r.local, "scripts", "release-rehearsal.sh")}, args...)...)
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, rehearsalEnv(r, bin)...)
	cmd.Env = append(cmd.Env, extra...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
}

// rehearsalRecord is the record the rehearsal wrote, as text.
func rehearsalRecord(t *testing.T, r *releaseRepo) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.recordDir, "record"))
	if err != nil {
		t.Fatalf("reading the rehearsal record: %v", err)
	}
	return string(b)
}

// writeRehearsalRecord writes a record by hand, for the states no run of the
// script produces (a verdict for another commit, a record left behind by one).
func writeRehearsalRecord(t *testing.T, r *releaseRepo, commit, build, verdict, log string) {
	t.Helper()
	if err := os.MkdirAll(r.recordDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "commit=" + commit + "\nbuild=" + build + "\nverdict=" + verdict + "\nlog=" + log + "\n"
	if err := os.WriteFile(filepath.Join(r.recordDir, "record"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fugaroCalls is every invocation of the fake fugaro, one line each: what the
// rehearsal handed the CLI it is about to trust.
func fugaroCalls(t *testing.T, r *releaseRepo) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.fugaroState, "log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

func requireNoRehearsalCalls(t *testing.T, r *releaseRepo) {
	t.Helper()
	if calls := fugaroCalls(t, r); len(calls) != 0 {
		t.Errorf("the rehearsal called the CLI when it should not have: %v", calls)
	}
}

// TestReleaseRehearsalRunWritesAGreenRecordAndTheGatePasses: the rehearsal makes
// the one call that builds this repository's own catalog entry -- `image build
// go`, in the sandbox project it was told -- and writes the record the tag is
// gated on: the commit, the Cloud Build id, the verdict and the build's log.
func TestReleaseRehearsalRunWritesAGreenRecordAndTheGatePasses(t *testing.T) {
	r := newReleaseRepo(t)
	sha := testutil.Git(t, r.local, "rev-parse", "HEAD")

	res := runRehearsal(t, r, nil, "run", sha)
	res.requireSuccess(t)

	calls := fugaroCalls(t, r)
	want := "image build go --gcp-project " + rehearsalSandbox
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("the rehearsal made %v, want exactly one %q: the entry it builds is this repository's own, and the project is the sandbox it was given", calls, want)
	}
	record := rehearsalRecord(t, r)
	for _, wantField := range []string{
		"commit=" + sha,
		"build=fake-build-1",
		"verdict=pass",
		"log=https://console.cloud.google.com/cloud-build/builds/fake-build-1?project=" + rehearsalSandbox,
	} {
		if !strings.Contains(record, wantField+"\n") {
			t.Errorf("the record does not carry %q:\n%s", wantField, record)
		}
	}
	if !strings.Contains(res.out, "PASS") || !strings.Contains(res.out, "fake-build-1") {
		t.Errorf("the rehearsal did not report what it did, with the build it made:\n%s", res.out)
	}

	check := runRehearsal(t, r, nil, "check", sha)
	check.requireSuccess(t)
	if !strings.Contains(check.out, "passed") {
		t.Errorf("the gate on a green record did not say so:\n%s", check.out)
	}
}

// TestReleaseRehearsalRefusesARedBuild: a build that did not produce the entry
// is recorded as a failure (so the gate can tell it from a rehearsal that never
// ran) and refuses, naming the Cloud Build to read.
func TestReleaseRehearsalRefusesARedBuild(t *testing.T) {
	r := newReleaseRepo(t)
	sha := testutil.Git(t, r.local, "rev-parse", "HEAD")

	res := runRehearsal(t, r, []string{"FAKE_FUGARO_STATUS=1"}, "run", sha)
	res.requireFailureContaining(t, "did not build the go catalog entry")
	res.requireFailureContaining(t, "https://console.cloud.google.com/cloud-build/builds/fake-build-1")

	record := rehearsalRecord(t, r)
	if !strings.Contains(record, "verdict=fail\n") {
		t.Errorf("a failed build must be recorded as a failure, not left missing:\n%s", record)
	}
	if !strings.Contains(record, "commit="+sha+"\n") {
		t.Errorf("the record does not name the commit that was rehearsed:\n%s", record)
	}

	check := runRehearsal(t, r, nil, "check", sha)
	check.requireFailureContaining(t, "the rehearsal of "+sha+" failed")
	check.requireFailureContaining(t, "fake-build-1")
}

// TestReleaseRehearsalRefusesAMissingRecord: with no record at all, the gate
// says which command writes one. This is the state a tag would otherwise be cut
// in: a release whose rehearsal never ran.
func TestReleaseRehearsalRefusesAMissingRecord(t *testing.T) {
	r := newReleaseRepo(t)
	sha := testutil.Git(t, r.local, "rev-parse", "HEAD")

	res := runRehearsal(t, r, nil, "check", sha)
	res.requireFailureContaining(t, "no rehearsal record")
	res.requireFailureContaining(t, "release-rehearsal.sh run "+sha)
}

// TestReleaseRehearsalRefusesARecordForAnotherCommit: a record that is not this
// commit's is a rehearsal that has not run for it, and the refusal names both
// commits rather than reading as a green one.
func TestReleaseRehearsalRefusesARecordForAnotherCommit(t *testing.T) {
	r := newReleaseRepo(t)
	sha := testutil.Git(t, r.local, "rev-parse", "HEAD")
	other := strings.Repeat("a", 40)
	writeRehearsalRecord(t, r, other, "fake-build-2", "pass", "https://console.cloud.google.com/cloud-build/builds/fake-build-2")

	res := runRehearsal(t, r, nil, "check", sha)
	res.requireFailureContaining(t, "is for "+other+", not "+sha)
}

// TestReleaseRehearsalRefusesTheOwnersProject: the sandbox is the only project
// it builds in. A name carrying `belong` -- the owner's own -- and an unset name
// both refuse before the CLI is called at all, so nothing is built and no record
// is written.
func TestReleaseRehearsalRefusesTheOwnersProject(t *testing.T) {
	for _, tc := range []struct {
		name, project, want string
	}{
		{"belong", "belong-prod", "carries 'belong'"},
		{"unset", "", "FUGARO_LIVE_GCP_PROJECT is not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			sha := testutil.Git(t, r.local, "rev-parse", "HEAD")

			res := runRehearsal(t, r, []string{"FUGARO_LIVE_GCP_PROJECT=" + tc.project}, "run", sha)
			res.requireFailureContaining(t, tc.want)
			requireNoRehearsalCalls(t, r)
			if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
				t.Error("a rehearsal refused before it built anything wrote a record: the record is about a build that happened")
			}
		})
	}
}

// TestReleaseRehearsalRefusesACheckoutAtAnotherCommit: the entry and the CLI are
// both read from the tree, so a checkout that is not on the commit being
// rehearsed refuses before anything is built -- a build of another tree's
// Dockerfile vouches for another commit.
func TestReleaseRehearsalRefusesACheckoutAtAnotherCommit(t *testing.T) {
	r := newReleaseRepo(t)
	head := testutil.Git(t, r.local, "rev-parse", "HEAD")
	other := strings.Repeat("b", 40)

	res := runRehearsal(t, r, nil, "run", other)
	res.requireFailureContaining(t, head+", not "+other)
	requireNoRehearsalCalls(t, r)
}

// TestReleaseRehearsalReusesAGreenRecord: the build is billable and a resumed
// release re-runs the step, so a green record for the very commit being
// rehearsed is reused rather than built again.
func TestReleaseRehearsalReusesAGreenRecord(t *testing.T) {
	r := newReleaseRepo(t)
	sha := testutil.Git(t, r.local, "rev-parse", "HEAD")

	runRehearsal(t, r, nil, "run", sha).requireSuccess(t)
	second := runRehearsal(t, r, nil, "run", sha)
	second.requireSuccess(t)
	if !strings.Contains(second.out, "already passed") {
		t.Errorf("the second rehearsal did not reuse the record it found:\n%s", second.out)
	}
	if calls := fugaroCalls(t, r); len(calls) != 1 {
		t.Errorf("the CLI was called %d times, want once: a green record for this commit means the build already happened", len(calls))
	}
}

// TestReleaseRehearsalBuildsTheCLIFromTheCommit: the rehearsal runs a CLI built
// from the tree it is in -- not whatever `fugaro` happens to be installed. The
// fake `go` stands in for the toolchain (the fixture holds no module) and
// answers the build by handing back the fake fugaro, so the arm that builds is
// exercised: `go build -o <path> ./cmd/fugaro`, from the checkout, and then that
// binary is what makes the call.
func TestReleaseRehearsalBuildsTheCLIFromTheCommit(t *testing.T) {
	r := newReleaseRepo(t)
	sha := testutil.Git(t, r.local, "rev-parse", "HEAD")
	bin := fakeBinDir(t)
	goState := t.TempDir()
	goDir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(), "scripts", "testdata", "fake-go.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "go"), src, 0o755); err != nil {
		t.Fatal(err)
	}

	res := runRehearsal(t, r, []string{
		"PATH=" + goDir + ":" + bin + ":" + os.Getenv("PATH"),
		// The default arm: build the CLI instead of being handed one.
		"RELEASE_REHEARSAL_FUGARO=",
		"FAKE_GO_DIR=" + goState,
		"FAKE_GO_FUGARO=" + filepath.Join(bin, "fugaro"),
	}, "run", sha)
	res.requireSuccess(t)

	log, err := os.ReadFile(filepath.Join(goState, "log"))
	if err != nil {
		t.Fatalf("the rehearsal did not build a CLI at all: %v", err)
	}
	if !strings.Contains(string(log), "cwd "+r.local+"\n") {
		t.Errorf("the build did not run in the checkout (%s):\n%s", r.local, log)
	}
	if !strings.Contains(string(log), "./cmd/fugaro") {
		t.Errorf("the build did not ask for this repository's CLI:\n%s", log)
	}
	if calls := fugaroCalls(t, r); len(calls) != 1 || calls[0] != "image build go --gcp-project "+rehearsalSandbox {
		t.Errorf("the CLI the build produced made %v, want the one catalog-build call", calls)
	}
	if !strings.Contains(rehearsalRecord(t, r), "verdict=pass\n") {
		t.Errorf("the build ran but the record is not green:\n%s", rehearsalRecord(t, r))
	}
}

// TestReleaseRehearsesTheMergedCommitBeforeTheTag: the whole CLI path, with the
// fake CLI. The record the tag was gated on names the very commit the tag points
// at, and it was written by one build of the entry.
func TestReleaseRehearsesTheMergedCommitBeforeTheTag(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())

	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)

	tagSHA := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}")
	record := rehearsalRecord(t, r)
	if !strings.Contains(record, "commit="+tagSHA+"\n") || !strings.Contains(record, "verdict=pass\n") {
		t.Errorf("the tag is on %s and the record does not rehearse it:\n%s", tagSHA, record)
	}
	if calls := fugaroCalls(t, r); len(calls) != 1 {
		t.Errorf("the release made %d catalog builds, want exactly one:\n%v", len(calls), calls)
	}
	if !strings.Contains(res.out, "rehearsal: this repository's own go catalog entry built for real") {
		t.Errorf("the release summary does not say the rehearsal happened:\n%s", res.out)
	}
}

// TestReleaseRefusesTheTagOnARedRehearsal: a catalog build that fails stops the
// release before the tag, with the Cloud Build link, and nothing outward-facing
// happens after it.
func TestReleaseRefusesTheTagOnARedRehearsal(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_FUGARO_STATUS=1")

	res := runRelease(t, r, g, "", "0.12.0")

	res.requireFailureContaining(t, "did not build the go catalog entry")
	res.requireFailureContaining(t, "https://console.cloud.google.com/cloud-build/builds/fake-build-1")
	requireNoTag(t, r, res)
}

// TestReleaseRefusesWithoutASandboxProject: the rehearsal is the release's own
// step, so a run that cannot say which sandbox to build in refuses in the
// preconditions -- before the branch, the pull request or the merge, not after
// them at the point the step would run.
func TestReleaseRefusesWithoutASandboxProject(t *testing.T) {
	for _, tc := range []struct {
		name, project, want string
	}{
		{"belong", "belong-prod", "carries 'belong'"},
		{"unset", "", "FUGARO_LIVE_GCP_PROJECT is not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks()).with("FUGARO_LIVE_GCP_PROJECT=" + tc.project)

			res := runRelease(t, r, g, "", "0.12.0")

			res.requireFailureContaining(t, tc.want)
			requireNoTag(t, r, res)
			if refExists(t, r.bare, "refs/heads/release/v0.12.0") {
				t.Error("a run refused for the sandbox project created the release branch: the refusal is a precondition, ahead of anything outward-facing")
			}
			if n := countLog(t, g, "pr create"); n != 0 {
				t.Errorf("the run opened %d pull request(s) before it refused", n)
			}
			requireNoRehearsalCalls(t, r)
		})
	}
}
