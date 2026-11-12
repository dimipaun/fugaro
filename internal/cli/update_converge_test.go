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
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
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
//   - the policy node is the one the fixture's release left published (v7Node,
//     v6Node or v3Node), so the policy stage has to migrate it when it is
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

// v7Node is the published node this binary writes: nothing has to be migrated,
// only the rules generation, the base image and the plugin moved. It is the
// fixture for "an installation that already published under this release and is
// behind on everything else", and it is what tells a door that leaves a current
// node alone from one that rewrites it every time.
func v7Node() map[string]any {
	return map[string]any{
		"version":     7,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	}
}

// v6Node is the published node the release before this one leaves behind: one
// policy version below this binary's, so the run's policy stage has to carry it
// forward as well as move the rules generation, the base image and the plugin.
// This reader reads such a node as it stands (ReadsVersion: the step from 6 to
// 7 moves the number and nothing else — E2's model grades have no default to
// write, like AR0's binaries block before them), so the fixture is stale on
// everything else and on nothing about the node's own shape — which is the
// state the bump leaves an installation in between the update's image stages
// and its policy stage.
func v6Node() map[string]any {
	n := v7Node()
	n["version"] = 6
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
	fakeTerraform(t) // built with go, which upgradeEnv takes off PATH
	upgradeEnv(t, convergeRelease)
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
	slug, err := task.Slug("github", "acme/app")
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
		converge(t, v7Node(), []string{"policy-rules-skew", "plugin-pin"})
	})
	// The node the previous release publishes: one policy version below this
	// binary's, so the policy stage has to carry it forward by the same run
	// that moves everything else (D990). It is *not* a skew this binary reports
	// (ReadsVersion reads the node's own shape), which is why the fixture's
	// staleness here is the rules generation and the plugin alone: the node is
	// written forward by the run, and the node it leaves is what the assertion
	// in converge reads.
	t.Run("the node the previous release publishes", func(t *testing.T) {
		converge(t, v6Node(), []string{"policy-rules-skew", "plugin-pin"})
	})
	// The node two releases back: version 4, whose step to 5 is not shape-free
	// (it marks every ownerless pool legacy-open), so the reader refuses it
	// until the same run's policy stage has carried it through both steps.
	t.Run("the node two releases back", func(t *testing.T) {
		node := v7Node()
		node["version"] = 4
		converge(t, node, []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})
	})
	// The node two versions back: a v3 node, which an installation that has not
	// republished since 0.13.0 carries. 0.14.0's operator notes removed that
	// node by hand and published again; the migration chain (a later release's)
	// does it inside `fugaro update` instead, so this is the fixture the update
	// door has to converge.
	t.Run("the node two versions back", func(t *testing.T) {
		converge(t, v3Node(nil), []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})
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
	r := newConvergeRig(t, v6Node())
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
	})}
	r.save(t)

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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
// same installation — a version-4 node published, the previous release's
// credentials service deployed — whose credentials image THIS release has not
// published yet (the state every release is in until its images stage has
// published it) used to reach the policy stage first, write the version-5 node,
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
	r := newConvergeRig(t, v6Node())
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
	})}
	r.save(t)
	recorded := credentialsServiceEntry(t, r.imagesRig)["image"]

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err == nil {
		t.Fatalf("an update whose credentials image this release does not publish succeeded, want the resolve's failure:\n%s", s)
	}
	if stage := stoppedStage(out); stage != initflow.Policy {
		t.Errorf("the run stopped at %q, want the %s stage: the resolve is the write's own precondition, and it may not land after the node has moved — the service the installation runs then refuses every call that reads the node\n%s", stage, initflow.Policy, s)
	}
	if v := nodeVersionOn(t, r.db); v != 5 {
		t.Errorf("the published policy is version %d, want 5: the node moved without the service that reads it, so every cascade and Bitbucket-proxied call of this project is refused until a later update succeeds\n%s", v, s)
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
	r := newConvergeRig(t, v6Node())
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
	})}
	r.save(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	recorded := credentialsServiceEntry(t, r.imagesRig)["image"]

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil && !strings.Contains(s, "left for you") {
		t.Fatalf("an update in a coding agent's session failed with something other than the steps it leaves for the user: %v\n%s", err, s)
	}
	if v := nodeVersionOn(t, r.db); v != 5 {
		t.Errorf("the published policy is version %d, want 5: the session raised the node in a run that replaces no service, so the previous release's service refuses every call that reads it until a later update succeeds\n%s", v, s)
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
	// The one thing this run writes in the checkout is the pin coming out
	// (U5's migration, inside this same `fugaro update`): the fixture carries
	// nothing but the pin, so both entries go and both parent keys with them,
	// and an empty settings object is left — never a deleted file.
	if got := upgradeRead(t, r.settings); got != "{}\n" {
		t.Errorf("the checkout's settings after the run:\n%s\nwant the pin taken out and nothing else moved:\n%s", got, "{}\n")
	}
	// And with the pin gone there is nothing stale left for the line every
	// command prints to say about this checkout: the door the line sends the
	// reader to is also the door that clears it (U2's review, #2235: until this
	// migration, a checkout an older release pinned kept printing it).
	if line := staleLine(pluginwire.Status(r.settings, convergeRelease, installedPlugins())); line != "" {
		t.Errorf("after fugaro update the staleness line still fires for a checkout the run unpinned: %q", line)
	}
	if p := convergeNode(t, r); p.Version != projectpolicy.Version {
		t.Errorf("after one fugaro update, the published node is version %d, want %d", p.Version, projectpolicy.Version)
	}
	if !strings.Contains(out, "plugin: done: installed "+convergeRelease) {
		t.Errorf("the plugin step did not converge the install on this release's version:\n%s", out)
	}
	// This run onboarded the repository itself (it is the run the anchor writes
	// fugaro.yaml's gcp_project: line in, which leaves the working tree
	// different from the default branch): the job images are built here, from
	// the repository this run converged, and a run that built neither of them
	// would be one refusing to build from a file it did onboard.
	//
	// Two builds reach Cloud Build, and both are the checkout stage's cloud
	// step — not the repository stage's: that stage offers a first build only
	// for a workflow with no build record (infra.NeedsBuild), and both of this
	// rig's workflows carry one, the release behind's, built FROM that release's
	// base image. What is behind is those two images, and the refresh is what
	// rebuilds them off those records (refreshVerdict).
	//
	// The count is the whole point of the assertion: that gap is not one the
	// repository stage covers, so a run whose checkout stage never applies
	// builds nothing at all. The cloud step's own check reads fugaro.yaml and
	// the local config and never a build record (upgrade_cloud.go's cloudCheck),
	// and by the time the checkout stage runs the images stage has already
	// pinned this release's base in that config — so the stage's own answer is
	// not the verdict that check gives (init_checkout_upgrade.go's Check).
	if n := len(r.builder.specs); n != 2 {
		t.Errorf("%d job image(s) reached Cloud Build, want the two the checkout stage's cloud step rebuilds from the records of the release behind: the job images of a repository this run onboarded are refreshed here", n)
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

	before := doctorChecks(t, r.root)
	for _, id := range stale {
		if c, ok := doctorCheckByID(before.Checks, id); !ok || c.OK {
			t.Fatalf("the fixture is not a previous release's: %s is %+v", id, c)
		}
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
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
	// plugin-pin is asserted the other way round, and the assertion is not an
	// omission: nothing in this tree writes a pin any more (U2, D949), so what
	// this run must NOT do is report the pin converged — a version read back
	// there would be the merge's write coming back. What it does instead is
	// take the entry out (U5's migration, inside this same `fugaro update`), so
	// the check reads the same file as a checkout with no marketplace at all.
	c, ok := doctorCheckByID(after.Checks, "plugin-pin")
	if !ok {
		t.Fatalf("after fugaro update, doctor has no plugin-pin check at all: %+v", after.Checks)
	}
	if !strings.Contains(c.Problem, "no fugaro marketplace") {
		t.Errorf("after fugaro update, doctor's plugin-pin check reads %+v, want the checkout read as one with no fugaro marketplace: the run takes the pin out (U5)", c)
	}
	if strings.Contains(c.Problem, convergePrev) {
		t.Errorf("after fugaro update, doctor's plugin-pin check still reads the previous release's pin: %s", c.Problem)
	}
	for _, c := range after.Checks {
		// The per-workflow settings comparison, which is the update's own
		// work: its images stage rebuilds the workflow images the records
		// still show on the previous release's base, so no image-config- line
		// may be firing when it is done.
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

// TestUpdateKeepsAHandWrittenBinariesBlock is the update door's half of the
// review finding AR0's keep exists for, and the door the probe that found it
// ran: this release models `binaries` and no key of the operator's config
// states it (D986), so the policy stage's publish has to carry the node's block
// over rather than rewrite the node without it — a rewrite that takes the
// owner's grant away in silence (the list gone, and max_bytes back at the
// ceiling it was lowered from), where the same node was *refused* while the key
// was unmodelled ("publishing would drop them"). Both doors build the node
// through policyNodeForPublish, so this is the same keep the explicit door's
// TestPublishKeepsTheBinariesListWhenTheLocalFileSaysNothing pins, reached
// through the one command the HARD RULE says the whole upgrade is.
func TestUpdateKeepsAHandWrittenBinariesBlock(t *testing.T) {
	node := v7Node()
	node["binaries"] = map[string]any{"allow": []string{"brand/*.png"}, "max_bytes": 1048576}
	r := newConvergeRig(t, node)
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
	p := convergeNode(t, r)
	if want := []string{"brand/*.png"}; !slices.Equal(p.BinariesAllow(), want) {
		t.Errorf("after one fugaro update, the node's binaries.allow = %v, want %v: the run dropped the owner's grant", p.BinariesAllow(), want)
	}
	if got := p.BinariesMaxBytes(); got != 1048576 {
		t.Errorf("after one fugaro update, the node's binaries.max_bytes = %d, want 1048576: the run put the cap back at the ceiling it had been lowered from", got)
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
	r := newConvergeRig(t, v6Node())
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

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	p := convergeNode(t, r)
	want := projectpolicy.ImageMap{"acme/app": "app"}
	if !reflect.DeepEqual(p.ImageMap, want) {
		t.Errorf("after one fugaro update, the published node's image_map = %v, want %v: the map the local config names did not reach the node, so a run would fall back to the catalog's default image",
			p.ImageMap, want)
	}
	if p.BakeRepos == nil || *p.BakeRepos {
		t.Errorf("after one fugaro update, the published node's bake_repos = %v, want a written false: the switch the local config states did not reach the node",
			p.BakeRepos)
	}
	if p.BakesRepos() {
		t.Error("the published node bakes repos, want it not to: the reader every run uses is what a run would act on")
	}
}

// TestUpdateConvergesNoCatalogDefault is O4 reading (b) at the door the HARD
// RULE is about: a project whose image catalog marks no default still converges
// with `fugaro update` alone. The catalog is the one thing that could take the
// per-repository image away, so a project that publishes entries without
// choosing one has to keep the images its workflows were built as — every job
// on the per-repository tag, and the images stage rebuilding exactly those —
// rather than stopping on a project that names no entry for a workflow.
//
// The alternative the plan rejected, a resolver that refuses when no default
// exists, would leave this installation needing a second command: `fugaro
// image build` and a catalog edit before the next `fugaro update`.
func TestUpdateConvergesNoCatalogDefault(t *testing.T) {
	const entry = "tools"
	r := newConvergeRig(t, v6Node())
	r.appendConfig(t, "image_catalog:\n  entries:\n    - name: "+entry+"\n      tags: [node]\n      source: { kind: inline, inline: \"FROM scratch\\n\" }\n")
	registerInstallation(t, r.initRig)
	if p := convergeNode(t, r); p.ImageCatalog != nil {
		t.Fatalf("the fixture's node already carries a catalog (%v): it is not an installation whose policy predates it", p.ImageCatalog)
	}

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	p := convergeNode(t, r)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 1 || p.ImageCatalog.Entries[0].Name != entry {
		t.Fatalf("after one fugaro update, the published node's image_catalog = %+v, want the one local entry: the catalog did not reach the node", p.ImageCatalog)
	}
	for _, e := range p.ImageCatalog.Entries {
		if e.Default {
			t.Errorf("after one fugaro update, the published entry %s is the default, want none: the project's config named no entry for its workflows", e.Name)
		}
	}
	// Two builds reach Cloud Build, one per workflow, and both are the checkout
	// stage's cloud step's — as in the converge rig's own count above: each of
	// the two workflows carries a build record of the release behind, so the
	// repository stage offers no first build (infra.NeedsBuild), and what the
	// cloud step rebuilds off those records is each workflow's own job image.
	if n := len(r.builder.specs); n != 2 {
		t.Fatalf("%d job image(s) reached Cloud Build, want one per workflow, the cloud step's: with no catalog default every workflow keeps its own image", n)
	}
	// The pin the fixture records carries a digest the fake source registry
	// answers differently, so what is compared is the repo:tag the pin names.
	pinned, _, _ := strings.Cut(pinnedRef("base", convergeRelease), "@")
	for _, s := range r.builder.specs {
		if strings.Contains(s.Image, "catalog-"+entry) {
			t.Errorf("workflow %s was built as %s, want the repository's own derived image: no catalog entry is this project's default, so no job runs one (O4 b)", s.Workflow, s.Image)
		}
		if base, _, _ := strings.Cut(s.Base, "@"); base != pinned {
			t.Errorf("workflow %s was built from %s, want this release's pinned base %s: the rebuild is the per-repository image's", s.Workflow, s.Base, pinned)
		}
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
// already runs: no catalog entry is any job's, and the images stage rebuilds
// exactly the per-repository images the records show. Both workflows write the
// key, because a workflow that wrote none has no image of its own to keep and
// is refused in so many words — the shape D1191 exists for, on this release and
// on the one behind alike.
func TestUpdateConvergesAPrebuiltDefaultBesideAWorkflowsOwnImage(t *testing.T) {
	const entry = "standard"
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
		"  app: { dockerfile: .fugaro/app.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n" +
		"  api: { dockerfile: .fugaro/api.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n"
	r := newConvergeRigYAML(t, v6Node(), yaml)
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

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	p := convergeNode(t, r)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 1 || p.ImageCatalog.Entries[0].Name != entry || !p.ImageCatalog.Entries[0].Default {
		t.Fatalf("after one fugaro update, the published node's image_catalog = %+v, want the one local entry %s, marked default: the catalog did not reach the node", p.ImageCatalog, entry)
	}
	// The same two builds as the converge rig's own count: a workflow that
	// wrote a dockerfile: keeps the image this project builds for it, so the
	// cloud step rebuilds each workflow's own job image and no job is handed
	// the prebuilt entry the catalog marks.
	if n := len(r.builder.specs); n != 2 {
		t.Fatalf("%d job image(s) reached Cloud Build, want one per workflow: a workflow that wrote a dockerfile: of its own keeps its own image, because the catalog's default is one its job cannot run", n)
	}
	for _, s := range r.builder.specs {
		if strings.Contains(s.Image, "catalog-"+entry) {
			t.Errorf("workflow %s was built as %s, want the repository's own derived image: the prebuilt default holds no checkout of this repository, so it is set aside for a job that wrote an image of its own (D1191, D1900)", s.Workflow, s.Image)
		}
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
	r := newConvergeRig(t, v6Node())
	r.appendConfig(t, "image_catalog:\n  entries:\n"+
		"    - name: app\n      tags: [node, postgres]\n      services: [postgres, redis]\n"+
		"      source: { kind: image, image: ghcr.io/acme/app:1 }\n")
	registerInstallation(t, r.initRig)
	// The fixture is the release behind, or nothing here has to converge.
	if p := convergeNode(t, r); p.ImageServices != nil {
		t.Fatalf("the fixture's node already carries image_services = %v: it is not an installation whose policy predates the key", p.ImageServices)
	}

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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
	r := newConvergeRig(t, v7Node())
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

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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
	r := newConvergeRig(t, v7Node())
	currentRelease(t)
	ahead := rules.OrchestratorRulesVersion + 1
	r.db.Set(rules.PathOrchestratorRulesMarker(), ahead)
	puts := r.db.RulePuts()

	out, errOut, err := executeStdin(t, "", "update", "--yes")
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
		// The resume line is the run's own: "stopped at policy; fix: rerun
		// fugaro update: it resumes at policy". The stage is what stands between
		// the two sentences, and it is the name the loop's own stages carry.
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "stopped at ")
		if !ok {
			continue
		}
		if stage, _, ok := strings.Cut(rest, "; fix: "); ok {
			return stage
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
	r := newConvergeRig(t, v6Node())
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// The log is the run's own file, and XDG_STATE_HOME is the developer's.
	isolateState(t)
	_ = r

	out, err := executeConsole(t, "update", "--yes")
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
			_ = newConvergeRig(t, v6Node())
			currentRelease(t)
			skillWarned.Store(false)
			t.Cleanup(func() { skillWarned.Store(false) })
			if tc.noClaude {
				noClaudeOnPath(t)
			}

			out, errOut, err := executeStdin(t, "", "update", "--yes")
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
