package plugin_test

// End-to-end fixtures for the setup skill (plan T19): small repositories
// under testdata/repos, each with the fugaro.yaml (and Dockerfile) a correct
// run of the skill produces. The skill itself is not run: the tests hold the
// expected outputs to the real validator and renderer, and hold the facts the
// skill's tables derive from each repository to those tables and the base
// images, so a change to either breaks them. testdata/repos/<name>/repo is the
// repository as found; testdata/repos/<name>/expected is what the skill adds
// or replaces in it.

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
)

const fixtureVersion = "0.0.0"

// fixtureFacts is what the skill's tables derive from one repository.
type fixtureFacts struct {
	name string
	// workflows: the expected workflow names (discovery.md, "Workflows and
	// runtimes"). Every workflow builds on the one Fugaro base.
	workflows []string
	// tools: the expected mise.toml [tools] table, tool name to version
	// (discovery.md, "Workflows and runtimes").
	tools map[string]string
	// toolEvidence maps a tool to the repository file that pins it; a tool
	// with no entry here is a decision taken with the user, with no
	// evidence in the repository.
	toolEvidence map[string]string
	// env: the expected mise.toml [env] table (discovery.md's go row:
	// `[env] GOTOOLCHAIN = "local"`); nil for a fixture with no go workflow.
	env map[string]string
	// servicesEvidence is a file and the text in it that says the tests need
	// Postgres; the services table then gives the start line.
	servicesFile, servicesText, testPrefix string
}

var fixtures = []fixtureFacts{
	{name: "node", workflows: []string{"web"}, tools: map[string]string{"node": "24.19.0"}, toolEvidence: map[string]string{"node": ".nvmrc"}},
	{name: "go", workflows: []string{"go"}, tools: map[string]string{"go": "1.25"}, toolEvidence: map[string]string{"go": "go.mod"},
		env: map[string]string{"GOTOOLCHAIN": "local"}},
	{name: "server", workflows: []string{"server"}, tools: map[string]string{"java": "temurin-25"},
		servicesFile: "bitbucket-pipelines.yml", servicesText: "postgres", testPrefix: "FUGARO_SERVICES=postgres fugaro-services start"},
	{name: "monorepo", workflows: []string{"api", "web"},
		tools: map[string]string{"go": "1.25", "node": "24"}, toolEvidence: map[string]string{"go": "services/api/go.mod"},
		env: map[string]string{"GOTOOLCHAIN": "local"}},
	{name: "existing-fugaro-yaml", workflows: []string{"go"}, tools: map[string]string{"go": "1.25"}, toolEvidence: map[string]string{"go": "go.mod"},
		env: map[string]string{"GOTOOLCHAIN": "local"}},
	{name: "hostile", workflows: []string{"web"}, tools: map[string]string{"node": "22"}, toolEvidence: map[string]string{"node": ".nvmrc"}},
}

func TestSetupFixtureWebNode(t *testing.T)      { checkFixture(t, "node") }
func TestSetupFixtureGo(t *testing.T)           { checkFixture(t, "go") }
func TestSetupFixtureJava(t *testing.T)         { checkFixture(t, "server") }
func TestSetupFixtureMonorepo(t *testing.T)     { checkFixture(t, "monorepo") }
func TestSetupFixtureExistingYAML(t *testing.T) { checkFixture(t, "existing-fugaro-yaml") }
func TestSetupFixtureHostile(t *testing.T)      { checkFixture(t, "hostile") }

// copyFixtureTree copies the files under src into dst, over what is there.
func copyFixtureTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureFile(t *testing.T, name, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "repos", name, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fixtureByName(t *testing.T, name string) fixtureFacts {
	t.Helper()
	for _, f := range fixtures {
		if f.name == name {
			return f
		}
	}
	t.Fatalf("no facts for fixture %s", name)
	return fixtureFacts{}
}

// checkFixture runs every check on one fixture.
func checkFixture(t *testing.T, name string) {
	facts := fixtureByName(t, name)
	dir := filepath.Join("testdata", "repos", name)

	// The checkout the skill leaves: the repository with the expected files
	// over it. For a fixture with an existing Dockerfile that the skill drops,
	// the stale one stays on disk, unreferenced, as it would in the checkout.
	checkout := t.TempDir()
	copyFixtureTree(t, filepath.Join(dir, "repo"), checkout)
	copyFixtureTree(t, filepath.Join(dir, "expected"), checkout)

	// (a) the real validator.
	c, problems := config.Parse([]byte(fixtureFile(t, name, "expected/fugaro.yaml")))
	if len(problems) > 0 {
		t.Fatalf("expected fugaro.yaml does not validate: %v", problems)
	}
	if problems := config.Check(c, checkout); len(problems) > 0 {
		t.Errorf("config.Check: %v", problems)
	}

	// (b) the discovery facts.
	got := slices.Sorted(maps.Keys(c.Workflows))
	if !slices.Equal(got, sortedStrings(facts.workflows)) {
		t.Errorf("workflows %v, want %v", got, facts.workflows)
	}
	if derived := detectRuntimes(t, filepath.Join(dir, "repo")); !slices.Equal(derived, sortedStrings(slices.Collect(maps.Keys(facts.tools)))) {
		t.Errorf("the discovery table's mise tools for the repository are %v, want %v", derived, sortedStrings(slices.Collect(maps.Keys(facts.tools))))
	}
	miseText := fixtureFile(t, name, "expected/mise.toml")
	mise := parseMiseTools(t, miseText)
	if !equalMaps(mise, facts.tools) {
		t.Errorf("mise.toml tools %v, want %v", mise, facts.tools)
	}
	if env := parseMiseEnv(t, miseText); !equalMaps(env, facts.env) {
		t.Errorf("mise.toml [env] %v, want %v", env, facts.env)
	}
	for tool, file := range facts.toolEvidence {
		version, ok := mise[tool]
		if !ok {
			t.Errorf("toolEvidence names %s, which is not in tools (fixtureFacts typo?)", tool)
			continue
		}
		if ev := fixtureFile(t, name, "repo/"+file); !strings.Contains(ev, version) {
			t.Errorf("mise.toml %s = %q, but %s does not say it", tool, version, file)
		}
	}
	for wf, w := range c.Workflows {
		if w.Base != "" || w.Image.Node != "" || len(w.Image.Tools) > 0 {
			t.Errorf("workflow %s: base %q, image.node %q, image.tools %v: the skill writes mise.toml instead", wf, w.Base, w.Image.Node, w.Image.Tools)
		}
		checkNoRuntimeApt(t, "fixture "+name, wf, w)
		if facts.servicesFile != "" {
			if !strings.Contains(fixtureFile(t, name, "repo/"+facts.servicesFile), facts.servicesText) {
				t.Errorf("%s does not name %s", facts.servicesFile, facts.servicesText)
			}
			if !strings.HasPrefix(w.Commands.Test, facts.testPrefix) {
				t.Errorf("commands.test %q does not start with %q", w.Commands.Test, facts.testPrefix)
			}
		}
	}

	// The tests must hold of a fixture with no secrets in it.
	for _, rel := range []string{"expected/fugaro.yaml", "expected/mise.toml"} {
		if secretLikeRE.MatchString(fixtureFile(t, name, rel)) {
			t.Errorf("%s looks like it holds a credential", rel)
		}
	}
}

var secretLikeRE = regexp.MustCompile(`(?i)(ghp_|github_pat_|sk-ant-|-----BEGIN|xox[bp]-|AKIA[0-9A-Z]{12})`)

// runtimeAptPackageRE matches Debian package names that install a language
// runtime directly (services-and-images.md: "Never apt-get install
// <language>"; mise is the one way to install a runtime on the base). Not
// exhaustive, but covers the common per-distro names and version suffixes,
// including the -headless JDK/JRE variant (openjdk-17-jdk-headless) that a
// plain (-jdk|-jre) suffix alone misses.
var runtimeAptPackageRE = regexp.MustCompile(`^(nodejs|npm|golang(-go)?|python3?(\.\d+)?(-pip|-venv|-dev)?|ruby(\d+(\.\d+)?)?|r?openjdk-?\d*(-jdk|-jre)?(-headless)?|(default-jdk|default-jre)(-headless)?|php\d*(\.\d+)?(-cli|-fpm)?|rustc|cargo|dotnet-sdk(-\d+(\.\d+)?)?|mono-complete)$`)

// checkNoRuntimeApt fails t if any image.apt entry of w installs a language
// runtime instead of declaring it in mise.toml or image.tools.
func checkNoRuntimeApt(t *testing.T, at, wf string, w config.Workflow) {
	t.Helper()
	for _, p := range w.Image.Apt {
		if runtimeAptPackageRE.MatchString(p) {
			t.Errorf("%s: workflow %s: image.apt installs %q, a language runtime; mise.toml or image.tools is the only way to install one", at, wf, p)
		}
	}
}

// aptGetInstallRE finds an apt(-get) install invocation in a Dockerfile line.
var aptGetInstallRE = regexp.MustCompile(`\bapt(-get)?\s+install\b`)

// joinShellContinuations collapses lines ending in a trailing `\` into their
// logical line: the project's own idiomatic multi-line
// `apt-get install -y --no-install-recommends \` style (images/base/Dockerfile
// lists its packages across several continuation lines), which scanning one
// physical line at a time would never see past the first.
func joinShellContinuations(text string) []string {
	var out []string
	var cur string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, `\`) {
			cur += strings.TrimSuffix(trimmed, `\`) + " "
			continue
		}
		cur += line
		out = append(out, cur)
		cur = ""
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// aptPackages returns the package names one logical Dockerfile line installs
// with apt: the words between the install verb and the first shell operator
// that ends the install, with apt's own flags (-y, --no-install-recommends)
// dropped.
func aptPackages(line string) []string {
	m := aptGetInstallRE.FindStringIndex(line)
	if m == nil {
		return nil
	}
	rest := line[m[1]:]
	for _, sep := range []string{"&&", ";", "|"} {
		if i := strings.Index(rest, sep); i >= 0 {
			rest = rest[:i]
		}
	}
	var out []string
	for _, f := range strings.Fields(rest) {
		if f = strings.Trim(f, "\\\"'`"); f == "" || strings.HasPrefix(f, "-") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// addedLinesInstallARuntime reports a package name a block of added
// Dockerfile lines apt-get installs that is a language runtime (see
// runtimeAptPackageRE): the one place a hand-written Dockerfile — the file the
// skill tells the agent to write now that no repository derives one — could
// smuggle a `base:`-style runtime install in. Lines are joined across `\`
// continuations first, so a package named on a continuation line is seen too.
func addedLinesInstallARuntime(text string) (pkg string, found bool) {
	for _, line := range joinShellContinuations(text) {
		for _, f := range aptPackages(line) {
			if runtimeAptPackageRE.MatchString(f) {
				return f, true
			}
		}
	}
	return "", false
}

// shellPipeRE matches a download piped into a shell, the other way a
// hand-written Dockerfile smuggles in a runtime the base never installed.
var shellPipeRE = regexp.MustCompile(`\|\s*(sudo\s+)?(ba|z)?sh\b`)

// dockerfileFences returns the body of every ```dockerfile fence in the text:
// the Dockerfiles a skill file shows the agent to write.
func dockerfileFences(text string) []string {
	var out []string
	var cur []string
	open := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !open && strings.HasPrefix(trimmed, "```dockerfile"):
			open, cur = true, nil
		case open && trimmed == "```":
			open = false
			out = append(out, strings.Join(cur, "\n"))
		case open:
			cur = append(cur, line)
		}
	}
	return out
}

// entryDockerfiles returns every ```dockerfile fence the skill files show,
// keyed by the file and the fence's index in it.
func entryDockerfiles(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for name, text := range setupFiles(t) {
		if fences := dockerfileFences(text); len(fences) > 0 {
			out[name] = fences
		}
	}
	return out
}

// baseAptPackages returns the Debian package names images/base/Dockerfile's
// apt-get install line bakes into the base, read from the Dockerfile itself
// so a change to it is what this test tracks, not a copy that can drift.
func baseAptPackages(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "images", "base", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range joinShellContinuations(string(data)) {
		out = append(out, aptPackages(line)...)
	}
	if len(out) == 0 {
		t.Fatal("images/base/Dockerfile has no apt-get install block this reads packages from")
	}
	return out
}

// TestEntryDockerfilesTheSkillShows: the Dockerfile a skill file shows the
// agent to write — the entry's own, built FROM the base with the packages the
// repository needs — carries no language runtime and pipes nothing into a
// shell. This is where the guard that used to ride the removed dockerfile: key
// lives now: no repository derives an image of its own, and the hand-written
// entry Dockerfile is what the skill tells the agent to produce.
func TestEntryDockerfilesTheSkillShows(t *testing.T) {
	fences := entryDockerfiles(t)
	if len(fences) == 0 {
		t.Fatal("no skill file shows a Dockerfile: the skill tells the agent to write one, so one of its examples is the shape a reader copies")
	}
	for name, bodies := range fences {
		for i, body := range bodies {
			at := fmt.Sprintf("%s fence %d", name, i+1)
			if !strings.Contains(body, "FROM ${FUGARO_BASE}") || !strings.Contains(body, "ARG FUGARO_BASE") {
				t.Errorf("%s: the example does not start FROM ${FUGARO_BASE} with the argument declared, the contract every entry keeps", at)
			}
			if !strings.Contains(body, "USER root") || len(aptPackages(body)) == 0 {
				t.Errorf("%s: the example installs no package in a root block, so it shows nothing of the shape a repository copies:\n%s", at, body)
			}
			if pkg, found := addedLinesInstallARuntime(body); found {
				t.Errorf("%s: the example apt-get installs %s, a language runtime; mise.toml is the only way to install one:\n%s", at, pkg, body)
			}
			if shellPipeRE.MatchString(body) {
				t.Errorf("%s: the example pipes a download into a shell:\n%s", at, body)
			}
		}
	}
}

// TestEntryDockerfilePackagesAreNotOnTheBase: a package the skill shows the
// agent adding is not one images/base/Dockerfile already bakes, where the line
// would install nothing. This compares Debian package names against Debian
// package names: tools.tsv's Name column is the command a presence check runs
// (rg, fd, dig), which very often differs from the Debian package that
// provides it (ripgrep, fd-find, bind9-dnsutils), so a comparison against
// tools.tsv would miss most real redundant apt entries.
func TestEntryDockerfilePackagesAreNotOnTheBase(t *testing.T) {
	base := baseAptPackages(t)
	for name, bodies := range entryDockerfiles(t) {
		for i, body := range bodies {
			for _, p := range aptPackages(body) {
				if slices.Contains(base, p) {
					t.Errorf("%s fence %d adds %s, which the base already carries", name, i+1, p)
				}
			}
		}
	}
}

// TestAddedLinesInstallARuntime: the guard itself, on the shapes a hand-edited
// Dockerfile takes — a package on the install line, one on a `\` continuation
// line (which is how the project's own Dockerfiles list packages), a benign
// package, and a runtime named somewhere other than an apt install.
func TestAddedLinesInstallARuntime(t *testing.T) {
	for _, tc := range []struct {
		what, text, want string
	}{
		{"on the install line", "RUN apt-get install -y --no-install-recommends nodejs", "nodejs"},
		{"on a continuation line", "RUN apt-get install -y --no-install-recommends \\\n      libvips-dev \\\n      openjdk-17-jdk-headless", "openjdk-17-jdk-headless"},
		{"a bare install", "RUN apt install python3-pip", "python3-pip"},
		{"a system library", "RUN apt-get install -y --no-install-recommends libvips-dev", ""},
		{"no apt at all", "RUN mise install && npm ci", ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got, found := addedLinesInstallARuntime(tc.text)
			if tc.want == "" {
				if found {
					t.Fatalf("addedLinesInstallARuntime(%q) = %q, want nothing", tc.text, got)
				}
				return
			}
			if !found || got != tc.want {
				t.Fatalf("addedLinesInstallARuntime(%q) = %q, %v; want %q", tc.text, got, found, tc.want)
			}
		})
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sortedStrings(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// parseMiseTOMLSection reads one top-level table of a mise.toml, one
// `key = "value"` per line, validating each with valid; section is "tools"
// or "env" without brackets.
func parseMiseTOMLSection(t *testing.T, text, section string, valid func(k, v string) bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			in = line == "["+section+"]"
		case in:
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				t.Errorf("mise.toml [%s] line %q is not name = \"value\"", section, line)
				continue
			}
			// TOML allows any amount of whitespace around "=" (node="x",
			// node  =  "x" are both legal); trim it from both sides rather
			// than requiring the skill's own canonical "name = \"value\""
			// spacing exactly.
			k := strings.Trim(strings.TrimSpace(line[:eq]), `"`)
			v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"`)
			if !valid(k, v) {
				t.Errorf("mise.toml [%s] line %q is not name = \"value\"", section, line)
				continue
			}
			out[k] = v
		}
	}
	return out
}

// parseMiseTools reads the [tools] table of a mise.toml as the skill writes
// it: one `name = "version"` per line, each a tool and version config allows.
func parseMiseTools(t *testing.T, text string) map[string]string {
	t.Helper()
	return parseMiseTOMLSection(t, text, "tools", config.ValidMiseTool)
}

// miseEnvKeyRE allows a shell environment variable name, as mise's [env]
// table keys (GOTOOLCHAIN, not a lowercase mise tool name).
var miseEnvKeyRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// parseMiseEnv reads the [env] table of a mise.toml, as the skill writes
// GOTOOLCHAIN = "local" for a go workflow (discovery.md).
func parseMiseEnv(t *testing.T, text string) map[string]string {
	t.Helper()
	return parseMiseTOMLSection(t, text, "env", func(k, v string) bool {
		return miseEnvKeyRE.MatchString(k) && v != ""
	})
}

// detectRuntimes applies the discovery table's evidence rows to a repository
// (the root and its directories two levels down, as separate roots) and
// returns the sorted, de-duplicated mise tools the discovery table derives
// from the evidence at each root.
func detectRuntimes(t *testing.T, repo string) []string {
	t.Helper()
	var out []string
	exists := func(dir string, names ...string) bool {
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
				return true
			}
		}
		return false
	}
	roots := []string{repo}
	for _, depth := range []string{"*", filepath.Join("*", "*")} {
		m, _ := filepath.Glob(filepath.Join(repo, depth))
		for _, p := range m {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() && !strings.HasPrefix(filepath.Base(p), ".") {
				roots = append(roots, p)
			}
		}
	}
	existsGlob := func(dir, pattern string) bool {
		m, _ := filepath.Glob(filepath.Join(dir, pattern))
		return len(m) > 0
	}
	for _, r := range roots {
		if exists(r, "package.json") && exists(r, "yarn.lock", "pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json") {
			out = append(out, "node")
		}
		if exists(r, "gradlew", "gradlew.sh", "settings.gradle", "settings.gradle.kts", "pom.xml") {
			out = append(out, "java")
		}
		if exists(r, "go.mod") {
			out = append(out, "go")
		}
		if exists(r, "pyproject.toml", ".python-version") || existsGlob(r, "requirements*.txt") {
			out = append(out, "python")
		}
		if exists(r, "Gemfile", ".ruby-version") {
			out = append(out, "ruby")
		}
		if exists(r, "rust-toolchain.toml", "Cargo.toml") {
			out = append(out, "rust")
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// TestDetectRuntimesCoversEveryDiscoveryRow: detectRuntimes recognizes the
// evidence discovery.md's table names for every language row, not only the
// three (node, go, java) the current fixtures happen to exercise.
func TestDetectRuntimesCoversEveryDiscoveryRow(t *testing.T) {
	for _, tc := range []struct {
		runtime string
		files   map[string]string
	}{
		{"node", map[string]string{"package.json": "{}", "package-lock.json": "{}"}},
		{"go", map[string]string{"go.mod": "module x\n\ngo 1.25\n"}},
		{"java", map[string]string{"pom.xml": "<project/>"}},
		{"python", map[string]string{"pyproject.toml": "[project]\n"}},
		{"ruby", map[string]string{"Gemfile": "source \"https://rubygems.org\"\n"}},
		{"rust", map[string]string{"Cargo.toml": "[package]\n"}},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := detectRuntimes(t, dir); !slices.Contains(got, tc.runtime) {
				t.Errorf("detectRuntimes(%v) = %v, want it to contain %q", tc.files, got, tc.runtime)
			}
		})
	}
}

// TestRenderedDockerfilePassesContract: the Dockerfile `fugaro image render`
// gives for each base kind, with the settings the fixtures use, passes the
// static contract the skill tells the agent to keep.
func TestRenderedDockerfilePassesContract(t *testing.T) {
	for _, base := range config.Bases {
		for _, img := range []config.Image{{}, {Apt: []string{"libvips-dev"}, Setup: []string{"echo ok"}}} {
			df, err := image.Render(image.RenderInput{Workflow: "w", Base: base, Image: img, Version: fixtureVersion})
			if err != nil {
				t.Fatal(err)
			}
			if msgs := config.LintDockerfile(df); len(msgs) > 0 {
				t.Errorf("base %s, image %+v: %v", base, img, msgs)
			}
		}
	}
}

// TestSetupFixtureTablesMatchTheSkill: the evidence the fixtures rely on is
// what the skill's discovery table says, and the base image carries
// `fugaro-services` (images/common, shared by every workflow now the base is
// one image, design base-image.md B11).
func TestSetupFixtureTablesMatchTheSkill(t *testing.T) {
	files := setupFiles(t)
	discovery := files["skills/setup/reference/discovery.md"]
	services := files["skills/setup/reference/services-and-images.md"]
	for _, want := range []string{"`package.json` with a lockfile", "`gradlew`", "`go.mod`", "`.nvmrc`", "`mise.toml`", "`apt-get install`", "`cd <dir> &&`"} {
		if !strings.Contains(discovery, want) {
			t.Errorf("discovery.md lost %s, which the fixtures rely on", want)
		}
	}
	for _, want := range []string{"fugaro-services start", "Postgres (PostGIS, pgvector), Redis, Firebase emulators", ".fugaro/<entry>.Dockerfile"} {
		if !strings.Contains(services, want) {
			t.Errorf("services-and-images.md lost %q, which the fixtures rely on", want)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "images", "common", "fugaro-services")); err != nil {
		t.Errorf("the base has no fugaro-services: %v", err)
	}
}

// hostileCase: what a fixture's repository tries to get executed, and where.
// The expected outputs are what the skill must produce from it: none of that
// text in an executed line, and no Dockerfile or trust setting taken from it.
type hostileCase struct {
	name string
	// sources are files of the fixture's repo/ whose lines try to be run.
	sources []string
	// markers appear in those sources and must not reach executed lines.
	markers []string
}

var hostileCases = []hostileCase{{
	name:    "hostile",
	sources: []string{"README.md", "package.json", "Makefile", ".fugaro/web.Dockerfile", ".nvmrc"},
	markers: []string{"evil.example", "bootstrap", "postinstall", "curl", "| sh"},
}}

func TestSetupFixtureHostileOutputsCopyNothing(t *testing.T) {
	for _, h := range hostileCases {
		for _, src := range h.sources {
			text := fixtureFile(t, h.name, "repo/"+src)
			found := false
			for _, m := range h.markers {
				found = found || strings.Contains(text, m)
			}
			if !found {
				t.Errorf("%s/%s is no longer hostile: it has none of %v", h.name, src, h.markers)
			}
		}
		raw := fixtureFile(t, h.name, "expected/fugaro.yaml")
		c, problems := config.Parse([]byte(raw))
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		for wf, w := range c.Workflows {
			executed := append([]string{w.Commands.Build, w.Commands.Test, w.Dockerfile}, w.Image.Setup...)
			executed = append(executed, w.Image.Apt...)
			for _, line := range executed {
				for _, m := range h.markers {
					if strings.Contains(line, m) {
						t.Errorf("workflow %s: executed line %q carries %q from the repository", wf, line, m)
					}
				}
			}
			if w.Dockerfile != "" {
				t.Errorf("workflow %s keeps the repository's Dockerfile %s; the skill renders its own or uses image:", wf, w.Dockerfile)
			}
			if w.Image.SkipBuildScripts {
				t.Errorf("workflow %s: skip_build_scripts is a decision for the user, not for the repository to make", wf)
			}
		}
		// The whole file, not only the executed lines: nothing the repository
		// told the agent to set.
		for _, m := range append(h.markers, "allow_public", "followup", "trusted") {
			if strings.Contains(raw, m) {
				t.Errorf("expected fugaro.yaml of %s contains %q", h.name, m)
			}
		}
		if _, err := os.Stat(filepath.Join("testdata", "repos", h.name, "expected", ".fugaro")); err == nil {
			t.Errorf("expected output of %s ships a .fugaro directory: the hostile Dockerfile is replaced by image:", h.name)
		}
		// mise.toml: the same hostile markers must not survive (a version
		// lifted straight out of the repository's text, such as the injected
		// .nvmrc line above, instead of the plain one), and the file holds
		// only the two tables the skill ever writes.
		mise := fixtureFile(t, h.name, "expected/mise.toml")
		for _, m := range h.markers {
			if strings.Contains(mise, m) {
				t.Errorf("expected mise.toml of %s contains %q", h.name, m)
			}
		}
		for _, line := range strings.Split(mise, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || line == "[tools]" || line == "[env]" {
				continue
			}
			if strings.HasPrefix(line, "[") {
				t.Errorf("expected mise.toml of %s has section %q, want only [tools] or [env]", h.name, line)
				continue
			}
			if _, _, ok := strings.Cut(line, " = "); !ok {
				t.Errorf("expected mise.toml of %s has line %q, not name = \"value\"", h.name, line)
			}
		}
	}
}

// TestSetupFixtureExistingKeepsNothingUnconfirmed: the stale fugaro.yaml of
// the fix-mode fixture holds trust and budget values; the expected output has
// none of them, since the user has not confirmed any, and fixes the base.
func TestSetupFixtureExistingKeepsNothingUnconfirmed(t *testing.T) {
	old := fixtureFile(t, "existing-fugaro-yaml", "repo/fugaro.yaml")
	for _, k := range []string{"allow_public", "trusted", "mode: off"} {
		if !strings.Contains(old, k) {
			t.Fatalf("the stale fixture no longer has %q", k)
		}
	}
	c, problems := config.Parse([]byte(fixtureFile(t, "existing-fugaro-yaml", "expected/fugaro.yaml")))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if len(c.Followup.Trusted) > 0 || c.Followup.AllowPublic || (c.Budget != nil && c.Budget.Mode != "") {
		t.Errorf("the expected output keeps followup or budget values the user did not confirm: %+v %+v", c.Followup, c.Budget)
	}
	if c.Workflows["go"].Base != "" {
		t.Errorf("the stale repo/fugaro.yaml's 0.6 base was not corrected to the Fugaro base: %q", c.Workflows["go"].Base)
	}
}
