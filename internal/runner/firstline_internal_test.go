package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
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
	// The policy of a Startup run whose round 2 left these three findings open,
	// all of them tolerated: the split is the mode's (rigor.Split), and the
	// line names each label in the reviewer's own order (T3.3).
	tolerated := FindingsPolicy{Mode: "startup", Round: 2, Tolerated: []rigor.Score{
		{Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness},
		{Likelihood: 1, Impact: 2, FixCost: 1, Class: rigor.Cosmetic},
		{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Cosmetic},
	}}
	for _, tc := range []struct {
		rs   []runstore.ReviewSummary
		pol  FindingsPolicy
		want string
	}{
		{[]runstore.ReviewSummary{{Round: 1, Verdict: "ship"}}, FindingsPolicy{}, "review: ship in round 1"},
		{[]runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 2}}, FindingsPolicy{}, "review round 1: 2 findings"},
		{[]runstore.ReviewSummary{first("changes", 1)}, FindingsPolicy{}, "first-line review round 1: 1 finding"},
		{[]runstore.ReviewSummary{first("ship", 0)}, FindingsPolicy{}, "first-line review: ship in round 1"},
		{[]runstore.ReviewSummary{first("none", 1)}, FindingsPolicy{}, "first-line review round 1: skipped, no verdict"},
		{[]runstore.ReviewSummary{first("ship", 0), senior("changes", 3)}, FindingsPolicy{}, "senior review round 2: 3 findings"},
		{[]runstore.ReviewSummary{first("changes", 1), senior("ship", 0)}, FindingsPolicy{}, "senior review: ship in round 2"},
		{[]runstore.ReviewSummary{senior("changes", 3)}, tolerated, "senior review round 2: 3 findings, tolerated at startup (low, nit, nit)"},
		// A round the mode did not let through: the policy is the one that
		// blocks, or the split is another round's, and the line is the one
		// this repository printed before T3.3 either way.
		{[]runstore.ReviewSummary{senior("changes", 3)}, FindingsPolicy{Mode: "startup", Round: 2}, "senior review round 2: 3 findings"},
		{[]runstore.ReviewSummary{senior("changes", 2)}, tolerated, "senior review round 2: 2 findings"},
		// A first-line round never decides, so it never tolerates anything: a
		// cheap loop that leaves the same count in the round number the split
		// was taken from claims nothing (seniorReview).
		{[]runstore.ReviewSummary{{Round: 2, Tier: runstore.TierFirst, Verdict: "changes", Findings: 3}}, tolerated, "first-line review round 2: 3 findings"},
	} {
		if got := reviewsPart(tc.rs, tc.pol); got != tc.want {
			t.Errorf("reviewsPart(%+v, %+v) = %q, want %q", tc.rs, tc.pol, got, tc.want)
		}
	}
}

func TestSeniorReviewSkipsFirstLine(t *testing.T) {
	rs := []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "ship"}}
	if seniorReview(rs) != nil {
		t.Fatal("a first-line review was read as the senior verdict")
	}
	if ok, _ := Decide(nil, "", seniorReview(rs), FindingsPolicy{}); ok {
		t.Fatal("ready")
	}
	rs = append(rs, runstore.ReviewSummary{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: 1}, runstore.ReviewSummary{Round: 1, Tier: runstore.TierFirst, Verdict: "ship"})
	if got := seniorReview(rs); got == nil || got.Verdict != "changes" {
		t.Fatalf("senior = %+v", got)
	}
}
