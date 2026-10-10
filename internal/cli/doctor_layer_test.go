package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
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

	cs := doctorLayerChecks(ctx, lc, dir)
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" || !strings.Contains(c.Problem, "project layer aurora generation") {
		t.Fatalf("checks %+v", cs)
	}
	if c := layerCheck(cs, "project-layer-copy"); c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "has no copy") {
		t.Fatalf("no copy: %+v", cs)
	}

	writeRunRecord(t, f, slug, "20261008-090000-aaaa", nil)
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "ran without the project layer") {
		t.Fatalf("a run without the layer: %+v", c)
	}
	old := strings.Repeat("a", 64)
	writeRunRecord(t, f, slug, "20261008-091000-bbbb", &runstore.ProjectLayerRecord{SHA256: old, Generation: 1, Applied: true})
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "used project layer generation 1 (sha256 aaaaaaaaaaaa)") {
		t.Fatalf("a run on an older layer: %+v", c)
	}
	writeRunRecord(t, f, slug, "20261008-092000-cccc", &runstore.ProjectLayerRecord{SHA256: config.LayerSum([]byte(testProjectLayer)), Generation: 2, Applied: true})
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	cs = doctorLayerChecks(ctx, lc, dir)
	if layerCheck(cs, "project-layer-drift") != nil || layerCheck(cs, "project-layer-copy") != nil {
		t.Fatalf("current run and copy still flagged: %+v", cs)
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
	c := layerCheck(doctorLayerChecks(context.Background(), fileEnv(t, f).lc, dir), "image-config-"+config.ImplicitWorkflow)
	if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") || !strings.Contains(c.Fix, "fugaro image build --workflow default") {
		t.Fatalf("check %+v", c)
	}
}
