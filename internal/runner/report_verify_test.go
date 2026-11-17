package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// verifyRec is the fixture the verify table is read back from: a build, a
// failing test run and a passing one with counts, in the order they ran.
func verifyRec() *runstore.Record {
	const sha = "abc1234def5678901234567890123456789012ab"
	return &runstore.Record{
		RunID:   "20261117-120000-abcd",
		HeadSHA: sha,
		Outcome: runstore.OutcomeReady,
		Stages: []runstore.StageTiming{
			{Name: "implement", DurationS: 120},
			{Name: "fix", DurationS: 200},
		},
		Verify: []verify.Record{
			{Kind: verify.KindBuild, HeadSHA: sha, ExitCode: 0, Passed: true, DurationS: 30},
			{Kind: verify.KindTest, HeadSHA: sha, ExitCode: 1, Passed: false, Tests: 12, Failures: 2, DurationS: 95},
			{Kind: verify.KindTest, HeadSHA: sha, ExitCode: 0, Passed: true, Tests: 12, DurationS: 40},
		},
	}
}

// TestReportListsEachVerifyRun: the report's second table carries one row per
// `fugaro verify` invocation, in the order the run made them, and its footer
// says what they add up to beside the run's wall clock. The stage table above
// it folds every verification into the stage that ran it, so this table is the
// only place the report says how long building and testing took (the owner's
// request of 2026-11-17).
func TestReportListsEachVerifyRun(t *testing.T) {
	got := Report(verifyRec(), "runs/acme-app/20261117-120000-abcd/", nil)

	if !strings.Contains(got, "| Verify | Head | Result | Tests | Duration |\n|---|---|---|---|---|\n") {
		t.Fatalf("the report has no verify table:\n%s", got)
	}
	rows := []string{
		"| build | abc1234 | passed (exit 0) |  | 30s |",
		"| test | abc1234 | failed (exit 1) | 12 tests | 1m35s |",
		"| test | abc1234 | passed (exit 0) | 12 tests | 40s |",
	}
	at := strings.Index(got, rows[0])
	if at < 0 {
		t.Fatalf("the report lost the build row:\n%s", got)
	}
	rest := got[at:]
	for _, row := range rows {
		i := strings.Index(rest, row)
		if i < 0 {
			t.Fatalf("the report does not carry the row %q in order:\n%s", row, got)
		}
		rest = rest[i+len(row):]
	}
	// 30+95+40 = 2m45s. The record states no finished_at -- the report is
	// written before the runner stamps one -- so the stages' own 5m20s is the
	// clock it is read against.
	if !strings.Contains(got, "**Verify time:** 2m45s, 52% of the run's 5m20s wall clock") {
		t.Errorf("the footer does not add the verify records up:\n%s", got)
	}
	// The stage table is unchanged by any of this.
	if !strings.Contains(got, "| implement | 2m0s |\n| fix | 3m20s |") {
		t.Errorf("the stage table changed:\n%s", got)
	}
}

// TestReportVerifyShareUsesTheRunsOwnClock: a record that states both ends of
// the run is read against what they span, not against the stages, so a report
// rendered from a finished record gives the share of the run's own wall clock.
func TestReportVerifyShareUsesTheRunsOwnClock(t *testing.T) {
	rec := verifyRec()
	start := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	finish := start.Add(20 * time.Minute)
	rec.StartedAt, rec.FinishedAt = start, &finish

	got := Report(rec, "runs/acme-app/20261117-120000-abcd/", nil)
	if !strings.Contains(got, "**Verify time:** 2m45s, 14% of the run's 20m0s wall clock") {
		t.Errorf("the footer does not read the run's own clock:\n%s", got)
	}
}

// TestReportOmitsTheVerifyTableWithoutRecords: a run that never called the
// wrapper has nothing to list, and an empty table under the stage table would
// read as a run whose verifications took no time.
func TestReportOmitsTheVerifyTableWithoutRecords(t *testing.T) {
	rec := verifyRec()
	rec.Verify = nil

	got := Report(rec, "runs/acme-app/20261117-120000-abcd/", nil)
	for _, claim := range []string{"| Verify |", "**Verify time:**"} {
		if strings.Contains(got, claim) {
			t.Errorf("the report carries %q for a run with no verify records:\n%s", claim, got)
		}
	}
}

// TestReportLeavesTheCountEmptyForARecordThatHasNone: only a test run reads
// JUnit reports, and a test run that found none carries the wrapper's warning
// and zero counts. Both cells stay empty rather than say "0 tests", which is
// the reading a suite nobody counted must not be given.
func TestReportLeavesTheCountEmptyForARecordThatHasNone(t *testing.T) {
	rec := verifyRec()
	rec.Verify = []verify.Record{
		{Kind: verify.KindBuild, HeadSHA: rec.HeadSHA, ExitCode: 0, Passed: true, DurationS: 30},
		{
			Kind: verify.KindTest, HeadSHA: rec.HeadSHA, ExitCode: 0, Passed: true, DurationS: 12,
			Warning: "no JUnit reports found under reports/",
		},
	}

	got := Report(rec, "runs/acme-app/20261117-120000-abcd/", nil)
	if strings.Contains(got, "0 tests") {
		t.Errorf("the report counts a suite the wrapper could not count:\n%s", got)
	}
	for _, row := range []string{
		"| build | abc1234 | passed (exit 0) |  | 30s |",
		"| test | abc1234 | passed (exit 0) |  | 12s |",
	} {
		if !strings.Contains(got, row) {
			t.Errorf("the report does not carry the row %q:\n%s", row, got)
		}
	}
}
