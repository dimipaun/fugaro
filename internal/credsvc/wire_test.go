package credsvc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
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
	_, _, body := ExhaustedUntil{ResetAt: reset, Tried: []Tried{{Name: "acct-one", Tier: TierA}}}.Wire(reset)

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
	tier, name := CredentialHeaders(h)
	if tier != TierB || name != "anthropic-key" {
		t.Fatalf("CredentialHeaders = (%q, %q), want (%q, %q)", tier, name, TierB, "anthropic-key")
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
	// The type field is spelled as the wire's key is, so the value cannot also
	// have an Error method; the type must therefore be readable as a field.
	if w.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal's type field = %q, want %q", w.Error, ErrorUnverifiedRunIdentity)
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
