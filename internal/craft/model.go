package craft

import "github.com/dimipaun/fugaro/internal/config"

// The model half's contract (docs/model-crafts/activation-contract.md, plan
// task MC11, reconciliation D1660–D1663 and D1665): the rungs a stage's model
// is chosen by, the craft each stage's model belongs to, where the resolved
// model is written so that every consumer of it agrees, and the verdict a
// craft's model carries when it cannot be served.
//
// **The rungs, most specific first (D1660):** the task's model grade
// (task.grade), a review level's own model (agent.levels.l1/l2/l3.model), the
// craft's model, the role slot the stage pins (agent.models.<role>) and
// agent.model. Each is a narrower statement by the same person about fewer
// stages, so a craft — a standing choice about a set of stages — may not
// overrule the grade a task carries or the model a review level names.
//
// **The craft each stage's model belongs to (F13, D1663):** the coder plans,
// implements and fixes; the first-line review is the review craft; the senior
// review and the triage that weighs its findings are the senior-review craft;
// level 3's adversarial review is the security-review craft. That map is the
// run's own and its one copy for the run is the runner's stageCraft
// (internal/runner/routing.go, beside stageLevel and stageRole), which the
// walk, the stage's own checks and the bootstrap write read; the CLI
// migration's second copy converges onto it (MC12, MC21). This package states
// the map too, because a stage's craft is half of what ModelFor answers and a
// package cannot read the runner's unexported function, and the two are held in
// step by the runner's own tests, which read both answers for every stage a run
// has.
//
// **Where the resolved model is written (D1661):** one bootstrap pass, first
// among the three that rewrite a model and so before applyGrade and
// applyRouting in resolvePolicy (internal/runner/policy.go), writes each
// craft's resolved model into the slot the run already reads — the coder craft
// into Agent.Models.Coder, the review craft into Agent.Levels.L1.Model where
// the level wrote none, the senior-review craft into Agent.Models.Reviewer and
// into Agent.Levels.L2.Model where the level wrote none, and the security-review
// craft into Agent.Levels.L3.Model where the level wrote none. It is a write
// and not a fifth rung read where the model is answered, because the consumers
// of a stage's model are more than stageModel: Agent.PinnedModels is the
// gateway's allow-list, config.CheckAllowed and config.CheckPins judge the
// launch and `fugaro validate`, cost.go prices the stage's row and the run
// record's StageTiming.Model reports it. One write is what makes them all name
// one model, and a rung read in stageModel alone would leave the gateway
// refusing a model the run was told to run.
//
// **The verdict a craft's model carries when it cannot be served (D1665):** it
// passes the checks a run's model passes — budget.allowed_models bounds it, a
// route claims it (config.RoutingClaudeFor), a price row covers it and the
// craft's own access admits the repository — and a model that fails one is
// refused at config time and in `fugaro validate`, at the path of the key in
// the document that wrote it (Layer.CraftKey), never by the gateway after the
// run has started.
//
// This file states that contract and answers the craft rung alone. The two
// rungs above it (a task's grade, a level's own model) and the two below it
// (agent.models.<role>, agent.model) are the run's, and half a ladder
// implemented in a package would be the drift the contract exists to prevent.

// ModelRungs are the rungs a stage's model is chosen by, most specific first
// (D1660). They are the contract's list and not this package's plan: only the
// craft rung is answered here (ModelFor), and a reader that has to name the key
// an owner should change — a refusal, a `craft ls` row — reads the rung from
// here rather than spelling it a second time.
func ModelRungs() []string {
	return []string{
		"task.grade",
		"agent.levels.<level>.model",
		"agent.crafts.<craft>.model",
		"agent.models.<role>",
		"agent.model",
	}
}

// ModelFor answers the craft rung of a stage: the model the stage's craft
// resolved to, and the craft the stage belongs to. crafts is the resolver's
// answer for the run (Resolve over the caller's own layers), because the layers
// are the caller's — a run's craftLayers, the launcher's own — and a package
// that assembled its own would be a second copy of them.
//
// The model is "" when no layer set that craft's model and the craft it follows
// set none either: the rungs below the craft are the run's, and answering
// agent.models.<role> here would hide that the owner wrote no model for the
// craft. The craft is "" for a stage this vocabulary has no craft for — a stage
// that spends no model call, and a name that is no stage at all — rather than
// guessing the coder.
func ModelFor(crafts map[string]Effective, stage string) (string, string) {
	name := stageCraft(stage)
	if name == "" {
		return "", ""
	}
	return crafts[name].Model, name
}

// stageCraft is the craft a stage's model belongs to (F13, D1663): the
// contract's statement of the map for the model half. The run's own copy is the
// runner's, which the walk, the stage's checks and the bootstrap write read
// (internal/runner/routing.go), and the two are held in step by the runner's
// tests. "" is a stage outside the run's vocabulary, answered rather than
// guessed at.
//
// merge_judge is the runner's own stage name (stageMergeJudge), not one of
// config.RunStages, and it is here because the map decides a stage's access as
// well as its model: the judge spends model calls of its own (E6), so a map
// that left it out would refuse a merge-judge run (F49) rather than answer the
// craft whose access the judge's calls go out on.
func stageCraft(stage string) string {
	switch stage {
	case "plan", "implement", "fix":
		return config.CraftCoder
	case "review_first":
		return config.CraftReview
	case "review", "triage":
		return config.CraftSeniorReview
	case "adversarial":
		return config.CraftSecurityReview
	case "merge_judge":
		return config.CraftMergeJudge
	}
	return ""
}
