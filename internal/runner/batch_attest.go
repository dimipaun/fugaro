package runner

import (
	"context"
	"fmt"
	"net/url"

	"github.com/dimipaun/fugaro/internal/backend"
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

// onBatch reports whether this run's job is on the Cloud Batch backend. The
// launcher writes FUGARO_BACKEND on every job it creates (backend.BackendEnv),
// and a Batch job says cloud-batch where the deployed job it stands in for
// says cloud-run (internal/backend/batch's jobVariables), so this is the run's
// own answer to which platform it is on — the fact that says its claim must
// carry an attestation.
func (r *run) onBatch() bool {
	return r.jobEnv(backend.BackendEnv) == backend.CloudBatch
}

// batchAttestation is what this run's claim carries beside its ticket: the
// VM's identity token in full format, read from the metadata server for the
// audience the service verifies it at. A run that is not on Batch returns ""
// and asks the metadata server nothing, which is what a Cloud Run claim
// carries (C1: the attestation is empty for a Cloud Run launch).
//
// It is read at the claim and not cached, for the reason the job's own token
// is minted per call (metadataIDToken): what the run needs is the token the
// platform is issuing now, and the round trip against the model calls around
// it is a millisecond. The caller is the claim, and it is the caller that
// decides the audience: the service the claim is made to is the one that
// verifies it.
func (r *run) batchAttestation(ctx context.Context, audience string) (string, error) {
	if !r.onBatch() {
		return "", nil
	}
	tok, err := metadataToken(ctx, metadataAttestationURL(r.d.Env, audience))
	if err != nil {
		return "", fmt.Errorf("reading this VM's identity for the claim: %w", err)
	}
	r.addSecret(tok)
	return tok, nil
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
