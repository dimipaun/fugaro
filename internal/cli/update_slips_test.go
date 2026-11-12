// update_slips_test.go is DS4.1 under the door CLAUDE.md's HARD RULE makes the
// whole upgrade: an installation the release behind left behind reaches this
// one with `fugaro update` alone, and nothing else. The convergence tests of
// update_converge_test.go are about the stages that move every installation
// (the rules generation, the base image, the plugin, the node); this file is
// about the three halves dynamic delivery adds to that list.
//
// The secrets intake is deployed by that run and by nothing else: the module's
// whole file is gated on `var.intake != null` (modules/installation/intake.tf),
// no release before this one wrote the variable — internal/infra's
// InstallationSpec still carries no block of that name and InstallationVars
// writes no such key, and spec.go's Intake is the OTHER direction, the
// installation root's own `intake` output, which is what the audience fill
// reads back — and the service is a subcommand of the image the credentials
// service already runs (there is no intake image kind in images/, and
// .github/workflows/images.yml builds history, base and credentials), so
// nothing else in the tree could deploy it. The block is written by the
// installation stage (deployCredentialsService, carriedIntake), moved onto this
// release with the service's own pin by the credentials-service stage, and
// carried forward by prepare: the test below pins the whole of that, in the
// tfvars an installation converges to and in the URL its revision would carry.
//
// The launcher class (D1938, F25) is the list the intake and the credentials
// service answer to. Cloud Run grants the invoker per service, so the roster's
// people pass IAM and then meet the process's own check, and the class has to
// name exactly the people the grant is made from — a roster that reached one
// list and not the other is a caller IAM admits and the process refuses.
// Nothing wrote a class before this release, so an installation at the previous
// version holds none, and the run that converges it is the run that writes it,
// from the installation root's own recorded roster. What the class leaves out
// is internal/infra's own rule and is pinned there (launchers_test.go): the
// roster these tests hand over is already the people a run launches as.
//
// The node is DS3.1's in-place migration. The version the release behind
// published is carried forward by the same run, keeping what its owner
// published, because the alternative — the operator step 0.14.0's notes asked
// for, `fugaro project policy publish --delete` and a fresh publish — is a
// second command, and the HARD RULE has no second command.

package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestUpdateConvergesFromPreviousInstallation is the dynamic delivery half of
// "an installation at the previous version converges": one `fugaro update`,
// with no other step, leaves the installation root carrying the roster's
// launcher class, the secrets intake the release behind never deployed, and the
// published node at this binary's version with the owner's own settings still
// in it.
func TestUpdateConvergesFromPreviousInstallation(t *testing.T) {
	const (
		launcher = "person-1@example.invalid"
		operator = "person-2@example.invalid"
	)
	// The fixture is what the release behind really leaves: an installation
	// whose credentials service is deployed on THAT release's image — a
	// reference whose digest is the installation's own record, which this run
	// has to resolve again rather than read — and whose tfvars carry no intake
	// block at all, since no earlier release wrote one.
	r := newConvergeRig(t, staleNode(t))
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	// The root's plan holds the service's own revision moving, which is the
	// plan this run applies (a plan that reports no changes is never applied,
	// TF.Plan's exit code being what says so). The rig's document answers the
	// state reads under "show -json" while the plan answers under
	// "show@installation": without the first, the fallback the fake documents
	// (a script naming only "show" answers the state too) would let this stage's
	// plan be read as the installation's state, which manages nothing.
	//
	// What the plan DOES NOT hold is the intake's own creation, and that is not
	// an omission to fix here: this door is not the managed gate (the fixture is
	// the owner's own terminal on an installation the run does not gate), so no
	// cover judges this plan, and a change planted in it would pass whatever it
	// said. That the module's intake IS admitted by the cover — the rule that
	// makes the apply deploy it on a managed installation — is pinned where it
	// is judged: internal/infra/tf's
	// TestCoverRefusesAnIntakeServiceThatIsNotTheModules, over the same fixture
	// intake.tf writes.
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	// The roster the installation root reports, which is the one the class is
	// derived from: a launcher and an operator (who launches too). A group is
	// not in it: the roster the module records is a list of people, and what
	// the class does with one is internal/infra's own rule, pinned where
	// launcherAccounts is (launchers_test.go).
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"launchers": []string{"user:" + launcher},
		"operators": []string{"user:" + operator},
	})}
	r.save(t)

	out := convergeOn(t, r, []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})

	// The intake, which no release before this one deployed and nothing else in
	// the tree can: the block the module's whole file is gated on, in the tfvars
	// the run applies, on this release's pin and at the URL the root's own
	// outputs name — the audience its callers' ID tokens are minted for. An
	// installation the update left without one has no service for the CLI's own
	// instruction to publish through ("deployed by `fugaro update` ... run it
	// and try again"), and a block left on the PREVIOUS release's image is a
	// service running the binary the release behind shipped.
	intake, _ := r.tfvars(t)["intake"].(map[string]any)
	if intake == nil {
		t.Fatalf("after one fugaro update the installation's tfvars carry no intake block: %v", r.tfvars(t))
	}
	for k, want := range map[string]any{
		"account_id": infra.IntakeAccountID,
		"service":    infra.IntakeServiceName,
		"image":      r.credentialsRefAt(convergeRelease),
		"url":        intakeServiceURL,
	} {
		if got := intake[k]; got != want {
			t.Errorf("after one fugaro update intake.%s = %v, want %v: the run deploys the intake in the same apply it moves the credentials service onto this release's pin, and fills both URLs from the root's outputs", k, got, want)
		}
	}
	// The pair moves together: the service's own entry is this release's
	// reference, which is the resolve's own and not a value the run composed.
	if got, want := credentialsServiceEntry(t, r.imagesRig)["image"], r.credentialsRefAt(convergeRelease); got != want {
		t.Errorf("after one fugaro update credentials_service.image = %v, want %s: the two services run one pin", got, want)
	}

	// The class, in the installation root's own variables: the people of the
	// roster as the addresses a Google ID token carries, sorted and each once.
	// An installation the update left without one would meet a service whose
	// class admits nobody, which is fail-closed and still not converged: the
	// people who launch are refused by the process IAM let through.
	if got, want := launcherAccountsOf(t, r), []string{launcher, operator}; !slices.Equal(got, want) {
		t.Errorf("after one fugaro update the installation root carries launcher_accounts = %v, want %v: the class the intake and the credentials service answer to is the roster's people", got, want)
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
