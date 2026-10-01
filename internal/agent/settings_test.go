package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPinVars(t *testing.T) {
	cases := []struct {
		name      string
		model, bg string
		maxOutput int64
		want      map[string]string
	}{
		{"model only", "m1", "", 0, map[string]string{
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "m1", "ANTHROPIC_DEFAULT_SONNET_MODEL": "m1", "CLAUDE_CODE_SUBAGENT_MODEL": "m1"}},
		{"with background", "m1", "m2", 0, map[string]string{
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "m1", "ANTHROPIC_DEFAULT_SONNET_MODEL": "m1", "CLAUDE_CODE_SUBAGENT_MODEL": "m1",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "m2"}},
		{"with max output", "m1", "", 4096, map[string]string{
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "m1", "ANTHROPIC_DEFAULT_SONNET_MODEL": "m1", "CLAUDE_CODE_SUBAGENT_MODEL": "m1",
			"CLAUDE_CODE_MAX_OUTPUT_TOKENS": "4096"}},
		{"empty", "", "m2", 4096, nil},
	}
	for _, c := range cases {
		got := PinVars(c.model, c.bg, c.maxOutput)
		if len(got) != len(c.want) {
			t.Errorf("%s: PinVars = %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got[k], v)
			}
		}
	}
}

func TestWriteManagedSettingsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed-settings.json")
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedSettings(path, map[string]string{"B": "2"}, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Env map[string]string }
	if err := json.Unmarshal(data, &got); err != nil || len(got.Env) != 1 || got.Env["B"] != "2" {
		t.Fatalf("settings = %s, %v", data, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", fi.Mode())
	}
	// Nothing is left beside it: the managed directory must hold only the file.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("directory holds %v", entries)
	}
}

func TestWriteManagedSettingsRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "managed-settings.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedSettings(path, nil, false); err == nil {
		t.Fatal("a symlink was replaced")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("the symlink's target was written: %q", b)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedSettings(path, nil, false); err == nil {
		t.Fatal("a directory was replaced")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temporary file is left: %s", e.Name())
		}
	}
}

func TestManagedSettingsHoldNoRealKey(t *testing.T) {
	const realKey = "sk-ant-real-key-123"
	parent := map[string]string{"ANTHROPIC_API_KEY": realKey, "NO_PROXY": "example.invalid"}
	vars, err := GatewayVars("api-key", Gateway{BaseURL: "http://127.0.0.1:4000", Token: "tok-abc"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	vars2 := PinVars("m1", "", 0)
	for k, v := range vars2 {
		vars[k] = v
	}
	path := filepath.Join(t.TempDir(), "managed-settings.json")
	if err := WriteManagedSettings(path, vars, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), realKey) {
		t.Fatalf("the real key is in the managed settings: %s", data)
	}
	if !strings.Contains(string(data), "tok-abc") || !strings.Contains(string(data), "http://127.0.0.1:4000") {
		t.Fatalf("the gateway's URL and token are missing: %s", data)
	}
}

func TestManagedSettingsDenyWebTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-settings.json")
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var got struct {
		Env         map[string]string
		Permissions struct{ Deny []string }
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	slices.Sort(got.Permissions.Deny)
	if !slices.Equal(got.Permissions.Deny, []string{"WebFetch", "WebSearch"}) || got.Env["A"] != "1" {
		t.Fatalf("settings = %s", data)
	}
	// Without the gateway the web tools stay as they were.
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "permissions") {
		t.Fatalf("pins alone deny tools: %s", data)
	}
}

func TestRoutingKeysRefused(t *testing.T) {
	for _, key := range []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_SKIP_VERTEX_AUTH",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	} {
		for _, name := range []string{key, strings.ToLower(key)} {
			got, err := RoutingKeys([]byte(`{"env":{"` + name + `":"x","FOO":"y"}}`))
			if err != nil || !slices.Equal(got, []string{key}) {
				t.Errorf("env %s: RoutingKeys = %v, %v, want [%s]", name, got, err, key)
			}
		}
	}
	got, err := RoutingKeys([]byte(`{"apiKeyHelper":"/bin/sh -c 'echo k'"}`))
	if err != nil || !slices.Equal(got, []string{"apiKeyHelper"}) {
		t.Errorf("apiKeyHelper: %v, %v", got, err)
	}
	got, _ = RoutingKeys([]byte(`{"env":{"ANTHROPIC_BASE_URL":"x","https_proxy":"y"},"apiKeyHelper":"z"}`))
	if !slices.Equal(got, []string{"ANTHROPIC_BASE_URL", "HTTPS_PROXY", "apiKeyHelper"}) {
		t.Errorf("several keys: %v", got)
	}
	// Only the settings' own env counts, not a same-named key elsewhere.
	if got, err := RoutingKeys([]byte(`{"hooks":{"env":{"ANTHROPIC_BASE_URL":"x"}}}`)); err != nil || len(got) != 0 {
		t.Errorf("a nested env elsewhere: %v, %v", got, err)
	}
	for _, bad := range []string{``, `{`, `[]`, `"x"`, `null`} {
		if got, err := RoutingKeys([]byte(bad)); err == nil {
			t.Errorf("RoutingKeys(%q) = %v, want an error", bad, got)
		}
	}
}

func TestRoutingKeysAllowsModelSettings(t *testing.T) {
	got, err := RoutingKeys([]byte(`{
		"env": {"ANTHROPIC_MODEL": "m", "ANTHROPIC_DEFAULT_SONNET_MODEL": "m", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "1"},
		"permissions": {"allow": ["Bash(ls)"], "deny": ["WebFetch"]},
		"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}]},
		"model": "m"}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("RoutingKeys = %v, %v", got, err)
	}
}

func TestSettingsFiles(t *testing.T) {
	work, home, cfg, managedDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	managed := filepath.Join(managedDir, "managed-settings.json")
	for _, f := range []string{"managed-settings.json", "managed-mcp.json"} {
		if err := os.WriteFile(filepath.Join(managedDir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(managedDir, "managed-settings.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := SettingsFiles(work, home, cfg, managed)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(work, ".claude", "settings.json"), filepath.Join(work, ".claude", "settings.local.json"),
		filepath.Join(home, ".claude", "settings.json"), filepath.Join(cfg, "settings.json"),
		filepath.Join(managedDir, "managed-mcp.json"), filepath.Join(managedDir, "managed-settings.d"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("SettingsFiles lacks %s: %v", want, got)
		}
	}
	if slices.Contains(got, managed) || len(got) != 6 {
		t.Errorf("SettingsFiles = %v (the runner's own file must not be listed)", got)
	}
	got, err = SettingsFiles(work, home, "", filepath.Join(t.TempDir(), "absent", "managed-settings.json"))
	if err != nil || len(got) != 3 {
		t.Errorf("no CLAUDE_CONFIG_DIR and no managed directory: %v, %v", got, err)
	}
}

func TestRoutingKeysAnyProviderSwitchAndAllProxy(t *testing.T) {
	for _, k := range []string{"CLAUDE_CODE_USE_MANTLE", "claude_code_use_anything", "ALL_PROXY", "all_proxy"} {
		got, err := RoutingKeys([]byte(`{"env":{"` + k + `":"1"}}`))
		if err != nil || len(got) != 1 || got[0] != strings.ToUpper(k) {
			t.Errorf("%s: %v, %v", k, got, err)
		}
	}
}
