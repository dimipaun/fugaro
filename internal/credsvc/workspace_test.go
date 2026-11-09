package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// The workspace's service, in the two members and the one owner person these
// tests are about. Every value here is a name — a project, an address at
// example.com, a repository — and no literal is, or looks like, a credential
// (the folder's rule).
const (
	wsWorkspace  = "workspace-alpha" // the workspace's own Fugaro project
	wsAudience   = "https://creds-workspace-alpha-uc.a.run.app"
	wsMemberA    = "project-a"
	wsMemberB    = "project-b"
	wsAccountA   = "fugaro-job-1@project-a.iam.example.com"
	wsAccountB   = "fugaro-job-1@project-b.iam.example.com"
	wsAccountOut = "fugaro-job-1@project-c.iam.example.com"
	// wsAccountA2 is the member A's SECOND job service account. A member's row
	// carries a list (WorkspaceCredentials.Validate admits several), so a fixture
	// with one account per member would leave a rewrite that bound only the
	// first one green: the account table is what names the member, and every
	// account of it has to.
	//
	// It is the one address here that is NOT all lower case, and that is its
	// second job: the fold is what matches a claim to this row, and what the
	// caller is answered with is the configured spelling (Caller's Account) and
	// never the presented one, so a fixture of lowercase addresses would leave a
	// rewrite that echoed the claim green.
	wsAccountA2 = "Fugaro-Job-2@Project-A.iam.example.com"
	wsOwner     = "owner@example.com"
	// wsMemberPerson is a member's own person: an address a member's launcher
	// list would name and this deployment holds no grant to read, so it is a
	// caller of no class here.
	wsMemberPerson = "operator@project-a.example.com"
	wsRepo         = "github/owner/repo-a"
	// wsRepoOther is a second repository of the same member. It is the
	// repository a run identity of that member's own Firebase project can name
	// while the job service account presenting it is the one the owner's table
	// binds, which is the state §8's residual risk 7 is about (server.go's
	// pairing).
	wsRepoOther = "github/owner/repo-b"
	// wsJob is the job service account the workspace's own runs use. It is an
	// account NAME and not a slug: the two are unrelated, and the slug the run
	// identity carries is task.Slug's output (below).
	wsJob = "fugaro-owner-repo-a"
	// wsShared is one credential of the workspace's pool. It is a name: the
	// pool's values live in the workspace's one bundle secret and no test
	// carries one.
	wsShared = "claude-oauth-token-team"
)

// wsSlug and wsSlugOther are what a run identity's `fs` carries: task.Slug's own
// output for the two repositories, which is the only spelling of a repository the
// workspace's service ever sees (and a valid database path segment).
//
// They are DERIVED and not written out, because the service holds a run's
// repository to the member's own `allow_repos` by hashing each entry and
// comparing (canonicalOfSlug, which the publish path keys the child with too). A
// written-out slug — the readable half alone, "owner-repo-a", or that half plus
// anything the hash is not — is a slug no launcher can mint, and a fixture that
// used one would be testing a run no deployment can have.
var (
	wsSlug      = mustSlug(wsRepo)
	wsSlugOther = mustSlug(wsRepoOther)
)

// mustSlug is task.Slug for a fixture repository, which the fixture may not get
// wrong: a canonical name the node's own rules refuse is a test that could not
// set up.
func mustSlug(canonical string) string {
	provider, repo, ok := strings.Cut(canonical, "/")
	if !ok || provider == "" || repo == "" {
		panic("the fixture's repository " + canonical + " is not a canonical provider/owner/name")
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		panic("task.Slug refused the fixture's repository " + canonical + ": " + err.Error())
	}
	return slug
}

// wsDB is one member's database: the nodes it holds, keyed by the path a
// reader asks for, and the paths it was asked for. Every member gets one of
// its own, so a test can see WHICH member's state a call read — which is the
// property the database derivation exists for.
type wsDB struct {
	url   string
	nodes map[string]any
	paths []string
	err   error
	// errPath, when set, is the ONE path whose read fails: a database that is
	// down at one node and healthy at the others. A test of a service's SECOND
	// read needs it — a database that failed everywhere would refuse at the
	// first read and hold nothing about the one behind it.
	errPath string
}

func (d *wsDB) Get(_ context.Context, path string, out any) (bool, error) {
	d.paths = append(d.paths, path)
	if d.err != nil && (d.errPath == "" || d.errPath == path) {
		return false, d.err
	}
	node, ok := d.nodes[path]
	if !ok {
		return false, nil
	}
	raw, err := json.Marshal(node)
	if err != nil {
		return false, err
	}
	if r, ok := out.(*json.RawMessage); ok {
		*r = raw
	}
	return true, nil
}

func (d *wsDB) read(path string) bool {
	for _, p := range d.paths {
		if p == path {
			return true
		}
	}
	return false
}

// wsFixture is one workspace's service, with two members, one owner person and
// the clock under the test's control.
type wsFixture struct {
	t   *testing.T
	now time.Time
	run string
	dbs map[string]*wsDB
	ws  *Workspace
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	return newWSFixtureWith(t, nil)
}

func newWSFixtureWith(t *testing.T, mutate func(*WorkspaceConfig)) *wsFixture {
	t.Helper()
	now := testNow()
	f := &wsFixture{
		t:   t,
		now: now,
		run: now.Format("20060102-150405") + "-ab12",
		dbs: map[string]*wsDB{
			wsMemberA: {url: "https://" + wsMemberA + "-default-rtdb.firebaseio.com", nodes: map[string]any{}},
			wsMemberB: {url: "https://" + wsMemberB + "-default-rtdb.firebaseio.com", nodes: map[string]any{}},
		},
	}
	cfg := WorkspaceConfig{
		Project:  wsWorkspace,
		Audience: wsAudience,
		Members: []WorkspaceMember{
			{
				Project: wsMemberA, FirebaseProject: wsMemberA, FP: wsMemberA,
				JobAccounts: []string{wsAccountA, wsAccountA2}, ExchangeURL: "https://exchange.project-a.example.com",
			},
			{
				Project: wsMemberB, FirebaseProject: wsMemberB, FP: wsMemberB,
				JobAccounts: []string{wsAccountB}, ExchangeURL: "https://exchange.project-b.example.com",
			},
		},
		OwnerPeople: []string{wsOwner},
		Keys:        staticKeys{testKid: &testKey().PublicKey},
		JobKeys:     staticKeys{testJobKid: &testKey().PublicKey},
		// The opener is the ONLY thing that decides which member's state a
		// call can read, and it is asked for a URL: a deployment that opened a
		// URL nothing derived is a deployment this test would hear about.
		Open: func(url string) (Getter, error) {
			for _, db := range f.dbs {
				if db.url == url {
					return db, nil
				}
			}
			return nil, fmt.Errorf("no database at %s in this deployment", url)
		},
		Now: func() time.Time { return f.now },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	ws, err := NewWorkspace(cfg)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	f.ws = ws
	return f
}

// jobToken is a caller's Google ID token: the identity a member's job service
// account presents, signed by a key the caller's key set holds.
func (f *wsFixture) jobToken(email string, mutate ...func(map[string]any)) string {
	c := map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            wsAudience,
		"sub":            "1234567890",
		"email":          email,
		"email_verified": true,
		"iat":            f.now.Add(-time.Minute).Unix(),
		"exp":            f.now.Add(30 * time.Minute).Unix(),
	}
	for _, m := range mutate {
		m(c)
	}
	return signJWT(f.t, testKey(), testJobKid, c)
}

// ownerToken is the workspace owner's person: the same Google ID token with an
// address of the owner's own list.
func (f *wsFixture) ownerToken(mutate ...func(map[string]any)) string {
	return f.jobToken(wsOwner, mutate...)
}

// runToken is a run identity, minted as the member named by `member` would
// have it: aud and iss are that member's Firebase project and fp is that
// member. A test that wants the pins to name another member mutates one of the
// three.
func (f *wsFixture) runToken(member string, mutate ...func(map[string]any)) string {
	c := map[string]any{
		"iss":       "https://securetoken.google.com/" + member,
		"aud":       member,
		"sub":       "r~" + wsSlug + "~" + f.run,
		"user_id":   "r~" + wsSlug + "~" + f.run,
		"auth_time": f.now.Add(-time.Minute).Unix(),
		"iat":       f.now.Add(-time.Minute).Unix(),
		"exp":       f.now.Add(30 * time.Minute).Unix(),
		"firebase":  map[string]any{"identities": map[string]any{}, "sign_in_provider": "custom"},
		"fs":        wsSlug,
		"fr":        f.run,
		"fp":        member,
		"fx":        f.now.Add(2 * time.Hour).UnixMilli(),
		"rb":        wsMemberPerson,
	}
	for _, m := range mutate {
		m(c)
	}
	return signJWT(f.t, testKey(), testKid, c)
}

// verify is one member's job calling a route that acts for a member.
func (f *wsFixture) verify(member, account string, mutate ...func(map[string]any)) (MemberCall, error) {
	f.t.Helper()
	return f.ws.Verify(context.Background(), Request{
		RunIDToken: f.runToken(member, mutate...),
		JobIDToken: f.jobToken(account),
		Job:        wsJob,
	})
}

// verifyOK is the happy path: the member's own job, its own run identity.
func (f *wsFixture) verifyOK(member, account string) MemberCall {
	f.t.Helper()
	caller, err := f.verify(member, account)
	if err != nil {
		f.t.Fatalf("Verify refused the member %s's own job: %v", member, err)
	}
	return caller
}

// setPolicy publishes one member's policy with one account admitted to this
// test's repository, which is the member's side of D1298's intersection.
// setPolicy writes the member's own published policy AND the repository's
// admission child the publish path derives from it (repoAdmissions): every
// account it names carries allow_repos naming wsRepo, so the child for wsSlug
// lists it. One command writes the two nodes, and a fixture that wrote only the
// policy would be a member whose repository node no publish ever produced.
func (f *wsFixture) setPolicy(member string, accounts ...string) {
	f.setPolicyOn(member, []string{wsRepo}, accounts...)
}

// setPolicyOn is setPolicy with the repositories given: the account's own
// allow_repos AND the project's list carry all of them, and one admission child
// per repository is written the way the publish path derives it — every account
// of the list, for every repository any of them names.
//
// It writes the children only for the repositories it is given and leaves the
// others where they are, which is what makes a STALE child reachable: a policy
// published with two repositories and then narrowed to one leaves the second
// repository's child behind, exactly as a `repos rm` whose publish did not
// re-derive it, two concurrent publishes or a hand edit would.
func (f *wsFixture) setPolicyOn(member string, repos []string, accounts ...string) {
	block := map[string]any{}
	for _, name := range accounts {
		block[name] = map[string]any{"allow_repos": append([]string(nil), repos...)}
	}
	f.dbs[member].nodes[projectpolicy.PathShared()] = map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": append([]string(nil), repos...),
		"accounts":    block,
	}
	for _, repo := range repos {
		f.setAdmissions(member, mustSlug(repo), accounts...)
	}
}

// setProjectAllow rewrites the published policy's OWN allow_repos — the
// project's list, which is the leg that closes the `repos rm` gap — and leaves
// the accounts block and every child as they are: the state a publish that
// narrowed the project's list, or a hand edit, leaves behind.
func (f *wsFixture) setProjectAllow(member string, repos ...string) {
	f.t.Helper()
	node, ok := f.dbs[member].nodes[projectpolicy.PathShared()].(map[string]any)
	if !ok {
		f.t.Fatalf("no policy node is published for %s", member)
	}
	node["allow_repos"] = append([]string(nil), repos...)
}

// setAccountAllow rewrites one credential's entry in the published policy's
// `accounts` block — the member's own allow_repos for that account — and leaves
// the project's list and every repository child as they are: the state a publish
// that narrowed the account's list, two concurrent publishes or a hand edit
// leaves behind, with the child the previous list derived still in place.
func (f *wsFixture) setAccountAllow(member, account string, repos ...string) {
	f.t.Helper()
	node, ok := f.dbs[member].nodes[projectpolicy.PathShared()].(map[string]any)
	if !ok {
		f.t.Fatalf("no policy node is published for %s", member)
	}
	block, ok := node["accounts"].(map[string]any)
	if !ok {
		f.t.Fatalf("the policy node of %s carries no accounts block", member)
	}
	entry, ok := block[account].(map[string]any)
	if !ok {
		f.t.Fatalf("the policy node of %s lists no account %q", member, account)
	}
	entry["allow_repos"] = append([]string(nil), repos...)
}

// setAdmissions writes one repository's own node — the child the service reads
// by the slug the run identity verified — with no account listed for no
// accounts, which is the absent node and not an empty block (a written block
// naming nobody is a document the reader refuses, projectpolicy.DecodeRepo).
// It is a fixture entry point of its own because the service reads the node and
// not the policy that derives it, so a test has to be able to write the states
// a publish cannot produce: a child listing somebody else, or nobody at all.
func (f *wsFixture) setAdmissions(member, slug string, accounts ...string) {
	if len(accounts) == 0 {
		delete(f.dbs[member].nodes, projectpolicy.PathRepo(slug))
		return
	}
	sorted := append([]string(nil), accounts...)
	slices.Sort(sorted)
	f.dbs[member].nodes[projectpolicy.PathRepo(slug)] = map[string]any{
		"credentials": map[string]any{"accounts": sorted},
	}
}

// offered is the workspace's declaration: one shared credential, offered to
// both members.
func (f *wsFixture) offered(projects ...string) []creds.Shared {
	return []creds.Shared{{
		Credential: creds.Credential{Name: wsShared, Tier: creds.TierA},
		Projects:   projects,
	}}
}

// TestWorkspaceServiceRefusesAJobServiceAccountOutsideTheWorkspace: the
// account table is the workspace's own, and an account that is not in it is
// not a caller — whatever it is elsewhere. The refusal is one message for
// every unknown address, so a caller cannot use the service to ask which
// members a workspace has.
func TestWorkspaceServiceRefusesAJobServiceAccountOutsideTheWorkspace(t *testing.T) {
	f := newWSFixture(t)
	_, err := f.ws.Caller(context.Background(), f.jobToken(wsAccountOut))
	if !errors.Is(err, ErrUnverifiedJob) {
		t.Fatalf("Caller for an account outside the table = %v, want ErrUnverifiedJob", err)
	}
	if err == nil || !strings.Contains(err.Error(), "not one of the workspace's member job service accounts") {
		t.Errorf("the refusal %q does not say the account is not one of the workspace's: an operator has to be able to tell an unknown account from a bad token", err)
	}
	if err != nil && strings.Contains(err.Error(), wsAccountOut) {
		t.Errorf("the refusal %q repeats the address it refused: it is the caller's own claim, and the package's no-echo rule holds every refusal to it", err)
	}
	// The three tables' jobs resolve, and each says which member it is — the
	// configured spelling, not the claim's. A member with two accounts resolves
	// through both: the row is a list, and a table built from its first entry
	// only is a member whose second job is refused.
	for _, tc := range []struct{ account, member string }{
		{wsAccountA, wsMemberA},
		{wsAccountA2, wsMemberA},
		{wsAccountB, wsMemberB},
	} {
		caller, err := f.ws.Caller(context.Background(), f.jobToken(tc.account))
		if err != nil {
			t.Fatalf("Caller for %s: %v", tc.account, err)
		}
		if caller.Class != ClassWorkspaceMember || caller.Member.Project != tc.member || caller.Account != tc.account {
			t.Errorf("Caller for %s = %+v, want the member %s's job with the configured spelling", tc.account, caller, tc.member)
		}
	}
}

// TestWorkspaceServiceRefusesACallerTokenForAnotherAudience: the front door's
// audience check is the ONLY gate that holds one of the workspace owner's
// people to this service. A member's caller is checked again further down — its
// own verifier re-runs the same audience check against the same configured
// value on the run identity's route (Verify -> verifyJob) — but the owner's
// person reaches a workspace-wide route through Caller alone, and nothing above
// re-checks it. An ID token Google minted for any other service is a token
// whose signature, issuer and window are all in order, so without this check it
// is admitted; the installation has the same case for the same reason
// (auth_test.go's audience test).
//
// Both classes are asserted, because the two take different paths out of Caller
// and only one of them is re-checked later: a test of the member's class alone
// would leave the owner's leg — the one with no second check behind it —
// unheld.
func TestWorkspaceServiceRefusesACallerTokenForAnotherAudience(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"another service's audience", func(c map[string]any) {
			c["aud"] = "https://creds-other-installation-uc.a.run.app"
		}},
		{"no audience at all", func(c map[string]any) { delete(c, "aud") }},
	} {
		for _, addr := range []string{wsAccountA, wsOwner} {
			t.Run(tc.name+" / "+addr, func(t *testing.T) {
				if _, err := f.ws.Caller(ctx, f.jobToken(addr, tc.mutate)); !errors.Is(err, ErrUnverifiedJob) {
					t.Errorf("Caller for %s with %s = %v, want ErrUnverifiedJob: the audience this service accepts is its own", addr, tc.name, err)
				}
			})
		}
	}
	// The honest token of each class is admitted, so the refusals above are the
	// audience and not the shape of the fixture.
	if _, err := f.ws.Caller(ctx, f.jobToken(wsAccountA)); err != nil {
		t.Errorf("Caller for a member's job with this service's audience = %v, want it admitted", err)
	}
	if _, err := f.ws.Caller(ctx, f.ownerToken()); err != nil {
		t.Errorf("Caller for one of the workspace owner's people = %v, want it admitted", err)
	}
}

// TestWorkspaceServiceRefusesACallerTokenThatIsNotCurrent: the rest of the
// shared caller check — the verified address, the token's window and its
// not-before — and, like the audience check beside it, the ONLY one the
// workspace owner's person class gets. A member's caller is re-checked inside
// its own verifier on the run identity's route; a workspace-wide route admits
// the owner through Caller alone, and it reaches every member's spend at once.
//
// The cases are the three a verifier exists for: a token whose address nobody
// proved, one whose life has run out, and one that is not yet valid — each
// asserted for BOTH classes, because only the member's leg has a second check
// behind it and a test of that leg alone would leave the owner's unheld.
func TestWorkspaceServiceRefusesACallerTokenThatIsNotCurrent(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"an unverified address", func(c map[string]any) { c["email_verified"] = false }},
		{"an expired token", func(c map[string]any) { c["exp"] = f.now.Add(-time.Minute).Unix() }},
		{"a token issued in the future", func(c map[string]any) { c["iat"] = f.now.Add(time.Hour).Unix() }},
		{"a token that is not valid yet", func(c map[string]any) { c["nbf"] = f.now.Add(time.Hour).Unix() }},
	} {
		for _, addr := range []string{wsAccountA, wsOwner} {
			t.Run(tc.name+" / "+addr, func(t *testing.T) {
				if _, err := f.ws.Caller(ctx, f.jobToken(addr, tc.mutate)); !errors.Is(err, ErrUnverifiedJob) {
					t.Errorf("Caller for %s with %s = %v, want ErrUnverifiedJob: the owner's person class reaches a workspace-wide route through Caller and nothing else", addr, tc.name, err)
				}
			})
		}
	}
}

// TestWorkspaceServiceAdmitsTheOwnerOnGcloudsAudience: one of the workspace
// owner's people is the only caller a workspace-wide route exists for, and a
// person cannot mint a token for this service's URL at all —
// `gcloud auth print-identity-token --audiences` is refused for a user account
// — so a person's ID token carries gcloud's own client id and nothing else
// (GcloudAudience, the same asymmetry the installation's own person class has).
// A front door holding this class to the service's URL alone would leave the
// class with no reachable caller: it would exist and admit nobody, and every
// workspace-wide route would be dead.
//
// The other direction is held beside it: gcloud's audience is the person's and
// is not offered to a member's job, so the widening stops at the class it is
// for and a token a person can mint is not a member's job's token.
func TestWorkspaceServiceAdmitsTheOwnerOnGcloudsAudience(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()
	gcloud := func(c map[string]any) { c["aud"] = GcloudAudience }
	owner, err := f.ws.Caller(ctx, f.ownerToken(gcloud))
	if err != nil {
		t.Fatalf("Caller for one of the workspace owner's people on gcloud's audience: %v", err)
	}
	if owner.Class != ClassWorkspaceOwner || owner.Account != wsOwner {
		t.Errorf("the caller resolved to %+v, want the workspace owner's own person", owner)
	}
	if err := f.ws.WorkspaceWide(owner); err != nil {
		t.Errorf("WorkspaceWide for the owner's person on gcloud's audience = %v, want it admitted: the class is admitted nowhere else", err)
	}
	if _, err := f.ws.Caller(ctx, f.jobToken(wsAccountA, gcloud)); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Caller for a member's job on gcloud's audience = %v, want ErrUnverifiedJob: that audience is the person class's alone", err)
	}
	// And the person's token is still held to the class's list: an address that
	// is nobody's, on the same audience, is refused.
	if _, err := f.ws.Caller(ctx, f.jobToken(wsMemberPerson, gcloud)); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Caller for an address on neither list = %v, want ErrUnverifiedJob", err)
	}
}

// TestWorkspaceServiceAnswersTheConfiguredSpellingOfAnAccount: what a caller is
// answered with is the address the OPERATOR configured, not the one the token
// presented. The fold is what matches them, and two spellings that fold together
// are one account — so the answer is the spelling an operator can search a log
// for, which is the same reason verifyJob answers the configured one.
//
// The token here presents the lower-cased form of the fixture's mixed-case
// account, which is the shape a real caller has: Google normalises an address
// claim, the operator's table carries what the operator typed, and the two have
// to resolve to the same member with the configured spelling coming back.
func TestWorkspaceServiceAnswersTheConfiguredSpellingOfAnAccount(t *testing.T) {
	f := newWSFixture(t)
	caller, err := f.ws.Caller(context.Background(), f.jobToken(strings.ToLower(wsAccountA2)))
	if err != nil {
		t.Fatalf("Caller for the folded spelling of a configured account: %v", err)
	}
	if caller.Account != wsAccountA2 {
		t.Errorf("the caller was answered as %q, want the configured %q: an address that folds to the same account is that account, and the spelling an operator searches for is theirs", caller.Account, wsAccountA2)
	}
	if caller.Member.Project != wsMemberA || caller.Class != ClassWorkspaceMember {
		t.Errorf("the folded spelling resolved to %+v, want the member %s's job", caller, wsMemberA)
	}
}

// TestWorkspaceServiceRefusesAMemberCallItDidNotProduce: MemberCall's whole job
// is that only Verify makes one, and a doc comment cannot hold that — a route
// written beside this package could build the literal out of Caller's answer,
// and its fields would all be well-formed while it stood for no verified run
// identity at all, which is the state the member's routes read the policy and
// serve the pool in. The type carries an unexported mark Verify sets and every
// entry point is held to it, so the literal is refused however plausible it is.
func TestWorkspaceServiceRefusesAMemberCallItDidNotProduce(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()
	caller, err := f.ws.Caller(ctx, f.jobToken(wsAccountA))
	if err != nil {
		t.Fatalf("Caller for the member's job: %v", err)
	}
	// The forged call is the literal a route would write, out of a caller that
	// really did resolve to the member: the class and the member are honest and
	// only the run identity is missing.
	forged := MemberCall{WorkspaceCaller: caller}
	if err := f.ws.MemberRoute(forged, wsMemberA); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("MemberRoute for a MemberCall this service did not produce = %v, want ErrUnverifiedJob", err)
	}
	if _, _, err := f.ws.Policy(ctx, forged); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Policy for a MemberCall this service did not produce = %v, want ErrUnverifiedJob", err)
	}
	if _, err := f.ws.Shared(ctx, forged, f.offered(wsMemberA)); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Shared for a MemberCall this service did not produce = %v, want ErrUnverifiedJob", err)
	}
	if len(f.dbs[wsMemberA].paths) != 0 {
		t.Errorf("the member's database was read for a call with no verified run identity (%v): the mark is checked before any read", f.dbs[wsMemberA].paths)
	}
	// The member's own verified call is served with the same fields, so the
	// refusals above are the missing mark and not the shape of the caller.
	f.setPolicy(wsMemberA, wsShared)
	got, err := f.ws.Shared(ctx, f.verifyOK(wsMemberA, wsAccountA), f.offered(wsMemberA))
	if err != nil || len(got) != 1 {
		t.Errorf("Shared for the verified caller = (%v, %v), want the pool's one credential", got, err)
	}
}

// TestWorkspaceServiceRefusesATokenWhoseIssuerAndAccountDisagree: the account
// names one member and the run identity's pins name another. The member is the
// account — so the RUN TOKEN is offered to that member's own verifier, and the
// pins have to agree with it: the issuer's member and the fp's member are
// checked against the account's member, and a token minted for the other member
// is refused rather than read as the caller's.
//
// This is the half of the task a reader is most likely to miss. A deployment
// that resolved the member from the account and then accepted any member's
// issuer would admit a run of project-b's Firebase into project-a's spend: the
// token is real, the account is real, and the pair is not.
func TestWorkspaceServiceRefusesATokenWhoseIssuerAndAccountDisagree(t *testing.T) {
	f := newWSFixture(t)
	// The account is project-a's; the run identity is project-b's, whole.
	if _, err := f.verify(wsMemberB, wsAccountA); !errors.Is(err, ErrUnverified) {
		t.Fatalf("Verify with project-b's identity and project-a's account = %v, want ErrUnverified", err)
	}
	// And each half of the pins alone is enough to refuse: the aud/iss pair,
	// and the fp.
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"another project's issuer", func(c map[string]any) {
			c["iss"] = "https://securetoken.google.com/" + wsMemberB
			c["aud"] = wsMemberB
		}},
		{"another member's fp", func(c map[string]any) { c["fp"] = wsMemberB }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.verify(wsMemberA, wsAccountA, tc.mutate); !errors.Is(err, ErrUnverified) {
				t.Fatalf("Verify with %s = %v, want ErrUnverified: the account's member and the pins' member have to be one member", tc.name, err)
			}
		})
	}
	// The honest pair is admitted, so the refusals above are the disagreement
	// and not the shape.
	if caller := f.verifyOK(wsMemberA, wsAccountA); caller.Member.Project != wsMemberA {
		t.Errorf("the admitted caller resolved to %s, want %s", caller.Member.Project, wsMemberA)
	}
}

// TestWorkspaceServiceReadsAMembersPolicyFromTheDatabaseItsIssNames: finding 6
// of the post-merge review. The database a call reads is the one the caller's
// own verified `iss` names — derived from the member the call resolved to and
// from nothing the owner declared beside it — so two members' calls read two
// databases and neither reads the other's.
func TestWorkspaceServiceReadsAMembersPolicyFromTheDatabaseItsIssNames(t *testing.T) {
	f := newWSFixture(t)
	f.setPolicy(wsMemberA, wsShared)
	f.setPolicy(wsMemberB, wsShared)
	caller := f.verifyOK(wsMemberA, wsAccountA)
	if want := "https://project-a-default-rtdb.firebaseio.com"; caller.Database != want {
		t.Errorf("the caller's database = %q, want %q: it is derived from the Firebase project the verified iss names", caller.Database, want)
	}
	if _, found, err := f.ws.Policy(context.Background(), caller); err != nil || !found {
		t.Fatalf("Policy for the member = (%t, %v), want the member's own policy", found, err)
	}
	if !f.dbs[wsMemberA].read(projectpolicy.PathShared()) {
		t.Error("the member's own database was not read: the policy has to come from the database its iss names")
	}
	if len(f.dbs[wsMemberB].paths) != 0 {
		t.Errorf("the other member's database was read (%v): one member's call may not read another member's state", f.dbs[wsMemberB].paths)
	}
	// Everything else the member's database was asked for is the liveness
	// scan's own outcome read (the one read every call makes): the policy node
	// is the only other path this call touched.
	for _, p := range f.dbs[wsMemberA].paths {
		if p != projectpolicy.PathShared() && !strings.HasPrefix(p, "outcomes/") {
			t.Errorf("the member's database was asked for %q, and a call of this shape reads the policy node and the run's outcomes", p)
		}
	}
}

// TestNewWorkspaceOpensTheDatabaseTheMembersRegionNames: a member whose project
// was adopted with a Realtime Database in a region has a database at
// `<project>-default-rtdb.<region>.firebasedatabase.app` and not at the legacy
// `firebaseio.com` host, and the row's `database_region` is where the service
// learns that. Deriving the legacy host from the project id alone would give
// such a member a URL that is not its database, and because NewWorkspace opens
// every member's database at construction, that one member would decide whether
// the whole deployment starts.
//
// The region says where in the project the database lives and the project id
// still decides WHOSE it is (finding 6): the URL below is derived from the
// verified `iss`'s project joined to the region the row declares, and never
// from a URL a row carries.
func TestNewWorkspaceOpensTheDatabaseTheMembersRegionNames(t *testing.T) {
	const region = "europe-west1"
	now := testNow()
	cfg := WorkspaceConfig{
		Project:  wsWorkspace,
		Audience: wsAudience,
		Members: []WorkspaceMember{{
			Project: wsMemberA, FirebaseProject: wsMemberA, DatabaseRegion: region, FP: wsMemberA,
			JobAccounts: []string{wsAccountA}, ExchangeURL: "https://exchange.project-a.example.com",
		}},
		OwnerPeople: []string{wsOwner},
		Keys:        staticKeys{testKid: &testKey().PublicKey},
		JobKeys:     staticKeys{testJobKid: &testKey().PublicKey},
		Now:         func() time.Time { return now },
	}
	var opened []string
	cfg.Open = func(url string) (Getter, error) {
		opened = append(opened, url)
		return &wsDB{url: url, nodes: map[string]any{}}, nil
	}
	if _, err := NewWorkspace(cfg); err != nil {
		t.Fatalf("NewWorkspace with a member in a regional database: %v", err)
	}
	want := "https://" + wsMemberA + "-default-rtdb." + region + ".firebasedatabase.app"
	if len(opened) != 1 || opened[0] != want {
		t.Errorf("the databases opened = %v, want just %q: the row's region is where the host comes from, and the legacy host is not this member's database", opened, want)
	}
	// A region outside the grammar a host is written with is refused while the
	// deployment is being built rather than turned into a URL: a region is a
	// piece of the host, so one carrying a `/` or a `.` would derive a URL that
	// names something else entirely.
	cfg.Members[0].DatabaseRegion = "europe-west1.example.com"
	opened = nil
	if _, err := NewWorkspace(cfg); !errors.Is(err, ErrWorkspaceConfig) {
		t.Fatalf("NewWorkspace with a database_region that is not a region = %v, want ErrWorkspaceConfig", err)
	}
	if len(opened) != 0 {
		t.Errorf("the databases opened for a refused region = %v, want none: the row is refused before anything is reached", opened)
	}
}

// TestWorkspaceDatabaseIsTheFirebaseProjectAndTheRegion: the region half of the
// derivation, which both the constructor and a caller's resolution go through.
// A region is the host the tree's own reader admits for a database created in
// one (internal/infra's CheckFirebaseOutputsFor names exactly the legacy
// `firebaseio.com` shape and this one), and it is a piece of that host — so a
// region outside the grammar a host is written with is a refusal and never a
// URL. The project's own default database, the row that carries no region, is
// held by TestWorkspaceIssuerMatchesTheLocalPins.
func TestWorkspaceDatabaseIsTheFirebaseProjectAndTheRegion(t *testing.T) {
	cases := []struct {
		name   string
		region string
		want   string
		refuse bool
	}{
		{"a region is the database that project has there", "europe-west1", "https://project-a-default-rtdb.europe-west1.firebasedatabase.app", false},
		{"a region is not a second host", "europe-west1.example.com", "", true},
		{"a region is not a path", "europe/west1", "", true},
		{"a region is lower case", "EUROPE-WEST1", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workspaceDatabase("project-a", tc.region)
			if tc.refuse {
				if err == nil {
					t.Fatalf("workspaceDatabase(project-a, %q) = %q, want a refusal: the region is a piece of the host and this one is not a region", tc.region, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("workspaceDatabase(project-a, %q) = (%q, %v), want %q", tc.region, got, err, tc.want)
			}
		})
	}
}

// TestWorkspaceServiceRefusesASplicedMemberAccounts: the splice the table used
// to allow — member B's accounts paired with member A's database, and
// therefore with A's policy and A's outcomes. It is refused twice, and the
// test holds both halves: the row a splice would need is not a shape the node
// can carry (a row that names a database at all is a node this Fugaro did not
// write), and the database a call reads is derived from the Firebase project in
// the row that NAMED the accounts, so B's callers read B's state.
func TestWorkspaceServiceRefusesASplicedMemberAccounts(t *testing.T) {
	// The declaration the splice would be: B's accounts with A's database URL
	// beside them.
	raw, err := projectpolicy.WorkspaceCredentialsNode(&projectpolicy.WorkspaceCredentials{
		Version: projectpolicy.WorkspaceCredentialsVersion,
		Name:    wsWorkspace,
		Members: []projectpolicy.WorkspaceCredentialMember{{
			Project: wsMemberB, FirebaseProject: wsMemberB, FP: wsMemberB,
			JobAccounts: []string{wsAccountB}, ExchangeURL: "https://exchange.project-b.example.com",
		}},
	})
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node is not JSON: %v", err)
	}
	doc["members"].([]any)[0].(map[string]any)["database_url"] = spliceDatabaseURL
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, found, err := projectpolicy.DecodeWorkspaceCredentials(edited); err == nil || !found {
		t.Fatalf("DecodeWorkspaceCredentials of a spliced row = (%v, %v), want a refusal: the member's database is derived and is not a field the owner may set", found, err)
	}

	// And with the shape that IS the table, the member reads its OWN database:
	// the splice has nowhere to be written, so it cannot be read.
	f := newWSFixture(t)
	f.setPolicy(wsMemberB, wsShared)
	f.setPolicy(wsMemberA, wsShared)
	b := f.verifyOK(wsMemberB, wsAccountB)
	if _, _, err := f.ws.Policy(context.Background(), b); err != nil {
		t.Fatalf("Policy for the member %s: %v", wsMemberB, err)
	}
	if !f.dbs[wsMemberB].read(projectpolicy.PathShared()) {
		t.Errorf("the member %s's own policy was not read", wsMemberB)
	}
	if len(f.dbs[wsMemberA].paths) != 0 {
		t.Errorf("the member %s's call read %s's state (%v): a member's call reads the database its own iss names", wsMemberB, wsMemberA, f.dbs[wsMemberA].paths)
	}
}

// spliceDatabaseURL is the URL a splice would carry: the other member's
// database, written beside this member's accounts. The node refuses the key,
// which is why the test above can hand the service a splice only by failing to
// build one.
const spliceDatabaseURL = "https://project-a-default-rtdb.firebaseio.com"

// TestCredsServiceLiveCheckIsPerMember: the outcomes reader takes the database
// from the call and never from a field the owner set. The liveness check is
// the one read every call makes, so this is where a shared reader would show
// up: each member's run is looked for in that member's own outcomes, and a
// member's run is refused when ITS OWN database holds the outcome.
func TestCredsServiceLiveCheckIsPerMember(t *testing.T) {
	f := newWSFixture(t)
	// B's run has ended: its own database holds the write-once outcome.
	day, err := time.Parse("20060102", f.run[:8])
	if err != nil {
		t.Fatalf("the run id does not name a launch day: %v", err)
	}
	f.dbs[wsMemberB].nodes[budget.PathOutcome(day.Unix()/86400, wsSlug, f.run)] = map[string]any{"status": "done"}
	_, err = f.verify(wsMemberB, wsAccountB)
	if !errors.Is(err, ErrUnverified) {
		t.Fatalf("Verify for a run whose own outcome exists = %v, want ErrUnverified", err)
	}
	if !strings.Contains(err.Error(), "outcome") {
		t.Errorf("the refusal %q does not say the run has ended", err)
	}
	// A's call of the same instant is admitted and read nothing of B's, and
	// B's call read nothing of A's: the outcomes are read per member.
	f.verifyOK(wsMemberA, wsAccountA)
	for _, p := range f.dbs[wsMemberA].paths {
		if strings.HasPrefix(p, "outcomes/") && !strings.Contains(p, "/"+wsSlug+"/") {
			t.Errorf("the member's liveness scan read %q, which is not this run's outcome", p)
		}
	}
	if !f.dbs[wsMemberB].read(budget.PathOutcome(day.Unix()/86400, wsSlug, f.run)) {
		t.Error("the member's own database was not asked for the outcome: the liveness check has to read the member the call resolved to")
	}
	if len(f.dbs[wsMemberA].paths) == 0 {
		t.Error("the admitted member's own database was never read: the scan reads the caller's member's outcomes, not the deployment's")
	}
	if len(f.dbs[wsMemberA].nodes) != 0 {
		t.Errorf("the member %s's database holds %v, which this test never wrote", wsMemberA, f.dbs[wsMemberA].nodes)
	}
}

// TestWorkspaceServiceRefusesAMembersPersonTheWorkspaceWideRead: one member's
// person asks for the workspace's own figures and is refused, and a member's
// job asks for them too and is refused as well — the person class is the
// workspace owner's people and no member's person is a class this deployment
// has (spec.md §4, F24/F31: the list that names a member's people is the
// member's own file, and an admission this service cannot check is one it does
// not make).
//
// What the refusals are worth: the workspace-wide routes answer with every
// member's spend at once, so an admission there is every member's figure
// handed to one member's people.
func TestWorkspaceServiceRefusesAMembersPersonTheWorkspaceWideRead(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	// A member's person is a caller of no class: the deployment's person list
	// is the owner's and holds no member's address.
	if _, err := f.ws.Caller(ctx, f.jobToken(wsMemberPerson)); !errors.Is(err, ErrUnverifiedJob) {
		t.Fatalf("Caller for a member's own person = %v, want ErrUnverifiedJob", err)
	}
	// A member's job may not read the workspace's figures.
	job, err := f.ws.Caller(ctx, f.jobToken(wsAccountA))
	if err != nil {
		t.Fatalf("Caller for the member's job: %v", err)
	}
	if err := f.ws.WorkspaceWide(job); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("WorkspaceWide for a member's job = %v, want ErrUnverifiedJob", err)
	}
	// The owner's person may read them, and reaches no member's route: it
	// holds no run identity, so a route that acts for a member is not open to
	// it.
	owner, err := f.ws.Caller(ctx, f.ownerToken())
	if err != nil {
		t.Fatalf("Caller for one of the workspace owner's people: %v", err)
	}
	if owner.Class != ClassWorkspaceOwner {
		t.Fatalf("the owner's person is of class %v, want the owner class", owner.Class)
	}
	if err := f.ws.WorkspaceWide(owner); err != nil {
		t.Errorf("WorkspaceWide for one of the workspace owner's people = %v, want it admitted", err)
	}
	// It reaches no member's route, and there is nothing for a test to call: a
	// member's route takes the type only Verify produces, and Verify refuses the
	// owner's person — so the class cannot reach one even by a route that forgot
	// to check, which is the property and not an accident of this call.
	if _, err := f.ws.Verify(ctx, Request{
		RunIDToken: f.runToken(wsMemberA), JobIDToken: f.ownerToken(), Job: wsJob,
	}); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Verify for one of the workspace owner's people = %v, want ErrUnverifiedJob: it holds no run identity", err)
	}
	// A member's route admits its own member and no other.
	memberA := f.verifyOK(wsMemberA, wsAccountA)
	if err := f.ws.MemberRoute(memberA, wsMemberA); err != nil {
		t.Errorf("MemberRoute for the member's own job = %v, want it admitted", err)
	}
	if err := f.ws.MemberRoute(memberA, wsMemberB); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("MemberRoute for another member's job = %v, want ErrUnverifiedJob", err)
	}
}

// TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt is W6's red
// step at the service: the effective set is the intersection, and the member's
// own policy is the side that refuses. The workspace offers the credential to
// both members; the member whose policy names it is served it and the member
// whose policy does not is served nothing, from its own database.
//
// A service that served the pool past the member's policy would be a policy
// bypass (rule 1: a workspace can only tighten a member), and the pool would be
// the one thing in the design that widens.
//
// **B's repository node is written to admit the credential anyway**, which is
// the state a publish does not produce (a child left behind by a policy that
// named it once, or a hand edit). Without it the admission leg refuses B first
// — setPolicy writes the two nodes from one account list, so a member whose
// policy names nothing has a child that names nothing either — and the leg this
// test is about, the member's own policy, would never be the leg that decides:
// the whole of the only-tighten rule could be deleted and this test would stay
// green.
func TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt(t *testing.T) {
	f := newWSFixture(t)
	f.setPolicy(wsMemberA, wsShared)             // A's own policy names the credential
	f.setPolicy(wsMemberB)                       // B's own policy names no account
	f.setAdmissions(wsMemberB, wsSlug, wsShared) // ... and B's repository node lists it
	ctx := context.Background()
	a := f.verifyOK(wsMemberA, wsAccountA)
	b := f.verifyOK(wsMemberB, wsAccountB)

	got, err := f.ws.Shared(ctx, a, f.offered(wsMemberA, wsMemberB))
	if err != nil {
		t.Fatalf("Shared for the member whose policy admits it: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsShared || got[0].Tier != creds.TierA {
		t.Fatalf("Shared for %s = %v, want the credential the workspace offers and its policy admits", wsMemberA, got)
	}
	got, err = f.ws.Shared(ctx, b, f.offered(wsMemberA, wsMemberB))
	if err != nil {
		t.Fatalf("Shared for the member whose policy excludes it: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared for %s = %v, want none: the member's own policy excludes it and a workspace can only tighten a member", wsMemberB, got)
	}
	// The leg BEHIND the policy admitted it, and the refusal above is the
	// policy's: B's repository node names the credential, and the SAME call is
	// served once B's own policy names it too. The node is written the same way
	// both times, so the policy is the only value that changed — which is what
	// makes this the only-tighten leg and not the admission leg refusing first.
	if !f.dbs[wsMemberB].read(projectpolicy.PathRepo(wsSlug)) {
		t.Error("B's repository node was not read: a test that never reads it cannot tell which of the two legs answered, and the leg rule 1 is made of is the policy's")
	}
	f.setPolicy(wsMemberB, wsShared)
	f.setAdmissions(wsMemberB, wsSlug, wsShared)
	got, err = f.ws.Shared(ctx, b, f.offered(wsMemberA, wsMemberB))
	if err != nil {
		t.Fatalf("Shared for the same member once its policy names the credential: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsShared {
		t.Errorf("Shared = %v, want the credential: the repository's node admitted it while the policy did not, so the member's own policy is the leg that refused", got)
	}
	// The other leg: a credential the workspace does not offer this member is
	// not served either, and the member's policy is not asked about it.
	got, err = f.ws.Shared(ctx, a, f.offered(wsMemberB))
	if err != nil {
		t.Fatalf("Shared for a credential the workspace offers elsewhere: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared for %s = %v, want none: the workspace's allow-list does not name it", wsMemberA, got)
	}
	// A member's policy that could not be read is a refusal and never an empty
	// set: a service that read an unreadable policy as "admits nothing" would
	// answer a permission question with a database outage.
	f.dbs[wsMemberA].err = errors.New("the database is unavailable")
	if _, err := f.ws.Shared(ctx, a, f.offered(wsMemberA)); err == nil {
		t.Error("Shared with an unreadable policy = nil error, want a refusal")
	}
}

// TestWorkspaceSharedIsRefusedWhereTheRepositoriesOwnNodeDoesNotAdmitIt: the
// third leg of the effective set, and the one that keeps rule 1 true in the
// repository dimension. The member's policy says which CREDENTIALS it admits;
// the repository's own node (`projectpolicy.PathRepo`, read at the slug the run
// identity verified) says which of them THAT repository was admitted to. A
// credential the member's policy names but the repository's node does not is not
// served, which is the band the member's own narrowing lives in: an
// administrator who admitted a credential to one repository has not admitted it
// to the others.
//
// It is the same node and the same slug the installation's own cascade reads
// (admittedAccounts), so the two services answer the same question the same way
// — and the three states are the cascade's three: listed is served, absent is
// admitted to nothing, unreadable is a refusal.
func TestWorkspaceSharedIsRefusedWhereTheRepositoriesOwnNodeDoesNotAdmitIt(t *testing.T) {
	ctx := context.Background()

	// The member's policy names the credential, and the repository's node lists
	// somebody else: the pool is offered to the project and the policy admits the
	// name, and the credential is still not served on this run's repository.
	f := newWSFixture(t)
	f.setPolicy(wsMemberA, wsShared)
	f.setAdmissions(wsMemberA, wsSlug, wsShared+"-other")
	caller := f.verifyOK(wsMemberA, wsAccountA)
	got, err := f.ws.Shared(ctx, caller, f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a repository the credential was not admitted to: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared = %v, want none: the repository's own node does not admit the credential, so the member's own narrowing is that it is not served here", got)
	}

	// The repository's node lists it, and the same call is served it: the
	// refusal above is the node and not the fixture.
	f.setAdmissions(wsMemberA, wsSlug, wsShared)
	got, err = f.ws.Shared(ctx, caller, f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a repository the credential was admitted to: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsShared {
		t.Errorf("Shared = %v, want the credential the repository's own node admits", got)
	}

	// An absent node is an admission to nothing and not an error: the member's
	// administrator admitted this repository to no account at all.
	f.setAdmissions(wsMemberA, wsSlug)
	got, err = f.ws.Shared(ctx, caller, f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a repository with no admission node: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared = %v, want none: a repository with no node is admitted to nothing", got)
	}

	// The node is read from the MEMBER's database and at the slug the run
	// identity names: the other member's node is not consulted, and a node at
	// another slug is not this run's.
	f.setAdmissions(wsMemberA, wsSlug, wsShared)
	got, err = f.ws.Shared(ctx, caller, f.offered(wsMemberA))
	if err != nil || len(got) != 1 {
		t.Fatalf("Shared with the member's own node written = (%v, %v), want the credential", got, err)
	}
	if !f.dbs[wsMemberA].read(projectpolicy.PathRepo(wsSlug)) {
		t.Error("the repository's node was not read from the member's own database: the admission is the member's own state")
	}
	if len(f.dbs[wsMemberB].paths) != 0 {
		t.Errorf("another member's database was read (%v): one member's call may not read another member's state", f.dbs[wsMemberB].paths)
	}

	// A node that could not be read is a refusal and not an empty set: answering
	// a permission question with a database outage would be the same answer by
	// luck and the wrong one by construction.
	f.dbs[wsMemberA].err = errors.New("the database is unavailable")
	f.dbs[wsMemberA].errPath = projectpolicy.PathRepo(wsSlug)
	if _, err := f.ws.Shared(ctx, caller, f.offered(wsMemberA)); err == nil {
		t.Error("Shared with an unreadable repository node = nil error, want a refusal")
	}
	// And the refusal is the repository node's, not the policy read's: the policy
	// was read and admitted the credential, so a service that had skipped the
	// node would have served it.
	if !f.dbs[wsMemberA].read(projectpolicy.PathShared()) {
		t.Error("the member's policy was not read: the test's unreadable node has to be the one behind the policy read")
	}
}

// TestWorkspaceSharedReadsTheRepositoryOfTheRunIdentityNotTheCallers pins, in
// both directions, the divergence docs/workspaces/spec.md §8's residual risk 7
// states: the member's own two allow_repos legs ARE asked — over the canonical
// name recovered from the verified slug — and what no reader carries is the
// pairing of the calling job service account with the repository the identity
// names.
//
// What the confusion reaches is bounded by the member's own lists: the sibling
// repository this call is served on is one the member's policy scopes the
// credential to, which is the fixture's shape (setPolicyOn names both), and the
// other half of that bound is
// TestWorkspaceSharedIsRefusedWhereTheMembersAllowListNoLongerNamesTheRun, where
// the same call is refused because those lists no longer name the run's
// repository. So the pairing crosses no member and leaves no list the member
// itself wrote — the column that would close it is what the review's N1 and
// spec.md §8's residual risk 7 are about.
//
// The installation's own front door holds the two together — its table binds
// each job account to the repository the service account was derived from, and
// it refuses a run whose slug is another's (`!bound || ja.Slug != id.Slug`,
// internal/credsvc/server.go:737). This deployment's per-member table names a
// member's job accounts and no repository (WorkspaceCredentialMember), so there
// is no slug to hold the verified one to and the node that decides is the one
// the run names. The test exists so that the SHIPPED behaviour is asserted
// rather than described: a change that closes risk 7 by carrying a repository
// beside each account has to change this test, which is where the owner's
// decision about the column is read — and a change that reached a repository
// the identity did not name fails it.
func TestWorkspaceSharedReadsTheRepositoryOfTheRunIdentityNotTheCallers(t *testing.T) {
	f := newWSFixture(t)
	// A's policy names BOTH repositories, so neither allow_repos leg decides
	// anything here: the pairing is the whole of the gap this test pins.
	f.setPolicyOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsShared)
	f.setAdmissions(wsMemberA, wsSlug)                // A's own repository admits nobody
	f.setAdmissions(wsMemberA, wsSlugOther, wsShared) // the other repository admits it
	ctx := context.Background()

	// A run identity of A's OWN Firebase project naming the other repository,
	// presented with A's own job service account. The pins name one member —
	// the account's, the issuer's and the fp's are all A's — so Verify admits
	// it, and nothing in the table says which repository that account belongs
	// to. verifyRun holds fs and fr to the uid, so the three move together.
	other, err := f.verify(wsMemberA, wsAccountA, func(c map[string]any) {
		uid := "r~" + wsSlugOther + "~" + f.run
		c["sub"], c["user_id"], c["fs"] = uid, uid, wsSlugOther
	})
	if err != nil {
		t.Fatalf("Verify refused a run identity of the member's own project for another repository: %v", err)
	}
	got, err := f.ws.Shared(ctx, other, f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a run on the other repository: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsShared {
		t.Errorf("Shared = %v, want the credential: the node read is the one the run identity's own slug names, which is what §8's residual risk 7 states and not what the account's repository would say", got)
	}
	if !f.dbs[wsMemberA].read(projectpolicy.PathRepo(wsSlugOther)) {
		t.Error("the other repository's node was not read: the slug is the run identity's, and a service that read the member's repository instead would decide by something the account does not carry")
	}

	// The other direction, on a fresh fixture of the same shape: the identity
	// names A's own repository, which admits nobody, and the credential is not
	// served even though another of A's repositories is admitted to it. Nothing
	// falls back to a repository the identity did not name, so the divergence is
	// the pairing and not a widening of the node that is read.
	f = newWSFixture(t)
	f.setPolicyOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsShared)
	f.setAdmissions(wsMemberA, wsSlug)
	f.setAdmissions(wsMemberA, wsSlugOther, wsShared)
	got, err = f.ws.Shared(ctx, f.verifyOK(wsMemberA, wsAccountA), f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a run on the member's own repository: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared = %v, want none: the run identity's own repository admits nobody", got)
	}
	if f.dbs[wsMemberA].read(projectpolicy.PathRepo(wsSlugOther)) {
		t.Error("the other repository's node was read: only the slug the run identity names is, so nothing falls back to a repository the run is not on")
	}
}

// TestWorkspaceSharedIsRefusedWhereTheMembersAllowListNoLongerNamesTheRun pins
// the two canonical legs themselves: the pool asks them over the name it
// recovers from the account's list, so a repository child a publish left behind
// does not serve a credential the member's own policy no longer scopes to the
// run's repository. The member's own cascade refuses the same call — its
// admittedAccounts asks both lists, which is the `repos rm` gap the project's leg
// closes — so a pool answering from the child alone would widen the member's own
// policy.
//
// The state is one a publish leaves when it is the FIRST writer of the narrowed
// list to finish (the publishes have no compare-and-set) or a hand edit does,
// which is why the fixture writes the nodes directly (setPolicyOn,
// setAccountAllow, setProjectAllow) instead of publishing through the verb.
func TestWorkspaceSharedIsRefusedWhereTheMembersAllowListNoLongerNamesTheRun(t *testing.T) {
	ctx := context.Background()

	// The account's list narrowed to one repository, the other repository's child
	// left behind and still admitting the credential: a run on the sibling
	// repository is served nothing.
	f := newWSFixture(t)
	f.setPolicyOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsShared)
	f.setAccountAllow(wsMemberA, wsShared, wsRepo)
	other, err := f.verify(wsMemberA, wsAccountA, func(c map[string]any) {
		uid := "r~" + wsSlugOther + "~" + f.run
		c["sub"], c["user_id"], c["fs"] = uid, uid, wsSlugOther
	})
	if err != nil {
		t.Fatalf("Verify refused a run identity for the other repository: %v", err)
	}
	got, err := f.ws.Shared(ctx, other, f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a run the account's list no longer names: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared = %v, want none: the member's account list no longer names the run's repository, and the child the list once derived does not decide for it", got)
	}
	if !f.dbs[wsMemberA].read(projectpolicy.PathRepo(wsSlugOther)) {
		t.Error("the other repository's node was not read: the run's own child is still the node read, and the refusal is the account's allow_repos leg's")
	}
	// The control, on the same fixture: the repository the account's list DOES
	// name is served, so the refusal above is the leg and not the fixture.
	got, err = f.ws.Shared(ctx, f.verifyOK(wsMemberA, wsAccountA), f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a run the account's list names: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsShared {
		t.Errorf("Shared = %v, want the credential: the run's repository is the one the account's list names and its child admits it", got)
	}

	// The project's list narrowed instead, the account's list and every child left
	// where they were: the same refusal, from the leg that closes the `repos rm`
	// gap and not from the account's.
	f = newWSFixture(t)
	f.setPolicyOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsShared)
	f.setProjectAllow(wsMemberA, wsRepo)
	other, err = f.verify(wsMemberA, wsAccountA, func(c map[string]any) {
		uid := "r~" + wsSlugOther + "~" + f.run
		c["sub"], c["user_id"], c["fs"] = uid, uid, wsSlugOther
	})
	if err != nil {
		t.Fatalf("Verify refused a run identity for the other repository: %v", err)
	}
	got, err = f.ws.Shared(ctx, other, f.offered(wsMemberA))
	if err != nil {
		t.Fatalf("Shared for a run the project's list no longer names: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Shared = %v, want none: the member's project list no longer names the run's repository, and the child still admitting the credential does not decide for it", got)
	}
}

// TestWorkspaceServesThePaidKeyAMemberNamesByItsSentinel: the workspace's pool
// and the member's policy do not spell the project's own Anthropic API key the
// same way, and the pool-to-policy lookup has to pair them.
//
// The bundle holds it as config.AnthropicSecret ("anthropic-api-key"), which is
// the name the pool, the wire contract and the service's audit lines speak, and
// the member's published `accounts` block files it under
// config.AnthropicKeyAccount ("anthropic-api-key--project"), the sentinel
// DeriveAccounts derives the entry under. Asking the member's policy in the
// pool's own spelling finds no entry and drops the credential in silence — and
// silence is the failure mode: the pool would be offered to a member whose
// policy admits the key and served nothing, with no refusal anywhere to read.
// The pairing is the one the installation's own cascade makes (offeredToReview).
func TestWorkspaceServesThePaidKeyAMemberNamesByItsSentinel(t *testing.T) {
	f := newWSFixture(t)
	// The workspace's one bundle, and the owner's declaration that the key is
	// shared with A.
	b, err := ParseBundle([]byte(config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n"))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name:   wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{{Name: config.AnthropicSecret, Projects: []string{wsMemberA}}},
	}
	offered := WorkspaceShared(b, node)
	if len(offered) != 1 || offered[0].Name != config.AnthropicSecret || offered[0].Tier != creds.TierB {
		t.Fatalf("the pool's own declaration = %+v, want the paid key at tier B", offered)
	}
	// A's own policy names the key the way its own publish path writes it.
	f.setPolicy(wsMemberA, config.AnthropicKeyAccount)
	ctx := context.Background()
	got, err := f.ws.Shared(ctx, f.verifyOK(wsMemberA, wsAccountA), offered)
	if err != nil {
		t.Fatalf("Shared for the member whose policy names the sentinel: %v", err)
	}
	want := []creds.Credential{{Name: config.AnthropicSecret, Tier: creds.TierB}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Shared = %+v, want %+v: the member's policy admits this credential under the sentinel, and the pool speaks its own name for it", got, want)
	}
}

// the workspace's service: no run, no CLI and no person is handed a
// credential's value, and the deployment that holds the pool is the one place
// the rule could be broken by an accessor that looked convenient.
//
// The API is the test: no method of the service or of the caller it answers
// with mentions a value-carrying type (bundle.Entry), and the values the pool
// is spoken of in — creds.Credential — carry a name and a tier and nothing
// else. A route that wanted a value would have to reach the bundle's own
// accessor for the kind of credential it means, which is where the kinds are
// kept apart (Q6).
func TestWorkspaceServiceNeverReturnsACredentialValue(t *testing.T) {
	entry := reflect.TypeOf(Entry{})
	credential := reflect.TypeOf(creds.Credential{})
	for _, typ := range []reflect.Type{
		reflect.TypeOf(&Workspace{}), reflect.TypeOf(WorkspaceCaller{}), reflect.TypeOf(WorkspaceMember{}),
	} {
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			for j := 0; j < m.Type.NumOut(); j++ {
				if mentions(m.Type.Out(j), entry) {
					t.Errorf("%s.%s returns %s, which carries a credential's value", typ, m.Name, m.Type.Out(j))
				}
			}
		}
	}
	for i := 0; i < credential.NumField(); i++ {
		switch name := credential.Field(i).Name; name {
		case "Name", "Tier":
		default:
			t.Errorf("creds.Credential carries a field %s: the pool is spoken of in names and tiers", name)
		}
	}
	if mentions(reflect.TypeOf(WorkspaceCaller{}), entry) || mentions(reflect.TypeOf(WorkspaceMember{}), entry) {
		t.Error("the caller the service answers with carries a bundle entry: no field of it may be a credential")
	}
}

// mentions reports whether a type is, or is a container of, the type given.
func mentions(typ, want reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Slice, reflect.Array, reflect.Pointer:
		return mentions(typ.Elem(), want)
	case reflect.Map:
		return mentions(typ.Elem(), want) || mentions(typ.Key(), want)
	}
	if typ == want {
		return true
	}
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			if mentions(typ.Field(i).Type, want) {
				return true
			}
		}
	}
	return false
}

// TestNewWorkspaceRefusesAConfigurationItCannotCheck: every one of these is a
// service that would start and admit what it cannot check, which is the one
// outcome a security-sensitive deployment may not have. The refusals are
// configuration errors and the process does not come up.
func TestNewWorkspaceRefusesAConfigurationItCannotCheck(t *testing.T) {
	base := func() WorkspaceConfig {
		return WorkspaceConfig{
			Project:  wsWorkspace,
			Audience: wsAudience,
			Members: []WorkspaceMember{{
				Project: wsMemberA, FirebaseProject: wsMemberA, FP: wsMemberA,
				JobAccounts: []string{wsAccountA}, ExchangeURL: "https://exchange.project-a.example.com",
			}},
			OwnerPeople: []string{wsOwner},
			Keys:        staticKeys{testKid: &testKey().PublicKey},
			JobKeys:     staticKeys{testJobKid: &testKey().PublicKey},
			Open:        func(string) (Getter, error) { return &wsDB{nodes: map[string]any{}}, nil },
		}
	}
	cases := map[string]func(*WorkspaceConfig){
		"no member":          func(c *WorkspaceConfig) { c.Members = nil },
		"no audience":        func(c *WorkspaceConfig) { c.Audience = "" },
		"no caller keys":     func(c *WorkspaceConfig) { c.JobKeys = nil },
		"no run keys":        func(c *WorkspaceConfig) { c.Keys = nil },
		"no database opener": func(c *WorkspaceConfig) { c.Open = nil },
		"a member with no job service account": func(c *WorkspaceConfig) {
			c.Members[0].JobAccounts = nil
		},
		"a member whose project and fp disagree": func(c *WorkspaceConfig) {
			c.Members[0].FP = wsMemberB
		},
		"a member whose Firebase project is not a project name": func(c *WorkspaceConfig) {
			c.Members[0].FirebaseProject = "Project A"
		},
		// The database is derived from the Firebase project, so two members
		// naming one are two members reading one database — the splice finding 6
		// closed, arrived at by a duplicated field. The plan's property is that
		// two members' accounts can never be pointed at one database, and the
		// derivation alone does not hold it.
		"one Firebase project in two rows": func(c *WorkspaceConfig) {
			c.Members = append(c.Members, WorkspaceMember{
				Project: wsMemberB, FirebaseProject: wsMemberA, FP: wsMemberB,
				JobAccounts: []string{wsAccountB}, ExchangeURL: "https://exchange.project-b.example.com",
			})
		},
		"one account in two rows": func(c *WorkspaceConfig) {
			c.Members = append(c.Members, WorkspaceMember{
				Project: wsMemberB, FirebaseProject: wsMemberB, FP: wsMemberB,
				JobAccounts: []string{wsAccountA}, ExchangeURL: "https://exchange.project-b.example.com",
			})
		},
		"an address that is both the owner's person and a member's job": func(c *WorkspaceConfig) {
			c.OwnerPeople = []string{wsAccountA}
		},
		"a person that is not a printable address": func(c *WorkspaceConfig) {
			c.OwnerPeople = []string{"owner@exa\x00mple.com"}
		},
		"a person that is a service account address": func(c *WorkspaceConfig) {
			c.OwnerPeople = []string{"ci@workspace-alpha.iam.gserviceaccount.com"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			mutate(&cfg)
			if _, err := NewWorkspace(cfg); !errors.Is(err, ErrWorkspaceConfig) {
				t.Fatalf("NewWorkspace with %s = %v, want ErrWorkspaceConfig", name, err)
			}
		})
	}
	// And the configuration the tests are built on does start.
	if _, err := NewWorkspace(base()); err != nil {
		t.Fatalf("NewWorkspace of a complete configuration: %v", err)
	}
}

// TestWorkspaceIssuerMatchesTheLocalPins: the service's half of the aud/iss
// pair is the launcher's half. Google's Firebase signing keys are ONE global
// set, so what tells one project's tokens from another's is the pair aud/iss
// and nothing else: a service whose issuer derivation differed from the one the
// member's own config pins would refuse every honest call, or admit a token
// minted for another project.
//
// The two spellings compared are the LIVE ones: firebaseIssuer is what the run
// identity's `iss` is held to (an Authenticator built over the member's own
// FirebaseProject, which is the verifier every member of this deployment gets),
// and localcfg.WorkspaceIssuer is what a member's own pin is validated against.
// Pinning a helper of the test's own to the pin would hold nothing to anything.
func TestWorkspaceIssuerMatchesTheLocalPins(t *testing.T) {
	for _, project := range []string{wsMemberA, wsMemberB, "aurora-dev"} {
		if got, want := firebaseIssuer(project), localcfg.WorkspaceIssuer(project); got != want {
			t.Errorf("firebaseIssuer(%q) = %q, want %q: the service's check and the member's pin have to derive the same issuer", project, got, want)
		}
	}
	// The database derivation keeps the shape the credentials service's own
	// guard holds an installation's database URL to.
	url, err := workspaceDatabase(wsMemberA, "")
	if err != nil {
		t.Fatalf("workspaceDatabase: %v", err)
	}
	if want := "https://project-a-default-rtdb.firebaseio.com"; url != want {
		t.Errorf("workspaceDatabase(%q) = %q, want %q", wsMemberA, url, want)
	}
	if _, err := workspaceDatabase("Project A", ""); err == nil {
		t.Error("workspaceDatabase accepted a project id that is not one: the URL it derives would be a database nothing serves")
	}
}

// TestWorkspaceMembersOfReadsTheNodesRows is the one conversion between the
// owner's node and the service's table: a field the conversion drops is a
// member the service can never place, and a field it invents is one the node
// never declared.
func TestWorkspaceMembersOfReadsTheNodesRows(t *testing.T) {
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		Members: []projectpolicy.WorkspaceCredentialMember{{
			Project: wsMemberA, FirebaseProject: wsMemberA, DatabaseRegion: "europe-west1", FP: wsMemberA,
			JobAccounts: []string{wsAccountA, wsAccountOut},
			ExchangeURL: "https://exchange.project-a.example.com",
		}},
	}
	got := WorkspaceMembersOf(node)
	want := WorkspaceMember{
		Project: wsMemberA, FirebaseProject: wsMemberA, DatabaseRegion: "europe-west1", FP: wsMemberA,
		JobAccounts: []string{wsAccountA, wsAccountOut},
		ExchangeURL: "https://exchange.project-a.example.com",
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("WorkspaceMembersOf = %+v, want %+v", got, want)
	}
	// The table a node is read into is a copy: a later edit of the node may
	// not reach through the service's configuration.
	got[0].JobAccounts[0] = wsAccountB
	if node.Members[0].JobAccounts[0] != wsAccountA {
		t.Error("WorkspaceMembersOf aliases the node's row: a reader of the table may not write the owner's declaration")
	}
	if WorkspaceMembersOf(nil) != nil {
		t.Error("WorkspaceMembersOf(nil) is not nil: a workspace that published no credentials node has no table")
	}
}
