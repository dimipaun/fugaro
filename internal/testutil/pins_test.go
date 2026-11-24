package testutil

import (
	"strings"
	"testing"

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
	file, layer := FixturePins(cfg)
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
	file, layer := FixturePins(FixtureFiles(t)["fugaro.yaml"])
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
		"no agent":     "version: 1\nproject: aurora\nworkflows: {}\n",
		"no project":   strings.Replace(fixture, "project: aurora\n", "", 1),
		"not a config": "project: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			file, layer := FixturePins(cfg)
			if layer != "" {
				t.Errorf("a layer was installed over %q:\n%s", name, layer)
			}
			if !strings.Contains(file, "agent:") && name != "no agent" && name != "not a config" {
				t.Errorf("the file was emptied rather than left alone:\n%s", file)
			}
		})
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
`)
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
