package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The workspace's scope under test (WS2). The fixture is workspace_test.go's —
// the two members, the frozen table, the member databases — and what this file
// adds is the other half a deployment is built from: the published node, the
// resolver over it, and the process a workspace mode runs (the SAME front door
// and handlers an installation runs, with the workspace's scope resolver handed
// in — which is the whole of D2980's one binary, two deployments).

// wsNode is the workspace's published node as the resolver reads it: the value
// the workspace owner's `fugaroo update` left, whether it is there at all, and
// what a read answers when it fails. It counts its reads, because R13's minute
// is a statement about how OFTEN the node is read and about nothing else.
type wsNode struct {
	mu    sync.Mutex
	node  *projectpolicy.WorkspaceCredentials
	found bool
	err   error
	reads int
}

// wsPublished is the node reader of a workspace that has published one.
func wsPublished(node *projectpolicy.WorkspaceCredentials) *wsNode {
	return &wsNode{node: node, found: true}
}

func (n *wsNode) WorkspaceNode(context.Context) (*projectpolicy.WorkspaceCredentials, bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reads++
	if n.err != nil {
		return nil, false, n.err
	}
	return n.node, n.found, nil
}

// publish is the workspace owner's `fugaro update` landing a new node.
func (n *wsNode) publish(node *projectpolicy.WorkspaceCredentials) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.node, n.found, n.err = node, true, nil
}

// fail is a database the node cannot be read from, which on a re-read is R17's
// case and not the caller's.
func (n *wsNode) fail(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.err = err
}

// readsSoFar is how many times the node has been read: R13's minute is not a
// claim about a value but about the number of reads.
func (n *wsNode) readsSoFar() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.reads
}

// wsPoolName is the name the workspace's pool speaks for the fixture's shared
// credential, and it is deliberately NOT the string the fixture's own
// declaration uses: a bundle's model index is keyed by the credential's NAME
// (the Claude account "team") while the document that holds its value is keyed
// by config.ClaudeAccountSecret ("claude-oauth-token-team"). WorkspaceShared
// resolves one into the other through Bundle.Lookup, so a node written with the
// container key is a declaration the pool cannot match and offers nobody —
// which is a state a test has to be able to tell from a member who refused it.
// wsExtra is the same for the second account.
var (
	wsPoolName = mustCredentialName(wsShared)
	wsExtra    = mustCredentialName(config.ClaudeAccountSecret("extra"))
)

// mustCredentialName is the name of a credential the fixture's own document
// declares, through the one reader that says what a document key names.
func mustCredentialName(key string) string {
	name, _, _, ok := CredentialKey(key)
	if !ok {
		panic("the workspace fixture's credential key is not a credential key: " + key)
	}
	return name
}

// wsMemberRow is one member's row of a version-2 node: the addresses its
// tokens are issued for, and the concrete repositories each of its job accounts
// is a job of (C3's JobAccountRepos, which is where R6 and D2992 land).
func wsMemberRow(project, fp string, accounts []string, repos [][]string) projectpolicy.WorkspaceCredentialMember {
	row := projectpolicy.WorkspaceCredentialMember{
		Project:         project,
		FirebaseProject: project,
		FP:              fp,
		JobAccounts:     accounts,
	}
	for i, account := range accounts {
		row.JobAccountRepos = append(row.JobAccountRepos, projectpolicy.WorkspaceJobAccount{Account: account, Repos: repos[i]})
	}
	return row
}

// wsNodeFor is the node a workspace publishes for the fixture's two members:
// member A's first account declared on `aRepos`, its second on `a2Repos`, and
// member B's one account on `bRepos`. It is the CHANGEABLE half of a
// workspace's deployment — the member table the workspace owner writes — which
// is why a test builds it here and swaps it between calls.
func wsNodeFor(aRepos, a2Repos, bRepos []string) *projectpolicy.WorkspaceCredentials {
	return &projectpolicy.WorkspaceCredentials{
		Version: projectpolicy.WorkspaceCredentialsVersion,
		Name:    wsWorkspace,
		Members: []projectpolicy.WorkspaceCredentialMember{
			wsMemberRow(wsMemberA, wsMemberA, []string{wsAccountA, wsAccountA2}, [][]string{aRepos, a2Repos}),
			wsMemberRow(wsMemberB, wsMemberB, []string{wsAccountB}, [][]string{bRepos}),
		},
		Shared: []projectpolicy.WorkspaceShared{
			{Name: wsPoolName, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA, wsMemberB}},
		},
	}
}

// wsOneRepo is the node a fixture starts from: every member's account declared
// on the fixture's own repository, and the pool's one credential offered to
// both members.
func wsOneRepo() *projectpolicy.WorkspaceCredentials {
	return wsNodeFor([]string{wsRepo}, []string{wsRepo}, []string{wsRepo})
}

// wsDeployment is a workspace's deployment under test: the frozen *Workspace, the
// published node and the resolver built from them, plus the fixture the members'
// databases live in.
type wsDeployment struct {
	*wsFixture
	node *wsNode
	res  ScopeResolver
}

func newWSDeployment(t *testing.T, node *wsNode, mutate ...func(*WorkspaceScopeConfig)) *wsDeployment {
	t.Helper()
	f := newWSFixture(t)
	if node == nil {
		node = wsPublished(wsOneRepo())
	}
	cfg := WorkspaceScopeConfig{
		Workspace: f.ws,
		Node:      node,
		// The clock is the FIXTURE's, read through the fixture: a test that
		// wants R13's minute reaches it by moving f.now, which the tokens, the
		// verifier and this resolver then all agree about.
		Now: func() time.Time { return f.now },
	}
	for _, m := range mutate {
		m(&cfg)
	}
	res, err := NewWorkspaceScopeResolver(cfg)
	if err != nil {
		t.Fatalf("NewWorkspaceScopeResolver: %v", err)
	}
	return &wsDeployment{wsFixture: f, node: node, res: res}
}

// onRepo is the run identity of the same member's job naming another
// repository: the identity Verify holds to the uid, so the three claims move
// together.
func onRepo(slug string) func(map[string]any) {
	return func(c map[string]any) {
		uid := "r~" + slug + "~" + strings.TrimPrefix(c["user_id"].(string), "r~"+wsSlug+"~")
		c["sub"], c["user_id"], c["fs"] = uid, uid, slug
	}
}

// request is one call of a member's job as the front door sees it: the job's
// Google identity in Authorization, the run's token in its own header and the
// job name the run reports.
func (s *wsDeployment) request(t *testing.T, path, account string, mutate ...func(map[string]any)) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, wsAudience+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	s.hdr(account, mutate...)(req)
	return req
}

// hdr is request's own headers as a mutate, for the calls a test makes through
// the whole front door (svc.callPath) rather than through the resolver alone.
// It is one function so the two paths cannot disagree about what a member's job
// presents.
func (s *wsDeployment) hdr(account string, mutate ...func(map[string]any)) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.jobToken(account))
		r.Header.Set(HeaderRunIdentity, s.runToken(wsMemberA, mutate...))
		r.Header.Set(HeaderRunJob, wsJob)
	}
}

// lines is the audit lines written so far, and it is auditLines for a buffer
// that may hold none: a route answered before a verification writes no line at
// all, and that is the fact half of this file's assertions about the count.
func lines(buf interface{ String() string }) []map[string]any {
	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil
		}
		out = append(out, m)
	}
	return out
}

// resolve is request + the resolver, which is the whole of what the front door
// asks of a workspace's deployment before a handler runs.
func (s *wsDeployment) resolve(t *testing.T, path, account string, mutate ...func(map[string]any)) (Scope, error) {
	t.Helper()
	sc, err := s.res.Resolve(context.Background(), s.request(t, path, account, mutate...))
	if err != nil {
		return nil, err
	}
	if sc == nil {
		t.Fatal("Resolve answered no scope and no error, which is C1's one forbidden pair")
	}
	return sc, nil
}

// served is the happy path: a scope that is served and whose caller is the
// member's job.
func (s *wsDeployment) served(t *testing.T, path, account string, mutate ...func(map[string]any)) Scope {
	t.Helper()
	sc, err := s.resolve(t, path, account, mutate...)
	if err != nil {
		t.Fatalf("Resolve refused the member %s's own job: %v", account, err)
	}
	if !sc.Serves(path) {
		t.Fatalf("the scope does not serve %s, and a member's own call on it is served", path)
	}
	return sc
}

// wsPoolDoc is the workspace's own bundle: the credential the node declares
// shared, one it does not, and a third the node declares and the member's own
// policy does not admit — so the whole bundle, the node's pool and the
// intersection are three sets a test can tell apart.
func wsPoolDoc(t *testing.T) []byte {
	t.Helper()
	return []byte(wsShared + ": " + fakeCred(t, "team") + "\n" +
		config.ClaudeAccountSecret("extra") + ": " + fakeCred(t, "extra") + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
}

// wsPool is wsPoolDoc parsed.
func wsPool(t *testing.T) *Bundle {
	t.Helper()
	b, err := ParseBundle(wsPoolDoc(t))
	if err != nil {
		t.Fatalf("ParseBundle of the workspace's pool: %v", err)
	}
	return b
}

// allowModels publishes the member's model allow-list, which every Messages
// call of a project is pinned to: a member that publishes none is refused
// before a credential is read (requestNoAllowListRefusal), which is a rule of
// the service and not of this task, so a test of a SERVED call has to publish
// one. It is written beside the policy the fixture publishes and touches no
// other block.
func (s *wsDeployment) allowModels(member string, models ...string) {
	s.t.Helper()
	node, ok := s.dbs[member].nodes[projectpolicy.PathShared()].(map[string]any)
	if !ok {
		s.t.Fatalf("no policy node is published for %s", member)
	}
	b := make([]any, 0, len(models))
	for _, m := range models {
		b = append(b, m)
	}
	node["budget"] = map[string]any{"mode": "enforce", "per_run_usd": 5, "allowed_models": b}
	node["credentials"] = map[string]any{"fallback": "auto"}
}

// serve is setPolicy + allowModels: the state a member's own `fugaro update`
// leaves for a repository whose runs may call the service.
func (s *wsDeployment) serve(member string, accounts ...string) {
	s.t.Helper()
	s.setPolicy(member, accounts...)
	s.allowModels(member, testModel)
}

// serveOn is setPolicyOn + allowModels, for the tests that need the member's
// own lists to name more than the one repository.
func (s *wsDeployment) serveOn(member string, repos []string, accounts ...string) {
	s.t.Helper()
	s.setPolicyOn(member, repos, accounts...)
	s.allowModels(member, testModel)
}

// wsService is a workspace-mode PROCESS: the installation's own front door,
// gate and handlers (server.go is one code path for both deployments) with the
// workspace's resolver handed in, which is the whole of what the deployment
// differs by.
func wsService(t *testing.T, s *wsDeployment) *svc {
	t.Helper()
	return newSvcWith(t, func(svc *svc, cfg *Config) {
		cfg.Resolver = s.res
		svc.src.docs = [][]byte{wsPoolDoc(t)}
	})
}

// ---------------------------------------------------------------------------
// R11, R13, R17: who admits a caller, and how often that is re-read
// ---------------------------------------------------------------------------

// TestWorkspaceScopeIgnoresADeclaredMember: the FROZEN table the process was
// built from is not what admits a caller — the published node is (R11, C1's
// rule 11). A member the workspace owner has removed from the node still
// resolves its token against the table the deployment started with, and the
// scope refuses it all the same, because a service that served it would go on
// drawing on the pool for a member who left.
//
// The refusal is a *ScopeFault and never a refusal of the caller, and that is
// the choice this file's other half is about: a refusal of a VERIFIED caller
// owes exactly one audit line, the auditor belongs to the Server, and a fault
// carrying the verified Caller is the channel the gate writes that line
// through. A callerRefusal here would be a refusal nobody audited — and the
// zero-line path this package forbids outright.
func TestWorkspaceScopeIgnoresADeclaredMember(t *testing.T) {
	// The control first: the node names the member, so its job is served, and
	// the scope is the one built over that member's own database.
	s := newWSDeployment(t, nil)
	sc := s.served(t, MessagesPath, wsAccountA)
	if sc.Caller().JobAccount != wsAccountA {
		t.Errorf("the scope's caller = %q, want the account that called", sc.Caller().JobAccount)
	}

	// The member is dropped from the node and nothing else changes. The
	// workscape's own table still resolves the caller, which is what makes this
	// a test of the node and not of the token.
	node := wsOneRepo()
	node.Members = node.Members[1:]
	s.node.publish(node)
	s.now = s.now.Add(2 * time.Minute)
	before := s.dbs[wsMemberA].paths
	_, err := s.resolve(t, MessagesPath, wsAccountA)
	var fault *ScopeFault
	if !errors.As(err, &fault) {
		t.Fatalf("Resolve for a member the node no longer names = %v, want a *ScopeFault", err)
	}
	if fault.Caller == nil {
		t.Error("the fault carries no Caller, so the gate would write no audit line for a refusal of a VERIFIED caller")
	}
	var refusal *callerRefusal
	if errors.As(err, &refusal) {
		t.Error("the refusal is a caller refusal: a verdict about a caller who did nothing wrong, whose answer closure would write no audit line")
	}
	if !errors.Is(fault, ErrUnverifiedJob) {
		t.Errorf("the fault's cause = %v, want the job identity's refusal", fault.Err)
	}
	// What the removed member's database was read FOR after the refusal is
	// Verify's own liveness scan — the outcomes, which every call of the frozen
	// table reads before the node is consulted at all — and nothing the scope
	// reads: a caller the node does not admit has no policy and no admission
	// read for it.
	for _, path := range s.dbs[wsMemberA].paths[len(before):] {
		if path == projectpolicy.PathShared() || path == projectpolicy.PathRepo(wsSlug) {
			t.Errorf("the removed member's %q was read: a scope the node does not admit reads no policy and no admission behind it", path)
		}
	}

	// The same for one ACCOUNT dropped from a row that stays: the row is a
	// list and every address of it is a membership the owner can revoke.
	node = wsOneRepo()
	node.Members[0].JobAccounts = []string{wsAccountA}
	node.Members[0].JobAccountRepos = node.Members[0].JobAccountRepos[:1]
	s.node.publish(node)
	s.now = s.now.Add(2 * time.Minute)
	if _, err := s.resolve(t, MessagesPath, wsAccountA2); !errors.As(err, &fault) {
		t.Errorf("Resolve for an account the row no longer names = %v, want a *ScopeFault", err)
	}
	// ... and the member's other account is still served, so the refusal above
	// is the account's and not the row's.
	s.served(t, MessagesPath, wsAccountA)

	// A workspace that published nothing admits nobody. Absent is not an empty
	// member table and not an error: the answer is the same refusal.
	empty := &wsNode{}
	plain := newWSDeployment(t, empty)
	if _, err := plain.resolve(t, MessagesPath, wsAccountA); !errors.As(err, &fault) {
		t.Errorf("Resolve with no node published = %v, want a *ScopeFault", err)
	}
	if plain.node.readsSoFar() != 1 {
		t.Errorf("the node was read %d times, want the one read the absent answer came from", plain.node.readsSoFar())
	}
}

// TestWorkspaceScopeRereadsItsNode: the copy of the node is served for a minute
// and then re-read (R13), so a member added, an account dropped or an entry
// shared takes effect with no new revision — and nothing re-reads it sooner
// than that, because the node is the one read this service makes that is not a
// member's own.
func TestWorkspaceScopeRereadsItsNode(t *testing.T) {
	s := newWSDeployment(t, nil)
	s.served(t, MessagesPath, wsAccountA)
	if got := s.node.readsSoFar(); got != 1 {
		t.Fatalf("the node was read %d times for the first call, want 1", got)
	}
	// A second call inside the minute is served from the copy held.
	s.served(t, MessagesPath, wsAccountA)
	if got := s.node.readsSoFar(); got != 1 {
		t.Errorf("the node was read %d times inside the minute, want the copy held (R13 is a minute of staleness and not a read per call)", got)
	}
	// The minute is a whole minute: 59 seconds are still inside it.
	s.now = s.now.Add(59 * time.Second)
	s.served(t, MessagesPath, wsAccountA)
	if got := s.node.readsSoFar(); got != 1 {
		t.Errorf("the node was read %d times 59 seconds in, want the copy held", got)
	}
	// And the entry shared in the meantime is the one that takes effect: the
	// member's own policy admits it, the node did not offer it, and the re-read
	// is what offers it.
	b := wsPool(t)
	before, err := s.served(t, MessagesPath, wsAccountA).Offering().Offering(context.Background(), b)
	if err != nil {
		t.Fatalf("Offering: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("the offering before the share = %v, want none: the node does not offer the credential yet", before)
	}
	node := wsOneRepo()
	node.Shared = append(node.Shared, projectpolicy.WorkspaceShared{
		Name: config.AnthropicSecret, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA},
	})
	s.node.publish(node)
	s.now = s.now.Add(2 * time.Second)
	// The member's own list keys the paid key by the key's ACCOUNT name and not
	// by the secret's: policyKey is the one place that mapping lives, and a
	// declaration written the other way is a credential the intersection drops
	// for a reason that is not this test's.
	s.setPolicyOn(wsMemberA, []string{wsRepo}, wsPoolName, config.AnthropicKeyAccount)
	after, err := s.served(t, MessagesPath, wsAccountA).Offering().Offering(context.Background(), b)
	if err != nil {
		t.Fatalf("Offering after the share: %v", err)
	}
	if got := s.node.readsSoFar(); got != 2 {
		t.Errorf("the node was read %d times past the minute, want 2", got)
	}
	if len(after) != 2 {
		t.Errorf("the offering after the share = %v, want the shared entry the re-read brought: the copy held was stale", after)
	}
}

// TestAFailedRereadRefuses: a re-read that fails refuses the call and never
// serves from the copy held (R17). The caller did nothing wrong and is owed the
// truth that the service could not answer it (R16), so the refusal is a
// *ScopeFault the front door answers as the service's own failure — not the
// 403 a run's gateway reads as a missing invoke grant.
func TestAFailedRereadRefuses(t *testing.T) {
	s := newWSDeployment(t, nil)
	s.served(t, MessagesPath, wsAccountA)

	// The node's database goes down after the copy was taken, and the minute
	// that would re-read it passes.
	s.node.fail(errors.New("the workspace's database is unavailable"))
	s.now = s.now.Add(2 * time.Minute)
	_, err := s.resolve(t, MessagesPath, wsAccountA)
	var fault *ScopeFault
	if !errors.As(err, &fault) {
		t.Fatalf("Resolve with a failed re-read = %v, want a *ScopeFault", err)
	}
	if fault.Caller == nil {
		t.Error("the fault carries no Caller, so the caller's one audit line would not be written")
	}
	if got := s.node.readsSoFar(); got != 2 {
		t.Errorf("the node was read %d times, want the re-read that failed", got)
	}
	// The failed read dropped the copy: the caller after it is refused too,
	// which is the "never serves from the stale table" half — a second call
	// does not silently serve what the first could not re-read.
	s.now = s.now.Add(time.Second)
	if _, err := s.resolve(t, MessagesPath, wsAccountA); !errors.As(err, &fault) {
		t.Errorf("the call after a failed re-read = %v, want a refusal: the copy held was dropped", err)
	}

	// Through the front door: the fault is the service's own failure, and the
	// verified caller gets exactly one audit line for it.
	svc := wsService(t, s)
	resp, body := svc.call(t, callBody("ws2-reread"), s.hdr(wsAccountA))
	if resp.StatusCode != StatusUnavailable {
		t.Errorf("the status = %d, want %d: a node that could not be re-read is not a caller who is not admitted", resp.StatusCode, StatusUnavailable)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal type = %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if got := lines(svc.audit); len(got) != 1 {
		t.Errorf("the fault wrote %d audit lines, want exactly 1: a refusal after a verification writes one and only one", len(got))
	}

	// And the database coming back is what serves the caller again — with a
	// fresh read, since the copy was dropped rather than kept.
	s.node.publish(wsOneRepo())
	s.now = s.now.Add(2 * time.Minute)
	s.served(t, MessagesPath, wsAccountA)
}

// TestWorkspaceScopeAnswersOnlyC1sTwoErrorShapes: every error this resolver
// answers is one of C1's two shapes — a callerRefusal, whose own closure writes
// the answer, or a *ScopeFault. A bare error is the one thing it may never
// answer, because refuseResolve's last branch writes an unclassified error's
// own sentence to the wire, and a workspace resolver's sentences are built from
// its readers' (WS1's review note on the seam).
func TestWorkspaceScopeAnswersOnlyC1sTwoErrorShapes(t *testing.T) {
	// Every failing call of this deployment: a call with no identity, one with a
	// token this deployment did not mint, one whose run identity names another
	// member, one from an account outside the workspace, one whose member the
	// node does not name, and a node that cannot be read.
	cases := map[string]func(*wsDeployment) (*http.Request, error){
		"no identity at all": func(s *wsDeployment) (*http.Request, error) {
			return http.NewRequest(http.MethodPost, "https://"+wsAudience+MessagesPath, strings.NewReader("{}"))
		},
		"a token signed by somebody else": func(s *wsDeployment) (*http.Request, error) {
			req := s.request(t, MessagesPath, wsAccountA)
			req.Header.Set(HeaderRunIdentity, "not-a-token-this-deployment-can-read-at-all")
			return req, nil
		},
		"an account outside the workspace": func(s *wsDeployment) (*http.Request, error) {
			return s.request(t, MessagesPath, wsAccountOut), nil
		},
		"a run identity of another member": func(s *wsDeployment) (*http.Request, error) {
			return s.request(t, MessagesPath, wsAccountA, func(c map[string]any) {
				c["iss"] = "https://securetoken.google.com/" + wsMemberB
				c["aud"] = wsMemberB
				c["fp"] = wsMemberB
			}), nil
		},
		"a member the node does not name": func(s *wsDeployment) (*http.Request, error) {
			node := wsOneRepo()
			node.Members = node.Members[1:]
			s.node.publish(node)
			return s.request(t, MessagesPath, wsAccountA), nil
		},
		"a node that cannot be read": func(s *wsDeployment) (*http.Request, error) {
			s.node.fail(errors.New("the database is unavailable"))
			return s.request(t, MessagesPath, wsAccountA), nil
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			s := newWSDeployment(t, nil)
			req, err := build(s)
			if err != nil {
				t.Fatalf("building the call: %v", err)
			}
			_, err = s.res.Resolve(context.Background(), req)
			if err == nil {
				t.Fatal("Resolve answered no error for a call that cannot be served")
			}
			var fault *ScopeFault
			var refusal *callerRefusal
			if !errors.As(err, &fault) && !errors.As(err, &refusal) {
				t.Fatalf("Resolve answered a bare error (%v): refuseResolve would write its own sentence to the wire, and C1's two shapes are the whole of what a resolver may answer", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// R5, R6, D2992, F28: what the pool is, and which repository a run may be on
// ---------------------------------------------------------------------------

// TestWorkspaceScopeOffersOnlyTheIntersection is this task's red step: while the
// offering is the bundle the member's run is served every credential the
// workspace holds, which is what R5 and D1298 exist to prevent. The offering is
// the INTERSECTION — the node's shared entries the bundle carries, narrowed by
// the member's own policy and by the admission of the repository the run is on —
// and it is read from the MEMBER's database and from no other.
func TestWorkspaceScopeOffersOnlyTheIntersection(t *testing.T) {
	ctx := context.Background()
	b := wsPool(t)
	if len(b.Credentials()) != 3 {
		t.Fatalf("the fixture's bundle holds %d credentials, want the 3 the intersection is told apart from", len(b.Credentials()))
	}
	node := wsOneRepo()
	node.Shared = append(node.Shared, projectpolicy.WorkspaceShared{
		Name: config.AnthropicSecret, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA},
	})
	s := newWSDeployment(t, wsPublished(node))
	// Member A's own policy admits one of the three, and its repository child
	// admits it there. Member B's policy admits the second — and this call is
	// not B's, which is what makes the read's own database part of the test: a
	// reader that consulted the wrong member would offer two.
	s.setPolicy(wsMemberA, wsPoolName)
	s.setPolicy(wsMemberB, config.AnthropicKeyAccount)

	got, err := s.served(t, MessagesPath, wsAccountA).Offering().Offering(ctx, b)
	if err != nil {
		t.Fatalf("Offering: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsPoolName {
		t.Errorf("Offering = %v, want just %q: the pool is the intersection of the workspace's share, the member's own policy and the repository's admission", got, wsPoolName)
	}
	if !s.dbs[wsMemberA].read(projectpolicy.PathShared()) {
		t.Error("the calling member's own policy was not read: the intersection's member leg is that member's database")
	}
	if !s.dbs[wsMemberA].read(projectpolicy.PathRepo(wsSlug)) {
		t.Error("the run's repository child was not read: the admission of the repository the run verified is the member's other leg")
	}
	if len(s.dbs[wsMemberB].paths) != 0 {
		t.Errorf("another member's database was read (%v): the read is the calling member's own", s.dbs[wsMemberB].paths)
	}

	// The other direction, on the same deployment: the node stops offering it
	// and the member's policy still admits it, which is the workspace owner's
	// half of the intersection and not the member's.
	node = wsOneRepo()
	node.Shared = nil
	s = newWSDeployment(t, wsPublished(node))
	s.setPolicy(wsMemberA, wsPoolName)
	got, err = s.served(t, MessagesPath, wsAccountA).Offering().Offering(ctx, b)
	if err != nil {
		t.Fatalf("Offering with nothing shared: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Offering = %v, want none: the workspace owner's node does not offer it, and a member's own policy cannot reach a credential the pool does not hold", got)
	}
}

// TestWorkspaceScopeHoldsTheSlugToTheCallingAccount: the run's VERIFIED slug is
// resolved through the repositories the workspace owner declared for the CALLING
// job account (R6, D2992, F28), so a member's run on a repository its account is
// not a job of is refused at the pairing — whatever the member's own lists say.
// It is where R19 lands, and this test is the half of it that is this file's:
// the table the pairing resolves through.
func TestWorkspaceScopeHoldsTheSlugToTheCallingAccount(t *testing.T) {
	// The member's OWN policy names both repositories and admits the credential
	// on both, so nothing here is the member's leg: the only thing that can
	// refuse the second repository is the workspace owner's declaration.
	s := newWSDeployment(t, nil)
	s.serveOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsPoolName)

	sc := s.served(t, MessagesPath, wsAccountA)
	if canonical, ok := sc.Repos()[wsSlug]; !ok || canonical != wsRepo {
		t.Errorf("Repos() = %v, want the account's own repository %q", sc.Repos(), wsRepo)
	}
	if _, ok := sc.Repos()[wsSlugOther]; ok {
		t.Errorf("Repos() = %v holds the other repository: the table is the CALLING account's declaration and not the member's allow-list", sc.Repos())
	}
	// The run on the other repository is a call the scope will not serve: the
	// slug is not in the table, so the front door's pairing refuses it (see
	// TestAnUnlistedRepositoryNamesTheFix for the refusal itself).
	sc = s.served(t, MessagesPath, wsAccountA, onRepo(wsSlugOther))
	if _, ok := sc.Repos()[wsSlugOther]; ok {
		t.Error("Repos() holds a repository the calling account is not declared on: the workspace owner's declaration is what the run is held to")
	}
	// The offering is NOT the fence and must not be read as one: it is the
	// member's own policy and the repository's admission, and the member's
	// policy here admits the credential on both repositories — so the run the
	// owner's declaration does not hold is offered it, and it is the TABLE that
	// refuses the call, at the pairing the gate makes before any of these
	// readers is asked (TestAnUnlistedRepositoryNamesTheFix). A reader that
	// answered nothing here would be a second fence, and one that could not say
	// what refused the call.
	got, err := sc.Offering().Offering(context.Background(), wsPool(t))
	if err != nil {
		t.Fatalf("Offering: %v", err)
	}
	if len(got) != 1 || got[0].Name != wsPoolName {
		t.Errorf("Offering = %v, want the credential the member's own policy admits on the repository the run verified: the fence is the table and not this reader", got)
	}

	// The account's second job account is a caller of its own, and its own
	// declaration is the one that decides: A2 declared on the other repository
	// serves a run on that repository and holds nothing of A's.
	node := wsOneRepo()
	node.Members[0].JobAccountRepos[1].Repos = []string{wsRepoOther}
	s = newWSDeployment(t, wsPublished(node))
	s.serveOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsPoolName)
	sc = s.served(t, MessagesPath, wsAccountA2, onRepo(wsSlugOther))
	if canonical, ok := sc.Repos()[wsSlugOther]; !ok || canonical != wsRepoOther {
		t.Errorf("Repos() for the member's second account = %v, want the repository ITS declaration names", sc.Repos())
	}
	if _, ok := sc.Repos()[wsSlug]; ok {
		t.Error("Repos() holds the other account's repository: one account's declaration is not another's")
	}

	// And an account the node lists with no repositories declared is an empty
	// table — a state (a version-1 row, or an owner who has not declared them
	// yet) and never "every repository".
	s = newWSDeployment(t, wsPublished(wsNodeFor(nil, nil, nil)))
	s.serveOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsPoolName)
	sc = s.served(t, MessagesPath, wsAccountA)
	if len(sc.Repos()) != 0 {
		t.Errorf("Repos() = %v, want the empty table: a declaration that names no repository names none", sc.Repos())
	}
	// It is an empty map and not a nil one: a nil map is the scope of a call
	// that was never resolved.
	if sc.Repos() == nil {
		t.Error("Repos() answered nil: the empty table is a state of the account's declaration, not an unresolved call")
	}
}

// TestWorkspaceScopeServesASharedProjectJob: a shared project job's account is
// a job of SEVERAL repositories (O3, O4, D2992), and its declaration is what
// holds each of its runs to the repository that run is on. The one account
// serves both, and each run is served the offering of the repository it
// verified.
func TestWorkspaceScopeServesASharedProjectJob(t *testing.T) {
	// The member's second account is a shared job's: a job of both the
	// project's repositories at once.
	node := wsOneRepo()
	node.Members[0].JobAccountRepos[1].Repos = []string{wsRepo, wsRepoOther}
	s := newWSDeployment(t, wsPublished(node))
	s.serveOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsPoolName)

	for _, tc := range []struct {
		name string
		slug string
		repo string
	}{
		{"the first repository", wsSlug, wsRepo},
		{"the second repository", wsSlugOther, wsRepoOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := s.served(t, MessagesPath, wsAccountA2, onRepo(tc.slug))
			if len(sc.Repos()) != 2 {
				t.Errorf("Repos() = %v, want both of the shared job's repositories: the declaration is a list, and both of its runs resolve through it", sc.Repos())
			}
			if canonical, ok := sc.Repos()[tc.slug]; !ok || canonical != tc.repo {
				t.Errorf("Repos()[%q] = %q, %v, want %q", tc.slug, canonical, ok, tc.repo)
			}
			got, err := sc.Offering().Offering(context.Background(), wsPool(t))
			if err != nil {
				t.Fatalf("Offering: %v", err)
			}
			if len(got) != 1 || got[0].Name != wsPoolName {
				t.Errorf("Offering = %v, want the credential the member's own policy admits on the repository the run verified", got)
			}
		})
	}
}

// TestAnUnlistedRepositoryNamesTheFix: a run whose repository the workspace
// owner has not declared for its account is refused at the pairing, with the
// typed reason the run reads (R19). The sentence that names the workspace
// owner's `fugaro update` is the RUN's and is written where the run's other
// refusals are (R16's missing grant and R19 are one mapping, internal/gateway,
// WS11) — what this task owns, and what this test pins, is that the scope
// answers the repository table such a refusal is built on, and that the front
// door's answer for it is a typed one and not the service's own failure.
//
// The empty declaration is the same refusal: a version-1 row, or an owner who
// has added an account and not declared its repositories yet, is an account
// that is a job of nothing.
func TestAnUnlistedRepositoryNamesTheFix(t *testing.T) {
	s := newWSDeployment(t, nil)
	// The member's own policy admits the credential on both repositories, so
	// the refusal below is the owner's declaration and not the member's lists.
	s.serveOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsPoolName)
	svc := wsService(t, s)

	// The control: the repository the account IS declared on is served.
	resp, body := svc.call(t, callBody("ws2-listed"), s.hdr(wsAccountA))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call on the declared repository = %d, want 200: %s", resp.StatusCode, body)
	}

	resp, body = svc.callPath(t, MessagesPath, callBody("ws2-unlisted"), s.hdr(wsAccountA, onRepo(wsSlugOther)))
	if resp.StatusCode != StatusUnverified {
		t.Errorf("the call on an undeclared repository = %d, want %d", resp.StatusCode, StatusUnverified)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal type = %q, want %q: the typed reason the run's halt is built on", e.Error, ErrorUnverifiedRunIdentity)
	}
	if got := lines(svc.audit); len(got) != 2 {
		t.Errorf("the two calls wrote %d audit lines, want one for each (the served call's and the refused one's)", len(got))
	}

	// An account the owner lists with no repositories declared: every run of it
	// is refused the same way, including one on a repository its member's own
	// policy admits.
	unlisted := newWSDeployment(t, wsPublished(wsNodeFor(nil, nil, nil)))
	unlisted.serveOn(wsMemberA, []string{wsRepo, wsRepoOther}, wsPoolName)
	svc = wsService(t, unlisted)
	resp, body = svc.call(t, callBody("ws2-unlisted-declaration"), unlisted.hdr(wsAccountA))
	if resp.StatusCode != StatusUnverified {
		t.Errorf("the call of an account whose declaration is empty = %d, want %d", resp.StatusCode, StatusUnverified)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal type = %q, want %q: an empty declaration is the state R19's refusal is for", e.Error, ErrorUnverifiedRunIdentity)
	}
}

// ---------------------------------------------------------------------------
// R8: the one store a workspace call writes, and the member it may not
// ---------------------------------------------------------------------------

// TestWorkspaceScopeWritesNothingToAMember: a served workspace call reads four
// things from the member's own database — the policy, the repository's
// admission, the kill switches and the per-repository auth record — and reads
// none of them anywhere else; every write it makes lands in the WORKSPACE's own
// store (R8, D1299 as amended). The member's deployment is opened as a Getter
// and nothing wider, so the write is not a rule a code path is trusted to keep:
// there is no method to call.
func TestWorkspaceScopeWritesNothingToAMember(t *testing.T) {
	s := newWSDeployment(t, nil)
	s.serve(wsMemberA, wsPoolName)
	svc := wsService(t, s)

	resp, body := svc.call(t, callBody("ws2-writes"), s.hdr(wsAccountA))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a served call = %d: %s", resp.StatusCode, body)
	}
	// The call's own state is the workspace's: the placement the cascade
	// recorded for this run is in the store the *Workspace was built with, and
	// it could only get there through the scope's State().
	run := creds.Run{Slug: wsSlug, ID: s.run}
	s.store.mu.Lock()
	placement, placed := s.store.current[runKey(run)]
	leases := len(s.store.leases)
	s.store.mu.Unlock()
	if !placed || placement != wsPoolName {
		t.Errorf("the workspace's own store holds %q for the run, want the placement on %q: a workspace call's state is the workspace's", placement, wsPoolName)
	}
	if leases == 0 {
		t.Error("the workspace's own store holds no lease: the call's state was written nowhere")
	}
	// And the member's database was read and never written. The handle has no
	// write on it at all; what this asserts is the other half — that nothing
	// this call read from the member is state, since the four readers it does
	// serve and the state store are different things.
	for _, path := range s.dbs[wsMemberA].paths {
		for _, state := range []string{"creds/marks", "creds/leases", "creds/current", "creds/tokens", "creds/roundrobin"} {
			if path == state || strings.HasPrefix(path, state+"/") {
				t.Errorf("the member's database was asked for %q, which is credential state: the call's state is the workspace's own store (R8)", path)
			}
		}
	}
	if !s.dbs[wsMemberA].read(projectpolicy.PathShared()) {
		t.Error("the member's own policy was not read: the served call's legs are that member's database's")
	}

	// The fence at the type level: the one writer-shaped reader this scope
	// hands out is creds' own per-repository record reader, and the two writes
	// creds.DB declares refuse rather than reach the member.
	ro := memberReadOnly{Getter: s.dbs[wsMemberA]}
	before := len(s.dbs[wsMemberA].paths)
	if err := ro.PutIfMatch(context.Background(), projectpolicy.PathShared(), "etag", map[string]any{"x": 1}); !errors.Is(err, errMemberReadOnly) {
		t.Errorf("PutIfMatch = %v, want the read-only refusal", err)
	}
	if err := ro.Patch(context.Background(), projectpolicy.PathShared(), map[string]any{"x": 1}); !errors.Is(err, errMemberReadOnly) {
		t.Errorf("Patch = %v, want the read-only refusal", err)
	}
	if got := len(s.dbs[wsMemberA].paths); got != before {
		t.Errorf("a refused write reached the member's database (%v)", s.dbs[wsMemberA].paths[before:])
	}
}

// ---------------------------------------------------------------------------
// R18, R14: what a call may carry, and which routes a workspace serves
// ---------------------------------------------------------------------------

// TestWorkspaceScopeRefusesACallThatPresentsASlip: a call to the workspace's
// service carries the job's identity and the run's token and nothing else of
// the member's — no slip id and no claim secret (R18, F40) — so a call that
// presents one is refused rather than served slip-less, and a call that
// presents none is served exactly as before.
func TestWorkspaceScopeRefusesACallThatPresentsASlip(t *testing.T) {
	s := newWSDeployment(t, nil)
	s.serve(wsMemberA, wsPoolName)
	sc := s.served(t, MessagesPath, wsAccountA)
	if sc.AcceptsSlip() {
		t.Error("AcceptsSlip() is true: a workspace's upstream carries no member secret, so no call of it presents a slip")
	}
	svc := wsService(t, s)

	// A call that carries neither header is the slip-less path and is served.
	if resp, body := svc.call(t, callBody("ws2-no-slip"), s.hdr(wsAccountA)); resp.StatusCode != http.StatusOK {
		t.Fatalf("a call with no slip = %d, want 200: %s", resp.StatusCode, body)
	}
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"a slip id", HeaderSlip},
		{"a claim secret", HeaderSlipClaim},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := svc.callPath(t, MessagesPath, callBody("ws2-slip"), func(r *http.Request) {
				s.hdr(wsAccountA)(r)
				r.Header.Set(tc.value, "an-id-the-member-s-service-would-know")
			})
			if resp.StatusCode != StatusUnverified {
				t.Errorf("a call presenting %s = %d, want %d", tc.name, resp.StatusCode, StatusUnverified)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal type = %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
			}
			if !strings.Contains(e.Message, "carries no slip") {
				t.Errorf("the refusal message %q does not say the service carries no slip", e.Message)
			}
		})
	}
}

// TestTheKeepWarmRouteCarriesNoSecret: the keep-warm route is one of the two
// routes of the workspace mode that answer nothing about a credential (R14,
// D2985), and it carries no secret: the beat takes the job's identity and
// presents neither a slip id nor a claim secret (F33, R18).
//
// **The scope's half is what is pinned here, and the front door's is not.**
// servedRoute (server.go) is the list of paths ServeHTTP admits at all, and its
// file is WS1's alone (the plan's shared-files table); this task's files do not
// include it. The gap that leaves — the path being answered 404 before the
// resolver is asked, and no handler existing for it — is recorded in this
// task's pull request rather than closed here.
func TestTheKeepWarmRouteCarriesNoSecret(t *testing.T) {
	const keepWarm = WorkspaceKeepWarmPath
	s := newWSDeployment(t, nil)
	sc := s.served(t, keepWarm, wsAccountA)
	if !sc.Serves(keepWarm) {
		t.Fatal("Serves is false for the keep-warm route: a beat posted there would be answered as a route this deployment does not have")
	}
	if sc.AcceptsSlip() {
		t.Error("AcceptsSlip() is true: the beat presents no slip, and a call that presents one is refused")
	}
	if !callerNamed(sc.Caller()) {
		t.Error("the scope names no caller: the beat takes the job's identity, and the operator's refusal line needs the identity that called")
	}
	// Resolving the beat reads no member's POLICY and no repository's
	// admission: the route answers nothing about a credential, and a beat that
	// read a member's policy to serve nothing would be a read the workspace's
	// pool pays for. What it does read is Verify's own liveness scan — the
	// member's outcomes, which is what every verified call reads before it is
	// verified at all (Verify, C1's rule 11) — and neither of the two is one of
	// those.
	for _, path := range s.dbs[wsMemberA].paths {
		if path == projectpolicy.PathShared() || path == projectpolicy.PathRepo(wsSlug) {
			t.Errorf("the beat read %q, and it serves nothing: the pool and the admissions are read by a route that answers a credential", path)
		}
	}
	if len(s.dbs[wsMemberB].paths) != 0 {
		t.Errorf("the beat read another member's database (%v), and it serves nothing", s.dbs[wsMemberB].paths)
	}
	// And a beat is a POST with no body this service reads.
	resp, _ := wsService(t, s).postRaw(t, http.MethodPost, keepWarm)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the front door answered the keep-warm route with %d, want the 404 the front door answers a path it does not admit: admitting it is the other half, and it is not this task's file (see this test's comment)", resp.StatusCode)
	}
}

// TestWorkspaceModeRefusesEveryOtherRoute: workspace mode serves a closed set of
// routes — the model routes, a run's own forge routes and the keep-warm route —
// and every other route is refused with a typed refusal, so that a member's run
// keeps calling its OWN project's service for them (R14, D2988, A2). The
// review routes are in that set deliberately: a review call is the member's own
// service's business, and a workspace that served it would offer the pool to a
// class of caller C1's fence does not describe (WS1's review note).
//
// The count of audit lines is part of the contract and not decoration: a route
// answered BEFORE the shared parse carries no run identity at all, so its
// refusal is the service's own and writes no line, while a route of the shared
// parse is refused after a verification and writes exactly one.
func TestWorkspaceModeRefusesEveryOtherRoute(t *testing.T) {
	s := newWSDeployment(t, nil)
	svc := wsService(t, s)

	// R14's served set, one of each: the model routes, the two forge routes a
	// run calls, the keep-warm route and the proxy in both of its methods.
	for _, served := range []string{MessagesPath, CountTokensPath, MintPath, ContextTokenPath, WorkspaceKeepWarmPath} {
		if !s.served(t, served, wsAccountA).Serves(served) {
			t.Errorf("Serves(%s) is false, and R14 serves it", served)
		}
	}
	proxy := s.request(t, BitbucketProxyPrefix+"owner/repo", wsAccountA)
	proxy.Method = http.MethodGet
	sc, err := s.res.Resolve(context.Background(), proxy)
	if err != nil {
		t.Fatalf("Resolve of the proxy: %v", err)
	}
	if !sc.Serves(proxy.URL.Path) {
		t.Error("the Bitbucket proxy is not served: it is one of the forge routes a run calls (R14)")
	}

	// Every route R14 leaves out, with the audit the refusal owes: the ones
	// answered ahead of the shared parse never verified a caller and write no
	// line, the others write one.
	for _, tc := range []struct {
		path  string
		audit bool
	}{
		{SignPath, true},
		{VerifyPath, false},
		{BitbucketReadPath, false},
		{GitHubReadPath, false},
		{MergeMintPath, false},
		{LabelPath, false},
		{IssuePath, false},
		{SlipPath, false},
		{SlipClaimPath, true},
		{WorkflowSecretsPath, true},
		{BuildTokenPath, false},
		{BuildCredentialsPath, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			svcBefore := len(lines(svc.audit))
			resp, body := svc.callPath(t, tc.path, "{}", s.hdr(wsAccountA))
			if resp.StatusCode != StatusUnverified {
				t.Errorf("%s = %d, want %d", tc.path, resp.StatusCode, StatusUnverified)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("%s refused with %q, want %q", tc.path, e.Error, ErrorUnverifiedJobIdentity)
			}
			if !strings.Contains(e.Message, "does not serve this route") {
				t.Errorf("the refusal message %q does not say the deployment does not serve the route", e.Message)
			}
			wrote := len(lines(svc.audit)) - svcBefore
			if tc.audit && wrote != 1 {
				t.Errorf("%s wrote %d audit lines, want 1: the route is one of the shared parse's, so a verified caller's refusal is audited", tc.path, wrote)
			}
			if !tc.audit && wrote != 0 {
				t.Errorf("%s wrote %d audit lines, want none: the route is answered before the shared parse and carried no identity to record", tc.path, wrote)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The constructor, the readers, and the meter
// ---------------------------------------------------------------------------

// TestNewWorkspaceScopeResolverRefusesAConfigurationItCannotCheck: every field
// is required for NewWorkspace's reason — a resolver that started without one
// answers every call with something, and none of those somethings is a call
// served under the workspace's rules.
func TestNewWorkspaceScopeResolverRefusesAConfigurationItCannotCheck(t *testing.T) {
	f := newWSFixture(t)
	node := wsPublished(wsOneRepo())
	for name, cfg := range map[string]WorkspaceScopeConfig{
		"no front door": {Node: node},
		"no node":       {Workspace: f.ws},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewWorkspaceScopeResolver(cfg); !errors.Is(err, ErrWorkspaceConfig) {
				t.Errorf("NewWorkspaceScopeResolver = %v, want ErrWorkspaceConfig", err)
			}
		})
	}
	// The resolver the tree ships is WS2's and not the scaffolding's: a stub
	// verifies no caller at all, so a job of a member this deployment holds
	// answering no scope is the stub's signature and not the real resolver's.
	res, err := NewWorkspaceScopeResolver(WorkspaceScopeConfig{Workspace: f.ws, Node: node, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatalf("NewWorkspaceScopeResolver: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "https://"+wsAudience+MessagesPath, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.jobToken(wsAccountA))
	req.Header.Set(HeaderRunIdentity, f.runToken(wsMemberA))
	req.Header.Set(HeaderRunJob, wsJob)
	sc, err := res.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve of a member's own job = %v, want a scope: the deployed constructor is the workspace's", err)
	}
	if sc.Caller().JobAccount != wsAccountA {
		t.Errorf("the scope's caller = %q, want the account that called", sc.Caller().JobAccount)
	}
}

// TestWorkspaceScopeWritesAndReadsUnderItsOwnReaders: the scope's readers are
// the ones C1 declares, and each is the member's own database — except the
// state, which is the workspace's, the stop switches, which are BOTH read
// together, and the offering, which is the intersection (C1's rule 2, rule 9,
// D1302).
func TestWorkspaceScopeAnswersItsReaders(t *testing.T) {
	s := newWSDeployment(t, nil)
	s.setPolicy(wsMemberA, wsPoolName)
	// The stop switches are the calling member's own pair AND the workspace's
	// own switch, read together: either one stops the call, and the two are
	// read where they are written — the member's switches in the member's own
	// database, and the workspace's in the workspace's own (D1302, spec.md §6).
	kill := budget.PathKillRepo(wsSlug)
	s.dbs[wsMemberA].nodes[kill] = map[string]any{"on": true, "by": "operator@project-a.example.com", "reason": "the member's own operator stopped it"}
	sc := s.served(t, MessagesPath, wsAccountA)
	on, reason, err := sc.Kills().Stop(context.Background(), wsSlug)
	if err != nil {
		t.Fatalf("Kills().Stop: %v", err)
	}
	if !on || reason == "" {
		t.Errorf("Kills().Stop = (%v, %q), want the member's own switch and its reason", on, reason)
	}
	if !s.dbs[wsMemberA].read(kill) {
		t.Errorf("the kill switch was not read from the calling member's database (read %v)", s.dbs[wsMemberA].paths)
	}
	if len(s.dbs[wsMemberB].paths) != 0 {
		t.Errorf("another member's database was read (%v): each member's switches are read from its own", s.dbs[wsMemberB].paths)
	}
	// And the workspace's own switch is read beside them, from the workspace's
	// own database: the pair above is a narrowing of a call that pays with the
	// workspace's credentials, and the workspace's stop is the one that reaches
	// every member at once.
	if !s.own.read(budget.PathKillGlobal) {
		t.Errorf("the workspace's own switch was not read (read %v): a workspace its owner has stopped would go on serving", s.own.paths)
	}
	if s.own.read(budget.PathKillRepo(wsSlug)) {
		t.Error("the workspace's database was read for a REPOSITORY's switch: a workspace's stop is its global one and never a repository's")
	}
	// The state is the workspace's own store and never the member's, which is
	// the one reader C1's rule 2 excepts (R8).
	if got := sc.State(); got != StateStore(s.store) {
		t.Errorf("State() = %v, want the workspace's own store and not the member's database", got)
	}
	// The per-repository auth record is the MEMBER's: it is a fact about that
	// member's repository, written by that member's own `fugaro update`.
	if _, _, err := sc.RepoAuth().RepoAuth(context.Background(), wsSlug); err != nil {
		t.Fatalf("RepoAuth: %v", err)
	}
	if !s.dbs[wsMemberA].read("creds/repoauth/" + wsSlug) {
		t.Error("the repository's own auth record was not read from the calling member's database")
	}
	// The forge reader is the account's repositories and no token store yet:
	// the store a proxied call needs is built over the workspace's bundle, and
	// handing one here would be a second read of that secret (C1's R-scope-forge
	// is where it is wired).
	if sc.Forge().Bitbucket() != nil {
		t.Error("Forge().Bitbucket() is not nil: the token store is built where the bundle is loaded, and this resolver holds no bundle")
	}
	covered, err := sc.Forge().Covers(context.Background(), wsRepo)
	if err != nil {
		t.Fatalf("Covers: %v", err)
	}
	if !covered {
		t.Errorf("Covers(%q) is false: the forge fence is the repositories the calling account is declared on", wsRepo)
	}
	if covered, err := sc.Forge().Covers(context.Background(), wsRepoOther); err != nil || covered {
		t.Errorf("Covers(%q) = (%v, %v), want false: the account is not declared on it", wsRepoOther, covered, err)
	}
}

// TestAWorkspaceKillRefusesAServedMemberCall is D1302's refusal, and the one
// control of a workspace that does not depend on a member's honesty (spec.md
// §8): `fugaro budget kill --workspace <name>` writes the workspace's own
// global switch, and the workspace's service refuses every shared-credential
// call on it. The call below is a member's own served call — 200 with the
// switch off, and the gate's stop refusal with it on — and the switch is in a
// database no member can write, which is why no member's operator can undo it.
//
// The member's own pair is read beside it and neither stop contains the other:
// the middle of this test is the member's own switch stopping the same call,
// which is a narrowing of that member's own runs rather than a second way to
// stop the workspace.
func TestAWorkspaceKillRefusesAServedMemberCall(t *testing.T) {
	s := newWSDeployment(t, nil)
	s.serve(wsMemberA, wsPoolName)
	svc := wsService(t, s)

	// The control: with neither switch on, the call is served.
	resp, body := svc.call(t, callBody("ws2-unstopped"), s.hdr(wsAccountA))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call with both switches off = %d, want 200: %s", resp.StatusCode, body)
	}
	if got := lines(svc.audit); len(got) != 1 {
		t.Errorf("the served call wrote %d audit lines, want one (a served call of this service writes one)", len(got))
	}

	// The member's own operator stops its own runs: the pair in the member's
	// own database, which the member's own service reads too.
	memberSwitch := budget.PathKillRepo(wsSlug)
	s.dbs[wsMemberA].nodes[memberSwitch] = budget.Kill{On: true, By: "operator@project-a.example.com", Reason: "runaway spend"}
	resp, body = svc.callPath(t, MessagesPath, callBody("ws2-member-killed"), s.hdr(wsAccountA))
	if resp.StatusCode != StatusUnavailable {
		t.Errorf("the call under the member's own switch = %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable || !strings.Contains(e.Message, "kill switch") {
		t.Errorf("the refusal = %+v, want the stop's own refusal of the call", e)
	}
	delete(s.dbs[wsMemberA].nodes, memberSwitch)

	// The workspace's own stop, and it is the half this test exists for: the
	// switch `fugaro budget kill --workspace` writes, read from the workspace's
	// own database over the members' heads.
	s.own.nodes[budget.PathKillGlobal] = budget.Kill{On: true, By: "owner@workspace.example.com", Reason: "runaway spend"}
	ownBefore, memberBefore := len(s.own.paths), len(s.dbs[wsMemberA].paths)
	resp, body = svc.callPath(t, MessagesPath, callBody("ws2-workspace-killed"), s.hdr(wsAccountA))
	if resp.StatusCode != StatusUnavailable {
		t.Errorf("the call under the workspace's own switch = %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal type = %q, want %q: a stop is the service's own refusal and not a verdict about the caller", e.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(e.Message, workspaceKillWord+" kill switch") {
		t.Errorf("the refusal %q does not name the workspace's own switch: the member's operator holds no grant on the database that switch is in, and the run's halt has to say which stop it met", e.Message)
	}
	read := false
	for _, path := range s.own.paths[ownBefore:] {
		if path == budget.PathKillGlobal {
			read = true
		}
	}
	if !read {
		t.Errorf("the workspace's own switch was not read (the workspace's database was asked for %v)", s.own.paths[ownBefore:])
	}
	// The member's own STOP SWITCHES are not read for the workspace's stop: the
	// refusal is answered before the member's pair is asked, so a stopped
	// workspace reads nothing of a member's for it — and writes nothing of a
	// member's at all (spec.md §9). What the member's database is read for in
	// this call is Verify's own liveness scan, which every verified call reads.
	for _, path := range s.dbs[wsMemberA].paths[memberBefore:] {
		if path == budget.PathKillGlobal || path == budget.PathKillRepo(wsSlug) {
			t.Errorf("the member's %q was read for the workspace's stop: a stopped workspace reads no member's switch", path)
		}
	}
	// And the refusal is an audited one: the three calls wrote three lines,
	// one each, because the caller was verified before any switch was read.
	if got := lines(svc.audit); len(got) != 3 {
		t.Errorf("the three calls wrote %d audit lines, want one each", len(got))
	}
}

// TestWorkspaceScopeAnswersItsMeter: the meter is the one accessor whose zero
// answer does not refuse (C1's rule 9), so a capped workspace must answer its
// cap here and never nil — a resolver that answered nil would not refuse a call,
// it would pay for every one of them.
func TestWorkspaceScopeAnswersItsMeter(t *testing.T) {
	_, client := wsMeterDB(t)
	meter := wsMeter(t, client)
	s := newWSDeployment(t, nil, func(cfg *WorkspaceScopeConfig) { cfg.Meter = meter })
	if got := s.served(t, MessagesPath, wsAccountA).Meter(); got != meter {
		t.Errorf("Meter() = %v, want the workspace's own cap", got)
	}
	// A route the workspace does not serve is answered with a scope too, and it
	// answers the same cap: the meter is the resolver's and not a call's.
	if got := s.served(t, WorkspaceKeepWarmPath, wsAccountA).Meter(); got != meter {
		t.Errorf("Meter() of a route the deployment does not serve = %v, want the workspace's own cap", got)
	}
	// A workspace with no cap answers nil, which is the same answer an
	// installation's does and means no cap rather than a refused call.
	plain := newWSDeployment(t, nil)
	if got := plain.served(t, MessagesPath, wsAccountA).Meter(); got != nil {
		t.Errorf("Meter() = %v, want nil for a workspace that declared no cap", got)
	}
}

// TestWorkspaceScopeRefusesEveryReaderOfAnUnservedCall: the scope of a route
// the deployment does not serve answers no reader at all — each one refuses
// rather than answering an empty value, because "this database is not this
// call's" and "this policy admits nothing" are different statements and a
// reader that answered the second for the first would be one a route could act
// on.
func TestWorkspaceScopeRefusesEveryReaderOfAnUnservedCall(t *testing.T) {
	s := newWSDeployment(t, nil)
	// The sign route: a route of the shared parse, so the caller IS verified —
	// which is what makes the gate's refusal of it an audited one.
	sc, err := s.resolve(t, SignPath, wsAccountA)
	if err != nil {
		t.Fatalf("Resolve of a route the workspace does not serve = %v, want a scope whose Serves is false", err)
	}
	if sc.Serves(SignPath) {
		t.Fatal("the scope serves the review signing route: R14 leaves it to the member's own service")
	}
	// The build half of the git proxy is the case a path predicate gets wrong:
	// it is answered ahead of the shared parse, so the scope it is handed is the
	// UNSERVED one — while the proxy's own prefix IS in R14's set, which is what
	// the gate reads through Serves before dispatching a handler. An unserved
	// scope that answered the predicate here would send the call to the build
	// proxy, which reads the INSTALLATION's own configuration and bundle around
	// the scope this deployment exists to put in front of it (server.go's
	// buildProxy branch), and the route would be served where it must not be.
	build := s.request(t, BitbucketProxyPrefix+wsRepo+".git/git-receive-pack", wsAccountA)
	build.Header.Set(HeaderBuild, "ws2-build")
	buildScope, err := s.res.Resolve(context.Background(), build)
	if err != nil {
		t.Fatalf("Resolve of a build's proxy call = %v, want the unserved scope", err)
	}
	if buildScope.Serves(build.URL.Path) {
		t.Fatal("the unserved scope serves the build half of the git proxy: the call would be dispatched to the build proxy, which reads the installation's own configuration around this scope")
	}
	if callerNamed(buildScope.Caller()) {
		t.Errorf("the unserved scope of a build's proxy call names a caller (%+v), and a build's call holds no run identity", buildScope.Caller())
	}
	// And the front door refuses such a call rather than dispatching it to the
	// build proxy: the refusal is the gate's own, and because a build's call
	// holds no run identity there was no verification to record, so it writes
	// no audit line at all.
	svc := wsService(t, s)
	resp, body := svc.postRaw(t, http.MethodPost, BitbucketProxyPrefix+wsRepo+".git/git-receive-pack", func(r *http.Request) {
		r.Header.Set(HeaderBuild, "ws2-build")
	})
	if resp.StatusCode != StatusUnverified {
		t.Errorf("a build's proxy call = %d, want %d, the gate's refusal of a route this deployment does not serve", resp.StatusCode, StatusUnverified)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("a build's proxy call refused with %q, want %q: the route is one this deployment does not serve", e.Error, ErrorUnverifiedJobIdentity)
	}
	if got := lines(svc.audit); len(got) != 0 {
		t.Errorf("a build's proxy call wrote %d audit lines, want none: it holds no run identity, so no verification happened to record", len(got))
	}
	ctx := context.Background()
	if _, _, err := sc.Policy().Shared(ctx); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Policy().Shared = %v, want the unserved refusal", err)
	}
	if _, _, err := sc.Kills().Stop(ctx, wsSlug); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Kills().Stop = %v, want the unserved refusal", err)
	}
	if _, err := sc.State().Marks(ctx); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("State().Marks = %v, want the unserved refusal", err)
	}
	if _, _, err := sc.RepoAuth().RepoAuth(ctx, wsSlug); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("RepoAuth = %v, want the unserved refusal", err)
	}
	if _, err := sc.Offering().Offering(ctx, wsPool(t)); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Offering = %v, want the unserved refusal", err)
	}
	if _, err := sc.Forge().Covers(ctx, wsRepo); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("Forge().Covers = %v, want the unserved refusal", err)
	}
	for _, path := range s.dbs[wsMemberA].paths {
		if path == projectpolicy.PathShared() || path == projectpolicy.PathRepo(wsSlug) {
			t.Errorf("an unserved call read %q: the readers behind a route this deployment does not serve answer nothing at all", path)
		}
	}
	// The scope still names the caller: the gate's refusal of a route it does
	// not serve is written with the identity and the purpose where a
	// verification happened.
	if sc.Caller().JobAccount != wsAccountA {
		t.Errorf("the unserved scope's caller = %+v, want the verified one", sc.Caller())
	}
	// A route answered before the shared parse names none at all: no identity
	// was ever verified, and the refusal writes no audit line.
	sc, err = s.resolve(t, VerifyPath, wsAccountA)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if callerNamed(sc.Caller()) {
		t.Errorf("the scope of a route answered ahead of the shared parse names a caller (%+v)", sc.Caller())
	}
}
