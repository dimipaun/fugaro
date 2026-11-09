package cli

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runview"
)

// lsGroupings are the groupings --group-by accepts: a closed list, refused by
// slices.Contains the way --state refuses anything outside lsStates. One value
// today -- the failure class -- and the list is where the second one goes.
var lsGroupings = []string{"failure_class"}

// lsGroup is one row of --group-by's block: a class of the closed vocabulary,
// how many of the window's runs ended in it, that count's share **of the
// window's runs** (D1465: never a share of its spend, which is the column
// beside it), and what those runs cost.
//
// The four keys are the whole block: the design's sketch also names the class's
// kind and properties, and they are left out because they are the vocabulary's
// own and a reader already has them -- failure.PropertiesOf answers them from
// the class alone, with no second copy of the table in a wire format.
type lsGroup struct {
	Class    failure.Class `json:"class"`
	Runs     int           `json:"runs"`
	Share    float64       `json:"share"`
	SpendUSD float64       `json:"spend_usd"`
}

// groupRows groups the rows the listing has already read -- no second read, no
// network path of its own -- into one lsGroup per class of failure.Classes(),
// in the vocabulary's own order, so two readers of one window print the same
// block. A class no run of the window ended in is left out rather than printed
// as a zero row, which would read as a class that happened and cost nothing.
//
// Which runs belong to a group is the row's FailureClass and nothing else, and
// that field is the record's own class or -- for a record written before the
// field shipped -- the reader's reading of its reason (runview.ClassOf, FC5), so
// a window is never split into before and after the field shipped. A run that
// did not fail carries no class and belongs to no group: the block accounts for
// the runs that failed, and the totals line beside it for the window.
func groupRows(rows []runview.Row) []lsGroup {
	counted := map[failure.Class]*lsGroup{}
	for _, r := range rows {
		if r.FailureClass == "" {
			continue
		}
		g := counted[r.FailureClass]
		if g == nil {
			// The in-vocabulary classes are counted by the walk below; a value
			// the record named and this build does not know reads as
			// unclassified through ClassOf, so this map holds no class the
			// vocabulary does not (internal/runview, knownClass).
			g = &lsGroup{Class: r.FailureClass}
			counted[r.FailureClass] = g
		}
		g.Runs++
		g.SpendUSD += r.Cost.TotalUSD
	}
	groups := make([]lsGroup, 0, len(counted))
	for _, class := range failure.Classes() {
		if g, ok := counted[class]; ok {
			groups = append(groups, *g)
		}
	}
	// A share of the window's *runs*: the runs the listing read, which are the
	// totals line's runs, so the shares and the totals are read off one
	// denominator. Rounded to four decimals -- a tenth of a percent's tenth,
	// finer than the table prints -- so a share is a figure two readers agree
	// on rather than a float's own tail.
	for i := range groups {
		groups[i].Share = math.Round(float64(groups[i].Runs)/float64(len(rows))*1e4) / 1e4
	}
	return groups
}

// printGroupsTable prints --group-by's block under the listing: one row per
// class, by runs descending and ties by class name (design §6.1), so the
// biggest cause of the window is its first row. The block the document carries
// keeps the vocabulary's order instead: a wire shape is read by name, not
// scanned, and the vocabulary's order is the one the report and the schema are
// built from (internal/failure).
func printGroupsTable(w io.Writer, groups []lsGroup) error {
	sorted := slices.Clone(groups)
	slices.SortFunc(sorted, func(a, b lsGroup) int {
		if a.Runs != b.Runs {
			return b.Runs - a.Runs
		}
		return strings.Compare(string(a.Class), string(b.Class))
	})
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CLASS\tRUNS\tSHARE\tSPEND")
	for _, g := range sorted {
		fmt.Fprintf(tw, "%s\t%d\t%s\t$%.2f\n", oneLine(string(g.Class)), g.Runs, sharePercent(g.Share), g.SpendUSD)
	}

	return tw.Flush()
}

// sharePercent renders a share as the percentage the table prints: a whole
// number when the share is one -- 75%, not 75.0% -- and one decimal otherwise.
func sharePercent(share float64) string {
	p := share * 100
	if p == math.Trunc(p) {
		return fmt.Sprintf("%.0f%%", p)
	}
	return fmt.Sprintf("%.1f%%", p)
}
