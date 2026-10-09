package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestValidateResolvesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, errOut, err := execute(t, "validate")
	if err != nil || !strings.Contains(out, "fugaro.yaml is valid") || !strings.Contains(errOut, "uses the project layer") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestValidateWithAProjectLayerFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "") // none published
	layerCheckout(t, f, minimalAnchored)
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"layer.yaml": testProjectLayer, "bad.yaml": layerWithDefaults("  budget: { per_run_usd: 1 }\n")})
	if out, _, err := execute(t, "validate", "--project-layer", filepath.Join(dir, "layer.yaml")); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("good layer: %q %v", out, err)
	}
	_, _, err := execute(t, "validate", "--project-layer", filepath.Join(dir, "bad.yaml"))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "bad.yaml is invalid") {
		t.Fatalf("bad layer: %v", err)
	}
}

func TestValidateOfflineNeedsTheLayerForAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "validate", "--offline")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "--project-layer") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

func TestValidateOfflineWarnsForAFullFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
	out, errOut, err := execute(t, "validate", "--offline")
	if err != nil || !strings.Contains(out, "is valid") || !strings.Contains(errOut, "project layer") || !strings.Contains(errOut, "was not checked") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestDoctorFugaroYAMLResolves(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if c, fy := fugaroYAMLCheck(context.Background(), dir, fileEnv(t, f).lc); c == nil || !c.OK || fy == nil || !fy.Valid {
		t.Fatalf("check %+v, %+v", c, fy)
	}
}

func TestImageRenderResolvesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "image", "render")
	if err != nil || !strings.Contains(out, "FROM") {
		t.Fatalf("out %q, err %v", out, err)
	}
}
