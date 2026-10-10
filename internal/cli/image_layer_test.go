package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// release060 (a build with a project layer may start from it) is defined in
// run_layer_test.go.

// Review Focus 3.
func TestImageBuildRefusesOldBaseWithLayer(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	l, ps := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	slug := mustSlug("github", "acme/other")
	ctx := context.Background()
	if _, err := prepareLayerCopy(ctx, env.bucket, slug, l, "ghcr.io/dimipaun/fugaro-web-node:0.5.1"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro image refresh") {
		t.Fatalf("old base: %v", err)
	}
	if bucketText(t, f, config.LayerCopyKey(slug)) != "" {
		t.Fatal("a copy was written for a refused build")
	}
	sha, err := prepareLayerCopy(ctx, env.bucket, slug, l, release060)
	if err != nil || sha != l.SHA256 || bucketText(t, f, config.LayerCopyKey(slug)) != testProjectLayer {
		t.Fatalf("0.6.0 base: %q, %v", sha, err)
	}
	if sha, err := prepareLayerCopy(ctx, env.bucket, slug, l, release060); err != nil || sha != l.SHA256 {
		t.Fatalf("an unchanged copy: %q, %v", sha, err)
	}
}

func TestCloudBuildSpecCarriesTheLayer(t *testing.T) {
	l, _ := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	cfg, _, ps := config.Resolve([]byte(minimalAnchored), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	rs := infra.RepoSpec{Slug: "s", Provider: "github", Workflows: map[string]infra.WorkflowSpec{config.ImplicitWorkflow: {}}}
	spec, err := cloudBuildSpec(rs, cfg, config.ImplicitWorkflow, release060, "E2_HIGHCPU_8", "gs://fugaro-runs-proj-1234")
	if err != nil || spec.ProjectLayerSHA256 != l.SHA256 {
		t.Fatalf("spec %+v, %v", spec, err)
	}
}

// Review fix (2): a missing copy is not "reading ... failed" with no next
// step; it names the fix (fugaro config publish), the same as a repository
// that was never published to in the first place.
//
// Mutation (run, restore): drop readLayerCopy's errors.Is(err,
// blobx.ErrNotExist) case (fall through to the generic remote wrap), and
// this test fails: the message no longer names fugaro config publish.
func TestReadLayerCopyMissingSaysPublish(t *testing.T) {
	f := newCloudFixture(t)
	slug := mustSlug("github", "acme/other")
	_, err := readLayerCopy(context.Background(), f.bucket, slug, strings.Repeat("0", 64))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the project layer copy "+config.LayerCopyKey(slug)+" is missing: run fugaro config publish, then the build again") {
		t.Fatalf("a missing copy: %v", err)
	}
}

// Review fix (2): the daily check job's rebuilds read the copy without ever
// writing it (Task 15), so a mismatch there is not fixed by "run the build
// again" alone: the copy itself may be the stale thing, which only fugaro
// config publish (re-copying every repository) corrects.
//
// Mutation (run, restore): drop the ", or run fugaro config publish if it
// is stale" suffix from the mismatch message, and this test fails.
func TestReadLayerCopyMismatchSuggestsPublish(t *testing.T) {
	f := newCloudFixture(t)
	slug := mustSlug("github", "acme/other")
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	_, err := readLayerCopy(context.Background(), f.bucket, slug, strings.Repeat("0", 64))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "run the build again, or run fugaro config publish if it is stale") {
		t.Fatalf("a mismatched copy: %v", err)
	}
}

func TestImageRenderReadsTheCopyAndChecksItsSum(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	sum := config.LayerSum([]byte(testProjectLayer))
	out, _, err := execute(t, "image", "render", "--layer-bucket", f.bucket, "--layer-slug", slug, "--layer-sha256", sum)
	if err != nil || !strings.Contains(out, "FROM") {
		t.Fatalf("out %q, err %v", out, err)
	}
	_, _, err = execute(t, "image", "render", "--layer-bucket", f.bucket, "--layer-slug", slug, "--layer-sha256", strings.Repeat("0", 64))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "changed while the build was queued") {
		t.Fatalf("a stale sum: %v", err)
	}
}

// Review fix (1), security (decision L16): fugaro init's first builds are a
// billable Cloud Build submission, never lenient like the repository and
// anchor stages' own earlier read (loadCheckoutConfigAt, read with lc==nil,
// before lc itself is even loaded). A bucket that cannot be read, with
// nothing cached, must refuse resolveForBuild outright, before
// r.offerBuilds (and buildImages, and submitAndWait, and Submit) is ever
// reached — the same contract fugaro run's own
// TestRunRefusesWhenTheLayerBucketIsUnreadable pins for launches, and
// TestRefreshReloadRefusesWhenTheLayerBucketIsUnreadable pins for fugaro
// image refresh.
//
// resolveForBuild always takes a real, non-nil lc (its one caller,
// repoEngine, already has one by the time it calls it): unlike the
// repository stage's own loadCheckoutConfigAt(ctx, dir) call, it can never
// be starved into findLayer's lc==nil, cache-only mode, so a stale cached
// layer cannot satisfy it while the bucket is reachable either (that class
// of bug is covered for fugaro image refresh's own refreshReload by
// TestRefreshReloadIgnoresAStaleCachedLayer, which uses the identical
// mechanism, loadCheckoutResolved with a strict layerOptions{}).
//
// Mutation (run, restore): change resolveForBuild's loadCheckoutResolved
// call to pass layerOptions{Lenient: true} instead of layerOptions{}, and
// this test fails: a lenient read tolerates the unreadable bucket as
// "unknown" instead of refusing.
func TestResolveForBuildRefusesWhenTheLayerBucketIsUnreadable(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	root := layerCheckout(t, f, anchoredRefreshYAML)
	f.appendConfig(t, "  acme/other: { provider: github, base_branch: main, workflows: ["+config.ImplicitWorkflow+"], github_app_id: \"12345\" }\n")
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	if _, err := resolveForBuild(context.Background(), root, lc); err == nil {
		t.Fatal("resolved despite an unreadable project layer bucket with nothing cached")
	}
}
