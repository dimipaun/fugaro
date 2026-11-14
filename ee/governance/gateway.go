// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance

import (
	"fmt"
	"math"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// This file is the gateway half of governance (plan T7.2, D331, D332): the
// limits the run's own configuration resolved, behind the core's stage-limit
// hook (internal/gateway's StageLimit, wired by ee/register). The core knows
// where to ask and what a stop means; what a stage may spend is here, with the
// policy it is read from (D527, D530).
//
// Two halves of the spec's §2 and §5 guardrails are not here. A stage's own
// wall-clock ceiling is the runner's (T7.6, the stage hook), and an oauth run
// — which makes its calls outside any gateway — gets no live embargo at all:
// the runner only learns a stage's figure when the agent reports it, so its
// phase budgets, ratio and fan-out are post-hoc flags in the reports
// (reconciliation F5). What is here bounds a gateway run's calls as they are
// made.

// CheckpointStage is the stage the runner opens for the one call that fills a
// halted run's checkpoint report (T6.1, T7.2). It is spent like any call —
// reserved against the run's cap, refused when the cap has no room — and it is
// the one stage whose name is not one of governance's three: it has no budget
// of its own, so all that bounds it is the run's cap and the one call it is
// allowed.
const CheckpointStage = "checkpoint"

// checkpointCalls is how many calls the checkpoint stage admits. One: the
// runner stops the stage's agent process first and makes this call itself, from
// a fresh session given its own summary, so the allowance is the checkpoint's
// and never a budget any caller of the stopped stage can spend (T7.2).
const checkpointCalls = 1

// StageLimit is the gateway's per-stage governor as the run's configuration
// made it: what a stage may spend in dollars and tokens, how many calls review
// may spend on the implementation it read, and how far one stage may fan out
// (T7.2). It is what ee/register installs behind the core's hook.
//
// t is the tier the run was dispatched at, which moves one figure: the fan-out
// maximum, 3 on trivial and small and the untiered default elsewhere (T8.1,
// D332). A run that carries no tier is bounded by the governance block alone.
//
// cap is the run's own per-run cap, which every stage budget is a fraction of.
// A cap of 0 — a gateway accounting only, with nothing to stop at — bounds no
// stage either: a fraction of nothing is not a budget, and the gateway's own
// rules are all that apply.
func StageLimit(g policy.Governance, t tier.Tier, cap pricing.Micros) gateway.StageLimit {
	return &stageLimit{g: g, cap: cap, maxAgents: fanoutMax(g, t)}
}

// stageLimit is one run's limits. It holds no state of its own: everything a
// decision reads is in the view the gateway hands it, which is what keeps the
// core's stage state the single account of a stage.
type stageLimit struct {
	g         policy.Governance
	cap       pricing.Micros
	maxAgents int64
}

// Begin does nothing: the limits are the run's resolved policy, and every
// figure a decision needs arrives with the call it decides.
func (l *stageLimit) Begin(gateway.StageLimitView) {}

// Stop is the decision before one call of a stage. The order is the order of
// the sentences a run is told in: the checkpoint stage first (its allowance is
// the only rule that applies to it), then the stage's own budget, then the
// review ratio, then the fan-out. A stage governance bounds is one of the
// three spend stages — an artifact run's plan and render steps have no
// governance budget (T7.1) — and any other stage is bounded by the run's cap
// and the gateway's pins alone.
func (l *stageLimit) Stop(v gateway.StageLimitView) gateway.StageStop {
	if v.Stage.Name == CheckpointStage {
		return checkpointStop(v)
	}
	if !governedStage(v.Stage.Name) {
		return gateway.StageStop{}
	}
	if dec := l.budgetStop(v); dec.Reason != "" {
		return dec
	}
	if dec := ratioStop(l.g, v); dec.Reason != "" {
		return dec
	}
	return fanoutStop(l.maxAgents, v)
}

// governedStage reports whether governance bounds the named stage's spend: the
// implementation, either review stage of a pass (a level 3 adversarial review
// counts as review here, as stage_timeout already counts it, T7.1) and the fix
// rounds.
func governedStage(stage string) bool {
	return stage == "implement" || stage == "fix" || config.IsReviewStage(stage)
}

// budgetStop is the stage's own budget: its dollars, and its token ceiling
// where a layer set one (T7.1).
//
// The dollars are decided before the call is sent and include it — the settled
// figure plus the call's worst case — because a stage that has already spent
// its budget is a stage that stops, and a call admitted on the hope that it is
// cheaper than its own reservation is the call that takes the stage over. The
// tokens are decided on what the stage's calls reported: a call's token count
// is only known once it has been served, so the ceiling bounds what was spent
// and not what is about to be.
func (l *stageLimit) budgetStop(v gateway.StageLimitView) gateway.StageStop {
	money, tokens := stageBudget(l.g, l.cap, v.Stage.Name)
	spent := stageSpend(v)
	if money > 0 && satAdd(spent.used, v.Needed) > money {
		return gateway.StageStop{
			Reason: string(runstore.HaltStageBudget),
			Detail: fmt.Sprintf("%s stage budget %s reached (%s spent, %s needed)", v.Stage.Name, usd(money), usd(spent.used), usd(v.Needed)),
		}
	}
	if tokens > 0 && spent.tokens > tokens {
		return gateway.StageStop{
			Reason: string(runstore.HaltStageBudget),
			Detail: fmt.Sprintf("%s stage token ceiling %d passed (%d tokens spent)", v.Stage.Name, tokens, spent.tokens),
		}
	}
	return gateway.StageStop{}
}

// stageSpend is what the stage's budget has already bought: the running
// stage's own figures, and — for a review stage — those of the review stages
// that ended before it in the same pass.
//
// The two review stages are one budget (T7.1: "first-line and senior review
// together"), so the second-line review of a pass starts from what the
// first-line one spent rather than from a fresh 0.5 of the cap. The gateway
// keeps one report per stage name, so what a review stage reads is the latest
// ending of each other review name: a pass's own, in the order the runner runs
// them, and never a report of a stage name that ran twice.
func stageSpend(v gateway.StageLimitView) spend {
	s := spend{used: v.Report.Used, tokens: v.Report.Tokens}
	if !config.IsReviewStage(v.Stage.Name) {
		return s
	}
	for name, rep := range v.Ended {
		if name == v.Stage.Name || !config.IsReviewStage(name) {
			continue
		}
		s.used = satAdd(s.used, rep.Used)
		s.tokens = satAddTokens(s.tokens, rep.Tokens)
	}
	return s
}

// spend is a stage's budget figure: what it settled in money (the tier-A
// notional part is not money and is not counted, C1.16) and in tokens.
type spend struct {
	used   pricing.Micros
	tokens int64
}

// stageBudget is the money and the token ceiling of the named stage's budget:
// fractions of the run's cap for the implementation and review, and — for the
// fix rounds — a fraction of the implement stage's own budget, which is G§2's
// "a fraction of implementation budget" and the arithmetic the two keys are
// merged under (T7.1). A stage with no budget of its own has none of either.
//
// The money is a fraction of the cap and never the cap itself: the defaults
// (0.7, 0.5, 0.5 of the implementation's) leave a stage a share of the run and
// are what keeps a single stage from spending the whole ceiling, which is the
// run's own.
func stageBudget(g policy.Governance, cap pricing.Micros, stage string) (money pricing.Micros, tokens int64) {
	switch {
	case stage == "implement":
		return fraction(cap, g.StageBudget.Implement), g.StageTokens.Implement
	case config.IsReviewStage(stage):
		return fraction(cap, g.StageBudget.Review), g.StageTokens.Review
	case stage == "fix":
		return fraction(fraction(cap, g.StageBudget.Implement), g.StageBudget.Fix), g.StageTokens.Fix
	}
	return 0, 0
}

// fraction is f of m, rounded to the nearest micro-dollar. A fraction at or
// below zero is no budget at all, and a fraction at or above 1 is the whole
// figure: both are what a merge of keys can make of the numbers, and neither
// may turn into a figure larger than the cap it is a share of.
func fraction(m pricing.Micros, f float64) pricing.Micros {
	if m <= 0 || f <= 0 {
		return 0
	}
	if f >= 1 {
		return m
	}
	return pricing.Micros(math.Round(float64(m) * f))
}

// ratioStop is the ratio guardrail: review calls above max(floor, N × the
// implement stage's calls) stop the review stage (spec §2, T7.2).
//
// The floor is what makes a short implementation reviewable: a one-line change
// costs a dozen calls to implement, and 5 × 12 would allow review fewer calls
// than reading the change takes (reconciliation F6). The calls are the
// implementation's own as the gateway counted them — calls sent upstream — and
// the bound is applied to the call about to be made, so review settles at the
// bound rather than a call past it.
//
// A run whose review stage is not preceded by an implement stage has no ratio
// at all: the rule is review's calls against the implementation's, and a
// review-only run (`mode: review`, which reviews a branch somebody else wrote)
// has no implementation of its own to be measured against (F6).
func ratioStop(g policy.Governance, v gateway.StageLimitView) gateway.StageStop {
	if !config.IsReviewStage(v.Stage.Name) {
		return gateway.StageStop{}
	}
	impl, ok := v.Ended["implement"]
	if !ok {
		return gateway.StageStop{}
	}
	bound := max(g.ReviewFloorCalls, satMul(g.ReviewRatio, int64(impl.Calls)))
	if bound <= 0 {
		return gateway.StageStop{}
	}
	calls := int64(v.Report.Calls)
	for name, rep := range v.Ended {
		if name == v.Stage.Name || !config.IsReviewStage(name) {
			continue
		}
		calls += int64(rep.Calls)
	}
	if calls >= bound {
		return gateway.StageStop{
			Reason: string(runstore.HaltReviewRatio),
			Detail: fmt.Sprintf("review calls (%d) reached max(%d, %d x %d implement calls)", calls, g.ReviewFloorCalls, g.ReviewRatio, impl.Calls),
		}
	}
	return gateway.StageStop{}
}

// fanoutStop is the fan-out guardrail: a stage that used more distinct
// sub-agents than it may stops (spec §5, T7.2).
//
// The count is the gateway's own — the distinct non-empty agent IDs the
// stage's calls carried — and it is deliberately not a session count: Check
// 139 found that a sub-agent's calls carry their stage's `session_id`, so a
// session cannot tell a sub-agent from the main session, while the agent ID
// does (an empty one is the main session and is never a sub-agent, and each
// sub-agent carries a distinct one). The count therefore grows by one per
// sub-agent the gateway admitted a call from, whatever session it claimed.
//
// It is read off the stage's settled calls, so the sub-agent that crosses the
// maximum is admitted its first call and the stage stops on the next one: the
// alternative would be for the gateway to refuse a call on a claim about who
// is making it that nothing verifies, and a sub-agent's first call is also the
// only evidence there is that the sub-agent exists.
func fanoutStop(maxAgents int64, v gateway.StageLimitView) gateway.StageStop {
	if maxAgents <= 0 || int64(v.Report.Agents) <= maxAgents {
		return gateway.StageStop{}
	}
	return gateway.StageStop{
		Reason: string(runstore.HaltFanout),
		Detail: fmt.Sprintf("%s stage used %d distinct sub-agents, its maximum is %d", v.Stage.Name, v.Report.Agents, maxAgents),
	}
}

// checkpointStop is the checkpoint stage's one allowance: it admits one call
// and refuses the next. That refusal is not a second halt — a checkpoint stage
// exists only after the run has already halted, and a halt is recorded once —
// so the reason it carries is the closest of the record's vocabulary to what
// happened, and what a reader of the run log gets is the sentence.
//
// It is a refusal and not a run-level stop: the gateway's own cap is what
// decides whether the checkpoint call fits at all, and a cap that has no room
// refuses it as it refuses any other call (T7.2).
func checkpointStop(v gateway.StageLimitView) gateway.StageStop {
	if v.Report.Calls < checkpointCalls {
		return gateway.StageStop{}
	}
	return gateway.StageStop{
		Reason: string(runstore.HaltStageBudget),
		Detail: fmt.Sprintf("the %s stage admits one call and has already made %d", CheckpointStage, v.Report.Calls),
	}
}

// fanoutMax is the most distinct sub-agents one stage of this run may start:
// the tier's own where the run carries one — 3 on trivial and small, the
// untiered default elsewhere (T8.1, D332) — and the governance block's
// max_subagents for a run whose task named no tier.
func fanoutMax(g policy.Governance, t tier.Tier) int64 {
	if tier.Known(t) {
		if max := g.Tiers.For(t).MaxSubagents; max > 0 {
			return max
		}
	}
	return g.MaxSubagents
}

// usd is money as a run's sentences spell it: "$12.34", with the cents a small
// budget needs so that a stage budget below a cent is not printed as $0.00.
func usd(m pricing.Micros) string {
	if m > 0 && m.USD() < 0.01 {
		return fmt.Sprintf("$%.6f", m.USD())
	}
	return fmt.Sprintf("$%.2f", m.USD())
}

// satAdd adds two amounts, saturating at the largest Micros: a limit's
// arithmetic must not wrap into a figure that admits everything.
func satAdd(a, b pricing.Micros) pricing.Micros {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// satAddTokens is satAdd for token counts.
func satAddTokens(a, b int64) int64 {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// satMul multiplies n by k, saturating: the ratio's N × implement calls is
// operator-supplied arithmetic over a count, and a product that wrapped would
// bound review by a figure smaller than the floor the same rule carries.
func satMul(n, k int64) int64 {
	n, k = max(n, 0), max(k, 0)
	if n == 0 || k == 0 {
		return 0
	}
	if n > math.MaxInt64/k {
		return math.MaxInt64
	}
	return n * k
}
