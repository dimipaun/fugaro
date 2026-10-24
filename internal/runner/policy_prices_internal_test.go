package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// slashPricedNode is a policy node the CLI writes for a model ID carrying a
// "/": the member name is the escaped one (projectpolicy.Prices), because RTDB
// refuses a key holding "." or "/" — the documented
// deepseek/deepseek-v4-flash carries the slash. Nothing here builds the policy
// in Go: the runner's read of that node is what these tests walk.
const slashPricedNode = `{"version":3,"budget":{"mode":"enforce","per_run_usd":5},` +
	`"model_prices":{"deepseek%2Fdeepseek-v4.1-flash":{"input_per_m":1,"output_per_m":2}}}`

// slashModel is the model ID the node above prices, as an operator writes it.
const slashModel = "deepseek/deepseek-v4.1-flash"

// routedProviders is the job's own providers block: one route claiming the
// deepseek family, which is what the built-in catalog prices (the job carries
// this as FUGARO_MODEL_PROVIDERS, Deps.Providers).
func routedProviders() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api/v1", Auth: "bearer",
		Secret: "openrouter-key", Models: []string{"deepseek/*"},
	}}
}

// catalogPriced is a model the catalog prices under routedProviders' route but
// the node above does not mention: this is the row a route-less table drops.
const catalogPriced = "deepseek/deepseek-v4-flash"

// TestPolicyOverridesReadsTheNodesEscapedKeys: the owner's overrides are read
// off the node under the escaped member names RTDB requires and come back keyed
// by the model ID — the spelling pricing.ParseOverrides reads. A map keyed by
// the node's escape would price a model nobody calls.
func TestPolicyOverridesReadsTheNodesEscapedKeys(t *testing.T) {
	pp, found, err := projectpolicy.Decode([]byte(slashPricedNode))
	if err != nil || !found {
		t.Fatalf("Decode = (found=%v, err=%v)", found, err)
	}
	o, err := policyOverrides(pp)
	if err != nil {
		t.Fatalf("reading the node's overrides: %v", err)
	}
	if _, ok := o[slashModel]; !ok {
		t.Fatalf("the overrides are not keyed by the model ID: %v", o)
	}
	if _, ok := o["deepseek%2Fdeepseek-v4.1-flash"]; ok {
		t.Error("the overrides are keyed by the node's escape, so a run would price a model nobody calls")
	}
	if o[slashModel].InputPerM != 1 || o[slashModel].OutputPerM != 2 {
		t.Errorf("the model's rates = %+v, want the node's override (1, 2)", o[slashModel])
	}
	// A node with no prices is no overrides at all, not an empty table.
	if got, err := policyOverrides(&projectpolicy.Policy{}); err != nil || got != nil {
		t.Errorf("policyOverrides(no prices) = (%v, %v), want (nil, nil)", got, err)
	}
}

// TestPolicyCeilingKeepsTheJobsRoutesAndCatalog: the node replaces the owner's
// price OVERRIDES, not the routes the job carries or the built-in catalog under
// them. A table built as pricing.Embedded().With(the node's overrides) drops
// every catalog row the job's providers contributed, so a run pinned to a
// catalog-priced provider model is refused for "having no price under
// model_prices" — while the same job with no policy published bootstraps fine —
// and an override over a catalog row comes back materialized bare, with none of
// the file's token limits (config.PriceTable's own comment: the row is
// materialized for the limits before Table.With replaces the rates).
func TestPolicyCeilingKeepsTheJobsRoutesAndCatalog(t *testing.T) {
	pp, found, err := projectpolicy.Decode([]byte(slashPricedNode))
	if err != nil || !found {
		t.Fatalf("Decode = (found=%v, err=%v)", found, err)
	}
	r := &run{d: Deps{Providers: routedProviders()}}
	_, spend, _, err := r.policyCeiling(pp, envCeiling(Spend{}, ""), Spend{})
	if err != nil {
		t.Fatalf("policyCeiling: %v", err)
	}
	if spend.Prices == nil {
		t.Fatal("the ceiling carries no price table")
	}
	// The node's own override, by model ID.
	if m, ok := spend.Prices.Lookup(slashModel); !ok || m.Rates.InputPerM != 1 {
		t.Errorf("Lookup(%q) = (%+v, %v), want the node's override", slashModel, m, ok)
	}
	// And the row the route contributed: the model the workflow may pin.
	if _, ok := spend.Prices.Lookup(catalogPriced); !ok {
		t.Errorf("the policy's table has no price for %q: the job's routes and the built-in catalog are not the node's to replace, and a run pinned to it is refused", catalogPriced)
	}
	// The override over a catalog-priced model keeps that model's limits: an
	// override replaces the rates, not the row the file materialized.
	ovNode := []byte(`{"version":3,"budget":{"mode":"enforce","per_run_usd":5},` +
		`"model_prices":{"deepseek%2Fdeepseek-v4-flash":{"input_per_m":9,"output_per_m":9}}}`)
	p2, _, err := projectpolicy.Decode(ovNode)
	if err != nil {
		t.Fatal(err)
	}
	_, spend2, _, err := r.policyCeiling(p2, envCeiling(Spend{}, ""), Spend{})
	if err != nil {
		t.Fatalf("policyCeiling: %v", err)
	}
	m, ok := spend2.Prices.Lookup(catalogPriced)
	if !ok || m.Rates.InputPerM != 9 {
		t.Fatalf("Lookup(%q) = (%+v, %v), want the owner's override", catalogPriced, m, ok)
	}
	if m.MaxOutputTokens == 0 && m.ContextTokens == 0 {
		t.Errorf("the overridden row lost the catalog's token limits: %+v", m)
	}
}
