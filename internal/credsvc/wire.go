package credsvc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
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
//
// Everything here is held to the same rules when it is read as when it is
// written (review finding M2/M3): a tier, a credential name, a reason and a
// reset instant are checked on both sides of the wire, so a value this code
// would refuse to send is also a value it refuses to accept. The rules are
// closed sets — a tier is A, B or C, a reason is one of the Reason*
// constants — because both ends of this contract are this repository's code:
// a new tier or a new reason is a change to this file, which is where the
// contract is written down. And no refusal here repeats what it refused: an
// error names the field, never the value (the package doc's no-echo rule).

// The request headers.
const (
	// HeaderRunIdentity carries the run's Firebase ID token: the budget
	// identity the launcher minted and the runner exchanged (token.Mint,
	// token.Exchange), for uid r~<slug>~<run>. It needs its own header
	// because the Authorization header is the job service account's Google
	// ID token, which Cloud Run IAM consumes for the invoker check (F30).
	HeaderRunIdentity = "X-Fugaro-Run-Identity"
	// HeaderRunJob names the Cloud Run job the run reports it is in: the job's
	// own CLOUD_RUN_JOB. It is the caller's claim and never evidence — a
	// launcher can override any environment variable (internal/runner's
	// jobName) — so the service holds it to the job-name rule (ValidJobName,
	// narrower than the header rule: review finding m3) and records it in the
	// run's record, and decides nothing from it: a run that names no job of
	// this installation cannot be placed, and which job actually called is
	// decided from the caller's verified service-account email (auth.go's
	// verifyJob). Global Constraint 6's refusal of a run in a shared project
	// job until C3.9 is decided there too, from the account and never from this
	// header.
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

// ValidTier reports whether tier is one of the three tiers of the wire. It is
// the rule SetCredentialHeaders writes by and CredentialHeaders reads by: a
// tier is a ledger input (C1.16 settles a call by it — A is notional, B and C
// are real money), so a tier this code does not know is refused on both sides
// rather than carried.
func ValidTier(tier string) bool {
	switch tier {
	case TierA, TierB, TierC:
		return true
	}
	return false
}

// jobNameRE is the shape of a Cloud Run job name as this repository builds
// them: internal/backend/gcp's JobName/ProjectJobName sanitize to lower-case
// letters, digits and hyphens. Cloud Run itself requires a name to start with a
// letter and to end with a letter or a digit. The length is ValidJobName's, at
// maxJobNameBytes, so that the two rules are one rule each: this one says what
// a name is made of and the bound says how long it may be.
var jobNameRE = regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`)

// ValidJobName reports whether s may be the Cloud Run job a call reports it is
// in (HeaderRunJob). It is a narrower rule than the header-value rule on
// purpose (review finding m3): the value is recorded in the run's record and
// in the audit line, and every job name this installation can have is
// [a-z0-9-] and at most 49 characters, so a value that could not be one — a
// quote, a backslash, a '$', a ';', a backtick, a space, 512 bytes of
// anything — is refused rather than recorded. It is still the caller's claim
// and decides nothing (auth.go's verifyJob decides which job called, from the
// verified account).
func ValidJobName(s string) bool {
	return len(s) <= maxJobNameBytes && jobNameRE.MatchString(s)
}

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
	// maxJobNameBytes is internal/backend/gcp's maxJobName: Cloud Run's own 63
	// less room for the execution token the execute page adds.
	maxJobNameBytes = 49
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

// ValidReason reports whether reason is one of the reasons this contract
// carries — in a tried list, where it says why a credential was skipped, and
// in a switch record, where it says why a call moved. It is a closed set read
// by both sides (review finding M3): the reason reaches the run's halt
// message and its switch log, and the four constants above are the whole of
// why this cascade can move, so a fifth is a change to this file.
func ValidReason(reason string) bool {
	switch reason {
	case ReasonExhausted, ReasonBroken, ReasonNotAdmitted, ReasonStopped:
		return true
	}
	return false
}

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

// validHeaderToken reports whether s is one clean header value: non-empty,
// bounded, and printable ASCII with no space — so it can be sent as a header
// and written into a log line without a newline, a control character or a
// space hiding in it. Its callers are ValidCredentialName and the token and
// job-name checks RequestFromHTTP makes on the request headers; a Cloud Run
// job name is held to ValidJobName, which is this rule narrowed.
//
// It is NOT a database path-segment rule, and nothing that becomes a path is
// held to it: it accepts '/', '~', '.' and '..', which are exactly what a
// path segment must not be. The run's slug and run id are held to
// token.ValidSegment instead — the rule the launcher's own minter applies,
// re-checked in splitRunUID and in RTDBOutcomes before either builds a
// budget.PathOutcome — and that is the rule to reach for when a segment is
// what you have.
func validHeaderToken(s string) bool {
	return printableHeaderValue(s, maxHeaderTokenBytes)
}

// printableHeaderValue reports whether s is non-empty, at most max bytes, and
// printable ASCII with no space: the content of one header value, at the bound
// the caller needs. A JWT is held to it at maxTokenBytes (a presented token is
// one header value like any other, and an oversized one is refused at the door
// rather than parsed).
func printableHeaderValue(s string, max int) bool {
	if s == "" || len(s) > max {
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

// validateTried holds one tried entry to the rules of this contract. It is
// asked by both sides — Wire before it renders an answer, ReadExhaustedUntil
// before it hands one to the runner — because a tried entry is what a run's
// halt message is built from (ExhaustedUntil.Error): a name that is not one
// clean value, a tier that is not one of the three, or a reason outside the
// closed set would be carried into that sentence, which is text a human reads
// and a log line a machine keeps.
func validateTried(tr Tried) error {
	if !ValidCredentialName(tr.Name) {
		return errors.New("credsvc: a tried credential's name is not one credential name")
	}
	if !ValidTier(tr.Tier) {
		return errors.New("credsvc: a tried credential's tier is not one of A, B or C")
	}
	if !ValidReason(tr.Reason) {
		return errors.New("credsvc: a tried credential's reason is not one this contract carries")
	}
	return nil
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
//
// The body is decoded strictly (Convention 6: new wire formats decode
// strictly): a field this code does not know is refused rather than ignored,
// so a body that says something this release cannot read is not read as a
// body it can. That is a version-skew break by design — the two halves of
// this contract are this repository's code, and a field added to the refusal
// body is a change to this file.
//
// The body is a caller's text — it arrives from the service, and nothing
// about it is trusted — so no error here repeats it: a decoder's own message
// names the byte it choked on, and this code says only that the body is not
// JSON.
func ReadWireError(status int, body []byte) (WireError, error) {
	if status/100 == 2 {
		return WireError{}, fmt.Errorf("credsvc: status %d is not a refusal", status)
	}
	var e WireError
	if err := decodeStrict(body, &e); err != nil {
		return WireError{}, errors.New("credsvc: the refusal body is not JSON")
	}
	if e.Error == "" {
		return WireError{}, errors.New("credsvc: the refusal body names no error type")
	}
	return e, nil
}

// decodeStrict decodes exactly one JSON value into out: a field this code does
// not know is an error, and so is anything after the value (Convention 6).
func decodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("credsvc: trailing data after the JSON value")
	}
	return nil
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
//
// An answer this code cannot write is an error rather than a body: a zero
// ResetAt is the instant the runner waits for and ReadExhaustedUntil refuses,
// so rendering it would build an answer the other end of this contract cannot
// read (review finding M3), and a tried entry that fails the rules above would
// be carried into the run's halt message. A caller that gets an error here has
// nothing to send: it answers a state_unavailable refusal instead
// (WriteExhaustedUntil does exactly that).
func (e ExhaustedUntil) Wire(now time.Time) (status int, hdr http.Header, body []byte, err error) {
	if e.ResetAt.IsZero() {
		return 0, nil, nil, errors.New("credsvc: the exhausted answer carries no reset instant")
	}
	for _, tr := range e.Tried {
		if err := validateTried(tr); err != nil {
			return 0, nil, nil, err
		}
	}
	hdr = http.Header{}
	hdr.Set("Content-Type", contentTypeJSON)
	if d := e.ResetAt.Sub(now); d > 0 {
		hdr.Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
	}
	body, err = json.Marshal(WireError{
		Error:   ErrorExhaustedUntil,
		Message: e.Error(),
		ResetAt: e.ResetAt.UnixMilli(),
		Tried:   e.Tried,
	})
	if err != nil {
		return 0, nil, nil, fmt.Errorf("credsvc: encoding the exhausted answer: %w", err)
	}
	return StatusExhaustedUntil, hdr, body, nil
}

// WriteExhaustedUntil writes the typed answer to a client. An answer that
// cannot be written is answered as state_unavailable — a typed refusal the
// runner halts on (D143), never a wait for an instant nobody could read.
func WriteExhaustedUntil(w http.ResponseWriter, e ExhaustedUntil, now time.Time) {
	status, hdr, body, err := e.Wire(now)
	if err != nil {
		WriteWireError(w, StatusUnavailable, WireError{
			Error:   ErrorStateUnavailable,
			Message: "the service could not build the exhausted answer",
		})
		return
	}
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
//
// Every tried entry is held to the rules Wire writes them by, for the same
// reason: the entries become the run's halt message. The body's Message field
// is deliberately not read: it is the writer's rendering of the fields, and a
// runner that rebuilt the sentence from validated fields instead cannot be
// made to carry text the contract did not check (review finding M3).
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
	for _, tr := range e.Tried {
		if err := validateTried(tr); err != nil {
			return ExhaustedUntil{}, false, err
		}
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
// name with a newline is a header injection. The error names the field, never
// the value (the no-echo rule): a name is text from another writer.
func SetCredentialHeaders(h http.Header, tier, name string) error {
	if !ValidTier(tier) {
		return errors.New("credsvc: the tier is not one of A, B or C")
	}
	if !ValidCredentialName(name) {
		return fmt.Errorf("credsvc: the credential name is not one clean header value (1 to %d printable ASCII bytes, no space)", maxCredentialNameBytes)
	}
	h.Set(HeaderTier, tier)
	h.Set(HeaderCredential, name)
	return nil
}

// CredentialHeaders reads the tier and the credential's name back, and holds
// them to the rules they were written by (review finding M2). ok is true only
// for a pair this contract could have sent: an answer that carries one of the
// two without the other, an unknown tier or a name that is not one clean value
// is an error rather than a pair — the tier settles a call against the caps
// (C1.16) and the name is what the run records (C1.19), so a caller that
// guessed at either would be reading an answer the service did not give. An
// answer that carries neither is (…, false, nil): most answers are not a
// credential's.
//
// A lone header needs no rule of its own: a missing tier is not one of A, B or
// C, and a missing name is not one clean header value, so each is refused by
// the rule for the half that is missing.
//
// The error names the field and never the value: both arrive as header text
// from another process.
func CredentialHeaders(h http.Header) (tier, name string, ok bool, err error) {
	tier, name = h.Get(HeaderTier), h.Get(HeaderCredential)
	if tier == "" && name == "" {
		return "", "", false, nil
	}
	if !ValidTier(tier) {
		return "", "", false, errors.New("credsvc: the answer's tier is not one of A, B or C")
	}
	if !ValidCredentialName(name) {
		return "", "", false, errors.New("credsvc: the answer's credential name is not one clean header value")
	}
	return tier, name, true, nil
}

// SetSwitchHeader records a switch on an answer. From is empty when the call
// started on the credential that answered it. The reason is one of the
// Reason* constants and a name is one clean header value: the record is what
// the run logs (C1.16), so neither is written unchecked. No error here repeats
// a name or a reason — they are values, and the record is text another writer
// produced.
func SetSwitchHeader(h http.Header, s Switch) error {
	if s.From != "" && !ValidCredentialName(s.From) {
		return errors.New("credsvc: the switch's from is not one credential name")
	}
	if !ValidCredentialName(s.To) {
		return errors.New("credsvc: the switch's to is not one credential name")
	}
	if !ValidReason(s.Reason) {
		return errors.New("credsvc: the switch's reason is not one this contract carries")
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
//
// A switch that is read is held to the same rules as one that is written, and
// for the same reason: the record is logged, displayed and stored. From is
// checked where SetSwitchHeader checks it (empty when the call started on the
// credential that answered it), To likewise, and the reason is one of the
// closed set — a record without the why, or with a why this contract does not
// carry, is not a record of a switch. The header is decoded strictly
// (Convention 6), and no error here repeats a name or a reason: the header is
// text from another process, and a value in an error is a value in a log.
func SwitchHeader(h http.Header) (Switch, bool, error) {
	raw := h.Get(HeaderSwitch)
	if raw == "" {
		return Switch{}, false, nil
	}
	var s Switch
	if err := decodeStrict([]byte(raw), &s); err != nil {
		return Switch{}, false, errors.New("credsvc: the switch header is not one JSON record this code knows")
	}
	if s.From != "" && !ValidCredentialName(s.From) {
		return Switch{}, false, errors.New("credsvc: the switch header's from is not one credential name")
	}
	if !ValidCredentialName(s.To) {
		return Switch{}, false, errors.New("credsvc: the switch header's to is not one credential name")
	}
	if !ValidReason(s.Reason) {
		return Switch{}, false, errors.New("credsvc: the switch header's reason is not one this contract carries")
	}
	return s, true, nil
}

// RequestFromHTTP extracts what a call presents from its HTTP request: the
// caller's Google ID token from the Authorization header's bearer scheme, the
// run's identity from HeaderRunIdentity, and the job the run reports from
// HeaderRunJob (review finding m7). The contract owns this because the rules
// it holds to are the contract's — one value per header, the bearer scheme
// spelled as RFC 6750 spells it, and a job name that could be one — and
// because a server (C1.11) that made its own choices about these headers
// would be a second contract.
//
// It refuses rather than repairs. A header that arrives twice is refused:
// Header.Values reports every value the request carried, and a caller that
// sends two run identities has sent none this service can pick between. An
// Authorization header that is not "Bearer <token>" is refused, including
// "bearer" and "BEARER" — RFC 7235 makes the scheme case-insensitive and this
// code does not guess which spelling the caller meant. A token that is empty,
// over maxTokenBytes, or not one clean header value is refused here rather
// than parsed, and a job name that is not one (ValidJobName) likewise.
//
// It does not check the tokens themselves: that is Verify's, which is the only
// place a presented token is trusted.
func RequestFromHTTP(r *http.Request) (Request, error) {
	auth, err := oneHeader(r, "Authorization")
	if err != nil {
		return Request{}, err
	}
	bearer, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return Request{}, errors.New("credsvc: the Authorization header is not a bearer token")
	}
	run, err := oneHeader(r, HeaderRunIdentity)
	if err != nil {
		return Request{}, err
	}
	job, err := oneHeader(r, HeaderRunJob)
	if err != nil {
		return Request{}, err
	}
	if !printableHeaderValue(bearer, maxTokenBytes) {
		return Request{}, errors.New("credsvc: the Authorization header's token is not one token")
	}
	if !printableHeaderValue(run, maxTokenBytes) {
		return Request{}, fmt.Errorf("credsvc: the %s header is not one token", HeaderRunIdentity)
	}
	if !ValidJobName(job) {
		return Request{}, fmt.Errorf("credsvc: the %s header is not a Cloud Run job name", HeaderRunJob)
	}
	return Request{RunIDToken: run, JobIDToken: bearer, Job: job}, nil
}

// oneHeader returns the single value of a request header, refusing a header
// the call did not send and one it sent more than once. The error names the
// header, which is a constant of this contract and never a caller's value.
func oneHeader(r *http.Request, name string) (string, error) {
	values := r.Header.Values(name)
	switch len(values) {
	case 1:
		return values[0], nil
	case 0:
		return "", fmt.Errorf("credsvc: the call carries no %s header", name)
	}
	return "", fmt.Errorf("credsvc: the call carries the %s header more than once", name)
}
