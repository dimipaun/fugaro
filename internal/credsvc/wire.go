package credsvc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The wire contract between a run's in-runner gateway and this service
// (D135, F30; plan task C1.10). It is written down once, here, because three
// tasks read it: C1.11 answers with it, C1.15's upstream sends it, and C1.17
// waits on it. It is the header names, the JSON refusal body, the headers
// that carry the tier and the credential's name, the switch record, and the
// typed "exhausted until T" answer.
//
// Two rules hold for everything in this file, and a test pins each: nothing
// it carries is ever a credential value (a name, a tier, a reason and an
// instant — never a token), and a name it carries is one clean header value,
// so a name that cannot be sent is refused rather than silently mangled.

// The request headers.
const (
	// HeaderRunIdentity carries the run's Firebase ID token: the budget
	// identity the launcher minted and the runner exchanged (token.Mint,
	// token.Exchange), for uid r~<slug>~<run>. It needs its own header
	// because the Authorization header is the job service account's Google
	// ID token, which Cloud Run IAM consumes for the invoker check (F30).
	HeaderRunIdentity = "X-Fugaro-Run-Identity"
	// HeaderRunJob names the Cloud Run job the run is in (the job's own
	// CLOUD_RUN_JOB, which only the platform sets — a launcher can override
	// any environment variable, internal/runner's jobName). The service
	// refuses a run it cannot place, and refuses a run in a shared project
	// job until C3.9.
	HeaderRunJob = "X-Fugaro-Run-Job"
)

// The response headers.
const (
	// HeaderTier is the credential tier that answered: TierA, TierB or
	// TierC. C1.16 settles the call against the caps from it (A is notional,
	// B and C are real money).
	HeaderTier = "X-Fugaro-Tier"
	// HeaderCredential is the name of the credential that answered, never
	// its value. It is what the run records on its agent entry (C1.19) and
	// what a reader displays — after internal/safetext, because a name is
	// text from another writer.
	HeaderCredential = "X-Fugaro-Credential"
	// HeaderSwitch, when present, is the JSON of a Switch: this call moved
	// from one credential to another inside the service, and the run's
	// record logs it (C1.16).
	HeaderSwitch = "X-Fugaro-Switch"
)

// The credential tiers of the spec, as they are spelled on the wire. Tier C
// is reserved: no code selects it until C3.6.
const (
	TierA = "A"
	TierB = "B"
	TierC = "C"
)

// The error types of the refusal body. The runner switches on these, not on
// a status: two of them share one status.
const (
	// ErrorExhaustedUntil is the typed answer of C1.11: no credential is
	// usable now, and the earliest reset is ResetAt. The runner waits for it
	// (C1.17) — under fallback: oauth-only, whatever the threshold.
	ErrorExhaustedUntil = "exhausted_until"
	// ErrorUnverifiedRunIdentity is the run's own identity being refused
	// (C1.10's Verify).
	ErrorUnverifiedRunIdentity = "unverified_run_identity"
	// ErrorUnverifiedJobIdentity is the caller's Google identity being
	// refused: the ID token Cloud Run IAM admitted is not one of the
	// installation's job service accounts.
	ErrorUnverifiedJobIdentity = "unverified_job_identity"
	// ErrorStateUnavailable is the service being unable to read the state it
	// must read (the write-once outcome, the cascade's marks): a refusal, so
	// the run halts budget_unavailable-style, and never a fallback (D143).
	ErrorStateUnavailable = "state_unavailable"
)

// The statuses. An unverified caller is 403; everything the service cannot
// answer now is 503, and the body's error type says which.
const (
	StatusUnverified       = http.StatusForbidden
	StatusUnavailable      = http.StatusServiceUnavailable
	StatusExhaustedUntil   = http.StatusServiceUnavailable
	contentTypeJSON        = "application/json"
	maxCredentialNameBytes = 200
	maxHeaderTokenBytes    = 512
)

// Why a credential was not used, in the tried list. They are display strings:
// the spec asks a failed cascade to name which credentials were tried and why
// each was skipped.
const (
	ReasonExhausted   = "exhausted"    // marked exhausted until its reset
	ReasonBroken      = "broken"       // a 401: revoked, and only the operator clears it
	ReasonNotAdmitted = "not_admitted" // not in the run's repository's admitted set
	ReasonStopped     = "stopped"      // the operator's kill switch is on
)

// ValidCredentialName reports whether name may be a credential's name: one
// clean header value, at most 200 bytes (the rules' MaxString, the bound the
// agent entry's credential field shares).
//
// The bundle loader (C1.11) and the owner's `fugaro creds set` (C1.25) refuse
// a name this rejects, so a credential that could be loaded but never reported
// to a run does not exist in the first place.
func ValidCredentialName(name string) bool {
	return len(name) <= maxCredentialNameBytes && validHeaderToken(name)
}

// validHeaderToken reports whether s is one clean value: non-empty, bounded,
// and printable ASCII with no space — so it can be sent as a header, written
// into a log line, and used as a database path segment without a newline, a
// control character or a separator hiding in it. The run's slug and run id,
// the Cloud Run job's name and a credential's name are all held to it.
func validHeaderToken(s string) bool {
	if s == "" || len(s) > maxHeaderTokenBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Tried is one credential the service considered and did not use.
type Tried struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

// WireError is the JSON body of every refusal the service writes. It carries
// names, tiers, reasons and an instant — never a value, and never anything
// the caller presented.
//
// Its type field is Error because the wire's key is "error", so the value
// cannot also have an Error method (Go allows a field and a method of the
// same name only one at a time). The value a caller of this package gets as an
// error is ExhaustedUntil, or a WireError's Error field read out by hand.
type WireError struct {
	Error   string  `json:"error"`
	Message string  `json:"message,omitempty"`
	ResetAt int64   `json:"reset_at,omitempty"`
	Tried   []Tried `json:"tried,omitempty"`
}

// WriteWireError writes the refusal: its type in the body, its status on the
// response. A message the caller reads names no credential value.
func WriteWireError(w http.ResponseWriter, status int, e WireError) {
	body, err := json.Marshal(e)
	if err != nil {
		// Unreachable: a WireError is strings and an integer. A body the
		// runner can read is still better than none.
		body = []byte(`{"error":"` + ErrorStateUnavailable + `"}`)
		status = StatusUnavailable
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ReadWireError reads a refusal body. A 2xx is not a refusal, and a body that
// is not JSON or names no type is an error rather than an empty refusal.
func ReadWireError(status int, body []byte) (WireError, error) {
	if status/100 == 2 {
		return WireError{}, fmt.Errorf("credsvc: status %d is not a refusal", status)
	}
	var e WireError
	if err := json.Unmarshal(body, &e); err != nil {
		return WireError{}, fmt.Errorf("credsvc: the refusal body is not JSON: %w", err)
	}
	if e.Error == "" {
		return WireError{}, errors.New("credsvc: the refusal body names no error type")
	}
	return e, nil
}

// ExhaustedUntil is C1.11's typed answer: no credential is usable before
// ResetAt. Tried is every credential that was considered, in the order it was
// considered, with why it was skipped.
type ExhaustedUntil struct {
	ResetAt time.Time
	Tried   []Tried
}

// Error is the sentence a run's halt message is built from. It names the
// instant and the credentials tried, never a value.
func (e ExhaustedUntil) Error() string {
	var b strings.Builder
	b.WriteString("no credential is usable before ")
	b.WriteString(e.ResetAt.UTC().Format(time.RFC3339))
	for i, tr := range e.Tried {
		if i == 0 {
			b.WriteString("; tried ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(tr.Name)
		if tr.Reason != "" {
			b.WriteString(" (")
			b.WriteString(tr.Reason)
			b.WriteString(")")
		}
	}
	return b.String()
}

// Wire renders the typed answer: the status, the headers and the body. now is
// the service's clock, so the courtesy Retry-After (whole seconds until the
// reset, which an intermediary may honour) is computed from it and not from a
// hidden clock. A reset that has already passed carries no Retry-After: it
// would be a lie to a proxy, and the runner reads the body's instant.
func (e ExhaustedUntil) Wire(now time.Time) (status int, hdr http.Header, body []byte) {
	hdr = http.Header{}
	hdr.Set("Content-Type", contentTypeJSON)
	if d := e.ResetAt.Sub(now); d > 0 {
		hdr.Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
	}
	body, _ = json.Marshal(WireError{
		Error:   ErrorExhaustedUntil,
		Message: e.Error(),
		ResetAt: e.ResetAt.UnixMilli(),
		Tried:   e.Tried,
	})
	return StatusExhaustedUntil, hdr, body
}

// WriteExhaustedUntil writes the typed answer to a client.
func WriteExhaustedUntil(w http.ResponseWriter, e ExhaustedUntil, now time.Time) {
	status, hdr, body := e.Wire(now)
	for k, v := range hdr {
		w.Header()[k] = v
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ReadExhaustedUntil reads the typed answer from a response's status and
// body. ok is false when the answer is a well formed body of another kind —
// the runner must not read a refusal as a wait — and an error when the body
// claims to be the typed answer and is not readable, or carries no reset
// instant, which is the one thing the runner waits for.
func ReadExhaustedUntil(status int, body []byte) (ExhaustedUntil, bool, error) {
	if status != StatusExhaustedUntil {
		return ExhaustedUntil{}, false, nil
	}
	e, err := ReadWireError(status, body)
	if err != nil {
		return ExhaustedUntil{}, false, err
	}
	if e.Error != ErrorExhaustedUntil {
		return ExhaustedUntil{}, false, nil
	}
	if e.ResetAt <= 0 {
		return ExhaustedUntil{}, false, errors.New("credsvc: the exhausted answer carries no reset instant")
	}
	return ExhaustedUntil{ResetAt: time.UnixMilli(e.ResetAt).UTC(), Tried: e.Tried}, true, nil
}

// Switch is one credential change inside the service: what the call was on,
// what answered, and why it moved. It is what the run's switch log records
// (C1.16) and what the spec asks to be logged per switch.
type Switch struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// SetCredentialHeaders records the tier and the credential's name on an
// answer. A tier it does not know or a name that is not one clean header
// value is an error: an answer that cannot carry them must not be sent as if
// it did — a wrong tier is a ledger error (notional against real money) and a
// name with a newline is a header injection.
func SetCredentialHeaders(h http.Header, tier, name string) error {
	switch tier {
	case TierA, TierB, TierC:
	default:
		return fmt.Errorf("credsvc: %q is not a credential tier", tier)
	}
	if !ValidCredentialName(name) {
		return fmt.Errorf("credsvc: %q is not a credential name (1 to %d printable ASCII bytes, no space)", name, maxCredentialNameBytes)
	}
	h.Set(HeaderTier, tier)
	h.Set(HeaderCredential, name)
	return nil
}

// CredentialHeaders reads the tier and the credential's name back. Either is
// empty when the answer did not carry it.
func CredentialHeaders(h http.Header) (tier, name string) {
	return h.Get(HeaderTier), h.Get(HeaderCredential)
}

// SetSwitchHeader records a switch on an answer. From is empty when the call
// started on the credential that answered it.
func SetSwitchHeader(h http.Header, s Switch) error {
	if s.From != "" && !ValidCredentialName(s.From) {
		return fmt.Errorf("credsvc: %q is not a credential name", s.From)
	}
	if !ValidCredentialName(s.To) {
		return fmt.Errorf("credsvc: %q is not a credential name", s.To)
	}
	if s.Reason == "" {
		return errors.New("credsvc: a switch carries a reason")
	}
	body, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("credsvc: encoding the switch: %w", err)
	}
	h.Set(HeaderSwitch, string(body))
	return nil
}

// SwitchHeader reads a switch back. ok is false when the answer carries none;
// an error means the answer carries one this code cannot read, which the
// run's record must not silently drop.
func SwitchHeader(h http.Header) (Switch, bool, error) {
	raw := h.Get(HeaderSwitch)
	if raw == "" {
		return Switch{}, false, nil
	}
	var s Switch
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Switch{}, false, fmt.Errorf("credsvc: the switch header is not JSON: %w", err)
	}
	if !ValidCredentialName(s.To) {
		return Switch{}, false, fmt.Errorf("credsvc: the switch header names %q, which is not a credential name", s.To)
	}
	return s, true, nil
}
