package batch

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// deployedJobEnv is the installation's values as a launcher hands them to this
// backend: one entry per name the deployed job a run stands in for carries
// (internal/infra's JobEnvNames), the bucket and the budget backend among them
// (F8). They are written into the task's variables, which are the run's whole
// environment on a VM, because a Batch job is created per run from nothing --
// where a Cloud Run execution is merged into the deployed job's own
// environment.
func deployedJobEnv() map[string]string {
	return map[string]string{
		"FUGARO_BUCKET":                  "gs://runs-proj-1234",
		"FUGARO_RTDB_URL":                "https://acme-default-rtdb.firebaseio.com",
		"FUGARO_FIREBASE_API_KEY":        "the-restricted-web-key",
		"FUGARO_CREDENTIALS_SERVICE_URL": "https://fugaro-credentials-abc.a.run.app",
		"FUGARO_COMPUTE_PRICES":          "0.00002,0.0000025",
		"FUGARO_MAX_RUN_USD":             "5",
	}
}

// TestABatchJobNamesItsBucketAndBudgetBackend is F8's red step: before BR8 a
// Batch job's variables were the six that name the run and a Docker socket,
// and a run had no bucket to write its results to and no budget backend to
// open its session against (the runner refuses a token without one,
// internal/runner/budget_session.go). Every value the deployed job carries is
// the deployed job's own, and the four entries this backend writes itself --
// the run, the backend it is on, the job's own name and the deployed job it
// stands in for -- are the ones a run reads where the two share a name.
func TestABatchJobNamesItsBucketAndBudgetBackend(t *testing.T) {
	ctx := context.Background()
	deployed := deployedJobEnv()
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.JobEnv = deployed })
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	vars := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	for name, want := range deployed {
		if got := vars[name]; got != want {
			t.Errorf("the task's %s = %v, want %q: the deployed job carries it, so the job standing in for it must too", name, got, want)
		}
	}
	// The deployed job says cloud-run under FUGARO_BACKEND; this job must say
	// cloud-batch, or a run inside it would read the wrong backend's rules.
	if got := vars[backend.BackendEnv]; got != backend.CloudBatch {
		t.Errorf("the task's %s = %v, want %s: the deployed job's own value must not decide which backend this run is on", backend.BackendEnv, got, backend.CloudBatch)
	}
	for name, want := range map[string]any{
		"FUGARO_RUN":         backendtest.Slug + "/" + spec().RunID,
		"FUGARO_PROJECT":     "acme",
		"FUGARO_GCP_PROJECT": testProject,
		"FUGARO_REGION":      testRegion,
		backend.BatchJobEnv:  ref.Name,
	} {
		if got := vars[name]; got != want {
			t.Errorf("the task's %s = %v, want %v: the job's own identity is written after the deployed job's values", name, got, want)
		}
	}
}

// TestABatchJobNamesItsDeployedJob is R6: a Batch job is named after the run
// and Batch injects nothing that names the job that run would have been a run
// of, so the launcher writes the deployed job's own name into
// backend.DeployedJobEnv, which the runner places the run by and sends to the
// credentials service (BR9, F9).
func TestABatchJobNamesItsDeployedJob(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	vars := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	want := gcp.JobName(backendtest.Slug, backendtest.Workflow)
	if got := vars[backend.DeployedJobEnv]; got != want {
		t.Errorf("the task's %s = %v, want %q: the per-repository workflow job the run stands in for", backend.DeployedJobEnv, got, want)
	}
	// A workflow that belongs to a shared project job stands in for that job,
	// whose name the launch already names (LaunchSpec.Job): BR10 is the task
	// that lets such a launch reach a job at all, and this is the name its
	// environment carries once it does.
	project := "fugaro-p-default-github-acme"
	if got := deployedJobName(backend.LaunchSpec{Repo: backend.RepoRef{Repo: backendtest.Repo, Slug: backendtest.Slug}, Workflow: backendtest.Workflow, Job: project}); got != project {
		t.Errorf("deployedJobName on a project job = %q, want %q", got, project)
	}
}

// TestALaunchEntryCannotSetThem: every name the deployed job's values arrive
// under is reserved against the launch's own entries, because a launch entry
// is applied into the same map: an entry naming the bucket, the budget
// backend, the credentials service's address, the price table or a cap would
// be the value the run believes about itself, which is the one thing the
// launcher's entry may not choose (backend.LaunchEnv). The refusal comes
// before the create, so nothing is sent for any of them.
func TestALaunchEntryCannotSetThem(t *testing.T) {
	ctx := context.Background()
	deployed := deployedJobEnv()
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.JobEnv = deployed })
	names := slices.Sorted(maps.Keys(deployed))
	names = append(names, backend.DeployedJobEnv)
	for _, name := range names {
		sent := len(bf.Creates())
		s := spec()
		s.Env = []string{name + "=someone-elses-" + name}
		if _, err := b.Launch(ctx, s); !errors.Is(err, backend.ErrRejected) {
			t.Fatalf("Launch with an env entry naming %s = %v, want ErrRejected", name, err)
		}
		if got := len(bf.Creates()); got != sent {
			t.Fatalf("a launch env naming %s was applied: %d job(s) created, want the %d before it", name, got, sent)
		}
	}
}
