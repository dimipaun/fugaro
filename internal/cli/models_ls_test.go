package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// modelsLocalExtra is what the models fixtures append to the project config
// writeProject writes: one route open to owner/repo-a alone, an override the
// catalog does not price, an allow-list that leaves one claimed model out, and
// two recorded repositories — one with a checkout on this machine and one
// without.
const modelsLocalExtra = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    route_fee_pct: 5
    models: ["deepseek/*", "vendor/model-x"]
    allow_data_to: ["owner/repo-a"]
model_prices:
  vendor/model-y:
    input_per_m: 1
    output_per_m: 2
budget:
  mode: enforce
  per_run_usd: 10
  allowed_models: ["claude-sonnet-5-5", "deepseek/deepseek-v4-flash", "vendor/model-x", "vendor/model-y"]
repos:
  owner/repo-a: {}
  owner/repo-b: {}
`

// modelsFixture is the project config every models test reads: project aurora
// in a config home of its own, with modelsLocalExtra appended.
func modelsFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	isolateProjects(t, home)
	path := writeProject(t, "aurora", "aurora-gcp")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(modelsLocalExtra)...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	return home
}

// modelsRepoYAML is the repository file the usage read resolves: a provider
// coder, a Claude reviewer and no background pin, so the effective background
// (config.EffectiveBackground) is the coder's own model rather than a hidden
// Claude haiku call.
const modelsRepoYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
workflows:
  web:
    commands: { build: go build ./..., test: go test ./... }
`

// modelsCheckout makes dir a real checkout of repo and remembers it for the
// project: the file is committed on the base branch, so the read at that ref
// has something to read (an uncommitted working tree would exercise the
// fallback instead of the read the spec asks for).
func modelsCheckout(t *testing.T, home, repo, fugaroYAML string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := filepath.Join(home, "checkouts", strings.ReplaceAll(repo, "/", "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": fugaroYAML})
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's files")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "github", Repo: repo}, dir, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestModelsLsListsRoutesModelsAllowListsAndUsage is the read's own test: the
// four sections and what each of them has to answer — the route table with its
// allow_data_to, the models a run can name today with the route and the source
// that serve each, the repositories each model is open to, and the pins of
// every recorded repository.
func TestModelsLsListsRoutesModelsAllowListsAndUsage(t *testing.T) {
	home := modelsFixture(t)
	modelsCheckout(t, home, "owner/repo-a", modelsRepoYAML)

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, section := range []string{"ROUTES", "MODELS", "ALLOW-LISTS", "USAGE"} {
		if !strings.Contains(out, section) {
			t.Errorf("the read has no %s section:\n%s", section, out)
		}
	}
	// The route table: the provider as the owner wrote it, with its host, its
	// claims, its fee and the repositories it may send code to — and the direct
	// Anthropic route beside it.
	for _, want := range []string{"openrouter", "openrouter.ai", "deepseek/*", "5%", "owner/repo-a", "direct"} {
		if !strings.Contains(out, want) {
			t.Errorf("the ROUTES section does not show %q:\n%s", want, out)
		}
	}
	// The models section: a model the catalog prices through the route, and one
	// only the owner's model_prices prices.
	for _, want := range []string{"deepseek/deepseek-v4-flash", "catalog", "vendor/model-y", "local"} {
		if !strings.Contains(out, want) {
			t.Errorf("the MODELS section does not show %q:\n%s", want, out)
		}
	}
	// An embedded Claude row is on the direct route: it is the one row that
	// shows what a model no provider claims resolves to, and without it the
	// direct route is a table row nothing in the read is ever sent down.
	models := modelsSection(t, out, "MODELS")
	line := ""
	for _, l := range strings.Split(models, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "claude-sonnet-5-5 ") {
			line = l
		}
	}
	if line == "" {
		t.Errorf("the MODELS section has no row for claude-sonnet-5-5:\n%s", models)
	} else if !strings.Contains(line, config.RouteDirect) {
		t.Errorf("claude-sonnet-5-5's row is %q, and a Claude ID no route claims goes direct", strings.TrimSpace(line))
	}
	// Allow-lists: the model budget.allowed_models leaves out is refused for
	// every repository, which is the one thing that column exists for.
	allow := modelsSection(t, out, "ALLOW-LISTS")
	if !strings.Contains(allow, "no repository") {
		t.Errorf("the ALLOW-LISTS section does not show a model refused for every repository:\n%s", allow)
	}
	// Usage: the checkout's pins, and the repository this machine cannot see.
	usage := modelsSection(t, out, "USAGE")
	for _, want := range []string{"owner/repo-a", "deepseek/deepseek-v4-flash", "claude-sonnet-5-5", "owner/repo-b", stateUnknown} {
		if !strings.Contains(usage, want) {
			t.Errorf("the USAGE section does not show %q:\n%s", want, usage)
		}
	}
	// The background is unset in the repository's file and its coder is a
	// provider model, so the effective background is the coder's own model
	// (config.EffectiveBackground) rather than an empty cell.
	if !strings.Contains(usage, "deepseek/deepseek-v4-flash") {
		t.Errorf("the USAGE section does not resolve the effective background:\n%s", usage)
	}
}

// TestModelsLsUnknownWithoutACheckout: a recorded repository this machine has
// no checkout for is shown as unknown, never guessed at (D793).
func TestModelsLsUnknownWithoutACheckout(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	usage := modelsSection(t, out, "USAGE")
	if !strings.Contains(usage, stateUnknown) {
		t.Errorf("a repository with no checkout on this machine is not shown as unknown:\n%s", usage)
	}
	if !strings.Contains(usage, "owner/repo-b") {
		t.Errorf("the USAGE section does not name the repository it could not read:\n%s", usage)
	}
	raw, _, err := execute(t, "models", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	var doc struct {
		Repositories []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	found := false
	for _, r := range doc.Repositories {
		if r.Name != "owner/repo-b" {
			continue
		}
		found = true
		if r.State != stateUnknown {
			t.Errorf("owner/repo-b's state is %q, and it has no checkout here", r.State)
		}
	}
	if !found {
		t.Errorf("the document has no row for owner/repo-b:\n%s", raw)
	}
}

// TestModelsLsShowsCatalogAgeAndSpread: a catalog-priced row carries the
// catalog's own date and age, and the spread the row is charged over — the
// dearest endpoint of the tier against the price charged (design D6, P11) —
// so the owner can see the size of what a cheapest bound leaves.
func TestModelsLsShowsCatalogAgeAndSpread(t *testing.T) {
	home := modelsFixture(t)
	modelsCheckout(t, home, "owner/repo-a", modelsRepoYAML)
	// A clock inside the catalog's 30-day window, so the row is priced at the
	// cheapest end and there is an undercharge to size.
	old := budgetNow
	t.Cleanup(func() { budgetNow = old })
	budgetNow = func() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC) }

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "provider price catalog:") {
		t.Errorf("the read does not head the models with the catalog's own provenance:\n%s", out)
	}
	if !strings.Contains(out, "2026-10-23") {
		t.Errorf("the read does not show the catalog's checked date:\n%s", out)
	}
	if !strings.Contains(out, "3 days ago") {
		t.Errorf("the read does not show the catalog's age:\n%s", out)
	}
	if !strings.Contains(out, "dearest endpoint of the tier") {
		t.Errorf("the read does not size the spread a catalog row is charged over:\n%s", out)
	}
}

// TestModelsLsSaysWhyAnUnpricedModelHasNoPrice: a model a route claims that
// nothing prices — the catalog does not carry it and the owner wrote no
// model_prices entry for it — has no refusal of the table's own, and its NOTES
// cell was empty, which reads as "nothing to say" on the one row a launch
// refuses. The reason is config's own wording (pins.go, providers.go), and the
// ID is the row's first cell, so the sentence does not repeat it.
func TestModelsLsSaysWhyAnUnpricedModelHasNoPrice(t *testing.T) {
	home := modelsFixture(t)
	modelsCheckout(t, home, "owner/repo-a", modelsRepoYAML)

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	models := modelsSection(t, out, "MODELS")
	line := ""
	for _, l := range strings.Split(models, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "vendor/model-x ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the MODELS section has no row for vendor/model-x:\n%s", models)
	}
	if !strings.Contains(line, "no price: add it under model_prices in the project config") {
		t.Errorf("vendor/model-x's row is %q, and the row carries no price and no reason given", strings.TrimSpace(line))
	}

	raw, _, err := execute(t, "models", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	var doc struct {
		Models []struct {
			ID      string `json:"id"`
			Source  string `json:"source"`
			Refused string `json:"refused"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	for _, m := range doc.Models {
		if m.ID != "vendor/model-x" {
			continue
		}
		if m.Source != modelsSourceNone {
			t.Errorf("vendor/model-x's source is %q, and nothing prices it", m.Source)
		}
		if m.Refused == "" {
			t.Error("vendor/model-x's row has no refused member, so a consumer cannot say why it is unusable")
		}
		return
	}
	t.Errorf("the document has no row for vendor/model-x:\n%s", raw)
}

// TestModelsLsEscapesTableCells: every value of these four tables is a cell,
// not a line. oneLine keeps a literal tab — right for a whole line, where a tab
// is only whitespace — and a tab written into a tabwriter row opens a column of
// its own, so a value carrying one would shift every column after it. The
// writer escapes them (cellOneLine, safetext.TableCell), and a bidi override or
// an ANSI sequence is escaped there too rather than reaching the terminal.
//
// The columns are the assertion that matters: the row with the tab has to put
// its SOURCE cell where the row without one puts it. A writer that went back to
// oneLine splits the ID into two cells and moves it.
func TestModelsLsEscapesTableCells(t *testing.T) {
	const tab = "vendor/tab\tbed"
	const bidi = "vendor/bidi\u202ebed"
	const ansi = "vendor/ansi\x1b[31mbed"

	doc := modelsLsDoc{
		Version: modelsLsDocVersion,
		Project: "aurora",
		Routes: []modelsRoute{{
			Name: tab, Host: bidi, Models: []string{ansi},
			AllowDataTo: []string{tab}, Note: bidi,
		}},
		Models: []modelsModel{
			{ID: "vendor/plain", Route: "openrouter", Source: modelsSourceNone, Refused: "no price", AllowedTo: []string{"owner/repo-a"}},
			{ID: tab, Route: bidi, Source: modelsSourceNone, Refused: ansi, AllowedTo: []string{tab}},
		},
		Repos: []modelsRepo{{Name: tab, State: bidi, Coder: ansi, Problems: []string{tab}}},
	}
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := writeModelsLs(cmd, nil, doc); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	models := modelsSection(t, text, "MODELS")
	plain, tabbed := "", ""
	for _, l := range strings.Split(models, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(l), "vendor/plain "):
			plain = l
		case strings.HasPrefix(strings.TrimSpace(l), "vendor/tab"):
			tabbed = l
		}
	}
	if plain == "" || tabbed == "" {
		t.Fatalf("the MODELS section does not carry both rows:\n%s", models)
	}
	if a, b := strings.Index(plain, modelsSourceNone), strings.Index(tabbed, modelsSourceNone); a != b {
		t.Errorf("a tab in a model ID shifted the row's columns: SOURCE at %d and %d\n%s", a, b, models)
	}
	for _, section := range []string{"ROUTES", "MODELS", "ALLOW-LISTS", "USAGE"} {
		body := modelsSection(t, text, section)
		for _, raw := range []string{"\u202e", "\x1b"} {
			if strings.Contains(body, raw) {
				t.Errorf("the %s section carries a raw %q:\n%s", section, raw, body)
			}
		}
	}
	// What a cell shows instead: the tab and the bidi override are marked, so
	// the reader sees that something was there, while the whole ANSI sequence
	// is dropped (safetext skips it: an ESC[31m left in would colour the rest
	// of the table).
	for _, want := range []string{"vendor/tab?bed", "vendor/bidi?bed", "vendor/ansibed"} {
		if !strings.Contains(text, want) {
			t.Errorf("the escaped value %q is not in the read:\n%s", want, text)
		}
	}
}

// TestModelsLsSanitisesAPin: a repository's own pin reaches three cells of this
// read — the MODELS and ALLOW-LISTS rows it adds, and the USAGE row's CODER —
// and a pin is a string the repository writes. A bidi override or an ANSI
// sequence in it is escaped rather than printed: an ESC[31m would colour the
// rest of the table and a U+202E would let the ID read backwards.
//
// Mutation (run, restored): print the model ID raw at the MODELS and
// ALLOW-LISTS rows (drop oneLine/cellOneLine) and this fails on the raw
// characters.
func TestModelsLsSanitisesAPin(t *testing.T) {
	home := modelsFixture(t)
	pin := `coder: "vendor/mo\u202edel\x1b[31m"`
	modelsCheckout(t, home, "owner/repo-a", strings.Replace(modelsRepoYAML, "coder: deepseek/deepseek-v4-flash", pin, 1))

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, raw := range []string{"\u202e", "\x1b"} {
		if strings.Contains(out, raw) {
			t.Errorf("the read prints a raw %q from a repository's pin:\n%q", raw, out)
		}
	}
	if !strings.Contains(out, "vendor/mo?del") {
		t.Errorf("the pin is not in the read at all:\n%s", out)
	}
	models := modelsSection(t, out, "MODELS")
	if !strings.Contains(models, "vendor/mo?del") {
		t.Errorf("the MODELS section does not carry the pin's row:\n%s", models)
	}
	allow := modelsSection(t, out, "ALLOW-LISTS")
	if !strings.Contains(allow, "vendor/mo?del") {
		t.Errorf("the ALLOW-LISTS section does not carry the pin's row:\n%s", allow)
	}
	usage := modelsSection(t, out, "USAGE")
	if !strings.Contains(usage, "vendor/mo?del") {
		t.Errorf("the USAGE section does not carry the pin:\n%s", usage)
	}
}

// TestModelsLsJSONMatchesSchema validates the document the command writes
// against schemas/models-ls.schema.json, the contract a consumer reads it by
// (D784).
func TestModelsLsJSONMatchesSchema(t *testing.T) {
	home := modelsFixture(t)
	modelsCheckout(t, home, "owner/repo-a", modelsRepoYAML)

	out, _, err := execute(t, "models", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "models-ls.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/models-ls.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/models-ls.schema.json: %v\n%s", err, out)
	}

	// The document is only worth validating while it carries the members the
	// schema closes over: a fixture that stopped producing a catalog row, a
	// local row or an unknown repository would leave those branches unchecked
	// and still green.
	var decoded struct {
		Version int `json:"version"`
		Routes  []struct {
			Name string `json:"name"`
		} `json:"routes"`
		Models []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
		} `json:"models"`
		Repositories []struct {
			State string `json:"state"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 {
		t.Errorf("version = %d, and the document's first version is 1 (D784)", decoded.Version)
	}
	sources := map[string]bool{}
	for _, m := range decoded.Models {
		sources[m.Source] = true
	}
	for _, want := range []string{"catalog", "local"} {
		if !sources[want] {
			t.Errorf("the document carries no %q row (it has %v)", want, sources)
		}
	}
	states := map[string]bool{}
	for _, r := range decoded.Repositories {
		states[r.State] = true
	}
	if !states[stateUnknown] {
		t.Errorf("the document carries no %q repository (it has %v)", stateUnknown, states)
	}
}

// TestModelsLsIsOffline: the read reaches nothing — no bucket, no database, no
// host (spec §2, D788). An empty PATH is not enough to say so: it stops gcloud
// and git, and not a Go SDK client, so the fakes the project config points at
// are installed and counted. A read that went to Cloud Run, Cloud Logging or
// the budget database is red here.
func TestModelsLsIsOffline(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.appendConfig(t, modelsOfflineExtra)
	t.Setenv("PATH", t.TempDir())

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("the read is not offline: %v\n%s", err, out)
	}
	if !strings.Contains(out, "openrouter") {
		t.Errorf("the offline read does not show the route table:\n%s", out)
	}
	if n := len(f.run.Requests()) + len(f.logging.Requests()) + len(f.db.Requests()); n != 0 {
		t.Errorf("models ls made %d cloud call(s), want none: the read is the project config and this binary's own tables", n)
	}
	if _, _, err := execute(t, "models", "ls", "--cloud", "gcp"); err == nil {
		t.Error("models ls takes --cloud: the read has no cloud path to select")
	}

	// The control: `budget show` reads the same fixture's database, so the
	// count above is measuring calls and not a fake nothing can reach.
	before := len(f.db.Requests())
	if _, _, err := execute(t, "budget", "show"); err != nil {
		t.Fatalf("the control run (budget show) failed: %v", err)
	}
	if len(f.db.Requests()) == before {
		t.Fatal("budget show read no database either, so the count above proves nothing")
	}
}

// modelsOfflineExtra is what the offline tests append to the cloud fixture's
// project config: a provider route and one override, so the read has routes, a
// catalog row and a local row to show while the fixture's fake endpoints sit
// there reachable.
const modelsOfflineExtra = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
model_prices:
  vendor/model-y:
    input_per_m: 1
    output_per_m: 2
`

// modelsSection is one section of the text read: the lines between its header
// and the next one, so an assertion about the routes cannot be satisfied by a
// line the models section happens to carry.
func modelsSection(t *testing.T, out, name string) string {
	t.Helper()
	headers := []string{"ROUTES", "MODELS", "ALLOW-LISTS", "USAGE"}
	var got []string
	in := false
	for _, line := range strings.Split(out, "\n") {
		if line == name {
			in = true
			continue
		}
		if !in {
			continue
		}
		if slices.Contains(headers, line) {
			break
		}
		got = append(got, line)
	}
	if len(got) == 0 {
		t.Fatalf("the read has no %s section:\n%s", name, out)
	}
	return strings.Join(got, "\n")
}

// selectedModelsConfig is the project config the models fixture selected, read
// the way the command reads it.
func selectedModelsConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	_, lc, err := localcfg.Select(localcfg.SelectInput{
		EnvConfig: os.Getenv("FUGARO_CONFIG"),
		Getenv:    os.Getenv,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lc
}
