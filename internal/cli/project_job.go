package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
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

// launchProjectJobFor resolves the project job spec runs on, and returns nil
// for the per-repository path — the job of (slug, WorkflowVariant(workflow,
// token)), which the backend resolves itself. It never writes and never
// launches: it decides, and every refusal of the launch itself is elsewhere.
func (e *cloudEnv) launchProjectJobFor(ctx context.Context, spec *task.Spec) (*launchProjectJob, error) {
	// The workflow's own checkout: says whether this is a clone workflow, and
	// which profile it selects. Both are read from the repository's own
	// fugaro.yaml, so a checkout that cannot be read — the working directory
	// is not repo's (fugaro run --repo ... from a machine that does not hold
	// it), or it is and the file is missing or does not resolve over the
	// project layer — leaves no answer to either. checkoutParse says nil for
	// both, and the refusal below names both.
	co := checkoutConfig(ctx, spec.Repo)
	if co == nil {
		// For an installation that has published no profiles the per-repository
		// job is not a guess: it is the only job there is, and every launch
		// runs on it. With profiles the launch cannot tell a baked workflow's
		// per-repository job from a clone workflow's shared one, and picking
		// either silently would start the run on a job it may not belong to —
		// so the ambiguity is refused, naming the way out.
		//
		// The read is only the question "has this project published profiles
		// at all", so a policy this CLI cannot read is not refused here: that
		// is the launch gate's and the runner's refusal, in their own words,
		// and it is not this decision's — a launcher that calls jobs.run
		// itself must not be stopped by a job it resolves
		// (TestPolicySinceGateIsNotOnTheRunnerPath).
		p, found, err := e.policy.read(ctx, e.lc)
		if err != nil || !found || p == nil || len(p.Profiles) == 0 {
			return nil, nil
		}
		return nil, userErr("fugaro run --repo %s cannot tell which Cloud Run job it targets: the working directory is not %s's checkout, or its fugaro.yaml could not be read (see fugaro validate). A workflow's checkout: kind and profile are read from that checkout's fugaro.yaml, and a clone workflow runs on a shared project job where a baked one runs on the repository's own. Run it from %s's checkout", spec.Repo, spec.Repo, spec.Repo)
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
func (e *cloudEnv) repoProvider(repo string, co *config.Config) string {
	if r, ok := e.localRepo(repo); ok && r.Provider != "" {
		return r.Provider
	}
	return co.Git.Provider
}

// projectJobAccountFor is the account axis of the job a launch on profile pr
// targets, and the launch's gate on the Claude credential that job holds (plan
// task 3, design §4.4 rows 4 to 6).
//
// The account is the run's own credential's, and only two kinds of run have
// one: a named Claude account, selected by a workflow whose agent.auth is oauth
// and by nothing else (claudeAccountAllowed, run.go, before any launch), and
// the axis's one value that is not a Claude account at all — the project's own
// Anthropic API key (config.AnthropicKeyAccount), which is the account of its
// job for the api-key workflow D1417 pairs it with. Every other run — Vertex,
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
//   - the project's own API key is that job's account for an api-key run and
//     for no other run, whatever the task's oauth_token says
//     (claudeAccountAllowed admits it for api-key, and for "" — a pre-check
//     with no document to read an auth from — and leaves the judgement to
//     here). A run whose own workflow resolves any other auth is refused
//     rather than placed on the profile's no-account job: it would start on a
//     job its own credential does not name, which the runner halts on.
func projectJobAccountFor(p *projectpolicy.Policy, profile string, pr projectpolicy.Profile, auth, selected, provider, repo string) (string, error) {
	// The account is the run's own credential's: a named account is only ever
	// selected by a workflow whose agent.auth is oauth (claudeAccountAllowed,
	// run.go, before any launch), the project's own API key only by one whose
	// agent.auth is api-key (the arm below), and the default account and no
	// selection at all are both the empty account — the profile's no-account
	// job (projectJobAccount). Every other kind of run has no Claude account to
	// place and targets that same no-account job, whatever its task.json or a
	// stale selection says.
	account := ""
	switch {
	case auth == "api-key" && selected == config.AnthropicKeyAccount:
		// The axis's one value that is not a Claude account (D1417, D1422): it
		// names the project's own Anthropic API key, and a profile that lists it
		// deploys a job whose account segment is the sentinel itself
		// (internal/infra's ProjectJobs, mounting no token on it). A launch that
		// names it targets that job, and it is what the review class needs: the
		// review-only variant of THAT job is the pairing the key is served from,
		// so a launch resolving the profile's no-account job instead would run
		// in a variant the run refuses as a pairing that names no key. It is no
		// second way onto an account's job — the sentinel's job holds no token
		// for a run to pick up, and api-key is the one auth the sentinel
		// survives claudeAccountAllowed under, which is the auth D1417 pairs the
		// key with.
		account = selected
	case selected == config.AnthropicKeyAccount:
		// The same value under every other auth, and the arm that makes
		// claudeAccountAllowed's promise true: it admits the sentinel for
		// api-key, and for "" — the pre-check callers that resolve with no
		// document to read an auth from (a stored task repeated or retried) —
		// and it is this function that re-judges it at the launch that can read
		// one. Without this arm the sentinel falls to the no-account job below
		// and the run starts on it carrying an account its own configuration
		// does not use: a vertex or cascade run has no Claude account to place,
		// and the sentinel is no account under any auth but the one D1417 pairs
		// the key with, so the launch would take the claim, mint the budget
		// token and start Cloud Run on a job the run's own credential does not
		// name — and, on a profile whose no-account job mounts the default
		// account's token, hold a token the run never asked for.
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
		return "", userErr("this run's own credential is %s, the project's own Anthropic API key, and that key is a job's account only for a run whose own workflow reads it (agent.auth: api-key, D1417): the account axis is spelled from the run's own credential, and this run's agent.auth %s, so the key is no account this run can be placed by — the job it names is not the job this run's own credential names, and the run would start on a job its own configuration does not read. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Launch the run without the key, which is where the account its own workflow names is resolved, or, if this repository is meant to run on the project's key, set agent.auth: api-key in its fugaro.yaml — the auth whose job and review variant the key names",
			config.AnthropicKeyAccount, authText)
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
// account axis's one non-account value, the project's own Anthropic API key
// (config.AnthropicKeyAccount), which no claude_accounts entry may define and
// which has a sentence of its own below. The first two are refusals of the same
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
	// The axis's one value that is no account at all, and the sentence for it
	// cannot be the one below: that one names the account's token and the
	// claude_accounts entry whose allow_data_to list bounds it, while the
	// sentinel has no token and config.ValidateClaudeAccounts refuses to define
	// it in claude_accounts — so the move it names would be a key the operator
	// may not write. What this says instead is what the value is and where its
	// job comes from (D1417, D1422): the profile's own claude_accounts list is
	// what deploys it, and the key's own reach in the local config is what its
	// published list is derived from.
	if selected == config.AnthropicKeyAccount {
		return userErr("this run's own credential is %s, the project's own Anthropic API key, which is not a Claude account and is no job of the profile %q: a project job's Claude credential is fixed where the job is deployed — one job per (profile, provider, account), the account segment of the job's own name — and the key's job is the one a profile deploys by listing it, whose review-only variant is what a review of such a repository runs on. Nothing was launched: no claim was taken, no budget token was minted and Cloud Run was not called. Add %s to profiles.%s.claude_accounts in the project's local config — the profile's own allow_repos are then the repositories the key serves, and project_secrets.%s.allow_data_to must reach them — and run fugaro update to deploy the job and its review variant",
			config.AnthropicKeyAccount, profile, config.AnthropicKeyAccount, profile, config.AnthropicSecret)
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
	if root == "" {
		// launchProjectJobFor only reaches this from a launch that has a
		// checkout of its own (checkoutConfig returned a config), so this is
		// that checkout having gone away between the two reads.
		return "", checkProjectJobAuthUnknown(spec.Repo, "the repository's own checkout could not be found")
	}
	data, err := readFugaroYAMLAtRef(ctx, root, spec.Ref)
	if err != nil {
		return "", checkProjectJobAuthUnknown(spec.Repo, "the fugaro.yaml at the ref the run checks out ("+oneLineCLI(spec.Ref)+") could not be read out of this checkout ("+oneLineCLI(err.Error())+")")
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
	data, err := gitCmd(ctx, root, "cat-file", "blob", at+":fugaro.yaml").Output()
	if err != nil {
		return nil, fmt.Errorf("%s has no fugaro.yaml at it", at)
	}
	return data, nil
}

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
