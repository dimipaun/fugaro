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

// TestRetiredNotesNameTheRoutingTable is MG5's red step
// (docs/model-credentials-routing/implementation-plan.md, F46 of its
// reconciliation.md): a note is what a person reads where a document still
// writes a retired key, and what replaced every one of these keys is the
// project's routing table and its pools. A note that still sends the reader to
// a craft's access sends them to a key this release no longer honours, and the
// key it names is not the one that decides the source.
func TestRetiredNotesNameTheRoutingTable(t *testing.T) {
	if len(RetiredKeys) == 0 {
		t.Fatal("this test reads nothing")
	}
	for _, rk := range RetiredKeys {
		why := rk.Why
		if strings.Contains(why, "craft's access") || strings.Contains(why, "crafts' access") {
			t.Errorf("%s (%s): the note still sends the reader to a craft's access: %q", rk.Key, docKindName(rk.In), why)
		}
		if !strings.Contains(why, "routing table") {
			t.Errorf("%s (%s): the note does not name the project's routing table: %q", rk.Key, docKindName(rk.In), why)
		}
		if !strings.Contains(why, "pool") {
			t.Errorf("%s (%s): the note does not name the project's pools: %q", rk.Key, docKindName(rk.In), why)
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

// TestRetiredWorkflowResources is D3421's
// (docs/ground-rules/reconciliation.md): every repository runs on the project's
// shared jobs, the profile the workflow starts from decides the sizing, and a
// repository's workflows.*.resources is read and ignored. The key still loads
// and the file is still judged as written — nothing refuses a document for it —
// and the resolution drops the block and names the profile in its place.
//
// The drop is in both arms, as the lifted keys' is (R2): a file resolved with
// no project layer is the config Parse reads, byte for byte
// (TestResolveOverAnEmptyLayerIsParse), so the arm that has no profile to name
// drops the block just the same and names Fugaro's defaults.
//
// Mutation (honour the repository's block again, drop the note, name a profile
// the workflow does not start from, or drop the key before the file is judged):
// this test fails.
func TestRetiredWorkflowResources(t *testing.T) {
	const written = "    resources: { cpu: 16, memory: 32Gi }\n"
	const git = "git: { provider: github }\n"
	const api = git + "workflows:\n  api:\n    profile: java-service\n    commands: { build: ./gradlew assemble, test: ./gradlew test }\n" + written
	const web = git + "workflows:\n  web:\n    commands: { build: make, test: make test }\n" + written

	// A workflow that starts from a profile is sized by that profile's own
	// resources: java-service's 4/16Gi, not the file's 16/32Gi.
	c, res, ps := Resolve([]byte(minimalRepo+api), mustLayer(t, testLayer))
	if len(ps) > 0 {
		t.Fatalf("a file that writes workflows.*.resources is refused: %v", ps)
	}
	if got := c.Workflows["api"].Resources; got != (Resources{CPU: 4, Memory: "16Gi"}) {
		t.Errorf("workflows.api.resources = %+v, want the profile's own 4/16Gi", got)
	}
	if got := res.SourceOf("workflows.api.resources.memory"); got != SourceProfile("java-service") {
		t.Errorf("the memory in force is sourced %q, want the profile's", got)
	}
	if len(res.IgnoredKeys) != 1 || res.IgnoredKeys[0].Path != "workflows.api.resources" {
		t.Fatalf("the resolution names %+v, want the one resources block the file wrote", res.IgnoredKeys)
	}
	if why := res.IgnoredKeys[0].Why; !strings.HasPrefix(why, "workflows.api.resources") || !strings.Contains(why, "java-service") {
		t.Errorf("the note is %q, want it to open with the key and name the profile that decides the sizing", why)
	}

	// A workflow that names no profile is sized by Fugaro's defaults, and the
	// note names none: no profile decides that workflow's sizing.
	c, res, ps = Resolve([]byte(minimalRepo+web), mustLayer(t, testLayer))
	if len(ps) > 0 {
		t.Fatalf("a file that writes workflows.*.resources is refused: %v", ps)
	}
	if got := c.Workflows["web"].Resources; got != (Resources{CPU: DefaultCPU, Memory: DefaultMemory}) {
		t.Errorf("workflows.web.resources = %+v, want Fugaro's own defaults", got)
	}
	if len(res.IgnoredKeys) != 1 || strings.Contains(res.IgnoredKeys[0].Why, "java-service") {
		t.Errorf("the resolution names %+v, want one note that names no profile", res.IgnoredKeys)
	}

	// Without a project layer there is no profile at all, and the block is
	// dropped all the same.
	c, res, ps = Resolve([]byte(minimalRepo+web), nil)
	if len(ps) > 0 {
		t.Fatalf("a file that writes workflows.*.resources is refused: %v", ps)
	}
	if got := c.Workflows["web"].Resources; got != (Resources{CPU: DefaultCPU, Memory: DefaultMemory}) {
		t.Errorf("without a layer workflows.web.resources = %+v, want Fugaro's own defaults", got)
	}
	if len(res.IgnoredKeys) != 1 {
		t.Errorf("without a layer the resolution names %+v, want the one block the file wrote", res.IgnoredKeys)
	}

	// The drop is not a loosening: the file as written is still judged, so a
	// resources block no reader could honour is still refused where it stands.
	if _, ps := Parse([]byte(minimalRepo + strings.Replace(web, "cpu: 16", "cpu: -1", 1))); len(ps) == 0 {
		t.Error("a file whose workflows.*.resources.cpu is -1 parsed: the file as written is still judged")
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
