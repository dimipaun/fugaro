package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
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
// account its mounts name are part of that name (plan task 3.2), and
// config.ClaudeAccountOfSecrets is the one function both sides compute the
// account with.

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

// launchProjectJobFor resolves the project job spec runs on, and returns nil
// for the per-repository path — the job of (slug, WorkflowVariant(workflow,
// token)), which the backend resolves itself. It never writes and never
// launches: it decides, and every refusal of the launch itself is elsewhere.
func (e *cloudEnv) launchProjectJobFor(ctx context.Context, spec *task.Spec) (*launchProjectJob, error) {
	// The workflow's own checkout: says whether this is a clone workflow, and
	// which profile it selects. No checkout in the working directory (fugaro
	// run --repo ... from a machine that does not hold the repository) means
	// no answer to either, and a launch that cannot tell keeps the job it has
	// always had.
	co := checkoutConfig(ctx, spec.Repo)
	if co == nil {
		return nil, nil
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
	account, _ := config.ClaudeAccountOfSecrets(pr.Secrets)
	if err := checkProjectJobAccount(profile, spec.OAuthToken, account); err != nil {
		return nil, err
	}
	// And a run whose own credential is an OAuth account cannot run here at
	// all: the job it would land on has none to read until phase 5 (task 3.4a,
	// the owner's ruling of 2026-10-17).
	if err := checkProjectJobOAuth(profile, co.Agent.Auth, spec.OAuthToken); err != nil {
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
func (e *cloudEnv) repoProvider(repo string, co *config.Config) string {
	if r, ok := e.localRepo(repo); ok && r.Provider != "" {
		return r.Provider
	}
	return co.Git.Provider
}

// checkProjectJobAccount refuses a launch whose run selects a Claude account
// the selected profile's job does not run as (plan task 3.4 step 1).
//
// On a per-repository job the account is part of the job's own name
// (gcp.JobName with gcp.WorkflowVariant(workflow, token)), so a run that
// selects an account is launched on the job that mounts that account's token.
// A project job's account is fixed where the job is deployed: it is the
// account segment of gcp.ProjectJobName, computed from the profile's secrets
// (internal/infra's projectJobSecrets), and the container reads that account's
// token, whatever the run's own task.json says. So a run that selects another
// account would run — and be recorded, and be shown by `runs ls` — as an
// account its own fugaro.yaml did not choose, and the credential the job holds
// is not the one this launch's allow_data_to check was made for (that check
// validates the selected name, agent.oauth_token or --token). The launch stops
// instead, as the rest of this release's selections do: a repository that must
// run as another account belongs on a profile that mounts it.
//
// The two selections are compared the way the model spells them elsewhere: the
// default account (config.DefaultClaudeAccount) and a profile that mounts no
// Claude token at all are both the empty account — gcp.WorkflowVariant joins no
// account segment for either, and the account axis of a job's name is empty for
// both — so a run that selects nothing is not refused on an api-key or Vertex
// installation for a token no job of it mounts.
func checkProjectJobAccount(profile, selected, mounted string) error {
	selected, mounted = projectJobAccount(selected), projectJobAccount(mounted)
	if selected == mounted {
		return nil
	}
	what := "mounts no Claude token, so its job runs as no account at all"
	if mounted != "" {
		what = fmt.Sprintf("mounts the Claude account %q, so its job runs as that account", mounted)
	}
	chose := "the default Claude account"
	if selected != "" {
		chose = fmt.Sprintf("the Claude account %q", selected)
	}
	return userErr("this run selects %s, and the job profile %q %s: a project job's Claude account is fixed where the job is deployed — it is the account segment of the job's own name, computed from the profile's secrets — and its container reads that account's token, so a run on it cannot run as another account, and the credential the job holds is not the one this launch's allow_data_to check was made for. The launch stops rather than running this repository's work as an account its own fugaro.yaml did not choose: name the profile's account in the repository's fugaro.yaml (agent.oauth_token), or run the repository on a profile that mounts the account it selects",
		chose, profile, what)
}

// projectJobAccount is a Claude account name as a job's account axis spells it:
// the default account and no account at all are both "". They are one selection
// everywhere the axis is computed — gcp.WorkflowVariant joins no segment for
// either, and internal/infra's projectJobSecrets reports "" for a profile that
// mounts no Claude token — so a comparison of two accounts is a comparison of
// what each names beyond the default.
func projectJobAccount(name string) string {
	if name == config.DefaultClaudeAccount {
		return ""
	}
	return name
}

// checkProjectJobOAuth refuses a launch onto a shared project job when the
// run's own Claude credential is an OAuth account (plan task 3.4a, the owner's
// ruling of 2026-10-17: "a Claude OAuth launch onto a shared project job is
// refused up front, with a clear message").
//
// An installation whose runs use `agent.auth: oauth` has no shared job, and
// will not until phase 5 decides how an account reaches one (design §4.1's
// shared-job section: "no Claude OAuth token on a shared job … an installation
// on `auth: oauth` has no usable shared job until then"). Every Claude OAuth
// token is refused on a shared job — a profile is shared by every repository
// admitted to it, and an account's allow_data_to is a per-repository list a
// shared job cannot honour — so `fugaro init` refuses to deploy a profile that
// mounts one, and the account axis of every project job's name is empty
// (internal/infra's projectJobSecrets, ProjectJobs). A run launched onto one
// therefore reads the profile's account, never the account its own
// configuration chose, and with `auth: oauth` there is no token of that
// account in the job's environment at all.
//
// Left to Cloud Run, that launch failed after it had spent its queueing slot:
// the claim was taken, the budget token was minted, and the run then stopped
// on a job that cannot serve it. It is refused here instead, where the job is
// decided — before the claim, before the token and before any Cloud Run call —
// as the provider mismatch beside it is (launchProjectJobFor).
//
// The question is the run's resolved agent.auth and nothing else: a named
// account is only ever selected by a run whose auth is oauth
// (claudeAccountAllowed, run.go), and spec.OAuthToken is empty for the default
// account (run.go sets it only for another one), so `auth != "oauth"` is the
// whole of "no OAuth account is involved here" — an api-key or Vertex run is
// untouched by this check whatever the profile mounts, and no other launch's
// behaviour changes. The account the run selected is read only to name it in
// the message: with `auth: oauth` it is the account the run's own configuration
// chose, the default one when it chose none.
//
// The refusal is scoped to the shared-job path, exactly as that one is: a
// repository whose workflow keeps its per-repository job — a baked workflow
// (design §4.4), or an installation that has published no profiles — launches
// an OAuth run as it always has, on the variant job that mounts its account's
// token.
func checkProjectJobOAuth(profile, auth, selected string) error {
	if auth != "oauth" {
		return nil
	}
	what := "this run's workflow runs as the default Claude OAuth account (its resolved agent.auth is oauth)"
	if selected != "" {
		what = fmt.Sprintf("this run selects the Claude OAuth account %q", selected)
	}
	return userErr("%s, and the launch would run it on the shared project job of the job profile %q: a Claude OAuth account is not supported on a shared project job until phase 5. A project job's Claude account is fixed where the job is deployed — it is the account segment of the job's own name, computed from the profile's secrets — and no profile may mount a Claude OAuth token in this release (fugaro init refuses to deploy such a job, and an account's allow_data_to is a per-repository list a shared job cannot honour), so an installation on auth: oauth has no usable shared job, and this launch is refused rather than started on a job that cannot serve it. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Run this repository on an API-key workflow (agent.auth: api-key); a repository that keeps its per-repository job — a baked workflow, or an installation that has published no profiles — is not refused here and launches exactly as it did",
		what, profile)
}
