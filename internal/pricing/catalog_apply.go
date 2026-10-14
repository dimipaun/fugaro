package pricing

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// CatalogRoute is one route that may be priced from the built-in catalog:
// its name (for the error messages), the model IDs it claims and its two
// switches. The pattern matching stays with the caller — config.ModelProvider
// resolves which IDs a route claims with config.PatternMatches and passes
// them in — because internal/config imports internal/pricing, and the
// reverse edge would be a cycle.
type CatalogRoute struct {
	Name       string
	Models     []string // the model IDs this route claims, already resolved
	RequireZDR bool     // price the ZDR-eligible endpoints only (the default)
	Policy     string   // "cheapest" (also ""), or "highest"
}

// ApplyCatalog compiles the loaded catalog into t: for every route, every
// model the route claims that the file prices gets a row whose rates are
// the selected tier's selected bound. It is the one place a catalog entry
// becomes money (design D5), so every surface that prices anything calls
// the builder that calls this, and none reaches for Embedded() directly.
//
// The rules it enforces:
//
//   - A model the file does not price is left alone: a route that claims a
//     model OpenRouter does not serve, or a model no route claims, keeps
//     the existing refusal rather than a marketplace guess.
//   - A require_zdr route whose model has no ZDR-eligible endpoint is an
//     error naming the model, the switch and both ways out (design D9, P8).
//     Nothing falls back to the all-endpoints set: that would price a
//     data-policy-sensitive route against a data-retaining endpoint.
//   - A dimension the tier does not price folds to one stated value, set
//     explicitly, never left at 0: a cache read to 1.0 (not
//     DefaultCacheRead, and never 0, because a catalog row never passes
//     through Table.With and so nothing else would raise it), the two cache
//     writes to DefaultCacheWrite5m/1h, and web search to 0 — the one
//     dimension with no conservative fold (design D7(a), plan P12).
//   - A ratio above MaxMultiplier is an error naming the model, the
//     dimension, both prices and the file's date, never a clamp: clamping
//     down would under-price and clamping up would hide a model the table
//     cannot express.
//   - A checked_at past catalog.StaleAfter forces the selected tier's
//     maximum whatever the policy, and marks the row stale: an old catalog
//     over-counts, and never under-prices (design D10, P7).
//
// now is the clock, injected so the staleness tests are not time-dependent.
func ApplyCatalog(t *Table, file *catalog.File, routes []CatalogRoute, now time.Time) (*Table, error) {
	if file == nil || len(file.Models) == 0 {
		return t, nil
	}
	checkedAt, err := time.Parse("2006-01-02", file.CheckedAt)
	if err != nil {
		return nil, fmt.Errorf("the built-in price catalog is malformed: checked_at %q is not YYYY-MM-DD", file.CheckedAt)
	}
	stale := now.Sub(checkedAt) > catalog.StaleAfter
	source := ""
	if len(file.Sources) > 0 {
		source = file.Sources[0]
	}
	out := t.clone()
	rs := slices.Clone(routes)
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	for _, r := range rs {
		models := slices.Clone(r.Models)
		sort.Strings(models)
		for _, id := range models {
			e, ok := file.Models[id]
			if !ok {
				continue
			}
			if IsAlias(id) {
				return nil, fmt.Errorf("the built-in price catalog is malformed: %s is not a model ID a route can claim and a pin can name", id)
			}
			m, err := catalogModel(id, r, e, source, file.CheckedAt, stale)
			if err != nil {
				return nil, err
			}
			out.Models[id] = m
		}
	}
	return out, nil
}

// catalogModel is one compiled row.
func catalogModel(id string, r CatalogRoute, e catalog.Entry, source, checkedAt string, stale bool) (Model, error) {
	tier, name := e.All, "all"
	if r.RequireZDR {
		tier, name = e.ZDR, "zdr"
		if len(tier) == 0 {
			return Model{}, fmt.Errorf("%s is routed to %s with require_zdr: true, but the built-in price catalog has no ZDR-eligible endpoint for it: set require_zdr: false on the route, or price the model under model_prices", id, r.Name)
		}
	}
	bound := "min"
	if r.Policy == "highest" {
		bound = "max"
	}
	if stale {
		// A stale catalog charges the safe end whatever the policy asked
		// for: over-counting, never under-pricing (design D10).
		bound = "max"
	}
	rates, err := catalogRates(id, tier, bound, checkedAt)
	if err != nil {
		return Model{}, err
	}
	return Model{
		ID: id, ContextTokens: e.ContextTokens, MaxOutputTokens: e.MaxOutputTokens,
		Rates: rates,
		Catalog: &CatalogPrice{
			Tier: name, Bound: bound, Endpoints: e.Endpoints, ZdrEndpoints: e.ZDREndpoints,
			Source: source, CheckedAt: checkedAt, Stale: stale,
		},
	}, nil
}

// catalogRates is the tier's bound compiled into rates: input and output
// are the bound's prices, each cache dimension is the bound's price over
// the bound's input price, and a dimension the tier does not price takes
// its fold value.
func catalogRates(id string, tier map[string]catalog.Bound, bound, checkedAt string) (Rates, error) {
	at := func(dim string) (float64, bool, error) {
		b, ok := tier[dim]
		if !ok {
			return 0, false, nil
		}
		s := b.Min
		if bound == "max" {
			s = b.Max
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false, fmt.Errorf("the built-in price catalog is malformed: %s %s.%s %q is not a number", id, dim, bound, s)
		}
		return v, true, nil
	}
	in, ok, err := at("input_per_m")
	if err != nil {
		return Rates{}, err
	}
	if !ok {
		return Rates{}, fmt.Errorf("the built-in price catalog is malformed: %s prices no input_per_m", id)
	}
	out, ok, err := at("output_per_m")
	if err != nil {
		return Rates{}, err
	}
	if !ok {
		return Rates{}, fmt.Errorf("the built-in price catalog is malformed: %s prices no output_per_m", id)
	}
	r := Rates{InputPerM: in, OutputPerM: out}
	// ratio is a cache dimension as a multiple of the input price, which is
	// how Rates expresses it.
	ratio := func(dim string, fold float64) (float64, error) {
		v, ok, err := at(dim)
		if err != nil || !ok {
			return fold, err
		}
		if v/in > MaxMultiplier {
			return 0, fmt.Errorf("the built-in price catalog cannot express %s: its %s is $%v per million against an input price of $%v (%s, checked %s), a multiple of %v, over the limit of %d: price it under model_prices",
				id, dim, v, in, bound, checkedAt, v/in, MaxMultiplier)
		}
		return v / in, nil
	}
	if r.CacheRead, err = ratio("cache_read_per_m", 1); err != nil {
		return Rates{}, err
	}
	if r.CacheWrite5m, err = ratio("cache_write_5m_per_m", DefaultCacheWrite5m); err != nil {
		return Rates{}, err
	}
	if r.CacheWrite1h, err = ratio("cache_write_1h_per_m", DefaultCacheWrite1h); err != nil {
		return Rates{}, err
	}
	// Web search is a per-search price with no input-price equivalent, so a
	// tier that prices no search charges none: the one dimension with no
	// conservative fold (design D7(a), U7).
	if r.WebSearchPer1k, _, err = at("web_search_per_1k"); err != nil {
		return Rates{}, err
	}
	if err := r.Validate(); err != nil {
		return Rates{}, fmt.Errorf("the built-in price catalog cannot express %s at the %s bound: %w", id, bound, err)
	}
	return r, nil
}
