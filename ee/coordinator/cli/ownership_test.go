// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// ownership_test.go is slice 2 of the owner's ruling of 2026-10-31
// (docs/design/upgrade.md, "Every writer into that database"): the
// coordinator's two writers — `fugaro coordinator report` and the launch
// declarer — write into the same database the budget commands write into, and
// they ask the ONE ownership question every writer there asks
// (budgetDBUnadopted, through fcli.RequireOwnBudgetDB), never a second copy of
// its two halves.
//
// Each writer is driven the same way twice. First against the ordinary
// installation (newCoordinatorReportFixture: budget.firebase_project recorded
// beside budget.rtdb_url, and the database carrying the mark `fugaro update
// --firebase-project` writes), which is unchanged — the record is written.
// Then against a database another installation marked, and against one that
// holds data and no mark at all, which is what a coordinator's subtree is after
// the first declaration has landed beside an absent mark: both are refused
// before anything under /coordinator is written, with the sentence the core's
// commands refuse with and what that writer would have done.
//
// The config's own half of the question is held too. It is the half a check
// written for this tree — the mark, and nothing else — would miss, and it is
// answered without the database being asked anything, so a project that records
// a database and no Firebase project is refused before this command contacts
// one, in the words `fugaro update` skips its stages with.

// anotherMark is the mark of a second installation: the fields of the one
// `fugaro update --firebase-project` writes with both names changed, which is
// what a database another Fugaro project wrote carries.
func anotherMark() map[string]any {
	return map[string]any{"managed_by": "fugaro", "version": 1, "project": "birch", "gcp_project": "proj-9999"}
}

// refusedByTheOwnershipCheck fails the test unless err is the shared refusal:
// non-nil, a user error (exit 1, not an outage), and ending on what the writer
// would have done. It is one helper so both writers are held to the one
// sentence rather than to a message of their own.
func refusedByTheOwnershipCheck(t *testing.T, what string, err error, declined string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s wrote into a database that is not this installation's", what)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Fatalf("%s: exit %d, want 1 (a refusal, not an outage): %v", what, fcli.ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), declined) {
		t.Fatalf("%s: the refusal does not say what was not done (%q): %v", what, declined, err)
	}
}

// refusalsOfTheMark are the two shapes of a database that is not this
// installation's, as the ownership question tells them apart: the mark of
// another Fugaro project, and data with no mark at all (which is every database
// a declaration has already been written into, whatever else it holds).
var refusalsOfTheMark = []struct {
	what   string
	seed   func(f *coordinatorReportFixture)
	detail string
}{
	{
		what:   "another installation's mark",
		seed:   func(f *coordinatorReportFixture) { f.db.Set(budget.PathMark, anotherMark()) },
		detail: "one Firebase project serves one installation",
	},
	{
		what:   "data and no mark",
		seed:   func(f *coordinatorReportFixture) { f.db.Set(budget.PathMark, nil) },
		detail: "and no Fugaro mark",
	},
}

// rewriteBudgetBlock replaces the fixture's budget block with block. The
// fixture writes the pair `fugaro update --firebase-project` records, so a test
// that needs the config to record something else says so here.
func rewriteBudgetBlock(t *testing.T, f *coordinatorReportFixture, block string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	was := "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + f.db.URL + " }\n"
	if !strings.Contains(string(data), was) {
		t.Fatalf("the config no longer carries the budget block this rewrites:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), was, block, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCoordinatorReportRefusesAnotherInstallationsDatabase: the report path's
// half of the ruling. `fugaro coordinator report` writes one record under
// /coordinator through the caller's own credential, and before this slice it
// asked nothing about the database beyond `budget.rtdb_url` being set — so a
// project config naming another installation's database reported into it.
func TestCoordinatorReportRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	args := []string{"coordinator", "report", "--repo", "acme/app",
		"--branch", "release/v0.11.0", "--file", "internal/cli/run.go"}

	// The installation's own marked database: unchanged, the record is written.
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("a report into the installation's own marked database was refused: %v", err)
	}
	if n := len(f.intents()); n != 1 {
		t.Fatalf("the report wrote %d records, want 1", n)
	}
	own := f.db.Value(coordpath.PathRoot)

	for _, tc := range refusalsOfTheMark {
		tc.seed(f)
		_, _, err := execute(t, args...)
		refusedByTheOwnershipCheck(t, "coordinator report", err, "nothing was reported")
		if err != nil && !strings.Contains(err.Error(), tc.detail) {
			t.Errorf("%s: the refusal is not the shared one (%s): %v", tc.what, tc.detail, err)
		}
		if !reflect.DeepEqual(f.db.Value(coordpath.PathRoot), own) {
			t.Fatalf("%s: the refused report wrote under /%s: %v, want %v", tc.what, coordpath.PathRoot, f.db.Value(coordpath.PathRoot), own)
		}
	}

	// The config's own half: a project that records the database and no
	// Firebase project records no shared budget backend at all, and the
	// sentence it is refused with is the one `fugaro update` skips a stage
	// with — its own remedy named, and no database contacted to reach it.
	f.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": "aurora", "gcp_project": "proj-1234"})
	rewriteBudgetBlock(t, f, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+f.db.URL+" }\n")
	_, _, err := execute(t, args...)
	if err == nil {
		t.Fatal("the report ran while its config recorded no Firebase budget backend")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Fatalf("coordinator report: exit %d, want 1 (a config fact the operator fixes): %v", fcli.ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "records no shared Firebase budget backend") ||
		!strings.Contains(err.Error(), "fugaro update --firebase-project proj-1234") {
		t.Errorf("the refusal is not the config half's own sentence and remedy: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(coordpath.PathRoot), own) {
		t.Errorf("the refused report wrote under /%s: %v, want %v", coordpath.PathRoot, f.db.Value(coordpath.PathRoot), own)
	}
}

// TestDeclareRefusesAnotherInstallationsDatabase: the launch declarer's half.
// It is the writer a run calls before the run starts (internal/cli's
// declareLaunch), so a declaration made on the strength of a typed URL is a
// record this installation's coordinator never reads, written into a database
// another installation owns.
func TestDeclareRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	cloud := openFromConfig(t, f.cloudFixture)
	spec := &task.Spec{Version: 1, RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"}

	// The installation's own marked database: unchanged, the launch declares.
	if err := Declare(context.Background(), cloud, appSlug, spec); err != nil {
		t.Fatalf("a declaration into the installation's own marked database was refused: %v", err)
	}
	if n := len(f.intents()); n != 1 {
		t.Fatalf("the declaration wrote %d records, want 1", n)
	}
	own := f.db.Value(coordpath.PathRoot)

	for _, tc := range refusalsOfTheMark {
		tc.seed(f)
		err := Declare(context.Background(), cloud, appSlug, spec)
		refusedByTheOwnershipCheck(t, "the launch declarer", err, "nothing was declared")
		if err != nil && !strings.Contains(err.Error(), tc.detail) {
			t.Errorf("%s: the refusal is not the shared one (%s): %v", tc.what, tc.detail, err)
		}
		if !reflect.DeepEqual(f.db.Value(coordpath.PathRoot), own) {
			t.Fatalf("%s: the refused declaration wrote under /%s: %v, want %v", tc.what, coordpath.PathRoot, f.db.Value(coordpath.PathRoot), own)
		}
	}
}

// TestDeclareAsksNothingWhenTheCoordinatorIsOff: the launch's own switch, and
// the reason the gate above is asked where it is. A project with no
// budget.rtdb_url has no coordinator to declare to, and the launch must not pay
// for the question or fail on it: no store is opened (which is what the opener
// below makes visible), nothing is declared, and the launch goes on. The gate
// sits behind the store's own refusal for that reason — a launch that asked it
// first would answer "the project config records no shared Firebase budget
// backend" instead of declaring nothing, which is a warning line on every
// launch of a project that never adopted a budget backend.
func TestDeclareAsksNothingWhenTheCoordinatorIsOff(t *testing.T) {
	f := &coordinatorReportFixture{cloudFixture: newCloudFixture(t), db: gcpfake.NewRTDB(t)}
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234 }\n")
	prev := openCoordinatorStore
	opened := false
	openCoordinatorStore = func(context.Context, *localcfg.Config) (*coordinator.Store, error) {
		opened = true
		return nil, fcli.UserErr("the store was opened")
	}
	t.Cleanup(func() { openCoordinatorStore = prev })

	err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug,
		&task.Spec{Version: 1, RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"})
	if err != nil {
		t.Fatalf("a declaration for a project with no database = %v, want nothing declared and no error", err)
	}
	if opened {
		t.Error("the declaration opened the database of a project that has none")
	}
	if v := f.db.Value(coordpath.PathRoot); v != nil {
		t.Errorf("the declaration wrote under /%s: %v", coordpath.PathRoot, v)
	}
}
