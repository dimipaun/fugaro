package batch

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// jobAccount is the service account the run's VMs run as: the account of the
// deployed job the workflow belongs to, which is the identity a Cloud Run run
// of the same workflow has (BR10, D3103, spec's requirement 2: "the run's
// identity on Batch is the same identity a Cloud Run run has").
//
// **This is the helper BR10 owns.** Until it landed an installation had one
// account, its block's (Options.ServiceAccount), and a launch whose workflow
// belonged to a shared project job was refused outright rather than run as an
// identity the project's policy did not choose. The block's single account
// stops deciding anything here: which account a job's VMs are is a fact about
// the deployed job (F10, F11), and the block cannot carry it -- it records one
// account only when the project holds exactly one, and a shared project job's
// account is not among the per-repository ones it is read from
// (internal/cli's batchBlockAccount, internal/infra's JobRunAccounts).
//
// The two classes of deployed job (R1) get their account two ways, and both
// come from the job the run stands in for rather than from the block:
//
//   - a shared project job's account is not derivable from its name -- that
//     name is written, never parsed, so no reader can recover the
//     (profile, provider, account) triple it hashes (gcp.ProjectJobName,
//     R2-5) -- so it is recorded, keyed by the job's own name
//     (Options.JobAccounts): the launcher reads it from the project's
//     published profiles, which is the one place the triple exists
//     (config.ProjectJobAccounts, internal/infra's ProjectJobs). A project
//     job this installation records no account for is refused, because
//     running it under the block's account would be exactly the identity the
//     policy did not choose.
//   - a per-repository job's account is a derivation of its own two facts: it
//     is gcp.ServiceAccountID(slug, workflow), the account the repository
//     root creates for that job and the Cloud Run job of the same workflow
//     runs as (internal/infra's spec.go), and the address is that id in this
//     installation's project. A recorded entry for that job wins, so an
//     installation that knows better than the derivation is not fought
//     (Options.JobAccounts' keys are both classes' job names).
//
// Never the default Compute Engine account: New refuses the block's account
// (checkServiceAccount) and this refuses the account a job is actually built
// with, so the rule holds for the account a run really gets rather than for
// the field it used to come from -- a record naming that account must not be
// the way around the check.
//
// The refusal is checked before the job is built and before anything is
// created, so a launch it refuses leaves no execution behind. It names the
// project job and never anything a launcher could have put in a secret's place.
func (b *Backend) jobAccount(spec backend.LaunchSpec) (string, error) {
	job := deployedJobName(spec)
	account, recorded := b.o.JobAccounts[job]
	if !recorded || account == "" {
		if spec.Job != "" {
			return "", fmt.Errorf("launching %s/%s: the workflow runs on the shared project job %s, and this installation records no account for it: a Batch job's VMs run as the account of the deployed job the run stands in for, and the block's single account is not that job's to run under; nothing was created", spec.Repo.Slug, spec.Workflow, spec.Job)
		}
		account = perRepositoryJobAccount(b.o.GCPProject, spec.Repo.Slug, spec.Workflow)
	}
	if defaultComputeServiceAccountRE.MatchString(account) {
		return "", fmt.Errorf("launching %s/%s: the account recorded for the deployed job %s is the default Compute Engine service account (%s), which is Editor on the project: a run's VMs must never run as it, so nothing was created", spec.Repo.Slug, spec.Workflow, job, account)
	}
	return account, nil
}

// perRepositoryJobAccount is the address of the account a per-repository
// deployed job runs as: gcp.ServiceAccountID(slug, workflow) in this
// installation's project. It is the account the repository root creates for
// that job and the one the Cloud Run job of the same workflow carries
// (internal/infra's spec.go), so a Batch run of that workflow runs as the
// identity a Cloud Run run of it has. It is a derivation of the job's own two
// facts, which is why a per-repository workflow needs no account recorded
// anywhere while a shared project job's does.
func perRepositoryJobAccount(project, slug, workflow string) string {
	return gcp.ServiceAccountID(slug, workflow) + "@" + project + ".iam.gserviceaccount.com"
}
