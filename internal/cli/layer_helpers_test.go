package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// test is about. The keys are pinnedAgent's, moved, so the values the tests read
// are the ones they always read.
//
// The reviewer is named beside agent.model rather than left to it: a level's
// model is the role's own slot where it has one and agent.model below that
// (config's levelDef), so a layer carrying agent.model alone puts level 1 and
// level 2 on one model and every launch of such a layer is refused by D721's
// guard — a repository whose first-line and senior review would run the same
// model.
const testProjectLayerAgent = "  agent: { auth: api-key, " + testProjectLayerRoles + " }\n"

// testProjectLayerRoles is the role keys testProjectLayerAgent names, for a
// test that writes an agent key of its own beside them (layerWithRoles).
const testProjectLayerRoles = "model: claude-sonnet-5-5, models: { reviewer: claude-opus-5, background: claude-haiku-4-5 }"

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

// layerWithRoles is testProjectLayer whose agent block carries extra beside the
// keys testProjectLayerAgent writes: the layer a test that judges a lifted
// agent key publishes. extra names keys only — auth: api-key and the three
// roles are already there, and a second models: key would not decode.
func layerWithRoles(extra string) string {
	return layerWithAgent("auth: api-key, " + testProjectLayerRoles + ", " + extra)
}

// layerWithAgent is testProjectLayer whose agent block is body's own keys: the
// layer a test publishes where the roles themselves are the point — a layer
// naming the coder and the reviewer the same model, say. body carries auth:
// api-key and whatever models it means.
func layerWithAgent(body string) string {
	return strings.Replace(testProjectLayer, testProjectLayerAgent, "  agent: { "+body+" }\n", 1)
}

// noneModelLayer is the project layer that writes a level's model as the
// literal none, for the level named (l2, l3): agent.levels.lN.model is a lifted
// key, so after S1 (D3237) a repository's file can no longer write the switch
// and the layer is the only text the gate that refuses an image reading the
// literal as a model ID is judged on.
func noneModelLayer(level string) string {
	return layerWithRoles("levels: { " + level + ": { model: none } }")
}

// anchorCheckoutFile gives the working directory's fugaro.yaml the
// gcp_project: line its project layer is matched by, for a test whose launch
// must resolve over the layer the fixture published: findLayer applies a layer
// only to a file naming both the same project and the same GCP project.
func anchorCheckoutFile(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile("fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("fugaro.yaml", []byte(anchoredText(string(b))), 0o644); err != nil {
		t.Fatal(err)
	}
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
// --offline reads. The GCP project and the bucket are the layer's own, which is
// what findLayer matches a cache entry on.
func cacheLayer(t *testing.T, project, text string) {
	t.Helper()
	isolateCache(t)
	cacheLayerNow(t, project, text)
}

// cacheLayerNow is cacheLayer without a cache directory of its own: for a rig
// whose XDG_CACHE_HOME is already the test's (the doctor rig's).
func cacheLayerNow(t *testing.T, project, text string) {
	t.Helper()
	gcp := gcpProjectNamed(text)
	if err := localcfg.SaveLayerCache(os.Getenv, project, localcfg.SharedCacheEntry{
		GCPProject: gcp,
		Bucket:     "fugaro-runs-" + gcp,
		CheckedAt:  time.Now(),
		YAML:       text,
	}); err != nil {
		t.Fatal(err)
	}
}

// gcpProjectNamed is the gcp_project: line of a project layer's text: the GCP
// project the layer and the cache entry keyed to it are matched on.
func gcpProjectNamed(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(l, "gcp_project:"); ok {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
	}
	return testutil.PinsGCPProject
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
	isolateCache(t)
	attachLayerHere(t, project, text)
}

// attachLayerHere is attachLayer without a cache directory of its own: for a
// rig whose XDG_CACHE_HOME is already the test's (the doctor rig's), where a
// fresh one would take the rig's own cache out from under it.
func attachLayerHere(t *testing.T, project, text string) {
	t.Helper()
	cacheLayerNow(t, project, text)
	if path, err := localcfg.ProjectPath(os.Getenv, project); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			cfg := strings.Replace(string(b),
				"runs_bucket: unused-bucket", "runs_bucket: fugaro-runs-"+gcpProjectNamed(text), 1)
			if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	bucketFor := layerBucket(t)
	for _, url := range []string{"gs://fugaro-runs-" + gcpProjectNamed(text), layerBucketURL(t, project)} {
		if url == "" {
			continue
		}
		p := filepath.Join(bucketFor(url), filepath.FromSlash(config.LayerKey))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// layerBucket serves the test its own buckets through layerBucketOpener: one
// directory per URL, under a root the whole test shares. A test that attaches a
// layer for two projects — the file's own, then another's — reads each one's
// object; one directory for both would serve the last attachment to every
// reader, whatever project it belongs to.
func layerBucket(t *testing.T) func(url string) string {
	t.Helper()
	if any, ok := layerBuckets.Load(t.Name()); ok {
		return any.(func(string) string)
	}
	root := t.TempDir()
	old := layerBucketOpener
	sync := func(ctx context.Context, url string) (*blobx.Bucket, error) {
		dir := filepath.Join(root, urlKey(url))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		return blobx.Open(ctx, "file://"+dir)
	}
	dirFor := func(url string) string { return filepath.Join(root, urlKey(url)) }
	layerBuckets.Store(t.Name(), dirFor)
	layerBucketOpener = sync
	t.Cleanup(func() {
		layerBucketOpener = old
		layerBuckets.Delete(t.Name())
	})
	return dirFor
}

// layerBuckets is layerBucket's per-test bucket roots, by test name. Every
// layerBucketOpener replacement in one test serves the same root, so the two
// exports are the same helper and the second attachment does not shadow the
// first.
var layerBuckets sync.Map

// urlKey is url as a directory name.
func urlKey(url string) string {
	return strings.NewReplacer("/", "_", ":", "_").Replace(url)
}

// layerBucketURL is the URL the selected project config's own bucket resolves
// to, "" when there is no such config or it names no bucket: findLayer reads a
// layer over a matching installation at this URL rather than at the default
// gs:// one (decision L9).
func layerBucketURL(t *testing.T, project string) string {
	t.Helper()
	path, err := localcfg.ProjectPath(os.Getenv, project)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "bucket_url:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
	body := publishFilePins(t, f, repoYAML)
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": body, "build.sh": "true\n", "test.sh": "true\n", "package-lock.json": "{}\n"})
	t.Chdir(dir)
	return dir
}

// publishFilePinsHere publishes a repository file's own lifted keys as aurora's
// project layer in this machine's cache — the copy a read that reaches no
// bucket loads (modelsLayer, decision L13) — and returns the file anchored to
// the project that layer names, which is what makes it apply. For a rig whose
// XDG_CACHE_HOME is already the test's. A file that names no lifted key comes
// back as it was.
func publishFilePinsHere(t *testing.T, body string) string {
	t.Helper()
	if pins := pinsLayer(body); pins != "" {
		cacheLayerNow(t, "aurora", pins)
	}
	return anchoredText(body)
}

// publishFilePins publishes a repository file's own lifted keys as the fixture's
// project layer, which is where a run reads them now (S1, D3237), and returns
// the file anchored to the project that layer names — the anchor is what makes
// it apply. A file that names no lifted key (minimalAnchored, pinnedMinimal)
// comes back as it was, and a test that publishes a layer of its own afterwards
// still has the last word.
func publishFilePins(t *testing.T, f *cloudFixture, body string) string {
	t.Helper()
	if pinsLayer(body) == "" {
		return body
	}
	publishedLayer(t, f, layerPins(body))
	return anchoredText(body)
}
