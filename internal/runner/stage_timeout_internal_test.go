package runner

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestStageTimeoutMapsEachStageToItsKey: governance bounds three stages, and
// each reads its own key (T7.1, T7.6). A review stage is "review" or the
// first-line "review_first" and both read stage_timeout.review, which is also
// what makes them one stage for the budget and the ratio (G6, D331). The
// stages governance bounds none of have no bound and no key: an artifact
// run's plan and render steps, and a stage name that is not one of the three.
//
// Both readers are here because they are one mapping: the duration a stage is
// bounded by, and the key a dropped bound is recorded under
// (noteStageTimeoutClamped), which have to agree about which key a stage is.
func TestStageTimeoutMapsEachStageToItsKey(t *testing.T) {
	r := &run{policy: policy.Effective{Governance: policy.Governance{StageTimeout: policy.StageTimeout{
		Implement: time.Minute, Review: 2 * time.Minute, Fix: 3 * time.Minute}}}}
	for _, tc := range []struct {
		stage string
		key   string
		want  time.Duration
	}{
		{"implement", policy.KeyStageTimeoutImplement, time.Minute},
		{"review", policy.KeyStageTimeoutReview, 2 * time.Minute},
		{"review_first", policy.KeyStageTimeoutReview, 2 * time.Minute},
		{"fix", policy.KeyStageTimeoutFix, 3 * time.Minute},
		{"plan", "", 0},
		{"render", "", 0},
		{"verify", "", 0},
		{"", "", 0},
	} {
		if got := stageTimeoutKey(tc.stage); got != tc.key {
			t.Errorf("stageTimeoutKey(%q) = %q, want %q", tc.stage, got, tc.key)
		}
		if got := r.stageTimeout(tc.stage); got != tc.want {
			t.Errorf("stageTimeout(%q) = %v, want %v", tc.stage, got, tc.want)
		}
	}
}

// TestStageTimeoutUnsetStageHasNoBound: a run that resolved no stage timeout
// for a stage has none, whatever the other two are: the merge leaves an unset
// key unset, and a stage bounded by zero would be a stage that cannot run at
// all.
func TestStageTimeoutUnsetStageHasNoBound(t *testing.T) {
	r := &run{policy: policy.Effective{Governance: policy.Governance{StageTimeout: policy.StageTimeout{
		Implement: time.Minute}}}}
	if got := r.stageTimeout("fix"); got != 0 {
		t.Errorf("stageTimeout(fix) = %v, want no bound", got)
	}
	if got := stageTimeoutKey("fix"); got != policy.KeyStageTimeoutFix {
		t.Errorf("stageTimeoutKey(fix) = %q, want %q", got, policy.KeyStageTimeoutFix)
	}
}
