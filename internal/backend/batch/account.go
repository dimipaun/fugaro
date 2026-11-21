package batch

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
)

// jobAccount is the service account the run's VMs run as: the account carried
// by the deployed job the workflow belongs to, or the refusal of a workflow
// whose job has no counterpart here yet.
//
// Never the default Compute Engine account: New refuses it (checkServiceAccount),
// because a run that took it would run the workflow as the project's own
// identity rather than as the installation's.
//
// **This is the helper BR10 owns** (D3103): today an installation has one
// account, its block's (Options.ServiceAccount), and this returns it for every
// workflow. That task makes a launch whose workflow belongs to a shared project
// job run under *that* job's account with that job's environment, and a
// per-repository workflow under its own job's account, so the block's single
// account stops deciding anything. Its test (`TestTheBlocksAccountIsNotRead`)
// is exactly that: the account comes from the deployed job, not from the block.
//
// The refusal below is what stands in the way of that today, and it is why this
// helper returns an error at all: a shared project job is a Cloud Run job the
// installation deploys per profile, with the profile's own account, secrets and
// limits, and this backend builds a single-use job per run from the repository's
// own settings and has no counterpart for a profile yet. Running the
// per-repository job here instead would run the workflow as an identity the
// project's policy did not choose, so the launch is refused rather than guessed
// at -- BR10's red step is this refusal disappearing. It is checked before the
// job is built and before anything is created, and it names the project job but
// never anything a launcher could have put in a secret's place.
func (b *Backend) jobAccount(spec backend.LaunchSpec) (string, error) {
	if spec.Job != "" {
		return "", fmt.Errorf("launching %s/%s: the workflow runs on the project job %s, and Cloud Batch has no project jobs yet; nothing was created", spec.Repo.Slug, spec.Workflow, spec.Job)
	}
	return b.o.ServiceAccount, nil
}
