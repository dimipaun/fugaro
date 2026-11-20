package credsvc

// WS1's own tests: the installation's side of C1's seam.
//
// The first is the pin the plan names — the installation's scope is today's
// behaviour, read through the scope and answered identically — and it is the
// test the red step fails: before the model path read its caller and its
// readers from the scope, there was no installation resolver to resolve at all.
// The second is the front door: a route the scope does not serve reaches no
// handler, and the refusal it gets is written with or without a caller
// according to what the resolver verified (R14). The rest are the front door's
// other two guards — a scope that brings no caller, and a scope that accepts no
// slip (R18) — each refused before any handler is dispatched.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// resolverWithout is an installation's resolver answering its scope with one of
// three answers changed: the path the scope does not serve, the caller it does
// not report, or the slip it does not accept. Each is a deployment whose scope
// and whose front door disagree, which is exactly what the front door's own
// guards are for, and the three are the three guards.
type resolverWithout struct {
	inner    ScopeResolver
	path     string
	noCaller bool
	noSlip   bool
}

func (r resolverWithout) Resolve(w http.ResponseWriter, req *http.Request) (Scope, bool) {
	sc, ok := r.inner.Resolve(w, req)
	if !ok {
		return nil, false
	}
	return scopeWithout{Scope: sc, path: r.path, noCaller: r.noCaller, noSlip: r.noSlip}, true
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
}

func (s scopeWithout) Serves(path string) bool { return path != s.path }

func (s scopeWithout) Caller() (Caller, bool) {
	if s.noCaller {
		return Caller{}, false
	}
	return s.Scope.Caller()
}

func (s scopeWithout) AcceptsSlip() bool { return !s.noSlip && s.Scope.AcceptsSlip() }
