package runner

// The run's own claim of its identity (LM7, C1, R2, R7).
//
// A launcher of this release does not mint the run's identity: it asks the
// installation's credentials service for a ticket for this run and puts it on
// the execution it starts (identitywire.TicketEnv, LM6). This file is the other
// end of that: the run presents the ticket, the job's own Google ID token --
// which is what the service admits the claim by, the account the record names
// (R4, F12) -- and a nonce it draws itself, and the service answers the custom
// token the record's claims were signed into, which the run exchanges for its
// identity exactly as it exchanged the launcher's own before (budgetClient's
// tail, unchanged: the same exchange, the same claim check against the slug and
// the run).
//
// It goes in where the sealed object used to be taken from: takeBudgetToken
// (projectpolicy.go) is the one take both the budget session and the policy
// read share, and it is the place both mechanisms hand the run its identity
// from, so that whichever the launch left, the policy read and the session are
// one identity and one take (TestTheBudgetTokenIsTakenAtOneCallSite).
//
// Four rules shape everything below.
//
//   - The ticket dies here. It is a secret of the service's, handed to one
//     execution and claimable once: it is registered with the redactor before
//     the first call that carries it, it reaches no log line, no record and no
//     reason this run writes, and the token the answer carries is registered
//     the same way before anything can print it.
//   - The nonce is the run's own and it is kept (R2). It is drawn once, before
//     the first attempt, and every retry presents the same one, because the
//     attempt it repeats may have landed at the service: the same nonce again
//     gets the same answer, and another one would be a second claim. It is
//     never logged and never audited -- the service keeps only its digest --
//     and it is not written into the record.
//   - A refusal is an answer and never retried. The contract's four codes are
//     decisions: a ticket already claimed is claimed for good (R5), a caller
//     the route does not admit is not admitted by asking again, and a service
//     that holds no issuer holds none a moment later. What is retried is an
//     attempt nothing answered: a service that could not be reached, one that
//     answered 5xx or its rate limit, a call this run could not build because
//     the metadata server was not there yet. The retry is bounded (spec
//     §Resilience: a job retries its credential requests with backoff) and the
//     run halts when it is spent -- no session, no cap and no kill switch would
//     be worse than stopping (D143, runstore.HaltBudgetUnavailable).
//   - It fails closed. Every path that does not end in a token the run can
//     exchange halts the run where it stands: an installation with no service
//     address in the job's environment, a URL this run will not present its own
//     identity token to, a nonce it could not draw, a refusal, an answer that
//     is not this contract's. A run with a ticket has no second mechanism to
//     fall back to -- it holds no seal key, because its launcher minted nothing
//     -- so the alternative to stopping is running with no identity at all.
//
// The bridge's other arm is not here: an execution that carries the old marker
// opens the sealed object as before (Backend.TokenMarked, takeBudgetToken's
// bucket half), and the two mechanisms live together for one release (R7,
// LM18).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// claimNonceBytes is the size of the nonce this run draws for its claim: 32
// bytes of crypto/rand, carried as one unpadded base64url string. It is a value
// nothing can guess and the service keeps only its digest (Claim.NonceHash), so
// its length is the only thing about it this run decides (R2).
const claimNonceBytes = 32

// claimAgain is the fix every refusal on this path names, and it is the one the
// budget token's refusals name: this run has launched, so a new run is what
// works, while retrying this run id only reports it.
const claimAgain = "start a new run (fugaro run with the same task): this run has launched, and retrying its run id only reports it"

// claimBudgetIdentity claims the identity this run's ticket was issued for: the
// ticket, the job's own Google ID token and a nonce the run keeps for its
// retries, answered with the custom token the run exchanges (LM7, C1, R2).
//
// It returns the custom token the claim answered, or an error. Every error it
// returns has already halted the run (bootstrapHalt): a claim that does not
// land is a run with no identity, its session, its cap and its kill switch all
// missing, and that is a halt and not a reason to go on.
func (r *run) claimBudgetIdentity(ctx context.Context, ticket string) (string, error) {
	// The ticket is a secret of the service's from this line on: registered
	// before the first call that carries it, so every reason this run
	// publishes afterwards is redacted of it (runner.go's addSecret).
	r.addSecret(ticket)
	serviceURL := envLookup(r.d.Env, credentialsServiceURLEnv)
	if serviceURL == "" {
		// The launcher asked an issuer, so the installation has one, and a job
		// that carries no address for it is a job deployed before
		// keys-through-service landed (U4): nothing to claim from, and the
		// ticket cannot be spent anywhere else.
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run",
			"this run was launched with an identity ticket ("+identitywire.TicketEnv+" is set in the job's environment) and the job carries no "+credentialsServiceURLEnv+", so there is no credentials service to claim it from: run fugaro update for this repository, then "+claimAgain)
	}
	if err := checkSlipServiceURL(serviceURL); err != nil {
		// The run's other service calls' rule, and the launcher's own before
		// this one: the claim carries the job's own identity token, and over
		// plain http to a host that is not this machine it would travel in
		// cleartext. A slip is optional and is given up over this; a claim is
		// not, so the run stops.
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run",
			"this run's claim was not made: "+err.Error()+", so no claim can be sent. Run fugaro update for this repository, then "+claimAgain)
	}
	nonce, err := claimNonce()
	if err != nil {
		// crypto/rand failing is the machine's state and not the service's,
		// and a run that claimed with a guessable or empty nonce would be one
		// whose claim another caller could repeat (R2). Nothing was asked.
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run",
			"this run's claim was not made: the nonce it is made with could not be drawn from the machine's random source, so no claim was sent, and a claim with a nonce that is not random is one another caller could repeat (R2). Launch the run again (fugaro run with the same task)")
	}
	cctx, cancel := context.WithTimeout(ctx, slipClaimBudget)
	defer cancel()
	ans, err := r.claimRetryCall(cctx, serviceURL, identitywire.ClaimRequest{Ticket: ticket, Nonce: nonce})
	if err != nil {
		// No answer at all, or none worth keeping, after the bounded retry. The
		// error is this file's own sentence (claimPost writes one and never
		// reads the service's answer into it), and it is redacted anyway: no
		// line of a reason may carry the ticket.
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", r.claimCallDetail(err))
	}
	if ans.status != http.StatusOK {
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", claimRefusalDetail(ans.status, ans.body))
	}
	var claim identitywire.ClaimResponse
	if err := identitywire.DecodeStrict(ans.body, &claim); err != nil {
		// Strictly, and generously for nothing: a field this release does not
		// know is a token read out of a document it does not understand. The
		// decoder's own message names the byte it choked on, which is the
		// answer's text, so it is not repeated here.
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run",
			"the credentials service answered this run's claim with a body that is not this contract's claim answer, so no token was taken from it. Start a new run (fugaro run with the same task)")
	}
	if claim.Token == "" {
		return "", r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run",
			"the credentials service answered this run's claim with no token, and an empty token is no identity. Start a new run (fugaro run with the same task)")
	}
	return claim.Token, nil
}

// errNoAttestation marks the failure that is this VM's rather than the
// service's: the claim's attestation could not be read from the metadata
// server. It is a sentinel because the two causes behind one spent retry have
// two sentences and two fixes -- a service that answered nothing is the
// installation's, an unreadable VM identity is the machine's -- and the halt
// has one error to tell them apart by.
var errNoAttestation = errors.New("this VM's identity could not be read for the claim")

// claimCallDetail is the halt's sentence for a claim that got no answer worth
// keeping after the bounded retry. Nothing is retried past it (F23: the run
// never proceeds without an identity), and the fix is a new run either way,
// because this one has launched. Which of the two sentences it is decides
// nothing about the fix and everything about the cause a person reads: the
// service that was not there, or the VM identity this claim must carry and
// could not read.
func (r *run) claimCallDetail(err error) string {
	if errors.Is(err, errNoAttestation) {
		return "this run's claim was not made: the identity of the VM this run is on could not be read from the metadata server, and a Batch run's claim carries it (the service serves this run's identity only to a VM of the job the record names, U2), so no claim was sent. A machine whose metadata server answers nothing is not one this run can claim from: launch the run again (fugaro run with the same task), and if it stops here again the VM it lands on cannot read its own identity"
	}
	return "the installation's credentials service could not be reached for this run's claim, and the run has no identity without one: " + r.redact(err.Error()) + ". Start a new run (fugaro run with the same task)"
}

// addIdentityTicket registers the launch's identity ticket with the redactor,
// at the run's first moment and long before the claim presents it. The ticket
// travels in the job's environment (Backend.Ticket, identitywire.TicketEnv),
// where Batch prints a job's variables itself (F3) and where Fugaro prints it
// nowhere (R7, D3115): holding it here is what makes even a reason published
// before the claim -- an admission halt, a config refusal, a git failure --
// redacted of it. The claim registers it again where it is presented, which is
// the guarantee that travels with the file that makes the call.
//
// The attestation needs no line here: it exists only once the claim reads it
// from the metadata server, and the read registers it before it returns
// (batchAttestation).
func (r *run) addIdentityTicket() {
	if r.d.Backend.Ticket != "" {
		r.addSecret(r.d.Backend.Ticket)
	}
}

// claimNonce draws the nonce the claim is made with: claimNonceBytes of
// crypto/rand as one unpadded base64url string, so the value is one header and
// body value and holds no byte a JSON encoder would have to escape.
func claimNonce() (string, error) {
	var b [claimNonceBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// claimRetryCall makes this run's claim, retrying the attempts nothing answered
// (spec §Resilience). The claim's two own values are drawn once and presented
// unchanged on every attempt -- the same ticket and the same nonce, so a retry
// after an answer that was lost is the same claim and not a second one (R2) --
// while what a claim carries of the machine it is made from is read per attempt
// by claimPost.
//
// It is the slip's retry shape with the slip's own delays and wait, because it
// is the same kind of call to the same service with the same budget: see
// slipRetryCall, whose loop this one repeats rather than shares -- the two
// differ in what they read of the answer (slipRetryable asks the status alone;
// this one stops on any of the contract's codes) and in what a spent retry
// means (a slip is given up, a claim halts).
func (r *run) claimRetryCall(ctx context.Context, serviceURL string, claim identitywire.ClaimRequest) (slipAnswer, error) {
	for attempt := 0; ; attempt++ {
		ans, err := r.claimPost(ctx, serviceURL, claim)
		if !claimRetryable(ans.status, ans.body, err) || attempt >= len(slipRetryDelays) {
			return ans, err
		}
		if werr := slipRetryWait(ctx, slipRetryDelays[attempt]); werr != nil {
			// The run's own budget for the claim ran out, or the run was
			// cancelled: nothing left to ask with.
			return ans, werr
		}
	}
}

// claimRetryable reports whether an answer, or the failure to get one, is one
// this run asks again about.
//
// A call that got no answer at all is the service being unreachable -- a cold
// start, a refused connection, a reset, a request this run could not build
// because the metadata server was not there yet -- and is the reason the retry
// exists. An answer is retried only when nothing decided it: 5xx and the rate
// limit. Every other answer is a decision read once, and the two of this
// contract that a caller must never quietly retry are the four refusal codes
// (asking again answers the same thing, and a claim is not a vote) and the
// release-behind 404 or 405 (a service one release back does not grow the
// route inside this run's start).
func claimRetryable(status int, answer []byte, err error) bool {
	if err != nil {
		return true
	}
	if claimRefusalCode(status, answer) != "" || slipRouteAbsent(status) {
		return false
	}
	return status == http.StatusTooManyRequests || status >= 500
}

// claimPost makes one POST of this run's claim and reads the answer back, with
// the two identities the route decides a claim by: the job's own Google ID
// token, which the service admits at Cloud Run IAM and verifies as the account
// the record names (R4), and -- where this run's job is on Batch -- the VM's
// own identity token in the metadata server's full format, which is what says
// the claim is made from a VM of the job the record names (BR4, R1, U2). The
// run's Firebase identity is deliberately not sent and does not exist yet: the
// claim is what creates it, and the route is answered ahead of the shared
// request parse for exactly that reason (C1).
//
// Both tokens are read here, per attempt, and neither is cached: they are
// credentials the platform is issuing now, and a retry that follows the
// metadata server's own cold start presents the ones it has by then. The
// body's two other values are the caller's and are the same on every attempt
// (R2).
//
// The answer is bounded and the client never follows a redirect, and neither
// token is read into an error: the ticket's own call is the one place it is on
// any wire but the service's, and a failure here is this run's sentence and not
// the service's text.
func (r *run) claimPost(ctx context.Context, serviceURL string, claim identitywire.ClaimRequest) (slipAnswer, error) {
	token, err := metadataIDToken(ctx, r.d.Env, serviceURL)
	if err != nil {
		return slipAnswer{}, err
	}
	if claim.Attestation, err = r.batchAttestation(ctx, serviceURL); err != nil {
		return slipAnswer{}, err
	}
	body, err := json.Marshal(claim)
	if err != nil {
		// A ClaimRequest is three strings, so this cannot fire: refused rather
		// than sent half-built, and the reason names no value.
		return slipAnswer{}, errors.New("the claim call's request could not be built")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+identitywire.ClaimPath, bytes.NewReader(body))
	if err != nil {
		return slipAnswer{}, errors.New("the claim call's request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := slipClient.Do(req)
	if err != nil {
		// A cancelled call is not the service's fault and is not retried:
		// slipRetryable reads the ctx the same way.
		if ctx.Err() != nil {
			return slipAnswer{}, ctx.Err()
		}
		return slipAnswer{}, errors.New("the installation's credentials service could not be reached")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSlipAnswerBytes+1))
	if err != nil {
		return slipAnswer{status: resp.StatusCode}, errors.New("the credentials service's answer could not be read")
	}
	if len(data) > maxSlipAnswerBytes {
		return slipAnswer{status: resp.StatusCode}, errors.New("the credentials service's answer is larger than a claim's")
	}
	return slipAnswer{status: resp.StatusCode, body: data}, nil
}

// claimRefusalCode is the contract's own code for a refused claim, or "" when
// the answer carries none this contract could have written. The call is a
// non-2xx answer, which is what credsvc.ReadWireError reads, and only a code of
// the four routes is relayed: a body that says something else is another
// route's refusal and not this one's.
func claimRefusalCode(status int, answer []byte) string {
	e, err := credsvc.ReadWireError(status, answer)
	if err != nil || !reviewServiceErrorCode.MatchString(e.Error) {
		return ""
	}
	switch e.Error {
	case identitywire.RefusalNotServed, identitywire.RefusalCallerNotAdmitted,
		identitywire.RefusalNoLaunchScope, identitywire.RefusalIdentityRefused:
		return e.Error
	}
	return ""
}

// claimRefusalDetail is the halt's sentence for a refused claim: this run's own
// words for what the service decided, the contract's own code, and the fix.
//
// The service's message is dropped, like every other refusal of its this run
// reads (workflowSecretRefusalCode): it is written for an operator's log, and a
// run's reason is not the place for another writer's sentence. What the code
// means is spelled out here instead, in the reading the record was written for
// -- the ticket claimed once (R5), the account the job runs as, the repository
// it may launch -- and never the service's text.
func claimRefusalDetail(status int, answer []byte) string {
	switch claimRefusalCode(status, answer) {
	case identitywire.RefusalIdentityRefused:
		// The one code that covers the race this whole mechanism exists for:
		// the ticket was claimed once, and the second claimant -- this run, if
		// another execution claimed first -- loses (R5). The other causes are
		// stated because the code is one code: the CLI branches on it the same
		// way for all of them (contract.md).
		return "the ticket this run was launched with was already claimed, or it is not this job account's, or it has expired: the credentials service refused the run's claim (identity_refused), and a ticket is claimed once, so the run that lost halts (R5). The service wrote one audit line naming the claim. Start a new run (fugaro run with the same task): this run has launched, and retrying its run id only reports it"
	case identitywire.RefusalCallerNotAdmitted:
		return "the account this job runs as is not one the credentials service admits a claim from (caller_not_admitted), so this run cannot be given an identity. Run fugaro update for this repository: it is what puts the installation's roster and the job's account in agreement, and then " + claimAgain
	case identitywire.RefusalNoLaunchScope:
		return "the account this job runs as may not be issued this repository's identity (no_launch_scope), so the credentials service refused the claim. Check the launcher roster and the roles for this repository, then " + claimAgain
	case identitywire.RefusalNotServed:
		return "the installation's credentials service holds no issuer (" + identitywire.RefusalNotServed + "), so it cannot sign a run's identity. This execution was launched with a ticket, which only an issuing service hands out, so the service is behind the launcher that started it: run fugaro update for this repository, then " + claimAgain
	}
	if slipRouteAbsent(status) {
		// The same skew the slip reads as an absent route, and the same
		// release-behind service: this execution was created before the update
		// and its job carries no identity routes (F24, O12).
		return "the installation's credentials service answered " + strconv.Itoa(status) + " to this run's claim, which is how a service that serves no identity route answers: the service is one release behind the launcher that started this execution. Run fugaro update for this repository (it deploys the service), then " + claimAgain
	}
	return "the installation's credentials service refused this run's claim with " + strconv.Itoa(status) + claimRefusalCodeSuffix(status, answer) + ", and the run has no identity without an answer it understands. Run fugaro update for this repository, then " + claimAgain
}

// claimRefusalCodeSuffix is ", refused: code" for an answer that carries a
// refusal this contract's shape but none of its four codes, and "" otherwise:
// the sentence keeps what the service decided visible without repeating its
// text.
func claimRefusalCodeSuffix(status int, answer []byte) string {
	e, err := credsvc.ReadWireError(status, answer)
	if err != nil || !reviewServiceErrorCode.MatchString(e.Error) {
		return ""
	}
	return ", refused: " + e.Error
}

// ticketNoBackend is the refusal of a claimed job with no budget backend in its
// environment: the launcher asked an issuer for this run's ticket, which it
// only does when the project has a shared budget or a published policy, so this
// run has a ceiling it cannot read and must stop before admission and before
// any git credential exists (M2, the marker's own refusal, one mechanism over).
func ticketNoBackend() error {
	return fmt.Errorf("this run was launched with an identity ticket ("+identitywire.TicketEnv+" is set in the job's environment), so this repository has a shared budget or a published policy, but its job carries no budget backend (%s is not set in the job's environment): run fugaro update for this repository, then launch the run again", RTDBURLEnv)
}

// noBackendForWhatTheLauncherLeft is M2's refusal for a job whose launcher
// provisioned this run's identity and whose environment carries no database to
// be accounted in. The two mechanisms have a sentence each, because what the
// launcher left is what the fix is about: the marker's is about a budget token
// that was minted and cannot be read, the ticket's about an identity asked for
// from a service this job cannot reach.
func noBackendForWhatTheLauncherLeft(b Backend) error {
	if b.Ticket != "" {
		return ticketNoBackend()
	}
	return strayTokenNoBackend()
}
