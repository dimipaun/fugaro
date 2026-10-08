package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudePluginScript is a fake claude for the plugin subcommands fugaro
// upgrade runs. Every call is logged as one line of calls.log: the working
// directory, then each argument, tab-separated. The two lists print their
// answer files; any other call acts on the files named after its key
// (ClaudePluginKey): sleep.<key> sleeps (exec, so a kill reaches it),
// fail.<key> fails with its content on stderr, plugins.<key> and
// marketplaces.<key> replace the lists' answers, touch.<key> appends a newline
// to the working directory's .claude/settings.json, and print.<key> is printed
// before "ok: <args>". Marker files change every call: dumpenv writes its
// environment to env.out, readstdin copies stdin to stdin.out (then
// "stdin-eof"), grandchild starts a background job that writes marker after
// 2 s and then sleeps (creating spawned once it started), holdout leaves a job in its own process group holding
// stdout open for 6 s (also creating spawned), bigout prints 300000 bytes, listfail fails with its
// content on stderr. cwdfiles.log has the number of files in each working
// directory.
const claudePluginScript = `#!/bin/sh
d=$(dirname "$0")
{ printf '%s' "$PWD"; for a in "$@"; do printf '\t%s' "$a"; done; printf '\n'; } >> "$d/calls.log"
ls -A "$PWD" | wc -l | tr -d ' ' >> "$d/cwdfiles.log"
[ -f "$d/dumpenv" ] && env > "$d/env.out"
[ -f "$d/readstdin" ] && { cat > "$d/stdin.out"; echo stdin-eof >> "$d/stdin.out"; }
if [ -f "$d/grandchild" ]; then (sleep 2; echo done >> "$d/marker") & : > "$d/spawned"; sleep 30; fi
if [ -f "$d/holdout" ]; then set -m; (sleep 6) & : > "$d/spawned"; exec sleep 30; fi
if [ -f "$d/bigout" ]; then head -c 300000 /dev/zero | tr '\000' x; exit 0; fi
if [ -f "$d/listfail" ]; then cat "$d/listfail" >&2; exit 1; fi
case "$*" in
"plugin marketplace list --json") cat "$d/marketplaces.json"; exit 0 ;;
"plugin list --json") cat "$d/plugins.json"; exit 0 ;;
esac
key=$(printf '%s' "$*" | tr ' @/' '___')
if [ -f "$d/sleep.$key" ]; then exec sleep 30; fi
if [ -f "$d/fail.$key" ]; then cat "$d/fail.$key" >&2; exit 1; fi
if [ -f "$d/plugins.$key" ]; then cp "$d/plugins.$key" "$d/plugins.json"; fi
if [ -f "$d/marketplaces.$key" ]; then cp "$d/marketplaces.$key" "$d/marketplaces.json"; fi
if [ -f "$d/touch.$key" ]; then printf '\n' >> "$PWD/.claude/settings.json"; fi
if [ -f "$d/print.$key" ]; then cat "$d/print.$key"; fi
echo "ok: $*"
`

// ClaudePluginFake is the fake claude's directory. Tests hand Bin() to the
// code under test (claudeLookPath), never through the real PATH, so a
// developer's own claude is never run.
type ClaudePluginFake struct{ Dir string }

// NewClaudePluginFake installs the fake, answering the marketplace list with
// markets and the plugin list with plugins (JSON arrays).
func NewClaudePluginFake(t *testing.T, markets, plugins string) *ClaudePluginFake {
	t.Helper()
	f := &ClaudePluginFake{Dir: t.TempDir()}
	f.Write(t, "claude", claudePluginScript)
	if err := os.Chmod(f.Bin(), 0o755); err != nil {
		t.Fatal(err)
	}
	f.Write(t, "marketplaces.json", markets)
	f.Write(t, "plugins.json", plugins)
	return f
}

// Bin is the fake's path.
func (f *ClaudePluginFake) Bin() string { return filepath.Join(f.Dir, "claude") }

// Write puts a file next to the fake.
func (f *ClaudePluginFake) Write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// On arranges what the call args does: kind is sleep, fail, plugins,
// marketplaces, touch or print (see claudePluginScript).
func (f *ClaudePluginFake) On(t *testing.T, kind, content string, args ...string) {
	t.Helper()
	f.Write(t, kind+"."+ClaudePluginKey(args...), content)
}

// ClaudePluginKey is a call's key: its arguments joined by spaces, with
// ' ', '@' and '/' made '_' (the script's tr).
func ClaudePluginKey(args ...string) string {
	return strings.NewReplacer(" ", "_", "@", "_", "/", "_").Replace(strings.Join(args, " "))
}

// ClaudeCall is one recorded call of the fake.
type ClaudeCall struct {
	Dir  string
	Args []string
}

// Calls are the fake's calls, in order.
func (f *ClaudePluginFake) Calls(t *testing.T) []ClaudeCall {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []ClaudeCall
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		parts := strings.Split(line, "\t")
		out = append(out, ClaudeCall{Dir: parts[0], Args: parts[1:]})
	}
	return out
}

// Changes are the argument lists of the calls other than the two lists.
func (f *ClaudePluginFake) Changes(t *testing.T) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range f.Calls(t) {
		if j := strings.Join(c.Args, " "); j != "plugin marketplace list --json" && j != "plugin list --json" {
			out = append(out, c.Args)
		}
	}
	return out
}

// SamePath reports whether a and b name the same existing directory
// (macOS's /var is /private/var).
func SamePath(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}
