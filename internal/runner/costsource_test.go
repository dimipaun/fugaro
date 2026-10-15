package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// A compute estimate that came from a price table naming itself records that
// line under the cost, so a reader can tell a VM estimate from Cloud Run's
// jobs list price and knows the boot disk and Cloud NAT are not in the
// figure (design compute-backend.md §5.2, C17). A cost with no source line
// renders exactly what it always did.
func TestCostLineNamesTheComputeSource(t *testing.T) {
	source := "Compute Engine VM list price, e2-standard family, us-central1 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"
	c := runstore.NewCost(4.12, 0.38, runstore.BasisAPIList)
	c.ComputeSource = source
	got := CostLine(c)
	if want := "**Cost:** ≈ $4.50 (model $4.12 + compute $0.38, estimate)\n**Compute source:** " + source; got != want {
		t.Errorf("CostLine = %q, want %q", got, want)
	}

	// Not estimated: there is no estimate for a source line to belong to,
	// even if a record carries one.
	only := runstore.ModelOnlyCost(4.12, runstore.BasisAPIList)
	only.ComputeSource = source
	if got := CostLine(only); strings.Contains(got, "Compute source") {
		t.Errorf("CostLine = %q, want no source line without an estimate", got)
	}

	// Cloud Run's list price records no source: its line is unchanged.
	if got, want := CostLine(runstore.NewCost(4.12, 0.38, runstore.BasisAPIList)), "**Cost:** ≈ $4.50 (model $4.12 + compute $0.38, estimate)"; got != want {
		t.Errorf("CostLine = %q, want %q", got, want)
	}
}
