package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// The budget mode's vocabulary at the repository's door (AB2 of
// docs/always-a-budget/implementation-plan.md): two modes, the word that was
// one until this release refused with the one text every door refuses it with,
// and an unset mode read as observe rather than as off.

// TestOffIsRefusedWithTheFix: a repository's fugaro.yaml that says the word
// that was a budget mode is rejected at once, and the refusal is the fix
// (D3022): what the word is not any more, what replaced it, and what to write
// instead. The word is not quietly read as unset — D3022 is the answer to the
// recommendation that it be — so this is a file its author has to fix before
// its runs start, and the schema refuses the same file's mode (the test below)
// while policy.ValidMode is what refuses it in the decoder.
//
// Mutation (run, restored): let ValidMode rank the word again — the decoder
// accepts the file and this test's first case fails on the problems it did not
// report.
func TestOffIsRefusedWithTheFix(t *testing.T) {
	if policy.ValidMode("off") {
		t.Fatal(`policy.ValidMode("off") = true: the word is still a mode this release accepts`)
	}
	for _, m := range []string{policy.ModeObserve, policy.ModeEnforce} {
		if !policy.ValidMode(m) {
			t.Errorf("policy.ValidMode(%q) = false: it is one of the two modes", m)
		}
	}

	_, ps := Parse([]byte(minimalYAML + "budget:\n  mode: off\n  per_run_usd: 5\n"))
	if len(ps) == 0 {
		t.Fatal("a fugaro.yaml that says the word was accepted: the mode is not a mode any more (D3022)")
	}
	got := problemsText(ps)
	if !strings.Contains(got, "budget.mode") {
		t.Errorf("the refusal is not at the key that was written: %q", got)
	}
	if !strings.Contains(got, policy.ModeOffRefusal) {
		t.Errorf("the refusal does not carry the one text every door refuses the word with (%q): %q", policy.ModeOffRefusal, got)
	}
	// The fix is in the text, and it is the two modes and not the word again.
	for _, want := range []string{policy.ModeObserve, policy.ModeEnforce} {
		if !strings.Contains(policy.ModeOffRefusal, want) {
			t.Errorf("the refusal does not offer %q as the fix: %q", want, policy.ModeOffRefusal)
		}
	}

	// A word that was never a mode is refused too, in its own words: the two
	// refusals are not the same sentence, and neither lands on observe.
	_, ps = Parse([]byte(minimalYAML + "budget:\n  mode: sometimes\n"))
	if got := problemsText(ps); !strings.Contains(got, "budget.mode") || strings.Contains(got, policy.ModeOffRefusal) {
		t.Errorf("an unknown mode: problems = %q, want the key and not the removed word's refusal", got)
	}
}

// TestTheRepositorySchemaListsTwoModes: the schema a repository's CI may check
// its file against lists the two modes and refuses the word with it (D3022,
// F14). The file's own decoder is the stricter surface of the two — it is what
// a run is judged by — and the two must not disagree: a mode the schema admits
// and the decoder refuses is a green check on a file whose runs stop.
func TestTheRepositorySchemaListsTwoModes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "fugaro.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Budget struct {
				Properties struct {
					Mode struct {
						Enum []string `json:"enum"`
					} `json:"mode"`
				} `json:"properties"`
			} `json:"budget"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	enum := doc.Properties.Budget.Properties.Mode.Enum
	if len(enum) == 0 {
		t.Fatal("schemas/fugaro.schema.json has no budget.mode enum: this scan no longer reads the key")
	}
	for _, want := range []string{policy.ModeObserve, policy.ModeEnforce} {
		if !slices.Contains(enum, want) {
			t.Errorf("budget.mode: the enum is %v and does not list %q", enum, want)
		}
	}
	if slices.Contains(enum, "off") {
		t.Errorf("budget.mode: the enum is %v and still lists the word that is not a mode any more (D3022)", enum)
	}

	// Both surfaces, over the same file: the schema refuses a word the decoder
	// refuses, and admits the two modes the decoder admits.
	sch := compileABSchema(t, "fugaro.schema.json")
	head := minimalRepo + "git: { provider: github }\nworkflows:\n  default:\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct {
		mode string
		ok   bool
	}{
		{"observe", true},
		{"enforce", true},
		{"off", false},
		{"sometimes", false},
	} {
		text := head + "budget: { mode: " + tc.mode + " }\n"
		if got := abSchemaAccepts(t, sch, text); got != tc.ok {
			t.Errorf("mode %q: the schema accepted = %v, want %v", tc.mode, got, tc.ok)
		}
		if _, ps := Parse([]byte(text)); (len(ps) == 0) != tc.ok {
			t.Errorf("mode %q: the decoder accepted = %v, want %v (problems: %s)", tc.mode, len(ps) == 0, tc.ok, problemsText(ps))
		}
	}
}

// TestAnEmptyModeIsNotOffForTheProviderCheck: a run that writes no budget mode
// resolves observe, so the provider rule judges it as observe and not as the
// off it used to read (AB2, R3, D3010): the refusal names the mode it found
// and what raising it takes, and it is the same sentence an installation that
// writes observe gets. Saying off was the other reading this release ends —
// there is always a budget — so the two must not differ.
//
// Mutation (run, restored): answer the empty mode with the sentence it had for
// off — the two messages differ and the test fails on the one it read.
func TestAnEmptyModeIsNotOffForTheProviderCheck(t *testing.T) {
	a := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash"}}
	ps := providers(nil)

	empty := problemsText(CheckProviderGateway(a, "", ps, ""))
	if empty == "" {
		t.Fatal("an unset mode carried a provider model through: it is observe, and observe never refuses a call")
	}
	if got := problemsText(CheckProviderGateway(a, "", ps, policy.ModeObserve)); got != empty {
		t.Errorf("an unset mode is not judged as observe:\nunset:   %q\nobserve: %q", empty, got)
	}
	if got := problemsText(CheckProviderGateway(a, "", ps, "off")); got != empty {
		t.Errorf("the removed word is not read as observe:\nword:  %q\nunset: %q", got, empty)
	}
	if !strings.Contains(empty, "agent.models.coder") || !strings.Contains(empty, policy.ModeEnforce) {
		t.Errorf("the refusal names neither the pin nor the mode to raise it to: %q", empty)
	}
	if got := problemsText(CheckProviderGateway(a, "", ps, policy.ModeEnforce)); got != "" {
		t.Errorf("enforce refused a provider model: %q", got)
	}
}
