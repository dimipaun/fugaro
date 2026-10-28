package policy

import (
	"slices"
	"testing"
	"time"
)

// TestGovernanceDefaults pins the guardrails Fugaro ships (plan T7.1): the
// review ratio N and its floor, the fan-out cap, and the three stage
// budgets. The stage budgets are fractions of the per-run cap — fix's of the
// implement stage's own budget, G§2's "a fraction of implementation budget" —
// and no one of them may reach the cap: G6 keeps the deployed per-run cap the
// ceiling, so a stage that could spend the whole of it alone would be no
// budget at all.
func TestGovernanceDefaults(t *testing.T) {
	g := DefaultGovernance()
	if g.ReviewRatio != 5 {
		t.Errorf("review_ratio default = %d, want 5", g.ReviewRatio)
	}
	if g.ReviewFloorCalls != 20 {
		t.Errorf("review_floor_calls default = %d, want 20", g.ReviewFloorCalls)
	}
	if g.MaxSubagents != 5 {
		t.Errorf("max_subagents default = %d, want 5 (an untiered run's every stage)", g.MaxSubagents)
	}
	for _, c := range []struct {
		key string
		v   float64
	}{
		{KeyStageBudgetImplement, g.StageBudget.Implement},
		{KeyStageBudgetReview, g.StageBudget.Review},
		{KeyStageBudgetFix, g.StageBudget.Fix},
	} {
		if !(c.v > 0 && c.v < 1) {
			t.Errorf("%s default = %v: a stage budget is a fraction of the per-run cap, and no stage default may reach it (G6)", c.key, c.v)
		}
	}
	if g.StageTokens != (StageTokens{}) {
		t.Errorf("stage_tokens default = %+v, want unset", g.StageTokens)
	}
	if g.StageTimeout != (StageTimeout{}) {
		t.Errorf("stage_timeout default = %+v, want unset", g.StageTimeout)
	}
	// A layer that sets nothing is the defaults, whole: WithDefaults is how
	// the project layer's block becomes the ceiling a repository's own block
	// is held to, so an empty block has to mean the shipped guardrails rather
	// than no governance at all.
	if got := (Governance{}).WithDefaults(); got != g {
		t.Errorf("an empty layer's governance = %+v, want the defaults %+v", got, g)
	}
	// A key the layer does set is its own, looser or not: the project layer is
	// where the guardrails are loosened (plan §7), so WithDefaults must not
	// tighten it back down to the default.
	loose := Governance{ReviewRatio: 20, StageBudget: StageBudget{Implement: 0.9}}.WithDefaults()
	if loose.ReviewRatio != 20 || loose.StageBudget.Implement != 0.9 {
		t.Errorf("the project layer's own values were not kept: %+v", loose)
	}
	if loose.MaxSubagents != DefaultMaxSubagents {
		t.Errorf("a key the layer leaves out did not take the default: max_subagents = %d", loose.MaxSubagents)
	}
}

// TestGovernanceTightenOnly: the project layer sets the guardrails for every
// repository of the project, and every layer above it may only tighten them
// (plan T7.1). A looser value is dropped and recorded, a tighter one wins,
// and a key no layer set keeps the default — which is what makes the
// defaults a ceiling no run's own file can loosen.
func TestGovernanceTightenOnly(t *testing.T) {
	ceiling := Layer{Governance: Governance{
		ReviewRatio: 10, MaxSubagents: 8,
		StageBudget: StageBudget{Implement: 0.9},
	}.WithDefaults()}
	repo := Layer{Governance: Governance{
		ReviewRatio: 20, MaxSubagents: 3, ReviewFloorCalls: 4,
		StageBudget:  StageBudget{Implement: 0.95, Review: 0.2, Fix: 0.1},
		StageTokens:  StageTokens{Implement: 1000},
		StageTimeout: StageTimeout{Review: 10 * time.Minute},
	}}
	// The layers are the merge's own three: the ceiling the run starts under
	// (the project layer's block with the defaults filled, T7.2), the default
	// branch's fugaro.yaml, and the branch the run checked out.
	e := Merge(ceiling, Layer{}, repo)
	g := e.Governance
	for _, c := range []struct {
		key  string
		got  int64
		want int64
	}{
		{KeyReviewRatio, g.ReviewRatio, 10},
		{KeyMaxSubagents, g.MaxSubagents, 3},
		{KeyReviewFloorCalls, g.ReviewFloorCalls, 4},
		{KeyStageTokensImplement, g.StageTokens.Implement, 1000},
		{KeyStageTokensReview, g.StageTokens.Review, 0},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.key, c.got, c.want)
		}
	}
	for _, c := range []struct {
		key  string
		got  float64
		want float64
	}{
		{KeyStageBudgetImplement, g.StageBudget.Implement, 0.9},
		{KeyStageBudgetReview, g.StageBudget.Review, 0.2},
		{KeyStageBudgetFix, g.StageBudget.Fix, 0.1},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.want)
		}
	}
	if g.StageTimeout.Review != 10*time.Minute {
		t.Errorf("%s = %v, want 10m", KeyStageTimeoutReview, g.StageTimeout.Review)
	}
	// A key no layer set is the default: the ceiling's own defaults survive
	// the layers above it.
	if g.StageBudget.Fix != 0.1 {
		t.Errorf("%s = %v", KeyStageBudgetFix, g.StageBudget.Fix)
	}

	// The two looser values are recorded, naming the layer that holds the
	// effective one, and the keys the repository did set are sourced to it.
	want := []Ignored{
		{Key: KeyReviewRatio, Value: "20", Effective: "10", Source: SourceCeiling},
		{Key: KeyStageBudgetImplement, Value: "0.95", Effective: "0.9", Source: SourceCeiling},
	}
	if !slices.Equal(e.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", e.Ignored, want)
	}
	for key, src := range map[string]string{
		KeyMaxSubagents:         SourceBranch,
		KeyReviewFloorCalls:     SourceBranch,
		KeyStageTokensImplement: SourceBranch,
		KeyStageTimeoutReview:   SourceBranch,
		KeyReviewRatio:          SourceCeiling,
	} {
		if e.Sources[key] != src {
			t.Errorf("Sources[%q] = %q, want %q", key, e.Sources[key], src)
		}
	}
	if e.Sources[KeyStageBudgetFix] != SourceBranch {
		t.Errorf("Sources[%q] = %q, want %q", KeyStageBudgetFix, e.Sources[KeyStageBudgetFix], SourceBranch)
	}
	// A key no layer writes is the ceiling's: WithDefaults put the default
	// there, and a run's sources say the ceiling rather than a layer that
	// never wrote the key.
	plain := Merge(Layer{Governance: Governance{}.WithDefaults()}, Layer{}, Layer{})
	if plain.Sources[KeyStageBudgetFix] != SourceCeiling {
		t.Errorf("Sources[%q] = %q, want %q", KeyStageBudgetFix, plain.Sources[KeyStageBudgetFix], SourceCeiling)
	}
	if plain.Ignored != nil {
		t.Errorf("Ignored = %+v, want none: the defaults are the ceiling, not a layer that lost", plain.Ignored)
	}
}

// TestGovernanceUnsetIsNoGovernance: a merge of layers that carry none is the
// zero Governance and no source at all. Nothing fills the defaults here —
// they are the project layer's ceiling (WithDefaults), not something Merge
// invents — so a caller that never builds one enforces nothing, which is the
// state this task leaves the runner in (T7.2 wires it).
func TestGovernanceUnsetIsNoGovernance(t *testing.T) {
	e := Merge(Layer{}, Layer{})
	if e.Governance != (Governance{}) {
		t.Errorf("Governance = %+v, want the zero value", e.Governance)
	}
	if len(e.Sources) != 0 {
		t.Errorf("Sources = %v, want none", e.Sources)
	}
}
