package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestModelsSearchFindsACatalogRow: a substring of a catalog model ID finds
// the row, with what a run would charge for it — the tiers the catalog carries,
// each tier's price range and endpoint count, and the model's context and
// output limits (spec §3).
func TestModelsSearchFindsACatalogRow(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"deepseek/deepseek-v4-flash", "catalog", "zdr", "all", "endpoints"} {
		if !strings.Contains(out, want) {
			t.Errorf("the hit does not show %q:\n%s", want, out)
		}
	}
	// The context and output limits the catalog carries for the row, in the
	// units a reader compares against a model's page.
	if !strings.Contains(out, "context") {
		t.Errorf("the hit does not show the context and output limits:\n%s", out)
	}
	// The search is a substring match on the ID, case-insensitively.
	upper, _, err := execute(t, "models", "search", "DEEPSEEK-V4-FLASH")
	if err != nil {
		t.Fatalf("%v\n%s", err, upper)
	}
	if !strings.Contains(upper, "deepseek/deepseek-v4-flash") {
		t.Errorf("the search is not case-insensitive:\n%s", upper)
	}
}

// TestModelsSearchFindsAnEmbeddedClaudeRow: the embedded Claude table is
// searched too, and its rows are marked embedded so a reader can tell a price
// this binary carries from one the catalog carries.
func TestModelsSearchFindsAnEmbeddedClaudeRow(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "claude-haiku")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "claude-haiku-4-5") {
		t.Errorf("the embedded Claude table is not searched:\n%s", out)
	}
	if !strings.Contains(out, "embedded") {
		t.Errorf("the hit does not say its price is the embedded table's:\n%s", out)
	}
}

// TestModelsSearchMissSaysHowToPrice: a model the catalog does not carry is
// not found, and the answer names the one way to use it anyway (D788, D782).
func TestModelsSearchMissSaysHowToPrice(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "vendor/nothing-here")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "no model matches") {
		t.Errorf("a miss does not say so:\n%s", out)
	}
	if !strings.Contains(out, "fugaro models add vendor/nothing-here --price-in X --price-out Y") {
		t.Errorf("a miss does not name the one way to use the model anyway:\n%s", out)
	}
}

// TestModelsSearchNamesTheClaimingRoute: a hit a route of the selected project
// already claims says which one, so a reader knows the model is usable before
// running anything.
func TestModelsSearchNamesTheClaimingRoute(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "openrouter") {
		t.Errorf("the hit does not name the route that claims it:\n%s", out)
	}
	// A model no route claims says so rather than staying silent: a row with no
	// route is refused at launch.
	other, _, err := execute(t, "models", "search", "kimi")
	if err != nil {
		t.Fatalf("%v\n%s", err, other)
	}
	if !strings.Contains(other, "no route") {
		t.Errorf("a hit no route claims does not say so:\n%s", other)
	}
}

// TestModelsSearchRefusesAPriceTableItCannotBuild: the search reads the owner's
// model_prices to know which rows they replace, so a table that will not build
// is the search's own error rather than a search that quietly answers without
// them — a catalog row shown where the owner set a price is a wrong price, not
// a missing one, and a model they priced would vanish from the hits.
// `models ls` and `budget prices` surface the same error, and a config that
// reaches the CLI clean cannot reach this at all (localcfg validates the
// overrides as it loads them), which is exactly why the branch is pinned here
// by the function's own contract.
func TestModelsSearchRefusesAPriceTableItCannotBuild(t *testing.T) {
	one := 1.0
	// An alias as a model_prices key: localcfg.Config.Overrides refuses it, and
	// the search must not answer from the catalog rows it could still read.
	lc := &localcfg.Config{ModelPrices: map[string]localcfg.ModelPrice{
		"sonnet": {InputPerM: &one, OutputPerM: &one},
	}}

	rows, err := modelsSearchRows(lc, "deepseek")
	if err == nil {
		t.Fatalf("the search answered from a price table it could not build: %d rows", len(rows))
	}
	if !strings.Contains(err.Error(), "alias") {
		t.Errorf("the search's error is %v, and it does not say what the table build refused", err)
	}
}

// TestModelsSearchSanitisesTheTerm: the search echoes the term it was given
// twice — quoted in the miss, and bare in the sentence naming the one way to
// use a model the catalog does not carry — and a term is a string the caller
// typed. A bidi override would let it read backwards and an ESC[31m would
// colour the rest of the terminal, so both echoes go through oneLine.
//
// Mutation (run, restored): print the term raw at models_search.go:72 and :79
// and this fails on the raw characters.
func TestModelsSearchSanitisesTheTerm(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "models", "search", "vend‮or\x1b[31m")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, raw := range []string{"‮", "\x1b"} {
		if strings.Contains(out, raw) {
			t.Errorf("the search echoes a raw %q from the term:\n%q", raw, out)
		}
	}
	if !strings.Contains(out, "vend?or") {
		t.Errorf("the term is not echoed at all:\n%s", out)
	}
}

// TestModelsSearchEscapesItsCells: a route name is a cell of the hit table,
// and a tab in it would open a column of its own and shift every column after
// it. The value is a repo's pin, so it is escaped rather than trusted
// (cellOneLine; TestModelsLsEscapesTableCells covers the same for `models ls`,
// whose writer the config also keeps tabs out of).
func TestModelsSearchEscapesItsCells(t *testing.T) {
	if got := routeCell("vendor/tab\tbed"); got != "vendor/tab?bed" {
		t.Errorf("a tab in a route name is not escaped: %q", got)
	}
	if got := routeCell(""); got != "no route" {
		t.Errorf("a model no route serves reads %q", got)
	}
}

// TestModelsSearchIsOffline: the search reads this binary's own tables and the
// selected project config, and reaches nothing (D788). An empty PATH is not
// enough to say so — it stops git and gcloud, and not a Go SDK client — so the
// fakes the project config points at are installed and counted: a search that
// went to Cloud Run, Cloud Logging or the budget database is red here.
func TestModelsSearchIsOffline(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.appendConfig(t, modelsOfflineExtra)
	t.Setenv("PATH", t.TempDir())

	out, _, err := execute(t, "models", "search", "deepseek")
	if err != nil {
		t.Fatalf("the search is not offline: %v\n%s", err, out)
	}
	if !strings.Contains(out, "deepseek/deepseek-v4-flash") {
		t.Errorf("the offline search does not find a catalog row:\n%s", out)
	}
	if n := len(f.run.Requests()) + len(f.logging.Requests()) + len(f.db.Requests()); n != 0 {
		t.Errorf("models search made %d cloud call(s), want none: the search is this binary's own tables and the project config", n)
	}
	if _, _, err := execute(t, "models", "search", "deepseek", "--cloud", "gcp"); err == nil {
		t.Error("models search takes --cloud: the search has no cloud path to select")
	}
}
