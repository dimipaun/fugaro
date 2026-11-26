package credsvc

// batchclaim_register.go is BR17 of docs/batch-run-identity: the registration
// (R19, D3108), the launcher's second call.
//
// # The two calls, and why the id is the second one
//
// A launcher that means its Batch run to be attested launches it in two calls
// around the platform's own. It ASKS for the run's ticket naming the job it is
// about to create — LM1's route, which writes that name on the issuance record
// with the ticket — creates the job, and then tells this route the id the
// platform gave it. The id is the second call because it does not exist before
// the create: what the ask can name is the job's full resource name, which the
// launcher chooses, and what ties the VM to the execution is the platform's own
// id, which only the create returns (F30, R1).
//
// # Why the id is written once, and by whom
//
// The VM's attestation is held to the id (BR2, BR3): a job deleted and created
// again under the same name is another job with another id, so a record that
// could be re-registered would be a record a stale VM could claim. Three facts
// fence the one write — the caller asked for this run's ticket, the job is the
// job that ask named, and the ticket is unclaimed — and each is decided from
// the node as it stands when the write lands (RegisterJob) and never from a
// value this route read earlier, for the reason the store's other two writes
// are: a fence made from a stale value is no fence at all.
//
// # What is not here
//
// The mount: verifying the caller, and answering the contract's "not served"
// where the installation has no issuer, are identity_mount.go's (I1), and this
// file changes not one line of it. What this task adds at the door is the PATH
// (server.go's servedRoute and the 404's sentence), which is what makes the
// route reachable at all.
//
// The opt-in fact is BR22's, on this file: where the installation has not opted
// in, a ticket that names a job and a registration are both refused, and the
// input it reads is declared where it already is (BatchEnabledEnv).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// ErrJobNotThisRuns is the store's answer to a registration that names a job
// other than the one the run's ticket ask named. It is a decision about the
// CALLER and not a failure, so the route serves it as its own refusal (R19):
// the record's job is the tie the VM's attestation is checked against (D3114),
// and an id written on another job would be an id on a record whose VM it does
// not belong to.
//
// It is declared here and not in identitywire, where the contract's own
// sentinels are, because it is not a fact of the wire: C1's route refuses a job
// that is not the record's, and no side outside this service ever reads the
// name of that refusal.
var ErrJobNotThisRuns = errors.New("credsvc: the job is not the one this run's ticket ask named")

// ErrJobAlreadyRegistered is the store's answer to a write on a record that
// already carries an id (R19): the id is written once, and a second write is
// refused rather than applied. Two writes are refused by it — a registration,
// and the re-ask that would replace the whole record and wipe the id with it
// (identity_record.go's Put) — because the record stops being re-askable once
// its job is registered, exactly as it stops being re-askable once claimed. It
// is a decision about the RUN — the launcher that lost a race, or one that
// re-created its job — and the fix is the same as any re-ask's: a new run id.
var ErrJobAlreadyRegistered = errors.New("credsvc: this run's job id is already registered")

// JobRegistration is the one write the registration makes on a run's issuance
// record: the id of the job the launcher created, written once, by the caller
// that asked for the ticket and only while the ticket is unclaimed (R19).
//
// It is declared here and not on IssuanceStore (identity_config.go), which is
// the launcher plan's C1 and no file of this task's: the store a deployment
// wires (NewIssuanceStore) implements it in this file, and a store that does
// not — the identity stub's, which decides nothing — is an installation whose
// registration is not wired, which the route answers with the contract's "not
// served" (C1).
//
// Every fence it makes is one atomic read-then-write on the record's node
// (identity_record.go's update): a caller that did not ask for the ticket is
// ErrAnotherCaller, a run with no record is ErrNoRecord, a job the record does
// not name is ErrJobNotThisRuns, an id already there is ErrJobAlreadyRegistered
// and a claimed ticket is ErrClaimed.
type JobRegistration interface {
	RegisterJob(ctx context.Context, slug, run string, job identitywire.JobName, jobID, caller string) error
}

// RegisterJob is JobRegistration over the installation's database: the record's
// id written under the three fences, from the node as it stands.
//
// The order the fences are asked in is the order a caller can do something
// about: a run nobody asked for a ticket for is told to ask for one, a caller
// that is not the record's is told the run is another's, and only then is the
// record's own content read — a job the ask did not name, an id already
// written, a ticket already claimed. Every one of them leaves the node exactly
// as it was.
//
// The caller is compared FOLDED (sameAccount) and not byte for byte, for the
// reason the store's claim compares its account that way: both spellings come
// from the deployment's own configuration, and a launcher whose roster entry
// was re-cased between the ask and the create is still the one identity that
// asked.
func (s rtdbIssuanceStore) RegisterJob(ctx context.Context, slug, run string, job identitywire.JobName, jobID, caller string) error {
	path, err := issuancePath(slug, run)
	if err != nil {
		return err
	}
	_, err = s.update(ctx, path, func(cur identitywire.Record, found bool) (identitywire.Record, bool, error) {
		switch {
		case !found:
			return cur, false, identitywire.ErrNoRecord
		case !sameAccount(caller, cur.Caller):
			return cur, false, identitywire.ErrAnotherCaller
		case cur.Job != job:
			return cur, false, ErrJobNotThisRuns
		case cur.JobID != "":
			return cur, false, ErrJobAlreadyRegistered
		case cur.Claimed.Account != "":
			return cur, false, identitywire.ErrClaimed
		}
		cur.JobID = jobID
		return cur, true, nil
	})
	return err
}

// identityJobRegister answers one registration (BR17, R19): a launcher's second
// call, made after the create returned, which writes the platform's id for the
// job the ticket ask named on that run's record.
//
// The handler is called the way the mount (I1) will call it, with the caller
// already verified, and the class fence is the contract's own table
// (identitywire.Routes) and not a second copy of it — the registration is the
// ticket ask's second half, and the classes that may be issued a run are the
// classes that may name the job it launched (D3123, R20).
//
// Every refusal of a verified caller goes through refuseRegister, which is what
// makes "each refusal writes one audit line" a property of the function rather
// than a rule each branch has to remember.
func (s *Server) identityJobRegister(ctx context.Context, w http.ResponseWriter, caller IdentityCaller, r *http.Request) {
	// The floor under the mount, as the ticket route's is: a call that reached
	// HERE did have a caller verified, so this branch owes the line below — and
	// its answer stays the contract's "not served", the code a CLI bridges on
	// (R7), because a service with no signer is exactly the installation not
	// yet given an issuer.
	if s.cfg.Identity.Signer == "" {
		s.refuseRegister(ctx, w, caller, StatusUnavailable, identitywire.RefusalNotServed, ErrorStateUnavailable,
			"this service holds no issuer, so it registers no job: the installation has not been given one yet (run `fugaro update`)")
		return
	}
	if s.cfg.Identity.Store == nil {
		// A service with an issuer and no record store is a wiring fault, and
		// deliberately NOT answered "not served", for the ticket route's reason:
		// a CLI would read that as the bridge (R7). It is the service's own
		// failure, and the line says so.
		s.refuseRegister(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"this service is not configured to register a job: an installation with an issuer but no issuance record store is broken")
		return
	}
	route, _ := identitywire.RouteFor(identitywire.JobPath)
	if !route.AdmitsClass(caller.Class) {
		// The route's own table is the contract's and not a second copy of it: a
		// mount that bound the wrong handler, or a verifier that answered a
		// class this route does not admit (a run's job account at the
		// registration), must not be a way in.
		s.refuseRegister(ctx, w, caller, StatusUnverified, identitywire.RefusalCallerNotAdmitted, ReasonNotAdmitted,
			"this route registers the job a launcher created for a run it launched: it serves a person on the installation's roster, a roster service account and the installation's orchestrator, and no other class")
		return
	}
	reg, ok := s.cfg.Identity.Store.(JobRegistration)
	if !ok {
		// A store that cannot register a job is an installation whose
		// registration is not wired — the identity stub's, which decides nothing
		// (identity_config.go). It is the contract's "not served" and not a
		// wiring fault, for the ticket route's reason read the other way: this
		// is the state I1 replaces, and a CLI bridges on the code (R7).
		s.refuseRegister(ctx, w, caller, StatusUnavailable, identitywire.RefusalNotServed, ErrorStateUnavailable,
			"this service holds no issuance record store that can register a job, so the registration is not served here (run `fugaro update`)")
		return
	}
	cl, err := readCall(r, nil)
	if err != nil {
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if len(cl.body) > maxIdentityRequestBytes {
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			fmt.Sprintf("%sthe registration's body is larger than the %d bytes this route accepts", requestRefused, maxIdentityRequestBytes))
		return
	}
	var req identitywire.JobRequest
	if err := identitywire.DecodeStrict(cl.body, &req); err != nil {
		// Strict, so a body naming a field this contract does not have is
		// refused BY NAME rather than served under a value the caller chose. The
		// reader's own error is the refusal's message, as it is at every other
		// read of a body in this service, and it names the field and the type it
		// expected and no value the body carried.
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the registration is not one JSON object of this route's fields: "+err.Error())
		return
	}
	// The record's two names, split and held to the spelling the record's path
	// is built from (checkRunRef, C1). The refusal is the splitter's own and
	// deliberately does not repeat what the body sent: the pair is the caller's
	// own bytes, and the message a launcher acts on is what the fields must be.
	slug, run, err := req.Record()
	if err != nil {
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the registration must name the run in the record's own two fields: the repository slug the ticket ask named, and the run id")
		return
	}
	switch {
	case req.Job == "" || !req.Job.Valid():
		// R19: the job is the one the ask named, in the platform's own spelling,
		// and the record's job was held to that shape before the launcher created
		// anything to name. A name of another shape is refused here rather than
		// compared, so a launcher that sent a truncated or re-spelled name is
		// told what a job's name is.
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the job must be the full resource name of the Cloud Batch job this launch created (projects/<project>/locations/<location>/jobs/<job>)")
		return
	case req.JobID == "":
		// The id is the whole of what this call writes: a registration without
		// one is a call that would leave the record exactly as the ask wrote it,
		// and the launcher would believe its run was registered.
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the registration carries no job id, and the id the platform gave the created job is the whole of what it writes")
		return
	}
	switch err := reg.RegisterJob(ctx, slug, run, req.Job, req.JobID, caller.Address); {
	case err == nil:
	case errors.Is(err, identitywire.ErrNoRecord):
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"this run has no issuance record, and a job is registered on the record its ticket ask wrote: the launcher asks for the ticket before it creates the job")
		return
	case errors.Is(err, identitywire.ErrAnotherCaller):
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"this run is another caller's record, and only the identity that asked for its ticket may register its job: a new attempt takes a new run id")
		return
	case errors.Is(err, ErrJobNotThisRuns):
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"the job this registration names is not the job this run's ticket ask named, and the VM that claims the run is held to the job the record carries")
		return
	case errors.Is(err, ErrJobAlreadyRegistered):
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"this run's job was already registered, and a job's id is written once: a job created again under the same name is another job with another id, so a new attempt takes a new run id")
		return
	case errors.Is(err, identitywire.ErrClaimed):
		s.refuseRegister(ctx, w, caller, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused,
			requestRefused+"this run's ticket was already claimed, and a job is registered before the run claims: a new attempt takes a new run id")
		return
	default:
		// The store's own failure, wrapped as a *StoreError: a node nobody could
		// read or write is this service's state and never a decision about the
		// caller (identity_config.go). Its error is logged and never answered —
		// it carries the record's path.
		s.log.Error("credsvc: a run's job id could not be registered", "run", run, "repository", slug, "error", err)
		s.refuseRegister(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"no job was registered for this run")
		return
	}
	body, err := json.Marshal(identitywire.JobResponse{})
	if err != nil {
		s.log.Error("credsvc: the registration's answer could not be built", "run", run, "repository", slug, "error", err)
		s.refuseRegister(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable,
			"the registration's answer could not be built")
		return
	}
	// The registration is recorded on the service's own line and not as an audit
	// line, for the ticket route's reason (slipGrant, audit.go's PurposeSlipGrant):
	// a StatusOK line requires a credential, and this route issues none — what it
	// writes is a record and what it answers is nothing. The job and its id are
	// the platform's own values and not secrets, so an operator reads them here;
	// the id is the fact the claim is later held to (BR2), which is what makes
	// this line worth having.
	s.log.Info("credsvc: a run's Batch job was registered",
		"run", run, "repository", slug, "job", req.Job, "job_id", req.JobID,
		"caller", caller.Address, "class", string(caller.Class))
	writeSlipAnswer(w, body)
}

// refuseRegister answers one refusal of the registration and records it, in the
// shape refuseTicket has for the ticket route: one typed refusal to the caller,
// one line to the operator, and the caller's answer the same whether or not the
// line lands (D143).
//
// It is this route's only refusal path once the caller is verified, which is
// what makes "each refusal writes one audit line" a property of the function
// rather than a rule every branch has to remember.
//
// The line is written under PurposeSlipGrant and carries the verified caller
// and nothing else, exactly as the ticket route's does and for the same two
// reasons: no purpose of audit.go's closed set names an identity route (and that
// file is no task's to edit), and the run, the repository and the job on the
// call are the caller's own bytes rather than values this service verified. An
// operator who needs those reads the service's own line of the issue beside it,
// and the exact code is on the caller's body.
func (s *Server) refuseRegister(ctx context.Context, w http.ResponseWriter, caller IdentityCaller, status int, typ, auditStatus, message string) {
	if err := s.audit.Write(ctx, AuditEvent{Caller: caller.Address, Purpose: PurposeSlipGrant, Status: auditStatus}); err != nil {
		s.log.Error("credsvc: a refused job registration could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}
