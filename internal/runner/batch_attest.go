package runner

import (
	"context"
	"fmt"
	"net/url"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// The attestation a Batch run's claim carries (BR4, R1, U2 of
// docs/batch-run-identity): the identity token of the VM the run is on, in the
// metadata server's full format.
//
// A Cloud Run run claims with the job's own Google ID token and nothing else.
// A Batch run's claim carries one thing more, because the account a Batch job
// runs as is shared by every job the launcher creates (F4): its VM's own
// identity token, which the service serves the identity to only from a VM of
// the job its record names (D3114). The full format is the whole point of the
// read rather than a detail of it — the short format carries no instance id,
// name, project or zone, and that is the fact the service's link is made of
// (U1, U2) — so the audience is asked for beside format=full.
//
// The token is a credential of this VM, so nothing prints it: it is registered
// with the run's redactor as it is read, the way the launch's seal key is, and
// it reaches no log line, no error, no audit line and no run record.

// attestationFormat is the metadata server's own value for the identity token
// that carries the instance's id, name, project and zone (U2).
const attestationFormat = "full"

// onBatch reports whether this process is one of Cloud Batch's tasks: the fact
// that says its claim must carry an attestation, and the one a launcher
// creates a job for only with this run's ticket on it (BR1, R18).
//
// The platform's own signal answers it, asked through backend.DetectPlatform --
// BATCH_TASK_INDEX and BATCH_TASK_COUNT, which Batch injects into every task,
// with Cloud Run's own signal asked first, as jobName asks the same question --
// and never FUGARO_BACKEND: that one is the launcher's statement, written into
// the job definition, so a task whose environment carries none of it is still
// one of Batch's tasks, and reading the launcher's variable as the answer let
// exactly such a task run with no session, no cap and no kill switch (the
// review finding on this change).
func (r *run) onBatch() bool {
	return backend.DetectPlatform(r.jobEnv).Backend() == backend.CloudBatch
}

// batchAttestation is what this run's claim carries beside its ticket: the
// VM's identity token in full format, read from the metadata server for the
// audience the service verifies it at, as the contract's own type for it
// (identitywire.Attestation, C1). A run that is not on Batch returns "" and
// asks the metadata server nothing, which is what a Cloud Run claim carries
// (C1: the attestation is empty for a Cloud Run launch).
//
// It is read at the claim and not cached, for the reason the job's own token
// is minted per call (metadataIDToken): what the run needs is the token the
// platform is issuing now, and the round trip against the model calls around
// it is a millisecond. The caller is the claim, and it is the caller that
// decides the audience: the service the claim is made to is the one that
// verifies it.
func (r *run) batchAttestation(ctx context.Context, audience string) (identitywire.Attestation, error) {
	if !r.onBatch() {
		return "", nil
	}
	tok, err := metadataToken(ctx, metadataAttestationURL(r.d.Env, audience))
	if err != nil {
		// Marked, so the claim's own halt can say which of the two failures
		// spent its retry (identity_claim.go's errNoAttestation): the metadata
		// server's message is this run's own sentence and never the server's
		// answer, which is the token itself when it refuses.
		return "", fmt.Errorf("%w: %v", errNoAttestation, err)
	}
	r.addSecret(tok)
	return identitywire.Attestation(tok), nil
}

// batchLaunchWithNoTicket is the refusal of a Batch task that carries neither a
// ticket nor the marker: a launch of this release gives a Batch job the ticket
// it asked the installation's credentials service for, and refuses the launch
// where it could not get one (BR1, R18), so a task with nothing on it is not one
// this installation launched -- a hand-started job, or an environment somebody
// emptied. The run has no identity to claim and no seal key to open anything
// with, and the alternative to stopping is a run on the environment ceiling
// with no session, no cap and no kill switch: nothing would account for what it
// spends and nothing could stop it.
//
// It is met at the policy read (budgetClient, where the marker's own refusal is
// met) and not at startBudgetBackend: what it reads is the same absence the
// markless arm reads as "this installation has published nothing", so a run
// that stopped there would have gone through admission, opened its provider,
// handed out a git credential and cloned the repository first -- a refusal after
// the clone is the fail-open direction M2's own rule names (projectpolicy.go).
//
// The marker's arm is not refused: it is the carrier of the release behind, and
// a job launched by it is one this runner still opens (R7's bridge). Only a job
// carrying neither mechanism -- a state no launcher of either release leaves --
// is refused, which is what Backend.Issued answers.
func batchLaunchWithNoTicket() error {
	return fmt.Errorf("this run's process is one of the cloud-batch backend's tasks (BATCH_TASK_INDEX and BATCH_TASK_COUNT are set, the pair Batch injects into every task) and its environment carries nothing a launcher provisioned this run's identity with: no identity ticket (%s is not set) and no marker for the sealed object the release before it left (%s is not set either). A Batch job is created by a launcher that asked the installation's credentials service for this run's ticket first, or not created at all (BR1), so this is not a job this installation launched, and there is no identity here to claim. The run stops rather than going on with no session, no cap and no kill switch. Upgrade the Fugaro that launches this repository's runs (brew upgrade dimipaun/tap/fugaro, then fugaro update) and launch the run again",
		identitywire.TicketEnv, BudgetTokenEnv)
}

// metadataAttestationURL is the metadata server's own endpoint for this VM's
// identity token in full format: the job's own identity endpoint, with the
// format the attestation needs beside the audience (metadataIdentityURL). It
// is separate from the read so that where the token is asked for is one
// expression a reader — and a test — can see without a call, as
// metadataIdentityURL is for the job's own.
func metadataAttestationURL(env []string, audience string) string {
	return metadataIdentityURL(env, audience) + "&format=" + url.QueryEscape(attestationFormat)
}
