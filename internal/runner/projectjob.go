package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
)

// The run's job identity (plan task 3.3, design §4.1 and §12a items 1 and 2).
//
// Under the project-centric model one Cloud Run job is shared by every
// repository admitted to a profile, so the job a run is in no longer names one
// repository: the run has to know *which* job it is in to know which provider
// it may clone from, which admission list holds it and where its clone comes
// from. That identity is the (profile, provider, account) the job's own name
// was computed from (gcp.ProjectJobName, plan task 3.2), and the run gets it
// by computing every name the published policy defines and matching
// CLOUD_RUN_JOB against them — never by parsing the name (R2-5: derive
// truncates the readable part, profile names hold hyphens, an api-key job's
// account segment is empty and "fugaro-p-" is also the legacy stem of an owner
// named "p", so a shape test would misread a per-repository job as a project
// job and halt it).
//
// A per-repository job keeps phase 1's rules: it matches its own recomputed
// gcp.JobName(slug, variant), and the provider kind stays derived from the
// slug and the repository (Task 1.5 step 2). That is what keeps phase 3
// shippable to every installation at once while repositories migrate one at a
// time: a run in a job the policy does not describe is not "a project job with
// no profile", it is a per-repository job, and it runs exactly as it did.

// jobTriple is one job's identity: the (profile, provider, account) its name
// was computed from, all three of them (R2-11 — the account is the axis that
// keeps each Claude account's token readable only by the runs using it, so a
// pair would not be the identity).
//
// A zero triple is not an identity: it is what a run in a per-repository job
// has, and r.job.Profile == "" is how every reader tells the two apart.
type jobTriple struct {
	Profile  string
	Provider string
	Account  string
}

// projectJob is the identity this run adopted, and whether the job it is in is
// a shared project job at all. A run that is not on a project job keeps phase
// 1's rules: the provider comes from the slug and the repository, only the
// project's allow_repos applies, and the clone URL is the job's environment's.
func (r *run) projectJob() (jobTriple, bool) {
	return r.job, r.job.Profile != ""
}

// jobIdentity computes and matches this run's job identity, and is the run's
// whole identity decision (plan task 3.3 step 1). It is called once, from
// admissionCheck, which is the one place that runs for a run with a Cloud Run
// execution and before any git credential exists.
//
// It returns the (profile, provider, account) of a matched project job, or
// three empty strings and no error for a run in a per-repository job (the
// phase-1 path) and for a run with no Cloud Run execution at all. An error is
// a halt: a job the run cannot place is a job it may not adopt an identity
// from.
//
// The published policy is read here (memoized, so it is the same read the rest
// of the run uses: one node, one decode, one token take) because the set of
// names to compute *is* the policy: every profile it defines, with that
// profile's own provider and the account its mounts name.
func (r *run) jobIdentity(ctx context.Context) (profile, provider, account string, err error) {
	job, err := r.jobName()
	if err != nil {
		return "", "", "", err
	}
	if job == "" {
		// No Cloud Run execution: no job identity to bind to (R2-9). A docker
		// run, a local invocation and `fugaro exec --provider fake` never
		// reach this, which is also why the fake provider needs no exemption.
		return "", "", "", nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return "", "", "", err
	}
	id, project, err := matchJobName(job, p, found, r.d.Store.Slug(), r.spec.Workflow, r.spec.OAuthToken)
	if err != nil {
		return "", "", "", err
	}
	if !project {
		// The per-repository path, unchanged: nothing is adopted, and
		// admissionCheck derives the provider as it does today.
		return "", "", "", nil
	}
	r.job = id
	return id.Profile, id.Provider, id.Account, nil
}

// jobName is the name of the Cloud Run job this run is in, "" when the run has
// no Cloud Run execution.
//
// It is read from the platform's own report of the run's execution
// (Deps.Execution, which internal/cli fills from backend.ExecutionFromEnv) and
// not from the environment: a launcher can override any job env var, and the
// job's name is the one thing about the job a launcher cannot write (finding
// 4, 12a item 1). Deps.Execution carries the name the platform returned, and
// ExecutionFromEnv refuses an empty CLOUD_RUN_JOB before any of this runs
// (internal/backend's codec), so "no name" here means "not on Cloud Run" — and
// "not on Cloud Run" is local only when the invocation said so (Deps.Local).
// A process no backend claims is in the cloud (backend.Platform.InCloud fails
// closed) and has no job name to place, so it can neither adopt a project
// job's identity nor be told apart from one: it halts here, before any git
// credential (review C2).
func (r *run) jobName() (string, error) {
	if r.d.Execution == "" {
		if r.d.Local {
			return "", nil
		}
		return "", fmt.Errorf("this run reports no Cloud Run execution and is not a local invocation (fugaro exec --task-file): a process no backend claims is in the cloud, and with no job name it cannot be placed in the job it is in, so it adopts no identity and is not admitted. The run stops before any git credential is requested; check that the job's environment carries the platform's own variables (CLOUD_RUN_JOB, CLOUD_RUN_EXECUTION)")
	}
	job, err := backend.JobOf(r.d.Execution)
	if err != nil {
		return "", fmt.Errorf("the execution this run reports (%s) cannot be read back into a job name: %w", r.d.Execution, err)
	}
	return job, nil
}

// matchJobName decides which job job is, on values alone: the triple of the
// project job whose computed name it is (project = true), or the per-repository
// job of (slug, workflow, token) (project = false), or a halt.
//
// The order is the plan's: the computed project names first, because that match
// carries an identity; then the run's own per-repository job name, recomputed
// with gcp.WorkflowVariant so a named OAuth run matches the job it was launched
// as (B2 — the deployed job of a named run is JobName(slug,
// WorkflowVariant(wf, token)), and comparing against the plain workflow would
// refuse every one of its own executions, which is the bug internal/cli's
// cloud.go:271-300 documents for --run-id repeats, ls, logs, diagnose and
// follow-up); then nothing.
func matchJobName(job string, p *projectpolicy.Policy, found bool, slug, workflow, token string) (jobTriple, bool, error) {
	if found {
		for _, c := range projectJobCandidates(p) {
			if gcp.ProjectJobName(c.Profile, c.Provider, c.Account) == job {
				return c, true, nil
			}
		}
	}
	variant, err := legacyVariant(job, slug, workflow, token)
	if err != nil {
		return jobTriple{}, false, err
	}
	if job == gcp.JobName(slug, variant) {
		return jobTriple{}, false, nil
	}
	return jobTriple{}, false, cannotPlaceJob(job, p, found, slug, workflow, variant)
}

// projectJobCandidates is every (profile, provider, account) the published
// policy defines a job for: each profile it defines, with the provider the
// profile pins and the Claude account the profile's mounts name (Task 3.1 step
// 2, plan task 3.2 step 1). The set is computed once per run, from the one
// policy read, and its size is the cost note the plan makes: N profiles × M
// accounts of string comparisons, no regex and no prefix test.
//
// The account comes from the profile's own secret list through one function
// (config.ClaudeAccountOfSecrets), which is the axis internal/infra computed
// the deployed name with (projectJobSecrets): a name the runner would not
// recompute is a job every run of that profile halts on, so the two must be
// one rule. Phase 5's named accounts are already covered — a profile mounting
// claude-oauth-token-<name> computes that account's job — and a policy that
// later carries accounts of its own adds them here.
func projectJobCandidates(p *projectpolicy.Policy) []jobTriple {
	names := p.ProfileNames()
	out := make([]jobTriple, 0, len(names))
	for _, name := range names {
		pr := p.Profiles[name]
		account, _ := config.ClaudeAccountOfSecrets(pr.Secrets)
		out = append(out, jobTriple{Profile: name, Provider: pr.Provider, Account: account})
	}
	return out
}

// legacyVariant is gcp.WorkflowVariant(workflow, token) for the workflow the
// run's own task.json names, with the two refusals that keep the variant's
// preconditions honest: WorkflowVariant panics on a workflow or a token outside
// its charsets (its "_" join would be ambiguous), and task.Spec.Workflow is
// never shape-checked — config.WorkflowNameRE constrains fugaro.yaml's keys,
// not what reaches the spec, which an unvalidated --workflow flag, a stored
// task another process wrote or bucket data a launcher wrote directly can all
// carry unchanged.
//
// An empty workflow is refused here rather than passed on: a per-repository
// job's name is computed from a workflow, so a run whose task.json names none
// cannot be placed in the job it is in. The launcher always resolves one
// (internal/cli's resolveWorkflow: --workflow, else the repository's single
// workflow), so this is a bucket edit or a hand-written task, not a launch.
func legacyVariant(job, slug, workflow, token string) (string, error) {
	if workflow == "" {
		return "", fmt.Errorf("this run is in the Cloud Run job %s, and its task.json names no workflow: a per-repository job's name is computed from the slug the run is bound to and the workflow (%s for the slug %s), so a run that names none cannot be placed in the job it is in. The run stops rather than assuming the workflow its repository happens to have", job, "gcp.JobName(slug, gcp.WorkflowVariant(workflow, token))", slug)
	}
	variant, ok := jobWorkflow(workflow, token)
	if !ok {
		return "", fmt.Errorf("this run is in the Cloud Run job %s, and the workflow %q its task.json names is not a workflow name a job could have been built from (%s): the run stops rather than panicking on it and rather than assuming another workflow", job, workflow, config.WorkflowNameRE)
	}
	return variant, nil
}

// jobWorkflow is gcp.WorkflowVariant, refusing instead of panicking when the
// workflow or the account is outside the charset the variant's "_" join trusts.
// internal/cli's helper of the same name is the shipped precedent and exists
// for the same reason; the runner cannot call it (internal/cli is above the
// runner), and the recover() guard is the whole of the helper.
func jobWorkflow(workflow, token string) (variant string, ok bool) {
	defer func() {
		if recover() != nil {
			variant, ok = "", false
		}
	}()
	return gcp.WorkflowVariant(workflow, token), true
}

// cannotPlaceJob is the halt for a non-empty CLOUD_RUN_JOB that is neither a
// project job the published policy defines nor this run's own per-repository
// job: another installation's job, a job of a module version the policy no
// longer describes, or one created by hand. None of them is an identity the
// run may adopt — a run that did would take its provider, its admission and
// its clone URL from a name nobody in this project wrote — so it stops, naming
// CLOUD_RUN_JOB, the slug the run is bound to, the workflow and the job its own
// per-repository job would have been (the fix's other half).
//
// The policy is named because it is what the set is computed from: the message
// says which profiles this project's policy defines, so a reader can tell "the
// job is stale" from "the policy is". A policy that defines no profiles is a
// third case and says so: it is published — the rules a policy is read under
// are deployed — but it names no profile, so the set is empty and no job is a
// project job of it. Saying "no policy published" there would send the reader
// to deploy rules that are already deployed.
func cannotPlaceJob(job string, p *projectpolicy.Policy, found bool, slug, workflow, variant string) error {
	detail := "This project has published no policy, so it defines no project job at all"
	fix := "The fix is `fugaro doctor`, then `fugaro init --firebase` with the current Fugaro"
	if found {
		if names := p.ProfileNames(); len(names) > 0 {
			detail = fmt.Sprintf("This project's published policy defines the job profiles %s, and a project job is one whose name is gcp.ProjectJobName of one of their (profile, provider, account) triples", strings.Join(names, ", "))
		} else {
			detail = "This project's published policy defines no job profiles at all, so it defines no project job either: a project job is gcp.ProjectJobName of a (profile, provider, account) triple, and a policy with no profiles has no such triple"
			fix = "The fix is `fugaro doctor`, then `fugaro project policy publish` with the profiles this project's fugaro.yaml defines"
		}
	}
	return fmt.Errorf("this run is in the Cloud Run job %s, and that is neither a project job of this project's policy nor the per-repository job of this run (%s for the slug %s and the workflow %s): a job the run cannot place may be another installation's, a job of a module version this policy no longer describes, or one created by hand, and the run adopts no identity from it. %s. %s",
		job, gcp.JobName(slug, variant), slug, workflow, detail, fix)
}

// checkProjectCheckoutOrigin refuses, on a project job, any checkout already
// in the work directory (review C5). openCheckout opens an existing checkout
// as it is, and on a per-repository job the admission holds its origin to the
// task's repository (originURL feeds remoteAdmissionErr); a project job
// computes its clone URL and reads no origin, so without this a checkout of
// another repository — a baked image built for one, or a directory something
// left behind — would be fetched with the profile's credential and worked on
// under this run's slug.
//
// The refusal covers a checkout whose origin is the computed URL too, and that
// is deliberate: an origin is one line of the checkout's .git/config, which
// also holds a push URL, url rewrites and hooks — code and configuration the
// run would execute with the profile's credential — so no origin can vouch for
// a checkout the run did not make. Nothing legitimate is lost: project jobs run
// the installation's base image, which bakes no checkout, and each Cloud Run
// execution starts from that image. A run in a per-repository job, and a work
// directory with no checkout, pass.
func (r *run) checkProjectCheckoutOrigin(ctx context.Context, remote string) error {
	job, project := r.projectJob()
	if !project {
		return nil
	}
	repo, err := gitops.Open(r.d.WorkDir, nil)
	if err != nil {
		return nil // no checkout: the run clones remote
	}
	got := "no origin"
	if origin, err := strip(repo).OriginURL(ctx); err == nil {
		got = remoteDisplay(origin)
	}
	return fmt.Errorf("the work directory %s already holds a checkout (origin %s), and this run is in the project job %s, which clones %s itself: a project job's image bakes no checkout and the run works only on the one it clones, since a checkout it did not make carries configuration and hooks the profile's credential would run with, so the run stops before any git credential is requested",
		r.d.WorkDir, got, gcp.ProjectJobName(job.Profile, job.Provider, job.Account), remoteDisplay(remote))
}

// cloneURL is where this run clones from (design §4.1(1), plan task 3.3 step
// 3).
//
// On a project job it is computed, never read: task.json's repository, the
// provider the job's own name pins, and the project's clone URL for that
// repository when the policy names one. FUGARO_REMOTE is not consulted at all
// — it is a launcher-writable value (12a item 2, D10) and a shared job sets
// none, so a launcher that overrides it must not be able to point the run at
// another repository. The URL that comes back was checked against the same
// rules the deployer applies (repourl.CloneURL, one function for both), so a
// project job cannot clone from anything but its own repository.
//
// A run in a per-repository job keeps the phase-1 answer: the origin of a
// checkout already in the work directory, else the job's FUGARO_REMOTE.
func (r *run) cloneURL(ctx context.Context) (string, error) {
	job, project := r.projectJob()
	if !project {
		return r.originURL(ctx), nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return "", err
	}
	custom := ""
	if found {
		canonical, err := projectpolicy.Canonical(job.Provider, r.spec.Repo)
		if err != nil {
			return "", fmt.Errorf("the repository this run is for cannot be named in the project's policy: %w", err)
		}
		custom = p.CloneURL(canonical)
	}
	url, err := repourl.CloneURL(job.Provider, r.spec.Repo, custom)
	if err != nil {
		return "", fmt.Errorf("the clone URL of this run's repository on the job profile %q: %w", job.Profile, err)
	}
	return url, nil
}
