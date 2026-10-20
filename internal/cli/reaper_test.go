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

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
