package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// reaperRig is one `fugaro reaper` fixture: the Batch and Compute Engine
// fakes its two passes read, and a project config on disk (selected with
// FUGARO_CONFIG) that names them.
//
// The job pass's endpoint comes from the config, as every cloud command's
// does; the instance pass's does not, because a project config has no Compute
// Engine endpoint on purpose (localcfg.Endpoints: the reaper's Compute root
// is the reaper job's own, not a CLI client's) -- so the rig sets the
// package's seam instead, which is the same value the command would use
// against the real API in every other respect.
type reaperRig struct {
	bf *gcpfake.Batch
	cf *gcpfake.Compute
}

// newReaperRig builds the rig. withBatch writes the batch: block; without it
// the project config is one of an installation that never provisioned Batch,
// which is what the command refuses.
func newReaperRig(t *testing.T, withBatch bool) *reaperRig {
	t.Helper()
	rig := &reaperRig{bf: gcpfake.NewBatch(t), cf: gcpfake.NewCompute(t)}
	rig.bf.Project, rig.bf.Region = "proj-1234", "us-east5"
	rig.cf.Project = "proj-1234"
	// The fake's zones are the reaper's region's: a VM of another zone is
	// refused by the region check, which is not what these tests are about.
	rig.cf.Zones = []string{"us-east5-a"}
	old := reaperComputeRoot
	reaperComputeRoot = rig.cf.URL + "/compute/v1/"
	t.Cleanup(func() { reaperComputeRoot = old })

	dir := t.TempDir()
	// Selection must see only this rig's config: the repository this test
	// runs in is itself a fugaro checkout (fugaro.yaml names project fugaro),
	// so without an isolated projects directory a developer's own config
	// could answer for it.
	isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		"endpoints: { batch: " + rig.bf.URL + "/, no_auth: true }\n"
	if withBatch {
		cfg += "batch:\n" +
			"  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n" +
			"  service_account: fugaro-batch@proj-1234.iam.gserviceaccount.com\n"
	}
	cfg += "repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	path := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	return rig
}

// theRun is the run the rig's job belongs to; otherRun is a run with no job
// at all, which is what makes its VM a leak the instance pass may delete (a
// job this pass deletes owns its run, so its VM is left to the pass after).
const (
	theRun   = "20260927-100000-abcd"
	otherRun = "20260927-100001-abcd"
)

// addQueuedJob creates the run's Batch job, in QUEUED, age old, exactly as the
// launcher's own create does: the label set in both label fields (which is
// what makes it reapable at all, design §2.4) and a status of QUEUED.
func (rig *reaperRig) addQueuedJob(t *testing.T, runID string, age time.Duration) string {
	t.Helper()
	id := batch.JobID("acme-app", "web", runID)
	labels, err := batch.Labels("acme-app", "web", runID)
	if err != nil {
		t.Fatalf("batch.Labels: %v", err)
	}
	job, alloc := map[string]string{}, map[string]string{}
	for k, v := range labels {
		job[k], alloc[k] = v, v
	}
	body, err := json.Marshal(map[string]any{
		"labels":           job,
		"allocationPolicy": map[string]any{"labels": alloc},
		"status":           map[string]any{"state": "QUEUED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	u := fmt.Sprintf("%s/v1/projects/proj-1234/locations/us-east5/jobs?jobId=%s", rig.bf.URL, id)
	resp, err := http.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: %s: %s", u, resp.Status, b)
	}
	// Older than the queue deadline, which is a run's own ceiling plus the
	// launcher's slack: the shape the queue deadline deletes.
	rig.bf.SetCreated(id, time.Now().Add(-age))
	return id
}

// addLeakedVM adds a VM of a run no job accounts for, past the age floor.
func (rig *reaperRig) addLeakedVM(t *testing.T, runID string, age time.Duration) string {
	t.Helper()
	labels, err := batch.Labels("acme-app", "web", runID)
	if err != nil {
		t.Fatalf("batch.Labels: %v", err)
	}
	name := "vm-" + runID
	rig.cf.AddInstanceCreated("us-east5-a", name, labels, time.Now().Add(-age))
	return "us-east5-a/" + name
}

// newReaperEnvRig is the rig of the reaper's own Cloud Run job: no project
// config file, no FUGARO_CONFIG, no FUGARO_PROJECT -- nothing but the two
// variables Terraform sets on the job (deploy/terraform/gcp/modules/compute/
// reaper.tf, from the module's project and region) and the fakes its two
// passes read.
func newReaperEnvRig(t *testing.T) *reaperRig {
	t.Helper()
	rig := &reaperRig{bf: gcpfake.NewBatch(t), cf: gcpfake.NewCompute(t)}
	rig.bf.Project, rig.bf.Region = "proj-1234", "us-east5"
	rig.cf.Project = "proj-1234"
	rig.cf.Zones = []string{"us-east5-a"}
	oldCompute, oldWiring := reaperComputeRoot, reaperEnvTest
	reaperComputeRoot = rig.cf.URL + "/compute/v1/"
	reaperEnvTest = &reaperEnvWiring{batch: rig.bf.URL + "/", noAuth: true}
	t.Cleanup(func() { reaperComputeRoot, reaperEnvTest = oldCompute, oldWiring })
	isolateProjects(t, t.TempDir())
	t.Setenv(envGCPProject, "proj-1234")
	t.Setenv(envRegion, "us-east5")
	// The rig IS the reaper's own Cloud Run job, so it carries the marker the
	// job sets: without it the pass is a dry run and deletes nothing
	// (TestReaperCommandFromAShellTakesNoAction is the other side of that).
	t.Setenv(envReaperJob, reaperJobMarker)
	return rig
}

// The ages the rig back-dates by, read off the deadlines the command's own
// config produces: a run's own override ceiling (backend.MaxRunTime) is longer
// than the batch block's absent task timeout, so the queue deadline is that
// ceiling plus the launcher's slack and the age floor is the queue deadline
// plus a run budget.
func reaperQueueDeadline() time.Duration {
	return max(backend.MaxRunTime, backend.MaxRunTime) + backend.TaskTimeoutSlack
}

func reaperMaxAge() time.Duration {
	return reaperQueueDeadline() + backend.MaxRunTime + backend.TaskTimeoutSlack + batch.ReapMargin
}

// TestReaperCommandRefusesWithoutBatchConfiguration: an installation that has
// no batch: block has nothing for the reaper to judge -- its deadlines are
// derived from that block's task timeouts -- so the command refuses before it
// reads anything from any API. It is the plan's own step 1: "it refuses to run
// at all when the project has no Batch configuration -- a sweeper that guesses
// what it is looking at is worse than one that does nothing".
func TestReaperCommandRefusesWithoutBatchConfiguration(t *testing.T) {
	rig := newReaperRig(t, false)
	// A job and a VM that a guessing sweeper would delete, so a command that
	// got past the refusal is caught doing it rather than passing quietly.
	rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)

	out, _, err := execute(t, "reaper", "--dry-run")
	if err == nil {
		t.Fatal("fugaro reaper ran in a project with no batch: block")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit code = %d, want %d (a user error: the config is what is missing)", code, ExitUserError)
	}
	for _, want := range []string{"aurora", "no Batch configuration", "batch: block"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want it to say %q", err, want)
		}
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing: the command refuses before it reaps", out)
	}
	if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
		t.Errorf("the refused command deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
	}
}

// TestReaperCommandPrintsTheReportAndActs: one pass, end to end -- the stuck
// job and the leaked VM of the fakes, the report on stdout, and the deletions
// the pass actually made. A dry run prints the same report with "would" on
// every line and deletes nothing.
func TestReaperCommandPrintsTheReportAndActs(t *testing.T) {
	for _, dry := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry-run=%v", dry), func(t *testing.T) {
			rig := newReaperRig(t, true)
			job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
			vm := rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)

			args := []string{"reaper"}
			if dry {
				args = append(args, "--dry-run")
			}
			out, _, err := execute(t, args...)
			if err != nil {
				t.Fatalf("fugaro reaper: %v", err)
			}
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			verb, reapVerb := "deleted", "reaped"
			if dry {
				verb, reapVerb = "would delete", "would reap"
			}
			// The headline first: the project and region the pass swept, and
			// the deadlines it judged by (internal/backend/batch's headline).
			loc := "projects/proj-1234/locations/us-east5"
			wantHead := reapVerb + " " + loc + ": queue deadline "
			if !strings.HasPrefix(lines[0], wantHead) {
				t.Errorf("first line = %q, want the headline %q...", lines[0], wantHead)
			}
			for _, want := range []string{
				verb + " job " + loc + "/jobs/" + job + " (",
				verb + " VM " + vm + " (",
			} {
				found := false
				for _, l := range lines {
					if strings.HasPrefix(l, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("the report %q has no line starting with %q", lines, want)
				}
			}
			if dry {
				if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
					t.Fatalf("a dry run deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
				}
				return
			}
			if got := rig.bf.Deletes(); len(got) != 1 || got[0] != job {
				t.Errorf("jobs deleted = %v, want [%s]", got, job)
			}
			if got := rig.cf.Deletes(); len(got) != 1 || got[0] != vm {
				t.Errorf("VMs deleted = %v, want [%s]", got, vm)
			}
		})
	}
}

// TestReaperCommandExitsNonZeroOnAnError: a pass that could not finish is not
// a clean project, so the command exits non-zero (remote, 2) -- and what the
// pass did before it failed is still printed, because the Cloud Run job's log
// is the only account of it anyone has.
func TestReaperCommandExitsNonZeroOnAnError(t *testing.T) {
	rig := newReaperRig(t, true)
	job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)
	// The job delete is answered, the instance delete is refused: the pass
	// stops with an error that has a completed action behind it.
	rig.cf.FailDeleteWith = http.StatusForbidden

	out, _, err := execute(t, "reaper")
	if err == nil {
		t.Fatal("fugaro reaper exited 0 on a pass whose instance delete was refused")
	}
	if code := ExitCode(err); code != ExitRemoteError {
		t.Errorf("exit code = %d, want %d (a remote failure: the API refused a delete)", code, ExitRemoteError)
	}
	if !strings.Contains(out, "deleted job projects/proj-1234/locations/us-east5/jobs/"+job) {
		t.Errorf("stdout = %q, want the action the pass completed before it failed", out)
	}
	if got := rig.bf.Deletes(); len(got) != 1 || got[0] != job {
		t.Errorf("jobs deleted = %v, want [%s]", got, job)
	}
}

// TestReaperCommandPrintsTheWholeRefusalLine: a refusal's REASON is the whole
// of what a reader needs from it -- the command's Long help calls the report
// the account of what the pass left alone and why, SECURITY.md calls it the
// audit trail, and the Cloud Run job's stdout is its only home -- so a line cut
// off part-way is a line that cannot be acted on. The command used to print
// every line through internal/pluginwire's Printable, whose cap is 200 runes
// (MaxPrintable), and these lines are long by construction: a refusal names the
// job's full resource name (project, region and job id) and then a sentence.
// The sibling scheduled command does not bound its lines at all (history.go
// prints through oneLine, safetext.OneLine), which is the shape this asserts.
//
// The test drives the one shape whose refusal reason is the longest in the
// engine -- a SCHEDULED job whose only job-level status event dates its queue
// wait (reap.go's refuseStaleStateEvent) -- and asserts the line EXACTLY, since
// a HasPrefix/Contains assertion passes on a line with its tail cut off. The
// last check is what keeps the test honest: the line must be longer than the
// cap, or it would pass with the cap still in place.
func TestReaperCommandPrintsTheWholeRefusalLine(t *testing.T) {
	rig := newReaperRig(t, true)
	// Created past the queue deadline (addQueuedJob's age), then moved into
	// SCHEDULED with the one job-level event the API leaves on it put back at
	// the creation time: the shape whose time in state cannot be read, so the
	// pass refuses it rather than deleting a run that may be live.
	job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	rig.bf.SetStateAt(job, "SCHEDULED", time.Now().Add(-time.Second))
	rig.bf.SetStatusEvents(job, map[string]any{
		"type": "STATUS_CHANGED", "description": "Job state is QUEUED",
		"eventTime": time.Now().Add(-(reaperQueueDeadline() + time.Hour)).Format(time.RFC3339Nano),
	})

	out, _, err := execute(t, "reaper", "--dry-run")
	if err != nil {
		t.Fatalf("fugaro reaper --dry-run: %v", err)
	}
	want := "refused projects/proj-1234/locations/us-east5/jobs/" + job +
		": its latest job-level status event predates its queue deadline, so it may date the queue wait" +
		" rather than the SCHEDULED state it reports: how long it has been in that state is unknown" +
		" and no deadline can clear it"
	if n := utf8.RuneCountInString(want); n <= pluginwire.MaxPrintable {
		t.Fatalf("the refusal line is %d runes, not past the %d-rune cap: this test would pass with the cap in place", n, pluginwire.MaxPrintable)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	found := false
	for _, l := range lines {
		if l == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the report has no whole refusal line\nwant %q\n got %q", want, lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "truncated") {
			t.Errorf("report line %q is cut off: the reason is the point of the line", l)
		}
	}
}

// TestReaperCommandFlags: `fugaro reaper` takes the cloud flags and --dry-run,
// and nothing else -- it is not a command that grows a subcommand, and an
// argument it does not understand must not be read as one.
func TestReaperCommandFlags(t *testing.T) {
	out, _, err := execute(t, "reaper", "--help")
	if err != nil {
		t.Fatalf("fugaro reaper --help: %v", err)
	}
	for _, want := range []string{"--dry-run", "--config", "--project", "--gcp-project", "--region"} {
		if !strings.Contains(out, want) {
			t.Errorf("fugaro reaper --help does not mention %s", want)
		}
	}
	if _, _, err := execute(t, "reaper", "now"); err == nil {
		t.Error("fugaro reaper accepted an argument")
	}
	// The command is in the tree, so `fugaro help` finds it.
	out, _, err = execute(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reaper") {
		t.Error("fugaro help does not list the reaper")
	}
}

// TestReaperOptionsFromTheProjectConfig: what the command derives for the
// pass, read off the config rather than through a command tree. The two
// endpoints are the point of it: the job pass reads the installation's own
// Batch endpoint, and the instance pass reads the package's Compute root,
// which no project config carries.
func TestReaperOptionsFromTheProjectConfig(t *testing.T) {
	lc, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		"endpoints: { batch: https://batch.example.com/, no_auth: true }\n" +
		"batch:\n  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n  task_timeout: 45m\n" +
		"repos:\n  acme/app: { provider: github, workflows: [web] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	o := reaperOptions(lc, true)
	if o.GCPProject != "proj-1234" || o.Region != "us-east5" {
		t.Errorf("the pass's project and region = %s/%s, want the config's proj-1234/us-east5", o.GCPProject, o.Region)
	}
	if !o.DryRun {
		t.Error("--dry-run is not passed to the reaper")
	}
	if o.Endpoints.Batch != lc.Endpoints.Batch || o.Endpoints.NoAuth != lc.Endpoints.NoAuth {
		t.Errorf("endpoints = %+v, want the config's own batch endpoint and no_auth", o.Endpoints)
	}
	if o.Endpoints.Compute != reaperComputeRoot {
		t.Errorf("the Compute endpoint = %q, want the package's root %q: no project config carries one", o.Endpoints.Compute, reaperComputeRoot)
	}
	// The timeouts are the installation's own, the same source a launch's are:
	// an installation that lengthens a workflow lengthens the reaper's
	// patience with it.
	got, err := o.Timeouts.LongestTaskTimeout()
	if err != nil {
		t.Fatal(err)
	}
	if got != lc.BatchTaskTimeout() || got != 45*time.Minute {
		t.Errorf("LongestTaskTimeout = %v, want the config's batch.task_timeout (45m)", got)
	}
	if _, err := batch.NewReaper(context.Background(), o); err != nil {
		t.Errorf("the derived options are not a pass at all: %v", err)
	}
}

// TestReaperCommandFromTheReaperJobsOwnEnvironment: the pass the Cloud Run job
// runs. The job has no config file, no checkout and no project name -- only
// FUGARO_GCP_PROJECT and FUGARO_REGION, which reaper.tf sets on it -- so the
// environment is the pass's configuration, and the whole pass runs from it:
// the report, the deletions, and (with --dry-run, which an operator would only
// add by hand) nothing deleted.
func TestReaperCommandFromTheReaperJobsOwnEnvironment(t *testing.T) {
	for _, dry := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry-run=%v", dry), func(t *testing.T) {
			rig := newReaperEnvRig(t)
			job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
			vm := rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)
			args := []string{"reaper"}
			if dry {
				args = append(args, "--dry-run")
			}
			out, _, err := execute(t, args...)
			if err != nil {
				t.Fatalf("fugaro reaper from the job's environment: %v", err)
			}
			loc := "projects/proj-1234/locations/us-east5"
			if !strings.Contains(out, loc+"\n") && !strings.Contains(out, loc+":") {
				t.Errorf("the report %q does not name the project and region the environment gives", out)
			}
			verb := "deleted"
			if dry {
				verb = "would delete"
			}
			for _, want := range []string{
				verb + " job " + loc + "/jobs/" + job + " (",
				verb + " VM " + vm + " (",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("the report %q has no line with %q", out, want)
				}
			}
			if dry {
				if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
					t.Fatalf("a dry run deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
				}
				return
			}
			if got := rig.bf.Deletes(); len(got) != 1 || got[0] != job {
				t.Errorf("jobs deleted = %v, want [%s]", got, job)
			}
			if got := rig.cf.Deletes(); len(got) != 1 || got[0] != vm {
				t.Errorf("VMs deleted = %v, want [%s]", got, vm)
			}
		})
	}
}

// TestReaperEnvOptions: the derivation the environment path rests on, read
// without a command tree. Its two properties are what the job's own
// environment can and cannot say: the project and the region (Terraform sets
// both), and no task timeout at all -- so the bound is a run's own ceiling,
// which is the deadline an installation with no batch.task_timeout derives
// anyway.
func TestReaperEnvOptions(t *testing.T) {
	env := map[string]string{envGCPProject: "proj-1234", envRegion: "us-east5"}
	getenv := func(k string) string { return env[k] }
	o, forced, err := reaperEnvOptions(getenv, true)
	if err != nil {
		t.Fatalf("reaperEnvOptions: %v", err)
	}
	if forced {
		t.Error("reaperEnvOptions reported a forced dry run for a pass that asked for one")
	}
	if o.GCPProject != "proj-1234" || o.Region != "us-east5" {
		t.Errorf("project/region = %s/%s, want the environment's proj-1234/us-east5", o.GCPProject, o.Region)
	}
	if !o.DryRun {
		t.Error("--dry-run is not passed to the reaper")
	}
	// With no test wiring the pass reaches the APIs themselves: "" is the
	// API's own address, and the account the job runs as is its credential.
	if o.Endpoints.Batch != "" || o.Endpoints.NoAuth {
		t.Errorf("endpoints = %+v, want the real APIs and the account's own credentials", o.Endpoints)
	}
	if o.Endpoints.Compute != reaperComputeRoot {
		t.Errorf("the Compute endpoint = %q, want the package's root %q", o.Endpoints.Compute, reaperComputeRoot)
	}
	longest, err := o.Timeouts.LongestTaskTimeout()
	if err != nil {
		t.Fatal(err)
	}
	if longest != backend.MaxRunTime {
		t.Errorf("LongestTaskTimeout = %v, want backend.MaxRunTime (%v): the environment carries no task timeout", longest, backend.MaxRunTime)
	}
	if per, err := o.Timeouts.TaskTimeout("acme-app", "web"); err != nil || per != backend.MaxRunTime {
		t.Errorf("TaskTimeout = %v, %v, want backend.MaxRunTime", per, err)
	}
	// The wiring a test may set is passed through as it is -- a pass reads the
	// fakes' addresses and sends no credentials -- and the production value is
	// what the assertions above read.
	old := reaperEnvTest
	reaperEnvTest = &reaperEnvWiring{batch: "https://batch.example.com/", noAuth: true}
	t.Cleanup(func() { reaperEnvTest = old })
	// No marker: this environment is an operator's shell, not the reaper job,
	// so a pass that asked to delete is forced to a dry run.
	wired, forced, err := reaperEnvOptions(getenv, false)
	if err != nil {
		t.Fatal(err)
	}
	if !forced || !wired.DryRun {
		t.Errorf("reaperEnvOptions without %s: forced=%v, DryRun=%v, want a forced dry run", envReaperJob, forced, wired.DryRun)
	}
	if wired.Endpoints.Batch != "https://batch.example.com/" || !wired.Endpoints.NoAuth {
		t.Errorf("endpoints = %+v, want the test wiring's own", wired.Endpoints)
	}
	// And with the job's marker the pass deletes, which is what the Cloud Run
	// job is for: the marker is the only difference between the two.
	withMarker := map[string]string{}
	for k, v := range env {
		withMarker[k] = v
	}
	withMarker[envReaperJob] = reaperJobMarker
	job, forced, err := reaperEnvOptions(func(k string) string { return withMarker[k] }, false)
	if err != nil {
		t.Fatal(err)
	}
	if forced || job.DryRun {
		t.Errorf("reaperEnvOptions with %s=%s: forced=%v, DryRun=%v, want a real pass", envReaperJob, reaperJobMarker, forced, job.DryRun)
	}
	if _, err := batch.NewReaper(context.Background(), wired); err != nil {
		t.Errorf("the derived options are not a pass at all: %v", err)
	}
	// Half an environment is not one: the region alone names nothing to sweep.
	for _, missing := range []string{envGCPProject, envRegion} {
		part := map[string]string{}
		for k, v := range env {
			part[k] = v
		}
		delete(part, missing)
		if _, _, err := reaperEnvOptions(func(k string) string { return part[k] }, false); err == nil {
			t.Errorf("reaperEnvOptions accepted an environment with no %s", missing)
		}
	}
}

// TestReaperRefusesWhereNothingIsConfigured: the environment path is the
// job's, and it is taken only where nothing named a config. A config the
// operator *did* name -- --config here, which selects nothing, the file being
// absent -- is never quietly passed over for the environment, however complete
// the environment is: the pass would otherwise sweep a project nobody asked
// about, and say nothing about the config that failed.
func TestReaperRefusesWhereNothingIsConfigured(t *testing.T) {
	// A job and a VM a pass would delete, so a command that got past the
	// refusal is caught sweeping rather than passing quietly.
	rig := newReaperEnvRig(t)
	rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)

	missing := filepath.Join(t.TempDir(), "absent.yaml")
	out, _, err := execute(t, "reaper", "--config", missing)
	if err == nil {
		t.Fatal("fugaro reaper ran with --config naming a file that does not exist")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit code = %d, want %d (a user error: the config is what is wrong)", code, ExitUserError)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing: the pass never ran", out)
	}
	if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
		t.Errorf("the refused command deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
	}
	// And the same command with nothing named and no environment to fall back
	// on: the selection's own refusal, which is the one that says what to do.
	rig2 := newReaperEnvRig(t)
	rig2.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	t.Setenv(envGCPProject, "")
	t.Setenv(envRegion, "")
	out, _, err = execute(t, "reaper")
	if err == nil {
		t.Fatal("fugaro reaper ran with no config and no environment")
	}
	if !strings.Contains(err.Error(), "no project config") {
		t.Errorf("refusal = %q, want the selection's own (no project config)", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing: the pass never ran", out)
	}
	if len(rig2.bf.Deletes()) != 0 || len(rig2.cf.Deletes()) != 0 {
		t.Errorf("the refused command deleted %v and %v", rig2.bf.Deletes(), rig2.cf.Deletes())
	}
}

// TestReaperCommandNeedsNoBatchBackend: the command never opens the seam's
// backend, so a project config that names its Batch resources is still a
// Cloud Run installation's -- `backend: cloud-batch` stays refused, which is
// Task 10's to lift and not this task's. The config below names no backend at
// all, which is Cloud Run.
func TestReaperCommandNeedsNoBatchBackend(t *testing.T) {
	newReaperRig(t, true)
	if _, _, err := execute(t, "reaper", "--dry-run"); err != nil {
		t.Fatalf("fugaro reaper in a Cloud Run installation with a batch: block: %v", err)
	}
	// And the refusal localcfg still makes is untouched by any of this.
	if _, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		"backend: cloud-batch\nrepos:\n  acme/app: { provider: github, workflows: [web] }\n")); err == nil {
		t.Error("localcfg accepts `backend: cloud-batch`, which only the enable task may do")
	}
}

// TestReaperRefusesTheProjectsOutOfBounds: the deny-list is in NewReaper, so it
// is on BOTH ways a project reaches the command -- a project config's
// gcp_project and the reaper job's own environment. Both are checked here end
// to end, with a job and a VM in the fakes that a pass would delete: a command
// that got past the refusal is caught deleting rather than passing quietly.
func TestReaperRefusesTheProjectsOutOfBounds(t *testing.T) {
	for _, project := range []string{"edge-devel-dimi", "fugaro-belong"} {
		t.Run("config:"+project, func(t *testing.T) {
			rig := newReaperRig(t, true)
			rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
			rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)
			// The rig's config, pointed at the out-of-bounds project: every
			// other field is a well-formed installation's, so the refusal can
			// only be the project.
			path := os.Getenv("FUGARO_CONFIG")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			edited := strings.Replace(string(b), "gcp_project: proj-1234", "gcp_project: "+project, 1)
			if edited == string(b) {
				t.Fatalf("the rig's config does not name gcp_project the way this test edits it: %s", b)
			}
			if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}

			out, _, err := execute(t, "reaper")
			if err == nil {
				t.Fatalf("fugaro reaper swept the out-of-bounds project %s", project)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d (a user error: the project is what is wrong)", code, ExitUserError)
			}
			if !strings.Contains(err.Error(), "out of bounds") {
				t.Errorf("refusal = %q, want it to name the rule (out of bounds)", err)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing: the pass never ran", out)
			}
			if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
				t.Errorf("the refused command deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
			}
		})

		t.Run("environment:"+project, func(t *testing.T) {
			rig := newReaperEnvRig(t)
			rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
			rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)
			// The job's own environment, naming the out-of-bounds project. The
			// Cloud Run job's marker is set too, so the refusal cannot be the
			// marker's read-only path doing the work.
			t.Setenv(envGCPProject, project)
			t.Setenv(envReaperJob, reaperJobMarker)

			out, _, err := execute(t, "reaper")
			if err == nil {
				t.Fatalf("fugaro reaper swept the out-of-bounds project %s from the job's environment", project)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d (a user error: the project is what is wrong)", code, ExitUserError)
			}
			if !strings.Contains(err.Error(), "out of bounds") {
				t.Errorf("refusal = %q, want it to name the rule (out of bounds)", err)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing: the pass never ran", out)
			}
			if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
				t.Errorf("the refused command deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
			}
		})
	}
}

// TestReaperCommandFromAShellTakesNoAction: the environment path's two
// variables are the history job's, so an operator's shell that exports them
// (the history job's own documentation tells them to) would otherwise reach
// this path and delete for real. Only the reaper's own Cloud Run job carries
// envReaperJob, and without it the pass is a dry run -- forced, and said out
// loud on stderr, because a report an operator reads as a sweep is the failure
// this guards against.
func TestReaperCommandFromAShellTakesNoAction(t *testing.T) {
	rig := newReaperEnvRig(t)
	job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
	vm := rig.addLeakedVM(t, otherRun, reaperMaxAge()+time.Hour)
	t.Setenv(envReaperJob, "")

	out, errOut, err := execute(t, "reaper")
	if err != nil {
		t.Fatalf("fugaro reaper from a shell: %v", err)
	}
	if !strings.Contains(errOut, envReaperJob) || !strings.Contains(errOut, "DRY RUN") {
		t.Errorf("stderr = %q, want a note naming %s and the forced dry run", errOut, envReaperJob)
	}
	loc := "projects/proj-1234/locations/us-east5"
	for _, want := range []string{
		"would delete job " + loc + "/jobs/" + job + " (",
		"would delete VM " + vm + " (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to say %q: the report is what a dry run is for", out, want)
		}
	}
	if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
		t.Errorf("a pass with no %s deleted %v and %v", envReaperJob, rig.bf.Deletes(), rig.cf.Deletes())
	}

	// --dry-run says the same thing without the note: a pass that asked never
	// gets a note about being forced.
	out, errOut, err = execute(t, "reaper", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing: --dry-run asked for the dry run", errOut)
	}
	if !strings.Contains(out, "would delete job ") {
		t.Errorf("stdout = %q, want the dry run's report", out)
	}
	// And a marker that is not the one the job sets is not the marker.
	t.Setenv(envReaperJob, "true")
	if _, _, err := execute(t, "reaper"); err != nil {
		t.Fatal(err)
	}
	if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
		t.Errorf("%s=true was read as the job's marker: deleted %v and %v", envReaperJob, rig.bf.Deletes(), rig.cf.Deletes())
	}
}

// TestReaperRefusesFlagsTheEnvironmentPathCannotHonour: --gcp-project and
// --region modify a SELECTED config and nothing else, so where nothing selected
// one they used to be dropped in silence -- `fugaro reaper --gcp-project
// other-proj` exited 0 having swept the environment's project. A flag that is
// never honoured is refused instead, and the refusal names both ways out.
func TestReaperRefusesFlagsTheEnvironmentPathCannotHonour(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"gcp-project", []string{"--gcp-project", "other-proj"}, "--gcp-project other-proj"},
		{"region", []string{"--region", "us-central1"}, "--region us-central1"},
		{"both", []string{"--gcp-project", "other-proj", "--region", "us-central1"}, "--gcp-project other-proj and --region us-central1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newReaperEnvRig(t)
			job := rig.addQueuedJob(t, theRun, reaperQueueDeadline()+time.Hour)
			out, _, err := execute(t, append([]string{"reaper"}, tc.args...)...)
			if err == nil {
				t.Fatalf("fugaro reaper %v exited 0: the flag was ignored and the environment's project swept", tc.args)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d (a user error: the flag is what is wrong)", code, ExitUserError)
			}
			for _, want := range []string{tc.want, envGCPProject, envRegion, "--dry-run"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal = %q, want it to name %q", err, want)
				}
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing: the pass never ran", out)
			}
			if len(rig.bf.Deletes()) != 0 || len(rig.cf.Deletes()) != 0 {
				t.Errorf("the refused command deleted %v and %v", rig.bf.Deletes(), rig.cf.Deletes())
			}
			if len(rig.bf.IDs()) != 1 || rig.bf.IDs()[0] != job {
				t.Errorf("the job under test is not the one the rig made: %v", rig.bf.IDs())
			}
		})
	}
}
