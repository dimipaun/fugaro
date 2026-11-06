package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
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
	// runs is the bucket's root: where the project layer object and the
	// repositories' copies live, for a fixture that plants a published layer
	// of the release behind (bucket_url is file:// on the same directory).
	runs string
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

	root := repoCheckout(t, githubOrigin, refreshYAML)
	t.Chdir(root)
	settings := filepath.Join(root, ".claude", "settings.json")
	checkoutFile(t, settings, wiredAt("v"+convergePrev))
	checkoutFile(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"`+convergePrev+`"}]}}`)

	// The checkout stage's cloud step rebuilds the workflow images the records
	// still show on the previous release's base: billable builds, and here the
	// fake builder's, never a real submit.
	useFakeBuilder(t)
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
	return &convergeRig{imagesRig: r, root: root, settings: settings, runs: runs}
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

// plantLayer writes a project layer of the release behind into the rig: the
// published object, this machine's own copy of it, and the generation the
// config records the file as written from — the state an installation that has
// published (or seeded) a layer is in, one release ago.
func plantLayer(t *testing.T, r *convergeRig, text string) {
	t.Helper()
	layerRigFile(t, filepath.Join(r.runs, filepath.FromSlash(config.LayerKey)), text)
	path := localLayerPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(data, []byte("layer_generation: \"0\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateConvergesTheProjectLayerFromThePreviousRelease: the same door, on
// the object the previous release left stale in a way no reader survives. Its
// published project layer holds the keys the repository-image removal refuses,
// and every reader refuses that document by name (config.ParseProjectLayer,
// through findLayer and ResolveConfig), so every run of every repository of
// the project fails until the keys are gone — out of the published object, and
// out of this machine's copy of it, which is written from the same bytes.
//
// `fugaro update` alone is what has to do it: a rewrite of the object in place
// (never a delete and a publish again, which leaves the project with no layer
// at all in between), and doctor clean afterwards.
func TestUpdateConvergesTheProjectLayerFromThePreviousRelease(t *testing.T) {
	r := newConvergeRig(t, v4Node())
	currentRelease(t)
	// The rig hands the rest of this suite a mem:// record bucket whose bytes
	// are gone the moment it closes, and a layer published into one could not
	// be read back from it. The layer's own bucket is the file:// directory the
	// config names, so this run reads and writes that one where the layer is
	// concerned and leaves every other caller on the rig's fake.
	rug := openRecordBucket
	layerURL := "file://" + r.runs
	openRecordBucket = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if url == layerURL {
			return blobx.Open(ctx, url)
		}
		return rug(ctx, url)
	}
	t.Cleanup(func() { openRecordBucket = rug })
	plantLayer(t, r, previousLayer)

	anchor := config.LayerAnchor{Project: initProjectName, GCPProject: initProject}
	if _, ps := config.ParseProjectLayer([]byte(previousLayer), anchor); len(ps) == 0 {
		t.Fatal("the fixture's layer is one this Fugaro reads: there is nothing for the run below to converge")
	}
	objPath := filepath.Join(r.runs, filepath.FromSlash(config.LayerKey))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update stopped at %s (exit %d), so this installation needs a second command:\n%s\n%s",
			stoppedStage(out), ExitCode(err), out, errOut)
	}
	if !strings.Contains(out+errOut, "repository-image removal") {
		t.Errorf("the run does not say it took the removed keys out of the project layer:\n%s%s", out, errOut)
	}

	for _, side := range []struct{ what, path string }{
		{"the published project layer", objPath},
		{"the local layer file", localLayerPath(t)},
	} {
		data, rerr := os.ReadFile(side.path)
		if rerr != nil {
			t.Fatalf("%s is gone after fugaro update: %v", side.what, rerr)
		}
		if _, ps := config.ParseProjectLayer(data, anchor); len(ps) > 0 {
			t.Errorf("%s is still a layer every reader refuses:\n%v", side.what, ps)
		}
		if string(data) != migratedLayer {
			t.Errorf("%s is not the layer without the removed keys:\n%s", side.what, data)
		}
	}

	// The object was rewritten, not replaced from the file: the project is
	// never left with no published layer for a repository to resolve against.
	after := doctorChecks(t, r.root)
	for _, c := range after.Checks {
		if (strings.HasPrefix(c.ID, "project-layer") || c.ID == "local-layer-file") && c.Severity == "warning" {
			t.Errorf("after fugaro update, doctor's %s check still fires: %s (fix: %s)", c.ID, c.Problem, c.Fix)
		}
	}
}

// converge runs the door once and checks the fixture converged. stale is the
// set of doctor checks the fixture has to be firing before the run, or the run
// below proves nothing: an installation that was current already would pass
// without the door doing anything.
func converge(t *testing.T, node map[string]any, stale []string) {
	t.Helper()
	r := newConvergeRig(t, node)
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
	if got := upgradeRead(t, r.settings); !strings.Contains(got, `"ref": "v`+convergeRelease+`"`) {
		t.Errorf("the plugin pin is still not this release's:\n%s", got)
	}
	if !strings.Contains(out, convergeRelease) {
		t.Errorf("the run never names the release it converged to:\n%s", out)
	}
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
