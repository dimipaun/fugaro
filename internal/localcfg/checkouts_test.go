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
	if dropped == 0 {
		t.Error("dropped = 0, want the entries the bounds cut")
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

// TestConfigEnrollsIsALocalFact: whether a repository is enrolled is read off
// this machine's own project config's repos: map — the same map `fugaro
// project policy publish` seeds the published allow-list from — and nothing
// else: no bucket, no credentials, no policy node. It is what a checkout's
// row is held to before it can be valid (or recorded at all), because a
// checkout's own fugaro.yaml claiming a project is a claim the checkout
// makes, not an enrollment the operator gave.
//
// Mutation (run, restore): return true unconditionally, or compare only
// owner/name and not the provider, and the first and third cases below fail.
func TestConfigEnrollsIsALocalFact(t *testing.T) {
	cfg := &Config{Repos: map[string]Repo{
		"acme/app":     {Provider: "github"},
		"Acme/Mixed":   {Provider: "bitbucket"},
		"acme/no-prov": {},
		"acme/other":   {Provider: "github"},
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
