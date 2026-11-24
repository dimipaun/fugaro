package credsvc

// What the service records about the rate-limit headers of the answers it
// serves (PL7): the NAMES, per source, and never a value.
//
// It exists for U1 (D3178): whether a subscription token's answer reports a
// utilisation figure at all is not something this repository can settle, and the
// threshold rule (PL8) is not launched until the owner's Check 880 says it can.
// The service's half of that check is this record, and the properties that make
// it usable and safe are all here: it is the LAST answer's names, it survives
// the hold that answer wrote, it belongs to the source whose answer carried it,
// and it holds no value of any header.
//
// Everything is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback and the state is a map. The values
// planted in the answers below are markers this test searches the record for,
// not credentials.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// The values the answers carry, and the marker an answer puts in a header that
// is not a rate-limit one. None of them may reach the record: it holds the
// names of the rate-limit headers and nothing else, which is what makes it safe
// to read from an operator's script and to print.
const (
	utilisationAnswer = "0.87"
	resetAnswer       = "2026-11-25T04:00:00Z"
	notRateLimitValue = "do-not-record-me"
)

func TestHeaderNamesAreRecordedWithoutValues(t *testing.T) {
	s := newSvc(t)
	served := http.Header{}
	served.Set("Anthropic-Ratelimit-Unified-5h-Utilization", utilisationAnswer)
	served.Set("anthropic-ratelimit-unified-7d-reset", resetAnswer)
	served.Set("Retry-After", "30")
	served.Set("X-Marker", notRateLimitValue)
	limit := http.Header{}
	limit.Set("Anthropic-Ratelimit-Unified-5h-Utilization", utilisationAnswer)
	limit.Set("Anthropic-Ratelimit-Unified-5h-Reset", resetAnswer)
	limit.Set("X-Marker", notRateLimitValue)
	s.up.Script = []anthropicfake.Reply{
		{Status: http.StatusOK, Header: served, Body: anthropicfake.MessageOK(testModel, testUsage).Body},
		// The second answer is a usage limit on the same credential, and it
		// carries the headers the check is really run after: Check 880 is run
		// after a run, and the answer it reads is the one that ended it.
		{Status: http.StatusTooManyRequests, Header: limit,
			Body: `{"type":"error","error":{"type":"` + rateLimitEventType + `","message":"usage limit reached"}}`},
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("served"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first call answered %d %s, want the 200", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != "default" {
		t.Fatalf("%s = %q, want the fixture's first credential: the record asserted below is its own", HeaderCredential, got)
	}
	m, ok := s.state.mark("default")
	if !ok {
		t.Fatal("the credential that answered has no mark: the answer's header names were not recorded at all")
	}
	want := []string{"Anthropic-Ratelimit-Unified-5h-Utilization", "Anthropic-Ratelimit-Unified-7d-Reset", "Retry-After"}
	if !slices.Equal(m.Headers, want) {
		t.Errorf("the record is %v, want %v: the NAMES of the rate-limit headers the answer carried, sorted, and no other header", m.Headers, want)
	}
	if !m.Available() {
		t.Errorf("the mark reads %q after a 200: recording what an answer carried is an observation, not a hold", m.State)
	}
	assertNoValueRecorded(t, m, "the 200's answer")

	// The limit's own answer, on the credential that answered it: the record is
	// the LAST answer's names, the exhaustion it wrote does not wipe them
	// (creds.Exhaust rebuilds the mark, and a wipe here would answer "nothing"
	// exactly when there is something to read), and the call moves on.
	resp, body = s.call(t, callBody("limit"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the second call answered %d %s, want the next credential's 200", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testAccount {
		t.Fatalf("%s = %q, want %q: the limit did not move the call", HeaderCredential, got, testAccount)
	}
	m, ok = s.state.mark("default")
	if !ok || m.State != creds.StateExhausted {
		t.Fatalf("the default account's mark is %q/%v, want %q after the 429", m.State, ok, creds.StateExhausted)
	}
	want = []string{"Anthropic-Ratelimit-Unified-5h-Reset", "Anthropic-Ratelimit-Unified-5h-Utilization"}
	if !slices.Equal(m.Headers, want) {
		t.Errorf("the record is %v, want %v: the LAST answer's names, carried through the hold it wrote", m.Headers, want)
	}
	assertNoValueRecorded(t, m, "the limit's answer")

	// Per source: the credential that answered the call that went out holds the
	// record of ITS answer, which carried no rate-limit header, so nothing of
	// the first credential's record is read as its own.
	if other, ok := s.state.mark(testAccount); ok && len(other.Headers) != 0 {
		t.Errorf("the credential that answered the retry recorded %v: a record belongs to the source whose answer carried it", other.Headers)
	}
}

// assertNoValueRecorded reads the record back as the operator's script reads it
// — the node's JSON — and fails on any value the answers carried. The names are
// the check's question; a value would be the leak the record exists to avoid.
//
// The reset time is held to the record's own field and not to the whole mark:
// it is the hold the answer named, which a selection reads as Until, and a
// utilisation figure is state no selection needs — which is why the figure, and
// the marker of a header that is not about a rate limit, must appear nowhere in
// the mark at all.
func assertNoValueRecorded(t *testing.T, m creds.Mark, what string) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("the record could not be read back: %v", err)
	}
	record := strings.Join(m.Headers, "\n")
	for _, value := range []string{utilisationAnswer, resetAnswer, notRateLimitValue} {
		if strings.Contains(record, value) {
			t.Errorf("%s: the record is %q, and it carries the value %q: it holds the names of the rate-limit headers and never one of their values", what, record, value)
		}
	}
	for _, value := range []string{utilisationAnswer, notRateLimitValue} {
		if strings.Contains(string(raw), value) {
			t.Errorf("%s: the mark carries the value %q: nothing but a name of a rate-limit header may be recorded\n%s", what, value, raw)
		}
	}
}
