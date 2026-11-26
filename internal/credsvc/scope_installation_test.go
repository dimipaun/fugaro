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
// other three guards — a scope that brings no caller, a scope that accepts no
// slip (R18), and a scope that could not be built at all (C1's ScopeFault) —
// each refused before any handler is dispatched.
//
// The last two are R1's guarantee itself, and they are the reason the ones
// before them are not enough: a scope that answers the deployment's own
// objects is indistinguishable from the deployment, so every test above passes
// against a model path that reads Config directly — which is what the first
// test's assertion is worth nothing without. Each gives the call a scope whose
// policy, kill switches and readers are objects of the test's own, wraps the
// deployment's in tripwires that fail what is read of them, and asserts one
// call reach the scope's readers and the deployment's none: the model path's
// reads (ServeHTTP's stop switches, its policy and the selector's store; the
// cascade's repository record — read through the scope's OWN RepoAuth reader,
// which is C1's rule 2 — and its admission's policy) and the probe's marks.

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
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestInstallationScopeIsTodaysBehaviour holds the installation's scope to the
// deployment it reads: every route, every reader, one caller — and the model
// path serving a call through it exactly as it did before the seam existed.
func TestInstallationScopeIsTodaysBehaviour(t *testing.T) {
	// The inventory whose slug→canonical row the scope's Repos answers from:
	// the same map the read mint resolves a workspace out of (workspaceOf),
	// which is why it is the deployment's answer rather than a second table.
	s := newSvcWith(t, func(sv *svc, cfg *Config) { cfg.Repos = map[string]string{sv.slug: testRepo} })
	ctx := context.Background()

	// A call the shared parse answers, resolved the way ServeHTTP resolves it.
	req := httptest.NewRequest(http.MethodPost, MessagesPath, nil)
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	sc, err := s.server.resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("the installation's resolver refused a call of its own run: %v", err)
	}
	call := sc.Caller()
	if !callerNamed(call) {
		t.Fatal("the installation's resolver answered a scope with no caller for a route of the shared parse")
	}
	if call.Slug != s.slug || call.Run != s.run {
		t.Errorf("the scope's caller is the run %q/%q, want the verified %q/%q", call.Slug, call.Run, s.slug, s.run)
	}
	// C1's scope carries the IDENTITY and not the parsed request: the two
	// headers the routes hand their readers as SCRUB values are read again by
	// the front door, and every claim a route acts on is a field of this
	// identity (C1.10).
	if call.JobAccount != testJobSA {
		t.Errorf("the scope's caller was verified as %q, want the job account %q", call.JobAccount, testJobSA)
	}
	if call.JobClass != ClassRepository {
		t.Errorf("the scope's caller is of class %q, want %q for a workflow job of one repository", call.JobClass, ClassRepository)
	}
	// The canonical repository the pairing resolved, read the way the front
	// door reads it: the run identity's own slug through the CALLER's table
	// (Repos, D1066), never guessed from the slug's own text.
	if canonical, found := sc.Repos()[call.Slug]; !found || canonical != testRepo {
		t.Errorf("the scope's table resolves %q to %q/%v, want the job account's repository %q", call.Slug, canonical, found, testRepo)
	}
	if ja, found := s.server.jobs[call.JobAccount]; !found || ja.Slug != s.slug || ja.Repo != testRepo {
		t.Errorf("the deployment's row for %q is %+v/%v, want the configured job bound to %s", call.JobAccount, ja, found, testRepo)
	}

	// Every route of this service, and the one predicate the resolver shares
	// with the front door: a route answered before the shared parse is resolved
	// with no identity read at all.
	routes := []string{MessagesPath, CountTokensPath, MintPath, ContextTokenPath, SignPath, VerifyPath,
		BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, IssuePath, SlipPath, SlipClaimPath,
		PoolDesignatePath,
		WorkflowSecretsPath, BuildTokenPath, BuildCredentialsPath, WorkspaceKeepWarmPath,
		identitywire.TicketPath, identitywire.ClaimPath, identitywire.PairingPath, identitywire.DevicePath,
		identitywire.ProbePath,
		BitbucketProxyPrefix + "acme/aurora.git/info/refs"}
	for _, path := range routes {
		if !sc.Serves(path) {
			t.Errorf("the installation's scope does not serve %q, and an installation serves every route of the service", path)
		}
	}
	early := []string{VerifyPath, BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, BuildTokenPath,
		BuildCredentialsPath, IssuePath, SlipPath, PoolDesignatePath,
		identitywire.TicketPath, identitywire.ClaimPath, identitywire.PairingPath, identitywire.DevicePath,
		identitywire.ProbePath}
	for _, path := range early {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		got, err := s.server.resolver.Resolve(ctx, r)
		if err != nil {
			t.Errorf("the installation's resolver refused %q before it read an identity: %v", path, err)
			continue
		}
		if callerNamed(got.Caller()) {
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
	// The repository's own auth record is the CALLER's reader and a reader of
	// its own (C1's rule 2): an installation answers it with the store it has
	// always held, and a workspace answers it from the MEMBER's database and
	// never from the state the workspace owns.
	if sc.RepoAuth() != RepoAuthReader(s.server.cfg.State) {
		t.Error("the installation's scope does not answer the repository's auth record out of the deployment's own store")
	}
	if sc.Outcomes() != nil {
		t.Error("the installation's scope answered an outcomes reader over a database this fixture's patcher cannot read as a Getter")
	}

	// The offering: the bundle's own credentials, which the crafts and the
	// admissions then narrow on the model path, and the forge reads: a token
	// store this fixture's configuration wired none of, and the coverage the
	// installation's table answers.
	b, err := s.server.cfg.Bundles.Load(ctx)
	if err != nil {
		t.Fatalf("the fixture's bundle could not be loaded: %v", err)
	}
	offered, err := sc.Offering().Offering(ctx, b)
	if err != nil {
		t.Fatalf("the installation's offering answered an error for a caller the bundle serves: %v", err)
	}
	if len(offered) != len(b.Credentials()) {
		t.Errorf("the offering holds %d credentials and the bundle holds %d: an installation offers its bundle's own credentials and narrows them in the cascade, not here", len(offered), len(b.Credentials()))
	}
	if sc.Forge().Bitbucket() == nil {
		t.Error("the installation's forge reader answered no Bitbucket token store, and an installation that wired none answers the legacy selector EXPLICITLY rather than nil (C1's rule 10: nil means refuse in both modes)")
	}
	if covered, err := sc.Forge().Covers(ctx, testRepo); err != nil || !covered {
		t.Errorf("the installation's forge reader covers %s with %v/%v, want the repository its own table holds", testRepo, covered, err)
	}
	if covered, err := sc.Forge().Covers(ctx, "acme/no-such-repository"); err != nil || covered {
		t.Errorf("the installation's forge reader covers a repository its table does not hold with %v/%v, want none", covered, err)
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
		s := newSvcWith(t, func(_ *svc, cfg *Config) { cfg.Resolver = unservedResolver{} })
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
		// This scope verified no caller, so the refusal is the service's own and
		// writes no audit line: the rule is about a refusal taken with an
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

	// And the fence covers the WORKSPACE's own two headers (F13): a call that
	// presents the slip of the other deployment — the one a person's run holds
	// — is refused here for the same reason and with the same sentence, so a
	// run can never have one service read a slip the other granted and serve
	// the credential it names.
	saw := len(s.up.Seen())
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"a workspace slip id", creds.PersonSlipHeader},
		{"a workspace claim secret", creds.PersonSlipClaimHeader},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := s.callPath(t, MessagesPath, callBody("ws1-person-slip"), func(r *http.Request) {
				r.Header.Set(tc.value, "a-workspace-slip-this-service-did-not-grant")
			})
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("a call presenting %s to an installation's service = %d, want %d: %s", tc.name, resp.StatusCode, StatusUnverified, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
			} else if !strings.Contains(e.Message, "carries no slip") {
				t.Errorf("the refusal's sentence is %q, want the one that says a call to this service carries no slip", e.Message)
			}
			if got := len(s.up.Seen()); got != saw {
				t.Errorf("the upstream saw %d calls, want the %d it had seen: the call was refused before any handler", got, saw)
			}
		})
	}
}

// TestAScopeThatCannotBeBuiltIsTheServicesOwnFailure pins C1's third answer
// (ScopeFault) at the front door: a scope that could not be built for a reason
// that is NOT the caller's is answered as this service's own failure —
// StatusUnavailable with ErrorStateUnavailable — and never as a refusal of the
// caller, because a run's gateway reads a 403 as a missing invoke grant and
// halts the run on it (R16) while a node that could not be re-read is not a
// caller who is not admitted (D143: a refusal the run halts on, never a
// fallback).
//
// The audit rule is not suspended for it. A fault carries the identity the
// resolver had verified when it failed, and that identity gets the one line
// every refusal after a verification writes; a fault that carries none —
// because the resolver had verified nobody — is answered with no line, exactly
// as today's pre-identity refusals are.
func TestAScopeThatCannotBeBuiltIsTheServicesOwnFailure(t *testing.T) {
	t.Run("a fault that carries a verified caller writes its one line", func(t *testing.T) {
		s := newSvc(t)
		s.server.resolver = faultResolver{
			inner:      installationResolver{s: s.server},
			fault:      ScopeFault{What: "the workspace's node", Err: errors.New("the node could not be re-read")},
			withCaller: true,
		}
		resp, body := s.call(t, callBody("ws1-scope-fault"))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a call whose scope could not be built is answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		e := refusal(t, resp.StatusCode, body)
		if e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q: a fault is the service's own failure and never a verdict about the caller", e.Error, ErrorStateUnavailable)
		}
		if e.Error == ErrorUnverifiedRunIdentity || e.Error == ErrorUnverifiedJobIdentity {
			t.Error("the fault is written as a refusal of the caller's identity, which is a verification that was never made")
		}
		if len(s.up.Seen()) != 0 {
			t.Errorf("the upstream saw %d calls, want none: the scope was never built", len(s.up.Seen()))
		}
		ev := s.auditLines(t)
		if len(ev) != 1 {
			t.Fatalf("the fault wrote %d audit lines, want the one a fault with a verified caller writes: %v", len(ev), ev)
		}
		if got := ev[0]["status"]; got != ErrorStateUnavailable {
			t.Errorf("the fault's line is recorded as %v, want %q", got, ErrorStateUnavailable)
		}
		if got := ev[0]["caller"]; got != testJobSA {
			t.Errorf("the fault's line names the caller %v, want %s: the caller had been verified when the scope failed", got, testJobSA)
		}
	})

	t.Run("a fault that carries no caller writes no line", func(t *testing.T) {
		s := newSvc(t)
		s.server.resolver = faultResolver{
			inner: installationResolver{s: s.server},
			fault: ScopeFault{What: "the workspace's node", Err: errors.New("the node could not be re-read")},
		}
		resp, body := s.call(t, callBody("ws1-scope-fault-nocaller"))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a call whose scope could not be built is answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
		}
		if len(s.up.Seen()) != 0 {
			t.Errorf("the upstream saw %d calls, want none: the scope was never built", len(s.up.Seen()))
		}
		// No identity was verified, so there is nothing to record: the rule is
		// about a refusal taken with an identity in hand (audit.go).
		if lines := s.auditLines(t); len(lines) != 0 {
			t.Errorf("a fault that carries no verified caller wrote %d audit lines: %v", len(lines), lines)
		}
	})
}

// TestTheModelPathReadsTheScopesReadersAndNotTheDeployments is R1's guarantee
// itself, over one served model call: the policy, the kill switches and the
// readers this call is decided by are the SCOPE's, and the deployment's are
// read nowhere.
//
// This is the assertion every other test in this file is worth nothing without.
// An installation's scope answers exactly the deployment's own objects, so a
// model path that read Config directly — the shape the seam replaced — serves
// the same call, writes the same state and passes everything above this test.
// What tells the two apart is a scope whose readers are objects of the test's
// own: the call below is served from those and refused by the deployment's,
// which are tripwires that fail every read of them.
//
// It covers the model path's R1 reads: ServeHTTP's stop switches, its policy's
// shared node and the store the selector is built over; and candidates' policy
// (the admission) and the repository's recorded auth — which is read through
// the scope's OWN RepoAuth reader and never through its state (C1's rule 2),
// because a record read out of the wrong database is an ABSENT one, which
// narrowToRepoAuth reads as "the cascade, unchanged" (D2070 (a)) and would
// serve a member's run the whole of the workspace's pool.
func TestTheModelPathReadsTheScopesReadersAndNotTheDeployments(t *testing.T) {
	// The deployment's own, as tripwires: the switches and the policy fail every
	// read of them, so a path that reached one answers that read's refusal
	// instead of serving the call — and the state counts the reads of the
	// repository's record, which is the one read whose wrong answer is silent
	// (a record that is not found there means the cascade unchanged).
	deployKills := &fakeKills{err: errors.New("the deployment's stop switches were read")}
	deployPolicy := &fakePolicy{
		sharedErr: errors.New("the deployment's policy was read"),
		admErr:    errors.New("the deployment's policy was read"),
	}
	deployState := newCountingStore()
	s := newSvcWith(t, func(_ *svc, cfg *Config) {
		cfg.Kills = deployKills
		cfg.Policy = deployPolicy
		cfg.State = deployState
	})
	// The record is keyed by the slug the cascade reads it with (candidates'
	// id.Slug), and the deployment's copy says `vertex`: the one recorded auth
	// this service serves no call by at all (narrowToRepoAuth), so a path that
	// read it there would refuse this call.
	deployState.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: "vertex"})

	// The scope's own, as a workspace's would be: objects of their own,
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
		inner:    installationResolver{s: s.server},
		kills:    scopedKills,
		policy:   scopedPolicy,
		state:    scopedState,
		repoAuth: scopedState,
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

	// And the deployment's own were read nowhere — the repository's record
	// included, whose read the state counts because a wrong store's answer to
	// it is an absent record and not a failure.
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
	if got := deployState.repoAuthReads(); got != 0 {
		t.Errorf("the deployment's state was read for the repository's auth record %d times, want none: the record is the scope's own reader and never its state (C1's rule 2)", got)
	}
}

// ws1Pool is the pool the scope answers in the two tests below: a STRICT SUBSET
// of the fixture's bundle, which holds the default account, alice's, bob's and
// the project's paid key. It is the shape a workspace member's own shared node
// has (R8), and it is what a project's crafts must never widen.
func ws1Pool() poolOfferingWithout {
	return poolOfferingWithout{
		{Name: testPoolAccount, Tier: creds.TierA},
		{Name: config.AnthropicSecret, Tier: creds.TierB},
	}
}

// ws1Crafts is a project whose crafts go out on the pool of the given members,
// which is how a craft's candidate list enters the model path (poolOffering).
func ws1Crafts(members ...string) string {
	typed := make([]string, 0, len(members))
	for _, m := range members {
		typed = append(typed, `{"name": "`+m+`", "type": "claude-oauth"}`)
	}
	return `"credentials": {"fallback": "auto"},
		` + poolCraftBlock(strings.Join(typed, ",\n\t\t\t"), members...) + `,
		"subscription_spread": true`
}

// TestTheCraftsNeverWidenTheScopesPool is the crafts∩pool guard's own pin
// (candidates, heldBy, MC6): a project's crafts narrow the pool a call belongs
// to and never widen it.
//
// The scope's pool is a strict subset of the deployment's bundle, and the
// crafts name one credential that is inside it (bob) and one that is not
// (alice). Both facts the guard exists for are read off the one walk: alice is
// never offered to the call — and never serves it — and every credential the
// crafts and the pool disagree about is reported with the reason the run's halt
// reads, alice and the paid key the crafts do not name alike.
func TestTheCraftsNeverWidenTheScopesPool(t *testing.T) {
	s := poolSvc(t, ws1Crafts(testAccount, testPoolAccount))
	s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, offering: ws1Pool()}

	ctx := context.Background()
	b, err := s.server.cfg.Bundles.Load(ctx)
	if err != nil {
		t.Fatalf("the fixture's bundle could not be loaded: %v", err)
	}
	// The call is resolved by the scope's resolver and the candidate walk is
	// given that scope, which is the seam the guard is on: the pool is the
	// scope's answer and the crafts are the project's.
	req := httptest.NewRequest(http.MethodPost, MessagesPath, nil)
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	sc, err := s.server.resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("the resolver refused a call of its own run: %v", err)
	}
	cands, _, _, skipped, err := s.server.candidates(withScope(ctx, sc), sc.Caller(), testRepo, s.policy.shared, b)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != testPoolAccount {
		t.Fatalf("the call is offered %v, want only %q, the one credential the crafts and the pool both hold: the crafts name %q too, and they do not open a pool this call does not belong to",
			cands, testPoolAccount, testAccount)
	}
	reported := reportedNotAdmitted(t, skipped)
	for _, name := range []string{testAccount, config.AnthropicSecret} {
		if got, ok := reported[name]; !ok || got.Reason != ReasonNotAdmitted {
			t.Errorf("the credential %q is neither offered nor reported as not admitted (%+v): one of the two lists it was left out by has to say so", name, got)
		}
	}

	// And the call itself is served, by the pool's own member: a credential the
	// crafts name and the pool does not hold would have served it here.
	resp, body := s.call(t, callBody("ws1-crafts-pool"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the model call is answered %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testPoolAccount {
		t.Errorf("%s = %q, want %q: the crafts never widen the pool this call belongs to", HeaderCredential, got, testPoolAccount)
	}
}

// TestCraftsNamingNothingThePoolHoldsAreRefused is the other end of the same
// guard: when the crafts and the pool hold nothing in common the call has no
// candidate at all, and the refusal is the crafts' own sentence rather than the
// selector's, which answers an empty list with "no credential is offered" and
// sends an operator to the bundle for a disagreement that is between the
// published node and the pool.
func TestCraftsNamingNothingThePoolHoldsAreRefused(t *testing.T) {
	s := poolSvc(t, ws1Crafts(testAccount))
	s.server.resolver = resolverWithout{inner: installationResolver{s: s.server}, offering: ws1Pool()}

	resp, body := s.call(t, callBody("ws1-crafts-pool-empty"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, poolRefusalHead) {
		t.Errorf("the refusal is %q, want the crafts' own sentence (%q): what disagrees here is the published node and the pool this call belongs to", e.Message, poolRefusalHead)
	}
	if strings.Contains(e.Message, noCandidatesHead) {
		t.Errorf("the refusal is %q, and it is the sentence an empty candidate list gets: the bundle is not what left this call with nothing", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
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

// countingStore is a store of the test's own that counts the reads of its marks
// and of its repository auth records: the two reads two stores can differ by
// with no observable answer at all — a store with no due mark and a store
// nobody asked both answer "nothing is due", and a store with no record and one
// nobody asked both answer "the cascade, unchanged" (D2070 (a)) — so a test
// that pins WHICH store a call read has to count them.
type countingStore struct {
	*fakeStore
	mu        sync.Mutex
	marksR    int
	repoAuthR int
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

func (c *countingStore) RepoAuth(ctx context.Context, slug string) (creds.RepoAuth, bool, error) {
	c.mu.Lock()
	c.repoAuthR++
	c.mu.Unlock()
	return c.fakeStore.RepoAuth(ctx, slug)
}

// repoAuthReads is how many repository auth records it was asked for.
func (c *countingStore) repoAuthReads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.repoAuthR
}

// policySharedReads is how many times a policy of the test's own was asked for
// the project's shared node, which fakePolicy records under its own lock.
func policySharedReads(p *fakePolicy) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sharedReads
}

// faultResolver is a resolver whose scope could not be built: it answers C1's
// *ScopeFault for every call, which is the one answer of Resolve that is
// neither a scope nor a refusal of the caller.
//
// withCaller wraps the fault in the identity the inner resolver verified, which
// is the whole of the difference between a fault that is audited and one that
// is not (C1: a resolver that had verified a caller when its fault happened
// puts that caller in the fault, and one that had verified none leaves it nil).
type faultResolver struct {
	inner      ScopeResolver
	fault      ScopeFault
	withCaller bool
}

func (r faultResolver) Resolve(ctx context.Context, req *http.Request) (Scope, error) {
	if !r.withCaller {
		return nil, &ScopeFault{What: r.fault.What, Err: r.fault.Err}
	}
	sc, err := r.inner.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	id := sc.Caller()
	return nil, &ScopeFault{What: r.fault.What, Err: r.fault.Err, Caller: &id}
}

// unservedResolver is a deployment whose resolver serves no route and verifies
// nobody: the answer the front door's own gate is asked about, and the one the
// stub this package shipped while WS1 and WS2 were being written used to give
// (I1 deletes the stub and keeps the case).
//
// It is deliberately not a scope of its own. The gate may ask a scope it
// refuses two things and no more — whether it serves this route, and who the
// caller is — so the Scope embedded below is nil on purpose: a gate that reached
// a reader of a scope that serves nothing panics here rather than passing.
type unservedResolver struct{}

func (unservedResolver) Resolve(context.Context, *http.Request) (Scope, error) {
	return unservedScope{}, nil
}

type unservedScope struct{ Scope }

func (unservedScope) Serves(string) bool { return false }

func (unservedScope) Caller() Identity { return Identity{} }

// resolverWithout is an installation's resolver answering its scope with one of
// eight answers changed, and the two groups are the two things a scope can
// disagree with: the three the front door's own guards read (the path the scope
// does not serve, the caller it does not report, the slip it does not accept),
// each a deployment whose scope and whose front door disagree; and the five
// readers R1's seam is about (the policy, the kill switches, the state, the
// repository's own auth record — a reader OF ITS OWN and never a method of the
// state, C1's rule 2 — and the offering, which is the POOL a call belongs to),
// each answered from an object of the test's own where the test sets one, which
// is what makes a reader that read the deployment instead of the scope fail.
type resolverWithout struct {
	inner    ScopeResolver
	path     string
	noCaller bool
	noSlip   bool
	policy   PolicyReader
	kills    KillReader
	state    StateStore
	repoAuth RepoAuthReader
	offering OfferingReader
}

func (r resolverWithout) Resolve(ctx context.Context, req *http.Request) (Scope, error) {
	sc, err := r.inner.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	return scopeWithout{Scope: sc, path: r.path, noCaller: r.noCaller, noSlip: r.noSlip,
		policy: r.policy, kills: r.kills, state: r.state, repoAuth: r.repoAuth, offering: r.offering}, nil
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
	// policy, kills, state, repoAuth and offering are the scope's readers where
	// the test answers them from its own object, and nil where it does not: a nil
	// one answers the scope this wraps, so a test names only the readers its
	// claim is about.
	policy   PolicyReader
	kills    KillReader
	state    StateStore
	repoAuth RepoAuthReader
	offering OfferingReader
}

func (s scopeWithout) Serves(path string) bool { return path != s.path }

func (s scopeWithout) Caller() Identity {
	if s.noCaller {
		return Identity{}
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

func (s scopeWithout) State() StateStore {
	if s.state == nil {
		return s.Scope.State()
	}
	return s.state
}

func (s scopeWithout) RepoAuth() RepoAuthReader {
	if s.repoAuth == nil {
		return s.Scope.RepoAuth()
	}
	return s.repoAuth
}

func (s scopeWithout) Offering() OfferingReader {
	if s.offering == nil {
		return s.Scope.Offering()
	}
	return s.offering
}

// poolOfferingWithout is a call's pool answered by a test's own list rather than
// by the bundle: a workspace member's shared node, which is a subset of the
// installation's bundle and never more than it.
type poolOfferingWithout []creds.Credential

func (o poolOfferingWithout) Offering(_ context.Context, _ *Bundle) ([]creds.Credential, error) {
	return o, nil
}
