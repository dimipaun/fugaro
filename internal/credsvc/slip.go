package credsvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The slip routes (DS1.3, D1937): the grant a launcher asks for and the claim
// the run makes once.
//
// They are one exchange in two halves, and the halves have different callers,
// which is why they are two routes and not one:
//
//	POST /v1/slip         launcher class. The launcher holds no run identity —
//	                      the run it is asking for does not exist yet — so this
//	                      path is answered ahead of the shared parse
//	                      (RequestFromHTTP), like the verify route and the two
//	                      read mints. It answers the slip's id.
//	POST /v1/slip/claim   job class. The run presents the two identities every
//	                      other run call does, so it goes through the shared
//	                      parse and the shared pairing, and is branched on after
//	                      the operator's stop switches, like the mint. Its first
//	                      call claims the slip and is answered the claim secret;
//	                      every call after it presents that secret and is
//	                      answered the claim's own reading (DS1.6 serves the
//	                      credentials).
//
// # What the two routes decide, and what they do not
//
// The grant decides four things: WHO is asking (the launcher class, D1938),
// WHICH credentials the slip will permit (the owner rule of R7 and the pool rule
// of G8, both checked here because this is the one place a whole run's set is
// written down), WHICH pool decided it (recorded on the slip, so a grant can be
// read back against the roster that allowed it), and how long the run may be
// served (R10's cap, kept here as a service constant).
//
// The pool rule is made here rather than where a credential is resolved because
// the slip is what a run is served from afterwards: the launcher is the person a
// roster is about, and what it asks for is decided once. The roster is therefore
// read at ISSUE time and never remembered — a member the publisher takes off it
// is refused at the next grant — while a slip already issued keeps serving the
// run it was granted for, which is what makes the rule a grant-time one and not
// one a run's calls re-answer (G8).
//
// The claim decides one thing: that the caller is the run the slip names, at
// the account the slip expects, and that nobody has claimed it yet (D1941). It
// reads no policy and no bundle, because a slip is a grant and not a
// credential.
//
// # The id is not a ticket
//
// Neither route admits a call by the id alone (G3): the grant is admitted by
// the launcher's own token, and the claim by the two identities the run proves
// and the account the slip expects. The id travels to the job in its
// environment and the agent can read it (F22), so it is treated as public, and
// the claim secret is what fences one launcher from another afterwards.

const (
	// SlipPath is the grant's path: a launcher asks for a run's grant.
	SlipPath = "/v1/slip"
	// SlipClaimPath is the claim's path: the run takes its slip once, and every
	// later call presents what the claim answered.
	SlipClaimPath = "/v1/slip/claim"
)

// maxSlipRuntime is the longest a granted slip may serve, and it is the cap R10
// puts on what a launcher may ask for.
//
// It is a constant of this service and not a read of the launcher's own
// configuration, deliberately: the point of the cap is that a slip cannot
// outlive the run it was granted for whatever the caller asks, and a bound read
// from the same place the request came from would be no bound at all. The value
// is the maximum runtime a Cloud Run job may be given
// (internal/backend.MaxRunTime, 24 hours): a longer slip would be one no run
// could use up, and a shorter one would end a run that the platform still lets
// work. It is spelled here rather than imported so that the compute platform —
// and the GCP client stack behind it — is not part of this service's binary;
// TestMaxSlipRuntimeIsThePlatformMaximum holds the two spellings together.
const maxSlipRuntime = 24 * time.Hour

// slipIDBytes is the entropy of a slip id: sixteen bytes, the width a random
// identifier needs to be unguessable in practice. The id is not a secret (a
// leaked one buys one run's credentials until that run ends and nothing else),
// but a guessable one would make an operator's database a place to enumerate
// runs at.
const slipIDBytes = 16

// maxSlipIDBytes bounds a presented slip id: the hex of slipIDBytes and not a
// byte more, because an id this service mints is exactly that and a longer
// value is a request this contract does not read rather than one to look up.
const maxSlipIDBytes = 2 * slipIDBytes

// maxSlipSlugBytes bounds the repository slug a grant names: task.Slug's own
// 63-character shape (a readable part, "-", and sixteen hex of the repository's
// hash), which is what a launcher's tokens spell.
//
// The bound is here, with a printable rule, rather than left to
// token.ValidSegment — the check the rest of this contract uses — because the
// slug is caller text that lands on this service's OWN line (slipGrant), and
// ValidSegment admits a bidi override: it is not a control rune and not a
// space, so it would rewrite the line an operator reads the slug in. The value
// is spelled here and TestGrantSlugIsOneCleanValue holds it to what task.Slug
// really produces.
const maxSlipSlugBytes = 63

// maxSlipExecutionBytes bounds the Cloud Run execution name a claim reports.
// It is generous for the real thing — an execution is its job's name and a
// short suffix, and a job name is at most 49 bytes (ValidJobName) — because the
// value is recorded and decides nothing, so a longer one is a name this service
// will not keep rather than a run it refuses to serve. It is a bound and a
// printable-ASCII rule together: this is the one free-text field either route
// carries, and the service's own line is the one place it lands, so it may hold
// no newline, no escape sequence and no bidi control that would rewrite the
// line an operator reads it in.
const maxSlipExecutionBytes = 128

// SlipGrantRequest is a launcher's grant call.
//
// Every field is a NAME or a bound and never a value: the credentials the slip
// will permit are named by their secret IDs (G9), and nothing here is or
// carries a credential.
type SlipGrantRequest struct {
	// Slug is the repository the run belongs to, as the launcher's own tokens
	// spell it (task.Slug). It is what the slip's Slug is, and the run identity
	// a claim presents must carry the same one.
	Slug string `json:"slug"`
	// Run is the run id the launcher minted (task.ValidRunID). It is the other
	// half of the binding, and a claim of any other run of the repository is
	// refused (F23).
	Run string `json:"run"`
	// Account is the job service account the launcher expects the run to call
	// as: the account the claim checks (D1937, R8). One account is dedicated to
	// one Cloud Run job per repository, so this is "which job", said in the one
	// way a job's token can prove.
	Account string `json:"account"`
	// Credentials are the secret IDs the run may be served, exactly and only
	// (creds.Slip.Allowed). A per-user name may be another owner's only if the
	// launcher IS that owner (R7), and a name a pool of the published policy
	// governs — the pool's own name, or a project-shared name whose credential
	// the pool lists — only if the launcher is that pool's owner or one of the
	// users it allows (D1936, G8).
	Credentials []string `json:"credentials"`
	// Sources are the sources of the table's chain this grant COVERS (C2's
	// `sources`, R4, D3171): each one a kind of the table's five and, for the
	// two kinds that name one, a pool's name. They are the whole of what the
	// run may be served from (CS1's walk), and they are a positive list and
	// never a wildcard: an empty one is a grant that covers no source of the new
	// kinds — what a launcher of a release that predates this route asks for —
	// and never a grant that covers everything, so an older CLI is served as it
	// was and no launch is widened by the absent member.
	//
	// What a grant does NOT carry is the table's provider, model and spelling:
	// those are read again at each call (CS1), because a source the project
	// moves between providers must serve every run already in flight.
	//
	// It is omitempty, and so is NamedPool below, because the launcher and the
	// service are routinely a release apart: the CLI is the piece a person
	// upgrades first and the service is deployed by the installation's own
	// `fugaro update` (CLAUDE.md's HARD RULE), so a launch of this release meets
	// a service whose reader is decodeStrict — and a member that reader does not
	// know refuses the WHOLE body, which is the grant refused, the run starting
	// with no slip, and the next call that needs a grammar-named credential
	// refused at the call (slipRequiresSlip). A launch that covers no source and
	// names no pool therefore sends neither member, and the older service reads
	// exactly the body it read before this route grew them.
	Sources []creds.GrantSource `json:"sources,omitempty"`
	// NamedPool is the pool the launch asked for with `--pool` (C2's
	// `named_pool`, D3176, CS12), empty when it named none. It is not Pool on
	// the record either: this is what the flag named, and the slip's Pool is the
	// pool the granted credentials belong to (the one whose owner and allowed
	// users this route checked), which a launch that named no pool still has.
	//
	// Naming a pool never widens what a run may be served from, so a name the
	// launcher is not entitled to is refused here rather than recorded: the
	// lookup that resolves "the launcher's pool" for the run checks the
	// entitlement again (PL4), and a record that carried a name its own grant
	// had refused would be a name that lookup could not tell from a checked one.
	//
	// It is omitempty for the reason Sources states: a launch that named no pool
	// sends no member, and a service deployed behind this release reads the body
	// it always did.
	NamedPool string `json:"named_pool,omitempty"`
	// MaxRuntimeSeconds is how long the launcher asks the run to be served for.
	// It is CLAMPED to maxSlipRuntime and never refused for being large: a
	// launcher asking for more than the platform allows is asking for the most
	// there is, and a refusal would fail a run over a number that buys it
	// nothing. A value that is not positive is refused, because a slip whose
	// runtime is not positive is one that is never live.
	MaxRuntimeSeconds int64 `json:"max_runtime_seconds"`
}

// SlipGrantResponse is what a granted call is answered: the slip's id, and the
// runtime that was actually granted, which may be less than what was asked for
// (maxSlipRuntime). The granted value is answered so that the launcher that
// passes the id on does not have to assume it.
type SlipGrantResponse struct {
	Slip              string `json:"slip"`
	MaxRuntimeSeconds int64  `json:"max_runtime_seconds"`
}

// SlipClaimRequest is a run's claim call.
type SlipClaimRequest struct {
	// Execution is the Cloud Run execution the run reports it is in
	// (CLOUD_RUN_EXECUTION). It is recorded and decides NOTHING (F7, D1937):
	// a job's token cannot prove which execution called, so binding a slip to
	// this value would be binding it to a claim the caller writes. It is on the
	// service's own line for an operator, and it is optional — a run that has
	// none to state is a call this contract reads.
	Execution string `json:"execution,omitempty"`
}

// SlipClaimResponse is what a claim call is answered.
//
// ClaimSecret is answered exactly once, by the call that CLAIMS the slip, and
// never again: a call that presents it is answered the claim's own reading,
// with this field empty, so a runner can tell the two answers apart and cannot
// be handed a second secret by replaying its own call (D1941). The record keeps
// only the hash of it (creds.ClaimSecretHash).
type SlipClaimResponse struct {
	ClaimSecret       string    `json:"claim_secret,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
	MaxRuntimeSeconds int64     `json:"max_runtime_seconds"`
}

// readGrantRequest reads a grant's body. The bound and the strict decode are
// the read mint's (readReadRequest), and so is the reason: a member this
// release does not model would otherwise be dropped silently, and a dropped
// member of a grant is a slip that permits nothing or a slip that never ends.
func readGrantRequest(body []byte) (SlipGrantRequest, error) {
	if len(body) > maxMintRequestBytes {
		return SlipGrantRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req SlipGrantRequest
	if err := decodeStrict(body, &req); err != nil {
		return SlipGrantRequest{}, errors.New("the body is not one JSON object of this route's fields")
	}
	switch {
	case !token.ValidSegment(req.Slug):
		return SlipGrantRequest{}, errors.New("the call names no repository slug")
	case !printableHeaderValue(req.Slug, maxSlipSlugBytes):
		// The slug is a name of the launcher's own grammar AND text that lands
		// on this service's own line, which is why it is held to one printable
		// value here: a bidi override is neither a control rune nor a space, so
		// ValidSegment admits it, and it would rewrite the line an operator
		// reads the slug in (the rule internal/safetext states).
		return SlipGrantRequest{}, fmt.Errorf("the call names a repository slug that is not one printable value (1 to %d ASCII bytes, no space)", maxSlipSlugBytes)
	case !task.ValidRunID(req.Run):
		return SlipGrantRequest{}, errors.New("the call names no run id of this installation's shape")
	case !validAccount(req.Account):
		return SlipGrantRequest{}, errors.New("the call names no job service account for the claim to expect")
	case req.MaxRuntimeSeconds <= 0:
		return SlipGrantRequest{}, errors.New("the call asks for no runtime, and a slip with none is a slip that is never live")
	}
	if err := checkGrantSources(req.Sources); err != nil {
		return SlipGrantRequest{}, err
	}
	return req, nil
}

// readClaimRequest reads a claim's body. It is readGrantRequest's rule at the
// size of a body that carries one name.
func readClaimRequest(body []byte) (SlipClaimRequest, error) {
	if len(body) > maxMintRequestBytes {
		return SlipClaimRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req SlipClaimRequest
	if err := decodeStrict(body, &req); err != nil {
		return SlipClaimRequest{}, errors.New("the body is not one JSON object of this route's fields")
	}
	if req.Execution != "" && !printableHeaderValue(req.Execution, maxSlipExecutionBytes) {
		// The execution is the ONE free-text value either route carries and the
		// only one that is not a name of this service's own grammar: it is the
		// caller's own string, and it is recorded on the service's line. Held to
		// one clean value for that reason — a newline, an escape sequence or a
		// bidi override in it would rewrite the line an operator reads it in,
		// which is the rule internal/safetext states and the rule the proxy's
		// own line keeps by carrying a class instead of a byte of the call.
		return SlipClaimRequest{}, fmt.Errorf("the call names an execution that is not one printable value (1 to %d ASCII bytes, no space)", maxSlipExecutionBytes)
	}
	return req, nil
}

// slipSecretFromHTTP reads the claim secret a call presents, if it presents
// one: presented=false is the CLAIM itself (the run's first call), and a caller
// that sent the header more than once, or sent one that is not a value this
// contract carries, is refused rather than read.
//
// The secret is not returned to a caller of this function for anything but the
// store's comparison and the call's scrub: it is the one value of these routes
// that is a credential.
func slipSecretFromHTTP(r *http.Request) (secret string, presented bool, err error) {
	values := r.Header.Values(HeaderSlipClaim)
	switch len(values) {
	case 0:
		return "", false, nil
	case 1:
		if !printableHeaderValue(values[0], maxHeaderTokenBytes) {
			return "", false, fmt.Errorf("credsvc: the %s header is not one claim secret", HeaderSlipClaim)
		}
		return values[0], true, nil
	}
	return "", false, fmt.Errorf("credsvc: the call carries the %s header more than once", HeaderSlipClaim)
}

// newSlipID draws one slip id: slipIDBytes from crypto/rand, in hex, which is
// the alphabet creds.PathSlip's segment rule admits and the one the read mint
// already uses for the claim secret it answers.
//
// A refused draw is an error and never a fallback to something guessable, for
// newClaimSecret's reason: an id another launcher could predict is an id it
// could present, and the only thing standing between it and the record is that
// it cannot claim it — which is a fence this service should not lean on when
// it can simply draw a good id.
func newSlipID() (string, error) {
	raw := make([]byte, slipIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("credsvc: drawing a slip id failed: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// slipGrant answers one grant call.
//
// The order below is why each step is where it is:
//
//   - the launcher, first, and on its own token: this is the route's whole
//     admission, and it is the reason the route is where it is in ServeHTTP. It
//     is the three classes the identity routes admit (launcherCaller). A refusal
//     here is typed and has no line — nothing is verified yet, so there is no
//     caller for a line to name.
//   - the daemon's class, right after it: admitted, and the one class no slip
//     can be written for, so its grant is refused here with its one line rather
//     than carried to a record nothing could serve (noGrantForTheOrchestrator).
//   - the run identity, refused by NAME if it was presented: a launcher holds
//     none and the run it is asking for does not exist yet, so a caller
//     presenting one is a call of a shape this route does not have. It is the
//     read mint's own check, and it is what keeps the class from being a second
//     way in for a run.
//   - the call's body, whose read is ALSO the registration of the launcher's own
//     token with the call-scoped scrub (readCall), exactly as the verify route
//     and the read mints do it.
//   - the operator's stop switches, which this route reads itself: it is
//     answered ahead of the shared parse that reads them for every other route,
//     and a stopped service grants nobody anything — the write below is a
//     lasting one, because an unclaimed slip never expires.
//   - the owner rule (R7), before the slip is written: a name the launcher may
//     not be served is a request this contract does not read, and a refused
//     grant writes nothing at all.
//   - the pool rule (G8, D1936), after the owner rule and beside it: the one
//     step here that reads the project's published policy, for the pools that
//     govern the names it asks for and for the pools the grant NAMES outright —
//     a `pool` source of the chain and the pool `--pool` asked for (CS14). It
//     comes after the stop switches because those are read from the database
//     too, and a stopped service is refused before any roster is consulted.
//   - the id and the record. The id is drawn here rather than taken from the
//     call: an id a caller chose is an id two callers can collide on.
//
// What is deliberately NOT here: the bundle and the cascade. A grant mints
// nothing — it writes down what a run may later ask for — so there is no
// credential to read and no admission to consult; the policy is read for its
// pools and for nothing else.
func (s *Server) slipGrant(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.launcherCaller(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way the verify route answers a
		// verifier's and the read mints theirs: a typed refusal and no line.
		s.refuseLauncher(w, err)
		return
	}
	if caller.Sub == "" {
		// The daemon's class, which is admitted and cannot be granted a slip:
		// the caller IS verified, so this refusal is one of the route's and
		// writes its one line (noGrantForTheOrchestrator says why a slip cannot
		// be written for it and where a resumed run's grant comes from instead).
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+noGrantForTheOrchestrator)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseGrant(ctx, w, caller.Account, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: a grant is asked for before the run exists, and a caller presenting one is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseGrant(ctx, w, caller.Account, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: a grant is asked for before the run exists, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		// A body this service will not hold is a refused REQUEST and not an
		// unavailable service, which is the reading the read mints give the same
		// error. The read is also what registers the launcher's own token with
		// the call's scrub.
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readGrantRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent.
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// The operator's stop switches, read HERE because this route is answered
	// ahead of the shared parse that reads them for every other route, as the
	// two read mints answered there read them themselves. A stopped service
	// writes no new record: an unclaimed slip never expires, so a grant that got
	// through while the service was stopped would be a lasting one, and the run
	// it starts is halted at the claim.
	on, which, err := s.cfg.Kills.Stop(ctx, req.Slug)
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so the
		// call is refused rather than served.
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	if err := checkGrantedNames(req.Credentials, caller.Sub); err != nil {
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	pool, err := s.grantPool(ctx, req, caller.Account)
	switch {
	case errors.Is(err, errPolicyUnreadable):
		// A roster that cannot be read is not an empty roster, and it is not a
		// refusal of what the launcher sent either: it is this service's state,
		// answered the way every other unreadable document here is.
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	case err != nil:
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	id, err := newSlipID()
	if err != nil {
		s.log.Error("credsvc: a slip id could not be drawn", "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	runtime := clampRuntime(req.MaxRuntimeSeconds)
	sl := creds.Slip{
		ID:              id,
		Owner:           caller.Sub,
		Launcher:        caller.Principal,
		Slug:            req.Slug,
		Run:             req.Run,
		ExpectedAccount: req.Account,
		Allowed:         append([]string(nil), req.Credentials...),
		Pool:            pool,
		SlipChain: creds.SlipChain{
			Sources:   append([]creds.GrantSource(nil), req.Sources...),
			NamedPool: req.NamedPool,
		},
		MaxRuntime: runtime,
	}
	if err := s.cfg.Slips.Create(ctx, sl); err != nil {
		// The store's own error is logged and never answered: it wraps the slip
		// id, and a refusal names the field and never what the call sent. The
		// answer is the same either way, which is what the store's refusals are
		// sentinels for.
		s.log.Error("credsvc: a slip could not be written", "slip", id, "run", req.Run, "repository", req.Slug, "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	// The grant is recorded on the service's own line and not as an audit line:
	// a StatusOK line requires a credential (checked), and a grant issues none —
	// what it writes is a record whose names DS1.6 serves. This line is where an
	// operator reads who asked for a run and which run it was.
	// The named pool is on this line beside the pool that decided the grant, and
	// the two are named apart because they are separate facts: an operator
	// reading why a run was served from one pool needs to see which pool the
	// launch asked for, which the record carries too (SlipChain.NamedPool).
	s.log.Info("credsvc: a slip was granted",
		"slip", id, "run", req.Run, "repository", req.Slug, "account", req.Account,
		"launcher", caller.Account, "pool", pool, "named_pool", req.NamedPool)
	body, err := json.Marshal(SlipGrantResponse{Slip: id, MaxRuntimeSeconds: int64(runtime.Seconds())})
	if err != nil {
		// The answer is built before it is written, as the read mints build
		// theirs: an answer this service cannot build is a refusal and not a
		// half-written body.
		s.log.Error("credsvc: the grant's answer could not be built", "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the grant's answer could not be built")
		return
	}
	writeSlipAnswer(w, body)
}

// slipClaim answers one claim call: the run's first call, which claims the slip
// and is answered the claim secret, and every call after it, which presents
// that secret and is answered the claim's own reading.
//
// The order below is why each step is where it is:
//
//   - the slip's id, and the claim secret if one was presented, before the body:
//     they are the two facts the record is found and decided by, and a call
//     that sends neither, or sends either twice, is a request this contract does
//     not read.
//   - the body, whose read is ALSO the registration of the three values of this
//     call with the call-scoped scrub (readCall): the run identity, the job's
//     token and the claim secret. The secret is the one credential either route
//     carries, so a body that echoed it back would be a body with a credential
//     in this service's logs.
//   - then the record, and only the record. A claim is decided by the slip and
//     by nothing else, so there is no policy read, no bundle read and no
//     cascade: this route's whole admission is the identity Verify already
//     returned and the shared pairing already bound to a repository.
//
// The two forms answer through ONE handler because they are one act seen twice:
// the run that claims and the run that says "this is my slip" are the same
// caller, presenting the same three facts, and the second form is what a
// keep-warm call is (DS1.5): a runner that must hold the process awake does it
// with a call that proves the slip still stands, and it does it here rather
// than at a route that reads a credential.
func (s *Server) slipClaim(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, req Request) {
	purpose, _ := routeAct(SlipClaimPath)
	slipID, err := oneHeader(r, HeaderSlip)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if !printableHeaderValue(slipID, maxSlipIDBytes) {
		// A slip id this service could not have minted is a request it does not
		// read, and it is refused BEFORE the database is asked: a lookup of
		// every string a caller sends is a lookup an operator's database should
		// not have to answer.
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"the "+HeaderSlip+" header is not one slip id")
		return
	}
	secret, presented, err := slipSecretFromHTTP(r)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	cl, err := readCall(r, []string{req.RunIDToken, req.JobIDToken, secret})
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	claim, err := readClaimRequest(cl.body)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if presented {
		// The reading form: the run already holds the slip and says so.
		sl, found, err := s.cfg.Slips.Slip(ctx, slipID)
		if err != nil {
			s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the slip could not be read")
			return
		}
		if !found || !slipIsThisRuns(sl, id) || !sl.ClaimMatches(secret) || !sl.Live(s.now()) {
			// ONE sentence for four different reasons, deliberately: a caller
			// that could tell "no such slip" from "not yours" from "wrong
			// secret" from "expired" would have an oracle for the ids of runs it
			// is not, and the run that may act on any of them acts the same way —
			// it has no slip it can use.
			s.refuseClaim(ctx, w, id, purpose, StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns)
			return
		}
		body, err := json.Marshal(SlipClaimResponse{
			ExpiresAt:         sl.Expires(),
			MaxRuntimeSeconds: int64(sl.MaxRuntime.Seconds()),
		})
		if err != nil {
			s.log.Error("credsvc: the claim's answer could not be built", "error", err)
			s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the claim's answer could not be built")
			return
		}
		s.log.Info("credsvc: a slip was read back",
			"slip", slipID, "run", id.Run, "repository", id.Slug, "account", id.JobAccount, "execution", claim.Execution)
		writeSlipAnswer(w, body)
		return
	}
	sl, secret, err := s.cfg.Slips.Claim(ctx, slipID, creds.Run{Slug: id.Slug, ID: id.Run}, id.JobAccount, s.now())
	if err != nil {
		status, typ, message := StatusUnavailable, ErrorStateUnavailable, "the slip could not be claimed"
		switch {
		case errors.Is(err, creds.ErrNoSlip), errors.Is(err, creds.ErrSlipIdentity),
			errors.Is(err, creds.ErrSlipClaimed), errors.Is(err, creds.ErrSlipExpired):
			// Every refusal the RECORD makes is one answer: the id names no
			// slip, the slip is another run's or another account's, or a run
			// already holds it. The store's own messages are not answered —
			// they wrap the id — and the four are not told apart, for the
			// reason the reading form gives.
			status, typ, message = StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns
		}
		s.log.Error("credsvc: a slip could not be claimed", "slip", slipID, "run", id.Run, "repository", id.Slug, "error", err)
		s.refuseClaim(ctx, w, id, purpose, status, typ, message)
		return
	}
	body, err := json.Marshal(SlipClaimResponse{
		ClaimSecret:       secret,
		ExpiresAt:         sl.Expires(),
		MaxRuntimeSeconds: int64(sl.MaxRuntime.Seconds()),
	})
	if err != nil {
		// The answer is built before anything is written, and a failure here
		// leaves a slip claimed with an answer nobody received. It is the one
		// failure on this route whose record cannot be unwound, which is why the
		// service's line names it: the run will re-claim, be refused as claimed,
		// and an operator needs the reason.
		s.log.Error("credsvc: the claim's answer could not be built", "slip", slipID, "run", id.Run, "error", err)
		s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the claim's answer could not be built")
		return
	}
	// The claim is recorded on the service's own line and not as an audit line:
	// a StatusOK line requires a credential, and what this call answers is a
	// secret the record keeps only a hash of. This line is also the ONLY place
	// the execution name the run reported is recorded (F7): it decides nothing,
	// and it is here so an operator can line a claimed slip up with the
	// execution that claimed it.
	s.log.Info("credsvc: a slip was claimed",
		"slip", slipID, "run", id.Run, "repository", id.Slug, "account", id.JobAccount, "execution", claim.Execution)
	writeSlipAnswer(w, body)
}

// slipNotThisRuns is the one sentence every refusal the RECORD makes is
// answered with, on both forms of the claim route. It names no value of the
// call — not the id, not the account, not the secret — and it does not say
// which of the four reasons it was, which is what keeps a caller from using the
// route to find out which slips exist.
const slipNotThisRuns = "the slip is not this run's, or not this account's, or is no longer live"

// slipIsThisRuns reports whether a record read back is the one the verified
// identity names: the run and the account, which are the whole of the binding
// (creds.Slip). It is the same comparison creds.RTDBStore.Claim makes inside its
// compare-and-set, repeated here because the reading form does not write and
// therefore has no cas to make it.
func slipIsThisRuns(sl creds.Slip, id Identity) bool {
	return sl.Slug == id.Slug && sl.Run == id.Run && sl.ExpectedAccount == id.JobAccount
}

// checkGrantSources holds the sources a grant covers to the table's own
// vocabulary (C1): each is one of the five kinds, and a pool is named on the one
// kind that names one.
//
// It is a SHAPE check and reads no policy, and the split is deliberate: which
// pools the project publishes and whether this launcher may be served from one
// is decided with the policy read (grantPool), because it is the same read that
// decides the credentials' own names. What is decided here needs no document at
// all, and refusing it earliest is what keeps a source nothing could ever serve
// out of the record.
//
// The kind rule is C1's OWN list (routing.ValidKind) rather than a second
// spelling of it, so the table's decoder and this route refuse a sixth kind by
// one answer: a kind the table cannot hold is a source no side serves, and
// skipping it — the alternative a caller might prefer — would silently narrow
// the grant of a run that believes it asked for it.
//
// The per-kind reading is C1's Source's own: `pool` names the pool it is, and
// the other four name nothing further — which pool serves a launcher is decided
// per run (launcher-pool), and the anthropic key, a provider's key and Vertex
// are one credential of the project each. A pool member on one of those is a
// member nothing would read, which is the same shape RT2's decoder refuses in a
// table.
//
// The refusal names the FIELD and the position in the list and never the value:
// the sources are the caller's own bytes, and this is the route where it hands
// this service a list of them.
func checkGrantSources(sources []creds.GrantSource) error {
	for i, source := range sources {
		switch routing.Kind(source.Kind) {
		case routing.KindPool:
			if source.Pool == "" {
				return fmt.Errorf("the source named in position %d is a pool's and names no pool, so nothing could ever be served from it", i+1)
			}
		case routing.KindLauncherPool, routing.KindAnthropic, routing.KindProvider, routing.KindVertex:
			if source.Pool != "" {
				return fmt.Errorf("the source named in position %d is a kind that names no pool, so the pool it names would be read by nothing", i+1)
			}
		default:
			return fmt.Errorf("the source named in position %d is not one of the table's kinds", i+1)
		}
	}
	return nil
}

// checkGrantedNames holds the credentials a grant names to the owner rule of
// R7: a per-user secret ID — fugaro_u_<id>_<name> — may be named only by the
// launcher whose verified Google account id is <id>.
//
// A project-shared name (fugaro_p_) that the grammar ADMITS has no owner to
// compare here: a shared credential belongs to the project and not to the
// person, and who may be served it is a fact about the pools of the published
// policy, which the grant's next step decides (grantPool, R11, G8). And a name
// with NEITHER of the grammar's two prefixes — a bundle entry's own name, such
// as a model credential's — has no owner to compare, so there is none to check.
//
// A name that carries one of the two prefixes and does NOT parse is refused,
// which is the one case that looks like the second and is not: the grammar is
// what says who owns a name, so a name that announces an owner this service
// cannot read — fugaro_u_<another account>_Model-Token, whose upper-case letter
// puts it outside the name rule; fugaro_u_<another account>_model_token, whose
// separator makes the split ambiguous; fugaro_u_<another account> with no name
// at all; fugaro_u_<another account>_<a platform credential kind> — is exactly
// the shape the owner rule exists for, and passing it over would leave the
// resolution (DS3.3) to decide, at a later call, what this route waved through.
// The prefix is the whole of the test, and the reading is fail-closed: a name
// that cannot be read as somebody else's is not thereby nobody's.
//
// The refusal names the FIELD and never the name: a credential name is caller
// text, and this is the one route where a caller hands this service a list of
// them.
func checkGrantedNames(names []string, owner string) error {
	for i, name := range names {
		if !ValidCredentialName(name) {
			return fmt.Errorf("the credential named in position %d is not one credential name this service could ever serve", i+1)
		}
		parsed, err := secretname.Parse(name)
		if err != nil {
			if strings.HasPrefix(name, secretname.UserPrefix) || strings.HasPrefix(name, secretname.ProjectPrefix) {
				return fmt.Errorf("the credential named in position %d carries one of the grammar's prefixes and is not a name the grammar admits, so no launcher may be granted it", i+1)
			}
			// Neither prefix: not a name of the grammar at all, so nothing here
			// can say who owns it, and what may be served under it is the
			// resolution's answer (DS3.3).
			continue
		}
		if parsed.ID != "" && parsed.ID != owner {
			return errors.New("a per-user credential may be granted only by the account that owns it")
		}
	}
	return nil
}

// grantCaller is a verified caller of the grant route: the two facts the two
// routes key on (the address an audit line writes and the numeric account id a
// slip's owner is written from) plus the PRINCIPAL the slip records (R7).
//
// It is LauncherCaller plus the principal rather than a second reading of it,
// because the principal is not derivable from either of the two: a person's
// account id is a Google subject and a service account's is its address, and
// which of the two a caller is is the fact the pool it is served from turns on
// (D3181, D2976) — so the class is carried, and it is carried as C2 already
// names it (routing.Principal).
type grantCaller struct {
	// Account is the verified address, as LauncherCaller's is: the "who" an
	// audit line writes.
	Account string
	// Sub is the verified Google account id, digits only, and it is EMPTY for
	// the one class that answers none (see launcherCaller).
	Sub string
	// Principal is what the slip records as the run's launcher: a person with
	// their own subject, or a service account with its address.
	Principal routing.Principal
}

// noGrantForTheOrchestrator is the refusal of a grant asked for by the resident
// orchestrator's class, which is the one class whose principal a slip cannot
// carry.
//
// It is a refusal of the GRANT and not of its caller, and it is the plan's own
// state rather than a gap: a slip's owner is the launcher's verified numeric
// account id (U2, and audit.go's validSlipOwner holds every line a slip serves
// to it), and VerifyOrchestrator answers the configured ADDRESS and no numeric
// subject at all — so a slip written for the daemon would be one every call of
// the run it starts fails to audit, which is a run that dies at its first model
// call. The daemon does not launch runs of its own either (CS1: "a run with no
// slip … an orchestrator's launch has none"): it RE-ISSUES an attempt of its own
// record, and the launcher of such a run is the one the run's own grant names —
// which is what PR3 grants, against the record it wrote, and the seam this
// refusal leaves open.
const noGrantForTheOrchestrator = "a slip records the launcher it serves as the launcher's own account id, and the orchestrator's class names an address instead of one — a resumed run's slip is granted from the service's own record of the paused run's grant"

// launcherCaller verifies the caller of the grant route, which is the three
// classes the identity routes admit and no others: a person on the
// installation's roster (VerifyLauncher), one of its roster service accounts
// (VerifyServiceAccountLauncher, LM3, D3123) and the resident
// orchestrator (VerifyOrchestrator, E16).
//
// The three are the same three the ticket route's TicketCaller asks, in the same
// order and for the same reason: the lists are disjoint — a person's address
// cannot be a service account's, and NewAuthenticator refuses an address that is
// both a roster service account and the orchestrator — so exactly one class can
// admit a caller, and the order decides only how many entry points are tried
// before the refusal. Each class's own refusal (ErrUnverifiedJob, the code every
// class of this service writes for a caller that is not one of its own) means
// "not this class" and nothing more; anything else is the service's own state
// and stops the check, because a caller must not be told it is unadmitted
// because this service is unwell.
//
// The service-account class is asked through CallerClasses and not off the
// Verifier interface, which is C1's own file and does not carry that entry point
// yet — the arrangement identity_callers.go states, and the one the ticket route
// already makes. A construction that answers only the two the interface carries
// is not a construction this route can widen: the assertion failing answers the
// class's own refusal, which is the refusal the route gave before CS14, and
// never a silent admission.
//
// The principal each class yields is what the slip records (R7, plan constraint
// row 67): the person's own verified subject, a roster service account's
// address, or the daemon's address. A class that answers no numeric account id
// yields an empty Sub, which the route refuses — see noGrantForTheOrchestrator.
func (s *Server) launcherCaller(ctx context.Context, tok string) (grantCaller, error) {
	person, err := s.cfg.Auth.VerifyLauncher(ctx, tok)
	switch {
	case err == nil:
		return grantCaller{
			Account:   person.Account,
			Sub:       person.Sub,
			Principal: routing.Principal{Kind: routing.PrincipalPerson, ID: person.Sub},
		}, nil
	case !errors.Is(err, ErrUnverifiedJob):
		return grantCaller{}, err
	}
	classes, ok := s.cfg.Auth.(CallerClasses)
	if !ok {
		return grantCaller{}, err
	}
	service, err := classes.VerifyServiceAccountLauncher(ctx, tok)
	switch {
	case err == nil:
		return grantCaller{
			Account:   service.Account,
			Sub:       service.Sub,
			Principal: routing.Principal{Kind: routing.PrincipalServiceAccount, ID: service.Account},
		}, nil
	case !errors.Is(err, ErrUnverifiedJob):
		return grantCaller{}, err
	}
	daemon, err := classes.VerifyOrchestrator(ctx, tok)
	switch {
	case err == nil:
		return grantCaller{
			Account:   daemon,
			Principal: routing.Principal{Kind: routing.PrincipalServiceAccount, ID: daemon},
		}, nil
	case !errors.Is(err, ErrUnverifiedJob):
		return grantCaller{}, err
	}
	// No class admitted the caller, and the sentence names all three: it is the
	// answer a run's job account, a stranger's token and a `group:` entry all
	// get, and the fix — the roster it is not on, and the update that moves one —
	// is the installation's. No address is named here, because what the caller
	// presented is its own claim set (the package's no-echo rule), and the
	// refusal is still the class's own code, so the wire answer is unchanged.
	return grantCaller{}, fmt.Errorf("%w: a slip is granted to a person on this installation's roster, to one of its roster service accounts, or to the resident orchestrator; a roster change reaches this service at the next fugaro update", ErrUnverifiedJob)
}

// errPolicyUnreadable is the class of a refused grant whose pools could not be
// decided because the project's published policy could not be read: this
// service's state and not the launcher's request, answered as an unavailable
// service (StatusUnavailable) the way every other unreadable document here is.
//
// It is a sentinel rather than a status carried back because the reading is the
// same one this package gives every other unreadable document — an unreadable
// switch is not an off switch, an unreadable admission is not an empty one — and
// a roster that could not be read admits nobody (fail closed, D143). The
// launcher loses nothing it had: a grant it cannot get is a run that starts
// slip-less, and a credential only a slip serves is refused at the call (R9).
var errPolicyUnreadable = errors.New("the project's policy could not be read, so no pool can be resolved for this grant")

// errGrantNotInPool is the refusal of a grant naming a pool, or a credential a
// pool lists, that the launcher is neither the owner of nor allowed by. It names
// no value of the call — not the pool, not the credential, not the launcher —
// because the names of a grant are the caller's own bytes and this is the route
// where it hands over a list of them.
var errGrantNotInPool = errors.New("a pool and the credentials it lists are served to its owner and to the users it allows, and this launcher is neither (D1936)")

// errGrantNoPool is the refusal of a grant naming a pool the project's published
// policy does not hold: a `pool` source's pool, or a pool a launch named with
// `--pool` (D3176). It is a refusal of the REQUEST and not of the launcher — the
// name is one this installation has no pool to resolve, whoever is asking — and
// it is its own sentinel rather than errGrantNotInPool's, because the two are
// different facts with different fixes: an unpublished name is a launch that
// named a pool of another installation (or one an operator has not published
// yet), and a pool that does not serve the launcher is a pool that exists and is
// somebody else's.
//
// A grant naming one is refused rather than written down: the grant is what the
// run is served from, and a source naming a pool nothing can resolve is a source
// the run would reach with no credential behind it, or — worse, on the path a
// permissive reading would leave open — one the walk could resolve against
// whatever pool a later release happened to publish under that name.
//
// It names no value of the call — not the pool, not the launcher — for
// errGrantNotInPool's reason: the names of a grant are the caller's own bytes.
var errGrantNoPool = errors.New("no pool of the project's published policy has that name, so no such source could ever be served")

// grantPool holds a grant's names to the pools of the project's published
// policy, and answers the pool that decided it: the empty string for a grant no
// pool governs (what wants a slip that names no pool — a bundle entry's own
// name, or a runner that cannot reach the service at all), and otherwise the
// name of the first pool, in the policy's own order, that admitted a name of
// this grant (G8, D1936, R11).
//
// The decision is PER NAME, and that is the whole of the rule: a name is served
// only where some pool that GOVERNS THAT NAME serves the launcher. Two names
// under two pools are two questions, and a roster answers for its own names and
// for no others — a launcher served by the first pool alone is refused the
// grant that also asks for the second pool's name. It has to be that way round,
// because the slip's list is what the run is served from later (DS1.6): a name
// admitted here is a name that run will be handed, so a check that read "the
// launcher is served by SOME pool of this grant" would serve one person a
// credential another pool shares with somebody else.
//
// A SINGLE name several pools govern is the other direction, and there it is a
// union: a publisher that lists one credential in two pools has said both
// rosters may have it, so a launcher either of them serves is admitted. The union is
// per name and never across the names of one call, which is exactly the atom
// this loop iterates.
//
// A name that is governed and that no governing pool admits refuses the WHOLE
// grant rather than being dropped from it: a run that got some of what it asked
// for is a run that fails later over a credential it was told it would have. The
// refusal happens before anything is written.
//
// Both directions come from the same rule, and config.Pool.Serves is where it
// lives: an owner, an allowed user or a pool the migration found ownerless are
// served, and nobody else is (D1936's default is owner-only, D1942's marker is
// the exception).
//
// The roster is compared by the rule this package compares every address by
// (foldEmail, the launcher class's own fold): a pool spells its owner and its
// allowed users as addresses — the roster's key, since a sub is not something an
// operator can write down — and a service that compared the operator's spelling
// to the claim's byte for byte would admit nobody whose case differs (R10).
//
// It reads the policy per grant and never caches it: the roster is resolved when
// the slip is issued (G8), so a publisher's change is in effect at the next
// grant. The slip's own cache (R4) holds the BODY of an issued grant, which is
// exactly the decision this rule must not make once and keep.
//
// It decides two things about pools, in one read, because they are the two ways
// a grant names one:
//
//   - the NAMES of the credentials it asks for, per name, as above;
//   - the POOLS it names outright — the pool of a `pool` source of the chain and
//     the pool a launch named with `--pool` (CS14, D3176). Those are held to the
//     same rule (the launcher is the pool's owner or a user it allows) with one
//     answer more: a name the project publishes no pool under is refused
//     (errGrantNoPool), because a pool named by a grant is a pool the run's
//     chain will resolve, and one nothing resolves is a source the run cannot be
//     served from. Naming a pool therefore still never widens what a run may be
//     served from: the name is either a pool that serves this launcher or a
//     refusal.
func (s *Server) grantPool(ctx context.Context, req SlipGrantRequest, account string) (string, error) {
	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil {
		return "", errPolicyUnreadable
	}
	// This installation may have published no policy, and that defines no pool
	// (G7's installation that has adopted nothing yet, R9): no credential's name
	// is one a pool governs, and the grant is what it was before pools existed.
	// It is NOT the unreadable case above — nothing failed, and a document that
	// does not exist cannot name a pool to enforce — and the pools a grant NAMES
	// are refused by the empty list below, which is where the two cases part
	// company: an installation that has published nothing serves a release-behind
	// grant exactly as it always did, and can serve no source that names a pool.
	var pools []config.Pool
	if found && shared != nil {
		pools = shared.CraftPools
	}
	launcher := foldedAddress(account)
	admitted := ""
	for _, name := range req.Credentials {
		governed, served := false, false
		for _, pool := range pools {
			if !poolGovernsName(pool, name) {
				continue
			}
			governed = true
			if !foldedPool(pool).Serves(launcher) {
				continue
			}
			served = true
			if admitted == "" {
				admitted = pool.Name
			}
			break
		}
		if governed && !served {
			return "", errGrantNotInPool
		}
	}
	for _, name := range grantNamedPools(req) {
		pool, ok := publishedPool(pools, name)
		if !ok {
			return "", errGrantNoPool
		}
		if !foldedPool(pool).Serves(launcher) {
			return "", errGrantNotInPool
		}
	}
	return admitted, nil
}

// grantNamedPools is the pools a grant names OUTRIGHT, in the order this route
// decides them: the pool the launch asked for with `--pool` first, because it is
// the launch's own act, and then the pools of its chain's `pool` sources in the
// order the launch listed them.
//
// It is a reading of the request and not a second member: the pools are already
// on the request, and collecting them here is what keeps the rule one loop —
// every pool a grant names is held to the same two answers (it is published, and
// it serves this launcher) in one place, whatever member carried it.
func grantNamedPools(req SlipGrantRequest) []string {
	names := make([]string, 0, 1+len(req.Sources))
	if req.NamedPool != "" {
		names = append(names, req.NamedPool)
	}
	for _, source := range req.Sources {
		if routing.Kind(source.Kind) == routing.KindPool {
			names = append(names, source.Pool)
		}
	}
	return names
}

// publishedPool answers the pool of the published node with this name. The
// comparison is exact, as poolGovernsName's own is: a pool's name is the name
// its owner wrote and the one `--pool` and a table name, and the fold this
// package compares ADDRESSES by has no business renaming a pool.
//
// A name that matches nothing answers false, and the caller refuses it: the
// alternative reading — an unknown name governs nothing, so nothing is checked —
// is the one grantPool gives a CREDENTIAL's name (R9: a name no pool governs is
// served as it was), and it cannot be given to a pool's name, which is a name
// the run's chain will resolve rather than one that may simply not be pooled.
func publishedPool(pools []config.Pool, name string) (config.Pool, bool) {
	for _, pool := range pools {
		if pool.Name == name {
			return pool, true
		}
	}
	return config.Pool{}, false
}

// poolGovernsName reports whether a pool decides whether a launcher may have
// ONE name of a grant, which is the whole of "this pool authorises this name":
// the pool's OWN NAME, which is what a craft's `access` writes when a craft runs
// on a pool, or, for a project-shared name of the grammar, the credential that
// name holds where the pool lists it among its members (R11: sharing is a
// project-shared secret under a pool's allowed users, and a pool's members are
// the credentials it shares).
//
// It takes one name rather than the grant's list because the pool's authority is
// per name: the caller must ask this of every name it is deciding, and a first
// name a pool governs may not carry a second one past the pools that govern that
// second name (grantPool).
//
// The two halves are disjoint by construction and cannot be confused:
// config.ValidatePools refuses a name defined as both a credential and a pool, so
// a pool's own name is no member of any pool, and a member is no pool's name.
//
// A plain credential name that a pool happens to list is NOT looked up here. The
// policy's own decision about a craft credential — that `access` names it, that
// the bundle serves it — is the path that serves it today, and a grant naming it
// directly names no pool (R9: the path that names no pool keeps serving what it
// served). What a pool does govern is the pool itself and the shared secrets
// under it.
func poolGovernsName(pool config.Pool, name string) bool {
	if name == pool.Name {
		return true
	}
	parsed, err := secretname.Parse(name)
	if err != nil || !parsed.Shared() {
		return false
	}
	return slices.Contains(pool.Members, parsed.Name)
}

// foldedAddress is an address in the shape this service compares addresses in:
// the launcher class's own fold, and the empty string for anything it cannot
// fold, which no roster can name and which therefore matches no pool that states
// an owner or an allowed user (the printable-ASCII rule of foldEmail; a
// legacy-open pool is served whoever asks, and Serves decides that itself).
func foldedAddress(email string) string {
	folded, _ := foldEmail(email)
	return folded
}

// foldedPool is a pool read in the same shape, so that config.Pool.Serves — the
// one rule that says who a pool serves (D1936, D1942) — is applied to addresses
// both of which are folded, and not restated here with a second comparison.
//
// It copies: the allowed users are compared through a new slice, so the decoded
// policy the service holds is never rewritten by a read of it. An address this
// service cannot fold is dropped rather than compared, because it names nobody
// it could ever admit — a roster entry is the operator's own text, and an entry
// that is not an address is one no verified launcher can match.
func foldedPool(pool config.Pool) config.Pool {
	pool.Owner = foldedAddress(pool.Owner)
	allowed := make([]string, 0, len(pool.AllowedUsers))
	for _, user := range pool.AllowedUsers {
		if folded, ok := foldEmail(user); ok {
			allowed = append(allowed, folded)
		}
	}
	pool.AllowedUsers = allowed
	return pool
}

// maxSlipRuntimeSeconds is maxSlipRuntime in the unit the launcher speaks, and
// it is the bound the clamp is applied IN: an ask is held to the cap before it
// is turned into a duration, because a duration is nanoseconds and a large
// enough ask multiplied first overflows int64 and wraps. The wrapped value is
// small or negative, so a clamp applied afterwards lets it through as a slip
// that is expired before it is claimed or one that never ends — the one reading
// a cap must not have, and the reason this constant exists at all.
const maxSlipRuntimeSeconds = int64(maxSlipRuntime / time.Second)

// clampRuntime holds a granted runtime to what a slip may serve (R10): a
// request above the cap is granted the cap rather than refused, and a request
// at or below it is granted what it asked for.
//
// It takes SECONDS rather than a duration for the reason the constant above
// gives: the cap has to be applied before the multiply, so a launcher asking
// for a runtime no duration can hold is answered the cap instead of an
// overflow. A value that is not positive cannot reach here — readGrantRequest
// refuses it — and one that did is answered 0 rather than the cap, so that a
// caller which lost that refusal gets a slip that is never live instead of one
// that outlives what was asked for.
func clampRuntime(seconds int64) time.Duration {
	switch {
	case seconds <= 0:
		return 0
	case seconds > maxSlipRuntimeSeconds:
		return maxSlipRuntime
	}
	return time.Duration(seconds) * time.Second
}

// auditGrant records one refused grant: the launcher's own act, written with
// the purpose and the credential routeAct gives the path, so this line and the
// refusals ServeHTTP answers cannot name the act two ways.
func (s *Server) auditGrant(ctx context.Context, caller, status string) error {
	purpose, credential := routeAct(SlipPath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseGrant answers one grant refusal and records it, in the shape refuseRead
// already has for the routes whose caller holds no Identity: one typed refusal
// to the caller, one line to the operator, and the caller's answer is the same
// whether or not the line lands (D143).
//
// It is reached only once the launcher IS verified — the refusals above that
// point are answered by refuse alone, with no line, because a line's "who" is
// what this service verified and there is none yet.
func (s *Server) refuseGrant(ctx context.Context, w http.ResponseWriter, caller string, status int, typ, message string) {
	if err := s.auditGrant(ctx, caller, typ); err != nil {
		// The service's own log line is where an operator reads an act it could
		// not record, for the reason refuseCall gives: the answer to the caller
		// is a refusal either way.
		s.log.Error("credsvc: a refused slip grant could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// refuseLauncher answers a refusal of the grant route's CALLER, which is that
// route's one refusal with no line: nothing has been verified yet, so there is
// no caller for a line to name. The class's own error type is the wire's
// (ErrUnverifiedJob is `unverified_job_identity`; ErrUnverified is
// `unverified_run_identity`, which this class never returns but the vocabulary
// maps; anything else is the state the token had to be checked against being
// unreadable). It is refuseVerifier's mapping and not a second one, because the
// two classes refuse for the same reasons.
func (s *Server) refuseLauncher(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnverifiedJob):
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
	case errors.Is(err, ErrUnverified):
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
	default:
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
	}
}

// refuseClaim answers one claim refusal and records it, in the shape refuseCall
// already has for the routes whose caller holds an Identity: one typed refusal
// to the run, one line to the operator, and the run's answer is the same
// whether or not the line lands (D143).
func (s *Server) refuseClaim(ctx context.Context, w http.ResponseWriter, id Identity, purpose string, status int, typ, message string) {
	if err := s.auditCall(ctx, id, purpose, "", "", typ); err != nil {
		s.log.Error("credsvc: a refused slip call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// writeSlipAnswer writes the grant's and the claim's answer, from the body
// already built. It is no-store: the grant's body carries the id of a record
// whose contents a run's calls are bound to, and the claim's carries the claim
// secret itself, so an intermediary that cached either would be handing the
// next caller an answer this service did not give it.
func writeSlipAnswer(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// The workspace's own grant and claim (WC4, R3, D2970, D3147): the two routes
// workspace mode serves for ITSELF. They are the other side of C1
// (internal/creds/person.go) and of C2 (scope_person.go), and the rules they
// carry are the ones docs/workspace-credentials/reconciliation.md states as R3,
// R4 and R8:
//
//   - **the class is the WORKSPACE's own** (R3, D2970). A member's admins can
//     forge their own project's launcher list (D1943), so a slip over the
//     workspace's money that they could mint would be one signed by the party
//     spending it. What admits a caller here is the workspace's roster, read
//     through the workspace's OWN database, and the class WC14 builds over it.
//   - **the roster is re-read as the node is** (R3): the copy the resolver holds
//     is served for workspaceNodeTTL and a re-read that failed refuses the call
//     rather than serving a stale one, so a person taken off the roster stops
//     being admitted within the same minute a member's reach moves in.
//   - **the binding is the run's, and then the person's** (R3, D3145 reopened):
//     the job account and the repository are held to the published node exactly
//     as they are for a member's grant, AND the person is held to the members
//     they are a launcher of — read from the workspace's own roster's `grants`
//     mark (D2818), which is the one record of that fact the workspace's
//     service can read, because it holds no grant on any member's own roster.
//   - **"no token held" is answered before the node is read** (R3, D3147): a
//     person who holds no token of the name they asked for is answered `held`
//     false and the run starts on the shared entries exactly as it did before
//     this plan, and they learn nothing about the workspace's membership table
//     from the route at all.
//   - **one class of refusal, naming nothing** (R3, D3147): every refusal OF
//     THE CALLER is ErrPersonGrantRefused's wire class, and no refusal names an
//     account, a repository or a person. A failure of the service's OWN state —
//     a roster, a node or a container that could not be read — is the state
//     class every other route answers a failed read with (D143), because a
//     launcher must be able to tell "the workspace refused me" from "the
//     workspace is not answering" (D3147's three answers).
//   - **the slip is the WORKSPACE's own** (R3): minted, written and read
//     through the workspace's own database, never a member's, and a call
//     carrying a member's slip is refused by the gate before a handler is
//     reached (R4, R18, F13, F40).

// personSlipClaimWindow is how long an UNCLAIMED workspace slip may be claimed,
// counted from the grant. It is this task's choice and it is pinned by
// TestAnUnclaimedWorkspaceSlipExpiresAtTheClaimDeadline.
//
// **Thirty minutes is the shape of the thing it bounds, and nothing else.** The
// window is the launcher's one hand-off — the id goes into the job's environment
// (creds.PersonSlipEnv) and the run claims it at bootstrap — so what it has to
// cover is a job that is queued, started and has read its environment: minutes,
// not hours. It is deliberately not `maxSlipRuntime`, which is the cap on how
// long a run may be SERVED: a grant nobody claimed in half an hour is a launch
// that did not happen, and leaving it claimable for a day would leave a record
// over the workspace's paid entries that a leaked id could still take. Nothing
// downstream is shortened by it — the run's own runtime is measured from its
// claim (creds.Slip.Expires), so a run that waits 29 minutes for a slot still
// gets every second it was granted.
const personSlipClaimWindow = 30 * time.Minute

// personSlipRuntime is how long a claimed workspace slip may serve, and it is
// the CAP rather than a value the caller asked for: C1's request carries four
// fields and no runtime (a person's grant asks whether the caller holds ONE
// token, not how long a run wants to run), so this is the service's own answer
// and it is the longest a slip may serve at all. A run that ends sooner ends
// sooner; issuance stops at this bound whatever the job does (R10).
const personSlipRuntime = maxSlipRuntime

// personGrantRefused is the ONE sentence every refused workspace grant is
// answered with (R3, D3147). It names no account, no repository and no person —
// not even which of the four things was wrong — so a person who holds a token
// learns nothing from a refusal beyond the fact of it, and the launcher has one
// answer to branch on.
const personGrantRefused = "the workspace did not grant this call a slip"

// personGrant answers the workspace's own grant (R3, D2970).
//
// The order is the rules, one step at a time:
//
//  1. the bearer and the launcher class (C2's Launchers), with no line before a
//     caller is verified — a line's "who" is what the class verified and there
//     is none yet.
//  2. the run-identity headers refused by name: a grant is asked for before the
//     run exists, exactly as the installation's is.
//  3. the body, read strictly (creds.ParsePersonGrantRequest) and registered
//     with the call's scrub, so the launcher's own token reaches no log.
//  4. the operator's stop switches, read HERE because this route is answered
//     ahead of the shared parse: a stopped workspace writes no new record.
//  5. **whether the caller holds the token they asked about**, and this is the
//     step that comes before the workspace's membership table is read at all
//     (R3): a container that is not there — none, or one with no version — is
//     the `held:false` ANSWER and not a refusal (D3147, §11 finding 5), and a
//     read that FAILED is the state class, because answering "not held" from a
//     Secret Manager that was unreachable would start a run on the shared
//     entries while the person's own token sat there (D143's fail-closed rule).
//  6. the roster, re-read as the node is (R3): the verified address must be a
//     `human` of it, and the person must be a launcher of the member the job
//     account belongs to (D3145 reopened — the `grants` mark below).
//  7. the job account and the repository held to the published node (R3, R19):
//     the account is one the node names, and the repository is one that
//     account's own row declares. It is the member's grant's own binding, made
//     against the same table.
//  8. the slip, written into the WORKSPACE's own store (ps.Slips), with the
//     claim deadline this file pins and the person's folded address on it.
//
// Every terminal step writes exactly one line: an audit line for a refusal, the
// service's own line for a grant and for a `held:false` answer — the same shape
// the installation's grant and claim have, and for the same reason (a StatusOK
// audit line requires a credential and a grant issues none, audit.go).
func (s *Server) personGrant(ctx context.Context, w http.ResponseWriter, r *http.Request, ps personScope) {
	tok, err := bearerFromHTTP(r)
	if err != nil {
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := ps.Launchers().VerifyLauncher(ctx, tok)
	if err != nil {
		// The class's own refusal, answered as the installation's grant answers
		// its launcher class's and with no line: nothing has been verified.
		s.refuseLauncher(w, err)
		return
	}
	if r.Header.Get(HeaderRunIdentity) != "" || r.Header.Get(HeaderRunJob) != "" {
		// A run identity on a grant is the shape C1 says this route never sees,
		// and it is answered as the route's ONE class and not with a refusal
		// that says which header it was (D3147's "and nothing finer").
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	req, err := creds.ParsePersonGrantRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent, and it is
		// the route's one class: a launcher reads the shape mistake on its own
		// side (the parse is C1's, and the launcher that wrote the body has it
		// too).
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, req.Slug)
	if err != nil {
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	// Step 5: the container, by its exact name and by nothing else. It is read
	// BEFORE the roster and before the node, which is R3's whole ordering: a
	// person who holds no token learns nothing at all from this route.
	held, err := s.personTokenHeld(ctx, caller.Sub, req.Name)
	if err != nil {
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the personal credential could not be read")
		return
	}
	if !held {
		// The ANSWER, not a refusal (R8, D3147): the run starts on the shared
		// entries exactly as it did before this plan, and the launcher prints
		// nothing. The line is the service's own, and it is what tells an
		// operator that a person asked and held nothing.
		s.log.Info("credsvc: a workspace grant found no token",
			"launcher", caller.Account, "repository", req.Slug, "run", req.Run)
		s.writePersonGrant(w, creds.PersonGrant{Held: false})
		return
	}
	// Step 6a: the roster's `human`. The address the class verified is the
	// roster's own key (F11: the roster is keyed by address), and the fold is
	// the one the class's own comparison uses.
	roster, found, err := ps.Roster(ctx)
	if err != nil {
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the workspace's roster could not be read")
		return
	}
	address := foldedAddress(caller.Account)
	if !found || !rosterListsHuman(roster, address) {
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	// Step 7: the job account and the repository, held to the node the resolver
	// has already admitted callers against.
	member, repos, err := ps.Member(ctx, req.Account)
	if err != nil {
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the workspace's published node could not be read")
		return
	}
	if member == nil {
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	if _, ok := repos[req.Slug]; !ok {
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	// Step 6b: the person held to the members they are a launcher of (D3145
	// reopened). The record is the workspace's OWN roster's `grants` mark —
	// what the workspace wrote into that member's roster (D2818) — because the
	// workspace's service holds no grant on a member's own roster and could not
	// read the grant itself even if it wanted to (R8).
	if !rosterGrantsLauncher(roster, member.Project, address) {
		s.refusePersonGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, personGrantRefused)
		return
	}
	id, err := newSlipID()
	if err != nil {
		s.log.Error("credsvc: a slip id could not be drawn", "error", err)
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	personalName, err := secretname.User(caller.Sub, req.Name)
	if err != nil {
		// The class verified a `sub` of Google's own shape (maxLauncherSubDigits)
		// and C1 refused a name that is not this plan's, so this is a wiring
		// mistake rather than a caller's, and the refusal says nothing of it.
		s.log.Error("credsvc: a personal name could not be built", "error", err)
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	deadline := s.now().Add(personSlipClaimWindow)
	sl := creds.Slip{
		ID:              id,
		Owner:           caller.Sub,
		Launcher:        routing.Principal{Kind: routing.PrincipalPerson, ID: caller.Sub},
		Slug:            req.Slug,
		Run:             req.Run,
		ExpectedAccount: req.Account,
		Allowed:         []string{personalName},
		OwnerAddress:    address,
		ClaimDeadline:   &deadline,
		MaxRuntime:      personSlipRuntime,
	}
	if err := ps.Slips().Create(ctx, sl); err != nil {
		s.log.Error("credsvc: a workspace slip could not be written", "slip", id, "run", req.Run, "repository", req.Slug, "error", err)
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	// The grant is recorded as one audit line (R3, WC4): the launcher's own act,
	// written with the purpose routeAct gives the path and no credential, because
	// a grant issues none and reads no bundle entry (audit.go's
	// credentiallessPurpose). The line names the verified launcher and nothing
	// of what the launcher asked for — the account, the repository and the run
	// are the caller's own bytes — which is the shape every refusal of this route
	// is written in, so the grant and its refusals are one class of line.
	if err := s.auditGrant(ctx, caller.Account, StatusOK); err != nil {
		// A line that cannot be recorded is not an answer the launcher gets:
		// refuseGrant's rule (D143), and the refusal it answers is the state
		// class, because the service's own record of the act is what failed.
		s.log.Error("credsvc: a workspace grant could not be audited", "error", err)
		s.refusePersonGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the grant could not be recorded")
		return
	}
	// The service's own line beside it is where an operator reads which person
	// asked for which run, and the slip's id.
	s.log.Info("credsvc: a workspace slip was granted",
		"slip", id, "run", req.Run, "repository", req.Slug, "account", req.Account,
		"launcher", caller.Account, "deadline", deadline)
	s.writePersonGrant(w, creds.PersonGrant{Held: true, Slip: id})
}

// personGrant answers the grant's body, and it is writeSlipAnswer with C1's
// answer shape around it: the same no-store rule, for the same reason (the body
// carries the id of a record a run's calls are bound to).
func (s *Server) writePersonGrant(w http.ResponseWriter, g creds.PersonGrant) {
	body, err := json.Marshal(g)
	if err != nil {
		s.log.Error("credsvc: the workspace grant's answer could not be built", "error", err)
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, "the grant's answer could not be built")
		return
	}
	writeSlipAnswer(w, body)
}

// personTokenHeld answers whether the caller holds a token of one personal name
// at the workspace: the per-secret container the intake writes, read by its
// EXACT name (secretname.User — `fugaro_u_<sub>_<name>`) and by nothing else,
// out of the Secret Manager project this deployment already reads its own bundle
// with (perSecrets).
//
// **The one read of this route that is deliberately not narrow.** Nothing here
// answers a VALUE: the container's newest version is read because Secret Manager
// has no cheaper way to ask whether a container carries one — a container with
// no version and no container at all are the same absence to this route, which
// is R6's `found=false` — and the value is dropped where it is read. It is never
// returned, never registered with the call's scrub (there is nothing of it left
// to scrub) and never written anywhere: the answer is the fact of it and nothing
// else, which is the whole of C1's "names, and never values" held here.
//
// A deployment that reads no container at all — every hermetic fixture whose
// bundle source is not Secret Manager's, and any installation reading its
// document another way — is a process in which no person holds a token: the
// answer is false, which is the state every call of this half must fall back to
// (R8), and not an error.
func (s *Server) personTokenHeld(ctx context.Context, sub, name string) (bool, error) {
	secrets := s.perSecrets()
	if secrets == nil || secrets.Source == nil {
		return false, nil
	}
	id, err := secretname.User(sub, name)
	if err != nil {
		return false, err
	}
	_, found, err := secrets.Source.Value(ctx, id)
	if err != nil {
		return false, err
	}
	return found, nil
}

// rosterListsHuman reports whether the roster lists the folded address as a
// person who is a `human` (R3). It is asked of the folded address because the
// roster is keyed by address and the class's own comparison is folded
// (foldedAddress), and a machine of the roster is deliberately not admitted:
// D2806 gives a machine the workspace node and no member roster, and R3's class
// is people.
func rosterListsHuman(roster *workspace.Roster, address string) bool {
	if roster == nil {
		return false
	}
	for _, p := range roster.People {
		if p.Kind == workspace.KindHuman && foldedAddress(p.Address) == address {
			return true
		}
	}
	return false
}

// rosterGrantsLauncher reports whether the workspace's roster records a grant
// into one member for one person (D2818's mark): the record of "this person is
// a launcher of that member", which is the whole of what holds a workspace grant
// to the members the person launches for (D3145 reopened).
//
// **It is the mark and not the member's own roster, and the difference is
// stated rather than papered over.** The real grant lives in the member's own
// node (internal/access), and this service holds no grant to read one (R8) — it
// opens a member's database read-only, for that member's own policy and
// outcomes, and a member's roster is not among the nodes it reads. What the
// workspace CAN read is its own record of what it wrote, which is the roster's
// `grants` mark, and it is the fact this decision turns on: a grant whose mark
// was rewritten by the member's admins is a drift the workspace's `status`
// already reports, and refusing here would refuse every run of a member whose
// copy had gone — which the mark's own contract says is the comparison's
// business and not a reader's.
//
// Every role counts: `viewer`, `operator` and `admin` are all grants the
// workspace wrote, and R3's class is "the members the person is a launcher of"
// and not "the members the person administers".
func rosterGrantsLauncher(roster *workspace.Roster, project, address string) bool {
	if roster == nil {
		return false
	}
	for _, g := range roster.Grants {
		if g.Project == project && foldedAddress(g.User) == address {
			return true
		}
	}
	return false
}

// personClaim answers one call of the workspace's claim route (R3): the two
// forms of the installation's claim, over the WORKSPACE's own slip store and
// under the workspace's own headers (creds.PersonSlipHeader,
// creds.PersonSlipClaimHeader), because the two slips a run holds travel in two
// sets of names (F13).
//
// The route's own admission is the gate's: this handler is reached after the
// shared parse verified the run identity and the job's token, after the pairing
// fixed the repository, and after the stop switches were read — so a stopped
// workspace claims nothing, and the record decides the rest. What this handler
// adds to the installation's two forms is the roster: a slip whose owner is no
// longer a `human` of the workspace's roster is refused as not this run's, which
// is R3's removal rule landing on the record rather than on the grant.
func (s *Server) personClaim(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, req Request, ps personScope) {
	purpose, _ := routeAct(SlipClaimPath)
	slipID, err := oneHeader(r, creds.PersonSlipHeader)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if !printableHeaderValue(slipID, maxSlipIDBytes) {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"the "+creds.PersonSlipHeader+" header is not one slip id")
		return
	}
	secret, presented, err := personSlipSecretFromHTTP(r)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	cl, err := readCall(r, []string{req.RunIDToken, req.JobIDToken, secret})
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	claim, err := readClaimRequest(cl.body)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// The ownership question comes FIRST and on its own (C2's OwnsSlip, R4,
	// F13): a call presenting a slip this service never granted — a member's,
	// another workspace's, this workspace's own and since ended — is refused as
	// presenting a slip that is not this service's, before the record is read
	// for its binding, its claim or its allowance.
	owned, err := ps.OwnsSlip(ctx, slipID)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the slip could not be read")
		return
	}
	if !owned {
		s.refuseClaim(ctx, w, id, purpose, StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns)
		return
	}
	now := s.now()
	if presented {
		// The reading form: the run already holds the slip and says so.
		sl, found, err := ps.Slips().Slip(ctx, slipID)
		if err != nil {
			s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the slip could not be read")
			return
		}
		if !found || !slipIsThisRuns(sl, id) || !sl.ClaimMatches(secret) || !sl.Live(now) {
			s.refuseClaim(ctx, w, id, purpose, StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns)
			return
		}
		if refused := s.personStillOnTheRoster(ctx, w, id, purpose, ps, sl); refused {
			return
		}
		body, err := json.Marshal(SlipClaimResponse{
			ExpiresAt:         sl.Expires(),
			MaxRuntimeSeconds: int64(sl.MaxRuntime.Seconds()),
		})
		if err != nil {
			s.log.Error("credsvc: the claim's answer could not be built", "error", err)
			s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the claim's answer could not be built")
			return
		}
		s.log.Info("credsvc: a workspace slip was read back",
			"slip", slipID, "run", id.Run, "repository", id.Slug, "account", id.JobAccount, "execution", claim.Execution)
		writeSlipAnswer(w, body)
		return
	}
	// The record is read BEFORE the claim so that the roster can be asked about
	// the person it belongs to: a claim must not hand a claim secret to a run
	// whose owner has left, and the store's own cas cannot ask the question.
	before, found, err := ps.Slips().Slip(ctx, slipID)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the slip could not be read")
		return
	}
	if !found {
		s.refuseClaim(ctx, w, id, purpose, StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns)
		return
	}
	if refused := s.personStillOnTheRoster(ctx, w, id, purpose, ps, before); refused {
		return
	}
	claimed, claimSecret, err := ps.Slips().Claim(ctx, slipID, creds.Run{Slug: id.Slug, ID: id.Run}, id.JobAccount, now)
	if err != nil {
		status, typ, message := StatusUnavailable, ErrorStateUnavailable, "the slip could not be claimed"
		switch {
		case errors.Is(err, creds.ErrNoSlip), errors.Is(err, creds.ErrSlipIdentity),
			errors.Is(err, creds.ErrSlipClaimed), errors.Is(err, creds.ErrSlipExpired):
			status, typ, message = StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns
		}
		s.log.Error("credsvc: a workspace slip could not be claimed", "slip", slipID, "run", id.Run, "repository", id.Slug, "error", err)
		s.refuseClaim(ctx, w, id, purpose, status, typ, message)
		return
	}
	body, err := json.Marshal(SlipClaimResponse{
		ClaimSecret:       claimSecret,
		ExpiresAt:         claimed.Expires(),
		MaxRuntimeSeconds: int64(claimed.MaxRuntime.Seconds()),
	})
	if err != nil {
		s.log.Error("credsvc: the claim's answer could not be built", "slip", slipID, "run", id.Run, "error", err)
		s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the claim's answer could not be built")
		return
	}
	s.log.Info("credsvc: a workspace slip was claimed",
		"slip", slipID, "run", id.Run, "repository", id.Slug, "account", id.JobAccount, "execution", claim.Execution)
	writeSlipAnswer(w, body)
}

// personStillOnTheRoster refuses a claim whose slip's owner has left the
// workspace's roster (R3), and reports whether it refused.
//
// **The question is asked of the RECORD's address and of the roster as
// re-read**, which is why the address is on the slip at all (creds.Slip's
// OwnerAddress, F11): the claim holds the run's identity and not the person's,
// so the record is the only thing that can say whose the slip is, and the roster
// is re-read as the node is rather than trusted to a copy taken at the grant.
// The refusal is the one sentence every refusal the record makes is given
// (slipNotThisRuns), so a caller cannot tell "removed" from "not yours".
//
// A roster that could not be read does NOT refuse this way: it is the service's
// own state and it answers the state class (D143, D2983), because a run whose
// person is still there must not be stopped by a roster nobody could read.
func (s *Server) personStillOnTheRoster(ctx context.Context, w http.ResponseWriter, id Identity, purpose string, ps personScope, sl creds.Slip) bool {
	roster, _, err := ps.Roster(ctx)
	if err != nil {
		s.refuseClaim(ctx, w, id, purpose, StatusUnavailable, ErrorStateUnavailable, "the workspace's roster could not be read")
		return true
	}
	if !rosterListsHuman(roster, sl.OwnerAddress) {
		s.refuseClaim(ctx, w, id, purpose, StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns)
		return true
	}
	return false
}

// personSlipSecretFromHTTP reads the claim secret a workspace call presents,
// under the workspace's own header (creds.PersonSlipClaimHeader). It is
// slipSecretFromHTTP for the other set of names, and the two are apart on
// purpose: a member's claim secret presented at the workspace's claim is a call
// of a shape this route does not read (F13).
func personSlipSecretFromHTTP(r *http.Request) (secret string, presented bool, err error) {
	values := r.Header.Values(creds.PersonSlipClaimHeader)
	switch len(values) {
	case 0:
		return "", false, nil
	case 1:
		if !printableHeaderValue(values[0], maxHeaderTokenBytes) {
			return "", false, fmt.Errorf("credsvc: the %s header is not one claim secret", creds.PersonSlipClaimHeader)
		}
		return values[0], true, nil
	}
	return "", false, fmt.Errorf("credsvc: the call carries the %s header more than once", creds.PersonSlipClaimHeader)
}

// refusePersonGrant answers one refused workspace grant and records it, in
// refuseGrant's shape: one typed refusal to the launcher, one line to the
// operator, and the launcher's answer is the same whether or not the line lands
// (D143). It is reached only once the launcher IS verified, for refuseGrant's
// own reason.
func (s *Server) refusePersonGrant(ctx context.Context, w http.ResponseWriter, caller string, status int, typ, message string) {
	if err := s.auditGrant(ctx, caller, typ); err != nil {
		s.log.Error("credsvc: a refused workspace grant could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}
