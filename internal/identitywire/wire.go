// Package identitywire is the wire contract of the credentials service's
// identity routes (C1 of docs/launcher-does-not-mint): the five paths and the
// class each admits, the request and the response body of each, the refusal
// codes a caller branches on, the name of the execution variable the ticket
// travels in, the shape of the issuance record and the place it lives at, and
// the strict decoding every body is read with.
//
// It is a package of its own and it depends on no service code — internal/credsvc
// and internal/cli import it and not the other way round — so that the sides
// that speak it agree on one spelling of every field: the launcher (LM6), the
// runner (LM7), the resident orchestrator's ask (LM24), the service's handlers
// (LM1 to LM4) and the rules test that pins where the record lives (LM5).
//
// # What a caller may and may not name
//
// No REQUEST of this contract has a field for a uid or for a claim set. The run
// and the repository are the launcher's to name (R5 states the residual that
// follows: a roster caller who learns a run id may ask for it first), and
// everything derived from them is the service's: the uid comes from the
// record's own path (token.UID), the project claim is the installation's own,
// and the claims are read off the record when the service signs (R3). A body
// that names one anyway is refused as an unknown field, which is what
// DecodeStrict is for, and what TestIdentityWireHasNoCallerChosenIdentity pins.
//
// A RESPONSE may report an id the service itself derived — the pairing's and
// the device's — because the CLI needs the handle of a thing it did not mint.
// Those are the service's own values from crypto/rand, reported under the name
// of the thing they identify, and no route reads one back from a caller.
//
// # What of the slip the routes reuse (F1)
//
// The slip (internal/credsvc/slip.go) is the same mechanism in small, and the
// identity routes reuse four things of it rather than inventing them:
//
//   - the roster class: the caller of the ticket, the pairing and the probe is
//     a verifier's Google ID token on the roster, read from the Authorization
//     bearer and verified by the service's own VerifyLauncher;
//   - the once-only fence: a record is claimed once, and the second claim is
//     refused (Claim, ErrClaimed, R5);
//   - a stored hash of a secret: the ticket is kept as its digest and never in
//     the clear (Record.TicketHash), and so is the nonce of the claim
//     (Claim.NonceHash, R2), exactly as the slip keeps ClaimSecretHash;
//   - the bridge: a CLI of this release signs locally where the service does
//     not serve the route, which is the answer `slipRouteAbsent` already reads.
//
// Three things are not the slip's and are this contract's own: the ticket is
// presented ONCE and then dies into a signed token, where the slip's claim
// secret is presented on every later call; the record holds the claims the
// service will sign, because the identity is signed long before the run can use
// it (R3); and the claim is answered ahead of the shared request parse, as the
// merge mint is, because its caller holds no run identity yet (R2).
package identitywire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
)

// The five routes of R2, one per ask, and the registration this plan adds to
// them (JobPath, declared beside its bodies in batchclaim.go) — six paths in
// one table, read by RouteFor and mounted by the service one by one. Their
// caller holds no run identity — the ticket is asked for before the run exists
// and the claim is the run's first call — which is why every one of them is
// answered ahead of the shared request parse (internal/credsvc's
// aheadOfTheSharedParse).
const (
	// TicketPath is where a launcher asks for a run's ticket (LM6), and where
	// the resident orchestrator asks for an attempt it re-issues (LM24).
	TicketPath = "/v1/identity/ticket"
	// ClaimPath is where the run claims the ticket for its identity (LM2, LM7).
	ClaimPath = "/v1/identity/claim"
	// PairingPath is where a verified person asks for a pairing token (LM4,
	// LM10), the token the phone's key is sealed to.
	PairingPath = "/v1/identity/pairing"
	// DevicePath is where a verified person asks for the device token of the
	// pairing they completed (LM4, LM10).
	DevicePath = "/v1/identity/device"
	// ProbePath is what `fugaro update` calls before it moves the grant (LM14,
	// R8): the service signs a fixed payload as the runs' signer and discards
	// it, so an answer means the issuer's row and its account are right (F33).
	// It admits the roster's operators only (OperatorsOnly).
	ProbePath = "/v1/identity/probe"
)

// CallerClass is the class a verified caller was admitted as (LM3). It is what
// the ticket route records on the issuance record (R3), and it is what tells
// the classes apart where the checks differ: the orchestrator's ask is judged
// against its own record (LM24) and a roster service account needs no scope
// judgement (D3137).
type CallerClass string

const (
	// ClassPerson is a person on the launcher roster: a `user:` entry, whose
	// Google ID token the service verified (D3123).
	ClassPerson CallerClass = "person"
	// ClassServiceAccount is a roster service account (D3123). A `group:` entry
	// is not one: a group holds no token to present, and `update` names such
	// entries before the grant moves.
	ClassServiceAccount CallerClass = "service_account"
	// ClassOrchestrator is the resident orchestrator, asking under its own
	// account for an attempt it re-issues (D3125 amended by D3136). It is a
	// class of its own because what it may be issued is bound to its record and
	// judged by no launch scope (LM24).
	ClassOrchestrator CallerClass = "orchestrator"
	// ClassJobAccount is a run's own job service account, and it is the claim's
	// caller alone (LM2): the claim must come from the account the record names
	// (R4, F12), and what it proves is the JOB and not the execution (F11, U1).
	ClassJobAccount CallerClass = "job_account"
)

// Route is one of the identity routes: the path it is reached on, the classes
// it admits, and whether it admits the roster's OPERATORS alone.
type Route struct {
	// Path is the route's path.
	Path string
	// Admits are the classes this route serves, and no others (R2, LM3).
	Admits []CallerClass
	// OperatorsOnly marks the probe, which the roster's operators answer and a
	// plain launcher does not (R2). It is a fact of its own and not a second
	// spelling of Admits: the person class is one list, and the operators are
	// the owner's subset of it that `update` may be run by.
	OperatorsOnly bool
}

// routes is the one spelling of the routes, and the paths of this contract are
// read from it rather than respelled by a caller.
var routes = []Route{
	{Path: TicketPath, Admits: []CallerClass{ClassPerson, ClassServiceAccount, ClassOrchestrator}},
	{Path: ClaimPath, Admits: []CallerClass{ClassJobAccount}},
	{Path: PairingPath, Admits: []CallerClass{ClassPerson}},
	{Path: DevicePath, Admits: []CallerClass{ClassPerson}},
	{Path: ProbePath, Admits: []CallerClass{ClassPerson}, OperatorsOnly: true},
	// The registration (BR17, R19). Its classes are the ticket ask's, because
	// the registration is the ticket ask's second half: the caller that may be
	// issued a run is the caller that may name the job it launched — and R19
	// holds it to its own record, since only the identity that asked for the
	// ticket may write that record's id. A roster service account is a launcher
	// (D3123, R20) and the resident orchestrator launches attempts it re-issues
	// under its own class (D3136), so both are here beside the person.
	{Path: JobPath, Admits: []CallerClass{ClassPerson, ClassServiceAccount, ClassOrchestrator}},
}

// Routes answers the routes as the service mounts them: the path, the classes
// each admits and the probe's own rule. It is a copy, so a caller cannot edit
// the contract's table.
//
// A route's presence here is the CONTRACT's word and not the mount's: the
// service mounts these paths one by one (internal/credsvc's servedRoute), and
// the registration is mounted by BR17 — until then a caller that reaches the
// path gets the unknown-path answer at the door and no refusal of this
// contract's.
func Routes() []Route {
	out := make([]Route, len(routes))
	for i, rt := range routes {
		out[i] = Route{Path: rt.Path, Admits: append([]CallerClass(nil), rt.Admits...), OperatorsOnly: rt.OperatorsOnly}
	}
	return out
}

// RouteFor answers the route a path names. It is how a reader tells a request
// of this contract from any other route of the service.
func RouteFor(path string) (Route, bool) {
	for _, rt := range routes {
		if rt.Path == path {
			return rt, true
		}
	}
	return Route{}, false
}

// Admits reports whether this route serves a caller of that class.
func (rt Route) AdmitsClass(class CallerClass) bool {
	for _, c := range rt.Admits {
		if c == class {
			return true
		}
	}
	return false
}

// AttemptID is the reference an orchestrator's ask carries to the attempt it
// re-issues (D3136): the id of the orchestrator's own intent for that attempt,
// which is the item's id, a colon and the attempt's number, exactly as
// internal/orchestrator's IntentID writes it and IntentItem and IntentAttempt
// read it back. The issuer looks the reference up on the orchestrator's record
// and serves only an attempt with no outcome (LM24); no other class may name
// one, and an ask of another class that carries one has nothing to look up.
type AttemptID string

// Valid reports whether the reference is one the orchestrator's own spelling
// could have produced: a non-empty item id, a colon, and an attempt number of
// at most nine digits that is not zero. The digits are read one by one, as
// internal/orchestrator's IntentAttempt reads them, so that "wi-1:1x" is not
// attempt 1 and two references that mean one attempt cannot both be looked up.
func (a AttemptID) Valid() bool {
	item, rest, ok := strings.Cut(string(a), ":")
	if !ok || item == "" || rest == "" || len(rest) > 9 {
		return false
	}
	n := 0
	for i := range len(rest) {
		if c := rest[i]; c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(rest[i]-'0')
	}
	return n > 0
}

// TicketRequest is the launcher's ask for a run's ticket (LM6), and the
// resident orchestrator's for an attempt it re-issues (LM24).
//
// It names the run and the repository, which are the launcher's own to choose,
// and the ingredients of the claims — never a claim set and never a uid. The
// service derives the rest: the uid from the record's path, the project claim
// from its own installation, and the expiry from MaxRuntimeSeconds, which it
// refuses beyond its configured maximum (R4).
type TicketRequest struct {
	// Slug and Run name the run the ticket is for.
	Slug string `json:"slug"`
	Run  string `json:"run"`
	// Account is the job service account the launch will run as. The service
	// refuses one that is not this repository's job's account (R4): the claim
	// must come from it (F12 — for a shared project job it narrows to the
	// profile and not to the repository, and that is stated).
	Account string `json:"account"`
	// Launcher is the rb claim: the launcher's own, self-asserted name (R11,
	// config user or git email). It is attribution only; nothing in the
	// database rules trusts it.
	Launcher string `json:"launcher"`
	// Branch is the branch the run works on (fb).
	Branch string `json:"branch,omitempty"`
	// PullRequests are the pulls the run was asked to review (fpr).
	PullRequests []int `json:"pull_requests,omitempty"`
	// ContextRepos are the repositories whose context the run may read (fcr).
	// Each one is judged for the view action and against the policy's
	// allow-list before the list is signed (F31, R16), and an orchestrator's
	// ask may name none at all (D3136).
	ContextRepos []string `json:"context_repos,omitempty"`
	// MaxRuntimeSeconds is how long the launcher asks the run's identity to
	// live, measured from the claim. It is bounded by the service's configured
	// maximum, which refuses an ask beyond it rather than clamping it (R4).
	MaxRuntimeSeconds int `json:"max_runtime_seconds"`
	// Attempt is the orchestrator's reference to the attempt it re-issues
	// (D3136). Every other class leaves it empty, and an orchestrator's ask
	// that names none is refused.
	Attempt AttemptID `json:"attempt,omitempty"`
	// Resumes is the orchestrator's reference to the paused run it continues
	// (D3189): the run that suspended itself, whose pause the credentials
	// service recorded and which the run this ticket is for picks up. It is
	// spelled as the task member a resumed run is launched with
	// (runstore.PauseMemberResumes), because it is the same reference in the
	// two places a resume is named.
	//
	// It is the orchestrator's own and no other class may name one, exactly as
	// Attempt is — and an ask names one or the other and never both, because a
	// run either re-issues an attempt of the orchestrator's own record or
	// continues a pause the service wrote (D3136, D3189).
	Resumes string `json:"resumes,omitempty"`
	// Job is the full resource name of the Cloud Batch job this launch is about
	// to create (R1, R19), and it is empty for a Cloud Run launch, which is the
	// launch this plan leaves exactly as it was (R18).
	//
	// It is the launcher's own word, and the ticket does not depend on it: what
	// it does is land on the record (Record.Job), where it decides how the run
	// may claim — a record that names a job is a Batch launch, and a Batch
	// launch is served only to a VM of that job, with the VM's own attestation
	// (D3114). A run whose record names no job claims as every run of the
	// release behind claims.
	Job JobName `json:"job,omitempty"`
}

// TicketResponse is the issuer's answer (LM1, LM6). The ticket is the one-time
// secret the launcher puts on the execution (TicketEnv) and the run presents at
// its claim; the service keeps only its digest (Record.TicketHash, R3) and no
// answer of it is written where another launcher reads (R5).
type TicketResponse struct {
	// Ticket is the secret the run claims with.
	Ticket string `json:"ticket"`
	// ExpiresAt is when the ticket stops being claimable. The ticket's life is
	// the service's own and shorter than the run's: R2 states that a ticket
	// with its nonce and the job's identity token together are a run's token
	// for that long and no longer.
	ExpiresAt time.Time `json:"expires_at"`
}

// ClaimRequest is the run's ask for its identity (LM2, LM7). It carries the
// ticket, the nonce and — for a Batch run — the attestation, and nothing else:
// the uid, the claims and the account are the record's (R2, R3), and the caller
// is the job's own Google ID token on the Authorization bearer, of which
// nothing but the account is read.
type ClaimRequest struct {
	// Ticket is the secret the launcher asked for and put on the execution.
	Ticket string `json:"ticket"`
	// Nonce is a value the runner draws and keeps in memory for as long as it
	// retries (R2). The same nonce again gets the same answer, so a retry after
	// a lost response is not a second claim; another nonce after a success is
	// refused. The service keeps only its digest (Claim.NonceHash), and the
	// nonce reaches no log line and no audit line.
	Nonce string `json:"nonce"`
	// Attestation is the claiming VM's identity token, in the metadata server's
	// full format (U2, R18), and it is empty for a Cloud Run launch — whose
	// claim is then byte for byte the body of the release behind, which is what
	// the two sides of a bridge agree on.
	//
	// A record that names a job (Record.Job) is a Batch launch and is served
	// ONLY with one: the account proves the job and never the execution (F4,
	// F11), so the VM's own token is the whole of what tells two runs on one
	// job's account apart (D3114). The value is a credential of that VM and is
	// treated as a secret: no refusal repeats it, no audit line carries it, and
	// nothing of it is stored (Record keeps no copy).
	Attestation Attestation `json:"attestation,omitempty"`
}

// ClaimResponse is the claim's answer: the custom token the run exchanges for
// its identity (LM2, LM7). The token is signed as the runs' signer with the
// claims the record holds (R3) and the uid the record's path derives; the
// answer carries no uid and no claims of its own, because the token is the
// whole of them.
type ClaimResponse struct {
	// Token is the custom token, and it is the only signature the route
	// returns. A run's token is what the exchange takes, and no route of this
	// contract returns bytes of a signature otherwise.
	Token string `json:"token"`
	// ExpiresAt is when the token's own claims expire (fx).
	ExpiresAt time.Time `json:"expires_at"`
}

// PairingRequest is the pairing ask's body, and it has no field: R14 states
// that the uid and the claims are the service's to derive (token.MintPairing),
// so there is nothing for a caller to name. It is sent as `{}`, and a body that
// names a field — a uid among them — is refused by DecodeStrict.
type PairingRequest struct{}

// PairingResponse is the pairing's answer (LM4, LM10).
type PairingResponse struct {
	// Token is the pairing token, whose uid the phone's key is sealed to.
	Token string `json:"token"`
	// Pairing is the id the pairing token names, derived by the service from
	// crypto/rand. It is the handle the CLI writes the pairing node at and the
	// phone's own name for the pairing: it is not an identity, no route takes
	// one from a caller, and the field reports what the service minted.
	Pairing string `json:"pairing"`
	// ExpiresAt is when the pairing token stops working (PairingLifetime).
	ExpiresAt time.Time `json:"expires_at"`
}

// DeviceRequest is the device ask's body, and it has no field: the device token
// names a freshly drawn uid and no claim of its own (token.MintDevice), so no
// field of the request can reach either (R14). That the person did complete the
// pairing — the typed code and the phone's key — is checked on the person's own
// side, which is where the code is read.
type DeviceRequest struct{}

// DeviceResponse is the device's answer (LM4, LM10).
type DeviceResponse struct {
	// Token is the device token, whose claims carry the device's uid.
	Token string `json:"token"`
	// Device is the uid the device token names, derived by the service. It is
	// the handle the device node and the keychain item are written under, and
	// it is the service's own value and not a caller's.
	Device string `json:"device"`
	// ExpiresAt is when the device token stops working.
	ExpiresAt time.Time `json:"expires_at"`
}

// ProbeRequest is the probe's body, and it has no field: the probe asks whether
// the service can sign as the runs' signer at all, and there is nothing about
// that for a caller to name (R8).
type ProbeRequest struct{}

// ProbePayload is the fixed payload the probe makes the service sign and
// discard (R8, F33). It is a constant of this contract so that the payload can
// never be a caller's bytes: the route is then neither a signing oracle nor a
// way to spend the signing quota at will (R2).
const ProbePayload = "fugaro-identity-probe"

// ProbeResponse is the probe's answer (R2, R8, F33): the account the service
// signed the fixed payload as — the runs' signer the installation's row names —
// and when it did. It carries no signature bytes and no digest of them: an
// answer that carried either would be one an attacker can use as a signature,
// and what the probe needs to prove is that the row and the account are right,
// which the signer's own name proves.
type ProbeResponse struct {
	// Signer is the account the service signed ProbePayload as.
	Signer string `json:"signer"`
	// SignedAt is when it signed.
	SignedAt time.Time `json:"signed_at"`
}

// The refusal codes of these routes, in the shape the service's other routes
// already write (internal/credsvc's WireError, read by the CLI and the runner
// through credsvc.ReadWireError).
//
// The CLI of this release branches on these codes for one thing only, and R7
// says it is the whole of what it may branch on: where the answer is
// RefusalNotServed it signs locally as before. Every other code is a refusal
// with the fix, and none of them may be read as a bridge.
const (
	// RefusalNotServed is the answer of a service whose identity half is not
	// configured: the routes are mounted, no signer is (I1). It is the code R7
	// names as the bridge's first trigger, and it is a code of this contract's
	// own and not the service's existing refuseNotServed, which writes
	// ErrorUnverifiedJobIdentity and refuses a route a workspace scope does not
	// serve (F27).
	//
	// It is answered with StatusUnavailable — 503 — and deliberately not with
	// the 404 a path the service does not know is answered with, because R7
	// keys the bridge on three things and those two must stay told apart: the
	// release-behind answer is the 404 that `slipRouteAbsent` already reads,
	// and this one is the service saying it holds no issuer yet.
	RefusalNotServed = "not_served"
	// RefusalCallerNotAdmitted is the refusal of a verified caller the route
	// does not admit: an account on no roster (LM3), a job account at the
	// ticket, or a caller of the probe who is not one of the roster's
	// operators (R2).
	RefusalCallerNotAdmitted = "caller_not_admitted"
	// RefusalNoLaunchScope is the refusal of the launch-scope check (LM23):
	// the caller is admitted, and this repository is not one it may launch.
	RefusalNoLaunchScope = "no_launch_scope"
	// RefusalIdentityRefused is the refusal of the ask itself, of the ticket or
	// of the claim: an account that is not the record's job account, a run that
	// is another caller's record, a ticket already claimed or beside an
	// outcome, a runtime beyond the configured maximum, an unknown or expired
	// ticket, a nonce that is not the claim's own, and an orchestrator's
	// attempt the record does not carry (LM1, LM2, LM24). It is one code
	// because the CLI's own branch is the same for all of them — fail with the
	// reason and the fix — and because a code a caller may act on differently
	// is a code that would have to be a decision.
	RefusalIdentityRefused = "identity_refused"
)

// TicketEnv is the name of the execution variable the ticket travels in: the
// launcher sets it on the execution it starts (LM6) and the runner reads its
// claim out of it (LM7).
//
// It is a new name and not runner.BudgetTokenEnv's, because that one keeps
// meaning "the seal key" until the release after this one deletes it (LM18):
// the two markers stand for two different mechanisms, and a runner of this
// release holds both — it claims where it finds a ticket and opens the sealed
// object where it finds the old marker (R7).
const TicketEnv = "FUGARO_IDENTITY_TICKET"

// pathRecords is the family the issuance records live in, under the database's
// creds subtree — which the rules already close to every signed-in identity,
// read and write (F26, R3). Both a run's token and a person's are refused there
// by a rule the generator already writes, so this contract adds no rule (LM5
// pins the path against the generator and generates nothing).
const pathRecords = "creds/issuance"

// Record is the issuance record (R3): what the service writes once per slug and
// run when it issues a ticket, and reads again before it signs. It is never a
// place a ticket or a nonce is: both are kept as their digests alone (Digest).
//
// The slug and the run are the record's PATH and not fields of it
// (PathRecord), exactly as a slip's id is its path: a copy of either in the
// value would be a second place for it to be wrong, and the uid the service
// signs is derived from the path it read (token.UID).
type Record struct {
	// Caller is the verified address of the caller that asked — the address its
	// own Google ID token carried — and Class is the class it was verified as
	// (LM3). They are what an operator reads to see who asked for a run (R3).
	Caller string      `json:"caller"`
	Class  CallerClass `json:"class"`
	// At is when the record was written.
	At time.Time `json:"at"`
	// Claims are the claims the service will sign at the claim (R3): the uid
	// comes from the path and the project from the installation, and every
	// other claim is here because the record is what the claim is re-checked
	// against before the signing (R4).
	//
	// It is the signer's own type (token.Claims) and not a second spelling of
	// the claim set: one type means the record cannot hold a set the signer
	// would refuse, and the wire names a token ends up carrying (fs, fr, fx and
	// the rest) are built from it at the signing, by token's own claimSet.
	Claims token.Claims `json:"claims"`
	// ExpectedAccount is the job service account the claim must come from (R4,
	// F12). It is the account the ask named, checked against the repository's
	// own job to build the record, so what it records is which job may claim.
	ExpectedAccount string `json:"expected_account"`
	// TicketHash is the digest of the ticket (Digest). The ticket itself is
	// never stored and never written down, here or anywhere (R3).
	TicketHash string `json:"ticket_hash"`
	// Claimed is who claimed the record, when, and the digest of the nonce the
	// claim carried (R2, R5). Its zero value is an unclaimed ticket, and every
	// claim on a record whose claim is not the same account with the same nonce
	// is refused.
	Claimed Claim `json:"claimed"`
	// Attempt is the reference an orchestrator's ask carried (D3136), empty for
	// every other class. It is kept so that the record says which attempt the
	// run id belongs to, which is what the issuer's own check reads (LM24).
	Attempt AttemptID `json:"attempt,omitempty"`
	// Job is the Cloud Batch job this run's launch is, as the launcher named it
	// in its ticket ask (R1, R19). It is empty on a Cloud Run run's record, and
	// it is the field that decides how the run may claim: a record that names a
	// job is served only to a VM of that job, with the VM's own attestation
	// (D3114), and a record that names none is claimed as every run of the
	// release behind claims.
	//
	// It is written with the ticket and written AGAIN by every ask before the
	// claim, because the ask replaces the whole record — a re-ask re-issues the
	// ticket and the old one dies with the node's digest (R6) — so the LAST
	// ask's name is the one the claim is held to. What it never does is change
	// under a claimed record: the store refuses a replacement once the ticket is
	// claimed (R5), so the name the run's identity was signed from is the name
	// it keeps, and a second job under the same name is a different launch with
	// a different run id.
	Job JobName `json:"job,omitempty"`
	// JobID is the platform's id for that job, as the registration wrote it
	// (R19). It is empty on a record that names no job and on one whose job is
	// not yet registered — the launcher's second call is what writes it — and a
	// Batch run whose record still carries none is refused at its claim: the
	// claim route has no id to hold the VM's job to, and the safe side of that
	// is the run stopping.
	JobID string `json:"job_id,omitempty"`
}

// Claim is one run's claim on its record: the account that claimed it, the
// instant it did, and the digest of the nonce it carried.
type Claim struct {
	// Account is the verified job service account that claimed. The claim
	// checked it against the record's ExpectedAccount, so the two agree; both
	// are kept because the record then says what was expected and what arrived,
	// which is what an operator reading a refusal needs (as the slip's does).
	Account string `json:"account"`
	// At is the instant of the claim.
	At time.Time `json:"at"`
	// NonceHash is the digest of the nonce this claim carried (Digest). It is
	// what makes the same nonce's second call the same claim's retry and a
	// different nonce's a refusal after a success (R2). The nonce itself is
	// never stored, logged or audited.
	NonceHash string `json:"nonce_hash"`
}

// PathRecord is where the issuance record lives (R3): the family under the
// database's creds subtree, then the slug, then the run. The two names are
// escaped as every other database key of this installation is (budget.Key), so
// that two pairs cannot collapse onto one node.
//
// An empty slug or run is a panic and not a path: an empty segment collapses
// the path onto another node, and neither ever arrives from a verified ask.
func PathRecord(slug, run string) string {
	if slug == "" {
		panic("identitywire: empty slug in the issuance record's path")
	}
	if run == "" {
		panic("identitywire: empty run in the issuance record's path")
	}
	return pathRecords + "/" + budget.Key(slug) + "/" + budget.Key(run)
}

// Digest is how this contract keeps a secret it must compare and must never
// store: the SHA-256 of the value, lower-hex. The ticket (Record.TicketHash)
// and the claim's nonce (Claim.NonceHash) are both kept this way and nowhere in
// the clear.
//
// SHA-256 with no salt and no stretching, which is the right choice for exactly
// this input and the slip's own (creds.ClaimSecretHash): the ticket is drawn
// from crypto/rand, so there is no dictionary to run and nothing a work factor
// would slow down.
func Digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// DecodeStrict reads one body of this contract: a single JSON value, no field
// the type does not have, and no bytes after it.
//
// It is the rule every body here is read with (R2: the wire decodes strictly),
// and it is what makes "no field for a uid or a claim set" a refusal rather
// than a byte quietly ignored: a caller that names a uid is refused by name and
// never served under a value it chose. The bodies whose type has no field at
// all (PairingRequest, DeviceRequest, ProbeRequest) are sent as `{}` and read
// with the same call.
func DecodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("identitywire: trailing data after the JSON value")
	}
	return nil
}

// ErrNoRecord is the issuance store's answer for a slug and run no record was
// written for: an unknown ticket, and the refusal of a claim that presents one
// (LM2, R4).
var ErrNoRecord = errors.New("identitywire: no issuance record for that run")

// ErrAnotherCaller is the store's refusal of a record that belongs to another
// caller (R5): the caller on the record is the only one it may be re-issued to,
// and the caller on a record whose ticket is claimed is refused too.
var ErrAnotherCaller = errors.New("identitywire: the run is another caller's record")

// ErrClaimed is the store's refusal of a claim on a record that is claimed by
// another account, or by this account with another nonce (R2, R5): a ticket is
// claimed once, and the retry that is the same claim's carries the same nonce.
var ErrClaimed = errors.New("identitywire: the ticket was already claimed")

// StoreError wraps a store's own failure so a route can tell "the database
// could not be read" from a decision about the caller: the first is the
// service's own failure and is answered as one (R16's ScopeFault), and the
// second is the caller's. The two are never one refusal.
type StoreError struct {
	// Path is the record's path, which names no secret.
	Path string
	// Err is what the store answered.
	Err error
}

func (e *StoreError) Error() string {
	return fmt.Sprintf("identitywire: the issuance record at %s: %v", e.Path, e.Err)
}

func (e *StoreError) Unwrap() error { return e.Err }
