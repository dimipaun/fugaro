package runner

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// gradedRun builds the run a model grade needs: a coder pin of its own, a
// reviewer the grade must leave alone, a coder craft already sent out on one of
// the owner's credentials (D1628), and a task that names a grade.
func gradedRun(t *testing.T, grade escalation.Grade, grades policy.Grades) *run {
	t.Helper()
	return &run{
		cfg: &config.Config{Agent: config.Agent{
			Models: config.ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5"},
			Crafts: config.Crafts{Coder: config.Craft{Model: "claude-sonnet-5-5", Access: "acme-own-key"}},
		}},
		spec:   &task.Spec{Version: 1, Repo: "acme/server", Ref: "main", Task: "settle the retry loop", ModelGrade: grade},
		policy: policy.Effective{Grades: grades},
	}
}

// TestRoutingResolvesGrade (E4): a task that names a grade runs on the model
// that grade names in the project's own table, out on the credential the grade
// names with it — the two halves of one choice, taken together.
//
// Hand mutations this fails on: ignoring the task's grade (the coder stays on
// agent.models.coder, which is the red step this test was written from);
// resolving the model and leaving the access behind (the grade's calls would go
// out on whatever the craft already named); reading the grade from a layer the
// task can reach rather than from the project's (the value would be the task's
// own, which TestRoutingGradeCannotNameRawModel refuses).
func TestRoutingResolvesGrade(t *testing.T) {
	r := gradedRun(t, escalation.Strong, policy.Grades{
		config.GradeStrong: {Model: "claude-opus-5-5", Access: "openrouter-main"},
	})
	if got := r.stageModel("implement"); got != "claude-sonnet-5-5" {
		t.Fatalf("the run starts on %q, want the configuration's own coder model", got)
	}
	if err := r.applyGrade(); err != nil {
		t.Fatalf("applyGrade: %v", err)
	}
	if got := r.stageModel("implement"); got != "claude-opus-5-5" {
		t.Errorf("the coder runs %q, want the grade's model: the task named it and the project layer resolved it", got)
	}
	if got := r.cfg.Agent.Crafts.Coder.Access; got != "openrouter-main" {
		t.Errorf("the coder craft's access = %q, want the grade's: a grade is one choice in two halves", got)
	}
}

// TestRoutingGradeUnsetRefuses (E4, G11, D2039): a grade the project sets
// nothing for halts the run rather than falling back to a model, and so does a
// task naming one grade when the project set only the other — the run must not
// reach for the strongest model because a task asked for one.
func TestRoutingGradeUnsetRefuses(t *testing.T) {
	r := gradedRun(t, escalation.Strongest, nil)
	err := r.applyGrade()
	if err == nil {
		t.Fatal("a task at a grade the project sets nothing for ran: no default ships for either grade, so the run has to ask")
	}
	if !strings.Contains(err.Error(), string(escalation.Strongest)) || !strings.Contains(err.Error(), "defaults.model_grades") {
		t.Errorf("applyGrade() error = %q, want it to name the grade and where the owner sets it", err)
	}
	if got := r.stageModel("implement"); got != "claude-sonnet-5-5" {
		t.Errorf("the refused run's coder = %q, want the configuration's own model untouched", got)
	}
	if got := r.cfg.Agent.Crafts.Coder.Access; got != "acme-own-key" {
		t.Errorf("the refused run's coder craft access = %q, want it untouched", got)
	}
	// The project set one grade, and the task names the other: a grade is the
	// name's own model, never the nearest one the project happens to have.
	other := gradedRun(t, escalation.Strong, policy.Grades{
		config.GradeStrongest: {Model: "claude-opus-5-5"},
	})
	if err := other.applyGrade(); err == nil {
		t.Error("a task naming `strong` ran on the project's `strongest`: a grade resolves by name or not at all")
	}
}

// TestRoutingGradeCannotNameRawModel (E4, G11): the field is a grade in the
// ladder's vocabulary and never a model ID or a credential name. A task that
// writes one is refused even when the project's table carries that exact string
// as a model — the lookup is by grade name, so nothing a task writes can reach
// a model or a credential the project did not put behind a grade.
//
// Hand mutation this fails on: letting the task's value through as a model when
// the project's table has no such grade (the raw name would become the coder's
// pin, and a task could pick any model the owner allows anywhere).
func TestRoutingGradeCannotNameRawModel(t *testing.T) {
	const raw = "anthropic/claude-opus-5.5"
	grades := policy.Grades{
		config.GradeStrong:    {Model: "claude-sonnet-5-5", Access: "openrouter-main"},
		config.GradeStrongest: {Model: raw},
	}
	for _, name := range []string{raw, "openrouter-main", "claude-opus-5", "Strong", "STRONGEST", "strongest "} {
		r := gradedRun(t, escalation.Grade(name), grades)
		err := r.applyGrade()
		if err == nil {
			t.Fatalf("a task naming %q as its model grade ran on %q: a task names a grade, not a model or a credential (G11)", name, r.stageModel("implement"))
		}
		// The refusal is the vocabulary's, not the project's: a run that
		// reported an unset grade here would send the owner to set
		// `defaults.model_grades.anthropic/claude-opus-5.5`.
		if !strings.Contains(err.Error(), "G11") {
			t.Errorf("applyGrade(%q) error = %q, want the grade vocabulary's refusal", name, err)
		}
		if got := r.stageModel("implement"); got != "claude-sonnet-5-5" {
			t.Errorf("after refusing %q the coder = %q, want the configuration's own model", name, got)
		}
	}
}

// TestRoutingGradeAppliesToCoderAndJudge (E4): the grade's model is what the
// judge stage runs — the stage a registered step's calls are charged to, which
// pins the coder's own role — and it is not what the reviewer runs. The write
// lands in the coder's slot rather than agent.model, so a run whose reviewer
// names no model of its own keeps the model its own configuration gave it
// rather than the pass's.
func TestRoutingGradeAppliesToCoderAndJudge(t *testing.T) {
	r := gradedRun(t, escalation.Strong, policy.Grades{
		config.GradeStrong: {Model: "claude-opus-5-5"},
	})
	if err := r.applyGrade(); err != nil {
		t.Fatalf("applyGrade: %v", err)
	}
	if got := r.stageModel(evaluateStage); got != "claude-opus-5-5" {
		t.Errorf("the judge stage runs %q, want the grade's model: the judge pins the coder's role, and one stronger single pass is what the rung bought", got)
	}
	// The grade carries no access, and the repository's own craft stands: a
	// grade is silent about a credential it does not name, not a revocation of
	// the selection docs/model-crafts grants the repository.
	if got := r.cfg.Agent.Crafts.Coder.Access; got != "acme-own-key" {
		t.Errorf("the coder craft access = %q, want the repository's own: a grade that names no credential writes none", got)
	}
	if got := r.cfg.Agent.Crafts.MergeJudge.Access; got != "" {
		t.Errorf("the merge judge craft access = %q, want it untouched: a grade that names no credential writes none", got)
	}
	// And the access half lands on the judge's craft as well as the coder's:
	// the judge's stage is charged to the merge-judge craft (stageCraft), so a
	// writer that half-filled the pair would send the judge's calls out on the
	// run's own default credential instead of the one the grade named — the
	// spending and egress grant the ladder bought, unread by the stage it
	// bought it for.
	withAccess := gradedRun(t, escalation.Strong, policy.Grades{
		config.GradeStrong: {Model: "claude-opus-5-5", Access: "openrouter-main"},
	})
	if err := withAccess.applyGrade(); err != nil {
		t.Fatalf("applyGrade: %v", err)
	}
	if got := withAccess.cfg.Agent.Crafts.Coder.Access; got != "openrouter-main" {
		t.Errorf("the coder craft access = %q, want the grade's", got)
	}
	if got := withAccess.cfg.Agent.Crafts.MergeJudge.Access; got != "openrouter-main" {
		t.Errorf("the merge judge craft access = %q, want the grade's: the judge's stage is charged to that craft, so the one choice in two halves lands on both", got)
	}
	// A run whose reviewer names no model of its own falls back to agent.model,
	// which the grade left alone.
	fallback := gradedRun(t, escalation.Strong, policy.Grades{config.GradeStrong: {Model: "claude-opus-5-5"}})
	fallback.cfg.Agent.Models.Reviewer = ""
	if err := fallback.applyGrade(); err != nil {
		t.Fatalf("applyGrade: %v", err)
	}
	if got := fallback.stageModel("review"); got != "" {
		t.Errorf("the reviewer runs %q, want the run's own agent.model: the grade is the coder's, not the whole run's", got)
	}
	if got := fallback.stageModel("implement"); got != "claude-opus-5-5" {
		t.Errorf("the coder runs %q, want the grade's model", got)
	}
}

// TestRoutingGradeAndTheRecipesRoleMapping (E4): the recipe's roles: mapping
// and the task's grade both say something about the coder's model, and the two
// directions end differently. roles: {reviewer: coder} copies the coder's
// model, so the mapping is applied again after the grade and the reviewer
// follows the coder onto it — a pair the mapping says runs one model does not
// split because a rung escalated. roles: {coder: reviewer} — premium's "coder
// on the reviewer's model" — names the coder's model itself and cannot hold
// beside the grade, so the grade stands and the run says in its log that the
// mapping is the half that gave way.
func TestRoutingGradeAndTheRecipesRoleMapping(t *testing.T) {
	grades := policy.Grades{config.GradeStrong: {Model: "claude-opus-5-5"}}
	t.Run("the reviewer runs as the coder", func(t *testing.T) {
		r := gradedRun(t, escalation.Strong, grades)
		r.rcp = &recipe.Recipe{Name: "thrifty", ReviewerIsCoder: true}
		ApplyRoles(&r.cfg.Agent, r.rcp)
		if got := r.cfg.Agent.Models.Reviewer; got != "claude-sonnet-5-5" {
			t.Fatalf("the mapping left the reviewer on %q, want the coder's model", got)
		}
		if err := r.applyGrade(); err != nil {
			t.Fatalf("applyGrade: %v", err)
		}
		if got := r.cfg.Agent.Models.Coder; got != "claude-opus-5-5" {
			t.Errorf("the coder runs %q, want the grade's model", got)
		}
		if got := r.cfg.Agent.Models.Reviewer; got != "claude-opus-5-5" {
			t.Errorf("the reviewer runs %q, want the model the coder now runs: roles: {reviewer: coder} says the two are one model, and a grade moves it rather than breaking the pair", got)
		}
	})
	t.Run("the coder runs as the reviewer", func(t *testing.T) {
		var logs bytes.Buffer
		r := gradedRun(t, escalation.Strong, grades)
		r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
		r.rcp = &recipe.Recipe{Name: "premium", CoderIsReviewer: true}
		ApplyRoles(&r.cfg.Agent, r.rcp)
		if got := r.cfg.Agent.Models.Coder; got != "claude-opus-5" {
			t.Fatalf("the mapping left the coder on %q, want the reviewer's model", got)
		}
		if err := r.applyGrade(); err != nil {
			t.Fatalf("applyGrade: %v", err)
		}
		if got := r.cfg.Agent.Models.Coder; got != "claude-opus-5-5" {
			t.Errorf("the coder runs %q, want the grade's model: the task's own choice of the coder's model is the more particular of the two", got)
		}
		if got := r.cfg.Agent.Models.Reviewer; got != "claude-opus-5" {
			t.Errorf("the reviewer runs %q, want the model the mapping gave it: the grade is the coder's alone", got)
		}
		if !strings.Contains(logs.String(), "model grade replaces the coder model") {
			t.Errorf("the run did not say the recipe's mapping gave way to the grade: a mapping nothing reports is a mapping the reader will believe is still in force\n%s", logs.String())
		}
	})
}

// TestGradesRideTheCeilingFromThePolicyNode (E4): the grades a run resolves
// against come from the published policy node — the same delivery path the
// routing switch takes (D1a) — and only the grades the node writes: an owner
// who sets one grade publishes one, and a run that fell back to a default for
// the other would run a task at a model he never chose.
func TestGradesRideTheCeilingFromThePolicyNode(t *testing.T) {
	r := &run{}
	pp := &projectpolicy.Policy{ModelGrades: &config.ModelGrades{
		Strong: &config.Craft{Model: "anthropic/claude-opus-5.5", Access: "openrouter-main"},
	}}
	layer, _, set, err := r.policyCeiling(pp, envCeiling(Spend{}, ""), Spend{})
	if err != nil {
		t.Fatalf("policyCeiling: %v", err)
	}
	if got, want := layer.Grades[config.GradeStrong], (policy.Grade{Model: "anthropic/claude-opus-5.5", Access: "openrouter-main"}); got != want {
		t.Errorf("the ceiling's strong grade = %+v, want %+v", got, want)
	}
	if _, ok := layer.Grades[config.GradeStrongest]; ok {
		t.Error("the ceiling carries a strongest grade the node never wrote")
	}
	// The key is named in the node's own set, which is what makes the merge
	// judge the grade against the node's copy of the job's layers rather than
	// reporting the node's own write as a layer's attempt to write one.
	if !set[policy.ModelGradeKey(config.GradeStrong)] {
		t.Errorf("the node's set = %v, want it to name %s", set, policy.ModelGradeKey(config.GradeStrong))
	}
}
