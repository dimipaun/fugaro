package pricing

import "math"

// Request is what the gateway learns from a request body before sending
// it, enough to bound what the call can cost.
type Request struct {
	BodyBytes  int64
	HasPDF     bool  // a base64 PDF block: input is bounded by ContextTokens, not bytes
	ImageCount int64 // base64 image blocks, each reserved at ImageTokens
	MaxTokens  int64 // already refused above MaxOutputTokens by the gateway
	// CacheTTL is the longest cache write the request can cause: "" only
	// when the request has no cache_control anywhere (the top-level
	// automatic one included); "5m" for a cache_control without a ttl
	// (the API's default) or with "5m"; "1h" when any asks for "1h". Any
	// other value is reserved like "1h".
	CacheTTL string
}

// WorstCase bounds what a call can cost, rounded up to the µ$:
//
//	inputBound × inputRate × cacheMult + maxTokens × outputRate
//
// inputBound is the body's bytes (a token is at least a byte) plus each
// image at the model's per-image ceiling, or the whole context window when
// the body holds a PDF. cacheMult is the dearest way the input can be
// billed, from the model's own rates (owner overrides included): cache
// reads (an override may price them above fresh input), plus a 5-minute
// write unless CacheTTL is "" (no cache_control anywhere; one without a
// ttl must be passed as "5m"), plus a 1-hour write unless CacheTTL is ""
// or "5m". A long-context tier applies when inputBound passes
// its threshold. It is a bound only for the shapes the gateway lets
// through; every other shape (server tools included, so there is no
// web-search term) is refused before this is called. The arithmetic
// saturates at the largest Micros: it never goes negative or wraps.
func (m Model) WorstCase(q Request) Micros {
	var bound int64
	if q.HasPDF {
		bound = m.ContextTokens
		if bound <= 0 {
			bound = DefaultContextTokens
		}
	} else {
		bound = satAdd(max(q.BodyBytes, 0), satMul(max(q.ImageCount, 0), max(m.ImageTokens, 0)))
	}

	r := m.Rates
	in, out := r.InputPerM, r.OutputPerM
	if t := r.LongContext; t != nil && bound > t.AboveInputTokens {
		in, out = max(in, t.InputPerM), max(out, t.OutputPerM)
	}

	mult := max(1, r.CacheRead)
	if q.CacheTTL != "" {
		mult = max(mult, r.CacheWrite5m)
	}
	if q.CacheTTL != "" && q.CacheTTL != "5m" {
		mult = max(mult, r.CacheWrite1h)
	}

	return Micros(ceilUnscale(satAdd(mulScaled(bound, in*mult), mulScaled(max(q.MaxTokens, 0), out))))
}

// rateScale turns a µ$-per-token rate into an integer number of
// millionths of a µ$ per token, so the products are integers.
const rateScale = 1_000_000

// mulScaled is n × rate for n ≥ 0 tokens, in millionths of a µ$,
// saturating.
func mulScaled(n int64, rate float64) int64 {
	if n <= 0 || rate <= 0 {
		return 0
	}
	s := rate * rateScale
	if s >= math.MaxInt64 || math.IsNaN(s) {
		return math.MaxInt64
	}
	// Round the scaled rate up, forgiving float noise below a millionth
	// of a unit (0.4 × 1e6 is 400000.00000000006, not 400001).
	scaled := math.Round(s)
	if s-scaled > 1e-6 {
		scaled = math.Ceil(s)
	}
	return satMul(n, int64(scaled))
}

// ceilUnscale turns millionths of a µ$ into µ$, rounding up; a saturated
// amount stays saturated.
func ceilUnscale(p int64) int64 {
	if p == math.MaxInt64 {
		return math.MaxInt64
	}
	q := p / rateScale
	if p%rateScale != 0 {
		q++
	}
	return q
}

// satAdd adds two non-negative int64s, saturating at math.MaxInt64.
func satAdd(a, b int64) int64 {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// satMul multiplies two non-negative int64s, saturating at math.MaxInt64.
func satMul(a, b int64) int64 {
	a, b = max(a, 0), max(b, 0)
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
