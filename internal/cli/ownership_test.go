package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// ownership_test.go is the CLI half of the owner's ruling of 2026-10-31: ONE
// shared ownership check covers every config-driven budget-database write path
// (docs/design/upgrade.md, "Every writer into that database"). `fugaro update`'s
// rules and policy stages asked it already (budgetDBUnadopted); these tests hold
// every command that writes the database to the same question, and to the same
// answer.
//
// Each test does the same two things: the installation's own marked database is
// written to as before, and then the database is handed to another installation
// (its mark) or emptied of the mark while holding data, and the command is
// refused with the shared sentence, naming what was not done.

// installMark is the mark `fugaro update --firebase-project` writes into the
// installation's own database (infra.DB.Plan): the Fugaro project's name and the
// GCP project it lives in. Every fixture here seeds it, because that is what
// makes a database this installation's at all.
func installMark(project, gcpProject string) map[string]any {
	return map[string]any{"managed_by": "fugaro", "version": 1, "project": project, "gcp_project": gcpProject}
}

// auroraMark is installMark for the fixtures' installation: the one every
// fixture's config names (name aurora, gcp_project proj-1234).
func auroraMark() map[string]any { return installMark("aurora", "proj-1234") }

// birchMark is that mark with both names changed: a database another
// installation wrote, which no command of this one may touch.
func birchMark() map[string]any { return installMark("birch", "proj-9999") }

// refusedByTheOwnershipCheck fails the test unless err is the shared refusal:
// the check's own words (the mark it found, or the data and the absent mark) and
// the word for what was not done. It is one helper so every door is held to the
// one sentence rather than to a message of its own.
func refusedByTheOwnershipCheck(t *testing.T, what string, err error, declined string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s wrote into a database that is not this installation's", what)
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("%s: exit %d, want 1 (a refusal, not an outage): %v", what, ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), declined) {
		t.Errorf("%s: the refusal does not say what was not done (%q): %v", what, declined, err)
	}
}

// TestBudgetSetRefusesAnotherInstallationsDatabase: `fugaro budget set` is a
// writer by definition — its whole job is a cap — and it asks the one ownership
// check before it reads, plans or writes anything.
func TestBudgetSetRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	// The installation's own marked database: unchanged.
	if out, _, err := execute(t, "budget", "set", "--global", "--daily", "60"); err != nil {
		t.Fatalf("budget set on the installation's own marked database was refused: %v\n%s", err, out)
	}
	own := f.db.Value(nodeCG)

	// Another installation's mark.
	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "budget", "set", "--global", "--daily", "50")
	refusedByTheOwnershipCheck(t, "budget set", err, "nothing was set")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one (the mark it found): %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(nodeCG), own) {
		t.Errorf("the refused set wrote the ceiling: %v, want %v", f.db.Value(nodeCG), own)
	}

	// Data and no mark at all: the other half of the same answer.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "budget", "set", "--global", "--daily", "50")
	refusedByTheOwnershipCheck(t, "budget set", err, "nothing was set")
	if err != nil && !strings.Contains(err.Error(), "and no Fugaro mark") {
		t.Errorf("the refusal does not name the data and the absent mark: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(nodeCG), own) {
		t.Errorf("the refused set wrote the ceiling: %v, want %v", f.db.Value(nodeCG), own)
	}
}

// TestBudgetKillRefusesAnotherInstallationsDatabase: the kill switch is the
// other writer `fugaro budget` carries, and it holds the same gate — a switch
// thrown in another installation's database halts runs that are not this
// project's.
func TestBudgetKillRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes", "--reason", "maintenance"); err != nil {
		t.Fatalf("budget kill on the installation's own marked database was refused: %v", err)
	}
	own := f.db.Value(budget.PathKillGlobal)

	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "budget", "kill", "--all", "--yes", "--reason", "maintenance")
	refusedByTheOwnershipCheck(t, "budget kill", err, "nothing was killed")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(budget.PathKillGlobal), own) {
		t.Errorf("the refused kill wrote the switch: %v, want %v", f.db.Value(budget.PathKillGlobal), own)
	}

	// Data and no mark at all: the other half of the same answer.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "budget", "resume", "--all", "--yes", "--reason", "maintenance")
	refusedByTheOwnershipCheck(t, "budget resume", err, "nothing was resumed")
	if !reflect.DeepEqual(f.db.Value(budget.PathKillGlobal), own) {
		t.Errorf("the refused resume wrote the switch: %v, want %v", f.db.Value(budget.PathKillGlobal), own)
	}
}

// TestPolicyPublishRefusesAnotherInstallationsDatabase: `project policy publish`
// (and, through the same opener, `--delete`) writes the node every run of the
// project is admitted by, and it is the door the ruling names first.
func TestPolicyPublishRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newProjectFixture(t, "")
	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("publish into the installation's own marked database was refused: %v\n%s", err, out)
	}
	own := f.db.Value(projectpolicy.PathShared())

	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "project", "policy", "publish")
	refusedByTheOwnershipCheck(t, "project policy publish", err, "nothing was published")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused publish rewrote the policy node")
	}

	// Data and no mark at all: the same answer, from the other half.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "project", "policy", "publish")
	refusedByTheOwnershipCheck(t, "project policy publish", err, "nothing was published")
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused publish rewrote the policy node with the mark removed")
	}

	// The delete door is the same opener: it writes exactly as much.
	f.db.Set(budget.PathMark, birchMark())
	_, _, err = execute(t, "project", "policy", "publish", "--delete", "--yes")
	refusedByTheOwnershipCheck(t, "project policy publish --delete", err, "nothing was published")
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Error("the refused delete removed the policy node")
	}
}

// TestReposAddRefusesAnotherInstallationsDatabase: `project repos add` and
// `repos rm` edit the published allow-list, which is what admits a repository to
// the project at all.
func TestReposAddRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "add", "github/acme/new"); err != nil {
		t.Fatalf("repos add on the installation's own marked database was refused: %v", err)
	}
	own := f.db.Value(projectpolicy.PathShared())

	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "project", "repos", "add", "github/acme/second")
	refusedByTheOwnershipCheck(t, "project repos add", err, "nothing was added to the allow-list")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	_, _, err = execute(t, "project", "repos", "rm", "github/acme/app", "--yes")
	refusedByTheOwnershipCheck(t, "project repos rm", err, "nothing was removed from the allow-list")
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused edit rewrote the policy node")
	}

	// Data and no mark at all: the same answer, from the other half.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "project", "repos", "add", "github/acme/third")
	refusedByTheOwnershipCheck(t, "project repos add", err, "nothing was added to the allow-list")
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused edit rewrote the policy node with the mark removed")
	}
}

// TestEnrollRefusesAnotherInstallationsDatabase: `project sync` enrolls a batch
// of repositories through the same writer, and it is refused before its own
// work — before the App's key is read and before GitHub is asked anything.
func TestEnrollRefusesAnotherInstallationsDatabase(t *testing.T) {
	typed(t)
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	g := newSyncRig(t, "acme/new")
	if out, _, err := executeStdin(t, "aurora\n", "project", "sync", "--github-app-id", "12345", "--github-app-key"); err != nil {
		t.Fatalf("project sync on the installation's own marked database was refused: %v\n%s", err, out)
	}
	own := f.db.Value(projectpolicy.PathShared())

	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "project", "sync", "--github-app-id", "12345", "--github-app-key")
	refusedByTheOwnershipCheck(t, "project sync", err, "nothing was added to the allow-list")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused sync rewrote the policy node")
	}
	if len(g.reads) != 1 {
		t.Errorf("the App's key was read %d times, want once (the refusal comes before the read)", len(g.reads))
	}

	// Data and no mark at all: the same answer, from the other half.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "project", "sync", "--github-app-id", "12345", "--github-app-key")
	refusedByTheOwnershipCheck(t, "project sync", err, "nothing was added to the allow-list")
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused sync rewrote the policy node with the mark removed")
	}
}

// TestAccountsMarkRefusesAnotherInstallationsDatabase: `accounts clear` writes a
// credential's mark, which is what tells every run of the project whether an
// account is in the cascade's rotation.
func TestAccountsMarkRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{"state": "broken"})
	if _, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes"); err != nil {
		t.Fatalf("accounts clear on the installation's own marked database was refused: %v", err)
	}

	f.db.Set(credsMarkPath("acme-personal"), map[string]any{"state": "broken"})
	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes")
	refusedByTheOwnershipCheck(t, "accounts clear", err, "nothing was cleared")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "broken" {
		t.Errorf("the refused clear wrote the mark: %v", m)
	}

	// Data and no mark at all: the same answer, from the other half.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "accounts", "clear", "acme-personal", "--yes")
	refusedByTheOwnershipCheck(t, "accounts clear", err, "nothing was cleared")
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "broken" {
		t.Errorf("the refused clear wrote the mark: %v", m)
	}
}

// TestPairRefusesAnotherInstallationsDatabase: pairing writes the device node
// the rules admit a phone by (and reads it back for `pair ls` and `pair rm`), so
// the whole family asks the one ownership check — a device paired into another
// installation's database is a phone signed into a project it was never given.
func TestPairRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newPairFixture(t, pairMe)
	if _, _, err := pairRun(t, "ls"); err != nil {
		t.Fatalf("pair ls on the installation's own marked database was refused: %v", err)
	}

	f.db.Set(budget.PathMark, birchMark())
	// --timeout 1s: a pairing that is not refused must not sit on the wait the
	// default asks for, so a regression here fails in a second and not in two
	// minutes.
	out, _, err := pairRun(t, "--json", "--timeout", "1s")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("pair wrote into a database that is not this installation's: err = %v", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedMismatch {
		t.Fatalf("events = %v, want one refused with code %q", events, pairRefusedMismatch)
	}
	if !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	if !strings.Contains(err.Error(), "nothing was paired") {
		t.Errorf("the refusal does not say what was not done: %v", err)
	}
	_, _, err = pairRun(t, "ls", "--json")
	if err == nil || !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("pair ls is not held to the same check: %v", err)
	}

	// Data and no mark at all: the same answer, from the other half.
	f.db.Set(budget.PathMark, nil)
	out, _, err = pairRun(t, "ls", "--json")
	if err == nil || !strings.Contains(err.Error(), "and no Fugaro mark") {
		t.Errorf("pair ls is not held to the unmarked database: %v\n%s", err, out)
	}
}
