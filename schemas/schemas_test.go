package schemas_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
	"github.com/dimipaun/fugaro/internal/verify"
)

// compile loads a schema file and compiles it under its $id.
func compile(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	id := doc.(map[string]any)["$id"].(string)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// yamlInstance converts YAML to the JSON value model the validator expects.
func yamlInstance(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

func globAll(t *testing.T, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		t.Fatalf("no files match %s: %v", pattern, err)
	}
	return files
}

// fencedYAML are a markdown document's ```yaml blocks, in order.
func fencedYAML(doc string) []string {
	var out []string
	lines := strings.Split(doc, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```yaml" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				out = append(out, strings.Join(lines[i+1:j], "\n")+"\n")
				i = j
				break
			}
		}
	}
	return out
}

// TestProjectLayerDocsExample: the §6 example in docs/project-layer.md is a
// project layer the authoring schema accepts *and* config.ParseProjectLayer
// parses. It is the shape a reader copies, so a key the docs show can never be
// one the schema (additionalProperties: false) or the parser refuses — which
// is exactly the gap this closes: claude_models shipped in the parser, the CLI
// and the docs example, and project-layer.schema.json had no property for it,
// so every layer carrying a claim — the documented example included — was
// rejected by the schema an editor validates against.
func TestProjectLayerDocsExample(t *testing.T) {
	data, err := os.ReadFile("../docs/project-layer.md")
	if err != nil {
		t.Fatal(err)
	}
	var example string
	for _, b := range fencedYAML(string(data)) {
		if strings.Contains(b, "claude_models:") {
			example = b
			break
		}
	}
	if example == "" {
		t.Fatal("docs/project-layer.md has no ```yaml example carrying claude_models:")
	}
	if err := compile(t, "project-layer.schema.json").Validate(yamlInstance(t, []byte(example))); err != nil {
		t.Errorf("project-layer.schema.json rejects docs/project-layer.md's own example: %v", err)
	}
	project, err := config.ProjectOf([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	gcp, err := config.GCPProjectOf([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if _, ps := config.ParseProjectLayer([]byte(example), config.LayerAnchor{Project: project, GCPProject: gcp}); len(ps) > 0 {
		t.Errorf("config.ParseProjectLayer rejects docs/project-layer.md's own example: %v", ps)
	}
}

// TestProjectLayerSchemaClaudeModels ties the schema's claude_models to the Go
// rules it mirrors (config.providerNameRE for the name, config.CheckClaudeClaim
// for the entry — the same rule a route entry is judged by): both are separate
// copies, and the schema's additionalProperties: false means a schema stricter
// than the parser refuses a layer the parser accepts. Every text below must
// come out the same way in both, which a change to either Go rule breaks here.
func TestProjectLayerSchemaClaudeModels(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	const head = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	anchor := config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	for _, entry := range []string{
		"claude_models:\n  openrouter: [\"anthropic/claude-sonnet-5.5\"]\n",
		"claude_models:\n  openrouter: [\"anthropic/claude-sonnet-5.5\", \"anthropic/claude-opus-5.5\"]\n",
		"claude_models:\n  openrouter: [\"anthropic/a\"]\n",
		"claude_models:\n  openrouter: [\"anthropic/claude-3.5-sonnet\"]\n",
		"claude_models:\n  openrouter: []\n",
		"claude_models:\n  openrouter: \"anthropic/claude-sonnet-5.5\"\n",
		"claude_models:\n  openrouter: [\"anthropic/*\"]\n",
		"claude_models:\n  openrouter: [\"anthropic/\"]\n",
		"claude_models:\n  openrouter: [\"claude-sonnet-5-5\"]\n",
		"claude_models:\n  openrouter: [\"openai/gpt-5\"]\n",
		"claude_models:\n  openrouter: [\"anthropic/Claude-Sonnet\"]\n",
		"claude_models:\n  openrouter: [\"anthropic/claude-sonnet[1m]\"]\n",
		"claude_models:\n  OpenRouter: [\"anthropic/claude-sonnet-5.5\"]\n",
		"claude_models:\n  openrouter:\n    - anthropic/claude-sonnet-5.5\n",
		"claude_models: []\n",
		"claude_models: openrouter\n",
		// 101 characters: the parser's route-entry rule (CheckModelPattern)
		// bound, which the schema mirrors as the item's maxLength.
		"claude_models:\n  openrouter: [\"anthropic/" + strings.Repeat("c", 91) + "\"]\n",
		// 100 exactly: inside the bound in both.
		"claude_models:\n  openrouter: [\"anthropic/" + strings.Repeat("c", 90) + "\"]\n",
	} {
		text := head + entry
		schemaOK := sch.Validate(yamlInstance(t, []byte(text))) == nil
		_, ps := config.ParseProjectLayer([]byte(text), anchor)
		if schemaOK != (len(ps) == 0) {
			t.Errorf("claude_models: %s: schema ok = %v, config.ParseProjectLayer problems = %v", entry, schemaOK, ps)
		}
	}
}

// TestSchemasAgentAuthAgreesWithGo pins both schemas' agent.auth enum to
// config.AgentAuths — the one list the validator (internal/config/validate.go),
// the layer's own defaults.agent.auth (layer.go, KnownAgentAuth) and the
// launcher (internal/cli/run.go) read. Each schema carries its own copy of that
// list, and with additionalProperties: false and an enum, a copy that lags the
// Go side refuses at authoring time — in an editor, before any command runs — a
// file or layer the binary accepts. That is the drift the cascade value
// introduced: fugaro.schema.json's auth gained it while
// project-layer.schema.json's defaults.agent.auth kept the three older values,
// even though config.ParseProjectLayer accepted it (TestLayerAcceptsCascade in
// internal/config) and docs/project-layer.md lists agent.auth as a key a layer
// may set.
//
// The layer's own round trip is the second half: the enum is compared here and
// the node it lives on is exercised below, so a list that names the value while
// the schema refuses the layer carrying it cannot pass. The file's half is
// testdata/config/valid/agent-auth-cascade.yaml, which TestFugaroSchemaCorpus
// validates against fugaro.schema.json.
//
// Mutation (run, restored): take "cascade" back out of either enum, or add a
// value to config.AgentAuths, and the comparison fails naming the side that has
// it.
func TestSchemasAgentAuthAgreesWithGo(t *testing.T) {
	for _, tc := range []struct {
		file string
		path []string
	}{
		{"fugaro.schema.json", []string{"properties", "agent", "properties", "auth"}},
		{"project-layer.schema.json", []string{"properties", "defaults", "properties", "agent", "properties", "auth"}},
	} {
		data, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		var node any = doc
		for _, key := range tc.path {
			m, ok := node.(map[string]any)
			if !ok {
				t.Fatalf("%s: %v: not an object at %q", tc.file, tc.path, key)
			}
			if node, ok = m[key]; !ok {
				t.Fatalf("%s: %v: missing %q", tc.file, tc.path, key)
			}
		}
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("%s: %v: not an object", tc.file, tc.path)
		}
		raw, ok := m["enum"].([]any)
		if !ok {
			t.Fatalf("%s: %v: no enum", tc.file, tc.path)
		}
		var got []string
		for _, v := range raw {
			s, ok := v.(string)
			if !ok {
				t.Fatalf("%s: %v: enum entry %v is not a string", tc.file, tc.path, v)
			}
			got = append(got, s)
		}
		if !sameStrings(got, config.AgentAuths) {
			t.Errorf("%s: %v enum = %v, config.AgentAuths = %v", tc.file, tc.path, got, config.AgentAuths)
		}
	}

	const layer = "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  agent:\n    auth: cascade\n"
	if err := compile(t, "project-layer.schema.json").Validate(yamlInstance(t, []byte(layer))); err != nil {
		t.Errorf("project-layer.schema.json rejects a layer whose defaults.agent.auth is cascade: %v", err)
	}
	anchor := config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	if _, ps := config.ParseProjectLayer([]byte(layer), anchor); len(ps) > 0 {
		t.Errorf("config.ParseProjectLayer rejects the same layer: %v", ps)
	}
}

// sameStrings reports whether two string lists hold the same values, order
// aside (an enum's order is the schema's business, not the Go list's).
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

func TestFugaroSchemaCorpus(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	for _, f := range globAll(t, "../testdata/config/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid config: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/config/invalid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err == nil {
			t.Errorf("%s: schema accepts an invalid config", f)
		}
	}
	if err := sch.Validate(yamlInstance(t, config.Example)); err != nil {
		t.Errorf("schema rejects the embedded example: %v", err)
	}
}

// schemaDescriptionAt walks file's raw JSON (not through the jsonschema
// compiler, which drops "description") down path and returns the
// "description" string at the end of it.
func schemaDescriptionAt(t *testing.T, file string, path ...string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var node any = doc
	for _, key := range path {
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("%s: %v: not an object at %q", file, path, key)
		}
		node, ok = m[key]
		if !ok {
			t.Fatalf("%s: %v: missing %q", file, path, key)
		}
	}
	m, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("%s: %v: not an object", file, path)
	}
	desc, _ := m["description"].(string)
	return desc
}

// TestSchemaDescriptionsNameMigrationDoc pins that every removed-key
// description in both schemas (base, image.node and image.jdk, in
// fugaro.schema.json and in project-layer.schema.json) names
// config.MigrationDoc, the same doc every Go-side refusal message names
// (internal/config/validate.go, image.go, layer.go, dockerfile.go): a later
// change to the constant, or an edit to only one of the two schema files,
// is caught here instead of quietly drifting into a stale doc link. It also
// pins that the doc itself exists, so the constant can never name a dead
// file again.
func TestSchemaDescriptionsNameMigrationDoc(t *testing.T) {
	if _, err := os.Stat("../" + config.MigrationDoc); err != nil {
		t.Fatalf("config.MigrationDoc names %s, which does not exist: %v", config.MigrationDoc, err)
	}
	for _, tc := range []struct {
		file string
		path []string
	}{
		{"fugaro.schema.json", []string{"$defs", "workflow", "properties", "base"}},
		{"fugaro.schema.json", []string{"$defs", "workflow", "properties", "image", "properties", "node"}},
		{"fugaro.schema.json", []string{"$defs", "workflow", "properties", "image", "properties", "jdk"}},
		{"project-layer.schema.json", []string{"$defs", "profile", "properties", "base"}},
		{"project-layer.schema.json", []string{"$defs", "profile", "properties", "image", "properties", "node"}},
		{"project-layer.schema.json", []string{"$defs", "profile", "properties", "image", "properties", "jdk"}},
	} {
		desc := schemaDescriptionAt(t, tc.file, tc.path...)
		if !strings.Contains(desc, config.MigrationDoc) {
			t.Errorf("%s %v: description %q does not name config.MigrationDoc (%s)", tc.file, tc.path, desc, config.MigrationDoc)
		}
	}
}

func TestSchemaBudgetBlock(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	base := "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  s: { commands: { build: make, test: make } }\n"
	for _, c := range []struct {
		budget string
		ok     bool
	}{
		{"budget: { mode: enforce, per_run_usd: 2.5, allowed_models: [claude-opus-5-5] }", true},
		{"budget: { mode: strict }", false},
		{"budget: { per_run_usd: -1 }", false},
		{"budget: { per_day_usd: 5 }", true},
		{"budget: { per_day_usd: -1 }", false},
		{"budget: { per_day_usd: 100001 }", false},
		{"budget: { per_day_usd: 0.0000001 }", false},
		{"budget: { per_day_usd: 0 }", true},
		{"budget: { per_day_usd: lots }", false},
		{"budget: { allowed_models: [] }", false},
		{"budget: { allowed_models: [\"a b\"] }", false},
		{"budget: { allowed_models: [sonnet] }", false},
		{"budget: { allowed_models: [claude-sonnet-latest] }", false},
		{"budget: { model_prices: {} }", false},
		// Where Go and the schema once differed: both agree now.
		{"budget:", true},
		{"budget: { mode: }", true},
		{"budget: { per_run_usd: }", true},
		{"budget: { allowed_models: }", true},
		{"budget: { per_day_usd: }", true},
		{"budget: { per_run_usd: 0 }", true},
		{"budget: { per_run_usd: 0.000001 }", true},
		{"budget: { per_run_usd: 0.0000001 }", false},
		{"budget: { per_run_usd: 100001 }", false},
		{"budget: { allowed_models: [claude-sonnet-5-5, claude-sonnet-5-5] }", false},
	} {
		err := sch.Validate(yamlInstance(t, []byte(base+c.budget+"\n")))
		if (err == nil) != c.ok {
			t.Errorf("%s: schema err = %v, want ok=%v", c.budget, err, c.ok)
		}
	}
}

func TestTaskSchemaCorpus(t *testing.T) {
	sch := compile(t, "task.schema.json")
	jsonInstance := func(f string) any {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return inst
	}
	for _, f := range globAll(t, "../testdata/task/valid/*.json") {
		if err := sch.Validate(jsonInstance(f)); err != nil {
			t.Errorf("%s: schema rejects a valid task: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/task/invalid/*.json") {
		if err := sch.Validate(jsonInstance(f)); err == nil {
			t.Errorf("%s: schema accepts an invalid task", f)
		}
	}
}

// TestTaskSchemaRigorRequestMatchesTheGoRules ties the task schema's `rigor`
// and `paths` properties to the Go rules they mirror (rigor.Modes,
// task.MaxDeclaredPaths, task.MaxDeclaredPathBytes): the schema's enum and
// bounds are separate copies of both, so without this a mode added to the
// vocabulary, or a bound moved, would drift from the published schema with
// only the corpus fixtures noticing — and a task.json the launcher writes is
// exactly what this schema is for.
//
// The paths bounds are what the schema can express and the decoder also
// applies: Spec.Validate refuses a blank path with strings.TrimSpace, so the
// pattern below asks for at least one non-whitespace character, as the
// inputs' name property does for the same reason.
func TestTaskSchemaRigorRequestMatchesTheGoRules(t *testing.T) {
	data, err := os.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var mode struct {
		Enum []string `json:"enum"`
	}
	if raw, ok := doc.Properties["rigor"]; !ok {
		t.Fatal("task.schema.json has no rigor property")
	} else if err := json.Unmarshal(raw, &mode); err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(rigor.Modes))
	for _, m := range rigor.Modes {
		want = append(want, string(m))
	}
	if !slices.Equal(mode.Enum, want) {
		t.Errorf("schema rigor enum %v != rigor.Modes %v", mode.Enum, want)
	}
	var paths struct {
		MaxItems int `json:"maxItems"`
		Items    struct {
			Pattern   string `json:"pattern"`
			MaxLength int    `json:"maxLength"`
		} `json:"items"`
	}
	if raw, ok := doc.Properties["paths"]; !ok {
		t.Fatal("task.schema.json has no paths property")
	} else if err := json.Unmarshal(raw, &paths); err != nil {
		t.Fatal(err)
	}
	if paths.MaxItems != task.MaxDeclaredPaths {
		t.Errorf("schema paths maxItems %d != task.MaxDeclaredPaths %d", paths.MaxItems, task.MaxDeclaredPaths)
	}
	if paths.Items.MaxLength != task.MaxDeclaredPathBytes {
		t.Errorf("schema paths items maxLength %d != task.MaxDeclaredPathBytes %d", paths.Items.MaxLength, task.MaxDeclaredPathBytes)
	}
	if paths.Items.Pattern != `\S` {
		t.Errorf("schema paths items pattern %q does not refuse a blank path, which Spec.Validate does", paths.Items.Pattern)
	}
}

// TestTaskSchemaTierMatchesTheGoRules is the same tie for T8.1's two fields:
// `tier`'s enum is tier.Tiers, and `expected_scope` is bounded exactly as the
// `paths` list above is (task.MaxDeclaredPaths, task.MaxDeclaredPathBytes, a
// blank entry refused). A tier added to the vocabulary, or one of those bounds
// moved, would otherwise leave the published schema — the surface a
// repository's CI checks a task.json against — describing a field the decoder
// refuses, or refusing one it reads.
func TestTaskSchemaTierMatchesTheGoRules(t *testing.T) {
	data, err := os.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var tiers struct {
		Enum []string `json:"enum"`
	}
	if raw, ok := doc.Properties["tier"]; !ok {
		t.Fatal("task.schema.json has no tier property")
	} else if err := json.Unmarshal(raw, &tiers); err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(tier.Tiers))
	for _, tt := range tier.Tiers {
		want = append(want, string(tt))
	}
	if !slices.Equal(tiers.Enum, want) {
		t.Errorf("schema tier enum %v != tier.Tiers %v", tiers.Enum, want)
	}
	var scope struct {
		MaxItems int `json:"maxItems"`
		Items    struct {
			Pattern   string `json:"pattern"`
			MaxLength int    `json:"maxLength"`
		} `json:"items"`
	}
	if raw, ok := doc.Properties["expected_scope"]; !ok {
		t.Fatal("task.schema.json has no expected_scope property")
	} else if err := json.Unmarshal(raw, &scope); err != nil {
		t.Fatal(err)
	}
	if scope.MaxItems != task.MaxDeclaredPaths {
		t.Errorf("schema expected_scope maxItems %d != task.MaxDeclaredPaths %d", scope.MaxItems, task.MaxDeclaredPaths)
	}
	if scope.Items.MaxLength != task.MaxDeclaredPathBytes {
		t.Errorf("schema expected_scope items maxLength %d != task.MaxDeclaredPathBytes %d", scope.Items.MaxLength, task.MaxDeclaredPathBytes)
	}
	if scope.Items.Pattern != `\S` {
		t.Errorf("schema expected_scope items pattern %q does not refuse a blank entry, which Spec.Validate does", scope.Items.Pattern)
	}
}

// TestTaskSchemaInputs: the attached-files list a launcher writes (design
// artifact-runs.md §4, plan Task 6) is an array of closed {name, bytes,
// sha256} objects, and an absent list stays absent. The name's own shape
// (no ".." element, no NUL, a length limit) is deliberately not a schema
// rule: the launcher and the runner are where a name becomes a path, and
// they refuse it there (design §8.2).
//
// The ONE exception is emptiness, which the schema can express and which the
// decoder refuses anyway (Spec.Validate's required-name check): minLength 1
// does not catch a name of nothing but whitespace, so the pattern below asks
// for at least one non-whitespace character — the same rule as
// task.InputNameBlank, which the launcher applies before it uploads.
//
// Mutation (drop the inputs property from schemas/task.schema.json): the
// valid instance below is rejected as an unknown key and this test fails.
func TestTaskSchemaInputs(t *testing.T) {
	sch := compile(t, "task.schema.json")
	validate := func(lit string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(lit))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	head := `{"version":1,"run_id":"20261015-100000-abcd","repo":"acme/app","ref":"main","task":"x","mode":"artifact"`
	ok := head + `,"inputs":[{"name":"shot.png","bytes":1024,"sha256":"` + strings.Repeat("a", 64) + `"}]` + `}`
	if err := validate(ok); err != nil {
		t.Fatalf("schema rejects a task carrying inputs: %v", err)
	}
	if err := validate(head + `}`); err != nil {
		t.Fatalf("schema rejects a task carrying none: %v", err)
	}
	for _, tc := range []struct{ name, lit string }{
		{"not an array", `,"inputs":{}`},
		{"empty name", `,"inputs":[{"name":"","bytes":1,"sha256":"` + strings.Repeat("a", 64) + `"}]`},
		{"whitespace-only name", `,"inputs":[{"name":"   ","bytes":1,"sha256":"` + strings.Repeat("a", 64) + `"}]`},
		{"negative bytes", `,"inputs":[{"name":"a.png","bytes":-1,"sha256":"` + strings.Repeat("a", 64) + `"}]`},
		{"short sha256", `,"inputs":[{"name":"a.png","bytes":1,"sha256":"abc"}]`},
		{"upper-case sha256", `,"inputs":[{"name":"a.png","bytes":1,"sha256":"` + strings.Repeat("A", 64) + `"}]`},
		{"unknown key", `,"inputs":[{"name":"a.png","bytes":1,"sha256":"` + strings.Repeat("a", 64) + `","content_type":"image/png"}]`},
		{"missing sha256", `,"inputs":[{"name":"a.png","bytes":1}]`},
	} {
		if err := validate(head + tc.lit + `}`); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, tc.lit)
		}
	}
}

// TestTaskSchemaDigestRuns pins the task schema's `digest_runs` property
// (task-governance plan T3.6): the report run's manifest, the run IDs the
// launching CLI listed for the digest step to read. task.schema.json is
// additionalProperties: false, so without this property every digest launch
// writes a task.json the published schema rejects.
//
// The two rules the schema carries are the decoder's own (Spec.Validate): a
// run ID of the one shape, and at most task.MaxDigestRuns of them, which the
// schema copies and TestTaskSchemaDigestRunsBound ties back to the constant.
//
// The task carrying the manifest is marshalled from task.Spec rather than
// written out, so it reads the Go field's own json tag, the way
// TestTaskSchemaCompressionOffMatchesTheGoField does: renaming the tag or the
// schema property stops the two agreeing, and the drift would otherwise be
// silent, since no Go test reads this document and the launcher's own tests
// never consult it.
//
// Mutation (drop the digest_runs property from schemas/task.schema.json, or
// rename the Go tag): the marshalled task is rejected as an unknown key and
// this test fails.
func TestTaskSchemaDigestRuns(t *testing.T) {
	sch := compile(t, "task.schema.json")
	validate := func(s task.Spec) error {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	// The negative cases below are literals: they carry values task.Spec
	// itself would not marshal.
	validateLit := func(lit string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(lit))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	head := `{"version":1,"run_id":"20261027-093000-abcd","repo":"acme/app","ref":"main","task":"x"`
	spec := task.Spec{Version: 1, RunID: "20261027-093000-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	if err := validate(spec); err != nil {
		t.Fatalf("schema rejects a task carrying no manifest: %v", err)
	}
	spec.DigestRuns = []string{"20261026-090000-1234", "20261025-080000-abcd"}
	if err := validate(spec); err != nil {
		t.Fatalf("schema rejects a task carrying a manifest, which fugaro run --recipe run-digest writes: %v", err)
	}
	for _, tc := range []struct{ name, lit string }{
		{"not an array", `,"digest_runs":"20261026-090000-1234"`},
		{"an empty list", `,"digest_runs":[]`},
		{"not a run ID", `,"digest_runs":["yesterday"]`},
		{"an empty ID", `,"digest_runs":[""]`},
		{"a run ID without its four hex digits", `,"digest_runs":["20261026-090000"]`},
		{"an upper-case run ID", `,"digest_runs":["20261026-090000-ABCD"]`},
		{"a null element", `,"digest_runs":[null]`},
	} {
		if err := validateLit(head + tc.lit + `}`); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, tc.lit)
		}
	}
}

// TestTaskSchemaDigestRunsBound ties the schema's own bound on the manifest to
// task.MaxDigestRuns: the two are separate copies, and a bound moved in one
// without the other would publish a schema that admits a list the decoder
// refuses (or the reverse).
func TestTaskSchemaDigestRunsBound(t *testing.T) {
	data, err := os.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			MaxItems int `json:"maxItems"`
			MinItems int `json:"minItems"`
			Items    struct {
				Pattern string `json:"pattern"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	p, ok := doc.Properties["digest_runs"]
	if !ok {
		t.Fatal("task.schema.json has no digest_runs property")
	}
	if p.MaxItems != task.MaxDigestRuns {
		t.Errorf("schema digest_runs maxItems %d != task.MaxDigestRuns %d", p.MaxItems, task.MaxDigestRuns)
	}
	// A manifest that is present names at least one run: an empty one is
	// omitted by the launcher (omitempty), so the schema refuses what the
	// decoder never sees.
	if p.MinItems != 1 {
		t.Errorf("schema digest_runs minItems %d, want 1", p.MinItems)
	}
	// The pattern is task.ValidRunID's own shape, which the decoder applies.
	if !regexp.MustCompile(p.Items.Pattern).MatchString("20261026-090000-abcd") {
		t.Errorf("schema digest_runs items pattern %q does not match a run ID", p.Items.Pattern)
	}
}

func TestResultSchemaAcceptsRunnerRecords(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	deadline, finished := at.Add(time.Hour), at.Add(10*time.Minute)
	calls, agents := 12, 2
	cost := runstore.NewCost(4.12, 0.38, runstore.BasisAPIList).WithNotional(0.42)
	// A VM-backed run's estimate carries the price table it was priced from,
	// which a record has to be able to store (design compute-backend.md
	// §5.2, C17).
	cost.ComputeSource = "Compute Engine VM list price, e2-standard family, us-central1 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"
	rec := runstore.Record{
		Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Workflow: "web",
		Execution: "projects/p/locations/r/jobs/j/executions/e",
		Status:    runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reason: "tests failing on the final commit", Branch: "fugaro/add-a-feature", BaseBranch: "main", HeadSHA: "abcdef1234567",
		// The commit the run was cut from, which its guards compare against
		// (design v1.md §4.6).
		StartSHA: strings.Repeat("c", 40),
		PR:       &runstore.PRRef{Number: 7, URL: "https://example.com/pr/7"},
		// The bounce is on the first-line review that followed a senior
		// rejection, which is the only shape that carries one (a recipe with
		// review: {bounce: first_line}, docs/recipes.md section 1), and the
		// entry is closed: without the field the schema refused the record of
		// a bounced run, catalog recipes included.
		Reviews: []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "changes", Findings: 2}, {Round: 1, Tier: runstore.TierSenior, Verdict: "ship"}, {Round: 2, Tier: runstore.TierFirst, Verdict: "ship", Bounce: 1}},
		Verify: []verify.Record{{
			N: 1, Kind: verify.KindTest, Rerun: true, HeadSHA: "abcdef1234567", CleanTree: true, ExitCode: 1,
			TimedOut: true, Tests: 3, Failures: 1, Skipped: 1, Failed: []string{"pkg.A"}, Flaky: []string{"pkg.B"},
			Warning: "w", StartedAt: at, DurationS: 1.5,
		}},
		CostUSD: 4.12, Cost: &cost,
		Stages: []runstore.StageTiming{{Name: "implement", StartedAt: at, DurationS: 61, ModelUSD: 0.42, ModelNotionalUSD: 0.12, Model: "claude-sonnet-5-5", Calls: &calls, Agents: &agents}},
		// The switch log (C1.16, D140): the service's record of a credential
		// change beside the cache-miss estimate only the gateway can compute,
		// including the one a tier's unmeasured cache behaviour leaves
		// unknown rather than free (F24).
		Switches: []runstore.Switch{
			{To: "repo-shared-b", Reason: "exhausted", CacheMissUSD: 0.00054},
			{From: "repo-shared-b", To: "api-key", Reason: "not_admitted", CacheMissUnmeasured: true},
		},
		StartedAt: at, Deadline: &deadline, FinishedAt: &finished,
		FinalizeReserveS: 90,
		// The artifact fields ride along on the fully-populated record so a
		// new one that is not in the schema fails this test: that is what
		// keeps runstore.Record and result.schema.json in step.
		Render: &runstore.RenderRecord{Command: "sh render.sh", CommandSHA256: strings.Repeat("a", 64),
			Tree: "working", ExitCode: 0, DurationS: 12.5, StartedAt: at.Format(time.RFC3339)},
		Artifacts: []runstore.ArtifactFile{{Name: "sizzle.mp4", Bytes: 4096, SHA256: strings.Repeat("b", 64),
			ContentType: "video/mp4", Probe: &runstore.ProbeResult{Ran: true, ExitCode: 1, Warning: "w"}}},
		// And the rigor block (docs/rigor-modes/implementation-plan.md T1.2):
		// the mode the run resolved, the layer that won, the areas that
		// matched with the moment each was applied, and the revision it read.
		Rigor: &runstore.RigorRecord{Mode: rigor.Critical, From: rigor.FromArea,
			Areas: []runstore.RigorArea{
				{Name: "payments", Mode: rigor.Critical, At: runstore.RigorAtBootstrap},
				{Name: "docs", Mode: rigor.POC, At: runstore.RigorAtRound},
			},
			At:       runstore.RigorAtFinalize,
			Revision: "origin/main"},
		// And the expectation tier the run was dispatched at and the figures
		// the run's governance resolved for it (docs/task-governance T8.1,
		// D330): a block a run launched with --tier writes, so a schema that
		// refused it would refuse the runner's own record.
		Tier: &runstore.TierRecord{Tier: tier.Small, ExpectedUSD: 1, ExpectedMinutes: 30},
		// And the items the run filed (docs/rigor-modes T4.5, D9): ids of
		// the files a reviewer finds under docs/backlog/ on the branch.
		Backlog: []string{"20261024-01", "20261024-02"},
		// And the items it closed as the follow-up that fixed them (D31).
		BacklogClosed: []string{"20261023-01"},
		// And what the mode let through (docs/rigor-modes T3.4, D9, F19):
		// the findings themselves, each with the score the table read, the
		// mode that tolerated it and the reviewer's own words for it.
		Tolerated: runstore.NewTolerated([]runstore.ToleratedFinding{
			{Title: "the retry loop has no backoff", Mode: rigor.Startup, Likelihood: 1, Impact: 3, FixCost: 2, Class: rigor.Correctness},
			{Title: "a long comment restates the function", Mode: rigor.Startup, Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Cosmetic},
		}),
		// And the review-scope comparison (docs/task-governance T5.2, F2,
		// D332): the paths implement changed, the paths the review range
		// covers, and what of the two is outside, with the range kept wide by
		// design recorded as not applicable rather than as a mismatch.
		ReviewScope: runstore.NewReviewScope(false, runstore.ReviewScopeNoNarrowerRange,
			[]string{"internal/runner/diffbase.go"},
			[]string{"internal/runner/diffbase.go", "internal/runner/reviewinput.go"}),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a runner record: %v\n%s", err, data)
	}
	bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, []byte(`"status":"failed"`), []byte(`"status":"done"`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(bad); err == nil {
		t.Fatal(`schema accepts "status": "done"`)
	}
	// The cost object is closed, so a compute_source that is not a string
	// (what a hand-edited result.json could carry) is rejected rather than
	// stored.
	lit := []byte(`"compute_source":"` + cost.ComputeSource + `"`)
	if !bytes.Contains(data, lit) {
		t.Fatalf("fixture lacks the literal this test replaces: %s", data)
	}
	bad, err = jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, lit, []byte(`"compute_source":5`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(bad); err == nil {
		t.Fatal("schema accepts a numeric compute_source")
	}
	// A switch log entry is closed too, and its estimate and its "unmeasured"
	// fact are different things: a record that carries a string where the
	// estimate goes, or drops the reason, is refused rather than stored.
	miss := []byte(`"cache_miss_usd":0.00054`)
	if !bytes.Contains(data, miss) {
		t.Fatalf("fixture lacks the literal this test replaces: %s", data)
	}
	bad, err = jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, miss, []byte(`"cache_miss_usd":"unknown"`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(bad); err == nil {
		t.Error("schema accepts a switch whose cache-miss estimate is not a number")
	}
	bad, err = jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, []byte(`"reason":"exhausted"`), []byte(`"why":"exhausted"`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(bad); err == nil {
		t.Error("schema accepts a switch with no reason")
	}
	// The rigor block is closed as well (docs/rigor-modes T1.2, D22, D24,
	// D25): the vocabulary, the layer names, D22's three moments and the
	// block's own keys are each a set the runner writes and a reader may rely
	// on, so a value outside one of them is a record no reader should have to
	// render. Each area carries its own mode, because D23's order is by it:
	// without the field a reader could not tell the strictest rule from the
	// first one written.
	for _, tc := range []struct{ name, old, new string }{
		{"a mode outside the vocabulary", `"mode":"critical"`, `"mode":"paranoid"`},
		{"a layer that is not one", `"from":"area"`, `"from":"floor"`},
		{"the layer only a resolution names", `"from":"area"`, `"from":"reached"`},
		{"the block's moment outside D22's three", `"at":"finalize"`, `"at":"sometime"`},
		{"an area's moment outside D22's three", `"at":"round"`, `"at":"lunchtime"`},
		{"a revision that is not text", `"revision":"origin/main"`, `"revision":5`},
		{"an area with no name", `"name":"payments"`, `"match":"internal/payments/**"`},
		{"an area with no mode", `"mode":"critical","at":"bootstrap"`, `"at":"bootstrap"`},
		{"an area's mode outside the vocabulary", `"mode":"critical","at":"bootstrap"`, `"mode":"paranoid","at":"bootstrap"`},
		{"a name that is not one", `"name":"payments"`, `"name":"../../etc/passwd"`},
		{"the block with no mode", `"mode":"critical",`, ``},
		{"the block with no layer", `"from":"area",`, ``},
		{"the block with no moment", `,"at":"finalize"`, ``},
		{"a key the block does not carry", `"revision":"origin/main"`, `"score":9`},
	} {
		old, repl := []byte(tc.old), []byte(tc.new)
		if !bytes.Contains(data, old) {
			t.Fatalf("fixture lacks the literal this test replaces (%s): %s", tc.name, data)
		}
		bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, old, repl, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(bad); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, repl)
		}
	}
	// The filed items (docs/rigor-modes T4.5, D9): an id is the day and the
	// day's sequence, so a record carrying anything else names no file. The
	// field is additive — a record from before it existed, which is most of
	// them, decodes exactly as it did — which is what D9's note about the
	// version staying 1 means.
	for _, tc := range []struct{ name, old, new string }{
		{"an id that is not one", `["20261024-01","20261024-02"]`, `["20261024-1"]`},
		{"an id with no day", `"20261024-01"`, `"not-an-id"`},
		{"an id that is not text", `"20261024-01"`, `20261024`},
		{"a filing the record cannot name", `["20261024-01","20261024-02"]`, `{"20261024-01":1}`},
	} {
		old, repl := []byte(tc.old), []byte(tc.new)
		if !bytes.Contains(data, old) {
			t.Fatalf("fixture lacks the literal this test replaces (%s): %s", tc.name, data)
		}
		bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, old, repl, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(bad); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, repl)
		}
	}
	// The closed items (D31) are the same ids, held to the same shape, and
	// bounded: a run closes what the runs of one pull request filed, which
	// runstore.MaxBacklogClosed is the ceiling of.
	for _, tc := range []struct{ name, old, new string }{
		{"a closed id that is not one", `"backlog_closed":["20261023-01"]`, `"backlog_closed":["20261023-1"]`},
		{"a closed id that is not text", `"backlog_closed":["20261023-01"]`, `"backlog_closed":[20261023]`},
		{"a close the record cannot name", `"backlog_closed":["20261023-01"]`, `"backlog_closed":{"20261023-01":1}`},
		{"more closes than the bound", `"backlog_closed":["20261023-01"]`, `"backlog_closed":[` + strings.TrimSuffix(strings.Repeat(`"20261023-01",`, runstore.MaxBacklogClosed+1), ",") + `]`},
	} {
		old, repl := []byte(tc.old), []byte(tc.new)
		if !bytes.Contains(data, old) {
			t.Fatalf("fixture lacks the literal this test replaces (%s): %s", tc.name, data)
		}
		bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, old, repl, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(bad); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, repl)
		}
	}
	// What the mode let through (docs/rigor-modes T3.4, D9, F19) is closed
	// too: an entry is the score the table read, the mode that tolerated it
	// and the word for the score, so a value outside one of those vocabularies
	// is a finding no reader should have to render — and the list is bounded
	// the way the record's other bounded lists are.
	for _, tc := range []struct{ name, old, new string }{
		{"a label that is not one", `"label":"low"`, `"label":"minor"`},
		{"a mode outside the vocabulary", `"mode":"startup","likelihood":1`, `"mode":"paranoid","likelihood":1`},
		{"a class outside the vocabulary", `"class":"cosmetic"`, `"class":"style"`},
		{"an axis outside the scale", `"likelihood":1`, `"likelihood":0`},
		{"a finding with no title", `"title":"the retry loop has no backoff",`, ``},
		{"a finding with no score", `"likelihood":1,"impact":3,"fix_cost":2,"class":"correctness"`, `"class":"correctness"`},
		{"a key the finding does not carry", `"fix_cost":2`, `"severity":"minor"`},
		{"a title longer than the bound", `"title":"the retry loop has no backoff"`, `"title":"` + strings.Repeat("x", runstore.MaxToleratedTitle+1) + `"`},
		{"more findings than the bound", `"findings":[`, `"findings":[` + strings.Repeat(`{"title":"t","label":"nit","mode":"startup","likelihood":1,"impact":3,"fix_cost":2,"class":"correctness"},`, runstore.MaxTolerated)},
	} {
		old, repl := []byte(tc.old), []byte(tc.new)
		if !bytes.Contains(data, old) {
			t.Fatalf("fixture lacks the literal this test replaces (%s): %s", tc.name, data)
		}
		bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, old, repl, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(bad); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, repl)
		}
	}
	// The review-scope comparison (docs/task-governance T5.2) is closed as
	// well: its reason is one of a set the runner writes and a reader may
	// render, the paths are bounded the way the record's other bounded lists
	// are, and the block carries no key beyond those — so a record with a
	// reason a digest cannot render, or a path at a length the record never
	// holds, is refused rather than stored.
	for _, tc := range []struct{ name, old, new string }{
		{"a reason outside the set", `"why_not":"no-narrower-range"`, `"why_not":"because"`},
		{"a reason that is not text", `"why_not":"no-narrower-range"`, `"why_not":5`},
		{"the block with no applicable", `"applicable":false,`, ``},
		{"an applicable that is not a boolean", `"applicable":false`, `"applicable":"no"`},
		{"a key the block does not carry", `"outside":[`, `"mismatch":[`},
		{"a path that is not text", `"internal/runner/diffbase.go"`, `5`},
		{"a path longer than the bound", `"internal/runner/diffbase.go"`, `"` + strings.Repeat("x", runstore.MaxReviewScopePath+1) + `"`},
		{"more paths than the bound", `"implement_files":[`, `"implement_files":[` + strings.Repeat(`"a",`, runstore.MaxReviewScopeFiles)},
	} {
		old, repl := []byte(tc.old), []byte(tc.new)
		if !bytes.Contains(data, old) {
			t.Fatalf("fixture lacks the literal this test replaces (%s): %s", tc.name, data)
		}
		bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, old, repl, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(bad); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, repl)
		}
	}
	without := rec
	without.Backlog = nil
	without.BacklogClosed = nil
	without.Tolerated = nil
	without.ReviewScope = nil
	bare, err := json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bare, []byte(`"backlog"`)) || bytes.Contains(bare, []byte(`"backlog_closed"`)) ||
		bytes.Contains(bare, []byte(`"tolerated"`)) || bytes.Contains(bare, []byte(`"review_scope"`)) {
		t.Fatalf("a run that filed, closed and tolerated nothing still writes the fields: %s", bare)
	}
	inst, err = jsonschema.UnmarshalJSON(bytes.NewReader(bare))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a record that filed, closed and tolerated nothing: %v\n%s", err, bare)
	}
}

// TestTierAdversarialInSchema: the review entry's tier enum is the vocabulary
// internal/runstore writes — level 3's adversarial review included
// (docs/review-levels-ab-jobmax L4.1, D727) — and the entry carries the head
// the review read, bounded like the schema's other commit fields. A tier
// added to runstore and not here is a record the runner legally wrote and its
// own published schema refuses.
func TestTierAdversarialInSchema(t *testing.T) {
	sch := compile(t, "result.schema.json")
	head := strings.Repeat("a", 40)
	rec := runstore.Record{
		Version: 1, RunID: "20261030-120000-abcd", Status: runstore.StatusSucceeded,
		Stage: "review",
		Reviews: []runstore.ReviewSummary{
			{Round: 3, Tier: runstore.TierFirst, Verdict: "ship", Bounce: 1},
			{Round: 3, Tier: runstore.TierSenior, Verdict: "ship", Head: head},
			{Round: 3, Tier: runstore.TierAdversarial, Verdict: "ship", Head: head},
		},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a record naming the adversarial tier and its head: %v\n%s", err, data)
	}

	raw, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var reviews struct {
		Items struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"items"`
	}
	if err := json.Unmarshal(doc.Properties["reviews"], &reviews); err != nil {
		t.Fatal(err)
	}
	var tier struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(reviews.Items.Properties["tier"], &tier); err != nil {
		t.Fatal(err)
	}
	want := []string{runstore.TierFirst, runstore.TierSenior, runstore.TierAdversarial}
	if !slices.Equal(tier.Enum, want) {
		t.Errorf("result.schema.json reviews tier enum %v != runstore's tiers %v", tier.Enum, want)
	}
	var headField struct {
		Type      string `json:"type"`
		MinLength int    `json:"minLength"`
	}
	if err := json.Unmarshal(reviews.Items.Properties["head"], &headField); err != nil {
		t.Fatal(err)
	}
	if headField.Type != "string" || headField.MinLength != 1 {
		t.Errorf("result.schema.json reviews head = %+v, want a string bounded at minLength 1 like the schema's other commit fields", headField)
	}
	lit := []byte(`"head":"` + head + `"`)
	if !bytes.Contains(data, lit) {
		t.Fatalf("fixture lacks the literal this test replaces: %s", data)
	}
	bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, lit, []byte(`"head":""`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(bad); err == nil {
		t.Error("schema accepts an empty review head")
	}
	bounce := []byte(`"bounce":1`)
	if !bytes.Contains(data, bounce) {
		t.Fatalf("fixture lacks the literal this test replaces: %s", data)
	}
	bad, err = jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, bounce, []byte(`"bounce":-1`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(bad); err == nil {
		t.Error("schema accepts a review that followed a negative number of cheap loops")
	}
}

// TestResultSchemaAcceptsAnArtifactRunRecord: a mode: artifact run's record
// validates in both of its shapes — the success (outcome artifacts, stage
// render, a render object) and the failed render (outcome none, timed_out
// true) — and the schema rejects a render object that is not one (a
// command_sha256 that is not a hash, an unknown tree), and an outcome or a
// stage the artifact mode never writes.
//
// Mutation (drop "artifacts" from the outcome enum, or the render property, or
// the artifacts property's minItems, in schemas/result.schema.json): this test
// fails — the first two on the success record it marshals, the last on the
// present-but-empty array it builds.
func TestResultSchemaAcceptsAnArtifactRunRecord(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	render := &runstore.RenderRecord{
		Command: "sh render.sh", CommandSHA256: strings.Repeat("a", 64), Tree: "working",
		ExitCode: 0, DurationS: 12.5, StartedAt: at.Format(time.RFC3339),
	}
	rec := runstore.Record{
		Version: 1, RunID: "20261015-100000-abcd", Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusSucceeded, Stage: "render", Outcome: runstore.OutcomeArtifacts,
		StartedAt: at, Render: render,
		Inputs: []runstore.InputFile{{Name: "shot.png", Bytes: 2048, SHA256: strings.Repeat("b", 64)}},
		Artifacts: []runstore.ArtifactFile{{Name: "sizzle.png", Bytes: 6, SHA256: strings.Repeat("b", 64),
			ContentType: "image/png", Probe: &runstore.ProbeResult{Ran: true, ExitCode: 0}}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a succeeded artifact run: %v\n%s", err, data)
	}
	// A present-but-empty artifacts array is refused: the field is ABSENT when
	// a run delivered none (it is omitempty on the record, and a run whose
	// globs matched nothing fails before it stages anything), so a reader that
	// finds the key knows there is at least one entry to read and does not
	// have to fold "no artifacts" and "an empty list of them" together.
	empty := map[string]any{}
	if err := json.Unmarshal(data, &empty); err != nil {
		t.Fatal(err)
	}
	empty["artifacts"] = []any{}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	} else if err := sch.Validate(inst); err == nil {
		t.Fatalf("schema accepts an empty artifacts array; the field is absent when there are none: %s", raw)
	}
	timedOut := *render
	timedOut.TimedOut, timedOut.ExitCode = true, -1
	rec.Status, rec.Outcome, rec.Stage = runstore.StatusFailed, runstore.OutcomeNone, "render"
	rec.Render = &timedOut
	data, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if inst, err = jsonschema.UnmarshalJSON(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a timed-out artifact run: %v\n%s", err, data)
	}
	// The render object is closed and its hash is a hash: a hand-edited
	// result.json cannot smuggle a free-form command_sha256 into the one
	// field a reviewer is meant to be able to recompute. The artifact
	// entries and their probes are closed the same way, and an entry
	// without a name, a size or a hash is not an artifact a reader could
	// verify what it downloads against.
	for _, tc := range []struct{ name, old, new string }{
		{"bad hash", strings.Repeat("a", 64), "not-a-hash"},
		{"bad tree", `"tree":"working"`, `"tree":"head"`},
		{"unknown key", `"exit_code":-1`, `"exit_code":-1,"extra":1`},
		{"negative input size", `"bytes":2048`, `"bytes":-1`},
		{"input without a hash", `"sha256":"` + strings.Repeat("b", 64) + `"`, `"sha256":"abc"`},
		{"artifact without a name", `"name":"sizzle.png"`, `"nope":"sizzle.png"`},
		{"artifact unknown key", `"bytes":6`, `"bytes":6,"extra":1`},
		{"artifact bad content type", `"content_type":"image/png"`, `"content_type":7`},
		{"probe with no exit code", `"probe":{"ran":true,"exit_code":0}`, `"probe":{"ran":true}`},
		{"probe that is not a probe", `"exit_code":0`, `"exit_code":"zero"`},
	} {
		raw := strings.Replace(string(data), tc.old, tc.new, 1)
		if raw == string(data) {
			t.Fatalf("%s: fixture lacks %s", tc.name, tc.old)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(raw)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, raw)
		}
	}
}

// TestResultSchemaStatesTheNoProbeVerdict is Review Focus 6's schema half for
// the probe: a record this runner writes carries a probe verdict for every
// artifact it uploaded, and a recipe that asked for no probe records
// {ran: false, exit_code: -1} rather than leaving the key out — "no probe
// ran" is stated in the record, not inferred from a field that is not there
// (plan Task 5). The schema says the same thing in the one place it can
// enforce it: a verdict that did not run must carry the -1 that marks it, so
// a record — hand-edited, or written by something that got this wrong — can
// never read as a probe that ran and passed with exit code 0.
//
// The key itself stays out of the item's required list, and the description
// says why: a record written before the probe step existed carries none, the
// same reason the artifacts array is optional at the record level. What is
// required is the shape of a verdict that IS there.
//
// Mutation (drop the allOf from the schema's probe object, or set its const
// to 0): this test fails on the {ran: false, exit_code: 0} document it
// builds, which the schema then accepts.
func TestResultSchemaStatesTheNoProbeVerdict(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{
		Version: 1, RunID: "20261016-100000-abcd", Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeArtifacts,
		StartedAt: at,
		Artifacts: []runstore.ArtifactFile{{
			Name: "sizzle.png", Bytes: 6, SHA256: strings.Repeat("b", 64), ContentType: "image/png",
			Probe: &runstore.ProbeResult{Ran: false, ExitCode: -1},
		}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"probe":{"ran":false,"exit_code":-1}`) {
		t.Fatalf("the record does not carry the no-probe verdict this test is about: %s", data)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a no-probe verdict: %v\n%s", err, data)
	}
	// The same artifact with the exit code a PASSING probe would have: the
	// schema must refuse it, because a reader cannot tell the two apart
	// otherwise and the whole point of the -1 is that it is not 0.
	raw := strings.Replace(string(data), `"exit_code":-1`, `"exit_code":0`, 1)
	inst, err = jsonschema.UnmarshalJSON(bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Fatalf("the schema accepts a probe that did not run with a passing exit code: %s", raw)
	}
	// A verdict that ran is unconstrained on the far side: a probe killed by
	// its deadline is {ran: true, exit_code: -1}, which is also what
	// procgroup reports for a signalled process.
	raw = strings.Replace(string(data), `"ran":false,"exit_code":-1`, `"ran":true,"exit_code":-1`, 1)
	inst, err = jsonschema.UnmarshalJSON(bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a probe that ran and was killed: %v\n%s", err, raw)
	}
}

// TestResultSchemaAcceptsARecordWithoutArtifacts is Review Focus 8's schema
// half: a result.json written before this change validates without an
// artifacts key at all, so an old record is never refused for a field that
// did not exist when it was written, and the new field is not in the
// schema's required list.
//
// Mutation (add artifacts to the schema's required list): this test fails,
// the schema refusing the pre-change record.
func TestResultSchemaAcceptsARecordWithoutArtifacts(t *testing.T) {
	sch := compile(t, "result.schema.json")
	const pre = `{
	  "version": 1,
	  "run_id": "20260926-221530-abcd",
	  "repo": "acme/app",
	  "workflow": "app",
	  "status": "succeeded",
	  "stage": "writeback",
	  "outcome": "ready",
	  "branch": "fugaro/add-a-feature",
	  "head_sha": "abcdef1234567",
	  "cost_usd": 4.12,
	  "started_at": "2026-09-26T22:15:30Z"
	}`
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(pre))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a pre-change record: %v", err)
	}
	var rec runstore.Record
	if err := json.Unmarshal([]byte(pre), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Artifacts != nil || rec.Render != nil {
		t.Fatalf("a pre-change record gained fields: %+v", rec)
	}
}

// TestResultSchemaOAuthAccount: a record naming a Claude OAuth account
// (docs/design/named-oauth-tokens.md) validates; a non-string value (what a
// hand-edited or corrupt result.json could carry) does not. config.
// ClaudeAccount's name regex (plan Task 1) has not landed on this branch, so
// the schema only bounds the length, not the charset; this test (and the
// property itself) is the thing to tighten once Task 1's
// ClaudeAccountNameRE exists.
func TestResultSchemaOAuthAccount(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{Version: 1, RunID: "20261009-100000-abcd", Repo: "acme/app",
		Status: runstore.StatusRunning, Stage: "implement", StartedAt: at, OAuthAccount: "acme-personal"}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a record with a named oauth_account: %v\n%s", err, data)
	}
	bad, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, []byte(`"oauth_account":"acme-personal"`), []byte(`"oauth_account":5`), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"oauth_account":"acme-personal"`)) {
		t.Fatalf("fixture lacks the literal this test replaces: %s", data)
	}
	if err := sch.Validate(bad); err == nil {
		t.Fatal("schema accepts a non-string oauth_account")
	}
	// The bound: a record's account name is data the runner wrote and the
	// display sanitises it, so the schema is lenient (a string) but bounded:
	// 64 runes are accepted, 65 are refused.
	for _, tc := range []struct {
		n    int
		want bool
	}{{64, true}, {65, false}} {
		long, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, []byte(`"oauth_account":"acme-personal"`), []byte(`"oauth_account":"`+strings.Repeat("a", tc.n)+`"`), 1)))
		if err != nil {
			t.Fatal(err)
		}
		if got := sch.Validate(long) == nil; got != tc.want {
			t.Errorf("oauth_account of %d runes: schema accepts = %v, want %v", tc.n, got, tc.want)
		}
	}
}

// TestResultSchemaStageCallsAndAgents pins the schema half of the record's
// two stage counters (docs/task-governance constraint G5): both are optional
// non-negative integers, and absent stays distinct from zero.
func TestResultSchemaStageCallsAndAgents(t *testing.T) {
	sch := compile(t, "result.schema.json")
	validate := func(stage string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(
			`{"version":1,"run_id":"20261026-100000-abcd","status":"running","stage":"implement",` +
				`"outcome":"none","cost_usd":0,"started_at":"2026-10-26T10:00:00Z","stages":[` + stage + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	const head = `{"name":"implement","started_at":"2026-10-26T10:00:00Z","duration_s":61`
	for _, tc := range []struct {
		name  string
		stage string
	}{
		{"both counts", head + `,"calls":12,"agents":2}`},
		{"neither count", head + `}`},
		{"zero is a count", head + `,"calls":0,"agents":0}`},
		{"a repeats count", head + `,"repeats":5}`},
		{"zero repeats", head + `,"repeats":0}`},
	} {
		if err := validate(tc.stage); err != nil {
			t.Errorf("schema rejects a stage with %s: %v", tc.name, err)
		}
	}
	for _, tc := range []struct {
		name  string
		stage string
	}{
		{"negative calls", head + `,"calls":-1}`},
		{"negative agents", head + `,"agents":-1}`},
		{"fractional calls", head + `,"calls":1.5}`},
		{"string agents", head + `,"agents":"2"}`},
		{"negative repeats", head + `,"repeats":-1}`},
		{"fractional repeats", head + `,"repeats":4.5}`},
		// The count is all a stage keeps of a repeated command (task-governance
		// T1.4): a command can carry a token or a path, so there is no field a
		// command string could be written into.
		{"a command string", head + `,"command":"go test ./..."}`},
	} {
		if err := validate(tc.stage); err == nil {
			t.Errorf("schema accepts %s: %s", tc.name, tc.stage)
		}
	}
}

func TestResultSchemaImageBlock(t *testing.T) {
	sch := compile(t, "result.schema.json")
	base := `{"version":1,"run_id":"20260927-100000-abcd","status":"running","stage":"bootstrap","outcome":"none","cost_usd":0,"started_at":"2026-09-27T10:00:00Z"`
	check := func(image string, ok bool) {
		t.Helper()
		doc := base
		if image != "" {
			doc += `,"image":` + image
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc + "}"))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); (err == nil) != ok {
			t.Errorf("image %s: valid = %v, want %v (%v)", image, err == nil, ok, err)
		}
	}
	check(``, true)
	check(`{"baked_commit":"abc123"}`, true)
	check(`{"baked_commit":"abc123","built_at":"2026-09-28T10:00:00Z"}`, true)
	check(`{"built_at":"2026-09-28T10:00:00Z"}`, false)
	check(`{"baked_commit":""}`, false)
	check(`{"baked_commit":"abc123","built_at":5}`, false)
	check(`{"baked_commit":"abc123","commits_behind":3}`, false)

	// What the runner writes validates.
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	data, err := json.Marshal(runstore.Record{Version: 1, RunID: "20260927-100000-abcd", Status: runstore.StatusRunning, Stage: "bootstrap",
		Outcome: runstore.OutcomeNone, StartedAt: at, Image: &runstore.ImageInfo{BuiltAt: &at, BakedCommit: "abc123"}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a runner record with an image block: %v\n%s", err, data)
	}
}

// TestFugaroSchemaReservesTheSameEnv keeps the schema's reserved secret
// variables in step with config.ReservedEnv.
func TestFugaroSchemaReservesTheSameEnv(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	doc := func(env string) []byte {
		return []byte("version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  server:\n" +
			"    commands: { build: make, test: make test }\n    secrets: [{ name: tok, env: " + env + " }]\n")
	}
	if err := sch.Validate(yamlInstance(t, doc("NPM_TOKEN"))); err != nil {
		t.Fatalf("schema rejects an ordinary secret variable: %v", err)
	}
	var envs []string
	envs = append(envs, config.ReservedEnvNames...)
	for _, p := range config.ReservedEnvPrefixes {
		envs = append(envs, p+"X")
	}
	for _, env := range envs {
		if !config.ReservedEnv(env) {
			t.Fatalf("config.ReservedEnv(%q) = false", env)
		}
		if err := sch.Validate(yamlInstance(t, doc(env))); err == nil {
			t.Errorf("schema accepts the reserved secret variable %s", env)
		}
	}
}

// TestFugaroSchemaReservesTheSameSecretNames keeps the schema's reserved
// secret names equal to config.ReservedSecrets, and its providers equal to
// config.Providers.
func TestFugaroSchemaReservesTheSameSecretNames(t *testing.T) {
	data, err := os.ReadFile("fugaro.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	at := func(v any, path ...string) any {
		for _, k := range path {
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("schema has no %v", path)
			}
			v = m[k]
		}
		return v
	}
	strs := func(v any) []string {
		var out []string
		for _, x := range v.([]any) {
			out = append(out, x.(string))
		}
		slices.Sort(out)
		return out
	}
	wf := at(doc, "$defs", "workflow")
	// secrets[].name's "not" clause now reserves two shapes: the platform's
	// fixed names (an enum, unchanged) and the whole claude-oauth-token-
	// prefix, config.ClaudeOAuthTokenPrefix's namespace (a pattern, added
	// alongside config.Validate's own refusal of it as a workflow secret —
	// a schema/parser pair, kept aligned the same way the enum already is).
	notClause, ok := at(wf, "properties", "secrets", "items", "properties", "name", "not").(map[string]any)
	if !ok {
		t.Fatalf("schema's secrets[].name.not is not an object: %v", at(wf, "properties", "secrets", "items", "properties", "name", "not"))
	}
	anyOf, ok := notClause["anyOf"].([]any)
	if !ok {
		t.Fatalf("schema's secrets[].name.not has no anyOf: %v", notClause)
	}
	var names []string
	var prefix string
	for _, c := range anyOf {
		m, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("anyOf entry is not an object: %v", c)
		}
		if e, ok := m["enum"]; ok {
			names = strs(e)
		}
		if p, ok := m["pattern"].(string); ok {
			prefix = p
		}
	}
	if want := slices.Sorted(maps.Keys(config.ReservedSecrets)); !slices.Equal(names, want) {
		t.Errorf("schema reserves secret names %v, config.ReservedSecrets has %v", names, want)
	}
	if want := "^" + config.ClaudeOAuthTokenPrefix; prefix != want {
		t.Errorf("schema reserves the claude-oauth-token- prefix as %q, want %q (config.ClaudeOAuthTokenPrefix)", prefix, want)
	}
	providers := strs(at(doc, "properties", "git", "properties", "provider", "enum"))
	if want := slices.Sorted(slices.Values(config.Providers)); !slices.Equal(providers, want) {
		t.Errorf("schema providers %v, config.Providers %v", providers, want)
	}
}

// The schema judges max_age as fugaro does for 0 in its every form (the
// string "0" included, which turns the age trigger off) and for malformed
// values. Other Go durations fugaro reads, such as 90m or 1.5h, are left
// out: the schema asks for days or hours first, and the 1h to 90d bounds
// are fugaro's to check.
func TestFugaroSchemaMaxAgeAgreesWithGo(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	const base = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web:\n    commands: { build: npm run build, test: npm test }\n    rebuild: { max_age: "
	for _, v := range []string{`0`, `"0"`, `0d`, `0s`, `0m`, `0h`, `0h0m`, `"00"`, `1h`, `14d`, `1d12h`, `90d`,
		`soon`, `""`, `-1h`, `2d-5h`, `"0x"`, `1`, `0d-0h`} {
		doc := []byte(base + v + " }\n")
		_, problems := config.Parse(doc)
		goOK := len(problems) == 0
		schemaOK := sch.Validate(yamlInstance(t, doc)) == nil
		if goOK != schemaOK {
			t.Errorf("max_age: %s: fugaro accepts it: %v, the schema: %v (%v)", v, goOK, schemaOK, problems)
		}
	}
}

// TestResultSchemaFollowUp: a follow-up's record, and every run's
// pushed_head, validate; a session that is neither resumed nor fresh doesn't.
func TestResultSchemaFollowUp(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{
		Version: 1, RunID: "20260930-100000-abcd", Status: runstore.StatusRunning, Stage: "bootstrap", Outcome: runstore.OutcomeNone,
		Branch: "fugaro/20260929-100000-0a1b", PR: &runstore.PRRef{Number: 12, URL: "https://example.invalid/pr/12"},
		PushedHead: "0123456789abcdef0123456789abcdef01234567", StartedAt: at,
		FollowUp: &runstore.FollowUp{
			PR: 12, PreviousRun: "20260929-100000-0a1b", StartSHA: "0123456789abcdef0123456789abcdef01234567",
			Session: "resumed", SessionNote: "resumed", Comments: 2,
			Authors: map[string]int{"Ada": 2}, UntrustedAuthors: []string{"mallory"}, UntrustedAuthorCount: 1, Omitted: map[string]int{"self": 1},
		},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a follow-up record: %v\n%s", err, data)
	}
	// A minimal follow_up (what bootstrap saves first) validates too.
	first := rec
	first.PushedHead, first.FollowUp = "", &runstore.FollowUp{PR: 12, PreviousRun: "20260929-100000-0a1b"}
	minimal, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if inst, err = jsonschema.UnmarshalJSON(bytes.NewReader(minimal)); err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a minimal follow_up: %v\n%s", err, minimal)
	}
	bad := bytes.Replace(data, []byte(`"session":"resumed"`), []byte(`"session":"maybe"`), 1)
	if bytes.Equal(bad, data) {
		t.Fatalf("no session in %s", data)
	}
	if inst, err = jsonschema.UnmarshalJSON(bytes.NewReader(bad)); err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Fatal(`schema accepts "session": "maybe"`)
	}
}

// TestFugaroSchemaTrustedAgreesWithGo: the schema and fugaro agree on
// followup.trusted, except for the cases listed in looser, which JSON
// Schema can't check: it sees the YAML-resolved value, not its text, so a
// number written with leading zeros, in hex or as a float, and a number
// and a string of the same digits in one list, look valid to it. fugaro
// (config.Parse) is the authority and refuses them.
func TestFugaroSchemaTrustedAgreesWithGo(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	doc := func(provider, trusted string) []byte {
		return []byte("version: 1\nproject: aurora\ngit: { provider: " + provider + " }\nworkflows:\n  web:\n    commands: { build: npm run build, test: npm test }\nfollowup:\n  trusted: " + trusted + "\n")
	}
	looser := map[string]bool{
		"github " + `[01234567]`:           true,
		"github " + `[0x10]`:               true,
		"github " + `[1e3]`:                true,
		"github " + `[1.0]`:                true,
		"github " + `[1234567, "1234567"]`: true,
		// 10^20 has 21 digits; the schema's bound is 10^20 so that 20
		// nines, which decode to 10^20 as a float, pass.
		"github " + `[100000000000000000000]`: true,
	}
	cases := []struct{ provider, trusted string }{
		{"github", `[]`}, {"github", `null`}, {"github", `[1234567]`}, {"github", `["1234567"]`},
		{"github", `[99999999999999999999]`}, {"github", `[100000000000000000000]`}, {"github", `["99999999999999999999"]`}, {"github", `["123456789012345678901"]`},
		{"github", `["01234567"]`}, {"github", `[01234567]`}, {"github", `[0]`}, {"github", `[-1]`}, {"github", `[0x10]`},
		{"github", `[1e3]`}, {"github", `[1.0]`}, {"github", `["octocat"]`}, {"github", `[""]`},
		{"github", `["1234567", "1234567"]`}, {"github", `[1234567, "1234567"]`},
		{"bitbucket", `["557058:00000000-0000-0000-0000-000000000001"]`}, {"bitbucket", `["0123456789abcdef01234567"]`},
		{"bitbucket", `["{00000000-0000-0000-0000-000000000001}"]`}, {"bitbucket", `["0123456789ABCDEF01234567"]`},
		{"bitbucket", `["someone"]`}, {"bitbucket", `[1234567]`},
	}
	for _, c := range cases {
		d := doc(c.provider, c.trusted)
		_, problems := config.Parse(d)
		goOK := len(problems) == 0
		schemaOK := sch.Validate(yamlInstance(t, d)) == nil
		key := c.provider + " " + c.trusted
		switch {
		case looser[key] && (goOK || !schemaOK):
			t.Errorf("%s: a known difference changed: fugaro accepts it: %v, the schema: %v", key, goOK, schemaOK)
		case !looser[key] && goOK != schemaOK:
			t.Errorf("%s: fugaro accepts it: %v, the schema: %v (%v)", key, goOK, schemaOK, problems)
		}
	}
}

func TestResultSchemaHalted(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	cost := runstore.NewCost(4.12, 0.38, runstore.BasisAPIList)
	cost.ModelSource, cost.ModelBy, cost.Unreconciled, cost.UsageUnparsed = "gateway", map[string]float64{"claude-a": 4.12}, 0.5, 1
	cost.RouteBy, cost.ReportedUSD = map[string]float64{"openrouter": 1.5}, 1.4
	check := func(reason runstore.HaltReason) error {
		rec := runstore.Record{
			Version: 1, RunID: "20260930-100000-abcd", Status: runstore.StatusHalted, Stage: "implement", Outcome: runstore.OutcomeDraft,
			Reason: "halted: " + string(reason), CostUSD: 4.12, Cost: &cost, StartedAt: at,
			Halt: &runstore.Halt{Reason: reason, Scope: "run", At: at, Detail: "d"},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	for _, r := range []runstore.HaltReason{
		runstore.HaltKillSwitch, runstore.HaltRunCap, runstore.HaltRepoDailyCap, runstore.HaltGlobalDailyCap,
		runstore.HaltNoCap, runstore.HaltTokenCap, runstore.HaltBudgetUnavailable, runstore.HaltBudgetTokenExpired,
		runstore.HaltNoCredential, runstore.HaltCredsRulesDenied,
		runstore.HaltStageBudget, runstore.HaltReviewRatio, runstore.HaltFanout,
		runstore.HaltReviewScope, runstore.HaltExpectedExceeded,
	} {
		if err := check(r); err != nil {
			t.Errorf("schema rejects halt reason %q: %v", r, err)
		}
	}
	if err := check("other"); err == nil {
		t.Error(`schema accepts halt reason "other"`)
	}
}

// TestResultSchemaCheckpoint: the checkpoint block a governance halt leaves
// validates, and the schema refuses the shapes that would let a reader render
// something the runner did not write — a trigger outside the governance set
// (the enum is what makes the value safe to render, so a cap's reason is not
// a checkpoint's trigger), an unknown key, a text field past
// runstore.MaxCheckpointText, a negative figure and a trigger that is not a
// string. A record with no checkpoint writes no key at all: absent is the
// fact "this run did not take a governance halt", which an empty block would
// blur.
func TestResultSchemaCheckpoint(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{
		Version: 1, RunID: "20260930-100000-abcd", Status: runstore.StatusHalted, Stage: "review", Outcome: runstore.OutcomeDraft,
		Reason: "halted: " + string(runstore.HaltFanout), CostUSD: 1, StartedAt: at,
		Halt: &runstore.Halt{Reason: runstore.HaltFanout, Scope: "run", At: at, Detail: "2 sub-agents"},
		Checkpoint: runstore.NewCheckpoint(runstore.Checkpoint{
			Trigger: string(runstore.HaltFanout), WhyOver: "more sub-agents than the maximum", EstimateUSD: 1,
		}),
	}
	validate := func(t *testing.T, data []byte) error {
		t.Helper()
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	marshal := func(t *testing.T, rec runstore.Record) []byte {
		t.Helper()
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	data := marshal(t, rec)
	if err := validate(t, data); err != nil {
		t.Fatalf("schema rejects a checkpoint record: %v\n%s", err, data)
	}

	// The checkpoint a halt writes before the checkpoint stage exists (T7.3)
	// carries the trigger alone, and that is the whole block.
	first := rec
	first.Checkpoint = runstore.NewCheckpoint(runstore.Checkpoint{Trigger: string(runstore.HaltStageBudget)})
	firstData := marshal(t, first)
	if err := validate(t, firstData); err != nil {
		t.Fatalf("schema rejects a checkpoint of the trigger alone: %v\n%s", err, firstData)
	}
	if !bytes.Contains(firstData, []byte(`"checkpoint":{"trigger":"stage_budget"}`)) {
		t.Errorf("a checkpoint of the trigger alone marshals %s", firstData)
	}

	// No checkpoint is an absent key, not an empty block.
	none := rec
	none.Checkpoint = nil
	noneData := marshal(t, none)
	if bytes.Contains(noneData, []byte("checkpoint")) {
		t.Errorf("a record with no checkpoint writes one: %s", noneData)
	}
	if err := validate(t, noneData); err != nil {
		t.Fatalf("schema rejects a governance halt with no checkpoint: %v\n%s", err, noneData)
	}

	for _, tc := range []struct {
		name string
		rec  runstore.Record
		from string
		to   string
		why  string
	}{
		{name: "trigger outside the governance set", from: `"trigger":"fanout"`, to: `"trigger":"kill_switch"`, why: "a cap's reason is not a checkpoint's trigger"},
		{name: "an unknown key", from: `"trigger":"fanout"`, to: `"who":"x","trigger":"fanout"`, why: "the block is closed"},
		{name: "a trigger that is not a string", from: `"trigger":"fanout"`, to: `"trigger":1`, why: "the enum is what makes the value safe to render"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := bytes.Replace(data, []byte(tc.from), []byte(tc.to), 1)
			if bytes.Equal(bad, data) {
				t.Fatalf("no %s in %s", tc.from, data)
			}
			if err := validate(t, bad); err == nil {
				t.Errorf("schema accepts %s (%s): %s", tc.name, tc.why, bad)
			}
		})
	}

	for _, tc := range []struct {
		name string
		cp   *runstore.Checkpoint
	}{
		{"a text field past the bound", &runstore.Checkpoint{Trigger: string(runstore.HaltFanout), Next: strings.Repeat("n", runstore.MaxCheckpointText+1)}},
		{"a negative figure", &runstore.Checkpoint{Trigger: string(runstore.HaltFanout), EstimateUSD: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := rec
			bad.Checkpoint = tc.cp
			badData := marshal(t, bad)
			if err := validate(t, badData); err == nil {
				t.Errorf("schema accepts %s: %s", tc.name, badData)
			}
		})
	}
}

// TestResultSchemaCheckpointAgreeWithGo pins result.schema.json's checkpoint
// block to internal/runstore's own vocabulary and bound, in both directions.
// The trigger enum and the text bound are hand-written copies of
// runstore.GovernanceHaltReasons and runstore.MaxCheckpointText, and the
// runner writes a checkpoint for every reason the former names: a reason the
// enum lacks is a result.json a consumer validating against the published
// schema refuses, a value only the enum knows is a trigger no run can write,
// and a bound that is not the one NewCheckpoint clips to is a wire that either
// accepts what the runner never writes or refuses what it did.
//
// The Go side is read from runstore rather than listed here, so a reason added
// to it without the schema fails this test instead of silently widening the
// gap the next time someone reads the enum above.
func TestResultSchemaCheckpointAgreeWithGo(t *testing.T) {
	data, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	props, _ := doc["properties"].(map[string]any)
	cp, ok := props["checkpoint"].(map[string]any)
	if !ok {
		t.Fatal("result.schema.json: no checkpoint property")
	}
	fields, ok := cp["properties"].(map[string]any)
	if !ok {
		t.Fatal("result.schema.json: checkpoint has no properties")
	}

	trigger, _ := fields["trigger"].(map[string]any)
	raw, ok := trigger["enum"].([]any)
	if !ok {
		t.Fatal("result.schema.json: checkpoint.trigger has no enum")
	}
	var got []string
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("result.schema.json: checkpoint.trigger enum entry %v is not a string", v)
		}
		got = append(got, s)
	}
	var want []string
	for _, reason := range runstore.GovernanceHaltReasons() {
		want = append(want, string(reason))
	}
	if len(want) == 0 {
		t.Fatal("runstore names no governance halt reason: this test proves nothing")
	}
	if !sameStrings(got, want) {
		t.Errorf("result.schema.json checkpoint.trigger = %v, runstore.GovernanceHaltReasons() = %v", got, want)
	}

	for _, name := range []string{"done", "why_over", "next", "hard_instructions"} {
		field, ok := fields[name].(map[string]any)
		if !ok {
			t.Fatalf("result.schema.json: checkpoint.%s is missing", name)
		}
		if got := field["maxLength"]; got != float64(runstore.MaxCheckpointText) {
			t.Errorf("result.schema.json checkpoint.%s maxLength = %v, want runstore.MaxCheckpointText (%d)",
				name, got, runstore.MaxCheckpointText)
		}
	}
}

// TestResultSchemaHaltReasonsAgreeWithGo pins result.schema.json's halt.reason
// enum to the HaltReason constants in internal/runstore, in both directions:
// a reason the Go side can write but the enum lacks is a result.json a
// consumer validating against the published schema refuses (the cascade's
// no_credential and creds_rules_denied were exactly that — the enum was a
// hand-written copy that no test compared to the constants), and a value only
// the enum knows is a reason no run can produce.
//
// The Go constants are read from the source rather than listed here, so a
// reason added to runstore.go without the schema fails this test instead of
// silently widening the gap the next time someone reads the list above.
// Mutation (run, restored): drop a value from the enum, or add one to
// runstore.go, and this names the side that has it.
func TestResultSchemaHaltReasonsAgreeWithGo(t *testing.T) {
	want := runstoreConstValues(t, haltReasonSpec)
	if len(want) == 0 {
		t.Fatal("no HaltReason constants found in ../internal/runstore/runstore.go: this test proves nothing")
	}
	data, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	node, _ := doc["properties"].(map[string]any)
	node, _ = node["halt"].(map[string]any)
	node, _ = node["properties"].(map[string]any)
	node, _ = node["reason"].(map[string]any)
	raw, ok := node["enum"].([]any)
	if !ok {
		t.Fatal(`result.schema.json: halt.reason has no enum`)
	}
	var got []string
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("result.schema.json: halt.reason enum entry %v is not a string", v)
		}
		got = append(got, s)
	}
	if !sameStrings(got, want) {
		t.Errorf("result.schema.json halt.reason = %v, internal/runstore's HaltReason constants = %v", got, want)
	}
}

// TestResultSchemaReviewScopeReasonsAgreeWithGo pins result.schema.json's
// review_scope.why_not enum to the ReviewScope* constants in
// internal/runstore, in both directions: a reason the runner can write but
// the enum lacks is a result.json a consumer validating against the published
// schema refuses, and a value only the enum knows is a reason no run can
// produce. The enum is a hand-written copy of the constants, so nothing but
// this test keeps the two from drifting (TestResultSchemaHaltReasonsAgreeWithGo
// is the same pin for halt.reason).
//
// The Go constants are read from the source rather than listed here, so a
// reason added to runstore.go without the schema fails this test instead of
// silently widening the gap the next time someone reads the list above.
//
// Mutation (run, restored): drop a value from the enum, or add one to
// runstore.go, and this names the side that has it.
func TestResultSchemaReviewScopeReasonsAgreeWithGo(t *testing.T) {
	want := runstoreConstValues(t, reviewScopeSpec)
	if len(want) == 0 {
		t.Fatal("no ReviewScope* constants found in ../internal/runstore/runstore.go: this test proves nothing")
	}
	data, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	node, _ := doc["properties"].(map[string]any)
	node, _ = node["review_scope"].(map[string]any)
	node, _ = node["properties"].(map[string]any)
	node, _ = node["why_not"].(map[string]any)
	raw, ok := node["enum"].([]any)
	if !ok {
		t.Fatal(`result.schema.json: review_scope.why_not has no enum`)
	}
	var got []string
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("result.schema.json: review_scope.why_not enum entry %v is not a string", v)
		}
		got = append(got, s)
	}
	if !sameStrings(got, want) {
		t.Errorf("result.schema.json review_scope.why_not = %v, internal/runstore's ReviewScope* constants = %v", got, want)
	}
}

// reviewScopeSpec reports whether a const spec in runstore.go declares a
// ReviewScope* reason. The constants are untyped, so their names are the only
// handle on them.
func reviewScopeSpec(vs *ast.ValueSpec) bool {
	for _, name := range vs.Names {
		if strings.HasPrefix(name.Name, "ReviewScope") {
			return true
		}
	}
	return false
}

// haltReasonSpec reports whether a const spec in runstore.go declares a
// HaltReason.
func haltReasonSpec(vs *ast.ValueSpec) bool {
	id, ok := vs.Type.(*ast.Ident)
	return ok && id.Name == "HaltReason"
}

// runstoreConstValues returns the string value of every constant in
// runstore.go whose spec keep accepts, in source order. Both enum pins above
// read their Go side through it: one parse and one walk, so the two cannot
// drift apart, and a constant that is not a string literal fails either the
// same way.
func runstoreConstValues(t *testing.T, keep func(*ast.ValueSpec) bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../internal/runstore/runstore.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || !keep(vs) {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					t.Fatalf("%s has no value in its spec: this test cannot read it", name.Name)
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s's value is not a string literal: %v", name.Name, vs.Values[i])
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, s)
			}
		}
	}
	return out
}

func TestPolicyRecordSchema(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	check := func(p *runstore.PolicyRecord) error {
		t.Helper()
		rec := runstore.Record{Version: 1, RunID: "20261001-100000-abcd", Status: runstore.StatusRunning, Stage: "bootstrap",
			Outcome: runstore.OutcomeNone, StartedAt: at, Policy: p}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	full := &runstore.PolicyRecord{
		Effective: runstore.PolicyEffective{PerRunUSD: 5, Mode: "enforce", MaxRunTokens: 1000,
			MaxOutputTokens: &runstore.PolicyOutput{Coder: 4096}, AllowedModels: []string{"claude-sonnet-5-5"}},
		Sources: map[string]string{"per_run_usd": "ceiling", "mode": "default-branch", "max_run_tokens": "branch"},
		Ignored: []runstore.PolicyIgnored{{Key: "per_run_usd", Value: "500", Effective: "5", Source: "ceiling", From: "branch"}},
	}
	if err := check(full); err != nil {
		t.Errorf("schema rejects a policy record: %v", err)
	}
	if err := check(nil); err != nil {
		t.Errorf("schema rejects a record without policy: %v", err)
	}
	// An empty allow-list (forbid everything) is valid and is written.
	// PR #379's own layer: a key the project's published node set is written
	// with the source "policy" (internal/policy.SourcePolicy, set by the
	// runner's policySources), including the source of an ignored value the
	// node's ceiling replaced. A run of a project-centric installation
	// writes this, and internal/runner's TestHaltedRecordSchema validates a
	// stored record against this schema, so the enum has to have it.
	policied := &runstore.PolicyRecord{
		Effective: runstore.PolicyEffective{Mode: "enforce", PerRunUSD: 5},
		Sources:   map[string]string{"mode": policy.SourcePolicy, "per_run_usd": policy.SourcePolicy, "max_run_tokens": policy.SourceCeiling},
		Ignored: []runstore.PolicyIgnored{{Key: "per_run_usd", Value: "500", Effective: "5",
			Source: policy.SourcePolicy, From: policy.SourceBranch}},
	}
	if err := check(policied); err != nil {
		t.Errorf("schema rejects a record whose ceiling came from the project's policy: %v", err)
	}
	// A stage timeout the run's own timeouts.stage bounds at least as tightly
	// is one of policy.ignored too, and Clamped is what marks it: its key,
	// source and from can read exactly like a value a tighter layer capped
	// (T7.6, runner.noteStageTimeoutClamped). The ignored item admits no other
	// key, so a stored clamp entry needs the property here.
	clamped := &runstore.PolicyRecord{
		Effective: runstore.PolicyEffective{Mode: "enforce", PerRunUSD: 5},
		Sources:   map[string]string{"governance.stage_timeout.implement": policy.SourceCeiling},
		Ignored: []runstore.PolicyIgnored{{Key: "governance.stage_timeout.implement", Value: "1h", Effective: "40m",
			Source: policy.SourceCeiling, From: policy.SourceBranch, Clamped: true}},
	}
	if err := check(clamped); err != nil {
		t.Errorf("schema rejects a clamped stage timeout: %v", err)
	}
	empty := &runstore.PolicyRecord{Effective: runstore.PolicyEffective{AllowedModels: []string{}}}
	if err := check(empty); err != nil {
		t.Errorf("schema rejects an empty allow-list: %v", err)
	}
	if data, _ := json.Marshal(empty); !strings.Contains(string(data), `"allowed_models":[]`) {
		t.Errorf("an empty allow-list is not written: %s", data)
	}
	for name, mut := range map[string]func(p *runstore.PolicyRecord){
		"bad mode":     func(p *runstore.PolicyRecord) { p.Effective.Mode = "loose" },
		"bad source":   func(p *runstore.PolicyRecord) { p.Sources["mode"] = "repo" },
		"bad from":     func(p *runstore.PolicyRecord) { p.Ignored[0].From = "repo" },
		"negative cap": func(p *runstore.PolicyRecord) { p.Effective.PerRunUSD = -1 },
		"empty model":  func(p *runstore.PolicyRecord) { p.Effective.AllowedModels = []string{""} },
	} {
		p := *full
		p.Sources = maps.Clone(full.Sources)
		p.Ignored = slices.Clone(full.Ignored)
		mut(&p)
		if err := check(&p); err == nil {
			t.Errorf("schema accepts a policy record with %s", name)
		}
	}
	doc := `{"version":1,"run_id":"20261001-100000-abcd","status":"running","stage":"bootstrap","outcome":"none","cost_usd":0,"started_at":"2026-10-01T10:00:00Z","policy":{"effective":{},"extra":1}}`
	inst, _ := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err := sch.Validate(inst); err == nil {
		t.Error("schema accepts an unknown key in policy")
	}
}

// The schema's layer names and internal/policy's are one list. The runner
// writes whichever the merge produced — "policy" for a key the project's
// published node named (PR #379), the rest for the ceiling, the default
// branch and the run's branch — so a Go constant the schema does not carry is
// a stored result.json the repository's own validator rejects. `from` stays
// without "policy" on purpose: the node is not a later layer that can ask for
// a looser value, it *is* the first layer, and its keys are replaced rather
// than loosened (plan task 1.3, H1). It carries "ceiling" for the one case a
// ceiling value is dropped — a stage timeout the run's own timeouts.stage
// bounds at least as tightly, which the runner records under the key the
// project layer's governance wrote (T7.6, runner.noteStageTimeoutClamped) —
// and the node carries no governance block, so "policy" is never that layer.
func TestPolicyRecordSchemaLayersAgreeWithGo(t *testing.T) {
	data, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	at := func(v any, path ...string) any {
		for _, k := range path {
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("schema has no %v", path)
			}
			v = m[k]
		}
		return v
	}
	enum := func(v any) []string {
		var out []string
		for _, x := range v.([]any) {
			out = append(out, x.(string))
		}
		slices.Sort(out)
		return out
	}
	pol := at(doc, "properties", "policy", "properties")
	for _, where := range []struct {
		name string
		got  []string
		want []string
	}{
		{"policy.sources", enum(at(pol, "sources", "additionalProperties", "enum")),
			slices.Sorted(slices.Values([]string{policy.SourceCeiling, policy.SourceDefaultBranch, policy.SourceBranch, policy.SourcePolicy}))},
		{"policy.ignored[].source", enum(at(pol, "ignored", "items", "properties", "source", "enum")),
			slices.Sorted(slices.Values([]string{policy.SourceCeiling, policy.SourceDefaultBranch, policy.SourceBranch, policy.SourcePolicy}))},
		{"policy.ignored[].from", enum(at(pol, "ignored", "items", "properties", "from", "enum")),
			slices.Sorted(slices.Values([]string{policy.SourceCeiling, policy.SourceDefaultBranch, policy.SourceBranch}))},
	} {
		if !slices.Equal(where.got, where.want) {
			t.Errorf("schema's %s = %v, internal/policy's layers are %v", where.name, where.got, where.want)
		}
	}
}

func TestBudgetRecordSchema(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	check := func(mut func(*runstore.Record)) error {
		t.Helper()
		rec := runstore.Record{Version: 1, RunID: "20261002-100000-abcd", Status: runstore.StatusRunning, Stage: "bootstrap",
			Outcome: runstore.OutcomeNone, StartedAt: at}
		mut(&rec)
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	good := runstore.BudgetRecord{Day: 20729, GrantedMicros: 2_000_000, ReleasedMicros: 750_000, Mode: "enforce", Backend: "rtdb"}
	if err := check(func(r *runstore.Record) { r.Budget = &good }); err != nil {
		t.Errorf("schema rejects a budget record: %v", err)
	}
	if err := check(func(r *runstore.Record) {}); err != nil {
		t.Errorf("schema rejects a record without a budget: %v", err)
	}
	// The committed day cap is part of the effective policy.
	if err := check(func(r *runstore.Record) {
		r.Policy = &runstore.PolicyRecord{Effective: runstore.PolicyEffective{PerRunUSD: 5, PerDayUSD: 20, Mode: "enforce"}}
	}); err != nil {
		t.Errorf("schema rejects per_day_usd in the effective policy: %v", err)
	}
	for name, mut := range map[string]func(*runstore.BudgetRecord){
		"bad mode":      func(b *runstore.BudgetRecord) { b.Mode = "loose" },
		"bad backend":   func(b *runstore.BudgetRecord) { b.Backend = "sql" },
		"negative":      func(b *runstore.BudgetRecord) { b.GrantedMicros = -1 },
		"negative rel.": func(b *runstore.BudgetRecord) { b.ReleasedMicros = -1 },
	} {
		b := good
		mut(&b)
		if err := check(func(r *runstore.Record) { r.Budget = &b }); err == nil {
			t.Errorf("schema accepts a budget record with %s", name)
		}
	}
	doc := `{"version":1,"run_id":"20261002-100000-abcd","status":"running","stage":"bootstrap","outcome":"none","cost_usd":0,"started_at":"2026-10-02T10:00:00Z","budget":{"day":1,"granted_micros":0,"released_micros":0,"mode":"observe","backend":"rtdb","extra":1}}`
	inst, _ := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err := sch.Validate(inst); err == nil {
		t.Error("schema accepts an unknown key in budget")
	}
}

func TestSchemaAcceptsPRDesc(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{
		Version: 1, RunID: "20261003-100000-abcd", Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Stage: "review", Branch: "fugaro/x", StartedAt: at,
		PR:            &runstore.PRRef{Number: 7, URL: "https://example.com/pr/7", Desc: "0123abcd", StatusAt: &at},
		DraftFallback: true,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects a record with pr.desc, pr.status_at, draft_fallback: %v\n%s", err, data)
	}
	bad, _ := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Replace(data, []byte(`"status_at":"2026`), []byte(`"status_at_x":"2026`), 1)))
	if err := sch.Validate(bad); err == nil {
		t.Fatal("schema accepts an unknown pr key")
	}
}

func TestSchemaAcceptsReviewFirstStage(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	data, err := json.Marshal(runstore.Record{
		Version: 1, RunID: "20261003-100000-abcd", Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Stage: "review_first", Branch: "fugaro/x", StartedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema rejects stage review_first: %v\n%s", err, data)
	}
}

// The repository's own fugaro.yaml (Fugaro dogfooding itself) is on the one
// Fugaro base since 0.7.0 (Task 22 Step 4 removed its base: go): the schema
// and the Go parser must both accept it with no problem, so a schema or
// parser change that breaks the file Fugaro runs on itself is caught here.
func TestRepositoryFugaroYAML(t *testing.T) {
	data, err := os.ReadFile("../fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := compile(t, "fugaro.schema.json").Validate(yamlInstance(t, data)); err != nil {
		t.Errorf("schema rejects fugaro.yaml: %v", err)
	}
	if _, problems := config.Parse(data); len(problems) != 0 {
		t.Errorf("fugaro.yaml: problems = %v, want none", problems)
	}
}

// TestRecipeSchemaCorpus: the schema and the Go parser agree on the corpus
// and the catalog. Files named shape-*.yaml break YAML rules the JSON model
// cannot see (anchors, repeated keys); only the Go parser judges them.
func TestRecipeSchemaCorpus(t *testing.T) {
	sch := compile(t, "recipe.schema.json")
	for _, f := range globAll(t, "../testdata/recipe/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid recipe: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/recipe/invalid/*.yaml") {
		if strings.HasPrefix(filepath.Base(f), "shape-") {
			continue
		}
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err == nil {
			t.Errorf("%s: schema accepts an invalid recipe", f)
		}
	}
	for _, name := range recipe.CatalogNames() {
		text, _ := recipe.CatalogText(name)
		if err := sch.Validate(yamlInstance(t, text)); err != nil {
			t.Errorf("catalog %s: %v", name, err)
		}
	}
}
func TestResultSchemaRecipe(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		rr *runstore.RecipeRecord
		ok bool
	}{
		{&runstore.RecipeRecord{Name: "claude-solo", Source: "catalog", SHA256: strings.Repeat("a", 64)}, true},
		{&runstore.RecipeRecord{Name: "claude-solo", Source: "bucket", SHA256: strings.Repeat("a", 64)}, false},
		{&runstore.RecipeRecord{Name: "claude-solo", Source: "repo", SHA256: "short"}, false},
	} {
		data, err := json.Marshal(runstore.Record{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app",
			Status: runstore.StatusRunning, Stage: "review", StartedAt: at, Recipe: tc.rr})
		if err != nil {
			t.Fatal(err)
		}
		inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err := sch.Validate(inst); (err == nil) != tc.ok {
			t.Errorf("%+v: err = %v, want ok %v", tc.rr, err, tc.ok)
		}
	}
}

func TestResultSchemaProjectLayer(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		pl  *runstore.ProjectLayerRecord
		sum string
		ok  bool
	}{
		{&runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}, strings.Repeat("b", 64), true},
		{nil, strings.Repeat("b", 64), true},
		{&runstore.ProjectLayerRecord{SHA256: "short", Applied: true}, "", false},
		{nil, "short", false},
	} {
		data, err := json.Marshal(runstore.Record{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/app",
			Status: runstore.StatusRunning, Stage: "implement", StartedAt: at, ProjectLayer: tc.pl, ConfigSHA256: tc.sum})
		if err != nil {
			t.Fatal(err)
		}
		inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err := sch.Validate(inst); (err == nil) != tc.ok {
			t.Errorf("%+v %q: err = %v, want ok %v", tc.pl, tc.sum, err, tc.ok)
		}
	}
}

func TestFugaroSchemaProfileKeys(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	for text, valid := range map[string]bool{
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\n":                                               true,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofile: node-web\n":                            true,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nworkflows:\n  api: { profile: java-service }\n": true,
		"version: 1\nproject: acme\n": false,
		"version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  api: { profile: java-service }\n":  true,
		"version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  api: { commands: { build: a } }\n": false,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofile: Bad_Name\n":                              false,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nworkflows:\n  api: { profile: Bad_Name }\n":       false,
		// The job profile's selection: the same name rule, on its own key —
		// profile is the project layer's template and job_profile the policy's
		// job, so a file may carry both and neither may be a bad name.
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nworkflows:\n  api: { job_profile: standard-bitbucket, commands: { build: make, test: make } }\n":     true,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nworkflows:\n  api: { profile: node-web, job_profile: big, commands: { build: make, test: make } }\n": true,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nworkflows:\n  api: { job_profile: Bad_Name, commands: { build: make, test: make } }\n":               false,
	} {
		if err := sch.Validate(yamlInstance(t, []byte(text))); (err == nil) != valid {
			t.Errorf("%q: valid = %v, want %v (%v)", text, err == nil, valid, err)
		}
	}
}

func TestProjectLayerSchemaCorpus(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	for _, f := range globAll(t, "../testdata/project-layer/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid project layer: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/project-layer/invalid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err == nil {
			t.Errorf("%s: schema accepts an invalid project layer", f)
		}
	}
}

// TestProjectLayerSchemaCommandsLintFix checks the profile schema's
// commands.lint and commands.fix the same way TestSchemaBudgetBlock checks
// budget: inline snippets, not the shared testdata/project-layer corpus.
// They can't live in that corpus: TestProjectLayerCorpus (layer_test.go)
// requires every file there to agree with config.ParseProjectLayer too, and
// the Go parser deliberately leaves both rules to the merged config's
// Validate (validateProfile's own comment: "what [a profile] leaves out the
// repository may set, and Validate checks the merged workflow") — a profile
// may ship commands.fix alone and let a repository supply commands.lint by
// its own key, which the key-by-key merge (docs/design/layered-config.md
// §4) allows. The schema is intentionally stricter here, to catch the
// likely mistake at authoring time; that's a false positive in an editor at
// worst, never a false negative, so it doesn't need Go to agree.
func TestProjectLayerSchemaCommandsLintFix(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	const base = "version: 1\nproject: aurora\ngcp_project: proj-1234\nprofiles:\n  p:\n    commands: "
	for _, c := range []struct {
		commands string
		ok       bool
	}{
		{"{ lint: make lint, fix: make fmt }", true},
		{"{ lint: make lint }", true},
		{"{ fix: make fmt }", false}, // dependentRequired: fix needs lint
		{"{ lint: '' }", false},      // minLength: 1
		{"{ lint: make lint, fix: '' }", false},
	} {
		err := sch.Validate(yamlInstance(t, []byte(base+c.commands+"\n")))
		if (err == nil) != c.ok {
			t.Errorf("commands: %s: schema err = %v, want ok=%v", c.commands, err, c.ok)
		}
	}
}

// TestProjectLayerSchemaArtifactKeys keeps the profile schema's 0.8.0 keys
// (commands.render, commands.probe, timeouts.render, inputs) in step with the
// Go parser the way TestProjectLayerSchemaClaudeModels keeps claude_models:
// each snippet must be accepted or refused by both. Without the properties a
// profile carrying any of them was refused by the schema an editor validates
// against while config.ParseProjectLayer accepted it (additionalProperties:
// false), which is the gap that made the docs example carrying claude_models
// fail before that property existed. The bounds are deliberately narrower
// here too: the schema's minimum 0 / maximum ceilings are the ones
// config.validateInputs applies (config.MaxInputFiles and friends), pinned
// against the Go constants by TestInputCapCeilingsAgreeWithGo.
func TestProjectLayerSchemaArtifactKeys(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	head := "version: 1\nproject: aurora\ngcp_project: proj-1234\nprofiles:\n  p:\n"
	anchor := config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	for _, entry := range []string{
		"    commands: { render: make shots, probe: make check }\n",
		"    commands: { render: make shots }\n",
		"    commands: { shots: make shots }\n",
		"    timeouts: { render: 30m }\n",
		"    timeouts: { render: soon }\n",
		"    inputs: { max_files: 4, max_bytes: 1048576, max_total_bytes: 4194304 }\n",
		"    inputs: {}\n",
		"    inputs: { max_files: 65 }\n",
		"    inputs: { max_bytes: -1 }\n",
		"    inputs: { max_total_bytes: 4294967297 }\n",
		"    inputs: { globs: ['out/*.png'] }\n",
	} {
		text := head + entry
		schemaOK := sch.Validate(yamlInstance(t, []byte(text))) == nil
		_, ps := config.ParseProjectLayer([]byte(text), anchor)
		if schemaOK != (len(ps) == 0) {
			t.Errorf("%s: schema ok = %v, config.ParseProjectLayer problems = %v", entry, schemaOK, ps)
		}
	}
}

// TestInputCapCeilingsAgreeWithGo ties both schemas' inputs bounds to the Go
// ceilings they mirror (config.MaxInputFiles, MaxInputBytes and
// MaxInputTotalBytes, applied by validateInputs): the numbers are separate
// copies in each schema, so a raised ceiling in Go would otherwise leave the
// schema silently stricter — an editor refusing a cap fugaro accepts — with
// no test to notice.
func TestInputCapCeilingsAgreeWithGo(t *testing.T) {
	for _, tc := range []struct{ file, root string }{
		{"fugaro.schema.json", "workflow"},
		{"project-layer.schema.json", "profile"},
	} {
		data, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		at := func(v any, path ...string) any {
			for _, k := range path {
				m, ok := v.(map[string]any)
				if !ok {
					t.Fatalf("%s: no %v", tc.file, path)
				}
				v = m[k]
			}
			return v
		}
		in := at(doc, "$defs", tc.root, "properties", "inputs", "properties")
		for _, c := range []struct {
			name string
			want int64
		}{
			{"max_files", config.MaxInputFiles},
			{"max_bytes", config.MaxInputBytes},
			{"max_total_bytes", config.MaxInputTotalBytes},
		} {
			p, ok := in.(map[string]any)[c.name].(map[string]any)
			if !ok {
				t.Fatalf("%s: inputs.%s is not an object", tc.file, c.name)
			}
			if got, ok := p["maximum"].(float64); !ok || int64(got) != c.want {
				t.Errorf("%s: inputs.%s maximum = %v, config has %d", tc.file, c.name, p["maximum"], c.want)
			}
			if got, ok := p["minimum"].(float64); !ok || got != 0 {
				t.Errorf("%s: inputs.%s minimum = %v, want 0", tc.file, c.name, p["minimum"])
			}
		}
	}
}

// TestRecipeSchemaArtifactStepsMatchGo pins the schema's step shapes for the
// artifact steps against recipe.Parse for every mode: a plan or render step
// is mode: artifact's own, so [render] under mode: implement (or with no mode
// at all, which is implement) is refused by both. The steps oneOf has to
// list artifactSteps for an artifact recipe to have a shape at all, and the
// schema's own branch is what stops that alternative admitting the step in
// the other two modes; testdata/recipe/invalid/render-implement.yaml pins the
// same thing through the corpus instead, so it can never be relaxed to a
// shape- fixture the schema test skips.
func TestRecipeSchemaArtifactStepsMatchGo(t *testing.T) {
	sch := compile(t, "recipe.schema.json")
	const outs = "outputs:\n  globs: [\"out/*.png\"]\n"
	for _, tc := range []struct{ name, text string }{
		{"implement names render", "version: 1\nname: a\nmode: implement\nsteps:\n  - render: {}\n"},
		{"no mode names render", "version: 1\nname: a\nsteps:\n  - render: {}\n"},
		{"no mode names plan then render", "version: 1\nname: a\nsteps:\n  - plan: {}\n  - render: {}\n"},
		{"review names render", "version: 1\nname: a\nmode: review\nsteps:\n  - render: {}\n"},
		{"artifact renders", "version: 1\nname: a\nmode: artifact\n" + outs + "steps:\n  - render: {}\n"},
		{"artifact plans then renders", "version: 1\nname: a\nmode: artifact\n" + outs + "steps:\n  - plan: {}\n  - render: {}\n"},
		{"artifact renders twice", "version: 1\nname: a\nmode: artifact\n" + outs + "steps:\n  - render: {}\n  - render: {}\n"},
		{"artifact without outputs", "version: 1\nname: a\nmode: artifact\nsteps:\n  - render: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schemaOK := sch.Validate(yamlInstance(t, []byte(tc.text))) == nil
			_, ps := recipe.Parse([]byte(tc.text))
			if schemaOK != (len(ps) == 0) {
				t.Errorf("schema ok = %v, recipe.Parse problems = %v", schemaOK, ps)
			}
		})
	}
}

// TestRecipeSchemaDigestStepsMatchGo pins the schema's step shape for the
// digest step against recipe.Parse for every mode: the shape is exactly
// [digest, review] in a mode: implement recipe (the catalog's run-digest),
// and the two modes that already pin their own steps — mode: review to one
// review, mode: artifact to a render — are what refuse it elsewhere, so the
// steps oneOf's digest alternative needs no branch of its own.
func TestRecipeSchemaDigestStepsMatchGo(t *testing.T) {
	sch := compile(t, "recipe.schema.json")
	const outs = "outputs:\n  globs: [\"out/*.png\"]\n"
	for _, tc := range []struct{ name, text string }{
		{"no mode digests then reviews", "version: 1\nname: a\nsteps:\n  - digest: {}\n  - review: {}\n"},
		{"implement digests then reviews", "version: 1\nname: a\nmode: implement\nsteps:\n  - digest: {}\n  - review: {}\n"},
		{"review digests then reviews", "version: 1\nname: a\nmode: review\nsteps:\n  - digest: {}\n  - review: {}\n"},
		{"artifact digests then reviews", "version: 1\nname: a\nmode: artifact\n" + outs + "steps:\n  - digest: {}\n  - review: {}\n"},
		{"digest alone", "version: 1\nname: a\nsteps:\n  - digest: {}\n"},
		{"digest then first line then review", "version: 1\nname: a\nsteps:\n  - digest: {}\n  - first_line: {}\n  - review: {}\n"},
		{"check then digest then review", "version: 1\nname: a\nsteps:\n  - check: { command: test }\n  - digest: {}\n  - review: {}\n"},
		{"digest twice then review", "version: 1\nname: a\nsteps:\n  - digest: {}\n  - digest: {}\n  - review: {}\n"},
		{"digest with options", "version: 1\nname: a\nsteps:\n  - digest: { max_rounds: 2 }\n  - review: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schemaOK := sch.Validate(yamlInstance(t, []byte(tc.text))) == nil
			_, ps := recipe.Parse([]byte(tc.text))
			if schemaOK != (len(ps) == 0) {
				t.Errorf("schema ok = %v, recipe.Parse problems = %v", schemaOK, ps)
			}
		})
	}
}

func TestFugaroSchemaLayeredCorpus(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	for _, f := range globAll(t, "../testdata/config/layered/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid layered config: %v", f, err)
		}
	}
}

// TestTaskSchemaPlanTaskMatchesTheGoRule ties the task schema's `plan_task`
// property to the Go rule it mirrors (task.PlanTaskProblem's own list: at
// least two "/"-separated elements, each matching task.PlanTaskElemRE, at
// most task.MaxPlanTaskBytes bytes). The schema is a second copy of that
// shape, and without this a change to the Go rule would drift from it with
// only the corpus fixtures noticing: a property that is too wide accepts a
// task.json the launcher would refuse to write, and one that is too narrow
// refuses a task.json it does write, which is the defect that made this
// property necessary -- task.schema.json is additionalProperties: false, so
// a launch with --plan-task wrote a document this schema rejected.
//
// Mutation (run, restored): widen the schema's pattern to `^[^/]+/[^/]+$`,
// or set maxLength to 256, and this fails naming the side that drifted.
func TestTaskSchemaPlanTaskMatchesTheGoRule(t *testing.T) {
	data, err := os.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			Pattern   string `json:"pattern"`
			MaxLength int    `json:"maxLength"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	p, ok := doc.Properties["plan_task"]
	if !ok {
		t.Fatal("task.schema.json has no plan_task property: a launch with --plan-task writes a task.json this schema refuses (additionalProperties: false)")
	}
	// One element, as task.PlanTaskElemRE writes it (its anchors are the
	// whole pattern's here, so they come off), then the whole id: at least
	// two of them.
	elem := strings.TrimSuffix(strings.TrimPrefix(task.PlanTaskElemRE.String(), "^"), "$")
	if want := "^" + elem + "(/" + elem + ")+$"; p.Pattern != want {
		t.Errorf("schema pattern %q != task.PlanTaskProblem's own shape %q (task.PlanTaskElemRE)", p.Pattern, want)
	}
	if p.MaxLength != task.MaxPlanTaskBytes {
		t.Errorf("schema maxLength %d != task.MaxPlanTaskBytes %d", p.MaxLength, task.MaxPlanTaskBytes)
	}
}

// TestTaskSchemaOAuthTokenMatchesTheGoRules ties the task schema's
// `oauth_token` property to the Go rules it mirrors (config.ClaudeAccountNameRE
// and config.MaxClaudeAccountNameLen): the schema pattern and maxLength are
// separate copies, so without this a change to the Go rule would drift from the
// schema with only the corpus fixtures noticing. The pattern alone admits 61
// characters (it counts repetition units of one or two), which is why the
// bound is a separate maxLength.
func TestTaskSchemaOAuthTokenMatchesTheGoRules(t *testing.T) {
	data, err := os.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			Pattern   string `json:"pattern"`
			MaxLength int    `json:"maxLength"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	p, ok := doc.Properties["oauth_token"]
	if !ok {
		t.Fatal("task.schema.json has no oauth_token property")
	}
	if p.Pattern != config.ClaudeAccountNameRE.String() {
		t.Errorf("schema pattern %q != config.ClaudeAccountNameRE %q", p.Pattern, config.ClaudeAccountNameRE.String())
	}
	if p.MaxLength != config.MaxClaudeAccountNameLen {
		t.Errorf("schema maxLength %d != config.MaxClaudeAccountNameLen %d", p.MaxLength, config.MaxClaudeAccountNameLen)
	}
}

// TestTaskSchemaCompressionOffMatchesTheGoField ties task.schema.json's
// `compression_off` property to the Go field it mirrors (task.Spec.
// CompressionOff, fugaro run --no-compression). The schema is
// additionalProperties: false, so a property missing here, or named
// differently from the Go field's own json tag, makes every task such a launch
// writes schema-invalid — and only the schema's consumers would notice: the
// corpus fixtures would not unless one carries the field (testdata/task/valid/
// compression-off.json now does), and no Go test reads this document. That is
// the defect that made the plan_task and oauth_token properties necessary, one
// field at a time.
//
// The document below is marshalled from task.Spec rather than written out, so
// it reads the Go field's own tag: renaming either side's spelling stops the
// two agreeing, which is the drift this catches.
//
// Mutation (rename the property to `compressionOff` in schemas/task.schema.json):
// the document carrying the field is rejected as an unknown key and this fails.
func TestTaskSchemaCompressionOffMatchesTheGoField(t *testing.T) {
	sch := compile(t, "task.schema.json")
	validate := func(s task.Spec) error {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	spec := task.Spec{Version: 1, RunID: "20261027-093000-abcd", Repo: "acme/app", Ref: "main", Task: "x"}
	if err := validate(spec); err != nil {
		t.Fatalf("schema rejects a task carrying no compression_off: %v", err)
	}
	spec.CompressionOff = true
	if err := validate(spec); err != nil {
		t.Fatalf("schema rejects a task carrying compression_off, which fugaro run --no-compression writes: %v", err)
	}
}

// TestFugaroSchemaOAuthTokenMatchesTheGoRules is TestTaskSchemaOAuthToken
// MatchesTheGoRules's sibling for fugaro.schema.json's agent.oauth_token
// (named oauth tokens plan Task 6): the two schema copies, and the Go rules
// both mirror, must never drift apart.
func TestFugaroSchemaOAuthTokenMatchesTheGoRules(t *testing.T) {
	data, err := os.ReadFile("fugaro.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Agent struct {
				Properties map[string]struct {
					Pattern   string `json:"pattern"`
					MaxLength int    `json:"maxLength"`
				} `json:"properties"`
			} `json:"agent"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	p, ok := doc.Properties.Agent.Properties["oauth_token"]
	if !ok {
		t.Fatal("fugaro.schema.json's agent has no oauth_token property")
	}
	if p.Pattern != config.ClaudeAccountNameRE.String() {
		t.Errorf("schema pattern %q != config.ClaudeAccountNameRE %q", p.Pattern, config.ClaudeAccountNameRE.String())
	}
	if p.MaxLength != config.MaxClaudeAccountNameLen {
		t.Errorf("schema maxLength %d != config.MaxClaudeAccountNameLen %d", p.MaxLength, config.MaxClaudeAccountNameLen)
	}
}

// TestFugaroSchemaAgentCompression is TestFugaroSchemaAgentOAuthToken's shape
// for agent.compression while the key is reserved (headroom-compression plan
// P11): the enum admits "" and nothing else, so the schema refuses the real
// value for the same reason config.Validate does, and the two move together
// when the lift admits "headroom".
func TestFugaroSchemaAgentCompression(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for text, valid := range map[string]bool{
		head + "agent: { compression: \"\" }\n":     true,
		head + "agent: { auth: api-key }\n":         true,
		head + "agent: { compression: headroom }\n": false,
		head + "agent: { compression: \"off\" }\n":  false,
	} {
		if err := sch.Validate(yamlInstance(t, []byte(text))); (err == nil) != valid {
			t.Errorf("%q: valid = %v, want %v (%v)", text, err == nil, valid, err)
		}
	}
}

// TestFugaroSchemaAgentOAuthToken is TestFugaroSchemaProfileKeys's shape for
// agent.oauth_token: "default" is reserved (the empty value already means
// it), and the schema's maxLength must refuse what the pattern alone would
// still accept (schemas_test.go's own TestTaskSchemaOAuthTokenMatchesTheGoRules
// doc comment explains why the pattern alone is not enough).
func TestFugaroSchemaAgentOAuthToken(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	dashHeavy := "a" + strings.Repeat("-a", 30) // 61 chars, matches the pattern alone
	for text, valid := range map[string]bool{
		head + "agent: { oauth_token: acme-personal }\n":     true,
		head + "agent: { oauth_token: default }\n":           false,
		head + "agent: { oauth_token: Acme }\n":              false,
		head + "agent: { oauth_token: " + dashHeavy + " }\n": false,
	} {
		if err := sch.Validate(yamlInstance(t, []byte(text))); (err == nil) != valid {
			t.Errorf("%q: valid = %v, want %v (%v)", text, err == nil, valid, err)
		}
	}
}

// TestResultSchemaAcceptsCacheFields (P12): the run's prompt-cache read and
// write token totals are part of the cost object. Pinned separately from
// TestResultSchemaAcceptsRunnerRecords so a later edit that tightens cost's
// additionalProperties fails here, naming the field, rather than only there.
func TestResultSchemaAcceptsCacheFields(t *testing.T) {
	sch := compile(t, "result.schema.json")
	head := `{"version":1,"run_id":"20261027-100000-abcd","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1.23,"started_at":"2026-10-27T10:00:00Z","cost":{"model_usd":1.23,"compute_usd":0,"compute_estimated":false,"total_usd":1.23,"estimate":true,"model_basis":"api-list"`
	validate := func(lit string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(head + lit + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	for _, lit := range []string{``, `,"cache_read_tokens":400`, `,"cache_write_tokens":50`, `,"cache_read_tokens":400,"cache_write_tokens":50`} {
		if err := validate(lit); err != nil {
			t.Errorf("schema rejects cost%s: %v", lit, err)
		}
	}
	for _, lit := range []string{`,"cache_read_tokens":-1`, `,"cache_write_tokens":"50"`} {
		if err := validate(lit); err == nil {
			t.Errorf("schema accepts cost%s", lit)
		}
	}
}

// TestResultSchemaAcceptsServiceTiers: the run's cost object carries the
// service tiers its calls came back at that the price table has no
// multiplier for (design §6.1). Those calls were charged conservatively, so
// the names are what explains the figure the record holds -- on a provider
// route nothing else does. Pinned here for the same reason the cache totals
// are: cost's additionalProperties is false, so a field the record writes
// and the schema does not name is a record the schema rejects.
func TestResultSchemaAcceptsServiceTiers(t *testing.T) {
	sch := compile(t, "result.schema.json")
	head := `{"version":1,"run_id":"20261027-100000-abcd","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1.23,"started_at":"2026-10-27T10:00:00Z","cost":{"model_usd":1.23,"compute_usd":0,"compute_estimated":false,"total_usd":1.23,"estimate":true,"model_basis":"api-list"`
	validate := func(lit string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(head + lit + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	for _, lit := range []string{``, `,"service_tiers":["usage.service_tier \"priority\""]`, `,"service_tiers":[]`,
		// The record bounds the names it carries: past eight distinct
		// tiers the last entry counts the rest instead of naming them,
		// and that entry is an ordinary non-empty string to the schema.
		`,"service_tiers":["usage.service_tier \"a\"","+12 more"]`} {
		if err := validate(lit); err != nil {
			t.Errorf("schema rejects cost%s: %v", lit, err)
		}
	}
	for _, lit := range []string{`,"service_tiers":"priority"`, `,"service_tiers":[7]`, `,"service_tiers":[""]`} {
		if err := validate(lit); err == nil {
			t.Errorf("schema accepts cost%s", lit)
		}
	}
}

// TestTaskSchemaBaseBranchMatchesTheGoRule ties the task schema's
// `base_branch` property to the Go rule it mirrors (config.ValidBranchName,
// the one validator fugaro.yaml's git.base_branch, the installation
// config's base_branch and the policy node's are all held to). The schema
// is a second copy of that shape, and task.schema.json is
// additionalProperties: false, so without this property a launch that
// carries a project default base branch writes a task.json this schema
// rejects -- and without the test, the two copies drift silently.
func TestTaskSchemaBaseBranchMatchesTheGoRule(t *testing.T) {
	sch := compile(t, "task.schema.json")
	doc := func(b string) any {
		return yamlInstance(t, []byte(`{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme/app","ref":"main","task":"x","base_branch":`+strconv.Quote(b)+`}`))
	}
	for _, b := range []string{"main", "master", "release/1.2", "feature/JIRA-123_x.y", "_x", "develop"} {
		if !config.ValidBranchName(b) {
			t.Fatalf("fixture %q is not a valid branch name", b)
		}
		if err := sch.Validate(doc(b)); err != nil {
			t.Errorf("the schema rejects base_branch %q, which config.ValidBranchName accepts: %v", b, err)
		}
	}
	for _, b := range []string{"--upload-pack=x", "a..b", "$(id)", "x y", "/x", ".hidden", "main.lock", "x/", "x.", "a//b"} {
		if config.ValidBranchName(b) {
			t.Fatalf("fixture %q is a valid branch name", b)
		}
		if err := sch.Validate(doc(b)); err == nil {
			t.Errorf("the schema accepts base_branch %q, which config.ValidBranchName refuses", b)
		}
	}
}

// TestFugaroSchemaGovernance: the governance block's shape, inline snippets
// like TestSchemaBudgetBlock. Every bound is config.validateGovernance's too:
// a stage budget is a fraction of the per-run cap and never the cap itself
// (G6), and 0 (or null) is "unset" in both, so a file may say "no value" the
// way it says it everywhere else.
func TestFugaroSchemaGovernance(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	base := "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  s: { commands: { build: make, test: make } }\n"
	for _, c := range []struct {
		gov string
		ok  bool
	}{
		{"governance: { review_ratio: 3, review_floor_calls: 10, max_subagents: 2 }", true},
		{"governance: { stage_budget: { implement: 0.6, review: 0.4, fix: 0.25 } }", true},
		{"governance: { stage_tokens: { implement: 1000000, review: 500000, fix: 250000 } }", true},
		{"governance: { stage_timeout: { implement: 30m, review: 1h30m, fix: 10m } }", true},
		{"governance:", true},
		{"governance: {}", true},
		{"governance: { review_ratio: }", true},
		{"governance: { review_ratio: 0 }", true},
		{"governance: { max_subagents: 0 }", true},
		{"governance: { stage_budget: { implement: 0 } }", true},
		{"governance: { review_ratio: -1 }", false},
		{"governance: { review_ratio: 2.5 }", false},
		{"governance: { review_ratios: 3 }", false},
		{"governance: { stage_budget: { implement: 1 } }", false},
		{"governance: { stage_budget: { implement: 1.5 } }", false},
		{"governance: { stage_budget: { review: -0.5 } }", false},
		{"governance: { stage_budget: { verify: 0.5 } }", false},
		{"governance: { stage_tokens: { plan: 1000 } }", false},
		{"governance: { stage_tokens: { review: -1 } }", false},
		{"governance: { stage_timeout: { render: 5m } }", false},
		{"governance: { stage_timeout: { fix: 5x } }", false},
		{"governance: { tiers: { trivial: { max_subagents: 3 } } }", true},
		{"governance: { tiers: { trivial: { expected_usd: 0.25, expected_minutes: 10, max_subagents: 3 }, small: { expected_usd: 1, expected_minutes: 30, max_subagents: 3 }, medium: { expected_usd: 3, expected_minutes: 90 }, large: { expected_usd: 8, expected_minutes: 240 } } }", true},
		{"governance: { tiers: {} }", true},
		{"governance: { tiers: { enormous: { max_subagents: 3 } } }", false},
		{"governance: { tiers: { small: { expected_dollars: 1 } } }", false},
		{"governance: { tiers: { small: { expected_usd: -1 } } }", false},
		{"governance: { tiers: { small: { expected_minutes: -1 } } }", false},
		{"governance: { tiers: { small: { max_subagents: 101 } } }", false},
		{"governance: 0.7", false},
		{"governance: { stage_budget: 0.7 }", false},
	} {
		err := sch.Validate(yamlInstance(t, []byte(base+c.gov+"\n")))
		if (err == nil) != c.ok {
			t.Errorf("%s: schema err = %v, want ok=%v", c.gov, err, c.ok)
		}
	}
}

// TestProjectLayerSchemaGovernance: the layer's own top-level governance
// block, the same shape and the same bounds (project-layer.schema.json's
// copy, like rigor). The layer is where the guardrails are loosened, so a
// looser value than Fugaro's default is valid here and a repository's own
// file may only tighten it -- which is the merge's rule, not the schema's:
// the schema refuses what no run could use, and the merge refuses the
// loosening.
func TestProjectLayerSchemaGovernance(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	head := "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	for _, c := range []struct {
		gov string
		ok  bool
	}{
		{"governance: { review_ratio: 20, max_subagents: 12 }", true},
		{"governance: { stage_budget: { implement: 0.9 } }", true},
		{"governance: { stage_timeout: { review: 45m } }", true},
		{"governance: { tiers: { trivial: { expected_usd: 0.5, expected_minutes: 20, max_subagents: 3 } } }", true},
		{"governance: { review_ratio: -1 }", false},
		{"governance: { stage_budget: { implement: 1 } }", false},
		{"governance: { stage_timeout: { implement: 5x } }", false},
		{"governance: { review_ratio: 3, typo: 1 }", false},
		{"governance: { tiers: { enormous: { max_subagents: 3 } } }", false},
		{"governance: { tiers: { small: { expected_minutes: -1 } } }", false},
		{"defaults:\n  governance: { review_ratio: 3 }", false},
	} {
		err := sch.Validate(yamlInstance(t, []byte(head+c.gov+"\n")))
		if (err == nil) != c.ok {
			t.Errorf("%s: schema err = %v, want ok=%v", c.gov, err, c.ok)
		}
	}
}

// TestSchemaGovernanceTiersMatchTheGoVocabulary ties both governance blocks'
// `tiers` object to internal/tier and to policy: the tier names are the only
// properties and are tier.Tiers, and each tier's own three keys are the policy
// keys the merge records (KeyExpectedUSD, KeyExpectedMinutes, KeyMaxSubagents)
// with no fourth. A tier added to the vocabulary without the schemas — or a
// key added to a tier — would otherwise be a name the decoder refuses while
// the published schema, the surface a repository's CI checks against, still
// accepts it.
func TestSchemaGovernanceTiersMatchTheGoVocabulary(t *testing.T) {
	wantTiers := make([]string, 0, len(tier.Tiers))
	for _, tt := range tier.Tiers {
		wantTiers = append(wantTiers, string(tt))
	}
	wantKeys := []string{policy.KeyExpectedUSD, policy.KeyExpectedMinutes, policy.KeyMaxSubagents}
	slices.Sort(wantKeys)
	for _, file := range []string{"fugaro.schema.json", "project-layer.schema.json"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Properties map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"properties"`
			Defs map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		gov, ok := doc.Properties["governance"]
		if !ok {
			t.Fatalf("%s: no governance block", file)
		}
		var tiers struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if raw, ok := gov.Properties["tiers"]; !ok {
			t.Fatalf("%s: no governance.tiers property", file)
		} else if err := json.Unmarshal(raw, &tiers); err != nil {
			t.Fatal(err)
		}
		var gotTiers []string
		for name := range tiers.Properties {
			gotTiers = append(gotTiers, name)
		}
		slices.Sort(gotTiers)
		sorted := slices.Clone(wantTiers)
		slices.Sort(sorted)
		if !slices.Equal(gotTiers, sorted) {
			t.Errorf("%s: governance.tiers properties %v != tier.Tiers %v", file, gotTiers, sorted)
		}
		limits, ok := doc.Defs["tierLimits"]
		if !ok {
			t.Fatalf("%s: no tierLimits def", file)
		}
		var gotKeys []string
		for name := range limits.Properties {
			gotKeys = append(gotKeys, name)
		}
		slices.Sort(gotKeys)
		if !slices.Equal(gotKeys, wantKeys) {
			t.Errorf("%s: tierLimits properties %v != the policy keys %v", file, gotKeys, wantKeys)
		}
	}
}

// TestSchemaGovernanceBoundsMatchGo: the counting keys of the block carry an
// upper bound in both schemas and the same bound in config.validateGovernance,
// so the two surfaces a file meets — `fugaro validate`, which reads Go, and
// the published schema a repository's CI may check against — refuse the same
// numbers. A bound only one of them knows is a red check nobody can reproduce
// from the file, or a number a run reads that the schema called impossible.
// The stage budgets are excluded on purpose: a fraction is bounded by the
// stageFraction def (under 1), which validateGovernance checks directly, with
// no Go figure to hold equal.
//
// A tier's max_subagents is held to the same bound as the untiered
// governance.max_subagents: one stage fans out no further because a run
// declared a tier, so the schema and validateGovernance must refuse the same
// figures for both.
func TestSchemaGovernanceBoundsMatchGo(t *testing.T) {
	for _, tc := range []struct {
		file    string
		maximum map[string]int
	}{
		{"fugaro.schema.json", map[string]int{
			"review_ratio":       config.MaxReviewRatio,
			"review_floor_calls": config.MaxReviewFloorCalls,
			"max_subagents":      config.MaxGovernanceSubagents,
		}},
		{"project-layer.schema.json", map[string]int{
			"review_ratio":       config.MaxReviewRatio,
			"review_floor_calls": config.MaxReviewFloorCalls,
			"max_subagents":      config.MaxGovernanceSubagents,
		}},
	} {
		data, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		gov, ok := doc["properties"].(map[string]any)["governance"].(map[string]any)
		if !ok {
			t.Fatalf("%s: no governance block", tc.file)
		}
		props, _ := gov["properties"].(map[string]any)
		for key, want := range tc.maximum {
			got, ok := props[key].(map[string]any)["maximum"].(float64)
			if !ok {
				t.Errorf("%s: governance.%s has no maximum", tc.file, key)
				continue
			}
			if int(got) != want {
				t.Errorf("%s: governance.%s maximum = %v, want %d (config's bound)", tc.file, key, got, want)
			}
		}
		tokens, ok := doc["$defs"].(map[string]any)["stageTokens"].(map[string]any)
		if !ok {
			t.Fatalf("%s: no stageTokens def", tc.file)
		}
		if got, _ := tokens["maximum"].(float64); int(got) != config.MaxStageTokens {
			t.Errorf("%s: stageTokens maximum = %v, want %d (config's bound)", tc.file, got, config.MaxStageTokens)
		}
		limits, ok := doc["$defs"].(map[string]any)["tierLimits"].(map[string]any)
		if !ok {
			t.Fatalf("%s: no tierLimits def", tc.file)
		}
		tierProps, _ := limits["properties"].(map[string]any)
		sub, _ := tierProps["max_subagents"].(map[string]any)
		got, ok := sub["maximum"].(float64)
		if !ok {
			t.Errorf("%s: tierLimits.max_subagents has no maximum", tc.file)
		} else if int(got) != config.MaxGovernanceSubagents {
			t.Errorf("%s: tierLimits.max_subagents maximum = %v, want %d (config's bound)", tc.file, got, config.MaxGovernanceSubagents)
		}
	}
}

// TestResultSchemaTierMatchesTheGoVocabulary pins result.schema.json's tier
// block to the Go side in both directions: the tier enum to tier.Tiers (the one
// closed vocabulary the task spec, the configuration and the CLI share), and
// the two figures' bound to the one the configuration side has, which is a
// floor and no ceiling. An enum narrower than the vocabulary is a result.json a
// consumer validating against the published schema refuses, while a value only
// the enum knows is a tier no run can carry; and a ceiling only the record's
// schema knows is a result.json the runner wrote and a repository's CI calls
// impossible, because the project layer may loosen a tier past any figure
// Fugaro ships.
//
// It is the same pin TestSchemaGovernanceTiersMatchTheGoVocabulary makes for
// the configuration's governance.tiers, on the record's own copy of the tier.
func TestResultSchemaTierMatchesTheGoVocabulary(t *testing.T) {
	want := make([]string, 0, len(tier.Tiers))
	for _, tt := range tier.Tiers {
		want = append(want, string(tt))
	}
	data, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	block, ok := doc["properties"].(map[string]any)["tier"].(map[string]any)
	if !ok {
		t.Fatal("result.schema.json: no tier block")
	}
	props, _ := block["properties"].(map[string]any)
	raw, ok := props["tier"].(map[string]any)["enum"].([]any)
	if !ok {
		t.Fatal(`result.schema.json: tier.tier has no enum`)
	}
	var got []string
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("result.schema.json: tier.tier enum entry %v is not a string", v)
		}
		got = append(got, s)
	}
	if !sameStrings(got, want) {
		t.Errorf("result.schema.json tier.tier = %v, internal/tier's Tiers = %v", got, want)
	}
	// The two figures are bounded below and not above, which is the bound the
	// configuration side has: validateGovernance refuses a negative or
	// non-finite expectation and nothing else, on purpose, because the project
	// layer may loosen a tier and an expectation above the per-run cap is still
	// only an expectation (G6). A maximum here would be a record the runner can
	// legally write and its own published schema rejects — and a repository's
	// CI would report a result.json the run itself produced.
	for _, key := range []string{"expected_usd", "expected_minutes"} {
		spec, _ := props[key].(map[string]any)
		if _, ok := spec["maximum"]; ok {
			t.Errorf("result.schema.json: tier.%s carries a maximum, which the configuration side has none of", key)
		}
		if minimum, ok := spec["minimum"].(float64); !ok || minimum != 0 {
			t.Errorf("result.schema.json: tier.%s minimum = %v, want 0", key, spec["minimum"])
		}
	}
	// A figure the project layer may write and the run resolved is a record the
	// schema has to accept: the per-run cap, never the schema, is what stops a
	// run whose expectation is larger than it.
	lit := `{"version":1,"run_id":"20261028-170609-abcd","status":"succeeded","stage":"finalize","outcome":"ready","cost_usd":0,"started_at":"2026-10-28T17:06:09Z",` +
		`"tier":{"tier":"large","expected_usd":5000,"expected_minutes":20000}}`
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(lit))
	if err != nil {
		t.Fatal(err)
	}
	if err := compile(t, "result.schema.json").Validate(inst); err != nil {
		t.Errorf("a record carrying a tier expectation the configuration side permits does not validate: %v", err)
	}
}

// TestTaskSchemaABBlock: task.schema.json carries the pair block (model A/B
// A1.1) in both arms and refuses the shapes task.Spec.Validate refuses. The
// schema is additionalProperties: false, so without the property every task a
// sampled launch writes — the only tasks that carry `ab` — is schema-invalid
// for every consumer of the published document.
func TestTaskSchemaABBlock(t *testing.T) {
	sch := compile(t, "task.schema.json")
	validate := func(t *testing.T, ab *task.AB) error {
		t.Helper()
		s := task.Spec{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", AB: ab}
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	const pairID = "20261029-120000-abcd"
	challenger := &task.AB{PairID: pairID, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
		Challenger: "invented-challenger-1", ChallengerSource: task.ABChallengerOwner}
	for _, tc := range []struct {
		name string
		ab   *task.AB
	}{
		{"no block", nil},
		{"a champion", &task.AB{PairID: pairID, Arm: task.ABArmChampion}},
		{"a challenger", challenger},
	} {
		if err := validate(t, tc.ab); err != nil {
			t.Errorf("schema rejects %s: %v", tc.name, err)
		}
	}
	noPublish := *challenger
	noPublish.Publish = ""
	noModel := *challenger
	noModel.Challenger = ""
	championPublishing := task.AB{PairID: pairID, Arm: task.ABArmChampion, Publish: task.ABPublishNone}
	championWithModel := task.AB{PairID: pairID, Arm: task.ABArmChampion, Challenger: "invented-challenger-1"}
	championWithSource := task.AB{PairID: pairID, Arm: task.ABArmChampion, ChallengerSource: task.ABChallengerOwner}
	unknownArm := *challenger
	unknownArm.Arm = "shadow"
	emptyPair := *challenger
	emptyPair.PairID = ""
	blankPair := *challenger
	blankPair.PairID = "   "
	blankModel := *challenger
	blankModel.Challenger = " "
	unknownSource := *challenger
	unknownSource.ChallengerSource = "repo"
	for _, tc := range []struct {
		name string
		ab   *task.AB
	}{
		{"a challenger with no publish", &noPublish},
		{"a challenger with no model", &noModel},
		{"a champion that publishes", &championPublishing},
		{"a champion naming a model", &championWithModel},
		{"a champion naming a source", &championWithSource},
		{"an unknown arm", &unknownArm},
		{"an empty pair_id", &emptyPair},
		{"a blank pair_id", &blankPair},
		{"a blank challenger model", &blankModel},
		{"an unknown challenger_source", &unknownSource},
	} {
		if err := validate(t, tc.ab); err == nil {
			t.Errorf("schema accepts %s", tc.name)
		}
	}
	// A key inside the block that is not one of its five, and a block beside
	// the override the challenger may never travel as: the block is
	// additionalProperties: false like the document around it, and the
	// overrides exclusion is task.Spec.Validate's own rule (D555).
	for _, raw := range []string{
		`{"version":1,"run_id":"20261029-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it",` +
			`"overrides":{},"ab":{"pair_id":"20261029-120000-abcd","arm":"champion","publish_rate":0.05}}`,
		`{"version":1,"run_id":"20261029-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it",` +
			`"overrides":{"model":"invented-champion-1"},` +
			`"ab":{"pair_id":"20261029-120000-abcd","arm":"champion"}}`,
	} {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err == nil {
			t.Errorf("schema accepts %s", raw)
		}
	}
}

// TestResultSchemaABBlock: a record carrying the pair block and the patch
// object's own two facts validates, a record carrying none of them validates
// too (the fields are additive, so version stays 1), and the patch object is
// the key it is rather than free text.
func TestResultSchemaABBlock(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	validate := func(t *testing.T, rec runstore.Record) error {
		t.Helper()
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	base := runstore.Record{Version: 1, RunID: "20261029-120000-abcd", Repo: "owner/repo-a",
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady,
		CostUSD: 1.25, StartedAt: at}
	if err := validate(t, base); err != nil {
		t.Fatalf("schema rejects a record with no ab block: %v", err)
	}
	// A challenger records the shadow arm and the patch it uploaded; a
	// champion records its own arm and its patch.
	for _, ab := range []*task.AB{
		{PairID: base.RunID, Arm: task.ABArmChampion},
		{PairID: base.RunID, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
			Challenger: "invented-challenger-1", ChallengerSource: task.ABChallengerOwner},
	} {
		rec := base
		rec.AB = ab
		rec.PatchObject = "runs/owner-repo-a/" + base.RunID + "/ab/patch.diff"
		rec.PatchTruncated = true
		if err := validate(t, rec); err != nil {
			t.Fatalf("schema rejects a record carrying %+v: %v", ab, err)
		}
	}
	rec := base
	rec.AB = &task.AB{PairID: base.RunID, Arm: "shadow"}
	if err := validate(t, rec); err == nil {
		t.Error("schema accepts an unknown arm on a record")
	}
	// The patch object is the key it is, and the truncation flag means
	// nothing without one: a record that stored no patch cannot say the patch
	// it does not name was cut (patch_object's pattern and patch_truncated's
	// dependentRequired).
	rec = base
	rec.AB, rec.PatchTruncated = &task.AB{PairID: base.RunID, Arm: task.ABArmChampion}, true
	if err := validate(t, rec); err == nil {
		t.Error("schema accepts patch_truncated with no patch_object")
	}
	rec = base
	rec.AB, rec.PatchObject = &task.AB{PairID: base.RunID, Arm: task.ABArmChampion}, "patch.diff"
	if err := validate(t, rec); err == nil {
		t.Error("schema accepts a patch_object that is not the run's own key")
	}
}

// TestABSchemaEnumsMatchTheGoVocabulary ties both schemas' ab enums to the Go
// constants they mirror: the schema and internal/task are separate copies of
// one closed vocabulary, so without this a value added on one side would drift
// from the other with only the fixtures noticing — and the two documents must
// agree with each other, since a pair's task and its record carry the same
// block.
func TestABSchemaEnumsMatchTheGoVocabulary(t *testing.T) {
	read := func(file string) map[string][]string {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		raw, ok := doc.Properties["ab"]
		if !ok {
			t.Fatalf("%s has no ab property: a sampled task or record is schema-invalid (additionalProperties: false)", file)
		}
		var ab struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &ab); err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for name, p := range ab.Properties {
			out[name] = p.Enum
		}
		return out
	}
	want := map[string][]string{
		"arm":               {string(task.ABArmChampion), string(task.ABArmChallenger)},
		"publish":           {string(task.ABPublishNone)},
		"challenger_source": {string(task.ABChallengerOwner)},
	}
	for _, file := range []string{"task.schema.json", "result.schema.json"} {
		got := read(file)
		for name, e := range want {
			if !slices.Equal(got[name], e) {
				t.Errorf("%s: ab.%s enum = %v, want %v (internal/task's own constants)", file, name, got[name], e)
			}
		}
	}
}

// TestSchemaCoversSpecAuthor: task.schema.json's `author` property and
// task.Spec.Author agree, on the shape and on which documents they accept.
// The schema is additionalProperties: false, so a property missing here
// makes every task that carries an author schema-invalid, and only the
// schema's consumers would notice — the defect that made the plan_task and
// oauth_token properties necessary, one field at a time (A2a).
//
// The bounds and the field names are read from internal/task's own constants
// and the marshalled struct rather than restated, so a cap moved on one side,
// or a field renamed, stops the two agreeing.
func TestSchemaCoversSpecAuthor(t *testing.T) {
	data, err := os.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, ok := doc.Properties["author"]
	if !ok {
		t.Fatal("task.schema.json has no author property: a task carrying one is schema-invalid (additionalProperties: false)")
	}
	var author struct {
		AdditionalProperties *bool    `json:"additionalProperties"`
		Required             []string `json:"required"`
		Properties           map[string]struct {
			Pattern   string `json:"pattern"`
			MaxLength int    `json:"maxLength"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &author); err != nil {
		t.Fatal(err)
	}
	if author.AdditionalProperties == nil || *author.AdditionalProperties {
		t.Error("author does not carry additionalProperties: false: a field inside it would be read by nobody")
	}
	if want := []string{"name", "email"}; !slices.Equal(author.Required, want) {
		t.Errorf("author.required = %v, want %v (task.Person's own two fields)", author.Required, want)
	}
	if got := author.Properties["name"].MaxLength; got != task.MaxAuthorNameLen {
		t.Errorf("author.name maxLength = %d, want task.MaxAuthorNameLen %d", got, task.MaxAuthorNameLen)
	}
	if got := author.Properties["email"].MaxLength; got != task.MaxAuthorEmailLen {
		t.Errorf("author.email maxLength = %d, want task.MaxAuthorEmailLen %d", got, task.MaxAuthorEmailLen)
	}
	// The patterns are the schema's copy of AuthorProblem's own two rules, held
	// against the Go check over a corpus rather than compared as strings: the
	// Go rule is a loop over the fields (which is what lets it say which rule
	// was broken), not an expression, so the two are compared by what they
	// answer. Every case below is inside both caps, which maxLength carries
	// above, so the pattern is the only rule in question.
	nameRE := regexp.MustCompile(author.Properties["name"].Pattern)
	emailRE := regexp.MustCompile(author.Properties["email"].Pattern)
	for _, tc := range []struct {
		name string
		p    task.Person
	}{
		{"a plain pair", task.Person{Name: "person-1", Email: "person-1@example.invalid"}},
		{"a space inside the name", task.Person{Name: "person one", Email: "person.one@example.invalid"}},
		{"a name outside ASCII", task.Person{Name: "Jörg", Email: "jorg@example.invalid"}},
		{"the name and the address at both caps", task.Person{Name: strings.Repeat("a", task.MaxAuthorNameLen), Email: strings.Repeat("a", task.MaxAuthorEmailLen-6) + "@b.co"}},
		{"a blank name", task.Person{Name: "  ", Email: "person-1@example.invalid"}},
		{"a name of one tab", task.Person{Name: "\t", Email: "person-1@example.invalid"}},
		{"a newline in the name", task.Person{Name: "person-1\nperson-2", Email: "person-1@example.invalid"}},
		{"an angle bracket in the name", task.Person{Name: "person-1 <person-2>", Email: "person-1@example.invalid"}},
		{"a control character in the email", task.Person{Name: "person-1", Email: "person\x1b-1@example.invalid"}},
		{"an email with no @", task.Person{Name: "person-1", Email: "person-1.example.invalid"}},
		{"an email with two @", task.Person{Name: "person-1", Email: "person-1@example.invalid@x"}},
		{"an email with an empty local part", task.Person{Name: "person-1", Email: "@example.invalid"}},
		{"an email with an empty domain", task.Person{Name: "person-1", Email: "person-1@"}},
	} {
		byPattern := nameRE.MatchString(tc.p.Name) && emailRE.MatchString(tc.p.Email)
		byGoRule := task.AuthorProblem(tc.p) == ""
		if byPattern != byGoRule {
			t.Errorf("%s: schema pattern accepts = %v, task.AuthorProblem accepts = %v (%s)", tc.name, byPattern, byGoRule, task.AuthorProblem(tc.p))
		}
	}

	sch := compile(t, "task.schema.json")
	// Both readers see the same document: the schema validates the JSON, and
	// task.Parse is what the runner does with it. A fixture one refuses and
	// the other accepts is the drift this test exists for, so each case
	// asserts both.
	agree := func(t *testing.T, raw string) (schemaErr, parseErr error) {
		t.Helper()
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		_, parseErr = task.Parse([]byte(raw))
		return sch.Validate(inst), parseErr
	}
	docFor := func(a *task.Person) string {
		t.Helper()
		s := task.Spec{Version: 1, RunID: "20261030-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", Author: a}
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, tc := range []struct {
		name string
		doc  string
		ok   bool
	}{
		{"no author", docFor(nil), true},
		{"an author", docFor(&task.Person{Name: "person-1", Email: "person-1@example.invalid"}), true},
		{"a name over the cap", docFor(&task.Person{Name: strings.Repeat("a", task.MaxAuthorNameLen+1), Email: "person-1@example.invalid"}), false},
		{"an email over the cap", docFor(&task.Person{Name: "person-1", Email: strings.Repeat("a", task.MaxAuthorEmailLen) + "@example.invalid"}), false},
		{"a blank name", docFor(&task.Person{Name: "   ", Email: "person-1@example.invalid"}), false},
		{"a missing email", `{"version":1,"run_id":"20261030-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it","overrides":{},"author":{"name":"person-1"}}`, false},
		{"an email with two @", docFor(&task.Person{Name: "person-1", Email: "person-1@example.invalid@x"}), false},
		{"an email with no @", docFor(&task.Person{Name: "person-1", Email: "person-1.example.invalid"}), false},
		{"a newline in the name", docFor(&task.Person{Name: "person-1\nperson-2", Email: "person-1@example.invalid"}), false},
		{"an angle bracket in the name", docFor(&task.Person{Name: "person-1 <person-2>", Email: "person-1@example.invalid"}), false},
		{"a key the block does not have", `{"version":1,"run_id":"20261030-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it","overrides":{},"author":{"name":"person-1","email":"person-1@example.invalid","login":"person-1"}}`, false},
	} {
		schemaErr, parseErr := agree(t, tc.doc)
		if tc.ok {
			if schemaErr != nil {
				t.Errorf("%s: schema rejects it: %v", tc.name, schemaErr)
			}
			if parseErr != nil {
				t.Errorf("%s: task.Parse rejects it: %v", tc.name, parseErr)
			}
			continue
		}
		if schemaErr == nil {
			t.Errorf("%s: schema accepts it", tc.name)
		}
		if parseErr == nil {
			t.Errorf("%s: task.Parse accepts it", tc.name)
		}
	}
	// The golden bytes: every field of a Person is written when the block is
	// present, so a decoder reading it back cannot mistake a missing field
	// for an empty one.
	if got := docFor(&task.Person{Name: "person-1", Email: "person-1@example.invalid"}); !strings.Contains(got, `"author":{"name":"person-1","email":"person-1@example.invalid"}`) {
		t.Errorf("an author is not written as its two fields: %s", got)
	}
}

// TestSchemaCoversGitAuthor: fugaro.schema.json's git.author enum and
// config's own vocabulary are one list, and the schema and config.Parse
// agree on which files carry a legal value — the schema is what an editor
// validates fugaro.yaml against, the parser is what the launcher does.
//
// The enum carries the empty string as well as config.GitAuthors' two
// values: a file that spells out author: "" says what the key absent says
// (today's behaviour, the run's own identity), and the parser accepts both,
// so a schema that refused one of them would reject a file the CLI runs.
func TestSchemaCoversGitAuthor(t *testing.T) {
	data, err := os.ReadFile("fugaro.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, ok := doc.Properties["git"]
	if !ok {
		t.Fatal("fugaro.schema.json has no git property")
	}
	var git struct {
		Properties map[string]struct {
			// []any, not []string: a JSON null in an enum of strings decodes
			// into "" and would hide the null this test exists to check.
			Enum []any `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &git); err != nil {
		t.Fatal(err)
	}
	got, ok := git.Properties["author"]
	if !ok {
		t.Fatal("fugaro.schema.json has no git.author property: a repository that opts in writes a file this schema rejects (additionalProperties: false)")
	}
	if want := []any{"", nil, config.GitAuthorFugaro, config.GitAuthorLauncher}; !slices.Equal(got.Enum, want) {
		t.Errorf("git.author enum = %v, want %v (config.GitAuthors, with the empty value and an explicit null — the spells budget.mode's enum carries)", got.Enum, want)
	}

	sch := compile(t, "fugaro.schema.json")
	const (
		base   = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make } }\n"
		gitKey = "git: { provider: github }"
	)
	for _, tc := range []struct {
		name   string
		author string
		ok     bool
	}{
		{"the key absent", "", true},
		{"fugaro", "author: fugaro", true},
		{"launcher", "author: launcher", true},
		{"the empty value", `author: ""`, true},
		// The two null spells YAML invites and config.Parse reads as the
		// empty value: an empty scalar after the colon, and an explicit null.
		{"the empty scalar", "author:", true},
		{"an explicit null", "author: null", true},
		{"a near miss", "author: Fugaro", false},
		{"a value that is not one of the two", "author: them", false},
	} {
		body := base
		if tc.author != "" {
			body = strings.Replace(base, gitKey, "git: { provider: github, "+tc.author+" }", 1)
		}
		schemaErr := sch.Validate(yamlInstance(t, []byte(body)))
		_, ps := config.Parse([]byte(body))
		if tc.ok {
			if schemaErr != nil {
				t.Errorf("%s: schema rejects it: %v", tc.name, schemaErr)
			}
			if len(ps) > 0 {
				t.Errorf("%s: config.Parse rejects it: %v", tc.name, ps)
			}
			continue
		}
		if schemaErr == nil {
			t.Errorf("%s: schema accepts it", tc.name)
		}
		if len(ps) == 0 {
			t.Errorf("%s: config.Parse accepts it", tc.name)
		}
	}
}

// TestSchemaCoversImageKeyFiles: fugaro.schema.json carries image.key_files,
// with the bounds internal/config enforces, and the two agree — the schema is
// what an editor reads, config.Parse is what a build refuses — over the corpus
// below: the paths that escape the repository, the ones that are patterns
// rather than paths, the ones that are not clean, and the caps.
func TestSchemaCoversImageKeyFiles(t *testing.T) {
	data, err := os.ReadFile("fugaro.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var workflows struct {
		AdditionalProperties json.RawMessage `json:"additionalProperties"`
	}
	if err := json.Unmarshal(doc.Properties["workflows"], &workflows); err != nil {
		t.Fatal(err)
	}
	// The workflow schema is a $ref, so image: sits in $defs, not inline.
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(workflows.AdditionalProperties, &ref); err != nil {
		t.Fatal(err)
	}
	defName, ok := strings.CutPrefix(ref.Ref, "#/$defs/")
	if !ok {
		t.Fatalf("workflows.additionalProperties = %s, want a $ref into $defs", workflows.AdditionalProperties)
	}
	var def struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(doc.Defs[defName], &def); err != nil {
		t.Fatal(err)
	}
	var image struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			MaxItems    int    `json:"maxItems"`
			UniqueItems bool   `json:"uniqueItems"`
			Items       struct {
				Type      string `json:"type"`
				MaxLength int    `json:"maxLength"`
				Pattern   string `json:"pattern"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(def.Properties["image"], &image); err != nil {
		t.Fatal(err)
	}
	got, hasKey := image.Properties["key_files"]
	if !hasKey {
		t.Fatal("fugaro.schema.json has no image.key_files property: a repository that writes one gets a schema error (additionalProperties: false)")
	}
	if got.Type != "array" || got.Items.Type != "string" || got.MaxItems != 32 || !got.UniqueItems || got.Items.MaxLength != 200 || got.Items.Pattern == "" {
		t.Errorf("image.key_files = %+v, want an array of at most 32 unique strings of at most 200 characters matching a pattern (maxImageKeyFiles, maxImageKeyFilePath)", got)
	}

	sch := compile(t, "fugaro.schema.json")
	const (
		base = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make } }\n"
		head = "commands: { build: make, test: make }"
	)
	caps := func(n int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = strconv.Quote(strings.Replace("patches/00.patch", "00", string(rune('a'+i%26))+string(rune('a'+i/26)), 1))
		}
		return strings.Join(out, ", ")
	}
	for _, tc := range []struct {
		name    string
		entries string // the YAML flow sequence, or "" for no image block
		ok      bool
	}{
		{"the key absent", "", true},
		{"one directory", strconv.Quote("patches"), true},
		{"a file and a directory", strconv.Quote(".yarn/patches/left-pad.patch") + ", " + strconv.Quote("vendor"), true},
		{"a space", strconv.Quote("vendor/my tool"), true},
		{"a non-ASCII path", strconv.Quote(strings.Repeat("é", 150)), true},
		{"at the length cap", strconv.Quote(strings.Repeat("a", 200)), true},
		{"at the cap", caps(32), true},
		{"an absolute path", strconv.Quote("/etc/passwd"), false},
		{"a parent segment", strconv.Quote("../outside"), false},
		{"a parent segment in the middle", strconv.Quote("a/../../etc/passwd"), false},
		{"a bare parent", strconv.Quote(".."), false},
		{".git", strconv.Quote(".git/config"), false},
		{"the repository root", strconv.Quote("."), false},
		{"a trailing slash", strconv.Quote("patches/"), false},
		{"a doubled slash", strconv.Quote("a//b"), false},
		{"a dot segment", strconv.Quote("./patches"), false},
		{"a star", strconv.Quote("*.patch"), false},
		{"a doublestar", strconv.Quote("**/*.patch"), false},
		{"a question mark", strconv.Quote("patch?"), false},
		{"a character class", strconv.Quote("p[abc]"), false},
		{"a brace group", strconv.Quote("p{a,b}"), false},
		{"a backslash", strconv.Quote(`a\b`), false},
		{"a control character", strconv.Quote("a\x1bb"), false},
		{"empty", strconv.Quote(""), false},
		{"a duplicate", strconv.Quote("patches") + ", " + strconv.Quote("patches"), false},
		{"over the cap", caps(33), false},
		{"over the length cap", strconv.Quote(strings.Repeat("a", 201)), false},
	} {
		body := base
		if tc.entries != "" {
			body = strings.Replace(base, head, "image: { key_files: ["+tc.entries+"] }, "+head, 1)
		}
		schemaErr := sch.Validate(yamlInstance(t, []byte(body)))
		_, ps := config.Parse([]byte(body))
		if tc.ok {
			if schemaErr != nil {
				t.Errorf("%s: schema rejects it: %v", tc.name, schemaErr)
			}
			if len(ps) > 0 {
				t.Errorf("%s: config.Parse rejects it: %v", tc.name, ps)
			}
			continue
		}
		if schemaErr == nil {
			t.Errorf("%s: schema accepts it", tc.name)
		}
		if len(ps) == 0 {
			t.Errorf("%s: config.Parse accepts it", tc.name)
		}
	}

	// checkout: clone builds no image at all, so a clone workflow refuses
	// key_files, exactly as config.Parse does: the schema's clone branch
	// cannot leave the key out, or an editor would bless a fugaro.yaml the
	// run then refuses.
	for _, entries := range []string{"", strconv.Quote("patches")} {
		body := strings.Replace(base, "  app: {", "  app: { checkout: clone,", 1)
		if entries != "" {
			body = strings.Replace(body, head, "image: { key_files: ["+entries+"] }, "+head, 1)
		}
		schemaErr := sch.Validate(yamlInstance(t, []byte(body)))
		_, ps := config.Parse([]byte(body))
		switch {
		case entries == "":
			if schemaErr != nil {
				t.Errorf("a clone workflow with no key files: schema rejects it: %v", schemaErr)
			}
			if len(ps) > 0 {
				t.Errorf("a clone workflow with no key files: config.Parse rejects it: %v", ps)
			}
		default:
			if schemaErr == nil {
				t.Error("the schema accepts image.key_files under checkout: clone, which config.Parse refuses")
			}
			if len(ps) == 0 {
				t.Error("config.Parse accepts image.key_files under checkout: clone")
			}
		}
	}
}

// TestSchemaCoversCheckoutShared: checkout: shared is a third kind
// (design forge-authorship-images/shared-images.md §6, route A) whose job
// runs a shared dependency-layer image and clones the source at launch, so
// the schema accepts the dependency-layer keys and refuses the two that
// would put this repository's own content into an image every repository on
// the same dependency set runs. The schema and config.Parse agree on which
// of those two is refused, and disagree on purpose about the kind itself:
// the kind stays in the schema's enum — an editor's user still gets the
// shape it will have — while validation refuses checkout: shared until
// 0.15.0 ships the image it would run (config/image.go's
// sharedCheckoutShipped, whose rules internal/config/image_test.go pins
// behind the flag). Nothing here reads a shared workflow's key rules out of
// config.Parse: those live in internal/config.
func TestSchemaCoversCheckoutShared(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	const base = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  app: { checkout: shared, commands: { build: make, test: make } }\n"

	// The enum is the Go constants, so a fourth kind cannot land in one
	// place only.
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	data, err := os.ReadFile("fugaro.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var def struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(doc.Defs["workflow"], &def); err != nil {
		t.Fatal(err)
	}
	want := []string{config.CheckoutBaked, config.CheckoutClone, config.CheckoutShared}
	if got := def.Properties["checkout"].Enum; !slices.Equal(got, want) {
		t.Errorf("fugaro.schema.json's checkout enum = %v, want the Go constants %v", got, want)
	}

	// The dependency layer's keys: the schema accepts them under checkout:
	// shared, and config.Parse refuses the kind where the repository typed
	// it, naming the release that lifts the refusal.
	body := strings.Replace(base, "commands:", `image: { apt: [libvips-dev], tools: { node: "24.19.0" }, key_files: [patches], skip_build_scripts: true }, commands:`, 1)
	if err := sch.Validate(yamlInstance(t, []byte(body))); err != nil {
		t.Errorf("the schema rejects checkout: shared with the dependency-layer keys: %v", err)
	}
	if _, ps := config.Parse([]byte(body)); len(ps) != 1 || ps[0].Path != "workflows.app.checkout" || !strings.Contains(ps[0].Message, "0.15.0") {
		t.Errorf("config.Parse problems = %v, want exactly one refusal at workflows.app.checkout naming 0.15.0", ps)
	}

	// The per-repository keys: the schema refuses both — a schema that
	// blessed a setup: validation refuses would send an editor's user away
	// with a fugaro.yaml that fails at validate — and config.Parse refuses
	// the kind before it reaches either key, so both halves still say this
	// declaration is not one to ship.
	for _, tc := range []struct{ name, extra, path string }{
		{"image.setup", `image: { setup: ["make deps"] }, `, "workflows.app.image.setup"},
		{"dockerfile", "dockerfile: .fugaro/app.Dockerfile, ", "workflows.app.dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(base, "commands:", tc.extra+"commands:", 1)
			schemaErr := sch.Validate(yamlInstance(t, []byte(body)))
			_, ps := config.Parse([]byte(body))
			if schemaErr == nil {
				t.Errorf("the schema accepts %s under checkout: shared", tc.path)
			}
			if len(ps) != 1 || ps[0].Path != "workflows.app.checkout" || !strings.Contains(ps[0].Message, "0.15.0") {
				t.Errorf("config.Parse problems = %v, want exactly one refusal at workflows.app.checkout naming 0.15.0", ps)
			}
		})
	}
}

// TestSchemaDescribesCheckoutSharedAsNotYetShipped: the enum keeps shared,
// so an editor still completes the key, and the description has to say why
// validation refuses it — a user who types it is owed the release that
// lifts the refusal, not just the refusal.
func TestSchemaDescribesCheckoutSharedAsNotYetShipped(t *testing.T) {
	desc := schemaDescriptionAt(t, "fugaro.schema.json", "$defs", "workflow", "properties", "checkout")
	for _, want := range []string{"0.15.0", "refuses checkout: shared"} {
		if !strings.Contains(desc, want) {
			t.Errorf("fugaro.schema.json's checkout description does not say %q: %q", want, desc)
		}
	}
}

// TestProjectLayerSchemaCoversCheckoutShared: the same third kind in the
// other layer. One validateCheckout serves both (config.ParseProjectLayer's
// validateProfile calls it), so the project layer's profile schema has to
// accept the same three values and refuse the one per-repository key under
// shared — left alone it refused checkout: shared in a profile while the Go
// validator accepted it, and no corpus file caught the disagreement the
// test above pins for fugaro.schema.json. As there, validation refuses the
// kind itself until 0.15.0, so what config.ParseProjectLayer is asked for
// here is that refusal.
func TestProjectLayerSchemaCoversCheckoutShared(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	data, err := os.ReadFile("project-layer.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var def struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(doc.Defs["profile"], &def); err != nil {
		t.Fatal(err)
	}
	want := []string{config.CheckoutBaked, config.CheckoutClone, config.CheckoutShared}
	if got := def.Properties["checkout"].Enum; !slices.Equal(got, want) {
		t.Errorf("project-layer.schema.json's profile checkout enum = %v, want the Go constants %v", got, want)
	}

	a := config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	const base = "version: 1\nproject: aurora\ngcp_project: proj-1234\nprofiles:\n  p:\n    checkout: shared\n    commands: { build: make, test: make }\n"

	// The dependency layer's keys a profile may carry: the schema accepts
	// them, the same shape
	// testdata/project-layer/valid/checkout-shared-profile.yaml carries
	// through the corpus (which internal/config runs with the flag open).
	withKeys := strings.Replace(base, "    commands:", "    image: { apt: [libvips-dev], tools: { node: \"24.19.0\" }, skip_build_scripts: true }\n    commands:", 1)
	if err := sch.Validate(yamlInstance(t, []byte(withKeys))); err != nil {
		t.Errorf("the schema rejects a checkout: shared profile with the dependency-layer keys: %v", err)
	}
	if _, ps := config.ParseProjectLayer([]byte(withKeys), a); len(ps) != 1 || ps[0].Path != "profiles.p.checkout" || !strings.Contains(ps[0].Message, "0.15.0") {
		t.Errorf("config.ParseProjectLayer problems = %v, want exactly one refusal at profiles.p.checkout naming 0.15.0", ps)
	}

	// image.setup is the one per-repository key a profile can carry
	// (dockerfile and image.key_files are repo-only): refused by the schema,
	// and by config.ParseProjectLayer through the refusal of the kind — the
	// mirror of fugaro.schema.json's rule.
	withSetup := strings.Replace(base, "    commands:", "    image: { setup: [\"./scripts/warm.sh\"] }\n    commands:", 1)
	schemaErr := sch.Validate(yamlInstance(t, []byte(withSetup)))
	_, ps := config.ParseProjectLayer([]byte(withSetup), a)
	if schemaErr == nil {
		t.Error("the schema accepts image.setup under a profile's checkout: shared")
	}
	if len(ps) != 1 || ps[0].Path != "profiles.p.checkout" || !strings.Contains(ps[0].Message, "0.15.0") {
		t.Errorf("config.ParseProjectLayer problems = %v, want exactly one refusal at profiles.p.checkout naming 0.15.0", ps)
	}
}

// TestSettingsSchema holds schemas/settings.schema.json to the registry and to
// the command that writes the document. The document's rows are dynamic — one
// per key and scope — so the schema cannot pin them by name; what it can pin,
// and what this test checks, is that the schema loads, that its layer
// vocabulary is settings.Layers (the same list --layer takes) and that its
// $defs.keyDefaults carries every registry key with the default the registry
// gives it. That is the DEFAULT column's contract written down where a
// consumer of the JSON document can read it, and D841's "pinned by a test
// rather than generated": the values are written out here, and this test is
// what makes a row that moves without them red.
//
// The document itself is validated against this schema by
// TestSettingsJSONMatchesSchema in internal/cli, which is where the command
// that writes it lives.
func TestSettingsSchema(t *testing.T) {
	data, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("no $defs")
	}
	// A schema that does not compile is a schema nobody can use, and the
	// $refs between $defs are exactly where a hand edit breaks.
	compile(t, "settings.schema.json")

	wantLayers := settings.Layers
	for _, tc := range []struct {
		def  string
		path []string
	}{
		{"source", []string{"source", "properties", "layer", "enum"}},
		{"row", []string{"row", "properties", "wins", "enum"}},
	} {
		got := stringsOf(t, defs, tc.path...)
		if !slices.Equal(got, wantLayers) {
			t.Errorf("$defs.%s: the enum is %v, and settings.Layers is %v", tc.def, got, wantLayers)
		}
	}

	kd, ok := defs["keyDefaults"].(map[string]any)
	if !ok {
		t.Fatal("no $defs.keyDefaults")
	}
	props, ok := kd["properties"].(map[string]any)
	if !ok {
		t.Fatal("$defs.keyDefaults has no properties")
	}
	rows := settings.Rows()
	if len(props) != len(rows) {
		t.Errorf("$defs.keyDefaults carries %d keys, and the registry has %d rows", len(props), len(rows))
	}
	for _, r := range rows {
		entry, ok := props[r.Key].(map[string]any)
		if !ok {
			t.Errorf("%s: no $defs.keyDefaults entry", r.Key)
			continue
		}
		want := r.Default.Value
		if want == "" {
			want = r.Default.Unset
		}
		if got, _ := entry["default"].(string); got != want {
			t.Errorf("%s: the schema's default is %q, and the registry says %q", r.Key, got, want)
		}
		if got, _ := entry["$comment"].(string); got != r.Default.Const {
			t.Errorf("%s: the schema's $comment is %q, and the registry's constant is %q", r.Key, got, r.Default.Const)
		}
		delete(props, r.Key)
	}
	for key := range props {
		t.Errorf("$defs.keyDefaults carries %s, and the registry has no such row", key)
	}
}

// stringsOf is a schema node's string array, walked by property names.
func stringsOf(t *testing.T, node map[string]any, path ...string) []string {
	t.Helper()
	var cur any = node
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%v: not an object at %q", path, key)
		}
		if cur, ok = m[key]; !ok {
			t.Fatalf("%v: missing %q", path, key)
		}
	}
	raw, ok := cur.([]any)
	if !ok {
		t.Fatalf("%v: not an array", path)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%v: %v is not a string", path, v)
		}
		out = append(out, s)
	}
	return out
}
