package access

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The resolver, held to the rules docs/roles-scopes pins for RS1: the highest
// role wins, a project-scope role is the only one that holds a project-level
// action, all-repos covers a repository enrolled later, a pattern's star stays
// inside one path segment, the two keywords are never repositories, a lone "*"
// is never a scope, a grant is normalised before it is stored, a grant is a
// subset of its grantor's scope (F17.1), a grant that does not parse grants
// nothing, and the caller is the credential rather than `git config user.email`
// (F21).

const (
	alice = "user:alice@example.com"
	bob   = "user:bob@example.com"
	carol = "user:carol@example.com"
)

// scope is ParseScope for a value the test knows is valid, so a test reads as
// the grants it is about.
func scope(t *testing.T, value string) Scope {
	t.Helper()
	s, err := ParseScope(value)
	if err != nil {
		t.Fatalf("ParseScope(%q): %v", value, err)
	}
	return s
}

// knows is an Input whose policy admits repos and nothing else.
func knows(grants ...Grant) Input {
	return Input{Grants: grants, AllowRepos: []string{"github/acme/app", "github/acme/api", "github/acme/api-v2", "github/acme/group/nested", "bitbucket/other/thing"}}
}

func grant(user string, role Role, s Scope) Grant {
	return Grant{User: user, Role: role, Scope: s}
}

// TestHighestRoleWins: the spec's "in any place, the highest role that applies
// there counts" — the answer is the best of the grants that cover the
// repository, never the first and never the sum.
func TestHighestRoleWins(t *testing.T) {
	in := knows(
		grant(alice, RoleOperator, scope(t, "project")),
		grant(alice, RoleAdmin, scope(t, "github/acme/api-*")),
		grant(bob, RoleViewer, scope(t, "all-repos")),
	)
	cases := []struct {
		caller, repo string
		want         Role
	}{
		{alice, "github/acme/api-v2", RoleAdmin},
		{alice, "github/acme/api", RoleOperator},
		{alice, "github/acme/app", RoleOperator},
		{alice, "bitbucket/other/thing", RoleOperator},
		{bob, "github/acme/app", RoleViewer},
		{bob, "bitbucket/other/thing", RoleViewer},
		{carol, "github/acme/app", RoleNone},
		{"", "github/acme/app", RoleNone},
		{alice, "", RoleNone},
	}
	for _, c := range cases {
		if got := Effective(in, c.caller, c.repo); got != c.want {
			t.Errorf("Effective(%s, %s) = %q, want %q", c.caller, c.repo, got, c.want)
		}
	}

	// Order is not a rule: the same grants in the other order answer the same.
	reversed := knows(in.Grants[2], in.Grants[1], in.Grants[0])
	if got := Effective(reversed, alice, "github/acme/api-v2"); got != RoleAdmin {
		t.Errorf("Effective with the grants reversed = %q, want %q: the highest role wins, whatever order the node stores them in", got, RoleAdmin)
	}
}

// TestProjectScopeOnlyForProjectActions: an all-repos admin is an admin of
// every repository and of nothing at the project level (F19) — the platform
// lead of the spec's examples.
func TestProjectScopeOnlyForProjectActions(t *testing.T) {
	platformLead := knows(grant(alice, RoleAdmin, scope(t, "all-repos")))
	repo := "github/acme/app"
	if !Allowed(platformLead, alice, ActionManageRepo, repo) {
		t.Errorf("an all-repos admin is refused %s on %s: every repository is what all-repos covers", ActionManageRepo, repo)
	}
	if Allowed(platformLead, alice, ActionManageProject, repo) {
		t.Errorf("an all-repos admin is allowed %s: the project-level actions (the guardrails, allow_repos, the catalog, accounts, the global budget, the grants of project or all-repos scope) are exactly what all-repos does not hold (F19)", ActionManageProject)
	}
	if Allowed(platformLead, alice, ActionViewProject, repo) {
		t.Errorf("an all-repos admin is allowed %s: project-level reads (spend, members) need a project-scope role (F19, D1586)", ActionViewProject)
	}

	projectAdmin := knows(grant(alice, RoleAdmin, scope(t, "project")))
	for _, action := range []Action{ActionManageRepo, ActionManageProject, ActionViewProject, ActionViewRepo, ActionLaunch, ActionCancel} {
		if !Allowed(projectAdmin, alice, action, repo) {
			t.Errorf("a project admin is refused %s: project scope covers the project and every repository in it", action)
		}
	}

	viewer := knows(grant(bob, RoleViewer, scope(t, "project")))
	if !Allowed(viewer, bob, ActionViewRepo, repo) || !Allowed(viewer, bob, ActionViewProject, repo) {
		t.Errorf("a viewer is refused a read: viewer is the read-only role")
	}
	for _, action := range []Action{ActionLaunch, ActionCancel, ActionManageRepo, ActionManageProject} {
		if Allowed(viewer, bob, action, repo) {
			t.Errorf("a viewer is allowed %s: a viewer cannot launch, cancel or change anything", action)
		}
	}

	// An action this package does not know is refused rather than allowed.
	if Allowed(projectAdmin, alice, Action("not-an-action"), repo) {
		t.Errorf("an unknown action is allowed: the action set is closed and a typo must not become a permission")
	}
	if Allowed(projectAdmin, carol, ActionManageRepo, repo) {
		t.Errorf("a caller with no grant at all is allowed %s", ActionManageRepo)
	}
}

// TestAllReposCoversLaterEnrolled: "all repos, including repos added later" —
// the grant names no repository, so a repository the policy enrolls after it
// is covered with no change to the grant, and a repository the policy does not
// admit is covered by nobody (F7, F27).
func TestAllReposCoversLaterEnrolled(t *testing.T) {
	in := Input{Grants: []Grant{grant(alice, RoleAdmin, scope(t, "all-repos"))}, AllowRepos: []string{"github/acme/app"}}
	if got := Effective(in, alice, "github/acme/app"); got != RoleAdmin {
		t.Errorf("Effective before any change = %q, want %q", got, RoleAdmin)
	}
	in.AllowRepos = append(in.AllowRepos, "bitbucket/newcomer/thing")
	if got := Effective(in, alice, "bitbucket/newcomer/thing"); got != RoleAdmin {
		t.Errorf("Effective on a repository enrolled after the grant = %q, want %q: all-repos is re-evaluated at the check and covers a repository added later with no change to the grant", got, RoleAdmin)
	}
	if got := Effective(in, alice, "github/acme/elsewhere"); got != RoleNone {
		t.Errorf("Effective on a repository the policy does not admit = %q, want %q: a repository outside allow_repos cannot run at all, so no scope admits it (F27)", got, RoleNone)
	}

	// The project scope is gated the same way: the gate is about the
	// repository, not about the caller's rank.
	project := Input{Grants: []Grant{grant(alice, RoleAdmin, scope(t, "project"))}, AllowRepos: []string{"github/acme/app"}}
	if got := Effective(project, alice, "github/acme/elsewhere"); got != RoleNone {
		t.Errorf("Effective of a project admin on an un-admitted repository = %q, want %q", got, RoleNone)
	}
}

// TestPatternStarStaysInsideASegment: "*" matches inside one path segment and
// never a "/", so a pattern admits a repository at its own depth and nothing
// nested under it.
func TestPatternStarStaysInsideASegment(t *testing.T) {
	in := knows(grant(alice, RoleAdmin, scope(t, "github/acme/*")))
	if got := Effective(in, alice, "github/acme/app"); got != RoleAdmin {
		t.Errorf("Effective(github/acme/app) = %q, want %q", got, RoleAdmin)
	}
	if got := Effective(in, alice, "github/acme/group/nested"); got != RoleNone {
		t.Errorf("Effective(github/acme/group/nested) = %q, want %q: the star never crosses \"/\", and the nested repository is admitted by the policy but by no grant", got, RoleNone)
	}
	if got := Effective(in, alice, "bitbucket/other/thing"); got != RoleNone {
		t.Errorf("Effective(bitbucket/other/thing) = %q, want %q: the provider segment is a literal too", got, RoleNone)
	}

	// A star inside a segment is a prefix rule, not a substring one.
	prefixed := knows(grant(bob, RoleAdmin, scope(t, "github/acme/api*")))
	if got := Effective(prefixed, bob, "github/acme/api-v2"); got != RoleAdmin {
		t.Errorf("Effective(github/acme/api-v2) = %q, want %q", got, RoleAdmin)
	}
	if got := Effective(prefixed, bob, "github/acme/app"); got != RoleNone {
		t.Errorf("Effective(github/acme/app) = %q, want %q: a star is a wildcard, not a licence to match any name", got, RoleNone)
	}
}

// TestBareStarIsRefusedAsScopedPattern: a lone "*" is not a scope value. It is
// "all-repos"' meaning, and "all-repos" is reserved to a project admin, so
// accepting "*" would let an all-repos holder mint a scope they do not hold.
func TestBareStarIsRefusedAsScopedPattern(t *testing.T) {
	for _, value := range []string{"*", " * ", "github/acme/app,*", "*,github/acme/app"} {
		_, err := ParseScope(value)
		if err == nil {
			t.Errorf("ParseScope(%q) parsed: a lone %q is refused as a scope value", value, "*")
			continue
		}
		if !strings.Contains(err.Error(), "all-repos") {
			t.Errorf("ParseScope(%q) refused with %q, which does not name all-repos: the refusal has to say what to write instead", value, err)
		}
	}

	// A star inside a segment is ordinary, and the provider segment is not a
	// place for one: MatchRepo admits "*/acme/*" for the allow-list, and a
	// grant is narrower than the allow-list.
	for _, value := range []string{"github/acme/*", "github/*/app"} {
		if _, err := ParseScope(value); err != nil {
			t.Errorf("ParseScope(%q): %v, want it admitted: a star is ordinary inside a segment once the provider is named", value, err)
		}
	}
	for _, value := range []string{"*/acme/*", "github/acme/**", "github/acme/app/**"} {
		if _, err := ParseScope(value); err == nil {
			t.Errorf("ParseScope(%q) parsed: a provider wildcard and a doubled star are refused, so a pattern cannot be read as reaching further than it does", value)
		}
	}
	// A bare owner/name names no provider, exactly as `fugaro project repos
	// add` refuses it.
	if _, err := ParseScope("acme/*"); err == nil {
		t.Errorf("ParseScope(acme/*) parsed: a pattern without a provider is refused (F5)")
	}
}

// TestKeywordsAreNeverRepositories: "project" and "all-repos" are whole scopes
// and can never parse as a repository. A canonical repository always carries a
// provider and a "/", so the two words cannot collide with one — which is why
// the reservation is this test rather than a refusal in `repos add` (F6).
func TestKeywordsAreNeverRepositories(t *testing.T) {
	for word, kind := range map[string]ScopeKind{"project": ScopeProject, "all-repos": ScopeAllRepos} {
		s := scope(t, word)
		if s.Kind != kind || len(s.Repos) != 0 {
			t.Errorf("ParseScope(%q) = %+v, want the %v scope with no repositories", word, s, kind)
		}
		if _, err := ParseScope(word + ",github/acme/app"); err == nil {
			t.Errorf("ParseScope(%q,...) parsed: a whole scope cannot be mixed with repositories", word)
		}
		// A hand-built repository list naming the keyword is not a scope this
		// package built, so the keyword cannot be smuggled in as one.
		if (Scope{Kind: ScopeRepos, Repos: []string{word}}).Valid() {
			t.Errorf("a repository list naming %q is valid: the two words are scopes, never repositories", word)
		}
	}

	// A repository whose own name is one of the words is an ordinary
	// repository: only the whole value is reserved.
	for _, value := range []string{"github/acme/project", "github/acme/all-repos"} {
		if s := scope(t, value); s.Kind != ScopeRepos || len(s.Repos) != 1 || s.Repos[0] != value {
			t.Errorf("ParseScope(%q) = %+v, want the repository pattern itself", value, s)
		}
	}
}

// TestPatternsAreCanonicalised: a grant is stored in the canonical spelling of
// the repository, so a case variant and a clone URL's ".git" are one grant
// (F17.4), the stored list is sorted and deduplicated, and a name the
// repository tree would not fold is refused rather than folded (the ASCII fold
// is task.ASCIILower's, never strings.ToLower's).
func TestPatternsAreCanonicalised(t *testing.T) {
	cases := []struct {
		value string
		want  []string
	}{
		{"GitHub/ACME/API-*", []string{"github/acme/api-*"}},
		{"github/acme/app.git", []string{"github/acme/app"}},
		{"github/acme/b,github/acme/a,github/acme/b", []string{"github/acme/a", "github/acme/b"}},
		{" github/acme/app , github/acme/api ", []string{"github/acme/api", "github/acme/app"}},
	}
	for _, c := range cases {
		s := scope(t, c.value)
		if len(s.Repos) != len(c.want) {
			t.Fatalf("ParseScope(%q).Repos = %q, want %q", c.value, s.Repos, c.want)
		}
		for i := range c.want {
			if s.Repos[i] != c.want[i] {
				t.Errorf("ParseScope(%q).Repos = %q, want %q", c.value, s.Repos, c.want)
			}
		}
	}

	// The stored spelling is the one the check compares with.
	in := knows(grant(alice, RoleViewer, scope(t, "GitHub/ACME/App.git")))
	if got := Effective(in, alice, "github/acme/app"); got != RoleViewer {
		t.Errorf("Effective(github/acme/app) = %q, want %q: a case or \".git\" variant is normalised before it is stored, so it grants the canonical repository", got, RoleViewer)
	}

	// The Kelvin sign is what makes the fold load-bearing: strings.ToLower
	// turns it into "k", so a name folded that way would merge two
	// repositories a reader sees as different.
	if _, err := ParseScope("github/acme/Kapp"); err == nil {
		t.Errorf("ParseScope(github/acme/KELVIN SIGN app) parsed: a non-ASCII name is refused rather than folded into an ASCII one")
	}
}

// TestGrantMustBeSubsetOfGrantorScope: F17.1 — nobody grants a scope wider
// than the one they hold. A repo admin grants within their own pattern and no
// further; only a project admin grants "project" or "all-repos".
func TestGrantMustBeSubsetOfGrantorScope(t *testing.T) {
	repoAdmin := scope(t, "github/acme/api-*")
	for _, c := range []struct {
		grantee string
		want    bool
	}{
		{"github/acme/api-*", true},
		{"github/acme/api-v2", true},
		{"github/acme/api-v2,github/acme/api-v3", true},
		{"github/acme/*", false},
		{"github/acme/app", false},
		{"github/acme/api", false},
		{"github/acme/group/nested", false},
		{"bitbucket/other/thing", false},
		{"all-repos", false},
		{"project", false},
	} {
		if got := SubsetOf(scope(t, c.grantee), repoAdmin); got != c.want {
			t.Errorf("SubsetOf(%q, %q) = %v, want %v: a grant that is not a subset of the grantor's scope is how a scoped admin widens their own reach", c.grantee, "github/acme/api-*", got, c.want)
		}
	}

	allRepos := scope(t, "all-repos")
	if !SubsetOf(allRepos, allRepos) {
		t.Errorf("SubsetOf(all-repos, all-repos) = false, want true")
	}
	if SubsetOf(scope(t, "project"), allRepos) {
		t.Errorf("SubsetOf(project, all-repos) = true: an all-repos admin cannot grant project")
	}
	if !SubsetOf(scope(t, "github/acme/app"), allRepos) {
		t.Errorf("SubsetOf(github/acme/app, all-repos) = false: all-repos covers every repository list")
	}

	project := scope(t, "project")
	for _, grantee := range []string{"project", "all-repos", "github/acme/app", "github/acme/*"} {
		if !SubsetOf(scope(t, grantee), project) {
			t.Errorf("SubsetOf(%q, project) = false: project covers everything", grantee)
		}
	}
}

// TestSubsetHostileCases: the patterns a subset test gets wrong if it reasons
// about strings instead of segments. Two properties matter more than any row:
// a false answer is a refusal a person can read, and a true answer must never
// be an escalation — so every row that is not clear is false.
func TestSubsetHostileCases(t *testing.T) {
	cases := []struct {
		grantee, grantor string
		want             bool
		why              string
	}{
		{"github/acme/api*", "github/acme/api-*", false, "api* matches apiX, which api-* does not"},
		{"github/acme/api-*", "github/acme/api*", true, "everything api-* matches begins with api"},
		{"github/acme/a*b*", "github/acme/a*", true, "every match of a*b* begins with a"},
		{"github/acme/a*b*", "github/acme/a*c", false, "the grantor's segment does not end in a star, so there is no prefix to extend"},
		{"github/acme/*bc", "github/acme/abc", false, "the grantor has no trailing star to extend"},
		{"github/acme/*", "github/acme/a/b", false, "segment counts differ: a star never crosses \"/\""},
		{"github/acme/a/b", "github/acme/a/*", true, "same depth, and the last segment is star-covered"},
		{"github/acme/a/b", "github/acme/*", false, "github/acme/* does not cover a nested repository"},
		{"github/acme/api-v2", "github/acme/api-*", true, "the literal extends the grantor's prefix"},
		{"github/acme/api-v2*", "github/acme/api-*", true, "the pattern's literal prefix extends the grantor's"},
		{"github/acme/apix", "github/acme/api-*", false, "apix does not begin with api-"},
		{"github/acme/bpi", "github/acme/api*", false, "a star is not a substring match"},
	}
	for _, c := range cases {
		if got := SubsetOf(scope(t, c.grantee), scope(t, c.grantor)); got != c.want {
			t.Errorf("SubsetOf(%q, %q) = %v, want %v (%s)", c.grantee, c.grantor, got, c.want, c.why)
		}
	}

	// A list is covered only when every pattern of it is.
	if !SubsetOf(scope(t, "github/acme/a,github/acme/b"), scope(t, "github/acme/a,github/acme/*")) {
		t.Errorf("a list of two patterns, each covered by one of the grantor's, is not a subset: each grantee pattern needs some grantor pattern")
	}
	if SubsetOf(scope(t, "github/acme/a,bitbucket/other/b"), scope(t, "github/acme/*")) {
		t.Errorf("a list with a pattern no grantor pattern covers is a subset: one uncovered pattern is a wider grant")
	}

	// "Unsure" is false on both sides, including for a scope that did not
	// parse.
	for _, bad := range []Scope{
		{},
		{Kind: ScopeNone},
		{Kind: ScopeRepos},
		{Kind: ScopeRepos, Repos: []string{""}},
		{Kind: ScopeRepos, Repos: []string{"*"}},
		{Kind: ScopeAllRepos, Repos: []string{"github/acme/app"}},
		{Kind: ScopeKind(99)},
	} {
		if SubsetOf(bad, scope(t, "project")) || SubsetOf(scope(t, "project"), bad) {
			t.Errorf("SubsetOf with %+v answered true: a scope this package did not parse is not a subset of anything and covers nothing", bad)
		}
	}
}

// TestAllReposAdminCannotGrantBareStar: the escalation D1583's amendment and
// round 2's finding 6 close — "all-repos" is a superset of every pattern, so
// if "*" were a scope an all-repos admin could write "*" and mint the
// project-reserved all-repos scope, or something a reader would take for it.
func TestAllReposAdminCannotGrantBareStar(t *testing.T) {
	allRepos := scope(t, "all-repos")
	for _, value := range []string{"*", "*/acme/*"} {
		if _, err := ParseScope(value); err == nil {
			t.Errorf("ParseScope(%q) parsed: neither is a scope value", value)
		}
	}
	// Even hand-built, a scope carrying a bare star or a provider star is not a
	// subset of all-repos: SubsetOf asks Valid first, so a shape ParseScope
	// would refuse can never be granted by somebody who holds all-repos.
	for _, repos := range [][]string{{"*"}, {"*/acme/*"}, {"github/acme/app", "*"}} {
		bare := Scope{Kind: ScopeRepos, Repos: repos}
		if bare.Valid() {
			t.Errorf("Scope{Repos: %q}.Valid() = true, want false", repos)
		}
		if SubsetOf(bare, allRepos) {
			t.Errorf("SubsetOf(%q, all-repos) = true: an all-repos admin must not be able to mint a bare star", repos)
		}
	}
}

// TestUnparseableGrantDeniesEverything: the fail-closed rule the plan's global
// constraints pin — a role check that quietly allows on a parse failure is the
// failure mode, so a grant written with a role or a scope this package does not
// know grants nothing at all.
func TestUnparseableGrantDeniesEverything(t *testing.T) {
	bad := []Grant{
		{User: alice, Role: Role("superuser"), Scope: Scope{Kind: ScopeProject}},
		{User: alice, Role: Role("Admin"), Scope: Scope{Kind: ScopeProject}},
		{User: alice, Role: RoleAdmin, Scope: Scope{}},
		{User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeNone}},
		{User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeRepos}},
		{User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeRepos, Repos: []string{"*"}}},
		{User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeRepos, Repos: []string{"project"}}},
		{User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeAllRepos, Repos: []string{"github/acme/app"}}},
		{User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeKind(99), Repos: []string{"github/acme/app"}}},
		{User: "", Role: RoleAdmin, Scope: Scope{Kind: ScopeProject}},
	}
	for i, g := range bad {
		in := knows(g)
		if got := Effective(in, alice, "github/acme/app"); got != RoleNone {
			t.Errorf("grant %d (%+v): Effective = %q, want %q: a grant that does not parse grants nothing", i, g, got, RoleNone)
		}
		if Allowed(in, alice, ActionViewRepo, "github/acme/app") {
			t.Errorf("grant %d (%+v): the caller is allowed a read, want refused", i, g)
		}
		if Effective(in, "", "github/acme/app") != RoleNone {
			t.Errorf("grant %d: an empty caller holds something", i)
		}
	}

	// The bad grants do not add to a good one either: the good grant is the
	// whole answer.
	in := knows(bad[0], bad[4], grant(alice, RoleViewer, Scope{Kind: ScopeProject}))
	if got := Effective(in, alice, "github/acme/app"); got != RoleViewer {
		t.Errorf("Effective with one good and two unparsable grants = %q, want %q", got, RoleViewer)
	}
}

// TestCallerIsTheCredentialNotGitConfig: F21 — the caller is the credential
// the command will use, never `git config user.email` or the local config's
// `user:` key, both of which are text a person or a coding agent can set. A
// check built on either is a courtesy.
func TestCallerIsTheCredentialNotGitConfig(t *testing.T) {
	ctx := context.Background()

	// A git configuration naming somebody else, in the HOME the default reader
	// would look under, and no credential it can read.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitConfig := "[user]\n\temail = git@example.com\n"
	if err := os.WriteFile(filepath.Join(home, ".git", "config"), []byte(gitConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(home, "gcloud"))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(home, "not-a-credential.json"))

	member, err := Caller(ctx)
	if err == nil {
		t.Fatalf("Caller = %q with no readable credential, want an error: a git configuration is not a credential", member)
	}
	if member != "" {
		t.Errorf("Caller = %q beside its error, want the empty member", member)
	}
	if strings.Contains(err.Error(), "git@example.com") {
		t.Errorf("Caller's error names the git address (%q): the caller is the credential, and a git configuration must not reach this answer at all", err)
	}

	// The credential's answer is the answer, even with the git configuration
	// still in place — and even when a grant names the git address.
	stubCredential(t, "Alice@Example.COM", nil)
	member, err = Caller(ctx)
	if err != nil {
		t.Fatalf("Caller: %v", err)
	}
	if member != "user:Alice@example.com" {
		t.Errorf("Caller = %q, want %q: the credential's own address, its domain folded as mobileauth.NormalizeEmail folds it and its local part kept byte for byte", member, "user:Alice@example.com")
	}
	in := knows(
		grant("user:git@example.com", RoleAdmin, Scope{Kind: ScopeProject}),
		grant(member, RoleViewer, Scope{Kind: ScopeProject}),
	)
	if got := Effective(in, member, "github/acme/app"); got != RoleViewer {
		t.Errorf("Effective(%q, ...) = %q, want %q: the person the credential names is the viewer, and the git address holds what it holds", member, got, RoleViewer)
	}

	// What a credential can be and still hold nothing.
	for _, address := range []string{
		"bot@project.iam.gserviceaccount.com",
		"",
		"git@example.com,admin@example.com",
		"Dimi Paun <dimi@example.com>",
		"dimi@example.com ",
		"nosuchaddress",
		"two@at@example.com",
	} {
		stubCredential(t, address, nil)
		if member, err := Caller(ctx); err == nil {
			t.Errorf("Caller = %q for the credential address %q, want an error: a service account and any value that is not one person's address hold nothing", member, address)
		}
	}
}

// stubCredential points the credential reader at a fixed answer for one test,
// so nothing here reaches the network.
func stubCredential(t *testing.T, address string, err error) {
	t.Helper()
	previous := credentialEmail
	credentialEmail = func(context.Context) (string, error) { return address, err }
	t.Cleanup(func() { credentialEmail = previous })
}
