package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// vaultSecretName is the name the checkouts below declare in their workflow's
// own `secrets:` block — one name of the class B3.8's flip takes off the job
// (config.VaultFetchedSecretClasses), so the repository root's plan stops
// producing the job account's accessor on it.
const vaultSecretName = "sandbox-probe"

// vaultWorkflow is the workflow this file's checkouts declare: checkoutYAML
// writes one, named "app", where internal/infra's fixtures say "web".
const vaultWorkflow = "app"

// vaultAccessorAddr is that accessor, the address Readiness stops producing
// once the flip takes the class off the job, and the one a run applied by the
// release before it converges.
const vaultAccessorAddr = `module.repo.module.workflow["` + vaultWorkflow + `"].google_secret_manager_secret_iam_member.job["` + vaultSecretName + `"]`

// vaultStateDoc is the repository root's state as the release before the flip
// wrote it: the accessor in the address given, beside the placeholder repoReady
// leaves for every root. The accessor is a binding, so it carries no attributes
// the run reads — its values are the address's own — and a state doc with the
// address alone is the whole of what the retirement's read needs (flipStateDoc,
// the proxied flip's own copy of this shape).
func vaultStateDoc(t *testing.T, addr string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"format_version": "1.0", "values": map[string]any{"root_module": map[string]any{
		"resources": []any{
			map[string]any{"address": "x"},
			map[string]any{"address": addr, "mode": "managed", "type": "google_secret_manager_secret_iam_member", "name": "job"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// vaultFlipRig is the world both rows below run in: an installation that
// publishes a node which admits acme/sandbox and does NOT proxy it, a
// credentials service deployed and this repository switched on, and a
// repository root whose state holds the accessor in addr while this tree's plan
// deletes it. The checkout's agent.auth is cascade and the installation's
// outputs carry the service, so CredentialsServiceServes answers true and
// Readiness takes the class off this job whatever the project's mode is; the
// repository is not proxied, so the delete in the plan is the workflow secret's
// accessor alone and nothing about the Bitbucket token or a model credential is
// in play (the two flips are separate, G22).
//
// The record already marks the name copied and the grammar already holds its
// value, which is what makes the accessor a delete fugaro itself decided: only
// a copied name goes (IS6, D3325), so a fetch of one the grammar does not hold
// yet — IS5's skipped name — keeps its mount, its accessors and its copy. IS5's
// copy is a late step of a converge and this flip is a plan of the repository
// root, so the update that copies a name is the one before the update that
// drops it; this rig is that second update's world.
func vaultFlipRig(t *testing.T, addr string) *initRig {
	t.Helper()
	r, db := proxiedDropRig(t)
	publishProxiedNode(t, db, false, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, proxiedRepoBundle(t))
	// The database the record is in is proven to be this installation's before
	// the run reads it — the config's Firebase project beside its URL, and the
	// mark that door writes (auroraMark) — which is what any installation with
	// a record has, and the door the record's reader asks before it trusts one
	// at all (budgetDBUnadopted). An installation whose database is unproven
	// reads no record: it drops nothing and keeps every mount, the direction
	// both halves of the migration fail in, and not a world this flip's own
	// test can be run in.
	rewriteConfigLine(t, "per_run_usd: 5", "per_run_usd: 5, firebase_project: "+initProject)
	db.Set(budget.PathMark, auroraMark())
	// The record's own entry for the name, and the value IS5's copy left in the
	// grammar the intake writes and the service serves: the two facts
	// copiedWorkflowSecrets reads, and both have to hold before any name goes.
	copiedDB, err := newBudgetClient(context.Background(), db.URL, true)
	if err != nil {
		t.Fatalf("opening the installation's budget database: %v", err)
	}
	if err := creds.WriteProjectSecrets(context.Background(), copiedDB, func(creds.ProjectSecrets, bool) (creds.ProjectSecrets, error) {
		return creds.ProjectSecrets{Version: creds.ProjectSecretsVersion}.
			WithRule(vaultSecretName, creds.ProjectSecretRule{Repos: []string{"acme/*"}}), nil
	}); err != nil {
		t.Fatalf("writing the record entry that marks %s copied: %v", vaultSecretName, err)
	}
	id, err := secretname.Project(vaultSecretName)
	if err != nil {
		t.Fatalf("secretname.Project(%q): %v", vaultSecretName, err)
	}
	r.sm.Seed(id, map[string]string{intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}, []byte("grammar-"+vaultSecretName))
	// The repository root's two reads, scripted apart (the fake answers
	// "show -json" with no plan file for the state and "show" for the plan):
	// the state the release before the flip wrote holds the accessor, and this
	// tree's plan deletes it.
	r.script["show -json@repo"] = map[string]any{"stdout": vaultStateDoc(t, addr)}
	r.script["show@repo"] = map[string]any{"stdout": planJSON(t, retireChange(addr, "google_secret_manager_secret_iam_member", "delete"))}
	r.save(t)
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git",
		checkoutYAML("bitbucket", "cascade", "aurora", ", secrets: [{ name: "+vaultSecretName+", env: SANDBOX_PROBE }]"))
	t.Chdir(dir)
	fakeTerminal(t)
	return r
}

// TestUpdateConvergesTheVaultFlipOnAProjectThatIsNotManaged is the HARD RULE's
// row for B3.8's flip, on the path almost every installation takes: one plain
// `fugaro update --repo` applies the repository root whose plan gives the job's
// account up its accessor on a workflow secret, with no `--allow-delete` and no
// second command. The drop is fugaro's own decision — the class list is filled
// and this repository is one the switch serves — so the operator may not be
// asked to allow it (infra.VaultAccessorAllowDelete, which planRepo's guard is
// given beside the flag's own list).
//
// The second row is the allowance's own negative control, and it is why the
// first one is not vacuous: the same world, with the plan deleting an accessor
// this spec does not stop producing, and the run refuses it and applies
// nothing. That is the guard judging this plan rather than a plan some other
// path admitted — with a delete nobody allowed, the same command the row above
// lets through stops and names the flag (CLAUDE.md's HARD RULE is that the flip
// is not one of those, not that no delete ever is).
func TestUpdateConvergesTheVaultFlipOnAProjectThatIsNotManaged(t *testing.T) {
	t.Run("the flip's own delete is applied", func(t *testing.T) {
		r := vaultFlipRig(t, vaultAccessorAddr)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
		if err != nil {
			t.Fatalf("one plain fugaro update --repo did not converge B3.8's flip on a project that is not managed, so the one command is not the whole upgrade for it: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
		}
		text := out + errOut
		if !strings.Contains(text, vaultAccessorAddr) {
			t.Fatalf("the run does not name %s anywhere, so the state this test scripted was not the state it read and nothing below is about the flip\n%s", vaultAccessorAddr, text)
		}
		if n := len(r.ran(t, "apply")); n == 0 {
			t.Errorf("the run did not apply the repository's root, so the delete the guard had to admit was never made: %q", r.calls(t))
		}
		if strings.Contains(text, "--allow-delete") {
			t.Errorf("the run owes the operator a flag for the flip's own delete, which is the operator step the HARD RULE forbids:\n%s", text)
		}
	})
	t.Run("a delete the flip does not make is refused", func(t *testing.T) {
		other := strings.Replace(vaultAccessorAddr, vaultSecretName, "some-other-secret", 1)
		r := vaultFlipRig(t, other)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
		text := out + errOut + ErrorText(err)
		if err == nil {
			t.Fatalf("the run applied a plan that deletes %s, an accessor this tree's spec still produces and no flip takes away: the allowance would be a licence for any accessor delete rather than the flip's own\nstdout:\n%s\nstderr:\n%s", other, out, errOut)
		}
		if n := len(r.ran(t, "apply")); n != 0 {
			t.Errorf("the run applied the repository's root %d time(s) after refusing: %q", n, r.calls(t))
		}
		if !strings.Contains(text, other) || !strings.Contains(text, "--allow-delete") {
			t.Errorf("the refusal does not name %s and the flag that admits it, so a reader cannot tell what stopped the run:\n%s", other, text)
		}
	})
}
