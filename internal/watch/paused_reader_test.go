package watch

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestEveryStatusReaderHandlesPaused is model-credentials-routing PR11's
// per-reader half for this package: a run whose record ended `paused` stopped
// its own execution to be resumed by its chain (contract-pause, R15), so the
// finished list draws it with its own word — never as a failure, and never as
// a success — and it is not held by the wider window a failed row gets. One
// sub-test per reader.
func TestEveryStatusReaderHandlesPaused(t *testing.T) {
	const paused = string(runstore.StatusPaused)

	t.Run("isFailure", func(t *testing.T) {
		// The failure set is the three endings a person has to look at; the
		// other five words runstore holds are not, and paused is one of them.
		for _, tc := range []struct {
			status string
			want   bool
		}{
			{string(runstore.StatusFailed), true},
			{string(runstore.StatusHalted), true},
			{string(runstore.StatusInfraError), true},
			{paused, false},
			{string(runstore.StatusSucceeded), false},
			{string(runstore.StatusUnreviewed), false},
			{string(runstore.StatusCancelled), false},
			{string(runstore.StatusRunning), false},
		} {
			if got := isFailure(tc.status); got != tc.want {
				t.Errorf("isFailure(%q) = %v, want %v", tc.status, got, tc.want)
			}
		}
		// The table is every word runstore's own set holds: a word added there
		// is a row someone writes here rather than one this reader answers
		// false for unread.
		if len(runstore.Statuses) != 8 {
			t.Errorf("runstore.Statuses holds %d words, and this table names 8", len(runstore.Statuses))
		}
	})

	t.Run("finishedRow", func(t *testing.T) {
		now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
		row := finishedRow(FinishedRun{
			Run: "20261124-081500-4c1a", Slug: "acme-app", Title: "the run that ran out of chain",
			Status: paused, FinishedAt: now.Add(-10 * time.Minute),
		}, now)
		if row.Failed {
			t.Errorf("the paused row is marked failed: it was drawn bad and kept for the failure window (filter.go's FailedKeep)")
		}
		if row.Stage != paused {
			t.Errorf("the row's word is %q, want %q: the OUTCOME cell is the record's own status", row.Stage, paused)
		}
		if !row.Paused {
			t.Errorf("the row is not marked paused: %+v", row)
		}
		// What a person reads: the paused word, never the word a finished run
		// is drawn with. A paused run is in the finished block — that is where
		// a record that ended is listed — and its own word is what says the
		// chain comes back, so it is never listed as a finished success.
		if word := finishedOutcomeText(row); word != "PAUSED" {
			t.Errorf("the OUTCOME cell reads %q, want PAUSED: a paused run is neither a success nor a failure", word)
		}
	})
}
