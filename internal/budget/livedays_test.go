package budget_test

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
)

// TestLiveDaysJoinsABatchRunQueuedAcrossMidnight: a live day's record is
// computed from the day's own nodes and from the facts of the day before it,
// because a run the day's ledger and outcome nodes name may have started there.
// On Cloud Batch such a run can be minted hours before that window opens — the
// job sat in the region's queue across its midnight — and it is the backend's
// own look-back that lists it: joined, the run is not a run of the live day;
// left out, its spend and its outcome, both written on the live day it
// finished on, are counted there although its record says it started on the
// day before.
// Mutation caught: Cloud Run's thirty minutes for every backend, which leaves
// this run counted on a day it did not start on.
func TestLiveDaysJoinsABatchRunQueuedAcrossMidnight(t *testing.T) {
	e := newRollEnv(t)
	d := e.today() - 2
	id, rec := queuedBatchRun(d - 1)
	e.seedDay(d, rollSlug, 3_000_000, id)
	plain, _ := factsBuckets(t, []factsRun{{slug: rollSlug, id: id, rec: rec}})

	days, err := budget.LiveDays(context.Background(), e.r.DB,
		budget.BucketFacts{Bucket: plain, Backend: backend.CloudBatch}, []int64{d}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	recs := days[d]
	if len(recs) != 1 || recs[0].Slug != rollSlug {
		t.Fatalf("day %d = %+v, want the day's one repository", d, recs)
	}
	r := recs[0]
	if r.SpentMicros == 0 {
		t.Fatalf("the day's record = %+v: the fixture put no spend on the day, so this test compares nothing", r)
	}
	if r.Runs != 0 || r.ComputeMicros != 0 || r.Outcomes["succeeded"] != 0 {
		t.Fatalf("the day's record = %+v: the run's record starts on the day before, so neither it nor its outcome is this day's", r)
	}
}
