// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance

import (
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/tier"
)

// Defaults is the governance every run starts under: the values Fugaro ships
// for the guardrails, with no stage token ceiling and no stage timeout of its
// own (a stage with neither is bounded by the run's own). It is what
// policy.Governance.WithDefaults fills an empty layer with and what
// policy.Governance.DefaultGovernanceKeys compares a value against, both
// through the core's policy hook (design ee-boundary-moves.md §5.2).
func Defaults() policy.Governance {
	return policy.Governance{
		ReviewRatio:      policy.DefaultReviewRatio,
		ReviewFloorCalls: policy.DefaultReviewFloorCalls,
		MaxSubagents:     policy.DefaultMaxSubagents,
		StageBudget: policy.StageBudget{
			Implement: policy.DefaultStageBudgetImplement,
			Review:    policy.DefaultStageBudgetReview,
			Fix:       policy.DefaultStageBudgetFix,
		},
		Tiers: TierExpectations(),
	}
}

// TierExpectations are the expectations Fugaro ships for the four tiers
// (G§3, spec §3's starting tiers): trivial $0.25/10m, small $1/30m, medium
// $3/90m and large $8/240m. The fan-out is 3 for trivial and small — the
// spec's §5 tripwire, where a small change has no business fanning out — and
// the untiered default of 5 for medium and large (D332, plan T7.1).
//
// None of these is a cap. The deployed per-run cap stays the ceiling whatever
// a tier expects (G6), so an expected cost above the cap is an expectation the
// run still stops at the cap for, and no tier key can raise or replace one.
func TierExpectations() policy.TierExpectations {
	return policy.TierExpectations{
		Trivial: policy.TierExpectation{ExpectedUSD: 0.25, ExpectedMinutes: 10, MaxSubagents: 3},
		Small:   policy.TierExpectation{ExpectedUSD: 1, ExpectedMinutes: 30, MaxSubagents: 3},
		Medium:  policy.TierExpectation{ExpectedUSD: 3, ExpectedMinutes: 90, MaxSubagents: policy.DefaultMaxSubagents},
		Large:   policy.TierExpectation{ExpectedUSD: 8, ExpectedMinutes: 240, MaxSubagents: policy.DefaultMaxSubagents},
	}
}

// Tighten applies one layer's governance limits to the running effective ones.
// It is the whole of the tighten-only rule for them: the smaller positive
// count, fraction or duration wins, a looser one is dropped and recorded, and
// a zero (or a key the layer left out) is unset and passes the running value
// through. The keys are named one by one rather than walked by reflection:
// this is the list a key added to policy.Governance has to be added to, and
// nothing at compile time notices that it was forgotten.
//
// The rule itself stays core — policy's TightenInt, TightenCap and
// TightenDuration are what it calls — so this side of the split cannot drift
// from the merge that runs it (E2.3a).
func Tighten(e *policy.Effective, g policy.Governance, src string) {
	e.TightenInt(&e.Governance.ReviewRatio, g.ReviewRatio, policy.KeyReviewRatio, src)
	e.TightenInt(&e.Governance.ReviewFloorCalls, g.ReviewFloorCalls, policy.KeyReviewFloorCalls, src)
	e.TightenInt(&e.Governance.MaxSubagents, g.MaxSubagents, policy.KeyMaxSubagents, src)

	e.TightenCap(&e.Governance.StageBudget.Implement, g.StageBudget.Implement, policy.KeyStageBudgetImplement, src)
	e.TightenCap(&e.Governance.StageBudget.Review, g.StageBudget.Review, policy.KeyStageBudgetReview, src)
	e.TightenCap(&e.Governance.StageBudget.Fix, g.StageBudget.Fix, policy.KeyStageBudgetFix, src)

	e.TightenInt(&e.Governance.StageTokens.Implement, g.StageTokens.Implement, policy.KeyStageTokensImplement, src)
	e.TightenInt(&e.Governance.StageTokens.Review, g.StageTokens.Review, policy.KeyStageTokensReview, src)
	e.TightenInt(&e.Governance.StageTokens.Fix, g.StageTokens.Fix, policy.KeyStageTokensFix, src)

	e.TightenDuration(&e.Governance.StageTimeout.Implement, g.StageTimeout.Implement, policy.KeyStageTimeoutImplement, src)
	e.TightenDuration(&e.Governance.StageTimeout.Review, g.StageTimeout.Review, policy.KeyStageTimeoutReview, src)
	e.TightenDuration(&e.Governance.StageTimeout.Fix, g.StageTimeout.Fix, policy.KeyStageTimeoutFix, src)

	tightenTier(e, &e.Governance.Tiers.Trivial, g.Tiers.Trivial, tier.Trivial, src)
	tightenTier(e, &e.Governance.Tiers.Small, g.Tiers.Small, tier.Small, src)
	tightenTier(e, &e.Governance.Tiers.Medium, g.Tiers.Medium, tier.Medium, src)
	tightenTier(e, &e.Governance.Tiers.Large, g.Tiers.Large, tier.Large, src)
}

// tightenTier is Tighten's rule one level down, over the three keys of a
// single tier: the smaller value wins per key, a looser one is dropped and
// recorded, and a key the layer left at zero passes the running value through.
// It never touches the per-run cap (G6): a tier's expected cost is an
// expectation, so no key here can raise or replace the ceiling a run stops at.
func tightenTier(e *policy.Effective, cur *policy.TierExpectation, v policy.TierExpectation, t tier.Tier, src string) {
	e.TightenCap(&cur.ExpectedUSD, v.ExpectedUSD, policy.TierKey(t, policy.KeyExpectedUSD), src)
	e.TightenInt(&cur.ExpectedMinutes, v.ExpectedMinutes, policy.TierKey(t, policy.KeyExpectedMinutes), src)
	e.TightenInt(&cur.MaxSubagents, v.MaxSubagents, policy.TierKey(t, policy.KeyMaxSubagents), src)
}
