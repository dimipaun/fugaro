package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/runner"
)

// update_craft_model_test.go is MC17 of docs/model-crafts/activation-plan.md:
// the converge test the HARD RULE requires for the model half, and the whole of
// what that half owes it — no migration code at all.
//
// **The model half needs no migration (D1666).** A craft that sets no model
// resolves exactly as it did before the crafts' model half was read
// (`agent.models.<role>` are still rungs 4 and 5, D1662), and a craft that sets
// one was written by the owner, so `fugaro update` has nothing to write for it:
// no key is renamed, no model is backfilled and there is no second command. The
// red step this file was written against is MC12's own write
// (internal/runner/craftmodel.go's WriteCraftRoles): before it, a run's stage
// resolved its model from `agent.models.<role>` alone and a craft's model
// reached no stage at all, so the assertions below had nothing to read.
//
// **What the tests pin is the other half of that sentence**, the one the HARD
// RULE is about: an installation at the previous version converges with
// `fugaro update` alone, and a run before and after resolves the same model and
// the same credential (F17, D1666) — the update may not move either. The
// fixture is that installation: the published node of the release behind
// (testdata/craft_model_node.json, version 7 with the account keys this
// machine's config states), the project layer its project published
// (testdata/craft_model_layer.yaml, whose `defaults.agent.crafts.coder.model` is
// the craft key the red step names), a checkout anchored to that project, and
// the previous release's own account keys in the installation's config
// (migratedAccounts), which is the half MC4's migration does write.

// The three documents of the fixture, read as the release behind left them and
// never spelled in Go: a previous-version fixture is a document, and one
// assembled at test time from this tree's own helpers would converge whatever
// this tree happens to write today.
const (
	craftModelNodeFixture     = "testdata/craft_model_node.json"
	craftModelLayerFixture    = "testdata/craft_model_layer.yaml"
	craftModelCheckoutFixture = "testdata/craft_model_checkout.yaml"
)

// craftModelModel is the model the fixture's project layer names for the coder
// craft — and, on the run itself, the model the plan, implement and fix stages
// call out on.
const craftModelModel = "claude-opus-4-1"

// craftModelStale is what makes the fixture an installation of the release
// behind rather than a current one: the deployed security rules carry the
// previous generation's marker and the checkout carries that release's plugin
// pin. The published node is version 7, one below this binary's, so the same
// run's policy stage carries it forward as well (the rig's own reading of
// v7Node: MC4's fixture, whose account keys this one states).
var craftModelStale = []string{"policy-rules-skew", "repo-plugin-pin"}

// craftModelDocuments is the fixture's three documents as they were read.
//
// They are read before the rig is built, and carried rather than re-read: the
// rig chdirs into the checkout it made (t.Chdir), so a testdata path spelled
// relatively stops naming this package's directory the moment the rig exists.
type craftModelDocuments struct {
	node     map[string]any
	checkout string
	layer    []byte
}

// craftModelRig is the installation the release behind left: the converge rig
// over the fixture's checkout, with the previous release's account keys appended
// to its config and the project layer its project published written where a run
// of the repository reads it — the runs bucket the config names, which is the
// same seam the rig's repository stage and the layer's own resolution read.
//
// The layer object is written and the *local* layer file deliberately is not:
// the file is the owner's editable copy, and a run resolves nothing from it —
// what a run reads is the published object, which is what makes this fixture
// say something about a run rather than about this machine.
func craftModelRig(t *testing.T) (*convergeRig, craftModelDocuments) {
	t.Helper()
	docs := craftModelDocuments{
		checkout: string(readFile(t, craftModelCheckoutFixture)),
		layer:    readFile(t, craftModelLayerFixture),
	}
	if err := json.Unmarshal(readFile(t, craftModelNodeFixture), &docs.node); err != nil {
		t.Fatalf("%s is not the published node it stands for: %v", craftModelNodeFixture, err)
	}
	r := newConvergeRigYAML(t, docs.node, docs.checkout)
	r.appendConfig(t, migratedAccounts)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// copy this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)

	if err := os.WriteFile(r.craftModelLayerObject(t), docs.layer, 0o644); err != nil {
		t.Fatalf("publishing the fixture's project layer: %v", err)
	}
	return r, docs
}

// craftModelRun is what a run of the fixture's repository resolves for the
// implement stage's model call: the model the call goes out on, and the
// credential or pool it leaves on.
type craftModelRun struct {
	Model  string
	Access string
}

// craftModelARunResolves reads that answer the way the run itself resolves it:
// the configuration the checkout's file resolves over the layers that apply to
// it, the crafts the run's own layer walk answers (craftLayers, whose launcher
// copy is launchCraftLayers), and MC12's write performed on that configuration
// through the functions the run's bootstrap performs it with — so what is read
// here is the slot the implement stage's call is pinned to (D1661), not a rung
// this test resolved for itself.
func craftModelARunResolves(t *testing.T, r *convergeRig) craftModelRun {
	t.Helper()
	lc := projectConfigOf(t, r)
	rf, err := resolveFugaroYAML(context.Background(),
		readFile(t, filepath.Join(r.root, "fugaro.yaml")), lc, layerOptions{})
	if err != nil {
		t.Fatalf("the fixture's checkout does not resolve: %v", err)
	}
	if rf.Cfg == nil {
		t.Fatalf("the fixture's checkout resolves no configuration: %v", rf.Problems)
	}
	// The node the run reads its project's crafts, credentials and pools from.
	// It is read carried forward, the way every reader of a fixture does: the
	// node of the release behind is the one this binary's own reader refuses
	// until the update has migrated it, and both reads below are about the
	// fixture rather than about the door.
	node := convergeNode(t, r)
	eff, problems := craft.Resolve(launchCraftLayers(rf.Cfg, node, true), false)
	if len(problems) > 0 {
		t.Errorf("the crafts the fixture's run resolves cannot be served, so this is not an installation the model half converged: %+v", problems)
	}
	// A copy, for launchCraftWrites' reason: the write allocates a level's
	// block, and every other reader of this configuration is a gate.
	run := *rf.Cfg
	runner.WriteCraftRoles(&run, eff, nil)
	return craftModelRun{Model: run.Agent.Models.Coder, Access: eff[config.CraftCoder].Access}
}

// TestCraftModelConvergeKeepsTheModelARunResolves: an installation one release
// behind, whose project layer names the coder craft's model, reaches this
// release with `fugaro update` alone — nothing else is asked of the user, and
// doctor is clean when the run is done (convergeOn) — and a run of the
// repository resolves the same model and the same credential before and after
// that run. That pair is the HARD RULE's own test for this half (F17): the model
// half ships no migration, so there is no step of the update that may move what
// a run's stages call out on.
func TestCraftModelConvergeKeepsTheModelARunResolves(t *testing.T) {
	r, docs := craftModelRig(t)
	before := craftModelARunResolves(t, r)
	// The craft's model is the model the stage runs: rung 3 of D1660's ladder,
	// written into the slot the run reads (D1661), and the reason the fixture is
	// about a run at all.
	if before.Model != craftModelModel {
		t.Fatalf("a run of the fixture resolves %q for its implement stage before the update, and its project layer names the coder craft %q: the fixture is not the installation the model half is about", before.Model, craftModelModel)
	}
	if before.Access == "" || before.Access == config.DefaultCredential {
		t.Fatalf("a run of the fixture resolves the access %q, so the fixture's node names no craft of its own and this test would pass over the default credential", before.Access)
	}

	convergeOn(t, r, craftModelStale)

	after := craftModelARunResolves(t, r)
	if after != before {
		t.Errorf("a run of the repository resolves %+v after fugaro update and %+v before it: the model half needs no migration, so no step of the update may move the model a stage runs or the credential it leaves on", after, before)
	}

	// The credential is the account the stages ran on before the crafts had
	// keys, which is the half MC4's migration writes: the craft's access is the
	// account `implement` resolves to in the config the update left, so the run
	// goes out on the same account through a new key and not on a second one.
	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	if want := installationAccount(written, "implement"); after.Access != want {
		t.Errorf("the craft resolves the access %q after the update, and the update's own config resolves the implement stage to the account %q: the credential a run leaves on moved", after.Access, want)
	}
	// And the model half is not in that config at all: what the update wrote for
	// the crafts is the access alone (MC4), never a model an owner wrote
	// somewhere else (D1666).
	for _, name := range config.CraftNames {
		if m := written.Crafts.Of(name).Model; m != "" {
			t.Errorf("after fugaro update, the installation's config names the craft %s a model (%q): the model half has no migration, and a craft's model is the layer's", name, m)
		}
	}
	// The layer the run resolves from is byte for byte what the project
	// published: a model half that rewrote it would be a migration nobody asked
	// for, under the runs of every repository of the project.
	if got, want := readFile(t, r.craftModelLayerObject(t)), docs.layer; !bytes.Equal(got, want) {
		t.Errorf("the project layer a run resolves is not the one the project published:\n%s\nwant:\n%s", got, want)
	}
}

// craftModelLayerObject is where the fixture's project layer was published, read
// back off the running configuration rather than remembered, so a test that
// compares the object with the fixture's own file reads the document the run
// would read.
func (r *convergeRig) craftModelLayerObject(t *testing.T) string {
	t.Helper()
	lc := projectConfigOf(t, r)
	return filepath.Join(strings.TrimPrefix(lc.BucketURL(), "file://"), config.LayerKey)
}

// TestUpdateIsIdempotentForTheModelHalf: the model half is not a write of the
// door's, so a second pass over what the door wrote — the one migration it
// performs on the crafts — changes none of its bytes, and the craft keys it
// writes are the access alone.
//
// The door is not run twice here, MC4's own test not being able to either: the
// first run of this rig takes the previous release's plugin pin out of the
// checkout, which leaves the working tree different from the default branch, and
// the second run's repository stage stops for the user over exactly that. What
// idempotence means for the model half is a property of the files the run left,
// and that is what this reads.
func TestUpdateIsIdempotentForTheModelHalf(t *testing.T) {
	r, docs := craftModelRig(t)
	convergeOn(t, r, craftModelStale)

	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	if notes := migrateCraftKeys(written); len(notes) != 0 {
		t.Errorf("the config fugaro update wrote is not converged: %v", notes)
	}
	for _, name := range config.CraftNames {
		if m := written.Crafts.Of(name).Model; m != "" {
			t.Errorf("a second pass over the file fugaro update wrote names the craft %s a model (%q): the model half is not a key the migration writes, in either pass", name, m)
		}
	}
	// The layer is the other file the model half could have moved, and it is the
	// one a run resolves from: nothing the door does rewrites it.
	if got, want := readFile(t, r.craftModelLayerObject(t)), docs.layer; !bytes.Equal(got, want) {
		t.Errorf("a second pass reads a project layer that is not the one the project published:\n%s\nwant:\n%s", got, want)
	}
}

// TestCraftModelLeavesARunInFlightRunning is the plan's own pin for this half:
// TestUpdateLeavesARunInFlightRunning, run again over an installation whose
// crafts name a model (MC17's fixture), so that the update is exercised with the
// model half on rather than over a project that names none.
//
// Nothing this half moved was available to a run in flight in the first place —
// there is no model-half write for the update to make — and what the test
// asserts is what the requirement of 2026-11-09 asks: the run is named once and
// not asked about, a run of the repository resolves the same model and the same
// credential across the update, and every write the run makes afterwards still
// lands.
func TestCraftModelLeavesARunInFlightRunning(t *testing.T) {
	r, _ := craftModelRig(t)
	before := craftModelARunResolves(t, r)
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, craftModelStale)

	if line := inFlightLine(t, out); !strings.Contains(line, "1 run in flight") {
		t.Errorf("the update's line does not say how many runs are running over a project whose crafts name a model: %q", line)
	}
	if after := craftModelARunResolves(t, r); after != before {
		t.Errorf("a run of the repository resolves %+v after the update and %+v before it: the record, the pins and the gateway's allow-list of the run in flight read one answer, and this half may not give them another one", after, before)
	}
	run.laterWrites(t, r)
}

// TestCraftModelNeedsNoOperatorStep: the model half asks nothing of the operator
// beyond the one command the HARD RULE allows — `fugaro update`, run once. The
// assertions are the three ways a step could still appear: a question the door
// asks, a migration that writes a model, and a release's `### For operators`
// entry naming a craft's model key.
func TestCraftModelNeedsNoOperatorStep(t *testing.T) {
	// The release files are read first, and that order is the rig's: it chdirs
	// into the checkout it made, from which the docs tree is not where this
	// package's own paths say it is.
	sections := releaseOperatorSections(t)
	r, _ := craftModelRig(t)
	// The run's own account of what it wrote and what it asked (stageDetail):
	// the screen and the log together are the whole of what the person read.
	out := convergeOn(t, r, craftModelStale) + newestLog(t, "update")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "CONFIRM") && (strings.Contains(line, "craft") || strings.Contains(line, "model")) {
			t.Errorf("fugaro update asks the person something about the model half: %q", line)
		}
	}
	for _, name := range config.CraftNames {
		if key := "crafts." + name + ".model"; strings.Contains(out, key) {
			t.Errorf("fugaro update's transcript names %s, so this half is a step it performs or asks about:\n%s", key, out)
		}
	}

	// The one migration the door performs on the crafts writes the access half
	// and nothing else, over the previous release's own config: a model written
	// here would be a model an installation already runs rewritten by an
	// upgrade, which is what D1662 refuses.
	lc := parseLocalConfig(t, previousInstallation)
	if notes := migrateCraftKeys(lc); len(notes) != 0 {
		t.Fatalf("the fixture migrates whole, but the run reported %v", notes)
	}
	for _, name := range config.CraftNames {
		if m := lc.Crafts.Of(name).Model; m != "" {
			t.Errorf("the migration writes the craft %s the model %q: an installation's models are the owner's, and `fugaro update` writes nothing for this half (D1666)", name, m)
		}
	}

	// And no release's operator step names a craft's model key either: the entry
	// a release that carried a model-half step would write is the one this half
	// must never need.
	for _, rel := range sections {
		for _, name := range config.CraftNames {
			if key := "crafts." + name + ".model"; strings.Contains(rel.section, key) {
				t.Errorf("%s's `### For operators` names %s: a craft's model is no operator step, and a release that wrote one would be the second command the HARD RULE forbids", rel.name, key)
			}
		}
	}
}

// releaseOperatorSection is one release file's `### For operators` section: the
// heading a release's own steps live under (docs/releases/README.md), which is
// the only place an operator step could be written.
type releaseOperatorSection struct {
	name    string
	section string
}

// releaseOperatorSections reads every release file's operator section. A release
// with no such section contributes nothing and is not an error — the heading is
// optional — but a scan that found none at all is one over no files, and the
// caller asserts that instead of trusting this.
func releaseOperatorSections(t *testing.T) []releaseOperatorSection {
	t.Helper()
	entries, err := os.ReadDir(releaseFilesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", releaseFilesDir, err)
	}
	var out []releaseOperatorSection
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		const heading = "### For operators"
		doc := readDoc(t, "docs/releases/"+e.Name())
		at := strings.Index(doc, heading)
		if at < 0 {
			continue
		}
		sec := doc[at+len(heading):]
		if next := strings.Index(sec, "\n### "); next >= 0 {
			sec = sec[:next]
		}
		out = append(out, releaseOperatorSection{name: e.Name(), section: sec})
	}
	if len(out) == 0 {
		t.Errorf("%s holds no release file with a `### For operators` section: this scan is over no files", releaseFilesDir)
	}
	return out
}
