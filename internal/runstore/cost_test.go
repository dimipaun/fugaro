package runstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNewCost(t *testing.T) {
	api := NewCost(4.12, 0.38, BasisAPIList)
	if api.TotalUSD != 4.50 || !api.Estimate || api.ModelBasis != "api-list" {
		t.Fatalf("api-list = %+v", api)
	}
	sub := NewCost(4.12, 0.38, BasisSubscription)
	if sub.TotalUSD != 0.38 || sub.ModelUSD != 4.12 || !sub.ComputeEstimated {
		t.Fatalf("subscription = %+v", sub)
	}
	if !api.ComputeEstimated {
		t.Fatalf("api-list compute must be estimated: %+v", api)
	}
	if z := NewCost(4.12, 0, BasisAPIList); !z.ComputeEstimated || z.TotalUSD != 4.12 {
		t.Fatalf("an estimated zero = %+v", z)
	}
	if m := ModelOnlyCost(4.12, BasisAPIList); m.ComputeEstimated || m.ComputeUSD != 0 || m.TotalUSD != 4.12 || !m.Estimate {
		t.Fatalf("model only, api-list = %+v", m)
	}
	if m := ModelOnlyCost(4.12, BasisSubscription); m.ComputeEstimated || m.TotalUSD != 0 || m.ModelUSD != 4.12 {
		t.Fatalf("model only, subscription = %+v", m)
	}
	for auth, want := range map[string]string{"vertex": "api-list", "api-key": "api-list", "oauth": "subscription"} {
		if got := ModelBasis(auth); got != want {
			t.Errorf("ModelBasis(%s) = %s", auth, got)
		}
	}
}

// TestCostNotionalHalves (P12, D140): the two reader-side halves of ModelUSD.
// A record written before C1.16 carries no ModelNotionalUSD, so an oauth run's
// whole figure is its subscription's -- the basis is the fallback -- and a
// reader that split on the basis instead would bill a mixed run's tier-A
// dollars, whose basis is api-list for the whole run.
func TestCostNotionalHalves(t *testing.T) {
	mixed := NewCost(3, 0.5, BasisAPIList).WithNotional(2)
	if mixed.ModelMoneyUSD() != 1 || mixed.ModelSubscriptionUSD() != 2 || mixed.TotalUSD != 1.5 {
		t.Fatalf("mixed = %+v: money %v notional %v total %v", mixed, mixed.ModelMoneyUSD(), mixed.ModelSubscriptionUSD(), mixed.TotalUSD)
	}
	paid := NewCost(3, 0.5, BasisAPIList)
	if paid.ModelMoneyUSD() != 3 || paid.ModelSubscriptionUSD() != 0 {
		t.Fatalf("api-list = %+v: money %v notional %v", paid, paid.ModelMoneyUSD(), paid.ModelSubscriptionUSD())
	}
	// A pre-C1.16 oauth record, and one C1.16 itself wrote: both are the
	// subscription's whole figure.
	old := NewCost(3, 0.5, BasisSubscription)
	if old.ModelMoneyUSD() != 0 || old.ModelSubscriptionUSD() != 3 {
		t.Fatalf("old subscription = %+v: money %v notional %v", old, old.ModelMoneyUSD(), old.ModelSubscriptionUSD())
	}
	sub := old.WithNotional(3)
	if sub.ModelMoneyUSD() != 0 || sub.ModelSubscriptionUSD() != 3 || sub.TotalUSD != 0.5 {
		t.Fatalf("subscription = %+v: money %v notional %v total %v", sub, sub.ModelMoneyUSD(), sub.ModelSubscriptionUSD(), sub.TotalUSD)
	}
	// WithNotional clamps, so a notional part outside the model figure cannot
	// make the money part negative.
	if c := NewCost(1, 0, BasisAPIList).WithNotional(5); c.ModelMoneyUSD() != 0 || c.ModelSubscriptionUSD() != 1 {
		t.Fatalf("clamped = %+v: money %v notional %v", c, c.ModelMoneyUSD(), c.ModelSubscriptionUSD())
	}
}

// TestCostAllNotional (P12, D140): the same split asked as yes or no, which is
// how a reader that wants "was anything here billed" asks it. The two rows the
// basis cannot answer are the ones the field is for -- a mixed cascade run and
// one the plan served in full are both api-list, and only one of them billed
// nothing -- and the two rows the field cannot answer are the fallback's: a
// record written before C1.16, with a figure and with none.
func TestCostAllNotional(t *testing.T) {
	for name, tc := range map[string]struct {
		cost Cost
		want bool
	}{
		"api-key":                             {NewCost(3, 0.5, BasisAPIList), false},
		"api-key, no model figure":            {NewCost(0, 0.5, BasisAPIList), false},
		"a pre-C1.16 oauth record":            {NewCost(3, 0.5, BasisSubscription), true},
		"a pre-C1.16 oauth record, no figure": {NewCost(0, 0.5, BasisSubscription), true},
		"oauth as C1.16 writes it":            {NewCost(3, 0.5, BasisSubscription).WithNotional(3), true},
		"a mixed cascade run":                 {NewCost(3, 0.5, BasisAPIList).WithNotional(2), false},
		"a cascade run the plan paid in full": {NewCost(3, 0.5, BasisAPIList).WithNotional(3), true},
		"the same, no compute estimate":       {ModelOnlyCost(3, BasisAPIList).WithNotional(3), true},
		"a zero figure on both sides":         {NewCost(0, 0.5, BasisAPIList).WithNotional(0), false},
	} {
		if got := tc.cost.AllNotional(); got != tc.want {
			t.Errorf("%s = %+v: AllNotional() = %v, want %v (money %v, notional %v)",
				name, tc.cost, got, tc.want, tc.cost.ModelMoneyUSD(), tc.cost.ModelSubscriptionUSD())
		}
	}
	// A hand-written record whose notional part is larger than its model figure
	// is a bug in whatever wrote it; of the two answers, the one that bills
	// nothing is the safe direction, exactly as WithNotional's clamp is.
	if c := (Cost{ModelUSD: 1, ModelBasis: BasisAPIList, ModelNotionalUSD: 5}); !c.AllNotional() {
		t.Errorf("a record claiming more notional than model dollars = %+v, and AllNotional() says it billed", c)
	}
}

func TestCostModelFieldsRoundTrip(t *testing.T) {
	c := NewCost(4.12, 0.38, BasisAPIList)
	c.ModelSource = "gateway"
	c.ModelBy = map[string]float64{"claude-a": 4, "claude-b": 0.12}
	c.Unreconciled = 0.25
	c.UsageUnparsed = 2
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got Cost
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ModelSource != "gateway" || got.ModelBy["claude-a"] != 4 || got.Unreconciled != 0.25 || got.UsageUnparsed != 2 {
		t.Fatalf("round trip = %+v", got)
	}
	plain, _ := json.Marshal(NewCost(1, 0, BasisAPIList))
	for _, k := range []string{"model_source", "model_by", "unreconciled", "usage_unparsed"} {
		if strings.Contains(string(plain), k) {
			t.Fatalf("%s is written when unset: %s", k, plain)
		}
	}
}

// TestCostCacheFieldsRoundTrip (P12): the run's prompt-cache read and write
// token totals are plain sums of the gateway's per-call usage, not a dollar
// split, and a run that recorded neither writes neither key.
func TestCostCacheFieldsRoundTrip(t *testing.T) {
	c := Cost{ModelUSD: 1.23, CacheReadTokens: 400, CacheWriteTokens: 50}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	// Field by field: Cost carries maps, so the struct itself is not
	// comparable.
	var got Cost
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ModelUSD != c.ModelUSD || got.CacheReadTokens != 400 || got.CacheWriteTokens != 50 {
		t.Fatalf("round trip: got %+v", got)
	}
	if data2, _ := json.Marshal(Cost{ModelUSD: 1}); strings.Contains(string(data2), "cache_read_tokens") ||
		strings.Contains(string(data2), "cache_write_tokens") {
		t.Errorf("a zero cache total was not omitted: %s", data2)
	}
}

// TestCostModelTokensRoundTrip (F8, G1.1): the run's cost keeps its tokens per
// model, which is what a repricing of the run reads -- the counts a run's own
// calls used, model by model, so `fugaro estimate` can price them again on
// another model. The wire spelling is pinned because readers outside this
// binary draw result.json, and a wire field never changes meaning.
func TestCostModelTokensRoundTrip(t *testing.T) {
	c := Cost{ModelUSD: 1.23, ModelTokens: map[string]ModelTokenCounts{
		"claude-opus-5-5":  {Input: 1200, Output: 340, CacheRead: 40000, CacheWrite5m: 900, CacheWrite1h: 100},
		"claude-haiku-4-5": {Input: 10, Output: 5},
	}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got Cost
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ModelTokens, c.ModelTokens) {
		t.Fatalf("round trip: got %+v, want %+v", got.ModelTokens, c.ModelTokens)
	}
	var wire struct {
		ModelTokens map[string]map[string]int64 `json:"model_tokens"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"input": 1200, "output": 340, "cache_read": 40000, "cache_write_5m": 900, "cache_write_1h": 100}
	if !reflect.DeepEqual(wire.ModelTokens["claude-opus-5-5"], want) {
		t.Fatalf("model_tokens[claude-opus-5-5] = %v, want %v", wire.ModelTokens["claude-opus-5-5"], want)
	}
	// A model the path could not split, or that read no cache, carries its
	// zeroes rather than a short entry: every model's entry has the same five
	// counts, so a reader never has to tell absent from zero.
	if len(wire.ModelTokens["claude-haiku-4-5"]) != len(want) {
		t.Fatalf("model_tokens[claude-haiku-4-5] = %v, want all %d counts", wire.ModelTokens["claude-haiku-4-5"], len(want))
	}
}

// TestCostModelTokensOmittedWhenEmpty (G1.1): a run that recorded no per-model
// tokens writes no key at all, so every record written before the field
// existed, and every run whose path filled nothing, keeps its bytes.
func TestCostModelTokensOmittedWhenEmpty(t *testing.T) {
	for name, c := range map[string]Cost{
		"nil":   NewCost(1, 0, BasisAPIList),
		"empty": {ModelUSD: 1, ModelTokens: map[string]ModelTokenCounts{}},
	} {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "model_tokens") {
			t.Errorf("%s map is written: %s", name, data)
		}
	}
}
