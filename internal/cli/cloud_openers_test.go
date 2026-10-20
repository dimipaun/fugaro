package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The seam's second implementation is opened from an installation's own facts
// (plan 2026-10-19-compute-backend.md Task 8 step 1). Nothing selects it --
// internal/localcfg refuses `backend: cloud-batch` until the enable task -- so
// these tests open the opener map directly, by name, and hold the two things
// that make the registration worth having: the map keeps backend.Open's
// contract, and the options the Batch entry builds are the project config's.

// batchRig is a project config whose Batch backend is opened against the Cloud
// Batch and Cloud Logging fakes, plus the fake itself. gid is the recorded
// socket gid; 0 is the state of an installation that has none (which is what
// localcfg.Batch writes when the key is left out).
type batchRig struct {
	lc *localcfg.Config
	bf *gcpfake.Batch
	lf *gcpfake.Logging
}

func newBatchRig(t *testing.T, gid int) *batchRig {
	t.Helper()
	rig := &batchRig{bf: gcpfake.NewBatch(t), lf: gcpfake.NewLogging(t)}
	rig.bf.Project, rig.bf.Region = "proj-1234", "us-east5"
	gidLine := ""
	if gid > 0 {
		gidLine = fmt.Sprintf("  socket_gid: %d\n", gid)
	}
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		"endpoints: { batch: " + rig.bf.URL + "/, logging: " + rig.lf.URL + "/, no_auth: true }\n" +
		"batch:\n" +
		"  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n" +
		"  machine_type: e2-standard-4\n" +
		"  boot_image: projects/proj-1234/global/images/family/fugaro-base\n" +
		"  network: projects/proj-1234/global/networks/fugaro\n" +
		"  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro\n" +
		"  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com\n" +
		"  secrets:\n    ANTHROPIC_API_KEY: projects/proj-1234/secrets/anthropic-api-key\n" +
		gidLine +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	lc, err := localcfg.Parse([]byte(cfg))
	if err != nil {
		t.Fatalf("the rig's own project config does not parse: %v", err)
	}
	rig.lc = lc
	return rig
}

// openers is the map openCloudFor builds for this rig, with the Cloud Run
// entry pointed at the run fake (which the tests that open it need).
func (r *batchRig) openers(runOpts gcp.Options) map[string]backend.Opener {
	return cloudOpeners(r.lc, runOpts)
}

// batchBackend is the Batch backend this rig opens.
func (r *batchRig) batchBackend(t *testing.T) backend.Backend {
	t.Helper()
	be, err := backend.Open(context.Background(), backend.CloudBatch, r.openers(gcp.Options{}))
	if err != nil {
		t.Fatalf("opening the Batch backend the rig's config describes: %v", err)
	}
	return be
}

// TestCloudOpenersHoldBackendOpensContract: the plan's first test. A known
// name opens, an unset name opens Cloud Run, and an unknown name fails closed
// naming the names there are -- it must never fall back to Cloud Run, because
// a fallback is a project whose backend is not what it wrote running somewhere
// the operator did not choose.
func TestCloudOpenersHoldBackendOpensContract(t *testing.T) {
	ctx := context.Background()
	run := gcpfake.NewRun(t)
	run.Project, run.Region = "proj-1234", "us-east5"
	rig := newBatchRig(t, 412)
	openers := rig.openers(gcp.Options{GCPProject: rig.lc.GCPProject, Region: rig.lc.Region,
		Endpoints: gcp.Endpoints{Run: run.URL + "/", NoAuth: true}})

	// The installation's own answer: no name in the config is Cloud Run.
	if got := rig.lc.Backend(); got != backend.CloudRun {
		t.Fatalf("a config that names no backend selects %q, want %s", got, backend.CloudRun)
	}
	for _, name := range []string{"", backend.CloudRun, backend.CloudBatch} {
		want := backend.CloudRun
		if name == backend.CloudBatch {
			want = backend.CloudBatch
		}
		be, err := backend.Open(ctx, rig.lc.Backend(), openers)
		if name != "" {
			be, err = backend.Open(ctx, name, openers)
		}
		if err != nil {
			t.Fatalf("opening %q: %v", name, err)
		}
		if got := be.Codec().Backend(); got != want {
			t.Errorf("opening %q gave the %s backend, want %s", name, got, want)
		}
	}
	// The two names an installation can select, and no third: Local is a
	// backend name this build knows (backend.Local, the invocation's own),
	// and it is not one a project config selects.
	if len(openers) != 2 {
		t.Errorf("the opener map has %d entries, want the two selectable backends", len(openers))
	}
	if _, ok := openers[backend.Local]; ok {
		t.Error("the opener map carries the local invocation, which is never a project's selection")
	}
	// An unknown name fails closed, with the ones there are in the message and
	// no backend returned.
	be, err := backend.Open(ctx, "ecs-fargate", openers)
	if err == nil || be != nil {
		t.Fatalf("an unknown backend name opened %#v, want a refusal", be)
	}
	for _, want := range []string{backend.CloudRun, backend.CloudBatch, "ecs-fargate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
}

// TestBatchOptionsAreTheProjectConfigs: every field the config can set reaches
// batch.Options, because a field the opener drops is a run launched with a
// default rather than with the installation's own fact.
func TestBatchOptionsAreTheProjectConfigs(t *testing.T) {
	rig := newBatchRig(t, 412)
	o := batchOptions(rig.lc)
	for _, f := range []struct{ name, got, want string }{
		{"GCPProject", o.GCPProject, "proj-1234"},
		{"Region", o.Region, "us-east5"},
		{"FugaroProject", o.FugaroProject, "aurora"},
		{"Image", o.Image, "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1"},
		{"MachineType", o.MachineType, "e2-standard-4"},
		{"BootImage", o.BootImage, "projects/proj-1234/global/images/family/fugaro-base"},
		{"Network", o.Network, "projects/proj-1234/global/networks/fugaro"},
		{"Subnetwork", o.Subnetwork, "projects/proj-1234/regions/us-east5/subnetworks/fugaro"},
		{"ServiceAccount", o.ServiceAccount, "fugaro-runs@proj-1234.iam.gserviceaccount.com"},
		{"LogView", o.LogView, rig.lc.LogView},
		{"Endpoints.Batch", o.Endpoints.Batch, rig.bf.URL + "/"},
		{"Endpoints.Logging", o.Endpoints.Logging, rig.lf.URL + "/"},
	} {
		if f.got != f.want {
			t.Errorf("batchOptions().%s = %q, want %q", f.name, f.got, f.want)
		}
	}
	if o.Docker.SocketGID != 412 {
		t.Errorf("batchOptions().Docker.SocketGID = %d, want the recorded 412", o.Docker.SocketGID)
	}
	if _, ok := o.Secrets["ANTHROPIC_API_KEY"]; !ok || len(o.Secrets) != 1 {
		t.Errorf("batchOptions().Secrets = %v, want the one the config names", o.Secrets)
	}
	if !o.Endpoints.NoAuth {
		t.Error("batchOptions() drops endpoints.no_auth, which is what points a test at a fake")
	}
	if o.Timeouts == nil {
		t.Fatal("batchOptions() leaves Timeouts nil: batch.New refuses a backend with no timeout source")
	}
	// An installation that has not been provisioned yet (no batch block at
	// all) gets the config's own facts and no invented ones: the empty fields
	// are what batch.New refuses, which is the right failure for a project
	// nothing has provisioned.
	plain, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\nrepos:\n  acme/app: { provider: github, workflows: [web] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if o := batchOptions(plain); o.Image != "" || o.ServiceAccount != "" || o.Network != "" || o.Docker.SocketGID != 0 {
		t.Errorf("an unprovisioned installation's options = %+v, want only its own identity", o)
	}
}

// TestBatchTimeoutsAreTheInstallationsBound: on Batch there is no deployed job
// to read a workflow's timeout back from, so the timeouts are a config read
// (design §2.3). What this installation answers with is backend.MaxRunTime --
// the longest any run may legitimately be active -- unless the operator wrote
// batch.task_timeout, and it answers the same way for every workflow: a bound
// shorter than a workflow's own would truncate a run that was doing nothing
// wrong.
func TestBatchTimeoutsAreTheInstallationsBound(t *testing.T) {
	rig := newBatchRig(t, 412)
	o := batchOptions(rig.lc)
	ctx := context.Background()
	b, err := o.Timeouts.TaskTimeout("acme-app", "web")
	if err != nil {
		t.Fatal(err)
	}
	if b != backend.MaxRunTime {
		t.Errorf("TaskTimeout with no batch.task_timeout = %v, want backend.MaxRunTime (%v)", b, backend.MaxRunTime)
	}
	longest, err := o.Timeouts.LongestTaskTimeout()
	if err != nil {
		t.Fatal(err)
	}
	if longest != b {
		t.Errorf("LongestTaskTimeout = %v and TaskTimeout = %v: every workflow gets the same bound, so they are one answer", longest, b)
	}
	// The backend asks this interface through its own two methods, so the same
	// bound is what a launch and a listing see.
	be := rig.batchBackend(t)
	if got, err := be.TaskTimeout(ctx, backendtest.Slug, backendtest.Workflow); err != nil || got != b {
		t.Errorf("the opened backend's TaskTimeout = %v, %v, want %v", got, err, b)
	}
	if got, err := be.LongestTaskTimeout(ctx); err != nil || got != b {
		t.Errorf("the opened backend's LongestTaskTimeout = %v, %v, want %v", got, err, b)
	}
	// A written timeout wins, because it is the operator's own answer.
	withTimeout, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		"batch:\n  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n  task_timeout: 45m\n" +
		"repos:\n  acme/app: { provider: github, workflows: [web] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	written := batchTimeouts{lc: withTimeout}
	if got, err := written.TaskTimeout("acme-app", "web"); err != nil || got != 45*time.Minute {
		t.Errorf("TaskTimeout with batch.task_timeout: 45m = %v, %v, want 45m", got, err)
	}
}

// TestDockerRunNeedsARecordedGid is the plan's red-step test: an installation
// whose gid is unrecorded refuses a Docker workflow, and accepts it once one is
// recorded. The recorded gid is what opens the socket -- the recipe is
// `--group-add <gid>` -- so a run launched without it would mount a socket its
// container cannot open, which is a run that fails on the VM after the launcher
// reported it started.
func TestDockerRunNeedsARecordedGid(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		gid     int
		created bool
	}{
		{"no gid recorded", 0, false},
		{"gid recorded", 412, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newBatchRig(t, tc.gid)
			be := rig.batchBackend(t)
			spec := backend.LaunchSpec{
				Repo:     backend.RepoRef{Repo: backendtest.Repo, Slug: backendtest.Slug},
				Workflow: backendtest.Workflow,
				RunID:    "20260919-120000-abcd",
				Docker:   true,
			}
			_, err := be.Launch(ctx, spec)
			if !tc.created {
				if err == nil {
					t.Fatal("a Docker run launched on an installation with no socket gid recorded")
				}
				if !strings.Contains(err.Error(), "no Docker socket gid recorded") {
					t.Fatalf("Launch = %v, want the refusal that names the missing gid", err)
				}
				if n := len(rig.bf.Creates()); n != 0 {
					t.Errorf("%d jobs were created for a Docker run this installation cannot open a socket for", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("Launch with a recorded gid: %v", err)
			}
			creates := rig.bf.Creates()
			if len(creates) != 1 {
				t.Fatalf("%d jobs created, want 1", len(creates))
			}
			// The gid reaches the job as the group join and the socket as the
			// mount: the two together are what the run's docker commands need.
			job, err := json.Marshal(creates[0].Job)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"--group-add 412", batch.HostSocketPath + ":" + batch.HostSocketPath} {
				if !strings.Contains(string(job), want) {
					t.Errorf("the created job does not carry %q", want)
				}
			}
		})
	}
}

// TestLaunchRecordNamesTheBackendTheLaunchRanOn is the plan's second red-step
// test, and it is in two halves because the two write sites (internal/cli/run.go:
// the launch backfilled from result.json, and the one written after a fresh
// launch) reach a backend in different ways.
//
// The first half is the backfill: existingLaunch is called with the Batch
// backend the opener built, on a run whose result.json records a Batch job, and
// the record it writes must say cloud-batch. That is a backend no installation
// can select today, so it is the only place this write site can be exercised
// against a platform that is not Cloud Run.
func TestLaunchRecordNamesTheBackendTheLaunchRanOn(t *testing.T) {
	ctx := context.Background()
	rig := newBatchRig(t, 412)
	be := rig.batchBackend(t)
	const runID = "20260919-120000-abcd"
	spec := backend.LaunchSpec{
		Repo:     backend.RepoRef{Repo: backendtest.Repo, Slug: backendtest.Slug},
		Workflow: backendtest.Workflow,
		RunID:    runID,
	}
	ref, err := be.Launch(ctx, spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	bucket, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	s := runstore.Open(bucket.Bucket, backendtest.Slug, runID)
	// The runner's own record: the CLI that launched died before writing
	// launch.json, which is what makes existingLaunch backfill one.
	if err := s.WriteRecord(ctx, &runstore.Record{
		Version: 1, RunID: runID, Repo: backendtest.Repo, Workflow: backendtest.Workflow,
		Execution: ref.Name, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	env := &cloudEnv{lc: rig.lc, bucket: bucket, be: be}
	lspec := &task.Spec{RunID: runID, Repo: backendtest.Repo, Workflow: backendtest.Workflow}
	l, err := existingLaunch(ctx, env, s, lspec)
	if err != nil {
		t.Fatalf("existingLaunch: %v", err)
	}
	if l == nil {
		t.Fatal("existingLaunch backfilled nothing")
	}
	if l.Backend != backend.CloudBatch {
		t.Errorf("the backfilled launch records backend %q, want %s: the record must name the backend the run actually used", l.Backend, backend.CloudBatch)
	}
	if l.Execution != ref.Name {
		t.Errorf("the backfilled launch records execution %q, want %q", l.Execution, ref.Name)
	}

	// The second half is the fresh launch, whose write site is inside the run
	// command: the launcher names the backend it launched on. This asserts the
	// seam-derived value both sites use, for both platforms, and the guard
	// below holds the two sites to it.
	if got := launchBackend(be); got != backend.CloudBatch {
		t.Errorf("launchBackend(Batch backend) = %q, want %s", got, backend.CloudBatch)
	}
	run := gcpfake.NewRun(t)
	run.Project, run.Region = "proj-1234", "us-east5"
	cr, err := backend.Open(ctx, backend.CloudRun, cloudOpeners(rig.lc, gcp.Options{
		GCPProject: "proj-1234", Region: "us-east5",
		Endpoints: gcp.Endpoints{Run: run.URL + "/", NoAuth: true},
	}))
	if err != nil {
		t.Fatalf("opening the Cloud Run backend: %v", err)
	}
	if got := launchBackend(cr); got != backend.CloudRun {
		t.Errorf("launchBackend(Cloud Run backend) = %q, want %s", got, backend.CloudRun)
	}
}

// TestLaunchRecordsNeverNameAConstant: the two write sites carry the same
// call, and neither may be a constant. A record that names the wrong backend
// is a record fugaro logs, fugaro cancel, fugaro diagnose and the reaper all
// misread, so the value has to come from the backend the launch ran on -- and
// a test that ran only on Cloud Run could not tell a seam-derived name from
// the constant, which is why this reads the two sites instead.
//
// It is a source check, like the plan's own pin test
// (docs_compute_backend_test.go), and for the same reason: the sites are
// inside the run command, and the fixture every CLI test can build is Cloud
// Run.
func TestLaunchRecordsNeverNameAConstant(t *testing.T) {
	data, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	code := stripLineComments(string(data))
	if n := strings.Count(code, "Backend: launchBackend(env.be)"); n != 2 {
		t.Errorf("run.go writes a launch record's backend %d times, want the two the plan names (the backfill and the fresh launch): a site that no longer reads the backend it ran on is a record naming the wrong platform", n)
	}
	for _, bad := range []string{"Backend: backend.CloudRun", "Backend: backend.CloudBatch", "Backend: backend.Local"} {
		if strings.Contains(code, bad) {
			t.Errorf("run.go writes %q into a launch record: the name comes from the backend the launch ran on, never from a constant", bad)
		}
	}
}
