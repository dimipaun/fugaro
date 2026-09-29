package schemas_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
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

func TestResultSchemaAcceptsRunnerRecords(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	deadline, finished := at.Add(time.Hour), at.Add(10*time.Minute)
	cost := runstore.NewCost(4.12, 0.38, runstore.BasisAPIList)
	rec := runstore.Record{
		Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Workflow: "web",
		Execution: "projects/p/locations/r/jobs/j/executions/e",
		Status:    runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reason: "tests failing on the final commit", Branch: "fugaro/add-a-feature", HeadSHA: "abcdef1234567",
		PR:      &runstore.PRRef{Number: 7, URL: "https://example.com/pr/7"},
		Reviews: []runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 2}},
		Verify: []verify.Record{{
			N: 1, Kind: verify.KindTest, Rerun: true, HeadSHA: "abcdef1234567", CleanTree: true, ExitCode: 1,
			TimedOut: true, Tests: 3, Failures: 1, Skipped: 1, Failed: []string{"pkg.A"}, Flaky: []string{"pkg.B"},
			Warning: "w", StartedAt: at, DurationS: 1.5,
		}},
		CostUSD: 4.12, Cost: &cost,
		Stages:    []runstore.StageTiming{{Name: "implement", StartedAt: at, DurationS: 61}},
		StartedAt: at, Deadline: &deadline, FinishedAt: &finished,
		FinalizeReserveS: 90,
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
		return []byte("version: 1\ngit: { provider: github }\nworkflows:\n  server:\n    base: server-jvm\n" +
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
	names := strs(at(wf, "properties", "secrets", "items", "properties", "name", "not", "enum"))
	if want := slices.Sorted(maps.Keys(config.ReservedSecrets)); !slices.Equal(names, want) {
		t.Errorf("schema reserves secret names %v, config.ReservedSecrets has %v", names, want)
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
	const base = "version: 1\ngit: { provider: github }\nworkflows:\n  web:\n    base: web-node\n    commands: { build: npm run build, test: npm test }\n    rebuild: { max_age: "
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
