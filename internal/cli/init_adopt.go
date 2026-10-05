package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// Adopt mode (design §3.1, "A new team member"): the installation exists
// (its Terraform state is in the state bucket) and this machine has no local
// config for it. The installation stage then does what init --config-only
// does, writing the config from the installation's outputs after its own
// confirmation, and applies nothing: the installation is the owner's, made
// by their init, and a plan from this person's empty defaults would try to
// undo it. Once the config exists the next stages and every rerun see the
// installation as it is (No changes).

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

// adopt writes the local config from the existing installation and says
// what the person may lack, with the one-line command an owner runs.
func (r *initRun) adopt(ctx context.Context, e *initEngine) error {
	fmt.Fprintf(r.w, "adopt mode: the installation of GCP project %s already exists (its Terraform state is in gs://%s) and this machine has no local config for it: init writes the local config from it and applies nothing.\n", e.spec.Project, e.spec.StateBucket)
	if r.o.planOnly {
		return nil
	}
	if _, err := os.Lstat(e.path); err == nil {
		return userErr("%s already exists: adopt mode writes only a new local config and never overwrites one; use fugaro init --config-only to update it", e.path)
	}
	outs, err := r.readOutputs(ctx, e.c, e.t, e.wd, e.spec)
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
	if !r.o.launchersChanged && len(next.Terraform.Launchers) == 0 {
		next.Terraform.Launchers = slices.Clone(outs.Launchers)
	}
	if !r.o.operatorsChanged && len(next.Terraform.Operators) == 0 {
		next.Terraform.Operators = slices.Clone(outs.Operators)
	}
	r.adoptBudget(ctx, e, outs, &next)
	if err := r.configOnly(ctx, e.c, e.t, e.wd, &next, e.spec, e.path, e.old); err != nil {
		return err
	}
	r.res.Applied = false
	for _, line := range adoptNotes(outs) {
		fmt.Fprintln(r.w, line)
	}
	e.adopted = "adopted the existing installation: wrote the local config, applied nothing"
	return nil
}

// adoptGuard refuses, for a stage that would change the cloud, to apply in the
// run that adopted an installation: adopt mode is for a teammate's machine,
// whose plan from empty defaults must not be applied (adoptNotes says so). The
// owner applies; a rerun here, with the config in place, is the ordinary
// converge. The plugin wiring writes a local file only (.claude/settings.json) and is not
// guarded: an adopting run still wires the plugin, with its own confirmation.
func (e *initEngine) adoptGuard(stage string) error {
	if e.adopted == "" || stage == initflow.Installation || stage == initflow.Plugin {
		return nil
	}
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: stage, Kind: initflow.LeftConsole,
		Text:     "this run adopted an existing installation and changed nothing in the cloud; it changes nothing more there from here (only the plugin wiring still writes a local .claude/settings.json): an owner applies changes (the notes above say how), then rerun this once your roles are granted",
		Commands: []string{selfCommand() + " init"}}}
}

// adoptNotes is what the person is told after adopting: that the config is a
// copy of what the outputs expose, which flags the owner passes for what they
// do not, that a plan from here must not be applied, and the roles.
func adoptNotes(outs infra.InstallationOutputs) []string {
	lines := []string{
		"the local config was copied from the installation's outputs: launchers, operators, registry cleanup (on, or dry-run when the outputs say a dry run), log isolation and the budget backend's history account; they do not carry the alert email, the scheduler region, whether cleanup is dry-run or off, or the Firebase project,",
		"so a plan from this machine can differ from what the owner applied and look noisy: do not apply an installation plan from here; ask an owner to run fugaro init, passing --alert-email, --scheduler-region, --registry-cleanup and --firebase as they did",
	}
	return append(lines, roleNotes(outs)...)
}

// roleNotes names the roles a new member may lack and the one-line command
// an owner runs to grant them (--launcher and --operator replace the lists,
// so the command carries the current members).
func roleNotes(outs infra.InstallationOutputs) []string {
	cmd := func(flag, you string, have []string) string {
		parts := []string{selfCommand(), "init"}
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
		fmt.Sprintf("to launch and watch runs you need the launcher role, held by: %s; if you are not listed, ask an owner to run: %s", list(outs.Launchers), cmd("--launcher", "user:<your-email>", outs.Launchers)),
		fmt.Sprintf("to onboard repositories you need the operator role, held by: %s; if you are not listed, ask an owner to run: %s", list(outs.Operators), cmd("--operator", "user:<your-email>", outs.Operators)),
	}
}

// adoptBudget fills next's budget section from the Firebase root's outputs
// (the realtime database's URL, the Firebase project, its restricted web API
// key and the token signer), so a teammate's watch and runs find the budget
// backend without an owner's init --firebase. Read only: terraform init and
// output in the Firebase root, which needs no variables and changes no state.
// None of the four is a secret (the API key is restricted by design) and
// nothing the outputs do not hold is written: not the mode, not the caps. An
// installation with no budget backend, no Firebase state, or outputs that can't
// be read or fail validation leaves the section as it is, saying why; the
// adoption itself goes on.
func (r *initRun) adoptBudget(ctx context.Context, e *initEngine, outs infra.InstallationOutputs, next *localcfg.Config) {
	if outs.HistoryServiceAccount == "" {
		return // no budget backend: nothing to find
	}
	skip := func(why string) {
		fmt.Fprintf(r.w, "the budget section of the local config was left empty (%s); watch and the budget commands need it: an owner's fugaro init --firebase <project> fills it, or rerun this once the Firebase root's state can be read\n", why)
	}
	switch ok, err := infra.StateExists(ctx, e.c, e.spec.StateBucket, infra.StatePrefixFirebase); {
	case err != nil:
		skip("the Firebase root's state could not be listed: " + oneLineCLI(err.Error()))
		return
	case !ok:
		skip("there is no Firebase root state in gs://" + e.spec.StateBucket + ", so the budget backend was not deployed with --firebase")
		return
	}
	fdir, err := infra.FirebaseWorkdir(os.Getenv, e.lc.GCPProject)
	if err != nil {
		skip(oneLineCLI(err.Error()))
		return
	}
	fwd, ft, err := r.terraform(e.bin, fdir, "firebase")
	if err != nil {
		skip(oneLineCLI(err.Error()))
		return
	}
	backend, err := fwd.WriteBackend(e.spec.StateBucket, infra.StatePrefixFirebase)
	if err != nil {
		skip(oneLineCLI(err.Error()))
		return
	}
	if err := ft.Init(ctx, backend); err != nil {
		skip("terraform init in the Firebase root failed: " + oneLineCLI(err.Error()))
		return
	}
	raw, err := ft.Output(ctx)
	if err != nil {
		skip("the Firebase root's outputs could not be read: " + oneLineCLI(err.Error()))
		return
	}
	fo, err := infra.DecodeFirebaseOutputs(raw)
	if err != nil {
		skip(oneLineCLI(err.Error()))
		return
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
		skip(oneLineCLI(err.Error()))
		return
	} else if _, err := localcfg.Parse(data); err != nil {
		skip("an output of the Firebase root is not valid: " + oneLineCLI(err.Error()))
		return
	}
	next.Budget = &b
	fmt.Fprintf(r.w, "the budget section of the local config was filled from the Firebase root's outputs (project %s, its realtime database, web API key and token signer; none is a secret): watch and runs can use the budget backend\n", pluginwire.Printable(fo.FirebaseProject))
}
