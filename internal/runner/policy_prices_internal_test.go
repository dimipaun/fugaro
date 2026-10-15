package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// slashPricedNode is a policy node the CLI writes for a model ID carrying a
// "/": the member name is the escaped one (projectpolicy.Prices), because RTDB
// refuses a key holding "." or "/" — the documented
// deepseek/deepseek-v4-flash carries the slash. Nothing here builds the policy
// in Go: the runner's read of that node is what these tests walk.
const slashPricedNode = `{"version":1,"budget":{"mode":"enforce","per_run_usd":5},` +
	`"model_prices":{"deepseek%2Fdeepseek-v4.1-flash":{"input_per_m":1,"output_per_m":2}}}`

// slashModel is the model ID the node above prices, as an operator writes it.
const slashModel = "deepseek/deepseek-v4.1-flash"

// TestPolicyPricesReadsTheNodesEscapedKeys: a run prices a model with the
// overrides the policy node carries, and the node's member names are escaped.
// The table the run meters with must therefore be keyed by the model ID: a
// table keyed by the node's escape would price a model nobody calls, and the
// model the run does call would be priced by the built-in list instead — the
// price the operator published, silently not applied.
func TestPolicyPricesReadsTheNodesEscapedKeys(t *testing.T) {
	pp, found, err := projectpolicy.Decode([]byte(slashPricedNode))
	if err != nil || !found {
		t.Fatalf("Decode = (found=%v, err=%v)", found, err)
	}
	if _, ok := pp.Prices[slashModel]; !ok {
		t.Fatalf("the decoded policy is not keyed by the model ID: %v", pp.Prices)
	}
	table, err := policyPrices(pp)
	if err != nil {
		t.Fatalf("pricing the node's overrides: %v", err)
	}
	m, ok := table.Lookup(slashModel)
	if !ok {
		t.Fatalf("the table has no entry for %q: the node's key did not reach the catalogue as a model ID", slashModel)
	}
	if m.Rates.InputPerM != 1 || m.Rates.OutputPerM != 2 {
		t.Errorf("the model's rates = %+v, want the node's override (1, 2)", m.Rates)
	}
	// And the escaped spelling is not a model: a table that has it is one the
	// escape leaked into.
	if _, ok := table.Lookup("deepseek%2Fdeepseek-v4.1-flash"); ok {
		t.Error("the table is keyed by the node's escape, so a run would meter a model nobody calls")
	}
}

// TestPolicyCeilingPricesFromTheNode: the same read through the ceiling, which
// is where the run takes its table from (resolvePolicy -> policyCeiling), so a
// policy pricing a slash-carrying model leaves the run with a table carrying
// it.
func TestPolicyCeilingPricesFromTheNode(t *testing.T) {
	pp, found, err := projectpolicy.Decode([]byte(slashPricedNode))
	if err != nil || !found {
		t.Fatalf("Decode = (found=%v, err=%v)", found, err)
	}
	_, spend, _, err := policyCeiling(pp, envCeiling(Spend{}), Spend{})
	if err != nil {
		t.Fatalf("policyCeiling: %v", err)
	}
	if spend.Prices == nil {
		t.Fatal("the ceiling carries no price table")
	}
	if _, ok := spend.Prices.Lookup(slashModel); !ok {
		t.Errorf("the ceiling's table has no entry for %q", slashModel)
	}
}
