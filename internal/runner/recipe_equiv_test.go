package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// Also pinned by existing tests, read and confirmed, and so not repeated here:
//   - first_line_review: auto with a provider coder is on, with a Claude coder
//     off: TestFirstLineAutoOnlyForProviderCoder, TestFirstLineOffAndClaudeOnlyUnchanged
//   - a skippable first-line failure (agent error, is_error, no verdict):
//     TestFirstLineFailureSkipped (the rows below pin the unparseable and
//     erroring cases in the golden table as well)
//   - follow-up implement prompt, resume, ErrNoSession fallback to fresh,
//     other resume error fails the stage, review addendum:
//     TestFollowUpResumesSession, TestFollowUpFreshWithoutSession,
//     TestFollowUpResumeFailureFallsBackFresh, TestFollowUpResumeOtherErrorFailsStage,
//     TestFollowUpReviewSeesComments
//   - token cap halts after a review and after a first-line stage:
//     TestTokenCapHaltsAtStageBoundary, TestFirstLineCostCountsToCap
//   - a PR closed during the run stops the loop after implement:
//     TestFollowUpPRMergedDuringRunNoPush (a close during review or fix is
//     not pinned by any test)
//
// TestDefaultLoopGolden pins the loop of a repository that names no recipe:
// stage order, review tiers, each call's model and the outcome. It was
// written against the hard-coded loop and must pass unchanged on the
// recipe-driven one (docs/design/recipes.md §10, equivalence).
func TestDefaultLoopGolden(t *testing.T) {
	one := 1
	override := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature",
		Overrides: task.Overrides{ReviewRounds: &one}}
	for _, c := range []struct {
		name    string
		rounds  int
		extra   string
		spec    *task.Spec
		steps   []step
		stages  []string
		tiers   []string
		models  []string
		outcome runstore.Outcome
	}{
		{"first line off", 2, "  first_line_review: off\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("ship", 0)},
			[]string{"implement", "review", "fix", "review"}, []string{":changes", ":ship"},
			[]string{"coder-model", "senior-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line on", 2, "  first_line_review: on\n  first_line_rounds: 1\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("ship", 0)},
			[]string{"implement", "review_first", "fix", "review"}, []string{"first:changes", "senior:ship"},
			[]string{"coder-model", "coder-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line hands over on ship", 1, "  first_line_review: on\n  first_line_rounds: 2\n", nil,
			[]step{implement("feature"), review("ship", 0), review("changes", 2)},
			[]string{"implement", "review_first", "review"}, []string{"first:ship", "senior:changes"},
			[]string{"coder-model", "coder-model", "senior-model"}, runstore.OutcomeDraft},
		{"rounds used up", 2, "  first_line_review: off\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("changes", 1)},
			[]string{"implement", "review", "fix", "review"}, []string{":changes", ":changes"},
			[]string{"coder-model", "senior-model", "coder-model", "senior-model"}, runstore.OutcomeDraft},
		{"senior ships on round 1", 2, "  first_line_review: off\n", nil,
			[]step{implement("feature"), review("ship", 0)},
			[]string{"implement", "review"}, []string{":ship"},
			[]string{"coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line auto with a Claude coder is off", 2, "", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("ship", 0)},
			[]string{"implement", "review", "fix", "review"}, []string{":changes", ":ship"},
			[]string{"coder-model", "senior-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line failure is skipped", 1, "  first_line_review: on\n  first_line_rounds: 2\n", nil,
			[]step{implement("feature"), failStep, review("ship", 0)},
			[]string{"implement", "review_first", "review"}, []string{"first:none", "senior:ship"},
			[]string{"coder-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line without a verdict is skipped", 1, "  first_line_review: on\n  first_line_rounds: 2\n", nil,
			[]step{implement("feature"), noVerdictStep, review("ship", 0)},
			[]string{"implement", "review_first", "review"}, []string{"first:none", "senior:ship"},
			[]string{"coder-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line rounds exhausted on changes", 1, "  first_line_review: on\n  first_line_rounds: 2\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("changes", 1), fixStep, review("ship", 0)},
			[]string{"implement", "review_first", "fix", "review_first", "fix", "review"}, []string{"first:changes", "first:changes", "senior:ship"},
			[]string{"coder-model", "coder-model", "coder-model", "coder-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"task override wins", 3, "  first_line_review: off\n", override,
			[]step{implement("feature"), review("changes", 1)},
			[]string{"implement", "review"}, []string{":changes"},
			[]string{"coder-model", "senior-model"}, runstore.OutcomeDraft},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, firstLineCfg(t, c.rounds, c.extra), c.spec)
			rec, err := h.run(t, c.steps...)
			if err != nil {
				t.Fatal(err)
			}
			if got := stageNames(rec); !eq(got, c.stages...) {
				t.Errorf("stages = %v, want %v", got, c.stages)
			}
			if got := tiers(rec); !eq(got, c.tiers...) {
				t.Errorf("tiers = %v, want %v", got, c.tiers)
			}
			// Each call's model with its stage; a fix resumes the implement
			// session, every other stage after implement starts its own.
			stages := stageNames(rec)
			if len(stages) != len(h.agent.calls) {
				t.Fatalf("%d stages, %d agent calls", len(stages), len(h.agent.calls))
			}
			var models, want []string
			for i, call := range h.agent.calls {
				models = append(models, stages[i]+"="+call.Model)
				want = append(want, c.stages[i]+"="+c.models[i])
				if stages[i] == "fix" && (!call.Resume || call.SessionID != h.agent.calls[0].SessionID) {
					t.Errorf("call %d (fix): resume %v, session %q, want implement's %q", i, call.Resume, call.SessionID, h.agent.calls[0].SessionID)
				}
				if stages[i] != "fix" && call.Resume {
					t.Errorf("call %d (%s) resumes a session", i, stages[i])
				}
			}
			if !eq(models, want...) {
				t.Errorf("stage=model = %v, want %v", models, want)
			}
			if rec.Outcome != c.outcome {
				t.Errorf("outcome = %s, want %s (reason %q)", rec.Outcome, c.outcome, rec.Reason)
			}
		})
	}
}

func failStep(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	return agent.Result{}, errors.New("upstream unavailable")
}

func noVerdictStep(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	return agent.Result{Text: "looks fine to me"}, nil
}
