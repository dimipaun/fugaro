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

// TestPolicyRigorModeLeavesThePublishedCapAlone: D16's fraction is the default
// a run gets where no layer sets a cap, never a multiplier over one that is
// set. A published policy's per_run_usd is the owner saying a number, so the
// run gets exactly it — under any mode — and the record still says the value
// came from the node (H1: a figure the node never wrote must not appear in
// result.json under the node's name).
func TestPolicyRigorModeLeavesThePublishedCapAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		rig  string
	}{
		{"poc", "rigor:\n  default: poc\n"},
		{"critical", "rigor:\n  default: critical\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The job carries $0.20 and this mode's share of it; the node says
			// enforce with $0.09. Each call costs $0.04, so the node's cap is
			// what refuses the third one.
			b := policyRun(t, gwConfig(t, tc.rig), "off", "0.20",
				`{"version":3,"budget":{"mode":"enforce","per_run_usd":0.09}}`, capScript(3)...)
			var st []int
			rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 403 {
				t.Fatalf("statuses = %v: the node's $0.09 cap must refuse the third call", st)
			}
			if rec.Policy == nil || rec.Policy.Effective.PerRunUSD != 0.09 {
				t.Fatalf("the record's per-run cap = %+v, want the node's 0.09", rec.Policy)
			}
			if s := sourceOf(t, rec, "per_run_usd"); s != "policy" {
				t.Errorf("per_run_usd source = %q, want policy", s)
			}
		})
	}
}
