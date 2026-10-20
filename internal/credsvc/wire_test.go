package credsvc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// The wire contract is what C1.11 writes and C1.15/C1.17 read. These tests
// pin the three things that must not drift apart: the typed "exhausted until
// T" answer (status, error type, reset field), the headers that carry the
// tier and the credential's name, and the switch record.

func TestExhaustedUntilWireRoundTrip(t *testing.T) {
	reset := time.Date(2026, 10, 20, 12, 30, 45, 0, time.UTC)
	now := reset.Add(-90 * time.Second)
	until := ExhaustedUntil{ResetAt: reset, Tried: []Tried{
		{Name: "acct-one", Tier: TierA, Reason: ReasonExhausted},
		{Name: "acct-two", Tier: TierA, Reason: ReasonBroken},
		{Name: "anthropic-key", Tier: TierB, Reason: ReasonNotAdmitted},
	}}

	buf := &headerWriter{h: http.Header{}}
	WriteExhaustedUntil(buf, until, now)
	status, hdr, body := buf.status, buf.h, buf.body
	if status != StatusExhaustedUntil {
		t.Fatalf("status = %d, want %d", status, StatusExhaustedUntil)
	}
	if got := hdr.Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, want %q", got, "90")
	}
	// The body names credentials, never carries a value: the tried list is
	// names, tiers and reasons only.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	if raw["error"] != ErrorExhaustedUntil {
		t.Errorf("error = %v, want %v", raw["error"], ErrorExhaustedUntil)
	}
	if got := raw["reset_at"]; got != float64(reset.UnixMilli()) {
		t.Errorf("reset_at = %v, want %v", got, reset.UnixMilli())
	}

	got, ok, err := ReadExhaustedUntil(status, body)
	if err != nil || !ok {
		t.Fatalf("ReadExhaustedUntil = (%v, %v, %v), want the typed answer", got, ok, err)
	}
	if !got.ResetAt.Equal(reset) {
		t.Errorf("reset = %v, want %v", got.ResetAt, reset)
	}
	if len(got.Tried) != len(until.Tried) {
		t.Fatalf("tried = %v, want %v", got.Tried, until.Tried)
	}
	for i, want := range until.Tried {
		if got.Tried[i] != want {
			t.Errorf("tried[%d] = %v, want %v", i, got.Tried[i], want)
		}
	}
}

// A reset in the past is not a wait: the answer still round-trips (the runner
// decides), but it carries no Retry-After, which would be a lie to an
// intermediary.
func TestExhaustedUntilWirePastResetHasNoRetryAfter(t *testing.T) {
	reset := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	buf := &headerWriter{h: http.Header{}}
	WriteExhaustedUntil(buf, ExhaustedUntil{ResetAt: reset}, reset.Add(time.Minute))
	if buf.status != StatusExhaustedUntil {
		t.Fatalf("status = %d, want %d", buf.status, StatusExhaustedUntil)
	}
	if got := buf.h.Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want none", got)
	}
	if _, ok, err := ReadExhaustedUntil(buf.status, buf.body); err != nil || !ok {
		t.Fatalf("ReadExhaustedUntil = (%v, %v), want the typed answer", ok, err)
	}
}

// The runner must not read an answer as "exhausted until T" when it is not
// one: another status, another error type, or a typed answer with no reset.
func TestReadExhaustedUntilRefusesWhatIsNotTheTypedAnswer(t *testing.T) {
	reset := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	_, _, body, err := ExhaustedUntil{ResetAt: reset, Tried: []Tried{{Name: "acct-one", Tier: TierA, Reason: ReasonExhausted}}}.Wire(reset)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	// wantErr separates the two ways of not being the typed answer: a well
	// formed body of another kind is a plain "not this" (the runner carries
	// on), while a body that claims to be the typed answer and is not
	// readable — or carries no reset instant, which is the one thing the
	// runner waits for — is an error it must not swallow.
	cases := map[string]struct {
		status  int
		body    string
		wantErr bool
	}{
		"another status":     {http.StatusOK, string(body), false},
		"another error type": {StatusExhaustedUntil, `{"error":"unverified_run_identity"}`, false},
		"no reset field":     {StatusExhaustedUntil, `{"error":"` + ErrorExhaustedUntil + `"}`, true},
		"not JSON":           {StatusExhaustedUntil, `no credential`, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok, err := ReadExhaustedUntil(c.status, []byte(c.body))
			if ok {
				t.Fatalf("ReadExhaustedUntil accepted %q as the typed answer", c.body)
			}
			if c.wantErr != (err != nil) {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestCredentialHeadersRoundTrip(t *testing.T) {
	h := http.Header{}
	if err := SetCredentialHeaders(h, TierB, "anthropic-key"); err != nil {
		t.Fatalf("SetCredentialHeaders: %v", err)
	}
	tier, name, ok, err := CredentialHeaders(h)
	if err != nil || !ok {
		t.Fatalf("CredentialHeaders = (%q, %q, %v, %v), want the pair", tier, name, ok, err)
	}
	if tier != TierB || name != "anthropic-key" {
		t.Fatalf("CredentialHeaders = (%q, %q), want (%q, %q)", tier, name, TierB, "anthropic-key")
	}
	// An answer that carries neither is not a pair: most answers are not a
	// credential's.
	if tier, name, ok, err := CredentialHeaders(http.Header{}); ok || err != nil || tier != "" || name != "" {
		t.Fatalf("an answer with neither header = (%q, %q, %v, %v), want no pair and no error", tier, name, ok, err)
	}
}

// The pair is validated when it is READ, not only when it is written (review
// finding M2): C1.16 settles a call against the caps from the tier (A is
// notional, B and C are real money) and C1.19 records the name, so a reader
// that took a raw header value would be reading an answer the service did not
// give. A tier of another release, a name with a space in it, a name past the
// bound, and either half without the other are all refused.
func TestCredentialHeadersRefusesWhatSetCredentialHeadersWouldNotWrite(t *testing.T) {
	cases := map[string]http.Header{
		"an unknown tier":     {HeaderTier: {"Z"}, HeaderCredential: {"acct-one"}},
		"a lower-case tier":   {HeaderTier: {"b"}, HeaderCredential: {"acct-one"}},
		"no tier":             {HeaderCredential: {"acct-one"}},
		"no name":             {HeaderTier: {TierA}},
		"a name with a space": {HeaderTier: {TierA}, HeaderCredential: {"acct one"}},
		"a name with a newline": {HeaderTier: {TierA},
			HeaderCredential: {"acct-one\nX-Evil: 1"}},
		"a name past the bound": {HeaderTier: {TierA},
			HeaderCredential: {strings.Repeat("a", maxCredentialNameBytes+1)}},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			tier, cred, ok, err := CredentialHeaders(h)
			if ok || err == nil {
				t.Fatalf("CredentialHeaders(%v) = (%q, %q, %v, %v), want a refusal", h, tier, cred, ok, err)
			}
			// The error names the field, never the value: both are header text
			// from another process.
			for _, v := range []string{"acct one", "X-Evil", "Z"} {
				if strings.Contains(err.Error(), v) {
					t.Errorf("err = %q repeats the header's value %q", err, v)
				}
			}
		})
	}
}

// Every tier of the wire is a tier, and nothing else is (ValidTier is what
// both sides of the pair above are held to).
func TestValidTier(t *testing.T) {
	for _, tier := range []string{TierA, TierB, TierC} {
		if !ValidTier(tier) {
			t.Errorf("ValidTier(%q) = false", tier)
		}
	}
	for _, tier := range []string{"", "a", "b", "D", "tier-a", " A"} {
		if ValidTier(tier) {
			t.Errorf("ValidTier(%q) = true", tier)
		}
	}
}

// A tried entry is what the run's halt message is built from, so it is held to
// the contract's rules on both sides (review finding M3): a name that is not
// one, a tier outside the set, and a reason outside the closed set are refused
// when the answer is built and when it is read.
func TestTriedEntriesAreValidatedOnBothSides(t *testing.T) {
	reset := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	bad := map[string]Tried{
		"a name that is not one":   {Name: "acct one", Tier: TierA, Reason: ReasonExhausted},
		"a name with a newline":    {Name: "acct\none", Tier: TierA, Reason: ReasonExhausted},
		"no name":                  {Tier: TierA, Reason: ReasonExhausted},
		"an unknown tier":          {Name: "acct-one", Tier: "Z", Reason: ReasonExhausted},
		"no tier":                  {Name: "acct-one", Reason: ReasonExhausted},
		"a reason outside the set": {Name: "acct-one", Tier: TierA, Reason: "because"},
		"a reason with a newline": {Name: "acct-one", Tier: TierA,
			Reason: "exhausted\nINJECTED line"},
		"no reason": {Name: "acct-one", Tier: TierA},
	}
	for name, tr := range bad {
		t.Run(name, func(t *testing.T) {
			// The build side: nothing is rendered, so nothing can carry it.
			if _, _, body, err := (ExhaustedUntil{ResetAt: reset, Tried: []Tried{tr}}).Wire(reset); err == nil {
				t.Fatalf("Wire rendered a tried entry it must refuse: %s", body)
			}
			// The read side: the same entry, in a body that is otherwise the
			// typed answer, is refused rather than handed to the runner.
			raw, err := json.Marshal(WireError{Error: ErrorExhaustedUntil, ResetAt: reset.UnixMilli(),
				Tried: []Tried{tr}})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok, err := ReadExhaustedUntil(StatusExhaustedUntil, raw); ok || err == nil {
				t.Fatalf("ReadExhaustedUntil accepted %+v", tr)
			}
		})
	}

	// The good entry, and a reason from the closed set, still round-trips: the
	// rules refuse what the contract does not carry, not the contract.
	good := Tried{Name: "acct-one", Tier: TierC, Reason: ReasonStopped}
	_, _, body, err := ExhaustedUntil{ResetAt: reset, Tried: []Tried{good}}.Wire(reset)
	if err != nil {
		t.Fatalf("Wire refused a good tried entry: %v", err)
	}
	got, ok, err := ReadExhaustedUntil(StatusExhaustedUntil, body)
	if err != nil || !ok || len(got.Tried) != 1 || got.Tried[0] != good {
		t.Fatalf("round trip = (%v, %v, %v), want %v", got, ok, err, good)
	}
}

// An answer the contract cannot read must not be built (review finding M3):
// Wire refuses a zero ResetAt, which is exactly the body ReadExhaustedUntil
// refuses, and WriteExhaustedUntil answers a typed state_unavailable refusal
// rather than a wait for an instant nobody can read.
func TestWireRefusesAnAnswerItCouldNotReadBack(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if _, _, body, err := (ExhaustedUntil{}).Wire(now); err == nil {
		t.Fatalf("Wire rendered an answer with no reset instant: %s", body)
	}

	buf := &headerWriter{h: http.Header{}}
	WriteExhaustedUntil(buf, ExhaustedUntil{}, now)
	if buf.status != StatusUnavailable {
		t.Errorf("status = %d, want %d", buf.status, StatusUnavailable)
	}
	got, err := ReadWireError(buf.status, buf.body)
	if err != nil {
		t.Fatalf("the fallback refusal is not readable: %v", err)
	}
	if got.Error != ErrorStateUnavailable {
		t.Errorf("error = %q, want %q", got.Error, ErrorStateUnavailable)
	}
	// It is a refusal, not a wait: the runner must not read it as one.
	if _, ok, err := ReadExhaustedUntil(buf.status, buf.body); ok {
		t.Errorf("the fallback refusal was read as a wait (%v)", err)
	}
}

// An answer that cannot carry the tier and the name must not be sent as if it
// did: a wrong tier is a ledger error (notional vs real), and a name that is
// not one clean header value is a header-injection risk.
func TestSetCredentialHeadersRefusesWhatIsNotATierOrAName(t *testing.T) {
	tiers := map[string]string{"no tier": "", "unknown tier": "D", "a tier of another release": "tier-a"}
	for name, tier := range tiers {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if err := SetCredentialHeaders(h, tier, "acct-one"); err == nil {
				t.Fatalf("SetCredentialHeaders(%q, ...) was accepted", tier)
			}
			if len(h) != 0 {
				t.Errorf("a refused pair wrote %v", h)
			}
		})
	}

	// The name rule is the same one the bundle loader and `fugaro creds set`
	// refuse a credential by (C1.11, C1.25).
	names := map[string]string{
		"empty name":                   "",
		"a control in name":            "acct\r\nX-Evil: 1",
		"a newline in name":            "acct\none",
		"a space in name":              "acct one",
		"a name past the rules' bound": strings.Repeat("a", maxCredentialNameBytes+1),
	}
	for name, cred := range names {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if err := SetCredentialHeaders(h, TierA, cred); err == nil {
				t.Fatalf("SetCredentialHeaders(TierA, %q) was accepted", cred)
			}
			if len(h) != 0 {
				t.Errorf("a refused pair wrote %v", h)
			}
			if ValidCredentialName(cred) {
				t.Errorf("ValidCredentialName(%q) = true, want false", cred)
			}
		})
	}
	if !ValidCredentialName("acct-one") || !ValidCredentialName("claude-oauth-token-2") {
		t.Error("ValidCredentialName refused a name that is one clean header value")
	}
}

// There are two rules here and they are not the same rule: validHeaderToken is
// the header-value rule (what may be sent as a header and written into a log
// line), token.ValidSegment is the path-segment rule (what may become a
// budget.PathOutcome). This pins the boundary between them, so that a comment
// claiming one is the other cannot pass unnoticed — the previous version of
// validHeaderToken's comment claimed exactly that, and the code never held to
// it.
func TestTheHeaderRuleAndThePathSegmentRuleAreTwoRules(t *testing.T) {
	// Values the header rule allows and the segment rule must not: these are
	// what a header value may be and a path segment may never be.
	for _, s := range []string{"a/b", "a~b", "a\\b", ".", ".."} {
		if !validHeaderToken(s) {
			t.Errorf("validHeaderToken(%q) = false, want true: it is the header rule, not the segment rule", s)
		}
		if token.ValidSegment(s) {
			t.Errorf("token.ValidSegment(%q) = true, want false: this is what a path segment must not be", s)
		}
	}
	// The header rule refuses what cannot be one header value.
	for _, s := range []string{"", "acct one", "acct\none", strings.Repeat("A", maxHeaderTokenBytes+1)} {
		if validHeaderToken(s) {
			t.Errorf("validHeaderToken(%q) = true, want false", s)
		}
	}
	// A credential's name is the header rule plus the name's bound, so it
	// inherits the allowance above: a name is a header value, a log line and a
	// value in the run's record, never a path segment. Nothing that becomes a
	// path is held to this rule — the uid's halves go through ValidSegment in
	// splitRunUID, and RTDBOutcomes checks them again before it builds a path.
	if !ValidCredentialName("acct/one") {
		t.Error(`ValidCredentialName("acct/one") = false, want true: a name is a header value, not a segment`)
	}
}

func TestSwitchHeaderRoundTrip(t *testing.T) {
	h := http.Header{}
	want := Switch{From: "acct-one", To: "anthropic-key", Reason: ReasonExhausted}
	if err := SetSwitchHeader(h, want); err != nil {
		t.Fatalf("SetSwitchHeader: %v", err)
	}
	got, ok, err := SwitchHeader(h)
	if err != nil || !ok {
		t.Fatalf("SwitchHeader = (%v, %v, %v), want the record", got, ok, err)
	}
	if got != want {
		t.Fatalf("SwitchHeader = %v, want %v", got, want)
	}
	if _, ok, err := SwitchHeader(http.Header{}); ok || err != nil {
		t.Fatalf("an answer with no switch reports (%v, %v), want (false, nil)", ok, err)
	}

	// A switch with no reason, or with a name that is not one, is not written:
	// the run's log records why a call moved, and a record without the why is
	// not one.
	refused := map[string]Switch{
		"no reason":    {To: "anthropic-key"},
		"no target":    {From: "acct-one", Reason: ReasonExhausted},
		"a bad target": {To: "anthropic key", Reason: ReasonExhausted},
		"a bad source": {From: "acct one", To: "anthropic-key", Reason: ReasonExhausted},
		"a source with a newline": {From: "acct-one\nX-Evil: 1", To: "anthropic-key",
			Reason: ReasonExhausted},
	}
	for name, s := range refused {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if err := SetSwitchHeader(h, s); err == nil {
				t.Fatalf("SetSwitchHeader(%+v) was accepted", s)
			}
			if len(h) != 0 {
				t.Errorf("a refused switch wrote %v", h)
			}
		})
	}

	// A switch this code *reads* is held to the rules one it writes is held
	// to: the header reaches the run's record, so a record with a name that
	// cannot be one, or without the why, is refused rather than logged.
	malformed := map[string]string{
		"a from that is not a name": `{"from":"acct one","to":"anthropic-key","reason":"exhausted"}`,
		"a from with a newline":     `{"from":"acct-one\nX-Evil: 1","to":"anthropic-key","reason":"exhausted"}`,
		"a to that is not a name":   `{"from":"acct-one","to":"anthropic key","reason":"exhausted"}`,
		"no reason":                 `{"from":"acct-one","to":"anthropic-key"}`,
		"an oversized from": fmt.Sprintf(`{"from":%q,"to":"anthropic-key","reason":"exhausted"}`,
			strings.Repeat("A", maxCredentialNameBytes+1)),
	}
	for name, raw := range malformed {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			h.Set(HeaderSwitch, raw)
			got, ok, err := SwitchHeader(h)
			if ok || err == nil {
				t.Fatalf("SwitchHeader(%s) = (%v, %v, %v), want a refusal", raw, got, ok, err)
			}
		})
	}
}

// The refusal the service answers an unverified caller is a typed body, and
// it never carries what the caller presented.
func TestWireErrorCarriesTheTypeAndNoIdentity(t *testing.T) {
	h := http.Header{}
	w := WireError{Error: ErrorUnverifiedRunIdentity, Message: "the run identity is not verified: the run has ended"}
	buf := &headerWriter{h: h}
	WriteWireError(buf, StatusUnverified, w)
	if buf.status != StatusUnverified {
		t.Fatalf("status = %d, want %d", buf.status, StatusUnverified)
	}
	got, err := ReadWireError(buf.status, buf.body)
	if err != nil {
		t.Fatalf("ReadWireError: %v", err)
	}
	if got.Error != ErrorUnverifiedRunIdentity || !strings.Contains(got.Message, "the run has ended") {
		t.Fatalf("WireError = %+v, want the type and the message", got)
	}
	if _, err := ReadWireError(StatusUnverified, []byte("not json")); err == nil {
		t.Error("ReadWireError accepted a body that is not JSON")
	}
	if _, err := ReadWireError(http.StatusOK, buf.body); err == nil {
		t.Error("ReadWireError accepted a 2xx as a refusal")
	}
	// A body that names no error type is not a refusal this code can act on:
	// the runner switches on the type, so a body without one is an error
	// rather than an empty refusal.
	for _, body := range []string{`{}`, `{"message":"something went wrong"}`, `{"error":""}`} {
		if _, err := ReadWireError(StatusUnverified, []byte(body)); err == nil {
			t.Errorf("ReadWireError accepted %s, which names no error type", body)
		}
	}
	// The body decodes strictly (Convention 6): a field this release does not
	// know is refused rather than ignored, and so is anything after the value.
	// A decoder's own message names the byte it choked on, so the error this
	// code returns must not carry it — the body is another writer's text.
	for _, body := range []string{
		`{"error":"state_unavailable","unknown_field":1}`,
		`{"error":"state_unavailable"} {"error":"state_unavailable"}`,
		`{"error":"state_unavailable"} trailing`,
		"{\"error\":\"state_unavailable\",\"tried\":[{\"name\":\"a\",\"tier\":\"A\",\"reason\":\"exhausted\",\"extra\":1}]}",
		"{\"error\":\"unverified_run_identity\",\"message\":\"a\x01b\"}",
	} {
		_, err := ReadWireError(StatusUnverified, []byte(body))
		if err == nil {
			t.Errorf("ReadWireError accepted %s", body)
			continue
		}
		for _, leak := range []string{"unknown_field", "trailing", "\x01", "extra"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("err = %q carries the body's own text %q", err, leak)
			}
		}
	}
	// The type field is spelled as the wire's key is, so the value cannot also
	// have an Error method; the type must therefore be readable as a field.
	if w.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal's type field = %q, want %q", w.Error, ErrorUnverifiedRunIdentity)
	}
	// The message is the one free-text field of the body, and it reaches a log
	// line and a halt message, so it is bounded (review finding M3): a body
	// carrying more of it than any refusal this code writes is not one this
	// contract wrote. Refused rather than clipped, so no half of another
	// writer's sentence is displayed as the service's words — and the error
	// says the message is too long without repeating any of it.
	long := strings.Repeat("m", maxRefusalMessageBytes+1)
	_, err = ReadWireError(StatusUnverified, []byte(`{"error":"state_unavailable","message":"`+long+`"}`))
	if err == nil {
		t.Fatal("ReadWireError accepted a body whose message is longer than any refusal this code writes")
	}
	if strings.Contains(err.Error(), "mmm") {
		t.Errorf("err = %q carries the message it refused", err)
	}
	// One byte inside the bound is read: the bound is a bound and not a rule
	// against long sentences.
	ok := strings.Repeat("m", maxRefusalMessageBytes)
	if got, err := ReadWireError(StatusUnverified, []byte(`{"error":"state_unavailable","message":"`+ok+`"}`)); err != nil || len(got.Message) != maxRefusalMessageBytes {
		t.Fatalf("ReadWireError refused a message at the bound: %v", err)
	}
}

// headerWriter is an http.ResponseWriter that keeps what was written, so a
// test can read back the bytes the service would send.
type headerWriter struct {
	h      http.Header
	status int
	body   []byte
}

func (w *headerWriter) Header() http.Header { return w.h }
func (w *headerWriter) WriteHeader(status int) {
	w.status = status
}
func (w *headerWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}

// A Cloud Run job name is a narrower thing than a header value (review finding
// m3): the value is recorded in the run's record and in the audit line, and
// every job name this installation can have is [a-z0-9-] and at most 49
// characters. The header-value rule is wider on purpose and stays as it is.
func TestValidJobNameIsTheJobNameRuleAndNotTheHeaderRule(t *testing.T) {
	for _, ok := range []string{
		"fugaro-aurora-app-review",
		"fugaro-p-profile-one-anthropic-acct-one-ab12cd34",
		"a",
		"a1",
		"job-1",
		// The longest name internal/backend/gcp's derive produces: its readable
		// part truncated to the limit less the hash's room, then "-" and twelve
		// hex digits — 36 + 13 = 49, Cloud Run's own bound. Every job of this
		// installation is that shape, so the rule must accept its edge.
		strings.Repeat("a", maxJobNameBytes-13) + "-0123456789ab",
	} {
		if !ValidJobName(ok) {
			t.Errorf("ValidJobName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"",                                     // empty
		"Fugaro-Job",                           // upper case: not a Cloud Run name
		"1job",                                 // starts with a digit
		"-job",                                 // starts with a hyphen
		"job-",                                 // ends with a hyphen
		"job name",                             // a space
		"job\ttab",                             // a control
		"job\"quote",                           // what the header rule allows and a name cannot be
		"job\\backslash",                       // likewise
		"job$dollar",                           // likewise
		"job;semis",                            // likewise
		"job`tick`",                            // likewise
		"job/nested",                           // likewise
		strings.Repeat("a", maxJobNameBytes+1), // past Cloud Run's own bound
	} {
		if ValidJobName(bad) {
			t.Errorf("ValidJobName(%q) = true, want false", bad)
		}
	}
	// The two rules are not the same rule: the header rule accepts values a job
	// name can never be.
	for _, s := range []string{`job"quote`, "job\\backslash", "job$dollar", "job;semis", "job`tick`"} {
		if !validHeaderToken(s) {
			t.Errorf("validHeaderToken(%q) = false, want true: it is the header rule", s)
		}
		if ValidJobName(s) {
			t.Errorf("ValidJobName(%q) = true: the job-name rule is narrower", s)
		}
	}
}

// request builds the HTTP request a call presents, so a test can leave one
// header out, send one twice, or spell the scheme another way.
func request(auth, run, job string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://creds.example.invalid/", nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if run != "" {
		r.Header.Set(HeaderRunIdentity, run)
	}
	if job != "" {
		r.Header.Set(HeaderRunJob, job)
	}
	return r
}

// The contract owns the request's headers (review finding m7): one value per
// header, the bearer scheme spelled as RFC 6750 spells it, and a job name that
// could be one. What it does not do is trust the tokens — that is Verify's.
func TestRequestFromHTTPTakesOneValuePerHeader(t *testing.T) {
	const tok = "header.payload.signature"
	got, err := RequestFromHTTP(request("Bearer "+tok, tok, "fugaro-aurora-app-review"))
	if err != nil {
		t.Fatalf("RequestFromHTTP: %v", err)
	}
	want := Request{RunIDToken: tok, JobIDToken: tok, Job: "fugaro-aurora-app-review"}
	if got != want {
		t.Fatalf("RequestFromHTTP = %+v, want %+v", got, want)
	}

	// A header the call did not send, a scheme spelled another way, a token
	// that is not one, and a job name that is not one: each refused, and the
	// error names the header rather than the value.
	refused := map[string]*http.Request{
		"no Authorization":      request("", tok, "fugaro-aurora-app-review"),
		"no run identity":       request("Bearer "+tok, "", "fugaro-aurora-app-review"),
		"no job":                request("Bearer "+tok, tok, ""),
		"no bearer scheme":      request(tok, tok, "fugaro-aurora-app-review"),
		"a lower-case scheme":   request("bearer "+tok, tok, "fugaro-aurora-app-review"),
		"an upper-case scheme":  request("BEARER "+tok, tok, "fugaro-aurora-app-review"),
		"another scheme":        request("Basic "+tok, tok, "fugaro-aurora-app-review"),
		"an empty bearer token": request("Bearer ", tok, "fugaro-aurora-app-review"),
		"a token with a space":  request("Bearer "+tok+" x", tok, "fugaro-aurora-app-review"),
		"an oversized token": request("Bearer "+strings.Repeat("a", maxTokenBytes+1), tok,
			"fugaro-aurora-app-review"),
		"an oversized run identity": request("Bearer "+tok, strings.Repeat("a", maxTokenBytes+1),
			"fugaro-aurora-app-review"),
		"a job name that is not one": request("Bearer "+tok, tok, "fugaro-aurora-app-review\nX-Evil: 1"),
	}
	for name, r := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := RequestFromHTTP(r); err == nil {
				t.Fatal("RequestFromHTTP accepted a request it must refuse")
			}
		})
	}

	// A header sent twice is a caller sending two identities: there is nothing
	// to pick between, so it is refused rather than resolved by order.
	for _, hdr := range []string{"Authorization", HeaderRunIdentity, HeaderRunJob} {
		t.Run("two "+hdr, func(t *testing.T) {
			r := request("Bearer "+tok, tok, "fugaro-aurora-app-review")
			r.Header.Add(hdr, "a-second-value")
			if _, err := RequestFromHTTP(r); err == nil {
				t.Fatalf("RequestFromHTTP accepted two %s headers", hdr)
			}
		})
	}

	// The error never carries a value the caller presented.
	r := request("Bearer "+tok, tok, "job with a space")
	_, err = RequestFromHTTP(r)
	if err == nil {
		t.Fatal("RequestFromHTTP accepted a job name with a space")
	}
	for _, leak := range []string{tok, "job with a space"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("err = %q carries the caller's %q", err, leak)
		}
	}
}
