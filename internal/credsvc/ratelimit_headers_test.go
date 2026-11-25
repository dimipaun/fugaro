package credsvc

// What the service records about the rate-limit headers of the answers it
// serves (PL7): the NAMES, per source, and never a value.
//
// It exists for U1 (D3178): whether a subscription token's answer reports a
// utilisation figure at all is not something this repository can settle, and the
// threshold rule (PL8) is not launched until the owner's Check 880 says it can.
// The service's half of that check is this record, and the properties that make
// it usable and safe are all here: it is the LAST answer's names, it belongs to
// the source whose answer carried it, it is written in a node of its own so the
// mark's wire shape does not change, and it holds no value of any header.
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
	o, ok := s.state.observation("default")
	if !ok {
		t.Fatal("the credential that answered has no observation: the answer's header names were not recorded at all")
	}
	want := []string{"Anthropic-Ratelimit-Unified-5h-Utilization", "Anthropic-Ratelimit-Unified-7d-Reset", "Retry-After"}
	if !slices.Equal(o.Headers, want) {
		t.Errorf("the record is %v, want %v: the NAMES of the rate-limit headers the answer carried, sorted, and no other header", o.Headers, want)
	}
	// The record is a node of its own: a serve writes no mark at all, so the
	// four members every release reads keep their exact shape and an answer
	// never turns into a hold (creds.Observation).
	if m, held := s.state.mark("default"); held {
		t.Errorf("the served credential has the mark %+v: recording what an answer carried is an observation and not a hold, and it is written in a node of its own", m)
	}
	assertNoValueRecorded(t, o, "the 200's answer")

	// The limit's own answer, on the credential that answered it: the record is
	// the LAST answer's names, the exhaustion beside it touches none of them,
	// and the call moves on.
	resp, body = s.call(t, callBody("limit"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the second call answered %d %s, want the next credential's 200", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testAccount {
		t.Fatalf("%s = %q, want %q: the limit did not move the call", HeaderCredential, got, testAccount)
	}
	m, ok := s.state.mark("default")
	if !ok || m.State != creds.StateExhausted {
		t.Fatalf("the default account's mark is %q/%v, want %q after the 429", m.State, ok, creds.StateExhausted)
	}
	o, ok = s.state.observation("default")
	if !ok {
		t.Fatal("the limited credential's observation went away")
	}
	want = []string{"Anthropic-Ratelimit-Unified-5h-Reset", "Anthropic-Ratelimit-Unified-5h-Utilization"}
	if !slices.Equal(o.Headers, want) {
		t.Errorf("the record is %v, want %v: the LAST answer's names, and the hold written beside them moves none of them", o.Headers, want)
	}
	assertNoValueRecorded(t, o, "the limit's answer")

	// The reset the limit named is a hold and a selection reads it as Until:
	// the observation is where the names are, and the mark is where the hold
	// is, which is the split that keeps the record from being read as state.
	if !strings.HasPrefix(m.Until.Format("2006-01-02T15:04:05Z"), "2026-11-25T04:00:00") {
		t.Errorf("the mark's hold is %v, want the reset the limit named", m.Until)
	}

	// Per source: the credential that answered the retry holds the record of ITS
	// answer, which carried no rate-limit header, and none of the first
	// credential's names is read as its own. The node is written (the answer was
	// recorded) and it names nothing, which Check 880 reads as UNVERIFIED
	// rather than as a positive.
	other, ok := s.state.observation(testAccount)
	if !ok {
		t.Fatal("the credential that answered the retry has no observation: an answer that carried no rate-limit header is still an answer that was recorded")
	}
	if len(other.Headers) != 0 {
		t.Errorf("the credential that answered the retry recorded %v: a record belongs to the source whose answer carried it", other.Headers)
	}
}

// TestAnAnswerAboutTheRequestIsNotRecorded: the record is a claim that an answer
// says something about the CREDENTIAL, and the answers this service relays
// without judging the credential say nothing about it — a 529 is the overloaded
// upstream's own answer and the service retries none of them (the chain's rule,
// "an answer about the request moves nothing"). The answer below carries the
// names a utilisation figure comes in, so a guard that recorded every status
// would be caught here rather than passing for having recorded nothing.
func TestAnAnswerAboutTheRequestIsNotRecorded(t *testing.T) {
	s := newSvc(t)
	about := http.Header{}
	about.Set("Anthropic-Ratelimit-Unified-5h-Utilization", utilisationAnswer)
	about.Set("Retry-After", "30")
	s.up.Script = []anthropicfake.Reply{
		{Status: 529, Header: about, Body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
	}

	resp, body := s.call(t, callBody("relayed-529"))
	if resp.StatusCode != 529 {
		t.Fatalf("the call answered %d %s, want the upstream's own 529", resp.StatusCode, body)
	}
	if o, ok := s.state.observation("default"); ok {
		t.Errorf("the credential that answered a 529 recorded %v: a record is a claim about the credential, and this answer is about the request", o.Headers)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want the one it answered: an overloaded answer is not retried", n)
	}
}

// TestAServeIsUnaffectedByAStoreThatCannotRecord: recording is a capability a
// store has or has not (creds.ObservationStore), and a store without it is not
// a failure of the call — the answer the run was given is the run's, and the
// record is the operator's check. It is also the only test that takes
// stateReads.UpdateObservation's fail-open branch: the wrapper always has the
// method, and this is a state whose own store does not, which is exactly what
// the branch is for.
func TestAServeIsUnaffectedByAStoreThatCannotRecord(t *testing.T) {
	s := newSvcWith(t, func(s *svc, cfg *Config) { cfg.State = noObservationStore{Store: s.state} })
	served := http.Header{}
	served.Set("Anthropic-Ratelimit-Unified-5h-Utilization", utilisationAnswer)
	s.up.Script = []anthropicfake.Reply{
		{Status: http.StatusOK, Header: served, Body: anthropicfake.MessageOK(testModel, testUsage).Body},
	}

	resp, body := s.call(t, callBody("no-record-store"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s: a store that cannot record what an answer carried is not a failure of the call", resp.StatusCode, body)
	}
	if o, ok := s.state.observation("default"); ok {
		t.Errorf("the store that cannot record one holds %v: a store without the capability writes nothing, and Check 880 reads that source as one it cannot answer for", o.Headers)
	}
}

// noObservationStore is a state store that cannot record an observation: it
// carries creds.Store's own methods and not UpdateObservation. It is written as
// an embedded interface rather than as a struct with seven methods so that a
// method added to creds.Store needs no edit here.
type noObservationStore struct{ creds.Store }

// assertNoValueRecorded reads the record back as the operator's script reads it
// — the node's JSON — and fails on any value the answers carried. The names are
// the check's question; a value would be the leak the record exists to avoid.
//
// The reset time is a hold and not a name, and it is a hold the MARK carries:
// the observation holds no time at all, so the whole of it must be free of every
// value an answer carried, the reset included.
func assertNoValueRecorded(t *testing.T, o creds.Observation, what string) {
	t.Helper()
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("the record could not be read back: %v", err)
	}
	record := strings.Join(o.Headers, "\n")
	for _, value := range []string{utilisationAnswer, resetAnswer, notRateLimitValue} {
		if strings.Contains(record, value) {
			t.Errorf("%s: the record is %q, and it carries the value %q: it holds the names of the rate-limit headers and never one of their values", what, record, value)
		}
		if strings.Contains(string(raw), value) {
			t.Errorf("%s: the recorded node carries the value %q: nothing but a name of a rate-limit header may be recorded\n%s", what, value, raw)
		}
	}
}
