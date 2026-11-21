package cli

// settings_meta_git_test.go is SM2 of docs/console-improvements/implementation-plan.md:
// the metadata of the six families `git.`, `followup.`, `clone_urls.`,
// `credentials.`, `watch.` and `governance.`, and the two facts a console needs
// of it (D2908, spec §3 and §4).
//
// The metadata itself lives beside the registry, in
// internal/settings/meta_git.go, because the console reads it from the emitted
// document rather than from this package — but the two questions that decide
// whether it is *right* are questions only this package can ask:
//
//   - which keys are the family's — settingsGenericKey and the write path's own
//     refusals (settingsWriteRow) are unexported here, and internal/settings
//     cannot import cli, so the completeness test is this file's;
//   - whether an enum or a bound the metadata claims is one the loader really
//     takes — the strict decode the write path re-reads with (settingsReparse)
//     is this package's too, and a metadata range that no file could carry
//     would draw a control whose every OK the CLI then refuses.
//
// The two tests are the plan's own: TestSettingsMetaGitComplete and
// TestSettingsMetaGitBoundariesAreAccepted.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
	"gopkg.in/yaml.v3"
)

// settingsMetaLabelWords is the plan's own ceiling on a label: at most six
// words, so a label stays a name rather than a sentence (spec §4 asks for a
// human-readable label, and a paragraph is one a person skims past).
const settingsMetaLabelWords = 6

// settingsMetaGitFamilies are the key prefixes SM2 owns. They are keys of
// settingsGenericPrefixes — the blocks spec §4.4 hands the generic write path —
// with `git.`, `followup.`, `clone_urls.` and `credentials.` added by §5.6's
// family (settings_set.go's own comment on the list).
var settingsMetaGitFamilies = []string{"git.", "followup.", "clone_urls.", "credentials.", "watch.", "governance."}

// settingsMetaGitKeys is the family this file's two tests are about: every
// registry key the generic path's allowlist carries under the six prefixes, and
// — because a key the write refuses has no control that could write it — every
// one of those a write really lands on (settingsWriteRow, the write path's own
// first half, asked here rather than restated).
//
// What that leaves out is deliberate and is D2908's own second half: a block
// (`governance.tiers`), a row another family owns (`credentials.fallback`, the
// accounts family's) and a row with a varying segment (`credentials.*.name`)
// are rows the write refuses, and a row with no metadata is drawn by its raw
// key under the console's own "Other" group. The read carries them all the same
// — the panel is the read's document — and marking a key no command may write
// with a type and a bound would offer a control whose OK the CLI refuses.
func settingsMetaGitKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	for _, row := range settings.Rows() {
		if !settingsGenericKey(row.Key) {
			continue
		}
		if !settingsMetaGitFamily(row.Key) {
			continue
		}
		if _, err := settingsWriteRow(row.Key); err != nil {
			continue
		}
		keys = append(keys, row.Key)
	}
	if len(keys) == 0 {
		t.Fatalf("no key of %v is one the write path takes: this file is probing a family that no longer exists, and both tests below would pass on an empty set", settingsMetaGitFamilies)
	}
	return keys
}

// settingsMetaGitFamily reports whether key is one of the six prefixes' own.
func settingsMetaGitFamily(key string) bool {
	for _, p := range settingsMetaGitFamilies {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// TestSettingsMetaGitComplete: every writable key of the family carries the
// four facts a console draws a row from — a label of at most six words, a
// one-sentence description, a group of the group order and a type of the type
// vocabulary. The registry's own Register already refuses an entry that names
// no group, no type or a control that cannot carry what it carries, so what is
// asked here is the half Register cannot: that a key of the family has an entry
// at all, and that the entry says something a person can read.
func TestSettingsMetaGitComplete(t *testing.T) {
	keys := settingsMetaGitKeys(t)
	for _, key := range keys {
		m, ok := settings.MetaOf(key)
		if !ok {
			t.Errorf("%s is a writable key of the family and has no metadata: a key with no entry is drawn by its raw key under the console's own group, which is what D2908 backfills away for every key a person may write", key)
			continue
		}
		if n := len(strings.Fields(m.Label)); n == 0 || n > settingsMetaLabelWords {
			t.Errorf("%s: the label %q is %d words, and a label is one to %d (spec §3)", key, m.Label, n, settingsMetaLabelWords)
		}
		if !settingsMetaOneSentence(m.Description) {
			t.Errorf("%s: the description %q is not one sentence ending in a full stop (spec §3)", key, m.Description)
		}
		if !containsMetaGroup(m.Group) {
			t.Errorf("%s: the group %q is not one of the group order, which is the whole of the vocabulary a Meta.Group may name", key, m.Group)
		}
		if !containsMetaType(m.Type) {
			t.Errorf("%s: the type %q is not one of the type vocabulary, which is the whole of what a console may branch on", key, m.Type)
		}
	}
}

// TestSettingsMetaGitBoundariesAreAccepted: every value the metadata declares
// is a value the strict decode takes. The values are read out of the entry
// itself rather than written here, so the test says what the console will
// believe: an enum's every member, a bound's own boundary on each side it
// states, and both spellings of a toggle. A bound the loader would refuse —
// `governance.stage_budget` at 1 is the one this family has — is left out of
// the entry's Max rather than stated and disproved here, because Meta.Max is
// inclusive and the loader's rule is not.
func TestSettingsMetaGitBoundariesAreAccepted(t *testing.T) {
	checked := 0
	for _, key := range settingsMetaGitKeys(t) {
		m, ok := settings.MetaOf(key)
		if !ok {
			continue // TestSettingsMetaGitComplete is the test that names this
		}
		values := settingsMetaBoundaryValues(m)
		if len(values) == 0 {
			if len(m.Enum) > 0 || m.Min != "" || m.Max != "" {
				t.Errorf("%s declares an enum or a bound and produced no value for this test to set: the entry states something a console validates by, and nothing here proves the loader agrees", key)
			}
			continue
		}
		for _, value := range values {
			target, doc := settingsMetaDocument(t, key, m, value)
			if err := settingsReparse(target, doc); err != nil {
				t.Errorf("%s = %q: the metadata declares the value and the strict decode refuses it: %v\n%s", key, value, err, doc)
				continue
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no key of the family declares an enum, a bound or a toggle: this test set nothing, and the acceptance's whole second half rests on it")
	}
}

// settingsMetaBoundaryValues is the values an entry's own members declare: the
// enum's members, the stated boundaries of a numeric or duration key, and both
// spellings of a toggle. A free-text string declares nothing a boundary test
// can set, so it answers none.
func settingsMetaBoundaryValues(m settings.Meta) []string {
	switch m.Type {
	case settings.TypeEnum:
		return m.Enum
	case settings.TypeBool:
		return []string{"true", "false"}
	case settings.TypeInt, settings.TypeFloat, settings.TypeDuration:
		var out []string
		if m.Min != "" {
			out = append(out, m.Min)
		}
		if m.Max != "" {
			out = append(out, m.Max)
		}
		return out
	}
	return nil
}

// settingsMetaDocument is the document a write of key = value lands in and the
// target that names its loader: the checkout's fugaro.yaml for a key a
// repository may state, the machine's own config for the one key here the
// repository file does not hold (watch.burn_alert_usd_per_hour). The scalar is
// written in the Go type the entry's own Type says it is, so a value the
// metadata calls an int reaches the loader as one rather than as a quoted
// string the strict decoder would refuse for the wrong reason.
func settingsMetaDocument(t *testing.T, key string, m settings.Meta, value string) (settingsTarget, []byte) {
	t.Helper()
	row, ok := settings.Lookup(key)
	if !ok {
		t.Fatalf("%s is no registry row, so no document states it", key)
	}
	base, target := settingsMetaBase(t, row)
	var tree map[string]any
	if err := yaml.Unmarshal([]byte(base), &tree); err != nil {
		t.Fatalf("the base document does not read: %v", err)
	}
	node := tree
	parts := strings.Split(key, ".")
	for _, part := range parts[:len(parts)-1] {
		child, ok := node[part].(map[string]any)
		if !ok {
			child = map[string]any{}
			node[part] = child
		}
		node = child
	}
	node[parts[len(parts)-1]] = settingsMetaScalar(t, key, m.Type, value)
	doc, err := yaml.Marshal(tree)
	if err != nil {
		t.Fatalf("%s: the document does not write: %v", key, err)
	}
	return target, doc
}

// settingsMetaScalar is value as the Go type the entry's Type names.
func settingsMetaScalar(t *testing.T, key string, ty settings.MetaType, value string) any {
	t.Helper()
	switch ty {
	case settings.TypeBool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			t.Fatalf("%s: the metadata calls %q a bool: %v", key, value, err)
		}
		return b
	case settings.TypeInt:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatalf("%s: the metadata calls %q an int: %v", key, value, err)
		}
		return n
	case settings.TypeFloat:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("%s: the metadata calls %q a float: %v", key, value, err)
		}
		return f
	}
	return value
}

// settingsMetaBase is the smallest document of the layer a key is stated in and
// the loader that reads it: `fugaro.yaml` needs a version, a project, a
// provider and one workflow with the two commands every workflow owes
// (config.Parse), and the machine's config needs its own anchors (localcfg).
func settingsMetaBase(t *testing.T, row settings.Row) (string, settingsTarget) {
	t.Helper()
	if containsLayer(row.Layers, settings.LayerRepo) || containsLayer(row.Layers, settings.LayerProject) {
		return "version: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n    commands:\n      build: go build ./...\n      test: go test ./...\n",
			settingsTarget{kind: settingsDocRepo, path: "fugaro.yaml"}
	}
	if containsLayer(row.Layers, settings.LayerLocal) {
		return "version: 1\nname: aurora\ngcp_project: aurora-gcp\nregion: us-central1\nruns_bucket: aurora-runs\n",
			settingsTarget{kind: settingsDocLocal, path: "aurora.yaml"}
	}
	t.Fatalf("%s is stated in %v and neither the checkout's file nor the machine's config holds it: this test would have to build a %s document", row.Key, row.Layers, row.Layers[0])
	return "", settingsTarget{}
}

// settingsMetaOneSentence reports whether d is one sentence: it ends in a full
// stop and holds no other sentence's end. A description with two sentences is
// the shape spec §3 asks against ("a description of what it does"), and one
// without the full stop reads as a fragment beside a value.
func settingsMetaOneSentence(d string) bool {
	if d == "" || !strings.HasSuffix(d, ".") || strings.ContainsAny(d, "\n") {
		return false
	}
	return !strings.Contains(d[:len(d)-1], ". ")
}

func containsMetaGroup(g settings.MetaGroup) bool {
	for _, want := range settings.MetaGroups {
		if want == g {
			return true
		}
	}
	return false
}

func containsMetaType(ty settings.MetaType) bool {
	for _, want := range settings.MetaTypes {
		if want == ty {
			return true
		}
	}
	return false
}

func containsLayer(layers []string, want string) bool {
	for _, l := range layers {
		if l == want {
			return true
		}
	}
	return false
}
