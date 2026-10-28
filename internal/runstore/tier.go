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
// limit: it is what a reader compares a run's actual cost and duration with,
// and T8.2 is the one consumer that turns the expected cost into a checkpoint.
//
// The block is additive and absent for every run that carried no tier — every
// record written before this field existed included — so an old reader sees
// the record it always saw.

// MaxTierExpectedUSD and MaxTierExpectedMinutes bound the two figures a record
// may carry. The record is an object the job's service account can write, so a
// block read back is not trusted to be the one a run resolved: the shipped
// figures are at most $8 and 240 minutes (policy.DefaultTierExpectations), and
// a reader clamps what it shows to these, the way clipRunes clamps a name.
const (
	MaxTierExpectedUSD     = 1000
	MaxTierExpectedMinutes = 7 * 24 * 60
)

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

// Bounded is the block as a reader may show it. The tier is trusted only
// against the closed vocabulary — a value outside it is a block no run
// resolved, and nil, which is the answer the field's absence gives and what
// every reader already renders as "no tier" — and the two figures are clamped
// to the bounds above, so a tampered record cannot put a number of its own
// choosing in a report.
func (t *TierRecord) Bounded() *TierRecord {
	if t == nil || !tier.Known(t.Tier) {
		return nil
	}
	return &TierRecord{
		Tier:            t.Tier,
		ExpectedUSD:     min(max(t.ExpectedUSD, 0), MaxTierExpectedUSD),
		ExpectedMinutes: min(max(t.ExpectedMinutes, 0), MaxTierExpectedMinutes),
	}
}
