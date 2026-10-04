package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
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
	outs, err := r.readOutputs(ctx, e.c, e.t, e.wd, e.spec)
	if err != nil {
		return initErr(err)
	}
	// The installation's own launchers and operators are what a later plan
	// compares with: an empty list here would plan their removal.
	next := *e.lc
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
	for _, line := range roleNotes(outs) {
		fmt.Fprintln(r.w, line)
	}
	e.adopted = "adopted the existing installation: wrote the local config, applied nothing"
	return nil
}

// roleNotes names the roles a new member may lack and the one-line command
// an owner runs to grant them (--launcher and --operator replace the lists,
// so the command carries the current members).
func roleNotes(outs infra.InstallationOutputs) []string {
	cmd := func(flag, you string, have []string) string {
		parts := []string{selfCommand(), "init"}
		for _, m := range have {
			parts = append(parts, flag, m)
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
