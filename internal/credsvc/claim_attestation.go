// claim_attestation.go is BR2 of docs/batch-run-identity: the attestation a
// Batch run's claim is served on (R1, R18, D3108, D3114).
//
// A run on a Batch job claims as every other run claims — the job's own Google
// ID token, the ticket and the nonce — with one thing more: the identity token
// of the VM it is on. The account alone proves the JOB and never the execution
// (F4), because every job one launcher creates runs as the same job service
// account, so the identity is served only where the link (C1's
// identitywire.VMIdentity, BR3) says the VM that presented the attestation is a
// VM of the job the record names.
//
// This file is the claim route's half of that decision, and not the link's:
// CheckClaimAttestation (C1's, in identity_config.go) judges the claim's own
// shape against the record, and checkClaimVM below asks the link its one
// question and maps the answer to the refusal the caller is served. Which VM a
// token names, and which fact of the platform says so, are the link's business
// and Check 830's measurement (U1, O1, D3102 as amended): nothing here parses
// the attestation, and nothing here decides what a link must read.

package credsvc

import (
	"context"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// claimNoAttestation is what a claim on a record that names a job is refused
// with when it carries no attestation, or carries something that is no
// identity token: C1's rule is one refusal for both spellings of the fault
// (ErrClaimAttestation, D3114), and the sentence names the field and never what
// the call sent (R7).
const claimNoAttestation = requestRefused + "this run's record names the Batch job it was launched on, so its claim must carry the claiming VM's own identity token in the attestation field: the account proves the job and never the execution, and a claim without the VM's token would be served on the account check alone"

// claimVMRefusal is one refusal of the VM question: the status the caller is
// answered, the contract's own code, and the sentence the run halts on.
type claimVMRefusal struct {
	status  int
	code    string
	message string
}

// checkClaimVM asks the link whether the VM that presented attestation is a VM
// of the job the record names, and answers nil when the run's identity may be
// signed. It is the whole of what BR2 decides about the VM: the link's false is
// a refusal of the caller, its error is this service's own failure, and an
// installation with no link serves no claim on a job's record at all.
//
// It is asked only for a record that names a job, and only once the record's
// own checks have passed: the question costs a read of the platform (Check 830,
// U1), and a claim that is not this run's — a forged ticket, another account —
// is refused without making one.
func (s *Server) checkClaimVM(ctx context.Context, rec identitywire.Record, attestation identitywire.Attestation) *claimVMRefusal {
	if !rec.NamesAJob() {
		// A Cloud Run run's record names no job, and its claim is the claim of
		// the release behind: the account check is the whole of what serves it
		// (R18), and the link is asked nothing.
		return nil
	}
	if rec.JobID == "" {
		// The launcher's two calls are the ask and the registration, and the id
		// is the registration's write (R19). A record that names a job and
		// carries no id is a launcher that died between them: there is no id to
		// hold the VM's job to, so the question has no answer and the run
		// stops, which is the safe side (C1's Record.JobID).
		return &claimVMRefusal{status: StatusUnverified, code: identitywire.RefusalIdentityRefused,
			message: "the job this run was launched on is not registered: a launcher names the job it is about to create, creates it and registers its id before the run claims, and this record carries no id — the run must be launched again"}
	}
	link := s.cfg.Identity.VMIdentity
	if link == nil {
		// An installation whose link is not wired answers the contract's "not
		// served" (C1): a VM nothing proved is no VM of any job, and the claim
		// must not be served on the account check alone (D3114). Nothing falls
		// back.
		return &claimVMRefusal{status: StatusUnavailable, code: identitywire.RefusalNotServed,
			message: "this service holds no link from a VM to its Batch job: a claim on a job's record cannot be judged here, so it is not served — run fugaro update"}
	}
	ok, err := link.VMsJob(ctx, attestation, rec.Job, rec.JobID, rec.At)
	switch {
	case err != nil:
		// A read that failed is this service's own failure and never a decision
		// about the caller (C1's VMIdentity): a run refused for the wrong
		// reason is a run nobody can debug. The error's own text stays off the
		// line — what the read named is the platform's — and the type is
		// carried instead, as the store's failures above are (refusallog.go).
		s.log.Error("credsvc: the VM could not be checked against its job", "error_class", errorClass(err))
		return &claimVMRefusal{status: StatusUnavailable, code: ErrorStateUnavailable,
			message: "the VM this claim came from could not be checked against the job the run was launched on"}
	case !ok:
		// One answer for the four the link refuses with — another job's VM, a
		// finished job, a job created outside the launch's bound, a job whose id
		// is not the registered one — because the link answers them with one
		// false: it is the safe side of every one, and the fix is the same (a
		// run launched again).
		return &claimVMRefusal{status: StatusUnverified, code: identitywire.RefusalIdentityRefused,
			message: "the attestation is not this run's: a Batch run's identity is served only to a VM of the job its launch named, live and created within the launch's bound, and the VM this claim came from is not that job's — the run must be launched again"}
	}
	return nil
}
