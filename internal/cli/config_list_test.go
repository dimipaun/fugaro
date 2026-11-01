package cli

import (
	"context"
	"encoding/json"
	"fmt"
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

// configListFixture points XDG_CONFIG_HOME at a directory of the test's own
// and writes two installations into it: the sandbox one (aurora) and a second
// (bramble), so a listing that returned "the one config" would fail here.
func configListFixture(t *testing.T) (dir, aurora, bramble string) {
	t.Helper()
	dir = t.TempDir()
	isolateProjects(t, dir)
	aurora = writeProject(t, "aurora", "aurora-gcp")
	bramble = writeProject(t, "bramble", "bramble-gcp")
	return dir, aurora, bramble
}

// TestConfigListNamesTheInstallations: the document is the enumeration the
// v1 design's gap 5 says does not exist — every project config, with its GCP
// project and region, and no more.
func TestConfigListNamesTheInstallations(t *testing.T) {
	configListFixture(t)
	out, _, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := map[string]configListInstallation{}
	for _, inst := range doc.Installations {
		got[inst.Name] = inst
	}
	if len(doc.Installations) != 2 {
		t.Fatalf("installations = %+v, want the two project configs", doc.Installations)
	}
	if a := got["aurora"]; a.GCPProject != "aurora-gcp" || a.Region != "us-east5" {
		t.Errorf("aurora = %+v, want its config's gcp_project and region", a)
	}
	if b := got["bramble"]; b.GCPProject != "bramble-gcp" || b.Region != "us-east5" {
		t.Errorf("bramble = %+v, want its config's gcp_project and region", b)
	}
	if got["aurora"].SharedMerged || got["aurora"].SharedCheckedAt != nil {
		t.Errorf("aurora carries a shared config this machine never read: %+v", got["aurora"])
	}

	// The schema golden: every key at the top level and under one
	// installation, so a field added to either type fails a named test
	// rather than reaching the console.
	var top map[string]any
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatal(err)
	}
	if k := keysOf(t, top); !reflect.DeepEqual(k, goldenKeys(t, "config_list_keys.golden")) {
		t.Errorf("config list --json top-level keys = %v", k)
	}
	inst, _ := top["installations"].([]any)
	if len(inst) == 0 {
		t.Fatal("no installation in the decoded document")
	}
	first, _ := inst[0].(map[string]any)
	if k := keysOf(t, first); !reflect.DeepEqual(k, goldenKeys(t, "config_list_installation_keys.golden")) {
		t.Errorf("config list --json installations[] keys = %v", k)
	}

	text, _, err := execute(t, "config", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NAME", "aurora", "aurora-gcp", "bramble", "bramble-gcp"} {
		if !strings.Contains(text, want) {
			t.Errorf("text listing does not name %q:\n%s", want, text)
		}
	}
}

// TestConfigListReportsTheSharedCopyItHas: "whether a shared config was
// merged, and when the shared copy was last checked" are the cache's own
// facts, and a copy cached for ANOTHER GCP project is not this
// installation's (SameInstallation's GCP-project half).
func TestConfigListReportsTheSharedCopyItHas(t *testing.T) {
	configListFixture(t)
	checked := time.Now().Add(-30 * time.Hour).UTC().Truncate(time.Second)
	if err := localcfg.SaveSharedCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp", Bucket: "fugaro-runs-aurora-gcp",
		Generation: 7, CheckedAt: checked, YAML: "name: aurora\ngcp_project: aurora-gcp\n",
	}); err != nil {
		t.Fatal(err)
	}
	// A cache entry for a DIFFERENT installation of the same name: the
	// config's gcp_project moved, so the cached copy is not this one.
	if err := localcfg.SaveSharedCache(os.Getenv, "bramble", localcfg.SharedCacheEntry{
		GCPProject: "somebody-elses-gcp", Bucket: "fugaro-runs-somebody-elses-gcp",
		Generation: 1, CheckedAt: checked, YAML: "name: bramble\ngcp_project: somebody-elses-gcp\n",
	}); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]configListInstallation{}
	for _, inst := range doc.Installations {
		got[inst.Name] = inst
	}
	a := got["aurora"]
	if !a.SharedMerged || a.SharedCheckedAt == nil || !a.SharedCheckedAt.Equal(checked) {
		t.Errorf("aurora = %+v, want the cached shared copy and its checked_at %s", a, checked)
	}
	if b := got["bramble"]; b.SharedMerged {
		t.Errorf("bramble = %+v: a cached copy of another GCP project is not this installation's", b)
	}
	// A copy over SharedFreshFor old is still this machine's copy; the
	// listing says how old it is rather than dropping the fact.
	if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "shared config was last checked") {
		t.Errorf("warnings = %q, want the stale shared copy named", doc.Warnings)
	}
}

// TestConfigListNeverCarriesAConfigField is V2.1.1's security half: the
// document is one field away from being a credential dump, so a value that
// lives in localcfg.Config and not in the named set must not reach the
// output, and the types must not BE that struct.
//
// Mutation (run, restore): add a field to configListInstallation (say
// `RunsBucket string `json:"runs_bucket"“ and fill it from lc.RunsBucketName())
// and this test fails on the field-set assertion and on the sentinel scan.
func TestConfigListNeverCarriesAConfigField(t *testing.T) {
	dir, path, _ := configListFixture(t)
	// Sentinel values in the fields the console must never see, written
	// into aurora's config: a provider's base URL, the reserved project
	// secrets, the named Claude accounts, the Terraform block, the runs
	// bucket and the local config's own bucket URL.
	sentinels := []string{
		"sentinel-provider-base-url", "sentinel-claude-account", "sentinel-project-secret",
		"sentinel-image-source", "sentinel-user", "sentinel-terraform-bucket",
		"sentinel-provider-secret", "sentinel-model", "sentinelprovider",
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	extra := strings.Join([]string{
		"image_source: ghcr.io/sentinel-image-source",
		"user: sentinel-user",
		"providers:",
		"  sentinelprovider:",
		"    kind: anthropic-compat",
		"    base_url: https://sentinel-provider-base-url.example",
		"    auth: bearer",
		"    secret: sentinel-provider-secret",
		"    models: [sentinel-model]",
		"claude_accounts:",
		"  sentinel-claude-account:",
		"    allow_data_to: [acme/sentinel-claude-account]",
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
	// The config must still load: a listing that refused it would prove
	// nothing about what a readable one carries.
	if _, _, err := localcfg.LoadProject(os.Getenv, "aurora"); err != nil {
		t.Fatalf("the fixture config does not load: %v", err)
	}

	out, _, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sentinels {
		if strings.Contains(out, s) {
			t.Errorf("config list --json carries %q, a localcfg.Config field outside the named set:\n%s", s, out)
		}
	}

	// The type identity, spelled out: the document is not localcfg.Config
	// and does not embed it, so a field added to that struct cannot join
	// this document by promotion.
	cfgType := reflect.TypeOf(localcfg.Config{})
	docType := reflect.TypeOf(configListDoc{})
	instType := reflect.TypeOf(configListInstallation{})
	for _, ft := range []reflect.Type{docType, instType} {
		if ft == cfgType {
			t.Errorf("%s IS localcfg.Config", ft)
		}
		for i := 0; i < ft.NumField(); i++ {
			f := ft.Field(i)
			if f.Type == cfgType || (f.Anonymous && f.Type.Kind() == reflect.Struct) {
				t.Errorf("%s embeds %s: a field added to it would join this document", ft, f.Type)
			}
		}
	}

	// The installation's fields are exactly the named set (V2.1.1), read
	// off the JSON tags so a rename is caught too.
	var got []string
	for i := 0; i < instType.NumField(); i++ {
		name, _, _ := strings.Cut(instType.Field(i).Tag.Get("json"), ",")
		got = append(got, name)
	}
	want := []string{"name", "gcp_project", "region", "shared_merged", "shared_checked_at", "base_branch", "checkouts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configListInstallation fields = %v, want exactly the named set %v", got, want)
	}
	// The projects directory is not in the document either: the console
	// picks from names, and the operator's config path is theirs.
	if strings.Contains(out, filepath.Join(dir, "xdg")) {
		t.Errorf("config list --json carries the config directory:\n%s", out)
	}
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

// configListJSON runs `config list --json` and decodes its document.
func configListJSON(t *testing.T) (configListDoc, string) {
	t.Helper()
	out, _, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatalf("config list --json: %v", err)
	}
	var doc configListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return doc, out
}

// configListCheckoutsOf is an installation's checkouts, by name.
func configListCheckoutsOf(t *testing.T, doc configListDoc, name string) []configListCheckout {
	t.Helper()
	for _, inst := range doc.Installations {
		if inst.Name == name {
			return inst.Checkouts
		}
	}
	t.Fatalf("installation %s is not in %+v", name, doc.Installations)
	return nil
}

// configListState is the remembered checkouts as they are on disk.
func configListState(t *testing.T) string {
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

// configListWriteState puts a state file this process did not write where the
// reader looks for it: the tests that hand over a body rather than recording a
// checkout have to make the file's directory too (the writer makes it 0700;
// here any mode does, the reader does not look at the directory's).
func configListWriteState(t *testing.T, body string) string {
	t.Helper()
	path, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// configListRecordBaseBranch writes the recorded base branch into project's
// config: the project's own top-level base_branch, and, when repo is not
// empty, that repository's repos.<repo>.base_branch. They are the two layers a
// console launch may be cut from (the launch has no checkout of the repository
// as its working directory, so the checkout layer of the CLI's merged order is
// out of reach — docs/design/desktop-console.md §5.1 rule 11), so a fixture
// needs a way to set them without enrolling anything else. A later enrollRepo
// adds to the block this writes.
func configListRecordBaseBranch(t *testing.T, project, projectBranch string, entries ...[2]string) {
	t.Helper()
	path, err := localcfg.ProjectPath(os.Getenv, project)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	add := ""
	if projectBranch != "" {
		add += "base_branch: " + projectBranch + "\n"
	}
	for i, entry := range entries {
		if i == 0 {
			add += "repos:\n"
		}
		add += "  " + entry[0] + ": { base_branch: " + entry[1] + " }\n"
	}
	if err := os.WriteFile(path, append(b, add...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestConfigListCarriesTheRecordedBaseBranch is rule 11's read: the branch a
// launch is cut from is shown in the window, and the branch the run keeps is
// the CLI's own merged resolution, whose checkout layer a launch never has —
// so the console requires the installation's recorded value, and this document
// is where it reads it (V2.5.1a).
//
// Both fields come off the LOCAL project config and nothing else: not the
// checkout's own fugaro.yaml, which the CLI's order reads first and which the
// app's empty working directory cannot reach, and not the published policy
// node, which is a network read `config list` does not do. The checkout here
// writes git.base_branch, and neither field carries it.
func TestConfigListCarriesTheRecordedBaseBranch(t *testing.T) {
	dir := t.TempDir()
	isolateProjects(t, dir)
	writeProject(t, "aurora", "aurora-gcp")
	writeProject(t, "bramble", "bramble-gcp")
	// The second entry is keyed the way a hand-edited config can leave it —
	// the repository with the ".git" — because the launch resolves its own
	// repos.<r> entry in canonical form: the row has to print the branch that
	// run is cut from, not the branch a stricter spelling would have found
	// (localcfg.Config.RepoFor, cloudEnv.localRepo).
	configListRecordBaseBranch(t, "aurora", "trunk",
		[2]string{"acme/app", "dev"}, [2]string{"acme/dotted.git", "release"})
	enrollRepo(t, "aurora", "acme/other")

	// A checkout whose own fugaro.yaml names a base branch: the field is not
	// that value, whatever its file says.
	app := checkoutsAt(t, filepath.Join(dir, "src", "app"),
		checkoutsProjectYAML("aurora")+"git:\n  base_branch: from-the-checkout\n",
		"https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", app, "https://github.com/acme/app.git")
	// An enrolled repository with no repos entry of its own: the row is
	// listed and records no base branch.
	other := checkoutsAt(t, filepath.Join(dir, "src", "other"), checkoutsProjectYAML("aurora"), "https://github.com/acme/other.git")
	checkoutsRemember(t, "aurora", other, "https://github.com/acme/other.git")
	// A repository whose only repos: entry is spelled "acme/dotted.git": the
	// launch finds that entry, so the row prints its branch.
	dotted := checkoutsAt(t, filepath.Join(dir, "src", "dotted"), checkoutsProjectYAML("aurora"), "https://github.com/acme/dotted.git")
	checkoutsRemember(t, "aurora", dotted, "https://github.com/acme/dotted.git")
	// And a repository aurora's repos: map does not name at all.
	unrecorded := checkoutsAt(t, filepath.Join(dir, "src", "unrecorded"), checkoutsProjectYAML("aurora"), "https://github.com/acme/unrecorded.git")
	checkoutsRemember(t, "aurora", unrecorded, "https://github.com/acme/unrecorded.git")

	doc, out := configListJSON(t)
	byName := map[string]configListInstallation{}
	for _, inst := range doc.Installations {
		byName[inst.Name] = inst
	}
	if got := byName["aurora"].BaseBranch; got != "trunk" {
		t.Errorf("aurora's base_branch = %q, want the project's own recorded value", got)
	}
	// An installation that records none carries "" and not an absent key:
	// absent is the console's own fact about an older CLI (§6.8), and reading
	// one as the other would hide which is which.
	if got, ok := byName["bramble"]; !ok || got.BaseBranch != "" {
		t.Errorf("bramble's base_branch = %q, want empty for an installation that records none", got.BaseBranch)
	}

	rows := map[string]configListCheckout{}
	for _, c := range configListCheckoutsOf(t, doc, "aurora") {
		rows[c.Repository] = c
	}
	if len(rows) != 4 {
		t.Fatalf("aurora's checkout rows = %+v, want the four remembered paths", rows)
	}
	if got := rows["github/acme/app"].BaseBranch; got != "dev" {
		t.Errorf("github/acme/app's base_branch = %q, want the repository's own recorded value", got)
	}
	// The entry is spelled with a ".git" the repository is not: the launch
	// resolves it anyway (task.CanonicalRepo), so the row carries its branch.
	if got := rows["github/acme/dotted"].BaseBranch; got != "release" {
		t.Errorf("github/acme/dotted's base_branch = %q, want the acme/dotted.git entry's value", got)
	}
	for _, repo := range []string{"github/acme/other", "github/acme/unrecorded"} {
		if got := rows[repo].BaseBranch; got != "" {
			t.Errorf("%s's base_branch = %q, want empty: its config records none", repo, got)
		}
	}
	if strings.Contains(out, "from-the-checkout") {
		t.Errorf("config list --json carries a base branch from the checkout's own fugaro.yaml:\n%s", out)
	}
	if !strings.Contains(out, `"base_branch": ""`) {
		t.Errorf("config list --json omits an empty base_branch instead of carrying it:\n%s", out)
	}
}

// TestConfigListListsTheCheckoutsThisMachineRemembers: the field is the second
// half of what the console's first run needs (D13, §3.4 rule 7) — the
// repositories of each installation and where this machine has them — and it
// arrives in the same document as the installation list, so the picker and
// the checkouts cannot disagree about which installation is which.
func TestConfigListListsTheCheckoutsThisMachineRemembers(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
	// A second, stale path, so the document carries a row with a reason as
	// well as a valid one.
	checkoutsRemember(t, "aurora", filepath.Join(dir, "gone", "app"), "https://github.com/acme/app.git")

	doc, out := configListJSON(t)
	if len(doc.Installations) != 2 || len(doc.Warnings) != 0 {
		t.Fatalf("document = %+v, want the two installations and no warning", doc)
	}
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 2 {
		t.Fatalf("aurora's checkouts = %+v, want the two remembered paths", got)
	}
	byPath := map[string]configListCheckout{}
	for _, c := range got {
		byPath[c.Path] = c
	}
	if c := byPath[co]; c.Repository != "github/acme/app" || !c.Valid || c.Reason != "" {
		t.Errorf("the valid checkout = %+v, want github/acme/app at %q, valid and with no reason", c, co)
	}
	if c := byPath[filepath.Join(dir, "gone", "app")]; c.Valid || c.Reason != checkoutMissing {
		t.Errorf("the stale checkout = %+v, want valid false and reason %q", c, checkoutMissing)
	}
	if b := configListCheckoutsOf(t, doc, "bramble"); len(b) != 0 {
		t.Errorf("bramble = %+v, want no checkouts", b)
	}

	// The schema golden: every key a checkout row can carry, so a field
	// added to the type fails a named test rather than reaching the console.
	// The stale row is the one pinned, because it is the one that carries
	// every key: reason is omitempty, so a valid row carries one fewer and a
	// reader must read a missing reason as "valid" (the console's JSON
	// contract §6.8).
	var top map[string]any
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatal(err)
	}
	insts, _ := top["installations"].([]any)
	if len(insts) == 0 {
		t.Fatal("no installation in the decoded document")
	}
	var rows []map[string]any
	for _, raw := range insts {
		inst, _ := raw.(map[string]any)
		if inst["name"] != "aurora" {
			continue
		}
		cs, _ := inst["checkouts"].([]any)
		for _, raw := range cs {
			row, _ := raw.(map[string]any)
			rows = append(rows, row)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("aurora's decoded rows = %v, want two", rows)
	}
	full := goldenKeys(t, "config_list_checkout_keys.golden")
	for _, row := range rows {
		k := keysOf(t, row)
		if row["valid"] == true {
			if want := []string{"base_branch", "enrolled", "path", "repository", "valid"}; !reflect.DeepEqual(k, want) {
				t.Errorf("a valid row's keys = %v, want %v (reason is omitted when there is none)", k, want)
			}
			continue
		}
		if !reflect.DeepEqual(k, full) {
			t.Errorf("config list --json installations[].checkouts[] keys = %v, want %v", k, full)
		}
	}

	// The named set, spelled out: the checkout is exactly these five fields,
	// so a localcfg field cannot join the document by a rename or a
	// promotion.
	wantCheckout := []string{"repository", "path", "base_branch", "enrolled", "valid", "reason"}
	if got := jsonTags(t, configListCheckout{}); !reflect.DeepEqual(got, wantCheckout) {
		t.Errorf("configListCheckout fields = %v, want exactly %v", got, wantCheckout)
	}

	// The text form names the same facts: the count, and the stale ones.
	text, _, err := execute(t, "config", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "CHECKOUTS") || !strings.Contains(text, "2 (1 stale)") {
		t.Errorf("the text listing does not count the checkouts:\n%s", text)
	}
}

// TestConfigListRevalidatesEveryPathAndWritesNothing is the read half of the
// read/write split, and the reason the checkouts are exposed by a read at
// all: the document re-validates what the state file holds and records
// nothing — not the path it looked at, not the identity the directory has
// since changed to, and not a rewrite of the file.
//
// The fixture holds two recorded checkouts, and the second is the one a
// recording resolver would change: the directory's origin is acme/other,
// while the state file remembers it as acme/other-2. A resolver that
// recorded what it looked at would add github/acme/other to the file.
//
// Mutation (run, restore): drop the consoleRead guard from recordCheckout
// (project.go) and make validateCheckoutPath call checkoutProject instead of
// resolveCheckout; the test fails on aurora's checkouts growing a third
// entry, github/acme/other, which is the identity the directory now has and
// the state file does not. Either edit alone is invisible here — the guard is
// what makes `config list` a read whatever it calls, and resolveCheckout is
// what keeps the read from depending on the guard — and that is the point of
// having both: this test pins the outcome, and the guard's own test
// (TestConsoleAllowListedReadsWriteNothing) pins the guard.
func TestConfigListRevalidatesEveryPathAndWritesNothing(t *testing.T) {
	dir := checkoutsFixture(t)
	valid := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", valid, "https://github.com/acme/app.git")
	renamed := checkoutsAt(t, filepath.Join(dir, "src", "other"), checkoutsProjectYAML("aurora"), "https://github.com/acme/other.git")
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "github", Repo: "acme/other-2"}, renamed, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	before, beforeState := treeState(t, dir), configListState(t)
	for _, args := range [][]string{{"config", "list", "--json"}, {"config", "list"}} {
		if _, _, err := execute(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	doc, _ := configListJSON(t)
	if got := configListCheckoutsOf(t, doc, "aurora"); len(got) != 2 {
		t.Fatalf("aurora's checkouts = %+v, want both remembered paths", got)
	}
	if after := configListState(t); after != beforeState {
		t.Errorf("config list rewrote the remembered checkouts:\nbefore %s\nafter  %s", beforeState, after)
	}
	if after := treeState(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("config list changed the config home:\nbefore %v\nafter  %v", before, after)
	}
}

// TestConfigListReportsAStalePathAndKeepsIt: a remembered path is
// re-validated on the read, and a path that is gone is reported as invalid
// with a reason code — never silently dropped, which is the read's half of
// the contract (the write path is what prunes).
func TestConfigListReportsAStalePathAndKeepsIt(t *testing.T) {
	dir := checkoutsFixture(t)
	stale := filepath.Join(dir, "gone", "app")
	checkoutsRemember(t, "aurora", stale, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 1 {
		t.Fatalf("checkouts = %+v, want the stale path listed", got)
	}
	if c := got[0]; c.Valid || c.Reason != checkoutMissing || c.Path != stale {
		t.Errorf("the stale path = %+v, want valid false, reason %q and the path kept", c, checkoutMissing)
	}
	// The text form counts it as stale rather than hiding it.
	text, _, err := execute(t, "config", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "1 (1 stale)") {
		t.Errorf("the text listing does not mark the stale checkout:\n%s", text)
	}
}

// TestConfigListReportsACheckoutThatNamesAnotherProject: a directory that is
// a checkout, but of another project (or of none), is not this project's
// checkout — and a listing that took it for one would send the console to the
// wrong installation.
func TestConfigListReportsACheckoutThatNamesAnotherProject(t *testing.T) {
	dir := checkoutsFixture(t)
	other := checkoutsAt(t, filepath.Join(dir, "src", "other"), checkoutsProjectYAML("bramble"), "https://github.com/acme/app.git")
	none := gitCheckout(t, filepath.Join(dir, "src", "none"), "version: 1\n")
	testutil.Git(t, none, "remote", "add", "origin", "https://github.com/acme/app.git")
	// The recorded string is the toplevel as git resolves it, the way a
	// command running in the checkout records it (resolveCheckout): the temp
	// directory's own spelling is not the path git answers with, and on darwin
	// it never is (/var is a symlink to /private/var), so recording the raw
	// one would make this test read the directory as "not a checkout" for a
	// reason that has nothing to do with the project its file names.
	none = testutil.Git(t, none, "rev-parse", "--show-toplevel")
	checkoutsRemember(t, "aurora", other, "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", none, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	got := map[string]string{}
	for _, c := range configListCheckoutsOf(t, doc, "aurora") {
		got[c.Path] = c.Reason
	}
	if got[other] != checkoutOtherProject {
		t.Errorf("the bramble checkout's reason = %q, want %q", got[other], checkoutOtherProject)
	}
	if got[none] != checkoutOtherProject {
		t.Errorf("the checkout with no project: = %q, want %q", got[none], checkoutOtherProject)
	}
}

// TestConfigListReportsACheckoutOfAnotherRepository: the directory is a
// checkout of this project, but its origin is another repository than the one
// the path was recorded for — the case a re-cloned or re-pointed checkout
// leaves behind.
func TestConfigListReportsACheckoutOfAnotherRepository(t *testing.T) {
	dir := checkoutsFixture(t)
	// Recorded as acme/app, but the directory's origin says acme/other.
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/other.git")
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	c := configListCheckoutsOf(t, doc, "aurora")[0]
	if c.Valid || c.Reason != checkoutOtherRepository {
		t.Errorf("the checkout = %+v, want valid false and reason %q", c, checkoutOtherRepository)
	}
	// The repository the state file remembers is still named as it was: the
	// read reports the mismatch, it does not adopt the directory's origin.
	if c.Repository != "github/acme/app" {
		t.Errorf("the repository = %q, want the recorded identity", c.Repository)
	}
}

// TestConfigListValidatesAgainstTheOriginsProvider: the read derives a path's
// provider the way the recording did — from the host its origin is on
// (checkoutProvider) — so a directory whose fugaro.yaml says bitbucket over a
// github.com origin is the github checkout it was recorded as, and a path this
// machine remembers under bitbucket/... is another repository than that
// directory is, rather than a repository no provider serves.
//
// Mutation (run, restore): read co.Provider first in checkoutProvider, as the
// code did before the review, and the first path below is reported
// other_repository although its origin is the very one it was recorded under.
func TestConfigListValidatesAgainstTheOriginsProvider(t *testing.T) {
	dir := checkoutsFixture(t)
	// The file says bitbucket, the origin is github.com: a copied config, or
	// a checkout re-pointed at another forge.
	const saysBitbucket = "version: 1\nproject: aurora\ngit: { provider: bitbucket, base_branch: main }\n"
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), saysBitbucket, "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
	// A second path remembered under the file's provider, which no origin
	// here gives: hand-written state, or state an older fugaro wrote.
	other := checkoutsAt(t, filepath.Join(dir, "src", "other"), saysBitbucket, "https://github.com/acme/other.git")
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "bitbucket", Repo: "acme/other"}, other, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}

	doc, _ := configListJSON(t)
	got := map[string]configListCheckout{}
	for _, c := range configListCheckoutsOf(t, doc, "aurora") {
		got[c.Repository] = c
	}
	if c, ok := got["github/acme/app"]; !ok || !c.Valid || c.Reason != "" {
		t.Errorf("github/acme/app = %+v (present %v), want valid with no reason: its origin is the one it was recorded under", c, ok)
	}
	if c, ok := got["bitbucket/acme/other"]; !ok || c.Valid || c.Reason != checkoutOtherRepository {
		t.Errorf("bitbucket/acme/other = %+v (present %v), want valid false and reason %q: its origin is on no provider's host", c, ok, checkoutOtherRepository)
	}
}

// TestConfigListReportsASymlinkLoop: a path that resolves to nothing — a
// symlink loop, a broken link, a dangling mount — is a path that is not
// there, and the read has to say so rather than fail.
func TestConfigListReportsASymlinkLoop(t *testing.T) {
	dir := checkoutsFixture(t)
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	checkoutsRemember(t, "aurora", loop, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	c := configListCheckoutsOf(t, doc, "aurora")[0]
	if c.Valid || c.Reason != checkoutMissing {
		t.Errorf("the symlink loop = %+v, want valid false and reason %q", c, checkoutMissing)
	}
}

// TestConfigListReportsAPathThatIsNoLongerACheckout: the path is still a
// directory, but it is not a git checkout's own root any more — the shape a
// moved, re-cloned or re-created directory leaves in the state file, and the
// one a listing that only stat'd the path would call valid.
//
// The reason code is its own (not_a_checkout, not missing): the console shows
// the operator a directory that is there and says what is wrong with it, and
// "missing" would send them looking for something that is not.
//
// Mutation (run, restore): return checkoutMissing from validateCheckoutPath's
// `co == nil || filepath.Clean(path) != co.Root` branch and every subtest here
// fails, while the rest of this file stays green.
func TestConfigListReportsAPathThatIsNoLongerACheckout(t *testing.T) {
	t.Run("the path is now inside another checkout", func(t *testing.T) {
		dir := checkoutsFixture(t)
		// A checkout of this project and repository, recorded at one of its
		// subdirectories: git resolves the toplevel for the path, and the
		// toplevel is not the path, so the path is a checkout of nothing.
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
		sub := filepath.Join(co, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		checkoutsRemember(t, "aurora", sub, "https://github.com/acme/app.git")

		doc, _ := configListJSON(t)
		c := configListCheckoutsOf(t, doc, "aurora")[0]
		if c.Valid || c.Reason != checkoutNotACheckout || c.Path != sub {
			t.Errorf("the subdirectory = %+v, want valid false, reason %q and the path kept", c, checkoutNotACheckout)
		}
	})
	t.Run("the checkout's git directory is gone", func(t *testing.T) {
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
		checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
		if err := os.RemoveAll(filepath.Join(co, ".git")); err != nil {
			t.Fatal(err)
		}

		doc, _ := configListJSON(t)
		c := configListCheckoutsOf(t, doc, "aurora")[0]
		if c.Valid || c.Reason != checkoutNotACheckout {
			t.Errorf("the de-gitted directory = %+v, want valid false and reason %q", c, checkoutNotACheckout)
		}
	})
	t.Run("the checkout's fugaro.yaml is gone", func(t *testing.T) {
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
		checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
		if err := os.Remove(filepath.Join(co, "fugaro.yaml")); err != nil {
			t.Fatal(err)
		}

		doc, _ := configListJSON(t)
		c := configListCheckoutsOf(t, doc, "aurora")[0]
		if c.Valid || c.Reason != checkoutNotACheckout {
			t.Errorf("the checkout with no fugaro.yaml = %+v, want valid false and reason %q", c, checkoutNotACheckout)
		}
	})
}

// TestConfigListReportsACheckoutItCannotRead: the path is a git checkout's own
// root, and its fugaro.yaml is one this command cannot read — a file that is
// not a regular file, or one whose project: is not a string. That is neither
// "missing" nor "not a checkout": the checkout is there and the file is what
// is wrong, which is the one of the two things an operator can go and fix.
//
// Mutation (run, restore): return checkoutMissing from validateCheckoutPath's
// resolveCheckout-error branch and both subtests fail.
func TestConfigListReportsACheckoutItCannotRead(t *testing.T) {
	t.Run("fugaro.yaml is not a regular file", func(t *testing.T) {
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
		checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
		if err := os.Remove(filepath.Join(co, "fugaro.yaml")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(co, "fugaro.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}

		doc, _ := configListJSON(t)
		c := configListCheckoutsOf(t, doc, "aurora")[0]
		if c.Valid || c.Reason != checkoutUnreadable {
			t.Errorf("the checkout with a directory for fugaro.yaml = %+v, want valid false and reason %q", c, checkoutUnreadable)
		}
	})
	t.Run("project: is not a string", func(t *testing.T) {
		dir := checkoutsFixture(t)
		co := checkoutsAt(t, filepath.Join(dir, "src", "app"), "version: 1\nproject: 7\n", "https://github.com/acme/app.git")
		checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")

		doc, _ := configListJSON(t)
		c := configListCheckoutsOf(t, doc, "aurora")[0]
		if c.Valid || c.Reason != checkoutUnreadable {
			t.Errorf("the checkout whose project: is a number = %+v, want valid false and reason %q", c, checkoutUnreadable)
		}
	})
}

// TestConfigListPrintsNothingTheDiskCanDrive: every string in the document
// comes from a file this process did not write, so every one of them goes
// through the printable escaping and the length bound — a control character,
// a bidi override or a path longer than any machine has is data, not output.
func TestConfigListPrintsNothingTheDiskCanDrive(t *testing.T) {
	checkoutsFixture(t)
	// Hand-written state, the way a state file that is not ours arrives:
	// the identity carries a right-to-left override and the path a bell, an
	// escape and 400 bytes of padding.
	long := "/src/" + strings.Repeat("x", 400)
	body := `{"version": 1, "projects": {"aurora": {"github/acme‮app": {` +
		`"provider": "github", "repo": "acme‮app", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "/src/\u0007app", "recorded_at": "2026-10-20T00:00:00Z"},` +
		`{"path": "` + long + `", "recorded_at": "2026-10-20T00:00:00Z"}]}}}}` + "\n"
	configListWriteState(t, body)

	doc, out := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 2 {
		t.Fatalf("checkouts = %+v, want the hand-written two", got)
	}
	// The escaping is Printable's: a character that must not reach a terminal
	// becomes a backslash, a "u" and the four hex digits of its code point.
	// The code points are spelled as rune literals so this file carries no
	// invisible character of its own.
	escaped := func(r rune) string { return "\\" + "u" + fmt.Sprintf("%04x", r) }
	if repo, want := got[0].Repository, "github/acme"+escaped('‮')+"app"; repo != want {
		t.Errorf("the repository = %q, want %q (the bidi override escaped)", repo, want)
	}
	if p, want := got[0].Path, "/src/"+escaped('\u0007')+"app"; p != want {
		t.Errorf("the path = %q, want %q (the bell escaped)", p, want)
	}
	if p := got[1].Path; !strings.HasSuffix(p, "... (truncated)") || len(p) > 250 {
		t.Errorf("the over-long path = %q, want it bounded and marked", p)
	}
	// Nothing in either form carries a control or format character: what a
	// terminal does with one is not this command's to decide.
	text, _, err := execute(t, "config", "list")
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

// TestConfigListReportsAStateFileItCannotRead: a state file that cannot be
// read is a warning and no checkouts — never "no checkouts" silently, and
// never a listing that fails: the installations are facts of the directory
// and are still printed.
func TestConfigListReportsAStateFileItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":   "this is not json\n",
		"version 2":  `{"version": 2, "projects": {}}` + "\n",
		"over bound": `{"version": 1, "projects": {}}` + strings.Repeat(" ", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			checkoutsFixture(t)
			path := configListWriteState(t, body)
			doc, _ := configListJSON(t)
			if len(doc.Installations) != 2 {
				t.Errorf("installations = %+v, want them still listed", doc.Installations)
			}
			for _, inst := range doc.Installations {
				if len(inst.Checkouts) != 0 {
					t.Errorf("%s carries checkouts from a file that could not be read: %+v", inst.Name, inst.Checkouts)
				}
			}
			if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "checkouts") {
				t.Errorf("warnings = %q, want the unreadable state file named", doc.Warnings)
			}
			text, _, err := execute(t, "config", "list")
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

// TestConfigListKeepsTheBounds: what the document shows is what the state
// file holds — at most 20 repositories per project and 5 paths per repository
// — and the entries it drops are the oldest, which the write path dropped
// before this read ever saw them. The bound is the reason the console can
// render this field without a limit of its own.
func TestConfigListKeepsTheBounds(t *testing.T) {
	checkoutsFixture(t)
	for r := range localcfg.MaxCheckoutRepos + 2 {
		repo := "acme/app-" + string(rune('a'+r))
		for i := range localcfg.MaxCheckoutPaths + 1 {
			if err := localcfg.RecordCheckout(os.Getenv, "aurora",
				localcfg.CheckoutIdentity{Provider: "github", Repo: repo},
				"/src/"+repo+"/"+string(rune('a'+i)), time.Now().Add(time.Duration(r*10+i)*time.Second), checkoutTempRoots()); err != nil {
				t.Fatal(err)
			}
		}
	}
	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != localcfg.MaxCheckoutRepos*localcfg.MaxCheckoutPaths {
		t.Fatalf("checkouts = %d, want %d repositories of %d paths",
			len(got), localcfg.MaxCheckoutRepos, localcfg.MaxCheckoutPaths)
	}
	seen := map[string]int{}
	for _, c := range got {
		seen[c.Repository]++
	}
	if len(seen) != localcfg.MaxCheckoutRepos {
		t.Errorf("repositories = %d, want the %d-repository bound", len(seen), localcfg.MaxCheckoutRepos)
	}
	for repo, n := range seen {
		if n != localcfg.MaxCheckoutPaths {
			t.Errorf("%s has %d paths, want the %d-path bound", repo, n, localcfg.MaxCheckoutPaths)
		}
	}
	if _, ok := seen["github/acme/app-a"]; ok {
		t.Errorf("checkouts = %+v, want the oldest repository dropped", seen)
	}
	if _, ok := seen["github/acme/app-"+string(rune('a'+localcfg.MaxCheckoutRepos+1))]; !ok {
		t.Errorf("checkouts = %+v, want the newest repository kept", seen)
	}
}

// TestConfigListListsTheCheckoutsOfAConfigItCannotParse: an installation
// whose config cannot be read still carries its checkouts — where this
// machine has a checkout is a fact of the machine, not of the file that could
// not be parsed, so the row is still listed with its path. What it does not
// carry is a verdict: enrolment is a fact of that config, so a config that
// cannot be read enrolls nothing and every row of it is not_enrolled and
// invalid. The path is still printed — a reader that wants to show the
// operator where their work is can — but nothing may act on it.
func TestConfigListListsTheCheckoutsOfAConfigItCannotParse(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
	if err := os.WriteFile(filepath.Join(dir, "xdg", "fugaro", "projects", "aurora.yaml"),
		[]byte("version: 1\nname: aurora\ngcp_project: [not a string\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, _ := configListJSON(t)
	if len(doc.Installations) != 2 {
		t.Fatalf("installations = %+v, want both names", doc.Installations)
	}
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 1 || got[0].Path != co {
		t.Errorf("aurora's checkouts = %+v, want the remembered path despite the unreadable config", got)
	}
	if len(got) == 1 && (got[0].Valid || got[0].Enrolled || got[0].Reason != checkoutNotEnrolled) {
		t.Errorf("the row of an unreadable config = %+v, want enrolled false, valid false and reason %q", got[0], checkoutNotEnrolled)
	}
	if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "aurora") {
		t.Errorf("warnings = %q, want the unreadable config named", doc.Warnings)
	}
}

// TestConfigListWithNoInstallations: an empty list is a fact, not an error —
// the console's picker must be able to say "there are none" — and the text
// form says what to run.
func TestConfigListWithNoInstallations(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"installations": []`) || !strings.Contains(out, `"warnings": []`) {
		t.Errorf("an empty listing is not two empty arrays (never a null):\n%s", out)
	}
	text, _, err := execute(t, "config", "list")
	if err != nil || !strings.Contains(text, "fugaro setup") {
		t.Errorf("text %q, %v", text, err)
	}
}

// TestConfigListReportsAnUnreadableConfig: an installation whose config
// cannot be read is listed by name with a warning, never dropped — a picker
// that offered a name and then failed silently would be worse than one that
// says what it could not read.
func TestConfigListReportsAnUnreadableConfig(t *testing.T) {
	_, _, bramble := configListFixture(t)
	if err := os.WriteFile(bramble, []byte("version: 1\nname: bramble\ngcp_project: [not a string\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Installations) != 2 {
		t.Fatalf("installations = %+v, want both names", doc.Installations)
	}
	var b *configListInstallation
	for i := range doc.Installations {
		if doc.Installations[i].Name == "bramble" {
			b = &doc.Installations[i]
		}
	}
	if b == nil || b.GCPProject != "" {
		t.Fatalf("bramble = %+v, want it listed with nothing read", b)
	}
	if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "bramble") {
		t.Errorf("warnings = %q, want the unreadable config named", doc.Warnings)
	}
}

// treeState is every file under root with its size, mode, mtime and content,
// so a read that wrote, touched or removed anything shows up as a difference.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		data := ""
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			data = string(b)
		}
		state[p] = fmt.Sprintf("%v %d %d %q", fi.Mode(), fi.Size(), fi.ModTime().UnixNano(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// TestConsoleV2ReadsWriteNothing: `config list` and `plan graph` are reads the
// console spawns, and a read the console spawns changes nothing — not the
// project configs, not the shared-config cache, not the checkout. Every file
// under the config home, the cache home and the plan's directory is the same
// after both commands, in both output forms.
func TestConsoleV2ReadsWriteNothing(t *testing.T) {
	dir, _, _ := configListFixture(t)
	if err := localcfg.SaveSharedCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp", Bucket: "fugaro-runs-aurora-gcp",
		Generation: 7, CheckedAt: time.Now().Add(-30 * time.Hour).UTC(), YAML: "name: aurora\ngcp_project: aurora-gcp\n",
	}); err != nil {
		t.Fatal(err)
	}
	planDir, planPath := planGraphFixture(t, planGraphDoc)
	before, beforePlan := treeState(t, dir), treeState(t, planDir)
	for _, args := range [][]string{
		{"config", "list", "--json"}, {"config", "list"},
		{"plan", "graph", "--plan", planPath, "--json"}, {"plan", "graph", "--plan", planPath},
	} {
		if _, _, err := execute(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if after := treeState(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("config list or plan graph changed the config or cache home:\nbefore %v\nafter  %v", before, after)
	}
	if after := treeState(t, planDir); !reflect.DeepEqual(beforePlan, after) {
		t.Errorf("plan graph changed the checkout:\nbefore %v\nafter  %v", beforePlan, after)
	}
}

// TestConfigListReportsACheckoutTheProjectDoesNotEnroll: a directory that IS
// a checkout of the project it claims — its fugaro.yaml says aurora, its
// origin is a real repository of that name — but that the project's own
// config does not enroll is not one of this installation's checkouts. The
// claim is the checkout's; the enrolment is the operator's.
//
// This is the probe an independent review ran: a fork, a third-party
// repository or a hostile pull request copies `project: aurora` into its
// fugaro.yaml, and a listing that took the claim for enrolment would hand the
// console a path to offer. The row is still printed — where the machine has
// this directory is a fact — but it is never valid and never usable.
//
// Mutation (run, restore): drop the `!enrolled` check from
// validateCheckoutPath (return valid with no reason) and the first row below
// is valid: the claim is taken for enrolment again.
func TestConfigListReportsACheckoutTheProjectDoesNotEnroll(t *testing.T) {
	dir := checkoutsFixture(t)
	// Not in aurora's repos: nothing the fixture enrolls.
	evil := checkoutsAt(t, filepath.Join(dir, "src", "evil"), checkoutsProjectYAML("aurora"), "https://github.com/acme/evil.git")
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "github", Repo: "acme/evil"}, evil, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	// The control: the same fixture's enrolled repository, same run.
	app := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	checkoutsRemember(t, "aurora", app, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	got := map[string]configListCheckout{}
	for _, c := range configListCheckoutsOf(t, doc, "aurora") {
		got[c.Repository] = c
	}
	if c, ok := got["github/acme/evil"]; !ok || c.Valid || c.Enrolled || c.Reason != checkoutNotEnrolled {
		t.Errorf("github/acme/evil = %+v (present %v), want the path listed, enrolled false, valid false and reason %q", c, ok, checkoutNotEnrolled)
	} else if c.Path != evil {
		t.Errorf("github/acme/evil's path = %q, want %q: the row is still shown, it is only never valid", c.Path, evil)
	}
	if c, ok := got["github/acme/app"]; !ok || !c.Valid || !c.Enrolled || c.Reason != "" {
		t.Errorf("github/acme/app = %+v (present %v), want enrolled and valid: the fixture enrolls it", c, ok)
	}
	if !strings.Contains(configListState(t), "acme/evil") {
		t.Errorf("the state file no longer holds the un-enrolled row:\n%s", configListState(t))
	}
}

// TestConfigListReportsAPathItCannotSpell: a path that printing changes — a
// relative one, one carrying a control character, one so long it is truncated
// — and an identity that is not a repository at all are reported as
// not_representable. The string in the document is not the string on the
// machine, so a reader that used it would use a directory that is not the one
// the row is about.
//
// Mutation (run, restore): drop `filepath.IsAbs(p.Path)` from the
// representability test and the relative row becomes `missing` instead —
// the read then touches the filesystem with a string that names nothing.
// Drop `validIdentity(id)` and the bad-identity row is `missing` too, for the
// same reason and one stat further along.
func TestConfigListReportsAPathItCannotSpell(t *testing.T) {
	checkoutsFixture(t)
	// Hand-written state: the write path refuses every one of these
	// (RecordCheckout), which is exactly why the read has to.
	long := "/src/" + strings.Repeat("x", 400)
	body := `{"version": 1, "projects": {"aurora": {` +
		`"github/acme/rel": {"provider": "github", "repo": "acme/rel", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "src/rel", "recorded_at": "2026-10-20T00:00:00Z"}]},` +
		`"github/acme/ctl": {"provider": "github", "repo": "acme/ctl", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "/src/\u0007ctl", "recorded_at": "2026-10-20T00:00:00Z"}]},` +
		`"github/acme/trunc": {"provider": "github", "repo": "acme/trunc", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "` + long + `", "recorded_at": "2026-10-20T00:00:00Z"}]},` +
		`"fake/acme/bad": {"provider": "fake", "repo": "not a repo", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "/src/bad", "recorded_at": "2026-10-20T00:00:00Z"}]}}}}` + "\n"
	configListWriteState(t, body)

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 4 {
		t.Fatalf("checkouts = %+v, want the four hand-written rows", got)
	}
	for _, c := range got {
		if c.Valid || c.Reason != checkoutNotRepresentable {
			t.Errorf("%s = %+v, want valid false and reason %q", c.Repository, c, checkoutNotRepresentable)
		}
	}
}

// TestConfigListReportsACheckoutRecordedUnderALinkedSpellingOfItsPath: a
// state file can hold a spelling of a path that is not the one git resolves
// for it — an older fugaro's, a hand-edited one's, or a machine whose temp
// directory is behind a symlink (darwin's /var → /private/var) — and it is
// still the same directory. The row must be the valid checkout it is, not
// "not_a_checkout" because two spellings of one path differ as strings.
//
// Mutation (run, restore): make sameCheckoutRoot compare the cleaned strings
// only (drop the two EvalSymlinks arms) and this fails with the row
// not_a_checkout although the directory is the very checkout it names.
func TestConfigListReportsACheckoutRecordedUnderALinkedSpellingOfItsPath(t *testing.T) {
	dir := checkoutsFixture(t)
	real := checkoutsAt(t, filepath.Join(dir, "src", "real"), checkoutsProjectYAML("aurora"), "https://github.com/acme/app.git")
	link := filepath.Join(dir, "src", "link")
	if err := os.Symlink(filepath.Dir(real), link); err != nil {
		t.Skipf("this machine cannot make a symlink: %v", err)
	}
	// The path as the operator's shell spelled it on the way in: through the
	// link. git resolves it to the same toplevel; the state file holds the
	// spelling.
	aliased := filepath.Join(link, filepath.Base(real))
	checkoutsRemember(t, "aurora", aliased, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 1 {
		t.Fatalf("checkouts = %+v, want the one remembered path", got)
	}
	if c := got[0]; !c.Valid || c.Reason != "" {
		t.Errorf("the aliased path = %+v, want valid with no reason: %s and %s are one directory", c, aliased, real)
	}
}

// TestConfigListDistrustsARewrittenOrigin: a checkout whose raw .git/config
// names a different host or repository than the one git itself resolves for
// "origin" (url.<base>.insteadOf) must not be re-validated against what the
// rewrite resolves to. The rewrite is a local config line that can point
// anywhere; the raw origin is the one the operator's checkout actually
// records, and the review's probe is a row that goes valid because the reader
// followed the rewrite instead of refusing it.
//
// Mutation (run, restore): change `if !ok || oi.Rewritten` to `if !ok` in
// validateCheckoutPath and this fails: the row is valid with no reason.
func TestConfigListDistrustsARewrittenOrigin(t *testing.T) {
	dir := checkoutsFixture(t)
	// The raw .git/config says gh:acme/app.git, git resolves it through the
	// rewrite to the github repository the row was recorded as — so the
	// rewritten origin is the ONLY thing keeping this row invalid. (A rewrite
	// that resolved to another host would be caught by the provider rule even
	// with this guard gone, and would prove nothing about it.)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsProjectYAML("aurora"), "gh:acme/app.git")
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")
	testutil.Git(t, co, "config", "--local", "--add",
		"url.https://github.com/acme/app.git.insteadOf", "gh:acme/app.git")
	if oi, ok := readOrigin(context.Background(), co); !ok || !oi.Rewritten {
		t.Fatalf("the fixture did not produce a rewritten origin: %+v ok=%v", oi, ok)
	}

	doc, _ := configListJSON(t)
	c := configListCheckoutsOf(t, doc, "aurora")[0]
	if c.Valid || c.Reason != checkoutOtherRepository {
		t.Errorf("the rewritten-origin checkout = %+v, want valid false and reason %q", c, checkoutOtherRepository)
	}
}

// TestConfigListWarnsWhenTheStateFileIsOverItsReadBounds: the state file this
// read opens is one this process did not write — a hand-edited one, or one a
// machine that has since cloned far more wrote — and each row costs a stat
// and about three git executions. The bounds the write keeps are therefore
// applied again here (localcfg.Bounded: 20 repositories per project, 5 paths
// each, 1000 rows in the document), and what they cut is named in warnings
// rather than silently presented as the whole file.
//
// Mutation (run, restore): pass `known` instead of `known.Bounded(names)` in
// buildConfigList and this fails with all 22 rows listed and no warning.
func TestConfigListWarnsWhenTheStateFileIsOverItsReadBounds(t *testing.T) {
	checkoutsFixture(t)
	// Hand-written, because the write path would have dropped the oldest
	// (RecordCheckout): this is the file a reader has to survive, not the
	// file a writer produces.
	const n = 22
	var b strings.Builder
	b.WriteString(`{"version": 1, "projects": {"aurora": {`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		repo := fmt.Sprintf("acme/app-%02d", i)
		fmt.Fprintf(&b, `%q: {"provider": "github", "repo": %q, "recorded_at": "2026-10-20T00:%02d:00Z", "paths": [{"path": "/src/%s", "recorded_at": "2026-10-20T00:%02d:00Z"}]}`,
			"github/"+repo, repo, i, repo, i)
	}
	b.WriteString("}}}\n")
	configListWriteState(t, b.String())

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != localcfg.MaxCheckoutRepos {
		t.Fatalf("checkouts = %d, want the %d-repository read bound", len(got), localcfg.MaxCheckoutRepos)
	}
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.Repository] = true
	}
	if newest := fmt.Sprintf("github/acme/app-%02d", n-1); !seen[newest] {
		t.Errorf("checkouts = %+v, want the newest repository %s kept", seen, newest)
	}
	if oldest := "github/acme/app-00"; seen[oldest] {
		t.Errorf("checkouts = %+v, want the oldest repository %s dropped", seen, oldest)
	}
	if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "checkouts") {
		t.Errorf("warnings = %q, want the over-bound state file named", doc.Warnings)
	}
	text, _, err := execute(t, "config", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "checkouts") {
		t.Errorf("the text form says nothing about the trimmed state file:\n%s", text)
	}
}

// TestConfigListRejectsARowWhoseHostIsNoProviders: a state file can hold a
// row for a host no provider lives on — an older fugaro's, a hand-written
// one's, or one written before the recorder stopped taking the checkout's own
// git.provider as evidence. The read must not name such a row valid: the pair
// (provider, repository) is what the console builds a provider-host link
// from, and a provider's name is only true of that provider's own host.
//
// Mutation (run, restore): put the `co.Provider` fallback back into the
// provider lookup and this fails: the row is valid, because the checkout's
// own file says github.
func TestConfigListRejectsARowWhoseHostIsNoProviders(t *testing.T) {
	dir := checkoutsFixture(t)
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"),
		"version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n",
		"https://git.evil.example/acme/app.git")
	// Recorded as the enrolled github/acme/app, by hand: the write path
	// refuses this now, which is why the read has to.
	checkoutsRemember(t, "aurora", co, "https://github.com/acme/app.git")

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 1 {
		t.Fatalf("checkouts = %+v, want the one remembered path", got)
	}
	if c := got[0]; c.Valid || c.Reason != checkoutOtherRepository {
		t.Errorf("the row for a host no provider lives on = %+v, want valid false and reason %q", c, checkoutOtherRepository)
	}
}

// TestConfigListReportsThePathBeforeTheEnrolment: an installation whose config
// cannot be read enrolls nothing, and every row of it is invalid — but the
// reason a row carries is the first thing wrong with the *path*, and
// `not_enrolled` is what a row whose path is otherwise sound gets. A stale
// path under an unreadable config is `missing`, not `not_enrolled`: the row
// says what is wrong with the directory the operator can see, and the
// enrolment is reported by its own field.
//
// Mutation (run, restore): move the `!enrolled` check to the top of
// validateCheckoutPath and this fails with reason "not_enrolled" for a path
// that is gone.
func TestConfigListReportsThePathBeforeTheEnrolment(t *testing.T) {
	dir := checkoutsFixture(t)
	checkoutsRemember(t, "aurora", filepath.Join(dir, "gone", "app"), "https://github.com/acme/app.git")
	if err := os.WriteFile(filepath.Join(dir, "xdg", "fugaro", "projects", "aurora.yaml"),
		[]byte("version: 1\nname: aurora\ngcp_project: [not a string\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, _ := configListJSON(t)
	got := configListCheckoutsOf(t, doc, "aurora")
	if len(got) != 1 {
		t.Fatalf("checkouts = %+v, want the remembered path", got)
	}
	if c := got[0]; c.Valid || c.Enrolled || c.Reason != checkoutMissing {
		t.Errorf("the stale row of an unreadable config = %+v, want enrolled false, valid false and reason %q", c, checkoutMissing)
	}
	if len(doc.Warnings) == 0 || !strings.Contains(strings.Join(doc.Warnings, "\n"), "aurora") {
		t.Errorf("warnings = %q, want the unreadable config named", doc.Warnings)
	}
}
