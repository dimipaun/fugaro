package credsvc

// The service's forwarding tests (C1.11): one call, end to end, from the
// headers a run's in-runner gateway sends to the answer it gets back.
//
// Where cascade_test.go asks which credential a call goes to, this file asks
// what the service does with the call itself: what reaches Anthropic, what
// comes back, what it refuses, and what it leaves in its own log and in the
// database. Everything here is hermetic (Convention 9) and nothing in it is, or
// looks like, a credential (Convention 2).

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// testBundlePaidOnlyDoc is an installation whose bundle holds no shared token:
// the cascade has no tier A to consider, and O4's admission is therefore never
// consulted at all.
func testBundlePaidOnlyDoc(t *testing.T) []byte {
	t.Helper()
	return []byte(config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
}

// callTokens is callPath with the two identities given rather than minted.
func (s *svc) callTokens(t *testing.T, body, runTok, jobTok, job string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	resp := s.postTokens(t, MessagesPath, body, runTok, jobTok, job, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// ---------------------------------------------------------------------------
// The forward
// ---------------------------------------------------------------------------

// TestServiceForwardsWithSelectedCredential: the happy path, and the two
// answers to "what did the run's call become": upstream, the call's own body
// and the headers Check 90 allows, with the selected credential attached and
// nothing of this service's own identity on it; back, the upstream's answer
// with the tier and the credential's name on it.
func TestServiceForwardsWithSelectedCredential(t *testing.T) {
	s := newSvc(t)
	const marker = "forward-marker"
	resp, body := s.call(t, callBody(marker))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the upstream's 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierA)
	}
	if got := resp.Header.Get(HeaderCredential); got != "alice" {
		t.Errorf("%s = %q, want the credential that answered", HeaderCredential, got)
	}
	if got := resp.Header.Get(HeaderSwitch); got != "" {
		t.Errorf("%s = %q, want none: this call did not move", HeaderSwitch, got)
	}
	if !strings.Contains(string(body), "msg_fake") {
		t.Errorf("the client's body is not the upstream's answer: %s", body)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	req := s.up.Seen()[0]
	if got := req.URL.Path; got != MessagesPath {
		t.Errorf("the upstream path is %q, want %q", got, MessagesPath)
	}
	if got := string(s.up.Body(0)); got != callBody(marker) {
		t.Errorf("the upstream body is %q, want the call's own", got)
	}
	if got, want := req.Header.Get("Authorization"), "Bearer "+fakeCred(t, testAccount); got != want {
		t.Error("the call did not go upstream with the credential that answered it")
	}
	if got := req.Header.Get("Authorization"); strings.Contains(got, fakeCred(t, "default")) {
		t.Error("the call went upstream with an account the round robin did not pick")
	}
	if got := req.Header.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Errorf("Anthropic-Version = %q, want the client's: Check 90 proved it mandatory", got)
	}
	// The service's own identity is never Anthropic's business.
	for _, name := range []string{HeaderRunIdentity, HeaderRunJob} {
		if got := req.Header.Get(name); got != "" {
			t.Errorf("the upstream call carries %s", name)
		}
	}
	if got := req.Header.Get("Authorization"); strings.Contains(got, s.jobToken()) {
		t.Error("the upstream call carries the job's ID token")
	}
}

// TestServiceStreamsUnbuffered: an answer reaches the run as it arrives (C1.8),
// and the service is a forwarder in that respect and not a buffer. The upstream
// holds each event back for a second, so an answer that were read whole before
// it was written could not deliver the first event in a fraction of that.
func TestServiceStreamsUnbuffered(t *testing.T) {
	s := newSvc(t)
	reply := anthropicfake.StreamOK(testModel, testUsage)
	reply.EventDelay = time.Second
	s.up.Script = []anthropicfake.Reply{reply}

	resp := s.post(t, MessagesPath, callBody("stream"), func(r *http.Request) {
		r.Header.Set("Accept", "text/event-stream")
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want an event stream", got)
	}
	start := time.Now()
	first := readEvent(t, bufio.NewReader(resp.Body))
	elapsed := time.Since(start)
	if first == "" {
		t.Fatal("the answer carried no event at all")
	}
	if !strings.Contains(first, "message_start") {
		t.Errorf("the first event is %q, want the stream's own first event", first)
	}
	// The upstream is still holding its second event back, so an answer that
	// had been buffered could not have reached the client yet.
	if elapsed > 250*time.Millisecond {
		t.Errorf("the first event took %v: the answer was buffered rather than streamed", elapsed)
	}
}

// TestServiceReadsTheBundleOnceForTwoCalls is the service's half of Q6's one
// read: two calls in the same instant read the container once, and each of them
// is served every credential of the document it read. The loader's own TTL and
// fail-closed behaviour is TestBundleLoadsOncePerCall in bundle_test.go.
func TestServiceReadsTheBundleOnceForTwoCalls(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}
	for i := 0; i < 2; i++ {
		if resp, _ := s.call(t, callBody("bundle")); resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: the answer is %d, want 200", i, resp.StatusCode)
		}
	}
	if n := s.src.readCount(); n != 1 {
		t.Errorf("the bundle source was read %d times, want 1: one read answers a call", n)
	}
}

// ---------------------------------------------------------------------------
// The refusals
// ---------------------------------------------------------------------------

// TestServiceRefusesUnverified: every identity the service cannot verify is
// refused typed, before anything is read of the call, and nothing reaches the
// upstream. The refusals name a header or the rule and never a value.
func TestServiceRefusesUnverified(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		build func(s *svc, t *testing.T) (*http.Response, []byte)
	}{
		{
			name: "no identity at all",
			typ:  ErrorUnverifiedRunIdentity,
			build: func(s *svc, t *testing.T) (*http.Response, []byte) {
				return s.postRaw(t, http.MethodPost, MessagesPath)
			},
		},
		{
			name: "a run token from another issuer",
			typ:  ErrorUnverifiedRunIdentity,
			build: func(s *svc, t *testing.T) (*http.Response, []byte) {
				tok := s.runToken(func(c map[string]any) { c["iss"] = "https://elsewhere.invalid" })
				return s.callTokens(t, callBody("unverified"), tok, s.jobToken(), s.job)
			},
		},
		{
			name: "a job token for another audience",
			typ:  ErrorUnverifiedJobIdentity,
			build: func(s *svc, t *testing.T) (*http.Response, []byte) {
				tok := s.jobToken(func(c map[string]any) { c["aud"] = "https://elsewhere.invalid" })
				return s.callTokens(t, callBody("unverified"), s.runToken(), tok, s.job)
			},
		},
		{
			name: "a job account the installation does not know",
			typ:  ErrorUnverifiedJobIdentity,
			build: func(s *svc, t *testing.T) (*http.Response, []byte) {
				tok := s.jobToken(func(c map[string]any) { c["email"] = "someone-else@example.invalid" })
				return s.callTokens(t, callBody("unverified"), s.runToken(), tok, s.job)
			},
		},
		{
			name: "a job name that is not a job name",
			typ:  ErrorUnverifiedRunIdentity,
			build: func(s *svc, t *testing.T) (*http.Response, []byte) {
				return s.callTokens(t, callBody("unverified"), s.runToken(), s.jobToken(), "Not A Job")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvc(t)
			resp, body := tc.build(s, t)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnverified)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != tc.typ {
				t.Errorf("the refusal is %q, want %q: %s", got.Error, tc.typ, got.Message)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d requests, want none: the call was refused", n)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want none: the identity comes first", n)
			}
		})
	}
}

// TestServiceRefusesASharedJobAccount: Global Constraint 6's gate, seen from the
// service. A run in one of the installation's shared project jobs is refused
// until the phase-3 shared-job rules are on main (C3.9), and it is refused from
// the caller's verified account and never from the job name it reports.
func TestServiceRefusesASharedJobAccount(t *testing.T) {
	s := newSvc(t)
	tok := s.jobToken(func(c map[string]any) { c["email"] = testSharedSA })
	resp, body := s.callTokens(t, callBody("shared"), s.runToken(), tok, s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnverified)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q: %s", got.Error, ErrorUnverifiedJobIdentity, got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
}

// TestForwardRefusesARunTokenOfAnotherRepository is review finding N1: the two
// identities a call carries are verified separately and nothing pairs them, so
// the pairing is this service's admission question. A run identity is honoured
// only by the job whose repository it belongs to — which is what stops an
// admitted job account from presenting a run token of another repository, and
// what stops a run from being served by an account its repository has nothing
// to do with.
func TestForwardRefusesARunTokenOfAnotherRepository(t *testing.T) {
	t.Run("the run belongs to another repository", func(t *testing.T) {
		s := newSvc(t)
		otherSlug := testSlug(t, testOtherRepoPath)
		tok := s.runToken(func(c map[string]any) {
			c["fs"] = otherSlug
			c["sub"] = "r~" + otherSlug + "~" + s.run
			c["user_id"] = c["sub"]
		})
		resp, body := s.callTokens(t, callBody("other-repo"), tok, s.jobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnverified)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q: %s", got.Error, ErrorUnverifiedRunIdentity, got.Message)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none", n)
		}
	})

	t.Run("the calling account is bound to no repository", func(t *testing.T) {
		// The account list the authenticator holds and the table the service
		// binds runs with are two published facts, and a deployment that
		// published them apart would leave an admitted account bound to no
		// repository. A caller whose email is in no row is refused, which is the
		// other arm of the same check.
		s := newSvcWith(t, func(_ *svc, cfg *Config) {
			cfg.JobAccounts = []JobAccount{{
				Email: testOtherSA,
				Slug:  testSlug(t, testOtherRepoPath),
				Repo:  testOtherRepo,
			}}
		})
		resp, body := s.call(t, callBody("unbound"))
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnverified)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q: %s", got.Error, ErrorUnverifiedRunIdentity, got.Message)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none", n)
		}
	})
}

// TestServiceHasNoAdminRoutes: D143's "no admin surface" is kept by there being
// nothing to reach rather than by a check inside a handler. Every path but the
// two Anthropic ones is a 404, including the ones an operator's CLI uses —
// which is why those live in internal/cli and not here.
func TestServiceHasNoAdminRoutes(t *testing.T) {
	paths := []struct{ method, path string }{
		{http.MethodPost, "/admin/kill"},
		{http.MethodPost, "/admin/clear"},
		{http.MethodGet, "/admin/accounts"},
		{http.MethodPost, "/admin"},
		{http.MethodGet, "/"},
		{http.MethodGet, "/healthz"},
		{http.MethodPost, "/v1/models"},
		{http.MethodPost, "/creds/marks"},
		{http.MethodGet, MessagesPath},
		{http.MethodPost, MessagesPath + "/"},
		{http.MethodPost, CountTokensPath + "/x"},
		{http.MethodPost, MessagesPath + "/../admin"},
	}
	for _, tc := range paths {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			s := newSvc(t)
			resp, body := s.postRaw(t, tc.method, tc.path, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+s.jobToken())
				r.Header.Set(HeaderRunIdentity, s.runToken())
				r.Header.Set(HeaderRunJob, s.job)
			})
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("the answer is %d, want 404: this service serves two paths", resp.StatusCode)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != "not_found" {
				t.Errorf("the refusal is %q, want not_found", got.Error)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d requests, want none", n)
			}
		})
	}
}

// TestServiceNeverLogsHeaders: the no-echo rule over the whole of what a call
// leaves behind. The service's log holds no token and no credential value, a
// header it does not know does not reach Anthropic beside a subscription
// credential, and this service's own two identity headers never leave it.
func TestServiceNeverLogsHeaders(t *testing.T) {
	s := newSvc(t)
	const marker = "marker-only-this-call-carried"
	runTok, jobTok := s.runToken(), s.jobToken()
	resp, body := s.callTokens(t, callBody(marker), runTok, jobTok, s.job, func(r *http.Request) {
		r.Header.Set("X-Test-Marker", marker)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	req := s.up.Seen()[0]
	if got := req.Header.Get("X-Test-Marker"); got != "" {
		t.Error("a header the client invented reached Anthropic beside a subscription credential")
	}
	if got := req.Header.Get(HeaderRunIdentity); got != "" {
		t.Error("the run's identity token reached Anthropic")
	}
	if got := req.Header.Get(HeaderRunJob); got != "" {
		t.Error("the run's job name reached Anthropic")
	}
	for _, secret := range []string{runTok, jobTok, marker, fakeCred(t, "default"), fakeCred(t, testAccount), fakeCred(t, "paid")} {
		if strings.Contains(s.logs.String(), secret) {
			t.Fatal("a value a call carried is in the service's log")
		}
		if strings.Contains(string(body), secret) {
			t.Fatal("a value a call carried is in the answer")
		}
	}
	if got := s.logs.String(); got != "" {
		t.Errorf("a call the service answered wrote a log line: %s", got)
	}
}

// TestServiceRefusesWhenKilled: the operator's stop flag is the switch they
// already have (the owner's answer of 2026-10-19), and with it on the service
// issues nothing at all. The bundle is read after the flag, so a stopped service
// reads no credential — not even to decide what it would have offered.
func TestServiceRefusesWhenKilled(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) { s.kills.global = true })
	resp, body := s.call(t, callBody("killed"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(e.Message, "global") {
		t.Errorf("the refusal does not name the switch that stopped it: %s", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("a stopped service read the bundle %d times, want none", n)
	}
}

// TestServiceStopsOnKillWithoutFallback: the stop flag is not a credential
// failure, so nothing falls back from it. The policy spends (`fallback: auto`)
// and the project's own key is in the bundle and available; a kill switch that
// a call could route around by spending money would be a switch that does not
// stop the service.
func TestServiceStopsOnKillWithoutFallback(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) { s.kills.repos[s.slug] = true })
	resp, body := s.call(t, callBody("repo-killed"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(e.Message, "repo") {
		t.Errorf("the refusal does not name the switch that stopped it: %s", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the paid key is not a way around the switch", n)
	}
}

// ---------------------------------------------------------------------------
// O4's admission, from the service's side
// ---------------------------------------------------------------------------

// TestServiceNeverGivesTierAToUnadmittedRepository is O4's first leg: a
// repository whose own node names no account is admitted to nothing, and the
// call is served by the project's own key — which is what the policy admits —
// or refused, but never by a subscription token the repository's administrator
// did not admit it to.
func TestServiceNeverGivesTierAToUnadmittedRepository(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		delete(s.policy.admissions, s.slug)
	})
	resp, _ := s.call(t, callBody("unadmitted"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the paid key's 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Fatalf("%s = %q, want %q: no shared token is admitted to this repository", HeaderTier, got, TierB)
	}
	req := s.up.Seen()[0]
	if got := req.Header.Get("x-api-key"); got != fakeCred(t, "paid") {
		t.Error("the call did not carry the project's own key")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Error("the call carried a subscription credential")
	}
	// The admission was consulted, and it is what decided this: an absent child
	// is an empty admission and not a reason to skip the question.
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want this repository's own node", got)
	}
}

// TestServiceTierBOnlyIgnoresAdmissions: O4's admission is a question about a
// shared token, so a bundle that holds none is a bundle whose call never asks
// it. A service that read an admission anyway would be reading a node for an
// answer it could not use — and, on a repository whose child is missing or
// unreadable, would refuse a call the project's own key can serve.
func TestServiceTierBOnlyIgnoresAdmissions(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{testBundlePaidOnlyDoc(t)}
	})
	resp, _ := s.call(t, callBody("paid-only"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierB)
	}
	if got := s.policy.readAdmissions(); len(got) != 0 {
		t.Errorf("the admissions read were %v, want none: there is no shared token to admit", got)
	}
}

// TestServiceIgnoresAChildWiderThanTheSharedNode: O4's intersection, and the
// reason it is an intersection rather than a read of the child. The
// repository's own node names both accounts; the shared node offers one of them
// to the other repository only. The child is what an administrator wrote once
// and can be wider than the policy — two concurrent publishes are a multi-path
// patch with no compare-and-set, and a node can be edited by hand — so the
// account the shared node does not offer this repository is not a candidate.
func TestServiceIgnoresAChildWiderThanTheSharedNode(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAccounts("auto",
			`"default": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]},
			"`+testAccount+`": {"allow_repos": ["`+testOtherRepo+`"]}`))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default", testAccount))
	})
	resp, _ := s.call(t, callBody("wider-child"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderCredential); got != "default" {
		t.Fatalf("%s = %q, want default: the shared node does not offer alice to this repository", HeaderCredential, got)
	}
	auth := s.up.Seen()[0].Header.Get("Authorization")
	if strings.Contains(auth, fakeCred(t, testAccount)) {
		t.Fatal("the call carried an account the shared node does not offer this repository")
	}
}

// TestServiceRefusesWhenTheAdmissionCannotBeRead: an unreadable node is a
// refusal and not an empty admission. The two answers differ — an empty
// admission means "this repository has none", a refusal means "nobody can say" —
// and a reader that turned the second into the first would serve a call by luck.
func TestServiceRefusesWhenTheAdmissionCannotBeRead(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) { s.policy.admErr = errors.New("the node could not be read") })
	resp, body := s.call(t, callBody("unreadable"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
}

// ---------------------------------------------------------------------------
// The ended run's placement (review finding N4)
// ---------------------------------------------------------------------------

// TestServiceForgetsAnEndedRunsPlacement is the N4 cleanup: C1.9 never deletes
// a run's creds/current node and deletes a lease only once it has lapsed, so
// without this the nodes of every run the project has ever had accumulate. The
// call whose liveness check learns a run has ended is where the deletion
// belongs, and it is one atomic Patch of both nodes because the pair is one fact
// about the run.
func TestServiceForgetsAnEndedRunsPlacement(t *testing.T) {
	s := newSvc(t)
	r := s.credsRun()
	// The run's own placement, a sibling run's, and a node outside the two
	// paths: the write is one run's placement and not the subtree.
	other := creds.Run{Slug: r.Slug, ID: r.ID + "-other"}
	s.nodes.nodes[creds.PathCurrent(r)] = "alice"
	s.nodes.nodes[creds.PathLease(r)] = map[string]any{"name": "alice"}
	s.nodes.nodes[creds.PathCurrent(other)] = "default"
	s.nodes.nodes["creds/marks/alice"] = map[string]any{"state": "available"}

	// The run's write-once outcome is written: it has ended.
	s.db.set(budget.PathOutcome(budget.Day(s.now), r.Slug, r.ID))

	resp, body := s.call(t, callBody("ended"))
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: the run has ended", resp.StatusCode, StatusUnverified)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
	if n := s.nodes.callCount(); n != 1 {
		t.Fatalf("the cleanup made %d database writes, want 1: the pair is one fact", n)
	}
	want := []string{"creds/marks/alice", creds.PathCurrent(other)}
	slices.Sort(want)
	if got := s.nodes.under("creds/"); !slices.Equal(got, want) {
		t.Errorf("creds/ holds %v, want %v: one run's placement was removed", got, want)
	}
	if got := s.logs.String(); got != "" {
		t.Errorf("a cleanup that landed wrote a log line: %s", got)
	}
}

// TestServiceRefusesAnEndedRunWhateverTheCleanupDoes: the refusal is answered
// whether or not the deletion landed — a caller whose run has ended must be
// refused either way — and the failure is a line in the log that carries no
// value. The next call for that run tries again.
func TestServiceRefusesAnEndedRunWhateverTheCleanupDoes(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) { s.nodes.err = errors.New("the database refused the write") })
	r := s.credsRun()
	s.db.set(budget.PathOutcome(budget.Day(s.now), r.Slug, r.ID))
	runTok, jobTok := s.runToken(), s.jobToken()
	resp, body := s.callTokens(t, callBody("ended-failed"), runTok, jobTok, s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnverified)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	logs := s.logs.String()
	if !strings.Contains(logs, "could not be removed") {
		t.Errorf("the failed cleanup wrote no line: %q", logs)
	}
	for _, secret := range []string{runTok, jobTok, fakeCred(t, "default"), fakeCred(t, testAccount)} {
		if strings.Contains(logs, secret) {
			t.Fatal("a value the call carried is in the line a failed cleanup wrote")
		}
	}
}
