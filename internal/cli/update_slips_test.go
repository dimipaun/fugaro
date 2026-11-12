// update_slips_test.go is DS4.1 under the door CLAUDE.md's HARD RULE makes the
// whole upgrade: an installation the release behind left behind reaches this
// one with `fugaro update` alone, and nothing else. The convergence tests of
// update_converge_test.go are about the stages that move every installation
// (the rules generation, the base image, the plugin, the node); this file is
// about the two halves the dynamic delivery class adds to that list.
//
// The launcher class (D1938, F25) is the list the secrets intake and the
// credentials service answer to. Cloud Run grants the invoker per service, so
// the roster's people pass IAM and then meet the process's own check, and the
// class has to name exactly the people the grant is made from — a roster that
// reached one list and not the other is a caller IAM admits and the process
// refuses. Nothing wrote a class before this release, so an installation at
// the previous version holds none, and the run that converges it is the run
// that writes it, from the installation root's own recorded roster. A group is
// left out of it: a token carries an address and not a group, and the class
// refuses what it cannot match rather than admitting it (launchers.go).
//
// The node is DS3.1's in-place migration. The version the release behind
// published is carried forward by the same run, keeping what its owner
// published, because the alternative — the operator step 0.14.0's notes asked
// for, `fugaro project policy publish --delete` and a fresh publish — is a
// second command, and the HARD RULE has no second command.
//
// What this file deliberately does NOT pin, and why: the intake service's own
// deploy. `var.intake` (modules/installation/intake.tf) has no writer in this
// tree — internal/infra's InstallationSpec carries no block of that name and no
// stage of this package writes one — and the service is served by an image no
// release builds (images/ has no intake kind, and .github/workflows/images.yml
// builds history, base and credentials), so a fixture could not reach a
// deployed intake with `fugaro update` alone. That gap is reported with this
// task; a test asserting the intake here would fail on a tree whose other half
// is right, and one asserting its absence would pin the gap as the contract.

package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestUpdateConvergesFromPreviousInstallation is the dynamic delivery half of
// "an installation at the previous version converges": one `fugaro update`,
// with no other step, leaves the installation root carrying the roster's
// launcher class and the published node at this binary's version with the
// owner's own settings still in it.
func TestUpdateConvergesFromPreviousInstallation(t *testing.T) {
	const (
		launcher = "person-1@example.invalid"
		operator = "person-2@example.invalid"
	)
	r := newConvergeRig(t, staleNode(t))
	// The roster the installation root reports, which is the one the class is
	// derived from: a launcher, an operator (who launches too), and a group.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"launchers": []string{"user:" + launcher, "group:team@example.invalid"},
		"operators": []string{"user:" + operator},
	})}
	r.save(t)

	out := convergeOn(t, r, []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})

	// The class, in the installation root's own variables: the people of the
	// roster as the addresses a Google ID token carries, sorted and each once,
	// with the group left out. An installation the update left without one
	// would meet a service whose class admits nobody, which is fail-closed and
	// still not converged: the people who launch are refused by the process IAM
	// let through.
	if got, want := launcherAccountsOf(t, r), []string{launcher, operator}; !slices.Equal(got, want) {
		t.Errorf("after one fugaro update the installation root carries launcher_accounts = %v, want %v: the class the intake and the credentials service answer to is the roster's people, and nobody a group might mean", got, want)
	}

	// The node the release behind published, carried forward IN PLACE: this
	// binary's version, and the settings its owner published still there. A run
	// that republished the node from the local config instead would leave an
	// installation whose policy is not the one the owner wrote, and one that
	// left the version where it was would leave a reader refusing it.
	p := convergeNode(t, r)
	if p.Version != projectpolicy.Version {
		t.Errorf("after one fugaro update the published node is version %d, want %d", p.Version, projectpolicy.Version)
	}
	if p.Budget.Mode != budget.ModeEnforce || !slices.Equal(p.AllowRepos, []string{"github/acme/app"}) {
		t.Errorf("the migrated node reads budget.mode %q allow_repos %v, want the fixture's own enforce and [github/acme/app]: the migration carries the node forward and does not republish it", p.Budget.Mode, p.AllowRepos)
	}

	// And no operator step, in the transcript the person actually reads: the
	// delete-and-publish-again the previous bump asked for is what this run
	// replaces, so naming it here would send the reader to a second command.
	if strings.Contains(out, "--delete") {
		t.Errorf("the run names a second command (--delete), which is the operator step this one replaces:\n%s", out)
	}
}

// TestUpdateAsksNoRunToFinish is the same convergence with a run of the
// installation's own repository in flight across it, and the fixture is the
// node the release behind really left (newestRefusedVersion, the state the
// upgrade meets on the day) rather than the oldest this chain still carries
// (TestUpdateLeavesARunInFlightRunning's v3 node). The door asks nothing of
// that run: the node is migrated out of the version its own binary refuses, the
// class is written, and everything the run writes afterwards still lands.
//
// A door that asked the run to finish first would show itself here as one of
// two things, and both are checked: the node it held back at the refused
// version (a policy write refused over a run), or a run whose later writes met
// a path or an object the update had taken away.
func TestUpdateAsksNoRunToFinish(t *testing.T) {
	const launcher = "person-1@example.invalid"
	r := newConvergeRig(t, staleNode(t))
	// The person whose run is in flight is a launcher of this installation, and
	// the run is of its repository: the update that writes the class is the
	// update that has to keep admitting them.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"launchers": []string{"user:" + inFlightOwner, "user:" + launcher},
	})}
	r.save(t)
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})

	line := inFlightLine(t, out)
	if !strings.Contains(line, "asks no run to finish first") {
		t.Errorf("the update does not say it asks no run to finish first: %q", line)
	}
	if got := convergeNode(t, r).Version; got != projectpolicy.Version {
		t.Errorf("with a run in flight the update left the published node at version %d, want %d: that is a door holding a policy write for a run rather than asking it to finish", got, projectpolicy.Version)
	}
	if got, want := launcherAccountsOf(t, r), []string{inFlightOwner, launcher}; !slices.Equal(got, want) {
		t.Errorf("with a run in flight the installation root carries launcher_accounts = %v, want %v: the update that writes the class is the update that keeps admitting the person who launched it", got, want)
	}

	run.laterWrites(t, r)
}

// launcherAccountsOf is the class the installation root's own variables carry,
// read back as the addresses it holds. It is the launcher_accounts key and not
// the roster the run read: this is the list a service's process compares a
// caller's token against.
func launcherAccountsOf(t *testing.T, r *convergeRig) []string {
	t.Helper()
	vars := r.tfvars(t)
	raw, ok := vars["launcher_accounts"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("launcher_accounts is %T, want a list: %v", raw, raw)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		account, ok := v.(string)
		if !ok {
			t.Fatalf("launcher_accounts holds %T, want the bare address a token carries: %v", v, v)
		}
		out = append(out, account)
	}
	return out
}
