package infra

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// batchEnvInputs is a repository whose template exercises every branch of the
// job environment a deployed job can carry: the installation's identity, a
// Vertex auth (both Vertex names), a GitHub App ID, a compute-price override,
// every budget variable including the backend's address and web key, and the
// redaction list of the secrets the job mounts.
func batchEnvInputs(t *testing.T) Inputs {
	t.Helper()
	in := webappInputs(t)
	in.LC = parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
compute_prices: { us-east5: { vcpu_second_usd: 0.00002, gib_second_usd: 0.0000025 } }
budget: { mode: enforce, per_run_usd: 5, max_run_tokens: 2000000, allowed_models: [claude-sonnet-5], rtdb_url: "https://proj-1234-default-rtdb.firebaseio.com", firebase_api_key: the-restricted-web-key, unreachable_grace: 90s }
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web, api], github_app_id: "123456" }
`)
	return in
}

// batchTestTimeouts is the config/policy read the Batch backend asks for a
// workflow's total time; every workflow of the test installation answers with
// the same bound.
type batchTestTimeouts struct{}

func (batchTestTimeouts) TaskTimeout(string, string) (time.Duration, error) {
	return 30 * time.Minute, nil
}
func (batchTestTimeouts) LongestTaskTimeout() (time.Duration, error) { return time.Hour, nil }

// batchJobVars is the variables map of the job the fake decoded from one
// launch's jobs.create request.
func batchJobVars(t *testing.T, job map[string]any) map[string]any {
	t.Helper()
	groups, ok := job["taskGroups"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("the created job's taskGroups = %v, want one group", job["taskGroups"])
	}
	group, _ := groups[0].(map[string]any)
	task, _ := group["taskSpec"].(map[string]any)
	env, _ := task["environment"].(map[string]any)
	vars, ok := env["variables"].(map[string]any)
	if !ok {
		t.Fatalf("the created job carries no plain variables: %v", env)
	}
	return vars
}

// TestABatchJobCarriesEveryVariableADeployedJobDoes is F8's pin, and BR8's: a
// Batch job is created per run from nothing, where a Cloud Run execution's
// override is merged into the deployed job's own environment, so the two lists
// — the names a deployed job carries (JobEnvNames, which the launcher fills
// batch.Options.JobEnv from) and the variables the Batch job is sent — are
// held to the template together.
//
// The job is built from JobEnvNames and the template and nothing else: a
// variable the template gains and JobEnvNames does not carry is a variable the
// launcher would not pass, so the Batch job would not carry it either, and
// this test fails on it rather than a Batch run discovering it with no bucket
// to write to (BR8's red step).
func TestABatchJobCarriesEveryVariableADeployedJobDoes(t *testing.T) {
	ws, err := Workflow(batchEnvInputs(t), "web")
	if err != nil {
		t.Fatal(err)
	}
	// The installation's values as the launcher hands them in: one entry per
	// name a deployed job carries, with the template's own value wherever the
	// template writes one. A name the template does not write still travels,
	// because which of the template's branches a deployment took is the
	// installation's business and not the backend's.
	jobEnv := make(map[string]string, len(JobEnvNames))
	for _, name := range JobEnvNames {
		value, ok := ws.Env[name]
		if !ok {
			value = "the installation's " + name
		}
		jobEnv[name] = value
	}

	// Every variable the template's own environment carries is a name this
	// list has: the two are one fact, and a variable added to the template
	// without being added here is the one nobody would pass to a Batch job.
	for _, name := range slices.Sorted(maps.Keys(ws.Env)) {
		if !slices.Contains(JobEnvNames, name) {
			t.Errorf("the template carries %s and JobEnvNames does not: a Batch job would be launched without it", name)
		}
	}

	bf, lf := gcpfake.NewBatch(t), gcpfake.NewLogging(t)
	b, err := batch.New(context.Background(), batch.Options{
		GCPProject:     "proj-1234",
		Region:         "us-east5",
		FugaroProject:  "aurora",
		JobEnv:         jobEnv,
		Image:          "us-east5-docker.pkg.dev/proj-1234/fugaro-webapp/web:1",
		ServiceAccount: "fugaro-job-webapp@proj-1234.iam.gserviceaccount.com",
		MachineType:    "e2-standard-4",
		BootImage:      "projects/proj-1234/global/images/fugaro-base-0-7-5",
		Network:        "projects/proj-1234/global/networks/default",
		Subnetwork:     "projects/proj-1234/regions/us-east5/subnetworks/default",
		Timeouts:       batchTestTimeouts{},
		Endpoints:      batch.Endpoints{Batch: bf.URL + "/", Logging: lf.URL + "/", NoAuth: true},
	})
	if err != nil {
		t.Fatalf("batch.New: %v", err)
	}
	if _, err := b.Launch(context.Background(), backend.LaunchSpec{
		Repo:     backend.RepoRef{Repo: "acme/webapp", Slug: "acme-webapp"},
		Workflow: "web",
		RunID:    "20261121-000000-abcd",
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	creates := bf.Creates()
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	vars := batchJobVars(t, creates[0].Job)

	// The job carries every name the launcher passes, and every value the
	// template carries byte for byte — except the one name that is about this
	// backend rather than about the installation: a run on Batch must read
	// cloud-batch there even though the deployed job says cloud-run.
	for _, name := range JobEnvNames {
		if _, ok := vars[name]; !ok {
			t.Errorf("the Batch job carries no %s, and a deployed job does: %v", name, vars)
		}
	}
	for name, want := range ws.Env {
		if name == BackendEnv {
			if got := vars[name]; got != backend.CloudBatch {
				t.Errorf("the Batch job's %s = %v, want %s: the deployed job's own value must not decide which backend the run is on", name, got, backend.CloudBatch)
			}
			continue
		}
		if got := vars[name]; got != want {
			t.Errorf("the Batch job's %s = %v, want %q: the deployed job the run stands in for carries it", name, got, want)
		}
	}
	// And the entries the backend writes itself name the run and the job it
	// stands in for, and are never taken from the installation's values.
	for name, want := range map[string]string{
		"FUGARO_RUN":           "acme-webapp/20261121-000000-abcd",
		backend.BatchJobEnv:    creates[0].Name,
		backend.DeployedJobEnv: gcp.JobName("acme-webapp", "web"),
	} {
		if got := vars[name]; got != want {
			t.Errorf("the Batch job's %s = %v, want %q", name, got, want)
		}
	}
}
