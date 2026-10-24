package runner_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runner"
)

// TestPolicyRigorModeScalesTheCeiling: D16's cost rule, and T6.1's — the
// deployed cap is the ceiling and a mode's per-run default is a fraction of
// it. A repository that configures nothing runs at Pro (D19), so its run gets
// Pro's fraction of the owner's cap; Critical contributes no default and gets
// the ceiling whole, which is exactly what a run of it got before this
// landed. The run is ended by the token cap and not the money cap so that
// every mode reaches the record the assertion reads.
func TestPolicyRigorModeScalesTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  string
		want float64
	}{
		{"no rigor block is Pro", "", 0.17},
		{"critical gets the deployed cap whole", "rigor:\n  default: critical\n", 0.20},
		{"poc lowers it", "rigor:\n  default: poc\n", 0.11},
		{"hotfix is the tightest", "rigor:\n  default: hotfix\n", 0.08},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tokenCfg(t, "1000000")+tc.cfg, nil)
			h.deps.Spend = ceilingOf(t, runner.MaxRunTokensEnv, "50", runner.MaxRunUSDEnv, "0.20")
			rec, err := h.run(t, big(implement("feature"), 500))
			tokenHalt(t, rec, err, "run used 500 tokens of 50")
			if rec.Policy == nil || rec.Policy.Effective.PerRunUSD != tc.want {
				t.Fatalf("the record's per-run cap = %+v, want %v: the run's mode did not scale the owner's cap", rec.Policy, tc.want)
			}
		})
	}
}

// TestPolicyRigorModeCannotRaiseALowerCap: the mode's default is one more
// ceiling under the owner's, so a lower value a layer sets still wins and no
// mode lifts it — the only-tightens rule the layers already have (F15).
func TestPolicyRigorModeCannotRaiseALowerCap(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "1000000")+"rigor:\n  default: critical\nbudget:\n  per_run_usd: 0.05\n", nil)
	h.deps.Spend = ceilingOf(t, runner.MaxRunTokensEnv, "50", runner.MaxRunUSDEnv, "0.20")
	rec, err := h.run(t, big(implement("feature"), 500))
	tokenHalt(t, rec, err, "run used 500 tokens of 50")
	if rec.Policy == nil || rec.Policy.Effective.PerRunUSD != 0.05 {
		t.Fatalf("the record's per-run cap = %+v, want the file's 0.05", rec.Policy)
	}
}
