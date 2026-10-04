package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
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
// converge. The plugin wiring writes a local file only and is not guarded.
func (e *initEngine) adoptGuard(stage string) error {
	if e.adopted == "" || stage == initflow.Installation || stage == initflow.Plugin {
		return nil
	}
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: stage, Kind: initflow.LeftConsole,
		Text:     "this run adopted an existing installation and applied nothing; it applies nothing more from here: an owner applies changes (the notes above say how), then rerun this once your roles are granted",
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
