package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestMiseConfigFiles(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{
		"mise.toml":                        "[tools]\nnode = \"24\"\n",
		".tool-versions":                   "python 3.12\n",
		".config/mise/conf.d/a.toml":       "[tools]\ngo = \"1.27\"\n",
		".config/mise/conf.d/.hidden.toml": "",
		"mise.local.toml":                  "[tools]\nnode = \"22\"\n", // never committed: not read
		"mise.lock":                        "",                         // pins, declares nothing
		"web/mise.toml":                    "",                         // a subdirectory: not the root's
	})
	if err := os.Symlink("mise.toml", filepath.Join(root, ".mise.toml")); err != nil {
		t.Fatal(err)
	}
	got, err := MiseConfigFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".config/mise/conf.d/a.toml", ".tool-versions", "mise.toml"}
	if !slices.Equal(got, want) {
		t.Fatalf("MiseConfigFiles = %v, want %v", got, want)
	}
	if got, err := MiseConfigFiles(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("empty checkout: %v %v", got, err)
	}
}

func TestBaseKind(t *testing.T) {
	if got := (Workflow{}).BaseKind(); got != BaseKind {
		t.Errorf("no base: = %q", got)
	}
	if got := (Workflow{Base: "go"}).BaseKind(); got != "go" {
		t.Errorf("base: go = %q", got)
	}
}

const baseYAML = `version: 1
project: acme
git: { provider: github }
workflows:
  app:
    commands: { build: make, test: make test }
`

func TestWorkflowWithoutBaseGetsUniformDefaults(t *testing.T) {
	c, problems := Parse([]byte(baseYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	w := c.Workflows["app"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" ||
		!slices.Equal(w.Commands.Reports, []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}) {
		t.Errorf("defaults: %+v %v", w.Resources, w.Commands.Reports)
	}
}

func TestValidateImageTools(t *testing.T) {
	for _, tc := range []struct {
		image, want string // want: "" for valid, else a substring of the problem
	}{
		{`{ tools: { node: "24.19.0", python: "3.12", "npm:firebase-tools": "15.32.1", java: temurin-25 } }`, ""},
		{`{ tools: { "Node!": "24" } }`, "image.tools.Node!"},
		{`{ tools: { node: "24; rm -rf /" } }`, "must be a version"},
		{`{ tools: { node: "'24'" } }`, "must be a version"},
	} {
		y := strings.Replace(baseYAML, "    commands:", "    image: "+tc.image+"\n    commands:", 1)
		_, problems := Parse([]byte(y))
		got := ""
		for _, p := range problems {
			got += p.String() + "\n"
		}
		if (tc.want == "") != (got == "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: problems %q, want %q", tc.image, got, tc.want)
		}
	}
	y := strings.Replace(baseYAML, "    commands:", "    base: web-node\n    image: { tools: { node: \"24\" } }\n    commands:", 1)
	if _, problems := Parse([]byte(y)); len(problems) == 0 || !strings.Contains(problems[0].Message, "the Fugaro base only") {
		t.Errorf("tools on web-node: %v", problems)
	}
}

func TestValidateRefusesToolsWithARepositoryMiseConfig(t *testing.T) {
	y := strings.Replace(baseYAML, "    commands:", "    image: { tools: { node: \"24\" } }\n    commands:", 1)
	c, problems := Parse([]byte(y))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"mise.toml": "[tools]\nnode = \"22\"\n"})
	ps := Check(c, root)
	if len(ps) != 1 || ps[0].Path != "workflows.app.image.tools" || !strings.Contains(ps[0].Message, "mise.toml") {
		t.Fatalf("Check = %v", ps)
	}
	if ps := Check(c, t.TempDir()); len(ps) != 0 {
		t.Fatalf("tools alone: %v", ps)
	}
}

func TestCheckWarningsNoRuntime(t *testing.T) {
	c, _ := Parse([]byte(baseYAML))
	ws := CheckWarnings(c, t.TempDir())
	if len(ws) != 1 || !strings.Contains(ws[0].Message, "installs no language runtime") {
		t.Fatalf("CheckWarnings = %v", ws)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{".tool-versions": "nodejs 24.19.0\n"})
	if ws := CheckWarnings(c, root); len(ws) != 0 {
		t.Fatalf("with .tool-versions: %v", ws)
	}
}

func TestSkipBuildScriptsOnTheBaseNeedsANodeWarmUp(t *testing.T) {
	y := strings.Replace(baseYAML, "    commands:", "    image: { skip_build_scripts: true }\n    commands:", 1)
	c, problems := Parse([]byte(y))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if ps := Check(c, t.TempDir()); len(ps) != 1 || !strings.Contains(ps[0].Message, "package.json and a lockfile") {
		t.Fatalf("no package.json: %v", ps)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": `{"lockfileVersion":3}`, "mise.toml": "[tools]\nnode = \"24\"\n"})
	if ps := Check(c, root); len(ps) != 0 {
		t.Fatalf("with npm: %v", ps)
	}
}
