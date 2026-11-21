// identity_claim.go is the claim route (LM2, R2): the run's first call, which
// presents the ticket its launcher asked for and the nonce its runner drew,
// and is answered the custom token its identity is (R3).
//
// It is also where the two halves the identity routes share live: the ticket's
// own spelling (joinIdentityTicket, splitIdentityTicket — the issuer writes it
// and the claim reads it back) and the refusal helpers the claim and the probe
// both answer through. The probe's handler and the runs' signer are in
// identity_sign.go.

package credsvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// identityTicketSeparator is what a ticket's three parts are joined with. It is
// '/' because budget.Key escapes it, so the separator can appear nowhere else
// in the ticket: the split is then unambiguous whatever a slug or a run id
// holds.
const identityTicketSeparator = "/"

// identityTicketSecretBytes is the entropy of a ticket's secret: thirty-two
// bytes from crypto/rand, the width a one-time secret needs to be unguessable
// in practice. The ticket is the whole of what stands between a job account
// that reads a peer execution's environment and that peer's identity (the
// residual R5 states), so its secret is drawn at the width of a key rather
// than at a slip id's.
const identityTicketSecretBytes = 32

// maxIdentityTicketBytes bounds a presented ticket: the two escaped names and
// the secret, with room to spare, and not a byte more. A longer value is a
// request this contract does not read rather than one to look up in the
// database, which is the rule the slip's id is held to (maxSlipIDBytes).
const maxIdentityTicketBytes = 512

// maxIdentityNonceBytes bounds a presented nonce. The value is the runner's
// own (LM7) and this service keeps nothing but its digest, so what the bound
// keeps out is a body's worth of text hashed into a record and a nonce that is
// no nonce at all.
const maxIdentityNonceBytes = 128

// identityTicketLife is how long a ticket stays claimable (R2): a ticket, its
// nonce and the job's identity token together are a run's token for that long
// and no longer.
//
// The value is derived and not chosen, because two bounds meet here. The
// ticket must outlive the wait between the execution's creation and its
// container's start, and that wait is exactly what the run's own identity is
// padded for: token.QueueAllowance is the hour a run may queue before it
// starts, and token.Slack is the little the clocks may differ by, both of them
// added to fx by token.ExpiryFX. And the ticket must not outlive the identity
// it buys: a record's fx is at least the launch, the run's own timeout, this
// same allowance and this same slack (LM1 writes it, R4), so this life is
// shorter than the run's identity by the run's own timeout and by nothing
// else. A longer one would be a ticket that outlives the token it is exchanged
// for — the claim's own signing refuses a record whose fx has passed, so it
// would buy nothing and only widen the window R5 states.
//
// It is the issuer's constant and the claim's: LM1 answers it as the ticket's
// ExpiresAt (rec.At + identityTicketLife) and this route holds the same window
// open, because a record carries no expiry of its own (identitywire.Record) —
// one spelling of the life, read by both, is what keeps the launcher's answer
// and the claim's fence from disagreeing.
const identityTicketLife = token.QueueAllowance + token.Slack

// joinIdentityTicket builds one run's ticket (LM1's mint, R3): the record's
// own path with the family's prefix dropped — the escaped slug, the escaped
// run, and a secret drawn from crypto/rand — joined by the separator above.
//
// The spelling is the claim's as much as the issuer's, and that is why it is
// one function here: the claim reads the slug and the run off the ticket
// (splitIdentityTicket), because the record is read by that path and by
// nothing else (IssuanceStore.Get), and a mint that spelled the ticket another
// way would be a ticket no run could claim.
//
// The secret is base64url without padding: no '/' and no '+', so the third
// part cannot be mistaken for a separator.
func joinIdentityTicket(slug, run string) (string, error) {
	if !token.ValidSegment(slug) {
		return "", errors.New("credsvc: a ticket's slug is not one path segment")
	}
	if !token.ValidSegment(run) {
		return "", errors.New("credsvc: a ticket's run is not one path segment")
	}
	secret := make([]byte, identityTicketSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("credsvc: no ticket secret could be drawn: %w", err)
	}
	return budget.Key(slug) + identityTicketSeparator + budget.Key(run) + identityTicketSeparator +
		base64.RawURLEncoding.EncodeToString(secret), nil
}

// splitIdentityTicket reads a presented ticket back into the slug and the run
// it is for, and it is the claim's whole read of it: what the claim then does
// with the two is a lookup and a comparison against the record (identitywire.Digest
// of the ticket is the record's own hash, so a ticket that parses and is not
// the record's is refused like one that does not parse).
//
// It is false for anything that is not this contract's ticket: a value too
// long to be one, one that does not split into exactly three parts, and one
// whose two names are not the path segments a record is written under
// (token.ValidSegment — the same rule the claims themselves are held to). A
// false answer names no reason to the caller and no part of the value to
// anyone: what the refusal says is that the ticket is not one this service
// issued.
func splitIdentityTicket(ticket string) (string, string, bool) {
	if !printableHeaderValue(ticket, maxIdentityTicketBytes) {
		return "", "", false
	}
	parts := strings.Split(ticket, identityTicketSeparator)
	if len(parts) != 3 || parts[2] == "" {
		return "", "", false
	}
	slug, err := budget.Unkey(parts[0])
	if err != nil || !token.ValidSegment(slug) {
		return "", "", false
	}
	run, err := budget.Unkey(parts[1])
	if err != nil || !token.ValidSegment(run) {
		return "", "", false
	}
	return slug, run, true
}

// identityListed reports whether the verified account is one of the addresses a
// list names, folded as every list of people in this service is (foldEmail):
// the spelling a deployment configured is the one an audit line writes, and
// which of the spellings matched is not a fact any answer carries.
func identityListed(list []string, account string) bool {
	folded, ok := foldEmail(account)
	if !ok {
		return false
	}
	for _, entry := range list {
		if f, ok := foldEmail(entry); ok && f == folded {
			return true
		}
	}
	return false
}

// jobVerifier is the half of the service's own authenticator the claim reads:
// the job class's check, which answers the verified account of a caller that
// presents its own Google ID token — the job's, the review job's or a shared
// project job's — and the profile that account serves beside it.
//
// It is a narrow interface over the method the Authenticator already has
// (auth.go's verifyJob), and not a second verification: the claim's caller is
// the job account and nothing else of the call is read from it, so what this
// route needs is exactly the class's own answer. The alternative — a job name
// — is deliberately not asked for: a job's ID token carries no job name, and a
// header the caller writes would be text a caller could satisfy by writing it
// (verifyJob's own reason for deciding the class from the verified email).
type jobVerifier interface {
	verifyJob(ctx context.Context, tok string, now time.Time) (string, string, JobClass, error)
}

// errNoJobVerifier is the service's own configuration failure: the claim's
// caller cannot be verified at all, because the authenticator wired into this
// service is not one that answers the job class. It is answered as this
// service's failure and never as "not served", which is the answer the CLI
// bridges on (R7): a service whose signer and store are configured has an
// issuer, and telling a launcher otherwise would have it sign a run's identity
// locally against an installation that has moved the grant.
var errNoJobVerifier = errors.New("credsvc: the configured authenticator does not answer the job class")

// verifyClaimCaller verifies the claim's caller and answers the job account it
// came from, which is the only fact this route reads off the call (R2).
func (s *Server) verifyClaimCaller(ctx context.Context, tok string) (string, error) {
	v, ok := s.cfg.Auth.(jobVerifier)
	if !ok {
		return "", errNoJobVerifier
	}
	account, _, _, err := v.verifyJob(ctx, tok, s.now())
	if err != nil {
		return "", err
	}
	return account, nil
}

// identityClaim answers one claim (LM2, R2, R3): the run's first call, which
// presents the job's own Google ID token on the Authorization bearer and the
// ticket and nonce in its body, and is answered the custom token signed as the
// runs' signer with the claims the record holds and the uid the record's path
// derives.
//
// Nothing of the call reaches the token: the run, the repository, the uid and
// the claims are the record's (R2, R3), and the account is the verified one
// checked against the record's (R4). A body that names any of them is refused
// by the strict decode, because the wire has no field for one.
//
// The ticket is claimed ONCE (R5): the store's Claim is the fence, and it is
// taken before the signing so that a claim whose answer was lost — or whose
// signing failed — is retried by the same nonce rather than refused. Every
// refusal past the caller's verification writes exactly one audit line, and
// the nonce is in no line of either kind.
func (s *Server) identityClaim(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.cfg.Identity.Signer == "" {
		s.refuseIdentityNotServed(w)
		return
	}
	if s.cfg.Identity.Store == nil {
		// A deployment with a signer and no store is a configuration error and
		// not a store that answers "no record": a claim read against one would
		// be a claim nobody could check (the scope stub's own rule, StubIdentityScope).
		s.log.Error("credsvc: the identity claim has no issuance store configured")
		s.refuseIdentityCall(ctx, w, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the service holds no issuance record to check this claim against")
		return
	}
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified.
		s.refuse(w, StatusUnverified, identitywire.RefusalCallerNotAdmitted, err.Error())
		return
	}
	account, err := s.verifyClaimCaller(ctx, tok)
	if err != nil {
		s.refuseIdentityCaller(w, err)
		return
	}
	// The claim is the run's FIRST call, so it carries no run identity: the run
	// it is for is the record's and not a caller's to name (R2). A call that
	// presents one is refused rather than served under a run this route would
	// have to ignore — the fence the slip's grant keeps for the same reason.
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseIdentityCall(ctx, w, "", "", StatusUnverified, identitywire.RefusalIdentityRefused,
			"this route takes no run identity: a claim is the run's first call and the run it is for is the ticket's, so a caller presenting one is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseIdentityCall(ctx, w, "", "", StatusUnverified, identitywire.RefusalIdentityRefused,
			"this route takes no run identity: a claim is the run's first call and the job it is for is the account's, so a caller naming one is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		// A body this service will not hold is a refused REQUEST and not an
		// unavailable service, which is the reading the slip's grant gives the
		// same error. The read is also what registers the job's own token with
		// the call's scrub.
		s.refuseIdentityCall(ctx, w, "", "", StatusRequestRefused, identitywire.RefusalIdentityRefused, requestRefused+err.Error())
		return
	}
	var req identitywire.ClaimRequest
	if err := identitywire.DecodeStrict(cl.body, &req); err != nil {
		// The refusal names the field and never what the call sent.
		s.refuseIdentityCall(ctx, w, "", "", StatusRequestRefused, identitywire.RefusalIdentityRefused, requestRefused+err.Error())
		return
	}
	if req.Nonce == "" || len(req.Nonce) > maxIdentityNonceBytes {
		// A nonce's shape beyond this is the runner's (LM7) and not this
		// service's: what the route needs of it is that the same value again is
		// recognised as the same claim, which any bytes carry. What it refuses
		// here is a claim that carries no nonce at all — the retry rule has
		// nothing to recognise — and one too long to be a value a runner drew.
		s.refuseIdentityCall(ctx, w, "", "", StatusRequestRefused, identitywire.RefusalIdentityRefused,
			requestRefused+"the claim carries no nonce this service reads: the nonce is the value the runner draws and keeps for as long as it retries")
		return
	}
	slug, run, ok := splitIdentityTicket(req.Ticket)
	if !ok {
		s.refuseIdentityCall(ctx, w, "", "", StatusRequestRefused, identitywire.RefusalIdentityRefused,
			requestRefused+"the ticket is not one this service issued: a ticket names the run it is for")
		return
	}
	rec, found, err := s.cfg.Identity.Store.Get(ctx, slug, run)
	switch {
	case err != nil:
		// The store's own failure is logged and never answered: it names the
		// path, and a refusal names the field and never what the call sent. The
		// run the ticket names is not written on this line either, because no
		// record was read: it is still the caller's text.
		s.log.Error("credsvc: the issuance record could not be read", "run", run, "repository", slug, "error", err)
		s.refuseIdentityCall(ctx, w, "", "", StatusUnavailable, ErrorStateUnavailable, "the issuance record could not be read")
		return
	case !found:
		// An unknown ticket, refused with one line and no run on it: the two
		// names in the ticket are the caller's text until a record at that path
		// says otherwise, and a line that carried them would let any job
		// account of this installation write run ids into the operator's audit.
		s.refuseIdentityCall(ctx, w, "", "", StatusUnverified, identitywire.RefusalIdentityRefused,
			"the ticket is not one this service issued for a run: no issuance record is at the path it names, so the run it belongs to was never launched here")
		return
	}
	now := s.now()
	// The ticket's own life, before every other check of the record and before
	// the fence: R2 says the same-nonce answer holds only for the ticket's own
	// life, so a ticket past it is refused however right it is — a retry of a
	// claim this long after the launch is a retry the launch has moved past.
	if !now.Before(rec.At.Add(identityTicketLife)) {
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
			"the ticket has expired: a ticket, its nonce and the job's identity token together are a run's token for the ticket's life and no longer, and this one is past it — the run must be launched again")
		return
	}
	if rec.TicketHash != identitywire.Digest(req.Ticket) {
		// A re-issue by the same caller replaces the record's ticket and kills
		// the old one (R6), and this is where that is enforced: the digest on
		// the record is the only ticket that claims it.
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
			"the ticket is not this run's: a ticket is replaced when the same caller asks for the run again, and the one presented here is not the record's — the run must be launched again")
		return
	}
	if !strings.EqualFold(account, rec.ExpectedAccount) {
		// The fold is the service's own spelling of an address and not a
		// loosening: the account was verified by Google, and the record's is the
		// one the launch named and this service checked against the repository's
		// own job (R4, F12). Two spellings of one account are one account; two
		// accounts are never one.
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
			"the caller is not the run's job account: the claim must come from the account the launch named, so a run may not claim another job's identity")
		return
	}
	if rec.Claims.Slug != slug || rec.Claims.Run != run {
		// R3: the uid this service signs is derived from the record's PATH, and
		// the claims the record holds must be the ones that path names — the
		// record is a place this service's own database admins can write, so
		// this is the check that a record moved from one run's path to another
		// signs nothing.
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
			"the record's claims are not this run's: the uid this service signs is derived from the record's path, and the claims on it name another run")
		return
	}
	claim := identitywire.Claim{Account: account, At: now, NonceHash: identitywire.Digest(req.Nonce)}
	claimed, err := s.cfg.Identity.Store.Claim(ctx, slug, run, claim)
	if err != nil {
		switch {
		case errors.Is(err, identitywire.ErrClaimed):
			// R5's residual, detected and not prevented: the loser of a race is
			// refused here, with the one line that names the run, and the run it
			// was launched for halts on its own refusal.
			s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
				"the ticket was already claimed: a ticket is claimed once, and this claim is not the one that claimed it — the same nonce again is that claim's own retry, and a second attempt takes a new run id")
			return
		case errors.Is(err, identitywire.ErrNoRecord), errors.Is(err, identitywire.ErrAnotherCaller):
			// The store's two other decisions are the ticket route's. The claim
			// reads the record first and refuses on its own checks — the account,
			// the ticket's digest, the path — so either of them here is a record
			// that moved under this call, and the run has to be launched again.
			s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
				"the run's issuance record is not the one this claim read: it was replaced or removed under the claim, so the run must be launched again")
			return
		default:
			s.log.Error("credsvc: the issuance record could not be claimed", "run", run, "repository", slug, "error", err)
			s.refuseIdentityCall(ctx, w, run, slug, StatusUnavailable, ErrorStateUnavailable, "the claim could not be recorded")
			return
		}
	}
	signer, err := identitySignerFor(ctx, s.cfg.Identity.Signer)
	if err != nil {
		s.log.Error("credsvc: the runs' signer could not be built", "signer", s.cfg.Identity.Signer, "error", err)
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnavailable, ErrorStateUnavailable,
			"the service could not reach the account the runs' identities are signed as")
		return
	}
	// The signing is token.MintAt's and not this file's: the uid is derived from
	// the claims (token.Claims.UID) — which the check above has held to the
	// record's path — and every other claim is validated there (R4), so the
	// record cannot be signed into a token the identity provider would refuse.
	witness := &signerWitness{Signer: signer}
	custom, err := token.MintAt(ctx, witness, claimed.Claims, now)
	if err != nil {
		if witness.reached {
			s.log.Error("credsvc: the run's identity could not be signed", "run", run, "repository", slug, "signer", signer.Email(), "error", err)
			s.refuseIdentityCall(ctx, w, run, slug, StatusUnavailable, ErrorStateUnavailable,
				"the service could not sign the run's identity as the runs' signer")
			return
		}
		// The record's own claims, and not the signer: the error names the claim
		// and never a value of it (token's own rule).
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnverified, identitywire.RefusalIdentityRefused,
			"the record's claims are not ones this service may sign: "+err.Error())
		return
	}
	// The claim is recorded on the service's own line and not as an audit line:
	// a StatusOK line requires a purpose, a run and a job (checked), and this
	// act has no purpose in the vocabulary and no job — a job's ID token
	// carries no job name. This line is where an operator reads which run
	// claimed, from which account, and that it was signed as the runs' signer;
	// the nonce and the ticket are in neither this line nor any other.
	s.log.Info("credsvc: a run claimed its identity",
		"run", run, "repository", slug, "account", account, "signer", signer.Email())
	body, err := json.Marshal(identitywire.ClaimResponse{Token: custom, ExpiresAt: claimed.Claims.FX})
	if err != nil {
		// The answer is built before it is written, as the slip's is: an answer
		// this service cannot build is a refusal and not a half-written body.
		s.log.Error("credsvc: the claim's answer could not be built", "error", err)
		s.refuseIdentityCall(ctx, w, run, slug, StatusUnavailable, ErrorStateUnavailable, "the claim's answer could not be built")
		return
	}
	// The token is the claim's answer and the only signature this contract
	// returns, and the answer is no-store for the slip's reason: a cached one
	// would hand the next caller a run's identity (writeSlipAnswer).
	writeSlipAnswer(w, body)
}

// refuseIdentityNotServed answers a call to an identity route on a service with
// no issuer: the contract's own code (identitywire.RefusalNotServed) with
// StatusUnavailable — 503 and deliberately not the 404 a path this service does
// not know is answered with, because R7 keys the CLI's bridge on those two
// being told apart.
//
// It writes no audit line, and that is the rule and not an omission: no
// caller's identity was verified — there is nothing to verify one against,
// since no issuer is configured — and the audit is written once a caller IS
// verified (audit.go, credentials-service's own rule).
func (s *Server) refuseIdentityNotServed(w http.ResponseWriter) {
	s.refuse(w, StatusUnavailable, identitywire.RefusalNotServed,
		"this service does not issue a run's identity: no issuer is configured on this installation, so the identity routes answer 'not served'. A launcher of this release signs the run's identity locally, as it did before an issuer existed; a launcher on an installation that has already moved the grant cannot, and refuses with the fix (run fugaro update)")
}

// refuseIdentityCaller answers a refusal of an identity route's CALLER, which
// is those routes' one refusal with no line: nothing has been verified yet, so
// there is no caller for a line to name.
//
// The class's refusal is the contract's own code and not the service's older
// vocabulary, because the launcher reads these codes: an account on no roster
// is caller_not_admitted (the contract's own words for it), and anything else
// — the key set the token had to be checked against being unreadable — is this
// service's own failure, answered the way every unreadable document here is
// (R16's ScopeFault rule: a 403 halts a caller as a verdict about itself).
func (s *Server) refuseIdentityCaller(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnverifiedJob) {
		s.refuse(w, StatusUnverified, identitywire.RefusalCallerNotAdmitted, err.Error())
		return
	}
	s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
}

// refuseIdentityCall answers one refusal of an identity route and records it, in
// the shape refuseGrant and refuseCall already have: one typed refusal to the
// caller, one line to the operator, and the caller's answer is the same whether
// or not the line lands (D143).
//
// It is reached only once the caller IS verified — the refusals above that
// point are answered by refuse alone, with no line, because a line's "who" is
// what this service verified and there is none yet.
//
// run and repository are the run the TICKET names and the repository it belongs
// to, and they are empty until a record at that path was read: the ticket's two
// names are the caller's text until then, and a line that carried them would
// let any job account of this installation write run ids into the operator's
// audit. From the first check that reads a record they are on every line, which
// is what R5's residual needs of the audit: the line names the run whose claim
// was refused.
func (s *Server) refuseIdentityCall(ctx context.Context, w http.ResponseWriter, run, repository string, status int, typ, message string) {
	if err := s.auditIdentity(ctx, run, repository, identityAuditStatus(status)); err != nil {
		// The service's own log line is where an operator reads an act it could
		// not record, for the reason refuseGrant gives: the answer to the caller
		// is a refusal either way.
		s.log.Error("credsvc: a refused identity call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// identityAuditStatus is the status an identity refusal is RECORDED with, and
// it is deliberately not the code the caller is answered (identitywire's
// RefusalIdentityRefused and its siblings).
//
// The audit's status is a closed vocabulary (audit.go's validErrorType lists
// the wire's older ten) and the conventions of this plan say no task edits that
// file: the identity codes are the CLI's to branch on and the line's to
// classify, so a refusal here is recorded as the class of thing it is. The
// class is the refusal's own HTTP status, which the route has already decided —
// 503 is this service's own failure, 400 is a request this contract does not
// read, and 403 is a run whose identity is not this caller's. There is no case
// for the job class: a refusal answered before the caller was verified writes
// no line at all (refuseIdentityCaller, refuseIdentityNotServed).
func identityAuditStatus(status int) string {
	switch status {
	case StatusUnavailable:
		return ErrorStateUnavailable
	case StatusRequestRefused:
		return ErrorRequestRefused
	default:
		return ErrorUnverifiedRunIdentity
	}
}

// auditIdentity writes one identity route's refusal line: the run and the
// repository it was refused for, and the status.
//
// The line is the narrowest this service writes, and the reason is the audit's
// own closed vocabulary (audit.go, which no task of this plan edits): its
// purposes are the acts this service issues credentials for, and none of them
// is an identity act — the probe's own purpose in that file is the credential
// probe of C1.12, which is another act entirely. So the two fields a line may
// carry here are the run identity's (which the rules hold to their own shape)
// and the status, and the fields it may not are the purpose, the caller and the
// job: a caller without a job is refused for every purpose but the person
// acts (callerWithoutJobPurpose), and this route's caller is a job account that
// holds no job name — a job's ID token carries none, and the claim reads no
// header a caller could write. The account a claim was refused for is therefore
// not on the line; the run it was refused for is, and that is the fact R5's
// residual turns on (the same job account is the attacker's and the victim's).
//
// It is deliberately NOT filled from an Identity (Audit): that would set the
// job and the caller from a verified run identity, and this route verifies
// neither — the run it names is the record's and the caller is a job account
// with no job of its own to report.
func (s *Server) auditIdentity(ctx context.Context, run, repository, status string) error {
	return s.audit.Write(ctx, AuditEvent{Run: run, Repository: repository, Status: status})
}
