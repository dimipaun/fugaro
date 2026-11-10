package credsvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
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
// The grant decides three things: WHO is asking (the launcher class, D1938),
// WHICH credentials the slip will permit (the owner rule of R7, checked here
// because this is the one place a whole run's set is written down), and how
// long the run may be served (R10's cap, kept here as a service constant). It
// decides nothing about pools: a `fugaro_p_` name is granted to any launcher,
// because whether a pool admits that person is a fact about the pool and the
// published policy and is DS3.2's check, made where the credential is actually
// resolved.
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
	// launcher IS that owner (R7).
	Credentials []string `json:"credentials"`
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
//     admission, and it is the reason the route is where it is in ServeHTTP. A
//     refusal here is typed and has no line — nothing is verified yet, so there
//     is no caller for a line to name.
//   - the run identity, refused by NAME if it was presented: a launcher holds
//     none and the run it is asking for does not exist yet, so a caller
//     presenting one is a call of a shape this route does not have. It is the
//     read mint's own check, and it is what keeps the class from being a second
//     way in for a run.
//   - the call's body, whose read is ALSO the registration of the launcher's own
//     token with the call-scoped scrub (readCall), exactly as the verify route
//     and the read mints do it.
//   - the owner rule (R7), before the slip is written: a name the launcher may
//     not be served is a request this contract does not read, and a refused
//     grant writes nothing at all.
//   - the id and the record. The id is drawn here rather than taken from the
//     call: an id a caller chose is an id two callers can collide on.
//
// What is deliberately NOT here: the policy, the bundle and the cascade. A
// grant mints nothing — it writes down what a run may later ask for — so there
// is no credential to read and no admission to consult.
func (s *Server) slipGrant(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyLauncher(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way the verify route answers a
		// verifier's and the read mints theirs: a typed refusal and no line.
		s.refuseLauncher(w, err)
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
	if err := checkGrantedNames(req.Credentials, caller.Sub); err != nil {
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
		Slug:            req.Slug,
		Run:             req.Run,
		ExpectedAccount: req.Account,
		Allowed:         append([]string(nil), req.Credentials...),
		MaxRuntime:      runtime,
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
	s.log.Info("credsvc: a slip was granted",
		"slip", id, "run", req.Run, "repository", req.Slug, "account", req.Account, "launcher", caller.Account)
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

// checkGrantedNames holds the credentials a grant names to the owner rule of
// R7: a per-user secret ID — fugaro_u_<id>_<name> — may be named only by the
// launcher whose verified Google account id is <id>.
//
// The rule applies to the names the GRAMMAR admits and to no others, and that
// is the whole of its scope: a project-shared name (fugaro_p_) is granted to
// any launcher here, because whether a pool admits that person is a fact about
// the pool and its allowed_users and is DS3.2's check where the credential is
// resolved (R11, G8); and a name the grammar does not admit at all — a bundle
// entry's own name, such as a model credential's, or a per-secret name of
// another spelling — has no owner to compare, so there is none to check.
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
			// Not a name of the grammar: nothing here can say who owns it, and
			// what may be served under it is the resolution's answer (DS3.2).
			continue
		}
		if parsed.ID != "" && parsed.ID != owner {
			return errors.New("a per-user credential may be granted only by the account that owns it")
		}
	}
	return nil
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
