// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance_test

// The policy half's own tests: the guardrails Fugaro ships, the tighten-only
// rule a layer is held to, and the tier expectations (moved here with the
// policy, plan task E2.3). The package is external and blank-imports
// ee/register, because what these tests drive is the merge — policy.Merge and
// policy.Governance.WithDefaults, both of which reach the policy through the
// core's hook, so they see this policy only in a binary that registered it.
// An internal test could not: ee/register imports this package.

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/governance"
	_ "github.com/dimipaun/fugaro/ee/register"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/tier"
)

// TestGovernanceDefaults pins the guardrails Fugaro ships (plan T7.1): the
// review ratio N and its floor, the fan-out cap, and the three stage
// budgets. The stage budgets are fractions of the per-run cap — fix's of the
// implement stage's own budget, G§2's "a fraction of implementation budget" —
// and no one of them may reach the cap: G6 keeps the deployed per-run cap the
// ceiling, so a stage that could spend the whole of it alone would be no
// budget at all.
func TestGovernanceDefaults(t *testing.T) {
	g := governance.Defaults()
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
		{policy.KeyStageBudgetImplement, g.StageBudget.Implement},
		{policy.KeyStageBudgetReview, g.StageBudget.Review},
		{policy.KeyStageBudgetFix, g.StageBudget.Fix},
	} {
		if !(c.v > 0 && c.v < 1) {
			t.Errorf("%s default = %v: a stage budget is a fraction of the per-run cap, and no stage default may reach it (G6)", c.key, c.v)
		}
	}
	if g.StageTokens != (policy.StageTokens{}) {
		t.Errorf("stage_tokens default = %+v, want unset", g.StageTokens)
	}
	if g.StageTimeout != (policy.StageTimeout{}) {
		t.Errorf("stage_timeout default = %+v, want unset", g.StageTimeout)
	}
	// A layer that sets nothing is the defaults, whole: WithDefaults is how
	// the project layer's block becomes the ceiling a repository's own block
	// is held to, so an empty block has to mean the shipped guardrails rather
	// than no governance at all.
	if got := (policy.Governance{}).WithDefaults(); got != g {
		t.Errorf("an empty layer's governance = %+v, want the defaults %+v", got, g)
	}
	// A key the layer does set is its own, looser or not: the project layer is
	// where the guardrails are loosened (plan §7), so WithDefaults must not
	// tighten it back down to the default.
	loose := policy.Governance{ReviewRatio: 20, StageBudget: policy.StageBudget{Implement: 0.9}}.WithDefaults()
	if loose.ReviewRatio != 20 || loose.StageBudget.Implement != 0.9 {
		t.Errorf("the project layer's own values were not kept: %+v", loose)
	}
	if loose.MaxSubagents != policy.DefaultMaxSubagents {
		t.Errorf("a key the layer leaves out did not take the default: max_subagents = %d", loose.MaxSubagents)
	}
}

// TestGovernanceTightenOnly: the project layer sets the guardrails for every
// repository of the project, and every layer above it may only tighten them
// (plan T7.1). A looser value is dropped and recorded, a tighter one wins,
// and a key no layer set keeps the default — which is what makes the
// defaults a ceiling no run's own file can loosen.
func TestGovernanceTightenOnly(t *testing.T) {
	ceiling := policy.Layer{Governance: policy.Governance{
		ReviewRatio: 10, MaxSubagents: 8,
		StageBudget: policy.StageBudget{Implement: 0.9},
	}.WithDefaults()}
	repo := policy.Layer{Governance: policy.Governance{
		ReviewRatio: 20, MaxSubagents: 3, ReviewFloorCalls: 4,
		StageBudget:  policy.StageBudget{Implement: 0.95, Review: 0.2, Fix: 0.1},
		StageTokens:  policy.StageTokens{Implement: 1000},
		StageTimeout: policy.StageTimeout{Review: 10 * time.Minute},
	}}
	// The layers are the merge's own three: the ceiling the run starts under
	// (the project layer's block with the defaults filled, T7.2), the default
	// branch's fugaro.yaml, and the branch the run checked out.
	e := policy.Merge(ceiling, policy.Layer{}, repo)
	g := e.Governance
	for _, c := range []struct {
		key  string
		got  int64
		want int64
	}{
		{policy.KeyReviewRatio, g.ReviewRatio, 10},
		{policy.KeyMaxSubagents, g.MaxSubagents, 3},
		{policy.KeyReviewFloorCalls, g.ReviewFloorCalls, 4},
		{policy.KeyStageTokensImplement, g.StageTokens.Implement, 1000},
		{policy.KeyStageTokensReview, g.StageTokens.Review, 0},
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
		{policy.KeyStageBudgetImplement, g.StageBudget.Implement, 0.9},
		{policy.KeyStageBudgetReview, g.StageBudget.Review, 0.2},
		{policy.KeyStageBudgetFix, g.StageBudget.Fix, 0.1},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.want)
		}
	}
	if g.StageTimeout.Review != 10*time.Minute {
		t.Errorf("%s = %v, want 10m", policy.KeyStageTimeoutReview, g.StageTimeout.Review)
	}
	// A key no layer set is the default: the ceiling's own defaults survive
	// the layers above it.
	if g.StageBudget.Fix != 0.1 {
		t.Errorf("%s = %v", policy.KeyStageBudgetFix, g.StageBudget.Fix)
	}

	// The two looser values are recorded, naming the layer that holds the
	// effective one, and the keys the repository did set are sourced to it.
	want := []policy.Ignored{
		{Key: policy.KeyReviewRatio, Value: "20", Effective: "10", Source: policy.SourceCeiling},
		{Key: policy.KeyStageBudgetImplement, Value: "0.95", Effective: "0.9", Source: policy.SourceCeiling},
	}
	if !slices.Equal(e.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", e.Ignored, want)
	}
	for key, src := range map[string]string{
		policy.KeyMaxSubagents:         policy.SourceBranch,
		policy.KeyReviewFloorCalls:     policy.SourceBranch,
		policy.KeyStageTokensImplement: policy.SourceBranch,
		policy.KeyStageTimeoutReview:   policy.SourceBranch,
		policy.KeyReviewRatio:          policy.SourceCeiling,
	} {
		if e.Sources[key] != src {
			t.Errorf("Sources[%q] = %q, want %q", key, e.Sources[key], src)
		}
	}
	if e.Sources[policy.KeyStageBudgetFix] != policy.SourceBranch {
		t.Errorf("Sources[%q] = %q, want %q", policy.KeyStageBudgetFix, e.Sources[policy.KeyStageBudgetFix], policy.SourceBranch)
	}
	// A key no layer writes is the ceiling's: WithDefaults put the default
	// there, and a run's sources say the ceiling rather than a layer that
	// never wrote the key.
	plain := policy.Merge(policy.Layer{Governance: policy.Governance{}.WithDefaults()}, policy.Layer{}, policy.Layer{})
	if plain.Sources[policy.KeyStageBudgetFix] != policy.SourceCeiling {
		t.Errorf("Sources[%q] = %q, want %q", policy.KeyStageBudgetFix, plain.Sources[policy.KeyStageBudgetFix], policy.SourceCeiling)
	}
	if plain.Ignored != nil {
		t.Errorf("Ignored = %+v, want none: the defaults are the ceiling, not a layer that lost", plain.Ignored)
	}
}

// TestGovernanceUnsetIsNoGovernance: a merge of layers that carry none is the
// zero Governance and no source at all. Nothing fills the defaults here —
// they are the project layer's ceiling (WithDefaults), not something Merge
// invents — so a caller that never builds one enforces nothing. The runner is
// no longer such a caller: resolvePolicy builds its ceiling with them, which
// is also what makes a repository's looser value one the merge drops. What
// still reads nothing is the enforcement, which T7.2 wires.
func TestGovernanceUnsetIsNoGovernance(t *testing.T) {
	e := policy.Merge(policy.Layer{}, policy.Layer{})
	if e.Governance != (policy.Governance{}) {
		t.Errorf("Governance = %+v, want the zero value", e.Governance)
	}
	if len(e.Sources) != 0 {
		t.Errorf("Sources = %v, want none", e.Sources)
	}
}

// TestTierExpectationsAreTheVocabulary walks policy.TierExpectations by
// reflection: every field is one tier of tier.Tiers, and every tier of the
// vocabulary has a field. The tiers are fields rather than map entries so an
// unknown tier name is an unknown key the strict decoder refuses, but that
// leaves nothing at compile time to notice a fifth tier: a tier added to
// internal/tier and not to this struct would silently set no expectation, and
// WithDefaults and Tighten would go on naming the four by hand. The field name
// is the tier's name in Go's spelling (Trivial is tier.Trivial), which is the
// mapping WithDefaults and For read too. internal/config holds the file's own
// block equal to the vocabulary the same way (TestGovernanceTiersAreTheVocabulary).
func TestTierExpectationsAreTheVocabulary(t *testing.T) {
	typ := reflect.TypeOf(policy.TierExpectations{})
	if typ.NumField() != len(tier.Tiers) {
		t.Errorf("TierExpectations has %d fields, want one per tier (%d)", typ.NumField(), len(tier.Tiers))
	}
	seen := make(map[tier.Tier]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := tier.Tier(strings.ToLower(f.Name))
		if !tier.Known(name) {
			t.Errorf("TierExpectations.%s is no tier of tier.Tiers (%s)", f.Name, tier.List())
			continue
		}
		seen[name] = true
		if f.Type != reflect.TypeOf(policy.TierExpectation{}) {
			t.Errorf("TierExpectations.%s is %s, want a TierExpectation", f.Name, f.Type)
		}
	}
	for _, tt := range tier.Tiers {
		if !seen[tt] {
			t.Errorf("tier %q has no field in TierExpectations, so nothing sets its expectation", tt)
		}
	}
	// The defaults the vocabulary is walked with are the four tiers whole: a
	// tier that had no default would be a field with a zero expectation, which
	// is also what an untiered run's ceiling must not silently become.
	if got := governance.TierExpectations(); got.For(tier.Trivial) == (policy.TierExpectation{}) {
		t.Errorf("TierExpectations().Trivial = %+v, want the shipped figures", got.For(tier.Trivial))
	}
}

// TestTierDefaultsFromProjectLayer pins the expectations Fugaro ships (G§3,
// D332) and the two rules that make them a ceiling a run starts under: a layer
// that writes one key of one tier keeps the default for the rest of that tier,
// and the project layer's own figures survive the layers above it.
func TestTierDefaultsFromProjectLayer(t *testing.T) {
	d := governance.Defaults()
	if d.Tiers != governance.TierExpectations() {
		t.Errorf("Defaults().Tiers = %+v, want TierExpectations() %+v", d.Tiers, governance.TierExpectations())
	}
	// The shipped figures, walked over the vocabulary rather than spelled out
	// four times: For is the one lookup from a tier name to the field that
	// holds its expectation, so a tier added to internal/tier whose case For
	// does not name reads as the zero expectation — the one value a shipped
	// figure never is — and this fails.
	shipped := map[tier.Tier]policy.TierExpectation{
		tier.Trivial: {ExpectedUSD: 0.25, ExpectedMinutes: 10, MaxSubagents: 3},
		tier.Small:   {ExpectedUSD: 1, ExpectedMinutes: 30, MaxSubagents: 3},
		tier.Medium:  {ExpectedUSD: 3, ExpectedMinutes: 90, MaxSubagents: policy.DefaultMaxSubagents},
		tier.Large:   {ExpectedUSD: 8, ExpectedMinutes: 240, MaxSubagents: policy.DefaultMaxSubagents},
	}
	if len(shipped) != len(tier.Tiers) {
		t.Fatalf("the shipped figures name %d tiers, the vocabulary has %d", len(shipped), len(tier.Tiers))
	}
	for _, tt := range tier.Tiers {
		want, ok := shipped[tt]
		if !ok {
			t.Fatalf("tier %q has no shipped figure here, so nothing pins its default", tt)
		}
		if got := d.Tiers.For(tt); got != want {
			t.Errorf("%s = %+v, want %+v", tt, got, want)
		}
	}

	// The project layer's block is the ceiling, so WithDefaults fills only the
	// keys it left out: one key of one tier, and every other tier whole.
	layer := policy.Governance{Tiers: policy.TierExpectations{Medium: policy.TierExpectation{ExpectedUSD: 2}}}.WithDefaults()
	want := policy.TierExpectation{ExpectedUSD: 2, ExpectedMinutes: 90, MaxSubagents: policy.DefaultMaxSubagents}
	if got := layer.Tiers.Medium; got != want {
		t.Errorf("medium = %+v, want the layer's own cost and the defaults for its other keys (%+v)", got, want)
	}
	for _, c := range []struct {
		tier tier.Tier
		want policy.TierExpectation
	}{
		{tier.Trivial, d.Tiers.Trivial},
		{tier.Small, d.Tiers.Small},
		{tier.Large, d.Tiers.Large},
	} {
		if got := layer.Tiers.For(c.tier); got != c.want {
			t.Errorf("%s = %+v, want the tier the layer never mentioned %+v", c.tier, got, c.want)
		}
	}
	if (policy.Governance{}).WithDefaults().Tiers != d.Tiers {
		t.Errorf("an empty layer's tiers = %+v, want the defaults %+v", (policy.Governance{}).WithDefaults().Tiers, d.Tiers)
	}
}

// TestTierMaxSubagents: the fan-out of a tier (spec §5's tripwire, D332). A
// small change has no business fanning out the way an untiered run may, so
// trivial and small stop at 3 while medium and large keep the untiered default
// of 5. A tier's figure is the tier's alone: it never moves the untiered
// governance.max_subagents, and a repository may only tighten it.
func TestTierMaxSubagents(t *testing.T) {
	d := governance.Defaults()
	for _, c := range []struct {
		tier tier.Tier
		want int64
	}{
		{tier.Trivial, 3},
		{tier.Small, 3},
		{tier.Medium, policy.DefaultMaxSubagents},
		{tier.Large, policy.DefaultMaxSubagents},
	} {
		if got := d.Tiers.For(c.tier).MaxSubagents; got != c.want {
			t.Errorf("%s max_subagents = %d, want %d", c.tier, got, c.want)
		}
	}

	tiers := governance.TierExpectations()
	tiers.Trivial.MaxSubagents = 3
	ceiling := policy.Layer{Governance: policy.Governance{MaxSubagents: 8, Tiers: tiers}.WithDefaults()}
	repo := policy.Layer{Governance: policy.Governance{Tiers: policy.TierExpectations{
		Trivial: policy.TierExpectation{MaxSubagents: 10},
		Small:   policy.TierExpectation{MaxSubagents: 2},
	}}}
	e := policy.Merge(ceiling, policy.Layer{}, repo)

	if got := e.Governance.Tiers.Trivial.MaxSubagents; got != 3 {
		t.Errorf("tiers.trivial.max_subagents = %d, want 3: a repository may not fan a small change out wider than the project allows", got)
	}
	if got := e.Governance.Tiers.Small.MaxSubagents; got != 2 {
		t.Errorf("tiers.small.max_subagents = %d, want 2: a repository's tighter figure wins", got)
	}
	want := []policy.Ignored{{Key: policy.TierKey(tier.Trivial, policy.KeyMaxSubagents), Value: "10", Effective: "3", Source: policy.SourceCeiling}}
	if !slices.Equal(e.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", e.Ignored, want)
	}
	for key, src := range map[string]string{
		policy.TierKey(tier.Trivial, policy.KeyMaxSubagents): policy.SourceCeiling,
		policy.TierKey(tier.Small, policy.KeyMaxSubagents):   policy.SourceBranch,
	} {
		if e.Sources[key] != src {
			t.Errorf("Sources[%q] = %q, want %q", key, e.Sources[key], src)
		}
	}
	// The project layer's untiered fan-out is untouched by any tier: they are
	// different keys, and one never becomes the other.
	if e.Governance.MaxSubagents != 8 {
		t.Errorf("max_subagents = %d, want the project layer's 8: a tier's figure is the tier's alone", e.Governance.MaxSubagents)
	}
}

// TestTierNeverRaisesCap is G6 on the policy side: a tier is an expectation,
// never a cap. The deployed per-run cap stays the ceiling whatever a tier
// expects, so an expected cost above it is an expectation the run still stops
// at the cap for, and no key of governance.tiers moves per_run_usd.
func TestTierNeverRaisesCap(t *testing.T) {
	tiers := governance.TierExpectations()
	tiers.Large.ExpectedUSD = 8
	ceiling := policy.Layer{PerRunUSD: 2, Governance: policy.Governance{Tiers: tiers}}
	e := policy.Merge(ceiling, policy.Layer{}, policy.Layer{})

	if e.PerRunUSD != 2 {
		t.Errorf("per_run_usd = %v, want the deployed cap 2: a tier's expectation is not a cap (G6)", e.PerRunUSD)
	}
	if got := e.Governance.Tiers.Large.ExpectedUSD; got != 8 {
		t.Errorf("tiers.large.expected_usd = %v, want the expectation 8: above the cap it is still an expectation", got)
	}
	if e.Sources[policy.KeyPerRunUSD] != policy.SourceCeiling {
		t.Errorf("Sources[%q] = %q, want %q", policy.KeyPerRunUSD, e.Sources[policy.KeyPerRunUSD], policy.SourceCeiling)
	}

	// A repository may only tighten an expectation, the same as any other key:
	// the ceiling's smaller figure wins and the looser one is recorded. A
	// tiers-only layer moves no cap at all.
	only := policy.Merge(ceiling, policy.Layer{}, policy.Layer{Governance: policy.Governance{Tiers: policy.TierExpectations{
		Large: policy.TierExpectation{ExpectedUSD: 50},
	}}})
	if only.PerRunUSD != 2 {
		t.Errorf("a tiers-only layer moved per_run_usd to %v: a tier never raises a cap (G6)", only.PerRunUSD)
	}
	if got := only.Governance.Tiers.Large.ExpectedUSD; got != 8 {
		t.Errorf("tiers.large.expected_usd = %v, want the ceiling's 8", got)
	}
	want := []policy.Ignored{{Key: policy.TierKey(tier.Large, policy.KeyExpectedUSD), Value: "50", Effective: "8", Source: policy.SourceCeiling}}
	if !slices.Equal(only.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", only.Ignored, want)
	}
}

// TestTheGovernanceWarningsSourceIsTheMergesRecord: the two merges the CLI
// builds before it warns — a project layer's block as the ceiling with the
// file's looser value under it, and no layer at all with the shipped figures
// as the ceiling — each record the dropped value, which is what
// `fugaro validate` and `setup --repo` render their warning from
// (internal/cli's clampWarning). The rendering is the core's and unchanged;
// what a tagged build has to hand it is this record, and an untagged build has
// none to hand (D521, D522).
//
// The two cases are the ones the core's validate tests used to run end to end
// (TestValidateDoesNotClampTheProjectLayersGovernance and
// TestValidateAcceptsALooserGovernanceValueWithNoLayer): the command's own
// arm is the core's, and the merge it reads is here.
func TestTheGovernanceWarningsSourceIsTheMergesRecord(t *testing.T) {
	layerCeiling := policy.Layer{Governance: policy.Governance{
		StageTimeout: policy.StageTimeout{Implement: 200 * time.Millisecond}}.WithDefaults()}
	e := policy.Merge(layerCeiling, policy.Layer{},
		policy.Layer{Governance: policy.Governance{StageTimeout: policy.StageTimeout{Implement: 5 * time.Minute}}})
	if e.Governance.StageTimeout.Implement != 200*time.Millisecond {
		t.Errorf("the layer's stage_timeout did not hold: %v", e.Governance.StageTimeout.Implement)
	}
	want := []policy.Ignored{{
		Key: policy.KeyStageTimeoutImplement, Value: "5m0s", Effective: "200ms", Source: policy.SourceCeiling}}
	if !slices.Equal(e.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", e.Ignored, want)
	}

	// With no layer the shipped figures are the ceiling, so a repository that
	// raises a guardrail is the looser side.
	shipped := policy.Merge(
		policy.Layer{Governance: policy.Governance{}.WithDefaults()},
		policy.Layer{},
		policy.Layer{Governance: policy.Governance{
			ReviewRatio: 20,
			Tiers:       policy.TierExpectations{Small: policy.TierExpectation{ExpectedUSD: 2}},
		}})
	want = []policy.Ignored{
		{Key: policy.KeyReviewRatio, Value: "20", Effective: "5", Source: policy.SourceCeiling},
		{Key: policy.TierKey(tier.Small, policy.KeyExpectedUSD), Value: "2", Effective: "1", Source: policy.SourceCeiling},
	}
	if !slices.Equal(shipped.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", shipped.Ignored, want)
	}
}
