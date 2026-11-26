package testutil

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"gopkg.in/yaml.v3"
)

// TestFixturePinsMovesWhatTheFileWrote: the models a test writes in the
// fixture's file move to the layer, and the file keeps everything else it
// wrote, down to its other agent keys.
func TestFixturePinsMovesWhatTheFileWrote(t *testing.T) {
	cfg := `version: 1
project: aurora
agent:
  auth: api-key
  review_rounds: 2
  model: claude-sonnet-5-5
  models: { coder: claude-sonnet-5-5, reviewer: claude-opus-5 }
  max_output_tokens: { coder: 4096 }
workflows:
  app:
    commands: { test: sh test.sh }
`
	file, layer := FixturePins(cfg, config.LiftedKeyPaths())
	if strings.Contains(file, "model: claude-sonnet-5-5") || strings.Contains(file, "models:") {
		t.Errorf("the file still names the pins:\n%s", file)
	}
	if !strings.Contains(file, "review_rounds: 2") || !strings.Contains(file, "max_output_tokens: {coder: 4096}") {
		t.Errorf("the file lost a key of its own:\n%s", file)
	}
	if !strings.Contains(file, "gcp_project: "+PinsGCPProject) {
		t.Errorf("the file is not anchored to the layer it is priced by:\n%s", file)
	}
	for _, want := range []string{"project: aurora", "gcp_project: " + PinsGCPProject, "defaults:",
		"model: claude-sonnet-5-5", "coder: claude-sonnet-5-5", "reviewer: claude-opus-5"} {
		if !strings.Contains(layer, want) {
			t.Errorf("the layer does not name %q:\n%s", want, layer)
		}
	}
}

// TestFixturePinsGivesTheBareFixtureItsRoles: a file that names no model is
// priced by the harness's own three roles, in the layer.
func TestFixturePinsGivesTheBareFixtureItsRoles(t *testing.T) {
	file, layer := FixturePins(FixtureFiles(t)["fugaro.yaml"], config.LiftedKeyPaths())
	if strings.Contains(file, "models:") {
		t.Errorf("the file names pins of its own:\n%s", file)
	}
	for _, want := range []string{"coder: claude-sonnet-5-5", "reviewer: claude-opus-5", "background: claude-haiku-4-5"} {
		if !strings.Contains(layer, want) {
			t.Errorf("the layer does not name %q:\n%s", want, layer)
		}
	}
	if !strings.Contains(layer, "project: aurora") {
		t.Errorf("the layer is not anchored to the file's project:\n%s", layer)
	}
}

// TestFixturePinsLeavesTheFilesThatAskForNoPins: the shapes the harnesses leave
// alone stay alone — an empty models block (a test about the pins rule), an
// oauth file (the credential the gateway does not carry), a fragment with no
// agent block, and a file that names no project, which no layer applies to.
func TestFixturePinsLeavesTheFilesThatAskForNoPins(t *testing.T) {
	fixture := FixtureFiles(t)["fugaro.yaml"]
	for name, cfg := range map[string]string{
		"empty models": strings.Replace(fixture, "agent:\n", "agent:\n  models: {}\n", 1),
		"oauth":        strings.Replace(fixture, "auth: api-key", "auth: oauth", 1),
		"a layer":      "version: 1\nproject: aurora\ngcp_project: " + PinsGCPProject + "\ndefaults: {}\n",
		"no project":   strings.Replace(fixture, "project: aurora\n", "", 1),
		"not a config": "project: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			file, layer := FixturePins(cfg, config.LiftedKeyPaths())
			if layer != "" {
				t.Errorf("a layer was installed over %q:\n%s", name, layer)
			}
			if !strings.Contains(file, "agent:") && name != "a layer" && name != "not a config" {
				t.Errorf("the file was emptied rather than left alone:\n%s", file)
			}
		})
	}
}

// TestSanctionAgentStatesTheFilesKeys: the tighten-only agent keys a fixture's
// file writes are written into the layer that prices it, so a run behind that
// layer takes the number the fixture wrote rather than the mode's own row (RT1,
// R20, RT2) — and a key the layer names itself stays exactly as the test wrote
// it, since the layer's own word is what wins in the merge.
func TestSanctionAgentStatesTheFilesKeys(t *testing.T) {
	const file = `version: 1
project: aurora
agent:
  auth: api-key
  review_rounds: 2
  first_line_rounds: 1
  levels: { l3: { none: true } }
  max_output_tokens: { coder: 4096 }
`
	const layer = "version: 1\nproject: aurora\ngcp_project: " + PinsGCPProject +
		"\ndefaults:\n  agent:\n    models: { coder: claude-sonnet-5-5 }\n    levels: { l2: { none: true } }\n"
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(SanctionAgent(file, layer)), &doc); err != nil {
		t.Fatal(err)
	}
	agent := doc["defaults"].(map[string]any)["agent"].(map[string]any)
	if agent["review_rounds"] != 2 || agent["first_line_rounds"] != 1 {
		t.Errorf("the layer does not carry the fixture's numbers: %v", agent)
	}
	levels := agent["levels"].(map[string]any)
	if levels["l3"] == nil || levels["l2"] == nil {
		t.Errorf("the sanction took a level the layer stated or missed the one it did not: %v", levels)
	}
	if agent["max_output_tokens"] != nil || agent["auth"] != nil {
		t.Errorf("the sanction carried a key that is not tighten-only: %v", agent)
	}
	// A key the layer states itself is the layer's: the sanction fills holes, it
	// does not decide.
	stated := SanctionAgent(file, strings.Replace(layer, "models:", "review_rounds: 5\n    models:", 1))
	if !strings.Contains(stated, "review_rounds: 5") || strings.Contains(stated, "review_rounds: 2") {
		t.Errorf("the sanction overwrote the layer's own number:\n%s", stated)
	}
}

// TestTakeFilePinsKeepsACraftsAccessAndALevelsEffort: the model is the only
// half of a craft and a level a repository's file may no longer decide, so the
// other half stays in the file and the model moves.
func TestTakeFilePinsKeepsACraftsAccessAndALevelsEffort(t *testing.T) {
	file, layer := TakeFilePins(`version: 1
project: aurora
agent:
  auth: api-key
  crafts:
    coder: { model: claude-sonnet-5-5, access: work }
  levels:
    l2: { model: claude-opus-5, effort: high, focus: security }
`, config.LiftedKeyPaths())
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(file), &doc); err != nil {
		t.Fatal(err)
	}
	agent := doc["agent"].(map[string]any)
	crafts := agent["crafts"].(map[string]any)["coder"].(map[string]any)
	if _, ok := crafts["model"]; ok || crafts["access"] != "work" {
		t.Errorf("the craft did not keep exactly its access: %v", crafts)
	}
	levels := agent["levels"].(map[string]any)["l2"].(map[string]any)
	if _, ok := levels["model"]; ok || levels["effort"] != "high" {
		t.Errorf("the level did not keep exactly its effort: %v", levels)
	}
	for _, want := range []string{"coder:", "model: claude-sonnet-5-5", "l2:", "model: claude-opus-5"} {
		if !strings.Contains(layer, want) {
			t.Errorf("the layer does not carry %q:\n%s", want, layer)
		}
	}
}

// TestTakeFilePinsMovesRigorAndTheGrades: the level, the areas and the grades'
// models lift with the models (S1, D3237 B11 and L13), the blocks the lift
// emptied go out with them — a migrated file carries no rigor: line — and what
// is not lifted stays where the test put it.
func TestTakeFilePinsMovesRigorAndTheGrades(t *testing.T) {
	file, layer := TakeFilePins(`version: 1
project: aurora
agent:
  auth: api-key
rigor:
  default: startup
  areas:
    - { name: payments, match: "internal/payments/**", level: critical }
model_grades:
  strong: { model: claude-opus-5, access: work }
`, config.LiftedKeyPaths())
	if strings.Contains(file, "rigor:") || strings.Contains(file, "claude-opus-5") {
		t.Errorf("the file still names a lifted key:\n%s", file)
	}
	if !strings.Contains(file, "access: work") {
		t.Errorf("the file lost a key of its own:\n%s", file)
	}
	for _, want := range []string{"rigor:", "default: startup", "areas:", "payments", "internal/payments/**",
		"level: critical", "model_grades:", "strong:", "model: claude-opus-5"} {
		if !strings.Contains(layer, want) {
			t.Errorf("the layer does not carry %q:\n%s", want, layer)
		}
	}
	if strings.Contains(layer, "access: work") {
		t.Errorf("the layer carries a key that did not lift:\n%s", layer)
	}
	if !strings.Contains(layer, "coder: claude-sonnet-5-5") {
		t.Errorf("the layer names no model to price the run with:\n%s", layer)
	}
}

// TestTakeFilePinsLeavesAnEmptyRigorBlock: R2's opt-in is a rigor: block a
// repository writes with nothing in it, and it stays — the lift takes out only
// the blocks it emptied itself.
func TestTakeFilePinsLeavesAnEmptyRigorBlock(t *testing.T) {
	file, _ := TakeFilePins(`version: 1
project: aurora
agent: { auth: api-key, model: claude-sonnet-5-5 }
rigor: {}
`, config.LiftedKeyPaths())
	if !strings.Contains(file, "rigor: {}") {
		t.Errorf("the file lost the empty block it wrote:\n%s", file)
	}
}
