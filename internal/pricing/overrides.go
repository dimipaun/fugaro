package pricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// EnvName is the job environment variable that carries the owner's price
// overrides (the project config's model_prices).
const EnvName = "FUGARO_MODEL_PRICES"

// The list multipliers and search price an override gets for a field it
// leaves out: a missing field must never make cache writes or searches
// free.
const (
	DefaultCacheWrite5m   = 1.25
	DefaultCacheWrite1h   = 2
	DefaultCacheRead      = 0.1
	DefaultWebSearchPer1k = 10
)

// Overrides are the owner's prices, keyed by model ID. Each replaces a
// model's rates whole (see Table.With).
type Overrides map[string]Rates

// wireRates is one model's prices as the project config's model_prices
// and FUGARO_MODEL_PRICES spell them.
type wireRates struct {
	InputPerM      *float64  `json:"input_per_m"`
	OutputPerM     *float64  `json:"output_per_m"`
	CacheWrite5m   *float64  `json:"cache_write_5m,omitempty"`
	CacheWrite1h   *float64  `json:"cache_write_1h,omitempty"`
	CacheRead      *float64  `json:"cache_read,omitempty"`
	WebSearchPer1k *float64  `json:"web_search_per_1k,omitempty"`
	LongContext    *wireTier `json:"long_context,omitempty"`
}

type wireTier struct {
	AboveInputTokens int64    `json:"above_input_tokens"`
	InputPerM        *float64 `json:"input_per_m"`
	OutputPerM       *float64 `json:"output_per_m"`
}

// ParseOverrides reads FUGARO_MODEL_PRICES: compact JSON, an object keyed
// by model ID. Empty is no overrides. Unknown fields, alias keys and rates
// out of range are errors.
func ParseOverrides(s string) (Overrides, error) {
	if s == "" {
		return Overrides{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()
	var w map[string]*wireRates
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvName, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: trailing data after the JSON object", EnvName)
	}
	o := Overrides{}
	for id, wr := range w {
		if wr == nil {
			return nil, fmt.Errorf("%s: %q has no prices", EnvName, id)
		}
		r, err := wr.rates()
		if err != nil {
			return nil, fmt.Errorf("%s: %q: %w", EnvName, id, err)
		}
		o[id] = r
	}
	if err := o.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvName, err)
	}
	return o, nil
}

func (w *wireRates) rates() (Rates, error) {
	if w.InputPerM == nil || w.OutputPerM == nil {
		return Rates{}, errors.New("input_per_m and output_per_m are required")
	}
	or := func(p *float64, def float64) float64 {
		if p == nil {
			return def
		}
		return *p
	}
	r := Rates{
		InputPerM:      *w.InputPerM,
		OutputPerM:     *w.OutputPerM,
		CacheWrite5m:   or(w.CacheWrite5m, DefaultCacheWrite5m),
		CacheWrite1h:   or(w.CacheWrite1h, DefaultCacheWrite1h),
		CacheRead:      or(w.CacheRead, DefaultCacheRead),
		WebSearchPer1k: or(w.WebSearchPer1k, DefaultWebSearchPer1k),
	}
	if t := w.LongContext; t != nil {
		if t.InputPerM == nil || t.OutputPerM == nil {
			return Rates{}, errors.New("long_context needs input_per_m and output_per_m")
		}
		r.LongContext = &Tier{AboveInputTokens: t.AboveInputTokens, InputPerM: *t.InputPerM, OutputPerM: *t.OutputPerM}
	}
	return r, nil
}

// Env is the overrides as FUGARO_MODEL_PRICES: compact JSON with every
// field spelled out and the keys sorted, or "" for none.
func (o Overrides) Env() (string, error) {
	if len(o) == 0 {
		return "", nil
	}
	if err := o.validate(); err != nil {
		return "", err
	}
	w := make(map[string]wireRates, len(o))
	for id, r := range o {
		wr := wireRates{
			InputPerM: ptr(r.InputPerM), OutputPerM: ptr(r.OutputPerM),
			CacheWrite5m: ptr(r.CacheWrite5m), CacheWrite1h: ptr(r.CacheWrite1h),
			CacheRead: ptr(r.CacheRead), WebSearchPer1k: ptr(r.WebSearchPer1k),
		}
		if t := r.LongContext; t != nil {
			wr.LongContext = &wireTier{AboveInputTokens: t.AboveInputTokens, InputPerM: ptr(t.InputPerM), OutputPerM: ptr(t.OutputPerM)}
		}
		w[id] = wr
	}
	b, err := json.Marshal(w) // map keys are sorted
	if err != nil {
		return "", fmt.Errorf("%s: %w", EnvName, err)
	}
	return string(b), nil
}

func ptr(f float64) *float64 { return &f }

// validate checks every key is a model ID and every rate is in range.
func (o Overrides) validate() error {
	ids := make([]string, 0, len(o))
	for id := range o {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		if IsAlias(id) {
			errs = append(errs, fmt.Errorf("model price for %q: prices are keyed by a model ID (such as claude-sonnet-5-5), not an alias", id))
			continue
		}
		if err := o[id].Validate(); err != nil {
			errs = append(errs, fmt.Errorf("model price for %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
