package pricing

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/cost_*.golden")

// The tests build their own rates and tables: only TestEmbeddedTableSane
// looks at the embedded numbers.

// opusLike: $4 / $20, cache reads at 0.05x.
func opusLike() Rates {
	return Rates{InputPerM: 4, OutputPerM: 20, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.05, WebSearchPer1k: 10}
}

// sonnetLike: $2 / $10, cache reads at 0.1x.
func sonnetLike() Rates {
	return Rates{InputPerM: 2, OutputPerM: 10, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, WebSearchPer1k: 10}
}

// tiered: $3 / $15, and $6 / $22.50 above 200,000 input tokens.
func tiered() Rates {
	return Rates{InputPerM: 3, OutputPerM: 15, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, WebSearchPer1k: 10,
		LongContext: &Tier{AboveInputTokens: 200_000, InputPerM: 6, OutputPerM: 22.5}}
}

func testTable() *Table {
	return &Table{
		Source:    "https://example.com/pricing",
		CheckedAt: "2026-09-30",
		Models: map[string]Model{
			"claude-alpha-1":   {ID: "claude-alpha-1", Aliases: []string{"claude-alpha-1@20260101"}, ContextTokens: 1_000_000, MaxOutputTokens: 128_000, ImageTokens: 6000, Rates: opusLike()},
			"claude-alpha-1-5": {ID: "claude-alpha-1-5", ContextTokens: 1_000_000, MaxOutputTokens: 128_000, ImageTokens: 6000, Rates: sonnetLike()},
			"claude-beta-2":    {ID: "claude-beta-2", ContextTokens: 200_000, MaxOutputTokens: 64_000, Rates: tiered()},
		},
	}
}

func describe(r Rates, u Usage, c Micros) string {
	var b strings.Builder
	fmt.Fprintf(&b, "rates: input_per_m=%g output_per_m=%g cache_write_5m=%g cache_write_1h=%g cache_read=%g web_search_per_1k=%g",
		r.InputPerM, r.OutputPerM, r.CacheWrite5m, r.CacheWrite1h, r.CacheRead, r.WebSearchPer1k)
	if r.LongContext == nil {
		b.WriteString(" long_context=none\n")
	} else {
		fmt.Fprintf(&b, " long_context=above %d: input_per_m=%g output_per_m=%g\n",
			r.LongContext.AboveInputTokens, r.LongContext.InputPerM, r.LongContext.OutputPerM)
	}
	fmt.Fprintf(&b, "usage: input=%d cache_write_5m=%d cache_write_1h=%d cache_read=%d output=%d web_searches=%d\n",
		u.Input, u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.Output, u.WebSearches)
	fmt.Fprintf(&b, "cost: %d µ$ = $%.6f\n", int64(c), c.USD())
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "cost_"+name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs from %s (run with -update after checking):\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

// The goldens were computed by hand: each states the rates, the usage and
// the cost, so a reviewer can check the arithmetic without running Go.
func TestCostGolden(t *testing.T) {
	cases := []struct {
		name string
		r    Rates
		u    Usage
	}{
		{"plain", opusLike(), Usage{Input: 1000, Output: 500}},
		{"cache_write_5m", opusLike(), Usage{Input: 100, CacheWrite5m: 2000, Output: 10}},
		{"cache_write_1h", opusLike(), Usage{Input: 100, CacheWrite1h: 2000, Output: 10}},
		{"cache_read", sonnetLike(), Usage{Input: 50, CacheRead: 100_000, Output: 100}},
		{"web_search", opusLike(), Usage{Input: 1000, WebSearches: 3}},
		{"tier_crossed_by_cache", tiered(), Usage{Input: 150_000, CacheRead: 60_000, Output: 1000}},
		{"rounding", opusLike(), Usage{CacheRead: 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			golden(t, c.name, describe(c.r, c.u, c.r.Cost(c.u)))
		})
	}
}

func TestCostCacheReadPerModel(t *testing.T) {
	fableLike := Rates{InputPerM: 10, OutputPerM: 50, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.025}
	u := Usage{CacheRead: 1_000_000}
	for name, c := range map[string]struct {
		r    Rates
		want Micros
	}{
		"0.05x on a $4 input":   {opusLike(), 200_000},
		"0.1x on a $2 input":    {sonnetLike(), 200_000},
		"0.025x on a $10 input": {fableLike, 250_000},
	} {
		if got := c.r.Cost(u); got != c.want {
			t.Errorf("%s: a million cache-read tokens cost %d µ$, want %d", name, got, c.want)
		}
	}
}

func TestCostInputExcludesCache(t *testing.T) {
	// Input is the uncached input only: cache reads are priced on top of
	// it, never subtracted from it.
	got := sonnetLike().Cost(Usage{Input: 100, CacheRead: 1000})
	if want := Micros(100*2 + 1000*2*0.1); got != want {
		t.Fatalf("cost %d µ$, want %d", got, want)
	}
}

func TestCostTierUsesTotalInput(t *testing.T) {
	r := tiered()
	at := r.Cost(Usage{Input: 100_000, CacheWrite5m: 100_000})
	if want := Micros(100_000*3 + 100_000*3*1.25); at != want {
		t.Errorf("at the threshold: %d µ$, want the base rates' %d", at, want)
	}
	above := r.Cost(Usage{Input: 100_000, CacheWrite5m: 100_001})
	if want := Micros(math.Round(100_000*6 + 100_001*6*1.25)); above != want {
		t.Errorf("past the threshold only by counting cache writes: %d µ$, want the tier's %d", above, want)
	}
	above = r.Cost(Usage{Input: 1, CacheRead: 200_000, Output: 10})
	if want := Micros(math.Round(1*6 + 200_000*6*0.1 + 10*22.5)); above != want {
		t.Errorf("past the threshold by cache reads: %d µ$, want the tier's %d", above, want)
	}
}

func TestCostUnsplitCacheCreation(t *testing.T) {
	r := opusLike()
	cases := []struct {
		ttl            string
		r              Rates
		want5m, want1h int64
	}{
		{"1h", r, 0, 700},
		{"5m", r, 700, 0},
		// An owner override can make the 5-minute write the dearer one.
		{"1h", Rates{InputPerM: 4, OutputPerM: 20, CacheWrite5m: 3, CacheWrite1h: 2}, 700, 0},
		// No cache_control, yet the response reports cache writes: the
		// dearer of the two.
		{"", r, 0, 700},
		{"", Rates{InputPerM: 4, OutputPerM: 20, CacheWrite5m: 3, CacheWrite1h: 2}, 700, 0},
	}
	for _, c := range cases {
		w5, w1 := c.r.UnsplitCacheWrites(700, c.ttl)
		if w5 != c.want5m || w1 != c.want1h {
			t.Errorf("ttl %q, 5m %v, 1h %v: split (%d, %d), want (%d, %d)", c.ttl, c.r.CacheWrite5m, c.r.CacheWrite1h, w5, w1, c.want5m, c.want1h)
		}
	}
	// Priced at 2x as a 1-hour write: 700 × $4/M × 2.
	w5, w1 := r.UnsplitCacheWrites(700, "1h")
	if got := r.Cost(Usage{CacheWrite5m: w5, CacheWrite1h: w1}); got != 5600 {
		t.Errorf("an unsplit 1-hour write costs %d µ$, want 5600", got)
	}
}

func TestCostHugeUsageSaturates(t *testing.T) {
	r := Rates{InputPerM: 1000, OutputPerM: 1000, CacheWrite5m: 10, CacheWrite1h: 10, CacheRead: 10, WebSearchPer1k: 1000}
	got := r.Cost(Usage{Input: math.MaxInt64, CacheWrite1h: math.MaxInt64, Output: math.MaxInt64, WebSearches: math.MaxInt64})
	if got != math.MaxInt64 {
		t.Fatalf("cost %d, want the saturated maximum", got)
	}
}

func TestLookupExactAndAliasOnly(t *testing.T) {
	tb := testTable()
	if m, ok := tb.Lookup("claude-alpha-1"); !ok || m.ID != "claude-alpha-1" {
		t.Errorf("exact ID: %v %v", m.ID, ok)
	}
	if m, ok := tb.Lookup("claude-alpha-1@20260101"); !ok || m.ID != "claude-alpha-1" {
		t.Errorf("alias: %v %v", m.ID, ok)
	}
	if m, ok := tb.Lookup("claude-alpha-1-5"); !ok || m.ID != "claude-alpha-1-5" {
		t.Errorf("the longer ID must be its own model: %v %v", m.ID, ok)
	}
	for _, s := range []string{"claude-alpha", "claude-alpha-1-", "claude-alpha-1-5-6", "CLAUDE-ALPHA-1", "claude-alpha-1@2026", " claude-alpha-1", ""} {
		if m, ok := tb.Lookup(s); ok {
			t.Errorf("%q matched %s: only exact IDs and aliases match", s, m.ID)
		}
	}
	if _, ok := (*Table)(nil).Lookup("claude-alpha-1"); ok {
		t.Error("a nil table matched")
	}
}

func TestMaxRates(t *testing.T) {
	got := testTable().Max()
	want := Rates{InputPerM: 6, OutputPerM: 22.5, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, WebSearchPer1k: 10}
	if got.LongContext != nil || got.InputPerM != want.InputPerM || got.OutputPerM != want.OutputPerM ||
		got.CacheWrite5m != want.CacheWrite5m || got.CacheWrite1h != want.CacheWrite1h ||
		got.CacheRead != want.CacheRead || got.WebSearchPer1k != want.WebSearchPer1k {
		t.Fatalf("Max() = %+v, want %+v (the tier's rates count as the highest input and output)", got, want)
	}
	// Priced at the maximum, a call costs at least as much as at any
	// model's own rates.
	u := Usage{Input: 300_000, CacheWrite5m: 10, CacheWrite1h: 20, CacheRead: 30, Output: 40, WebSearches: 1}
	for id, m := range testTable().Models {
		if got.Cost(u) < m.Rates.Cost(u) {
			t.Errorf("%s costs more than the maximum rates", id)
		}
	}
}

func TestMaxOfEmptyTableIsNotFree(t *testing.T) {
	for name, tb := range map[string]*Table{"nil": nil, "empty": {}, "no models": {Models: map[string]Model{}}} {
		r := tb.Max()
		if r.InputPerM != MaxPerM || r.OutputPerM != MaxPerM || r.CacheWrite5m != MaxMultiplier ||
			r.CacheWrite1h != MaxMultiplier || r.CacheRead != MaxMultiplier || r.WebSearchPer1k != MaxWebSearchPer1k {
			t.Errorf("%s table: Max() = %+v, want every rate at its limit", name, r)
		}
	}
}

func TestFromUSD(t *testing.T) {
	for _, c := range []struct {
		usd  float64
		want Micros
	}{
		{0, 0}, {20, 20_000_000}, {0.0000014, 1}, {0.0000016, 2}, {100_000, 100_000_000_000}, {1.2345675, 1_234_568},
	} {
		got, err := FromUSD(c.usd)
		if err != nil || got != c.want {
			t.Errorf("FromUSD(%v) = %d, %v; want %d", c.usd, got, err, c.want)
		}
	}
	for _, bad := range []float64{-0.01, math.NaN(), math.Inf(1), math.Inf(-1), 100_000.01} {
		if got, err := FromUSD(bad); err == nil {
			t.Errorf("FromUSD(%v) = %d, want an error", bad, got)
		}
	}
	if got := Micros(1_500_000).USD(); got != 1.5 {
		t.Errorf("USD() = %v", got)
	}
}
