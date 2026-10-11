package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
)

func readDoc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestMigrationDocCoversEveryKind: the doc every refusal names exists and
// says, per removed kind, what to write and what moves (design section 10).
func TestMigrationDocCoversEveryKind(t *testing.T) {
	doc := readDoc(t, config.MigrationDoc)
	for _, want := range []string{
		"## From `go`", "## From `web-node`", "## From `java-services`",
		`GOTOOLCHAIN = "local"`, `java = "temurin-25"`, `"npm:firebase-tools"`, "memory: 16Gi",
		"postgresql-17-postgis-3", "postgresql-17-pgvector", "redis-server",
		"image.node", "image.jdk", "image.tools", "skip_build_scripts", "base_images.base",
		"fugaro image refresh", "fugaro image render", "fugaro validate", "fugaro image build --local",
		"don't launch runs", "## The order",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", config.MigrationDoc, want)
		}
	}
}

// TestBaseImageDocNamesEveryTool: the user doc lists every harness and
// critical tool of the presence table, says which harnesses Fugaro drives,
// and states the contract (mise, the Dockerfile shape, no sudo).
func TestBaseImageDocNamesEveryTool(t *testing.T) {
	doc := readDoc(t, "docs/base-image.md")
	tools, err := image.ParseTools(images.BaseTools)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Tier == "kit" {
			continue
		}
		name := tool.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/base-image.md never names `%s`", name)
		}
	}
	for _, want := range []string{
		"only Claude Code is driven by Fugaro", "mise.toml", "image.tools", "mise install",
		"fugaro image render", "apt-get install", "image.apt", "mise use -g", "linux/arm64",
		"fugaro-services start", "never bake", config.MigrationDoc,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/base-image.md lacks %q", want)
		}
	}
}
