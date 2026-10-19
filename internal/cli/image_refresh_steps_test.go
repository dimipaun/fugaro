package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/dimipaun/fugaro/internal/initflow"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// atTerminal points e's run at a fake terminal whose input is stdin, with
// its output in the returned buffer.
func atTerminal(t *testing.T, e *initEngine, stdin string) *syncBuf {
	t.Helper()
	fakeTerminal(t)
	out := &syncBuf{}
	e.r.w = out
	e.r.in = bufio.NewReader(strings.NewReader(stdin))
	e.r.cmd.SetIn(strings.NewReader(stdin))
	return out
}

// atNoTerminal is atTerminal without faking a terminal: stdin stays a
// strings.Reader, which stdinIsTerminal (unpatched) reports as not one, as a
// closed pipe in CI would be. Used to prove --yes needs none (2026-10-08
// decision).
func atNoTerminal(e *initEngine) *syncBuf {
	out := &syncBuf{}
	e.r.w = out
	e.r.in = bufio.NewReader(strings.NewReader(""))
	e.r.cmd.SetIn(strings.NewReader(""))
	return out
}

// Step 2 pins only the kinds it is given, after its own confirmation (the
// reference and its digest shown), and a rerun changes nothing. Nothing is
// copied and no step here touches Terraform.
func TestRefreshBaseStep(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML)) // names base
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"base"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if r.dst.puts != 0 {
		t.Fatalf("the base kind was copied into the registry: %d write(s)", r.dst.puts)
	}
	if r.resolveCalls == 0 || r.arCalls == 0 {
		t.Fatalf("the pin was recorded without resolving and reading it back (%d resolve(s), %d read-back(s))", r.resolveCalls, r.arCalls)
	}
	if got, want := r.localConfig(t).BaseImages["base"], r.pinnedBase("base", "1.2.3"); got != want {
		t.Fatalf("base_images.base = %s, want %s", got, want)
	}
	if got := r.localConfig(t).ImageSource; got != "ghcr.io/dimipaun" {
		t.Errorf("image_source = %q, want the recorded owner ghcr.io/dimipaun", got)
	}
	for _, want := range []string{"⚠ CONFIRM", "sha256:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q (the confirmation or the digest):\n%s", want, out.String())
		}
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the base step ran terraform: %q", calls)
	}
	resolves := r.resolveCalls
	out = atTerminal(t, e, "")
	if err := e.r.refreshBase(t.Context(), e, []string{"base"}); err != nil || r.resolveCalls != resolves || !strings.Contains(out.String(), "No changes") {
		t.Fatalf("rerun: %v, %d resolve(s)\n%s", err, r.resolveCalls-resolves, out.String())
	}
}

// 2026-10-08 decision: --yes confirms the base copy without a terminal (a
// closed stdin, as CI would give it), with its own "confirmed by --yes"
// line; the digest is still shown first.
func TestRefreshBaseStepYesNoTerminal(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	e := rigEngine(t, r.initRig, &initOptions{yes: true})
	out := atNoTerminal(e)
	if err := e.r.refreshBase(t.Context(), e, []string{"base"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got, want := r.localConfig(t).BaseImages["base"], r.pinnedBase("base", "1.2.3"); got != want {
		t.Fatalf("base_images.base = %s, want %s", got, want)
	}
	for _, want := range []string{"⚠ CONFIRM", "sha256:", "confirmed by --yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// A declined base copy stops the step: nothing is copied or recorded.
func TestRefreshBaseStepDeclined(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, "nope\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"base"}); err == nil || r.dst.puts != 0 {
		t.Fatalf("declined: %v, %d puts\n%s", err, r.dst.puts, out.String())
	}
	if got := r.localConfig(t).BaseImages["base"]; got != "" {
		t.Fatalf("base_images.base was recorded despite the decline: %s", got)
	}
}

// The base step never replaces a custom or hand-pushed base image, even
// called directly with that kind: the images stage's own rule (D4) leaves it
// alone, the same guard fugaro init --base has.
func TestRefreshBaseStepNeverReplacesCustomBase(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	r.appendConfig(t, "base_images: {base: ghcr.io/me/fugaro-base:mine}\n")
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"base"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if r.dst.puts != 0 {
		t.Fatalf("a custom base was copied: %d puts", r.dst.puts)
	}
	if got := r.localConfig(t).BaseImages["base"]; got != "ghcr.io/me/fugaro-base:mine" {
		t.Fatalf("base_images.base was changed to %q", got)
	}
	if !strings.Contains(out.String(), "nothing mirrored") {
		t.Errorf("output does not explain the skip:\n%s", out.String())
	}
}

// implicitBaseRefreshYAML is refreshYAML's checkout with one workflow that
// has no base: (the implicit Fugaro base).
const implicitBaseRefreshYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// Today's installations have no signed "-pinned" image published yet (the
// release side that publishes one is #318's, and it is not shipped): fugaro
// image refresh's base step (step 2) on a checkout whose workflow has no
// base: must refuse with a clear error naming the missing tag, write nothing
// to the local config or the registry, and never reach the builds step:
// runImageRefresh returns at refreshStopped(2, ...) when step 2 fails,
// before s.builds (step 4) is ever called, so the fake builder sees no
// submission.
//
// Mutation (run, restore): publish the "-pinned" tag on the fake source
// registry (letting the resolve-and-verify step proceed, as it would once
// such a release exists), and this test's assertions fail: the base step
// succeeds and base_images.base is pinned.
func TestRefreshBaseStepRefusesWhenFugaroBaseIsNotPublished(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	delete(r.src.tags, "dimipaun/fugaro-base:1.2.3-pinned")
	t.Chdir(repoCheckout(t, githubOrigin, implicitBaseRefreshYAML))
	fb := useFakeBuilder(t)
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	err := e.r.refreshBase(t.Context(), e, []string{"base"})
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out.String())
	}
	if !strings.Contains(err.Error(), "fugaro-base") || !strings.Contains(err.Error(), "-pinned") {
		t.Fatalf("err = %v, want it to name the missing image and the -pinned tag it looked for", err)
	}
	if r.dst.puts != 0 {
		t.Errorf("the registry was written to: %d puts", r.dst.puts)
	}
	if got := r.localConfig(t).BaseImages["base"]; got != "" {
		t.Errorf("base_images.base was recorded: %s", got)
	}
	if len(fb.specs) != 0 {
		t.Errorf("a build was submitted: %+v", fb.specs)
	}
}

// refreshReload re-reads the local config the base step wrote (not the
// plan's own, now-stale copy) and re-resolves the checkout's config
// strictly against it (decision L16: this leads to a billable Cloud Build
// submission, so it must not reuse preflight's own lenient, flag-blind
// read), and the check job's image is the re-read base.
func TestRefreshReload(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.5.1"))
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.lc.BaseImages) != 0 {
		t.Fatalf("fixture: preflight's own lc already has base_images: %v", p.lc.BaseImages)
	}
	// Simulate step 2 having since recorded the kind: refreshReload must
	// pick this up from the file, not from p.lc.
	r.appendConfig(t, "base_images: {base: "+managedRef("base", "0.5.1")+"}\n")

	tg, err := refreshReload(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.lc.BaseImages) != 0 {
		t.Errorf("refreshReload mutated the plan's own (stale) lc: %v", p.lc.BaseImages)
	}
	if tg.lc.BaseImages["base"] != managedRef("base", "0.5.1") {
		t.Fatalf("lc.BaseImages = %v, want the kind the base step just recorded", tg.lc.BaseImages)
	}
	// Re-resolved (a fresh object, strictly, against tg.lc), not the same
	// pointer as preflight's own lenient p.cfg; it must still resolve to
	// the same bytes, since nothing about the checkout or its layer changed
	// between the two reads.
	if tg.cfg == p.cfg || tg.cfg.SHA256() != p.cfg.SHA256() {
		t.Errorf("tg.cfg SHA256 = %s, p.cfg SHA256 = %s (same pointer: %v)", tg.cfg.SHA256(), p.cfg.SHA256(), tg.cfg == p.cfg)
	}
	if tg.spec.Name != "acme/app" || tg.spec.Slug != p.slug || tg.spec.RegistryPath == "" || tg.spec.BuildServiceAccountEmail == "" {
		t.Fatalf("spec %+v", tg.spec)
	}
	if tg.spec.Check == nil || tg.spec.Check.Image != managedRef("base", "0.5.1") {
		t.Fatalf("check image = %+v, want %s", tg.spec.Check, managedRef("base", "0.5.1"))
	}
}

// A workflow with no base: (the implicit Fugaro base) falls back to this
// release's own fugaro-base image, not a malformed ref under the
// empty-string key.
//
// Mutation (run, restore): change refreshReload's specLC.BaseImages[kind]
// lookups back to specLC.BaseImages[w.Base], and this test fails: the
// fallback is written under base_images[""] (ghcr.io/.../fugaro-:0.5.1)
// instead of base_images.base, so infra.Repo refuses the checked workflow
// with "no base_images entry for base".
func TestRefreshReloadOnTheBaseKind(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
		"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	r := newAnchorModeRig(t, yaml, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.5.1"))
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tg, err := refreshReload(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if tg.lc.BaseImages["base"] != "" {
		t.Fatalf("refreshReload wrote base_images.base to the local config: %v", tg.lc.BaseImages)
	}
	want, err := image.BaseRef("0.5.1")
	if err != nil {
		t.Fatal(err)
	}
	if tg.spec.Check == nil || tg.spec.Check.Image != want {
		t.Fatalf("check image = %+v, want the release fallback %s", tg.spec.Check, want)
	}
}

// anchoredRefreshYAML is acme/other, anchored to aurora/proj-1234, with one
// workflow named config.ImplicitWorkflow (matching layerCheckout's
// pre-added Cloud Run job and registerOtherLocally's repos: entry) that
// needs nothing from the project layer to validate: refreshPreflight's own
// (lenient, flag-blind) read may come back Unknown without failing the
// fixture, so these tests can isolate refreshReload's own, separately
// strict read.
var anchoredRefreshYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  " + config.ImplicitWorkflow + ": { commands: { build: sh build.sh, test: sh test.sh } }\n"

// Review fix (1), security (decision L16): fugaro image refresh's rebuild is
// a billable Cloud Build submission, never lenient like refreshPreflight's
// earlier, flag-blind read (loadCheckoutConfigAt, lc==nil). A bucket that
// cannot be read, with nothing cached, must refuse before refreshReload ever
// hands a config to refreshBuilds, so submitAndWait (and Submit) are never
// reached — the same contract fugaro run's own TestRunRefusesWhenTheLayerBucketIsUnreadable
// pins for launches.
//
// Mutation (run, restore): change refreshReload's `cfg := rf.Cfg` back to
// `cfg := p.cfg`, and this test fails: p.cfg comes from the earlier lenient
// read, which (lc==nil forces offline) never even attempts the live read
// this test breaks, and comes back Unknown (no layer, no error) instead of
// refusing.
func TestRefreshReloadRefusesWhenTheLayerBucketIsUnreadable(t *testing.T) {
	useVersion(t, "0.6.0")
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, anchoredRefreshYAML)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: ["+config.ImplicitWorkflow+"], github_app_id: \"12345\" }\n")
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// refreshPreflight's own read just succeeded, which caches the layer as
	// a side effect (findLayer, on every successful read): a fresh, empty
	// cache directory, so the "unreachable, but a cache entry can stand in"
	// fallback (decision L13) does not mask the refusal this test pins.
	isolateCache(t)
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	// refreshReload itself refuses, so refreshBuilds (and submitAndWait,
	// and Submit) is never even reached: there is nothing here to build
	// against yet, which is the whole point — a build is never submitted
	// over an unknown or stale layer.
	if _, err := refreshReload(t.Context(), p); err == nil {
		t.Fatal("reloaded despite an unreadable project layer bucket with nothing cached")
	}
}

// Review fix (1): the earlier, flag-blind p.cfg (loadCheckoutConfigAt,
// lc==nil) never even attempts a live bucket read: with lc==nil forcing
// offline mode (decision L16's Lenient path), it is satisfied by whatever
// is cached, however stale, as soon as findLayer sees one for the right
// project and bucket name. refreshReload's own re-resolution, against the
// real lc, must do a live read instead, even though a cache entry (seeded
// here directly, standing in for one left over from an earlier command)
// would otherwise satisfy it.
//
// Mutation (run, restore): change refreshReload's `cfg := rf.Cfg` back to
// `cfg := p.cfg`, and this test fails: the resolved layer's sha256 becomes
// the stale cached one, not the live bucket's current text.
func TestRefreshReloadIgnoresAStaleCachedLayer(t *testing.T) {
	useVersion(t, "0.6.0")
	f := newCloudFixture(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, anchoredRefreshYAML)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: ["+config.ImplicitWorkflow+"], github_app_id: \"12345\" }\n")
	cfgPath := os.Getenv("FUGARO_CONFIG")
	// A clean slate: isolateProjects clears $FUGARO_PROJECT/$FUGARO_CONFIG
	// and points XDG (config and cache) at empty directories, so
	// loadCheckoutConfigAt's own flag-blind fallback (selectedProjectConfig,
	// which refreshPreflight's first, lenient read uses, never iopts.cloud)
	// has nothing to find by name, $FUGARO_*, or "exactly one project
	// config" — only --config below (iopts.cloud, which loadRepoConfig
	// does thread through) names the installation at all. Without this,
	// selectedProjectConfig would find the same config loadRepoConfig does
	// (both discoverable via $FUGARO_CONFIG alone), and the bug this test
	// pins (lc==nil forcing cache-only mode) would never trigger.
	isolateProjects(t, t.TempDir())
	stale := strings.Replace(testProjectLayer, "sh build.sh", "sh stale-build.sh", 1)
	if stale == testProjectLayer {
		t.Fatal("fixture: testProjectLayer has no \"sh build.sh\" to make a distinct stale copy of")
	}
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", Generation: 1, CheckedAt: time.Now(), YAML: stale,
	}); err != nil {
		t.Fatal(err)
	}
	// The bucket itself still holds testProjectLayer (published above),
	// perfectly reachable: only the cache is stale.
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{cloud: cloudOptions{config: cfgPath}})
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.Layer == nil || p.cfg.Layer.SHA256 != config.LayerSum([]byte(stale)) {
		t.Fatalf("fixture: refreshPreflight's own lenient read did not pick up the stale cache (layer %v), so this test would not catch a regression", p.cfg.Layer)
	}
	tg, err := refreshReload(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if want := config.LayerSum([]byte(testProjectLayer)); tg.cfg.Layer == nil || tg.cfg.Layer.SHA256 != want {
		t.Fatalf("resolved layer sha = %v, want the live bucket's %s (not the stale cache)", tg.cfg.Layer, want)
	}
}

// checkJobOwner is the label a repository's check job carries, and the
// RepoSpec fields refreshCheckJob needs to find and own it.
func checkJobOwner(t *testing.T, provider, repo string) (slug, label string) {
	t.Helper()
	slug = mustSlug(provider, repo)
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	return slug, label
}

// Step 3 moves the image and the spec's base together, after the typed
// name, and says No changes on a rerun. A decline leaves the job untouched.
func TestRefreshCheckJobStep(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"base": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec), "FUGARO_PROJECT": "aurora"})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": newRef}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"base"}}
	// Declined: nothing written.
	out := atTerminal(t, e, "nope\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err == nil || len(r.run.Patches()) != 0 {
		t.Fatalf("declined: %v, %d patches\n%s", err, len(r.run.Patches()), out.String())
	}
	out = atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"image " + oldRef + " -> " + newRef, "⚠ CONFIRM", "updated the daily image check job"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(r.run.Patches()) != 1 {
		t.Fatalf("%d patches", len(r.run.Patches()))
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the check-job step ran terraform: %q", calls)
	}
	out = atTerminal(t, e, "")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil || len(r.run.Patches()) != 1 || !strings.Contains(out.String(), "No changes") {
		t.Fatalf("rerun: %v\n%s", err, out.String())
	}
}

// 2026-10-08 decision: --yes confirms the check-job update without a
// terminal, after showing the same old/new diff as a typed run, with its own
// "confirmed by --yes" line.
func TestRefreshCheckJobStepYesNoTerminal(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{yes: true})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"base": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": newRef}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"base"}}
	out := atNoTerminal(e)
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"image " + oldRef + " -> " + newRef, "⚠ CONFIRM", "confirmed by --yes", "updated the daily image check job"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(r.run.Patches()) != 1 {
		t.Fatalf("%d patches", len(r.run.Patches()))
	}
}

// PlanCheckJob's own owner check (labels, spec repo) refuses a job that is
// not this repository's: ErrCheckJobShape surfaces as a user error, and
// nothing is patched. Run with and without --yes (2026-10-08 decision): the
// owner check runs before any confirmation, so --yes changes nothing here.
func testRefreshCheckJobForeignJob(t *testing.T, yes bool) {
	t.Helper()
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{yes: yes})
	slug, _ := checkJobOwner(t, "bitbucket", "acme/sandbox")
	_, otherLabel := checkJobOwner(t, "bitbucket", "acme/other")
	oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"base": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: otherLabel}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": newRef}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"base"}}
	if yes {
		atNoTerminal(e)
	} else {
		atTerminal(t, e, initProjectName+"\n")
	}
	err = e.r.refreshCheckJob(t.Context(), tg)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not as fugaro init --repo made it") || len(r.run.Patches()) != 0 {
		t.Fatalf("yes=%v: exit %d, err %v, %d patches", yes, ExitCode(err), err, len(r.run.Patches()))
	}
}

func TestRefreshCheckJobRefusesForeignJob(t *testing.T) { testRefreshCheckJobForeignJob(t, false) }

func TestRefreshCheckJobRefusesForeignJobEvenWithYes(t *testing.T) {
	testRefreshCheckJobForeignJob(t, true)
}

// A failed job read (a 403, say) is a remote error: never treated as "no
// job" (which would skip step 3 silently) or as already current.
func TestRefreshCheckJobReadErrorIsRemote(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, managedRef("base", "0.4.0"))
	r.run.SetJobFaults(gcpfake.JobFaults{GetStatus: http.StatusForbidden})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": managedRef("base", "0.5.1")}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"base"}}
	atTerminal(t, e, initProjectName+"\n")
	err = e.r.refreshCheckJob(t.Context(), tg)
	if ExitCode(err) != ExitRemoteError || len(r.run.Patches()) != 0 {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// Hostile bytes in the job's own image (control or bidi characters) reach
// the terminal escaped, through Printable, like every other cloud-derived
// text fugaro prints.
func TestRefreshCheckJobEscapesHostileBytes(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	oldRef := managedRef("base", "0.4.0") + "\u202e-evil"
	newRef := managedRef("base", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"base": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": newRef}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"base"}}
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "\u202e") || !strings.Contains(out.String(), `\u202e`) {
		t.Errorf("hostile bytes reached the terminal unescaped:\n%q", out.String())
	}
}

// buildTarget is the refreshTarget and plan TestRefreshBuilds* share: acme/app
// with its two workflows, both base kinds already at 0.5.1.
func buildTarget(t *testing.T, r *anchorModeRig) (*initEngine, *refreshTarget) {
	t.Helper()
	return buildTargetOpts(t, r, &initOptions{})
}

// buildTargetOpts is buildTarget with the caller's own options (o.yes, for
// the --yes tests).
func buildTargetOpts(t *testing.T, r *anchorModeRig, o *initOptions) (*initEngine, *refreshTarget) {
	t.Helper()
	e := rigEngine(t, r.initRig, o)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": managedRef("base", "0.5.1")}
	cfg, problems := config.Parse([]byte(refreshYAML))
	if cfg == nil {
		t.Fatal(problems)
	}
	slug := mustSlug("github", "acme/app")
	tg := &refreshTarget{lc: lc, cfg: cfg, spec: infra.RepoSpec{Name: "acme/app", Slug: slug, RegistryPath: "r", BuildServiceAccountEmail: "b@x.iam",
		Workflows: map[string]infra.WorkflowSpec{"app": {}, "api": {}}}}
	return e, tg
}

// Step 4 rebuilds the stale workflow only, from the base the base step
// recorded, after the typed name; a declined build stops it. No step here
// touches Terraform.
func TestRefreshBuildsStartFromTheNewBase(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"api", "app"}}
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.5.1"))
	putWorkflowRecordFrom("api", "0.4.0", managedRef("base", "0.4.0"))
	atTerminal(t, e, "nope\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err == nil || !strings.Contains(err.Error(), "not confirmed") || len(fb.specs) != 0 {
		t.Fatalf("declined: %v, %d builds", err, len(fb.specs))
	}
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "api" || fb.specs[0].Base != managedRef("base", "0.5.1") {
		t.Fatalf("builds %+v", fb.specs)
	}
	if !strings.Contains(out.String(), "app: current: built from "+managedRef("base", "0.5.1")) {
		t.Errorf("output:\n%s", out.String())
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the builds step ran terraform: %q", calls)
	}
}

// The never-downgrade rule compares a build record against the TARGET's
// version, not against the running CLI's (review finding, medium). Here the
// local config already pins 0.9.0 while this CLI is 0.7.5, and app's record
// says 0.8.0 — OLDER than the pin the check job runs from. Comparing
// against the CLI's own 0.7.5 kept it and the step then printed "No
// changes: every selected image is built from its current base" over an
// image the check job has moved past. api's record is at the pin, so it is
// the control: it is still kept, and only app is built.
//
// Mutation (run, restore): compare against ver instead of the target in
// refreshVerdict, and this test fails: no build is submitted and the output
// carries the "No changes" line.
func TestRefreshBuildsRebuildsAnImageOlderThanTheConfigsNewerPin(t *testing.T) {
	useVersion(t, "0.7.5")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	pin := pinnedRef("base", "0.9.0")
	tg.lc.BaseImages = map[string]string{"base": pin}
	tg.lc.ImageSource = "ghcr.io/" + refreshOwner
	p := &refreshPlan{workflows: []string{"app", "api"}, ver: "0.7.5"}
	putWorkflowRecordFrom("app", "0.7.5", managedRef("base", "0.8.0"))
	putWorkflowRecordFrom("api", "0.7.5", pin)
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" || fb.specs[0].Base != pin {
		t.Fatalf("builds %+v, want app alone from %s", fb.specs, pin)
	}
	if !strings.Contains(out.String(), "app: built from "+managedRef("base", "0.8.0")+": rebuilt from "+pin) {
		t.Errorf("output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "api: current: built from "+pin) {
		t.Errorf("api is at the pin and must be left alone:\n%s", out.String())
	}
	if strings.Contains(out.String(), "No changes") {
		t.Errorf("printed \"No changes\" with an image older than the pin:\n%s", out.String())
	}
}

// The other direction, so the fix cannot pass by rebuilding everything: with
// every record at or above the config's newer pin, the step still reports
// "No changes" and submits nothing.
func TestRefreshBuildsSaysNoChangesWhenEveryImageIsAtTheNewerPin(t *testing.T) {
	useVersion(t, "0.7.5")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	pin := pinnedRef("base", "0.9.0")
	tg.lc.BaseImages = map[string]string{"base": pin}
	// The owner the pin is checked against is the local config's recorded
	// image_source (design §3.6); this rig's config has none until the base
	// step writes it, so the test records it as that step would.
	tg.lc.ImageSource = "ghcr.io/" + refreshOwner
	p := &refreshPlan{workflows: []string{"app", "api"}, ver: "0.7.5"}
	putWorkflowRecordFrom("app", "0.7.5", pin)
	putWorkflowRecordFrom("api", "0.7.5", managedRef("base", "0.10.0"))
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 0 {
		t.Fatalf("builds %+v, want none", fb.specs)
	}
	if !strings.Contains(out.String(), "No changes: every selected image is built from its current base") {
		t.Errorf("output:\n%s", out.String())
	}
}

// A workflow with no base: (the implicit Fugaro base) is rebuilt from
// base_images.base, not from a malformed ref for kind "".
//
// Mutation (run, restore): change refreshBuilds' `kind :=
// t.cfg.Workflows[wf].BaseKind()` back to `.Base`, and this test fails: tg.lc
// has no base_images[""] entry, so refreshBuilds refuses with "no base image
// for kind " instead of submitting the build.
func TestRefreshBuildsOnTheBaseKind(t *testing.T) {
	useVersion(t, "0.5.1")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
		"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	r := newAnchorModeRig(t, yaml, true)
	fb := useFakeBuilder(t)
	e := rigEngine(t, r.initRig, &initOptions{})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := managedRef("base", "0.5.1")
	lc.BaseImages = map[string]string{"base": base}
	cfg, problems := config.Parse([]byte(yaml))
	if cfg == nil {
		t.Fatal(problems)
	}
	slug := mustSlug("github", "acme/app")
	tg := &refreshTarget{lc: lc, cfg: cfg, spec: infra.RepoSpec{Name: "acme/app", Slug: slug, RegistryPath: "r", BuildServiceAccountEmail: "b@x.iam",
		Workflows: map[string]infra.WorkflowSpec{"app": {}}}}
	p := &refreshPlan{workflows: []string{"app"}}
	atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v", err)
	}
	if len(fb.specs) != 1 || fb.specs[0].Base != base {
		t.Fatalf("builds %+v, want one from %s", fb.specs, base)
	}
}

// A decline on the first of several stale workflows stops the loop: the
// builds after it are never submitted (D9).
func TestRefreshBuildsDeclineStopsSubsequentBuilds(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"api", "app"}}
	// Neither has a build record: both are todo.
	out := atTerminal(t, e, "nope\n")
	err := e.r.refreshBuilds(t.Context(), p, tg)
	if err == nil || !strings.Contains(err.Error(), "api") || len(fb.specs) != 0 {
		t.Fatalf("%v, %d builds\n%s", err, len(fb.specs), out.String())
	}
}

// TestRefreshMovesCloneJob (design generic-tool.md §2; the enabled state of
// the 0.7.0 safety fix round 2's gate). A checkout: clone workflow has no
// derived image to rebuild: its job's container image moves to the local
// config's base_images.base in place, through the Cloud Run Admin API,
// after its own confirmation, and no Cloud Build is ever submitted for it.
// The job has to be a clone job for that to be the right move at all —
// infra.PlanWorkflowJob refuses one whose container does not carry
// FUGARO_CHECKOUT=clone, since moving such a job's image would leave it
// expecting a baked checkout — so the test deploys it the way `fugaro init
// --repo` does, env included.
//
// Mutation (run, restore): set cloneShipped back to false in recipes_skew.go
// — refreshCloneJobs then refuses before any cloud call and this test fails:
// no confirmation is offered, 0 patches, no "updated app's job", and the
// error carries the not-released-yet wording instead.
func TestRefreshMovesCloneJob(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	fb := useFakeBuilder(t)
	slug, label := checkJobOwner(t, "github", "acme/app")
	oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
	jobName := gcp.JobName(slug, "app")
	r.run.SetJob(jobName, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "app"}, oldRef)
	r.run.SetJobEnv(jobName, map[string]string{infra.CheckoutEnv: config.CheckoutClone})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{config.BaseKind: newRef}
	cfg, problems := config.Parse([]byte(cloneOnlyYAML))
	if cfg == nil {
		t.Fatal(problems)
	}
	p := &refreshPlan{workflows: []string{"app"}}
	tg := &refreshTarget{lc: lc, cfg: cfg, spec: infra.RepoSpec{Name: "acme/app", Slug: slug, Label: label,
		Workflows: map[string]infra.WorkflowSpec{"app": {Job: jobName}}}}
	// Declined: nothing written.
	out := atTerminal(t, e, "nope\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err == nil || len(r.run.Patches()) != 0 {
		t.Fatalf("declined: %v, %d patches\n%s", err, len(r.run.Patches()), out.String())
	}
	out = atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"app: image " + oldRef + " -> " + newRef, "⚠ CONFIRM", "updates the checkout: clone workflow app's job", "updated app's job"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(r.run.Patches()) != 1 {
		t.Fatalf("%d patches, want 1 (the clone job's image moved)", len(r.run.Patches()))
	}
	if len(fb.specs) != 0 {
		t.Fatalf("a Cloud Build was submitted for a checkout: clone workflow: %+v", fb.specs)
	}
	// The move is idempotent: a second run finds the job already at the base.
	out = atTerminal(t, e, "")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil || len(r.run.Patches()) != 1 || !strings.Contains(out.String(), "No changes: the job already runs from") {
		t.Fatalf("rerun: %v, %d patches\n%s", err, len(r.run.Patches()), out.String())
	}
}

// TestRefreshMovesEveryCloneVariantJob is TestRefreshMovesCloneJob with a
// named Claude account, so one checkout: clone workflow has a job per
// account (gcp.WorkflowVariant): both are clone jobs and both are moved.
//
// Mutation (run, restore): same as TestRefreshMovesCloneJob, plus dropping
// either job from the tg.spec.Workflows map — the loop over the spec's job
// keys is what finds the per-account jobs, so one dropped is one not moved.
func TestRefreshMovesEveryCloneVariantJob(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{yes: true})
	fb := useFakeBuilder(t)
	slug, label := checkJobOwner(t, "github", "acme/app")
	oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
	variant := gcp.WorkflowVariant("app", "acme-personal")
	if variant == "app" {
		t.Fatal("the named account's variant key is the default's")
	}
	defJob, varJob := gcp.JobName(slug, "app"), gcp.JobName(slug, variant)
	r.run.SetJob(defJob, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "app"}, oldRef)
	r.run.SetJobEnv(defJob, map[string]string{infra.CheckoutEnv: config.CheckoutClone})
	r.run.SetJob(varJob, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: variant}, oldRef)
	r.run.SetJobEnv(varJob, map[string]string{infra.CheckoutEnv: config.CheckoutClone})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{config.BaseKind: newRef}
	cfg, problems := config.Parse([]byte(cloneOnlyYAML))
	if cfg == nil {
		t.Fatal(problems)
	}
	p := &refreshPlan{workflows: []string{"app"}}
	tg := &refreshTarget{lc: lc, cfg: cfg, spec: infra.RepoSpec{Name: "acme/app", Slug: slug, Label: label,
		Workflows: map[string]infra.WorkflowSpec{"app": {Name: "app", Job: defJob}, variant: {Name: "app", Job: varJob}}}}
	out := atTerminal(t, e, "")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(r.run.Patches()) != 2 {
		t.Fatalf("%d patches, want 2 (both of the clone workflow's jobs)\n%s", len(r.run.Patches()), out.String())
	}
	if len(fb.specs) != 0 {
		t.Fatalf("a Cloud Build was submitted for a checkout: clone workflow: %+v", fb.specs)
	}
}

// cloneOnlyYAML is acme/app with one checkout: clone workflow (app).
const cloneOnlyYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"

// mixedCloneYAML is acme/app with one checkout: clone workflow (app) and
// one baked workflow (api, on the base kind).
const mixedCloneYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// TestRefreshCloneMoveDoesNotBlockBakedRebuilds (review finding 3, LOW; the
// property survives the cloneShipped flip unchanged — it was written against
// the clone side refusing, and the two sides are independent either way):
// moving a clone workflow's job image and rebuilding a baked workflow's are
// independent per-workflow actions, bundled into step 4 only because neither
// touches Terraform. A mixed repository gets both, in one run, in that
// order: app the clone job moved (no Cloud Build), api the baked image
// rebuilt (one Cloud Build).
//
// Mutation (run, restore): revert refreshBuilds to `if err :=
// r.refreshCloneJobs(ctx, p, t); err != nil { return err }` (dropping the
// cloneErr capture) — this test then fails: the baked workflow's rebuild is
// never even offered (0 Cloud Builds submitted), because the clone side's
// error returns before the baked loop is ever reached. (Flip cloneShipped
// back to false and it fails the same way, which is why the property is
// worth pinning in both directions.)
func TestRefreshCloneMoveDoesNotBlockBakedRebuilds(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, mixedCloneYAML, true)
	fb := useFakeBuilder(t)
	e := rigEngine(t, r.initRig, &initOptions{})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{config.BaseKind: managedRef("base", "0.5.1")}
	cfg, problems := config.Parse([]byte(mixedCloneYAML))
	if cfg == nil {
		t.Fatal(problems)
	}
	slug := mustSlug("github", "acme/app")
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	jobName := gcp.JobName(slug, "app")
	r.run.SetJob(jobName, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "app"}, managedRef("base", "0.4.0"))
	r.run.SetJobEnv(jobName, map[string]string{infra.CheckoutEnv: config.CheckoutClone})
	tg := &refreshTarget{lc: lc, cfg: cfg, spec: infra.RepoSpec{Name: "acme/app", Slug: slug, Label: label, RegistryPath: "r", BuildServiceAccountEmail: "b@x.iam",
		Workflows: map[string]infra.WorkflowSpec{"app": {Job: jobName}, "api": {}}}}
	p := &refreshPlan{workflows: []string{"api", "app"}}
	putWorkflowRecordFrom("api", "0.4.0", managedRef("base", "0.4.0"))
	// Both sides ask: the clone move for the project's name, the baked
	// rebuild for it too.
	out := atTerminal(t, e, initProjectName+"\n"+initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "api" {
		t.Fatalf("the baked rebuild was not offered: %+v", fb.specs)
	}
	if len(r.run.Patches()) != 1 {
		t.Fatalf("%d patches, want 1 (the clone job's image moved)\n%s", len(r.run.Patches()), out.String())
	}
	if !strings.Contains(out.String(), "updated app's job") || !strings.Contains(out.String(), "built acme/app/api") {
		t.Errorf("output does not report both halves:\n%s", out.String())
	}
}

// Review fix (code review on the prior fix): submitAndWait's project-layer
// copy write now opens the record bucket through openRecordBucket (the
// test seam every other record-bucket caller uses), not blobx.Open
// directly, which could not be exercised successfully in a test at all
// (lc.RecordBucketURL() is always a real gs:// URL, and blobx has no
// endpoint-override mechanism). This is the happy path the previous test
// suite never covered for fugaro image refresh's rebuilds: a project
// layer, a reachable (fake, file://-backed) record bucket, and the copy
// actually landing at builds/<slug>/project-layer.yaml with the sha the
// build was submitted with.
//
// Mutation (run, restore): change submitAndWait's openRecordBucket(ctx,
// lc.RecordBucketURL()) call back to blobx.Open(ctx, lc.RecordBucketURL()),
// and this test fails: blobx.Open ignores the openRecordBucket override
// below entirely and tries to open the real gs:// URL, which is not
// panic-guarded the way layerBucketOpener is (TestMain only guards that
// one), so it attempts a real network connection and the build fails with
// a connection/credentials error instead of succeeding.
func TestRefreshBuildsWritesTheProjectLayerCopy(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTargetOpts(t, r, &initOptions{yes: true})
	l, ps := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	tg.cfg.Layer = l
	// A layer needs a base whose fugaro reads it (layeredSince, 0.6.0):
	// buildTargetOpts' own 0.5.1 base would otherwise be refused before the
	// copy write this test means to exercise.
	tg.lc.BaseImages["base"] = managedRef("base", "0.6.0")
	recordsDir := t.TempDir()
	prevRB := openRecordBucket
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+recordsDir)
	}
	t.Cleanup(func() { openRecordBucket = prevRB })
	p := &refreshPlan{workflows: []string{"app"}}
	out := atNoTerminal(e)
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].ProjectLayerSHA256 != l.SHA256 {
		t.Fatalf("specs = %+v, want one with ProjectLayerSHA256 %s", fb.specs, l.SHA256)
	}
	got, err := os.ReadFile(filepath.Join(recordsDir, "builds", tg.spec.Slug, "project-layer.yaml"))
	if err != nil || string(got) != testProjectLayer {
		t.Fatalf("the copy = %q, %v; want testProjectLayer", got, err)
	}
}

// A build record read that fails in the cloud stops the run at exit 2,
// before any build is submitted (D8's own rule: an unreadable cloud read is
// never "no record").
func TestRefreshBuildsRecordReadErrorStopsBuilds(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"api", "app"}}
	prev := openRecordBucket
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { openRecordBucket = prev })
	err := e.r.refreshBuilds(t.Context(), p, tg)
	if ExitCode(err) != ExitRemoteError || len(fb.specs) != 0 {
		t.Fatalf("exit %d, err %v, %d builds", ExitCode(err), err, len(fb.specs))
	}
}

// The build confirmation is askBuild's (the project's name typed, or --yes:
// 2026-10-08 decision), never the run's own confirmation or a plain,
// un-gated ask.
func TestRefreshBuildsConfirmationIsTypedOrYes(t *testing.T) {
	src, err := os.ReadFile("image_refresh_steps.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (r *initRun) refreshBuilds(")
	if i < 0 {
		t.Fatal("refreshBuilds not found")
	}
	body = body[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	if !strings.Contains(body, "r.askBuild(") {
		t.Error("refreshBuilds does not ask through askBuild")
	}
	for _, bad := range []string{"confirmOrdinary", "askOrdinary", "runCovers", "r.confirm(", "r.ask(", "r.askTyped("} {
		if strings.Contains(body, bad) {
			t.Errorf("refreshBuilds uses %s directly (should go through askBuild)", bad)
		}
	}
}

// askBuild itself: without --yes it is exactly askTyped (a real terminal,
// never auto-confirmed); with --yes it confirms without one, printing its
// own "confirmed by --yes" line, same as the ordinary steps' r.ask.
func TestAskBuild(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	out := atNoTerminal(e)
	ok, reachable, err := e.r.askBuild("do the thing")
	if err != nil || ok || reachable {
		t.Fatalf("no --yes, no terminal: ok=%v reachable=%v err=%v", ok, reachable, err)
	}
	if strings.Contains(out.String(), "confirmed by --yes") {
		t.Errorf("confirmed without --yes:\n%s", out.String())
	}

	e = rigEngine(t, r, &initOptions{yes: true})
	out = atNoTerminal(e)
	ok, reachable, err = e.r.askBuild("do the thing")
	if err != nil || !ok || !reachable {
		t.Fatalf("--yes, no terminal: ok=%v reachable=%v err=%v", ok, reachable, err)
	}
	for _, want := range []string{"⚠ CONFIRM", "do the thing", "confirmed by --yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// Defence in depth: askBuild refuses an agent's session itself, --yes
// included, even if the up-front refusal were bypassed (it is called
// directly here). Mutation: drop the agent check and --yes confirms.
func TestAskBuildRefusesAnAgentSession(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			for _, k := range agentMarkers {
				t.Setenv(k, "")
			}
			t.Setenv(marker, "1")
			r := newInitRig(t)
			e := rigEngine(t, r, &initOptions{yes: true})
			out := atNoTerminal(e)
			ok, _, err := e.r.askBuild("do the thing")
			var ae *initflow.AgentError
			if !errors.As(err, &ae) || ae.Marker != marker || ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if strings.Contains(out.String(), "confirmed by --yes") {
				t.Errorf("confirmed in an agent session:\n%s", out.String())
			}
		})
	}
}

// 2026-10-08 decision: --yes confirms every stale build with no terminal and
// no cap on how many run (here, both workflows).
func TestRefreshBuildsYesNoTerminalNoCap(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTargetOpts(t, r, &initOptions{yes: true})
	p := &refreshPlan{workflows: []string{"api", "app"}}
	// Neither has a build record: both are todo, and --yes confirms both,
	// with no cap on the count.
	out := atNoTerminal(e)
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 2 {
		t.Fatalf("builds %+v, want both (no cap)", fb.specs)
	}
	if n := strings.Count(out.String(), "confirmed by --yes"); n != 2 {
		t.Errorf("%d \"confirmed by --yes\" lines, want 2 (one per build):\n%s", n, out.String())
	}
}

// 2026-10-08 decision: under --yes a failed build still stops the ones
// after it, and names what finished, exactly as without --yes.
func TestRefreshBuildsYesFailureStopsTheLoop(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFailingBuilder(t, 1)
	e, tg := buildTargetOpts(t, r, &initOptions{yes: true})
	p := &refreshPlan{workflows: []string{"api", "app"}}
	out := atNoTerminal(e)
	err := e.r.refreshBuilds(t.Context(), p, tg)
	if ExitCode(err) != ExitRemoteError || len(fb.specs) != 1 || fb.specs[0].Workflow != "api" {
		t.Fatalf("exit %d, err %v, builds %+v", ExitCode(err), err, fb.specs)
	}
	if !strings.Contains(err.Error(), "quota") || !strings.Contains(err.Error(), "built before this: none") {
		t.Errorf("error %q", err)
	}
	if !strings.Contains(out.String(), "confirmed by --yes") {
		t.Errorf("output lacks the --yes confirmation of the one build that started:\n%s", out.String())
	}
}

// longJob sets up a check job whose spec is long (repo_url, workflows,
// registry, service account), so that its base_images, the spec's last field,
// lies past what Printable shows of the whole string.
func longJob(t *testing.T, bases, local map[string]string, image string, selected []string) (*initRig, *initEngine, *refreshTarget) {
	t.Helper()
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	spec, err := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", Provider: "bitbucket",
		RepoURL:    "https://bitbucket.org/acme/" + strings.Repeat("a-very-long-repository-name-", 5) + "sandbox.git",
		BaseBranch: "main", Workflows: []string{"alpha-workflow", "beta-workflow", "gamma-workflow"},
		Registry:            "us-east5-docker.pkg.dev/" + strings.Repeat("proj-", 10) + "/fugaro-images",
		BuildServiceAccount: "build-" + strings.Repeat("sa-", 20) + "@proj.iam.gserviceaccount.com",
		MachineType:         "E2_HIGHCPU_8", BuildRegion: "us-east5", BaseImages: bases})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec) < 2*pluginwire.MaxPrintable {
		t.Fatalf("fixture: spec is only %d bytes", len(spec))
	}
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, image)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = local
	return r, e, &refreshTarget{lc: lc, kinds: selected, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}}
}

// The confirmation shows the real change: the base_images diff per kind, an
// unselected kind that also moves flagged as such, a kind that does not move
// not listed, and a statement that the rest is unchanged. A long spec does
// not hide the base change (Printable cuts the whole string; the spec is
// never printed).
func TestRefreshCheckJobShowsTheBaseDiff(t *testing.T) {
	oldRed, newRed := managedRef("red", "0.4.0"), managedRef("red", "0.5.1")
	oldBlue, newBlue := managedRef("blue", "0.4.0"), managedRef("blue", "0.5.1")
	same := managedRef("green", "0.4.0")
	r, e, tg := longJob(t, map[string]string{"red": oldRed, "blue": oldBlue, "green": same},
		map[string]string{"red": newRed, "blue": newBlue, "green": same}, oldRed, []string{"red"})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"red: " + oldRed + " -> " + newRed + "\n",
		"blue: " + oldBlue + " -> " + newBlue + " (not selected: also moved to the local config's base)",
		"no other field of " + infra.CheckSpecEnv + " or the job changes; its execution tokens (startExecutionToken, runExecutionToken) are not sent back",
		"is designed to plan no change to it (not yet verified against the real API: see docs/gcp-live-checklist.md Check 30)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "green") {
		t.Errorf("a kind that does not move is listed:\n%s", got)
	}
	if strings.Contains(got, "alpha-workflow") || strings.Contains(got, `"repo_url"`) {
		t.Errorf("the whole spec was printed:\n%s", got)
	}
	if len(r.run.Patches()) != 1 {
		t.Fatalf("%d patches", len(r.run.Patches()))
	}
}

// A failed PATCH stops the run: it returns the (remote) error and prints
// nothing as success.
func TestRefreshCheckJobApplyErrorStops(t *testing.T) {
	oldRed, newRed := managedRef("red", "0.4.0"), managedRef("red", "0.5.1")
	r, e, tg := longJob(t, map[string]string{"red": oldRed}, map[string]string{"red": newRed}, oldRed, []string{"red"})
	r.run.SetJobFaults(gcpfake.JobFaults{PatchStatus: http.StatusInternalServerError})
	out := atTerminal(t, e, initProjectName+"\n")
	err := e.r.refreshCheckJob(t.Context(), tg)
	if ExitCode(err) != ExitRemoteError || strings.Contains(out.String(), "updated the daily image check job") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out.String())
	}
}

// A missing job (404) is a not-found message naming init --repo when the
// repository's spec has a check job, and "nothing to update" only when it has
// none.
// A missing job (404) is reported and the run continues, with or without
// --yes (2026-10-08 decision): there is nothing to confirm either way.
func testRefreshCheckJobMissing(t *testing.T, yes bool) {
	t.Helper()
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{yes: yes})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"base": managedRef("base", "0.5.1")}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label, Check: &infra.CheckSpec{}}}
	at := func() *syncBuf {
		if yes {
			return atNoTerminal(e)
		}
		return atTerminal(t, e, "")
	}
	out := at()
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "was not found") || !strings.Contains(out.String(), "fugaro init --repo") || strings.Contains(out.String(), "nothing to update") {
		t.Errorf("yes=%v, with a check in the spec:\n%s", yes, out.String())
	}
	tg.spec.Check = nil
	out = at()
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil || !strings.Contains(out.String(), "nothing to update") || strings.Contains(out.String(), "was not found") {
		t.Errorf("yes=%v, without a check: %v\n%s", yes, err, out.String())
	}
}

func TestRefreshCheckJobMissing(t *testing.T) { testRefreshCheckJobMissing(t, false) }

func TestRefreshCheckJobMissingEvenWithYes(t *testing.T) { testRefreshCheckJobMissing(t, true) }

// failingBuilder is the fake builder whose failOn'th (1-based) submit fails.
type failingBuilder struct {
	*fakeBuilder
	failOn int
}

func (f *failingBuilder) Submit(ctx context.Context, s gcp.BuildSpec) (gcp.BuildResult, error) {
	if len(f.specs)+1 == f.failOn {
		f.specs = append(f.specs, s)
		return gcp.BuildResult{}, errors.New("quota")
	}
	return f.fakeBuilder.Submit(ctx, s)
}

func useFailingBuilder(t *testing.T, failOn int) *failingBuilder {
	t.Helper()
	f := &failingBuilder{fakeBuilder: &fakeBuilder{}, failOn: failOn}
	old := newCloudBuilder
	newCloudBuilder = func(context.Context, *localcfg.Config) (cloudBuilder, error) { return f, nil }
	t.Cleanup(func() { newCloudBuilder = old })
	return f
}

// A failing build stops the loop: the next workflow is never submitted, the
// error is returned (exit 2), it names what finished (here nothing), and the
// no-record notes are printed.
func TestRefreshBuildsFailureStopsTheLoop(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFailingBuilder(t, 1)
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"api", "app"}}
	out := atTerminal(t, e, initProjectName+"\n"+initProjectName+"\n")
	err := e.r.refreshBuilds(t.Context(), p, tg)
	if ExitCode(err) != ExitRemoteError || len(fb.specs) != 1 || fb.specs[0].Workflow != "api" {
		t.Fatalf("exit %d, err %v, builds %+v", ExitCode(err), err, fb.specs)
	}
	if !strings.Contains(err.Error(), "quota") || !strings.Contains(err.Error(), "built before this: none") {
		t.Errorf("error %q", err)
	}
	for _, wf := range []string{"api", "app"} {
		if !strings.Contains(out.String(), "note: "+wf+" had no build record") {
			t.Errorf("no note for %s:\n%s", wf, out.String())
		}
	}
}

// A failure or a decline after a finished build names it, and keeps the
// notes.
func TestRefreshBuildsNameWhatFinished(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		failOn       int
		want         string
	}{
		{"failed", initProjectName + "\n", 2, "quota"},
		{"declined", "nope\n", 0, "not confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useVersion(t, "0.5.1")
			r := newAnchorModeRig(t, refreshYAML, true)
			useFailingBuilder(t, tc.failOn)
			e, tg := buildTarget(t, r)
			p := &refreshPlan{workflows: []string{"api", "app"}}
			out := atTerminal(t, e, initProjectName+"\n"+tc.second)
			err := e.r.refreshBuilds(t.Context(), p, tg)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "built before this: api") {
				t.Fatalf("err %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "built acme/app/api") || !strings.Contains(out.String(), "note: app had no build record") {
				t.Errorf("output:\n%s", out.String())
			}
		})
	}
}

// Without a terminal the refusal says a terminal is required, not that the
// name was not typed (D2).
func TestRefreshBuildsNoTerminalSaysSo(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"api", "app"}}
	e.r.w = &syncBuf{}
	err := e.r.refreshBuilds(t.Context(), p, tg)
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") || strings.Contains(err.Error(), "was not typed") || len(fb.specs) != 0 {
		t.Fatalf("err %v, %d builds", err, len(fb.specs))
	}
}

// M4: a build already submitted when its wait fails (Ctrl-C cancels the
// context) is not cancelled: the stop says so, names its ID, and warns that
// a rerun right away may offer a duplicate build.
// Mutation (run, restore): `return res.ID, remote(err)` back to
// `return "", remote(err)` in submitAndWait, or drop the withNote call in
// refreshBuilds: this test fails.
func TestRefreshBuildsWaitFailureSaysTheBuildKeepsRunning(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	fb.waitErr = context.Canceled
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"api", "app"}}
	out := atTerminal(t, e, initProjectName+"\n")
	err := e.r.refreshBuilds(t.Context(), p, tg)
	if ExitCode(err) != ExitRemoteError || len(fb.specs) != 1 {
		t.Fatalf("exit %d, err %v, %d builds\n%s", ExitCode(err), err, len(fb.specs), out.String())
	}
	for _, want := range []string{"build b0001 of acme/app/api was already submitted and KEEPS RUNNING (and billing) in Cloud Build",
		"gcloud builds describe b0001 --region ", "may find no build record yet and offer a duplicate build", "built before this: none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}
