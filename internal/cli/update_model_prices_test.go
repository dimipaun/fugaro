package cli

import (
	"strings"
	"testing"
)

// cacheKeyFixture is an installation whose local config writes its cache
// prices under the old key names — the multiplication the old keys have always
// meant — and whose cache read is priced below what the built-in catalog prices
// the model's cheapest endpoint at: the model the route claims is one the
// catalog prices, and 0.0042 of a 0.14 input is $0.000588 per million tokens
// against the catalog's $0.002 floor.
//
// The two write dimensions carry values of their own rather than the list
// defaults, so a rename that kept the number a default would have put there
// anyway still reads as one: the catalog prices no cache_write endpoint tier of
// this model, so neither of them is an undercut and the run has exactly one
// warning to print.
const cacheKeyFixture = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - deepseek/*
model_prices:
  deepseek/deepseek-v4.1-flash:
    input_per_m: 0.14
    output_per_m: 0.42
    cache_read: 0.0042
    cache_write_5m: 1.4
    cache_write_1h: 2.5
`

// TestUpdateMigratesCacheReadKey is SA4.2's red step, and the state every
// installation whose owner priced a cache price by hand is in: the old key
// names still mean what they always meant (SA4.1 folds them), so the file has
// to be rewritten in place under the names this release documents — the
// owner's own file, through yamledit, with the values kept and nothing asked.
//
// Both halves are asserted here: the file, byte for byte, and the line the
// command prints about a price below the catalog's floor, which is the other
// thing R4 owes the one command the owner runs.
func TestUpdateMigratesCacheReadKey(t *testing.T) {
	r := newConvergeRig(t, v8Node())
	r.appendConfig(t, cacheKeyFixture)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	written := string(readFile(t, installationConfigPath()))
	for _, want := range []string{
		"cache_read_mult: 0.0042",
		"cache_write_5m_mult: 1.4",
		"cache_write_1h_mult: 2.5",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("after fugaro update the owner's config holds no %q:\n%s", want, written)
		}
	}
	for _, old := range []string{"cache_read:", "cache_write_5m:", "cache_write_1h:"} {
		if strings.Contains(written, old) {
			t.Errorf("after fugaro update the owner's config still writes %s, so the key this release documents is not the one the file holds:\n%s", old, written)
		}
	}
	// The prices themselves are the owner's and are not touched: a migration
	// that renamed the key and moved the number would be a price change nobody
	// asked for. The other keys of the entry say the same, and the map is what
	// every reader of the file resolves (localcfg folds the new names into the
	// same multiplier the old ones were).
	lc := parseLocalConfigBytes(t, []byte(written))
	mp := lc.ModelPrices["deepseek/deepseek-v4.1-flash"]
	for _, c := range []struct {
		what string
		got  *float64
		want float64
	}{
		{"cache_read", mp.CacheRead, 0.0042},
		{"cache_write_5m", mp.CacheWrite5m, 1.4},
		{"cache_write_1h", mp.CacheWrite1h, 2.5},
		{"input_per_m", mp.InputPerM, 0.14},
		{"output_per_m", mp.OutputPerM, 0.42},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("after the migration model_prices.deepseek/deepseek-v4.1-flash.%s = %v, want the %v the owner priced", c.what, c.got, c.want)
		}
	}
	// The one price of this entry below the catalog's floor is named, with both
	// figures and the day the catalog was checked: the owner reads what their
	// price is against, and nothing is refused.
	if n := strings.Count(out, "the override undercuts the catalog"); n != 1 {
		t.Errorf("fugaro update printed %d undercut warning(s), want the one the cache read is:\n%s", n, out)
	}
	if want := "warning: model_prices.deepseek/deepseek-v4.1-flash.cache_read_per_m is $0.000588 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $0.002 per million tokens"; !strings.Contains(out, want) {
		t.Errorf("fugaro update did not print %q:\n%s", want, out)
	}
}
