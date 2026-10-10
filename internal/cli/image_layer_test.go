package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// release060 is a release base image at layeredSince: the one base a build
// with a project layer may start from.
const release060 = "ghcr.io/dimipaun/fugaro-web-node:0.6.0"

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
