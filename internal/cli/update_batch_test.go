package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_batch_test.go is task BR6 of docs/batch-run-identity's plan: `fugaro
// update` applies the compute root where the installation's block carries the
// opt-in fact (R2, D3110), whatever the updating machine's own `backend:` says,
// and leaves alone an installation nobody opted in.
//
// The fact is the installation's and it is published; what the run reads is its
// own effective config, which is the machine's file wherever it has one and the
// published copy only where it has none (updatesComputeRoot has the rule, R15).
// These fixtures write it onto the machine's file, which is the state BR16
// leaves every machine in; a machine with a file of its own and no `batch:`
// block states no fact and converges nothing.
//
// The fixture is the update door's own light rig (updateRig: a checkout-less
// update of an installation one release behind) plus the compute stage's
// preparatives — computeReadyWith adds the state bucket, the base pin, the run
// account, the preload job and the root's outputs — so the cases below
// drive `fugaro update` itself rather than a hand-built stage list: what the
// door registers is half of what these tests are about.

// updateBatchOperator is the installation's admin (the role A2 names) as the
// local config spells a member: `user:<address>`, which is what terraform.admins
// holds. The caller this machine presents is that same member, which is what the
// stage's operator check compares the roster against.
const updateBatchOperator = "user:ops@example.com"

// updateBatchRegion is the region the rig's local config names. It is spelled
// here because the Fugaro job-name condition the compute module grants its
// launchers under is built from it.
const updateBatchRegion = "us-east5"

// updateBatchReleasePin stands for the base pin THIS release records, as the
// images stage writes it into the config the run holds: a pin other than the
// one the installation was on, which is what makes a stale read visible in the
// root's tfvars.
const updateBatchReleasePin = "us-east5-docker.pkg.dev/" + initProject + "/" + infra.BaseRegistry + "/fugaro-base:9.9.9"

// updateBatchRig is the update door's rig on an installation that opted in to
// Batch, with an operator's credential on this machine: the installation R2's
// second half is about.
func updateBatchRig(t *testing.T) *initRig {
	t.Helper()
	r := updateRig(t)
	computeReadyWith(t, r, computeOwnPin)
	r.appendConfig(t, "batch:\n  image: "+computeOwnPin+"\n  opt_in: "+localcfg.BatchOptInOn+"\n")
	updateBatchOperatorOn(t, r)
	return r
}

// updateBatchOperatorOn writes the installation's admin list and makes this
// machine's credential that member: the two halves of the operator check
// (s.e.spec.Operators against accessCallerLookup).
func updateBatchOperatorOn(t *testing.T, r *initRig) {
	t.Helper()
	r.appendConfig(t, "terraform:\n  admins: ["+updateBatchOperator+"]\n")
	r.save(t)
	accessRunAs(t, updateBatchOperator)
}

// computeProvisioned scripts the compute root's own state as a root that was
// applied once holds it. It is the answer updateSkip reads and the only thing
// that tells "the operator never provisioned this" from "the operator is not
// an operator".
func computeProvisioned(t *testing.T, r *initRig) {
	t.Helper()
	r.script["show -json@compute"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"module.compute.google_compute_router.reaper"}]}}}`}
	r.save(t)
}

// computePlans scripts a change for the compute root alone: the plan exits 2
// (terraform's "changes are present") and the plan document is the caller's.
// Every other root keeps the rig's own empty plan.
func computePlans(t *testing.T, r *initRig, changes ...planChange) {
	t.Helper()
	r.script["plan@compute"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "compute", changes...)
}

// launcherGrantChange is the launchers' Batch Job Editor grant as the compute
// root's plan carries it: the compute module's own row, under the Fugaro
// job-name condition jobsCondition holds it to. It is the one grant a plan of
// this root may hold, and R12's "an apply that changes only a role or a grant"
// is exactly this change.
func launcherGrantChange(member string) planChange {
	return jobsEditorChange(member, []any{map[string]any{
		"title":       gcp.LauncherJobsConditionTitle,
		"description": gcp.LauncherJobsConditionDescription,
		"expression":  gcp.LauncherJobsCondition(initProject, updateBatchRegion),
	}})
}

// TestUpdateAppliesTheComputeRootWhereTheInstallationOptedIn is BR6's red step
// and its whole point: an installation whose published block says "in" reaches
// the compute root through `fugaro update` alone, with no flag, in the root's
// own workdir and state prefix — the first provisioning stays `fugaro setup
// --provision-batch`'s, and every later converge of the same root is the one
// command the upgrade is (R2, D3110).
func TestUpdateAppliesTheComputeRootWhereTheInstallationOptedIn(t *testing.T) {
	r := updateBatchRig(t)
	computeProvisioned(t, r)
	computePlans(t, r, launcherGrantChange(updateBatchOperator))
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("an update on an installation that opted in did not apply the compute root: %v\n%s", err, out)
	}
	calls := computeCalls(t, r)
	if got := subcommands(calls); !slicesContains(got, "plan") || !slicesContains(got, "apply") {
		t.Fatalf("the update neither planned nor applied the compute root it found an opt-in fact for: %q\n%s", got, out)
	}
	if init := calls[1]; !slicesContains(init, "-backend-config=prefix="+infra.StatePrefixCompute) {
		t.Errorf("the update did not converge the compute root's own state: %q", init)
	}
	if !strings.Contains(out, "✔ compute applied:") {
		t.Errorf("the transcript does not show the compute root applied:\n%s", out)
	}
	// No rebuild is started for a grant: R12's other half, pinned here as well
	// as in TestAGrantOnlyApplyStartsNoPreloadRebuild because this is the run
	// the acceptance's first sentence is about.
	if runs := r.sched.Runs(); len(runs) != 0 {
		t.Errorf("an apply that changed only a grant started the preload rebuild: %v", runs)
	}
}

// TestUpdateLeavesAnInstallationNobodyOptedInAlone: a block with no opt-in fact
// — what `fugaro setup --provision-batch` writes on a machine that never chose
// Batch — is an installation `fugaro update` does not touch, and the stage is
// not registered at all rather than skipped: a door cannot leave alone what it
// never reaches.
func TestUpdateLeavesAnInstallationNobodyOptedInAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
	}{
		{"no batch block at all", ""},
		{"a block with no opt-in fact", "batch:\n  image: " + computeOwnPin + "\n"},
		{"a block whose opt-in was withdrawn", "batch:\n  image: " + computeOwnPin + "\n  opt_in: " + localcfg.BatchOptInWithdrawn + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := updateRig(t)
			computeReadyWith(t, r, computeOwnPin)
			if tc.block != "" {
				r.appendConfig(t, tc.block)
			}
			updateBatchOperatorOn(t, r)
			computeProvisioned(t, r)
			computePlans(t, r, launcherGrantChange(updateBatchOperator))

			e := rigEngine(t, r, &initOptions{door: updateDoor.name})
			if _, ok := stageNamed(initStages(e), initflow.Compute); ok {
				t.Fatalf("the update door registers the compute stage on an installation nobody opted in: %s", tc.name)
			}
			fakeTerminal(t)
			out, _, err := executeStdin(t, "", "update")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if calls := computeCalls(t, r); len(calls) != 0 {
				t.Errorf("the update reached the compute root on an installation nobody opted in: %q\n%s", calls, out)
			}
			if strings.Contains(out, "compute") && strings.Contains(out, "applied") {
				t.Errorf("the update applied the compute root on an installation nobody opted in:\n%s", out)
			}
		})
	}
}

// TestTheUpdatingMachinesOwnBackendDoesNotDecide: the fact that decides is the
// PUBLISHED block's, and a machine's own `backend:` is that machine's (F32).
// The two cases are the two ways the two can disagree, and both are read the
// same way: the block's opt-in fact alone.
func TestTheUpdatingMachinesOwnBackendDoesNotDecide(t *testing.T) {
	t.Run("the machine runs Cloud Run on an installation that opted in", func(t *testing.T) {
		r := updateBatchRig(t)
		computeProvisioned(t, r)
		computePlans(t, r, launcherGrantChange(updateBatchOperator))
		if lc := rigEngine(t, r, &initOptions{door: updateDoor.name}).lc; lc.Backend() != backend.CloudRun {
			t.Fatalf("the fixture is not a machine selecting %s but %s", backend.CloudRun, lc.Backend())
		}
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", "update")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if calls := subcommands(computeCalls(t, r)); !slicesContains(calls, "apply") {
			t.Errorf("a machine selecting %s left the root an installation opted in to unapplied: %q\n%s", backend.CloudRun, calls, out)
		}
	})
	t.Run("the machine selects Batch on an installation that opted out", func(t *testing.T) {
		r := updateRig(t)
		computeReadyWith(t, r, computeOwnPin)
		r.appendConfig(t, "batch:\n  image: "+computeOwnPin+"\n  opt_in: "+localcfg.BatchOptInWithdrawn+"\nbackend: "+backend.CloudBatch+"\n")
		updateBatchOperatorOn(t, r)
		computeProvisioned(t, r)
		computePlans(t, r, launcherGrantChange(updateBatchOperator))
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", "update")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if calls := computeCalls(t, r); len(calls) != 0 {
			t.Errorf("a machine's own backend selection decided what the installation applies: %q\n%s", calls, out)
		}
	})
}

// TestANonOperatorsUpdateSkipsTheStage: the root is applied by an operator or
// by nobody (A2, R2). A machine whose credential is not on the roster is not an
// error and not a refusal — it is one line, and the update finishes every other
// step, because the one command the upgrade is may never stop for a role.
func TestANonOperatorsUpdateSkipsTheStage(t *testing.T) {
	r := updateBatchRig(t)
	computeProvisioned(t, r)
	computePlans(t, r, launcherGrantChange(updateBatchOperator))
	// The credential is somebody else's, and the roster still names the
	// operator: the two are read apart.
	accessRunAs(t, "user:member@example.com")
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("a non-operator's update did not finish: %v\n%s", err, out)
	}
	if !strings.Contains(out, "– compute skipped: only an operator applies the compute root") {
		t.Errorf("the non-operator's update does not say why the compute root was left alone:\n%s", out)
	}
	if calls := subcommands(computeCalls(t, r)); len(calls) != 0 {
		t.Errorf("a non-operator's update reached the compute root: %q", calls)
	}
}

// TestAnOptedInInstallationNeverProvisionedIsSkipped: the installation opted in
// but the root holds no resource yet, and that is the FIRST provisioning —
// `fugaro setup --provision-batch`'s, the owner's own step (D3110 as amended).
// The update says so in one line naming that command and applies nothing.
func TestAnOptedInInstallationNeverProvisionedIsSkipped(t *testing.T) {
	r := updateBatchRig(t)
	computePlans(t, r, launcherGrantChange(updateBatchOperator))
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the update stopped over a root nobody provisioned: %v\n%s", err, out)
	}
	// The screen cuts a long row and the log keeps it whole (report.Brief), so
	// the command the person has to run is read back where it is whole.
	want := "the compute root was never provisioned here; provision it once with " + selfCommand() + " setup --provision-batch"
	if all := out + newestLog(t, "update"); !strings.Contains(all, want) {
		t.Errorf("the update does not name the first provisioning's own command:\n%s", all)
	}
	if calls := subcommands(computeCalls(t, r)); slicesContains(calls, "apply") {
		t.Errorf("the update provisioned the root itself: %q", calls)
	}
}

// TestTheUpdateSaysABatchRunKeepsRunning: the update is safe while runs fly,
// always (the owner's requirement of 2026-11-09), and this step is one of the
// ones that can still reach a Batch run. It says the count in one line and
// changes nothing the run holds — the registry entry the database counts it by
// is exactly what it found.
func TestTheUpdateSaysABatchRunKeepsRunning(t *testing.T) {
	r := updateBatchRig(t)
	computeProvisioned(t, r)
	computePlans(t, r, launcherGrantChange(updateBatchOperator))
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	const runID = "20261124-201827-570b"
	db := buildBudgetForRig(t, r)
	db.Set("agents/"+slug+"/"+runID, map[string]any{"repo": "acme/app", "stage": "implement"})
	useLaunchRecords(t, slug, runID, backend.CloudBatch)
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "1 run in flight: this step changes nothing it holds") {
		t.Errorf("the compute step does not say that a Batch run is in flight:\n%s", out)
	}
	if got := db.Value("agents/" + slug + "/" + runID); got == nil {
		t.Errorf("the update removed the run's registry entry: it is not in flight any more")
	}
	if calls := subcommands(computeCalls(t, r)); !slicesContains(calls, "apply") {
		t.Errorf("the run in flight stopped the step instead of being named by it: %q\n%s", calls, out)
	}
}

// useLaunchRecords makes the record bucket this run reads serve the launch
// records the caller names: the registry says which runs are in flight, and a
// run's own launch.json is where the backend it started on is written
// (runstore.Launch.Backend), which is what narrows the count to Batch.
func useLaunchRecords(t *testing.T, slug, runID string, backends ...string) {
	t.Helper()
	prev := openRecordBucket
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		b, err := blobx.Open(ctx, "mem://")
		if err != nil {
			return nil, err
		}
		store := runstore.Open(b.Bucket, slug, runID)
		l := &runstore.Launch{Version: 1, RunID: runID}
		if len(backends) > 0 {
			l.Backend = backends[0]
		}
		if err := store.WriteLaunch(ctx, l); err != nil {
			return nil, err
		}
		return b, nil
	}
	t.Cleanup(func() { openRecordBucket = prev })
}

// TestTheComputeStageUnderUpdateAsksNothing: the operator's opt-in fact is the
// authorization (R2), so this path asks nothing at all — no typed name, no
// y/N, and stdin is never read. What authorizes the apply is on the screen
// either way: the plan this stage prints.
func TestTheComputeStageUnderUpdateAsksNothing(t *testing.T) {
	r := updateBatchRig(t)
	computeProvisioned(t, r)
	computePlans(t, r, launcherGrantChange(updateBatchOperator))
	fakeTerminal(t)
	spy := &readerSpy{Reader: strings.NewReader("")}

	out, _, err := executeStdinReader(t, spy, "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if spy.read {
		t.Errorf("the compute step read stdin: this step asks nothing and no answer a run carries reaches it")
	}
	if calls := subcommands(computeCalls(t, r)); !slicesContains(calls, "apply") {
		t.Errorf("nothing was asked and nothing was applied either: %q\n%s", calls, out)
	}
}

// TestTheComputeStageUnderUpdateIsRefusedInAnAgentSession: the fence is the
// session's (D1555). A coding agent's update is refused before anything of this
// root runs — no tfvars, no init, no plan, no apply — and what it reads is the
// command its operator runs themselves.
func TestTheComputeStageUnderUpdateIsRefusedInAnAgentSession(t *testing.T) {
	r := updateBatchRig(t)
	computeProvisioned(t, r)
	computePlans(t, r, launcherGrantChange(updateBatchOperator))
	agentSession(t)
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("a coding agent's session applied the compute root:\n%s", out)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "CLAUDECODE") {
		t.Errorf("the refusal does not name the session's marker: %s", got)
	}
	if calls := computeCalls(t, r); len(calls) != 0 {
		t.Errorf("a coding agent's session reached the compute root: %q", calls)
	}
}

// TestAGrantOnlyApplyStartsNoPreloadRebuild: R12's rule, both halves. The
// preloaded boot image is built from the rebuild job's own inputs, so an apply
// that changes a role or a grant starts nothing; one that changes those inputs
// starts the rebuild now rather than at its weekly schedule, and says so in one
// line with what it costs.
func TestAGrantOnlyApplyStartsNoPreloadRebuild(t *testing.T) {
	t.Run("a grant", func(t *testing.T) {
		r := updateBatchRig(t)
		computeProvisioned(t, r)
		computePlans(t, r, launcherGrantChange(updateBatchOperator))
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", "update")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if runs := r.sched.Runs(); len(runs) != 0 {
			t.Errorf("an apply that changed only a grant started the preload rebuild: %v", runs)
		}
		if strings.Contains(out, "the preload image's inputs changed") {
			t.Errorf("the update claims the image's inputs changed under a grant-only plan:\n%s", out)
		}
	})
	t.Run("the image's inputs", func(t *testing.T) {
		r := updateBatchRig(t)
		computeProvisioned(t, r)
		computePlans(t, r, change("module.compute.google_cloud_scheduler_job.preload_rebuild", "update"))
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", "update")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if runs := r.sched.Runs(); len(runs) != 1 {
			t.Errorf("an apply that changed the preload's inputs started the rebuild %v, want once", runs)
		}
		if !strings.Contains(out, "the preload image's inputs changed: its rebuild starts now (Cloud Build, about two minutes, billable), rather than at its weekly schedule") {
			t.Errorf("the update does not say what the rebuild costs:\n%s", out)
		}
	})
}

// TestTheComputeStageUnderUpdateConvergesThePinTheRunRecorded: the read-only
// preview pass prepares the root before the stages that change what prepare
// reads have run — the images stage records this release's base pin and hands
// the engine the refreshed config — so the converge prepares again. A memo of
// the preview's read would put the release behind's pin in the root's tfvars,
// terraform would compare it against a state holding the same value and report
// no change, and R12's rebuild would never start in the run that changed the
// image's inputs.
func TestTheComputeStageUnderUpdateConvergesThePinTheRunRecorded(t *testing.T) {
	r := updateBatchRig(t)
	computeProvisioned(t, r)
	computePlans(t, r, change("module.compute.google_cloud_scheduler_job.preload_rebuild", "update"))
	fakeTerminal(t)

	ctx := context.Background()
	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	s := newUpdateComputeStage(e)
	// The preview pass's own read, before any stage of the run has applied.
	if _, err := s.Check(ctx); err != nil {
		t.Fatalf("the preview's check: %v", err)
	}
	// The images stage's own write: this release's pin, with the engine's view
	// of the config following it exactly as init_images.go leaves it.
	next := *e.lc
	next.BaseImages[config.BaseKind] = updateBatchReleasePin
	e.lc = &next
	if _, err := s.Apply(ctx, initflow.Env{}); err != nil {
		t.Fatalf("the converge: %v", err)
	}
	dir, err := infra.ComputeWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	b, err := infra.ReadWorkdirVars(dir, "compute")
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("the converge wrote no tfvars for the compute root")
	}
	var vars map[string]any
	if err := json.Unmarshal(b, &vars); err != nil {
		t.Fatalf("the compute root's tfvars: %v", err)
	}
	if got := vars["base_image"]; got != updateBatchReleasePin {
		t.Errorf("the compute root was planned on the pin %v, want this release's %s: the converge used the config as of the run's start, so the pin would land one update late and R12's rebuild would never start in the run that changed the image's inputs", got, updateBatchReleasePin)
	}
}
