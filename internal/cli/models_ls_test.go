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
// host. With no PATH at all there is no gcloud and no git to run, and the read
// still answers from the files this machine holds (spec §2, D788).
func TestModelsLsIsOffline(t *testing.T) {
	modelsFixture(t)
	t.Setenv("PATH", t.TempDir())

	out, _, err := execute(t, "models", "ls")
	if err != nil {
		t.Fatalf("the read is not offline: %v\n%s", err, out)
	}
	if !strings.Contains(out, "openrouter") {
		t.Errorf("the offline read does not show the route table:\n%s", out)
	}
	if _, _, err := execute(t, "models", "ls", "--cloud", "gcp"); err == nil {
		t.Error("models ls takes --cloud: the read has no cloud path to select")
	}
}

// TestBudgetPricesJSONUnchanged pins the refactor M1 carries: the price-row
// builder moved out of `budget prices` behind one function both commands call,
// and the command's document has to stay exactly what that builder produces —
// a second row construction grown back beside it fails here. The rows
// themselves are pinned by value, not only agreed with, so a builder that
// stopped carrying a catalog row's provenance or an override's prices is red
// here rather than in a run.
func TestBudgetPricesJSONUnchanged(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var doc pricesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var byID = map[string]priceRow{}
	for _, r := range doc.Models {
		byID[r.ID] = r
	}
	// The catalog row the route claims: its provenance is the catalog's own,
	// and its prices are the ones the table holds.
	if row, ok := byID["deepseek/deepseek-v4-flash"]; !ok {
		t.Errorf("no row for deepseek/deepseek-v4-flash:\n%s", out)
	} else if row.Catalog == nil {
		t.Errorf("deepseek/deepseek-v4-flash is not marked catalog-priced: %+v", row)
	} else if row.Catalog.Tier != "zdr" || row.Catalog.Bound != "min" || row.Catalog.Endpoints == 0 {
		t.Errorf("deepseek/deepseek-v4-flash's catalog provenance = %+v", row.Catalog)
	}
	// The override: the owner's own numbers, marked local.
	if row, ok := byID["vendor/model-y"]; !ok {
		t.Errorf("no row for vendor/model-y:\n%s", out)
	} else if !row.Local || row.InputPerM != 1 || row.OutputPerM != 2 {
		t.Errorf("vendor/model-y = %+v, and the owner priced it 1/2", row)
	}
	// And the whole document is the shared builder's rows, in its order.
	table, ov, err := priceTable(selectedModelsConfig(t), budgetNow())
	if err != nil {
		t.Fatal(err)
	}
	want := priceRowsFor(table, ov)
	if len(want) != len(doc.Models) {
		t.Fatalf("the command prints %d rows and the shared builder has %d", len(doc.Models), len(want))
	}
	for i := range want {
		got, wantJSON := jsonOf(t, doc.Models[i]), jsonOf(t, want[i])
		if got != wantJSON {
			t.Errorf("row %d is %s, and the shared builder makes %s", i, got, wantJSON)
		}
	}
}

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

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
