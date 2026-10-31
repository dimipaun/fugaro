package config

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// testLayerAB is a project layer carrying the A/B block in full
// (docs/model-ab-testing, D550-D573).
const testLayerAB = `version: 1
project: acme
gcp_project: acme-fugaro
ab:
  rate: 0.05
  max_pairs_per_day: 2
  challengers: [deepseek/chat-x, anthropic/claude-haiku-4-5]
  judge_model: anthropic/claude-opus-5-5
  exchange_rate: { poc: 0, startup: 0.25, pro: 0.5, critical: 2, hotfix: 0 }
default_profile: node-web
profiles:
  node-web:
    commands: { build: npm run build, test: npm test }
`

// TestABStrictDecode pins that a project layer may carry an ab: block (D550:
// the A/B keys are core MIT). Every key of the block is read by the strict
// decoder, so all five exist as fields of the block rather than being refused
// as unknown keys; the repository's own file is a separate question
// (TestABRepoMayOnlyOptOut).
func TestABStrictDecode(t *testing.T) {
	l, ps := ParseProjectLayer([]byte(testLayerAB), testAnchor)
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	a := l.AB
	if a == nil {
		t.Fatal("ProjectLayer.AB is nil: the ab: block did not decode")
	}
	if a.Rate != 0.05 || a.MaxPairsPerDay != 2 || a.JudgeModel != "anthropic/claude-opus-5-5" {
		t.Errorf("ab = %+v, want rate 0.05, 2 pairs a day, the judge model", a)
	}
	if want := []string{"deepseek/chat-x", "anthropic/claude-haiku-4-5"}; !reflect.DeepEqual(a.Challengers, want) {
		t.Errorf("ab.challengers = %v, want %v", a.Challengers, want)
	}
	if want := map[rigor.Mode]float64{rigor.POC: 0, rigor.Startup: 0.25, rigor.Pro: 0.5, rigor.Critical: 2, rigor.Hotfix: 0}; !reflect.DeepEqual(a.ExchangeRate, want) {
		t.Errorf("ab.exchange_rate = %v, want %v", a.ExchangeRate, want)
	}
	// A repository's own file carries its opt-out through the same type, so
	// the strict decode must accept the key there too; Validate is what
	// refuses a value other than 0 (TestABRepoMayOnlyOptOut).
	c, pss := Parse([]byte(minimalRepo + "git: { provider: github }\nab:\n  rate: 0\nworkflows:\n  default:\n    commands: { build: make, test: make test }\n"))
	if len(pss) > 0 {
		t.Fatalf("repo problems: %v", pss)
	}
	if c.AB == nil || c.AB.Rate != 0 {
		t.Errorf("Config.AB = %+v, want rate 0", c.AB)
	}
}

// TestABDefaultsOff pins the defaults A1.2 states: a layer that writes no
// ab: block samples nothing, a block that omits the rate is off (rate 0), and
// the daily pair count a layer leaves out takes the shipped 5 (D557).
func TestABDefaultsOff(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	if l := mustLayer(t, head+"ab: {}\n"); l.AB == nil || !l.AB.Off() || l.AB.Rate != 0 {
		t.Errorf("ab: {} = %+v, want a block that is off at rate 0", l.AB)
	}
	if l := mustLayer(t, head); l.AB != nil {
		t.Errorf("a layer with no ab: block = %+v, want nil", l.AB)
	}
	var none *AB
	if !none.Off() || none.MaxPairs() != ABDefaultMaxPairsPerDay {
		t.Errorf("a nil block: Off = %v, MaxPairs = %d, want true and the shipped %d", none.Off(), none.MaxPairs(), ABDefaultMaxPairsPerDay)
	}
	l := mustLayer(t, head+"ab:\n  challengers: [deepseek/chat-x]\n")
	if l.AB.Rate != 0 || !l.AB.Off() {
		t.Errorf("ab with challengers and no rate = %+v, want off", l.AB)
	}
	if l.AB.MaxPairs() != ABDefaultMaxPairsPerDay {
		t.Errorf("MaxPairs() = %d, want the shipped %d", l.AB.MaxPairs(), ABDefaultMaxPairsPerDay)
	}
	if one := mustLayer(t, head+"ab: { max_pairs_per_day: 1 }\n"); one.AB.MaxPairs() != 1 {
		t.Errorf("MaxPairs() = %d, want the layer's own 1", one.AB.MaxPairs())
	}
}

// TestABRateBounds pins D557's ceiling: a rate in [0, ABMaxRate], no NaN and
// no negative, a daily count that is not negative, a model ID that is one and
// a mode of the vocabulary. What it refuses is a figure no sampling run could
// use, never a figure the owner chose inside the bound.
func TestABRateBounds(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for _, tc := range []struct{ name, body, want string }{
		{"over the ceiling", "ab: { rate: 0.11 }\n", "ab.rate: must be between 0 and"},
		{"a negative rate", "ab: { rate: -0.01 }\n", "ab.rate: must be between 0 and"},
		{"not a number", "ab: { rate: .nan }\n", "ab.rate: must be between 0 and"},
		{"a negative daily count", "ab: { max_pairs_per_day: -1 }\n", "ab.max_pairs_per_day: must not be negative"},
		{"a challenger that is no model ID", "ab:\n  challengers: ['not a model']\n", `ab.challengers[0]: "not a model" must be a model ID`},
		{"a judge that is no model ID", "ab: { judge_model: 'a b' }\n", `ab.judge_model: "a b" must be a model ID`},
		{"an unknown mode", "ab:\n  exchange_rate: { poc: 0.5, fast: 1 }\n", "ab.exchange_rate.fast: is not a rigor mode"},
		{"a negative exchange rate", "ab:\n  exchange_rate: { pro: -1 }\n", "ab.exchange_rate.pro: must be a finite number, not negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := ParseProjectLayer([]byte(head+tc.body), testAnchor)
			if len(ps) != 1 || !bytes.Contains([]byte(ps[0].String()), []byte(tc.want)) {
				t.Fatalf("problems = %v, want one holding %q", ps, tc.want)
			}
		})
	}
	for _, ok := range []string{"ab: { rate: 0 }\n", "ab: { rate: 0.10 }\n", "ab: { max_pairs_per_day: 0 }\n"} {
		if _, ps := ParseProjectLayer([]byte(head+ok), testAnchor); len(ps) > 0 {
			t.Errorf("%s: problems = %v, want none", ok, ps)
		}
	}
}

// TestABRepoMayOnlyOptOut pins D556/D557's tighten-only rule for the
// repository's own file: ab.rate: 0 is the one thing it may say, and any
// other rate is refused with a message naming the project layer.
func TestABRepoMayOnlyOptOut(t *testing.T) {
	l := mustLayer(t, testLayerAB)
	body := func(a string) []byte {
		return []byte(minimalRepo + "git: { provider: github }\n" + a + "workflows:\n  default:\n    commands: { build: make, test: make test }\n")
	}
	c, _, ps := Resolve(body("ab:\n  rate: 0\n"), l)
	if len(ps) > 0 {
		t.Fatalf("problems = %v, want none: a repository may opt out", ps)
	}
	if c.AB == nil || c.AB.Rate != 0 {
		t.Errorf("Config.AB = %+v, want the repository's rate 0", c.AB)
	}
	for _, rate := range []string{"0.05", "0.1"} {
		_, _, ps := Resolve(body("ab:\n  rate: "+rate+"\n"), l)
		if len(ps) != 1 || ps[0].Path != "ab.rate" || !bytes.Contains([]byte(ps[0].Message), []byte(LayerKey)) {
			t.Fatalf("rate %s: problems = %v, want one at ab.rate naming %s", rate, ps, LayerKey)
		}
	}
}

// TestABRepoCannotNameChallengers pins D556 for the repository's file: the
// challengers, the judge model, the daily pair count and the exchange rates
// are the owner's, and each is refused with a message naming the project
// layer. A repository author who could name a challenger could send that
// repository's code to a provider the owner opened only to others (F18).
func TestABRepoCannotNameChallengers(t *testing.T) {
	body := func(a string) []byte {
		return []byte(minimalRepo + "git: { provider: github }\nab:\n" + a + "workflows:\n  default:\n    commands: { build: make, test: make test }\n")
	}
	for _, tc := range []struct{ name, body, path string }{
		{"challengers", "  challengers: [deepseek/chat-x]\n", "ab.challengers"},
		{"judge_model", "  judge_model: anthropic/claude-opus-5-5\n", "ab.judge_model"},
		{"max_pairs_per_day", "  max_pairs_per_day: 100\n", "ab.max_pairs_per_day"},
		{"exchange_rate", "  exchange_rate: { pro: 1 }\n", "ab.exchange_rate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse(body(tc.body))
			if len(ps) != 1 || ps[0].Path != tc.path || !bytes.Contains([]byte(ps[0].Message), []byte(LayerKey)) {
				t.Fatalf("problems = %v, want one at %s naming %s", ps, tc.path, LayerKey)
			}
		})
	}
	if _, ps := Parse(body("")); len(ps) > 0 {
		t.Errorf("problems = %v, want none for an ab: block that names nothing", ps)
	}
}

// TestABExchangeRateDefaults pins D565's shipped figures and the layer's
// override of them: POC 0, Startup 0.25, Pro 0.5, Critical 2.0, and Hotfix 0
// (D558 never samples one).
func TestABExchangeRateDefaults(t *testing.T) {
	want := map[rigor.Mode]float64{rigor.POC: 0, rigor.Startup: 0.25, rigor.Pro: 0.5, rigor.Critical: 2, rigor.Hotfix: 0}
	var none *AB
	if got := none.ExchangeRates(); !reflect.DeepEqual(got, want) {
		t.Errorf("a nil block's exchange rates = %v, want %v", got, want)
	}
	l := mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\nab:\n  exchange_rate: { pro: 1.5 }\n")
	want[rigor.Pro] = 1.5
	got := l.AB.ExchangeRates()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exchange rates = %v, want the layer's pro over the defaults %v", got, want)
	}
	got[rigor.Startup] = 99
	if l.AB.ExchangeRate[rigor.Startup] != 0 {
		t.Errorf("ExchangeRates wrote into the layer's block: %v", l.AB.ExchangeRate)
	}
	for mode, want := range map[rigor.Mode]float64{rigor.POC: 0, rigor.Startup: 0.25, rigor.Pro: 0.5, rigor.Critical: 2, rigor.Hotfix: 0} {
		if got := ABExchangeRateDefault(mode); got != want {
			t.Errorf("ABExchangeRateDefault(%s) = %v, want %v", mode, got, want)
		}
	}
	if math.IsNaN(ABExchangeRateDefault("")) {
		t.Error("an unknown mode has an exchange rate")
	}
}

// TestABSchemasMatchDecoder pins that each schema accepts and refuses the
// samples its decoder does. Two surfaces read the same file — the strict
// decoder a run uses and the published schema a repository's CI may check
// against — and a value one refuses while the other accepts is a red check
// nobody can reproduce (the reason TestSchemaGovernanceBoundsMatchGo exists
// for the governance block).
//
// Two samples cannot match, and are pinned below as the asymmetry they are
// rather than left unsampled. The layer schema states the shape of a model
// entry and not the whole rule: CheckModelPattern also refuses a claim on
// Claude models (ClaudePatterns, behind CheckClaudeClaim) and a prefix too
// broad to name, and neither is a rule a schema can carry without a second
// copy of it in Go's place. The repository schema is the stricter side
// instead: its ab: block admits rate alone, so it refuses a key the decoder
// happens to read as unset — writing the key at all is not something a
// repository may do.
func TestABSchemasMatchDecoder(t *testing.T) {
	layerSchema := compileABSchema(t, "project-layer.schema.json")
	repoSchema := compileABSchema(t, "fugaro.schema.json")
	const head = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	const repoHead = minimalRepo + "git: { provider: github }\nworkflows:\n  default:\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct {
		name, text string
		ok         bool
	}{
		{"a full block", testLayerAB, true},
		{"no block", head, true},
		{"a rate at the ceiling", head + "ab: { rate: 0.10 }\n", true},
		{"a rate over the ceiling", head + "ab: { rate: 0.11 }\n", false},
		{"a negative rate", head + "ab: { rate: -1 }\n", false},
		{"a negative daily count", head + "ab: { max_pairs_per_day: -1 }\n", false},
		{"a challenger that is no model ID", head + "ab:\n  challengers: ['not a model']\n", false},
		{"an unknown mode", head + "ab:\n  exchange_rate: { fast: 1 }\n", false},
		{"a negative exchange rate", head + "ab:\n  exchange_rate: { pro: -1 }\n", false},
		{"an unknown block key", head + "ab: { rat: 0.05 }\n", false},
	} {
		t.Run("layer/"+tc.name, func(t *testing.T) {
			_, ps := ParseProjectLayer([]byte(tc.text), testAnchor)
			if got := len(ps) == 0; got != tc.ok {
				t.Fatalf("the decoder accepted = %v, want %v (problems: %v)", got, tc.ok, ps)
			}
			if got := abSchemaAccepts(t, layerSchema, tc.text); got != tc.ok {
				t.Errorf("project-layer.schema.json accepted = %v, want %v: the schema and the decoder disagree", got, tc.ok)
			}
		})
	}
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"the opt-out", "ab: { rate: 0 }\n", true},
		{"no block", "", true},
		{"a rate above zero", "ab: { rate: 0.05 }\n", false},
		{"challengers", "ab:\n  challengers: [deepseek/chat-x]\n", false},
		{"a judge model", "ab: { judge_model: anthropic/claude-opus-5-5 }\n", false},
		{"a daily pair count", "ab: { max_pairs_per_day: 3 }\n", false},
		{"an exchange rate", "ab:\n  exchange_rate: { pro: 1 }\n", false},
	} {
		t.Run("repo/"+tc.name, func(t *testing.T) {
			text := repoHead + tc.body
			_, ps := Parse([]byte(text))
			if got := len(ps) == 0; got != tc.ok {
				t.Fatalf("the decoder accepted = %v, want %v (problems: %v)", got, tc.ok, ps)
			}
			if got := abSchemaAccepts(t, repoSchema, text); got != tc.ok {
				t.Errorf("fugaro.schema.json accepted = %v, want %v: the schema and the decoder disagree", got, tc.ok)
			}
		})
	}

	// The layer schema is the looser side for a model entry of the right
	// shape that is still no pattern CheckModelPattern accepts. Each sample
	// asserts both halves, so the asymmetry is a pinned fact rather than an
	// omission: the schema cannot refuse these without copying
	// CheckClaudeClaim or the too-broad rule out of internal/config.
	for _, tc := range []struct{ name, text, why string }{
		{"a claim on Claude models", head + "ab:\n  challengers: ['claude-*']\n", "CheckModelPattern refuses a pattern overlapping ClaudePatterns"},
		{"a prefix too broad to name", head + "ab: { judge_model: 'a*' }\n", "CheckModelPattern refuses a one-character prefix"},
	} {
		t.Run("layer/schema looser/"+tc.name, func(t *testing.T) {
			if _, ps := ParseProjectLayer([]byte(tc.text), testAnchor); len(ps) == 0 {
				t.Errorf("the decoder accepted it, want a refusal: %s", tc.why)
			}
			if !abSchemaAccepts(t, layerSchema, tc.text) {
				t.Errorf("project-layer.schema.json refused it, and the schema is the looser side here: %s", tc.why)
			}
		})
	}

	// The repository schema is the stricter side: it refuses the block's
	// other keys outright, while the decoder reads an empty one as a key that
	// says nothing (validateRepoAB complains about a value, not a key). Both
	// halves are asserted, since a repository author meets the schema first.
	for _, tc := range []struct{ name, body string }{
		{"empty challengers", "ab:\n  challengers: []\n"},
		{"an empty judge model", "ab: { judge_model: '' }\n"},
		{"a daily pair count of zero", "ab: { max_pairs_per_day: 0 }\n"},
		{"an empty exchange rate", "ab:\n  exchange_rate: {}\n"},
	} {
		t.Run("repo/schema stricter/"+tc.name, func(t *testing.T) {
			if _, ps := Parse([]byte(repoHead + tc.body)); len(ps) > 0 {
				t.Errorf("the decoder refused it, and an empty key is read as unset: %v", ps)
			}
			if abSchemaAccepts(t, repoSchema, repoHead+tc.body) {
				t.Error("fugaro.schema.json accepted it: a repository may write no key of ab: but rate")
			}
		})
	}
}

// compileABSchema compiles one of this repository's schemas by file name.
func compileABSchema(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", file))
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

// abSchemaAccepts reports whether sch accepts the YAML text as an instance,
// through the JSON value model the validator reads (the way
// schemas/schemas_test.go's yamlInstance does).
func abSchemaAccepts(t *testing.T, sch *jsonschema.Schema, text string) bool {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
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
	return sch.Validate(inst) == nil
}
