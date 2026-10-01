package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ManagedSettingsPath is where Claude Code reads its managed settings, the
// highest-precedence settings it has: neither a repository's nor a user's
// settings file can override them. The base image creates the directory
// owned by the agent's user, because the runner is not root.
const ManagedSettingsPath = "/etc/claude-code/managed-settings.json"

// deniedWebTools are denied in the managed settings with a gateway: their
// server-side use is billed as input that a request body doesn't bound, so
// the gateway refuses it anyway; denying them keeps the agent from trying.
var deniedWebTools = []string{"WebSearch", "WebFetch"}

type managedSettings struct {
	Env         map[string]string `json:"env"`
	Permissions *struct {
		Deny []string `json:"deny"`
	} `json:"permissions,omitempty"`
}

// WriteManagedSettings replaces path with {"env": env} (and, with denyWeb,
// a permissions rule denying the web tools), mode 0644, atomically: it
// writes a file beside it, syncs it and renames it over path. It refuses a
// symlink or a file that isn't regular at path.
func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {
	if fi, err := os.Lstat(path); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%s); refusing to replace it", path, fi.Mode().Type())
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	s := managedSettings{Env: env}
	if s.Env == nil {
		s.Env = map[string]string{}
	}
	if denyWeb {
		s.Permissions = &struct {
			Deny []string `json:"deny"`
		}{Deny: deniedWebTools}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".managed-settings-*.tmp")
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	done = true
	return nil
}

// routingEnvKeys are the settings env variables that would send Claude
// Code's model calls, or its credentials, somewhere other than Fugaro's
// gateway. They are compared in upper case, so no_proxy is covered too.
var routingEnvKeys = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL",
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_SKIP_VERTEX_AUTH", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
}

// RoutingKeys lists what a settings file sets that would reroute Claude
// Code: "apiKeyHelper", and the env keys in routingEnvKeys, in any case,
// reported in upper case. It returns an error for a file that isn't a JSON
// object, because then nothing can be said about what Claude Code makes of it.
func RoutingKeys(settings []byte) ([]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(settings, &top); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if top == nil {
		return nil, fmt.Errorf("not a JSON object: null")
	}
	var found []string
	for k := range top {
		if strings.EqualFold(k, "apiKeyHelper") {
			found = append(found, "apiKeyHelper")
		}
	}
	for k, raw := range top {
		if k != "env" {
			continue
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(raw, &env); err != nil {
			continue // not an object: Claude Code has no env to apply
		}
		for name := range env {
			if up := strings.ToUpper(name); slices.Contains(routingEnvKeys, up) && !slices.Contains(found, up) {
				found = append(found, up)
			}
		}
	}
	slices.Sort(found)
	return found, nil
}

// SettingsFiles are the places a settings file that RoutingKeys must check
// can be: <workdir>/.claude/settings.json and settings.local.json,
// $HOME/.claude/settings.json, $CLAUDE_CONFIG_DIR/settings.json when that
// is set, and every entry of dir(managedPath) other than managedPath itself
// (a managed-mcp.json, a managed-settings.d/ directory, anything). Files
// that don't exist are listed too; the caller skips them.
func SettingsFiles(workdir, home, claudeConfigDir, managedPath string) ([]string, error) {
	files := []string{
		filepath.Join(workdir, ".claude", "settings.json"),
		filepath.Join(workdir, ".claude", "settings.local.json"),
	}
	if home != "" {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	if claudeConfigDir != "" {
		files = append(files, filepath.Join(claudeConfigDir, "settings.json"))
	}
	dir := filepath.Dir(managedPath)
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil:
		for _, e := range entries {
			if p := filepath.Join(dir, e.Name()); p != filepath.Clean(managedPath) {
				files = append(files, p)
			}
		}
	case os.IsNotExist(err):
	default:
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	return files, nil
}
