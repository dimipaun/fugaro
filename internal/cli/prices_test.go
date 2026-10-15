package cli

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// cliRoute is the fixture route the CLI tests price: OpenRouter, claiming the
// DeepSeek rows the shipped catalog prices.
func cliRoute() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"deepseek/*"},
	}}
}

// TestSurfaceSeesCatalogRow pins Review Focus 10 for the CLI: the table
// `budget prices`, `validate` and `diagnose` build carries the catalog row
// for the route's model, so a surface left on a bare pricing.Embedded()
// fails here rather than printing a number no run would charge.
func TestSurfaceSeesCatalogRow(t *testing.T) {
	tbl, _, err := priceTable(&localcfg.Config{Providers: cliRoute()})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := tbl.Lookup("deepseek/deepseek-v4.1-flash")
	if !ok {
		t.Fatal("the CLI's table does not price the route's model")
	}
	if m.Catalog == nil || m.Catalog.Tier != "zdr" || m.Catalog.CheckedAt == "" {
		t.Errorf("the CLI's row is not catalog-priced: %+v", m.Catalog)
	}
	// No project config: the embedded table, and the provider model has no
	// row at all (nothing says it goes to OpenRouter).
	bare, ov, err := priceTable(nil)
	if err != nil || len(ov) != 0 {
		t.Fatalf("no project: %v %v", ov, err)
	}
	if _, ok := bare.Lookup("deepseek/deepseek-v4.1-flash"); ok {
		t.Error("a machine with no route priced a provider model")
	}
}
