package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

func TestSpendFromEnv(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	const sonnet = `{"claude-sonnet-5-5":{"input_per_m":30,"output_per_m":150}}`
	for name, tc := range map[string]struct {
		env     func(string) string
		mode    string
		cap     pricing.Micros
		on      bool
		wantErr string
	}{
		"unset":                {env(), "off", 0, false, ""},
		"off with the rest":    {env(BudgetModeEnv, "off", MaxRunUSDEnv, "5"), "off", 0, false, ""},
		"enforce and cap":      {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "12.5"), "enforce", 12_500_000, true, ""},
		"enforce without cap":  {env(BudgetModeEnv, "enforce"), "enforce", 0, true, ""},
		"observe":              {env(BudgetModeEnv, "observe"), "observe", 0, true, ""},
		"observe with cap":     {env(BudgetModeEnv, "observe", MaxRunUSDEnv, "1"), "observe", 1_000_000, true, ""},
		"bad mode":             {env(BudgetModeEnv, "strict"), "", 0, false, "FUGARO_BUDGET_MODE"},
		"bad cap":              {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "five"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"NaN cap":              {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "NaN"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"negative cap":         {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "-1"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"zero cap":             {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "0"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"cap over the maximum": {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "100001"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"cap rounding to 0":    {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "0.0000001"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"the smallest cap":     {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "0.000001"), "enforce", 1, true, ""},
		"oversized prices":     {env(BudgetModeEnv, "observe", ModelPricesEnv, `{"claude-sonnet-5-5":{"input_per_m":1,"output_per_m":2},"`+strings.Repeat("x", 40<<10)+`":{}}`), "", 0, false, "over the limit"},
		"bad prices JSON":      {env(BudgetModeEnv, "observe", ModelPricesEnv, "{nope"), "", 0, false, "FUGARO_MODEL_PRICES"},
		"prices with unknown":  {env(BudgetModeEnv, "observe", ModelPricesEnv, `{"claude-sonnet-5-5":{"input_per_m":1,"output_per_m":2,"x":3}}`), "", 0, false, "FUGARO_MODEL_PRICES"},
		"an override":          {env(BudgetModeEnv, "observe", ModelPricesEnv, sonnet), "observe", 0, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := SpendFromEnv(tc.env)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Mode != tc.mode || s.Cap != tc.cap || s.On() != tc.on {
				t.Fatalf("spend = %+v, On %v", s, s.On())
			}
			if s.On() && s.Prices == nil {
				t.Fatal("no price table with the budget on")
			}
		})
	}

	// An override replaces the built-in price; the other models keep theirs.
	s, err := SpendFromEnv(env(BudgetModeEnv, "observe", ModelPricesEnv, sonnet))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := s.Prices.Lookup("claude-sonnet-5-5")
	if !ok || m.Rates.InputPerM != 30 || m.Rates.OutputPerM != 150 {
		t.Fatalf("sonnet = %+v, %v", m, ok)
	}
	if m, ok := s.Prices.Lookup("claude-haiku-4-5"); !ok || m.Rates.InputPerM != 1 {
		t.Fatalf("haiku = %+v, %v", m, ok)
	}
}
