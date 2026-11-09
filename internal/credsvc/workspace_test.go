package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	wsOwner      = "owner@example.com"
	// wsMemberPerson is a member's own person: an address a member's launcher
	// list would name and this deployment holds no grant to read, so it is a
	// caller of no class here.
	wsMemberPerson = "operator@project-a.example.com"
	wsRepo         = "github/owner/repo-a"
	// wsSlug is what a run identity's `fs` carries: task.Slug's sanitised form
	// of the repository, which is the only spelling of the repository the
	// workspace's service ever sees (and a valid database path segment).
	wsSlug = "owner-repo-a"
	wsJob  = "fugaro-owner-repo-a"
	// wsShared is one credential of the workspace's pool. It is a name: the
	// pool's values live in the workspace's one bundle secret and no test
	// carries one.
	wsShared = "claude-oauth-token-team"
)

// wsDB is one member's database: the nodes it holds, keyed by the path a
// reader asks for, and the paths it was asked for. Every member gets one of
// its own, so a test can see WHICH member's state a call read — which is the
// property the database derivation exists for.
type wsDB struct {
	url   string
	nodes map[string]any
	paths []string
	err   error
}

func (d *wsDB) Get(_ context.Context, path string, out any) (bool, error) {
	d.paths = append(d.paths, path)
	if d.err != nil {
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
				JobAccounts: []string{wsAccountA}, ExchangeURL: "https://exchange.project-a.example.com",
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
func (f *wsFixture) verify(member, account string, mutate ...func(map[string]any)) (WorkspaceCaller, error) {
	f.t.Helper()
	return f.ws.Verify(context.Background(), Request{
		RunIDToken: f.runToken(member, mutate...),
		JobIDToken: f.jobToken(account),
		Job:        wsJob,
	})
}

// verifyOK is the happy path: the member's own job, its own run identity.
func (f *wsFixture) verifyOK(member, account string) WorkspaceCaller {
	f.t.Helper()
	caller, err := f.verify(member, account)
	if err != nil {
		f.t.Fatalf("Verify refused the member %s's own job: %v", member, err)
	}
	return caller
}

// setPolicy publishes one member's policy with one account admitted to this
// test's repository, which is the member's side of D1298's intersection.
func (f *wsFixture) setPolicy(member string, accounts ...string) {
	block := map[string]any{}
	for _, name := range accounts {
		block[name] = map[string]any{"allow_repos": []string{wsRepo}}
	}
	f.dbs[member].nodes[projectpolicy.PathShared()] = map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []string{wsRepo},
		"accounts":    block,
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
	// configured spelling, not the claim's.
	for _, tc := range []struct{ account, member string }{
		{wsAccountA, wsMemberA},
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
	if err := f.ws.MemberRoute(owner, wsMemberA); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("MemberRoute for one of the workspace owner's people = %v, want ErrUnverifiedJob", err)
	}
	if _, err := f.ws.Verify(ctx, Request{
		RunIDToken: f.runToken(wsMemberA), JobIDToken: f.ownerToken(), Job: wsJob,
	}); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Verify for one of the workspace owner's people = %v, want ErrUnverifiedJob: it holds no run identity", err)
	}
	// A member's route admits its own member and no other.
	if err := f.ws.MemberRoute(job, wsMemberA); err != nil {
		t.Errorf("MemberRoute for the member's own job = %v, want it admitted", err)
	}
	if err := f.ws.MemberRoute(job, wsMemberB); !errors.Is(err, ErrUnverifiedJob) {
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
func TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt(t *testing.T) {
	f := newWSFixture(t)
	f.setPolicy(wsMemberA, wsShared) // A's own policy names the credential
	f.setPolicy(wsMemberB)           // B's own policy names no account
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

// TestWorkspaceServiceNeverReturnsACredentialValue is the package's rule 2, at
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
func TestWorkspaceIssuerMatchesTheLocalPins(t *testing.T) {
	for _, project := range []string{wsMemberA, wsMemberB, "aurora-dev"} {
		if got, want := workspaceIssuer(project), localcfg.WorkspaceIssuer(project); got != want {
			t.Errorf("workspaceIssuer(%q) = %q, want %q: the service and the member's pin have to derive the same issuer", project, got, want)
		}
	}
	// The database derivation keeps the shape the credentials service's own
	// guard holds an installation's database URL to.
	url, err := workspaceDatabase(wsMemberA)
	if err != nil {
		t.Fatalf("workspaceDatabase: %v", err)
	}
	if want := "https://project-a-default-rtdb.firebaseio.com"; url != want {
		t.Errorf("workspaceDatabase(%q) = %q, want %q", wsMemberA, url, want)
	}
	if _, err := workspaceDatabase("Project A"); err == nil {
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
			Project: wsMemberA, FirebaseProject: wsMemberA, FP: wsMemberA,
			JobAccounts: []string{wsAccountA, wsAccountOut},
			ExchangeURL: "https://exchange.project-a.example.com",
		}},
	}
	got := WorkspaceMembersOf(node)
	want := WorkspaceMember{
		Project: wsMemberA, FirebaseProject: wsMemberA, FP: wsMemberA,
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
