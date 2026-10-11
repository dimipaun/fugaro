package cli

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// docFlagToken is a --flag (or --flag=) token found in a doc, with the
// --no-value suffix and surrounding punctuation stripped, or "" when f
// holds no such token (including a markdown table rule like |---|---|,
// which also contains "--" but never a letter right after it).
var flagTokenRE = regexp.MustCompile("--([a-z][a-z-]*)")

func docFlagToken(f string) string {
	m := flagTokenRE.FindStringSubmatch(f)
	if m == nil {
		return ""
	}
	name, _, _ := strings.Cut(m[1], "=")
	return strings.TrimRight(name, "-")
}

// TestBaseImageDocFlagsAreReal: every --flag docs/base-image.md and
// docs/base-image-migration.md name for fugaro itself (not another tool
// such as gradlew or playwright) exists on at least one of init, image
// build or image render. A renamed flag on either side -- the doc's text
// or the command's own definition -- drops out of this union and fails.
// Not a tool this test owns: a flag that exists on the WRONG one of the
// three commands (e.g. --base on render instead of build) would still
// pass; distinguishing that would need per-sentence command binding, which
// the docs' prose (flags and command names interleaved in one paragraph)
// makes fragile to do reliably. The two docs are short enough that this
// was checked by hand instead (see the PR body).
func TestBaseImageDocFlagsAreReal(t *testing.T) {
	union := map[string]bool{}
	for _, cmd := range []*cobra.Command{newInitCmd(), newImageBuildCmd(), newImageRenderCmd()} {
		cmd.Flags().VisitAll(func(f *pflag.Flag) { union[f.Name] = true })
	}
	// Not fugaro's own flags: other tools these docs also quote.
	notFugaro := map[string]bool{"version": true, "no-daemon": true, "with-deps": true}
	for _, path := range []string{"docs/base-image.md", "docs/base-image-migration.md"} {
		doc := readDoc(t, path)
		for _, line := range strings.Split(doc, "\n") {
			for _, f := range strings.Fields(line) {
				name := docFlagToken(f)
				if name == "" || notFugaro[name] {
					continue
				}
				if !union[name] {
					t.Errorf("%s: --%s is not a flag of fugaro init, image build or image render", path, name)
				}
			}
		}
	}
}

// TestGCPSetupBaseImageSectionNamesRealKinds: the three worked command
// examples in gcp-setup.md's base-image section name the real base kind
// (config.BaseKind, "base"), not a stale or typo'd one -- this is what
// would have caught the reviewed-away "--base nonexist" class of mistake,
// which a flag-name Lookup alone does not: --base is a real flag either
// way, only its example VALUE would be wrong.
func TestGCPSetupBaseImageSectionNamesRealKinds(t *testing.T) {
	doc := readDoc(t, "docs/gcp-setup.md")
	start := strings.Index(doc, "### 3. The base image")
	end := strings.Index(doc, "### 4. A repository")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("docs/gcp-setup.md's base-image section (### 3 ... ### 4) was renamed or reordered; update this test's markers")
	}
	section := doc[start:end]
	if !slices.Contains(config.Bases, config.BaseKind) {
		t.Fatalf("config.BaseKind %q is not in config.Bases %v", config.BaseKind, config.Bases)
	}
	for _, want := range []string{
		"`fugaro init --base " + config.BaseKind + "`",
		"`--expect-digest " + config.BaseKind + "=sha256:<hex>`",
		"`--base-image " + config.BaseKind + "=IMAGE`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/gcp-setup.md's base-image section lacks %q (config.BaseKind is %q)", want, config.BaseKind)
		}
	}
}

// docPathRE pulls a trailing "(docs/....md)" citation off a refusal
// message, the shape every MigrationDoc-citing error uses.
var docPathRE = regexp.MustCompile(`\(([^()]+\.md)\)$`)

// TestMigrationDocCitationIsExactAndSectioned exercises the one refusal
// wired to config.MigrationDoc today (image.tools on a non-base kind,
// internal/config/image.go), reads the doc path straight out of the real
// error text (not the constant: a hardcoded wrong path in the add() call
// would still be caught), asserts it is exactly "docs/base-image-migration.md"
// and not merely an existing file, and that the file actually carries the
// section this specific refusal (base: go, plus image.tools) points a
// reader at.
func TestMigrationDocCitationIsExactAndSectioned(t *testing.T) {
	yaml := "\nversion: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n    base: go\n    image: { tools: { node: \"24\" } }\n    commands:\n      build: npm run build\n      test: npm test\n"
	_, problems := config.Parse([]byte(yaml))
	var msg string
	for _, p := range problems {
		if p.Path == "workflows.web.image.tools" {
			msg = p.Message
		}
	}
	if msg == "" {
		t.Fatalf("no workflows.web.image.tools problem among %v", problems)
	}
	m := docPathRE.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("message %q names no (doc.md) citation", msg)
	}
	path := m[1]
	if path != "docs/base-image-migration.md" {
		t.Errorf("the image.tools refusal cites %q, want exactly %q", path, "docs/base-image-migration.md")
	}
	if path != config.MigrationDoc {
		t.Errorf("the cited path %q and config.MigrationDoc %q have drifted apart", path, config.MigrationDoc)
	}
	doc := readDoc(t, path)
	for _, want := range []string{"## From `go`", "image.tools"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q, the section this refusal (base: go, image.tools) should point a reader at", path, want)
		}
	}
}
