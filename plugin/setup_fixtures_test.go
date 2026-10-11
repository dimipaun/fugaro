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
	"io/fs"
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
	// bases maps each expected workflow to its base kind (discovery.md,
	// "The base kind and workflows").
	bases map[string]string
	// dockerfiles lists the workflows built from a Dockerfile in expected/.
	dockerfiles []string
	// versionFile and node: the file that pins Node (discovery.md,
	// "Versions") and the image.node it gives.
	versionFile, node string
	// apt: the CI line whose packages become image.apt, and the packages.
	aptEvidence string
	apt         []string
	// servicesEvidence is a file and the text in it that says the tests need
	// Postgres; the services table then gives the start line.
	servicesFile, servicesText, testPrefix string
}

var fixtures = []fixtureFacts{
	{name: "web-node", bases: map[string]string{"web": "web-node"}, versionFile: ".nvmrc", node: "24.19.0",
		aptEvidence: ".github/workflows/ci.yml", apt: []string{"libvips-dev"}},
	{name: "go", bases: map[string]string{"go": "go"}},
	{name: "java-services", bases: map[string]string{"server": "java-services"},
		servicesFile: "bitbucket-pipelines.yml", servicesText: "postgres", testPrefix: "fugaro-services start"},
	{name: "monorepo", bases: map[string]string{"api": "go", "web": "web-node"}, dockerfiles: []string{"api"}},
	{name: "existing-fugaro-yaml", bases: map[string]string{"go": "go"}},
	{name: "hostile", bases: map[string]string{"web": "web-node"}, versionFile: ".nvmrc", node: "22"},
}

func TestSetupFixtureWebNode(t *testing.T)      { checkFixture(t, "web-node") }
func TestSetupFixtureGo(t *testing.T)           { checkFixture(t, "go") }
func TestSetupFixtureJava(t *testing.T)         { checkFixture(t, "java-services") }
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

	// (b) the Dockerfile each workflow builds from passes the contract.
	for wf, w := range c.Workflows {
		df, repoFile, err := image.Dockerfile(checkout, c, wf, fixtureVersion)
		if err != nil {
			t.Errorf("workflow %s: %v", wf, err)
			continue
		}
		if msgs := config.LintDockerfile(df, w.Base); len(msgs) > 0 {
			t.Errorf("workflow %s: Dockerfile breaks the contract: %v", wf, msgs)
		}
		if (repoFile != "") != slices.Contains(facts.dockerfiles, wf) {
			t.Errorf("workflow %s: dockerfile %q, want a Dockerfile only for %v", wf, repoFile, facts.dockerfiles)
		}
		if repoFile != "" {
			checkDerivedFromRender(t, wf, w.Base, string(df))
		}
	}

	// (c) the discovery facts.
	got := map[string]string{}
	for wf, w := range c.Workflows {
		got[wf] = w.Base
	}
	if !equalMaps(got, facts.bases) {
		t.Errorf("workflows and bases %v, want %v", got, facts.bases)
	}
	if derived := detectBases(t, filepath.Join(dir, "repo")); !slices.Equal(derived, sortedBases(facts.bases)) {
		t.Errorf("the discovery table's base kinds for the repository are %v, want %v", derived, sortedBases(facts.bases))
	}
	for _, w := range c.Workflows {
		if facts.versionFile != "" {
			pinned := strings.TrimSpace(fixtureFile(t, name, "repo/"+facts.versionFile))
			if w.Image.Node != pinned || pinned != facts.node {
				t.Errorf("image.node %q, %s pins %q, want %q", w.Image.Node, facts.versionFile, pinned, facts.node)
			}
		} else if w.Image.Node != "" {
			t.Errorf("image.node %q without version evidence", w.Image.Node)
		}
		if !slices.Equal(w.Image.Apt, facts.apt) {
			t.Errorf("image.apt %v, want %v", w.Image.Apt, facts.apt)
		}
		if facts.aptEvidence != "" {
			ci := fixtureFile(t, name, "repo/"+facts.aptEvidence)
			for _, p := range facts.apt {
				if !strings.Contains(ci, "apt-get install") || !strings.Contains(ci, p) {
					t.Errorf("%s has no apt-get install of %s", facts.aptEvidence, p)
				}
			}
		}
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
	for _, rel := range []string{"expected/fugaro.yaml"} {
		if secretLikeRE.MatchString(fixtureFile(t, name, rel)) {
			t.Errorf("%s looks like it holds a credential", rel)
		}
	}
}

var secretLikeRE = regexp.MustCompile(`(?i)(ghp_|github_pat_|sk-ant-|-----BEGIN|xox[bp]-|AKIA[0-9A-Z]{12})`)

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

func sortedBases(m map[string]string) []string {
	var out []string
	for _, b := range m {
		out = append(out, b)
	}
	slices.Sort(out)
	return out
}

// detectBases applies the discovery table's evidence rows to a repository
// (the root and its directories two levels down, as separate roots) and
// returns the base kind of each root found, sorted.
func detectBases(t *testing.T, repo string) []string {
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
	for _, r := range roots {
		if exists(r, "package.json") && exists(r, "yarn.lock", "pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json") {
			out = append(out, "web-node")
		}
		if exists(r, "gradlew", "gradlew.sh", "settings.gradle", "settings.gradle.kts") {
			out = append(out, "java-services")
		}
		if exists(r, "go.mod") {
			out = append(out, "go")
		}
	}
	slices.Sort(out)
	return out
}

// checkDerivedFromRender: a Dockerfile the skill saved from `fugaro image
// render` keeps every line of the rendered one, in order, and adds only a
// root block that downloads by checksum and pipes nothing into a shell.
func checkDerivedFromRender(t *testing.T, wf, base, df string) {
	t.Helper()
	rendered, err := image.Render(image.RenderInput{Workflow: wf, Base: base, Version: fixtureVersion})
	if err != nil {
		t.Fatal(err)
	}
	have := strings.Split(df, "\n")
	i := 0
	var added []string
	for _, line := range have {
		if i < len(strings.Split(string(rendered), "\n")) && line == strings.Split(string(rendered), "\n")[i] {
			i++
		} else {
			added = append(added, line)
		}
	}
	if i != len(strings.Split(string(rendered), "\n")) {
		t.Errorf("workflow %s: the Dockerfile does not keep the rendered one's lines in order (matched %d)", wf, i)
	}
	text := strings.Join(added, "\n")
	if strings.TrimSpace(text) == "" {
		t.Errorf("workflow %s: the Dockerfile adds nothing to the rendered one; use image: instead", wf)
	}
	if !strings.Contains(text, "USER root") || !strings.Contains(text, "sha256sum -c") {
		t.Errorf("workflow %s: the added lines are not a checksummed install in a root block:\n%s", wf, text)
	}
	if regexp.MustCompile(`\|\s*(sudo\s+)?(ba|z)?sh\b`).MatchString(text) {
		t.Errorf("workflow %s: the added lines pipe into a shell:\n%s", wf, text)
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
			if msgs := config.LintDockerfile(df, base); len(msgs) > 0 {
				t.Errorf("base %s, image %+v: %v", base, img, msgs)
			}
		}
	}
}

// TestSetupFixtureTablesMatchTheSkill: the evidence the fixtures rely on is
// what the skill's discovery table says, and the base images carry what its
// apt row says they carry.
func TestSetupFixtureTablesMatchTheSkill(t *testing.T) {
	files := setupFiles(t)
	discovery := files["skills/setup/reference/discovery.md"]
	services := files["skills/setup/reference/services-and-images.md"]
	for _, want := range []string{"`package.json` with a lockfile", "`gradlew`", "`go.mod`", "`.nvmrc`", "`image.node`", "`image.apt`", "`cd <dir> &&`"} {
		if !strings.Contains(discovery, want) {
			t.Errorf("discovery.md lost %s, which the fixtures rely on", want)
		}
	}
	for _, want := range []string{"fugaro-services start", "Postgres, Redis, Firebase emulators", ".fugaro/<workflow>.Dockerfile"} {
		if !strings.Contains(services, want) {
			t.Errorf("services-and-images.md lost %q, which the fixtures rely on", want)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "images", "common", "fugaro-services")); err != nil {
		t.Errorf("the java-services base has no fugaro-services: %v", err)
	}
	// The packages discovery.md says each base has are in the base's Dockerfile.
	row := regexp.MustCompile("`web-node` and `java-services` have `([^`]+)`; `go` has `([^`]+)`").FindStringSubmatch(discovery)
	if row == nil {
		t.Fatal("discovery.md has no row of the packages each base carries")
	}
	for base, pkgs := range map[string]string{"web-node": row[1], "java-services": row[1], "go": row[2]} {
		df, err := os.ReadFile(filepath.Join("..", "images", base, "Dockerfile"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range strings.Fields(pkgs) {
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(p) + `\b`).Match(df) {
				t.Errorf("discovery.md says the %s base has %s, its Dockerfile does not", base, p)
			}
		}
	}
	// An apt package the fixtures add is not one the base already has.
	for _, f := range fixtures {
		for _, p := range f.apt {
			for _, have := range strings.Fields(row[1] + " " + row[2]) {
				if p == have {
					t.Errorf("fixture %s adds %s, which every base has", f.name, p)
				}
			}
		}
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
	sources: []string{"README.md", "package.json", "Makefile", ".fugaro/web.Dockerfile"},
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
	if c.Workflows["go"].Base != "go" {
		t.Errorf("the stale web-node base was not corrected to go")
	}
}
