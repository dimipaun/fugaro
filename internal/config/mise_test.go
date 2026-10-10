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

// TestMiseConfigFilesOnePerPath writes exactly one file at a time, so
// dropping any entry of MiseConfigPaths or MiseConfigDirs makes its own
// subtest fail distinctly, rather than only the one combined scenario of
// TestMiseConfigFiles (which never exercises .mise.toml, mise/config.toml,
// .mise/config.toml or .config/mise.toml as regular files at all).
func TestMiseConfigFilesOnePerPath(t *testing.T) {
	// A literal list, not derived from MiseConfigPaths/MiseConfigDirs: if it
	// were derived, dropping an entry from those vars would just shrink this
	// loop instead of failing a subtest. The completeness check below
	// instead catches a path added to the vars without a case here.
	files := []string{
		"mise.toml", ".mise.toml", "mise/config.toml", ".mise/config.toml",
		".config/mise.toml", ".config/mise/config.toml", ".tool-versions",
		"mise/conf.d/a.toml", ".mise/conf.d/a.toml", ".config/mise/conf.d/a.toml",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			root := t.TempDir()
			testutil.WriteFiles(t, root, map[string]string{file: ""})
			got, err := MiseConfigFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{file}; !slices.Equal(got, want) {
				t.Fatalf("with only %s: MiseConfigFiles = %v, want %v", file, got, want)
			}
		})
	}
	var declared []string
	declared = append(declared, MiseConfigPaths...)
	for _, dir := range MiseConfigDirs {
		declared = append(declared, dir+"/a.toml")
	}
	gotSorted, wantSorted := slices.Clone(files), slices.Clone(declared)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Errorf("this test's file list = %v, want one entry per MiseConfigPaths/MiseConfigDirs: %v", gotSorted, wantSorted)
	}
}

// TestMiseConfigFilesIgnoresASymlinkInConfD mirrors the root paths' own
// symlink exclusion (TestMiseConfigFiles' .mise.toml case) for a conf.d
// fragment: os.Lstat, not os.Stat, so a symlinked *.toml the glob matches is
// still excluded rather than followed.
func TestMiseConfigFilesIgnoresASymlinkInConfD(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"mise/conf.d/real.toml": "[tools]\nnode = \"24\"\n"})
	if err := os.Symlink("real.toml", filepath.Join(root, "mise", "conf.d", "link.toml")); err != nil {
		t.Fatal(err)
	}
	got, err := MiseConfigFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"mise/conf.d/real.toml"}; !slices.Equal(got, want) {
		t.Fatalf("MiseConfigFiles = %v, want %v (the symlink must be excluded)", got, want)
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

// TestBadBaseMessageConsistency pins that an unknown base: gives the exact
// same message at the repo's workflows.*.base (validate.go) and the project
// layer's profiles.*.base (layer.go): both share the Bases list, and a
// change to one message without the other is the inconsistency a reviewer
// found once already.
func TestBadBaseMessageConsistency(t *testing.T) {
	y := strings.Replace(baseYAML, "    commands:", "    base: bogus\n    commands:", 1)
	_, ps := Parse([]byte(y))
	workflowMsg := ""
	for _, p := range ps {
		if p.Path == "workflows.app.base" {
			workflowMsg = p.Message
		}
	}
	if workflowMsg == "" {
		t.Fatalf("no workflows.app.base problem in %v", ps)
	}
	_, lps := ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofiles:\n  p: { base: bogus }\n"), testAnchor)
	profileMsg := ""
	for _, p := range lps {
		if p.Path == "profiles.p.base" {
			profileMsg = p.Message
		}
	}
	if profileMsg == "" {
		t.Fatalf("no profiles.p.base problem in %v", lps)
	}
	if workflowMsg != profileMsg {
		t.Errorf("workflow base message %q != profile base message %q", workflowMsg, profileMsg)
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

// TestCheckSurfacesAMiseConfigFilesError: unlike CheckWarnings (best-effort,
// swallows the error), Check must report a MiseConfigFiles error as a real
// problem, since it is what fugaro validate refuses a config for.
func TestCheckSurfacesAMiseConfigFilesError(t *testing.T) {
	c, problems := Parse([]byte(baseYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"mise": "not a directory"})
	ps := Check(c, root)
	if len(ps) != 1 || ps[0].Path != "workflows.app" || !strings.Contains(ps[0].Message, "reading the repository's mise config") {
		t.Fatalf("Check = %v", ps)
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

// TestCheckWarningsRedactsTheWorkflowName matches Check and Validate, which
// both build their Problem.Path with showKey(name), not the raw workflow
// name: a Config built directly (bypassing Parse's WorkflowNameRE check, as
// a caller holding an already-parsed Config could) with a credential-shaped
// workflow name must not echo it back unredacted.
func TestCheckWarningsRedactsTheWorkflowName(t *testing.T) {
	cred := "ghp_" + strings.Repeat("a", 20)
	c := &Config{Workflows: map[string]Workflow{cred: {}}}
	ws := CheckWarnings(c, t.TempDir())
	if len(ws) != 1 || !strings.Contains(ws[0].Path, "<credential>") || strings.Contains(ws[0].Path, cred) {
		t.Fatalf("CheckWarnings = %v, want the workflow name redacted", ws)
	}
}

// TestCheckWarningsSkipConditions covers CheckWarnings' three independent
// skip conditions (a legacy base, a dockerfile:, or image.tools set): each
// alone must silence the "installs no language runtime" warning that an
// otherwise-identical Fugaro-base workflow with no mise config gets. It
// also confirms a MiseConfigFiles error is swallowed (no warning, no
// panic), which is intentional: CheckWarnings is best-effort, not
// authoritative (Check is what refuses a config).
func TestCheckWarningsSkipConditions(t *testing.T) {
	for name, y := range map[string]string{
		"legacy base": strings.Replace(baseYAML, "    commands:", "    base: web-node\n    commands:", 1),
		"dockerfile":  strings.Replace(baseYAML, "    commands:", "    dockerfile: .fugaro/app.Dockerfile\n    commands:", 1),
		"image.tools": strings.Replace(baseYAML, "    commands:", "    image: { tools: { node: \"24\" } }\n    commands:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			c, problems := Parse([]byte(y))
			if len(problems) > 0 {
				t.Fatal(problems)
			}
			if ws := CheckWarnings(c, t.TempDir()); len(ws) != 0 {
				t.Fatalf("CheckWarnings = %v, want none", ws)
			}
		})
	}
	t.Run("a MiseConfigFiles error is swallowed, not a warning", func(t *testing.T) {
		c, problems := Parse([]byte(baseYAML))
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		root := t.TempDir()
		// "mise" is a regular file, not a directory: MiseConfigFiles(root)
		// returns a non-ErrNotExist error (os.Lstat: not a directory) for
		// the mise/config.toml and mise/conf.d/*.toml paths.
		testutil.WriteFiles(t, root, map[string]string{"mise": "not a directory"})
		if _, err := MiseConfigFiles(root); err == nil {
			t.Fatal("MiseConfigFiles: want an error from the non-directory mise")
		}
		if ws := CheckWarnings(c, root); len(ws) != 0 {
			t.Fatalf("CheckWarnings = %v, want none (the error is swallowed, not surfaced as a false warning)", ws)
		}
	})
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
