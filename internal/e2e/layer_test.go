package e2e

import (
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// liftPins moves the models a fixture's file names into the project layer a
// run reads: a repository's file may no longer decide them (S1, D3237), and
// config.Resolve drops them from it before the merge.
func liftPins(files map[string]string) (map[string]string, string) {
	file, layer := testutil.FixturePins(files["fugaro.yaml"], config.LiftedKeyPaths())
	files["fugaro.yaml"] = file
	return files, layer
}

// projectLayer is layer as a task carries it, nil when there is none.
func projectLayer(layer string) *task.ProjectLayer {
	if layer == "" {
		return nil
	}
	return &task.ProjectLayer{SHA256: config.LayerSum([]byte(layer)), Generation: 3, YAML: layer}
}

// taskJSON is the launcher's task file for the fixture repository, carrying
// layer as the project layer it read.
func taskJSON(t *testing.T, layer string) []byte {
	t.Helper()
	tk := map[string]any{"version": 1, "run_id": runID, "repo": "acme/app", "ref": "main", "task": "Add a feature"}
	if pl := projectLayer(layer); pl != nil {
		tk["project_layer"] = pl
	}
	b, err := json.Marshal(tk)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// seedRemote seeds the rig's remote with files, the models they name moved to
// the project layer the rig's config directory carries.
func (r *cloudRig) seedRemote(files map[string]string) {
	files, r.layer = liftPins(files)
	r.remote = testutil.NewRemote(r.t, files)
}
