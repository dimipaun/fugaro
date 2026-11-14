package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
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
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("one fugaro update --yes did not converge a version-%d node that proxies this repository: %v\nstdout:\n%s\nstderr:\n%s", projectpolicy.Version-1, err, out, errOut)
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
