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

	"github.com/dimipaun/fugaro/internal/localcfg"
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
	want := []string{"name", "gcp_project", "region", "shared_merged", "shared_checked_at"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configListInstallation fields = %v, want exactly the named set %v", got, want)
	}
	// The projects directory is not in the document either: the console
	// picks from names, and the operator's config path is theirs.
	if strings.Contains(out, filepath.Join(dir, "xdg")) {
		t.Errorf("config list --json carries the config directory:\n%s", out)
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
