package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/imagecheck"
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
//   - the checkout still carries the pin the previous release wrote, and that
//     release's plugin is installed, so plugin-pin fires — the pin is a
//     leftover nothing in the tree moves any more (U2 of
//     docs/plugin-user-scope/implementation-plan.md, D949), which U5's
//     migration takes out — and the checkout stage has to move the
//     installation.
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
// version the bump to Version 5 landed on top of, so this release's update
// migrates it as well as moving the rules generation, the base image and the
// plugin. It is the node every installation holds while that update runs.
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
	// The node the previous release publishes: one version behind this
	// binary, so the run migrates it to Version 5 and moves the rules
	// generation, the base image and the plugin beside it.
	t.Run("the node the previous release publishes", func(t *testing.T) {
		converge(t, v4Node(), []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})
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
	out := convergeOn(t, r, stale)
	// Nothing in the tree writes a pin any more (U2, D949): the settings the
	// checkout carried come out of the run byte for byte as they went in, and
	// what the run moves is the plugin Claude Code has installed, at user
	// scope — the one the plugin stage is about now.
	if got := upgradeRead(t, r.settings); got != wiredAt("v"+convergePrev) {
		t.Errorf("the checkout's settings changed, and nothing writes a pin any more:\n%s", got)
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
	// omission: the pin this fixture carries is a leftover no run writes or
	// moves any more (U2, D949), so what this run must NOT do is report it
	// converged — a pass here would be the merge's write coming back. The
	// fixture is asserted stale above, with every other id, and still stale
	// here. U5's migration is what takes the entry out, inside this same
	// `fugaro update`, and this is the assertion it flips.
	if c, ok := doctorCheckByID(after.Checks, "plugin-pin"); !ok || c.OK {
		t.Errorf("after fugaro update, doctor's plugin-pin check reads %+v: the fixture still carries the previous release's pin and nothing in this tree moves one, so the check must still fire (U5's migration is what removes the entry)", c)
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
	r := newConvergeRig(t, v4Node())
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
	// node this installation carries names neither key.
	if p := convergeNode(t, r); p.ImageMap != nil || p.BakeRepos != nil {
		t.Fatalf("the fixture's node already carries image_map = %v and bake_repos = %v: it is not an installation whose policy predates both",
			p.ImageMap, p.BakeRepos)
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
	r := newConvergeRig(t, v4Node())
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
	r := newConvergeRigYAML(t, v4Node(), yaml)
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
	r := newConvergeRig(t, v4Node())
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
