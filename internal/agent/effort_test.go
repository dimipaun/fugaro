package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestEffortVarsClaudeOnly(t *testing.T) {
	for _, e := range config.Efforts {
		got, err := EffortVars(e, false)
		if err != nil {
			t.Errorf("EffortVars(%q, false): %v", e, err)
			continue
		}
		if len(got) != 1 || got["CLAUDE_CODE_EFFORT_LEVEL"] != e {
			t.Errorf("EffortVars(%q, false) = %v, want CLAUDE_CODE_EFFORT_LEVEL=%q alone", e, got, e)
		}
	}
	got, err := EffortVars("", false)
	if err != nil {
		t.Fatalf("EffortVars(\"\", false): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("EffortVars(\"\", false) = %v, want nothing: an absent effort leaves the model's own default", got)
	}
}

func TestEffortVarsNeverOnProviderStage(t *testing.T) {
	for _, e := range config.Efforts {
		got, err := EffortVars(e, true)
		if err != nil {
			t.Errorf("EffortVars(%q, true): %v", e, err)
			continue
		}
		if len(got) != 0 {
			t.Errorf("EffortVars(%q, true) = %v, want nothing: a provider route sends no effort (G5)", e, got)
		}
	}
	// A provider stage's variables are applied beside ProviderRouteVars', and
	// G5's MAX_THINKING_TOKENS=0 has to survive them.
	for _, e := range config.Efforts {
		stage := ProviderRouteVars()
		vars, err := EffortVars(e, true)
		if err != nil {
			t.Fatalf("EffortVars(%q, true): %v", e, err)
		}
		for k, v := range vars {
			stage[k] = v
		}
		if stage["MAX_THINKING_TOKENS"] != "0" || stage["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"] != "1" {
			t.Errorf("EffortVars(%q, true) over ProviderRouteVars() = %v, want MAX_THINKING_TOKENS=0 and CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1 kept", e, stage)
		}
		if _, ok := vars["MAX_THINKING_TOKENS"]; ok {
			t.Errorf("EffortVars(%q, true) sets MAX_THINKING_TOKENS: the stage's thinking-off value is ProviderRouteVars' to set", e)
		}
	}
}

func TestEffortVarsRefusesUnknown(t *testing.T) {
	for _, bad := range []string{"none", "auto", "unset", "MAX", "High", " medium", "low ", "ultra"} {
		for _, provider := range []bool{false, true} {
			got, err := EffortVars(bad, provider)
			if err == nil {
				t.Errorf("EffortVars(%q, %v) = %v, want an error", bad, provider, got)
				continue
			}
			if !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), strings.Join(config.Efforts, ", ")) {
				t.Errorf("EffortVars(%q, %v) error %q, want it to name the value and the closed set", bad, provider, err)
			}
		}
	}
}

func TestEffortVarsSetsTheVariable(t *testing.T) {
	got, err := EffortVars("xhigh", false)
	if err != nil {
		t.Fatalf("EffortVars(\"xhigh\", false): %v", err)
	}
	want := map[string]string{"CLAUDE_CODE_EFFORT_LEVEL": "xhigh"}
	if len(got) != len(want) {
		t.Fatalf("EffortVars = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestManagedSettingsCarryNoEffortCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-settings.json")
	env := map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}
	if err := WriteManagedSettings(path, env, DenyRules(true, true)); err != nil {
		t.Fatalf("WriteManagedSettings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("the managed settings are not a JSON object: %v", err)
	}
	var keys []string
	for k := range top {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"env", "permissions"}) {
		t.Fatalf("managed settings keys = %v, want env and permissions", keys)
	}
	for _, cap := range []string{"maxEffortLevel", "effortLevel"} {
		if strings.Contains(string(data), cap) {
			t.Errorf("the managed settings carry %s: a cap there would clamp the level Fugaro set, silently", cap)
		}
	}
}
