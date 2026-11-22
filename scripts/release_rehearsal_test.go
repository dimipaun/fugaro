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

// TestReleaseRehearsalRunsInACodingAgentsSession: the rehearsal holds no fence
// against a session, and the markers are set here to pin that it does not come
// back. The owner's ruling of 2026-11-19 is why: `run` builds in the sandbox
// project only (guard_project) and tags nothing, so a session that runs it can
// spoil nothing outward-facing. What stays the person's is the release around
// it, and scripts/release.sh no longer refuses a coding agent's session either
// (TestReleaseRunsInACodingAgentsSession, the owner's order of 2026-11-21).
//
// The CLI's own gate is not such a fence either: it admits a coding agent's
// session whose plan it admitted and prints AgentProceeded there
// (internal/cli/image_build_managed.go), refusing only where no verdict came --
// and internal/cli's own TestImageBuildManagedAnAgentSessionIsAdmittedAndRecorded
// pins that an agent's admitted build IS submitted -- which is why nothing here
// leans on it.
func TestReleaseRehearsalRunsInACodingAgentsSession(t *testing.T) {
	for _, tc := range []struct {
		name, marker, value string
	}{
		{"agent-set marker", "CLAUDECODE", "1"},
		{"another agent's marker", "AI_AGENT", "1"},
		// The IDE extension's marker alone is an agent's session only where no
		// real terminal is attached (agentenv.Session), and a test run has none.
		{"the IDE marker with no terminal", "CLAUDE_CODE_SSE_PORT", "12345"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			sha := testutil.Git(t, r.local, "rev-parse", "HEAD")

			res := runRehearsal(t, r, []string{tc.marker + "=" + tc.value}, "run", sha)

			res.requireSuccess(t)
			calls := fugaroCalls(t, r)
			want := "image build go --gcp-project " + rehearsalSandbox
			if len(calls) != 1 || calls[0] != want {
				t.Errorf("the rehearsal made %v, want exactly one %q: a coding agent's session builds the entry it names in the sandbox it was given, the same as any other", calls, want)
			}
			record := rehearsalRecord(t, r)
			if !strings.Contains(record, "commit="+sha) || !strings.Contains(record, "verdict=pass") {
				t.Errorf("the rehearsal of a coding agent's session did not record a green verdict for %s:\n%s", sha, record)
			}
		})
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
// it builds in, and the owner's own is not it under any of the names it answers
// to (scripts/belong.sh: `belong` anywhere, and `edge-devel-dimi` and
// `edgeappinc`, the ids `internal/orchestrator.NeverBelong` and the register's
// own checks refuse). Each refuses before the CLI is called at all, so nothing
// is built and no record is written -- the CLI does not refuse these names for
// it (NeverBelong is `fugaro orchestrator serve`'s), which is why the case is
// pinned here rather than left to the build.
func TestReleaseRehearsalRefusesTheOwnersProject(t *testing.T) {
	for _, tc := range []struct {
		name, project, want string
	}{
		{"the word", "belong-prod", "names 'belong-prod', the owner's own installation"},
		{"the GCP project the owner's resources run in", "edge-devel-dimi", "names 'edge-devel-dimi', the owner's own installation"},
		{"the Firebase project the owner's database is", "fugaro-belong", "names 'fugaro-belong', the owner's own installation"},
		{"the workspace the sandbox repository lives in", "edgeappinc-sandbox", "names 'edgeappinc-sandbox', the owner's own installation"},
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
	// The fake logs the cwd a shell reports, which is getcwd() and not the
	// path the test handed out: on macOS a temp dir under /var comes back out
	// of /private/var, and the comparison is between resolved paths so that
	// difference is not read as a build in the wrong directory.
	local, err := filepath.EvalSymlinks(r.local)
	if err != nil {
		t.Fatalf("resolving the checkout's path (%s): %v", r.local, err)
	}
	if !strings.Contains(string(log), "cwd "+local+"\n") {
		t.Errorf("the build did not run in the checkout (%s, as %s):\n%s", r.local, local, log)
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

// TestReleaseRunsInACodingAgentsSession: the owner's order of 2026-11-21 removed
// the refusal of a coding agent's session ("Remove the guard"; the release is
// not the person's alone). A session that carries an agent's marker cuts the
// tag the same as any other, and it builds the rehearsal once.
func TestReleaseRunsInACodingAgentsSession(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("CLAUDECODE=1")

	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)

	if !refExists(t, r.bare, "refs/tags/v0.12.0") {
		t.Errorf("a coding agent's session did not reach the tag:\n%s", res.out)
	}
	if calls := fugaroCalls(t, r); len(calls) != 1 {
		t.Errorf("the release made %d catalog builds, want exactly one:\n%v", len(calls), calls)
	}
}

// TestReleaseRefusesWithoutASandboxProject: the rehearsal is the release's own
// step, so a run that cannot say which sandbox to build in refuses in the
// preconditions -- before the branch, the pull request or the merge, not after
// them at the point the step would run. The names it refuses are the owner's
// own, all of them, the same list scripts/release-rehearsal.sh holds: a release
// pointed at `edge-devel-dimi` would otherwise build in the owner's project for
// real, the CLI admitting that name.
func TestReleaseRefusesWithoutASandboxProject(t *testing.T) {
	for _, tc := range []struct {
		name, project, want string
	}{
		{"the word", "belong-prod", "names 'belong-prod', the owner's own installation"},
		{"the GCP project the owner's resources run in", "edge-devel-dimi", "names 'edge-devel-dimi', the owner's own installation"},
		{"the Firebase project the owner's database is", "fugaro-belong", "names 'fugaro-belong', the owner's own installation"},
		{"the workspace the sandbox repository lives in", "edgeappinc-sandbox", "names 'edgeappinc-sandbox', the owner's own installation"},
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

// TestReleaseSkipRehearsalCutsTheTagWithoutOne: the owner's ruling of 2026-11-21
// is one release without the pre-tag rehearsal, because the sandbox it builds
// in was not set up. The flag is the whole of the exemption: with no sandbox
// project named the run still reaches the tag, the fake CLI is never called, no
// record is read, and the summary and stderr both say the rehearsal is owed.
func TestReleaseSkipRehearsalCutsTheTagWithoutOne(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FUGARO_LIVE_GCP_PROJECT=")

	res := runRelease(t, r, g, "", "0.12.0", "--skip-rehearsal")
	res.requireSuccess(t)

	if !refExists(t, r.bare, "refs/tags/v0.12.0") {
		t.Errorf("the run with --skip-rehearsal did not cut the tag:\n%s", res.out)
	}
	requireNoRehearsalCalls(t, r)
	for _, want := range []string{
		"--skip-rehearsal: this release is cut WITHOUT the pre-tag rehearsal",
		"rehearsal: SKIPPED on the owner's explicit word of 2026-11-21",
		"it is owed",
	} {
		if !strings.Contains(res.out, want) {
			t.Errorf("the output does not say %q:\n%s", want, res.out)
		}
	}
	if strings.Contains(res.out, "rehearsal: this repository's own go catalog entry built for real") {
		t.Errorf("the summary claims a rehearsal that did not run:\n%s", res.out)
	}
}

// TestReleaseSkipRehearsalIsOnlyWhatTheFlagSays: without the flag nothing about
// the rehearsal changed -- a run with no sandbox project still refuses before the
// branch -- and the dry-run plan with the flag names the skipped step instead of
// the billable build.
func TestReleaseSkipRehearsalIsOnlyWhatTheFlagSays(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FUGARO_LIVE_GCP_PROJECT=")

	runRelease(t, r, g, "", "0.12.0").requireFailureContaining(t, "FUGARO_LIVE_GCP_PROJECT is not set")

	dry := runRelease(t, r, g, "", "0.12.0", "--dry-run", "--skip-rehearsal")
	dry.requireSuccess(t)
	if !strings.Contains(dry.out, "4b. SKIPPED: --skip-rehearsal") {
		t.Errorf("the dry-run plan does not name the skipped rehearsal:\n%s", dry.out)
	}
	if strings.Contains(dry.out, "ONE billable Cloud Build") {
		t.Errorf("the dry-run plan still lists the billable build:\n%s", dry.out)
	}
	requireNoRehearsalCalls(t, r)
}
