package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// layerObject is the project layer as the rig's bucket holds it right now, and
// whether it is there at all.
func layerObject(t *testing.T, r *layerRig) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.runs, filepath.FromSlash(config.LayerKey)))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// localEdit is testProjectLayer with one key that is not executable changed:
// the owner's own edit, which publishes without the executable gate.
const localEdit = `version: 1
project: aurora
gcp_project: proj-1234
governance: { review_ratio: 20 }
defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    commands: { build: sh build.sh, test: sh test.sh }
default_profile: svc
`

// executableEdit is testProjectLayer with a command changed: what the
// executable-key gate exists for.
const executableEdit = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    commands: { build: sh build.sh, test: sh test-all.sh }
default_profile: svc
`

// TestUpdateSeedWritesNothingWhenTheLocalFileExists: an owner's file is never
// overwritten by the seed, even when it differs from the published object —
// that is the publish half's job, after a diff the owner is shown.
func TestUpdateSeedWritesNothingWhenTheLocalFileExists(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	recordLayer(t, localEdit, "0")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update refused an owner's own layer file: %v\n%s%s", err, out, errOut)
	}
	if data, _ := os.ReadFile(path); string(data) != localEdit {
		t.Errorf("the seed overwrote the owner's layer file:\n%s", data)
	}
	if got, ok := layerObject(t, r); !ok || got != localEdit {
		t.Errorf("the owner's file was not published: %q %v\n%s%s", got, ok, out, errOut)
	}
}

// TestUpdateSeedWritesNothingWhenNoLayerIsPublished: the first update of a
// project that has never published a layer leaves no file behind — there is
// nothing to seed it from, and an empty one would be a layer the owner's next
// edit publishes by accident.
func TestUpdateSeedWritesNothingWhenNoLayerIsPublished(t *testing.T) {
	r := newLayerRig(t, "")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s%s", err, out, errOut)
	}
	if _, rerr := os.Stat(localLayerPath(t)); !errors.Is(rerr, os.ErrNotExist) {
		t.Errorf("a local layer file was written with nothing to seed it from: %v", rerr)
	}
	if _, ok := layerObject(t, r); ok {
		t.Error("an object was published with nothing published before it")
	}
}

// TestUpdatePublishesTheProjectLayerWhenItIsUnpublished: the local file is the
// owner's and differs from the object, so this run publishes it — the diff is
// shown and the fan-out runs — and the file itself is left exactly as it is.
func TestUpdatePublishesTheProjectLayerWhenItIsUnpublished(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	recordLayer(t, localEdit, "0")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update did not publish the local layer file: %v\n%s%s", err, out, errOut)
	}
	if got, ok := layerObject(t, r); !ok || got != localEdit {
		t.Errorf("the object is not the local layer file: %q %v", got, ok)
	}
	all := out + errOut
	if !strings.Contains(all, "replacing the project layer") || !strings.Contains(all, "base_branch") {
		t.Errorf("the diff of what the publish replaces was not shown:\n%s", all)
	}
	slug := mustSlug("bitbucket", "acme/sandbox")
	if _, err := os.Stat(filepath.Join(r.runs, filepath.FromSlash(config.LayerCopyKey(slug)))); err != nil {
		t.Errorf("the fan-out did not write the repository's copy: %v\n%s", err, all)
	}
	if data, _ := os.ReadFile(path); string(data) != localEdit {
		t.Errorf("the publish rewrote the local layer file:\n%s", data)
	}
}

// TestUpdateLayerStageSkipsWithNoLocalFileAndNoPublishedLayer: the one state
// with nothing to seed from and nothing to publish is said in a sentence, and
// the stage is skipped rather than done.
func TestUpdateLayerStageSkipsWithNoLocalFileAndNoPublishedLayer(t *testing.T) {
	newLayerRig(t, "")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s%s", err, out, errOut)
	}
	all := out + errOut
	if !strings.Contains(all, "[skipped]    layer") {
		t.Errorf("the layer stage is not skipped:\n%s", all)
	}
	if !strings.Contains(all, "no local layer file at "+localLayerPath(t)) {
		t.Errorf("the stage does not say why it did nothing:\n%s", all)
	}
}

// TestUpdateLayerStageRefusesAFileThatDoesNotParse: a local layer file this
// Fugaro cannot read is the owner's to fix, and nothing is published from it.
func TestUpdateLayerStageRefusesAFileThatDoesNotParse(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	recordLayer(t, "version: 1\nproject: aurora\ngcp_project: proj-1234\nnonsense_key: 1\n", "0")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("fugaro update accepted a layer file that does not parse: %v\n%s%s", err, out, errOut)
	}
	all := out + errOut + err.Error()
	if !strings.Contains(all, path) || !strings.Contains(all, "nonsense_key") {
		t.Errorf("the refusal does not name the file and its problem:\n%s", all)
	}
	if got, ok := layerObject(t, r); !ok || got != testProjectLayer {
		t.Errorf("the object was written from a file that does not parse: %q", got)
	}
}

// TestUpdateLayerStageRefusesExecutableChangesWithoutTheFlag: a change to a
// command is what the executable-key gate exists for, and update carries no
// flag to acknowledge it — the same banner and refusal config publish gives.
func TestUpdateLayerStageRefusesExecutableChangesWithoutTheFlag(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	recordLayer(t, executableEdit, "0")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("fugaro update published a command change without the gate: %v\n%s%s", err, out, errOut)
	}
	all := out + errOut + err.Error()
	if !strings.Contains(all, "=== EXECUTABLE CHANGES") || !strings.Contains(all, "--executable-changes") {
		t.Errorf("the refusal is not the one the gate gives:\n%s", all)
	}
	if got, ok := layerObject(t, r); !ok || got != testProjectLayer {
		t.Errorf("the object was written past the executable gate: %q", got)
	}
}

// TestUpdateLayerStageAppliesNothingInAnAgentSessionEvenWithYes: the project
// layer changes what every repository of the project runs, so it is a cloud
// write like any other and a coding agent's session never reaches it.
func TestUpdateLayerStageAppliesNothingInAnAgentSessionEvenWithYes(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	t.Setenv(agentMarkers[0], "1")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil {
		t.Fatalf("the run succeeded in an agent's session:\n%s%s", out, errOut)
	}
	if _, rerr := os.Stat(localLayerPath(t)); !errors.Is(rerr, os.ErrNotExist) {
		t.Errorf("the local layer file was seeded in an agent's session: %v", rerr)
	}
	if got, ok := layerObject(t, r); !ok || got != testProjectLayer {
		t.Errorf("the object was written in an agent's session: %q", got)
	}
	if all := out + errOut; !strings.Contains(all, "layer") || !strings.Contains(all, "needs you") {
		t.Errorf("the run does not say the layer stage is left for a person:\n%s", all)
	}
}

// TestUpdateLayerStageRefusesWhenTheGenerationMovedSinceTheSeed: a publisher
// that lands between this run's read and its write is refused, never
// overwritten — the write carries the generation the read returned, so the
// guard needs no record and holds on a first run as on any other.
func TestUpdateLayerStageRefusesWhenTheGenerationMovedSinceTheSeed(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	recordLayer(t, localEdit, "0")
	race := layerPublishRace
	t.Cleanup(func() { layerPublishRace = race })
	layerPublishRace = func(context.Context, *blobx.Bucket) {
		r.put(t, testProjectLayer+"# someone else's publish\n")
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("fugaro update overwrote a publisher it never read: %v\n%s%s", err, out, errOut)
	}
	if got, _ := layerObject(t, r); got != testProjectLayer+"# someone else's publish\n" {
		t.Errorf("the object another publisher wrote was overwritten: %q", got)
	}
	if data, _ := os.ReadFile(path); string(data) != localEdit {
		t.Errorf("the local layer file changed on a refused publish:\n%s", data)
	}
	if all := out + errOut + err.Error(); !strings.Contains(all, "another publisher changed") {
		t.Errorf("the refusal is not the one the conditional write gives:\n%s", all)
	}
}

// TestUpdateLayerStageRefusesWhenThePublishedGenerationIsNewer: the record says
// this machine's file was written from an older generation than the published
// object's, so publishing would replace someone else's newer layer with it. The
// run shows the three-way state and re-seeds the local file under its own
// confirmation, and the object is not written.
func TestUpdateLayerStageRefusesWhenThePublishedGenerationIsNewer(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	recordLayer(t, localEdit, "3")
	read := layerPublishedRead
	t.Cleanup(func() { layerPublishedRead = read })
	layerPublishedRead = func(context.Context, *localcfg.Config, string) ([]byte, int64, bool, error) {
		return []byte(testProjectLayer), 7, true, nil
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s%s", err, out, errOut)
	}
	if got, _ := layerObject(t, r); got != testProjectLayer {
		t.Errorf("the object was overwritten from the older local file: %q", got)
	}
	all := out + errOut
	if !strings.Contains(all, "generation 3 recorded for this machine's config") ||
		!strings.Contains(all, "generation 7 published") ||
		!strings.Contains(all, config.LayerSum([]byte(testProjectLayer))) ||
		!strings.Contains(all, config.LayerSum([]byte(localEdit))) {
		t.Errorf("the three-way state was not shown:\n%s", all)
	}
	if data, _ := os.ReadFile(path); string(data) != testProjectLayer {
		t.Errorf("the local layer file was not re-seeded from the published one:\n%s", data)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) == 0 {
		t.Errorf("the file the re-seed replaced was not backed up: %v %v", backups, err)
	}
}

// TestUpdateLayerStageAdoptsTheGenerationWhenNothingIsRecorded: an installation
// that carries no layer_generation — every installation this release reaches —
// publishes as it would and records the generation it read, saying so, rather
// than refusing once to force a record.
func TestUpdateLayerStageAdoptsTheGenerationWhenNothingIsRecorded(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update refused an installation with no record: %v\n%s%s", err, out, errOut)
	}
	if got, _ := layerObject(t, r); got != localEdit {
		t.Errorf("the local layer file was not published: %q", got)
	}
	if all := out + errOut; !strings.Contains(all, "adopts") {
		t.Errorf("the run does not say it adopted the generation:\n%s", all)
	}
	cfg, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "layer_generation:") {
		t.Errorf("the generation this run published was not recorded:\n%s", cfg)
	}
}

// TestInstallationAtThePreviousVersionConverges: the HARD RULE's own test. An
// installation that predates the record — the project's layer was published
// before `layer_generation` existed, and the local config carries neither it
// nor a file — ends `fugaro update` with the layer still published and the
// generation adopted, and not refused for the record it does not have.
func TestInstallationAtThePreviousVersionConverges(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testProjectLayer), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update left an installation at the previous version behind: %v\n%s%s", err, out, errOut)
	}
	if got, ok := layerObject(t, r); !ok || got != testProjectLayer {
		t.Errorf("the local layer was not published: %q %v\n%s%s", got, ok, out, errOut)
	}
	if data, _ := os.ReadFile(path); string(data) != testProjectLayer {
		t.Errorf("the local layer file was not left as it is:\n%s", data)
	}
}
