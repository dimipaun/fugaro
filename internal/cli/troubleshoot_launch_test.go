package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// troubleshoot_launch_test.go is T-TC8's own behaviour: the two halves the tier
// calls (troubleshootCloudPlan, troubleshootLaunchCloud), driven here directly
// so a case is about the launch and not about the tier's order — that is T-TC7's
// file's subject, and it drives both seams with its own.
//
// Every case is the installation a real diagnosis lands in: a checkout of
// acme/app (the scope D2409 resolves), a workflow that is baked (so the launch
// resolves the installation's implicit review-only job rather than a shared
// project job), the credentials service opted in and the review variants the
// review fixture deploys, and the roles node the launch's own gate is judged
// against.

// tcEvidence is the failure the launch is about: the logged command's own two
// fields, as the tier hands them to the plan.
func tcEvidence() diagnoseagent.Evidence {
	return diagnoseagent.Evidence{RunID: "update-20261107183012-48213", Command: "update"}
}

// tcLaunchFixture is the installation a cloud diagnosis is launched into, and
// the plan the tier would launch from it — the real one (troubleshootPlanJob),
// so what the cases below launch is the spec this binary builds and not one
// written in a test.
func tcLaunchFixture(t *testing.T) (*projectJobFixture, troubleshootCloudJob) {
	t.Helper()
	f := newImplicitReviewFixture(t)
	projectJobCheckoutYAML(t, bakedOAuthRepoYAML(t))
	accessRunNode(t, f.launchBudgetFixture, userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA))
	accessRunAs(t, accessOperator)
	return f, tcLaunchPlan(t)
}

// tcLaunchPlan is the plan of the checkout the test is standing in, resolved by
// the seam the tier calls.
func tcLaunchPlan(t *testing.T) troubleshootCloudJob {
	t.Helper()
	job, err := troubleshootCloudPlan(t.Context(), tcEvidence())
	if err != nil {
		t.Fatalf("planning the cloud diagnosis: %v", err)
	}
	return job
}

// tcLaunchAnswers is the platform's answer that the diagnosis's job ran: the
// execution the launch started reaches a terminal state as it starts. Without
// it the fake leaves the execution pending, which is the other outcome and the
// one D2407's sentence is about.
func tcLaunchAnswers(t *testing.T, f *projectJobFixture) {
	t.Helper()
	f.run.OnRun = func(c gcpfake.RunCall) { f.run.SetState(c.Execution, backend.StateSucceeded) }
}

// tcLaunch is one launch of the plan, as the tier makes it.
func tcLaunch(t *testing.T, job troubleshootCloudJob) (string, error) {
	t.Helper()
	return troubleshootLaunchCloud(t.Context(), job.env, job.slug, job.spec)
}

// tcStoredTask is the task a run stored under slug, as the bucket holds it.
func tcStoredTask(t *testing.T, f *cloudFixture, slug, runID string) *task.Spec {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, slug, runID).ReadTask(context.Background())
	if err != nil {
		t.Fatalf("the run %s/%s stored no task this build can read: %v", slug, runID, err)
	}
	return spec
}

// TestTroubleshootLaunchAsksForTheOperatorRole is the admit half of the gate
// (F19, RS6a): a credential holding operator at the repository the diagnosis is
// scoped to launches it. The refusal the other role gets is the next case; this
// one is what says the gate asks for operator rather than refusing everybody,
// and it is also the launch's whole happy path: one execution, on the review
// variant, with the task stored.
func TestTroubleshootLaunchAsksForTheOperatorRole(t *testing.T) {
	f, job := tcLaunchFixture(t)
	tcLaunchAnswers(t, f)

	ref, err := tcLaunch(t, job)
	if err != nil {
		t.Fatalf("the operator's diagnosis: %v", err)
	}
	if ref != job.slug+"/"+job.spec.RunID {
		t.Errorf("the launch answered the run %q, want %q", ref, job.slug+"/"+job.spec.RunID)
	}
	if calls := f.run.RunRequests(); len(calls) != 1 {
		t.Errorf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if stored := tcStoredTask(t, f.cloudFixture, job.slug, job.spec.RunID); stored.Recipe == nil || stored.Recipe.Name != recipe.TroubleshootRecipe {
		t.Errorf("the stored task carries the recipe %+v, want the catalog's %s", stored.Recipe, recipe.TroubleshootRecipe)
	}
}

// TestTroubleshootLaunchRefusedForAViewer is the other half, and the case the
// plan's mutation check names: a credential holding viewer may read a run and
// not start one, so the launch is refused with the gate's own sentence — the
// one that names the role, the repository and the grant that would admit it —
// and nothing of the run exists afterwards.
//
// Mutation (run, restore): drop the requireAccess call from the launch and this
// case fails at the execution it should not have started.
func TestTroubleshootLaunchRefusedForAViewer(t *testing.T) {
	f, job := tcLaunchFixture(t)
	accessRunNode(t, f.launchBudgetFixture, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAs(t, accessViewer)

	_, err := tcLaunch(t, job)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the viewer's diagnosis: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"nothing was launched", "launching " + accessRepoA, "needs the role operator", "holds the role viewer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch asked Cloud Run for %d executions, want none: %v", len(calls), calls)
	}
}

// TestTroubleshootLaunchOutsideACheckoutDeclines is D2409: the scope is the
// working directory's checkout, and a failure that happened outside any
// checkout has no repository to be scoped to. The plan declines before the
// project is even opened, with the reason that names what to do, and the tier
// answers it through the offline path rather than launching a run under some
// other repository.
func TestTroubleshootLaunchOutsideACheckoutDeclines(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := troubleshootCloudPlan(t.Context(), tcEvidence())
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a plan outside a checkout: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"not a checkout", "fugaro troubleshoot", "checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the decline %q does not say %q", err, want)
		}
	}
}

// TestTroubleshootLaunchSetsItsOwnTimeout is D2407's bound: the diagnosis is
// fifteen minutes long and four dollars short of any coding run, written into
// the task's own overrides — where the run applies its cap from — and handed to
// the platform as the execution's task timeout, with the slack every launch's
// timeout carries.
//
// Mutation (run, restore): write no total timeout (or the workflow's own) into
// the spec and the launch is handed the wrong deadline: a diagnosis that hung
// would hold its job for the six hours a coding run is allowed.
func TestTroubleshootLaunchSetsItsOwnTimeout(t *testing.T) {
	f, job := tcLaunchFixture(t)
	tcLaunchAnswers(t, f)

	if job.spec.Overrides.TotalTimeout != troubleshootTimeout.String() {
		t.Errorf("the plan's task carries the total timeout %q, want %q", job.spec.Overrides.TotalTimeout, troubleshootTimeout.String())
	}
	if _, err := tcLaunch(t, job); err != nil {
		t.Fatalf("the diagnosis: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	// The platform's own spelling of the deadline (backend/gcp's run.go): the
	// launcher hands it a duration and the request carries whole seconds.
	want := fmt.Sprintf("%ds", int((troubleshootTimeout+backend.TaskTimeoutSlack)/time.Second))
	if calls[0].Timeout != want {
		t.Errorf("the execution's task timeout is %q, want %q", calls[0].Timeout, want)
	}
	if usd := tcStoredTask(t, f.cloudFixture, job.slug, job.spec.RunID).Overrides.MaxBudgetUSD; usd == nil || *usd != troubleshootMaxUSD {
		t.Errorf("the stored task caps the run at %v, want %v", usd, troubleshootMaxUSD)
	}
}

// TestTroubleshootLaunchRunsOnTheReviewVariant is D2403: the diagnosis runs on
// the review-only job, whose container mounts no App key and no Claude token,
// because the person's credential never enters it — the project's own cascade
// or key pays for the model call. It is the same variant a review runs on, and
// that is the whole reason the marker below exists.
//
// The launch carries no credential mount and no slip, and the job it starts is
// the installation's implicit review-only job for the provider (a baked
// workflow resolves no shared project job, and the review-only variant of the
// per-repository job is not deployed beside one either).
func TestTroubleshootLaunchRunsOnTheReviewVariant(t *testing.T) {
	f, job := tcLaunchFixture(t)
	tcLaunchAnswers(t, f)

	if _, err := tcLaunch(t, job); err != nil {
		t.Fatalf("the diagnosis: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if want := infra.ImplicitReviewJobName("github"); calls[0].Job != want {
		t.Errorf("the launch ran the job %q, want the review-only job %q", calls[0].Job, want)
	}
	if calls[0].Env[runner.EnvReviewJob] != "1" {
		t.Errorf("the execution's environment does not carry %s=1: %v", runner.EnvReviewJob, calls[0].Env)
	}
	for _, absent := range []string{envCredentialsServiceURL, envSlip} {
		if _, ok := calls[0].Env[absent]; ok {
			t.Errorf("the execution's environment carries %s, and a review-only variant mounts no credential: %v", absent, calls[0].Env)
		}
	}
}

// TestTroubleshootLaunchMarksTheRunForTheReviewVariant is T-TC12's field, which
// is what admits the run to the variant above: the stored task is a mode:
// artifact run of the catalog's troubleshoot recipe carrying the class marker,
// so the runner takes it for a review-class run (projectjob.go's runClass) and
// the launch that wrote the marker starts it on the job that can serve it —
// without it, the launch's own job halts the run at bootstrap (F20).
//
// Mutation (run, restore): drop ReviewClass from the spec and the run is
// refused at bootstrap by the variant its own task selected.
func TestTroubleshootLaunchMarksTheRunForTheReviewVariant(t *testing.T) {
	f, job := tcLaunchFixture(t)
	tcLaunchAnswers(t, f)

	if _, err := tcLaunch(t, job); err != nil {
		t.Fatalf("the diagnosis: %v", err)
	}
	spec := tcStoredTask(t, f.cloudFixture, job.slug, job.spec.RunID)
	if !spec.ReviewClass {
		t.Errorf("the stored task %+v is not marked for the review class", spec)
	}
	if spec.Mode != recipe.ModeArtifact {
		t.Errorf("the stored task's mode is %q, want %q", spec.Mode, recipe.ModeArtifact)
	}
	text, ok := recipe.CatalogText(recipe.TroubleshootRecipe)
	if !ok {
		t.Fatalf("%s is not in the catalog", recipe.TroubleshootRecipe)
	}
	if spec.Recipe == nil || spec.Recipe.YAML != string(text) || spec.Recipe.SHA256 != recipe.Sum(text) {
		t.Errorf("the stored task carries the recipe %+v, want the catalog's own text and hash", spec.Recipe)
	}
	if spec.Validate() != nil {
		t.Errorf("the stored task does not validate: %v", spec.Validate())
	}
}

// TestTroubleshootLaunchRetriesNothing is D2407's "never retries": one
// execution is started, and a job that does not answer within its timeout is
// not launched again — the tier answers with the sentence that names the
// timeout and sends the person to the evidence, and the run the launcher
// started is one Cloud Run execution, not two.
//
// The fake's execution stays pending, which is what a job that never answers
// looks like, and the wait's own bound is shrunk to a millisecond so the case
// does not sleep for fifteen minutes (troubleshootAwait is a variable for
// exactly this).
func TestTroubleshootLaunchRetriesNothing(t *testing.T) {
	f, job := tcLaunchFixture(t)
	old := troubleshootAwait
	troubleshootAwait = time.Millisecond
	t.Cleanup(func() { troubleshootAwait = old })

	ref, err := tcLaunch(t, job)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a diagnosis whose job never answered: ref = %q, err = %v (exit %d), want a refusal", ref, err, ExitCode(err))
	}
	if want := "did not answer within 15 minutes"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal %q does not say %q", err, want)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Errorf("the launch asked Cloud Run for %d executions, want exactly 1", len(calls))
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Errorf("the platform holds %d executions of the diagnosis, want 1", n)
	}
	// The run the launcher started is a run: its reference is answered even
	// though the job did not, because that is where its record is.
	if ref != job.slug+"/"+job.spec.RunID {
		t.Errorf("the refusal answered the run %q, want %q", ref, job.slug+"/"+job.spec.RunID)
	}
}

// TestTroubleshootLaunchWritesNoTaskWhenRefused is the roles gate's whole
// point: it runs before anything is written, so a credential that may not
// launch this repository leaves no task, no claim and no execution behind — the
// bucket the run would live in holds nothing of it.
func TestTroubleshootLaunchWritesNoTaskWhenRefused(t *testing.T) {
	f, job := tcLaunchFixture(t)
	accessRunNode(t, f.launchBudgetFixture, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAs(t, accessViewer)

	if _, err := tcLaunch(t, job); err == nil {
		t.Fatal("the viewer's diagnosis was launched, want a refusal")
	}
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := runstore.Open(b, job.slug, job.spec.RunID).ReadTask(context.Background()); err == nil {
		t.Error("the refused launch stored a task, want the run's prefix untouched")
	}
	// And nothing at all under the repository: a claim, a launch record or a
	// budget token on its own is a launch this gate was supposed to prevent.
	dir := filepath.Join(f.dir, "runs", "runs", filepath.FromSlash(job.slug))
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		entries, _ := os.ReadDir(dir)
		t.Errorf("the refused launch left %s behind (%d entries), want it untouched", dir, len(entries))
	}
}
