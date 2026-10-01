package runner

import (
	"fmt"
	"math"
	"strconv"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// The job environment variables that carry the project's budget, set by
// fugaro init --repo on every workflow job when the budget is not off.
const (
	BudgetModeEnv  = "FUGARO_BUDGET_MODE"
	MaxRunUSDEnv   = "FUGARO_MAX_RUN_USD"
	ModelPricesEnv = pricing.EnvName
)

// Spend is the job's budget: the mode, the per-run cap and the prices the
// ledger charges.
type Spend struct {
	Mode   string         // off | observe | enforce
	Cap    pricing.Micros // 0: none
	Prices *pricing.Table // the built-in table with the owner's overrides; nil when off
}

// On reports whether the budget accounts for spend (observe or enforce).
func (s Spend) On() bool { return s.Mode == "observe" || s.Mode == "enforce" }

// SpendFromEnv reads the budget from the job's environment. Unset, or
// off, is off, whatever else is set. A malformed value is an error, which
// the runner reports as an infra_error at bootstrap: never a silent off.
func SpendFromEnv(getenv func(string) string) (Spend, error) {
	switch mode := getenv(BudgetModeEnv); mode {
	case "", "off":
		return Spend{Mode: "off"}, nil
	case "observe", "enforce":
		s := Spend{Mode: mode}
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
		o, err := pricing.ParseOverrides(getenv(ModelPricesEnv))
		if err != nil {
			return Spend{}, err
		}
		if s.Prices, err = pricing.Embedded().With(o); err != nil {
			return Spend{}, fmt.Errorf("%s: %w", ModelPricesEnv, err)
		}
		return s, nil
	default:
		return Spend{}, fmt.Errorf("%s %q: it must be off, observe or enforce", BudgetModeEnv, mode)
	}
}
