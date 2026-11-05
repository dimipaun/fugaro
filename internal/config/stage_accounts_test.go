package config

import (
	"strings"
	"testing"
)

// TestRunStagesAreTheModelCallingStages pins the closed vocabulary the
// `stages.<stage>.claude_account` segment is held to
// (docs/accounts-add/spec.md §11.1): the stages a run spends a model call on,
// and nothing else. The names are the runner's own stage literals plus
// recipe.StepAdversarial, and the stages that spend no model call —
// bootstrap, render, finalize, writeback and provision — are deliberately
// absent: no account could apply to them, so naming one is refused rather than
// silently never applying.
func TestRunStagesAreTheModelCallingStages(t *testing.T) {
	want := []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}
	if len(RunStages) != len(want) {
		t.Fatalf("RunStages = %v, want %v", RunStages, want)
	}
	for i, stage := range want {
		if RunStages[i] != stage {
			t.Fatalf("RunStages = %v, want %v", RunStages, want)
		}
		if !IsRunStage(stage) {
			t.Errorf("IsRunStage(%q) = false", stage)
		}
	}
	for _, stage := range []string{"bootstrap", "render", "finalize", "writeback", "provision", "Review", ""} {
		if IsRunStage(stage) {
			t.Errorf("IsRunStage(%q) = true, want false", stage)
		}
		if ps := CheckStageSegment(stage, "stages"); len(ps) != 1 || !strings.Contains(ps[0].Message, "is not a stage of a run") {
			t.Errorf("CheckStageSegment(%q) = %+v, want one refusal", stage, ps)
		}
	}
	if ps := CheckStageSegment("review", "stages"); ps != nil {
		t.Errorf("CheckStageSegment(review) = %+v, want none", ps)
	}
}

// TestCheckStageAccountMixIsTheB0Ruling is the ruling of
// docs/design/mixed-credentials.md §11 read as the one function every layer
// that judges it calls: an api-key run holds one provider key and one Claude
// account, and that account serves the senior review and the adversarial
// review and no other stage.
func TestCheckStageAccountMixIsTheB0Ruling(t *testing.T) {
	cases := []struct {
		name   string
		auth   string
		stages map[string]string
		want   [][]string // one entry per expected problem, each the substrings it must name
	}{
		{name: "api-key, the permitted mix", auth: "api-key",
			stages: map[string]string{"review": "acme-overflow", "adversarial": "acme-overflow"}},
		{name: "api-key, review alone", auth: "api-key", stages: map[string]string{"review": "acme-overflow"}},
		{name: "api-key, no stage account", auth: "api-key", stages: nil},
		{name: "api-key, the coder's stage refused", auth: "api-key", stages: map[string]string{"implement": "acme-overflow"},
			want: [][]string{{"stages.implement.claude_account", "would never be used", "review and adversarial"}}},
		{name: "api-key, review_first refused", auth: "api-key", stages: map[string]string{"review_first": "acme-overflow"},
			want: [][]string{{"stages.review_first.claude_account", "review and adversarial"}}},
		{name: "api-key, fix refused", auth: "api-key", stages: map[string]string{"fix": "acme-overflow"},
			want: [][]string{{"stages.fix.claude_account"}}},
		{name: "api-key, plan refused", auth: "api-key", stages: map[string]string{"plan": "acme-overflow"},
			want: [][]string{{"stages.plan.claude_account"}}},
		{name: "api-key, two accounts refused", auth: "api-key",
			stages: map[string]string{"review": "acme-overflow", "adversarial": "acme-personal"},
			want:   [][]string{{"stages", "one provider key and one Claude account", "acme-overflow, acme-personal"}}},
		{name: "api-key, a stage the tree does not have yet", auth: "api-key", stages: map[string]string{"digest": "acme-overflow"},
			want: [][]string{{"stages.digest.claude_account"}}},
		// Two problems at once: the stage key first (the sorted walk), the count
		// after it, so the sentence a person reads starts with the key to fix.
		{name: "api-key, a bad stage and a second account", auth: "api-key",
			stages: map[string]string{"implement": "acme-overflow", "review": "acme-personal"},
			want:   [][]string{{"stages.implement.claude_account"}, {"stages", "one provider key and one Claude account"}}},
		// The accounts design's own rung 2 is what an oauth run's stage key is
		// for: one account for a stage and another for the rest is admitted, and
		// no stage is out of bounds.
		{name: "oauth keeps the accounts design's meaning", auth: "oauth",
			stages: map[string]string{"implement": "acme-overflow", "review": "acme-personal", "fix": "acme-overflow"}},
		{name: "oauth names one account", auth: "oauth", stages: map[string]string{"review": "acme-overflow"}},
		{name: "oauth names none", auth: "oauth", stages: nil},
		// "Never under another auth": neither vertex nor cascade reaches a Claude
		// OAuth credential, so an account there could never be used.
		{name: "vertex refused", auth: "vertex", stages: map[string]string{"review": "acme-overflow"},
			want: [][]string{{"stages.review.claude_account", "this run's is vertex"}}},
		{name: "cascade refused", auth: "cascade", stages: map[string]string{"review": "acme-overflow"},
			want: [][]string{{"stages.review.claude_account", "this run's is cascade"}}},
		{name: "vertex with no stage account", auth: "vertex", stages: nil},
		// The launch resolves the auth from a checkout it may not have yet, so
		// an auth it cannot place is not judged here: the run judges it with the
		// auth it resolves.
		{name: "an unknown auth is not judged", auth: "", stages: map[string]string{"implement": "acme-overflow"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := CheckStageAccountMix(tc.auth, tc.stages, "stages")
			if len(ps) != len(tc.want) {
				t.Fatalf("CheckStageAccountMix(%q, %v) = %+v, want %d problem(s)", tc.auth, tc.stages, ps, len(tc.want))
			}
			for i, wants := range tc.want {
				for _, want := range wants {
					if !strings.Contains(ps[i].Path, want) && !strings.Contains(ps[i].Message, want) {
						t.Errorf("problem %d = %+v, want it to name %q", i, ps[i], want)
					}
				}
			}
		})
	}
}
