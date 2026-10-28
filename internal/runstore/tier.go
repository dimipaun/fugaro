package runstore

import "github.com/dimipaun/fugaro/internal/tier"

// The record's tier block (docs/task-governance/implementation-plan.md T8.1,
// D330, G6): the expectation tier a run was dispatched at, and what Fugaro
// expects a run of that tier to cost and take. The launcher asks for the tier
// (fugaro run --tier, or the tier a --pr follow-up keeps from its PR) and the
// runner writes the block at bootstrap, out of the expectation its governance
// resolved for that tier.
//
// None of it is a cap. The deployed per-run cap stays the ceiling whatever a
// tier expects (G6), which is why the block carries the two figures and no
// limit. Nothing reads it yet — no runview.Row key projects it and no report
// renders it; the record is what survives the run, and T8.2 is the one consumer
// that will turn the expected cost into a checkpoint.
//
// The block is additive and absent for every run that carried no tier — every
// record written before this field existed included — so an old reader sees
// the record it always saw.

// TierRecord is the tier a run was dispatched at and the expectation that tier
// resolved: what the run was expected to cost and take.
type TierRecord struct {
	// Tier is the tier, one of tier.Tiers. An unknown value is not a tier any
	// runner resolved, and Bounded refuses a block carrying one.
	Tier tier.Tier `json:"tier"`
	// ExpectedUSD is what a run of the tier is expected to cost, in dollars.
	// An expectation: it moves no cap (G6).
	ExpectedUSD float64 `json:"expected_usd"`
	// ExpectedMinutes is what a run of the tier is expected to take.
	ExpectedMinutes int64 `json:"expected_minutes"`
}

// Bounded is the block as a reader may show it. The record is an object the
// job's service account can write, so a block read back is not trusted to be
// the one a run resolved: the tier is trusted only against the closed
// vocabulary, where a value outside it is a block no run resolved and nil —
// the answer the field's absence gives, and what a reader already renders as
// "no tier" — is the answer.
//
// The two figures are floored at zero and not capped, because a negative
// expectation is one no configuration could have written (config's
// validateGovernance refuses it) while a large one is a value the project layer
// is free to write: it may loosen a tier past anything Fugaro ships, so a
// reader that clamped it would report a number the run never resolved.
func (t *TierRecord) Bounded() *TierRecord {
	if t == nil || !tier.Known(t.Tier) {
		return nil
	}
	return &TierRecord{
		Tier:            t.Tier,
		ExpectedUSD:     max(t.ExpectedUSD, 0),
		ExpectedMinutes: max(t.ExpectedMinutes, 0),
	}
}
