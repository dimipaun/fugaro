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
	"github.com/dimipaun/fugaro/internal/recipe"
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

func TestSchemaBudgetBlock(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	base := "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  s: { base: java-services, commands: { build: make, test: make } }\n"
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

func TestResultSchemaAcceptsRunnerRecords(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	deadline, finished := at.Add(time.Hour), at.Add(10*time.Minute)
	cost := runstore.NewCost(4.12, 0.38, runstore.BasisAPIList)
	rec := runstore.Record{
		Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Workflow: "web",
		Execution: "projects/p/locations/r/jobs/j/executions/e",
		Status:    runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reason: "tests failing on the final commit", Branch: "fugaro/add-a-feature", BaseBranch: "main", HeadSHA: "abcdef1234567",
		PR:      &runstore.PRRef{Number: 7, URL: "https://example.com/pr/7"},
		Reviews: []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: "changes", Findings: 2}, {Round: 1, Tier: runstore.TierSenior, Verdict: "ship"}},
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
		return []byte("version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  server:\n    base: java-services\n" +
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
	const base = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web:\n    base: web-node\n    commands: { build: npm run build, test: npm test }\n    rebuild: { max_age: "
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
		return []byte("version: 1\nproject: aurora\ngit: { provider: " + provider + " }\nworkflows:\n  web:\n    base: web-node\n    commands: { build: npm run build, test: npm test }\nfollowup:\n  trusted: " + trusted + "\n")
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
	} {
		if err := check(r); err != nil {
			t.Errorf("schema rejects halt reason %q: %v", r, err)
		}
	}
	if err := check("other"); err == nil {
		t.Error(`schema accepts halt reason "other"`)
	}
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

// The repository's own fugaro.yaml (Fugaro dogfooding itself) must pass the
// schema, the Go validator and the checkout checks, so a base or command rename
// can't leave it broken until a run reads it.
func TestRepositoryFugaroYAML(t *testing.T) {
	data, err := os.ReadFile("../fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := compile(t, "fugaro.schema.json").Validate(yamlInstance(t, data)); err != nil {
		t.Errorf("schema rejects fugaro.yaml: %v", err)
	}
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("fugaro.yaml: %v", problems)
	}
	if problems := config.Check(cfg, ".."); len(problems) > 0 {
		t.Errorf("fugaro.yaml against the checkout: %v", problems)
	}
	if w := cfg.Workflows["go"]; cfg.Project != "fugaro" || w.Base != "go" {
		t.Errorf("project %q, workflow go = %+v", cfg.Project, w)
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
	} {
		if err := sch.Validate(yamlInstance(t, []byte(text))); (err == nil) != valid {
			t.Errorf("%q: valid = %v, want %v (%v)", text, err == nil, valid, err)
		}
	}
}
