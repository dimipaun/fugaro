package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// TestPersonScopeIsImplementedByTheWorkspacesScopeAlone pins C2's acceptance
// sentence at its strongest reading — `PersonScope` "is implemented by the
// workspace's scope and by no other" — which is the whole of why a route of the
// personal half can be typed at all: a second implementation would be a second
// deployment that serves a person's token, and the plan's A3 rule says an
// installation's service behaves exactly as it does today.
//
// It is a source walk rather than a type assertion because a compile-time
// assertion only ever proves an implementation exists. Every non-test file of
// this package is read and every method of the four the interface declares is
// attributed to its receiver, so a type that declares any one of them is found
// wherever it sits — and the walk's own two assertions (the one implementation,
// and all four methods on it) fail together if the interface is ever retyped
// without the type beside it.
//
// What it does not reach is a type outside this package: a scope is this
// package's `Scope`, a struct's methods cannot be added from another package,
// and C2 declares `PersonScope` here, so an implementation elsewhere could only
// be one nothing could ever hand out as a scope.
func TestPersonScopeIsImplementedByTheWorkspacesScopeAlone(t *testing.T) {
	const alone = "workspacePersonScope"
	methods := []string{
		"Launchers() LauncherVerifier",
		"Slips() creds.SlipStore",
		"OwnsSlip(ctx context.Context, id string) (bool, error)",
		"Personal() PersonalReader",
	}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package's files: %v", err)
	}
	// decls is each interface method, and the receivers that declare it.
	decls := map[string][]string{}
	read := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		read++
		for _, line := range strings.Split(string(data), "\n") {
			code := line
			if i := strings.Index(code, "//"); i >= 0 {
				code = code[:i]
			}
			code = strings.TrimSpace(code)
			if !strings.HasPrefix(code, "func (") {
				continue
			}
			for _, m := range methods {
				if !strings.Contains(code, ") "+m+" {") {
					continue
				}
				recv := code[len("func ("):strings.Index(code, ") "+m)]
				decls[m] = append(decls[m], strings.TrimSpace(recv))
			}
		}
	}
	if read < 2 {
		t.Fatalf("the walk read %d non-test files of this package, and the scope it guards lives in one of them: a walk that read nothing would pass every assertion below", read)
	}
	for _, m := range methods {
		got := decls[m]
		if len(got) != 1 || !strings.Contains(got[0], alone) {
			t.Errorf("%s is declared by %v, want exactly the workspace's own scope (%s): C2's rule is that PersonScope is implemented by the workspace's scope and by no other, and a second type declaring one of its four methods is a second deployment that would serve a person's token", m, got, alone)
		}
	}
}

// wsTokens is the snapshot's bundle as a store's own test answers it: the
// document keys the workspace's bundle holds and, behind the accessor, their
// values. It is C1's BundleTokens without a document — which is what lets the
// case above ask WHICH name a store chooses without a secret being written
// anywhere, and the values it is handed are the fixture's minted ones.
type wsTokens map[string]string

func (t wsTokens) Names() []bbtokens.Name {
	out := make([]bbtokens.Name, 0, len(t))
	for name := range t {
		out = append(out, bbtokens.Name(name))
	}
	return out
}

func (t wsTokens) Value(name bbtokens.Name) (string, bool) {
	value, ok := t[string(name)]
	return value, ok
}

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

// ws2Scope is a workspace's deployment under test: the frozen *Workspace, the
// published node and the resolver built from them, plus the fixture the members'
// databases live in.
type ws2Scope struct {
	*wsFixture
	node *wsNode
	res  ScopeResolver
}

func newWS2Scope(t *testing.T, node *wsNode, mutate ...func(*WorkspaceScopeConfig)) *ws2Scope {
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
	return &ws2Scope{wsFixture: f, node: node, res: res}
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
func (s *ws2Scope) request(t *testing.T, path, account string, mutate ...func(map[string]any)) *http.Request {
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
func (s *ws2Scope) hdr(account string, mutate ...func(map[string]any)) func(*http.Request) {
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
func (s *ws2Scope) resolve(t *testing.T, path, account string, mutate ...func(map[string]any)) (Scope, error) {
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
func (s *ws2Scope) served(t *testing.T, path, account string, mutate ...func(map[string]any)) Scope {
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
func (s *ws2Scope) allowModels(member string, models ...string) {
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
func (s *ws2Scope) serve(member string, accounts ...string) {
	s.t.Helper()
	s.setPolicy(member, accounts...)
	s.allowModels(member, testModel)
}

// serveOn is setPolicyOn + allowModels, for the tests that need the member's
// own lists to name more than the one repository.
func (s *ws2Scope) serveOn(member string, repos []string, accounts ...string) {
	s.t.Helper()
	s.setPolicyOn(member, repos, accounts...)
	s.allowModels(member, testModel)
}

// wsService is a workspace-mode PROCESS: the installation's own front door,
// gate and handlers (server.go is one code path for both deployments) with the
// workspace's resolver handed in, which is the whole of what the deployment
// differs by.
func wsService(t *testing.T, s *ws2Scope) *svc {
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
	s := newWS2Scope(t, nil)
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
	plain := newWS2Scope(t, empty)
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
	s := newWS2Scope(t, nil)
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
	s := newWS2Scope(t, nil)
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
	cases := map[string]func(*ws2Scope) (*http.Request, error){
		"no identity at all": func(s *ws2Scope) (*http.Request, error) {
			return http.NewRequest(http.MethodPost, "https://"+wsAudience+MessagesPath, strings.NewReader("{}"))
		},
		"a token signed by somebody else": func(s *ws2Scope) (*http.Request, error) {
			req := s.request(t, MessagesPath, wsAccountA)
			req.Header.Set(HeaderRunIdentity, "not-a-token-this-deployment-can-read-at-all")
			return req, nil
		},
		"an account outside the workspace": func(s *ws2Scope) (*http.Request, error) {
			return s.request(t, MessagesPath, wsAccountOut), nil
		},
		"a run identity of another member": func(s *ws2Scope) (*http.Request, error) {
			return s.request(t, MessagesPath, wsAccountA, func(c map[string]any) {
				c["iss"] = "https://securetoken.google.com/" + wsMemberB
				c["aud"] = wsMemberB
				c["fp"] = wsMemberB
			}), nil
		},
		"a member the node does not name": func(s *ws2Scope) (*http.Request, error) {
			node := wsOneRepo()
			node.Members = node.Members[1:]
			s.node.publish(node)
			return s.request(t, MessagesPath, wsAccountA), nil
		},
		"a node that cannot be read": func(s *ws2Scope) (*http.Request, error) {
			s.node.fail(errors.New("the database is unavailable"))
			return s.request(t, MessagesPath, wsAccountA), nil
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			s := newWS2Scope(t, nil)
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
	s := newWS2Scope(t, wsPublished(node))
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
	s = newWS2Scope(t, wsPublished(node))
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
	s := newWS2Scope(t, nil)
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
	s = newWS2Scope(t, wsPublished(node))
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
	s = newWS2Scope(t, wsPublished(wsNodeFor(nil, nil, nil)))
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
	s := newWS2Scope(t, wsPublished(node))
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
	s := newWS2Scope(t, nil)
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
	unlisted := newWS2Scope(t, wsPublished(wsNodeFor(nil, nil, nil)))
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
	s := newWS2Scope(t, nil)
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

// TestWorkspaceScopeRefusesAMembersSlip: a call to the workspace's service
// carries the job's identity and the run's token and nothing else of the
// member's — no slip id and no claim secret (R18, F40) — and now that the
// workspace has a slip of its OWN (WC4) the fence has two sides: a workspace
// with no roster wired accepts no slip at all and refuses a member's, and one
// with the roster wired reads only its own two headers, so a member's slip is
// refused there too rather than read as the workspace's (F13 — each slip is
// presented to the service that granted it).
//
// The two refusals are the same status and the same type on purpose: what tells
// them apart is the sentence, and neither names the slip or where it came from.
func TestWorkspaceScopeRefusesAMembersSlip(t *testing.T) {
	s := newWS2Scope(t, nil)
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

	// The other side: the same call to a deployment that DOES read the
	// workspace's own slip. The member's headers are not the workspace's, so
	// the call is refused — and it is refused rather than served slip-less,
	// because a run holding a slip means to use the credential that slip names
	// (D2974) and this service does not serve it.
	p := newWSPerson(t)
	personSvc := p.service(t)
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"a slip id", HeaderSlip},
		{"a claim secret", HeaderSlipClaim},
	} {
		t.Run("the workspace reads its own slip: "+tc.name, func(t *testing.T) {
			resp, body := personSvc.callPath(t, MessagesPath, callBody("ws4-members-slip"), func(r *http.Request) {
				p.hdr(wsAccountA)(r)
				r.Header.Set(tc.value, "an-id-the-workspace-s-service-would-know")
			})
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("a call presenting the member's %s = %d, want %d: %s", tc.name, resp.StatusCode, StatusUnverified, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal type = %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
			}
			if !strings.Contains(e.Message, "no member's") {
				t.Errorf("the refusal's sentence is %q, want the one that says the workspace's service reads the workspace's own slip", e.Message)
			}
			if len(personSvc.up.Seen()) != 0 {
				t.Errorf("the upstream saw %d calls, want none: the call was refused before any handler", len(personSvc.up.Seen()))
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
	s := newWS2Scope(t, nil)
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
	s := newWS2Scope(t, nil)
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
	s := newWS2Scope(t, nil)
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
	// The forge reader's token store is the workspace's OWN DECLARATION: the
	// Bitbucket tokens the owner's node names, chosen for the repository the
	// call is on, and read for a value by the route out of the snapshot it
	// decided the call on (C1's rule 10). The fixture's node declares no
	// Bitbucket token, so this one covers nothing — and a store that answered a
	// name it cannot forward with would be the fallback C1 refuses.
	store := sc.Forge().Bitbucket()
	if store == nil {
		t.Fatal("Forge().Bitbucket() is nil, and nil refuses every proxied call: a workspace answers the tokens its own node declares (C1's rule 10)")
	}
	repo, other := testCanonical(t, wsMemberRepo), testCanonical(t, wsMemberOtherRepo)
	var refusal *bbtokens.Refusal
	if _, err := store.Select(context.Background(), bbtokens.Snapshot{}, repo, false); !errors.As(err, &refusal) || refusal.Class != bbtokens.ClassNotCovered {
		t.Errorf("the store of a node declaring no Bitbucket token answered %v, want the coverage refusal", err)
	}
	// The other half of the same claim, on a node that declares one: the token
	// is chosen by the NAME the owner's node carries, and the value stays the
	// route's to read — a snapshot holding that name is answered, and one
	// holding no token at all is refused rather than served under a name.
	declared := wsOneRepo()
	declared.Shared = append(declared.Shared, projectpolicy.WorkspaceShared{
		Name: config.BitbucketSecret, Kind: gitprov.KindBitbucket,
		Projects: []string{wsMemberA, wsMemberB}, Covers: []string{repo},
	})
	declaredStore := newWS2Scope(t, wsPublished(declared)).served(t, MessagesPath, wsAccountA).Forge().Bitbucket()
	held := bbtokens.Snapshot{Bundle: wsTokens{config.BitbucketSecret: fakeCred(t, "workspace-token")}}
	choice, err := declaredStore.Select(context.Background(), held, repo, false)
	if err != nil || choice.Name != bbtokens.Name(config.BitbucketSecret) {
		t.Errorf("Select of a declared and held token = (%v, %v), want the name the workspace's own node carries", choice, err)
	}
	if _, err := declaredStore.Select(context.Background(), bbtokens.Snapshot{}, repo, false); !errors.As(err, &refusal) || refusal.Class != bbtokens.ClassNoToken {
		t.Errorf("Select of a declared token the bundle does not hold = %v, want the store's own refusal: a name nothing can forward with is never chosen (D143)", err)
	}
	if _, err := declaredStore.Select(context.Background(), held, other, false); !errors.As(err, &refusal) || refusal.Class != bbtokens.ClassNotCovered {
		t.Errorf("Select of a repository the owner's coverage does not name = %v, want the coverage refusal (C3's covers)", err)
	}
	// And the allow-list is a fence on this store too, which is R5's first leg
	// and not the model route's alone: the same entry offered to the OTHER
	// member alone is one this call is not served, however well the owner's
	// coverage covers the repository — the store holds no entry for it, so
	// Select falls through to the coverage refusal rather than forwarding the
	// workspace's token on a project the owner offered it to nobody (D1298).
	unoffered := wsOneRepo()
	unoffered.Shared = append(unoffered.Shared, projectpolicy.WorkspaceShared{
		Name: config.BitbucketSecret, Kind: gitprov.KindBitbucket,
		Projects: []string{wsMemberB}, Covers: []string{repo},
	})
	unofferedStore := newWS2Scope(t, wsPublished(unoffered)).served(t, MessagesPath, wsAccountA).Forge().Bitbucket()
	if _, err := unofferedStore.Select(context.Background(), held, repo, false); !errors.As(err, &refusal) || refusal.Class != bbtokens.ClassNotCovered {
		t.Errorf("Select of a covered repository whose entry the allow-list names another member alone = %v, want the coverage refusal: the entry's projects is asked beside the member's own policy (R5, D1298)", err)
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
	s := newWS2Scope(t, nil)
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
	s := newWS2Scope(t, nil, func(cfg *WorkspaceScopeConfig) { cfg.Meter = meter })
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
	plain := newWS2Scope(t, nil)
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
	s := newWS2Scope(t, nil)
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

// ---------------------------------------------------------------------------
// WC4: the workspace's own grant and claim (R3, R4, R8, D2970, D3147)
// ---------------------------------------------------------------------------

// The workspace's OWN half wired for one deployment: the roster the grant is
// admitted against (C2's RosterReader), the launcher class (C2's
// LauncherVerifier) and the workspace's own slip store — over a database of its
// own, so a test can tell the workspace's record from an installation's.
//
// Everything here is the fixture's: the roster is the real
// internal/workspace document, encoded and decoded through that package's own
// reader, and the slip store is creds.RTDBStore over internal/gcpfake, so the
// claim's compare-and-set and the grant's deadline are exercised rather than
// faked.

const (
	// wsSub is the Google account id the fixture's launcher class answers. It
	// is digits only and of the width a real `sub` has, because that is the
	// shape the launcher class and secretname.User both hold it to — and it is
	// nobody's: the container it names is the fixture's own.
	wsSub = "117843201122334455667"
	// wsOtherSub is ANOTHER person's account id: the owner of a personal
	// container the fixture's launcher must never be able to name (the owner
	// rule read at the grant, R7).
	wsOtherSub = "104729113355608719402"
	// wsLauncherBearer is the bearer the fixture's class reads. It is no
	// credential: the class it stands in for verifies a Google ID token against
	// Google's keys, and what a test of THIS file needs is the pair the class
	// answers (the account and the id), which is what C2's seam hands on.
	wsLauncherBearer = "a-launcher-token"
	// wsStrangerBearer is a token the class admits nobody by: a caller that is
	// not a launcher of the workspace at all — a service account of a member's
	// project asking over the workspace's money, which is the class R3 refuses.
	wsStrangerBearer = "a-service-account-of-a-member"
)

// wsLaunchers is the workspace's launcher class as the tests hold it: C2's
// LauncherVerifier and nothing else. A token the test wrote answers the caller
// it was mapped to; every other token is refused with ErrUnverifiedJob, which
// is the class's own refusal and the one that makes a service account's grant
// impossible rather than refused a second time.
type wsLaunchers struct {
	mu      sync.Mutex
	callers map[string]LauncherCaller
	err     error
	calls   int
}

func (l *wsLaunchers) VerifyLauncher(_ context.Context, tok string) (LauncherCaller, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.err != nil {
		return LauncherCaller{}, l.err
	}
	c, ok := l.callers[tok]
	if !ok {
		return LauncherCaller{}, fmt.Errorf("%w: this token is not one of the workspace's launchers", ErrUnverifiedJob)
	}
	return c, nil
}

// wsRoster is the workspace's roster node as the tests hold it: C2's
// RosterReader over the real document. It counts its reads, because R3's rule
// is about WHEN the roster is read — "no token held" is answered before it, and
// it is re-read as the node is (workspaceNodeTTL).
type wsRoster struct {
	mu    sync.Mutex
	raw   json.RawMessage
	found bool
	err   error
	reads int
}

func (r *wsRoster) Read(context.Context) (workspace.RosterNode, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.err != nil {
		return workspace.RosterNode{}, false, r.err
	}
	if !r.found {
		return workspace.RosterNode{}, false, nil
	}
	return workspace.RosterNode{Raw: r.raw, ETag: "an-etag"}, true, nil
}

// readsSoFar is how many times the roster has been read.
func (r *wsRoster) readsSoFar() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

// publish is the workspace owner's `fugaro workspace ...` landing a new roster.
func (r *wsRoster) publish(t *testing.T, raw json.RawMessage) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raw, r.found, r.err = raw, true, nil
}

// fail is a roster the workspace's database cannot be read for (D2983).
func (r *wsRoster) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// wsHuman and wsMachine are one entry of the roster's people, in the shape
// internal/workspace's own document holds them.
func wsHuman(address string) workspace.Person {
	return workspace.Person{Address: address, Kind: workspace.KindHuman}
}

// wsMachine is a machine of the roster, which is never a launcher of the
// workspace's own grant (R3's class is people).
func wsMachine(address string) workspace.Person {
	return workspace.Person{Address: address, Kind: workspace.KindSA}
}

// wsRosterRaw builds the roster the workspace's own store would hold: the
// people, the admins (the contract refuses a workspace nobody administers) and
// D2818's mark — the grants the workspace wrote into its members, which is
// where "the members this person is a launcher of" is read from (D3145
// reopened).
func wsRosterRaw(t *testing.T, people []workspace.Person, admin string, grants ...workspace.StoredGrant) json.RawMessage {
	t.Helper()
	raw, err := workspace.EncodeRoster(&workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismFolder,
		FolderID:  "folders/123456789012",
		People:    people,
		Admins:    []string{admin},
		Grants:    grants,
	})
	if err != nil {
		t.Fatalf("the fixture's roster is not one internal/workspace writes: %v", err)
	}
	return raw
}

// wsRosterWithOwner is the roster a fixture starts from: the owner as a human
// and an admin, and a grant recording that the owner launches for member A.
func wsRosterWithOwner(t *testing.T) json.RawMessage {
	t.Helper()
	return wsRosterRaw(t, []workspace.Person{wsHuman(wsOwner)}, wsOwner,
		workspace.StoredGrant{Project: wsMemberA, User: wsOwner, Role: workspace.RoleViewer})
}

// wsPerSecret is the per-secret source a deployment reads a person's container
// with (PerSecretSource). found=false is "no container, or one with no version"
// — R6's fall, and the answer the grant turns into `held:false` — and err is a
// read that failed, which must refuse rather than answer "not held" (D143).
type wsPerSecret struct {
	mu    sync.Mutex
	doc   []byte
	held  map[string]string
	err   error
	reads []string
}

func newWSPerSecret() *wsPerSecret {
	return &wsPerSecret{held: map[string]string{}}
}

// Latest is the bundle source's own read. **This fixture is both sources on
// purpose**: perSecrets asks the loader's source whether it reads containers
// itself, and the one type that does both is a fixture's (dynamicsecrets.go) —
// a deployment's Secret Manager source is one type per half. So the workspace's
// pool document is read from here and the person's container from Value.
func (p *wsPerSecret) Latest(context.Context) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.doc == nil {
		return nil, errors.New("the fixture's per-secret source holds no bundle document")
	}
	return p.doc, nil
}

func (p *wsPerSecret) Value(_ context.Context, id string) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads = append(p.reads, id)
	if p.err != nil {
		return "", false, p.err
	}
	v, ok := p.held[id]
	return v, ok, nil
}

// fail is a container read that did not answer (D143: the grant must refuse
// rather than answer "not held", or a run would go out on the shared paid
// entries while the person's own token sat there).
func (p *wsPerSecret) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

// readNames is the containers this source was asked about, which is what a test
// asserts the OWNER rule with: the grant reads the caller's own container by its
// exact name and nothing else.
func (p *wsPerSecret) readNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.reads...)
}

// wsPerson is one workspace's deployment with its own half wired.
type wsPerson struct {
	*ws2Scope
	db        *gcpfake.RTDB
	store     *creds.RTDBStore
	roster    *wsRoster
	launchers *wsLaunchers
	// secrets is the deployment's per-secret source, holding the caller's own
	// container by default: a fixture whose person holds nothing would answer
	// "no token held" on every grant and never reach the rules the tests are
	// about. A test that wants the empty state empties it, and one that wants a
	// failed read fails it.
	secrets *wsPerSecret
	// switchOn is the process's switch, which the fixture installs ON and puts
	// back when the test ends (newWSPerson says why ON): a test that asserts the
	// member the switch was asked about reads it here.
	switchOn *scriptedSwitch
}

// newWSPerson wires C2's configuration, builds the resolver over it and serves
// the member's own policy — which is what makes a call under a slip reach the
// model path rather than refusing for a missing allow-list.
//
// **The member's switch is ON, and that is this fixture's premise and not a
// convenience.** A slip exists only because the switch was on when it was
// granted (R11), so a fixture that grants a slip naming a personal token and
// calls under it is a member whose switch is on: with the process's own switch
// left at its default — C1's stub, OFF for every project — every call of this
// file that expects 200 would be refused with personalSwitchOff instead. The
// real reader is the deployment's (`projectpolicy.PersonSwitchReader` over the
// member's published policy, WC6) and the workspace-mode serve path installs it
// (I1, and WC5's comment on the variable says so); a hermetic fixture has no
// member's policy node to read it from, so it stands in for that wiring here the
// way the assignment below stands in for WC14's.
func newWSPerson(t *testing.T, mutate ...func(*PersonScopeConfig)) *wsPerson {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	store := slipStore(t, db)
	roster := &wsRoster{}
	roster.publish(t, wsRosterWithOwner(t))
	launchers := &wsLaunchers{callers: map[string]LauncherCaller{
		wsLauncherBearer: {Account: wsOwner, Sub: wsSub},
	}}
	cfg := PersonScopeConfig{Roster: roster, Launchers: launchers, Slips: store}
	for _, m := range mutate {
		m(&cfg)
	}
	own, err := secretname.User(wsSub, creds.PersonTokenName)
	if err != nil {
		t.Fatalf("building the fixture's own container name: %v", err)
	}
	secrets := &wsPerSecret{doc: wsPoolDoc(t), held: map[string]string{own: fakeCred(t, "personal")}}
	was := PersonScopeConfigForServe
	PersonScopeConfigForServe = cfg
	t.Cleanup(func() { PersonScopeConfigForServe = was })
	sw := personalSwitch(t, true, nil)
	s := newWS2Scope(t, nil)
	s.serve(wsMemberA, wsPoolName)
	return &wsPerson{ws2Scope: s, db: db, store: store, roster: roster, launchers: launchers, secrets: secrets, switchOn: sw}
}

// service is wsService with the deployment's own configuration still open to the
// test: a source that reads a person's container, or none at all.
func (p *wsPerson) service(t *testing.T, mutate ...func(*svc, *Config)) *svc {
	t.Helper()
	return newSvcWith(t, func(svc *svc, cfg *Config) {
		cfg.Resolver = p.res
		svc.src.docs = [][]byte{wsPoolDoc(t)}
		// The workspace's pool document is read through the per-secret source,
		// which is what perSecrets asks (see wsPerSecret.Latest).
		cfg.Bundles.Source = p.secrets
		for _, m := range mutate {
			m(svc, cfg)
		}
	})
}

// launcher is the grant route's own caller: the bearer and NO run identity —
// the run it is asking for does not exist yet — which is the shape the route
// refuses by name.
func (p *wsPerson) launcher(bearer string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Del(HeaderRunIdentity)
		r.Header.Del(HeaderRunJob)
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
}

// grantBody is one grant request, in C1's own shape (creds.PersonGrantRequest):
// the four fields and nothing else.
func (p *wsPerson) grantBody(t *testing.T, account, name string) string {
	t.Helper()
	raw, err := json.Marshal(creds.PersonGrantRequest{Slug: wsSlug, Run: p.run, Account: account, Name: name})
	if err != nil {
		t.Fatalf("building the grant's request: %v", err)
	}
	return string(raw)
}

// grant asks for a workspace slip, with the launcher the test names.
func (p *wsPerson) grant(t *testing.T, svc *svc, bearer, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	head := p.launcher(bearer)
	return svc.callPath(t, SlipPath, body, append([]func(*http.Request){head}, mutate...)...)
}

// granted asks for a slip and answers the id the workspace wrote, failing the
// test on any refusal.
func (p *wsPerson) granted(t *testing.T, svc *svc, body string) string {
	t.Helper()
	resp, b := p.grant(t, svc, wsLauncherBearer, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the grant is answered %d, want 200: %s", resp.StatusCode, b)
	}
	var ans creds.PersonGrant
	if err := json.Unmarshal(b, &ans); err != nil {
		t.Fatalf("the grant's answer is not one C1 reads: %v", err)
	}
	if !ans.Held || ans.Slip == "" {
		t.Fatalf("the grant answered held=%v slip=%q, want a slip: the fixture's launcher holds a token", ans.Held, ans.Slip)
	}
	return ans.Slip
}

// claim claims a slip as the run it names, and answers the workspace's claim
// answer whole (the secret is in it, and it is printed nowhere).
func (p *wsPerson) claim(t *testing.T, svc *svc, slip string, mutate ...func(*http.Request)) (*http.Response, SlipClaimResponse, []byte) {
	t.Helper()
	head := p.hdr(wsAccountA)
	resp, b := svc.callPath(t, SlipClaimPath, "{}", append([]func(*http.Request){func(r *http.Request) {
		head(r)
		r.Header.Set(creds.PersonSlipHeader, slip)
	}}, mutate...)...)
	var ans SlipClaimResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &ans); err != nil {
			t.Fatalf("the claim's answer is not one this contract reads: %v", err)
		}
	}
	return resp, ans, b
}

// claimed claims a slip and answers the secret, failing the test on a refusal.
func (p *wsPerson) claimed(t *testing.T, svc *svc, slip string) string {
	t.Helper()
	resp, ans, b := p.claim(t, svc, slip)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim is answered %d, want 200: %s", resp.StatusCode, b)
	}
	if ans.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret, and this is the call that takes the slip")
	}
	return ans.ClaimSecret
}

// slip reads one record back out of the WORKSPACE's own store.
func (p *wsPerson) slip(t *testing.T, id string) creds.Slip {
	t.Helper()
	sl, found, err := p.store.Slip(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the slip %q back: %v", id, err)
	}
	if !found {
		t.Fatalf("the slip %q is not in the workspace's own database", id)
	}
	return sl
}

// slipIDs is the ids the WORKSPACE's own database holds under the slips node:
// what the grants wrote, whatever they answered (slipNode is the path
// creds.RTDBStore writes under).
func (p *wsPerson) slipIDs(t *testing.T) []string {
	t.Helper()
	node, _ := p.db.Value(slipNode).(map[string]any)
	ids := make([]string, 0, len(node))
	for id := range node {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// personCall is a member's call under a workspace slip: the run's own identity
// and the workspace's two headers, which is what a run presents after its claim
// (F13, R4).
func (p *wsPerson) personCall(t *testing.T, svc *svc, slip, secret, body string, mutate ...func(map[string]any)) (*http.Response, []byte) {
	t.Helper()
	head := p.hdr(wsAccountA, mutate...)
	return svc.callPath(t, MessagesPath, body, func(r *http.Request) {
		head(r)
		if slip != "" {
			r.Header.Set(creds.PersonSlipHeader, slip)
		}
		if secret != "" {
			r.Header.Set(creds.PersonSlipClaimHeader, secret)
		}
	})
}

// TestWorkspaceModeServesTheGrantAndTheClaim is WC4's red step: the workspace's
// own two routes are served by a deployment whose own half is wired, the grant
// writes a slip and answers its id, and the claim takes it — once — and is
// answered the secret the run presents on every call after (R3, R4).
func TestWorkspaceModeServesTheGrantAndTheClaim(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)

	// The route set: both are served by a deployment with its own half wired,
	// and the scope says so (Serves), which is what the front door dispatches
	// on. With no half wired they are refused as any other unserved route is
	// (TestWorkspaceModeRefusesEveryOtherRoute).
	req, err := http.NewRequest(http.MethodPost, wsAudience+SlipPath, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	sc, err := p.res.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve of the workspace's own grant: %v", err)
	}
	if !sc.Serves(SlipPath) {
		t.Fatal("the workspace's own half does not serve its grant, and R3 serves it")
	}
	if _, person := personScopeOf(sc); !person {
		t.Fatal("the scope of the workspace's own grant implements no person half, so no route of it could be served")
	}
	if !p.served(t, SlipClaimPath, wsAccountA).Serves(SlipClaimPath) {
		t.Fatal("the workspace's own half does not serve its claim, and R3 serves it")
	}

	id := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	sl := p.slip(t, id)
	if sl.Owner != wsSub {
		t.Errorf("the record's owner = %q, want the verified `sub` %q: the slip is the person's own", sl.Owner, wsSub)
	}
	if sl.Launcher.Kind != routing.PrincipalPerson || sl.Launcher.ID != wsSub {
		t.Errorf("the record's launcher = %+v, want the person's own account id", sl.Launcher)
	}
	if sl.Run != p.run || sl.Slug != wsSlug || sl.ExpectedAccount != wsAccountA {
		t.Errorf("the record binds %q/%q/%q, want the run, repository and account the call named", sl.Slug, sl.Run, sl.ExpectedAccount)
	}
	if sl.Pool != "" {
		t.Errorf("the record carries the pool %q, and a person's slip names a CONTAINER and not a pool: the two grants are not the same grant", sl.Pool)
	}

	secret := p.claimed(t, svc, id)
	// The claim answers the secret ONCE: a second claim of the same slip is
	// refused, which is D1941 read on the workspace's own record.
	if _, _, body := p.claim(t, svc, id); strings.Contains(string(body), secret) {
		t.Error("the workspace's claim answered the claim secret a second time")
	}
	// And a call under the slip is served: the workspace's own record is the
	// admission, and the member's own policy and pool decide the credential
	// (his fixture's own path, unchanged).
	resp, body := p.personCall(t, svc, id, secret, callBody("ws4-grant-and-claim"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a call under the workspace's own slip = %d, want 200: %s", resp.StatusCode, body)
	}
}

// TestWorkspaceModeAnswersThePersonalSlipThroughTheSwitch pins the seam the
// personal half reads its switch through: a workspace-mode call under a slip
// naming a person's token is answered 200 and not `personalSwitchOff`, and the
// switch it was answered on is THIS PROCESS'S — the package variable the
// workspace-mode serve path assigns (I1's line, and WC5's comment on the
// variable says so) — asked about the caller's own member (wsMemberA, the
// project its verified identity names) and about nobody else.
//
// It also pins WHICH credential is served, which is the half of R7 a call can
// get wrong while still answering 200: the person's own container, and not the
// shared entry the fixture's list holds beside it (D2974, D3144). What that
// assertion is worth, exactly: it reads the answer's own header, so a call path
// that served the shared member where the person's own token can serve — the
// failure I1's end-to-end test is the place for — fails here. It does NOT, on
// this fixture's state, separate the exclusive marking from the offered order:
// the person's token is offered first, so this fixture's tie-break reaches it
// either way. What pins the marking is WC5's own two tests, which seed the
// counter so that the offered order alone would answer a shared member
// (TestThePersonalTokenWinsTheTieBreak, TestThePersonalTokenWinsARoundRobinRotation),
// and `creds`' own Selector.Exclusive tests (select_test.go). What THIS test
// pins is that the production-shaped scope reaches the candidate at all: its
// half is handed out (Persons) rather than declared on the scope, which is the
// path a workspace-mode call really takes.
//
// And with the switch turned off the same call is refused with R11's own
// sentence, which is the class a workspace-mode run is told: the reader's
// refusal travels out of the candidate list rather than being collapsed into a
// sentence of the call path's.
//
// **What it does not prove is that the deployment's wiring exists.** The fixture
// installs a scripted switch ON (newWSPerson says why ON), so the call is
// answered by that reader whether or not I1's line is ever written, and this
// test guards the seam and the member the reader asks about, not the assignment.
// The end-to-end proof — a member's real published policy read through WC6's
// `projectpolicy` reader — is I1's own, which its plan entry names and whose red
// step is this file's default: the stub answers `personalSwitchOff` for every
// project while nothing has assigned the variable.
func TestWorkspaceModeAnswersThePersonalSlipThroughTheSwitch(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	id := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	secret := p.claimed(t, svc, id)

	resp, body := p.personCall(t, svc, id, secret, callBody("ws5-the-switch"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a call under a slip naming a person's token = %d, want 200: %s", resp.StatusCode, body)
	}
	if got := p.switchOn.projects(); len(got) != 1 || got[0] != wsMemberA {
		t.Errorf("the personal switch was asked about %v, want the caller's own member %q alone: the switch is the MEMBER's, and the member is the project the caller's verified identity names", got, wsMemberA)
	}
	own, err := secretname.User(wsSub, creds.PersonTokenName)
	if err != nil {
		t.Fatalf("building the person's own container name: %v", err)
	}
	if got := resp.Header.Get(HeaderCredential); got != own {
		t.Errorf("the call was served %q, want the person's own token %q: a call under a slip naming one is served the person's container and not the shared entry it is also admitted to (R7, D2974)", got, own)
	}

	// And the answer a workspace run gets while the switch is off is R11's OWN
	// sentence and not a house one: the reader's class travels out of the
	// candidate list and is what the call is refused with. The fixture reads the
	// process's variable per call, so turning it off here is the member's
	// published policy turning it off there.
	personalSwitch(t, false, nil)
	resp, body = p.personCall(t, svc, id, secret, callBody("ws5-the-switch-off"))
	if e := wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable); e.Message != personalSwitchOff {
		t.Errorf("the answer with the switch off = %q, want R11's own sentence: a workspace run is told the class its reader answered (R9, R11)", e.Message)
	}
}

// TestTheGrantAdmitsOnlyARosterPerson: the class verified an address, and the
// address must be a `human` of the workspace's roster (R3) — an address the
// roster does not hold, and one it holds as a machine, are the same refusal,
// one class, one audit line, and no record (D3147).
func TestTheGrantAdmitsOnlyARosterPerson(t *testing.T) {
	for _, tc := range []struct {
		name   string
		people []workspace.Person
		admins string
		who    string
	}{
		{"an address the roster does not hold", []workspace.Person{wsHuman("somebody-else@example.com")}, "somebody-else@example.com", "the owner's own address"},
		{"a machine of the roster", []workspace.Person{wsMachine(wsOwner), wsHuman("somebody-else@example.com")}, "somebody-else@example.com", "a machine and not a human"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The roster is written without a grant (internal/workspace refuses
			// a mark for somebody who is not one of its own people), and the
			// grant check is asked AFTER the human's — so the refusal here is
			// the human's, which is the rule this test is about.
			p := newWSPerson(t)
			p.roster.publish(t, wsRosterRaw(t, tc.people, tc.admins))
			svc := p.service(t)
			resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName))
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the grant of %s = %d, want %d: %s", tc.who, resp.StatusCode, StatusRequestRefused, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal type = %q, want C1's one class %q", e.Error, ErrorRequestRefused)
			}
			if ids := p.slipIDs(t); len(ids) != 0 {
				t.Errorf("a refused grant wrote the slips %v, want none", ids)
			}
			if got := lines(svc.audit); len(got) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want 1", len(got))
			}
		})
	}
}

// TestAServiceAccountIsRefusedAGrant: the workspace's class is people, and a
// service account of a member's project is not one — the class refuses it
// before any roster is read, so there is no caller for a line to name and the
// refusal is the class's own (R3, D2970: a member's admins can forge their own
// project's launchers, and this is the class they cannot join).
func TestAServiceAccountIsRefusedAGrant(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	resp, body := p.grant(t, svc, wsStrangerBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("a service account's grant = %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal type = %q, want the launcher class's own %q", e.Error, ErrorUnverifiedJobIdentity)
	}
	if got := lines(svc.audit); len(got) != 0 {
		t.Errorf("a refusal before any caller was verified wrote %d audit lines, want none", len(got))
	}
	if ids := p.slipIDs(t); len(ids) != 0 {
		t.Errorf("a refused grant wrote the slips %v, want none", ids)
	}
}

// TestAGrantNamesOnlyTheCallersOwnToken: the slip names ONE credential — the
// container of the caller's OWN verified account id and of the name the request
// asked for (the owner rule, R7) — and the route reads that container and no
// other. The request carries no person at all: the name is the whole of what
// the caller chooses, and a name this plan does not serve is refused by C1's
// own parser.
func TestAGrantNamesOnlyTheCallersOwnToken(t *testing.T) {
	p := newWSPerson(t)
	own, err := secretname.User(wsSub, creds.PersonTokenName)
	if err != nil {
		t.Fatalf("building the fixture's own container name: %v", err)
	}
	svc := p.service(t)

	id := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	sl := p.slip(t, id)
	if len(sl.Allowed) != 1 || sl.Allowed[0] != own {
		t.Fatalf("the record allows %v, want exactly the caller's own container %q", sl.Allowed, own)
	}
	stranger, err := secretname.User(wsOtherSub, creds.PersonTokenName)
	if err != nil {
		t.Fatalf("building another person's container name: %v", err)
	}
	for _, name := range sl.Allowed {
		if name == stranger {
			t.Errorf("the record allows %q, which is another person's container", name)
		}
	}
	if got := p.secrets.readNames(); len(got) != 1 || got[0] != own {
		t.Errorf("the deployment read the containers %v, want exactly the caller's own %q", got, own)
	}
	// A name this plan does not serve is refused, and it never reaches the
	// deployment's containers at all (C1's parser owns the name's vocabulary;
	// the route names it as its one class).
	if resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, "some-other-name")); resp.StatusCode != StatusRequestRefused {
		t.Errorf("a grant naming a token this plan does not serve = %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if got := p.secrets.readNames(); len(got) != 1 {
		t.Errorf("a refused grant read %v, want only the one container the granted call read", got)
	}
}

// TestAGrantIsHeldToTheNode: the job account and the repository are held to the
// published node, exactly as a member's grant is (R3, R19) — an account the
// node does not name, and a repository that account's own row does not declare,
// are both refusals, of the route's one class.
func TestAGrantIsHeldToTheNode(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)

	for _, tc := range []struct {
		name    string
		account string
		body    func(*wsPerson) string
	}{
		{"an account the node does not name", "fugaro-job-9@project-a.iam.example.com", func(p *wsPerson) string {
			return p.grantBody(t, "fugaro-job-9@project-a.iam.example.com", creds.PersonTokenName)
		}},
		{"a repository the account is not declared on", wsAccountA, func(p *wsPerson) string {
			raw, _ := json.Marshal(creds.PersonGrantRequest{Slug: wsSlugOther, Run: p.run, Account: wsAccountA, Name: creds.PersonTokenName})
			return string(raw)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(lines(svc.audit))
			resp, body := p.grant(t, svc, wsLauncherBearer, tc.body(p))
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the grant of %s = %d, want %d: %s", tc.name, resp.StatusCode, StatusRequestRefused, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal type = %q, want %q", e.Error, ErrorRequestRefused)
			}
			if got := len(lines(svc.audit)) - before; got != 1 {
				t.Errorf("the refusal wrote %d audit lines, want 1", got)
			}
			if ids := p.slipIDs(t); len(ids) != 0 {
				t.Errorf("a refused grant wrote the slips %v, want none", ids)
			}
		})
	}
	// The control: the account the node names, on the repository its row
	// declares, is granted.
	p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
}

// TestAGrantIsBoundToTheMembersThePersonLaunchesFor: a person may ask for a
// slip over the members they are a LAUNCHER of, and the fact is read from the
// workspace's own roster's grants — D2818's mark, which is the one record of it
// the workspace's service can read (D3145 reopened: the workspace holds no grant
// on a member's own roster).
func TestAGrantIsBoundToTheMembersThePersonLaunchesFor(t *testing.T) {
	// The control: the mark names member A, and member A's account is granted.
	p := newWSPerson(t)
	svc := p.service(t)
	p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))

	// The mark names ANOTHER member, and nothing else changes: the person is a
	// human of the roster and the account is one the node names, and the grant
	// is refused all the same.
	other := newWSPerson(t)
	other.roster.publish(t, wsRosterRaw(t, []workspace.Person{wsHuman(wsOwner)}, wsOwner,
		workspace.StoredGrant{Project: wsMemberB, User: wsOwner, Role: workspace.RoleAdmin}))
	svcOther := other.service(t)
	resp, body := other.grant(t, svcOther, wsLauncherBearer, other.grantBody(t, wsAccountA, creds.PersonTokenName))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the grant of a member the person does not launch for = %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if ids := other.slipIDs(t); len(ids) != 0 {
		t.Errorf("a refused grant wrote the slips %v, want none", ids)
	}
	// And the member the mark DOES name is served by the same deployment: the
	// binding is per member and not "the person may launch for nobody".
	other.roster.publish(t, wsRosterRaw(t, []workspace.Person{wsHuman(wsOwner)}, wsOwner,
		workspace.StoredGrant{Project: wsMemberA, User: wsOwner, Role: workspace.RoleOperator}))
	other.now = other.now.Add(2 * time.Minute)
	other.granted(t, svcOther, other.grantBody(t, wsAccountA2, creds.PersonTokenName))
}

// TestTheSlipStoresTheCaseFoldedAddress: the record carries the person's
// verified address in the shape every address in this service is compared in
// (F11) — the fold, and not the spelling the token happened to carry — because
// the check on every call reads it against the roster's own text.
func TestTheSlipStoresTheCaseFoldedAddress(t *testing.T) {
	p := newWSPerson(t, func(cfg *PersonScopeConfig) {
		cfg.Launchers.(*wsLaunchers).callers[wsLauncherBearer] = LauncherCaller{Account: "Owner@Example.COM", Sub: wsSub}
	})
	svc := p.service(t)
	sl := p.slip(t, p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName)))
	if sl.OwnerAddress != wsOwner {
		t.Errorf("the record's owner address = %q, want the folded %q", sl.OwnerAddress, wsOwner)
	}
}

// TestAnUnclaimedWorkspaceSlipExpiresAtTheClaimDeadline: a grant nobody claimed
// stops being claimable at the deadline counted from the GRANT (O1, threat-model
// §11 finding 1), which is what a workspace slip carries and a member's does not:
// an unclaimed member's slip is `Claimed.At + MaxRuntime` and never expires, and
// a record over the workspace's own paid entries must not stay live for a day
// because a launch did not happen.
func TestAnUnclaimedWorkspaceSlipExpiresAtTheClaimDeadline(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	granted := p.now
	id := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))

	sl := p.slip(t, id)
	if !sl.Claimed.At.IsZero() {
		t.Fatal("the fixture's slip is already claimed")
	}
	if sl.ClaimDeadline == nil {
		t.Fatal("the workspace's slip carries no claim deadline, and O1 pins one")
	}
	if want := granted.Add(personSlipClaimWindow); !sl.ClaimDeadline.Equal(want) {
		t.Errorf("the claim deadline = %s, want the grant's %s plus %s", sl.ClaimDeadline, granted, personSlipClaimWindow)
	}
	if got := sl.Expires(); !got.Equal(*sl.ClaimDeadline) {
		t.Errorf("Expires() = %s on an unclaimed slip, want its claim deadline %s", got, *sl.ClaimDeadline)
	}

	// Inside the window the claim stands: the deadline bounds an unclaimed
	// grant and not the run's own life. The instant is the SERVICE's own clock
	// (the fixture's, which the deadline was drawn from), not the resolver's.
	svc.fixture.now = granted.Add(personSlipClaimWindow - time.Minute)
	p.claimed(t, svc, id)

	// Past it the record refuses the claim, and the run reads the one sentence
	// every refusal of the record is given (slipNotThisRuns) — the same answer
	// a slip that was never this run's gets, so a caller cannot use the route
	// to date a record it does not hold.
	svc.fixture.now = granted
	second := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	svc.fixture.now = granted.Add(personSlipClaimWindow + time.Minute)
	resp, ans, body := p.claim(t, svc, second)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the claim of a slip past its deadline = %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity || e.Message != slipNotThisRuns {
		t.Errorf("the refusal is %q/%q, want %q/%q", e.Error, e.Message, ErrorUnverifiedRunIdentity, slipNotThisRuns)
	}
	if ans.ClaimSecret != "" {
		t.Error("a refused claim answered a claim secret")
	}
	// And the record is still unclaimed: the store's compare-and-set wrote
	// nothing, which is what keeps a later call from being served under it.
	if !p.slip(t, second).Claimed.At.IsZero() {
		t.Error("a refused claim wrote the record as claimed")
	}
}

// TestARemovedPersonIsRefusedAfterTheReread: the roster is re-read as the
// published node is (R3, workspaceNodeTTL), so a person taken off it stops
// being admitted within the minute — and the same deployment that granted
// before the removal refuses after it, with one line and no record.
func TestARemovedPersonIsRefusedAfterTheReread(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))

	// The workspace owner's own write: the person is off the roster, and
	// somebody else administers it now.
	p.roster.publish(t, wsRosterRaw(t, []workspace.Person{wsHuman("other@example.com")}, "other@example.com"))
	// Inside the minute the copy held is what is served, which is the staleness
	// R13 accepts (and the reason the next line has to move the clock).
	if resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the grant inside the roster's minute = %d, want 200: %s", resp.StatusCode, body)
	}
	p.now = p.now.Add(2 * time.Minute)
	before := len(lines(svc.audit))
	resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the grant after the removal = %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
		t.Errorf("the refusal type = %q, want %q", e.Error, ErrorRequestRefused)
	}
	if got := len(lines(svc.audit)) - before; got != 1 {
		t.Errorf("the refusal wrote %d audit lines, want 1", got)
	}
}

// TestARemovedPersonsSlipIsRefused: a slip whose owner has left the roster is
// refused on every call (R3) — and the refusal is the record's own sentence, so
// the run cannot tell "removed" from "not yours".
func TestARemovedPersonsSlipIsRefused(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)

	slip := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	secret := p.claimed(t, svc, slip)
	// The control: with the person on the roster the call is served.
	if resp, body := p.personCall(t, svc, slip, secret, callBody("ws4-before-removal")); resp.StatusCode != http.StatusOK {
		t.Fatalf("a call under the slip = %d, want 200: %s", resp.StatusCode, body)
	}

	p.roster.publish(t, wsRosterRaw(t, []workspace.Person{wsHuman("other@example.com")}, "other@example.com"))
	p.now = p.now.Add(2 * time.Minute)
	resp, body := p.personCall(t, svc, slip, secret, callBody("ws4-after-removal"))
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("a call under a removed person's slip = %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity || e.Message != slipNotThisRuns {
		t.Errorf("the refusal is %q/%q, want %q/%q", e.Error, e.Message, ErrorUnverifiedRunIdentity, slipNotThisRuns)
	}
	if len(svc.up.Seen()) != 1 {
		t.Errorf("the upstream saw %d calls, want the one the control made: the removed person's call reached no upstream", len(svc.up.Seen()))
	}
}

// TestARemovedPersonsSlipIsRefusedAtTheClaim: the claim asks the roster about
// the person the RECORD names before it writes anything (R3, personClaim's
// first-claim form). A slip whose owner has left is refused with the record's
// own sentence, and the refusal spends nothing of it: the same slip is
// claimable again once the person is back on the roster.
func TestARemovedPersonsSlipIsRefusedAtTheClaim(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	slip := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))

	p.roster.publish(t, wsRosterRaw(t, []workspace.Person{wsHuman("other@example.com")}, "other@example.com"))
	p.now = p.now.Add(2 * time.Minute)
	resp, ans, body := p.claim(t, svc, slip)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the claim of a removed person's slip = %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity || e.Message != slipNotThisRuns {
		t.Errorf("the refusal is %q/%q, want %q/%q", e.Error, e.Message, ErrorUnverifiedRunIdentity, slipNotThisRuns)
	}
	if ans.ClaimSecret != "" {
		t.Error("the refused claim answered a claim secret")
	}
	if sl := p.slip(t, slip); sl.Claimed != (creds.Claim{}) || sl.ClaimHash != "" {
		t.Errorf("the refused claim took the slip: %+v", sl.Claimed)
	}

	p.roster.publish(t, wsRosterWithOwner(t))
	p.now = p.now.Add(2 * time.Minute)
	if secret := p.claimed(t, svc, slip); secret == "" {
		t.Fatal("the slip is not claimable after the person came back")
	}
}

// TestARemovedPersonsSlipIsRefusedWhenItIsReadBack is the claim's read-back
// form, the one a run makes with the secret it already holds: the roster is
// asked there too (R3), so a person removed after their run claimed is refused
// on the call that would have read the slip back, and the run is answered the
// record's sentence rather than a second claim secret.
func TestARemovedPersonsSlipIsRefusedWhenItIsReadBack(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	slip := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	secret := p.claimed(t, svc, slip)
	// The control: with the person on the roster the read-back is served.
	if resp, _, body := p.readBack(t, svc, slip, secret); resp.StatusCode != http.StatusOK {
		t.Fatalf("the read-back with the person on the roster = %d, want 200: %s", resp.StatusCode, body)
	}

	p.roster.publish(t, wsRosterRaw(t, []workspace.Person{wsHuman("other@example.com")}, "other@example.com"))
	p.now = p.now.Add(2 * time.Minute)
	resp, ans, body := p.readBack(t, svc, slip, secret)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the read-back under a removed person's slip = %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity || e.Message != slipNotThisRuns {
		t.Errorf("the refusal is %q/%q, want %q/%q", e.Error, e.Message, ErrorUnverifiedRunIdentity, slipNotThisRuns)
	}
	if ans.ClaimSecret != "" {
		t.Error("the refused read-back answered a claim secret")
	}
}

// readBack makes the claim's reading form: the run presents the slip AND the
// claim secret it was answered, and is answered the slip's own times rather
// than a second secret.
func (p *wsPerson) readBack(t *testing.T, svc *svc, slip, secret string) (*http.Response, SlipClaimResponse, []byte) {
	t.Helper()
	return p.claim(t, svc, slip, func(r *http.Request) {
		r.Header.Set(creds.PersonSlipClaimHeader, secret)
	})
}

// TestAnUnreadableRosterRefuses: a roster that cannot be read is not a roster
// that lists nobody (D2983): the grant is refused as this service's own state
// and not as a refusal of the caller, so a launcher can tell "the workspace
// refused me" from "the workspace is not answering" (D3147's three answers).
func TestAnUnreadableRosterRefuses(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	p.roster.fail(errors.New("the workspace's database is down"))
	resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a grant whose roster cannot be read = %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal type = %q, want the state class %q", e.Error, ErrorStateUnavailable)
	}
	if ids := p.slipIDs(t); len(ids) != 0 {
		t.Errorf("a refused grant wrote the slips %v, want none", ids)
	}
}

// TestAnUnreadableRosterRefusesACallAsUnavailable is the same rule on the other
// side: a call under a slip whose roster cannot be re-read refuses as the
// service's own state, and NOT with the record's sentence — a run whose person
// is still on the roster must not be stopped by a roster nobody could read.
func TestAnUnreadableRosterRefusesACallAsUnavailable(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	slip := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	secret := p.claimed(t, svc, slip)

	p.roster.fail(errors.New("the workspace's database is down"))
	p.now = p.now.Add(2 * time.Minute)
	resp, body := p.personCall(t, svc, slip, secret, callBody("ws4-unreadable-roster"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a call whose roster cannot be read = %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal type = %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if len(svc.up.Seen()) != 0 {
		t.Errorf("the upstream saw %d calls, want none", len(svc.up.Seen()))
	}
}

// TestNoTokenHeldIsAnsweredBeforeTheNode: a person who holds no token of the
// name they asked about is ANSWERED `held:false` — an answer and not a refusal
// (R8, D3147), the state every run started in before this plan — and it is
// answered before the workspace's membership table is read at all, so the route
// tells a person who holds nothing nothing about the workspace.
//
// A failed read is the other half and must NOT be answered "not held": a
// Secret Manager that was unreachable would otherwise start the run on the
// shared paid entries while the person's own token sat there (D143's
// fail-closed rule, D2974).
func TestNoTokenHeldIsAnsweredBeforeTheNode(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	// The caller's container is not there: no container, or one with no
	// version, are one absence to the grant (R6).
	own, err := secretname.User(wsSub, creds.PersonTokenName)
	if err != nil {
		t.Fatalf("building the fixture's own container name: %v", err)
	}
	delete(p.secrets.held, own)

	before := len(lines(svc.audit))
	resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a grant whose caller holds no token = %d, want %d: %s", resp.StatusCode, http.StatusOK, body)
	}
	var ans creds.PersonGrant
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not one C1 reads: %v", err)
	}
	if ans.Held || ans.Slip != "" {
		t.Errorf("the answer is held=%v slip=%q, want no token held", ans.Held, ans.Slip)
	}
	if ids := p.slipIDs(t); len(ids) != 0 {
		t.Errorf("a `held:false` answer wrote the slips %v, want none", ids)
	}
	if got := p.node.readsSoFar(); got != 0 {
		t.Errorf("the published node was read %d times, want none: R3 answers this before the node is read", got)
	}
	if got := p.roster.readsSoFar(); got != 0 {
		t.Errorf("the roster was read %d times, want none: a person who holds no token learns nothing of the workspace", got)
	}
	if got := len(lines(svc.audit)) - before; got != 0 {
		t.Errorf("a `held:false` answer wrote %d audit lines, want none: it is an answer and not a refusal", got)
	}

	// A read that FAILED is not that answer.
	p.secrets.fail(errors.New("secret manager is unreachable"))
	resp, body = p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a grant whose container cannot be read = %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal type = %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if got := p.node.readsSoFar(); got != 0 {
		t.Errorf("the published node was read %d times, want none", got)
	}

	// And with a container that IS there, the node is read — which is the
	// control for the two assertions above.
	p.secrets.fail(nil)
	p.secrets.held[own] = fakeCred(t, "personal")
	p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if got := p.node.readsSoFar(); got == 0 {
		t.Error("the node was never read, and a grant that found the token is held to it")
	}
}

// TestAGrantRefusalNamesNothing: every refusal of the route's own is ONE class
// and one sentence, and that sentence names no account, no repository, no
// address and no name (R3, D3147) — so a person who holds a token learns
// nothing from a refusal beyond the fact of it.
func TestAGrantRefusalNamesNothing(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, "fugaro-job-9@project-a.iam.example.com", creds.PersonTokenName))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the refusal = %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Message != personGrantRefused {
		t.Errorf("the refusal's sentence is %q, want the route's one %q", e.Message, personGrantRefused)
	}
	for _, named := range []string{wsAccountA, "fugaro-job-9@project-a.iam.example.com", wsRepo, wsSlug, wsOwner, wsMemberA} {
		if strings.Contains(e.Message, named) {
			t.Errorf("the refusal %q names %q", e.Message, named)
		}
	}
}

// TestTheGrantRefusesARunIdentityHeader: the grant route sees no run identity
// (C1's own shape for it, R3) — a call that carries one is refused as the
// route's ONE class, written the same as a refusal of the roster, and no slip
// is written for it. The refusal names no header either: which header it was is
// the one thing this route never says (D3147's "and nothing finer").
func TestTheGrantRefusesARunIdentityHeader(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	withRunIdentity := func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-token-nobody-verified") }
	resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, creds.PersonTokenName), withRunIdentity)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("a grant carrying a run identity = %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorRequestRefused || e.Message != personGrantRefused {
		t.Errorf("the refusal is %q/%q, want the route's one class %q/%q", e.Error, e.Message, ErrorRequestRefused, personGrantRefused)
	}
	if strings.Contains(e.Message, HeaderRunIdentity) || strings.Contains(e.Message, wsAccountA) {
		t.Errorf("the refusal %q names what it refused", e.Message)
	}
	if ids := p.slipIDs(t); len(ids) != 0 {
		t.Errorf("a refused grant wrote the slips %v, want none", ids)
	}
	got := lines(svc.audit)
	if len(got) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly 1: %v", len(got), got)
	}
	if got[0]["caller"] != wsOwner || got[0]["status"] != ErrorRequestRefused || got[0]["purpose"] != PurposeSlipGrant {
		t.Errorf("the line is %v, want the verified launcher %q, status %q and the grant's purpose", got[0], wsOwner, ErrorRequestRefused)
	}
}

// TestEveryGrantIsAudited: once a caller IS verified, each grant and each
// refusal of the grant writes exactly one line (R3, WC4). The granted call's
// line is written with no credential, because a grant issues none and reads no
// bundle entry (audit.go's credentiallessPurpose); it names the verified
// launcher, and nothing the launcher asked for — the account, the repository
// and the run are the caller's own bytes.
func TestEveryGrantIsAudited(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	if got := lines(svc.audit); len(got) != 0 {
		t.Fatalf("the fixture starts with %d audit lines, want none", len(got))
	}
	p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	granted := lines(svc.audit)
	if len(granted) != 1 {
		t.Fatalf("a granted call wrote %d audit lines, want exactly 1: %v", len(granted), granted)
	}
	if got, want := granted[0]["caller"], wsOwner; got != want {
		t.Errorf("the granted line names the caller %v, want the verified launcher %q", got, want)
	}
	for field, want := range map[string]any{
		"purpose":    PurposeSlipGrant,
		"status":     StatusOK,
		"credential": "",
		"tier":       "",
	} {
		if got := granted[0][field]; got != want {
			t.Errorf("the granted line's %s is %v, want %q: a grant issues no credential", field, got, want)
		}
	}
	for key, value := range granted[0] {
		if text, ok := value.(string); ok && strings.Contains(text, wsAccountA) {
			t.Errorf("the granted line's %q carries the account %q", key, text)
		}
	}
	resp, body := p.grant(t, svc, wsLauncherBearer, p.grantBody(t, wsAccountA, "a-name-this-plan-does-not-serve"))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the refused grant = %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	got := lines(svc.audit)
	if len(got) != 2 {
		t.Fatalf("the grant and its refusal wrote %d audit lines, want exactly 1 each: %v", len(got), got)
	}
	refused := got[len(got)-1]
	if refused["caller"] != wsOwner {
		t.Errorf("the line names the caller %v, want the verified launcher %q", refused["caller"], wsOwner)
	}
	if refused["status"] != ErrorRequestRefused {
		t.Errorf("the line's status is %v, want %q", refused["status"], ErrorRequestRefused)
	}
	for key, value := range refused {
		if text, ok := value.(string); ok && strings.Contains(text, wsAccountA) {
			t.Errorf("the audit line's %q carries the account %q", key, text)
		}
	}
}

// TestTheSlipIsWrittenToTheWorkspaceOnly: the record is written into the
// WORKSPACE's own database and into no other (R3, R4) — the installation's own
// slip store, which the same process holds for its member routes, holds nothing
// of it.
func TestTheSlipIsWrittenToTheWorkspaceOnly(t *testing.T) {
	p := newWSPerson(t)
	svc := p.service(t)
	id := p.granted(t, svc, p.grantBody(t, wsAccountA, creds.PersonTokenName))
	if !slices.Contains(p.slipIDs(t), id) {
		t.Errorf("the workspace's own database holds %v, and the grant answered %q", p.slipIDs(t), id)
	}
	if _, found, err := svc.server.cfg.Slips.Slip(context.Background(), id); err != nil {
		t.Fatalf("reading the installation's store: %v", err)
	} else if found {
		t.Error("the workspace's slip is in the installation's store, and a member's service would serve it")
	}
}
