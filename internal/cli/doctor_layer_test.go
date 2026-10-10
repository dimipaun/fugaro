package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// release060 is a plausible build record BaseRef for TestDoctorFlagsAStaleImage:
// imageConfigChecks never looks at BaseRef, so its exact form doesn't matter.
const release060 = "ghcr.io/dimipaun/fugaro-web-node:0.6.0"

func layerCheck(cs []doctorCheck, id string) *doctorCheck {
	for i := range cs {
		if cs[i].ID == id {
			return &cs[i]
		}
	}
	return nil
}

// resolvedLayerFixture resolves dir's fugaro.yaml over the fixture's
// published layer exactly as doctor.go does (through fugaroYAMLCheck), for
// a test to pass into doctorLayerChecks without a second bucket round trip.
func resolvedLayerFixture(t *testing.T, ctx context.Context, lc *localcfg.Config, dir string) resolvedFile {
	t.Helper()
	_, fy, rf := fugaroYAMLCheck(ctx, dir, lc)
	if fy == nil || !fy.Valid || rf == nil {
		t.Fatalf("fugaro.yaml at %s did not resolve: %+v", dir, fy)
	}
	return *rf
}

func writeRunRecord(t *testing.T, f *cloudFixture, slug, id string, pl *runstore.ProjectLayerRecord) {
	t.Helper()
	data, err := json.Marshal(runstore.Record{Version: 1, RunID: id, Repo: "acme/other", Status: runstore.StatusSucceeded,
		StartedAt: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), ProjectLayer: pl})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, "runs/"+slug+"/"+id+"/result.json", string(data))
}

// Review Focus 1.
func TestDoctorFlagsLayerDrift(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	slug := mustSlug("github", "acme/other")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" || !strings.Contains(c.Problem, "project layer aurora generation") {
		t.Fatalf("checks %+v", cs)
	}
	if c := layerCheck(cs, "project-layer-copy"); c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "has no copy") {
		t.Fatalf("no copy: %+v", cs)
	}

	writeRunRecord(t, f, slug, "20261008-090000-aaaa", nil)
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "ran without the project layer") {
		t.Fatalf("a run without the layer: %+v", c)
	}
	old := strings.Repeat("a", 64)
	writeRunRecord(t, f, slug, "20261008-091000-bbbb", &runstore.ProjectLayerRecord{SHA256: old, Generation: 1, Applied: true})
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "used project layer generation 1 (sha256 aaaaaaaaaaaa)") {
		t.Fatalf("a run on an older layer: %+v", c)
	}
	writeRunRecord(t, f, slug, "20261008-092000-cccc", &runstore.ProjectLayerRecord{SHA256: config.LayerSum([]byte(testProjectLayer)), Generation: 2, Applied: true})
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	cs = doctorLayerChecks(ctx, lc, dir, rf)
	if layerCheck(cs, "project-layer-drift") != nil || layerCheck(cs, "project-layer-copy") != nil {
		t.Fatalf("current run and copy still flagged: %+v", cs)
	}
}

// TestDoctorLayerChecksDistrustsARewrittenOrigin: a checkout whose raw
// .git/config disagrees with what git resolves for "origin" (readOrigin's
// own "not trusted by name" case) must not make doctor compute a slug and
// read another repository's layer-drift, copy-staleness or image-config
// state from the bucket.
//
// Mutation (run, restore): change `if !ok || oi.Rewritten` to `if !ok`, and
// this test fails: the project-layer-copy warning (which only a successful
// bucket read under the spoofed slug would add) appears.
func TestDoctorLayerChecksDistrustsARewrittenOrigin(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/other.git")
	// A raw .git/config naming a different host/repo than what git itself
	// resolves for "origin" (no url.<base>.insteadOf rewrites it back):
	// readOrigin reports Rewritten.
	testutil.Git(t, dir, "config", "--local", "--add", "url.https://evil.example/acme/spoofed.git.insteadOf", "https://github.com/acme/other.git")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)
	oi, ok := readOrigin(ctx, dir)
	if !ok || !oi.Rewritten {
		t.Fatalf("fixture did not produce a rewritten origin: %+v, ok=%v", oi, ok)
	}

	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if layerCheck(cs, "project-layer-copy") != nil || layerCheck(cs, "project-layer-drift") != nil {
		t.Fatalf("a rewritten origin must not reach the bucket: %+v", cs)
	}
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" {
		t.Fatalf("the layer-applies line must still show: %+v", cs)
	}
}

// TestDoctorImageConfigNamesEachDifferingSource: when a workflow's base
// comes from its profile but its image settings are overridden directly in
// the repository (or vice versa), imageConfigChecks' message must name both
// sources, never silently attribute the whole thing to just one of them.
//
// Mutation (run, restore): revert imageConfigReason to the single `from :=
// res.SourceOf(...base); if s := res.SourceOf(...image); s !=
// SourceDefault { from = s }` form, and this test fails: the message says
// only "its base comes from repo" (image's source silently overwrote
// base's), never naming profile svc at all.
func TestDoctorImageConfigNamesEachDifferingSource(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  default: { profile: svc, image: { node: \"22\" } }\n")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: config.ImplicitWorkflow, ImageConfigHash: strings.Repeat("0", 64)})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, config.ImplicitWorkflow), string(data))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-config-"+config.ImplicitWorkflow)
	if c == nil || !strings.Contains(c.Problem, "its base comes from profile svc") || !strings.Contains(c.Problem, "its image settings from repo") {
		t.Fatalf("check %+v", c)
	}
}

func TestDoctorFlagsAStaleImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: config.ImplicitWorkflow, ImageConfigHash: strings.Repeat("0", 64), BaseRef: release060})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, config.ImplicitWorkflow), string(data))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)
	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-config-"+config.ImplicitWorkflow)
	if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") || !strings.Contains(c.Fix, "fugaro image build --workflow default") {
		t.Fatalf("check %+v", c)
	}
}
