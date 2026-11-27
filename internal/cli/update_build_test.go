package cli

import (
	"strings"
	"testing"
)

// buildFlipHandling is the build account's accessor on a secret this
// repository's build no longer mounts — the address a run applied by the
// release before B4.9's flip converges, and the one the guard has to admit for
// one plain `fugaro update --repo`.
//
// The name is the sandbox checkout's own build secret (its workflow's
// `secrets:` block, the same name B3.8's flip fetches for the job), so the
// accessor is one the release before the flip planned and this tree's spec no
// longer produces. The provider credential's accessor goes in the same plan;
// the rows below name one of them, since the guard judges each address alone.
const buildAccessorAddr = `module.repo.google_secret_manager_secret_iam_member.build["` + buildSecretName + `"]`

// buildSecretName is that secret: the name the checkout below declares in its
// workflow's own `secrets:` block, which both flips of the repository's build
// move (B3.8's fetched class takes the job's mount, B4.9's build class the
// build account's accessor).
const buildSecretName = "sandbox-probe"

// buildFlipRig is the world the two rows below run in: an installation that
// publishes a node which admits acme/sandbox and does NOT proxy it, a
// credentials service deployed and this repository switched on, and a
// repository root whose state holds the accessor in addr while this tree's plan
// deletes it. It is vaultFlipRig's world, told about the OTHER half of the S4
// flip: the build account's list instead of the job's, which is why the address
// sits in modules/repo's own file and not in the workflow module (the build's
// accessors are declared once per repository, modules/repo/build.tf).
func buildFlipRig(t *testing.T, addr string) *initRig {
	t.Helper()
	r, db := proxiedDropRig(t)
	// The rows below are about the path an installation that is NOT managed
	// takes: the guard's own refusal is the one that names --allow-delete, and in
	// a managed project the gate answers first with its own sentence and no flag
	// (D1012).
	r.unmarkedInstallation(t)
	publishProxiedNode(t, db, false, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, proxiedRepoBundle(t))
	r.script["show -json@repo"] = map[string]any{"stdout": vaultStateDoc(t, addr)}
	r.script["show@repo"] = map[string]any{"stdout": planJSON(t, retireChange(addr, "google_secret_manager_secret_iam_member", "delete"))}
	r.save(t)
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git",
		checkoutYAML("bitbucket", "cascade", "aurora", ", secrets: [{ name: "+buildSecretName+", env: SANDBOX_PROBE }]"))
	t.Chdir(dir)
	fakeTerminal(t)
	return r
}

// TestUpdateConvergesTheBuildFlipOnAProjectThatIsNotManaged is the HARD RULE's
// row for B4.9's flip, on the path almost every installation takes: one plain
// `fugaro update --repo` applies the repository root whose plan gives the build
// account up its accessor on a secret the build no longer mounts, with no
// `--allow-delete` and no second command. The drop is fugaro's own decision —
// the class list is filled and this repository is one the switch serves — so
// the operator may not be asked to allow it (infra.BuildAccessorAllowDelete,
// which planRepo's guard is given beside the flag's own list and the two other
// flips').
//
// The second row is the allowance's own negative control, and it is why the
// first one is not vacuous: the same world, with the plan deleting an accessor
// on a name this repository never declared, and the run refuses it and applies
// nothing. That is the guard judging this plan rather than a plan some other
// path admitted — with a delete nobody allowed, the same command the row above
// lets through stops and names the flag (CLAUDE.md's HARD RULE is that the flip
// is not one of those, not that no delete ever is).
func TestUpdateConvergesTheBuildFlipOnAProjectThatIsNotManaged(t *testing.T) {
	t.Run("the flip's own delete is applied", func(t *testing.T) {
		r := buildFlipRig(t, buildAccessorAddr)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
		if err != nil {
			t.Fatalf("one plain fugaro update --repo did not converge B4.9's flip on a project that is not managed, so the one command is not the whole upgrade for it: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
		}
		text := out + errOut
		if !strings.Contains(text, buildAccessorAddr) {
			t.Fatalf("the run does not name %s anywhere, so the state this test scripted was not the state it read and nothing below is about the flip\n%s", buildAccessorAddr, text)
		}
		if n := len(r.ran(t, "apply")); n == 0 {
			t.Errorf("the run did not apply the repository's root, so the delete the guard had to admit was never made: %q", r.calls(t))
		}
		if strings.Contains(text, "--allow-delete") {
			t.Errorf("the run owes the operator a flag for the flip's own delete, which is the operator step the HARD RULE forbids:\n%s", text)
		}
	})
	t.Run("a delete the flip does not make is refused", func(t *testing.T) {
		other := strings.Replace(buildAccessorAddr, buildSecretName, "some-other-secret", 1)
		r := buildFlipRig(t, other)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
		text := out + errOut + ErrorText(err)
		if err == nil {
			t.Fatalf("the run applied a plan that deletes %s, an accessor on a name this repository never declared: the allowance would be a licence for any accessor delete rather than the flip's own\nstdout:\n%s\nstderr:\n%s", other, out, errOut)
		}
		if n := len(r.ran(t, "apply")); n != 0 {
			t.Errorf("the run applied the repository's root %d time(s) after refusing: %q", n, r.calls(t))
		}
		if !strings.Contains(text, other) || !strings.Contains(text, "--allow-delete") {
			t.Errorf("the refusal does not name %s and the flag that admits it, so a reader cannot tell what stopped the run:\n%s", other, text)
		}
	})
}
