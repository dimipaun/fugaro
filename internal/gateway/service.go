package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// C1.15. Everything in this file exists for one thing: a run's model calls
// going to the installation's credentials service instead of to Anthropic,
// with the job's own Google identity and the run's, and the answer's tier
// read back for the ledger and the runner (D135, D143). It runs in the job,
// in the in-runner gateway; the service's own half of the contract is
// internal/credsvc, and the OAuth upstream this kind reaches through the
// service is oauth.go.
//
// Deleting the cascade means deleting this file and the case that names
// KindService; no shared code is unpicked.
//
// # Why the wire contract is mirrored here
//
// The names, the closed sets and the reader of a refusal below are a mirror
// of C1.10's contract in internal/credsvc, and not that package itself:
// internal/credsvc imports this one (C1.8's exported helpers are what the
// service forwards with), so this package cannot import it without a cycle.
// The mirror is held to the contract by TestServiceContractMirrorsCredsvc,
// an external test file (package gateway_test, which may import
// internal/credsvc) that runs both readers over one table of answers, and by
// TestServiceConstantsMirrorCredsvc, which compares the names one by one.
// This is the shape the repository already uses for a value two packages
// share (MaxFeePct, pinned by a test that the two are equal).
//
// # What never reaches the agent, and what never reaches a log
//
// The tier, the credential's name and the switch are the service's answer to
// this gateway and to nobody else: they are dropped from the answer before
// it is copied on (dropResponseHeaders), so the agent never learns the set
// of credentials its project holds — which is what D143's accepted single
// point of compromise rests on. A refusal the service wrote is fugaro's own
// text and is never relayed either: the agent gets a sentence this gateway
// builds from validated fields.
//
// No value is ever logged or rendered here. The credential's *name* is
// carried (it is what the run's record displays, C1.19) and it is never a
// value; the two tokens this file attaches are attached and nothing else —
// they are not logged, not written into an error, and not put in a halt's
// detail, and every sentence below is one this file spells.

// KindService is the C1.15 upstream kind: the installation's credentials
// service, which answers the Messages API's own paths with a credential it
// holds and this job does not (D135). It serves the paths the anthropic
// upstream serves, with the same gateway token in front, so the agent's
// configuration is the same one byte for byte: only the destination and the
// credential differ.
const KindService = "service"

// The request headers of the contract (credsvc.HeaderRunIdentity,
// credsvc.HeaderRunJob). The job service account's Google ID token travels
// in Authorization, which Cloud Run IAM consumes for the invoker check (F30),
// so the run's own identity needs a header of its own.
const (
	// HeaderRunIdentity carries the run's Firebase ID token.
	HeaderRunIdentity = "X-Fugaro-Run-Identity"
	// HeaderRunJob names the Cloud Run job the run reports it is in. It is
	// the caller's claim and never evidence (C1.10).
	HeaderRunJob = "X-Fugaro-Run-Job"
)

// The answer headers of the contract (credsvc.HeaderTier,
// credsvc.HeaderCredential, credsvc.HeaderSwitch, credsvc.HeaderProbe).
const (
	// HeaderTier is the credential tier that answered: TierA, TierB or
	// TierC. A Messages call settles by it (C1.16).
	HeaderTier = "X-Fugaro-Tier"
	// HeaderCredential is the name of the credential that answered, never
	// its value.
	HeaderCredential = "X-Fugaro-Credential"
	// HeaderSwitch, when present, is the JSON of a switch: this call moved
	// from one credential to another inside the service.
	HeaderSwitch = "X-Fugaro-Switch"
	// HeaderProbe, when present, names the tiers the service probed while
	// answering this call, comma separated and once each. A probe is a call
	// the run pays for, and this is the only place the run can learn of it
	// (C1.12, C1.16): it settles as notional on tier A and as money on any
	// other tier.
	HeaderProbe = "X-Fugaro-Probed"
)

// The credential tiers, as they are spelled on the wire (credsvc.TierA and
// its neighbours). Tier C is reserved.
const (
	TierA = "A"
	TierB = "B"
	TierC = "C"
)

// The error types of a refusal body (credsvc.ErrorExhaustedUntil and its
// neighbours). The runner switches on these, not on a status: two of them
// share one status.
const (
	// ErrorExhaustedUntil is the typed answer: no credential is usable
	// before the answer's instant. It is the one refusal that does not halt
	// the run: the runner waits for the instant (C1.17).
	ErrorExhaustedUntil = "exhausted_until"
	// ErrorUnverifiedRunIdentity is the run's own identity being refused.
	ErrorUnverifiedRunIdentity = "unverified_run_identity"
	// ErrorUnverifiedJobIdentity is the caller's Google identity being
	// refused: the ID token Cloud Run IAM admitted is not one of the
	// installation's job service accounts.
	ErrorUnverifiedJobIdentity = "unverified_job_identity"
	// ErrorStateUnavailable is the service being unable to read the state it
	// must read: a refusal, so the run halts (D143), never a fallback.
	ErrorStateUnavailable = "state_unavailable"
)

// Why a credential was not used, in a refusal's tried list
// (credsvc.ReasonExhausted and its neighbours). A closed set.
const (
	ReasonExhausted   = "exhausted"
	ReasonBroken      = "broken"
	ReasonNotAdmitted = "not_admitted"
	ReasonStopped     = "stopped"
)

// ReasonBudgetUnavailable is the halt a run that cannot reach the service is
// recorded with (D143: "the run halts like budget_unavailable"), the same
// spelling runstore.HaltBudgetUnavailable and budget.ReasonBudgetUnavailable
// use. A test pins the three.
const ReasonBudgetUnavailable = "budget_unavailable"

const (
	// maxCredentialNameBytes is the contract's bound on a credential's name
	// (credsvc's maxCredentialNameBytes: the rules' MaxString).
	maxCredentialNameBytes = 200
	// maxJobNameBytes is internal/backend/gcp's maxJobName: Cloud Run's own
	// 63 less room for the execution token the execute page adds
	// (credsvc's maxJobNameBytes).
	maxJobNameBytes = 49
	// maxRefusalMessageBytes bounds the free-text message of a refusal body
	// (credsvc's maxRefusalMessageBytes). The message is never read back —
	// the sentence is rebuilt from the fields — but a body this contract
	// could not have written is not read as one it did.
	maxRefusalMessageBytes = 8 << 10
	// maxTriedEntries bounds a refusal's tried list (credsvc's
	// maxTriedEntries; C1.11 must refuse a bundle larger than it).
	maxTriedEntries = 64
	// maxServiceHeaderBytes is the contract's bound on a token-shaped header
	// value (credsvc's maxHeaderTokenBytes).
	maxServiceHeaderBytes = 512
	// maxServiceRefusalBytes is more than any legal refusal body can be: the
	// message's own bound plus maxTriedEntries entries of names at their own
	// bound. A longer answer is not one this contract wrote, and is relayed
	// rather than read (a model error's body is another writer's and may be
	// any size).
	maxServiceRefusalBytes = maxRefusalMessageBytes + maxTriedEntries*(maxCredentialNameBytes+64)
)

// jobNameRE is the shape of a Cloud Run job name (credsvc's jobNameRE): what
// internal/backend/gcp sanitizes to, and what Cloud Run itself requires of a
// name.
var jobNameRE = regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`)

// Service is how a service upstream identifies itself to the credentials
// service (D135): the job's Google identity, which Cloud Run IAM admits for
// run.invoker, and the run's own.
//
// Every field is required, and validate refuses a service upstream without
// one: the service verifies both identities on every call (C1.10), so an
// upstream that cannot present them cannot be served, and there is nothing
// else it could fall back to (D143).
type Service struct {
	// IDToken mints the job service account's Google ID token for the
	// service's URL (Upstream.BaseURL): the audience Cloud Run IAM checks.
	// It is called once per request — see metadataIDToken in
	// internal/runner for why the job does not cache it — and an error
	// means the call is not sent.
	//
	// It is called from concurrent calls, so it must be safe for that.
	IDToken func(ctx context.Context) (string, error)
	// RunIDToken is the run's own Firebase ID token, the budget identity
	// the launcher minted (C1.10's HeaderRunIdentity). It is read per
	// request — the session's token changes under it — and an empty value
	// means the call is not sent: an unauthenticated call to the service
	// under this run's other identity is not a call this gateway makes.
	//
	// It is called from concurrent calls, so it must be safe for that.
	RunIDToken func() string
	// Job is the Cloud Run job name the run reports it is in
	// (HeaderRunJob). The service holds it to the job-name rule and decides
	// nothing from it — which job called is decided from the verified
	// service-account email — so this is a claim, and validate refuses one
	// that could not be a job name.
	Job string
}

// ServiceAnswer is what the credentials service said about one call: the
// tier that answered it, the credential's name, and the switch it made
// (C1.10's answer headers; C1.16 settles the call by the tier, C1.19 records
// the name, C1.16 logs the switch).
type ServiceAnswer struct {
	// Tier is the tier the answer was served from: TierA, TierB or TierC.
	Tier string
	// Credential is the name of the credential that answered, never its
	// value.
	Credential string
	// Switch is the contract's switch header as it arrived: the JSON of a
	// Switch record, unread here. A switch is what the run's own log carries
	// (C1.16), and the runner reads it with the contract's own reader
	// (credsvc.SwitchHeader) rather than this package keeping a second
	// reading of it.
	Switch string
	// SwitchCost is what this answer's switch is estimated to cost (C1.16,
	// F24), computed here from the last call's cache reads and the tier
	// that answered, and zero when the answer carries no switch. It is the
	// record's "estimated cache-miss cost", and the service cannot supply
	// it: what the run has cached is the gateway's own knowledge.
	SwitchCost SwitchCost
	// Probed is every tier the service made a probe on while answering this
	// call (C1.12's HeaderProbe), once each and in the order it made them.
	// A probe is a call, so it is the run's to account for (C1.16), and the
	// service is the only place that knows it happened: its own answer is
	// closed unread, and its audit line is the service's record, not the
	// run's. The tiers are what the ledger needs and the names are not
	// carried -- which credential was probed is C1.13's line, and the run
	// learns the name of the credential that ANSWERED from Credential.
	Probed []string
	// Exhausted, when set, is the service's typed answer that no credential
	// is usable before its instant: this call was not served, and the one
	// thing to do about it is wait (C1.17). Every other field is then the
	// zero value: an exhausted answer carries no tier, and the credentials
	// it names are in Tried.
	Exhausted *Exhausted
}

// Exhausted is the service's typed exhausted answer (credsvc's
// ExhaustedUntil): no credential is usable before ResetAt, and Tried is every
// credential the service considered, in the order it considered them, with
// why each was skipped.
//
// It is the one refusal that does not halt the run: a wait is what the runner
// does with it (C1.17), and D143's halt is for a service the run cannot reach
// or cannot be served by at all. Until the runner acts on it the call is
// answered 503 with no retry-after of its own, so the client backs off by its
// own schedule rather than sleeping for an instant the run has not agreed to
// wait for.
type Exhausted struct {
	ResetAt time.Time
	Tried   []Tried
}

// Tried is one credential the service considered and did not use
// (credsvc's Tried).
type Tried struct {
	Name   string
	Tier   string
	Reason string
}

// serviceUpstream builds a service call's headers and credential: the model
// request's own headers as the anthropic upstream would forward them, the
// job service account's Google ID token as the bearer credential Cloud Run
// IAM checks, the run's identity and the job name in the contract's headers,
// and nothing else. Both tokens are attached here and in no other place in
// the gateway.
//
// The invariant D143 rests on is that this call carries no credential of the
// job's own: validate refuses a service upstream that has one, and this
// function writes only the two identities above — the agent's own headers,
// the gateway token and every credential header Claude Code sent are already
// gone from what forwardHeaders returns. A route is refused as well, here and
// in validateRoutes, on oauthUpstream's reasoning: a route is a provider's
// own endpoint and is reached with the provider's key, which a service run
// does not have.
func (s *Server) serviceUpstream(ctx context.Context, rt route, in http.Header) (http.Header, error) {
	if rt.provider != nil {
		return nil, errServiceOnRoute
	}
	sv := s.o.Upstream.Service
	if sv == nil || sv.IDToken == nil || sv.RunIDToken == nil {
		return nil, fmt.Errorf("%w: the credentials service's identity isn't available", errServiceUnidentified)
	}
	run := sv.RunIDToken()
	if run == "" {
		// An unauthenticated call to the service under the job's own Google
		// identity is not a call this gateway makes (D143): the run's
		// identity is what the service verifies the run with, and there is
		// no second way to be admitted.
		return nil, fmt.Errorf("%w: the run's identity token isn't available", errServiceUnidentified)
	}
	id, err := sv.IDToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: the job's identity token isn't available: %v", errServiceUnidentified, err)
	}
	if id == "" {
		return nil, fmt.Errorf("%w: the job's identity token isn't available", errServiceUnidentified)
	}
	h := forwardHeaders(in)
	s.scrubToken(h)
	// The service's own answer headers mean nothing in a request, and a
	// client that sent them must not have them arrive as if the service had
	// answered with them.
	for _, k := range []string{HeaderTier, HeaderCredential, HeaderSwitch, HeaderProbe} {
		h.Del(k)
	}
	h.Set("Authorization", "Bearer "+id)
	h.Set(HeaderRunIdentity, run)
	h.Set(HeaderRunJob, sv.Job)
	return h, nil
}

// errServiceOnRoute is the refusal both validateRoutes and serviceUpstream
// give a route offered to a service upstream. There is no cycle to break
// here (a route's key would be the job's own credential, which a service run
// does not have): it is refused by name so a misconfiguration says what it
// is, and in both places so a Route built in code past validation cannot
// reach a provider through a service run either.
var errServiceOnRoute = errors.New("a provider route is never reached through the credentials service: a route is a provider's own endpoint and is called with the job's own key, which a cascade run does not have (D143)")

// errServiceUnidentified is every way a service call fails before it is sent
// for want of an identity: the run's own token, or the job's Google ID token,
// which only the metadata server mints. They are one error because the run's
// reaction to all of them is one -- there is no credential of the job's to
// fall back to (D143), so a call that cannot present an identity is a call
// that will never be admitted, and the run halts budget_unavailable-style
// rather than being answered 502 once per call until its deadline.
//
// Nothing is ever sent without both tokens: serviceUpstream attaches them and
// returns this error before any request exists, so a caller can only see it
// with nothing on the wire.
var errServiceUnidentified = errors.New("this job cannot present its identity to the credentials service")

// serviceResponseHeaderTimeout bounds how long a service call waits for the
// service's response headers: a connection that never answers at all is one
// this run cannot be served by, and without a bound it would hang until the
// stage's own deadline instead of halting (D143). It covers the wait for the
// headers only, never the body: the service streams a model answer, and the
// gateway never buffers a stream. The bound is generous on purpose -- the
// service may be cold-starting, and it may retry the same request on another
// credential before its first byte (C1.11) -- so it is a backstop for a
// connection that is gone, not a per-call limit. A var, like endStageWait and
// oauthResponseHeaderTimeout, so a test can shorten it.
var serviceResponseHeaderTimeout = 10 * time.Minute

// serviceUpstreamClient is the client a service call is made with: the
// gateway's own, with the response-header bound above. It states the bound
// here rather than inheriting it from the order Start happens to build its
// client in, exactly as oauthUpstreamClient does.
func serviceUpstreamClient(c *http.Client) *http.Client {
	out := *c
	tr := c.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	if t, ok := tr.(*http.Transport); ok {
		nt := t.Clone()
		nt.ResponseHeaderTimeout = serviceResponseHeaderTimeout
		out.Transport = nt
	}
	return &out
}

// validTier reports whether tier is one of the three of the contract.
func validTier(t string) bool { return t == TierA || t == TierB || t == TierC }

// validReason reports whether reason is one of the four of the contract's
// closed set.
func validReason(r string) bool {
	switch r {
	case ReasonExhausted, ReasonBroken, ReasonNotAdmitted, ReasonStopped:
		return true
	}
	return false
}

// validCredentialName reports whether name may be a credential's name: one
// clean header value at the contract's bound (credsvc.ValidCredentialName).
func validCredentialName(name string) bool {
	return len(name) <= maxCredentialNameBytes && printableValue(name)
}

// validJobName reports whether s may be the Cloud Run job a call reports it
// is in (credsvc.ValidJobName): a name this installation could have, bounded
// so that the two rules are one rule each.
func validJobName(s string) bool {
	return len(s) <= maxJobNameBytes && jobNameRE.MatchString(s)
}

// printableValue reports whether s is one clean header value: non-empty,
// bounded, printable ASCII with no space (credsvc's validHeaderToken), so it
// can be displayed and logged without a newline or a control character
// hiding in it.
func printableValue(s string) bool {
	if s == "" || len(s) > maxServiceHeaderBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// responseHeader returns the single value of an answer's header, present
// false when the answer carries none. An answer that carries it more than
// once is an error: the tier settles a call against the caps and the name is
// what the run records, so neither may be decided by header order (C1.10's
// responseHeader, which this mirrors).
func responseHeader(h http.Header, name string) (value string, present bool, err error) {
	values := h.Values(name)
	switch len(values) {
	case 1:
		return values[0], true, nil
	case 0:
		return "", false, nil
	}
	return "", false, fmt.Errorf("the answer carries the %s header more than once", name)
}

// credentialHeaders reads the tier and the credential's name back, holding
// them to the rules they were written by (C1.10's CredentialHeaders). ok is
// false for an answer that carries neither, which is most answers that are
// not a credential's; an answer carrying one without the other, an unknown
// tier or a name that is not one clean value is an error rather than a pair.
//
// No error here repeats a value: both arrive as header text from another
// process.
func credentialHeaders(h http.Header) (tier, name string, ok bool, err error) {
	tier, haveTier, err := responseHeader(h, HeaderTier)
	if err != nil {
		return "", "", false, err
	}
	name, haveName, err := responseHeader(h, HeaderCredential)
	if err != nil {
		return "", "", false, err
	}
	if !haveTier && !haveName {
		return "", "", false, nil
	}
	if !validTier(tier) {
		return "", "", false, errors.New("the answer's tier isn't one this contract carries")
	}
	if !validCredentialName(name) {
		return "", "", false, errors.New("the answer's credential name isn't one clean header value")
	}
	return tier, name, true, nil
}

// switchValue reads the contract's switch header, unread. An answer that
// carries it twice is an error for the same reason as above: which of two
// records is logged must not be decided by header order.
func switchValue(h http.Header) (string, error) {
	v, _, err := responseHeader(h, HeaderSwitch)
	return v, err
}

// maxProbedTiers is how many tiers the probe header can name: the tiers are a
// closed set of three and a call probes at most one credential per tier
// (C1.12). It is credsvc's own bound (maxProbedTiers), respelled because the
// package exports it only as the writer's rule; a list longer than it is not
// this contract's, and is refused rather than read.
const maxProbedTiers = 3

// probedTiers reads the tiers the service probed while answering this call
// (C1.12's HeaderProbe). No answer carrying none is the common case and is
// not an error: a call the service could serve without asking a backed-off
// credential anything probed nothing.
//
// Every refusal here is the reader's own sentence: the tiers arrive as header
// text from another process, and a tier that is not one of the contract's
// three is a value this ledger must not be asked to charge by -- the by-tier
// split has a leaf for B and C and none for anything else (C1.16).
func probedTiers(h http.Header) ([]string, error) {
	value, present, err := responseHeader(h, HeaderProbe)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxProbedTiers {
		return nil, errors.New("the answer's probe header names more tiers than this contract carries")
	}
	seen := make(map[string]bool, len(parts))
	for _, tier := range parts {
		if !validTier(tier) {
			return nil, errors.New("the answer's probed tier isn't one this contract carries")
		}
		if seen[tier] {
			return nil, errors.New("the answer's probe header names one tier twice")
		}
		seen[tier] = true
	}
	return parts, nil
}

// serviceRefusal is the JSON body of a refusal, the mirror of credsvc's
// WireError. Message is decoded and never read: the contract's own reader
// keeps the sentence out of the caller's hands, and this one does not even
// hand it on — a sentence built from the validated fields is what a halt
// says.
type serviceRefusal struct {
	Error   string         `json:"error"`
	Message string         `json:"message,omitempty"`
	ResetAt int64          `json:"reset_at,omitempty"`
	Tried   []serviceTried `json:"tried,omitempty"`
}

// serviceTried is one entry of a refusal's tried list (credsvc's Tried).
type serviceTried struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

// readServiceRefusal reads a non-2xx answer as the service's own refusal.
// ok is false when the answer is not one — a 2xx, or a body that does not say
// it is a refusal (claimsRefusal) — which is how a *model* error the service
// relayed is told from the service's own answer: that one is relayed to the
// agent as it always was, and a refusal never is.
//
// An error means the answer says it is a refusal and is not one this contract
// could have written: a body over the contract's bound, one that does not
// decode strictly, one with something after its JSON value, a message past
// its bound, or a tried list that breaks its rules. The caller must not act
// on it as a refusal — and must not relay it either, which is the whole point
// of asking here rather than at the relay: a refusal names the project's
// credentials, and a body that claims to be one is never put in front of the
// agent, whether or not this release can read it.
//
// The validating half is the contract's own (validateTriedList,
// ReadWireError, ReadExhaustedUntil), mirrored here; the two are held
// together by TestServiceContractMirrorsCredsvc.
func readServiceRefusal(status int, body []byte) (serviceRefusal, bool, error) {
	if status/100 == 2 {
		return serviceRefusal{}, false, nil
	}
	if !claimsRefusal(body) {
		return serviceRefusal{}, false, nil
	}
	// From here the body says it is this service's own refusal: it is acted
	// on as one or refused as one, and never relayed to the agent -- a body
	// this contract could not have written names the project's credentials,
	// and the agent has no business seeing the set of them (D143).
	if len(body) >= maxServiceRefusalBytes {
		// Longer than any refusal this contract can write.
		return serviceRefusal{}, true, errors.New("the refusal is longer than this contract's")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var e serviceRefusal
	if dec.Decode(&e) != nil {
		return serviceRefusal{}, true, errors.New("the refusal isn't a body this contract could have written")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return serviceRefusal{}, true, errors.New("the refusal carries something after its JSON value")
	}
	if len(e.Message) > maxRefusalMessageBytes {
		return serviceRefusal{}, true, errors.New("the refusal's message is longer than this contract's")
	}
	if err := validTriedList(e.Tried); err != nil {
		return serviceRefusal{}, true, err
	}
	return e, true, nil
}

// claimsRefusal reports whether a body says it is this service's own refusal:
// a JSON object whose "error" member is a non-empty string, and which is not
// the model API's own error envelope — {"type":"error","error":{...}} — which
// the service relays from the credential's upstream and which is another
// writer's body.
//
// It is deliberately loose where the reader above is strict. The question
// here is not "can this release read the refusal" but "is this the service's
// own text": a refusal this release cannot read halts the run rather than
// being relayed (readServiceRefusal's error), so a field a later service adds
// to a refusal body cannot put a sentence naming the project's credentials in
// front of the agent. No error is returned and no part of the body is echoed.
func claimsRefusal(body []byte) bool {
	var probe struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	// The first JSON value alone: a body with something after it still says
	// what it is, and the strict reader below is what refuses the trailer.
	if json.NewDecoder(bytes.NewReader(body)).Decode(&probe) != nil || probe.Type == "error" {
		return false
	}
	var typ string
	return json.Unmarshal(probe.Error, &typ) == nil && typ != ""
}

// modelErrorEnvelope reports whether a body is the model API's own error
// envelope -- {"type":"error","error":{"type":...,"message":...}} -- which the
// service relays from the credential's upstream unchanged (C1.11). It is the
// one non-2xx body that is another writer's and is meant for the agent, and it
// is what separates a relayed model error from the platform's own front door:
// both arrive as 401 or 403, and the two must not be treated alike.
//
// It is as strict as the relay allows, because the two mistakes are not equal.
// Failing to recognise a relayed model error stops a run that another
// credential could serve and hides the model's own sentence from the agent;
// failing to recognise the platform's front door relays a body that names the
// project and the caller, which is the one thing D143 refuses. So the envelope
// has to be there in full: the type that identifies it, and the error object
// inside it with a type of its own, which is the shape the model API writes and
// the platform's own JSON ({"error":{"code":...,"status":...}}) does not.
func modelErrorEnvelope(body []byte) bool {
	var probe struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	// The first JSON value alone, as claimsRefusal reads it: a body with
	// something after it still says what it is, and a relayed body is passed
	// through whole whatever follows.
	if json.NewDecoder(bytes.NewReader(body)).Decode(&probe) != nil {
		return false
	}
	return probe.Type == "error" && probe.Error.Type != ""
}

// validTriedList holds a tried list to the contract's rules: the count bound
// first, then every entry by name, tier and reason. It is what the contract's
// own reader asks of every refusal body (credsvc.validateTriedList): the
// names reach a run's halt message, which is text a human reads.
func validTriedList(tried []serviceTried) error {
	if len(tried) > maxTriedEntries {
		return fmt.Errorf("the refusal carries %d tried credentials, over this contract's bound of %d", len(tried), maxTriedEntries)
	}
	for _, tr := range tried {
		if !validCredentialName(tr.Name) {
			return errors.New("a tried credential's name isn't one credential name")
		}
		if !validTier(tr.Tier) {
			return errors.New("a tried credential's tier isn't one this contract carries")
		}
		if !validReason(tr.Reason) {
			return errors.New("a tried credential's reason isn't one this contract carries")
		}
	}
	return nil
}

// exhaustedAnswer is the typed exhausted answer as this package hands it on,
// or an error when the answer is not one this contract could have written:
// the instant is the one thing the runner waits for, so an answer without it,
// or with a list the contract does not allow, is not a wait.
func exhaustedAnswer(e serviceRefusal) (Exhausted, error) {
	if e.ResetAt <= 0 {
		return Exhausted{}, errors.New("the exhausted answer carries no reset instant")
	}
	tried := make([]Tried, 0, len(e.Tried))
	for _, tr := range e.Tried {
		tried = append(tried, Tried{Name: tr.Name, Tier: tr.Tier, Reason: tr.Reason})
	}
	return Exhausted{ResetAt: time.UnixMilli(e.ResetAt).UTC(), Tried: tried}, nil
}

// serviceAnswer reads what the credentials service said about a call and acts
// on it, before any of the answer reaches the agent. It reports the status
// the agent's answer was written with and done true when the answer was dealt
// with here — a refusal, a halt, or an answer this gateway cannot read — and
// done false when the answer is the model's own and the caller relays it.
//
// needTier is a Messages call's answer: it settles by the tier C1.16 charges
// it at, so an answer that carries none (or a pair the contract would not
// write) halts the run rather than settling by a guess. A token count is free
// and settles nothing, and asks for nothing.
//
// The probes the answer reports (HeaderProbe, C1.12) come back with the tier:
// each one is a call the run paid for and settles the same way the call does,
// by the tier it was made on (C1.16).
//
// A non-2xx answer is read as a refusal only when it is one; a model error
// the service relayed is its own writer's text, reaches the agent as it does
// today, and its body is put back on the response for the relay to stream. A
// refusal is never relayed: it is fugaro's own answer, it names the project's
// credentials, and the agent has no business seeing the set of them (D143).
func (s *Server) serviceAnswer(w http.ResponseWriter, resp *http.Response, needTier bool) (status int, done bool, tier string, probed []string) {
	if resp.StatusCode/100 == 2 {
		if !needTier {
			return resp.StatusCode, false, "", nil
		}
		tier, name, ok, err := credentialHeaders(resp.Header)
		if err == nil && !ok {
			err = errors.New("the answer carries no tier")
		}
		if err != nil {
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		sw, err := switchValue(resp.Header)
		if err != nil {
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		if probed, err = probedTiers(resp.Header); err != nil {
			// An answer whose probes cannot be read is a halt rather than a
			// ledger input dropped: the call was made and somebody paid for
			// it, and a run that cannot say so would report a spend it did
			// not make.
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		a := ServiceAnswer{Tier: tier, Credential: name, Switch: sw, Probed: probed}
		if sw != "" {
			// A switch's cache-miss cost is the gateway's to estimate
			// (F24): it is computed here, where the last call's usage is,
			// and handed to the run with the record the service wrote.
			a.SwitchCost = s.switchCost(tier)
		}
		s.answer(a)
		return resp.StatusCode, false, tier, probed
	}
	head, rerr := io.ReadAll(io.LimitReader(resp.Body, maxServiceRefusalBytes))
	// Whatever this answer turns out to be, the reader above has consumed
	// its first bytes: they go back in front of the rest, so a relayed body
	// arrives whole and the pump streams it as it always did.
	resp.Body = replayBody{Reader: io.MultiReader(bytes.NewReader(head), resp.Body), Closer: resp.Body}
	if rerr != nil {
		// A body that broke mid-read, before any of it reached the agent:
		// not a refusal this code can read, and not one to relay.
		return s.haltService(w, stateDetailUnreadable), true, "", nil
	}
	e, ok, err := readServiceRefusal(resp.StatusCode, head)
	if !ok {
		// Not this contract's refusal. A 401 or 403 that is not the model
		// API's own envelope either is the platform's layer answering: Cloud
		// Run IAM refusing this job's invoker grant, or a proxy in front of
		// the service. The contract's own 403 (unverified_job_identity) was
		// read above, so what is left here is a caller the service never
		// admitted. It is not relayed -- the body is the platform's, and it
		// names the project and the caller -- and it is not a model error the
		// service passed on: every call this run makes will be refused the
		// same way, so the run halts budget_unavailable-style (D143) rather
		// than being answered 401 once per call.
		//
		// A credential's own 401 or 403 is the other thing that arrives with
		// these statuses, and it is the model's answer about the credential,
		// not the platform's about the caller. C1.11 acts on two answers
		// itself -- a usage limit, and a 401 that marks the credential broken
		// (D139) -- and everything else it relays as it found it, so a 403
		// from the credential's upstream is the model's and reaches the agent
		// as it always did. The run is not stopped by it either: another
		// credential, or a person, may be what fixes it.
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && !modelErrorEnvelope(head) {
			return s.haltService(w, stateDetailUnadmitted), true, "", nil
		}
		return resp.StatusCode, false, "", nil // a model error the service relayed
	}
	if err != nil {
		return s.haltService(w, stateDetailUnreadable), true, "", nil
	}
	switch e.Error {
	case ErrorExhaustedUntil:
		ex, err := exhaustedAnswer(e)
		if err != nil {
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		// The wait is not a halt: the runner decides what to do about it
		// (C1.17), and until it does the call is answered retryably and
		// without a retry-after of its own, so the client backs off by its
		// own schedule.
		s.answer(ServiceAnswer{Exhausted: &ex})
		writeError(w, http.StatusServiceUnavailable, "api_error",
			"fugaro: the credentials service has no credential available before "+ex.ResetAt.UTC().Format(time.RFC3339))
		return http.StatusServiceUnavailable, true, "", nil
	case ErrorStateUnavailable:
		return s.haltService(w, refusalDetail(e.Error)), true, "", nil
	case ErrorUnverifiedRunIdentity, ErrorUnverifiedJobIdentity:
		return s.haltService(w, refusalDetail(e.Error)), true, "", nil
	}
	// A refusal whose type this release does not know. It is the service's
	// own answer and is not relayed, and the call cannot be accounted for
	// either: stop rather than improvise (D143's posture). The type is from
	// a closed set the service writes, and is not echoed into the detail.
	return s.haltService(w, stateDetailUnreadable), true, "", nil
}

// replayBody is a response body whose first bytes were read and put back.
type replayBody struct {
	io.Reader
	io.Closer
}

// The details a service halt is recorded with. They are constants: a halt's
// Detail is displayed to the agent in every later call's 403 and stored in
// the run's record, and nothing the service sent is ever echoed into it.
const (
	// stateDetailUnreachable is a service this job could not reach.
	stateDetailUnreachable = "the credentials service could not be reached"
	// stateDetailUnreadable is an answer this gateway could not read as the
	// service's own, or could not account a call by.
	stateDetailUnreadable = "the credentials service answered with something this gateway cannot read"
	// stateDetailUnadmitted is the service's own front door refusing this
	// job: an answer that is neither a model answer nor the contract's typed
	// refusal, on the two statuses the platform's IAM layer uses.
	stateDetailUnadmitted = "the credentials service refused this job's call (check the job's invoker grant on the service)"
	// stateDetailUnidentified is a call this job could not present its
	// identity on: the run's own token, or the Google ID token the metadata
	// server mints. Nothing was sent.
	stateDetailUnidentified = "this job could not present its identity to the credentials service (the metadata server or the run's own identity was not available)"
)

// refusalDetail is the detail for a typed refusal the run cannot be served
// by: the type, which is one of the contract's own constants, and nothing
// else.
func refusalDetail(typ string) string {
	switch typ {
	case ErrorStateUnavailable:
		return "the credentials service cannot read the state it must read (state_unavailable)"
	case ErrorUnverifiedRunIdentity:
		return "the credentials service refused this run's identity (unverified_run_identity)"
	case ErrorUnverifiedJobIdentity:
		return "the credentials service refused this job's identity (unverified_job_identity)"
	}
	return stateDetailUnreadable
}

// haltService records a halt the service's own answer forces, writes the
// refusal the agent's call is answered with -- the first halt's message, which
// is this one's unless a halt came first -- and returns its status. The first
// halt of any kind stands, and a halt the gateway decided itself does not
// cancel the calls in flight, exactly as a run-cap halt does not: each of them
// settles by its own answer.
func (s *Server) haltService(w http.ResponseWriter, detail string) int {
	s.mu.Lock()
	if s.halt == nil {
		h := Halt{Reason: ReasonBudgetUnavailable, Scope: "run", Detail: detail, At: time.Now().UTC()}
		s.haltLocked(h, "fugaro: budget halted: "+haltText(h), s.stageNameLocked(), true)
	}
	msg := s.haltMsg
	s.mu.Unlock()
	writeHalt(w, msg)
	return http.StatusForbidden
}

// serviceUnreachable halts a run whose gateway could not reach the service at
// all (D143), answering the call with the halt, and reports whether this is
// the thing that stopped it: an external halt or a cancelled request came
// first, and then the agent's answer is the one that cause wrote.
func (s *Server) serviceUnreachable(w http.ResponseWriter, base, req context.Context) (halted bool) {
	if req.Err() != nil || base.Err() != nil {
		return false
	}
	s.haltService(w, stateDetailUnreachable)
	return true
}

// answer hands one service answer to the run's hook, if it has one.
func (s *Server) answer(a ServiceAnswer) {
	if s.o.OnServiceAnswer != nil {
		s.o.OnServiceAnswer(a)
	}
}
