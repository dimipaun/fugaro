package config

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// wantLifted is R1's list (docs/repo-override-rulings/reconciliation.md, the
// spec's §5) spelled out as this test's own expectation, so it is red before
// the list exists at all: B3's `agent.model`, its four role slots and its five
// crafts' models (D3291 adds the three review levels' models to the same move),
// B9's `ab.rate`, B11's two rigor keys, and, carried inside L8 by D3299, L13's
// two grade models. Each with the project home that replaces it.
//
// A `.access` row is deliberately absent: a craft's and a grade's access are
// model-credentials-routing's retired keys, which stay honoured until its
// release 2, and a repository's grade access is the merge's to drop, never this
// list's. The test below asserts both halves of that split.
var wantLifted = []LiftedKey{
	{Key: "ab.rate", Home: "ab.repos.<owner/name>"},
	{Key: "agent.crafts.coder.model", Home: "defaults.agent.crafts.coder.model"},
	{Key: "agent.crafts.merge-judge.model", Home: "defaults.agent.crafts.merge-judge.model"},
	{Key: "agent.crafts.review.model", Home: "defaults.agent.crafts.review.model"},
	{Key: "agent.crafts.security-review.model", Home: "defaults.agent.crafts.security-review.model"},
	{Key: "agent.crafts.senior-review.model", Home: "defaults.agent.crafts.senior-review.model"},
	{Key: "agent.levels.l1.model", Home: "defaults.agent.levels.l1.model"},
	{Key: "agent.levels.l2.model", Home: "defaults.agent.levels.l2.model"},
	{Key: "agent.levels.l3.model", Home: "defaults.agent.levels.l3.model"},
	{Key: "agent.model", Home: "defaults.agent.model"},
	{Key: "agent.models.background", Home: "defaults.agent.models.background"},
	{Key: "agent.models.coder", Home: "defaults.agent.models.coder"},
	{Key: "agent.models.diagnose", Home: "defaults.agent.models.diagnose"},
	{Key: "agent.models.reviewer", Home: "defaults.agent.models.reviewer"},
	{Key: "agent.models.vision", Home: "defaults.agent.models.vision"},
	{Key: "model_grades.strong.model", Home: "defaults.model_grades.strong.model"},
	{Key: "model_grades.strongest.model", Home: "defaults.model_grades.strongest.model"},
	{Key: "rigor.areas", Home: "defaults.rigor.areas"},
	{Key: "rigor.default", Home: "defaults.rigor.repos.<owner/name>"},
}

// TestLiftedKeysAreNotMCRsRetiredKeys is R1's pin, in both directions and on
// every way the two lists could be confused.
//
// A lifted key is not a retired one: marking its Scopes row Retired would break
// TestEveryRetiredKeyIsNamedWhereItIsWritten (RetiredKeys is R3's list exactly)
// and would tell a person a key had moved before `update` has moved it — while
// a Resolve that dropped every Retired: InRepo row would cut
// model-credentials-routing's bridge, which is still honoured until its X2. And
// each row has to admit the document the key is lifted out of: a key a
// repository's file may not write is not one this list can lift from it.
//
// Mutation (run, restored): add a lifted key to Scopes with a Retired bit, or
// put a craft's or a grade's `access` on the list, and this test names it.
func TestLiftedKeysAreNotMCRsRetiredKeys(t *testing.T) {
	if len(wantLifted) == 0 {
		t.Fatal("this test reads nothing")
	}
	if !slices.IsSortedFunc(LiftedKeys, func(a, b LiftedKey) int { return strings.Compare(a.Key, b.Key) }) {
		t.Errorf("LiftedKeys is not in path order: %+v", keysOf(LiftedKeys))
	}
	if len(LiftedKeys) != len(wantLifted) {
		t.Errorf("LiftedKeys holds %d entries (%+v), want %d", len(LiftedKeys), keysOf(LiftedKeys), len(wantLifted))
	}
	for i, want := range wantLifted {
		if i >= len(LiftedKeys) {
			t.Errorf("LiftedKeys leaves out %s", want.Key)
			continue
		}
		got := LiftedKeys[i]
		if got.Key != want.Key || got.Home != want.Home {
			t.Errorf("LiftedKeys[%d] = {%s -> %s}, want {%s -> %s}", i, got.Key, got.Home, want.Key, want.Home)
		}
		if strings.TrimSpace(got.Why) == "" {
			t.Errorf("the lifted key %s carries no sentence for a person to read", got.Key)
		} else if !strings.HasPrefix(got.Why, got.Key) {
			t.Errorf("the lifted key %s's sentence is %q, which does not open with the key: release B prints it alone", got.Key, got.Why)
		}
		row, ok := scopeRow(got.Key)
		if !ok {
			t.Errorf("the lifted key %s is no Scopes row", got.Key)
			continue
		}
		if row.In&InRepo == 0 {
			t.Errorf("%s is lifted out of a repository's file, which its Scopes row never admits (%v)", got.Key, row.In)
		}
		if row.Retired != 0 {
			t.Errorf("%s is marked retired (%v) and lifted: a lifted key is named by its own list, never by Retired (R1)", got.Key, row.Retired)
		}
	}
	// The two lists stay apart, by key and by document.
	for _, rk := range RetiredKeys {
		if _, ok := liftedRow(rk.Key); ok {
			t.Errorf("%s is on both lists: it is retired in %v (model-credentials-routing's, still honoured) and lifted", rk.Key, rk.In)
		}
	}
	// And the two halves of a craft, a grade and a level stay apart: the model
	// lifts, the credential or pool does not.
	for _, craft := range CraftNames {
		liftedExactly(t, "agent.crafts."+craft+".model", "agent.crafts."+craft+".access")
	}
	for _, grade := range GradeNames {
		liftedExactly(t, "model_grades."+grade+".model", "model_grades."+grade+".access")
	}
	for _, level := range []string{"l1", "l2", "l3"} {
		liftedExactly(t, "agent.levels."+level+".model", "agent.levels."+level+".effort")
	}
}

// liftedExactly asserts that the one key is lifted and the other, its own
// block's neighbour, is not.
func liftedExactly(t *testing.T, lifted, kept string) {
	t.Helper()
	if _, ok := liftedRow(lifted); !ok {
		t.Errorf("%s is not lifted, and its block's model moves to the project", lifted)
	}
	if _, ok := liftedRow(kept); ok {
		t.Errorf("%s is lifted, and it is not a model: its block's other halves stay where they are", kept)
	}
}

// TestLiftedKeyNotesNamesEveryWrittenKey is the note's own red step and its
// whole point: a repository's file that writes every lifted key is told about
// each of them, with the path as the file writes it and the one sentence
// release B prints, in path order.
//
// The document is built from LiftedKeys itself, so a key added to the list is
// written by it the moment it is added — and a key the list holds that no note
// names fails here rather than slipping through.
func TestLiftedKeyNotesNamesEveryWrittenKey(t *testing.T) {
	notes := LiftedKeyNotes(docWritingEveryLiftedKey(t))
	if len(notes) != len(LiftedKeys) {
		t.Fatalf("a file that writes every lifted key yields %d notes (%+v), want %d", len(notes), notes, len(LiftedKeys))
	}
	for i, k := range LiftedKeys {
		if notes[i].Path != k.Key {
			t.Errorf("note %d is %s, want %s: the notes come back in path order, the list's own", i, notes[i].Path, k.Key)
		}
		if notes[i].Why != k.Why {
			t.Errorf("the note for %s reads %q, want the list's own sentence %q", k.Key, notes[i].Why, k.Why)
		}
	}
}

// TestALiftedKeyIsNamedOnlyWhereItIsWritten: the note is about the keys a
// repository's file writes and never about their new homes, and it stays silent
// where no lifted key is written at all — including on bytes that are no YAML
// document, where the strict decode reports and this must not turn into a
// second, quieter answer.
//
// The layer case writes the new homes and no `ab.rate`: a project layer's own
// top-level `ab:` block spells the project's sampling rate `ab.rate` too
// (AB1 puts `ab.repos` beside it), which is the one path two documents share.
// A lifted key is one a repository's file must stop writing, the scope table
// is what governs a layer, and LiftedKeyNotes answers for the document it is
// handed — so the caller hands it a repository's file and a layer is never
// passed here.
func TestALiftedKeyIsNamedOnlyWhereItIsWritten(t *testing.T) {
	for _, tc := range []struct {
		what string
		doc  string
	}{
		{"a repository's file", minimalYAML},
		{"a file with an empty agent block",
			"version: 1\nproject: aurora\nagent:\nworkflows:\n  server:\n    commands: {build: b, test: t}\n"},
		{"bytes that are no YAML document", "\tnot: yaml\n"},
		// Every key's new home, in the layer that holds it: the project layer
		// writes the lifted paths under defaults: — a different path, and the
		// one the key lives at now — and its ab: block writes repos:, never rate.
		{"a project layer that writes the new homes",
			"version: 1\nproject: aurora\ndefaults:\n  agent:\n    model: claude-sonnet-5\n    models: {coder: claude-sonnet-5}\n    crafts: {coder: {model: claude-sonnet-5}}\n    levels: {l2: {model: claude-sonnet-5}}\n  model_grades: {strong: {model: claude-sonnet-5}}\n  rigor: {default: standard, repos: {acme/app: critical}}\nab:\n  repos: {acme/app: 0}\n"},
	} {
		if notes := LiftedKeyNotes([]byte(tc.doc)); len(notes) != 0 {
			t.Errorf("%s: notes = %+v, want none", tc.what, notes)
		}
	}
}

// docWritingEveryLiftedKey is a repository's file that writes every lifted key
// and nothing else of theirs: each key's own path is nested into one mapping,
// with a placeholder value, since no reader of the notes parses the values.
func docWritingEveryLiftedKey(t *testing.T) []byte {
	t.Helper()
	root := map[string]any{}
	for _, k := range LiftedKeys {
		parts := strings.Split(k.Key, ".")
		at := root
		for _, p := range parts[:len(parts)-1] {
			sub, ok := at[p].(map[string]any)
			if !ok {
				sub = map[string]any{}
				at[p] = sub
			}
			at = sub
		}
		at[parts[len(parts)-1]] = "x"
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		t.Fatalf("marshalling a document that writes every lifted key: %v", err)
	}
	return out
}

// keysOf is the keys of a list, for a test message.
func keysOf(keys []LiftedKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Key)
	}
	return out
}
