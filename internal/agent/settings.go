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

// ManagedSettingsTmpPattern names the temporary file WriteManagedSettings
// writes beside the settings; a crash can leave one behind.
const ManagedSettingsTmpPattern = ".managed-settings-*.tmp"

// IsManagedSettingsTmp reports whether name is such a leftover.
func IsManagedSettingsTmp(name string) bool {
	ok, _ := filepath.Match(ManagedSettingsTmpPattern, name)
	return ok
}

// deniedWebTools are denied in the managed settings with a gateway: their
// server-side use is billed as input that a request body doesn't bound, so
// the gateway refuses it anyway; denying them keeps the agent from trying.
var deniedWebTools = []string{"WebSearch", "WebFetch"}

// deniedBinaryReads are denied in a review stage's managed settings: reads
// of the file kinds the review input never carries as bytes, one rule per
// extension rather than a brace pattern, since a rule's specifier is a path
// pattern Claude Code matches and a list of plain extensions is what this
// settings shape is known to take.
//
// The review stage's prompt already says so — reviewinput.go's binaryRule,
// carried by the built input and by PickPrompt — but a prompt is not a
// permission. The reviewer runs with --dangerously-skip-permissions and a
// full Read tool, and one Read of a changed PNG is enough: Claude Code puts
// what it read into the next Messages body as a base64 image block, and the
// gateway refuses an image for a model with no image rate
// (internal/gateway/forward.go), which a model priced by the project's
// model_prices never has. Run 20261021-002046-31cc ended exactly there —
// `stage review: images aren't priced for model deepseek/deepseek-v4.1-flash`
// — after its pull request added two PNGs, which is the failure the built
// review input fixes on the builder's side and this rule fixes on the
// reviewer's side. These kinds are the ones the rule is for; a text file the
// reviewer wants to read is not one of them.
var deniedBinaryReads = []string{
	"Read(**/*.png)", "Read(**/*.jpg)", "Read(**/*.jpeg)", "Read(**/*.gif)",
	"Read(**/*.webp)", "Read(**/*.bmp)", "Read(**/*.ico)", "Read(**/*.pdf)",
	"Read(**/*.heic)", "Read(**/*.tiff)",
}

// DenyRules is the permissions deny list a stage's managed settings carry:
// the web tools when denyWeb, and the review stage's binary reads when
// review. The two are independent, and how a stage is recognised as a review
// stage is the caller's business (the runner's config.IsReviewStage): this
// package knows what a stage may not do, not what a stage is.
func DenyRules(denyWeb, review bool) []string {
	var deny []string
	if denyWeb {
		deny = append(deny, deniedWebTools...)
	}
	if review {
		deny = append(deny, deniedBinaryReads...)
	}
	return deny
}

// ReviewHomeName is the directory, under the run's state directory, that a
// review stage's session uses as HOME.
const ReviewHomeName = "review-home"

// reviewSettingSources are the settings levels a review stage's session
// reads: the user's (which ReviewHome makes an empty directory of its own)
// and the managed file. The project level is the reviewed pull request's own
// checkout, which is exactly what the reviewer must not be configured by.
var reviewSettingSources = []string{"user", "managed"}

type managedSettings struct {
	Env         map[string]string `json:"env"`
	Permissions *struct {
		Deny []string `json:"deny"`
	} `json:"permissions,omitempty"`
	// The review stage's posture, below, and absent from every other
	// stage's file: pointers, so a stage that sets none of them writes no
	// such key and "false" is a value rather than an omission.
	DisableAllHooks            *bool    `json:"disableAllHooks,omitempty"`
	AllowManagedHooksOnly      *bool    `json:"allowManagedHooksOnly,omitempty"`
	EnableAllProjectMcpServers *bool    `json:"enableAllProjectMcpServers,omitempty"`
	SettingSources             []string `json:"settingSources,omitempty"`
}

// reviewPosture turns on the review stage's posture (D894(a)). The review
// stage's working directory is the pull request's head, so a hook, an MCP
// server or a settings file that reaches that checkout must not run: a hook
// is a shell command Claude Code itself starts, with the job's environment
// and its ordinary egress, which no deny rule on the agent's own tools
// reaches. The strip (the runner's stripClaudeConfig) is one deletion from
// one code path, so the launch is the boundary as well: hooks from the
// managed file alone (and that file declares none, so none run at all), no
// project MCP server, and setting sources from the user and managed levels —
// never the project's.
func (s *managedSettings) reviewPosture() {
	no, yes := false, true
	s.DisableAllHooks, s.AllowManagedHooksOnly = &yes, &yes
	s.EnableAllProjectMcpServers = &no
	s.SettingSources = reviewSettingSources
}

// ReviewHome returns the directory a review stage's session runs with as
// HOME, creating it 0700 under stateDir (D894(a)). The image's own home is
// the user level of Claude Code's settings, so a settings file baked into
// the image would contribute a rule to a session that reads a pull request
// it must not trust; a fresh, empty directory is what leaves that level with
// nothing in it.
func ReviewHome(stateDir string) (string, error) {
	home := filepath.Join(stateDir, ReviewHomeName)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("creating the review stage's home: %w", err)
	}
	return home, nil
}

// WriteManagedSettings replaces path with {"env": env} and, when deny is not
// empty, a permissions rule carrying it, mode 0644, atomically: it writes a
// file beside it, syncs it and renames it over path. It refuses a symlink or
// a file that isn't regular at path. deny is DenyRules' answer for the stage
// about to run.
func WriteManagedSettings(path string, env map[string]string, deny []string) error {
	return writeManagedSettings(path, env, deny, false)
}

// WriteReviewManagedSettings is WriteManagedSettings for the review stage a
// review-mode run launches: the same file with the review posture added
// (D894(a)). It is a function of its own rather than a flag on
// WriteManagedSettings so that every other caller keeps writing exactly the
// file it wrote before, which is the promise D894(a) makes the coding run's
// own review rounds.
func WriteReviewManagedSettings(path string, env map[string]string, deny []string) error {
	return writeManagedSettings(path, env, deny, true)
}

func writeManagedSettings(path string, env map[string]string, deny []string, review bool) error {
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
	if review {
		s.reviewPosture()
	}
	if len(deny) > 0 {
		s.Permissions = &struct {
			Deny []string `json:"deny"`
		}{Deny: deny}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ManagedSettingsTmpPattern)
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

// routingEnvKeys (and any CLAUDE_CODE_USE_* key, which picks a provider) are the settings env variables that would send Claude
// Code's model calls, or its credentials, somewhere other than Fugaro's
// gateway. They are compared in upper case, so no_proxy is covered too.
var routingEnvKeys = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL",
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_SKIP_VERTEX_AUTH", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
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
			if up := strings.ToUpper(name); (slices.Contains(routingEnvKeys, up) || strings.HasPrefix(up, "CLAUDE_CODE_USE_")) && !slices.Contains(found, up) {
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
