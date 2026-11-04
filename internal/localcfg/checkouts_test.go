package localcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkoutsEnv points XDG_CONFIG_HOME at a directory of the test's own, and
// returns the getenv the package's readers take: no test here reads or
// writes the developer's own home directory.
func checkoutsEnv(t *testing.T) func(string) string {
	t.Helper()
	xdg := t.TempDir()
	return func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
}

func checkoutID(provider, repo string) CheckoutIdentity {
	return CheckoutIdentity{Provider: provider, Repo: repo}
}

// at is a fixed clock, so "the oldest" is a fact of the test and not of how
// fast the machine ran.
func at(min int) time.Time { return time.Date(2026, 10, 20, 12, min, 0, 0, time.UTC) }

// TestRecordCheckoutWritesTheDocument: recording a checkout writes a file in
// a directory of its own under the config home (never into a project config,
// and never beside it), mode 0600 in a 0700 directory, keyed by the
// repository identity. The directory is the file's own because the config home
// belongs to the operator and this is not the writer to chmod it over: that
// directory may well be 0755, and the state file's own directory is created
// 0700.
func TestRecordCheckoutWritesTheDocument(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro", CheckoutsDir, CheckoutsFile); path != want {
		t.Fatalf("CheckoutsPath = %q, want %q", path, want)
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("the checkouts file is mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("the checkouts directory is mode %v, want 0700", di.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "projects", CheckoutsFile)); err == nil {
		t.Error("the checkouts file was written inside projects/, among the project configs")
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := c.Projects["aurora"]["github/acme/app"]
	if !ok {
		t.Fatalf("aurora's checkouts = %+v, want the identity key github/acme/app", c.Projects)
	}
	if r.Provider != "github" || r.Repo != "acme/app" || len(r.Paths) != 1 || r.Paths[0].Path != "/src/app" {
		t.Fatalf("the recorded repository = %+v, want the identity and the path", r)
	}
}

// TestRecordCheckoutDoesNotTouchAProjectConfig: the state is a file of its
// own. A project config beside it is byte-for-byte what it was — the reason
// the state is not a key in <project>.yaml, which init rewrites with a diff
// and a .bak and every reader parses strictly.
func TestRecordCheckoutDoesNotTouchAProjectConfig(t *testing.T) {
	getenv := checkoutsEnv(t)
	path := filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro", "projects", "aurora.yaml")
	writeFile(t, path, projectYAML("aurora", "aurora-gcp-1"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the project config changed:\n%s\nwant\n%s", after, before)
	}
	if _, _, err := LoadProject(getenv, "aurora"); err != nil {
		t.Errorf("the project config no longer loads: %v", err)
	}
}

// TestRecordCheckoutKeepsAnAlreadyKnownPath: a command in a checkout this
// machine already knows writes nothing at all (the file's own mtime is the
// proof), so the steady state of running fugaro in a checkout is a read.
//
// The path is a directory that is there, which is the only kind a command
// records — it records the toplevel it is running in — and the one the pruning
// leaves alone. A path that is gone is stamped as gone on the first write that
// sees it, and that stamp is a change like any other; the roots are the test's
// own list of the machine's temporary directories, which is where t.TempDir()
// lives.
func TestRecordCheckoutKeepsAnAlreadyKnownPath(t *testing.T) {
	getenv := checkoutsEnv(t)
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Join(t.TempDir(), "not-a-temp-root")}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), dir, at(0), roots); err != nil {
		t.Fatal(err)
	}
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), dir, at(5), roots); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Error("recording a path that is already recorded rewrote the file")
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if r := c.Projects["aurora"]["github/acme/app"]; len(r.Paths) != 1 || !r.Paths[0].RecordedAt.Equal(at(0)) {
		t.Errorf("the repository = %+v, want the one path, recorded once", r)
	}
}

// TestRecordCheckoutBoundsThePaths: at most MaxCheckoutPaths paths per
// repository, and the oldest is the one dropped.
func TestRecordCheckoutBoundsThePaths(t *testing.T) {
	getenv := checkoutsEnv(t)
	for i := range MaxCheckoutPaths + 3 {
		if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), pathOf(i), at(i)); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Projects["aurora"]["github/acme/app"].Paths
	if len(got) != MaxCheckoutPaths {
		t.Fatalf("paths = %d, want the %d-path bound: %+v", len(got), MaxCheckoutPaths, got)
	}
	for i, p := range got {
		if want := pathOf(i + 3); p.Path != want {
			t.Errorf("path %d = %q, want %q: the oldest paths are the ones dropped", i, p.Path, want)
		}
	}
}

// TestRecordCheckoutBoundsTheRepositories: at most MaxCheckoutRepos
// repositories per project, and the repository recorded longest ago is the
// one dropped.
func TestRecordCheckoutBoundsTheRepositories(t *testing.T) {
	getenv := checkoutsEnv(t)
	for i := range MaxCheckoutRepos + 1 {
		if err := RecordCheckout(getenv, "aurora", checkoutID("github", repoOf(i)), pathOf(i), at(i)); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	proj := c.Projects["aurora"]
	if len(proj) != MaxCheckoutRepos {
		t.Fatalf("repositories = %d, want the %d-repository bound: %+v", len(proj), MaxCheckoutRepos, proj)
	}
	if _, ok := proj[checkoutID("github", repoOf(0)).Key()]; ok {
		t.Errorf("the repository recorded first is still there: %+v", proj)
	}
	if _, ok := proj[checkoutID("github", repoOf(MaxCheckoutRepos)).Key()]; !ok {
		t.Errorf("the repository recorded last was dropped: %+v", proj)
	}
}

// TestRecordCheckoutKeepsTwoProjectsApart: the document is keyed by project
// first, so one project's checkouts never answer for another's.
func TestRecordCheckoutKeepsTwoProjectsApart(t *testing.T) {
	getenv := checkoutsEnv(t)
	for _, p := range []string{"aurora", "borealis"} {
		if err := RecordCheckout(getenv, p, checkoutID("github", "acme/app"), "/src/"+p, at(0)); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ProjectCheckouts("aurora"); len(got) != 1 || got[0].Paths[0].Path != "/src/aurora" {
		t.Errorf("aurora's checkouts = %+v, want its own path", got)
	}
	if got := c.ProjectCheckouts("borealis"); len(got) != 1 || got[0].Paths[0].Path != "/src/borealis" {
		t.Errorf("borealis's checkouts = %+v, want its own path", got)
	}
}

// TestRecordCheckoutRefusesWhatIsNotAnIdentity: a project name, a provider
// this build does not carry, an owner/name that is not one, and a relative
// path are all refused, and nothing is written.
func TestRecordCheckoutRefusesWhatIsNotAnIdentity(t *testing.T) {
	for name, tc := range map[string]struct {
		project string
		id      CheckoutIdentity
		path    string
	}{
		"not a project name": {"Aurora", checkoutID("github", "acme/app"), "/src/app"},
		"not a provider":     {"aurora", checkoutID("gitlab", "acme/app"), "/src/app"},
		"no provider":        {"aurora", checkoutID("", "acme/app"), "/src/app"},
		"not owner/name":     {"aurora", checkoutID("github", "app"), "/src/app"},
		"empty repo":         {"aurora", checkoutID("github", ""), "/src/app"},
		"relative path":      {"aurora", checkoutID("github", "acme/app"), "src/app"},
		"empty path":         {"aurora", checkoutID("github", "acme/app"), ""},
		"over-long path":     {"aurora", checkoutID("github", "acme/app"), "/" + strings.Repeat("x", maxCheckoutPathLen)},
	} {
		t.Run(name, func(t *testing.T) {
			getenv := checkoutsEnv(t)
			if err := RecordCheckout(getenv, tc.project, tc.id, tc.path, at(0)); err == nil {
				t.Fatal("recorded, want a refusal")
			}
			path, err := CheckoutsPath(getenv)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("a refused record wrote %s (err = %v)", path, err)
			}
		})
	}
}

// TestRecordCheckoutSkipsTempDir: a checkout under the machine's temporary
// directory is not remembered at all. The scratchpad worktrees a coding agent
// makes live there, and a remembered path is what the console offers as a
// working directory, so the row is not state this machine wants.
//
// The second half is the symlink the rule turns on: the path is judged after
// resolving symlinks, because a temporary directory is reached through one on
// macOS (/tmp is /private/tmp and /var/folders is /private/var/folders), and a
// comparison of the strings alone would remember a path under a temporary
// directory reached the other way. The link is built in the test rather than
// taken from the platform, so the test means the same thing on every one.
func TestRecordCheckoutSkipsTempDir(t *testing.T) {
	getenv := checkoutsEnv(t)
	// The machine's own temporary directory, as the writer reads it: every
	// directory a test can make is inside it.
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), t.TempDir(), at(0)); err != nil {
		t.Fatal(err)
	}
	// And the same rule through a link, which is how macOS reaches its own:
	// the link is one the test makes, and the root it resolves into is the
	// only temporary directory this half names.
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/other"), filepath.Join(link, "app"), at(1), []string{real}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Projects["aurora"]) != 0 {
		t.Errorf("aurora's checkouts = %+v, want none: a temporary directory is never remembered", c.Projects["aurora"])
	}
}

// TestRecordCheckoutSkipsATempRootReachedThroughALink: the rule is judged in
// both spellings of both sides. A temporary directory is reached through a
// link on macOS — os.TempDir() is /var/folders/... and the same directory
// through its own link is /private/var/folders/... — so a root named as the
// link and a path spelled as what it resolves to are the same directory, and
// the writer says so whichever way round the two come.
func TestRecordCheckoutSkipsATempRootReachedThroughALink(t *testing.T) {
	getenv := checkoutsEnv(t)
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// The root as the link, the path as what the link resolves to.
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), filepath.Join(real, "app"), at(0), []string{link}); err != nil {
		t.Fatal(err)
	}
	// And the root as what the link resolves to, the path through the link.
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/other"), filepath.Join(link, "app"), at(1), []string{real}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Projects["aurora"]) != 0 {
		t.Errorf("aurora's checkouts = %+v, want none: the root and the path name the same directory", c.Projects["aurora"])
	}
}

// TestRecordCheckoutSkipsALinkOutOfATempRoot: a path under a temporary
// directory is temporary whatever the link resolves to. An agent's scratchpad
// is a directory in the temporary directory, and the path this machine
// remembers is the one the console is offered as a working directory: judged
// by what the link points at today, a link the agent can re-point after the
// row is written would be offered, and the target it is pointed at then is
// whatever the agent likes.
func TestRecordCheckoutSkipsALinkOutOfATempRoot(t *testing.T) {
	getenv := checkoutsEnv(t)
	root := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "work", "app")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "app")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), link, at(0), []string{root}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Projects["aurora"]) != 0 {
		t.Errorf("aurora's checkouts = %+v, want none: the path is under the temporary directory, whatever it points at", c.Projects["aurora"])
	}
}

// TestRecordCheckoutKeepsNormalCheckout: the rule is about temporary
// directories and nothing else. A checkout that is not under one is recorded
// as it always was, and the roots are the test's own list because every
// directory a test can make is inside the machine's temporary directory.
func TestRecordCheckoutKeepsNormalCheckout(t *testing.T) {
	getenv := checkoutsEnv(t)
	dir := filepath.Join(t.TempDir(), "work", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Join(t.TempDir(), "scratch")}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), dir, at(0), roots); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Projects["aurora"]["github/acme/app"]
	if len(r.Paths) != 1 || r.Paths[0].Path != dir {
		t.Fatalf("aurora's checkouts = %+v, want the one path %q", c.Projects["aurora"], dir)
	}
	if r.Paths[0].MissingSince != nil {
		t.Errorf("the recorded row = %+v, want no missing clock: the path is there", r.Paths[0])
	}
}

// TestPruneDropsTempRows: a row under a temporary directory is dropped by the
// next write, whatever its age and whether or not it is still there — the
// scratchpad a coding agent left behind is a live checkout, and it is exactly
// the row the console would otherwise offer as a working directory. The
// project it emptied goes with it. A row whose path is a link out of the
// temporary directory goes too: the row is what the console is offered, and a
// link can be re-pointed after it is written.
func TestPruneDropsTempRows(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(t.TempDir(), "work", "app")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "work", "linked")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(scratch, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	writeHandWritten(t, path, &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{
		"aurora": {
			"github/acme/app": {Provider: "github", Repo: "acme/app", RecordedAt: at(0),
				Paths: []CheckoutPath{{Path: kept, RecordedAt: at(0)}}},
			"github/acme/scratch": {Provider: "github", Repo: "acme/scratch", RecordedAt: at(1),
				Paths: []CheckoutPath{{Path: scratch, RecordedAt: at(1)}}},
			"github/acme/linked": {Provider: "github", Repo: "acme/linked", RecordedAt: at(1),
				Paths: []CheckoutPath{{Path: linked, RecordedAt: at(1)}}},
		},
		"bramble": {
			"github/acme/other": {Provider: "github", Repo: "acme/other", RecordedAt: at(2),
				Paths: []CheckoutPath{{Path: scratch, RecordedAt: at(2)}}},
		},
	}})
	roots := []string{scratch}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), kept, at(5), roots); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Projects["aurora"]["github/acme/scratch"]; ok {
		t.Errorf("aurora's checkouts = %+v, want no row for the scratchpad", c.Projects["aurora"])
	}
	if _, ok := c.Projects["aurora"]["github/acme/linked"]; ok {
		t.Errorf("aurora's checkouts = %+v, want no row for the link out of the scratchpad", c.Projects["aurora"])
	}
	if _, ok := c.Projects["bramble"]; ok {
		t.Errorf("bramble = %+v, want the project gone: its only row was the scratchpad", c.Projects["bramble"])
	}
	if r := c.Projects["aurora"]["github/acme/app"]; len(r.Paths) != 1 || r.Paths[0].Path != kept {
		t.Errorf("the kept row = %+v, want %q", r, kept)
	}
}

// TestPruneDropsLongMissingRows: a row whose path has been gone for longer
// than the grace is dropped by the next write, and one gone for less than that
// is kept with its clock. A row that is there again has its clock cleared, so
// a directory that comes back is not dropped for the time it was away.
func TestPruneDropsLongMissingRows(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	back := filepath.Join(t.TempDir(), "back")
	if err := os.MkdirAll(back, 0o700); err != nil {
		t.Fatal(err)
	}
	old := at(0).Add(-checkoutMissingGrace - time.Hour)
	recent := at(0).Add(-time.Hour)
	writeHandWritten(t, path, &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{
		"aurora": {
			"github/acme/old": {Provider: "github", Repo: "acme/old", RecordedAt: at(0),
				Paths: []CheckoutPath{{Path: gone + "-old", RecordedAt: at(0), MissingSince: &old}}},
			"github/acme/recent": {Provider: "github", Repo: "acme/recent", RecordedAt: at(0),
				Paths: []CheckoutPath{{Path: gone + "-recent", RecordedAt: at(0), MissingSince: &recent}}},
			"github/acme/back": {Provider: "github", Repo: "acme/back", RecordedAt: at(0),
				Paths: []CheckoutPath{{Path: back, RecordedAt: at(0), MissingSince: &old}}},
		},
	}})
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/new"), filepath.Join(t.TempDir(), "new"), at(5), []string{}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Projects["aurora"]["github/acme/old"]; ok {
		t.Errorf("aurora's checkouts = %+v, want no row gone for longer than the grace", c.Projects["aurora"])
	}
	r, ok := c.Projects["aurora"]["github/acme/recent"]
	if !ok {
		t.Fatalf("aurora's checkouts = %+v, want the row gone for an hour kept", c.Projects["aurora"])
	}
	if r.Paths[0].MissingSince == nil || !r.Paths[0].MissingSince.Equal(recent) {
		t.Errorf("the kept row = %+v, want its clock %v kept", r.Paths[0], recent)
	}
	b, ok := c.Projects["aurora"]["github/acme/back"]
	if !ok {
		t.Fatalf("aurora's checkouts = %+v, want the path that came back kept", c.Projects["aurora"])
	}
	if b.Paths[0].MissingSince != nil {
		t.Errorf("the row that came back = %+v, want no missing clock", b.Paths[0])
	}
}

// TestPruneStampsTheFirstMissingObservation: a document written before the
// clock existed carries rows with no missing_since, and the first write that
// sees such a path gone stamps it with that moment — the clock starts at the
// first observation rather than at a moment nothing recorded — so the row is
// not dropped at once for a time this machine never saw.
func TestPruneStampsTheFirstMissingObservation(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	writeHandWritten(t, path, &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{
		"aurora": {
			"github/acme/app": {Provider: "github", Repo: "acme/app", RecordedAt: at(0),
				Paths: []CheckoutPath{{Path: gone, RecordedAt: at(0)}}},
		},
	}})
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/new"), filepath.Join(t.TempDir(), "new"), at(5), []string{}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := c.Projects["aurora"]["github/acme/app"]
	if !ok {
		t.Fatalf("aurora's checkouts = %+v, want the row kept: it was seen gone for the first time", c.Projects["aurora"])
	}
	if r.Paths[0].MissingSince == nil || !r.Paths[0].MissingSince.Equal(at(5)) {
		t.Errorf("the row = %+v, want it stamped with the moment it was first seen gone", r.Paths[0])
	}
}

// TestLoadCheckoutsMissingFileIsEmpty: a machine that has recorded nothing
// is an empty document, not an error.
func TestLoadCheckoutsMissingFileIsEmpty(t *testing.T) {
	c, err := LoadCheckouts(checkoutsEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != CheckoutsVersion || len(c.Projects) != 0 {
		t.Fatalf("checkouts = %+v, want an empty version %d document", c, CheckoutsVersion)
	}
}

// TestLoadCheckoutsRefusesAHugeFile: the read is bounded, so a state file
// grown past the bound (or replaced by something else entirely) is refused
// rather than read into memory.
func TestLoadCheckoutsRefusesAHugeFile(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	// A document that IS valid JSON, padded past the bound with the
	// whitespace JSON allows: the bound, not the parse, is what has to
	// refuse it.
	body := `{"version": 1, "projects": {}}` + strings.Repeat(" ", maxCheckoutsBytes)
	writeFile(t, path, body)
	if _, err := LoadCheckouts(getenv); err == nil {
		t.Fatal("a file over the bound was read, want a refusal")
	}
}

// TestLoadCheckoutsRefusesAnotherVersionAndIsNotClobbered: a document a
// newer fugaro wrote is refused by this one, and recording never writes over
// it — an older binary must not drop a newer one's state.
func TestLoadCheckoutsRefusesAnotherVersionAndIsNotClobbered(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	const future = `{"version": 2, "projects": {}}` + "\n"
	writeFile(t, path, future)
	if _, err := LoadCheckouts(getenv); err == nil {
		t.Fatal("version 2 was read as this version, want a refusal")
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err == nil {
		t.Fatal("recorded over a version this build does not understand, want a refusal")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != future {
		t.Errorf("the newer document was rewritten:\n%s\nwant\n%s", got, future)
	}
}

// TestLoadCheckoutsRefusesSomethingThatIsNotADocument: a corrupt file is an
// error, not "no checkouts" — a reader that took it for empty would then
// write over it, and a listing would silently lose every path.
func TestLoadCheckoutsRefusesSomethingThatIsNotADocument(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":       "this is not json\n",
		"an array":       "[]\n",
		"no version":     `{"projects": {"aurora": {}}}` + "\n",
		"a directory":    "",
		"a null project": `{"version": 1, "projects": null}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			getenv := checkoutsEnv(t)
			path, err := CheckoutsPath(getenv)
			if err != nil {
				t.Fatal(err)
			}
			if name == "a directory" {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, path, body)
			}
			c, err := LoadCheckouts(getenv)
			if name == "a null project" {
				// A null projects map is an empty one, not a refusal: it is
				// what a document with nothing recorded marshals to.
				if err != nil || len(c.Projects) != 0 {
					t.Fatalf("checkouts = %+v, %v, want an empty document", c, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("read %q as a document, want a refusal", body)
			}
		})
	}
}

// TestRecordCheckoutIsBestEffortWhenItCannotWrite: a state file that cannot
// be written is an error the caller ignores, never a panic and never a
// partial file.
func TestRecordCheckoutIsBestEffortWhenItCannotWrite(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory where the state file goes: the rename over it fails.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err == nil {
		t.Fatal("recorded into a path that is a directory, want an error")
	}
}

// TestProjectCheckoutsIsOrdered: the document's maps are unordered, so a
// listing that ranged over them would print a different table on each read.
func TestProjectCheckoutsIsOrdered(t *testing.T) {
	getenv := checkoutsEnv(t)
	for _, r := range []string{"acme/zeta", "acme/alpha", "bitbucket/acme/mid"} {
		provider, repo := "github", r
		if strings.HasPrefix(r, "bitbucket/") {
			provider, repo = "bitbucket", strings.TrimPrefix(r, "bitbucket/")
		}
		if err := RecordCheckout(getenv, "aurora", checkoutID(provider, repo), "/src/"+repo, at(0)); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range c.ProjectCheckouts("aurora") {
		keys = append(keys, CheckoutIdentity{r.Provider, r.Repo}.Key())
	}
	want := []string{"bitbucket/acme/mid", "github/acme/alpha", "github/acme/zeta"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("identities = %v, want %v (identity order)", keys, want)
	}
}

// TestCheckoutsAreNotInTheSharedConfig: the paths are local state. Shared()
// is what is published to the runs bucket, and no path is in it.
func TestCheckoutsAreNotInTheSharedConfig(t *testing.T) {
	getenv := checkoutsEnv(t)
	writeFile(t, filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro", "projects", "aurora.yaml"), projectYAML("aurora", "aurora-gcp-1"))
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadProject(getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/src/app") || strings.Contains(string(data), "checkout") {
		t.Errorf("the shared config carries a checkout path: %s", data)
	}
}

func pathOf(i int) string { return "/src/app-" + string(rune('a'+i)) }

func repoOf(i int) string { return "acme/app-" + string(rune('a'+i)) }

// TestLoadCheckoutsRefusesAFileThatIsNotRegular is the read's half of the
// file-type rule: the state file is read through config.ReadRegular, which
// inspects the path and refuses a symlink rather than following it — a
// symlink's target is another file on this machine, and the state file is not
// a pointer to one. A FIFO or a device would be refused by the same check
// (and a FIFO would block a plain ReadFile for as long as no one writes to
// it).
//
// Mutation (run, restore): read the file with os.Stat + os.ReadFile, as the
// code did before the review — the symlink is followed and this test fails
// with the target's contents beside it.
func TestLoadCheckoutsRefusesAFileThatIsNotRegular(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	writeFile(t, target, `{"version": 1, "projects": {}}`+"\n")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	if _, err := LoadCheckouts(getenv); err == nil {
		t.Fatal("a symlinked state file was followed, want a refusal")
	}
}

// TestRecordCheckoutReplacesAFileItCannotRead: a state file that has gone
// wrong — a crash left it empty or half-written, or it is not a document at
// all — is pure state, and the writer replaces it rather than leaving the
// recording broken until someone deletes the file by hand. A *newer* version
// is the one refusal that is honoured (TestLoadCheckoutsRefusesAnotherVersionAndIsNotClobbered).
func TestRecordCheckoutReplacesAFileItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"empty":     "",
		"half JSON": `{"version": 1, "proj`,
		"not JSON":  "this is not json\n",
	} {
		t.Run(name, func(t *testing.T) {
			getenv := checkoutsEnv(t)
			path, err := CheckoutsPath(getenv)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, body)
			if _, err := LoadCheckouts(getenv); err == nil {
				t.Fatal("the broken file was read as a document, want a refusal")
			}
			if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err != nil {
				t.Fatalf("recording over a broken state file: %v", err)
			}
			c, err := LoadCheckouts(getenv)
			if err != nil {
				t.Fatalf("the file the writer left is not a document: %v", err)
			}
			if r := c.Projects["aurora"]["github/acme/app"]; len(r.Paths) != 1 || r.Paths[0].Path != "/src/app" {
				t.Errorf("the document = %+v, want the recorded path", c.Projects)
			}
		})
	}
}

// TestBoundedAppliesTheWriteBoundsOnTheRead: the bounds RecordCheckout keeps
// are applied again where the document is read, because the file is one this
// process did not write — a hand-edited file, or one written by a machine
// that has since cloned far more, must not cost a reader more than the writer
// allows. Every examined path costs a stat and about three git executions, so
// an unbounded read is a console that hangs at startup. dropped says how many
// entries the bounds cut, so a reader can say so rather than present a
// trimmed list as the whole one.
//
// The document here is written by hand (the writer would have trimmed it, and
// a document built through RecordCheckout is the test that does not test the
// read), and the entries it keeps are the newest by their own recorded_at.
func TestBoundedAppliesTheWriteBoundsOnTheRead(t *testing.T) {
	doc := &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{}}
	proj := map[string]CheckoutRepo{}
	for i := range MaxCheckoutRepos + 4 {
		r := CheckoutRepo{Provider: "github", Repo: repoOf(i), RecordedAt: clock(0, i, 0)}
		for j := range MaxCheckoutPaths + 3 {
			r.Paths = append(r.Paths, CheckoutPath{Path: pathOf(i) + "-" + string(rune('a'+j)), RecordedAt: clock(0, i, j)})
		}
		proj[checkoutID("github", repoOf(i)).Key()] = r
	}
	doc.Projects["aurora"] = proj
	doc.Projects["bramble"] = map[string]CheckoutRepo{
		"github/acme/other": {Provider: "github", Repo: "acme/other", RecordedAt: clock(0, 0, 0),
			Paths: []CheckoutPath{{Path: "/src/other", RecordedAt: clock(0, 0, 0)}}},
	}

	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	writeHandWritten(t, path, doc)
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(c.ProjectCheckouts("aurora")); got != MaxCheckoutRepos+4 {
		t.Fatalf("the hand-written document holds %d repositories, want %d (the read is what is under test)", got, MaxCheckoutRepos+4)
	}
	bounded, dropped := c.Bounded([]string{"aurora"})
	// Every entry the read did not examine, counted as the entries they are:
	// the 24 repositories hold 192 rows between them and the bound examines
	// 20 x 5 = 100, so 92 rows went unexamined. Counting a dropped repository
	// as one entry would say 64 — it would hide the 8 rows of each of the 4
	// repositories the repository bound drops.
	wantDropped := (MaxCheckoutRepos+4)*(MaxCheckoutPaths+3) - MaxCheckoutRepos*MaxCheckoutPaths
	if dropped != wantDropped {
		t.Errorf("dropped = %d, want %d: the entries the bounds cut, not the repositories", dropped, wantDropped)
	}
	got := bounded.ProjectCheckouts("aurora")
	if len(got) != MaxCheckoutRepos {
		t.Fatalf("repositories = %d, want the %d bound", len(got), MaxCheckoutRepos)
	}
	for _, r := range got {
		if len(r.Paths) != MaxCheckoutPaths {
			t.Errorf("%s holds %d paths, want the %d bound", r.Repo, len(r.Paths), MaxCheckoutPaths)
		}
	}
	// The newest repository survives and the oldest is what the bound drops.
	kept := map[string]bool{}
	for _, r := range got {
		kept[r.Repo] = true
	}
	if !kept[repoOf(MaxCheckoutRepos+3)] || kept[repoOf(0)] {
		t.Errorf("kept = %v, want the newest %d repositories and not the oldest", kept, MaxCheckoutRepos)
	}
	// The projects a reader did not ask for are not in the result at all: the
	// bound is applied to the projects this listing is about.
	if _, ok := bounded.Projects["bramble"]; ok {
		t.Error("Bounded carried a project it was not asked about")
	}
}

// TestBoundedCapsTheWholeDocument: over MaxCheckoutRows entries across all the
// projects — which a hand-written file can hold, since the file's only other
// bound is its byte size — a read examines the newest MaxCheckoutRows and
// says how many it did not.
func TestBoundedCapsTheWholeDocument(t *testing.T) {
	// One project more than MaxCheckoutRows can hold, each at its full
	// per-project bound: 11 x 20 x 5 = 1100 rows.
	count := MaxCheckoutRows/(MaxCheckoutRepos*MaxCheckoutPaths) + 1
	doc := &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{}}
	names := make([]string, count)
	for p := range count {
		names[p] = "p" + string(rune('a'+p))
		proj := map[string]CheckoutRepo{}
		for i := range MaxCheckoutRepos {
			r := CheckoutRepo{Provider: "github", Repo: repoOf(i), RecordedAt: clock(p, i, 0)}
			for j := range MaxCheckoutPaths {
				r.Paths = append(r.Paths, CheckoutPath{Path: "/src/" + names[p] + "-" + repoOf(i), RecordedAt: clock(p, i, j)})
			}
			proj[checkoutID("github", repoOf(i)).Key()] = r
		}
		doc.Projects[names[p]] = proj
	}

	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	writeHandWritten(t, path, doc)
	c, err := LoadCheckouts(getenv)
	if err != nil {
		t.Fatal(err)
	}
	bounded, dropped := c.Bounded(names)
	if want := count*MaxCheckoutRepos*MaxCheckoutPaths - MaxCheckoutRows; dropped != want {
		t.Errorf("dropped = %d, want %d (the rows over the %d-row bound)", dropped, want, MaxCheckoutRows)
	}
	rows := 0
	for _, name := range names {
		for _, r := range bounded.ProjectCheckouts(name) {
			rows += len(r.Paths)
		}
	}
	if rows != MaxCheckoutRows {
		t.Errorf("rows = %d, want the %d-row bound", rows, MaxCheckoutRows)
	}
	// The newest rows are the ones kept: the last project's stamps are the
	// latest, so the bound must not cut its rows.
	if got := len(bounded.ProjectCheckouts(names[count-1])); got == 0 {
		t.Error("the newest project's rows were dropped, want the newest rows kept")
	}
}

// clock is a distinct stamp per (project, repository, path), so "newest" is a
// fact of the test rather than of how fast the machine ran.
func clock(p, i, j int) time.Time {
	return time.Date(2026, 10, 20, 12, p, i, j, time.UTC)
}

// writeHandWritten writes a document to path the way a hand-edited state file
// arrives: as JSON, without going through RecordCheckout (whose own bounds are
// the thing under test here).
func writeHandWritten(t *testing.T, path string, doc *Checkouts) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
}

// TestConfigEnrollsPicksTheSameEntryEveryTime: repos: is keyed by the
// operator's own spelling, and two keys can fold together ("acme/app" and
// "Acme/App"), so the fold alone names no entry. The exact spelling is read
// first — configRepo's rule — and the fold is then resolved over the sorted
// keys, so the same files answer the same thing on every run rather than
// whichever entry the map's iteration happens to reach first. A gate that
// answers differently on two reads of unchanged files is a validity verdict
// that flips: an operator who sees "not enrolled" and runs the same command
// again can get "valid".
//
// Mutation (run, restore): drop the exact-spelling lookup and the first half
// fails, because "Acme/App" sorts before "acme/app" and answers for it.
// Iterate the map instead of the sorted keys and the second half fails within
// a few runs (`go test -count=20`): the answer follows the map's order.
func TestConfigEnrollsPicksTheSameEntryEveryTime(t *testing.T) {
	// Two spellings of one name, naming different providers: the entry the
	// operator wrote the way the repository is written is the one that
	// answers.
	cfg := &Config{Repos: map[string]Repo{
		"acme/app": {Provider: "github"},
		"Acme/App": {Provider: "bitbucket"},
	}}
	if !cfg.Enrolls("github", "acme/app") {
		t.Error("Enrolls(github, acme/app) = false, want the entry spelled as the repository is")
	}
	if cfg.Enrolls("bitbucket", "acme/app") {
		t.Error("Enrolls(bitbucket, acme/app) = true, want the exact spelling to answer, not the other entry")
	}
	// No exact spelling: the fold is resolved over the sorted keys, so the
	// answer does not change between reads.
	folded := &Config{Repos: map[string]Repo{
		"Acme/App": {Provider: "github"},
		"ACME/APP": {Provider: "bitbucket"},
	}}
	for i := range 8 {
		if !folded.Enrolls("bitbucket", "acme/app") || folded.Enrolls("github", "acme/app") {
			t.Fatalf("read %d: the folded entries answered differently (sorted keys: ACME/APP is the one that answers)", i)
		}
	}
}

// TestConfigRepoForPicksTheSameEntryEveryTime: RepoFor is the one lookup
// repos.<r> is resolved through — fugaro run's own localRepo, and the
// base_branch column of `fugaro config list --json` — so what it answers has
// to be the same on every read of unchanged files and the same in both
// readers. Two keys can name one repository and differ by spelling alone
// ("acme/app" and "acme/app.git", or two spellings the ASCII fold merges), so
// the canonical form is compared over the sorted keys: no map iteration order
// and no exact-spelling shortcut decides which entry answers.
//
// Mutation (run, restore): drop the sort (iterate the map) and the second half
// fails within a few runs (`go test -count=20`), because the answer follows the
// iteration order; compare the keys as written instead of through
// task.CanonicalRepo and the first half fails on the trailing ".git".
func TestConfigRepoForPicksTheSameEntryEveryTime(t *testing.T) {
	// The entry the launch resolves: the key is the repository with the
	// ".git" an operator can leave in a hand-edited config, and the branch is
	// what a run is cut from.
	dotGit := &Config{Repos: map[string]Repo{"acme/app.git": {BaseBranch: "dev"}}}
	if r, ok := dotGit.RepoFor("acme/app"); !ok || r.BaseBranch != "dev" {
		t.Errorf("RepoFor(acme/app) = %+v, %v, want the acme/app.git entry", r, ok)
	}
	if r, ok := dotGit.RepoFor("ACME/APP"); !ok || r.BaseBranch != "dev" {
		t.Errorf("RepoFor(ACME/APP) = %+v, %v, want the same entry as acme/app", r, ok)
	}
	// Two spellings of one name, and no exact match for the repository: the
	// first of the sorted keys answers, every time.
	folded := &Config{Repos: map[string]Repo{
		"Acme/App": {BaseBranch: "one"},
		"ACME/APP": {BaseBranch: "two"},
	}}
	for i := range 8 {
		r, ok := folded.RepoFor("acme/app")
		if !ok || r.BaseBranch != "two" {
			t.Fatalf("read %d: RepoFor(acme/app) = %+v, %v, want ACME/APP's entry (the first sorted key) every time", i, r, ok)
		}
	}
	// A repository this tree cannot name, and a config that could not be
	// read, both name no entry.
	for _, repo := range []string{"", "acme", "acme/", "/app", "acme/../app", "acme/app/x/y"} {
		if _, ok := dotGit.RepoFor(repo); ok {
			t.Errorf("RepoFor(%q) found an entry, want none: it is not a canonical owner/name", repo)
		}
	}
	var nilCfg *Config
	if _, ok := nilCfg.RepoFor("acme/app"); ok {
		t.Error("a nil config named a repository, want none")
	}
}

// TestConfigEnrollsIsALocalFact: whether a repository is enrolled is read off
// this machine's own project config's repos: map — the same map `fugaro
// project policy publish` seeds the published allow-list from — and nothing
// else: no bucket, no credentials, no policy node. It is what a checkout's
// row is held to before it can be valid (or recorded at all), because a
// checkout's own fugaro.yaml claiming a project is a claim the checkout
// makes, not an enrollment the operator gave.
//
// The fold is the tree's own (task.ASCIILower), not Unicode case folding: two
// names that differ only by a Unicode case pair (U+212A KELVIN SIGN and "k")
// are two repositories, and reading them as one would let a name the operator
// never enrolled be treated as enrolled.
//
// Mutation (run, restore): return true unconditionally, or compare only
// owner/name and not the provider, and the first and third cases below fail.
// Fold with strings.EqualFold instead of task.ASCIILower and the look-alike
// case fails: the repository above is written "acme/" + U+212A + "pp" (a Go
// escape, so this file carries no look-alike character of its own), and
// Unicode simple case folding reads that as the enrolled "acme/kpp".
func TestConfigEnrollsIsALocalFact(t *testing.T) {
	cfg := &Config{Repos: map[string]Repo{
		"acme/app":     {Provider: "github"},
		"Acme/Mixed":   {Provider: "bitbucket"},
		"acme/no-prov": {},
		"acme/other":   {Provider: "github"},
		"acme/kpp":     {Provider: "github"},
	}}
	for _, c := range []struct {
		name     string
		provider string
		repo     string
		want     bool
	}{
		{"enrolled, same provider", "github", "acme/app", true},
		{"enrolled, another provider", "bitbucket", "acme/app", false},
		{"enrolled with no provider in the entry", "github", "acme/no-prov", true},
		{"the fold the allow-list uses", "bitbucket", "acme/mixed", true},
		{"the fold is ASCII only: a look-alike is another repository", "github", "acme/\u212app", false},
		{"not in the map at all", "github", "attacker/evil", false},
		{"a different repository", "github", "acme/vanished", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := cfg.Enrolls(c.provider, c.repo); got != c.want {
				t.Errorf("Enrolls(%q, %q) = %v, want %v", c.provider, c.repo, got, c.want)
			}
		})
	}
	// A config that could not be read enrolls nothing: the reader fails
	// closed, so a row it cannot check is never valid.
	var nilCfg *Config
	if nilCfg.Enrolls("github", "acme/app") {
		t.Error("a nil config enrolled a repository, want nothing enrolled")
	}
	if (&Config{}).Enrolls("github", "acme/app") {
		t.Error("a config with no repos: enrolled a repository, want nothing enrolled")
	}
}
