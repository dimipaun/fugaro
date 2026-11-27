package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/runstore"
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
// pair would not be the identity), and the class of job it is (CSR8).
//
// A zero triple is not an identity: it is what a run in a per-repository job
// has, and r.job.Profile == "" is how every reader tells the two apart.
type jobTriple struct {
	Profile  string
	Provider string
	Account  string
	Class    jobClass
	// Implicit marks the one kind of project job that is the installation's own
	// rather than a published policy profile's: the implicit review-only job of
	// the repository's provider (implicitReviewCandidates), the review job a
	// project has whether or not it declares a profile. The profile name its own
	// name spells is the default one, which a policy with no profiles block does
	// not define, so the readers that look a profile up — the admission and the
	// job's timeout class — have nothing to read for it: a review on that job is
	// admitted by the project's own allow_repos, and it is bounded by the job
	// the installation deployed rather than by a class no policy states. The
	// credential check needs it too, for the same reason and one step earlier
	// (checkProjectJobAccount).
	Implicit bool
}

// jobClass is which of the two jobs of one (profile, provider, account) axis a
// name is: the shared coding job the run's work runs on, or the review-only
// variant of it (CSR8). The class is not a property of the name — nothing
// parses the variant's suffix back out (R2-5) — it is a property of the
// candidate a name matched, and it is what tells checkProjectJobAccount
// whether the job it is looking at mounts a Claude token at all.
//
// classCoding is the zero value deliberately: every triple a policy computed
// before the variant existed is a coding job, and a literal without a Class is
// one.
type jobClass int

const (
	classCoding jobClass = iota
	classReview
)

// runClass is the class of job this run is: a run of the review class belongs
// on the review-only variant and every other run on the shared coding job.
//
// The markers are the launcher's own and never anything the job's environment
// says, so a container cannot be told which class it is by the name it was
// started under. There are two, and either is enough:
//
//   - spec.Mode == recipe.ModeReview, which is the mode finishLaunch sends a
//     run to the variant on (internal/cli/run.go). It is the task's mode and
//     not the pull requests it names: `fugaro run --pr N --recipe review-only`
//     carries ReviewPRs with an empty Mode and is launched onto the shared
//     coding job, so its run is a review (r.review, reviewmode.go) on the
//     coder's container, and only `fugaro review` — the class whose container
//     mounts no App key and no Claude token — carries the mode.
//   - spec.ReviewClass, the diagnosis launch's marker (docs/troubleshoot-cloud
//     D2411): the cloud tier of `fugaro troubleshoot` runs a mode: artifact
//     recipe (D2402) on the same credential-less variant (D2403), and the mode
//     alone would make it a coding-class run the variant's doors halt. The
//     mode still wins where the two would disagree — a mode: review spec is a
//     review whatever this field says — task.Validate refuses the field
//     outside mode: artifact, and resolveRecipe refuses a spec carrying it
//     against a recipe that holds no diagnose step (runner.go), so no other
//     run claims the class.
//
// Every reader of the class reads it here — checkJobClass, the second door on
// the same credential (checkProjectReviewJobAccount), and the model path
// (reviewClassRun, gateway.go) — so a diagnosis admitted by the second marker
// is admitted and served as a review is, without any of them being taught what
// a diagnosis is.
func (r *run) runClass() jobClass {
	if r.spec.Mode == recipe.ModeReview || r.spec.ReviewClass {
		return classReview
	}
	return classCoding
}

// reviewVariantAccount is one entry of config.ProjectJobAccounts' axis as the
// review-only variant's names spell it (config.ReviewVariantAccount plus
// config.ReviewVariantSuffix): the account segment of the variant's job name
// and service account, which is also the account ProjectJobOfPolicy reports
// for it.
func reviewVariantAccount(account string) string {
	return config.ReviewVariantAccount(account) + config.ReviewVariantSuffix
}

// projectJob is the identity this run adopted, and whether the job it is in is
// a shared project job at all. A run that is not on a project job keeps phase
// 1's rules: the provider comes from the slug and the repository, only the
// project's allow_repos applies, and the clone URL is the job's environment's.
func (r *run) projectJob() (jobTriple, bool) {
	return r.job, r.job.Profile != ""
}

// clampTotalToJob bounds this run's clock by the shared job it is in: the
// job's Cloud Run task timeout is its profile's timeout_class plus
// backend.TaskTimeoutSlack (internal/infra's ProjectJobs, and the same sum
// internal/cli's launchProjectJobFor reads), while the run's own clock is the
// workflow's timeouts.total, and nothing on the launch path compares the two.
// A repository whose workflow is left at the default is therefore launched with
// a three-hour budget onto a job Cloud Run kills at ninety-two minutes when the
// profile's class is ninety, and the container dies before finalize: no result
// and no ready pull request, where the ninety-minute default it replaced fitted
// that class. The job's own class is what the run may spend, so the resolved
// workflow is held to it before anything reads a timeout from it — the budget
// (r.budget), the record's reserve and the lock's expiry (lockcache.go) all
// derive from this one place, and the per-repository path needs no clamp at all
// because internal/infra derives that job's timeout from this same workflow.
//
// It shortens only, and only where the job's class is really the run's bound:
// a run whose task carries a `--total-timeout` override is launched with that
// total as the *execution's own* task timeout (backend.LaunchSpec.Timeout,
// which internal/cli sets to the override, and internal/backend/gcp puts on the
// execution's overrides), and an execution's timeout wins over its job's — so
// there the platform holds the run to its own total and nothing is clamped. A
// class at or above the total changes nothing either, so a run that already
// fits keeps its budget and an installation whose profile was sized for the new
// default behaves exactly as before.
//
// Every timeout that has to fit inside the total is cut with it, and by
// config.Validate's own rules, so the workflow the run goes on to use is one a
// repository could have written: the stage to the total, the verify cap to the
// total, the render cap to what the reserve leaves, and the reserve itself only
// when it no longer fits (a class at or below the reserve would otherwise leave
// the run exhausted before its first stage, ending it with nothing pushed).
// A cap left above the total is worse than a shorter one: a gate step bounded
// by a verify longer than the job outlives the job, and the run is killed in it
// — no finalize, no result — which is the outcome this clamp exists to prevent.
func (r *run) clampTotalToJob(ctx context.Context, wf *config.Workflow) error {
	job, project := r.projectJob()
	if !project {
		return nil
	}
	if r.spec.Overrides.TotalTimeout != "" {
		return nil
	}
	if job.Implicit {
		// The installation's implicit default review-only job has no class to
		// clamp to: the deployment sized it at the total a repository workflow
		// that names none is given (internal/infra's implicitReviewJobTimeout,
		// the job the launcher resolved this review onto), and no published
		// profile states a timeout_class for it. Clamping to a profile the
		// policy happens to define under the same name — a project whose
		// default profile pins another provider, where the two are different
		// jobs — would bound the run by a class its own job was not deployed
		// with, and it is the platform that holds this run to the job it is in.
		return nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	if !found || p == nil {
		// Unreachable: a project job's identity is only adopted by matching
		// the published policy's own names (jobIdentity), and
		// checkProjectJobAccount halts a run here without one.
		return nil
	}
	pr, err := p.ProfileFor(job.Profile)
	if err != nil {
		return nil
	}
	class, err := config.ParseDuration(pr.TimeoutClass)
	if err != nil || class <= 0 {
		// validTimeoutClass holds every published profile to a positive
		// duration and to the platform's own ceiling; an unreadable one is a
		// policy this run has no bound to apply from, not a reason to stop it
		// where the checks above let it through.
		return nil
	}
	if wf.Timeouts.Total.Duration <= class {
		return nil
	}
	r.d.Log.Warn("the run's timeouts.total is longer than the shared job it runs on; the run is bounded by the job's timeout_class",
		"total", wf.Timeouts.Total.Duration, "timeout_class", class,
		"job_profile", job.Profile, "job", gcp.ProjectJobName(job.Profile, job.Provider, job.Account))
	wf.Timeouts.Total.Duration = class
	if res := wf.Timeouts.FinalizeReserve.Duration; res >= class {
		wf.Timeouts.FinalizeReserve.Duration = class / 5
	}
	if st := wf.Timeouts.Stage.Duration; st > class {
		wf.Timeouts.Stage.Duration = class
	}
	if v, tot := wf.Timeouts.Verify.Duration, wf.Timeouts.Total.Duration; v > tot {
		wf.Timeouts.Verify.Duration = tot
	}
	if rd, room := wf.Timeouts.Render.Duration, wf.Timeouts.Total.Duration-wf.Timeouts.FinalizeReserve.Duration; rd > room {
		wf.Timeouts.Render.Duration = room
	}
	return nil
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
	if err := r.checkJobClass(id); err != nil {
		return "", "", "", err
	}
	r.job = id
	return id.Profile, id.Provider, id.Account, nil
}

// checkJobClass holds a run to the job of the class it is: a review run
// belongs on the review-only variant of its profile's job, and every other run
// on the shared one. Both directions are refusals, because both are a job the
// launcher does not put a run of that class in, and the two containers are not
// interchangeable: the variant mounts no App key and no Claude token and takes
// its model credential from the credentials service's review class (O1), while
// the shared job mounts the App key the review container exists to be without.
//
// The class is the run's own markers (r.runClass: the task's mode: review, or
// the review class a diagnosis's launch writes, both set before admission) and
// the candidate's, never a suffix read back out of the job's name (R2-5): a
// name is matched against the names the published policy computes, and what a
// matched name *means* is the candidate it matched. So a container cannot
// choose its class by the name it was started under — the name is the
// platform's, but a run of neither class is a run that may not adopt a review
// job's identity however it got there.
//
// It is a halt and not a silent correction in either direction: what a run may
// do with the credential the job it is in already holds is decided before it
// starts (the platform mounts it), so a run in the wrong job has already been
// handed the wrong container, and the only safe answer is to stop.
func (r *run) checkJobClass(id jobTriple) error {
	class := r.runClass()
	if id.Class == class {
		return nil
	}
	if class == classReview {
		return fmt.Errorf("this run is a review or a diagnosis, and it is in the Cloud Run job %s, which is the shared project job of the profile %q: a run of that class runs on the review-only variant of that job, %s, whose container mounts no App key and no Claude token, and never on the shared job, whose container holds both. What class a run is, is the launcher's marker (the task's mode: review, or the review class a diagnosis's launch writes) and the job the run is in, never the job's name. The run stops before any git credential is requested",
			gcp.ProjectJobName(id.Profile, id.Provider, id.Account), id.Profile,
			gcp.ProjectJobName(id.Profile, id.Provider, reviewVariantAccount(id.Account)))
	}
	return fmt.Errorf("this run is in the Cloud Run job %s, which is the review-only variant of the project job of the profile %q: the variant exists for a review that reads a pull request, or for a diagnosis that reads its own evidence, and pushes nothing either way; its container mounts no App key and no Claude token, and this run is neither a review nor a diagnosis. Every other run of the profile belongs on the shared project job of that profile. What class a run is, is the launcher's marker (the task's mode: review, or the review class a diagnosis's launch writes) and the job the run is in, never the job's name: this name is not read back out of the job. The run stops before any git credential is requested",
		gcp.ProjectJobName(id.Profile, id.Provider, id.Account), id.Profile)
}

// jobName is the name of the deployed job this run is a run of — the one name
// the run is placed by (matchJobName) and the one it reports to the credentials
// service (serviceIdentities) — and "" when the run has no job to name at all,
// which is the local invocation alone.
//
// On Cloud Run it is read from the platform's own report of the run's execution
// (Deps.Execution, which internal/cli fills from backend.ExecutionFromEnv) and
// not from the environment: a launcher can override any job env var, and the
// job's name is the one thing about the job a launcher cannot write (finding 4,
// 12a item 1). Deps.Execution carries the name the platform returned, and
// ExecutionFromEnv refuses an empty CLOUD_RUN_JOB before any of this runs
// (internal/backend's codec), so "no name" here means "not on Cloud Run" — and
// "not on Cloud Run" is local only when the invocation said so (Deps.Local).
// A process no backend claims is in the cloud (backend.Platform.InCloud fails
// closed) and has no job name to place, so it can neither adopt a project
// job's identity nor be told apart from one: it halts here, before any git
// credential (review C2).
//
// On Batch the platform reports no deployed job. A Batch job is created per run
// and named after the run (batch.JobID, design compute-backend.md §2.4), so the
// name such a report carries — FUGARO_BATCH_JOB, written by the launch
// (internal/backend/batch/env.go) — is one no deployer and no policy ever
// computed, and a run placed by it would end at cannotPlaceJob (F9). What a
// Batch job carries instead is the name of the deployed job its run stands in
// for (backend.DeployedJobEnv, R6): the very name CLOUD_RUN_JOB carries for a
// run on Cloud Run — gcp.JobName(slug, gcp.WorkflowVariant(workflow, token)) of a
// per-repository job, gcp.ProjectJobName(profile, provider, account) of a
// shared one — because the deployed job is the job the run's identity, its
// admission and its clone URL are all read from, and on Batch the launch is
// what knows which one that is (BR8 writes it). The name is therefore
// launcher-forgeable, exactly like FUGARO_RUN and FUGARO_BATCH_JOB, where the
// Cloud Run name is not, and the difference is deliberate rather than
// overlooked: everything this name decides is what the run believes about
// itself, while what bounds a Batch run is the job it is really on — whose
// service account the credentials service verifies every call by, reading no
// header for it (auth.go's verifyJob, C3.9), and whose container the platform
// mounts before the run starts.
//
// The backend is the one the platform's own signal claims, asked through
// backend.DetectPlatform: Cloud Run's CLOUD_RUN_EXECUTION first, Batch's
// BATCH_TASK_INDEX and BATCH_TASK_COUNT second, and neither for a process no
// platform claims. It is never a FUGARO_* variable, and never the shape of the
// name the run was handed. Both of those are the launcher's: FUGARO_BACKEND is
// written into the job definition, and Deps.Execution is composed by the codec
// out of FUGARO_GCP_PROJECT and FUGARO_REGION beside the platform's own
// CLOUD_RUN_JOB and CLOUD_RUN_EXECUTION, so a launcher that wrote a slash into
// the project or the region leaves a name that does not parse as a Cloud Run
// execution at all. A branch that read either of those would let that launcher
// take the Batch path on Cloud Run and choose the run's identity — its
// provider, its admission, its clone URL (finding 4, 12a item 1). Asking Cloud
// Run first is what closes it:
// a Cloud Run job whose definition also carries the Batch pair is placed by the
// job the platform put it in, and a Batch task whose launcher wrote
// CLOUD_RUN_EXECUTION into it stops on its own job id (cannotPlaceJob) rather
// than adopting an identity.
func (r *run) jobName() (string, error) {
	if r.d.Execution == "" {
		if r.d.Local {
			return "", nil
		}
		return "", fmt.Errorf("this run reports no Cloud Run execution and is not a local invocation (fugaro exec --task-file): a process no backend claims is in the cloud, and with no job name it cannot be placed in the job it is in, so it adopts no identity and is not admitted. The run stops before any git credential is requested; check that the job's environment carries the platform's own variables (CLOUD_RUN_JOB, CLOUD_RUN_EXECUTION)")
	}
	// The Batch branch is taken on the platform's own signal alone — the pair
	// Batch injects into every task — and Cloud Run's is asked first, so a Cloud
	// Run job whose definition also carries that pair is still placed by the job
	// the platform put it in (the paragraph above). On Batch the name the
	// platform reports is the run's own job id, which is not an identity
	// (batchDeployedJob).
	if backend.DetectPlatform(r.jobEnv).Backend() == backend.CloudBatch {
		return r.batchDeployedJob()
	}
	job, err := backend.JobOf(r.d.Execution)
	if err != nil {
		return "", fmt.Errorf("the execution this run reports (%s) cannot be read back into a job name: %w", r.d.Execution, err)
	}
	return job, nil
}

// batchDeployedJob is the deployed job a Batch run stands in for, read from the
// job's own environment (backend.DeployedJobEnv): the name the launch wrote
// there because nothing else on the VM names it (jobName's third paragraph).
//
// An environment that carries none is refused rather than placed: with no name
// the run cannot be told which job's identity it may adopt, and a run that
// placed itself some other way — by its own Batch job id, by its slug — would
// be taking a provider, an admission list and a clone URL from a name nobody
// wrote (R2-5). The state it names is a launch older than the release that
// writes the variable, which is why the fix is an update rather than anything
// the run can read.
func (r *run) batchDeployedJob() (string, error) {
	job := r.jobEnv(backend.DeployedJobEnv)
	if job == "" {
		return "", fmt.Errorf("this run's job is on the cloud-batch backend (%s is %q) and its environment names no deployed job (%s is not set): a Batch job is created per run and named after the run, so nothing on its VM names the job the run stands in for, and with no name the run cannot be placed in the job it is in — it adopts no identity and is not admitted. The launcher writes that name (batch-run-identity R6); a launch older than the release that writes it carries none, so update the Fugaro that launches this repository's runs (brew upgrade dimipaun/tap/fugaro, then fugaro update) and launch it again. The run stops before any git credential is requested",
			backend.BackendEnv, backend.CloudBatch, backend.DeployedJobEnv)
	}
	return job, nil
}

// matchJobName decides which job job is, on values alone: the triple of the
// project job whose computed name it is (project = true), or the per-repository
// job of (slug, workflow, token) (project = false), or a halt.
//
// The order is the plan's: the computed project names first, because that match
// carries an identity, and it is the one name a policy need not derive itself:
// the installation's implicit review-only job of this run's provider is in the
// set whether or not a policy is published (implicitReviewCandidates), and a
// profile the policy defines under the same name wins, which is the same job
// the deployer's collision gate leaves standing (internal/infra's
// implicitReviewVariant); then the run's own per-repository job name, recomputed
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
	for _, c := range implicitReviewCandidates() {
		if gcp.ProjectJobName(c.Profile, c.Provider, c.Account) == job {
			return c, true, nil
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
// profile pins, and one entry per account the profile serves — its
// no-account job and one per account it lists (config.ProjectJobAccounts,
// the one function the deployer built the deployed jobs with and the
// launcher picks the job it launches with) — **and, beside each of those, the
// review-only variant of it** (CSR8), whose name is the same derivation over
// the account segment config.ReviewVariantAccount and config.ReviewVariantSuffix
// spell. The set is computed once per run, from the one policy read, and its
// size is the cost note the plan makes: N profiles × M accounts of string
// comparisons, no regex and no prefix test.
//
// The account comes from the profile's own claude_accounts field and from
// nothing else, because that is what internal/infra deployed the names from
// (ProjectJobs): a name the runner would not recompute is a job every run of
// that profile halts on, so the two must be one rule. It used to be read out
// of the profile's secrets: (config.ClaudeAccountOfSecrets), which was the
// same answer while no profile could mount a Claude token; a token in
// secrets: is still refused (projectpolicy's isClaudeOAuthSecret), so the
// field is now the only place the axis can come from.
//
// Both classes are in the one set on purpose, and the plan's reason is the
// security property: the runner places CLOUD_RUN_JOB here and the CLI's watch
// reads its job's triple from ProjectJobOfPolicy, which iterates this same
// set, so the two cannot disagree about which job is whose — and a review
// container, which holds no App key and no Claude token, believes it is the
// review job because the published policy computes that name and not because
// its own name says so.
//
// The installation's implicit review-only jobs are in the set beside the
// profiles, added when the policy does not derive their names itself — and they
// are the entries that can be there with no profile behind them, which is what
// makes a policy that declares no profile have exactly the installation's own
// review jobs (implicitReviewCandidates, reconciliation F3).
func projectJobCandidates(p *projectpolicy.Policy) []jobTriple {
	names := p.ProfileNames()
	out := make([]jobTriple, 0, len(names)+len(implicitReviewCandidates()))
	for _, name := range names {
		pr := p.Profiles[name]
		for _, account := range config.ProjectJobAccounts(pr.JobProfile) {
			out = append(out, jobTriple{Profile: name, Provider: pr.Provider, Account: account, Class: classCoding})
			out = append(out, jobTriple{Profile: name, Provider: pr.Provider, Account: reviewVariantAccount(account), Class: classReview})
		}
	}
	for _, imp := range implicitReviewCandidates() {
		name := gcp.ProjectJobName(imp.Profile, imp.Provider, imp.Account)
		derived := false
		for _, c := range out {
			if gcp.ProjectJobName(c.Profile, c.Provider, c.Account) == name {
				derived = true
				break
			}
		}
		if derived {
			// The policy derives this job itself — a github profile under
			// DefaultProfileName always derives the review variant of its
			// no-account job, which is what the deployer's collision gate
			// suppresses its own copy of the job for. The set is names, so the
			// derivation's entry is the entry, and the triple is the same one
			// either way.
			continue
		}
		out = append(out, imp)
	}
	return out
}

// implicitReviewCandidates is the identity of the installation's implicit
// default review-only jobs: one per provider kind (jobProviderKinds, the list
// internal/cli holds to config.Providers), under the deployer's implicit
// profile name and the default account's variant segment, which is the name
// internal/infra's ImplicitReviewJobName computes.
//
// It is a second set and not part of projectJobCandidates on purpose, and the
// difference is what each set is a function of. projectJobCandidates is the
// published policy and nothing else — that is what makes the runner's and the
// launcher's answer about a profile's job unforgeable — while these names exist
// whether or not a policy does: an installation deploys the job from its own
// local config, so a run in one has a job to bind to before the first policy is
// published. Nothing a policy says can add or remove one, and a profile the
// policy happens to define under the same name is matched first, which is the
// job the deployer's collision gate leaves standing.
//
// The profile name is projectpolicy.DefaultProfileName and not a local-config
// value: the deployer writes it into the job's own name (ImplicitReviewProfile),
// and a run reads the name back by computing it, never by parsing it (R2-5).
// jobProviderKinds is the whole list because it is the whole list of kinds a
// Cloud Run job's storage slug exists under: a run in a job of any other kind
// halts rather than adopting an identity no installation deploys.
func implicitReviewCandidates() []jobTriple {
	out := make([]jobTriple, 0, len(jobProviderKinds))
	for _, kind := range jobProviderKinds {
		out = append(out, jobTriple{
			Profile:  projectpolicy.DefaultProfileName,
			Provider: kind,
			Account:  reviewVariantAccount(""),
			Class:    classReview,
			Implicit: true,
		})
	}
	return out
}

// ProjectJobOfPolicy is the (profile, provider, account) of the project job
// named job that the published policy defines, and whether it defines one at
// all. It is the same computation and the same order matchJobName uses, so a
// reader outside this package — internal/cli's watch, which shows a run's job
// in its JSON — can never disagree with the runner about which job is whose,
// and neither of them ever parses the name back (R2-5).
//
// ok is false for a name no profile of the policy computes: a per-repository
// job, another installation's, or one a policy no longer describes. p may be
// nil, which is a project that has published no policy and so defines no
// project job.
//
// The installation's implicit review-only jobs are here like any other
// candidate, and they are the rows that may name a profile the policy does not
// define (implicitReviewCandidates): a reader that reads the profile's own
// fields out of the policy for one reads the zero value, which is why the
// runner holds such a run's admission and its clock to the project and to the
// deployed job instead.
func ProjectJobOfPolicy(p *projectpolicy.Policy, job string) (profile, provider, account string, ok bool) {
	if p == nil || job == "" {
		return "", "", "", false
	}
	for _, c := range projectJobCandidates(p) {
		if gcp.ProjectJobName(c.Profile, c.Provider, c.Account) == job {
			return c.Profile, c.Provider, c.Account, true
		}
	}
	return "", "", "", false
}

// ProjectJobDefaultProfile is the profile a workflow that names no job profile
// runs on, for a job serving provider, and "" when the policy defines none for
// that provider.
//
// It is the provider-default rule of D2310 and R8: a project that mixes
// providers has one default profile per provider, the project's own default
// (`standard`) for the first of them and `standard-<provider>` for another, so
// a workflow's silence resolves against the provider of the repository it is
// written in and not against one profile every repository would land on. The
// provider is read on both sides of the rule rather than trusted from the
// name: a profile called `standard-bitbucket` whose provider is github is not
// the Bitbucket default, and a policy that defines one is a policy whose
// repository the launch keeps on its per-repository job.
//
// It is exported because the launcher and the runner's candidate logic answer
// the same question over the same policy and must agree (contract.md: they
// answer `JobChoice` "name for name"). internal/cli's launchProjectJobFor
// calls it for a workflow that names no profile and computes the job's name
// from the profile it returns, and this package's candidate set
// (projectJobCandidates) is the set of names that answer holds over, so the
// launcher can only compute a name the runner places. A return of "" is the
// choice of no profile: the policy defines no profile for the repository's
// provider at all, and the launch keeps the per-repository job it has always
// had.
//
// p may be nil, which is a project that has published no policy and so has no
// default for any provider.
func ProjectJobDefaultProfile(p *projectpolicy.Policy, provider string) string {
	if p == nil || provider == "" {
		return ""
	}
	if pr, err := p.ProfileFor(projectpolicy.DefaultProfileName); err == nil && pr.Provider == provider {
		return projectpolicy.DefaultProfileName
	}
	named := projectpolicy.DefaultProfileName + "-" + provider
	if pr, err := p.ProfileFor(named); err == nil && pr.Provider == provider {
		return named
	}
	return ""
}

// ProjectJobIsImplicitReviewJob is whether job is the installation's implicit
// default review-only job as *this* policy defines it, rather than a job of one
// of its profiles — the flag matchJobName places the run by (jobTriple.Implicit,
// which the admission and the clock read), asked by name so a reader outside
// this package can place the same name the same way without computing the
// candidate set itself.
//
// The distinction is not readable off the name: a policy whose default github
// profile already derives that name holds ONE entry for it, under the profile's
// own triple, and the run in it is admitted by that profile rather than by the
// project's allow_repos (projectJobCandidates adds the implicit candidate only
// for a name the policy does not derive itself). A launcher that read such a
// name back as the implicit job would check the project's list where the run
// checks the profile's — or the reverse — and the launch and the run would
// disagree about the repositories the job serves, and about which side refuses
// a repository neither admits.
//
// false answers for a name no candidate computes as well as for a profile's job:
// both are names the implicit entry does not carry, and a caller that needs the
// difference asks ProjectJobOfPolicy for it.
func ProjectJobIsImplicitReviewJob(p *projectpolicy.Policy, job string) bool {
	if p == nil || job == "" {
		return false
	}
	for _, c := range projectJobCandidates(p) {
		if gcp.ProjectJobName(c.Profile, c.Provider, c.Account) == job {
			return c.Implicit
		}
	}
	return false
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
// are deployed — but it names no profile, so the only project jobs it derives
// are the installation's implicit review-only jobs (implicitReviewCandidates)
// and no other name is a project job of it. Saying "no policy published" there
// would send the reader to deploy rules that are already deployed.
//
// The fix is a command either way: publishing the policy a project job is
// defined by where there is none to define one, and the installation's own
// apply — which is what creates the project jobs, from that policy — where
// there is a policy and the job is one it no longer describes. That apply is
// the plain `fugaro update`, spelled with no flag: --firebase-project takes
// only the installation's own GCP project and is refused for any other, and a
// run holds no config to read that project from, so a placeholder after the
// flag would be a command that only some readers can run.
func cannotPlaceJob(job string, p *projectpolicy.Policy, found bool, slug, workflow, variant string) error {
	detail := "This project has published no policy, so it defines no project job at all"
	fix := "The fix is `fugaro doctor`, then `fugaro project policy publish` with the profiles this project's fugaro.yaml defines"
	if found {
		if names := p.ProfileNames(); len(names) > 0 {
			detail = fmt.Sprintf("This project's published policy defines the job profiles %s, and a project job is one whose name is gcp.ProjectJobName of one of their (profile, provider, account) triples", strings.Join(names, ", "))
			fix = "The fix is `fugaro doctor`, then `fugaro update` with the current Fugaro, whose first apply is the one that deploys this project's jobs from that policy"
		} else {
			implicit := implicitReviewCandidates()
			jobNames := make([]string, 0, len(implicit))
			for _, c := range implicit {
				jobNames = append(jobNames, gcp.ProjectJobName(c.Profile, c.Provider, c.Account))
			}
			detail = fmt.Sprintf("This project's published policy defines no job profiles at all, so the only project jobs it derives are the installation's implicit review-only jobs %s — review jobs — and that is not the job this run is in: every other project job is gcp.ProjectJobName of a (profile, provider, account) triple of a profile this policy defines, and a policy with no profiles defines none", strings.Join(jobNames, ", "))
		}
	}
	return fmt.Errorf("this run is in the Cloud Run job %s, and that is neither a project job of this project's policy nor the per-repository job of this run (%s for the slug %s and the workflow %s): a job the run cannot place may be another installation's, a job of a module version this policy no longer describes, or one created by hand, and the run adopts no identity from it. %s. %s",
		job, gcp.JobName(slug, variant), slug, workflow, detail, fix)
}

// checkProjectCheckoutOrigin settles the checkout a project job works on when
// its image already holds one: it is the run's own repository's, it is put into
// a state this run wrote, and only then does anything fetch through it (D2308,
// review F22). Every other repository the image baked is removed before the
// agent starts (D2309, review F23) — this job class's half of it, the other
// being openBakedCheckout's. A run in a per-repository job, and a work
// directory with no checkout at all, pass: the first is the admission's own
// case (openBakedCheckout), and the second clones.
//
// The guard this replaces refused every checkout a project job's image held
// (review C5), because a project job's image was the base one and baked none —
// so any checkout was something the run did not make, and its .git/config,
// which also holds push URLs, url rewrites and hooks, was code the profile's
// credential would have run. The job model no longer decides that: a project
// job runs the project's catalog image (S10), whose generated stages bake the
// checkout of every repository the entry serves (D2301), and the entry's own
// Dockerfile may clone into the work directory besides. What replaces the
// refusal is the reset gitops' ResetBakedGit performs — the configuration
// replaced by one the run writes, the hooks removed, the replace refs and
// grafts dropped, the build's leftovers cleaned — so what a run works on is a
// checkout whose configuration, hooks and refs are the run's own, whichever
// stage put the files there. A checkout whose origin is not this run's
// repository is refused before that reset, as it was before: no origin can
// vouch for a checkout of another repository.
//
// Which checkout it is comes from bakedProjectCheckout, and it is read before
// the provider is opened, so every refusal below is a run that requested no git
// credential and fetched nothing.
func (r *run) checkProjectCheckoutOrigin(ctx context.Context, remote string) error {
	job, project := r.projectJob()
	if !project {
		return nil
	}
	// A tree this run may not work on goes and the run clones its own (CF1,
	// D3440): the refusal a guard below writes is this run's answer to a
	// checkout the image holds that is not one it can use, and it is the same
	// answer openBakedCheckout gives one. A local invocation keeps the refusal
	// instead and stops (discardBakedCheckout, F24).
	if err := r.settleProjectCheckout(ctx, job, remote); err != nil {
		return r.discardBakedCheckout(ctx, err)
	}
	return nil
}

// settleProjectCheckout is checkProjectCheckoutOrigin's own work, split out so
// that every refusal it makes is one answer to one question — is the tree this
// project job's image holds usable by this run? — and the caller decides what a
// "no" does (discardBakedCheckout).
//
// "No" from the two returns below is the quietest one: the image holds no
// checkout of this run's repository at all — a project job's image bakes the
// project's repositories and this run's is not among them — so the run clones
// its own, and the clone path is the absence of a usable bake and nothing else
// (openCheckout reads r.cloned, D3440, F35).
func (r *run) settleProjectCheckout(ctx context.Context, job jobTriple, remote string) error {
	if err := r.removeOtherBakedRepos(); err != nil {
		return err
	}
	dir := r.bakedProjectCheckout()
	if dir == "" {
		// No checkout: the run clones remote.
		r.cloned = true
		return nil
	}
	repo, err := gitops.Open(dir, nil)
	if err != nil {
		// Not a checkout after all: the run clones remote.
		r.cloned = true
		return nil
	}
	origin, err := strip(repo).BakedGitOrigin(ctx)
	if err != nil {
		return fmt.Errorf("the checkout this run found at %s cannot be reset to the state this run works from: %w", dir, err)
	}
	// The image's own configuration is not the run's, and its origin is the one
	// line of it this run quotes: registered before anything can print it, as
	// the job's remote and a work-directory checkout's are (bootstrap).
	r.addRemoteSecrets(origin)
	if err := remoteAdmissionErr(origin, job.Provider, r.spec.Repo, r.d.Store.Slug()); err != nil {
		return fmt.Errorf("the checkout this run found at %s is not this run's repository (origin %s): %w", dir, remoteDisplay(origin), err)
	}
	if err := strip(repo).ResetBakedGit(ctx, remote, r.bakedHooksDir()); err != nil {
		return fmt.Errorf("the checkout this run found at %s cannot be reset to the state this run works from: %w", dir, err)
	}
	if dir == r.d.WorkDir {
		return nil // already the work directory: there is nothing to adopt
	}
	// The image may hold a second checkout, in the work directory the run
	// started in — a hand-written entry's own clone (D1208) beside the per-slug
	// stage an engine-generated one writes (D2301) — and it is verified here,
	// before this run works from the per-slug one: the guard this run replaces
	// refused a work-directory checkout of another repository, and a second one
	// left unchecked would readmit exactly that — the read of /work/repo C5's
	// refusal was about — because the run adopts the per-slug directory and
	// never looks at the work directory again.
	if err := r.refuseForeignWorkDirCheckout(ctx, job); err != nil {
		return err
	}
	// From here the checkout this run works on is this one, as it is for a
	// per-repository job that adopts its own baked path (openBakedCheckout),
	// and for the same reason: everything bootstrap reads of the checkout from
	// now on — which remote the agent's git and the push name, which tree a
	// stage runs in — reads this directory.
	if err := stateDirOutsideCheckout(dir, r.d.StateDir); err != nil {
		return err
	}
	r.d.WorkDir = dir
	// The record's image has to name the commit this run's checkout is at, and
	// the read bootstrap made ran while the work directory held no checkout of
	// this run's — where imageInfo falls back to the commit the image's own
	// record names, which belongs to whichever repository the image was built
	// from. Read here, after the adoption and before the sync below moves the
	// checkout off the baked commit, as openBakedCheckout's adoption does.
	if sha := r.bakedHead(ctx); sha != "" {
		if r.rec.Image == nil {
			r.rec.Image = &runstore.ImageInfo{}
		}
		r.rec.Image.BakedCommit = sha
	}
	return nil
}

// bakedProjectCheckout is the checkout a project job's image holds for this
// run, or the empty string when it holds none and the run clones.
//
// The directory the image baked this run's own repository at is preferred: it
// is the one an engine-generated bake stage writes (runner.BakedDir), which is
// the stage D2308 trusts, and it is the layout imagebake guarantees — the
// generated stages bake at a per-slug path and never at /work/repo, so a
// hand-written entry's own clone sits in the work directory beside it. That
// clone is taken when there is no per-slug checkout — a derived image whose
// Dockerfile clones is a definition D1208 leaves to its author — and it is
// reset like the baked one, since the guard this replaces refused it for the
// same reason it refused any other.
func (r *run) bakedProjectCheckout() string {
	for _, dir := range []string{BakedDir(r.d.WorkDir, r.d.Store.Slug()), r.d.WorkDir} {
		if _, err := gitops.Open(dir, nil); err == nil {
			return dir
		}
	}
	return ""
}

// refuseForeignWorkDirCheckout refuses a checkout of another repository in the
// work directory this run started in, when the checkout it adopts is the
// per-slug directory beside it (review F25).
//
// Which checkout is adopted is a preference, not an exclusion: an image whose
// entry both bakes the per-slug path and clones into its own WORKDIR holds a
// checkout of each, and the guard above reads only the one it adopts. The other
// is a checkout of another repository whenever the entry serves one, and a run
// that adopts the per-slug directory works from there — the agent's stages run
// in it, and nothing reads the work directory again — so the refusal has to be
// made here or it is not made at all.
//
// A checkout of this run's own repository in the work directory is left where
// it is: it is the source this run holds a grant over, and what the run works
// on is the checkout it reset and adopted.
func (r *run) refuseForeignWorkDirCheckout(ctx context.Context, job jobTriple) error {
	repo, err := gitops.Open(r.d.WorkDir, nil)
	if err != nil {
		return nil // no checkout beside this run's own: the run adopts what it found
	}
	origin, err := strip(repo).BakedGitOrigin(ctx)
	if err != nil {
		return fmt.Errorf("the checkout this run found at %s cannot be verified as this run's: %w", r.d.WorkDir, err)
	}
	r.addRemoteSecrets(origin)
	if err := remoteAdmissionErr(origin, job.Provider, r.spec.Repo, r.d.Store.Slug()); err != nil {
		return fmt.Errorf("the checkout this run found at %s is not this run's repository (origin %s): %w", r.d.WorkDir, remoteDisplay(origin), err)
	}
	return nil
}

// removeOtherBakedRepos removes every directory the image baked beside this
// run's own (D2309, review F23): a catalog image bakes the checkout of every
// repository its entry serves (D2301), so leaving them in place would let this
// run's agent read a repository this run holds no grant over and works on none
// of. Only the directory named by this run's storage slug stays — the run's own
// repository's, whichever stage baked it.
//
// Both job classes call it, each from the function that settles its checkout:
// checkProjectCheckoutOrigin for a project job, openBakedCheckout for a
// per-repository one. The read F23 names is a property of the shared image's
// layout (D1900: the per-repository class runs the project's catalog image too)
// and not of the job class, so neither class leaves a sibling for its agent.
// The call is before the checkout decision either way: a run that finds no
// baked checkout of its own clones, and the other repositories the image baked
// are no more its to read for that. The one run that reaches neither call is a
// per-repository job whose storage slug derives no provider kind
// (providerForSlug): it adopts no baked directory either, so the layout this
// removal is about is not the layout it runs in.
//
// The directories are found by their position beside the work directory, so
// before anything is removed the run has to be able to show that directory is
// the job image's own (review F24). A caller supplies a local invocation's
// (exec's --workdir, whose default only happens to spell the path a job image's
// WORKDIR does), and the directory beside a caller's is the caller's too —
// holding their own directories and files, not all of them repositories — so a
// local run removes nothing at all. Deps.Local is that distinction and the one
// place it is read: its zero value means the run is in the cloud, in a job the
// platform placed, whose work directory the job's image chose and no caller did.
// That is the layout this removal and D2309's read are about.
func (r *run) removeOtherBakedRepos() error {
	if r.d.Local {
		return nil
	}
	root := filepath.Join(filepath.Dir(r.d.WorkDir), BakedRepoRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the baked repositories at %s: %w", root, err)
	}
	keep := r.d.Store.Slug()
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing %s, which the image baked for a repository this run is not for: %w", path, err)
		}
	}
	return nil
}

// bakedHooksDir is the directory a reset baked checkout points core.hooksPath
// at: an empty directory this run owns, so no hook the image's build installed
// runs for this run — on the agent's commits, whose stamp pass-throughs chain
// to whatever the checkout configures (writeStampHooks), or anywhere else.
//
// It is under the state directory, the one place of the run's own that is
// outside the checkout (stateDirOutsideCheckout), and ResetBakedGit empties and
// re-makes it, so a file an earlier execution of the job left in it is not one
// a later hook reaches.
func (r *run) bakedHooksDir() string {
	return filepath.Join(r.d.StateDir, "baked-hooks")
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

// checkProjectJobAccount is the runner's own half of the launch gate (plan task
// 3, design §4.4 rows 6 to 9): the credential a run holds is a fact of the job
// it is in, and the run re-decides, from the policy and its own resolved
// configuration, that the two are the same one.
//
// The launcher refuses every case below before the claim and before the budget
// token (internal/cli's projectJobAccountFor), and this is deliberately a
// second, independent door: the launcher's answer is a value in the run's
// environment — CLOUD_RUN_JOB is the platform's and cannot be written, but the
// selection the run acts on is its own task.json, a bucket object the launcher
// writes and other things can too — so a run must never hold a credential its
// own configuration did not choose, nor be a credential-less OAuth run, merely
// because some other process said so. It is called once the run's own config is
// resolved (runner.go), since agent.auth is what says which credential this run
// is; the job identity and the policy come from admissionCheck, which runs
// before any git credential exists.
//
// Four rules, and each is a way for the job and the run to disagree:
//
//   - the job holds one account's token, or none (config.ProjectJobTokenAccount
//     on the profile the job's name pins — the same join the launcher and the
//     deployer make). A job holding no token is the no-account job of a profile
//     that does not list `default`: an OAuth run there would start with no
//     credential at all, so it is refused rather than left to run against an
//     API key the job may not even carry — the run's own auth is oauth, and
//     agent.BuildEnv refuses to write ANTHROPIC_API_KEY for it
//     (internal/agent), so the alternative to refusing is a run with nothing
//     to authenticate with;
//   - a run that is not an OAuth run selects no account, so it may only be in
//     the profile's no-account job: a non-OAuth run in an account job would
//     hold that account's token while its own configuration asks for another
//     credential entirely;
//   - an OAuth run may only be in the job of the account its own configuration
//     selected (the default account and no selection are the same selection,
//     and ride the no-account job, D5);
//   - and that account's own policy list must cover this repository: the token
//     is the account's blast radius (§6.1), and accounts.<name>.allow_repos is
//     the per-repository decision of which runs may use it (D3).
//
// A run in a per-repository job is not this check's business: there the account
// is part of the job's own name (gcp.WorkflowVariant), the job mounts exactly
// the selected account's token, and the per-repository rules hold unchanged.
//
// A run in a review-only variant is the review arm's
// (checkProjectReviewJobAccount), and it is asked before the published policy is
// read at all: the variant holds no token whose account the policy could bound,
// and the job it is in may be the installation's implicit default review-only
// job, whose profile no policy need define.
func (r *run) checkProjectJobAccount(ctx context.Context, auth string) error {
	job, project := r.projectJob()
	if !project {
		return nil
	}
	if job.Class == classReview {
		// The review arm is asked before the policy is, because nothing it
		// decides is the policy's: what a review container holds is its
		// class's and never a profile's token, and the job may be the
		// installation's implicit review-only job, whose profile no policy
		// with a profiles block of its own need define
		// (implicitReviewCandidates).
		return r.checkProjectReviewJobAccount(job)
	}
	if r.projectJobServed() {
		// The arm of a job whose model credentials the credentials service
		// serves. It is asked after the review arm and never before it: the
		// review arm's check is what keeps a coding run out of a review
		// container, and the review variant's environment is its coding job's
		// (internal/infra's reviewVariant clones it), so a served profile's
		// review job carries this fact too — an arm asked first would admit
		// the very run the review arm exists to refuse.
		return r.checkProjectJobServed(job)
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	if !found || p == nil {
		// Unreachable: jobIdentity only adopts a coding project job's identity
		// by matching the published policy's own names, so a run with one has
		// read a policy. Kept as the halt it would be — a token is not
		// allowed to ride on an identity nothing describes.
		return fmt.Errorf("this run is in the project job %s, and this project has published no policy to read its account from: the run stops rather than holding a credential no policy bounds",
			gcp.ProjectJobName(job.Profile, job.Provider, job.Account))
	}
	pr, err := p.ProfileFor(job.Profile)
	if err != nil {
		return fmt.Errorf("this run is in the project job %s: %w", gcp.ProjectJobName(job.Profile, job.Provider, job.Account), err)
	}
	account, oauth := "", auth == "oauth"
	if oauth {
		account = claudeAccountOf(r.spec.OAuthToken)
	}
	tokenAccount, serves := config.ProjectJobTokenAccount(pr.JobProfile, job.Account)
	if !serves {
		// The job holds no Claude token at all. A non-OAuth run is the run that
		// job is for; an OAuth one has nothing to authenticate with.
		if oauth {
			return fmt.Errorf("this run's own credential is the Claude OAuth account %q, and this run is in the project job %s, which holds no Claude token: the job profile %q serves no such account on it — the account's token is mounted into the job of that account and no other, and the profile's no-account job mounts one only when the profile lists %q (config.ProjectJobTokenAccount) — so this run would start with no credential at all. The run stops rather than authenticating as something its own configuration did not choose or falling back to an API key: agent.auth is oauth, and this release writes no API key for one (agent.BuildEnv). The fix is the launcher's: an OAuth run is only launched onto the job of the account its own credential selects. This run's job is the no-account job of the profile %s",
				selectedName(r.spec.OAuthToken), gcp.ProjectJobName(job.Profile, job.Provider, job.Account), job.Profile, config.DefaultClaudeAccount, job.Profile)
		}
		return nil
	}
	if account != job.Account {
		return fmt.Errorf("this run is in the project job %s, whose container holds the Claude account %q's token (%s, mounted as CLAUDE_CODE_OAUTH_TOKEN) for every run of it, and the run's own credential is not that account's: agent.auth is %q and the task selects %q (the default account and no selection are the same selection). A project job holds one account's token and no other, and the platform mounts it before the run starts, so this run would hold a credential its own configuration does not use — and would use the account's token for whatever the run does. The run stops rather than running as an account its own configuration did not choose. The fix is the launcher's: a run is only launched onto the job of the account it selects",
			gcp.ProjectJobName(job.Profile, job.Provider, job.Account), tokenAccount,
			config.ClaudeAccountSecret(tokenAccount), auth, selectedName(r.spec.OAuthToken))
	}
	acc, ok := p.Accounts[tokenAccount]
	if !ok {
		// Unreachable: a profile naming an account the accounts block does not
		// define is refused by the policy's own decoder
		// (projectpolicy.validProfileAccounts). Kept as a halt, because the
		// list is the whole of the rule that bounds the token (D3) and an
		// absent one is a gate that is not there.
		return fmt.Errorf("this run is in the project job %s, which holds the Claude account %q's token (%s), and the published policy describes no list of repositories that token may be used by: accounts.%s is absent, so nothing bounds which repositories may hold it. The run stops rather than using an unbounded token",
			gcp.ProjectJobName(job.Profile, job.Provider, job.Account), tokenAccount, config.ClaudeAccountSecret(tokenAccount), tokenAccount)
	}
	canonical, err := projectpolicy.Canonical(job.Provider, r.spec.Repo)
	if err != nil {
		return fmt.Errorf("the repository this run is for (%s) cannot be named in the project's policy: %w", r.spec.Repo, err)
	}
	if projectpolicy.AdmitRepo(acc.AllowRepos, canonical) {
		return nil
	}
	return fmt.Errorf("this run is in the project job %s, which holds the Claude account %q's token (%s, mounted as CLAUDE_CODE_OAUTH_TOKEN), and this repository is not on the list of repositories that token may be used by: accounts.%s.allow_repos is %s, and %s is not one of them. The account's list is derived at publish from the project's local config (%s), and it is the per-repository decision of which runs may use that token (D3). The run stops before the agent starts, independently of the launcher that started it",
		gcp.ProjectJobName(job.Profile, job.Provider, job.Account), tokenAccount, config.ClaudeAccountSecret(tokenAccount),
		tokenAccount, strings.Join(acc.AllowRepos, ", "), canonical, allowReposSource(tokenAccount))
}

// checkProjectReviewJobAccount is checkProjectJobAccount's review arm: a run
// in a review-only job, the variant of a profile's job or the installation's
// implicit one (jobTriple.Implicit), which the job identity has already held to
// its class (checkJobClass). Neither shape reads a profile: the variant's job
// is the profile's by its name alone, and the implicit job's profile is the
// deployer's own, which no policy defines — so the coding arm's lookup is not
// this arm's business and, for the implicit job, would refuse the run.
//
// It admits, and the three rules the coding arm applies are each refused for a
// reason that does not hold here:
//
//   - The variant holds no Claude token at all (internal/infra's
//     ProjectReviewJobs mounts nothing under O1), so the token axis has no
//     answer for it: config.ProjectJobTokenAccount knows the profile's coding
//     jobs only — the variant's account segment is the profile's account with
//     config.ReviewVariantSuffix on it, which no profile lists — and its
//     "does this job hold a token" would read false on a job that holds none
//     by design. Asking it anyway is what refuses an OAuth review run with
//     "this job holds no Claude token", which is the refusal this arm is for.
//   - The run's own credential is therefore not the account the axis names,
//     and that is right: a review job's credential is its class's, not the
//     run's selection. The credentials service mints the read-only
//     installation token and serves the reviewer's model calls to the caller
//     it verified (D893 (c), O1), keyed on the account the deployment gave the
//     variant. The coding arm's refusal — a run holding a credential its own
//     configuration did not choose — is about a token the job already holds,
//     and this job holds none, so it does not apply.
//   - The account's allow_repos list is not read either, for the same reason:
//     it is the bound on a token's blast radius (D3) and there is no token
//     here. Which repositories a review may be about is the profile's
//     admission, which admissionCheck applied before this check runs.
//
// The arm is keyed on the run's own marker and on the candidate's class, never
// on the job name's suffix parsed back out (R2-5).
func (r *run) checkProjectReviewJobAccount(job jobTriple) error {
	// The run's class is read through the same function checkJobClass reads it
	// through (r.runClass), so the two doors on the credential a review job
	// holds cannot come to disagree about which class a run is.
	if r.runClass() != classReview {
		// Unreachable: checkJobClass refuses a non-review run in a review job
		// in jobIdentity, which runs first and is the one place the class is
		// decided. Kept as the halt it would be, because this check is the
		// run's second, independent door on the credential it holds and a door
		// that admits what the first refused is not one.
		return fmt.Errorf("this run is in the review-only project job %s, and it is neither a review nor a diagnosis: that job's container holds no Claude token and no App key, and what a run of it may do is decided by its class, so no other run may be admitted to it. The run stops before the agent starts",
			gcp.ProjectJobName(job.Profile, job.Provider, job.Account))
	}
	return nil
}

// projectJobServed reports whether this run's job is one whose model
// credentials the credentials service serves: the fact the deployer plans into
// the job's own environment (config.CredentialsServedEnv, C1 (h)) exactly when
// the class's mounts are gone from it (internal/infra's ProjectJobs).
//
// It is read from the execution's environment and from nothing else, the way
// the service's address is (credentialsServiceURLEnv): both are the deployer's
// own statements about the container this run is in. What it is NOT is a claim
// a run makes about itself — the value is a constant, and the reader asks the
// environment it was started with.
//
// The second clause is the container's own answer and not a restatement of the
// first: the fact is an environment entry, which a launch override can write as
// readily as the deployer's job definition can, while the mount it is about is
// the deployer's — internal/infra's sharedJobServedEnv moves the two together,
// and this is where they are held to each other. So a container that still
// carries one of the class's mounts is not a served job whatever its
// environment says, and the run in it meets the coding arm — the account and
// its allow_repos list — rather than being let past the door holding a token
// nobody bounded (D3).
func (r *run) projectJobServed() bool {
	return envLookup(r.d.Env, config.CredentialsServedEnv) == config.ServedValue && !mountedModelEnv(r.d.Env)
}

// mountedModelEnv reports whether env carries one of the model credentials a
// served class drops (config.ModelSecretEnvs) — the mount side of the fact
// config.CredentialsServedEnv states, asked of the container rather than taken
// on the environment's word. A bare name counts as carried, since exec reads it
// as the parent's value.
func mountedModelEnv(env []string) bool {
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); slices.Contains(config.ModelSecretEnvs, name) {
			return true
		}
	}
	return false
}

// checkProjectJobServed is checkProjectJobAccount's arm for a job whose model
// credentials the credentials service serves (projectJobServed).
//
// The coding arm's three rules are each refused for a reason that does not hold
// here, and the first is what this arm exists for:
//
//   - The job holds no Claude token, so the account axis has nothing to be
//     about. The axis is a statement about the token a job mounts
//     (config.ProjectJobTokenAccount), and the class's mounts are exactly what
//     the service took away — so comparing the run's own credential against
//     the job's account would refuse every run of a job that is doing what it
//     was deployed to do, and admitting the comparison would be reading an
//     account name to choose a credential the container does not hold. Which
//     credential serves a call is the credentials service's answer now, walked
//     from the project's routing table (R2: the in-job gateway holds no key and
//     no table), and this door reads neither agent.auth nor an account name to
//     choose one.
//   - The account's allow_repos list is not read either, for the same reason:
//     it is the bound on a token's blast radius (D3), and there is no token
//     here. Which repositories may reach which credential is the table's
//     admissions, which the service refuses a call by.
//   - What is left is the one thing that can still be wrong, and the one thing
//     this arm refuses over: a run with no way to a model at all. The job holds
//     no credential, so a run whose model path reaches neither the credentials
//     service nor a credential that is not a mount would die at its first model
//     call with nothing to fall back on (G17, D143). The refusal names the job
//     and the run's own auth and is the launcher's to fix, because what decides
//     whether a job is served at all is the profile's switch — one repository
//     cannot take a shared job off it.
//
// A run whose auth is vertex is admitted without the service and is not an
// exception to the rule above: Vertex is the installation's own identity, the
// job's GCP service account, and it is no mount — the class never took it away
// and the service serves no such credential (config.IsSharedJobServedModelEnv
// holds a provider's key back for the same reason, and Vertex is not even a
// key).
func (r *run) checkProjectJobServed(job jobTriple) error {
	if r.serviceModelPath() || r.modelAuth() == "vertex" {
		return nil
	}
	return fmt.Errorf("this run is in the project job %s, which the credentials service serves: its container mounts none of the model credentials that class covers, so a run of it reaches a model through the service or not at all, and this run's model path reaches neither — agent.auth is %q, and the execution's environment carries no %s for the service. The fix is the launcher's: a run on a served job is handed the service's address, and which profiles are served at all is the deployer's own answer for the profile (B5.7), read from the repositories' records and not from this run. The run stops before the agent starts rather than reaching its first model call with no credential",
		gcp.ProjectJobName(job.Profile, job.Provider, job.Account), r.modelAuth(), credentialsServiceURLEnv)
}

// allowReposSource is where an account's policy list is derived from at publish
// (projectpolicy.DeriveAccounts): a named account's own allow_data_to in the
// project's local config, and the project_secrets entry for the default account,
// whose name is reserved in claude_accounts.
func allowReposSource(account string) string {
	if account == config.DefaultClaudeAccount {
		return "project_secrets." + config.ClaudeAccountSecret(account) + ".allow_data_to"
	}
	return "claude_accounts." + account + ".allow_data_to"
}

// claudeAccountOf is a selection as a job's account axis and the policy's
// accounts block spell it: the default account and no selection at all are both
// the empty account (internal/cli's projectJobAccount is the launcher's copy of
// this rule, and config.ProjectJobAccounts/ProjectJobTokenAccount are the same
// spelling).
func claudeAccountOf(selected string) string {
	if selected == config.DefaultClaudeAccount {
		return ""
	}
	return selected
}

// selectedName is a selection as a message spells it, "" included: the default
// account is the name of the account a run with no selection uses.
func selectedName(selected string) string {
	if selected == "" {
		return config.DefaultClaudeAccount
	}
	return selected
}
