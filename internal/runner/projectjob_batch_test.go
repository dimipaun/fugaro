package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the Batch half of the job identity (batch-run-identity F9, R6):
// a run whose job is on Cloud Batch rather than Cloud Run.
//
// On Cloud Run the platform names the job a run is in and the runner places the
// run by that name alone (projectjob_internal_test.go). A Batch job is created
// per run and named after the run (batch.JobID), so nothing on the VM names the
// deployed job the run stands in for: the launch writes that name into the job's
// environment instead (backend.DeployedJobEnv, which BR8 fills in), and the run
// is placed by it exactly as a Cloud Run run is placed by CLOUD_RUN_JOB — the
// one name that decides which provider it may clone from, which admission list
// holds it and which class of job it may be in, and the name every call this run
// makes to the credentials service reports (jobName).
//
// The tests ask the decision on values, the way the Cloud Run half does: jobName
// and jobIdentity with the memoized policy read pre-filled (identityRun), plus
// one read of the value the gateway hands the service.
//
// The last test stands the same decision the other way round, which is why it
// is here rather than in the Cloud Run half: a Cloud Run execution whose
// environment carries every variable a launcher could write into it — the Batch
// pair included — is placed by the job the platform put it in, because the
// platform's own signals tell the two backends apart and neither a FUGARO_*
// variable nor the shape of the name the run was handed does.

// batchExecution is the platform's own report of a run's execution on Cloud
// Batch: the job's own resource name, which is the whole identity there — one
// job is one run, six fields and no execution in it (backend.BatchCodec,
// design compute-backend.md §2.5).
func batchExecution(jobID string) string {
	return "projects/aurora-fp/locations/europe-west1/jobs/" + jobID
}

// batchJobID is this run's own Batch job as the platform names it: the id
// batch.JobID derives from the slug, the workflow and the run, which is a name
// no deployer and no policy ever computed (design compute-backend.md §2.4).
const batchJobID = "fugaro-acme-app-app-0123456789abcdef"

// batchTaskVars is the environment of a Batch task: BATCH_TASK_INDEX and
// BATCH_TASK_COUNT, which Batch injects into every task and nowhere else and
// which are therefore the platform's own claim that this process is one of its
// runs (the codec's On), beside FUGARO_BACKEND, which is the installation's
// record of the backend the job was launched on and is the launcher's to write.
func batchTaskVars() []string {
	return []string{"BATCH_TASK_INDEX=0", "BATCH_TASK_COUNT=1", backend.BackendEnv + "=" + backend.CloudBatch}
}

// batchRun is identityRun for a Batch job: the platform reports the Batch job's
// own name rather than an execution of a deployed job, the environment is the
// job's, and deployedJob is what the launch wrote into it as the job the run
// stands in for — "" for a launch older than the release that writes one.
func batchRun(t *testing.T, deployedJob, slug string, p *projectpolicy.Policy, found bool) *run {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main",
		Workflow: identityWorkflow, Task: "Add a feature"}
	r := identityRun(t, "", slug, spec, p, found)
	r.d.Execution = batchExecution(batchJobID)
	env := batchTaskVars()
	if deployedJob != "" {
		env = append(env, backend.DeployedJobEnv+"="+deployedJob)
	}
	r.d.Env = env
	return r
}

// TestABatchRunIsPlacedByItsDeployedJob is BR9's acceptance (F9, R6): on Batch
// the run is placed by the deployed job its launch named in its environment,
// exactly as a Cloud Run run is placed by the job the platform put it in. Both
// classes of deployed job are here, because the identity a run adopts is not
// what the name's class decides — "a shared project job" is a triple adopted
// from the published policy, "the run's own per-repository job" is the phase-1
// path with nothing adopted — and both are reached the same way: the value in
// FUGARO_DEPLOYED_JOB, matched against the same candidates CLOUD_RUN_JOB is
// matched against. A published policy is in hand in both cases, so a candidate
// set exists and the per-repository name has something to be told apart from.
//
// The last check is the other half of the acceptance: the name is what the run
// reports to the credentials service, which is the value the gateway puts in
// X-Fugaro-Run-Job on every call the run makes (internal/gateway/service.go
// sets that header from Service.Job, and the service holds it to its own
// name rule and decides nothing from it).
//
// Mutation (run, restored): place the run by its own Batch job id — the platform
// name, which is what a Cloud Run run is placed by — and every case here fails
// on a name no candidate computes (cannotPlaceJob), which is the red step F9
// records. Mutation (run, restored): read CLOUD_RUN_JOB, or the execution's own
// job, instead of the deployed-job variable: the same.
func TestABatchRunIsPlacedByItsDeployedJob(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	p := policyOf(map[string]config.JobProfile{projectpolicy.DefaultProfileName: profileOf("github")})
	shared := gcp.ProjectJobName(projectpolicy.DefaultProfileName, "github", "")
	perRepo := gcp.JobName(slug, gcp.WorkflowVariant(identityWorkflow, ""))
	for _, tc := range []struct {
		name        string
		deployed    string
		want        jobTriple
		wantProject bool
	}{
		{"a shared project job", shared,
			jobTriple{Profile: projectpolicy.DefaultProfileName, Provider: "github"}, true},
		{"the run's own per-repository job", perRepo, jobTriple{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := batchRun(t, tc.deployed, slug, p, true)
			profile, provider, account, err := r.jobIdentity(context.Background())
			if err != nil {
				t.Fatalf("jobIdentity(%s) = %v", tc.deployed, err)
			}
			if got := (jobTriple{Profile: profile, Provider: provider, Account: account}); got != tc.want {
				t.Fatalf("jobIdentity = %+v, want %+v (the triple %s was computed from)", got, tc.want, tc.deployed)
			}
			if _, project := r.projectJob(); project != tc.wantProject {
				t.Fatalf("projectJob = %v, want %v: the deployed job's class is what the run is placed as", project, tc.wantProject)
			}
			job, err := r.jobName()
			if err != nil {
				t.Fatalf("jobName = %v", err)
			}
			if job != tc.deployed {
				t.Fatalf("jobName = %q, want the deployed job %q the launch named", job, tc.deployed)
			}
		})
	}

	t.Run("the name goes to the credentials service", func(t *testing.T) {
		r := svcRun(t, config.AuthCascade, func(r *run) {
			r.d.Execution = batchExecution(batchJobID)
			r.d.Env = append(append(batchTaskVars(), backend.DeployedJobEnv+"="+shared),
				credentialsServiceURLEnv+"=https://creds.example")
		})
		r.sess = svcSession(t)
		up, err := r.gatewayUpstream(context.Background())
		if err != nil {
			t.Fatalf("a Batch run's upstream: %v", err)
		}
		if up.Service == nil || up.Service.Job != shared {
			t.Fatalf("the upstream reports the job %+v, want the deployed job %q", up.Service, shared)
		}
		if up.Service.Job == batchJobID {
			t.Error("the run reports the Batch job it is in rather than the deployed job it stands in for")
		}
	})
}

// TestABatchRunWithNoDeployedJobStops is the other side of the same acceptance:
// a Batch run is placed by the deployed-job variable or it is not placed at all.
// Two ways to have no deployed job, and both stop the run where a run in a job
// nobody can place stops today — before any git credential, at the identity
// decision — rather than falling back to the Batch job the run is in, which
// names no provider, no admission list and no class, or to the slug, which is
// the name a launcher writes (R2-5).
//
// Mutation (run, restored): return "" from jobName for a Batch run whose
// environment names no deployed job. That run is then placed as a run with no
// execution at all — the per-repository path, which adopts nothing and reads
// its admission off the project's own list — and the refusal this test asks
// for never happens.
func TestABatchRunWithNoDeployedJobStops(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	p := policyOf(map[string]config.JobProfile{projectpolicy.DefaultProfileName: profileOf("github")})
	for _, tc := range []struct {
		name     string
		deployed string
		want     []string
		// wantPlacementHalt is whether jobName itself is the halt: an
		// environment naming no deployed job has no name to be placed by,
		// while a name no candidate computes is read fine and refused when it
		// is matched (cannotPlaceJob).
		wantPlacementHalt bool
	}{
		{"the environment names no deployed job", "",
			[]string{backend.DeployedJobEnv, backend.CloudBatch, "not set"}, true},
		{"the deployed job matches no candidate", "fugaro-acme-app-other-0123456789abcdef",
			[]string{"fugaro-acme-app-other-0123456789abcdef", slug, identityWorkflow}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := batchRun(t, tc.deployed, slug, p, true)
			_, _, _, err := r.jobIdentity(context.Background())
			if err == nil {
				t.Fatal("a Batch run whose deployed job could not be placed was placed anyway")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q lacks %q", err, want)
				}
			}
			if _, project := r.projectJob(); project {
				t.Error("the run adopted a project job identity it could not place")
			}
			job, nameErr := r.jobName()
			if (nameErr != nil) != tc.wantPlacementHalt {
				t.Errorf("jobName = %q, %v; want the halt: %v", job, nameErr, tc.wantPlacementHalt)
			}
		})
	}
}

// cloudRunExecutionEnv is the variable Cloud Run injects into every task it
// runs, and the codec's own claim that a process is one of its runs
// (cloudRunCodec.On). It cannot be written into a job's definition, which is
// what makes it the one thing here a launcher cannot forge.
const cloudRunExecutionEnv = "CLOUD_RUN_EXECUTION"

// cloudRunForgedVars is everything a launcher CAN write into a Cloud Run job's
// definition, and would, reaching for the identity of a run it did not launch:
// the pair Batch injects into its own tasks, FUGARO_BACKEND, and a
// deployed-job name of its choosing. Cloud Run's own signal is in the same list
// because it is in the same environment on a real run.
func cloudRunForgedVars(deployedJob string) []string {
	return append([]string{cloudRunExecutionEnv + "=" + svcJob + "-aaaaa"},
		append(batchTaskVars(), backend.DeployedJobEnv+"="+deployedJob)...)
}

// TestACloudRunIsPlacedByItsOwnExecution is why jobName reads the platform's
// own signals and nothing else, and it is a test of a halt rather than of a
// fallback. Two ways a launcher reaching for the identity of a run it did not
// launch gets neither: the Batch pair it wrote into the Cloud Run job's
// definition does not move the run onto the Batch path, because Cloud Run's own
// CLOUD_RUN_EXECUTION is asked first (finding 4, 12a item 1); and a slash it
// wrote into FUGARO_GCP_PROJECT or FUGARO_REGION — which is what makes the name
// the run was handed stop parsing as a Cloud Run execution — does not either,
// because the shape of that name is the launcher's too: the run then stops on
// the name it cannot read back, exactly as it did before there was a Batch
// path. Both halves of the placement are here, for the same reason the Batch
// test asks for both: the triple the run adopts (nothing) and the name it
// reports to the credentials service.
//
// Mutation (run, restored): read FUGARO_BACKEND, or the shape of the name the
// run was handed, instead of the platform's signals. The run is then placed by
// FUGARO_DEPLOYED_JOB, adopts the shared project job's (profile, provider)
// triple and reports that name to the service, while the container it was
// handed is the per-repository job's: its provider, its admission list, its
// clone URL and its account door all come from a name a launcher wrote.
func TestACloudRunIsPlacedByItsOwnExecution(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	p := policyOf(map[string]config.JobProfile{projectpolicy.DefaultProfileName: profileOf("github")})
	shared := gcp.ProjectJobName(projectpolicy.DefaultProfileName, "github", "")
	perRepo := gcp.JobName(slug, gcp.WorkflowVariant(identityWorkflow, ""))

	t.Run("the run stays in the job the platform reported", func(t *testing.T) {
		r := identityRun(t, perRepo, slug, nil, p, true)
		r.d.Env = cloudRunForgedVars(shared)
		profile, provider, account, err := r.jobIdentity(context.Background())
		if err != nil {
			t.Fatalf("jobIdentity: %v", err)
		}
		if got := (jobTriple{Profile: profile, Provider: provider, Account: account}); got != (jobTriple{}) {
			t.Fatalf("jobIdentity = %+v, want nothing adopted: the platform reported the per-repository job %s, and %s names a job the run is not in", got, perRepo, backend.DeployedJobEnv)
		}
		if _, project := r.projectJob(); project {
			t.Errorf("the run was placed in the shared project job %s its environment named, not in the job the platform reported", shared)
		}
		job, err := r.jobName()
		if err != nil {
			t.Fatalf("jobName: %v", err)
		}
		if job != perRepo {
			t.Fatalf("jobName = %q, want the Cloud Run job %q the platform reported", job, perRepo)
		}
	})

	t.Run("a name the launcher broke is not read as a Batch job", func(t *testing.T) {
		r := identityRun(t, perRepo, slug, nil, p, true)
		// FUGARO_GCP_PROJECT as a launcher can write it, and the codec composes
		// the name the run is handed out of it: the result is no Cloud Run
		// execution at all, which is the shape the Batch branch must not be
		// reached by.
		r.d.Execution = "projects/a/locations/b/jobs/c/executions/d" + strings.TrimPrefix(r.d.Execution, "projects/aurora-fp")
		r.d.Env = cloudRunForgedVars(shared)
		_, _, _, err := r.jobIdentity(context.Background())
		if err == nil {
			t.Fatal("a Cloud Run run whose name the launcher broke was placed anyway")
		}
		if !strings.Contains(err.Error(), "cannot be read back into a job name") {
			t.Errorf("the refusal %q is not the one for a name that is no execution: the run went somewhere else with it", err)
		}
		if _, project := r.projectJob(); project {
			t.Error("the run adopted a project job identity from a name the platform did not report")
		}
		job, nameErr := r.jobName()
		if nameErr == nil {
			t.Errorf("jobName = %q, want the halt on the broken name", job)
		}
	})

	t.Run("the name goes to the credentials service", func(t *testing.T) {
		r := svcRun(t, config.AuthCascade, func(r *run) {
			r.d.Env = append(cloudRunForgedVars(shared), credentialsServiceURLEnv+"=https://creds.example")
		})
		r.sess = svcSession(t)
		up, err := r.gatewayUpstream(context.Background())
		if err != nil {
			t.Fatalf("a Cloud Run run's upstream: %v", err)
		}
		if up.Service == nil || up.Service.Job != svcJob {
			t.Fatalf("the upstream reports the job %+v, want the job the platform reported, %q", up.Service, svcJob)
		}
		if up.Service.Job == shared {
			t.Error("the run reports the deployed-job variable rather than the job it is in")
		}
	})
}
