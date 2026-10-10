package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

const release060 = "ghcr.io/dimipaun/fugaro-web-node:0.6.0"

// registerOtherLocally registers acme/other's provider and (implicit)
// workflow in the fixture's local config.
//
// Without it, resolveWorkflow and repoSlug cannot tell acme/other's workflow
// or git provider from a minimal anchored checkout (no workflows:, no
// git.provider:): both read them from the checkout's fugaro.yaml through
// checkoutConfig, which still calls plain config.Parse (no layer) and so
// fails closed on a minimal file. Task 10 ("every in-checkout command
// resolves through the seam") is what points checkoutConfig at the layer
// too, and is not part of this codebase yet; see the PR description. This
// helper routes around that gap with the local config's own, pre-existing
// repos: mechanism (the same one appSlug/acme-app already uses), so these
// tests still exercise embedProjectLayer itself, which always resolves the
// checkout's fugaro.yaml over the layer on its own.
func registerOtherLocally(t *testing.T, f *cloudFixture) {
	t.Helper()
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: ["+config.ImplicitWorkflow+"] }\n")
}

func TestRunEmbedsTheProjectLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	// file:// buckets always report generation 0 (internal/blobx), which
	// would let a bug that always embeds 0 pass unnoticed (M9): layerRead is
	// patched to report a distinct generation, the real bytes untouched.
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
		data, _, err := read(ctx, b)
		return data, 7, err
	}
	out, errOut, err := execute(t, "run", "--json", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	pl := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261008-100000-abcd").ProjectLayer
	if pl == nil || pl.SHA256 != config.LayerSum([]byte(testProjectLayer)) || pl.YAML != testProjectLayer || pl.Generation != 7 {
		t.Fatalf("task project_layer = %+v", pl)
	}
	for _, want := range []string{"project layer: aurora generation 7", "commands: from profile svc (build, test; project layer generation 7)"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.ProjectLayer == nil || res.ProjectLayer.SHA256 != config.LayerSum([]byte(testProjectLayer)) || res.ProjectLayer.Generation != 7 {
		t.Fatalf("--json project_layer = %+v", res.ProjectLayer)
	}
}

// Review fix (1), mutation M5: the launch line used to check only
// workflows.<w>.commands.test, so a profile that sets only commands.build
// (or only commands.rerun_failed) ran shell from the bucket with zero
// announcement. The repository here keeps its own commands.test (overriding
// the profile's), so the only run-time key the profile actually supplies is
// build.
func TestRunAnnouncesABuildOnlyProfile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	layer := strings.Replace(testProjectLayer, "commands: { build: sh build.sh, test: sh test.sh }", "commands: { build: sh build.sh }", 1)
	if layer == testProjectLayer {
		t.Fatal("testProjectLayer's profile commands line changed shape")
	}
	publishedLayer(t, f, layer)
	layerCheckout(t, f, minimalAnchored+"workflows:\n  svc: { profile: svc, commands: { test: sh test.sh } }\n")
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: [svc] }\n")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", release060)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "commands: from profile svc (build; project layer generation") {
		t.Errorf("stderr %q lacks the build-only announcement", errOut)
	}
	if strings.Contains(errOut, "(build, test;") || strings.Contains(errOut, "(test;") {
		t.Errorf("stderr %q wrongly folds in commands.test, which the repository overrides", errOut)
	}
}

// Review Focus 3.
func TestRunRefusesLayerOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release050)
	wantRefused(t, f, "the project layer needs a job image whose runner knows the project layer (fugaro 0.6.0 or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

func TestRunWithoutALayerEmbedsNothing(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	recipeCheckout(t, f, "", nil)
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if pl := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261008-100000-abcd").ProjectLayer; pl != nil {
		t.Fatalf("task project_layer = %+v", pl)
	}
}

// A repeated --run-id must stay idempotent even though embedProjectLayer
// re-reads the bucket on every call (decision L13, no fresh window): an
// object rewritten with the same bytes gets a new generation, which must
// not make the repeat look like "a different task" (code review finding).
func TestRunRepeatedRunIDToleratesLayerGenerationChange(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	var calls int64
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
		data, _, err := read(ctx, b)
		calls++
		return data, calls, err // same bytes, a new generation on every read
	}
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("repeated --run-id after the layer's generation changed: %v", err)
	}
	if !strings.Contains(out, "already launched") {
		t.Fatalf("stdout = %q", out)
	}
}

// Review fix (3): a repeated --run-id must stay idempotent even when the
// project layer is genuinely republished (different content, not just a
// new generation of the same bytes) between the first launch and the
// repeat. The stored task is what the job actually reads, so the repeat
// keeps it rather than refusing or silently swapping in the new one.
func TestRunRepeatedRunIDToleratesLayerRepublish(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	republished := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	writeBucketFile(t, f, config.LayerKey, republished)
	out, errOut, err := execute(t, "run", "--json", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("repeated --run-id after the layer was republished: %v", err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "already-launched" {
		t.Fatalf("status = %q", res.Status)
	}
	// The repeat's own report must reflect the STORED (original) layer, not
	// whatever it just freshly re-resolved from the republished bucket.
	if res.ProjectLayer == nil || res.ProjectLayer.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("repeat's reported project_layer = %+v, want the original layer's sum", res.ProjectLayer)
	}
	pl := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261008-100000-abcd").ProjectLayer
	if pl == nil || pl.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("the stored task's project_layer changed after a repeat: %+v", pl)
	}
	// The repeat's human-readable stderr line must describe the same
	// adopted (original) layer, not the republished one it briefly saw
	// while re-resolving.
	origShort, newShort := config.LayerSum([]byte(testProjectLayer))[:12], config.LayerSum([]byte(republished))[:12]
	if !strings.Contains(errOut, "project layer: aurora generation") || !strings.Contains(errOut, origShort) {
		t.Errorf("stderr %q lacks the adopted layer's own announcement", errOut)
	}
	if strings.Contains(errOut, newShort) {
		t.Errorf("stderr %q announces the republished layer instead of the one actually adopted", errOut)
	}
}

// Review fix (6), mutation M8: findLayer's cache-fallback note must reach
// the user even through a full fugaro run launch, not just findLayer's own
// tests. The bucket is warmed once, then made unreachable, so the launch
// can only succeed by falling back to the 7-day cache (decision L13).
func TestRunAnnouncesACachedLayerNote(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, time.Now()); err != nil {
		t.Fatal(err) // warms the cache
	}
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note: using the cached project layer of aurora") {
		t.Errorf("stderr %q lacks the cached-copy note", errOut)
	}
}

// Review fix (6), mutation M7: embedProjectLayer must resolve against the
// checkout's own fugaro.yaml when there is one, never always fall back to
// the installation's own project/gcp_project. The checkout here names a
// different project and GCP project than the fixture's installation
// (aurora/proj-1234), with its own layer in a bucket layerBucketOpener
// stands in for.
//
// This calls embedProjectLayer directly, like TestCheckRecipeImage calls
// checkRecipeImage: going through the full fugaro run command would first
// hit openCloud's project selection, which (correctly, but besides this
// test's point) falls back to fetching a *shared config* from the
// checkout's own gcp_project when no local project config matches it
// (internal/cli/project.go's selectWith) — a real, pre-existing mechanism
// unrelated to project layers that would need its own network fake here.
func TestEmbedProjectLayerUsesTheCheckoutsOwnProject(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer) // aurora/proj-1234's; must not be the one embedded
	otherLayer := "version: 1\nproject: otherproj\ngcp_project: proj-9999\nprofiles:\n  x:\n    base: web-node\n    commands: { build: sh build.sh, test: sh test.sh }\ndefault_profile: x\n"
	otherBucket := blobx.Wrap(memblob.OpenBucket(nil))
	if _, err := otherBucket.Create(context.Background(), config.LayerKey, []byte(otherLayer), "application/yaml"); err != nil {
		t.Fatal(err)
	}
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if url == "gs://fugaro-runs-proj-9999" {
			return otherBucket, nil
		}
		return open(ctx, url)
	}
	layerCheckout(t, f, "version: 1\nproject: otherproj\ngcp_project: proj-9999\n")
	env := fileEnv(t, f) // the installation's own: aurora/proj-1234
	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Workflow: config.ImplicitWorkflow, Task: "x"}
	var warn bytes.Buffer
	if _, err := embedProjectLayer(context.Background(), env, spec, &warn); err != nil {
		t.Fatal(err)
	}
	if spec.ProjectLayer == nil || spec.ProjectLayer.SHA256 != config.LayerSum([]byte(otherLayer)) {
		t.Fatalf("spec.ProjectLayer = %+v, want otherproj's own layer, not the installation's", spec.ProjectLayer)
	}
}

// Review fix (6), mutation M10: a follow-up (--pr) must carry the CURRENT
// project layer too (decision L15: a follow-up already re-reads the base
// branch's fugaro.yaml, so it resolves against today's project, not the
// previous run's), not skip embedding because runRun reaches it through
// prSpec rather than newSpec.
func TestRunFollowUpEmbedsTheCurrentProjectLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release060)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.ProjectLayer == nil || res.ProjectLayer.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("follow-up project_layer = %+v", res.ProjectLayer)
	}
}

// Review fix (4): a checkout whose fugaro.yaml exists but can't be read
// (here, it is a directory: a real problem, unlike the ordinary "no
// checkout at all" case) must say so instead of silently resolving against
// the installation's own project as if there were no checkout here. The
// installation's own project happens to be aurora/proj-1234 too, so the
// fallback still finds a layer; the point is that it is announced as a
// fallback, not applied silently.
//
// This calls embedProjectLayer directly (as TestEmbedProjectLayerUsesThe
// CheckoutsOwnProject does): going through the full fugaro run command
// would first hit internal/cli/project.go's checkoutProject, which already
// refuses a fugaro.yaml that is a directory before openCloud even returns,
// for reasons of its own unrelated to project layers.
func TestEmbedProjectLayerAnnouncesAnUnreadableCheckoutFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "fugaro.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Workflow: config.ImplicitWorkflow, Task: "x"}
	var warn bytes.Buffer
	if _, err := embedProjectLayer(context.Background(), env, spec, &warn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "note: could not read") || !strings.Contains(warn.String(), "not a regular file") {
		t.Errorf("warnings %q lack the unreadable-checkout note", warn.String())
	}
	if spec.ProjectLayer == nil {
		t.Fatal("the installation's own layer should still apply as a fallback")
	}
}

// Review fix (must-fix): a checkout that simply has no fugaro.yaml yet
// (os.ErrNotExist) is the ordinary, harmless case checkoutParse (cloud.go)
// already treats the same as "no checkout at all", silently. Unlike the
// directory case above, this one must stay quiet: a missing file is not "a
// real problem", and every fugaro run from such a checkout would otherwise
// print a misleading note on every single launch.
func TestEmbedProjectLayerStaysQuietWithNoFugaroYAMLYet(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Workflow: config.ImplicitWorkflow, Task: "x"}
	var warn bytes.Buffer
	if _, err := embedProjectLayer(context.Background(), env, spec, &warn); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(warn.String(), "could not read") {
		t.Errorf("warnings %q wrongly flag the ordinary missing-file case", warn.String())
	}
	if spec.ProjectLayer == nil {
		t.Fatal("the installation's own layer should still apply as a fallback")
	}
}

// Review fix (5): when the checkout's fugaro.yaml resolves fine for
// findLayer (which only needs the lenient project:/gcp_project: scalars)
// but fails the full, strict Resolve announceProfileCommands uses to pick
// out which profile applies (an unknown top-level key here), the launch
// still carries the layer (embedProjectLayer already set it), so it must
// say a note instead of silently announcing nothing.
func TestRunAnnouncesWhenLocalResolutionFails(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+"bogus_key: 1\n")
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "project layer: aurora generation") {
		t.Errorf("stderr %q lacks the layer line", errOut)
	}
	if !strings.Contains(errOut, "note: could not tell locally which profile") {
		t.Errorf("stderr %q lacks the local-resolution-failed note", errOut)
	}
}

// Round-2 review fix (1), security (decision L16): a launch is strict, never
// lenient like validate/doctor. A bucket that cannot be read, with nothing
// usable cached, must refuse the launch outright: a full fugaro.yaml may
// depend on project defaults a silently-skipped layer would otherwise drop
// without anyone noticing.
func TestRunRefusesWhenTheLayerBucketIsUnreadable(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err == nil {
		t.Fatal("launched despite an unreadable project layer bucket with nothing cached")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// Round-2 review fix (2), B13: the read-error note must go through
// oneLineCLI (collapsed to one line, control characters escaped), the same
// as every other message built from text this codebase does not fully
// trust. readCheckoutFugaroYAML is patched directly, since the real
// config.ReadRegular errors never embed anything but a (safe) path.
func TestEmbedProjectLayerEscapesAReadError(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	read := readCheckoutFugaroYAML
	t.Cleanup(func() { readCheckoutFugaroYAML = read })
	readCheckoutFugaroYAML = func(string) ([]byte, error) {
		return nil, errors.New("line one\x1b[31mESCAPED\x1b[0m\nline two")
	}
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Workflow: config.ImplicitWorkflow, Task: "x"}
	var warn bytes.Buffer
	if _, err := embedProjectLayer(context.Background(), env, spec, &warn); err != nil {
		t.Fatal(err)
	}
	out := warn.String()
	if !strings.Contains(out, "line one") {
		t.Errorf("warnings %q lack the error's first line", out)
	}
	if strings.Contains(out, "line two") {
		t.Errorf("warnings %q wrongly carry a second line", out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("warnings %q contain a raw escape byte", out)
	}
	if !strings.Contains(out, `\u001b`) {
		t.Errorf("warnings %q lack the escaped control character", out)
	}
}

// Round-2 review fix (3), B14: the L7 line is one per distinct profile; a
// bug that appended a duplicate would pass every existing assertion, since
// they only use strings.Contains.
func TestRunAnnouncesEachProfileLineExactlyOnce(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	line := "commands: from profile svc (build, test; project layer generation 0)\n"
	if n := strings.Count(errOut, line); n != 1 {
		t.Fatalf("stderr %q has the commands line %d times, want 1", errOut, n)
	}
}

// Round-2 review fix (4): a repeated --run-id whose earlier attempt stored
// a task but never actually launched it (here, an ambiguous launch
// failure) must gate on the project layer that stored task actually
// carries, not whatever embedProjectLayer freshly re-resolves this time:
// createTask (via repeatTaskBytes/adoptStoredProjectLayer) keeps the
// stored task's layer regardless of what changed in the bucket since, so
// the image gate must judge that SAME adopted layer rather than skip it
// because a fresh resolve this time found none.
func TestRunGatesOnTheAdoptedLayerForAnUnlaunchedStoredTask(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	// The first attempt resolves and stores the layer, passes the image
	// gate (the job image is new enough), but its launch itself fails
	// ambiguously: task.json exists, launch.json never gets written.
	f.run.FailRunWith = 503
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("first attempt: %v", err)
	}
	f.run.FailRunWith = 0
	slug := mustSlug("github", "acme/other")
	runDir := filepath.Join(f.dir, "runs", "runs", slug, "20261008-100000-abcd")
	// Drop the launch claim the failed attempt left (it survives an
	// ambiguous failure on purpose, to block an immediate relaunch): this
	// test is about the image gate, not the claim's own staleness rules.
	if err := os.Remove(filepath.Join(runDir, "launching")); err != nil {
		t.Fatal(err)
	}
	// Between the two attempts, the layer is unpublished and the job
	// image now predates layeredSince: a launch that still carries the
	// stored task's layer must refuse it.
	if err := os.Remove(filepath.Join(f.dir, "runs", filepath.FromSlash(config.LayerKey))); err != nil {
		t.Fatal(err)
	}
	writeBuildRecord(t, f, slug, config.ImplicitWorkflow, release050)
	n := len(f.run.Executions())
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the project layer needs a job image whose runner knows the project layer (fugaro 0.6.0 or later)") {
		t.Fatalf("err = %v", err)
	}
	if len(f.run.Executions()) != n {
		t.Fatal("a second execution started despite the refusal")
	}
}

func TestRunOutsideACheckoutEmbedsTheInstallationLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	writeBuildRecord(t, f, appSlug, "web", release060)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if pl := readSpec(t, f, "20261008-100000-abcd").ProjectLayer; pl == nil || pl.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("task project_layer = %+v", pl)
	}
}
