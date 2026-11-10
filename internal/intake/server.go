// Package intake is the secret intake: the one process that writes the
// per-user and project-shared credentials a launch reads, and the only way a
// person's own Google account puts a credential where the credentials service
// can serve it (docs/secrets-brokering/dynamic-delivery, DS2.1, R3).
//
// What it is, in one line: a small HTTP service a launcher calls with their
// own Google ID token, which writes, lists and deletes Secret Manager
// containers under the two prefixes of internal/secretname and never reads a
// value.
//
//	POST /v1/secrets/create   the credential's name and value: make the container, add a version
//	POST /v1/secrets/version  the same, into a container that must already exist (a rotation)
//	POST /v1/secrets/list     names and dates, under the caller's own prefix
//	POST /v1/secrets/delete   the caller's own container, or — an admin's — a member's whole prefix
//
// The four properties that are the point of it, and each is a refusal rather
// than a feature:
//
//   - THE PREFIX COMES FROM THE TOKEN (R3). A per-user container is
//     fugaro_u_<sub>_<name>, and <sub> is the Google account id of the ID
//     token Cloud Run IAM admitted and this package verified (credsvc's
//     launcher class, DS1.2). No field of any request names an owner, and the
//     wire types have none: a caller cannot ask for another person's prefix,
//     and a body that tries is refused by the decoder rather than ignored.
//   - IT NEVER READS A VALUE (G2). The SecretStore interface below is the
//     intake's whole reach in Secret Manager: describe, create, add a version,
//     list, delete. There is no method that answers a version's payload, and
//     no route that would. A list answers names and dates, and an audit line
//     names the act and never the credential.
//   - THE SHARED PREFIX IS AN ADMIN'S (D1935, D1940). fugaro_p_<name> is
//     written only by an address on the derived admin list `fugaro update`
//     publishes (internal/access's PathAdminList), which the intake reads as a
//     bucket object and nothing else: it holds no database role, and the list
//     lags a role change until the next update.
//   - IT WRITES ONLY CONTAINERS IT MADE (R12). An existing container is
//     written into only when its shape is exactly the intake's own — no TTL,
//     no expiry, no Pub/Sub topic, no CMEK key, no rotation schedule, and
//     exactly the annotations this package writes. A container the managed
//     deployer pre-created (F35) is therefore refused, not trusted, and its
//     annotations are never evidence of who owns it: the NAME is.
//
// The owner of a container — the fact offboarding and pool resolution use — is
// the <sub> in its name and the address recorded beside it as an annotation
// (R10): fugaro_owner_sub, fugaro_owner_email and fugaro_created_by. Labels
// cannot hold an address, which is why they are annotations, and the
// annotations are a lookup aid and never an authorisation (R12).
//
// Every call writes exactly one audit line (G6, G13), and a line that cannot
// be written turns the answer into state_unavailable: an act no operator can
// read afterwards is not one this service reports as done. No line carries a
// credential: the line's fields are the act, the caller, the container's name
// and the status, and the value the call carried is scrubbed out of every
// field before anything else looks at them.
package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// The routes. Four, and every one of them is a POST with a JSON body: the
// intake has no verb that reads a value, so there is nothing a GET would be
// for, and a reader of this list is meant to see that (TestNoReadMethodExists).
const (
	// CreatePath ensures the container — creating it with this package's
	// annotations when it does not exist — adds a version, and is idempotent:
	// a second call with the same name rotates it. It is the one route whose
	// caller cannot know whether the container exists, which is why the
	// decision is made here and not by the client.
	CreatePath = "/v1/secrets/create"
	// VersionPath adds a version to a container that must already exist: the
	// route a rotation takes when a name typed wrong must not become a new
	// credential. The container is shape-checked exactly as CreatePath's is.
	VersionPath = "/v1/secrets/version"
	// ListPath answers the containers under the caller's prefix: their names
	// and the dates they were made, and no version's value.
	ListPath = "/v1/secrets/list"
	// DeletePath removes a container — the caller's own by name, or, for an
	// admin naming a member by address, every container whose owner
	// annotation is that member's. That second form is offboarding (DS2.4).
	DeletePath = "/v1/secrets/delete"
)

// Routes is the intake's whole surface, in the order this file declares it.
// It is a function and not the constants alone so that a test can hold the
// served set to the declared one (TestNoReadMethodExists).
func Routes() []string { return []string{CreatePath, VersionPath, ListPath, DeletePath} }

// The two scopes: which prefix a call is about. A scope is a closed set,
// because it selects the prefix and the authorisation and a scope this release
// does not know is not one it may guess at.
type Scope string

const (
	// ScopeUser is the caller's own prefix, fugaro_u_<sub>_<name> (R1).
	ScopeUser Scope = "user"
	// ScopeShared is the project's, fugaro_p_<name>: an admin's alone.
	ScopeShared Scope = "shared"
)

// The annotations every container the intake writes carries (R10, R12).
//
// They are annotations and not labels because a label value cannot hold an
// address (F30), and they are the intake's to write and no one else's: a
// container whose annotations are not exactly these is refused rather than
// written into or deleted (R12). Their spelling is exported because the
// credentials service resolves a pool by them (DS3.3) and the offboarding
// sweep matches on one (DS2.4), and two spellings of a key are two keys.
const (
	// AnnotationOwnerSub is the account id in the container's own name: the
	// fact that makes an annotation a lookup aid rather than a claim. The
	// name is the truth and this is a copy of it.
	AnnotationOwnerSub = "fugaro_owner_sub"
	// AnnotationOwnerEmail is the verified address of the account that owns a
	// per-user container: what offboarding names a member by, since a person
	// is known by an address and the prefix is keyed by an account id.
	AnnotationOwnerEmail = "fugaro_owner_email"
	// AnnotationCreatedBy is the verified address that created the container.
	// For a per-user container it is the owner's own (only they write under
	// their prefix); for a shared one it is the admin who made it first.
	AnnotationCreatedBy = "fugaro_created_by"
)

// The audit line's acts: what the call asked for. A closed set, for the reason
// the scopes are one.
const (
	OpCreate  = "create"
	OpVersion = "version"
	OpList    = "list"
	OpDelete  = "delete"
)

// The refusal types of the answer's body. They are this contract's own
// vocabulary, in the shape the credentials service's is (credsvc's WireError
// is the body, so the CLI reads both answers with one reader).
const (
	// ErrorUnverifiedLauncher is a caller the launcher class does not admit:
	// not on the roster, a token for another audience, or an identity Google
	// did not sign. Also the answer to a call with no token at all.
	ErrorUnverifiedLauncher = "unverified_launcher"
	// ErrorNotAdmin is a caller that is not on the derived admin list asking
	// for the project-shared prefix (D1935, D1940).
	ErrorNotAdmin = "not_admin"
	// ErrorRequestRefused is the request itself: a body this release cannot
	// read, a name outside R1's grammar, a scope or an act it does not have.
	ErrorRequestRefused = "request_refused"
	// ErrorNoSuchSecret is a rotation or a delete of a container that is not
	// there, and a sweep that matched nothing.
	ErrorNoSuchSecret = "no_such_secret"
	// ErrorForeignContainer is an existing container whose shape is not the
	// intake's own (R12): another writer's, and refused rather than written
	// into or deleted.
	ErrorForeignContainer = "foreign_container"
	// ErrorStateUnavailable is the intake unable to do what it must to answer:
	// Secret Manager, the admin list, the audit log, or the caller's key set.
	// Never a fallback (D143's fail-closed half).
	ErrorStateUnavailable = "state_unavailable"
)

// The wire shapes. They are strict both ways (Convention 6): a body carrying a
// field this release does not know is refused rather than read, which is what
// keeps a request from naming an owner the release does not have a field for.

// CreateRequest is what CreatePath and VersionPath carry: which prefix, the
// credential's name, and the credential itself.
//
// Value is base64, as Secret Manager's own payloads are, so a credential is
// bytes on the wire and not text this contract would have to escape. The
// answer never carries it back, and no field of any request names the owner:
// the account id is the token's (R3).
type CreateRequest struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SecretAnswer is the answer to a create, a version or a delete: which
// container was written, which version it became, and whether the container
// was made by this call. Never a value, and never a version's payload.
type SecretAnswer struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Created bool   `json:"created,omitempty"`
}

// ListRequest asks for containers: the caller's own prefix, or — an admin
// alone — a member's, by address. Naming an owner is what offboarding looks a
// member up by (R10), so it is an admin's call and not a caller's own.
type ListRequest struct {
	Scope Scope  `json:"scope"`
	Owner string `json:"owner,omitempty"`
}

// ListAnswer is what a list answers: names and dates, and nothing about a
// version at all.
type ListAnswer struct {
	Secrets []ListedSecret `json:"secrets"`
}

// ListedSecret is one container as a list shows it: the name a caller asks for
// it by, the scope it lives in, and when it was made.
type ListedSecret struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Scope   Scope     `json:"scope"`
	Created time.Time `json:"created"`
}

// DeleteRequest removes exactly one thing: a container by name, or — an admin
// alone, for offboarding — every container a member owns, named by address.
type DeleteRequest struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name,omitempty"`
	Owner string `json:"owner,omitempty"`
}

// DeleteAnswer names what was removed, so a sweep says what it did rather than
// only that it succeeded.
type DeleteAnswer struct {
	Deleted []string `json:"deleted"`
}

// Secret is one container as the intake reads it: metadata only, and there is
// no field here a version's payload could be read into.
type Secret struct {
	// ID is the container's secret ID, which is the name grammar's output.
	ID string
	// Created is when the container was made.
	Created time.Time
	// Annotations are the container's annotations, whole: the intake holds
	// them to its own set before it writes (R12).
	Annotations map[string]string
	// Shape is what the container carries beyond what the intake writes.
	Shape Shape
}

// Shape is everything an existing container may carry that the intake never
// writes, and the whole of the refusal R12 makes. Each field is a fact about
// the container that would take the credential away from the person who wrote
// it: a TTL or an expiry would delete it, a topic would publish it, a CMEK
// would put it under a key the intake does not hold, and a rotation schedule
// would replace it on someone else's clock. An empty Shape is the shape the
// intake makes, and a container carrying any of them was made by another
// writer (F35: the managed deployer holds secrets.create).
type Shape struct {
	// TTL is the container's time to live, in the API's duration text.
	TTL string
	// ExpireTime is the container's expiry, in the API's timestamp text.
	ExpireTime string
	// Topics are the Pub/Sub topics a new version is published to.
	Topics []string
	// CMEK is the customer-managed encryption key the container is bound to.
	CMEK string
	// Rotation is the container's rotation schedule.
	Rotation string
}

// Empty reports whether the container carries nothing but what the intake
// writes.
func (s Shape) Empty() bool {
	return s.TTL == "" && s.ExpireTime == "" && len(s.Topics) == 0 && s.CMEK == "" && s.Rotation == ""
}

// String names the facts the shape carries, in the words the refusal uses. It
// names them and never their values' content beyond the fact itself: a topic
// or a key is a resource name the deployment published, not a credential.
func (s Shape) String() string {
	var parts []string
	if s.TTL != "" {
		parts = append(parts, "a TTL")
	}
	if s.ExpireTime != "" {
		parts = append(parts, "an expiry")
	}
	if len(s.Topics) > 0 {
		parts = append(parts, "a Pub/Sub topic")
	}
	if s.CMEK != "" {
		parts = append(parts, "a customer-managed key")
	}
	if s.Rotation != "" {
		parts = append(parts, "a rotation schedule")
	}
	return strings.Join(parts, ", ")
}

// ErrExists is a container that is already there, answered by Create. The
// caller of it is the write path, which reads the container again and puts it
// to the shape check rather than trusting that it was ours: the two calls are
// not atomic, and the second read is what closes the window (R12).
var ErrExists = errors.New("the container already exists")

// SecretStore is the intake's whole reach in Secret Manager, and it is
// deliberately short: a container can be described, created, given a version,
// listed and deleted, and there is no call that answers a version's payload.
// The intake's service account holds no secretmanager.versions.access at all
// (DS2.2), so this interface is the code's half of a bound the deployment also
// makes — and the code's half is the one a test can hold (TestNoReadMethodExists).
//
// A write to an existing container is refused by the caller of this interface
// and not by the store: the shape check is R12's rule and it lives where the
// name and the caller's identity are, so the store sees only two calls, and
// the second of them is refused before it is made.
type SecretStore interface {
	// Describe answers one container's metadata, and whether it is there.
	Describe(ctx context.Context, id string) (Secret, bool, error)
	// Create makes the container with annotations, or answers ErrExists.
	Create(ctx context.Context, id string, annotations map[string]string) error
	// AddVersion stores value as a new version of id and answers the version
	// ID. The value is bytes: nothing here reads it back.
	AddVersion(ctx context.Context, id string, value []byte) (string, error)
	// List answers the metadata of every container whose ID begins with
	// prefix. Metadata only, and never a version's payload.
	List(ctx context.Context, prefix string) ([]Secret, error)
	// Delete removes the container and every version in it. There is no
	// undelete.
	Delete(ctx context.Context, id string) error
}

// Caller is the launcher class (DS1.2), as the intake consumes it: the
// verification of a caller's Google ID token, over Google's own key set, with
// no run identity and no reader of the installation's state. The
// implementation is credsvc's Launchers, which the service's own Authenticator
// shares its check with.
type Caller interface {
	// VerifyLauncher answers the verified account and the account id the
	// token names, or an error naming no caller text.
	VerifyLauncher(ctx context.Context, tok string) (credsvc.LauncherCaller, error)
}

// AdminList is DS2.0's derived list: the roster's project-scoped admins, as
// the bucket object `fugaro update` publishes. The intake reads it and holds
// no database role at all (D1940), which is the whole reason it is an object.
type AdminList interface {
	// Admins answers the list as it is now. A read that fails is a refusal:
	// a caller whose authority cannot be established is not an admin.
	Admins(ctx context.Context) ([]access.Admin, error)
}

// Config is the intake's whole configuration. Everything it needs is required:
// an intake that cannot verify a caller, cannot read the admin list or cannot
// audit a call is one that must not start, because the alternative is a
// service that writes credentials it cannot account for.
type Config struct {
	// Callers is the launcher class: the roster of people who may write their
	// own credentials, over Google's ID-token key set.
	Callers Caller
	// Admins is the derived admin list: who may write the project-shared
	// prefix and sweep a member's containers.
	Admins AdminList
	// Secrets is Secret Manager, through the five calls above.
	Secrets SecretStore
	// Audit is the one line per call.
	Audit *Auditor
}

// Server is the intake.
type Server struct {
	cfg Config
}

// New validates the configuration and returns the intake.
func New(cfg Config) (*Server, error) {
	switch {
	case cfg.Callers == nil:
		return nil, errors.New("intake: no caller class: any caller IAM admits would be admitted here too")
	case cfg.Admins == nil:
		return nil, errors.New("intake: no admin list: the project-shared prefix could not be checked")
	case cfg.Secrets == nil:
		return nil, errors.New("intake: no secret store: the intake would write nowhere")
	case cfg.Audit == nil:
		return nil, errors.New("intake: no auditor: a call this intake could not account for must not be served")
	}
	return &Server{cfg: cfg}, nil
}

// ServeHTTP answers one call. A path this intake does not serve names no act,
// so it is answered and not audited; everything the four routes answer is
// audited exactly once, by call.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case CreatePath, VersionPath, ListPath, DeletePath:
	default:
		credsvc.WriteWireError(w, http.StatusNotFound, credsvc.WireError{
			Error:   ErrorRequestRefused,
			Message: "the intake serves four routes and this is not one of them",
		})
		return
	}
	if r.Method != http.MethodPost {
		// A route with another verb is not an act: nothing was asked for and
		// nothing is audited.
		w.Header().Set("Allow", http.MethodPost)
		credsvc.WriteWireError(w, http.StatusMethodNotAllowed, credsvc.WireError{
			Error:   ErrorRequestRefused,
			Message: "the intake's routes are POSTs: each carries the call's body, and none of them reads anything",
		})
		return
	}
	switch r.URL.Path {
	case CreatePath:
		s.set(w, r, OpCreate)
	case VersionPath:
		s.set(w, r, OpVersion)
	case ListPath:
		s.list(w, r)
	case DeletePath:
		s.remove(w, r)
	}
}

// The bounds of a call, in the credentials service's shape and for its reason:
// a header and a body are another writer's text, and a service that reads all
// of what it is handed is one a caller can spend.
const (
	// maxBodyBytes bounds a request's body. Secret Manager's own payload limit
	// is 64 KiB, so a credential and the JSON around it fit inside the bound
	// with room for the name.
	maxBodyBytes = 72 << 10
	// maxValueBytes bounds the credential itself, at Secret Manager's own
	// limit.
	maxValueBytes = 64 << 10
	// maxTokenBytes bounds a presented token before it is parsed: the same
	// 8 KiB the credentials service bounds the same Authorization header's
	// token by (credsvc's maxTokenBytes, wire.go's bearerFromHTTP). A Google
	// ID token is a JWT with a certificate-signed 2048-bit signature and is
	// routinely past a kilobyte, so a tighter bound would refuse every real
	// caller.
	maxTokenBytes = 8 << 10
)

// call is one request to one of the four routes. It carries the audit event
// being built and the value the call brought, so that the one line a call
// writes is armed with the value before anything else looks at the event: a
// handler cannot write a line without saying which credential it held.
type call struct {
	s     *Server
	w     http.ResponseWriter
	ev    AuditEvent
	value []byte
}

// caller verifies the call's Google ID token and answers the verified caller,
// or refuses — its audit line included — and reports false.
func (c *call) caller(r *http.Request) (credsvc.LauncherCaller, bool) {
	tok, err := bearer(r)
	if err != nil {
		// A call with no token is a caller the class does not admit, and it is
		// answered as that rather than as a malformed request: there is
		// nothing the caller could fix about its body, and the CLI's own
		// reading of this type is "you are not signed in".
		c.refuse(credsvc.StatusUnverified, ErrorUnverifiedLauncher,
			"the call presents no Google ID token in its Authorization header: the intake verifies the caller on every route")
		return credsvc.LauncherCaller{}, false
	}
	caller, err := c.s.cfg.Callers.VerifyLauncher(r.Context(), tok)
	if err != nil {
		if errors.Is(err, credsvc.ErrUnverifiedJob) {
			c.refuse(credsvc.StatusUnverified, ErrorUnverifiedLauncher,
				"the caller is not one of this installation's launchers")
			return credsvc.LauncherCaller{}, false
		}
		c.refuse(credsvc.StatusUnavailable, ErrorStateUnavailable,
			"the intake could not check the caller's Google ID token")
		return credsvc.LauncherCaller{}, false
	}
	c.ev.Caller, c.ev.Sub = caller.Account, caller.Sub
	return caller, true
}

// body decodes the call's body strictly, or refuses and reports false. The
// refusal never repeats what the body held: a body is the caller's text, and a
// credential may be in it.
func (c *call) body(r *http.Request, out any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		c.refuse(credsvc.StatusRequestRefused, ErrorRequestRefused, "the request's body could not be read")
		return false
	}
	if len(raw) > maxBodyBytes {
		c.refuse(credsvc.StatusRequestRefused, ErrorRequestRefused, "the request's body is over the intake's bound")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		c.refuse(credsvc.StatusRequestRefused, ErrorRequestRefused,
			"the request's body is not one request object: it carries a field this release does not know, or a value of another type")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		c.refuse(credsvc.StatusRequestRefused, ErrorRequestRefused, "the request's body carries more than one JSON value")
		return false
	}
	return true
}

// fail maps a handler's error to the answer's refusal, or reports
// state_unavailable for one it does not know: a failure this intake did not
// plan for is one it cannot account for, and it answers as that.
func (c *call) fail(err error) {
	switch {
	case errors.Is(err, errNotAdmin):
		c.refuse(credsvc.StatusUnverified, ErrorNotAdmin,
			"the project-shared prefix is written by a project admin, and the derived admin list does not name this caller")
	case errors.Is(err, errNoSuchSecret):
		c.refuse(http.StatusNotFound, ErrorNoSuchSecret,
			"the intake has no such container under this caller's prefix")
	case errors.Is(err, errForeignContainer):
		// The sentence is the shape check's own and names the fact that makes
		// the container another writer's, never a value.
		c.refuse(http.StatusConflict, ErrorForeignContainer, err.Error())
	case errors.Is(err, errRequestRefused):
		// R1's grammar and this file's own rules answer with sentences that
		// echo the caller's bytes through pluginwire.Printable and never raw
		// (secretname's rule), so the message is safe to hand back.
		c.refuse(credsvc.StatusRequestRefused, ErrorRequestRefused, err.Error())
	default:
		c.refuse(credsvc.StatusUnavailable, ErrorStateUnavailable,
			"the intake could not read the state it must read to answer this call")
	}
}

// refuse writes the call's one audit line and then the refusal.
func (c *call) refuse(status int, typ, message string) {
	if !c.record(typ) {
		return
	}
	credsvc.WriteWireError(c.w, status, credsvc.WireError{Error: typ, Message: message})
}

// answer writes the call's one audit line and then the body.
func (c *call) answer(status int, body any) {
	if !c.record(credsvc.StatusOK) {
		return
	}
	raw, err := json.Marshal(body)
	if err != nil {
		// The bodies here are strings and a time, so this is unreachable; it
		// is answered as state_unavailable rather than as a shape a reader
		// would refuse.
		credsvc.WriteWireError(c.w, credsvc.StatusUnavailable, credsvc.WireError{Error: ErrorStateUnavailable})
		return
	}
	c.w.Header().Set("Content-Type", "application/json")
	// A credential's name is in every answer here, so no proxy and no browser
	// may keep one (the credentials service's own rule for its answers).
	c.w.Header().Set("Cache-Control", "no-store")
	c.w.WriteHeader(status)
	_, _ = c.w.Write(raw)
}

// record writes the call's one audit line and reports whether it was written.
// A line that cannot be written turns the answer into state_unavailable: the
// audit line is how an operator reads what this intake did, so a call that
// cannot be recorded is not one the caller is told succeeded (D143's
// fail-closed half).
func (c *call) record(status string) bool {
	c.ev.Status = status
	if err := c.s.cfg.Audit.withValue(c.value).Write(context.Background(), c.ev); err != nil {
		credsvc.WriteWireError(c.w, credsvc.StatusUnavailable, credsvc.WireError{
			Error:   ErrorStateUnavailable,
			Message: "the call could not be written to the intake's audit log, so it is not reported as done",
		})
		return false
	}
	return true
}

// bearer reads the call's bearer token: exactly one Authorization header, the
// Bearer scheme, and a token inside the header's bound. It is the credentials
// service's own rule (wire.go's bearerFromHTTP and oneHeader) and it is
// spelled here because the two processes read the same header of the same
// kind of call and neither imports the other's plumbing.
func bearer(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", errors.New("the call does not present exactly one Authorization header")
	}
	scheme, tok, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return "", errors.New("the call does not present a bearer token")
	}
	if len(tok) > maxTokenBytes {
		return "", errors.New("the presented token is over the header's bound")
	}
	return tok, nil
}

// AuditEvent is one intake call: exactly the fields the line carries, and no
// other. Every one is an act, a name, an address or a status — never a
// credential's value, and there is no field a value could be written into.
type AuditEvent struct {
	// Caller is the verified address, and Sub the account id, of the launcher
	// that called. Both are empty for a call refused before the class admitted
	// it, which is the one case a line names no caller.
	Caller string
	Sub    string
	// Op is what the call asked for (OpCreate, OpVersion, OpList, OpDelete).
	Op string
	// Scope is which prefix it was about, and empty when it could not be read.
	Scope string
	// ID is the container the call was about, when one was decided.
	ID string
	// Owner is the member an admin's lookup named, for the sweep and the
	// admin's list only.
	Owner string
	// Status is StatusOK for a call that was answered and the refusal's type
	// for one that was not.
	Status string
}

// The line's message: a reader greps it for the intake's acts and reads the
// rest of the line as JSON.
const auditMessage = "intake: audit"

// Auditor writes the intake's audit lines: one per call, and never a value.
//
// It is built on a handler and not on a logger for the credentials service's
// reason (credsvc.NewAuditor): a logger is a handler plus whatever With and
// WithGroup put on it, and a line is meant to be this contract's fields and
// nothing else.
type Auditor struct {
	h slog.Handler
	// scrub removes the call's own credential from every field before any
	// rule looks at it. It is armed per call (withValue) and never left unset:
	// Write refuses an auditor that was not told what value the call held, so
	// a handler that forgot is a refusal rather than a line a value reached.
	scrub func(string) string
}

// AuditOption configures an Auditor.
type AuditOption func(*Auditor)

// WithAuditScrub arms the auditor with a redaction applied to every field of
// every event before anything else looks at it. It exists for the unit tests
// and for a caller that holds a value this package does not: the call paths
// arm the line through withValue, and a value the intake never sees is one no
// rule here can recognise.
func WithAuditScrub(f func(string) string) AuditOption {
	return func(a *Auditor) {
		if f != nil {
			a.scrub = f
		}
	}
}

// NewAuditor returns the auditor writing to h. The handler is required, and a
// handler that does not record the level the lines are written at is refused
// rather than accepted: an act this intake could not audit must not be made
// (D143), and a logger whose level excludes the line would drop every one of
// them silently.
func NewAuditor(h slog.Handler, opts ...AuditOption) (*Auditor, error) {
	if h == nil {
		return nil, errors.New("intake: no handler: a call this intake could not audit must not be served")
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		return nil, errors.New("intake: the log handler does not record at the level the intake's audit lines are written at, so every one of them would be dropped")
	}
	a := &Auditor{h: h}
	for _, o := range opts {
		o(a)
	}
	return a, nil
}

// noScrub is the redaction of a call that carried no value: it removes nothing
// and it is not nil, because a nil scrub is the state Write refuses.
func noScrub(s string) string { return s }

// withValue arms the line with the credential this call carried: every field
// is passed through the scrub before it is written, so a value that reached
// one by a bug is removed here rather than written into a log an operator
// reads and greps. The auditor it answers is a copy, so one call's value
// cannot reach another's line.
//
// A value that is short enough to be a substring of the line's own text
// over-redacts that text, and that is the safe direction: a mangled line is
// readable enough to act on, and a line with a credential in it is not.
func (a *Auditor) withValue(value []byte) *Auditor {
	armed := *a
	if len(value) == 0 {
		armed.scrub = noScrub
		return &armed
	}
	armed.scrub = func(s string) string { return strings.ReplaceAll(s, string(value), "[redacted]") }
	return &armed
}

// Write writes the call's one line. It refuses an event whose fields are not
// ones this intake writes, and an auditor that was not armed with the call's
// value: the field rules cannot tell a value from a name, so an unarmed line
// is one a value could reach, and a call that cannot be recorded is not made.
func (a *Auditor) Write(ctx context.Context, ev AuditEvent) error {
	if a.scrub == nil {
		return errors.New("intake: the audit line was not armed with the call's value, so a value could reach it: the call must not be reported as done")
	}
	ev = AuditEvent{
		Caller: a.scrub(ev.Caller),
		Sub:    a.scrub(ev.Sub),
		Op:     a.scrub(ev.Op),
		Scope:  a.scrub(ev.Scope),
		ID:     a.scrub(ev.ID),
		Owner:  a.scrub(ev.Owner),
		Status: a.scrub(ev.Status),
	}
	if err := checked(ev); err != nil {
		return err
	}
	return a.line(ctx, slog.LevelInfo, auditMessage,
		slog.String("caller", safetext.OneLine(ev.Caller)),
		slog.String("sub", safetext.OneLine(ev.Sub)),
		slog.String("op", safetext.OneLine(ev.Op)),
		slog.String("scope", safetext.OneLine(ev.Scope)),
		slog.String("id", safetext.OneLine(ev.ID)),
		slog.String("owner", safetext.OneLine(ev.Owner)),
		slog.String("status", safetext.OneLine(ev.Status)),
	)
}

// line writes one record to the auditor's handler and reports whether it was
// written. The handler's own level is asked before anything is built and its
// error is returned, so a log that is not recording is a failure and not a
// line that looks written (credsvc's own rule, for its reason).
func (a *Auditor) line(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) error {
	if !a.h.Enabled(ctx, level) {
		return fmt.Errorf("intake: the audit log does not record at %s: a call this intake could not audit must not be served", level)
	}
	r := slog.NewRecord(time.Now().UTC(), level, msg, 0)
	r.AddAttrs(attrs...)
	if err := a.h.Handle(ctx, r); err != nil {
		return fmt.Errorf("intake: the audit line was not written: %w", err)
	}
	return nil
}

// checked holds an event to the rules of its fields, after the scrub has run,
// and answers the error naming the field that is not one this intake can
// record — never what it held. Every field is a closed set or a shape the
// intake itself built, which is the second half of the no-value rule: the
// scrub removes a value that reached a field, and these rules are why one
// cannot reach one.
func checked(ev AuditEvent) error {
	switch {
	case !validOp(ev.Op):
		return errors.New("intake: the audit event names no act this intake serves")
	case ev.Scope != "" && !validScope(Scope(ev.Scope)):
		return errors.New("intake: the audit event names a scope this intake does not serve")
	case !validStatus(ev.Status):
		return errors.New("intake: the audit event names no status this intake writes")
	}
	if ev.Caller != "" && !printableValue(ev.Caller, maxCallerBytes) {
		return errors.New("intake: the audit event's caller is not a printable address")
	}
	if ev.Sub != "" {
		if _, err := secretname.User(ev.Sub, "audit"); err != nil {
			return errors.New("intake: the audit event's account id is not one this intake builds a name from")
		}
	}
	if ev.ID != "" {
		if _, err := secretname.Parse(ev.ID); err != nil {
			return errors.New("intake: the audit event's container is not a secret ID this intake writes")
		}
	}
	if ev.Owner != "" && !printableValue(ev.Owner, maxCallerBytes) {
		return errors.New("intake: the audit event's owner is not a printable address")
	}
	return nil
}

// maxCallerBytes bounds an address the line carries. The bound is the header
// token's, which is what a verified address came in as.
const maxCallerBytes = 512

// printableValue reports whether s is printable ASCII with no space: an
// address, a name or a status, and the rule the credentials service's own
// header values are held to.
func printableValue(s string, max int) bool {
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

// validScope, validOp and validStatus are the closed sets the wire and the
// audit line share: an act, a scope or a status this file does not declare is
// one no route can produce and no reader should act on.
func validScope(s Scope) bool { return s == ScopeUser || s == ScopeShared }

func validOp(op string) bool {
	return op == OpCreate || op == OpVersion || op == OpList || op == OpDelete
}

func validStatus(status string) bool {
	switch status {
	case credsvc.StatusOK, ErrorUnverifiedLauncher, ErrorNotAdmin, ErrorRequestRefused,
		ErrorNoSuchSecret, ErrorForeignContainer, ErrorStateUnavailable:
		return true
	}
	return false
}
