package credsvc

// What the service counts of the calls it serves (PL10): the calls and the
// tokens, per member, in a node of its own — the figures the operator's listing
// and the console's pools screen read back.
//
// The two things worth pinning are here. A member is counted for the calls it
// SERVED: a usage limit is read as about the credential and retried, so the
// member that was limited served nothing and is counted for nothing, which is
// what keeps the figures a statement about work rather than about attempts. And
// a stream is counted like a whole body: its usage arrives inside the answer,
// and the reader that counts it is fed from the same bytes the run is already
// getting.
//
// Everything is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback and the state is a map.

import (
	"net/http"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

func TestAServedCallIsCountedForItsMember(t *testing.T) {
	s := newSvc(t)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		// The same credential, limited: the service reads the answer as about
		// the credential, holds it and retries on the next one.
		anthropicfake.Error(http.StatusTooManyRequests, rateLimitEventType, "usage limit reached"),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.StreamOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("served"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first call answered %d %s, want the 200", resp.StatusCode, body)
	}
	served := resp.Header.Get(HeaderCredential)
	if served == "" {
		t.Fatal("the answer names no credential: the figures asserted below belong to the one it names")
	}
	u, ok := s.state.usage(served)
	if !ok {
		t.Fatal("the member that served the call has no figures: the call was not counted at all")
	}
	// The whole body's own usage: the fake reports 10 in and 10 out.
	if u.Calls != 1 || u.Tokens != 20 {
		t.Errorf("the member that served one call reads %d calls and %d tokens, want 1 and 20", u.Calls, u.Tokens)
	}

	resp, body = s.call(t, callBody("limited"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the second call answered %d %s, want the next credential's 200", resp.StatusCode, body)
	}
	next := resp.Header.Get(HeaderCredential)
	if next == served {
		t.Fatalf("the retry was served by %q, the credential the limit was on: the two members cannot be told apart below", next)
	}
	if u, ok := s.state.usage(served); !ok || u.Calls != 1 || u.Tokens != 20 {
		t.Errorf("the limited member reads %+v, want the one call it served and nothing for the attempt the limit refused", u)
	}
	u, ok = s.state.usage(next)
	if !ok || u.Calls != 1 || u.Tokens != 20 {
		t.Errorf("the member that served the retry reads %+v (found=%v), want its own one call and 20 tokens", u, ok)
	}

	// A stream is counted like a whole body, from the usage its events carry.
	resp, body = s.call(t, callBody("stream"), func(r *http.Request) {
		r.Header.Set("Accept", "text/event-stream")
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the streamed call answered %d %s, want the 200", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != next {
		t.Fatalf("the streamed call was served by %q, want %q: the second member's figures are the ones asserted here", got, next)
	}
	u, ok = s.state.usage(next)
	if !ok || u.Calls != 2 || u.Tokens != 40 {
		t.Errorf("the member that served a whole body and a stream reads %+v (found=%v), want 2 calls and 40 tokens", u, ok)
	}
}
