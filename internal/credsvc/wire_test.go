package credsvc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
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

	// The body's message is the writer's own rendering of the fields, and it
	// is the sentence the run's halt message carries: the reader rebuilds it
	// from the fields, so a body whose message was dropped or rewritten would
	// still read back and nothing else would notice (review finding). Pin it
	// against the writer's sentence.
	we, err := ReadWireError(status, body)
	if err != nil {
		t.Fatalf("ReadWireError: %v", err)
	}
	want := "no credential is usable before " + reset.UTC().Format(time.RFC3339) +
		"; tried acct-one (exhausted), acct-two (broken), anthropic-key (not_admitted)"
	if we.Message != want {
		t.Errorf("message = %q, want %q", we.Message, want)
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
		// A header sent twice is refused on this side too (review finding):
		// the tier settles a call against the caps (C1.16) and the name is what
		// the run records (C1.19), so neither may be decided by header order.
		// Both values of each pair are individually valid, so only the
		// duplicate rule can refuse them.
		"a tier sent twice": {HeaderTier: {TierA, TierB}, HeaderCredential: {"acct-one"}},
		"a name sent twice": {HeaderTier: {TierA}, HeaderCredential: {"acct-one", "acct-two"}},
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

	// Whatever Wire renders, ReadExhaustedUntil reads. The instant is the one
	// field the two sides disagreed about (review finding): the writer refused
	// only the zero time.Time while the reader refused anything at or before
	// the epoch, so `time.UnixMilli(0)` was rendered, dropped by the field's
	// omitempty, and refused on the way back in.
	for name, reset := range map[string]time.Time{
		"the zero instant":            {},
		"the epoch itself":            time.UnixMilli(0),
		"an instant before the epoch": time.UnixMilli(-1),
		"an ordinary instant":         time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, body, err := (ExhaustedUntil{ResetAt: reset}).Wire(now)
			if err != nil {
				return // refusing to render it is the other way to hold the rule
			}
			if _, ok, err := ReadExhaustedUntil(StatusExhaustedUntil, body); err != nil || !ok {
				t.Fatalf("Wire rendered %s, which ReadExhaustedUntil will not read: %v", body, err)
			}
		})
	}

	// And the same invariant for the sentence (review finding): forty
	// credentials at the rules' 200-byte name bound is over the refusal bound,
	// so a writer that rendered its message whole would build an answer the
	// reader refuses — the runner would get no typed answer at all for the one
	// case this type exists for. The structured list is not clipped: it is
	// what the runner reads the names from.
	var many []Tried
	for i := 0; i < 50; i++ {
		many = append(many, Tried{
			Name:   fmt.Sprintf("%03d-%s", i, strings.Repeat("n", 190)),
			Tier:   TierA,
			Reason: ReasonExhausted,
		})
	}
	_, _, body, err := (ExhaustedUntil{ResetAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Tried: many}).Wire(now)
	if err != nil {
		t.Fatalf("Wire refused fifty tried entries: %v", err)
	}
	if got, ok, err := ReadExhaustedUntil(StatusExhaustedUntil, body); err != nil || !ok {
		t.Fatalf("Wire rendered an answer ReadExhaustedUntil will not read: %v", err)
	} else if len(got.Tried) != len(many) {
		t.Errorf("tried = %d entries, want %d: the list is not the message", len(got.Tried), len(many))
	}
	// Clipped, not dropped: the sentence a human reads is still the writer's,
	// cut to the bound the reader holds it to. An empty one would leave the
	// run's halt message with nothing to say.
	we, err := ReadWireError(StatusExhaustedUntil, body)
	if err != nil {
		t.Fatalf("ReadWireError: %v", err)
	}
	full := ExhaustedUntil{ResetAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Tried: many}.Error()
	if we.Message == "" || !strings.HasPrefix(full, we.Message) {
		t.Errorf("message = %q, want the writer's sentence, clipped", we.Message)
	}
	if len(we.Message) > maxRefusalMessageBytes {
		t.Errorf("message is %d bytes, over the bound the reader holds it to", len(we.Message))
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
		// The header decodes strictly (Convention 6): a field this release does
		// not know is refused, not ignored. Nothing else in this map needs the
		// strict decode — every other case is refused by the rules the fields
		// are held to — so without this one the whole rule could be replaced
		// with a plain json.Unmarshal and the suite would not notice (review
		// finding).
		"a field this release does not know": `{"from":"acct-one","to":"anthropic-key","reason":"exhausted","extra":1}`,
		"an oversized from": fmt.Sprintf(`{"from":%q,"to":"anthropic-key","reason":"exhausted"}`,
			strings.Repeat("A", maxCredentialNameBytes+1)),
		// A closing brace or bracket after the record is a second value like
		// any other: dec.More() reports false for it — it answers "is the next
		// byte '}' or ']'", not "is anything left" — so a trailer of '}' or ']'
		// is the one that slips past a More() check (review finding on the
		// strict-decode rule).
		"a closing brace after the record":   `{"from":"acct-one","to":"anthropic-key","reason":"exhausted"}}`,
		"a closing bracket after the record": `{"from":"acct-one","to":"anthropic-key","reason":"exhausted"}]`,
		// A header present with no value is not "no switch": nothing here
		// writes one, so it is a record this code cannot read (review nit). It
		// must be refused rather than read as an absent header.
		"a present but empty header": "",
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

	// A switch header sent twice is refused for the same reason the request
	// side refuses a header a call sent twice (review finding): the record is
	// what the run logs, and which of the two is logged must not be decided by
	// header order. Both values here are individually valid.
	twice := http.Header{}
	twice.Add(HeaderSwitch, `{"from":"acct-one","to":"anthropic-key","reason":"exhausted"}`)
	twice.Add(HeaderSwitch, `{"from":"acct-two","to":"anthropic-key","reason":"exhausted"}`)
	if got, ok, err := SwitchHeader(twice); ok || err == nil {
		t.Fatalf("SwitchHeader(%v) = (%v, %v, %v), want a refusal", twice, got, ok, err)
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
		// A closing brace or bracket after the value is the trailer that slips
		// past a decoder's More(): More() answers "is the next byte '}' or ']'",
		// not "is anything left", so `...}}` and `...}]` read as one value
		// (review finding on the strict-decode rule).
		`{"error":"state_unavailable"}}`,
		`{"error":"state_unavailable"}]`,
		`{"error":"state_unavailable"} } }`,
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

// The message is the body's one free-text field and it is another writer's
// text: it reaches a log line and a halt message, so it is held to one
// printable line, as every other field of the body is held to a printable
// rule (review finding). An escape sequence in it would repaint a terminal
// and a newline would forge a second line — the injection the length bound
// says nothing about. The repository's rule for text from another writer is
// internal/safetext (internal/budget/registry.go holds a credential name to it
// for the same reason).
func TestReadWireErrorMakesTheMessageOnePrintableLine(t *testing.T) {
	body := []byte(`{"error":"state_unavailable","message":"\u001b[31mInjected\u001b[0m\nX-Log: forged"}`)
	got, err := ReadWireError(StatusUnavailable, body)
	if err != nil {
		t.Fatalf("ReadWireError: %v", err)
	}
	for _, bad := range []string{"\x1b", "\n", "\r", "\x00"} {
		if strings.Contains(got.Message, bad) {
			t.Errorf("message = %q carries %q: it is not one printable line", got.Message, bad)
		}
	}
	// Sanitised, not dropped: the sentence is still the writer's, and a reader
	// of the log can still see what it said.
	if !strings.Contains(got.Message, "Injected") || !strings.Contains(got.Message, "X-Log: forged") {
		t.Errorf("message = %q lost the writer's sentence", got.Message)
	}
	// A message that is already one printable line is handed over unchanged.
	plain, err := ReadWireError(StatusUnavailable, []byte(`{"error":"state_unavailable","message":"try again later"}`))
	if err != nil || plain.Message != "try again later" {
		t.Fatalf("ReadWireError = (%q, %v), want the message unchanged", plain.Message, err)
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
	// Two different tokens: one token in both headers would let a swap of the
	// two fields go unnoticed (review finding).
	const runTok = "run.header.payload.signature"
	const jobTok = "job.header.payload.signature"
	got, err := RequestFromHTTP(request("Bearer "+jobTok, runTok, "fugaro-aurora-app-review"))
	if err != nil {
		t.Fatalf("RequestFromHTTP: %v", err)
	}
	want := Request{RunIDToken: runTok, JobIDToken: jobTok, Job: "fugaro-aurora-app-review"}
	if got != want {
		t.Fatalf("RequestFromHTTP = %+v, want %+v", got, want)
	}

	// A header the call did not send, a scheme spelled another way, a token
	// that is not one, and a job name that is not one: each refused, and the
	// error names the header rather than the value.
	refused := map[string]*http.Request{
		"no Authorization":      request("", runTok, "fugaro-aurora-app-review"),
		"no run identity":       request("Bearer "+jobTok, "", "fugaro-aurora-app-review"),
		"no job":                request("Bearer "+jobTok, runTok, ""),
		"no bearer scheme":      request(jobTok, runTok, "fugaro-aurora-app-review"),
		"a lower-case scheme":   request("bearer "+jobTok, runTok, "fugaro-aurora-app-review"),
		"an upper-case scheme":  request("BEARER "+jobTok, runTok, "fugaro-aurora-app-review"),
		"another scheme":        request("Basic "+jobTok, runTok, "fugaro-aurora-app-review"),
		"an empty bearer token": request("Bearer ", runTok, "fugaro-aurora-app-review"),
		"a token with a space":  request("Bearer "+jobTok+" x", runTok, "fugaro-aurora-app-review"),
		"an oversized token": request("Bearer "+strings.Repeat("a", maxTokenBytes+1), runTok,
			"fugaro-aurora-app-review"),
		"an oversized run identity": request("Bearer "+jobTok, strings.Repeat("a", maxTokenBytes+1),
			"fugaro-aurora-app-review"),
		"a job name that is not one": request("Bearer "+jobTok, runTok, "fugaro-aurora-app-review\nX-Evil: 1"),
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
			r := request("Bearer "+jobTok, runTok, "fugaro-aurora-app-review")
			r.Header.Add(hdr, "a-second-value")
			if _, err := RequestFromHTTP(r); err == nil {
				t.Fatalf("RequestFromHTTP accepted two %s headers", hdr)
			}
		})
	}

	// The error never carries a value the caller presented.
	r := request("Bearer "+jobTok, runTok, "job with a space")
	_, err = RequestFromHTTP(r)
	if err == nil {
		t.Fatal("RequestFromHTTP accepted a job name with a space")
	}
	for _, leak := range []string{jobTok, runTok, "job with a space"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("err = %q carries the caller's %q", err, leak)
		}
	}
}

// The structured tried list is bounded on both sides of the wire (review
// finding N2): the message field is clipped, but the list is what the runner
// rebuilds a halt message from, and a body that carried more entries than this
// contract allows is one this code would never write. The reader refuses it and
// the writer refuses to write it rather than handing the runner an unbounded
// sentence.
func TestRefusalTriedListIsBoundedOnBothSides(t *testing.T) {
	reset := time.Date(2026, 10, 20, 12, 30, 45, 0, time.UTC)
	full := func(n int) []Tried {
		out := make([]Tried, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, Tried{Name: fmt.Sprintf("acct-%d", i), Tier: TierA, Reason: ReasonExhausted})
		}
		return out
	}

	// At the bound, a writer renders and a reader accepts.
	atBound := ExhaustedUntil{ResetAt: reset, Tried: full(maxTriedEntries)}
	_, _, body, err := atBound.Wire(reset)
	if err != nil {
		t.Fatalf("Wire refused a list of exactly %d entries: %v", maxTriedEntries, err)
	}
	if got, ok, err := ReadExhaustedUntil(StatusExhaustedUntil, body); err != nil || !ok || len(got.Tried) != maxTriedEntries {
		t.Fatalf("ReadExhaustedUntil at the bound = (%d entries, %v, %v), want the list", len(got.Tried), ok, err)
	}

	// One over: the writer refuses to render it and the reader refuses to read
	// it, so neither side can carry a list the other will not.
	over := full(maxTriedEntries + 1)
	if _, _, _, err := (ExhaustedUntil{ResetAt: reset, Tried: over}).Wire(reset); err == nil {
		t.Fatalf("Wire rendered %d tried entries, one over the bound", len(over))
	}
	overBody, err := json.Marshal(WireError{Error: ErrorExhaustedUntil, ResetAt: reset.UnixMilli(), Tried: over})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadExhaustedUntil(StatusExhaustedUntil, overBody); err == nil {
		t.Fatal("ReadExhaustedUntil accepted a body one tried entry over the bound")
	}
	// A general refusal is held to the same rule: the reader and the writer do
	// not disagree about a body just because its error type is not the typed
	// answer (review finding N3).
	if _, err := ReadWireError(StatusUnavailable, overBody); err == nil {
		t.Fatal("ReadWireError accepted a general refusal over the tried bound")
	}
	w := httptest.NewRecorder()
	WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Tried: over})
	if w.Code != StatusUnavailable || !strings.Contains(w.Body.String(), ErrorStateUnavailable) {
		t.Fatalf("WriteWireError wrote %d %q for an over-long list; want the state_unavailable fallback", w.Code, w.Body.String())
	}
	// The fallback body is one the reader can read.
	if _, err := ReadWireError(w.Code, w.Body.Bytes()); err != nil {
		t.Fatalf("the fallback body is not readable: %v", err)
	}

	// An invalid entry on a general refusal is refused too, where before only a
	// general refusal's message was held to anything.
	badBody, err := json.Marshal(WireError{Error: ErrorStateUnavailable, Tried: []Tried{{Name: "acct one", Tier: TierA, Reason: ReasonExhausted}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWireError(StatusUnavailable, badBody); err == nil {
		t.Fatal("ReadWireError accepted a general refusal with a tried name that is not one")
	}
	// And a valid general refusal still writes and reads as itself.
	okBody, err := json.Marshal(WireError{Error: ErrorUnverifiedRunIdentity, Message: "no", Tried: full(1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWireError(StatusUnverified, okBody); err != nil {
		t.Fatalf("ReadWireError refused a well formed general refusal: %v", err)
	}
	w = httptest.NewRecorder()
	WriteWireError(w, StatusUnverified, WireError{Error: ErrorUnverifiedRunIdentity, Message: "no", Tried: full(1)})
	got, err := ReadWireError(w.Code, w.Body.Bytes())
	if err != nil || got.Error != ErrorUnverifiedRunIdentity {
		t.Fatalf("WriteWireError round trip = (%v, %v), want the refusal written as itself", got, err)
	}
}

// The job-name length this contract enforces is internal/backend/gcp's, not a
// second copy of the number (review nit): the longest name gcp.JobName can
// produce is exactly maxJobNameBytes, and ValidJobName accepts it. A change to
// either side of the copy breaks this test rather than silently drifting.
func TestMaxJobNameBytesIsTheJobNameRuleGcpProduces(t *testing.T) {
	longest := gcp.JobName(strings.Repeat("a", 200), strings.Repeat("b", 200))
	if len(longest) != maxJobNameBytes {
		t.Fatalf("the longest job name gcp.JobName produces is %q (%d bytes); want exactly maxJobNameBytes (%d)", longest, len(longest), maxJobNameBytes)
	}
	if !ValidJobName(longest) {
		t.Errorf("ValidJobName(%q) = false, but gcp.JobName produces it", longest)
	}
	if ValidJobName(longest + "a") {
		t.Errorf("ValidJobName accepted a name one byte over the limit gcp.JobName respects")
	}
}
