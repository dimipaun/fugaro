package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// fakeWorkspaceAddress is the address a fixture publishes in the repository's
// credential-route record: the shape a workspace's own service is deployed at
// (https, a host, no path), which is what creds.RouteRecord's own writer admits
// and what checkSlipServiceURL would send a launcher's identity token to. No
// test reaches it — the grant call is recorded, not made — and the one test
// that does make the call runs its own service.
const fakeWorkspaceAddress = "https://fugaro-workspace-credentials-abcde-uc.a.run.app"

// personalAccountJSON is what the fixtures' gcloud answers for `config list`: a
// person's own login, the account a launch asks the workspace for a grant as.
const personalAccountJSON = `{"core":{"account":"someone@example.com"}}`

// personalLaunchRig is one launch of a member whose repository's model route is
// the workspace: the member's own database with the published policy and the
// route record, the gcloud the launcher reads its account from, and the grant
// call recorded rather than made.
type personalLaunchRig struct {
	f    *launchBudgetFixture
	env  *cloudEnv
	rec  *envRecorder
	spec *task.Spec
	// warned is what the launch printed: the one line about the person's own
	// token, whichever of D3147's answers it got.
	warned *strings.Builder
	// gcloud is the stub of the launcher's own mint, which records every
	// invocation: a launch that asks for no grant must run none of them.
	gcloud *personGcloudStub
	// url, req and calls are the grant call: where it went, what it asked and
	// how many times it was made.
	url   string
	req   creds.PersonGrantRequest
	calls int
}

// newPersonalLaunchRig is the rig over a member whose route record moves the
// model kind to the workspace (route) and whose published policy carries R9's
// switch (on). route is written as the record itself, so a test can publish one
// that moves another kind or names the project, and the zero Route writes none
// at all.
func newPersonalLaunchRig(t *testing.T, route creds.Route, on bool) *personalLaunchRig {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, personalLaunchPolicy(on))
	if route != (creds.Route{}) {
		raw, err := creds.RouteRecord(route)
		if err != nil {
			t.Fatalf("the route this fixture publishes is not one the member's own update could write: %v", err)
		}
		f.db.Set(creds.RoutePath(appSlug), json.RawMessage(raw))
	}
	env := memEnv(t, f.cloudFixture)
	rec := &envRecorder{Backend: env.be}
	env.be = rec
	var warned strings.Builder
	prevWarn := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prevWarn })
	g := (&personGcloudStub{configOut: personalAccountJSON, tokenOut: fakeLauncherToken}).use(t)
	return &personalLaunchRig{f: f, env: env, rec: rec, spec: raceSpec(t, env), warned: &warned, gcloud: g}
}

// launch runs the fixture's launch with the grant call answered by answer, and
// returns the launch's plain environment without the services stamp. A nil
// answer leaves the real call in place, which is what the test of the call
// itself asks for.
func (r *personalLaunchRig) launch(t *testing.T, answer func(creds.PersonGrantRequest) (creds.PersonGrant, error)) []string {
	t.Helper()
	if answer != nil {
		prev := personGrant
		personGrant = func(_ context.Context, url string, req creds.PersonGrantRequest) (creds.PersonGrant, error) {
			r.url, r.req, r.calls = url, req, r.calls+1
			return answer(req)
		}
		t.Cleanup(func() { personGrant = prev })
	}
	if _, err := launchRun(context.Background(), r.env, appSlug, r.spec, time.Now()); err != nil {
		t.Fatal(err)
	}
	return withoutServicesStamp(t, r.rec.env)
}

// personalGrantHeld is the answer a workspace gives a person who holds the
// token the call asked about: the id of the slip bound to it.
func personalGrantHeld(creds.PersonGrantRequest) (creds.PersonGrant, error) {
	return creds.PersonGrant{Held: true, Slip: fakeSlipID}, nil
}

// personalLaunchPolicy is the member's published policy carrying R9's switch:
// the field WC6 publishes, the member's own machine reads here, and the
// workspace's service is the only enforcer of (C1's PersonSwitchKey). It is an
// ordinary policy with no routing block, so the launch stays the non-cascade
// one of a repository whose own file asked for nothing.
func personalLaunchPolicy(on bool) string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"personal_workspace_tokens":%t}`, projectpolicy.Version, on)
}

// personalRouteRecord is the record the member's own `fugaro update` publishes
// for a repository whose model kind comes from the workspace.
func personalRouteRecord(address string) creds.Route {
	return creds.Route{Version: creds.RouteVersion, Address: address, Model: creds.HolderWorkspace}
}

// TestNoGrantIsAskedOnAProjectRoute: R8's first condition is the ROUTE (D3141),
// and the model kind's holder alone decides it. A project that holds its own
// model credential shadows every person's workspace token, so a launch of it
// asks the workspace for nothing — no call, no entry, no line — and neither
// does a member whose switch is off (R9), nor a repository that published no
// record at all (every kind the project's).
//
// The red step of this task is here: while the grant is asked unconditionally,
// the first case below reaches the recorded call and this fails on it.
//
// Mutation (run, restored): drop the Holder(creds.KindModel) gate from
// runPersonalEnv and the first two cases fail on the call they made.
func TestNoGrantIsAskedOnAProjectRoute(t *testing.T) {
	for _, c := range []struct {
		name  string
		route creds.Route
		on    bool
	}{
		{"the record names the project for the model kind",
			creds.Route{Version: creds.RouteVersion, Address: fakeWorkspaceAddress, Model: creds.HolderProject}, true},
		{"the record moves another kind, not the model",
			creds.Route{Version: creds.RouteVersion, Address: fakeWorkspaceAddress, Bitbucket: creds.HolderWorkspace}, true},
		{"no record was published at all", creds.Route{}, true},
		{"the member's switch is off", personalRouteRecord(fakeWorkspaceAddress), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newPersonalLaunchRig(t, c.route, c.on)
			plain := r.launch(t, func(req creds.PersonGrantRequest) (creds.PersonGrant, error) {
				t.Errorf("the launch asked the workspace for a grant %+v, and this repository's model route is not the workspace's: a project that keeps its own model credential shadows every person's token, and the switch is what a member turns the personal half on with (D3141, R9)", req)
				return personalGrantHeld(req)
			})
			if r.calls != 0 {
				t.Errorf("the launch made %d grant calls, want none", r.calls)
			}
			if len(plain) != 0 {
				t.Errorf("the launch handed the job %v, want no entry: nothing was granted", plain)
			}
			if r.warned.Len() != 0 {
				t.Errorf("the launch said %q, want nothing: a repository this plan does not reach launches what it launched before it did, line for line", r.warned.String())
			}
			if len(r.gcloud.args) != 0 {
				t.Errorf("the launch ran gcloud %v, want no invocation at all: the account is read after the two conditions, and a launch that fails them asks for no grant", r.gcloud.args)
			}
		})
	}
}

// TestNoTokenHeldStartsTheRunAsBefore: "no token held" is an ANSWER and not a
// refusal (R8, D3147). The workspace says the caller holds no token of the name
// it asked about, the run is served the shared entries exactly as before this
// plan, and the launch says nothing about it: a person who never stored a token
// launches the way they always did, and a line would be news about nothing.
//
// Mutation (run, restored): read Held false as a refusal — print the failure
// line — and this fails on what the launch said. Pass a second, wrong account
// at run.go's call site (the slug) and it fails on the account the call named,
// which is what a non-empty account would have read as an answer.
func TestNoTokenHeldStartsTheRunAsBefore(t *testing.T) {
	r := newPersonalLaunchRig(t, personalRouteRecord(fakeWorkspaceAddress), true)
	plain := r.launch(t, func(creds.PersonGrantRequest) (creds.PersonGrant, error) {
		return creds.PersonGrant{Held: false}, nil
	})
	if r.calls != 1 {
		t.Fatalf("the launch made %d grant calls, want exactly one: the route is the workspace's and the switch is on", r.calls)
	}
	if r.url != fakeWorkspaceAddress {
		t.Errorf("the grant call went to %q, want the address the repository's own route record carries (%q)", r.url, fakeWorkspaceAddress)
	}
	if r.req.Slug != appSlug || r.req.Run != r.spec.RunID {
		t.Errorf("the grant call named %s/%s, want this launch's run (%s/%s)", r.req.Slug, r.req.Run, appSlug, r.spec.RunID)
	}
	if r.req.Name != creds.PersonTokenName {
		t.Errorf("the grant call asked about %q, want the one personal name this plan serves (%q)", r.req.Name, creds.PersonTokenName)
	}
	// The account is this launch's own, SPELLED OUT rather than read back from
	// the launch that would be checked: the workspace holds the grant to the
	// account the execution runs as, so an account that is merely non-empty is
	// an account the service may bind the slip to and the run never claim
	// (R3, D1937). It is the same string the run's slip names, which is why one
	// derivation serves both.
	wantAccount := gcp.ServiceAccountID(appSlug, "web") + "@" + r.env.lc.GCPProject + ".iam.gserviceaccount.com"
	if r.req.Account != wantAccount {
		t.Errorf("the grant call named the account %q, want this launch's own (%q): the workspace holds the grant to the account the execution will call as, and a slip bound to any other is one this run can never claim (R3)", r.req.Account, wantAccount)
	}
	if len(plain) != 0 {
		t.Errorf("the launch handed the job %v, want no entry: no token is held, so there is no slip", plain)
	}
	if r.warned.Len() != 0 {
		t.Errorf("the launch said %q, want nothing: no token held is an answer and not a failure, and the run starts on the shared entries exactly as before (R8)", r.warned.String())
	}
}

// TestTheFlagSkipsTheGrant: --no-personal-token is D3142's way out of drawing
// on the person's own subscription, and it is read where the grant would be
// asked for: the flagged launch neither reads the route, nor mints, nor calls,
// and it says nothing. The same installation without the flag reaches all
// three, which is what makes the first half a statement about the flag rather
// than about a fixture that never asks.
//
// Mutation (run, restored): drop the `!env.noPersonalToken` guard at run.go's
// call site and the flagged launch asks for the grant.
func TestTheFlagSkipsTheGrant(t *testing.T) {
	one := func(t *testing.T, flagged bool) (int, string) {
		t.Helper()
		f := newLaunchBudget(t, "enforce")
		publishPolicy(t, f, personalLaunchPolicy(true))
		raw, err := creds.RouteRecord(personalRouteRecord(fakeWorkspaceAddress))
		if err != nil {
			t.Fatal(err)
		}
		f.db.Set(creds.RoutePath(appSlug), json.RawMessage(raw))
		(&personGcloudStub{configOut: personalAccountJSON, tokenOut: fakeLauncherToken}).use(t)
		var said strings.Builder
		prevWarn := launchWarn
		launchWarn = &said
		t.Cleanup(func() { launchWarn = prevWarn })
		n := 0
		prev := personGrant
		personGrant = func(_ context.Context, _ string, req creds.PersonGrantRequest) (creds.PersonGrant, error) {
			n++
			return personalGrantHeld(req)
		}
		t.Cleanup(func() { personGrant = prev })

		args := []string{"run", "--repo", "acme/app", "--run-id", budgetRun, "Add a feature"}
		if flagged {
			args = append(args, "--no-personal-token")
		}
		if _, _, err := execute(t, args...); err != nil {
			t.Fatal(err)
		}
		return n, said.String()
	}

	if calls, warned := one(t, true); calls != 0 {
		t.Errorf("`fugaro run --no-personal-token` made %d grant calls, want none: the flag is the whole of D3142's way out of a person spending their own subscription", calls)
	} else if warned != "" {
		t.Errorf("the flagged launch said %q, want nothing: it asked the workspace for nothing and has nothing to report", warned)
	}

	// And the control, so the assertion above is about the flag: the same
	// launch without it is one that does draw on the person's token.
	calls, warned := one(t, false)
	if calls != 1 {
		t.Fatalf("the same launch without the flag made %d grant calls, want one: this fixture is the state the flag is a way out of, and this test would otherwise pass over nothing", calls)
	}
	if !strings.Contains(warned, "--no-personal-token") {
		t.Errorf("the launch that did draw on the person's token said %q, want one line naming the flag that launches one without it (D3142)", warned)
	}
}

// errPersonalUnreachable is the transport failure a launch meets when the
// workspace's service does not answer, worded as the real call words it.
var errPersonalUnreachable = fmt.Errorf("the workspace's credentials service could not be reached")

// TestAFailedGrantStartsTheRunWithoutIt: D3147. A refused grant and a service
// that cannot be reached are two different answers, and both leave the run on
// the shared entries with one line saying which happened — the launch is never
// stopped by either, because the credential the person's token would have
// served is one the run may not even ask for, and failing the launch would
// break a run that works today.
//
// Mutation (run, restored): return an error from the launch instead of the
// warning and this fails on the launch that never started.
func TestAFailedGrantStartsTheRunWithoutIt(t *testing.T) {
	// The three parts of D3147, asserted the same way for both answers: no
	// entry handed to the job, one line saying which entries the run spends and
	// why, and an execution that started anyway.
	check := func(t *testing.T, r *personalLaunchRig, plain []string, why string) {
		t.Helper()
		if len(plain) != 0 {
			t.Errorf("the launch handed the job %v, want no entry: no slip was granted", plain)
		}
		said := r.warned.String()
		if !strings.Contains(said, "shared entries") || !strings.Contains(said, why) {
			t.Errorf("the launch said %q, want one line saying the run spends the shared entries and why (%q): D3147 is the run starting anyway, and the person being told which entries it spends", said, why)
		}
		if n := len(r.f.run.Executions()); n == 0 {
			t.Error("no execution was started: a failed grant must never stop a launch (D3147)")
		}
	}

	t.Run("the workspace refused the grant", func(t *testing.T) {
		// The REAL call, against a service answering the launcher's own class of
		// refusal: the mapping from that status to creds.ErrPersonGrantRefused
		// is the shipped one, and a stub carrying the sentinel already wrapped
		// would assert nothing about it — the launch would read the service's
		// answer as any other status and say so.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(credsvc.StatusRequestRefused)
		}))
		t.Cleanup(srv.Close)
		prev := personGrant
		personGrant = personGrantOverService
		t.Cleanup(func() { personGrant = prev })

		r := newPersonalLaunchRig(t, personalRouteRecord(srv.URL), true)
		plain := r.launch(t, nil)
		if r.calls != 0 {
			t.Errorf("the fixture's own grant answer was asked for %d times: this case drives the real call", r.calls)
		}
		check(t, r, plain, "the workspace refused its grant")
	})

	t.Run("the service could not be reached", func(t *testing.T) {
		// The transport failure is the client's and not a service's answer, so
		// it is the one case a stub stands for: there is no answer to read.
		r := newPersonalLaunchRig(t, personalRouteRecord(fakeWorkspaceAddress), true)
		plain := r.launch(t, func(creds.PersonGrantRequest) (creds.PersonGrant, error) {
			return creds.PersonGrant{}, errPersonalUnreachable
		})
		if r.calls != 1 {
			t.Fatalf("the launch made %d grant calls, want one", r.calls)
		}
		check(t, r, plain, "could not be reached")
	})
}

// TestTheGrantCarriesNoClaimSecret: F14, and the launcher's end of it. The
// claim secret is answered by the claim route alone, to the run the slip names
// — never by the grant, whose reader is this launcher's process and every line
// it writes. C1 makes that a property of the wire rather than of the service's
// manners (creds.ParsePersonGrant refuses a key the contract does not name),
// and what this test adds is the launcher's own half: the answer is read for
// its status, its size and its shape, the slip is what reaches the job, and
// neither the claim secret nor the slip that came attached to it goes anywhere.
//
// The call is the real one here (personGrantOverService against a service this
// test runs), so the strict decode and the route are the shipped code and not a
// fixture's stand-in.
//
// Mutation (run, restored): read the answer with json.Unmarshal into
// creds.PersonGrant instead of creds.ParsePersonGrant and the extra key is
// decoded past, and that slip reaches the job.
func TestTheGrantCarriesNoClaimSecret(t *testing.T) {
	const (
		claim = "fake-claim-secret-4f5e6d7c-not-a-credential"
		// withClaim is a grant whose slip a launcher would otherwise carry: the
		// key the contract does not name is the whole of what refuses it.
		withClaim = `{"held":true,"slip":"` + fakeSlipID + `","claim_secret":"` + claim + `"}`
		// plainGrant is the same answer without it, so the refusals below are
		// read as the extra key's and not as a call that never answered.
		plainGrant = `{"held":true,"slip":"` + fakeSlipID + `"}`
	)
	serve := func(t *testing.T, body string) *httptest.Server {
		t.Helper()
		var answer atomic.Value
		answer.Store(body)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(answer.Load().(string)))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	prev := personGrant
	personGrant = personGrantOverService
	t.Cleanup(func() { personGrant = prev })

	srv := serve(t, withClaim)
	r := newPersonalLaunchRig(t, personalRouteRecord(srv.URL), true)
	plain := r.launch(t, nil)
	if r.calls != 0 {
		t.Errorf("the fixture's own answer was asked for %d times: this test drives the real call", r.calls)
	}
	for _, kv := range slices.Concat(plain, r.rec.secretEnv) {
		if strings.Contains(kv, claim) {
			t.Errorf("the launch carried %q, and the claim secret must reach no environment of the job at all: it is answered by the claim route alone, to the run the slip names (F14)", kv)
		}
	}
	if slices.Contains(plain, creds.PersonSlipEnv+"="+fakeSlipID) {
		t.Errorf("the launch handed the job %s=%s out of an answer carrying a claim secret: that answer is not one this contract reads, and the slip that came with it is not a slip this launcher carries", creds.PersonSlipEnv, fakeSlipID)
	}
	if said := r.warned.String(); !strings.Contains(said, "not a grant's") {
		t.Errorf("the launch said %q, want one line saying the workspace's answer is not a grant's (D3147)", said)
	}

	srv = serve(t, plainGrant)
	s := newPersonalLaunchRig(t, personalRouteRecord(srv.URL), true)
	plain = s.launch(t, nil)
	if !slices.Contains(plain, creds.PersonSlipEnv+"="+fakeSlipID) {
		t.Fatalf("the same launch over the same service without the unknown key handed the job %v, want %s=%s: the refusal above is the key's and not the call's", plain, creds.PersonSlipEnv, fakeSlipID)
	}
}

// TestTheLaunchSaysWhichTokenItDrawsOn: D3142's line. A person stored their
// token to use it, so a run of theirs draws on it without being asked — and the
// launch says so, naming the workspace the token is held at and the flag that
// launches one without it. The entry itself is PLAIN environment and never a
// secret one (creds.PersonSlipEnv): the id is not a credential, and the claim
// secret, which is, never reaches a job's environment at all.
//
// Mutation (run, restored): drop the Fprint of the note line and this fails on
// what the launch said.
func TestTheLaunchSaysWhichTokenItDrawsOn(t *testing.T) {
	r := newPersonalLaunchRig(t, personalRouteRecord(fakeWorkspaceAddress), true)
	plain := r.launch(t, personalGrantHeld)
	if !slices.Contains(plain, creds.PersonSlipEnv+"="+fakeSlipID) {
		t.Fatalf("the launch handed the job %v, want the workspace slip's id under C1's own name (%s), which is what the run claims at bootstrap (WC8)", plain, creds.PersonSlipEnv)
	}
	for _, kv := range r.rec.secretEnv {
		if strings.HasPrefix(kv, creds.PersonSlipEnv+"=") {
			t.Errorf("the launch carried %q as secret environment, want the slip's id in plain environment only: the id is not a secret, and the backend that matters refuses SecretEnv outright (backend.LaunchSpec.SecretEnv)", kv)
		}
	}
	said := r.warned.String()
	if !strings.Contains(said, "--no-personal-token") {
		t.Errorf("the launch said %q, want one line naming the flag that launches a run without the person's token (D3142)", said)
	}
	if !strings.Contains(said, "workspace") {
		t.Errorf("the launch said %q, want one line naming the workspace the token is drawn from", said)
	}
}

// TestAServiceAccountLaunchAsksForNoGrant: D2976's consequence. A run launched
// by a service account holds no personal token, so a launcher that IS one asks
// the workspace for nothing: gcloud's own listing of the account it would mint
// with is read first, and the grant is never called. Nothing is said about it —
// a daemon's or a coordinator's launch is the ordinary state of that machine,
// and a line on every one of them would be noise about a fact its owner set.
// A gcloud naming no account at all is the same answer: there is no credential
// to mint the person's token with, so the launch asks for nothing and says
// nothing rather than warning about a login this machine never had.
//
// Mutation (run, restored): ask before reading the account and this fails on
// the grant call a service-account launcher makes.
func TestAServiceAccountLaunchAsksForNoGrant(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  string
		why  string
	}{
		{"the active account is a service account",
			`{"core":{"account":"fugaro-run@proj-1234.iam.gserviceaccount.com"}}`, "a service account"},
		{"gcloud is impersonating a service account",
			`{"core":{"account":"someone@example.com"},"auth":{"impersonate_service_account":"fugaro-run@proj-1234.iam.gserviceaccount.com"}}`, "an impersonated account"},
		// A machine whose gcloud names no account at all has nothing to mint
		// with either, and the same silence: it is the state of every daemon
		// whose gcloud was never logged in, and a line on each of its launches
		// would be a warning about a credential the machine never had.
		{"gcloud names no account", `{}`, "a machine with no account at all"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newPersonalLaunchRig(t, personalRouteRecord(fakeWorkspaceAddress), true)
			r.gcloud.configOut = c.cfg
			plain := r.launch(t, func(req creds.PersonGrantRequest) (creds.PersonGrant, error) {
				t.Errorf("a launch by %s asked the workspace for a personal token %+v, want no call: a machine is no person and holds no personal token (D2976)", c.why, req)
				return personalGrantHeld(req)
			})
			if r.calls != 0 {
				t.Errorf("the launch made %d grant calls, want none", r.calls)
			}
			if len(plain) != 0 {
				t.Errorf("the launch handed the job %v, want no entry", plain)
			}
			if r.warned.Len() != 0 {
				t.Errorf("the launch said %q, want nothing: this machine's launch is what it always was", r.warned.String())
			}
		})
	}
}
