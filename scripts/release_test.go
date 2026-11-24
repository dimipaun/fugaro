// Package scripts_test exercises scripts/release.sh end to end against a
// local bare repository standing in for origin, and a fake `gh` on PATH
// that performs a real squash merge against that repo so step 4's
// verification (fetching the merge commit, checking the plugin bump and
// the release gate) runs for real too.
package scripts_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// releaseRepo is a bare "origin" seeded with this checkout's scripts/, plugin/
// and images/ trees, and a clone of it (on main) to run release.sh from.
type releaseRepo struct {
	bare  string
	local string
	// recordDir is where scripts/release-rehearsal.sh writes its record
	// (RELEASE_REHEARSAL_DIR), and fugaroState the directory the fake fugaro
	// logs its calls to (FAKE_FUGARO_DIR): the pre-tag rehearsal runs on the
	// path to every tag these tests cut, so every run gets both.
	recordDir   string
	fugaroState string
}

func newReleaseRepo(t *testing.T) *releaseRepo {
	t.Helper()
	testutil.IsolateGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	testutil.Git(t, root, "init", "--quiet", "--bare", "-b", "main", bare)
	testutil.Git(t, bare, "config", "receive.autogc", "false")
	testutil.Git(t, bare, "config", "maintenance.auto", "false")

	seed := filepath.Join(root, "seed")
	testutil.Git(t, root, "clone", "--quiet", bare, seed)
	moduleRoot := testutil.ModuleRoot()
	// images/ travels with the seed too: scripts/verify-release.sh reads the
	// tagged tree to decide which images a release published (images/$kind/
	// Dockerfile, the way images.yml does) and calls images/verify-public.sh
	// for them, which the fake docker on PATH answers.
	for _, name := range []string{"scripts", "plugin", "images"} {
		if err := os.CopyFS(filepath.Join(seed, name), os.DirFS(filepath.Join(moduleRoot, name))); err != nil {
			t.Fatalf("copying %s: %v", name, err)
		}
	}
	// The copied plugin carries the repository's own version, which is the last
	// release's and moves with every one: a test cutting 0.12.0 would find the
	// plugin already there once 0.12.0 ships and its bump commit would be empty.
	// Pin the fixture's plugin below every version these tests release.
	pin := exec.Command("bash", filepath.Join(seed, "scripts", "bump-plugin-version.sh"), "0.11.4")
	if out, err := pin.CombinedOutput(); err != nil {
		t.Fatalf("pinning the fixture plugin version: %v\n%s", err, out)
	}
	// scripts/release.sh reads the CLI stream out of docs/release-streams.yaml,
	// so the fixture carries its own, pinned: the repository's own file moves
	// with every release, and a test reading it would assert against whatever
	// the last release left there (streamsFixture).
	writeStreams(t, seed, streamsFixture)
	// The release commit rewrites the two doc lines that name the released
	// version (releasedVersionDocs), so the fixture carries both, at the shape
	// the real files have.
	writeReleasedVersionDocs(t, seed, "0.11.4")
	// And the compatibility matrix's "as of" stamp, which the release's own PR
	// moves with the notes file: the seed's names the version before the one
	// these tests release, as a tree that has not been through the release's PR
	// does.
	writeCompatibility(t, seed, "0.11.4")
	// Releases from 0.4.0 need docs/releases/vX.Y.Z.md on main and an entry in
	// website/data/release-summaries.yaml; give the versions these tests release
	// both by default.
	for _, v := range []string{"0.12.0", "0.12.10"} {
		writeHighlights(t, seed, v, "- A user-facing change.\n")
	}
	writeSummaries(t, seed, map[string]string{
		"0.12.0":  "A user-facing change.",
		"0.12.10": "A user-facing change.",
	})
	// The default release these fixtures cut is 0.12.0, so the seed's
	// FEATURES.md is stamped for it; a test that releases another version
	// stamps its own (addReleaseNotes, stampFeatures).
	writeFeatures(t, seed, "0.12.0")
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "seed")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")

	local := filepath.Join(root, "work")
	testutil.Git(t, root, "clone", "--quiet", bare, local)
	return &releaseRepo{bare: bare, local: local, recordDir: t.TempDir(), fugaroState: t.TempDir()}
}

func writeHighlights(t *testing.T, dir, version, body string) {
	t.Helper()
	path := filepath.Join(dir, "docs", "releases", "v"+version+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeFeatures writes the fixture's FEATURES.md carrying the release's stamp,
// in the shape docs/feature-matrix/contract.md fixes for the line release.sh
// greps: `as_of: vX.Y.Z`, as a line of its own. The release's own PR writes it
// with the notes (docs/release.md, "Before you tag"), so a fixture that makes a
// version releasable stamps it for that version, the way writeHighlights and
// writeSummaries write the release's other two files.
func writeFeatures(t *testing.T, dir, version string) {
	t.Helper()
	path := filepath.Join(dir, "FEATURES.md")
	body := "# Features\n\nas_of: v" + version + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stampFeatures writes FEATURES.md stamped for version on the fixture's main and
// commits it, which is what the release's own PR does: release.sh refuses an
// origin/main whose FEATURES.md carries no commit since the previous release
// tag (D2449), so a fixture that puts a tag in front of one needs this rather
// than a file already in the seed.
func stampFeatures(t *testing.T, r *releaseRepo, version string) {
	t.Helper()
	writeFeatures(t, r.local, version)
	testutil.Git(t, r.local, "add", "FEATURES.md")
	testutil.Git(t, r.local, "commit", "--quiet", "-m", "features: the matrix as of v"+version)
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
}

// streamsFixture is the fixture checkout's docs/release-streams.yaml: the real
// file's shape, with the CLI stream at 0.0.0 (nothing shipped) and Badger next,
// so every version on the CLI's named line (0.10 and up) is above its floor.
// The versions this package's release tests cut are on that line -- 0.12.0
// "Dolphin" is the default -- because the release refuses one the catalogue
// cannot record (a major bump, a minor below 0.10). Tests that need a stream
// with a spent name (the release's spoken name, "Camel two" for 0.11.2) install
// streamsFixtureReleased instead.
const streamsFixture = `streams:
  - name: cli
    theme: animals
    names: [Badger, Camel, Dolphin, Gecko, Hippo, Koala, Moose]
    current: "0.0.0"
    next: Badger
    tag_prefix: v

  - name: console
    theme: fruits
    names: [Cherry, Mango, Melon]
    current: "0.111.0"
    next: Melon
    tag_prefix: console-v

  - name: mobile
    theme: birds
    names: [Falcon, Pelican, Robin]
    current: "0.0.0"
    next: Falcon
    tag_prefix: mobile-v
`

// streamsFixtureReleased is that catalogue with the CLI stream where the real
// file has it: 0.11.1 "Camel one" shipped, Dolphin next. A test installing it
// can release 0.11.2 and read both the name the catalogue gives it and the
// version it writes back.
const streamsFixtureReleased = `streams:
  - name: cli
    theme: animals
    names: [Badger, Camel, Dolphin, Gecko, Hippo, Koala, Moose]
    current: "0.11.1"
    next: Dolphin
    tag_prefix: v

  - name: console
    theme: fruits
    names: [Cherry, Mango, Melon]
    current: "0.111.0"
    next: Melon
    tag_prefix: console-v

  - name: mobile
    theme: birds
    names: [Falcon, Pelican, Robin]
    current: "0.0.0"
    next: Falcon
    tag_prefix: mobile-v
`

// writeStreams writes a streams catalogue into dir, at the path release.sh
// reads it from.
func writeStreams(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "docs", "release-streams.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rewriteFixtureStreams replaces the fixture checkout's catalogue with body and
// commits it on main, so the clean-tree and main-equals-origin/main
// preconditions still hold for a run that has to read it.
func rewriteFixtureStreams(t *testing.T, r *releaseRepo, body string) {
	t.Helper()
	writeStreams(t, r.local, body)
	testutil.Git(t, r.local, "add", "docs/release-streams.yaml")
	testutil.Git(t, r.local, "commit", "--quiet", "-m", "fixture: streams catalogue")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
}

// addReleaseNotes writes the Highlights, the end-user summary and the
// FEATURES.md stamp a release needs -- the three files its own PR carries
// (docs/release.md, "Before you tag") -- commits them on main and pushes them,
// so a test can release a version of its own.
func addReleaseNotes(t *testing.T, r *releaseRepo, version string) {
	t.Helper()
	writeHighlights(t, r.local, version, "- Something a user sees.\n")
	writeSummaries(t, r.local, map[string]string{version: "Something a user sees."})
	// The release's own PR carries FEATURES.md too (docs/release.md, "Before
	// you tag"), so the file is stamped for the version being released.
	writeFeatures(t, r.local, version)
	testutil.Git(t, r.local, "add", "-A")
	testutil.Git(t, r.local, "commit", "--quiet", "-m", "release notes")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
}

// ghState is the directory backing scripts/testdata/fake-gh.sh's state
// across the several `gh` invocations one release.sh run makes.
type ghState struct {
	dir string
	env []string
}

func newGHState(t *testing.T, r *releaseRepo, checks map[string]string) *ghState {
	t.Helper()
	dir := t.TempDir()
	env := []string{
		"FAKE_GH_DIR=" + dir,
		"FAKE_GH_ORIGIN=" + r.bare,
		"FAKE_GH_REPO=" + repoSlug(r.bare),
	}
	for name, concl := range checks {
		env = append(env, "FAKE_GH_CHECK_"+strings.ToUpper(name)+"="+concl)
	}
	return &ghState{dir: dir, env: env}
}

// writeSummaries writes website/data/release-summaries.yaml with one entry per
// version, in the shape the file documents (`  vX.Y.Z: the sentence` under
// `summaries:`, website/data/release-summaries.yaml). A version whose sentence
// is blank writes the tag with nothing after the colon, which is the shape
// release.sh refuses.
func writeSummaries(t *testing.T, dir string, entries map[string]string) {
	t.Helper()
	path := filepath.Join(dir, "website", "data", "release-summaries.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		"version: 1",
		"summaries:",
	}
	// Sorted, so a case's fixture file is the same on every run.
	versions := make([]string, 0, len(entries))
	for v := range entries {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	for _, v := range versions {
		lines = append(lines, "  v"+v+": "+entries[v])
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (g *ghState) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (g *ghState) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.dir, name))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (g *ghState) log(t *testing.T) string { return g.read(t, "log") }

// countLog returns how many logged gh invocations start with prefix (one
// per line, as fake-gh.sh writes "$*").
func countLog(t *testing.T, g *ghState, prefix string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(g.log(t), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// lockedBuffer is a bytes.Buffer safe to read while a command still writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// viewsAfterMerge counts the "pr view" calls logged after the first "pr merge".
func viewsAfterMerge(t *testing.T, g *ghState) int {
	t.Helper()
	merged, n := false, 0
	for _, line := range strings.Split(g.log(t), "\n") {
		switch {
		case strings.HasPrefix(line, "pr merge"):
			merged = true
		case merged && strings.HasPrefix(line, "pr view"):
			n++
		}
	}
	return n
}

// releaseProcs lists the processes of the given group, or running release.sh,
// as a diagnostic for a script that will not die.
func releaseProcs(pgid int) string {
	out, err := exec.Command("ps", "-axo", "pid,ppid,pgid,stat,command").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("ps failed: %v", err)
	}
	var keep []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && (f[2] == fmt.Sprint(pgid) || strings.Contains(line, "release.sh")) {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// fakeBinDir returns a directory on PATH ahead of the real one, holding the
// fakes for gh, docker and fugaro (and nothing else: git, bash, sed, awk, sort,
// date stay real). The fake docker answers images/verify-public.sh's three
// calls, which scripts/verify-release.sh makes for every image a release
// published; the fake fugaro is the CLI the pre-tag rehearsal runs.
func fakeBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fakeBinDirAt(t, dir)
	return dir
}

// fakeBinDirAt writes those three fakes into a directory the caller owns, so a
// test can put one of its own beside them (runReleaseFrom takes such a
// directory).
func fakeBinDirAt(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"gh", "docker", "fugaro"} {
		src, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(), "scripts", "testdata", "fake-"+name+".sh"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// streamsBin is the compiled internal/streams/cmd/streams, the helper
// release.sh reads the catalogue through.
var streamsBin struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// streamsHelper builds that helper once and returns its path. The fixture
// checkout holds scripts/ and plugin/ and no go module, so release.sh's own
// `go run ./internal/streams/cmd/streams` cannot run there -- the same reason
// the plugin budget check is stubbed -- and every run in this package is handed
// the compiled binary as RELEASE_SH_STREAMS_CMD instead: the code the default
// runs, built from this checkout's module.
func streamsHelper(t *testing.T) string {
	t.Helper()
	streamsBin.once.Do(func() {
		dir, err := os.MkdirTemp("", "fugaro-streams-helper-")
		if err != nil {
			streamsBin.err = err
			return
		}
		streamsBin.dir = dir
		path := filepath.Join(dir, "streams")
		cmd := exec.Command("go", "build", "-o", path, "./internal/streams/cmd/streams")
		cmd.Dir = testutil.ModuleRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			streamsBin.err = fmt.Errorf("go build ./internal/streams/cmd/streams: %v\n%s", err, out)
			return
		}
		streamsBin.path = path
	})
	if streamsBin.err != nil {
		t.Fatalf("every release.sh run reads the catalogue through this helper, and it did not build: %v", streamsBin.err)
	}
	return streamsBin.path
}

// rehearsalSandbox is the sandbox project name every release.sh run in this
// package is handed: the pre-tag rehearsal builds in it and nowhere else, so a
// run without it refuses before the branch (scripts/release.sh).
const rehearsalSandbox = "fake-sandbox"

// rehearsalEnv is what a release.sh run needs for the pre-tag rehearsal
// (scripts/release-rehearsal.sh): the sandbox project's name, the directory its
// record goes in, and the fake fugaro -- the CLI the rehearsal would otherwise
// build from the commit, which the fixture checkout cannot (it holds no go
// module, the same reason RELEASE_SH_STREAMS_CMD exists). A test that wants a
// red, missing or other-commit record overrides these with g.with(...) or by
// writing the record file itself.
//
// It also clears the coding-agent markers (internal/agentenv's list): the
// fixture is a person at a terminal. Since the owner's order of 2026-11-21
// release.sh refuses no agent's session, so this is no longer about a refusal,
// only about every run here starting from the same environment. A test that
// means to be an agent's session sets one of them last
// (g.with("CLAUDECODE=1"), or extra in runRehearsal), which wins over this.
func rehearsalEnv(r *releaseRepo, bin string) []string {
	return []string{
		"FUGARO_LIVE_GCP_PROJECT=" + rehearsalSandbox,
		"RELEASE_REHEARSAL_DIR=" + r.recordDir,
		"RELEASE_REHEARSAL_FUGARO=" + filepath.Join(bin, "fugaro"),
		"FAKE_FUGARO_DIR=" + r.fugaroState,
		"CLAUDECODE=",
		"CLAUDE_CODE_ENTRYPOINT=",
		"CLAUDE_CODE_REMOTE=",
		"CLAUDE_CODE_SSE_PORT=",
		"CURSOR_AGENT=",
		"AI_AGENT=",
	}
}

// releaseEnv is what a release.sh run needs on top of the process environment:
// the streams helper, which the module-less fixture cannot run by itself.
func releaseEnv(t *testing.T) []string {
	t.Helper()
	return []string{"RELEASE_SH_STREAMS_CMD=" + streamsHelper(t)}
}

// TestMain removes the directory the helper was built into: it is created once
// for the whole package, so no single test can clean it up.
func TestMain(m *testing.M) {
	code := m.Run()
	if streamsBin.dir != "" {
		_ = os.RemoveAll(streamsBin.dir)
	}
	os.Exit(code)
}

// repoSlug is the owner/name release.sh derives from origin's URL: the last
// two path components, minus ".git" (origin here is a local path).
func repoSlug(bare string) string {
	return filepath.Base(filepath.Dir(bare)) + "/" + strings.TrimSuffix(filepath.Base(bare), ".git")
}

type runResult struct {
	out string
	err error
}

func (r runResult) requireSuccess(t *testing.T) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("release.sh failed: %v\n%s", r.err, r.out)
	}
}

func (r runResult) requireFailureContaining(t *testing.T, substr string) {
	t.Helper()
	if r.err == nil {
		t.Fatalf("release.sh unexpectedly succeeded:\n%s", r.out)
	}
	if !strings.Contains(r.out, substr) {
		t.Fatalf("release.sh output does not contain %q:\n%s", substr, r.out)
	}
}

// runRelease runs scripts/release.sh from r.local with the fake gh on PATH,
// g's state, stdin, and extra arguments.
func runRelease(t *testing.T, r *releaseRepo, g *ghState, stdin string, args ...string) runResult {
	t.Helper()
	return runReleaseFrom(t, r, g, fakeBinDir(t), stdin, args...)
}

// runReleaseFrom is runRelease with the directory that goes in front of PATH
// named by the test, for the one case that needs a fake of its own beside the
// three: a `git` wrapper, which fakes a checkout with no `main` ref
// (TestAtNeedsNoLocalMainRef).
func runReleaseFrom(t *testing.T, r *releaseRepo, g *ghState, bin, stdin string, args ...string) runResult {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(r.local, "scripts", "release.sh")}, args...)...)
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1", "RELEASE_RETRY_SECONDS=0",
		// The verification release.sh ends with waits on the tag's runs and the
		// images; the fake answers both at once, and these keep a run whose
		// wait does not end from hanging a test for 45 minutes.
		"VERIFY_RELEASE_POLL_SECONDS=1", "VERIFY_RELEASE_TIMEOUT_SECONDS=60")
	// The helper first and the test's own environment after it, so a test can
	// unset RELEASE_SH_STREAMS_CMD (an empty value is the default `go run`).
	cmd.Env = append(cmd.Env, releaseEnv(t)...)
	cmd.Env = append(cmd.Env, rehearsalEnv(r, bin)...)
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
}

// greenChecks is every check scripts/release-gate.sh requires, green, keyed by
// the check name. TestFakeGHAnswersEveryCheckTheGateRequires reads the gate's
// own `names` array and fails if a name it holds has no key here or no arm in
// the fake.
func greenChecks() map[string]string {
	return map[string]string{"test": "success", "terraform": "success", "rules": "success"}
}

// TestFakeGHAnswersEveryCheckTheGateRequires ties the three lists that have to
// agree about what the gate requires -- the gate's `names` array, greenChecks
// and scripts/testdata/fake-gh.sh's arms -- which nothing did before: the fake
// ended with `echo success` for any name it did not recognise, so a sixth name
// added to the gate that no workflow reports left every release.sh gate test
// green, the fake vouching for a check nobody produces. The names are read from
// the gate rather than repeated, and each one is actually asked of the fake, so
// a missing arm fails here instead of passing on a default.
func TestFakeGHAnswersEveryCheckTheGateRequires(t *testing.T) {
	moduleRoot := testutil.ModuleRoot()
	script, err := os.ReadFile(filepath.Join(moduleRoot, "scripts", "release-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	names := releaseGateNames(t, string(script))
	if len(names) == 0 {
		t.Fatal("read no names from scripts/release-gate.sh: this test reads the gate's own required set")
	}

	// Every required name's stem is a key greenChecks covers, so a new check in
	// the gate cannot be green-by-accident in the release.sh fixtures.
	green := greenChecks()
	for _, name := range names {
		stem, _, _ := strings.Cut(name, " (")
		if _, ok := green[stem]; !ok {
			t.Errorf("scripts/release-gate.sh requires %q but greenChecks() has no %q: the release.sh tests would set no conclusion for it, and a check nobody sets is a check the fixtures cannot fail", name, stem)
		}
	}

	// And the fake answers each name: its arm matches, so it does not fall
	// through to the error it now ends with.
	testutil.IsolateGit(t)
	origin := testutil.NewRemote(t, map[string]string{"README.md": "seed\n"})
	sha := testutil.Git(t, origin, "rev-parse", "HEAD")
	dir := t.TempDir()
	var env []string
	env = append(env,
		"FAKE_GH_DIR="+dir,
		"FAKE_GH_ORIGIN="+origin,
		"FAKE_GH_REPO=o/r",
		"FAKE_GH_STATE="+filepath.Join(dir, "target.tsv"),
	)
	if err := os.WriteFile(filepath.Join(dir, "target.tsv"), []byte(strings.Join(greenCheckRunLines(names), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for stem, conclusion := range green {
		env = append(env, "FAKE_GH_CHECK_"+strings.ToUpper(stem)+"="+conclusion)
	}
	bin := fakeBinDir(t)

	ask := func(name string) (string, error) {
		// The gate's own check_state shape, so the fake's arm matches the way it
		// does in a real run: the name and the app slug in one select.
		expr := fmt.Sprintf(`[.check_runs[] | select(.name == %q and .app.slug == "github-actions")] | length`, name)
		cmd := exec.Command(filepath.Join(bin, "gh"), "api",
			"repos/o/r/commits/"+sha+"/check-runs?per_page=100", "--jq", expr)
		cmd.Env = append(os.Environ(), env...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.String(), err
	}

	for _, name := range names {
		out, err := ask(name)
		if err != nil {
			t.Errorf("the fake gh refused %q, which scripts/release-gate.sh requires: %v\n%s", name, err, out)
			continue
		}
		if strings.TrimSpace(out) == "0" {
			t.Errorf("the fake gh answered no run for %q: the fixtures set %q green, so a name with no run is a release.sh test passing on a check that does not exist", name, name)
		}
	}
	// The fail-closed half: a name with no arm is an error, not a default
	// success. This is the mutation-proof assertion for the line that used to
	// read `echo success`.
	if out, err := ask("lint"); err == nil {
		t.Errorf("the fake gh answered %q, a check it has no arm for, with %q: a fake that vouches for any name keeps the release.sh tests green when a name nobody reports enters the gate", "lint", strings.TrimSpace(out))
	}
}

// releaseGateNames reads the required-check set out of scripts/release-gate.sh:
// the quoted strings of its single `names=(...)` array, in the order the script
// has them. It is how this package's tests read the gate rather than repeating
// it (the docs test internal/cli/docs_desktop_console_test.go reads the same
// line and pins it to test, terraform and rules).
func releaseGateNames(t *testing.T, script string) []string {
	t.Helper()
	line := ""
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(l, "names=(") {
			if line != "" {
				t.Fatalf("scripts/release-gate.sh has more than one `names=(` line: this test cannot tell which set the gate requires")
			}
			line = l
		}
	}
	if line == "" {
		t.Fatal("scripts/release-gate.sh has no `names=(...)` line: the required set is not readable, and a whitespace-split `names=` string cannot hold a name with a space in it")
	}
	var names []string
	for _, quoted := range quoteRE.FindAllStringSubmatch(line, -1) {
		names = append(names, quoted[1])
	}
	return names
}

// quoteRE matches one `"..."` element of the gate's names array.
var quoteRE = regexp.MustCompile(`"([^"]*)"`)

// greenCheckRunLines is the fake's state file for a fully green commit: one
// `name<TAB>app<TAB>state` line per required check run.
func greenCheckRunLines(names []string) []string {
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+"\tgithub-actions\tsuccess")
	}
	return lines
}

// TestPreconditionFailures checks every precondition stops the script with
// a clear message before anything is created, in the order docs/release.md
// (via the task) specifies.
func TestPreconditionFailures(t *testing.T) {
	cases := []struct {
		name    string
		version string
		mutate  func(t *testing.T, r *releaseRepo, g *ghState)
		want    string
	}{
		{
			name:    "not strict semver (leading v)",
			version: "v0.12.0",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "not strict SemVer",
		},
		{
			name:    "not strict semver (pre-release)",
			version: "0.12.0-rc1",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "not strict SemVer",
		},
		{
			name:    "not strict semver (leading zero)",
			version: "01.2.3",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "not strict SemVer",
		},
		{
			name:    "dirty working tree",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				if err := os.WriteFile(filepath.Join(r.local, "dirty.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "working tree is not clean",
		},
		{
			name:    "not on main",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "switch", "--quiet", "-c", "other")
			},
			want: "not main",
		},
		{
			name:    "main diverged from origin",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "commit", "--quiet", "--allow-empty", "-m", "local only")
			},
			want: "is not origin/main",
		},
		{
			name:    "gh not authenticated",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				g.env = append(g.env, "FAKE_GH_UNAUTH=1")
			},
			want: "gh is not authenticated",
		},
		{
			name:    "tag already exists",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "tag", "-a", "v0.12.0", "-m", "x")
			},
			want: "already exists",
		},
		{
			name:    "version not greater than latest tag",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "tag", "-a", "v2.0.0", "-m", "x")
			},
			want: "is not greater than",
		},
		{
			name:    "red check",
			version: "0.12.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				g.env = append(g.env, "FAKE_GH_CHECK_TEST=failed")
			},
			want: "required check 'test' is failed",
		},
		{
			// The seed's FEATURES.md is stamped 0.12.0, so a release of another
			// version finds a stamp that is not its own.
			name:    "FEATURES.md without the release's stamp",
			version: "0.12.10",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "FEATURES.md on main does not carry v0.12.10's stamp",
		},
		{
			// The stamp is right and the file is on main, but nothing has
			// committed it since the previous release tag: the shape D2449
			// refuses, and a stamp written mid-cycle would otherwise pass.
			name:    "FEATURES.md stamped but not committed since the previous tag",
			version: "0.12.10",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				stampFeatures(t, r, "0.12.10")
				testutil.Git(t, r.local, "tag", "-a", "v0.12.9", "-m", "x")
			},
			want: "FEATURES.md on main carries v0.12.10's stamp but has no commit to it since v0.12.9",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			c.mutate(t, r, g)
			res := runRelease(t, r, g, "", c.version)
			res.requireFailureContaining(t, c.want)
			if countLog(t, g, "pr create") != 0 {
				t.Errorf("a precondition failure must not create a pull request:\n%s", res.out)
			}
		})
	}
}

func TestGHNotInstalled(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "0.12.0")
	cmd.Dir = r.local
	// A PATH with no gh at all (just enough for git, bash's builtins, etc).
	realPath := os.Getenv("PATH")
	cmd.Env = append(os.Environ(), "PATH="+realPath)
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Env = append(cmd.Env, releaseEnv(t)...)
	// The pre-tag rehearsal's environment too, although this run can never
	// reach it: the refusal this test is about is 'gh is not installed', and a
	// precondition ahead of it (the sandbox project the release refuses
	// without) would otherwise be the sentence it fails on -- on exactly the
	// machines this test runs on, since it skips wherever gh is installed.
	cmd.Env = append(cmd.Env, rehearsalEnv(r, bin)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// Only meaningful if gh truly isn't already on PATH; skip otherwise.
	if _, err := exec.LookPath("gh"); err == nil {
		t.Skip("gh is installed on this machine's real PATH")
	}
	err := cmd.Run()
	if err == nil || !strings.Contains(out.String(), "gh is not installed") {
		t.Fatalf("want 'gh is not installed', got err=%v out=%s", err, out.String())
	}
}

// TestDryRunCreatesNothing checks --dry-run stops after the plan, with no
// branch, commit, PR or tag.
func TestDryRunCreatesNothing(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "Dry run for v0.12.0") {
		t.Fatalf("expected a dry-run plan, got:\n%s", res.out)
	}
	if countLog(t, g, "pr ") != 0 {
		t.Errorf("dry-run must not touch any pull request; got:\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "branch", "--list", "release/v0.12.0"); out != "" {
		t.Errorf("dry-run created a branch: %s", out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out != "" {
		t.Errorf("dry-run created a tag: %s", out)
	}
}

// writePluginTestStub writes the stand-in for `go test ./plugin/` that
// TestDryRunChecksThePluginBudgetOnABumpedTree puts in
// RELEASE_SH_PLUGIN_TEST_CMD. It runs inside the scratch worktree release.sh
// bumped, so it fails unless that tree really carries the new version -- the
// check under test is what applies the bump -- and it records the path it ran
// in, so the test can assert the worktree is gone afterwards. STUB_RC is the
// status the test wants it to exit with.
func writePluginTestStub(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin-test-stub.sh")
	script := `#!/usr/bin/env bash
set -eu
version=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' plugin/.claude-plugin/plugin.json | head -1)
echo "tree=$PWD version=$version" >> "$STUB_LOG"
[ "$version" = "$STUB_WANT" ] || { echo "plugin.json says $version, want $STUB_WANT" >&2; exit 1; }
grep -q "fugaro-version=$STUB_WANT" plugin/skills/setup/SKILL.md || { echo "the skill headers are not bumped" >&2; exit 1; }
exit "${STUB_RC:-0}"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDryRunChecksThePluginBudgetOnABumpedTree checks that --dry-run applies
// the version bump to a scratch worktree and runs the plugin tests there. The
// bump rewrites two version stamps in every skill file, so it can push a skill
// file over its byte budget by itself: 0.10.0's bump commit did exactly that
// to the setup skill, two bytes over, and failed TestPluginSkillsLintClean on
// main (#929). Nothing else can see it -- the version strings all agree, so
// scripts/bump-plugin-version.sh --check passes on the same tree.
func TestDryRunChecksThePluginBudgetOnABumpedTree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rc       string
		wantFail bool
	}{
		{"the plugin tests pass", "0", false},
		{"the plugin tests fail", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			log := filepath.Join(t.TempDir(), "stub.log")
			g := newGHState(t, r, greenChecks()).with(
				"RELEASE_SH_PLUGIN_TEST_CMD=bash "+writePluginTestStub(t),
				"STUB_LOG="+log, "STUB_WANT=0.12.0", "STUB_RC="+tc.rc)

			res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

			// The stub's one line says where it ran: a worktree of the tree
			// being released, carrying the bump, and removed when the script
			// exited.
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatalf("the plugin test command did not run: %v\n%s", err, res.out)
			}
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) != 1 {
				t.Fatalf("want one plugin test run, got %q:\n%s", lines, res.out)
			}
			scratch := strings.TrimPrefix(strings.Fields(lines[0])[0], "tree=")
			if !strings.Contains(lines[0], "version=0.12.0") {
				t.Errorf("the plugin tests did not run on a bumped tree: %s", lines[0])
			}
			if _, err := os.Stat(scratch); !os.IsNotExist(err) {
				t.Errorf("the scratch worktree %s was not removed (stat: %v)", scratch, err)
			}
			if out := strings.TrimSpace(testutil.Git(t, r.local, "worktree", "list")); strings.Contains(out, "\n") || !strings.Contains(out, r.local) {
				t.Errorf("want exactly the checkout's own worktree left, got:\n%s", out)
			}
			if out := testutil.Git(t, r.local, "status", "--porcelain"); out != "" {
				t.Errorf("--dry-run changed the checkout: %s", out)
			}

			if tc.wantFail {
				res.requireFailureContaining(t, "the plugin tests fail on a tree with the 0.12.0 bump applied")
				if strings.Contains(res.out, "Dry run for v0.12.0") {
					t.Errorf("a failed plugin check must not print the plan:\n%s", res.out)
				}
				return
			}
			res.requireSuccess(t)
			if !strings.Contains(res.out, "the plugin tests pass on a tree with the 0.12.0 bump applied") {
				t.Errorf("want the plugin check reported, got:\n%s", res.out)
			}
			if !strings.Contains(res.out, "Dry run for v0.12.0") {
				t.Errorf("expected a dry-run plan, got:\n%s", res.out)
			}
		})
	}
}

// TestDryRunReportsASkippedPluginBudgetCheck: the fixture repository holds no
// go.mod, so with no RELEASE_SH_PLUGIN_TEST_CMD the check cannot run at all.
// The plan has to say that rather than print the sentence the check exists to
// earn: the check is what --dry-run is for, and a skip reported as a pass is
// worse than no check, because it is read as one.
func TestDryRunReportsASkippedPluginBudgetCheck(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())

	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

	res.requireSuccess(t)
	if !strings.Contains(res.out, "the plugin budget check did not run (this checkout holds no go.mod)") {
		t.Errorf("want the skipped check named in the plan, got:\n%s", res.out)
	}
	if strings.Contains(res.out, "the plugin tests pass on a tree") {
		t.Errorf("a check that did not run must not read as a pass:\n%s", res.out)
	}
	if !strings.Contains(res.out, "Dry run for v0.12.0") {
		t.Errorf("a skipped check is not a refusal, so the plan still prints:\n%s", res.out)
	}
}

// TestDryRunRefusesWhenGoIsMissing: `go` missing from PATH is the one skip that
// is a refusal. The check cannot run, and it is the check the dry run exists
// for, so printing a plan that does not mention it -- or a plan at all -- would
// vouch for a release nothing verified. go.mod is what tells the two cases
// apart: without it there is no module to test and the check is skipped.
func TestDryRunRefusesWhenGoIsMissing(t *testing.T) {
	r := newReleaseRepo(t)
	writeFixtureFile(t, r.local, "go.mod", "module example.com/fixture\n")
	g := newGHState(t, r, greenChecks()).with("PATH=" + pathWithoutGo(t, fakeBinDir(t)))

	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

	res.requireFailureContaining(t, "go is not on PATH")
	if strings.Contains(res.out, "Dry run for v0.12.0") {
		t.Errorf("a refused dry run must not print the plan:\n%s", res.out)
	}
}

// TestReleaseRefusesWhenDockerIsMissing: the release's last step pulls the
// images the release published, so docker missing is one of the preconditions,
// refused before the branch, the pull request and the tag -- not after the tag
// exists, where the verifier would stop with the tag already pushed.
func TestReleaseRefusesWhenDockerIsMissing(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("PATH=" + pathWithoutDocker(t, fakeBinDir(t)))

	res := runRelease(t, r, g, "", "0.12.0")

	res.requireFailureContaining(t, "docker is not on PATH")
	if refExists(t, r.bare, "refs/heads/release/v0.12.0") {
		t.Error("a run refused for docker created the release branch")
	}
	if refExists(t, r.bare, "refs/tags/v0.12.0") {
		t.Error("a run refused for docker tagged the release")
	}
	if n := countLog(t, g, "repo view"); n != 0 {
		t.Errorf("the run reached gh (%d call(s)): docker is refused with the preconditions, before anything outward-facing", n)
	}
}

// writeFixtureFile writes name into the fixture checkout, commits it on main
// and pushes it, so release.sh's clean-tree and main-equals-origin/main
// preconditions still hold when a test needs one more file on main.
func writeFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", name)
	testutil.Git(t, dir, "commit", "--quiet", "-m", "fixture: "+name)
	testutil.Git(t, dir, "push", "--quiet", "origin", "main")
}

// pathWithoutGo returns a directory holding a link to every command on PATH
// except go itself, plus the fake gh in extra (which must shadow any real one).
// Emptying PATH is not an option: the script, the gate and the fake gh all need
// git, mktemp, awk, sed and friends, and there is no way to stub a command out
// of PATH -- so the absence is built by subtraction.
func pathWithoutGo(t *testing.T, extra ...string) string {
	t.Helper()
	return pathWithoutTools(t, []string{"go", "gofmt"}, extra...)
}

// pathWithoutDocker is the same subtraction for docker, which the fake bin dir
// carries: the release refuses without it, because the verifier it ends with
// pulls the images the release published.
func pathWithoutDocker(t *testing.T, extra ...string) string {
	t.Helper()
	return pathWithoutTools(t, []string{"docker"}, extra...)
}

// pathWithoutTools returns a directory holding a link to every command on PATH
// except the names in without, plus the entries in extra first (which must
// shadow any real one). A name is skipped however many directories hold it, so
// the absence holds on a machine that has the tool installed in several places.
func pathWithoutTools(t *testing.T, without []string, extra ...string) string {
	t.Helper()
	skip := make(map[string]bool, len(without))
	for _, name := range without {
		skip[name] = true
	}
	dir := t.TempDir()
	link := func(src string) {
		name := filepath.Base(src)
		if skip[name] {
			return
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil {
			return // an earlier PATH entry already supplied this name
		}
		_ = os.Symlink(src, dst)
	}
	for _, d := range append(append([]string{}, extra...), filepath.SplitList(os.Getenv("PATH"))...) {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				link(filepath.Join(d, e.Name()))
			}
		}
	}
	return dir
}

// writeSignalStub writes the stand-in for `go test ./plugin/` that
// TestDryRunRemovesTheScratchWorktreeOnASignal uses: it records the worktree it
// runs in, then sends the release script the signal a Ctrl-C sends, so the run
// ends through on_signal's exit 130 and not through the check's own cleanup --
// which is the path that used to leave the worktree and its registration
// behind. The script is the OUTERMOST of this stub's ancestors whose command
// line carries its path: the subshells it forks carry the same line. It is
// found by walking up the parent chain rather than by picking the lowest pid
// that matches, because pids wrap and a subshell forked after a wrap has a
// lower pid than the script that forked it -- the signal would then end only
// the subshell, and the run would go on to finish.
func writeSignalStub(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin-test-signal-stub.sh")
	script := `#!/usr/bin/env bash
set -eu
echo "tree=$PWD" >> "$STUB_LOG"
pid=""
p=$PPID
while [ "${p:-0}" -gt 1 ]; do
  case "$(ps -o command= -p "$p" 2>/dev/null || true)" in
  *"$STUB_SCRIPT"*) pid=$p ;;
  esac
  p=$(ps -o ppid= -p "$p" 2>/dev/null | tr -d ' ' || true)
done
[ -n "$pid" ]
kill -TERM "$pid"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDryRunRemovesTheScratchWorktreeOnASignal: the script's header,
// docs/release.md and the check's own comment all say the scratch worktree is
// removed however the check ends. An interrupted run ends through on_signal,
// which exits 130 -- before the EXIT trap held the worktree, that exit left the
// directory and its .git/worktrees entry behind, and the test could not see it
// because no other case interrupts a run.
func TestDryRunRemovesTheScratchWorktreeOnASignal(t *testing.T) {
	r := newReleaseRepo(t)
	log := filepath.Join(t.TempDir(), "stub.log")
	g := newGHState(t, r, greenChecks()).with(
		"RELEASE_SH_PLUGIN_TEST_CMD=bash "+writeSignalStub(t),
		"STUB_LOG="+log,
		"STUB_SCRIPT="+filepath.Join(r.local, "scripts", "release.sh"))

	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

	if res.err == nil {
		t.Fatalf("the interrupted run succeeded:\n%s", res.out)
	}
	if !strings.Contains(res.out, "interrupted") {
		t.Errorf("want the interrupt reported, got:\n%s", res.out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the plugin test command did not run: %v\n%s", err, res.out)
	}
	scratch := strings.TrimPrefix(strings.Fields(strings.TrimSpace(string(b)))[0], "tree=")
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("the scratch worktree %s survived the interrupt (stat: %v)", scratch, err)
	}
	if out := strings.TrimSpace(testutil.Git(t, r.local, "worktree", "list")); strings.Contains(out, "\n") || !strings.Contains(out, r.local) {
		t.Errorf("want no worktree left registered after the interrupt, got:\n%s", out)
	}
}

// TestHappyPathAnsweringYes drives release.sh through a real bump, PR,
// squash merge and the tag (no prompt) — checking that exactly
// one branch, one commit and one PR were created, and the tag matches the
// merge commit.
func TestHappyPathAnsweringYes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	initialMain := testutil.Git(t, r.bare, "rev-parse", "main")

	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)

	if n := countLog(t, g, "pr create"); n != 1 {
		t.Errorf("want exactly one 'pr create', got %d:\n%s", n, g.log(t))
	}
	branchCommits := testutil.Git(t, r.bare, "rev-list", "--count", initialMain+"..refs/heads/release/v0.12.0")
	if branchCommits != "1" {
		t.Errorf("want exactly one commit on the release branch, got %s", branchCommits)
	}
	subject := testutil.Git(t, r.bare, "log", "-1", "--format=%s", "refs/heads/release/v0.12.0")
	if subject != "release: v0.12.0" {
		t.Errorf("unexpected commit subject: %s", subject)
	}
	body := testutil.Git(t, r.bare, "log", "-1", "--format=%b", "refs/heads/release/v0.12.0")
	if !strings.Contains(body, "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>") {
		t.Errorf("commit body is missing the attribution trailer:\n%s", body)
	}

	tagSHA := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}")
	mainSHA := testutil.Git(t, r.bare, "rev-parse", "main")
	if tagSHA != mainSHA {
		t.Errorf("tag v0.12.0 (%s) does not point at main (%s)", tagSHA, mainSHA)
	}
	if mainSHA == initialMain {
		t.Errorf("main did not advance past the merge")
	}
}

// ---- --at: a release cut at a given commit of main -------------------------
//
// A console release tags an exact commit whose checks are green, and the owner
// asked for the same on the CLI (2026-11-21): scripts/release.sh X.Y.Z --at SHA
// tags SHA itself. SHA has to be an ancestor of origin/main that already carries
// the release's whole bump -- plugin version and skill headers, the notes, the
// summary line, the cli stream's current, the two doc lines, the compatibility
// stamp and FEATURES.md's -- and the run opens no branch, no commit and no pull
// request of its own. The gate is read on SHA by sha and nothing is dispatched on
// it, the rehearsal builds SHA's own tree, and the tag is pushed where it is and
// verified the same way. These tests hold the flag to that, and to the ordinary
// path's being untouched by it (its own tests, unchanged, say so).

// atBump lands on the fixture's main the commit the ordinary path's step 2 would
// make, and returns its sha: the plugin version and every skill header, the cli
// stream's current, the two doc lines and the compatibility stamp. The rest of
// what --at requires the commit to carry is already in the seed for the version
// these tests release (docs/releases/v0.12.0.md, its summary line, FEATURES.md's
// stamp), so this is the whole bump for it. leaveOut names the pieces to leave
// out, one per test that holds --at to a missing one.
func atBump(t *testing.T, r *releaseRepo, version string, leaveOut ...string) string {
	t.Helper()
	skip := func(name string) bool {
		for _, l := range leaveOut {
			if l == name {
				return true
			}
		}
		return false
	}
	if !skip("plugin") {
		bump := exec.Command("bash", filepath.Join(r.local, "scripts", "bump-plugin-version.sh"), version)
		bump.Dir = r.local
		if out, err := bump.CombinedOutput(); err != nil {
			t.Fatalf("bumping the fixture plugin to %s: %v\n%s", version, err, out)
		}
	}
	if !skip("stream") {
		set := exec.Command(streamsHelper(t), "set", "cli", version)
		set.Dir = r.local
		if out, err := set.CombinedOutput(); err != nil {
			t.Fatalf("recording %s as the cli stream's current: %v\n%s", version, err, out)
		}
	}
	if !skip("docs") {
		writeReleasedVersionDocs(t, r.local, version)
	}
	if !skip("compat") {
		writeCompatibility(t, r.local, version)
	}
	if skip("notes") {
		if err := os.Remove(filepath.Join(r.local, "docs", "releases", "v"+version+".md")); err != nil {
			t.Fatal(err)
		}
	}
	if skip("summary") {
		writeSummaries(t, r.local, map[string]string{version: ""})
	}
	testutil.Git(t, r.local, "add", "-A")
	testutil.Git(t, r.local, "commit", "--quiet", "-m", "release: v"+version)
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
	return testutil.Git(t, r.local, "rev-parse", "HEAD")
}

// advanceMainPastCommit pushes a commit of its own onto main, so origin/main's
// tip is no longer the commit a test is releasing: the shape --at is for (the tip
// is red, or its checks are still running, while an earlier commit of main
// carries the whole bump and its own green checks).
func advanceMainPast(t *testing.T, r *releaseRepo) string {
	t.Helper()
	testutil.Git(t, r.local, "commit", "--quiet", "--allow-empty", "-m", "after the bump")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
	return testutil.Git(t, r.bare, "rev-parse", "main")
}

// rehearsalRecordFor reads the pre-tag rehearsal's record and holds it to the
// commit it was written for, which is the only way a test sees which tree the
// rehearsal built the CLI from (the record is what the tag waits on).
func rehearsalRecordFor(t *testing.T, r *releaseRepo, sha string, res runResult) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.recordDir, "record"))
	if err != nil {
		t.Fatalf("the rehearsal wrote no record: %v\n%s", err, res.out)
	}
	record := string(b)
	if !strings.Contains(record, "commit="+sha+"\n") {
		t.Errorf("the rehearsal's record is not for %s:\n%s", sha, record)
	}
	if !strings.Contains(record, "verdict=pass\n") {
		t.Errorf("the rehearsal's record is not green:\n%s", record)
	}
	return record
}

// TestAtTagsTheCommitItNames: --at cuts the release at the commit it names rather
// than at main's tip. The tag goes on that commit and no other, no branch, commit
// or pull request of the run's own is made, nothing is dispatched (a
// workflow_dispatch runs a workflow at a ref's tip, and the commit being tagged
// is not one), and the checkout, which is main's tip, is never moved. The gate is
// read on the named commit: its sha has the full run and main's tip has none at
// all, so a gate that read the tip could not have passed. And the rehearsal,
// which refuses a checkout that is not at the commit it records, built the named
// commit's tree.
func TestAtTagsTheCommitItNames(t *testing.T) {
	r := newReleaseRepo(t)
	sha := atBump(t, r, "0.12.0")
	tip := advanceMainPast(t, r)

	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, sha+"\tci (full)\t"+sha)

	res := runRelease(t, r, g, "", "0.12.0", "--at", sha)
	res.requireSuccess(t)

	if got := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}"); got != sha {
		t.Errorf("tag v0.12.0 is on %s, want the commit --at named (%s)", got, sha)
	}
	if got := testutil.Git(t, r.bare, "rev-parse", "main"); got != tip {
		t.Errorf("main is %s, want the tip the run left alone (%s)", got, tip)
	}
	if !strings.Contains(res.out, "--at: the release is cut at "+sha) {
		t.Errorf("the release does not say which commit it is cut at:\n%s", res.out)
	}
	if n := countLog(t, g, "pr "); n != 0 {
		t.Errorf("--at makes no pull request of its own:\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "branch", "--list", "release/v0.12.0"); out != "" {
		t.Errorf("--at created a release branch: %s", out)
	}
	if dispatched := workflowRunLines(t, g); len(dispatched) != 0 {
		t.Errorf("--at dispatches no ci run, and the commit carries one already: %q", dispatched)
	}
	if got := currentBranch(t, r); got != "main" {
		t.Errorf("--at left the checkout on %s; it is cut from main and never moves it", got)
	}
	if out := testutil.Git(t, r.local, "status", "--porcelain"); out != "" {
		t.Errorf("--at left the checkout dirty:\n%s", out)
	}
	rehearsalRecordFor(t, r, sha, res)
}

// gateRanName is the file a replaced release-gate.sh writes when it runs. It
// sits beside the fixture's checkout rather than in it: a release refuses a
// dirty tree, and an untracked file inside `work/` would be that refusal instead
// of the one under test.
const gateRanName = "gate-ran"

// TestAtStillNeedsACleanMain: --at changes which commit the tag goes on and
// nothing else -- the release is still cut from a checkout on main equal to
// origin/main, because release.sh, scripts/release-gate.sh,
// scripts/release-policy.sh and scripts/verify-release.sh all run from it: an
// agent's task branch carrying a stale or altered gate would otherwise decide
// whether the named commit is green, and no other tree's files are released on
// the --at path. Both halves of that precondition refuse before the worktree,
// the gate or the tag, and the commit named is a perfectly good one.
func TestAtStillNeedsACleanMain(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(t *testing.T, r *releaseRepo)
		want   string
		// gateRan is where a case that replaces the checkout's own
		// release-gate.sh would have that gate leave its mark, so the run can
		// be caught running it (empty for the cases that leave the scripts be).
		gateRan string
	}{
		{
			name: "the checkout's gate is not origin/main's",
			mutate: func(t *testing.T, r *releaseRepo) {
				loosened := fmt.Sprintf("#!/bin/sh\n: > '%s'\nexit 0\n", filepath.Join(filepath.Dir(r.local), gateRanName))
				if err := os.WriteFile(filepath.Join(r.local, "scripts", "release-gate.sh"), []byte(loosened), 0o755); err != nil {
					t.Fatal(err)
				}
				testutil.Git(t, r.local, "add", "scripts/release-gate.sh")
				testutil.Git(t, r.local, "commit", "--quiet", "-m", "a gate that approves anything")
			},
			want:    "is not origin/main",
			gateRan: gateRanName,
		},
		{
			name: "the checkout is on another branch",
			mutate: func(t *testing.T, r *releaseRepo) {
				testutil.Git(t, r.local, "switch", "--quiet", "-c", "elsewhere")
			},
			want: "not main",
		},
		{
			name: "main is ahead of origin/main",
			mutate: func(t *testing.T, r *releaseRepo) {
				testutil.Git(t, r.local, "commit", "--quiet", "--allow-empty", "-m", "local only")
			},
			want: "is not origin/main",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			sha := atBump(t, r, "0.12.0")
			c.mutate(t, r)
			g := newGHState(t, r, greenChecks())
			ciRuns(t, g, sha+"\tci (full)\t"+sha)

			res := runRelease(t, r, g, "", "0.12.0", "--at", sha)

			res.requireFailureContaining(t, c.want)
			requireNoTag(t, r, res)
			if n := countLog(t, g, "pr "); n != 0 {
				t.Errorf("a refused --at must not touch a pull request:\n%s", g.log(t))
			}
			if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
				t.Errorf("a refused --at was rehearsed (stat: %v)\n%s", err, res.out)
			}
			if c.gateRan != "" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(r.local), c.gateRan)); !os.IsNotExist(err) {
					t.Errorf("a refused --at ran the checkout's own release-gate.sh: the scripts a release is decided with are origin/main's, not the tree's (stat: %v)\n%s", err, res.out)
				}
			}
		})
	}
}

// TestAtNeedsNoLocalMainRef: --at is cut from a checkout whose tree is
// origin/main's, and the `main` ref is not part of that question. The branch is
// still main (the check above, pinned by TestAtStillNeedsACleanMain even when
// the tree's own scripts are not origin's), and what is compared with
// origin/main is HEAD's commit, the one that check has tied to main. A run that
// asks the bare ref instead ends in git's own `fatal:` line the moment it does
// not resolve -- no refusal, no tag -- which is the abort the wrapper below makes
// real. The wrapper is what says the run asked that ref at all, and the control
// just after it is what says the wrapper bites.
func TestAtNeedsNoLocalMainRef(t *testing.T) {
	r := newReleaseRepo(t)
	sha := atBump(t, r, "0.12.0")
	tip := advanceMainPast(t, r)
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, sha+"\tci (full)\t"+sha)

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeBinDirAt(t, bin)
	asked := filepath.Join(bin, "asked-for-main")
	// Every call but one is the real git's, `git rev-parse refs/remotes/origin/main`
	// included: only the local ref by its bare name is refused, which is the call
	// release.sh used to make.
	wrapper := fmt.Sprintf(`#!/bin/sh
if [ "$1" = rev-parse ]; then
  for a in "$@"; do
    if [ "$a" = main ]; then
      echo "$a" >> '%s'
      echo "fatal: not a valid object name: '$a'" >&2
      exit 128
    fi
  done
fi
exec '%s' "$@"
`, asked, realGit)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	// The control: asked directly, the wrapper refuses, so a run that gets past
	// it has really not asked.
	if out, err := exec.Command(filepath.Join(bin, "git"), "rev-parse", "main").CombinedOutput(); err == nil {
		t.Fatalf("the git wrapper answered `rev-parse main`, so this test would pass whatever release.sh did: %s", out)
	}
	if err := os.Remove(asked); err != nil {
		t.Fatal(err)
	}

	res := runReleaseFrom(t, r, g, bin, "", "0.12.0", "--at", sha)
	res.requireSuccess(t)

	if _, err := os.Stat(asked); !os.IsNotExist(err) {
		t.Errorf("--at resolved a local `main` ref: the commit a cut is held to origin/main by is HEAD's, which the branch check has already tied to main, and the bare ref is a second question whose only other answer is git's own fatal: line ending the script (stat: %v)\n%s", err, res.out)
	}
	if got := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}"); got != sha {
		t.Errorf("tag v0.12.0 is on %s, want the commit --at named (%s)", got, sha)
	}
	if got := testutil.Git(t, r.bare, "rev-parse", "main"); got != tip {
		t.Errorf("main is %s, want the tip the run left alone (%s)", got, tip)
	}
}

// TestAtReadsTheChecksOnTheCommitItNames: the gate is the named commit's own
// checks, read by sha, never main's tip's. A commit whose only `ci` run is a
// selected one is refused at once with the command that puts a full run on it --
// and that command names a ref of the person's own, because the commit is not
// main's tip and the gate invents none. main's tip carries a full, green run
// throughout, so a gate that read the tip would have released this.
func TestAtReadsTheChecksOnTheCommitItNames(t *testing.T) {
	r := newReleaseRepo(t)
	sha := atBump(t, r, "0.12.0")
	advanceMainPast(t, r)
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, mainFullyGreen(t, r), sha+"\tci (selected)\t"+sha+"-selected")

	res := runRelease(t, r, g, "", "0.12.0", "--at", sha)

	res.requireFailureContaining(t, "no ci run on that commit is a full one")
	res.requireFailureContaining(t, "gh workflow run ci --ref <a ref pointing at "+sha+"> -f full=true")
	requireNoTag(t, r, res)
	if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
		t.Errorf("a commit the gate refused was rehearsed (stat: %v)\n%s", err, res.out)
	}
}

// TestAtRefusesACommitWithoutTheBump: --at makes no commit and opens no pull
// request, so the commit it names has to carry every piece the ordinary path's
// release commit would have written -- the notes, their summary line, the plugin
// version and skill headers, the cli stream's current, the two doc lines, the
// compatibility stamp and FEATURES.md's. Each missing piece is refused before the
// gate, the rehearsal or the tag. The pieces --at's own checks read carry the
// sentence that says where the bump gets landed instead (through a PR on main),
// because this run writes nothing itself; the notes and their summary line are
// the checks the ordinary path shares, and they name the re-run command.
func TestAtRefusesACommitWithoutTheBump(t *testing.T) {
	// hint says whether the refusal carries --at's own sentence about where the
	// bump gets landed instead; the notes and the summary line are the two
	// checks the ordinary path shares, and they point at the re-run command.
	for _, c := range []struct {
		name, leave, want string
		hint              bool
	}{
		{"no plugin version", "plugin", "does not carry every 0.12.0 plugin version stamp", true},
		{"no stream current", "stream", "says the cli stream's current is '0.0.0', not 0.12.0", true},
		{"no doc lines", "docs", "the doc lines that name the released version at ", true},
		{"no compatibility stamp", "compat", "says the compatibility matrix was last read as of 'v0.11.4', not v0.12.0", true},
		{"no release notes", "notes", "docs/releases/v0.12.0.md does not exist at ", false},
		{"no summary line", "summary", "website/data/release-summaries.yaml at ", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			sha := atBump(t, r, "0.12.0", c.leave)
			g := newGHState(t, r, greenChecks())

			res := runRelease(t, r, g, "", "0.12.0", "--at", sha)

			res.requireFailureContaining(t, c.want)
			if c.hint {
				// --at's own sentence: the bump lands on main through a PR, and
				// the sha to re-run with is that commit's, which only the person
				// landing it knows.
				res.requireFailureContaining(t, "already carries v0.12.0's bump")
			} else {
				// The two shared checks re-run this release, and --at is given
				// the commit to land the piece at rather than the one it was.
				res.requireFailureContaining(t, "scripts/release.sh 0.12.0 --at <the commit that carries it>")
			}
			requireNoTag(t, r, res)
			if n := countLog(t, g, "pr "); n != 0 {
				t.Errorf("a refused --at must not touch a pull request:\n%s", g.log(t))
			}
			if out := testutil.Git(t, r.bare, "branch", "--list", "release/v0.12.0"); out != "" {
				t.Errorf("a refused --at created a branch: %s", out)
			}
			if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
				t.Errorf("a refused --at was rehearsed (stat: %v)\n%s", err, res.out)
			}
		})
	}

	t.Run("a commit that is not in this checkout", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())

		res := runRelease(t, r, g, "", "0.12.0", "--at", strings.Repeat("0", 39)+"1")

		res.requireFailureContaining(t, "is not a commit in this checkout")
		requireNoTag(t, r, res)
	})
}

// TestAtRefusesACommitThatIsNotOnMain: a CLI release is tagged on a commit of
// main. A commit that is not one -- a shape only --hotfix cuts, from the
// maintenance branch -- is refused before the worktree, the gate, the tag or
// anything else, and the refusal names the flag that path belongs to.
func TestAtRefusesACommitThatIsNotOnMain(t *testing.T) {
	r := newReleaseRepo(t)
	atBump(t, r, "0.12.0")
	// A commit of the fixture's own that main does not have: the bump is whole
	// and the commit is still no ancestor of origin/main. It is made on a branch
	// of its own and the checkout goes back to main, which the release demands of
	// every path.
	testutil.Git(t, r.local, "switch", "--quiet", "-c", "side")
	testutil.Git(t, r.local, "commit", "--quiet", "--allow-empty", "-m", "not on main")
	off := testutil.Git(t, r.local, "rev-parse", "HEAD")
	testutil.Git(t, r.local, "switch", "--quiet", "main")
	g := newGHState(t, r, greenChecks())

	res := runRelease(t, r, g, "", "0.12.0", "--at", off)

	res.requireFailureContaining(t, "which is not an ancestor of origin/main")
	res.requireFailureContaining(t, "--hotfix")
	requireNoTag(t, r, res)
	if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
		t.Errorf("a refused --at was rehearsed (stat: %v)\n%s", err, res.out)
	}
}

// TestAtDryRunPlansNoBranchAndNoDispatch: --at combines with --dry-run, and the
// plan says what this path does not do -- no branch, no commit, no pull request,
// no dispatch -- and that the plugin budget check did not run, because the commit
// already carries the bump and its own `test` check ran the plugin tests on that
// tree (the gate reads that check). Nothing is created, and no rehearsal is run.
func TestAtDryRunPlansNoBranchAndNoDispatch(t *testing.T) {
	r := newReleaseRepo(t)
	sha := atBump(t, r, "0.12.0")
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, sha+"\tci (full)\t"+sha)

	res := runRelease(t, r, g, "", "0.12.0", "--at", sha, "--dry-run")

	res.requireSuccess(t)
	for _, want := range []string{
		"Dry run for v0.12.0 (Dolphin) at " + sha,
		"2. be skipped: --at tags a commit that already carries the bump",
		"3. be skipped: with --at there is no release pull request to wait for",
		"4. dispatch no ci run",
		"5. create and push the tag v0.12.0 on " + sha,
		"with the CLI built from " + sha + ", the commit --at names",
	} {
		if !strings.Contains(res.out, want) {
			t.Errorf("the plan does not say %q:\n%s", want, res.out)
		}
	}
	if strings.Contains(res.out, "the plugin tests pass on a tree with the") {
		t.Errorf("the plan claims the plugin budget check, which --at does not run:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out != "" {
		t.Errorf("the dry run created a tag: %s", out)
	}
	if out := testutil.Git(t, r.bare, "branch", "--list", "release/v0.12.0"); out != "" {
		t.Errorf("the dry run created a branch: %s", out)
	}
	if n := countLog(t, g, "pr "); n != 0 {
		t.Errorf("the dry run touched a pull request:\n%s", g.log(t))
	}
	if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
		t.Errorf("the dry run rehearsed (stat: %v)\n%s", err, res.out)
	}
}

// TestAtWithSkipRehearsal: the flag combines with --skip-rehearsal too, and the
// owner's own words are what the summary carries -- no rehearsal was run for the
// commit being tagged, and it is owed. The release still tags the named commit:
// the skip is the owner's, never a default.
func TestAtWithSkipRehearsal(t *testing.T) {
	r := newReleaseRepo(t)
	sha := atBump(t, r, "0.12.0")
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, sha+"\tci (full)\t"+sha)

	res := runRelease(t, r, g, "", "0.12.0", "--at", sha, "--skip-rehearsal")

	res.requireSuccess(t)
	if !strings.Contains(res.out, "--skip-rehearsal: no rehearsal was run for "+sha) {
		t.Errorf("the release does not say the rehearsal was skipped for the commit it tags:\n%s", res.out)
	}
	if got := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}"); got != sha {
		t.Errorf("tag v0.12.0 is on %s, want the commit --at named (%s)", got, sha)
	}
	if _, err := os.Stat(filepath.Join(r.recordDir, "record")); !os.IsNotExist(err) {
		t.Errorf("--skip-rehearsal wrote a rehearsal record (stat: %v)\n%s", err, res.out)
	}
}

// TestReleaseRefusesVersionBelowStreamCurrent: docs/release-streams.yaml
// records the version each stream is at, and a CLI release never goes behind it
// (GV1.5). The fixture's CLI stream is at 0.11.1, so 0.11.0 and below are
// refused in the preconditions, naming the file, before a branch, a pull
// request or a tag exists -- the file is the stream's own record, and a version
// under it is a release the stream has already spent. The version the stream is
// already at is not below it: an interrupted release whose commit recorded the
// version is finished by re-running it, and that run has to get past this check
// (the tag check is what refuses a version that really did ship).
func TestReleaseRefusesVersionBelowStreamCurrent(t *testing.T) {
	for _, version := range []string{"0.11.0", "0.10.9", "0.0.1"} {
		t.Run(version, func(t *testing.T) {
			r := newReleaseRepo(t)
			rewriteFixtureStreams(t, r, streamsFixtureReleased)
			// The notes precondition comes before the streams check; the
			// version's own notes are on main so the refusal is the floor's.
			addReleaseNotes(t, r, version)
			g := newGHState(t, r, greenChecks())

			res := runRelease(t, r, g, "", version, "--dry-run")

			res.requireFailureContaining(t, "is below the cli stream's current 0.11.1")
			res.requireFailureContaining(t, "docs/release-streams.yaml")
			if strings.Contains(res.out, "Dry run for") {
				t.Errorf("a refused version must not print the plan:\n%s", res.out)
			}
			if countLog(t, g, "pr ") != 0 {
				t.Errorf("a refused version must not touch a pull request:\n%s", res.out)
			}
			if out := testutil.Git(t, r.bare, "branch", "--list", "release/v"+version); out != "" {
				t.Errorf("a refused version created a branch: %s", out)
			}
		})
	}

	t.Run("the version the stream is already at", func(t *testing.T) {
		r := newReleaseRepo(t)
		rewriteFixtureStreams(t, r, streamsFixtureReleased)
		addReleaseNotes(t, r, "0.11.1")
		g := newGHState(t, r, greenChecks())

		res := runRelease(t, r, g, "", "0.11.1", "--dry-run")

		res.requireSuccess(t)
	})
}

// TestReleaseRefusesWhatStepTwoWouldRefuse: step 1 runs step 2's catalogue
// write in memory (`streams set --check`), so a version the write would refuse
// is refused before a branch, a pull request or a tag exists -- in the dry run
// too, which is how the dry run predicts step 2. A major bump and a minor below
// 0.10 are off the CLI's named line (the reader refuses a file that records
// them), a version past the last name leaves no name for `next`, and an unquoted
// `current:` is one the line-targeted rewrite cannot find.
func TestReleaseRefusesWhatStepTwoWouldRefuse(t *testing.T) {
	unquoted := strings.Replace(streamsFixtureReleased, `current: "0.11.1"`, "current: 0.11.1", 1)
	for _, c := range []struct {
		name, catalogue, version, want string
	}{
		{"a major bump", streamsFixture, "1.0.0", "is not on the line"},
		{"a minor below the named line", streamsFixture, "0.9.0", "is not on the line"},
		{"no name left after it", streamsFixtureReleased, "0.16.0", "no name for the minor after it"},
		{"an unquoted current", unquoted, "0.11.2", "no current version"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			if c.catalogue != streamsFixture {
				rewriteFixtureStreams(t, r, c.catalogue)
			}
			addReleaseNotes(t, r, c.version)
			g := newGHState(t, r, greenChecks())

			res := runRelease(t, r, g, "", c.version, "--dry-run")

			res.requireFailureContaining(t, "docs/release-streams.yaml cannot record "+c.version)
			res.requireFailureContaining(t, c.want)
			if strings.Contains(res.out, "Dry run for") {
				t.Errorf("a refused version must not print the plan:\n%s", res.out)
			}
			if countLog(t, g, "pr ") != 0 {
				t.Errorf("a refused version must not touch a pull request:\n%s", res.out)
			}
			if out := testutil.Git(t, r.bare, "branch", "--list", "release/v"+c.version); out != "" {
				t.Errorf("a refused version created a branch: %s", out)
			}
		})
	}
}

// TestReleaseReadsTheStreamWithGoRunByDefault: every other test hands release.sh
// the compiled helper; without RELEASE_SH_STREAMS_CMD the script runs
// `go run ./internal/streams/cmd/streams` itself. A stand-in `go` records its
// arguments and runs the helper, so the default command line is what is pinned.
func TestReleaseReadsTheStreamWithGoRunByDefault(t *testing.T) {
	r := newReleaseRepo(t)
	goDir := t.TempDir()
	log := filepath.Join(goDir, "go.log")
	stub := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"if [ \"$1\" = run ] && [ \"$2\" = ./internal/streams/cmd/streams ]; then shift 2; exec " + streamsHelper(t) + " \"$@\"; fi\n" +
		"exit 97\n"
	if err := os.WriteFile(filepath.Join(goDir, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	g := newGHState(t, r, greenChecks()).with(
		"RELEASE_SH_STREAMS_CMD=", "PATH="+goDir+":"+fakeBinDir(t)+":"+os.Getenv("PATH"))

	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

	res.requireSuccess(t)
	if !strings.Contains(res.out, "Dry run for v0.12.0 (Dolphin)") {
		t.Errorf("want the plan to name the release, got:\n%s", res.out)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("go was never run: %v\n%s", err, res.out)
	}
	for _, want := range []string{
		"run ./internal/streams/cmd/streams set --check cli 0.12.0",
		"run ./internal/streams/cmd/streams name cli 0.12.0",
	} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("go was not run as %q; it ran:\n%s", want, calls)
		}
	}
}

// TestReleaseRefusesWhenGoIsMissingForTheStream: with the default `go run` and
// no go on PATH the release refuses, naming the stream read, even in a checkout
// with no go.mod (where the plugin budget check would only be skipped).
func TestReleaseRefusesWhenGoIsMissingForTheStream(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with(
		"RELEASE_SH_STREAMS_CMD=", "PATH="+pathWithoutGo(t, fakeBinDir(t)))

	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

	res.requireFailureContaining(t, "go is not on PATH, and the release reads the CLI stream")
	if strings.Contains(res.out, "Dry run for") {
		t.Errorf("a refused dry run must not print the plan:\n%s", res.out)
	}
}

// TestReleasePrintsStreamName: the release is spoken with the name the
// catalogue gives its minor and the patch's word -- "Camel two" for 0.11.2 on a
// stream at 0.11.1 -- so the operator reads which release they are cutting and
// not only its number.
func TestReleasePrintsStreamName(t *testing.T) {
	r := newReleaseRepo(t)
	rewriteFixtureStreams(t, r, streamsFixtureReleased)
	addReleaseNotes(t, r, "0.11.2")
	g := newGHState(t, r, greenChecks())

	res := runRelease(t, r, g, "", "0.11.2", "--dry-run")

	res.requireSuccess(t)
	if !strings.Contains(res.out, "Dry run for v0.11.2 (Camel two)") {
		t.Errorf("want the release spoken by its name, got:\n%s", res.out)
	}
}

// TestReleaseBumpsStreamCurrent: step 2 records the version being released as
// the CLI stream's current in docs/release-streams.yaml, in the release commit
// itself, so the file the graph draws and the next release reads never
// describes a version that has shipped.
func TestReleaseBumpsStreamCurrent(t *testing.T) {
	r := newReleaseRepo(t)
	rewriteFixtureStreams(t, r, streamsFixtureReleased)
	addReleaseNotes(t, r, "0.11.2")
	g := newGHState(t, r, greenChecks())
	initial := testutil.Git(t, r.bare, "rev-parse", "main")

	res := runRelease(t, r, g, "", "0.11.2")

	res.requireSuccess(t)
	if n := testutil.Git(t, r.bare, "rev-list", "--count", initial+"..refs/heads/release/v0.11.2"); n != "1" {
		t.Errorf("want the catalogue update in the release commit itself, got %s commits on the release branch", n)
	}
	catalogue := testutil.Git(t, r.bare, "show", "main:docs/release-streams.yaml")
	if !strings.Contains(catalogue, `current: "0.11.2"`) {
		t.Errorf("docs/release-streams.yaml on main does not record 0.11.2 as the cli stream's current:\n%s", catalogue)
	}
	if !strings.Contains(catalogue, `current: "0.111.0"`) || !strings.Contains(catalogue, `current: "0.0.0"`) {
		t.Errorf("the console's and the mobile's rows moved, and a CLI release writes the cli row only:\n%s", catalogue)
	}
	if !strings.Contains(res.out, "name:    Camel two") {
		t.Errorf("want the release summary to speak the release, got:\n%s", res.out)
	}
}

// TestReleaseMovesStreamNextOnANewMinor: the release that opens a new minor
// spends the name the catalogue's `next` holds, so the same commit moves `next`
// on to the name after it. `next` is the name the minor after the current one
// takes, so a file left at current 0.12.0 with next Dolphin would contradict its
// own header -- Dolphin is 0.12.0's own name -- and every later release would be
// spoken one minor behind: 0.12.1 "Camel one", 0.13.0 "Dolphin".
func TestReleaseMovesStreamNextOnANewMinor(t *testing.T) {
	r := newReleaseRepo(t)
	rewriteFixtureStreams(t, r, streamsFixtureReleased)
	addReleaseNotes(t, r, "0.12.0")
	g := newGHState(t, r, greenChecks())

	res := runRelease(t, r, g, "", "0.12.0")

	res.requireSuccess(t)
	catalogue := testutil.Git(t, r.bare, "show", "main:docs/release-streams.yaml")
	if !strings.Contains(catalogue, `current: "0.12.0"`) || !strings.Contains(catalogue, "next: Gecko") {
		t.Errorf("0.12.0 opens a new minor, so the release commit should leave the cli row at current 0.12.0 with Gecko next:\n%s", catalogue)
	}
	if !strings.Contains(res.out, "name:    Dolphin") {
		t.Errorf("0.12.0 is the minor Dolphin, so the release should be spoken with it, got:\n%s", res.out)
	}
}

// TestReleaseRefusesACatalogueItCannotRead: the file the release takes its own
// floor and its name from is repository-written, and the reader refuses one
// that is not the catalogue -- here a cli row whose tag prefix is not `v`, a
// release no workflow of the stream's would pick up. Nothing is created: the
// refusal is in the preconditions, before the branch.
func TestReleaseRefusesACatalogueItCannotRead(t *testing.T) {
	r := newReleaseRepo(t)
	rewriteFixtureStreams(t, r, strings.Replace(streamsFixture, "tag_prefix: v\n", "tag_prefix: cli-v\n", 1))
	g := newGHState(t, r, greenChecks())

	res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

	res.requireFailureContaining(t, "docs/release-streams.yaml")
	res.requireFailureContaining(t, "tag_prefix")
	if countLog(t, g, "pr ") != 0 {
		t.Errorf("an unreadable catalogue must not touch a pull request:\n%s", res.out)
	}
}

// TestTagsWithoutPromptOrStdin checks the tag is created with no prompt and
// no stdin at all, and that --yes is still accepted as a no-op.
func TestTagsWithoutPromptOrStdin(t *testing.T) {
	for _, extra := range [][]string{nil, {}} {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		res := runRelease(t, r, g, "", append([]string{"0.12.0"}, extra...)...)
		res.requireSuccess(t)
		if strings.Contains(res.out, "[y/N]") {
			t.Errorf("no prompt expected:\n%s", res.out)
		}
		if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
			t.Errorf("tag missing (args %v); output:\n%s", extra, res.out)
		}
	}
}

// TestResumeAfterMerge simulates a prior run that got as far as the PR
// merging on origin but was interrupted before the tag, and checks a
// fresh run finds that state and continues straight to tagging, creating
// no second pull request.
func TestResumeAfterMerge(t *testing.T) {
	r := newReleaseRepo(t)

	// Stand in for "the PR already merged": bump and commit directly on a
	// clone, then fast-forward origin/main, as the earlier run's squash
	// merge would have.
	clone := filepath.Join(t.TempDir(), "premerge")
	testutil.Git(t, filepath.Dir(clone), "clone", "--quiet", r.bare, clone)
	bumpCmd := exec.Command("bash", filepath.Join(clone, "scripts", "bump-plugin-version.sh"), "0.12.0")
	bumpCmd.Dir = clone
	if out, err := bumpCmd.CombinedOutput(); err != nil {
		t.Fatalf("bump: %v\n%s", err, out)
	}
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", "release: v0.12.0", "-m", "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>")
	testutil.Git(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	mergeSHA := testutil.Git(t, clone, "rev-parse", "HEAD")

	// A fresh checkout, as if the user re-ran release.sh after pulling.
	local2 := filepath.Join(t.TempDir(), "work2")
	testutil.Git(t, filepath.Dir(local2), "clone", "--quiet", r.bare, local2)
	r2 := &releaseRepo{bare: r.bare, local: local2, recordDir: t.TempDir(), fugaroState: t.TempDir()}

	g := newGHState(t, r2, greenChecks())
	g.write(t, "pr_number", "1")
	g.write(t, "pr_state", "MERGED")
	g.write(t, "pr_merge_sha", mergeSHA)

	res := runRelease(t, r2, g, "y\n", "0.12.0")
	res.requireSuccess(t)

	if n := countLog(t, g, "pr create"); n != 0 {
		t.Errorf("resuming an already-merged PR must not create a new one, got %d:\n%s", n, g.log(t))
	}
	tagSHA := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}")
	if tagSHA != mergeSHA {
		t.Errorf("tag v0.12.0 (%s) does not point at the pre-merged commit (%s)", tagSHA, mergeSHA)
	}
}

// TestResumeRebuildsStaleLocalBranch simulates an interrupted run that left
// a local, never-pushed release/vX.Y.Z branch behind, with main having
// since advanced (the user pulled before re-running). A fresh run must
// rebuild that branch on top of the new main rather than basing the PR on
// the stale history it already had checked out.
func TestResumeRebuildsStaleLocalBranch(t *testing.T) {
	r := newReleaseRepo(t)

	testutil.Git(t, r.local, "switch", "--quiet", "-c", "release/v0.12.0")
	testutil.Git(t, r.local, "switch", "--quiet", "main")

	// Main advances independently of the stale local branch above.
	advancer := filepath.Join(t.TempDir(), "advancer")
	testutil.Git(t, filepath.Dir(advancer), "clone", "--quiet", r.bare, advancer)
	testutil.Git(t, advancer, "commit", "--quiet", "--allow-empty", "-m", "unrelated main commit")
	testutil.Git(t, advancer, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	newMain := testutil.Git(t, advancer, "rev-parse", "HEAD")

	// As if the user had pulled main before re-running: local main now
	// matches origin/main, but the stale local release branch does not.
	testutil.Git(t, r.local, "fetch", "--quiet", "origin", "main")
	testutil.Git(t, r.local, "merge", "--quiet", "--ff-only", "origin/main")

	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireSuccess(t)

	if !strings.Contains(res.out, "predates main") {
		t.Errorf("expected a message about rebuilding the stale branch, got:\n%s", res.out)
	}
	branchCommits := testutil.Git(t, r.bare, "rev-list", "--count", newMain+"..refs/heads/release/v0.12.0")
	if branchCommits != "1" {
		t.Errorf("want exactly one commit on the rebuilt release branch on top of the new main, got %s", branchCommits)
	}
	if base := testutil.Git(t, r.bare, "merge-base", "v0.12.0", newMain); base != newMain {
		t.Errorf("tag v0.12.0 is not based on the new main (%s); merge-base was %s", newMain, base)
	}
}

// TestPRClosedUnmerged checks a closed-without-merging PR stops the script
// with a message naming it, rather than hanging or tagging anything.
func TestPRClosedUnmerged(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_number", "7")
	g.write(t, "pr_state", "CLOSED")
	pushReleaseBranch(t, r, "0.12.0")
	g.write(t, "pr_branch", "release/v0.12.0")

	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "#7")
	res.requireFailureContaining(t, "closed without merging")
	res.requireFailureContaining(t, "git push origin --delete release/v0.12.0")
	if countLog(t, g, "pr merge") != 0 {
		t.Errorf("a closed PR must never reach 'gh pr merge':\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out != "" {
		t.Errorf("a closed PR must not lead to a tag: %s", out)
	}
}

// TestPRFailedCheckStopsWaiting checks a failing status check on the
// release PR is reported by name rather than waited out until timeout.
func TestPRFailedCheckStopsWaiting(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_check_fail", "rules FAILURE")

	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "check 'rules' failed")
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out != "" {
		t.Errorf("a failed check must not lead to a tag: %s", out)
	}
}

// TestInterruptPrintsResumeInstructions checks an interrupt delivered to the
// process group is handled with a message telling the caller how to continue,
// rather than leaving the terminal on a bare stack trace.
//
// The test sends SIGTERM, not SIGINT: when the test process itself starts with
// SIGINT ignored (background job, nohup-style CI launchers), the bash child
// inherits SIG_IGN, and a signal ignored on entry can never be trapped, so the
// script would never exit. SIGTERM is not ignored by inheritance. release.sh
// handles INT and TERM with the same trap, so this covers the handler a
// terminal's Ctrl-C (SIGINT) reaches.
func TestInterruptPrintsResumeInstructions(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	// Never let the fake merge the PR, so the run is still in the poll
	// loop's sleep (not exited already) when the signal arrives.
	g.env = append(g.env, "FAKE_GH_MERGE_DELAY_CALLS=1000000")
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "0.12.0")
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Env = append(cmd.Env, releaseEnv(t)...)
	cmd.Env = append(cmd.Env, rehearsalEnv(r, bin)...)
	var out lockedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 5 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Signal only once the poll loop is demonstrably running: it has polled
	// "pr view" at least twice after the merge call (the state lookup just
	// before the loop is one more view) and announced "waiting for".
	// Signalling right after the merge call can land just after the loop's
	// subshell forked but before it reset its inherited traps; bash then
	// swallows the signal in the child while the parent defers its trap until
	// the child exits, and the run hangs.
	deadline := time.Now().Add(60 * time.Second)
	for !(viewsAfterMerge(t, g) >= 3 && strings.Contains(out.String(), "waiting for")) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			t.Fatalf("release.sh never reached the poll loop; output:\n%s", out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	pgid := cmd.Process.Pid
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	// Like a terminal's Ctrl-C, deliver to the whole process group: the poll
	// loop runs in a subshell, which a signal sent to the parent alone would
	// never reach (bash defers its trap until that child exits). Repeat it,
	// as a user would, if the script has not exited after 5s.
	exited := false
	for sends := 0; sends < 3 && !exited; sends++ {
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Fatal(err)
		}
		select {
		case <-done:
			exited = true
		case <-time.After(5 * time.Second):
		}
	}
	if !exited {
		select {
		case <-done:
			exited = true
		case <-time.After(15 * time.Second):
		}
	}
	if !exited {
		procs := releaseProcs(pgid)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
		t.Fatalf("release.sh did not exit within 30s of SIGTERM; output:\n%s\nprocesses:\n%s", out.String(), procs)
	}
	if !strings.Contains(out.String(), "resume") {
		t.Fatalf("expected resume instructions after SIGTERM, got:\n%s", out.String())
	}
}

// TestVersionComparisonIsNumeric checks 0.12.10 is accepted as greater than
// an existing v0.12.9 tag: a lexicographic (not numeric) comparison would
// wrongly block this release, since "0.12.10" < "0.12.9" as strings.
func TestVersionComparisonIsNumeric(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	testutil.Git(t, r.local, "tag", "-a", "v0.12.9", "-m", "x")
	// The tag carries the seed's FEATURES.md, stamped 0.12.0; the release's own
	// PR stamps the file for the version it releases, after the previous tag
	// (D2449).
	stampFeatures(t, r, "0.12.10")
	res := runRelease(t, r, g, "", "0.12.10", "--dry-run")
	res.requireSuccess(t)
}

// pushReleaseBranch stands in for an earlier interrupted run: it pushes
// release/vVERSION to origin, already carrying the plugin bump commit but not
// the catalogue's cli row.
func pushReleaseBranch(t *testing.T, r *releaseRepo, version string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "earlier")
	testutil.Git(t, filepath.Dir(clone), "clone", "--quiet", r.bare, clone)
	testutil.Git(t, clone, "switch", "--quiet", "-c", "release/v"+version)
	bump := exec.Command("bash", filepath.Join(clone, "scripts", "bump-plugin-version.sh"), version)
	bump.Dir = clone
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bump: %v\n%s", err, out)
	}
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", "release: v"+version)
	testutil.Git(t, clone, "push", "--quiet", "origin", "release/v"+version)
}

// installHook installs a pre-receive hook (a shell script body) on origin.
func installHook(t *testing.T, r *releaseRepo, body string) {
	t.Helper()
	hook := filepath.Join(r.bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func removeHook(t *testing.T, r *releaseRepo) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.bare, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
}

func currentBranch(t *testing.T, r *releaseRepo) string {
	t.Helper()
	return testutil.Git(t, r.local, "rev-parse", "--abbrev-ref", "HEAD")
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func (g *ghState) with(env ...string) *ghState {
	return &ghState{dir: g.dir, env: append(append([]string{}, g.env...), env...)}
}

func requireNoTag(t *testing.T, r *releaseRepo, res runResult) {
	t.Helper()
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out != "" {
		t.Errorf("no tag may exist on origin, got %s\n%s", out, res.out)
	}
}

// TestWaitsForPostMergeChecks: right after the merge, CI has not reported
// on the merge commit yet; the gate must wait rather than fail.
func TestWaitsForPostMergeChecks(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_MISSING_CALLS=2")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "still waiting: check 'test' is missing") {
		t.Errorf("expected the pending check to be named:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("tag missing after the checks went green:\n%s", res.out)
	}
}

// TestPreconditionWaitsForPendingChecksOnMain: checks still running on main
// when the script starts are waited for (not failed), with a message.
func TestPreconditionWaitsForPendingChecksOnMain(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_PRE_PENDING_CALLS=2")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "still waiting: check 'test' is pending") {
		t.Errorf("expected the pending check to be named:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("tag missing after the checks went green:\n%s", res.out)
	}
}

// TestPreconditionPendingChecksOnMainTimeOut: never-finishing checks on main
// end the wait at the timeout with a clear error, before anything is created.
func TestPreconditionPendingChecksOnMainTimeOut(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_CHECK_RULES=pending", "RELEASE_SH_TIMEOUT_SECONDS=2")
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireFailureContaining(t, "required check 'rules' is pending")
	if n := countLog(t, g, "pr create"); n != 0 {
		t.Errorf("a pull request was created despite pending checks on main")
	}
	requireNoTag(t, r, res)
}

func TestPostMergeCheckFailsFast(t *testing.T) {
	r := newReleaseRepo(t)
	// A long timeout: a conclusive failure must not be waited out.
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_POST_TERRAFORM=failed")
	start := time.Now()
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireFailureContaining(t, "required check 'terraform' is failed")
	if time.Since(start) > 30*time.Second {
		t.Errorf("a red check was waited out")
	}
	requireNoTag(t, r, res)
}

func TestPostMergeCheckStillPendingTimesOut(t *testing.T) {
	for _, state := range []string{"pending", "missing", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks()).with("FAKE_GH_POST_RULES="+state, "RELEASE_SH_TIMEOUT_SECONDS=2")
			res := runRelease(t, r, g, "y\n", "0.12.0")
			res.requireFailureContaining(t, "required check 'rules' is "+state)
			requireNoTag(t, r, res)
		})
	}
}

// identicalTreeEnv makes the target (merge commit) checks not ready yet, as
// right after a squash merge, and lists the merged PR whose head carries the
// same tree, so the gate may use the head's checks.
func identicalTreeEnv(g *ghState, extra ...string) *ghState {
	env := append([]string{
		"FAKE_GH_POST_TEST=missing", "FAKE_GH_POST_TERRAFORM=pending", "FAKE_GH_POST_RULES=missing",
		"FAKE_GH_IDENTICAL_PR=1", "RELEASE_SH_TIMEOUT_SECONDS=2",
	}, extra...)
	return g.with(env...)
}

// TestIdenticalTreeHeadChecksEndTheWait: the squash-merge commit has no
// checks yet, but the merged PR's head has the same tree and all three
// green; the gate passes at once and says whose checks it used.
func TestIdenticalTreeHeadChecksEndTheWait(t *testing.T) {
	r := newReleaseRepo(t)
	g := identicalTreeEnv(newGHState(t, r, greenChecks()))
	start := time.Now()
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	head := testutil.Git(t, r.bare, "rev-parse", "refs/heads/release/v0.12.0")
	if !strings.Contains(res.out, "using the checks of "+head+" (identical tree)") {
		t.Errorf("expected the head's checks to be named:\n%s", res.out)
	}
	if strings.Contains(res.out, "still waiting") || time.Since(start) > 30*time.Second {
		t.Errorf("an identical-tree green head must not wait:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("tag missing:\n%s", res.out)
	}
}

// TestIdenticalTreeOwnChecksPreferred: when the target has complete green
// checks of its own, nothing is looked up.
func TestIdenticalTreeOwnChecksPreferred(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_IDENTICAL_PR=1")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if strings.Contains(res.out, "identical tree") || countLog(t, g, "api repos/") == 0 {
		t.Errorf("own green checks must be used without a lookup:\n%s", res.out)
	}
	if strings.Contains(g.log(t), "/pulls") || strings.Contains(g.log(t), "/git/commits/") {
		t.Errorf("no tree or pulls lookup expected:\n%s", g.log(t))
	}
}

// TestIdenticalTreeNotAccepted: every way a candidate can fail to vouch
// leaves the wait (and the timeout failure) as before.
func TestIdenticalTreeNotAccepted(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
	}{
		{"different tree", []string{"FAKE_GH_HEAD_TREE=0123456789abcdef0123456789abcdef01234567"}},
		{"head check failed", []string{"FAKE_GH_HEAD_RULES=failed"}},
		{"head check missing", []string{"FAKE_GH_HEAD_TERRAFORM=missing"}},
		{"head check pending", []string{"FAKE_GH_HEAD_TEST=pending"}},
		{"head check cancelled", []string{"FAKE_GH_HEAD_TEST=cancelled"}},
		{"PR not merged", []string{"FAKE_GH_PULL_UNMERGED=1"}},
		{"merge commit is another sha", []string{"FAKE_GH_PULL_MERGE_SHA=0123456789abcdef0123456789abcdef01234567"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := identicalTreeEnv(newGHState(t, r, greenChecks()), c.extra...)
			res := runRelease(t, r, g, "", "0.12.0")
			res.requireFailureContaining(t, "required check 'test' is missing")
			if strings.Contains(res.out, "identical tree") {
				t.Errorf("the candidate must not be used:\n%s", res.out)
			}
			requireNoTag(t, r, res)
		})
	}
}

// TestIdenticalTreeLookupFailureNeverPasses: a failing pulls or git api call
// is noted once on stderr and the gate behaves as without the shortcut.
func TestIdenticalTreeLookupFailureNeverPasses(t *testing.T) {
	for _, c := range []struct{ env, call string }{
		{"FAKE_GH_PULLS_FAIL=1", "pulls of"},
		{"FAKE_GH_GIT_FAIL=1", "git commit"},
	} {
		t.Run(c.env, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := identicalTreeEnv(newGHState(t, r, greenChecks()), c.env)
			res := runRelease(t, r, g, "", "0.12.0")
			res.requireFailureContaining(t, "required check 'test' is missing")
			if n := strings.Count(res.out, "identical-tree lookup unavailable ("+c.call); n != 1 {
				t.Errorf("want the note exactly once, got %d:\n%s", n, res.out)
			}
			requireNoTag(t, r, res)
		})
	}
}

// TestIdenticalTreeSecondPRCanVouch: a first associated PR that does not
// qualify does not stop a later one that does.
func TestIdenticalTreeSecondPRCanVouch(t *testing.T) {
	r := newReleaseRepo(t)
	g := identicalTreeEnv(newGHState(t, r, greenChecks()), "FAKE_GH_BAD_PR_FIRST=1")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	head := testutil.Git(t, r.bare, "rev-parse", "refs/heads/release/v0.12.0")
	if !strings.Contains(res.out, "using the checks of "+head+" (identical tree)") {
		t.Errorf("expected the second PR's head to be used:\n%s", res.out)
	}
}

// TestIdenticalTreeNeverOverridesTargetFailure: a conclusive failure on the
// target fails the gate at once, even next to a green identical-tree head.
func TestIdenticalTreeNeverOverridesTargetFailure(t *testing.T) {
	r := newReleaseRepo(t)
	g := identicalTreeEnv(newGHState(t, r, greenChecks()), "FAKE_GH_POST_RULES=failed")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "required check 'rules' is failed")
	if strings.Contains(res.out, "identical tree") {
		t.Errorf("the head's checks must not override a failure:\n%s", res.out)
	}
	requireNoTag(t, r, res)
}

// TestTagsMergeCommitWhenMainMoved: something else landed on main after the
// release merge; the tag still goes on the merge commit, not the tip.
func TestTagsMergeCommitWhenMainMoved(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_ADVANCE_AFTER_MERGE=1")
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireSuccess(t)
	tagged := testutil.Git(t, r.bare, "rev-parse", "refs/tags/v0.12.0^{commit}")
	tip := testutil.Git(t, r.bare, "rev-parse", "refs/heads/main")
	if tagged == tip {
		t.Errorf("the tag is on main's tip %s, want the release merge commit", tip)
	}
	if !strings.Contains(res.out, "tagging "+tagged+", not the tip") {
		t.Errorf("expected the script to say it tagged the merge commit:\n%s", res.out)
	}
}

func TestBumpCheckFailsAfterMerge(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_CORRUPT_MERGE=1")
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireFailureContaining(t, "want 0.12.0")
	requireNoTag(t, r, res)
}

func TestRemoteOnlyExistingTag(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", r.bare, other)
	testutil.Git(t, other, "tag", "-a", "v0.12.0", "-m", "x")
	testutil.Git(t, other, "push", "--quiet", "origin", "refs/tags/v0.12.0")
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireFailureContaining(t, "already exists on origin")
	if countLog(t, g, "pr create") != 0 {
		t.Errorf("no PR may be created:\n%s", res.out)
	}
}

func TestTimeoutMinutesEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no value", []string{"0.12.0", "--timeout-minutes"}},
		{"empty", []string{"0.12.0", "--timeout-minutes", ""}},
		{"zero", []string{"0.12.0", "--timeout-minutes", "0"}},
		{"negative", []string{"0.12.0", "--timeout-minutes", "-5"}},
		{"non-numeric", []string{"0.12.0", "--timeout-minutes", "abc"}},
		{"fraction", []string{"0.12.0", "--timeout-minutes", "1.5"}},
		{"leading zero", []string{"0.12.0", "--timeout-minutes", "05"}},
		{"multi-line", []string{"0.12.0", "--timeout-minutes", "5\nx"}},
		{"swallows the next flag", []string{"--timeout-minutes", "0.12.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			res := runRelease(t, r, g, "", c.args...)
			if exitCode(res.err) != 2 {
				t.Fatalf("want exit 2, got %v:\n%s", res.err, res.out)
			}
			if !strings.Contains(res.out, "--timeout-minutes") {
				t.Errorf("the message must name the flag:\n%s", res.out)
			}
		})
	}
	t.Run("valid", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		runRelease(t, r, g, "", "0.12.0", "--timeout-minutes", "7", "--dry-run").requireSuccess(t)
	})
}

func TestArgumentErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"0.12.0", "--bogus"}, "unknown flag: --bogus"},
		{"unknown short flag", []string{"-y", "0.12.0"}, "unknown flag: -y"},
		{"two versions", []string{"0.12.0", "0.12.1"}, "unexpected argument"},
		{"no version", nil, "usage:"},
		// --at is a commit, and one the person types: a bare flag is refused at
		// once, naming what it wants, rather than reading the next flag as one.
		{"--at without a commit", []string{"0.12.0", "--at"}, "--at needs a commit"},
		{"--at followed by a flag", []string{"0.12.0", "--at", "--dry-run"}, "--at needs a commit"},
		// The two cuts are exclusive: --at tags a commit of origin/main and
		// --hotfix builds and tags the maintenance branch's own tip.
		{"--at with --hotfix", []string{"0.12.0", "--at", "HEAD", "--hotfix"}, "--at tags a commit of origin/main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			res := runRelease(t, r, g, "", c.args...)
			if exitCode(res.err) != 2 || !strings.Contains(res.out, c.want) {
				t.Fatalf("want exit 2 and %q, got %v:\n%s", c.want, res.err, res.out)
			}
		})
	}
}

func TestStrictSemVerRejectsMultiLine(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "", "1.0.0\nfoo")
	res.requireFailureContaining(t, "not strict SemVer")

	root := testutil.ModuleRoot()
	for _, c := range []struct {
		script string
		args   []string
		code   int
	}{
		{"bump-plugin-version.sh", []string{"1.0.0\n1.0.0"}, 2},
		{"bump-plugin-version.sh", []string{"--check", "1.0.0\nx"}, 2},
		{"bump-plugin-version.sh", []string{"01.0.0"}, 2},
		{"release-gate.sh", []string{"v1.0.0\nx"}, 1},
		{"release-gate.sh", []string{"v01.0.0"}, 1},
		// The mobile app's stream under the same rule: the prefix is stripped
		// and the version under it is held to the strict shape, so a tag that
		// carries the prefix without a strict version -- one with no `v`, one
		// with a leading zero, one with a newline smuggled into it -- is
		// refused exactly as the CLI's own malformed tags are.
		{"release-gate.sh", []string{"mobile-1.0.0"}, 1},
		{"release-gate.sh", []string{"mobile-v01.0.0"}, 1},
		{"release-gate.sh", []string{"mobile-v1.0.0\nx"}, 1},
		{"release-gate.sh", []string{"mobile-vtest"}, 1},
	} {
		cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", c.script)}, c.args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if exitCode(err) != c.code {
			t.Errorf("%s %q: want exit %d, got %v\n%s", c.script, c.args, c.code, err, out)
		}
	}
}

// TestReleaseGateAcceptsTheMobileStreamsTag: the mobile app releases on its own
// tag (`mobile-vX.Y.Z`, docs/release.md's "Mobile releases"), and the gate is
// the one script every publishing workflow in this repository runs. It strips
// that single prefix and holds the version under it to the same strict vX.Y.Z
// shape, so the app's release is gated exactly as the CLI's is -- and the tag it
// names in the log is the tag the run was started from, not a rewritten one.
func TestReleaseGateAcceptsTheMobileStreamsTag(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	sha := strings.TrimSpace(testutil.Git(t, r.local, "rev-parse", "HEAD"))

	accepted := runGate(t, r, g, sha, "mobile-v1.0.0")
	accepted.requireSuccess(t)
	// The log names the mobile tag: a run that passed under a rewritten `v1.0.0`
	// would read as a CLI release in the actions log, which is the confusion the
	// two streams exist to end.
	if !strings.Contains(accepted.out, "gate passed for mobile-v1.0.0") {
		t.Errorf("the gate did not report the mobile tag it was given:\n%s", accepted.out)
	}

	// And the CLI's own tag is untouched by the change: the same commit passes
	// the same gate under `v1.0.0`, which is what every other release workflow
	// passes it.
	runGate(t, r, g, sha, "v1.0.0").requireSuccess(t)

	// Every refused shape is refused by the *shape* check and not later: an
	// off-shape tag must not reach the fetch, the checks API or a store, and the
	// message has to name the two shapes the gate accepts. The call count is read
	// around the loop, so a refusal that got as far as the API is visible.
	apiCalls := countLog(t, g, "api")
	for _, tag := range []string{"mobile-1.0.0", "mobile-v01.0.0", "mobile-v1.0", "mobile-v1.0.0-rc1", "mobile-vtest", "mobile-", "console-v1.0.0"} {
		res := runGate(t, r, g, sha, tag)
		if res.err == nil {
			t.Errorf("the gate accepted %q: only a strict vX.Y.Z and the app's strict mobile-vX.Y.Z name a release, and a tag of any other shape has no version to gate", tag)
			continue
		}
		if !strings.Contains(res.out, "not a strict vX.Y.Z or mobile-vX.Y.Z release tag") {
			t.Errorf("the gate refused %q for the wrong reason -- it must be the shape check, before any API call:\n%s", tag, res.out)
		}
	}
	if after := countLog(t, g, "api"); after != apiCalls {
		t.Errorf("the refused tags made %d gh api call(s): the shape check comes before the checks API is read, so a tag that is not a release of either stream reaches nothing", after-apiCalls)
	}
}

// runGate runs scripts/release-gate.sh from r.local against the commit sha, with
// the fake gh on PATH and g's checks: the gate reads the ci runs on the tag's
// commit and their check suites, so its answer depends on the same state a
// release.sh run would see.
func runGate(t *testing.T, r *releaseRepo, g *ghState, sha, tag string) runResult {
	t.Helper()
	return runGateEnv(t, r, g, sha, tag)
}

// runGateEnv is runGate with extra environment appended after the gate's own
// defaults, for a test that has to move one of them (GATE_WAIT_SECONDS, or the
// console release's GATE_REQUIRE_CONSOLE=1).
func runGateEnv(t *testing.T, r *releaseRepo, g *ghState, sha, tag string, extra ...string) runResult {
	t.Helper()
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release-gate.sh"), tag)
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"GITHUB_SHA="+sha,
		"GITHUB_REPOSITORY="+repoSlug(r.bare),
		"GATE_WAIT_SECONDS=0",
		"GATE_POLL_SECONDS=1",
	)
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Env = append(cmd.Env, extra...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
}

// ciRuns writes the fake's ci_runs state file: one "<sha><TAB><title><TAB>
// <suite>" line per `ci` run on that commit, which is what the gate's read of
// the commit's runs answers with. An optional fourth field is the run's
// run_number, which is what orders the runs when a commit has more than one --
// the gate reads the newest full one -- and defaults to the line's position. A
// commit the file does not name has no `ci` run at all -- what a tag on a commit
// nobody dispatched CI on looks like.
func ciRuns(t *testing.T, g *ghState, lines ...string) {
	t.Helper()
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	g.write(t, "ci_runs", body)
}

// tagCommit is the commit these gate tests cut their tag on: the fixture's
// HEAD, which is origin/main's tip, so the gate's dispatch hint names `main`.
func tagCommit(t *testing.T, r *releaseRepo) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, r.local, "rev-parse", "HEAD"))
}

// commitTree is the git tree id of a commit in dir, which the identical-tree
// fallback compares between the tag's commit and a pull request head.
func commitTree(t *testing.T, dir, sha string) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, dir, "rev-parse", sha+"^{tree}"))
}

// TestGateCountsAFullRunsChecks: `test`, `terraform` and `rules` come from the
// check suite of the full `ci` run on the tag's commit, and from nothing else
// (R1, D3040). A selected run on the same commit is not merged in -- its suite
// answers failed for every required name in the fake, so a gate that read it
// could not pass -- and a red check in the full run's own suite is the refusal.
func TestGateCountsAFullRunsChecks(t *testing.T) {
	r := newReleaseRepo(t)
	sha := tagCommit(t, r)
	g := newGHState(t, r, greenChecks())
	runGate(t, r, g, sha, "v0.12.0").requireSuccess(t)

	ciRuns(t, g, sha+"\tci (full)\t"+sha, sha+"\tci (selected)\t"+sha+"-selected")
	runGate(t, r, g, sha, "v0.12.0").requireSuccess(t)

	red := newGHState(t, r, greenChecks()).with("FAKE_GH_CHECK_TEST=failed")
	runGate(t, r, red, sha, "v0.12.0").requireFailureContaining(t, "required check 'test' is failed")
}

// TestGateIgnoresASelectedRun: a `ci (selected)` run does not vouch for a tag.
// The commit's own check runs used to be enough, and a selected run's `test` was
// one of them; now the gate refuses at once -- it does not wait out
// GATE_WAIT_SECONDS for a full run's checks to appear -- and prints the command
// that dispatches the full run, naming main because the commit is main's tip.
func TestGateIgnoresASelectedRun(t *testing.T) {
	r := newReleaseRepo(t)
	sha := tagCommit(t, r)
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, sha+"\tci (selected)\t"+sha+"-selected")

	start := time.Now()
	res := runGateEnv(t, r, g, sha, "v0.12.0", "GATE_WAIT_SECONDS=45")
	if res.err == nil {
		t.Fatalf("the gate accepted a tag on a commit whose only ci run is selected:\n%s", res.out)
	}
	if !strings.Contains(res.out, "gh workflow run ci --ref main -f full=true") {
		t.Errorf("the refusal does not print the command that dispatches a full run on main's tip:\n%s", res.out)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the gate waited %s on a commit whose ci runs can never satisfy it: it must refuse at once", elapsed)
	}
}

// TestGateAcceptsTheOldTitle: every run a ci.yml without a run-name: produced is
// titled exactly `ci`, and a tag cut before that change lands must still pass the
// gate. A third title names no full run, so it does not.
func TestGateAcceptsTheOldTitle(t *testing.T) {
	r := newReleaseRepo(t)
	sha := tagCommit(t, r)
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, sha+"\tci\t"+sha)
	runGate(t, r, g, sha, "v0.12.0").requireSuccess(t)

	ciRuns(t, g, sha+"\tci (nightly)\t"+sha)
	res := runGate(t, r, g, sha, "v0.12.0")
	if res.err == nil {
		t.Fatalf("the gate accepted a title that names neither `ci (full)` nor `ci`:\n%s", res.out)
	}
	if !strings.Contains(res.out, "no ci run on that commit is a full one") {
		t.Errorf("the refusal does not say why the commit's runs do not count:\n%s", res.out)
	}
}

// TestGateReadsTheNewestFullRun: only the newest full run's checks count, so a
// re-dispatch that went green clears a red full run before it -- the reading
// this replaces took the latest run per name, and a commit-level merge of every
// full run's suite would block the tag for good. The rows come back in the order
// the api listed them, which is why the fake writes the red run first: a gate
// that took the first full row instead of the highest run number would refuse
// here. The newest run being the red one is still the refusal.
func TestGateReadsTheNewestFullRun(t *testing.T) {
	r := newReleaseRepo(t)
	sha := tagCommit(t, r)
	g := newGHState(t, r, greenChecks())

	ciRuns(t, g,
		sha+"\tci (full)\t"+sha+"-red\t7",
		sha+"\tci (full)\t"+sha+"\t8")
	runGate(t, r, g, sha, "v0.12.0").requireSuccess(t)

	ciRuns(t, g,
		sha+"\tci (full)\t"+sha+"\t8",
		sha+"\tci (full)\t"+sha+"-red\t7")
	runGate(t, r, g, sha, "v0.12.0").requireSuccess(t)

	ciRuns(t, g,
		sha+"\tci (full)\t"+sha+"\t7",
		sha+"\tci (full)\t"+sha+"-red\t8")
	runGate(t, r, g, sha, "v0.12.0").requireFailureContaining(t, "required check 'test' is failed")
}

// TestGateFallbackAppliesTheTitleRule: the identical-tree fallback reads the
// merged pull request head's checks with the same rule as the target's, so a
// head whose only `ci` run is selected vouches for nothing -- and one whose run
// is full does. The target has no `ci` run of its own at all, which is the state
// the fallback exists for.
func TestGateFallbackAppliesTheTitleRule(t *testing.T) {
	r := newReleaseRepo(t)
	sha := tagCommit(t, r)
	// The head of the pull request merged as the target: an empty commit, so the
	// same tree, pushed on the branch the fake reports as the PR's head.
	testutil.Git(t, r.local, "checkout", "--quiet", "-b", "release/v0.12.0")
	testutil.Git(t, r.local, "commit", "--quiet", "--allow-empty", "-m", "the pull request head")
	head := strings.TrimSpace(testutil.Git(t, r.local, "rev-parse", "HEAD"))
	testutil.Git(t, r.local, "push", "--quiet", "origin", "HEAD:refs/heads/release/v0.12.0")
	testutil.Git(t, r.local, "checkout", "--quiet", "main")
	if headTree, targetTree := commitTree(t, r.local, head), commitTree(t, r.local, sha); headTree != targetTree {
		t.Fatalf("the fixture head's tree is %s, not the target's %s: the fallback is about identical trees", headTree, targetTree)
	}

	g := newGHState(t, r, greenChecks()).with("FAKE_GH_IDENTICAL_PR=1")
	g.write(t, "pr_branch", "release/v0.12.0")
	g.write(t, "pr_merge_sha", sha)

	ciRuns(t, g, head+"\tci (full)\t"+head)
	res := runGate(t, r, g, sha, "v0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "using the checks of "+head+" (identical tree)") {
		t.Errorf("the fallback did not use the full run on the head:\n%s", res.out)
	}

	ciRuns(t, g, head+"\tci (selected)\t"+head+"-selected")
	res = runGate(t, r, g, sha, "v0.12.0")
	if res.err == nil {
		t.Fatalf("the gate let a selected run on the pull request head vouch for the tag:\n%s", res.out)
	}
	if strings.Contains(res.out, "identical tree") {
		t.Errorf("a selected run's checks must not vouch:\n%s", res.out)
	}
	if !strings.Contains(res.out, "required check 'test' is missing on "+sha) {
		t.Errorf("the gate refused for the wrong reason:\n%s", res.out)
	}
}

// TestGateKeepsTheConsoleLegsReading: with GATE_REQUIRE_CONSOLE=1 the console
// release's gate requires the two matrix legs as it always did, and reads them
// from the commit's own check runs: they come from console-tests.yml, and no
// `ci` run's suite carries them -- a gate that looked for them in one could not
// pass at all.
func TestGateKeepsTheConsoleLegsReading(t *testing.T) {
	r := newReleaseRepo(t)
	sha := tagCommit(t, r)
	g := newGHState(t, r, greenChecks())
	runGateEnv(t, r, g, sha, "v0.12.0", "GATE_REQUIRE_CONSOLE=1").requireSuccess(t)

	red := newGHState(t, r, greenChecks()).with("FAKE_GH_CHECK_CONSOLE_MACOS=failed")
	runGateEnv(t, r, red, sha, "v0.12.0", "GATE_REQUIRE_CONSOLE=1").
		requireFailureContaining(t, "required check 'console (macos-latest)' is failed")
}

// TestLatestTagIgnoresNonStrictTags: -rc and four-part tags never count as
// "the latest release".
func TestLatestTagIgnoresNonStrictTags(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	for _, tg := range []string{"v9.0.0-rc1", "v9.0.0.1", "v09.0.0", "vfoo"} {
		testutil.Git(t, r.local, "tag", "-a", tg, "-m", "x")
	}
	runRelease(t, r, g, "", "0.12.0", "--dry-run").requireSuccess(t)
}

func TestOriginMustMatchGHRepo(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_REPO=other/thing")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "gh targets other/thing")
	if countLog(t, g, "pr create") != 0 {
		t.Errorf("no PR may be created:\n%s", res.out)
	}
}

func TestFailedBranchPushLeavesMainAndResumes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	installHook(t, r, `while read old new ref; do case "$ref" in refs/heads/release/*) echo "rejected" >&2; exit 1;; esac; done`)
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "pushing release/v0.12.0 failed")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	removeHook(t, r)
	runRelease(t, r, g, "", "0.12.0").requireSuccess(t)
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("the re-run did not finish the release")
	}
}

func TestFailedPRCreateLeavesMainAndResumes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g.with("FAKE_GH_PR_CREATE_FAIL=1"), "", "0.12.0")
	res.requireFailureContaining(t, "'gh pr create' failed")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	res = runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "resuming pushed branch") {
		t.Errorf("expected to resume the pushed branch:\n%s", res.out)
	}
}

// TestSignalsLeaveMain: a signal while the release branch is checked out
// (here: during the branch push) ends on main with the resume message.
func TestSignalsLeaveMain(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			marker := filepath.Join(t.TempDir(), "in-push")
			installHook(t, r, fmt.Sprintf(`touch %s; sleep 2`, marker))
			bin := fakeBinDir(t)
			cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "0.12.0")
			cmd.Dir = r.local
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
			cmd.Env = append(cmd.Env, rehearsalEnv(r, bin)...)
			cmd.Env = append(cmd.Env, g.env...)
			cmd.Env = append(cmd.Env, releaseEnv(t)...)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(60 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatalf("never reached the push:\n%s", out.String())
				}
				time.Sleep(20 * time.Millisecond)
			}
			if b := currentBranch(t, r); b != "release/v0.12.0" {
				t.Fatalf("expected to be on the release branch mid-push, on %q", b)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if b := currentBranch(t, r); b != "main" {
				t.Errorf("checkout left on %q, want main", b)
			}
			if !strings.Contains(out.String(), "Re-run") {
				t.Errorf("expected resume instructions:\n%s", out.String())
			}
		})
	}
}

// failedTagPush runs a release whose final tag push is rejected by origin.
func failedTagPush(t *testing.T) (*releaseRepo, *ghState) {
	t.Helper()
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	installHook(t, r, `while read old new ref; do case "$ref" in refs/tags/*) echo "rejected" >&2; exit 1;; esac; done`)
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "git push origin refs/tags/v0.12.0")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	if strings.Contains(res.out, "tag v0.12.0 pushed") {
		t.Errorf("must not claim the tag was pushed:\n%s", res.out)
	}
	if out := testutil.Git(t, r.local, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("the local tag should remain for a retry")
	}
	removeHook(t, r)
	return r, g
}

func TestFailedTagPushThenRerunPushesLocalTag(t *testing.T) {
	r, g := failedTagPush(t)
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "exists locally at this commit but is not on origin") {
		t.Errorf("expected the local-tag notice:\n%s", res.out)
	}
	merge := testutil.Git(t, r.bare, "rev-parse", "main")
	if tag := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}"); tag != merge {
		t.Errorf("origin's tag is at %s, want the merge commit %s", tag, merge)
	}
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("the re-run must not open another PR:\n%s", g.log(t))
	}
}

func TestLocalTagWrongCommit(t *testing.T) {
	r, g := failedTagPush(t)

	// A local tag on some other commit is refused, never pushed.
	testutil.Git(t, r.local, "tag", "-d", "v0.12.0")
	testutil.Git(t, r.local, "tag", "-a", "v0.12.0", "-m", "x", "HEAD~1")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "not the verified merge commit")
	requireNoTag(t, r, res)
}

func TestResumeOpenPR(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	pushReleaseBranch(t, r, "0.12.0")
	g.write(t, "pr_number", "4")
	g.write(t, "pr_state", "OPEN")
	g.write(t, "pr_branch", "release/v0.12.0")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if countLog(t, g, "pr create") != 0 || countLog(t, g, "pr merge --auto --squash 4") != 1 {
		t.Errorf("want auto-merge on the open PR #4 and no new PR:\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("tag missing:\n%s", res.out)
	}
}

func TestExistingRemoteBranchWithoutPR(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	initial := testutil.Git(t, r.bare, "rev-parse", "main")
	pushReleaseBranch(t, r, "0.12.0")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "resuming pushed branch release/v0.12.0") {
		t.Errorf("expected the pushed branch to be reused:\n%s", res.out)
	}
	if n := testutil.Git(t, r.bare, "rev-list", "--count", initial+"..refs/heads/release/v0.12.0"); n != "2" {
		t.Errorf("want the pushed bump commit and the catalogue and docs commit on the branch, got %s", n)
	}
	// The catalogue and the two doc lines that name the released version
	// (releasedVersionDocs), sorted as git lists them.
	want := "docs/project-claude-accounts.md\ndocs/project-named-oauth-tokens/spec.md\ndocs/release-streams.yaml"
	if files := testutil.Git(t, r.bare, "diff-tree", "--no-commit-id", "--name-only", "-r", "refs/heads/release/v0.12.0"); files != want {
		t.Errorf("the commit on top of the pushed bump should touch the catalogue and the released-version docs only, got:\n%s", files)
	}
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("want one PR:\n%s", g.log(t))
	}
}

// TestResumedBranchWithTheBumpStillRecordsTheStream: a release branch pushed by
// hand (or by an earlier script that predates the catalogue) already carries
// the plugin bump, so the bump is skipped; the catalogue's cli row must still
// move, or main keeps describing the release before this one.
func TestResumedBranchWithTheBumpStillRecordsTheStream(t *testing.T) {
	r := newReleaseRepo(t)
	rewriteFixtureStreams(t, r, streamsFixtureReleased)
	addReleaseNotes(t, r, "0.11.2")
	g := newGHState(t, r, greenChecks())
	pushReleaseBranch(t, r, "0.11.2")

	res := runRelease(t, r, g, "", "0.11.2")

	res.requireSuccess(t)
	catalogue := testutil.Git(t, r.bare, "show", "main:docs/release-streams.yaml")
	if !strings.Contains(catalogue, `current: "0.11.2"`) {
		t.Errorf("docs/release-streams.yaml on main does not record 0.11.2 as the cli stream's current:\n%s", catalogue)
	}
}

// TestResumedBranchWhoseCatalogueRefusesTheVersion: the precondition read main's
// catalogue, but a hand-pushed branch may carry its own; one that already
// records a later release is refused before anything is committed or opened.
func TestResumedBranchWhoseCatalogueRefusesTheVersion(t *testing.T) {
	r := newReleaseRepo(t)
	rewriteFixtureStreams(t, r, streamsFixtureReleased)
	addReleaseNotes(t, r, "0.11.2")
	g := newGHState(t, r, greenChecks())
	clone := filepath.Join(t.TempDir(), "earlier")
	testutil.Git(t, filepath.Dir(clone), "clone", "--quiet", r.bare, clone)
	testutil.Git(t, clone, "switch", "--quiet", "-c", "release/v0.11.2")
	writeStreams(t, clone, strings.Replace(streamsFixtureReleased, `current: "0.11.1"`, `current: "0.11.3"`, 1))
	testutil.Git(t, clone, "commit", "--quiet", "-am", "release: v0.11.2")
	testutil.Git(t, clone, "push", "--quiet", "origin", "release/v0.11.2")

	res := runRelease(t, r, g, "", "0.11.2")

	res.requireFailureContaining(t, "on release/v0.11.2 cannot record 0.11.2")
	if countLog(t, g, "pr create") != 0 {
		t.Errorf("a refused branch must not reach 'gh pr create':\n%s", g.log(t))
	}
	if b := currentBranch(t, r); b != "main" {
		t.Errorf("want the checkout back on main, got %s", b)
	}
}

func TestClosedPRWithBranchDeletedOpensNewOne(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_number", "7")
	g.write(t, "pr_state", "CLOSED")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("the documented recovery (branch deleted) must open a fresh PR:\n%s", g.log(t))
	}
}

func TestAutoMergeUnavailable(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_AUTOMERGE_FAIL=1")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "could not enable auto-merge")
	res.requireFailureContaining(t, "Merge the pull request yourself, then re-run")
	requireNoTag(t, r, res)
	if b := currentBranch(t, r); b != "main" {
		t.Errorf("checkout left on %q", b)
	}
}

func TestCancelledCheckIsRetriedOnce(t *testing.T) {
	t.Run("transient", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		g.write(t, "pr_check_fail", "rules CANCELLED")
		g.write(t, "pr_check_once", "")
		res := runRelease(t, r, g, "", "0.12.0")
		res.requireSuccess(t)
		if !strings.Contains(res.out, "was cancelled") {
			t.Errorf("expected a retry notice:\n%s", res.out)
		}
	})
	t.Run("persistent", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		g.write(t, "pr_check_fail", "rules CANCELLED")
		res := runRelease(t, r, g, "", "0.12.0")
		res.requireFailureContaining(t, "check 'rules' failed (CANCELLED)")
		if countLog(t, g, "pr checks") < 2 {
			t.Errorf("a cancelled check must be given one more poll:\n%s", g.log(t))
		}
	})
}

// TestHighlightsRequiredFrom040 checks that a release from 0.4.0 on stops in
// the preconditions, naming the file, when docs/releases/vX.Y.Z.md is not on
// main (or is empty), and that older versions are unaffected.
func TestHighlightsRequiredFrom040(t *testing.T) {
	cases := []struct {
		name    string
		version string
		file    string // content to commit on main; "-" leaves the file out
		want    string // "" means the dry run must succeed
	}{
		{"missing for 0.4.0", "0.4.0", "-", "docs/releases/v0.4.0.md"},
		{"missing for 0.14.3", "0.14.3", "-", "docs/releases/v0.14.3.md"},
		{"missing for 0.10.0 (numeric compare)", "0.10.0", "-", "docs/releases/v0.10.0.md"},
		{"empty for 0.4.0", "0.4.0", "  \n", "is empty"},
		// A release below 0.10 is refused by the streams check (the CLI's
		// named line starts there), so the dry runs that pass are on the line;
		// "not needed below 0.4.0" is release-policy.sh's own rule, pinned by
		// TestCheckHighlights and TestReleasePolicy.
		{"present for 0.10.0", "0.10.0", "- Something.\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			if c.file != "-" {
				writeHighlights(t, r.local, c.version, c.file)
				testutil.Git(t, r.local, "add", "-A")
				testutil.Git(t, r.local, "commit", "--quiet", "-m", "highlights")
				testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
			}
			// The summaries entry this test is not about (TestSummaries...)
			// goes in, so a case that expects the run to pass does not stop on
			// the later precondition instead, and so does the matrix's stamp
			// (TestPreconditionFailures' FEATURES.md cases).
			writeSummaries(t, r.local, map[string]string{c.version: "Something a user sees."})
			writeFeatures(t, r.local, c.version)
			testutil.Git(t, r.local, "add", "-A")
			testutil.Git(t, r.local, "commit", "--quiet", "-m", "summary")
			testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
			res := runRelease(t, r, g, "", c.version, "--dry-run")
			if c.want == "" {
				res.requireSuccess(t)
				return
			}
			res.requireFailureContaining(t, c.want)
			if countLog(t, g, "pr ") != 0 {
				t.Errorf("a missing highlights file must stop before any pull request:\n%s", res.out)
			}
			if out := testutil.Git(t, r.bare, "branch", "--list", "release/v"+c.version); out != "" {
				t.Errorf("a branch was created: %s", out)
			}
		})
	}
}

// TestHighlightsMustBeCommitted checks that a Highlights file which is not on
// origin/main never counts: untracked is refused by the clean-tree rule, and
// committed but unpushed by the main == origin/main rule.
func TestHighlightsMustBeCommitted(t *testing.T) {
	t.Run("untracked", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		writeHighlights(t, r.local, "0.4.0", "- x\n")
		res := runRelease(t, r, g, "", "0.4.0", "--dry-run")
		res.requireFailureContaining(t, "working tree is not clean")
	})
	t.Run("committed but unpushed", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		writeHighlights(t, r.local, "0.4.0", "- x\n")
		testutil.Git(t, r.local, "add", "-A")
		testutil.Git(t, r.local, "commit", "--quiet", "-m", "highlights")
		res := runRelease(t, r, g, "", "0.4.0", "--dry-run")
		res.requireFailureContaining(t, "is not origin/main")
	})
}

// TestSummariesRequiredFrom040 checks the precondition the changelog page put
// in the release path: the site's build reads one end-user summary per release
// from website/data/release-summaries.yaml and stops, naming the file, when a
// release file has no entry there (website/README.md, website/src/content.ts).
// So a release whose entry is not on main must be refused before the branch and
// the pull request — a release that reaches the tag without it publishes a site
// that cannot build.
func TestSummariesRequiredFrom040(t *testing.T) {
	cases := []struct {
		name    string
		version string
		entries map[string]string // nil leaves the file out entirely
		want    string            // "" means the dry run must succeed
	}{
		{"missing file for 0.4.0", "0.4.0", nil, "website/data/release-summaries.yaml"},
		{"missing entry for 0.14.3", "0.14.3", map[string]string{"0.14.2": "An older release."}, "carries no entry for v0.14.3"},
		{"blank entry for 0.4.0", "0.4.0", map[string]string{"0.4.0": ""}, "carries no entry for v0.4.0"},
		{"spaces-only entry for 0.10.0 (numeric compare)", "0.10.0", map[string]string{"0.10.0": "   "}, "carries no entry for v0.10.0"},
		{"only a longer tag's entry counts as another version", "0.14.3", map[string]string{"0.14.30": "A different release."}, "carries no entry for v0.14.3"},
		// As in TestHighlightsRequiredFrom040: a passing dry run is on the
		// CLI's named line, and the below-0.4.0 rule is release-policy.sh's.
		{"present for 0.10.0", "0.10.0", map[string]string{"0.10.0": "Something a user sees."}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			// The version's Highlights go in first, so a failure below is the
			// summaries precondition's and not the earlier one's.
			writeHighlights(t, r.local, c.version, "- Something a user sees.\n")
			if c.entries != nil {
				writeSummaries(t, r.local, c.entries)
			}
			// As in TestHighlightsRequiredFrom040: the release's own three
			// files go in together, so a case that expects the run to pass
			// does not stop on a later precondition instead.
			writeFeatures(t, r.local, c.version)
			testutil.Git(t, r.local, "add", "-A")
			testutil.Git(t, r.local, "commit", "--quiet", "-m", "release notes")
			testutil.Git(t, r.local, "push", "--quiet", "origin", "main")

			res := runRelease(t, r, g, "", c.version, "--dry-run")
			if c.want == "" {
				res.requireSuccess(t)
				return
			}
			res.requireFailureContaining(t, c.want)
			if !strings.Contains(res.out, "website/data/release-summaries.yaml") {
				t.Errorf("the message must name the file the entry belongs in:\n%s", res.out)
			}
			if countLog(t, g, "pr ") != 0 {
				t.Errorf("a missing summary entry must stop before any pull request:\n%s", res.out)
			}
			if out := testutil.Git(t, r.bare, "branch", "--list", "release/v"+c.version); out != "" {
				t.Errorf("a branch was created: %s", out)
			}
		})
	}
}

// TestSummariesMustBeCommitted mirrors TestHighlightsMustBeCommitted for the
// summaries file: an entry that is not on origin/main never counts.
func TestSummariesMustBeCommitted(t *testing.T) {
	t.Run("untracked", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		writeHighlights(t, r.local, "0.4.0", "- x\n")
		writeSummaries(t, r.local, map[string]string{"0.4.0": "Something."})
		res := runRelease(t, r, g, "", "0.4.0", "--dry-run")
		res.requireFailureContaining(t, "working tree is not clean")
	})
	t.Run("committed but unpushed", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		writeHighlights(t, r.local, "0.4.0", "- x\n")
		writeSummaries(t, r.local, map[string]string{"0.4.0": "Something."})
		testutil.Git(t, r.local, "add", "-A")
		testutil.Git(t, r.local, "commit", "--quiet", "-m", "release notes")
		res := runRelease(t, r, g, "", "0.4.0", "--dry-run")
		res.requireFailureContaining(t, "is not origin/main")
	})
}

// TestCheckHighlights covers `release-policy.sh check-highlights`, the step
// release.yml and images.yml run (in a checkout of the tag) before publishing.
func TestCheckHighlights(t *testing.T) {
	script := filepath.Join(testutil.ModuleRoot(), "scripts", "release-policy.sh")
	cases := []struct {
		name, version, file string // file "-" means absent
		wantOK              bool
	}{
		{"required and present", "0.4.0", "- x\n", true},
		{"required and missing", "0.4.0", "-", false},
		{"required and blank", "1.2.3", " \n\n", false},
		{"not required and missing", "0.3.1", "-", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.file != "-" {
				writeHighlights(t, dir, c.version, c.file)
			}
			cmd := exec.Command("bash", script, "check-highlights", c.version)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.wantOK {
				t.Fatalf("ok=%v want %v:\n%s", err == nil, c.wantOK, out)
			}
			if !c.wantOK && !strings.Contains(string(out), "docs/releases/v"+c.version+".md") {
				t.Errorf("message does not name the file:\n%s", out)
			}
		})
	}
}

// TestReleasePolicy covers scripts/release-policy.sh, which the release
// workflow uses to decide the pre-release flag and whether highlights are
// required.
func TestReleasePolicy(t *testing.T) {
	script := filepath.Join(testutil.ModuleRoot(), "scripts", "release-policy.sh")
	cases := []struct {
		cmd, version string
		want         int // exit code: 0 yes, 1 no, 2 usage
	}{
		{"prerelease", "0.1.0", 0},
		{"prerelease", "0.3.1", 0},
		{"prerelease", "0.3.99", 0},
		{"prerelease", "0.4.0", 1},
		{"prerelease", "0.4.1", 1},
		{"prerelease", "0.10.0", 1},
		{"prerelease", "0.100.0", 1},
		{"prerelease", "1.0.0", 1},
		{"prerelease", "10.0.0", 1},
		{"highlights-required", "0.3.1", 1},
		{"highlights-required", "0.4.0", 0},
		{"highlights-required", "0.9.0", 0},
		{"highlights-required", "0.10.0", 0},
		{"highlights-required", "1.0.0", 0},
		{"highlights-required", "0.0.1", 1},
		{"prerelease", "v0.3.1", 2},
		{"prerelease", "0.4", 2},
		{"prerelease", "04.0.0", 2},
		{"prerelease", "0.4.0-rc1", 2},
		{"prerelease", "", 2},
		{"bogus", "0.4.0", 2},
	}
	for _, c := range cases {
		t.Run(c.cmd+" "+c.version, func(t *testing.T) {
			out, err := exec.Command("bash", script, c.cmd, c.version).CombinedOutput()
			got := 0
			if err != nil {
				got = exitCode(err)
			}
			if got != c.want {
				t.Errorf("exit %d, want %d:\n%s", got, c.want, out)
			}
		})
	}
}

// releasedVersionDocs are the two doc lines internal/cli's docs tests pin to
// the version the tree carries (TestDocsProjectNamedOAuthTokensStatusNamesTheReleasedCLI,
// TestDocsProjectClaudeAccountsOperatorGuide), as fixture text: the file, the
// line with the version left as %s, and the lines around it, which the release
// must leave alone.
var releasedVersionDocs = []struct{ path, before, line, after string }{
	{
		path:   "docs/project-named-oauth-tokens/spec.md",
		before: "# Project-level named Claude OAuth tokens\n\n",
		line:   "Status: **built, release 0.9.0 (released; the released CLI is %s)**, 2026-10-19. Was: design,\n",
		after:  "2026-10-18, **RULED** (release 0.9.0).\n",
	},
	{
		path:   "docs/project-claude-accounts.md",
		before: "# Project Claude accounts: one OAuth token per (profile, account)\n\n",
		line:   "Status: **0.9.0, released.** The released Fugaro is %s, and 0.9.0 is the first release any of this page works on:\n",
		after:  "the `claude_accounts` field inside a profile.\n",
	},
}

func releasedVersionDoc(i int, version string) string {
	d := releasedVersionDocs[i]
	return d.before + fmt.Sprintf(d.line, version) + d.after
}

func writeReleasedVersionDocs(t *testing.T, dir, version string) {
	t.Helper()
	for i, d := range releasedVersionDocs {
		writeDoc(t, dir, d.path, releasedVersionDoc(i, version))
	}
}

// writeCompatibility writes the fixture's website/data/compatibility.yaml with
// the cli half of the matrix's "as of" stamp at version, which is the one value
// release.sh reads out of the file (the release's own PR moves it with the notes
// file, and website/test/compatibility.mjs refuses a stamp behind the newest
// notes file). The console half is a version the CLI never releases, so a read
// that took the wrong half is a failure rather than a pass.
func writeCompatibility(t *testing.T, dir, version string) {
	t.Helper()
	writeDoc(t, dir, "website/data/compatibility.yaml",
		"version: 1\nas_of:\n  cli: v"+version+"\n  console: v0.125.0\n")
}

// writeDoc writes content at dir/name, creating its directory, and commits
// nothing.
func writeDoc(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseRewritesTheReleasedVersionDocLines: the two doc lines that name
// the released version move to the new version in the release commit itself,
// since a docs test pins them to the tree's version and so they can change in
// no other commit (0.9.5's first bump went red on main for want of them). The
// rewrite is idempotent: a line that already names the version is left as is.
func TestReleaseRewritesTheReleasedVersionDocLines(t *testing.T) {
	for _, from := range []string{"0.11.4", "0.12.0"} {
		t.Run("from "+from, func(t *testing.T) {
			r := newReleaseRepo(t)
			if from != "0.11.4" {
				writeReleasedVersionDocs(t, r.local, from)
				testutil.Git(t, r.local, "add", "-A")
				testutil.Git(t, r.local, "commit", "--quiet", "-m", "fixture: docs at "+from)
				testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
			}
			g := newGHState(t, r, greenChecks())
			initial := testutil.Git(t, r.bare, "rev-parse", "main")

			res := runRelease(t, r, g, "", "0.12.0")

			res.requireSuccess(t)
			if n := testutil.Git(t, r.bare, "rev-list", "--count", initial+"..refs/heads/release/v0.12.0"); n != "1" {
				t.Errorf("want the doc lines in the release commit itself, got %s commits on the release branch", n)
			}
			for i, d := range releasedVersionDocs {
				got := testutil.Git(t, r.bare, "show", "main:"+d.path) + "\n"
				if want := releasedVersionDoc(i, "0.12.0"); got != want {
					t.Errorf("%s on main after releasing 0.12.0:\n%s\nwant:\n%s", d.path, got, want)
				}
			}
		})
	}
}

// TestReleaseRefusesAReleasedVersionDocLineOfAnotherShape: a doc line that no
// longer reads the way the rewrite expects (reworded, or gone) is a refusal at
// step 1, naming the file, before any branch exists -- not a release commit that
// leaves the line behind and goes red on main.
func TestReleaseRefusesAReleasedVersionDocLineOfAnotherShape(t *testing.T) {
	for i, d := range releasedVersionDocs {
		t.Run(d.path, func(t *testing.T) {
			r := newReleaseRepo(t)
			reworded := strings.Replace(releasedVersionDoc(i, "0.11.4"), "released", "shipped", -1)
			writeDoc(t, r.local, d.path, reworded)
			testutil.Git(t, r.local, "add", "-A")
			testutil.Git(t, r.local, "commit", "--quiet", "-m", "fixture: reworded")
			testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
			g := newGHState(t, r, greenChecks())

			res := runRelease(t, r, g, "", "0.12.0", "--dry-run")

			res.requireFailureContaining(t, d.path)
			if out := testutil.Git(t, r.bare, "branch", "--list", "release/v0.12.0"); out != "" {
				t.Errorf("a refused doc line must not leave a release branch on origin:\n%s", res.out)
			}
		})
	}
}

// writeFlakyStub writes a command that fails with a transient network error on
// its first fails calls and prints "ok" after, counting its calls in a file
// beside it; it returns the command's path and the count file's.
func writeFlakyStub(t *testing.T, fails int, message string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	count := filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "flaky")
	body := fmt.Sprintf(`#!/bin/sh
n=$(($(cat %[1]q 2>/dev/null || echo 0) + 1))
echo "$n" >%[1]q
if [ "$n" -le %[2]d ]; then
  echo "partial"
  echo %[3]q >&2
  exit 1
fi
echo ok
`, count, fails, message)
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, count
}

// runRetryRead runs scripts/retry.sh's retry_read on cmd with no sleep.
func runRetryRead(t *testing.T, cmd ...string) (string, string, error) {
	t.Helper()
	script := `. "$1"; shift; retry_read "$@"`
	args := append([]string{"-c", script, "bash", filepath.Join(testutil.ModuleRoot(), "scripts", "retry.sh")}, cmd...)
	c := exec.Command("bash", args...)
	c.Env = append(os.Environ(), "RELEASE_RETRY_SECONDS=0")
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}

func readCount(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestRetryReadSucceedsAfterTwoTransientFailures: a read that fails twice with
// a network error and then answers is retried, and the caller sees only the
// answer -- not the failed tries' partial output.
func TestRetryReadSucceedsAfterTwoTransientFailures(t *testing.T) {
	for _, msg := range []string{
		"net/http: TLS handshake timeout",
		"unexpected EOF",
		"dial tcp 140.82.112.6:443: i/o timeout",
		"read: connection reset by peer",
		"fatal: unable to access 'https://github.com/o/r/': Recv failure: Connection reset by peer",
	} {
		t.Run(msg, func(t *testing.T) {
			stub, count := writeFlakyStub(t, 2, msg)
			out, errOut, err := runRetryRead(t, stub)
			if err != nil {
				t.Fatalf("retry_read failed: %v\n%s", err, errOut)
			}
			if out != "ok\n" {
				t.Errorf("stdout = %q, want only the successful try's %q", out, "ok\n")
			}
			if n := readCount(t, count); n != "3" {
				t.Errorf("the stub ran %s times, want 3", n)
			}
		})
	}
}

// TestRetryReadIsBounded: three transient failures in a row end it, with the
// last error passed on and the command's exit status kept.
func TestRetryReadIsBounded(t *testing.T) {
	stub, count := writeFlakyStub(t, 5, "net/http: TLS handshake timeout")
	_, errOut, err := runRetryRead(t, stub)
	if exitCode(err) != 1 {
		t.Fatalf("want the stub's exit status 1, got %v\n%s", err, errOut)
	}
	if n := readCount(t, count); n != "3" {
		t.Errorf("the stub ran %s times, want 3", n)
	}
	if !strings.Contains(errOut, "TLS handshake timeout") {
		t.Errorf("the last error is not passed on:\n%s", errOut)
	}
}

// TestRetryReadDoesNotRetryAPermanentError: an answer that is not a network
// error (a 404, a refusal) is final on the first try.
func TestRetryReadDoesNotRetryAPermanentError(t *testing.T) {
	stub, count := writeFlakyStub(t, 5, "HTTP 404: Not Found")
	if _, errOut, err := runRetryRead(t, stub); err == nil {
		t.Fatalf("retry_read succeeded on a permanent error\n%s", errOut)
	}
	if n := readCount(t, count); n != "1" {
		t.Errorf("the stub ran %s times, want 1", n)
	}
}

// TestRetryReleaseRidesOutTransientGHErrors: a release whose gh calls fail
// twice with a network error -- the repository read, the pull request's state,
// the release gate's check polling, and the auto-merge request -- still tags.
// The auto-merge request is not read-only: before it is repeated the pull
// request's state is read again.
func TestRetryReleaseRidesOutTransientGHErrors(t *testing.T) {
	for _, c := range []struct{ prefix, count string }{
		{"repo view", "2"},
		{"pr view", "2"},
		{"api", "2"},
		{"pr merge", "2"},
	} {
		t.Run(c.prefix, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks()).with("FAKE_GH_FLAKY="+c.prefix, "FAKE_GH_FLAKY_CALLS="+c.count)
			res := runRelease(t, r, g, "", "0.12.0")
			res.requireSuccess(t)
			if !strings.Contains(res.out, "retrying") {
				t.Errorf("want the retry noted:\n%s", res.out)
			}
			if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
				t.Errorf("tag missing:\n%s", res.out)
			}
			if c.prefix == "pr merge" {
				if n := countLog(t, g, "pr merge"); n != 3 {
					t.Errorf("want 3 'pr merge' calls, got %d:\n%s", n, g.log(t))
				}
			}
		})
	}
}

// TestRetryReleaseGivesUpOnAPersistentGHError: the retry is bounded, so a gh
// read that keeps failing still stops the release with its own message.
func TestRetryReleaseGivesUpOnAPersistentGHError(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_FLAKY=repo view", "FAKE_GH_FLAKY_CALLS=9")
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireFailureContaining(t, "'gh repo view' failed")
	if n := countLog(t, g, "repo view"); n != 3 {
		t.Errorf("want 3 'repo view' calls, got %d", n)
	}
}

// TestRetryTagPushAfterATransientFailure: a tag push that fails with a network
// error is repeated only after origin has been read and found without the tag.
func TestRetryTagPushAfterATransientFailure(t *testing.T) {
	r := newReleaseRepo(t)
	marker := filepath.Join(t.TempDir(), "rejected-once")
	installHook(t, r, fmt.Sprintf(`while read old new ref; do case "$ref" in refs/tags/*) if [ ! -e %[1]q ]; then touch %[1]q; echo "unexpected EOF" >&2; exit 1; fi;; esac; done`, marker))
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "", "0.12.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "retrying") {
		t.Errorf("want the retry noted:\n%s", res.out)
	}
	tagSHA := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}")
	if mainSHA := testutil.Git(t, r.bare, "rev-parse", "main"); tagSHA != mainSHA {
		t.Errorf("tag v0.12.0 (%s) does not point at main (%s)", tagSHA, mainSHA)
	}
}

// TestReleaseGateAcceptsAPatchOnItsMaintenanceBranch: a hotfix is a patch
// release of a line that was released from main, cut from that line's own
// maintenance branch so it carries the fix and nothing merged since. The gate
// accepts such a commit only when every condition holds, and refuses each
// other shape by the same "not on main" refusal: a commit on main is accepted
// as before, and nothing else is. A patch of an older line is refused by its
// own message, because it would be published as the latest release.
func TestReleaseGateAcceptsAPatchOnItsMaintenanceBranch(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }

	// v1.2.0 is released from main; release/1.2 is cut from it and takes one fix.
	git("tag", "-a", "v1.2.0", "-m", "v1.2.0")
	git("push", "--quiet", "origin", "v1.2.0")
	git("checkout", "--quiet", "-b", "release/1.2", "v1.2.0")
	git("commit", "--quiet", "--allow-empty", "-m", "the fix")
	fix := git("rev-parse", "HEAD")
	git("push", "--quiet", "origin", "release/1.2")
	git("checkout", "--quiet", "main")

	// The patch release's plugin bump belongs on main as well: the branch is never
	// merged back, so a version only the branch carries leaves main serving a
	// plugin older than the published tag — what made `fugaro update` on 0.21.1
	// install main's 0.21.0 on 2026-11-09. The gate refuses it by its own message.
	behind := runGate(t, r, g, fix, "v1.2.1")
	if behind.err == nil {
		t.Fatalf("the gate accepted a patch whose plugin version main does not carry:\n%s", behind.out)
	}
	if !strings.Contains(behind.out, "main's plugin/.claude-plugin/plugin.json says '0.11.4'") {
		t.Errorf("the tag was refused for the wrong reason:\n%s", behind.out)
	}

	// Bump the plugin on main (where the fix went), then cherry-pick that commit
	// onto the line's branch with the fix, as the section's step 3 says.
	bump := exec.Command("bash", filepath.Join(r.local, "scripts", "bump-plugin-version.sh"), "1.2.1")
	bump.Dir = r.local
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bump: %v\n%s", err, out)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "plugin: 1.2.1")
	bumpSHA := git("rev-parse", "HEAD")
	git("push", "--quiet", "origin", "main")
	git("checkout", "--quiet", "release/1.2")
	git("cherry-pick", "-x", bumpSHA)
	fix = git("rev-parse", "HEAD")
	git("push", "--quiet", "origin", "release/1.2")
	git("checkout", "--quiet", "main")

	// While 1.2 is the newest line: accepted on its branch, and a commit on main
	// is accepted exactly as before.
	accepted := runGate(t, r, g, fix, "v1.2.1")
	accepted.requireSuccess(t)
	if !strings.Contains(accepted.out, "maintenance branch release/1.2") || !strings.Contains(accepted.out, "gate passed for v1.2.1") {
		t.Errorf("the gate did not say it took the maintenance path:\n%s", accepted.out)
	}
	runGate(t, r, g, git("rev-parse", "main"), "v1.2.1").requireSuccess(t)

	// A patch whose only `ci` run is a selected one is refused at once, and the
	// command it prints names the branch the commit is on rather than main: a
	// dispatch is by ref, and release/1.2 is the ref that points at the fix.
	sel := newGHState(t, r, greenChecks())
	ciRuns(t, sel, fix+"\tci (selected)\t"+fix+"-selected")
	selected := runGate(t, r, sel, fix, "v1.2.1")
	if selected.err == nil {
		t.Fatalf("the gate accepted a patch whose only ci run is a selected one:\n%s", selected.out)
	}
	if want := "gh workflow run ci --ref release/1.2 -f full=true"; !strings.Contains(selected.out, want) {
		t.Errorf("want %q in:\n%s", want, selected.out)
	}

	// A commit on a branch that is not the line's own, cut from the same tag.
	git("checkout", "--quiet", "-b", "scratch", "v1.2.0")
	git("commit", "--quiet", "--allow-empty", "-m", "unrelated work")
	scratch := git("rev-parse", "HEAD")
	git("push", "--quiet", "origin", "scratch")

	// A tag named like the line's remote-tracking branch, on a commit that is not
	// on the branch: an unqualified ref would resolve the tag first and treat the
	// commit as being on the branch.
	git("commit", "--quiet", "--allow-empty", "-m", "impersonator")
	impostor := git("rev-parse", "HEAD")
	git("tag", "origin/release/1.2", impostor)
	git("checkout", "--quiet", "main")

	// A line whose first release was never on main: v1.4.0 exists only on its branch.
	git("checkout", "--quiet", "-b", "release/1.4", "main")
	git("commit", "--quiet", "--allow-empty", "-m", "a minor that never went through main")
	git("tag", "-a", "v1.4.0", "-m", "v1.4.0")
	git("commit", "--quiet", "--allow-empty", "-m", "its fix")
	other := git("rev-parse", "HEAD")
	git("push", "--quiet", "origin", "release/1.4", "v1.4.0")

	// An unrelated history named like the line: release/1.6 shares nothing with v1.6.0.
	git("checkout", "--quiet", "main")
	git("tag", "-a", "v1.6.0", "-m", "v1.6.0")
	git("push", "--quiet", "origin", "v1.6.0")
	git("checkout", "--quiet", "--orphan", "release/1.6")
	git("rm", "--quiet", "-rf", ".")
	git("commit", "--quiet", "--allow-empty", "-m", "an unrelated root")
	orphan := git("rev-parse", "HEAD")
	git("push", "--quiet", "origin", "release/1.6")
	git("checkout", "--quiet", "main")

	for name, tc := range map[string]struct{ sha, tag string }{
		"a patch of another line":                              {fix, "v1.3.1"},
		"the line's own first release (Z of 0)":                {fix, "v1.2.0"},
		"a commit on a branch that is not the line's":          {scratch, "v1.2.1"},
		"a tag named like the remote-tracking branch":          {impostor, "v1.2.1"},
		"a line whose vX.Y.0 is not on main":                   {other, "v1.4.1"},
		"an unrelated history named like the line":             {orphan, "v1.6.1"},
		"the app's mobile tag":                                 {fix, "mobile-v1.2.1"},
		"a line with no maintenance branch at all":             {fix, "v1.8.1"},
		"a patch tag on a commit on neither main nor a branch": {strings.Repeat("0", 40), "v1.2.1"},
	} {
		res := runGate(t, r, g, tc.sha, tc.tag)
		if res.err == nil {
			t.Errorf("%s: the gate accepted %s at %s", name, tc.tag, tc.sha)
			continue
		}
		if !strings.Contains(res.out, "is not on main") {
			t.Errorf("%s: refused for the wrong reason:\n%s", name, res.out)
		}
	}

	// v1.6.0 now exists, so line 1.2 is no longer the newest: the same commit that
	// was accepted above is refused, by its own message, because a patch of an
	// older line would be published as the latest release.
	backport := runGate(t, r, g, fix, "v1.2.1")
	if backport.err == nil {
		t.Fatalf("the gate accepted a patch of the older line v1.2 while v1.6.0 exists:\n%s", backport.out)
	}
	if !strings.Contains(backport.out, "a newer line exists (v1.6.0)") {
		t.Errorf("the backport was refused for the wrong reason:\n%s", backport.out)
	}
}

// ---- CS7b: a release dispatches its own full run -------------------------
//
// CI starts on no commit by itself (the owner's ruling of 2026-11-07: dispatch
// by hand, in batches), and the gate reads the checks of the full `ci` run on a
// commit and of nothing else, so a release that waited for CI on its own merge
// commit would wait out --timeout-minutes for checks that never appear.
// scripts/release.sh therefore puts that run on the commit it is about to tag
// (R12), and these tests read what it dispatched: the ordinary path on the merge
// commit, --hotfix on the maintenance branch, with only the inputs the ci.yml the
// dispatch runs declares -- main's own file for the ordinary path, the branch's
// for --hotfix. An input a workflow does not declare is a 422 from GitHub, which
// would stop the release before the run it waits for could even start.

// workflowRunLines is the fake gh's record of the `gh workflow run` calls a run
// made, one per line, in the order it made them, and nothing when it made none.
func workflowRunLines(t *testing.T, g *ghState) []string {
	t.Helper()
	body := strings.TrimSpace(g.read(t, "workflow_run"))
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// mainTip is origin/main's commit in the fixture: the merge commit's parent
// while the release is being prepared, which is what the pre-merge gate reads.
func mainTip(t *testing.T, r *releaseRepo) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, r.bare, "rev-parse", "main"))
}

// mainFullyGreen is a ci_runs line giving origin/main's commit one full run of
// its own, green by the fixture's checks. A test that writes ci_runs at all
// owns the fake's whole answer for every commit (scripts/testdata/fake-gh.sh's
// header), and this is the run the release's pre-merge gate reads on main --
// without it the gate would wait out its timeout for a run nobody dispatched.
func mainFullyGreen(t *testing.T, r *releaseRepo) string {
	t.Helper()
	sha := mainTip(t, r)
	return sha + "\tci (full)\t" + sha
}

// ciYAML renders a .github/workflows/ci.yml whose `on: workflow_dispatch:` block
// declares the given inputs, at the shape scripts/release.sh reads the names out
// of. Only the names matter to it: a dispatch may pass an input only when the
// workflow it runs declares it, because GitHub refuses an unknown one with a
// 422.
func ciYAML(inputs string) string {
	body := "name: ci\non:\n  workflow_dispatch:\n"
	if inputs != "" {
		body += "    inputs:\n" + inputs
	}
	return body + "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"
}

// ciFullAndLiveScripts is a ci.yml declaring both inputs: the selector's `full`
// (CS6) and #3301's `live_scripts`, whose `auto` -- its default -- could skip the
// live-script runners, which is not a gate a release can be cut on. A ref carries
// both only between CS6's copy and the removal of `live_scripts` (R9), and it is
// what makes a dispatch pass each flag its own file declares, and no other.
var ciFullAndLiveScripts = ciYAML(`      full:
        description: run every leg
        type: boolean
        default: false
      live_scripts:
        description: the live-script runners
        type: choice
        options: [auto, always]
        default: auto
`)

// ciLiveScriptsOnly is ci.yml's shape on main today, the one #3301 left: a
// maintenance branch cut from a tag between #3301 and CS6 carries it, and its
// dispatch may pass `live_scripts` and no `full`.
var ciLiveScriptsOnly = ciYAML(`      live_scripts:
        description: the live-script runners
        type: choice
        options: [auto, always]
        default: auto
`)

// ciNoInputs is the shape older than both: a bare workflow_dispatch, which a
// dispatch may pass nothing to.
var ciNoInputs = ciYAML("")

// releaseMainWithCI is newReleaseRepo whose origin/main carries the given
// ci.yml. A dispatch with no --ref runs the workflow file on the repository's
// default branch, so main's own file is what an ordinary release's dispatch runs
// and the only thing that says which inputs it may pass. The plain fixture has
// no .github/ at all, which is why a test about that dispatch builds it here.
func releaseMainWithCI(t *testing.T, ci string) *releaseRepo {
	t.Helper()
	r := newReleaseRepo(t)
	commitCIYAML(t, r.local, ci)
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main")
	return r
}

// commitCIYAML writes ci.yml into dir's .github/workflows and commits it there,
// the way a repository carries the workflow a release dispatches.
func commitCIYAML(t *testing.T, dir, ci string) {
	t.Helper()
	workflows := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflows, "ci.yml"), []byte(ci), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "ci: the workflow a release dispatches")
}

// hotfixLineWithCI is the hotfix fixture -- a line 0.12 that shipped from main,
// with the 0.12.1 plugin bump on main -- whose v0.12.0 tree carries the given
// ci.yml. A maintenance branch is cut from the line's last tag, so the file a
// dispatch on release/0.12 runs, and the inputs it may pass, are the tag's.
func hotfixLineWithCI(t *testing.T, ci string) *hotfixRepo {
	t.Helper()
	r := newReleaseRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }
	commitCIYAML(t, r.local, ci)
	git("tag", "-a", "v0.12.0", "-m", "Fugaro v0.12.0")
	git("push", "--quiet", "origin", "refs/tags/v0.12.0")
	first := git("rev-parse", "v0.12.0^{commit}")
	bump := exec.Command("bash", filepath.Join(r.local, "scripts", "bump-plugin-version.sh"), "0.12.1")
	bump.Dir = r.local
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bumping the fixture plugin to 0.12.1: %v\n%s", err, out)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "plugin: 0.12.1")
	git("push", "--quiet", "origin", "main")
	return &hotfixRepo{releaseRepo: r, first: first, bump: git("rev-parse", "HEAD")}
}

// TestReleaseDispatchesAFullRunForTheMergeCommit: the ordinary path's step 4
// puts the full `ci` run its gate reads on the merge commit itself, because the
// commit has none -- nothing dispatches CI for a merge -- and then waits for the
// run it started, which is the one on the commit being tagged. main's ci.yml
// there is the selector's shape (CS6), so the dispatch asks for the full variant
// of the run the gate reads; without that input it is a bare `ci` run, which the
// gate reads as full too (R1).
func TestReleaseDispatchesAFullRunForTheMergeCommit(t *testing.T) {
	r := releaseMainWithCI(t, ciFullAndLiveScripts)
	g := newGHState(t, r, greenChecks())
	// The merge commit has runs -- a selected one -- and no full one, which is
	// what a merge commit looks like. Its sha is the fake's `@merge`: the squash
	// merge a release makes does not exist when a test writes this file.
	ciRuns(t, g, mainFullyGreen(t, r), "@merge\tci (selected)\t@merge-selected")

	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireSuccess(t)

	if !strings.Contains(res.out, "dispatched a full ci run on main") {
		t.Errorf("the release did not say it dispatched the run its gate reads:\n%s", res.out)
	}
	if dispatched := workflowRunLines(t, g); len(dispatched) != 1 || dispatched[0] != "workflow run ci -f full=true -f live_scripts=always" {
		t.Errorf("want exactly one `workflow run ci -f full=true -f live_scripts=always`, got %q:\n%s", dispatched, g.log(t))
	}
	merge := strings.TrimSpace(g.read(t, "pr_merge_sha"))
	if tagged := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}"); tagged != merge {
		t.Errorf("the tag is on %s, want the merge commit the run was dispatched for (%s)", tagged, merge)
	}
}

// TestReleaseReadsAnExistingFullRunInsteadOfDispatch: a merge commit that
// already has a full run -- a person's dispatch, or a re-run of this release --
// gets no second one: the gate reads the newest full run, so a second would only
// cost a runner. A bare `ci` counts as full too, which is the title a ci.yml
// without run-name gives every run (the file main carries today).
func TestReleaseReadsAnExistingFullRunInsteadOfDispatch(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, mainFullyGreen(t, r), "@merge\tci\t@merge")

	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireSuccess(t)

	merge := strings.TrimSpace(g.read(t, "pr_merge_sha"))
	if !strings.Contains(res.out, "ci run #2 on "+merge+" is a full one") {
		t.Errorf("the release did not say it read the run the commit already had:\n%s", res.out)
	}
	if dispatched := workflowRunLines(t, g); len(dispatched) != 0 {
		t.Errorf("the release dispatched a run the commit already had: %q", dispatched)
	}
}

// TestReleaseOnAMainWithoutTheFullInputPassesLiveScripts: main's ci.yml between
// #3301 and the selector (CS6) declares live_scripts and no `full`. GitHub refuses
// a dispatch that passes an input its workflow does not declare (422), so
// `-f full=true` there would stop every ordinary release at step 4, before the run
// it waits for could start -- and `live_scripts` must still be passed, because
// `auto`, that input's default, may skip the live-script runners, and a gate run
// that skipped them is not one a tag can be cut on (R9). The run is titled `ci`,
// which the gate reads as full (R1).
func TestReleaseOnAMainWithoutTheFullInputPassesLiveScripts(t *testing.T) {
	r := releaseMainWithCI(t, ciLiveScriptsOnly)
	g := newGHState(t, r, greenChecks())
	ciRuns(t, g, mainFullyGreen(t, r), "@merge\tci (selected)\t@merge-selected")

	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireSuccess(t)

	if dispatched := workflowRunLines(t, g); len(dispatched) != 1 || dispatched[0] != "workflow run ci -f live_scripts=always" {
		t.Errorf("want exactly one `workflow run ci -f live_scripts=always` on a main that declares no full input, got %q:\n%s", dispatched, g.log(t))
	}
	merge := strings.TrimSpace(g.read(t, "pr_merge_sha"))
	if tagged := testutil.Git(t, r.bare, "rev-parse", "v0.12.0^{commit}"); tagged != merge {
		t.Errorf("the tag is on %s, want the merge commit the run was dispatched for (%s)", tagged, merge)
	}
}

// TestReleaseRefusesARunTheDispatchPutOnAnotherCommit: a workflow_dispatch runs
// a workflow at the ref's tip, so when main moves on between the merge and the
// dispatch, the run the release started is on a commit that is not the one it is
// releasing, and reading it would vouch for a commit nobody released. The
// release refuses instead, names the commit and the one command that would put a
// full run on it, and creates no ref of its own: that command names a branch a
// person makes.
func TestReleaseRefusesARunTheDispatchPutOnAnotherCommit(t *testing.T) {
	r := releaseMainWithCI(t, ciFullAndLiveScripts)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_ADVANCE_AFTER_MERGE=1")
	ciRuns(t, g, mainFullyGreen(t, r), "@merge\tci (selected)\t@merge-selected")

	res := runRelease(t, r, g, "y\n", "0.12.0")

	merge := strings.TrimSpace(g.read(t, "pr_merge_sha"))
	res.requireFailureContaining(t, "gh workflow run ci --ref <a branch that points at "+merge+"> -f full=true")
	if !strings.Contains(res.out, "main had moved past the release's merge commit") {
		t.Errorf("the refusal does not say why the run landed elsewhere:\n%s", res.out)
	}
	// The command it prints names a ref a person makes: this run created none of
	// its own.
	heads := strings.Split(strings.TrimSpace(testutil.Git(t, r.bare, "for-each-ref", "--format=%(refname:short)", "refs/heads")), "\n")
	sort.Strings(heads)
	if strings.Join(heads, ",") != "main,release/v0.12.0" {
		t.Errorf("the release left branches %q, want only main and its release branch", heads)
	}
	requireNoTag(t, r, res)
}

// TestHotfixDispatchesAFullRun: --hotfix dispatches the run its own gate reads
// on the maintenance branch, and what it may pass is what the branch's ci.yml
// declares -- the workflow a dispatch runs is the file on the ref it names, and
// a maintenance branch is cut from the line's last tag, so its ci.yml may be
// older than main's. GitHub refuses an input a workflow does not declare (422),
// so each is passed only when the branch declares it.
func TestHotfixDispatchesAFullRun(t *testing.T) {
	for name, tc := range map[string]struct{ ci, want string }{
		"a branch that declares both inputs": {
			ci:   ciFullAndLiveScripts,
			want: "workflow run ci --ref release/0.12 -f full=true -f live_scripts=always",
		},
		"a branch from before the full input (main's shape today)": {
			ci:   ciLiveScriptsOnly,
			want: "workflow run ci --ref release/0.12 -f live_scripts=always",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := hotfixLineWithCI(t, tc.ci)
			g := r.state(t)

			// The first run builds and pushes the branch and then stops: the
			// release's own text is not on it yet. A release that has not got
			// there yet dispatches nothing.
			first := runHotfix(t, r, g, "0.12.1")
			first.requireFailureContaining(t, "docs/releases/v0.12.1.md")
			if dispatched := workflowRunLines(t, g); len(dispatched) != 0 {
				t.Fatalf("the run that stopped for the release's own text dispatched CI: %q", dispatched)
			}
			commitHotfixText(t, r, "0.12.1")

			second := runHotfix(t, r, g, "0.12.1")
			second.requireSuccess(t)
			if dispatched := workflowRunLines(t, g); len(dispatched) != 1 || dispatched[0] != tc.want {
				t.Errorf("want exactly one %q, got %q:\n%s", tc.want, dispatched, second.out)
			}
			tip := testutil.Git(t, r.bare, "rev-parse", "release/0.12")
			if tagged := r.git(t, "rev-parse", "refs/tags/v0.12.1^{commit}"); tagged != tip {
				t.Errorf("tag v0.12.1 (%s) is not on the branch tip (%s)", tagged, tip)
			}
		})
	}
}

// TestHotfixOnAnOlderBranchPassesNoInput: a line whose tag predates both inputs
// is dispatched exactly as before -- no -f at all -- because its ci.yml declares
// neither and a dispatch passing one is refused by GitHub with a 422. The run it
// starts is titled a bare `ci`, which is what a ci.yml without run-name gives
// every run, and that is the title the gate reads as a full run.
func TestHotfixOnAnOlderBranchPassesNoInput(t *testing.T) {
	r := hotfixLineWithCI(t, ciNoInputs)
	g := r.state(t)

	first := runHotfix(t, r, g, "0.12.1")
	first.requireFailureContaining(t, "docs/releases/v0.12.1.md")
	commitHotfixText(t, r, "0.12.1")

	second := runHotfix(t, r, g, "0.12.1")
	second.requireSuccess(t)
	if dispatched := workflowRunLines(t, g); len(dispatched) != 1 || dispatched[0] != "workflow run ci --ref release/0.12" {
		t.Errorf("want exactly one `workflow run ci --ref release/0.12` and no input, got %q:\n%s", dispatched, second.out)
	}
}
