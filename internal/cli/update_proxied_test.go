package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// proxiedRepoRig is the update --repo rig of an installation that publishes a
// policy node and has opted in to the credentials service: the checkout of
// acme/sandbox, an applied installation, and a database address in the local
// config, so the engine reads the node the way a real run does (internal/cli's
// policyReadOnce over budget.rtdb_url). Both halves are needed before any
// entry is judged: with the opt-in off nothing is proxied and nothing
// disagrees (G9, resolveBitbucketProxied).
func proxiedRepoRig(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r := newInitRig(t)
	r.repoReady(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": map[string]any{"name": "fugaro-credentials", "region": "us-east5"},
	})}
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+db.URL+" }\n")
	r.save(t)
	return r, db
}

// publishProxiedNode writes a node whose bitbucket_token block admits repos
// with proxy: proxy. The block is the one source of the per-repository fact
// (G15), so the tests below read it rather than the local config.
func publishProxiedNode(t *testing.T, db *gcpfake.RTDB, proxy bool, repos ...string) {
	t.Helper()
	publishProxiedNodeAt(t, db, projectpolicy.Version, proxy, repos...)
}

// publishProxiedNodeAt is publishProxiedNode with the node's version chosen:
// the node the first `fugaro update` after a version bump reads is written one
// version behind, which is what the converge test below starts from.
func publishProxiedNodeAt(t *testing.T, db *gcpfake.RTDB, version int, proxy bool, repos ...string) {
	t.Helper()
	node := map[string]any{"version": version, "allow_repos": []string{"bitbucket/acme/sandbox"}}
	if len(repos) > 0 {
		node["bitbucket_token"] = map[string]any{"allow_repos": repos, "proxy": proxy}
	}
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), node)
}

// repoProxiedFact is the fact as the repository root's plan reads it: the
// repo object's bitbucket_proxied in the tfvars the run wrote under the
// repository's own workdir. present is false when the key is not there at
// all, which is what the module's optional(bool, false) reads as false — the
// same contract internal/infra's repoBitbucketProxied pins at the spec.
func repoProxiedFact(t *testing.T, slug string) (value, present bool) {
	t.Helper()
	wd, err := infra.RepoWorkdir(os.Getenv, initProject, slug)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "gcp", "roots", "repo", infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Repo map[string]json.RawMessage `json:"repo"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	raw, present := doc.Repo["bitbucket_proxied"]
	if !present {
		return false, false
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("repo.bitbucket_proxied = %s: %v", raw, err)
	}
	return value, true
}

// TestUpdateRepoCarriesTheProxiedFact runs the whole command in the checkout
// of a repository the published node admits, and reads the fact where the
// plan reads it: the repository root's tfvars. The node's own proxy switch is
// the only thing that moves between the two cases — the local config carries
// no project_secrets entry at all, so there is nothing to disagree with and
// the node's answer is the fact (G15).
func TestUpdateRepoCarriesTheProxiedFact(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := projectpolicy.Canonical("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ proxy, want bool }{
		"the node admits this repository with proxy: true":  {true, true},
		"the node admits this repository with proxy: false": {false, false},
	} {
		r, db := proxiedRepoRig(t)
		publishProxiedNode(t, db, tc.proxy, canonical)
		dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", ""))
		t.Chdir(dir)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--plan-only")
		if err != nil {
			t.Fatalf("%s: the run failed: %v\nstdout:\n%s\nstderr:\n%s", name, err, out, errOut)
		}
		got, present := repoProxiedFact(t, slug)
		if tc.want {
			if !present || !got {
				t.Errorf("%s: the repository root's tfvars carry repo.bitbucket_proxied = %v (key present: %v), want true: the node is the one source of the fact and it admits this repository (G15)", name, got, present)
			}
			continue
		}
		if present {
			t.Errorf("%s: the repository root's tfvars carry repo.bitbucket_proxied = %v, want the key omitted: the block admits the repository and forwards nothing (D493, G3)", name, got)
		}
		if len(r.ran(t, "apply")) != 0 {
			t.Errorf("%s: the run applied: %q", name, r.calls(t))
		}
	}
}

// proxiedConvergeRig is proxiedRepoRig for a whole converge: the installation's
// database carries the budget backend `fugaro update --firebase-project` writes
// (the nodes the rules and policy stages act on), so one run reaches the
// repository stage and the policy stage behind it.
func proxiedConvergeRig(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r := newInitRig(t)
	// The installation is the default's managed one, so each gated step's record
	// names the account the run went as (D1014) and a hermetic world has none.
	isolateGateCredentials(t)
	r.repoReady(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": map[string]any{"name": "fugaro-credentials", "region": "us-east5"},
	})}
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMode, budget.ModeEnforce)
	db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5_000_000})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+", firebase_project: \""+initProject+"\" }\n")
	r.save(t)
	return r, db
}

// TestUpdateConvergesAProxiedV4NodeInOneRun: the first `fugaro update` after
// the upgrade that bumps the policy version converges a proxied repository in
// ONE run — the node migrated to this binary's version, and the repository root
// carrying the fact the node states.
//
// The two halves are one run's because the repository stage runs BEFORE the
// policy stage (initStages registers it there, and the order is deliberate: the
// rules stage is what the policy write is gated on). A strict read of the node
// in the repository stage refuses the version behind, the stage then passes no
// policy at all (repoPolicyInput), and internal/infra reads a nil policy as
// "nothing is proxied" (resolveBitbucketProxied judges nothing without one) —
// so the repository root drops bitbucket_proxied, the job's service account
// keeps the accessor and the mount on bitbucket-token the credentials-service
// proxy withholds, and the node's published image catalog and map are not
// enforced. All of that converges on a SECOND `fugaro update`, which is the
// operator step the HARD RULE forbids.
//
// The read that fixes it is the repository stage's own
// (repoPolicyRead/ReadCarriedForwardShapeFree, v4 being projectpolicy's
// ShapeFreeFloor), so the stage reads the node the policy stage of the same run
// is about to write.
func TestUpdateConvergesAProxiedV4NodeInOneRun(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := projectpolicy.Canonical("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	r, db := proxiedConvergeRig(t)
	// The credentials the installation already holds: a converge reaches the
	// repository stage only past the secrets stage, and this test is about the
	// stages behind it.
	r.seedRepoSecret(t, "bitbucket-token", []byte(tokenValue))
	r.seedRepoSecret(t, "claude-oauth-token", []byte(tokenValue))
	// The image build is the fake builder: what this test reads is the root's
	// plan and the node, not a Cloud Build.
	useFakeBuilder(t)
	publishProxiedNodeAt(t, db, projectpolicy.Version-1, true, canonical)
	if got := nodeVersionOn(t, db); got != projectpolicy.Version-1 {
		t.Fatalf("the rig's node is version %d, want %d: this test is about the node the first update after the bump reads", got, projectpolicy.Version-1)
	}
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("one fugaro update did not converge a version-%d node that proxies this repository: %v\nstdout:\n%s\nstderr:\n%s", projectpolicy.Version-1, err, out, errOut)
	}
	if got := nodeVersionOn(t, db); got != projectpolicy.Version {
		t.Errorf("the published policy is version %d after one run, want %d: the policy stage migrates the node in place, and the run the repository stage is in is that run\n%s", got, projectpolicy.Version, out)
	}
	got, present := repoProxiedFact(t, slug)
	if !present || !got {
		t.Errorf("the repository root's tfvars carry repo.bitbucket_proxied = %v (key present: %v), want true: the repository stage read the node carried forward, and a stage that passes no policy there reads as 'nothing is proxied' (the second fugaro update the HARD RULE forbids)\n%s", got, present, out)
	}
	if !strings.Contains(out, "migrated the published policy from version "+strconv.Itoa(projectpolicy.Version-1)+" to "+strconv.Itoa(projectpolicy.Version)) {
		t.Errorf("the run's account does not name the migration this test is about:\n%s", out)
	}
}

// proxiedDropRig is proxiedRepoRig for an APPLYING run of a cascade
// repository: the installation's outputs carry the service's URL, which is what
// credentialsServiceProbe hands credentialsBundleReady — the probe takes the
// block from the outputs only where it has one, so without a URL there is
// nothing to read and the switch's record comes out off — and the base image is moved
// past cascadeSince, which refuseCascadeSkew measures an applying cascade
// checkout against (a --plan-only run is exempt from that gate, which is why
// the tests above sit at 0.4.0 and these cannot).
func proxiedDropRig(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r, db := proxiedRepoRig(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"credentials_service": credentialsServiceOutput()})}
	// A plan with something in it: repoReady leaves the rig up to date, and a
	// run with no change to make applies nothing, which is not the apply this
	// file is about.
	r.script["plan"] = map[string]any{"exit": 2}
	r.save(t)
	rewriteConfigLine(t, "fugaro-base:0.4.0", "fugaro-base:"+shiftRelease(t, cascadeSince, 1))
	return r, db
}

// proxiedRepoCanonical is acme/sandbox as the published node spells it: the
// one repository every row of this file is about.
func proxiedRepoCanonical(t *testing.T) string {
	t.Helper()
	c, err := projectpolicy.Canonical("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// proxiedRepoBundle is the bundle document as the installation that has
// stored one model credential leaves it: enough for the switch's record and
// nothing for the git credential the drop takes away. The rows below add
// config.BitbucketSecret to it with brokeringBundleDoc where the token is the
// thing that moved.
func proxiedRepoBundle(t *testing.T) []byte {
	t.Helper()
	return []byte(config.AnthropicSecret + ": " + credsValue(t, "model") + "\n")
}

// TestUpdateRefusesTheProxiedDropWithoutTheToken is the owner's ruling of
// 2026-11-15 (O8, docs/secrets-brokering/reconciliation.md §7) as a run: the
// apply that gives a proxied repository's jobs up their Bitbucket token — the
// job's mount of it and its account's accessor on it — does not run while the
// credentials service's bundle holds no token to fetch instead. Everything
// else in the world says the drop is the right plan: the installation opted in
// and serves this repository, the node admits it with proxy: true, the
// checkout's agent.auth is cascade, and the bundle holds a model credential,
// so the switch's record is on. The token is the one thing missing, and the
// refusal is the one command that stores it and never a second step for the
// person to remember: the HARD RULE is that `fugaro update` is the whole
// upgrade, which is why the operator draft's "creds set before the re-apply"
// stays removed.
func TestUpdateRefusesTheProxiedDropWithoutTheToken(t *testing.T) {
	r, db := proxiedDropRig(t)
	publishProxiedNode(t, db, true, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, proxiedRepoBundle(t))
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	text := out + errOut + ErrorText(err)
	if err == nil {
		t.Fatalf("the run applied a root that takes this repository's Bitbucket token away while the bundle holds no credential to fetch instead\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: the state is the person's to fix at their own terminal\n%s", got, ExitUserError, text)
	}
	for _, want := range []string{
		"acme/sandbox",
		"creds set " + config.BitbucketSecret,
		"Nothing has been applied",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q, so it is not the gate's own sentence\n%s", want, text)
		}
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("the refused run applied %d time(s): %q", n, r.calls(t))
	}
}

// TestUpdateRefusesTheDropWhenTheBundleWasNotRead: the O8 gate is judged on a
// READING, so a read that FAILED is not a reason to go on — the record the
// switch reads is the last reading's (recordCredentialsServiceReady leaves it
// as it found it when there is nothing to write), so a repository recorded on
// still plans the drop, and applying it would take the mount away on the word
// of a read that never happened.
//
// The state is the one a member of the installation who is not the bundle's
// owner is in: the record on from a run that read it, and this run's own read
// refused — the fixture's bundle is not there at all, which is what the store's
// own first call answers for a container the read fails on. It is the state the
// gate must refuse and not warn about: the two markers — a coding agent's
// session and a Fugaro run's own process — are the only sessions that may not
// answer the question at all, and a person's own terminal with a read that
// failed is not one of them.
func TestUpdateRefusesTheDropWhenTheBundleWasNotRead(t *testing.T) {
	r, db := proxiedDropRig(t)
	publishProxiedNode(t, db, true, proxiedRepoCanonical(t))
	markRepoSwitchOn(t, r)
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	text := out + errOut
	if err == nil {
		t.Fatalf("the run applied a root that takes this repository's Bitbucket token away on the word of a read that did not happen\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: the read is the person's to make work at their own terminal\n%s", got, ExitUserError, text+ErrorText(err))
	}
	for _, want := range []string{
		"the credentials service's bundle was not read",
		"could not check whether the credentials service's bundle holds a " + config.BitbucketSecret,
		"Nothing has been applied",
	} {
		if !strings.Contains(text+ErrorText(err), want) {
			t.Errorf("the refusal does not carry %q, so it is not the gate's own sentence over a failed read\n%s%s", want, text, ErrorText(err))
		}
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("the run refused the drop and applied anyway (%d time(s)): %q", n, r.calls(t))
	}
}

// markRepoSwitchOn writes the switch's record on for the rig's acme/sandbox
// entry, which is what a run that read the bundle leaves in the local config
// (recordCredentialsServiceReady). A run whose read FAILS writes nothing, so
// this is the only way the drop is planned in the test above.
func markRepoSwitchOn(t *testing.T, r *initRig) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), switchRepos) {
		t.Fatalf("the rig's local config no longer holds %q:\n%s", switchRepos, b)
	}
	on := strings.Replace(switchRepos, "workflows: [web] }", "workflows: [web], credentials_service_ready: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(b), switchRepos, on, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateAppliesTheProxiedDropWithTheToken is the other side of the ruling:
// the bundle holds the upstream token, which is every installation whose proxy
// is in use, since the service has nothing to forward with otherwise. The run
// applies the root and says the drop out loud, so the operator reads which
// credential the jobs now fetch instead of mounting.
func TestUpdateAppliesTheProxiedDropWithTheToken(t *testing.T) {
	r, db := proxiedDropRig(t)
	publishProxiedNode(t, db, true, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, []byte(brokeringBundleDoc(t, credsValue(t, "token"))))
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	if err != nil {
		t.Fatalf("the run refused a drop the bundle can serve: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if got := out + errOut; !strings.Contains(got, "holds a "+config.BitbucketSecret+" credential") {
		t.Errorf("the run does not say the jobs fetch the credential from the service from now on:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Errorf("the run did not apply the repository's root: %q", r.calls(t))
	}
}

// TestUpdateConvergesTheFlipOnAProjectThatIsNotManaged is the HARD RULE's
// other half, and the one internal/infra's
// TestTheFlipConvergesThroughTheUpdatesOwnRetirement cannot reach: that test
// is the MANAGED path, where the update retires the accessor by itself
// (retireManaged, whose guard is RetireAllowDelete). This rig's project is not
// managed — nothing here labels the run's creation fact, and the same
// repository, checkout, node and bundle say so in every test of this file —
// so the run takes the ordinary plan. The retirement set is read and printed
// there and nothing removes or deletes it for the user, so before the fix the
// plan's own delete of the accessor the state holds and this tree no longer
// produces was refused by the guard, and the operator was owed
// `fugaro update --repo . --retire` (two typed confirmations) or
// `--allow-delete` for a change fugaro itself decided: the operator step the
// review found in docs/releases/v0.26.0.md.
//
// The world is the flip's, read from the state a release before it wrote: the
// repository is proxied by the published node, its agent.auth is cascade, the
// base image is past cascadeSince, and the bundle holds the token the refusal
// needs (refuseTheProxiedDrop, TestUpdateRefusesTheProxiedDropWithoutTheToken
// for the other side). One plain `fugaro update --repo`, with no flag that
// admits a delete and no --retire, has to apply it.
func TestUpdateConvergesTheFlipOnAProjectThatIsNotManaged(t *testing.T) {
	r, db := proxiedDropRig(t)
	publishProxiedNode(t, db, true, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, []byte(brokeringBundleDoc(t, credsValue(t, "token"))))
	// The repository root's two reads, scripted apart (the fake answers
	// "show -json" with no plan file for the state and "show" for the plan):
	// the state the release before the flip wrote holds the accessor, and this
	// tree's plan deletes it.
	r.script["show -json@repo"] = map[string]any{"stdout": flipStateDoc(t)}
	r.script["show@repo"] = map[string]any{"stdout": planJSON(t, retireChange(flipAccessorAddr, "google_secret_manager_secret_iam_member", "delete"))}
	r.save(t)
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	if err != nil {
		t.Fatalf("one plain fugaro update --repo did not converge the flip on a project that is not managed, so the one command is not the whole upgrade for it: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, flipAccessorAddr) {
		t.Fatalf("the run does not name %s anywhere, so the state this test scripted was not the state it read and nothing below is about the flip\n%s", flipAccessorAddr, text)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Errorf("the run did not apply the repository's root, so the delete the guard had to admit was never made: %q", r.calls(t))
	}
	if strings.Contains(text, "--allow-delete") {
		t.Errorf("the run owes the operator a flag for the flip's own delete, which is the operator step the HARD RULE forbids:\n%s", text)
	}
}

// flipAccessorAddr is the job account's own accessor on the repository's
// Bitbucket token, in the workflow this file's checkouts declare ("app":
// checkoutYAML's only workflow, where internal/infra's fixtures say "web").
// It is the address Readiness stops producing once the flip names the token,
// and the one a run applied by the release before it converges.
const flipAccessorAddr = `module.repo.module.workflow["app"].google_secret_manager_secret_iam_member.job["` + config.BitbucketSecret + `"]`

// flipStateDoc is the repository root's state as the release before the flip
// wrote it: the accessor above, beside the placeholder repoReady leaves for
// every root. The accessor is a binding, so it carries no attributes the run
// reads -- its values are the address's own -- and a state doc with the
// address alone is the whole of what the retirement's read needs.
func flipStateDoc(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"format_version": "1.0", "values": map[string]any{"root_module": map[string]any{
		"resources": []any{
			map[string]any{"address": "x"},
			map[string]any{"address": flipAccessorAddr, "mode": "managed", "type": "google_secret_manager_secret_iam_member", "name": "job"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpdateAppliesWhereNothingIsDropped: the gate is the plan's own
// condition, so the applies that would take no token away are not judged and
// the missing credential does not stop them. The node that does not proxy this
// repository and the installation that never opted in are the two halves of
// infra.RepoSpec.DropsBitbucketToken, and each is a root whose jobs keep their
// mount (internal/infra's Readiness and the workflow module read the same two
// facts).
func TestUpdateAppliesWhereNothingIsDropped(t *testing.T) {
	canonical := proxiedRepoCanonical(t)
	for name, tc := range map[string]struct{ optedIn, proxy bool }{
		"the node admits the repository and forwards nothing": {true, false},
		"the installation never opted in":                     {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			r, db := proxiedDropRig(t)
			if !tc.optedIn {
				// The installation's outputs name no credentials service:
				// there is no switch to turn on, whatever the node admits.
				r.script["output"] = map[string]any{"stdout": outputsJSON(t)}
				r.save(t)
			}
			publishProxiedNode(t, db, tc.proxy, canonical)
			putSwitchBundle(t, r, proxiedRepoBundle(t))
			dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", ""))
			t.Chdir(dir)
			fakeTerminal(t)
			out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
			if err != nil {
				t.Fatalf("the run refused an apply that takes no token away: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
			}
			if got := out + errOut; strings.Contains(got, "creds set "+config.BitbucketSecret) {
				t.Errorf("the run sent the operator to store a credential for a drop it does not make:\n%s", got)
			}
			if n := len(r.ran(t, "apply")); n == 0 {
				t.Errorf("the run did not apply the repository's root: %q", r.calls(t))
			}
		})
	}
}

// TestUpdateRepoPlanOnlyDoesNotRefuseTheDrop: --plan-only applies nothing, so
// nothing is dropped and the gate has nothing to refuse — the same exemption
// the other refusals that stop a CHANGE carry (refuseClonesNotShipped,
// Readiness's D11 gate), unlike the disagreement above, which is a
// contradiction in the configuration itself. The row is the plan-only run of
// the refusal's own world: cascade checkout, node proxying the repository, and
// a bundle with no token.
func TestUpdateRepoPlanOnlyDoesNotRefuseTheDrop(t *testing.T) {
	r, db := proxiedDropRig(t)
	publishProxiedNode(t, db, true, proxiedRepoCanonical(t))
	putSwitchBundle(t, r, proxiedRepoBundle(t))
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", ""))
	t.Chdir(dir)
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--plan-only")
	if err != nil {
		t.Fatalf("a --plan-only run was refused over a drop it does not make: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("a --plan-only run applied %d time(s): %q", n, r.calls(t))
	}
}

// TestUpdateRepoPlanOnlyRefusesProxyDisagreement: the local entry and the
// published node disagreeing about whether acme/sandbox is proxied is refused
// under --plan-only exactly as it is under an applying run. The refusal exists
// because the next `fugaro project policy publish` from this config would move
// the fact under a running installation, and a plan that cannot be applied is
// not a plan: --plan-only exempts the refusals that stop a *change* (Readiness's
// D11 gate, refuseClonesNotShipped), never one that says the configuration
// itself is contradictory.
func TestUpdateRepoPlanOnlyRefusesProxyDisagreement(t *testing.T) {
	for _, args := range [][]string{
		{"update", "--repo", "--plan-only"},
		{"update", "--repo", "--plan-only", "--json"},
	} {
		r, db := proxiedRepoRig(t)
		r.appendConfig(t, "project_secrets:\n  bitbucket-token:\n    allow_data_to: [\"acme/sandbox\"]\n    proxy: true\n")
		r.save(t)
		// The node proxies the installation's token but admits another
		// repository, so acme/sandbox is not proxied by it.
		publishProxiedNode(t, db, true, "bitbucket/workspace-b/other")
		dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", ""))
		t.Chdir(dir)
		out, errOut, err := executeStdin(t, "", args...)
		if err == nil {
			t.Fatalf("%v: the run exited 0 with a contradictory config\nstdout:\n%s\nstderr:\n%s", args, out, errOut)
		}
		if got := ExitCode(err); got != ExitUserError {
			t.Errorf("%v: exit %d, want %d", args, got, ExitUserError)
		}
		got := out + errOut + ErrorText(err)
		if !strings.Contains(got, "disagree about acme/sandbox") {
			t.Errorf("%v: the refusal does not name the disagreement: %q", args, got)
		}
		if len(r.ran(t, "apply")) != 0 {
			t.Errorf("%v: a --plan-only run applied: %q", args, r.calls(t))
		}
	}
}
