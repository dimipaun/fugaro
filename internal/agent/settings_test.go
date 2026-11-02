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
		// Each pin stands alone: no model doesn't drop the others.
		{"no model", "", "m2", 4096, map[string]string{
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "m2", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "4096"}},
		{"nothing", "", "", 0, nil},
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

func TestProviderRouteVars(t *testing.T) {
	want := map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1", "MAX_THINKING_TOKENS": "0"}
	got := ProviderRouteVars()
	if len(got) != len(want) {
		t.Fatalf("ProviderRouteVars = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestWriteManagedSettingsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed-settings.json")
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedSettings(path, map[string]string{"B": "2"}, nil); err != nil {
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
	if err := WriteManagedSettings(path, nil, nil); err == nil {
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
	if err := WriteManagedSettings(path, nil, nil); err == nil {
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
	const realKey = "placeholder-api-key"
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
	if err := WriteManagedSettings(path, vars, DenyRules(true, false)); err != nil {
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
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, DenyRules(true, false)); err != nil {
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
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "permissions") {
		t.Fatalf("pins alone deny tools: %s", data)
	}
}

// denyExtensions are the file kinds the review stage's settings deny a Read
// of: the ones the review input never carries as bytes, and whose bytes
// Claude Code would put into the next Messages body as an image block.
var denyExtensions = []string{"png", "jpg", "jpeg", "gif", "webp", "bmp", "ico", "pdf", "heic", "tiff"}

// TestDenyRulesPerStage: the two parts of the deny list are independent, and
// a review stage's is the web tools plus one Read rule per binary kind. That
// list is what makes the review stage's prompt rule enforced rather than
// merely said: the reviewer runs with --dangerously-skip-permissions and a
// full Read tool, and one Read of a changed PNG is the whole of the failure
// run 20261021-002046-31cc ended on (`images aren't priced for model ...`).
//
// Mutation (return deniedWebTools alone whatever review says): the review
// assertion fails. Mutation (return the binary reads for every stage): the
// non-review assertion fails. Mutation (drop one extension from
// deniedBinaryReads): the list assertion fails. Mutation (add a rule the
// specifier of which is not a Read of a file kind, e.g. "Read(/etc/**)"):
// the list assertion fails.
func TestDenyRulesPerStage(t *testing.T) {
	var reads []string
	for _, ext := range denyExtensions {
		reads = append(reads, "Read(**/*."+ext+")")
	}
	for _, c := range []struct {
		name    string
		denyWeb bool
		review  bool
		want    []string
	}{
		{"implement, fix or plan", true, false, []string{"WebSearch", "WebFetch"}},
		{"a review stage", true, true, append([]string{"WebSearch", "WebFetch"}, reads...)},
		{"a review stage with no gateway", false, true, reads},
		{"neither", false, false, nil},
	} {
		if got := DenyRules(c.denyWeb, c.review); !slices.Equal(got, c.want) {
			t.Errorf("%s: DenyRules(%v, %v) = %v, want %v", c.name, c.denyWeb, c.review, got, c.want)
		}
	}
}

// TestManagedSettingsCarryTheReviewDenyRules: the review stage's settings
// file holds the reads, so the reviewer's own Read tool is refused a PNG
// whatever the prompt says. Nothing here robs the reviewer of the reads it
// needs: a text file's rule is not in the list.
//
// Mutation (write the web tools alone for a review stage): the PNG assertion
// fails.
func TestManagedSettingsCarryTheReviewDenyRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-settings.json")
	if err := WriteManagedSettings(path, map[string]string{"A": "1"}, DenyRules(true, true)); err != nil {
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
	for _, want := range []string{"Read(**/*.png)", "Read(**/*.jpeg)", "Read(**/*.webp)", "WebSearch"} {
		if !slices.Contains(got.Permissions.Deny, want) {
			t.Errorf("the review stage's settings lack %q: %s", want, data)
		}
	}
	for _, unwanted := range []string{"Read(**/*.go)", "Read(**/*.md)", "Read(**/*.svg)"} {
		if slices.Contains(got.Permissions.Deny, unwanted) {
			t.Errorf("the review stage's settings deny %q, which a reviewer reads: %s", unwanted, data)
		}
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

// reviewPostureKeys are the keys of the review stage's launch posture
// (D894(a)). A coding run's stages carry none of them: the promise is that a
// coding run's own review rounds keep byte-for-byte the file they had.
var reviewPostureKeys = []string{"disableAllHooks", "allowManagedHooksOnly", "allowManagedMcpServersOnly", "allowedMcpServers"}

// TestWriteReviewManagedSettings: the review stage's file turns hooks off
// except the managed ones (and the managed file declares none), and allows
// MCP servers from the managed file alone — of which there are none, the
// allow-list being empty. Everything else about the file is unchanged: the
// gateway's env and the stage's deny rules are there as they are for any
// stage.
//
// The two keys this file used to carry and must never carry again are
// asserted absent by name: settingSources is not a settings key at all
// (setting-source selection is --setting-sources, which the stage's argv
// passes, and TestArgs pins it), and enableAllProjectMcpServers blocks
// nothing under `claude -p`, where a project-scoped MCP server loads without
// asking.
//
// Mutation (write the ordinary file for a review stage): every posture
// assertion fails. Mutation (drop one key from reviewPosture): the assertion
// for that key fails. Mutation (put either dead key back): the absent-key
// assertion fails. Mutation (make WriteManagedSettings call reviewPosture
// too): the coding-run assertion fails.
func TestWriteReviewManagedSettings(t *testing.T) {
	dir := t.TempDir()
	review := filepath.Join(dir, "review.json")
	if err := WriteReviewManagedSettings(review, map[string]string{"A": "1"}, DenyRules(true, true)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(review)
	var got struct {
		Env                        map[string]string
		Permissions                struct{ Deny []string }
		DisableAllHooks            *bool
		AllowManagedHooksOnly      *bool
		AllowManagedMcpServersOnly *bool
		AllowedMcpServers          *[]string
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.DisableAllHooks == nil || !*got.DisableAllHooks {
		t.Errorf("hooks are not disabled: %s", data)
	}
	if got.AllowManagedHooksOnly == nil || !*got.AllowManagedHooksOnly {
		t.Errorf("hooks are not restricted to the managed file's: %s", data)
	}
	if got.AllowManagedMcpServersOnly == nil || !*got.AllowManagedMcpServersOnly {
		t.Errorf("MCP servers are not restricted to the managed file's: %s", data)
	}
	if got.AllowedMcpServers == nil {
		t.Errorf("the MCP allow-list is absent, which is not an empty allow-list: %s", data)
	} else if len(*got.AllowedMcpServers) != 0 {
		t.Errorf("the MCP allow-list is not empty: %s", data)
	}
	for _, dead := range []string{`"settingSources"`, `"enableAllProjectMcpServers"`} {
		if strings.Contains(string(data), dead) {
			t.Errorf("the review stage's settings carry %s, which enforces nothing: %s", dead, data)
		}
	}
	if got.Env["A"] != "1" || !slices.Contains(got.Permissions.Deny, "Read(**/*.png)") {
		t.Errorf("the review posture cost the file its env or deny rules: %s", data)
	}

	coding := filepath.Join(dir, "coding.json")
	if err := WriteManagedSettings(coding, map[string]string{"A": "1"}, DenyRules(true, true)); err != nil {
		t.Fatal(err)
	}
	plain, _ := os.ReadFile(coding)
	for _, key := range reviewPostureKeys {
		if strings.Contains(string(plain), key) {
			t.Errorf("a coding run's stage carries %q: %s", key, plain)
		}
	}
}

// TestReviewHome: the review stage's HOME is a directory of its own under the
// run's state directory, empty, and closed to everyone but its owner — the
// user level of Claude Code's settings, which the image's own home would
// otherwise fill.
//
// Mutation (return the state directory itself, or the image's home): the
// distinctness assertion fails. Mutation (leave the mode at 0755): the mode
// assertion fails. Mutation (write anything into it): the emptiness assertion
// fails.
func TestReviewHome(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "task.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	home, err := ReviewHome(state)
	if err != nil {
		t.Fatal(err)
	}
	if home == state || filepath.Dir(home) != state {
		t.Fatalf("home = %s, want a directory under %s", home, state)
	}
	fi, err := os.Stat(home)
	if err != nil || !fi.IsDir() {
		t.Fatalf("home = %s: %v", home, err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the review stage's home holds %v", entries)
	}
	if again, err := ReviewHome(state); err != nil || again != home {
		t.Errorf("a second call = %s, %v, want %s", again, err, home)
	}
}
