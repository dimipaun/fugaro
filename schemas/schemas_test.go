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
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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

func TestResultSchemaAcceptsRunnerRecords(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	deadline, finished := at.Add(time.Hour), at.Add(10*time.Minute)
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
		Reviews:  []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "changes", Findings: 2}, {Round: 1, Tier: runstore.TierSenior, Verdict: "ship"}},
		Verify: []verify.Record{{
			N: 1, Kind: verify.KindTest, Rerun: true, HeadSHA: "abcdef1234567", CleanTree: true, ExitCode: 1,
			TimedOut: true, Tests: 3, Failures: 1, Skipped: 1, Failed: []string{"pkg.A"}, Flaky: []string{"pkg.B"},
			Warning: "w", StartedAt: at, DurationS: 1.5,
		}},
		CostUSD: 4.12, Cost: &cost,
		Stages: []runstore.StageTiming{{Name: "implement", StartedAt: at, DurationS: 61, ModelUSD: 0.42, ModelNotionalUSD: 0.12, Model: "claude-sonnet-5-5"}},
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
	} {
		if err := check(r); err != nil {
			t.Errorf("schema rejects halt reason %q: %v", r, err)
		}
	}
	if err := check("other"); err == nil {
		t.Error(`schema accepts halt reason "other"`)
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
	var want []string
	for _, vs := range haltReasonSpecs(t) {
		for _, v := range vs.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("a HaltReason constant's value is not a string literal: %v", v)
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, s)
		}
	}
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

// haltReasonSpecs returns every value spec in runstore.go whose declared type
// is HaltReason.
func haltReasonSpecs(t *testing.T) []*ast.ValueSpec {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../internal/runstore/runstore.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []*ast.ValueSpec
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "HaltReason" {
				out = append(out, vs)
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
		"bad from":     func(p *runstore.PolicyRecord) { p.Ignored[0].From = "ceiling" },
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
// than loosened (plan task 1.3, H1).
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
			slices.Sorted(slices.Values([]string{policy.SourceDefaultBranch, policy.SourceBranch}))},
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
