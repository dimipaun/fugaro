package runner_test

import (
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// TestGovernanceLooserInTheRepositoryIsIgnored: with no project layer the
// ceiling carries Fugaro's own guardrails (Governance.WithDefaults fills them
// in, T7.1), so the repository's fugaro.yaml — the branch the run checks out,
// which the agent can edit mid-run — may only tighten them. Every governance
// key that has a shipped figure is held to it: a looser value is dropped,
// recorded in policy.ignored with the figure the run is actually held to and
// the layer that holds it, and warned about, exactly as a looser budget key is
// (T7.1, D331).
//
// Before the defaults were in the ceiling this was the inversion the review
// found: the repository's own block was the first value written for the key,
// so it won with it, silently and with nothing recorded. Nothing reads these
// figures yet (T7.2 will), which is why this is the moment to pin it.
//
// The keys are listed one by one rather than read off the ceiling: a key the
// list forgets is a key nothing proves is held. The table is checked against
// policy.DefaultGovernanceKeys below, so a governance key added without a case
// here fails rather than passing quietly.
func TestGovernanceLooserInTheRepositoryIsIgnored(t *testing.T) {
	cases := []struct {
		name      string
		block     string // appended to the fixture's fugaro.yaml
		key       string
		value     string
		effective string
	}{
		{"review_ratio", "governance:\n  review_ratio: 9\n", policy.KeyReviewRatio, "9", "5"},
		{"review_floor_calls", "governance:\n  review_floor_calls: 40\n", policy.KeyReviewFloorCalls, "40", "20"},
		{"max_subagents", "governance:\n  max_subagents: 9\n", policy.KeyMaxSubagents, "9", "5"},
		{"stage_budget.implement", "governance:\n  stage_budget: { implement: 0.9 }\n", policy.KeyStageBudgetImplement, "0.9", "0.7"},
		{"stage_budget.review", "governance:\n  stage_budget: { review: 0.9 }\n", policy.KeyStageBudgetReview, "0.9", "0.5"},
		{"stage_budget.fix", "governance:\n  stage_budget: { fix: 0.9 }\n", policy.KeyStageBudgetFix, "0.9", "0.5"},
		{"tiers.trivial.expected_usd", "governance:\n  tiers: { trivial: { expected_usd: 0.5 } }\n", policy.TierKey(tier.Trivial, policy.KeyExpectedUSD), "0.5", "0.25"},
		{"tiers.trivial.expected_minutes", "governance:\n  tiers: { trivial: { expected_minutes: 20 } }\n", policy.TierKey(tier.Trivial, policy.KeyExpectedMinutes), "20", "10"},
		{"tiers.trivial.max_subagents", "governance:\n  tiers: { trivial: { max_subagents: 9 } }\n", policy.TierKey(tier.Trivial, policy.KeyMaxSubagents), "9", "3"},
		{"tiers.small.expected_usd", "governance:\n  tiers: { small: { expected_usd: 2 } }\n", policy.TierKey(tier.Small, policy.KeyExpectedUSD), "2", "1"},
		{"tiers.small.expected_minutes", "governance:\n  tiers: { small: { expected_minutes: 60 } }\n", policy.TierKey(tier.Small, policy.KeyExpectedMinutes), "60", "30"},
		{"tiers.small.max_subagents", "governance:\n  tiers: { small: { max_subagents: 9 } }\n", policy.TierKey(tier.Small, policy.KeyMaxSubagents), "9", "3"},
		{"tiers.medium.expected_usd", "governance:\n  tiers: { medium: { expected_usd: 4 } }\n", policy.TierKey(tier.Medium, policy.KeyExpectedUSD), "4", "3"},
		{"tiers.medium.expected_minutes", "governance:\n  tiers: { medium: { expected_minutes: 120 } }\n", policy.TierKey(tier.Medium, policy.KeyExpectedMinutes), "120", "90"},
		{"tiers.medium.max_subagents", "governance:\n  tiers: { medium: { max_subagents: 9 } }\n", policy.TierKey(tier.Medium, policy.KeyMaxSubagents), "9", "5"},
		{"tiers.large.expected_usd", "governance:\n  tiers: { large: { expected_usd: 9 } }\n", policy.TierKey(tier.Large, policy.KeyExpectedUSD), "9", "8"},
		{"tiers.large.expected_minutes", "governance:\n  tiers: { large: { expected_minutes: 300 } }\n", policy.TierKey(tier.Large, policy.KeyExpectedMinutes), "300", "240"},
		{"tiers.large.max_subagents", "governance:\n  tiers: { large: { max_subagents: 9 } }\n", policy.TierKey(tier.Large, policy.KeyMaxSubagents), "9", "5"},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.key] = true
	}
	for key := range policy.DefaultGovernance().DefaultGovernanceKeys() {
		if !covered[key] {
			t.Errorf("the shipped default for %q is not covered here, so nothing proves a repository cannot loosen it", key)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, fixtureYAML(t)+c.block, nil)
			var logs lockedBuf
			h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			pol := rec.Policy
			if pol == nil {
				t.Fatalf("the record of a run that dropped %s carries no policy", c.key)
			}
			var found bool
			for _, ig := range pol.Ignored {
				if ig.Key != c.key {
					continue
				}
				found = true
				if ig.Value != c.value || ig.Effective != c.effective || ig.Source != policy.SourceCeiling || ig.From != policy.SourceBranch {
					t.Errorf("ignored = %+v, want the repository's %s dropped for the ceiling's %s", ig, c.value, c.effective)
				}
			}
			if !found {
				t.Fatalf("the repository's looser %s was dropped without being recorded: %+v", c.key, pol.Ignored)
			}
			got := logs.String()
			if !strings.Contains(got, "a looser value in fugaro.yaml was ignored") || !strings.Contains(got, "governance."+c.key) {
				t.Errorf("the run log does not warn about the dropped %s:\n%s", c.key, got)
			}
		})
	}
}

// TestGovernanceStageTimeoutWithNoProjectLayerIsTheRepositorys: stage_timeout
// is the one governance key with no shipped figure (Governance.WithDefaults
// leaves it zero: a stage with no timeout of its own is bounded by the run's
// own timeouts.stage), so with no project layer there is nothing for a
// repository's value to be looser than — it is the only value there is, and it
// stands. The ceiling carrying the defaults must not invent one for it: a
// ceiling that did would silently drop every repository's own stage_timeout
// and, since a stage timeout only ever shortens timeouts.stage (T7.6), would
// leave the run with a bound nobody set.
func TestGovernanceStageTimeoutWithNoProjectLayerIsTheRepositorys(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Detail != "time" {
		t.Fatalf("record = %+v, halt = %+v: the repository's own stage_timeout did not bound the stage", rec, rec.Halt)
	}
	if pol := rec.Policy; pol != nil {
		for _, ig := range pol.Ignored {
			if ig.Key == policy.KeyStageTimeoutImplement {
				t.Errorf("the repository's stage_timeout was dropped for a ceiling figure that does not exist: %+v", ig)
			}
		}
	}
}

// TestGovernanceLooserInTheProjectLayerHolds: the project layer is the one
// layer that may loosen Fugaro's defaults (plan §7, D331), and it does so by
// writing the looser figure itself: WithDefaults keeps a key the layer set
// exactly as it stands, so the layer's 20 is the ceiling every repository of
// the project is held to, sourced to the ceiling and with nothing ignored.
// A tier's own figure works the same way, which is what makes a project able
// to say its repositories' small runs are expected to cost $5 where Fugaro
// ships $1.
func TestGovernanceLooserInTheProjectLayerHolds(t *testing.T) {
	layer := runnerLayer + "governance: { review_ratio: 20, tiers: { small: { expected_usd: 5 } } }\n"
	spec := layerSpec(layer)
	spec.Tier = tier.Small
	h := newHarness(t, layeredRepo, spec)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	pol := rec.Policy
	if pol == nil || pol.Sources[policy.KeyReviewRatio] != policy.SourceCeiling {
		t.Fatalf("policy = %+v, want the project layer's review_ratio as the ceiling's", pol)
	}
	for _, ig := range pol.Ignored {
		if ig.Key == policy.KeyReviewRatio || ig.Key == policy.TierKey(tier.Small, policy.KeyExpectedUSD) {
			t.Errorf("the project layer's own looser value was dropped: %+v", ig)
		}
	}
	if rec.Tier == nil || rec.Tier.Tier != tier.Small || rec.Tier.ExpectedUSD != 5 {
		t.Fatalf("tier = %+v, want the project layer's own $5 for small", rec.Tier)
	}
}

// TestGovernanceAtTheShippedFigureIsNotAPolicy: a repository that writes the
// figure Fugaro already ships changes nothing, and its record says so: no
// ignored entry (the value is not looser than the ceiling's) and no source for
// the key, because a key still at the shipped figure is not a decision any
// layer made (H1). Writing it is not an error — a repository is free to spell
// out the guardrail it runs under — but it must not read as a policy of its
// own.
func TestGovernanceAtTheShippedFigureIsNotAPolicy(t *testing.T) {
	d := policy.DefaultGovernance()
	block := "governance:\n  review_ratio: " + strconv.FormatInt(d.ReviewRatio, 10) +
		"\n  stage_budget: { implement: " + strconv.FormatFloat(d.StageBudget.Implement, 'f', -1, 64) + " }\n"
	h := newHarness(t, fixtureYAML(t)+block, nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if pol := rec.Policy; pol != nil {
		if _, ok := pol.Sources[policy.KeyReviewRatio]; ok {
			t.Errorf("the record sources review_ratio to %q, want no source: the value is Fugaro's own", pol.Sources[policy.KeyReviewRatio])
		}
		for _, ig := range pol.Ignored {
			t.Errorf("a value equal to the shipped figure was recorded as ignored: %+v", ig)
		}
	}
}
