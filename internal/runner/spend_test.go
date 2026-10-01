package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

func env(kv ...string) func(string) (string, bool) {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestSpendFromEnv(t *testing.T) {
	const sonnet = `{"claude-sonnet-5-5":{"input_per_m":30,"output_per_m":150}}`
	for name, tc := range map[string]struct {
		env     func(string) (string, bool)
		mode    string
		cap     pricing.Micros
		on      bool
		wantErr string
	}{
		"unset":                {env(), "off", 0, false, ""},
		"off with the rest":    {env(BudgetModeEnv, "off", MaxRunUSDEnv, "5"), "off", 5_000_000, false, ""}, // the ceiling holds whatever the mode
		"cap, mode unset":      {env(MaxRunUSDEnv, "5"), "off", 5_000_000, false, ""},
		"bad cap, mode off":    {env(BudgetModeEnv, "off", MaxRunUSDEnv, "five"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"bad cap, mode unset":  {env(MaxRunUSDEnv, "0"), "", 0, false, "FUGARO_MAX_RUN_USD"},
		"bad prices, mode off": {env(BudgetModeEnv, "off", ModelPricesEnv, "{nope"), "", 0, false, "FUGARO_MODEL_PRICES"},
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

func TestSpendFromEnvPolicyKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		env    func(string) (string, bool)
		tokens int64
		models []string
		err    string
	}{
		"neither":                {env(BudgetModeEnv, "observe"), 0, nil, ""},
		"tokens":                 {env(MaxRunTokensEnv, "250000"), 250000, nil, ""},
		"both, mode off":         {env(BudgetModeEnv, "off", MaxRunTokensEnv, "7", AllowedModelsEnv, "claude-a-1,claude-b-2"), 7, []string{"claude-a-1", "claude-b-2"}, ""},
		"enforce and both":       {env(BudgetModeEnv, "enforce", MaxRunUSDEnv, "5", MaxRunTokensEnv, "7", AllowedModelsEnv, "claude-a-1"), 7, []string{"claude-a-1"}, ""},
		"empty tokens":           {env(MaxRunTokensEnv, ""), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"empty models":           {env(AllowedModelsEnv, ""), 0, nil, "FUGARO_ALLOWED_MODELS"},
		"empty tokens, mode off": {env(BudgetModeEnv, "off", MaxRunTokensEnv, ""), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"zero tokens":            {env(MaxRunTokensEnv, "0"), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"negative tokens":        {env(MaxRunTokensEnv, "-5"), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"float tokens":           {env(MaxRunTokensEnv, "1.5"), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"NaN tokens":             {env(MaxRunTokensEnv, "NaN"), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"word tokens":            {env(MaxRunTokensEnv, "lots"), 0, nil, "FUGARO_MAX_RUN_TOKENS"},
		"empty entry":            {env(AllowedModelsEnv, "claude-a-1,,claude-b-2"), 0, nil, "FUGARO_ALLOWED_MODELS"},
		"only a comma":           {env(AllowedModelsEnv, ","), 0, nil, "FUGARO_ALLOWED_MODELS"},
		"alias":                  {env(AllowedModelsEnv, "sonnet"), 0, nil, "FUGARO_ALLOWED_MODELS"},
		"space":                  {env(AllowedModelsEnv, "claude-a-1, claude-b-2"), 0, nil, "FUGARO_ALLOWED_MODELS"},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := SpendFromEnv(tc.env)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || s.MaxRunTokens != tc.tokens || !slices.Equal(s.AllowedModels, tc.models) || (tc.models == nil) != (s.AllowedModels == nil) {
				t.Fatalf("spend = %+v, %v", s, err)
			}
		})
	}
}

// The owner's price overrides hold whatever the mode: a repository that
// escalates an off ceiling to enforce is charged at the owner's prices.
func TestSpendFromEnvPricesHoldWhenOff(t *testing.T) {
	const sonnet = `{"claude-sonnet-5-5":{"input_per_m":30,"output_per_m":150}}`
	for _, e := range []func(string) (string, bool){
		env(ModelPricesEnv, sonnet), env(BudgetModeEnv, "off", ModelPricesEnv, sonnet),
	} {
		s, err := SpendFromEnv(e)
		if err != nil || s.On() || s.Prices == nil {
			t.Fatalf("spend = %+v, %v", s, err)
		}
		if m, ok := s.Prices.Lookup("claude-sonnet-5-5"); !ok || m.Rates.InputPerM != 30 {
			t.Fatalf("sonnet = %+v", m)
		}
	}
	// No keys: exactly M9a's off.
	if s, err := SpendFromEnv(env()); err != nil || s.Prices != nil || s.Cap != 0 || s.Mode != "off" {
		t.Fatalf("%+v, %v", s, err)
	}
}
