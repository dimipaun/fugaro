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
)

func readDoc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// flattenDoc collapses a file's whitespace and drops its line-comment markers,
// so a phrase can be searched for across the line breaks prose and Go comments
// wrap at. A guard that matches only the unwrapped spelling misses the claim it
// is meant to catch the moment someone re-wraps the sentence.
func flattenDoc(s string) string {
	s = strings.ReplaceAll(s, "//", " ")
	return strings.Join(strings.Fields(s), " ")
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

// trackedDocCommands maps the exact doc phrase of a fugaro command the scanned
// docs quote flags of to the cobra command it names, for
// TestBaseImageDocCommandFlagsAreReal.
//
// `fugaro setup` and `fugaro update` are two doors over one engine (W1.2
// retired the one command that used to be both into them), and the docs these
// tests scan name both: the wizard's own steps spell the first, and every
// re-run after it spells the second. W2.3 renamed the prose of every document
// scanned here, which is what retired the old-spelling entry this map used to
// carry beside them: it was tracked so the flags a doc quoted next to it stayed
// checked while the prose still used it.
var trackedDocCommands = map[string]func() *cobra.Command{
	"fugaro setup":         newSetupCmd,
	"fugaro update":        newUpdateCmd,
	"fugaro image build":   newImageBuildCmd,
	"fugaro image render":  newImageRenderCmd,
	"fugaro image refresh": newImageRefreshCmd,
	"fugaro run":           newRunCmd,
	"fugaro artifacts ls":  newArtifactsLsCmd,
	"fugaro artifacts get": newArtifactsGetCmd,
}

// mustBeAbsentFlags lists, per tracked command phrase, flag names a doc may
// mention while saying the command does NOT have them (a documented
// negative, such as gcp-setup.md and release.md's "there is still no --json
// or --plan-only" about fugaro image refresh): checked for absence, the
// opposite of every other flag this test finds.
var mustBeAbsentFlags = map[string]map[string]bool{
	"fugaro image refresh": {"json": true, "plan-only": true},
}

// commandBoundaryRE finds every "fugaro <subcommand>" mention in a doc --
// every real top-level fugaro command (root.go's AddCommand list) and every
// "image" subcommand, not just the four trackedDocCommands names -- so a
// tracked command's flag window stops at the next command's own mention
// rather than absorbing its flags too. This repo's docs often name several
// commands in one long paragraph or bullet (gcp-setup.md especially), so
// without this a flag of, say, "fugaro doctor" right after an aside about
// "fugaro setup" in the same sentence would be wrongly checked against setup.
// "runs" must precede "run" in the alternation: Go's regexp alternation is
// leftmost-first, not longest-match, so the shorter alternative would win
// and leave a dangling "s" otherwise. "update-skills" must precede "update"
// for the same reason.
var commandBoundaryRE = regexp.MustCompile(`fugaro (artifacts (?:ls|get)|image (?:build|render|refresh|check|gate|record|status|selftest|git-credential)|version|validate|config|verify|exec|image|runs|run|ls|logs|diagnose|cancel|secrets|setup|budget|watch|report|update-skills|update|upgrade|doctor)\b`)

// otherToolBoundaryRE closes a tracked command's flag window at the next
// mention of another real CLI this repo's docs quote flags of (gcloud,
// Docker, npm/npx, Gradle, Playwright, git, Homebrew): without it, e.g.
// gcp-setup.md's "gcloud scheduler jobs list --location ..." right after an
// aside about fugaro setup, with no further "fugaro X" mention in between,
// would attribute gcloud's --location to setup.
var otherToolBoundaryRE = regexp.MustCompile(`\b(?:gcloud|docker|npx|npm|gradlew|playwright|git|brew|go)\b`)

// commandFlagExceptions lists, per (document, command phrase), a flag name
// that belongs to a DIFFERENT command mentioned in the same sentence, where
// neither commandBoundaryRE nor otherToolBoundaryRE finds a mention to
// anchor a fresh segment on before the flag appears. Each entry is a
// specific, audited case, not a blanket skip.
var commandFlagExceptions = map[string]map[string]map[string]bool{
	"docs/release.md": {
		// "...rebuild it with `fugaro image build --repo ... --workflow
		// ...`. `--yes` confirms every step unattended... there is still
		// no `--json` or `--plan-only`." -- yes/json/plan-only here
		// describe fugaro image refresh (the paragraph's actual subject,
		// named two sentences earlier), not this image build aside; no
		// "fugaro image refresh" mention recurs between them for the
		// boundary regex to reopen on.
		"fugaro image build": {"yes": true, "json": true, "plan-only": true},
	},
	"docs/gcp-setup.md": {
		// "Upgrading: fugaro upgrade. ... then runs the cloud step... The
		// cloud step is `fugaro image refresh` for that repository. `--yes`
		// confirms every cloud step...; `--local` skips the cloud step;
		// `--check` writes nothing..." -- yes/local/check here are
		// fugaro upgrade's own flags (it names fugaro image refresh only
		// to say what its cloud step runs); no "fugaro upgrade" mention
		// recurs before them. yes is real on refresh too, so only the two
		// that are not need the exception.
		"fugaro image refresh": {"local": true, "check": true,
			// "Moving a repository... (2) The base step copies this
			// release's base image of each selected workflow's kind as
			// `update --base` does..." -- update's own flag, named without
			// repeating "fugaro" (already established at the top of the
			// same bullet).
			"base": true},
		// "without them nobody holds the launcher or token-minter role and
		// the first `fugaro run` fails with ... Passing `--launcher` or
		// `--operator` replaces that default entirely ... and `fugaro setup`
		// stops with the one line naming `--launcher` and `--operator`." --
		// launcher/operator here are fugaro setup's own flags, but they sit
		// in the flag window the `fugaro run` mention opens: the boundary
		// regex does reopen on the `fugaro setup` later in the sentence, and
		// that mention comes after every one of them.
		"fugaro run": {"launcher": true, "operator": true,
			// "fugaro run \"add a --verbose flag to the export command\"" --
			// the flag is inside a QUOTED TASK TEXT, which is a sentence for
			// the agent and not a command line: no fugaro command has
			// --verbose, and none should.
			"verbose": true},
	},
	"README.md": {
		// The same quoted example task as docs/gcp-setup.md's above:
		// `fugaro run "add a --verbose flag to the export command"`.
		"fugaro run": {"verbose": true},
	},
}

// TestBaseImageDocCommandFlagsAreReal: every --flag token that follows a
// mention of a trackedDocCommands phrase -- fugaro setup, fugaro update, image
// build, image render, image refresh, run, artifacts ls, artifacts get -- in
// docs/base-image.md, docs/base-image-migration.md, docs/gcp-setup.md,
// docs/release.md, docs/dogfooding.md, docs/artifact-runs.md and README.md --
// up to the next mention of ANY fugaro command or other CLI, or the end of
// the line -- is checked with Flags().Lookup against THAT command's real
// cobra flag set, not a union of them. A flag renamed on either side (the
// doc's text or the command's own definition), or one attributed to the wrong
// command by name, fails. docs/artifact-runs.md is scanned from the release
// that documents the feature, so every flag it names -- --attach above all --
// is checked against the command that actually has it.
func TestBaseImageDocCommandFlagsAreReal(t *testing.T) {
	for _, path := range []string{
		"docs/base-image.md", "docs/base-image-migration.md", "docs/gcp-setup.md",
		"docs/release.md", "docs/dogfooding.md", "README.md", "docs/artifact-runs.md",
	} {
		doc := readDoc(t, path)
		for _, line := range strings.Split(doc, "\n") {
			var bounds [][2]int
			for _, b := range commandBoundaryRE.FindAllStringIndex(line, -1) {
				bounds = append(bounds, [2]int{b[0], b[1]})
			}
			for _, b := range otherToolBoundaryRE.FindAllStringIndex(line, -1) {
				bounds = append(bounds, [2]int{b[0], b[1]})
			}
			slices.SortFunc(bounds, func(a, b [2]int) int { return a[0] - b[0] })
			for i, b := range bounds {
				phrase := line[b[0]:b[1]]
				newCmd, tracked := trackedDocCommands[phrase]
				if !tracked {
					continue // a real command (fugaro's or another tool's), but not one this test owns
				}
				segEnd := len(line)
				if i+1 < len(bounds) {
					segEnd = bounds[i+1][0]
				}
				seg := line[b[1]:segEnd]
				cmd := newCmd()
				except := commandFlagExceptions[path][phrase]
				absent := mustBeAbsentFlags[phrase]
				for _, f := range strings.Fields(seg) {
					name := docFlagToken(f)
					if name == "" || except[name] {
						continue
					}
					switch exists := cmd.Flags().Lookup(name) != nil; {
					case absent[name] && exists:
						t.Errorf("%s: %q documents --%s as absent, but it exists (segment: %q)", path, phrase, name, strings.TrimSpace(seg))
					case !absent[name] && !exists:
						t.Errorf("%s: %q has no flag --%s (segment: %q)", path, phrase, name, strings.TrimSpace(seg))
					}
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
		"`fugaro update --base " + config.BaseKind + "`",
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

// problemMessage parses yaml with config.Parse and returns the message of
// the one problem at path, failing the test if there is none.
func problemMessage(t *testing.T, yaml, path string) string {
	t.Helper()
	_, problems := config.Parse([]byte(yaml))
	for _, p := range problems {
		if p.Path == path {
			return p.Message
		}
	}
	t.Fatalf("no %s problem among %v", path, problems)
	return ""
}

// legacyFromMessage lints a repository Dockerfile that still builds FROM a
// published legacy base image, through config.LintDockerfile (not
// config.Parse: this refusal is the Dockerfile-lint path, not a fugaro.yaml
// field), and returns its one message.
func legacyFromMessage(t *testing.T) string {
	t.Helper()
	msgs := config.LintDockerfile([]byte("FROM ghcr.io/dimipaun/fugaro-web-node:0.6.0\nRUN git clone x /work/repo\n"))
	if len(msgs) != 1 {
		t.Fatalf("LintDockerfile = %v, want exactly one message", msgs)
	}
	return msgs[0]
}

// TestMigrationDocCitationIsExactAndSectioned exercises every refusal Task
// 17 wired to config.MigrationDoc (base:, image.node and image.jdk through
// config.Parse; a legacy FROM through config.LintDockerfile), reads the doc
// path straight out of each real error text (not the constant: a
// hardcoded wrong path in an add() call would still be caught), asserts it
// is exactly "docs/base-image-migration.md" and not merely an existing
// file, and that the file actually carries the section each specific
// refusal points a reader at.
func TestMigrationDocCitationIsExactAndSectioned(t *testing.T) {
	head := "\nversion: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n"
	tail := "    commands:\n      build: npm run build\n      test: npm test\n"
	for _, tc := range []struct {
		name, msg string
		sections  []string
	}{
		{"base: go", problemMessage(t, head+"    base: go\n"+tail, "workflows.web.base"), []string{"## From `go`"}},
		{"image.node", problemMessage(t, head+"    image: { node: \"24\" }\n"+tail, "workflows.web.image.node"), []string{"image.node` moves to `mise.toml`"}},
		{"image.jdk", problemMessage(t, head+"    image: { jdk: \"21\" }\n"+tail, "workflows.web.image.jdk"), []string{"## `image.jdk` and other removed settings"}},
		{"a legacy FROM in a project Dockerfile", legacyFromMessage(t), []string{"FROM ghcr.io/dimipaun/fugaro-web-node", "${FUGARO_BASE}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := docPathRE.FindStringSubmatch(tc.msg)
			if m == nil {
				t.Fatalf("message %q names no (doc.md) citation", tc.msg)
			}
			path := m[1]
			if path != "docs/base-image-migration.md" {
				t.Errorf("the %s refusal cites %q, want exactly %q", tc.name, path, "docs/base-image-migration.md")
			}
			if path != config.MigrationDoc {
				t.Errorf("the cited path %q and config.MigrationDoc %q have drifted apart", path, config.MigrationDoc)
			}
			doc := readDoc(t, path)
			for _, want := range tc.sections {
				if !strings.Contains(doc, want) {
					t.Errorf("%s lacks %q, the section the %s refusal should point a reader at", path, want, tc.name)
				}
			}
		})
	}
}

// migrationDocCitationRE finds every "(docs/....md)" citation in a chunk of
// text, the shape every refusal message in design/base-image.md section 10
// uses to name the migration doc.
var migrationDocCitationRE = regexp.MustCompile(`\(docs/[a-zA-Z0-9/_.-]+\.md\)`)

// migrationRefusalSections is every removed-setting refusal design section
// 10 describes (D2, the hard cut), and the docs/base-image-migration.md
// content each one should point a reader at. Task 17 is merged on this
// branch: base:, image.node, image.jdk and a legacy Dockerfile FROM are
// real, live refusals today, and TestMigrationDocCitationIsExactAndSectioned
// triggers each of those through config.Parse / config.LintDockerfile
// directly, reading the doc path out of the real error text. This table
// covers what that test does not re-trigger for brevity (base: web-node and
// base: java-services take the same code path as base: go, just a
// different value) and what internal/config cannot trigger at all (a
// legacy base_images key is internal/localcfg's refusal, not
// internal/config's), reading the design doc's own canonical message list
// instead -- "the doc's own list" -- rather than triggering them.
var migrationRefusalSections = []struct {
	name     string   // the removed setting or value this refusal is about
	sections []string // substrings docs/base-image-migration.md must carry
}{
	{"base: go", []string{"## From `go`"}},
	{"base: web-node", []string{"## From `web-node`"}},
	{"base: java-services", []string{"## From `java-services`"}},
	{"image.node", []string{"image.node` moves to `mise.toml`"}},
	{"image.jdk", []string{"## `image.jdk` and other removed settings"}},
	{"a legacy base_images key", []string{"base_images.go", "base_images.web-node", "base_images.java-services", "base_images.base"}},
	{"a legacy FROM in a project Dockerfile", []string{"FROM ghcr.io/dimipaun/fugaro-web-node", "${FUGARO_BASE}"}},
}

// TestDesignRefusalsCiteTheExactMigrationDocAndItsSections covers every
// removed-base-setting refusal design/base-image.md section 10 (D2, the
// hard cut) describes, not just the one config.Parse can trigger today:
// every "(docs/....md)" citation in that section must name exactly
// config.MigrationDoc (not merely an existing file), and
// docs/base-image-migration.md must carry the section each refusal is
// about. A section 10 bullet whose citation drifted to another doc, or
// whose heading the migration doc dropped or renamed, fails here.
func TestDesignRefusalsCiteTheExactMigrationDocAndItsSections(t *testing.T) {
	design := readDoc(t, "docs/design/base-image.md")
	start := strings.Index(design, "## 10. Migration: a hard cut")
	end := strings.Index(design, "## 11. Bring your own image")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("docs/design/base-image.md's migration section (## 10 ... ## 11) was renamed or reordered; update this test's markers")
	}
	section := design[start:end]
	cites := migrationDocCitationRE.FindAllString(section, -1)
	if len(cites) == 0 {
		t.Fatal("design/base-image.md section 10 names no (docs/....md) citation to check")
	}
	want := "(" + config.MigrationDoc + ")"
	for _, c := range cites {
		if c != want {
			t.Errorf("design/base-image.md section 10 cites %q, want %q (config.MigrationDoc)", c, want)
		}
	}

	migration := readDoc(t, config.MigrationDoc)
	for _, c := range migrationRefusalSections {
		for _, want := range c.sections {
			if !strings.Contains(migration, want) {
				t.Errorf("%s lacks %q, the section the %s refusal (design section 10) should point a reader at", config.MigrationDoc, want, c.name)
			}
		}
	}
}
