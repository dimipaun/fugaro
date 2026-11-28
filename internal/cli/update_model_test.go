package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// modelWorkflow is the workflow these checkouts declare (checkoutYAML names
// it "app", where internal/infra's fixtures say "web") — the variant whose
// job account held the accessor the release before B2.7's flip wrote.
const modelWorkflow = "app"

// modelAccessorAddr is the job account's own accessor on a Claude credential:
// the address B2.7's flip stops producing for a switched `api-key` or `oauth`
// repository (D2072), and the one a run applied by the release before it
// converges. It is vaultAccessorAddr's shape with the model name in it, so
// the two files' addresses cannot be confused for one another.
func modelAccessorAddr(name string) string {
	return `module.repo.module.workflow["` + modelWorkflow + `"].google_secret_manager_secret_iam_member.job["` + name + `"]`
}

// publishServedNode writes the policy node of a project whose budget mode is
// enforce, which is one of the two modes the switch's fourth condition admits
// for an `api-key` or `oauth` repository (D2071: the gateway that reaches the
// service exists in `observe` and `enforce` alone). The node admits
// acme/sandbox and does NOT proxy it, so the delete in play is the model
// credential's and nothing about the Bitbucket token is in this file (the two
// flips are separate, G22). mode overrides the published mode, which every row
// below takes as `enforce`: the vocabulary has one other live mode and both
// serve the bundle, so a mode is no longer a way to hold a repository off the
// switch (D3009, F26) and the negative controls below reach that state through
// the probe instead (modelFlipRig's served).
func publishServedNode(t *testing.T, db *gcpfake.RTDB, mode string, repos ...string) {
	t.Helper()
	node := map[string]any{"version": projectpolicy.Version, "allow_repos": []string{"bitbucket/acme/sandbox"}, "budget": map[string]any{"mode": mode}}
	if len(repos) > 0 {
		node["bitbucket_token"] = map[string]any{"allow_repos": repos, "proxy": false}
	}
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), node)
}

// modelFlipRig is the world the rows below run in: an installation that
// publishes an `enforce` node admitting acme/sandbox and does not proxy it, a
// credentials service deployed and this repository switched on, and a
// repository root whose STATE holds the Claude accessor in addr while this
// tree's plan deletes it — the state the release before B2.7's flip wrote, and
// the plan the flip's own drop makes of it. The checkout's agent.auth is auth
// and the bundle holds the credential that auth calls for, so the switch's
// record comes out on and Readiness plans `model_secrets_served = anthropic`
// for this repository (credentialsBundleServes is judged per auth, so `oauth`
// needs the account's token where `api-key` needs the paid key).
//
// served is the probe's own answer, and false is the second state D2071 names:
// the outputs carry no credentials_service, so the probe drops the block and
// nothing is served for this repository, whatever the node's mode says.
func modelFlipRig(t *testing.T, auth, credential, mode string, served bool, addr string) *initRig {
	t.Helper()
	r, db := proxiedRepoRig(t)
	// The rows below are about the path an installation that is NOT managed
	// takes: the guard's own refusal is the one that names --allow-delete, and in
	// a managed project the gate answers first with its own sentence and no flag
	// (D1012).
	r.unmarkedInstallation(t)
	// The bundle has to be READABLE for the switch's record to be judged: the
	// probe takes the block off the installation's outputs (credentialsServiceProbe)
	// and drops it when the outputs carry no URL, and a repository the probe
	// cannot find a service for is one nothing is served (D2071).
	if served {
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"credentials_service": credentialsServiceOutput()})}
	} else {
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{})}
	}
	publishServedNode(t, db, mode, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, []byte(credential+": "+credsValue(t, "model")+"\n"))
	// A plan with something in it: repoReady leaves the rig up to date, and a
	// run with no change to make applies nothing, which is not the apply these
	// rows are about.
	r.script["plan"] = map[string]any{"exit": 2}
	// The repository root's two reads, scripted apart (the fake answers
	// "show -json" with no plan file for the state and "show" for the plan):
	// the state the release before the flip wrote holds the accessor, and this
	// tree's plan deletes it.
	r.script["show -json@repo"] = map[string]any{"stdout": vaultStateDoc(t, addr)}
	r.script["show@repo"] = map[string]any{"stdout": planJSON(t, retireChange(addr, "google_secret_manager_secret_iam_member", "delete"))}
	r.save(t)
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", auth, "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	return r
}

// TestUpdateConvergesTheModelFlipOnAProjectThatIsNotManaged is the HARD RULE's
// row for B2.7's flip, on the path almost every installation takes: one plain
// `fugaro update --repo` applies the repository root whose plan gives the job's
// account up its accessor on the model credential the credentials service now
// serves it, with no `--allow-delete` and no second command. The drop is
// fugaro's own decision — the flip list carries the auth, the node's mode
// admits the repository and the bundle holds the credential — so the operator
// may not be asked to allow it (infra.ModelAccessorAllowDelete, which
// planRepo's guard is given beside the flag's own list).
//
// It is run for both auths the flip carries, because the two rows reach the
// drop through different mounts: an `api-key` repository's job mounts the paid
// key (config.AnthropicSecret) and an `oauth` one mounts the default account's
// token, and the allowance has to admit each name's accessor and no other.
func TestUpdateConvergesTheModelFlipOnAProjectThatIsNotManaged(t *testing.T) {
	for name, tc := range map[string]struct{ auth, credential string }{
		"an api-key repository": {config.AuthAPIKey, config.AnthropicSecret},
		"an oauth repository":   {config.AuthOAuth, config.ClaudeOAuthToken},
	} {
		t.Run(name, func(t *testing.T) {
			addr := modelAccessorAddr(tc.credential)
			r := modelFlipRig(t, tc.auth, tc.credential, config.BudgetEnforce, true, addr)
			before := logNames(t, "update")
			out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
			if err != nil {
				t.Fatalf("one plain fugaro update --repo did not converge B2.7's flip for %s, so the one command is not the whole upgrade for it: %v\nstdout:\n%s\nstderr:\n%s", name, err, out, errOut)
			}
			// The summary's addresses are the log's (printRetirements).
			text := out + errOut + logsAdded(t, before, "update")
			if !strings.Contains(text, addr) {
				t.Fatalf("the run does not name %s anywhere, so the state this test scripted was not the state it read and nothing below is about the flip\n%s", addr, text)
			}
			if n := len(r.ran(t, "apply")); n == 0 {
				t.Errorf("the run did not apply the repository's root, so the delete the guard had to admit was never made: %q", r.calls(t))
			}
			if strings.Contains(text, "--allow-delete") {
				t.Errorf("the run owes the operator a flag for the flip's own delete, which is the operator step the HARD RULE forbids:\n%s", text)
			}
		})
	}
}

// TestUpdateRefusesADeleteTheModelFlipDoesNotMake is the allowance's own
// negative control, and it is why the rows above are not vacuous: the same
// world, with the plan deleting an accessor no flip takes away, and the run
// refuses it and applies nothing. That is the guard judging this plan rather
// than a plan some other path admitted — with a delete nobody allowed, the
// same command the rows above let through stops and names the flag (CLAUDE.md's
// HARD RULE is that the flip is not one of those, not that no delete ever is).
//
// Two rows, because two things must stay refused:
//
//   - the job account's accessor on a name this spec still declares and mounts,
//     the checkout's own git credential: the allowance is not a licence for
//     every accessor this root declares — a rule that admitted any declared
//     name would pass the rows above and fail here. The node above does not
//     proxy this repository, so the token's own flip (G22) takes nothing away
//     and the guard must weigh this delete as it always has.
//   - the job account's accessor on the same Claude credential of a repository
//     the installation serves nothing — the outputs carry no credentials
//     service, so the probe drops the block, Readiness plans
//     `model_secrets_served = none` and the drop this allowance exists for is
//     not one this repository's plan makes. The state is D2071's own second
//     one, and it is the one this row reads since a mode cannot express it any
//     more: `off` is gone (D3009), and the two live modes both serve a
//     switched repository whose bundle is ready (F26), so no published mode
//     holds a repository off the flip. The delete there is the operator's own
//     change, which `--allow-delete` is still the way past.
func TestUpdateRefusesADeleteTheModelFlipDoesNotMake(t *testing.T) {
	t.Run("an accessor this spec still produces is refused", func(t *testing.T) {
		other := modelAccessorAddr(config.BitbucketSecret)
		r := modelFlipRig(t, config.AuthAPIKey, config.AnthropicSecret, config.BudgetEnforce, true, other)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
		text := out + errOut + ErrorText(err)
		if err == nil {
			t.Fatalf("the run applied a plan that deletes %s, an accessor this tree's spec still mounts and no flip takes away: the allowance would be a licence for any accessor delete rather than the flip's own\nstdout:\n%s\nstderr:\n%s", other, out, errOut)
		}
		if n := len(r.ran(t, "apply")); n != 0 {
			t.Errorf("the run applied the repository's root %d time(s) after refusing: %q", n, r.calls(t))
		}
		if !strings.Contains(text, other) || !strings.Contains(text, "--allow-delete") {
			t.Errorf("the refusal does not name %s and the flag that admits it, so a reader cannot tell what stopped the run:\n%s", other, text)
		}
	})
	t.Run("the repository the installation serves nothing is refused", func(t *testing.T) {
		addr := modelAccessorAddr(config.AnthropicSecret)
		r := modelFlipRig(t, config.AuthAPIKey, config.AnthropicSecret, config.BudgetObserve, false, addr)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
		text := out + errOut + ErrorText(err)
		if err == nil {
			t.Fatalf("the run applied a plan that deletes %s for a repository the installation serves nothing: no flip takes that accessor away there — Readiness plans model_secrets_served = none and the job keeps every mount — so the delete is the operator's own and the guard must weigh it as it always has\nstdout:\n%s\nstderr:\n%s", addr, out, errOut)
		}
		if n := len(r.ran(t, "apply")); n != 0 {
			t.Errorf("the run applied the repository's root %d time(s) after refusing: %q", n, r.calls(t))
		}
		if !strings.Contains(text, addr) || !strings.Contains(text, "--allow-delete") {
			t.Errorf("the refusal does not name %s and the flag that admits it, so a reader cannot tell what stopped the run:\n%s", addr, text)
		}
	})
}
