package cli

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The cloud diagnosis's launch (T-TC8): the plan that resolves what the tier
// launches, and the launch itself. troubleshoot.go declares the two as seams
// and calls them in the tier's own order (plan, pre-flight, upload, launch).
//
// D2402, D2403, D2407 and D2409 are what it is made of, and each is one fact
// about a run this command does not otherwise start:
//
//   - the task carries the catalog recipe `troubleshoot` — a mode: artifact
//     recipe whose one step is diagnose, which the runner runs in place of
//     render — and T-TC12's class marker beside it (D2411). The marker is what
//     admits the run to the review-only job variant, whose container mounts no
//     App key and no Claude token: without it the launch's own job halts the
//     run at bootstrap (F20), and its model call would not take the review
//     class's path through the credentials service.
//   - the scope is the working directory's checkout (D2409). A run lives under
//     runs/<slug>/ and every job clones a repository, so a failure that
//     happened outside any checkout has no repository to belong to: the tier
//     declines there, with the reason that names what to do.
//   - the roles gate runs before anything is written, so a credential that may
//     not launch this repository leaves no task, no token and no execution
//     behind.
//   - the bounds are the tier's own (D2407): USD 0.25 and fifteen minutes
//     against the three hours a coding run takes, written into the task — a
//     per-run cap the gateway enforces as it enforces any run's, and the
//     backend timeout the job is handed.
//   - it never retries. One execution is started, and a job that does not
//     answer within its timeout reaches the offline path with the sentence
//     D2407 names; there is no second launch and no --rerun path, because a
//     diagnosis that already spent its cap is a question that was asked.
//
// The launch itself is the one every other launch shape uses (launchRun): the
// claim that makes it at most once, the budget token the run's spend is bounded
// by, the credentials-service environment and the launch record are that
// function's, and a second path beside it is exactly the hole the daemon's own
// launcher refuses to grow (orchestrator_launcher.go). What this file adds is
// the plan, the task, the roles gate and the wait.

// D2407's two bounds, and how often the launcher looks at the execution it
// started.
const (
	// troubleshootMaxUSD is the run's own cap: a diagnosis is a few calls of a
	// model that reads code and logs, and the project's budget is a shared one,
	// so it is far below any run's per-run cap.
	//
	// It goes into the task as overrides.max_usd — the chain's dollar limit,
	// which the runner's chain layer resolves into the merged per_run_usd, the
	// figure the gateway's lease is taken against (run-limit-carry-over D1041
	// and D1059, the runner's policy.go and lease.go). It is the only override
	// that reaches that cap, and the merge keeps the smallest figure any layer
	// names: a diagnosis is a chain of one run, so the number it carries is its
	// own cap, while a project or a repository whose own budget.per_run_usd is
	// smaller keeps the smaller one.
	//
	// overrides.max_budget_usd is NOT this: that is the per-stage ceiling the
	// agent binary is launched with (Spec.Apply writes it into
	// agent.max_budget_usd), a figure Claude Code stops a stage at, outside the
	// gateway — a diagnosis bounded by it alone would be bounded by the
	// project's ordinary per-run cap instead.
	//
	// It is a field a runner older than launchLimitsSince refuses outright, so
	// carrying one is gated on the job image wherever the launch path judges a
	// spec — and the pre-flight judges this one through that same block, since
	// it is freshLaunchGates (T-TC1). The gate refuses nothing the recipe's own
	// does not: the diagnose step arrived in a later release than the chain
	// limits did.
	troubleshootMaxUSD = 0.25
	// troubleshootTimeout is the diagnosis's own total timeout: fifteen minutes
	// against the three hours a coding run takes, written into the task as
	// overrides.total_timeout — which is what the job's task timeout is handed
	// (launchTimeoutOf), and what the wait below is counted from.
	troubleshootTimeout = 15 * time.Minute
	// troubleshootPoll is how often the wait asks the platform about the
	// execution. The answer usually arrives within a minute or two and each ask
	// is one platform read, so this is where noticing at once stops being worth
	// the reads.
	troubleshootPoll = 5 * time.Second
)

// troubleshootAwait is how long the launch waits for the execution it started
// before it calls the job silent: the diagnosis's own timeout plus the slack
// the platform adds to the job's task timeout, which is a job still inside its
// allowance rather than one that did not answer.
//
// It is a variable because no test can wait fifteen minutes: the case that
// proves D2407's sentence — and the one execution it leaves behind — shrinks it
// to a few milliseconds and answers the platform with an execution that never
// finishes.
var troubleshootAwait = troubleshootTimeout + backend.TaskTimeoutSlack

// troubleshootPlanJob is the tier's plan (D2402, D2409): the repository the
// diagnosis is scoped to, the environment the run lives in, and the task it
// launches. It writes nothing — the tier opens the run's own prefix from what
// this answers — and T-TC1's pre-flight judges the spec before any of it
// exists.
//
// D2409's decline is the first thing here, before the project is even opened: a
// failure that happened outside any checkout has no repository to be scoped to,
// and the reason names the command that fixes it rather than a second failure.
func troubleshootPlanJob(ctx context.Context, ev diagnoseagent.Evidence) (troubleshootCloudJob, error) {
	repo, err := originRepo(ctx)
	if err != nil {
		return troubleshootCloudJob{}, userErr("the cloud diagnosis runs as a job of the repository this machine is in, and this directory is not a checkout of one: run `fugaro troubleshoot` from that repository's checkout, where the failed command ran, or log this machine in to Claude and the local diagnosis answers here instead")
	}
	env, err := openCloud(ctx, cloudOptions{})
	if err != nil {
		return troubleshootCloudJob{}, err
	}
	slug, spec, err := troubleshootDiagnosisSpec(ctx, env, ev, repo)
	if err != nil {
		return troubleshootCloudJob{}, err
	}
	return troubleshootCloudJob{env: env, slug: slug, spec: spec}, nil
}

// troubleshootDiagnosisSpec is the task a diagnosis is launched as: the
// repository the plan resolved, the ref that repository's checkout is on, the
// workflow it runs, the project layer the runner reads every run's
// configuration from, and the fields of D2402, D2403 and D2407 that are this
// tier's own.
//
// The recipe is the catalog's own text, embedded in the task rather than named
// for the runner to resolve: what a diagnosis runs has to be the recipe this
// binary ships — the one step kind the launch's gates are written for (T-TC6)
// and the two answer files the tier reads back by name — and a repository or a
// project recipe called `troubleshoot` would otherwise stand in for it. The
// runner still prefers a repository recipe of that name at the ref, exactly as
// it does for every other run: a repository that commits one gets the recipe it
// wrote, and the run's own class marker refuses that recipe when it holds no
// diagnose step (T-TC12).
func troubleshootDiagnosisSpec(ctx context.Context, env *cloudEnv, ev diagnoseagent.Evidence, repo string) (string, *task.Spec, error) {
	// A repository whose fugaro.yaml declares a checkout Fugaro cannot run yet
	// is refused here, the way every other launch from a checkout refuses it
	// (newSpec's and reviewSpec's own call): the launch reads that file
	// leniently, so without this the launch would derive the run's jobs and
	// images from a document the run itself would refuse at bootstrap.
	if err := refuseSharedCheckout(ctx, repo); err != nil {
		return "", nil, err
	}
	checkout := checkoutOf(ctx, repo)
	slug, err := env.repoSlug(repo, checkout)
	if err != nil {
		return "", nil, err
	}
	base, _ := launchBaseBranch(ctx, env, repo)
	// The checkout's own branch, as every other launch from a checkout takes it
	// (newRunRef). The note that call can print is dropped rather than shown
	// here: the plan has no writer of its own (the tier's plan seam takes
	// none), and the note's reader is a person who passed --ref — which this
	// command does not offer.
	ref := newRunRef(ctx, repo, base, io.Discard)
	workflow, err := resolveWorkflow(env, repo, "", checkout)
	if err != nil {
		return "", nil, err
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return "", nil, userErr("%v", err)
	}
	runID, err := task.NewRunID(time.Now(), crand.Reader)
	if err != nil {
		return "", nil, err
	}
	text, ok := recipe.CatalogText(recipe.TroubleshootRecipe)
	if !ok {
		return "", nil, userErr("the catalog recipe %s this binary ships is not in it, so there is nothing for a cloud diagnosis to run", recipe.TroubleshootRecipe)
	}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: ref, BaseBranch: base, Workflow: workflow,
		Task:        "Diagnose the failure of `fugaro " + ev.Command + "` logged on this machine as " + ev.RunID + ", from the evidence attached to this run, and answer with what failed and what to do about it.",
		Title:       "diagnose " + ev.RunID,
		RequestedBy: me,
		Mode:        recipe.ModeArtifact,
		ReviewClass: true,
		Recipe:      &task.Recipe{Name: recipe.TroubleshootRecipe, Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text), YAML: string(text)},
	}
	spec.Overrides.MaxUSD = ptr(troubleshootMaxUSD)
	spec.Overrides.TotalTimeout = troubleshootTimeout.String()
	// The project layer goes into the task's own copy, the way `fugaro run` and
	// the daemon's launcher put it there: the runner resolves the run's
	// configuration from the task and nowhere else, so a launch that skipped
	// this would start a diagnosis without the project's shared defaults — and
	// the pre-flight would judge the task without the layer the run reads.
	if _, err := embedProjectLayer(ctx, env, spec, io.Discard); err != nil {
		return "", nil, err
	}
	// The spec is judged once more before it is stored, over the whole task as
	// it will be written: a task the runner would refuse at bootstrap is a
	// container already paid for, and this is the launcher's own rule for every
	// shape it writes.
	if err := spec.Validate(); err != nil {
		return "", nil, userErr("%v", err)
	}
	return slug, spec, nil
}

// troubleshootLaunchRun is the launch itself: the roles gate, the task, one
// execution, and the reference the tier reads back from.
//
// The gate is the launch path's own, in its launch form (run_allowlist.go's
// gate block, which is T-TC1's pre-flight here): launching a repository takes
// the role operator at it, and it runs before the task, the claim, the token or
// the execution exist, so a credential that may not launch this repository
// leaves nothing behind. T-TC1's pre-flight asks the same gate, which is what
// makes a refusal reach the person with the gate's own sentence before any
// object is written; this is the same judgement made by the code that starts
// the run, so a launch reached by any other road is held to it too.
//
// The wait is what makes the tier's read-back possible: the answer is read from
// the run's own prefix as soon as this returns, so a launch that returned while
// the container was still starting would read nothing. It waits for the
// execution to reach a terminal state and answers D2407's sentence when it does
// not — this is where the diagnosis's own timeout bounds the launcher.
func troubleshootLaunchRun(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) (string, error) {
	// The provider kind is derived from the pair the run itself trusts — the
	// slug the run is stored under and the repository the task names — exactly
	// as run_allowlist.go derives it, sentence for sentence: no launch this CLI
	// builds can reach the refusal, and it is here for the day repoSlug and
	// runner.ProviderKinds stop agreeing.
	kind, ok := runner.ProviderForSlug(slug, spec.Repo)
	if !ok {
		return "", userErr("the slug this launch stores its run under (%s) is not the slug of %s under any git provider kind (%s): the run this launch would start refuses it for that reason, so the launch is refused before any execution exists",
			slug, spec.Repo, strings.Join(runner.ProviderKinds(), ", "))
	}
	canonical, err := launchCanonical(kind, spec.Repo)
	if err != nil {
		return "", err
	}
	if err := requireAccess(ctx, env, accesslib.ActionLaunch, canonical, "nothing was launched"); err != nil {
		return "", err
	}
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	if err := createTask(ctx, s, spec); err != nil {
		return "", err
	}
	res, err := launchRun(ctx, env, slug, spec, time.Now())
	if err != nil {
		return "", err
	}
	if err := troubleshootAwaitExecution(ctx, env, res.Execution); err != nil {
		return res.Run, err
	}
	return res.Run, nil
}

// troubleshootAwaitExecution waits for the execution the launch started to
// reach a terminal state, and answers D2407's sentence when it does not.
//
// The bound is the diagnosis's own timeout plus the platform's slack: a job
// that ran its whole allowance and was killed at the end of it has answered
// nothing, while one still inside that allowance is not silent yet. Nothing
// here cancels the execution: a job that outlives its bound is the platform's
// to reap, its record says what it did, and a second write to stop a run whose
// answer nobody is waiting for any more is not a thing this command does — and
// it is what makes D2407's "never retries" true of the launcher and not only of
// its sentences.
func troubleshootAwaitExecution(ctx context.Context, env *cloudEnv, execution string) error {
	deadline := time.Now().Add(troubleshootAwait)
	for {
		ex, err := env.be.Execution(ctx, execution)
		if err != nil {
			return remote(fmt.Errorf("reading the cloud diagnosis's execution %s: %w", execution, err))
		}
		if ex.State.Terminal() {
			return nil
		}
		if !time.Now().Before(deadline) {
			return userErr("the cloud diagnosis did not answer within %d minutes: the job was started once and is not launched again, so run the command again — or log this machine in to Claude and the local diagnosis answers here instead — and what the run did is in its record", int(troubleshootTimeout.Minutes()))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(troubleshootPoll):
		}
	}
}
