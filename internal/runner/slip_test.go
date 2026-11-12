package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// These are DS1.5's runner half: the run takes the slip its launcher was
// granted at its own start, keeps the claim's secret in memory and nowhere
// else, presents it on every call the gateway makes to the credentials service
// (internal/gateway's service upstream), and keeps one instance of that service
// warm for as long as it lives (D1933, D1934; Check 511 measured the beat).
//
// The fixtures are the ones internal/runner's cascade tests already build (the
// run's own budget session is its Firebase identity, so no test can make it
// up), plus a stand-in for the credentials service: the claim and every
// keep-warm beat are calls the run makes on its own behalf, and this is where
// they are read.
//
// No value below is, or looks like, a credential. The slip's id is the node
// name a run presents (it travels in the job's environment and the agent can
// read it: F22), and the claim secret is a fixture's own value that reaches no
// service but this stand-in.
const (
	slipFixtureID     = "fake-slip-0f1e2d3c-not-a-credential"
	slipFixtureSecret = "fake-claim-secret-not-a-credential"
	slipFixtureToken  = "ya29.an-id-token-from-the-metadata-server"
)

// slipReply is what the stand-in answers one call with.
type slipReply struct {
	status int
	body   string
}

// slipCall is one call the run made, as the stand-in read it.
type slipCall struct {
	path, method string
	header       http.Header
	body         []byte
}

// slipService is the installation's credentials service as this file needs it:
// the two routes the run calls, recorded, with the answer the test decides.
type slipService struct {
	*httptest.Server
	mu     sync.Mutex
	calls  []slipCall
	answer func(n int, r *http.Request, body []byte) slipReply
}

// newSlipService starts the stand-in. answer is called with the number of this
// call (1 for the first) and the call itself, so a test can answer a failure
// first and a success after it.
func newSlipService(t *testing.T, answer func(n int, r *http.Request, body []byte) slipReply) *slipService {
	t.Helper()
	if answer == nil {
		// The service as it is when nothing is wrong: a call that presents no
		// claim secret takes the slip and is answered its secret, and a call
		// that presents one is answered the claim's own reading — no secret
		// again (D1941).
		answer = func(_ int, r *http.Request, _ []byte) slipReply {
			if r.Header.Get(credsvc.HeaderSlipClaim) != "" {
				return slipReply{body: `{"expires_at":"2026-11-13T00:00:00Z","max_runtime_seconds":86400}`}
			}
			return claimAnswer()
		}
	}
	s := &slipService{answer: answer}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		s.mu.Lock()
		s.calls = append(s.calls, slipCall{path: r.URL.Path, method: r.Method, header: r.Header.Clone(), body: body})
		n := len(s.calls)
		s.mu.Unlock()
		rep := s.answer(n, r, body)
		if rep.status == 0 {
			rep.status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(s.Close)
	return s
}

// claims returns the calls made to the claim route that present no claim
// secret: the run's first call, the one that takes the slip.
func (s *slipService) claims() []slipCall {
	var out []slipCall
	for _, c := range s.seen(credsvc.SlipClaimPath) {
		if c.header.Get(credsvc.HeaderSlipClaim) == "" {
			out = append(out, c)
		}
	}
	return out
}

// beats returns the calls made to the claim route that present the claim
// secret: the keep-warm requests (DS1.5; the route answers both, which is what
// makes a beat a call that proves the slip still stands).
func (s *slipService) beats() []slipCall {
	var out []slipCall
	for _, c := range s.seen(credsvc.SlipClaimPath) {
		if c.header.Get(credsvc.HeaderSlipClaim) != "" {
			out = append(out, c)
		}
	}
	return out
}

// seen returns every call this stand-in has read so far, copied.
func (s *slipService) seen(path string) []slipCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []slipCall
	for _, c := range s.calls {
		if path == "" || c.path == path {
			out = append(out, c)
		}
	}
	return out
}

// claimAnswer is the service's answer to a claim that took the slip: the
// secret answered exactly once, and the slip's own bounds.
func claimAnswer() slipReply {
	return slipReply{body: `{"claim_secret":"` + slipFixtureSecret +
		`","expires_at":"2026-11-13T00:00:00Z","max_runtime_seconds":86400}`}
}

// slipRun is a cascade run that holds a slip: the service's URL, the slip's id
// and a metadata server in its environment, and the budget session that is the
// identity the service verifies the claim with. edit, when set, sees the run
// last -- after the environment, the way svcRun's own callers do it.
func slipRun(t *testing.T, svc *slipService, answer func(n int, r *http.Request, body []byte) slipReply, edit func(*run)) *run {
	t.Helper()
	if svc == nil {
		svc = newSlipService(t, answer)
	}
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			http.Error(w, "the metadata server is asked with Metadata-Flavor: Google", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, slipFixtureToken+"\n")
	}))
	t.Cleanup(meta.Close)
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{
			credentialsServiceURLEnv + "=" + svc.URL,
			metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
			SlipEnv + "=" + slipFixtureID,
			"FUGARO_STATE_DIR=" + t.TempDir(),
		}
		if edit != nil {
			edit(r)
		}
	})
	r.sess = svcSession(t)
	// A run's own end stops the beat (Run's deferred cleanup does it on every
	// path out of it), so a fixture's beat is stopped here: one that outlived
	// its test would keep asking this stand-in for the length of the next
	// test, and would read the interval that test set.
	t.Cleanup(r.stopSlipBeat)
	return r
}

// TestClaimAtRunnerStart: the run takes its slip once, at its own start, with
// the two identities the service decides a claim by -- the job's Google ID
// token, which Cloud Run IAM admits, and the run's own Firebase identity -- and
// it holds what the claim answered. A run that took it twice would be refused
// as already claimed (D1941), so the second call is not made at all.
func TestClaimAtRunnerStart(t *testing.T) {
	svc := newSlipService(t, func(int, *http.Request, []byte) slipReply { return claimAnswer() })
	r := slipRun(t, svc, nil, nil)

	r.claimSlip(context.Background())
	// A run claims once: the slip is taken by the call that answers the
	// secret, and a second call is the service's own refusal.
	r.claimSlip(context.Background())

	claims := svc.claims()
	if len(claims) != 1 {
		t.Fatalf("the credentials service was asked to claim %d times, want once", len(claims))
	}
	c := claims[0]
	if c.method != http.MethodPost || c.path != credsvc.SlipClaimPath {
		t.Errorf("the claim is %s %s, want POST %s", c.method, c.path, credsvc.SlipClaimPath)
	}
	if got := c.header.Get(credsvc.HeaderSlip); got != slipFixtureID {
		t.Errorf("the claim presents the slip %q, want the one the launcher handed the job", got)
	}
	if got := c.header.Get(credsvc.HeaderSlipClaim); got != "" {
		t.Errorf("the claim presents a claim secret (%q): the first call is the one that takes the slip", got)
	}
	if got := c.header.Get("Authorization"); got != "Bearer "+slipFixtureToken {
		t.Errorf("the claim's Authorization is %q, want the job's ID token from the metadata server", got)
	}
	if got := c.header.Get(gateway.HeaderRunIdentity); got != r.sess.IDToken()() {
		t.Error("the claim does not carry the run's own identity: the service decides a claim by it, and the job's token alone is not the run")
	}
	if got := c.header.Get(gateway.HeaderRunJob); got != svcJob {
		t.Errorf("the claim reports the job as %q, want %q", got, svcJob)
	}
	var req credsvc.SlipClaimRequest
	if err := json.Unmarshal(c.body, &req); err != nil {
		t.Fatalf("the claim's body is not the route's request: %v", err)
	}
	// The execution the claim reports is the SHORT name: the service records
	// it for an operator and refuses a value above its own bound (128 bytes),
	// and the canonical name the platform reports this process is far longer.
	if want := svcJob + "-aaaaa"; req.Execution != want {
		t.Errorf("the claim reports the execution %q, want %q", req.Execution, want)
	}

	hold := r.slipHold()
	if hold == nil || hold.id != slipFixtureID || hold.secret != slipFixtureSecret {
		t.Fatalf("the run holds %+v, want the slip the claim answered", hold)
	}
	// The claim's secret is a credential: the run registers it with its own
	// redactor before anything can print it.
	if !slices.Contains(r.secretList(), slipFixtureSecret) {
		t.Error("the claim's secret is not registered for redaction")
	}
	if got := r.redact("the answer was " + slipFixtureSecret); strings.Contains(got, slipFixtureSecret) {
		t.Errorf("the claim's secret is not redacted: %q", got)
	}
}

// TestSlipNotExportedToAgentEnv: the slip reaches the agent as nothing at all
// (G5). The launcher puts the id in the job's environment -- the runner
// inherits it, and the agent can read the runner's environment anyway (F22) --
// and the runner hands the agent its own allow-listed environment, which
// carries neither the id nor the secret the claim answered. The secret exists
// in this process's memory and in no document.
//
// The same test states the positive half, because the negative one alone would
// pass for a run that presents nothing anywhere: the gateway's call to the
// credentials service carries the slip, and it is the gateway -- holding the
// claim -- that presents it, not the agent.
func TestSlipNotExportedToAgentEnv(t *testing.T) {
	const gwToken = "the-gateway-token-not-a-credential"
	var logBuf bytes.Buffer

	var mu sync.Mutex
	var svcSeen []*http.Request
	fake, upstream := anthropicfake.New(t)
	fake.Func = func(r *http.Request, body []byte) anthropicfake.Reply {
		mu.Lock()
		svcSeen = append(svcSeen, r.Clone(r.Context()))
		mu.Unlock()
		rep := anthropicfake.MessageOK("claude-sonnet-5-5", pricing.Usage{Input: 1, Output: 3})
		// The pair the service answers a served call with: the tier it
		// settled by and the NAME of the credential that answered (C1.16,
		// C1.19). A tier with no name is not an answer this contract reads.
		rep.Header = http.Header{
			gateway.HeaderTier:       []string{gateway.TierB},
			gateway.HeaderCredential: []string{"repo-shared-b"},
		}
		return rep
	}
	svc := newSlipService(t, nil)

	r := slipRun(t, svc, nil, func(r *run) {
		// The runner's logger is this run's own: every line it writes is read
		// back below, redaction included.
		r.d.Log = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		// The model calls go to the stand-in, which is where the slip is read
		// back off the wire.
		r.d.GatewayUpstream = ""
		r.d.Env = append(r.d.Env, "ANTHROPIC_API_KEY="+gwToken)
	})
	// The upstream the run would build points at the installation's service;
	// this test's service is the stand-in the gateway sends to, one hop on.
	svcURL := upstream.URL
	r.claimSlip(context.Background())
	if r.slipHold() == nil {
		t.Fatal("the run did not hold the claim it just made")
	}

	// What the gateway sends, built from the run's own upstream with this
	// test's stand-in as its destination: the claim travels with it.
	up, err := r.serviceUpstream()
	if err != nil {
		t.Fatalf("a cascade run's upstream: %v", err)
	}
	up.BaseURL = svcURL
	if up.Service == nil || up.Service.SlipID != slipFixtureID || up.Service.SlipClaim != slipFixtureSecret {
		t.Fatalf("the upstream carries %+v, want the run's claim", up.Service)
	}
	gw, err := gateway.Start(context.Background(), gateway.Options{
		Upstream: up, Prices: pricing.Embedded(), Mode: gateway.Observe,
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("starting the gateway on the run's own upstream: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(gateway.Stage{Name: "implement", Model: "claude-sonnet-5-5"})
	r.gwAgent = &agent.Gateway{BaseURL: gw.URL(), Token: gw.Token()}

	// The agent's environment, built exactly as bootstrap builds it: the
	// runner's own environment in, the allow-list of what the agent may see
	// out.
	env, _, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{
		Auth:     config.AuthCascade,
		Set:      map[string]string{"FUGARO_STATE_DIR": r.d.StateDir},
		Gateway:  r.gwAgent,
		ImageEnv: nil,
	})
	if err != nil {
		t.Fatalf("building the agent's environment: %v", err)
	}
	// The positive control: a name Fugaro sets for the agent is there, so an
	// allow-list that carried nothing would not read as this test passing.
	if !envHas(env, "FUGARO_STATE_DIR=") {
		t.Error("the agent's environment carries no FUGARO_STATE_DIR: this test cannot tell a variable that was dropped from a list that was empty")
	}
	for _, kv := range env {
		for _, forbidden := range []string{slipFixtureID, slipFixtureSecret} {
			if strings.Contains(kv, forbidden) {
				t.Errorf("the agent's environment carries %q, which is the run's slip and never the agent's (G5)", strings.SplitN(kv, "=", 2)[0])
			}
		}
		if strings.HasPrefix(kv, SlipEnv+"=") {
			t.Errorf("the agent's environment carries %s: the slip is the runner's, and the gateway presents it (G5)", SlipEnv)
		}
	}
	if !envHas(r.d.Env, SlipEnv+"="+slipFixtureID) {
		t.Fatalf("the fixture's own environment carries no %s, so the check above proves nothing", SlipEnv)
	}

	// A model call, through the gateway: what the credentials service sees on
	// it is the run's claim, and nothing of it came from the agent.
	req, err := http.NewRequest("POST", gw.URL()+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", gw.Token())
	// An agent that tried to present a slip of its own: the gateway drops
	// what the agent sent and presents the run's own claim, if it holds one.
	req.Header.Set(gateway.HeaderSlip, "a-slip-the-agent-invented")
	req.Header.Set(gateway.HeaderSlipClaim, "a-claim-the-agent-invented")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the agent's call was answered %d: %s (fake saw %d)", resp.StatusCode, answer, fake.Count())
	}
	mu.Lock()
	seen := append([]*http.Request(nil), svcSeen...)
	mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("the credentials service saw %d calls, want one", len(seen))
	}
	if got := seen[0].Header.Get(gateway.HeaderSlip); got != slipFixtureID {
		t.Errorf("the service was presented the slip %q, want the run's own claim", got)
	}
	if got := seen[0].Header.Get(gateway.HeaderSlipClaim); got != slipFixtureSecret {
		t.Error("the service was not presented the run's claim secret")
	}

	// Nothing the run published carries either value: the record it writes,
	// and every line of its own log.
	rec, err := json.Marshal(r.rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{slipFixtureID, slipFixtureSecret} {
		if bytes.Contains(rec, []byte(secret)) {
			t.Errorf("the run's record carries %q, which is the run's slip and never a document", secret)
		}
		if strings.Contains(logBuf.String(), secret) {
			t.Errorf("a log line carries %q, which is the run's slip", secret)
		}
	}
}

// TestBackoffOnServiceError: a call the run makes to the credentials service
// that answers a failure it can retry -- the service being unreachable, or
// answering 5xx or its rate limit -- is retried, with the backoff between the
// attempts growing (spec §Resilience: jobs retry credential and proxy requests
// with backoff). What is not retried is an answer about the call: a refusal is
// a decision, and the retries it would spend answer the same thing.
//
// The waits are the test's own, so nothing here spends them: slipRetryWait is
// the seam, and what it is handed is read back.
func TestBackoffOnServiceError(t *testing.T) {
	for i := 1; i < len(slipRetryDelays); i++ {
		if slipRetryDelays[i] <= slipRetryDelays[i-1] {
			t.Fatalf("the retry waits are %v: a backoff is waits that grow", slipRetryDelays)
		}
	}
	// The waits a test measures instead of sleeping through: every call the
	// run makes to the service shares the retry, the beat's included.
	waits := swapSlipRetryWait(t)

	t.Run("a failure the service can be asked again about is retried", func(t *testing.T) {
		fails := len(slipRetryDelays)
		svc := newSlipService(t, func(n int, _ *http.Request, _ []byte) slipReply {
			if n <= fails {
				return slipReply{status: http.StatusServiceUnavailable, body: `{"error":{"message":"not now"}}`}
			}
			return claimAnswer()
		})
		r := slipRun(t, svc, nil, nil)
		r.claimSlip(context.Background())
		if got := len(svc.claims()); got != fails+1 {
			t.Fatalf("the service saw %d claim calls, want %d: one and one per retry", got, fails+1)
		}
		if r.slipHold() == nil {
			t.Error("the run holds no slip after a claim that was answered on its last attempt")
		}
		if got := *waits; !slices.Equal(got, slipRetryDelays) {
			t.Errorf("the retries waited %v, want the backoff %v", got, slipRetryDelays)
		}
	})

	t.Run("a refusal is not retried", func(t *testing.T) {
		svc := newSlipService(t, func(int, *http.Request, []byte) slipReply {
			return slipReply{status: http.StatusForbidden, body: `{"error":{"message":"no"}}`}
		})
		r := slipRun(t, svc, nil, nil)
		*waits = nil
		r.claimSlip(context.Background())
		if got := len(svc.claims()); got != 1 {
			t.Errorf("the service saw %d claim calls after refusing one, want one: a refusal is an answer", got)
		}
		if r.slipHold() != nil {
			t.Error("the run holds a slip the service refused")
		}
		if len(*waits) != 0 {
			t.Errorf("the run waited %v over a refusal", *waits)
		}
	})

	t.Run("a service without the claim route is the version skew", func(t *testing.T) {
		// The service one release behind: it serves no claim route at all, so
		// the 404 its own router gives is not a refusal of this run. The run
		// proceeds on the path it took before the slip existed (DS1.4's
		// constant).
		svc := newSlipService(t, func(int, *http.Request, []byte) slipReply {
			return slipReply{status: http.StatusNotFound, body: `{"error":{"message":"no such route"}}`}
		})
		r := slipRun(t, svc, nil, nil)
		*waits = nil
		r.claimSlip(context.Background())
		if got := len(svc.claims()); got != 1 {
			t.Errorf("the service saw %d claim calls, want one", got)
		}
		if r.slipHold() != nil {
			t.Error("the run holds a slip from a service that serves no claim route")
		}
		if len(*waits) != 0 {
			t.Errorf("the run waited %v over a route the service does not serve", *waits)
		}
		// The beat goes with the claim: there is nothing to keep warm with.
		r.startSlipBeat(context.Background())
		if beats := svc.beats(); len(beats) != 0 {
			t.Errorf("a run with no claim sent %d beats", len(beats))
		}
	})
}

// TestHeartbeatStopsWithRun: while the run lives, the keep-warm beat asks the
// service to read the slip back, which is one request per interval (D1934:
// what Check 511 measured at 15 s). It stops with the run -- the run's own end
// waits for the beat's goroutine, so nothing of a finished run is still in
// flight.
func TestHeartbeatStopsWithRun(t *testing.T) {
	restore := slipBeatEvery
	slipBeatEvery = 2 * time.Millisecond
	t.Cleanup(func() { slipBeatEvery = restore })

	svc := newSlipService(t, nil)
	r := slipRun(t, svc, nil, nil)
	r.claimSlip(context.Background())
	if got := len(svc.claims()); got != 1 {
		t.Fatalf("the service saw %d claim calls, want one", got)
	}

	// The beat arrives on its own, with the claim the run took and the slip it
	// was granted.
	deadline := time.Now().Add(10 * time.Second)
	for len(svc.beats()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	beats := svc.beats()
	if len(beats) < 2 {
		t.Fatalf("the run sent %d beats over %s, want one every %s", len(beats), 10*time.Second, slipBeatEvery)
	}
	for _, b := range beats {
		if b.method != http.MethodPost || b.header.Get(credsvc.HeaderSlip) != slipFixtureID {
			t.Fatalf("a beat is %s with the slip %q, want a claim of the run's own", b.method, b.header.Get(credsvc.HeaderSlip))
		}
		if b.header.Get(gateway.HeaderRunIdentity) != r.sess.IDToken()() {
			t.Fatal("a beat does not carry the run's own identity")
		}
	}

	r.stopSlipBeat()
	settled := len(svc.beats())
	if settled < 2 {
		t.Fatalf("the run sent %d beats before it stopped", settled)
	}
	time.Sleep(20 * slipBeatEvery)
	if got := len(svc.beats()); got != settled {
		t.Errorf("the run sent %d beats after it stopped (had %d): the beat outlives the run", got, settled)
	}
}

// swapSlipRetryWait replaces the retry's wait for the length of one test and
// hands back the list of the waits the run asked for.
func swapSlipRetryWait(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	restore := slipRetryWait
	slipRetryWait = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	t.Cleanup(func() { slipRetryWait = restore })
	return &waits
}

// envHas reports whether env carries a variable whose entry starts with prefix.
func envHas(env []string, prefix string) bool {
	return slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, prefix) })
}
