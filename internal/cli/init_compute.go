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
)

// The compute stage: the Batch backend's installation-level provisioning
// (design docs/design/compute-backend.md §3.2, §3.7, §8.1 task 6). It is the
// owner's step, and it is behind --provision-batch: what it creates is money
// and infrastructure `fugaro update` does not apply, and a run that never asks
// for it never reaches this root.
//
// It is money and it is permanent — a preloaded boot image's Cloud Build, a
// Cloud NAT's hours, a custom role and a scheduled job — so --yes cannot
// cover it (the stage is registered noYes, and askTyped's own class refuses it
// too) and its confirmation is the owner's own session: a person at a real
// terminal is what applies it, and nothing is typed at them (D1540). A coding
// agent's session is refused rather than left for the owner like a session
// that merely cannot answer (D1555, Apply), and what it reads beside the
// refusal is the command its owner runs themselves (Left).
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
}

func newComputeStage(e *initEngine) *computeStage { return &computeStage{e: e} }

func (s *computeStage) Name() string { return initflow.Compute }

// Check cannot know without planning whether anything is missing, so it says
// what it will do; Plan does the reading, and a Plan that finds nothing is
// the stage being done.
func (s *computeStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}

func (s *computeStage) Left() initflow.Left {
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
		if !slices.ContainsFunc(acts, func(a string) bool { return a != "no-op" && a != "read" }) {
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
	// VM cannot report its state (see infra.JobRunAccounts).
	runAccounts, err := infra.JobRunAccounts(ctx, e.c, e.spec.Project)
	if err != nil {
		return initErr(err)
	}
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
		RunAccounts:        runAccounts,
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
// images stage records a pin, and the stage's own typed confirmation is what
// covers it, exactly as it covers that one.
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
	if sameBatch(merged, owner) {
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
	account, why := batchBlockAccount(s.spec.RunAccounts)
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
		return "", "the batch block was not recorded: this installation's base image is the release PIN " + pluginwire.Printable(pin) + ", which this installation's registry serves through the remote repository (a pull-through to ghcr.io), and a Batch job may execute only an image of this installation's own STANDARD repository -- localcfg's batch.image grammar, the published block's reader and doctor's batch-job-image all refuse a pull-through path (design compute-backend.md §4.4; §2.1's \"fugaro-base at the installation's pinned version\" is the one reading this disagrees with). Build this installation's own base, push it into " + host + "/" + lc.GCPProject + "/" + infra.BaseRegistry + " (sh images/build-base.sh " + config.BaseKind + " tags it locally; the push is yours), record it with fugaro setup --base-image " + config.BaseKind + "=<the ref you pushed>, then run fugaro setup --provision-batch again -- docs/backends.md, \"The batch block\", walks the whole path"
	}
	if strings.HasPrefix(pin, host+"/"+lc.GCPProject+"/") && localcfg.ValidBatchImage(pin) {
		return pin, ""
	}
	return "", "the batch block was not recorded: this installation's base image " + pluginwire.Printable(pin) + " is not an image of this installation's own Artifact Registry (" + host + "/" + lc.GCPProject + "/...), and " + field + " has to be one -- the published block's reader holds it to the installation's own project, and doctor reads the repository it is served from (design compute-backend.md §4.4); point the base at an image of your own in this project (fugaro setup --base-image " + config.BaseKind + "=IMAGE), then run fugaro setup --provision-batch again"
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
// CLEARS: an owner value is never replaced with an empty one.
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
	if owner.Secrets != nil {
		out.Secrets = owner.Secrets
	}
	if owner.SocketGID != 0 {
		out.SocketGID = owner.SocketGID
	}
	if owner.TaskTimeout.Set {
		out.TaskTimeout = owner.TaskTimeout
	}
	return &out
}

// sameBatch reports whether two blocks carry the same fields, nil included.
func sameBatch(a, b *localcfg.Batch) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Image == b.Image && a.MachineType == b.MachineType && a.BootImage == b.BootImage &&
		a.Network == b.Network && a.Subnetwork == b.Subnetwork && a.ServiceAccount == b.ServiceAccount &&
		maps.Equal(a.Secrets, b.Secrets) && a.SocketGID == b.SocketGID && a.TaskTimeout == b.TaskTimeout
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
