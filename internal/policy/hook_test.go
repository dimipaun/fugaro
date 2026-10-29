package policy_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestHooksDefaultToTodaysBehaviour: the policy hook's default pair is what
// the core did before the seam — the values DefaultGovernance ships and the
// tighten-only rule of (*Effective).tightenGovernance — and the readers that
// need them go through the hook rather than around it (G10). The probe at the
// end is the proof of the second half: defaults the core never ships and a
// tighten rule that overwrites, both reaching a merge the core runs.
func TestHooksDefaultToTodaysBehaviour(t *testing.T) {
	// Today's behaviour, with the default pair installed: a layer's ceiling is
	// the shipped guardrails, and a later layer that loosens one is dropped and
	// recorded.
	if got := (policy.Governance{}).WithDefaults(); got != policy.DefaultGovernance() {
		t.Errorf("WithDefaults() = %+v, want DefaultGovernance() %+v", got, policy.DefaultGovernance())
	}
	ceiling := policy.Layer{Governance: policy.Governance{}.WithDefaults()}
	e := policy.Merge(ceiling, policy.Layer{},
		policy.Layer{Governance: policy.Governance{ReviewRatio: policy.DefaultReviewRatio + 1}})
	if e.Governance.ReviewRatio != policy.DefaultReviewRatio {
		t.Errorf("review_ratio = %d, want the ceiling's %d", e.Governance.ReviewRatio, policy.DefaultReviewRatio)
	}
	if len(e.Ignored) != 1 || e.Ignored[0].Key != policy.KeyReviewRatio {
		t.Errorf("the looser layer was not recorded: %+v", e.Ignored)
	}

	// The probe: WithDefaults and DefaultGovernanceKeys read the hook's
	// Defaults, and Merge calls its Tighten.
	sentinel := policy.Governance{ReviewRatio: 7, MaxSubagents: 2}
	tightened := 0
	policy.SetHooks(t, policy.GovernanceFuncs{
		Defaults: func() policy.Governance { return sentinel },
		Tighten: func(e *policy.Effective, g policy.Governance, src string) {
			tightened++
			e.Governance, e.Sources[policy.KeyReviewRatio] = g, src
		},
	})
	if got := (policy.Governance{}).WithDefaults(); got.ReviewRatio != sentinel.ReviewRatio || got.MaxSubagents != sentinel.MaxSubagents {
		t.Errorf("WithDefaults() = %+v, want the probe's defaults %+v", got, sentinel)
	}
	if keys := (policy.Governance{ReviewRatio: sentinel.ReviewRatio}).DefaultGovernanceKeys(); !keys[policy.KeyReviewRatio] {
		t.Errorf("DefaultGovernanceKeys() = %+v, want review_ratio named: the reader did not ask the probe's Defaults", keys)
	}
	e = policy.Merge(policy.Layer{Governance: policy.Governance{ReviewRatio: 3}})
	if tightened != 1 || e.Governance.ReviewRatio != 3 {
		t.Errorf("Merge = %+v after %d hook calls, want the probe's Tighten to have been the one that ran",
			e.Governance, tightened)
	}
	if e.Sources[policy.KeyReviewRatio] == "" {
		t.Errorf("the hook's src did not reach the merge's sources: %+v", e.Sources)
	}
}

// TestUntaggedPolicyIgnoresGovernance: the pair an untagged build has after the
// move (E2.3) decodes a governance: block and enforces none of it — nothing to
// tighten from, and a tighten rule that writes nothing — so a block reaches
// neither the effective policy nor result.json's policy.ignored. Ignoring is
// what it does; refusing the block is what it must never do (D522, G2).
func TestUntaggedPolicyIgnoresGovernance(t *testing.T) {
	policy.SetCore(t)
	e := policy.Merge(
		policy.Layer{Governance: policy.Governance{}.WithDefaults()},
		policy.Layer{},
		policy.Layer{Governance: policy.Governance{MaxSubagents: 2}})
	if e.Governance != (policy.Governance{}) {
		t.Errorf("the ignored block enforced something: %+v", e.Governance)
	}
	if len(e.Ignored) != 0 {
		t.Errorf("the ignored block was recorded as a dropped value: %+v", e.Ignored)
	}
	if got := (policy.Governance{MaxSubagents: 2}).WithDefaults(); got != (policy.Governance{MaxSubagents: 2}) {
		t.Errorf("WithDefaults() = %+v, want the block as written: with nothing shipped there is nothing to fill in", got)
	}
	if len(e.Sources) != 0 {
		t.Errorf("the ignored block sourced a key: %+v", e.Sources)
	}
}
