package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The convergence of an installation that is one release behind: the whole of
// the fix is `fugaro update`, and doctor is clean afterwards. The two versions
// are this rig's own (the images rig publishes them in its fake source
// registry), not the release the fixture stands for.
const (
	convergePrev    = "1.2.2"
	convergeRelease = "1.2.3"
)

var argsUpdUser = []string{"plugin", "update", "fugaro@fugaro", "--scope", "user"}

// convergeRig is an installation set up at convergePrev and left alone since,
// in a checkout of its own repository:
//
//   - the policy node is the one the fixture's release left published (currentNode,
//     v7Node or v3Node), so the policy stage has to migrate it when it is
//     behind and leave it alone when it is not;
//   - the deployed rules carry the previous generation's marker, so
//     policy-rules-skew fires and the rules stage has to deploy;
//   - the local config records the previous release's base image, so
//     policy-vs-jobs fires and the images stage has to re-pin it;
//   - the checkout still carries the pin the previous release wrote, and that
//     release's plugin is installed, so plugin-pin fires — the pin is a
//     leftover nothing in the tree writes any more (U2 of
//     docs/plugin-user-scope/implementation-plan.md, D949), which this same
//     run takes out of the checkout (U5) — and the checkout stage has to move
//     the installation.
type convergeRig struct {
	*imagesRig
	root     string
	settings string
	// claude is the fake the checkout stage's plugin step runs: what it
	// recorded is how a test sees that the plugin was installed in Claude
	// Code, as opposed to pinned in the checkout's settings.
	claude *testutil.ClaudePluginFake
	// builder is the fake Cloud Build the checkout stage's cloud step submits
	// to: what it recorded is how a test sees, and counts, the billable
	// job-image builds of a run.
	builder *fakeBuilder
}

// currentNode is the published node this binary writes: nothing has to be
// migrated, only the rules generation, the base image and the plugin moved. It
// is the fixture for "an installation that already published under this release
// and is behind on everything else", and it is what tells a door that leaves a
// current node alone from one that rewrites it every time.
//
// The version is read from projectpolicy.Version rather than spelled, because
// what the fixture is about is the rest of the installation and not the number:
// a node one version behind this binary's is stale in a way the readers refuse
// from this release on (v7Node), which is the other fixture.
func currentNode() map[string]any {
	return map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	}
}

// v7Node is the published node the release before this one (0.25.0) leaves
// behind, so the run's policy stage has to carry it forward as well as move the
// rules generation, the base image and the plugin.
//
// It is no longer a node this reader reads as it stands: the step from 8
// rewrites the node's craft_pools and crafts (MC21, D1668), so ReadsVersion is
// false for version 7 as well, and every reader of the node refuses it until
// the update's policy stage migrates it — which is the state an installation is
// in between the update's image stages and its policy stage, and the reason the
// convergence tests below are what pins that the update converges it.
func v7Node() map[string]any {
	n := currentNode()
	n["version"] = v7Version
	return n
}

// v7Version is the policy version the release before this one (0.25.0) leaves
// published, which is what v7Node writes and what the fixtures below hold their
// "the node did not move" assertions against. It is spelled here rather than
// read from projectpolicy, whose Version has moved twice since 0.25.0 wrote one.
const v7Version = 7

// storedNodeVersion is the version field of the node as the database holds it,
// with none of the carry DecodeCarriedForward does: a fixture that asserts its
// own staleness needs the number the release behind wrote, and from this
// release's bump that is not what a carried decode reports.
func storedNodeVersion(t *testing.T, r *convergeRig) int {
	t.Helper()
	v := r.db.Value(projectpolicy.PathShared())
	if v == nil {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	return head.Version
}

// newestRefusedVersion is the newest policy version below this binary's that
// the reader still refuses and the chain carries forward: a shape-free
// predecessor is read as it stands (ReadsVersion), so it is not the previous
// release's node in the sense the update's fixtures need.
func newestRefusedVersion(t *testing.T) int {
	t.Helper()
	v := projectpolicy.Version - 1
	for projectpolicy.ReadsVersion(v) {
		v--
	}
	if !projectpolicy.Migratable(v) {
		t.Fatalf("version %d is no longer one the chain carries forward: the fixture needs the newest version that is", v)
	}
	return v
}

// staleNode is the node a release the reader refuses leaves published: the
// update's policy stage has to migrate it in place.
func staleNode(t *testing.T) map[string]any {
	n := v7Node()
	n["version"] = newestRefusedVersion(t)
	return n
}

func newConvergeRig(t *testing.T, node map[string]any) *convergeRig {
	t.Helper()
	return newConvergeRigYAML(t, node, refreshYAML)
}

// newConvergeRigYAML is newConvergeRig over a checkout whose default branch
// holds yaml as fugaro.yaml: the fixture for the update door's own failures,
// where the file the repository stage reads is the thing that is wrong.
func newConvergeRigYAML(t *testing.T, node map[string]any, yaml string) *convergeRig {
	t.Helper()
	fakeTerraform(t) // built with go, which convergeEnv takes off PATH
	convergeEnv(t, convergeRelease)
	// The checkout stage's cloud step builds the stale workflows, and a build
	// leases its estimated cost before it is submitted (T4): its lease needs
	// the caps below and the price of a build, which a hermetic test reads
	// from its own book rather than the Cloud Billing catalog.
	stubBuildPricer(t)
	r := newImagesRig(t, convergeRelease)
	buildBudgetCaps(r.db)
	r.installationState(t)
	// The installation is there and nothing about it has to change: the plan
	// reads back empty, so the run is about the stale things alone.
	r.script["plan"] = map[string]any{"exit": 0}
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.save(t)

	runs := t.TempDir()
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app, api], github_app_id: \"12345\" }\n")
	r.appendConfig(t, "image_source: ghcr.io/dimipaun\n"+
		"base_images: {base: "+pinnedRef("base", convergePrev)+"}\n"+
		"bucket_url: file://"+runs+"\n"+
		"budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+r.db.URL+", firebase_project: "+initProject+" }\n")
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: initProjectName, GCPProject: initProject})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project.json"), marker, 0o644); err != nil {
		t.Fatal(err)
	}

	// One config file, where both doors read it: the checkout's own project
	// file, so `fugaro update` writes what `fugaro doctor` then reads.
	registerInstallation(t, r.initRig)
	t.Setenv("FUGARO_CONFIG", "")
	t.Setenv("FUGARO_PROJECT", "")

	// The project's database as the release behind left it: this installation's
	// own (the mark), the previous rules generation deployed, and the published
	// policy node.
	r.db.Set(budget.PathProject, initProjectName)
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
	r.db.SetRules([]byte(`{"rules":{".read":false}}`))
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion-1)
	r.db.Set(projectpolicy.PathShared(), node)

	root := repoCheckout(t, githubOrigin, yaml)
	t.Chdir(root)
	settings := filepath.Join(root, ".claude", "settings.json")
	checkoutFile(t, settings, wiredAt("v"+convergePrev))
	checkoutFile(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"`+convergePrev+`"}]}}`)

	// The builds this installation made at the release behind, in both places
	// this rig reads a build record from (putWorkflowRecordBoth): the runs
	// bucket the checkout's cloud step opens, and the installation's runs
	// bucket as this rig's storage fake serves it, which is where the
	// repository stage's own read looks for one. The checkout stage's cloud
	// step rebuilds these two workflow images off those records: billable
	// builds, and here the fake builder's, never a real submit.
	builder := useFakeBuilder(t)
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	putWorkflowRecordBoth(t, r.initRig, "app", convergePrev, pinnedRef("base", convergePrev))
	putWorkflowRecordBoth(t, r.initRig, "api", convergePrev, pinnedRef("base", convergePrev))

	// The marketplace an installation one release old has on the machine is the
	// unpinned one a previous fugaro declared, so the plugin step clears it and
	// declares it at this release's tag: the clear takes the older install with
	// it (claude's own record of the removal), and the install that follows the
	// pinned add is the one that leaves this release's version.
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall(convergePrev))
	f.On(t, "plugins", "[]", argsRemove...)
	f.On(t, "plugins", userInstall(convergeRelease), argsInstall...)
	useFakeClaude(t, f)

	seedAppSecret(t, r.initRig, "github-app-key")
	seedAppSecret(t, r.initRig, "claude-oauth-token")
	return &convergeRig{imagesRig: r, root: root, settings: settings, claude: f, builder: builder}
}

// seedAppSecret stores acme/app's own container for logical, as a run of
// fugaro secrets set would have: the secrets an installation one release old
// already carries.
func seedAppSecret(t *testing.T, r *initRig, logical string) {
	t.Helper()
	seedRepoSecret(t, r, "github", logical)
}

// seedRepoSecret is seedAppSecret for a named provider's repository, which the
// tests that move a checkout from one provider to the other need: the secrets a
// repository's runs want are stored under the slug of the provider the config
// records it at, so a repository that moves wants its secrets there.
func seedRepoSecret(t *testing.T, r *initRig, provider, logical string) {
	t.Helper()
	seedNamedRepoSecret(t, r, provider, "acme/app", logical)
}

// seedNamedRepoSecret is seedRepoSecret for any repository: the secrets stage
// reads every repository the project lists, so a sibling with none stored
// leaves the update needing its owner.
func seedNamedRepoSecret(t *testing.T, r *initRig, provider, repo, logical string) {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	r.sm.Seed(gcp.SecretID(slug, logical),
		map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: logical},
		[]byte("stored-before-this-run"))
}

// putWorkflowRecordBoth stores one workflow's build record of the release
// behind where BOTH of this rig's reads find it: the runs bucket the
// checkout's cloud step opens its records through (openRecordBucket), and the
// installation's runs bucket as the storage fake serves it, which is where
// the repository stage's own read looks for one (infra.NeedsBuild: a workflow
// WITH a record is not offered a first build, and one without is). They are
// one bucket in a real installation, so a fixture that seeded only one of
// them would have one of the two stages build an image the other read says is
// already built — and the count at the end of converge would say nothing
// about which of them did.
func putWorkflowRecordBoth(t *testing.T, r *initRig, wf, version, base string) {
	t.Helper()
	putWorkflowRecordFrom(wf, version, base)
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	key := imagecheck.RecordKey(slug, wf)
	r.gcs.Put(initRunsBucket, key, []byte(records[key]))
}

// currentRelease is the update check's answer: this binary is the latest
// release, so doctor's cli-version check has nothing to warn about.
func currentRelease(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v` + convergeRelease + `"}`))
	}))
	t.Cleanup(srv.Close)
	oldClient, oldEndpoint := updateCheckHTTPClient, updateCheckEndpoint
	updateCheckHTTPClient, updateCheckEndpoint = srv.Client(), srv.URL
	t.Cleanup(func() { updateCheckHTTPClient, updateCheckEndpoint = oldClient, oldEndpoint })
}

// TestUpdateConvergesFromThePreviousRelease: an installation one release
// behind reaches this one with `fugaro update` alone. Every stale fact the
// fixture was built with — the published policy node, the deployed rules
// generation, the recorded base image, the plugin pin and the installed
// plugin — is current afterwards, and doctor says so.
//
// If any of them needs a second command, the test fails and names the stage
// the door stopped at: that is a gap the update door owns, and the one command
// the user is told to run may not be two.
func TestUpdateConvergesFromThePreviousRelease(t *testing.T) {
	// The node this binary writes: nothing to migrate, so the run moves the
	// rules generation, the base image and the plugin and leaves the node
	// alone.
	t.Run("the node this binary writes", func(t *testing.T) {
		converge(t, currentNode(), []string{"policy-rules-skew", "repo-plugin-pin"})
	})
	// The node the previous release publishes: one policy version below this
	// binary's, so the policy stage has to carry it forward by the same run
	// that moves everything else (D990). It is *not* a skew this binary reports
	// (ReadsVersion reads the node's own shape), which is why the fixture's
	// staleness here is the rules generation and the plugin alone: the node is
	// written forward by the run, and the node it leaves is what the assertion
	// in converge reads.
	t.Run("the node the previous release publishes", func(t *testing.T) {
		converge(t, v7Node(), []string{"policy-rules-skew", "repo-plugin-pin"})
	})
	// The node two releases back: version 4, whose step to 5 is not shape-free
	// (it marks every ownerless pool legacy-open), so the reader refuses it
	// until the same run's policy stage has carried it through both steps.
	t.Run("the node two releases back", func(t *testing.T) {
		node := currentNode()
		node["version"] = 4
		converge(t, node, []string{"policy-older-than-binary", "policy-rules-skew", "repo-plugin-pin"})
	})
	// The node two versions back: a v3 node, which an installation that has not
	// republished since 0.13.0 carries. 0.14.0's operator notes removed that
	// node by hand and published again; the migration chain (a later release's)
	// does it inside `fugaro update` instead, so this is the fixture the update
	// door has to converge.
	t.Run("the node two versions back", func(t *testing.T) {
		converge(t, v3Node(nil), []string{"policy-older-than-binary", "policy-rules-skew", "repo-plugin-pin"})
	})
}

// TestUpdateMigratesTheNodeBeforeItReplacesTheCredentialsService is the order
// the owner ruled on 2026-11-10, and the half of AR0's first finding this
// release answers by ordering rather than by tolerance: the credentials service
// reads the published node on every cascade and Bitbucket-proxied call and
// refuses a node its own binary does not understand (credsvc's RTDBPolicy reads
// it through projectpolicy.Decode and refuses the call on the error), so the
// release that replaces the service may not be the release that hands it a node
// it has not migrated yet. The node is migrated first and the service is
// replaced in the stage immediately after it, with no stage between the two —
// which is the whole of what the order buys, and what this test pins.
//
// The installation is one release behind in both halves of the pair — a v4 node
// published, and the service on the previous release's image — and neither is a
// second command: one `fugaro update` reaches this release's node and this
// release's service, which is the HARD RULE's own shape and the reason the
// ruling is a reorder and not an operator step.
//
// The order is checked on the canonical order and on the order initStages
// registers, exactly as the rules-before-policy stage's own test is
// (TestUpdateRulesStageRunsBeforePolicy): the loop sorts by the first, and a
// reader of the second sees who runs when. The job images are held to the same
// rule in the same assertion — an image built by this release is what reads the
// migrated node, so the run moves them before the node and never after it. What
// the update cannot reach is an image it does not rebuild: a launch on one is
// refused by the launcher's own gate (run_allowlist_test.go's
// TestRunRefusesAJobImageThatCannotReadThePolicyNode), not left to halt a run at
// bootstrap.
func TestUpdateMigratesTheNodeBeforeItReplacesTheCredentialsService(t *testing.T) {
	pos := func(names []string, name string) int {
		for i, n := range names {
			if n == name {
				return i
			}
		}
		return -1
	}
	canonical := initflow.Names()
	images, policy, creds := pos(canonical, initflow.Images), pos(canonical, initflow.Policy), pos(canonical, initflow.CredentialsService)
	switch {
	case images < 0 || policy < 0:
		t.Fatalf("%s or %s is not a canonical stage: %v", initflow.Images, initflow.Policy, canonical)
	case creds < 0:
		t.Fatalf("%s is not a canonical stage: %v", initflow.CredentialsService, canonical)
	case images > policy:
		t.Errorf("the canonical order runs %s at %d and %s at %d: the job images are rebuilt before the node is migrated, or the run leaves an installation whose images cannot read its own policy", initflow.Images, images, initflow.Policy, policy)
	case policy > creds:
		t.Errorf("the canonical order runs %s at %d and %s at %d: the node is migrated before the service that reads it per call is replaced, so the service is never handed a node its own binary does not understand", initflow.Policy, policy, initflow.CredentialsService, creds)
	}
	if !slices.Contains(initflow.Needs(initflow.CredentialsService), initflow.Policy) {
		t.Errorf("the credentials-service stage does not need %s: %v", initflow.Policy, initflow.Needs(initflow.CredentialsService))
	}
	registered := namesOf(initStages(prodEngine(initOptions{door: updateDoor.name})))
	i, p, c := pos(registered, initflow.Images), pos(registered, initflow.Policy), pos(registered, initflow.CredentialsService)
	if i < 0 || p < 0 || c < 0 || i > p || p > c {
		t.Errorf("update registers the images stage at %d, the policy stage at %d and the credentials-service stage at %d in %v: the order is images, then policy, then the service", i, p, c, registered)
	}

	// The fixture the ruling is about: an installation at the release before
	// this one, with its service on that release's image. The image is this
	// release's in the fake source, so a run that reaches the move resolves and
	// verifies it rather than failing the resolve and leaving the service where
	// it was (the rig of the images kind publishes base and history only).
	r := newConvergeRig(t, v7Node())
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	// The installation root's plan holds the service's own revision moving, and
	// the root is applied for it (a plan that reports no changes is never
	// applied, TF.Plan's exit code being what says so). The rig's document
	// answers the state reads under "show -json" while the plan answers under
	// "show@installation"; without the first, the fallback the fake documents
	// (a script naming only "show" answers the state too) would let this stage's
	// plan be read as the installation's state, which manages nothing.
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	// The installation's outputs carry the service this run's tfvars name, as
	// the module's outputs.tf does: the installation stage refuses the
	// half-carried state (a tfvars block whose outputs name no service), and
	// this rig's repository stage reads the same document for the
	// installation's other facts.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	r.save(t)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	// Both halves moved in ONE run: the node is this release's, and the service
	// is on this release's image, which is the resolve's own reference and not a
	// value the run composed.
	if p := convergeNode(t, r); p.Version != projectpolicy.Version {
		t.Errorf("after one fugaro update, the published node is version %d, want %d: the run replaced the credentials service without migrating the node it reads per call", p.Version, projectpolicy.Version)
	}
	svc := credentialsServiceEntry(t, r.imagesRig)
	if got, want := svc["image"], r.credentialsRefAt(convergeRelease); got != want {
		t.Errorf("after one fugaro update, credentials_service.image = %v, want %s: the installation stayed on the previous release's service, which is a release whose reader refuses the node this same run published", got, want)
	}
	// And the move is that one member: every other fact the previous release
	// wrote is what the entry still holds, so the run that migrates the node
	// rewrites nothing of the service it does not own.
	for k, want := range map[string]any{
		"account_id": infra.CredentialsAccountID, "service": infra.CredentialsServiceName,
		"firebase_project": fpID, "url": credentialsServiceURL, "rtdb_url": r.db.URL,
	} {
		if got := svc[k]; got != want {
			t.Errorf("credentials_service.%s = %v, want %v: the move rewrote a fact of the service it does not own", k, got, want)
		}
	}
}

// TestUpdateFailsTheCredentialsResolveBeforeItMigratesTheNode is the failure
// half of the same rule, and the state this branch's review reproduced: the
// same installation — the release behind's node published, the previous
// release's credentials service deployed — whose credentials image THIS release
// has not published yet (the state every release is in until its images stage
// has published it) used to reach the policy stage first, write a node at this
// release's version,
// and only then stop at the resolve the move needs. What it left was a node at
// this version with the previous release's service still on the project: a
// service that refuses every cascade and Bitbucket-proxied call until a later
// update succeeds, which the owner's one command may not leave behind.
//
// The run fails with the node exactly where it was instead: the resolve is the
// write's own precondition (credentialsResolveBeforeRaise), and the failure
// lands at the policy stage — the credentials-service stage after it is already
// too late, the node being at this version by then.
func TestUpdateFailsTheCredentialsResolveBeforeItMigratesTheNode(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	// The block an earlier release left, and no publishCredentialsImage: the
	// release's signed "-pinned" tag is what the resolve reads, and this run
	// has none.
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	// The installation's outputs name the service the tfvars carry: a block
	// without it is the half-carried state the installation stage refuses (and
	// the repository roots read this output to decide whether a job mounts a
	// model credential at all).
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	// The installation root's plan is NOT empty here, and the fixture says so
	// rather than leaving it to the rig's default: this release's run writes the
	// secrets intake's block into an installation that has none (DS4.1), the
	// module's whole intake file is gated on that variable, so the apply that
	// follows it creates the service — and an installation stage whose plan
	// reports no changes while its tfvars gained a service is the shape the
	// audience fill refuses (TF.Plan's exit code is what says a plan has
	// something to apply). The plan's own content is not what this test is
	// about; where the run STOPS is, and a stage that plans nothing never
	// reaches the policy stage's precondition.
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.intake", "create"))
	r.save(t)
	recorded := credentialsServiceEntry(t, r.imagesRig)["image"]

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err == nil {
		t.Fatalf("an update whose credentials image this release does not publish succeeded, want the resolve's failure:\n%s", s)
	}
	if stage := stoppedStage(out); stage != initflow.Policy {
		t.Errorf("the run stopped at %q, want the %s stage: the resolve is the write's own precondition, and it may not land after the node has moved — the service the installation runs then refuses every call that reads the node\n%s", stage, initflow.Policy, s)
	}
	if v, want := nodeVersionOn(t, r.db), v7Version; v != want {
		t.Errorf("the published policy is version %d, want %d: the node moved without the service that reads it, so every cascade and Bitbucket-proxied call of this project is refused until a later update succeeds\n%s", v, want, s)
	}
	if got := credentialsServiceEntry(t, r.imagesRig)["image"]; got != recorded {
		t.Errorf("credentials_service.image = %v, want the recorded %v: a run that resolved no image rewrote the installation's tfvars\n%s", got, recorded, s)
	}
}

// TestUpdateInAnAgentSessionHoldsTheNodeAtItsVersion is the other half of the
// same rule, and the second state the review named: the move is not the only
// thing that can be missing when the policy stage writes. In a coding agent's
// session on a managed project the credentials-service stage SKIPS itself ("a
// coding agent's session deploys nothing") while the policy stage, which the
// gate admits, would go on and raise the node — leaving exactly the state the
// failure above leaves, without any failure at all: this release's node with the
// previous release's service deployed on the project, refusing every cascade and
// Bitbucket-proxied call until a later update succeeds.
//
// So the node stays where it is and the run says why, in the policy stage's own
// line: the version it would raise and the sentence naming the session. The
// credentials image is untouched too, which is what makes the two halves one
// decision rather than two.
func TestUpdateInAnAgentSessionHoldsTheNodeAtItsVersion(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	r.save(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	recorded := credentialsServiceEntry(t, r.imagesRig)["image"]

	out, errOut, err := executeStdin(t, "", "update")
	// What the policy stage says it did with the node is the run's own
	// account, so the log is read beside the screen (stageDetail).
	s := out + errOut + newestLog(t, "update")
	if err != nil && !strings.Contains(s, "left for you") {
		t.Fatalf("an update in a coding agent's session failed with something other than the steps it leaves for the user: %v\n%s", err, s)
	}
	if v, want := nodeVersionOn(t, r.db), v7Version; v != want {
		t.Errorf("the published policy is version %d, want %d: the session raised the node in a run that replaces no service, so the previous release's service refuses every call that reads it until a later update succeeds\n%s", v, want, s)
	}
	if !strings.Contains(s, "holds it there") || !strings.Contains(s, credentialsSessionHeld) {
		t.Errorf("the policy stage did not say it held the node where it was, and why:\n%s", s)
	}
	if got := credentialsServiceEntry(t, r.imagesRig)["image"]; got != recorded {
		t.Errorf("credentials_service.image = %v, want the recorded %v: a session rewrote the installation's tfvars\n%s", got, recorded, s)
	}
}

// converge runs the door once and checks the fixture converged. stale is the
// set of doctor checks the fixture has to be firing before the run, or the run
// below proves nothing: an installation that was current already would pass
// without the door doing anything.
func converge(t *testing.T, node map[string]any, stale []string) {
	t.Helper()
	r := newConvergeRig(t, node)
	out := convergeOn(t, r, stale)
	// The run writes nothing in the checkout: the pin the release behind left
	// is still there, byte for byte, and the run's account of it is the notice
	// line (U6a, D3340). The staleness line therefore still fires for this
	// checkout, which is the state U6a leaves and U6b retires.
	if got := convergeRead(t, r.settings); got != wiredAt("v"+convergePrev) {
		t.Errorf("the checkout's settings after the run:\n%s\nwant them left as the release behind wrote them:\n%s", got, wiredAt("v"+convergePrev))
	}
	if line := staleLine(pluginwire.Status(r.settings, convergeRelease, installedPlugins())); line == "" {
		t.Errorf("the staleness line stopped firing for a checkout that still carries the pin, which is not this run's doing (U6a)")
	}
	if p := convergeNode(t, r); p.Version != projectpolicy.Version {
		t.Errorf("after one fugaro update, the published node is version %d, want %d", p.Version, projectpolicy.Version)
	}
	if !strings.Contains(out, "plugin: done: installed "+convergeRelease) {
		t.Errorf("the plugin step did not converge the install on this release's version:\n%s", out)
	}
	// This run onboarded the repository itself (it is the run the anchor writes
	// fugaro.yaml's gcp_project: line in, which leaves the working tree
	// different from the default branch) — and it onboarded it apply-free: the
	// repository is admitted by the profile this same run writes for its
	// provider (S5's default, `standard` here), so it runs on the project's
	// shared job and no repository-level Terraform is applied for it at all
	// (M7, D2312 as amended).
	//
	// That is why NO job image reaches Cloud Build here, and the count is still
	// the whole point of the assertion: the two per-repository images the
	// records of the release behind show are the images the checkout stage's
	// cloud step used to rebuild off those records (refreshVerdict), and a
	// repository on a shared job has none of its own to rebuild — its
	// workflows' images are the project's, built FROM the base pin this same run
	// records, per project and not per repository (spec decision 3, F5).
	//
	// Put M7's guard back — let refreshBuilds run for a shared repository — and
	// this fails with the two builds the records of the release behind name.
	// The step's own line saying why it rebuilt nothing is on the run's log
	// rather than here (stageDetail: a stage line of a converge is the run's
	// account, D1352), and TestImagesStageSkipsASharedRepo is where it is read.
	//
	// TestUpdateConvergesSlimBase asserts the half that still moves for such an
	// installation, the base pin.
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build, want none: a repository the project's profile admits runs on the shared job and has no per-repository image of its own (M7, D2312)\n%s", n, out)
	}
}

// convergeOn is converge over a rig the caller built and can go on reading:
// the fixture is asserted stale, `fugaro update` is run once, and doctor is
// clean afterwards. converge is this plus the two checks about the checkout, so
// a test that has to look at the database the run left behind — the rules it
// deployed, the markers it wrote — reaches it through here rather than building
// a rig of its own and then running the door against another one.
//
// It returns the run's own stdout, so a test that is about what the door SAID —
// the line it prints about the runs in flight — reads the transcript the person
// read rather than running the door a second time.
func convergeOn(t *testing.T, r *convergeRig, stale []string) string {
	t.Helper()
	currentRelease(t)
	// The owner's own terminal, which is what confirms the steps a converge
	// asks about (T11 of docs/no-prompt-policy: the flag that used to stand in
	// for it is gone). A rig with no terminal would stop at the first of them,
	// which is what the refusals' own tests are for.
	fakeTerminal(t)

	before := doctorChecks(t, r.root)
	for _, id := range stale {
		if c, ok := doctorCheckByID(before.Checks, id); !ok || c.OK {
			t.Fatalf("the fixture is not a previous release's: %s is %+v", id, c)
		}
	}

	out, errOut, err := executeStdin(t, "aurora\n", "update")
	if err != nil {
		t.Fatalf("fugaro update stopped at %s (exit %d), so this installation needs a second command:\n%s\n%s",
			stoppedStage(out), ExitCode(err), out, errOut)
	}

	after := doctorChecks(t, r.root)
	for _, id := range []string{"cli-version", "base-image", "policy-vs-jobs", "policy-older-than-binary", "policy-rules-skew"} {
		if c, ok := doctorCheckByID(after.Checks, id); ok && !c.OK {
			t.Errorf("after fugaro update, doctor's %s check still fires: %s (fix: %s)", id, c.Problem, c.Fix)
		}
	}
	// A fixture whose checkout holds an older release's pin still holds it
	// after the run, and that is the U6a truth rather than an omission: nothing
	// in this tree writes a pin (U2, D949) and since U6a nothing takes one out
	// either — the leftover is a notice naming the edit (repo_notices.go,
	// D3340), and the checkout's file is left exactly as the release behind
	// wrote it. Since U6b the check that says so is the per-repository one
	// (R15), and it carries U6a's text. Only a fixture that asked for that
	// stale id in `stale` has a pin to report: the other rigs this helper
	// serves never wrote one.
	if slices.Contains(stale, repoPluginPinCheckID) {
		c, ok := doctorCheckByID(after.Checks, repoPluginPinCheckID)
		if !ok {
			t.Fatalf("after fugaro update, doctor has no %s check at all: %+v", repoPluginPinCheckID, after.Checks)
		}
		if c.OK || !strings.Contains(c.Problem, repoNoticePinEdit) {
			t.Errorf("after fugaro update, doctor's %s check reads %+v, want the previous release's pin still there, in U6a's words: nothing takes a pin out any more", repoPluginPinCheckID, c)
		}
		if !strings.Contains(out, repoNoticePinFile+": "+repoNoticePinEdit) {
			t.Errorf("the run printed no notice for the pin still in the checkout's %s:\n%s", repoNoticePinFile, out)
		}
	}
	for _, c := range after.Checks {
		// The per-workflow settings comparison, which is the update's own
		// work: the images stage re-pins the base the workflow images the
		// records still show were built from — and for a repository the project
		// admits, M7's cloud step rebuilds none of them — so no image-config-
		// line may be firing when it is done.
		//
		// image-catalog-<workflow> is deliberately not asserted here, and
		// asserting it would be wrong rather than merely weak: `fugaro update`
		// publishes the catalog and builds no entry of it, so an installation
		// whose catalog holds an entry no build has built is still told to run
		// `fugaro image build <entry>` afterwards — the check is outside
		// doctorUpdatePrefixes for exactly that reason (catalogImageChecks).
		// This fixture has no catalog at all, so the assertion could never
		// fire anyway.
		if strings.HasPrefix(c.ID, "image-config-") && !c.OK {
			t.Errorf("after fugaro update, doctor's %s check still fires: %s", c.ID, c.Problem)
		}
	}
	if !strings.Contains(out, convergeRelease) {
		t.Errorf("the run never names the release it converged to:\n%s", out)
	}
	return out
}

// TestInstallationAtPolicyVersion7ConvergesBinaries is this task's red step,
// and the state every installation of the release behind is in: its published
// node is version 7 and carries the binaries block it has carried since version
// 6, and `fugaro update` must end with a node at this binary's version that
// still carries that block.
//
// Both halves of the step are the assertions below, and each of them fails on
// its own without this task. The number: the chain has no step from 7 to this
// binary's version until this release adds one, so the run stops at the policy stage and the
// installation is left needing a second command — the HARD RULE's one failure.
// The block: the drift between the owner's config and the node plans a publish,
// and that publish rebuilds the node from the config, which states no binaries
// block here (the key's carrier in the project layer is new with the same
// release, AR1, D987, so a machine nobody has edited says nothing about it).
// What leaves the block standing is the keep the publish reads the config
// through (BinariesEffective): silence is not a request to withdraw a grant,
// and the owner who wants one withdrawn writes the empty block. The step itself
// moves the number and nothing else for this node (migrateV8ToV9, whose pools
// this node's block does not touch), so nothing here is what puts the member
// back.
func TestInstallationAtPolicyVersion7ConvergesBinaries(t *testing.T) {
	node := v7Node()
	node["binaries"] = map[string]any{"allow": []string{"brand/*.png"}, "max_bytes": 1048576}
	r := newConvergeRig(t, node)
	if v := nodeVersionOn(t, r.db); v != v7Version {
		t.Fatalf("the fixture's published node is version %d, want %d: the red step is about the node the release behind leaves", v, v7Version)
	}
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	if v, want := nodeVersionOn(t, r.db), projectpolicy.Version; v != want {
		t.Errorf("after one fugaro update, the published node is version %d, want %d", v, want)
	}
	p := convergeNode(t, r)
	if want := []string{"brand/*.png"}; !slices.Equal(p.BinariesAllow(), want) {
		t.Errorf("after one fugaro update, the node's binaries.allow = %v, want %v: the step to version 8 dropped the owner's grant", p.BinariesAllow(), want)
	}
	if got := p.BinariesMaxBytes(); got != 1048576 {
		t.Errorf("after one fugaro update, the node's binaries.max_bytes = %d, want 1048576: the step to version 8 put the cap back at the ceiling it had been lowered from", got)
	}
}

// TestUpdatePolicyStagePublishesTheBinariesBlock is the other half of the same
// command: the owner's own config states a block — the key the local project
// config gained with this release (AR1; the project layer file refuses it) —
// and `fugaro update` is what carries it onto the node. No second command,
// which is the HARD RULE's whole point here: an operator who edits `binaries:`
// in the local project config and runs `fugaro update` has published it, and
// one who never edits it never sees the member move.
//
// The fixture's node is this binary's own version (currentNode), because what this
// test is about is the editor's path and not the migration: nothing about the
// node is stale but the rules generation and the plugin, so the only reason the
// policy stage has anything to publish is the drift between this config's block
// and the node's — the binaries row policyConfigDrift gained with this release,
// without which the edit would sit in the file and never reach a run.
func TestUpdatePolicyStagePublishesTheBinariesBlock(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	r.appendConfig(t, "binaries:\n  allow:\n    - website/public/**\n    - brand/*.png\n  max_bytes: 1048576\n")
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	p := convergeNode(t, r)
	if want := []string{"website/public/**", "brand/*.png"}; !slices.Equal(p.BinariesAllow(), want) {
		t.Errorf("after one fugaro update, the node's binaries.allow = %v, want %v: the policy stage did not carry the owner's block onto the node", p.BinariesAllow(), want)
	}
	if got := p.BinariesMaxBytes(); got != 1048576 {
		t.Errorf("after one fugaro update, the node's binaries.max_bytes = %d, want 1048576: the policy stage did not carry the cap the owner set", got)
	}
}

// TestUpdateKeepsAHandWrittenBinariesBlock is the update door's half of the
// review finding AR0's keep exists for, and the door the probe that found it
// ran: the policy stage's publish has to carry the node's block over rather
// than rewrite the node without it — a rewrite that takes the owner's grant
// away in silence (the list gone, and max_bytes back at the ceiling it was
// lowered from), where the same node was *refused* while the key was unmodelled
// ("publishing would drop them"). The state it pins is the one this release
// leaves an installation in: the owner's config states no binaries block (its
// key arrived with this same release, AR1, so a file nothing was written into
// says nothing about it), and the node's own block is what every run of the
// project must go on reading. Both doors build the node through
// policyNodeForPublish, so this is the same keep the explicit door's
// TestPublishKeepsTheBinariesListWhenTheLocalFileSaysNothing pins, reached
// through the one command the HARD RULE says the whole upgrade is.
func TestUpdateKeepsAHandWrittenBinariesBlock(t *testing.T) {
	node := currentNode()
	node["binaries"] = map[string]any{"allow": []string{"brand/*.png"}, "max_bytes": 1048576}
	r := newConvergeRig(t, node)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	p := convergeNode(t, r)
	if want := []string{"brand/*.png"}; !slices.Equal(p.BinariesAllow(), want) {
		t.Errorf("after one fugaro update, the node's binaries.allow = %v, want %v: the run dropped the owner's grant", p.BinariesAllow(), want)
	}
	if got := p.BinariesMaxBytes(); got != 1048576 {
		t.Errorf("after one fugaro update, the node's binaries.max_bytes = %d, want 1048576: the run put the cap back at the ceiling it had been lowered from", got)
	}
}

// TestUpdateKeepsAHandWrittenModelGrades is the update door's half of the
// finding the model_grades keep exists for, the way the binaries test above is
// for its member: this release models the block and no key of the operator's
// config writes one onto the node, so the policy stage's publish has to carry
// the node's grades over rather than rewrite the node without them — silently,
// since no rung reads a grade in this release yet. Both doors build the node
// through policyNodeForPublish, so this is the same keep the explicit door's
// TestProjectPolicyPublishKeepsTheModelGrades pins, reached through the one
// command the HARD RULE says the whole upgrade is.
func TestUpdateKeepsAHandWrittenModelGrades(t *testing.T) {
	node := currentNode()
	node["model_grades"] = map[string]any{
		"strong":    map[string]any{"model": "anthropic/claude-sonnet-5.5", "access": "openrouter-main"},
		"strongest": map[string]any{"model": "anthropic/claude-opus-4.6"},
	}
	r := newConvergeRig(t, node)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	p := convergeNode(t, r)
	if p.ModelGrades == nil {
		t.Fatal("after one fugaro update, the node carries no model_grades: the run dropped the project's grades")
	}
	if got, want := p.ModelGrades.Of(config.GradeStrong), (config.Craft{Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"}); got != want {
		t.Errorf("after one fugaro update, model_grades.strong = %+v, want %+v: the run dropped the grade the owner set", got, want)
	}
	if got, want := p.ModelGrades.Of(config.GradeStrongest), (config.Craft{Model: "anthropic/claude-opus-4.6"}); got != want {
		t.Errorf("after one fugaro update, model_grades.strongest = %+v, want %+v: the run dropped the grade the owner set", got, want)
	}
}

// TestUpdatePolicyStagePublishesThePauseBlock is the pause settings' own half
// of the binaries test above (R11 of docs/model-credentials-routing, D3184,
// D3187, D3190): the local config is the block's carrier and `fugaro settings
// set` writes it, so the policy stage is what carries it into the node — the
// node and nothing else, because the credentials service is what decides
// between waiting, pausing and halting on it (PR3) and no job's environment
// carries a setting of this kind. Without the stage the operator's own edit
// would reach no run until they ran the explicit publish, which is the second
// command the HARD RULE forbids.
func TestUpdatePolicyStagePublishesThePauseBlock(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	r.appendConfig(t, "pause:\n  wait_in_job: 2m\n  min_pause: 40m\n  max_pause: 8h\n")
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	p := convergeNode(t, r)
	if p.Pause == nil {
		t.Fatal("after one fugaro update, the node carries no pause block: the policy stage did not carry the settings the owner set, and every run of the project is decided on the defaults instead")
	}
	for _, tc := range []struct {
		member string
		got    time.Duration
		want   time.Duration
	}{
		{"wait_in_job", p.Pause.WaitInJobDuration(), 2 * time.Minute},
		{"min_pause", p.Pause.MinPauseDuration(), 40 * time.Minute},
		{"max_pause", p.Pause.MaxPauseDuration(), 8 * time.Hour},
	} {
		if tc.got != tc.want {
			t.Errorf("after one fugaro update, the node's pause.%s = %s, want %s: the policy stage did not carry the setting the owner wrote", tc.member, tc.got, tc.want)
		}
	}
}

// TestUpdateKeepsAHandWrittenPauseBlock is the keep direction of the test
// above, and the one the update's own promise rests on: a node that already
// carries the block, read by a config that says nothing about it, keeps what it
// carries. That is the state every installation is in the moment this release
// arrives — the config's `pause:` key is this release's, so a file nothing was
// written into says nothing — and the merge is what stops a publish from
// putting a project's runs back on the defaults without anyone asking.
func TestUpdateKeepsAHandWrittenPauseBlock(t *testing.T) {
	node := currentNode()
	node["pause"] = map[string]any{"wait_in_job": "2m", "min_pause": "40m", "max_pause": "8h"}
	r := newConvergeRig(t, node)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})
	p := convergeNode(t, r)
	if p.Pause == nil {
		t.Fatal("after one fugaro update, the node carries no pause block: the run rewrote the node without the settings the project was paused by")
	}
	for _, tc := range []struct {
		member string
		got    time.Duration
		want   time.Duration
	}{
		{"wait_in_job", p.Pause.WaitInJobDuration(), 2 * time.Minute},
		{"min_pause", p.Pause.MinPauseDuration(), 40 * time.Minute},
		{"max_pause", p.Pause.MaxPauseDuration(), 8 * time.Hour},
	} {
		if tc.got != tc.want {
			t.Errorf("after one fugaro update, the node's pause.%s = %s, want %s: the run dropped the setting the node carried", tc.member, tc.got, tc.want)
		}
	}
}

// TestUpdateRefusesAPauseBlockTheMergeBreaks is the third state of the two
// tests above: neither half is wrong on its own and the block they make
// together is. The node's `min_pause` is 15m, which is the shortest pause that
// project takes and a node its own decode reads — the default 5m floor is under
// it; the file writes only a 20m in-job floor, which its own load reads — the
// default 30m minimum is above it. Merged, the floor is above the pause, which
// the credentials service would refuse every run of the project on.
//
// So the publish is where it has to be caught, and the run stops there: the
// sentence names the member the operator wrote, the file is what they have to
// change, and the node is left exactly as it was — a publish that wrote the
// block would leave a node every run of the project fails to read
// (projectpolicy's decode refuses the block whole).
func TestUpdateRefusesAPauseBlockTheMergeBreaks(t *testing.T) {
	node := currentNode()
	node["pause"] = map[string]any{"min_pause": "15m"}
	r := newConvergeRig(t, node)
	r.appendConfig(t, "pause:\n  wait_in_job: 20m\n")
	registerInstallation(t, r.initRig)

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err == nil {
		t.Fatalf("an update whose file and node make a pause block no run can use succeeded, want the publish's refusal:\n%s", s)
	}
	if stage := stoppedStage(out); stage != initflow.Policy {
		t.Errorf("the run stopped at %q, want the %s stage: the block is read by the credentials service out of the node, so the merge is refused where the node is written\n%s", stage, initflow.Policy, s)
	}
	// The refusal is the publish's own and not the file's: the file's block is
	// valid on its own (the default 30m minimum is above its 20m floor), so a
	// sentence about the merge is what says the refusal is where the two halves
	// met, beside the member the operator has to change.
	if !strings.Contains(s, "make together is not one a run can use") || !strings.Contains(s, "pause.wait_in_job") {
		t.Errorf("the refusal does not name pause.wait_in_job and the merge that broke it: %s", s)
	}
	p := convergeNode(t, r)
	if p.Pause == nil {
		t.Fatal("after the refused update, the node carries no pause block: the node's own block was dropped by a publish that wrote nothing")
	}
	for _, tc := range []struct {
		member string
		got    time.Duration
		want   time.Duration
	}{
		{"wait_in_job", p.Pause.WaitInJobDuration(), projectpolicy.DefaultWaitInJob},
		{"min_pause", p.Pause.MinPauseDuration(), 15 * time.Minute},
		{"max_pause", p.Pause.MaxPauseDuration(), projectpolicy.DefaultMaxPause},
	} {
		if tc.got != tc.want {
			t.Errorf("after the refused update, the node's pause.%s = %s, want %s: the refused merge reached the node, and every run of this project would fail to read it", tc.member, tc.got, tc.want)
		}
	}
}

// TestUpdateConvergesTheCrashReportSwitch is D738's whole update-side claim at
// the one door the HARD RULE is about: an installation whose published node is
// the node a release behind wrote — the mobile block as it states it and no
// `mobile_crash_reports` key, which is the key this release adds (D731) —
// reaches it with `fugaro update` alone, and one run is enough.
//
// The node is the current version's and not the release behind's, which is the
// state that makes the key's own fill load-bearing: no migration step runs for
// it, so `fugaro update`'s policy stage is the only thing that could carry the
// key, and it does it through the row `policyConfigDrift` has for it
// (project_policy_test.go's TestMobileCrashReportsDriftNamesTheKey is the same
// claim at the comparison itself). Without the fill the row would be silent —
// the node carries no value, so nothing would write one — and the app of every
// installation at the release behind would send nothing for ever, because the
// reader answers a key that is not there off
// (projectpolicy.MobileCrashReportsOn).
//
// The published default is `true` and not the reader's fail-safe, which is the
// other half: the value in the node is what an operator reads and what a hand
// edit turns off, so it has to be written rather than left to a default a later
// reader happens to know. The members the project wrote are asserted beside it,
// because a fill that overwrote one would be the same bug in the other
// direction.
func TestUpdateConvergesTheCrashReportSwitch(t *testing.T) {
	node := currentNode()
	node["mobile"] = map[string]any{
		"enabled":             true,
		"sync":                false,
		"accepted_providers":  []string{projectpolicy.ProviderGoogle},
		"max_auth_age":        "6h",
		"strict_new_device":   true,
		"costly_modes":        []string{"critical"},
		"biometric_on_costly": true,
	}
	r := newConvergeRig(t, node)
	// The fixture is the release behind, or the assertions below prove nothing:
	// the node it carries predates the key, in the node's own bytes.
	raw, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"`+projectpolicy.MobileCrashReportsKey+`"`)) {
		t.Fatalf("the fixture's node already carries %s (%s): it is not an installation whose node predates the key", projectpolicy.MobileCrashReportsKey, raw)
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	p := convergeNode(t, r)
	if p.Mobile == nil {
		t.Fatal("after one fugaro update, the node carries no mobile block: the run dropped the project's own switches")
	}
	if p.MobileCrashReports == nil || !*p.MobileCrashReports {
		t.Errorf("after one fugaro update, the node's %s = %v, want a written true: the key did not reach the node, so the app would send nothing and the operator would have no diff to read", projectpolicy.MobileCrashReportsKey, p.MobileCrashReports)
	}
	if !p.MobileCrashReportsOn() {
		t.Error("after one fugaro update, MobileCrashReportsOn() = false, want true: the reader every phone's session is assembled from is what decides whether a report is sent")
	}
	if p.MobileSync() {
		t.Error("after one fugaro update, sync = true, want the written false: the fill overwrote a member the project chose")
	}
	if got := p.MobileAcceptedProviders(); !slices.Equal(got, []string{projectpolicy.ProviderGoogle}) {
		t.Errorf("after one fugaro update, accepted_providers = %v, want the one the node names: the fill widened a list the project narrowed", got)
	}
	if !p.MobileStrictNewDevice() || !p.MobileBiometricOnCostly() {
		t.Errorf("after one fugaro update, strict_new_device = %v and biometric_on_costly = %v, want both true", p.MobileStrictNewDevice(), p.MobileBiometricOnCostly())
	}
	if got := p.MobileMaxAuthAge(); got != 6*time.Hour {
		t.Errorf("after one fugaro update, max_auth_age = %s, want 6h", got)
	}
	if got := p.MobileCostlyModes(); !slices.Equal(got, []rigor.Rigor{rigor.Critical}) {
		t.Errorf("after one fugaro update, costly_modes = %v, want [critical]", got)
	}
}

// TestUpdateConvergesPreviousPolicy: an installation whose local project config
// names a repo-to-image map and the bake switch — the two keys this release
// adds to the policy node (D1901, D1906) — reaches them with `fugaro update`
// alone. The node the fixture carries is the previous release's, so neither key
// is on it, and the run's policy stage is what publishes both: no second
// command, which is what this test is for (the HARD RULE, F15).
//
// The node is read back through internal/projectpolicy's own decoder, so what
// the assertions are made against is what a run of the project reads — and the
// keep rule the two keys are published under is the assertion's second half: a
// file that writes them replaces what the node holds, where a machine whose
// config is silent about them keeps it (the catalog's rule, D1140).
func TestUpdateConvergesPreviousPolicy(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	// The local config states both, which is all an operator does after this
	// release: `fugaro image map set` writes the map, and the bake switch is
	// the setup wizard's answer or `fugaro settings set bake_repos`.
	r.appendConfig(t, "image_map:\n  acme/app: app\nbake_repos: false\n")
	// A run selected by its checkout reads the copy registerInstallation made in
	// the projects directory and not this file (FUGARO_CONFIG is empty here), so
	// the file is registered again after the append and the run reads what this
	// test wrote.
	registerInstallation(t, r.initRig)
	// The fixture is the release behind, or nothing here has to converge: the
	// node this installation carries names neither key. It is read as raw
	// members rather than through convergeNode, because what this half asserts
	// is the fixture's own bytes: the node a run of the release behind leaves
	// is one this binary now reads (ReadsVersion), so decoding it would answer
	// and prove nothing about the state the keys are absent from.
	raw, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"image_map", "bake_repos"} {
		if bytes.Contains(raw, []byte(`"`+key+`"`)) {
			t.Fatalf("the fixture's node already carries %s (%s): it is not an installation whose policy predates both", key, raw)
		}
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	p := convergeNode(t, r)
	want := projectpolicy.RepoImages{"github/acme/app": "app"}
	if !reflect.DeepEqual(p.RepoImages, want) {
		t.Errorf("after one fugaro update, the published node's repo_images = %v, want %v: the map the local config names did not reach the node, so a run would fall back to the catalog's default image",
			p.RepoImages, want)
	}
	if p.BakeRepos == nil || *p.BakeRepos {
		t.Errorf("after one fugaro update, the published node's bake_repos = %v, want a written false: the switch the local config states did not reach the node",
			p.BakeRepos)
	}
	if p.BakesRepos() {
		t.Error("the published node bakes repos, want it not to: the reader every run uses is what a run would act on")
	}
	// The file drops the old member in the same run (R14), and the node keeps
	// it until the step after the credentials service takes it away.
	b, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if written := string(b); strings.Contains(written, "image_map") || !strings.Contains(written, "repo_images:") {
		t.Errorf("after one fugaro update, the local config still states the old member or names no repo_images:\n%s", written)
	}
}

// TestUpdateConvergesNoCatalogDefault is O4 reading (b) at the door the HARD
// RULE is about: a project whose image catalog marks no default still converges
// with `fugaro update` alone. The catalog is the one thing that could take the
// per-repository image away, so a project that publishes entries without
// choosing one must not stop on a project that names no entry for a workflow:
// on the release behind it kept the images its workflows were built as, every
// job on the per-repository tag and the images stage rebuilding exactly those,
// and since M7 — where such a repository runs on the project's shared job and
// has no per-repository image left — what the run has to do instead is publish
// the catalog and re-pin the base, with no entry marked default either way.
//
// The alternative the plan rejected, a resolver that refuses when no default
// exists, would leave this installation needing a second command: `fugaro
// image build` and a catalog edit before the next `fugaro update`.
func TestUpdateConvergesNoCatalogDefault(t *testing.T) {
	const entry = "tools"
	r := newConvergeRig(t, v7Node())
	r.appendConfig(t, "image_catalog:\n  entries:\n    - name: "+entry+"\n      tags: [node]\n      source: { kind: inline, inline: \"FROM scratch\\n\" }\n")
	registerInstallation(t, r.initRig)
	if p := convergeNode(t, r); p.ImageCatalog != nil {
		t.Fatalf("the fixture's node already carries a catalog (%v): it is not an installation whose policy predates it", p.ImageCatalog)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	p := convergeNode(t, r)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 1 || p.ImageCatalog.Entries[0].Name != entry {
		t.Fatalf("after one fugaro update, the published node's image_catalog = %+v, want the one local entry: the catalog did not reach the node", p.ImageCatalog)
	}
	for _, e := range p.ImageCatalog.Entries {
		if e.Default {
			t.Errorf("after one fugaro update, the published entry %s is the default, want none: the project's config named no entry for its workflows", e.Name)
		}
	}
	// And no build reaches Cloud Build, which is M7's change to this door
	// rather than a gap in it: the checkout is admitted by the profile this
	// same run writes for its provider (S5's default), so its workflows run on
	// the project's shared job and the two per-repository images the records of
	// the release behind show are not the run's to rebuild (D2312 as amended,
	// spec decision 3). The cloud step prints the line that says which of the
	// two reasons left the images alone, and it is on the run's log rather than
	// on this stream (stageDetail, D1352).
	//
	// The reading this test is about is unaffected and still asserted above:
	// the catalog reaches the node with no entry marked default, which is the
	// half a resolver refusing on a default-less catalog would have broken.
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build, want none: a repository the project's profile admits runs on the shared job and has no per-repository image of its own (M7, D2312)\n%s", n, out)
	}
}

// TestUpdateConvergesAPrebuiltDefaultBesideAWorkflowsOwnImage is the HARD
// RULE at the one shape this release's D1900 could have broken: a project whose
// catalog marks a PREBUILT entry as its default, in a checkout whose workflows
// wrote a `dockerfile:` of their own. On the release behind those workflows
// were left out of the catalog entirely, so they ran the image this project
// builds for them and the installation deployed; D1900 routes every workflow
// through the catalog, and the default it would have handed such a workflow is
// an entry the job-spec builder refuses (D1191, infra's unbuiltOnBaked) — so
// the door would have stopped and asked for a manual config edit, which is the
// second command the HARD RULE (F15) rules out.
//
// The resolver sets that default aside for a workflow that wrote an image of
// its own (catalog.JobImage), so the installation converges on the images it
// already runs: no catalog entry is any job's, and on the release behind the
// images stage rebuilt exactly the per-repository images the records showed.
// Since M7 there is no per-repository image to rebuild for this checkout — the
// profile the same run writes for its provider admits it, so it runs on the
// project's shared job — and what the test still pins is the half D1191 is
// about: the door converges rather than stopping to ask for a config edit.
// Both workflows write the key, because a workflow that wrote none has no image
// of its own to keep and is refused in so many words — the shape D1191 exists
// for, on this release and on the one behind alike.
func TestUpdateConvergesAPrebuiltDefaultBesideAWorkflowsOwnImage(t *testing.T) {
	const entry = "standard"
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
		"  app: { dockerfile: .fugaro/app.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n" +
		"  api: { dockerfile: .fugaro/api.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n"
	r := newConvergeRigYAML(t, v7Node(), yaml)
	// The two Dockerfiles the workflows name, as the repository behind had
	// them: `fugaro validate` reads a workflow's dockerfile: as a file that is
	// there, so a checkout without them is not the shape this test is about.
	const def = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN git clone \"$REPO_URL\" /work/repo\n"
	for _, wf := range []string{"app", "api"} {
		checkoutFile(t, filepath.Join(r.root, ".fugaro", wf+".Dockerfile"), def)
	}
	// The prebuilt default is what the release behind deploys with: the entry
	// is the project's own image, already built and pushed, so no job of this
	// installation has a build of it to wait for.
	r.appendConfig(t, "image_catalog:\n  entries:\n"+
		"    - name: "+entry+"\n      default: true\n      tags: [node]\n"+
		"      source: { kind: image, image: ghcr.io/dimipaun/"+entry+":1.0 }\n")
	registerInstallation(t, r.initRig)
	if p := convergeNode(t, r); p.ImageCatalog != nil {
		t.Fatalf("the fixture's node already carries a catalog (%v): it is not an installation whose policy predates it", p.ImageCatalog)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	p := convergeNode(t, r)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 1 || p.ImageCatalog.Entries[0].Name != entry || !p.ImageCatalog.Entries[0].Default {
		t.Fatalf("after one fugaro update, the published node's image_catalog = %+v, want the one local entry %s, marked default: the catalog did not reach the node", p.ImageCatalog, entry)
	}
	// And no build reaches Cloud Build, as in the converge rig's own count and
	// for M7's reason: the checkout is admitted by the profile this run writes
	// for its provider, so its workflows run on the project's shared job and
	// the per-repository images the records show are not the run's to rebuild
	// (D2312 as amended). No job was handed the prebuilt entry either — that
	// much is what the run converging at all shows, since a workflow whose job
	// the catalog would hand an image that holds no checkout of the repository
	// is refused in so many words (D1191, infra's unbuiltOnBaked) — and the
	// cloud step's line says which of the two reasons left the images alone, on
	// the run's log rather than on this stream (stageDetail, D1352).
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build, want none: a repository the project's profile admits runs on the shared job and has no per-repository image of its own (M7, D2312)\n%s", n, out)
	}
}

// TestEntryServicesOlderReaderConverges: an installation whose local project
// config declares the services a catalog entry's image provides — the fact this
// release adds (D1904, IS9) — reaches the published node with `fugaro update`
// alone, and it reaches it where a reader of the release behind can still read
// it (F20).
//
// Both halves are one test because they are one decision. The node the fixture
// carries is the previous release's, so no services are on it, and the run's
// policy stage is what publishes them: no second command, which is the HARD
// RULE (F15). Where they land is the second half: as the top-level
// `image_services` key and NOT as a member of the entry — the image_catalog
// block decodes strictly, so an entry member would make every reader of the
// release behind refuse the whole policy, and a published node is re-read while
// runs are in flight (the credentials service reads it per model call). The
// assertions are made on the raw node, because what an older reader meets is
// the bytes and not this binary's decoded view of them.
func TestEntryServicesOlderReaderConverges(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	r.appendConfig(t, "image_catalog:\n  entries:\n"+
		"    - name: app\n      tags: [node, postgres]\n      services: [postgres, redis]\n"+
		"      source: { kind: image, image: ghcr.io/acme/app:1 }\n")
	registerInstallation(t, r.initRig)
	// The fixture is the release behind, or nothing here has to converge.
	if p := convergeNode(t, r); p.ImageServices != nil {
		t.Fatalf("the fixture's node already carries image_services = %v: it is not an installation whose policy predates the key", p.ImageServices)
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	p := convergeNode(t, r)
	want := projectpolicy.ImageServices{"app": {"postgres", "redis"}}
	if !reflect.DeepEqual(p.ImageServices, want) {
		t.Errorf("after one fugaro update, the published node's image_services = %v, want %v: the services the catalog entry declares did not reach the node, so a launcher that reads the node would start nothing",
			p.ImageServices, want)
	}
	if got := strings.Join(p.ServicesFor("app"), " "); got != "postgres redis" {
		t.Errorf("the node's own reader answers ServicesFor(app) = %q, want postgres redis", got)
	}

	// And the entry itself carries no such member: that is what keeps the
	// bytes readable by a release that does not know the key at all.
	raw, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"services"`) {
		t.Errorf("the published node carries an entry member named services, which a reader of the release behind refuses whole:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"image_services"`) {
		t.Errorf("the published node carries no image_services key at all:\n%s", raw)
	}
}

// convergeNode is the policy node the fixture's installation carries, read the
// way `fugaro update` reads it.
//
// Carried forward rather than through Decode alone: the fixture is an
// installation of the release behind, and the node it carries is the version
// this binary's own reader refuses until the update has migrated it. The tests
// that read the node before the door runs are asking what the fixture holds —
// whether it already carries the key under test — and the refusing reader
// would fail them on the fixture rather than on the run. Nothing is written
// back, and that the migration landed is what convergeOn's own
// policy-older-than-binary check, asserted clean after the run, is for.
func convergeNode(t *testing.T, r *convergeRig) *projectpolicy.Policy {
	t.Helper()
	v := r.db.Value(projectpolicy.PathShared())
	if v == nil {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.DecodeCarriedForward(b)
	if err != nil || !found {
		t.Fatalf("the node at %s does not decode (found=%v): %v", projectpolicy.PathShared(), found, err)
	}
	return p
}

// TestUpdateConvergesTheOrchestratorRulesFromThePreviousGeneration: an
// installation whose deployed rules predate the orchestrator subtree reaches it
// with `fugaro update` alone, and the one run writes both halves — the
// generation marker and the document the marker names — so the daemon's first
// state write lands on rules that carry the subtree, rather than on a document
// under which the person's own phone may not read the state back (D1220,
// D1241; stateless orchestrator plan, O2).
//
// The fixture is the release behind: the deployed rules are the previous
// generation's document and carry no orchestrator marker at all, which is the
// state every installation is in until this release's update runs.
func TestUpdateConvergesTheOrchestratorRulesFromThePreviousGeneration(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	// The fixture is the previous generation, or the assertions below prove
	// nothing: no marker, and a document this binary does not generate.
	if got := r.db.Value(rules.PathOrchestratorRulesMarker()); got != nil {
		t.Fatalf("the fixture already carries %s = %v: it is not an installation whose rules predate the orchestrator subtree",
			rules.PathOrchestratorRulesMarker(), got)
	}
	if infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Fatal("the fixture's deployed rules are already the document this binary generates, so nothing has to converge")
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	// One run, and the marker records this binary's generation. A marker left
	// absent here is the daemon's own gate refusing to start on a database the
	// owner was told was up to date.
	if got := r.db.Value(rules.PathOrchestratorRulesMarker()); fmt.Sprint(got) != fmt.Sprint(rules.OrchestratorRulesVersion) {
		t.Errorf("after one fugaro update, %s = %v, want %d", rules.PathOrchestratorRulesMarker(), got, rules.OrchestratorRulesVersion)
	}
	// And the deployed document is the one the marker names: the subtree, with
	// the person's read and the explicit refusal of every token writer.
	var doc struct {
		Rules map[string]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(r.db.Rules(), &doc); err != nil {
		t.Fatalf("the deployed rules are not JSON: %v", err)
	}
	raw, ok := doc.Rules[rules.PathOrchestrator]
	if !ok {
		t.Fatalf("the rules this run deployed carry no /%s subtree: the marker claims a generation the document does not have", rules.PathOrchestrator)
	}
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("the deployed /%s node is not an object: %v", rules.PathOrchestrator, err)
	}
	if v, has := node[".write"]; !has || v != false {
		t.Errorf("the deployed /%s carries .write = %v, want false", rules.PathOrchestrator, v)
	}
	if cond, _ := node[".read"].(string); !strings.Contains(cond, "auth.token.mu") {
		t.Errorf("the deployed /%s .read = %v, want the expanded USER predicate", rules.PathOrchestrator, node[".read"])
	}
}

// TestUpdateConvergesTheVoicePolicyNodeFromThePreviousGeneration: an
// installation whose deployed rules predate the voice policy node reaches it
// with `fugaro update` alone, and the one run writes both halves — the
// generation marker and the document the marker names — so the hop P5.3.3
// builds is written against a node that is really there (mobile plan task
// P5.3.1, approved by the owner on 2026-11-14; the voice-evaluation switch
// design §4.3).
//
// The bump is a rules generation like any other, so the migration is the rules
// stage `fugaro update` already runs, and nothing here is a second command
// (CLAUDE.md's HARD RULE). What this test adds to the orchestrator one above is
// the subtree itself: an installation at the release behind carries no voice
// node, and after the run it carries the two nodes with the grants the
// template writes — a run token's read of its own project and of the shared
// node, and no writer anywhere. That the update is safe to run while a run is
// in flight, which is the other half of the same rule, is pinned by
// TestUpdateLeavesARunInFlightRunning.
func TestUpdateConvergesTheVoicePolicyNodeFromThePreviousGeneration(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	// The fixture is the release behind, or the assertions below prove
	// nothing: the marker is the previous generation and the deployed document
	// is not the one this binary generates — it has no `config` node at all,
	// so no voice policy can be read under it.
	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion-1) {
		t.Fatalf("the fixture carries %s = %v, want the previous generation %d: it is not an installation whose rules predate the voice policy node",
			projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion-1)
	}
	if infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Fatal("the fixture's deployed rules are already the document this binary generates, so nothing has to converge")
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion) {
		t.Errorf("after one fugaro update, %s = %v, want %d", projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion)
	}
	// And the document the marker names carries the voice subtree, child by
	// child: the grants are the whole point of the node, so the parent has
	// none and each child has its own.
	var doc struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(r.db.Rules(), &doc); err != nil {
		t.Fatalf("the deployed rules are not JSON: %v", err)
	}
	voice := deployedNodeOf(t, doc.Rules, "config", "policy", "voice")
	if len(voice) != 2 {
		t.Fatalf("the deployed config/policy/voice holds %d keys (%v), want the two the template writes: `shared` and `repos`", len(voice), sortedAnyKeys(voice))
	}
	if _, has := voice[".read"]; has {
		t.Errorf("the deployed config/policy/voice carries a `.read` of its own: an RTDB grant cascades down, so a grant here would hand every project's voice policy to every run")
	}
	if _, has := voice[".write"]; has {
		t.Errorf("the deployed config/policy/voice carries a `.write` of its own, want none")
	}
	shared := deployedNodeOf(t, voice, "shared")
	// The grants are the template's, rendered: `TOKEN` expands to the run
	// token's own claims, and the point of the assertion is what is in the
	// disjunction -- the run's `fp`/`fx`, and no user or consumer claim, which
	// is why a signed-in reader is refused this subtree.
	sharedRead, _ := shared[".read"].(string)
	if !strings.Contains(sharedRead, "auth.token.fp") {
		t.Errorf("the deployed config/policy/voice/shared .read = %q, want the run token's own predicate (TOKEN)", sharedRead)
	}
	if strings.Contains(sharedRead, "auth.token.mu") || strings.Contains(sharedRead, "auth.token.mc") {
		t.Errorf("the deployed config/policy/voice/shared .read = %q and carries a user or consumer claim: the node is the run token's alone", sharedRead)
	}
	if v, has := shared[".write"]; !has || v != false {
		t.Errorf("the deployed config/policy/voice/shared carries .write = %v, want false", shared[".write"])
	}
	repos := deployedNodeOf(t, voice, "repos")
	if _, has := repos[".read"]; has {
		t.Errorf("the deployed config/policy/voice/repos carries a `.read` of its own: the grant is on the `$slug` child, and a grant here would be every project's node")
	}
	slug := deployedNodeOf(t, repos, "$slug")
	slugRead, _ := slug[".read"].(string)
	if !strings.Contains(slugRead, "auth.token.fs == $slug") {
		t.Errorf("the deployed config/policy/voice/repos/$slug .read = %q, want the run token's predicate bound to this project (`TOKEN && auth.token.fs == $slug`)", slugRead)
	}
	if strings.Contains(slugRead, "auth.token.mu") || strings.Contains(slugRead, "auth.token.mc") {
		t.Errorf("the deployed config/policy/voice/repos/$slug .read = %q and carries a user or consumer claim: the node is the run token's alone", slugRead)
	}
	if v, has := slug[".write"]; !has || v != false {
		t.Errorf("the deployed config/policy/voice/repos/$slug carries .write = %v, want false", slug[".write"])
	}
}

// TestUpdateConvergesTheRegistrationKindFromThePreviousGeneration: an
// installation whose deployed rules predate the registration kind reaches it
// with `fugaro update` alone, and the one run writes both halves — the
// generation marker and the document the marker names — so the app's
// registration (P5.2.8) and the consumer that reads it (P5.2.4) are written
// against a database that admits the message (mobile plan task P5.2.2;
// the push-notifications design §3.5 (b) and §3.8).
//
// The fixture is the release behind, the marker one generation old over a
// document with no `config` node, and the assertion this generation is about is
// the app's *write*: a registration is a message the phone writes into its own
// inbox, so under the document behind every one of them is refused at the
// database with no error and the phone is registered with nothing. After one
// `fugaro update` the deployed mailbox admits the kind and the three fields,
// each gated on that kind and bounded by the shared schema's own number — and
// none of them carries a `.write` of its own, so the fields sit under the
// message's grant rather than widening who may write one.
//
// It is the same rules stage as every other generation (CLAUDE.md's HARD RULE):
// nothing here is a second command, and the update is safe while a run is in
// flight, which TestUpdateLeavesARunInFlightRunning pins.
func TestUpdateConvergesTheRegistrationKindFromThePreviousGeneration(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion-1) {
		t.Fatalf("the fixture carries %s = %v, want the previous generation %d: it is not an installation whose rules predate the registration kind",
			projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion-1)
	}
	if infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Fatal("the fixture's deployed rules are already the document this binary generates, so nothing has to converge")
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion) {
		t.Errorf("after one fugaro update, %s = %v, want %d", projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion)
	}
	var doc struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(r.db.Rules(), &doc); err != nil {
		t.Fatalf("the deployed rules are not JSON: %v", err)
	}
	inbox := deployedNodeOf(t, doc.Rules, "mailbox", "$mb", "inbox", "$msg")
	kind, _ := inbox["kind"].(map[string]any)
	enum, _ := kind[".validate"].(string)
	if !strings.Contains(enum, "newData.val() == 'registration'") {
		t.Errorf("the deployed kind enumeration (inbox/$msg/kind .validate = %q) does not carry `registration`: the document is not the one this generation deploys, and a registration message is refused at the database", enum)
	}
	for _, field := range []string{"token", "platform", "prefs"} {
		node, ok := inbox[field].(map[string]any)
		if !ok {
			t.Errorf("the deployed /mailbox/$mb/inbox/$msg carries no %s field: the app's registration is refused at the database", field)
			continue
		}
		v, _ := node[".validate"].(string)
		if !strings.Contains(v, "'registration'") {
			t.Errorf("the deployed /mailbox/$mb/inbox/$msg/%s .validate = %q, want it gated on the registration kind", field, v)
		}
		if _, has := node[".write"]; has {
			t.Errorf("the deployed /mailbox/$mb/inbox/$msg/%s carries a `.write` of its own: the field sits under the message's grant and must not widen who may write one", field)
		}
	}
	// The token's bound is the shared schema's own number, which the rules
	// package holds to it (TestMailboxRulesAgreeWithTheSharedSchema): what this
	// asserts is that the number reached the deployed document with the field.
	token, _ := inbox["token"].(map[string]any)
	if v, _ := token[".validate"].(string); !strings.Contains(v, "<= "+strconv.Itoa(rules.MaxTokenLength)) {
		t.Errorf("the deployed /mailbox/$mb/inbox/$msg/token .validate = %q, want the schema's token bound (%d)", token[".validate"], rules.MaxTokenLength)
	}
}

// TestUpdateConvergesTheSpendLeavesFromThePreviousGeneration: an installation
// whose deployed rules predate the four day-counter leaves a run's stage report
// writes per model reaches them with `fugaro update` alone, and the one run
// writes both halves — the generation marker and the document the marker names
// — so the writer the stage report grows is written against a database that
// admits what it writes (spend-accuracy tasks SA5.0 and SA5.1; D2648 as amended
// by D2650).
//
// The fixture is the deployed generation-6 document itself
// (internal/budget/rules/testdata/rules.generation6.json, the frozen copy of the
// golden main carried before this generation), not a stand-in: the assertion
// below is about what a document an installation is really running carries, and
// `rules.generation3.json` is the same kind of fixture one generation's worth of
// distance further back. The marker is the release behind's (RulesVersion-1),
// which is the state the atomicity argument is about: a stage report is one
// multi-path update, so under this document the *whole* report is refused rather
// than the four leaves (F10).
//
// The run is in flight across the update because that is the state the owner's
// rule of 2026-11-09 is about, and its later writes are the ones this generation
// must not disturb: under a document that only adds, the lease, the ledger and
// the counters the run holds before the update still land after it. The four
// leaves are the client's — a run's own report, not the update's — so what this
// test can assert of the deployed document is the grant, and the writer that
// really writes them is SA5.1's (TestUpdateLeavesARunInFlightRunning is the test
// that runs a report through the client the run uses).
func TestUpdateConvergesTheSpendLeavesFromThePreviousGeneration(t *testing.T) {
	// The fixture is read before the rig is built: newConvergeRig chdirs into
	// the checkout it makes, and the path below is this package's own.
	previous := previousGenerationRulesDocument(t)
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	r.db.SetRules(previous)
	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion-1) {
		t.Fatalf("the fixture carries %s = %v, want the previous generation %d: it is not an installation whose rules predate the spend leaves",
			projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion-1)
	}
	if infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Fatal("the fixture's deployed rules are already the document this binary generates, so nothing has to converge")
	}
	// The fixture is genuinely the generation behind, or the assertions after the
	// update say nothing about a difference: the document must carry the
	// registration kind's fields (generation 6's own addition, the mobile plan's
	// P5.2.2) and must not carry one of the leaves this generation adds.
	inbox := deployedNodeOf(t, deployedRulesOf(t, r.db.Rules()), "mailbox", "$mb", "inbox", "$msg")
	if _, ok := inbox["token"]; !ok {
		t.Fatalf("the fixture's document has no /mailbox/$mb/inbox/$msg/token: it is not the generation 6 the release behind deploys")
	}
	byModel := deployedNodeOf(t, deployedRulesOf(t, r.db.Rules()), "spend", "$day", "repos", "$slug", "byModel", "$model")
	for _, leaf := range []string{"notional", "rep", "tlo", "thi"} {
		if _, ok := byModel[leaf]; ok {
			t.Fatalf("the fixture's document already carries /%s/%s, so nothing has to converge", rules.PathByModel, leaf)
		}
	}

	run := startRunInFlight(t, r)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion) {
		t.Errorf("after one fugaro update, %s = %v, want %d", projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion)
	}
	if !infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Errorf("the update did not leave this binary's document deployed, so the run below is not under the rules this test reads it against:\n%s", r.db.Rules())
	}
	// The four leaves, each granted as `micros` is: the run token's own predicate
	// bound to this project, the day's bound, and a write that may only raise the
	// figure it finds and by no more than one write's worth. The two are read
	// together because the leaves are a stage report's own group — a document
	// that carried one without the others would refuse the report the same way
	// the old one does (SA5.1's writer writes all four in one update).
	after := deployedNodeOf(t, deployedRulesOf(t, r.db.Rules()), "spend", "$day", "repos", "$slug", "byModel", "$model")
	micros, ok := after["micros"].(map[string]any)
	if !ok {
		t.Fatalf("the deployed /%s carries no `micros` leaf to compare the four against", rules.PathByModel)
	}
	for _, leaf := range []string{"notional", "rep", "tlo", "thi"} {
		node, ok := after[leaf].(map[string]any)
		if !ok {
			t.Errorf("the deployed /%s carries no %s leaf: the stage report that writes it is refused at the database, whole (a multi-path update is atomic)", rules.PathByModel, leaf)
			continue
		}
		write, _ := node[".write"].(string)
		wantWrite, _ := micros[".write"].(string)
		if write != wantWrite {
			t.Errorf("the deployed /%s/%s .write = %q, want `micros`'s own (%q)", rules.PathByModel, leaf, write, wantWrite)
		}
		if !strings.Contains(write, "auth.token.fs == $slug") {
			t.Errorf("the deployed /%s/%s .write = %q, want the run token's predicate bound to this project (`TOKEN && auth.token.fs == $slug && DAYOK && newData.exists()`)", rules.PathByModel, leaf, write)
		}
		if strings.Contains(write, "auth.token.mu") || strings.Contains(write, "auth.token.mc") {
			t.Errorf("the deployed /%s/%s .write = %q and carries a user or consumer claim: the leaves are the run token's alone", rules.PathByModel, leaf, write)
		}
		validate, _ := node[".validate"].(string)
		wantValidate, _ := micros[".validate"].(string)
		if validate != wantValidate {
			t.Errorf("the deployed /%s/%s .validate = %q, want `micros`'s own (%q)", rules.PathByModel, leaf, validate, wantValidate)
		}
		for _, want := range []string{"newData.val() >= (data.exists() ? data.val() : 0)", "root.child('config/limits/maxReserveMicros').val()"} {
			if !strings.Contains(validate, want) {
				t.Errorf("the deployed /%s/%s .validate = %q, want it bounded by %q (a write may raise the figure and by no more than one write's worth, which is what the rules' own MAXRES is)", rules.PathByModel, leaf, validate, want)
			}
		}
	}

	run.laterWrites(t, r)
}

// TestRulesConvergeFromThePreviousVersion: an installation at the generation
// behind reaches this one's credential route with `fugaro update` alone, with a
// run in flight across the update (workspace-service task WS22, R3 and R4).
//
// The rule is a READ grant to a run's own token — a run reads its repository's
// route record at bootstrap, before its first call, and mints an identity token
// for the address it names — so the generation moves for the reason generations
// 4 and 8 did: a run under the document behind is refused a read its routing
// depends on. The migration is the rules stage `fugaro update` already runs, so
// nothing here is a second command and the owner has no other step (CLAUDE.md's
// HARD RULE); the fixture is the release behind — the marker one generation old
// over a document with no `config` node, so no route can be read under it.
//
// The run is in flight because the acceptance is about it: the update asks no
// run to finish first, rewrites nothing the run holds and reaches none of its
// writes, so the run that bootstrapped under the release behind finishes under
// this one (TestUpdateLeavesARunInFlightRunning is the general pin; this test
// reads the same state under the generation this task moves).
func TestRulesConvergeFromThePreviousVersion(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion-1) {
		t.Fatalf("the fixture carries %s = %v, want the previous generation %d: it is not an installation whose rules predate the credential route",
			projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion-1)
	}
	if infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Fatal("the fixture's deployed rules are already the document this binary generates, so nothing has to converge")
	}

	run := startRunInFlight(t, r)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	// One run, and the marker records this binary's generation over the document
	// the marker names.
	if got := r.db.Value(projectpolicy.PathRulesMarker()); fmt.Sprint(got) != fmt.Sprint(projectpolicy.RulesVersion) {
		t.Errorf("after one fugaro update, %s = %v, want %d", projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion)
	}
	if !infra.SameJSON(r.db.Rules(), generatedRules(t)) {
		t.Errorf("the update did not leave this binary's document deployed, so no run of this installation reads its route:\n%s", r.db.Rules())
	}
	route := deployedNodeOf(t, deployedRulesOf(t, r.db.Rules()), "config", "policy", "route")
	// The parent carries no grant of its own, which is the whole reason the
	// record is a leaf per repository: an RTDB grant cascades down, so a read
	// here would hand every repository's route — its address, and the holder of
	// each kind — to every run of the installation. It is also the path the
	// member's own update walks to remove the records of the repositories it no
	// longer publishes.
	if _, has := route[".read"]; has {
		t.Errorf("the deployed config/policy/route carries a `.read` of its own: a grant here cascades to every repository's record")
	}
	if _, has := route[".write"]; has {
		t.Errorf("the deployed config/policy/route carries a `.write` of its own, want none")
	}
	slug := deployedNodeOf(t, route, "$slug")
	read, _ := slug[".read"].(string)
	// The grant is the template's, rendered: `TOKEN` expands to the run token's
	// own claims and the leaf is bound to the token's slug claim, which is the
	// escape the path is keyed with — so one repository's run reads its own
	// record and no other's, and a signed-in reader is refused the subtree.
	if !strings.Contains(read, "auth.token.fp") {
		t.Errorf("the deployed config/policy/route/$slug .read = %q, want the run token's own predicate (TOKEN)", read)
	}
	if !strings.Contains(read, "auth.token.fs == $slug") {
		t.Errorf("the deployed config/policy/route/$slug .read = %q, want the run token's predicate bound to this repository (`TOKEN && auth.token.fs == $slug`)", read)
	}
	if strings.Contains(read, "auth.token.mu") || strings.Contains(read, "auth.token.mc") {
		t.Errorf("the deployed config/policy/route/$slug .read = %q and carries a user or consumer claim: the record is the run token's alone", read)
	}
	if v, has := slug[".write"]; !has || v != false {
		t.Errorf("the deployed config/policy/route/$slug carries .write = %v, want false: no token but the member's admins' writes the route (the member's own update, over IAM)", slug[".write"])
	}

	run.laterWrites(t, r)
}

// previousGenerationRulesDocument is the document the release before this one
// deployed: the frozen copy of the golden this generation's leaves were added
// to (internal/budget/rules/testdata/rules.generation6.json). It is read from
// the rules package's own testdata rather than derived from Generate(), because
// what the converge test is about is the document an installation is really
// running — a fixture derived by deleting this generation's leaves would be
// testing a document nobody deployed, which is the reading
// rules.generation3.json exists for.
func previousGenerationRulesDocument(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../budget/rules/testdata/rules.generation6.json")
	if err != nil {
		t.Fatalf("reading the generation-6 fixture: %v", err)
	}
	return raw
}

// deployedRulesOf is the `rules` object of a deployed document, which is the
// shape the database serves and the one deployedNodeOf walks.
func deployedRulesOf(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var root struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(doc, &root); err != nil {
		t.Fatalf("the deployed rules are not JSON: %v", err)
	}
	if root.Rules == nil {
		t.Fatal("the deployed rules carry no `rules` object")
	}
	return root.Rules
}

// deployedNodeOf walks the deployed document to the named node, one key at a
// time, and fails the test rather than returning an empty node for a path that
// is not there: a subtree a marker claims and the document does not carry is
// the state this convergence is about.
func deployedNodeOf(t *testing.T, node map[string]any, keys ...string) map[string]any {
	t.Helper()
	for i, k := range keys {
		child, ok := node[k].(map[string]any)
		if !ok {
			t.Fatalf("the deployed rules carry no %s node", strings.Join(keys[:i+1], "/"))
		}
		node = child
	}
	return node
}

func sortedAnyKeys(node map[string]any) []string {
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestUpdateRefusesToRewindTheOrchestratorMarker: a marker above this binary's
// generation is a database whose rules came from a newer Fugaro, and the deploy
// path refuses before anything is planned, so this binary's older document is
// never written over the newer one and the newer marker is never recorded over
// (stateless orchestrator plan, O2; the credentials marker's own rule).
//
// The refusal is what makes the marker a boundary rather than a note: without
// it `fugaro update` on an older binary would take the subtree out from under a
// daemon whose own gate reads the same marker.
func TestUpdateRefusesToRewindTheOrchestratorMarker(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	currentRelease(t)
	ahead := rules.OrchestratorRulesVersion + 1
	r.db.Set(rules.PathOrchestratorRulesMarker(), ahead)
	puts := r.db.RulePuts()

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("fugaro update deployed this binary's rules over a newer orchestrator generation:\n%s", out)
	}
	// The step's line is the first clause of its reason and the log the whole of
	// it (report.Brief), so the marker the refusal read is read back from there.
	whole := out + errOut + namedLog(t, out+errOut)
	if !strings.Contains(whole, rules.PathOrchestratorRulesMarker()) {
		t.Errorf("the refusal does not name the marker it read (%s), so the owner cannot tell which generation refused:\n%s",
			rules.PathOrchestratorRulesMarker(), whole)
	}
	if got := r.db.Value(rules.PathOrchestratorRulesMarker()); fmt.Sprint(got) != fmt.Sprint(ahead) {
		t.Errorf("the refusal wrote the marker: %s = %v, want the newer %d left alone", rules.PathOrchestratorRulesMarker(), got, ahead)
	}
	if n := r.db.RulePuts(); n != puts {
		t.Errorf("the refusal deployed the rules anyway (%d deployment(s), want the %d the fixture started with)", n, puts)
	}
}

// generatedRules is the document this binary deploys, for a test that has to
// tell a fixture's rules apart from the current generation's.
func generatedRules(t *testing.T) []byte {
	t.Helper()
	gen, err := rules.Generate()
	if err != nil {
		t.Fatalf("generating the rules: %v", err)
	}
	return gen
}

// stoppedStage is the stage `fugaro update`'s own summary names as the one it
// stopped at, so a failing convergence says which step needed a second
// command.
func stoppedStage(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), markFailed+" failed"); ok {
			return strings.TrimSpace(rest)
		}
		// The closing line is the run's own: "Failed in 2.2s. Rerun fugaro
		// update to resume at policy." The stage is what follows "to resume
		// at", and it is the name the loop's own stages carry.
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Failed in ")
		if !ok {
			continue
		}
		_, resume, ok := strings.Cut(rest, ". Rerun ")
		if !ok {
			continue
		}
		if _, stage, ok := strings.Cut(resume, " to resume at "); ok {
			return strings.TrimSuffix(stage, ".")
		}
	}
	return "(the summary names no failed stage)"
}

// doctorChecks is doctor's own answer in the checkout: the same command the
// user runs to see whether the update was the whole of it.
func doctorChecks(t *testing.T, root string) doctorOutput {
	t.Helper()
	out, errOut, err := execute(t, "doctor", "--json", "--dir", root)
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("doctor --json: %v (err %v)\n%s\n%s", jerr, err, out, errOut)
	}
	return o
}

// executeConsole runs the command tree with stdout and stderr on ONE writer,
// which is what a terminal is: the reporter draws its line in flight on stdout
// and a stage's own notes go to stderr, and both land on the same screen. A
// test that captures the two separately cannot see one running into the other,
// which is exactly the defect this seam is here for.
//
// writerIsTerminal answers true, so the reporter draws the line in flight (it
// is what decides colour, and colour decides the spinner).
func executeConsole(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := writerIsTerminal
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = old })

	cmd := NewRootCmd()
	both := &syncBuf{}
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(both)
	cmd.SetErr(both)
	cmd.SetArgs(NormalizeArgs(args))
	err := cmd.Execute()
	return both.String(), err
}

// TestUpdateEndsTheSpinnerLineBeforeTheProjectHeader: the stage's own header
// ("project: <name> (GCP <id>)") is printed to stderr while the reporter's line
// in flight is on stdout, and a terminal draws both on the same row. The line
// has to come down before the header is written, or the person reads
// "- repositoryproject: <name>" — the stage run into the header, on no line of
// its own.
func TestUpdateEndsTheSpinnerLineBeforeTheProjectHeader(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// The log is the run's own file, and XDG_STATE_HOME is the developer's.
	isolateState(t)
	_ = r

	fakeTerminal(t)
	out, err := executeConsole(t, "update")
	if err != nil {
		t.Fatalf("fugaro update failed (exit %d): %v\n%s", ExitCode(err), err, out)
	}
	// The header itself, and not every sentence that happens to hold
	// "project: ": the name and the GCP project in parentheses are what the
	// first line of every cloud command says.
	header := regexp.MustCompile(`project: [a-z0-9-]+ \(GCP [a-z0-9-]+\)`)
	at := header.FindAllStringIndex(out, -1)
	if len(at) < 2 {
		t.Fatalf("the run printed the project header %d time(s), want the one at the door and the one each stage prints for its own checkout:\n%s", len(at), out)
	}
	for _, m := range at {
		if col := columnAt(out, m[0]); col != 0 {
			start := strings.LastIndex(out[:m[0]], "\n") + 1
			t.Errorf("the project header is not at the start of a line: the line in flight ran into it: %q", out[start:m[1]])
		}
	}
}

// columnAt is the column the byte at i sits in, on a terminal: a carriage
// return and the reporter's own clear-to-end-of-line both put the cursor back
// at the start, and nothing else in a run's output does.
func columnAt(s string, i int) int {
	line := s[strings.LastIndex(s[:i], "\n")+1 : i]
	if j := strings.LastIndex(line, "\r"); j >= 0 {
		line = line[j+1:]
	}
	line = ansiRE.ReplaceAllString(line, "")
	return len(line)
}

// ansiRE is a terminal escape sequence: what the reporter paints a frame with,
// and nothing a person reads as a column.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// TestUpdateNeverSendsThePersonToUpdateFromInsideIt: the converge's plugin
// stage used to end its wiring with the two notes written for `fugaro
// update-skills`, which told the person inside `fugaro update` to run the
// command they were running (the owner's feedback of 2026-11-10). The wiring
// write and its notes are gone (U2 of docs/plugin-user-scope); both worlds are
// still read so neither sentence comes back: the fake claude installs the
// plugin in one, and the other is the machine with no claude on PATH.
func TestUpdateNeverSendsThePersonToUpdateFromInsideIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		// noClaude is the machine the install half cannot run on.
		noClaude bool
	}{
		{"claude on PATH, so this run installs it", false},
		{"no claude on PATH", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = newConvergeRig(t, v7Node())
			currentRelease(t)
			skillWarned.Store(false)
			t.Cleanup(func() { skillWarned.Store(false) })
			if tc.noClaude {
				noClaudeOnPath(t)
			}
			fakeTerminal(t)

			out, errOut, err := executeStdin(t, "", "update")
			if err != nil {
				t.Fatalf("fugaro update failed (exit %d): %v\n%s\n%s", ExitCode(err), err, out, errOut)
			}
			for _, bad := range []string{"Run fugaro update to install the plugin for you", "Run fugaro update to refresh it for you"} {
				if strings.Contains(out, bad) {
					t.Errorf("the run prints %q: that is the command this run IS\n%s", bad, out)
				}
			}
		})
	}
}

// TestUpdateConvergesACachePriceWrittenTheOldWay: an installation whose owner
// priced a cache read by hand, under the key name the release behind documented,
// reaches this release with `fugaro update` alone — one command, no operator
// step (the HARD RULE), and no question asked (CLAUDE.md: fugaro asks for
// nothing). The run renames the key in the owner's own file, keeps the price as
// it is, and says both things: the rename, and the one warning about a price
// below the cheapest endpoint the built-in catalog prices that dimension at
// (SA4.2, SA4.3; R4, RS3).
//
// The file is the same fixture the stage's own test uses, and the door here is
// the whole run: the config `fugaro update` reads and writes is the one this
// rig left in the XDG projects directory, and the assertions after it are on
// the bytes that run wrote.
func TestUpdateConvergesACachePriceWrittenTheOldWay(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	// The keys go in before the copy the door reads is taken: the rig cleared
	// FUGARO_CONFIG, so the local config this run reads and writes is the
	// installation's own file in the XDG projects directory.
	r.appendConfig(t, cacheKeyFixture)
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	path := installationConfigPath()
	written := string(readFile(t, path))
	for _, want := range []string{"cache_read_mult: 0.0042", "cache_write_5m_mult: 1.4", "cache_write_1h_mult: 2.5"} {
		if !strings.Contains(written, want) {
			t.Errorf("one fugaro update left no %q in %s, so the owner needs a second command for it:\n%s", want, path, written)
		}
	}
	for _, old := range []string{"cache_read:", "cache_write_5m:", "cache_write_1h:"} {
		if strings.Contains(written, old) {
			t.Errorf("one fugaro update left %s in %s, so the owner needs a second command for it:\n%s", old, path, written)
		}
	}
	// The stage's own step line says what it did, and the mark on it is an
	// applied step rather than a question: the run asks nothing about this file
	// (the assertion stops where the screen cuts a long step line — the whole
	// of it is in the run's log, and what is asserted past that point is the
	// file itself).
	if want := "✔ model-prices renamed model_prices.deepseek/deepseek-v4.1-flash.cache_read ->"; !strings.Contains(out, want) {
		t.Errorf("the run did not print %q:\n%s", want, out)
	}
	// And the warning names both prices: the rename is not silent, and the
	// price the owner wrote is reported rather than refused.
	if want := "Warning: model_prices.deepseek/deepseek-v4.1-flash.cache_read_per_m is $0.000588 per million tokens"; !strings.Contains(out, want) {
		t.Errorf("the run did not warn about the price below the catalog's floor (%q), which is the other half of what this release owes the one command the owner runs:\n%s", want, out)
	}
	// And the price the node carries is the owner's, unchanged by the rename.
	// The node is published from the config the run holds, so a migration that
	// renamed the key in the file and lost the multiplier from that copy would
	// publish the list default in its place — every call of this model priced
	// differently from the file the owner reads, with nothing on the screen.
	if got := convergeNode(t, r).Prices["deepseek/deepseek-v4.1-flash"].CacheRead; got == nil || *got != 0.0042 {
		t.Errorf("after one fugaro update the published policy prices deepseek/deepseek-v4.1-flash's cache read at %v, want the 0.0042 the owner wrote", got)
	}
}

// wsMemberBAccount is the job account of update_workspace_test.go's owner table
// (wsOwnerTable): the principal the workspace root binds one invoke grant to,
// and the address the cover admits a grant to.
const wsMemberBAccount = "job-account-b@member-b-gcp.iam.gserviceaccount.com"

// wsApplies counts the applies of the workspace root in a converge's transcript:
// every apply is logged with the directory it ran in, and the workspace root's
// own is the one named "workspace".
func wsApplies(t *testing.T, r *convergeRig) int {
	t.Helper()
	n := 0
	for _, root := range r.applies(t) {
		if root == "workspace" {
			n++
		}
	}
	return n
}

// wsPlanSeq is the plans the workspace root is read under in one owner's run
// that DEPLOYS the service, in the order the door makes them: the plan view's
// check and the run's own, the apply's read, the URL fill's second plan, and the
// verify behind the apply — which is the one that finds nothing, the root having
// just been applied. The last step repeats, so a run that reads one more plan
// than this is not a failure of the fixture.
func wsPlanSeq() []map[string]any {
	return []map[string]any{{"exit": 2}, {"exit": 2}, {"exit": 2}, {"exit": 2}, {"exit": 0}}
}

// wsPlanSeqConverged is the same for a run whose block already carries the
// audience the root's own output reports: the fill has nothing to write — the
// revision behind the block admits its callers, whether this run moved its image
// or not — so the verify is the fourth read and the one that finds nothing.
func wsPlanSeqConverged() []map[string]any {
	return []map[string]any{{"exit": 2}, {"exit": 2}, {"exit": 2}, {"exit": 0}}
}

// wsOwnerConvergeRig is the fixture the workspace owner's converge is read over:
// an installation one release behind whose config declares a workspace of its own
// (ownerExtra is which half: the owner's, or the owner's with a member taken out),
// the installation's OWN credentials service as the release behind left it, and
// this release's credentials image published in the fake source.
//
// The image is what makes the run resolve one at all, and the resolve is the
// same one for both services: a fixture whose source publishes none deploys
// neither, so the state a workspace's own service is deployed in has to have it.
// The installation-side dance around its own service — the image's move, the
// audience's fill — is the fixture the sibling tests of this file already carry,
// and the four lines below put the rig in exactly that state.
func wsOwnerConvergeRig(t *testing.T, ownerExtra string) *convergeRig {
	t.Helper()
	r := newConvergeRig(t, v7Node())
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	// The installation root's plan moves the service onto this release's image,
	// and its state read is the rig's own document under the other spelling.
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	// The owner's half, and the copy the doors read: the rig cleared
	// FUGARO_CONFIG, so the config an update reads and writes is the
	// installation's own file in the XDG projects directory, which
	// registerInstallation is what takes from the rig's seed.
	appendWorkspaceConfig(t, r.initRig, ownerExtra)
	registerInstallation(t, r.initRig)
	return r
}

// TestUpdateConvergesAWorkspaceFromThePreviousRelease is WS7's red step, and the
// two halves of what the workspace owner's one command owes: the workspace's own
// credentials service is DEPLOYED by `fugaro update` alone — the root's new
// variables (`members` and `credentials_service`) are written and applied, with
// this release's resolved image, the owner's member job accounts and the URL
// Cloud Run assigned — and an installation at the previous release converges in
// that same run, needing no second command (the HARD RULE).
//
// Before this task the run wrote the root's tfvars with `project`, `workspace`
// and `names` alone: the service was never planned, never applied and never had
// an audience, so an owner who ran the one command they are told to run got a
// workspace with no service and nothing saying so. That is what the assertions
// below fail on.
//
// The second case is the other side of the same pin: a MEMBER's installation —
// the config that names the workspace and one pin, and no part of the owner's
// half — converges untouched, and the run writes no service address into it. A
// pin without an address is the member's own trust in the workspace (WS3), and
// inventing one here would be this release admitting a service the member's
// owner never told it about.
func TestUpdateConvergesAWorkspaceFromThePreviousRelease(t *testing.T) {
	t.Run("the workspace's own project, one release behind", func(t *testing.T) {
		r := wsOwnerConvergeRig(t, wsOwnerTable+wsOwnerCap)
		// The workspace root's own plan: the whole root, on the image the run
		// resolves, with one invoke grant for member-b — and the URL Cloud Run
		// assigned in the root's own outputs, which is what the second apply
		// writes into credentials_service.url.
		r.script["plan@workspace"] = wsPlanSeq()
		r.setPlanAt(t, "workspace", wsRootPlan(initProjectName, r.credentialsRefAt(convergeRelease), wsMemberBAccount)...)
		r.script["output@workspace"] = map[string]any{
			"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
		r.save(t)

		convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

		v := wsRootVars(t)
		want := map[string]workspaceMemberVar{"member-b": {JobServiceAccounts: []string{wsMemberBAccount}}}
		if !reflect.DeepEqual(v.Members, want) {
			t.Errorf("the workspace root's members = %+v, want %+v: the invoke grants are made from this table, and a run that writes none leaves every member's run unable to invoke the workspace's service (F17)\n", v.Members, want)
		}
		if v.Service == nil {
			t.Fatalf("the workspace root's tfvars carry no credentials_service after an update that had one to deploy: %+v", v)
		}
		for _, c := range []struct {
			name, got, want string
		}{
			{"service", v.Service.Service, creds.WorkspaceServiceName},
			{"image", v.Service.Image, r.credentialsRefAt(convergeRelease)},
			{"region", v.Service.Region, "us-east5"},
			{"bundle_secret", v.Service.BundleSecret, credsvc.WorkspaceBundleSecretID(initProjectName)},
			{"firebase_project", v.Service.FirebaseProject, initProject},
			{"rtdb_url", v.Service.RTDBURL, r.db.URL},
			{"url", v.Service.URL, wsServiceURL},
		} {
			if c.got != c.want {
				t.Errorf("the workspace root's credentials_service.%s = %q, want %q: the service is the same image an installation's runs, in the workspace's own project, reading the workspace's own database and reached at its own URL (D2980)", c.name, c.got, c.want)
			}
		}
		// The image written is the one the run resolved through the
		// installation's remote repository, not a value the run composed: the
		// root's own validation and the cover both hold it to that shape.
		if !strings.HasPrefix(v.Service.Image, "us-east5-docker.pkg.dev/"+initProject+"/"+infra.GhcrRegistry+"/") {
			t.Errorf("the workspace's service image %q is not this installation's remote repository's", v.Service.Image)
		}
		// TWO applies of the root, and both are the stage's: the one that creates
		// the service, and the one that writes the URL Cloud Run assigned back
		// into its audience (fillServiceURL). A run that stopped at the first
		// leaves a revision whose audience is the module's unassigned placeholder,
		// which admits no member's run at all.
		if n := wsApplies(t, r); n != 2 {
			t.Errorf("the update applied the workspace root %d time(s), want 2 (the create and the audience's own URL): %v", n, r.applies(t))
		}
	})
	t.Run("a member's pin names no address and is left alone", func(t *testing.T) {
		r := newConvergeRig(t, v7Node())
		r.appendConfig(t, wsPin)
		registerInstallation(t, r.initRig)

		convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

		// The config the doors read and write, of which the rig's own file is
		// only the seed (registerInstallation).
		b, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		written := string(b)
		if strings.Contains(written, "service_address") {
			t.Errorf("one fugaro update wrote a service address into a member's pin, and the pin the owner leaves without one converges untouched — the address is the workspace's own fact and not something this member's machine may invent (WS3):\n%s", written)
		}
		for _, want := range []string{"workspace: " + wsProjectName, "workspace_pin:", "firebase_project: project-ws",
			"service_account: marker-workspace@project-ws.iam.gserviceaccount.com"} {
			if !strings.Contains(written, want) {
				t.Errorf("the member's pin no longer carries %q after the update:\n%s", want, written)
			}
		}
		// A member's project is not a workspace's own, so the stage that deploys
		// the workspace's service skips it and never opens the root's workdir.
		if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", initProject, "workspace")); !os.IsNotExist(err) {
			t.Errorf("a member's update opened the workspace root's workdir: %v", err)
		}
	})
}

// TestUpdateDeploysTheWorkspaceServiceWithARunInFlight is R11's own line: the
// workspace owner's one command puts a new revision of the workspace's
// credentials service behind the address a member's run is already calling, over
// a run that is going on, and the note the door prints names that step — the
// revision drains rather than stops, so the run keeps the environment and the
// route it bootstrapped with.
//
// The fixture is the state that makes the step real: the workspace's own workdir
// holds the block the release behind deployed, on that release's image, which is
// the block this run replaces. workspaceServiceRevisionStale reads exactly that
// document, and the run in flight is started before the update, as a person's own
// run would be going when they converge the installation it belongs to.
func TestUpdateDeploysTheWorkspaceServiceWithARunInFlight(t *testing.T) {
	r := wsOwnerConvergeRig(t, wsOwnerTable+wsOwnerCap)
	wsSeedRecorded(t, initProjectName, oldCredentialsRef(convergePrev), wsServiceURL)
	// The block the release behind deployed carries its own audience, so this run
	// moves the image and writes no url: four reads, the last finding nothing.
	r.script["plan@workspace"] = wsPlanSeqConverged()
	r.setPlanAt(t, "workspace", wsRootPlan(initProjectName, r.credentialsRefAt(convergeRelease), wsMemberBAccount)...)
	r.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	r.save(t)

	startRunInFlight(t, r)
	line := inFlightLine(t, convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"}))

	if !strings.Contains(line, "workspace service:") {
		t.Errorf("the note does not name the workspace's service over a run in flight that this update puts a new revision of it behind (R11):\n%s", line)
	}
	if !strings.Contains(line, "drains") {
		t.Errorf("the note does not say the new revision drains the one a run started on, which is the whole of why the run keeps the environment and the route it bootstrapped with:\n%s", line)
	}
	// And the step the line named really happened: the document the root wrote is
	// this release's image, watched over the same plan that deploys it — a line
	// naming a step no run makes is worse than no line at all.
	v := wsRootVars(t)
	if v.Service == nil || v.Service.Image != r.credentialsRefAt(convergeRelease) {
		t.Errorf("after the update the workspace's service is %+v, want this release's image %s: the note named a step the run did not make", v.Service, r.credentialsRefAt(convergeRelease))
	}
}

// TestUpdateRemovesAMembersInvokeGrant is F25's own converge, the half of
// removing a member that is the workspace's own: the owner's table no longer
// names member-b, and the same one command takes that member's job account off
// the workspace's service — the ONE deletion the workspace root's cover admits,
// so `fugaro update` converges a shrunk member table with no repair and no second
// step.
//
// The plan is the one terraform really makes on that removal: the service the run
// is not moving (a no-op, where the plan's own service name is read from) and the
// grant going away. What the run must NOT do is refuse it, which is what
// convergeOn fails on — and what the case before this task did, the cover having
// held the deletion to the member table the workspace's config no longer names.
func TestUpdateRemovesAMembersInvokeGrant(t *testing.T) {
	r := wsOwnerConvergeRig(t, "workspace_members: {}\n"+wsOwnerCap)
	// The block this release's own run deployed: the member table is what moved,
	// and the service is not — so the run carries the block forward and applies
	// the removal alone, which is the one apply below. Its URL is the audience
	// that run read back, which is what makes this apply the last one: the
	// second apply is for a block with no audience, and this one has its own
	// (fillServiceURL reads the root's output to be told so).
	wsSeedRecorded(t, initProjectName, r.credentialsRefAt(convergeRelease), wsServiceURL)
	r.script["plan@workspace"] = wsPlanSeqConverged()
	r.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	r.setPlanAt(t, "workspace",
		wsServiceNoOpPlan(r.credentialsRefAt(convergeRelease)), wsInvokerRemovedPlan(wsMemberBAccount))
	r.save(t)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if n := wsApplies(t, r); n != 1 {
		t.Errorf("the update applied the workspace root %d time(s), want 1 (the member's grant going away): %v", n, r.applies(t))
	}
	// The document the run wrote names the account nowhere: F17's grant per job
	// account is that same table, one direction.
	raw, err := os.ReadFile(filepath.Join(wsWorkdirRoot(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), wsMemberBAccount) {
		t.Errorf("the workspace root's tfvars still name the member who left (%s), so nothing took the grant away:\n%s", wsMemberBAccount, raw)
	}
	if v := wsRootVars(t); len(v.Members) != 0 {
		t.Errorf("the workspace root's members = %+v, want none: the only member the owner's table named is gone", v.Members)
	}
}

// TestUpdateConvergesAWorkspaceWhoseServiceHasNoAudience is the state the second
// apply is gated on, and the one the update after a run has to heal: the
// workspace root's tfvars already carry this release's image and no url — what
// `fugaro update --plan-only` leaves, and what a create apply that never landed
// leaves — so the run carries the block forward rather than resolving anything,
// and terraform is content with it either way: an empty url is the module's own
// placeholder, so a plan over that document finds the service converged while
// the revision behind it admits no member's ID token at all.
//
// The block a run carries forward is therefore not evidence that the audit is
// done: the URL Cloud Run assigned is read back and applied on the update that
// finds the block without one, which is what the two applies and the written
// audience below pin. Gating that apply on this run's own resolve instead — the
// run that moves the image — would leave the workspace unreachable until the
// next release, which is the one thing the HARD RULE forbids.
func TestUpdateConvergesAWorkspaceWhoseServiceHasNoAudience(t *testing.T) {
	r := wsOwnerConvergeRig(t, wsOwnerTable+wsOwnerCap)
	wsSeedRecorded(t, initProjectName, r.credentialsRefAt(convergeRelease), "")
	r.script["plan@workspace"] = wsPlanSeq()
	r.setPlanAt(t, "workspace", wsRootPlan(initProjectName, r.credentialsRefAt(convergeRelease), wsMemberBAccount)...)
	r.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	r.save(t)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if n := wsApplies(t, r); n != 2 {
		t.Errorf("the update applied the workspace root %d time(s), want 2 (the root itself, and the audience write that makes the revision behind it admit a member's ID token): %v", n, r.applies(t))
	}
	if v := wsRootVars(t); v.Service == nil || v.Service.URL != wsServiceURL {
		t.Errorf("after the update the workspace's service is %+v, want the URL Cloud Run assigned (%s) written into its block: a block with no audience is a revision that admits nobody, and no plan over that document ever says so", v.Service, wsServiceURL)
	}
}

// wsPlanSeqNoAudienceAlone is wsPlanSeq for the state above once the root has
// nothing else in it: the root is the release's own already — the image moved,
// the members are the table — so the plan is EMPTY where it is read to decide
// whether the stage is done, and the only plan with a change in it is the one
// the audience write makes AFTER the tfvars are rewritten with the URL the
// root's own output reports. Five reads, in the door's order: the plan view's
// check, the run's own, the apply's read, the audience fill's plan, and the
// verify behind the apply, which finds nothing again.
func wsPlanSeqNoAudienceAlone() []map[string]any {
	return []map[string]any{{"exit": 0}, {"exit": 0}, {"exit": 0}, {"exit": 2}, {"exit": 0}}
}

// TestUpdateConvergesAWorkspaceWhoseServiceHasNoAudienceAndPlansNothing is the
// state the check itself has to answer Todo about, and the reason the audience
// write cannot live on the apply path alone: an update interrupted between the
// create apply (which put the service on the module's unassigned placeholder)
// and the write of the URL leaves a block with no audience beside a root that
// plans NOTHING — terraform reads the placeholder as the service's own audience
// and has no change to make.
//
// So a stage that decided by the plan alone reports this stage converged for
// ever, in the words of "its own credentials service is deployed on this
// release's image", while the revision behind that sentence admits no member's
// ID token — and nothing heals it until a release whose own change makes the
// plan non-empty, which is not a step anybody can run (the one-command rule).
// The three reads below are what it takes instead: the check is Todo on an empty
// plan where the block carries no url, the plan view is not "No changes" there
// (a coding agent's session records NothingToDo as the stage being done), and
// the apply makes the one write the plan cannot show. One apply, and the
// audience in the block.
func TestUpdateConvergesAWorkspaceWhoseServiceHasNoAudienceAndPlansNothing(t *testing.T) {
	r := wsOwnerConvergeRig(t, wsOwnerTable+wsOwnerCap)
	wsSeedRecorded(t, initProjectName, r.credentialsRefAt(convergeRelease), "")
	r.script["plan@workspace"] = wsPlanSeqNoAudienceAlone()
	// The one plan with a change in it is the fill's: the service listed as an
	// update, the audience moving, and nothing else in the root.
	r.setPlanAt(t, "workspace", wsServiceAudiencePlan(r.credentialsRefAt(convergeRelease)))
	r.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	r.save(t)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if n := wsApplies(t, r); n != 1 {
		t.Errorf("the update applied the workspace root %d time(s), want 1 (the audience write alone, the root having nothing else to change): %v", n, r.applies(t))
	}
	if v := wsRootVars(t); v.Service == nil || v.Service.URL != wsServiceURL {
		t.Errorf("after the update the workspace's service is %+v, want the URL Cloud Run assigned (%s) written into its block: a block with no audience is a revision that admits nobody, and the plan a root like this makes says nothing about it", v.Service, wsServiceURL)
	}
	// The owner is told what the run is about to do where there is no plan to
	// read, and the sentence is the state's own rather than the accounts a root
	// with changes creates (audienceTodo).
	if !strings.Contains(out, "unassigned audience") {
		t.Errorf("the run's own transcript does not say the revision the workspace's service is serving carries the module's unassigned audience, so the owner confirms a write it does not name:\n%s", out)
	}
}

// TestUpdateRendersTheRostersPeopleIntoTheWorkspaceProjectsIntake is WC3's own
// half of the workspace owner's one command (R5, D3143): the workspace's roster
// is rendered into the workspace's own project's intake — its invoker set and
// the class the process reads, which the module composes into one list — and
// nowhere else of that installation. The other half of the rule, the empty list
// on an installation of no workspace, is
// TestUpdateRendersNoPeoplesListOnAnInstallationOfNoWorkspace
// (init_credentials_test.go), which needs no workspace at all.
//
// The roster is read from the workspace's own database at
// workspaceRosters/<name> (C1's node, workspace.PathRoster) and is written as
// the person's ADDRESS, never an account id: the intake is admitted by the
// verified token's address against the class (F11) and names the container by
// the token's own `sub`, so nothing has to map one to the other. Only the
// HUMANS are written — a machine's entry is a service account's address, the one
// shape the launcher class refuses outright (internal/infra.IntakePeople), and
// an entry in the class no token can match is one nobody could ever be admitted
// by — and neither is a human whose address carries a colon, which the class
// list cannot hold either: the module spells every entry into the invoker grant
// as "user:<entry>", so an address that IS that spelling would name a principal
// IAM has none of (internal/access refuses an entry of that shape for the same
// reason). The roster holds both, and the renderer leaving the person off is
// what keeps the roster's own content from being an update that fails.
//
// The owner is in the list like anybody else, and that is the point of the
// fence F10 draws: the list is the INTAKE's alone (var.intake.people —
// local.intake_people in the module), so the same person is admitted to the
// installation's intake and granted nothing at its credentials service and
// grant route (the Terraform test the_intake_admits_the_rosters_people holds
// that half, root tests/installation.tftest.hcl).
func TestUpdateRendersTheRostersPeopleIntoTheWorkspaceProjectsIntake(t *testing.T) {
	r := wsOwnerConvergeRig(t, wsOwnerTable+wsOwnerCap)
	r.script["plan@workspace"] = wsPlanSeq()
	r.setPlanAt(t, "workspace", wsRootPlan(initProjectName, r.credentialsRefAt(convergeRelease), wsMemberBAccount)...)
	r.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	roster, err := workspace.EncodeRoster(&workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People: []workspace.Person{
			// A roster can hold an address whose local part carries a colon —
			// `workspace user add` stores one and CanonicalAddress keeps it —
			// and it is NOT the class list's: each entry there is spelled into
			// the invoker grant as the member form, and this is the member form
			// already (internal/access's own rule, and var.launcher_accounts'
			// shape since F31). The renderer leaves the person off rather than
			// write a value no plan takes, which is the same split
			// internal/access's admin list makes.
			{Address: "a:b@example.com", Kind: workspace.KindHuman, External: true},
			{Address: "ada@example.com", Kind: workspace.KindHuman, External: true},
			{Address: "bot@member-b-gcp.iam.gserviceaccount.com", Kind: workspace.KindSA},
			{Address: "owner@example.com", Kind: workspace.KindHuman, External: true},
			{Address: "user:ada@example.com", Kind: workspace.KindHuman, External: true},
			{Address: "zoe@example.com", Kind: workspace.KindHuman, External: true},
		},
		Admins: []string{"owner@example.com"},
	})
	if err != nil {
		t.Fatalf("encoding the roster: %v", err)
	}
	path, err := workspace.PathRoster(initProjectName)
	if err != nil {
		t.Fatalf("workspace.PathRoster: %v", err)
	}
	r.db.Set(path, json.RawMessage(roster))
	r.save(t)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	block, _ := r.tfvars(t)["intake"].(map[string]any)
	if block == nil {
		t.Fatalf("the installation's tfvars carry no intake: %v", r.tfvars(t))
	}
	raw, ok := block["people"].([]any)
	if !ok {
		t.Fatalf("the intake's block carries no people after the workspace owner's own update (R5): %v", block)
	}
	got := make([]string, 0, len(raw))
	for _, p := range raw {
		s, _ := p.(string)
		got = append(got, s)
	}
	want := []string{"ada@example.com", "owner@example.com", "zoe@example.com"}
	if !slices.Equal(got, want) {
		t.Fatalf("the intake's people are %v, want %v: the roster's humans, sorted and each once, minus the shapes the class list cannot carry — no machine, whose address is a service account's, and no address whose local part carries a colon, which is the member spelling the grant is made of rather than the bare address a token's email claim holds (internal/access's rule for the same kind of value, and what makes the person's absence fail closed rather than a plan no entry can pass)", got, want)
	}
}

// seedConvergedInstallation writes the installation's tfvars in the state one
// run of this release leaves behind: the credentials service's block with the
// audience its own output reports (seedCredentialsService's seed) and the
// secrets intake's block beside it, on the release's own image and with ITS
// audience written — the two writes a later run of the same release has nothing
// to fill.
func seedConvergedInstallation(t *testing.T, r *convergeRig, image string) {
	t.Helper()
	seedCredentialsService(t, r.imagesRig, image)
	path := filepath.Join(r.imagesRig.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := infra.SetIntake(b, infra.Intake(image))
	if err != nil {
		t.Fatal(err)
	}
	vars, err = infra.SetIntakeURL(vars, intakeServiceURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, vars, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateLeavesTheIntakeAloneWhenTheRosterNamesOnlyALauncher is the roster
// change the intake's fill must NOT write for: the workspace's roster names one
// person, and that person is already on the installation's launcher list, so the
// class the intake's revision is handed — the launchers and the roster's people
// as one list (intake.tf's local.intake_accounts) — is byte for byte what it was
// (infra.IntakeClass).
//
// The installation is converged: both audiences in the tfvars and an empty plan
// at the installation root. Writing the block for that person anyway would leave
// the apply with nothing to do, and the fill reads an empty plan as the module
// not carrying the class onto the revision at all and refuses the run with the
// audience's own sentence — a misleading failure over a roster update that
// changed no class, which is the state this test pins.
func TestUpdateLeavesTheIntakeAloneWhenTheRosterNamesOnlyALauncher(t *testing.T) {
	r := wsOwnerConvergeRig(t, wsOwnerTable+wsOwnerCap)
	seedConvergedInstallation(t, r, r.credentialsRefAt(convergeRelease))
	// The installation root has nothing left to change, so the only plan the
	// fill could read is one it made itself.
	r.script["plan@installation"] = map[string]any{"exit": 0}
	// The installation's own launcher list, as its IAM reports it back: the
	// owner, who is the one person the roster names.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
		"launchers":           []string{"user:test@example.com"},
	})}
	// The workspace's own root is converged too: its block carries the address
	// Cloud Run assigned, which is the fact that stage fills (WS7).
	wsSeedRecorded(t, initProjectName, r.credentialsRefAt(convergeRelease), wsServiceURL)
	r.script["plan@workspace"] = map[string]any{"exit": 0}
	r.script["output@workspace"] = map[string]any{
		"stdout": `{"workspace_service_url":{"value":"` + wsServiceURL + `"}}`}
	roster, err := workspace.EncodeRoster(&workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People: []workspace.Person{
			{Address: "test@example.com", Kind: workspace.KindHuman, External: true},
		},
		Admins: []string{"test@example.com"},
	})
	if err != nil {
		t.Fatalf("encoding the roster: %v", err)
	}
	path, err := workspace.PathRoster(initProjectName)
	if err != nil {
		t.Fatalf("workspace.PathRoster: %v", err)
	}
	r.db.Set(path, json.RawMessage(roster))
	r.save(t)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if strings.Contains(out, "plans nothing with credentials_service.url") {
		t.Errorf("the update refused itself over the roster's own person, who is on the installation's launcher list already: the class the intake's revision is handed is the launchers and the people as ONE list, and this roster moved neither\n%s", out)
	}
	vars := r.tfvars(t)
	raw, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	launchers, err := infra.LauncherAccountsOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"test@example.com"}; !slices.Equal(launchers, want) {
		t.Fatalf("the installation's launcher_accounts are %v, want %v: the roster's person is the launcher this fixture is about, so a list without them would test something else", launchers, want)
	}
	spec, have, err := infra.IntakeOf(raw)
	if err != nil || !have {
		t.Fatalf("IntakeOf(vars) = %+v, %v, %v, want the intake's block", spec, have, err)
	}
	if got, want := infra.IntakeClass(launchers, spec.People), []string{"test@example.com"}; !slices.Equal(got, want) {
		t.Errorf("the intake's class after the update is %v, want %v: the roster's only person is the installation's own launcher, so the class the revision is handed is one address — the launchers' — whatever the block's own list says", got, want)
	}
}
