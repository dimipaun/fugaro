package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsTestDoc and its members mirror the --json document without naming
// the package's own types: the contract is what a consumer sees, and a test
// that decoded into the producer's struct would agree with the producer
// whatever it wrote.
type settingsTestDoc struct {
	Version     int               `json:"version"`
	Project     string            `json:"project"`
	GeneratedAt string            `json:"generated_at"`
	Sources     []settingsTestSrc `json:"sources"`
	Settings    []settingsTestRow `json:"settings"`
}

type settingsTestSrc struct {
	Layer      string `json:"layer"`
	ReadAt     string `json:"read_at"`
	DurationMS int64  `json:"duration_ms"`
	Status     string `json:"status"`
	Note       string `json:"note"`
}

type settingsTestRow struct {
	Key     string  `json:"key"`
	Scope   string  `json:"scope"`
	Value   *string `json:"value"`
	Wins    string  `json:"wins"`
	Default struct {
		Value  string `json:"value"`
		Source string `json:"source"`
		Unset  string `json:"unset"`
	} `json:"default"`
	State     string `json:"state"`
	Sensitive bool   `json:"sensitive"`
	Levels    []struct {
		Layer   string `json:"layer"`
		Value   string `json:"value"`
		Outcome string `json:"outcome"`
	} `json:"levels"`
}

// settingsFixture writes this machine's own state: the project config of
// workspace-a's project aurora, plus whatever extra local config the caller
// appends.
func settingsFixture(t *testing.T, extraLocal string) (home string) {
	t.Helper()
	home = t.TempDir()
	isolateProjects(t, home)
	path := writeProject(t, "aurora", "aurora-gcp")
	// isolateProjects clears FUGARO_CONFIG, so the project is selected by
	// its own file: name the one writeProject just wrote.
	t.Setenv("FUGARO_CONFIG", path)
	if extraLocal != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, []byte(extraLocal)...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// settingsCheckout remembers a checkout of repo under dir, carrying the
// fugaro.yaml the caller wrote.
func settingsCheckout(t *testing.T, dir, repo, fugaroYAML string) string {
	t.Helper()
	path := filepath.Join(dir, "checkouts", strings.ReplaceAll(repo, "/", "-"))
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "fugaro.yaml"), []byte(fugaroYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := localcfg.RecordCheckout(os.Getenv, "aurora", localcfg.CheckoutIdentity{Provider: "github", Repo: repo}, path, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsRepoYAML is a repository's own file, the smallest one config.Resolve
// accepts plus whatever the caller adds.
func settingsRepoYAML(extra string) string {
	return `version: 1
project: aurora
git:
  provider: github
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
` + extra
}

// settingsRepoFromProfile is a repository's own file anchored to the GCP
// project testProjectLayer's cache carries, whose one workflow names profile
// svc and leaves commands.build to it. The resolved config then holds a value
// neither the repository's file nor the layer's defaults wrote — the one origin
// whose WINS word is `profile <name>` and not a layer name.
const settingsRepoFromProfile = `version: 1
project: aurora
gcp_project: proj-1234
git:
  provider: github
workflows:
  web:
    profile: svc
    commands: { test: go test ./... }
`

// settingsJSON runs the read and decodes the document.
func readSettingsJSON(t *testing.T, args ...string) settingsTestDoc {
	t.Helper()
	out, _, err := execute(t, append([]string{"settings", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var doc settingsTestDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return doc
}

// rowOf is the one row of key in scope, or a fatal.
func rowOf(t *testing.T, doc settingsTestDoc, key, scope string) settingsTestRow {
	t.Helper()
	for _, r := range doc.Settings {
		if r.Key == key && r.Scope == scope {
			return r
		}
	}
	t.Fatalf("%s in %s: no such row (the document has %d rows)", key, scope, len(doc.Settings))
	return settingsTestRow{}
}

// TestSettingsPrintsKeyValueWinsDefaultState: the table is the whole contract,
// so its six columns are named and in order, and every row fills them. SCOPE is
// one of them because the same key is a row per scope under --repo and
// --all-repos.
func TestSettingsPrintsKeyValueWinsDefaultState(t *testing.T) {
	settingsFixture(t, "")
	out, _, err := execute(t, "settings", "budget.per_run_usd")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("one key prints its header and its row, got:\n%s", out)
	}
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "KEY SCOPE VALUE WINS DEFAULT STATE" {
		t.Errorf("header = %q, want the six columns", lines[0])
	}
	if fields := strings.Fields(lines[1]); len(fields) < 2 || fields[0] != "budget.per_run_usd" || fields[1] != "project" {
		t.Errorf("row = %q, want the key and then its scope", lines[1])
	}
	if !strings.Contains(lines[1], "default") {
		t.Errorf("row = %q, want the layer that decided it", lines[1])
	}
}

// TestSettingsTableSeparatesTheScopes: the same key is one row per scope, and
// the text table names the scope of each, so two repositories' values are not
// two identical-looking rows.
func TestSettingsTableSeparatesTheScopes(t *testing.T) {
	home := settingsFixture(t, "repos:\n  owner/repo-b: {}\n")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML("agent:\n  review_rounds: 3\n"))
	out, _, err := execute(t, "settings", "agent.review_rounds", "--all-repos")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	seen := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "agent.review_rounds" {
			t.Fatalf("row = %q, want the key first", line)
		}
		if _, dup := seen[fields[1]]; dup {
			t.Errorf("scope %q is printed twice", fields[1])
		}
		seen[fields[1]] = fields[2]
	}
	if len(seen) != 3 {
		t.Fatalf("scopes = %v, want the project and its two repositories", seen)
	}
	if seen["project"] != "2" {
		t.Errorf("the project scope's value = %q, want the default 2", seen["project"])
	}
	if seen["repo:owner/repo-a"] != "3" {
		t.Errorf("the repository scope's value = %q, want the checkout's 3", seen["repo:owner/repo-a"])
	}
	if seen["repo:owner/repo-b"] != "2" {
		t.Errorf("the unchecked-out repository's value = %q, want its default 2", seen["repo:owner/repo-b"])
	}
}

// TestSettingsShowsTheDefaultWhenNoLayerSetsIt: a key set nowhere prints the
// default value, never a blank, and the row says the default decided it.
func TestSettingsShowsTheDefaultWhenNoLayerSetsIt(t *testing.T) {
	settingsFixture(t, "")
	doc := readSettingsJSON(t, "workflows.*.resources.cpu")
	row := rowOf(t, doc, "workflows.*.resources.cpu", "project")
	if row.Wins != "default" {
		t.Errorf("wins = %q, want default", row.Wins)
	}
	if row.Value == nil || *row.Value != row.Default.Value {
		t.Errorf("value = %q, default = %q: a key no layer sets prints the default", deref(row.Value), row.Default.Value)
	}
	if row.Default.Value != "4" || row.Default.Source != "config.DefaultCPU" {
		t.Errorf("default = %+v, want 4 from config.DefaultCPU", row.Default)
	}
}

// TestSettingsChangedShowsOnlyNonDefaults: --changed is the one-screen answer
// to "what have I changed", so a row the default decides is not in it.
func TestSettingsChangedShowsOnlyNonDefaults(t *testing.T) {
	settingsFixture(t, "max_parallel: 3\n")
	doc := readSettingsJSON(t, "--changed")
	if len(doc.Settings) == 0 {
		t.Fatal("--changed printed nothing, and this machine's local config sets max_parallel")
	}
	for _, r := range doc.Settings {
		if r.Wins == "default" {
			t.Errorf("%s: --changed printed a row the default decides", r.Key)
		}
	}
	if row := rowOf(t, doc, "max_parallel", "project"); row.Value == nil || *row.Value != "3" {
		t.Errorf("max_parallel = %v, want the local config's 3", row.Value)
	}
	if row := rowOf(t, doc, "region", "project"); row.Value == nil || *row.Value != "us-east5" {
		t.Errorf("region = %v, want the local config's us-east5", row.Value)
	}
}

// TestSettingsLocalConfigDecidesOnlyTheKeysItWrites: the local file decides a
// key where its text writes it, and no other key. The decoded config is not
// evidence — the loader fills some of it and leaves the rest at the zero value
// — so a read that asked the struct instead of the file printed WINS local for
// the 57 keys of a five-line config, and --changed listed every one of them as
// a change nobody made.
func TestSettingsLocalConfigDecidesOnlyTheKeysItWrites(t *testing.T) {
	settingsFixture(t, "")
	doc := readSettingsJSON(t, "--changed")
	var got []string
	for _, r := range doc.Settings {
		if r.Scope != "project" {
			continue
		}
		got = append(got, r.Key)
		if r.Wins != settings.LayerLocal {
			t.Errorf("%s: wins = %q, and the file writes the key", r.Key, r.Wins)
		}
	}
	// The five keys settingsFixture's own file writes, which is every key of
	// the fixture: the read has nothing else to list.
	want := []string{"gcp_project", "name", "region", "runs_bucket", "version"}
	if !slices.Equal(got, want) {
		t.Errorf("--changed lists %v, and the file writes %v", got, want)
	}
}

// TestSettingsUnsetLocalKeyShowsTheDefault: a local key the file leaves out is
// the default's, and prints what the default says rather than a blank or a
// `null` — a blank would read as "off". The keys here are the shapes the
// decoded struct cannot tell from a written key: an empty string, a zero
// number, a nil map and a nil pointer.
func TestSettingsUnsetLocalKeyShowsTheDefault(t *testing.T) {
	settingsFixture(t, "")
	for _, key := range []string{
		"build.service_account", "terraform.state_bucket", "secret_rotation_days",
		"base_images", "budget.max_run_tokens", "update_check",
	} {
		row := rowOf(t, readSettingsJSON(t, key), key, "project")
		if row.Wins != settings.LayerDefault {
			t.Errorf("%s: wins = %q, want default: the file writes no such key", key, row.Wins)
		}
		want := row.Default.Value
		if want == "" {
			want = row.Default.Unset
		}
		if row.Value == nil || *row.Value != want {
			t.Errorf("%s: value = %q, default = %q: a key no layer sets prints the default", key, deref(row.Value), want)
		}
	}
}

// TestSettingsLocalZeroIsAValueTheFileWrote: the other half of the same rule —
// a key the file writes as the zero value is the file's, so `secret_rotation_days:
// 0` is a setting and not an absent key. The read asks the text for exactly
// this reason: a rule that called every zero field unset would get it wrong.
func TestSettingsLocalZeroIsAValueTheFileWrote(t *testing.T) {
	settingsFixture(t, "secret_rotation_days: 0\n")
	row := rowOf(t, readSettingsJSON(t, "secret_rotation_days"), "secret_rotation_days", "project")
	if row.Wins != settings.LayerLocal {
		t.Errorf("wins = %q, want local: the file writes the key", row.Wins)
	}
	if deref(row.Value) != "0" {
		t.Errorf("value = %q, want the 0 the file wrote", deref(row.Value))
	}
}

// TestSettingsLoaderFilledValuesShowAsDefaults: max_parallel and
// build.machine_type are the two keys the local loader fills for a file that
// sets them nowhere, so a run gets them whatever the file says. Their rows
// print the value a run gets, as the default, with the constant that holds it
// (spec §2.4) — the read called them local, which is a layer that never wrote
// them, and --changed listed them as changes nobody made.
func TestSettingsLoaderFilledValuesShowAsDefaults(t *testing.T) {
	settingsFixture(t, "")
	for _, tc := range []struct{ key, value, constant string }{
		{"max_parallel", "20", "localcfg.DefaultMaxParallel"},
		{"build.machine_type", "E2_HIGHCPU_8", "localcfg.DefaultBuildMachineType"},
	} {
		row := rowOf(t, readSettingsJSON(t, tc.key), tc.key, "project")
		if row.Wins != settings.LayerDefault {
			t.Errorf("%s: wins = %q, want default: the file writes no such key", tc.key, row.Wins)
		}
		if deref(row.Value) != tc.value {
			t.Errorf("%s: value = %q, want the value a run gets, %q", tc.key, deref(row.Value), tc.value)
		}
		if row.Default.Value != tc.value || row.Default.Source != tc.constant {
			t.Errorf("%s: default = %q from %q, want %q from %q", tc.key, row.Default.Value, row.Default.Source, tc.value, tc.constant)
		}
	}
}

// TestSettingsUnknownKeyNamesTheNearest: a typo is a refusal that names the
// key it was meant to be, not an empty table.
func TestSettingsUnknownKeyNamesTheNearest(t *testing.T) {
	settingsFixture(t, "")
	out, _, err := execute(t, "settings", "budget.per_run_uds")
	if err == nil {
		t.Fatalf("an unknown key is refused, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "budget.per_run_usd") {
		t.Errorf("refusal = %q, want the nearest registry name", err.Error())
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitUserError)
	}
}

// TestSettingsRepoLayerReadsTheRememberedCheckout: the repository scope is
// read from the checkout this machine remembers for it, so a value the
// repository's own fugaro.yaml sets wins there.
func TestSettingsRepoLayerReadsTheRememberedCheckout(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML("agent:\n  review_rounds: 3\n"))
	doc := readSettingsJSON(t, "agent.review_rounds", "--repo", "owner/repo-a")
	row := rowOf(t, doc, "agent.review_rounds", "repo:owner/repo-a")
	if row.Value == nil || *row.Value != "3" {
		t.Fatalf("value = %q, wins = %q, state = %q, want the checkout's 3", deref(row.Value), row.Wins, row.State)
	}
	if row.Wins != "repo" {
		t.Errorf("wins = %q, want repo", row.Wins)
	}
	if row.State != "ok" {
		t.Errorf("state = %q, want ok", row.State)
	}
}

// deref is a row's value for a failure message.
func deref(p *string) string {
	if p == nil {
		return "<absent>"
	}
	return *p
}

// TestSettingsAllReposShowsUnknownWithoutACheckout: a repository with no
// checkout on this machine is not guessed at (the rigor design's D839).
func TestSettingsAllReposShowsUnknownWithoutACheckout(t *testing.T) {
	settingsFixture(t, "repos:\n  owner/repo-b: {}\n")
	doc := readSettingsJSON(t, "agent.review_rounds", "--all-repos")
	row := rowOf(t, doc, "agent.review_rounds", "repo:owner/repo-b")
	if row.State != "unknown (no checkout on this machine)" {
		t.Errorf("state = %q, want the unknown sentence", row.State)
	}
	if row.Value == nil || *row.Value != row.Default.Value {
		t.Errorf("value = %q, default = %q: a repository that was not read is not guessed", deref(row.Value), row.Default.Value)
	}
}

// TestSettingsWinsNamesALayerTheRowHas: WINS is either `default` or a layer the
// key's own row says it may be set in — `profile <name>` being the profile
// level's own spelling — so the column can never name a store that could not
// have decided the value. The read used to print WINS env for update_check — a
// layer that row does not carry, and printed precisely when the environment was
// not what decided the value.
func TestSettingsWinsNamesALayerTheRowHas(t *testing.T) {
	home := settingsFixture(t, "max_parallel: 3\n")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoFromProfile)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now(), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	doc := readSettingsJSON(t, "--all-repos")
	won, profile, scopes := 0, 0, map[string]bool{}
	for _, r := range doc.Settings {
		if r.Wins == "default" {
			continue
		}
		won++
		scopes[r.Scope] = true
		row, ok := settings.Lookup(r.Key)
		if !ok {
			t.Errorf("%s: the document carries a key the registry does not", r.Key)
			continue
		}
		layer := r.Wins
		if strings.HasPrefix(layer, "profile ") {
			layer, profile = settings.LayerProfile, profile+1
		}
		if !slices.Contains(row.Layers, layer) {
			t.Errorf("%s (%s): wins = %q, and the row's layers are %v", r.Key, r.Scope, r.Wins, row.Layers)
		}
	}
	if won == 0 {
		t.Fatal("no row was decided by a layer, so this test checked nothing")
	}
	// Both scopes, and a profile-decided row among them: a read that answered
	// the repository scope from the project scope's layers would leave the
	// profile spelling unchecked, and the mapping above is what makes it
	// checkable at all.
	if !scopes["project"] || !scopes["repo:owner/repo-a"] {
		t.Errorf("the rows a layer decided are in %v, want both scopes", scopes)
	}
	if profile == 0 {
		t.Error("no row's WINS named a profile, so the profile spelling is unchecked")
	}
}

// TestSettingsValueAtReadsANamedStringKeyedMap: a path element is text, so a
// map is addressable by a string key whatever the key type is *named* —
// config.AB's exchange_rate is map[rigor.Mode]float64, and a comparison of the
// two reflect.Types would refuse "poc" there and report the key absent, which
// is a row printing its default while the layer sets it.
func TestSettingsValueAtReadsANamedStringKeyedMap(t *testing.T) {
	doc := config.AB{ExchangeRate: map[rigor.Mode]float64{rigor.POC: 0.25}}
	if got, ok := valueAt(doc, "exchange_rate.poc"); !ok || got != "0.25" {
		t.Errorf("exchange_rate.poc = %q, %v: want 0.25, true", got, ok)
	}
	if _, ok := valueAt(doc, "exchange_rate.pro"); ok {
		t.Error("exchange_rate.pro: a mode the map does not carry reads as present")
	}
}

// TestSettingsOfflineReadsNoCloudLayer: --offline is the flag's promise — no
// bucket, no database — and the read keeps it by not entering its cloud half
// at all. The seam reports every call it gets and fails it, so a cloud layer
// that starts reading before the gate is in place reds this test rather than
// reaching a network. The second half proves the seam is on the read's path:
// a flag that guarded nothing would leave a command that never reads the cloud
// layers green too.
func TestSettingsOfflineReadsNoCloudLayer(t *testing.T) {
	settingsFixture(t, "")
	real := settingsCloudLayers
	t.Cleanup(func() { settingsCloudLayers = real })
	calls := 0
	settingsCloudLayers = func(context.Context, *settingsRead, settingsOptions) error {
		calls++
		return errors.New("the read left this machine")
	}

	if out, _, err := execute(t, "settings", "--offline", "--all-repos", "--json"); err != nil {
		t.Fatalf("--offline read a cloud layer: %v\n%s", err, out)
	}
	if calls != 0 {
		t.Errorf("--offline made %d cloud reads, want none", calls)
	}
	if _, _, err := execute(t, "settings"); err == nil {
		t.Error("a read without --offline never entered the cloud layers, so --offline guards nothing")
	} else if calls != 1 {
		t.Errorf("the cloud layers were read %d times, want once", calls)
	}
}

// TestSettingsGlobMatchesNestedKeys: in a glob * is one dotted segment and **
// is any number of them, so a key of a varying segment is addressable and a
// whole subtree is reachable in one argument. `workflows.*` — what a *
// spanning dots would make of it — names a key directly under workflows, and
// the registry has none, so it is refused rather than answered with the
// subtree.
func TestSettingsGlobMatchesNestedKeys(t *testing.T) {
	settingsFixture(t, "")
	one := readSettingsJSON(t, "budget.*_usd")
	keys := map[string]bool{}
	for _, r := range one.Settings {
		keys[r.Key] = true
	}
	if !keys["budget.per_run_usd"] || !keys["budget.per_day_usd"] {
		t.Errorf("budget.*_usd matched %v, want both budgets", keys)
	}

	deep := readSettingsJSON(t, "workflows.**")
	if len(deep.Settings) < 2 {
		t.Fatalf("workflows.** matched %d rows, want the whole subtree", len(deep.Settings))
	}
	for _, r := range deep.Settings {
		if !strings.HasPrefix(r.Key, "workflows.") {
			t.Errorf("workflows.** matched %s, which is not under workflows", r.Key)
		}
	}

	exact := readSettingsJSON(t, "layer.profiles.*.commands.test")
	if len(exact.Settings) != 1 || exact.Settings[0].Key != "layer.profiles.*.commands.test" {
		t.Errorf("a key named with its own * segment matched %d rows", len(exact.Settings))
	}

	out, _, err := execute(t, "settings", "workflows.*")
	if err == nil {
		t.Errorf("workflows.* matched a key, and * is one segment:\n%s", out)
	}

	// A * never crosses a dot, and a ** in the middle is the same any-depth
	// rule as at the end.
	if out, _, err := execute(t, "settings", "budget*usd"); err == nil {
		t.Errorf("budget*usd matched a key, so * crossed a dot:\n%s", out)
	}
	mid := readSettingsJSON(t, "layer.profiles.**.commands.test")
	if len(mid.Settings) != 1 || mid.Settings[0].Key != "layer.profiles.*.commands.test" {
		t.Errorf("layer.profiles.**.commands.test matched %d rows, want the one key", len(mid.Settings))
	}
	all := readSettingsJSON(t)
	everything := readSettingsJSON(t, "**")
	if len(everything.Settings) != len(all.Settings) {
		t.Errorf("** matched %d of %d rows: it is every key", len(everything.Settings), len(all.Settings))
	}
}

// TestSettingsRecordsEachLayersReadDuration: duration_ms is what reading that
// layer cost, so 0 belongs to the one source that is not a read — the
// registry's defaults, which are in memory — and every layer that was read
// reports its own. The clock is a seam so the durations are exact: a field
// nothing writes and a field written from the clock are the same `0` in a
// document, and only this tells them apart.
func TestSettingsRecordsEachLayersReadDuration(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML(""))
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now(), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	ticks := 0
	settingsNow = func() time.Time {
		ticks++
		return time.Unix(0, 0).Add(time.Duration(ticks) * 10 * time.Millisecond)
	}
	t.Cleanup(func() { settingsNow = time.Now })

	got := map[string]int64{}
	for _, s := range readSettingsJSON(t, "--all-repos").Sources {
		got[s.Layer] = s.DurationMS
	}
	if got["default"] != 0 {
		t.Errorf("default duration_ms = %d, want 0: the defaults are the registry in memory", got["default"])
	}
	for _, layer := range []string{"project", "local", "repo"} {
		if got[layer] != 10 {
			t.Errorf("%s duration_ms = %d, want the 10ms the clock advanced while it was read", layer, got[layer])
		}
	}
}

// TestSettingsValueAtRendersAMap: a key whose leaf is a map prints it rather
// than a blank, in sorted order. The four registry keys that land on one —
// layer.claude_models, base_images and review_floor with ab.exchange_rate —
// hold lists and numbers, so a blank there would read as unset while the layer
// sets the key.
func TestSettingsValueAtRendersAMap(t *testing.T) {
	doc := config.AB{ExchangeRate: map[rigor.Mode]float64{rigor.Pro: 1, rigor.POC: 0.25}}
	if got, ok := valueAt(doc, "exchange_rate"); !ok || got != `{"poc":0.25,"pro":1}` {
		t.Errorf("exchange_rate = %q, %v, want the map as its JSON in key order", got, ok)
	}
	type holder struct {
		Models map[string][]string `yaml:"models"`
	}
	got, ok := valueAt(holder{Models: map[string][]string{"b": {"2"}, "a": {"1"}}}, "models")
	if !ok || got != `{"a":["1"],"b":["2"]}` {
		t.Errorf("models = %q, %v, want the lists inside a map rendered too", got, ok)
	}
}

// TestSettingsGrepFiltersRows: the pattern is unanchored and matched against
// the key and each rendered column.
func TestSettingsGrepFiltersRows(t *testing.T) {
	settingsFixture(t, "")
	doc := readSettingsJSON(t, "--grep", "^budget\\.")
	if len(doc.Settings) == 0 {
		t.Fatal("--grep matched nothing")
	}
	for _, r := range doc.Settings {
		if !strings.HasPrefix(r.Key, "budget.") {
			t.Errorf("%s: matched a pattern anchored at budget.", r.Key)
		}
	}
	all := readSettingsJSON(t)
	if len(doc.Settings) >= len(all.Settings) {
		t.Errorf("--grep kept %d of %d rows", len(doc.Settings), len(all.Settings))
	}
	none := readSettingsJSON(t, "--grep", "nothing-matches-this")
	if len(none.Settings) != 0 {
		t.Errorf("--grep with no match printed %d rows", len(none.Settings))
	}
}

// TestSettingsTwoProvidersOfOneRepositoryReadOneCheckout: this machine can
// remember one owner/name under two providers — a repository cloned from both
// GitHub and Bitbucket — and the remembered document is keyed by provider, so
// the choice between them has to be made rather than fallen into. It is the
// key in sorted order, and the repository is read once: two rows for one scope
// with one of two checkouts' values would be a row that means nothing.
func TestSettingsTwoProvidersOfOneRepositoryReadOneCheckout(t *testing.T) {
	home := settingsFixture(t, "")
	write := func(dir, yaml string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	bitbucket := write(filepath.Join(home, "bitbucket"), settingsRepoYAML("agent:\n  review_rounds: 3\n"))
	github := write(filepath.Join(home, "github"), settingsRepoYAML("agent:\n  review_rounds: 7\n"))
	for _, c := range []struct{ provider, dir string }{{"bitbucket", bitbucket}, {"github", github}} {
		if err := localcfg.RecordCheckout(os.Getenv, "aurora",
			localcfg.CheckoutIdentity{Provider: c.provider, Repo: "owner/repo-a"}, c.dir, time.Now(), checkoutTempRoots()); err != nil {
			t.Fatal(err)
		}
	}

	doc := readSettingsJSON(t, "agent.review_rounds", "--all-repos")
	rows := []settingsTestRow{}
	for _, r := range doc.Settings {
		if r.Scope == "repo:owner/repo-a" {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows for one repository, want one", len(rows))
	}
	if rows[0].Value == nil || *rows[0].Value != "3" {
		t.Errorf("value = %s, want the bitbucket checkout's 3: the provider keys are read in sorted order", deref(rows[0].Value))
	}
}

// TestSettingsGrepNeverSeesASensitiveValue: --grep is unanchored over the key
// and every rendered column, and a sensitive row's rendered value is the
// set/unset fact — so the credential string matches nothing, while the key
// itself still does. A pattern that answered with the row would mean the value
// reached a column.
func TestSettingsGrepNeverSeesASensitiveValue(t *testing.T) {
	settingsFixture(t, "budget:\n  firebase_api_key: "+settingsFakeAPIKey+"\n")
	if doc := readSettingsJSON(t, "--grep", "AIzaSy"); len(doc.Settings) != 0 {
		t.Errorf("--grep for the credential matched %d rows", len(doc.Settings))
	}
	if doc := readSettingsJSON(t, "--grep", "--why", "--grep", "firebase_api_key"); len(doc.Settings) == 0 {
		t.Error("--grep for the key matched nothing")
	}
}

// TestSettingsLayerRestrictsTheRows: --layer says which stores to read, so a
// row none of whose layers is named is left out, and a name the registry does
// not know is refused. `flag` is a level a run's own command line holds and
// `settings` takes no such override, so it reads an empty layer rather than
// disappearing from the vocabulary.
func TestSettingsLayerRestrictsTheRows(t *testing.T) {
	settingsFixture(t, "max_parallel: 3\n")
	local := readSettingsJSON(t, "--layer", "local")
	keys := map[string]bool{}
	for _, r := range local.Settings {
		keys[r.Key] = true
	}
	if !keys["max_parallel"] {
		t.Errorf("--layer local left out a key the local config sets: %v", keys)
	}
	if keys["agent.review_rounds"] || keys["layer.default_profile"] {
		t.Errorf("--layer local kept a key whose layers are not local (review_rounds %v, layer.default_profile %v)",
			keys["agent.review_rounds"], keys["layer.default_profile"])
	}
	if row := rowOf(t, local, "max_parallel", "project"); row.Wins != "local" {
		t.Errorf("max_parallel wins = %q, want local", row.Wins)
	}

	flag := readSettingsJSON(t, "--layer", "flag")
	hasFlag := false
	for _, r := range flag.Settings {
		row, ok := settings.Lookup(r.Key)
		hasFlag = hasFlag || (ok && slices.Contains(row.Layers, "flag"))
	}
	if !hasFlag {
		t.Error("--layer flag read no row with a flag level, so the name is not in the vocabulary")
	}

	if out, _, err := execute(t, "settings", "--layer", "bogus"); err == nil {
		t.Errorf("--layer bogus was accepted:\n%s", out)
	} else if ExitCode(err) != ExitUserError {
		t.Errorf("--layer bogus exits %d, want %d", ExitCode(err), ExitUserError)
	}
}

// TestEditDistanceCountsEdits: the name a typo is answered with is the nearest
// registry key by this distance, so it is tested on its own rather than only
// through a message.
func TestEditDistanceCountsEdits(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"budget", "budget", 0},
		{"budget.per_run_usd", "budget.per_run_uds", 2},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
	} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got, rev := editDistance(tc.b, tc.a), tc.want; got != rev {
			t.Errorf("editDistance(%q, %q) = %d, want %d: the distance is symmetric", tc.b, tc.a, got, rev)
		}
	}
}

// TestSettingsStatesCarryNoTrailingSpace: a state that carries a reason is a
// word and its text, and the two are only ever joined in one place. A constant
// holding the trailing space would put it in the document the moment a reason
// was empty — and the document is what a consumer branches on.
func TestSettingsStatesCarryNoTrailingSpace(t *testing.T) {
	home := settingsFixture(t, "")
	// A checkout the machine remembers but whose file is refused reads
	// `unread: <reason>`; the layer cache is old enough to read `stale <age>`.
	settingsCheckout(t, home, "owner/repo-b", "project: aurora\n")
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now().Add(-3 * time.Minute), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "settings", "--json", "--all-repos")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	states := doc2states(t, out)
	if len(states) == 0 {
		t.Fatal("the document carries no state")
	}
	unread, stale := false, false
	for _, s := range states {
		if strings.TrimRight(s, " ") != s || s == "" {
			t.Errorf("state %q carries a trailing space or is empty", s)
		}
		unread = unread || strings.HasPrefix(s, "unread: ")
		stale = stale || strings.HasPrefix(s, "stale ")
	}
	if !unread || !stale {
		t.Errorf("the fixture produced no unread and no stale state (%v), so this test checked one of them at most", states)
	}
}

// TestSettingsWritesNothing: the read is a read. It is the one command an
// agent may run about its own installation, and that is only true while it
// changes nothing.
func TestSettingsWritesNothing(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML(""))
	before := snapshotTree(t, home)
	if _, _, err := execute(t, "settings", "--all-repos"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "settings", "--json", "--why"); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, home)
	if before != after {
		t.Errorf("the read changed this machine:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestSettingsRepoScopeReportsTheKeyTheRepositorySets: a registry key with a
// varying middle segment (workflows.*.commands.test) is the repository's own
// path once resolved, and the resolution is keyed by real workflow names — so a
// row that looked the key up as written found nothing and printed the built-in
// default while the checkout set the key. The project scope of the same key
// still says no offline layer decided it.
func TestSettingsRepoScopeReportsTheKeyTheRepositorySets(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML(""))
	doc := readSettingsJSON(t, "workflows.*.commands.test", "--all-repos")
	repo := rowOf(t, doc, "workflows.*.commands.test", "repo:owner/repo-a")
	if repo.Value == nil || *repo.Value != "go test ./..." {
		t.Fatalf("the repository scope = %q, wins = %q, state = %q, want the checkout's own command", deref(repo.Value), repo.Wins, repo.State)
	}
	if repo.Wins != "repo" {
		t.Errorf("wins = %q, want repo: the checkout sets the key", repo.Wins)
	}
	if proj := rowOf(t, doc, "workflows.*.commands.test", "project"); proj.Wins != "default" {
		t.Errorf("the project scope's wins = %q, want default: no offline layer sets a workflow command", proj.Wins)
	}
}

// TestSettingsRepoScopeNamesTheProfileThatDecidedAValue: a value the project
// layer's profile supplied is still the value a run gets, and WINS says so in
// the resolution's own words — `profile <name>`, the spelling spec §2.2 pins to
// `config show`'s (config.SourceProfile). A row that printed the bare layer
// name would not say which profile decided it, and the level is not the ten
// names `--layer` takes.
func TestSettingsRepoScopeNamesTheProfileThatDecidedAValue(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoFromProfile)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now(), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	doc := readSettingsJSON(t, "workflows.*.commands.build", "workflows.*.commands.test", "--repo", "owner/repo-a")
	fromProfile := rowOf(t, doc, "workflows.*.commands.build", "repo:owner/repo-a")
	if fromProfile.Value == nil || *fromProfile.Value != "sh build.sh" {
		t.Fatalf("the repository scope = %q, wins = %q, state = %q, want the profile's sh build.sh", deref(fromProfile.Value), fromProfile.Wins, fromProfile.State)
	}
	if fromProfile.Wins != config.SourceProfile("svc") {
		t.Errorf("wins = %q, want %q: the workflow named profile svc, which supplied the value", fromProfile.Wins, config.SourceProfile("svc"))
	}
	if fromProfile.State != "ok" {
		t.Errorf("state = %q, want ok", fromProfile.State)
	}
	// The key the checkout does set is the checkout's, so the two words are not
	// one: the row is not reporting the profile for a value the repository
	// wrote.
	fromRepo := rowOf(t, doc, "workflows.*.commands.test", "repo:owner/repo-a")
	if fromRepo.Value == nil || *fromRepo.Value != "go test ./..." || fromRepo.Wins != "repo" {
		t.Errorf("workflows.*.commands.test = %q/%q, want the checkout's own command and repo", deref(fromRepo.Value), fromRepo.Wins)
	}
}

// TestSettingsProjectScopeReadsTheLayersDefaults: the project layer's defaults:
// are the project level of a resolved fugaro.yaml key (spec §2.2), so the
// project scope reports the value a run gets from them. The keys the fixture's
// defaults do not carry still read as defaults: the decoded fields cannot tell
// `review_rounds: 0` from an unset key, so a read that took the block for the
// answer would print a 0 for agent.review_rounds and its 25-dollar default for
// agent.max_budget_usd.
func TestSettingsProjectScopeReadsTheLayersDefaults(t *testing.T) {
	settingsFixture(t, "")
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now().Add(-3 * time.Minute), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	doc := readSettingsJSON(t, "git.provider", "agent.auth", "agent.review_rounds", "agent.max_budget_usd")
	for _, tc := range []struct{ key, value string }{
		{"git.provider", "github"},
		{"agent.auth", "api-key"},
	} {
		row := rowOf(t, doc, tc.key, "project")
		if row.Value == nil || *row.Value != tc.value || row.Wins != "project" {
			t.Errorf("%s = %q/%q, want the layer's defaults: %q decided by project", tc.key, deref(row.Value), row.Wins, tc.value)
		}
		if row.State != "stale 3m" {
			t.Errorf("%s: state = %q, want the cache's age: the layer read is a cached copy", tc.key, row.State)
		}
	}
	for _, tc := range []struct{ key, value string }{
		{"agent.review_rounds", "2"},
		{"agent.max_budget_usd", "25"},
	} {
		row := rowOf(t, doc, tc.key, "project")
		if row.Value == nil || *row.Value != tc.value || row.Wins != "default" {
			t.Errorf("%s = %q/%q, want %q: the layer's defaults do not write it", tc.key, deref(row.Value), row.Wins, tc.value)
		}
	}
}

// TestSettingsScopesAgreeOnADefaultedValue: a key no layer sets prints the
// DEFAULT column's value in both scopes, and the sentence saying what unset
// means where that is the default. The repository scope printed the DEFAULT
// column's own text — `4 (config.DefaultCPU)` — where the project scope printed
// `4`, so one key had two answers a consumer had to know to compare.
func TestSettingsScopesAgreeOnADefaultedValue(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML(""))
	doc := readSettingsJSON(t, "workflows.*.resources.cpu", "git.pr.checkpoints", "--all-repos")
	// The registry is the oracle, the way valueOrDefault reads it: the value
	// alone, or the sentence saying what unset means. The DEFAULT column is the
	// other rendering — `4 (config.DefaultCPU)` — and it belongs there only.
	for _, key := range []string{"workflows.*.resources.cpu", "git.pr.checkpoints"} {
		row, ok := settings.Lookup(key)
		if !ok {
			t.Fatalf("%s: not a registry key", key)
		}
		want := row.Default.Value
		if want == "" {
			want = row.Default.Unset
		}
		proj := rowOf(t, doc, key, "project")
		if proj.Value == nil || *proj.Value != want {
			t.Errorf("%s in the project scope = %q, want %q", key, deref(proj.Value), want)
		}
		repo := rowOf(t, doc, key, "repo:owner/repo-a")
		if repo.Value == nil || *repo.Value != want || repo.Wins != "default" {
			t.Errorf("%s in the repository scope = %q/%q, want the registry's %q and default", key, deref(repo.Value), repo.Wins, want)
		}
	}
}

// TestSettingsRepoScopeBeatsTheProjectScopesLocalValue: the two scopes answer
// one key from different documents, and the repository's own file is the
// stricter of the two (spec §2.2: local < repo). A repository scope that let
// the local config decide would print the installation's GCP project for a
// checkout that pins its own.
func TestSettingsRepoScopeBeatsTheProjectScopesLocalValue(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoFromProfile)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now(), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	doc := readSettingsJSON(t, "gcp_project", "--all-repos")
	proj := rowOf(t, doc, "gcp_project", "project")
	if proj.Value == nil || *proj.Value != "aurora-gcp" || proj.Wins != "local" {
		t.Errorf("the project scope = %q/%q, want the local config's aurora-gcp and local", deref(proj.Value), proj.Wins)
	}
	repo := rowOf(t, doc, "gcp_project", "repo:owner/repo-a")
	if repo.Value == nil || *repo.Value != "proj-1234" {
		t.Fatalf("the repository scope = %q, wins = %q, want the checkout's proj-1234", deref(repo.Value), repo.Wins)
	}
	if repo.Wins != "repo" {
		t.Errorf("wins = %q, want repo: the checkout sets the key, and the project scope's local value is not this row's answer", repo.Wins)
	}
}

// TestSettingsUnreadNamesThePathItIsAbout: a checkout whose fugaro.yaml does
// not resolve reads `unread: <reason>`, and the reason is the problem's own
// line, which starts with the key it is about. The message alone — `is "", but
// the project layer given is GCP project "proj-1234"'s` — says nothing about
// which key of the file it means, so a reader could not tell a missing
// gcp_project from a missing project.
func TestSettingsUnreadNamesThePathItIsAbout(t *testing.T) {
	home := settingsFixture(t, "")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoYAML(""))
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now(), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	doc := readSettingsJSON(t, "agent.review_rounds", "--repo", "owner/repo-a")
	row := rowOf(t, doc, "agent.review_rounds", "repo:owner/repo-a")
	if !strings.HasPrefix(row.State, "unread: gcp_project: ") {
		t.Errorf("state = %q, want a reason that names the key the problem is about", row.State)
	}
}

// TestSettingsUpdateCheckReadsItsEnvLayer: update_check is the one key a
// process environment sets as well as a file, and the order is the run's — env
// over local (spec §2.2) — so the row says off while FUGARO_NO_UPDATE_CHECK is
// set to the value updatewarn.go reads, whatever the local file says. A row
// that read the file first would call an installation whose runs turn the
// check off one that checks for updates.
func TestSettingsUpdateCheckReadsItsEnvLayer(t *testing.T) {
	settingsFixture(t, "update_check: true\n")
	reg, ok := settings.Lookup("update_check")
	if !ok || !slices.Contains(reg.Layers, settings.LayerEnv) {
		t.Fatalf("update_check's layers = %v, want the environment among them", reg.Layers)
	}
	file := rowOf(t, readSettingsJSON(t, "update_check"), "update_check", "project")
	if file.Value == nil || *file.Value != "true" || file.Wins != "local" {
		t.Errorf("without the variable the row = %q/%q, want the local file's true", deref(file.Value), file.Wins)
	}

	t.Setenv(noUpdateCheckEnv, "1")
	off := rowOf(t, readSettingsJSON(t, "update_check"), "update_check", "project")
	if off.Value == nil || *off.Value != "false" || off.Wins != "env" {
		t.Errorf("with %s=1 the row = %q/%q, want false/env: the environment is read before the file", noUpdateCheckEnv, deref(off.Value), off.Wins)
	}
	if doc := readSettingsJSON(t, "update_check", "--layer", "env"); len(doc.Settings) != 1 {
		t.Errorf("--layer env kept %d rows, want the one key the environment can decide", len(doc.Settings))
	}

	// The variable's word is "1" (noUpdateCheckEnv is compared with it), so a
	// row that read the variable for being set at all would call the check off
	// for FUGARO_NO_UPDATE_CHECK=0.
	t.Setenv(noUpdateCheckEnv, "0")
	on := rowOf(t, readSettingsJSON(t, "update_check"), "update_check", "project")
	if on.Value == nil || *on.Value != "true" || on.Wins != "local" {
		t.Errorf("with %s=0 the row = %q/%q, want the local file's true", noUpdateCheckEnv, deref(on.Value), on.Wins)
	}
}

// snapshotTree is every file under dir with its size and contents, so a write
// anywhere under the config home is visible.
func snapshotTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b.WriteString(rel + " " + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
