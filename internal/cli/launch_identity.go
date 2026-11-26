package cli

// Where a launch's ticket comes from (LM6 of docs/launcher-does-not-mint): the
// installation's credentials service, which issues one run's identity as a
// one-time ticket the launcher puts on the execution and the run claims back
// with its own Google ID token and a nonce (internal/runner's identity_claim.go,
// LM7). Where the service serves the route, the launch signs nothing itself and
// writes no object: the ticket is the whole of what the run's identity travels
// on (R7), and the launcher holds nothing that could mint one for it.
//
// The ask is C1's route (identitywire.TicketPath) and it carries the
// INGREDIENTS of the run's claims and never a claim set: the slug and the run,
// the account the execution will run as, the launcher's own name, the branch
// and the pull requests it works on, the context repositories it may read, the
// runtime it asks the identity to live, the orchestrator's attempt where a
// resident pass is re-issuing one (D3136), and the Batch job where this launch
// is about to create one. The service derives the rest — the uid, the project
// claim, the expiry — and refuses an ask it will not serve (R4).
//
// The bridge is R7's and it is exactly three answers: no service configured,
// the service's own "this revision holds no issuer"
// (identitywire.RefusalNotServed, answered 503), and the answer of a release
// behind, which is the 404/405 slipRouteAbsent already reads. On those three
// the launch signs locally as it did before and its execution carries the old
// seal marker, which a runner of an older release still opens. On every other
// answer it fails with the reason and the fix: a CLI that signed locally on one
// would mint with a role `fugaro update` has taken away and stop with IAM's own
// error in place of the sentence that names the fix.
//
// The first of those three is asked of the RUN and not of the installation: the
// ask is made only where the execution is really handed the service's address
// (executionServesTheService), so a repository the switch holds off — a root
// applied with it off, a project whose mode is off, a `vertex` repository — is
// launched exactly as main launched it, with a sealed identity and the mounts
// its job kept.
//
// The ask is retried where nothing answered it (R1): an unreachable service, a
// 5xx or its rate limit, an answer that could not be read, and an identity this
// launch could not mint are the attempts a retry is for, bounded by ticketWaits
// (thirty seconds in all, slipGrantTimeout's own bound). A refusal is never
// retried — the contract's four codes are decisions, and the runner's rule for
// the same route is the same — and asking again for the same run is safe by the
// service's own rule: before a ticket is claimed, the same caller's ask for the
// same run gets a new ticket and the old one dies (R6).
//
// R5, and the one part of it this launch does not keep. The ask is made before
// the claim this launch takes, before the declaration it writes and before the
// launch record another launcher reads (launchRun), which is the whole of what
// THIS launch writes about the run. The task object names the run id and is
// written by the caller before the launch, and the ask cannot precede it
// without preceding the guard that answers a run which already went: a resident
// pass re-issues runs that are still going (orchestrator/resident_launch.go),
// and every one of those must answer with the execution its record names rather
// than with an ask the service refuses. That is the residual R5 itself states —
// a caller that writes first — stated here rather than prevented.

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

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// maxTicketAnswerBytes bounds what the ticket route may answer, as
	// maxSlipAnswerBytes bounds the grant route's: the service is another
	// writer, and an answer nothing bounds is an answer this command renders
	// into memory.
	maxTicketAnswerBytes = 16 << 10
	// launchTicketMinBytes and launchTicketMaxBytes are the bounds the RUNNER
	// itself holds the value to (internal/runner's minSecretLen and
	// maxIdentityTicketBytes, which is maxSlipSecretBytes): a ticket outside
	// them is one the run halts over at bootstrap, where the launcher is long
	// gone and only a halt is left. The ticket the service draws is a slug, a
	// run id and thirty-two bytes of crypto/rand in hex (credsvc's newTicket),
	// well inside both — this side's floor is the shorter of what a service
	// could draw and not the run's own four bytes, because a value that short
	// is one this command would carry into no execution.
	launchTicketMinBytes = 16
	launchTicketMaxBytes = 512
)

var (
	// errLaunchTicketAbsent is the umbrella of R7's three bridge answers: there
	// is nothing to ask. It is told apart from every refusal by errors.Is, and
	// the two below tell the answers themselves apart, because an operator acts
	// on them differently: an installation with no service has nothing to run,
	// and one whose service holds no issuer or is a release behind is an
	// installation to update.
	errLaunchTicketAbsent = errors.New("this installation's credentials service issues no run ticket")
	// errLaunchTicketNotServed is the service's own answer for a revision whose
	// identity half is not configured (I1, R7's second answer).
	errLaunchTicketNotServed = fmt.Errorf("%w: this revision's identity half is not configured", errLaunchTicketAbsent)
	// errLaunchTicketReleaseBehind is the answer of a service whose release
	// does not serve this route at all (R7's third answer), which is the 404
	// slipRouteAbsent reads.
	errLaunchTicketReleaseBehind = fmt.Errorf("%w: this release does not serve the ticket route", errLaunchTicketAbsent)
	// errLaunchTicketRefused is a refusal: this caller is not one the route
	// admits, or the ask itself was refused. The fix is the installation's own
	// roster or its release, never a local mint.
	errLaunchTicketRefused = errors.New("the installation's credentials service refused the launch's ticket")
	// errLaunchTicketAnswer means the ask was made and the answer cannot be
	// used: the service could not be reached, its answer was not read back, or
	// what it carried is not a ticket this launch will put on an execution.
	// Nothing was signed and nothing was written.
	errLaunchTicketAnswer = errors.New("the installation's credentials service's ticket answer cannot be used")
)

// ticketWaits are the waits before each retry of an ask nothing answered: about
// thirty seconds in all, which is the bound R1 names (slipGrantTimeout). A
// variable so a test's waits are instant (propagationWaits' own reason: the
// durations are what a person reads, and a test cannot afford to wait for
// them), and ticketSleep is the wait itself.
var ticketWaits = []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second}

var ticketSleep = sleepFor

// launchAskTicket is the ticket ask itself, from the request's parts to the
// answer R7's bridge reads, and a package variable for the reason slipGrant is
// one (run.go): the ask is made on every launch whose installation has a
// service, and no other test may reach a service or mint an identity token.
// TestMain answers it for every test that is not about the ticket
// (main_test.go), with the answer that needs no fixture to change — a service
// that does not serve the route, which is the version skew and leaves the
// launch reading exactly the environment it read before the ticket existed. A
// test about the ticket itself replaces it.
var launchAskTicket = func(ctx context.Context, i *launchIssuer, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
	return i.askRetry(ctx, req)
}

// launchIdentity is the ID token a launch presents to the installation's
// credentials service: the launcher's own Google ID token for the service's
// URL, which is the identity the service's person class verifies (C1, LM3). It
// is the plain mint the slip and a pairing ask with, and a package variable for
// the same reason slipIdentity is one: no test may run gcloud. A resident
// service presents the token of the account it runs as instead, read from the
// metadata server (residentIdentity), and reaches this one never.
var launchIdentity = func(ctx context.Context, serviceURL string) (string, error) {
	return personIdentity(ctx, serviceURL, "a run's identity ticket is asked for")
}

// residentIdentity is the ID token a resident service presents instead: the
// token of the account the service runs AS, read from the metadata server for
// the credentials service's own URL. A container holds no gcloud and no person,
// so the mint a launcher's own identity is obtained with is not one it could
// make — and the class the service verifies is the address the token carries,
// which for this caller is its own service account (credsvc's
// VerifyOrchestrator, D3125 as amended by D3136).
//
// The audience is the same URL the ask goes to, which is the whole of what
// Cloud Run's front end checks and what the service reads a non-person token's
// audience for (checkCallerAudience): a token minted for another URL is refused
// there, so the URL this launch asks at and the URL it presents a token for are
// one value (ticketServiceURL) and never two.
func residentIdentity(ctx context.Context, serviceURL string) (string, error) {
	return buildIdentityToken(ctx, serviceURL)
}

// issuerAttemptKey is the context key the attempt a resident pass re-issues
// travels under, and issuerAttempt is what it carries.
type issuerAttemptKey struct{}

// withIssuerAttempt installs the attempt this launch re-issues (D3136), which
// the ask for the run's identity names and no other launch does.
//
// It is a value of the CONTEXT because that is the only thing a launcher of a
// resident pass can carry to the launch through orchestrator.Launcher: the
// interface hands a spec and a run id, the record the attempt is read out of
// belongs to the pass (orchestrator.State) and not to the launcher, and the
// launcher is shared by every pass of the process — so a field on it, or on the
// environment it opens, would be one tick of a service Cloud Run may run two of
// at once writing another's. Every other per-launch fact of this command
// crosses the same way (withLaunchDocument, withServiceReadCache), and this one
// is scoped to the single Launch call the carrying makes.
func withIssuerAttempt(ctx context.Context, attempt identitywire.AttemptID) context.Context {
	return context.WithValue(ctx, issuerAttemptKey{}, attempt)
}

// issuerAttemptOf is the attempt this launch's ask names: the one a resident
// pass installed, and "" everywhere else — which is what the contract wants of
// every other class (an ask that is not an orchestrator's names no attempt at
// all, and an orchestrator's ask that names none is refused by the issuer).
func issuerAttemptOf(ctx context.Context) identitywire.AttemptID {
	attempt, _ := ctx.Value(issuerAttemptKey{}).(identitywire.AttemptID)
	return attempt
}

// launchTicket is the ticket one launch holds for its run's identity: the
// value the execution carries in identitywire.TicketEnv and the run presents at
// its claim, and when the service stops serving it.
type launchTicket struct {
	value     string
	expiresAt time.Time
}

// launchIdentityAsk is what one launch's ask for its run's identity came to:
// the ticket it holds where the service issued one, and which of R7's answers
// it got where it holds none.
type launchIdentityAsk struct {
	ticket *launchTicket
	// bridge is R7's answer and "" where a ticket was issued: one of the three
	// values below, and this file's whole reading of an absent ticket.
	bridge string
}

// R7's three bridge answers, the only ones a launch signs locally on.
const (
	bridgeNoService     = "no_service"
	bridgeNotServed     = "not_served"
	bridgeReleaseBehind = "release_behind"
)

// mintedLocally reports whether this launch signs its run's identity with the
// operator's own credential, which is R7's bridge and nothing else.
func (a launchIdentityAsk) mintedLocally() bool { return a.ticket == nil }

// fromService reports whether the installation HAS a credentials service that
// answered, which is what tells a bridge the operator can act on (the service
// is behind, or holds no issuer) from an installation that never had one.
func (a launchIdentityAsk) fromService() bool {
	return a.bridge == bridgeNotServed || a.bridge == bridgeReleaseBehind
}

// launchIssuer asks one installation's credentials service for one run's
// ticket. It is one value per launch and it holds no state across launches: the
// identity it presents, the URL it presents it to, and the clock the answer's
// expiry is judged against.
type launchIssuer struct {
	service  string
	now      time.Time
	identity func(ctx context.Context, serviceURL string) (string, error)
}

// ticketUnanswered is an attempt the service did not answer, which is the one
// thing a retry is for. It carries the sentence the launch fails with once the
// waits are spent, so the reason a person reads is the same after one attempt
// and after four.
type ticketUnanswered struct{ err error }

func (u *ticketUnanswered) Error() string { return u.err.Error() }
func (u *ticketUnanswered) Unwrap() error { return u.err }

// launchTicketFor asks the installation's credentials service for this run's
// ticket, before the launch takes its claim and before it writes anything of
// the run, and answers R7's bridge where there is nothing to ask — including
// where this run is handed no address for the service at all
// (executionServesTheService).
//
// It is called by launchRun and by nothing else, so every way in — a person's
// launch, a challenger, a cloud diagnosis, the daemon's and the resident
// service's — asks the same question of the same route with the parts it has.
// account is the account the execution will run as (runAccount), derived by the
// caller because the launch's own slip names the same string and the two must
// not be able to disagree; job is the job the execution will really run on, and
// variant is the workflow it is created with — run.go's jobWorkflow of the
// spec's workflow and its account token, the same string the backend is handed
// as backend.LaunchSpec.Workflow, derived by the caller for the same reason
// account is: a Batch job's name must be the one this launch really creates.
func (e *cloudEnv) launchTicketFor(ctx context.Context, slug string, spec *task.Spec, account, variant string, job *launchProjectJob, reviewJob bool, now time.Time) (launchIdentityAsk, error) {
	// A Batch launch names its job in the ask, and has to have one to name
	// (BR1, R1): the name is the launcher's own derivation (batchJobName), over
	// the variant the create is made with, it is what the service writes on the
	// run's issuance record with the ticket, and the VM that claims the run is
	// held to a job spelled that way (BR17, BR2). R18's rule that a Cloud Run
	// launch names none is the empty value here.
	var batchJob identitywire.JobName
	if launchBackend(e.be) == backend.CloudBatch {
		full, err := batchJobName(e.lc, slug, variant, spec)
		if err != nil {
			return launchIdentityAsk{}, err
		}
		batchJob = full
	}
	// R7's bridge is R18's refusal on Batch: a job there has no carrier for the
	// sealed object a bridged launch hands over (internal/backend/batch's
	// sealedVars refuses it), so a Batch launch with no ticket is a run that
	// could hold no identity at all, and it is refused here rather than left to
	// stop at its claim with the launcher long gone. reason is the service's
	// own answer where there was one — C1's code, never its free text — because
	// a person sent to the service's log by nothing is a person who cannot tell
	// an installation to update from one to opt in (O15).
	bridged := func(bridge, reason string) (launchIdentityAsk, error) {
		if launchBackend(e.be) == backend.CloudBatch {
			return launchIdentityAsk{}, batchTicketRefusal(bridge, reason, spec)
		}
		return launchIdentityAsk{bridge: bridge}, nil
	}
	service := e.ticketServiceURL()
	if service == "" {
		// R7's first answer. The launch signs locally as it did before the
		// service existed, and nothing here is judged: an installation with no
		// service has no ticket route to be a release behind.
		return bridged(bridgeNoService, "")
	}
	served, err := e.executionServesTheService(ctx, slug, spec, job, reviewJob)
	if err != nil {
		return launchIdentityAsk{}, err
	}
	if !served {
		// R7's first answer in the other shape an installation gives it: it
		// carries the service and does not serve THIS run — the switch holds
		// this repository off (a root applied with it off, a project whose mode
		// is off, a `vertex` repository), so the job kept every mount it had and
		// a ticket would be an identity the run cannot claim (it reads the
		// address to claim at). The launch signs locally, exactly as main did.
		return bridged(bridgeNoService, "")
	}
	if err := checkSlipServiceURL(service); err != nil {
		// The rule is asked again because what is presented to this URL is a
		// bearer credential: over plain http to another host it would travel in
		// cleartext.
		return launchIdentityAsk{}, err
	}
	req, err := e.launchTicketRequest(ctx, slug, spec, account, job, batchJob)
	if err != nil {
		return launchIdentityAsk{}, err
	}
	i := &launchIssuer{service: service, now: now, identity: e.presentedIdentity()}
	ans, err := launchAskTicket(ctx, i, req)
	switch {
	case errors.Is(err, errLaunchTicketNotServed):
		return bridged(bridgeNotServed, identitywire.RefusalNotServed)
	case errors.Is(err, errLaunchTicketReleaseBehind):
		return bridged(bridgeReleaseBehind, bridgeReleaseBehind)
	case err != nil:
		return launchIdentityAsk{}, err
	}
	if !launchTicketUsable(ans.Ticket) {
		return launchIdentityAsk{}, remote(fmt.Errorf("%w: the service's answer carries a ticket that is not one this launch will put on an execution (it must be one printable value of at least %d and at most %d bytes, and the run reads it back at bootstrap), so nothing was launched; run '%s update' — the CLI and the service its installation runs are two halves of one release", errLaunchTicketAnswer, launchTicketMinBytes, launchTicketMaxBytes, selfCommand()))
	}
	if !ans.ExpiresAt.After(now) {
		// A ticket that has already died cannot be claimed: the run would stop
		// at bootstrap with no identity at all, on a launch already paid for.
		// The service's clock and this machine's are the two clocks in it.
		return launchIdentityAsk{}, remote(fmt.Errorf("%w: the service issued run %s a ticket that had already expired (%s), so it could never be claimed; check this machine's clock against the installation's, and launch again", errLaunchTicketAnswer, task.ShortRun(oneLine(spec.RunID)), ans.ExpiresAt.UTC().Format(time.RFC3339)))
	}
	return launchIdentityAsk{ticket: &launchTicket{value: ans.Ticket, expiresAt: ans.ExpiresAt}}, nil
}

// executionServesTheService reports whether the run this launch starts will
// read a credentials service address out of its own environment, which is the
// address it claims its ticket at: what this launch writes
// (credentialsServiceLaunchEnv), or what the job it lands on carries from its
// own deployment (internal/infra's projectjobs gives a shared job the address
// where its provider needs it, and a review variant always carries it).
//
// A resident service's launch judges that switch on the same config with the
// facts the installation PUBLISHED filled in (serviceConfig): the one it
// composes carries neither the block nor a repository's record, and a
// container holds no gcloud, so a repository the switch holds off must be
// judged off here rather than handed a ticket its run could never claim.
//
// A launch whose execution carries none signs locally (R7's first answer): the
// job kept its mounts, and a ticket is an identity the run could not claim.
//
// A job this launch cannot read — a backend with no job to read (Cloud Batch,
// whose launches this gate never decides: batchLaunchRefusal refuses an
// unserved one first), a workflow that is no job name (the launch's own steps
// refuse that) — is answered by the launch's own write alone. A read that
// FAILS is remote and refuses the launch: whether the run it starts has an
// identity at all is exactly what cannot be answered then, and the same read
// refuses a minting launch the same way (budgetJobBackend).
func (e *cloudEnv) executionServesTheService(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob, reviewJob bool) (bool, error) {
	jobProfile := ""
	if job != nil {
		jobProfile = job.Profile
	}
	auth, deployed := e.repoServiceAuth(ctx, spec, io.Discard)
	if len(credentialsServiceLaunchEnv(e.serviceConfig(), jobProfile, spec.Repo, auth, e.policyMode(ctx), deployed, reviewJob)) > 0 {
		return true, nil
	}
	be, ok := e.be.(jobBackendEnvReader)
	if !ok {
		return false, nil
	}
	name := launchJobName(slug, spec, job)
	if name == "" {
		return false, nil
	}
	env, exists, err := be.JobFullEnv(ctx, name)
	if err != nil {
		return false, remote(fmt.Errorf("whether the job %s this launch would start carries a credentials service could not be read, so the launch is refused: %w", name, err))
	}
	return exists && env[envCredentialsServiceURL] != "", nil
}

// ticketServiceURL is the installation's credentials service as THIS launch
// reaches it: the block the config it judges the switch on publishes
// (configuredCredentialsServiceURL, the one place the block is read). "" where
// there is none, which is R7's first answer.
func (e *cloudEnv) ticketServiceURL() string {
	return configuredCredentialsServiceURL(e.serviceConfig())
}

// serviceConfig is the config this launch judges the credentials service's
// switch on: its own, or — for the one launcher that holds no record of the
// installation's (a resident service, whose composed config is the
// deployment's own facts: orchestrator_serve.go's base) — that config with the
// facts the switch reads filled in from the record the installation publishes
// (launchService): the service's block where its own config has none, and the
// repositories' records of the roots that were applied with the service on,
// where an entry of its own carries none. Nothing else is taken from the
// published copy, and a record the launch's own entry holds wins whatever it
// says — a `false` is an apply that ran with the switch off, and it has to
// travel — which is the rule the publish keeps (localcfg's mergeRepoRecords).
//
// That record is what makes a resident launch's answer the same one a person's
// launch reaches: a repository whose root was applied with the service off is
// held off for both, so neither hands it an address and neither asks for a
// ticket it could not claim.
func (e *cloudEnv) serviceConfig() *localcfg.Config {
	if e.launchService == nil || e.lc == nil {
		return e.lc
	}
	lc := *e.lc
	if lc.CredentialsService == nil {
		lc.CredentialsService = e.launchService.CredentialsService
	}
	repos := make(map[string]localcfg.Repo, len(e.launchService.Repos)+len(lc.Repos))
	for name, r := range e.launchService.Repos {
		repos[name] = r
	}
	for name, r := range lc.Repos {
		if r.CredentialsServiceReady == nil {
			r.CredentialsServiceReady = e.launchService.Repos[name].CredentialsServiceReady
		}
		repos[name] = r
	}
	lc.Repos = repos
	return &lc
}

// presentedIdentity is the identity this launch presents to the service: the
// seam a resident service sets, and the launcher's own everywhere else.
func (e *cloudEnv) presentedIdentity() func(ctx context.Context, serviceURL string) (string, error) {
	if e.identity != nil {
		return e.identity
	}
	return launchIdentity
}

// launchTicketRequest is the ask itself, built from the run's own parts: the
// same derivations the sealed token's claims were built from (budgetBranch,
// budgetPRs, launchIdentityWindow), so the ticket a run holds and the claims it
// would have held name one branch, one set of pull requests and one runtime.
func (e *cloudEnv) launchTicketRequest(ctx context.Context, slug string, spec *task.Spec, account string, job *launchProjectJob, jobName identitywire.JobName) (identitywire.TicketRequest, error) {
	me, err := e.lc.Me(ctx)
	if err != nil {
		return identitywire.TicketRequest{}, userErr("%v", err)
	}
	branch, err := budgetBranch(spec)
	if err != nil {
		return identitywire.TicketRequest{}, err
	}
	window, err := e.launchIdentityWindow(ctx, slug, spec, job)
	if err != nil {
		return identitywire.TicketRequest{}, err
	}
	req := identitywire.TicketRequest{
		Slug: slug, Run: spec.RunID, Account: account, Launcher: me,
		Branch: branch, PullRequests: budgetPRs(spec), ContextRepos: spec.ContextRepos,
		MaxRuntimeSeconds: int(window / time.Second), Attempt: e.issuerAttempt,
	}
	if e.issuerAttempt != "" {
		// An orchestrator's ask may name no context repository at all (D3136),
		// and LM24's check refuses one that does: the reference it carries is
		// what makes it an orchestrator's ask, and the run it re-issues is
		// signed for the run and its branch alone. The plan's decision, not this
		// file's — and a run the resident re-issues reads no context repository.
		req.ContextRepos = nil
	}
	// Job is the full resource name of the Cloud Batch job this launch is about
	// to create, and the empty value for a Cloud Run launch — every launch of
	// that backend names none (R18): the service writes it on the run's record,
	// and the VM that claims the run is held to it (BR1, BR17).
	req.Job = jobName
	return req, nil
}

// runAccount is the service account THIS EXECUTION will run as, which is the
// account the ask names and the one the service holds to this repository's job
// (R4), and the same string the run's slip names: one derivation for both, so a
// launch cannot ask as one account and write a slip its execution can never
// claim (slipIsThisRuns, D1937).
//
// A per-repository job is one account per (slug, workflow) (gcp.ServiceAccountID,
// the pair the backend derives the job's own name from); the shared project job
// a switched profile's launch runs on is one account per (profile, provider,
// account) (gcp.ProjectServiceAccountID, the derivation internal/infra's
// ProjectJobs deployed that job's account with); and a review launch runs on the
// review-only variant, whose account carries the variant's suffix over the
// coding job's own axis (credsvc.ReviewServiceAccountID, the spelling
// internal/infra deployed the variant with). A review-only variant carries
// neither its profile and provider nor its account on the launchProjectJob
// (projectJobReviewFor keeps its name and timeout alone), so the pair is read
// from the coding job it was derived from, or from the installation's own
// implicit review job where the launch resolved no shared coding job.
func (e *cloudEnv) runAccount(ctx context.Context, slug string, spec *task.Spec, codingJob, job *launchProjectJob, reviewJob bool, variant string) (string, error) {
	var id string
	switch {
	case reviewJob && codingJob != nil:
		id = credsvc.ReviewServiceAccountID(codingJob.Profile, codingJob.Provider, infra.ReviewVariantAccount(codingJob.Account))
	case reviewJob:
		provider, ok := runner.ProviderForSlug(slug, spec.Repo)
		if !ok {
			return "", userErr("this launch was made for the slug %s, and that is not the slug of the repository its task names (%s) under any provider kind (%s): the account it runs as is the review job's of the repository's own provider, and there is none to derive. Nothing was launched", slug, spec.Repo, strings.Join(runner.ProviderKinds(), ", "))
		}
		j, ok := e.implicitReviewJob(provider)
		if !ok {
			return "", userErr("this launch resolved the installation's implicit review-only job of the provider %s and the installation composes none: nothing was launched", provider)
		}
		id = credsvc.ReviewServiceAccountID(j.Profile, provider, j.Account)
	case job != nil:
		id = gcp.ProjectServiceAccountID(job.Profile, job.Provider, job.Account)
	default:
		id = gcp.ServiceAccountID(slug, variant)
	}
	return id + "@" + e.lc.GCPProject + ".iam.gserviceaccount.com", nil
}

// askRetry makes the ask, and makes it again while nothing answered it (R1).
// A refusal is never retried — the contract's four codes are decisions (a
// ticket already claimed is claimed for good, a caller the route does not admit
// is not admitted by asking again), and the runner's rule for the same route is
// the same — and a retry that lands asks for the SAME run, which the service
// answers with a new ticket and the death of the old one (R6), so a retry after
// an answer that was lost is not a second identity.
func (i *launchIssuer) askRetry(ctx context.Context, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
	for attempt := 0; ; attempt++ {
		out, err := i.ask(ctx, req)
		if err == nil {
			return out, nil
		}
		var unanswered *ticketUnanswered
		if !errors.As(err, &unanswered) {
			return identitywire.TicketResponse{}, err
		}
		if attempt >= len(ticketWaits) {
			return identitywire.TicketResponse{}, unanswered.err
		}
		if werr := ticketSleep(ctx, ticketWaits[attempt]); werr != nil {
			return identitywire.TicketResponse{}, werr
		}
	}
}

// ask makes one attempt: the identity this launch presents, the contract's ask,
// and the answer read back strictly. Every failure it answers with a
// ticketUnanswered is one a retry is for; everything else is final.
//
// Nothing here can put a value a caller sent into an error: the identity token
// is never formatted into one, and the answer is read for its status, its size
// and its shape alone.
func (i *launchIssuer) ask(ctx context.Context, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
	var out identitywire.TicketResponse
	actx, cancel := context.WithTimeout(ctx, slipGrantTimeout)
	defer cancel()
	identity, err := i.identity(actx, i.service)
	if err != nil {
		// The mint is this launch's own credential and it is read per attempt,
		// as the slip's is read per call: a metadata server that was not there
		// yet is an attempt nothing answered (the runner's own reading of its
		// claim's mint).
		return out, i.unanswered(remote(fmt.Errorf("%w: this launch could not mint the identity it presents to the installation's credentials service, so no ticket was asked for: %s. '%s doctor' reads the installation's service, and '%s update' re-applies it; then launch again", errLaunchTicketAnswer, oneLine(err.Error()), selfCommand(), selfCommand())))
	}
	body, err := json.Marshal(req)
	if err != nil {
		// The ask is strings, counts and ids, so this cannot fire: refused
		// rather than sent half-built, and the reason names no value.
		return out, errors.New("the ticket ask could not be built")
	}
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, strings.TrimSuffix(i.service, "/")+identitywire.TicketPath, bytes.NewReader(body))
	if err != nil {
		return out, errors.New("the ticket ask could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	// The slip's own client, and not a second one: no redirect (the request
	// carries this launcher's ID token, and a followed 307 would carry it to
	// whatever host a Location named), no cookie jar, one bound.
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return out, i.unanswered(remote(fmt.Errorf("%w: the installation's credentials service could not be reached, and a launch on an installation that has one takes its run's identity there: no ticket was issued and nothing started; check that the service is deployed and answering — '%s doctor' reads the installation's service and '%s update' re-applies it — and launch again", errLaunchTicketAnswer, selfCommand(), selfCommand())))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTicketAnswerBytes+1))
	if err != nil {
		return out, i.unanswered(remote(fmt.Errorf("%w: the service's answer could not be read, so this launch holds no ticket and nothing started: launch again, and '%s doctor' reads the installation if it happens twice", errLaunchTicketAnswer, selfCommand())))
	}
	// Read before the status is judged, and bounded: a refusal's body is where
	// the code the operator acts on lives, and nothing reads an unbounded body
	// to find it.
	if slipRouteAbsent(resp.StatusCode) {
		return out, fmt.Errorf("%w: the service answered %d, which is the answer of a service whose release does not serve this route", errLaunchTicketReleaseBehind, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return out, launchTicketRefusal(req.Slug, req.Run, resp.StatusCode, data)
	}
	if len(data) > maxTicketAnswerBytes {
		return out, i.unanswered(remote(fmt.Errorf("%w: the service's answer is larger than a ticket's, so nothing was started", errLaunchTicketAnswer)))
	}
	if err := identitywire.DecodeStrict(data, &out); err != nil {
		// Strictly, and generously for nothing: a field this release does not
		// know is a ticket read out of a document it does not understand.
		return out, i.unanswered(remote(fmt.Errorf("%w: the service's answer is not one of this contract's (it is read strictly: one JSON object, and no field this release does not know), so nothing was started; run '%s update' — the CLI and the service its installation runs are two halves of one release", errLaunchTicketAnswer, selfCommand())))
	}
	return out, nil
}

// unanswered wraps an attempt nothing answered, which is the one thing a retry
// is for.
func (i *launchIssuer) unanswered(err error) error { return &ticketUnanswered{err: err} }

// launchTicketRefusal reads a refusal body and turns it into the sentence a
// person is owed: what the service refused, and the fix. It is pairServiceRefusal's
// shape for this route, and its rules are the same — the service's own message
// is never repeated (another writer's text, reaching a terminal and a log line;
// the code is what this CLI acts on), and the code is printed through safetext's
// rule for another writer's text.
//
// The four codes branch exactly as C1's own note says this CLI may: not_served
// is one of R7's three bridge answers and is answered as absence; every other
// code is a refusal with the fix, and none of them may be read as a bridge.
func launchTicketRefusal(slug, run string, status int, body []byte) error {
	e, err := credsvc.ReadWireError(status, body)
	if err != nil {
		// A refusal whose body is not one of the contract's: damage to the
		// answer rather than a decision, so it is an attempt nothing answered
		// and a retry is made — the sentence below is what a person reads once
		// the waits are spent.
		return (&launchIssuer{}).unanswered(remote(fmt.Errorf("%w: the service refused the ask (%d) and the refusal is not one of this contract's bodies (one JSON object naming a code): run '%s doctor' — it reads the installation's service and what it was deployed with — and '%s update', which re-applies it; the service's own log has the answer it wrote", errLaunchTicketAnswer, status, selfCommand(), selfCommand())))
	}
	code := safetext.Field(e.Error)
	switch e.Error {
	case identitywire.RefusalNotServed:
		// The service's own answer for a revision whose identity half is not
		// configured (I1): R7's second bridge answer, and the sentence below is
		// never printed — the launch signs locally instead.
		return fmt.Errorf("%w: the service answered %s", errLaunchTicketNotServed, code)
	case identitywire.RefusalCallerNotAdmitted:
		return fmt.Errorf("%w: %s — a run's ticket is asked for by the people this installation's roster names, and this machine's credential is not one of them; '%s update' is what renders that roster into the service, and '%s doctor' prints the service's own answer if the refusal stays", errLaunchTicketRefused, code, selfCommand(), selfCommand())
	case identitywire.RefusalNoLaunchScope:
		return fmt.Errorf("%w: %s — this machine's credential is admitted, and the repository %s is not one it may launch; the roster the service verifies against names the repositories each caller may launch, and '%s update' is what renders it", errLaunchTicketRefused, code, oneLine(slug), selfCommand())
	case identitywire.RefusalIdentityRefused:
		// The one refusal a person acts on by launching something else: the run
		// already holds an identity record this ask cannot take — another
		// caller's record for the same run, an account that is not this run's
		// job's account, a ticket already claimed, or a runtime beyond the
		// installation's maximum (LM1, LM2, LM24). A run id is one identity, so
		// the fix is a run id that is nobody's yet.
		return fmt.Errorf("%w: %s — run %s cannot be given an identity: its record already names one this ask cannot take (another launcher's record for the same run, an account that is not this run's job's account, a ticket already claimed, or a runtime beyond the installation's maximum). Launch a new run — a new run id is a new identity, and a follow-up to this run is what continues its work — and '%s doctor' prints the record if the refusal stays", errLaunchTicketRefused, code, task.ShortRun(oneLine(run)), selfCommand())
	default:
		return fmt.Errorf("%w: %s, which this release knows no ticket refusal by: run '%s update', which converges a CLI and the service its installation runs — they are two halves of one release — and the service's own log has the refusal it wrote", errLaunchTicketRefused, code, selfCommand())
	}
}

// ticketRedact returns err with the ticket removed from its text, and it is
// sealRedact's rule for the other secret a launch hands an execution: a backend
// error may quote the request it was given, and the ticket is in that request.
// Unwrap still reaches err, so errors.Is finds backend.ErrRejected and the
// launcher drops its claim exactly as before. A nil ticket, or a nil err, is
// returned as is.
func ticketRedact(err error, t *launchTicket) error {
	if err == nil || t == nil || t.value == "" {
		return err
	}
	return redactedError{err: err, text: agent.Redact(err.Error(), []string{t.value})}
}

// launchMinterRefusal is R7's bridge's own failure, told as the fix it has: a
// launch the service answered "not served" (or a release behind) signs locally,
// which needs the operator's fugaroTokenMinter role on the signer account — and
// an installation whose service issues run tickets has no such row to hold any
// more (LM14 takes the people's row away once the issuer answers its probe). So
// what an operator meets here is a release skew and not a missing grant, and
// IAM's own words are kept because they are what the role's absence looks like
// (pairMinterRefusal's rule for the same bridge).
func launchMinterRefusal(lc *localcfg.Config, err error) error {
	return userErr("%s; a launch on an installation with no identity service mints the run's identity with your own credential, which needs the fugaroTokenMinter role on the signer account %s (the installation's launchers and operators hold it) — and this installation's credentials service is there, so the update that deploys it has taken that role away and its service is not serving the ticket route: run '%s update', which converges the whole installation, and launch again", oneLine(err.Error()), lc.Budget.TokenSigner, selfCommand())
}

// launchTicketUsable reports whether a ticket is one this launch will put on an
// execution: one printable value, within the bounds internal/runner's
// BackendFromEnv holds it to. The journey of a value that fails this is one way
// — the launcher is long gone when the run reads it, and all that is left there
// is a halt at bootstrap — so the shape is asked before the ticket is carried
// anywhere. A ticket's own spelling names the slug and the run it is for, which
// is the service's business and its claim's to judge; this side asks only what
// the run will ask.
func launchTicketUsable(v string) bool {
	if len(v) < launchTicketMinBytes || len(v) > launchTicketMaxBytes {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return false
		}
	}
	return true
}
