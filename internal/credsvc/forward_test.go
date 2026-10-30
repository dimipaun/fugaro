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
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
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
// holds each event after the first back for a second, so an answer that were
// read whole before it was written could not deliver the first event until five
// seconds had passed — and the clock is started before the call is made, because
// a service that buffered would hold the response HEADERS back with the body and
// the client's own call would be what blocked.
func TestServiceStreamsUnbuffered(t *testing.T) {
	streamsUnbuffered(t, false)
}

// TestServiceStreamsUnbufferedOverH2C is the same pin over the protocol the
// deployment speaks to the container (B1.0): Cloud Run's front end talks HTTP/2
// cleartext to a service whose port is named h2c, so what the run reads has to
// arrive event by event over that protocol too. HTTP/2 is a different answer
// path in net/http -- frames, flow control and a write scheduler of its own --
// and a service that streamed over one and buffered over the other would look
// healthy in every test that used the first.
func TestServiceStreamsUnbufferedOverH2C(t *testing.T) {
	streamsUnbuffered(t, true)
}

func streamsUnbuffered(t *testing.T, overH2C bool) {
	s := newSvc(t)
	reply := anthropicfake.StreamOK(testModel, testUsage)
	reply.EventDelay = time.Second
	s.up.Script = []anthropicfake.Reply{reply}

	post := s.post
	if overH2C {
		post = s.postH2C
	}
	start := time.Now()
	resp := post(t, MessagesPath, callBody("stream"), func(r *http.Request) {
		r.Header.Set("Accept", "text/event-stream")
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	if overH2C && resp.ProtoMajor != 2 {
		t.Fatalf("the answer arrived over HTTP/%d.%d: this is meant to be an h2c call", resp.ProtoMajor, resp.ProtoMinor)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want an event stream", got)
	}
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
// leaves behind. The service's log holds no token and no credential value, and
// this service's own two identity headers never leave it — the run's Firebase ID
// token and the job's Google ID token are this contract's, and Anthropic has no
// use for either.
//
// The two tiers are two different questions and both are asked, because the two
// upstream paths are built by two different helpers: the shared tier's is an
// allow-list (Check 90's, and a header the client invented cannot ride beside a
// subscription credential), while the paid tier's is the gateway's usual
// "everything but the credentials" — and that one would carry this contract's
// own headers upstream if the call were not read without them.
func TestServiceNeverLogsHeaders(t *testing.T) {
	const marker = "marker-only-this-call-carried"
	cases := []struct {
		name string
		// paidOnly serves the call from the project's own key: the bundle holds
		// no shared token, so the cascade has no tier A to consider.
		paidOnly bool
		// inventedReaches is whether a header this contract knows nothing about
		// may reach Anthropic on this tier: the shared tier's allow-list drops
		// it, and the paid tier's forward-everything rule does not.
		inventedReaches bool
	}{
		{name: "the shared tier", inventedReaches: false},
		{name: "the project's own key", paidOnly: true, inventedReaches: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvcWith(t, func(s *svc, _ *Config) {
				if tc.paidOnly {
					s.src.docs = [][]byte{testBundlePaidOnlyDoc(t)}
				}
			})
			runTok, jobTok := s.runToken(), s.jobToken()
			resp, body := s.callTokens(t, callBody(marker), runTok, jobTok, s.job, func(r *http.Request) {
				r.Header.Set("X-Test-Marker", marker)
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the answer is %d, want 200", resp.StatusCode)
			}
			req := s.up.Seen()[0]
			if got := req.Header.Get(HeaderRunIdentity); got != "" {
				t.Error("the run's identity token reached Anthropic")
			}
			if got := req.Header.Get(HeaderRunJob); got != "" {
				t.Error("the run's job name reached Anthropic")
			}
			if got := req.Header.Get("Authorization"); strings.Contains(got, jobTok) {
				t.Error("the job's ID token reached Anthropic")
			}
			if got := req.Header.Get("X-Test-Marker"); (got != "") != tc.inventedReaches {
				t.Errorf("a header the client invented reached Anthropic: got %v, want %v", got != "", tc.inventedReaches)
			}
			for _, value := range []string{runTok, jobTok, marker, fakeCred(t, "default"), fakeCred(t, testAccount), fakeCred(t, "paid")} {
				if strings.Contains(s.logs.String(), value) {
					t.Fatal("a value a call carried is in the service's log")
				}
				if strings.Contains(string(body), value) {
					t.Fatal("a value a call carried is in the answer")
				}
			}
			if got := s.logs.String(); got != "" {
				t.Errorf("a call the service answered wrote a log line: %s", got)
			}
		})
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

// TestServiceNeverGivesTierAWhenTheProjectNoLongerAdmitsTheRepository: O4's
// admission is an intersection of three published facts, and the third is the
// shared node's own top-level allow_repos — the leg that closes the
// `project repos rm` gap. That command removes a repository from the
// allow-list and does not touch its child, so a service that read the child and
// the accounts' own allow_repos alone would keep issuing a shared token to a
// repository the project has removed from itself.
//
// The child and both accounts still name this repository: the only thing that
// has changed is the project's own list, which is exactly the state the command
// leaves behind.
//
// Mutation (run, restore): drop the AdmitRepo(shared.AllowRepos, canonical)
// check in admittedAccounts. The answer is tier A and the call carries a shared
// token.
func TestServiceNeverGivesTierAWhenTheProjectNoLongerAdmitsTheRepository(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAllowRepos(`["`+testOtherRepo+`"]`,
			`"default": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]},
			"`+testAccount+`": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]}`))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default", testAccount))
	})
	resp, _ := s.call(t, callBody("removed-from-the-allow-list"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the paid key's 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Fatalf("%s = %q, want %q: the project no longer admits this repository", HeaderTier, got, TierB)
	}
	if got := s.up.Seen()[0].Header.Get("Authorization"); got != "" {
		t.Error("the call carried a shared token to a repository the project removed")
	}
	// The admission was still asked: the answer is "this repository is admitted
	// to nothing", and not "the question was skipped".
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want this repository's own node", got)
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

// ---------------------------------------------------------------------------
// The call's query
// ---------------------------------------------------------------------------

// TestServiceDropsAQueryThatCarriesTheCallersTokens: the gateway's own rule
// over its own token (route.go's upstreamQuery/carriesToken), which C1.8's
// exported helpers do not cover and which the service therefore has to hold
// itself. The query of a call is kept as the client wrote it — the SDK puts
// things there, and a service that dropped every query would change what a
// call means — unless it carries one of the two tokens the caller presented,
// in which case it is dropped WHOLE (an edited query is a query that has been
// half-forwarded) rather than passed on. A token in the query of a call to
// Anthropic is a token in Anthropic's logs.
//
// Mutation (run, restore): append the query unconditionally, or compare it
// without the case fold and the lenient unescape, and the subtests below are
// red.
func TestServiceDropsAQueryThatCarriesTheCallersTokens(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}
	runTok, jobTok := s.runToken(), s.jobToken()
	// A run identity token and a job ID token are JWTs: base64url, so they
	// carry "-" and "_" and no "+", "/" or "=". Encoded for a query they are
	// spelled as they are; the test spells one with the escapes a client could
	// also have used, which is the case the lenient unescape is for, and one
	// with its letters shifted, which is the case the case fold is for.
	encoded := strings.ReplaceAll(strings.ReplaceAll(runTok, "a", "%61"), "E", "%45")
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"a query that carries nothing of the caller's goes upstream", "beta=true", "beta=true"},
		{"the run identity in the query drops the query", "t=" + runTok, ""},
		{"the job's ID token in the query drops the query", "t=" + jobTok, ""},
		{"an escaped run identity is still the run identity", "t=" + encoded, ""},
		{"a recased run identity is still the run identity", "t=" + strings.ToUpper(runTok), ""},
		{"a query that is empty stays empty", "", ""},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := MessagesPath
			if c.query != "" {
				path += "?" + c.query
			}
			resp, body := s.callPath(t, path, callBody("query"), func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+jobTok)
				r.Header.Set(HeaderRunIdentity, runTok)
				r.Header.Set(HeaderRunJob, s.job)
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
			}
			seen := s.up.Seen()
			if len(seen) != i+1 {
				t.Fatalf("the upstream saw %d requests, want %d", len(seen), i+1)
			}
			if got := seen[i].URL.RawQuery; got != c.want {
				t.Errorf("the upstream was called with query %q, want %q", got, c.want)
			}
		})
	}
}

// TestServiceServesBothAnthropicPaths: the other half of the endpoint check.
// TestServiceHasNoAdminRoutes holds every path BUT the two Anthropic ones to a
// 404; this one holds the two the service does serve to a forward, because a
// condition that lost one of them would still refuse everything else and would
// still look like a working service — the run's own calls to that path would
// simply 404, and the SDK's token count is a call the harness makes.
//
// Mutation (run, restore): drop CountTokensPath (or MessagesPath) from
// ServeHTTP's condition, and the subtest for it is red.
func TestServiceServesBothAnthropicPaths(t *testing.T) {
	s := newSvc(t)
	for _, path := range []string{MessagesPath, CountTokensPath} {
		t.Run(path, func(t *testing.T) {
			s.up.Script = append(s.up.Script, anthropicfake.MessageOK(testModel, testUsage))
			before := s.up.Count()
			resp, body := s.callPath(t, path, callBody("path"))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST %s is answered %d, want 200: %s", path, resp.StatusCode, body)
			}
			seen := s.up.Seen()
			if len(seen) != before+1 {
				t.Fatalf("the upstream saw %d requests, want %d", len(seen), before+1)
			}
			if got := seen[before].URL.Path; got != path {
				t.Errorf("the upstream was called at %q, want %q", got, path)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Where a call goes
// ---------------------------------------------------------------------------

// recordingTransport is a RoundTripper that records the URL each request was
// BUILT with and then refuses it. The service's transport is the one thing
// that still sees the destination as the service wrote it: the fixture's
// hostRewriter, which every other test uses, rewrites the host to the fake's
// before the request leaves, so a test that read the URL off the fake could
// not tell api.anthropic.com from anywhere else.
type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.mu.Unlock()
	return nil, errors.New("recordingTransport: no request leaves this test")
}

func (r *recordingTransport) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

// TestServiceSendsTierAOnlyToAnthropicsOwnEndpoint: D133 at the service's own
// boundary. The service has no routes and no Vertex upstream — the two paths
// it forwards on are C1.8's helpers, and a tier-A credential is Anthropic's
// OAuth token, which may only ever go to Anthropic's own endpoint. The URL the
// service builds is therefore part of the rule and not an implementation
// detail: it is checked here on the request the service actually wrote, before
// the test's transport rewrites the host.
//
// The destination rule itself runs inside the helper (gateway.OAuthHeaders'
// destination check, which is a check and not a comment), and a tier-A call
// whose destination is not Anthropic's is refused rather than sent — which is
// what makes this test's subject the built URL and not the helper.
//
// Mutation (run, restore): build the tier-A destination from anything but
// Anthropic's own base. The test is red either way it can go — the helper
// refuses the foreign destination, so the transport sees no request at all,
// and a base the helper accepted but that was not Anthropic's would be caught
// by the URL assertion.
func TestServiceSendsTierAOnlyToAnthropicsOwnEndpoint(t *testing.T) {
	rec := &recordingTransport{}
	s := newSvcWith(t, func(_ *svc, c *Config) {
		c.Client = &http.Client{Transport: rec}
	})
	resp, _ := s.call(t, callBody("destination"))
	if resp.StatusCode == http.StatusOK {
		t.Fatal("the answer is 200: no request may leave a recording transport")
	}
	urls := rec.seen()
	if len(urls) != 1 {
		t.Fatalf("the service built %d requests, want 1: %v", len(urls), urls)
	}
	want := gateway.AnthropicBase(gateway.Upstream{}) + MessagesPath
	if urls[0] != want {
		t.Errorf("the tier-A call was built for %q, want %q: an OAuth token goes to Anthropic's own endpoint or nowhere", urls[0], want)
	}
	// The query is this call's own and the base is Anthropic's, so the
	// destination the rule is checked against is the one the request carries.
	if !strings.HasPrefix(urls[0], "https://api.anthropic.com/") {
		t.Errorf("the tier-A destination %q is not Anthropic's own endpoint", urls[0])
	}
}

// ---------------------------------------------------------------------------
// A redirect is never followed
// ---------------------------------------------------------------------------

// TestUpstreamClientNeverFollowsARedirect is the client rule on its own, for
// both branches: the client the service builds when Config.Client is nil and
// the one a caller passes in. A caller's client is put through the same
// function rather than trusted, so a redirect cannot be followed by a client
// this service was handed.
func TestUpstreamClientNeverFollowsARedirect(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	cases := []struct {
		name string
		in   *http.Client
	}{
		{name: "the service builds one", in: nil},
		{
			name: "a caller passes one that would follow anything",
			in: &http.Client{
				Timeout:       time.Minute,
				Jar:           jar,
				CheckRedirect: func(*http.Request, []*http.Request) error { return nil },
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := upstreamClient(tc.in)
			if got.CheckRedirect == nil {
				t.Fatal("the client has no CheckRedirect: it would follow a redirect")
			}
			req, err := http.NewRequest(http.MethodPost, "https://example.invalid/v1/messages", nil)
			if err != nil {
				t.Fatalf("building the request: %v", err)
			}
			if err := got.CheckRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
				t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse: the answer is returned rather than followed", err)
			}
			if got.Jar != nil {
				t.Error("the client kept a cookie jar: an upstream answer must not set state on this service")
			}
		})
	}
	// A client built here keeps the service's own bound on the whole attempt;
	// one passed in keeps its own, which is the caller's to choose.
	if got := upstreamClient(nil); got.Timeout != upstreamTimeout {
		t.Errorf("the built client's timeout is %v, want %v", got.Timeout, upstreamTimeout)
	}
	if got := upstreamClient(cases[1].in); got.Timeout != time.Minute {
		t.Errorf("a passed-in client's timeout became %v, want the caller's", got.Timeout)
	}
}

// TestServiceNeverFollowsAnUpstreamRedirect: a 307 from the upstream, with the
// Location pointing at another host. The POST and the tier-B key must not go
// there — Go's client strips Authorization across hosts but not x-api-key — and
// the redirect is relayed to the client with no credential in it.
//
// The other host is a real server, so a service that followed the redirect
// would be caught having sent the call: it records every request it is sent and
// this test requires none. The service's client is the one the fixture passes
// in, which is the branch this test exists for.
func TestServiceNeverFollowsAnUpstreamRedirect(t *testing.T) {
	var mu sync.Mutex
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path+" x-api-key="+r.Header.Get("x-api-key"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{testBundlePaidOnlyDoc(t)}
	})
	cred := fakeCred(t, "paid")
	s.up.Script = []anthropicfake.Reply{{
		Status: http.StatusTemporaryRedirect,
		Header: http.Header{"Location": []string{other.URL + MessagesPath}},
		Body:   `{"type":"error","error":{"type":"api_error","message":"moved to ` + cred + `"}}`,
	}}

	// The client that makes the call to the service must not follow the relayed
	// redirect either: this test is about what the service does with the
	// upstream's answer, and a following client would blur the two hops.
	req, err := http.NewRequest(http.MethodPost, s.http.URL+MessagesPath, strings.NewReader(callBody("redirect")))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("the answer is %d, want the upstream's %d relayed", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	mu.Lock()
	got := append([]string(nil), elsewhere...)
	mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("the redirect was followed: the other host saw %v", got)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1: the call is not retried onto the redirect", n)
	}
	if strings.Contains(string(body), cred) {
		t.Fatalf("the credential reached the client in the redirect's body:\n%s", body)
	}
	if got := resp.Header.Get(HeaderCredential); got != "anthropic-api-key" {
		t.Errorf("%s = %q, want the credential that was sent", HeaderCredential, got)
	}
	// The relayed answer names the other host: it is the upstream's own
	// Location, and it carries no credential — the run's gateway is free to
	// refuse it, and nothing here follows it.
	if loc := resp.Header.Get("Location"); loc != other.URL+MessagesPath {
		t.Errorf("Location = %q, want the upstream's own", loc)
	}
}

// ---------------------------------------------------------------------------
// The tokens the call presents
// ---------------------------------------------------------------------------

// TestServiceNeverSendsTheCallersTokensUpstream: the two tokens a call presents
// — the run identity this service verified and the job's ID token — are this
// service's to keep out of Anthropic's logs, and the gateway's own deny-list
// covers one header only (Authorization). A run that put its identity token in
// another header, or pasted either token into the request body, would otherwise
// have that token reach the upstream: so every header carrying either token is
// dropped and the body is scrubbed of both, for every tier.
//
// The body assertion is the pair — no token, and the marker in its place — so a
// service that dropped the body, or refused the call, cannot pass by losing the
// run's own text.
//
// Mutation (run, restore): remove the `gateway.RedactHeaders` loop in send, and
// the header rows are red with the token in the header the upstream was sent;
// remove the scrub in readCall, and the body rows are red with it in the body.
func TestServiceNeverSendsTheCallersTokensUpstream(t *testing.T) {
	cases := []struct {
		name   string
		bundle func(*testing.T) []byte
		tier   string
	}{
		{"a tier-A call", func(t *testing.T) []byte { return testBundleDoc(t) }, TierA},
		{"a tier-B call", testBundlePaidOnlyDoc, TierB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvcWith(t, func(s *svc, _ *Config) { s.src.docs = [][]byte{tc.bundle(t)} })
			run, job := s.runToken(), s.jobToken()
			// The body carries both tokens as a run might have pasted them —
			// in a message's text — and the two extra headers carry them where
			// the contract's own headers do not.
			body := `{"model":"` + testModel + `","max_tokens":16,"messages":[{"role":"user","content":"` + run + ` and ` + job + `"}]}`
			resp, _ := s.call(t, body, func(r *http.Request) {
				r.Header.Set("X-Run-Identity-Copy", run)
				r.Header.Set("X-Job-Token-Copy", job)
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the answer is %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get(HeaderTier); got != tc.tier {
				t.Errorf("%s = %q, want %q", HeaderTier, got, tc.tier)
			}
			req := s.firstUpstream(t)
			for name, vals := range req.Header {
				for _, v := range vals {
					if strings.Contains(v, run) || strings.Contains(v, job) {
						t.Errorf("the upstream was sent the caller's token in %s: %s", name, v)
					}
				}
			}
			// The header is deleted rather than edited, so the name is gone
			// with the value: this contract's own headers by prefix (readCall)
			// and any other header carrying a token by value (send).
			for _, name := range []string{"X-Run-Identity-Copy", "X-Job-Token-Copy", HeaderRunIdentity, HeaderRunJob} {
				if got := req.Header.Get(name); got != "" {
					t.Errorf("the upstream was sent %s = %q, want the header dropped", name, got)
				}
			}
			up := s.up.Body(0)
			if bytes.Contains(up, []byte(run)) || bytes.Contains(up, []byte(job)) {
				t.Fatalf("the body the upstream was sent carries the caller's tokens:\n%s", up)
			}
			if !bytes.Contains(up, []byte(redactedValue)) {
				t.Fatalf("the body was not scrubbed but lost:\n%s", up)
			}
			// The call's own text is still there: the scrub replaces the token
			// rather than the body.
			if !bytes.Contains(up, []byte(" and ")) {
				t.Errorf("the body's own text was lost rather than scrubbed:\n%s", up)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// What a bundle failure tells a run (review finding n1)
// ---------------------------------------------------------------------------

// TestServiceRefusesAnUnreadableBundleWithoutCarryingIt: the service's bundle
// refusal is the CLASS of the failure and never the loader's own text. That
// text names the key it is about — up to 80 bytes of a hand-edited document,
// which can be a value pasted where a name belongs — or, when the source
// itself failed, another package's sentence naming the container and the
// request. Both are the operator's to read; neither is a run's.
//
// Mutation (run, restore): answer err.Error(). The first row is red with the
// pasted value in the refusal and the second with Secret Manager's sentence.
func TestServiceRefusesAnUnreadableBundleWithoutCarryingIt(t *testing.T) {
	pasted := "sk-ant-" + strings.ReplaceAll(t.Name(), "/", "-")
	sourceErr := errors.New("secretmanager: permission denied on projects/aurora/secrets/creds-bundle/versions/latest")
	cases := []struct {
		name  string
		docs  [][]byte
		errs  []error
		class string
		notIn []string
	}{
		{
			name:  "a key that is not a credential name",
			docs:  [][]byte{[]byte(pasted + ": " + fakeCred(t, "x") + "\n")},
			class: ErrBundleUnknownKey.Error(),
			notIn: []string{pasted},
		},
		{
			name:  "a container the source could not read",
			errs:  []error{sourceErr},
			class: ErrBundleUnreadable.Error(),
			notIn: []string{"secretmanager", "permission denied", "projects/aurora", "creds-bundle"},
		},
		{
			name:  "a document that is not a map of names to values",
			docs:  [][]byte{[]byte("- " + config.AnthropicSecret + "\n")},
			class: ErrBundleUnparseable.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvcWith(t, func(s *svc, _ *Config) {
				s.src.docs = tc.docs
				s.src.errs = tc.errs
			})
			resp, body := s.call(t, callBody("unreadable-bundle"))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
			}
			got := refusal(t, resp.StatusCode, body)
			if got.Error != ErrorStateUnavailable {
				t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
			}
			if got.Message != tc.class {
				t.Errorf("the refusal's message is %q, want the class %q", got.Message, tc.class)
			}
			for _, secret := range tc.notIn {
				if strings.Contains(got.Message, secret) {
					t.Errorf("the refusal carries %q, which a run is not told:\n%s", secret, got.Message)
				}
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d requests, want none", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The operator's stop switches
// ---------------------------------------------------------------------------

// TestServiceRefusesWhenTheKillSwitchesCannotBeRead: an unreadable switch is
// not an off switch (budget's own rule), and it is not a serve either. The
// service cannot tell whether the operator has stopped it, so it issues nothing
// and says so in its own sentence — the reader's error is another package's
// text and the run is not the operator.
//
// It is read before the bundle, so a service whose switches cannot be read
// reads no credential at all, which the bundle read count pins.
//
// Mutation (run, restore): answer `false, nil` from an unreadable switch (a
// fail-open read). The call is served, the answer is 200, and this test is red
// on the status, the refusal and the upstream's count at once.
func TestServiceRefusesWhenTheKillSwitchesCannotBeRead(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.kills.err = errors.New("the outcomes node could not be read")
	})
	resp, body := s.call(t, callBody("unreadable-switch"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	if strings.Contains(got.Message, "outcomes node") {
		t.Errorf("the refusal carries the reader's own text: %q", got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
	if n := s.kills.readCount(); n != 1 {
		t.Errorf("the switches were read %d times, want 1", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the switch is read first", n)
	}
}
