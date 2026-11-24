package testutil

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// PinsGCPProject is the GCP project the fixture's own file does not name and a
// project layer must: a layer is refused without one (config.GCPProjectRE) and
// applies only to a file anchored to the same one (config.ProjectLayer.Applies,
// on the project and the GCP project together).
const PinsGCPProject = "proj-1234"

// TakeFilePins lifts the model pins a fixture file writes out of the file and
// returns the file without them and, in Layer, the text of a project layer
// carrying them under defaults: — "" when the file named none, or is not a
// repository's own file at all.
//
// It exists because a repository's file may no longer decide a run's models
// (S1, lift L8, D3237): config.LiftedKeys lists agent.model, agent.models.*,
// the crafts' and the review levels' and the escalation grades' models, and
// config.Resolve drops every one of them before the merge, so a fixture that
// both harnesses pin in the file — internal/runner's gwConfig and
// withFixturePins, internal/runner/runnertest's own copy — would otherwise run
// a configuration with no models and be refused at bootstrap. A real
// installation's pins arrive from the project layer's defaults
// (config.ProjectLayer); this puts a fixture's there, in the file's own
// project and GCP project — the anchor a layer applies by.
//
// The file keeps every other key it writes: the pins are the four model paths
// alone, taken key by key, so a craft's access and a level's effort stay where
// the test put them.
func TakeFilePins(cfg string) (file, layer string) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil || len(doc.Content) != 1 {
		return cfg, ""
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cfg, ""
	}
	agent := mapGet(root, "agent")
	if agent == nil || agent.Kind != yaml.MappingNode {
		// A layer or a fragment: nothing here is a repository's own file, and a
		// test that hands one to a harness is testing something else.
		return cfg, ""
	}
	agentPins := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if v := mapGet(agent, "model"); v != nil {
		mapTake(agent, "model")
		mapSet(agentPins, "model", v)
	}
	// An empty models block is left where it is: it is how a test asks for a
	// run that pins nothing at all (the bootstrap refusal the pins rule makes),
	// and a block with no key in it says nothing to a layer either.
	if v := mapGet(agent, "models"); v != nil && (v.Kind != yaml.MappingNode || len(v.Content) > 0) {
		mapTake(agent, "models")
		mapSet(agentPins, "models", v)
	}
	for _, k := range []string{"crafts", "levels"} {
		if sub := mapGet(agent, k); sub != nil && sub.Kind == yaml.MappingNode {
			if models := modelsUnder(sub); models != nil {
				mapSet(agentPins, k, models)
			}
		}
	}
	grades := mapGet(root, "model_grades")
	var gradeModels *yaml.Node
	if grades != nil && grades.Kind == yaml.MappingNode {
		gradeModels = modelsUnder(grades)
	}
	if len(agentPins.Content) == 0 && gradeModels == nil {
		return cfg, ""
	}
	file = encodeNode(&doc)
	if len(agentPins.Content) == 0 {
		agentPins = nil
	}
	gcp := gcpProjectOf(root)
	if gcp == "" {
		gcp = PinsGCPProject
	}
	layer = pinsLayer(projectOf(root), gcp, agentPins, gradeModels)
	return file, layer
}

// modelsUnder returns a mapping of the same keys with each one's model: kept,
// or nil when none of them writes one.
func modelsUnder(block *yaml.Node) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(block.Content); i += 2 {
		v := block.Content[i+1]
		if v == nil || v.Kind != yaml.MappingNode {
			continue
		}
		if m := mapTake(v, "model"); m != nil {
			inner := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapSet(inner, "model", m)
			out.Content = append(out.Content, block.Content[i], inner)
		}
	}
	if len(out.Content) == 0 {
		return nil
	}
	return out
}

// pinsLayer is the project layer's text: the anchor the file carries and the
// pins under defaults:, in the shape config.ProjectLayer decodes.
func pinsLayer(project, gcpProject string, agentPins, gradeModels *yaml.Node) string {
	defaults := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if agentPins != nil {
		mapSet(defaults, "agent", agentPins)
	}
	if gradeModels != nil {
		mapSet(defaults, "model_grades", gradeModels)
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(root, "version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	mapSet(root, "project", scalar(project))
	mapSet(root, "gcp_project", scalar(gcpProject))
	mapSet(root, "defaults", defaults)
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return encodeNode(doc)
}

// FixturePins is what both runner harnesses do to a test's configuration: the
// file a run is seeded with, and the project layer that prices it. The pins a
// test wrote in the file move to the layer (TakeFilePins); a file that names
// none gets the harness's own three roles (PinsLayerText); and the file is
// anchored to the GCP project its layer names (AnchorFile), since a layer
// applies to a file only when the two agree — the fixture's own file names
// none, as a repository's file need not.
func FixturePins(cfg string) (file, layer string) {
	file, layer = TakeFilePins(cfg)
	if layer == "" {
		layer = PinsLayerText(file)
	}
	return AnchorFile(file), layer
}

// AnchorFile gives a fixture's file the GCP project its layer names when the
// file names none, and leaves it alone otherwise: the fixture's own file names
// none, as a repository's file need not, and a layer applies to a file only
// when both name the same project and GCP project.
func AnchorFile(cfg string) string {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil || len(doc.Content) != 1 {
		return cfg
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || mapGet(root, "gcp_project") != nil || mapGet(root, "project") == nil {
		return cfg
	}
	// After project:, the line the two are read together on.
	at := len(root.Content) - 2
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "project" {
			at = i + 2
			break
		}
	}
	kv := []*yaml.Node{scalar("gcp_project"), scalar(PinsGCPProject)}
	rest := append(append([]*yaml.Node{}, root.Content[:at]...), kv...)
	root.Content = append(rest, root.Content[at:]...)
	return encodeNode(&doc)
}

// projectOf and gcpProjectOf are the file's own anchor, read off the tree the
// caller is holding: the layer this returns is built to apply to it, so an
// unreadable one is left empty for the layer's own validation to report.
func projectOf(root *yaml.Node) string {
	if v := mapGet(root, "project"); v != nil {
		return v.Value
	}
	return ""
}

func gcpProjectOf(root *yaml.Node) string {
	if v := mapGet(root, "gcp_project"); v != nil {
		return v.Value
	}
	return ""
}

// mapGet returns the value at key of a mapping node, nil when it has none.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapTake removes and returns the value at key, nil when the mapping has none.
func mapTake(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return v
		}
	}
	return nil
}

// mapSet sets key to value, replacing a key the mapping already has.
func mapSet(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, scalar(key), value)
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// encodeNode writes a document out at the fixture's own indentation: the file
// a test built by string replacement goes to a repository, and the layer its
// pins moved to is read by config.ParseProjectLayer.
func encodeNode(doc *yaml.Node) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return ""
	}
	if err := enc.Close(); err != nil {
		return ""
	}
	return buf.String()
}

// PinsLayerText is the project layer a fixture that names no model of its own
// is priced by: the three roles a run must name (the coder and the first line
// on sonnet, the senior review on opus, the background model on haiku), in the
// roles' own shape. It is what the two runner harnesses install so that a test
// handed the bare fixture still runs a stage at all, and it answers "" for the
// configurations those harnesses leave alone: a file with no agent block, which
// is a layer or a fragment rather than a repository's own file; one that names
// a model or a models block at all — even an empty one, which is how a test
// about the pins rule asks for a run that pins nothing; one the gateway does
// not carry, which is every oauth run (D3026, R12), the only ones with no pins
// to price; one that does not parse, which is the config under test in tests of
// their own; and one that names no project, which is a file no layer applies
// to.
func PinsLayerText(cfg string) string {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil || len(doc.Content) != 1 {
		return ""
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || projectOf(root) == "" {
		return ""
	}
	agent := mapGet(root, "agent")
	if agent == nil || agent.Kind != yaml.MappingNode {
		return ""
	}
	if mapGet(agent, "model") != nil || mapGet(agent, "models") != nil {
		return ""
	}
	if auth := mapGet(agent, "auth"); auth != nil && auth.Value == "oauth" {
		return ""
	}
	models := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for key, model := range map[string]string{
		"coder": "claude-sonnet-5-5", "reviewer": "claude-opus-5", "background": "claude-haiku-4-5",
	} {
		mapSet(models, key, scalar(model))
	}
	agentPins := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(agentPins, "models", models)
	gcp := gcpProjectOf(root)
	if gcp == "" {
		gcp = PinsGCPProject
	}
	return pinsLayer(projectOf(root), gcp, agentPins, nil)
}
