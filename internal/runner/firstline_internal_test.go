package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// The PR's status section names the tier of the review it reports, and the
// readiness rule never reads a first-line review.
func TestReviewsPartShowsTiers(t *testing.T) {
	first := func(v string, n int) runstore.ReviewSummary {
		return runstore.ReviewSummary{Round: 1, Tier: runstore.TierFirst, Verdict: v, Findings: n}
	}
	senior := func(v string, n int) runstore.ReviewSummary {
		return runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: v, Findings: n}
	}
	for _, tc := range []struct {
		rs   []runstore.ReviewSummary
		want string
	}{
		{[]runstore.ReviewSummary{{Round: 1, Verdict: "ship"}}, "review: ship in round 1"},
		{[]runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 2}}, "review round 1: 2 findings"},
		{[]runstore.ReviewSummary{first("changes", 1)}, "first-line review round 1: 1 finding"},
		{[]runstore.ReviewSummary{first("ship", 0)}, "first-line review: ship in round 1"},
		{[]runstore.ReviewSummary{first("none", 1)}, "first-line review round 1: skipped, no verdict"},
		{[]runstore.ReviewSummary{first("ship", 0), senior("changes", 3)}, "senior review round 2: 3 findings"},
		{[]runstore.ReviewSummary{first("changes", 1), senior("ship", 0)}, "senior review: ship in round 2"},
	} {
		if got := reviewsPart(tc.rs); got != tc.want {
			t.Errorf("reviewsPart(%+v) = %q, want %q", tc.rs, got, tc.want)
		}
	}
}

func TestSeniorReviewSkipsFirstLine(t *testing.T) {
	rs := []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "ship"}}
	if seniorReview(rs) != nil {
		t.Fatal("a first-line review was read as the senior verdict")
	}
	if ok, _ := Decide(nil, "", seniorReview(rs)); ok {
		t.Fatal("ready")
	}
	rs = append(rs, runstore.ReviewSummary{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: 1}, runstore.ReviewSummary{Round: 1, Tier: runstore.TierFirst, Verdict: "ship"})
	if got := seniorReview(rs); got == nil || got.Verdict != "changes" {
		t.Fatalf("senior = %+v", got)
	}
}
