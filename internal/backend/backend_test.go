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
	// The six-field name is a Batch job, and on Batch the job is the run, so
	// the seam's Name slot stays empty (design compute-backend.md §2.5).
	job, ok := ParseExecution("projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web")
	if !ok || job.Job != "fugaro-acme-app-web" || job.Name != "" || job.Region != "us-east5" {
		t.Fatalf("a Batch job: %+v, %v", job, ok)
	}
	if got := BatchCodec().Format(job); got != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web" {
		t.Fatalf("BatchCodec().Format = %q", got)
	}
	for _, bad := range []string{
		"",
		"fugaro-acme-app-web-x7k2p",
		"projects/p/locations/r/jobs", // five fields: neither shape
		"projects/p/locations/r/jobs/j/executions",     // seven
		"projects/p/locations/r/jobs/j/executions/e/f", // nine
		"projects//locations/r/jobs/j/executions/e",    // an empty field
		"projects/p/locations/r/jobs//executions/e",    // an empty field
		"projects/p/locations/r/jobs/",                 // an empty job
	} {
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

// ValidateRun is the job-binding half of the codec's name checks, in the
// backend's region and spelled [a-z0-9-]. The CLI's checkExecution does not
// use it -- it binds a name to a run by FUGARO_RUN, because a shared job no
// longer says which run an execution is (Codec's own doc) -- so this pins
// the codec's behavior on its own.
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

// The Batch codec is the second one the seam carries (design
// compute-backend.md §2.5, §8.1 Task 2): on Batch one job is one task group
// and one run, so the run's identity is the job's own resource name, and the
// platform's signal is BATCH_TASK_INDEX/BATCH_TASK_COUNT.
func TestBatchCodecNames(t *testing.T) {
	const name = "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web-0123456789ab"
	c := BatchCodec()
	if c.Backend() != CloudBatch {
		t.Fatalf("Backend = %q", c.Backend())
	}
	id, ok := c.Parse(name)
	if !ok || id.GCPProject != "my-proj" || id.Region != "us-east5" || id.Job != "fugaro-acme-app-web-0123456789ab" || id.Name != "" {
		t.Fatalf("Parse = %+v, %v", id, ok)
	}
	if got := c.Format(id); got != name {
		t.Fatalf("Format = %q, want %q", got, name)
	}
	// Format is the job's name, not ExecID.String: that one spells Cloud
	// Run's eight fields and would invent an execution.
	if got := id.String(); got == name {
		t.Fatal("the seam's String must stay Cloud Run's shape; Batch names come from the codec")
	}
	// The project number the API may answer with names the same run.
	num, ok := c.Parse("projects/123456789/locations/us-east5/jobs/fugaro-acme-app-web-0123456789ab")
	if !ok || num.Key() != id.Key() || num.GCPProject != "123456789" {
		t.Fatalf("a project number changed the identity: %+v, %v", num, ok)
	}
	// The two codecs' name shapes cannot be mistaken for each other.
	if _, ok := c.Parse("projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p"); ok {
		t.Fatal("the Batch codec parsed a Cloud Run execution name")
	}
	for _, bad := range []string{"", "fugaro-acme-app-web-0123456789ab", "projects/my-proj/locations/us-east5/jobs", "projects/my-proj/locations/us-east5/jobs/"} {
		if _, ok := c.Parse(bad); ok {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

// ParseInRegion is what the backend's canonical uses to rewrite a name it
// already knows, and it is the check that keeps a bucket object a run wrote
// from pointing the operator's cancel or logs at another job.
func TestBatchCodecParseInRegion(t *testing.T) {
	const job = "fugaro-acme-app-web-0123456789ab"
	in := "projects/my-proj/locations/us-east5/jobs/" + job
	c := BatchCodec()
	if id, err := c.ParseInRegion(in, "us-east5"); err != nil || id.Job != job || id.GCPProject != "my-proj" {
		t.Fatalf("ParseInRegion = %+v, %v", id, err)
	}
	var wr *WrongRegionError
	if _, err := c.ParseInRegion(in, "europe-west1"); !errors.As(err, &wr) || wr.Region != "us-east5" {
		t.Fatalf("another region: %v", err)
	}
	for _, bad := range []string{
		"",
		"projects/my-proj/locations/us-east5/jobs/" + job + "/executions/" + job + "-x7k2p", // a Cloud Run execution
		"projects/my-proj/locations/us-east5/jobs/Fugaro-Acme-App",                          // not [a-z0-9-]
		"projects/my-proj/locations/us-east5/jobs/../../etc/passwd",
	} {
		if _, err := c.ParseInRegion(bad, "us-east5"); err == nil {
			t.Errorf("ParseInRegion(%q) accepted", bad)
		}
	}
}

// ValidateRun binds a Batch name to the run's own job. On Batch the job id
// is derived per run (design §2.4), so the caller passes the id it launched;
// an empty one is refused rather than read as "skip the binding".
func TestBatchCodecValidateRun(t *testing.T) {
	const job = "fugaro-acme-app-web-0123456789ab"
	in := "projects/my-proj/locations/us-east5/jobs/" + job
	c := BatchCodec()
	if id, err := c.ValidateRun(in, "us-east5", job); err != nil || id.Job != job {
		t.Fatalf("ValidateRun = %+v, %v", id, err)
	}
	if id, err := c.ValidateRun(in, "us-east5", ""); err == nil || id.Job != "" {
		t.Fatalf("an unbound name was accepted: %+v, %v", id, err)
	}
	other := "projects/my-proj/locations/us-east5/jobs/fugaro-other-web-0123456789ab"
	if _, err := c.ValidateRun(other, "us-east5", job); err == nil || !strings.Contains(err.Error(), "not the run's job") {
		t.Fatalf("another job's name: %v", err)
	}
	var wr *WrongRegionError
	if _, err := c.ValidateRun(in, "europe-west1", job); !errors.As(err, &wr) || wr.Want != "europe-west1" {
		t.Fatalf("another region: %v", err)
	}
}

// On is the platform's own signal, and it needs both halves of it.
func TestBatchCodecOn(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c := BatchCodec()
	if !c.On(get(map[string]string{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1"})) {
		t.Error("both Batch task markers must claim the process")
	}
	for _, env := range []map[string]string{
		nil,
		{"BATCH_TASK_INDEX": "0"},
		{"BATCH_TASK_COUNT": "1"},
		// A job's own variables are the launcher's record, never a platform
		// signal (design §2.5).
		{"FUGARO_BACKEND": CloudBatch, BatchJobEnv: "projects/p/locations/r/jobs/j", "FUGARO_REGION": "r"},
	} {
		if c.On(get(env)) {
			t.Errorf("%v claimed the process", env)
		}
	}
}

// FromEnv is the name the job definition carries, checked against the region
// and the project the same definition carries: none of the three is evidence,
// but a job whose environment was edited must not be able to name another
// region's or another project's job.
func TestBatchCodecFromEnv(t *testing.T) {
	const name = "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web-0123456789ab"
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c := BatchCodec()
	env := map[string]string{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", BatchJobEnv: name, "FUGARO_REGION": "us-east5", "FUGARO_GCP_PROJECT": "my-proj"}
	got, err := c.FromEnv(get(env))
	if err != nil || got != name {
		t.Fatalf("FromEnv = %q, %v", got, err)
	}
	// Outside a Batch task the codec says nothing, whatever the job's own
	// variables say.
	if got, err := c.FromEnv(get(map[string]string{BatchJobEnv: name, "FUGARO_REGION": "us-east5"})); got != "" || err != nil {
		t.Fatalf("outside Batch = %q, %v", got, err)
	}
	// On Batch without the name the job definition must carry, or with one
	// naming another region or another project, the process cannot spell
	// itself: the job's environment names the job the process is, so a
	// FUGARO_GCP_PROJECT that disagrees with the name -- including an empty
	// one -- is a job environment that was edited to name someone else's job.
	for _, bad := range []map[string]string{
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", "FUGARO_REGION": "us-east5"},
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", BatchJobEnv: name},
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", BatchJobEnv: name, "FUGARO_REGION": "europe-west1", "FUGARO_GCP_PROJECT": "my-proj"},
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", BatchJobEnv: name, "FUGARO_REGION": "us-east5", "FUGARO_GCP_PROJECT": "other-proj"},
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", BatchJobEnv: name, "FUGARO_REGION": "us-east5"},
		{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1", BatchJobEnv: "not-a-name", "FUGARO_REGION": "us-east5", "FUGARO_GCP_PROJECT": "my-proj"},
	} {
		if got, err := c.FromEnv(get(bad)); err == nil {
			t.Errorf("%v named itself %q", bad, got)
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

// TestExecutionFromEnvRefusesAnEmptyJobName: an empty CLOUD_RUN_JOB on Cloud
// Run is refused here, before anything asks which job the run is in (plan task
// 3.3, "an empty CLOUD_RUN_JOB on Cloud Run does not land here either"). The
// distinction is the runner's: "" from ExecutionFromEnv means "this process is
// not on Cloud Run" — a local invocation, a docker run — and the identity step
// is skipped for it (R2-9), so an empty name that meant "on Cloud Run but
// nobody set the variable" would be read as exactly the case the step must not
// run for. It is refused at the source instead.
//
// Mutation (run, restored): drop id.Job from the check below (or let it
// through as a name). The run proceeds with no job identity at all and the
// binding is skipped, which is the fail-open this refusal closes.
func TestExecutionFromEnvRefusesAnEmptyJobName(t *testing.T) {
	c := CloudRunCodec()
	env := map[string]string{"CLOUD_RUN_EXECUTION": "fugaro-acme-app-web-x7k2p", "FUGARO_GCP_PROJECT": "my-proj", "FUGARO_REGION": "us-east5"}
	if name, err := c.FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatalf("an empty CLOUD_RUN_JOB named the execution %q", name)
	} else if !strings.Contains(err.Error(), "CLOUD_RUN_JOB") {
		t.Errorf("the refusal does not name CLOUD_RUN_JOB: %v", err)
	}
}

// TestJobOfNamesTheJobOrFailsClosed is the runner's one reading of the job it
// is in (plan task 3.3 step 1): the job out of an execution name this build's
// codec parses, and an error for anything else. The runner halts on the error
// rather than treating it as "no Cloud Run execution", because the two mean
// different things: the first is a process whose platform report cannot be
// read, the second is a process that is not on the platform at all.
func TestJobOfNamesTheJobOrFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		execution string
		job       string
	}{
		{"projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p", "fugaro-acme-app-web"},
		{"projects/123456789/locations/europe-west1/jobs/fugaro-p-standard-github-abc123/executions/x", "fugaro-p-standard-github-abc123"},
	} {
		job, err := JobOf(tc.execution)
		if err != nil || job != tc.job {
			t.Errorf("JobOf(%s) = %q, %v; want %q", tc.execution, job, err, tc.job)
		}
	}
	// A Batch job's resource name is its whole identity (the job is the run,
	// codec_batch.go), so it names a job too — that is not a leniency this
	// function added.
	for _, tc := range []struct {
		execution, job string
	}{
		{"projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web", "fugaro-acme-app-web"},
	} {
		if job, err := JobOf(tc.execution); err != nil || job != tc.job {
			t.Errorf("JobOf(%s) = %q, %v; want %q", tc.execution, job, err, tc.job)
		}
	}
	for _, bad := range []string{
		"",
		"fugaro-acme-app-web",
		"projects/proj-1234/locations/us-east5/jobs//executions/x",
		"projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/",
	} {
		if job, err := JobOf(bad); err == nil {
			t.Errorf("JobOf(%q) = %q, want an error: a name no codec parses names no job", bad, job)
		}
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
	// A Batch task's own markers (design §2.5): Batch injects both into every
	// task, and no other platform does, so they claim the process for the
	// Batch codec -- which then needs the job's own name to spell the run.
	onBatch := DetectPlatform(get(map[string]string{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1"}))
	if onBatch.Backend() != CloudBatch || !onBatch.InCloud() || onBatch.Local() {
		t.Fatalf("a Batch task: %+v", onBatch)
	}
	// One marker alone is not the signal: a single stray variable must not
	// claim a process for a backend it is not on.
	if p := DetectPlatform(get(map[string]string{"BATCH_TASK_INDEX": "0"})); p.Backend() != "" || !p.InCloud() {
		t.Fatalf("BATCH_TASK_INDEX alone: %+v", p)
	}
	// Neither the job name nor a Fugaro variable is a platform signal. The
	// answer is still "in the cloud", because everything that is not the
	// local invocation is.
	for _, env := range []map[string]string{
		nil,
		{"CLOUD_RUN_JOB": "j"},
		{"FUGARO_BACKEND": CloudRun, "FUGARO_GCP_PROJECT": "p", "FUGARO_REGION": "us-east5", "FUGARO_RUN": "acme-app/20260926-221530-abcd"},
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
