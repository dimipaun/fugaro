package cli

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
	"gopkg.in/yaml.v3"
)

// This file is SM3's own half of the metadata backfill
// (docs/console-improvements/implementation-plan.md, D2908): the family is the
// project layer's keys and a repository's workflows.*, whose entries live in
// internal/settings/meta_layer.go and meta_workflows.go. The two tests here are
// the plan's: the first is the completeness the console needs, the second is
// the proof that what the entries tell a console to offer is what the write
// path accepts — settingsReparse is the strict decode `settings set` re-reads
// the bytes it wrote with (settings_set.go), so a bound this file sets is a
// bound that decode takes.

// settingsMetaLayerKey reports whether key is one of this family's: a key the
// generic path writes that belongs to the project layer's own file or to a
// repository's workflows block.
func settingsMetaLayerKey(key string) bool {
	return settingsGenericKey(key) && (strings.HasPrefix(key, "layer.") || strings.HasPrefix(key, "workflows."))
}

// settingsMetaLayerFamily is every key of the family, in key order: the
// spellings settingsGenericKeys names — the profiles, the A/B keys, the jobs
// and the entry blocks a write adds a member to — together with every registry
// row the family's prefixes reach (layer.defaults., layer.governance.). The
// rows are read rather than listed, so a key added to the registry and to the
// allowlist is in the family the day it lands, and the completeness test fails
// on it until its entry does.
func settingsMetaLayerFamily() []string {
	set := map[string]bool{}
	for key := range settingsGenericKeys {
		if settingsMetaLayerKey(key) {
			set[key] = true
		}
	}
	for _, row := range settings.Rows() {
		if settingsMetaLayerKey(row.Key) {
			set[row.Key] = true
		}
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// TestSettingsMetaLayerComplete is the plan's acceptance for SM3: every key of
// the family carries a label of at most six words, a one-sentence description,
// a group of the group order and a type, and an enum or a bound only where the
// type may carry one. The red step is the first writable key without an entry,
// which the message below names.
func TestSettingsMetaLayerComplete(t *testing.T) {
	family := settingsMetaLayerFamily()
	if len(family) == 0 {
		t.Fatal("the family is empty: settingsGenericKey no longer allows a layer.* or workflows.* key, so this test proves nothing")
	}
	for _, key := range family {
		t.Run(key, func(t *testing.T) {
			m, ok := settings.MetaOf(key)
			if !ok {
				t.Fatalf("%s is a key settings set writes and carries no metadata: the console draws it by its raw key under its own Other group (D2908)", key)
			}
			if n := len(strings.Fields(m.Label)); n == 0 || n > 6 {
				t.Errorf("the label %q is %d words, and a label is 1 to 6", m.Label, n)
			}
			if m.Description == "" || !strings.HasSuffix(m.Description, ".") {
				t.Errorf("the description %q is not a sentence ending in a period", m.Description)
			}
			if strings.Contains(strings.TrimSuffix(m.Description, "."), ". ") {
				t.Errorf("the description %q is more than one sentence", m.Description)
			}
			want := settings.GroupProfiles
			if strings.HasPrefix(key, "workflows.") {
				want = settings.GroupWorkflows
			}
			if m.Group != want {
				t.Errorf("the group is %q, and this family's keys are drawn under %q", m.Group, want)
			}
			if !slices.Contains(settings.MetaTypes, m.Type) {
				t.Errorf("the type %q is not one of MetaTypes", m.Type)
			}
			switch m.Type {
			case settings.TypeEnum:
				if len(m.Enum) < 2 {
					t.Errorf("an enum of %v offers nothing to choose", m.Enum)
				}
				if m.Min != "" || m.Max != "" {
					t.Errorf("an enum carries the bound %q..%q", m.Min, m.Max)
				}
			default:
				if len(m.Enum) > 0 {
					t.Errorf("the type %q carries the enum %v", m.Type, m.Enum)
				}
				if m.Type != settings.TypeInt && m.Type != settings.TypeFloat && m.Type != settings.TypeDuration &&
					(m.Min != "" || m.Max != "") {
					t.Errorf("the type %q carries the bound %q..%q", m.Type, m.Min, m.Max)
				}
			}
			if m.Format != settings.FormatNone && m.Type != settings.TypeString && m.Type != settings.TypeDuration {
				t.Errorf("the format %q is on a %q, and a format is a shape of a string or a duration", m.Format, m.Type)
			}
		})
	}

	// The other direction: an entry this family's files registered under one of
	// its two prefixes is a key the console would label and the write path
	// refuses — layer.claude_models is the one such key today — which is the
	// drift a table beside the allowlist can always grow.
	for _, row := range settings.Rows() {
		if !strings.HasPrefix(row.Key, "layer.") && !strings.HasPrefix(row.Key, "workflows.") {
			continue
		}
		if _, ok := settings.MetaOf(row.Key); ok && !settingsMetaLayerKey(row.Key) {
			t.Errorf("%s carries metadata, and settings set refuses it: a key no write lands on is not this family's", row.Key)
		}
	}

	// One entry here is a copy of a vocabulary the loader owns, and it is the
	// copy a third forge has to be added to as well as to config's own list: the
	// metadata table writes the forge kinds out for the console's enum rather
	// than reading them (internal/gitprov's forgeKindGoSites is where that copy
	// is inventoried), so the two are held together here. A kind in one list and
	// not the other is a console offering a value the write refuses, or hiding
	// one it takes.
	if m, ok := settings.MetaOf("layer.defaults.git.provider"); ok && !slices.Equal(m.Enum, config.Providers) {
		t.Errorf("layer.defaults.git.provider offers %v, and the config's providers are %v", m.Enum, config.Providers)
	}
}

// TestSettingsMetaLayerBoundariesAreAccepted is the plan's proof that the
// entries match the write path: every value an entry offers is written into a
// document of the kind the key lives in and read back with settingsReparse,
// and a value just past a bound is read back as a refusal. An entry whose enum
// or bound the decode does not accept is a console offering a value the write
// refuses; an entry that declares a bound the decode does not enforce is a
// console refusing a value the write takes.
func TestSettingsMetaLayerBoundariesAreAccepted(t *testing.T) {
	for _, key := range settingsMetaLayerFamily() {
		m, ok := settings.MetaOf(key)
		if !ok {
			continue // TestSettingsMetaLayerComplete reports the missing entry.
		}
		for _, c := range settingsMetaLayerBoundaries(t, m) {
			t.Run(key+" "+c.what, func(t *testing.T) {
				err := settingsMetaLayerReparse(t, key, c.value)
				if c.refused {
					if err == nil {
						t.Fatalf("%v was accepted, and %s", c.value, c.why)
					}
					return
				}
				if err != nil {
					t.Fatalf("%v was refused, and %s: %v", c.value, c.why, err)
				}
			})
		}
	}
}

// settingsMetaLayerCase is one value the boundary test writes: the value
// itself, what the case is about, and whether the decode must refuse it.
type settingsMetaLayerCase struct {
	what    string
	value   any
	why     string
	refused bool
}

// settingsMetaLayerBoundaries is the cases one entry calls for: an enum's every
// member, and each bound of an int or a float with one step past it. A type
// with no enum and no bound — a string, a bool, a duration with no bound —
// calls for none: there is nothing in the entry a decode could disagree with.
func settingsMetaLayerBoundaries(t *testing.T, m settings.Meta) []settingsMetaLayerCase {
	t.Helper()
	var cases []settingsMetaLayerCase
	if m.Type == settings.TypeEnum {
		for _, v := range m.Enum {
			cases = append(cases, settingsMetaLayerCase{
				what:  "enum " + v,
				value: v,
				why:   "it is a value of the entry's own enum",
			})
		}
		cases = append(cases, settingsMetaLayerCase{
			what:    "outside the enum",
			value:   "not-a-value-of-the-enum",
			why:     "an enum is the closed set the entry offers, so a value outside it is refused",
			refused: true,
		})
		return cases
	}
	if m.Type != settings.TypeInt && m.Type != settings.TypeFloat {
		return cases
	}
	for _, b := range []struct {
		what string
		raw  string
		step int
	}{
		{"the minimum", m.Min, -1},
		{"the maximum", m.Max, 1},
	} {
		if b.raw == "" {
			continue
		}
		value, outside := settingsMetaLayerNumber(t, m.Type, b.raw, b.step)
		cases = append(cases,
			settingsMetaLayerCase{
				what:  b.what,
				value: value,
				why:   "it is the bound the entry itself carries",
			},
			settingsMetaLayerCase{
				what:    "past " + b.what,
				value:   outside,
				why:     "the decoder refuses a value past the bound the entry carries",
				refused: true,
			})
	}
	// A ceiling the entry does not declare is one the console does not offer:
	// a number input with no maximum takes whatever a person types, so a value
	// past every bound a reader could have must be a value the write accepts.
	// Where it is not, the entry is missing the Max that reader enforces —
	// a console offering a number `settings set` refuses at re-read, which is
	// what layer.governance.stage_budget's fraction below 1 was.
	if m.Max == "" {
		value, _ := settingsMetaLayerNumber(t, m.Type, "1000000000", 0)
		cases = append(cases, settingsMetaLayerCase{
			what:  "an undeclared ceiling",
			value: value,
			why:   "the entry declares no maximum, so the console offers none and the write takes what a person types, or the entry is missing the Max the decoder enforces",
		})
	}
	return cases
}

// settingsMetaLayerNumber reads raw as the type's own number and answers it
// with one step past it on the side step names: -1 below the value, +1 above.
// A float steps by a half, which is past every bound the entries carry
// (ab.rate's 0.1 and review_stop_fraction's 1) and never lands on one.
func settingsMetaLayerNumber(t *testing.T, typ settings.MetaType, raw string, step int) (any, any) {
	t.Helper()
	if typ == settings.TypeInt {
		n, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("the bound %q is not an integer: %v", raw, err)
		}
		return n, n + step
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("the bound %q is not a number: %v", raw, err)
	}
	return f, f + float64(step)*0.5
}

// settingsMetaLayerReparse writes value at key's path in a document of the kind
// the key lives in and runs the strict decode over the bytes. It is the write
// path's own reader: `settings set` re-reads a repository's fugaro.yaml with
// config.Parse and a project layer with config.ParseProjectLayer, which is
// exactly what settingsReparse does.
func settingsMetaLayerReparse(t *testing.T, key string, value any) error {
	t.Helper()
	target, data := settingsMetaLayerDoc(t, key, value)
	return settingsReparse(target, data)
}

// settingsMetaLayerDoc builds the document and the target the decode is run
// against: a repository's own file for workflows.*, the project layer for
// layer.*, each with the anchors its loader requires and one concrete name
// where the registry's row carries a `*` (web for a workflow, svc for a
// profile).
//
// The document holds only the key under test, which is what makes the decode
// the answer about that key: a value it takes, or a problem that names it. The
// two paths whose `*` is a list entry rather than a name — a profile's cache
// and the rigor rules of defaults.rigor.areas — are written whole, because
// those readers judge the entry's other keys together with the one under test
// (validateProfile's cache rule, rigor.Area.Validate), so a half-written entry
// would answer about the missing key instead.
func settingsMetaLayerDoc(t *testing.T, key string, value any) (settingsTarget, []byte) {
	t.Helper()
	var (
		target settingsTarget
		doc    map[string]any
		path   []string
	)
	if strings.HasPrefix(key, "workflows.") {
		doc = map[string]any{
			"version": 1,
			"project": "aurora",
			"git":     map[string]any{"provider": "github"},
			"workflows": map[string]any{
				"web": map[string]any{
					"commands": map[string]any{"build": "go build ./...", "test": "go test ./..."},
				},
			},
		}
		path = append([]string{"workflows"}, settingsMetaLayerPath(strings.TrimPrefix(key, "workflows."), "web")...)
		target = settingsTarget{layer: settings.LayerRepo, kind: settingsDocRepo, path: "/checkout/fugaro.yaml", keyPath: path, project: "aurora"}
	} else {
		doc = map[string]any{"version": 1, "project": "aurora", "gcp_project": "aurora-gcp"}
		path = settingsMetaLayerPath(strings.TrimPrefix(key, "layer."), "svc")
		target = settingsTarget{
			layer:   settings.LayerProject,
			kind:    settingsDocLayer,
			path:    "/home/layer.yaml",
			keyPath: path,
			anchor:  config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"},
			project: "aurora",
		}
	}
	settingsMetaLayerSet(doc, path, value)
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling the document for %s: %v", key, err)
	}
	return target, data
}

// settingsMetaLayerPath splits a registry spelling into the path a document
// writes, with the block's `*` replaced by name.
func settingsMetaLayerPath(rest, name string) []string {
	path := strings.Split(rest, ".")
	for i, seg := range path {
		if seg == "*" {
			path[i] = name
		}
	}
	return path
}

// settingsMetaLayerSet puts value at path, the two list-shaped paths written
// whole: one rigor rule and a profile's cache entry.
func settingsMetaLayerSet(doc map[string]any, path []string, value any) {
	// Areas is a list of rules, and Area.Validate reads the name and the match
	// before it reaches the level, so the rule is written whole and the field
	// under test replaces its placeholder.
	if slices.Equal(path[:min(4, len(path))], []string{"defaults", "rigor", "areas", "svc"}) {
		rule := map[string]any{"name": "the api", "match": "api/**", "level": "pro"}
		rule[path[4]] = value
		settingsMetaLayerPut(doc, []string{"defaults", "rigor", "areas"}, []any{rule})
		return
	}
	// A profile's cache is a list of entries, and validateProfile refuses one
	// that names no file or no path, so both members are written.
	if len(path) == 5 && path[0] == "profiles" && path[2] == "cache" {
		entry := map[string]any{"key": []any{"go.sum"}, "paths": []any{"go/pkg/mod"}}
		entry[path[4]] = value
		settingsMetaLayerPut(doc, []string{"profiles", path[1], "cache"}, []any{entry})
		return
	}
	settingsMetaLayerPut(doc, path, value)
}

// settingsMetaLayerPut is settingsValueIn's opposite: it writes value at path,
// creating the mappings on the way and merging into the ones already there, so
// a key written under a block the document already holds keeps that block's
// other members.
func settingsMetaLayerPut(doc map[string]any, path []string, value any) {
	cur := doc
	for _, seg := range path[:len(path)-1] {
		child, ok := cur[seg].(map[string]any)
		if !ok {
			child = map[string]any{}
			cur[seg] = child
		}
		cur = child
	}
	cur[path[len(path)-1]] = value
}
