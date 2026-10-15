package pricing

import (
	"fmt"
	"maps"
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

	// Overridden is the subset of the models this route claims that the owner
	// has priced by hand under model_prices. Such a model keeps the file's
	// row for its limits alone: Table.With replaces the rates and clears the
	// provenance, but it keeps the limits, and a row the override created out
	// of nothing would carry neither — raising the model's ceiling to the
	// table's defaults, so the gateway would stop refusing a max_tokens above
	// the model's real maximum. Nothing else about them is compiled, so the
	// route's switches are not a refusal there either: an owner who priced the
	// model has answered what the switches ask (design D9, P8).
	//
	// It must be disjoint from Models, and every entry must also be in the
	// Overrides passed to Table.With: until that call the row carries no rates
	// at all, and a model left out of both would be free.
	Overridden []string
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
//   - A model in r.Overridden — one the owner has priced by hand under
//     model_prices — keeps the file's row for its limits alone, with no
//     rates compiled at all: Table.With replaces those, and the route's
//     switches (the refusal below, and the ratio bound) are about a price
//     the owner has already stated. See CatalogRoute.Overridden.
//   - A require_zdr route whose model has no ZDR-eligible endpoint gets no
//     row, and Table.Refused records why: the message names the model, the
//     switch and both ways out (design D9, P8). Nothing falls back to the
//     all-endpoints set: that would price a data-policy-sensitive route
//     against a data-retaining endpoint. The refusal is recorded rather
//     than returned because a route's pattern claims every model of a
//     family: failing the table would refuse a run whose pin names a
//     different, perfectly priceable model of that family, and the next
//     model OpenRouter adds without a ZDR endpoint would break every
//     default route on upgrade. A refused model is refused where it is
//     named — a pin, or a call — which is what Table.Refusal is for.
//   - A dimension the tier does not price folds to one stated value, set
//     explicitly, never left at 0: a cache read to 1.0 (not
//     DefaultCacheRead, and never 0, because a catalog row never passes
//     through Table.With and so nothing else would raise it), the two cache
//     writes to DefaultCacheWrite5m/1h, and web search to 0 — the one
//     dimension with no conservative fold (design D7(a), plan P12).
//   - A ratio above MaxMultiplier gets no row either, with the model, the
//     dimension, both prices and the file's date recorded in Table.Refused,
//     never a clamp: clamping down would under-price and clamping up would
//     hide a model the table cannot express.
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
	// refused holds this call's refusals until every route has been walked:
	// a model two routes claim is refused only if none of them can price it.
	refused := map[string]string{}
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
				if _, seen := refused[id]; !seen {
					refused[id] = err.Error()
				}
				continue
			}
			out.Models[id] = m
			delete(refused, id)
		}
		overridden := slices.Clone(r.Overridden)
		sort.Strings(overridden)
		for _, id := range overridden {
			e, ok := file.Models[id]
			if !ok {
				continue
			}
			if IsAlias(id) {
				return nil, fmt.Errorf("the built-in price catalog is malformed: %s is not a model ID a route can claim and a pin can name", id)
			}
			out.Models[id] = catalogLimits(out.Models[id], id, e)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(refused)) {
		if _, priced := out.Models[id]; priced {
			// Another route priced it, or the table already had a row:
			// nothing is missing, so there is nothing to refuse.
			continue
		}
		if out.Refused == nil {
			out.Refused = map[string]string{}
		}
		out.Refused[id] = refused[id]
	}
	return out, nil
}

// catalogLimits is the row a model the owner priced by hand keeps: the file's
// context window and maximum output, and nothing else. Table.With replaces the
// rates and clears the provenance a moment later, but it keeps the limits —
// which is exactly why the row has to exist, because a row it has to create
// carries neither. A row already in the table keeps its own limits: a
// hand-priced model is never the place to widen a ceiling the table already
// states.
func catalogLimits(m Model, id string, e catalog.Entry) Model {
	if m.ID == "" {
		m.ID = id
	}
	if m.ContextTokens == 0 {
		m.ContextTokens = e.ContextTokens
	}
	if m.MaxOutputTokens == 0 {
		m.MaxOutputTokens = e.MaxOutputTokens
	}
	return m
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
