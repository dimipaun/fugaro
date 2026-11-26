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
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// batchLaunchRefusal is the launch's own arm for the Batch backend: the things
// a launch there needs that a launch on Cloud Run does not, judged before the
// claim is taken, before a budget token is minted and before any byte of the
// task is written -- so a launch it refuses leaves no task, no token, no claim
// and no execution behind (BR11, D3104, D3107).
//
// **It refuses a repository the credentials service does not serve** (BR11). A
// Batch job mounts nothing: `batch.secrets` was a job's Secret Manager mounts,
// and BR11 took the mount away with the rest of the code half of R10
// (internal/backend/batch/secrets.go), so a run there holds a model credential
// only where the credentials service carries its calls -- the same switch a
// Cloud Run run's job is judged by (credentialsServiceLaunchEnv, D146/D147).
// A launch the service does not serve would therefore start a Task whose first
// model call has nothing to make it with, which is the failure D143 exists to
// keep out of a run: the launcher, where a person is watching, is where it is
// refused.
//
// **It refuses an installation that has not opted in** (BR1, R3, R16, R17). A
// Batch run's identity is a ticket, and the ticket is the credentials service's
// -- a service an installation has only because `fugaro update` deployed it for
// a published opt-in fact (D3110 as amended, R2). A launch on a machine whose
// block carries no fact, or carries the written withdrawal, is a launch whose
// run could hold no identity at all, and the fact is the launch's own to read:
// R3's rule is that no launch reads the cloud to decide anything.
//
// Where the two disagree -- a machine whose local copy still says "in" after an
// operator withdrew it -- this gate admits the launch and the SERVICE refuses it
// at the ticket ask, which is the authority R17 names; a stale copy follows at
// its next `fugaro update` (O19). This one is the faster message, not the
// authority.
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
	if err := batchOptInLaunchRefusal(env.lc, spec); err != nil {
		return err
	}
	auth, deployed := env.repoServiceAuth(ctx, spec, io.Discard)
	if len(credentialsServiceLaunchEnv(env.lc, "", spec.Repo, auth, env.policyMode(ctx), deployed, false)) > 0 {
		return nil
	}
	return userErr("a run of %s cannot be launched on cloud-batch: a Batch job mounts no secret at all, so the model credential this run's calls are made with has to come from the credentials service, and this installation's does not serve it (D3104, D3107). The service serves a repository where the installation carries it, that repository's record says a root of it was applied with the switch on, and its agent.auth is one the service serves -- agent.auth: cascade, or agent.auth: api-key or oauth under a project policy whose budget mode observes or enforces. Run '%s update' in your own terminal to deploy the service where the installation can carry it, then set agent.auth: cascade in %s's fugaro.yaml and run '%s update --repo %s' to apply that repository's root with the switch on -- or launch this repository's runs on the Cloud Run backend, where its job carries its own model credential ('%s backends use cloud-run')",
		spec.Repo, selfCommand(), spec.Repo, selfCommand(), spec.Repo, selfCommand())
}

// batchOptInLaunchRefusal is BR1's half of the gate above: the block's opt-in
// fact, read from the launch's own config, or the refusal of the launch with the
// fix that changes it (R3, R17, O15).
//
// The sentence is validate's and doctor's own (batchOptInRefusal and
// batchOptInFix, BR7): one field read by three callers, and a launch that
// spelled its own reason would send an operator somewhere the other two do not
// name. What the launch adds is only what a launch knows -- that nothing was
// created, and that a Batch run's identity is a ticket the service issues, so
// there is no launch to make without the fact.
//
// It is asked after the backend comparison, so a Cloud Run launch never reads
// it: the fact is Batch's alone and a config with no `batch:` block is every
// installation that never chose the backend.
func batchOptInLaunchRefusal(lc *localcfg.Config, spec *task.Spec) error {
	if lc == nil || lc.Batch.OptedIn() {
		return nil
	}
	return userErr("a run of %s cannot be launched on cloud-batch: %s. %s -- and a Batch run's identity is a ticket the installation's credentials service issues, so there is no launch to make without the fact and nothing was created; the selection publishes the fact, and '%s update' applies the compute root and deploys the service with it on (R2, R17)",
		spec.Repo, batchOptInRefusal(lc), batchOptInFix(lc), selfCommand())
}

// batchJobName is the full resource name of the Cloud Batch job THIS launch is
// about to create, which is what the ticket ask names (BR1, R1): JobID over the
// run's own parts -- the backend's own derivation (internal/backend/batch's
// names.go) -- under the installation's project and region.
//
// variant is the workflow the create is made with, which is not
// spec.Workflow: run.go hands the backend backend.LaunchSpec.Workflow =
// jobWorkflow(spec.Workflow, spec.OAuthToken), and a named Claude account joins
// its token to the workflow there (gcp.WorkflowVariant) -- the string both
// backends name a job by and label a job with. A name derived from the plain
// workflow would ask for a ticket for a job this launch never creates, on the
// one axis where the two differ, and the id read-back that follows would find
// no job at all.
//
// It is computed here, before the create, because the ask precedes the job: the
// service writes the name on the run's issuance record with the ticket, and the
// VM that claims the run is held to a job of that name (BR17, BR2). One
// expression, used by the ask and by the registration that follows the create
// (registerBatchJob), so the two cannot name two jobs.
func batchJobName(lc *localcfg.Config, slug, variant string, spec *task.Spec) (identitywire.JobName, error) {
	if lc == nil {
		return "", userErr("this launch holds no installation config, and a Batch launch's ticket ask names the job it is about to create: nothing was launched")
	}
	name := identitywire.JobName("projects/" + lc.GCPProject + "/locations/" + lc.Region + "/jobs/" + batch.JobID(slug, variant, spec.RunID))
	if !name.Valid() {
		return "", userErr("this launch's Batch job would be named %s, and that is not a Cloud Batch job's full resource name (projects/<project>/locations/<location>/jobs/<job>, [a-z0-9-] throughout, which is what the ticket ask, the registration and the run's claim are all held to): nothing was launched", name)
	}
	return name, nil
}

// batchTicketRefusal is R7's bridge read the other way, for a Batch launch
// (BR1, R18): every one of the three answers a Cloud Run launch signs locally on
// is, here, the refusal of the launch.
//
// A bridged launch carries the identity of the release behind -- a sealed object
// the launcher mints with its own credential, handed over as a marker -- and a
// Batch job has no carrier for it: the only place a launch's variable can go on
// this backend is the job's definition, which is what the sealed entry is
// refused for (internal/backend/batch's seal.go). So a Batch launch with no
// ticket is a run that could hold no identity at all, and it is refused before
// the create rather than left to fail at its claim, where the launcher is long
// gone.
//
// The three answers are told apart because an operator acts on them
// differently: an installation with no service has to be given one (and opted
// in), and one whose service holds no issuer or is a release behind is an
// installation to update. reason is the service's own answer where it gave one
// -- C1's refusal code for the two the service answers, R7's own name for the
// route it does not serve, and "" where no service was reached at all -- and it
// is printed because a refusal that named no reason would send an operator to
// the service's log for a fact the service already told this machine (O15). It
// is the code and never the service's free text: that text is another writer's
// (launchTicketRefusal's own rule).
func batchTicketRefusal(bridge, reason string, spec *task.Spec) error {
	fix := "'" + selfCommand() + " update' converges the whole installation -- the CLI, the service it runs and the policy the service reads -- and launch again"
	said := ""
	if reason != "" {
		said = " The service answered " + safetext.Field(reason) + "."
	}
	switch bridge {
	case bridgeNoService:
		return userErr("a run of %s cannot be launched on cloud-batch with no identity: this machine reaches no credentials service for its runs, and a Batch run's identity is a ticket that service issues -- a Batch job has no carrier for the sealed identity an older launcher mints here, so there is nothing else this launch could hand the run. An operator opts the installation in with '%s backends use cloud-batch' and then '%s update' deploys the service and applies the compute root with it on",
			spec.Repo, selfCommand(), selfCommand())
	case bridgeNotServed:
		return userErr("a run of %s cannot be launched on cloud-batch: this installation's credentials service is deployed and its revision holds no issuer, so it would issue no ticket -- and a Batch job has no carrier for the sealed identity an older launcher mints here.%s "+fix+" (if the refusal stays, '%s doctor' reads the service the installation runs)",
			spec.Repo, said, selfCommand())
	default:
		return userErr("a run of %s cannot be launched on cloud-batch: this installation's credentials service is a release behind and does not serve the ticket route, so it would issue no ticket -- and a Batch job has no carrier for the sealed identity an older launcher mints here.%s "+fix,
			spec.Repo, said)
	}
}

// batchJobIDReader is the backend's own id call (internal/backend/batch's
// register.go, CreatedJobID): the Google-assigned id of the job this launch has
// just created, which BR17's registration writes on the run's issuance record.
//
// The launcher reaches it through the backend seam by assertion and not by a
// second field on it: internal/backend's ExecutionRef -- what Launch answers
// with -- carries the job's name, its log URL and no id, and that seam is
// nobody's file here; a backend that cannot answer this call is one whose
// launches this launcher cannot register.
type batchJobIDReader interface {
	CreatedJobID(ctx context.Context, full identitywire.JobName) (string, error)
}

// registerBatchJob is BR17's registration, the launcher's second call: after the
// create returned -- and on the branch where its outcome is unknown, where a job
// the create may have made is read back and registered the same way -- the id
// the platform gave the job is written on the run's issuance record, so the
// record names the execution the service will serve the run's identity to (BR1,
// R1, D3108).
//
// It runs on the launch's own path and nowhere else -- launchRun calls it twice,
// once on each of those branches -- and it is what BR4's claim is held to: a
// record whose job carries no id is a record the link refuses, so a launch that
// skipped this call would start a VM that could never claim its identity, which
// no later --retry can put right: BR17's route writes an id once, on the record
// the ticket ask wrote, and only the launcher that holds the claim may write it.
//
// **A job that cannot be registered is stopped.** The create has happened by
// the time this runs, so a failure here leaves a Batch job with nothing that can
// ever claim it: its VM would run, be billed and stop at its claim. The job is
// cancelled before the launch fails, and a cancellation that failed is said out
// loud (launchWarn) with the job's name, because the one thing left is an
// operator deleting it. A platform that holds no job of the name at all is the
// one failure that is not this: errNoBatchJob, nothing to cancel and nothing to
// report, because the create made nothing.
func registerBatchJob(ctx context.Context, env *cloudEnv, slug, variant string, spec *task.Spec) error {
	if launchBackend(env.be) != backend.CloudBatch {
		return nil
	}
	reader, ok := env.be.(batchJobIDReader)
	if !ok {
		return userErr("this launch cannot register the Batch job it created: the backend it launched on answers no job id, and BR17's registration is what ties the run to the execution the service serves its identity to. Run '%s update' -- the CLI and the backends it ships are one release -- and launch again", selfCommand())
	}
	full, err := batchJobName(env.lc, slug, variant, spec)
	if err != nil {
		return err
	}
	id, err := reader.CreatedJobID(ctx, full)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return remote(fmt.Errorf("%w: %s", errNoBatchJob, full))
		}
		cancelBatchJob(ctx, env, full, "its id could not be read")
		return remote(fmt.Errorf("the Batch job %s could not be read back after the create, so this run was not registered and nothing can claim its identity: %s. The job was cancelled, so its VM does not keep running", full, oneLine(err.Error())))
	}
	req := identitywire.JobRequest{Slug: slug, Run: spec.RunID, Job: full, JobID: id}
	if err := env.registerJob(ctx, req); err != nil {
		cancelBatchJob(ctx, env, full, "it could not be registered")
		return err
	}
	return nil
}

// errNoBatchJob is what the id read-back answers when the platform holds no job
// of the name this launch derived: the create made nothing. It is the ordinary
// answer to an ambiguous create -- a timeout or a dropped connection whose
// request never landed -- and it is told apart from a read-back that failed for
// another reason, which is a job that exists and cannot be named: the first has
// nothing to cancel, nothing to warn about and no identity lost, and the second
// leaves a VM that would run, be billed and stop at its claim.
var errNoBatchJob = errors.New("the platform holds no Batch job of this launch's name")

// registerJob makes BR17's registration call itself: the contract's body, the
// launcher's own identity, and the refusal read back the way the ticket ask's is
// (launchTicketRefusal), because it is the same route family and the same
// caller.
//
// There is no retry. The ask retries because an ask nothing answered leaves the
// run exactly where it was; this call is the second half of a create that has
// already happened, and a second attempt after an answer that was lost would be
// refused by the record's own fence (the id is written once, ErrJobAlreadyRegistered)
// -- so a lost answer reads as a failure and the job is cancelled, which is the
// safe side: nothing claims a job nobody registered (R1).
func (e *cloudEnv) registerJob(ctx context.Context, req identitywire.JobRequest) error {
	service := e.ticketServiceURL()
	if service == "" {
		return userErr("this launch cannot register the Batch job it created: this machine's config names no credentials service to register it with, so no job was created for a reason this launch should have refused first. Run '%s doctor' and '%s update', then launch again", selfCommand(), selfCommand())
	}
	if err := checkSlipServiceURL(service); err != nil {
		return err
	}
	body, err := json.Marshal(req)
	if err != nil {
		// The body is strings and ids, so this cannot fire: refused rather than
		// sent half-built, and the reason names no value.
		return errors.New("the registration's body could not be built")
	}
	actx, cancel := context.WithTimeout(ctx, slipGrantTimeout)
	defer cancel()
	identity, err := e.presentedIdentity()(actx, service)
	if err != nil {
		return remote(fmt.Errorf("this launch could not mint the identity it registers the run's Batch job with, so the job the create returned is not registered and nothing can claim the run's identity: %s. '%s doctor' reads the installation's service and '%s update' re-applies it", oneLine(err.Error()), selfCommand(), selfCommand()))
	}
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, strings.TrimSuffix(service, "/")+identitywire.JobPath, bytes.NewReader(body))
	if err != nil {
		return errors.New("the registration's body could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	// The ticket ask's own client, and not a second one (slipClient).
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return remote(fmt.Errorf("the installation's credentials service could not be reached to register the run's Batch job, so nothing can claim the run's identity: %s", oneLine(err.Error())))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSlipAnswerBytes+1))
	if err != nil {
		return remote(fmt.Errorf("the service's answer to the registration could not be read, so nothing can claim the run's identity: launch again, and '%s doctor' reads the installation if it happens twice", selfCommand()))
	}
	if resp.StatusCode != http.StatusOK {
		return batchRegistrationRefusal(req, resp.StatusCode, data)
	}
	return nil
}

// batchRegistrationRefusal reads a registration's refusal body and turns it
// into the sentence a person is owed, in launchTicketRefusal's shape and under
// its rules: the service's own message is never repeated (another writer's
// text), the code is what this CLI acts on, and every code carries the fix.
//
// The codes are C1's four, and the one a launcher meets here is
// identity_refused: the route refuses a registration whose run has no record,
// whose caller is not the record's, whose job is not the one the ask named,
// whose id is already written and whose ticket is already claimed -- all of
// them "this run id cannot be this job's", and all of them fixed by a run id
// that is nobody's yet (R19).
func batchRegistrationRefusal(req identitywire.JobRequest, status int, body []byte) error {
	e, err := credsvc.ReadWireError(status, body)
	if err != nil {
		return remote(fmt.Errorf("the service refused the registration of run %s's Batch job (%d) and the refusal is not one of this contract's bodies (one JSON object naming a code): run '%s doctor' -- it reads the installation's service and what it was deployed with -- and '%s update', which re-applies it; the service's own log has the answer it wrote", task.ShortRun(oneLine(req.Run)), status, selfCommand(), selfCommand()))
	}
	code := safetext.Field(e.Error)
	switch e.Error {
	case identitywire.RefusalNotServed:
		return userErr("the installation's credentials service refused the registration of run %s's Batch job: %s — it was deployed with no issuer or is a release behind, so it registers no job; '%s update' converges the CLI and the service its installation runs",
			task.ShortRun(oneLine(req.Run)), code, selfCommand())
	default:
		return userErr("the installation's credentials service refused the registration of run %s's Batch job: %s — the job's id is written once, on the record its ticket ask wrote, by the identity that asked and only before the run claims, so this run id cannot be this job's (its record already names another launcher, another job, another id or a claimed ticket). Launch a new run — a new run id is a new identity — and '%s doctor' prints the record if the refusal stays", task.ShortRun(oneLine(req.Run)), code, selfCommand())
	}
}

// cancelBatchJob stops the job a launch created and could not register, or
// could not read the id of: nothing can claim its identity, so its VM would run,
// be billed and stop at the run's claim. The cancellation is best-effort and its
// failure is warned about rather than refused -- the launch is already failing
// -- and the warning names the job, which is the one thing an operator needs to
// delete it by hand.
//
// It is given the launcher's own name for the job and not the reference a
// create answered with, because the branch whose create's outcome is unknown has
// no reference at all: the name the ask and the registration were made over is
// the one the job really has.
func cancelBatchJob(ctx context.Context, env *cloudEnv, full identitywire.JobName, why string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := env.be.Cancel(cctx, string(full)); err != nil {
		fmt.Fprintf(launchWarn, "the Batch job %s was created, %s, and it could not be cancelled (%s): delete it in your own terminal, so its VM stops being billed\n", full, why, oneLine(err.Error()))
	}
}
