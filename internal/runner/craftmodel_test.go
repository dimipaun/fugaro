package runner

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The model half of the crafts, at the one place it is decided: a run whose
// crafts name models resolves them in resolvePolicy, and every reader of a
// stage's model — the pins, the gateway's allow-list judgement, the cost row
// and the record — names the same one (D1661).

const (
	// theCraftModel is the provider's model a craft names, and coderRoleModel the
	// Claude one the configuration names beside it: the two are deliberately
	// different, so a run that kept the role's model is visible.
	theCraftModel  = "deepseek-v4.1-flash"
	coderRoleModel = "claude-sonnet-5-5"
	// reviewerRoleModel is the model the reviewer's role carries, distinct from both
	// so a write into the wrong slot shows.
	reviewerRoleModel = "claude-opus-5"
	// anotherCraftModel is a second craft's own model.
	anotherCraftModel = "qwen3-coder"
)

// craftRun is a run at bootstrap with the crafts its configuration names: the
// least resolvePolicy reads, at pro, with no policy node published.
func craftRun(t *testing.T, agent config.Agent) *run {
	t.Helper()
	r := &run{
		cfg:  &config.Config{Agent: agent},
		spec: &task.Spec{Version: 1, Repo: "acme/app", Ref: "main", Task: "the crafts' models"},
		rec: &runstore.Record{Version: 1, Rigor: &runstore.RigorRecord{
			Mode: rigor.Pro, From: rigor.FromDefault,
		}},
		d: Deps{Log: slog.New(slog.DiscardHandler), Now: time.Now},
	}
	r.projPolicy = policyRead{done: true}
	return r
}

// craftAgent is the configuration the tests start from: both roles named, so
// the allow-list and the pins have something to judge, and the background
// model with them.
func craftAgent(crafts config.Crafts) config.Agent {
	return config.Agent{
		Models: config.ModelRoles{Coder: coderRoleModel, Reviewer: reviewerRoleModel, Background: "claude-haiku-4-5"},
		Crafts: crafts,
	}
}

// bootstrap resolves the run's policy, the way the run's own bootstrap does.
func bootstrap(t *testing.T, r *run) {
	t.Helper()
	if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
		t.Fatalf("resolvePolicy: %v", err)
	}
}

// applyCraftsOrFail is the write on its own, for a test that steps the
// rewrites by hand rather than through resolvePolicy.
func applyCraftsOrFail(t *testing.T, r *run) {
	t.Helper()
	if err := r.applyCrafts(context.Background()); err != nil {
		t.Fatalf("applyCrafts: %v", err)
	}
}

// TestCraftModelReachesTheStageSlot is the red step this task was written from:
// a run whose coder craft names a model runs it, in all four places a stage's
// model is read.
//
// Hand mutations this fails on: not reading the model half of the crafts at all
// (the run keeps agent.models.coder, which is the state before this task);
// writing it after applyRouting (the switch never sees it, so an owner who
// routes the craft's model finds the run sending it direct); writing it after
// the levels (level 1, which runs the coder's model, keeps the model the
// level resolved).
func TestCraftModelReachesTheStageSlot(t *testing.T) {
	r := craftRun(t, craftAgent(config.Crafts{Coder: config.Craft{Model: theCraftModel}}))
	bootstrap(t, r)

	// The stage's model, which is what cost.go's row and the record read.
	if got := r.stageModel("implement"); got != theCraftModel {
		t.Errorf("implement runs %q, want the coder craft's %q: the craft's model is rung 3 and agent.models.coder is rung 4", got, theCraftModel)
	}
	// The pins the agent is given.
	if pins := r.cfg.Agent.PinnedModels(); !slices.Contains(pins, theCraftModel) {
		t.Errorf("PinnedModels() = %v, want the coder craft's %q among them", pins, theCraftModel)
	}
	if got := r.cfg.Agent.Models.Coder; got != theCraftModel {
		t.Errorf("agent.models.coder = %q, want the craft's %q: the write lands in the slot the readers already read", got, theCraftModel)
	}
	// The gateway's own judgement of the pins, over an allow-list that names
	// the craft's model and the reviewer's: a coder still pinned to the role's
	// model is refused here and nowhere else.
	allowed := policy.Effective{AllowedModels: []string{theCraftModel, reviewerRoleModel, "claude-haiku-4-5"}}
	if ps := config.CheckAllowed(r.cfg.Agent, allowed, nil, nil, nil); len(ps) != 0 {
		t.Errorf("CheckAllowed refused the run: %v\nthe gateway judges the pins, so the craft's model has to be the one pinned", ps)
	}
	// The row the record's own stage timing carries.
	if got := r.stageTiming("implement", time.Now(), 0, 0, 0).Model; got != theCraftModel {
		t.Errorf("the stage's timing names %q, want %q: the record and the pins are the same answer", got, theCraftModel)
	}
}

// TestOneAnswerForEveryConsumer: every stage of the run names one model, and
// it is the craft's — the pins, the gateway's set, the cost row and the record
// agree about each stage rather than resolving it a second time.
func TestOneAnswerForEveryConsumer(t *testing.T) {
	r := craftRun(t, craftAgent(config.Crafts{
		Coder:        config.Craft{Model: theCraftModel},
		SeniorReview: config.Craft{Model: reviewerRoleModel},
	}))
	// Level 2 runs and level 1 does too, so all three review crafts answer a
	// stage: the first-line review runs on the review craft's model, which
	// inherits the coder's.
	r.cfg.Agent.FirstLineReview = config.FirstLineOn
	bootstrap(t, r)

	for _, tt := range []struct {
		stage string
		craft string
	}{
		{"plan", config.CraftCoder},
		{"implement", config.CraftCoder},
		{"fix", config.CraftCoder},
		{"review_first", config.CraftReview},
		{"review", config.CraftSeniorReview},
		{"triage", config.CraftSeniorReview},
	} {
		model, name := craft.ModelFor(r.stageCrafts(context.Background()), tt.stage)
		if name != tt.craft {
			t.Fatalf("stage %s answers the %s craft, want %s: this test's table is stale", tt.stage, name, tt.craft)
		}
		if got := r.stageModel(tt.stage); got != model {
			t.Errorf("stage %s runs %q, want the %s craft's %q", tt.stage, got, tt.craft, model)
		}
		if got := r.stageTiming(tt.stage, time.Now(), 0, 0, 0).Model; got != model {
			t.Errorf("stage %s is recorded as running %q, want %q: the record resolves the stage a second time", tt.stage, got, model)
		}
		if pins := r.cfg.Agent.PinnedModels(); !slices.Contains(pins, model) {
			t.Errorf("stage %s runs %q, which is in none of the pins %v", tt.stage, model, pins)
		}
	}
}

// TestALevelModelBeatsACraftModel: the level's own model is the rung above the
// craft's, so a level that names one keeps it.
func TestALevelModelBeatsACraftModel(t *testing.T) {
	agent := craftAgent(config.Crafts{SeniorReview: config.Craft{Model: theCraftModel}})
	agent.Levels.L2 = &config.LevelSetting{Model: anotherCraftModel}
	r := craftRun(t, agent)
	bootstrap(t, r)

	if got := r.stageModel("review"); got != anotherCraftModel {
		t.Errorf("review runs %q, want the level's own %q: agent.levels.l2.model is the rung above the craft's", got, anotherCraftModel)
	}
	if got := r.levelModel(policy.LevelL2); got != anotherCraftModel {
		t.Errorf("the level's block names %q, want %q: the craft's model must not land on a level that wrote one", got, anotherCraftModel)
	}
	// The craft's model still moves the role slot it owns: the two are
	// separate writes, and the reviewer's role is not the level's.
	if got := r.cfg.Agent.Models.Reviewer; got != theCraftModel {
		t.Errorf("agent.models.reviewer = %q, want the craft's %q", got, theCraftModel)
	}
}

// TestAModelGradeBeatsACraftModel: a task's grade is rung 1, above the craft's
// rung 3, and it is applied after the crafts for exactly that reason.
func TestAModelGradeBeatsACraftModel(t *testing.T) {
	r := craftRun(t, craftAgent(config.Crafts{Coder: config.Craft{Model: theCraftModel}}))
	r.spec.ModelGrade = escalation.Strong
	r.policy = policy.Effective{Grades: policy.Grades{
		config.GradeStrong: {Model: anotherCraftModel},
	}}
	applyCraftsOrFail(t, r)
	if got := r.stageModel("implement"); got != theCraftModel {
		t.Fatalf("the coder runs %q before the grade, want the craft's %q", got, theCraftModel)
	}
	if err := r.applyGrade(); err != nil {
		t.Fatalf("applyGrade: %v", err)
	}
	if got := r.stageModel("implement"); got != anotherCraftModel {
		t.Errorf("the coder runs %q, want the grade's %q: a task's grade is the rung above the craft's", got, anotherCraftModel)
	}
}

// TestCraftModelInheritsLikeTheResolver: a craft that names no model of its own
// is the model it inherits — the first-line review follows the coder — and the
// level it fills takes that inherited model, not the empty half of the block.
func TestCraftModelInheritsLikeTheResolver(t *testing.T) {
	agent := craftAgent(config.Crafts{Coder: config.Craft{Model: theCraftModel}})
	agent.FirstLineReview = config.FirstLineOn
	r := craftRun(t, agent)
	bootstrap(t, r)

	if got := r.levelModel(policy.LevelL1); got != theCraftModel {
		t.Errorf("level 1 names %q, want the coder craft's %q: the review craft inherits the coder's model", got, theCraftModel)
	}
	if got := r.stageModel("review_first"); got != theCraftModel {
		t.Errorf("review_first runs %q, want %q", got, theCraftModel)
	}
	// The senior review follows nobody, so the reviewer's role keeps the
	// configuration's own model rather than inheriting the coder's.
	if got := r.cfg.Agent.Models.Reviewer; got != reviewerRoleModel {
		t.Errorf("agent.models.reviewer = %q, want the configuration's %q: the senior review inherits nothing", got, reviewerRoleModel)
	}
}

// TestCraftModelNotSetChangesNothing is F17: a craft that sets no model — no
// craft block at all, or one that sets only its access — leaves the run
// exactly as it was, so no installation's behaviour moves on upgrade and
// `fugaro update` has nothing to write for the model half (D1662, D1666).
func TestCraftModelNotSetChangesNothing(t *testing.T) {
	// The run the configurations below are held against: no craft block at
	// all, which is every installation that has written none.
	plain := craftRun(t, craftAgent(config.Crafts{}))
	bootstrap(t, plain)
	want := modelAnswers(plain)
	for _, tt := range []struct {
		name   string
		crafts config.Crafts
	}{
		{"no craft block", config.Crafts{}},
		{"crafts that name only their access", config.Crafts{
			Coder:        config.Craft{Access: "acme-own-key"},
			SeniorReview: config.Craft{Access: "openrouter-main"},
		}},
		{"a craft that names the model its role already has", config.Crafts{
			Coder: config.Craft{Model: coderRoleModel},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := craftRun(t, craftAgent(tt.crafts))
			bootstrap(t, r)
			if got := modelAnswers(r); !slices.Equal(got, want) {
				t.Errorf("the run resolves\n %v\nwant\n %v\na craft that names no model of its own changes nothing (F17)", got, want)
			}
		})
	}
}

// modelAnswers is every model a run's stages and levels resolve to, one
// "where=model" entry each: what a craft's model may move, and nothing else
// about the run.
func modelAnswers(r *run) []string {
	out := []string{
		"coder=" + r.cfg.Agent.Models.Coder,
		"reviewer=" + r.cfg.Agent.Models.Reviewer,
		"background=" + r.cfg.Agent.Models.Background,
		"vision=" + r.cfg.Agent.Models.Vision,
	}
	for _, stage := range config.RunStages {
		out = append(out, stage+"="+r.stageModel(stage))
	}
	for _, level := range policy.Levels {
		out = append(out, string(level)+"="+r.levelModel(level))
	}
	return out
}

// TestCraftModelKeepsTheRecipesRoleMapping: a recipe that maps the reviewer
// onto the coder (roles: {reviewer: coder}) means the reviewer runs whatever
// the coder runs, and the craft moves the coder — so the reviewer follows it,
// the rule applyGrade applies to the grade.
func TestCraftModelKeepsTheRecipesRoleMapping(t *testing.T) {
	r := craftRun(t, craftAgent(config.Crafts{Coder: config.Craft{Model: theCraftModel}}))
	r.rcp = &recipe.Recipe{Name: "cheap-review", Mode: recipe.ModeImplement, ReviewerIsCoder: true}
	// What config time already did, on the coder's model as it stood then: the
	// reviewer carries the configuration's own coder model.
	ApplyRoles(&r.cfg.Agent, r.rcp)
	bootstrap(t, r)

	if got := r.cfg.Agent.Models.Reviewer; got != theCraftModel {
		t.Errorf("the reviewer runs %q, want the coder craft's %q: the mapping makes the reviewer the coder's model", got, theCraftModel)
	}
	if got := r.stageModel("review"); got != theCraftModel {
		t.Errorf("review runs %q, want %q", got, theCraftModel)
	}
}

// TestSecurityCraftStillTurnsNothingOn is F14: the security review is off
// unless the review levels turn it on, and no craft key may do it — so a
// security-review craft that names a model of its own leaves level 3 where it
// was, with no block, no model and no adversarial step.
func TestSecurityCraftStillTurnsNothingOn(t *testing.T) {
	r := craftRun(t, craftAgent(config.Crafts{
		SecurityReview: config.Craft{Model: theCraftModel},
	}))
	bootstrap(t, r)

	if r.levels.L3.On {
		t.Errorf("level 3 runs at pro with only a security-review craft naming a model: the craft decided the switch, which is the review levels' (F14)")
	}
	if got := r.levelModel(policy.LevelL3); got != "" {
		t.Errorf("level 3's block names %q, want none: a craft's model must not write the level on", got)
	}
	if b := r.cfg.Agent.Levels.L3; b != nil {
		t.Errorf("level 3 gained a block %+v: writing one is how a layer turns the level on", b)
	}
	if got := r.stageModel("adversarial"); got != reviewerRoleModel {
		t.Errorf("adversarial runs %q, want the reviewer's own %q: the level did not run", got, reviewerRoleModel)
	}
}
