package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/watch"
)

// The launcher's half of the project-centric job model (design §4.1, plan
// task 3.4 step 1): which Cloud Run job a launch runs on.
//
// A launch runs on the shared project job of the profile its workflow selects
// — one job for every repository admitted to that profile — when, and only
// when, the workflow is a `checkout: clone` one. A baked workflow keeps the
// per-repository job it has always had until phase 4 ends (design §4.4), and
// so does every launch of a project that has published no profiles: that is
// what keeps this change invisible to an installation that has not migrated,
// down to the request body.
//
// The job's name is computed from the *published policy* — the document the
// runner matches CLOUD_RUN_JOB against (internal/runner's projectJobCandidates)
// — and never from the local config's profiles: a launcher that resolved the
// profile from anywhere else could launch a job the runner would then refuse
// to place, halting every run of it. The profile's own provider and the Claude
// account it serves are part of that name (plan task 3.2), and
// config.ProjectJobAccounts is the one function the deployer, the runner and
// this launcher compute the account with.

// launchProjectJob is the shared project job a launch runs on, and the two
// things about it a launch needs.
type launchProjectJob struct {
	// Name is the job's own name (gcp.ProjectJobName), as Cloud Run deployed
	// it.
	Name string
	// Profile is the profile's name, for messages.
	Profile string
	// Timeout is the job's own task timeout: the profile's timeout_class plus
	// the slack every job's template carries (internal/infra's ProjectJobs).
	// It bounds this run's execution, and it is the budget token's window: a
	// launch on a long job gets a token that outlives it, and the same launch
	// on a short one does not.
	Timeout time.Duration
}

// launchConfigWhereTheTreeGivesNone is the repository's own document for a
// launch whose working tree could not answer with one: checkoutParse answers
// nil for two states that are not one case — the working directory is not
// repo's checkout at all (a launch from a machine that does not hold it, and
// the daemon, whose working directory is whatever its unit was started from),
// or it is and the working tree cannot give the file — and this is the ordered
// decision that tells them apart.
//
// Two of the launch's own facts answer before any document is looked for, and
// neither is a refusal to launch: a provider no shared job serves is decided
// from the same fact the checkout path decides it from (D908), and a project
// that published no profile has nothing to decide — its per-repository job is
// not a guess but the only job there is, so a launch that cannot read a
// document is left on it rather than refused. (nil, nil) is that answer, and
// it is the same answer for a ref whose tree carries no document at all: a
// repository with no document selects no clone workflow and no profile, so the
// per-repository job is the answer rather than a fallback (spec.md §2, §7).
//
// Two readers of a launch ask it, and they must agree: the job decision
// (launchProjectJobFor, which reads the profile from the document) and the
// launch's own gates (freshLaunchGates, run.go, which read the workflow's kind
// and checkout mode from it before they judge a single image — a checkout:
// clone workflow is judged on the installation's base image and has no build
// record for a derived image's gates to find, image_refresh.go). The read at
// the forge is memoized per (repo, ref) in the command's own scope
// (withServiceReadCache, which the launch installs before its two readers
// ask), so the two cost one mint: a read mint is an act the credentials
// service audits (D904, spec.md §6).
func (e *cloudEnv) launchConfigWhereTheTreeGivesNone(ctx context.Context, spec *task.Spec) (*config.Config, error) {
	// The provider is the decision's first question (D908): no shared job
	// serves Bitbucket (infra.SharedJobProvider — a Bitbucket credential cannot
	// be scoped to one project's repositories, the owner's ruling of
	// 2026-10-15), so for such a repository the per-repository job is not a
	// guess about a document nobody read: it is the same answer the checkout
	// path gives for the same repository, from the same fact. It is asked of
	// the repository's own provider rather than of the profile's because there
	// is no profile to read here, and it is asked through localRepoProvider,
	// which needs no checkout (F3): the comparison the caller makes would
	// dereference a nil co, and the local config's entry is the one source that
	// answers without a document.
	//
	// Only a provider this command knows and no shared job serves rules the
	// shared job out. An unknown one does not: the local entry's provider is
	// optional (localcfg), and the slug's own answer is the checkout's
	// git.provider, which a launch with no checkout cannot read — so ruling
	// the shared job out on the entry's silence would let a launch past the
	// document below onto a job the policy did not choose (D905). It falls
	// through instead, and the refusals below answer as for any other
	// provider.
	provider := e.localRepoProvider(spec.Repo)
	if provider != "" && !infra.SharedJobProvider(provider) {
		return nil, nil
	}
	// The read below is only the question "has this project published profiles
	// at all", and it is asked before any document is looked for because it
	// decides whether there is anything to decide: with no profile published
	// the per-repository job is not a guess — it is the only job there is, and
	// every launch runs on it — so a launch that cannot read a document is left
	// on it rather than refused. A policy this CLI cannot read is not refused
	// here either: that is the launch gate's and the runner's refusal, in their
	// own words, and it is not this decision's — a launcher that calls jobs.run
	// itself must not be stopped by a job it resolves
	// (TestPolicySinceGateIsNotOnTheRunnerPath).
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil || !found || p == nil || len(p.Profiles) == 0 {
		return nil, nil
	}
	// Then the document, from the source this launch has. Where a checkout of
	// the repository is here at all, only the second state above is left (the
	// working tree cannot give the file, missing or unparseable) and the ref's
	// file is read out of its objects (D909) and resolved exactly as the
	// working tree's would have been, so the rest of the decision and every
	// gate after it is unchanged. Where the machine holds no checkout of the
	// repository anywhere, its own objects cannot answer and the document is
	// read at the forge — as the operator themselves, through the
	// installation's credentials service (source 3, spec.md §2, §5; D902).
	if root := checkoutRoot(ctx, spec.Repo); root != "" {
		d, resolved := launchDocumentOf(ctx)
		if !resolved {
			d = refDocumentOf(ctx, root, spec.Ref)
		}
		if d.err != nil {
			return nil, cannotReadConfigAtRef(spec.Repo, spec.Ref, d.err)
		}
		return d.cfg, nil
	}
	return e.repoConfigAtTheRef(ctx, spec)
}

// launchProjectJobFor resolves the project job spec runs on, and returns nil
// for the per-repository path — the job of (slug, WorkflowVariant(workflow,
// token)), which the backend resolves itself. It never writes and never
// launches: it decides, and every refusal of the launch itself is elsewhere.
func (e *cloudEnv) launchProjectJobFor(ctx context.Context, spec *task.Spec) (*launchProjectJob, error) {
	// The workflow's own checkout: says whether this is a clone workflow, and
	// which profile it selects. Both are read from the repository's own
	// fugaro.yaml, and checkoutParse answers nil for two states that are not
	// one case: the working directory is not repo's checkout at all (fugaro
	// run --repo ... from a machine that does not hold it), or it is and the
	// working tree cannot give the file. Where a checkout is here and its tree
	// has no document, this launch has already resolved the ref for every
	// reader of it (withLaunchDocument): checkoutParse answers that document,
	// so the gates the launch passed and the job decided here are judged by one
	// file. What is left — the document read out of the checkout's own objects,
	// and the read at the forge for a machine that holds no checkout at all —
	// is launchConfigWhereTheTreeGivesNone's ordered decision, and the launch's
	// own gates ask it too, so both judge one file.
	co := checkoutConfig(ctx, spec.Repo)
	if co == nil {
		// A launch with no config in hand is an ordered decision
		// (launchConfigWhereTheTreeGivesNone), and its answer is nil for every
		// state that selects nothing: a provider no shared job serves, a
		// project that published no profile, and a ref whose tree carries no
		// document.
		//
		// The cache is installed here for a caller that reached this decision
		// directly (a test, or a launch shape that installed none): this
		// decision has a second reader of the same document two gates further
		// in (projectJobAgentAuth), and a mint is an act the credentials
		// service audits. The command's own launches install one before their
		// gates, which run first (finishLaunch, the daemon's Launch).
		ctx = withServiceReadCache(ctx)
		cfg, err := e.launchConfigWhereTheTreeGivesNone(ctx, spec)
		if err != nil {
			return nil, err
		}
		if cfg == nil {
			return nil, nil
		}
		co = cfg
	}
	w, ok := co.Workflows[spec.Workflow]
	if !ok || w.Checkout != config.CheckoutClone {
		return nil, nil
	}
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil {
		return nil, policyReadErr(err)
	}
	if !found || p == nil {
		return nil, nil
	}
	profile := w.JobProfileOrDefault()
	pr, err := p.ProfileFor(profile)
	if err != nil {
		// A profile the policy does not define is not this launcher's to
		// refuse, and falling back to the per-repository job is not falling
		// back to another profile: an explicit selection the policy does not
		// define is refused at run start by the runner's own check
		// (internal/runner's checkJobProfile, task 3.1), and the default
		// profile is undefined only in a policy that defines no profiles at
		// all — an installation that has not migrated, which runs on its own
		// jobs exactly as it did.
		return nil, nil
	}
	// A project job serves exactly its profile's provider: the provider is an
	// axis of the job's name, and the runner binds the run's slug — computed
	// with the repository's own provider — against the job it runs on. A
	// repository on another provider stays on its per-repository job, decided
	// here before any claim is taken or budget token minted.
	//
	// And a profile of a provider no shared job serves is no job at all, so a
	// repository that selects one — its provider agrees with the profile's, and
	// the runner is content because the policy defines the profile — stays on
	// its per-repository job too. In phase 3 that is every Bitbucket profile:
	// internal/infra's ProjectJobs skips one (a Bitbucket credential cannot be
	// scoped to one project's repositories, the owner's ruling of 2026-10-15,
	// so no shared job holds one), and infra.SharedJobProvider is the one place
	// both sides read that fact. Without it this launch computed the name of a
	// job no installation has, spent the claim and the budget token, and had
	// Cloud Run answer 404 — the backend's missing job — for a repository that
	// is supposed to keep the job it has always had (design §4.5, D5 as
	// amended; docs/project-layer.md).
	provider := e.repoProvider(spec.Repo, co)
	if provider != pr.Provider || !infra.SharedJobProvider(pr.Provider) {
		return nil, nil
	}
	// The repository must be admitted to the profile, not only to the
	// project's allow_repos the launch gate checked (run_allowlist.go's
	// profileAdmissionRefusal, the launcher's half of the profile admission
	// the runner makes at run start). This is the first point at which the
	// launcher knows which profile that is, and the profile's list can be the
	// narrower of the two, so a repository whose own fugaro.yaml selects a
	// profile it is not admitted to would otherwise reach Cloud Run, take the
	// claim and mint the token, and stop only in the runner.
	if err := profileAdmissionRefusal(p, profile, provider, spec.Repo); err != nil {
		return nil, err
	}
	// The run's own credential, re-resolved over the layer the run itself
	// carries rather than read off co — the checkout's own, leniently made
	// resolution, which a repeated run's stored layer can legitimately
	// disagree with — and a credential that cannot be resolved is refused
	// rather than guessed at (projectJobAgentAuth).
	auth, err := e.projectJobAgentAuth(ctx, spec)
	if err != nil {
		return nil, err
	}
	// The account axis of the job this launch targets, which is also the gate
	// on the Claude credential a shared job holds (plan task 3, design §4.4):
	// an OAuth run lands on the job of the account its own credential selects —
	// and on no other — while every other kind of run targets the profile's
	// no-account job, and whichever job that is, the token its container holds
	// must be one whose own policy list covers this repository.
	account, err := projectJobAccountFor(p, profile, pr, auth, spec.OAuthToken, provider, spec.Repo)
	if err != nil {
		return nil, err
	}
	// The timeout_class of a decoded policy is a duration: projectpolicy's
	// strict decode validates every profile (validProfiles → validTimeoutClass)
	// before this read can return one, which is why the error is dropped here
	// as it is in internal/infra's ProjectJobs, the function that deployed the
	// job with this same number.
	timeout, _ := config.ParseDuration(pr.TimeoutClass)
	return &launchProjectJob{
		Name:    gcp.ProjectJobName(profile, pr.Provider, account),
		Profile: profile,
		Timeout: timeout + backend.TaskTimeoutSlack,
	}, nil
}

// projectJobIndex places a run's Cloud Run job, for the readers that show it
// (plan task 3.5 step 2: watch's JSON gains profile, provider and account
// beside the job).
//
// The name a run's launch.json records is the only thing about the job anyone
// wrote down, and it spells none of the three out. So the triple is *computed*
// — every (profile, provider, account) the project's published policy defines,
// with gcp.ProjectJobName, matched against the name — and never parsed out of
// it (R2-5: derive truncates the readable part, profile names hold hyphens, an
// api-key job's account segment is empty, and "fugaro-p-" is also the legacy
// stem of an owner named "p", so a shape test would misread a per-repository
// job as a project job).
//
// The document it is computed from is the *published policy*, which is the one
// internal/runner matches CLOUD_RUN_JOB against (runner.ProjectJobOfPolicy is
// that same function, so a reader and the runner cannot disagree), and not the
// local config's profiles: a launcher's shared config drops them, and the jobs
// were deployed from the policy in any case.
//
// The read is made once, when watch starts, and it never fails the command: a
// project that has published no policy, or whose policy node cannot be read,
// still gets its runs — each with the job its own launch.json records and
// without the triple, which is what the JSON's absent fields say.
type projectJobIndex struct {
	p   *projectpolicy.Policy // the published policy; nil when there is none
	err error                 // the read's error, kept for ref's own rule below
}

// newProjectJobIndex reads the published policy from the client watch already
// holds. A read of the node is not a new credential and not a new connection:
// the same database, the same read the launcher and the runner make.
func newProjectJobIndex(ctx context.Context, db *rtdb.Client) *projectJobIndex {
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil {
		return &projectJobIndex{err: err}
	}
	if !found {
		return &projectJobIndex{}
	}
	return &projectJobIndex{p: p}
}

// ref is job as a watch row's job: the name, and the triple it was computed
// from when the published policy defines it. A job the policy does not define
// — a per-repository job, one of another installation, one a policy no longer
// describes — keeps its name and nothing else, rather than a triple guessed
// from its shape.
func (i *projectJobIndex) ref(job string) watch.JobRef {
	ref := watch.JobRef{Job: job}
	if i == nil || i.err != nil || i.p == nil {
		return ref
	}
	profile, provider, account, ok := runner.ProjectJobOfPolicy(i.p, job)
	if !ok {
		return ref
	}
	ref.Profile, ref.Provider, ref.Account = profile, provider, account
	return ref
}

// repoProvider is repo's git provider kind as repoSlug resolves it: the local
// config's entry first, then the checkout's fugaro.yaml (repoSlug has already
// refused the two disagreeing by the time a launch gets here).
//
// A nil co is not a panic but an empty answer: a caller with no checkout has
// localRepoProvider and nothing else to read (F3), and a form that reached
// here with one would be asking a document that does not exist.
func (e *cloudEnv) repoProvider(repo string, co *config.Config) string {
	if p := e.localRepoProvider(repo); p != "" {
		return p
	}
	if co == nil {
		return ""
	}
	return co.Git.Provider
}

// repoConfigAtTheRef is the document of a launch that has no checkout of the
// repository anywhere, read at the forge as the operator themselves (source 3,
// spec.md §2, §5; D901, D902). It answers with the repository's own
// fugaro.yaml, resolved over the project layer every other source's document is
// resolved over, or nil for the one state that selects nothing: a ref whose
// tree carries no document at all, which keeps the per-repository job (spec.md
// §7). Every other outcome is a refusal — this function never guesses.
//
// It is reached only where the code was about to refuse (the trigger is narrow
// on purpose, D901): a checkout of the repository is not here, a shared job
// serves this provider, and the project has published a profile.
// launchConfigWhereTheTreeGivesNone is its one caller, and both the launch's
// gates and its job decision go through that, so the read is made once for the
// launch and never for a launch that would have been answered without it. What
// it does with that is the design's own order — the refusal for an installation
// with no credentials service, the refusal a coding agent's session gets before
// anything is read, then one read through the service.
func (e *cloudEnv) repoConfigAtTheRef(ctx context.Context, spec *task.Spec) (*config.Config, error) {
	// Both refusals that come before a read — the installation's own records and
	// the guard that keeps this read out of a coding agent's session — are the
	// read's own, in serviceReadAtTheForge: this function decides a job and does
	// not make the read, and the credential gate reads the same document
	// (projectJobAgentAuth), so a guard here would be one the other reader could
	// go without.
	data, err := readRepoConfigAtRef(ctx, e, spec.Repo, spec.Ref)
	switch {
	case errors.Is(err, github.ErrFileNotFound):
		// The ref resolved and its tree carries no fugaro.yaml: a repository with
		// no document selects no clone workflow and no profile, and the project
		// layer is never resolved on its own, so the per-repository job is not a
		// fallback but the answer (spec.md §7).
		return nil, nil
	case err != nil:
		return nil, serviceReadRefusal(spec.Repo, spec.Ref, err)
	}
	cfg, err := resolveRepoConfig(ctx, data)
	if err != nil {
		// The read answered and the document does not resolve — a file that
		// names a profile of no layer, a layer that no longer parses. It is
		// D905's "a read that fails is a refusal": nothing was launched, and
		// the fix is what `fugaro validate` reports against the file.
		return nil, userErr("cannot read %s's fugaro.yaml at %s: the file the credentials service answered with does not resolve (%s), so the job this launch runs on cannot be decided from it. A document that does not resolve is what fugaro validate reports. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called", spec.Repo, oneLineCLI(spec.Ref), oneLineCLI(err.Error()))
	}
	return cfg, nil
}

// launchReadCommand and launchServiceReadWhat name, for the shared
// agent-session refusal, the command and the value this launch reads: there is
// no flag that would allow it (refuseKeyReadInAgent's flag slot carries the
// command where a read happens as part of one, as `fugaro image build` does),
// and the value is named with the way out an agent has — the repository's own
// checkout, where nothing is read at all.
const (
	launchReadCommand     = "fugaro run"
	launchServiceReadWhat = "the repository's fugaro.yaml at the ref the run checks out, as you, through the installation's credentials service (run it from the repository's checkout and the file is read out of the checkout's own objects, with no credential at all)"
)

// serviceReadRefusal refuses a launch whose read of the ref's document at the
// forge failed, in the words of what failed (spec.md §7). Nothing falls back to
// the per-repository job here: a read that failed means the job cannot be
// decided, and a run placed on a job the project's policy did not choose is
// worse than a refusal (D905). Every message names the repository, the ref and
// the way out, and none of them carries a credential: the token the service
// mints, the identity presented for it and GitHub's own refusal text are never
// put in one.
func serviceReadRefusal(repo, ref string, cause error) error {
	// A refusal this package already wrote for the operator is that refusal, not
	// a cause to be worded: the guard that keeps the read out of a coding
	// agent's session writes one (refuseKeyReadInAgent), and it arrives here as
	// a read's failure because it is the read's own first step
	// (serviceReadAtTheForge). Re-wording it would report an agent's session as
	// a remote failure and drop the way out it names.
	var refusal *ExitError
	if errors.As(cause, &refusal) {
		return cause
	}
	var se *httpjson.StatusError
	switch {
	case errors.Is(cause, errNoCredentialsService):
		// spec.md §7's first row, and the cost of D902's answer: the service
		// ships dormant, and an installation that has not opted in leaves the
		// launcher nothing to read a repository's configuration with.
		return userErr("cannot read %s's fugaro.yaml at %s as yourself: this installation has no credentials service, so the read that decides the run's job cannot be made. Run it from %s's checkout", repo, oneLineCLI(ref), repo)
	case errors.Is(cause, errNoGitHubAppID):
		// The read mint is signed as the repository's App, and the App ID is the
		// project's own recorded value (localcfg.Repo.GitHubAppID): a launch that
		// cannot name it cannot ask for a read at all.
		return userErr("cannot read %s's fugaro.yaml at %s as yourself: this installation's local config records no github_app_id for %s, and the read the credentials service mints is signed as that App, so the read that decides the run's job cannot be made. Run it from %s's checkout", repo, oneLineCLI(ref), repo, repo)
	case errors.Is(cause, github.ErrRefNotFound):
		// The ref does not exist at the forge. A deleted, force-pushed or
		// mistyped branch is not a reason to read another one (spec.md §3):
		// the document is the one at the ref the run checks out, and no other
		// ref's file places this run.
		return userErr("cannot read %s's fugaro.yaml at %s: the repository has no such ref, and a deleted or never-pushed branch is not a reason to read another one — a launch is not placed by a document nobody read. Check the ref, or run it from %s's checkout. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called", repo, oneLineCLI(ref), repo)
	case errors.As(cause, &se) && se.Status == http.StatusForbidden:
		// The mint is the service's and the set it asks with is the service's
		// (A0's PersonReadTokenPermissions), so a forge that refuses this read
		// is an installation whose service still mints the set before A0 — or
		// an App not granted contents. The fix is the redeploy, and the
		// message names the service and it, never the token and never GitHub's
		// own text (spec.md §7).
		return userErr("cannot read %s's fugaro.yaml at %s: the installation's credentials service minted a token that cannot read a file; redeploy it with fugaro update", repo, oneLineCLI(ref))
	case errors.Is(cause, github.ErrServiceReadIdentity):
		// This machine could not present what a read mint is verified by: no
		// gcloud, no Google account, an identity token that is not one header
		// value. The refusal says so and names the fix; it is a local
		// condition, not a remote one.
		return userErr("cannot read %s's fugaro.yaml at %s as yourself: %s. Run it from %s's checkout", repo, oneLineCLI(ref), oneLineCLI(cause.Error()), repo)
	}
	// Everything else is the service or the forge failing to answer — an
	// unreachable service, a 5xx, a repository the installation does not hold
	// — and it is reported as the remote failure it is, with the same way out.
	// A forge that answered with a status is reported by that status alone,
	// whatever it was and not only the 403 above: httpjson keeps up to 512
	// bytes of the answer's body (httpjson.maxErrorBody), and GitHub's own
	// text is not Fugaro's to print (spec.md §7).
	why := oneLineCLI(cause.Error())
	if errors.As(cause, &se) {
		why = fmt.Sprintf("HTTP %d", se.Status)
	}
	return remote(fmt.Errorf("cannot read %s's fugaro.yaml at %s as yourself: the read that decides the run's job could not be made (%s). Run it from %s's checkout", repo, oneLineCLI(ref), why, repo))
}

// The two states a read cannot be attempted from at all, kept as sentinels so
// that the refusal's words are one place (serviceReadRefusal) rather than the
// reader's: the seam answers what it found, and the message names the repository
// and the ref.
var (
	// errNoCredentialsService is an installation that has not opted into the
	// credentials service, or one whose local config carries no block: the
	// launcher holds no provider credential of its own (D902).
	errNoCredentialsService = errors.New("this installation has no credentials service")
	// errNoGitHubAppID is a repository the project's local config records no
	// App for, which is the App the read mint is signed as.
	errNoGitHubAppID = errors.New("this repository's github_app_id is not recorded")
)

// serviceReadCacheKey is the context key withServiceReadCache installs.
type serviceReadCacheKey struct{}

// serviceReadCache is the one read a command's launches share: the document a
// launch with no checkout read at the forge, per (repo, ref). It exists because
// two readers of that one document ask for it — the job decision and the
// credential gate (projectJobAgentAuth) — and a read mint is an act the
// credentials service AUDITS: one launch, one mint, one read, and one for the
// whole of a best-of-N batch, which is what spec.md §6's "at most one read per
// command" is.
//
// Nothing outlives the command (D904): the value is a context's, nothing writes
// it anywhere, and the next command starts with none.
type serviceReadCache struct {
	mu    sync.Mutex
	byKey map[string]serviceReadResult
}

// serviceReadResult is one read's answer, kept so that the second reader of it
// pays no second mint: the ref's own bytes, or the failure the first reader got.
type serviceReadResult struct {
	data []byte
	err  error
}

// withServiceReadCache installs an empty cache for this launch's reads. It is
// installed by each launch shape over the whole of it — runAttempts for a
// best-of-N batch, whose attempts share one command's context, and finishLaunch
// for a single `fugaro run` and for the daemon's own launches
// (orchestrator_launcher's Launch) — because a launch with no checkout of the
// repository has two readers of that one document, its gates
// (freshLaunchGates) and its job decision, and the first of them runs before
// the second. launchProjectJobFor installs one too, for a caller that reaches
// the decision directly. A launch its own checkout answered, and every command
// that reads no document at all, carries nothing but the context value.
func withServiceReadCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(serviceReadCacheKey{}).(*serviceReadCache); ok {
		return ctx
	}
	return context.WithValue(ctx, serviceReadCacheKey{}, &serviceReadCache{byKey: map[string]serviceReadResult{}})
}

// readRepoConfigAtRef is serviceReadRepoConfigAtRef; a test seam, like
// readFugaroYAMLAtRef, so a read's own failure can be controlled without a
// service.
var readRepoConfigAtRef = serviceReadRepoConfigAtRef

// serviceReadRepoConfigAtRef reads repo's fugaro.yaml at ref through the
// installation's credentials service, as the operator themselves: the person
// class's read mint (github.ServiceReadTokens, A0) for one repository, and A1's
// reader (github.FileAt) over it. The launcher holds no App key and reads no
// environment variable (D902): the service's address is the installation's own
// published block, and the App ID is the project's own recorded value.
//
// The two calls it makes are the service's mint and the forge's read, and
// nothing here decides a job: the bytes come back and the caller reads them.
// The memo is keyed by (repo, ref) in the command's own scope, so the two
// readers of one launch's document pay for one mint between them.
func serviceReadRepoConfigAtRef(ctx context.Context, e *cloudEnv, repo, ref string) ([]byte, error) {
	cache, _ := ctx.Value(serviceReadCacheKey{}).(*serviceReadCache)
	if cache != nil {
		cache.mu.Lock()
		res, ok := cache.byKey[repo+"\x00"+ref]
		cache.mu.Unlock()
		if ok {
			return res.data, res.err
		}
	}
	data, err := serviceReadAtTheForge(ctx, e, repo, ref)
	if cache != nil {
		cache.mu.Lock()
		cache.byKey[repo+"\x00"+ref] = serviceReadResult{data: data, err: err}
		cache.mu.Unlock()
	}
	return data, err
}

// serviceReadTarget is the two things a read needs from the installation's own
// records, and the only place either is looked up: the credentials service's
// published address (localcfg.CredentialsService.URL) and the App ID the mint
// is signed as (localcfg.Repo.GitHubAppID, which is not a secret). A missing one
// is a sentinel rather than a message, so that the refusal's words stay with the
// launch that cannot be decided (serviceReadRefusal).
//
// The repository's entry is matched in canonical form (localRepo, which is
// localcfg.RepoFor), as every other reader of this launch's local config matches
// it — a repos: entry is spelled the way the installation recorded the remote,
// and "acme/app.git" is acme/app's entry (repoConfigKey writes through the key
// that is already there). A raw index would answer this launch with a refusal
// about a value the config does hold.
func (e *cloudEnv) serviceReadTarget(repo string) (url, appID string, err error) {
	if e.lc.CredentialsService != nil {
		url = e.lc.CredentialsService.URL
	}
	if r, ok := e.localRepo(repo); ok {
		appID = r.GitHubAppID
	}
	if url == "" {
		return "", "", errNoCredentialsService
	}
	if appID == "" {
		return "", "", errNoGitHubAppID
	}
	return url, appID, nil
}

// serviceReadAtTheForge is the read itself, once — and the one place a read is
// refused before it is made. Two things come first, in this order: the
// installation's own records, which are what a read needs and the only place
// either is looked up (serviceReadTarget), and the guard that keeps a read made
// with the operator's own credential out of a coding agent's process
// (refuseKeyReadInAgent, D902, spec.md §5).
//
// The order is the catalog's (catalogProviderEnv): a value is refused where it
// would actually be read, and an installation with no credentials service has
// no read to refuse — its own line is the honest one, and the checkout it names
// is a way out that works for an agent too. The guard is refused here, in the
// read, rather than in a caller: a launch's document has two readers — the job
// decision (repoConfigAtTheRef) and the credential gate (projectJobAgentAuth) —
// and the caller that had to remember the guard is the caller that would mint
// the operator's token inside an agent's process (the state where a checkout
// answered the decision and cannot be found a moment later). The way out it
// names is the checkout: a launch from the repository's own checkout reads the
// file out of its objects and asks the service for nothing.
func serviceReadAtTheForge(ctx context.Context, e *cloudEnv, repo, ref string) ([]byte, error) {
	url, appID, err := e.serviceReadTarget(repo)
	if err != nil {
		return nil, err
	}
	if err := refuseKeyReadInAgent(os.Getenv, launchReadCommand, launchServiceReadWhat); err != nil {
		return nil, err
	}
	// The identity is minted for the service's own URL, which is the audience
	// its person class admits (a user account's plain token is the other), and
	// it is minted per call and cached nowhere: a Google ID token lives an
	// hour, and this read is one call.
	ts := (&github.ServiceReadTokens{
		URL:     url,
		IDToken: func(ctx context.Context) (string, error) { return personIDToken(ctx, url) },
		HTTP:    githubAPIClient,
	}).Repo(repo, appID)
	// The API root and the client are the package's own seams (githubAPIURL,
	// githubAPIClient): nothing here reads a host or a proxy out of the
	// environment, and a test points both at its own server.
	return github.FileAt(ctx, github.Options{AppID: appID, BaseURL: githubAPIURL, HTTP: githubAPIClient}, ts, repo, ref, "fugaro.yaml")
}

// cannotReadConfigAtRef refuses a launch whose checkout is here but whose
// document at the ref could not be read out of it (D912).
//
// It is the other no-checkout refusal, and its way out is not the checkout:
// the operator is standing in it (F2 — the merged message sent them to the
// directory they are already in). What failed is the read of the ref's
// document: a ref this checkout does not hold, or a fugaro.yaml that does not
// resolve over the project layer. Both are D905's "a read that fails is a
// refusal": the job cannot be decided, and a run placed on a job the project's
// policy did not choose is worse than a refusal. Nothing of the launch
// happened, and the message says so, as every other gate of this decision does.
func cannotReadConfigAtRef(repo, ref string, cause error) error {
	return userErr("fugaro run --repo %s cannot tell which Cloud Run job it targets: %s's fugaro.yaml at %s could not be read out of this checkout (%s). A workflow's `checkout:` kind and its `job_profile` are read from that file at the ref the run checks out, which is the document the run itself resolves after cloning it — so a launch cannot decide the job from another ref, or from the working tree. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. A ref this checkout does not hold can be fetched or checked out; a document that does not resolve is what fugaro validate reports", repo, repo, oneLineCLI(ref), oneLineCLI(cause.Error()))
}

// launchDocumentKey is the context key withLaunchDocument installs.
type launchDocumentKey struct{}

// launchDocument is what a launch resolved about the repository's own
// fugaro.yaml where the working tree could not give one, installed for the
// whole launch so that every reader of it answers from one document
// (withLaunchDocument, checkoutParse).
//
// One document and not two is the whole point of it. The gates of a launch
// read the workflow's kind, its checkout mode and its recipe from this
// document, and the job it runs on is decided from it too: a launch whose
// gates judge a working tree with no fugaro.yaml — read as "no clone workflow"
// — and whose job decision judges the ref's own file starts runs no gate
// looked at, on the shared clone job, with cloneSince's base image, the seal,
// the recipe, the levels and the governance gates all skipped.
type launchDocument struct {
	// root is the checkout this answer was read out of: it is given to a
	// reader asking about that same checkout and to no other.
	root string
	// cfg is the fugaro.yaml at the ref the run checks out. It is nil when
	// that ref's tree carries none — a repository with no document anywhere,
	// which selects no clone workflow and no profile and keeps the
	// per-repository job (spec.md §2, §7). A launch with no checkout of the
	// repository at all is one no document is resolved for at all: root is set
	// only where a checkout answered (withLaunchDocument), and that launch's
	// job decision reads the ref at the forge instead (repoConfigAtTheRef).
	cfg *config.Config
	// problems are the working tree's own: a document at the ref answers for
	// none of them, and the one refusal that reads them (refuseSharedCheckout)
	// still has to fire on the file the operator wrote.
	problems []config.Problem
	// err is a read that failed — a ref this checkout does not hold, a
	// fugaro.yaml its objects cannot produce, a document that does not resolve
	// over the project layer. It is D905's refusal, and it is kept rather than
	// raised here because the launch that needs the answer is the one that
	// asks for it: an installation that published no profile decides nothing
	// from a document, and is not refused for one it could not read.
	err error
}

// withLaunchDocument resolves the repository's own document for this launch
// where the working tree cannot give one, once, and installs it as the answer
// every reader of that launch gets (checkoutParse).
//
// Read where the tree has none, and only there: the launcher holds the
// operator's checkout, and a tree carrying a fugaro.yaml is what this launch
// judges, as it always has. Where the tree cannot give one — no file, or one
// that does not resolve — the answer is the document at the ref the run checks
// out, out of the checkout's own object database, exactly as the run resolves
// that file after cloning the ref (refConfigAtRef, D909). The alternative is
// not "no answer": it is every gate that reads this document judging a
// repository the run will not see.
//
// A launch with no checkout of the repository is left alone — the daemon's own
// launches, or a run --repo from a machine that does not hold it: there is
// nothing here to read, and that launch's document is the one read at the forge
// instead (launchConfigWhereTheTreeGivesNone, source 3, D902).
func withLaunchDocument(ctx context.Context, repo, ref string) context.Context {
	if _, ok := launchDocumentOf(ctx); ok {
		return ctx
	}
	cfg, problems := checkoutParse(ctx, repo)
	if cfg != nil {
		return ctx
	}
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return ctx
	}
	d := refDocumentOf(ctx, root, ref)
	d.problems = problems
	return context.WithValue(ctx, launchDocumentKey{}, d)
}

// launchDocumentOf is this launch's own answer about the repository's
// document, where it resolved one.
func launchDocumentOf(ctx context.Context) (launchDocument, bool) {
	d, ok := ctx.Value(launchDocumentKey{}).(launchDocument)
	return d, ok
}

// refDocumentOf is the document at the ref the run checks out, read out of the
// checkout at root (refConfigAtRef) as a launch document: no cfg and no error
// for a ref whose tree carries no fugaro.yaml, err for a read that failed.
func refDocumentOf(ctx context.Context, root, ref string) launchDocument {
	d := launchDocument{root: root}
	d.cfg, d.err = refConfigAtRef(ctx, root, ref)
	return d
}

// refConfigAtRef is the document a launch reads where the working tree could
// not give it: spec's fugaro.yaml at the ref the run checks out, out of the
// checkout's own object database (checkoutFugaroYAMLAtRef — a local git
// cat-file, never the network and never the working tree), resolved over the
// project layer the working tree's own resolution uses (checkoutParse:
// selectedProjectConfig and a lenient resolution, so both documents are
// composed the same way).
//
// It returns (nil, nil) for one definite answer — the ref resolved and its
// tree carries no fugaro.yaml, which checkoutFugaroYAMLAtRef establishes from
// the tree rather than from a read that failed — because that is a repository
// with no document: it selects no clone workflow and no profile, so its launch
// keeps the per-repository job (spec.md §2, §7). Every other failure is an
// error for the caller to refuse with, cause and all: a ref the checkout does
// not hold, a fugaro.yaml the tree names and this checkout cannot produce, a
// project layer that cannot be read, and a document that does not resolve are
// reads that failed, and D905 refuses rather than falling back.
func refConfigAtRef(ctx context.Context, root, ref string) (*config.Config, error) {
	data, err := checkoutFugaroYAMLAtRef(ctx, root, ref)
	if err != nil {
		if errors.Is(err, errNoConfigAtRef) {
			return nil, nil
		}
		return nil, err
	}
	return resolveRepoConfig(ctx, data)
}

// resolveRepoConfig is a repository's own fugaro.yaml — its bytes, wherever
// they were read from — resolved over the project layer the working tree's own
// resolution uses (checkoutParse). Both of the launch's sources of that document
// go through it: the checkout's objects (refConfigAtRef) and the read at the
// forge (serviceReadRepoConfigAtRef), because a document that means one thing
// read out of a checkout and another read at the ref would place two runs of the
// same ref on two jobs.
//
// It answers with an error rather than nil for a document that does not resolve:
// every caller of it has established that the file exists, so "no config" here
// would be a repository with no document, which is the caller's own answer to
// give (spec.md §7).
func resolveRepoConfig(ctx context.Context, data []byte) (*config.Config, error) {
	rf, err := resolveFugaroYAML(ctx, data, selectedProjectConfig(ctx), layerOptions{Lenient: true})
	if err != nil {
		return nil, err
	}
	if rf.Cfg == nil {
		return nil, errors.New(layerProblemsText(rf.Problems))
	}
	return rf.Cfg, nil
}

// projectJobAccountFor is the account axis of the job a launch on profile pr
// targets, and the launch's gate on the Claude credential that job holds (plan
// task 3, design §4.4 rows 4 to 6).
//
// The account is the run's own credential's, and only two kinds of run have
// one: a named Claude account, selected by a workflow whose agent.auth is oauth
// and by nothing else (claudeAccountAllowed, run.go, before any launch), and
// the axis's values that are not Claude accounts at all — the project's own
// Anthropic API key and the project's provider key
// (config.AnthropicKeyAccount, config.ProviderKeyAccount) — which are the
// account of their own job for the api-key workflow D1417 pairs them with. Every other run — Vertex,
// cascade, fake — has no account to place and targets the profile's no-account
// job, which is the job every profile keeps whatever it serves (D9). The
// account axis is therefore spelled the way the job's own name spells it: a
// named account is that account, and the default account and no selection at
// all are both the empty account, which is the profile's no-account job
// (projectJobAccount).
//
// Three rules then decide whether that job may serve this run:
//
//   - an OAuth run may only target a job of the account it selects, so a
//     selection the profile serves no job for is refused (checkProjectJobAccount).
//     The set it is judged against is config.ProjectJobAccounts, the one
//     function the deployer built the deployed jobs with and the runner
//     recomputes the names it matches against with: a launch can therefore
//     only ever target a job those two know, which is what keeps a run from
//     being started on a name nothing deployed (a 404 that would blame the
//     converge for a job it was never asked for) or on a job the runner would
//     refuse to place.
//   - the job's container holds one account's token — or none — and that
//     account's own policy list must cover this repository
//     (checkProjectJobAccountCovers). This is asked of the *job's* account and
//     not of the run's selection, because on the no-account job it is the
//     profile's `default` entry that decides which token the run holds (D5),
//     whatever the run's own credential is.
//   - the axis's non-account values are that job's account for an api-key run
//     and for no other run, whatever the task's oauth_token says
//     (claudeAccountAllowed admits them for api-key, and for "" — a pre-check
//     with no document to read an auth from — and leaves the judgement to
//     here). A run whose own workflow resolves any other auth is refused
//     rather than placed on the profile's no-account job: it would start on a
//     job its own credential does not name, which the runner halts on.
func projectJobAccountFor(p *projectpolicy.Policy, profile string, pr projectpolicy.Profile, auth, selected, provider, repo string) (string, error) {
	// The account is the run's own credential's: a named account is only ever
	// selected by a workflow whose agent.auth is oauth (claudeAccountAllowed,
	// run.go, before any launch), the axis's non-account values only by one
	// whose agent.auth is api-key (the arm below), and the default account and no
	// selection at all are both the empty account — the profile's no-account
	// job (projectJobAccount). Every other kind of run has no Claude account to
	// place and targets that same no-account job, whatever its task.json or a
	// stale selection says.
	account := ""
	switch {
	case auth == "api-key" && config.AxisNonAccountValue(selected):
		// The axis's values that are not Claude accounts (D1417, D1422, AK6a):
		// they name the project's own Anthropic API key and the project's
		// provider key, and a profile that lists one deploys a job whose account
		// segment is the value itself (internal/infra's ProjectJobs, mounting no
		// token on it). A launch that names it targets that job, and it is what
		// the review class needs: the review-only variant of THAT job is the
		// pairing the credential is served from, so a launch resolving the
		// profile's no-account job instead would run in a variant the run
		// refuses as a pairing that names no credential. It is no second way
		// onto an account's job — such a job holds no token for a run to pick
		// up, and api-key is the one auth these values survive
		// claudeAccountAllowed under, which is the auth D1417 pairs them with.
		account = selected
	case config.AxisNonAccountValue(selected):
		// The same values under every other auth, and the arm that makes
		// claudeAccountAllowed's promise true: it admits them for api-key, and
		// for "" — the pre-check callers that resolve with no document to read
		// an auth from (a stored task repeated or retried) — and it is this
		// function that re-judges them at the launch that can read one. Without
		// this arm a value falls to the no-account job below and the run starts
		// on it carrying an account its own configuration does not use: a vertex
		// or cascade run has no Claude account to place, and these values are no
		// account under any auth but the one D1417 pairs them with, so the
		// launch would take the claim, mint the budget token and start Cloud Run
		// on a job the run's own credential does not name — and, on a profile
		// whose no-account job mounts the default account's token, hold a token
		// the run never asked for.
		//
		// The refusal is not checkProjectJobAccount's: that one's move — add
		// the value to profiles.<p>.claude_accounts and publish again — is the
		// move for a job the profile does not serve, and it cannot answer this
		// one, where the value is refused for the auth the run's own workflow
		// names however the profile's list is written (and is false besides for
		// a profile that already lists the sentinel: its job is deployed).
		authText := "could not be read"
		if auth != "" {
			authText = fmt.Sprintf("is %q", auth)
		}
		return "", userErr("this run's own credential is %s, %s, and that credential is a job's account only for a run whose own workflow reads it (agent.auth: api-key, D1417): the account axis is spelled from the run's own credential, and this run's agent.auth %s, so the value is no account this run can be placed by — the job it names is not the job this run's own credential names, and the run would start on a job its own configuration does not read. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Launch the run without the value, which is where the account its own workflow names is resolved, or, if this repository is meant to run on the project's own credential, set agent.auth: api-key in its fugaro.yaml — the auth whose job and review variant the value names",
			selected, nonAccountPhrase(selected), authText)
	case auth == "oauth":
		account = projectJobAccount(selected)
	}
	// The job that account names, and the token it holds. Both are the
	// profile's own claude_accounts list, read by the two functions the
	// deployer and the runner read it with: config.ProjectJobAccounts says a
	// job with this account segment exists at all (the no-account job every
	// profile has, plus one per account it lists), and
	// config.ProjectJobTokenAccount says that job's container holds this
	// account's token. An OAuth run needs both — a job nothing deployed is a
	// 404, and a job holding another account's token, or none, is a run whose
	// own credential is not the credential it holds — while every other run
	// targets the no-account job, which holds a token only when the profile
	// lists the default account.
	servesJob := slices.Contains(config.ProjectJobAccounts(pr.JobProfile), account)
	tokenAccount, servesToken := config.ProjectJobTokenAccount(pr.JobProfile, account)
	if !servesJob || (auth == "oauth" && !servesToken) {
		return "", checkProjectJobAccount(profile, pr, selected)
	}
	// And the token the job holds must be one whose own policy list covers
	// this repository — asked of the job's token account rather than of the
	// run's selection, because on the no-account job the two differ whenever
	// the profile lists `default` (D5).
	if servesToken {
		if err := checkProjectJobAccountCovers(p, profile, provider, account, tokenAccount, repo); err != nil {
			return "", err
		}
	}
	return account, nil
}

// projectJobReviewFor is the launcher's review entry (design
// docs/cloud-senior-review, CSR7): the review-only variant of the shared
// project job a launch on (profile, provider, account) resolved — the job a
// senior review of that profile's runs executes on, deployed beside the coding
// job with the variant's own service account (internal/infra's
// ProjectReviewJobs).
//
// The name is credsvc.ReviewJobName's, over the account segment the policy
// speaks: config.ProjectJobAccounts' axis writes the default account as the
// empty string and the variant's names carry config.DefaultClaudeAccount there
// (infra.ReviewVariantAccount, the same mapping the deployer makes). That is
// the one spelling the deployer deploys the job with and the credentials
// service admits its account by (credsvc.ReviewJobAccountsFor), so a launcher
// that spelled either its own way would resolve a job no installation has and
// Cloud Run would answer 404 for a review that was never placed.
//
// What a launch on it hands the execution is nothing. The variant's own
// environment carries the credentials service's address from its deployment
// (internal/infra, D893 (c)), so this entry adds no variable and the job is
// left with the environment it was deployed with — no App key, no Claude
// token, no signing key and, unlike a repository's own job, no per-launch
// entry at all (credentialsServiceLaunchEnv's rule for a job named at launch).
// The review run fetches the read-only installation token by name from the
// service when it needs one, and refreshes it through Token(ctx, minValid);
// nothing of the installation's is handed in.
//
// account is the axis segment of the coding job the review is about — the
// account whose admissions the service pairs the review caller by — and
// timeout the profile's own, which the variant carries as its task timeout.
func projectJobReviewFor(profile, provider, account string, timeout time.Duration) launchProjectJob {
	return launchProjectJob{
		Name:    credsvc.ReviewJobName(profile, provider, infra.ReviewVariantAccount(account)),
		Profile: profile,
		Timeout: timeout,
	}
}

// checkProjectJobAccount refuses a launch whose run selects a Claude account
// the selected profile serves no job for (plan task 3, design §4.4 row 5).
//
// On a per-repository job the account is part of the job's own name
// (gcp.JobName with gcp.WorkflowVariant(workflow, token)), so a run that
// selects an account is launched on the job that mounts that account's token.
// A project job's account is fixed where the job is deployed: it is the
// account segment of gcp.ProjectJobName, one job per (profile, provider,
// account) (internal/infra's ProjectJobs), and that job's container holds that
// account's token and no other, whatever the run's own task.json says. So a
// run that selects an account the profile serves no job for would either run —
// and be recorded, and be shown by `runs ls` — as an account its own
// fugaro.yaml did not choose, or land on a job that holds no token at all; and
// the credential the job holds is not the one this launch's allow_data_to
// check was made for (that check validates the selected name,
// agent.oauth_token or --token). The launch stops instead, as the rest of this
// release's selections do: a repository that must run as another account
// belongs on a profile that serves it.
//
// The three cases the caller reaches this with are the run's own credential
// being the default account on a profile that does not list it — including a
// profile that lists nothing, which is every profile of an installation that has
// not adopted accounts — a named account the profile does not list, and the
// account axis's non-account values, the project's own Anthropic API key and the
// project's provider key (config.AnthropicKeyAccount, config.ProviderKeyAccount),
// which no claude_accounts entry may define and which have a sentence of their
// own below. The first two are refusals of the same
// rule, and both name the token and the account: an OAuth run on a profile that
// serves no Claude account would start with no credential at all, and the runner
// refuses exactly that, so the launch stops here, before the claim and before
// the token, rather than at a run that halts.
//
// The default account is spelled the way the model spells it everywhere: the
// default account (config.DefaultClaudeAccount) and a profile that serves no
// Claude account are both the empty account on a job's axis
// (config.ProjectJobAccounts, config.ProjectJobTokenAccount).
func checkProjectJobAccount(profile string, pr projectpolicy.Profile, selected string) error {
	// The axis's values that are no account at all, and the sentence for them
	// cannot be the one below: that one names the account's token and the
	// claude_accounts entry whose allow_data_to list bounds it, while these
	// values have no token and config.ValidateClaudeAccounts refuses to define
	// them in claude_accounts — so the move it names would be a key the operator
	// may not write. What this says instead is what each value is and where its
	// job comes from (D1417, D1422, AK6a): the profile's own claude_accounts
	// list is what deploys it, and where its published list is derived from is
	// the value's own reach — the project's own key's project_secrets entry, or
	// the service's own hold on the provider key, which no entry narrows.
	if config.AxisNonAccountValue(selected) {
		reach := "the profile's own allow_repos are then the repositories the key serves, and project_secrets." + config.AnthropicSecret + ".allow_data_to must reach them"
		if selected == config.ProviderKeyAccount {
			reach = "the profile's own allow_repos are then the repositories the provider key serves — the credential is the credentials service's, held once for the project, so no project_secrets entry narrows the list"
		}
		return userErr("this run's own credential is %s, %s, which is not a Claude account and is no job of the profile %q: a project job's Claude credential is fixed where the job is deployed — one job per (profile, provider, account), the account segment of the job's own name — and the value's job is the one a profile deploys by listing it, whose review-only variant is what a review of such a repository runs on. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Add %s to profiles.%s.claude_accounts in the project's local config — %s — and run fugaro update to deploy the job and its review variant",
			selected, nonAccountPhrase(selected), profile, selected, profile, reach)
	}
	secret := config.ClaudeAccountSecret(projectJobAccount(selected))
	chose := fmt.Sprintf("the default Claude OAuth account, whose token is %s", secret)
	if selected != "" {
		chose = fmt.Sprintf("the Claude OAuth account %q, whose token is %s", selected, secret)
	}
	serves := fmt.Sprintf("lists no Claude account at all (profiles.%s.claude_accounts in the project's local config is not written)", profile)
	if n := len(pr.ClaudeAccounts); n > 0 {
		serves = fmt.Sprintf("lists the Claude accounts %s, and not that one (profiles.%s.claude_accounts in the project's local config)", strings.Join(pr.ClaudeAccounts, ", "), profile)
	}
	return userErr("this run's own credential is %s, and the job profile %q %s: a project job's Claude account is fixed where the job is deployed — one job per (profile, provider, account), the account segment of the job's own name, and its container holds that account's token (mounted as CLAUDE_CODE_OAUTH_TOKEN) and no other — so an OAuth run can only be launched on a profile that serves the account its own credential selects, and on that account's job. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Add the account to profiles.%s.claude_accounts in the project's local config, store its token (%s) and publish the policy again, or run this repository on a profile that serves the account it selects",
		chose, profile, serves, profile, secret)
}

// nonAccountPhrase is how a refusal names one of the account axis's non-account
// values (config.AxisNonAccountValue): the project's own Anthropic API key, or
// the project's provider key. The sentences that refuse them are one rule with
// two subjects, so the subject is a phrase beside them rather than two copies of
// each refusal — a second wording for one rule is a second rule, and the values
// differ in exactly this: which credential is meant.
func nonAccountPhrase(account string) string {
	if account == config.ProviderKeyAccount {
		return "the project's provider key"
	}
	return "the project's own Anthropic API key"
}

// projectJobAccount is a Claude account name as a job's account axis spells it:
// the default account and no account at all are both "". They are one selection
// everywhere the axis is computed — gcp.WorkflowVariant joins no segment for
// either, and config.ProjectJobAccounts spells the default account as the empty
// one because it rides the profile's no-account job — so a comparison of two
// accounts is a comparison of what each names beyond the default.
func projectJobAccount(name string) string {
	if name == config.DefaultClaudeAccount {
		return ""
	}
	return name
}

// checkProjectJobAccountCovers refuses a launch whose repository is not on the
// list of the Claude account whose token the job it targets holds (design §4.4
// rows 3 and 4, plan task 3).
//
// A project job's container holds one account's token — the platform mounts it
// from the job's own definition (internal/infra's secret_env carrier), so no run
// can be without it and no run can decline it — and every run of that job
// therefore holds it: the account is the blast radius of a hostile run
// (design §6.1). The list of repositories whose runs may use that token is the
// published policy's accounts.<name>.allow_repos, derived at publish from the
// account's own allow_data_to in the project's local config (DeriveAccounts),
// and it is the rule the runner re-checks at run start: this is the same
// comparison, on the same list, made where the job is chosen rather than only
// where the run starts.
//
// The question is the *job's* token account, not the run's own selection: the
// profile's no-account job holds the default account's token whenever the
// profile lists `default` (D5), so an api-key or Vertex run of such a profile
// holds it too and is held to its list — which the subset rule makes true by
// construction, both lists being exact and the account's being the wider one
// (projectpolicy.validProfileAccounts).
//
// A policy that describes no list for the account the profile serves is refused
// rather than passed through: the list is the whole of the gate, and an absent
// one is a gate that is not there. It is unreachable through a decoded policy —
// validProfileAccounts refuses a profile naming an account the block does not
// define — but the two readings differ by more than a message: this is defence
// in depth on the one rule that bounds the token, so it fails closed.
func checkProjectJobAccountCovers(p *projectpolicy.Policy, profile, provider, account, tokenAccount, repo string) error {
	canonical, err := launchCanonical(provider, repo)
	if err != nil {
		return err
	}
	job := gcp.ProjectJobName(profile, provider, account)
	secret := config.ClaudeAccountSecret(tokenAccount)
	source := "claude_accounts." + tokenAccount + ".allow_data_to"
	if tokenAccount == config.DefaultClaudeAccount {
		source = "project_secrets." + secret + ".allow_data_to"
	}
	held := fmt.Sprintf("this launch would run %s on the shared project job %s, which is the job of the Claude account %q: that job's container holds the account's token (%s, mounted as CLAUDE_CODE_OAUTH_TOKEN) for every run of it, so the token is held by runs this repository's fugaro.yaml never chose, and accounts.%s.allow_repos — the list of repositories the token may be used by — is what bounds them",
		canonical, job, tokenAccount, secret, tokenAccount)
	acct, ok := p.Accounts[tokenAccount]
	if !ok {
		return userErr("%s. The published policy has no accounts.%s entry, so there is no such list and nothing bounds which repositories may use the token this job holds. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. The project's owner publishes the block with `fugaro project policy publish`, from a local config whose profiles.%s.claude_accounts names the account",
			held, tokenAccount, profile)
	}
	if projectpolicy.AdmitRepo(acct.AllowRepos, canonical) {
		return nil
	}
	return userErr("%s. This repository is not on it: accounts.%s.allow_repos is %s, and %s is not one of them. The block is derived at publish from %s in the project's local config, so the fix is the project's owner's: add this repository there and publish the policy again, or run it on a profile whose account covers it. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called",
		held, tokenAccount, strings.Join(acct.AllowRepos, ", "), canonical, source)
}

// projectJobAgentAuth is the agent.auth a launch on a shared project job runs
// under: the fugaro.yaml at spec.Ref — the ref the run checks out, read out of
// the launcher's checkout's object database — resolved over spec.ProjectLayer,
// the project layer the task carries into task.json, through
// runner.ResolveConfig, the runner's own function, so the two documents are
// composed exactly as the run composes them.
//
// It is deliberately not the checkout's own resolution (co.Agent.Auth), which
// launchProjectJobFor has in hand and used to read here. Three things make that
// one a different document from the run's:
//
//   - It is the *launcher's* resolution, made leniently (decision L16: launches
//     are strict, the in-checkout readers are not), so a project layer the
//     launcher could not read leaves it with the file's own values and a
//     problem rather than the layer's defaults — the values a run does not use.
//   - It may stand on a cached copy of the layer, up to seven days old (L13),
//     while the run's task carries the layer that was resolved for real.
//   - It is resolved *before* createTask adopts a repeated --run-id's stored
//     project layer over the freshly resolved one (run.go's
//     adoptStoredProjectLayer and createTask: a layer republished between the
//     first launch and the repeat makes the two differ, deliberately, because
//     the stored task is what the job reads). A task stored while the layer
//     still said `agent.auth: oauth`, repeated after the layer stopped saying
//     it, carries an OAuth layer into a run while the launcher's own resolution
//     now says api-key — a run whose credential selects an account's job
//     launched onto the no-account job (projectJobAccountFor), the launch the
//     account gate exists to refuse, and one this check would let through if
//     it read co.
//
// Both documents are the run's. The layer is: the task carries the bytes the
// run applies. And so is the fugaro.yaml, read at the ref the run checks out —
// spec.Ref, out of this checkout's own object database
// (checkoutFugaroYAMLAtRef), never the working tree. The run resolves that
// file after cloning spec.Ref (runner.go's blobless checkout of origin/
// spec.Ref) and it resolves the layer over it, so a launcher that judged its
// own working tree instead would judge a document the run never reads: a
// checkout left on another branch, or carrying an uncommitted edit, can say
// api-key where the ref the run clones says oauth, and the launch this check
// exists to refuse would go through. Even the plain-ref fallback is a fact
// about the repository rather than about the working tree.
//
// The workflow, the checkout mode and the profile still come from the working
// tree's resolution (co), and a difference there fails closed in the run (the
// runner's job-profile and job-binding checks refuse it, or the run lacks the
// credential). The condition's own agent.oauth_token is not one of those: it
// is repository-scope (named-oauth-tokens T2), it is what selects the account
// axis of this launch's job, and it is therefore read from the same document
// as the auth — claudeAccountFor, at spec build, where the --token flag
// overrides it and the ref's answer stands over the working tree's — so that
// the account this function's caller resolves and the account the run's own
// task.json carries cannot be two different documents' answers. The value that
// reaches this point is spec.OAuthToken, the run's own, whatever the working
// tree says. The layer is not read for it either way: no project layer may
// name an account.
//
// Nothing is fetched to make the read: a fetch is a network write into a
// repository this command does not own. A checkout that does not hold the ref
// at all is therefore refused with the message naming it (an operator can
// fetch or check it out), never launched on a guess at another document.
//
// A resolution that cannot be made is refused rather than guessed at: an
// unknown credential is one the job may not be able to serve, and the run would
// halt inside Cloud Run, past the claim and the budget token, instead of here.
//
// The resolution itself is the runner's own — runner.ResolveConfig, the
// exported core of (*run).resolveConfig, called with the deployment's project
// exactly as the runner calls it (internal/cli/consumers_test.go states the
// convention: every consumer resolves the same bytes through that function,
// never through a copy of its logic). Two of its decisions are the ones this
// check has to share:
//
//   - it parses the carried layer against the *deployment's* project alone and
//     then computes Applied, the layer's project and gcp_project against the
//     file's own; a layer that belongs to another project, or to another GCP
//     project, is not an error there — it simply does not apply, and the file
//     resolves alone (out.Applied false, the case the runner logs as "project
//     layer not applied"). Reading the file's own pair as the anchor instead
//     would refuse such a launch here with "the project layer its task carries
//     is invalid" although the run would start perfectly well, and would refuse
//     it on a document the run is not affected by.
//   - an invalid layer is LayerErr, and the credential is then unknown rather
//     than the file's: the run itself fails bootstrap on it
//     (resolveConfig), so a launch that resolved past it would start a run that
//     cannot begin.
func (e *cloudEnv) projectJobAgentAuth(ctx context.Context, spec *task.Spec) (string, error) {
	root := checkoutRoot(ctx, spec.Repo)
	var data []byte
	if root == "" {
		// A launch with no checkout of the repository: the document this
		// resolves is the one the job decision read at the forge, and it is the
		// same read — readRepoConfigAtRef's memo answers from the launch's
		// cache, so the credential gate costs no second mint (D904, spec.md
		// §6).
		//
		// Where the read was not made for this launch, it is made here: the
		// decision read the working tree's checkout and this gate cannot find
		// that checkout a moment later (checkoutRoot asks git again, and a
		// directory whose git answers once and not twice is the state the old
		// refusal named). What comes back is then the read's own refusal, in
		// spec.md §7's words — an agent's session refused before anything is
		// minted, an installation with no credentials service naming the
		// checkout, a forge status reported by that status — because it is the
		// same read the decision makes (serviceReadRefusal). The one failure
		// this branch words for itself is the absent file: a ref whose tree
		// carries no document leaves no agent.auth to resolve, which is this
		// gate's own subject rather than a read that failed.
		var err error
		data, err = readRepoConfigAtRef(ctx, e, spec.Repo, spec.Ref)
		switch {
		case errors.Is(err, github.ErrFileNotFound):
			return "", checkProjectJobAuthUnknown(spec.Repo, "the fugaro.yaml at the ref the run checks out ("+oneLineCLI(spec.Ref)+") is not there at the forge")
		case err != nil:
			return "", serviceReadRefusal(spec.Repo, spec.Ref, err)
		}
	} else {
		var err error
		data, err = readFugaroYAMLAtRef(ctx, root, spec.Ref)
		if err != nil {
			return "", checkProjectJobAuthUnknown(spec.Repo, "the fugaro.yaml at the ref the run checks out ("+oneLineCLI(spec.Ref)+") could not be read out of this checkout ("+oneLineCLI(err.Error())+")")
		}
	}
	out := runner.ResolveConfig(data, spec.ProjectLayer, e.lc.Name, spec.BaseBranch)
	if out.LayerErr != nil {
		return "", checkProjectJobAuthUnknown(spec.Repo, oneLineCLI(out.LayerErr.Error()))
	}
	if len(out.Problems) > 0 {
		return "", checkProjectJobAuthUnknown(spec.Repo, "its fugaro.yaml and the project layer its task carries do not resolve ("+oneLineCLI(layerProblemsText(out.Problems))+")")
	}
	return out.Cfg.Agent.Auth, nil
}

// readFugaroYAMLAtRef is checkoutFugaroYAMLAtRef; a test seam, like
// readCheckoutFugaroYAML, so a read failure's exact text can be controlled
// without a repository.
var readFugaroYAMLAtRef = checkoutFugaroYAMLAtRef

// checkoutFugaroYAMLAtRef reads ref's fugaro.yaml out of the checkout's own
// object database — git cat-file, never the network and never the working
// tree — or returns an error when this checkout does not hold that ref.
//
// ref is tried as the remote-tracking ref first, refs/remotes/origin/<ref>,
// because that is the ref the run itself checks out: the runner clones the
// repository blobless and does CheckoutNewBranch over origin/<ref>
// (internal/runner/runner.go), so origin/<ref> as this checkout last fetched
// it is the ref whose fugaro.yaml the run resolves agent.auth from. The plain
// ref is the fallback for a checkout that has the branch but no
// remote-tracking ref of it (a freshly made repository, or a branch never
// fetched): git resolves it through refs/heads, refs/tags and a commit id in
// turn. Nothing is fetched here — a fetch is a network write into a repository
// this command does not own — so a ref this checkout does not hold is an
// error rather than a guess at another document.
//
// The one error of its own that a caller may read as an answer is
// errNoConfigAtRef, and it means exactly one state: the ref resolved and its
// tree has no fugaro.yaml. Every other error — including a fugaro.yaml the
// tree names but this checkout cannot produce — is a read that failed, and
// callers refuse on it (D905).
func checkoutFugaroYAMLAtRef(ctx context.Context, root, ref string) ([]byte, error) {
	if ref == "" {
		return nil, errors.New("the launch names no ref")
	}
	at := ""
	for _, candidate := range []string{"refs/remotes/origin/" + ref, ref} {
		sha, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}")
		if err == nil && sha != "" {
			at = candidate
			break
		}
	}
	if at == "" {
		return nil, fmt.Errorf("this checkout holds no %s ref (fetch it, or check it out)", ref)
	}
	// Whether the commit carries the file is asked of its tree, not of the
	// read's exit status: cat-file fails both for a path the tree does not
	// have and for a file it does have whose object this checkout cannot
	// produce — one it never got (a promisor clone whose lazy fetch did not
	// run), one pruned, one corrupt. Only the first is errNoConfigAtRef's
	// definite answer, and D905 refuses the second rather than reading it as
	// it: a launch that did would take the claim and mint the budget token on
	// a job the project's policy did not choose, with nothing downstream to
	// catch it (a per-repository job carries no profile).
	listed, err := gitRead(ctx, root, "ls-tree", at, "--", "fugaro.yaml")
	if err != nil {
		return nil, fmt.Errorf("%s's tree could not be read out of this checkout (%s)", at, oneLineCLI(err.Error()))
	}
	if listed == "" {
		return nil, fmt.Errorf("%s has %w", at, errNoConfigAtRef)
	}
	out, err := gitCmd(ctx, root, "cat-file", "blob", at+":fugaro.yaml").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s names a fugaro.yaml this checkout could not read (%s)", at, oneLineCLI(gitFailure(err, out)))
	}
	return out, nil
}

// gitFailure is a failed git call's own words where it printed any: git puts
// the reason a read failed on stderr — a missing object, a corrupt one — and
// that is what tells a refusal's reader whether to fetch or to repair.
func gitFailure(err error, out []byte) string {
	if text := strings.TrimSpace(string(out)); text != "" {
		return text
	}
	return err.Error()
}

// errNoConfigAtRef is checkoutFugaroYAMLAtRef's one definite answer: the ref
// resolved, and the tree of the commit it names has no fugaro.yaml in it. It
// is never a read that failed — checkoutFugaroYAMLAtRef establishes this
// absence from the tree so that the two cannot be confused. A caller that can
// tell it from a failure reads it as "this repository has no document" rather
// than as a refusal: a repository with no document anywhere selects no clone
// workflow and no profile, so its launch keeps the per-repository job
// (refConfigAtRef; spec.md §2, §7).
var errNoConfigAtRef = errors.New("no fugaro.yaml at it")

// checkProjectJobAuthUnknown refuses a launch onto a shared project job whose
// own Claude credential the launcher cannot resolve, with the same guarantee
// the account gate's refusals make (checkProjectJobAccount): nothing of the
// launch happened.
//
// Refusing rather than launching is the conservative direction, and it is the
// only one that keeps the account gate's promise: a launch whose agent.auth
// cannot be told may be an OAuth one, whose account decides which job it lands
// on and which token that job holds (projectJobAccountFor), so a guess could
// start it on a job holding another account's token, or none. The launch is where the job is decided (launchProjectJobFor),
// so this runs before the claim, before the budget token and before any Cloud
// Run call — an unknown credential costs a refusal here, not a run that stops
// inside a job that cannot hold it.
//
// The fix named is the two ways out an operator has: repair the document the
// message names (a fugaro.yaml that does not resolve, a layer that no longer
// parses), or take the workflow off the shared-job path — a workflow that is
// not `checkout: clone` keeps the per-repository job it has always had
// (design §4.4).
func checkProjectJobAuthUnknown(repo, why string) error {
	return userErr("cannot tell which Claude credential a run of %s would use on the shared project job its workflow selects, so nothing was launched: %s. A run resolves agent.auth from the fugaro.yaml at the ref it checks out, over the project layer its own task carries (project_layer in task.json), and this launch is judged on that same layer with that same fugaro.yaml — read at that ref out of your checkout's git objects, never out of the working tree — rather than on a guess: a shared job holds the credential its profile was deployed with — the account segment of its own name — so a launch whose credential cannot be resolved is refused instead of started on a job that may not be able to serve it. Fix what the message names, or keep this repository on its own per-repository job (a workflow that is not `checkout: clone` keeps the job it has always had). Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called",
		repo, why)
}
