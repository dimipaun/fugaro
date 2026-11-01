package cli

import (
	"strings"
	"testing"
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

// TestModelsSearchIsOffline: the search reads this binary's own tables and the
// selected project config, and reaches nothing (D788). With no PATH there is no
// git and no gcloud to run, and it still answers.
func TestModelsSearchIsOffline(t *testing.T) {
	modelsFixture(t)
	t.Setenv("PATH", t.TempDir())

	out, _, err := execute(t, "models", "search", "deepseek")
	if err != nil {
		t.Fatalf("the search is not offline: %v\n%s", err, out)
	}
	if !strings.Contains(out, "deepseek/deepseek-v4-flash") {
		t.Errorf("the offline search does not find a catalog row:\n%s", out)
	}
	if _, _, err := execute(t, "models", "search", "deepseek", "--cloud", "gcp"); err == nil {
		t.Error("models search takes --cloud: the search has no cloud path to select")
	}
}
