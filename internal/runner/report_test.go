package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// toleratedRec is a record whose mode let one finding through: the shape
// T4.2's report reads back.
func toleratedRec(findings []runstore.ToleratedFinding) *runstore.Record {
	return &runstore.Record{
		RunID:   "20261027-120000-abcd",
		Outcome: runstore.OutcomeReady,
		Rigor: &runstore.RigorRecord{
			Mode: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtFinalize, Revision: "origin/main",
		},
		Reviews:   []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: len(findings)}},
		Tolerated: runstore.NewTolerated(findings),
	}
}

// TestReportOmitsToleratedForARoundTheModeBlocked: a round that left a
// blocking finding beside a tolerated one is a round Decide did not tolerate,
// and the pull request's body says so in the round's own count rather than
// naming anything tolerated. The report's table must not claim what the body
// leaves out: what the mode tolerated is the whole round or nothing (D7, F4).
func TestReportOmitsToleratedForARoundTheModeBlocked(t *testing.T) {
	tol, blocked := tiny, big
	rec := &runstore.Record{
		RunID:   "20261027-120000-abcd",
		HeadSHA: "abc",
		Outcome: runstore.OutcomeDraft,
		Rigor: &runstore.RigorRecord{
			Mode: rigor.Startup, From: rigor.FromRepo, At: runstore.RigorAtFinalize, Revision: "origin/main",
		},
		Reviews: []runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 2}},
		Tolerated: runstore.NewTolerated([]runstore.ToleratedFinding{{
			Title: "unbounded retry loop in the fetch", Mode: rigor.Startup,
			Likelihood: tol.Likelihood, Impact: tol.Impact, FixCost: tol.FixCost, Class: tol.Class,
		}}),
	}
	pol := FindingsPolicy{Mode: rigor.Startup, Round: 2, Tolerated: []rigor.Score{tol}, Blocking: []rigor.Score{blocked}}
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: rec.HeadSHA, CleanTree: true, Passed: true}
	ready, reason := Decide([]verify.Record{pass}, rec.HeadSHA, seniorReview(rec.Reviews), pol)
	if ready {
		t.Fatalf("the round was tolerated: %q", reason)
	}
	rec.Reason = reason

	got := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)
	if !strings.Contains(got, "**Outcome:** draft — "+reason) {
		t.Errorf("the report does not carry the round the mode blocked:\n%s", got)
	}
	if !strings.Contains(got, "**Reviews:** senior round 2: changes (2 findings)") {
		t.Errorf("the report lost the round's own count:\n%s", got)
	}
	if !strings.Contains(got, "**Rigor:** "+rec.Rigor.Line()) {
		t.Errorf("the report lost the mode line:\n%s", got)
	}
	for _, claim := range []string{"#### Tolerated", "| Label |", "unbounded retry loop"} {
		if strings.Contains(got, claim) {
			t.Errorf("the report claims %q for a round the mode blocked:\n%s", claim, got)
		}
	}
}

// TestReportListsWhatTheModeTolerated: the report carries the list
// `fugaro diagnose` reads back — the mode the run resolved and one row per
// tolerated finding, with the score the mode's table read.
func TestReportListsWhatTheModeTolerated(t *testing.T) {
	rec := toleratedRec([]runstore.ToleratedFinding{{
		Title: "unbounded retry loop in the fetch", Mode: rigor.Pro,
		Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness,
	}})
	got := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)
	for _, want := range []string{
		"**Rigor:** pro, from repo, at finalize, read from origin/main",
		// The words are the block's own, which `fugaro diagnose` prints too:
		// the report contributes the label, never a second wording.
		"**Rigor:** " + rec.Rigor.Line(),
		"#### Tolerated",
		"| Label | Likelihood | Impact | Fix cost | Class | Mode | Finding |",
		"| low | 2 | 2 | 1 | correctness | pro | `unbounded retry loop in the fetch` |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

// TestReportToleratedTotalIsSaidWhenTheTableIsPartial: a table that is not the
// whole list says how many findings the mode really tolerated, rather than
// passing its last row off as the last one. It never says its rows are "the
// first" of the total: the gap is the record's cap only when the list was long
// enough for it, and NewTolerated also drops an entry it cannot read, so the
// rows are some of the total and not the opening of it. The round the record's
// summary carries counts every finding it left, which is the total the mode
// tolerated: the rows are short of it because the record could not keep them,
// not because the round held anything back.
func TestReportToleratedTotalIsSaidWhenTheTableIsPartial(t *testing.T) {
	row := runstore.ToleratedFinding{
		Title: "one finding", Mode: rigor.Pro,
		Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness,
	}
	for _, tc := range []struct {
		name  string
		total int
		want  string
	}{
		{"the record's cap", runstore.MaxTolerated + 7, "107 findings tolerated in all; the record lists 1 of them."},
		{"an entry the record dropped", 2, "2 findings tolerated in all; the record lists 1 of them."},
	} {
		rec := toleratedRec([]runstore.ToleratedFinding{row})
		rec.Tolerated.Total, rec.Reviews[0].Findings = tc.total, tc.total
		got := Report(rec, "loc/", nil)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: the partial table does not say so (%q):\n%s", tc.name, tc.want, got)
		}
		if strings.Contains(got, "the first") {
			t.Errorf("%s: the table claims its rows are the first of the total:\n%s", tc.name, got)
		}
	}
	// A list that is the whole answer says nothing about a total.
	rec := toleratedRec([]runstore.ToleratedFinding{row})
	if whole := Report(rec, "loc/", nil); strings.Contains(whole, "tolerated in all") {
		t.Errorf("a complete list names a total:\n%s", whole)
	}
}

// TestReportWithoutToleratedDiffersByTheModeLine: a run that tolerated nothing
// is today's report plus the one line naming the mode it resolved — no
// section, no header, no count.
func TestReportWithoutToleratedDiffersByTheModeLine(t *testing.T) {
	rec := toleratedRec(nil)
	rec.Rigor = nil
	today := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)

	rec.Rigor = &runstore.RigorRecord{Mode: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtFinalize}
	got := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)

	const line = "**Rigor:** pro, from repo, at finalize\n\n"
	if !strings.Contains(got, line) {
		t.Fatalf("the mode line is missing:\n%s", got)
	}
	if stripped := strings.Replace(got, line, "", 1); stripped != today {
		t.Errorf("a run that tolerated nothing changed beyond the mode line:\n--- got ---\n%s\n--- want ---\n%s", stripped, today)
	}
}

// TestReportToleratedTitleStaysInsideItsCell: a title is the reviewer's own
// text, so it can carry a pipe, a backtick run or a line that reads as a
// heading. Each must render inside the cell it belongs to.
func TestReportToleratedTitleStaysInsideItsCell(t *testing.T) {
	rec := toleratedRec([]runstore.ToleratedFinding{{
		Title: "drop the cache | then ``rm -rf``\n# Heading\nand the rest", Mode: rigor.Pro,
		Likelihood: 3, Impact: 3, FixCost: 2, Class: rigor.Performance,
	}})
	got := Report(rec, "loc/", nil)
	if want := "| medium | 3 | 3 | 2 | performance | pro | ```drop the cache \\| then ``rm -rf`` # Heading and the rest``` |"; !strings.Contains(got, want) {
		t.Errorf("the title's row is not one cell holding it:\nwant %q\ngot:\n%s", want, got)
	}
	if strings.Contains(got, "\n# Heading") {
		t.Errorf("a title forged a heading:\n%s", got)
	}
}

// TestPolicyLineGivesAStageTimeoutItsOwnWording: a stage timeout the run's
// own timeouts.stage bounds at least as tightly is dropped for a different
// reason than a looser value — the bound is not looser than a limit, it can
// come from the project layer itself — so the report says why rather than
// folding it into the "looser than the project's limits" sentence (T7.6).
func TestPolicyLineGivesAStageTimeoutItsOwnWording(t *testing.T) {
	p := &runstore.PolicyRecord{Ignored: []runstore.PolicyIgnored{
		{Key: policy.KeyPerRunUSD, Value: "20", Effective: "5", Source: policy.SourceDefaultBranch, From: policy.SourceBranch},
		{Key: policy.KeyStageTimeoutImplement, Value: "1h", Effective: "40m", Source: policy.SourceCeiling, From: policy.SourceCeiling},
	}}
	got := policyLine(p)
	loose := "**Policy:** 1 value from fugaro.yaml on this branch was looser than the project's limits and was ignored (budget.per_run_usd 20 -> 5)\n\n"
	if !strings.Contains(got, loose) {
		t.Errorf("the looser value's sentence is missing or now carries the stage timeout:\n%s\nwant %q", got, loose)
	}
	stage := "**Policy:** the run's own timeouts.stage bounds a stage at least as tightly as the stage timeout asked for, so 1 value was ignored (governance.stage_timeout.implement 1h from the project layer -> 40m)\n\n"
	if !strings.Contains(got, stage) {
		t.Errorf("the stage timeout has no wording of its own:\n%s\nwant %q", got, stage)
	}
}

// TestPolicyLineNamesTheBranchForAStageTimeoutFromTheFile: the same sentence
// when the layer that asked for the dropped stage timeout is the repository's
// own file rather than the project layer.
func TestPolicyLineNamesTheBranchForAStageTimeoutFromTheFile(t *testing.T) {
	p := &runstore.PolicyRecord{Ignored: []runstore.PolicyIgnored{
		{Key: policy.KeyStageTimeoutReview, Value: "30m", Effective: "10m", Source: policy.SourceBranch, From: policy.SourceBranch},
	}}
	got := policyLine(p)
	want := "so 1 value was ignored (governance.stage_timeout.review 30m from fugaro.yaml on this branch -> 10m)"
	if !strings.Contains(got, want) {
		t.Errorf("policyLine = %q, want %q", got, want)
	}
}
