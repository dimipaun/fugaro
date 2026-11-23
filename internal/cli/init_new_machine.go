package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// A new machine (design §3.1, "A new team member", and the owner's own second
// machine): the installation exists (its Terraform state is in the state
// bucket) and this machine has no local config for it. That is `fugaro
// update`'s case. `fugaro setup` makes a project Fugaro's only when it holds
// nothing of Fugaro's, so it refuses one an earlier run of setup installed
// into before it gets here (init_project.go), and the one
// setup run that reaches this is the creator's own resume, after an earlier
// run applied the installation and stopped before writing its config. The installation
// stage then does what update --config-only does, writing the config from the
// installation's outputs after its own confirmation, and applies nothing: the
// installation was made by its owner's fugaro setup, and a plan from this
// machine's empty defaults would try to undo it. Once the config exists the
// next stages and every rerun see the installation as it is (No changes).

// installationExists reports whether the state bucket holds the installation's
// state. Read-only.
func (r *initRun) installationExists(ctx context.Context, c *infra.Clients, spec infra.InstallationSpec) (bool, error) {
	ok, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil || !ok {
		return false, initErr(err)
	}
	has, err := infra.InstallationStateExists(ctx, c, spec.StateBucket)
	return has, initErr(err)
}

// newMachineConfig writes the local config from the existing installation and
// says what the person may lack, with the one-line command an owner runs.
func (r *initRun) newMachineConfig(ctx context.Context, e *initEngine) error {
	fmt.Fprintf(r.w, "new machine: the installation of GCP project %s already exists (its Terraform state is in gs://%s) and this machine has no local config for it: this run writes the local config from it and applies nothing.\n", e.spec.Project, e.spec.StateBucket)
	if r.o.planOnly {
		return nil
	}
	if _, err := os.Lstat(e.path); err == nil {
		return userErr("%s already exists: a new machine's run writes only a new local config and never overwrites one; use fugaro update --config-only to update it", e.path)
	}
	outs, err := r.readOutputs(ctx, e.c, e.t, e.wd, e.lc, e.spec)
	if err != nil {
		return initErr(err)
	}
	// What the installation's outputs say about it is what a later plan
	// compares with: an empty launcher list would plan their removal, and a
	// default cleanup mode or log isolation would plan a change back.
	next := *e.lc
	if !r.o.registryCleanupSet() && next.Terraform.RegistryCleanup == "" && outs.RegistryCleanupDryRun != nil && !*outs.RegistryCleanupDryRun {
		next.Terraform.RegistryCleanup = "on" // the outputs say a dry run is off: cleanup is on
	}
	if !r.o.noLogIsolation && outs.LogView == "" {
		next.Terraform.NoLogIsolation = true
	}
	if outs.HistoryServiceAccount != "" {
		next.Terraform.BudgetBackend = true
	}
	if outs.CoordinatorServiceAccount != "" {
		next.Terraform.Coordinator = true
	}
	if !r.o.operatorsChanged && len(next.Terraform.Operators) == 0 {
		next.Terraform.Operators = slices.Clone(outs.Launchers)
	}
	if !r.o.adminsChanged && len(next.Terraform.Admins) == 0 {
		next.Terraform.Admins = slices.Clone(outs.Operators)
	}
	r.newMachineBudget(ctx, e, &next)
	if err := r.configOnly(ctx, e.c, e.t, e.wd, &next, e.spec, e.path, e.old); err != nil {
		return err
	}
	r.res.Applied = false
	for _, line := range newMachineNotes(outs, r.o.doorName()) {
		fmt.Fprintln(r.w, line)
	}
	e.newMachine = "wrote this machine's local config from the existing installation, applied nothing"
	return nil
}

// newMachineGuard refuses, for a stage that would change the cloud, to apply in
// the run that wrote a new machine's config: that run's plan from empty
// defaults must not be applied (newMachineNotes says so). The owner applies; a
// rerun here, with the config in place, is the ordinary converge. The stages
// that are only this machine's are not guarded (initflow.MachineLocal — the
// plugin stage): the run still installs the plugin in the reader's own Claude
// Code, which is what the command they ran came for, and a stage that runs past
// the cloud stop this refusal makes is one the guard must not stop either.
func (e *initEngine) newMachineGuard(stage string) error {
	if e.newMachine == "" || stage == initflow.Installation || initflow.MachineLocal(stage) {
		return nil
	}
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: stage, Kind: initflow.LeftConsole,
		Text:     "this run wrote this machine's local config from an existing installation and changed nothing in the cloud; it changes nothing more there from here: an owner applies changes (the notes above say how), then rerun this once your roles are granted",
		Commands: []string{selfCommand() + " " + e.r.o.doorName()}}}
}

// newMachineNotes is what the person is told after the config is written: that it is a
// copy of what the outputs expose, which flags the owner passes for what they
// do not, that a plan from here must not be applied, and the roles.
func newMachineNotes(outs infra.InstallationOutputs, door string) []string {
	lines := []string{
		"the local config was copied from the installation's outputs: operators, admins, registry cleanup (on, or dry-run when the outputs say a dry run), log isolation and the budget backend's history account; they do not carry the alert email, the scheduler region, whether cleanup is dry-run or off, or the Firebase project,",
		"so a plan from this machine can differ from what the owner applied and look noisy: do not apply an installation plan from here; ask an owner to run fugaro update, passing --alert-email, --scheduler-region, --registry-cleanup and --firebase-project as they did",
	}
	return append(lines, roleNotes(outs, door)...)
}

// roleNotes names the roles a new member may lack and the one-line command
// an owner runs to grant them (--operator and --admin replace the lists,
// so the command carries the current members).
func roleNotes(outs infra.InstallationOutputs, door string) []string {
	cmd := func(flag, you string, have []string) string {
		parts := []string{selfCommand(), door}
		for _, m := range have {
			parts = append(parts, flag, quoteWord(m))
		}
		return strings.Join(append(parts, flag, you), " ")
	}
	list := func(m []string) string {
		if len(m) == 0 {
			return "nobody"
		}
		return strings.Join(m, ", ")
	}
	return []string{
		fmt.Sprintf("to launch and watch runs you need the operator role, held by: %s; if you are not listed, ask an owner to run: %s", list(outs.Launchers), cmd("--operator", "user:<your-email>", outs.Launchers)),
		fmt.Sprintf("to onboard repositories you need the admin role, held by: %s; if you are not listed, ask an owner to run: %s", list(outs.Operators), cmd("--admin", "user:<your-email>", outs.Operators)),
	}
}

// budgetDatabaseFromState fills next's budget block from the Firebase root's
// recorded outputs — the realtime database's URL, the Firebase project, its
// restricted web API key and the token signer — so that a machine which has no
// database of its own adopts the installation's. Read only: terraform init and
// output in the Firebase root, which needs no variables and changes no state.
//
// It is the adoption half of both doors that meet a database which exists and is
// unknown on this machine: a teammate's new machine (`fugaro update` on a
// machine that has never held a config for the installation) and `fugaro update`
// itself with no --firebase-project (AB7, R2). Two rules are its own, and both
// are about a document anyone who can write the state bucket can write:
//
//   - The probe is the Firebase STATE's and never the history account's (F23).
//     The history account is an output of the installation root and says nothing
//     about whether the Firebase root was ever applied, while the state is what
//     that apply wrote.
//   - Every output is pinned to fp, the installation's OWN project (F24).
//     CheckFirebaseOutputsFor refuses an rtdb_url that is not that project's
//     database and a signer that is not its account, so a write to the state
//     bucket can never point every run's session at a foreign database.
//
// None of the four is a secret (the API key is restricted by design) and nothing
// the outputs do not hold is written: not the mode, not the caps. ok is whether
// the block was filled; why says why it was not, in one line ready to print.
func (r *initRun) budgetDatabaseFromState(ctx context.Context, c *infra.Clients, bin, stateBucket, fp string, next *localcfg.Config) (ok bool, why string) {
	switch exists, err := infra.StateExists(ctx, c, stateBucket, infra.StatePrefixFirebase); {
	case err != nil:
		return false, "the Firebase root's state could not be listed: " + oneLineCLI(err.Error())
	case !exists:
		return false, "there is no Firebase root state in gs://" + stateBucket + ", so the budget backend was not deployed with --firebase-project"
	}
	fdir, err := infra.FirebaseWorkdir(os.Getenv, next.GCPProject)
	if err != nil {
		return false, oneLineCLI(err.Error())
	}
	fwd, ft, err := r.terraform(bin, fdir, "firebase")
	if err != nil {
		return false, oneLineCLI(err.Error())
	}
	backend, err := fwd.WriteBackend(stateBucket, infra.StatePrefixFirebase)
	if err != nil {
		return false, oneLineCLI(err.Error())
	}
	if err := ft.Init(ctx, backend); err != nil {
		return false, "terraform init in the Firebase root failed: " + oneLineCLI(err.Error())
	}
	raw, err := ft.Output(ctx)
	if err != nil {
		return false, "the Firebase root's outputs could not be read: " + oneLineCLI(err.Error())
	}
	fo, err := infra.DecodeFirebaseOutputs(raw)
	if err != nil {
		return false, oneLineCLI(err.Error())
	}
	if err := infra.CheckFirebaseOutputsFor(fo, fp, next.Endpoints.NoAuth); err != nil {
		return false, oneLineCLI(err.Error())
	}
	b := localcfg.Budget{}
	if next.Budget != nil {
		b = *next.Budget
	}
	b.FirebaseProject, b.RTDBURL, b.FirebaseAPIKey, b.TokenSigner = fo.FirebaseProject, fo.RTDBURL, fo.FirebaseAPIKey, fo.TokenSigner
	trial := *next
	trial.Budget = &b
	// The outputs come from state anyone with the state bucket can write: each
	// goes through the config's own validation before it is kept.
	if data, err := trial.Marshal(); err != nil {
		return false, oneLineCLI(err.Error())
	} else if _, err := localcfg.Parse(data); err != nil {
		return false, "an output of the Firebase root is not valid: " + oneLineCLI(err.Error())
	}
	next.Budget = &b
	// The Firebase root's outputs are this run's too, and not only the copy of
	// them it is about to write into the config: the installation-2 stage runs
	// behind an adoption that reaches it (F29), and the history job it deploys
	// is composed from the adopted root's outputs (firebaseHistory refuses to
	// run without them).
	r.fb = &fo
	return true, ""
}

// newMachineBudget fills next's budget section from the Firebase root's outputs
// (budgetDatabaseFromState), so a teammate's watch and runs find the budget
// backend without an owner's update --firebase-project. What it cannot read
// leaves the section as it is, saying why; the config is still written.
func (r *initRun) newMachineBudget(ctx context.Context, e *initEngine, next *localcfg.Config) {
	ok, why := r.budgetDatabaseFromState(ctx, e.c, e.bin, e.spec.StateBucket, e.lc.GCPProject, next)
	if !ok {
		fmt.Fprintf(r.w, "the budget section of the local config was left empty (%s); watch and the budget commands need it: an owner's fugaro update --firebase-project %s fills it, or rerun this once the Firebase root's state can be read\n", why, e.spec.Project)
		return
	}
	fmt.Fprintf(r.w, "the budget section of the local config was filled from the Firebase root's outputs (project %s, its realtime database, web API key and token signer; none is a secret): watch and runs can use the budget backend\n", pluginwire.Printable(next.Budget.FirebaseProject))
}

// The installation's region, and a --region that is not it. `fugaro setup` and
// `fugaro update` are the commands that plan, and a plan in another region
// would replace the runs bucket (regional, upper(var.region)) and everything in
// it — refused today, but late, by Terraform's data-bearing rule. Those two
// commands refuse the typed region before anything is planned instead (D3088 as
// amended, LP8); every other command keeps --region as the per-invocation
// override it is. The loaders are where the refusal goes because they are the
// one place both doors, --repo and --plan-only all pass through.

// errRunsBucketMissing is the runs bucket not being there at all: nothing to
// compare a typed region with, which is a setup that is about to create it.
var errRunsBucketMissing = errors.New("the runs bucket does not exist")

// runsBucketLocation reads the location the runs bucket was created in, which
// is where the installation is. It is the only answer there is on a machine
// with no project config to hold the region (R6, F12): a bucket's location is
// fixed when it is made and cannot drift. Read-only; tests replace it, because
// a config that does not exist yet carries no endpoints for a fake to answer
// on.
var runsBucketLocation = func(ctx context.Context, lc *localcfg.Config, bucket string) (string, error) {
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return "", err
	}
	b, err := c.Storage.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return "", errRunsBucketMissing
		}
		return "", err
	}
	return b.Location, nil
}

// refuseAnotherRegion refuses a typed region that is not the installation's
// own, naming the installation's region, saying it cannot move, and printing
// the fix: the same command with that region (D3088 as amended, R6). The
// comparison folds case: the region it is handed may be a bucket's location,
// which the API spells in upper case.
func refuseAnotherRegion(o *initOptions, own string) error {
	if o.cloud.region == "" || strings.EqualFold(o.cloud.region, own) {
		return nil
	}
	return userErr("--region %s is not the installation's region %s: an installation's region is where it was created and cannot move (the runs bucket and the installation's data are regional). Run the same command with --region %s", o.cloud.region, own, own)
}

// checkNewRegion is refuseAnotherRegion for a machine that has no project
// config yet: with no region written down, the runs bucket's own location is
// the installation's (R6). A bucket that is not there says nothing — a fresh
// setup is about to create it — and a bucket whose location cannot be read is
// accepted with one line saying it was not checked (R10): refusing would stop
// a launcher's first update on a new machine for a permission the launcher is
// not meant to hold, and Terraform's own refusal of a bucket's replacement
// stays behind it.
func checkNewRegion(ctx context.Context, o *initOptions, lc *localcfg.Config) error {
	bucket := lc.RunsBucketName()
	loc, err := runsBucketLocation(ctx, lc, bucket)
	switch {
	case errors.Is(err, errRunsBucketMissing):
		return nil
	case err != nil, loc == "":
		fmt.Fprintf(o.cloud.errw(), "fugaro: note: --region %s was not checked against the runs bucket gs://%s, whose location could not be read\n", o.cloud.region, bucket)
		return nil
	}
	// The bucket answers the location the API's way, "US-EAST5"; a region a
	// --region and the config's own validation accept is lower case, and the
	// fix this refusal prints has to be one the flag takes.
	return refuseAnotherRegion(o, strings.ToLower(loc))
}
