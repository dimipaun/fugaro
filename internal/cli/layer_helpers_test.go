package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// testProjectLayerAgent is the agent block aurora's project layer carries: the
// three role models a run behind a gateway must be priced by (AB3, D3020).
//
// They are the layer's and not the repository's, because since S1 (D3237) a
// repository's file may name no model at all — config.Resolve drops every key
// config.LiftedKeys lists before the merge — so a fixture whose file wrote them
// would resolve to a run that names no model and is refused before whatever the
// test is about. The keys are pinnedAgent's, moved verbatim, so the values the
// tests read are the ones they always read.
const testProjectLayerAgent = "  agent: { auth: api-key, model: claude-sonnet-5-5, models: { background: claude-haiku-4-5 } }\n"

// testProjectLayer is aurora's project layer in the cloud fixture: profile
// svc is recipeCheckout's workflow svc.
const testProjectLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
` + testProjectLayerAgent + `profiles:
  svc:
    commands: { build: sh build.sh, test: sh test.sh }
default_profile: svc
`

const minimalAnchored = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"

// pinnedMinimal is a repository file for the layer fixtures whose file a command
// validates or a run judges: it is anchored to the layer above and names the one
// agent key a repository still decides, its auth. The models a priced run needs
// are the layer's (S1, D3237).
const pinnedMinimal = minimalAnchored + "agent: { auth: api-key }\n"

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
	return strings.Replace(testProjectLayer, testProjectLayerAgent, testProjectLayerAgent+extra, 1)
}

// isolateCache gives the test a cache directory of its own.
func isolateCache(t *testing.T) { t.Setenv("XDG_CACHE_HOME", t.TempDir()) }

// pinsLayer is the project layer a repository file's own lifted keys become —
// testutil.TakeFilePins, the move the runner harnesses make. A fixture whose
// file writes a model and has to resolve as it did before S1 (D3237) publishes
// this, which is where a run now reads them; "" for a file that names none.
func pinsLayer(body string) string {
	_, layer := testutil.TakeFilePins(body, config.LiftedKeyPaths())
	return layer
}

// layerPins is the project layer a checkout publishes: the file's own lifted
// keys over the fixture layer's other defaults. Since S1 (D3237) a repository
// file need not name git.provider — a checkout that names none was judged by
// the layer's, which is why the fixture layer carries one — and a test that
// republishes the pins alone would have left the checkout judged without a
// provider at all.
func layerPins(body string) string {
	pins := pinsLayer(body)
	if pins == "" {
		return testProjectLayer
	}
	return testutil.MergePins(strings.Replace(testProjectLayer, testProjectLayerAgent, "", 1), pins)
}

// cacheLayer is the copy of a project layer this machine holds after it read
// one (localcfg.SaveLayerCache): the fallback findLayer reaches for when the
// bucket cannot be reached (decision L13), and the only copy a command run
// --offline reads.
func cacheLayer(t *testing.T, project, text string) {
	t.Helper()
	isolateCache(t)
	if err := localcfg.SaveLayerCache(os.Getenv, project, localcfg.SharedCacheEntry{
		GCPProject: testutil.PinsGCPProject,
		Bucket:     "fugaro-runs-" + testutil.PinsGCPProject,
		CheckedAt:  time.Now(),
		YAML:       text,
	}); err != nil {
		t.Fatal(err)
	}
}

// projectNamed is the project: line of a repository document: the project whose
// layer the file's own keys are published as.
func projectNamed(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(l, "project:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// anchoredText gives body the gcp_project: line its project layer is matched
// by, when it names none: the line goes after project:, and every other byte of
// the file stays as its author wrote it — fixtures here do their own surgery on
// those bytes and some compare them, so the file is edited textually rather
// than round-tripped through a YAML node (testutil.AnchorFile, which the runner
// harnesses use, re-encodes).
func anchoredText(body string) string {
	lines := strings.Split(body, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "gcp_project:") {
			return body
		}
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "project:") {
			continue
		}
		out := append(append([]string{}, lines[:i+1]...), "gcp_project: "+testutil.PinsGCPProject)
		return strings.Join(append(out, lines[i+1:]...), "\n")
	}
	return body
}

// attachLayer gives project the project layer text, where every reader of the
// fixture's file finds it. Since S1 (D3237) a repository's file may name no
// model, so a fixture whose file wrote one resolves to a run that names none —
// refused before whatever the test is about — unless the keys it wrote are the
// project layer's, which is where a run reads them.
//
// The object is written at config.LayerKey in a bucket of the test's own and
// served at whatever URL findLayer computes (layerBucketOpener), which is also
// what keeps a read off a real gs:// one (TestMain refuses one outright); the
// same text is left in this machine's layer cache, for the readers that fall
// back to it (decision L13) and for a command run --offline.
//
// The installation's own project config is moved to the default runs bucket
// name first (decision L9): findLayer applies a layer only over a repository
// whose project is the selected config's own and whose runs bucket is
// fugaro-runs-<gcp>, and the harness's runs_bucket: unused-bucket is neither. A
// world with no project config yet — another project's is selected, or the test
// builds one later — has nothing to move, but is served the same object: the
// anchor and gcp_project: the qualified body carries are what the layer is
// matched against, not that config.
func attachLayer(t *testing.T, project, text string) {
	t.Helper()
	cacheLayer(t, project, text)
	if path, err := localcfg.ProjectPath(os.Getenv, project); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			cfg := strings.Replace(string(b),
				"runs_bucket: unused-bucket", "runs_bucket: fugaro-runs-"+testutil.PinsGCPProject, 1)
			if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := t.TempDir()
	old := layerBucketOpener
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+dir) }
	t.Cleanup(func() { layerBucketOpener = old })
	p := filepath.Join(dir, filepath.FromSlash(config.LayerKey))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

// noLayerBucketReads fails the test if anything tries to open the project
// layer bucket at all, for a command whose contract is "never a bucket":
// unlike noProjectLayerBucket (a fake bucket that answers "not found"),
// this one proves the bucket is never reached in the first place, not just
// that it's reached safely.
func noLayerBucketReads(t *testing.T) {
	t.Helper()
	old := layerBucketOpener
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("the project layer bucket was opened")
		return nil, nil
	}
	t.Cleanup(func() { layerBucketOpener = old })
}

// layerCheckout makes the current directory a checkout of acme/other whose
// fugaro.yaml is repoYAML, with a Cloud Run job for the implicit workflow
// (config.ImplicitWorkflow), which is what a minimal file resolves to.
func layerCheckout(t *testing.T, f *cloudFixture, repoYAML string) string {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), config.ImplicitWorkflow), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	body := anchoredText(repoYAML)
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": body, "build.sh": "true\n", "test.sh": "true\n", "package-lock.json": "{}\n"})
	// A file that names a lifted key has it published as aurora's project layer,
	// which is where a run reads it now (S1, D3237): the checkout resolves as it
	// did when the key was the file's own. A test that publishes a layer of its
	// own over this one still has the last word, and one whose file names none
	// (minimalAnchored, pinnedMinimal) is untouched.
	if pinsLayer(body) != "" {
		publishedLayer(t, f, layerPins(body))
	}
	t.Chdir(dir)
	return dir
}
