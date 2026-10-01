package runner

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// The job environment variables that carry the project's budget (the
// ceiling), set by fugaro init --repo on every workflow job from whatever the
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
	getenv := func(k string) string { v, _ := lookup(k); return v }
	s, err := spendFromEnv(getenv)
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

func spendFromEnv(getenv func(string) string) (Spend, error) {
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
		if s.Prices, err = pricing.Embedded().With(o); err != nil {
			return Spend{}, fmt.Errorf("%s: %w", ModelPricesEnv, err)
		}
	}
	return s, nil
}
