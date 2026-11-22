package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// runnerSpendGolden is the record the runner's own end-to-end test writes
// (SA1.4): one file with two readers, so a figure the gateway records and a
// reader drops shows up here rather than only in the half that wrote it. The
// CLI reads the file itself, byte for byte, and never a record this test built
// -- that is the whole point of sharing it.
var runnerSpendGolden = filepath.Join("..", "runner", "testdata", "spend_accuracy_result.golden.json")

// The figures the runner's fixture settles at, pinned here as well as in the
// runner test and in the golden. Pinning them is what makes this test an
// end-to-end one and not a tautology: compared against the file it also read,
// a field that never reached the file -- or one a reader drops on the way out
// -- would read as a zero on both sides and pass. Against a constant it reads
// as a zero and fails.
//
//   - the fixture makes three calls of the same usage, two reporting a cost
//     (0.0021 and 0.0004) and one reporting none, on a route with a 10% fee;
//   - the table's row for the model bands those two calls at 0.00067 to
//     0.00392, with no fee in the band, so it compares with the report;
//   - the charge is what the ledger settles under SA2.1: the provider's own cost
//     for the two calls that report one, and the policy's price for the third.
const (
	spendChargeUSD   = 0.004906
	spendReportedUSD = 0.0025
	spendTableLoUSD  = 0.00067
	spendTableHiUSD  = 0.00392
	spendUnreported  = 1
	spendCalls       = 3
	spendModel       = "deepseek/deepseek-v4-flash"
)

// TestReconcileReadsARealRecord (SA1.4, SA2.2) is the reading half of the pair
// whose writing half is internal/runner's TestSpendFieldsEndToEnd. It hands
// `fugaro diagnose` the record that test wrote and checks the figures the
// command prints are the ones the fixture settled at: the charge, the
// provider's report, the table's band over the calls that reported one, the
// count of the calls that did not, and the charge split by model -- the split
// a reconcile view puts a model's report beside.
//
// The plan names this test TestReconcileReadsARealRecord, for the two readers
// SA1.4 hands the file to: `budget reconcile` (SA1.3) and `diagnose` (SA2.2).
// SA1.3 is not on this tree, so the reader here is diagnose -- the one that
// prints these fields today -- and the same file is what reconcile reads when
// it lands. The figures are the record's either way, which is the half of
// SA1.4 that does not depend on reconcile existing.
func TestReconcileReadsARealRecord(t *testing.T) {
	golden, err := os.ReadFile(runnerSpendGolden)
	if err != nil {
		t.Fatal(err)
	}
	var rec runstore.Record
	if err := json.Unmarshal(golden, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Cost == nil {
		t.Fatal("the runner's record carries no cost block")
	}
	// The runner test asserts these of the live run it settled; here they are
	// asserted of the file, so a difference is a figure lost between the run
	// and the record.
	if !sameUSD(rec.CostUSD, spendChargeUSD) || !sameUSD(rec.Cost.ReportedUSD, spendReportedUSD) {
		t.Fatalf("the file's charge/report = %v/%v, want %v/%v",
			rec.CostUSD, rec.Cost.ReportedUSD, spendChargeUSD, spendReportedUSD)
	}
	if !sameUSD(rec.Cost.TableLoUSD, spendTableLoUSD) || !sameUSD(rec.Cost.TableHiUSD, spendTableHiUSD) {
		t.Fatalf("the file's band = $%v-$%v, want $%v-$%v",
			rec.Cost.TableLoUSD, rec.Cost.TableHiUSD, spendTableLoUSD, spendTableHiUSD)
	}
	if rec.Cost.UnreportedCalls != spendUnreported {
		t.Fatalf("the file's unreported_calls = %d, want %d", rec.Cost.UnreportedCalls, spendUnreported)
	}
	if !sameUSD(rec.Cost.ReportedBy[spendModel], spendReportedUSD) {
		t.Fatalf("the file's reported_by = %v, want %v against %s", rec.Cost.ReportedBy, spendReportedUSD, spendModel)
	}

	f := newCloudFixture(t)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	seedRun(t, f, rec.RunID, "", "someone@example.com", true)
	if err := runstore.Open(b, appSlug, rec.RunID).PutFile(ctx, "result.json", golden, "application/json"); err != nil {
		t.Fatal(err)
	}

	js, _, err := execute(t, "diagnose", "--json", rec.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	if !sameUSD(d.Charge, spendChargeUSD) {
		t.Errorf("charge_usd = %v, want %v", d.Charge, spendChargeUSD)
	}
	if !sameUSD(d.Reported, spendReportedUSD) {
		t.Errorf("reported_usd = %v, want %v", d.Reported, spendReportedUSD)
	}
	if !sameUSD(d.TableLoUSD, spendTableLoUSD) || !sameUSD(d.TableHiUSD, spendTableHiUSD) {
		t.Errorf("band = $%v-$%v, want $%v-$%v", d.TableLoUSD, d.TableHiUSD, spendTableLoUSD, spendTableHiUSD)
	}
	if d.UnreportedCalls != spendUnreported {
		t.Errorf("unreported_calls = %d, want %d", d.UnreportedCalls, spendUnreported)
	}
	// The per-model split is the figure a reconcile view reads a model's
	// report beside, so it must survive the reader as well.
	if !maps.Equal(d.ModelBy, rec.Cost.ModelBy) || !sameUSD(d.ModelBy[spendModel], spendChargeUSD) {
		t.Errorf("model_by = %v, want the record's %v over %s at %v", d.ModelBy, rec.Cost.ModelBy, spendModel, spendChargeUSD)
	}
	if !maps.Equal(d.Route, rec.Cost.RouteBy) {
		t.Errorf("route_by = %v, want the record's %v", d.Route, rec.Cost.RouteBy)
	}
	// The report sits inside the band the table prices those calls in, so this
	// is a provider that billed what the table expected and neither warning is
	// set: the file's real figure, not a record picked to warn.
	if d.BandWarning != "" || d.ReportedWarning != "" {
		t.Errorf("warnings = %q, %q, want none for a report inside the band", d.BandWarning, d.ReportedWarning)
	}

	// The human view prints the same facts off the same fields, and prints the
	// band instead of the charge comparison a bandless record gets (D2650,
	// F16).
	human, _, err := execute(t, "diagnose", rec.RunID)
	if err != nil {
		t.Fatal(err)
	}
	settled := fmt.Sprintf("Settled:  $%.4f (reported on %d of %d calls); table band $%.4f-$%.4f",
		spendChargeUSD, spendCalls-spendUnreported, spendCalls, spendTableLoUSD, spendTableHiUSD)
	if !strings.Contains(human, settled) {
		t.Errorf("diagnose lacks %q:\n%s", settled, human)
	}
	if strings.Contains(human, "Reported:") {
		t.Errorf("a record with a band printed the charge comparison:\n%s", human)
	}
}

// sameUSD compares two money figures at the precision a record round-trips:
// diagnose adds nothing to a record's dollar figures, so a difference here is
// a value a reader changed and not a floating-point tail.
func sameUSD(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
