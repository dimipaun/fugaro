package batch

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// register.go is the id half of the launcher's second call (BR1, R1, D3108 as
// amended): the Google-assigned id of the job this launch has just created,
// which the launcher writes on the run's issuance record through BR17's route
// (POST /v1/identity/job, internal/credsvc's identityJobRegister).
//
// **The call is here because the id is the platform's** (F30). The job's NAME
// is the launcher's own -- JobID derives it, and the ticket ask names it before
// the create -- but what ties a VM to an execution is the id Batch assigns when
// the create returns, which no launcher can predict and no second job under the
// same name can reproduce: a job deleted and created again is another job with
// another id, and the service's link holds the claiming VM to the id the record
// carries (D3108's intent). So the launcher reads it back from the platform,
// and this package is the only side that holds a Batch client and the
// installation's project and region.
//
// **Why a read rather than the create's own answer.** This backend's Launch has
// the created job in hand (run.go) and does not register it: the registration
// is an HTTP call to the installation's credentials service, whose address and
// whose caller identity are the launcher's own facts and reach no backend
// option (internal/cli's launch_batch.go makes the call), and internal/backend's
// ExecutionRef -- the seam's answer, which batch.go and backend.go declare and
// this task does not write -- carries the job's name and no id. Reading the id
// back by name is one `batch.jobs.get`, which the service's own link needs
// anyway (R14), and it answers the same uid the create's answer carried.
//
// **A read that fails is an error and never an answer** (D143): whether the
// created job has an id is not a thing to guess at, because a record written
// with none is a record whose run can never claim, and the launcher is the only
// side left to say so.

// CreatedJobID is the id the platform assigned the Batch job at full, read back
// after the create returned: the value BR17's registration writes on the run's
// issuance record (identitywire.JobRequest.JobID).
//
// full is the job's full resource name, which the launcher computed before it
// asked for the ticket and created the job -- the name this backend's Launch
// creates the job under (JobID over the same slug, workflow and run id), so the
// three sides of the tie (the ticket ask, the registration, the job) name one
// job and not three spellings of one. It is checked here rather than trusted:
// a name no Batch job can have is refused before an API call is made for it.
//
// A job the platform does not hold is backend.ErrNotFound, the model's own word
// for it, and a job that exists and carries no id -- which the API does not
// answer, and which a fake or a future revision could -- is refused as this
// build's own failure: the registration would write an empty id, and the run
// would stop at its claim with nothing an operator could read.
func (b *Backend) CreatedJobID(ctx context.Context, full identitywire.JobName) (string, error) {
	if !full.Valid() {
		return "", fmt.Errorf("reading the id of a job: %q is not a Cloud Batch job's full resource name (projects/<project>/locations/<location>/jobs/<job>)", full)
	}
	j, err := b.batch.Projects.Locations.Jobs.Get(string(full)).Context(ctx).Do()
	if err != nil {
		return "", apiError("reading job "+string(full), err)
	}
	if j.Uid == "" {
		return "", fmt.Errorf("Batch holds %s and answers no id for it, so this run's job cannot be registered: the id is what ties the claiming VM to this execution (D3108), and a record written without one is a run that can never claim its identity", full)
	}
	return j.Uid, nil
}
