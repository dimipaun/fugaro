package userstats

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestEveryStatusReaderHandlesPaused is model-credentials-routing PR11's
// per-reader half for this package: a run that suspended itself to be resumed
// by its chain (contract-pause, R15) is neither a failure nor a success, so the
// census counts it in Other beside cancelled and unreviewed (D996: a status
// this view names no counter of its own for) and never in Failed. One sub-test
// per reader.
func TestEveryStatusReaderHandlesPaused(t *testing.T) {
	t.Run("aggregate", func(t *testing.T) {
		res := Aggregate([]Run{{
			User: "dana@example.com", Status: string(runstore.StatusPaused), CountedUSD: 3, CountedCost: true,
		}})
		if len(res.Rows) != 1 || len(res.Totals) != 1 {
			t.Fatalf("rows = %+v totals = %+v, want one of each", res.Rows, res.Totals)
		}
		want := Counts{Runs: 1, Other: 1}
		if res.Rows[0].Counts != want {
			t.Fatalf("counts = %+v, want %+v: a paused run is Other, never failed and never succeeded", res.Rows[0].Counts, want)
		}
		// The counts still add up, which is the invariant a spend screen reads
		// the shape by, and the run's money is still counted: the census keeps
		// the ending in Other, not the run out of the totals.
		c := res.Totals[0].Counts
		if sum := c.Succeeded + c.Failed + c.Halted + c.InfraError + c.Other + c.NoRecord; sum != c.Runs {
			t.Errorf("the counts do not add up: %d != %d (%+v)", sum, c.Runs, c)
		}
		if res.Totals[0].CountedUSD != 3 || res.Totals[0].CountedRuns != 1 {
			t.Errorf("total = %+v, want the paused run's spend counted once", res.Totals[0])
		}
	})
}
