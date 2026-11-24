package credsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// identity_scope.go's own behaviour: the launch-scope check the ticket route
// asks (LM23). The table that runs it BESIDE the CLI gate it must agree with
// lives in internal/cli/access_issuer_agree_test.go, because the gate is
// unexported there; what is here is the check's own answer, per arm, and the
// two states a test has to be able to build without a database.
//
// The records are read through the real reader in the agreement test (one
// fixture database, both sides reading it) and through a fake here, where the
// point is which arm refuses and not which bytes are on a node.

// The scope fixtures' repository, its storage slug and a second repository the
// installation has a job for: the slug is a hash of the pair and names nothing
// back, which is why the check reads the installation's table rather than
// deriving anything from the ask.
const (
	scopeSlug  = "6f1c0f0a2b8e"
	scopeOther = "b41d9c77a0f3"
)

var scopeRepo = "github/acme/app"

// scopeRecords is the roles-and-scopes records as a test hands them over: one
// node's answer, and the marker's, each replaceable by a failure.
type scopeRecords struct {
	grants      []accesslib.Grant
	found       bool
	migrated    bool
	readErr     error
	migratedErr error
	reads       int
}

func (r *scopeRecords) Read(context.Context) (accesslib.Node, error) {
	r.reads++
	if r.readErr != nil {
		return accesslib.Node{}, r.readErr
	}
	return accesslib.Node{Grants: r.grants, Found: r.found}, nil
}

func (r *scopeRecords) Migrated(context.Context) (bool, error) {
	if r.migratedErr != nil {
		return false, r.migratedErr
	}
	return r.migrated, nil
}

// scopeGrant is the stored pair a test means.
func scopeGrant(t *testing.T, member string, role accesslib.Role, scope string) accesslib.Grant {
	t.Helper()
	parsed, err := accesslib.ParseScope(scope)
	if err != nil {
		t.Fatalf("the test's own scope %q is not one the node parses: %v", scope, err)
	}
	return accesslib.Grant{User: member, Role: role, Scope: parsed}
}

// scopeAllow is a published policy admitting the repositories a test names.
func scopeAllow(allow ...string) *fakePolicy {
	return &fakePolicy{shared: &projectpolicy.Policy{Version: projectpolicy.Version, AllowRepos: allow}, sharedFound: true}
}

// scopeAllowAll is a published policy admitting every repository the rows here
// name, for the rows where nothing is meant to be refused for the list's sake.
// It is spelled with wildcard segments rather than as the bare "*" a policy may
// also carry, because the CI read map's scan reads a bare "*" string literal as
// a glob of the checkout's root and would record this package's tests as reading
// the whole tree (docs/ci-affected-tests/read-map.json, and the test that holds
// the map to the tree). The judgement is the same one — the match is
// projectpolicy's own — and every repository a row here names is a github one.
func scopeAllowAll() *fakePolicy { return scopeAllow("github/*/*") }

// scopeAsk is the ask the ticket route is handed for a run of scopeSlug.
func scopeAsk(run string, contextRepos ...string) identitywire.TicketRequest {
	return identitywire.TicketRequest{
		Slug: scopeSlug, Run: run, Account: "run@example.iam.gserviceaccount.com",
		Launcher: "boss@example.com", Branch: "main", MaxRuntimeSeconds: 3600,
		ContextRepos: contextRepos,
	}
}

// scopeCaller is a verified person, spelled the way LM3 hands one over: the
// address its own Google ID token carried and the class it was verified as.
func scopeCaller(address string) IdentityCaller {
	return IdentityCaller{Address: address, Class: identitywire.ClassPerson}
}

// scopeCheck is the check under test over one installation.
func scopeCheck(cfg LaunchScopeConfig) ScopeCheck {
	if cfg.Repos == nil {
		cfg.Repos = map[string]string{scopeSlug: scopeRepo, scopeOther: "github/acme/other"}
	}
	return NewLaunchScope(cfg)
}

// TestIssuerRefusesACallerWithoutTheLaunchScope is the plan's red step: a
// verified person whose records give them viewer at the repository asks for a
// run of it, and launching takes operator (F19) - the ticket route must be
// refused before anything is minted. Against C1's stub check, which admits
// everyone, this test fails.
//
// What it pins beside the refusal is that the refusal is the SCOPE's and not a
// blanket one: the same installation admits an operator at the same
// repository, and refuses the viewer for a second repository the installation
// holds a job for, one ask later.
func TestIssuerRefusesACallerWithoutTheLaunchScope(t *testing.T) {
	ctx := context.Background()
	records := &scopeRecords{found: true, grants: []accesslib.Grant{
		scopeGrant(t, "user:viewer@example.com", accesslib.RoleViewer, scopeRepo),
		scopeGrant(t, "user:operator@example.com", accesslib.RoleOperator, scopeRepo),
	}}
	sc := scopeCheck(LaunchScopeConfig{Records: records, Policy: scopeAllowAll()})

	err := sc.Check(ctx, scopeCaller("viewer@example.com"), scopeAsk("20261121-120000-abcd"))
	if !errors.Is(err, ErrNoLaunchScope) {
		t.Fatalf("a viewer's ask for a run of %s = %v, want ErrNoLaunchScope: launching takes the operator role", scopeRepo, err)
	}
	for _, want := range []string{"viewer@example.com", scopeRepo, "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	var fault *ScopeFault
	if errors.As(err, &fault) {
		t.Errorf("a caller the records place is judged and refused as a caller, not as the service's own failure: %v", err)
	}

	if err := sc.Check(ctx, scopeCaller("operator@example.com"), scopeAsk("20261121-120000-abcd")); err != nil {
		t.Fatalf("an operator's ask for a run of the same repository = %v, want it admitted", err)
	}
	if err := sc.Check(ctx, scopeCaller("viewer@example.com"), scopeAsk("20261121-120000-abcd")); !errors.Is(err, ErrNoLaunchScope) {
		t.Fatalf("the same viewer's second ask = %v, want ErrNoLaunchScope", err)
	}

	// A slug the installation's table does not hold: the ask names a repository
	// nothing here can name back (a slug is a hash), and a scope is never
	// judged for one. It is refused rather than guessed at, and the refusal
	// says which fact it is.
	unknown := scopeAsk("20261121-120000-abcd")
	unknown.Slug = "0123456789ab"
	err = sc.Check(ctx, scopeCaller("operator@example.com"), unknown)
	if !errors.Is(err, ErrNoLaunchScope) || !strings.Contains(err.Error(), "0123456789ab") {
		t.Fatalf("an ask for a slug the installation does not hold = %v, want a refusal naming it", err)
	}
}

// TestIssuerRefusesAContextRepositoryWithoutTheViewScope: the list the issuer
// signs into the run's identity is judged the way the gate judges it (F31,
// R16's fourth arm) - every context repository for the view action AND against
// the published allow-list - and both halves refuse their own case.
//
// The role half is what makes this not the gate's allow-list question: a
// caller may read a repository the policy admits and hold no role in it, and
// the run's identity is what would carry the read (the gateway holds the
// signature, not a role).
func TestIssuerRefusesAContextRepositoryWithoutTheViewScope(t *testing.T) {
	ctx := context.Background()
	repoOther := "github/acme/other"
	records := &scopeRecords{found: true, grants: []accesslib.Grant{
		scopeGrant(t, "user:operator@example.com", accesslib.RoleOperator, scopeRepo),
		scopeGrant(t, "user:reader@example.com", accesslib.RoleOperator, scopeRepo),
		scopeGrant(t, "user:reader@example.com", accesslib.RoleViewer, repoOther),
	}}
	sc := scopeCheck(LaunchScopeConfig{Records: records, Policy: scopeAllow("github/acme/*")})

	err := sc.Check(ctx, scopeCaller("operator@example.com"), scopeAsk("20261121-120000-abcd", "acme/other"))
	if !errors.Is(err, ErrNoLaunchScope) {
		t.Fatalf("an operator with no role in the context repository = %v, want ErrNoLaunchScope: the list is signed into the run's identity", err)
	}
	for _, want := range []string{"acme/other", "reading", "viewer", "operator@example.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	// The same ask with a viewer at the context repository is admitted: the
	// refusal above is about the role and not about the launch.
	if err := sc.Check(ctx, scopeCaller("reader@example.com"), scopeAsk("20261121-120000-abcd", "acme/other")); err != nil {
		t.Fatalf("a launch whose context repository the caller may read = %v, want it admitted", err)
	}

	// A context repository the policy does not admit is refused by the
	// allow-list, for a caller the roles DO place there: the view role and the
	// policy are two questions, and the gate asks both.
	project := &scopeRecords{found: true, grants: []accesslib.Grant{
		scopeGrant(t, "user:reader@example.com", accesslib.RoleOperator, "all-repos"),
	}}
	tight := scopeCheck(LaunchScopeConfig{Records: project, Policy: scopeAllow(scopeRepo)})
	err = tight.Check(ctx, scopeCaller("reader@example.com"), scopeAsk("20261121-120000-abcd", "acme/other"))
	if !errors.Is(err, ErrNoLaunchScope) || !strings.Contains(err.Error(), "allow_repos") {
		t.Fatalf("a context repository outside the published allow-list = %v, want the policy's own refusal", err)
	}
}

// TestOrchestratorClassIsLeftToItsOwnCheck: the orchestrator's class is judged
// by LM24 and not here (D3130 as amended, D3136). The ask below is one this
// check would refuse on every arm it owns - a slug the installation does not
// hold, context repositories, records and a policy that cannot be read at all -
// and it is admitted, because the class is not this check's question and a
// launch scope that refused it would refuse every ask LM24 admits.
func TestOrchestratorClassIsLeftToItsOwnCheck(t *testing.T) {
	ctx := context.Background()
	sc := scopeCheck(LaunchScopeConfig{
		Records: &scopeRecords{readErr: errors.New("the records were not read")},
		Policy:  &fakePolicy{sharedErr: errors.New("the policy was not read")},
	})
	ask := scopeAsk("20261121-120000-abcd", "acme/other")
	ask.Slug = "0123456789ab"
	caller := IdentityCaller{Address: "orchestrator@example.iam.gserviceaccount.com", Class: identitywire.ClassOrchestrator}

	if err := sc.Check(ctx, caller, ask); err != nil {
		t.Fatalf("an orchestrator's ask = %v, want the launch scope to leave the class to its own check (LM24)", err)
	}
}

// TestIssuerRefusesARolesNodeThatCannotBeRead is the fifth arm and the sixth
// side by side, because the two are one line of code apart and must not be one
// answer (R16).
//
// A node that does not decode, or records that cannot be reached at all, are
// both refused - and both as the SERVICE's own failure and never as a refusal
// of the caller: a run's gateway reads a 403 as a missing invoke grant and
// halts the run naming a grant, when what happened is an installation this
// service could not judge (R16, ScopeFault). The gate treats the same two facts
// differently from each other - it fails closed on a node it cannot hold whole
// and does not judge at all on one it cannot reach - and that divergence is
// R16's sixth arm, pinned beside the gate itself
// (internal/cli/access_issuer_agree_test.go's outage row).
func TestIssuerRefusesARolesNodeThatCannotBeRead(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		records AccessRecords
	}{
		{"a node that does not decode", &scopeRecords{found: true, readErr: errors.New("the node is not a document this reader holds whole")}},
		{"records that cannot be reached", &scopeRecords{readErr: fmt.Errorf("reading %s: %w", accesslib.PathNode, rtdb.ErrUnavailable)}},
		{"a marker that cannot be read", &scopeRecords{found: false, migratedErr: fmt.Errorf("reading %s: %w", accesslib.PathMigrated, rtdb.ErrUnavailable)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := scopeCheck(LaunchScopeConfig{Records: tc.records, Policy: scopeAllowAll()})
			err := sc.Check(ctx, scopeCaller("operator@example.com"), scopeAsk("20261121-120000-abcd"))
			var fault *ScopeFault
			if !errors.As(err, &fault) {
				t.Fatalf("the ask = %v, want a *ScopeFault: the service could not judge, which is not a caller who is not admitted", err)
			}
			if errors.Is(err, ErrNoLaunchScope) {
				t.Errorf("the fault carries the caller's refusal code, which the route would answer as a 403: %v", err)
			}
			if fault.What == "" {
				t.Error("the fault names no step, so an operator reading the line learns nothing about what could not be read")
			}
		})
	}
}
