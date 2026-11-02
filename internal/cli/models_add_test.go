package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modelsAddCloudExtra is what the offline test appends to the cloud fixture's
// config: one route and one override, both written the block way, because that
// is the shape a models write can edit.
const modelsAddCloudExtra = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - deepseek/*
    allow_data_to:
      - owner/repo-a
`

// TestModelsAddRefusesAClaudeID: a Claude model is served direct, or routed by
// the owner's routing.claude switch, and a provider route never claims one
// (D785). The refusal names that switch, which is where such a model is
// routed, and nothing is written.
func TestModelsAddRefusesAClaudeID(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "add", "claude-sonnet-5-5", "--yes")
	if err == nil {
		t.Fatal("a Claude ID was added as a provider model")
	}
	if !strings.Contains(err.Error(), "routing.claude") {
		t.Errorf("the refusal does not point at routing.claude: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused add wrote the file:\n%s", got)
	}
}

// TestModelsAddRefusesAVariant: a ":" suffix is another price and routing, and
// `fugaro validate` refuses it in a pin — so the command that writes the claim
// refuses it too, rather than landing a model no launch can name.
func TestModelsAddRefusesAVariant(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "add", "vendor/model-z:free", "--price-in", "1", "--price-out", "2", "--yes")
	if err == nil {
		t.Fatal("a variant was added")
	}
	if !strings.Contains(err.Error(), "variant") {
		t.Errorf("the refusal does not say it is a variant: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused add wrote the file:\n%s", got)
	}
}

// TestModelsAddRefusesAnUnpricedModelWithoutPrices is the red step: a model the
// built-in catalog does not price is usable only once the owner prices it, and
// the command says which two flags that takes rather than writing a claim whose
// every run is refused at bootstrap (D782).
func TestModelsAddRefusesAnUnpricedModelWithoutPrices(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "add", "vendor/model-z", "--yes")
	if err == nil {
		t.Fatal("an uncatalogued model was added with no price")
	}
	for _, want := range []string{"--price-in", "--price-out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused add wrote the file:\n%s", got)
	}
	// One flag alone is not the pair the model needs.
	_, _, err = execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--yes")
	if err == nil {
		t.Fatal("a half-given price was accepted")
	}
	if !strings.Contains(err.Error(), "--price-out") {
		t.Errorf("the refusal of a lone --price-in does not name --price-out: %v", err)
	}
}

// TestModelsAddAppendsTheExactIDNeverAPattern: the claim is the one model the
// owner named, written as its exact ID (D785). A pattern would claim models
// nobody priced or reviewed, and widen where a repository's code may go by more
// than the owner typed.
func TestModelsAddAppendsTheExactIDNeverAPattern(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lc := modelsWriteLocalConfig(t, path)
	models := lc.Providers["openrouter"].Models
	if !slices.Contains(models, "vendor/model-z") {
		t.Errorf("the route does not claim the ID it was given: %v", models)
	}
	for _, m := range models {
		if strings.Contains(m, "*") && strings.HasPrefix("vendor/model-z", strings.TrimSuffix(m, "*")) {
			t.Errorf("the claim was written as a pattern: %v", models)
		}
	}
	// The price is the owner's own, in the two fields the loader requires; the
	// cache rates are left to their defaults (D782).
	p, ok := lc.ModelPrices["vendor/model-z"]
	if !ok {
		t.Fatalf("no model_prices entry was written: %v", lc.ModelPrices)
	}
	if p.InputPerM == nil || *p.InputPerM != 1 || p.OutputPerM == nil || *p.OutputPerM != 2 {
		t.Errorf("the written price is %v/%v, want 1/2", p.InputPerM, p.OutputPerM)
	}
	if p.CacheRead != nil || p.CacheWrite5m != nil {
		t.Errorf("the write invented cache rates: %v", p)
	}
}

// TestModelsAddRefusesARateTheBudgetCannotCount: a rate the price table cannot
// carry is refused by the flag that named it. A model written at 0 would run at
// no cost and one at a negative rate would credit the run, so pricing.Rates.
// Validate accepts only more than 0 and at most pricing.MaxPerM — said here as
// the flag's own refusal rather than as the loader's sentence about a file the
// owner never wrote (the read-back holds that second wall).
func TestModelsAddRefusesARateTheBudgetCannotCount(t *testing.T) {
	for _, c := range []struct {
		name     string
		in, out  string
		wantFlag string
	}{
		{"zero input", "0", "2", "--price-in"},
		{"zero output", "2", "0", "--price-out"},
		{"negative output", "2", "-1.5", "--price-out"},
		{"more than any provider charges", "1001", "2", "--price-in"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, path := modelsWriteHome(t)
			before := readFileString(t, path)

			out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", c.in, "--price-out", c.out, "--yes")
			if err == nil {
				t.Fatalf("the rate %s/%s was accepted:\n%s", c.in, c.out, out)
			}
			if !strings.Contains(err.Error(), c.wantFlag) || !strings.Contains(err.Error(), "more than 0") {
				t.Errorf("the refusal does not name the flag %s and the rule: %v", c.wantFlag, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused price wrote the file:\n%s", got)
			}
		})
	}
}

// TestModelsAddLeavesAClaimingRouteUnchanged: a route that already claims the
// ID — by the exact entry or by a pattern — is not touched, and the command
// says which claim covers the model (D785).
func TestModelsAddLeavesAClaimingRouteUnchanged(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "add", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "deepseek/*") {
		t.Errorf("the read does not say which claim covers the model:\n%s", out)
	}
	// The route's own list is what the claim is about, and it is untouched: the
	// exact ID is not appended beside the pattern that already covers it.
	lc := modelsWriteLocalConfig(t, path)
	if models := lc.Providers["openrouter"].Models; !slices.Equal(models, []string{"deepseek/*", "vendor/model-x"}) {
		t.Errorf("a route that already claims the ID gained an entry: %v", models)
	}
	// A catalog-priced claim needs no price of its own, so the file gains no
	// model_prices entry either: the only thing this add writes is the
	// allow-list entry the project's own budget requires.
	if _, ok := lc.ModelPrices["deepseek/deepseek-v4-pro"]; ok {
		t.Errorf("a price was written for a model the catalog prices: %v", lc.ModelPrices)
	}
}

// TestModelsAddAppendsToASetAllowList: a model added to a project whose
// budget.allowed_models is set has to be listed there too, or no run could use
// it (D786).
func TestModelsAddAppendsToASetAllowList(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lc := modelsWriteLocalConfig(t, path)
	if !slices.Contains(lc.Budget.AllowedModels, "vendor/model-z") {
		t.Errorf("the allow-list does not name the added model: %v", lc.Budget.AllowedModels)
	}
}

// TestModelsAddLeavesAnAbsentAllowListAbsent: no budget.allowed_models means
// every model the routes open is allowed, so writing one there would make the
// owner's config narrower than it was, and a list of one is a ceiling nobody
// asked for (D786).
func TestModelsAddLeavesAnAbsentAllowListAbsent(t *testing.T) {
	_, path := modelsWriteHomeWith(t, modelsWriteNoBudgetExtra)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); strings.Contains(got, "allowed_models") {
		t.Errorf("an absent allow-list was written:\n%s", got)
	}
	if lc := modelsWriteLocalConfig(t, path); lc.Budget != nil {
		t.Errorf("a budget block was invented: %+v", lc.Budget)
	}
}

// TestModelsAddExplainsTheSpreadAndWritesNoSwitch: a model the catalog prices
// needs no price, and the command explains what it will be charged at — the
// tier, the bound charged and the spread to the dearest endpoint of that tier,
// which is what a call served from the dearest one can be billed. It writes no
// price_policy switch: `fugaro update` does not accept the key yet, so writing
// it would refuse the owner's very next step (D795).
func TestModelsAddExplainsTheSpreadAndWritesNoSwitch(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "add", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"cheapest end", "dearest endpoint", "price_policy", "fugaro update"} {
		if !strings.Contains(out, want) {
			t.Errorf("the price note does not explain %q:\n%s", want, out)
		}
	}
	if got := readFileString(t, path); strings.Contains(got, "price_policy") {
		t.Errorf("a switch the update does not accept was written:\n%s", got)
	}
	if p := modelsWriteLocalConfig(t, path).Providers["openrouter"]; p.PricePolicy != "" {
		t.Errorf("the route's price_policy is %q, want it left unset", p.PricePolicy)
	}
}

// TestModelsAddRefusesInAnAgentSessionEvenWithYes: the file a models write
// edits is the owner's own project config, outside every repository, so a
// coding agent's session prints the diff and the command and writes nothing —
// --yes included (D789).
func TestModelsAddRefusesInAnAgentSessionEvenWithYes(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err == nil {
		t.Fatal("a models write ran in a coding agent's session with --yes")
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker it saw: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro models add vendor/model-z") {
		t.Errorf("the refusal does not print the command to run by hand: %v", err)
	}
	if !strings.Contains(out, "+ ") {
		t.Errorf("the refusal prints no diff:\n%s", out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestModelsAddAsksForTheTypedProjectNameForAClaim: appending a claim is a
// widening — the route serves the model, so a repository of this project may
// pin it and send its code there — and D846 reserves a widening for the
// project's own name, typed, the form `settings set budget.allowed_models
// --add` already asks for. --yes answers it outside a coding agent's session
// (D1107), and a run with neither the flag nor a terminal writes nothing.
func TestModelsAddAsksForTheTypedProjectNameForAClaim(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	noTerminal(t)
	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2")
	if err == nil {
		t.Fatal("a widening with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}

	// --yes is the person's own word for it without a terminal, and it is the
	// typed form it answers here and not the ordinary y/N: the refusal above is
	// the same command with the flag left off.
	out, _, err = execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("--yes did not answer the typed confirmation outside a session: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "vendor/model-z") {
		t.Errorf("--yes wrote nothing:\n%s", got)
	}

	// And the form itself: the project's name, typed at a terminal.
	_, path = modelsWriteHome(t)
	before = readFileString(t, path)
	fakeTerminal(t)
	out, _, err = executeStdin(t, "aurora\n", "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Type aurora to confirm") {
		t.Errorf("the confirmation does not ask for the project's name:\n%s", out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "vendor/model-z") {
		t.Errorf("the typed confirmation wrote nothing:\n%s", got)
	}
	if got := readFileString(t, path); got == before {
		t.Error("the confirmed write changed nothing")
	}
}

// TestModelsAddAsksForTheTypedProjectNameForAnAllowListEntry: the other
// widening of one add is the allow-list entry, which decides what a run of this
// project may use — the very key `settings set budget.allowed_models --add`
// makes the typed form — so an add whose claim the route already served is the
// typed form too, and not the ordinary y/N.
func TestModelsAddAsksForTheTypedProjectNameForAnAllowListEntry(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	noTerminal(t)
	_, _, err := execute(t, "models", "add", "deepseek/deepseek-v4-pro", "--price-in", "1", "--price-out", "2")
	if err == nil {
		t.Fatal("a widening with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestModelsAddAsksForNoTypedNameWhenNothingWidens: a model the route already
// claims and the allow-list already names leaves both widening halves alone, so
// the write of the price alone is the ordinary y/N — one `y` at a terminal, and
// no project name to type. The typed form is for the widening directions D846
// reserves it for, and not for every write.
func TestModelsAddAsksForNoTypedNameWhenNothingWidens(t *testing.T) {
	_, path := modelsWriteHome(t)

	fakeTerminal(t)
	out, _, err := executeStdin(t, "y\n", "models", "add", "vendor/model-x", "--price-in", "3", "--price-out", "4")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "Type aurora to confirm") {
		t.Errorf("a write that widens nothing asked for the project's name:\n%s", out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "input_per_m: 3") {
		t.Errorf("the ordinary confirmation did not write the price:\n%s", got)
	}
}

// TestModelsAddBacksUpAndWritesAtomically: the write keeps the old bytes in a
// backup beside the file, the way `fugaro update` rewrites the same document
// (spec §9), and the comments and keys the edit does not touch survive it
// (D790).
func TestModelsAddBacksUpAndWritesAtomically(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("%d backups, want one", len(backups))
	}
	if got := readFileString(t, backups[0]); got != before {
		t.Errorf("the backup does not hold the old bytes:\n%s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the written config is %v, want 0600", info.Mode().Perm())
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "# the owner's route: the catalog prices the deepseek models it claims") {
		t.Errorf("the edit dropped a comment it does not touch:\n%s", after)
	}
	for _, key := range []string{"vendor/model-x", "vendor/model-y", "route_fee_pct"} {
		if !strings.Contains(after, key) {
			t.Errorf("the edit dropped %q:\n%s", key, after)
		}
	}
}

// TestModelsAddPrintsOnlyFugaroUpdateNext: the hand-off is `fugaro update`, the
// one command of the HARD RULE, and never `project policy publish` — the price
// and the allow-list entry reach every run from its publish. The claim is the
// exception the help text states: it lives in the providers block each job
// holds its own copy of, so the line names `update --repo .` too — until E2's
// converge re-converges the recorded repositories from the one command, which
// the plan records M3/M4 do not ship without (D780, D787).
func TestModelsAddPrintsOnlyFugaroUpdateNext(t *testing.T) {
	_, path := modelsWriteHome(t)
	modelsWriteCheckout(t, modelsWriteHomeDir(t, path), "owner/repo-b", modelsWriteRepoYAML)

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "next: fugaro update, then fugaro update --repo . in each repository that should run the model\n") {
		t.Errorf("the hand-off does not name both halves of the change:\n%s", out)
	}
	if strings.Contains(out, "policy publish") {
		t.Errorf("the hand-off names `project policy publish`, which is not a command the owner runs:\n%s", out)
	}
}

// TestModelsAddPrintsTheOneNextLineWhenTheRouteAlreadyClaimedIt: a model the
// route already serves leaves the providers block alone, so the one command is
// the whole hand-off — the line that names `update --repo .` is for the claim
// this command wrote and not for the write itself.
func TestModelsAddPrintsTheOneNextLineWhenTheRouteAlreadyClaimedIt(t *testing.T) {
	_, path := modelsWriteHome(t)
	modelsWriteCheckout(t, modelsWriteHomeDir(t, path), "owner/repo-b", modelsWriteRepoYAML)

	out, _, err := execute(t, "models", "add", "deepseek/deepseek-v4-pro", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "next: fugaro update\n") {
		t.Errorf("the hand-off is not `next: fugaro update`:\n%s", out)
	}
	if strings.Contains(out, "update --repo") {
		t.Errorf("a write that left the providers block alone names the repository command:\n%s", out)
	}
}

// TestModelsAddWritesNothingInTheCloud: every models command is a local file
// edit whose change reaches the cloud through the next `fugaro update` (D780).
// The fakes the fixture points at are reachable and counted, so a write that
// went to the bucket, the database or a host is red here.
func TestModelsAddWritesNothingInTheCloud(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.appendConfig(t, modelsAddCloudExtra)
	t.Setenv("PATH", t.TempDir())
	path := os.Getenv("FUGARO_CONFIG")

	out, _, err := execute(t, "models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := len(f.run.Requests()) + len(f.logging.Requests()) + len(f.db.Requests()); n != 0 {
		t.Errorf("models add made %d cloud call(s), want none", n)
	}
	lc := modelsWriteLocalConfig(t, path)
	if _, ok := lc.ModelPrices["vendor/model-z"]; !ok {
		t.Errorf("the write did not land in the local project config: %v", lc.ModelPrices)
	}
}

// modelsWriteHomeDir is the config home modelsWriteHome made: the writes'
// fixture puts its checkouts under it, and a test that records one needs the
// directory again.
func modelsWriteHomeDir(t *testing.T, path string) string {
	t.Helper()
	home := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	return home
}
