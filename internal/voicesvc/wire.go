// Package voicesvc is the voice hop: the Fugaro-hosted service a phone calls
// before its speech reaches a provider (docs/mobile/design/voice-evaluation-
// switch.md §4.4, mobile plan Task P5.3.3). It is deployed separately from
// every job, in the installation root, and the app reaches it with the
// Firebase Auth ID token it already holds — the app adds no credential of its
// own (Global Constraint 4).
//
// Three things are the whole of it, and a test pins each:
//
//   - **The caller is a device, and a live one.** The ID token's signature is
//     checked against Google's own certificates, its issuer and audience
//     against the installation's Firebase project, and its uid against the
//     `m~` shape a paired device is minted with (auth.go). A run's `r~`
//     identity, a pairing session's `p~` and every other uid this project
//     issues are refused by name. The device's liveness is the node at
//     config/admin/devices/$uid, read over IAM (status.go): that node is what
//     revocation deletes, so revoking a device denies its next call
//     immediately (D166, §4.4 point 3).
//
//   - **The project's policy is enforced here and nowhere else.** The app
//     holds no grant on config/policy/** (§2), so a stale cache, a modified
//     client and the device's own setting all reach this refusal rather than
//     deciding for themselves (§4.1, §4.4 point 5). What the hop reads is the
//     node, through internal/projectpolicy's reader — the default deny of
//     P5.3.2 — and the refusal carries its reason rather than a copy of the
//     node (policy.go).
//
//   - **No byte of a body is logged.** The log line is internal/gateway's
//     callLog shape: the act, the identity's own names and the error type,
//     and no audio, no transcript and no text of any kind (§4.4 point 6).
//     Nothing this package answers, refuses or records is a payload.
//
//   - **A call that passes every gate reaches exactly one provider, once.**
//     The engines are this hop's own register (§7), with each one's terms and
//     the directions it is served in (register.go); the policy node names
//     engines and never an address; and every endpoint is a constant of this
//     tree's own text (provider.go). Nothing is retried and nothing falls over to a
//     second provider, and nothing is buffered, spooled or written to disk:
//     the audio is forwarded as it arrives and the answer streams back as it
//     is generated (forward.go, stream.go; §4.5, §8).
//
// The shell (auth.go, status.go, policy.go, server.go) is mobile P5.3.3 and
// the forwarding half (register.go, provider.go, forward.go, stream.go) is
// P5.3.5. What the whole of it holds is one provider key, in the
// installation's Secret Manager: it is read per call, it travels in one header
// of one request, and no answer, log line or test fixture carries it (§4.4
// point 4, voice.tf, secrets.tf).
//
// Two limits are the design's and are stated rather than hidden (§4.4, "two
// honest limits"). **The project is a claim, not an authenticated fact**: the
// hop takes the slug from the request, so the policy check is a guard against
// accident and stale state and not a boundary against the device's owner. And
// **the hop is a second recipient of the speech**, not a privacy win: what it
// buys is the credential and the enforcement, never confidentiality.
package voicesvc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// StatusPath is the app's project-status query: the same authenticated call,
// carrying no audio and no text, that answers what is effective for one
// device and one project (§4.4 point 7). It is this service's own contract
// and not a path any provider spells, so it is declared here rather than
// beside a route that forwards.
const StatusPath = "/v1/voice/status"

const contentTypeJSON = "application/json"

// HeaderAuth is the header the app's Firebase ID token arrives in, and
// authScheme is the only scheme this service reads. A caller that presents
// its token another way is refused rather than guessed at: the scheme is what
// tells an ID token from the basic-credential form a proxy or a browser might
// have put there.
const (
	HeaderAuth = "Authorization"
	authScheme = "Bearer "
)

// The reasons, one per refusal. They are the wire's vocabulary and not
// sentences: the app branches on a reason (§4.2's indicator shows "blocked by
// project policy" and the app says the device is revoked), so the spelling of
// one is a contract and a reason added here is a change to this file.
//
// The first four are the project-policy family and are answered as an ANSWER
// rather than as an HTTP refusal: the project-status query exists to report
// them (§4.4 point 7), and "this project is served locally" is not an error
// in the app's hands. The rest are refusals of the caller or of the state,
// and each carries the status its own handler writes.
const (
	// ReasonForceLocal: the project forbids remote for every device (§4.3).
	ReasonForceLocal = "force_local"
	// ReasonNoAdmission: the node admits nobody — an absent, empty or
	// all-switches-off policy, which are one answer by design (§4.3).
	ReasonNoAdmission = "no_admission"
	// ReasonPolicyUnreadable: the node could not be read or decoded. It is a
	// refusal and never an admission: the four answers of §4.3 are one
	// answer, and this is the one a caller is most tempted to soften.
	ReasonPolicyUnreadable = "policy_unreadable"
	// ReasonNotAdmitted: the named engine is not on this project's list, or
	// the direction asked for is off. It is the per-call refusal the
	// forwarding half answers with (P5.3.5); the project-status answer never
	// carries it, because the query names no engine and no direction.
	ReasonNotAdmitted = "not_admitted"
	// ReasonNotADevice: a verified token whose uid is not a device's — a
	// run's `r~`, a pairing session's `p~`, an account's own.
	ReasonNotADevice = "not_a_device"
	// ReasonDeviceUnknown: a device whose node is gone — never paired, or
	// revoked. It is the refusal D166's answer was taken for: revocation is
	// the node's deletion and this is where it is read.
	ReasonDeviceUnknown = "device_unknown"
	// ReasonTokenUnverified: an ID token this service does not accept.
	ReasonTokenUnverified = "token_unverified"
	// ReasonStateUnavailable: the state a check needs could not be read. It
	// is not an admission and not a refusal of the caller (§4.5: the app
	// falls back to local either way).
	ReasonStateUnavailable = "state_unavailable"
	// ReasonUnknownEngine: a forwarding call named an engine this hop does not
	// hold — no register row, or a row that does not serve the direction asked
	// for. The two are one reason on purpose (register.go): which engines and
	// which directions this service holds is not something a caller enumerates
	// by asking one direction at a time, and the answer to either is the local
	// engine.
	ReasonUnknownEngine = "unknown_engine"
	// ReasonProviderFailed: the call reached no provider, or the provider
	// answered with a failure. Its answer is never relayed and never read — it
	// may echo the key this service presented (§4.4 point 4) — and the app's
	// answer to it is the local engine, exactly as for a refusal (§4.5).
	ReasonProviderFailed = "provider_failed"
	// ReasonBadRequest: a request this contract cannot read.
	ReasonBadRequest = "bad_request"
	// ReasonNotFound: a path this service does not serve.
	ReasonNotFound = "not_found"
)

// The bounds. Each is a limit on caller-chosen text that reaches a log line,
// a refusal body or a database path, and each is stated rather than left to
// the decoder's own limits: a body this service will not read is a request it
// refuses by name.
const (
	// maxRequestBytes bounds the whole body. The status query is a slug and
	// nothing else, so anything larger is not a body this contract wrote —
	// and the bound is what keeps a caller from spending this service's
	// memory with a payload the answer never uses. It is the same order as
	// internal/gateway's own request bound and is deliberately small: the
	// forwarding half adds its own bound per direction (P5.3.5), and it must
	// not inherit one this wide.
	maxRequestBytes = 4096
	// maxRefusalMessageBytes bounds the sentence a refusal may carry. It is
	// free text on the wire and in a log line, so it is bounded here and
	// clipped rather than refused — the reason is what the app acts on.
	maxRefusalMessageBytes = 512
)

// projectRE is the shape of the project a request names: the slug
// internal/task.Slug produces, which is also the key the policy node is
// written under (projectpolicy.PathVoiceRepo).
//
// It is spelled again here rather than read from internal/task, because what
// this service holds is a string a phone sent and internal/task's function
// builds a slug from a provider and a repository — there is no exported
// predicate to ask. What holds the two to one answer is a test and not this
// comment (TestProjectShapeIsTaskSlugsShape): every candidate goes through
// both, and the two must admit and refuse the same strings.
//
// The rule is deliberately the narrow one. A slug this service admitted and
// the publisher never wrote would be a project whose node is absent, which
// §4.3 answers local — so a wide rule here would not open anything, and the
// narrow one is what keeps a caller's text out of a database path.
var projectRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,45}-[0-9a-f]{16}$`)

// StatusRequest is the app's project-status query: the one project it is
// asking about, and nothing else. There is no device field, because the
// device is the token and never a claim — a caller that could name another
// device would be a caller that could read another phone's answer.
type StatusRequest struct {
	// Project is the slug of the project the app is speaking about. It is a
	// claim (§4.4's first limit): the hop checks it against the policy node
	// and against nothing else, because the only fact it can authenticate is
	// the device, from the token.
	Project string `json:"project"`
}

// StatusAnswer is what the hop answers about one project for one device: what
// is effective, and the reason when nothing is (§4.4 point 7). It is the
// app's only source for "blocked by project policy", and it is an ANSWER and
// not a copy of the node — the app caches it, a stale copy is harmless
// because the hop refuses on the call itself, and no member here is a policy
// field the app may read a rule out of.
//
// The two directions are separate members for the reason the node's are
// (projectpolicy.VoiceDirection): an edit that widens text must not silently
// widen audio.
type StatusAnswer struct {
	// Project echoes the slug the answer is about, so a cached answer cannot
	// be read against another project.
	Project string `json:"project"`
	// Remote is whether ANY engine is admitted for this project on this
	// device. False is the app's local answer, whether the reason is the
	// policy, a project that admits nobody, or a node this service could not
	// read.
	Remote bool `json:"remote"`
	// Providers are the engine names this project admits, in the node's own
	// order and empty when Remote is false. They are names and never
	// addresses: a policy-carried URL is refused at decode
	// (projectpolicy.validVoiceProviders), so nothing here can carry a
	// destination.
	Providers []string `json:"providers,omitempty"`
	// Direction is which directions are admitted, each defaulting false.
	Direction StatusDirection `json:"direction"`
	// Reason is why remote is not admitted, and is empty when it is. It is
	// one of the project-policy family above and never a sentence.
	Reason string `json:"reason,omitempty"`
	// Detail is a sentence for a person — the app may show it, and it names
	// the field a node got wrong rather than any content of it. It is the
	// decode refusal's own text and is bounded and one printable line
	// (safeDetail).
	Detail string `json:"detail,omitempty"`
}

// StatusDirection is the answer's two switches, each defaulting false.
type StatusDirection struct {
	STT bool `json:"stt"`
	TTS bool `json:"tts"`
}

// WireError is the body of every refusal that is not a project-policy answer:
// a caller this service does not admit, a request it cannot read, and state
// it cannot reach.
//
// Its type field is Error because the wire's key is "error", the shape
// internal/credsvc's WireError has. It is a struct and not an error: a value
// cannot carry a field and a method of one name, and the wire's key is what a
// reader of the format depends on. It carries no value the caller presented:
// the message names what is wrong with the identity and never the token, and
// a device uid is the only caller text it may echo — a uid is a key the
// operator prints (`fugaro pair ls`) and not a credential.
type WireError struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// RefusalError is a refusal travelling as an error: the wire reason the answer
// carries, and the sentence a person reads. Every layer below the handler
// (auth.go, policy.go, status.go) returns one, so the handler writes the
// status the reason names without a table of its own and without reading a
// message to decide anything.
//
// It exists because the alternative is what this package must not do: a
// handler that mapped an error's TEXT onto a reason would be a handler whose
// behaviour changes when a sentence is edited, and a sentence is free text
// that may carry another writer's words (internal/projectpolicy's refusal).
type RefusalError struct {
	// Reason is one of the reason constants above.
	Reason string
	// Err is the underlying error: for ErrUnverified, ErrDeviceUnknown and
	// the rest, so a caller can still ask errors.Is.
	Err error
}

// Error is the sentence, prefixed with the reason so a log line that prints
// only the error still says which refusal it was.
func (e *RefusalError) Error() string {
	if e.Err == nil {
		return e.Reason
	}
	return e.Reason + ": " + e.Err.Error()
}

// Unwrap is what keeps errors.Is working through a refusal, so
// errors.Is(err, ErrUnverified) is the same question whether the caller reads
// the verifier's return or a handler's.
func (e *RefusalError) Unwrap() error { return e.Err }

// refuse attaches a wire reason to a refusal. A nil error is a programming
// error and is refused rather than wrapped: a refusal with nothing wrong to
// report is a bug in the caller, not a reason to answer a caller of this
// service.
func refuse(reason string, err error) error {
	if err == nil {
		err = fmt.Errorf("%s", reason)
	}
	return &RefusalError{Reason: reason, Err: err}
}

// ReasonOf reads the wire reason off an error, and ReasonStateUnavailable for
// one that carries none.
//
// The default is the point of the function. Every refusal this package builds
// goes through refuse, so a bare error reaching a handler is one this package
// did not anticipate — and the answer for "this service does not know what
// went wrong" is the one the app treats as a reason to fall back to local
// (§4.5), never an admission and never a claim about the caller's token.
func ReasonOf(err error) string {
	var r *RefusalError
	if errors.As(err, &r) && r.Reason != "" {
		return r.Reason
	}
	return ReasonStateUnavailable
}

// WriteAnswer writes a project-status answer. It is 200 even when the answer
// is "local only", because a project served locally is the answer and not a
// failure of the request (§4.4 point 7, §4.5's first rows).
func WriteAnswer(w http.ResponseWriter, a StatusAnswer) {
	body, err := json.Marshal(a)
	if err != nil {
		// A StatusAnswer is strings, booleans and a string slice, so this is
		// unreachable; answering the typed refusal beats answering nothing.
		WriteWireError(w, http.StatusInternalServerError, WireError{
			Error:   ReasonStateUnavailable,
			Message: "the answer could not be written",
		})
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// WriteWireError writes a refusal: its reason in the body, its status on the
// response. The message is clipped to maxRefusalMessageBytes and made one
// printable line, because it reaches a log file and a terminal and it may
// carry another writer's text (internal/projectpolicy's own refusal).
func WriteWireError(w http.ResponseWriter, status int, e WireError) {
	e.Message = safeDetail(e.Message)
	body, err := json.Marshal(e)
	if err != nil {
		body = []byte(`{"error":"` + ReasonStateUnavailable + `"}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// safeDetail bounds a sentence and makes it one printable line: it is the
// only free text this service puts on the wire, and a newline or an escape
// sequence in it would forge a second line of a log or repaint a terminal.
// internal/safetext is this repository's rule for text that reaches one.
func safeDetail(s string) string {
	return safetext.OneLine(safetext.Clip(s, maxRefusalMessageBytes))
}

// ReadStatusRequest reads the query's body: the one member this contract
// carries, decoded strictly (a member this release does not know is refused
// rather than ignored, so a newer app's field is a version skew this service
// says out loud instead of reading the request as one it understands), and
// bounded before it is read at all.
func ReadStatusRequest(r *http.Request) (StatusRequest, error) {
	var req StatusRequest
	body, err := readBody(r, maxRequestBytes)
	if err != nil {
		return StatusRequest{}, err
	}
	if len(body) == 0 {
		return StatusRequest{}, fmt.Errorf("the request carries no body: the query is a JSON object naming the project")
	}
	if err := onlyMembers(body, "project"); err != nil {
		return StatusRequest{}, err
	}
	if err := decodeStrict(body, &req); err != nil {
		return StatusRequest{}, err
	}
	if !projectRE.MatchString(req.Project) {
		// The value is deliberately not repeated: it is a caller's string,
		// and a refusal that echoed it would be a line the caller writes into
		// this service's log. The rule is named instead, and it is the one
		// internal/task.Slug produces.
		return StatusRequest{}, fmt.Errorf("the request names no project: a project is a slug — the lowercase repository name, a dash and its sixteen hex digits — which is the key the policy node is written under")
	}
	return req, nil
}

// onlyMembers refuses a document carrying a member name that is not one of
// want, spelled exactly.
//
// The exactness is the point and it is not what encoding/json does: the
// decoder folds a member's name to the struct field's case-insensitively, so
// `{"Project": …}` decodes into a struct whose field is tagged `project`.
// internal/projectpolicy checks its own members for the same reason
// (checkMemberNames): a wire format this tree reads has one spelling per
// member, and a caller that wrote another one wrote something no writer of
// this contract produces.
func onlyMembers(body []byte, want ...string) error {
	var written map[string]json.RawMessage
	if err := json.Unmarshal(body, &written); err != nil {
		return fmt.Errorf("the request body is not this contract's: %w", err)
	}
	for name := range written {
		if !slices.Contains(want, name) {
			// The name is not repeated: it is a caller's string, bounded only
			// by the body's own bound.
			return fmt.Errorf("the request carries a member this contract does not have: the query is a JSON object naming the project, and nothing else")
		}
	}
	return nil
}

// decodeStrict decodes one JSON document, refusing an unknown member and
// trailing content. Every wire format this repository reads is strict
// (Convention 6), and this one is read by a service that decides whether a
// microphone opens.
func decodeStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the request body is not this contract's: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("the request body carries more than one document")
	}
	return nil
}

// readBody reads at most max bytes of a request body and refuses one that is
// larger rather than truncating it: half a document is not a request, and a
// decoder that read one would be reading a body this contract did not write.
func readBody(r *http.Request, max int) ([]byte, error) {
	if r.Body == nil {
		return nil, fmt.Errorf("the request carries no body")
	}
	body := make([]byte, 0, 512)
	buf := make([]byte, 512)
	for {
		n, err := r.Body.Read(buf)
		body = append(body, buf[:n]...)
		if len(body) > max {
			return nil, fmt.Errorf("the request body is over this service's bound of %d bytes", max)
		}
		if err != nil {
			break
		}
	}
	return body, nil
}

// BearerToken reads the ID token out of an Authorization header, and reports
// whether one was there. It is exact about the scheme for HeaderAuth's reason.
func BearerToken(header string) (string, bool) {
	tok, ok := strings.CutPrefix(header, authScheme)
	if !ok || tok == "" {
		return "", false
	}
	return tok, true
}
