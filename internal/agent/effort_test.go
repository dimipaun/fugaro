package agent

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestEffortVarsClaudeOnly(t *testing.T) {
	for _, e := range config.Efforts {
		got := mustEffortVars(t, e, false)
		if len(got) != 1 {
			t.Errorf("EffortVars(%q, false) = %v, want one variable", e, got)
			continue
		}
		for k, v := range got {
			if v != e {
				t.Errorf("EffortVars(%q, false) sets %s=%q, want %q", e, k, v, e)
			}
		}
	}
	if got := mustEffortVars(t, "", false); len(got) != 0 {
		t.Errorf("EffortVars(\"\", false) = %v, want nothing: an absent effort leaves the model's own default", got)
	}
}

func TestEffortVarsNeverOnProviderStage(t *testing.T) {
	for _, e := range config.Efforts {
		if got := mustEffortVars(t, e, true); len(got) != 0 {
			t.Errorf("EffortVars(%q, true) = %v, want nothing: a provider route sends no effort (G5)", e, got)
		}
	}
	// G5's other half: a stage applies these beside ProviderRouteVars', so an
	// empty result is what leaves MAX_THINKING_TOKENS=0 standing rather than
	// replacing it. Asserted on the composition, since that is where a
	// non-empty result would do its damage.
	stage := ProviderRouteVars()
	for k, v := range mustEffortVars(t, "max", true) {
		stage[k] = v
	}
	if want := ProviderRouteVars(); !maps.Equal(stage, want) {
		t.Errorf("ProviderRouteVars() with EffortVars(max, true) merged = %v, want %v unchanged", stage, want)
	}
}

func mustEffortVars(t *testing.T, effort string, provider bool) map[string]string {
	t.Helper()
	vars, err := EffortVars(effort, provider)
	if err != nil {
		t.Fatalf("EffortVars(%q, %v): %v", effort, provider, err)
	}
	return vars
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
	// The one test that writes the variable's name literally: the other tests
	// read whatever key came back, so a rename would slip past them.
	want := map[string]string{"CLAUDE_CODE_EFFORT_LEVEL": "xhigh"}
	got := mustEffortVars(t, "xhigh", false)
	if len(got) != len(want) {
		t.Fatalf("EffortVars(\"xhigh\", false) = %v, want %v", got, want)
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
