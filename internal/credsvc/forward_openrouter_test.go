package credsvc

// DS3.5's tests: the OpenRouter upstream through this service (F1, U1).
//
// U1 asked whether the service's forward already serves an OpenRouter upstream,
// and the answer these tests pin is yes — it has since AK6b gave the bundle the
// project's provider key. A provider entry carries the two facts such a call
// needs, where it goes and how the key is sent (D1418), and OpenRouter's
// Anthropic-compatible endpoint is its own base URL joined to the path this
// service serves with a bearer key: the provider path IS the OpenRouter path.
// Nothing outside this file changed for this task, and these two tests are the
// evidence for that answer.
//
// Why they drive send and answer rather than the messages route: a served
// call's candidate list is the cascade's, and creds.Select refuses a tier-C
// candidate by name until Check 50 runs (D150, C3.6, creds.checkCandidates).
// Moving that gate is the owner's live check and a product decision, not this
// task's; what the service owes OpenRouter today is that the upstream half is
// the provider half, exactly — the destination, the key it is sent with, the
// stream that comes back and the key that never comes back with it — and that
// is what is pinned here.
//
// Everything here is hermetic (Convention 9). The base URL is OpenRouter's own
// — the one the owner's local config writes (docs/multi-model.md §2) — and the
// fixture's client rewrites the host to a loopback fake, so the URL the service
// builds is the real one and no request leaves the machine. Nothing here is, or
// looks like, a credential (Convention 2): every value comes from fakeCred.
//
// What this file does not restate is the provider path's general rules, which
// the AK6b tests beside it already pin for a provider entry: the call's query
// follows it unless it carries a caller token, a redirect is not followed, the
// key is attached in the shape the entry names (forward_test.go). OpenRouter
// adds no rule of its own to any of them, so the pins here are the ones its own
// base and its own stream need — and a rule duplicated here would be a second
// reading of the same code that could disagree with the first.

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// openRouterBase is OpenRouter's own Anthropic-compatible base: the URL the
// owner writes into a providers entry (docs/multi-model.md §2) and the one a
// bundle's provider entry holds. It is a URL and not a credential.
const openRouterBase = "https://openrouter.ai/api"

// openRouterModel is the provider model ID a call to OpenRouter names in its
// body. It is not a credential either, and it matters here for what it says
// about the service: the body is the caller's and is forwarded whole — the
// entry the service selects says where the call goes, never what it asks for,
// and the model a provider serves is the route's and the pin's business.
const openRouterModel = "deepseek/deepseek-v4.1-flash"

// openRouterCall is one call as the run's in-runner gateway sends it to this
// service: the job's own ID token in Authorization — the value the service
// replaces with the provider's key, and the one that must never reach a third
// party — the contract's headers, and the event stream asked for. own is that
// token, named as the scrub the service reads, so the call's body carrying it
// too is covered.
func openRouterCall(t *testing.T, own string) *call {
	t.Helper()
	body := `{"model":"` + openRouterModel + `","max_tokens":16,"messages":[{"role":"user","content":"` + own + `"}]}`
	req, err := http.NewRequest(http.MethodPost, MessagesPath, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+own)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	cl, err := readCall(req, []string{own})
	if err != nil {
		t.Fatalf("reading the call: %v", err)
	}
	return cl
}

// openRouterServed is what a served OpenRouter call would answer with: the
// project's provider key at tier C — the third-party route's credential
// (AK6b), which is the one a call to OpenRouter is settled against — and the
// fixture's own verified run.
func openRouterServed(s *svc) (Identity, creds.Credential) {
	return Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA, JobClass: ClassRepository},
		creds.Credential{Name: config.ProviderKeySecret, Tier: creds.TierC}
}

// TestOpenRouterCallSwapsKeyAndStreams is the call itself, and it is the plan's
// red step: with the provider branch of send gone, the call is built for
// Anthropic's own endpoint and carries the project's key as an x-api-key — the
// destination subtest goes red on the URL, and the swap subtest on the bearer
// value the key was to arrive under and on the x-api-key it arrives under
// instead.
//
// Mutation (run, restore): build the provider's destination from
// upstreamKindAnthropic's address rather than the entry's base URL, and the
// destination subtest goes red on the URL — the swap subtest's own path
// assertion goes red with it, since where the call goes is read in both places
// — while the credential the call went with is not what changed. Drop the
// provider branch of send instead and it is the credential that goes red, as
// the bearer value that was expected and as the x-api-key the key arrives
// under. Dropping readCall's scrub of the caller's own token turns the body
// assertion red by itself.
func TestOpenRouterCallSwapsKeyAndStreams(t *testing.T) {
	t.Run("the call is built for OpenRouter's own endpoint", func(t *testing.T) {
		rec := &recordingTransport{}
		s := newSvcWith(t, func(_ *svc, c *Config) {
			c.Client = &http.Client{Transport: rec}
		})
		e := providerEntryFrom(t, openRouterBase, providerAuthBearer)
		if _, err := s.server.send(context.Background(), openRouterCall(t, s.jobToken()), e); err == nil {
			t.Fatal("the call was sent: no request may leave a recording transport")
		}
		urls := rec.seen()
		if len(urls) != 1 {
			t.Fatalf("the service built %d requests, want 1: %v", len(urls), urls)
		}
		if want := openRouterBase + MessagesPath; urls[0] != want {
			t.Errorf("the OpenRouter call was built for %q, want %q: the entry's own base is where OpenRouter is, and the served path is joined to it", urls[0], want)
		}
		if anthropic := gateway.AnthropicBase(gateway.Upstream{}); strings.HasPrefix(urls[0], anthropic) {
			t.Errorf("the OpenRouter call was built for Anthropic's own endpoint (%q), where its key is nobody's business", anthropic)
		}
	})

	t.Run("the project's key is swapped in and the run's own token is not sent", func(t *testing.T) {
		s := newSvc(t)
		own := s.jobToken()
		key := fakeCred(t, "provider-key")
		e := providerEntryFrom(t, openRouterBase, providerAuthBearer)
		cl := openRouterCall(t, own)
		ans, err := s.server.send(context.Background(), cl, e)
		if err != nil {
			t.Fatalf("the OpenRouter call was not sent: %v", err)
		}
		defer ans.resp.Body.Close()
		if ans.credential != key {
			t.Error("the answer's redaction value is not the entry's: the key the call was served by is what has to be held out of the answer")
		}
		if n := s.up.Count(); n != 1 {
			t.Fatalf("OpenRouter saw %d requests, want 1", n)
		}
		req := s.up.Seen()[0]
		if got, want := req.URL.Path, "/api"+MessagesPath; got != want {
			t.Errorf("OpenRouter was called at %q, want the entry's own prefix and the served path", got)
		}
		// The swap: the key the bundle holds is what OpenRouter is handed, and
		// it is handed in exactly one header — the shape the entry names.
		//
		// The value is compared whole rather than searched for the run's token,
		// and that is the stronger reading on this path: Authorization is the
		// one header of a provider's call that could carry a token of the run's
		// (the gateway's allow-list passes five names through and not this one,
		// and the key is written over whatever arrived), so an Authorization
		// that is exactly the key's is one the run's token is not in.
		if got, want := req.Header.Get("Authorization"), "Bearer "+key; got != want {
			t.Error("the call did not go to OpenRouter with the project's key under the bearer shape the entry names")
		}
		if got := req.Header.Get("x-api-key"); got != "" {
			t.Errorf("the call also carried x-api-key = %q: one key, one header", got)
		}
		// The run's own token — the job's ID token, which this service verified
		// and which is no third party's business — reached the body, which is
		// where this call carries it and the one place the key does not
		// overwrite. (The query rule is the provider path's and is pinned with
		// the rest of it, in forward_test.go.)
		body := string(s.up.Body(0))
		if strings.Contains(body, own) {
			t.Errorf("the call to OpenRouter carries the run's own token in its body: %s", body)
		}
		if !strings.Contains(body, redactedValue) {
			t.Errorf("the call's body reached OpenRouter with neither the run's token nor where it stood: %s", body)
		}
		if !strings.Contains(body, openRouterModel) {
			t.Errorf("the call's body reached OpenRouter without the model the caller pinned: %s", body)
		}
	})

	t.Run("the answer streams back to the run", func(t *testing.T) {
		s := newSvc(t)
		reply := anthropicfake.StreamOK(openRouterModel, testUsage)
		// The upstream holds every event after the first back, so an answer the
		// service read whole before it wrote any of it could not deliver the
		// first event inside the bound below.
		reply.EventDelay = time.Second
		s.up.Script = []anthropicfake.Reply{reply}
		e := providerEntryFrom(t, openRouterBase, providerAuthBearer)
		cl := openRouterCall(t, s.jobToken())
		ans, err := s.server.send(context.Background(), cl, e)
		if err != nil {
			t.Fatalf("the OpenRouter call was not sent: %v", err)
		}
		id, cred := openRouterServed(s)
		// The answer path over a socket rather than a recorder, because what
		// this subtest measures is a stream: the handler is the service's own
		// answer, and the only thing a served call adds to it is the candidate
		// list the selection was made from.
		served := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.server.answer(r.Context(), w, id, cl, ans, cred, nil, &usageLimitScanner{})
		}))
		defer served.Close()

		start := time.Now()
		resp, err := served.Client().Get(served.URL)
		if err != nil {
			t.Fatalf("reading the answer: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want OpenRouter's own 200 relayed", resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
			t.Fatalf("Content-Type = %q, want the provider's own event stream", got)
		}
		// The answer is the provider's call and the third tier: C1.16 settles
		// it as money, and the name it is settled under is the entry's.
		if got := resp.Header.Get(HeaderTier); got != TierC {
			t.Errorf("%s = %q, want %q", HeaderTier, got, TierC)
		}
		if got := resp.Header.Get(HeaderCredential); got != config.ProviderKeySecret {
			t.Errorf("%s = %q, want the credential's name %q", HeaderCredential, got, config.ProviderKeySecret)
		}
		first := readEvent(t, bufio.NewReader(resp.Body))
		elapsed := time.Since(start)
		if !strings.Contains(first, "message_start") {
			t.Errorf("the first event is %q, want the stream's own first event", first)
		}
		if elapsed > 250*time.Millisecond {
			t.Errorf("the first event took %v: the answer was buffered rather than streamed", elapsed)
		}
	})
}

// TestOpenRouterKeyNeverReachesJob: the key the bundle holds goes to OpenRouter
// and nowhere else. It is read on the answer the run receives — the body and
// every header of it — and on the line the operator gets, and the two are the
// whole of what leaves this service once the call is made. The upstream here
// echoes the key it was handed, in its body and in a header, which is what a
// provider reporting the credential it refused does; the run is shown neither.
//
// What it does not check is the run's own token on the way out — that is the
// swap test above, which is where the token is put on the call — because an
// upstream that never saw it cannot echo it, and an assertion here that read
// the answer for a value the answer could not carry would hold whatever this
// service did.
//
// The test is not vacuous, and that is the first thing it checks: the upstream
// really was handed the key, so what the rest of it asserts is that the answer
// does not carry it rather than that nothing ever held it.
//
// Mutation (run, restore): drop the credential from the answer's redaction —
// copyAnswerHeaders' last line, or the writers answer chains over the body —
// and the key reaches the run in the body or in the echoed header, which is the
// spec's "proxied rather than handed to jobs" undone.
func TestOpenRouterKeyNeverReachesJob(t *testing.T) {
	s, auditLog := auditSvc(t, nil)
	key := fakeCred(t, "provider-key")
	own := s.jobToken()
	s.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"X-Openrouter-Key": []string{key},
		},
		Body: `{"id":"msg_fake","type":"message","model":"` + openRouterModel + `",` +
			`"content":[{"type":"text","text":"the key ` + key + ` was not accepted"}]}`,
	}}
	e := providerEntryFrom(t, openRouterBase, providerAuthBearer)
	cl := openRouterCall(t, own)
	ans, err := s.server.send(context.Background(), cl, e)
	if err != nil {
		t.Fatalf("the OpenRouter call was not sent: %v", err)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("OpenRouter saw %d requests, want 1", n)
	}
	if got, want := s.up.Seen()[0].Header.Get("Authorization"), "Bearer "+key; got != want {
		t.Fatal("OpenRouter was not handed the project's key: this test would assert nothing")
	}

	id, cred := openRouterServed(s)
	rec := httptest.NewRecorder()
	s.server.answer(context.Background(), rec, id, cl, ans, cred, nil, &usageLimitScanner{})
	if rec.Code != http.StatusOK {
		t.Fatalf("the answer is %d, want OpenRouter's own 200 relayed: %s", rec.Code, rec.Body.String())
	}
	for k, vs := range rec.Header() {
		for _, v := range vs {
			if strings.Contains(v, key) {
				t.Errorf("the answer's %s header carries OpenRouter's key", k)
			}
		}
	}
	body := rec.Body.String()
	if strings.Contains(body, key) {
		t.Errorf("the answer echoed OpenRouter's key to the run:\n%s", body)
	}
	if !strings.Contains(body, redactedValue) {
		t.Errorf("the answer carries neither the key nor where it stood:\n%s", body)
	}

	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	if got := auditField(line, "credential"); got != config.ProviderKeySecret {
		t.Errorf("the audit line carries credential %v, want the name %q: the name travels and the value does not", got, config.ProviderKeySecret)
	}
	if got := auditField(line, "tier"); got != TierC {
		t.Errorf("the audit line carries tier %v, want %q", got, TierC)
	}
	if raw := auditLog.String(); strings.Contains(raw, key) {
		t.Errorf("the audit line carries OpenRouter's key:\n%s", raw)
	}
}
