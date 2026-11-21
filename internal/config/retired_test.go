package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// wantRetired is R3's list (docs/credentials-belong-to-models/reconciliation.md)
// spelled out as this test's own expectation, so it is red before the table and
// the note exist at all: `agent.auth` and `defaults.agent.auth`, a repository's
// `agent.crafts.<craft>.access`, `model_grades.<grade>.access` in a repository's
// file (F38), and `agent.oauth_token`. R3's other keys — a stage's
// `claude_account` and `default_claude_account` — are the installation config's
// and the published node's, not a file's or a layer's, so they are not here:
// CM3's function answers for the two documents a repository and a project
// write.
var wantRetired = func() []RetiredKey {
	out := []RetiredKey{
		{Key: "agent.auth", In: InRepo},
		{Key: "agent.auth", In: InProject},
	}
	for _, craft := range CraftNames {
		out = append(out, RetiredKey{Key: "agent.crafts." + craft + ".access", In: InRepo})
	}
	for _, grade := range GradeNames {
		out = append(out, RetiredKey{Key: "model_grades." + grade + ".access", In: InRepo})
	}
	return append(out, RetiredKey{Key: "agent.oauth_token", In: InRepo})
}()

// TestEveryRetiredKeyIsNamedWhereItIsWritten is CM3's red step and its whole
// point: a document that still writes a retired key is told so, with the path
// as that document writes it and the sentence a person reads. R3's list is
// written into a document of its own — a repository's file or a project layer,
// whichever the entry names — and the note has to come back.
//
// Mutation (run, restored): unmark one row (drop its Retired bits) or add a key
// to the table that R3 does not hold, and this test names it.
func TestEveryRetiredKeyIsNamedWhereItIsWritten(t *testing.T) {
	if len(wantRetired) == 0 {
		t.Fatal("this test reads nothing")
	}
	if len(RetiredKeys) != len(wantRetired) {
		t.Errorf("RetiredKeys holds %d entries (%+v), want R3's %d", len(RetiredKeys), RetiredKeys, len(wantRetired))
	}
	for _, want := range wantRetired {
		row, ok := scopeRow(want.Key)
		if !ok {
			t.Errorf("R3 retires %q, which is no Scopes row", want.Key)
			continue
		}
		if row.Retired&want.In == 0 {
			t.Errorf("%q is retired in %v, but its Scopes row marks %v: config.Scopes does not mark it retired", want.Key, want.In, row.Retired)
		}
		if want.In&^row.In != 0 {
			t.Errorf("%q is retired in %v, a document its Scopes row never admits (%v)", want.Key, want.In, row.In)
		}
		if strings.TrimSpace(row.RetiredWhy) == "" {
			t.Errorf("the retired key %s carries no sentence for a person to read", want.Key)
		}
		if !namesRetired(RetiredKeys, want.Key, want.In) {
			t.Errorf("RetiredKeys does not name %q in %v", want.Key, want.In)
		} else if rk, _ := retiredIn(RetiredKeys, want.Key, want.In); rk.Why != row.RetiredWhy {
			t.Errorf("RetiredKeys names %q in %v with %q, want its Scopes row's own sentence", want.Key, want.In, rk.Why)
		}
		// A project layer writes every fugaro.yaml key under defaults:; a
		// repository's file writes it at the top level.
		path := want.Key
		if want.In == InProject {
			path = "defaults." + want.Key
		}
		notes := RetiredKeyNotes(docWriting(t, want.Key, want.In), want.In)
		if len(notes) != 1 {
			t.Errorf("a %s that writes %s: notes = %+v, want exactly one", docKindName(want.In), want.Key, notes)
			continue
		}
		if notes[0].Path != path || notes[0].Why != row.RetiredWhy {
			t.Errorf("a %s that writes %s: note = %+v, want the path %s and the row's own sentence", docKindName(want.In), want.Key, notes[0], path)
		}
	}
	// The other direction: a mark nothing reads, or a key RetiredKeys invents,
	// would let a document be told about a key R3 never retired.
	for _, rk := range RetiredKeys {
		if !namesRetired(wantRetired, rk.Key, rk.In) {
			t.Errorf("RetiredKeys names %q retired in %v, which R3 does not", rk.Key, rk.In)
		}
	}
	for _, row := range Scopes {
		for _, doc := range []Scope{InRepo, InProject} {
			if row.Retired&doc == 0 {
				continue
			}
			if !namesRetired(wantRetired, row.Key, doc) {
				t.Errorf("Scopes marks %q retired in %v, which R3 does not", row.Key, doc)
			}
		}
	}
}

// namesRetired reports whether keys names key retired in doc, and retiredIn is
// its entry — the pair compared on Key and In alone, so an empty Why in a
// test's own expectation is not what decides the answer.
func namesRetired(keys []RetiredKey, key string, doc Scope) bool {
	_, ok := retiredIn(keys, key, doc)
	return ok
}

func retiredIn(keys []RetiredKey, key string, doc Scope) (RetiredKey, bool) {
	for _, rk := range keys {
		if rk.Key == key && rk.In == doc {
			return rk, true
		}
	}
	return RetiredKey{}, false
}

// TestAFileWithNoRetiredKeySaysNothing: the note is about the keys a document
// writes and never about the ones it leaves out — a file that names none of
// them is silent, a block that is empty is silent, and so is bytes this cannot
// read as YAML at all (the strict decode reports that, and this must not turn
// into a second, quieter answer).
func TestAFileWithNoRetiredKeySaysNothing(t *testing.T) {
	for _, tc := range []struct {
		what string
		doc  string
		in   Scope
	}{
		{"a repository's file", minimalYAML, InRepo},
		{"a project layer", "version: 1\nproject: aurora\n", InProject},
		// A grade's access is retired in a repository's file alone: the project
		// layer is the one layer a grade's credential or pool may still be
		// named in (F38 of docs/credentials-belong-to-models/reconciliation.md,
		// whyGrade).
		{"a layer that names a grade's access",
			"defaults:\n  model_grades:\n    strong:\n      model: claude-opus-5-5\n      access: our-key\n", InProject},
		{"a file with an empty agent block",
			"version: 1\nproject: aurora\nagent:\nworkflows:\n  server:\n    commands: {build: b, test: t}\n", InRepo},
		{"bytes that are no YAML document", "\tnot: yaml\n", InRepo},
	} {
		if notes := RetiredKeyNotes([]byte(tc.doc), tc.in); len(notes) != 0 {
			t.Errorf("%s: notes = %+v, want none", tc.what, notes)
		}
	}
}

// TestARetiredKeyStillDecodes: release 1 reads the retired keys and they set
// nothing (D3063), and CM3 changes no reader — so a file that writes every one
// of them still decodes and still validates, and a project layer that writes
// defaults.agent.auth does too. The note is an addition to what the file does,
// never a refusal of it.
func TestARetiredKeyStillDecodes(t *testing.T) {
	const doc = `
version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  oauth_token: bmb
  crafts:
    coder:
      access: our-key
model_grades:
  strong:
    model: claude-opus-5-5
    access: our-key
workflows:
  server:
    commands: {build: b, test: t}
`
	cfg, ps := Parse([]byte(doc))
	if len(ps) > 0 {
		t.Fatalf("a file that writes the retired keys is refused: %v", ps)
	}
	if cfg.Agent.Auth != AuthAPIKey || cfg.Agent.OAuthToken != "bmb" {
		t.Errorf("agent = %+v, want the retired keys decoded exactly as written", cfg.Agent)
	}
	if got := cfg.Agent.Crafts.Of(CraftCoder).Access; got != "our-key" {
		t.Errorf("agent.crafts.coder.access = %q, want our-key", got)
	}
	if cfg.ModelGrades == nil || cfg.ModelGrades.Of(GradeStrong).Access != "our-key" {
		t.Errorf("model_grades.strong.access = %+v, want our-key", cfg.ModelGrades)
	}
	if notes := RetiredKeyNotes([]byte(doc), InRepo); len(notes) != 4 {
		t.Errorf("the same file yields %d notes (%+v), want the four retired keys it writes", len(notes), notes)
	}

	const layer = `
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  agent:
    auth: oauth
`
	if _, ps := ParseProjectLayer([]byte(layer), LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}); len(ps) > 0 {
		t.Fatalf("a project layer that writes defaults.agent.auth is refused: %v", ps)
	}
}

// TestTheExampleWritesNoRetiredKey: `fugaro config example` prints the template
// a person starts from, so it may not offer a key no release honours (CM3).
func TestTheExampleWritesNoRetiredKey(t *testing.T) {
	for _, doc := range []Scope{InRepo, InProject} {
		if notes := RetiredKeyNotes(Example, doc); len(notes) > 0 {
			t.Errorf("example.yaml writes the retired key %s: the template must not offer it", notes[0].Path)
		}
	}
}

// docWriting is a document that writes key and nothing else of fugaro.yaml's: a
// repository's file for InRepo, a project layer for InProject.
func docWriting(t *testing.T, key string, doc Scope) []byte {
	t.Helper()
	parts := strings.Split(key, ".")
	var v any = "x"
	for i := len(parts) - 1; i >= 0; i-- {
		v = map[string]any{parts[i]: v}
	}
	if doc == InProject {
		v = map[string]any{"defaults": v}
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling a document that writes %s: %v", key, err)
	}
	return out
}

// docKindName is the document a retired key's In bit names, for a test message.
func docKindName(doc Scope) string {
	if doc == InProject {
		return "project layer"
	}
	return "repository's file"
}
