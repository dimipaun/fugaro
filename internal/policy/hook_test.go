package policy_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestHooksDefaultToTheUntaggedBuild: the policy hook's default pair is the
// one an untagged build runs with — nothing shipped and a rule that tightens
// nothing — so the core holds no run to a governance: block on its own (D522,
// G10). The readers still go through the hook rather than around it, which the
// probe at the end proves: defaults the core never ships and a tighten rule
// that rewrites, both reaching a merge the core runs.
func TestHooksDefaultToTheUntaggedBuild(t *testing.T) {
	// The untagged default, installed: an empty layer resolves no guardrails,
	// and a layer that writes one is neither tightened nor dropped — there is
	// no ceiling for it to be looser than.
	if got := (policy.Governance{}).WithDefaults(); got != (policy.Governance{}) {
		t.Errorf("WithDefaults() = %+v, want the zero governance: this build ships none", got)
	}
	e := policy.Merge(
		policy.Layer{Governance: policy.Governance{}.WithDefaults()},
		policy.Layer{},
		policy.Layer{Governance: policy.Governance{ReviewRatio: policy.DefaultReviewRatio + 1}})
	if e.Governance != (policy.Governance{}) {
		t.Errorf("the block was enforced: %+v", e.Governance)
	}
	if len(e.Ignored) != 0 {
		t.Errorf("the block was recorded as a dropped value: %+v", e.Ignored)
	}
	if len(e.Sources) != 0 {
		t.Errorf("the block sourced a key: %+v", e.Sources)
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

// TestUntaggedPolicyIgnoresGovernance: the core's own pair decodes a
// governance: block and enforces none of it — nothing to tighten from, and a
// tighten rule that writes nothing — so a block reaches neither the effective
// policy nor result.json's policy.ignored. Ignoring is what it does; refusing
// the block is what it must never do (D522, G2).
//
// The pair is the real default and not a probe: this is the state every
// untagged build is in, and a test that installed the off-switch itself could
// not tell it from a core that had quietly kept the shipped values.
func TestUntaggedPolicyIgnoresGovernance(t *testing.T) {
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

// TestLateOrDuplicatePolicyHookIsRefused: the edition of a build is decided
// once, by ee/register, so a second SetGovernance — the hook a later caller
// would install, and the way a tagged binary's enforcement would be halved or
// loosened after it was set — is refused, and so is a pair that would panic the
// first reader of it. The refusal leaves the installed pair exactly as it was
// (design §5.5).
func TestLateOrDuplicatePolicyHookIsRefused(t *testing.T) {
	policy.ResetHooks(t)
	real := policy.GovernanceFuncs{
		Defaults: func() policy.Governance { return policy.Governance{ReviewRatio: 5} },
		Tighten:  func(*policy.Effective, policy.Governance, string) {},
	}
	for _, tc := range []struct {
		name string
		f    policy.GovernanceFuncs
	}{
		{"no Defaults", policy.GovernanceFuncs{Tighten: real.Tighten}},
		{"no Tighten", policy.GovernanceFuncs{Defaults: real.Defaults}},
		{"neither", policy.GovernanceFuncs{}},
	} {
		if err := policy.SetGovernanceHook(tc.f); err == nil {
			t.Errorf("a hook with %s was accepted", tc.name)
		}
	}
	if err := policy.SetGovernanceHook(real); err != nil {
		t.Fatalf("the first SetGovernance was refused: %v", err)
	}
	if err := policy.SetGovernanceHook(policy.GovernanceFuncs{
		Defaults: func() policy.Governance { return policy.Governance{} },
		Tighten:  func(*policy.Effective, policy.Governance, string) {},
	}); err == nil {
		t.Error("a second SetGovernance was accepted, so a tagged build's enforcement can be replaced")
	}
	if got := (policy.Governance{}).WithDefaults(); got.ReviewRatio != 5 {
		t.Errorf("a refused call changed the installed pair: WithDefaults() = %+v", got)
	}
}
