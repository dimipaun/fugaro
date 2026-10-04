// Package plugin_test checks the Claude Code plugin's structure.
package plugin_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func decodeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestMarketplace(t *testing.T) {
	var m struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Metadata struct {
			Description string `json:"description"`
		} `json:"metadata"`
		Plugins []struct {
			Name        string `json:"name"`
			Source      string `json:"source"`
			Description string `json:"description"`
		} `json:"plugins"`
	}
	decodeJSON(t, filepath.Join("..", ".claude-plugin", "marketplace.json"), &m)
	if m.Name != "fugaro" || m.Owner.Name == "" || m.Metadata.Description == "" || len(m.Plugins) != 1 {
		t.Fatalf("marketplace = %+v", m)
	}
	p := m.Plugins[0]
	if p.Name != "fugaro" || p.Source != "./plugin" || p.Description == "" {
		t.Fatalf("plugin entry = %+v", p)
	}
	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Author      struct {
			Name string `json:"name"`
		} `json:"author"`
		Repository string `json:"repository"`
		License    string `json:"license"`
	}
	decodeJSON(t, filepath.Join(".claude-plugin", "plugin.json"), &manifest)
	if manifest.Name != p.Name || manifest.Version == "" || manifest.Description == "" {
		t.Fatalf("plugin.json = %+v", manifest)
	}
}

// TestMarketplaceEntryHasNoVersion: the marketplace lists the plugin by a
// relative path with no version of its own (design §4.3); plugin.json's
// version is the only one, so the pin in a repository's settings.json names
// a tag, never a marketplace version that could drift from it.
func TestMarketplaceEntryHasNoVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Plugins []map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Plugins {
		if _, ok := p["version"]; ok {
			t.Fatalf("marketplace plugin entry carries a version: %+v", p)
		}
	}
}

func skillMeta(t *testing.T, path string) (name, description string, body []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	front, rest, ok := bytes.Cut(bytes.TrimPrefix(data, []byte("---\n")), []byte("\n---\n"))
	if !ok || !bytes.HasPrefix(data, []byte("---\n")) {
		t.Fatalf("%s has no frontmatter", path)
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		t.Fatalf("%s frontmatter: %v", path, err)
	}
	return meta.Name, meta.Description, rest
}

// TestSkillSetIsFour: the plugin carries exactly the four skills of design
// §4.1, each named after its directory with a non-empty, bounded
// description and a non-empty body.
func TestSkillSetIsFour(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("skills", "*", "SKILL.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		name, description, body := skillMeta(t, f)
		if dir := filepath.Base(filepath.Dir(f)); name != dir {
			t.Errorf("%s: name %q does not match its directory %q", f, name, dir)
		}
		if description == "" || len(description) > 1024 {
			t.Errorf("%s: description must be 1-1024 characters, is %d", f, len(description))
		}
		if len(bytes.TrimSpace(body)) == 0 {
			t.Errorf("%s has no body", f)
		}
		names[name] = true
	}
	want := []string{"setup", "working", "routing", "parallelism"}
	if len(names) != len(want) {
		t.Fatalf("skills = %v, want exactly %v", names, want)
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("skills = %v, missing %s", names, w)
		}
	}
}

// headerRE matches the do-not-edit header every skill file carries (design
// §4.4): a machine-readable tag naming the owning skill and its version.
var headerRE = regexp.MustCompile(`<!-- fugaro-skill name=(\S+) fugaro-version=(\S+) -->`)

// skillFiles returns every markdown file under root, SKILL.md and its
// reference files alike: both carry the header (design §4.4, §4.6 test 1).
// It walks every directory depth, so a skill's own SKILL.md and its
// reference/*.md files are both found.
func skillFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no skill files: %v", err)
	}
	return files
}

// TestEveryFileHasHeaderAndVersion: every skill file, including reference
// files, names its owning skill and carries a version (design §4.4).
func TestEveryFileHasHeaderAndVersion(t *testing.T) {
	for _, f := range skillFiles(t, "skills") {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := headerRE.FindSubmatch(data)
		if m == nil {
			t.Errorf("%s has no fugaro-skill header", f)
			continue
		}
		// The owning skill is the directory right under skills/, for both
		// a skill's own SKILL.md and its reference/*.md files.
		rel, err := filepath.Rel("skills", f)
		if err != nil {
			t.Fatal(err)
		}
		owner := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if name := string(m[1]); name != owner {
			t.Errorf("%s: header names %q, owning skill is %q", f, name, owner)
		}
		if len(m[2]) == 0 {
			t.Errorf("%s: header has no version", f)
		}
	}
}

// TestHeaderVersionEqualsPluginVersion: every skill header's version agrees
// with plugin.json's, so a release always ships skills that match its own
// manifest (design §4.4, enforced again by bump-plugin-version.sh --check).
func TestHeaderVersionEqualsPluginVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, f := range skillFiles(t, "skills") {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := headerRE.FindSubmatch(data)
		if m == nil {
			continue // reported by TestEveryFileHasHeaderAndVersion
		}
		if got := string(m[2]); got != manifest.Version {
			t.Errorf("%s: header version %s, plugin.json has %s", f, got, manifest.Version)
		}
	}
}

// copyTree copies the bump script and the plugin from the repository at src
// into a fresh temp directory and returns its path, so the script's tests can
// rewrite files without touching the checkout. (cp -R with a directory
// source behaves the same on GNU and BSD cp, unlike cp -r on a bare "..".)
func copyTree(t *testing.T, src string) string {
	t.Helper()
	abs, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := exec.LookPath("cp")
	if err != nil {
		t.Skip("no cp")
	}
	dst := t.TempDir()
	for _, name := range []string{"scripts", "plugin"} {
		if out, err := exec.Command(cp, "-R", filepath.Join(abs, name), dst).CombinedOutput(); err != nil {
			t.Fatalf("cp -R %s %s: %v\n%s", name, dst, err, out)
		}
	}
	return dst
}

// manifestVersion reads the version of a plugin.json.
func manifestVersion(t *testing.T, path string) string {
	t.Helper()
	var m struct {
		Version string `json:"version"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m); err != nil || m.Version == "" {
		t.Fatalf("%s: no version: %v", path, err)
	}
	return m.Version
}

func runBumpScript(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	script := filepath.Join(root, "scripts", "bump-plugin-version.sh")
	cmd := exec.Command(bash, append([]string{script}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestBumpScriptRewritesHeaders: bumping the version rewrites plugin.json
// and every skill header together, and the result passes its own --check.
func TestBumpScriptRewritesHeaders(t *testing.T) {
	root := copyTree(t, "..")
	if out, err := runBumpScript(t, root, "9.9.9"); err != nil {
		t.Fatalf("bump: %v\n%s", err, out)
	}
	pluginJSON, err := os.ReadFile(filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pluginJSON), `"version": "9.9.9"`) {
		t.Errorf("plugin.json not bumped:\n%s", pluginJSON)
	}
	for _, f := range skillFiles(t, filepath.Join(root, "plugin", "skills")) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := headerRE.FindSubmatch(data)
		if m == nil || string(m[2]) != "9.9.9" {
			t.Errorf("%s header not bumped: %s", f, m)
		}
	}
	if out, err := runBumpScript(t, root, "--check", "9.9.9"); err != nil {
		t.Errorf("--check after bump: %v\n%s", err, out)
	}
}

// TestBumpScriptCheckFailsOnStaleHeader: --check fails, and names the file,
// when one skill header disagrees with the target version, even if
// plugin.json itself is right.
func TestBumpScriptCheckFailsOnStaleHeader(t *testing.T) {
	root := copyTree(t, "..")
	// The tree's own version, whatever release it is at.
	v := manifestVersion(t, filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"))
	current, err := runBumpScript(t, root, "--check", v)
	if err != nil {
		t.Fatalf("baseline --check %s: %v\n%s", v, current, err)
	}
	stale := filepath.Join(root, "plugin", "skills", "setup", "SKILL.md")
	data, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("fugaro-version="+v), []byte("fugaro-version=0.0.0"), 1)
	if err := os.WriteFile(stale, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runBumpScript(t, root, "--check", v)
	if err == nil {
		t.Fatalf("--check passed with a stale header:\n%s", out)
	}
	if !strings.Contains(out, "setup/SKILL.md") {
		t.Errorf("--check did not name the stale file:\n%s", out)
	}
}
