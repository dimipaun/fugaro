package pricing

import (
	"math"
	"strings"
	"testing"
)

const dsID = "deepseek/deepseek-v4-flash"

func TestWithFee(t *testing.T) {
	for _, c := range []struct {
		in   Micros
		pct  float64
		want Micros
	}{
		{1000, 0, 1000},
		{1000, 5.5, 1055},
		{1001, 5.5, 1057}, // rounds up, never under-charges
		{1, 0.1, 2},
		{0, 50, 0},
		{1000, 10, 1100}, // no float noise: 1000 at 10% is exactly 1100
		{1000, math.NaN(), 1000},
		{1000, -10, 1000},
		{1000, math.Inf(-1), 1000},
		{1000, math.Inf(1), math.MaxInt64},
		{1000, 1e300, math.MaxInt64},
		{1 << 62, 1e6, math.MaxInt64},
	} {
		if got := WithFee(c.in, c.pct); got != c.want {
			t.Errorf("WithFee(%d, %v) = %d, want %d", c.in, c.pct, got, c.want)
		}
	}
	if got := WithFee(Micros(1<<63-1), 5); got != Micros(1<<63-1) {
		t.Errorf("saturated amount became %d", got)
	}
	// A bad fee never lowers the charge.
	if got := WithFee(1000, -10); got != 1000 {
		t.Errorf("negative fee gave %d", got)
	}
}

func TestUnverifiedPriceWarns(t *testing.T) {
	tbl := Embedded()
	m, ok := tbl.Lookup(dsID)
	if !ok {
		t.Fatal("the starter row for " + dsID + " is missing")
	}
	if !m.Unverified || m.PriceSource == "" || m.PriceCheckedAt == "" {
		t.Errorf("starter row must be unverified with a source and a date: %+v", m)
	}
	if err := m.Rates.Validate(); err != nil {
		t.Errorf("starter rates: %v", err)
	}
	if !tbl.Unverified(dsID) || tbl.Unverified("claude-sonnet-5-5") || tbl.Unverified("nope") {
		t.Error("Unverified wrong")
	}
}

func TestOverrideBeatsEmbedded(t *testing.T) {
	base := Embedded()
	o := Overrides{dsID: Rates{InputPerM: 0.11, OutputPerM: 0.22, CacheRead: 0.1}}
	tbl, err := base.With(o)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := tbl.Lookup(dsID)
	if m.Rates.InputPerM != 0.11 || m.Rates.OutputPerM != 0.22 {
		t.Errorf("override lost: %+v", m.Rates)
	}
	if m.Unverified || tbl.Unverified(dsID) {
		t.Error("an owner's price is the owner's: it must not stay unverified")
	}
	if b, _ := base.Lookup(dsID); !b.Unverified {
		t.Error("With changed the embedded table")
	}
	// Other rows keep their flags.
	if _, ok := tbl.Lookup("claude-sonnet-5-5"); !ok {
		t.Error("claude row lost")
	}
}

func TestMaxOf(t *testing.T) {
	a := Rates{InputPerM: 1, OutputPerM: 9, CacheRead: 0.1, CacheWrite5m: 1.25}
	b := Rates{InputPerM: 3, OutputPerM: 4, CacheRead: 0.5, LongContext: &Tier{AboveInputTokens: 10, InputPerM: 2, OutputPerM: 20}}
	got := MaxOf(a, b)
	want := Rates{InputPerM: 3, OutputPerM: 20, CacheRead: 0.5, CacheWrite5m: 1.25}
	if got != want {
		t.Errorf("MaxOf = %+v, want %+v", got, want)
	}
}

// An owner's override that leaves a provider row's cache multipliers at 0
// must not make cache tokens free: they default to the input rate, and the
// row says so. A Claude row keeps what the owner set.
func TestOverrideDefaultsZeroCacheRatesOnProviderRow(t *testing.T) {
	o := Overrides{dsID: Rates{InputPerM: 0.2, OutputPerM: 0.4, CacheRead: 0.1}, "qwen/qwen3-coder": Rates{InputPerM: 1, OutputPerM: 2}}
	tbl, err := Embedded().With(o)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := tbl.Lookup(dsID)
	if m.Rates.CacheWrite5m != 1 || m.Rates.CacheWrite1h != 1 || m.Rates.CacheRead != 0.1 {
		t.Errorf("rates %+v: writes should be 1x, the owner's read kept", m.Rates)
	}
	if len(m.CacheDefaulted) != 2 || !strings.Contains(m.CacheWarning(), "cache_write_5m, cache_write_1h") {
		t.Errorf("defaulted %v, warning %q", m.CacheDefaulted, m.CacheWarning())
	}
	if (m.Rates.Cost(Usage{CacheWrite5m: 1000})) == 0 {
		t.Error("cache writes are free")
	}
	q, _ := tbl.Lookup("qwen/qwen3-coder") // a row the table didn't have
	if q.Rates.CacheRead != 1 || len(q.CacheDefaulted) != 3 {
		t.Errorf("added row %+v %v", q.Rates, q.CacheDefaulted)
	}
	// Claude rows are the owner's word; nothing is defaulted or warned.
	c, err := Embedded().With(Overrides{"claude-sonnet-5-5": Rates{InputPerM: 2, OutputPerM: 10}})
	if err != nil {
		t.Fatal(err)
	}
	cm, _ := c.Lookup("claude-sonnet-5-5")
	if cm.Rates.CacheRead != 0 || cm.CacheWarning() != "" {
		t.Errorf("claude row %+v %q", cm.Rates, cm.CacheWarning())
	}
	// A full override, or one from the wire (which defaults the fields
	// itself), warns about nothing.
	w, err := ParseOverrides(`{"` + dsID + `":{"input_per_m":0.2,"output_per_m":0.4}}`)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := Embedded().With(w)
	if wm, _ := wt.Lookup(dsID); wm.CacheWarning() != "" {
		t.Errorf("wire override warned: %q", wm.CacheWarning())
	}
	// With does not touch the table it started from.
	if b, _ := Embedded().Lookup(dsID); b.CacheWarning() != "" {
		t.Error("embedded row carries a warning")
	}
}
