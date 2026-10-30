package runner

import (
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The run's expectation tier: where the task's field is read and how the record
// gets the block (docs/task-governance/implementation-plan.md T8.1, D330).
//
// The tier is not resolved from layers the way the rigor mode is: it is the
// launcher's own dispatch decision (spec §3, "the orchestrator attaches an
// expectation envelope"), and the task carries it as it carries the task text.
// The runner's part is smaller than the mode's and has one moment: the
// expectation the run's governance resolved for the tier is read once, at
// bootstrap, and folded into the record. Nothing acts on it yet — the
// expected-cost checkpoint is T8.2 — and nothing here is a cap: the deployed
// per-run cap stays the ceiling whatever a tier expects (G6).

// recordTier writes the tier the run was dispatched at and the expectation the
// run's own governance resolved for it. It runs after resolvePolicy, which is
// where that expectation is settled (T7.1, D331), and it is what makes the
// record say what the run was expected to cost and take, beside what it really
// did.
//
// A run that carries no tier — every launch without --tier whose PR recorded
// none — records none: the field is absent, never a block with an empty tier,
// so an old reader and a reader of a tiered run can tell the two apart. A tier
// outside the vocabulary is refused by task.Parse before the run starts
// (Spec.Validate), so the guard here is the reader's own: only a known tier is
// written, which is also all the schema's enum carries.
func (r *run) recordTier() {
	if !tier.Known(r.spec.Tier) {
		return
	}
	exp := r.tierExpectation(r.spec.Tier)
	r.rec.Tier = &runstore.TierRecord{Tier: r.spec.Tier, ExpectedUSD: exp.ExpectedUSD, ExpectedMinutes: exp.ExpectedMinutes}
}

// tierExpectation is what the run expects tier t to cost and take: the value
// the run's policy resolved for it. The defaults are already in it — the
// ceiling is built with them (policy.Governance.WithDefaults, T7.1), which is
// also what makes a repository's looser figure one the merge drops — so there
// is nothing to fill in here. The figures themselves are ee/governance's
// (governance.Defaults, D521), so a tagged build carries the shipped
// $0.25/10m, $1/30m, $3/90m, $8/240m for a tier of the vocabulary unless a
// layer wrote a tighter one, and an untagged build — whose hook ships none and
// resolves none (D522) — carries whatever the layers wrote, which is zero for
// a repository that wrote nothing.
func (r *run) tierExpectation(t tier.Tier) policy.TierExpectation {
	return r.policy.Governance.Tiers.For(t)
}
