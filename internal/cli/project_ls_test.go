package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// projectLsYAML is a fugaro.yaml that validates cleanly, so the record tests
// run a command that succeeds and can only fail by what it does beside it.
const projectLsYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// projectLsFixture points XDG_CONFIG_HOME at a directory of the test's own
// and writes two installations into it: the sandbox one (aurora) and a second
// (bramble), so a listing that returned "the one config" fails here.
func projectLsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	writeProject(t, "aurora", "aurora-gcp")
	writeProject(t, "bramble", "bramble-gcp")
	return dir
}

// projectLsProjectYAML is a fugaro.yaml that names project and nothing else.
func projectLsProjectYAML(project string) string {
	return "version: 1\nproject: " + project + "\n"
}

// projectLsCheckout makes a git checkout at dir whose fugaro.yaml is yaml and
// whose origin is origin, and returns the checkout's toplevel as git resolves
// it (the path a command in it would record, which is what the fixture has to
// compare against).
func projectLsCheckout(t *testing.T, dir, yaml, origin string) string {
	t.Helper()
	gitCheckout(t, dir, yaml)
	testutil.Git(t, dir, "remote", "add", "origin", origin)
	return testutil.Git(t, dir, "rev-parse", "--show-toplevel")
}

// projectLsRemember records a checkout exactly as a command running in it does, and
// returns the identity it was recorded under.
func projectLsRemember(t *testing.T, project, path, origin string) localcfg.CheckoutIdentity {
	t.Helper()
	id := localcfg.CheckoutIdentity{Provider: "github", Repo: strings.TrimSuffix(strings.TrimPrefix(origin, "https://github.com/"), ".git")}
	if err := localcfg.RecordCheckout(os.Getenv, project, id, path, time.Now()); err != nil {
		t.Fatal(err)
	}
	return id
}

// projectLsJSON runs `project ls --json` and decodes its document.
func projectLsJSON(t *testing.T) (projectListDoc, string) {
	t.Helper()
	out, _, err := execute(t, "project", "ls", "--json")
	if err != nil {
		t.Fatalf("project ls --json: %v", err)
	}
	var doc projectListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return doc, out
}

// projectLsEntry is the listing's entry for name.
func projectLsEntry(t *testing.T, doc projectListDoc, name string) projectListEntry {
	t.Helper()
	for _, p := range doc.Projects {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("project %s is not in %+v", name, doc.Projects)
	return projectListEntry{}
}

// jsonTags is a type's JSON field names, in declaration order, read off the
// tags so a rename is caught too.
func jsonTags(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v)
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

// TestProjectLsListsInstallationsAndTheirCheckouts: the document is what the
// console's first run is built on — every installation, with its GCP project,
// region and name, and the repositories this machine remembers a checkout of,
// with the checkout's path re-validated.
func TestProjectLsListsInstallationsAndTheirCheckouts(t *testing.T) {
	dir := projectLsFixture(t)
	co := projectLsCheckout(t, filepath.Join(dir, "src", "app"), projectLsProjectYAML("aurora"), "https://github.com/acme/app.git")
	projectLsRemember(t, "aurora", co, "https://github.com/acme/app.git")

	doc, out := projectLsJSON(t)
	if doc.Version != projectListVersion {
		t.Errorf("version = %d, want %d", doc.Version, projectListVersion)
	}
	if len(doc.Projects) != 2 || len(doc.Warnings) != 0 {
		t.Fatalf("document = %+v, want the two installations and no warning", doc)
	}
	a := projectLsEntry(t, doc, "aurora")
	if a.Installation != "aurora" || a.GCPProject != "aurora-gcp" || a.Region != "us-east5" || a.Error != "" {
		t.Errorf("aurora = %+v, want its config's name, gcp_project and region", a)
	}
	if len(a.Checkouts) != 1 {
		t.Fatalf("aurora's checkouts = %+v, want the one remembered repository", a.Checkouts)
	}
	c := a.Checkouts[0]
	if c.Provider != "github" || c.Repo != "acme/app" || len(c.Paths) != 1 {
		t.Fatalf("the checkout = %+v, want github/acme/app with its one path", c)
	}
	if p := c.Paths[0]; !p.Valid || p.Reason != "" || p.Path != co {
		t.Errorf("the path = %+v, want %q valid and with no reason", p, co)
	}
	if b := projectLsEntry(t, doc, "bramble"); len(b.Checkouts) != 0 {
		t.Errorf("bramble = %+v, want no checkouts", b)
	}

	// The schema golden: every key at the top level and under one checkout,
	// so a field added to either fails a named test rather than reaching the
	// console.
	var top map[string]any
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatal(err)
	}
	if k := keysOf(t, top); !reflect.DeepEqual(k, goldenKeys(t, "project_list_keys.golden")) {
		t.Errorf("project ls --json top-level keys = %v", k)
	}
	projects, _ := top["projects"].([]any)
	if len(projects) == 0 {
		t.Fatal("no project in the decoded document")
	}
	first, _ := projects[0].(map[string]any)
	checkouts, _ := first["checkouts"].([]any)
	if len(checkouts) == 0 {
		t.Fatal("no checkout in the decoded document")
	}
	firstCheckout, _ := checkouts[0].(map[string]any)
	if k := keysOf(t, firstCheckout); !reflect.DeepEqual(k, goldenKeys(t, "project_list_checkout_keys.golden")) {
		t.Errorf("project ls --json projects[].checkouts[] keys = %v", k)
	}

	// The named set, spelled out: the entry and the path are exactly these
	// fields, so a localcfg.Config field cannot join the document by a
	// rename or a promotion.
	wantEntry := []string{"name", "installation", "gcp_project", "region", "error", "checkouts"}
	if got := jsonTags(t, projectListEntry{}); !reflect.DeepEqual(got, wantEntry) {
		t.Errorf("projectListEntry fields = %v, want exactly %v", got, wantEntry)
	}
	wantPath := []string{"path", "valid", "reason"}
	if got := jsonTags(t, projectCheckoutPath{}); !reflect.DeepEqual(got, wantPath) {
		t.Errorf("projectCheckoutPath fields = %v, want exactly %v", got, wantPath)
	}
	if reflect.TypeOf(projectListDoc{}) == reflect.TypeOf(localcfg.Config{}) {
		t.Error("projectListDoc IS localcfg.Config")
	}

	// The text form names the same facts, as a table.
	text, _, err := execute(t, "project", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PROJECT", "GCP PROJECT", "aurora", "aurora-gcp", "us-east5", "github/acme/app", co, "ok"} {
		if !strings.Contains(text, want) {
			t.Errorf("text listing does not name %q:\n%s", want, text)
		}
	}
}

// TestProjectLsReportsAStalePathAndKeepsIt: a remembered path is re-validated
// on the read, and a path that is gone is reported as invalid with a reason
// code — never silently dropped, which is the read's half of the contract
// (the write path is what prunes).
func TestProjectLsReportsAStalePathAndKeepsIt(t *testing.T) {
	dir := projectLsFixture(t)
	stale := filepath.Join(dir, "gone", "app")
	projectLsRemember(t, "aurora", stale, "https://github.com/acme/app.git")

	doc, _ := projectLsJSON(t)
	c := projectLsEntry(t, doc, "aurora").Checkouts
	if len(c) != 1 || len(c[0].Paths) != 1 {
		t.Fatalf("checkouts = %+v, want the stale path listed", c)
	}
	if p := c[0].Paths[0]; p.Valid || p.Reason != checkoutMissing {
		t.Errorf("the stale path = %+v, want valid false and reason %q", p, checkoutMissing)
	}

	// The read repaired nothing: the state file is byte for byte what it was.
	before := projectLsState(t)
	if _, _, err := execute(t, "project", "ls"); err != nil {
		t.Fatal(err)
	}
	if after := projectLsState(t); after != before {
		t.Errorf("project ls rewrote the remembered checkouts:\nbefore %s\nafter  %s", before, after)
	}
}

// projectLsState is the remembered checkouts as they are on disk, "" when there is
// no file.
func projectLsState(t *testing.T) string {
	t.Helper()
	path, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestProjectLsReportsACheckoutThatNamesAnotherProject: a directory that is a
// checkout, but of another project (or of none), is not this project's
// checkout — and a listing that took it for one would send the console to the
// wrong installation.
func TestProjectLsReportsACheckoutThatNamesAnotherProject(t *testing.T) {
	dir := projectLsFixture(t)
	other := projectLsCheckout(t, filepath.Join(dir, "src", "other"), projectLsProjectYAML("bramble"), "https://github.com/acme/app.git")
	none := gitCheckout(t, filepath.Join(dir, "src", "none"), "version: 1\n")
	testutil.Git(t, none, "remote", "add", "origin", "https://github.com/acme/app.git")
	projectLsRemember(t, "aurora", other, "https://github.com/acme/app.git")
	projectLsRemember(t, "aurora", none, "https://github.com/acme/app.git")

	doc, _ := projectLsJSON(t)
	got := map[string]string{}
	for _, p := range projectLsEntry(t, doc, "aurora").Checkouts[0].Paths {
		got[p.Path] = p.Reason
	}
	if got[other] != checkoutOtherProject {
		t.Errorf("the bramble checkout's reason = %q, want %q", got[other], checkoutOtherProject)
	}
	if got[none] != checkoutOtherProject {
		t.Errorf("the checkout with no project: = %q, want %q", got[none], checkoutOtherProject)
	}
}

// TestProjectLsReportsACheckoutOfAnotherRepository: the directory is a
// checkout of this project, but its origin is another repository than the one
// the path was recorded for — the case a re-cloned or re-pointed checkout
// leaves behind.
func TestProjectLsReportsACheckoutOfAnotherRepository(t *testing.T) {
	dir := projectLsFixture(t)
	// Recorded as acme/app, but the directory's origin says acme/other.
	co := projectLsCheckout(t, filepath.Join(dir, "src", "app"), projectLsProjectYAML("aurora"), "https://github.com/acme/other.git")
	projectLsRemember(t, "aurora", co, "https://github.com/acme/app.git")

	doc, _ := projectLsJSON(t)
	p := projectLsEntry(t, doc, "aurora").Checkouts[0].Paths[0]
	if p.Valid || p.Reason != checkoutOtherRepository {
		t.Errorf("the path = %+v, want valid false and reason %q", p, checkoutOtherRepository)
	}
}

// TestProjectLsReportsASymlinkLoop: a path that resolves to nothing — a
// symlink loop, a broken link, a dangling mount — is a path that is not
// there, and the read has to say so rather than fail.
func TestProjectLsReportsASymlinkLoop(t *testing.T) {
	dir := projectLsFixture(t)
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	projectLsRemember(t, "aurora", loop, "https://github.com/acme/app.git")

	doc, _ := projectLsJSON(t)
	p := projectLsEntry(t, doc, "aurora").Checkouts[0].Paths[0]
	if p.Valid || p.Reason != checkoutMissing {
		t.Errorf("the symlink loop = %+v, want valid false and reason %q", p, checkoutMissing)
	}
}

// TestProjectLsPrintsNothingTheDiskCanDrive: every string in the document
// comes from a file this process did not write, so every one of them goes
// through the printable escaping and the length bound — a control character,
// a bidi override or a path longer than any machine has is data, not output.
func TestProjectLsPrintsNothingTheDiskCanDrive(t *testing.T) {
	projectLsFixture(t)
	// Hand-written state, the way a state file that is not ours arrives:
	// the identity carries a right-to-left override and the path a bell, an
	// escape and 400 bytes of padding.
	path, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	long := "/src/" + strings.Repeat("x", 400)
	body := `{"version": 1, "projects": {"aurora": {"github/acme\u202eapp": {` +
		`"provider": "github", "repo": "acme\u202eapp", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "/src/\u0007app", "recorded_at": "2026-10-20T00:00:00Z"},` +
		`{"path": "` + long + `", "recorded_at": "2026-10-20T00:00:00Z"}]}}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, out := projectLsJSON(t)
	c := projectLsEntry(t, doc, "aurora").Checkouts
	if len(c) != 1 {
		t.Fatalf("checkouts = %+v, want the hand-written one", c)
	}
	if c[0].Repo != `acme\u202eapp` {
		t.Errorf("the repository = %q, want the bidi override escaped", c[0].Repo)
	}
	if got := c[0].Paths[0].Path; !strings.Contains(got, `\u0007`) {
		t.Errorf("the path = %q, want the bell escaped", got)
	}
	if got := c[0].Paths[1].Path; !strings.HasSuffix(got, "... (truncated)") || len(got) > 250 {
		t.Errorf("the over-long path = %q, want it bounded and marked", got)
	}
	// Nothing in either form carries a control or format character: what a
	// terminal does with one is not this command's to decide.
	text, _, err := execute(t, "project", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range map[string]string{"--json": out, "text": text} {
		for _, r := range form {
			if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
				t.Fatalf("the output carries %q (U+%04X):\n%s", r, r, form)
			}
		}
	}
}

// TestProjectLsListsAConfigItCannotParse: a project config that cannot be
// read is listed by name with a bounded, inert reason, and never aborts the
// listing — the console's picker has to be able to show it and say why.
func TestProjectLsListsAConfigItCannotParse(t *testing.T) {
	dir := projectLsFixture(t)
	// bramble's config has a key nothing knows (Parse is strict), and
	// cygnus's names another project — whose name carries a bidi override,
	// so the reason the loader built is a string off the disk too.
	if err := os.WriteFile(filepath.Join(dir, "xdg", "fugaro", "projects", "bramble.yaml"),
		[]byte("version: 1\nname: bramble\ngcp_project: bramble-gcp\nnot_a_key: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "xdg", "fugaro", "projects", "cygnus.yaml"),
		[]byte("version: 1\nname: \"cyg\u202enus\"\ngcp_project: cygnus-gcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, out := projectLsJSON(t)
	if len(doc.Projects) != 3 {
		t.Fatalf("projects = %+v, want every config listed", doc.Projects)
	}
	for _, name := range []string{"bramble", "cygnus"} {
		p := projectLsEntry(t, doc, name)
		if p.Error == "" {
			t.Errorf("%s = %+v, want the reason it could not be read", name, p)
		}
		if p.GCPProject != "" || p.Installation != "" {
			t.Errorf("%s = %+v, want nothing read out of it", name, p)
		}
		if len(p.Error) > 300 {
			t.Errorf("%s's reason is %d bytes, want it bounded: %q", name, len(p.Error), p.Error)
		}
		if strings.ContainsRune(p.Error, '\u202e') {
			t.Errorf("%s's reason carries the raw override: %q", name, p.Error)
		}
	}
	if !strings.Contains(projectLsEntry(t, doc, "cygnus").Error, `\u202e`) {
		t.Errorf("cygnus's reason = %q, want the override escaped", projectLsEntry(t, doc, "cygnus").Error)
	}
	if strings.ContainsRune(out, '\u202e') {
		t.Errorf("the document carries the raw override:\n%s", out)
	}
	// aurora, which is fine, is still listed with everything read: the
	// listing does not stop at the first config it cannot parse.
	if a := projectLsEntry(t, doc, "aurora"); a.GCPProject != "aurora-gcp" || a.Error != "" {
		t.Errorf("aurora = %+v, want it read", a)
	}
	// The text form says the same, before the table, and still prints it.
	text, _, err := execute(t, "project", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bramble", "cygnus", "aurora-gcp", "PROJECT"} {
		if !strings.Contains(text, want) {
			t.Errorf("text listing does not name %q:\n%s", want, text)
		}
	}
}

// TestProjectLsReportsAStateFileItCannotRead: a state file that cannot be
// read is a warning and no checkouts — never "no checkouts" silently, and
// never a listing that fails: the projects are facts of the directory and are
// still printed.
func TestProjectLsReportsAStateFileItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":   "this is not json\n",
		"version 2":  `{"version": 2, "projects": {}}` + "\n",
		"over bound": `{"version": 1, "projects": {}}` + strings.Repeat(" ", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			projectLsFixture(t)
			path, err := localcfg.CheckoutsPath(os.Getenv)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			doc, _ := projectLsJSON(t)
			if len(doc.Projects) != 2 {
				t.Errorf("projects = %+v, want the installations still listed", doc.Projects)
			}
			if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "checkouts") {
				t.Errorf("warnings = %q, want the unreadable state file named", doc.Warnings)
			}
			text, _, err := execute(t, "project", "ls")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "aurora-gcp") {
				t.Errorf("the table is missing:\n%s", text)
			}
			// The read did not repair or replace it.
			if got, err := os.ReadFile(path); err != nil || string(got) != body {
				t.Errorf("the state file changed: %q, %v", got, err)
			}
		})
	}
}

// TestProjectLsKeepsTheBounds: what the listing shows is what the state file
// holds — at most 20 repositories per project and 5 paths per repository —
// and the entries it drops are the oldest, which the write path dropped
// before this read ever saw them.
func TestProjectLsKeepsTheBounds(t *testing.T) {
	projectLsFixture(t)
	for r := range localcfg.MaxCheckoutRepos + 2 {
		repo := "acme/app-" + string(rune('a'+r))
		for i := range localcfg.MaxCheckoutPaths + 1 {
			if err := localcfg.RecordCheckout(os.Getenv, "aurora",
				localcfg.CheckoutIdentity{Provider: "github", Repo: repo},
				"/src/"+repo+"/"+string(rune('a'+i)), time.Now().Add(time.Duration(r*10+i)*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	doc, _ := projectLsJSON(t)
	c := projectLsEntry(t, doc, "aurora").Checkouts
	if len(c) != localcfg.MaxCheckoutRepos {
		t.Fatalf("repositories = %d, want the %d-repository bound", len(c), localcfg.MaxCheckoutRepos)
	}
	for _, r := range c {
		if len(r.Paths) != localcfg.MaxCheckoutPaths {
			t.Errorf("%s/%s has %d paths, want the %d-path bound", r.Provider, r.Repo, len(r.Paths), localcfg.MaxCheckoutPaths)
		}
	}
	if c[0].Repo == "acme/app-a" || c[len(c)-1].Repo != "acme/app-"+string(rune('a'+localcfg.MaxCheckoutRepos+1)) {
		t.Errorf("checkouts = %+v, want the oldest dropped and the newest kept", c)
	}
}

// TestProjectLsWithNoInstallations: an empty list is a fact, not an error —
// the console's first run is exactly this state — and the text form says what
// to run.
func TestProjectLsWithNoInstallations(t *testing.T) {
	isolateProjects(t, t.TempDir())
	doc, out := projectLsJSON(t)
	if doc.Version != projectListVersion || len(doc.Projects) != 0 || len(doc.Warnings) != 0 {
		t.Errorf("document = %+v, want an empty versioned one", doc)
	}
	if !strings.Contains(out, `"projects": []`) || !strings.Contains(out, `"warnings": []`) {
		t.Errorf("an empty listing is not two empty arrays (never a null):\n%s", out)
	}
	text, _, err := execute(t, "project", "ls")
	if err != nil || !strings.Contains(text, "fugaro init") {
		t.Errorf("text %q, %v", text, err)
	}
}

// TestProjectLsWritesNothing: the console spawns this read, and a read the
// console spawns changes nothing — not the project configs, not the
// remembered checkouts, not the cache home — in either output form.
func TestProjectLsWritesNothing(t *testing.T) {
	dir := projectLsFixture(t)
	co := projectLsCheckout(t, filepath.Join(dir, "src", "app"), projectLsProjectYAML("aurora"), "https://github.com/acme/app.git")
	projectLsRemember(t, "aurora", co, "https://github.com/acme/app.git")
	projectLsRemember(t, "aurora", filepath.Join(dir, "gone"), "https://github.com/acme/app.git")
	before := treeState(t, dir)
	for _, args := range [][]string{{"project", "ls"}, {"project", "ls", "--json"}} {
		if _, _, err := execute(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if after := treeState(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("project ls changed the machine:\nbefore %v\nafter  %v", before, after)
	}
}

// TestProjectLsNeverCarriesAConfigField: the document is one field away from
// being a credential dump, so a value that lives in localcfg.Config and not
// in the named set must not reach the output.
func TestProjectLsNeverCarriesAConfigField(t *testing.T) {
	dir := projectLsFixture(t)
	path := filepath.Join(dir, "xdg", "fugaro", "projects", "aurora.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sentinels := []string{"sentinel-provider-secret", "sentinel-project-secret", "sentinel-user", "sentinel-terraform-bucket"}
	extra := strings.Join([]string{
		"user: sentinel-user",
		"providers:",
		"  sentinelprovider:",
		"    kind: anthropic-compat",
		"    base_url: https://sentinel-provider-base-url.example",
		"    auth: bearer",
		"    secret: sentinel-provider-secret",
		"    models: [sentinel-model]",
		"project_secrets:",
		"  anthropic-api-key:",
		"    allow_data_to: [acme/sentinel-project-secret]",
		"terraform:",
		"  state_bucket: sentinel-terraform-bucket",
		"",
	}, "\n")
	if err := os.WriteFile(path, append(data, extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := localcfg.LoadProject(os.Getenv, "aurora"); err != nil {
		t.Fatalf("the fixture config does not load: %v", err)
	}
	_, out := projectLsJSON(t)
	for _, s := range sentinels {
		if strings.Contains(out, s) {
			t.Errorf("project ls --json carries %q, a localcfg.Config field outside the named set:\n%s", s, out)
		}
	}
	// The paths are local state: the checkout is in the document, the
	// directory the configs are in is not.
	if strings.Contains(out, filepath.Join(dir, "xdg")) {
		t.Errorf("project ls --json carries the config directory:\n%s", out)
	}
}

// TestACommandInACheckoutRecordsIt: the remembered checkouts are written by
// the commands that already resolve a checkout's project — the one common
// code path, not one per command — so `fugaro validate` in a checkout is what
// makes the console's first run find it.
func TestACommandInACheckoutRecordsIt(t *testing.T) {
	dir := projectLsFixture(t)
	co := projectLsCheckout(t, filepath.Join(dir, "src", "app"), projectLsYAML, "https://github.com/acme/app.git")
	t.Chdir(co)
	if out, _, err := execute(t, "validate", "--offline"); err != nil {
		t.Fatalf("fugaro validate: %v\n%s", err, out)
	}
	known, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := known.Projects["aurora"]["github/acme/app"]
	if !ok || len(r.Paths) != 1 || r.Paths[0].Path != co {
		t.Fatalf("aurora's checkouts = %+v, want the checkout validate ran in", known.Projects)
	}
}

// captureStderr runs f with the process's own stderr redirected and returns
// what f wrote there. The commands' own output goes to the command's writer
// (execute's buffer), so what lands here is what a side effect printed behind
// the command's back.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAFailedRecordNeverFailsTheCommand: remembering a checkout is best
// effort. A state file that cannot be written (here: a directory where the
// file goes) leaves the resolver — and so the command that called it —
// exactly as it was: the checkout is returned, the error is nil, nothing is
// printed about it and the command exits 0.
func TestAFailedRecordNeverFailsTheCommand(t *testing.T) {
	dir := projectLsFixture(t)
	co := projectLsCheckout(t, filepath.Join(dir, "src", "app"), projectLsYAML, "https://github.com/acme/app.git")
	statePath, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	// The resolver the commands share, called where the write it makes at
	// the end cannot succeed: the checkout is still the answer.
	var got *localcfg.Checkout
	printed := captureStderr(t, func() { got, err = checkoutProject(context.Background(), co) })
	if err != nil || got == nil || got.Project != "aurora" {
		t.Fatalf("checkoutProject = %+v, %v, want the checkout and no error", got, err)
	}
	t.Chdir(co)
	var stdout, stderr string
	printed += captureStderr(t, func() { stdout, stderr, err = execute(t, "validate", "--offline") })
	if err != nil {
		t.Fatalf("fugaro validate failed because it could not remember a checkout: %v\n%s", err, stdout)
	}
	// Nothing was said about it anywhere: not on the command's stderr, not
	// on the process's, and the path is not in either.
	for _, out := range []string{stderr, printed} {
		if strings.Contains(out, "checkout") || strings.Contains(out, dir) {
			t.Errorf("the failure to remember the checkout was printed: %q", out)
		}
	}
}
