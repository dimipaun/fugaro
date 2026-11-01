// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"

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

// rootReadDown puts the failure inside the ownership question, at its first
// read, the way slice 1's tests do with a transport that fails one request
// (internal/cli/ownership_test.go, rootReadDownTransport): the fake answers
// every call but the shallow root GET — infra.DB.Check's first read through
// rtdb.RootKeys — with a 503, which is an outage and not a refusal. It is that
// one request and not the whole database, because a database that answers
// nothing would fail the WRITE too, and the test could then not tell a gate
// that refused the write from a gate that was never asked: with the read down
// alone, a writer that skipped the question reaches the fake's own nodes and
// its record lands under /coordinator.
func rootReadDown(f *coordinatorReportFixture) {
	f.db.RefusePath("/.json", http.StatusServiceUnavailable, "UNAVAILABLE", "", "the database is unreachable")
}

// TestCoordinatorReportExitsTwoWhenTheDatabaseCannotBeRead is the split the
// report's writer owes its callers, the code slice 1's own outage tests hold
// for the commands (TestBudgetSetExitsTwoWhenTheDatabaseCannotBeRead): a
// database that is not this installation's is the operator's to fix and exits
// 1 (the refusals above), while a database that cannot be read at all is an
// outage and exits 2, so a script does not go and edit a config over a network
// that was down. Failing closed is the whole point: a question that cannot be
// answered is not an answer, and a report written on one is a record in a
// database nobody proved is this installation's.
func TestCoordinatorReportExitsTwoWhenTheDatabaseCannotBeRead(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	rootReadDown(f)

	_, _, err := execute(t, "coordinator", "report", "--repo", "acme/app",
		"--branch", "release/v0.11.0", "--file", "internal/cli/run.go")
	if err == nil {
		t.Fatal("coordinator report wrote while the database could not be read")
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Fatalf("coordinator report: exit %d, want 2 (an outage, not a refusal): %v", fcli.ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "nothing was reported") {
		t.Errorf("the outage does not say what was not done: %v", err)
	}
	if v := f.db.Value(coordpath.PathRoot); v != nil {
		t.Errorf("the failed report wrote under /%s: %v", coordpath.PathRoot, v)
	}
}

// TestDeclareFailsClosedWhenTheDatabaseCannotBeRead is the same outage at the
// launch declarer. The declaration is advisory and the caller turns a failure
// into a warning line (internal/cli/run.go), so this is not about the launch
// stopping: it is that a launch whose database could not be read must not
// declare into it on the strength of a question nobody answered. The class is
// held too — the outage comes back as one, and not as the refusal the caller
// drops without a word (a refusal is what a launcher who may not write gets,
// and it says nothing about a database that never answered).
func TestDeclareFailsClosedWhenTheDatabaseCannotBeRead(t *testing.T) {
	f := newCoordinatorReportFixture(t)
	rootReadDown(f)
	cloud := openFromConfig(t, f.cloudFixture)

	err := Declare(context.Background(), cloud, appSlug,
		&task.Spec{Version: 1, RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"})
	if err == nil {
		t.Fatal("the launch declarer declared while the database could not be read")
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Fatalf("the launch declarer: exit %d, want 2 (an outage, not a refusal): %v", fcli.ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "nothing was declared") {
		t.Errorf("the outage does not say what was not done: %v", err)
	}
	if v := f.db.Value(coordpath.PathRoot); v != nil {
		t.Errorf("the failed declaration wrote under /%s: %v", coordpath.PathRoot, v)
	}
}

// TestDeclareAsksNothingWhenTheCoordinatorIsOff: the launch's own switch, and
// the reason the gate above is asked where it is. A project with no
// budget.rtdb_url has no coordinator to declare to, and the launch must not pay
// for the question or fail on it — the gate sits behind the switch for that
// reason.
//
// The claim is carried by the nil error, and it is the fixture's shape that
// carries it: the config records budget.firebase_project (proj-1234) and no
// budget.rtdb_url, so the ownership question refuses it whichever half it is
// answered from — the config's own ("records a Firebase project … and no
// database URL"), and the client the gate builds before that cannot even name a
// URL. A Declare that asked the gate ahead of its switch therefore returns a
// refusal instead of declaring nothing, which is a warning line on every launch
// of a project that never adopted a backend, and this test goes red.
func TestDeclareAsksNothingWhenTheCoordinatorIsOff(t *testing.T) {
	f := &coordinatorReportFixture{cloudFixture: newCloudFixture(t), db: gcpfake.NewRTDB(t)}
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234 }\n")

	err := Declare(context.Background(), openFromConfig(t, f.cloudFixture), appSlug,
		&task.Spec{Version: 1, RunID: "20261024-120000-abcd", RequestedBy: "someone@example.com", Branch: "fugaro/x"})
	if err != nil {
		t.Fatalf("a declaration for a project with no database = %v, want nothing declared and no error", err)
	}
	if v := f.db.Value(coordpath.PathRoot); v != nil {
		t.Errorf("the declaration wrote under /%s: %v", coordpath.PathRoot, v)
	}
}
