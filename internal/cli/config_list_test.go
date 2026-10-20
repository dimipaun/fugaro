package cli

import (
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
	want := []string{"name", "gcp_project", "region", "shared_merged", "shared_checked_at", "checkouts"}
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
	// all four: reason is omitempty, so a valid row carries three keys and a
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
			if want := []string{"path", "repository", "valid"}; !reflect.DeepEqual(k, want) {
				t.Errorf("a valid row's keys = %v, want %v (reason is omitted when there is none)", k, want)
			}
			continue
		}
		if !reflect.DeepEqual(k, full) {
			t.Errorf("config list --json installations[].checkouts[] keys = %v, want %v", k, full)
		}
	}

	// The named set, spelled out: the checkout is exactly these four fields,
	// so a localcfg field cannot join the document by a rename or a
	// promotion.
	wantCheckout := []string{"repository", "path", "valid", "reason"}
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
		localcfg.CheckoutIdentity{Provider: "github", Repo: "acme/other-2"}, renamed, time.Now()); err != nil {
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
		localcfg.CheckoutIdentity{Provider: "bitbucket", Repo: "acme/other"}, other, time.Now()); err != nil {
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

// TestConfigListPrintsNothingTheDiskCanDrive: every string in the document
// comes from a file this process did not write, so every one of them goes
// through the printable escaping and the length bound — a control character,
// a bidi override or a path longer than any machine has is data, not output.
func TestConfigListPrintsNothingTheDiskCanDrive(t *testing.T) {
	checkoutsFixture(t)
	// Hand-written state, the way a state file that is not ours arrives:
	// the identity carries a right-to-left override and the path a bell, an
	// escape and 400 bytes of padding.
	path, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	long := "/src/" + strings.Repeat("x", 400)
	body := `{"version": 1, "projects": {"aurora": {"github/acme‮app": {` +
		`"provider": "github", "repo": "acme‮app", "recorded_at": "2026-10-20T00:00:00Z", "paths": [` +
		`{"path": "/src/\u0007app", "recorded_at": "2026-10-20T00:00:00Z"},` +
		`{"path": "` + long + `", "recorded_at": "2026-10-20T00:00:00Z"}]}}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

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
			path, err := localcfg.CheckoutsPath(os.Getenv)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
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
				"/src/"+repo+"/"+string(rune('a'+i)), time.Now().Add(time.Duration(r*10+i)*time.Second)); err != nil {
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
// whose config cannot be read still carries its checkouts. Which directory
// holds this project's checkout is a fact of this machine, not of the file
// that could not be parsed, and it is the one thing a reader can still act
// on.
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
	if len(got) != 1 || !got[0].Valid || got[0].Path != co {
		t.Errorf("aurora's checkouts = %+v, want the remembered path despite the unreadable config", got)
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
	if err != nil || !strings.Contains(text, "fugaro init") {
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
