package pluginwire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Notices are the other things a project's settings file says, one line each,
// for the diff screen and doctor: wiring the plugin blesses the whole file,
// and the file is the repository's, so what else an agent obeys because of it
// is shown, never judged. settings is the file's path (the project's
// .claude/settings.json) and data its content; the directories beside it,
// .claude/skills and .claude/commands, are looked at for names that shadow
// Fugaro's own (such as the retired /fugaro-setup). An unreadable or invalid
// file has no notices here (Plan reports it). No value is echoed: names only,
// and what is printed is cut and made safe for a terminal.
func Notices(settings string, data []byte) []string {
	var out []string
	if len(strings.TrimSpace(string(data))) > 0 {
		if top, err := parseObject(data); err == nil {
			out = append(out, settingsNotices(top)...)
		}
	}
	out = append(out, shadowNotices(filepath.Dir(settings))...)
	return append(out, mcpNotices(filepath.Dir(filepath.Dir(settings)))...)
}

// mcpNotices notes a repository-level .mcp.json: MCP servers it declares are
// commands or services an agent calls, and its content is not read here.
func mcpNotices(root string) []string {
	if fi, err := os.Lstat(filepath.Join(root, ".mcp.json")); err == nil && !fi.IsDir() {
		return []string{"the repository has a .mcp.json, which declares MCP servers (commands or services Claude Code can run or call): read it before trusting them"}
	}
	return nil
}

// NoticesCaveat goes with the notices wherever they are shown: the list is
// what Fugaro knows to look for, so a short one (or none) is no assurance.
const NoticesCaveat = "this list is not exhaustive: read the file yourself"

func settingsNotices(top object) []string {
	var out []string
	nonEmpty := func(raw json.RawMessage) bool {
		switch strings.TrimSpace(string(raw)) {
		case "", "null", "{}", "[]", "false", `""`:
			return false
		}
		return true
	}
	if raw, ok := top.get("hooks"); ok && nonEmpty(raw) {
		out = append(out, "the file defines hooks: commands that run on every teammate's machine when Claude Code starts or uses a tool")
	}
	if perms, _, err := objectAt(top, "permissions"); err == nil {
		if raw, ok := perms.get("allow"); ok && nonEmpty(raw) {
			var allow []string
			_ = json.Unmarshal(raw, &allow)
			msg := "permissions.allow pre-approves tools without asking"
			if slices.ContainsFunc(allow, func(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "Bash") }) {
				msg += ", Bash (commands) among them"
			}
			out = append(out, msg)
		}
		if raw, ok := perms.get("defaultMode"); ok && strings.TrimSpace(string(raw)) == `"bypassPermissions"` {
			out = append(out, "permissions.defaultMode is bypassPermissions: tools run without asking")
		}
	}
	if raw, ok := top.get("apiKeyHelper"); ok && nonEmpty(raw) {
		out = append(out, "apiKeyHelper is set: a command that supplies the API key")
	}
	for _, k := range []struct{ key, msg string }{
		{"statusLine", "statusLine is set: a command that runs while Claude Code is open"},
		{"enabledMcpjsonServers", "enabledMcpjsonServers pre-approves MCP servers the repository declares"},
		{"awsAuthRefresh", "awsAuthRefresh is set: a command that runs to refresh AWS credentials"},
		{"awsCredentialExport", "awsCredentialExport is set: a command that supplies AWS credentials"},
		{"otelHeadersHelper", "otelHeadersHelper is set: a command that supplies telemetry headers"},
	} {
		if raw, ok := top.get(k.key); ok && nonEmpty(raw) {
			out = append(out, k.msg)
		}
	}
	if sb, _, err := objectAt(top, "sandbox"); err == nil {
		if raw, ok := sb.get("excludedCommands"); ok && nonEmpty(raw) {
			out = append(out, "sandbox.excludedCommands names commands that run outside the sandbox")
		}
		if raw, ok := sb.get("allowUnsandboxedCommands"); ok && strings.TrimSpace(string(raw)) == "true" {
			out = append(out, "sandbox.allowUnsandboxedCommands is true: a command may be run outside the sandbox")
		}
	}
	if raw, ok := top.get("enableAllProjectMcpServers"); ok && strings.TrimSpace(string(raw)) == "true" {
		out = append(out, "enableAllProjectMcpServers is true: every MCP server the repository declares is trusted without asking")
	}
	if raw, ok := top.get("env"); ok && nonEmpty(raw) {
		out = append(out, "env sets environment variables for every session")
	}
	if markets, _, err := objectAt(top, "extraKnownMarketplaces"); err == nil {
		if names := otherKeys(markets, Marketplace); len(names) > 0 {
			out = append(out, "other marketplaces are added: "+names2(names))
		}
	}
	if plugins, _, err := objectAt(top, "enabledPlugins"); err == nil {
		if names := otherKeys(plugins, PluginID); len(names) > 0 {
			out = append(out, "other plugins are enabled: "+names2(names))
		}
	}
	return out
}

func otherKeys(o object, own string) []string {
	var out []string
	for _, m := range o {
		if m.key != own {
			out = append(out, Printable(m.key))
		}
	}
	return out
}

// names2 is up to five names, then a count.
func names2(names []string) string {
	if len(names) > 5 {
		return strings.Join(names[:5], ", ") + fmt.Sprintf(" and %d more", len(names)-5)
	}
	return strings.Join(names, ", ")
}

// shadowNotices lists repository-local skills and commands whose name begins
// with fugaro: they sit where Fugaro's own (and the retired /fugaro-setup)
// would be looked for, and are text an agent obeys too.
func shadowNotices(claudeDir string) []string {
	var out []string
	for _, sub := range []string{"skills", "commands"} {
		entries, err := os.ReadDir(filepath.Join(claudeDir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(strings.ToLower(e.Name()), "fugaro") {
				out = append(out, fmt.Sprintf("the repository has its own .claude/%s/%s, which shadows a Fugaro name (the retired /fugaro-setup included): read it before trusting it", sub, Printable(e.Name())))
			}
		}
	}
	return out
}

// NoticesFor reads the settings file at path (never following a link, never
// more than a settings file's size) and returns its Notices; nil for a file
// that is missing or cannot be read.
func NoticesFor(path string) []string {
	if checkPath(path) != nil {
		return nil
	}
	data, _, _, err := readFile(path, maxSettingsBytes)
	if err != nil {
		return nil
	}
	return Notices(path, data)
}

// NoticeText is the change's notices for the diff screen, one "heads-up" line
// each, then the caveat that the list is not exhaustive (always: a clean
// screen must not read as assurance).
func (c *Change) NoticeText() string {
	var b strings.Builder
	for _, n := range c.Notices {
		b.WriteString("heads-up: " + n + "\n")
	}
	b.WriteString("heads-up: " + NoticesCaveat + "\n")
	return b.String()
}
