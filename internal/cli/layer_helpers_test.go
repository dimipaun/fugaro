package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// testProjectLayer is aurora's project layer in the cloud fixture: profile
// svc is recipeCheckout's workflow svc.
const testProjectLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    base: web-node
    commands: { build: sh build.sh, test: sh test.sh }
default_profile: svc
`

const minimalAnchored = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"

// publishedLayer moves the fixture to the default runs bucket name and,
// unless text is "", writes text as the project layer.
func publishedLayer(t *testing.T, f *cloudFixture, text string) {
	t.Helper()
	projectRecipesFixture(t, f, nil)
	if text != "" {
		writeBucketFile(t, f, config.LayerKey, text)
	}
}

// layerWithDefaults is testProjectLayer with extra lines (indented two
// spaces) added under defaults:.
func layerWithDefaults(extra string) string {
	return strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n", "  agent: { auth: api-key }\n"+extra, 1)
}

// isolateCache gives the test a cache directory of its own.
func isolateCache(t *testing.T) { t.Setenv("XDG_CACHE_HOME", t.TempDir()) }

// noProjectLayerBucket points layerBucketOpener at an empty file:// bucket
// for the rest of the test: a rig built around a local config with no
// bucket_url: (newDoctorRig, newInitRig) computes a gs://fugaro-runs-<gcp>
// URL for findLayer exactly as it would for the real installation, and
// there is no fake behind that URL. Every other rig (newCloudFixture and
// its layerCheckout/publishedLayer) already sets bucket_url: file://, so
// only a test anchoring one of the former needs this. The read then comes
// back "not found" (no layer applies), never a real network call: TestMain
// panics any test that reaches a gs:// URL through this seam.
func noProjectLayerBucket(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := layerBucketOpener
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+dir) }
	t.Cleanup(func() { layerBucketOpener = old })
}

// layerCheckout makes the working directory a git checkout holding yaml as
// fugaro.yaml, and returns its root. f is unused today (no test here needs
// a build record or an origin remote); it is kept so a later test that
// does can add one without changing every call site.
func layerCheckout(t *testing.T, f *cloudFixture, yaml string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	t.Chdir(dir)
	return dir
}
