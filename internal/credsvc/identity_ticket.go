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
	"unicode"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/task"
)

// The ticket route (R2, R4, R5, R6; LM1): a verified launcher's ask for a run's
// ticket, and the issuance record it is written on.
//
// The route is one act made of two halves, and they are the two things this file
// and identity_record.go hold: the CHECKS of R4, made here over an ask and the
// installation's own tables, and the RECORD they end in, written by the store.
// Nothing is written until every check has passed, so a refused ask leaves the
// node exactly as it was — which is the property R5's re-issue rule and R6's
// retry both rest on.
//
// # What the caller holds, and what it never does
//
// The ask names the run, the repository, the account the launch will run as and
// the ingredients of the claims, and nothing else: the uid comes from the
// record's path and the project from the installation (R3), so the ticket
// answers a SECRET the caller did not choose and cannot derive. The ticket is
// kept as its digest and answered once (Record.TicketHash); it is the only value
// of this route that is a credential, it is in no log line and no audit line,
// and the answer carrying it is no-store.
//
// # Why the answer is no larger than a ticket and a time
//
// The launcher puts the ticket on the execution's environment (TicketEnv) and
// passes it on; it holds no identity, and it holds nothing it could use to
// derive one. The run exchanges the ticket for its identity at the claim route
// (LM2, LM7), where the record's claims are signed after they are checked again.

const (
	// ticketBytes is the entropy of a ticket: thirty-two bytes of crypto/rand,
	// in hex. It is a credential and not a handle — whoever holds it and the
	// job's own identity token together holds a run's identity (R2) — so it is
	// drawn at the width a secret needs, twice the slip id's, and a draw that
	// fails is an error and never a fallback to something guessable (the same
	// rule newSlipID and newClaimSecret follow).
	ticketBytes = 32

	// ticketLifetime is how long a ticket the issuer answered stays claimable,
	// measured from the record's own stamp: the queue's allowance plus the
	// clock slack the claims themselves carry (token.QueueAllowance +
	// token.Slack, the padding ExpiryFX adds beyond a run's own timeout).
	//
	// The two properties it has to have, and has by being exactly that sum:
	//
	//   - a ticket always dies strictly BEFORE the identity it would produce.
	//     The identity's fx is issue + runtime + QueueAllowance + Slack and a
	//     runtime is always positive (R4 refuses an ask that names none), so
	//     ticketExpiry(rec) < rec.Claims.FX whatever the ask was;
	//   - a ticket outlives the queue it is waiting in. A launcher asks before
	//     the execution it is about to start has a container, and the platform
	//     may hold that execution for up to the queue allowance; a ticket that
	//     died inside that window would fail a launch that did everything
	//     right.
	//
	// A shorter life would be a run that cannot claim what it was given; a
	// longer one would be a ticket a victim run has already replaced (R6) and
	// still claimable, which is the second claim R2's fence is about.
	ticketLifetime = token.QueueAllowance + token.Slack

	// maxClaimStringBytes is the signer's own bound on one claim string
	// (token's maxFieldLen, which is unexported): the wide claims — the slug,
	// the run id, a context repository — are held to narrower shapes of their
	// own above and in signableClaims, and this is the floor under the one
	// string that travels from the ask to the wire unshaped (rb).
	maxClaimStringBytes = 200

	// maxIdentityRequestBytes bounds an ask's body. The bodies of these routes
	// carry names and numbers — a slug, a run id, an account, a branch, a
	// handful of context repositories — so the bound is the small one the
	// service's other mint-shaped routes use (maxMintRequestBytes), and a body
	// past it is refused before it is parsed.
	maxIdentityRequestBytes = 4 << 10
)

// identityTicket answers one verified caller's ask for a run's ticket.
//
// The mount (identity_mount.go, I1) reads the Authorization bearer, answers a
// service with no issuer before it verifies anybody (the contract's "not
// served", with no line: nothing is verified yet) and says which class the
// caller was verified as (LM3). What is left here is the ASK, and every refusal
// made below is a refusal of a caller this service HAS verified — which is why
// every one of them goes through refuseTicket and writes exactly one audit line.
//
// The order of the checks is R4's, and it is an order for two reasons: nothing of
// the installation is read before the ask itself is a shape this contract reads
// (a call that names no repository is refused without a database call), and
// nothing is written until every check has passed, so the record's own decisions
// (whose it is, whether it is claimed) are the last thing that happens and are
// made from the node as it stands at that moment.
func (s *Server) identityTicket(ctx context.Context, w http.ResponseWriter, caller IdentityCaller, r *http.Request) {
	// The floor under the mount. The mount answers a service with no issuer
	// before it verifies anyone, and that answer carries no line because no
	// caller is verified yet (identity_stub.go's rule, kept by I1); a call that
	// reached HERE did have a caller verified, so this branch owes the line
	// below — and its answer stays the contract's "not served", the code a CLI
	// bridges on (R7), because a service with no signer is exactly the
	// installation not yet given an issuer.
	if s.cfg.Identity.Signer == "" {
		s.refuseTicket(ctx, w, caller, StatusUnavailable, identitywire.RefusalNotServed, ErrorStateUnavailable,
			"this service holds no issuer, so it issues no ticket: the installation has not been given one yet (run `fugaro update`)")
		return
	}
	if s.cfg.Identity.Store == nil || s.cfg.Identity.Scope == nil {
		// A service with a signer and no record store or no scope check is a
		// wiring fault, and deliberately NOT answered "not served": a CLI would
		// read that as the bridge and sign the run's identity locally with a row
		// this installation may already have moved (R7). It is the service's own
		// failure, and the line says so.
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"this service is not configured to issue a ticket: an installation with an issuer but no issuance record store or no launch-scope check is broken")
		return
	}
	route, _ := identitywire.RouteFor(identitywire.TicketPath)
	if !route.AdmitsClass(caller.Class) {
		// The route's own table is the contract's (identitywire.Routes) and not
		// a second copy of it: a mount that bound the wrong handler, or a
		// verifier that answered a class this route does not admit (a job
		// account at the ticket route), must not be a way in.
		s.refuseTicket(ctx, w, caller, StatusUnverified, identitywire.RefusalCallerNotAdmitted, ReasonNotAdmitted,
			"this route issues a run's ticket to the caller that launches the run: it serves a person on the installation's roster, a roster service account and the installation's orchestrator, and no other class")
		return
	}
	// readCall is the service's one body read (its bound, and the scrub of a
	// token a route would forward); the ask's body is read with no value to
	// scrub because this route forwards nothing and echoes nothing — the ask is
	// refused by field name and never by what it held.
	cl, err := readCall(r, nil)
	if err != nil {
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if len(cl.body) > maxIdentityRequestBytes {
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			fmt.Sprintf("%sthe ask's body is larger than the %d bytes this route accepts", requestRefused, maxIdentityRequestBytes))
		return
	}
	var req identitywire.TicketRequest
	if err := identitywire.DecodeStrict(cl.body, &req); err != nil {
		// Strict, so a body naming a field this contract does not have — a uid
		// or a claim set among them — is refused BY NAME rather than served
		// under a value the caller chose (R2). The reader's own error is the
		// refusal's message, as it is at every other read of a body in this
		// service, and it names the field and the type it expected and no value
		// the ask carried.
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the ask is not one JSON object of this route's fields: "+err.Error())
		return
	}
	// The ask's own shape, before anything of the installation is read. Two of
	// the checks are the launcher's own (mintBudgetToken refuses a slug or a run
	// id the database would have to escape, and a slug that is not one printable
	// value because it lands on this service's line): a name whose escaped form
	// differs from the name is a name two spellings can collapse onto one node,
	// and the ticket route is where that node is written.
	switch {
	case !token.ValidSegment(req.Slug) || budget.Key(req.Slug) != req.Slug || !printableHeaderValue(req.Slug, maxSlipSlugBytes):
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the ask names no repository slug of this installation's shape")
		return
	case !task.ValidRunID(req.Run) || budget.Key(req.Run) != req.Run:
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the ask names no run id of this installation's shape")
		return
	case req.Job != "" && !req.Job.Valid():
		// R1: a Batch launch's job is named by its full resource name, and the
		// name is the tie two later calls are held to — the registration writes
		// the job's id on it, and the claim is served only to a VM of it
		// (D3114). A name of any other shape can never be served, so the ask is
		// refused here, before the launcher has created anything to name.
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the job must be the full resource name of the Cloud Batch job this launch creates (projects/<project>/locations/<location>/jobs/<job>)")
		return
	case caller.Class != identitywire.ClassOrchestrator && req.Attempt != "":
		// The attempt reference is the orchestrator's own (D3136): it is the
		// orchestrator's id of an intent it re-issues, and no other class has
		// one to name or a record to look it up on.
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the ask names an attempt, which is the resident orchestrator's own reference to the attempt it re-issues")
		return
	case req.Attempt != "" && !req.Attempt.Valid():
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the attempt the ask names is not one the orchestrator's own spelling could have written")
		return
	case req.MaxRuntimeSeconds <= 0:
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the ask names no runtime, and a ticket for no runtime is a ticket for an identity that is never live")
		return
	}
	sc := scopeOfCall(ctx, s)
	canonical, known := sc.Repos()[req.Slug]
	if !known {
		// R4: the repository is one the installation has a job for. The table is
		// the installation's own (RepositoryTableFor, which every other route
		// resolves a call's repository through), so an unknown slug is refused
		// and never guessed from the call's own text (D1066) — and it is answered
		// as the launch-scope refusal, because "this installation serves no such
		// repository" and "you may not launch it" are one thing for a caller:
		// it gets no ticket, and what to do about it is the same (`fugaro
		// update --repo`, or the roles a person holds).
		s.refuseTicket(ctx, w, caller, StatusUnverified, identitywire.RefusalNoLaunchScope, ReasonNotAdmitted,
			"this installation serves no repository of this ask's slug, so it issues no ticket for it")
		return
	}
	// R4: the caller holds the launch action on that repository, by the resolver
	// the CLI gate uses (LM23's ScopeCheck, with the orchestrator's class judged
	// by LM24 behind the same hook). It is asked ONCE per ask, after the class is
	// verified and before anything is written (identity_config.go), and the whole
	// ask travels with it because what the orchestrator's class may be issued is
	// bound to its own record and to the attempt it names.
	var fault *ScopeFault
	switch err := s.cfg.Identity.Scope.Check(ctx, caller, req); {
	case err == nil:
	case errors.As(err, &fault):
		// A fault is the SCOPE's failure and not a verdict about the caller
		// (R16): a roles record that could not be read is not an empty one, and
		// answering it "no launch scope" would tell a person they hold no scope
		// when the service could not tell. It is this service's own failure, and
		// it gets its own line for the operator.
		s.log.Error("credsvc: the launch scope of a ticket's ask could not be judged", "error", err)
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"the launch scope of this ask could not be judged, so no ticket is issued for it")
		return
	default:
		s.refuseTicket(ctx, w, caller, StatusUnverified, identitywire.RefusalNoLaunchScope, ReasonNotAdmitted, err.Error())
		return
	}
	if !s.ticketAccountAdmitted(req.Slug, canonical, req.Account) {
		// R4, F12. The account is what the claim proves (F11: the job and not
		// the execution), so a ticket bound to another account would be one no
		// run could claim — or one another job could.
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the account the ask names is not a job account the runs of this repository are launched as")
		return
	}
	if maxSeconds := int64(s.cfg.Identity.MaxRuntime / time.Second); int64(req.MaxRuntimeSeconds) > maxSeconds {
		// R4: the expiry is no further out than the service's configured
		// maximum, and an ask beyond it is REFUSED and not clamped — a shorter
		// identity than the run needs is a run that halts in the middle of its
		// work, which is worse than a launch that fails where the person is
		// still there. A maximum of zero issues nothing at all, which is the
		// fail-closed reading of an installation that configured none.
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			fmt.Sprintf("%sthe ask names a runtime of %d seconds and this installation issues at most %d, and it is refused rather than clamped", requestRefused, req.MaxRuntimeSeconds, maxSeconds))
		return
	}
	// The claims the record will carry (R3): the signer's own rules are applied
	// HERE, because what the record holds is exactly what the service signs at
	// the claim — and a set the signer refuses is refused there, inside the run,
	// long after the launcher was told the launch had succeeded.
	now := s.now()
	project, ok := installationProject(s)
	if !ok {
		s.log.Error("credsvc: the installation's own project could not be read for a ticket's claims")
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"the installation's own project claim could not be read, so no ticket is issued for this ask")
		return
	}
	claims := token.Claims{
		Slug:         budget.Key(req.Slug),
		Run:          budget.Key(req.Run),
		FX:           token.ExpiryFX(now, time.Duration(req.MaxRuntimeSeconds)*time.Second),
		FP:           project,
		RB:           req.Launcher,
		Branch:       req.Branch,
		PRs:          append([]int(nil), req.PullRequests...),
		ContextRepos: append([]string(nil), req.ContextRepos...),
	}
	if err := signableClaims(claims); err != nil {
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// R4's last checks are the record's own. The outcome is read first, because
	// the run of a re-issue must not have ended: replacing the record of a run
	// that is over would hand a live ticket to a run that has already written its
	// outcome and be refused at the claim anyway.
	over, err := s.ticketRunEnded(ctx, sc, req.Slug, req.Run)
	if err != nil {
		s.log.Error("credsvc: a run's outcome could not be read for a ticket's ask", "run", req.Run, "repository", req.Slug, "error", err)
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"this run's outcome could not be read, so no ticket is issued for it")
		return
	}
	if over {
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the run this ask names is already over, so no ticket is issued for it: a run's identity is minted before it starts (R4, R6)")
		return
	}
	// The ticket is drawn before the record is written and is kept only as its
	// digest (identitywire.Digest): the value itself is answered to the launcher
	// and written NOWHERE — not on the record, not in a log line, not in an
	// audit line (R3, and the one rule audit.go exists for).
	ticket, err := newTicket(req.Slug, req.Run)
	if err != nil {
		s.log.Error("credsvc: a ticket could not be drawn", "error", err)
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"no ticket could be drawn for this ask")
		return
	}
	rec := identitywire.Record{
		Caller:          caller.Address,
		Class:           caller.Class,
		At:              now,
		Claims:          claims,
		ExpectedAccount: req.Account,
		TicketHash:      identitywire.Digest(ticket),
		Attempt:         req.Attempt,
		// R19: the job the launcher named is written WITH the ticket, and it is
		// what the registration's id is later written on and what the claim is
		// held to (D3114). Empty for a Cloud Run launch, whose record is then the
		// record of the release behind.
		Job: req.Job,
	}
	// The store decides the two facts of the record that are the record's own,
	// from the node as it stands (identity_record.go's Put): whose record this
	// run already is (R5) and whether its ticket was claimed (R6). Both are
	// refusals of the ASK — the caller is told what to do about each, which is a
	// new run id — and neither writes anything.
	switch err := s.cfg.Identity.Store.Put(ctx, req.Slug, req.Run, rec); {
	case err == nil:
	case errors.Is(err, identitywire.ErrAnotherCaller):
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"this run is another caller's record, and a run's ticket is re-issued to the caller on the record alone (R5): a new attempt takes a new run id")
		return
	case errors.Is(err, identitywire.ErrClaimed):
		s.refuseTicket(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"this run's ticket was already claimed, and a claimed ticket is never re-issued: a new attempt takes a new run id (R6)")
		return
	default:
		// The store's own failure, wrapped as a *StoreError: a node nobody could
		// read or write is this service's state and never a decision about the
		// caller (identity_config.go). Its error is logged and never answered —
		// it carries the record's path — and the answer does not say which of
		// the two failures it was.
		s.log.Error("credsvc: an issuance record could not be written", "run", req.Run, "repository", req.Slug, "error", err)
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"no ticket could be issued for this ask")
		return
	}
	body, err := json.Marshal(identitywire.TicketResponse{Ticket: ticket, ExpiresAt: ticketExpiry(rec)})
	if err != nil {
		// The answer is built after the record is written, and a failure here
		// leaves a record whose ticket nobody received. It is the one failure on
		// this route the record cannot be unwound from, which is why the
		// service's own line below names it: the launcher's retry gets a new
		// ticket and replaces this record (R6), and an operator reading the log
		// can see that is what happened.
		s.log.Error("credsvc: the ticket's answer could not be built", "run", req.Run, "repository", req.Slug, "error", err)
		s.refuseTicket(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"the ticket's answer could not be built")
		return
	}
	// The issue is recorded on the service's own line and not as an audit line,
	// for the slip grant's reason (slipGrant, audit.go's PurposeSlipGrant): a
	// StatusOK line requires a credential, and this route issues none — what it
	// writes is a record that keeps a digest, and what it answers is a secret
	// that must reach no line. This line is where an operator reads who asked for
	// which run, and it carries the run, the repository and the account that the
	// refusals above deliberately do not.
	s.log.Info("credsvc: a run's ticket was issued",
		"run", req.Run, "repository", req.Slug, "account", req.Account,
		"caller", caller.Address, "class", string(caller.Class), "expires_at", ticketExpiry(rec))
	writeSlipAnswer(w, body)
}

// refuseTicket answers one refusal of the ticket route and records it, in the
// shape refuseGrant already has for a route whose caller holds no run identity:
// one typed refusal to the caller, one line to the operator, and the caller's
// answer the same whether or not the line lands (D143).
//
// It is this route's only refusal path once the caller is verified, which is
// what makes LM1's "every refusal is one audit line" a property of the function
// rather than a rule every branch has to remember: a branch that refuses without
// calling it writes no line, and there is no such branch.
//
// # The line's purpose
//
// The line is written under PurposeSlipGrant — the launcher class's own purpose
// (audit.go) — because the act IS a launcher's request for a run it has not
// started: no purpose of the closed set names an identity route, and none may be
// added here (ValidPurpose's set is audit.go's, and that file is no task's to
// edit). The repository and the run the ask named are NOT on the line, exactly
// as they are not on a grant's refusal line: they are the caller's own bytes and
// not values this service verified — the verified fact this line carries is the
// caller — and the route's own service line is where an operator reads the run,
// the repository and the account of an issue.
//
// # What the status loses
//
// The body's code and this line's status are not one vocabulary: identitywire's
// codes are not audit.go's statuses, and a status audit.go cannot write is
// refused there (Write answers a warn line and an error, never a line with a
// value outside the closed set). So the answers map onto the nearest status the
// set holds: caller_not_admitted and no_launch_scope are both ReasonNotAdmitted
// (a caller this service did not admit), identity_refused is ErrorRequestRefused
// (an ask this service will not read), and every failure of the service's own is
// ErrorStateUnavailable. The caller's body carries the exact code; an operator
// who needs it reads it there, and for a failure it reads the service's own line
// written beside this one. The loss is stated here rather than papered over.
func (s *Server) refuseTicket(ctx context.Context, w http.ResponseWriter, caller IdentityCaller, status int, typ, auditStatus, message string) {
	if err := s.audit.Write(ctx, AuditEvent{Caller: caller.Address, Purpose: PurposeSlipGrant, Status: auditStatus}); err != nil {
		// The service's own line is where an operator reads an act it could not
		// record, for the reason refuseGrant gives: the answer to the caller is a
		// refusal either way.
		s.log.Error("credsvc: a refused identity ticket could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// newTicket draws one ticket for the slug and run the record is written under:
// ticketBytes from crypto/rand, in hex, which is the alphabet these values are
// spelled in everywhere else in this service, carried beside the record's own
// two names.
//
// The two names are part of the ticket and not decoration: the claim's caller
// holds no run identity — the claim is a run's first call, made before it has
// one (R2) — so C1's ClaimRequest carries the ticket and the nonce and nothing
// else, and the ticket is the ONLY thing that can tell the claim which record to
// read. The spelling that carries them is joinIdentityTicket's and not this
// file's (identity_claim.go): one function spells the ticket for the mint and
// reads it back at the claim, so the two cannot come apart into a ticket no run
// could claim.
//
// It returns an error and never a fallback, for newSlipID's reason: a ticket
// another caller could predict is a ticket it could claim, and the only thing
// standing between it and the record would be the account the claim checks —
// which is a fence this service must not lean on when it can simply draw a
// secret.
func newTicket(slug, run string) (string, error) {
	raw := make([]byte, ticketBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("credsvc: drawing a ticket failed: %w", err)
	}
	return joinIdentityTicket(slug, run, hex.EncodeToString(raw))
}

// ticketExpiry is when a ticket stops being claimable, read off the record it
// was written on: the record's own stamp plus ticketLifetime.
//
// The record has no expiry FIELD — identitywire.Record is the contract's and
// this task adds none (a field the readers outside this binary consume does not
// change meaning under it, and adding one would be a wire change of C1's) — and
// it needs none: the ticket's life is the service's own and fixed, derived from
// the one thing the record does hold.
//
// It is a function and not a constant applied at the write because the claim
// route reads the SAME answer back off a record it did not write (LM2 refuses an
// expired ticket): one derivation, so a ticket cannot be live at one route and
// dead at the other.
func ticketExpiry(rec identitywire.Record) time.Time { return rec.At.Add(ticketLifetime) }

// installationProject is the Fugaro project every identity of this installation
// carries (fp), and the reading of R4's "the project claim is the installation's
// own": the ask does NOT name it — identitywire.TicketRequest has no field for a
// claim the service derives — so it is read out of the one place a deployment
// puts it, the verifier's own configuration (AuthConfig.Project, the fp the
// CLI's own minter writes into every run identity of this installation).
//
// It is read through the Verifier's concrete type because there is nothing else
// to read it through: the identity configuration (identity_config.go, C1) has no
// project field, the scope answers none, and neither file is this task's to
// widen. A verifier that is not the deployment's own *Authenticator, or one that
// names no project, is answered false — and the route fails closed with the
// service's own failure, because a record whose fp is not the installation's is
// a record the rules refuse, which is an identity that could never work.
func installationProject(s *Server) (string, bool) {
	a, ok := s.cfg.Auth.(*Authenticator)
	if !ok {
		return "", false
	}
	return a.cfg.Project, a.cfg.Project != ""
}

// ticketAccountAdmitted reports whether the account an ask names is one the
// repository's launch runs as (R4, F12): the repository's own job account, one
// of a shared project job's — whose account spans the profile's repositories, so
// a ticket for it is bound to the profile and not to the repository, which is
// what F12 states — or one of the installation's review jobs, whose accounts
// name no repository at all (review.go).
//
// The account is the JOB and not the execution (F11): what a claim proves is
// which job called, so a ticket bound to another account would be one no run
// could claim — or, worse, one another job could.
//
// Addresses are compared folded, the rule this package compares every address by
// (foldEmail, the launcher class's own fold): an operator writes an address the
// way their console spells it and a Google ID token carries it the way the
// account's owner registered it, and a comparison of the two spellings byte for
// byte would admit nobody whose case differs (R10).
func (s *Server) ticketAccountAdmitted(slug, canonical, account string) bool {
	folded := foldedAddress(account)
	if folded == "" {
		// An account this service cannot fold names nobody: no roster of the
		// installation can hold it, so it admits nothing rather than matching a
		// row by accident.
		return false
	}
	if ja, ok := s.jobs[folded]; ok && ja.Slug == slug {
		return true
	}
	for _, row := range s.cfg.SharedAccounts {
		if foldedAddress(row.Email) == folded && slices.Contains(row.Repos, canonical) {
			return true
		}
	}
	for _, row := range s.cfg.ReviewAccounts {
		if foldedAddress(row.Email) == folded {
			return true
		}
	}
	return false
}

// signableClaims holds a claim set to the rules the signer will apply to it. The
// record keeps exactly what the service signs (R3), so a set that would be
// refused at the mint must be refused HERE: the launcher is still there to fix
// its ask, where a signature refused at the claim is refused inside the run,
// after the launch was reported as succeeded.
//
// token.Claims' own validation is unexported (internal/budget/token), so this is
// the exported half of the same rules — the bound on the claim set, the counts
// of context repositories and pull requests, and the shape of each value that is
// put on the wire — and the signer's check remains the backstop for the rest
// (LM2). The rules it does not restate are the ones the route has already
// applied to the same values a field at a time: the slug and the run id are held
// to token.ValidSegment (their own check above) and the project claim is the
// installation's own (installationProject).
func signableClaims(c token.Claims) error {
	if c.RB == "" {
		return errors.New("the ask names no launcher, and a run's identity carries the launcher it was asked for (rb)")
	}
	// rb is the one claim string this route takes from the ask and does not
	// shape elsewhere, and the signer holds every claim string to the same rule
	// (token's own: at most 200 bytes, valid UTF-8, no control character). It is
	// the launcher's own name — a config user or a git email, which may hold a
	// space and is NOT a header value — so what is refused here is only the
	// width and the control characters, which are refused for the reason the
	// signer refuses them: a claim that breaks a line is a line an operator
	// reads wrongly.
	if len(c.RB) > maxClaimStringBytes || !utf8.ValidString(c.RB) || strings.ContainsFunc(c.RB, unicode.IsControl) {
		return errors.New("the launcher the ask names (rb) is not one a claim may carry: it must be at most 200 bytes of UTF-8 with no control character")
	}
	for i, n := range c.PRs {
		if n <= 0 {
			return fmt.Errorf("the pull request in position %d is not a positive number", i+1)
		}
	}
	if len(c.PRs) > token.MaxPRs {
		return fmt.Errorf("the ask names more than the %d pull requests one identity may carry", token.MaxPRs)
	}
	if len(c.ContextRepos) > token.MaxContextRepos {
		return fmt.Errorf("the ask names more than the %d context repositories one identity may carry", token.MaxContextRepos)
	}
	for i, repo := range c.ContextRepos {
		if problem := task.ContextReposProblem(repo); problem != "" {
			return fmt.Errorf("the context repository in position %d %s", i+1, problem)
		}
	}
	if c.Branch != "" && !task.BranchRE.MatchString(c.Branch) {
		return errors.New("the branch the ask names is not one a Fugaro run may work on")
	}
	if n := token.ClaimsSize(c); n > token.MaxClaimsBytes {
		// The identity provider's own bound, measured here and not at the
		// exchange: a custom token carrying more is refused by Firebase inside
		// the run (token.MaxClaimsBytes).
		return fmt.Errorf("the claims of this ask would take %d bytes, over the %d a custom token may carry: drop context repositories or shorten the run id and the repository slug", n, token.MaxClaimsBytes)
	}
	return nil
}

// ticketRunEnded reports whether the run an ask names is already over: its
// write-once outcome is written on one of the days an outcome of it can land on,
// or the run id names a launch so old that no identity of it could still be
// honoured.
//
// The window is the verifier's own (checkLive, auth.go): every day from the day
// above this service's today down to the day below the launch day, because the
// day an outcome is written under is the DATABASE's and its clock may sit either
// side of this one. Reading a day an outcome cannot be on can only find one that
// is there — an outcome is written once and never removed — so the wide window
// can never refuse a live run, and the narrow one would admit a run that ended
// across the database's midnight.
//
// It is a second reading of that window rather than a call to checkLive, which
// is the verifier's and reads the VERIFIER's outcome reader (auth.go is C1's
// file, and a task does not edit it): this route reads the SCOPE's, because a
// deployment's scope is what answers where a run's state lives (R1) and the
// identity routes are served under a scope like every other route. The two
// readers are the same node in an installation's deployment and are deliberately
// each other's check on this window.
func (s *Server) ticketRunEnded(ctx context.Context, sc Scope, slug, run string) (bool, error) {
	reader := sc.Outcomes()
	if reader == nil {
		// A deployment whose database this process cannot read as a Getter is
		// one this build cannot ask, and an outcome nobody can read is not an
		// outcome that is not there: the route fails closed rather than issuing a
		// ticket beside a run state it could not check (the reading checkLive's
		// own unreadable case gets).
		return false, errors.New("this deployment's outcomes cannot be read")
	}
	launch, ok := runDay(run)
	if !ok {
		// The run id was held to task.ValidRunID above, so this is unreachable
		// from this route — and it is still an error and never "live". runDay
		// refuses a string that is not a run id, and a run whose launch day
		// cannot be read is a run whose outcome cannot be looked for at all.
		return false, errors.New("the run id names no launch time")
	}
	today := budget.Day(s.now())
	if launch >= today {
		// The launch day is not the oldest day the outcome can be on: a launch
		// day at or ahead of this service's today is this clock behind the
		// minter's rather than evidence that the run has not started, so the scan
		// starts a day below today whatever the launch day is (checkLive's
		// argument, unrepeated here).
		launch = today - 1
	}
	if today-launch > maxRunDays {
		// The run id names a launch older than any identity's fx can reach
		// (maxRunDays, auth.go): no identity of such a run can still be
		// honoured, so a ticket for it would be one no rules would accept. It is
		// refused as a run that is over, which is what it is.
		return true, nil
	}
	for day := today + 1; day >= launch-1; day-- {
		exists, err := reader.OutcomeExists(ctx, day, slug, run)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}
