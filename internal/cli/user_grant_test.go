package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// user_grant_test.go is RS5's own behaviour: `fugaro user grant`, `revoke` and
// `ls --repo`, at the command a person runs. Every case is about the judgement
// the write is made under — the subset rule, the last project admin, the
// repository admin's reach, the conditional write and the database's own
// refusal — and about the two listings.
//
// The caller is stubbed (userPairAs) because it is a credential's verified
// address on a real invocation (access.Caller, F21): a test that reached
// Google's userinfo endpoint would be neither hermetic nor about the judgement.

// userPairLists is what THIS MACHINE's own config carries beside the node: its
// two IAM lists (the roles' own words, RS3) and the budget admins it names
// among the implicit admins (F17.3).
type userPairLists struct {
	operators    []string // terraform.operators: who may launch and watch runs
	admins       []string // terraform.admins: who may onboard and apply
	budgetAdmins []string // terraform.budget_admins: implicit admins this machine can read
}

// userPairRig is an installation whose roles are the grants node: the fake
// budget database a converge reads and derives from, and the node the test
// leaves it holding. An empty lists leaves the machine's own config without a
// terraform block, which is the state a test wants when the lists are not the
// point — the node is present, so nothing is judged against them but the
// implicit admins, and there are none.
func userPairRig(t *testing.T, lists userPairLists, grants ...accesslib.Grant) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	noAgentSession(t)
	noTerminal(t)
	r, db := updateBudgetRig(t)
	if len(lists.operators)+len(lists.admins)+len(lists.budgetAdmins) > 0 {
		r.appendConfig(t, "terraform:\n  launchers: "+yamlList(lists.operators)+"\n  operators: "+yamlList(lists.admins)+"\n  budget_admins: "+yamlList(lists.budgetAdmins)+"\n")
	}
	if len(grants) > 0 {
		db.Set(accesslib.PathNode, accessNodeDoc(grants...))
	}
	r.save(t)
	return r, db
}

// userPairAs is the credential these commands judge with: access.Caller's
// answer, in the canonical member form Caller returns. Every case sets it, so
// no case depends on who is signed in on the machine running the suite.
func userPairAs(t *testing.T, member string) {
	t.Helper()
	old := userCallerLookup
	userCallerLookup = func(context.Context) (string, error) { return member, nil }
	t.Cleanup(func() { userCallerLookup = old })
}

// userStored is one pair as the node holds it, built through ParseScope so a
// test's scope is the canonical spelling this command writes.
func userStored(t *testing.T, member string, role accesslib.Role, scope string) accesslib.Grant {
	t.Helper()
	sc, err := accesslib.ParseScope(scope)
	if err != nil {
		t.Fatalf("the test's own scope %q is not one the node parses: %v", scope, err)
	}
	return accesslib.Grant{User: member, Role: role, Scope: sc}
}

// userNodeLines is the grants node as a person reads it: the member, the role
// and the scope per pair, sorted, so a case can say what a write left behind
// without the node's own order mattering.
func userNodeLines(t *testing.T, db *gcpfake.RTDB) []string {
	t.Helper()
	out := make([]string, 0)
	for _, g := range accessGrants(t, db) {
		out = append(out, g.User+" ("+string(g.Role)+") at "+g.Scope.String())
	}
	slices.Sort(out)
	return out
}

// userPairRun runs one grant or revoke as the person would, and answers with
// the whole account of it: both streams and the error's own text.
func userPairRun(t *testing.T, verb, member, role, scope string) (string, error) {
	t.Helper()
	out, errOut, err := userCommand(t, verb, member, "--role", role, "--scope", scope)
	return accessOut(out, errOut, err), err
}

// userPairWait waits for the fake database to receive the request a hold
// stamped. A command that never got that far is a different failure from one
// that lost the race the case is about, so it says which request it waited for
// and unblocks the command rather than leaving it in flight.
func userPairWait(t *testing.T, stamped <-chan struct{}, release func(), done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-stamped:
	case <-time.After(30 * time.Second):
		release()
		<-done
		t.Fatalf("the command never sent its %s to the grants node", what)
	}
}

// TestGrantMustBeSubsetOfGrantorScope is RS5's red step at the command level: a
// grant is a subset of the grantor's own scope at that role or higher (F17.1),
// so a repository admin holding github/acme/api cannot grant github/acme/*
// — the pattern is wider than what they hold — and the refusal names the role
// and the scope the change would need.
//
// The control is the same caller granting the same role at the scope they hold:
// the rule is about the subset and not about a repository admin being unable to
// grant at all. Its scope is written as the variant a person types
// (github/acme/api.git), which also pins the half F17.4 asks for: the stored
// pair and the printed sentence carry the canonical scope, not what was typed.
func TestGrantMustBeSubsetOfGrantorScope(t *testing.T) {
	const me, other = "user:api-admin@example.com", "user:new@example.com"
	t.Run("wider than the grantor's own scope", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "github/acme/api"))
		userPairAs(t, me)
		all, err := userPairRun(t, "grant", other, "operator", "github/acme/*")
		if err == nil {
			t.Fatalf("a repository admin granted a scope wider than their own:\n%s", all)
		}
		if got := ExitCode(err); got != ExitUserError {
			t.Errorf("the refusal exits %d, want %d", got, ExitUserError)
		}
		for _, w := range []string{
			"would need operator at a scope covering github/acme/*",
			"you hold admin at github/acme/api",
			"finding F17.1",
		} {
			if !strings.Contains(all, w) {
				t.Errorf("the refusal does not say %q:\n%s", w, all)
			}
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{me + " (admin) at github/acme/api"}) {
			t.Errorf("the refused grant wrote the node: %v\n%s", got, all)
		}
	})
	t.Run("the same role at the scope they hold", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "github/acme/api"))
		userPairAs(t, me)
		all, err := userPairRun(t, "grant", other, "operator", "github/acme/api.git")
		if err != nil {
			t.Fatalf("a repository admin could not grant at the scope they hold: %v\n%s", err, all)
		}
		if !strings.Contains(all, `now holds operator at "github/acme/api" in project`) {
			t.Errorf("the grant does not print the stored canonical scope:\n%s", all)
		}
		want := []string{
			me + " (admin) at github/acme/api",
			other + " (operator) at github/acme/api",
		}
		if got := userNodeLines(t, db); !slices.Equal(got, want) {
			t.Errorf("the node holds %v, want %v:\n%s", got, want, all)
		}
	})
}

// TestRevokeRefusesLastProjectAdmin: the installation keeps a project-scope
// admin, and a revoke is judged over the document that would be written (F17.2)
// — so the member who is the node's only project admin cannot take themselves
// out of the role, and nobody else can either.
//
// The two controls are the two ways the rule is satisfied: a second stored
// project admin, and an implicit one this machine can read (its own
// terraform.budget_admins, F17.3).
func TestRevokeRefusesLastProjectAdmin(t *testing.T) {
	const only, second, root = "user:only@example.com", "user:second@example.com", "user:root@example.com"
	t.Run("the node's only project admin", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, only, accesslib.RoleAdmin, "project"))
		userPairAs(t, only)
		all, err := userPairRun(t, "revoke", only, "admin", "project")
		if err == nil {
			t.Fatalf("the last project admin revoked their own pair:\n%s", all)
		}
		for _, w := range []string{"would hold no admin at project scope", "terraform.budget_admin"} {
			if !strings.Contains(all, w) {
				t.Errorf("the refusal does not say %q:\n%s", w, all)
			}
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{only + " (admin) at project"}) {
			t.Errorf("the refused revoke wrote the node: %v\n%s", got, all)
		}
	})
	t.Run("a second stored project admin", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{},
			userStored(t, only, accesslib.RoleAdmin, "project"),
			userStored(t, second, accesslib.RoleAdmin, "project"))
		userPairAs(t, only)
		all, err := userPairRun(t, "revoke", only, "admin", "project")
		if err != nil {
			t.Fatalf("the installation was left with a project admin and the revoke was refused: %v\n%s", err, all)
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{second + " (admin) at project"}) {
			t.Errorf("the node holds %v, want the second admin alone:\n%s", got, all)
		}
	})
	t.Run("a budget admin of this machine's config", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{budgetAdmins: []string{root}},
			userStored(t, only, accesslib.RoleAdmin, "project"))
		userPairAs(t, only)
		all, err := userPairRun(t, "revoke", only, "admin", "project")
		if err != nil {
			t.Fatalf("an implicit admin was named and the last stored admin could not go: %v\n%s", err, all)
		}
		if got := userNodeLines(t, db); len(got) != 0 {
			t.Errorf("the node still holds %v, want nothing:\n%s", got, all)
		}
	})
}

// TestGrantRefusesInAgentSession: a change to who may do what is a person's to
// make (D1590), and a coding agent's session holds the admin's credentials, so
// the command refuses there rather than asking. What it prints is the exact
// command for the person to run — the member, the role and the scope — and
// nothing is read or written on the way to it.
//
// The control is the same command in the same rig with the marker gone: the
// refusal is the session's and nothing else's.
func TestGrantRefusesInAgentSession(t *testing.T) {
	stubSelfCommand(t)
	const me, other = "user:me@example.com", "user:new@example.com"
	_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
	userPairAs(t, me)
	agentSession(t)
	all, err := userPairRun(t, "grant", other, "operator", "github/acme/*")
	if err == nil {
		t.Fatalf("an agent's session granted a role:\n%s", all)
	}
	for _, w := range []string{
		// One shell word per argument: the pattern is quoted, because an
		// unquoted "*" is a glob the pasting shell would expand against
		// whatever the current directory holds (holderCommand).
		"fugaro user grant " + other + " --role operator --scope 'github/acme/*'",
		"a coding agent's session",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("the refusal does not say %q:\n%s", w, all)
		}
	}
	if got := userNodeLines(t, db); !slices.Equal(got, []string{me + " (admin) at project"}) {
		t.Errorf("the refused grant wrote the node: %v\n%s", got, all)
	}
	noAgentSession(t)
	if _, err := userPairRun(t, "grant", other, "operator", "github/acme/*"); err != nil {
		t.Fatalf("the same grant outside an agent's session was refused: %v", err)
	}
}

// TestAgentRefusalQuotesTheCommand: the refusal's sentence is a command to be
// copied into a shell, and it is built from the agent's own argv before any of
// it is validated — the refusal is about who is running, so it comes first. An
// argument holding a space, a quote or a ";" has to reach the shell as the one
// word the agent attempted: quoted, or the person pastes a command that is not
// the one this refusal is about (a second word the shell splits off, an option,
// or a command of its own).
//
// The scope is the plausible shape of it — a person typing two patterns
// space-separated — and the pin is the shell word, not the text: the unquoted
// form appears nowhere in the sentence.
func TestAgentRefusalQuotesTheCommand(t *testing.T) {
	stubSelfCommand(t)
	const me, other = "user:me@example.com", "user:new@example.com"
	_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
	userPairAs(t, me)
	agentSession(t)
	scope := "github/acme/* github/other/*"
	all, err := userPairRun(t, "grant", other, "operator", scope)
	if err == nil {
		t.Fatalf("an agent's session granted a role:\n%s", all)
	}
	if want := "fugaro user grant " + other + " --role operator --scope '" + scope + "'"; !strings.Contains(all, want) {
		t.Errorf("the refusal does not hand over the command as one shell word per argument (%q):\n%s", want, all)
	}
	if strings.Contains(all, "--scope "+scope) {
		t.Errorf("the refusal hands over a command a shell splits into two arguments:\n%s", all)
	}
	if got := userNodeLines(t, db); !slices.Equal(got, []string{me + " (admin) at project"}) {
		t.Errorf("the refused grant wrote the node: %v\n%s", got, all)
	}
}

// judged, and written conditionally on that ETag, so a change another admin
// lands in between is re-read and re-judged rather than overwritten (F23). The
// fake database holds the two requests apart, so this test lands the other
// admin's node in exactly that window.
//
// Both halves of the rule are pinned: a change that no longer passes the
// judgement is refused with the node the other writer made left intact, and one
// that still passes lands on top of it — never on the picture it was first
// judged against, which would drop the other writer's grant.
func TestConcurrentGrantsRejudgeOnETagMismatch(t *testing.T) {
	const me, other, third = "user:me@example.com", "user:new@example.com", "user:third@example.com"
	t.Run("the re-judged change is refused", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
		userPairAs(t, me)
		stampedRead, releaseRead := db.HoldNext(accesslib.PathNode)
		stampedWrite, releaseWrite := db.HoldNext(accesslib.PathNode)
		done := make(chan struct{})
		var all string
		var err error
		go func() {
			defer close(done)
			out, errOut, e := userCommand(t, "grant", other, "--role", "operator", "--scope", "project")
			all = accessOut(out, errOut, e)
			err = e
		}()
		userPairWait(t, stampedRead, releaseRead, done, "read")
		releaseRead()
		userPairWait(t, stampedWrite, releaseWrite, done, "write")
		// The other admin's node lands while this change's write is in flight,
		// and it holds this caller at one repository's scope: the grant that
		// passed the first judgement is wider than what they hold now.
		db.Set(accesslib.PathNode, accessNodeDoc(userStored(t, me, accesslib.RoleAdmin, "github/acme/api")))
		releaseWrite()
		<-done
		if err == nil {
			t.Fatalf("a change judged against a node it no longer matched landed:\n%s", all)
		}
		if !strings.Contains(all, "would need operator at a scope covering project") {
			t.Errorf("the re-judged refusal does not name the role and the scope:\n%s", all)
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{me + " (admin) at github/acme/api"}) {
			t.Errorf("the node holds %v, want the other admin's node untouched:\n%s", got, all)
		}
	})
	t.Run("the re-judged change lands on the new node", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
		userPairAs(t, me)
		stampedRead, releaseRead := db.HoldNext(accesslib.PathNode)
		stampedWrite, releaseWrite := db.HoldNext(accesslib.PathNode)
		done := make(chan struct{})
		var all string
		var err error
		go func() {
			defer close(done)
			out, errOut, e := userCommand(t, "grant", other, "--role", "operator", "--scope", "project")
			all = accessOut(out, errOut, e)
			err = e
		}()
		userPairWait(t, stampedRead, releaseRead, done, "read")
		releaseRead()
		userPairWait(t, stampedWrite, releaseWrite, done, "write")
		// The other admin's node keeps this caller at project scope, so the
		// change still holds — and it is judged against what the node holds
		// NOW, so the grant the other admin made is not dropped by it.
		db.Set(accesslib.PathNode, accessNodeDoc(
			userStored(t, me, accesslib.RoleAdmin, "project"),
			userStored(t, third, accesslib.RoleViewer, "github/acme/*")))
		releaseWrite()
		<-done
		if err != nil {
			t.Fatalf("a change that still held after the re-read was refused: %v\n%s", err, all)
		}
		want := []string{
			me + " (admin) at project",
			other + " (operator) at project",
			third + " (viewer) at github/acme/*",
		}
		if got := userNodeLines(t, db); !slices.Equal(got, want) {
			t.Errorf("the node holds %v, want %v — a write from the first read drops the other admin's grant:\n%s", got, want, all)
		}
	})
}

// TestSelfDemotionObeysLastAdminRule: `grant --role operator` for the member who
// is the node's only project admin is a demotion, and F17.2's rule is about the
// document that would be written rather than about the verb — the role is
// emptied exactly as a revoke empties it. A second project admin makes the same
// change an ordinary one.
func TestSelfDemotionObeysLastAdminRule(t *testing.T) {
	const me, second = "user:me@example.com", "user:second@example.com"
	t.Run("the only project admin", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
		userPairAs(t, me)
		all, err := userPairRun(t, "grant", me, "operator", "project")
		if err == nil {
			t.Fatalf("the last project admin demoted themselves:\n%s", all)
		}
		if !strings.Contains(all, "would hold no admin at project scope") {
			t.Errorf("the refusal does not name the role the document would lose:\n%s", all)
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{me + " (admin) at project"}) {
			t.Errorf("the refused demotion wrote the node: %v\n%s", got, all)
		}
	})
	t.Run("a second project admin", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{},
			userStored(t, me, accesslib.RoleAdmin, "project"),
			userStored(t, second, accesslib.RoleAdmin, "project"))
		userPairAs(t, me)
		all, err := userPairRun(t, "grant", me, "operator", "project")
		if err != nil {
			t.Fatalf("a demotion beside another project admin was refused: %v\n%s", err, all)
		}
		if !strings.Contains(all, `now holds operator at "project"`) {
			t.Errorf("the demotion does not print the stored canonical scope:\n%s", all)
		}
		want := []string{me + " (operator) at project", second + " (admin) at project"}
		if got := userNodeLines(t, db); !slices.Equal(got, want) {
			t.Errorf("the node holds %v, want %v:\n%s", got, want, all)
		}
	})
}

// TestRepoAdminCannotRevokeProjectGrant: a revoke or a demotion needs admin at
// a scope covering the pair (F24), so a repository admin can neither take away
// a project-scope pair nor one outside their own repository — the reach they
// hold is the reach they may take away.
func TestRepoAdminCannotRevokeProjectGrant(t *testing.T) {
	const me, root, other = "user:api-admin@example.com", "user:root@example.com", "user:other@example.com"
	t.Run("a project-scope pair", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{},
			userStored(t, me, accesslib.RoleAdmin, "github/acme/api"),
			userStored(t, root, accesslib.RoleAdmin, "project"),
			userStored(t, other, accesslib.RoleOperator, "project"))
		userPairAs(t, me)
		all, err := userPairRun(t, "revoke", other, "operator", "project")
		if err == nil {
			t.Fatalf("a repository admin revoked a project-scope pair:\n%s", all)
		}
		for _, w := range []string{
			"needs admin at a scope covering project",
			"you hold admin at github/acme/api",
			"finding F24",
		} {
			if !strings.Contains(all, w) {
				t.Errorf("the refusal does not say %q:\n%s", w, all)
			}
		}
		want := []string{
			me + " (admin) at github/acme/api",
			other + " (operator) at project",
			root + " (admin) at project",
		}
		if got := userNodeLines(t, db); !slices.Equal(got, want) {
			t.Errorf("the refused revoke wrote the node: %v, want %v\n%s", got, want, all)
		}
	})
	t.Run("a pair inside their own scope", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{},
			userStored(t, me, accesslib.RoleAdmin, "github/acme/api"),
			userStored(t, root, accesslib.RoleAdmin, "project"),
			userStored(t, other, accesslib.RoleOperator, "github/acme/api"))
		userPairAs(t, me)
		all, err := userPairRun(t, "revoke", other, "operator", "github/acme/api")
		if err != nil {
			t.Fatalf("a repository admin could not revoke a pair inside their own scope: %v\n%s", err, all)
		}
		want := []string{
			me + " (admin) at github/acme/api",
			root + " (admin) at project",
		}
		if got := userNodeLines(t, db); !slices.Equal(got, want) {
			t.Errorf("the node holds %v, want %v:\n%s", got, want, all)
		}
	})
}

// TestExplicitAdminRefusalNamesBudgetAdmins: the grants node sits under
// config/admin, whose `.write` is false, so no client token writes it and the
// IAM permission that does is roles/firebasedatabase.admin — which an admin
// STORED in the node derives none of (F22, D1592). The write is what refuses
// them, and the sentence names the permission, the holders and the one command
// that settles it, rather than reporting a change that did not land.
//
// The control is the same change with the database answering the write: the
// judgement itself was never the problem.
func TestExplicitAdminRefusalNamesBudgetAdmins(t *testing.T) {
	const me, other = "user:root@example.com", "user:new@example.com"
	t.Run("the database refuses the write", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
		userPairAs(t, me)
		db.DenyNext(1)
		all, err := userPairRun(t, "grant", other, "operator", "project")
		if err == nil {
			t.Fatalf("a write the database refused was reported as landed:\n%s", all)
		}
		for _, w := range []string{
			"roles/firebasedatabase.admin",
			"terraform.budget_admins",
			"fugaro update --budget-admin",
		} {
			if !strings.Contains(all, w) {
				t.Errorf("the refusal does not say %q:\n%s", w, all)
			}
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{me + " (admin) at project"}) {
			t.Errorf("the refused write wrote the node: %v\n%s", got, all)
		}
	})
	t.Run("the same change with the write allowed", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleAdmin, "project"))
		userPairAs(t, me)
		if _, err := userPairRun(t, "grant", other, "operator", "project"); err != nil {
			t.Fatalf("an admin whose write the database allows was refused: %v", err)
		}
		want := []string{other + " (operator) at project", me + " (admin) at project"}
		if got := userNodeLines(t, db); !slices.Equal(got, want) {
			t.Errorf("the node holds %v, want %v", got, want)
		}
	})
}

// TestRefusalNamesRoleAndScope: every refusal is a sentence naming the role and
// the scope the change would need, and never a prompt — a command a person runs
// at their own terminal must not ask a question, because the answer is the
// change itself and nothing here is a confirmation of something already
// decided.
func TestRefusalNamesRoleAndScope(t *testing.T) {
	const me, other = "user:api-admin@example.com", "user:new@example.com"
	for _, tc := range []struct {
		name   string
		verb   string
		member string
		role   string
		scope  string
		node   []accesslib.Grant
		wants  []string
	}{
		{
			name: "a grant wider than the grantor's scope", verb: "grant", member: other,
			role: "operator", scope: "github/acme/*",
			wants: []string{"operator", "github/acme/*", "you hold admin at github/acme/api"},
		},
		{
			name: "a revoke outside the caller's admin scope", verb: "revoke", member: other,
			role: "admin", scope: "project",
			node: []accesslib.Grant{
				userStored(t, me, accesslib.RoleOperator, "github/acme/api"),
				userStored(t, other, accesslib.RoleAdmin, "project"),
			},
			wants: []string{"admin", "project", "you hold operator at github/acme/api"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := tc.node
			if node == nil {
				node = []accesslib.Grant{
					userStored(t, me, accesslib.RoleAdmin, "github/acme/api"),
					userStored(t, other, accesslib.RoleAdmin, "project"),
				}
			}
			_, db := userPairRig(t, userPairLists{}, node...)
			userPairAs(t, me)
			all, err := userPairRun(t, tc.verb, tc.member, tc.role, tc.scope)
			if err == nil {
				t.Fatalf("the change was allowed:\n%s", all)
			}
			for _, w := range append(slices.Clone(tc.wants), "nothing was written") {
				if !strings.Contains(all, w) {
					t.Errorf("the refusal does not say %q:\n%s", w, all)
				}
			}
			for _, ask := range []string{"?", "Type ", "[y/N]", "confirm"} {
				if strings.Contains(strings.ToLower(all), strings.ToLower(ask)) {
					t.Errorf("the refusal asks something (%q) instead of saying what is needed:\n%s", ask, all)
				}
			}
			if got := userNodeLines(t, db); len(got) != len(node) {
				t.Errorf("the refused change wrote the node: %v\n%s", got, all)
			}
		})
	}
}

// TestUserLsJSONHasNoLegacyKeys: `user ls --json` is the machine-readable
// listing, and its names are the ones this release gives the two lists
// (run_operators, admins) and the node's own (grants, effective, legacy). The
// version-1 keys are gone and are NOT reused with a new meaning: `operators`
// and `launchers` named the two lists of a shape this document no longer
// carries, and a reader of either word would draw a wrong figure with no error
// (CLAUDE.md's field rule, F27).
func TestUserLsJSONHasNoLegacyKeys(t *testing.T) {
	const me, ops, root = "user:me@example.com", "user:ops@example.com", "user:root@example.com"
	_, _ = userPairRig(t,
		userPairLists{operators: []string{ops}, admins: []string{root}},
		userStored(t, me, accesslib.RoleAdmin, "project"))
	out, errOut, err := userCommand(t, "ls", "--json")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro user ls --json failed: %v\n%s", err, all)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the listing is not one JSON document: %v\n%s", err, all)
	}
	for _, k := range []string{"operators", "launchers"} {
		if _, ok := doc[k]; ok {
			t.Errorf("the document carries %q: the two lists' version-1 keys are gone, and a version-1 reader must not meet one of them holding something else (F27)", k)
		}
		if strings.Contains(out, `"`+k+`"`) {
			t.Errorf("the document spells %q somewhere in it:\n%s", k, out)
		}
	}
	for _, k := range []string{"version", "project", "gcp_project", "run_operators", "admins", "grants", "effective", "legacy"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("the document has no %s field:\n%s", k, out)
		}
	}
	var doc2 struct {
		Version      int      `json:"version"`
		RunOperators []string `json:"run_operators"`
		Admins       []string `json:"admins"`
		Grants       []struct {
			User  string `json:"user"`
			Role  string `json:"role"`
			Scope string `json:"scope"`
		} `json:"grants"`
	}
	if err := json.Unmarshal([]byte(out), &doc2); err != nil {
		t.Fatalf("the listing does not decode into the document's shape: %v", err)
	}
	if doc2.Version != userListDocVersion {
		t.Errorf("the document says version %d, want %d", doc2.Version, userListDocVersion)
	}
	if !slices.Equal(doc2.RunOperators, []string{ops}) || !slices.Equal(doc2.Admins, []string{root}) {
		t.Errorf("the two lists are %v / %v, want %v / %v: they are this machine's own config, under the names this version gives them",
			doc2.RunOperators, doc2.Admins, []string{ops}, []string{root})
	}
	if len(doc2.Grants) != 1 || doc2.Grants[0].User != me || doc2.Grants[0].Role != "admin" || doc2.Grants[0].Scope != "project" {
		t.Errorf("the grants are %+v, want the node's one pair: %s (admin) at project", doc2.Grants, me)
	}
}

// TestUserLsEffectiveRoleInARepo: `ls --repo` answers the one question the
// roster cannot — the role each member holds in one repository — and the roster
// beside it names the repositories a pattern matches among those this machine
// knows, labelled "known" (F27): that answer is this machine's own config and
// never a claim that no other repository is admitted.
func TestUserLsEffectiveRoleInARepo(t *testing.T) {
	const me, root, viewer = "user:me@example.com", "user:root@example.com", "user:viewer@example.com"
	_, _ = userPairRig(t, userPairLists{},
		userStored(t, me, accesslib.RoleOperator, "bitbucket/acme/*"),
		userStored(t, root, accesslib.RoleAdmin, "project"),
		userStored(t, viewer, accesslib.RoleViewer, "bitbucket/acme/sandbox"))
	out, errOut, err := userCommand(t, "ls", "--repo", "bitbucket/acme/sandbox")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro user ls --repo failed: %v\n%s", err, all)
	}
	if !strings.Contains(all, "known: bitbucket/acme/sandbox") {
		t.Errorf("the pattern's line does not name the repository it matches among those this machine knows:\n%s", all)
	}
	for _, w := range []string{
		"the effective role in bitbucket/acme/sandbox:",
		me + ": operator",
		root + ": admin",
		viewer + ": viewer",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("the listing does not say %q:\n%s", w, all)
		}
	}
	// The JSON carries the same answer under `effective`, and the pattern's
	// matches under `known`.
	jsonOut, _, err := userCommand(t, "ls", "--repo", "bitbucket/acme/sandbox", "--json")
	if err != nil {
		t.Fatalf("fugaro user ls --repo --json failed: %v", err)
	}
	var doc struct {
		Grants    []userLsGrant     `json:"grants"`
		Effective []userLsEffective `json:"effective"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatalf("the listing does not decode: %v\n%s", err, jsonOut)
	}
	want := []userLsEffective{
		{User: me, Repo: "bitbucket/acme/sandbox", Role: "operator"},
		{User: root, Repo: "bitbucket/acme/sandbox", Role: "admin"},
		{User: viewer, Repo: "bitbucket/acme/sandbox", Role: "viewer"},
	}
	if !slices.Equal(doc.Effective, want) {
		t.Errorf("the document's effective list is %+v, want %+v", doc.Effective, want)
	}
	for _, g := range doc.Grants {
		if g.Scope == "bitbucket/acme/*" && !slices.Equal(g.Known, []string{"bitbucket/acme/sandbox"}) {
			t.Errorf("the pattern grant's known list is %v, want the one repository this machine lists", g.Known)
		}
	}
	// A pattern is not a repository — --repo names one — and a repository no
	// pair of the node covers is said to be nobody's rather than left empty.
	// That needs a node without a project-scope pair: an admin at project scope
	// holds a role in every repository there is.
	patternOut, patternErr, err := userCommand(t, "ls", "--repo", "bitbucket/acme/*")
	all = accessOut(patternOut, patternErr, err)
	if err == nil {
		t.Fatalf("--repo took a pattern:\n%s", all)
	}
	if !strings.Contains(all, "is a pattern") {
		t.Errorf("the refusal does not say why a pattern is not a repository:\n%s", all)
	}
	_, _ = userPairRig(t, userPairLists{}, userStored(t, me, accesslib.RoleOperator, "bitbucket/acme/*"))
	noneOut, noneErr, err := userCommand(t, "ls", "--repo", "bitbucket/other/repo")
	all = accessOut(noneOut, noneErr, err)
	if err != nil {
		t.Fatalf("a repository outside every grant failed the listing: %v\n%s", err, all)
	}
	if !strings.Contains(all, "nobody holds a role in bitbucket/other/repo") {
		t.Errorf("a repository nobody holds a role in says nothing:\n%s", all)
	}
}

// TestUserLsReportsARepositoryOutsideAllowRepos: the published allow_repos
// gates the effective role before any grant is read (access.Effective), so a
// repository it does not admit has an answer no grant can change — and the
// roster above the answer may well hold a pair whose scope covers it. The
// listing then says what the policy does, as its own help text promises,
// rather than the sentence a repository nobody's scope covers gets: "no stored
// grant covers it" would be false of exactly that pair, and a reader would go
// looking for the grant the listing is showing them.
func TestUserLsReportsARepositoryOutsideAllowRepos(t *testing.T) {
	const me = "user:me@example.com"
	_, db := userPairRig(t, userPairLists{},
		userStored(t, me, accesslib.RoleAdmin, "project"))
	// A published policy that admits one repository and not the one asked about.
	// The pair above is at project scope, so it covers every repository there
	// is: the only thing that keeps the role out of the answer is the list. The
	// rules marker is what makes the node readable at all (projectpolicy.Read):
	// without it an installation's rules are older than the policy nodes, and
	// every reader answers "no policy published".
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	db.Set(projectpolicy.PathShared(), &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		Budget:     projectpolicy.Budget{Mode: "enforce", PerRunUSD: 5},
		AllowRepos: []string{"github/acme/app"},
	})
	out, errOut, err := userCommand(t, "ls", "--repo", "github/other/repo")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro user ls --repo failed: %v\n%s", err, all)
	}
	if !strings.Contains(all, "does not admit github/other/repo") || !strings.Contains(all, "allow_repos") {
		t.Errorf("a repository outside allow_repos is not reported as such:\n%s", all)
	}
	if strings.Contains(all, "no stored grant covers it") {
		t.Errorf("the listing says no stored grant covers the repository while listing the project-scope pair that does:\n%s", all)
	}
	// The roster is still the node's, which is what makes the wrong sentence
	// wrong: the pair is printed, and it covers the repository asked about.
	if !strings.Contains(all, me+" (admin) at project") {
		t.Errorf("the roster does not name the pair that covers the repository:\n%s", all)
	}
	// The repository the policy does admit keeps the other answer.
	appOut, appErr, err := userCommand(t, "ls", "--repo", "github/acme/app")
	all = accessOut(appOut, appErr, err)
	if err != nil {
		t.Fatalf("fugaro user ls --repo failed: %v\n%s", err, all)
	}
	if !strings.Contains(all, me+": admin") {
		t.Errorf("the admitted repository does not carry the role the pair holds:\n%s", all)
	}
}

// TestUserLsPrintsTheRosterWithNoGrantsNode: an installation that has not
// migrated its lists into the node — every installation until its first
// post-RS3 `fugaro update` — has its roles in this machine's two lists, and
// those lists are what `--json` carries under run_operators and admins and
// what a person reading the roles has to see. A sentence naming the config the
// reader would have to open instead is not a listing.
func TestUserLsPrintsTheRosterWithNoGrantsNode(t *testing.T) {
	const ops, root, legacy = "user:ops@example.com", "user:root@example.com", "group:eng@example.com"
	_, db := userPairRig(t, userPairLists{
		operators: []string{ops, legacy},
		admins:    []string{root},
	})
	if accessNode(t, db).Found {
		t.Fatal("the rig wrote a grants node: this case is about an installation that has none")
	}
	out, errOut, err := userCommand(t, "ls")
	all := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("fugaro user ls failed: %v\n%s", err, all)
	}
	for _, w := range []string{
		"has no grants node at " + accesslib.PathNode,
		"operators of " + initProjectName, ops,
		"admins of " + initProjectName, root,
	} {
		if !strings.Contains(all, w) {
			t.Errorf("the listing does not say %q:\n%s", w, all)
		}
	}
	// The one thing the two lists do not say is kept: a member a grant cannot
	// hold is still marked as the legacy member F25 makes of it.
	if !strings.Contains(all, legacy+" (operator)") || !strings.Contains(all, "legacy") {
		t.Errorf("the listing drops the legacy marker its lists' members are owed:\n%s", all)
	}
}

// the roles release has to refuse: an installation whose roles are still this
// machine's two IAM lists has no grants node, and the next `fugaro update`
// derives those lists FROM the node (RS3). A node created by this command would
// hold the one pair it writes and nobody else, so that update would derive the
// lists from it and drop every member this machine still names — a grant that
// quietly revokes the installation's roster. The migration runs first.
//
// The control is the installation with nothing to lose: lists that hold nobody
// are written, which is also the case the command's own judgement allows for a
// caller the node names nowhere (the write is what proves them an implicit
// admin, F22's table).
func TestGrantRefusesOnUnmigratedLists(t *testing.T) {
	const other, root = "user:new@example.com", "user:root@example.com"
	t.Run("this machine's lists hold members", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{
			operators: []string{"user:ops@example.com"},
			admins:    []string{root},
		})
		userPairAs(t, root)
		all, err := userPairRun(t, "grant", other, "operator", "project")
		if err == nil {
			t.Fatalf("a grant created the node an installation's lists have not migrated into:\n%s", all)
		}
		for _, w := range []string{"no grants node at " + accesslib.PathNode, "fugaro update", root, "user:ops@example.com"} {
			if !strings.Contains(all, w) {
				t.Errorf("the refusal does not say %q:\n%s", w, all)
			}
		}
		if accessNode(t, db).Found {
			t.Errorf("the refused grant created the node:\n%s", all)
		}
	})
	t.Run("this machine's lists hold nobody", func(t *testing.T) {
		_, db := userPairRig(t, userPairLists{})
		userPairAs(t, root)
		all, err := userPairRun(t, "grant", other, "operator", "project")
		if err != nil {
			t.Fatalf("an installation with no list member to drop refused a grant: %v\n%s", err, all)
		}
		if got := userNodeLines(t, db); !slices.Equal(got, []string{other + " (operator) at project"}) {
			t.Errorf("the node holds %v, want the new pair:\n%s", got, all)
		}
	})
}

// TestRevokeIsNotUndoneByTheNextUpdate is the command's half of F29a at the door
// a person uses: the revoke takes the pair out of the node, and the converge
// after it derives the installation's IAM lists from the node — so the member
// is in no list that lands, and the run says it dropped them. A revoke that the
// next update re-granted would leave the person believing a role was taken away
// while it was not.
func TestRevokeIsNotUndoneByTheNextUpdate(t *testing.T) {
	noAgentSession(t)
	fakeTerminal(t)
	const ops, revoked = "user:ops@example.com", "user:revoked@example.com"
	r, db := accessRig(t, []string{ops, revoked}, nil, []string{ops, revoked}, nil)
	db.Set(accesslib.PathNode, accessNodeDoc(
		projectGrant(ops, accesslib.RoleAdmin),
		projectGrant(revoked, accesslib.RoleOperator)))
	userPairAs(t, ops)
	all, err := userPairRun(t, "revoke", revoked, "operator", "project")
	if err != nil {
		t.Fatalf("the revoke failed: %v\n%s", err, all)
	}
	if got := grantLines(accessGrants(t, db)); !slices.Equal(got, []string{ops + " (admin)"}) {
		t.Fatalf("the node holds %v, want the revoked member gone:\n%s", got, all)
	}
	out, errOut, err := executeStdin(t, "", "update")
	converge := accessOut(out, errOut, err)
	if err != nil {
		t.Fatalf("the update after a revoke failed: %v\n%s", err, converge)
	}
	// A project-scope admin lands on both of the installation's lists (its
	// roles are the operator's and the admin's), and the revoked member is on
	// neither: a list that carried them would re-grant the IAM the revoke took
	// away.
	for _, l := range []string{"launchers", "operators"} {
		if got := members(r.tfvars(t)[l]); !slices.Equal(got, []string{ops}) {
			t.Errorf("the plan's %s are %v, want the revoked member gone:\n%s", l, got, converge)
		}
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config the run wrote does not load: %v\n%s", err, converge)
	}
	if !slices.Equal(lc.Terraform.Operators, []string{ops}) || !slices.Equal(lc.Terraform.Admins, []string{ops}) {
		t.Errorf("this machine's own lists are %v / %v, want the revoked member gone from both:\n%s",
			lc.Terraform.Operators, lc.Terraform.Admins, converge)
	}
	if !strings.Contains(converge, revoked) {
		t.Errorf("the run drops the member in silence:\n%s", converge)
	}
}

// TestGrantRefusesASelectionWithNoFile: the drift guard above reads THIS
// MACHINE's two lists, because an absent grants node is both "not migrated" and
// "the lists hold nobody" (access.Node) and only those lists tell the two
// apart. A machine whose project was selected from the shared config has no
// config file at all — a shared config carries no terraform block — so those
// lists read empty and the guard would not fire: a grant there would create a
// node holding one pair on an installation whose lists are full, and the next
// `fugaro update` derives the IAM lists from that node and drops every member
// they held (accessDropped). The refusal is the one user add and rm meet, and
// it comes before the database is opened.
func TestGrantRefusesASelectionWithNoFile(t *testing.T) {
	noAgentSession(t)
	noTerminal(t)
	f := sharedEnd2End(t)
	// The installation's own database, marked for the project the shared config
	// names, and pointed at from that config: if the refusal were not there the
	// command would reach a real write, which is what makes this case worth
	// pinning rather than a refusal for a database it could not open anyway.
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathMark, map[string]any{
		"managed_by": "fugaro", "project": initProjectName, "gcp_project": "proj-1234", "version": 1,
	})
	db.Set(budget.PathProject, initProjectName)
	inner := sharedFetch
	sharedFetch = func(ctx context.Context, getenv func(string) string, now time.Time, name, gcp string) (*localcfg.Config, string, error) {
		c, note, err := inner(ctx, getenv, now, name, gcp)
		if err == nil && c != nil && c.Budget != nil {
			c.Budget.RTDBURL = db.URL
		}
		return c, note, err
	}
	t.Cleanup(func() { sharedFetch = inner })
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: "+initProjectName+"\ngcp_project: proj-1234\n"))
	userPairAs(t, "user:root@example.com")

	all, err := userPairRun(t, "grant", "user:new@example.com", "operator", "project")
	if err == nil {
		t.Fatalf("a grant written from a machine with no config of its own created the installation's node:\n%s", all)
	}
	for _, w := range []string{"fugaro setup", "shared config", userListSource} {
		if !strings.Contains(all, w) {
			t.Errorf("the refusal does not say %q:\n%s", w, all)
		}
	}
	if accessNode(t, db).Found {
		t.Errorf("the refused grant created the node:\n%s", all)
	}
}
