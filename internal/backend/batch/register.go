package batch

// register.go is BR1's id call (docs/batch-run-identity, R1 and R19): the two
// facts a launcher needs about the job it creates, and nothing else.
//
// The launcher names the job in its ticket ask BEFORE the create -- the name is
// its own (JobID, names.go), so it can -- and the platform answers the job's id
// only when the create returns. The id is what ties the VM to the execution
// (F30): a job deleted and created again under the same name is another job
// with another id, so the registration BR17 serves writes that id on the run's
// issuance record and the claim's link holds the VM to it. Neither fact is a
// field of backend.ExecutionRef, which is every backend's and holds no platform
// id: the id is read back from the job this launcher just created.
//
// The CLI declares the interface it reads these through
// (internal/cli/launch_batch.go's batchIdentityBackend), as it does for the
// job's own environment (jobBackendEnvReader): a backend that names no job
// leaves a launch exactly as it was.

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// LaunchJobName is the full resource name of the job a launch of this run
// creates: projects/<project>/locations/<location>/jobs/<job>, the platform's
// own spelling of a job and the name the ticket ask carries (identitywire
// JobName, whose Valid asks the shape).
//
// It is the name Launch really creates, derived from the same three parts the
// create derives its job id from (JobID, names.go): the ask, the create and
// the read-back below have to name one job, and the way they agree is that
// there is one derivation and this is it.
func (b *Backend) LaunchJobName(slug, workflow, runID string) identitywire.JobName {
	return identitywire.JobName(b.location() + "/jobs/" + JobID(slug, workflow, runID))
}

// LaunchJobID reads back the id the platform gave a job this launcher created,
// which is the second half of the tie the run's claim is served on (R1, BR17's
// JobRequest.JobID).
//
// It is one read of one job and not a keystroke of the create: the create's own
// answer carries the id into a backend.ExecutionRef this seam does not hold,
// and a read that fails is answered rather than guessed -- an id this launcher
// invented would be an id no VM's attestation is checked against, which is
// worse than a registration the launcher reports it could not make.
func (b *Backend) LaunchJobID(ctx context.Context, name identitywire.JobName) (string, error) {
	if !name.Valid() {
		return "", fmt.Errorf("reading back the job %q: it is not a Cloud Batch job's name", string(name))
	}
	got, err := b.batch.Projects.Locations.Jobs.Get(string(name)).Context(ctx).Do()
	if err != nil {
		return "", apiError("reading back the job "+string(name), err)
	}
	if got.Uid == "" {
		return "", fmt.Errorf("reading back the job %s: the platform answers no id for it, and the id is what a run's attestation is checked against", string(name))
	}
	return got.Uid, nil
}
