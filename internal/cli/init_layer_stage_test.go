package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// layerRig is updateRig with a project layer bucket this machine can read:
// the rig's endpoints are fakes, so a gs:// runs bucket is never read for
// real (TestMain's layerBucketOpener refuses one outright), and a fixture that
// means to publish or seed a layer points bucket_url at a directory of its
// own, the way the layer tests of the other files do.
type layerRig struct {
	*initRig
	runs string // the bucket's root: <runs>/fugaro/project-layer.yaml
}

// newLayerRig is that rig with published written to the bucket's canonical
// object key ("" publishes none), and the installation's project marker
// beside it, which is what every command that reaches the runs bucket checks.
func newLayerRig(t *testing.T, published string) *layerRig {
	t.Helper()
	r := updateRig(t)
	runs := t.TempDir()
	r.appendConfig(t, "bucket_url: file://"+runs+"\n")
	r.save(t)
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: initProjectName, GCPProject: initProject})
	if err != nil {
		t.Fatal(err)
	}
	layerRigFile(t, filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject)), string(marker))
	if published != "" {
		layerRigFile(t, filepath.Join(runs, filepath.FromSlash(config.LayerKey)), published)
	}
	return &layerRig{initRig: r, runs: runs}
}

// put publishes text as the project layer, replacing whatever is there: the
// object as another publisher (or an earlier run) left it.
func (r *layerRig) put(t *testing.T, text string) {
	t.Helper()
	layerRigFile(t, filepath.Join(r.runs, filepath.FromSlash(config.LayerKey)), text)
}

// layerRigFile writes one file of the rig's bucket, creating its directory.
func layerRigFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordLayer writes an owner's local layer file and a generation record, the
// state every installation that has published or seeded one is in.
func recordLayer(t *testing.T, text, generation string) {
	t.Helper()
	path := localLayerPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(data, []byte("layer_generation: \""+generation+"\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The red step: no stage writes a local layer file, so an installation whose
// project publishes a layer ends `fugaro update` with no file at the path
// D830 names, and the owner's first `fugaro rigor set` has nothing to edit.
func TestUpdateSeedsTheLocalLayerFromThePublishedOne(t *testing.T) {
	newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update did not seed the local layer file: %v\n%s%s", err, out, errOut)
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("the local layer file was not seeded: %v\n%s%s", rerr, out, errOut)
	}
	if string(data) != testProjectLayer {
		t.Errorf("the local layer file is not the published layer:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the seeded file's mode is %o, want 600", perm)
	}
	if all := out + errOut; !strings.Contains(all, path) || !strings.Contains(all, "the published project layer") {
		t.Errorf("the run does not say what it wrote where:\n%s", all)
	}
}
