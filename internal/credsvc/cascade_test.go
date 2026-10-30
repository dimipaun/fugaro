package credsvc

// The cascade's tests (C1.11): which credential a call goes to, what an answer
// means, and what the run is told when nothing can serve it.
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback, the policy is a decoded document,
// the state is a map, and nothing in this file is, or looks like, a credential
// (Convention 2) — every value is built at run time by fakeCred.

import (
	"bufio"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// waitFor polls until f is true, or fails the test having waited long enough.
//
// It exists for the one mark this service writes after the answer has reached
// the client: a usage limit inside a stream is learned as the stream ends, so
// the write races the client's read of the last byte, and a test that read the
// state once would be a test that is sometimes red.
func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waited 10s for %s", what)
}

// hold writes an exhausted mark on a credential, as a call before this one
// would have: the exhaustion opened at since and holds until until.
func (s *svc) hold(name string, since, until time.Time) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.marks[name] = creds.Mark{State: creds.StateExhausted, Since: since, Until: until}
}

// ---------------------------------------------------------------------------
// The red step
// ---------------------------------------------------------------------------

// TestCascadeNeverRetriesAfterStreamStarted is the plan's red step (C1.11) and
// the boundary of the retry rule: a usage limit that arrives INSIDE a 200
// stream has already given the client its bytes, so the call is the client's
// and the credential is marked rather than retried.
//
// The strongest assertion here is the fake upstream's own: it answers exactly
// one request and fails the test on a second (anthropicfake.serve), so a
// service that retried would fail this test twice over. The mark is read
// through waitFor because the service writes it once the last byte is on its
// way, which is after the client's read has returned.
func TestCascadeNeverRetriesAfterStreamStarted(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Events: []anthropicfake.Event{
			anthropicfake.StartEvent(testModel, testUsage),
			{Name: "error", Data: `{"type":"error","error":{"type":"` + rateLimitEventType + `","message":"usage limit reached"}}`},
		},
	}}
	resp := s.post(t, MessagesPath, callBody("stream-limit"), func(r *http.Request) {
		r.Header.Set("Accept", "text/event-stream")
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: a limit inside a stream is the client's answer", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderCredential); got != "alice" {
		t.Fatalf("%s = %q, want the credential the round robin picked", HeaderCredential, got)
	}
	var stream strings.Builder
	br := bufio.NewReader(resp.Body)
	for {
		ev := readEvent(t, br)
		if ev == "" {
			break
		}
		stream.WriteString(ev)
	}
	if !strings.Contains(stream.String(), rateLimitEventType) {
		t.Fatalf("the client's stream does not carry the upstream's usage limit:\n%s", stream.String())
	}
	if got := resp.Header.Get(HeaderSwitch); got != "" {
		t.Errorf("%s = %q, want no switch: the call ended where it started", HeaderSwitch, got)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1: a limit inside a stream is never retried", n)
	}
	waitFor(t, "the credential to be marked", func() bool {
		m, ok := s.state.mark("alice")
		return ok && m.State == creds.StateBackedOff
	})
	// The limit named no reset, which is the shape U3 leaves open until a
	// subscription usage limit is observed: C1.9's backoff, not a reset.
	m, _ := s.state.mark("alice")
	if want := s.now.Add(creds.DefaultBackoffInitial); !m.Until.Equal(want) {
		t.Errorf("the mark holds until %v, want %v", m.Until, want)
	}
}

// ---------------------------------------------------------------------------
// The two answers a credential can give
// ---------------------------------------------------------------------------

// TestCascadeSwitchesOnUsageLimitBeforeFirstByte: a 429 arrives as a status, so
// nothing has been streamed and the call is retried — on another credential,
// never on this one. The credential is held until the reset the answer named,
// the client is told which credential answered, and the answer carries the
// switch record of the move.
func TestCascadeSwitchesOnUsageLimitBeforeFirstByte(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{
		{
			Status: http.StatusTooManyRequests,
			Header: http.Header{"Retry-After": []string{"3600"}},
			Body:   `{"type":"error","error":{"type":"` + rateLimitEventType + `","message":"usage limit reached"}}`,
		},
		anthropicfake.MessageOK(testModel, testUsage),
	}
	resp, body := s.call(t, callBody("switch"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the second credential's 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierA)
	}
	if got := resp.Header.Get(HeaderCredential); got != "default" {
		t.Errorf("%s = %q, want default: alice answered the usage limit", HeaderCredential, got)
	}
	sw, ok, err := SwitchHeader(resp.Header)
	if err != nil {
		t.Fatalf("the switch header is not one this contract can read: %v", err)
	}
	if !ok {
		t.Fatal("the answer carries no switch header: this call moved")
	}
	if sw.From != "alice" || sw.To != "default" || sw.Reason != ReasonExhausted {
		t.Errorf("switch = %+v, want alice -> default because it was exhausted", sw)
	}
	if strings.Contains(string(body), rateLimitEventType) {
		t.Fatal("the upstream's usage limit reached the client: it is about a credential the run does not hold")
	}
	if n := s.up.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2: a 429 before any byte is retried once", n)
	}
	// The retry went out with the second credential: a retry on the same one
	// would be a second charge for one call.
	if got, want := s.up.Seen()[1].Header.Get("Authorization"), "Bearer "+fakeCred(t, "default"); got != want {
		t.Errorf("the retry's Authorization is not the second credential's")
	}
	m, ok := s.state.mark("alice")
	if !ok || m.State != creds.StateExhausted {
		t.Fatalf("alice's mark is %q/%v, want an exhaustion until the reset the answer named", m.State, ok)
	}
	if want := s.now.Add(time.Hour); !m.Until.Equal(want) {
		t.Errorf("the hold is until %v, want %v (Retry-After: 3600)", m.Until, want)
	}
}

// TestCascade401MarksBroken: D139's mark, and the answer the run is not shown.
// A 401 says the credential is dead and only an operator clears it (C1.21), so
// the credential is taken out of rotation and the call moves on; the upstream's
// own authentication error is not forwarded, because it is about a credential
// the run does not hold.
func TestCascade401MarksBroken(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{
		{Status: http.StatusUnauthorized, Body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`},
		anthropicfake.MessageOK(testModel, testUsage),
	}
	resp, body := s.call(t, callBody("broken"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the second credential's 200", resp.StatusCode)
	}
	if strings.Contains(string(body), "authentication_error") {
		t.Fatal("the upstream's authentication error reached the client: it is about a credential the run does not hold")
	}
	if got := resp.Header.Get(HeaderCredential); got != "default" {
		t.Errorf("%s = %q, want default", HeaderCredential, got)
	}
	sw, ok, err := SwitchHeader(resp.Header)
	if err != nil || !ok {
		t.Fatalf("the answer carries no readable switch header (err %v)", err)
	}
	if sw.From != "alice" || sw.To != "default" || sw.Reason != ReasonBroken {
		t.Errorf("switch = %+v, want alice -> default because it answered 401", sw)
	}
	if n := s.up.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2", n)
	}
	m, ok := s.state.mark("alice")
	if !ok || m.State != creds.StateBroken {
		t.Fatalf("alice's mark is %q, want %q", m.State, creds.StateBroken)
	}
}

// TestCascadeOAuthOnlyWaits: D152's other half. Under `oauth-only` the paid
// tier is not in the cascade at all, so a wait that is longer than the
// threshold is still a wait — there is nothing to move to — and the run is told
// the instant rather than served by a key the policy does not admit.
func TestCascadeOAuthOnlyWaits(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocWith("oauth-only"))
	})
	// Both shared tokens are held well past the threshold. The paid key is in
	// the bundle and the project's own node offers it: the policy is what keeps
	// it out, and that is the whole of this test.
	held := s.now.Add(6 * time.Hour)
	s.hold("alice", s.now.Add(-time.Minute), held)
	s.hold("default", s.now.Add(-time.Minute), held)
	resp, body := s.call(t, callBody("wait"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the answer is %d, want the typed wait (%d)", resp.StatusCode, StatusExhaustedUntil)
	}
	eu, ok, err := ReadExhaustedUntil(resp.StatusCode, body)
	if err != nil {
		t.Fatalf("the wait is not one this contract can read: %v", err)
	}
	if !ok {
		t.Fatalf("the answer is not an exhausted_until: %s", body)
	}
	if !eu.ResetAt.Equal(held) {
		t.Errorf("the wait is until %v, want %v", eu.ResetAt, held)
	}
	if len(eu.Tried) != 0 {
		t.Errorf("tried = %+v, want none: nothing was sent", eu.Tried)
	}
	if n := s.up.Count(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want none: the run waits", n)
	}
}

// TestCascadeAutoFallsToB: D152's first half. The same state under
// `fallback: auto` moves to the project's own paid key, which is real money and
// so carries tier B on the answer. It is not a switch: the switch record is for
// a move between credentials of one cascade, and this call's cascade never had
// a credential to move off.
func TestCascadeAutoFallsToB(t *testing.T) {
	s := newSvc(t) // the fixture's policy is `fallback: auto`
	held := s.now.Add(6 * time.Hour)
	s.hold("alice", s.now.Add(-time.Minute), held)
	s.hold("default", s.now.Add(-time.Minute), held)
	resp, body := s.call(t, callBody("paid"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the paid key's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q: the project's own key is real money", HeaderTier, got, TierB)
	}
	if got := resp.Header.Get(HeaderCredential); got != "anthropic-api-key" {
		t.Errorf("%s = %q, want the project's key", HeaderCredential, got)
	}
	if got := resp.Header.Get(HeaderSwitch); got != "" {
		t.Errorf("%s = %q, want none: a tier change is not a switch", HeaderSwitch, got)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	req := s.up.Seen()[0]
	if got := req.Header.Get("x-api-key"); got != fakeCred(t, "paid") {
		t.Error("the call did not carry the project's key")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Error("the call carried an Authorization header a tier-B call must not have")
	}
}

// TestCascadeStickyAcrossCalls: D154's stickiness, and it is checked against
// the round robin rather than in its absence. The second call is made after the
// round-robin counter has moved, so a service that re-ran the tie-break would
// answer with the other credential; the run keeps the one it is on for as long
// as that credential can serve it, because the prompt cache is what the
// stickiness is for.
func TestCascadeStickyAcrossCalls(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}
	first, _ := s.call(t, callBody("first"))
	name := first.Header.Get(HeaderCredential)
	if name == "" {
		t.Fatal("the first answer names no credential")
	}
	if got := first.Header.Get(HeaderSwitch); got != "" {
		t.Errorf("the first answer carries a switch: %s", got)
	}
	if placed, ok := s.state.placed(s.credsRun()); !ok || placed != name {
		t.Fatalf("the run is placed on %q (%v), want %q", placed, ok, name)
	}
	// Move the round robin on, so the tie-break the first call made would now
	// answer with the other credential.
	s.state.mu.Lock()
	s.state.rr++
	s.state.mu.Unlock()

	second, _ := s.call(t, callBody("second"))
	if got := second.Header.Get(HeaderCredential); got != name {
		t.Errorf("the second call went to %q, want %q: a run keeps the credential it is on", got, name)
	}
	if got := second.Header.Get(HeaderSwitch); got != "" {
		t.Errorf("the second answer carries a switch: %s", got)
	}
	if n := s.up.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2", n)
	}
	if got, want := s.up.Seen()[1].Header.Get("Authorization"), s.up.Seen()[0].Header.Get("Authorization"); got != want {
		t.Error("the second call carried a different credential from the first")
	}
}

// TestCascadeUsesAdmittedAccountsOnly: O4's admission, from the cascade's side.
// The repository's own node admits one of the two shared accounts, so the other
// is never a candidate — it is not selected, and it is not sent to. The round
// robin would have picked it first, which is what makes this a test of the
// admission rather than of the order.
func TestCascadeUsesAdmittedAccountsOnly(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default"))
	})
	resp, _ := s.call(t, callBody("admitted"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderCredential); got != "default" {
		t.Fatalf("%s = %q, want default: alice is not admitted to this repository", HeaderCredential, got)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	auth := s.up.Seen()[0].Header.Get("Authorization")
	if !strings.Contains(auth, fakeCred(t, "default")) {
		t.Error("the call did not carry the admitted account's token")
	}
	if strings.Contains(auth, fakeCred(t, testAccount)) {
		t.Fatal("the call carried the account the repository is not admitted to")
	}
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want just this repository's", got)
	}
}

// ---------------------------------------------------------------------------
// The credential never reaches the client in the answer's body
// ---------------------------------------------------------------------------

// chunkedBody hands its bytes out in pieces of at most n, so that a credential
// written across two of them is a credential the redaction has to hold back and
// join rather than one it can only find whole.
type chunkedBody struct {
	data []byte
	n    int
}

func (b *chunkedBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := b.n
	if n > len(p) {
		n = len(p)
	}
	if n > len(b.data) {
		n = len(b.data)
	}
	copy(p, b.data[:n])
	b.data = b.data[n:]
	return n, nil
}

func (b *chunkedBody) Close() error { return nil }

// echoTransport answers every request with one scripted answer, whatever URL
// the service built. It is how a test hands the service a body no
// anthropicfake reply can shape: one that echoes the credential it was sent,
// in pieces.
type echoTransport struct {
	status int
	body   string
	// bodyOf, when set, builds the body from the request instead of using
	// body: it is for the answers that must echo something only the call
	// carries, and it is handed the request as the service sent it upstream.
	bodyOf func(*http.Request) string
	// header is sent as is, for the answers that echo a value in a header.
	header http.Header
	chunk  int
	seen   []*http.Request
}

func (t *echoTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.seen = append(t.seen, r)
	body := t.body
	if t.bodyOf != nil {
		body = t.bodyOf(r)
	}
	h := http.Header{"Content-Type": []string{"application/json"}}
	for k, vs := range t.header {
		h[k] = vs
	}
	n := t.chunk
	if n <= 0 {
		n = len(body)
	}
	return &http.Response{
		StatusCode: t.status,
		Header:     h,
		Body:       &chunkedBody{data: []byte(body), n: n},
		// The body is handed over in pieces and its length is not the
		// declared one, which is what a chunked answer is.
		ContentLength: -1,
		Request:       r,
	}, nil
}

// TestServiceNeverEchoesTheCredentialInTheAnswerBody: the no-echo rule over the
// answer's BODY, for every tier and for an answer that is an error as well as
// one that is the run's result.
//
// The headers were already redacted (copyAnswerHeaders) and the body was not:
// the service pumped the upstream's bytes straight to the client, so an
// upstream that echoed the credential it was handed — in a 200's text, in a
// 400's error message — handed it to the run. Every credential this service
// attaches is a secret value, tier B's paid key as much as tier A's token, so
// the wrap is not the subscription path's alone.
//
// One row splits the echo across two reads: the redaction holds back a tail
// that could still become the credential, so a credential no single chunk holds
// is redacted too, and a service that only searched each chunk would fail here
// with the credential in the client's body.
//
// Mutation (run, restore): pump ans.resp.Body straight to w. Every row is red
// with the credential whole in the client's body — including the split one,
// where the two halves arrive as two writes.
func TestServiceNeverEchoesTheCredentialInTheAnswerBody(t *testing.T) {
	// The upstream's answer echoes the credential it was sent, in the shape an
	// error or a message body would carry it.
	const echo = `{"type":"message","content":[{"type":"text","text":"you sent me $CRED and I am keeping it"}]}`
	oauthOnly := func(t *testing.T) []byte {
		return []byte(config.ClaudeOAuthToken + ": " + fakeCred(t, "default") + "\n")
	}
	keyOnly := func(t *testing.T) []byte {
		return []byte(config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
	}
	cases := []struct {
		name   string
		bundle func(*testing.T) []byte
		what   string
		status int
		split  bool
	}{
		{name: "a tier-A 200 that echoes the token", bundle: oauthOnly, what: "default", status: http.StatusOK},
		{name: "a tier-A 200 that echoes it across two reads", bundle: oauthOnly, what: "default", status: http.StatusOK, split: true},
		{name: "a tier-B 400 that echoes the paid key", bundle: keyOnly, what: "paid", status: http.StatusBadRequest},
		{name: "a tier-B 400 that echoes it across two reads", bundle: keyOnly, what: "paid", status: http.StatusBadRequest, split: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cred := fakeCred(t, tc.what)
			answer := strings.ReplaceAll(echo, "$CRED", cred)
			tr := &echoTransport{status: tc.status, body: answer}
			if tc.split {
				// A piece that ends three bytes into the credential: the
				// boundary falls inside it, so no single read holds it whole.
				tr.chunk = strings.Index(answer, cred) + 3
			}
			s := newSvcWith(t, func(s *svc, c *Config) {
				s.src.docs = [][]byte{tc.bundle(t)}
				c.Client = &http.Client{Transport: tr}
			})
			resp, body := s.call(t, callBody("echo"))
			if resp.StatusCode != tc.status {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			if strings.Contains(string(body), cred) {
				t.Fatalf("the credential the service attached reached the client in the answer's body:\n%s", body)
			}
			if !strings.Contains(string(body), "redacted") {
				t.Errorf("the body carries no redaction marker, so it is not the upstream's answer scrubbed:\n%s", body)
			}
			if !strings.Contains(string(body), "keeping it") {
				t.Errorf("the answer's own text was lost rather than scrubbed:\n%s", body)
			}
			if len(tr.seen) != 1 {
				t.Fatalf("the upstream saw %d requests, want 1", len(tr.seen))
			}
		})
	}
}

// TestServiceRedactsAStreamedAnswerBody is the same rule for the other shape an
// answer comes back in — the streamed one — where the credential is written
// inside an event rather than in a whole body. The redaction wraps the writer
// the pump writes through, so it applies to a stream exactly as to a message;
// the usage-limit scanner still reads the upstream's own bytes, which is why
// the limit event below is still found with the credential redacted around it.
func TestServiceRedactsAStreamedAnswerBody(t *testing.T) {
	redactsStreamedAnswerBody(t, false)
}

// TestServiceRedactsAStreamedAnswerBodyOverH2C is the same pin over the
// protocol the deployment speaks to the container (B1.0): the redaction wraps
// the writer the pump writes through, and over HTTP/2 that writer is a stream
// of DATA frames, so this is where a credential echoed by an upstream could
// reach a run if the wrapper were attached to the HTTP/1.1 path alone.
func TestServiceRedactsAStreamedAnswerBodyOverH2C(t *testing.T) {
	redactsStreamedAnswerBody(t, true)
}

func redactsStreamedAnswerBody(t *testing.T, overH2C bool) {
	cred := fakeCred(t, "default")
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{[]byte(config.ClaudeOAuthToken + ": " + cred + "\n")}
	})
	s.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Events: []anthropicfake.Event{
			anthropicfake.StartEvent(testModel, testUsage),
			{Name: "error", Data: `{"type":"error","error":{"type":"` + rateLimitEventType + `","message":"you sent me ` + cred + `"}}`},
		},
	}}
	post := s.post
	if overH2C {
		post = s.postH2C
	}
	resp := post(t, MessagesPath, callBody("stream-echo"), func(r *http.Request) {
		r.Header.Set("Accept", "text/event-stream")
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200", resp.StatusCode)
	}
	if overH2C && resp.ProtoMajor != 2 {
		t.Fatalf("the answer arrived over HTTP/%d.%d: this is meant to be an h2c call", resp.ProtoMajor, resp.ProtoMinor)
	}
	stream, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if strings.Contains(string(stream), cred) {
		t.Fatalf("the credential reached the client in the stream:\n%s", stream)
	}
	if !strings.Contains(string(stream), "redacted") {
		t.Errorf("the stream carries no redaction marker:\n%s", stream)
	}
	if !strings.Contains(string(stream), rateLimitEventType) {
		t.Errorf("the streamed answer's own text was lost rather than scrubbed:\n%s", stream)
	}
}

// TestServiceNeverEchoesTheCallersTokensInTheAnswerBody: the answer is scrubbed
// of the caller's own tokens — the run identity this service verified and the
// job's ID token it read from Authorization — as well as of the credential. An
// upstream that echoed one of them, in a message's text or in an error, must
// not have it echoed on to the run.
//
// The two tokens do not reach the upstream through this service at all
// (TestServiceNeverSendsTheCallersTokensUpstream pins that), so what this pins
// is the second line of the same rule: whatever an answer carries, the tokens
// the call presented are scrubbed out of it before the run reads it — the same
// rule, over the same writer, that the credential gets, so the two cannot drift
// apart.
//
// Mutation (run, restore): scrub only ans.credential in answer. The body is red
// with both tokens in the client's hands.
func TestServiceNeverEchoesTheCallersTokensInTheAnswerBody(t *testing.T) {
	tr := &echoTransport{status: http.StatusOK}
	s := newSvcWith(t, func(_ *svc, c *Config) { c.Client = &http.Client{Transport: tr} })
	// The fixture's tokens are a fixed claim set signed with a fixed key, so
	// these are the two strings this call presents.
	run, job := s.runToken(), s.jobToken()
	tr.body = `{"type":"error","error":{"message":"you sent me ` + run + ` and ` + job + `"}}`

	resp, body := s.call(t, callBody("token-echo"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	for what, tok := range map[string]string{"the run identity": run, "the job's ID token": job} {
		if strings.Contains(string(body), tok) {
			t.Fatalf("%s reached the client in the answer's body:\n%s", what, body)
		}
	}
	if !strings.Contains(string(body), redactedValue) {
		t.Errorf("the body carries no redaction marker, so it is not the upstream's answer scrubbed:\n%s", body)
	}
	if !strings.Contains(string(body), "you sent me") {
		t.Errorf("the answer's own text was lost rather than scrubbed:\n%s", body)
	}
}

// TestServiceRedactsAnAnswerHeaderThatEchoesTheCredential: the no-echo rule is
// over the whole answer and not only its body. An upstream that echoed the
// credential it was handed — in a request id, in an error header — must not
// have it echoed on to the run, and gateway.RedactHeaders deletes the header
// rather than editing its value, so a header that carried the credential is
// gone rather than half-removed. The header that carried nothing is untouched,
// which is what makes this a rule about the value and not "drop every header
// that is not ours".
//
// Mutation (run, restore): drop the gateway.RedactHeaders call in
// copyAnswerHeaders. The answer reaches the client with the credential whole in
// the header the upstream echoed it in.
func TestServiceRedactsAnAnswerHeaderThatEchoesTheCredential(t *testing.T) {
	cred := fakeCred(t, "default")
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{[]byte(config.ClaudeOAuthToken + ": " + cred + "\n")}
	})
	s.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Header: http.Header{
			"X-Request-Id": []string{cred},
			"X-Echoed":     []string{"the token " + cred + " was seen"},
			"X-Clean":      []string{"untouched"},
		},
		Body: `{"ok":true}`,
	}}
	resp, body := s.call(t, callBody("header-echo"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	for name, vals := range resp.Header {
		for _, v := range vals {
			if strings.Contains(v, cred) {
				t.Errorf("the credential reached the client in %s: %s", name, v)
			}
		}
	}
	for _, name := range []string{"X-Request-Id", "X-Echoed"} {
		if got := resp.Header.Get(name); got != "" {
			t.Errorf("%s = %q, want the header deleted: it carried the credential", name, got)
		}
	}
	if got := resp.Header.Get("X-Clean"); got != "untouched" {
		t.Errorf("X-Clean = %q, want it kept: it carried no credential", got)
	}
	if strings.Contains(string(body), cred) {
		t.Errorf("the credential reached the client in the body:\n%s", body)
	}
}

// TestServiceNamesTheTriedCredentialsOnEveryRefusal: the tried list is carried
// on every refusal this path writes — the typed wait carries it structurally
// and a refusal that halts carries it in the same field of the same body — so a
// run that halts is told what was considered and why each one was skipped,
// whether the answer was "wait" or "nothing".
//
// This is the halting shape: alice is not admitted to this repository (so she
// is skipped before anything is sent), and the account that is admitted answers
// 401, which marks it broken and leaves nothing to send to. The service refuses
// with the whole list — the skip and the mark — and the refusal is the one the
// runner halts on rather than the typed wait.
//
// Mutation (run, restore): drop Tried from the WireError in refuseSelection's
// non-wait branch. The refusal reaches the client with no tried list at all.
func TestServiceNamesTheTriedCredentialsOnEveryRefusal(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) {
		// Two shared tokens and no paid key: the bundle is the whole cascade,
		// so nothing but the two below can be considered or selected.
		s.src.docs = [][]byte{[]byte(config.ClaudeOAuthToken + ": " + fakeCred(t, "default") + "\n" +
			config.ClaudeOAuthTokenPrefix + testAccount + ": " + fakeCred(t, testAccount) + "\n")}
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default"))
	})
	s.up.Script = []anthropicfake.Reply{{Status: http.StatusUnauthorized, Body: `{"type":"error","error":{"type":"authentication_error"}}`}}

	resp, body := s.call(t, callBody("nothing-left"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	want := []Tried{
		{Name: testAccount, Tier: TierA, Reason: ReasonNotAdmitted},
		{Name: config.DefaultClaudeAccount, Tier: TierA, Reason: ReasonBroken},
	}
	if !slices.Equal(got.Tried, want) {
		t.Errorf("the refusal's tried list is %+v, want %+v", got.Tried, want)
	}
	if strings.Contains(got.Message, fakeCred(t, "default")) {
		t.Errorf("the refusal's message carries a credential value: %q", got.Message)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: nothing is retried once every candidate is out", n)
	}
}

// ---------------------------------------------------------------------------
// A policy with no credentials block
// ---------------------------------------------------------------------------

// TestSettingsOfReadsAPolicyWithNoCredentialsBlock: the `credentials` block is
// optional in a published policy — Decode accepts a document without it and
// leaves Policy.Credentials nil — and an installation that writes none is an
// installation whose every call reads the protective defaults. Reading D142's
// two controls off the block rather than through the policy's own nil-safe
// accessors was a nil dereference on exactly that call, so this test is the one
// the function's own doc promised and did not have.
//
// The written opt-outs are in the same table deliberately: the fix must not
// flatten the two pointers into the accessors' answers, because "the operator
// wrote false" and "the operator wrote nothing" are different states
// (StopsAfterExhaustion, ConcurrencyCap) and only a table that reads both tells
// the two apart.
func TestSettingsOfReadsAPolicyWithNoCredentialsBlock(t *testing.T) {
	cases := []struct {
		name        string
		policy      *projectpolicy.Policy
		wantFall    creds.Fallback
		wantStops   bool
		wantConc    int
		wantBackoff time.Duration
	}{
		{
			name:        "no policy at all",
			policy:      nil,
			wantFall:    creds.OAuthOnly,
			wantStops:   true,
			wantConc:    1,
			wantBackoff: projectpolicy.DefaultBackoffCap,
		},
		{
			name:        "a policy with no credentials block",
			policy:      decodeTestPolicy(t, testPolicyDocNoCredentials()),
			wantFall:    creds.OAuthOnly,
			wantStops:   true,
			wantConc:    1,
			wantBackoff: projectpolicy.DefaultBackoffCap,
		},
		{
			name:        "the written opt-outs",
			policy:      decodeTestPolicy(t, testPolicyDocControls("oauth-only", false, 0)),
			wantFall:    creds.OAuthOnly,
			wantStops:   false,
			wantConc:    0,
			wantBackoff: projectpolicy.DefaultBackoffCap,
		},
		{
			name:        "the written cascade",
			policy:      decodeTestPolicy(t, testPolicyDocControls("auto", true, 3)),
			wantFall:    creds.Auto,
			wantStops:   true,
			wantConc:    3,
			wantBackoff: projectpolicy.DefaultBackoffCap,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := settingsOf(tc.policy)
			if got.Fallback != tc.wantFall {
				t.Errorf("Fallback = %v, want %v", got.Fallback, tc.wantFall)
			}
			if got.StopAfterExhaustionPerWindow == nil {
				t.Fatal("StopAfterExhaustionPerWindow is nil: the control in force is not readable")
			}
			if *got.StopAfterExhaustionPerWindow != tc.wantStops {
				t.Errorf("StopAfterExhaustionPerWindow = %v, want %v",
					*got.StopAfterExhaustionPerWindow, tc.wantStops)
			}
			if got.ConcurrencyPerToken == nil {
				t.Fatal("ConcurrencyPerToken is nil: the control in force is not readable")
			}
			if *got.ConcurrencyPerToken != tc.wantConc {
				t.Errorf("ConcurrencyPerToken = %d, want %d", *got.ConcurrencyPerToken, tc.wantConc)
			}
			if got.WaitThreshold != projectpolicy.DefaultWaitThreshold {
				t.Errorf("WaitThreshold = %v, want the default %v", got.WaitThreshold, projectpolicy.DefaultWaitThreshold)
			}
			if got.BackoffCap != tc.wantBackoff {
				t.Errorf("BackoffCap = %v, want %v", got.BackoffCap, tc.wantBackoff)
			}
		})
	}
}

// TestServiceServesUnderAPolicyWithNoCredentialsBlock is the same case end to
// end, because the unit above proves the settings and not the call: settingsOf
// runs on every call, so an installation whose policy has no credentials block
// must answer one rather than panic.
//
// The second half is what makes the first half worth anything: the defaults are
// the PROTECTIVE ones, so a held pair of shared tokens under a credentials-less
// policy is the typed wait and not the project's paid key. `auto` would have
// reached the key, and only oauth-only — the default — leaves the upstream
// untouched.
func TestServiceServesUnderAPolicyWithNoCredentialsBlock(t *testing.T) {
	t.Run("a call is served", func(t *testing.T) {
		s := newSvcWith(t, func(s *svc, _ *Config) {
			s.policy.shared = decodeTestPolicy(t, testPolicyDocNoCredentials())
		})
		resp, body := s.call(t, callBody("no-block"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderTier); got != TierA {
			t.Errorf("%s = %q, want %q: the default fallback offers the shared tier",
				HeaderTier, got, TierA)
		}
		if n := s.up.Count(); n != 1 {
			t.Fatalf("the upstream saw %d requests, want 1", n)
		}
	})

	t.Run("the defaults are the protective ones", func(t *testing.T) {
		s := newSvcWith(t, func(s *svc, _ *Config) {
			s.policy.shared = decodeTestPolicy(t, testPolicyDocNoCredentials())
		})
		held := s.now.Add(6 * time.Hour)
		s.hold("alice", s.now.Add(-time.Minute), held)
		s.hold("default", s.now.Add(-time.Minute), held)
		resp, body := s.call(t, callBody("no-block-wait"))
		if resp.StatusCode != StatusExhaustedUntil {
			t.Fatalf("the answer is %d, want the typed wait (%d): %s",
				resp.StatusCode, StatusExhaustedUntil, body)
		}
		if n := s.up.Count(); n != 0 {
			t.Fatalf("the upstream saw %d requests, want none: oauth-only does not reach the paid key", n)
		}
	})
}
