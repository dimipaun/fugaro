// Package pricing holds the model price table the budget gateway charges
// from: per-model token rates, cache multipliers and optional long-context
// tiers, the cost of a call's reported usage, and the worst case a call can
// cost before it is sent. Money is integer micro-dollars from here to the
// ledger; float64 appears only in the rates and at the dollar edges.
package pricing

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Micros are integer micro-dollars (µ$). A price of $X per million tokens
// is exactly X µ$ per token, so rates are µ$ per token.
type Micros int64

// USD is the amount in US dollars.
func (m Micros) USD() float64 { return float64(m) / 1e6 }

// MaxUSD bounds a dollar amount FromUSD accepts.
const MaxUSD = 100_000

// FromUSD converts a dollar amount (a cap, a price) to µ$, rounded to the
// nearest µ$. It must be finite, at least 0 and at most $100,000.
func FromUSD(usd float64) (Micros, error) {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 || usd > MaxUSD {
		return 0, fmt.Errorf("%v US dollars: it must be a number from 0 to %d", usd, MaxUSD)
	}
	return Micros(math.Round(usd * 1e6)), nil
}

// Rates are one model's prices.
type Rates struct {
	InputPerM, OutputPerM                 float64 // USD per million tokens = µ$ per token
	CacheWrite5m, CacheWrite1h, CacheRead float64 // multipliers of InputPerM
	LongContext                           *Tier   // nil: one flat rate
	WebSearchPer1k                        float64 // USD per 1,000 searches
}

// Tier is a long-context price that replaces the base input and output
// rates for the whole call once its input passes a threshold.
type Tier struct {
	AboveInputTokens      int64 // applies when input + cache writes + cache reads exceed it
	InputPerM, OutputPerM float64
}

// Model is one row of the table.
type Model struct {
	ID              string
	Aliases         []string // the other spellings providers serve it under (e.g. Vertex "<id>@<date>")
	ContextTokens   int64    // the context window, for reserving PDF blocks; 0: DefaultContextTokens
	MaxOutputTokens int64    // the model's output maximum; 0: DefaultMaxOutputTokens (more is refused)
	ImageTokens     int64    // per-image token ceiling (conservative); 0: images refused
	Rates           Rates
	// Unverified marks a placeholder row nobody has checked against the
	// provider's price: it caps money but is not trusted, so pins on it
	// warn until the owner sets the real price under model_prices (which
	// replaces the row and clears this). PriceSource and PriceCheckedAt say
	// where the row came from and when; empty on a row the table's own
	// Source and CheckedAt cover.
	Unverified     bool
	PriceSource    string
	PriceCheckedAt string
	// CacheDefaulted names the cache multipliers an owner's override left
	// at 0 on a provider row and With raised to 1 (the input rate), so
	// cache tokens are never free there: a provider that reports cache
	// tokens bills them, and 0 is almost always a field left out.
	CacheDefaulted []string
}

// CodeCacheRateDefaulted marks the warning CacheWarning returns, for callers
// that report pin warnings by code.
const CodeCacheRateDefaulted = "cache_rate_defaulted"

// CacheWarning is the warning for a row whose cache rates were defaulted, or
// "" when none were.
func (m Model) CacheWarning() string {
	if len(m.CacheDefaulted) == 0 {
		return ""
	}
	return fmt.Sprintf("%s: %s left at 0 in model_prices; counted at the input price (1x) so cache tokens are not free: set the real multipliers",
		m.ID, strings.Join(m.CacheDefaulted, ", "))
}

// isProviderID reports whether a model ID is a provider's "vendor/model".
func isProviderID(id string) bool { return strings.Contains(id, "/") }

const (
	// DefaultMaxOutputTokens is a model's output maximum when the table
	// has none: the gateway refuses a larger max_tokens.
	DefaultMaxOutputTokens = 1_000_000
	// DefaultContextTokens is the context window a PDF is reserved at for
	// a model the table has none for (one an owner added by price only).
	DefaultContextTokens = 1_000_000
)

// SurpriseMultiplier scales the charge for a response priced in a
// dimension the request didn't allow (fast mode, a priority tier, a
// non-default inference geography): the table can't price it, so it is
// charged at a multiple of the table's rates.
const SurpriseMultiplier = 2

// Limits on a rate: every rate finite and at least 0, input and output
// more than 0 and at most MaxPerM, multipliers at most MaxMultiplier.
const (
	MaxPerM           = 1000
	MaxMultiplier     = 10
	MaxWebSearchPer1k = 1000
)

// Validate checks the rates are within the limits above.
func (r Rates) Validate() error {
	var errs []error
	perM := func(name string, v float64) {
		if !finite(v) || v <= 0 || v > MaxPerM {
			errs = append(errs, fmt.Errorf("%s is %v: it must be more than 0 and at most %d (US dollars per million tokens)", name, v, MaxPerM))
		}
	}
	mult := func(name string, v float64) {
		if !finite(v) || v < 0 || v > MaxMultiplier {
			errs = append(errs, fmt.Errorf("%s is %v: it must be from 0 to %d (a multiple of the input price)", name, v, MaxMultiplier))
		}
	}
	perM("input_per_m", r.InputPerM)
	perM("output_per_m", r.OutputPerM)
	mult("cache_write_5m", r.CacheWrite5m)
	mult("cache_write_1h", r.CacheWrite1h)
	mult("cache_read", r.CacheRead)
	if v := r.WebSearchPer1k; !finite(v) || v < 0 || v > MaxWebSearchPer1k {
		errs = append(errs, fmt.Errorf("web_search_per_1k is %v: it must be from 0 to %d (US dollars per 1,000 searches)", v, MaxWebSearchPer1k))
	}
	if t := r.LongContext; t != nil {
		if t.AboveInputTokens <= 0 {
			errs = append(errs, fmt.Errorf("long_context.above_input_tokens is %d: it must be more than 0", t.AboveInputTokens))
		}
		perM("long_context.input_per_m", t.InputPerM)
		perM("long_context.output_per_m", t.OutputPerM)
	}
	return errors.Join(errs...)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Table is the price table: the embedded one, or it with owner overrides.
type Table struct {
	Source    string // the pricing page's URL
	CheckedAt string // YYYY-MM-DD
	Models    map[string]Model
}

// Lookup finds a model by its exact ID or one of its aliases; it never
// matches a prefix.
func (t *Table) Lookup(model string) (Model, bool) {
	if t == nil || model == "" {
		return Model{}, false
	}
	if m, ok := t.Models[model]; ok {
		return m, true
	}
	for _, m := range t.Models {
		for _, a := range m.Aliases {
			if a == model {
				return m, true
			}
		}
	}
	return Model{}, false
}

// Unverified reports whether model's row is a placeholder nobody checked.
func (t *Table) Unverified(model string) bool {
	m, ok := t.Lookup(model)
	return ok && m.Unverified
}

// Max is the highest of every rate in the table, for a call served by a
// model the table doesn't know. A long-context tier's rates count towards
// the input and output maximum, and the result is flat. A table with no
// models (never Embedded() or anything With() returns) gives every rate
// at its validation limit rather than zero, so nothing is priced free.
func (t *Table) Max() Rates {
	var r Rates
	if t == nil || len(t.Models) == 0 {
		return Rates{InputPerM: MaxPerM, OutputPerM: MaxPerM, CacheWrite5m: MaxMultiplier,
			CacheWrite1h: MaxMultiplier, CacheRead: MaxMultiplier, WebSearchPer1k: MaxWebSearchPer1k}
	}
	for _, m := range t.Models {
		r = MaxOf(r, m.Rates)
	}
	return r
}

// MaxOf is the highest of each rate of a and b, flat: a long-context tier's
// rates count towards the input and output maximum, and the result has none.
func MaxOf(a, b Rates) Rates {
	r := Rates{
		InputPerM: max(a.InputPerM, b.InputPerM), OutputPerM: max(a.OutputPerM, b.OutputPerM),
		CacheWrite5m: max(a.CacheWrite5m, b.CacheWrite5m), CacheWrite1h: max(a.CacheWrite1h, b.CacheWrite1h),
		CacheRead: max(a.CacheRead, b.CacheRead), WebSearchPer1k: max(a.WebSearchPer1k, b.WebSearchPer1k),
	}
	for _, t := range []*Tier{a.LongContext, b.LongContext} {
		if t != nil {
			r.InputPerM, r.OutputPerM = max(r.InputPerM, t.InputPerM), max(r.OutputPerM, t.OutputPerM)
		}
	}
	return r
}

// With returns a copy of the table with the overrides applied. An
// override replaces the whole of a model's rates (found by ID or alias),
// keeping its aliases and limits (two keys for the same model, such as
// an ID and its alias, are refused); a model the table doesn't have is added
// with only its rates, so its images are refused and a PDF reserves the
// default context window.
func (t *Table) With(o Overrides) (*Table, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	out := t.clone()
	// Resolve every key to the model it prices before changing anything,
	// in a fixed order: two keys for one model (an ID and its alias)
	// would otherwise leave the price to map iteration order.
	keys := make([]string, 0, len(o))
	for key := range o {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	byID := map[string]string{}
	for _, key := range keys {
		id := key
		if m, ok := out.Lookup(key); ok {
			id = m.ID
		}
		if prev, dup := byID[id]; dup {
			return nil, fmt.Errorf("model prices: %s and %s both price %s: keep one", prev, key, id)
		}
		byID[id] = key
	}
	for _, key := range keys {
		r := o[key].clone()
		if m, ok := out.Lookup(key); ok {
			m.Rates = r
			m.Unverified, m.PriceSource, m.PriceCheckedAt = false, "", ""
			m.CacheDefaulted = defaultCacheRates(m.ID, &m.Rates)
			out.Models[m.ID] = m
			continue
		}
		m := Model{ID: key, Rates: r}
		m.CacheDefaulted = defaultCacheRates(key, &m.Rates)
		out.Models[key] = m
	}
	return out, nil
}

// defaultCacheRates raises a provider row's cache multipliers left at 0 to 1,
// the input rate, and names them: a cache token is never cheaper than an
// input token unless the owner says so with a real number, and a missing
// field must not make it free. Claude rows keep what the owner set.
func defaultCacheRates(id string, r *Rates) []string {
	if !isProviderID(id) {
		return nil
	}
	var names []string
	for _, f := range []struct {
		name string
		p    *float64
	}{{"cache_write_5m", &r.CacheWrite5m}, {"cache_write_1h", &r.CacheWrite1h}, {"cache_read", &r.CacheRead}} {
		if *f.p == 0 {
			*f.p = 1
			names = append(names, f.name)
		}
	}
	return names
}

func (t *Table) clone() *Table {
	out := &Table{Models: map[string]Model{}}
	if t == nil {
		return out
	}
	out.Source, out.CheckedAt = t.Source, t.CheckedAt
	for id, m := range t.Models {
		m.Aliases = append([]string(nil), m.Aliases...)
		m.CacheDefaulted = append([]string(nil), m.CacheDefaulted...)
		m.Rates = m.Rates.clone()
		out.Models[id] = m
	}
	return out
}

func (r Rates) clone() Rates {
	if r.LongContext != nil {
		t := *r.LongContext
		r.LongContext = &t
	}
	return r
}

// Usage is a call's reported token counts. Input is the uncached input
// only: cache writes and reads are counted apart from it.
type Usage struct {
	Input, CacheWrite5m, CacheWrite1h, CacheRead, Output, WebSearches int64
}

// inputTotal is what a long-context tier's threshold compares.
func (u Usage) inputTotal() int64 {
	return satAdd(satAdd(satAdd(max(u.Input, 0), max(u.CacheWrite5m, 0)), max(u.CacheWrite1h, 0)), max(u.CacheRead, 0))
}

// Cost prices a call's usage, rounded to the nearest µ$. A long-context
// tier applies to the whole call when the input total, cache writes and
// reads included, passes its threshold. The result saturates at the
// largest Micros rather than wrapping.
func (r Rates) Cost(u Usage) Micros {
	in, out := r.InputPerM, r.OutputPerM
	if t := r.LongContext; t != nil && u.inputTotal() > t.AboveInputTokens {
		in, out = t.InputPerM, t.OutputPerM
	}
	tok := func(n int64) float64 { return float64(max(n, 0)) }
	f := tok(u.Input)*in +
		tok(u.CacheWrite5m)*in*r.CacheWrite5m +
		tok(u.CacheWrite1h)*in*r.CacheWrite1h +
		tok(u.CacheRead)*in*r.CacheRead +
		tok(u.Output)*out +
		tok(u.WebSearches)*r.WebSearchPer1k*1000 // $ per 1,000 = 1,000 µ$ per search per $
	return toMicros(math.Round(f))
}

// UnsplitCacheWrites splits a cache_creation_input_tokens count the
// response didn't split by TTL. It is priced at the highest write
// multiplier the request allowed: with a 1-hour TTL the dearer of the two
// writes, and with a 5-minute one the 5-minute write. With no
// cache_control at all (a response that shouldn't happen) it deliberately
// takes the dearer of the two rather than the 5-minute write: nothing in
// the request says which TTL was used, so doubt charges more.
func (r Rates) UnsplitCacheWrites(tokens int64, cacheTTL string) (w5m, w1h int64) {
	if cacheTTL == "5m" || r.CacheWrite5m > r.CacheWrite1h {
		return tokens, 0
	}
	return 0, tokens
}

// WithFee adds a route fee of pct percent to an amount. The fee is rounded
// up so it is never under-charged, and the sum saturates at the largest
// Micros like the rest of the arithmetic: a fee too big to count (+Inf, or a
// product past the range) saturates rather than leaving the amount alone. A
// fee that is negative, zero or not a number adds nothing.
func WithFee(m Micros, pct float64) Micros {
	if m <= 0 || !(pct > 0) {
		return max(m, 0)
	}
	fee := math.Ceil(float64(m) * pct / 100)
	if math.IsNaN(fee) || fee >= math.MaxInt64 {
		return math.MaxInt64
	}
	return Micros(satAdd(int64(m), int64(fee)))
}

// toMicros converts a non-negative float amount of µ$, saturating.
func toMicros(f float64) Micros {
	switch {
	case math.IsNaN(f) || f >= math.MaxInt64:
		return math.MaxInt64
	case f <= 0:
		return 0
	}
	return Micros(f)
}
