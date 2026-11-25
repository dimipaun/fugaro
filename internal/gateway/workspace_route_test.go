package gateway_test

// WS11, the run's side of the credential route: a run reads one record at its
// start and each kind of its credentials goes to one service for the whole run
// (D2981, D2982, D2989).
//
// **Why this file is in the external test package.** internal/creds imports
// internal/budget, which imports internal/gateway, so the gateway's own package
// cannot name a route's types: the import is a cycle the go tool refuses, and
// the plan's premise that the reader could live in internal/gateway is not one
// this tree can hold. The reading half is therefore the runner's
// (run.routeGateway, internal/runner/gateway.go), which reads the record with
// the contract's own reader and hands the gateway the facts it acts on: the
// address, and whether the model kind is served there. This file is what drives
// the gateway with exactly those facts, built from a record a member's own
// `fugaro update` would publish — an external test package is imported by
// nobody, so it may import internal/creds and hold the record and the gateway
// to each other.
//
// **What Holder decides and what it does not.** No test here re-implements the
// route: the vocabulary and the per-kind answer are the contract's
// (creds.Route.Holder), the bytes are the member's publisher's own writer
// (creds.RouteRecord), and they are read back by the run's own reader
// (creds.DecodeRoute). What the gateway is asked is what it does once it is
// told: which service a call reaches, what the call carries, and what happens
// when that service refuses.
//
// Nothing in this file is, or looks like, a credential: every identity is built
// from the test's own name (mirrorName).

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

const (
	// wsJob is the Cloud Run job name the run reports to either service. It is a
	// claim the service holds to the job-name rule and decides nothing from, and
	// it is not a secret.
	wsJob = mirrorJob
)

// wsUsage is what each fake service reports a served call cost.
var wsUsage = pricing.Usage{Input: 11, Output: 13}

// wsServiceOK is what a credentials service answers a served model call with:
// the model's own body and the two headers a service settles a call by — the
// tier it settled at and the credential's name (C1.10). An answer without them
// is one the gateway halts on, so both fakes here answer as the real one does.
func wsServiceOK(name string) anthropicfake.Reply {
	rep := anthropicfake.MessageOK(mirrorModel, wsUsage)
	rep.Header = http.Header{
		gateway.HeaderTier:       []string{gateway.TierA},
		gateway.HeaderCredential: []string{name},
	}
	return rep
}

// wsOpts is what one case varies about the run it drives.
type wsOpts struct {
	// route is the record the member's own `fugaro update` published, built from
	// the workspace service's address. A nil route is a repository with NO
	// record: the node is not there at all, which is today's run.
	route func(address string) creds.Route
	// projectEdit is applied to the run's own upstream before the gateway is
	// started: the run R18 is about is one that holds a slip and declares its
	// own auth, and both of those hang off this upstream.
	projectEdit func(*gateway.Upstream)
	// workspaceEdit is applied to the run's second upstream, the one the record
	// built: it is where the workspace's OWN slip hangs (WC8, R4), which is the
	// pair that travels under C1's two names and to that service alone.
	workspaceEdit func(*gateway.Workspace)
	// projectAnswer and workspaceAnswer override what each service answers; the
	// default is a served call naming one credential each.
	projectAnswer   func(*http.Request, []byte) anthropicfake.Reply
	workspaceAnswer func(*http.Request, []byte) anthropicfake.Reply
}

// wsRun is one run's gateway, built from one record the way the runner builds
// it: the project's own upstream as the run's, plus the second upstream the
// record's holders call for.
type wsRun struct {
	gw *gateway.Server
	// project and workspace are the two services a call can reach. Their counts
	// are what most of these tests ask: a call that reached the wrong one is the
	// failure each of them is about.
	project   *anthropicfake.Fake
	workspace *anthropicfake.Fake
	// projectAddress and address are the two services' URLs, which are also the
	// two audiences an identity token can be minted for.
	projectAddress string
	address        string
	// route is the record as the run read it, and second is whether the run
	// built a second upstream at all: a record that moves no kind is one the run
	// never calls, so it adds no upstream and keeps nothing warm
	// (contract-wire.md).
	route  creds.Route
	second bool
	// answers are the answers the gateway read a served call by.
	answers []gateway.ServiceAnswer

	mu        sync.Mutex
	audiences []string
}

// minted is the job identity this run presents to the workspace's service: an
// ID token the platform's metadata server is asked for, for the audience the
// caller names. It records the audience it was asked for, which is what
// TestTheWorkspaceUpstreamHasItsOwnAudience reads.
func (r *wsRun) minted(audience string) string {
	r.mu.Lock()
	r.audiences = append(r.audiences, audience)
	r.mu.Unlock()
	return "id-token-for-" + audience
}

// audiencesSeen is every audience this run minted an identity token for.
func (r *wsRun) audiencesSeen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.audiences...)
}

// wsMovesAKind reports whether a record serves any kind of this repository's
// credentials from the workspace.
//
// It is the runner's own first question (run.routeGateway), asked here for the
// same reason it is asked there: the run builds the second upstream on Holder
// and not on Address, so a record that carries an address and moves nothing is
// one the run never calls. The answer is the contract's Holder per kind — this
// loop decides nothing Holder does not — and the runner's own half, node and
// mistakes included, is pinned in internal/runner/gateway_test.go.
func wsMovesAKind(route creds.Route) bool {
	for _, kind := range creds.Kinds {
		if route.Holder(kind) == creds.HolderWorkspace {
			return true
		}
	}
	return false
}

// newWSRun drives one run: it publishes the record through the contract's own
// writer, reads it back through the run's own reader, and starts the gateway on
// the two facts the runner hands it.
func newWSRun(t *testing.T, o wsOpts) *wsRun {
	t.Helper()
	projectFake, projectSrv := anthropicfake.New(t)
	wsFake, wsSrv := anthropicfake.New(t)
	projectAnswer := o.projectAnswer
	if projectAnswer == nil {
		projectAnswer = func(*http.Request, []byte) anthropicfake.Reply { return wsServiceOK("repo-own-a") }
	}
	workspaceAnswer := o.workspaceAnswer
	if workspaceAnswer == nil {
		workspaceAnswer = func(*http.Request, []byte) anthropicfake.Reply { return wsServiceOK("ws-shared-a") }
	}
	projectFake.Func = projectAnswer
	wsFake.Func = workspaceAnswer

	r := &wsRun{project: projectFake, workspace: wsFake, projectAddress: projectSrv.URL, address: wsSrv.URL}

	var raw []byte
	if o.route == nil {
		// A repository with no published record: the node is not there.
		raw = []byte("null")
	} else {
		var err error
		raw, err = creds.RouteRecord(o.route(wsSrv.URL))
		if err != nil {
			t.Fatalf("the record a member's own update would publish was refused by its own writer: %v", err)
		}
	}
	route, found, err := creds.DecodeRoute(raw)
	if err != nil {
		t.Fatalf("the run refused the record the member's own publisher wrote: %v", err)
	}
	if want := o.route != nil; found != want {
		t.Fatalf("the record read back as found=%v, want %v", found, want)
	}
	r.route = route

	up := gateway.Upstream{
		Kind: gateway.KindService, BaseURL: projectSrv.URL,
		Service: &gateway.Service{
			Job:        wsJob,
			RunIDToken: func() string { return "run-identity-" + mirrorName(t) },
			IDToken:    func(context.Context) (string, error) { return "id-token-for-" + projectSrv.URL, nil },
		},
	}
	if o.projectEdit != nil {
		o.projectEdit(&up)
	}
	opts := gateway.Options{
		Upstream: up,
		Prices:   pricing.Embedded(),
		Mode:     gateway.Observe,
		Log:      slog.New(slog.DiscardHandler),
		OnServiceAnswer: func(a gateway.ServiceAnswer) {
			r.answers = append(r.answers, a)
		},
	}
	// The runner's two lines (run.routeGateway): a second upstream when the
	// record moves a kind to the workspace, and the record's own answer for the
	// model kind. The address is the record's and nothing else's — this test
	// file never reads one from the environment, which is the point of
	// TestTheAddressIsNeverReadFromTheEnvironment.
	if wsMovesAKind(route) {
		r.second = true
		opts.Workspace = &gateway.Workspace{
			Address:    route.Address,
			Job:        wsJob,
			RunIDToken: func() string { return "run-identity-" + mirrorName(t) },
			IDToken:    func(_ context.Context, audience string) (string, error) { return r.minted(audience), nil },
		}
		opts.ModelFromWorkspace = route.Holder(creds.KindModel) == creds.HolderWorkspace
		if o.workspaceEdit != nil {
			o.workspaceEdit(opts.Workspace)
		}
	}
	gw, err := gateway.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(gateway.Stage{Name: "implement", Model: mirrorModel})
	r.gw = gw
	return r
}

// wsAbsent fails when the service was sent any of these headers. It reads the
// request as received rather than the answer, because what a call carries is
// what the service decides on.
func wsAbsent(t *testing.T, got *http.Request, names ...string) {
	t.Helper()
	for _, name := range names {
		if v := got.Header.Get(name); v != "" {
			t.Errorf("the call carried %s = %q", name, v)
		}
	}
}

// wsServed is one model call through the gateway, with the status the agent
// saw.
func wsServed(t *testing.T, r *wsRun, extra http.Header) int {
	t.Helper()
	status, body := postMirrorWith(t, r.gw, extra)
	if status != http.StatusOK {
		t.Fatalf("the model call was answered %d: %s", status, body)
	}
	return status
}

// TestARunRoutedToTheWorkspaceNeverCallsTheProject: a repository whose model
// credential the record serves from the workspace makes every model call at the
// workspace's service, and its own project's service is never reached — the
// project holds no credential for this repository at all, so a call to it would
// be a call no one can serve.
func TestARunRoutedToTheWorkspaceNeverCallsTheProject(t *testing.T) {
	r := newWSRun(t, wsOpts{route: func(address string) creds.Route {
		return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
	}})
	if !r.second {
		t.Fatal("a record that moves the model kind built no second upstream")
	}
	wsServed(t, r, nil)

	if got := r.project.Count(); got != 0 {
		t.Errorf("the project's own service received %d calls, want none: a kind the record serves from the workspace never reaches it", got)
	}
	if got := r.workspace.Count(); got != 1 {
		t.Fatalf("the workspace's service received %d calls, want one", got)
	}
	// Which service answered is not only where the bytes went: the credential
	// the call was served by is the workspace's, and the agent was told so by
	// the tier the service settled at.
	if len(r.answers) != 1 || r.answers[0].Credential != "ws-shared-a" {
		t.Errorf("the run read the served call as %+v, want the workspace's own credential", r.answers)
	}
}

// TestAnExhaustedProjectDoesNotFallThrough: the route decides once, so an
// exhausted service is waited for rather than routed around (D2989). Both
// directions are here, because both are the same mistake seen from either end:
// a repository that holds its own credential waits on the project rather than
// spending the workspace's, and a repository that holds none waits on the
// workspace rather than being answered by a project that has nothing for it.
func TestAnExhaustedProjectDoesNotFallThrough(t *testing.T) {
	exhausted := `{"error":"exhausted_until","reset_at":1800000000000,` +
		`"tried":[{"name":"repo-own-a","tier":"A","reason":"exhausted"}]}`
	unavailable := func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusServiceUnavailable, Body: exhausted}
	}

	t.Run("the project's credential is exhausted", func(t *testing.T) {
		// The record moves the forge kind and leaves the model the project's, so
		// the run holds the workspace's address and still waits on its own
		// service: the second upstream exists and is not what the model call
		// falls back to.
		r := newWSRun(t, wsOpts{
			route: func(address string) creds.Route {
				return creds.Route{Version: creds.RouteVersion, Address: address, Bitbucket: creds.HolderWorkspace}
			},
			projectAnswer: unavailable,
		})
		if !r.second {
			t.Fatal("a record that moves the forge kind built no second upstream")
		}
		status, _ := postMirror(t, r.gw)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("the exhausted call was answered %d, want the wait (503)", status)
		}
		if detail := haltDetail(t, r.gw); detail != "" {
			t.Errorf("an exhausted call halted the run: %q", detail)
		}
		if got := r.workspace.Count(); got != 0 {
			t.Errorf("the workspace's service received %d calls, want none: the model kind is the project's, exhausted or not (D2989)", got)
		}
	})

	t.Run("the workspace's credential is exhausted", func(t *testing.T) {
		r := newWSRun(t, wsOpts{
			route: func(address string) creds.Route {
				return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
			},
			workspaceAnswer: unavailable,
		})
		status, _ := postMirror(t, r.gw)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("the exhausted call was answered %d, want the wait (503)", status)
		}
		if got := r.project.Count(); got != 0 {
			t.Errorf("the project's own service received %d calls, want none: the model kind is the workspace's, exhausted or not (D2989)", got)
		}
	})
}

// TestTheWorkspaceUpstreamHasItsOwnAudience: the identity the run presents to
// the workspace's service is minted for that service's own address and for
// nothing else. Cloud Run IAM checks the audience, so an identity minted for
// the wrong one is a call the service never receives — and the address it is
// minted for is the record's, which is what makes R3's "never the environment"
// a fact about the audience too.
func TestTheWorkspaceUpstreamHasItsOwnAudience(t *testing.T) {
	r := newWSRun(t, wsOpts{route: func(address string) creds.Route {
		return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
	}})
	wsServed(t, r, nil)

	if got := r.audiencesSeen(); len(got) != 1 || got[0] != r.address {
		t.Fatalf("the run minted an identity for %v, want exactly the record's address %q", got, r.address)
	}
	if r.address == r.projectAddress {
		t.Fatal("the two services share an address, so this test would pass with either audience")
	}
	seen := r.workspace.Seen()
	if len(seen) != 1 {
		t.Fatalf("the workspace's service received %d calls, want one", len(seen))
	}
	// The token that arrived is the one minted for that audience, which is what
	// makes the check above about the call and not only about a counter.
	if got, want := seen[0].Header.Get("Authorization"), "Bearer "+r.minted(r.address); got != want {
		t.Errorf("Authorization = %q, want the identity minted for the workspace's own address", got)
	}
	// The run's own identity travels with it, exactly as it does against the
	// member's own service (F30, O4): the workspace's service verifies the run
	// by the token that admitted it, and by the job name it reports.
	if got := seen[0].Header.Get(gateway.HeaderRunIdentity); got != "run-identity-"+mirrorName(t) {
		t.Errorf("%s = %q, want the run's own identity", gateway.HeaderRunIdentity, got)
	}
	if got := seen[0].Header.Get(gateway.HeaderRunJob); got != wsJob {
		t.Errorf("%s = %q, want %q", gateway.HeaderRunJob, got, wsJob)
	}
}

// TestTheAddressIsNeverReadFromTheEnvironment: an address a launcher can write
// is an address a launcher can move, and the run mints an identity token for
// whatever address it reads (R3, F32). So the address a run calls is the one
// its own record carries, and every name the environment could hold one under
// is inert: the name the runner reads its own project's service from, and a
// name a launcher would have to invent because no release reads one.
func TestTheAddressIsNeverReadFromTheEnvironment(t *testing.T) {
	envFake, envSrv := anthropicfake.New(t)
	envFake.Func = func(*http.Request, []byte) anthropicfake.Reply { return wsServiceOK("env-a") }
	for _, name := range []string{"FUGARO_CREDENTIALS_SERVICE_URL", "FUGARO_WORKSPACE_SERVICE_URL"} {
		t.Setenv(name, envSrv.URL)
	}

	r := newWSRun(t, wsOpts{route: func(address string) creds.Route {
		return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
	}})
	wsServed(t, r, nil)

	if got := envFake.Count(); got != 0 {
		t.Errorf("the service the environment named received %d calls, want none: the run's address is the one its record carries (R3)", got)
	}
	if got := r.workspace.Count(); got != 1 {
		t.Errorf("the record's address received %d calls, want one", got)
	}
	if got := r.audiencesSeen(); len(got) != 1 || got[0] != r.address {
		t.Errorf("the run minted an identity for %v, want exactly the record's address %q", got, r.address)
	}
}

// TestWorkspaceSlipMirrorsC1: the two header names the workspace's slip travels
// in are C1's own, one by one. internal/gateway cannot spell them through
// internal/creds — that package imports internal/budget, which imports this one
// — so they are declared twice on purpose, and this is what keeps the two
// spellings one (C1's own header comment asks for exactly this test; the shape
// is TestServiceConstantsMirrorCredsvc's).
//
// A drift here is silent in the worst way: the run's own claim would write the
// workspace's secret under one name and the gateway would present it under
// another, so every call would carry a slip header the workspace's service does
// not read — a call refused, or worse, read as no slip at all.
func TestWorkspaceSlipMirrorsC1(t *testing.T) {
	if gateway.WorkspaceSlipHeader != creds.PersonSlipHeader {
		t.Errorf("the workspace slip's header is %q in the gateway and %q in C1", gateway.WorkspaceSlipHeader, creds.PersonSlipHeader)
	}
	if gateway.WorkspaceSlipClaimHeader != creds.PersonSlipClaimHeader {
		t.Errorf("the claim secret's header is %q in the gateway and %q in C1", gateway.WorkspaceSlipClaimHeader, creds.PersonSlipClaimHeader)
	}
	if creds.PersonSlipHeader == credsvc.HeaderSlip || creds.PersonSlipClaimHeader == credsvc.HeaderSlipClaim {
		// R4 as a spelling: two slips that share a header name cannot be told
		// apart by the service that reads it.
		t.Errorf("a workspace slip header is spelled the same as the member's (%q, %q): the two services could not tell the two slips apart", creds.PersonSlipHeader, creds.PersonSlipClaimHeader)
	}
}

// TestEachSlipGoesToItsOwnService is WC8's headline (R4): a run holds two slips
// at once, and each is presented to the service that granted it and to no
// other. The member's goes on every call to the member's own service under the
// member's own names (credsvc.HeaderSlip and its claim secret), and the workspace's is never sent to the member's service either.
//
// Both directions are here because either one alone passes for a gateway that
// sent nothing anywhere: the call to the workspace's service is read for the
// workspace's pair, and the call to the member's own service — a call whose
// model kind the record leaves the project's — is read for the member's.
func TestEachSlipGoesToItsOwnService(t *testing.T) {
	member, memberClaim := "slip-"+mirrorName(t), "claim-"+mirrorName(t)
	wsSlip, wsClaim := "ws-slip-"+mirrorName(t), "ws-claim-"+mirrorName(t)

	t.Run("the workspace's own slip reaches the workspace's service", func(t *testing.T) {
		r := newWSRun(t, wsOpts{
			route: func(address string) creds.Route {
				return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
			},
			projectEdit: func(up *gateway.Upstream) {
				up.Service.SlipID, up.Service.SlipClaim = member, memberClaim
			},
			workspaceEdit: func(w *gateway.Workspace) { w.SlipID, w.SlipClaim = wsSlip, wsClaim },
		})
		wsServed(t, r, nil)

		seen := r.workspace.Seen()
		if len(seen) != 1 {
			t.Fatalf("the workspace's service received %d calls, want one", len(seen))
		}
		if got := seen[0].Header.Get(gateway.WorkspaceSlipHeader); got != wsSlip {
			t.Errorf("the workspace's service was sent %s = %q, want the workspace's own slip %q", gateway.WorkspaceSlipHeader, got, wsSlip)
		}
		if got := seen[0].Header.Get(gateway.WorkspaceSlipClaimHeader); got != wsClaim {
			t.Errorf("the workspace's service was sent %s = %q, want the claim secret the workspace answered", gateway.WorkspaceSlipClaimHeader, got)
		}
		// The member's slip and its secret are the other half, and this is the
		// whole point of R4: the workspace's service must not read them.
		wsAbsent(t, seen[0], gateway.HeaderSlip, gateway.HeaderSlipClaim)
	})

	t.Run("the member's slip reaches the member's own service alone", func(t *testing.T) {
		// The record moves the forge kind and leaves the model the project's, so
		// the model call is the member's own service's and the run holds both
		// slips at once (a run routed anywhere holds the workspace's).
		r := newWSRun(t, wsOpts{
			route: func(address string) creds.Route {
				return creds.Route{Version: creds.RouteVersion, Address: address, Bitbucket: creds.HolderWorkspace}
			},
			projectEdit: func(up *gateway.Upstream) {
				up.Service.SlipID, up.Service.SlipClaim = member, memberClaim
			},
			workspaceEdit: func(w *gateway.Workspace) { w.SlipID, w.SlipClaim = wsSlip, wsClaim },
		})
		wsServed(t, r, nil)

		seen := r.project.Seen()
		if len(seen) != 1 {
			t.Fatalf("the member's own service received %d calls, want one", len(seen))
		}
		if got := seen[0].Header.Get(gateway.HeaderSlip); got != member {
			t.Errorf("the member's own service was sent %s = %q, want the member's slip %q", gateway.HeaderSlip, got, member)
		}
		if got := seen[0].Header.Get(gateway.HeaderSlipClaim); got != memberClaim {
			t.Errorf("the member's own service was sent %s = %q, want the member's claim secret", gateway.HeaderSlipClaim, got)
		}
		// And the workspace's pair never travels there: a member's admins own
		// that service, and the workspace's slip is the workspace's own secret
		// (F13).
		wsAbsent(t, seen[0], gateway.WorkspaceSlipHeader, gateway.WorkspaceSlipClaimHeader)
	})
}

// TestARunWithNoWorkspaceSlipIsUnchanged: a run the workspace granted no slip
// behaves exactly as it did before that slip existed (R4's last clause). Its
// gateway is built with a Workspace and no slip on it — the state of every run
// of a member whose launcher holds no personal token, and of every run whose
// grant did not land (D3147) — and its calls to the workspace's service carry
// no slip of either pair.
//
// The positive half is here too, because "no slip" is also what a gateway that
// sent nothing anywhere would look like: this run's own service is told the
// member's slip, so the absence above is about the workspace's service and not
// about a run that dropped every claim.
//
// The agent writes a slip of its own on the call, under both pairs of names:
// this run holds no workspace slip that would displace it, so what keeps an
// invented one off the wire is the drop of the caller's own headers and nothing
// else.
func TestARunWithNoWorkspaceSlipIsUnchanged(t *testing.T) {
	member, memberClaim := "slip-"+mirrorName(t), "claim-"+mirrorName(t)
	agent := http.Header{}
	agent.Set(gateway.WorkspaceSlipHeader, "ws-slip-the-agent-invented")
	agent.Set(gateway.WorkspaceSlipClaimHeader, "ws-claim-the-agent-invented")
	r := newWSRun(t, wsOpts{
		route: func(address string) creds.Route {
			return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
		},
		projectEdit: func(up *gateway.Upstream) {
			up.Service.SlipID, up.Service.SlipClaim = member, memberClaim
		},
	})
	wsServed(t, r, agent)

	seen := r.workspace.Seen()
	if len(seen) != 1 {
		t.Fatalf("the workspace's service received %d calls, want one", len(seen))
	}
	wsAbsent(t, seen[0], gateway.WorkspaceSlipHeader, gateway.WorkspaceSlipClaimHeader, gateway.HeaderSlip, gateway.HeaderSlipClaim)

	own := newWSRun(t, wsOpts{
		route: func(address string) creds.Route {
			return creds.Route{Version: creds.RouteVersion, Address: address, Bitbucket: creds.HolderWorkspace}
		},
		projectEdit: func(up *gateway.Upstream) {
			up.Service.SlipID, up.Service.SlipClaim = member, memberClaim
		},
	})
	wsServed(t, own, nil)
	seen = own.project.Seen()
	if len(seen) != 1 || seen[0].Header.Get(gateway.HeaderSlip) != member {
		t.Fatalf("the run's own service was sent %d calls with %s = %q, want the member's slip: this test says nothing about the workspace unless the run really holds one",
			len(seen), gateway.HeaderSlip, seen[0].Header.Get(gateway.HeaderSlip))
	}
}

// TestTheWorkspaceUpstreamCarriesNoMembersSlip: a call to the workspace's
// service carries no slip a member's own service granted, and no auth-mode or
// account header (R18, R4). The member's slip is the member's own grant: the
// workspace's scope refuses a call that presents one, and the declaration of an
// auth mode and account is a statement about a repository the workspace already
// decides for itself.
//
// The slip it DOES carry is the workspace's own, under C1's two names, which is
// TestEachSlipGoesToItsOwnService's subject: what this test holds is that the
// member's pair never appears beside it. The run here holds a member's slip,
// declares its own auth and is given the workspace's slip; the agent sends a
// slip of its own as well: only the workspace's pair reaches the workspace.
func TestTheWorkspaceUpstreamCarriesNoMembersSlip(t *testing.T) {
	member, memberClaim := "slip-"+mirrorName(t), "claim-"+mirrorName(t)
	wsSlip, wsClaim := "ws-slip-"+mirrorName(t), "ws-claim-"+mirrorName(t)
	edit := func(up *gateway.Upstream) {
		up.Service.SlipID, up.Service.SlipClaim = member, memberClaim
		up.AuthMode, up.Account = "oauth", config.DefaultClaudeAccount
	}
	route := func(address string) creds.Route {
		return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
	}
	// What the agent writes on the call it makes: a slip of its own — under
	// both pairs of names, since it can spell either — and a declaration of its
	// own, which must not arrive as the runner's statement about this run.
	agent := http.Header{}
	agent.Set(gateway.HeaderSlip, "slip-the-agent-invented")
	agent.Set(gateway.HeaderSlipClaim, "claim-the-agent-invented")
	agent.Set(gateway.WorkspaceSlipHeader, "ws-slip-the-agent-invented")
	agent.Set(gateway.WorkspaceSlipClaimHeader, "ws-claim-the-agent-invented")
	agent.Set(gateway.HeaderAuthMode, "api-key")
	agent.Set(gateway.HeaderAuthAccount, "an-account-the-agent-named")

	r := newWSRun(t, wsOpts{
		route:         route,
		projectEdit:   edit,
		workspaceEdit: func(w *gateway.Workspace) { w.SlipID, w.SlipClaim = wsSlip, wsClaim },
	})
	wsServed(t, r, agent)

	seen := r.workspace.Seen()
	if len(seen) != 1 {
		t.Fatalf("the workspace's service received %d calls, want one", len(seen))
	}
	wsAbsent(t, seen[0], gateway.HeaderSlip, gateway.HeaderSlipClaim, gateway.HeaderAuthMode, gateway.HeaderAuthAccount,
		gateway.HeaderTier, gateway.HeaderCredential, gateway.HeaderSwitch, gateway.HeaderProbe)
	// The pair that does travel is the workspace's own, and it is the run's
	// claim rather than either value the agent wrote beside it.
	if got := seen[0].Header.Get(gateway.WorkspaceSlipHeader); got != wsSlip {
		t.Errorf("the workspace's service was sent %s = %q, want the workspace's own slip: the agent's own value must not arrive in its place", gateway.WorkspaceSlipHeader, got)
	}
	if got := seen[0].Header.Get(gateway.WorkspaceSlipClaimHeader); got != wsClaim {
		t.Errorf("the workspace's service was sent %s = %q, want the claim secret the run holds", gateway.WorkspaceSlipClaimHeader, got)
	}

	// The contrast, so that "the member's slip does not travel" is about the
	// workspace and not about a run that dropped its slip everywhere: the same
	// run's own service, reached for a kind the record leaves the project's, is
	// told the slip the run holds and the auth it declared (DS1.5, D2070 (a)).
	own := newWSRun(t, wsOpts{route: func(address string) creds.Route {
		return creds.Route{Version: creds.RouteVersion, Address: address, Bitbucket: creds.HolderWorkspace}
	}, projectEdit: edit, workspaceEdit: func(w *gateway.Workspace) { w.SlipID, w.SlipClaim = wsSlip, wsClaim }})
	wsServed(t, own, nil)
	seen = own.project.Seen()
	if len(seen) != 1 {
		t.Fatalf("the project's own service received %d calls, want one", len(seen))
	}
	if got := seen[0].Header.Get(gateway.HeaderSlip); got != member {
		t.Errorf("the member's own service was sent %s = %q, want the slip the run holds (%q)", gateway.HeaderSlip, got, member)
	}
	if got := seen[0].Header.Get(gateway.HeaderAuthMode); got != "oauth" {
		t.Errorf("the member's own service was sent %s = %q, want the declaration the run resolved", gateway.HeaderAuthMode, got)
	}
}

// TestAMissingInvokeGrantNamesTheFix: a platform 403 from the workspace's
// service is the workspace's own front door refusing this job, and no run and
// no operator of the member's repository can grant it — the workspace owner's
// own `fugaro update` in the workspace's project is what does (R16). The run
// halts at once, and the reason names that step.
func TestAMissingInvokeGrantNamesTheFix(t *testing.T) {
	// What a platform answers a caller it never admitted: a body that is not
	// this contract's refusal and not the model API's envelope either.
	refused := func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusForbidden, Body: "Forbidden: the caller does not have permission"}
	}
	r := newWSRun(t, wsOpts{
		route: func(address string) creds.Route {
			return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
		},
		workspaceAnswer: refused,
	})
	if status, _ := postMirror(t, r.gw); status != http.StatusForbidden {
		t.Fatalf("a refused call was answered %d, want the halt (403)", status)
	}
	detail := haltDetail(t, r.gw)
	if detail == "" {
		t.Fatal("a platform 403 from the workspace's service did not halt the run")
	}
	if !strings.Contains(detail, "fugaro update") {
		t.Errorf("the halt does not name the workspace owner's own 'fugaro update': %q", detail)
	}
	if got := r.project.Count(); got != 0 {
		t.Errorf("the project's own service received %d calls after the halt, want none", got)
	}

	// Which service answered is what decides the fix: the member's own job,
	// refused by its own project's service, is the member's own invoker grant,
	// and a reason that named the workspace owner's `fugaro update` there would
	// send an operator to a project that has nothing to do with it.
	own := newWSRun(t, wsOpts{
		route:         func(address string) creds.Route { return creds.Route{Version: creds.RouteVersion, Address: address} },
		projectAnswer: refused,
	})
	if status, _ := postMirror(t, own.gw); status != http.StatusForbidden {
		t.Fatalf("a refused call was answered %d, want the halt (403)", status)
	}
	ownDetail := haltDetail(t, own.gw)
	if ownDetail == "" {
		t.Fatal("a platform 403 from the project's own service did not halt the run")
	}
	if strings.Contains(ownDetail, "fugaro update") {
		t.Errorf("the halt on the member's own service names the workspace owner's step: %q", ownDetail)
	}
	if !strings.Contains(ownDetail, "invoker grant") {
		t.Errorf("the halt on the member's own service does not name its own invoker grant: %q", ownDetail)
	}
}

// TestAnUnlistedRepositoryHaltsNamingTheFix: the workspace's scope refuses a
// repository its record does not name, and the refusal is the same class of
// halt as the front door's — the run stops at once and the reason names the
// workspace owner's own `fugaro update`, which is what adds the repository
// (R19, F17).
func TestAnUnlistedRepositoryHaltsNamingTheFix(t *testing.T) {
	// The refusal the workspace's scope writes (C1's vocabulary, WS2's writer).
	// It is spelled here because this release carries the reader and not the
	// writer: a scope that writes another word is a change to this test.
	const notListed = "repository_not_listed"
	r := newWSRun(t, wsOpts{
		route: func(address string) creds.Route {
			return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
		},
		workspaceAnswer: func(*http.Request, []byte) anthropicfake.Reply {
			return anthropicfake.Reply{Status: http.StatusForbidden, Body: `{"error":"` + notListed + `"}`}
		},
	})
	if status, _ := postMirror(t, r.gw); status != http.StatusForbidden {
		t.Fatalf("the refusal was answered %d, want the halt (403)", status)
	}
	detail := haltDetail(t, r.gw)
	if detail == "" {
		t.Fatal("the scope's refusal of an unlisted repository did not halt the run")
	}
	if !strings.Contains(detail, notListed) {
		t.Errorf("the halt does not name the refusal (%s): %q", notListed, detail)
	}
	if !strings.Contains(detail, "fugaro update") {
		t.Errorf("the halt does not name the workspace owner's own 'fugaro update': %q", detail)
	}
	if got := r.project.Count(); got != 0 {
		t.Errorf("the project's own service received %d calls after the halt, want none", got)
	}
}

// TestNoRecordIsTodaysRun: a repository with no record is served by its own
// project's service alone, which is exactly what every run did before the
// record existed. The second upstream is built on Holder and not on Address, so
// the address never being called is a fact about the record and not about the
// count of kinds it happens to move.
func TestNoRecordIsTodaysRun(t *testing.T) {
	t.Run("no record at all", func(t *testing.T) {
		r := newWSRun(t, wsOpts{})
		if r.route != (creds.Route{}) {
			t.Fatalf("a repository with no record read %+v, want the zero route", r.route)
		}
		if r.second {
			t.Fatal("a run with no record built a second upstream")
		}
		wsServed(t, r, nil)
		if got := r.project.Count(); got != 1 {
			t.Errorf("the project's own service received %d calls, want one", got)
		}
		if got := r.workspace.Count(); got != 0 {
			t.Errorf("a service was reached with no record at all, %d times", got)
		}
		if len(r.audiencesSeen()) != 0 {
			t.Errorf("a run with no record minted an identity for %v", r.audiencesSeen())
		}
	})

	t.Run("a record that moves nothing", func(t *testing.T) {
		// A member in a workspace whose own bundle covers this repository: the
		// record carries an address and names every kind the project's, and
		// Holder answers HolderProject for each of them.
		r := newWSRun(t, wsOpts{route: func(address string) creds.Route {
			return creds.Route{Version: creds.RouteVersion, Address: address}
		}})
		if r.route.Address == "" {
			t.Fatal("the record carried no address, so this case would be the one above")
		}
		if r.second {
			t.Fatal("a record that moves no kind built a second upstream: the run builds it on Holder and not on Address")
		}
		wsServed(t, r, nil)
		if got := r.project.Count(); got != 1 {
			t.Errorf("the project's own service received %d calls, want one", got)
		}
		if got := r.workspace.Count(); got != 0 {
			t.Errorf("the address the record carries was reached %d times, want none: nothing is served from it", got)
		}
		if len(r.audiencesSeen()) != 0 {
			t.Errorf("a run that moves no kind minted an identity for %v", r.audiencesSeen())
		}
	})
}

// wsValidRoute is the second upstream a run builds from a record, before a case
// below breaks one thing about it: every field Workspace.validate requires,
// holding no identity of the member's (R18) — the job's identity, the run's own
// token, and the job's name, which is a claim and not a secret.
func wsValidRoute(t *testing.T) *gateway.Workspace {
	t.Helper()
	return &gateway.Workspace{
		Address:    "https://fugaro-workspace-credentials.example.invalid",
		Job:        wsJob,
		IDToken:    func(context.Context, string) (string, error) { return "id-token-" + mirrorName(t), nil },
		RunIDToken: func() string { return "run-identity-" + mirrorName(t) },
	}
}

// TestTheGatewayRefusesAWorkspaceUpstreamItCannotCallSafely: the gateway's own
// check of the second upstream, which is what still holds when Options is built
// by something other than the runner that read the record — the runner's own
// reader refuses these first (creds.DecodeRoute checks the address, the
// reader's own half), and a gateway that leaned on that would be a gateway
// whose rule lives in its caller.
//
// The address is the one that matters most: a routed call carries the run's own
// token, so an upstream that is not a scheme and a host, or that travels in the
// clear off loopback, is a token sent where nobody chose. The rest are the
// identity the workspace's service is called by and the two facts a call
// presents, and a run missing one of them is a run whose every call would be
// refused one at a time instead of at its start.
//
// The first case is the control: the same Options with nothing broken starts,
// so a table of refusals is not a table that refuses everything.
func TestTheGatewayRefusesAWorkspaceUpstreamItCannotCallSafely(t *testing.T) {
	base := func() gateway.Options {
		return gateway.Options{
			Upstream: gateway.Upstream{Kind: "anthropic"},
			Prices:   pricing.Embedded(),
			Mode:     gateway.Observe,
			Log:      slog.New(slog.DiscardHandler),
			// What the runner hands over for a record that serves the model
			// kind from the workspace: the second upstream, and the answer
			// that the model calls are its.
			Workspace:          wsValidRoute(t),
			ModelFromWorkspace: true,
		}
	}
	tests := []struct {
		name string
		edit func(*gateway.Options)
		// want is what the refusal must say; the empty string is the control
		// case, which must start.
		want string
	}{
		{
			name: "the record's upstream, unbroken",
		},
		{
			name: "an empty address",
			edit: func(o *gateway.Options) { o.Workspace.Address = "" },
			want: "the workspace upstream has no address",
		},
		{
			name: "http on a host that is not loopback",
			edit: func(o *gateway.Options) {
				o.Workspace.Address = "http://fugaro-workspace-credentials.example.invalid"
			},
			want: "must be https, or http on a loopback address",
		},
		{
			name: "an address that carries a path",
			edit: func(o *gateway.Options) {
				o.Workspace.Address = "https://fugaro-workspace-credentials.example.invalid/creds"
			},
			want: "must be a scheme and a host only",
		},
		{
			name: "no job identity",
			edit: func(o *gateway.Options) { o.Workspace.IDToken = nil },
			want: "the workspace upstream has no job identity",
		},
		{
			name: "no run identity",
			edit: func(o *gateway.Options) { o.Workspace.RunIDToken = nil },
			want: "the workspace upstream has no run identity",
		},
		{
			name: "no job name",
			edit: func(o *gateway.Options) { o.Workspace.Job = "" },
			want: "the workspace upstream reports no Cloud Run job name",
		},
		{
			// The workspace's slip is what a run the workspace granted one
			// carries for the model path (WC8), and upstream() is the one
			// place the pair is set, so half of one is a mistake in code —
			// refused here, where it is, rather than on every call the run
			// makes afterwards.
			name: "half the workspace's slip",
			edit: func(o *gateway.Options) { o.Workspace.SlipID = "a-workspace-slip-id" },
			want: "the workspace upstream carries half its slip",
		},
		{
			name: "a route that serves the model from the workspace with no upstream for it",
			edit: func(o *gateway.Options) { o.Workspace = nil },
			want: "serves the model from the workspace and no workspace upstream was built for it",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := base()
			if tc.edit != nil {
				tc.edit(&opts)
			}
			gw, err := gateway.Start(context.Background(), opts)
			if gw != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = gw.Close(ctx)
			}
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Start refused the run's own route: %v", err)
				}
				if gw == nil {
					t.Fatal("Start answered no server and no error")
				}
				return
			}
			if err == nil {
				t.Fatalf("Start accepted a workspace upstream with %s: a routed call carries the run's own token, so this is a call made before anything decided it could be", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the fault (%q): %v", tc.want, err)
			}
		})
	}
}
