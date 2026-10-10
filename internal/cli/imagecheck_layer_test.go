package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob/gcsblob"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// forbidden403Bucket is a real gcsblob bucket whose server answers every
// GET with 403, as GCS does for a missing object when the caller lacks
// storage.objects.list (the build account's prefix-conditioned grant):
// internal/blobx/blobx_test.go's forbiddenBucket, duplicated here (it is
// unexported) to prove jobLayerOptions, not just blobx itself, treats it
// as absent.
func forbidden403Bucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":403,"message":"denied"}}`)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithEndpoint(srv.URL+"/storage/v1/"), option.WithoutAuthentication(), storage.WithJSONReads())
	if err != nil {
		t.Fatal(err)
	}
	client.SetRetry(storage.WithPolicy(storage.RetryNever))
	gb, err := gcsblob.OpenBucket(ctx, nil, "b", &gcsblob.Options{Client: client})
	if err != nil {
		t.Fatal(err)
	}
	b := &blobx.Bucket{Bucket: gb, GCSName: "b"}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func cloneOf(t *testing.T, files map[string]string) *imagecheck.GitTree {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, files)
	tree, err := imagecheck.Clone(context.Background(), imagecheck.CloneOptions{URL: remote, Branch: "main", Dir: filepath.Join(t.TempDir(), "repo")})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// jobWarnings collects jobHeadConfig's warn lines.
func jobWarnings() (warn func(string), get func() []string) {
	var lines []string
	return func(msg string) { lines = append(lines, msg) }, func() []string { return lines }
}

// Review fix (PR #258, item 1): a minimal anchored file (no workflows: of
// its own) cannot resolve without the layer, so a missing copy must say
// so clearly, naming the fix, rather than the harder to place "must
// define at least one workflow".
//
// Mutation (run, restore): in jobHeadConfig, change the `if rerr != nil`
// branch to `return nil, rerr` (the bare resolution error) instead of the
// "no project layer copy" message, and this test fails: the error no
// longer names builds/<slug>/project-layer.yaml or fugaro config publish.
func TestCheckJobResolvesOverItsCopy(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	tree := cloneOf(t, map[string]string{"fugaro.yaml": minimalAnchored})
	ctx := context.Background()
	warn, _ := jobWarnings()
	_, err := jobHeadConfig(ctx, env.bucket, slug, tree, warn)
	if err == nil || strings.Contains(err.Error(), "must define at least one workflow") ||
		!strings.Contains(err.Error(), "no project layer copy at "+config.LayerCopyKey(slug)) || !strings.Contains(err.Error(), "fugaro config publish") {
		t.Fatalf("a minimal file without a copy: %v", err)
	}
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	cfg, err := jobHeadConfig(ctx, env.bucket, slug, tree, warn)
	if err != nil || cfg.Workflows[config.ImplicitWorkflow].Commands.Test != "sh test.sh" || layerSHAOf(cfg) != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("cfg %+v, %v", cfg, err)
	}
	writeBucketFile(t, f, config.LayerCopyKey(slug), strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n", "  agent: { auth: api-key }\n  budget: {}\n", 1))
	if _, err := jobHeadConfig(ctx, env.bucket, slug, tree, warn); err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("an invalid copy: %v", err)
	}
}

// selfSufficientAnchored is an anchored file with its own full workflows:,
// which resolves fine with no project layer at all (ruling, PR #258 item
// 1b): a repository whose project simply never published one.
const selfSufficientAnchored = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n" +
	"  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"

// Review fix (PR #258, item 1): an anchored, self-sufficient file (its
// own workflows:) with no copy is a legitimate state (decision L6: a
// build without a layer never touches a copy), so the rebuild proceeds
// exactly as before -- but silently, which made a project that meant to
// publish a layer whose copy went missing indistinguishable from one that
// never published at all. jobHeadConfig must warn, by name, every time.
//
// Mutation (run, restore): delete the `warn(fmt.Sprintf(...))` call in
// jobHeadConfig's ErrNotExist branch, and this test fails: cfg still
// resolves, but no warning is recorded.
func TestCheckJobWarnsWhenASelfSufficientFileHasNoCopy(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	tree := cloneOf(t, map[string]string{"fugaro.yaml": selfSufficientAnchored})
	ctx := context.Background()
	warn, warnings := jobWarnings()
	cfg, err := jobHeadConfig(ctx, env.bucket, slug, tree, warn)
	if err != nil || cfg.Workflows["app"].Commands.Test != "sh test.sh" || layerSHAOf(cfg) != "" {
		t.Fatalf("cfg %+v, %v", cfg, err)
	}
	ws := warnings()
	if len(ws) != 1 || !strings.Contains(ws[0], "no project layer copy at "+config.LayerCopyKey(slug)) || !strings.Contains(ws[0], "fugaro config publish") {
		t.Fatalf("warnings = %v", ws)
	}
}

// unanchoredLegacy has no gcp_project: at all: no project layer applies to
// it (decision L9), old-style, full workflows: of its own.
const unanchoredLegacy = "version: 1\nproject: legacy\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n" +
	"  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"

// Review fix (PR #258, item 2): jobLayerOptions must run only for an
// anchored repository. An unanchored repository's copy path (which does
// not even apply to it) must never be touched at all, so a copy broken
// for an unrelated reason -- here, oversized -- must not turn into a new
// failure mode for a repository that never needed a layer, byte for byte
// unchanged from before this file was ever anchored.
//
// Mutation (run, restore): in jobHeadConfig, remove the
// `if !isAnchoredFile(data) { ... }` early return (always call
// jobLayerOptions), and this test fails: the oversized copy now fails the
// whole check.
func TestCheckJobNeverTouchesTheCopyWhenUnanchored(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	writeBucketFile(t, f, config.LayerCopyKey(slug), strings.Repeat("x", config.LayerMaxBytes+1))
	tree := cloneOf(t, map[string]string{"fugaro.yaml": unanchoredLegacy})
	ctx := context.Background()
	warn, warnings := jobWarnings()
	cfg, err := jobHeadConfig(ctx, env.bucket, slug, tree, warn)
	if err != nil || cfg.Workflows["app"].Commands.Test != "sh test.sh" || len(warnings()) != 0 {
		t.Fatalf("cfg %+v, err %v, warnings %v", cfg, err, warnings())
	}
}

// Review fix (PR #258, item 5): jobLayerOptions uses the ordinary (not
// strict) read, so a 403 from the build account's own prefix-conditioned
// grant reads as "no copy" (blobx.TestReadTreatsForbiddenAsAbsent), not a
// failure: the same real gcsblob driver a live GCS 403 would reach.
func TestJobLayerOptionsTreatsForbiddenAsNoCopy(t *testing.T) {
	ctx := context.Background()
	b := forbidden403Bucket(t)
	lo, err := jobLayerOptions(ctx, b, "acme-other-deadbeef")
	if err != nil || !lo.NoBucket || lo.Data != nil {
		t.Fatalf("a 403 copy read: %+v, %v", lo, err)
	}
}

// Review fix (PR #258, item 5): an oversized copy is a real, present,
// published object (unlike a 403 or a missing one), so it is always an
// error, and republishing it within the limit (fugaro config publish) is
// the actual fix -- unlike a 403 or a generic unreadable error, which
// name no such advice.
//
// Mutation (run, restore): drop the "run fugaro config publish..." suffix
// from jobLayerOptions's ErrTooLarge branch, and this test fails.
func TestJobLayerOptionsOversizeNamesThePublishFix(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	writeBucketFile(t, f, config.LayerCopyKey(slug), strings.Repeat("x", config.LayerMaxBytes+1))
	_, err := jobLayerOptions(context.Background(), env.bucket, slug)
	if err == nil || !strings.Contains(err.Error(), "over the") || !strings.Contains(err.Error(), "fugaro config publish") {
		t.Fatalf("err = %v", err)
	}
}

// TestLocalImageCheckReadsHeadStrictly: fugaro image check (run from a
// checkout, no --job) passes readHeadConfig the operator's own lc and a
// strict layerOptions{} (runImageCheckLocal), unlike the check job's
// NoBucket copy: it reads the project's own published layer fresh, the
// same way fugaro validate or fugaro run would, not the job's lenient,
// cache-only fallback.
//
// Mutation (run, restore): change runImageCheckLocal's
// readHeadConfig(ctx, tree, lc, layerOptions{}) call to pass nil and
// layerOptions{Lenient: true} instead, and this test fails: with no cache
// populated, the minimal file can no longer resolve its implicit workflow
// from the published layer.
func TestLocalImageCheckReadsHeadStrictly(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	env := fileEnv(t, f)
	tree := cloneOf(t, map[string]string{"fugaro.yaml": minimalAnchored})
	ctx := context.Background()
	cfg, err := readHeadConfig(ctx, tree, env.lc, layerOptions{})
	if err != nil || cfg.Workflows[config.ImplicitWorkflow].Commands.Test != "sh test.sh" {
		t.Fatalf("cfg %+v, %v", cfg, err)
	}
}
