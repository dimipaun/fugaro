package image

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestToolsTableParses(t *testing.T) {
	tools, err := ParseTools(images.BaseTools)
	if err != nil {
		t.Fatal(err)
	}
	tier := map[string]string{}
	for _, tool := range tools {
		tier[tool.Name] = tool.Tier
	}
	for _, name := range []string{"fugaro", "claude", "git", "gh", "mise", "tini", "/usr/local/lib/fugaro/finalize-checkout"} {
		if tier[name] != "critical" {
			t.Errorf("%s is %q, want critical", name, tier[name])
		}
	}
	// The harnesses of design section 6, present but unsupported by the runner.
	for _, name := range []string{"codex", "gemini", "pi", "qwen", "opencode", "goose", "crush"} {
		if tier[name] != "harness" {
			t.Errorf("%s is %q, want harness", name, tier[name])
		}
	}
	for _, name := range []string{"gcloud", "docker", "rg", "fd", "bat", "yq", "jq", "fugaro-services"} {
		if tier[name] != "kit" {
			t.Errorf("%s is %q, want kit", name, tier[name])
		}
	}
}

func TestParseToolsRefuses(t *testing.T) {
	for name, table := range map[string]string{
		"three fields":  "git\tgit --version\tall\n",
		"bad arch":      "git\tgit --version\tamd64,s390x\tkit\n",
		"bad tier":      "git\tgit --version\tall\toptional\n",
		"repeated name": "git\t-\tall\tkit\ngit\t-\tall\tkit\n",
		"empty argv":    "git\t \tall\tkit\n",
	} {
		if _, err := ParseTools(table); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

// presenceTable is a table for the selftest tests: one tool that answers,
// one missing, one that fails, and one on the other architecture only.
func presenceTable(t *testing.T) {
	t.Helper()
	other := map[string]string{"amd64": "arm64", "arm64": "amd64"}[runtime.GOARCH]
	old := baseTools
	baseTools = "ok\tok --version\tall\tcritical\n" +
		"absent\t-\tall\tkit\n" +
		"fails\tfails --version\tall\tharness\n" +
		"elsewhere\t-\t" + other + "\tkit\n"
	t.Cleanup(func() { baseTools = old })
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{
		// ok proves the check's HOME is an empty directory of its own.
		"ok":        "#!/bin/sh\n[ -d \"$HOME\" ] && [ -z \"$(ls -A \"$HOME\")\" ] && [ \"$HOME\" != \"$REAL_HOME\" ] || { echo \"HOME=$HOME is not empty\"; exit 1; }\necho 'ok 1.0'\n",
		"fails":     "#!/bin/sh\necho 'needs a login' >&2\nexit 3\n",
		"elsewhere": "#!/bin/sh\nexit 0\n",
	})
	for _, f := range []string{"ok", "fails", "elsewhere"} {
		if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_HOME", os.Getenv("HOME"))
}

func TestSelftestToolsOnly(t *testing.T) {
	presenceTable(t)
	var log bytes.Buffer
	r := Selftest(context.Background(), SelftestSpec{ToolsOnly: true, Tools: "all"}, &log)
	got := map[string]bool{}
	for _, c := range r.Checks {
		got[c.Name] = c.OK
	}
	want := map[string]bool{"tool:ok": true, "tool:absent": false, "tool:fails": false}
	for name, ok := range want {
		if v, present := got[name]; !present || v != ok {
			t.Errorf("%s = %v (present %v), want %v; report %+v", name, v, present, ok, r)
		}
	}
	if _, ok := got["tool:elsewhere"]; ok {
		t.Error("a tool of the other architecture was checked")
	}
	if r.Passed {
		t.Error("passed with a missing and a failing tool")
	}
	if names := slices.Collect(func(yield func(string) bool) {
		for _, c := range r.Checks {
			if !yield(c.Name) {
				return
			}
		}
	}); slices.Contains(names, "user") || slices.Contains(names, "checkout") {
		t.Errorf("tools_only ran other checks: %v", names)
	}
	c, _ := checkNamed(r, "tool:fails")
	if !strings.Contains(c.Detail, "needs a login") {
		t.Errorf("the failure detail lacks the tool's output: %q", c.Detail)
	}
}

func TestSelftestToolsCriticalOnly(t *testing.T) {
	presenceTable(t)
	r := Selftest(context.Background(), SelftestSpec{ToolsOnly: true, Tools: "critical"}, &bytes.Buffer{})
	if !r.Passed || len(r.Checks) != 1 || r.Checks[0].Name != "tool:ok" {
		t.Fatalf("critical: %+v", r)
	}
}
