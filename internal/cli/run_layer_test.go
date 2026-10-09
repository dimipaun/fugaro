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
	writeBucketFile(t, f, config.LayerKey, strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1))
	out, _, err := execute(t, "run", "--json", "--run-id", "20261008-100000-abcd", "A task")
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
	if err := embedProjectLayer(context.Background(), env, spec, &warn); err != nil {
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
	if err := embedProjectLayer(context.Background(), env, spec, &warn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "note: could not read") || !strings.Contains(warn.String(), "not a regular file") {
		t.Errorf("warnings %q lack the unreadable-checkout note", warn.String())
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
