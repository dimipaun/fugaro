package gateway

// WS18, the forwarder's half of the credential route: which credentials service
// the run's git traffic reaches, and what it carries there.
//
// A run takes each kind of its forge credentials from the service its own route
// record names for that kind (C2, D2979): the project's own, which is today's
// run and every run with no record, or the workspace's, whose address the
// record carries. The record itself is not read here — it is the runner's, and
// internal/creds cannot be named in this package (creds imports budget, which
// imports gateway, so the import is a cycle) — so what these cases hand the
// forwarder is the answer the runner hands it: WS11's second upstream for the
// kind the record serves from the workspace, and nothing at all for a kind it
// serves from the project.
//
// What is pinned here is the two halves of that answer: the traffic goes to the
// service the route named and never to the one the job's own environment names
// (R3, F32), the identity presented there is the workspace's own and carries no
// slip (R18), today's project path is unchanged, and a service that is down on
// the git path is waited for rather than failed at once.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsForward is the workspace upstream a routed run hands the forwarder: WS11's
// second upstream (D2981), with the address the run's own record carries, the
// job's identity minted for that address and the run's own token. It has no
// field for a slip and none for an auth declaration, which is R18 held by the
// shape.
func wsForward(t *testing.T, address string) *Workspace {
	t.Helper()
	return &Workspace{
		Address:    address,
		Job:        serviceJob,
		RunIDToken: func() string { return forwardIdentity(t, "run-identity") },
		IDToken: func(context.Context, string) (string, error) {
			return forwardIdentity(t, "workspace-job-id-token"), nil
		},
	}
}

// TestGitTrafficFollowsTheRoute: a run whose route serves the Bitbucket kind
// from a workspace's credentials service sends its git traffic there, to the
// address the run's own record carries, and the project's service — the one the
// job's environment names — is not called at all (WS18, D2979, R3, F32).
//
// The forwarder is built with both, as the runner builds it: the project's
// service and identity are what a run with no route uses, and the route's
// answer is the one the forwards go to. The red step of this task is this case
// with the answer taken away: a push routed to the workspace that reaches the
// project's fake.
func TestGitTrafficFollowsTheRoute(t *testing.T) {
	project := newForwardService(t, nil)
	ws := newForwardService(t, nil)
	f := startForward(t, project.url, func(o *GitForwardOptions) {
		o.Workspace = wsForward(t, ws.url)
	})

	resp, _ := forwardGet(t, f, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch was answered %d, want the service's own 200", resp.StatusCode)
	}
	if got := ws.count(); got != 1 {
		t.Errorf("the workspace's service received %d forwards, want the run's fetch: the run's route serves the Bitbucket kind there (D2979)", got)
	}
	if got := project.count(); got != 0 {
		t.Errorf("the project's service received %d forwards, want none: an address the job's environment names is not one a routed run's credential goes to (R3, F32)", got)
	}
	req, _ := ws.call(t, 0)
	if got, want := req.URL.Path, GitProxyPrefix+"acme/sandbox.git/info/refs"; got != want {
		t.Errorf("the workspace's service was asked for %q, want %q: the route's own path, unchanged (G13)", got, want)
	}
	if got := req.URL.RawQuery; got != "service=git-upload-pack" {
		t.Errorf("the query = %q, want the caller's own", got)
	}
	// The identity is the workspace's: the job's ID token minted for that
	// address's audience (D135's rule, applied to the address the record
	// carries), the run's own token and the job's name.
	if got, want := req.Header.Get("Authorization"), "Bearer "+forwardIdentity(t, "workspace-job-id-token"); got != want {
		t.Errorf("Authorization = %q, want the workspace's own ID token %q", got, want)
	}
	if got, want := req.Header.Get(HeaderRunIdentity), forwardIdentity(t, "run-identity"); got != want {
		t.Errorf("%s = %q, want %q", HeaderRunIdentity, got, want)
	}
	if got := req.Header.Get(HeaderRunJob); got != serviceJob {
		t.Errorf("%s = %q, want %q", HeaderRunJob, got, serviceJob)
	}
}

// TestGitToTheWorkspaceCarriesNoSlip: the run's slip is its own member's, and
// the workspace's service is outside the member: a call that presents one is
// refused there (R18, WS2's TestWorkspaceScopeRefusesACallThatPresentsASlip).
// So a forward to the workspace's service carries the run's own identity and
// the job's, and no slip header — and no X-Fugaro- header at all, which is the
// contract's own prefix: a header this contract gains later cannot ride to the
// workspace's service by being added to a request that already arrived.
//
// The run here is one that DOES hold a slip — the identity the options carry
// presents it to the project's service — so this case fails on a forwarder that
// moved its target's address and kept the identity it had.
func TestGitToTheWorkspaceCarriesNoSlip(t *testing.T) {
	project := newForwardService(t, nil)
	ws := newForwardService(t, nil)
	f := startForward(t, project.url, func(o *GitForwardOptions) {
		o.Identity.SlipID = forwardIdentity(t, "slip-id")
		o.Identity.SlipClaim = forwardIdentity(t, "slip-claim")
		o.Workspace = wsForward(t, ws.url)
	})

	if resp, _ := forwardGet(t, f, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch was answered %d, want the service's own 200", resp.StatusCode)
	}
	req, _ := ws.call(t, 0)
	// The three headers a forward writes are the whole of what this contract
	// sends, so anything else under its own prefix — the slip's two, and the
	// declaration's — is a header that rode to the workspace's service from an
	// identity that was not the workspace's.
	written := map[string]bool{"Authorization": true, HeaderRunIdentity: true, HeaderRunJob: true}
	for k := range req.Header {
		ck := http.CanonicalHeaderKey(k)
		if strings.HasPrefix(ck, "X-Fugaro-") && !written[ck] {
			t.Errorf("the workspace's service was sent %s: a run's slip and its declaration are its member's, and the workspace's service is outside the member (R18)", ck)
		}
	}
	if got := req.Header.Get(HeaderSlip); got != "" {
		t.Errorf("%s = %q, want none", HeaderSlip, got)
	}
	if got := req.Header.Get(HeaderSlipClaim); got != "" {
		t.Errorf("%s = %q, want none", HeaderSlipClaim, got)
	}
	// One header set less and not an unattributable call: what arrives is the
	// identity the workspace's service verifies the run by (F30, O4).
	if got := req.Header.Get(HeaderRunIdentity); got != forwardIdentity(t, "run-identity") {
		t.Errorf("%s = %q, want the run's own token", HeaderRunIdentity, got)
	}
	if got := req.Header.Get(HeaderRunJob); got != serviceJob {
		t.Errorf("%s = %q, want %q", HeaderRunJob, got, serviceJob)
	}
}

// TestNoRouteGitGoesToTheProject: a run with no record — and a run whose record
// serves the Bitbucket kind from its own project — sends its git traffic to the
// project's credentials service, under the identity the run holds for it. That
// is today's run, and the route changes nothing about it (WS18, WS11's
// TestARunRoutedToTheWorkspaceNeverCallsTheProject is the other side).
func TestNoRouteGitGoesToTheProject(t *testing.T) {
	project := newForwardService(t, nil)
	f := startForward(t, project.url, nil)

	if resp, _ := forwardGet(t, f, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch was answered %d, want the service's own 200", resp.StatusCode)
	}
	if got := project.count(); got != 1 {
		t.Fatalf("the project's service received %d forwards, want the run's fetch", got)
	}
	req, _ := project.call(t, 0)
	// The identity is the options' own, which is what a run's own service is
	// presented: a forwarder that had reached for a workspace's would have
	// nothing to reach for here, and one that answered the route's identity
	// would answer it with no route to read.
	if got, want := req.Header.Get("Authorization"), "Bearer "+forwardIdentity(t, "job-id-token"); got != want {
		t.Errorf("Authorization = %q, want the run's own service ID token %q", got, want)
	}
	if got, want := req.Header.Get(HeaderRunIdentity), forwardIdentity(t, "run-identity"); got != want {
		t.Errorf("%s = %q, want %q", HeaderRunIdentity, got, want)
	}
}

// gitForwardPushPath is a push's own path: the receive-pack route, on the
// repository the pass-through cases fetch.
const gitForwardPushPath = "acme/sandbox.git/git-receive-pack"

// freeAddr is an address nothing listens on, for a service that comes back: the
// listener is opened to learn a free port and closed again, and the service is
// then started on exactly that address (forwardServiceOn).
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving an address: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the address: %v", err)
	}
	return addr
}

// forwardServiceOn is newForwardService on an address of the caller's choosing,
// which is what lets a case start the service a forwarder has already been
// pointed at and is already waiting for. The answer is the caller's from the
// start, because a service that starts already behind a waiting forwarder is
// sent a request the moment it is up.
func forwardServiceOn(t *testing.T, addr string, answer func(http.ResponseWriter, *http.Request)) *forwardService {
	t.Helper()
	s := &forwardService{t: t, answer: answer}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("starting a service on %s: %v", addr, err)
	}
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// TestAnUnreachableGitServiceIsWaitedFor: the git paths have no stage to wait
// in, so a forward that could not reach the service at all is made again, for
// the same bound the model path waits out (WS18, D2983, serviceUnreachableBound)
// — which is what makes the run's bootstrap clone, and its last push, survive a
// revision that is still rolling out. The service here comes back inside the
// bound and each forward is then made.
//
// The push is the case with a body, and what the service reads is ONE body: a
// forwarder that sent a request again after the service had read it would be a
// second push, which is why only a forward whose connection could not be made
// at all is made again (send, dialFailed). The fetch that follows it is the
// clone's own shape — no body, so a forwarder that waited only for one of the
// two would fail here.
func TestAnUnreachableGitServiceIsWaitedFor(t *testing.T) {
	bound(t, 5*time.Second)
	defer func(old time.Duration) { gitForwardRetryDelay = old }(gitForwardRetryDelay)
	gitForwardRetryDelay = 10 * time.Millisecond

	addr := freeAddr(t)
	f := startForward(t, "http://"+addr, nil)

	// The service is not there yet, and the forwarder waits rather than
	// answering the caller: a push met with a 502 would be a run that stopped
	// over a service that was coming back.
	type answer struct {
		status int
		err    error
	}
	pushed := make(chan answer, 1)
	const pack = "a pack the run pushed"
	go func() {
		req, err := http.NewRequest(http.MethodPost, f.URL()+gitForwardPushPath, strings.NewReader(pack))
		if err != nil {
			pushed <- answer{err: err}
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			pushed <- answer{err: err}
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		pushed <- answer{status: resp.StatusCode}
	}()
	select {
	case got := <-pushed:
		t.Fatalf("a push met a service that was not there and was answered at once (%+v), want the wait", got)
	case <-time.After(3 * gitForwardRetryDelay):
	}

	svc := forwardServiceOn(t, addr, nil)
	select {
	case got := <-pushed:
		if got.err != nil {
			t.Fatalf("the push through the forwarder: %v", got.err)
		}
		if got.status != http.StatusOK {
			t.Fatalf("the push was answered %d, want the service's own 200", got.status)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the push was not made again once the service was there")
	}
	if got := svc.count(); got != 1 {
		t.Fatalf("the service read %d forwards, want the one push it answered", got)
	}
	if _, body := svc.call(t, 0); string(body) != pack {
		t.Errorf("the service read the body %q, want %q: a push put on the wire twice is a second push", body, pack)
	}

	if resp, _ := forwardGet(t, f, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch was answered %d, want the service's own 200", resp.StatusCode)
	}
	if got := svc.count(); got != 2 {
		t.Errorf("the service read %d forwards, want the push and the fetch after it", got)
	}
}
