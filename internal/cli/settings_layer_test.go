package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// G4's tests (docs/config-management/implementation-plan.md, D1175): the
// project layer file's entries are the generic writer's. The layer's blocks are
// written through the two mechanisms the earlier tasks landed — a concrete
// spelling of a wildcard row (D1161) and the entry pair (D1162, D1163) — and
// the refusals `fugaro config publish` makes over the layer document are the
// ones this edit makes over its own.
//
// What each of these holds: the layer file is the document that decides which
// profile a project's repositories run under, so the two writes that are new
// here are asserted on the file itself and read back through the loader the
// runner reads it with, and the two refusals are asserted to be the ones that
// name what the person reached for rather than the generic allowlist.

// settingsLayerProfiles is a layer file carrying one profile, the shape every
// executable key of a layer is written through (config.ProjectLayer's
// profiles.*, spec §3.6).
const settingsLayerProfiles = "default_profile: web\nprofiles:\n  web:\n    description: the web profile\n    commands:\n      rerun_failed:\n        command: pytest\n        each: pytest {id}\n"

// TestSettingsSetLayerProfileEntryWritesTheLayerFile: an entry of the layer's
// `profiles.<n>.commands.rerun_failed` reaches the layer file through
// --add-entry (D1162), which is the mechanism a map's entry has — the
// registry spells the block's leaves one at a time
// (`layer.profiles.*.commands.rerun_failed.command`) and holds no row for the
// block a person names. The entry is addressed by its field and never by
// position (D1163).
//
// The entry is a command, so the owner's --executable-changes is named with it:
// every executable key of a layer lives in a profile, and this is the document
// the gate below refuses without it.
func TestSettingsSetLayerProfileEntryWritesTheLayerFile(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerProfiles)
	noTerminal(t)
	out, _, err := execute(t, "settings", "set", "layer.profiles.web.commands.rerun_failed",
		"--add-entry", "command=pytest -k", "--layer", "project", "--executable-changes")
	if err != nil {
		t.Fatalf("the layer's profile entry was refused: %v\n%s", err, out)
	}
	got := readFileString(t, path)
	if !strings.Contains(got, "rerun_failed:") || !strings.Contains(got, "command: pytest -k") {
		t.Errorf("the layer file does not carry the entry:\n%s", got)
	}
	// The file the write produced is the one the loader reads: an entry this
	// command can write and the run cannot read would be a setting nobody has.
	l := parseLayerOrFatal(t, got)
	rf := l.Profiles["web"].Commands.RerunFailed
	if rf == nil || rf.Command != "pytest -k" {
		t.Errorf("the loader reads the profile's rerun command as %v, want %q", rf, "pytest -k")
	}
	// And the write ends where every layer write ends (D1176): the publish is
	// `fugaro update`'s own step and not a second command.
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the layer write does not end with the next step:\n%s", out)
	}
}

// TestSettingsSetLayerAbEntryWritesTheLayerFile: the layer's `ab` block is the
// third block D1161 names (spec §3.6), and its entries are written the same
// way: `--add-entry rate=0.05` writes the member the row names, into this
// machine's own layer file.
func TestSettingsSetLayerAbEntryWritesTheLayerFile(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, "")
	noTerminal(t)
	out, _, err := execute(t, "settings", "set", "layer.ab", "--add-entry", "rate=0.05", "--layer", "project")
	if err != nil {
		t.Fatalf("the layer's ab entry was refused: %v\n%s", err, out)
	}
	got := readFileString(t, path)
	if !strings.Contains(got, "rate: 0.05") {
		t.Errorf("the layer file does not carry the ab entry:\n%s", got)
	}
	l := parseLayerOrFatal(t, got)
	if l.AB == nil || l.AB.Rate != 0.05 {
		t.Errorf("the loader reads ab.rate as %v, want 0.05", l.AB.Rate)
	}
	// The removal is the same mechanism, and it leaves the file a layer: a
	// member taken away is not an empty mapping.
	if out, _, err := execute(t, "settings", "set", "layer.ab", "--remove-entry", "rate", "--layer", "project"); err != nil {
		t.Fatalf("the ab entry was not removable: %v\n%s", err, out)
	}
	if got := readFileString(t, path); strings.Contains(got, "rate:") {
		t.Errorf("the removed ab entry is still in the layer file:\n%s", got)
	}
}

// settingsLayerAgentCaps is a layer file whose defaults.agent block holds the
// spend cap beside another member: the cap is the key these tests write, and
// the member beside it is what keeps the block a mapping once the cap leaves.
const settingsLayerAgentCaps = "defaults:\n  agent:\n    max_budget_usd: 25\n    review_rounds: 3\n"

// TestSettingsSetLayerAgentEntryIsClassifiedAsTheLeafItWrites: an entry of the
// layer's `defaults.agent` block is one member of that block, and the key the
// write is about is the member's own — `layer.defaults.agent.max_budget_usd`,
// the row the registry holds and D846's fence reads — and not the block the
// command line named. So the entry spelling and the leaf spelling of one edit
// answer the widening question alike, which is what D1165's "classified ...
// exactly as a scalar write is" means: an entry write that raises a ceiling is
// a ceiling raised, in the dry run's widening (D1106, the console's tick) and
// in the class the managed gate judges (settingsOwnerOnlyRule, D1026).
func TestSettingsSetLayerAgentEntryIsClassifiedAsTheLeafItWrites(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteLayerFile(t, settingsLayerAgentCaps)
	noTerminal(t)

	raised := settingsDryRunDoc(t, "settings", "set", "layer.defaults.agent",
		"--add-entry", "max_budget_usd=100", "--layer", "project")
	leaf := settingsDryRunDoc(t, "settings", "set", "layer.defaults.agent.max_budget_usd",
		"100", "--layer", "project")
	if !raised.Widening {
		t.Errorf("raising the cap through an entry is not classified as a widening: %+v", raised)
	}
	if raised.Widening != leaf.Widening || raised.Why != leaf.Why {
		t.Errorf("the entry spelling and the leaf spelling of one edit are classified differently: %+v against %+v", raised, leaf)
	}
	// The classification is the leaf's and the window is still the entry's: the
	// members a person is shown are the entry's own (D1164), and the block
	// around it is not what changed.
	if raised.Operation != "add-entry" || raised.Old != "max_budget_usd=25" || raised.New != "max_budget_usd=100" {
		t.Errorf("the dry run of the entry does not show the entry the write is about: %+v", raised)
	}
	// The other direction is unchanged: a cap written below the one the file
	// holds tightens it, and no tightening asks for the owner's own hand.
	lowered := settingsDryRunDoc(t, "settings", "set", "layer.defaults.agent",
		"--add-entry", "max_budget_usd=10", "--layer", "project")
	if lowered.Widening {
		t.Errorf("lowering the cap through an entry is classified as a widening: %+v", lowered)
	}
}

// TestSettingsSetLayerAgentEntryRemovalIsACeilingRemoval: taking the cap out of
// the layer's agent block hands the decision back to a looser layer, which is
// the removal `settings unset` makes and one of the class D846 reserves for the
// owner — an entry removal that is a ceiling's and not the narrowing an
// `--add`/`--remove` of a list member is.
func TestSettingsSetLayerAgentEntryRemovalIsACeilingRemoval(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteLayerFile(t, settingsLayerAgentCaps)
	noTerminal(t)

	doc := settingsDryRunDoc(t, "settings", "set", "layer.defaults.agent",
		"--remove-entry", "max_budget_usd", "--layer", "project")
	if !doc.Changed {
		t.Errorf("the removal of the cap changes nothing, and the file holds it: %+v", doc)
	}
	if !doc.Widening {
		t.Errorf("removing the cap through an entry is not classified as a widening: %+v", doc)
	}
	if doc.Old != "max_budget_usd=25" || doc.New != "" {
		t.Errorf("the document says %q -> %q, and the file holds the cap and the removal leaves the entry gone", doc.Old, doc.New)
	}
	// The scalar spelling of the same removal is the same class and carries the
	// same sentence, so the two doors into one ceiling do not part company.
	leaf := settingsDryRunDoc(t, "settings", "unset", "layer.defaults.agent.max_budget_usd", "--layer", "project")
	if !leaf.Widening || doc.Why != leaf.Why {
		t.Errorf("the entry removal and the leaf removal of one ceiling answer differently: %+v against %+v", doc, leaf)
	}
}

// settingsLayerRigorFloor is a layer file holding the mode its runs start
// from: the floor D20c puts in the layer, with the block's other member beside
// it, which is what keeps `defaults.rigor` a mapping once the mode leaves.
const settingsLayerRigorFloor = "defaults:\n  rigor:\n    default: pro\n    areas:\n      - name: security\n        match: internal/creds/**\n        mode: critical\n"

// TestSettingsSetLayerRigorEntryLoweringTheFloorIsAWidening: the layer's
// `defaults.rigor.default` is a floor, and its widening direction is the
// ceiling's other way round — a *lower* mode is every run the project's
// branches starting to review less, which is the step D1026 reserves for the
// owner's own hand exactly as raising a cap is (spec §5's two rows: `settings
// set` writes what "lowers no floor", and a floor "lowered or removed — the
// typed name").
//
// The entry spelling is the only one that reaches this floor: the leaf spelling
// `layer.defaults.rigor.default` is the rigor family's row and is refused with
// `fugaro rigor set` (the divergence settingsEntryRow documents, D1175), so
// this classification is the whole of what stands between a session and a
// lowered floor — the dry run's `widening` (D1106, the console's tick) and the
// gate's typed rule (settingsOwnerOnlyRule, D1026) both read it.
//
// A raise is the direction spec §5 admits for `fugaro rigor set` ("a mode at or
// above the resolved mode"), and it is asserted here so the fence is a
// direction and not a refusal of the block.
func TestSettingsSetLayerRigorEntryLoweringTheFloorIsAWidening(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteLayerFile(t, settingsLayerRigorFloor)
	noTerminal(t)

	lowered := settingsDryRunDoc(t, "settings", "set", "layer.defaults.rigor",
		"--add-entry", "default=poc", "--layer", "project")
	if !lowered.Changed {
		t.Fatalf("the entry writes the mode the file holds and the dry run says nothing changed: %+v", lowered)
	}
	if !lowered.Widening || lowered.Why == "" {
		t.Errorf("lowering the layer's floor through an entry is not classified as a widening: %+v", lowered)
	}
	if lowered.Operation != "add-entry" || lowered.Old != "default=pro" || lowered.New != "default=poc" {
		t.Errorf("the dry run of the entry does not show the entry the write is about: %+v", lowered)
	}

	raised := settingsDryRunDoc(t, "settings", "set", "layer.defaults.rigor",
		"--add-entry", "default=critical", "--layer", "project")
	if raised.Widening {
		t.Errorf("raising the layer's floor through an entry is classified as a widening: %+v", raised)
	}
}

// TestSettingsSetLayerRigorEntryRemovalIsAFloorRemoval: taking the floor out of
// the layer hands the mode back to a weaker reading, which is the removal
// `settings unset` makes at the leaf and the second half of the class D1026
// reserves — "a floor lowered or removed" is one step, not two.
func TestSettingsSetLayerRigorEntryRemovalIsAFloorRemoval(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerRigorFloor)
	before := readFileString(t, path)
	noTerminal(t)

	doc := settingsDryRunDoc(t, "settings", "set", "layer.defaults.rigor",
		"--remove-entry", "default", "--layer", "project")
	if !doc.Changed {
		t.Fatalf("the removal of the floor changes nothing, and the file holds it: %+v", doc)
	}
	if !doc.Widening || doc.Why == "" {
		t.Errorf("removing the layer's floor through an entry is not classified as a widening: %+v", doc)
	}
	if doc.Old != "default=pro" || doc.New != "" {
		t.Errorf("the document says %q -> %q, and the file holds the floor and the removal leaves the entry gone", doc.Old, doc.New)
	}

	// The class is the write's and not the window's: a session asking for the
	// same removal is refused whatever --yes says, and the file keeps its floor.
	agentSession(t)
	out, _, err := execute(t, "settings", "set", "layer.defaults.rigor",
		"--remove-entry", "default", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a session removed the layer's rigor floor:\n%s", out)
	}
	if !strings.Contains(err.Error(), "run this yourself at your own terminal") {
		t.Errorf("the removal was not refused as the owner's own step: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused floor removal")
	}
}

// TestSettingsSetLayerRigorEntryRemovingTheAreasIsAFloorRemoval: the block's
// `areas` member is the other half of the same floor class and not a shape the
// fence may skip. Removing it takes every rigor rule the layer holds with it —
// `settingsLayerRigorFloor` holds a critical rule over internal/creds/** — and
// that is exactly the "a floor lowered or removed" step D1026 reserves, so the
// write is the typed one and a session asking for it is refused.
//
// It is the case the member-by-member reading misses: `areas` is a list and so
// has no leaf row of its own in the registry (settingsEntryLeafKey reaches the
// block, and settingsWriteLookup finds no row for
// `layer.defaults.rigor.areas`), which would leave the write classified by the
// block and untyped. The floor is judged by the function that judges it at the
// publish door (layerRigorRules, over the two parsed layers), which reads the
// rules that leave rather than the member that was named.
func TestSettingsSetLayerRigorEntryRemovingTheAreasIsAFloorRemoval(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerRigorFloor)
	before := readFileString(t, path)
	noTerminal(t)

	doc := settingsDryRunDoc(t, "settings", "set", "layer.defaults.rigor",
		"--remove-entry", "areas", "--layer", "project")
	if !doc.Changed {
		t.Fatalf("the removal of the areas changes nothing, and the file holds them: %+v", doc)
	}
	if !doc.Widening || doc.Why == "" {
		t.Errorf("removing the layer's rigor rules through an entry is not classified as a widening: %+v", doc)
	}
	if !strings.Contains(doc.Why, "removes the rigor rule security") {
		t.Errorf("the sentence does not name the rule the removal takes with it: %q", doc.Why)
	}
	if doc.Operation != "remove-entry" {
		t.Errorf("the dry run does not show the entry the write is about: %+v", doc)
	}

	// The class is the write's and not the window's: a session asking for the
	// same removal is refused whatever --yes says, and the file keeps its rules.
	agentSession(t)
	out, _, err := execute(t, "settings", "set", "layer.defaults.rigor",
		"--remove-entry", "areas", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a session removed the layer's rigor rules:\n%s", out)
	}
	if !strings.Contains(err.Error(), "run this yourself at your own terminal") {
		t.Errorf("the removal was not refused as the owner's own step: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused rigor rule removal")
	}
}

// TestSettingsSetLayerRefusesAReservedKey: `fugaro config publish` refuses the
// layer's reserved keys in a coding agent's session (layerReservedRuns), and a
// layer edit makes the same refusal over its own key — so a session that
// reaches for the project's governance block reads the reason it may not have
// it, rather than the session fence's "run this yourself". At the owner's own
// terminal the same key is exactly what the row is for, which is the test
// beside this one (TestSettingsSetLayersGovernanceBlockIsAWideningThatAsksNothing).
func TestSettingsSetLayerRefusesAReservedKey(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, "governance:\n  review_ratio: 0.5\n")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "layer.governance.review_ratio", "0.9", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a session wrote a reserved key into the layer file:\n%s", out)
	}
	if !strings.Contains(err.Error(), "governance.review_ratio") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the refusal does not name the reserved run: %v", err)
	}
	if strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("the session fence answered instead of the reserved key's own refusal: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused key")
	}
}

// TestSettingsSetLayerRefusesAnExecutableChangeWithoutTheOwnersFlag: the
// profile keys that run as shell are the reason the layer file is the widest
// surface this design touches (§3.6, spec §4.2). A layer edit that moves one
// is refused with `fugaro config publish`'s own comparison and sentence
// (executableChanges, config.ExecutableKeys), and the owner's flag is what
// lets it through — exactly as it is on a repository's workflows.
func TestSettingsSetLayerRefusesAnExecutableChangeWithoutTheOwnersFlag(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerProfiles)
	before := readFileString(t, path)
	noTerminal(t)

	out, _, err := execute(t, "settings", "set", "layer.profiles.web.commands.build", "sh build.sh", "--layer", "project")
	if err == nil {
		t.Fatalf("an executable key of a profile was written without the owner's flag:\n%s", out)
	}
	if !strings.Contains(out, "commands.build") || !strings.Contains(out, "sh build.sh") {
		t.Errorf("the banner does not name the executable change:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--executable-changes") {
		t.Errorf("the refusal does not name the flag that would allow it: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused executable change")
	}

	// The flag is the owner's word and the write lands with it, in the same
	// document and through the same entry the run reads.
	out, _, err = execute(t, "settings", "set", "layer.profiles.web.commands.build", "sh build.sh", "--layer", "project", "--executable-changes")
	if err != nil {
		t.Fatalf("the layer's build command was refused with the owner's flag: %v\n%s", err, out)
	}
	got := readFileString(t, path)
	l := parseLayerOrFatal(t, got)
	if got := l.Profiles["web"].Commands.Build; got != "sh build.sh" {
		t.Errorf("the loader reads the profile's build command as %q:\n%s", got, readFileString(t, path))
	}
	if !strings.Contains(out, "commands.build") || !strings.Contains(out, "sh build.sh") {
		t.Errorf("the write does not report the executable change it made:\n%s", out)
	}
}

// settingsLayerProfileCommands is the fixture the unset path's two refusals
// are read over: one profile whose commands run as shell, and the governance
// block `fugaro config publish` reserves.
const settingsLayerProfileCommands = "profiles:\n  web:\n    commands:\n      build: sh build.sh\n      test: pytest\n"

// TestSettingsUnsetLayerRefusesAnExecutableChangeWithoutTheOwnersFlag: the
// unset path meets the layer's own gate exactly as the set path does
// (settingsLayerGate, spec §3.6) — taking a profile's command away moves what a
// run of every repository that takes the profile runs as shell just as writing
// one does, and the owner's --executable-changes is the word for it on both.
//
// It is asserted on `unset` and not only on `set` because the two paths call
// the gate in two places: a refusal wired to one of them is a layer whose
// build commands a session can still take away.
func TestSettingsUnsetLayerRefusesAnExecutableChangeWithoutTheOwnersFlag(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerProfileCommands)
	before := readFileString(t, path)
	noTerminal(t)

	out, _, err := execute(t, "settings", "unset", "layer.profiles.web.commands.build", "--layer", "project")
	if err == nil {
		t.Fatalf("an executable key of a profile was removed without the owner's flag:\n%s", out)
	}
	if !strings.Contains(out, "commands.build") || !strings.Contains(out, "sh build.sh") {
		t.Errorf("the banner does not name the executable change:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--executable-changes") {
		t.Errorf("the refusal does not name the flag that would allow it: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused executable change")
	}

	// The flag is the owner's word and the removal lands with it, in the same
	// document the loader reads.
	out, _, err = execute(t, "settings", "unset", "layer.profiles.web.commands.build", "--layer", "project", "--executable-changes")
	if err != nil {
		t.Fatalf("the layer's build command was refused the owner's flag: %v\n%s", err, out)
	}
	l := parseLayerOrFatal(t, readFileString(t, path))
	if got := l.Profiles["web"].Commands.Build; got != "" {
		t.Errorf("the loader still reads the profile's build command as %q:\n%s", got, readFileString(t, path))
	}
	if got := l.Profiles["web"].Commands.Test; got != "pytest" {
		t.Errorf("the removal took the profile's other command with it: test reads %q", got)
	}
}

// TestSettingsUnsetLayerRefusesAReservedKeyInASession: the layer gate's other
// half is read on this path too, and it is the file's own answer rather than
// the session fence's — a session that takes the project's governance ratio
// away reads `fugaro config publish`'s reserved-run refusal, and the file keeps
// the key.
func TestSettingsUnsetLayerRefusesAReservedKeyInASession(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerProfileCommands+"governance:\n  review_ratio: 0.5\n")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "unset", "layer.governance.review_ratio", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a session removed a reserved key from the layer file:\n%s", out)
	}
	if !strings.Contains(err.Error(), "governance.review_ratio") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the refusal does not name the reserved run: %v", err)
	}
	if strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("the session fence answered instead of the reserved key's own refusal: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused key")
	}
}

// TestSettingsSetLayerEntryInAManagedSessionIsRefusedByTheGate: the entry
// mechanism adds no door of its own (D1180): an entry write of the layer's is
// the same `settings set --layer project` the gate already judges, so a
// session's management of the layer stays refused by the rules that refuse it
// and the refusal is recorded like every other (D1025).
func TestSettingsSetLayerEntryInAManagedSessionIsRefusedByTheGate(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored)
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "layer.ab", "--add-entry", "rate=0.05", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a session wrote the layer's ab block:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the entry write was not refused by the gate: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused entry write")
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
}

// TestSettingsLayerEntryBlocksResolveToTheirLeaves: every project-layer block
// the entry table names is one the registry has rows for, whose layers agree and
// none of which is Sensitive. Those are the three facts settingsEntryRow reads
// the row it builds out of, and the table is what says an entry of the block can
// be written at all: a block whose leaves a later release renames, moves to
// another layer or marks Sensitive would otherwise be a name that resolves to a
// row this Fugaro invents rather than reads.
func TestSettingsLayerEntryBlocksResolveToTheirLeaves(t *testing.T) {
	blocks := 0
	for _, pattern := range settingsEntryBlockOrder {
		if !strings.HasPrefix(pattern, "layer.") {
			continue
		}
		blocks++
		leaves := 0
		var layers []string
		for _, r := range settings.Rows() {
			if !strings.HasPrefix(r.Key, pattern+".") {
				continue
			}
			leaves++
			if layers == nil {
				layers = r.Layers
				continue
			}
			if !slices.Equal(layers, r.Layers) {
				t.Errorf("%s's leaves disagree about the layers they are set at: %v against %v, and the block's row is built from the first of them", pattern, layers, r.Layers)
			}
		}
		if leaves == 0 {
			t.Errorf("%s is in the entry table and no registry row names a member of it: an entry write of the block would be refused as one no row names", pattern)
			continue
		}
		if !slices.Contains(layers, settings.LayerProject) {
			t.Errorf("%s's leaves are set at %v and not at the project layer, so a --layer project write of the block has no layer the row names: the entry would be refused by settingsResolveTarget rather than written", pattern, layers)
		}
		if r, ok := settings.Lookup(pattern); ok {
			t.Errorf("%s is a registry row of its own (%s): a block the registry holds needs no row built for it, and the two would answer the same write differently", pattern, r.Key)
		}
	}
	if blocks == 0 {
		t.Fatal("the entry table holds no project-layer block, so nothing here is checked")
	}
}

// TestSettingsSetLayerCacheEntryIsRefusedByTheKeyFieldRule: the layer's cache is
// the one block this pair reaches and cannot address (D1163, spec §2.2) — its
// key field holds a list of files rather than a name — so an entry of it is
// refused with that sentence rather than with "takes no entry", exactly as a
// repository's own cache is.
func TestSettingsSetLayerCacheEntryIsRefusedByTheKeyFieldRule(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteLayerFile(t, settingsLayerProfiles)
	before := readFileString(t, path)
	noTerminal(t)

	out, _, err := execute(t, "settings", "set", "layer.profiles.web.cache", "--add-entry", "key=pip", "--layer", "project")
	if err == nil {
		t.Fatalf("a cache entry of the layer was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "key field") || !strings.Contains(err.Error(), "a list of files and not a name") {
		t.Errorf("the refusal is not D1163's key-field rule over the layer's cache: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the layer file changed under a refused cache entry")
	}
}

// S4's tests (docs/settings-commands/implementation-plan.md, D845): the read
// side of the project layer. The layer's keys have one store — the layer file,
// which is also the layer's only writer (spec §2.2) — and the object published
// from it is what a run actually resolves, so the layer's half of this read is
// two sides and one comparison: the file's values on the rows, the shared
// sha256 comparison (localLayerState, which doctor and `fugaro config layer`
// decide `unpublished` by) as the STATE, and one source that names the file and
// both sha256s.
//
// The three tests below are that comparison's three states: the file and the
// object differ (unpublished, the state this read exists for), the file is the
// published layer (ok, and the source names both sha256s), and there is no file
// yet (the object is what the rows read, and the source says so).

// settingsLayerObject is the published object the tests below compare this
// machine's file with: the layer every other layer test in this package uses,
// so the two sides differ in one registry key (default_profile) and nothing
// else.
const settingsLayerObject = testProjectLayer

// settingsLayerOwnFileBody is this machine's own layer file for the budget
// fixture's project, under the header settingsLayerOwnLayerFile writes:
// settingsLayerObject with the profile the project defaults to changed, which
// is the edit the state this read exists for is about. The header is written by
// the helper, so the body carries no version:, project: or gcp_project: of its
// own — one file with two of each is a file no reader accepts.
const settingsLayerOwnFileBody = `defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    commands: { build: sh build.sh, test: sh test.sh }
  web:
    commands: { build: sh build-web.sh }
default_profile: web
`

// settingsLayerOwnFile is that file whole: the bytes the read compares with the
// published object, and so the ones a test publishes when the object is to
// carry what the file says.
const settingsLayerOwnFile = layerPreamble + settingsLayerOwnFileBody

// settingsLayerPublish writes object as the fixture's published project layer.
// It is a write into the installation's own runs bucket, which is where
// `fugaro config publish` puts it (env.bucket, from the config), so the read
// reaches it through the same URL the write used.
func settingsLayerPublish(t *testing.T, f *budgetFixture, object string) {
	t.Helper()
	testutil.WriteFiles(t, f.runsDir(), map[string]string{config.LayerKey: object})
}

// settingsLayerPublished is settingsCloudFixture with object already published,
// and no local layer file: the state a machine is in before `fugaro update`
// seeds one.
func settingsLayerPublished(t *testing.T, object string) *budgetFixture {
	t.Helper()
	f := settingsCloudFixture(t)
	settingsLayerPublish(t, f, object)
	return f
}

// settingsLayerOwnLayerFile writes this machine's own layer file, body under
// the header every layer file carries: an unanchored file is refused by every
// reader of it, and a test file that no reader accepts would pin the refusal
// rather than the comparison.
func settingsLayerOwnLayerFile(t *testing.T, body string) string {
	t.Helper()
	return settingsLayerFileText(t, layerPreamble+body)
}

// settingsLayerFileText writes text as this machine's own layer file and
// returns its path, header and all: the door for a file the readers do not
// accept, which settingsLayerOwnLayerFile's header would repair.
func settingsLayerFileText(t *testing.T, text string) string {
	t.Helper()
	path, err := localcfg.LayerFile(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsSourceOf is the one source of layer in the document, or a fatal.
func settingsSourceOf(t *testing.T, doc settingsTestDoc, layer string) settingsTestSrc {
	t.Helper()
	for _, s := range doc.Sources {
		if s.Layer == layer {
			return s
		}
	}
	t.Fatalf("the document lists no %s source (it lists %d)", layer, len(doc.Sources))
	return settingsTestSrc{}
}

// settingsLayerBucketUnreachable makes the installation's bucket unreadable for
// the rest of the test. It is the other side of `--offline`'s promise: the flag
// reads no published value on purpose and this read reaches none by accident,
// and neither may claim `unpublished`, which is a comparison (spec §2.5).
func settingsLayerBucketUnreachable(t *testing.T) {
	t.Helper()
	old := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = old })
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
}

// TestSettingsMarksAPublishedLayerThatIsUnpublished is S4's red step: the
// layer's rows read this machine's own file — the layer's one store (spec §2.2)
// — and the STATE says whether what they read is what a run gets. The file's
// default_profile is `web` while the published object carries `svc`, so the row
// reads the file's value with WINS project and the state is `unpublished`: the
// edit reaches runs on the next `fugaro update` and not before (SD3).
//
// The second half is the same read once the object carries what the file says:
// nothing is waiting for an update, and the row is ok. `--offline` is the third
// claim, because `unpublished` compares the file with the cloud and the flag
// reads none (spec §2.5): a state that cannot be checked is not printed.
func TestSettingsMarksAPublishedLayerThatIsUnpublished(t *testing.T) {
	f := settingsLayerPublished(t, settingsLayerObject)
	path := settingsLayerOwnLayerFile(t, settingsLayerOwnFileBody)

	row := rowOf(t, readSettingsJSON(t, "layer.default_profile"), "layer.default_profile", "project")
	if deref(row.Value) != "web" || row.Wins != "project" {
		t.Errorf("value/wins = %q/%q, want the file's web decided by project: the file is the layer's one store", deref(row.Value), row.Wins)
	}
	if row.State != stateUnpublished {
		t.Errorf("state = %q, want unpublished: the file's web is not what the object carries", row.State)
	}
	// A key the two sides agree on is not unpublished: the state is about the
	// keys that differ and not about the layer being behind as a whole.
	same := rowOf(t, readSettingsJSON(t, "layer.defaults.git.provider"), "layer.defaults.git.provider", "project")
	if deref(same.Value) != "github" || same.State != "ok" {
		t.Errorf("value/state = %q/%q, want github/ok: the object carries what the file says for this key", deref(same.Value), same.State)
	}

	// Published: the object now carries the file's own bytes, so nothing is
	// waiting for an update.
	settingsLayerPublish(t, f, settingsLayerOwnFile)
	row = rowOf(t, readSettingsJSON(t, "layer.default_profile"), "layer.default_profile", "project")
	if deref(row.Value) != "web" || row.State != "ok" {
		t.Errorf("value/state = %q/%q, want web/ok: the object carries what the file says", deref(row.Value), row.State)
	}

	// --offline: no published value was read, so the comparison is not claimed
	// and no source names the file (spec §2.5).
	offline := readSettingsJSON(t, "layer.default_profile", "--offline")
	for _, s := range offline.Settings {
		if s.State == stateUnpublished {
			t.Errorf("%s reads unpublished offline, and --offline read no published value to differ from", s.Key)
		}
	}
	for _, s := range offline.Sources {
		if s.Layer == "project" {
			t.Errorf("the offline document lists a project source (%q), and --offline read no project layer: %s", s.Status, s.Note)
		}
	}

	// A bucket that cannot be opened is the same claim met the other way round,
	// and the one a reader is likelier to meet: the file's values are still what
	// these rows read — the file is a local document and does not depend on the
	// bucket — but no key is unpublished, because a comparison nobody could make
	// decides no difference. The source says which of the two it got.
	settingsLayerBucketUnreachable(t)
	unread := readSettingsJSON(t, "layer.default_profile")
	row = rowOf(t, unread, "layer.default_profile", "project")
	if deref(row.Value) != "web" || row.State != stateOK {
		t.Errorf("value/state = %q/%q with the bucket unreachable, want the file's web/ok: nothing was compared, so nothing differs", deref(row.Value), row.State)
	}
	if s := settingsSourceOf(t, unread, "project"); !strings.Contains(s.Note, "was not read") || !strings.Contains(s.Note, path) {
		t.Errorf("the project source = %q/%q, want it to name the file and say the published layer was not read", s.Status, s.Note)
	}
}

// TestSettingsLayerRowsNameTheLocalFileAndBothShas: the state word alone does
// not tell an owner which two things differ, which is why every door that
// prints `unpublished` prints the shared sentence with it (D845): the local
// file's path, its sha256, the published layer's sha256, which of the two
// causes it is and the remedy that fits. `fugaro settings --json` has no column
// for a sentence, so the sentence is the project layer's own source note —
// beside `note: repo:<name>` for a repository and the local config's path for
// the local file, which is what that member is for.
func TestSettingsLayerRowsNameTheLocalFileAndBothShas(t *testing.T) {
	settingsLayerPublished(t, settingsLayerObject)
	path := settingsLayerOwnLayerFile(t, settingsLayerOwnFileBody)

	doc := readSettingsJSON(t, "layer.default_profile", "--why")
	row := rowOf(t, doc, "layer.default_profile", "project")
	if row.State != stateUnpublished {
		t.Fatalf("state = %q, want unpublished: this test is about what the state's sentence says", row.State)
	}
	src := settingsSourceOf(t, doc, "project")
	if src.Status != stateUnpublished {
		t.Errorf("the project source is %q, want the state its rows read", src.Status)
	}
	own := config.LayerSum([]byte(readFileString(t, path)))
	published := config.LayerSum([]byte(settingsLayerObject))
	if own == published {
		t.Fatal("the two sides are the same bytes, so nothing here can be told apart")
	}
	for _, want := range []string{path, shortSHA(own), shortSHA(published), "fugaro update"} {
		if !strings.Contains(src.Note, want) {
			t.Errorf("the project source's note does not carry %q: %s", want, src.Note)
		}
	}
}

// TestSettingsWithNoLocalLayerFileReadsThePublishedOneAndSaysSo: a machine that
// has never been seeded has no file to read, and the layer's keys then read the
// object the project published — the value a run resolves — rather than the
// defaults: a `layer.` row printing its default on an installation that has
// published a layer would be a wrong answer about every repository of the
// project. Nothing is `unpublished` here, because the read has one side and
// there is no difference to claim (spec §2.5), and the source says which of the
// two sides answered.
func TestSettingsWithNoLocalLayerFileReadsThePublishedOneAndSaysSo(t *testing.T) {
	settingsLayerPublished(t, settingsLayerObject)

	doc := readSettingsJSON(t, "layer.default_profile")
	row := rowOf(t, doc, "layer.default_profile", "project")
	if deref(row.Value) != "svc" || row.Wins != "project" {
		t.Errorf("value/wins = %q/%q, want the published object's svc decided by project", deref(row.Value), row.Wins)
	}
	if row.State != "ok" {
		t.Errorf("state = %q, want ok: the object was read and there is nothing to compare with", row.State)
	}
	for _, s := range doc.Settings {
		if s.State == stateUnpublished {
			t.Errorf("%s reads unpublished with no local file, and there is no local value to differ", s.Key)
		}
	}
	path, err := localcfg.LayerFile(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	src := settingsSourceOf(t, doc, "project")
	if src.Status != "ok" {
		t.Errorf("the project source is %q, want ok: the object was read", src.Status)
	}
	for _, want := range []string{path, "does not exist", "published project layer", shortSHA(config.LayerSum([]byte(settingsLayerObject)))} {
		if !strings.Contains(src.Note, want) {
			t.Errorf("the project source's note does not say %q: %s", want, src.Note)
		}
	}
}

// TestSettingsReadsTheLayerSideWithoutABudgetBackend: the layer side is one
// bucket read, and a bucket needs no Firebase backend — cloudRefusal's own
// comment calls the project without one the ordinary shape of an installation
// that never turned the budget on. Only the two database layers have nothing to
// dial there, and a read that skipped the layer side with them would answer a
// `layer.` row with its default and `ok` where a run gets the published value:
// a wrong answer about every repository of the project, which is the one thing
// this read may not give (SD4).
func TestSettingsReadsTheLayerSideWithoutABudgetBackend(t *testing.T) {
	// A budget block with no rtdb_url: the project has no backend, so there is
	// no database to dial and the two database layers say why.
	f := newBudgetFixture(t, "budget: { firebase_project: proj-1234 }\n")
	settingsLayerPublish(t, f, settingsLayerObject)
	settingsLayerOwnLayerFile(t, settingsLayerOwnFileBody)

	doc := readSettingsJSON(t, "layer.default_profile")
	row := rowOf(t, doc, "layer.default_profile", "project")
	if deref(row.Value) != "web" || row.Wins != settings.LayerProject {
		t.Errorf("value/wins = %q/%q, want the file's web decided by project: the file is what these rows read, and reading it needs no backend", deref(row.Value), row.Wins)
	}
	if row.State != stateUnpublished {
		t.Errorf("state = %q, want unpublished: the file and the object this bucket holds differ", row.State)
	}
	for _, layer := range []string{"policy", "caps"} {
		src := settingsSourceOf(t, doc, layer)
		if !strings.Contains(src.Status, "budget.rtdb_url") {
			t.Errorf("the %s source is %q, want the reason naming the key that is not set", layer, src.Status)
		}
	}
}

// TestSettingsWithALayerFileThatDoesNotParseSaysSo: a file that is there and is
// not a layer this Fugaro reads states no value the read can print, so the rows
// fall back to the published object — the value a run gets — and no key is
// `unpublished`: that word is decided between two parsed layers (spec §2.5), and
// one of the two was never parsed. The parse's own sentence is what its owner
// needs, and the STATE column has no room for it: it is the source's note.
func TestSettingsWithALayerFileThatDoesNotParseSaysSo(t *testing.T) {
	settingsLayerPublished(t, settingsLayerObject)
	// A layer file without its header, which every reader of one refuses: the
	// problems name the three keys the anchor needs.
	path := settingsLayerFileText(t, "default_profile: web\n")

	doc := readSettingsJSON(t, "layer.default_profile")
	row := rowOf(t, doc, "layer.default_profile", "project")
	if deref(row.Value) != "svc" || row.Wins != settings.LayerProject {
		t.Errorf("value/wins = %q/%q, want the published object's svc: a file no reader parses states no value to print", deref(row.Value), row.Wins)
	}
	if row.State != stateOK {
		t.Errorf("state = %q, want ok: a file that did not parse is no value to differ from, and `unpublished` is decided between two parsed layers", row.State)
	}
	if src := settingsSourceOf(t, doc, "policy"); src.Status != stateOK {
		t.Errorf("the policy source is %q, and only the layer is malformed here", src.Status)
	}
	src := settingsSourceOf(t, doc, "project")
	if src.Status != unreadState("the local layer file is not a layer this Fugaro reads") {
		t.Errorf("the project source is %q, want the read's own reason for a file it does not read", src.Status)
	}
	for _, want := range []string{path, "version: must be 1"} {
		if !strings.Contains(src.Note, want) {
			t.Errorf("the project source's note does not carry %q: %s", want, src.Note)
		}
	}
}

// TestSettingsWithNoLocalLayerFileAndAnUnreadableObjectSaysWhich: with neither
// side read the rows keep the shape a key no layer decided has — the default,
// with WINS default — and the source names the side that could not be reached:
// the object and its URL, not the file this machine does not have. Nothing is
// `unpublished`: the comparison is what claims it, and nothing was compared
// (spec §2.5).
func TestSettingsWithNoLocalLayerFileAndAnUnreadableObjectSaysWhich(t *testing.T) {
	f := settingsCloudFixture(t)
	settingsLayerBucketUnreachable(t)

	doc := readSettingsJSON(t, "layer.default_profile")
	// The row is what this project printed before the read looked at all: the
	// default, with WINS default. `unpublished` is not claimed — nothing was
	// compared — and the reason the comparison could not be made is on the
	// layer's own source rather than on each of its rows: one sentence beside
	// the layer, which is the shape the two cloud sources already have.
	row := rowOf(t, doc, "layer.default_profile", "project")
	if row.Wins != "default" || row.State != stateOK {
		t.Errorf("wins/state = %q/%q, want default/ok: no layer decided this key, and the reason is the source's", row.Wins, row.State)
	}
	want := row.Default.Value
	if want == "" {
		want = row.Default.Unset
	}
	if deref(row.Value) != want {
		t.Errorf("value = %q, want the default (%q): a row nobody read is not guessed", deref(row.Value), want)
	}
	src := settingsSourceOf(t, doc, "project")
	if !strings.HasPrefix(src.Status, "unread: ") {
		t.Errorf("the project source is %q, want a reason", src.Status)
	}
	where := "file://" + filepath.Join(f.runsDir(), filepath.FromSlash(config.LayerKey))
	if !strings.Contains(src.Note, where) {
		t.Errorf("the project source's note does not name the object the read could not reach (%s): %s", where, src.Note)
	}
}
