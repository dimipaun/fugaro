package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The mode's thresholds decide which findings block, inside Decide
// (docs/rigor-modes/implementation-plan.md T3.1; reconciliation.md D4, D7,
// D9). These tests hold the one change in the plan that can make a pull
// request ready that was not ready before, in both directions: what a mode
// tolerates, and that Critical — and every run whose filing cannot carry a
// tolerated finding — tolerates nothing at all.

// optedIn is a repository that configured a rigor: block, which is D9's first
// condition for filing a tolerated finding and therefore for tolerating one.
var optedIn = rigor.Config{RepoBlock: true}

// The scores the table reads, as the reviewer writes them: three anchored
// axes and a class (D2). Risk is likelihood × impact, which is the axis every
// mode's floor is stated against.
var (
	// risk 3: below Startup's floor and below Pro's, so both tolerate it on
	// the risk axis alone. Its fix cost is 1, so it never reaches Pro's escape
	// — that case is rareExpensive, not this one.
	tiny = rigor.Score{Likelihood: 1, Impact: 3, FixCost: 1, Class: rigor.Correctness}
	// risk 4, which Pro blocks at, and the one finding Pro's escape is for:
	// likelihood 1 and fix cost 4 are the edges that sentence names, "at most
	// 1" and "4 or more" (table.go).
	rareExpensive = rigor.Score{Likelihood: 1, Impact: 4, FixCost: 4, Class: rigor.Correctness}
	// risk 4: Pro's floor itself, at a likelihood the escape does not reach.
	small = rigor.Score{Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness}
	// risk 9: above every on-axis floor.
	big = rigor.Score{Likelihood: 3, Impact: 3, FixCost: 2, Class: rigor.Correctness}
	// risk 1 with an ordinary class: the same axes as catastrophic below, so
	// the pair of rows says the override reads the class and not the numbers.
	// It is the mildest finding the scale carries, which is what Hotfix's rows
	// use — Hotfix tolerates the set POC does.
	benign = rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}
	// the spec's severity override: risk 1, and catastrophic by its class.
	catastrophic = rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Security}
	// a finding the reviewer left unscored, which is the zero Score (D3, D4).
	unscored = rigor.Score{}
)

// TestDecideByMode is the headline test: for each mode, what a round whose
// findings it tolerates decides, and what one it does not decides. The
// Critical rows are today's behaviour, held here so a later edit that moves
// them is caught rather than argued about (implementation-plan §5).
func TestDecideByMode(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	fail := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true}
	dirty := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", Passed: true}
	cases := []struct {
		name     string
		records  []verify.Record
		mode     rigor.Mode
		round    int
		verdict  string
		findings []rigor.Score
		ready    bool
		reason   string
	}{
		// Critical tolerates nothing: every row here is Decide's answer
		// before modes existed, reason string included.
		{"critical blocks what every other mode tolerates", []verify.Record{pass}, rigor.Critical, 2, "changes", []rigor.Score{small, small, small}, false, "review round 2 still has 3 findings"},
		{"critical blocks pro's rare expensive escape", []verify.Record{pass}, rigor.Critical, 2, "changes", []rigor.Score{rareExpensive}, false, "review round 2 still has 1 finding"},
		{"critical blocks an unscored finding", []verify.Record{pass}, rigor.Critical, 2, "changes", []rigor.Score{unscored}, false, "review round 2 still has 1 finding"},
		{"critical with nothing left is still a draft", []verify.Record{pass}, rigor.Critical, 2, "changes", nil, false, "review round 2 still has 0 findings"},

		{"startup tolerates below its floor", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{small}, true, "review round 2 left 1 finding, tolerated at startup"},
		{"startup tolerates a whole round", []verify.Record{pass}, rigor.Startup, 3, "changes", []rigor.Score{small, tiny, rareExpensive}, true, "review round 3 left 3 findings, tolerated at startup"},
		{"startup blocks at its floor", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{big}, false, "review round 2 still has 1 finding"},
		{"startup blocks the one finding it does not tolerate", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{small, big}, false, "review round 2 still has 2 findings"},
		{"startup blocks the severity override", []verify.Record{pass}, rigor.Startup, 2, "changes", []rigor.Score{catastrophic}, false, "review round 2 still has 1 finding"},

		{"pro tolerates below its floor", []verify.Record{pass}, rigor.Pro, 2, "changes", []rigor.Score{tiny}, true, "review round 2 left 1 finding, tolerated at pro"},
		{"pro tolerates the rare expensive case", []verify.Record{pass}, rigor.Pro, 2, "changes", []rigor.Score{rareExpensive}, true, "review round 2 left 1 finding, tolerated at pro"},
		{"pro blocks at its floor", []verify.Record{pass}, rigor.Pro, 2, "changes", []rigor.Score{small}, false, "review round 2 still has 1 finding"},

		// Hotfix's row is empty like POC's — "only findings that make the fix
		// wrong or unsafe block" is the same set once catastrophic is read the
		// way the spec reads it — so a benign finding is tolerated there too.
		// Its row is the only one that is both Tolerated and Deferred (D30),
		// which is why the run-level test beside this one is about Hotfix: the
		// tolerated round makes the pull request ready *and* is the second item
		// the run files.
		{"hotfix tolerates a benign finding", []verify.Record{pass}, rigor.Hotfix, 2, "changes", []rigor.Score{benign}, true, "review round 2 left 1 finding, tolerated at hotfix"},
		{"hotfix blocks the severity override", []verify.Record{pass}, rigor.Hotfix, 2, "changes", []rigor.Score{catastrophic}, false, "review round 2 still has 1 finding"},

		// POC files nothing — its account is its pull request body's own
		// line (D30) — so D9 leaves it nothing to tolerate.
		{"poc files nothing so it tolerates nothing", []verify.Record{pass}, rigor.POC, 2, "changes", []rigor.Score{small}, false, "review round 2 still has 1 finding"},
		// A mode the table does not hold blocks on everything: the zero Mode
		// a record written before the mode existed carries, and a name that
		// arrived unparsed (F3).
		{"a mode the table does not know blocks on everything", []verify.Record{pass}, rigor.Mode("wild"), 2, "changes", []rigor.Score{tiny}, false, "review round 2 still has 1 finding"},

		// The gates Decide already had outrank the mode: a tolerated round
		// is still a draft when the run did not verify it.
		{"a failing test outranks the mode", []verify.Record{fail}, rigor.Startup, 2, "changes", []rigor.Score{small}, false, "tests failing on the final commit"},
		{"no clean test outranks the mode", []verify.Record{dirty}, rigor.Startup, 2, "changes", []rigor.Score{small}, false, ReasonNoVerifiedTest},
		{"no verdict outranks the mode", []verify.Record{pass}, rigor.Startup, 2, "", []rigor.Score{small}, false, "no review verdict"},
		{"a shipped round is ready whatever the mode", []verify.Record{pass}, rigor.Critical, 1, "ship", nil, true, ""},
	}
	for _, tc := range cases {
		var last *runstore.ReviewSummary
		if tc.verdict != "" {
			last = &runstore.ReviewSummary{Round: tc.round, Verdict: tc.verdict, Findings: len(tc.findings)}
		}
		ready, reason := Decide(tc.records, "abc", last, findingsPolicyOf(optedIn, tc.mode, tc.round, tc.findings))
		if ready != tc.ready || reason != tc.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", tc.name, ready, reason, tc.ready, tc.reason)
		}
	}
}

// TestDecideUnscoredFindingBlocksEveryMode is D4: a reviewer that stops
// emitting scores must not turn a strict mode into a lenient one, so a
// finding the table cannot read blocks wherever it appears — including beside
// one the same mode tolerates once it is scored.
func TestDecideUnscoredFindingBlocksEveryMode(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	last := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 2}
	for _, mode := range rigor.Modes {
		ready, reason := Decide([]verify.Record{pass}, "abc", last, findingsPolicyOf(optedIn, mode, 2, []rigor.Score{unscored, small}))
		if ready || reason != "review round 2 still has 2 findings" {
			t.Errorf("%s: an unscored finding was tolerated: Decide = %v %q", mode, ready, reason)
		}
	}
}

// TestDecideToleratesOnlyWhatTheRunCanFile is D9: a mode may not tolerate a
// finding it cannot file, so tolerance is taken only for a repository that
// opted in and a mode whose rigor.Files row files what it tolerates. Every
// other run decides by the count on the record, exactly as it did before
// modes existed.
func TestDecideToleratesOnlyWhatTheRunCanFile(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	last := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 1}
	for _, tc := range []struct {
		name      string
		cfg       rigor.Config
		mode      rigor.Mode
		tolerated bool
	}{
		{"a repository that configured a mode", optedIn, rigor.Startup, true},
		{"a repository that configured pro", optedIn, rigor.Pro, true},
		{"a repository that configured hotfix", optedIn, rigor.Hotfix, true},
		{"a project layer that set a rigor default", rigor.Config{Project: rigor.Pro}, rigor.Pro, true},
		{"a repository that configured poc", optedIn, rigor.POC, false},
		{"a repository that configured critical", optedIn, rigor.Critical, false},
		{"a repository that configured nothing", rigor.Config{}, rigor.Pro, false},
	} {
		ready, reason := Decide([]verify.Record{pass}, "abc", last, findingsPolicyOf(tc.cfg, tc.mode, 2, []rigor.Score{tiny}))
		if ready != tc.tolerated {
			t.Errorf("%s: Decide = %v %q, want tolerated %v", tc.name, ready, reason, tc.tolerated)
			continue
		}
		want := "review round 2 still has 1 finding"
		if tc.tolerated {
			want = "review round 2 left 1 finding, tolerated at " + string(tc.mode)
		}
		if reason != want {
			t.Errorf("%s: reason = %q, want %q", tc.name, reason, want)
		}
	}
}

// TestDecideBlocksAPolicyItCannotPlace: a split that is not the round the
// record counts is a policy that cannot decide, and one that cannot decide
// blocks (F3, D4) rather than tolerating a round nothing measured.
func TestDecideBlocksAPolicyItCannotPlace(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	last := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 1}
	for _, tc := range []struct {
		name string
		pol  FindingsPolicy
	}{
		{"a split from another round", findingsPolicyOf(optedIn, rigor.Startup, 1, []rigor.Score{tiny})},
		{"a split from fewer findings than the record counts", findingsPolicyOf(optedIn, rigor.Startup, 2, nil)},
	} {
		ready, reason := Decide([]verify.Record{pass}, "abc", last, tc.pol)
		if ready || reason != "review round 2 still has 1 finding" {
			t.Errorf("%s: Decide = %v %q, want false with the count's own reason", tc.name, ready, reason)
		}
	}
}

// TestOutcomeReadsTheScoredFields pins the seam between the reviewer's
// finding and the table: the three anchored axes and the class are read, and
// the free-form severity word beside them is not (D2).
func TestOutcomeReadsTheScoredFields(t *testing.T) {
	got := scoresOf([]Finding{
		{Severity: "blocker", Likelihood: 2, Impact: 3, FixCost: 4, Class: "maintainability", Summary: "x"},
		{Severity: "blocker", Summary: "unscored"},
	})
	want := []rigor.Score{
		{Likelihood: 2, Impact: 3, FixCost: 4, Class: rigor.Maintainability},
		{},
	}
	if len(got) != len(want) {
		t.Fatalf("scoresOf = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("scoresOf[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
