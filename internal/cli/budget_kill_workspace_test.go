package cli

// The workspace scope of `fugaro budget kill` and `budget resume` (D1302,
// budget_kill_workspace.go).
//
// The switch it sets is the workspace's own service's stop flag, in the
// workspace's own database: the node the credentials service deployed in the
// workspace's own project reads with budget.ReadKills and refuses every call on
// (internal/credsvc/server.go's RTDBKills.Stop). So these tests hold the two
// halves of its scope at once. It reaches a member: a member's call that pays
// with the
// workspace's shared credentials is refused, and no member's operator can undo
// that, because the switch is in a database no member can write. And it reaches
// nobody else: no node of a member's own database, and no other workspace's.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// wsKillClient is a client of one fixture database, as the commands open one.
func wsKillClient(t *testing.T, db *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	client, err := newBudgetClient(context.Background(), db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// wsKillStop reads a workspace's stop flag the way the workspace's own service
// reads it (internal/credsvc/server.go's RTDBKills.Stop: budget.ReadKills
// against the database the deployment serves), so "the stop reached a member"
// is asserted through the read the refusal is made on and not through the raw
// node alone.
func wsKillStop(t *testing.T, db *gcpfake.RTDB, name string) (bool, string) {
	t.Helper()
	kills, err := budget.ReadKills(context.Background(), wsKillClient(t, db), name)
	if err != nil {
		t.Fatal(err)
	}
	return kills.On()
}

// wsKillSwitch is the switch a kill wrote, as the database holds it.
func wsKillSwitch(t *testing.T, db *gcpfake.RTDB) budget.Kill {
	t.Helper()
	var k budget.Kill
	found, err := wsKillClient(t, db).Get(context.Background(), budget.PathKillGlobal, &k)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("nothing was written at %s: the workspace was not stopped", budget.PathKillGlobal)
	}
	return k
}

// TestWorkspaceKillStopsAMemberRunInFlight is the red step: the scope did not
// exist (`--workspace` was an unknown flag). It is the test the task exists for
// — the stop is the one control that does not depend on a member's honesty
// (spec.md §8), so it is the control that has to be shown to reach a member.
func TestWorkspaceKillStopsAMemberRunInFlight(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable)
	member := h.add(t, "member-a", "member-a-gcp", "")
	before := member.db.Value("")
	wsDB := h.project(t, "workspace-alpha").db
	// The workspace's own database is one of the workspace's own project: the
	// name check every budget write makes reads this (checkDBProject), and the
	// test for a database that does not hold it is beside this one.
	wsDB.Set(budget.PathProject, "workspace-alpha")

	// The word asked for is the workspace's own name (confirmTyped compares
	// against the config the switch is written from), and a different word
	// writes nothing, as it does for --all.
	typed(t)
	if _, _, err := executeStdin(t, "aurora\n", "budget", "kill", "--workspace", "workspace-alpha", "--reason", "runaway spend"); err == nil {
		t.Fatal("the workspace kill accepted a word that is not the workspace's name")
	}
	if on, _ := wsKillStop(t, wsDB, "workspace-alpha"); on {
		t.Fatal("the refused confirmation killed the workspace")
	}

	out, _, err := executeStdin(t, "workspace-alpha\n", "budget", "kill", "--workspace", "workspace-alpha", "--reason", "runaway spend")
	if err != nil {
		t.Fatalf("budget kill --workspace: %v", err)
	}
	k := wsKillSwitch(t, wsDB)
	if !k.On || k.By != "person-1@example.com" || k.Reason != "runaway spend" ||
		k.At <= 0 || k.At > time.Now().UnixMilli() {
		t.Errorf("the workspace's switch is %+v, want it on, recorded by the operator, with the reason and the time", k)
	}
	// The refusal a member's shared-credential call meets is this read.
	if on, which := wsKillStop(t, wsDB, "workspace-alpha"); !on || which != "global" {
		t.Errorf("the workspace service's stop read is (on=%v, which=%q), want its own global switch on: every member's shared-credential call is refused on it", on, which)
	}
	if !strings.Contains(out, "workspace-alpha") || !strings.Contains(out, "resume --workspace workspace-alpha") {
		t.Errorf("the kill does not name the workspace and its undo:\n%s", out)
	}

	// Nothing of a member's own (spec.md §9): no node of its database was
	// written, so its credentials, its caps, its sign-in and the runs that never
	// reach the workspace are all untouched.
	if after := member.db.Value(""); !reflect.DeepEqual(before, after) {
		t.Errorf("the member's database changed:\nbefore %v\nafter  %v", before, after)
	}
	if on, which := wsKillStop(t, member.db, "member-a"); on {
		t.Errorf("the member's own stop read is on (%s): a workspace reached into a member", which)
	}

	// The undo is the same scope, in the same database.
	if _, _, err := execute(t, "budget", "resume", "--workspace", "workspace-alpha", "--yes"); err != nil {
		t.Fatalf("budget resume --workspace: %v", err)
	}
	if on, _ := wsKillStop(t, wsDB, "workspace-alpha"); on {
		t.Error("resume --workspace left the workspace's switch on")
	}
	if after := member.db.Value(""); !reflect.DeepEqual(before, after) {
		t.Errorf("the member's database changed across the resume:\nbefore %v\nafter  %v", before, after)
	}
}

// TestWorkspaceKillDoesNotStopAnotherWorkspace: the scope is one workspace and
// never the group of them nor the project this command happens to run in. Both
// ways it could be read as another scope are refused rather than resolved — a
// second scope flag, and the flags that name the project the command acts on —
// and a kill of one workspace leaves another's database byte for byte as it was.
//
// The named workspace and the project the command runs in are two different
// projects here, and that is the point of the fixture: --project is refused
// precisely because the selected project is not the scope, so a command that
// read the selected project's config instead of the named workspace's would
// write the wrong database and every other test in this file would still pass
// (the two are the same project there). This is also the ordinary use: a member
// runs this from its own checkout, where the selected project is its own.
func TestWorkspaceKillDoesNotStopAnotherWorkspace(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable)
	other := h.add(t, "workspace-beta", "ws-beta-gcp", "")
	selected := h.add(t, "other-project", "other-gcp", "")
	h.selectProject(t, "other-project")
	h.project(t, "workspace-alpha").db.Set(budget.PathProject, "workspace-alpha")
	before, selectedBefore := other.db.Value(""), selected.db.Value("")

	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha", "--all", "--yes"); err == nil ||
		!strings.Contains(err.Error(), "name one scope") {
		t.Errorf("--workspace with --all: %v, want a refusal naming the one scope", err)
	}
	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha", "--project", "workspace-beta", "--yes"); err == nil ||
		!strings.Contains(err.Error(), "--project") {
		t.Errorf("--workspace with --project: %v, want a refusal, not a write into whatever --project names", err)
	}
	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-nowhere", "--yes"); err == nil ||
		!strings.Contains(err.Error(), "workspace-nowhere") {
		t.Errorf("an unknown workspace: %v, want a refusal naming it", err)
	}
	if !reflect.DeepEqual(before, other.db.Value("")) ||
		!reflect.DeepEqual(selectedBefore, selected.db.Value("")) {
		t.Fatal("a refused kill wrote a database")
	}

	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha", "--yes", "--reason", "runaway spend"); err != nil {
		t.Fatalf("budget kill --workspace: %v", err)
	}
	if on, _ := wsKillStop(t, h.project(t, "workspace-alpha").db, "workspace-alpha"); !on {
		t.Error("the killed workspace's own switch is not on")
	}
	if after := selected.db.Value(""); !reflect.DeepEqual(selectedBefore, after) {
		t.Errorf("the kill wrote the project the command ran in (other-project), not the workspace it named:\nbefore %v\nafter  %v", selectedBefore, after)
	}
	if on, _ := wsKillStop(t, selected.db, "other-project"); on {
		t.Error("the selected project's own switch is on: the kill acted on the project the command ran in")
	}
	if after := other.db.Value(""); !reflect.DeepEqual(before, after) {
		t.Errorf("killing workspace-alpha wrote workspace-beta:\nbefore %v\nafter  %v", before, after)
	}
	if on, _ := wsKillStop(t, other.db, "workspace-beta"); on {
		t.Error("killing workspace-alpha stopped workspace-beta")
	}
}

// TestWorkspaceKillRefusesHTTP2Debug: every cloud command of this repository
// refuses to run while GODEBUG holds http2debug, because Go then prints
// requests — the person's ADC bearer token included — to stderr, and GODEBUG is
// read before main runs so it cannot be unset in time (refuseHTTP2Debug). The
// workspace scope resolves its own project instead of selecting one, so it
// never passes through openBudgetDB, which is where the project scopes get the
// refusal: without its own it is the one budget write that leaks the token.
func TestWorkspaceKillRefusesHTTP2Debug(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable)
	db := h.project(t, "workspace-alpha").db
	db.Set(budget.PathProject, "workspace-alpha")
	before := db.Value("")
	t.Setenv("GODEBUG", "http2debug=2")

	for _, verb := range []string{"kill", "resume"} {
		_, _, err := execute(t, "budget", verb, "--workspace", "workspace-alpha", "--yes")
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
			t.Errorf("budget %s --workspace under GODEBUG=http2debug: %v, want the refusal every cloud command makes: Go would print the person's bearer token to stderr", verb, err)
		}
	}
	if after := db.Value(""); !reflect.DeepEqual(before, after) {
		t.Errorf("a command that ran under GODEBUG=http2debug wrote the workspace's database:\nbefore %v\nafter  %v", before, after)
	}
}

// TestWorkspaceKillRefusesADatabaseThatIsNotTheWorkspaces is the second check,
// beside the ownership question: the database's own /fugaro/project has to be
// the workspace's name (checkDBProject). A workspace whose
// budget.firebase_project is its own gcp_project passes the ownership check on
// an empty, unmarked database, so without this one the stop is written into a
// database nothing proves is the workspace's — and the operator reads a success
// that stopped nothing.
func TestWorkspaceKillRefusesADatabaseThatIsNotTheWorkspaces(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable)
	db := h.project(t, "workspace-alpha").db

	// No /fugaro/project at all: the state the ownership check passes.
	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha", "--yes"); err == nil ||
		!strings.Contains(err.Error(), "/fugaro/project") {
		t.Errorf("a workspace database with no /fugaro/project: %v, want the refusal that it is not this project's budget database", err)
	}
	if db.Value(budget.PathKillGlobal) != nil {
		t.Error("the refused kill wrote the workspace's switch: the stop went into a database nothing proves is the workspace's")
	}

	// And a database that names another project, which is the same refusal with
	// the name check's own sentence.
	db.Set(budget.PathProject, "some-other-project")
	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha", "--yes"); err == nil ||
		!strings.Contains(err.Error(), "some-other-project") {
		t.Errorf("a workspace database belonging to another project: %v, want the refusal naming it", err)
	}
	if db.Value(budget.PathKillGlobal) != nil {
		t.Error("the refused kill wrote the workspace's switch")
	}

	// The workspace's own database, marked with its own name, goes through:
	// the two checks are refusals of the wrong database and not of this scope.
	db.Set(budget.PathProject, "workspace-alpha")
	if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha", "--yes"); err != nil {
		t.Fatalf("budget kill --workspace on the workspace's own database: %v", err)
	}
	if on, _ := wsKillStop(t, db, "workspace-alpha"); !on {
		t.Error("the workspace's own database was not stopped")
	}
}
