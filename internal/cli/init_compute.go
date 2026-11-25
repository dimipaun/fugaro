package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The compute stage: the Batch backend's installation-level provisioning
// (design docs/compute-backend/spec.md §3.2, §3.7, §8.1 task 6).
//
// The FIRST provisioning is the owner's step and it is behind
// --provision-batch: that is money and infrastructure created on purpose, and
// a run that never asks for it never reaches this root. Converging a root that
// already exists is the other path, and `fugaro update` reaches it by the
// installation's own opt-in fact (R2, D3110) — and so does the `fugaro apply`
// that answers a step this machine owes, which registers the stage on the same
// fact (initStages). A run whose installation never opted in reaches this root
// on neither door.
//
// It is money and it is permanent — a preloaded boot image's Cloud Build, a
// Cloud NAT's hours, a custom role and a scheduled job — so --yes cannot
// cover it (the stage is registered noYes, and the step is one no answer a run
// carries reaches either): the FIRST provisioning is confirmed by a person at a
// real terminal, who types the project's name, and nothing else is asked of
// them (D1540); the update's converge asks nothing at all, the opt-in fact
// being what authorizes it. A coding agent's session is refused rather than
// left for the owner like a session that merely cannot answer (D1555, Apply),
// and what it reads beside the refusal is the command its owner runs
// themselves (Left).
//
// What it does NOT do: touch the installation's own root (a workdir and a
// state of its own, see infra.ComputeWorkdir), handle a secret, or delete
// anything it did not create — the plan passes tf.Guard like every other
// apply, and --allow-delete names any address on purpose.
//
// Its plan is never covered by the run's one confirmation, but its GRANTS are
// held to the compute module's rules all the same: Plan and Apply run
// infra.ComputeCover's GrantsNotCovered over it (the compute root's own
// project and region), so a Batch Job Editor grant that is not under the
// launchers' Fugaro job-name condition, a grant to someone who is neither a
// launcher, an operator nor one of the root's accounts, or a role the module
// does not grant is refused before anything is confirmed.
type computeStage struct {
	e *initEngine
	// underUpdate is the door this stage was registered by: false is
	// `--provision-batch`'s first provisioning (the owner's own step, one
	// typed confirmation), true is `fugaro update`'s converge of a root the
	// installation opted in to (R2, R12). The two differ in who authorizes the
	// apply, in what they skip, and in whether a rebuild is started -- not in
	// what they plan.
	underUpdate bool
	// changes and applied mirror engineStage's, so the plan view and the
	// JSON result read the same either way.
	changes *infra.PlanCounts
	applied bool
	// image is the preloaded boot image the root's outputs named: what the
	// stage prints and what its result records. It is not something Verify
	// can hold to anything of its own (see Verify).
	image string
	// gidLabel is the label every generation in that image's family records
	// its socket gid under, from the same outputs (infra.ComputeOutputs). The
	// stage carries the NAME and never a value: the gid is read off the
	// builder VM by the build itself and written onto the image it makes, so
	// there is nothing here to hold and nothing here to guess. What a run's
	// `--group-add` needs is that value once the image exists, which is what
	// Check 57b measures and a later `doctor` check reads back (see Verify).
	gidLabel string
	// t and spec are the compute root this stage is working on, kept
	// between the plan, the apply and the outputs.
	t    *tf.TF
	spec infra.ComputeSpec
	// outs are those outputs as the root answered them (infra.ComputeOutputs):
	// the preloaded family and the label beside it that the stage reports, and
	// the network and the subnetwork the block recorded below names. Read on
	// both paths that reach the block, since a run that applies nothing still
	// has a state to read the family out of.
	outs infra.ComputeOutputs
	// repoAccounts are the PER-REPOSITORY job accounts the project holds
	// (infra.JobRunAccounts), which is the list the block's single account is
	// picked from -- and only when there is exactly one. The root is fed these
	// BESIDE the shared project job accounts (spec.RunAccounts,
	// infra.RunAccounts, D3105), and the two are kept apart here because what
	// the root is fed is a union while the block may carry one account only
	// (batchBlockAccount).
	repoAccounts []string
}

func newComputeStage(e *initEngine) *computeStage { return &computeStage{e: e} }

// newUpdateComputeStage is the stage `fugaro update` registers where the
// installation's own opt-in fact says it is on Batch (R2, D3110): the same root,
// converged by the one command the upgrade is, on an installation that asked for
// it.
func newUpdateComputeStage(e *initEngine) *computeStage {
	return &computeStage{e: e, underUpdate: true}
}

func (s *computeStage) Name() string { return initflow.Compute }

// Check cannot know without planning whether anything is missing, so it says
// what it will do; Plan does the reading, and a Plan that finds nothing is
// the stage being done.
//
// Under `fugaro update` it is the stage's whole gate, and it is what makes the
// stage safe to register on every opted-in installation: an updating machine
// whose credential is not an operator's skips it with one line (R2, A2), and so
// does one whose root was never provisioned, naming the first provisioning's own
// command (D3110). A coding agent's session is not skipped here: the loop
// refuses it, and it can only do that for a stage that reaches it -- a Skipped
// status would suppress the refusal. Nothing is applied on any of these paths.
func (s *computeStage) Check(ctx context.Context) (initflow.Status, error) {
	if !s.underUpdate {
		return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
	}
	if m := agentMarker(os.Getenv, s.e.r.stdinTerminal()); m != "" {
		return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
	}
	skip, err := s.updateSkip(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	if skip != "" {
		return initflow.Status{State: initflow.Skipped, Detail: skip}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}

// updateSkip is why an opted-in installation's update leaves the compute root
// alone, or "" where it has nothing to say: this machine's credential is not one
// of the installation's operators, or the root holds no resource yet.
//
// The second answer reads the STATE and not the plan, and the difference is the
// whole of it: a root that was never applied plans every resource it would
// create, which is exactly `--provision-batch`'s first provisioning -- the
// update converges what exists and never does that (D3110, R2). A root that is
// there and needs tightening goes on to Apply.
func (s *computeStage) updateSkip(ctx context.Context) (string, error) {
	caller, err := accessCallerLookup(ctx)
	if err != nil || !slices.ContainsFunc(s.e.spec.Operators, func(m string) bool { return strings.EqualFold(m, caller) }) {
		// Failing closed and saying the same thing: a credential that could not
		// be read and one that is not on the roster are the same fact for a
		// person reading this line -- the root is not theirs to apply -- and
		// neither is a reason to stop the update (R2).
		return "skipped: only an operator applies the compute root, and this machine's credential is not one", nil
	}
	if err := s.prepare(ctx); err != nil {
		return "", err
	}
	st, err := s.t.ShowState(ctx)
	if err != nil {
		return "", remote(err)
	}
	if !st.Managed() {
		return "skipped: the compute root was never provisioned here; provision it once with " + selfCommand() + " setup --provision-batch", nil
	}
	return "", nil
}

// Left is what a person does about this stage. Under `--provision-batch` it is
// that flag's own command; under update it is the update, run at a terminal:
// the stage is noYes, so the loop hands it to the person instead of applying it
// wherever the session has no terminal (initflow's converge), and the `fugaro
// apply` that answers a step this machine owes reaches the same stage by the
// same opt-in fact (initStages).
func (s *computeStage) Left() initflow.Left {
	if s.underUpdate {
		return promptLeft(initflow.Compute, "run the command below at a terminal: it asks you nothing and applies the compute root the installation opted in to", updateDoor.name)
	}
	return promptLeft(initflow.Compute, "run the command below: it asks you nothing and provisions the batch", s.e.r.o.doorName(), "--provision-batch")
}

// Verify has nothing of its own to read back, and this is deliberately not a
// comparison of the outputs against the derived name: the two are equal by
// construction. The outputs are written by the apply that this stage ran,
// from the tfvars it wrote, and the module derives the image's name from the
// same pin's hash with the same expression Go does
// (infra.PreloadedImage, held together by
// TestPreloadedImageMatchesTheModule) -- so a check comparing them could
// never fail, on any path, and an apply is never reached with a pin it did
// not read (a moved pin changes the scheduler job's own body, preload.tf, so
// terraform reports it as a change and applies it rather than reporting "No
// changes"). What is NOT equal by construction is the preloaded IMAGE: a
// script builds it out of band and no resource holds it, so terraform's
// refresh covers none of it, and reading it back would take a Compute client
// this package's Clients does not carry. That read is the owner's live check
// (Check 57b) and `doctor`'s, which §8.1 task 8 gives the preload image.
func (s *computeStage) Verify(context.Context) error { return nil }

// Plan shows what the apply would do: the real plan, guarded, with nothing
// applied and nothing confirmed.
func (s *computeStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	plan, err := s.plan(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	if plan == nil {
		s.changes = &infra.PlanCounts{}
		return s.e.planned(s.changes), nil
	}
	s.e.r.planBlock(plan)
	if err := s.e.r.guard(ctx, s.e.c, plan, s.e.r.o.allowDelete); err != nil {
		return initflow.Plan{}, err
	}
	if err := s.grants(plan); err != nil {
		return initflow.Plan{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts
	return s.e.planned(&counts), nil
}

// Apply plans, guards, confirms and applies the compute root, then starts the
// preload rebuild once: a fresh installation must not wait a week for its
// first preloaded image.
//
// A coding agent's session is refused rather than left (D1555), and it is
// refused first: plan() writes the root's tfvars and plans it, so the refusal
// has to come before anything of this root runs. The fence is beside the
// loop's own agent arm and not only in it, because the loop answers this stage
// from its registration — which is not this step's — and a stage whose only
// fence is another component's ordering is one a later registration change can
// leave unfenced.
func (s *computeStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	e, r := s.e, s.e.r
	if m := agentMarker(os.Getenv, r.stdinTerminal()); m != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: m}
	}
	if s.underUpdate {
		return s.updateApply(ctx)
	}
	plan, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if plan == nil {
		s.changes = &infra.PlanCounts{}
		fmt.Fprintln(r.stageDetail(), "No changes: the compute backend's provisioning matches the plan.")
		// The provisioning is already there, so nothing is applied -- and the
		// block is written from what its state holds all the same: an
		// installation provisioned by an earlier release (or by hand, before
		// this stage wrote one) gets its block on the first rerun rather than
		// needing a re-apply it would be told it does not need.
		if err := s.recordBatchBlock(ctx); err != nil {
			return initflow.Outcome{}, err
		}
		s.announceBackend()
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	r.planBlock(plan)
	if err := r.guard(ctx, e.c, plan, r.o.allowDelete); err != nil {
		return initflow.Outcome{}, err
	}
	if err := s.grants(plan); err != nil {
		return initflow.Outcome{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts

	// The confirmation, which is the owner's own session: --yes never covers
	// this stage, and a run that cannot confirm leaves it for the owner.
	what := fmt.Sprintf("provisions the Batch compute backend in GCP project %s: the Compute and Batch APIs, the run accounts' IAM, the launcher's, reaper's and preload accounts and roles, a Cloud NAT and router (billable while they exist), and a preloaded boot image built by a scheduled Cloud Build (%s)", e.lc.GCPProject, counts.String())
	confirmed, reachable, err := r.askTyped(what)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !reachable {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if !confirmed {
		return initflow.Outcome{}, r.notConfirmed("nothing was applied")
	}
	if err := s.apply(ctx, plan); err != nil {
		return initflow.Outcome{}, err
	}
	s.applied = true
	r.res.Applied = true

	// The preload rebuild, now rather than at the next schedule. A refusal
	// here is the stage's own error: the provisioning is done, and the one
	// line that starts the build is the fix.
	//
	// The job is started where it LIVES, which is the scheduler region and not
	// the installation's (spec.SchedulerRegion): Cloud Scheduler does not serve
	// every Cloud Run region, so on an installation in one it does not serve
	// the job is in the nearest location it does, and naming the installation's
	// region here would look for a job that is not there. The Fix line below
	// has to name the same one, for the same reason.
	if err := infra.RunSchedulerJob(ctx, e.c, e.spec.Project, s.spec.SchedulerRegion, s.spec.Names.PreloadSchedulerJob); err != nil {
		return initflow.Outcome{}, &initflow.StageError{Err: err,
			Fix: "start it yourself: gcloud scheduler jobs run " + s.spec.Names.PreloadSchedulerJob + " --location=" + s.spec.SchedulerRegion + " --project=" + e.spec.Project}
	}
	if err := s.readOutputs(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(r.w, "provisioned; the preloaded boot image %s is being built now (Cloud Build, about two minutes), and rebuilt on its schedule; each generation records the socket's gid under its %s label, read off the builder VM\n", s.image, s.gidLabel)
	if err := s.recordBatchBlock(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	s.announceBackend()
	return initflow.Outcome{Changed: true, Detail: "applied: " + counts.String() + " (preload image " + s.image + ", socket gid label " + s.gidLabel + ")"}, nil
}

// updateApply is the compute root under `fugaro update` (R2, R12): the same
// plan, guard and grant rules as the provisioning, and three things that are
// not the provisioning's.
//
// It asks nothing. The operator's opt-in fact is the authorization -- the
// installation asked for this root and the update is what applies it -- so the
// typed confirmation stays where it is, on `fugaro setup --provision-batch`,
// and no flag confirms this path either (the stage is registered noYes, so a run
// with no terminal leaves it for the operator instead of applying it).
//
// It writes the block, and says nothing about the selection: the block is the
// installation's and every machine's own `backend:` is that machine's. What the
// update writes there is what the provisioning writes -- the facts this root
// answers for, merged into the block the machine already carries -- and, beside
// them, the retirement this door is the one for: the facts the block no longer
// carries leave the machine's file in place, whatever else the update finds to
// do and even where it finds nothing (recordBatchBlock). An installation that
// opted in and never applied this root keeps its launch refusal and still loses
// a key fact the release behind wrote.
//
// It starts the preload rebuild only where the plan changes what that image is
// built from -- a role or a grant is not an input, and an update that tightens
// one must not spend a Cloud Build (R12). Where it does, the one line says so
// with what it costs.
//
// A Batch run in flight is named in one line and changes nothing: the update is
// safe while runs fly, always (the owner's requirement of 2026-11-09). The count
// is the runs on this backend, read the way the update's own note reads them.
func (s *computeStage) updateApply(ctx context.Context) (initflow.Outcome, error) {
	r := s.e.r
	// The read-only preview pass prepared this root, and it ran before the
	// stages that change what prepare reads: the images stage records this
	// release's base pin and hands the engine the refreshed config, and the
	// installation stage rewrites the outputs this root takes as its inputs. A
	// memo of that pass would land this release's pin one update late — the
	// tfvars would carry the release behind's pin, terraform would compare it
	// against a state holding the same value and report no change — so R12's
	// rebuild would never start in the run that changed its inputs. The
	// converge prepares again, as init.go's writeConfig drops the policy
	// stage's own preview read for the same reason.
	s.t = nil
	plan, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if plan == nil {
		s.changes = &infra.PlanCounts{}
		fmt.Fprintln(r.stageDetail(), "No changes: the compute backend's provisioning matches the plan.")
		// Nothing to apply, and the block is written all the same: the
		// provisioning's no-changes path does the same, and this is the door
		// an installation that provisioned under an earlier release comes
		// through -- the one where a key fact the release behind wrote is
		// still in the file and has to leave it.
		if err := s.recordBatchBlock(ctx); err != nil {
			return initflow.Outcome{}, err
		}
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	r.planBlock(plan)
	if err := r.guard(ctx, s.e.c, plan, r.o.allowDelete); err != nil {
		return initflow.Outcome{}, err
	}
	if err := s.grants(plan); err != nil {
		return initflow.Outcome{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts
	if n := s.batchRunsInFlight(ctx); n > 0 {
		fmt.Fprintf(r.w, "%s in flight: this step changes nothing it holds\n", runsPhrase(n))
	}
	if err := s.apply(ctx, plan); err != nil {
		return initflow.Outcome{}, err
	}
	s.applied = true
	r.res.Applied = true
	if preloadInputsChange(plan) {
		if err := infra.RunSchedulerJob(ctx, s.e.c, s.e.spec.Project, s.spec.SchedulerRegion, s.spec.Names.PreloadSchedulerJob); err != nil {
			return initflow.Outcome{}, &initflow.StageError{Err: err,
				Fix: "start it yourself: gcloud scheduler jobs run " + s.spec.Names.PreloadSchedulerJob + " --location=" + s.spec.SchedulerRegion + " --project=" + s.e.spec.Project}
		}
		fmt.Fprintf(r.w, "the preload image's inputs changed: its rebuild starts now (Cloud Build, about two minutes, billable), rather than at its weekly schedule\n")
	}
	if err := s.recordBatchBlock(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: true, Detail: "applied: " + counts.String()}, nil
}

// preloadInputsChange reports whether a plan changes what the preloaded boot
// image is built from: the rebuild's own job, which is where the build's inputs
// live (modules/compute/preload.tf puts the base image, the refresh window and
// the family in the job's body). Everything else the root holds -- the network,
// the accounts and their bindings, the reaper's sweep -- leaves the image as it
// is, which is what R12's "an apply that changes only a role or a grant starts no
// rebuild" is about.
func preloadInputsChange(plan *tf.Plan) bool {
	for _, rc := range plan.ResourceChanges {
		if !planChangesSomething(rc) {
			continue
		}
		if strings.HasPrefix(rc.Address, "module.compute.google_cloud_scheduler_job.preload_rebuild") {
			return true
		}
	}
	return false
}

// planChangesSomething reports whether a change acts on its resource at all: a
// no-op and a read are in every plan terraform prints, for every resource the
// root holds.
func planChangesSomething(rc tf.ResourceChange) bool {
	return slices.ContainsFunc(rc.Change.Actions, func(a string) bool { return a != "no-op" && a != "read" })
}

// batchRunsInFlight counts this project's runs in flight that were launched on
// the Batch backend: the registry's entries, and each run's own launch record,
// which is where the backend it started on is written (runstore.Launch.Backend).
// It is the count R12's line carries.
//
// It reads the runs the way the update's own note does (announceRunsInFlight,
// update_in_flight.go), narrowed to the runs whose record names this backend: a
// run on Cloud Run holds nothing the compute root touches. Every failure is
// silence and not an error -- the note's own rule: a count that could not be read
// is a line nobody printed, never a reason for the one command the upgrade is to
// stop.
func (s *computeStage) batchRunsInFlight(ctx context.Context) int {
	e := s.e
	if e.lc == nil {
		return 0
	}
	lc, db, err := budgetDBFor(ctx, e.lc)
	if err != nil {
		return 0
	}
	agents, err := inFlightRegistry(ctx, db, lc)
	if err != nil || len(agents) == 0 {
		return 0
	}
	url := recordReadURL(e.lc)
	if url == "" {
		return 0
	}
	b, err := openRecordBucket(ctx, url)
	if err != nil {
		return 0
	}
	defer b.Close()
	n := 0
	for slug, runs := range agents {
		for runID := range runs {
			launch, err := runstore.Open(b.Bucket, slug, runID).ReadLaunch(ctx)
			if err != nil || launch == nil {
				continue
			}
			if launch.Backend == backend.CloudBatch {
				n++
			}
		}
	}
	return n
}

// grants refuses a plan whose grants are not the compute module's (see the
// type's comment): the compute root's own cover, never the installation's.
func (s *computeStage) grants(plan *tf.Plan) error {
	if why := infra.ComputeCover(s.spec).GrantsNotCovered(plan); len(why) > 0 {
		return userErr("the compute root's plan grants what the compute module does not (%s); nothing was applied", strings.Join(why, "; "))
	}
	return nil
}

// apply applies the confirmed plan, and waits out IAM propagation (design
// compute-backend §7.2 finding 5, §8.1 task 6): the module's bindings name the
// accounts it creates, so Terraform orders each account before its bindings,
// but a service account is not visible to IAM binding calls for a while after
// it is created, and ordering alone does not wait for that. So an apply that
// fails only on that gap (the closed list tf.PropagationFailure holds) is
// planned, guarded and granted again -- propagationRetry's cycle, which is the
// same one every other step of the converge runs -- and what is left is
// applied only when the new plan passes the guard and the grant rules and
// every change in it is a change the confirmed plan held with the same
// actions, which is what lets it go ahead without asking again. Any other
// failure, a gap that outlasts the waits, or a re-plan holding anything new is
// the stage's error.
//
// The first pass applies the plan this stage already planned, guarded, granted
// and had confirmed: it is not planned again for nothing. Every pass after it
// plans and shows the root directly (s.t.Plan, s.t.Show) rather than through
// s.plan: a failed apply leaves the workdir, the tfvars and the backend it was
// run on, so prepare has nothing to redo, and the new plan is judged like the
// first one all the same.
func (s *computeStage) apply(ctx context.Context, confirmed *tf.Plan) error {
	r := s.e.r
	first := true
	var applied *tf.Plan
	return propagationRetry(ctx, r.w, "the compute step", func(ctx context.Context) (*tf.Plan, error) {
		if first {
			first = false
			applied = confirmed
		} else {
			changed, err := s.t.Plan(ctx, infra.PlanFile)
			if err != nil {
				return nil, remote(err)
			}
			if !changed {
				// Nothing left to apply: the failed apply got there.
				return nil, nil
			}
			next, err := s.t.Show(ctx, infra.PlanFile)
			if err != nil {
				return nil, remote(err)
			}
			if err := r.guard(ctx, s.e.c, next, r.o.allowDelete); err != nil {
				return next, err
			}
			if err := s.grants(next); err != nil {
				return next, err
			}
			if extra := unconfirmed(next, confirmed); len(extra) > 0 {
				return next, userErr("the retry's plan holds changes the confirmed plan did not (%s); nothing more was applied: run fugaro setup --provision-batch again to review and confirm them", strings.Join(extra, ", "))
			}
			applied = next
		}
		return applied, remote(s.t.Apply(ctx, infra.PlanFile))
	})
}

// unconfirmed are the addresses next changes that confirmed did not change,
// or changed with other actions.
func unconfirmed(next, confirmed *tf.Plan) []string {
	had := map[string][]string{}
	for _, rc := range confirmed.ResourceChanges {
		had[rc.Address] = rc.Change.Actions
	}
	var out []string
	for _, rc := range next.ResourceChanges {
		acts := rc.Change.Actions
		if !planChangesSomething(rc) {
			continue
		}
		if prev, ok := had[rc.Address]; !ok || !slices.Equal(prev, acts) {
			out = append(out, rc.Address)
		}
	}
	return out
}

// plan prepares the workdir and the root and plans it. It returns a nil plan
// when there is nothing to do.
func (s *computeStage) plan(ctx context.Context) (*tf.Plan, error) {
	if err := s.prepare(ctx); err != nil {
		return nil, err
	}
	changed, err := s.t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return nil, remote(err)
	}
	if !changed {
		return nil, nil
	}
	return s.t.Show(ctx, infra.PlanFile)
}

// readOutputs is what the stage reports: the names the apply created, the
// preloaded image's name the root's state carries, and the label that image's
// family records the socket gid under (the value itself is not terraform's:
// see infra.ComputeOutputs.PreloadSocketGIDLabel).
func (s *computeStage) readOutputs(ctx context.Context) error {
	raw, err := s.t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	outs, err := infra.DecodeComputeOutputs(raw)
	if err != nil {
		return remote(err)
	}
	s.outs = outs
	s.image = outs.PreloadImage
	s.gidLabel = outs.PreloadSocketGIDLabel
	return nil
}

// prepare writes the compute workdir's tfvars and backend and inits it: the
// installation's outputs (its project, region, role ids, the launcher and
// operator members and the repository the pin names), the project's number,
// the run accounts the repository roots created, and the installation's
// pinned base image.
func (s *computeStage) prepare(ctx context.Context) error {
	e := s.e
	// Memoized for one pass: under update the stage's own Check prepares the
	// root to read its state, and the plan behind it reuses the same workdir.
	// The answer is the same only within a pass — the stages before this one
	// change what prepare reads — so the update door's converge drops the memo
	// before it plans (updateApply).
	if s.t != nil {
		return nil
	}
	if err := e.setup(ctx); err != nil {
		return err
	}
	// The installation must exist and be readable: its outputs are this
	// root's inputs, and its state bucket is where this root's state goes.
	outs, err := e.r.readOutputs(ctx, e.c, e.t, e.wd, e.lc, e.spec)
	if err != nil {
		return err
	}
	number, err := infra.ProjectNumber(ctx, e.c, e.spec.Project)
	if err != nil {
		return initErr(err)
	}
	// The run-class accounts, from the project itself: they are what the
	// repository roots created, and an account missing here is a run whose
	// VM cannot report its state (see infra.JobRunAccounts). Beside them go
	// the shared project job accounts (D3105): a project job's workflow runs
	// on Batch under that job's account, and the root is what gives a launcher
	// actAs on the accounts it is fed -- so a root fed the per-repository
	// accounts alone refuses every launch of a project job's workflow.
	runAccounts, err := infra.JobRunAccounts(ctx, e.c, e.spec.Project)
	if err != nil {
		return initErr(err)
	}
	projectAccounts, err := infra.ProjectJobRunAccounts(ctx, e.c, e.spec.Project)
	if err != nil {
		return initErr(err)
	}
	s.repoAccounts = runAccounts
	pin := e.lc.BaseImage(config.BaseKind)
	// The installation's never-touch list, read from the same variable the
	// reaper command reads (reaperRefuseProjects): it goes into this root's
	// tfvars so the SCHEDULED sweep refuses the same projects a pass run from a
	// shell does. Without it the list would hold only while the sweep was
	// started by hand, which is exactly when it matters least. It can only ADD
	// to the engine's own floor, and an entry that is not a project id is
	// refused here rather than dropped, for the reason reaperRefuseProjects
	// gives.
	refuse, err := reaperRefuseProjects(os.Getenv)
	if err != nil {
		return err
	}
	// Where the root's Cloud Scheduler jobs live, which is not the
	// installation's region whenever Cloud Scheduler does not serve it:
	// us-east5, the region this installation runs in, is one it does not, and
	// a Scheduler job created there fails the whole apply -- leaving the
	// reaper's sweep and the preload's rebuild not merely unscheduled but
	// absent, which is the leak the sweep exists to close. The installation's
	// own scheduler_region when it names one, and the nearest location
	// Scheduler does serve otherwise: the same resolution the history job and
	// the daily image check make (infra.History, spec.go's check), so an
	// installation writes the region once and every schedule follows it. The
	// jobs' HTTP targets name the Cloud Run job's own location, so a Scheduler
	// job in another region still starts it.
	schedulerRegion := e.lc.SchedulerRegion
	if schedulerRegion == "" {
		r, err := gcp.SchedulerRegion(e.spec.Region)
		if err != nil {
			return userErr("%v", err)
		}
		schedulerRegion = r
	}
	spec := infra.ComputeSpec{
		Project:         e.spec.Project,
		ProjectNumber:   fmt.Sprint(number),
		Region:          e.spec.Region,
		SchedulerRegion: schedulerRegion,
		Network:         infra.DefaultNetwork(e.spec.Project),
		Subnetwork:      infra.DefaultSubnetwork(e.spec.Project, e.spec.Region),
		Names:           infra.ComputeNamesFor(),
		ManageAPIs:      e.spec.ManageAPIs,
		// The launchers, and beside them the admins: this root's var.launchers
		// is what roles/batch.jobsEditor and serviceAccountUser on the run
		// accounts are for, and its var.operators hold actAs on the preload
		// account and nothing else (modules/compute/iam.tf, design
		// compute-backend §8.1 task 6). spec.Launchers is the list this run
		// treats as launchers — the operators the derive builds, admins
		// included, because an admin operates too — which is the same pair the
		// installation and repository roots take and union themselves
		// (infra.tfvars.go's launchersWithOperators).
		//
		// The admins are NOT subtracted back out of it. A member on both lists
		// — what defaultMembers writes for a one-person installation, and
		// where the migration's collapse to the highest role leaves every
		// launcher who was also an operator — would lose roles/batch.jobsEditor
		// relative to the shape before the rename, and an installation whose
		// two lists hold the same person would provision a launcher list of
		// nobody. The widening the other way (an admin who was not a launcher
		// gaining the grant) is what the roles' own rank says, and this stage
		// never applies it on --yes: it prints the plan and asks for the
		// project's name typed at a real terminal.
		Launchers:          e.spec.Launchers,
		Operators:          e.spec.Operators,
		RunAccounts:        infra.RunAccounts(runAccounts, projectAccounts),
		RegistryRepository: registryRepository(e.lc, outs),
		BaseImage:          pin,
		BaseProject:        "batch-custom-image",
		BaseFamily:         "batch-cos-stable-official",
		PreloadZone:        e.spec.Region + "-a",
		PreloadSchedule:    "0 3 * * 1",
		PreloadRefreshDays: infra.PreloadRefreshDaysDefault,
		BuilderMachineType: "e2-medium",
		BuildSubmitterRole: outs.RoleIDs.BuildSubmitter,

		ReaperRefuseProjects: refuse,
	}
	vars, err := infra.ComputeVars(spec)
	if err != nil {
		return userErr("%v; the installation must be applied first", err)
	}
	dir, err := infra.ComputeWorkdir(os.Getenv, e.spec.Project)
	if err != nil {
		return userErr("%v", err)
	}
	wd, t, err := e.r.terraform(e.bin, dir, "compute")
	if err != nil {
		return err
	}
	if err := wd.WriteVars(vars); err != nil {
		return userErr("writing the compute tfvars: %v", err)
	}
	backend, err := wd.WriteBackend(e.spec.StateBucket, infra.StatePrefixCompute)
	if err != nil {
		return userErr("%v", err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	s.t, s.spec = t, spec
	return nil
}

// recordBatchBlock writes the `batch:` block this provisioning answers for into
// this machine's project config: the block names the boot image family the
// preload publishes, the network and subnetwork a run's VMs are placed in, the
// run account they act as, and the installation's own job image. Before this,
// the block was written by hand -- the stage applied the root and left the
// config alone -- so an installation that had provisioned still could not run
// on the backend until someone typed four lines out of `terraform output`.
//
// What it does NOT do is switch the backend: the selection is
// `fugaro backends use`'s, at the owner's own terminal (announceBackend says
// so in one line), because it moves every launch of the installation onto VMs
// and puts the reaper's deletions behind it.
//
// A field the owner has already written is kept, field by field, and a block
// that is already what this run would write is not written at all. The write
// itself is the one every converge stage makes (initRun.writeLocalConfig:
// re-read, parse, marshal, diff, backup, atomic replace) and not a second
// writer of its own -- so the block lands the way base_images lands when the
// images stage records a pin. On the applying path the stage's own typed
// confirmation is what covers the write, exactly as it covers that one; on the
// no-changes path there is nothing to apply and so nothing asked, and the write
// describes the provisioning that is already there.
//
// A fact the block no longer carries leaves the file HERE, in place, whatever
// else this run writes. The block is written whenever the file still names the
// retired key: the merge this stage writes through has stopped carrying it, so
// the write is what removes it, and the file's own text is what decides that
// there is a write to make. Nothing else could: the merged block and the
// effective one compare EQUAL in every field this stage reads -- which is why
// the check is a name read off the text (retiredBlockFacts) rather than a
// comparison of the two blocks -- so without it a machine whose block is
// otherwise current would keep the key fact for good, its update having nothing
// left to say about it (R10). The publish at the end carries the same removal
// to every other machine.
func (s *computeStage) recordBatchBlock(ctx context.Context) error {
	r := s.e.r
	block, why := s.batchBlock()
	if block == nil {
		// The terminal, not the log: every one of these lines names something
		// the owner has to do before this installation can run on the backend,
		// and a stage that left it in a log file would be a provisioning that
		// looks complete and is not.
		fmt.Fprintln(r.w, why)
		return nil
	}
	if s.e.path == "" {
		fmt.Fprintln(r.w, "the batch block was not recorded: this run has no project config file to write it to (the project is read from somewhere else, such as --config with no file of its own)")
		return nil
	}
	old, err := os.ReadFile(s.e.path)
	if err != nil {
		return userErr("reading the local config to record the batch block: %v", err)
	}
	cur, err := localcfg.Parse(old)
	if err != nil {
		return userErr("%s: %v", s.e.path, err)
	}
	// The owner's block is the EFFECTIVE one, which is the local file's or the
	// published one the shared config serves (localcfg.MergeShared's whole-block
	// rule), and a merged block equal to it is not written: materializing a
	// published block into this file would make this machine's copy shadow the
	// installation's for good.
	owner := s.e.lc.Batch
	merged := mergeBatch(owner, block)
	if sameBatch(merged, owner) && !namesRetiredBlockFact(old) {
		if why != "" {
			fmt.Fprintln(r.w, why)
		}
		return nil
	}
	next := *cur
	next.Batch = merged
	if err := r.writeLocalConfig(&next, s.e.path, old, true); err != nil {
		return err
	}
	s.e.lc = &next
	if !r.o.planOnly {
		r.publishSharedConfig(ctx, &next)
	}
	if why != "" {
		fmt.Fprintln(r.w, why)
	}
	return nil
}

// retiredBlockFacts are the facts the `batch:` block no longer carries, by the
// name the config file's own text gives them: the key fact a Batch launch
// carried before a run's identity came from the claims service. Each is a key
// this stage's own writing never contains, so a file that still names one is a
// file the release behind wrote and this run has to rewrite (R10).
//
// The name is TEXT and never the Go field and its type. Those have to stay in
// internal/localcfg until the deletion that follows this task runs -- a file
// still carrying them has to load (O10) -- and a writer that named them would be
// a file that deletion has to reach into: what it removes is the field, its
// validation and its doc, so this stage knows the fact only as the word the
// config file spells.
var retiredBlockFacts = []string{"seal_key"}

// namesRetiredBlockFact reports whether the project config's text still names a
// retired fact inside its `batch:` block. It reads the file as lines rather than
// as a document because that is all it needs to be: what it answers is whether
// there is a write to make, and the write itself is the block this stage would
// have written anyway, whose merge has stopped carrying those facts.
func namesRetiredBlockFact(src []byte) bool {
	const block = "batch:"
	in := false
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, block) {
			in = true
			continue
		}
		if in && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			in = false
		}
		if !in || strings.HasPrefix(line, "#") {
			continue
		}
		for _, name := range retiredBlockFacts {
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), name+":") {
				return true
			}
		}
	}
	return false
}

// batchBlock is the block this provisioning answers for, and the line saying
// why there is none when one of its fields cannot be formed.
//
// Every field but the image comes from the apply: the family from the root's
// own outputs (with the values the root was given as the fallback for a state
// an older release wrote), and the account from the project's run accounts.
// The image is the one field no output answers -- localcfg.Batch says so in as
// many words, and it is a per-workflow fact on the design's own reading -- so
// it is formed from the installation's base image (batchJobImage), and an
// installation with no base image gets no block rather than one the parser
// refuses: localcfg refuses a batch block with an empty image, and that
// refusal is not this stage's to drop.
func (s *computeStage) batchBlock() (*localcfg.Batch, string) {
	image, why := s.batchJobImage()
	if image == "" {
		return nil, why
	}
	network, subnetwork := s.outs.Network, s.outs.Subnetwork
	if network == "" {
		network = s.spec.Network
	}
	if subnetwork == "" {
		subnetwork = s.spec.Subnetwork
	}
	// The family the preload publishes, from the root's own output -- the state
	// this stage just applied or planned against -- and from the pin it derives
	// it from when that output is not in the state (a state an earlier release
	// wrote, and the no-changes path, which reads no outputs at all). The two
	// are the same name by construction: the module's local is
	// "<preload image>-<sha256(var.base_image) first 8>" (modules/compute's
	// preload.tf) and infra.PreloadedImage is that computation (compute.go),
	// which exists so a caller can name the family without a state.
	family := s.outs.PreloadImage
	if family == "" && s.spec.BaseImage != "" {
		family = infra.PreloadedImage(s.spec.BaseImage)
	}
	if family == "" {
		return nil, "the batch block was not recorded: neither the compute root's state nor this installation's base image names the preloaded boot image family a run's VM boots (batch.boot_image), so the root's state holds no outputs and the base pin is empty"
	}
	account, why := batchBlockAccount(s.repoAccounts)
	return &localcfg.Batch{
		Image: image,
		// The family resource name and not the family's own name: a run's
		// bootDisk.image takes the resource (doctor's batch-image-family reads
		// the same shape, infra.ComputeOutputs.PreloadImage is where the bare
		// name is explained).
		BootImage:      "projects/" + s.spec.Project + "/global/images/family/" + family,
		Network:        network,
		Subnetwork:     subnetwork,
		ServiceAccount: account,
	}, why
}

// batchJobImage is the container image the block records, and the line saying
// why no image can be recorded when there is none.
//
// What is recorded is the installation's base image AS THIS INSTALLATION'S OWN
// REGISTRY SERVES IT, named by the config's own base_images entry: either a
// copy in the installation's own base repository (the shape every release
// before the remote-repository migration put there) or an image the operator
// built and pushed into that repository themselves (docs/backends.md, "The
// batch block"). Anything else is refused, with the reason, rather than written
// into a block:
//
//   - the release pin is a path through the installation's REMOTE repository
//     (fugaro-ghcr, a pull-through to ghcr.io) since the remote-repository
//     migration, and an image served from there is refused by the block's own
//     grammar in localcfg (baseImageRE takes a tag OR a digest, and a pin
//     carries both), by the shared config's reader (ParseShared refuses that
//     repository outright, sharedcfg.go) and by doctor's batch-job-image (a
//     repository that is not a STANDARD_REPOSITORY is refused there, and the
//     design's §4.4 says the same in as many words). The design's §2.1 wants
//     "fugaro-base at the installation's pinned version" and this is the one
//     place the two disagree: the pin changed shape under the block, and a
//     stage that wrote it would write a block nothing can read.
//   - another project's registry is refused for a reason of the same kind and
//     one more: ParseShared holds batch.image to the anchor's own project, so a
//     value from elsewhere cannot be published at all -- it would be a local
//     block the runs bucket's reader then refuses.
//
// So an installation whose base is the release pin gets NO block from this
// stage and one line saying what to do: build the base and push it into this
// installation's own registry, and point the config at it with --base-image
// (the images stage's own documented path for a base this tool does not
// publish), after which this stage records the whole block. That is a
// deliberate refusal rather than a guess: the alternative is a block naming an
// image nothing has pushed, which doctor passes (the repository is there and is
// standard) and the first run's pull fails on.
func (s *computeStage) batchJobImage() (image, why string) {
	lc := s.e.lc
	const field = "batch.image"
	pin := lc.BaseImage(config.BaseKind)
	if pin == "" {
		return "", "the batch block was not recorded: this installation's config names no base image (base_images." + config.BaseKind + "), and the image a run's job runs is the one field of the block no root output answers; record the installation's base first (fugaro setup's images stage pins it, or fugaro setup --base-image " + config.BaseKind + "=IMAGE records one of your own), then run fugaro setup --provision-batch again"
	}
	// The registry host is the installation's own, from the config or from the
	// region and project this run already holds: whether the pin is this tool's
	// is a question asked OF that host, so the comparison is with the same
	// string the images stage compares with.
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return "", "the batch block was not recorded: this installation's registry host cannot be read (" + err.Error() + "), and " + field + " has to be an image of this installation's own Artifact Registry for a run's VMs to execute it"
	}
	owner := baseOwner(lc)
	// Anything under REMOTE repository that is not a base this tool put there:
	// the proxy serves all of ghcr.io, so a path there belongs to whoever wrote
	// it, and the images stage refuses one outright for that reason
	// (foreignProxyRef). A job image is the worse place to carry it -- it is
	// what the run's VMs execute, as the run account -- so no block is written
	// from it either.
	if foreignProxyRef(pin, host, config.BaseKind, owner) {
		return "", "the batch block was not recorded: this installation's base image " + pluginwire.Printable(pin) + " is under the remote repository " + host + "/" + infra.GhcrRegistry + " without being a base this tool pinned for " + config.BaseKind + ", so what it holds is not your own image; remove the entry (fugaro image refresh writes the pin), then run fugaro setup --provision-batch again"
	}
	if _, ok := pinnedVersion(pin, host, config.BaseKind, owner); ok {
		return "", "the batch block was not recorded: this installation's base image is the release PIN " + pluginwire.Printable(pin) + ", which this installation's registry serves through the remote repository (a pull-through to ghcr.io), and a Batch job may execute only an image of this installation's own STANDARD repository -- localcfg's batch.image grammar, the published block's reader and doctor's batch-job-image all refuse a pull-through path (design compute-backend.md §4.4; §2.1's \"fugaro-base at the installation's pinned version\" is the one reading this disagrees with). Build this installation's own base, push it into " + host + "/" + infra.BaseRegistry + " (sh images/build-base.sh " + config.BaseKind + " tags it locally; the push is yours), record it with fugaro setup --base-image " + config.BaseKind + "=<the ref you pushed>, then run fugaro setup --provision-batch again -- docs/backends.md, \"The batch block\", walks the whole path"
	}
	// The host is <region>-docker.pkg.dev/<project> and is the WHOLE prefix of
	// every image of this installation's registries (infra.RegistryHost,
	// which refuses a host naming another project), so what follows it is a
	// repository of this project's own and never another project's.
	if strings.HasPrefix(pin, host+"/") && localcfg.ValidBatchImage(pin) {
		return pin, ""
	}
	return "", "the batch block was not recorded: this installation's base image " + pluginwire.Printable(pin) + " is not an image of this installation's own Artifact Registry (" + host + "/...), and " + field + " has to be one -- the published block's reader holds it to the installation's own project, and doctor reads the repository it is served from (design compute-backend.md §4.4); point the base at an image of your own in this project (fugaro setup --base-image " + config.BaseKind + "=IMAGE), then run fugaro setup --provision-batch again"
}

// batchBlockAccount is the account the block records as the one a job's VMs run
// as, and the line saying why it records none.
//
// One account is written, and never a choice among several: the run accounts
// are per (profile, account) pair (infra.JobRunAccounts), the block carries ONE
// for every workflow of the installation, and a pick among them would put a
// run of one profile under another profile's reach -- which SECURITY.md's "The
// IAM boundary is unchanged" accepts only because Cloud Run's shared job
// already runs every repository on one account, and does not accept across
// profiles. So an installation with more than one gets a block without the
// field and the reason beside it, and the owner writes the one their runs may
// use; infra.JobRunAccounts sorts its answer, so a single account is written
// the same way on every run.
func batchBlockAccount(members []string) (email, why string) {
	switch len(members) {
	case 0:
		return "", ""
	case 1:
		_, e, _ := strings.Cut(members[0], ":")
		return e, ""
	}
	return "", fmt.Sprintf("the batch block records no service_account: this project holds %d Fugaro run accounts (%s), one per (profile, account) pair, and the block carries one account for every workflow of the installation; set batch.service_account to the account your runs may act as", len(members), strings.Join(members, ", "))
}

// mergeBatch is the block that gets written: what this stage formed, with every
// field the owner has already written left alone. The owner's value wins field
// by field -- a hand-set machine type, a network they placed the VMs in, an
// image they built themselves, a socket gid they measured -- and the fields
// they left empty take this stage's answer. The block has no field this stage
// CLEARS: an owner value is never replaced with an empty one. The opt-in fact is
// carried the same way and is the reason the rule matters most: this stage forms
// its block out of the root's outputs and has no answer for it, so a block
// written without it would delete what an operator wrote (R2, R15).
//
// Two of the block's fields are NOT carried, and that is the whole of what this
// stage does to them: the key fact and `secrets` are facts the block no longer
// carries (R10), so the block this merge returns holds neither, and a file that
// carried one loses it at the first write. Neither is named here -- not the
// field and not its type -- for retireBlockFacts' reason: what removes them is
// the deletion that follows this task.
func mergeBatch(owner, want *localcfg.Batch) *localcfg.Batch {
	if owner == nil {
		return want
	}
	out := *want
	if owner.Image != "" {
		out.Image = owner.Image
	}
	if owner.MachineType != "" {
		out.MachineType = owner.MachineType
	}
	if owner.BootImage != "" {
		out.BootImage = owner.BootImage
	}
	if owner.Network != "" {
		out.Network = owner.Network
	}
	if owner.Subnetwork != "" {
		out.Subnetwork = owner.Subnetwork
	}
	if owner.ServiceAccount != "" {
		out.ServiceAccount = owner.ServiceAccount
	}
	if owner.SocketGID != 0 {
		out.SocketGID = owner.SocketGID
	}
	if owner.TaskTimeout.Set {
		out.TaskTimeout = owner.TaskTimeout
	}
	if owner.OptIn != "" {
		out.OptIn = owner.OptIn
	}
	return &out
}

// sameBatch reports whether two blocks carry the same fields, nil included. The
// two facts a file may still carry and the block no longer writes are compared
// as they are read -- the retired one above all, which the merge drops and so
// can only be told apart from a block without it by what the FILE holds
// (retireBlockFacts), never by a comparison here.
func sameBatch(a, b *localcfg.Batch) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Image == b.Image && a.MachineType == b.MachineType && a.BootImage == b.BootImage &&
		a.Network == b.Network && a.Subnetwork == b.Subnetwork && a.ServiceAccount == b.ServiceAccount &&
		maps.Equal(a.Secrets, b.Secrets) && a.SocketGID == b.SocketGID && a.TaskTimeout == b.TaskTimeout &&
		a.OptIn == b.OptIn
}

// announceBackend is the one line the stage ends with while the installation
// has not selected the backend it just provisioned: the command that does, and
// nothing beside it. The stage does not select it -- every launch of the
// installation would move onto VMs, the reaper deletes resources there, and
// the block this run wrote carries no confirmation for either -- and it is not
// left unsaid either: the provisioning is money already spent, and an owner
// who reads nothing here would go looking for the block by hand, which is what
// this stage exists to spare them.
func (s *computeStage) announceBackend() {
	if s.e.lc.Backend() == backend.CloudBatch {
		return
	}
	// A config the block is missing from is not one this line may end with:
	// selecting cloud-batch is refused for it (backendsUseGate), so the
	// sentence would send the owner to a command that answers with the
	// provisioning they have just run, and the line above it — the reason the
	// block was not recorded — is the one to act on.
	if s.e.lc.Batch == nil {
		return
	}
	fmt.Fprintf(s.e.r.w, "this installation still runs on %s: select the backend with fugaro backends use %s\n", s.e.lc.Backend(), backend.CloudBatch)
}

// registryRepository is the Artifact Registry repository the installation's
// base pin names: the remote fugaro-ghcr when the pin goes through it, else
// the shared fugaro-base. It is read from the pin rather than written out by
// hand, because §7.2's finding is that a grant aimed at the wrong one leaves
// every agent unable to pull.
func registryRepository(lc *localcfg.Config, outs infra.InstallationOutputs) string {
	// The host comes from the installation's outputs when the local config has
	// none yet: on a first run --provision-batch the config is written by this
	// very run, so the loaded one may not carry the line yet, and reading the
	// pin against an empty host would silently aim the grant at the shared
	// repository the pin does not go through.
	host := lc.RegistryHost
	if host == "" {
		host = outs.RegistryHost
	}
	if pin := lc.BaseImage(config.BaseKind); pin != "" && host != "" && strings.HasPrefix(pin, host+"/"+infra.GhcrRegistry+"/") {
		return infra.GhcrRegistry
	}
	if outs.BaseRegistry != "" {
		return outs.BaseRegistry
	}
	return infra.BaseRegistry
}
