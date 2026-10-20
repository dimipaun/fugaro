package localcfg

import (
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

// TestRecordCheckoutWritesTheDocument: recording a checkout writes a file of
// its own beside the projects directory (never into a project config), mode
// 0600 in a 0700 directory, keyed by the repository identity.
func TestRecordCheckoutWritesTheDocument(t *testing.T) {
	getenv := checkoutsEnv(t)
	path, err := CheckoutsPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro", CheckoutsFile); path != want {
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
func TestRecordCheckoutKeepsAnAlreadyKnownPath(t *testing.T) {
	getenv := checkoutsEnv(t)
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(0)); err != nil {
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
	if err := RecordCheckout(getenv, "aurora", checkoutID("github", "acme/app"), "/src/app", at(5)); err != nil {
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
	dir := filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory where the state file goes: the rename over it fails.
	if err := os.MkdirAll(filepath.Join(dir, CheckoutsFile), 0o700); err != nil {
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
