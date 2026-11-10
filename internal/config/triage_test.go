package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// The triage stage's config surface (docs/post-review-triage/implementation-plan.md
// T-TR5, D1851, D1854): the stage name, the switch that turns it off, and
// where the switch may be read from.

// triageHead is a repository file whose agent: block the cases below extend,
// and triageTail the workflows every fugaro.yaml needs.
const (
	triageHead = minimalRepo + "git: { provider: github }\n"
	triageTail = "workflows:\n  app:\n    commands: { build: make, test: make test }\n"
)

// TestTriageIsARunStage: the triage stage spends a model call — it is the
// judge that weighs a review round's findings (D1851, D1848) — so it is one of
// config.RunStages, and `stages.triage.claude_account` is a key an owner may
// write. A stage outside the list is refused rather than silently dropped
// (CheckStageSegment), which is why the name has to land here and not only in
// the runner.
func TestTriageIsARunStage(t *testing.T) {
	if !slices.Contains(RunStages, "triage") {
		t.Fatalf("RunStages = %v, want triage among them: the stage spends a model call, so a stage account may be named for it", RunStages)
	}
	if !IsRunStage("triage") {
		t.Error("IsRunStage(\"triage\") = false")
	}
	if ps := CheckStageSegment("triage", "stages"); ps != nil {
		t.Errorf("CheckStageSegment(triage) = %+v, want none", ps)
	}
}

// TestTriageIsAMixedAccountStage: the stage runs on the senior reviewer's
// model and account (D1848), so under api-key it is one of the stages the
// run's single Claude account serves, beside the senior review and the
// adversarial review (docs/design/mixed-credentials.md §11, "An allow-list,
// not a denylist"). A coder's stage is still refused, so the list did not
// widen into "any stage".
func TestTriageIsAMixedAccountStage(t *testing.T) {
	if !slices.Contains(MixedAccountStages, "triage") {
		t.Fatalf("MixedAccountStages = %v, want triage among them: the stage runs on the senior reviewer's model and account (D1848)", MixedAccountStages)
	}
	if ps := CheckStageAccountMix("api-key", map[string]string{"triage": "acme-overflow"}, "stages"); len(ps) != 0 {
		t.Errorf("CheckStageAccountMix(api-key, an account for triage) = %+v, want none", ps)
	}
	if ps := CheckStageAccountMix("api-key", map[string]string{"implement": "acme-overflow"}, "stages"); len(ps) == 0 {
		t.Error("CheckStageAccountMix admitted an account for the implement stage: the coder's stages keep the provider key the gateway holds")
	}
	// The two stages the list already named keep their places, and triage
	// comes last of the three: the launch reads the list in order to decide
	// which account picks the run's job (internal/cli's mixedRunAccount), so a
	// name that sorted ahead of review would change the job a mixed run starts
	// on for a repository that names no review account.
	for i, want := range []string{"review", "adversarial", "triage"} {
		if i >= len(MixedAccountStages) || MixedAccountStages[i] != want {
			t.Fatalf("MixedAccountStages = %v, want %v", MixedAccountStages, []string{"review", "adversarial", "triage"})
		}
	}
}

// TestAgentTriageDefaultsOnOnlyInStartupAndPro is D1851 as the key's own
// default: with nothing set the stage runs in Startup and Pro, and no mode but
// those two makes a triage call at all — POC and Critical are byte-identical
// to the releases before this one whatever the key says, and Hotfix's table
// blocks only findings the judge may not move (F28). A written key can only
// take the stage away: there is no mode a key turns the stage on in.
func TestAgentTriageDefaultsOnOnlyInStartupAndPro(t *testing.T) {
	var absent Agent
	for _, mode := range rigor.Modes {
		want := mode == rigor.Startup || mode == rigor.Pro
		if got := absent.TriageOn(mode); got != want {
			t.Errorf("an absent key: TriageOn(%s) = %v, want %v", mode, got, want)
		}
	}
	off, on := false, true
	absent.Triage = &off
	if absent.TriageOn(rigor.Startup) || absent.TriageOn(rigor.Pro) {
		t.Error("agent.triage: false: the stage still runs in Startup and Pro")
	}
	absent.Triage = &on
	for _, mode := range []rigor.Mode{rigor.POC, rigor.Critical, rigor.Hotfix} {
		if absent.TriageOn(mode) {
			t.Errorf("agent.triage: true: TriageOn(%s) = true, and no mode but Startup and Pro makes a triage call (D1851)", mode)
		}
	}
}

// TestConfigRefusesUnknownTriageKey: the key is modelled, so the strict
// decoder admits the two values a switch has and refuses everything else —
// a value of another type, and a key nobody models, which is the refusal a
// repository that wrote the key before this release's binary would meet.
func TestConfigRefusesUnknownTriageKey(t *testing.T) {
	for name, body := range map[string]string{
		"the stage off":  "  triage: false\n",
		"the stage on":   "  triage: true\n",
		"nothing at all": "",
	} {
		if _, ps := Parse([]byte(triageHead + "agent:\n" + body + triageTail)); len(ps) != 0 {
			t.Errorf("%s: Parse = %+v, want none", name, ps)
		}
	}
	for name, body := range map[string]string{
		"a word where the switch is":   "  triage: nope\n",
		"a number where the switch is": "  triage: 1\n",
		"a misspelt key":               "  triage_off: true\n",
		"the key outside agent":        "triage: true\n",
	} {
		if _, ps := Parse([]byte(triageHead + "agent:\n" + body + triageTail)); len(ps) == 0 {
			t.Errorf("%s: Parse accepted %q, want the strict decoder's refusal", name, body)
		}
	}
}

// TestAgentTriageIsReadFromTheBaseNotTheCheckout: the switch is a key of the
// repository's own fugaro.yaml, so its value is that file's and nothing else —
// and the file a run reads is the base branch's (D25, the read baseRigor makes
// for the mode). The layer in the cases below is the task's own
// (task.Spec.ProjectLayer, the object the launcher writes into the task), which
// is the one place a checkout's own answer could reach a run without being
// read from a branch at all: the answer follows the file's bytes in both
// directions while that layer is in place, so what a task carries cannot move
// it.
func TestAgentTriageIsReadFromTheBaseNotTheCheckout(t *testing.T) {
	l := mustLayer(t, layerHead+"defaults:\n  agent:\n    review_rounds: 3\n")
	for name, tc := range map[string]struct {
		text string
		want bool
	}{
		"the base's file turned it off": {triageHead + "agent:\n  triage: false\n" + triageTail, false},
		"the base's file turned it on":  {triageHead + "agent:\n  triage: true\n" + triageTail, true},
		"the base's file says neither":  {triageHead + triageTail, true},
	} {
		c, _, ps := Resolve([]byte(tc.text), l)
		if len(ps) > 0 {
			t.Fatalf("%s: %+v", name, ps)
		}
		if got := c.Agent.TriageOn(rigor.Startup); got != tc.want {
			t.Errorf("%s: TriageOn(startup) = %v, want %v: the switch is the file the caller read", name, got, tc.want)
		}
	}
}

// TestTaskEmbeddedLayerCannotReenableTriage is T-TR5's red step, and the trust
// boundary D1854 draws: a launcher writes the task, and the task's own project
// layer travels with it (task.Spec.ProjectLayer), so a layer that could name
// this switch would be a second home for it — reachable from whatever revision
// the launcher read, including the checkout under review. A run that read the
// switch there could have the stage put back after the base branch turned it
// off, which is the one direction the switch must never move.
//
// The key is therefore not a layer key at all: config.Scopes gives it InRepo
// and nothing else (the refusal agent.compression carries, for the same
// reason), so the project layer refuses defaults.agent.triage in every
// repository of the project. The other half of the answer is the owner's
// published policy node — policy.Effective.TriageOff, merged tighter-only
// (internal/policy's Merge) and read from the base beside this key — which a
// launcher can write no more than it can write this file.
func TestTaskEmbeddedLayerCannotReenableTriage(t *testing.T) {
	for name, body := range map[string]string{
		"on":   "    triage: true\n",
		"off":  "    triage: false\n",
		"null": "    triage:\n",
	} {
		if _, ps := ParseProjectLayer([]byte(layerHead+"defaults:\n  agent:\n"+body), testAnchor); len(ps) == 0 {
			t.Errorf("the project layer accepted defaults.agent.triage written %s: a layer a launcher writes is not a home for the switch, whatever it says", name)
		}
	}
	found := false
	for _, s := range Scopes {
		if s.Key != "agent.triage" {
			continue
		}
		found = true
		if s.In != InRepo {
			t.Errorf("config.Scopes: agent.triage is set in %b, want InRepo alone", s.In)
		}
		if s.Why == "" {
			t.Error("config.Scopes: agent.triage carries no why")
		}
	}
	if !found {
		t.Error("config.Scopes has no agent.triage row: the key is unknown to every reader that judges a layer")
	}
}

// TestTriageKeyInSchemas holds the published schemas to the decoder, the way
// TestStopFractionSchemaAgreesOnZero holds the stop fraction's: fugaro.yaml is
// read by two surfaces — the strict decoder a run uses and the schema an
// editor or a repository's CI checks the file against — and a value one admits
// while the other refuses is a red check nobody can reproduce.
//
// The settings schema's own half is the registry's keyDefaults, one entry per
// registry row, which schemas' TestSettingsSchema holds and which the registry
// derives for the two keys this change adds (agent.triage, and the stage's own
// account through config.RunStages — stageAccountRows).
func TestTriageKeyInSchemas(t *testing.T) {
	sch := compileABSchema(t, "fugaro.schema.json")
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"the stage off":                {"  triage: false\n", true},
		"the stage on":                 {"  triage: true\n", true},
		"a word where the switch is":   {"  triage: nope\n", false},
		"a number where the switch is": {"  triage: 0\n", false},
	} {
		text := triageHead + "agent:\n" + tc.body + triageTail
		if ps := mustParse(t, text); (len(ps) == 0) != tc.ok {
			t.Fatalf("%s: the decoder accepted = %v, want %v (%+v)", name, len(ps) == 0, tc.ok, ps)
		}
		if got := abSchemaAccepts(t, sch, text); got != tc.ok {
			t.Errorf("%s: fugaro.schema.json accepted = %v, want %v: the schema and the decoder disagree", name, got, tc.ok)
		}
	}
	// The schema's own node, and the release its sentence names: a binary
	// before that release refuses the key whatever it says, which is what the
	// launcher's gate (internal/cli's triageSince) judges a job image on.
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "fugaro.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	node := agentProperty(t, doc, "triage")
	if node["type"] != "boolean" {
		t.Errorf("fugaro.schema.json's agent.triage is %v, want a boolean", node)
	}
	if d, _ := node["description"].(string); !strings.Contains(d, "Needs fugaro "+triageShipsIn) {
		t.Errorf("fugaro.schema.json's agent.triage does not say \"Needs fugaro %s\": %q", triageShipsIn, d)
	}
}

// triageShipsIn is the release the key ships in, pinned here for the schema's
// sentence alone. internal/cli's triageSince is the constant the gate reads
// and the one its own test compares against the release actually cut; this
// copy is the second reader's, and the two have to name the same release.
const triageShipsIn = "0.22.0"

// agentProperty is props."agent".properties[name] of a schema document, for a
// test that has to read the node rather than validate an instance.
func agentProperty(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := doc["properties"].(map[string]any)
	agent, _ := props["agent"].(map[string]any)
	agentProps, _ := agent["properties"].(map[string]any)
	node, ok := agentProps[name].(map[string]any)
	if !ok {
		t.Fatalf("fugaro.schema.json's agent has no %s property", name)
	}
	return node
}

// mustParse is Parse for a test that knows the file it wrote is well formed.
func mustParse(t *testing.T, text string) []Problem {
	t.Helper()
	_, ps := Parse([]byte(text))
	return ps
}
