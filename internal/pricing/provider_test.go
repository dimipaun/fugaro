package pricing

import "testing"

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
