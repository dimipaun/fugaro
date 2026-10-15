package backend

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestMemoryGiB(t *testing.T) {
	for in, want := range map[string]float64{"512Mi": 0.5, "8Gi": 8, "1024Mi": 1, "32Gi": 32} {
		got, err := MemoryGiB(in)
		if err != nil || got != want {
			t.Errorf("MemoryGiB(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "8G", "8GB", "Gi", "-1Gi", "0Mi"} {
		if _, err := MemoryGiB(bad); err == nil {
			t.Errorf("MemoryGiB(%q) accepted", bad)
		}
	}
}

func TestComputeUSD(t *testing.T) {
	p := Prices{VCPUSecondUSD: 0.000018, GiBSecondUSD: 0.000002}
	// 4 vCPU, 8 GiB for an hour: 3600 × (4×0.000018 + 8×0.000002) = $0.3168.
	if got := p.ComputeUSD(4, 8, time.Hour); math.Abs(got-0.3168) > 1e-9 {
		t.Fatalf("ComputeUSD = %v", got)
	}
	if got := p.ComputeUSD(4, 8, -time.Second); got != 0 {
		t.Fatalf("a negative duration costs %v", got)
	}
}

func TestParseExecution(t *testing.T) {
	id, ok := ParseExecution("projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p")
	if !ok || id.Job != "fugaro-acme-app-web" || id.Name != "fugaro-acme-app-web-x7k2p" || id.Region != "us-east5" {
		t.Fatalf("id = %+v, %v", id, ok)
	}
	if id.String() != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p" {
		t.Fatalf("String = %s", id.String())
	}
	// The API may answer with the project number: still the same execution.
	if !SameExecution(id.String(), "projects/123456789/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p") {
		t.Fatal("project ID vs number must not change identity")
	}
	for _, bad := range []string{"", "fugaro-acme-app-web-x7k2p", "projects/p/locations/r/jobs/j", "projects//locations/r/jobs/j/executions/e"} {
		if _, ok := ParseExecution(bad); ok {
			t.Errorf("ParseExecution(%q) accepted", bad)
		}
	}
}

// The Cloud Run codec spells an execution exactly as the package always
// has: the stored names in result.json and launch.json, and every name
// CompareExecution is read against, must not change (design
// compute-backend.md §8.1, Task 1).
func TestCloudRunCodecNames(t *testing.T) {
	const name = "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p"
	c := CloudRunCodec()
	if c.Backend() != CloudRun {
		t.Fatalf("Backend = %q", c.Backend())
	}
	id, ok := c.Parse(name)
	if !ok {
		t.Fatalf("Parse(%q) failed", name)
	}
	if got := c.Format(id); got != name {
		t.Fatalf("Format = %q, want %q", got, name)
	}
	// A project number the API sends names the same execution: the project
	// is not part of the key, which is what lets the backend rewrite it to
	// its own project ID (gcp's canonical).
	num, ok := c.Parse("projects/123456789/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p")
	if !ok || num.Key() != id.Key() || num.GCPProject != "123456789" {
		t.Fatalf("a project number changed the identity: %+v, %v", num, ok)
	}
}

// ValidateRun is the job-binding check the CLI's checkExecution relies on:
// a name the run's own service account wrote must be an execution of the
// run's own job, in the backend's region, and spelled [a-z0-9-].
func TestCloudRunCodecValidateRun(t *testing.T) {
	const job = "fugaro-acme-app-web"
	in := "projects/my-proj/locations/us-east5/jobs/" + job + "/executions/" + job + "-x7k2p"
	c := CloudRunCodec()
	if id, err := c.ValidateRun(in, "us-east5", job); err != nil || id.Job != job {
		t.Fatalf("ValidateRun = %+v, %v", id, err)
	}
	// An empty job is refused, not read as "skip the binding": a caller
	// that lost the run's job must not follow an unbound name.
	if id, err := c.ValidateRun(in, "us-east5", ""); err == nil || id.Job != "" {
		t.Fatalf("an unbound name was accepted: %+v, %v", id, err)
	}
	other := "projects/my-proj/locations/us-east5/jobs/fugaro-other-web/executions/fugaro-other-web-x7k2p"
	if _, err := c.ValidateRun(other, "us-east5", job); err == nil || !strings.Contains(err.Error(), "belongs to job") {
		t.Fatalf("another job's execution: %v", err)
	}
	var wr *WrongRegionError
	if _, err := c.ValidateRun(in, "europe-west1", job); !errors.As(err, &wr) || wr.Region != "us-east5" || wr.Want != "europe-west1" {
		t.Fatalf("another region: %v", err)
	}
	for _, bad := range []string{
		"projects/my-proj/locations/us-east5/jobs/" + job,                                   // a Batch-shaped name, not an execution
		"projects/my-proj/locations/us-east5/jobs/" + job + "/executions/" + job + "-X7K2P", // not [a-z0-9-]
		"projects/my-proj/locations/us-east5/jobs/" + job + "/executions/../../etc/passwd",
	} {
		if _, err := c.ValidateRun(bad, "us-east5", job); err == nil {
			t.Errorf("ValidateRun(%q) accepted", bad)
		}
	}
}

// ParseInRegion is the same check without the job binding: it is what
// gcp's canonical uses to rewrite a name the backend already knows.
func TestCloudRunCodecParseInRegion(t *testing.T) {
	const job = "fugaro-acme-app-web"
	in := "projects/my-proj/locations/us-east5/jobs/" + job + "/executions/" + job + "-x7k2p"
	c := CloudRunCodec()
	if id, err := c.ParseInRegion(in, "us-east5"); err != nil || id.Job != job || id.GCPProject != "my-proj" {
		t.Fatalf("ParseInRegion = %+v, %v", id, err)
	}
	var wr *WrongRegionError
	if _, err := c.ParseInRegion(in, "europe-west1"); !errors.As(err, &wr) {
		t.Fatalf("another region: %v", err)
	}
	for _, bad := range []string{
		"",
		"projects/my-proj/locations/us-east5/jobs/" + job, // six fields: a Batch job, not an execution
		"projects/my-proj/locations/us-east5/jobs/" + job + "/executions/../../etc/passwd",
	} {
		if _, err := c.ParseInRegion(bad, "us-east5"); err == nil {
			t.Errorf("ParseInRegion(%q) accepted", bad)
		}
	}
}

func TestExecutionFromEnvReadsGCPProject(t *testing.T) {
	c := CloudRunCodec()
	env := map[string]string{"CLOUD_RUN_EXECUTION": "fugaro-acme-app-web-x7k2p", "CLOUD_RUN_JOB": "fugaro-acme-app-web", "FUGARO_GCP_PROJECT": "my-proj", "FUGARO_PROJECT": "aurora", "FUGARO_REGION": "us-east5"}
	name, err := c.FromEnv(func(k string) string { return env[k] })
	if err != nil || name != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p" {
		t.Fatalf("name = %q, %v", name, err)
	}
	// The project name is never the GCP ID, and the GCP ID is read only
	// from FUGARO_GCP_PROJECT.
	delete(env, "FUGARO_GCP_PROJECT")
	if _, err := c.FromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "FUGARO_GCP_PROJECT") {
		t.Fatalf("a missing FUGARO_GCP_PROJECT on Cloud Run: %v", err)
	}
	if name, err := c.FromEnv(func(string) string { return "" }); name != "" || err != nil {
		t.Fatalf("outside Cloud Run = %q, %v", name, err)
	}
}

// The platform signal is the platform's own, and the "in the cloud" answer
// fails closed (design compute-backend.md §2.5, C12).
func TestPlatformSignalAndFailClosed(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	// Cloud Run claims a process from CLOUD_RUN_EXECUTION alone.
	onRun := DetectPlatform(get(map[string]string{"CLOUD_RUN_EXECUTION": "x"}))
	if onRun.Backend() != CloudRun || !onRun.InCloud() || onRun.Local() {
		t.Fatalf("a Cloud Run execution: %+v", onRun)
	}
	// Neither the job name nor a Fugaro variable is a platform signal. The
	// answer is still "in the cloud", because everything that is not the
	// local invocation is.
	for _, env := range []map[string]string{
		nil,
		{"CLOUD_RUN_JOB": "j"},
		{"FUGARO_BACKEND": CloudRun, "FUGARO_GCP_PROJECT": "p", "FUGARO_REGION": "us-east5", "FUGARO_RUN": "acme-app/20260926-221530-abcd"},
		// A Batch task's own markers (design §2.5): the codec that answers
		// them is Task 2's, so today the process is unclaimed -- and still
		// in the cloud.
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1"},
	} {
		p := DetectPlatform(get(env))
		if p.Backend() != "" || !p.InCloud() || p.Local() {
			t.Errorf("%v: %+v", env, p)
		}
		if name, err := p.ExecutionFromEnv(get(env)); name != "" || err != nil {
			t.Errorf("%v: an unclaimed process named itself %q, %v", env, name, err)
		}
	}
}

// Local mode is the invocation's answer, and it is the only thing that is
// not in the cloud.
func TestLocalPlatform(t *testing.T) {
	p := LocalPlatform()
	if !p.Local() || p.InCloud() || p.Backend() != Local {
		t.Fatalf("a local invocation: %+v", p)
	}
	// No environment can make a local invocation claim a platform, and a
	// local run has no execution name whatever the job's variables say.
	env := map[string]string{"CLOUD_RUN_EXECUTION": "x", "FUGARO_BACKEND": CloudRun, "BATCH_TASK_INDEX": "0"}
	if name, err := p.ExecutionFromEnv(func(k string) string { return env[k] }); name != "" || err != nil {
		t.Fatalf("a local run named itself %q, %v", name, err)
	}
	if _, err := p.Codec().ValidateRun("projects/p/locations/r/jobs/j/executions/e", "r", "j"); err == nil {
		t.Fatal("the local codec accepted a run name")
	}
	if _, err := p.Codec().ParseInRegion("projects/p/locations/r/jobs/j/executions/e", "r"); err == nil {
		t.Fatal("the local codec accepted a name in region")
	}
	if _, ok := p.Codec().Parse("projects/p/locations/r/jobs/j/executions/e"); ok {
		t.Fatal("the local codec parsed a Cloud Run name")
	}
	if p.Codec().On(func(string) string { return "anything" }) {
		t.Fatal("the local codec claimed a process")
	}
}

// OnPlatform is the cosmetic question (the skill and update nudges): it
// answers from the platform's own signal, and does not fail closed, so a
// local `fugaro ls` keeps nudging.
func TestOnPlatform(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if !OnPlatform(get(map[string]string{"CLOUD_RUN_EXECUTION": "x"})) {
		t.Error("CLOUD_RUN_EXECUTION is the Cloud Run signal")
	}
	if OnPlatform(get(map[string]string{"CLOUD_RUN_JOB": "j", "FUGARO_BACKEND": CloudRun, "FUGARO_GCP_PROJECT": "p"})) {
		t.Error("only a platform's own signal counts")
	}
	if OnPlatform(get(nil)) {
		t.Error("an empty environment is not a platform's")
	}
}

func TestExecutionBilled(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	running := Execution{State: StateRunning, Started: t0}
	if got := running.Billed(t0.Add(90 * time.Second)); got != 90*time.Second {
		t.Fatalf("running Billed = %v", got)
	}
	done := Execution{State: StateSucceeded, Started: t0, Completed: t0.Add(time.Minute)}
	if got := done.Billed(t0.Add(time.Hour)); got != time.Minute {
		t.Fatalf("done Billed = %v", got)
	}
	if got := (Execution{State: StatePending}).Billed(t0); got != 0 {
		t.Fatalf("pending Billed = %v", got)
	}
	for s, terminal := range map[State]bool{StatePending: false, StateRunning: false, StateSucceeded: true, StateFailed: true, StateCancelled: true} {
		if s.Terminal() != terminal {
			t.Errorf("%s.Terminal() = %v", s, !terminal)
		}
	}
}
