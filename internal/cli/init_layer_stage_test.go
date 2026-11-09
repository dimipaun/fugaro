package cli

import (
	"bytes"
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
	"github.com/dimipaun/fugaro/internal/initflow"
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

// TestUpdateTakesTheJobsPausedSettingFromTheLocalLayerFile: the project's two
// schedules are the layer's (R13), and the installation stage — which runs
// before the layer stage — reads them from the local file the owner edits.
// Every way of having no value reads as no value, which the installation's
// options then take as paused: the one direction this setting may never take
// by accident is the one that starts a job.
func TestUpdateTakesTheJobsPausedSettingFromTheLocalLayerFile(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// No local layer file at all: there is nothing to read, and the run
	// leaves the schedules as the module deploys them.
	if reports, catalog := jobsPaused(lc); reports != nil || catalog != nil {
		t.Fatalf("with no local layer file the options are %v, %v, want no value at all", reports, catalog)
	}
	for _, tc := range []struct {
		what string
		text string
		// want is the two values, reports first, or nil for "the file states
		// nothing this run may take": a file that does not parse, or one with
		// a jobs: key this release does not know. The layer stage refuses
		// those a moment later, and the installation stage must not read a
		// schedule out of a file that is about to be refused.
		want []bool
	}{
		{"the layer states nothing", testProjectLayer, []bool{true, true}},
		{"the layer writes both out", testProjectLayer + "jobs:\n  reports: { paused: true }\n  catalog_check: { paused: true }\n", []bool{true, true}},
		{"the owner starts the reports job", testProjectLayer + "jobs:\n  reports: { paused: false }\n", []bool{false, true}},
		{"the owner starts the catalog check", testProjectLayer + "jobs:\n  catalog_check: { paused: false }\n", []bool{true, false}},
		{"the owner starts both", testProjectLayer + "jobs:\n  reports: { paused: false }\n  catalog_check: { paused: false }\n", []bool{false, false}},
		{"a jobs: key this release does not know", testProjectLayer + "jobs:\n  digests: { paused: false }\n", nil},
		{"a paused: that is not a bool", testProjectLayer + "jobs:\n  reports: { paused: nope }\n", nil},
	} {
		t.Run(tc.what, func(t *testing.T) {
			recordLayer(t, tc.text, "0")
			reports, catalog := jobsPaused(lc)
			if tc.want == nil {
				if reports != nil || catalog != nil {
					t.Fatalf("the options took %v, %v from a layer file this run cannot use, want no value at all", reports, catalog)
				}
				return
			}
			if reports == nil || catalog == nil {
				t.Fatalf("the layer's jobs: block gave no value: %v, %v", reports, catalog)
			}
			if *reports != tc.want[0] || *catalog != tc.want[1] {
				t.Errorf("reports paused = %v, catalog check paused = %v, want %v and %v", *reports, *catalog, tc.want[0], tc.want[1])
			}
		})
	}
}

// TestUpdateAppliesThePublishedLayersJobsSettingWithNoLocalLayerFile: the
// installation stage applies before the layer stage seeds a missing local layer
// file from the published object, so a run that read the file alone would apply
// paused — stopping the project's catalog check schedule the published layer
// started, and putting it back only on the next update. What the installation
// applies is the published layer's value, and a local file that IS there still
// decides by itself: it is the file this run publishes, so the installation
// must not carry a value the file it is about to publish does not have.
//
// The catalog check is the job that makes the value observable: the run's
// installation tfvars are the only place the schedule's state is decided.
func TestUpdateAppliesThePublishedLayersJobsSettingWithNoLocalLayerFile(t *testing.T) {
	// A catalog with one entry whose source is an inline definition: the
	// project's first repository builds it, so the installation plans the job
	// (internal/infra.CatalogCheck) and its schedule.
	catalog := "base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }\n" +
		"image_catalog:\n  entries:\n    - name: tools\n      tags: [node]\n      source: { kind: inline, inline: \"FROM scratch\\n\" }\n"
	for _, tc := range []struct {
		what string
		file bool // a local layer file that says nothing about jobs
		want bool
	}{
		{"no local layer file", false, false},
		{"a local layer file that says nothing", true, true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newLayerRig(t, testProjectLayer+"jobs:\n  catalog_check: { paused: false }\n")
			r.appendConfig(t, catalog)
			r.save(t)
			if tc.file {
				recordLayer(t, testProjectLayer, "0")
			}
			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err != nil {
				t.Fatalf("fugaro update: %v\n%s%s", err, out, errOut)
			}
			cc, _ := r.tfvars(t)["catalog_check"].(map[string]any)
			if cc == nil {
				t.Fatalf("the installation's tfvars carry no catalog_check: %v", r.tfvars(t))
			}
			if cc["paused"] != tc.want {
				t.Errorf("the installation applies paused %v, want %v", cc["paused"], tc.want)
			}
		})
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
	if row := planRowOf(all, "layer"); !strings.HasPrefix(row, markSkipped+" skipped") {
		t.Errorf("the layer stage is not skipped: %q\n%s", row, all)
	}
	if _, log := onlyLog(t, "update"); !strings.Contains(log, "no local layer file at "+localLayerPath(t)) {
		t.Errorf("the stage's whole reason is not in the log:\n%s", log)
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
	if all := out + errOut; !strings.Contains(all, markLeft+" layer") || !strings.Contains(all, "left for you (layer") {
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

// TestUpdateLayerStageRefusesAFileTheDoorsDoNotRead: the stage reads the local
// file exactly as `fugaro config publish` does (readLayerFile), so a symlink
// and a file over the layer size limit are refused here as there, and nothing
// is published from either — an update that read a symlink would publish
// whatever it points at, and one that read past the cap would publish a file no
// other door accepts.
func TestUpdateLayerStageRefusesAFileTheDoorsDoNotRead(t *testing.T) {
	for _, kind := range unreadableLayerKinds {
		t.Run(kind, func(t *testing.T) {
			r := newLayerRig(t, testProjectLayer)
			path := localLayerPath(t)
			reason := unreadableLayerFile(t, path, kind)

			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("fugaro update accepted a file the doors do not read: %v\n%s%s", err, out, errOut)
			}
			all := out + errOut + err.Error()
			if !strings.Contains(all, path) || !strings.Contains(all, reason) {
				t.Errorf("the refusal does not name the file and its problem:\n%s", all)
			}
			if got, ok := layerObject(t, r); !ok || got != testProjectLayer {
				t.Errorf("the object was written from a file the doors do not read: %q", got)
			}
		})
	}
}

// TestUpdateLayerStageSkipsAGSBucketOnFakeEndpoints: the stage's read is the
// bucket read every other command makes, so an installation whose endpoints say
// the real bucket is a fake — a config carrying a storage or no_auth override —
// skips rather than reaching past the fake, and the skip is the sentence the
// unreachable bucket gets. skipGSOnFakeEndpoints is off for every other test in
// this package (main_test.go), so it is turned back on here, as the tests of
// the guard itself do.
func TestUpdateLayerStageSkipsAGSBucketOnFakeEndpoints(t *testing.T) {
	updateRig(t)
	old := skipGSOnFakeEndpoints
	skipGSOnFakeEndpoints = true
	t.Cleanup(func() { skipGSOnFakeEndpoints = old })

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update: %v\n%s%s", err, out, errOut)
	}
	all := out + errOut
	if row := planRowOf(all, "layer"); !strings.HasPrefix(row, markSkipped+" skipped") {
		t.Errorf("the layer stage did not skip the fake-endpoint bucket: %q\n%s", row, all)
	}
	if _, log := onlyLog(t, "update"); !strings.Contains(log, "could not be read") {
		t.Errorf("the stage's whole reason is not in the log:\n%s", log)
	}
	if _, rerr := os.Stat(localLayerPath(t)); !errors.Is(rerr, os.ErrNotExist) {
		t.Errorf("a local layer file was written with no bucket read: %v", rerr)
	}
}

// TestUpdateLayerStageRefusesWhenTheObjectChangedSinceItsPlanRead: the stage
// reads the object once, when it plans, and publishes against THAT read
// (publishLayer's at). An object replaced between the two is refused rather than
// silently overwritten: with no at, publishLayer reads the bucket again at the
// write and would happily write over the publisher that landed in between.
//
// The stub is the read this run planned against — the object as it was before
// the other publisher's write — while the bucket holds the other publisher's
// bytes, which is the state the guard exists for and one a file:// bucket
// (always generation 0) can only be handed through this seam.
func TestUpdateLayerStageRefusesWhenTheObjectChangedSinceItsPlanRead(t *testing.T) {
	r := newLayerRig(t, testProjectLayer+"# another publisher's layer\n")
	recordLayer(t, localEdit, "0")
	read := layerPublishedRead
	t.Cleanup(func() { layerPublishedRead = read })
	layerPublishedRead = func(context.Context, *localcfg.Config, string) ([]byte, int64, bool, error) {
		return []byte(testProjectLayer), 0, true, nil
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if got, _ := layerObject(t, r); got != testProjectLayer+"# another publisher's layer\n" {
		t.Fatalf("the object another publisher wrote was overwritten: %q\n%s%s", got, out, errOut)
	}
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("fugaro update accepted a publish against a read that is no longer the object: %v\n%s%s", err, out, errOut)
	}
	if all := out + errOut + err.Error(); !strings.Contains(all, "another publisher changed") {
		t.Errorf("the refusal is not the one the conditional write gives:\n%s", all)
	}
}

// layerStageRig is the layer stage over a rig's own project config, driven a
// call at a time (the policy tests' own pattern): Verify has to be exercised
// after the state it watches has moved, which the door cannot be.
type layerStageRig struct {
	*initRig
	s    *layerStage
	out  *syncBuf
	runs string // the layer rig's bucket, for layerObject after a call-at-a-time run
}

// newLayerStageRig is that rig: published is the object in the runs bucket, and
// local ("" for no file) is the owner's local layer file with generation
// recorded beside it.
func newLayerStageRig(t *testing.T, published, local, generation string) *layerStageRig {
	t.Helper()
	r := newLayerRig(t, published)
	if local != "" {
		recordLayer(t, local, generation)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	// The run's own streams are the door's (newInitRun): the stage writes
	// through initRun.w, which is the run's writer and not the buffer itself,
	// so a test that gives this run a reporter sees what a person would.
	run := newInitRun(cmd, &initOptions{door: updateDoor.name})
	run.setProject(lc)
	return &layerStageRig{initRig: r.initRig, runs: r.runs, s: newLayerStage(&initEngine{r: run, lc: lc, path: r.cfg}), out: out}
}

// put publishes text as the project layer of this rig's bucket, as another
// publisher would.
func (r *layerStageRig) put(t *testing.T, text string) {
	t.Helper()
	layerRigFile(t, filepath.Join(r.runs, filepath.FromSlash(config.LayerKey)), text)
}

// TestUpdateLayerStageVerifiesTheRecordItWrote: with the bytes already agreeing
// the record is the only write the run claims, so reading it back is the only
// way to see it landed — the layer file and the object say the same thing
// whether it did or not.
func TestUpdateLayerStageVerifiesTheRecordItWrote(t *testing.T) {
	r := newLayerStageRig(t, testProjectLayer, testProjectLayer, "7")
	ctx := context.Background()

	if _, err := r.s.Apply(ctx, initflow.Env{Yes: true}); err != nil {
		t.Fatalf("apply: %v\n%s", err, r.out.String())
	}
	if err := r.s.Verify(ctx); err != nil {
		t.Fatalf("the generation this run recorded does not verify: %v", err)
	}
	// Another writer's record, or a write that never landed: the config says a
	// generation this run did not write.
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, bytes.Replace(data, []byte(`layer_generation: "0"`), []byte(`layer_generation: "5"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	err = r.s.Verify(ctx)
	if err == nil {
		t.Fatal("a record this run did not write verified")
	}
	if !strings.Contains(err.Error(), "records generation 5") {
		t.Errorf("the refusal is not the record one: %v", err)
	}
}

// TestUpdateLayerStageVerifiesThePublishItWrote: a publish's only claim is the
// object, and the local file says the same thing whether the write landed or
// not — so nothing but reading the object back can catch a publish that did
// not land, or one another publisher overwrote afterwards.
func TestUpdateLayerStageVerifiesThePublishItWrote(t *testing.T) {
	r := newLayerStageRig(t, testProjectLayer, localEdit, "0")
	ctx := context.Background()

	if _, err := r.s.Apply(ctx, initflow.Env{Yes: true}); err != nil {
		t.Fatalf("apply: %v\n%s", err, r.out.String())
	}
	if got, ok := layerObject(t, &layerRig{initRig: r.initRig, runs: r.runs}); !ok || got != localEdit {
		t.Fatalf("the local layer file was not published: %q %v", got, ok)
	}
	if err := r.s.Verify(ctx); err != nil {
		t.Fatalf("the publish this run wrote does not verify: %v", err)
	}
	// Another publisher's object, or a write that never landed: the layer file
	// still reads as this run's own edit, so only the object can say.
	r.put(t, testProjectLayer)
	err := r.s.Verify(ctx)
	if err == nil {
		t.Fatal("an object this run did not write verified")
	}
	if !strings.Contains(err.Error(), "is not the local layer file") {
		t.Errorf("the refusal is not the publish one: %v", err)
	}
}

// TestUpdateLayerStageVerifiesTheSeedItWrote: a seed writes the local file from
// the published object, so the file is the only write there is to read back —
// and a file that changed after the seed is not the object this run says it
// wrote.
func TestUpdateLayerStageVerifiesTheSeedItWrote(t *testing.T) {
	r := newLayerStageRig(t, testProjectLayer, "", "")
	ctx := context.Background()

	if _, err := r.s.Apply(ctx, initflow.Env{Yes: true}); err != nil {
		t.Fatalf("apply: %v\n%s", err, r.out.String())
	}
	if data, _ := os.ReadFile(localLayerPath(t)); string(data) != testProjectLayer {
		t.Fatalf("the seed did not write the published object to the local layer file:\n%s", data)
	}
	if err := r.s.Verify(ctx); err != nil {
		t.Fatalf("the seed this run wrote does not verify: %v", err)
	}
	if err := os.WriteFile(localLayerPath(t), []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	err := r.s.Verify(ctx)
	if err == nil {
		t.Fatal("a local file this run did not write verified")
	}
	if !strings.Contains(err.Error(), "is not the published project layer it was written from") {
		t.Errorf("the refusal is not the seed one: %v", err)
	}
}

// TestUpdateLayerStageVerifiesTheReseedItWrote: the re-seed of D854's refusal
// writes the same local file a seed does, from an object whose generation is
// not the recorded one, and Verify reads that file back the same way. The rig's
// bucket is file://, so every object reports generation 0 and a record of 3 is
// already the state the refusal exists for.
func TestUpdateLayerStageVerifiesTheReseedItWrote(t *testing.T) {
	r := newLayerStageRig(t, testProjectLayer, localEdit, "3")
	ctx := context.Background()

	if _, err := r.s.Apply(ctx, initflow.Env{Yes: true}); err != nil {
		t.Fatalf("apply: %v\n%s", err, r.out.String())
	}
	if data, _ := os.ReadFile(localLayerPath(t)); string(data) != testProjectLayer {
		t.Fatalf("the re-seed did not write the published object to the local layer file:\n%s", data)
	}
	if err := r.s.Verify(ctx); err != nil {
		t.Fatalf("the re-seed this run wrote does not verify: %v", err)
	}
	if err := os.WriteFile(localLayerPath(t), []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	err := r.s.Verify(ctx)
	if err == nil {
		t.Fatal("a local file this run did not write verified")
	}
	if !strings.Contains(err.Error(), "is not the published project layer it was written from") {
		t.Errorf("the refusal is not the re-seed one: %v", err)
	}
}

// TestUpdateLayerStageRefusesWhenTheRecordIsAheadOfThePublishedGeneration: the
// record is not the generation published now, and which side is larger does not
// matter — an older record is another machine's publish this file would revert,
// a newer one is an object this file was written from that the bucket no longer
// holds. Either way the bytes here are not what the published object descends
// from, so the run re-seeds rather than publishing them.
func TestUpdateLayerStageRefusesWhenTheRecordIsAheadOfThePublishedGeneration(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	path := localLayerPath(t)
	recordLayer(t, localEdit, "9")
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
		t.Errorf("the object was overwritten from a file written from another generation: %q", got)
	}
	all := out + errOut
	if !strings.Contains(all, "generation 9 recorded for this machine's config") ||
		!strings.Contains(all, "generation 7 published") {
		t.Errorf("the three-way state was not shown:\n%s", all)
	}
	if data, _ := os.ReadFile(path); string(data) != testProjectLayer {
		t.Errorf("the local layer file was not re-seeded from the published one:\n%s", data)
	}
}
