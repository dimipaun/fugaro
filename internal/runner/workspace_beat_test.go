package runner_test

// WS13, the runner's half of the workspace's keep-warm beat (R10, R14, F33,
// D2985): a run whose own route record serves a kind of its credentials from the
// workspace's service posts to that service's keep-warm route at the slip
// beat's interval, with the job's identity and no slip id and no claim secret
// (F33), a setting turns the beat off, and a run that routed nothing there
// sends none. What the workspace's service does with the beat — the scope that
// serves the route (R14), the identity it verifies (F30/O4) and the refusal a
// slip earns (R18) — is pinned in internal/credsvc's own tests; what is pinned
// here is what the run sends and when it sends nothing.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// beatMetadata starts the metadata stand-in the workspace's service verifies a
// call's Google identity by, and points the run's environment at it: every call
// the run makes to the workspace presents the token this answers, which is what
// lets a test read the identity the beat really sent.
func beatMetadata(t *testing.T, b *bk) string {
	t.Helper()
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(beatIDToken))
	}))
	t.Cleanup(meta.Close)
	b.deps.Env = append(filterEnv(b.deps.Env, runner.MetadataHostEnv),
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"))
	return beatIDToken
}

// beatIDToken is what the metadata stand-in mints: not a credential of any kind,
// and the value the workspace's beat must present as its Authorization bearer.
const beatIDToken = "job-id-token-from-the-metadata-stand-in"

// workspaceService is the workspace's credentials service as this file needs it:
// it answers the keep-warm route with an empty 200 — which is all the route
// answers (R14) — and every other path as a service answers the model call the
// run routed there. A run whose model kind is served from the workspace sends
// both to this one host, which is why one fake stands in for both routes.
func workspaceService(t *testing.T) (*anthropicfake.Fake, *httptest.Server) {
	t.Helper()
	f, srv := anthropicfake.New(t)
	f.Func = func(r *http.Request, _ []byte) anthropicfake.Reply {
		if r.URL.Path == credsvc.WorkspaceKeepWarmPath {
			return anthropicfake.Reply{}
		}
		return wsAnswer()
	}
	return f, srv
}

// beats is every keep-warm request the workspace's service received: the run's
// beat, and not the model calls it routed there. A beat is read off the path
// because that is the whole of what tells the two apart at the service — the
// beat carries no body and no slip, and both are POSTs of the same client.
func beats(f *anthropicfake.Fake) []*http.Request {
	var out []*http.Request
	for _, r := range f.Seen() {
		if r.URL.Path == credsvc.WorkspaceKeepWarmPath {
			out = append(out, r)
		}
	}
	return out
}

// modelCalls is how many of a service's requests were not keep-warm beats: a
// service a run is routed to answers both, so a test that counts the calls the
// run made there counts the beats out of its own figure.
func modelCalls(f *anthropicfake.Fake) int { return f.Count() - len(beats(f)) }

// beatWindow is a step that does its work and then holds the stage open for a
// few of the fixture's intervals, so the run lives through several ticks of the
// beat before it ends (bkHeartbeatEvery is 30 ms here). Without it a quick run
// could end before its first tick, and the test would read the run's own speed
// rather than the beat.
func beatWindow(intervals int, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := inner(t, ctx, req)
		time.Sleep(time.Duration(intervals) * bkHeartbeatEvery)
		return res, err
	}
}

// TestARoutedRunBeatsTheWorkspace: a run whose record serves the model kind
// from the workspace posts to that service's keep-warm route while it lives, at
// the interval its own beat keeps. The service is the one the record's address
// names (R3), and every beat presents the job's identity — the Google ID token
// minted for that address and the run's own — which is what the service
// verifies the call by (F30, O4).
//
// The cadence is read as a count over a known window rather than as a gap
// between two requests: the fixture's interval is 30 ms, and a beat that ticked
// once per stage, or one that spun, is outside the band either way. The window
// is the run's own life, measured rather than assumed — a whole run is what the
// harness drives, and how long that takes is the machine's answer.
func TestARoutedRunBeatsTheWorkspace(t *testing.T) {
	wsFake, wsSrv := workspaceService(t)
	b := admissionRun(t, "github", "acme/app", "observe", "", routePolicy, out4k())
	b.deps.Local = false
	b.deps.Project = "aurora"
	idToken := beatMetadata(t, b)
	b.db.Set(creds.RoutePath(repoSlug), routeRecord(t, creds.Route{
		Version: creds.RouteVersion, Address: wsSrv.URL, Model: creds.HolderWorkspace,
	}))

	var st []int
	start := time.Now()
	rec, err := b.run(t, beatWindow(4, calling(&st, implement("feature"), call{sonnet, 4000})), review("ship", 0))
	lived := time.Since(start)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the run routed to the record's address stopped: rec = %+v, err = %v", rec, err)
	}
	if len(st) != 1 || st[0] != 200 {
		t.Fatalf("the run's model call was answered %v, want the one it made: the workspace serves this run's model kind", st)
	}
	seen := beats(wsFake)
	if len(seen) < 2 {
		t.Fatalf("the workspace's service received %d keep-warm requests over %v of run, want more than one: a run that routed a kind there keeps it warm for as long as it lives (R14, D2985)", len(seen), lived)
	}
	// The beat starts when the run reads its route record and ends with the
	// run, so its window is inside the run's life: one tick an interval is the
	// most it could have made, plus the one in flight when the run ended.
	if most := int(lived/bkHeartbeatEvery) + 2; len(seen) > most {
		t.Errorf("the workspace's service received %d keep-warm requests in %v, want no more than one per %v: the beat is faster than the run's own interval", len(seen), lived, bkHeartbeatEvery)
	}
	for i, r := range seen {
		if r.Method != http.MethodPost {
			t.Errorf("beat %d is a %s, want the POST the route is served on", i, r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+idToken {
			t.Errorf("beat %d presents Authorization %q, want the job's own Google identity minted for the address it calls (F30): the workspace's service verifies the run by it", i, got)
		}
		if got := r.Header.Get(gateway.HeaderRunIdentity); got == "" {
			t.Errorf("beat %d presents no %s: the run's own identity is the other half of what the service verifies (O4)", i, gateway.HeaderRunIdentity)
		}
		if got := r.Header.Get(gateway.HeaderRunJob); got == "" {
			t.Errorf("beat %d reports no %s: the call has to say which job it is in, as the run's other calls to a service do", i, gateway.HeaderRunJob)
		}
	}
}

// TestTheWorkspaceBeatCarriesNoSlip: the beat a run sends to its own service is
// not the beat it sends to the workspace's. R18 gives the workspace's service a
// call carrying the job's identity and the run's token and nothing else of the
// member's — no slip id and no claim secret (F33) — because a member's slip is
// its own installation's, and the workspace has no business reading it.
//
// The run here holds a slip and beats with it against its own service, which is
// what makes this more than an assertion about two absent headers: the same run
// sends both beats in the same window, and the test reads the difference off the
// two services' records rather than off the code.
func TestTheWorkspaceBeatCarriesNoSlip(t *testing.T) {
	const slipID = "fake-slip-0f1e2d3c-not-a-credential"

	svc := newSlipStandIn("fake-claim-secret-not-a-credential")
	wsFake, wsSrv := workspaceService(t)
	// A run that holds a slip is a switched one: its slip is claimed at the
	// installation's own credentials service (C1.10, O2), which for this
	// fixture is the stand-in its own environment names. The route record
	// below then moves the model kind off that service and onto the
	// workspace's, so this one run sends both beats in the same window — the
	// slip's to its own service and the keep-warm one to the workspace — and
	// the difference between them is read off the two services' records.
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	b.deps.Env = append(filterEnv(b.deps.Env, runner.SlipEnv), runner.SlipEnv+"="+slipID)
	b.fake.Func = func(r *http.Request, _ []byte) anthropicfake.Reply {
		if r.URL.Path == credsvc.SlipClaimPath {
			return svc.answer(r)
		}
		return wsAnswer()
	}
	beatMetadata(t, b)
	b.db.Set(creds.RoutePath(repoSlug), routeRecord(t, creds.Route{
		Version: creds.RouteVersion, Address: wsSrv.URL, Model: creds.HolderWorkspace,
	}))

	var st []int
	rec, err := b.run(t, beatWindow(4, calling(&st, implement("feature"), call{sonnet, 4000})), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the run holding a slip and routed to the workspace stopped: rec = %+v, err = %v", rec, err)
	}
	claims, ownBeats := svc.counted()
	if claims != 1 || ownBeats == 0 {
		t.Fatalf("the run's own service took %d claims and %d beats: this test says nothing about the workspace's beat unless the run really holds a slip and beats with it", claims, ownBeats)
	}
	seen := beats(wsFake)
	if len(seen) == 0 {
		t.Fatalf("the workspace's service received no keep-warm request, so the two beats cannot be told apart")
	}
	for i, r := range seen {
		if got := r.Header.Get(credsvc.HeaderSlip); got != "" {
			t.Errorf("beat %d carries %s %q: the workspace's service is not the run's own and a slip is the member's (R18, F33)", i, credsvc.HeaderSlip, got)
		}
		if got := r.Header.Get(credsvc.HeaderSlipClaim); got != "" {
			t.Errorf("beat %d carries a %s: the claim secret is the member's own credential and travels to no other service (R18, F40)", i, credsvc.HeaderSlipClaim)
		}
	}
}

// TestTheBeatCanBeTurnedOff: the switch is the run's own (R10, D2985), and the
// key it is written under is the one C2 names — creds.WorkspaceBeatSetting, held
// to its spelling by that package's own test — so this test writes the config it
// hands the run from the constant rather than from a second spelling of it.
//
// The run is routed exactly as the test above's is, and its model call still
// reaches the workspace: what the switch turns off is the beat and nothing else.
func TestTheBeatCanBeTurnedOff(t *testing.T) {
	block, leaf, ok := strings.Cut(creds.WorkspaceBeatSetting, ".")
	if !ok || block != "agent" {
		t.Fatalf("creds.WorkspaceBeatSetting is %q: this test writes it into fugaro.yaml's agent block", creds.WorkspaceBeatSetting)
	}
	off := "  " + leaf + ": false\n"
	cfg := gwConfig(t, off)
	// The key this test writes is the key the run's own decoder reads: a
	// spelling that drifted from the contract's would leave the switch unread
	// without failing anything, which is the one way this test could pass while
	// proving nothing.
	resolved, _, ps := config.Resolve([]byte(cfg), nil)
	if len(ps) > 0 {
		t.Fatalf("the switch this test writes is not one fugaro.yaml's decoder reads: %v", ps)
	}
	if resolved.Agent.WorkspaceBeatOn() {
		t.Fatalf("%s: false left the beat on, so the key this test writes is not the key the run reads", creds.WorkspaceBeatSetting)
	}

	wsFake, wsSrv := workspaceService(t)
	b := admissionRunCfg(t, "github", "acme/app", "observe", "", cfg, routePolicy, out4k())
	b.deps.Local = false
	b.deps.Project = "aurora"
	beatMetadata(t, b)
	b.db.Set(creds.RoutePath(repoSlug), routeRecord(t, creds.Route{
		Version: creds.RouteVersion, Address: wsSrv.URL, Model: creds.HolderWorkspace,
	}))

	var st []int
	rec, err := b.run(t, beatWindow(4, calling(&st, implement("feature"), call{sonnet, 4000})), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a run with the beat turned off stopped: rec = %+v, err = %v", rec, err)
	}
	if len(st) != 1 || st[0] != 200 || wsFake.Count() == 0 {
		t.Fatalf("the run's model call was answered %v with %d calls at the workspace: this test says nothing about the switch unless the run is really routed there", st, wsFake.Count())
	}
	if got := len(beats(wsFake)); got != 0 {
		t.Errorf("a run with %s false sent %d keep-warm requests, want none: the switch is the whole of the meaning (R10)", creds.WorkspaceBeatSetting, got)
	}
}

// TestNoRouteNoBeat: a beat exists only for a run that routed a kind to the
// workspace (contract-wire.md). A record that moves no kind — every kind served
// by the run's own project — is a run that never calls the workspace at all, and
// nothing is kept warm for it: the service on the other side of that address is
// another project's, and a beat of this run's would be a request it has no
// business making.
func TestNoRouteNoBeat(t *testing.T) {
	// No script and no Func: a request to the workspace's service fails the
	// test by name.
	wsFake, wsSrv := anthropicfake.New(t)
	b := admissionRun(t, "github", "acme/app", "observe", "", routePolicy, out4k())
	b.deps.Local = false
	b.deps.Project = "aurora"
	beatMetadata(t, b)
	b.db.Set(creds.RoutePath(repoSlug), routeRecord(t, creds.Route{
		Version: creds.RouteVersion, Address: wsSrv.URL,
	}))

	var st []int
	rec, err := b.run(t, beatWindow(4, calling(&st, implement("feature"), call{sonnet, 4000})), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a run whose record moves no kind stopped: rec = %+v, err = %v", rec, err)
	}
	if len(st) != 1 || st[0] != 200 {
		t.Fatalf("the run's model call was answered %v, want the one it made on its own upstream", st)
	}
	if got := wsFake.Count(); got != 0 {
		t.Errorf("a run whose record serves no kind from the workspace sent %d requests to its address, want none: nothing of this run's is served there", got)
	}
}
