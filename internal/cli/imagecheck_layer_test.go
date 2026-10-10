package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/testutil"
)

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

func TestCheckJobResolvesOverItsCopy(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	tree := cloneOf(t, map[string]string{"fugaro.yaml": minimalAnchored})
	ctx := context.Background()
	lo, err := jobLayerOptions(ctx, env.bucket, slug)
	if err != nil || !lo.NoBucket || lo.Data != nil {
		t.Fatalf("no copy: %+v, %v", lo, err)
	}
	if _, err := readHeadConfig(ctx, tree, nil, lo); err == nil || !strings.Contains(err.Error(), "must define at least one workflow") {
		t.Fatalf("a minimal file without a copy: %v", err)
	}
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	if lo, err = jobLayerOptions(ctx, env.bucket, slug); err != nil || lo.Data == nil {
		t.Fatalf("a copy: %+v, %v", lo, err)
	}
	cfg, err := readHeadConfig(ctx, tree, nil, lo)
	if err != nil || cfg.Workflows[config.ImplicitWorkflow].Commands.Test != "sh test.sh" || layerSHAOf(cfg) != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("cfg %+v, %v", cfg, err)
	}
	writeBucketFile(t, f, config.LayerCopyKey(slug), strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n", "  agent: { auth: api-key }\n  budget: {}\n", 1))
	lo, _ = jobLayerOptions(ctx, env.bucket, slug)
	if _, err := readHeadConfig(ctx, tree, nil, lo); err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("an invalid copy: %v", err)
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
