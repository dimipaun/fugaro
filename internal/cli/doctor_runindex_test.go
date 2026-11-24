package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// runIndexRig is doctorPolicyRig with the installation's own Firestore: the
// local config names the fake (endpoints.firestore) and the Firebase project
// the index lives in (budget.firebase_project), and an IAM fake keeps the
// signers check a named Firebase project switches on off the network.
func runIndexRig(t *testing.T) (*doctorRig, *gcpfake.Firestore) {
	t.Helper()
	r, db := doctorPolicyRig(t, doctorPolicy(nil), "")
	fs, iam := gcpfake.NewFirestore(t), gcpfake.NewIAM(t)
	cfg := strings.Replace(r.cfg, "endpoints: { ", "endpoints: { iam: "+iam.URL+"/, firestore: "+fs.URL+"/, ", 1)
	cfg = strings.Replace(cfg, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+" }",
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: "+db.URL+" }", 1)
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	return r, fs
}

// runIndexSummaryDoc is the summary document as the history job writes it
// (contract-index.md): the two facts the check reads, and the fields the
// contract carries beside them, so the decode under test is the real one and
// not a document only this test would write.
func runIndexSummaryDoc(complete bool, folded time.Time) map[string]any {
	return map[string]any{
		"version": runindex.Version, "complete": complete, "folded_through": folded,
		"updated_at": folded, "runs": int64(42),
		"states":          map[string]any{"succeeded": int64(40), "failed": int64(2)},
		"spent_micros":    int64(1_500_000),
		"notional_micros": int64(0),
		"compute_runs":    int64(41),
		"estimate":        map[string]any{"schema_version": 1, "written_at": folded, "groups": []any{}},
	}
}

// TestDoctorNamesReindexWhenIncomplete is RI6 (F9): doctor reads the index's
// summary document and, when the index is not keeping up — a summary that is
// not complete, or one whose last fold is older than the days the budget
// database keeps — prints one line naming `fugaro runs reindex`. The states
// that are not a finding are silence, and they are three: a summary that is
// complete with a stamp the retention still covers, an installation whose index
// has never been built at all (the state of every installation until the fold
// lands, RI4), and a summary the retention's own last day still covers. A
// summary doctor could not read is neither silence nor a finding: it is a
// counted line saying the fact is unknown.
func TestDoctorNamesReindexWhenIncomplete(t *testing.T) {
	// The clock the checks read is the rig's pinned one (pinCatalogClock, in
	// newDoctorRig) and not the wall clock, so the stamps below are built from
	// it here as well: a stamp the wall clock produced drifts one day older
	// every real day against the pinned clock, and the two states about a stamp
	// older than the retention stop being that once the drift passes the
	// retention itself.
	pinCatalogClock(t, shippedCatalog(t), 24*time.Hour)
	now := budgetNow()
	stale := now.Add(-(budget.PruneAfter + 1) * 24 * time.Hour)
	for _, tc := range []struct {
		name string
		seed map[string]any
		// refuse makes the fake answer the summary read with 503, the way an
		// unreachable Firestore does.
		refuse bool
		// want is what the state is: "line" for the finding, "unread" for the
		// read that did not answer, and "" for silence.
		want    string
		problem []string
	}{
		{name: "a walk that has not reached the end of history",
			seed: runIndexSummaryDoc(false, now.Add(-time.Hour)), want: "line",
			problem: []string{"incomplete", "42 runs"}},
		{name: "a fold older than the database's retention",
			seed: runIndexSummaryDoc(true, stale), want: "line",
			problem: []string{stale.Format("2006-01-02"), fmt.Sprintf("older than the %d days", budget.PruneAfter), "42 runs"}},
		{name: "a summary that is not complete and long stale",
			seed: runIndexSummaryDoc(false, stale), want: "line",
			problem: []string{"incomplete", "stopped keeping up"}},
		{name: "a fold the retention still covers",
			seed: runIndexSummaryDoc(true, now.Add(-budget.PruneAfter*24*time.Hour))},
		{name: "a summary folded through minutes ago",
			seed: runIndexSummaryDoc(true, now.Add(-3*time.Minute))},
		{name: "an installation whose index has never been built"},
		{name: "a summary doctor could not read", refuse: true, want: "unread",
			problem: []string{"could not be checked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, fs := runIndexRig(t)
			if tc.refuse {
				fs.RefusePath(runindex.SummaryCollection, 503, "UNAVAILABLE", "", "the service is unavailable")
			}
			if tc.seed != nil {
				fs.Set(runindex.SummaryCollection, runindex.SummaryDocument, tc.seed)
			}
			c, ok := doctorChecksByID(t)[runIndexIncompleteCheckID]
			if !ok {
				if tc.want != "" {
					t.Fatalf("doctor said nothing about the index in the state %q", tc.name)
				}
				return
			}
			if tc.want == "" {
				t.Fatalf("doctor reported the index where there is nothing to say: %+v", c)
			}
			if c.OK || c.Severity != "info" {
				t.Fatalf("the index line is %+v, want an info line: an index still building is not something wrong, and a warning would fail doctor --strict for every young installation", c)
			}
			if checksFail([]doctorCheck{c}, true) {
				t.Error("the index line fails doctor --strict")
			}
			for _, want := range tc.problem {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("the line does not name %q: %s", want, c.Problem)
				}
			}
			if tc.want == "unread" {
				// An unknown is counted rather than printed (doctorClassOf): the
				// line says the fact is unknown, and no view calls it a measurement.
				if c.Class != doctorClassAudit {
					t.Errorf("an unreadable summary is classed %q, want the counted class", c.Class)
				}
				return
			}
			if !strings.Contains(c.Fix, "fugaro runs reindex") {
				t.Errorf("the fix does not name the rebuild: %q", c.Fix)
			}
			if c.Class != doctorClassActionable {
				t.Errorf("the index line is classed %q, so the default view hides the one command that fixes it", c.Class)
			}
			if out := doctorDefaultOutput(t, []doctorCheck{c}); !strings.Contains(out, "fugaro runs reindex") {
				t.Errorf("the default view does not print the rebuild: %q", out)
			}
		})
	}
}
