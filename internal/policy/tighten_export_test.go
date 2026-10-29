package policy_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestExportedTightenRulesAreTheMerges: the three exported wrappers are the
// merge's own tighten-only rule and not a second copy of it — the governance
// policy moves to ee/governance, whose moved tightenGovernance and tightenTier
// cannot reach the unexported methods, and a copy would be a second place the
// rule lives, to drift from this one the first time a key's shape changed
// (design ee-boundary-moves.md §5.2).
//
// What each wrapper answers is what Merge answers, driven through the public
// API only: the state a layer's value is applied to is built by Merge, the
// wrapper applies the same value to it, and the whole effective policy is
// compared with the Merge that did both itself — the value, the source it is
// attributed to, and the entry a looser value leaves in Ignored.
//
// The two keys Merge still tightens on its own are the comparison's: the
// per-day cap for TightenCap and the coder's output limit for TightenInt. The
// only duration key is a governance one, and Merge reaches those through the
// policy hook, which is exactly the half of the seam the move flips (E2.3b):
// comparing a duration against Merge would pin the core default's behaviour
// rather than the rule's, so the three outcomes the two comparisons prove the
// shape of are asserted for it directly.
func TestExportedTightenRulesAreTheMerges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start policy.Layer
		layer policy.Layer
		apply func(e *policy.Effective)
	}{
		{"a tighter per-day cap", policy.Layer{PerDayUSD: 5}, policy.Layer{PerDayUSD: 3},
			func(e *policy.Effective) {
				e.TightenCap(&e.PerDayUSD, 3, policy.KeyPerDayUSD, policy.SourceDefaultBranch)
			}},
		{"a looser per-day cap", policy.Layer{PerDayUSD: 5}, policy.Layer{PerDayUSD: 9},
			func(e *policy.Effective) {
				e.TightenCap(&e.PerDayUSD, 9, policy.KeyPerDayUSD, policy.SourceDefaultBranch)
			}},
		{"a cap the layer left unset", policy.Layer{PerDayUSD: 5}, policy.Layer{},
			func(e *policy.Effective) {
				e.TightenCap(&e.PerDayUSD, 0, policy.KeyPerDayUSD, policy.SourceDefaultBranch)
			}},
		{"a tighter coder output limit", policy.Layer{MaxOutputCoder: 4000}, policy.Layer{MaxOutputCoder: 2000},
			func(e *policy.Effective) {
				e.TightenInt(&e.MaxOutputCoder, 2000, policy.KeyMaxOutputCoder, policy.SourceDefaultBranch)
			}},
		{"a looser coder output limit", policy.Layer{MaxOutputCoder: 4000}, policy.Layer{MaxOutputCoder: 8000},
			func(e *policy.Effective) {
				e.TightenInt(&e.MaxOutputCoder, 8000, policy.KeyMaxOutputCoder, policy.SourceDefaultBranch)
			}},
		{"an int the layer left unset", policy.Layer{MaxOutputCoder: 4000}, policy.Layer{},
			func(e *policy.Effective) {
				e.TightenInt(&e.MaxOutputCoder, 0, policy.KeyMaxOutputCoder, policy.SourceDefaultBranch)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := policy.Merge(tc.start, tc.layer)
			got := policy.Merge(tc.start)
			tc.apply(&got)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the wrapper left %+v, want the merge's own %+v", got, want)
			}
			// Two zero values compare equal, so a case that never reached a
			// value has to say so: what is compared is a policy the merge
			// set something on.
			if len(want.Sources)+len(want.Ignored) == 0 {
				t.Fatal("the case sets nothing, so the comparison proves nothing")
			}
		})
	}

	// A value that is unset in any of the ways a number can be unset leaves
	// the running value, its source and the ignored list exactly as they were.
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		e := policy.Merge(policy.Layer{PerDayUSD: 5})
		e.TightenCap(&e.PerDayUSD, v, policy.KeyPerDayUSD, policy.SourceBranch)
		if e.PerDayUSD != 5 || e.Sources[policy.KeyPerDayUSD] != policy.SourceCeiling || len(e.Ignored) != 0 {
			t.Errorf("TightenCap(%v) left %+v, want the running value, source and ignored list untouched", v, e)
		}
	}
	for _, v := range []int64{0, -1} {
		e := policy.Merge(policy.Layer{MaxOutputCoder: 4000})
		e.TightenInt(&e.MaxOutputCoder, v, policy.KeyMaxOutputCoder, policy.SourceBranch)
		if e.MaxOutputCoder != 4000 || e.Sources[policy.KeyMaxOutputCoder] != policy.SourceCeiling || len(e.Ignored) != 0 {
			t.Errorf("TightenInt(%d) left %+v, want the running value, source and ignored list untouched", v, e)
		}
	}

	// A stage timeout, the one duration key there is: tighter wins with the
	// layer's name, looser is dropped and recorded with the durations spelled
	// as Merge spells them, and a duration that is not positive is unset.
	at := func(v time.Duration) policy.Effective {
		e := policy.Effective{Layer: policy.Layer{Governance: policy.Governance{
			StageTimeout: policy.StageTimeout{Review: 2 * time.Minute}}}}
		e.Sources = map[string]string{policy.KeyStageTimeoutReview: policy.SourceCeiling}
		e.TightenDuration(&e.Governance.StageTimeout.Review, v, policy.KeyStageTimeoutReview, policy.SourceBranch)
		return e
	}
	if e := at(time.Minute); e.Governance.StageTimeout.Review != time.Minute ||
		e.Sources[policy.KeyStageTimeoutReview] != policy.SourceBranch || len(e.Ignored) != 0 {
		t.Errorf("a tighter stage timeout left %+v, want the layer's own minute", e)
	}
	for _, v := range []time.Duration{0, -time.Minute} {
		if e := at(v); e.Governance.StageTimeout.Review != 2*time.Minute ||
			e.Sources[policy.KeyStageTimeoutReview] != policy.SourceCeiling || len(e.Ignored) != 0 {
			t.Errorf("TightenDuration(%v) left %+v, want the running value untouched", v, e)
		}
	}
	e := at(5 * time.Minute)
	if e.Governance.StageTimeout.Review != 2*time.Minute {
		t.Errorf("a looser stage timeout replaced the running %v with %v", 2*time.Minute, e.Governance.StageTimeout.Review)
	}
	want := policy.Ignored{Key: policy.KeyStageTimeoutReview, Value: "5m0s", Effective: "2m0s", Source: policy.SourceCeiling}
	if len(e.Ignored) != 1 || e.Ignored[0] != want {
		t.Errorf("a looser stage timeout left %+v, want %+v", e.Ignored, want)
	}
}
