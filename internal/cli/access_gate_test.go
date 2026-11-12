package cli

import (
	"context"
	"errors"
	"io"
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

// TestAccessRunListsAreTheRolesWithoutANode: an installation with no grants node
// reads this machine's own two lists as its roles, which is what the node is
// migrated FROM (RS3) and what `fugaro user ls` prints for it. A member of
// neither list is refused; a member of the operator list launches.
func TestAccessRunListsAreTheRolesWithoutANode(t *testing.T) {
	noAgentSession(t)
	f := newLaunchBudget(t, "off")
	f.appendConfig(t, "terraform:\n  operators: ['user:listed@example.com']\n")
	env := fileEnv(t, f.cloudFixture)

	accessRunAs(t, "user:stranger@example.com")
	err := launchPolicyGates(context.Background(), env, appSlug, accessRunSpec("acme/app"), "web", false, false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "holds no role") {
		t.Fatalf("a credential on neither list was not refused: %v", err)
	}
	accessRunAs(t, "user:listed@example.com")
	if err := launchPolicyGates(context.Background(), env, appSlug, accessRunSpec("acme/app"), "web", false, false, io.Discard); err != nil {
		t.Fatalf("the machine's own operator was refused: %v", err)
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
	const id = "20261112-120005-abd2"
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
