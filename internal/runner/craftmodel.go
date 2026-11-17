package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// applyCrafts writes the crafts' models where the run's stages read them: a
// craft's model is rung 3 of D1660's ladder, below a task's grade and a review
// level's own model, and above agent.models.<role> and agent.model.
//
// It is the one write, and it is what makes the four readers of a stage's
// model agree: the pins the agent is given (config.Agent.PinnedModels), the
// set the gateway admits a call against (config.CheckAllowed), the row cost.go
// charges the stage from and the model the record's StageTiming names all read
// it back through r.stageModel, so a single answer written here is the model
// every one of them names (D1661).
//
// Where it runs is part of the contract: it is the first of the rewrites
// resolvePolicy performs, before applyGrade and before applyRouting. A model
// written after the switch would be one the switch never judged — an owner who
// routes the model a craft names would find the run sending it direct — and one
// written after the grade or the levels would land on top of a model those had
// already resolved for the same slot.
//
// A craft's model moves two kinds of slot, and the role slots come first: level
// 1's own answer is read off the coder's and the reviewer's models
// (config.Agent.FirstLineOn, agent.first_line_review: auto), so the crafts'
// role models have to be in place before the levels are asked whether they run.
//
// A challenger arm is the one run this write refuses rather than performs, and
// the refusal is the coder craft's alone: an arm is compared by the coder model
// it names, so a craft that names one is a second answer to the question the
// arm exists to ask (applyChallenger refuses a model grade for the same reason,
// D555/D556/D559). Letting the craft win would not settle it either: the arm's
// champion coder is read off the configuration before the crafts resolve
// (championCoder), and the champion's own run — the one this arm is compared
// with — would run the craft's model, so the pair would be derived from, and
// reported on, a model neither run ran. The write's other slots are not this
// refusal's business: the reviewer's model and the levels are the same on both
// runs of a pair, because the crafts are the repository's and not the task's.
func (r *run) applyCrafts(ctx context.Context) error {
	crafts := r.stageCrafts(ctx)
	// The coder craft's model is the coder role's and the senior review's is
	// the reviewer's (D1661). The first-line review runs the coder's model on
	// the review craft's access and so takes its own level's slot below rather
	// than the role's, and the security review takes neither: it is the senior
	// review's model where it names none, and a security review that names one
	// must not move the senior review's calls onto it.
	if m, _ := craft.ModelFor(crafts, "plan"); m != "" {
		if r.shadow {
			return r.refuseArm(fmt.Sprintf("this task is a challenger arm and the coder craft names the model %q (%s): an arm is compared by the coder model it names, and a craft's model would be the model this run's calls go out on while the record and the champion's own coder — read off the configuration before the crafts resolve — name another one. Launch the arm on a repository whose crafts name no coder model, or the run without an ab: block", m, crafts[config.CraftCoder].ModelFrom))
		}
	}
	WriteCraftRoles(r.cfg, crafts, r.rcp)
	WriteCraftLevels(r.cfg, crafts, r.craftLevelsRunning())
	return nil
}

// WriteCraftRoles writes the crafts' role models into the agent's own slots —
// the coder craft's into agent.models.coder and the senior review's into
// agent.models.reviewer (MC12, D1661) — and answers the crafts it wrote, by
// craft name.
//
// The role slots come first and they are separate from the two levels below
// because level 1's own answer is read off them (config.Agent.FirstLineOn,
// agent.first_line_review: auto): the crafts' role models have to be in place
// before the levels are asked whether they run, and a caller resolving the
// levels with the crafts' models (config.LevelInputs.CraftModels) still has to
// have them written here, or level 1's answer would be read off the models the
// layers wrote instead of the crafts'.
//
// A recipe may map the reviewer onto the coder (roles: {reviewer: coder}),
// which bootstrap applied to the coder's model as it stood then: the craft
// moves the coder, so the mapping is applied again — applyGrade's own rule for
// the grade, and for the same reason — and before the senior review's own model
// below, which is the rung above the mapping. The other direction (roles: {coder:
// reviewer}) names the coder's model itself and gives way to the craft's, as it
// gives way to the grade's.
func WriteCraftRoles(cfg *config.Config, crafts map[string]craft.Effective, rcp *recipe.Recipe) map[string]bool {
	written := map[string]bool{}
	if m, name := craft.ModelFor(crafts, "plan"); m != "" {
		cfg.Agent.Models.Coder = m
		if rcp != nil && rcp.ReviewerIsCoder {
			ApplyRoles(&cfg.Agent, rcp)
		}
		written[name] = true
	}
	if m, name := craft.ModelFor(crafts, "review"); m != "" {
		cfg.Agent.Models.Reviewer = m
		written[name] = true
	}
	return written
}

// WriteCraftLevels writes the first-line review's and the security review's
// models into their own level's slot — level 1's and level 3's — and answers
// the crafts it wrote, by craft name. It is the second half of MC12's write,
// and it holds the whole of F14: a craft decides a level's model, never whether
// the level runs, so a level that does not run, and a level that wrote a model
// of its own, keep their slot — the craft's model reaches it nowhere, and a
// craft written nowhere the run reads is a craft the run charges nothing for
// and refuses nothing for.
//
// running is which levels run with the configuration as it stands (the caller's
// own answer, CraftLevelsRunning below): the question is the run's, and a
// caller judging the crafts before the run exists — a launch (MC14) — asks it
// with the same function rather than a copy of the rule.
func WriteCraftLevels(cfg *config.Config, crafts map[string]craft.Effective, running map[policy.Level]bool) map[string]bool {
	written := map[string]bool{}
	for _, stage := range []string{"review_first", "review", stageAdversarial} {
		level, ok := stageLevel(stage)
		if !ok || !running[level] || cfgLevelModel(cfg, level) != "" {
			continue
		}
		if m, name := craft.ModelFor(crafts, stage); m != "" {
			setCfgLevelModel(cfg, level, m)
			written[name] = true
		}
	}
	return written
}

// CraftLevelsRunning is craftLevelsRunning for a configuration a caller holds
// before a run exists: which of the three review levels run, asked of the
// resolver the run's own levels are resolved by (config.ResolveLevels) rather
// than answered here, so that a launcher judging the crafts before it writes a
// task (MC14, internal/cli's launchReviewLevelsAsk) cannot answer differently
// from the run it would start.
//
// in.CraftModels is not read: it decides the model a level runs, and this is
// the level's own switch.
func CraftLevelsRunning(in config.LevelInputs) map[policy.Level]bool {
	if !rigor.Known(in.Mode) {
		return nil
	}
	levels, _, err := config.ResolveLevels(in)
	if err != nil {
		// A configuration the resolver refuses resolves no levels, and the run
		// is halted by that same refusal where the levels are resolved for real
		// (resolveLevels): a refused run gets no write either.
		return nil
	}
	return map[policy.Level]bool{
		policy.LevelL1: levels.L1.On,
		policy.LevelL2: levels.L2.On,
		policy.LevelL3: levels.L3.On,
	}
}

// craftLevelsRunning is which of the three review levels run with the run's
// configuration as it stands, before a craft's model is written into it. The
// craft decides a level's model, never whether the level runs (F14), and a
// block that writes a key is how a layer turns a level on (config's writesLevel
// and layerAnswer): a craft's model written into a level the run's own layers
// left off would be the craft turning that level on, which for level 3 is
// exactly what no craft key may do.
//
// It is asked on the configuration as it stands — the crafts' role slots are
// already written (WriteCraftRoles), and the grade, the switch and the vision
// slot have not run yet. Level 1 is the one level whose answer reads a model
// (agent.first_line_review: auto), so its answer is the one this write can be
// wrong about: where the run's later answer turns level 1 on and this one left
// it off — a coder the grade or the switch moves onto a model a route claims —
// the craft's model does not reach the level and it runs the model its own slot
// resolves to; where the pins move the other way — the vision slot moving the
// coder onto a model no route claims, the switch routing the reviewer's — the
// level runs on the craft's model where the run's own answer would have left it
// off. Levels 2 and 3 read no model, so the answer taken here is their answer
// everywhere.
func (r *run) craftLevelsRunning() map[policy.Level]bool {
	mode := r.reachedRigor()
	return CraftLevelsRunning(config.LevelInputs{
		Config:         r.cfg,
		Mode:           mode,
		Floor:          r.policy.ReviewFloor(mode),
		Task:           r.spec.ReviewLevels,
		Recipe:         r.rcp,
		CatalogDefault: r.derived,
		Providers:      r.modelProviders(),
	})
}
