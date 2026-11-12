package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/task"
)

// access_gate_test.go is RS6a's own behaviour: the commands that launch a run,
// cancel one, read a run's status, read its logs and list the history ask the
// resolver, and every refusal they make is made before anything is written —
// no task, no claim, no execution, no cancel marker.
//
// The credential is stubbed (accessRunAs) because on a real invocation it is the
// verified address of the caller's own credential (access.Caller, F21): a test
// that reached Google's userinfo endpoint would be neither hermetic nor about
// the judgement. The node these cases are judged against is the fixture's own
// budget database, written with the document RS5's commands write.
//
// Two credentials carry every case: a viewer and an operator, both at one
// repository (accessRepoA). A pair at another repository (accessRepoB) is what
// makes the scope the thing being tested rather than the role.

const (
	accessViewer   = "user:viewer@example.com"
	accessOperator = "user:operator@example.com"
	accessRepoA    = "github/acme/app"
	accessRepoB    = "github/acme/other"
)

// accessRunAs is the credential these commands judge with: access.Caller's
// answer, in the canonical member form Caller returns. Every case sets it, so
// no case depends on who is signed in on the machine running the suite.
func accessRunAs(t *testing.T, member string) {
	t.Helper()
	old := accessCallerLookup
	accessCallerLookup = func(context.Context) (string, error) { return member, nil }
	t.Cleanup(func() { accessCallerLookup = old })
}

// accessRunAsUnreadable is the credential's address that cannot be read, which
// on a real invocation is a missing or expired ADC.
func accessRunAsUnreadable(t *testing.T) {
	t.Helper()
	old := accessCallerLookup
	accessCallerLookup = func(context.Context) (string, error) { return "", errors.New("no credentials") }
	t.Cleanup(func() { accessCallerLookup = old })
}

// accessRunNode puts the grants node on the fixture's database.
func accessRunNode(t *testing.T, f *launchBudgetFixture, grants ...accesslib.Grant) {
	t.Helper()
	f.db.Set(accesslib.PathNode, accessNodeDoc(grants...))
}

// accessRunUnreachableDB points the fixture's roles database at a port nothing
// listens on: a network failure, the state rtdb reports as ErrUnavailable
// rather than as a refusal. It is the same address the shared-config fixtures
// use to prove a command reaches no network at all.
func accessRunUnreachableDB(t *testing.T, f *launchBudgetFixture) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), f.db.URL, "http://127.0.0.1:1", 1)
	if updated == string(data) {
		t.Fatal("the fixture's config does not name its budget database")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// accessRunSpec is a launch's task for one repository, the shape
// launchPolicyGates is handed by the launcher.
func accessRunSpec(repo string) *task.Spec {
	return &task.Spec{Version: 1, RunID: budgetRun, Repo: repo, Ref: "main", Workflow: "web", Task: "x"}
}

// TestAccessRunViewerCannotLaunch is the plan's TestViewerCannotLaunch: the
// credential holds viewer at the repository it is launching, and launching
// takes operator (F19). The refusal names the role, the repository and what the
// credential holds, and the launch leaves nothing behind — the gate runs before
// the task is stored and before the claim (run_allowlist.go).
func TestAccessRunViewerCannotLaunch(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAs(t, accessViewer)

	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the viewer's launch: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		"nothing was launched",
		"launching " + accessRepoA,
		"needs the role operator",
		"holds the role viewer",
		accessViewer,
		"user grant " + accessViewer + " --role operator --scope " + accessRepoA,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored its task")
	}
}

// TestAccessRunOperatorOnRepoAOnlyCannotLaunchRepoB is the plan's
// TestAccessRunOperatorOnRepoAOnlyCannotLaunchRepoB: a credential holding
// operator at ONE repository may launch that one and not another, which is the
// whole point of a scope (D1580, the contractor example).
//
// The second repository is launched through the gate rather than through
// `fugaro run` because this fixture's config lists one repository and the gate
// is where the answer is: what a launch of a repository the local config does
// not carry is judged by is the pair the run itself trusts — the slug it would
// be stored under and the repository its task names.
func TestAccessRunOperatorOnRepoAOnlyCannotLaunchRepoB(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA))
	accessRunAs(t, accessOperator)
	env := fileEnv(t, f.cloudFixture)

	err := launchPolicyGates(context.Background(), env, mustSlug("github", "acme/other"), accessRunSpec("acme/other"), "web", false, false, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the operator's launch of another repository: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		"nothing was launched", "launching " + accessRepoB, "needs the role operator",
		"holds no role", accessOperator, accessRepoB,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The scope is not a wall: the same credential launches the repository its
	// pair names.
	if err := launchPolicyGates(context.Background(), env, appSlug, accessRunSpec("acme/app"), "web", false, false, io.Discard); err != nil {
		t.Fatalf("the operator's own repository was refused: %v", err)
	}
}

// TestAccessRunPlacesNobodyIsNotRefused is the gate's own floor: an
// installation whose roles place nobody at all — no stored grant, no implicit
// admin, no database to hold either — has nothing to judge against, and the
// credential is not even read for it (the `t.Fatal` below is the assertion).
// Refusing here would lock the owner of a machine that has never held a role
// out of their own runs, and the lists of every installation that has ever had
// an operator are what RS3 migrates into the node.
func TestAccessRunPlacesNobodyIsNotRefused(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	old := accessCallerLookup
	accessCallerLookup = func(context.Context) (string, error) {
		t.Error("the gate read the credential on an installation that places nobody")
		return "", nil
	}
	t.Cleanup(func() { accessCallerLookup = old })

	env := fileEnv(t, f.cloudFixture)
	if err := launchPolicyGates(context.Background(), env, appSlug, accessRunSpec("acme/app"), "web", false, false, io.Discard); err != nil {
		t.Fatalf("a launch on an installation that places nobody was refused: %v", err)
	}
}

// TestAccessRunListsAreNotTheRoles: an installation whose roles are still only
// its own terraform lists — no grants node, because RS3's migration has not run
// over it — is judged by nothing here. The lists are the machine's copy of the
// roles and not their authority: the node is, and `fugaro update` is what moves
// them into it. Reading them as roles would make this release the one that
// changes what an installation which has not updated does, including refusing a
// credential the lists place by a rule (a service account, F25) that no list can
// express — the state the previous release behaved in is the state this leaves.
func TestAccessRunListsAreNotTheRoles(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	f.appendConfig(t, "terraform:\n  operators: ['user:listed@example.com']\n")
	env := fileEnv(t, f.cloudFixture)

	accessRunAs(t, "user:stranger@example.com")
	if err := launchPolicyGates(context.Background(), env, appSlug, accessRunSpec("acme/app"), "web", false, false, io.Discard); err != nil {
		t.Fatalf("a machine whose roles are only its own lists judged a launch by them: %v", err)
	}
	accessRunAs(t, "user:listed@example.com")
	if err := launchPolicyGates(context.Background(), env, appSlug, accessRunSpec("acme/app"), "web", false, false, io.Discard); err != nil {
		t.Fatalf("the listed member's own launch was refused: %v", err)
	}
}

// TestAccessRunRolesDatabaseOutageIsNotRefused: the roles database is
// unreachable — a network failure, a 5xx or a 429, which rtdb reports as its
// own sentinel and never as a refusal — so nothing is placed and nothing is
// refused. An outage is not an answer about the caller, and a courtesy check
// that turned one into a lockout would stop an installation's own people from
// reading their runs for as long as the endpoint is having a bad day.
//
// The config names an implicit admin (F22), which is the half of this
// installation's roster that is not the node: judged on the half that did not
// go missing, the credential here — which the node places as a viewer — would
// be refused, and that is the lockout this case is about. The observation is
// the check itself and then a read that goes through it, because a launch meets
// the same outage at its own policy read one step later and fails there for its
// own reasons.
func TestAccessRunRolesDatabaseOutageIsNotRefused(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	f.appendConfig(t, "terraform:\n  budget_admins: ['user:boss@example.com']\n")
	accessRunUnreachableDB(t, f)
	env := fileEnv(t, f.cloudFixture)
	accessRunAs(t, accessViewer)

	if err := requireAccess(context.Background(), env, accesslib.ActionLaunch, accessRepoA, "nothing was launched"); err != nil {
		t.Fatalf("the outage of the roles database was read as a refusal: %v", err)
	}
	const id = "20261112-120006-abd4"
	exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
	f.logging.AddJSONLines(exec, []byte(`{"time":"`+logTime(time.Second)+`","severity":"INFO","message":"stage started","stage":"implement"}
`))
	if out, _, err := execute(t, "logs", id); err != nil || !strings.Contains(out, "stage started") {
		t.Fatalf("a read under an unreachable roles database = %q, %v", out, err)
	}
}

// TestAccessRunUnreadableRolesNodeIsRefused is the other side of the outage: a
// node that ANSWERS and cannot be held whole — a document this build cannot
// decode — places nobody and is refused rather than half-read, which is F27's
// fail-closed half. A gate that read it as an empty roster would let the very
// grants it could not understand through.
func TestAccessRunUnreadableRolesNodeIsRefused(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	f.db.Set(accesslib.PathNode, map[string]any{"grants": "not-a-list"})
	accessRunAs(t, accessViewer)

	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) == ExitOK {
		t.Fatalf("the unreadable roles node: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
}

// TestAccessRunUnreadableCredentialIsRefused: the roles come from the
// credential the command is using (F21), so a credential whose address cannot
// be read is refused rather than allowed through — and the refusal says which
// command to run. It is the same choice RS5's grant makes, and the opposite of
// the failure mode the plan pins ("a role check that quietly allows").
func TestAccessRunUnreadableCredentialIsRefused(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAsUnreadable(t)

	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the unreadable credential's launch: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"finding F21", "gcloud auth application-default login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
}

// TestAccessCancelViewerCannotCancel: D1587 gives cancel to an operator within
// scope, and an ordinary viewer does not cancel at all. The run is in the
// credential's own repository, so the role is the only thing refusing it, and
// the refusal is before the cancel's own work — no marker is written and no
// reporter is opened for it.
func TestAccessCancelViewerCannotCancel(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAs(t, accessViewer)
	const id = "20261112-120000-abcd"
	exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateRunning)

	_, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--grace-floor", "0", "--poll", "10ms", id)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the viewer's cancel: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		"nothing was cancelled", "cancelling " + accessRepoA, "needs the role operator", "holds the role viewer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if cancelled(t, f.cloudFixture, id) {
		t.Error("the refused cancel wrote the marker anyway")
	}
}

// TestAccessCancelOperatorWithinScope is the other half of D1587: an operator
// cancels a run of a repository its pair covers, whichever person started it
// (the run here was started by someone else), and a credential whose only pair
// is at another repository does not.
func TestAccessCancelOperatorWithinScope(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA))
	accessRunAs(t, accessOperator)
	const id = "20261112-120001-abce"
	exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateRunning)

	if _, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--grace-floor", "0", "--poll", "10ms", id); err != nil {
		t.Fatalf("the operator's cancel was refused: %v", err)
	}
	if !cancelled(t, f.cloudFixture, id) {
		t.Error("the operator's cancel wrote no marker")
	}
}

// TestAccessCancelOutsideScopeIsRefused: the same operator pair, a run of
// another repository — the scope refuses it (D1587's "within scope").
func TestAccessCancelOutsideScopeIsRefused(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessOperator, accesslib.RoleOperator, accessRepoB))
	accessRunAs(t, accessOperator)
	const id = "20261112-120002-abcf"
	exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateRunning)

	_, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--grace-floor", "0", "--poll", "10ms", id)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the out-of-scope cancel: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), accessRepoA) {
		t.Errorf("the refusal does not name the repository the run is in: %v", err)
	}
	if cancelled(t, f.cloudFixture, id) {
		t.Error("the refused cancel wrote the marker anyway")
	}
}

// TestAccessRunLogsAreReadInScope: a run's logs are read by the credential
// holding a role in its repository (D1586's read half), and the repository the
// credential holds nothing in is refused.
func TestAccessRunLogsAreReadInScope(t *testing.T) {
	noAgentSession(t)
	const id = "20261112-120003-abd0"
	logs := []byte(`{"time":"` + logTime(time.Second) + `","severity":"INFO","message":"stage started","stage":"implement"}
`)

	t.Run("in scope", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
		accessRunAs(t, accessViewer)
		exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
		f.logging.AddJSONLines(exec, logs)
		out, _, err := execute(t, "logs", id)
		if err != nil || !strings.Contains(out, "stage started") {
			t.Fatalf("logs in scope = %q, %v", out, err)
		}
	})

	t.Run("another repository only", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoB))
		accessRunAs(t, accessViewer)
		exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
		f.logging.AddJSONLines(exec, logs)
		out, _, err := execute(t, "logs", id)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("logs out of scope: err = %v (exit %d), out = %q", err, ExitCode(err), out)
		}
		if strings.Contains(out, "stage started") {
			t.Errorf("the refused read printed the run's logs anyway: %q", out)
		}
		for _, want := range []string{"nothing was read", accessRepoA, "needs the role viewer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
	})
}

// TestAccessRunsShowIsReadInScope: a run's status is its own repository's read,
// so a credential scoped elsewhere does not see it.
func TestAccessRunsShowIsReadInScope(t *testing.T) {
	noAgentSession(t)
	const id = "20261112-120004-abd1"

	t.Run("in scope", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
		accessRunAs(t, accessViewer)
		seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
		if _, _, err := execute(t, "runs", "show", id); err != nil {
			t.Fatalf("runs show in scope: %v", err)
		}
	})

	t.Run("another repository only", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoB))
		accessRunAs(t, accessViewer)
		seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
		out, _, err := execute(t, "runs", "show", id)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("runs show out of scope: err = %v (exit %d), out = %q", err, ExitCode(err), out)
		}
		for _, want := range []string{"nothing was read", accessRepoA, "needs the role viewer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
	})
}

// TestAccessRunsLsListsInScope: the history a credential reads is the history of
// the repositories it holds a role in, and what that leaves out is said on
// stderr rather than dropped in silence (D1586's read half).
func TestAccessRunsLsListsInScope(t *testing.T) {
	noAgentSession(t)
	// A listing's window is measured back from the clock lsNow reads, and this
	// test's listing is that one: the id is minted against it rather than fixed
	// (runid_test.go's guard, TestNoFixedRunIDsInRecencyTests).
	id := recentRunID(t, time.Minute, "abd2")
	// A listing prints a run's short form, not its id (task.ShortRun).
	short := task.ShortRun(id)

	t.Run("in scope", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
		accessRunAs(t, accessViewer)
		seedRun(t, f.cloudFixture, id, "", "someone@example.com", false)
		out, errOut, err := execute(t, "runs", "ls")
		if err != nil || !strings.Contains(out, short) {
			t.Fatalf("runs ls in scope = %q, %v", out, err)
		}
		if strings.Contains(errOut, "holds no role") {
			t.Errorf("the listing left out a run of the credential's own repository: %q", errOut)
		}
	})

	t.Run("another repository only", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoB))
		accessRunAs(t, accessViewer)
		seedRun(t, f.cloudFixture, id, "", "someone@example.com", false)
		out, errOut, err := execute(t, "runs", "ls")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, short) {
			t.Errorf("the listing printed a run of a repository the credential holds no role in: %q", out)
		}
		if !strings.Contains(errOut, "holds no role") {
			t.Errorf("the listing dropped the run in silence: stderr %q", errOut)
		}
	})
}

// TestAccessRunDiagnoseIsReadInScope: a diagnosis prints the run's logs and the
// chain it belongs to, so it is the same read `fugaro logs` and
// `fugaro runs show` make — D1586's read half, which a command cannot step
// around by being named differently — and a credential scoped to another
// repository reads none of it.
func TestAccessRunDiagnoseIsReadInScope(t *testing.T) {
	noAgentSession(t)
	const id = "20261112-120005-abd3"
	logs := []byte(`{"time":"` + logTime(time.Second) + `","severity":"INFO","message":"stage started","stage":"implement"}
`)

	t.Run("in scope", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
		accessRunAs(t, accessViewer)
		exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
		f.logging.AddJSONLines(exec, logs)
		out, _, err := execute(t, "diagnose", id)
		if err != nil || !strings.Contains(out, "stage started") {
			t.Fatalf("diagnose in scope = %q, %v", out, err)
		}
	})

	t.Run("another repository only", func(t *testing.T) {
		f := newLaunchBudget(t, "off")
		accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoB))
		accessRunAs(t, accessViewer)
		exec := seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
		f.logging.AddJSONLines(exec, logs)
		out, _, err := execute(t, "diagnose", id)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("diagnose out of scope: err = %v (exit %d), out = %q", err, ExitCode(err), out)
		}
		if strings.Contains(out, "stage started") {
			t.Errorf("the refused diagnosis printed the run's logs anyway: %q", out)
		}
		for _, want := range []string{"nothing was read", accessRepoA, "needs the role viewer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
	})
}

// TestAccessRunProjectScopeRefusalNamesAUsableCommand: a run under a slug this
// machine's config cannot name is judged at project scope, and the remedy the
// refusal names has to be a command that runs. `user ls --repo` takes a
// repository (userLsRepo refuses anything that is not provider/owner/name), so
// a project-scope refusal pointing at `--repo project` would answer one error
// with another.
func TestAccessRunProjectScopeRefusalNamesAUsableCommand(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAs(t, accessViewer)
	env := fileEnv(t, f.cloudFixture)

	err := requireProjectAccess(context.Background(), env, accesslib.ActionViewRepo, "github/acme/unlisted", "nothing was read")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the project-scope read: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		"nothing was read",
		"judged at project scope",
		"user grant " + accessViewer + " --role viewer --scope project",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--repo project") {
		t.Errorf("the refusal names a --repo value this build refuses: %v", err)
	}
	if _, _, lsErr := execute(t, "user", "ls"); lsErr != nil {
		t.Errorf("the command the refusal names does not run: %v", lsErr)
	}
}

// TestAccessRunActionRolesAgreeWithTheResolver is what keeps the refusal
// sentences honest: this file's own table names the role each action takes, and
// access.Allowed is the one that decides. The least role is derived here by
// asking Allowed about a credential holding each role in turn, so the day the
// resolver's table moves — or the day a sentence above names the wrong role —
// this fails rather than printing a person a role nobody needs.
func TestAccessRunActionRolesAgreeWithTheResolver(t *testing.T) {
	const probe = "user:probe@example.com"
	project := accesslib.Scope{Kind: accesslib.ScopeProject}
	for action, a := range accessActions {
		if len(accesslib.Roles()) == 0 {
			t.Fatal("the resolver names no roles")
		}
		least := accesslib.RoleNone
		for _, r := range accesslib.Roles() {
			in := accesslib.Input{
				Grants:     []accesslib.Grant{{User: probe, Role: r, Scope: project}},
				AllowRepos: []string{"*"},
			}
			if accesslib.Allowed(in, probe, action, accessRepoA) {
				least = r
				break
			}
		}
		if least != a.role {
			t.Errorf("%s takes %q, and this file's refusal says %q", action, least, a.role)
		}
	}
}
