package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
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
	// repository on another provider (in phase 3: every Bitbucket repository,
	// since no shared job serves bitbucket — design §4.5, D5 as amended, and
	// internal/infra's ProjectJobs) stays on its per-repository job, decided
	// here before any claim is taken or budget token minted.
	if provider := e.repoProvider(spec.Repo, co); provider != pr.Provider {
		return nil, nil
	}
	account, _ := config.ClaudeAccountOfSecrets(pr.Secrets)
	if err := checkProjectJobAccount(profile, spec.OAuthToken, account); err != nil {
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
