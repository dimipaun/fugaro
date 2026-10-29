package policy_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/policy"
)

// coreKeys are the keys Merge tightens on its own, with no governance and no
// hook in the way: the money caps, the mode, the token ceiling, the
// allow-list and the two output limits. They are the whole of what D520 keeps
// in the core, and what this file holds apart from the block that moved.
var coreKeys = []string{
	policy.KeyPerRunUSD, policy.KeyPerDayUSD, policy.KeyMode,
	policy.KeyMaxRunTokens, policy.KeyAllowedModels,
	policy.KeyMaxOutputCoder, policy.KeyMaxOutputReviewer,
}

// TestCapsAreUnmovedByTheGovernanceSeam: the caps answer the same with the
// policy hook installed as with it back at the core default (D520, G7).
//
// This is the one place the two arms can be compared, so the comparison lives
// here rather than in ee/governance: the off-switch is this package's
// export_test.go, and a tagged binary's hook is ee/governance's Tighten, which
// the core cannot import. What is installed below is therefore a stand-in of
// the same shape — a Defaults that ships figures and a Tighten that writes
// governance keys and nothing else — and the claim it pins is the shipped
// shape's: a rule that touches only the block's keys cannot move a cap, and
// what the merge made of the block never reaches internal/budget.
//
// The untagged build's arm is not a weaker merge of the same layers: with the
// core default's no-op Tighten, Merge writes no governance key at all, not even
// the ceiling's, because the hook is the only thing that ever puts one into an
// Effective (merge.go). So the two arms differ in the governance half by
// construction, and the test asserts they differ there — otherwise the
// comparison would prove nothing — while every core key, its source and its
// ignored entries are identical.
func TestCapsAreUnmovedByTheGovernanceSeam(t *testing.T) {
	ceiling := policy.Layer{
		PerRunUSD: 2, PerDayUSD: 20, MaxRunTokens: 100000, Mode: policy.ModeEnforce,
		AllowedModels: []string{"sonnet", "opus"}, MaxOutputCoder: 4000, MaxOutputReviewer: 8000,
		Governance: policy.Governance{ReviewRatio: 5, MaxSubagents: 5,
			StageTimeout: policy.StageTimeout{Review: 2 * time.Minute}},
	}
	// A layer that asks for more of everything: the caps above it, looser
	// governance below it, and the mode it cannot raise.
	looser := policy.Layer{
		PerRunUSD: 5, PerDayUSD: 30, MaxRunTokens: 200000, Mode: policy.ModeObserve,
		AllowedModels: []string{"sonnet", "opus", "haiku"}, MaxOutputCoder: 8000, MaxOutputReviewer: 4000,
		Governance: policy.Governance{ReviewRatio: 20, MaxSubagents: 9,
			StageTimeout: policy.StageTimeout{Review: 30 * time.Minute}},
	}
	merge := func() policy.Effective { return policy.Merge(ceiling, looser) }

	policy.ResetHooks(t)
	untagged := merge()

	policy.SetHooks(t, governanceShaped())
	tagged := merge()

	stripGovernance := func(e policy.Effective) policy.Effective {
		e.Governance = policy.Governance{}
		kept := []policy.Ignored{}
		for _, ig := range e.Ignored {
			if !governanceKey(ig.Key) {
				kept = append(kept, ig)
			}
		}
		e.Ignored = kept
		src := map[string]string{}
		for k, v := range e.Sources {
			if !governanceKey(k) {
				src[k] = v
			}
		}
		if len(src) == 0 {
			src = nil
		}
		e.Sources = src
		return e
	}
	if got, want := stripGovernance(tagged), stripGovernance(untagged); !reflect.DeepEqual(got, want) {
		t.Errorf("the hook moved a core key: with it installed %+v, without it %+v", got, want)
	}

	// The arms have to differ, or the comparison above is between one merge
	// and itself: the installed rule resolves the block and drops the looser
	// half of it, and the core default resolves none of it.
	if tagged.Governance == untagged.Governance {
		t.Fatalf("the installed hook resolved %+v, the core default %+v: the two arms are the same merge", tagged.Governance, untagged.Governance)
	}
	if len(tagged.Ignored) == 0 {
		t.Fatalf("the installed hook dropped nothing from %+v: there is no governance half to hold apart", tagged.Governance)
	}
	if untagged.Governance.ReviewRatio != 0 || untagged.Governance.MaxSubagents != 0 {
		t.Errorf("the core default resolved %+v, want the zero governance an untagged build holds no run to", untagged.Governance)
	}

	// And no cap is a governance key in either edition: DefaultGovernanceKeys
	// names the block's own keys, and a cap among them would be a value the
	// tag could move (G6).
	for _, k := range coreKeys {
		if governanceKey(k) {
			t.Errorf("%q is a policy key the merge tightens on its own, but it is named among the governance keys", k)
		}
		if tagged.Governance.DefaultGovernanceKeys()[k] || untagged.Governance.DefaultGovernanceKeys()[k] {
			t.Errorf("DefaultGovernanceKeys names the core key %q", k)
		}
	}
}

// governanceShaped is a stand-in for the pair a tagged build installs: a
// Defaults that ships a figure for the guardrails, and a Tighten that writes
// governance keys through the merge's own rules and never touches a cap. The
// real pair is ee/governance's, which this package cannot import.
func governanceShaped() policy.GovernanceFuncs {
	shipped := policy.Governance{ReviewRatio: 5, ReviewFloorCalls: 20, MaxSubagents: 3,
		StageBudget:  policy.StageBudget{Implement: 0.7, Review: 0.5, Fix: 0.5},
		StageTimeout: policy.StageTimeout{Implement: 5 * time.Minute, Review: 2 * time.Minute},
		StageTokens:  policy.StageTokens{Implement: 100000, Review: 50000},
	}
	return policy.GovernanceFuncs{
		Defaults: func() policy.Governance { return shipped },
		Tighten: func(e *policy.Effective, g policy.Governance, src string) {
			if g.ReviewRatio > 0 {
				e.TightenInt(&e.Governance.ReviewRatio, g.ReviewRatio, policy.KeyReviewRatio, src)
			}
			if g.MaxSubagents > 0 {
				e.TightenInt(&e.Governance.MaxSubagents, g.MaxSubagents, policy.KeyMaxSubagents, src)
			}
			if g.StageTimeout.Review > 0 {
				e.TightenDuration(&e.Governance.StageTimeout.Review, g.StageTimeout.Review, policy.KeyStageTimeoutReview, src)
			}
		},
	}
}

// governanceKey reports whether key is one of the block's, spelled the way
// Effective.Sources and Ignored.Key spell them: the shipped rule writes
// nothing outside this set. The list is the mirror of the constants, so a key
// added to the vocabulary without being added here fails the test rather than
// being silently treated as a cap.
func governanceKey(key string) bool {
	switch key {
	case policy.KeyReviewRatio, policy.KeyReviewFloorCalls, policy.KeyMaxSubagents,
		policy.KeyStageBudgetImplement, policy.KeyStageBudgetReview, policy.KeyStageBudgetFix,
		policy.KeyStageTokensImplement, policy.KeyStageTokensReview, policy.KeyStageTokensFix,
		policy.KeyStageTimeoutImplement, policy.KeyStageTimeoutReview, policy.KeyStageTimeoutFix:
		return true
	}
	return strings.HasPrefix(key, "tiers.")
}
