package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/safetext"
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

// batchIdentityBackend is the backend's half of a Batch launch's identity
// (BR1, R1 and R19): the job the launch is about to create, and the id the
// platform gave it once it exists. internal/backend/batch implements it
// (LaunchJobName and LaunchJobID, its register.go), and the CLI declares the
// interface here for the reason jobBackendEnvReader is declared here: a
// backend that names no job -- every other one, and a test's codec -- leaves a
// launch exactly as it was, and no launch reads a platform through a concrete
// type.
type batchIdentityBackend interface {
	// LaunchJobName is the full resource name of the job a launch of this run
	// creates, which is the name the ticket ask carries.
	LaunchJobName(slug, workflow, runID string) identitywire.JobName
	// LaunchJobID reads back the id the platform gave that job once the create
	// returned, which is what BR17's route registers (D3108, F30).
	LaunchJobID(ctx context.Context, name identitywire.JobName) (string, error)
}

// batchJobName is the job this launch is about to create, "" where it creates
// none: a launch on another backend, or on a backend that names no job. It is
// the name the ticket ask carries and the name the read-back below is made
// with, and it has to be the job the create really makes -- one derivation, and
// it is the backend's own (LaunchJobName).
//
// A name this contract does not carry is refused rather than asked with: the
// issuer holds the ask's job to the platform's spelling, and the reason a
// launch could not name its own job is a fact about the installation's block,
// which is what the refusal names.
func (e *cloudEnv) batchJobName(slug string, spec *task.Spec, variant string) (identitywire.JobName, error) {
	if launchBackend(e.be) != backend.CloudBatch {
		return "", nil
	}
	be, ok := e.be.(batchIdentityBackend)
	if !ok {
		return "", nil
	}
	name := be.LaunchJobName(slug, variant, spec.RunID)
	if !name.Valid() {
		return "", userErr("the job this launch would create is %s, and that is not a Cloud Batch job's name (projects/<project>/locations/<location>/jobs/<job>): the installation's own block is what it is built from, and nothing was created; '%s doctor' reads the block, and '%s update' applies it", string(name), selfCommand(), selfCommand())
	}
	return name, nil
}

// batchIdentityRefusal is the installation's half of a Batch launch's identity,
// judged with the other launch gates -- before the claim, before a budget token
// is minted, before the ticket is asked and before any byte of the run is
// written (BR1, R17, D3110 and D3111).
//
// It refuses a Batch launch whose installation's batch block does not carry the
// opt-in fact. That fact is the installation's own answer to whether Batch runs
// are wanted, and it is what the service's deploy input (`FUGARO_BATCH_ON`,
// R17) and every grant R16 removes are rendered from: a launch that went ahead
// without it would be a job whose VMs exist and whose service refuses the
// registration and the claim, which is the failure a launch refuses at the
// terminal where a person is watching.
//
// The sentence and the fix are batchOptInRefusal's and batchOptInFix's, which
// `fugaro doctor` and `fugaro validate` answer with too (BR7): one field, one
// fact, one thing an operator is told to do about it. Nothing here reads the
// cloud to decide it (R3), and nothing here asks a question: the fix is a
// command the operator runs in their own terminal.
func batchIdentityRefusal(env *cloudEnv, spec *task.Spec) error {
	if launchBackend(env.be) != backend.CloudBatch {
		return nil
	}
	if env.lc != nil && env.lc.Batch.OptedIn() {
		return nil
	}
	return userErr("a run of %s cannot be launched on cloud-batch: %s. %s", spec.Repo, batchOptInRefusal(env.lc), batchOptInFix(env.lc))
}

// batchTicketRefusal is R7's bridge where the backend is Batch, and it is a
// refusal and not a fall-back (BR1, F21, R17).
//
// The bridge exists for Cloud Run: an installation that carries no service, one
// whose service holds no issuer and one whose service is a release behind are
// three answers a launch reads as "there is nothing to ask", and it signs the
// run's identity locally exactly as it did before the service existed -- the
// execution then carries the seal marker, and a runner of an older release
// opens it.
//
// On Batch there is no such fall-back to take: a local mint hands the execution
// the seal marker, and a Batch job has no carrier for it (AB9, R5,
// docs/budget-token-channel/spec.md), so the launch would be refused by the
// backend after its token was minted, its object written and its claim taken.
// It is refused here instead, before anything is created, with the reason and
// the fix, and what it refuses is exactly the state R17 describes: an
// installation whose CLI and service are not yet one release, or one that has
// not opted in.
func batchTicketRefusal(spec *task.Spec, bridge string) error {
	why := map[string]string{
		bridgeNoService:     "this launch is handed no credentials service address at all (the installation carries none, or its switch holds this repository off)",
		bridgeNotServed:     "the service answered that this revision holds no issuer",
		bridgeReleaseBehind: "the service answered that its release does not serve this route",
	}[bridge]
	if why == "" {
		why = "this installation issues it no ticket"
	}
	return userErr("a run of %s cannot be launched on cloud-batch: %s, and a Batch run's identity is the ticket its launcher asks the installation's credentials service for -- with the full name of the job it is about to create (docs/batch-run-identity/contract-batch-claim.md) -- so a launch that holds none is refused here, before anything is created, rather than signing the run's identity itself: a local mint hands the execution the budget token's seal key, and a Batch job has no carrier for it (docs/budget-token-channel/spec.md). '%s update' brings this CLI and the service its installation runs to one release, and it is what deploys the service and renders this installation's opt-in into it; '%s doctor' reads both", spec.Repo, why, selfCommand(), selfCommand())
}

// registerBatchJob registers the job this launch just created -- with the id
// the platform gave it -- through the credentials service's registration route
// (BR1, R19, D3108). It is the second half of the tie the run's claim is served
// on: the ticket ask named the job, and the id is what tells the job it named
// from a job re-created under the same name (F30).
//
// It runs only for a launch that holds a ticket for a named job: a Cloud Run
// launch registers nothing (its record names no job, R18), and a launch that
// got no ticket never reached the create.
//
// A registration that fails WARNS and does not refuse the launch. The job is
// already running when this is called, so refusing here would leave an
// execution nobody was told about and a claim to release, to save a run that
// halts at its own claim with the reason (internal/runner's identity_claim.go);
// the warning is where a person reads what happened, and it names the two
// commands that read the installation.
func (e *cloudEnv) registerBatchJob(ctx context.Context, slug string, spec *task.Spec, ask launchIdentityAsk, warn io.Writer) {
	if ask.ticket == nil || ask.job == "" {
		return
	}
	be, ok := e.be.(batchIdentityBackend)
	if !ok {
		return
	}
	// The hold's own deadline is not the registration's: this is one read and
	// one call, and a launch whose hold is nearly spent still owes the run's
	// record its id.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), slipGrantTimeout)
	defer cancel()
	short := task.ShortRun(oneLine(spec.RunID))
	jobID, err := be.LaunchJobID(rctx, ask.job)
	if err != nil {
		fmt.Fprintf(warn, "warning: run %s's job %s was created and its id could not be read back, so the run's claim is not served: %s; '%s doctor' reads the installation\n", short, ask.job, oneLine(err.Error()), selfCommand())
		return
	}
	if err := e.askRegisterJob(rctx, slug, spec, ask.job, jobID); err != nil {
		fmt.Fprintf(warn, "warning: run %s's job %s was created and its id was not registered with the installation's credentials service, so the run's claim is not served: %s; '%s doctor' reads the service\n", short, ask.job, oneLine(err.Error()), selfCommand())
	}
}

// askRegisterJob is the registration itself: identitywire.JobPath, the route
// BR17 serves, presented with the same identity the ticket ask was presented
// with (R19: only the launcher that asked for the ticket may write its record's
// id, and the service decides that from the record and the caller).
//
// Nothing here can put a value the service sent into an error: the refusal's
// code is read through safetext's rule for another writer's text, as the
// ticket ask's refusals are, and the body is bounded.
func (e *cloudEnv) askRegisterJob(ctx context.Context, slug string, spec *task.Spec, job identitywire.JobName, jobID string) error {
	service := e.ticketServiceURL()
	if service == "" {
		return errors.New("this installation carries no credentials service address")
	}
	if err := checkSlipServiceURL(service); err != nil {
		return err
	}
	identity, err := e.presentedIdentity()(ctx, service)
	if err != nil {
		return fmt.Errorf("this launcher could not mint the identity the registration is presented with: %s", oneLine(err.Error()))
	}
	body, err := json.Marshal(identitywire.JobRequest{Slug: slug, Run: spec.RunID, Job: job, JobID: jobID})
	if err != nil {
		return errors.New("the registration could not be built")
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(service, "/")+identitywire.JobPath, bytes.NewReader(body))
	if err != nil {
		return errors.New("the registration could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return fmt.Errorf("the service could not be reached (%s)", oneLine(err.Error()))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxTicketAnswerBytes+1))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if e, rerr := credsvc.ReadWireError(resp.StatusCode, data); rerr == nil {
			return fmt.Errorf("the service answered %d (%s)", resp.StatusCode, safetext.Field(e.Error))
		}
		return fmt.Errorf("the service answered %d, and the refusal is not one of this contract's bodies", resp.StatusCode)
	}
	return nil
}
