package credsvc

// WS1's own tests: the installation's side of C1's seam.
//
// The first is the pin the plan names — the installation's scope is today's
// behaviour, read through the scope and answered identically — and it is the
// test the red step fails: before the model path read its caller and its
// readers from the scope, there was no installation resolver to resolve at all.
// The second is the front door: a route the scope does not serve reaches no
// handler, and the refusal it gets is written with or without a caller
// according to what the resolver verified (R14). Then come the front door's
// other two guards — a scope that brings no caller, and a scope that accepts no
// slip (R18) — each refused before any handler is dispatched.
//
// The last two are R1's guarantee itself, and they are the reason the ones
// before them are not enough: a scope that answers the deployment's own
// objects is indistinguishable from the deployment, so every test above passes
// against a model path that reads Config directly — which is what the first
// test's assertion is worth nothing without. Each gives the call a scope whose
// policy, kill switches and state are objects of the test's own, wraps the
// deployment's in tripwires that fail what is read of them, and asserts one
// call reach the scope's readers and the deployment's none: the model path's
// five reads (ServeHTTP's stop switches, its policy and the selector's store;
// the cascade's repository record and its admission's policy) and the probe's
// marks.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestInstallationScopeIsTodaysBehaviour holds the installation's scope to the
// deployment it reads: every route, every reader, one caller — and the model
// path serving a call through it exactly as it did before the seam existed.
func TestInstallationScopeIsTodaysBehaviour(t *testing.T) {
	// The inventory whose slug→canonical row the table's Repo read answers
	// from: the same map the read mint resolves a workspace out of
	// (workspaceOf), which is why it is the deployment's answer rather than a
	// second table.
	s := newSvcWith(t, func(sv *svc, cfg *Config) { cfg.Repos = map[string]string{sv.slug: testRepo} })
	ctx := context.Background()

	// A call the shared parse answers, resolved the way ServeHTTP resolves it.
	req := httptest.NewRequest(http.MethodPost, MessagesPath, nil)
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	sc, ok := s.server.resolver.Resolve(httptest.NewRecorder(), req)
	if !ok {
		t.Fatal("the installation's resolver refused a call of its own run")
	}
	call, called := sc.Caller()
	if !called {
		t.Fatal("the installation's resolver answered a scope with no caller for a route of the shared parse")
	}
	if call.Identity.Slug != s.slug || call.Identity.Run != s.run {
		t.Errorf("the scope's caller is the run %q/%q, want the verified %q/%q", call.Identity.Slug, call.Identity.Run, s.slug, s.run)
	}
	if call.Canonical != testRepo {
		t.Errorf("the scope paired the call with %q, want the job account's repository %q", call.Canonical, testRepo)
	}
	if call.Job.Email != testJobSA || call.Job.Repo != testRepo {
		t.Errorf("the scope's job row is %+v, want the configured %s bound to %s", call.Job, testJobSA, testRepo)
	}
	if call.Request.JobIDToken == "" || call.Request.RunIDToken == "" {
		t.Error("the scope carries no parsed request, and the claim route and the sign route read it")
	}

	// Every route of this service, and the one predicate the resolver shares
	// with the front door: a route answered before the shared parse is resolved
	// with no identity read at all.
	routes := []string{MessagesPath, CountTokensPath, MintPath, ContextTokenPath, SignPath, VerifyPath,
		BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, IssuePath, SlipPath, SlipClaimPath,
		WorkflowSecretsPath, BuildTokenPath, BuildCredentialsPath, BitbucketProxyPrefix + "acme/aurora.git/info/refs"}
	for _, path := range routes {
		if !sc.Serves(path) {
			t.Errorf("the installation's scope does not serve %q, and an installation serves every route of the service", path)
		}
	}
	early := []string{VerifyPath, BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, BuildTokenPath,
		BuildCredentialsPath, IssuePath, SlipPath}
	for _, path := range early {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		got, ok := s.server.resolver.Resolve(httptest.NewRecorder(), r)
		if !ok {
			t.Errorf("the installation's resolver refused %q before it read an identity, and the route's own class has to admit the caller", path)
			continue
		}
		if _, called := got.Caller(); called {
			t.Errorf("%q was resolved with a caller, and its caller holds no run identity (RequestFromHTTP would refuse the call before the class read anything)", path)
		}
		if !aheadOfTheSharedParse(r) {
			t.Errorf("aheadOfTheSharedParse does not name %q, which the resolver resolved with no identity: the predicate and the front door's own branches disagree", path)
		}
		if !got.Serves(path) {
			t.Errorf("the installation's scope does not serve %q, and an installation serves every route of the service", path)
		}
	}
	proxy := httptest.NewRequest(http.MethodPost, BitbucketProxyPrefix+"acme/aurora.git/git-receive-pack", nil)
	if aheadOfTheSharedParse(proxy) {
		t.Error("a run's proxy call is answered ahead of the shared parse, and only the build half carries " + HeaderBuild)
	}
	build := httptest.NewRequest(http.MethodPost, BitbucketProxyPrefix+"acme/aurora.git/git-receive-pack", nil)
	build.Header.Set(HeaderBuild, "build-1")
	if !aheadOfTheSharedParse(build) {
		t.Error("a build's proxy call is not named by aheadOfTheSharedParse, and its caller holds no run identity")
	}

	// The readers: the deployment's own, and the ones this scope answers
	// differently because they are not its deployment's to answer.
	if sc.AcceptsSlip() != true {
		t.Error("the installation's scope refuses a slip, and the slips of an installation are a run's own calls to it (DS1)")
	}
	if sc.Meter() != nil {
		t.Error("the installation's scope answered a meter, and R15's cap is the workspace's")
	}
	if sc.Policy() != s.server.cfg.Policy {
		t.Error("the installation's scope does not answer the policy the service was configured with")
	}
	if sc.Kills() != s.server.cfg.Kills {
		t.Error("the installation's scope does not answer the kill reader the service was configured with")
	}
	if sc.State() != s.server.cfg.State {
		t.Error("the installation's scope does not answer the state the service was configured with")
	}
	if sc.Outcomes() != nil {
		t.Error("the installation's scope answered an outcomes reader over a database this fixture's patcher cannot read as a Getter")
	}
	if ja, found := sc.Table().Job(testJobSA); !found || ja.Repo != testRepo {
		t.Errorf("the scope's table answers %+v/%v for %s, want the configured row bound to %s", ja, found, testJobSA, testRepo)
	}
	if repo, found := sc.Table().Repo(s.slug); !found || repo != testRepo {
		t.Errorf("the scope's table resolves %q to %q/%v, want %q", s.slug, repo, found, testRepo)
	}
	if _, found := sc.Table().Review(testJobSA); found {
		t.Error("the scope's table knows a review account, and this deployment configures none")
	}
	if _, found := sc.Table().Shared(testJobSA); found {
		t.Error("the scope's table knows a shared account, and this deployment configures none")
	}

	// The offering: the bundle's own credentials, not final (the admission and
	// the repository's record narrow it), and the forge reads: the bundle's own
	// entries, which this fixture's document does not carry.
	b, err := s.server.cfg.Bundles.Load(ctx)
	if err != nil {
		t.Fatalf("the fixture's bundle could not be loaded: %v", err)
	}
	offered, err := sc.Offering().Offer(ctx, call, s.policy.shared, b)
	if err != nil {
		t.Fatalf("the installation's offering answered an error for a caller the bundle serves: %v", err)
	}
	if offered.Final {
		t.Error("the installation's offering answered a final list for a repository caller, and only a review caller's list is final")
	}
	if len(offered.Credentials) != len(b.Credentials()) {
		t.Errorf("the offering holds %d credentials and the bundle holds %d: an installation offers its bundle and narrows it in the cascade, not here", len(offered.Credentials), len(b.Credentials()))
	}
	if _, ok, err := sc.Forge().BitbucketToken(ctx, testRepo); err != nil || ok {
		t.Errorf("the installation's forge reader answered %v/%v for the Bitbucket token, want no entry and no error (this fixture's bundle carries none)", ok, err)
	}
	if _, ok, err := sc.Forge().AppKey(ctx, testRepo); err != nil || ok {
		t.Errorf("the installation's forge reader answered %v/%v for the App key, want no entry and no error (this fixture's bundle carries none)", ok, err)
	}

	// And the call itself: the model path serves the fixture's run through the
	// scope exactly as it did before the seam existed.
	resp, body := s.call(t, callBody("ws1-installation-scope"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the model call is answered %d: %s", resp.StatusCode, body)
	}
	if len(s.up.Seen()) != 1 {
		t.Errorf("the upstream saw %d calls, want the one this call was forwarded as", len(s.up.Seen()))
	}
	ev := s.auditLines(t)
	if len(ev) != 1 {
		t.Fatalf("the issuance wrote %d audit lines, want one per served call", len(ev))
	}
	if got := ev[0]["caller"]; got != testJobSA {
		t.Errorf("the issuance's line names the caller %v, want %s", got, testJobSA)
	}
}

// TestARouteTheScopeDoesNotServeIsRefused is the front door's half of R14: a
// route the scope does not serve reaches no handler at all, and the refusal is
// the service's own when no caller was verified and an audited refusal when one
// was.
func TestARouteTheScopeDoesNotServeIsRefused(t *testing.T) {
	t.Run("a scope that serves nothing refuses every route", func(t *testing.T) {
		s := newSvcWith(t, func(_ *svc, cfg *Config) { cfg.Resolver = StubScopeResolver{} })
		for _, path := range []string{MessagesPath, SignPath, VerifyPath, BitbucketProxyPrefix + "acme/aurora.git/info/refs"} {
			resp, body := s.postRaw(t, http.MethodPost, path, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+s.jobToken())
				r.Header.Set(HeaderRunIdentity, s.runToken())
				r.Header.Set(HeaderRunJob, s.job)
			})
			if resp.StatusCode != StatusUnverified {
				t.Errorf("POST %s is answered %d, want %d: a route the scope does not serve is refused", path, resp.StatusCode, StatusUnverified)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("POST %s is refused with %q, want %q", path, e.Error, ErrorUnverifiedJobIdentity)
			}
		}
		// The stub scope verified no caller, so the refusal is the service's own
		// and writes no audit line: the rule is about a refusal taken with an
		// identity in hand (audit.go).
		if lines := s.auditLines(t); len(lines) != 0 {
			t.Errorf("a refusal written with no verified caller wrote %d audit lines: %v", len(lines), lines)
		}
		if len(s.up.Seen()) != 0 {
			t.Errorf("the upstream saw %d calls, want none: the route reached no handler at all", len(s.up.Seen()))
		}
	})

	t.Run("a route of the shared parse is refused with a line", func(t *testing.T) {
		// A scope that serves everything but the sign route: the shape of a
		// workspace, which serves the model and forge routes and refuses the
		// rest (R14). It wraps the installation's own resolver, which is what
		// the service built out of its configuration — the wrap is installed
		// after NewServer because the resolver it wraps is the server's.
		s := newSvc(t)
		s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, path: SignPath}
		resp, body := s.callPath(t, SignPath, "{}")
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the sign route is answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		e := refusal(t, resp.StatusCode, body)
		if e.Error != ErrorUnverifiedJobIdentity {
			t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedJobIdentity)
		}
		ev := s.auditLines(t)
		if len(ev) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want the one a refusal with a verified caller writes: %v", len(ev), ev)
		}
		if got := ev[0]["status"]; got != ErrorUnverifiedJobIdentity {
			t.Errorf("the refusal's line is recorded as %v, want %q (the audit status is the refusal's own wire type)", got, ErrorUnverifiedJobIdentity)
		}
		if got := ev[0]["caller"]; got != testJobSA {
			t.Errorf("the refusal's line names the caller %v, want %s: the caller was verified", got, testJobSA)
		}
		// The routes the scope does serve are untouched: the wrap is one path.
		resp2, body2 := s.call(t, callBody("ws1-served-route"))
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("a route the scope does serve is answered %d: %s", resp2.StatusCode, body2)
		}
	})
}

// TestAScopeThatBringsNoCallerIsRefused pins the front door's own guard (R1,
// C1): a scope that serves a route of the shared parse and answers no caller is
// a resolver disagreeing with the front door, and the call is refused rather
// than dispatched with nobody's identity. It is the fence that keeps a scope
// which verified nobody from being served the project's own paid key — which is
// why it is a test and not a comment.
func TestAScopeThatBringsNoCallerIsRefused(t *testing.T) {
	s := newSvc(t)
	s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, noCaller: true}
	resp, body := s.call(t, callBody("ws1-no-caller"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a call a scope served with no caller is answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if len(s.up.Seen()) != 0 {
		t.Errorf("the upstream saw %d calls, want none: a call with no caller reaches no handler", len(s.up.Seen()))
	}
	// The line is what tells this guard from the tail the same call reaches
	// without it: an empty identity would still end in `state_unavailable` at
	// the selector (nothing is left to select), so the status alone does not
	// say which refusal this is — and the guard answers through `refuse`, the
	// wire writer alone, where a refusal with a caller in hand is audited.
	if lines := s.auditLines(t); len(lines) != 0 {
		t.Errorf("a refusal of a call with no caller wrote %d audit lines: %v", len(lines), lines)
	}
}

// TestAScopeThatAcceptsNoSlipRefusesOne is R18's fence: a call that PRESENTS a
// slip to a scope that accepts none is refused, and never served slip-less —
// which is what "a member's call carries the job's identity and the run's token
// and nothing else of the member's" means for the one secret a call could
// carry. The caller was verified, so the refusal is audited like every other.
func TestAScopeThatAcceptsNoSlipRefusesOne(t *testing.T) {
	s := newSvc(t)
	s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, noSlip: true}
	resp, body := s.call(t, callBody("ws1-no-slip"), func(r *http.Request) {
		r.Header.Set(HeaderSlip, "a-slip-this-scope-does-not-accept")
	})
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("a call presenting a slip to a scope that accepts none is answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
	} else if !strings.Contains(e.Message, "carries no slip") {
		t.Errorf("the refusal's sentence is %q, want the one that says a call to this service carries no slip: the refusal of a slip the service cannot read is the same status and the same type, so the sentence is what says which of the two this is", e.Message)
	}
	if len(s.up.Seen()) != 0 {
		t.Errorf("the upstream saw %d calls, want none: the call was refused before any handler", len(s.up.Seen()))
	}
	ev := s.auditLines(t)
	if len(ev) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want the one a refusal with a verified caller writes: %v", len(ev), ev)
	}
	if got := ev[0]["status"]; got != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal's line is recorded as %v, want %q", got, ErrorUnverifiedRunIdentity)
	}
	if got := ev[0]["caller"]; got != testJobSA {
		t.Errorf("the refusal's line names the caller %v, want %s: the caller was verified", got, testJobSA)
	}
	// The fence is on the slip a call presents and not on the scope's callers: a
	// call that presents none is served exactly as it was.
	resp2, body2 := s.call(t, callBody("ws1-no-slip-absent"))
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("a slip-less call is answered %d: %s", resp2.StatusCode, body2)
	}
}

// TestTheModelPathReadsTheScopesReadersAndNotTheDeployments is R1's guarantee
// itself, over one served model call: the policy, the kill switches and the
// state this call is decided by are the SCOPE's three readers, and the
// deployment's are read nowhere.
//
// This is the assertion every other test in this file is worth nothing without.
// An installation's scope answers exactly the deployment's own objects, so a
// model path that read Config directly — the shape the seam replaced — serves
// the same call, writes the same state and passes everything above this test.
// What tells the two apart is a scope whose readers are objects of the test's
// own: the call below is served from those and refused by the deployment's,
// which are tripwires that fail every read of them.
//
// It covers the model path's five R1 reads: ServeHTTP's stop switches, its
// policy's shared node and the store the selector is built over; and
// candidates' policy (the admission) and its state (the repository's recorded
// auth).
func TestTheModelPathReadsTheScopesReadersAndNotTheDeployments(t *testing.T) {
	// The deployment's three, as tripwires: each fails every read of it, so a
	// path that reached one answers that read's refusal instead of serving the
	// call.
	deployKills := &fakeKills{err: errors.New("the deployment's stop switches were read")}
	deployPolicy := &fakePolicy{
		sharedErr: errors.New("the deployment's policy was read"),
		admErr:    errors.New("the deployment's policy was read"),
	}
	deployState := newFakeStore()
	s := newSvcWith(t, func(_ *svc, cfg *Config) {
		cfg.Kills = deployKills
		cfg.Policy = deployPolicy
		cfg.State = deployState
	})
	// The record is keyed by the slug the cascade reads its state with
	// (candidates' id.Slug), and the deployment's copy says `vertex`: the one
	// recorded auth this service serves no call by at all (narrowToRepoAuth),
	// so a path that read it there would refuse this call.
	deployState.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: "vertex"})

	// The scope's three, as a workspace's would be: objects of their own,
	// working, and recording what the call reads of them.
	scopedKills := &fakeKills{repos: map[string]bool{}}
	scopedPolicy := &fakePolicy{
		shared:      decodeTestPolicy(t, testPolicyDoc()),
		sharedFound: true,
		admissions:  map[string]*projectpolicy.RepoPolicy{s.slug: decodeTestChild(t, testChildDoc("default", testAccount))},
	}
	scopedState := newFakeStore()
	scopedState.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: config.AuthCascade})
	// Installed after NewServer, because the resolver it wraps is the server's.
	s.server.resolver = resolverWithout{
		inner:  installationResolver{s: s.server},
		kills:  scopedKills,
		policy: scopedPolicy,
		state:  scopedState,
	}

	resp, body := s.call(t, callBody("ws1-scope-readers"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the model call is answered %d, want the call served out of the scope's readers: %s", resp.StatusCode, body)
	}
	if len(s.up.Seen()) != 1 {
		t.Errorf("the upstream saw %d calls, want the one this call was forwarded as", len(s.up.Seen()))
	}

	// Reached: the stop switches, the policy's shared node (read by the front
	// door and again by the admission), the repository's own node, and the
	// state the selection is made over and written back to.
	if got := scopedKills.readCount(); got != 1 {
		t.Errorf("the scope's stop switches were read %d times, want the one read every routed call makes", got)
	}
	if got := policySharedReads(scopedPolicy); got < 2 {
		t.Errorf("the scope's policy answered %d reads of the shared node, want the front door's own and the admission's", got)
	}
	if got := scopedPolicy.readAdmissions(); len(got) != 1 || got[0] != s.slug {
		t.Errorf("the scope's policy answered the admissions read %v, want the caller's own repository %q", got, s.slug)
	}
	if placed, ok := scopedState.placed(s.credsRun()); !ok {
		t.Error("the call wrote no placement in the scope's state, and the selector has to be built over it")
	} else if placed == "" {
		t.Error("the call's placement in the scope's state names no credential")
	}

	// And the deployment's three were read nowhere.
	if got := deployKills.readCount(); got != 0 {
		t.Errorf("the deployment's stop switches were read %d times by a call the scope's own answered", got)
	}
	if got := policySharedReads(deployPolicy); got != 0 {
		t.Errorf("the deployment's policy answered %d reads of the shared node, want none: the policy is the scope's", got)
	}
	if got := deployPolicy.readAdmissions(); len(got) != 0 {
		t.Errorf("the deployment's policy answered the admissions read %v, want none: the admission is the scope's", got)
	}
	if placed, ok := deployState.placed(s.credsRun()); ok {
		t.Errorf("the call wrote its placement on %q in the deployment's state, want the scope's", placed)
	}
}

// TestTheProbeReadsTheScopesStateAndNotTheDeployments pins the probe's own read
// (probe.go's probeDue, R1, R8): the marks that decide whether a probe is owed,
// and on which credential, are the CALL's scope's — the same state the
// selection that answered the wait was made over.
//
// The two stores are what tells it apart. The scope's is the fixture's own and
// carries the backed-off mark; the deployment's is empty and counts the reads
// of its marks. A probe decided from the deployment's marks finds nothing due
// and answers the run the wait it was given, so the call below is served at all
// only if the marks came from the scope.
func TestTheProbeReadsTheScopesStateAndNotTheDeployments(t *testing.T) {
	deployState := newCountingStore()
	s, _ := probeSvc(t, func(_ *svc, cfg *Config) { cfg.State = deployState })
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		// The probe's own answer first, and one no run may ever be served: the
		// marker is what says which of the two the run was given.
		{Status: http.StatusOK, Body: `{"type":"message","probe_answer":"probe-answer"}`},
		anthropicfake.MessageOK(testModel, testUsage),
	}
	// The scope answers the fixture's own state — the one the mark was planted
	// in — and the deployment's store is left out of the call entirely.
	s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, state: s.state}

	resp, body := s.call(t, callBody("ws1-probe-state"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is answered %d, want the probe made and the run's own call served: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "msg_fake") || strings.Contains(string(body), "probe-answer") {
		t.Errorf("the run was given an answer that is not the upstream's answer to its own call: %s", body)
	}
	if got := s.up.Count(); got != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe and then the run's call", got)
	}
	if m, ok := s.state.mark("default"); !ok || !m.Available() {
		t.Errorf("the successful probe left the scope's mark at %+v, want it cleared", m)
	}
	if got := deployState.markReads(); got != 0 {
		t.Errorf("the deployment's state was read for its marks %d times by a call whose state is the scope's", got)
	}
}

// countingStore is a store of the test's own that counts the reads of its
// marks: the probe's own mark read is one two stores can differ by with no
// observable answer at all — a store with no due mark and a store nobody asked
// both answer "nothing is due" — so a test that pins WHICH store the probe read
// has to count it.
type countingStore struct {
	*fakeStore
	mu     sync.Mutex
	marksR int
}

func newCountingStore() *countingStore { return &countingStore{fakeStore: newFakeStore()} }

func (c *countingStore) Marks(ctx context.Context) (map[string]creds.Mark, error) {
	c.mu.Lock()
	c.marksR++
	c.mu.Unlock()
	return c.fakeStore.Marks(ctx)
}

// markReads is how many of those reads it answered.
func (c *countingStore) markReads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.marksR
}

// policySharedReads is how many times a policy of the test's own was asked for
// the project's shared node, which fakePolicy records under its own lock.
func policySharedReads(p *fakePolicy) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sharedReads
}

// resolverWithout is an installation's resolver answering its scope with one of
// six answers changed, and the two groups are the two things a scope can
// disagree with: the three the front door's own guards read (the path the scope
// does not serve, the caller it does not report, the slip it does not accept),
// each a deployment whose scope and whose front door disagree; and the three
// readers R1's seam is about (the policy, the kill switches and the state),
// each answered from an object of the test's own where the test sets one, which
// is what makes a reader that read the deployment instead of the scope fail.
type resolverWithout struct {
	inner    ScopeResolver
	path     string
	noCaller bool
	noSlip   bool
	policy   PolicyReader
	kills    KillReader
	state    creds.Store
}

func (r resolverWithout) Resolve(w http.ResponseWriter, req *http.Request) (Scope, bool) {
	sc, ok := r.inner.Resolve(w, req)
	if !ok {
		return nil, false
	}
	return scopeWithout{Scope: sc, path: r.path, noCaller: r.noCaller, noSlip: r.noSlip,
		policy: r.policy, kills: r.kills, state: r.state}, true
}

type scopeWithout struct {
	Scope
	path string
	// noCaller answers Caller with no caller at all: the shape a resolver that
	// served a route of the shared parse without verifying anybody answers.
	noCaller bool
	// noSlip refuses the slip (R18): a workspace's scope, whose callers present
	// none.
	noSlip bool
	// policy, kills and state are the scope's readers where the test answers
	// them from its own object, and nil where it does not: a nil one answers
	// the scope this wraps, so a test names only the readers its claim is
	// about.
	policy PolicyReader
	kills  KillReader
	state  creds.Store
}

func (s scopeWithout) Serves(path string) bool { return path != s.path }

func (s scopeWithout) Caller() (Caller, bool) {
	if s.noCaller {
		return Caller{}, false
	}
	return s.Scope.Caller()
}

func (s scopeWithout) AcceptsSlip() bool { return !s.noSlip && s.Scope.AcceptsSlip() }

func (s scopeWithout) Policy() PolicyReader {
	if s.policy == nil {
		return s.Scope.Policy()
	}
	return s.policy
}

func (s scopeWithout) Kills() KillReader {
	if s.kills == nil {
		return s.Scope.Kills()
	}
	return s.kills
}

func (s scopeWithout) State() creds.Store {
	if s.state == nil {
		return s.Scope.State()
	}
	return s.state
}
