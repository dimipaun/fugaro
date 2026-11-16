package runner

import (
	"maps"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// costRun is a run with a gateway and a record ready for updateCost: what
// svcRun makes, plus the two fields updateCost reads.
func costRun(t *testing.T) *run {
	t.Helper()
	r := svcRun(t, "api-key", func(r *run) { r.gw = &gateway.Server{} })
	r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Status: runstore.StatusRunning}
	return r
}

// TestRunRecordsReportedBy (SA1.5): the record's reported_by is the gateway
// ledger's provider report, by serving model. It is the ledger's alone and
// not a sum of the stage reports, which is what keeps a call counted once
// (TestLateCallCountedOnce) and what makes the field the same statement
// model_by makes about the charge.
//
// The mutation it fails on is the field being left unwritten: a record with
// the report in the ledger and no reported_by in result.json is the gap this
// task closes (the red step).
func TestRunRecordsReportedBy(t *testing.T) {
	r := costRun(t)
	r.recordLedgerCost(gateway.Ledger{ReportedBy: map[string]pricing.Micros{
		"vendor/alpha": 1_250_000,
		"vendor/beta":  400_000,
	}})
	r.updateCost()

	c := r.rec.Cost
	if c == nil {
		t.Fatal("the record carries no cost breakdown")
	}
	want := map[string]float64{"vendor/alpha": 1.25, "vendor/beta": 0.4}
	if !maps.Equal(c.ReportedBy, want) {
		t.Fatalf("reported_by = %v, want the ledger's report %v by serving model", c.ReportedBy, want)
	}
}

// TestRunRecordsBandAndUnreported (SA1.5): the record's band is the ledger's
// table figures summed over the models that reported a cost, and
// unreported_calls is the ledger's count of the calls that reported none. The
// ends are the run's own, so a model the catalog prices at one bound rather
// than a range contributes the same figure to both.
func TestRunRecordsBandAndUnreported(t *testing.T) {
	r := costRun(t)
	r.recordLedgerCost(gateway.Ledger{
		ReportedBy: map[string]pricing.Micros{"vendor/alpha": 1_250_000},
		TableLoBy: map[string]pricing.Micros{
			"vendor/alpha": 1_000_000,
			"vendor/beta":  250_000,
		},
		TableHiBy: map[string]pricing.Micros{
			"vendor/alpha": 2_000_000,
			"vendor/beta":  250_000,
		},
		UnreportedCalls: 3,
	})
	r.updateCost()

	c := r.rec.Cost
	if c == nil {
		t.Fatal("the record carries no cost breakdown")
	}
	if c.TableLoUSD != 1.25 || c.TableHiUSD != 2.25 {
		t.Fatalf("the band = %v..%v, want 1.25..2.25 from the ledger's ends", c.TableLoUSD, c.TableHiUSD)
	}
	if c.UnreportedCalls != 3 {
		t.Fatalf("unreported_calls = %d, want the ledger's 3", c.UnreportedCalls)
	}
	if got := c.ReportedBy["vendor/alpha"]; got != 1.25 {
		t.Fatalf("reported_by[vendor/alpha] = %v, want 1.25 beside the band", got)
	}
}

// TestLateCallCountedOnce (SA1.5, F24): a call that settled after its stage's
// wait ran out is in the ledger's report and in no stage report, and the
// record carries it exactly once. The run's own accumulators here hold the
// stage's report alone -- the earlier, smaller figure -- so a runner that
// added the stage reports to the ledger's figures would write 2.40 rather
// than the ledger's 1.40.
//
// reported_usd keeps its own meaning and is untouched by this task: it is the
// stages' figure, which is why the two disagree on such a run.
func TestLateCallCountedOnce(t *testing.T) {
	r := costRun(t)
	r.reported = 1_000_000
	r.modelBy = map[string]pricing.Micros{"vendor/alpha": 1_000_000}
	r.recordLedgerCost(gateway.Ledger{
		ReportedBy:      map[string]pricing.Micros{"vendor/alpha": 1_400_000},
		TableLoBy:       map[string]pricing.Micros{"vendor/alpha": 1_100_000},
		TableHiBy:       map[string]pricing.Micros{"vendor/alpha": 1_600_000},
		UnreportedCalls: 1,
	})
	r.updateCost()

	c := r.rec.Cost
	if c == nil {
		t.Fatal("the record carries no cost breakdown")
	}
	if got := c.ReportedBy["vendor/alpha"]; got != 1.4 {
		t.Fatalf("reported_by[vendor/alpha] = %v, want the ledger's 1.40: the stage's report is not added to it", got)
	}
	if len(c.ReportedBy) != 1 {
		t.Fatalf("reported_by = %v, want one entry: the late call is not a second one", c.ReportedBy)
	}
	if c.TableLoUSD != 1.1 || c.TableHiUSD != 1.6 {
		t.Fatalf("the band = %v..%v, want the ledger's 1.10..1.60", c.TableLoUSD, c.TableHiUSD)
	}
	if c.UnreportedCalls != 1 {
		t.Fatalf("unreported_calls = %d, want the ledger's 1", c.UnreportedCalls)
	}
	if c.ReportedUSD != 1.0 {
		t.Fatalf("reported_usd = %v, want the stages' 1.00: that field keeps its meaning", c.ReportedUSD)
	}
}
