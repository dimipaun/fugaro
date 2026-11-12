package cli

// RS6c's enforcement at the command level: the image, budget, accounts, secrets
// and project-repository commands ask the resolver before they write
// (docs/roles-scopes/implementation-plan.md, findings F19 and D1584/D1586).
//
// Every case is a refusal first and a write second: the sentence is half the
// answer and "nothing was written" is the other half, so a case that only read
// the message would pass a guard that refused after the write had landed.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

const (
	// accessAdminAddr is the caller every case seats: the addresses are the
	// fixture's own and the roles are what each case grants it.
	accessAdminAddr = "admin@example.com"
	// accessRepoCanon is the one repository of the budget fixture's config,
	// canonically, which is the form a grant's scope is written in.
	accessRepoCanon = "github/acme/app"
)

// accessFixture is the budget fixture the whole file works from: it has the
// backend the grants node lives beside, which is what makes the gate a check
// rather than a no-op, and — where the case is about the catalog — the image
// catalog its config carries.
func accessFixture(t *testing.T, catalog bool) *budgetFixture {
	t.Helper()
	noAgentSession(t)
	f := newBudgetFixture(t, "")
	if catalog {
		f.appendConfig(t, catalogYAML)
	}
	return f
}

// accessMember is an address in the canonical member form the node stores and
// the resolver compares a caller against.
func accessMember(t *testing.T, address string) string {
	t.Helper()
	m, err := accesslib.Member(address)
	if err != nil {
		t.Fatalf("the test's own address %q: %v", address, err)
	}
	return m
}

// accessAs stores the roster and makes address the credential the command
// judges with (userPairAs points the one seam access.Caller is read through at
// it), so no case depends on who is signed in on the machine running the suite.
func accessAs(t *testing.T, db *gcpfake.RTDB, address string, grants ...accesslib.Grant) {
	t.Helper()
	db.Set(accesslib.PathNode, accessNodeDoc(grants...))
	userPairAs(t, accessMember(t, address))
}

// accessRefused asserts a command was refused as a user error whose sentence
// says what wants.
func accessRefused(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("the command was allowed")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("exit %d, want %d — a refusal, not a failure: %v", got, ExitUserError, err)
	}
	got := ErrorText(err)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, got)
		}
	}
}

// accessConfigText is the project config as it is on disk, for a case that has
// to prove a refused command left it alone.
func accessConfigText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// accessDockerfile is the file `image add --inline` reads.
func accessDockerfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "extra.Dockerfile")
	if err := os.WriteFile(path, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRepoAdminCannotAddCatalogImage is RS6c's own named case: the image
// catalog is the project's (F19, and D1584 for the entries themselves), so a
// grant that covers one repository is not a grant on the catalog — an all-repos
// admin is a repository admin of every enrolled repository and holds none of
// the project-level actions. The refused command leaves the file alone.
func TestRepoAdminCannotAddCatalogImage(t *testing.T) {
	f := accessFixture(t, true)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, accessRepoCanon))
	before := accessConfigText(t)
	_, _, err := execute(t, "image", "add", "extra", "--tags", "redis", "--inline", accessDockerfile(t), "--description", "a redis image")
	accessRefused(t, err,
		"nothing was written",
		"admin at project scope",
		"(user:admin@example.com) hold admin at "+accessRepoCanon)
	if after := accessConfigText(t); after != before {
		t.Errorf("the refused add rewrote the project config:\n%s", after)
	}
}

// TestAccessImageProjectAdminAddsCatalogImage is the other half: the same
// command, the same fixture, a grant at project scope — allowed, and the entry
// is in the file. Without it a gate that refused everything would pass every
// refusal case above.
func TestAccessImageProjectAdminAddsCatalogImage(t *testing.T) {
	f := accessFixture(t, true)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "project"))
	out, _, err := execute(t, "image", "add", "extra", "--tags", "redis", "--inline", accessDockerfile(t), "--description", "a redis image")
	if err != nil {
		t.Fatalf("a project admin could not add a catalog entry: %v", err)
	}
	if !strings.Contains(out, "extra") {
		t.Errorf("image add printed %q, want the entry it wrote", out)
	}
	if !strings.Contains(accessConfigText(t), "extra") {
		t.Error("the entry is not in the project config")
	}
}

// TestAllReposAdminCannotSetGlobalBudget is RS6c's second named case: the
// project's own caps are a project-level action (F19), and a grant at all-repos
// is a repository grant over every repository — it is not the project. The
// refused command writes no cap.
func TestAllReposAdminCannotSetGlobalBudget(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "all-repos"))
	_, _, err := execute(t, "budget", "set", "--global", "--per-run", "3")
	accessRefused(t, err,
		"nothing was set",
		"admin at project scope",
		"(user:admin@example.com) hold admin at all-repos")
	if v := f.db.Value(budget.PathCapsGlobal); v != nil {
		t.Errorf("the refused set wrote %s = %v", budget.PathCapsGlobal, v)
	}
}

// TestAccessBudgetRepoAdminSetsItsOwnRepo: `budget set --repo X` needs admin on
// X, and nothing more — a repository admin sets its own repository's caps, and
// the write lands.
func TestAccessBudgetRepoAdminSetsItsOwnRepo(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, accessRepoCanon))
	if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "5"); err != nil {
		t.Fatalf("a repository admin could not set its own caps: %v", err)
	}
	got := repoCapsOf(t, f, appSlug)
	if got == nil || got["repo"] != "acme/app" {
		t.Fatalf("acme/app's caps = %v, want the write", got)
	}
}

// TestAccessBudgetViewerCannotSetItsRepo: the role is what decides, not the
// repository — a viewer reads and does not set.
func TestAccessBudgetViewerCannotSetItsRepo(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleViewer, accessRepoCanon))
	_, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "5")
	accessRefused(t, err,
		"nothing was set",
		"needs admin on "+accessRepoCanon,
		"(user:admin@example.com) hold viewer at "+accessRepoCanon)
	if v := f.db.Value(budget.PathCapsRepo(appSlug)); v != nil {
		t.Errorf("the refused set wrote %s = %v", budget.PathCapsRepo(appSlug), v)
	}
}

// TestAccessBudgetAdminOnAnotherRepoCannotSetThisOne: an admin's scope is the
// repositories it covers, so a grant over another repository is not a grant
// over this one.
func TestAccessBudgetAdminOnAnotherRepoCannotSetThisOne(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "github/acme/web"))
	_, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "5")
	accessRefused(t, err,
		"nothing was set",
		"needs admin on "+accessRepoCanon,
		"(user:admin@example.com) hold admin at github/acme/web")
	if v := f.db.Value(budget.PathCapsRepo(appSlug)); v != nil {
		t.Errorf("the refused set wrote %s = %v", budget.PathCapsRepo(appSlug), v)
	}
}

// TestAccessBudgetProjectViewerCannotSetGlobal: reading the project's caps is
// the viewer's, setting them is the admin's.
func TestAccessBudgetProjectViewerCannotSetGlobal(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleViewer, "project"))
	_, _, err := execute(t, "budget", "set", "--global", "--per-run", "3")
	accessRefused(t, err, "nothing was set", "admin at project scope", "(user:admin@example.com) hold viewer at project")
	if v := f.db.Value(budget.PathCapsGlobal); v != nil {
		t.Errorf("the refused set wrote %s = %v", budget.PathCapsGlobal, v)
	}
}

// TestAccessBudgetProjectAdminSetsGlobal is the allowed half of the case above.
func TestAccessBudgetProjectAdminSetsGlobal(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "project"))
	if _, _, err := execute(t, "budget", "set", "--global", "--per-run", "3"); err != nil {
		t.Fatalf("a project admin could not set the global cap: %v", err)
	}
	if v := f.db.Value(budget.PathCapsGlobal); v == nil {
		t.Errorf("the global caps were not written")
	}
}

// TestAccessBudgetShowIsAProjectRead: the spend document is the project's own
// (D1586, and F19's project-level reads) — the `global` block, the project
// figures and every repository's row in one document — so it is read by a
// project-scope role. A repository-scoped grant, and an all-repos one, hold
// none of the project-level reads: their repositories' runs and logs are theirs
// and the project's spend is not.
func TestAccessBudgetShowIsAProjectRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  accesslib.Role
		scope string
	}{
		{"a repository viewer", accesslib.RoleViewer, accessRepoCanon},
		{"a repository admin", accesslib.RoleAdmin, accessRepoCanon},
		{"an all-repos admin", accesslib.RoleAdmin, "all-repos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := accessFixture(t, false)
			accessAs(t, f.db, accessAdminAddr,
				userStored(t, accessMember(t, accessAdminAddr), tc.role, tc.scope))
			_, _, err := execute(t, "budget", "show")
			accessRefused(t, err, "project-level", "project scope")
		})
	}
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleViewer, "project"))
	if _, _, err := execute(t, "budget", "show"); err != nil {
		t.Fatalf("a project viewer could not read the spend document: %v", err)
	}
}

// TestAccessReposAllReposAdminCannotEditTheAllowList: the allow-list is what
// admits a repository to the project (F19 names `project repos add/rm` among
// the project-level actions), so an admin of every enrolled repository still
// cannot widen it.
func TestAccessReposAllReposAdminCannotEditTheAllowList(t *testing.T) {
	f := accessFixture(t, false)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "all-repos"))
	before := allowReposOf(t, f.db)
	_, _, err := execute(t, "project", "repos", "add", "github/acme/new")
	accessRefused(t, err, "nothing was added to the allow-list", "admin at project scope", "(user:admin@example.com) hold admin at all-repos")
	if after := allowReposOf(t, f.db); !slices.Equal(after, before) {
		t.Errorf("the refused add rewrote the published allow-list: %v, want %v", after, before)
	}
}

// TestAccessAccountsAllReposAdminCannotAddAnAccount: accounts are on F19's
// project-level list for the same reason the catalog is — the account is the
// project's, and what it may be mounted for is a project-wide list.
func TestAccessAccountsAllReposAdminCannotAddAnAccount(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "all-repos"))
	_, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
	accessRefused(t, err, "nothing was written", "admin at project scope", "(user:admin@example.com) hold admin at all-repos")
	if strings.Contains(accessConfigText(t), "team-a") {
		t.Error("the refused add declared the account in the project config")
	}
}

// TestAccessSecretsRepoAdminCannotWriteTheProjectCopy: a secret's scope is what
// the write is about — the project's own copy is a project-level action, and
// the repository's own copy needs admin on that repository (which is the one
// thing a repository-scoped grant is for).
func TestAccessSecretsRepoAdminCannotWriteTheProjectCopy(t *testing.T) {
	t.Run("the project's copy", func(t *testing.T) {
		f := accessFixture(t, false)
		sm := gcpfake.NewSecrets(t)
		f.rewriteConfig(t, "endpoints: { no_auth: true }", "endpoints: { secret_manager: "+sm.URL+"/, no_auth: true }")
		accessAs(t, f.db, accessAdminAddr,
			userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, accessRepoCanon))
		_, _, err := executeStdin(t, "value\n", "secrets", "set", config.AnthropicSecret, "--project-wide")
		accessRefused(t, err, "nothing was written", "admin at project scope", "(user:admin@example.com) hold admin at "+accessRepoCanon)
		if got := sm.Latest(gcp.ProjectSecretID("aurora", config.AnthropicSecret)); len(got) != 0 {
			t.Errorf("the refused set stored the project's copy: %q", got)
		}
	})
	t.Run("another repository's copy", func(t *testing.T) {
		f := accessFixture(t, false)
		sm := gcpfake.NewSecrets(t)
		f.rewriteConfig(t, "endpoints: { no_auth: true }", "endpoints: { secret_manager: "+sm.URL+"/, no_auth: true }")
		accessAs(t, f.db, accessAdminAddr,
			userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "github/acme/web"))
		_, _, err := executeStdin(t, "value\n", "secrets", "set", config.AnthropicSecret, "--repo", "acme/app")
		accessRefused(t, err, "nothing was written", "needs admin on "+accessRepoCanon, "(user:admin@example.com) hold admin at github/acme/web")
		if got := sm.Latest(gcp.SecretID(appSlug, config.AnthropicSecret)); len(got) != 0 {
			t.Errorf("the refused set stored acme/app's copy: %q", got)
		}
	})
}

// TestAccessWithoutAGrantsNodeTheCommandsAreUnchanged pins the one rule that
// keeps this task's checks off installations that have not migrated yet: the
// roster is the node (F21 — the machine's own lists are text a person edits,
// and a role read from them would be a courtesy), so a project with no node has
// no roster to judge against and its commands behave exactly as they did. The
// node arrives with `fugaro update`, the one command of the HARD RULE, and the
// same update is what makes every check above bite.
func TestAccessWithoutAGrantsNodeTheCommandsAreUnchanged(t *testing.T) {
	f := accessFixture(t, false)
	userPairAs(t, accessMember(t, accessAdminAddr))
	if _, _, err := execute(t, "budget", "set", "--global", "--per-run", "3"); err != nil {
		t.Fatalf("a project with no grants node refused a budget set: %v", err)
	}
	if v := f.db.Value(budget.PathCapsGlobal); v == nil {
		t.Error("the global caps were not written")
	}
}

// TestAccessUnreadableGrantsNodeIsARefusal pins the other half of the rule
// above: a node that is there and cannot be read is a refusal, never an empty
// roster. Failing open here would hand the project's own surfaces to whoever
// the database cannot answer for, which is the opposite of what the node is for.
func TestAccessUnreadableGrantsNodeIsARefusal(t *testing.T) {
	f := accessFixture(t, false)
	f.db.Set(accesslib.PathNode, "not a grants node")
	userPairAs(t, accessMember(t, accessAdminAddr))
	_, _, err := execute(t, "budget", "set", "--global", "--per-run", "3")
	if err == nil {
		t.Fatal("a roster that could not be read allowed a project-level write")
	}
	if v := f.db.Value(budget.PathCapsGlobal); v != nil {
		t.Errorf("the refused set wrote %s = %v", budget.PathCapsGlobal, v)
	}
}

// TestAccessGateRefusesHTTP2DebugBeforeItOpens: the gate is the first cloud
// open of commands that were purely local before this task — `accounts add`
// edits the config and talked to nothing — so it carries the guard every other
// open carries. Without it the request the gate makes would be one Go prints
// under GODEBUG=http2debug, bearer token and all.
func TestAccessGateRefusesHTTP2DebugBeforeItOpens(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, accessRepoCanon))
	before := accessConfigText(t)
	t.Setenv("GODEBUG", "http2debug=2")
	_, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--yes")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("accounts add: err = %v, want the GODEBUG refusal", err)
	}
	if after := accessConfigText(t); after != before {
		t.Errorf("the GODEBUG refusal wrote the project config:\n%s", after)
	}
}
