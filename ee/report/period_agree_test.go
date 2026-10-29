// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package report_test

import (
	"testing"
	"time"

	report "github.com/dimipaun/fugaro/ee/report"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/digest"
)

// The digest prints its own committed report files from
// internal/digest/period.go — the MIT copy it keeps so an untagged build still
// writes a digest (D444, T11) — and `fugaro report` prints its figures from
// ee/report: a drift between the two would put two answers to the same run's
// dollars in one tree. This is the only place that sees both, so it is where
// the cross-check lives; it moved here with the report (E2.2b), because a core
// test may not import ee/.
func TestDigestPeriodCopyMatchesReport(t *testing.T) {
	t.Run("the vocabulary", func(t *testing.T) {
		for _, s := range []string{"day", "week", "month", "year", "repo", "model", "person", "mode", "weekly", ""} {
			got, gotErr := digest.ParseDim(s)
			want, wantErr := report.ParseDim(s)
			if string(got) != string(want) {
				t.Errorf("ParseDim(%q) = %q, want report's %q", s, got, want)
			}
			if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
				t.Errorf("ParseDim(%q) refused with %v, want report's %v", s, gotErr, wantErr)
			}
		}
	})

	t.Run("is-period", func(t *testing.T) {
		for _, d := range []string{"day", "week", "month", "year", "repo", "model", "person", "mode"} {
			if got, want := digest.Dim(d).IsPeriod(), report.Dim(d).IsPeriod(); got != want {
				t.Errorf("IsPeriod(%s) = %v, want report's %v", d, got, want)
			}
		}
	})

	t.Run("the period keys", func(t *testing.T) {
		for _, date := range []string{"2026-10-21", "2026-12-31", "2027-01-01", "2026-02-01", "not-a-date"} {
			for _, d := range []string{"day", "week", "month", "year", "repo"} {
				gotKey, gotStart, gotErr := digest.PeriodKey(digest.Dim(d), date)
				wantKey, wantStart, wantErr := report.PeriodKey(report.Dim(d), date)
				if gotKey != wantKey || gotStart != wantStart {
					t.Errorf("PeriodKey(%s, %s) = %q, %q, want report's %q, %q", d, date, gotKey, gotStart, wantKey, wantStart)
				}
				if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
					t.Errorf("PeriodKey(%s, %s) refused with %v, want report's %v", d, date, gotErr, wantErr)
				}
			}
		}
	})

	t.Run("the dollars", func(t *testing.T) {
		for _, m := range []budget.Micros{0, 1, 1234, 1000000, 123456789012, -2500000} {
			if got, want := digest.USD6(m), report.USD6(m); got != want {
				t.Errorf("USD6(%d) = %q, want report's %q", int64(m), got, want)
			}
			if got, want := digest.USD(m), report.USD(m); got != want {
				t.Errorf("USD(%d) = %q, want report's %q", int64(m), got, want)
			}
		}
	})
}

// TestDigestPeriodCopyMatchesReportOnATime is the same check on a time: the
// digest's own readers spell a period's key from a clock, and a copy that
// rounded differently would put a run in a period the report does not.
func TestDigestPeriodCopyMatchesReportOnATime(t *testing.T) {
	at := time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC)
	got, err := digest.NewPeriod(digest.ByWeek, at.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	key, start, err := report.PeriodKey(report.ByWeek, at.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != key || got.Start != start {
		t.Errorf("the digest's week = %q, %q, want report's %q, %q", got.Key, got.Start, key, start)
	}
}
