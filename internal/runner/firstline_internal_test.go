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

// TestReviewsPartNamesTheReviewer: the status line says what reviewed the
// round it reports — the model, the reasoning effort and the commit (CLI
// 0.16.0) — and each part is written only when the round recorded it, so a
// record written before the fields existed prints the line this repository
// printed before them.
func TestReviewsPartNamesTheReviewer(t *testing.T) {
	head := "0123456789abcdef0123456789abcdef01234567"
	short := head[:7]
	round := func(v string, n int, model, effort string) runstore.ReviewSummary {
		return runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: v, Findings: n, Head: head, Model: model, Effort: effort}
	}
	tolerated := FindingsPolicy{Mode: "startup", Round: 2, Tolerated: []rigor.Score{
		{Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness},
		{Likelihood: 1, Impact: 2, FixCost: 1, Class: rigor.Cosmetic},
		{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Cosmetic},
	}}
	for _, tc := range []struct {
		name string
		rs   []runstore.ReviewSummary
		pol  FindingsPolicy
		want string
	}{
		{
			"a shipping round names all three",
			[]runstore.ReviewSummary{round("ship", 0, "claude-opus-5-5", "high")}, FindingsPolicy{},
			"senior review: ship in round 2 (recorded: model claude-opus-5-5, effort high, at " + short + ")",
		},
		{
			"a level with no effort of its own leaves it out",
			[]runstore.ReviewSummary{round("ship", 0, "claude-sonnet-5", "")}, FindingsPolicy{},
			"senior review: ship in round 2 (recorded: model claude-sonnet-5, at " + short + ")",
		},
		{
			"a round that recorded no head leaves the commit out",
			[]runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "ship", Model: "claude-opus-5-5", Effort: "max"}}, FindingsPolicy{},
			"senior review: ship in round 2 (recorded: model claude-opus-5-5, effort max)",
		},
		{
			"a round that recorded none of them prints the line it always did",
			[]runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "ship"}}, FindingsPolicy{},
			"senior review: ship in round 2",
		},
		{
			"the facts come after the tolerated part, so the verdict and the policy stay one clause",
			[]runstore.ReviewSummary{round("changes", 3, "claude-opus-5-5", "high")}, tolerated,
			"senior review round 2: 3 findings, tolerated at startup (low, nit, nit) (recorded: model claude-opus-5-5, effort high, at " + short + ")",
		},
		{
			"a first-line round that answered nothing still names what ran",
			[]runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "none", Head: head, Model: "deepseek/deepseek-v4-flash"}},
			FindingsPolicy{},
			"first-line review round 1: skipped, no verdict (recorded: model deepseek/deepseek-v4-flash, at " + short + ")",
		},
		{
			"an unparseable outer round names what ran too",
			[]runstore.ReviewSummary{{Round: 1, Tier: runstore.TierAdversarial, Verdict: "none", Head: head, Effort: "xhigh"}},
			FindingsPolicy{},
			"adversarial review round 1 returned no parseable verdict after a retry (recorded: effort xhigh, at " + short + ")",
		},
		{
			"a model a hand edited into the record cannot forge section markup",
			[]runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "ship", Model: "claude\n## shipped by *someone*"}},
			FindingsPolicy{},
			"senior review: ship in round 2 (recorded: model claude shipped by \\*someone\\*)",
		},
		{
			"a head a hand edited into the record is not printed as one",
			[]runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "ship", Head: "abc\n## shipped by *someone*"}},
			FindingsPolicy{},
			"senior review: ship in round 2 (recorded: at (invalid))",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewsPart(tc.rs, tc.pol); got != tc.want {
				t.Errorf("reviewsPart(%+v, %+v) = %q, want %q", tc.rs, tc.pol, got, tc.want)
			}
		})
	}
}

func TestSeniorReviewSkipsFirstLine(t *testing.T) {
	rs := []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "ship"}}
	if seniorReview(rs) != nil {
		t.Fatal("a first-line review was read as the senior verdict")
	}
	if ok, _ := Decide(nil, "", seniorReview(rs), FindingsPolicy{}, nil); ok {
		t.Fatal("ready")
	}
	rs = append(rs, runstore.ReviewSummary{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: 1}, runstore.ReviewSummary{Round: 1, Tier: runstore.TierFirst, Verdict: "ship"})
	if got := seniorReview(rs); got == nil || got.Verdict != "changes" {
		t.Fatalf("senior = %+v", got)
	}
}
