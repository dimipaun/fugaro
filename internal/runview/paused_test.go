package runview

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// statusPausedConstant is the name of the constant a reader names the word
// with, which is what the source read below looks for.
const statusPausedConstant = "StatusPaused"

// funcBody is one top-level function's body as its source spells it: the text
// from header to the closing brace that starts at column 0, which is where a
// top-level declaration ends.
func funcBody(src, header string) (string, bool) {
	at := strings.Index(src, header)
	if at < 0 {
		return "", false
	}
	rest := src[at:]
	if end := strings.Index(rest, "\n}"); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

// TestEveryStatusReaderHandlesPaused is model-credentials-routing PR11's
// per-reader half for this package: `paused` (contract-pause, R15) is a run
// that suspended itself so its chain can resume it, so it is neither a failure
// nor a success, and every reader of a status here answers for the word rather
// than falling through a list that predates it. One sub-test per reader.
func TestEveryStatusReaderHandlesPaused(t *testing.T) {
	const paused = runstore.StatusPaused

	t.Run("failedStatus", func(t *testing.T) {
		// The failure set is the set a record's class is written for, and a
		// paused run has no class to carry: it did not end.
		for _, tc := range []struct {
			status runstore.Status
			want   bool
		}{
			{runstore.StatusFailed, true},
			{runstore.StatusUnreviewed, true},
			{runstore.StatusHalted, true},
			{runstore.StatusInfraError, true},
			{runstore.StatusCancelled, true},
			{paused, false},
			{runstore.StatusSucceeded, false},
			{runstore.StatusRunning, false},
		} {
			if got := failedStatus(tc.status); got != tc.want {
				t.Errorf("failedStatus(%q) = %v, want %v", tc.status, got, tc.want)
			}
		}
		// The table is every word runstore's own set holds, so a status added
		// there is a row someone has to write here rather than a word this
		// reader silently answers false for.
		if len(runstore.Statuses) != 8 {
			t.Errorf("runstore.Statuses holds %d words, and this table names 8: a word added to or removed from the set is a row to write here", len(runstore.Statuses))
		}
		// The answers above are the fall-through's as much as this reader's —
		// a switch that never heard of the word answers false for it too — so
		// the word being a decision in this switch is read from the reader's
		// own source, which is the half of this test that fails on a build
		// whose word is unknown to it (PR11's red step).
		src, err := os.ReadFile("runview.go")
		if err != nil {
			t.Fatal(err)
		}
		body, ok := funcBody(string(src), "func failedStatus(")
		if !ok {
			t.Fatal("runview.go no longer declares func failedStatus, and this test reads that reader's own switch")
		}
		if !strings.Contains(body, "runstore."+statusPausedConstant) {
			t.Errorf("failedStatus does not name runstore.%s: a paused run's answer is then the fall-through's rather than this reader's", statusPausedConstant)
		}
	})

	t.Run("classOf", func(t *testing.T) {
		// A paused record carries no class whatever its reason reads like: the
		// class is the ending the status names, and a run that suspended
		// itself has not ended.
		suspended := &runstore.Record{Status: paused, Reason: "a credential is exhausted, resume at the reset"}
		if got := ClassOf(suspended); got != "" {
			t.Errorf("ClassOf(paused, %q) = %q, want no class", suspended.Reason, got)
		}
		// The control: the same reason under a failure status is a class
		// (Unclassified is a class), so the assertion above is about the
		// status and not about the sentence.
		ended := &runstore.Record{Status: runstore.StatusFailed, Reason: suspended.Reason}
		if ClassOf(ended) == "" {
			t.Errorf("ClassOf(failed, %q) carries no class: the control does not hold, so the paused case proves nothing", ended.Reason)
		}
		// A class written beside the paused status is not published either:
		// the field describes an ending, and this record says the run has not
		// had one.
		suspended.FailureClass = failure.TestsFailed
		if got := ClassOf(suspended); got != "" {
			t.Errorf("ClassOf(paused, class %q) = %q, want no class", suspended.FailureClass, got)
		}
	})

	t.Run("row", func(t *testing.T) {
		// The row a listing shows: the record's own word, no class, and no
		// halt — a pause is not a refusal (contract-pause's table of the four
		// answers an exhausted credential gets).
		rec := &runstore.Record{Status: paused, Stage: "implement", Reason: "a credential is exhausted, resume at the reset",
			CostUSD: 2}
		row := Join(Input{Task: spec, Launch: launch, Record: rec, Exec: exec(backend.StateSucceeded)}, prices, now)
		if row.Status != string(paused) {
			t.Errorf("the row reads %q, want %q: a paused run is drawn as the word its record carries", row.Status, paused)
		}
		if row.FailureClass != "" {
			t.Errorf("the row carries the class %q, want none: a paused run is not a failure", row.FailureClass)
		}
		if row.Halt != nil {
			t.Errorf("the row carries the halt %+v, want none: a run that suspended itself was not refused", row.Halt)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		// The row's own derivation: a paused run's execution is over — the
		// container stopped and the lease was released (contract-pause, R15) —
		// so the row is terminal, which is what the listing reads to know no
		// live execution is behind it. Terminal is not "finished": the chain
		// is resumed by another run, and the settled word the row carries is
		// the record's own.
		rec := &runstore.Record{Status: paused, Stage: "implement", CostUSD: 2}
		row := Join(Input{Task: spec, Launch: launch, Record: rec, Exec: exec(backend.StateSucceeded)}, prices, now)
		if !row.Terminal {
			t.Errorf("the paused row is not terminal: its execution stopped and its lease was released")
		}
		if row.Status == string(runstore.StatusSucceeded) {
			t.Errorf("the paused row reads succeeded: a run that suspended itself is not a success")
		}
	})
}
