package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// rigor_test.go is G6's half of the `fugaro rigor` group (D1169,
// docs/config-management/implementation-plan.md): `fugaro rigor set`, the
// command docs/rigor-command/spec.md §3 and §5 design, writing through
// applySettingsEdit — the one write path (settings_write.go) — as a layer write
// of this machine's own project layer file (D830).
//
// The design's own rules are what these tests hold, one test each, because every
// one of them is a refusal a later change could quietly drop:
//
//   - the rank rule (D832): a mode below what this project resolves to today is
//     the sensitive direction, so it is the `Typed` step — refused in a coding
//     agent's session with nothing written, and written at the owner's own
//     terminal with the sentence saying what it lowers (D1180, D1552). The plan
//     names this test's answer "refuses a mode below the resolved one" for the
//     session half, which is what a session's fence does with a Typed step;
//   - D834: `hotfix` is not a mode any layer sets, in either form, and the
//     refusal names `fugaro run --mode hotfix`;
//   - D838: `--area` addresses a rule by its glob, so a layer that carries one
//     glob twice is refused and a pair is never created — the loader's own
//     repeat rule (internal/config's validateRigor, F14) is the check;
//   - §3 step 4's second absence: a file that does not exist is refused, naming
//     `fugaro update`, rather than replaced. The design's first absence — no
//     file and no published layer — is the creation half, which is not built;
//     the test below says what it pins and why the second half is the one the
//     write path can carry.
//
// The world is the settings family's own: settingsWriteHome's hermetic
// installation (project aurora, gcp_project aurora-gcp) and a layer file written
// by the test at localcfg's one path — the path `fugaro update` seeds and this
// command edits (localcfg.LayerFile). Which document the file is, and what it
// must read as, is config.ParseProjectLayer's own anchor: the two anchors come
// from the selected installation, so a fixture that named another project would
// be refused before any rule below was asked.

// rigorAnchors is the project layer file's three anchor keys with nothing else:
// the shortest document this command edits, and the shape `fugaro update` seeds
// from the published layer. The anchor values are the settings family's own
// project aurora and its GCP project aurora-gcp (settingsFixture).
const rigorAnchors = "version: 1\nproject: aurora\ngcp_project: aurora-gcp\n"

// rigorAnchor is the anchor the layer file a settingsWriteHome test writes is
// read against: the selected project's own name and GCP project.
var rigorAnchor = config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"}

// rigorReadsBack holds an edited document against the loader the write path
// itself re-reads with (settingsReparse): a document this command writes is one
// ParseProjectLayer accepts against the same anchor the publisher uses.
func rigorReadsBack(t *testing.T, text string) {
	t.Helper()
	if _, ps := config.ParseProjectLayer([]byte(text), rigorAnchor); len(ps) > 0 {
		t.Fatalf("the written file is not a project layer: %s\n%s", ps[0].String(), text)
	}
}

// rigorMode is the mode the layer file a test seeds sets.
func rigorModeSet(t *testing.T, text, mode string) string {
	t.Helper()
	return settingsManagedLayerFile(t, text+"defaults:\n  rigor:\n    default: "+mode+"\n")
}

// The red step, first half: a mode that ranks BELOW what this project resolves
// to today is the sensitive direction (D832), and the sensitive direction is the
// `Typed` step (D1180): a coding agent's session is refused it whatever --yes
// says, with the diff and the command a person runs themselves, and nothing is
// written (SD9). The second half is the door it does have: at the owner's own
// terminal the same write lands, with the sentence saying what it lowers printed
// beside the diff (D1552: a Typed step asks nothing, and a session is the fence
// it keeps).
//
// The baseline is a RESOLUTION and not the key the file happens to hold — the
// design's own point — but with no --repo and no --path the inputs are this
// layer's default and its own rules, so `critical` resolving today is what makes
// `startup` a lowering.
func TestRigorSetRefusesAModeBelowTheResolvedOne(t *testing.T) {
	settingsWriteHome(t)
	path := rigorModeSet(t, rigorAnchors, "critical")
	before := readFileString(t, path)

	agentSession(t)
	out, _, err := execute(t, "rigor", "set", "startup", "--yes")
	if err == nil {
		t.Fatalf("a coding agent's session lowered the project's rigor mode:\n%s", out)
	}
	if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the refusal does not name the session: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro rigor set startup") {
		t.Errorf("the refusal does not print the command to run: %v", err)
	}
	if !strings.Contains(out, "default: critical") {
		t.Errorf("the refused write printed no diff:\n%s", out)
	}
	if !strings.Contains(err.Error(), "defaults.rigor.default") {
		t.Errorf("the refusal does not name the key it refused: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}

	noAgentSession(t)
	noTerminal(t)
	out, _, err = execute(t, "rigor", "set", "startup")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused a lowering: %v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "lowers") {
		t.Errorf("the write does not say what it lowers:\n%s", out)
	}
	got := readFileString(t, path)
	if !strings.Contains(got, "default: startup") {
		t.Errorf("the layer file does not carry the lowered mode:\n%s", got)
	}
	if strings.Contains(got, "default: critical") {
		t.Errorf("the layer file still carries the mode it replaced:\n%s", got)
	}
}

// The red step, second half: D834's refusal. `hotfix` is off the axis, ranks
// below every on-axis mode, and is an operator's own run mode (D12), so no layer
// may set it — as a default or as an area rule — and the refusal names the one
// command that reaches it rather than offering a flag that would write it.
func TestRigorSetRefusesHotfix(t *testing.T) {
	settingsWriteHome(t)
	path := rigorModeSet(t, rigorAnchors, "pro")
	before := readFileString(t, path)
	noAgentSession(t)
	noTerminal(t)

	for _, args := range [][]string{
		{"rigor", "set", "hotfix"},
		{"rigor", "set", "hotfix", "--area", "docs/**", "--name", "docs"},
	} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v wrote hotfix into the layer file:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "fugaro run --mode hotfix") {
			t.Errorf("%v: the refusal does not name the operator's own door: %v", args, err)
		}
	}
	if readFileString(t, path) != before {
		t.Error("a refused hotfix changed the layer file")
	}
}

// The fence a raising write keeps, which is the one arm of it nothing else here
// reads. A mode below the resolved one is refused in a session by the `Typed`
// class before the session is consulted, and a managed project's session is
// refused by the gate's allowlist, so both of those refusals hold even if the
// document were read as a repository's own file. What turns a session away from
// this write is that the file is NOT one: it is this machine's project layer
// file, outside every checkout, and a session may not write it in any direction.
// The document is at `pro` and the mode asked for is `critical`, so nothing else
// in this command has a reason to refuse it.
func TestRigorSetRefusesARaisingWriteInAnAgentSession(t *testing.T) {
	settingsWriteHome(t)
	path := rigorModeSet(t, rigorAnchors, "pro")
	before := readFileString(t, path)
	agentSession(t)

	for _, args := range [][]string{
		{"rigor", "set", "critical"},
		{"rigor", "set", "critical", "--yes"},
		{"rigor", "set", "critical", "--area", "docs/**", "--name", "docs"},
	} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v wrote to the project layer file from a coding agent's session:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "coding agent's session") {
			t.Errorf("%v: the refusal does not name the session: %v", args, err)
		}
		if !strings.Contains(out, "default: critical") && !strings.Contains(out, "match: docs/**") {
			t.Errorf("%v: the refused write printed no diff:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "fugaro rigor set") {
			t.Errorf("%v: the refusal does not print the command to run: %v", args, err)
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("%v: the refused write changed the layer file:\n%s", args, got)
		}
	}
}

// The red step, third part: the ordinary write. A mode at or above what the
// project resolves to is written into this machine's own project layer file
// (D830), through the shared write path: the document is re-read strictly, the
// old bytes are kept beside it, and every other byte of the file — a block, its
// comments — is left exactly as it was, which is what yamledit is for. The
// command ends with `next: fugaro update`, the stage that publishes it (D831),
// and it prints the review floor beside the write: the floor is outside every
// layer, this command cannot lower it, and `fugaro rigor floor` is where one is
// changed (SD10).
//
// The plan's name for this test says "the local project config": the document is
// this machine's own layer file, and the name is the plan's shorthand for it.
func TestRigorSetWritesTheLocalProjectConfig(t *testing.T) {
	settingsWriteHomeWith(t, "review_floor:\n  critical: [l2, l3]\n")
	path := settingsManagedLayerFile(t, rigorAnchors+"# the layer this machine edits\ndefaults:\n  agent:\n    review_rounds: 4\n")
	before := readFileString(t, path)
	noAgentSession(t)
	noTerminal(t)

	out, _, err := execute(t, "rigor", "set", "critical")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "fugaro rigor floor") {
		t.Errorf("the write does not name the command that changes a floor:\n%s", out)
	}
	if !strings.Contains(out, "critical: l2, l3") {
		t.Errorf("the write does not print the floor this project holds:\n%s", out)
	}
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); lines[len(lines)-1] != "next: fugaro update" {
		t.Errorf("the last line is %q, want the one hand-off line", lines[len(lines)-1])
	}
	got := readFileString(t, path)
	if !strings.Contains(got, "default: critical") {
		t.Errorf("the layer file does not carry the mode:\n%s", got)
	}
	if !strings.Contains(got, "review_rounds: 4") || !strings.Contains(got, "# the layer this machine edits") {
		t.Errorf("the write did not keep the rest of the file:\n%s", got)
	}
	if !strings.Contains(before, "review_rounds: 4") {
		t.Fatal("the fixture did not write the block this test is about")
	}
	rigorReadsBack(t, got)
}

// The vocabulary is internal/rigor's, refused in the words `fugaro run --mode`
// refuses it in (§3 step 1): a near miss is not a mode, and nothing is written.
func TestRigorSetRefusesAVocabularyNearMiss(t *testing.T) {
	settingsWriteHome(t)
	path := rigorModeSet(t, rigorAnchors, "pro")
	before := readFileString(t, path)
	noAgentSession(t)
	noTerminal(t)

	for _, near := range []string{"Pro", "critical ", "crit", ""} {
		out, _, err := execute(t, "rigor", "set", near)
		if err == nil {
			t.Fatalf("%q was written as a rigor mode:\n%s", near, out)
		}
		if !strings.Contains(err.Error(), "poc, startup, pro, critical, hotfix") {
			t.Errorf("%q: the refusal does not print the vocabulary: %v", near, err)
		}
	}
	if readFileString(t, path) != before {
		t.Error("a refused mode changed the layer file")
	}
}

// A file that does not exist is refused, and no byte is created where it would
// be. The refusal this pins is the one write path's own sentence, and the
// absence it is written for is §3 step 4's second: a file absent while the
// project HAS a published layer, where publishing a fresh, nearly empty one over
// the project's real layer is the mistake this command must not make, and the
// remedy is `fugaro update`, whose Layer stage seeds the file from that layer
// (D831, merged). The design's first absence — no file and no published layer,
// where there is nothing to seed from — is the creation half, and it is not
// built: the one write path compares and swaps against a file it re-reads and
// backs that file up, so it cannot create one, and the plan that owns that path
// (settings S5, re-entered by G7 and S7) forbids a second copy of its refusal
// and its write in this file. G6's pull request reports the gap rather than
// choosing where the creation belongs.
func TestRigorSetRefusesWithNoLocalLayerFile(t *testing.T) {
	settingsWriteHome(t)
	path := localLayerPath(t)
	noAgentSession(t)
	noTerminal(t)

	out, _, err := execute(t, "rigor", "set", "critical")
	if err == nil {
		t.Fatalf("a write with no layer file to edit succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "fugaro update") || !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the file and the remedy: %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the refusal created the file it refused to edit")
	}
}

// §5: a rule is addressed by its glob. A glob the layer does not carry is added
// and the rule then needs a name — it is what the run record and the pull
// request print (rigor.Area's own reason) — and a rule that has one is written
// whole, in the layer's own format, with every other byte of the file kept.
func TestRigorSetAreaAddsARuleAndRequiresAName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsManagedLayerFile(t, rigorAnchors+"defaults:\n  agent:\n    review_rounds: 4\n")
	before := readFileString(t, path)
	noAgentSession(t)
	noTerminal(t)

	out, _, err := execute(t, "rigor", "set", "critical", "--area", "docs/**")
	if err == nil {
		t.Fatalf("a rule with no name was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("the refusal does not name the flag that supplies one: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}

	out, _, err = execute(t, "rigor", "set", "critical", "--area", "docs/**", "--name", "docs")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, path)
	if !strings.Contains(got, "areas:") || !strings.Contains(got, "match: docs/**") ||
		!strings.Contains(got, "name: docs") || !strings.Contains(got, "mode: critical") {
		t.Errorf("the layer file does not carry the rule:\n%s", got)
	}
	if !strings.Contains(got, "review_rounds: 4") {
		t.Errorf("the write did not keep the rest of the file:\n%s", got)
	}
	rigorReadsBack(t, got)

	// A name without a glob names nothing: the rule a name belongs to is the one
	// the glob addresses.
	_, _, err = execute(t, "rigor", "set", "critical", "--name", "docs")
	if err == nil || !strings.Contains(err.Error(), "--area") {
		t.Fatalf("--name without --area was accepted: %v", err)
	}
}

// A glob the layer already carries has its mode replaced — one rule, its name
// kept — and the replacement is a lowering when the rule's own paths resolve
// higher today, which is the same rank rule read over the rule's own input
// (§5, D832).
func TestRigorSetAreaReplacesTheModeOfAnExistingGlob(t *testing.T) {
	settingsWriteHome(t)
	path := settingsManagedLayerFile(t, rigorAnchors+
		"defaults:\n  rigor:\n    areas:\n      - match: docs/**\n        mode: critical\n        name: docs\n")
	noAgentSession(t)
	noTerminal(t)

	out, _, err := execute(t, "rigor", "set", "critical", "--area", "docs/**", "--name", "the docs")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, path)
	if strings.Count(got, "match: docs/**") != 1 {
		t.Errorf("the write added a second rule for one glob:\n%s", got)
	}
	if !strings.Contains(got, "name: the docs") {
		t.Errorf("--name on an existing rule did not rename it:\n%s", got)
	}

	// The mode a rule sets is a floor over its paths: lowering it in a coding
	// agent's session is refused, exactly as lowering the default is.
	agentSession(t)
	before := readFileString(t, path)
	_, _, err = execute(t, "rigor", "set", "poc", "--area", "docs/**")
	if err == nil || !strings.Contains(err.Error(), "coding agent's session") {
		t.Fatalf("a coding agent's session lowered an area rule's mode: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}
}

// D838 with F14: two rules with one glob in a layer are refused, and by the
// loader's own repeat rule rather than by a second table here — a glob addresses
// one rule, so an edit that cannot tell which one it is addressing writes
// nothing. The refusal is the one the file's own reader prints.
func TestRigorSetAreaRefusesATwinGlob(t *testing.T) {
	settingsWriteHome(t)
	path := settingsManagedLayerFile(t, rigorAnchors+
		"defaults:\n  rigor:\n    areas:\n      - match: docs/**\n        mode: critical\n        name: docs\n"+
		"      - match: docs/**\n        mode: pro\n        name: docs again\n")
	before := readFileString(t, path)
	noAgentSession(t)
	noTerminal(t)

	_, _, err := execute(t, "rigor", "set", "poc", "--area", "docs/**")
	if err == nil {
		t.Fatal("a layer carrying one glob twice was edited")
	}
	if !strings.Contains(err.Error(), "docs/**") || !strings.Contains(err.Error(), "repeats") {
		t.Errorf("the refusal does not name the glob this edit cannot address: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}
}

// §5's shape rules are rigor.Area's own, and this command reads them rather than
// restating them: a glob that is absolute, that walks up out of the repository,
// or that is not a doublestar pattern, and a name outside areaNameRE, are each
// refused with the rule's own words, and nothing is written.
func TestRigorSetAreaRefusesAGlobOutsideTheRepository(t *testing.T) {
	settingsWriteHome(t)
	path := settingsManagedLayerFile(t, rigorAnchors)
	before := readFileString(t, path)
	noAgentSession(t)
	noTerminal(t)

	for _, tc := range []struct{ glob, name, want string }{
		{"/etc/**", "etc", "relative"},
		{"../secrets/**", "secrets", ".."},
		{"docs/[", "docs", "glob"},
		{"docs/**", "docs\nname", "name"},
	} {
		out, _, err := execute(t, "rigor", "set", "critical", "--area", tc.glob, "--name", tc.name)
		if err == nil {
			t.Fatalf("--area %q --name %q was written:\n%s", tc.glob, tc.name, out)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--area %q --name %q: the refusal does not carry %q: %v", tc.glob, tc.name, tc.want, err)
		}
	}
	if readFileString(t, path) != before {
		t.Error("a refused rule changed the layer file")
	}
}

// The layer file is the document the managed gate judges (M0) and its answer is
// recorded (M6, D1014), so a managed project's coding agent's session reaches the
// gate rather than only this command's fence — and the gate's own sentence is
// what refuses it. What the gate admits here is its allowlist's business
// (settingsManagedKeys, D1027): the rigor keys are not on it, so the refusal is
// the allowlist's, and the record of it lands in the runs bucket.
func TestRigorSetManagedRefusesTheKeyInAnAgentSession(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored+"defaults:\n  rigor:\n    default: pro\n")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "rigor", "set", "critical")
	if err == nil {
		t.Fatalf("a managed project's session wrote the rigor mode:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not a key a coding agent's session writes") {
		t.Errorf("the gate's own refusal is not what refused the write: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the layer file")
	}
	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("%d gate records, want the one refusal of this write: %+v", len(recs), recs)
	}
	if !strings.HasSuffix(recs[0].Command, " rigor set") {
		t.Errorf("the record names %q, want the command a person types", recs[0].Command)
	}
}
