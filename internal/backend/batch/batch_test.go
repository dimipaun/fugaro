package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

	// The VM the installation's runs get (Task 7): a machine type the price
	// table carries, the preloaded boot image, and the two network names a
	// run's VMs are placed in.
	testMachineType = "e2-standard-4"
	testBootImage   = "projects/fake-project/global/images/fugaro-base-0-7-5"

	// testSocketGID is the gid the installation recorded after reading it off
	// the VM's socket (`stat -c %g /var/run/docker.sock`, Check 57a step 6).
	// It is deliberately NOT 412, which is what that one boot image answered
	// on 2026-10-15: the backend must carry the gid it was given and never one
	// of its own, so a test that passed by matching a hard-coded 412 would be
	// testing the wrong thing.
	testSocketGID = 998
)

// testNetwork is the network interface pair a run's VMs get. Both are named
// because Batch refuses the no-external-IP field without both (design §1.7,
// measured by Check 57a).
func testNetwork() (network, subnetwork string) {
	return "projects/" + testProject + "/global/networks/default",
		"projects/" + testProject + "/regions/" + testRegion + "/subnetworks/default"
}

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
	network, subnetwork := testNetwork()
	return Options{
		GCPProject:     testProject,
		Region:         testRegion,
		FugaroProject:  "acme",
		Image:          testImage,
		ServiceAccount: testSA,
		MachineType:    testMachineType,
		BootImage:      testBootImage,
		Network:        network,
		Subnetwork:     subnetwork,
		// The installation's socket: the gid the VM reported, and the path
		// Batch mounts it at (design §2.2). A run asks for it with
		// LaunchSpec.Docker.
		Docker:    Docker{SocketGID: testSocketGID},
		Timeouts:  testTimeouts{task: 30 * time.Minute, longest: time.Hour},
		Endpoints: Endpoints{Batch: bf.URL + "/", Logging: lf.URL + "/", NoAuth: true},
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

// TestCancelErrorsAndRaces: Cancel is jobs.cancel plus a read-back, and every
// way the two can disagree is a case a caller meets in the field. The error
// path matters as much as the tolerance: a cancel of a job that is not there
// is ErrNotFound, and swallowing it would report a run stopped that never
// existed. (Making Cancel return nil unconditionally passes every other test
// in this file, which is why this one exists.)
func TestCancelErrorsAndRaces(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if err := b.Cancel(ctx, "projects/"+testProject+"/locations/"+testRegion+"/jobs/fugaro-nope-web-0123456789ab"); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Cancel of an unknown job = %v, want ErrNotFound", err)
	}
	if got := bf.Cancels(); len(got) != 0 {
		t.Fatalf("the fake recorded a cancel of a job it does not have: %v", got)
	}

	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// Cancelling twice: the second call meets a job that is already
	// CANCELLED, and stopping a run that has stopped is not a failure. The
	// fake refuses the second (400 FAILED_PRECONDITION), so the tolerance is
	// the read-back's, not the API's answer.
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatalf("first Cancel: %v", err)
	}
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
	if got := bf.Cancels(); len(got) != 1 {
		t.Fatalf("cancels recorded = %v, want one: the second is refused and tolerated", got)
	}

	// A job that finished between the caller's read and the cancel.
	s := spec()
	s.RunID = "20260927-120000-cafe"
	done, err := b.Launch(ctx, s)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bf.SetState(done.Job, "SUCCEEDED")
	if err := b.Cancel(ctx, done.Name); err != nil {
		t.Fatalf("Cancel of a finished job: %v", err)
	}
	if got := bf.Cancels(); len(got) != 1 {
		t.Fatalf("cancels recorded = %v, want the refusal not to be recorded", got)
	}
	// The same race with the fake told the API answers OK instead of
	// FAILED_PRECONDITION (which of the two it really answers is UNVERIFIED,
	// Check 57a step 7): the call succeeds either way.
	bf.AllowCancelOfTerminal = true
	if err := b.Cancel(ctx, done.Name); err != nil {
		t.Fatalf("Cancel with AllowCancelOfTerminal: %v", err)
	}
	if got := bf.Cancels(); len(got) != 2 {
		t.Fatalf("cancels recorded = %v, want the accepted one recorded", got)
	}
	if got := bf.State(done.Job); got != "SUCCEEDED" {
		t.Fatalf("cancelling a finished job changed its state to %q", got)
	}

	// A job already on its way out: CANCELLATION_IN_PROGRESS is cancelled for
	// the seam, so the refusal is tolerated the same way.
	s = spec()
	s.RunID = "20260927-120001-feed"
	stopping, err := b.Launch(ctx, s)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bf.SetState(stopping.Job, "CANCELLATION_IN_PROGRESS")
	if err := b.Cancel(ctx, stopping.Name); err != nil {
		t.Fatalf("Cancel of a job that is already stopping: %v", err)
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

// TestLaunchSendsNoSecretValue is C10 stated as a search of the bytes Batch
// receives, over the one surface where a caller could actually put a value
// there: Options.Secrets. The job carries names only, and this is the test
// that fails if someone later resolves one.
//
// Hand mutation: in run.go, write the secrets into the task's plain
// Variables as well (`vars[k] = v` beside `env.SecretVariables = ...`, or
// `env.Variables = b.o.Secrets` in its place). It is the mutation the review
// called M4, and it stayed green against the earlier version of this test:
// that one searched for a value the backend was never given, so nothing could
// make it fail. This one passes a recognisable value through the API surface
// -- both halves of the map, the value slot and the variable-name slot -- and
// then requires the job's serialized form to contain the secret's *name*
// exactly once.
//
// It also covers the two refusals, with a value spelled the way real
// credentials are: a name-shaped value passes envNameRE, so the entry whose
// key is the secret is the one the value slot's message must not print. Hand
// mutation: name that entry by its key in checkSecrets
// (`fmt.Errorf("the Batch backend's secret for %s is not ...", k)`); red.
func TestLaunchSendsNoSecretValue(t *testing.T) {
	ctx := context.Background()
	const (
		// A value that is not name-shaped at all: its dashes fail envNameRE, so
		// it is refused on either side of the map.
		value = "sk-ant-not-a-real-secret-value"
		// And a value that IS name-shaped -- an AWS access key id, a form real
		// credentials really take. It passes envNameRE, so the shape check
		// cannot tell it from a variable's name, and it is the value the old
		// value-slot refusal would have printed back (it named the entry by its
		// key, and the key was the secret).
		nameShaped = "AKIAIOSFODNN7EXAMPLE"
	)

	// A value used as the variable's name: the key slot, which refuses without
	// naming anything.
	o := testOptions(t)
	o.Secrets = map[string]string{value: testSecret}
	b0, err := New(ctx, o)
	if err == nil {
		t.Fatalf("New accepted a secret value as a variable name (%+v)", b0)
	}
	if strings.Contains(err.Error(), value) || strings.Contains(err.Error(), "sk-ant-") {
		t.Fatalf("the refusal repeats the secret value: %v", err)
	}

	// A value in the name slot with a key that is a variable's name: the value
	// slot's refusal, and it must not repeat the value.
	o = testOptions(t)
	o.Secrets = map[string]string{"ANTHROPIC_API_KEY": value}
	b1, err := New(ctx, o)
	if err == nil {
		t.Fatalf("New accepted a secret value in the name slot (%+v)", b1)
	}
	if strings.Contains(err.Error(), value) || strings.Contains(err.Error(), "sk-ant-") {
		t.Fatalf("the refusal repeats the secret value: %v", err)
	}

	// The same refusal where the key IS a secret: `nameShaped` is a value that
	// happens to be spelled like a variable's name, so the entry's key is the
	// thing that must not be printed. The entry is named by position instead.
	// Hand mutation: put the key back into the value slot's message
	// (`fmt.Errorf("the Batch backend's secret for %s is not ...", k)`); red.
	o = testOptions(t)
	o.Secrets = map[string]string{nameShaped: "AWS_ACCESS_KEY_ID"}
	b2, err := New(ctx, o)
	if err == nil {
		t.Fatalf("New accepted a secret value in the name slot, spelled like a variable name (%+v)", b2)
	}
	if strings.Contains(err.Error(), nameShaped) {
		t.Fatalf("the refusal repeats an environment-name-shaped secret: %v", err)
	}

	// The documented use: an environment variable name mapped to the secret's
	// resource name. The job may carry that name -- it is what the VM resolves
	// -- and nothing else about the secret.
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.Secrets = map[string]string{"ANTHROPIC_API_KEY": testSecret}
	})
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	job := bf.Creates()[0].Job
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), value) || strings.Contains(string(raw), "sk-ant-") {
		t.Fatalf("the job definition carries a secret value: %s", raw)
	}
	// Exactly once, and therefore only under secretVariables: the platform
	// resolves the entry there, so a copy of the name in the task's plain
	// variables is the same leak under another label (and a copy in both would
	// also be the collision jobDefinition refuses).
	if n := strings.Count(string(raw), testSecret); n != 1 {
		t.Fatalf("the secret's name appears %d times in the job definition, want once (under secretVariables): %s", n, raw)
	}
	if got := object(t, job, "taskGroups", "0", "taskSpec", "environment", "variables"); len(got) != 6 {
		t.Errorf("the task's plain variables = %v, want the job's own six and no secret", got)
	}
	for k, v := range object(t, job, "taskGroups", "0", "taskSpec", "environment", "variables") {
		if s, ok := v.(string); ok && strings.Contains(s, "secrets/") {
			t.Errorf("variables[%s] = %q names a secret: secrets belong in secretVariables", k, s)
		}
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

// TestLaunchRefusesAProjectJob: a spec that names a shared project job
// (LaunchSpec.Job, phase 3) is refused before anything is created. This
// backend has no project jobs, and launching the repository's own job in its
// place would run the workflow under an identity the project's policy did not
// pick (the merge of main into next-0.8.5 surfaced the gap).
func TestLaunchRefusesAProjectJob(t *testing.T) {
	b, bf, _, _ := newTestBackend(t, nil)
	_, err := b.Launch(context.Background(), backend.LaunchSpec{Repo: backend.RepoRef{Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd", Job: "fugaro-p-standard-github"})
	if err == nil || !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch of a project job = %v, want ErrRejected", err)
	}
	if got := bf.Creates(); len(got) != 0 {
		t.Fatalf("a job was created for a project-job launch: %v", got)
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

// TestListSortsNewestFirstWhateverTheServersOrderIs: the API documents no
// order for jobs.list (design §2.4), so the listing's order is the backend's
// own decision rather than the server's. The fake answers in creation order,
// oldest first, and the create times below are set so that creation order and
// create-time order disagree: only the explicit sort can produce the answer
// the callers want.
func TestListSortsNewestFirstWhateverTheServersOrderIs(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	var names []string
	for _, run := range []string{"20260927-100000-aaaa", "20260927-100001-bbbb", "20260927-100002-cccc"} {
		s := spec()
		s.RunID = run
		ref, err := b.Launch(ctx, s)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		names = append(names, ref.Job)
	}
	// The job launched first is the newest, and the one launched last is the
	// oldest: the server's order is not the answer.
	now := time.Now()
	bf.SetCreated(names[0], now)
	bf.SetCreated(names[1], now.Add(-3*time.Hour))
	bf.SetCreated(names[2], now.Add(-time.Hour))

	got, err := b.List(ctx, backend.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var order []string
	for _, x := range got {
		order = append(order, x.Job)
	}
	want := []string{names[0], names[2], names[1]}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("List = %v, want %v (newest first by createTime)", order, want)
	}
}

// TestListDropsAJobTheFilterMatchedButTheLabelsDoNot: the filter's syntax
// against the real API is not a verified fact (design §9), so List checks the
// labels itself. The job below carries the marker in Job.labels alone -- the
// field a server honouring the filter reads -- so it is exactly the answer
// such a server gives, and it is still not a job this installation may adopt:
// the marker has to be in both fields (design §2.4, C13).
func TestListDropsAJobTheFilterMatchedButTheLabelsDoNot(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	ours, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	const impostor = "fugaro-acme-app-web-deadbeef0000"
	postRawJob(t, bf, impostor, `{"labels":{"fugaro":"managed"}}`)

	got, err := b.List(ctx, backend.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != ours.Name {
		t.Fatalf("List = %+v, want only %s", got, ours.Name)
	}
	// Asked for by id it is dropped too: naming a job is not what makes it
	// ours, so a caller cannot reach one this way.
	got, err = b.List(ctx, backend.ListFilter{Jobs: []string{impostor}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List(Jobs) = %+v, want nothing", got)
	}
}

// postRawJob creates a job in the fake's region from a raw body, as a server
// that ignored Fugaro's filter would still be listing.
func postRawJob(t *testing.T, bf *gcpfake.Batch, id, body string) {
	t.Helper()
	u := bf.URL + "/v1/projects/" + testProject + "/locations/" + testRegion + "/jobs?jobId=" + id
	resp, err := http.Post(u, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("creating %s: status %d", id, resp.StatusCode)
	}
}

// TestExecutionRewritesTheProjectNumber: Batch answers a created job's name
// with the project *number*, so a name taken from it verbatim would stop
// matching the name the backend launched with (design §2.3). The fake is set
// to answer with the number, which is what makes the rewrite -- rather than
// the fake agreeing -- the reason the names below match.
func TestExecutionRewritesTheProjectNumber(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	bf.ProjectNumber = "999"
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if creates := bf.Creates(); len(creates) != 1 || !strings.HasPrefix(creates[0].Name, "projects/999/") {
		t.Fatalf("the create was answered with %+v, want the project number", creates)
	}
	want := "projects/" + testProject + "/locations/" + testRegion + "/jobs/" + ref.Job
	if ref.Name != want {
		t.Fatalf("Launch ref.Name = %q, want %q", ref.Name, want)
	}
	x, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	if x.Name != want || x.Job != ref.Job || x.State.Terminal() {
		t.Fatalf("Execution = %+v, want %s", x, want)
	}
	// The region is the backend's own, so a caller never parses a Batch
	// name's six fields to find it (backend.Execution.Region, §5.2).
	if x.Region != testRegion {
		t.Errorf("Execution region = %q, want the backend's own %q", x.Region, testRegion)
	}
	// The machine type is the job's own, read off the job rather than off the
	// installation's configuration, so a profile whose machine type changes
	// does not silently re-price every run already recorded (design §5.2).
	if x.MachineType != testMachineType {
		t.Errorf("Execution machine type = %q, want the job's own %q", x.MachineType, testMachineType)
	}
	// The job's SCHEDULED time is still not read (design §2.3, §9), so the
	// window falls back to the create time and says so -- a window that
	// includes Batch's queue time, which errs high rather than low.
	if x.Scheduled != (time.Time{}) {
		t.Errorf("Execution scheduled = %v, want the zero time (Task 7 does not read it)", x.Scheduled)
	}
	if !strings.Contains(x.WindowNote(), "errs high") {
		t.Errorf("WindowNote = %q, want the create-time fallback named", x.WindowNote())
	}
	// The number form is the same run, not a second one.
	if x2, err := b.Execution(ctx, strings.Replace(want, "/projects/"+testProject+"/", "/projects/999/", 1)); err != nil || x2.Name != want {
		t.Fatalf("Execution(by number) = %+v, %v", x2, err)
	}
}

// TestLogsReadsOneJobsTaskLogsByUID pins the filter design §2.3 ships, and
// why it costs one jobs.get: labels.job_uid needs the job's UID, which only
// the job itself reports.
//
// UNVERIFIED, and it is the whole log surface of this backend: the filter's
// two clauses are the form Google documents for one job's task logs, and the
// fake answers that shape because it was written to (internal/gcpfake), not
// because a real entry was read. Nothing here proves that a real
// batch.googleapis.com/Job entry carries labels.job_uid, that the log's name
// is batch_task_logs, or that the sink's resource.type arm matches what the
// platform really writes. Check 57a step 10 reads a raw entry and settles
// all three (design §9); until then these tests hold the code to the
// documented shape, and that is all they claim.
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

// TestLogsTimeBounds pins the cut every read carries: without a Since it
// starts createdMargin before the job was created, and with one it uses the
// caller's own instant. It is what keeps a run's read from paging through
// every entry the project's batch_task_logs has ever held -- and what makes
// `fugaro logs --since` mean something.
func TestLogsTimeBounds(t *testing.T) {
	ctx := context.Background()
	b, bf, lf, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	x, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	uid, _ := bf.JobJSON(testProject, testRegion, ref.Job)["uid"].(string)
	// One entry an hour before the job was created, one a second after it.
	lf.AddUID(uid, gcpfake.LogEntry{Time: x.Created.Add(-time.Hour), Text: "before"})
	lf.AddUID(uid, gcpfake.LogEntry{Time: x.Created.Add(time.Second), Text: "after"})

	read := func(q backend.LogQuery) []string {
		t.Helper()
		var got []string
		if err := b.Logs(ctx, q, func(e backend.LogEntry) error { got = append(got, e.Message); return nil }); err != nil {
			t.Fatalf("Logs: %v", err)
		}
		return got
	}
	if got := read(backend.LogQuery{Execution: ref.Name}); len(got) != 1 || got[0] != "after" {
		t.Fatalf("Logs = %q, want only the entry after the job was created", got)
	}
	want := ` AND timestamp>="` + x.Created.Add(-createdMargin).UTC().Format(time.RFC3339Nano) + `"`
	if last := lastLogFilter(t, lf); !strings.HasSuffix(last, want) {
		t.Fatalf("filter = %q, want it to end with %s", last, want)
	}
	// An explicit Since is the caller's own cut, not the job's creation time:
	// the entry after the create is still before it, so nothing comes back.
	since := x.Created.Add(30 * time.Second)
	if got := read(backend.LogQuery{Execution: ref.Name, Since: since}); len(got) != 0 {
		t.Fatalf("Logs(Since) = %q, want nothing", got)
	}
	want = ` AND timestamp>="` + since.UTC().Format(time.RFC3339Nano) + `"`
	if last := lastLogFilter(t, lf); !strings.HasSuffix(last, want) {
		t.Fatalf("filter = %q, want it to end with %s", last, want)
	}
}

// lastLogFilter is the filter of the last entries.list request the fake
// answered.
func lastLogFilter(t *testing.T, lf *gcpfake.Logging) string {
	t.Helper()
	reqs := lf.Requests()
	if len(reqs) == 0 {
		t.Fatal("no entries.list request was sent")
	}
	var req struct{ Filter string }
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &req); err != nil {
		t.Fatalf("entries.list body: %v", err)
	}
	return req.Filter
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

// TestLogsRefusesAUIDTheFilterMustNotBeBuiltFrom: the filter is built by
// concatenation (design §2.3) and the UID is a value the API reports, so one
// carrying a quote, a space or a backslash could add clauses of its own --
// selecting another job's lines, or another resource type. Every such UID is
// refused before any request goes out, and the refusal does not echo the
// value back.
func TestLogsRefusesAUIDTheFilterMustNotBeBuiltFrom(t *testing.T) {
	ctx := context.Background()
	b, bf, lf, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, uid := range []string{
		`1" OR resource.type="cloud_run_job`,
		`1" AND labels.job_uid="2`,
		"1 2",
		`1\2`,
		"1\n2",
		strings.Repeat("9", 129),
	} {
		bf.SetUID(ref.Job, uid)
		err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(backend.LogEntry) error { return nil })
		if err == nil {
			t.Errorf("Logs read the task logs of a job whose UID is %q", uid)
			continue
		}
		if strings.Contains(err.Error(), uid) {
			t.Errorf("the refusal echoed a UID it refused: %v", err)
		}
	}
	if got := len(lf.Requests()); got != 0 {
		t.Fatalf("%d entries.list requests were sent for a UID that must be refused", got)
	}
	// The real shape -- what the API reports -- is read: the refusal is a
	// charset check, not a filter that finds nothing.
	bf.SetUID(ref.Job, "7123456789012345678")
	lf.AddUID("7123456789012345678", gcpfake.LogEntry{Time: time.Now(), Text: "hello"})
	var got []string
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(e backend.LogEntry) error {
		got = append(got, e.Message)
		return nil
	}); err != nil || len(got) != 1 {
		t.Fatalf("Logs = %q, %v", got, err)
	}
}

// TestLogsFollow: follow keeps reading a job's task logs by the UID it read
// once, emits an entry that arrives while it waits, never repeats one it has
// already emitted, and returns once the job has ended and its logs have
// settled. Poll and LogSettle are shrunk so the test does not wait them out.
func TestLogsFollow(t *testing.T) {
	ctx := context.Background()
	b, bf, lf, _ := newTestBackend(t, func(o *Options) { o.LogSettle = 50 * time.Millisecond })
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	uid, _ := bf.JobJSON(testProject, testRegion, ref.Job)["uid"].(string)
	lf.AddUID(uid, gcpfake.LogEntry{Time: time.Now(), Text: "first"})
	var got []string
	go func() {
		time.Sleep(30 * time.Millisecond)
		lf.AddUID(uid, gcpfake.LogEntry{Time: time.Now().Add(time.Second), Text: "second"})
		bf.SetState(ref.Job, "SUCCEEDED")
	}()
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	collect := func(e backend.LogEntry) error { got = append(got, e.Message); return nil }
	if err := b.Logs(fctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond}, collect); err != nil {
		t.Fatalf("Logs(Follow): %v", err)
	}
	// "first" is re-read by the lookback window and must not be emitted
	// twice: an entry the operator has already seen, printed again on every
	// poll, is the bug the insert IDs exist for.
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("followed = %q", got)
	}
}

// TestLogsFollowStopsOnContext: a job that never ends must not make follow
// hang. Ctrl-C and a closed pipe reach it through the context.
func TestLogsFollowStopsOnContext(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// A launched job is QUEUED and stays there: nothing else moves it.
	fctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	err = b.Logs(fctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond}, func(backend.LogEntry) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Logs(Follow) = %v, want the context's own error", err)
	}
}

// TestLogURL pins the link a run carries: a Logs Explorer query scoped to the
// installation's log view, and the time window the job's own createTime
// gives it. A run whose UID is unknown cannot be selected at all, so it
// answers "" rather than a link that opens the whole project's logs.
func TestLogURL(t *testing.T) {
	b, _, _, _ := newTestBackend(t, func(o *Options) {
		o.LogView = "projects/" + testProject + "/locations/global/buckets/fugaro/views/runs"
	})
	id := backend.ExecID{GCPProject: testProject, Region: testRegion, Job: "fugaro-acme-app-web-0123456789ab"}
	created := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	// The window starts createdMargin before the createTime, for clock skew
	// between Batch and the VM, and runs logMaxSpan from there.
	want := "https://console.cloud.google.com/logs/query;query=" +
		url.PathEscape(`labels.job_uid="uid-1" AND logName="projects/`+testProject+`/logs/batch_task_logs"`) +
		";storageScope=storage," + url.PathEscape("projects/"+testProject+"/locations/global/buckets/fugaro/views/runs") +
		";timeRange=2026-09-27T09:59:00Z/2026-09-28T09:59:00Z?project=" + testProject
	if got := b.logURL(id, "uid-1", created); got != want {
		t.Fatalf("logURL = %q\nwant      %q", got, want)
	}
	// A job whose createTime is unreadable has no window to open: the query
	// alone, never a range starting at the zero time.
	got := b.logURL(id, "uid-1", time.Time{})
	if strings.Contains(got, "timeRange=") || !strings.Contains(got, "batch_task_logs") {
		t.Fatalf("logURL without a createTime = %q", got)
	}
	if got := b.logURL(id, "", created); got != "" {
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

// --- Task 7: the job definition, the socket and the VM --------------------

// dockerSpec is the run every Task 7 test launches: the conformance suite's
// run, with the workflow's `docker: true` ask (design §4.2, C16).
func dockerSpec() backend.LaunchSpec {
	s := spec()
	s.Docker = true
	return s
}

// TestJobDefinitionIsAVMWithTheDesignsNetwork is Task 7's job JSON, field by
// field: a VM of the installation's machine type, booted from the preloaded
// image (the only fast-start path a Docker run has, C4), placed in both the
// named network and subnetwork with no external IP (C7 -- and both names are
// required, which Check 57a measured), and asking for a share of that machine
// type's quantities.
func TestJobDefinitionIsAVMWithTheDesignsNetwork(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	call := bf.Creates()[0]

	policy := object(t, call.Job, "allocationPolicy", "instances", "0", "policy")
	// The machine type is the installation's, and the boot disk is the
	// preloaded image: a run that pulled fugaro-base instead would spend about
	// 145 s on a cold start against the budget's 133 s ceiling, and image
	// streaming -- the other fast path -- cannot carry a socket (design §1.2).
	if policy["machineType"] != testMachineType {
		t.Errorf("machineType = %v, want %s", policy["machineType"], testMachineType)
	}
	boot := object(t, call.Job, "allocationPolicy", "instances", "0", "policy", "bootDisk")
	if boot["image"] != testBootImage {
		t.Errorf("bootDisk.image = %v, want the preloaded image %s", boot["image"], testBootImage)
	}
	if fmt.Sprint(boot["sizeGb"]) != "50" {
		t.Errorf("bootDisk.sizeGb = %v, want 50 (the default Check 57a's jobs carried)", boot["sizeGb"])
	}

	// Both names, always, and the run's VMs carry no external IP: Batch
	// refuses noExternalIpAddress without the pair, and the pair without the
	// field is an external IP, which C7 has the run without.
	network, subnetwork := testNetwork()
	ni := object(t, call.Job, "allocationPolicy", "network", "networkInterfaces", "0")
	if ni["network"] != network || ni["subnetwork"] != subnetwork {
		t.Errorf("networkInterfaces[0] = network %v, subnetwork %v; want %s, %s", ni["network"], ni["subnetwork"], network, subnetwork)
	}
	if ni["noExternalIpAddress"] != true {
		t.Errorf("noExternalIpAddress = %v, want true (C7)", ni["noExternalIpAddress"])
	}

	// The task asks for a share of the machine type, not the whole of it: the
	// VM it lands on also runs the OS, the Batch agent and the Docker daemon,
	// and a request for the whole type leaves nothing for them and sits
	// QUEUED. 2000m/4096MiB on e2-standard-4 is what Check 57a scheduled with
	// (design §7.2), and it is half the vCPUs and a quarter of the memory.
	cr := object(t, call.Job, "taskGroups", "0", "taskSpec", "computeResource")
	if fmt.Sprint(cr["cpuMilli"]) != "2000" || fmt.Sprint(cr["memoryMib"]) != "4096" {
		t.Errorf("computeResource = %v, want e2-standard-4's 2000m/4096MiB (Check 57a's measured request)", cr)
	}
}

// TestDockerRunMountsTheSocketAndJoinsItsGroup is the socket recipe C2 takes,
// stated as the four things that make it work: the host's socket is mounted
// into the container, the container joins the socket's group by the gid the
// installation read off the VM (never a hard-coded one, and never the group's
// name -- there is no docker group inside the image), the agent's environment
// names it, and Batch's runAsNonRoot stays unset, because only that branch was
// measured and setting it can move the uid the recipe works for (design §2.2).
func TestDockerRunMountsTheSocketAndJoinsItsGroup(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if _, err := b.Launch(ctx, dockerSpec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	container := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec", "runnables", "0", "container")
	if got := container["volumes"]; !equalJSON(got, []any{"/var/run/docker.sock:/var/run/docker.sock"}) {
		t.Errorf("container.volumes = %v, want the socket bind mount", got)
	}
	if got := container["options"]; got != fmt.Sprintf("--group-add %d", testSocketGID) {
		t.Errorf("container.options = %v, want \"--group-add %d\" (the gid read off the VM)", got, testSocketGID)
	}
	// --privileged is not the recipe (it changes nothing for a uid-1000
	// process), and --user 0 is the fallback, not the choice: the uid must
	// stay the image's own fugaro user.
	if got := fmt.Sprint(container["options"]); strings.Contains(got, "--user") || strings.Contains(got, "--privileged") {
		t.Errorf("container.options = %v, want the group join and nothing else", got)
	}
	// Streaming would drop the options field altogether (measured, 57a-6), so a
	// Docker run must not turn it on: the field is `omitempty`, so "not on" is
	// the key being absent from the job, not the key reading false.
	// Hand mutation: set EnableImageStreaming unconditionally in run.go
	// (`container.EnableImageStreaming = true`), which the streaming refusal
	// does not catch because b.o.ImageStreaming is false; red.
	if on, ok := container["enableImageStreaming"]; ok {
		t.Errorf("container.enableImageStreaming = %v, want the key absent for a Docker run", on)
	}

	vars := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	if vars["DOCKER_HOST"] != "unix:///var/run/docker.sock" {
		t.Errorf("DOCKER_HOST = %v, want the mounted socket's path", vars["DOCKER_HOST"])
	}

	// runAsNonRoot must stay unset: only that branch was measured, and Batch
	// documents the true branch as running the runnables as an OS Login
	// non-root user, which could move the container's uid (design §2.2, §9).
	tg := object(t, bf.Creates()[0].Job, "taskGroups", "0")
	if v, ok := tg["runAsNonRoot"]; ok {
		t.Errorf("taskGroups[0].runAsNonRoot = %v, want the field absent", v)
	}
}

// TestDockerRunMountsTheConfiguredSocketPath: the socket's path is the
// installation's, not this package's (Docker.SocketPath, design §2.2). The
// mount and the run's DOCKER_HOST are both built from it, and neither may
// fall back to this package's default when one was configured -- a run whose
// daemon listens at /run/docker.sock would otherwise get a mount of a path
// that is not there and an address that is not its socket.
//
// Hand mutation: in run.go, use HostSocketPath instead of `d.path()` for the
// volume and for DOCKER_HOST (`p := HostSocketPath`). This is the review's
// M18, which stayed green while every test ran with the default path.
func TestDockerRunMountsTheConfiguredSocketPath(t *testing.T) {
	ctx := context.Background()
	const p = "/run/docker.sock"
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.Docker.SocketPath = p })
	if _, err := b.Launch(ctx, dockerSpec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	job := bf.Creates()[0].Job
	container := object(t, job, "taskGroups", "0", "taskSpec", "runnables", "0", "container")
	if got := container["volumes"]; !equalJSON(got, []any{p + ":" + p}) {
		t.Errorf("container.volumes = %v, want the configured socket %s:%s", got, p, p)
	}
	if got := object(t, job, "taskGroups", "0", "taskSpec", "environment", "variables")["DOCKER_HOST"]; got != "unix://"+p {
		t.Errorf("DOCKER_HOST = %v, want unix://%s", got, p)
	}
	// And the default is not also sent: one socket, one mount.
	if raw, err := json.Marshal(job); err != nil {
		t.Fatalf("marshal: %v", err)
	} else if strings.Contains(string(raw), HostSocketPath) {
		t.Errorf("the job carries this package's default socket path %s as well: %s", HostSocketPath, raw)
	}
}

// TestTheJobHasNoTaskEnvironments: a Batch task's environment comes from
// taskSpec.environment, and the task-level place (TaskGroup.taskEnvironments)
// is left empty on purpose. Check 57a-12 measured what a task-level
// environment does to the pair Batch injects -- a task-level BATCH_TASK_INDEX
// of 99 reached the container while BATCH_TASK_COUNT stayed 1 -- so a job of
// this backend never writes there: the run's identity is the job env, and the
// injected index is not a pair anything may depend on (design §2.5, §8.1
// Task 7).
//
// Hand mutation: add `TaskEnvironments: map[string]*batch.Environment{...}`
// to the TaskGroup in run.go with any variable in it. The review called the
// earlier version's silence on this M15, which stayed green.
func TestTheJobHasNoTaskEnvironments(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	job := bf.Creates()[0].Job
	tg := object(t, job, "taskGroups", "0")
	if v, ok := tg["taskEnvironments"]; ok && v != nil {
		t.Errorf("taskGroups[0].taskEnvironments = %v, want it absent: a task-level environment overrides the task index Batch injects (Check 57a-12)", v)
	}
	// The deprecated map beside it, which the API still accepts and which
	// would be the same override under an older name.
	if v, ok := object(t, job, "taskGroups", "0", "taskSpec")["environments"]; ok && v != nil {
		t.Errorf("taskSpec.environments = %v, want it absent (deprecated, and the same override)", v)
	}
	// The environment the job does carry is the task's own, and the run's
	// identity is in it.
	if got := object(t, job, "taskGroups", "0", "taskSpec", "environment", "variables")["FUGARO_RUN"]; got == nil {
		t.Errorf("the task's own environment is missing: %v", job)
	}
}

// TestLaunchRefusesASecretThatIsAlsoAJobVariable: the job's own variables are
// how the run is told which run, backend and project it is (§2.1, §2.5), and
// secretVariables resolve into the same environment on the VM. A name in both
// would leave which entry wins to the platform, so the launch is refused
// before anything is created rather than launched with a run whose identity
// may have come from a secret.
//
// Hand mutation: drop the collision loop in jobDefinition (leave
// `env.SecretVariables = b.o.Secrets` alone), which launches the job with
// FUGARO_RUN in both fields.
func TestLaunchRefusesASecretThatIsAlsoAJobVariable(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.Secrets = map[string]string{"FUGARO_RUN": testSecret}
	})
	_, err := b.Launch(ctx, spec())
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch(secret named FUGARO_RUN) = %v, want ErrRejected", err)
	}
	if !strings.Contains(err.Error(), "FUGARO_RUN") {
		t.Errorf("the refusal does not name the colliding variable: %v", err)
	}
	if len(bf.Creates()) != 0 {
		t.Fatalf("a refused launch still created %d job(s)", len(bf.Creates()))
	}
	// A secret that is not one of the job's own variables is unaffected.
	b2, bf2, _, _ := newTestBackend(t, func(o *Options) {
		o.Secrets = map[string]string{"ANTHROPIC_API_KEY": testSecret}
	})
	if _, err := b2.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch(a secret that collides with nothing): %v", err)
	}
	if got := object(t, bf2.Creates()[0].Job, "taskGroups", "0", "taskSpec", "environment", "secretVariables")["ANTHROPIC_API_KEY"]; got != testSecret {
		t.Errorf("secretVariables = %v", got)
	}
}

// TestLaunchCarriesTheSpecEnv: the launcher's per-execution variables (the
// budget marker) go into the task's own variables, which is the whole of a
// Batch task's environment on the VM (design §2.2). The marker is how a run
// tells a minted token from a project that published no policy (plan
// R2-8(a)), and a Batch backend that dropped it would leave every Batch run
// reading the token's absence as "nothing was minted".
//
// The map is also why the job's own names are reserved here and not only on
// Cloud Run: a later write to a map key is what the run would believe about
// itself, so an entry naming FUGARO_RUN (the run's own identity, which
// jobDefinition writes for the same map), FUGARO_BACKEND or FUGARO_PROJECT is
// refused rather than applied — the same collision the secrets check below
// refuses, for the same reason. The platform's own five are reserved beside
// them (backend.ReservedEnvNames, which is the same list Cloud Run asks), so
// the two backends refuse one set of launch entries rather than each its own.
func TestLaunchCarriesTheSpecEnv(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	s.Env = []string{"FUGARO_BUDGET_TOKEN=1"}
	if _, err := b.Launch(ctx, s); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	vars := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	if vars["FUGARO_BUDGET_TOKEN"] != "1" {
		t.Errorf("the task's variables = %v, want the launch's own entry in them", vars)
	}
	// And the job's own identity is still the job's: the marker went in
	// beside it, not over it.
	if vars["FUGARO_RUN"] != backendtest.Slug+"/"+spec().RunID || vars["FUGARO_BACKEND"] != backend.CloudBatch {
		t.Errorf("the task's variables = %v, want the job's own identity intact", vars)
	}

	// An entry that is not NAME=value, and one naming a variable this job
	// already sets, are refused before the create: nothing was created, and
	// the refusal is definitive (ErrRejected) rather than ambiguous, so the
	// launcher drops its claim and the token it minted.
	//
	// The platform's own five are reserved here too, Cloud Run's list
	// (backend.ReservedEnvNames), and FUGARO_BUCKET is the one of them no
	// Batch job writes: an entry naming it would be the only writer of the
	// bucket a run puts its results in, which is what the same entry is
	// refused for on Cloud Run — where the job's environment already holds it
	// — rather than a name this backend happens not to set (the review
	// finding on this change).
	before := len(bf.Creates())
	for _, bad := range []string{
		"FUGARO_BUDGET_TOKEN", "=1", "FUGARO_RUN=acme-app/someone-elses-run",
		"FUGARO_PROJECT=someone-elses-project", "FUGARO_BUCKET=gs://someone-elses-bucket",
		"FUGARO_REGION=elsewhere",
	} {
		s.Env = []string{bad}
		if _, err := b.Launch(ctx, s); !errors.Is(err, backend.ErrRejected) {
			t.Fatalf("Launch with Env %q = %v, want ErrRejected", bad, err)
		}
	}
	// A Docker run's own socket variable is one of them, because that block
	// writes it: the launch merge is after it, so the path the installation
	// configured is the one that stays.
	s.Docker, s.Env = true, []string{"DOCKER_HOST=unix:///tmp/other.sock"}
	if _, err := b.Launch(ctx, s); !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch overriding a Docker run's DOCKER_HOST = %v, want ErrRejected", err)
	}
	if got := len(bf.Creates()); got != before {
		t.Fatalf("a refused launch still created %d job(s)", got-before)
	}
}

// TestARunWithoutDockerGetsNoSocket scopes the socket to the workflows that
// asked for it (design §4.2, §4.4's last check): a run whose workflow did not
// ask for Docker gets no mount, no group and no DOCKER_HOST, while still
// being a VM of the same shape.
func TestARunWithoutDockerGetsNoSocket(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	job := bf.Creates()[0].Job
	container := object(t, job, "taskGroups", "0", "taskSpec", "runnables", "0", "container")
	if got, ok := container["volumes"]; ok {
		t.Errorf("container.volumes = %v, want none without docker: true", got)
	}
	if got, ok := container["options"]; ok {
		t.Errorf("container.options = %v, want none without docker: true", got)
	}
	vars := object(t, job, "taskGroups", "0", "taskSpec", "environment", "variables")
	if got, ok := vars["DOCKER_HOST"]; ok {
		t.Errorf("DOCKER_HOST = %v, want none without docker: true", got)
	}
	// The VM itself is unchanged: every run on this backend is a VM.
	if got := object(t, job, "allocationPolicy", "instances", "0", "policy")["machineType"]; got != testMachineType {
		t.Errorf("machineType = %v, want %s", got, testMachineType)
	}
}

// TestLaunchRefusesDockerWithoutASocketGID: the gid is read off the VM, so an
// installation that has not read it has nothing to join, and a run that asked
// for Docker is refused before anything is created rather than launched with a
// socket it cannot open (or with a guessed 412 that belongs to somebody else's
// boot image).
func TestLaunchRefusesDockerWithoutASocketGID(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.Docker = Docker{} })
	_, err := b.Launch(ctx, dockerSpec())
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch(docker without a gid) = %v, want ErrRejected", err)
	}
	if len(bf.Creates()) != 0 {
		t.Fatalf("a refused launch still created %d job(s)", len(bf.Creates()))
	}
	// A run that did not ask for Docker is unaffected: the socket is what it
	// needs the gid for.
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch(no docker): %v", err)
	}
}

// TestLaunchRefusesDockerWithImageStreaming: streaming makes Batch ignore the
// container's options field -- measured on 2026-10-15, and the fact C2 and C4
// rest on -- so the two are mutually exclusive and the combination is refused
// rather than launched with a socket that silently is not there (design §1.1,
// §1.2, §8.1 Task 7).
func TestLaunchRefusesDockerWithImageStreaming(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.ImageStreaming = true })
	_, err := b.Launch(ctx, dockerSpec())
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch(docker + streaming) = %v, want ErrRejected", err)
	}
	if len(bf.Creates()) != 0 {
		t.Fatalf("a refused launch still created %d job(s)", len(bf.Creates()))
	}
	// A run without Docker may stream: it has no socket to lose.
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch(no docker, streaming): %v", err)
	}
	if got, ok := object(t, bf.Creates()[0].Job, "taskGroups", "0", "taskSpec", "runnables", "0", "container")["enableImageStreaming"]; !ok || got != true {
		t.Errorf("enableImageStreaming = %v (present: %v), want true for a streamed run", got, ok)
	}
}

// TestNewRefusesAnIncompleteVMConfiguration: every field Task 7's job JSON
// needs is required rather than defaulted into something wrong -- a machine
// type the price table cannot price, a boot image nobody built (the preload is
// C4's decision, not an optimisation), a network the job cannot be placed in,
// and a socket gid that is not one.
func TestNewRefusesAnIncompleteVMConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tweak func(*Options)
	}{
		{"an unknown machine type", func(o *Options) { o.MachineType = "n2-standard-96" }},
		{"no boot image", func(o *Options) { o.BootImage = "" }},
		{"no network", func(o *Options) { o.Network = "" }},
		{"no subnetwork", func(o *Options) { o.Subnetwork = "" }},
		{"a network that is not a network name", func(o *Options) { o.Network = "default" }},
		{"a subnetwork that is not a subnetwork name", func(o *Options) { o.Subnetwork = "default" }},
		{"a subnetwork in another region", func(o *Options) {
			o.Subnetwork = "projects/" + testProject + "/regions/elsewhere/subnetworks/default"
		}},
		{"a socket gid that is not one", func(o *Options) { o.Docker = Docker{SocketGID: -1} }},
		{"a socket path with no gid", func(o *Options) { o.Docker = Docker{SocketPath: "/run/docker.sock"} }},
		{"a socket path that is not absolute", func(o *Options) {
			o.Docker = Docker{SocketGID: testSocketGID, SocketPath: "run/docker.sock"}
		}},
		// The mount is `<path>:<path>`, so a path that is the root directory
		// hands the run the whole filesystem of the machine that has the
		// daemon, and a path that is not the daemon's socket is a bind of
		// nothing the run can talk to (design §2.2).
		{"a socket path that is the root directory", func(o *Options) {
			o.Docker = Docker{SocketGID: testSocketGID, SocketPath: "/"}
		}},
		{"a socket path that is a directory", func(o *Options) {
			o.Docker = Docker{SocketGID: testSocketGID, SocketPath: "/var/run"}
		}},
		{"a socket path that is not docker.sock", func(o *Options) {
			o.Docker = Docker{SocketGID: testSocketGID, SocketPath: "/var/run/fugaro.sock"}
		}},
		{"a socket path that is not clean", func(o *Options) {
			o.Docker = Docker{SocketGID: testSocketGID, SocketPath: "/var/run/../run/docker.sock/"}
		}},
		{"a negative boot disk size", func(o *Options) { o.BootDiskGB = -1 }},
		// A secret is a NAME on both sides of the map: the value slot is the
		// resource name the VM resolves, the key is the environment variable.
		{"a secret value where a name belongs", func(o *Options) {
			o.Secrets = map[string]string{"ANTHROPIC_API_KEY": "sk-ant-not-a-real-secret-value"}
		}},
		{"a secret value as the variable name", func(o *Options) {
			o.Secrets = map[string]string{"sk-ant-not-a-real-secret-value": testSecret}
		}},
		{"a secret name that is not a resource name", func(o *Options) {
			o.Secrets = map[string]string{"ANTHROPIC_API_KEY": "fugaro-acme-app-anthropic-api-key"}
		}},
		{"a variable name with a dash in it", func(o *Options) {
			o.Secrets = map[string]string{"ANTHROPIC-API-KEY": testSecret}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := testOptions(t)
			tc.tweak(&o)
			if _, err := New(context.Background(), o); err == nil {
				t.Fatalf("New accepted %s", tc.name)
			}
		})
	}
	// The untweaked configuration is accepted, so each refusal above is about
	// its own field.
	if _, err := New(context.Background(), testOptions(t)); err != nil {
		t.Fatalf("New(refused a complete configuration): %v", err)
	}
	// And the forms that are right are not refused by a check written too
	// tightly: a secret named with a pinned version, and a socket path that is
	// not this package's default.
	o := testOptions(t)
	o.Secrets = map[string]string{
		"ANTHROPIC_API_KEY": testSecret + "/versions/7",
		"FUGARO_OTEL_TOKEN": "projects/" + testProject + "/secrets/fugaro-otel",
	}
	o.Docker.SocketPath = "/run/docker.sock"
	if _, err := New(context.Background(), o); err != nil {
		t.Fatalf("New(refused a valid secret name or socket path): %v", err)
	}
}

// TestTheVMDefaultsAreTheDesignsOwn: the two fields an installation may leave
// unset are the design's own values -- the e2-standard-4 the design names as
// the likely default, which every price and size in prices.go is carried for,
// and the 50 GB disk Check 57a's jobs carried (design §1.3, §7.2) -- and they
// are defaults, not silent zeros: a job whose computeResource and boot disk
// were zero would be a VM of the wrong shape or no VM at all.
func TestTheVMDefaultsAreTheDesignsOwn(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.MachineType, o.BootDiskGB = "", 0 })
	if defaultMachineType != "e2-standard-4" {
		t.Errorf("defaultMachineType = %q, want the design's e2-standard-4 (§1.3)", defaultMachineType)
	}
	if defaultBootDiskGB != 50 {
		t.Errorf("defaultBootDiskGB = %d, want the 50 Check 57a's jobs carried (§7.2)", defaultBootDiskGB)
	}
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	policy := object(t, bf.Creates()[0].Job, "allocationPolicy", "instances", "0", "policy")
	if policy["machineType"] != defaultMachineType {
		t.Errorf("machineType = %v, want the default %s", policy["machineType"], defaultMachineType)
	}
	if got := object(t, policy, "bootDisk")["sizeGb"]; fmt.Sprint(got) != fmt.Sprint(defaultBootDiskGB) {
		t.Errorf("bootDisk.sizeGb = %v, want the default %d", got, defaultBootDiskGB)
	}
	// The execution reports the same type, and it prices: the machine type's
	// quantities are what a run of it is billed (design §5.2).
	if x, err := b.Execution(ctx, ref.Name); err != nil {
		t.Fatalf("Execution: %v", err)
	} else if x.MachineType != defaultMachineType {
		t.Errorf("Execution machine type = %q, want the default %s", x.MachineType, defaultMachineType)
	}
}

// TestExecutionReportsTheJobsOwnMachineType: the machine type is a fact about
// the execution, so it is read off the job that ran, not off the backend's
// current Options. An operator who changes the profile's machine type -- a
// bigger type for a heavier workflow, say -- must not silently re-price every
// run already recorded at the new type's quantities (design §5.2), and a job's
// record outlives the change by 60 days (§2.4).
func TestExecutionReportsTheJobsOwnMachineType(t *testing.T) {
	ctx := context.Background()
	b, _, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// The profile changes under the recorded run.
	b.o.MachineType = "c2-standard-8"
	x, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	if x.MachineType != testMachineType {
		t.Errorf("Execution machine type = %q after the profile changed, want the job's own %q", x.MachineType, testMachineType)
	}
}

// TestAJobThatNamesNoMachineTypeFallsBackToTheConfiguration: MachineType is a
// fact about the execution, read off the job so that a later change of the
// installation's profile cannot silently re-price runs already recorded
// (design §5.2, and TestExecutionReportsTheJobsOwnMachineType). This
// backend's own jobs always name one -- checkVM requires a machine type and
// jobDefinition writes it into the job -- so a job that names none is one this
// backend did not build, or one built before the field was sent. Reading it
// must then fall back to the installation's configured type: an empty
// MachineType is how backend.Execution says "not a VM", and a VM-backed run
// read as one would be priced on Cloud Run's path instead of on the machine
// type it really is.
//
// The bodies below are the four shapes that reach the fallback, one per guard
// in jobMachineType plus the empty policy.
//
// Hand mutation: drop the fallback in toExecution (leave `x.MachineType =
// jobMachineType(j)` with no second statement); every case here goes red.
func TestAJobThatNamesNoMachineTypeFallsBackToTheConfiguration(t *testing.T) {
	ctx := context.Background()
	const configured = "c2-standard-8"
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.MachineType = configured })
	for name, tc := range map[string]struct{ id, body string }{
		"no allocation policy":          {"20260927-100000-a001", `{"taskGroups":[{"taskCount":"1"}]}`},
		"no instances":                  {"20260927-100000-a002", `{"allocationPolicy":{"instances":[]}}`},
		"a null instance":               {"20260927-100000-a003", `{"allocationPolicy":{"instances":[null]}}`},
		"an instance with no policy":    {"20260927-100000-a004", `{"allocationPolicy":{"instances":[{}]}}`},
		"a policy with no machine type": {"20260927-100000-a005", `{"allocationPolicy":{"instances":[{"policy":{}}]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			id := JobID(backendtest.Slug, backendtest.Workflow, tc.id)
			postRawJob(t, bf, id, tc.body)
			x, err := b.Execution(ctx, "projects/"+testProject+"/locations/"+testRegion+"/jobs/"+id)
			if err != nil {
				t.Fatalf("Execution: %v", err)
			}
			if x.MachineType != configured {
				t.Errorf("MachineType = %q, want the configured %s: a job that names none must not read as a non-VM execution", x.MachineType, configured)
			}
		})
	}
}

// TestAFinishedBatchRunHasNoWindowToPrice: a Batch job's record outlives the
// run by 60 days (design §2.4) and this backend reads no completion time
// (Started and Completed are left zero: the job's status events are
// UNVERIFIED, §2.3). So a finished run's window must be unknown -- "compute
// not estimated" -- and must not grow with every read, which is what pricing
// it from `now` would do: one hour, one day and ten days after the same job
// ended would be three different estimates for one finished run.
func TestAFinishedBatchRunHasNoWindowToPrice(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	ref, err := b.Launch(ctx, spec())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bf.SetState(ref.Job, "SUCCEEDED")
	x, err := b.Execution(ctx, ref.Name)
	if err != nil {
		t.Fatalf("Execution: %v", err)
	}
	if !x.State.Terminal() {
		t.Fatalf("Execution state = %q, want a terminal one", x.State)
	}
	if x.MachineType == "" || x.Created.IsZero() {
		t.Fatalf("Execution = %+v: the machine type and create time are what a window would be built from", x)
	}
	if !x.Completed.IsZero() {
		t.Fatalf("Completed = %v, want the zero time (this backend reads none)", x.Completed)
	}
	for _, d := range []time.Duration{time.Hour, 24 * time.Hour, 240 * time.Hour} {
		if got, ok := x.BilledWindow(x.Created.Add(d)); ok {
			t.Errorf("%v after the job ended, BilledWindow = %v, true; want unknown, never a figure that grows", d, got)
		}
	}
	// A running run is the opposite case and is priced: its VM really is
	// billing, and the window says where it started (errs high).
	bf.SetState(ref.Job, "RUNNING")
	if y, err := b.Execution(ctx, ref.Name); err != nil {
		t.Fatalf("Execution: %v", err)
	} else if d, ok := y.BilledWindow(y.Created.Add(time.Hour)); !ok || d != time.Hour {
		t.Errorf("a running job's window = %v, %v; want the hour since its creation, known", d, ok)
	}
}

// TestTheTaskLeavesTheVMHeadroom: computeResource is a share of the machine
// type, never the whole of it, on every type this build can price. Batch
// places a task on a VM that can hold what it asks for, and that VM is also
// running the OS, the Batch agent and the Docker daemon (design §1.3, §2.2):
// a request for the whole type is a job that sits QUEUED, which is the failure
// this leaves room against. The shares themselves are Check 57a's measured
// request -- 2000m/4096MiB on e2-standard-4, half the vCPUs and a quarter of
// the memory (§7.2) -- and Check 57b-9 is what measures whether they can be
// raised.
func TestTheTaskLeavesTheVMHeadroom(t *testing.T) {
	for _, name := range KnownMachineTypes() {
		mt, err := MachineTypeOf(name)
		if err != nil {
			t.Fatalf("MachineTypeOf(%s): %v", name, err)
		}
		got := taskResources(mt)
		fullCPU, fullMem := int64(mt.VCPU)*1000, int64(mt.MemoryGiB)*1024
		if want := fullCPU / 2; got.CpuMilli != want {
			t.Errorf("%s: cpuMilli = %d, want half the type's %d = %d", name, got.CpuMilli, fullCPU, want)
		}
		if want := fullMem / 4; got.MemoryMib != want {
			t.Errorf("%s: memoryMib = %d, want a quarter of the type's %d = %d", name, got.MemoryMib, fullMem, want)
		}
		if got.CpuMilli >= fullCPU || got.MemoryMib >= fullMem {
			t.Errorf("%s: the task asked for the whole machine type (%+v)", name, got)
		}
		if got.CpuMilli <= 0 || got.MemoryMib <= 0 {
			t.Errorf("%s: the task asked for nothing (%+v)", name, got)
		}
	}
	// The design's own probe request, on the design's own type: the numbers
	// above are not invented, they are what Check 57a submitted.
	mt, err := MachineTypeOf("e2-standard-4")
	if err != nil {
		t.Fatal(err)
	}
	if got := taskResources(mt); got.CpuMilli != 2000 || got.MemoryMib != 4096 {
		t.Errorf("e2-standard-4 = %+v, want Check 57a's 2000m/4096MiB", got)
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

// TestLaunchRefusesASecretEnv is the Batch half of the seal's carrier
// (docs/design/budget-token-channel.md, the Slice B note): a Batch job has no
// per-execution override, so the only place a launch's variable can go is the
// job definition's plain variables, which every launcher (batch.jobsEditor)
// and the reaper (batch.jobs.get) read back with `gcloud batch jobs describe`
// for as long as the job is kept — a standing copy, not the Cloud Run
// override the design prices. So
// a launch carrying a secret entry (the budget seal key) is refused before
// the create, definitively (ErrRejected, so the launcher drops its claim and
// its sealed object), and the refusal does not quote the value. Batch carries
// a sealed budget run only once a mechanism the design allows exists (an
// open owner question).
//
// Mutation (run, restored): let jobDefinition merge secret entries into vars
// like plain ones — the create happens and this test fails.
func TestLaunchRefusesASecretEnv(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	v := strings.Repeat("k", 43)
	s.SecretEnv = []string{"FUGARO_BUDGET_TOKEN=" + v}
	_, err := b.Launch(ctx, s)
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch with a secret entry = %v, want ErrRejected", err)
	}
	if strings.Contains(err.Error(), v) {
		t.Fatalf("the refusal quotes the secret's value: %v", err)
	}
	if n := len(bf.Creates()); n != 0 {
		t.Fatalf("a refused launch created %d job(s)", n)
	}
}
