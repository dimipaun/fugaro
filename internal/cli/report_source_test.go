package cli

import (
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// The report's own source line names the price table each run's compute
// estimate came from, once per distinct table, and says nothing at all when
// no row carries one -- which is every run priced with Cloud Run's list
// price, so those reports read exactly as they did (design
// compute-backend.md §5.2, C17).
func TestReportComputeSources(t *testing.T) {
	vm := "Compute Engine VM list price, e2-standard family, us-central1 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"
	row := func(src string) runview.Row {
		return runview.Row{Cost: runstore.Cost{ComputeSource: src}}
	}
	for _, tc := range []struct {
		name string
		rows []runview.Row
		want []string
	}{
		{"none", []runview.Row{row(""), row("")}, nil},
		{"one", []runview.Row{row(vm), row(""), row(vm)}, []string{vm}},
		{"two, sorted", []runview.Row{row("b"), row("a"), row("b")}, []string{"a", "b"}},
	} {
		if got := computeSources(tc.rows); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: computeSources = %q, want %q", tc.name, got, tc.want)
		}
	}
}
