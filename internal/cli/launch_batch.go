package cli

import (
	"context"
	"io"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/task"
)

// batchLaunchRefusal is the launch's own arm for the Batch backend: the one
// thing a launch there needs that a launch on Cloud Run does not, judged
// before the claim is taken, before a budget token is minted and before any
// byte of the task is written -- so a launch it refuses leaves no task, no
// token, no claim and no execution behind (BR11, D3104, D3107).
//
// It refuses a launch whose repository the credentials service does not serve.
// A Batch job mounts nothing: `batch.secrets` was a job's Secret Manager
// mounts, and BR11 took the mount away with the rest of the code half of R10
// (internal/backend/batch/secrets.go), so a run there holds a model credential
// only where the credentials service carries its calls -- the same switch a
// Cloud Run run's job is judged by (credentialsServiceLaunchEnv, D146/D147).
// A launch the service does not serve would therefore start a Task whose first
// model call has nothing to make it with, which is the failure D143 exists to
// keep out of a run: the launcher, where a person is watching, is where it is
// refused.
//
// **Cloud Run is not judged here.** A Cloud Run run's job still carries its own
// mounts where the service does not serve it (G17: the flip drops them only for
// a repository the service serves), so an unserved launch there is the launch
// it always was. What BR11 removes is Batch's second path to a credential, and
// with it the reason the two backends could disagree.
//
// **Why the repository and not the job.** The question is asked of the same
// function the launch hands the execution's environment with, and with the
// repository's own job -- the shape proxiedRepo's last question has, and the
// same reader pair (repoServiceAuth and policyMode). The two answers are one:
// an address this gate found is an address the execution is handed, and one it
// did not is a run this launch would start without a credential. A shared
// project job is served on the same switch a repository's own job is (B5.2),
// and the question of which job this launch runs on is launchProjectJobFor's,
// asked there and by the launch itself rather than a second time here.
//
// io.Discard is the reader's warning writer, the way contextReposRefusal's and
// proxiedRepo's are: the launch reads the same two documents and words the one
// warning such a read can report (repoServiceAuth).
func batchLaunchRefusal(ctx context.Context, env *cloudEnv, spec *task.Spec) error {
	if launchBackend(env.be) != backend.CloudBatch {
		return nil
	}
	auth, deployed := env.repoServiceAuth(ctx, spec, io.Discard)
	if len(credentialsServiceLaunchEnv(env.lc, "", spec.Repo, auth, env.policyMode(ctx), deployed, false)) > 0 {
		return nil
	}
	return userErr("a run of %s cannot be launched on cloud-batch: a Batch job mounts no secret at all, so the model credential this run's calls are made with has to come from the credentials service, and this installation's does not serve it (D3104, D3107). The service serves a repository where the installation carries it, that repository's record says a root of it was applied with the switch on, and its agent.auth is one the service serves -- agent.auth: cascade, or agent.auth: api-key or oauth under a project policy whose budget mode observes or enforces. Run '%s update' in your own terminal to deploy the service where the installation can carry it, then set agent.auth: cascade in %s's fugaro.yaml and run '%s update --repo %s' to apply that repository's root with the switch on -- or launch this repository's runs on the Cloud Run backend, where its job carries its own model credential ('%s backends use cloud-run')",
		spec.Repo, selfCommand(), spec.Repo, selfCommand(), spec.Repo, selfCommand())
}
