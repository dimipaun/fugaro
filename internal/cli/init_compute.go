package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The compute stage: the Batch backend's installation-level provisioning
// (design docs/design/compute-backend.md §3.2, §3.7, §8.1 task 6). It is the
// owner's step, and it is behind --provision-batch: what it changes is not
// what an installation needs, `backend: cloud-batch` stays refused by config
// validation until the enable task (§8.1 task 9), and a run that never asks
// for it never reaches this root.
//
// It is money and it is permanent — a preloaded boot image's Cloud Build, a
// Cloud NAT's hours, a custom role and a scheduled job — so --yes cannot
// cover it (the stage is registered noYes) and its confirmation is the
// project's name typed at a real terminal (initflow.Typed), exactly as the
// project's creation and the secrets are.
//
// What it does NOT do: touch the installation's own root (a workdir and a
// state of its own, see infra.ComputeWorkdir), handle a secret, or delete
// anything it did not create — the plan passes tf.Guard like every other
// apply, and --allow-delete names any address on purpose.
type computeStage struct {
	e *initEngine
	// changes and applied mirror engineStage's, so the plan view and the
	// JSON result read the same either way.
	changes *infra.PlanCounts
	applied bool
	// image is the preloaded boot image the root's outputs named, and want
	// the name the pin derives: Verify compares them.
	image, want string
	// t and spec are the compute root this stage is working on, kept
	// between the plan, the apply and the outputs.
	t    *tf.TF
	spec infra.ComputeSpec
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
	return promptLeft(initflow.Compute, "type the project's name at the provisioning confirmation", "init", "--provision-batch")
}

// Verify re-reads what the apply read back: the preloaded boot image the root
// names is the one the installation's pinned base image derives, so a state
// that names something else is a stage that provisioned for a different pin.
func (s *computeStage) Verify(context.Context) error {
	if s.want == "" || s.image == "" || s.image == s.want {
		return nil
	}
	return fmt.Errorf("the compute root reports the preloaded image %s, but the installation's pin builds %s: rerun with --provision-batch so the boot image matches the image the runs pull", s.image, s.want)
}

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
	fmt.Fprint(s.e.r.w, tf.Summary(plan))
	if err := guard(plan, s.e.r.o.allowDelete); err != nil {
		return initflow.Plan{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts
	return s.e.planned(&counts), nil
}

// Apply plans, guards, confirms and applies the compute root, then starts the
// preload rebuild once: a fresh installation must not wait a week for its
// first preloaded image.
func (s *computeStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	e, r := s.e, s.e.r
	plan, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if plan == nil {
		s.changes = &infra.PlanCounts{}
		fmt.Fprintln(r.w, "No changes: the compute backend's provisioning matches the plan.")
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	fmt.Fprint(r.w, tf.Summary(plan))
	if err := guard(plan, r.o.allowDelete); err != nil {
		return initflow.Outcome{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts

	// The confirmation, typed at a real terminal: --yes never covers this
	// stage, and a run that cannot ask leaves it for the owner.
	what := fmt.Sprintf("provisions the Batch compute backend in GCP project %s: the Compute and Batch APIs, the run accounts' IAM, the launcher's, reaper's and preload accounts and roles, a Cloud NAT and router (billable while they exist), and a preloaded boot image built by a scheduled Cloud Build (%s to create)", e.lc.GCPProject, counts.String())
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
	if err := s.t.Apply(ctx, infra.PlanFile); err != nil {
		return initflow.Outcome{}, remote(err)
	}
	s.applied = true
	r.res.Applied = true

	// The preload rebuild, now rather than at the next schedule. A refusal
	// here is the stage's own error: the provisioning is done, and the one
	// line that starts the build is the fix.
	if err := infra.RunSchedulerJob(ctx, e.c, e.spec.Project, e.spec.Region, s.spec.Names.PreloadSchedulerJob); err != nil {
		return initflow.Outcome{}, &initflow.StageError{Err: err,
			Fix: "start it yourself: gcloud scheduler jobs run " + s.spec.Names.PreloadSchedulerJob + " --location=" + e.spec.Region + " --project=" + e.spec.Project}
	}
	if err := s.readOutputs(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(r.w, "provisioned; the preloaded boot image %s is being built now (Cloud Build, about two minutes), and rebuilt on its schedule\n", s.image)
	return initflow.Outcome{Changed: true, Detail: "applied: " + counts.String() + " (preload image " + s.image + ")"}, nil
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

// readOutputs is what the stage reports: the names the apply created, and the
// preloaded image's name, which Verify holds to the pin.
func (s *computeStage) readOutputs(ctx context.Context) error {
	raw, err := s.t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	outs, err := infra.DecodeComputeOutputs(raw)
	if err != nil {
		return remote(err)
	}
	s.image = outs.PreloadImage
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
	outs, err := e.r.readOutputs(ctx, e.c, e.t, e.wd, e.spec)
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
	spec := infra.ComputeSpec{
		Project:            e.spec.Project,
		ProjectNumber:      fmt.Sprint(number),
		Region:             e.spec.Region,
		Network:            infra.DefaultNetwork(e.spec.Project),
		Subnetwork:         infra.DefaultSubnetwork(e.spec.Project, e.spec.Region),
		Names:              infra.ComputeNamesFor(),
		ManageAPIs:         e.spec.ManageAPIs,
		Launchers:          e.spec.Launchers,
		Operators:          e.spec.Operators,
		RunAccounts:        runAccounts,
		RegistryRepository: registryRepository(e.lc, outs),
		BaseImage:          pin,
		BaseProject:        "batch-custom-image",
		BaseFamily:         "batch-cos-stable-official",
		PreloadZone:        e.spec.Region + "-a",
		PreloadSchedule:    "0 3 * * 1",
		BuilderMachineType: "e2-medium",
		BuildSubmitterRole: outs.RoleIDs.BuildSubmitter,
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
	s.t, s.spec, s.want = t, spec, infra.PreloadedImage(spec.BaseImage)
	return nil
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
