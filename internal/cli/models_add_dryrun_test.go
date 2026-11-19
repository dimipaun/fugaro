package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// modelsAddPlanJSON is the dry run's document as a consumer reads it. It is
// written out here rather than read off the CLI's own type on purpose: the
// member names are the contract (schemas/models-add-plan.schema.json is the
// other half of it), and a test that borrowed the struct would follow the
// command wherever it renamed a member.
type modelsAddPlanJSON struct {
	Version int    `json:"version"`
	Layer   string `json:"layer"`
	Path    string `json:"path"`
	Model   string `json:"model"`
	Route   string `json:"route"`
	Changed bool   `json:"changed"`
	Claim   struct {
		Key       string `json:"key"`
		Entry     string `json:"entry"`
		ClaimedBy string `json:"claimed_by"`
	} `json:"claim"`
	AllowedModels struct {
		Key     string `json:"key"`
		Entry   string `json:"entry"`
		Present bool   `json:"present"`
	} `json:"allowed_models"`
	Prices struct {
		Key        string  `json:"key"`
		InputPerM  float64 `json:"input_per_m"`
		OutputPerM float64 `json:"output_per_m"`
		Source     string  `json:"source"`
	} `json:"prices"`
	Steps []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"steps"`
	Refusal string `json:"refusal"`
}

// modelsAddPlanGoldenPath is the golden the CLI's own --dry-run --json output is
// held to (MD1).
const modelsAddPlanGoldenPath = "testdata/models_add_plan.golden.json"

var updateModelsAddPlanGolden = flag.Bool("update-models-add-plan", false, "rewrite "+modelsAddPlanGoldenPath)

// modelsAddBackupStamp is the backup's own name: the config's path with
// `.bak-<UTC stamp>` appended, the one part of a plain add's answer that a
// second run writes differently.
var modelsAddBackupStamp = regexp.MustCompile(`\.bak-[0-9]{8}T[0-9]{6}Z`)

// modelsAddDryRunJSON runs a dry run with --json and decodes the document it
// prints, which is the contract the console's Manage window draws from (MW1).
func modelsAddDryRunJSON(t *testing.T, args ...string) (modelsAddPlanJSON, string) {
	t.Helper()
	out, _, err := execute(t, append(slices.Clone(args), "--dry-run", "--json")...)
	if err != nil {
		t.Fatalf("the dry run was refused: %v\n%s", err, out)
	}
	var doc modelsAddPlanJSON
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the dry run printed no document: %v\n%s", err, out)
	}
	return doc, out
}

// modelsAddPlanNormalized is the document as the golden holds it: the one member
// that varies from run to run — the selected config's own temp path — replaced
// by the placeholder the file carries, so every other member and the shape
// itself are pinned and the golden is the same file on every machine. The member
// is checked against the fixture's own path before it is replaced, so a document
// naming the wrong file is red here rather than hidden by the rewrite.
func modelsAddPlanNormalized(t *testing.T, out, path string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the dry run printed no document: %v\n%s", err, out)
	}
	if got, _ := doc["path"].(string); got != path {
		t.Fatalf("the document names %q as the file it would write, want the selected config %s", got, path)
	}
	doc["path"] = "<config>"
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	// The placeholder is angle brackets, and the encoding/json default would
	// write them as <: the golden is read by people, so it holds the
	// placeholder itself.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestModelsAddUnchanged is written first and stays green: without the flags
// this task adds — the aiming pair, --dry-run and --json — the command behaves
// and prints byte for byte what it printed before. It pins a plain add's whole
// answer, with the two parts that vary run to run — the fixture's own temp path
// and the backup's timestamp — normalized, because those are the only parts a
// second run could differ in.
func TestModelsAddUnchanged(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := strings.ReplaceAll(out, path, "<config>")
	got = modelsAddBackupStamp.ReplaceAllString(got, ".bak-<stamp>")
	want := `<config>
  +       - vendor/model-z
  +   vendor/model-z:
  +     input_per_m: 1
  +     output_per_m: 2
  +     - vendor/model-z
this widens where code may go and raises a limit: the route then serves the model, so a repository of this project may pin it and have its code sent there, and a run of this project may use it
add vendor/model-z to the project config, on route openrouter; claim it on route openrouter; priced at $1/M in and $2/M out; budget.allowed_models gains it
wrote <config> (backup at <config>.bak-<stamp>)
model_prices.vendor/model-z carries $1/M in and $2/M out; the cache fields are left at their defaults
no repository pins vendor/model-z yet: fugaro models use owner/repo-a coder vendor/model-z
`
	if got != want {
		t.Errorf("a plain `models add` no longer prints what it printed (the flags this task adds must change nothing without them):\ngot:\n%s\nwant:\n%s", got, want)
	}

	// And the write is the same write: the claim, the allow-list entry and the
	// price it appended, with every key it did not touch left where it was.
	lc := modelsWriteLocalConfig(t, path)
	if !slices.Contains(lc.Providers["openrouter"].Models, "vendor/model-z") {
		t.Errorf("the plain add did not claim the model: %v", lc.Providers["openrouter"].Models)
	}
	if !slices.Contains(lc.Budget.AllowedModels, "vendor/model-z") {
		t.Errorf("the plain add did not widen the allow-list: %v", lc.Budget.AllowedModels)
	}
	if p, ok := lc.ModelPrices["vendor/model-z"]; !ok || p.InputPerM == nil || *p.InputPerM != 1 || p.OutputPerM == nil || *p.OutputPerM != 2 {
		t.Errorf("the plain add wrote no 1/2 price: %+v", p)
	}
	if _, ok := lc.ModelPrices["vendor/model-x"]; !ok {
		t.Errorf("the plain add dropped a price the file already carried: %v", lc.ModelPrices)
	}
}

// TestModelsAddDryRunWritesNothing is the red step: --dry-run is not a flag this
// command registers until this task adds it, and once it is, the change it
// answers for is computed and nothing else happens — no config file rewritten,
// no backup beside it, no cloud call of any kind, and no step saved for
// `fugaro apply`. The fakes the fixture points at are reachable and counted, so
// a dry run that went to the bucket, the database or a host is red here.
func TestModelsAddDryRunWritesNothing(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.appendConfig(t, modelsAddCloudExtra)
	t.Setenv("PATH", t.TempDir())
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--dry-run")
	if err != nil {
		t.Fatalf("models add --dry-run: %v\n%s", err, out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the dry run wrote the config:\n%s", got)
	}
	settingsNoBackup(t, path)
	if n := len(f.run.Requests()) + len(f.logging.Requests()) + len(f.db.Requests()); n != 0 {
		t.Errorf("the dry run made %d cloud call(s), want none: this change reaches the cloud only when its step is run", n)
	}
	if steps := pendingSteps(record(t)); len(steps) != 0 {
		t.Errorf("the dry run saved %v as owed, want nothing: it runs no step and owes none", steps)
	}

	// And it answers with the change rather than with a summary of one: the
	// model, the route it would be claimed on, and whether the file changes.
	for _, want := range []string{"vendor/model-z", "openrouter", "changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run's answer does not name %q:\n%s", want, out)
		}
	}

	// The dry run is a read, so the fence that refuses this write in a coding
	// agent's session (D789) does not refuse it — the difference a caller that
	// asks what a change would do before it asks for the change relies on — and
	// the write itself is still refused there.
	t.Setenv("CLAUDECODE", "1")
	if out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--dry-run"); err != nil {
		t.Errorf("the dry run was refused in a coding agent's session, and it is a read: %v\n%s", err, out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the dry run wrote the config in a coding agent's session:\n%s", got)
	}
	if _, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2"); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the write was not refused in a coding agent's session: %v", err)
	}
}

// TestModelsAddDryRunDocument is the document itself: one versioned JSON
// document naming the layer it would write, the entries it would append, the
// price with both rates, and the follow-up step. The golden is the CLI's own
// output with the fixture's temp path normalized (modelsAddPlanNormalized), so a
// member that moved, a rate that rounded differently or a step that changed name
// is a diff rather than a reading.
func TestModelsAddDryRunDocument(t *testing.T) {
	_, path := modelsWriteHome(t)
	doc, out := modelsAddDryRunJSON(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2")

	got := modelsAddPlanNormalized(t, out, path)
	if *updateModelsAddPlanGolden {
		if err := os.WriteFile(modelsAddPlanGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(modelsAddPlanGoldenPath)
	if err != nil {
		t.Fatalf("no golden file: write it with go test ./internal/cli -run TestModelsAddDryRunDocument -update-models-add-plan")
	}
	if got != string(want) {
		t.Errorf("the plan document changed (go test ./internal/cli -run TestModelsAddDryRunDocument -update-models-add-plan to accept):\ngot:\n%s\nwant:\n%s", got, want)
	}

	// The members a consumer reads the change off: the dry run reaches the
	// change through the write path's own computation, so each of them has to
	// be the fact the write would have acted on.
	if doc.Version != modelsAddPlanVersion {
		t.Errorf("version = %d, want modelsAddPlanVersion %d", doc.Version, modelsAddPlanVersion)
	}
	if doc.Layer != settingsLayerLocal {
		t.Errorf("layer = %q, want %q: this command edits the selected installation's own config and nothing else (D780)", doc.Layer, settingsLayerLocal)
	}
	if doc.Model != "vendor/model-z" || doc.Route != "openrouter" {
		t.Errorf("the document names %s on route %s, want vendor/model-z on openrouter", doc.Model, doc.Route)
	}
	if doc.Claim.Key != "providers.openrouter.models" || doc.Claim.Entry != "vendor/model-z" || doc.Claim.ClaimedBy != "" {
		t.Errorf("claim = %+v, want the exact ID appended to providers.openrouter.models", doc.Claim)
	}
	if doc.AllowedModels.Key != "budget.allowed_models" || doc.AllowedModels.Entry != "vendor/model-z" || !doc.AllowedModels.Present {
		t.Errorf("allowed_models = %+v, want the ID appended to a list the fixture sets", doc.AllowedModels)
	}
	if doc.Prices.Key != "model_prices.vendor/model-z" || doc.Prices.InputPerM != 1 || doc.Prices.OutputPerM != 2 || doc.Prices.Source != modelsAddPriceGiven {
		t.Errorf("prices = %+v, want both rates the caller gave", doc.Prices)
	}
	if len(doc.Steps) != 1 || doc.Steps[0].Name != initflow.Policy || doc.Steps[0].State != modelsAddPlanStepRun {
		t.Errorf("steps = %+v, want the publish the change owes, to be run", doc.Steps)
	}
	if doc.Refusal != "" {
		t.Errorf("refusal = %q, and a refusal is this program's own sentence with a non-zero exit and no document at all", doc.Refusal)
	}

	// A model the route already claims and the file already prices changes
	// nothing: the document says so, names the entry that covers it, and owes
	// no step.
	unchanged, _ := modelsAddDryRunJSON(t, "models", "add", "vendor/model-x")
	if unchanged.Changed {
		t.Errorf("adding a model the file already claims and prices would change the file: %+v", unchanged)
	}
	if unchanged.Claim.Entry != "" || unchanged.Claim.ClaimedBy != "vendor/model-x" {
		t.Errorf("claim = %+v, want no entry appended and the entry that already covers it named", unchanged.Claim)
	}
	if len(unchanged.Steps) != 0 {
		t.Errorf("steps = %+v, want none: a change that writes nothing owes nothing", unchanged.Steps)
	}
	if unchanged.Prices.Source != modelsAddPriceLocal {
		t.Errorf("prices.source = %q, want %q: the file's own model_prices prices it", unchanged.Prices.Source, modelsAddPriceLocal)
	}

	// A model the built-in catalog prices needs no entry of its own, so the
	// document is the claim alone and names the rates it is charged at and
	// where they come from.
	catalog, _ := modelsAddDryRunJSON(t, "models", "add", "deepseek/deepseek-v4-pro")
	if catalog.Prices.Source != modelsAddPriceCatalog {
		t.Errorf("prices.source = %q, want %q", catalog.Prices.Source, modelsAddPriceCatalog)
	}
	if catalog.Prices.InputPerM <= 0 || catalog.Prices.OutputPerM <= 0 {
		t.Errorf("prices = %+v, want the catalog's own rates", catalog.Prices)
	}
	if catalog.Claim.Entry != "" || catalog.Claim.ClaimedBy != "deepseek/*" {
		t.Errorf("claim = %+v, want the pattern that already claims it named", catalog.Claim)
	}

	// --no-apply is the one word that moves the follow-up: the same document
	// with the step saved rather than run, which is what apply would then do —
	// and what the console's window shows, since main sets the flag.
	saved, _ := modelsAddDryRunJSON(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--no-apply")
	if len(saved.Steps) != 1 || saved.Steps[0].Name != initflow.Policy || saved.Steps[0].State != modelsAddPlanStepSaved {
		t.Errorf("steps = %+v, want the publish saved, not run", saved.Steps)
	}
}

// TestModelsAddPlanSchema holds schemas/models-add-plan.schema.json to the
// document the command prints: the schema compiles, the document the command
// really printed validates against it, its required list is exactly the members
// modelsAddPlanDoc writes without omitempty, and version is modelsAddPlanVersion
// — the constant the schema's own $comment points at, so the contract and the
// code cannot drift apart.
func TestModelsAddPlanSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "models-add-plan.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	obj, _ := tree.(map[string]any)
	id, _ := obj["$id"].(string)
	if id == "" {
		t.Fatal("schemas/models-add-plan.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, tree); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}

	props, _ := obj["properties"].(map[string]any)
	version, _ := props["version"].(map[string]any)
	if got := fmt.Sprint(version["const"]); got != strconv.Itoa(modelsAddPlanVersion) {
		t.Errorf("version: const = %v, want modelsAddPlanVersion %d", version["const"], modelsAddPlanVersion)
	}
	if comment, _ := version["$comment"].(string); !strings.Contains(comment, "modelsAddPlanVersion") {
		t.Errorf("version: $comment = %q, and it is the pointer from the contract to the constant that writes it", comment)
	}
	required, _ := obj["required"].([]any)
	got := make([]string, 0, len(required))
	for _, r := range required {
		name, _ := r.(string)
		got = append(got, name)
	}
	if want := modelsAddPlanWrittenMembers(t); !slices.Equal(got, want) {
		t.Errorf("required = %v, and modelsAddPlanDoc writes %v: every member the document always carries is required", got, want)
	}

	// The document the command really prints is the one that validates: a
	// schema nothing was ever held against is a second contract that has never
	// been read.
	modelsWriteHome(t)
	_, out := modelsAddDryRunJSON(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2")
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the document does not match schemas/models-add-plan.schema.json: %v\n%s", err, out)
	}

	// And a document with no step list at all is refused: a consumer draws the
	// follow-up from `steps`, so a document that may leave it out is one it
	// draws a change that owes a step it never names.
	var bad map[string]any
	if err := json.Unmarshal([]byte(out), &bad); err != nil {
		t.Fatal(err)
	}
	delete(bad, "steps")
	if err := sch.Validate(bad); err == nil {
		t.Error("a document with no steps member validates, and a consumer would read it as a change that owes a step it never names")
	}
}

// modelsAddPlanWrittenMembers is the top-level members modelsAddPlanDoc writes,
// in the order the struct writes them, read off its own json tags: a member
// added there is one the schema's required list has to carry, with no second
// list here to keep in step.
func modelsAddPlanWrittenMembers(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(modelsAddPlanDoc{})
	out := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("modelsAddPlanDoc.%s carries no json member name: %q", field.Name, tag)
		}
		if slices.Contains(strings.Split(opts, ","), "omitempty") {
			t.Fatalf("modelsAddPlanDoc.%s is optional (%q), and this document's shape is a closed one: every member is written", field.Name, tag)
		}
		out = append(out, name)
	}
	return out
}

// TestModelsAddDryRunRefusesAsTheCommandDoes: a refusal the write would print is
// the same refusal under --dry-run — one sentence, a non-zero exit and no
// document at all, which is what a consumer branches on — and nothing is written
// either way. The three are a Claude ID, which no provider route may claim
// (D785); a half-given price; and a model neither flag nor catalog prices, which
// is the refusal that sends a person to the two flags.
func TestModelsAddDryRunRefusesAsTheCommandDoes(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"a Claude ID", []string{"models", "add", "claude-sonnet-5-5"}, "routing.claude"},
		{"a half-given price", []string{"models", "add", "vendor/model-z", "--price-in", "1"}, "--price-out"},
		{"a model no price can serve", []string{"models", "add", "vendor/model-z"}, "--price-in"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, path := modelsWriteHome(t)
			before := readFileString(t, path)
			_, _, plain := execute(t, c.args...)
			if plain == nil {
				t.Fatalf("the command itself accepted %s, so there is no refusal to answer for", c.name)
			}
			for _, flags := range [][]string{{"--dry-run"}, {"--dry-run", "--json"}} {
				out, _, err := execute(t, append(slices.Clone(c.args), flags...)...)
				if err == nil {
					t.Fatalf("the dry run %v accepted %s:\n%s", flags, c.name, out)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("the dry run's refusal of %s does not name %q: %v", c.name, c.want, err)
				}
				if strings.Contains(out, `"version"`) {
					t.Errorf("a refused dry run printed a document:\n%s", out)
				}
				// The sentence is the write's own, not a second one written
				// for the dry run: a consumer that branches on it reads the
				// same words either way.
				if err.Error() != plain.Error() {
					t.Errorf("the dry run refuses %s with\n%v\nand the command refuses it with\n%v\nthe two have to be one sentence", c.name, err, plain)
				}
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("a refused dry run wrote the file:\n%s", got)
			}
			settingsNoBackup(t, path)
		})
	}
}

// TestModelsAddJSONNeedsDryRun: --json is the dry run's own document, so a
// caller that asks for one has to ask for the dry run too; alone it is refused
// with the fix, and nothing is written. The console always passes both.
func TestModelsAddJSONNeedsDryRun(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--json")
	if err == nil {
		t.Fatalf("a bare --json was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("the refusal does not name --dry-run, which is the fix: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused command wrote the file:\n%s", got)
	}
	settingsNoBackup(t, path)
}

// TestModelsAddAimsTheNamedProject: --project and --config are the aiming pair
// the settings writers take (desktop-console.md §3.4 rule 3), so a caller names
// the installation this write lands in rather than whichever one the machine's
// environment would have selected. With two installations and nothing selecting
// one, the unaimed command is refused — the failure the console cannot recover
// from, since it needs the write aimed, not guessed — and each flag lands the
// write in the installation it names and in no other.
func TestModelsAddAimsTheNamedProject(t *testing.T) {
	first, second := settingsAimHome(t)
	for _, path := range []string{first, second} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, []byte(modelsWriteExtra)...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	beforeFirst, beforeSecond := readFileString(t, first), readFileString(t, second)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--no-apply")
	if err == nil {
		t.Fatalf("the unaimed command answered for one of two installations:\n%s", out)
	}
	if !strings.Contains(err.Error(), "several project configs") {
		t.Errorf("the unaimed refusal is not the one that names the choice: %v", err)
	}
	if readFileString(t, first) != beforeFirst || readFileString(t, second) != beforeSecond {
		t.Error("the unaimed command wrote an installation anyway")
	}

	if out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--no-apply", "--project", "aurora"); err != nil {
		t.Fatalf("--project aurora: %v\n%s", err, out)
	}
	if lc := modelsWriteLocalConfig(t, first); !slices.Contains(lc.Providers["openrouter"].Models, "vendor/model-z") {
		t.Errorf("--project aurora did not write aurora's file: %v", lc.Providers["openrouter"].Models)
	}
	if got := readFileString(t, second); got != beforeSecond {
		t.Errorf("--project aurora wrote the installation that was not named:\n%s", got)
	}

	firstAfter := readFileString(t, first)
	if out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--no-apply", "--config", second); err != nil {
		t.Fatalf("--config: %v\n%s", err, out)
	}
	if lc := modelsWriteLocalConfig(t, second); !slices.Contains(lc.Providers["openrouter"].Models, "vendor/model-z") {
		t.Errorf("--config did not write the file it named: %v", lc.Providers["openrouter"].Models)
	}
	if got := readFileString(t, first); got != firstAfter {
		t.Errorf("the write aimed by --config reached the other installation:\n%s", got)
	}
}
