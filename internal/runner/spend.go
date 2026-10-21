package runner

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// The job environment variables that carry the project's budget (the
// ceiling), set by fugaro update on every workflow job from whatever the
// project config holds: the mode whenever a budget block is there, the cap,
// the prices, the token cap and the allow-list whatever the mode.
const (
	BudgetModeEnv  = "FUGARO_BUDGET_MODE"
	MaxRunUSDEnv   = "FUGARO_MAX_RUN_USD"
	ModelPricesEnv = pricing.EnvName
	// MaxRunTokensEnv is the ceiling on a run's total tokens, and
	// AllowedModelsEnv the comma-separated models a run may use. Both are
	// set whatever the budget mode, on every workflow job.
	MaxRunTokensEnv  = "FUGARO_MAX_RUN_TOKENS"
	AllowedModelsEnv = "FUGARO_ALLOWED_MODELS"
)

// Spend is the job's budget: the mode, the per-run cap and the prices the
// ledger charges.
type Spend struct {
	Mode   string         // off | observe | enforce
	Cap    pricing.Micros // 0: none
	Prices *pricing.Table // the built-in table with the owner's overrides; nil when off without overrides
	// MaxRunTokens is the ceiling's token cap; 0: none. It applies whatever
	// the mode.
	MaxRunTokens int64
	// AllowedModels is the ceiling's allow-list; nil: none set. A non-nil
	// list is a restriction even when empty, so test it with != nil.
	AllowedModels []string
}

// On reports whether the budget accounts for spend (observe or enforce).
func (s Spend) On() bool { return s.Mode == "observe" || s.Mode == "enforce" }

// SpendFromEnv reads the budget from the job's environment. lookup reports
// whether a variable is present, because the two policy variables must tell
// "not set" from "set to nothing": a present-but-empty one is damage to the
// ceiling and an error, never a silent unset. Unset, or off, is off, whatever
// else is set. A malformed value is an error, which the runner reports as an
// infra_error at bootstrap: never a silent off.
func SpendFromEnv(lookup func(string) (string, bool)) (Spend, error) {
	tokens, models, err := policyFromEnv(lookup)
	if err != nil {
		return Spend{}, err
	}
	s, err := spendFromEnv(lookup)
	if err != nil {
		return Spend{}, err
	}
	s.MaxRunTokens, s.AllowedModels = tokens, models
	return s, nil
}

// policyFromEnv reads the token cap and the allow-list, which hold whatever
// the budget mode is.
func policyFromEnv(lookup func(string) (string, bool)) (int64, []string, error) {
	var tokens int64
	if v, ok := lookup(MaxRunTokensEnv); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return 0, nil, fmt.Errorf("%s %q: it must be a whole number of tokens, more than 0", MaxRunTokensEnv, v)
		}
		tokens = n
	}
	var models []string
	if v, ok := lookup(AllowedModelsEnv); ok {
		// "" splits to one empty entry, which CheckModelID refuses: a
		// present-but-empty list is an error, never "no list".
		for _, m := range strings.Split(v, ",") {
			if msg := config.CheckModelID(m); msg != "" {
				return 0, nil, fmt.Errorf("%s %q: model %q %s", AllowedModelsEnv, v, m, msg)
			}
			models = append(models, m)
		}
	}
	return tokens, models, nil
}

func spendFromEnv(lookup func(string) (string, bool)) (Spend, error) {
	getenv := func(k string) string { v, _ := lookup(k); return v }
	var s Spend
	switch mode := getenv(BudgetModeEnv); mode {
	case "", "off":
		s.Mode = "off"
	case "observe", "enforce":
		s.Mode = mode
	default:
		return Spend{}, fmt.Errorf("%s %q: it must be off, observe or enforce", BudgetModeEnv, mode)
	}
	// The cap and the price overrides are the owner's ceiling whatever the
	// mode: a repository that escalates an off ceiling to enforce runs at
	// the owner's cap and prices, never at its own.
	if v := getenv(MaxRunUSDEnv); v != "" {
		usd, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(usd) || usd <= 0 {
			return Spend{}, fmt.Errorf("%s %q: it must be a number of US dollars, more than 0 and at most %d", MaxRunUSDEnv, v, pricing.MaxUSD)
		}
		if s.Cap, err = pricing.FromUSD(usd); err != nil {
			return Spend{}, fmt.Errorf("%s %q: %w", MaxRunUSDEnv, v, err)
		}
		if s.Cap < 1 {
			// A cap that rounds to 0 would read as no cap at all.
			return Spend{}, fmt.Errorf("%s %q: it rounds to nothing; the smallest cap is $0.000001", MaxRunUSDEnv, v)
		}
	}
	raw := getenv(ModelPricesEnv)
	if s.On() || raw != "" {
		o, err := pricing.ParseOverrides(raw)
		if err != nil {
			return Spend{}, err
		}
		if s.Prices, err = priceTableFromEnv(lookup, o); err != nil {
			return Spend{}, err
		}
	}
	return s, nil
}

// priceTableFromEnv is the run's price table: config.PriceTable over the
// providers the job carries and the built-in price catalog, with the owner's
// overrides. The routes come from FUGARO_MODEL_PROVIDERS, the same value the
// run routes with (Deps.Providers), so the models the run prices and the
// models it may send to a provider cannot disagree.
func priceTableFromEnv(lookup func(string) (string, bool), ov pricing.Overrides) (*pricing.Table, error) {
	providers, err := ProvidersFromEnv(lookup)
	if err != nil {
		return nil, err
	}
	return priceTable(providers, ov)
}

// loadCatalog is the embedded catalog behind a variable, the same seam the
// CLI has. A released binary's file is valid — the generator validates its own
// output with catalog.Parse and a compile-time test prices every shipped row —
// so the run's reporting of a file that will not load has no other way to be
// exercised from a test.
var loadCatalog = catalog.Load

// priceTable is config.PriceTable with the built-in catalog and the clock:
// the one place the runner builds a table, so a run with the budget off is
// priced by the same rules as one with it on.
func priceTable(providers map[string]config.ModelProvider, ov pricing.Overrides) (*pricing.Table, error) {
	cat, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	return config.PriceTable(providers, ov, cat, time.Now())
}

// priceTable is the run's own table (priceTable over the job's providers).
func (r *run) priceTable(ov pricing.Overrides) (*pricing.Table, error) {
	return priceTable(r.d.Providers, ov)
}

// catalogStale reports whether any of the named models is a row the built-in
// provider price catalog priced while the catalog was past its own 30-day
// threshold (catalog.StaleAfter). Every such row has been forced to the dearest
// end of its tier's range (design D10, P7), which result.json records and the
// run warns about once.
//
// named is the models the caller means, never the whole table: a table holds a
// stale row as soon as one catalog-eligible route claims one, and a run whose
// call was priced by hand, or never made, charged nothing at the dearest end.
// cost.catalog_stale is about the charge, so it names the run's spend by model;
// the pre-flight WARN names the models the run is pinned to, which is what it
// is about to charge.
func catalogStale(t *pricing.Table, named []string) bool {
	if t == nil {
		return false
	}
	for _, id := range named {
		if m, ok := t.Lookup(id); ok && m.Catalog != nil && m.Catalog.Stale {
			return true
		}
	}
	return false
}

// chargedModels names the run's spend by model, in a fixed order, for the
// callers that ask what a run actually charged from.
func chargedModels(by map[string]pricing.Micros) []string {
	return slices.Sorted(maps.Keys(by))
}
