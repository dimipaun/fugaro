package pricing

import (
	"math"
	"math/rand/v2"
	"testing"
)

func model(r Rates) Model {
	return Model{ID: "claude-test-1", ContextTokens: 200_000, MaxOutputTokens: 64_000, ImageTokens: 5000, Rates: r}
}

func TestWorstCaseCacheWrite1h(t *testing.T) {
	m := model(opusLike())
	for ttl, want := range map[string]Micros{
		"":   1000*4 + 100*20,      // no cache_control: fresh input
		"5m": 1000*4*1.25 + 100*20, // a 5-minute write at most
		"1h": 1000*4*2 + 100*20,    // a 1-hour write at most
	} {
		if got := m.WorstCase(Request{BodyBytes: 1000, MaxTokens: 100, CacheTTL: ttl}); got != want {
			t.Errorf("ttl %q: %d µ$, want %d", ttl, got, want)
		}
	}
	// A TTL it doesn't know is reserved as the dearest write.
	if got := m.WorstCase(Request{BodyBytes: 1000, MaxTokens: 100, CacheTTL: "24h"}); got != 1000*4*2+100*20 {
		t.Errorf("unknown ttl: %d µ$", got)
	}
}

func TestWorstCaseUsesOverrideCacheMultipliers(t *testing.T) {
	tb, err := testTable().With(Overrides{"claude-alpha-1": {InputPerM: 4, OutputPerM: 20, CacheWrite5m: 1.25, CacheWrite1h: 3, CacheRead: 0.05}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := tb.Lookup("claude-alpha-1")
	if got, want := m.WorstCase(Request{BodyBytes: 1000, MaxTokens: 100, CacheTTL: "1h"}), Micros(1000*4*3+100*20); got != want {
		t.Fatalf("%d µ$, want %d (the owner's 3x 1-hour write)", got, want)
	}
}

func TestWorstCaseCacheReadOverrideAboveOne(t *testing.T) {
	tb, err := testTable().With(Overrides{"claude-alpha-1": {InputPerM: 4, OutputPerM: 20, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 3}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := tb.Lookup("claude-alpha-1")
	for _, ttl := range []string{"", "5m", "1h"} {
		if got, want := m.WorstCase(Request{BodyBytes: 1000, MaxTokens: 100, CacheTTL: ttl}), Micros(1000*4*3+100*20); got != want {
			t.Errorf("ttl %q: %d µ$, want %d (cache reads priced at 3x)", ttl, got, want)
		}
	}
}

func TestWorstCaseImagesAtCeiling(t *testing.T) {
	m := model(sonnetLike())
	got := m.WorstCase(Request{BodyBytes: 1000, ImageCount: 3, MaxTokens: 0})
	if want := Micros((1000 + 3*5000) * 2); got != want {
		t.Fatalf("%d µ$, want %d: each image at its ceiling on top of the bytes", got, want)
	}
}

func TestWorstCasePDFAtContextWindow(t *testing.T) {
	m := model(sonnetLike())
	got := m.WorstCase(Request{BodyBytes: 5000, HasPDF: true, MaxTokens: 10})
	if want := Micros(200_000*2 + 10*10); got != want {
		t.Fatalf("%d µ$, want %d: a PDF reserves the whole context window", got, want)
	}
	// A model with no context window recorded (an owner-added one) is
	// reserved at the default million.
	m.ContextTokens = 0
	if got, want := m.WorstCase(Request{BodyBytes: 5000, HasPDF: true}), Micros(DefaultContextTokens*2); got != want {
		t.Fatalf("%d µ$, want %d", got, want)
	}
}

func TestWorstCaseTierFromBytes(t *testing.T) {
	m := model(tiered())
	if got, want := m.WorstCase(Request{BodyBytes: 200_000, MaxTokens: 100}), Micros(200_000*3+100*15); got != want {
		t.Errorf("at the threshold: %d µ$, want %d", got, want)
	}
	if got, want := m.WorstCase(Request{BodyBytes: 200_001, MaxTokens: 100}), Micros(200_001*6+100*22.5); got != want {
		t.Errorf("past it: %d µ$, want %d", got, want)
	}
	// Images can carry the bound past the threshold too.
	if got, want := m.WorstCase(Request{BodyBytes: 1000, ImageCount: 40, MaxTokens: 100}), Micros(201_000*6+100*22.5); got != want {
		t.Errorf("past it by images: %d µ$, want %d", got, want)
	}
}

func TestWorstCaseRoundsUp(t *testing.T) {
	m := model(Rates{InputPerM: 0.3, OutputPerM: 1.5, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1})
	// 1 × 0.3 + 1 × 1.5 = 1.8 µ$
	if got := m.WorstCase(Request{BodyBytes: 1, MaxTokens: 1}); got != 2 {
		t.Fatalf("%d µ$, want 2", got)
	}
}

func TestWorstCaseSaturates(t *testing.T) {
	top := Rates{InputPerM: 1000, OutputPerM: 1000, CacheWrite5m: 10, CacheWrite1h: 10, CacheRead: 10,
		LongContext: &Tier{AboveInputTokens: 1, InputPerM: 1000, OutputPerM: 1000}}
	near := []int64{0, 1, 1 << 20, 1 << 40, math.MaxInt64 / 1_000_000_000, math.MaxInt64 / 10_000, math.MaxInt64 / 2, math.MaxInt64 - 1, math.MaxInt64}
	rng := rand.New(rand.NewPCG(7, 11))
	pick := func() int64 {
		if rng.IntN(2) == 0 {
			return near[rng.IntN(len(near))]
		}
		return rng.Int64()
	}
	for i := 0; i < 20_000; i++ {
		m := Model{ID: "claude-test-1", ContextTokens: pick(), MaxOutputTokens: pick(), ImageTokens: pick(), Rates: top}
		if rng.IntN(3) == 0 {
			m.Rates = sonnetLike()
		}
		q := Request{BodyBytes: pick(), ImageCount: pick(), MaxTokens: pick(), HasPDF: rng.IntN(4) == 0, CacheTTL: []string{"", "5m", "1h"}[rng.IntN(3)]}
		got := m.WorstCase(q)
		if got < 0 {
			t.Fatalf("negative worst case %d for %+v %+v", got, m, q)
		}
		// More of anything never costs less: the bound can't have wrapped.
		q2 := q
		q2.MaxTokens = satAdd(q.MaxTokens, 1)
		q2.BodyBytes = satAdd(q.BodyBytes, 1)
		if got2 := m.WorstCase(q2); got2 < got {
			t.Fatalf("growing the request lowered the worst case: %d -> %d for %+v", got, got2, q)
		}
	}
	m := Model{ID: "claude-test-1", ImageTokens: math.MaxInt64, Rates: top}
	if got := m.WorstCase(Request{BodyBytes: math.MaxInt64, ImageCount: math.MaxInt64, MaxTokens: math.MaxInt64, CacheTTL: "1h"}); got != math.MaxInt64 {
		t.Fatalf("%d, want the saturated maximum", got)
	}
	// Negative inputs (which the gateway never passes) count as zero.
	if got := model(sonnetLike()).WorstCase(Request{BodyBytes: -5, ImageCount: -1, MaxTokens: -7}); got != 0 {
		t.Fatalf("negative request sizes gave %d", got)
	}
}

func TestSatArithmetic(t *testing.T) {
	if satAdd(math.MaxInt64, 1) != math.MaxInt64 || satAdd(2, 3) != 5 {
		t.Error("satAdd")
	}
	if satMul(math.MaxInt64/2, 3) != math.MaxInt64 || satMul(6, 7) != 42 || satMul(0, math.MaxInt64) != 0 {
		t.Error("satMul")
	}
}

// Over every request shape the gateway lets through, with random rates,
// tiers and owner-overridden cache multipliers, any usage whose token
// counts fit the bound (input and cache tokens within the body's bytes
// plus the images at their ceiling, or within the context window with a
// PDF; output within max_tokens; cache writes only when the TTL allows
// them; no web searches, which are refused) costs at most the worst case.
func TestWorstCaseBoundsActual(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 930))
	rate := func(lo, hi float64) float64 { return lo + rng.Float64()*(hi-lo) }
	upTo := func(n int64) int64 {
		if n <= 0 {
			return 0
		}
		switch rng.IntN(4) {
		case 0:
			return n
		case 1:
			return 0
		default:
			return rng.Int64N(n + 1)
		}
	}
	for i := 0; i < 10_000; i++ {
		base := []Rates{opusLike(), sonnetLike(), tiered()}[rng.IntN(3)]
		tb := testTable()
		tb.Models["claude-alpha-1"] = Model{ID: "claude-alpha-1", ContextTokens: 1_000_000, MaxOutputTokens: 128_000, ImageTokens: 6000, Rates: base}
		if rng.IntN(2) == 0 {
			o := base
			o.InputPerM = rate(0.01, 1000)
			o.OutputPerM = rate(0.01, 1000)
			o.CacheWrite5m = rate(0, 10)
			o.CacheWrite1h = rate(0, 10)
			o.CacheRead = rate(0, 10)
			if o.LongContext != nil {
				o.LongContext = &Tier{AboveInputTokens: 1 + rng.Int64N(1_000_000), InputPerM: rate(0.01, 1000), OutputPerM: rate(0.01, 1000)}
			}
			var err error
			if tb, err = tb.With(Overrides{"claude-alpha-1": o}); err != nil {
				t.Fatal(err)
			}
		}
		m, _ := tb.Lookup("claude-alpha-1")
		m.ContextTokens = 1 + rng.Int64N(1_000_000)
		if rng.IntN(3) == 0 {
			m.ImageTokens = 0
		}

		q := Request{
			BodyBytes: rng.Int64N(32 << 20),
			HasPDF:    rng.IntN(5) == 0,
			MaxTokens: rng.Int64N(m.MaxOutputTokens + 1),
			CacheTTL:  []string{"", "5m", "1h"}[rng.IntN(3)],
		}
		if m.ImageTokens > 0 && rng.IntN(3) == 0 {
			q.ImageCount = rng.Int64N(601)
		}
		bound := q.BodyBytes + q.ImageCount*m.ImageTokens
		if q.HasPDF {
			bound = m.ContextTokens
		}

		// Spread the input bound over the buckets the request allows.
		var u Usage
		left := upTo(bound)
		u.CacheRead = upTo(left)
		left -= u.CacheRead
		if q.CacheTTL != "" {
			u.CacheWrite5m = upTo(left)
			left -= u.CacheWrite5m
		}
		if q.CacheTTL == "1h" {
			u.CacheWrite1h = upTo(left)
			left -= u.CacheWrite1h
		}
		u.Input = left
		u.Output = upTo(q.MaxTokens)
		// Sometimes the whole bound in the single dearest bucket.
		if rng.IntN(4) == 0 {
			u = Usage{Output: q.MaxTokens}
			buckets := []*int64{&u.Input, &u.CacheRead}
			if q.CacheTTL != "" {
				buckets = append(buckets, &u.CacheWrite5m)
			}
			if q.CacheTTL == "1h" {
				buckets = append(buckets, &u.CacheWrite1h)
			}
			*buckets[rng.IntN(len(buckets))] = bound
		}

		cost, worst := m.Rates.Cost(u), m.WorstCase(q)
		if cost > worst {
			t.Fatalf("case %d: usage %+v costs %d µ$, above the worst case %d µ$ for %+v at %+v (tier %+v)",
				i, u, cost, worst, q, m.Rates, m.Rates.LongContext)
		}
	}
}

// "" means no cache_control anywhere in the request, the top-level one
// included; a cache_control without a ttl is the API's default 5-minute
// write and is passed as "5m".
func TestWorstCaseCacheTTLContract(t *testing.T) {
	m := model(Rates{InputPerM: 4, OutputPerM: 20, CacheWrite5m: 1.5, CacheWrite1h: 2, CacheRead: 0.1})
	none := m.WorstCase(Request{BodyBytes: 1000, CacheTTL: ""})
	noTTL := m.WorstCase(Request{BodyBytes: 1000, CacheTTL: "5m"})
	if none != Micros(1000*4) {
		t.Errorf("no cache_control: %d µ$, want fresh input only", none)
	}
	if noTTL != Micros(1000*4*1.5) {
		t.Errorf("a cache_control without ttl: %d µ$, want the 5-minute write", noTTL)
	}
	for _, unknown := range []string{"x", "24h", "5M"} {
		if got := m.WorstCase(Request{BodyBytes: 1000, CacheTTL: unknown}); got != Micros(1000*4*2) {
			t.Errorf("ttl %q: %d µ$, want the 1-hour write", unknown, got)
		}
	}
}
