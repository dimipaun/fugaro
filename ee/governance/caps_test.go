// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance_test

// The caps are not governance (D520). What a run stops at is the deployed cap
// and the kill switch, both the core's, and neither is a key of the block that
// moved here.
//
// This file cannot compare the two editions: it blank-imports ee/register (see
// policy_test.go), so the shipped rule is installed for every merge below, and
// the off-switch that would take it out is internal/policy's test-only export,
// which nothing outside that package can call. The edition comparison is
// internal/policy's TestCapsAreUnmovedByTheGovernanceSeam. What is pinned here
// is the other half, with the real rule in place: that the rule this package
// ships writes governance keys and nothing else, so no cap can move because of
// it, and that the warning and the refusal are internal/budget's, which takes
// no governance and asks no hook at all.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/governance"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/policy"
)

// TestCapsUnchangedByTheTag: with this package's tighten rule installed, a
// layer that asks for looser governance over a tighter cap leaves the cap where
// it was, and nothing under the cap keys changes (D520, G6, G7).
//
// The merge below is resolved the way a tagged run resolves one, shipped
// defaults and all (policy.Governance.WithDefaults reads Defaults here), and
// its core half is asserted against what the non-governance rules say on their
// own — the shipped rule writes only the block's keys, so the two merges agree
// on every one of them.
func TestCapsUnchangedByTheTag(t *testing.T) {
	ceiling := policy.Layer{PerRunUSD: 2, PerDayUSD: 20, MaxRunTokens: 100000,
		Mode: policy.ModeEnforce, AllowedModels: []string{"sonnet", "opus"},
		MaxOutputCoder: 4000, MaxOutputReviewer: 8000,
		Governance: policy.Governance{}.WithDefaults()}
	looser := policy.Layer{PerRunUSD: 5, PerDayUSD: 30, MaxRunTokens: 200000,
		Mode: policy.ModeObserve, AllowedModels: []string{"sonnet", "opus", "haiku"},
		MaxOutputCoder: 8000, MaxOutputReviewer: 4000,
		Governance: policy.Governance{ReviewRatio: 99, MaxSubagents: 99,
			StageBudget:  policy.StageBudget{Implement: 0.99, Review: 0.99, Fix: 0.99},
			StageTimeout: policy.StageTimeout{Implement: 30 * time.Minute, Review: 30 * time.Minute}}}

	tagged := policy.Merge(ceiling, looser)
	for key, want := range map[string]float64{
		policy.KeyPerRunUSD: 2, policy.KeyPerDayUSD: 20, policy.KeyMaxRunTokens: 100000,
	} {
		got, ok := numericKey(tagged, key)
		if !ok || got != want {
			t.Errorf("%s = %v (%v), want the ceiling's %v: a governance value is not a cap", key, got, ok, want)
		}
	}
	if tagged.Mode != policy.ModeEnforce || len(tagged.AllowedModels) != 2 {
		t.Errorf("the governance block moved mode/allowed_models to %q/%v", tagged.Mode, tagged.AllowedModels)
	}
	// The comparison that makes the claim: the same two layers with the block
	// taken out of them resolve the core half identically, key for key, source
	// for source and ignored entry for ignored entry. The shipped rule is
	// installed for both merges, so what differs between them is the block
	// alone — and the block is the only thing that turns out to matter.
	bare := policy.Layer{PerRunUSD: 2, PerDayUSD: 20, MaxRunTokens: 100000,
		Mode: policy.ModeEnforce, AllowedModels: []string{"sonnet", "opus"},
		MaxOutputCoder: 4000, MaxOutputReviewer: 8000}
	bareLooser := policy.Layer{PerRunUSD: 5, PerDayUSD: 30, MaxRunTokens: 200000,
		Mode: policy.ModeObserve, AllowedModels: []string{"sonnet", "opus", "haiku"},
		MaxOutputCoder: 8000, MaxOutputReviewer: 4000}
	withoutBlock := policy.Merge(bare, bareLooser)
	if got, want := stripGovernance(tagged), stripGovernance(withoutBlock); !reflect.DeepEqual(got, want) {
		t.Errorf("the block moved a core key: with it %+v, without it %+v", got, want)
	}
	// The two arms have to differ, or the comparison is between a merge and
	// itself: the block is what the shipped rule resolved.
	if tagged.Governance == withoutBlock.Governance {
		t.Errorf("the block left the governance %+v, the same as no block at all", tagged.Governance)
	}

	// The 80% warning is one threshold for both editions (WarnShare, D520):
	// below it nothing is said, at it the note names the cap and says the cap
	// keeps applying.
	if got := budget.DailyWarnNote("the repository's", 79, 100); got != "" {
		t.Errorf("79%% of the cap warned: %q", got)
	}
	note := budget.DailyWarnNote("the repository's", 80, 100)
	if !strings.Contains(note, "(80.0%)") || !strings.Contains(note, "the cap keeps applying") {
		t.Errorf("the 80%% note = %q", note)
	}

	// The kill switch refuses identically whatever the governance resolved:
	// it is checked on the run's first lease, before any bound of the block is
	// looked at, and it names the switch's own scope. Evaluate takes no
	// governance and asks no hook, which is the whole of the claim.
	now := time.Now()
	write := budget.Write{Amount: 1, Op: budget.OpLease, Day: budget.Day(now)}
	d := budget.Evaluate(budget.State{}, write, budget.Caps{}, budget.Kills{Global: &budget.Kill{On: true}}, now)
	if d.Allow || d.Reason != budget.ReasonKillSwitch || d.Scope != budget.ScopeGlobal {
		t.Errorf("the kill switch answered %+v, want the global refusal (D520)", d)
	}
	if repo := budget.Evaluate(budget.State{}, write, budget.Caps{}, budget.Kills{Repo: &budget.Kill{On: true}}, now); repo.Reason != budget.ReasonKillSwitch || repo.Scope != budget.ScopeRepo {
		t.Errorf("the repository's kill switch answered %+v", repo)
	}

	// And a build that enforces governance still stops at the shipped figures
	// rather than at anything the block could raise: the guardrails this
	// package ships are positive and below one, so no stage budget is the cap.
	g := governance.Defaults()
	for _, v := range []float64{g.StageBudget.Implement, g.StageBudget.Review, g.StageBudget.Fix} {
		if !(v > 0 && v < 1) {
			t.Errorf("stage budget %v is not a fraction of the per-run cap (G6)", v)
		}
	}
}

// numericKey reads the cap keys the merge tightens on its own. They are three
// different Go types behind one key string, so the test spells the three out
// rather than reflecting over Effective.
func numericKey(e policy.Effective, key string) (float64, bool) {
	switch key {
	case policy.KeyPerRunUSD:
		return e.PerRunUSD, true
	case policy.KeyPerDayUSD:
		return e.PerDayUSD, true
	case policy.KeyMaxRunTokens:
		return float64(e.MaxRunTokens), true
	}
	return 0, false
}

// coreKey reports whether key is one Merge tightens with no governance and no
// hook in the way: the money caps, the mode, the token ceiling, the allow-list
// and the two output limits.
func coreKey(key string) bool {
	switch key {
	case policy.KeyPerRunUSD, policy.KeyPerDayUSD, policy.KeyMode, policy.KeyMaxRunTokens,
		policy.KeyAllowedModels, policy.KeyMaxOutputCoder, policy.KeyMaxOutputReviewer:
		return true
	}
	return false
}

// stripGovernance is e with the block taken out of it: the resolved governance,
// and every source and ignored entry that is not a core key. What is left is
// the half the tag cannot touch (D520).
func stripGovernance(e policy.Effective) policy.Effective {
	e.Governance = policy.Governance{}
	kept := []policy.Ignored{}
	for _, ig := range e.Ignored {
		if coreKey(ig.Key) {
			kept = append(kept, ig)
		}
	}
	e.Ignored = kept
	src := map[string]string{}
	for k, v := range e.Sources {
		if coreKey(k) {
			src[k] = v
		}
	}
	if len(src) == 0 {
		src = nil
	}
	e.Sources = src
	return e
}
