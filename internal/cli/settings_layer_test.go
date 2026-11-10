package cli

import (
	"errors"
	"strings"
	"testing"

	managedmode "github.com/dimipaun/fugaro/internal/managed"
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
