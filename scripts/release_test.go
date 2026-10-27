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

// releaseRepo is a bare "origin" seeded with this checkout's scripts/ and
// plugin/ trees, and a clone of it (on main) to run release.sh from.
type releaseRepo struct {
	bare  string
	local string
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
	for _, name := range []string{"scripts", "plugin"} {
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
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "seed")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")

	local := filepath.Join(root, "work")
	testutil.Git(t, root, "clone", "--quiet", bare, local)
	return &releaseRepo{bare: bare, local: local}
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

// addReleaseNotes writes the Highlights and the end-user summary a release from
// 0.4.0 on needs, commits them on main and pushes them, so a test can release a
// version of its own.
func addReleaseNotes(t *testing.T, r *releaseRepo, version string) {
	t.Helper()
	writeHighlights(t, r.local, version, "- Something a user sees.\n")
	writeSummaries(t, r.local, map[string]string{version: "Something a user sees."})
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
// fake gh (and nothing else: git, bash, sed, awk, sort, date stay real).
func fakeBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(), "scripts", "testdata", "fake-gh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
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
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", append([]string{filepath.Join(r.local, "scripts", "release.sh")}, args...)...)
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1", "RELEASE_RETRY_SECONDS=0")
	// The helper first and the test's own environment after it, so a test can
	// unset RELEASE_SH_STREAMS_CMD (an empty value is the default `go run`).
	cmd.Env = append(cmd.Env, releaseEnv(t)...)
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
}

// greenChecks is every check scripts/release-gate.sh requires, green, keyed by
// the check name's stem: the three CLI jobs and `console`, which stands for
// both check runs the console workflow's matrix reports (Task 4.6 -- the fake
// answers for both legs from FAKE_GH_CHECK_CONSOLE, so the legs cannot be set
// apart here). That claim is not left to the reader:
// TestFakeGHAnswersEveryCheckTheGateRequires reads the gate's own `names` array
// and fails if a name it holds has no stem here or no arm in the fake.
func greenChecks() map[string]string {
	return map[string]string{"test": "success", "terraform": "success", "rules": "success", "console": "success"}
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
// line, and pins it against the console workflow's matrix).
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
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "0.12.0")
	cmd.Dir = r.local
	// A PATH with no gh at all (just enough for git, bash's builtins, etc).
	realPath := os.Getenv("PATH")
	cmd.Env = append(os.Environ(), "PATH="+realPath)
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Env = append(cmd.Env, releaseEnv(t)...)
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
	dir := t.TempDir()
	link := func(src string) {
		name := filepath.Base(src)
		if name == "go" || name == "gofmt" {
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
// behind. The script is the lowest-numbered process whose command line carries
// its path: the subshells it forks carry the same line.
func writeSignalStub(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin-test-signal-stub.sh")
	script := `#!/usr/bin/env bash
set -eu
echo "tree=$PWD" >> "$STUB_LOG"
pid=$(pgrep -f -- "$STUB_SCRIPT" | sort -n | head -1)
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
	for _, extra := range [][]string{nil, {"--yes"}} {
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
	r2 := &releaseRepo{bare: r.bare, local: local2}

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
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "0.12.0", "--yes")
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Env = append(cmd.Env, releaseEnv(t)...)
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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

// TestPostMergeConsoleCheckFailsFast: the console is in the gate's required
// set (Task 4.6), so a red console leg on the merge commit stops a release at
// once, exactly as a red `terraform` does. The gate names the first console
// name it requires, `console (ubuntu-latest)`, when both legs are red.
func TestPostMergeConsoleCheckFailsFast(t *testing.T) {
	r := newReleaseRepo(t)
	// A long timeout: a conclusive failure must not be waited out.
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_POST_CONSOLE=failed")
	start := time.Now()
	res := runRelease(t, r, g, "y\n", "0.12.0")
	res.requireFailureContaining(t, "required check 'console (ubuntu-latest)' is failed")
	if time.Since(start) > 30*time.Second {
		t.Errorf("a red check was waited out")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
		// The fallback's set is the target's set, console legs included: a head
		// that is green on the CLI jobs and red on the console vouches for
		// nothing (Task 4.6).
		{"head console check failed", []string{"FAKE_GH_HEAD_CONSOLE=failed"}},
		{"head console check missing", []string{"FAKE_GH_HEAD_CONSOLE=missing"}},
		{"PR not merged", []string{"FAKE_GH_PULL_UNMERGED=1"}},
		{"merge commit is another sha", []string{"FAKE_GH_PULL_MERGE_SHA=0123456789abcdef0123456789abcdef01234567"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := identicalTreeEnv(newGHState(t, r, greenChecks()), c.extra...)
			res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
			res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
	res.requireFailureContaining(t, "required check 'rules' is failed")
	if strings.Contains(res.out, "identical tree") {
		t.Errorf("the head's checks must not override a failure:\n%s", res.out)
	}
	requireNoTag(t, r, res)
}

// TestHeadNotMergeCommit: something else landed on main after the release
// merge; the script must refuse rather than tag a different commit.
func TestHeadNotMergeCommit(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_ADVANCE_AFTER_MERGE=1")
	res := runRelease(t, r, g, "y\n", "0.12.0", "--yes")
	res.requireFailureContaining(t, "is not the release merge commit")
	requireNoTag(t, r, res)
}

func TestBumpCheckFailsAfterMerge(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_CORRUPT_MERGE=1")
	res := runRelease(t, r, g, "y\n", "0.12.0", "--yes")
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
		{"swallows the next flag", []string{"--timeout-minutes", "--yes", "0.12.0"}},
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
// the fake gh on PATH and g's checks: the gate reads the tag's check-runs, so
// its answer depends on the same state a release.sh run would see.
func runGate(t *testing.T, r *releaseRepo, g *ghState, sha, tag string) runResult {
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
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
	res.requireFailureContaining(t, "pushing release/v0.12.0 failed")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	removeHook(t, r)
	runRelease(t, r, g, "", "0.12.0", "--yes").requireSuccess(t)
	if out := testutil.Git(t, r.bare, "tag", "--list", "v0.12.0"); out == "" {
		t.Errorf("the re-run did not finish the release")
	}
}

func TestFailedPRCreateLeavesMainAndResumes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g.with("FAKE_GH_PR_CREATE_FAIL=1"), "", "0.12.0", "--yes")
	res.requireFailureContaining(t, "'gh pr create' failed")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	res = runRelease(t, r, g, "", "0.12.0", "--yes")
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
			cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "0.12.0", "--yes")
			cmd.Dir = r.local
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "resuming pushed branch release/v0.12.0") {
		t.Errorf("expected the pushed branch to be reused:\n%s", res.out)
	}
	if n := testutil.Git(t, r.bare, "rev-list", "--count", initial+"..refs/heads/release/v0.12.0"); n != "2" {
		t.Errorf("want the pushed bump commit and the catalogue and docs commit on the branch, got %s", n)
	}
	// The catalogue and the two doc lines that name the released version
	// (releasedVersionDocs), sorted as git lists them.
	want := "docs/design/project-named-oauth-tokens.md\ndocs/project-claude-accounts.md\ndocs/release-streams.yaml"
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

	res := runRelease(t, r, g, "", "0.11.2", "--yes")

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

	res := runRelease(t, r, g, "", "0.11.2", "--yes")

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
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
	res.requireSuccess(t)
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("the documented recovery (branch deleted) must open a fresh PR:\n%s", g.log(t))
	}
}

func TestAutoMergeUnavailable(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_AUTOMERGE_FAIL=1")
	res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
		res := runRelease(t, r, g, "", "0.12.0", "--yes")
		res.requireSuccess(t)
		if !strings.Contains(res.out, "was cancelled") {
			t.Errorf("expected a retry notice:\n%s", res.out)
		}
	})
	t.Run("persistent", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		g.write(t, "pr_check_fail", "rules CANCELLED")
		res := runRelease(t, r, g, "", "0.12.0", "--yes")
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
			// the later precondition instead.
			writeSummaries(t, r.local, map[string]string{c.version: "Something a user sees."})
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
		path:   "docs/design/project-named-oauth-tokens.md",
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
