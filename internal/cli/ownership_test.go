package cli

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
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
//
// The refusal is held to its exit code as well as to its words. A database that
// is not this installation's is the operator's to fix and exits 1; a database
// that cannot be read at all is an outage and exits 2, at the pairing door as
// the "unreachable" code a console switches on. And the config's own half of
// the question — a pair of config facts that do not name one database — is
// asked first of all, without any database being contacted: it is the answer
// `fugaro update`'s stages skip on, so it carries their sentence and not a
// command's.

// installMark is the mark `fugaro update --firebase-project` writes into the
// installation's own database (infra.DB.Plan): the Fugaro project's name and the
// GCP project it lives in. Every fixture here seeds it, because that is what
// makes a database this installation's at all.
func installMark(project, gcpProject string) map[string]any {
	return map[string]any{"managed_by": "fugaro", "version": 1, "project": project, "gcp_project": gcpProject}
}

// auroraMark is installMark for the fixtures' installation: the one every
// fixture's config names (name aurora, gcp_project proj-1234), and the mark
// `fugaro update --firebase-project` writes. A database carrying it is this
// installation's, which is what every writer into it asks before it writes
// (requireOwnBudgetDB, a call of budgetDBUnadopted), so a fixture that seeds it
// is the ordinary installation and one that hands the database a different mark
// is the refusal the tests here are about.
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
		// Fatal, like the checks above: a caller goes on to assert that nothing
		// was written, and an error that is a refusal of some other kind is not
		// one those assertions can be read against.
		t.Fatalf("%s: the refusal does not say what was not done (%q): %v", what, declined, err)
	}
}

// refusedByTheConfigHalf fails the test unless err is the config half's answer:
// a user error naming the fact the config records. It is a helper of its own,
// and not refusedByTheOwnershipCheck, because that half carries the *stage's*
// sentence rather than the caller's declined line (budgetDBUnadoptedErr): the
// words an operator reads there are the ones `fugaro update` skips a stage
// with, and a command's "nothing was set" is not among them.
func refusedByTheConfigHalf(t *testing.T, what string, err error, fact string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s wrote while its config named no database of this installation's", what)
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("%s: exit %d, want 1 (a config fact the operator fixes, not an outage): %v", what, ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), fact) {
		t.Fatalf("%s: the refusal does not name the config fact %q: %v", what, fact, err)
	}
}

// rewriteBudgetBlock replaces the config's budget block with block. The fixture
// writes its block with the fake database's URL, which is only known once the
// fake is up, so a test that needs the config to record something else says so
// here rather than through newBudgetFixture's argument.
func rewriteBudgetBlock(t *testing.T, f *budgetFixture, block string) {
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

// emptySeparateProjectDatabase is a fixture whose database is EMPTY and whose
// config names a Firebase project other than the installation's own GCP
// project: the shape the one question refuses on the absent mark, because an
// empty database passes every check that asks it anything.
func emptySeparateProjectDatabase(t *testing.T) *budgetFixture {
	t.Helper()
	f := newBudgetFixture(t, "")
	rewriteBudgetBlock(t, f, "budget: { mode: observe, per_run_usd: 5, firebase_project: shared-fp-1, rtdb_url: "+f.db.URL+" }\n")
	f.db.Set(budget.PathProject, nil)
	f.db.Set(budget.PathMark, nil)
	return f
}

// rootReadDownTransport is a database answering every request but the one that
// says what is in it: the one question's first read is a shallow root GET
// (infra.DB.Check through rtdb.RootKeys), so failing exactly that request puts
// the failure inside the question while every read the command makes before it
// still succeeds. A transport that fails every request cannot hold the outage
// branch on its own — the name read of /fugaro/project comes first and would
// fail instead, and the answer would be that gate's.
type rootReadDownTransport struct{ rt http.RoundTripper }

func (c rootReadDownTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet && r.URL.Query().Get("shallow") == "true" {
		return nil, errors.New("the database is unreachable")
	}
	return c.rt.RoundTrip(r)
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

// TestBudgetSetRefusesAConfigWithNoBudgetBackend: the question has a config
// half, and it is answered without the database being asked anything at all —
// budget.rtdb_url with no budget.firebase_project is a config that records no
// shared backend, and `budget set` refuses it as the config fact it is rather
// than writing caps into a database the config never named. It is the why-only
// answer: no database was contacted, and the answer is not an error.
func TestBudgetSetRefusesAConfigWithNoBudgetBackend(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	own := f.db.Value(nodeCG)
	rewriteBudgetBlock(t, f, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+f.db.URL+" }\n")

	_, _, err := execute(t, "budget", "set", "--global", "--daily", "60")
	refusedByTheConfigHalf(t, "budget set", err, "records no shared Firebase budget backend")
	if !reflect.DeepEqual(f.db.Value(nodeCG), own) {
		t.Errorf("the refused set wrote the ceiling: %v, want %v", f.db.Value(nodeCG), own)
	}
}

// TestBudgetSetRefusesAConfigWhoseDatabaseIsAnotherProjects: the config half's
// other fact — a budget.firebase_project and a budget.rtdb_url that are not one
// Firebase project's. The database the config names is one nothing here may
// contact, so the refusal has to come from the pair alone: the transport
// carries the one read the command makes before the question (the name read of
// /fugaro/project, checkDBProject) to the fixture's own database, and the
// address the config names is never reached.
func TestBudgetSetRefusesAConfigWhoseDatabaseIsAnotherProjects(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	own := f.db.Value(nodeCG)
	rewriteBudgetBlock(t, f, "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: https://proj-9999-default-rtdb.firebaseio.com }\n")
	base, err := url.Parse(f.db.URL)
	if err != nil {
		t.Fatal(err)
	}
	budgetTransport = carryToFake{base: base, rt: http.DefaultTransport}
	t.Cleanup(func() { budgetTransport = nil })

	_, _, err = execute(t, "budget", "set", "--global", "--daily", "60")
	refusedByTheConfigHalf(t, "budget set", err, "is not the database of the Firebase project its budget.firebase_project names")
	if !reflect.DeepEqual(f.db.Value(nodeCG), own) {
		t.Errorf("the refused set wrote the ceiling: %v, want %v", f.db.Value(nodeCG), own)
	}
}

// TestBudgetSetRefusesAnEmptyDatabaseOfASeparateProject: the empty-database
// case the shared check was written for — nothing in the database and no mark,
// with a config naming a Firebase project other than the installation's own.
// `budget set` refuses and writes nothing, and what answers at this level is
// the name gate: checkDBProject reads /fugaro/project before the ownership
// question is asked, and an empty database does not name the project either. So
// this is where a command stops, and the question's own sentence for this shape
// — the one a `fugaro update` stage skips on, where no name gate stands in
// front of it — is held by the test below.
func TestBudgetSetRefusesAnEmptyDatabaseOfASeparateProject(t *testing.T) {
	f := emptySeparateProjectDatabase(t)

	_, _, err := execute(t, "budget", "set", "--global", "--daily", "60")
	if err == nil {
		t.Fatal("budget set wrote into an empty database that no mark proves is this installation's")
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("budget set: exit %d, want 1 (a refusal, not an outage): %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "no /fugaro/project") {
		t.Errorf("the refusal does not name the empty database's missing project: %v", err)
	}
	if f.db.Value(nodeCG) != nil || f.db.Value(budget.PathMark) != nil {
		t.Errorf("the refused set wrote into the empty database: caps %v, mark %v", f.db.Value(nodeCG), f.db.Value(budget.PathMark))
	}
}

// TestRequireOwnBudgetDBRefusesAnEmptyDatabaseOfASeparateProject is that same
// database asked by the one question itself: with a separate Firebase project
// named, an empty database that carries no mark is not a licence (it passes
// every check that asks it anything), and the answer is the long one — not this
// installation's, and here is the mark that would be the proof. The command's
// own opener is used without its name gate (budgetDBFor), because no command
// reaches this branch otherwise.
func TestRequireOwnBudgetDBRefusesAnEmptyDatabaseOfASeparateProject(t *testing.T) {
	f := emptySeparateProjectDatabase(t)
	ctx := context.Background()
	_, lc, err := selectProject(ctx, cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		t.Fatal(err)
	}

	err = requireOwnBudgetDB(ctx, lc, db, "nothing was set")
	refusedByTheOwnershipCheck(t, "budget set", err, "nothing was set")
	if err != nil && !strings.Contains(err.Error(), "nothing proves the database at") {
		t.Errorf("the refusal is not the empty database's own answer: %v", err)
	}
	if f.db.Value(budget.PathMark) != nil {
		t.Errorf("the refusal wrote the mark into the database it refused: %v", f.db.Value(budget.PathMark))
	}
}

// TestBudgetSetExitsTwoWhenTheDatabaseCannotBeRead: the split the one question
// owes its callers. A database that is not this installation's is the
// operator's to fix and exits 1 (every refusal above); a database that cannot
// be read at all is an outage and exits 2, so a script does not treat the
// network as a config it should go and edit. The failure is put inside the
// question, at its first read: everything the command does before it — reaching
// the database, reading its /fugaro/project — has already succeeded.
func TestBudgetSetExitsTwoWhenTheDatabaseCannotBeRead(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	own := f.db.Value(nodeCG)
	budgetTransport = rootReadDownTransport{rt: http.DefaultTransport}
	t.Cleanup(func() { budgetTransport = nil })

	_, _, err := execute(t, "budget", "set", "--global", "--daily", "60")
	if err == nil {
		t.Fatal("budget set wrote while the database could not be read")
	}
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("budget set: exit %d, want 2 (an outage, not a refusal): %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "nothing was set") {
		t.Errorf("the outage does not say what was not done: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(nodeCG), own) {
		t.Errorf("the failed set wrote the ceiling: %v, want %v", f.db.Value(nodeCG), own)
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

	// The node the two refusals above left alone is the one the delete door is
	// about to be refused over — what it may not remove is this value and no
	// other — so the same assertion is made after the delete as well, and
	// neither of the two can hide a node the other one removed.
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Fatalf("the policy node did not survive the refusals above: %v, want %v", f.db.Value(projectpolicy.PathShared()), own)
	}

	// The delete door is the same opener: it writes exactly as much.
	f.db.Set(budget.PathMark, birchMark())
	_, _, err = execute(t, "project", "policy", "publish", "--delete", "--yes")
	refusedByTheOwnershipCheck(t, "project policy publish --delete", err, "nothing was published")
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Errorf("the refused delete changed the policy node: %v, want %v", f.db.Value(projectpolicy.PathShared()), own)
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
	// How many times the sync of the installation's own database read the
	// App's key. Both refusals below are held to that same count rather than
	// to a fixed one: what says "the refusal comes before the read" is a count
	// that did not move, which a key read and written back to a net-zero change
	// cannot produce.
	readsBeforeRefused := len(g.reads)

	f.db.Set(budget.PathMark, birchMark())
	_, _, err := execute(t, "project", "sync", "--github-app-id", "12345", "--github-app-key")
	refusedByTheOwnershipCheck(t, "project sync", err, "nothing was added to the allow-list")
	if err != nil && !strings.Contains(err.Error(), "one Firebase project serves one installation") {
		t.Errorf("the refusal is not the shared one: %v", err)
	}
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused sync rewrote the policy node")
	}
	if len(g.reads) != readsBeforeRefused {
		t.Errorf("the App's key was read %d more times, want none (the refusal comes before the read)", len(g.reads)-readsBeforeRefused)
	}

	// Data and no mark at all: the same answer, from the other half.
	f.db.Set(budget.PathMark, nil)
	_, _, err = execute(t, "project", "sync", "--github-app-id", "12345", "--github-app-key")
	refusedByTheOwnershipCheck(t, "project sync", err, "nothing was added to the allow-list")
	if !reflect.DeepEqual(f.db.Value(projectpolicy.PathShared()), own) {
		t.Error("the refused sync rewrote the policy node with the mark removed")
	}
	if len(g.reads) != readsBeforeRefused {
		t.Errorf("the App's key was read %d more times, want none (the refusal comes before the read)", len(g.reads)-readsBeforeRefused)
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

// TestPairExitsTwoWhenTheDatabaseCannotBeRead is the same outage at the pairing
// door, where the exit code is also a wire code a console switches on: exit 2
// with "unreachable", so a phone is not told its pairing is into somebody
// else's project when the answer was the database. It is the ownership
// question's failure and not the probe's — the probe's read of the devices node
// comes first and succeeds — so the refusal is held to its declined line as
// well as to the code.
func TestPairExitsTwoWhenTheDatabaseCannotBeRead(t *testing.T) {
	newPairFixture(t, pairMe)
	budgetTransport = rootReadDownTransport{rt: http.DefaultTransport}
	t.Cleanup(func() { budgetTransport = nil })

	out, _, err := pairRun(t, "--json", "--timeout", "1s")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("pair with a database that cannot be read: err = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "nothing was paired") {
		t.Fatalf("the failure is not the ownership question's: %v", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedUnreachable {
		t.Fatalf("events = %v, want one refused with code %q", events, pairRefusedUnreachable)
	}
}
