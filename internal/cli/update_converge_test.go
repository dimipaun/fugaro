package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/infra"
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
//   - the policy node is the one the fixture's release left published (v4Node
//     or v3Node), so the policy stage has to migrate it when it is behind;
//   - the deployed rules carry the previous generation's marker, so
//     policy-rules-skew fires and the rules stage has to deploy;
//   - the local config records the previous release's base image, so
//     policy-vs-jobs fires and the images stage has to re-pin it;
//   - the checkout is pinned to the previous release's plugin and has it
//     installed, so plugin-pin fires and the checkout stage has to move both.
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

// v4Node is the published node the release before this one leaves behind: the
// version this binary writes too, so nothing has to be migrated, only the
// rules generation, the base image and the plugin moved.
func v4Node() map[string]any {
	return map[string]any{
		"version":     4,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	}
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
	r := newImagesRig(t, convergeRelease)
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
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, initProjectName+".yaml"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
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

	// The checkout stage's cloud step rebuilds the workflow images the records
	// still show on the previous release's base: billable builds, and here the
	// fake builder's, never a real submit.
	builder := useFakeBuilder(t)
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	putWorkflowRecordFrom("app", convergePrev, pinnedRef("base", convergePrev))
	putWorkflowRecordFrom("api", convergePrev, pinnedRef("base", convergePrev))

	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall(convergePrev))
	f.On(t, "plugins", userInstall(convergeRelease), argsUpdMarket...)
	f.On(t, "plugins", userInstall(convergeRelease), argsUpdUser...)
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
	// The node the previous release publishes: the version this binary writes
	// too, so the run moves the rules generation, the base image and the
	// plugin and leaves the node alone.
	t.Run("the node the previous release publishes", func(t *testing.T) {
		converge(t, v4Node(), []string{"policy-rules-skew", "plugin-pin"})
	})
	// The node one version back: a v3 node, which an installation that has not
	// republished since 0.13.0 carries. 0.14.0's operator notes removed that
	// node by hand and published again; the migration chain (a later release's)
	// does it inside `fugaro update` instead, so this is the fixture the update
	// door has to converge.
	t.Run("the node one version back", func(t *testing.T) {
		converge(t, v3Node(nil), []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})
	})
}

// converge runs the door once and checks the fixture converged. stale is the
// set of doctor checks the fixture has to be firing before the run, or the run
// below proves nothing: an installation that was current already would pass
// without the door doing anything.
func converge(t *testing.T, node map[string]any, stale []string) {
	t.Helper()
	r := newConvergeRig(t, node)
	convergeOn(t, r, stale)
	if got := upgradeRead(t, r.settings); !strings.Contains(got, `"ref": "v`+convergeRelease+`"`) {
		t.Errorf("the plugin pin is still not this release's:\n%s", got)
	}
	// The checkout's cloud step is the third step of its stage, and this run
	// onboarded the repository itself (it is the run the anchor writes
	// fugaro.yaml's gcp_project: line in, which leaves the working tree
	// different from the default branch): the job images are built here, from
	// the repository this run converged, and a skipped cloud step would be a
	// run refusing to build from a file it did onboard.
	if n := len(r.builder.specs); n != 2 {
		t.Errorf("%d job image(s) reached Cloud Build, want the checkout's two workflows: the job images of a repository this run onboarded are refreshed here", n)
	}
}

// convergeOn is converge over a rig the caller built and can go on reading:
// the fixture is asserted stale, `fugaro update` is run once, and doctor is
// clean afterwards. converge is this plus the two checks about the checkout, so
// a test that has to look at the database the run left behind — the rules it
// deployed, the markers it wrote — reaches it through here rather than building
// a rig of its own and then running the door against another one.
func convergeOn(t *testing.T, r *convergeRig, stale []string) {
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
	for _, id := range []string{"cli-version", "plugin-pin", "base-image", "policy-vs-jobs", "policy-older-than-binary", "policy-rules-skew"} {
		if c, ok := doctorCheckByID(after.Checks, id); ok && !c.OK {
			t.Errorf("after fugaro update, doctor's %s check still fires: %s (fix: %s)", id, c.Problem, c.Fix)
		}
	}
	for _, c := range after.Checks {
		if strings.HasPrefix(c.ID, "image-config-") && !c.OK {
			t.Errorf("after fugaro update, doctor's %s check still fires: %s", c.ID, c.Problem)
		}
	}
	if !strings.Contains(out, convergeRelease) {
		t.Errorf("the run never names the release it converged to:\n%s", out)
	}
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
	r := newConvergeRig(t, v4Node())
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
	r := newConvergeRig(t, v4Node())
	currentRelease(t)
	ahead := rules.OrchestratorRulesVersion + 1
	r.db.Set(rules.PathOrchestratorRulesMarker(), ahead)
	puts := r.db.RulePuts()

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil {
		t.Fatalf("fugaro update deployed this binary's rules over a newer orchestrator generation:\n%s", out)
	}
	if !strings.Contains(out+errOut, rules.PathOrchestratorRulesMarker()) {
		t.Errorf("the refusal does not name the marker it read (%s), so the owner cannot tell which generation refused:\n%s\n%s",
			rules.PathOrchestratorRulesMarker(), out, errOut)
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
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "[failed]"); ok {
			return strings.TrimSpace(rest)
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
