package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

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
	fmt.Fprint(s.e.r.w, tf.Summary(plan))
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
	if err := r.guard(ctx, e.c, plan, r.o.allowDelete); err != nil {
		return initflow.Outcome{}, err
	}
	if err := s.grants(plan); err != nil {
		return initflow.Outcome{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts

	// The confirmation, typed at a real terminal: --yes never covers this
	// stage, and a run that cannot ask leaves it for the owner.
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
	if err := infra.RunSchedulerJob(ctx, e.c, e.spec.Project, e.spec.Region, s.spec.Names.PreloadSchedulerJob); err != nil {
		return initflow.Outcome{}, &initflow.StageError{Err: err,
			Fix: "start it yourself: gcloud scheduler jobs run " + s.spec.Names.PreloadSchedulerJob + " --location=" + e.spec.Region + " --project=" + e.spec.Project}
	}
	if err := s.readOutputs(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(r.w, "provisioned; the preloaded boot image %s is being built now (Cloud Build, about two minutes), and rebuilt on its schedule; each generation records the socket's gid under its %s label, read off the builder VM\n", s.image, s.gidLabel)
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

// computeApplyWaits are the waits before each retry of an apply that failed
// on IAM propagation: about four minutes in all. A variable so a test can make
// them instant.
var computeApplyWaits = []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second}

// iamGap is how an apply reports a binding that names a service account IAM
// does not see yet: "Service account <email> does not exist".
var iamGap = regexp.MustCompile(`(?i)service\s+account\s+\S+\s+does\s+not\s+exist`)

// apply applies the confirmed plan, and waits out IAM propagation (design
// compute-backend §7.2 finding 5, §8.1 task 6): the module's bindings name the
// accounts it creates, so Terraform orders each account before its bindings,
// but a service account is not visible to IAM binding calls for a while after
// it is created, and ordering alone does not wait for that. So an apply that
// fails with "Service account ... does not exist" waits, re-plans, and applies
// what is left -- and only when the new plan passes the guard and the grant
// rules and every change in it is a change the confirmed plan held with the
// same actions, which is what lets it go ahead without asking again. Any other
// failure, a gap that outlasts the waits, or a re-plan holding anything new is
// the stage's error.
func (s *computeStage) apply(ctx context.Context, confirmed *tf.Plan) error {
	r := s.e.r
	for attempt := 0; ; attempt++ {
		err := s.t.Apply(ctx, infra.PlanFile)
		if err == nil {
			return nil
		}
		if !iamGap.MatchString(err.Error()) || attempt >= len(computeApplyWaits) {
			return remote(err)
		}
		wait := computeApplyWaits[attempt]
		fmt.Fprintf(r.w, "a service account this apply created is not visible to IAM yet (design compute-backend §7.2); retrying what is left in %s\n", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		changed, err := s.t.Plan(ctx, infra.PlanFile)
		if err != nil {
			return remote(err)
		}
		if !changed {
			return nil
		}
		next, err := s.t.Show(ctx, infra.PlanFile)
		if err != nil {
			return remote(err)
		}
		if err := r.guard(ctx, s.e.c, next, r.o.allowDelete); err != nil {
			return err
		}
		if err := s.grants(next); err != nil {
			return err
		}
		if extra := unconfirmed(next, confirmed); len(extra) > 0 {
			return userErr("the retry's plan holds changes the confirmed plan did not (%s); nothing more was applied: run init --provision-batch again to review and confirm them", strings.Join(extra, ", "))
		}
	}
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
		PreloadRefreshDays: infra.PreloadRefreshDaysDefault,
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
	s.t, s.spec = t, spec
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
