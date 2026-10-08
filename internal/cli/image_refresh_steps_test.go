package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

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

// Step 2 copies only the kinds it is given, after its own confirmation (the
// digest shown), and a rerun copies nothing. No step here touches Terraform.
func TestRefreshBaseStep(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML)) // names web-node and go
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"go"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] == "" || r.dst.tags[initProject+"/fugaro-base/fugaro-web-node:1.2.3"] != "" {
		t.Fatalf("copied %v", r.dst.tags)
	}
	if got := r.localConfig(t).BaseImages["go"]; got != "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:1.2.3" {
		t.Fatalf("base_images.go = %s", got)
	}
	for _, want := range []string{"⚠ CONFIRM", "sha256:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q (the confirmation or the digest):\n%s", want, out.String())
		}
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the base step ran terraform: %q", calls)
	}
	puts := r.dst.puts
	out = atTerminal(t, e, "")
	if err := e.r.refreshBase(t.Context(), e, []string{"go"}); err != nil || r.dst.puts != puts || !strings.Contains(out.String(), "No changes") {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out.String())
	}
}

// A declined base copy stops the step: nothing is copied or recorded.
func TestRefreshBaseStepDeclined(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, "nope\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"go"}); err == nil || r.dst.puts != 0 {
		t.Fatalf("declined: %v, %d puts\n%s", err, r.dst.puts, out.String())
	}
	if got := r.localConfig(t).BaseImages["go"]; got != "" {
		t.Fatalf("base_images.go was recorded despite the decline: %s", got)
	}
}

// The base step never replaces a custom or hand-pushed base image, even
// called directly with that kind: the images stage's own rule (D4) leaves it
// alone, the same guard fugaro init --base has.
func TestRefreshBaseStepNeverReplacesCustomBase(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, refreshYAML))
	r.appendConfig(t, "base_images: {go: ghcr.io/me/fugaro-go:mine}\n")
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"go"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if r.dst.puts != 0 {
		t.Fatalf("a custom base was copied: %d puts", r.dst.puts)
	}
	if got := r.localConfig(t).BaseImages["go"]; got != "ghcr.io/me/fugaro-go:mine" {
		t.Fatalf("base_images.go was changed to %q", got)
	}
	if !strings.Contains(out.String(), "nothing mirrored") {
		t.Errorf("output does not explain the skip:\n%s", out.String())
	}
}

// refreshReload re-reads the local config the base step wrote (not the
// plan's own, now-stale copy) and computes the repository's spec from it:
// the checkout's config is carried through, and the check job's image (the
// first workflow's kind, sorted: api's go before app's web-node) is the
// re-read base.
func TestRefreshReload(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	putWorkflowRecordFrom("api", "0.5.1", managedRef("go", "0.5.1"))
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.lc.BaseImages) != 0 {
		t.Fatalf("fixture: preflight's own lc already has base_images: %v", p.lc.BaseImages)
	}
	// Simulate step 2 having since recorded both kinds: refreshReload must
	// pick this up from the file, not from p.lc.
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.5.1")+", go: "+managedRef("go", "0.5.1")+"}\n")

	tg, err := refreshReload(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.lc.BaseImages) != 0 {
		t.Errorf("refreshReload mutated the plan's own (stale) lc: %v", p.lc.BaseImages)
	}
	if tg.lc.BaseImages["web-node"] != managedRef("web-node", "0.5.1") || tg.lc.BaseImages["go"] != managedRef("go", "0.5.1") {
		t.Fatalf("lc.BaseImages = %v, want both kinds the base step just recorded", tg.lc.BaseImages)
	}
	if tg.cfg != p.cfg {
		t.Error("the checkout's config was not carried through")
	}
	if tg.spec.Name != "acme/app" || tg.spec.Slug != p.slug || tg.spec.RegistryPath == "" || tg.spec.BuildServiceAccountEmail == "" {
		t.Fatalf("spec %+v", tg.spec)
	}
	if tg.spec.Check == nil || tg.spec.Check.Image != managedRef("go", "0.5.1") {
		t.Fatalf("check image = %+v, want %s (api's kind, sorted first)", tg.spec.Check, managedRef("go", "0.5.1"))
	}
}

// A kind the local config has no base_images entry for yet (the base step
// has not recorded it) falls back to this release's own image for the spec
// only, as fugaro image build does: without the fallback, infra.Repo refuses
// a checked workflow with no base_images entry for its kind, and the file
// itself is never written by this fallback.
func TestRefreshReloadFillsMissingBaseFromRelease(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.5.1")+"}\n") // go is still unset
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	putWorkflowRecordFrom("api", "0.5.1", managedRef("go", "0.5.1"))
	p, err := refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tg, err := refreshReload(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if tg.lc.BaseImages["go"] != "" {
		t.Fatalf("refreshReload wrote base_images.go to the local config: %v", tg.lc.BaseImages)
	}
	want, err := image.BaseRef("go", "0.5.1")
	if err != nil {
		t.Fatal(err)
	}
	if tg.spec.Check == nil || tg.spec.Check.Image != want {
		t.Fatalf("check image = %+v, want the release fallback %s", tg.spec.Check, want)
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
	oldRef, newRef := managedRef("web-node", "0.4.0"), managedRef("web-node", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"web-node": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec), "FUGARO_PROJECT": "aurora"})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": newRef}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"web-node"}}
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

// PlanCheckJob's own owner check (labels, spec repo) refuses a job that is
// not this repository's: ErrCheckJobShape surfaces as a user error, and
// nothing is patched.
func TestRefreshCheckJobRefusesForeignJob(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, _ := checkJobOwner(t, "bitbucket", "acme/sandbox")
	_, otherLabel := checkJobOwner(t, "bitbucket", "acme/other")
	oldRef, newRef := managedRef("web-node", "0.4.0"), managedRef("web-node", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"web-node": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: otherLabel}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": newRef}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"web-node"}}
	atTerminal(t, e, initProjectName+"\n")
	err = e.r.refreshCheckJob(t.Context(), tg)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not as fugaro init --repo made it") || len(r.run.Patches()) != 0 {
		t.Fatalf("exit %d, err %v, %d patches", ExitCode(err), err, len(r.run.Patches()))
	}
}

// A failed job read (a 403, say) is a remote error: never treated as "no
// job" (which would skip step 3 silently) or as already current.
func TestRefreshCheckJobReadErrorIsRemote(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, managedRef("web-node", "0.4.0"))
	r.run.SetJobFaults(gcpfake.JobFaults{GetStatus: http.StatusForbidden})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": managedRef("web-node", "0.5.1")}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"web-node"}}
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
	oldRef := managedRef("web-node", "0.4.0") + "\u202e-evil"
	newRef := managedRef("web-node", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"web-node": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": newRef}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}, kinds: []string{"web-node"}}
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
	e := rigEngine(t, r.initRig, &initOptions{})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": managedRef("web-node", "0.5.1"), "go": managedRef("go", "0.5.1")}
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
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	putWorkflowRecordFrom("api", "0.4.0", managedRef("go", "0.4.0"))
	atTerminal(t, e, "nope\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err == nil || !strings.Contains(err.Error(), "not confirmed") || len(fb.specs) != 0 {
		t.Fatalf("declined: %v, %d builds", err, len(fb.specs))
	}
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "api" || fb.specs[0].Base != managedRef("go", "0.5.1") {
		t.Fatalf("builds %+v", fb.specs)
	}
	if !strings.Contains(out.String(), "app: current: built from "+managedRef("web-node", "0.5.1")) {
		t.Errorf("output:\n%s", out.String())
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the builds step ran terraform: %q", calls)
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

// The build confirmation is typed-only, never the run's or --yes's.
func TestRefreshBuildsConfirmationIsTyped(t *testing.T) {
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
	if !strings.Contains(body, "r.askTyped(") {
		t.Error("refreshBuilds does not ask the typed confirmation")
	}
	for _, bad := range []string{"confirmOrdinary", "askOrdinary", "runCovers", "r.confirm(", "r.ask("} {
		if strings.Contains(body, bad) {
			t.Errorf("refreshBuilds uses %s", bad)
		}
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
	oldWeb, newWeb := managedRef("web-node", "0.4.0"), managedRef("web-node", "0.5.1")
	oldGo, newGo := managedRef("go", "0.4.0"), managedRef("go", "0.5.1")
	same := managedRef("python", "0.4.0")
	r, e, tg := longJob(t, map[string]string{"web-node": oldWeb, "go": oldGo, "python": same},
		map[string]string{"web-node": newWeb, "go": newGo, "python": same}, oldWeb, []string{"web-node"})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"web-node: " + oldWeb + " -> " + newWeb + "\n",
		"go: " + oldGo + " -> " + newGo + " (not selected: also moved to the local config's base)",
		"no other field of " + infra.CheckSpecEnv + " or the job changes; its execution tokens (startExecutionToken, runExecutionToken) are not sent back",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "python") {
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
	oldWeb, newWeb := managedRef("web-node", "0.4.0"), managedRef("web-node", "0.5.1")
	r, e, tg := longJob(t, map[string]string{"web-node": oldWeb}, map[string]string{"web-node": newWeb}, oldWeb, []string{"web-node"})
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
func TestRefreshCheckJobMissing(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": managedRef("web-node", "0.5.1")}
	tg := &refreshTarget{lc: lc, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label, Check: &infra.CheckSpec{}}}
	out := atTerminal(t, e, "")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "was not found") || !strings.Contains(out.String(), "fugaro init --repo") || strings.Contains(out.String(), "nothing to update") {
		t.Errorf("with a check in the spec:\n%s", out.String())
	}
	tg.spec.Check = nil
	out = atTerminal(t, e, "")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil || !strings.Contains(out.String(), "nothing to update") || strings.Contains(out.String(), "was not found") {
		t.Errorf("without a check: %v\n%s", err, out.String())
	}
}

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
