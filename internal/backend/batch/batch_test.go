package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	batch "google.golang.org/api/batch/v1"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	testProject = "fake-project"
	testRegion  = "fake-region"
	testSA      = "fugaro-job-acme-app@fake-project.iam.gserviceaccount.com"
	testImage   = "fake-region-docker.pkg.dev/fake-project/fugaro-acme-app/acme-app:1"
	// testSecret is a Secret Manager secret *name*, never a value: the job
	// definition carries names only.
	testSecret = "projects/fake-project/secrets/fugaro-acme-app-anthropic-api-key"
)

// testTimeouts is the config/policy read the backend asks for the workflow
// timeouts (design §2.3). Task 8 wires the real one.
type testTimeouts struct {
	task, longest time.Duration
	err           error
}

func (t testTimeouts) TaskTimeout(slug, workflow string) (time.Duration, error) {
	if t.err != nil {
		return 0, t.err
	}
	return t.task, nil
}

func (t testTimeouts) LongestTaskTimeout() (time.Duration, error) { return t.longest, t.err }

// testOptions is the configuration every test starts from: one installation
// on the fakes, with a dedicated service account and no secret values.
func testOptions(t *testing.T) Options {
	t.Helper()
	bf, lf := gcpfake.NewBatch(t), gcpfake.NewLogging(t)
	return Options{
		GCPProject:     testProject,
		Region:         testRegion,
		FugaroProject:  "acme",
		Image:          testImage,
		ServiceAccount: testSA,
		Timeouts:       testTimeouts{task: 30 * time.Minute, longest: time.Hour},
		Endpoints:      Endpoints{Batch: bf.URL + "/", Logging: lf.URL + "/", NoAuth: true},
	}
}

func newTestBackend(t *testing.T, tweak func(*Options)) (*Backend, *gcpfake.Batch, *gcpfake.Logging, *[]string) {
	t.Helper()
	bf, lf := gcpfake.NewBatch(t), gcpfake.NewLogging(t)
	o := testOptions(t)
	// The fakes the caller inspects are the ones the backend talks to.
	o.Endpoints = Endpoints{Batch: bf.URL + "/", Logging: lf.URL + "/", NoAuth: true}
	var warns []string
	o.Warn = func(m string) { warns = append(warns, m) }
	if tweak != nil {
		tweak(&o)
	}
	be, err := New(context.Background(), o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return be, bf, lf, &warns
}

// spec is the run every test launches, the conformance suite's own.
func spec() backend.LaunchSpec {
	return backend.LaunchSpec{
		Repo:     backend.RepoRef{Repo: backendtest.Repo, Slug: backendtest.Slug},
		Workflow: backendtest.Workflow,
		RunID:    "20260927-100000-abcd",
	}
}

// TestBatchBackendPassesConformance runs the shared suite against this
// backend on the Cloud Batch and Logging fakes: "a contributor's pull
// request passes backendtest" (design m11-setup-and-skills.md §7). The cancel
// case is the one that makes jobs.cancel rather than jobs.delete a tested
// requirement (design compute-backend.md §1.4): it reads the job back after
// cancelling it and requires a terminal state, which a delete could not
// answer.
func TestBatchBackendPassesConformance(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) backend.Backend {
		b, _, _, _ := newTestBackend(t, nil)
		return b
	})
}

// TestCancelIsJobsCancelNotDelete pins C8: Cancel leaves the record behind,
// so fugaro ls, diagnose, watch and the sweeper can still read the run.
func TestCancelIsJobsCancelNotDelete(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := bf.Cancels(); len(got) != 1 || got[0] != ref.Job {
		t.Fatalf("jobs.cancel calls = %v, want [%s]", got, ref.Job)
	}
	if got := bf.Deletes(); len(got) != 0 {
		t.Fatalf("Cancel called jobs.delete: %v", got)
	}
	if got := bf.State(ref.Job); got != "CANCELLED" {
		t.Fatalf("state after Cancel = %q, want CANCELLED", got)
	}
	// The record survives: the job is still readable, and terminal.
	x, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution after Cancel: %v", err)
	}
	if x.State != backend.StateCancelled || !x.State.Terminal() {
		t.Fatalf("Execution after Cancel = %+v", x)
	}
}

// TestCancelToleratesAFinishedJob covers the race design §1.4 names: a job
// can finish between the caller's read and the cancel, and stopping a run
// that has already stopped is not a failure. What the API answers for a job
// that already finished is UNVERIFIED (Check 57a step 7), so the tolerance is
// a read-back rather than an interpretation of the refusal's code; the fake
// refuses (400 FAILED_PRECONDITION), which is its own choice, so this
// exercises the read-back.
func TestCancelToleratesAFinishedJob(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bf.SetState(ref.Job, "SUCCEEDED")
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatalf("Cancel of a finished job: %v", err)
	}
	if got := bf.Cancels(); len(got) != 0 {
		t.Fatalf("the fake recorded a cancel it refused: %v", got)
	}
}

// TestCancelAndExecutionRefuseForeignNames: names reach these calls from
// bucket objects a run's own service account can write, so one that is not a
// job of this region must never reach a request path (design §2.5).
func TestCancelAndExecutionRefuseForeignNames(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, nil)
	for _, name := range []string{
		"projects/" + testProject + "/locations/europe-west1/jobs/fugaro-acme-app-web-0123456789ab",
		"projects/other-project/locations/" + testRegion + "/jobs/fugaro-acme-app-web-0123456789ab/executions/x",
		"not-a-name",
		"",
	} {
		if err := b.Cancel(ctx, name); err == nil {
			t.Errorf("Cancel(%q) accepted", name)
		}
		if _, err := b.Execution(ctx, name); err == nil {
			t.Errorf("Execution(%q) accepted", name)
		}
	}
}

// TestExecutionOfUnknownJobIsNotFound keeps the seam's contract: callers test
// errors.Is(err, backend.ErrNotFound) to tell "gone" from "broken".
func TestExecutionOfUnknownJobIsNotFound(t *testing.T) {
	b, _, _, _ := newTestBackend(t, nil)
	_, err := b.Execution(context.Background(), "projects/"+testProject+"/locations/"+testRegion+"/jobs/fugaro-nope-web-0123456789ab")
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Execution of an unknown job = %v, want ErrNotFound", err)
	}
}

// TestStateMapping covers the complete JobStatus.State enum (design §2.3,
// §9) and the value this build does not know. An unrecognised state must
// never read as finished: the sweeper would ignore a run whose VM is still
// up, and a watch would exit while the job runs.
func TestStateMapping(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want backend.State
	}{
		{"STATE_UNSPECIFIED", backend.StatePending},
		{"QUEUED", backend.StatePending},
		{"SCHEDULED", backend.StatePending},
		{"RUNNING", backend.StateRunning},
		{"SUCCEEDED", backend.StateSucceeded},
		{"FAILED", backend.StateFailed},
		{"CANCELLATION_IN_PROGRESS", backend.StateCancelled},
		{"CANCELLED", backend.StateCancelled},
		{"DELETION_IN_PROGRESS", backend.StateCancelled},
	} {
		got, known := stateOf(c.raw)
		if !known || got != c.want {
			t.Errorf("stateOf(%q) = %v, %v; want %v, true", c.raw, got, known, c.want)
		}
	}
	// The table above is the whole enum: Batch v1 has no SUCCEEDING, no
	// FAILING, and nothing else (design §9).
	if _, known := stateOf("SUCCEEDING"); known {
		t.Error("stateOf accepted a value Batch does not document")
	}
	if st, known := stateOf(""); known || st.Terminal() {
		t.Errorf("stateOf(\"\") = %v, %v: an empty state must not read as finished", st, known)
	}
}

// TestUnrecognisedStateIsPendingAndWarned drives the same rule through a real
// read, which is where a wrong answer would be acted on.
func TestUnrecognisedStateIsPendingAndWarned(t *testing.T) {
	ctx := context.Background()
	b, bf, _, warns := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bf.SetState(ref.Job, "SOMETHING_NEW")
	x, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	if x.State.Terminal() {
		t.Fatalf("an unknown state read as %v, which is terminal", x.State)
	}
	if len(*warns) == 0 || !strings.Contains((*warns)[0], "SOMETHING_NEW") {
		t.Fatalf("warnings = %v, want one naming the state", *warns)
	}
}

// TestJobDefinitionIsTheDesignsJob checks the job JSON Launch builds against
// the fields design §3 and §8.1 Task 7 name, as far as a fake-level test can
// hold them. The parts that need the cloud -- the socket mount, the env
// allowlist, the IAM -- are Task 7's, and provisioning is Task 6's.
func TestJobDefinitionIsTheDesignsJob(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.Secrets = map[string]string{"ANTHROPIC_API_KEY": testSecret}
	})
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	creates := bf.Creates()
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	call := creates[0]
	if call.JobID != ref.Job {
		t.Fatalf("created job id = %q, ExecutionRef.Job = %q", call.JobID, ref.Job)
	}
	if !batchJobIDRE.MatchString(call.JobID) {
		t.Fatalf("job id %q is not a Batch job id", call.JobID)
	}
	if want := "projects/" + testProject + "/locations/" + testRegion + "/jobs/" + call.JobID; ref.Name != want {
		t.Fatalf("ExecutionRef.Name = %q, want %q", ref.Name, want)
	}

	// Labels, in both fields (C13): Job.labels reach the job and its log
	// entries, AllocationPolicy.labels reach the VMs.
	want := map[string]any{
		"fugaro":          "managed",
		"fugaro_repo":     backendtest.Slug,
		"fugaro_workflow": backendtest.Workflow,
		"fugaro_run":      backendtest.Slug + "-" + spec().RunID,
	}
	if got := object(t, call.Job, "labels"); !equalJSON(got, want) {
		t.Errorf("Job.labels = %v, want %v", got, want)
	}
	if got := object(t, call.Job, "allocationPolicy", "labels"); !equalJSON(got, want) {
		t.Errorf("AllocationPolicy.labels = %v, want %v", got, want)
	}

	// The run's own names: FUGARO_RUN records the run, FUGARO_BACKEND is the
	// launcher's record of the backend it chose, FUGARO_PROJECT is what a
	// run's RequireProject check reads, and BatchJobEnv is how the codec names
	// the job, which Batch itself does not inject (design §2.5).
	vars := object(t, call.Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	for k, want := range map[string]any{
		"FUGARO_RUN":         backendtest.Slug + "/" + spec().RunID,
		"FUGARO_BACKEND":     backend.CloudBatch,
		"FUGARO_PROJECT":     "acme",
		"FUGARO_GCP_PROJECT": testProject,
		"FUGARO_REGION":      testRegion,
		backend.BatchJobEnv:  ref.Name,
	} {
		if vars[k] != want {
			t.Errorf("job env %s = %v, want %v", k, vars[k], want)
		}
	}
	// Secrets by name, never by value (design §1.6, C10): the value here is a
	// Secret Manager secret's resource name.
	if got := object(t, call.Job, "taskGroups", "0", "taskSpec", "environment", "secretVariables"); !equalJSON(got, map[string]any{"ANTHROPIC_API_KEY": testSecret}) {
		t.Errorf("secretVariables = %v", got)
	}

	taskSpec := object(t, call.Job, "taskGroups", "0", "taskSpec")
	// A run is not idempotent, so nothing is retried (design §2.3). The zero
	// must be in the JSON and not dropped as an empty value: a job that omits
	// it takes Batch's default, and a reader of the job could not then tell
	// the decision from an omission.
	if got, ok := taskSpec["maxRetryCount"]; !ok || fmt.Sprint(got) != "0" {
		t.Errorf("maxRetryCount = %v (present: %v), want 0", got, ok)
	}
	// The workflow's 30m task timeout plus backend.TaskTimeoutSlack.
	if got := taskSpec["maxRunDuration"]; got != "1920s" {
		t.Errorf("maxRunDuration = %v, want 1920s", got)
	}
	if got := object(t, call.Job, "taskGroups", "0", "taskSpec", "runnables", "0", "container")["imageUri"]; got != testImage {
		t.Errorf("imageUri = %v, want %s", got, testImage)
	}
	// One job is one task is one run.
	if got := object(t, call.Job, "taskGroups", "0")["taskCount"]; fmt.Sprint(got) != "1" {
		t.Errorf("taskCount = %v, want 1", got)
	}
	// Without this the job's output never reaches Cloud Logging, and a run's
	// logs land in the project's default logs (design §1.5, C9).
	if got := object(t, call.Job, "logsPolicy")["destination"]; got != "CLOUD_LOGGING" {
		t.Errorf("logsPolicy.destination = %v", got)
	}
	// A VM running a Docker-capable agent must not accept project-wide SSH
	// keys (design §3.5).
	if got := object(t, call.Job, "allocationPolicy", "instances", "0")["blockProjectSshKeys"]; got != true {
		t.Errorf("blockProjectSshKeys = %v, want true", got)
	}
	// Never the default Compute Engine account (design §3.2, C6/C21).
	if got := object(t, call.Job, "allocationPolicy", "serviceAccount")["email"]; got != testSA {
		t.Errorf("serviceAccount.email = %v, want %s", got, testSA)
	}
}

// TestLaunchSendsNoSecretValue is C10 stated as a search: the bytes Batch
// receives must not contain a secret's value. The job carries names only, and
// this is the test that would fail if someone later resolved one.
func TestLaunchSendsNoSecretValue(t *testing.T) {
	ctx := context.Background()
	const value = "sk-ant-not-a-real-secret-value"
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.Secrets = map[string]string{"ANTHROPIC_API_KEY": testSecret}
	})
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	raw, err := json.Marshal(bf.Creates()[0].Job)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), value) || strings.Contains(string(raw), "sk-ant-") {
		t.Fatalf("the job definition carries a secret value: %s", raw)
	}
}

// TestLaunchTimeoutComesFromTheWorkflow covers the two sources of the run's
// total time: the caller's when it has one, the workflow's configuration
// otherwise (design §2.3 -- on Batch there is no deployed job to read it
// from).
func TestLaunchTimeoutComesFromTheWorkflow(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec")["maxRunDuration"]; got != "1920s" {
		t.Errorf("the workflow's 30m timeout gave maxRunDuration %v", got)
	}
	// A caller that knows the run's own total (the launcher does) wins.
	s := spec()
	s.RunID = "20260927-110000-beef"
	s.Timeout = time.Hour
	if _, err := b.Launch(ctx, s); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := object(t, bf.Creates()[1].Job, "taskGroups", "0", "taskSpec")["maxRunDuration"]; got != "3720s" {
		t.Errorf("an hour plus the slack gave maxRunDuration %v, want 3720s", got)
	}
}

// TestLaunchOfAnUnknownWorkflowIsNotFound: the timeouts are a config read, so
// a workflow that does not exist must be reported as such rather than
// launched with a guessed timeout. It created nothing, so it is also a
// definitive refusal: the launcher drops the run's claim and its minted
// budget token instead of holding them and telling the operator the outcome
// is unknown.
func TestLaunchOfAnUnknownWorkflowIsNotFound(t *testing.T) {
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.Timeouts = testTimeouts{err: fmt.Errorf("no such workflow: %w", backend.ErrNotFound)}
	})
	_, err := b.Launch(context.Background(), spec())
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Launch = %v, want ErrNotFound", err)
	}
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch = %v, want ErrRejected too: nothing was created", err)
	}
	if got := bf.Creates(); len(got) != 0 {
		t.Fatalf("a job was created for an unknown workflow: %v", got)
	}
}

// TestLaunchBeforeTheCreateIsRejected: everything that can fail before the
// create created nothing, whatever the cause, so it says so with ErrRejected
// (design §2.3). The launcher acts on that by releasing the run's claim and
// dropping the budget token it minted, so a corrected retry goes at once --
// without it, an operator is told "the outcome is unknown; don't relaunch
// before <claim TTL>" for a launch that never reached the API.
func TestLaunchBeforeTheCreateIsRejected(t *testing.T) {
	ctx := context.Background()
	// A workflow the installation cannot write as a label: the job can never
	// be built, and no request is sent.
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	s.Workflow = "Web"
	_, err := b.Launch(ctx, s)
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch with an unwritable label = %v, want ErrRejected", err)
	}
	// The timeout source failing for a reason that is not a missing workflow
	// (an unreadable config, say) is still nothing created.
	b2, bf2, _, _ := newTestBackend(t, func(o *Options) {
		o.Timeouts = testTimeouts{err: errors.New("reading the workflow's timeout: permission denied")}
	})
	if _, err := b2.Launch(ctx, spec()); !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch with an unreadable timeout = %v, want ErrRejected", err)
	}
	for _, f := range []*gcpfake.Batch{bf, bf2} {
		if got := f.Creates(); len(got) != 0 {
			t.Fatalf("a rejected launch created a job: %v", got)
		}
	}
}

// TestLaunchNeedsARun checks the argument validation, the one place a
// caller's own mistake is caught before any request is sent. A run Fugaro
// cannot turn into a job created nothing, so it is a refusal, not an unknown
// outcome.
func TestLaunchNeedsARun(t *testing.T) {
	b, bf, _, _ := newTestBackend(t, nil)
	for name, s := range map[string]backend.LaunchSpec{
		"no slug":     {Workflow: "web", RunID: "20260927-100000-abcd"},
		"no workflow": {Repo: backend.RepoRef{Slug: "acme-app"}, RunID: "20260927-100000-abcd"},
		"no run id":   {Repo: backend.RepoRef{Slug: "acme-app"}, Workflow: "web"},
	} {
		_, err := b.Launch(context.Background(), s)
		if err == nil {
			t.Errorf("Launch accepted a run with %s", name)
			continue
		}
		if !errors.Is(err, backend.ErrRejected) {
			t.Errorf("Launch with %s = %v, want ErrRejected", name, err)
		}
	}
	if got := bf.Creates(); len(got) != 0 {
		t.Fatalf("a job was created for an invalid run: %v", got)
	}
}

// TestLaunchRefusalIsRejected: a 4xx other than 408, 429 and 499 created
// nothing and says so, so the launcher may undo its own bookkeeping. A
// conflict is the same run id launched twice: a Batch job is created per run
// and is single-use.
func TestLaunchRefusalIsRejected(t *testing.T) {
	b, bf, _, _ := newTestBackend(t, nil)
	bf.FailCreateWith = 409
	if _, err := b.Launch(context.Background(), spec()); !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch of a duplicate run = %v, want ErrRejected", err)
	}
	// A 5xx is ambiguous: the job may exist.
	bf.FailCreateWith = 500
	if _, err := b.Launch(context.Background(), spec()); err == nil || errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch with a 500 = %v, want an ambiguous error", err)
	}
}

// TestNewRefusesAnUnsafeConfiguration: the facts an installation must never
// get wrong are which image a run runs, which account its VMs are (design
// §3.2), and that nothing is launched without a timeout to bound it.
func TestNewRefusesAnUnsafeConfiguration(t *testing.T) {
	for name, tweak := range map[string]func(*Options){
		"no image":                func(o *Options) { o.Image = "" },
		"no service account":      func(o *Options) { o.ServiceAccount = "" },
		"the default compute one": func(o *Options) { o.ServiceAccount = "123456789-compute@developer.gserviceaccount.com" },
		"no Fugaro project":       func(o *Options) { o.FugaroProject = "" },
		"no GCP project":          func(o *Options) { o.GCPProject = "" },
		"no region":               func(o *Options) { o.Region = "" },
		"no timeout source":       func(o *Options) { o.Timeouts = nil },
	} {
		o := testOptions(t)
		tweak(&o)
		if b, err := New(context.Background(), o); err == nil {
			t.Errorf("New accepted %s: %+v", name, b)
		}
	}
}

// TestCheckServiceAccountMatchesTheDefaultAccountOnly: the rule is about the
// default Compute Engine account's own domain, not about anything that merely
// looks like it. A near miss must be allowed, or the check would refuse
// accounts an installation is entitled to use.
func TestCheckServiceAccountMatchesTheDefaultAccountOnly(t *testing.T) {
	for _, ok := range []string{
		testSA,
		"fugaro-job@other-project.iam.gserviceaccount.com",
		"123456789-compute@developer.example.com",
		"compute@developer.gserviceaccount.com",
	} {
		if err := checkServiceAccount(ok); err != nil {
			t.Errorf("checkServiceAccount(%q) = %v, want nil", ok, err)
		}
	}
	if err := checkServiceAccount("123456789-compute@developer.gserviceaccount.com"); err == nil {
		t.Error("checkServiceAccount accepted the default Compute Engine account")
	}
}

// TestJobID pins the id rule (design §2.4): Batch's charset and length, one
// id per run, and a stable id for the same run.
func TestJobID(t *testing.T) {
	id := JobID(backendtest.Slug, backendtest.Workflow, "20260927-100000-abcd")
	if !batchJobIDRE.MatchString(id) {
		t.Fatalf("JobID = %q, not a Batch job id", id)
	}
	if !strings.HasPrefix(id, "fugaro-acme-app-web-") {
		t.Fatalf("JobID = %q, want the fugaro-<slug>-<workflow>- prefix", id)
	}
	if id != JobID(backendtest.Slug, backendtest.Workflow, "20260927-100000-abcd") {
		t.Error("JobID is not deterministic")
	}
	// Two runs of one workflow differ, and so do two workflows.
	if id == JobID(backendtest.Slug, backendtest.Workflow, "20260927-100001-abcd") {
		t.Error("two runs share a job id")
	}
	if id == JobID(backendtest.Slug, "check", "20260927-100000-abcd") {
		t.Error("two workflows share a job id")
	}
	// A long slug and workflow are truncated to fit, and the hash keeps two
	// different pairs apart.
	long := strings.Repeat("a-very-long-repository-name-", 4)
	one := JobID(long, "a-very-long-workflow-name", "20260927-100000-abcd")
	two := JobID(long, "a-very-long-workflow-name", "20260927-100001-abcd")
	for _, got := range []string{one, two} {
		if !batchJobIDRE.MatchString(got) {
			t.Fatalf("JobID(long) = %q (%d characters), not a Batch job id", got, len(got))
		}
	}
	if one == two {
		t.Error("truncation made two runs of a long slug collide")
	}
	// A slug that sanitizes away entirely still gets an id.
	if got := JobID("!!!", "web", "run"); !batchJobIDRE.MatchString(got) {
		t.Fatalf("JobID of an unsanitizable slug = %q", got)
	}
}

// TestRunLabel pins the per-run label the reaper finds a leaked VM by (design
// §2.4, C13), including the truncation that must not collide two runs.
func TestRunLabel(t *testing.T) {
	got, err := RunLabelValue(backendtest.Slug, "20260927-100000-abcd")
	if err != nil || got != "acme-app-20260927-100000-abcd" {
		t.Fatalf("RunLabelValue = %q, %v", got, err)
	}
	long := strings.Repeat("a-very-long-repository-name-", 4)
	one, err := RunLabelValue(long, "20260927-100000-abcd")
	if err != nil || len(one) > 63 {
		t.Fatalf("RunLabelValue(long) = %q (%d), %v", one, len(one), err)
	}
	two, err := RunLabelValue(long, "20260927-100001-abcd")
	if err != nil || one == two {
		t.Fatalf("truncation made two runs collide: %q, %q, %v", one, two, err)
	}
	if _, err := RunLabelValue("", "run"); err == nil {
		t.Error("a run label with no slug was accepted")
	}
}

// TestLabelsRefuseWhatIsNotALabel: a label the installation cannot write is
// an error, never a shortened or widened one -- the marker is what the
// installation adopts resources by.
func TestLabelsRefuseWhatIsNotALabel(t *testing.T) {
	if _, err := Labels(backendtest.Slug, "Web", "20260927-100000-abcd"); err == nil {
		t.Error("an uppercase workflow was accepted as a label value")
	}
	if _, err := Labels(strings.Repeat("s", 64), "web", "run"); err == nil {
		t.Error("a slug too long for a label was accepted")
	}
	l, err := Labels(backendtest.Slug, "web_default", "20260927-100000-abcd")
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	if l[labelManaged] != managedValue || l["fugaro_workflow"] != "web_default" || l[RunLabel] != "acme-app-20260927-100000-abcd" {
		t.Fatalf("Labels = %v", l)
	}
}

// TestManagedJobNeedsBothLabelFields: Job.labels alone leave every VM of the
// run unlabelled, so the reaper finds nothing precisely when a VM has leaked
// (design §2.4, C13).
func TestManagedJobNeedsBothLabelFields(t *testing.T) {
	marked := map[string]string{labelManaged: managedValue}
	if !managedJob(&batch.Job{Labels: marked, AllocationPolicy: &batch.AllocationPolicy{Labels: marked}}) {
		t.Error("a job marked in both fields is not ours")
	}
	for name, j := range map[string]*batch.Job{
		"no labels at all":    {},
		"job labels only":     {Labels: marked},
		"allocation only":     {AllocationPolicy: &batch.AllocationPolicy{Labels: marked}},
		"a foreign marker":    {Labels: map[string]string{labelManaged: "someone-else"}, AllocationPolicy: &batch.AllocationPolicy{Labels: map[string]string{labelManaged: "someone-else"}}},
		"a Fugaro-shaped one": {Labels: map[string]string{"fugaro": "true"}, AllocationPolicy: &batch.AllocationPolicy{Labels: map[string]string{"fugaro": "true"}}},
	} {
		if managedJob(j) {
			t.Errorf("managedJob accepted a job with %s", name)
		}
	}
}

// TestListFiltersByLabelStateAndTime: jobs.list keeps returning a job for 60
// days after it finished (design §2.4), so a listing that assumed its
// contents were live work would report finished runs as active ones.
func TestListFiltersByLabelStateAndTime(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	old, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	s := spec()
	s.RunID = "20260927-110000-beef"
	live, err := b.Launch(ctx, s)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// The old job finished a day ago, which is exactly the case the API keeps
	// listing for 60 days: it must not read as active work.
	bf.SetCreated(old.Job, time.Now().Add(-24*time.Hour))
	bf.SetState(old.Job, "SUCCEEDED")
	bf.SetState(live.Job, "RUNNING")

	all, err := b.List(ctx, backend.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List = %+v, want both runs", all)
	}
	active, err := b.List(ctx, backend.ListFilter{ActiveOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(active) != 1 || active[0].Name != live.Name {
		t.Fatalf("List(ActiveOnly) = %+v, want only %s", active, live.Name)
	}
	// The state comes back as the seam's, and the run's own FUGARO_RUN with
	// it.
	if active[0].State != backend.StateRunning || active[0].Run != backendtest.Slug+"/20260927-110000-beef" {
		t.Fatalf("List(ActiveOnly)[0] = %+v", active[0])
	}
	since, err := b.List(ctx, backend.ListFilter{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(since) != 1 || since[0].Name != live.Name {
		t.Fatalf("List(Since) = %+v, want only %s", since, live.Name)
	}
	// A named job narrows the listing.
	one, err := b.List(ctx, backend.ListFilter{Jobs: []string{old.Job}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(one) != 1 || one[0].Name != old.Name {
		t.Fatalf("List(Jobs) = %+v, want only %s", one, old.Name)
	}
	// Every listing asked the server for the installation's own jobs.
	for _, r := range bf.Requests() {
		if r.Method == "GET" && strings.HasSuffix(r.Path, "/jobs") && !strings.Contains(r.Query, "filter=") {
			t.Errorf("jobs.list without a filter: %s?%s", r.Path, r.Query)
		}
	}
}

// TestListKeepsAJobWhoseCreateTimeIsMissing: a job the API answered without a
// createTime reads as the zero time, which is Before every Since. It is kept
// rather than cut (Options.Warn: one bad job does not fail a whole listing),
// because the caller of ActiveOnly+Since is the sweeper, where a job missing
// from the listing is a VM nobody reaps. gcp's List applies the same cut only
// to a time it parsed and that is non-zero.
func TestListKeepsAJobWhoseCreateTimeIsMissing(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	unreadable, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	s := spec()
	s.RunID = "20260927-110000-beef"
	live, err := b.Launch(ctx, s)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// The job's createTime goes missing, which is what an unreadable or
	// absent one looks like to the backend.
	bf.SetCreated(unreadable.Job, time.Time{})
	bf.SetState(live.Job, "RUNNING")

	got, err := b.List(ctx, backend.ListFilter{Since: time.Now().Add(-time.Hour), ActiveOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List(Since, ActiveOnly) = %+v, want both jobs: a missing createTime is kept", got)
	}
	// The one that was cut, if any, is the zero-time job; it is the last of
	// the newest-first order.
	if !got[1].Created.IsZero() || got[1].Name != unreadable.Name {
		t.Fatalf("List(Since, ActiveOnly)[1] = %+v, want %s with a zero time", got[1], unreadable.Name)
	}
	// And a job whose createTime is readable and older than Since is still
	// cut: keeping the unreadable one must not turn the cut off.
	bf.SetCreated(unreadable.Job, time.Now().Add(-48*time.Hour))
	got, err = b.List(ctx, backend.ListFilter{Since: time.Now().Add(-time.Hour), ActiveOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != live.Name {
		t.Fatalf("List(Since, ActiveOnly) = %+v, want only %s", got, live.Name)
	}
}

// TestListFollowsPageTokens: a region's listing is paged, and a run dropped
// at a page boundary would be a run the sweeper never sees.
func TestListFollowsPageTokens(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, nil)
	b.listPageSize = 1
	for _, run := range []string{"20260927-100000-aaaa", "20260927-100001-bbbb", "20260927-100002-cccc"} {
		s := spec()
		s.RunID = run
		if _, err := b.Launch(ctx, s); err != nil {
			t.Fatalf("Launch: %v", err)
		}
	}
	got, err := b.List(ctx, backend.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List = %d runs, want 3", len(got))
	}
}

// TestLogsReadsOneJobsTaskLogsByUID pins the filter design §2.3 ships, and
// why it costs one jobs.get: labels.job_uid needs the job's UID, which only
// the job itself reports.
func TestLogsReadsOneJobsTaskLogsByUID(t *testing.T) {
	ctx := context.Background()
	b, bf, lf, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	uid, _ := bf.JobJSON(testProject, testRegion, ref.Job)["uid"].(string)
	if uid == "" {
		t.Fatalf("the fake's job has no UID: %v", bf.JobJSON(testProject, testRegion, ref.Job))
	}
	// Someone else's job's logs, which must not be read.
	lf.AddUID("someone-elses-uid", gcpfake.LogEntry{Time: time.Now(), Text: "not ours"})
	lf.AddUID(uid, gcpfake.LogEntry{Time: time.Now(), Text: "hello", Severity: "INFO"})
	lf.AddUID(uid, gcpfake.LogEntry{Time: time.Now().Add(time.Second), JSON: map[string]any{"message": "world", "run_id": "x"}})

	var got []backend.LogEntry
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(e backend.LogEntry) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(got) != 2 || got[0].Message != "hello" || got[1].Message != "world" {
		t.Fatalf("Logs = %+v", got)
	}
	if got[0].Severity != "INFO" || got[1].Fields["run_id"] != "x" {
		t.Fatalf("the entries were not mapped: %+v", got)
	}
	// The read names the job's UID and the batch_task_logs log, through the
	// installation's own project.
	var sawFilter string
	for _, r := range lf.Requests() {
		if r.Method != "POST" {
			continue
		}
		var req struct{ Filter, OrderBy string }
		if err := json.Unmarshal(r.Body, &req); err != nil {
			t.Fatalf("entries.list body: %v", err)
		}
		sawFilter = req.Filter
		if req.OrderBy != "timestamp asc" {
			t.Errorf("orderBy = %q, want timestamp asc", req.OrderBy)
		}
	}
	if !strings.Contains(sawFilter, `labels.job_uid="`+uid+`"`) ||
		!strings.Contains(sawFilter, `logName="projects/`+testProject+`/logs/batch_task_logs"`) {
		t.Fatalf("the log filter was %q", sawFilter)
	}
}

// TestLogsStopWhenTheCallbackStops: a caller that has seen enough (a --tail,
// a closed pipe) must not have the rest of the logs read for it.
func TestLogsStopWhenTheCallbackStops(t *testing.T) {
	ctx := context.Background()
	b, bf, lf, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	uid, _ := bf.JobJSON(testProject, testRegion, ref.Job)["uid"].(string)
	for i := 0; i < 5; i++ {
		lf.AddUID(uid, gcpfake.LogEntry{Time: time.Now().Add(time.Duration(i) * time.Second), Text: fmt.Sprint(i)})
	}
	stop := errors.New("enough")
	seen := 0
	err = b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(backend.LogEntry) error {
		seen++
		if seen == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("Logs = %v, want the callback's own error", err)
	}
	if seen != 2 {
		t.Fatalf("the callback ran %d times after it stopped", seen)
	}
}

// TestLogsOfAJobWithoutAUIDIsAnError: without the UID the read cannot select
// one job's task logs, and reading the whole project's logs instead would be
// worse than failing.
func TestLogsOfAJobWithoutAUIDIsAnError(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bf.SetUID(ref.Job, "")
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(backend.LogEntry) error { return nil }); err == nil {
		t.Fatal("Logs of a job with no UID read something")
	}
}

// TestLogsFollowIsNotSilentlyIgnored: a follow that quietly read once would
// look exactly like a run that had gone silent. Task 4 owns it, with the
// installation's sink arm.
func TestLogsFollowIsNotSilentlyIgnored(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	err = b.Logs(ctx, backend.LogQuery{Execution: ref.Name, Follow: true}, func(backend.LogEntry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "Task 4") {
		t.Fatalf("Logs(Follow) = %v, want a refusal naming Task 4", err)
	}
}

// TestLogURL pins the link a run carries: a Logs Explorer query scoped to the
// installation's log view. A run whose UID is unknown cannot be selected at
// all, so it answers "" rather than a link that opens the whole project's
// logs.
func TestLogURL(t *testing.T) {
	b, _, _, _ := newTestBackend(t, func(o *Options) {
		o.LogView = "projects/" + testProject + "/locations/global/buckets/fugaro/views/runs"
	})
	id := backend.ExecID{GCPProject: testProject, Region: testRegion, Job: "fugaro-acme-app-web-0123456789ab"}
	u := b.logURL(id, "uid-1", time.Now())
	for _, want := range []string{"logs/query", "batch_task_logs", "storageScope=storage", "project=" + testProject} {
		if !strings.Contains(u, want) {
			t.Errorf("logURL = %q, want it to contain %q", u, want)
		}
	}
	if got := b.logURL(id, "", time.Now()); got != "" {
		t.Fatalf("logURL without a UID = %q, want empty", got)
	}
}

// TestTimeoutsComeFromTheWorkflowConfiguration: on Batch there is no deployed
// job to read a timeout from, so both questions are the config/policy read's
// (design §2.3).
func TestTimeoutsComeFromTheWorkflowConfiguration(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, func(o *Options) {
		o.Timeouts = testTimeouts{task: 17 * time.Minute, longest: 3 * time.Hour}
	})
	if d, err := b.TaskTimeout(ctx, backendtest.Slug, backendtest.Workflow); err != nil || d != 17*time.Minute {
		t.Fatalf("TaskTimeout = %v, %v", d, err)
	}
	if d, err := b.LongestTaskTimeout(ctx); err != nil || d != 3*time.Hour {
		t.Fatalf("LongestTaskTimeout = %v, %v", d, err)
	}
}

// TestCodecIsTheBatchCodec: the backend answers with the shape its own names
// have, which is what a caller must ask the selected backend for rather than
// assume Cloud Run's (design §2.5).
func TestCodecIsTheBatchCodec(t *testing.T) {
	b, _, _, _ := newTestBackend(t, nil)
	c := b.Codec()
	if c.Backend() != backend.CloudBatch {
		t.Fatalf("Codec().Backend() = %q", c.Backend())
	}
	ref, err := b.Launch(context.Background(), spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	id, ok := c.Parse(ref.Name)
	if !ok || id.Job != ref.Job || id.Name != "" {
		t.Fatalf("Codec().Parse(%q) = %+v, %v", ref.Name, id, ok)
	}
	if got := c.Format(id); got != ref.Name {
		t.Fatalf("Codec().Format = %q, want %q", got, ref.Name)
	}
}

// batchJobIDRE is Batch's job id rule, restated here so the test does not
// depend on the fake's copy of it.
var batchJobIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)

// object walks a decoded JSON object, with integer strings indexing arrays.
func object(t *testing.T, v any, keys ...string) map[string]any {
	t.Helper()
	for _, k := range keys {
		switch cur := v.(type) {
		case map[string]any:
			v = cur[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(cur) {
				t.Fatalf("path %v: no index %q", keys, k)
			}
			v = cur[i]
		default:
			t.Fatalf("path %v: %T has no key %q", keys, v, k)
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("path %v is %T, not an object", keys, v)
	}
	return m
}

// equalJSON compares two decoded JSON values by their canonical JSON.
func equalJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}
