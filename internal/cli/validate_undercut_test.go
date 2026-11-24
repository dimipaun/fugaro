package cli

import (
	"strings"
	"testing"
)

// undercutCheckout is a checkout of a project whose owner priced a catalog
// model by hand: the route claims the deepseek models the built-in catalog
// prices, and the cache read the file sets is the caller's — 0.0042 of a 0.14
// input is $0.000588 per million tokens, below the $0.002 the catalog's
// cheapest zdr endpoint charges for the same dimension.
func undercutCheckout(t *testing.T, cacheRead string) (out, errOut string, err error) {
	t.Helper()
	f := newCloudFixture(t)
	isolateCache(t)
	f.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"+
		"model_prices:\n  deepseek/deepseek-v4.1-flash: { input_per_m: 0.14, output_per_m: 0.42, cache_read: "+cacheRead+" }\n")
	repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\nbudget: { mode: enforce, per_run_usd: 2 }\n" +
		"agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { reviewer: claude-opus-5-5, background: claude-haiku-4-5 }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	layerCheckout(t, f, repo)
	return execute(t, "validate")
}

// TestValidateWarnsUndercut: a price the owner wrote below the cheapest
// endpoint the built-in catalog prices that model at is reported, with both
// figures and the day the catalog was checked — and never refused (SA4.2/SA4.3,
// R4: an owner may hold a real price below the marketplace's, which is what
// model_prices is for).
//
// The floor is what the message is a warning ABOUT, so the second half is the
// test's own control: the same file with a price at or above the catalog's is
// valid and silent, which is what a check that always fired would fail.
func TestValidateWarnsUndercut(t *testing.T) {
	out, errOut, err := undercutCheckout(t, "0.0042")
	if err != nil {
		t.Fatalf("a cache price below the catalog's floor was refused: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "is valid") {
		t.Errorf("validate did not say the file is valid:\n%s", out)
	}
	// The day the warning names is the catalog's OWN checked_at, read from the
	// same embedded file the check reads it from (pricing.UndercutsCatalog):
	// the date moves every time the catalog is regenerated, so a literal here
	// is a test that goes red on the release that regenerates it rather than on
	// a change to the message.
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("the built-in price catalog could not be read: %v", err)
	}
	if want := "warning: model_prices.deepseek/deepseek-v4.1-flash.cache_read_per_m: is $0.000588 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $0.002 per million tokens (checked " + cat.CheckedAt + "): the override undercuts the catalog"; !strings.Contains(errOut, want) {
		t.Errorf("validate did not print:\n%s\ngot:\n%s", want, errOut)
	}

	// The same model priced from the catalog's own figure: no dimension of this
	// entry is below the floor, so the one warning is gone and nothing else
	// moved.
	out, errOut, err = undercutCheckout(t, "0.02")
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("a cache price above the catalog's floor: exit %d, %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	if strings.Contains(errOut, "undercuts the catalog") {
		t.Errorf("a price of $0.0028 per million tokens is not below the catalog's $0.002, and validate warned about it anyway:\n%s", errOut)
	}
}
