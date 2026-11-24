package testutil

import (
	"bytes"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"gopkg.in/yaml.v3"
)

// PinsGCPProject is the GCP project the fixture's own file does not name and a
// project layer must: a layer is refused without one (config.GCPProjectRE) and
// applies only to a file anchored to the same one (config.ProjectLayer.Applies,
// on the project and the GCP project together).
const PinsGCPProject = "proj-1234"

// The three roles a run behind a gateway must name to be priced at all
// (CheckPins, D3020): the coder and the first line on sonnet, the senior review
// on opus, the background model on haiku.
var roleModels = map[string]string{
	"coder": "claude-sonnet-5-5", "reviewer": "claude-opus-5", "background": "claude-haiku-4-5",
}

// TakeFilePins lifts the keys a repository's file may no longer write out of it
// and returns the file without them and, in layer, the text of a project layer
// carrying them under defaults: — "" when the file named none, or is not a
// repository's own file at all.
//
// It exists because a repository's file may no longer decide any of them (S1,
// lift L8, D3237): config.LiftedKeys is the list — the model a role, a craft or
// a review level runs as, the escalation grades' models, the rigor default and
// its areas, and ab.rate — and config.Resolve drops every one of them before
// the merge, so a fixture that seeds them in the file would otherwise run a
// configuration without them: no model to price the run with, no level of its
// own. A real installation's values arrive from the project layer
// (config.ProjectLayer), and this puts a fixture's there, in the file's own
// project and GCP project — the anchor a layer applies by.
//
// The file keeps every other key it writes: the lift is key by key, so a
// craft's access, a level's effort and a grade's access stay where the test put
// them, and a block the lift emptied is taken out with it, which is the shape a
// migrated file has.
//
// ab.rate is the one lifted key left where it is: its home is the layer's own
// top-level ab: block (AB1, D3237 B9) rather than a key under defaults:, and no
// fixture prices a run from it — a repository's file may only write it as the
// opt-out, which the layer states as ab.repos.<owner/name>: 0.
func TakeFilePins(cfg string) (file, layer string) {
	doc, ok := parseDoc(cfg)
	if !ok {
		return cfg, ""
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cfg, ""
	}
	if mapGet(root, "defaults") != nil || projectOf(root) == "" {
		// A project layer's own text (its defaults: block), or a fragment
		// naming no project for a layer to apply to: neither is a repository's
		// own file, and a test that hands one to a harness is testing
		// something else.
		return cfg, ""
	}
	moved := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	blocks := map[string]bool{}
	for _, k := range config.LiftedKeys {
		parts := strings.Split(k.Key, ".")
		if parts[0] == "ab" {
			continue
		}
		if v := takePath(root, parts); v != nil {
			setPath(moved, parts, v)
			blocks[parts[0]] = true
		}
	}
	if len(moved.Content) == 0 {
		return cfg, ""
	}
	for name := range blocks {
		if block := mapGet(root, name); block != nil && block.Kind == yaml.MappingNode {
			pruneEmpty(block)
			if len(block.Content) == 0 {
				mapTake(root, name)
			}
		}
	}
	ensureRoles(moved)
	return encodeNode(doc), pinsLayer(projectOf(root), gcpProjectOf(root), moved)
}

// ensureRoles names the three roles a priced run needs when the file's own keys
// did not: a file that lifted only a craft's model, or only a grade's, still
// has to name the coder, the reviewer and the background model, and the harness
// gives it the roles PinsLayerText would have.
func ensureRoles(defaults *yaml.Node) {
	agent := mapGet(defaults, "agent")
	if agent == nil {
		agent = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setPath(defaults, []string{"agent"}, agent)
	}
	if mapGet(agent, "model") != nil {
		return
	}
	models := mapGet(agent, "models")
	if models == nil {
		models = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapSet(agent, "models", models)
	}
	for role, model := range roleModels {
		if mapGet(models, role) == nil {
			mapSet(models, role, scalar(model))
		}
	}
}

// takePath removes and returns the value a dotted path names, nil when the tree
// holds none of it.
func takePath(root *yaml.Node, parts []string) *yaml.Node {
	cur := root
	for _, k := range parts[:len(parts)-1] {
		cur = mapGet(cur, k)
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil
		}
	}
	return mapTake(cur, parts[len(parts)-1])
}

// setPath writes value at a dotted path, making the mappings on the way.
func setPath(root *yaml.Node, parts []string, value *yaml.Node) {
	cur := root
	for _, k := range parts[:len(parts)-1] {
		next := mapGet(cur, k)
		if next == nil || next.Kind != yaml.MappingNode {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapSet(cur, k, next)
		}
		cur = next
	}
	mapSet(cur, parts[len(parts)-1], value)
}

// pruneEmpty takes every emptied mapping out of a block the lift reached: the
// file a planner writes carries no agent.models: {} and no rigor: {} line.
func pruneEmpty(m *yaml.Node) {
	keep := m.Content[:0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		v := m.Content[i+1]
		if v != nil && v.Kind == yaml.MappingNode {
			pruneEmpty(v)
			if len(v.Content) == 0 {
				continue
			}
		}
		keep = append(keep, m.Content[i], v)
	}
	m.Content = keep
}

// pinsLayer is the project layer's text: the anchor the file carries and the
// lifted keys as its defaults:, in the shape config.ProjectLayer decodes.
func pinsLayer(project, gcpProject string, defaults *yaml.Node) string {
	if gcpProject == "" {
		gcpProject = PinsGCPProject
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
// file a run is seeded with, and the project layer that prices it. The keys a
// test wrote in the file move to the layer (TakeFilePins); a file that names
// none of them gets the harness's own three roles (PinsLayerText); and the file
// is anchored to the GCP project its layer names (AnchorFile), since a layer
// applies to a file only when the two agree — the fixture's own file names
// none, as a repository's file need not.
func FixturePins(cfg string) (file, layer string) {
	file, layer = TakeFilePins(cfg)
	if layer == "" {
		layer = PinsLayerText(file)
	}
	return AnchorFile(file), layer
}

// MergePins merges the pins a fixture's file named into the project layer a test
// handed the run, and returns the layer's text: every key of the layer's own
// stays, the pins decide the models, and a layer that already names a model or
// a models block of its own is returned exactly as the test wrote it — it is
// already pricing the run, and both harnesses leave it alone (their specs write
// the pins a stage needs, as ee/governance's and internal/runner's runnerLayer
// do).
func MergePins(layer, pins string) string {
	layerDoc, ok := parseDoc(layer)
	if !ok {
		return layer
	}
	pinsDoc, ok := parseDoc(pins)
	if !ok {
		return layer
	}
	agent := mapGet(mapGet(layerDoc.Content[0], "defaults"), "agent")
	if agent != nil && (mapGet(agent, "model") != nil || mapGet(agent, "models") != nil) {
		return layer
	}
	defaults := mapGet(layerDoc.Content[0], "defaults")
	pinsDefaults := mapGet(pinsDoc.Content[0], "defaults")
	if defaults == nil {
		mapSet(layerDoc.Content[0], "defaults", pinsDefaults)
		return encodeNode(layerDoc)
	}
	mergeNode(defaults, pinsDefaults)
	return encodeNode(layerDoc)
}

// mergeNode writes src's keys into dst, recursing where both carry a mapping of
// the same name: the layer keeps every key the pins do not name.
func mergeNode(dst, src *yaml.Node) {
	if src == nil {
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		k, v := src.Content[i], src.Content[i+1]
		if have := mapGet(dst, k.Value); have != nil && have.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode {
			mergeNode(have, v)
			continue
		}
		mapSet(dst, k.Value, v)
	}
}

// parseDoc is a text as a document node, false when it does not parse or holds
// more than one document.
func parseDoc(text string) (*yaml.Node, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil || len(doc.Content) != 1 {
		return nil, false
	}
	return &doc, true
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

// mapGet returns the value at key of a mapping node, nil when it has none —
// and nil for a nil mapping, so a caller can walk a path that may not be there.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil {
		return nil
	}
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
// keys moved to is read by config.ParseProjectLayer.
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

// PinsLayerText is the project layer a fixture that names no lifted key of its
// own is priced by: the three roles a run must name, in the roles' own shape.
// It is what the two runner harnesses install so that a test handed the bare
// fixture still runs a stage at all, and it answers "" for the configurations
// those harnesses leave alone: a project layer's own text, which is not a
// repository's file at all; one that names a model or a models block at all —
// even an empty one, which is how a test about the pins rule asks for a run
// that pins nothing; one the gateway does not carry, which is every oauth run
// (D3026, R12), the only ones with no pins to price; one that does not parse,
// which is the config under test in tests of their own; and one that names no
// project, which is a file no layer applies to.
func PinsLayerText(cfg string) string {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil || len(doc.Content) != 1 {
		return ""
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || projectOf(root) == "" {
		return ""
	}
	if mapGet(root, "defaults") != nil {
		return ""
	}
	agent := mapGet(root, "agent")
	if agent != nil && agent.Kind == yaml.MappingNode {
		if mapGet(agent, "model") != nil || mapGet(agent, "models") != nil {
			return ""
		}
		if auth := mapGet(agent, "auth"); auth != nil && auth.Value == "oauth" {
			return ""
		}
	}
	models := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for role, model := range roleModels {
		mapSet(models, role, scalar(model))
	}
	agentPins := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(agentPins, "models", models)
	defaults := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(defaults, "agent", agentPins)
	return pinsLayer(projectOf(root), gcpProjectOf(root), defaults)
}
