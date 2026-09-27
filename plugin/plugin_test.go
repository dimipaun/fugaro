// Package plugin_test checks the Claude Code plugin's structure.
package plugin_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
		Plugins []struct {
			Name        string `json:"name"`
			Source      string `json:"source"`
			Description string `json:"description"`
		} `json:"plugins"`
	}
	decodeJSON(t, filepath.Join("..", ".claude-plugin", "marketplace.json"), &m)
	if m.Name != "fugaro" || m.Owner.Name == "" || len(m.Plugins) != 1 {
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

func TestSkills(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("skills", "*", "SKILL.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		front, body, ok := bytes.Cut(bytes.TrimPrefix(data, []byte("---\n")), []byte("\n---\n"))
		if !ok || !bytes.HasPrefix(data, []byte("---\n")) {
			t.Fatalf("%s has no frontmatter", f)
		}
		var meta struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal(front, &meta); err != nil {
			t.Fatalf("%s frontmatter: %v", f, err)
		}
		if dir := filepath.Base(filepath.Dir(f)); meta.Name != dir {
			t.Errorf("%s: name %q does not match its directory %q", f, meta.Name, dir)
		}
		if meta.Description == "" || len(meta.Description) > 1024 {
			t.Errorf("%s: description must be 1-1024 characters, is %d", f, len(meta.Description))
		}
		if len(bytes.TrimSpace(body)) == 0 {
			t.Errorf("%s has no body", f)
		}
		names[meta.Name] = true
	}
	if !names["onboard"] {
		t.Errorf("skills = %v, want onboard", names)
	}
}
