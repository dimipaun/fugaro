package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
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
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	pl := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261008-100000-abcd").ProjectLayer
	if pl == nil || pl.SHA256 != config.LayerSum([]byte(testProjectLayer)) || pl.YAML != testProjectLayer {
		t.Fatalf("task project_layer = %+v", pl)
	}
	for _, want := range []string{"project layer: aurora generation", "commands: from profile svc (project layer generation"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
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
