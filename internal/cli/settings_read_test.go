package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
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
	if err := localcfg.RecordCheckout(os.Getenv, "aurora", localcfg.CheckoutIdentity{Provider: "github", Repo: repo}, path, time.Now()); err != nil {
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
// so its five columns are named and in order, and every row fills them.
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
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "KEY VALUE WINS DEFAULT STATE" {
		t.Errorf("header = %q, want the five columns", lines[0])
	}
	if !strings.HasPrefix(lines[1], "budget.per_run_usd") {
		t.Errorf("row = %q, want the key first", lines[1])
	}
	if !strings.Contains(lines[1], "default") {
		t.Errorf("row = %q, want the layer that decided it", lines[1])
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
