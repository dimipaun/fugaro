package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// lsGroupWire is --group-by's block as the document carries it. The test reads
// the wire shape rather than the Go type, because the wire shape is what the
// console, the phone and console/docs/json-contract.md are about.
type lsGroupWire struct {
	Class    string  `json:"class"`
	Runs     int     `json:"runs"`
	Share    float64 `json:"share"`
	SpendUSD float64 `json:"spend_usd"`
}

// groupDoc is the two halves of a grouped listing's document: the rows every
// listing carries, and the block --group-by adds beside them.
type groupDoc struct {
	Rows   []runview.Row  `json:"runs"`
	Totals runview.Totals `json:"totals"`
	Groups []lsGroupWire  `json:"groups"`
}

// groupJSON runs `runs ls --json --group-by` and answers the document decoded,
// and its raw bytes for the golden and the shape checks.
func groupJSON(t *testing.T, args ...string) (groupDoc, string) {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "ls", "--json", "--group-by", "failure_class"}, args...)...)
	if err != nil {
		t.Fatalf("runs ls --group-by: %v (%s)", err, errOut)
	}
	var got groupDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got, out
}

// seedClassedRun seeds one run whose record names class and spent spend, so a
// window can hold a class with the runs and the money a test needs.
func seedClassedRun(t *testing.T, f *cloudFixture, id string, class failure.Class, spend float64) {
	t.Helper()
	seedRun(t, f, id, "", "someone@example.com", false)
	c := runstore.NewCost(spend-0.5, 0.5, runstore.BasisAPIList)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusFailed, Stage: "verify", Reason: "the run did not finish its work",
		FailureClass: class, CostUSD: spend, Cost: &c})
}

// TestRunsGroupByFailureClassNamesEveryClass: one run of every class of the
// vocabulary is one group per class, in failure.Classes()'s order -- the order
// the vocabulary is written in, so two readers of one window print the same
// table -- and the groups' runs add up to the window's, because every run of
// this window failed and a run that failed belongs to its class's group.
func TestRunsGroupByFailureClassNamesEveryClass(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	for i, class := range failure.Classes() {
		seedClassedRun(t, f, recentRunID(t, time.Duration(i+1)*time.Minute, fmt.Sprintf("%04x", i)), class, 1)
	}

	got, _ := groupJSON(t, "--since", "0")
	if len(got.Groups) != len(failure.Classes()) {
		t.Fatalf("--group-by named %d classes, want the vocabulary's %d: %+v", len(got.Groups), len(failure.Classes()), got.Groups)
	}
	runs := 0
	for i, g := range got.Groups {
		if want := string(failure.Classes()[i]); g.Class != want {
			t.Errorf("group %d is %q, want %q: the block is in the vocabulary's order", i, g.Class, want)
		}
		runs += g.Runs
	}
	if runs != len(got.Rows) {
		t.Errorf("the groups' runs are %d of the window's %d, want every failed run counted once", runs, len(got.Rows))
	}
	// The block is additive: the rows every listing carries are still there.
	if len(got.Rows) != len(failure.Classes()) || got.Totals.Runs != len(failure.Classes()) {
		t.Errorf("the grouped document lists %d rows and totals %d, want the window's %d runs",
			len(got.Rows), got.Totals.Runs, len(failure.Classes()))
	}
}

// TestRunsGroupByRefusesAnotherValue: the flag's values are a closed list, the
// shape --state's is, and the refusal names the one value it takes rather than
// listing nothing.
func TestRunsGroupByRefusesAnotherValue(t *testing.T) {
	newCloudFixture(t)
	_, _, err := execute(t, "runs", "ls", "--group-by", "reason")
	if err == nil || !strings.Contains(err.Error(), `"reason"`) || !strings.Contains(err.Error(), "failure_class") {
		t.Fatalf("runs ls --group-by reason: err = %v, want a refusal naming the value it takes", err)
	}
}

// TestRunsGroupByShareIsRunsNotSpend: the share column is a share of the
// window's runs, never of its spend (D1465). The window is built so the two
// readings disagree -- one expensive class against three cheap ones -- and the
// spend is beside the runs rather than folded into the figure beside it.
func TestRunsGroupByShareIsRunsNotSpend(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	seedClassedRun(t, f, recentRunID(t, time.Minute, "aaaa"), failure.TestsFailed, 10)
	for i, hex := range []string{"bbbb", "cccc", "dddd"} {
		seedClassedRun(t, f, recentRunID(t, time.Duration(i+2)*time.Minute, hex), failure.BranchMoved, 1)
	}

	got, _ := groupJSON(t, "--since", "0")
	byClass := map[string]lsGroupWire{}
	for _, g := range got.Groups {
		byClass[g.Class] = g
	}
	moved, failed := byClass[string(failure.BranchMoved)], byClass[string(failure.TestsFailed)]
	if moved.Runs != 3 || failed.Runs != 1 {
		t.Fatalf("the groups are %+v, want three branch_moved runs against one tests_failed", got.Groups)
	}
	if moved.Share != 0.75 || failed.Share != 0.25 {
		t.Errorf("the shares are %v and %v, want 0.75 and 0.25: share is a share of the window's runs", moved.Share, failed.Share)
	}
	if failed.SpendUSD <= moved.SpendUSD {
		t.Errorf("the spends are $%.2f and $%.2f, want the one-run class the dearer: the spend is a column of its own",
			failed.SpendUSD, moved.SpendUSD)
	}
	human, _, err := execute(t, "runs", "ls", "--since", "0", "--group-by", "failure_class")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "75%") || !strings.Contains(human, "$10.00") {
		t.Errorf("the printed table does not carry the runs' share (75%%) and the spend ($10.00):\n%s", human)
	}
}

// TestWatchDocumentCarriesNoGroups: the degraded `watch --json` document
// embeds ls's own document, and the phone refuses an unknown key in it -- of
// the whole document, not of the key -- so a groups block the watch path never
// asked for is the app rendering degraded-as-nothing (F14, D1458).
func TestWatchDocumentCarriesNoGroups(t *testing.T) {
	f := newCloudFixture(t) // no budget backend: the degraded document
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	// A failed run, so the block would be non-empty if the watch path emitted
	// one: absence is the shape, not an empty window.
	seedClassedRun(t, f, recentRunID(t, time.Minute, "aaaa"), failure.TestsFailed, 1)

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []string{"degraded", "notice", "project", "runs", "totals", "warnings"}
	if got := keysOf(t, doc); !slices.Equal(got, want) {
		t.Errorf("watch --json's degraded document carries %v, want %v: groups is the one key --group-by may add, and the phone reads this shape strictly", got, want)
	}
	if strings.Contains(out, `"groups"`) {
		t.Errorf("watch --json carries a groups block:\n%s", out)
	}
}

var updateRunsGroupGolden = flag.Bool("update-runs-group", false, "rewrite testdata/runs_group.golden")

// TestRunsGroupByGolden pins the whole grouped listing -- the table a person
// reads and the block the console reads -- for a window of two classes, so a
// column, an order or a rounding that moved is a diff rather than a reading.
func TestRunsGroupByGolden(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	seedClassedRun(t, f, recentRunID(t, 3*time.Minute, "aaaa"), failure.TestsFailed, 4)
	seedClassedRun(t, f, recentRunID(t, 2*time.Minute, "bbbb"), failure.NoCommits, 1)
	seedClassedRun(t, f, recentRunID(t, time.Minute, "cccc"), failure.NoCommits, 0.5)

	human, _, err := execute(t, "runs", "ls", "--since", "0", "--group-by", "failure_class")
	if err != nil {
		t.Fatal(err)
	}
	_, raw := groupJSON(t, "--since", "0")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	var groups bytes.Buffer
	if err := json.Indent(&groups, doc["groups"], "", "  "); err != nil {
		t.Fatalf("the document carries no groups block: %v: %s", err, raw)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== runs ls --group-by failure_class\n%s\n--- groups\n%s\n", human, groups.String())

	path := filepath.Join("testdata", "runs_group.golden")
	if *updateRunsGroupGolden {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file: run go test ./internal/cli -run TestRunsGroupByGolden -update-runs-group")
	}
	if string(want) != b.String() {
		t.Fatalf("the grouped listing changed (go test -update-runs-group to accept):\n%s\nwant:\n%s", b.String(), want)
	}
}
