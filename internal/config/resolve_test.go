package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const minimalRepo = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"

func TestResolveMinimalTakesTheDefaultProfile(t *testing.T) {
	l := mustLayer(t, testLayer)
	c, res, ps := Resolve([]byte(minimalRepo), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if c.Layer != l {
		t.Fatal("the resolved config does not carry its layer")
	}
	w, ok := c.Workflows[ImplicitWorkflow]
	if !ok || len(c.Workflows) != 1 {
		t.Fatalf("workflows = %v", c.Workflows)
	}
	if w.Base != "java-services" || w.Commands.Test != "./gradlew test" || w.Profile != "java-service" ||
		w.Timeouts.Total.Duration != 2*time.Hour || w.Timeouts.Stage.Duration != 40*time.Minute {
		t.Fatalf("workflow = %+v", w)
	}
	if c.Git.Provider != "github" || c.Agent.ReviewRounds != 3 || c.Agent.Recipe != "claude-solo" || c.Git.PR.EarlyDraftOn() {
		t.Fatalf("defaults not applied: %+v %+v", c.Git, c.Agent)
	}
	for path, want := range map[string]string{
		"project":                            SourceRepo,
		"git.provider":                       SourceProject,
		"git.pr.labels":                      SourceProject,
		"git.base_branch":                    SourceDefault,
		"workflows.default.commands.test":    SourceProfile("java-service"),
		"workflows.default.profile":          SourceProject,
		"workflows.default.timeouts.stage":   SourceDefault,
		"workflows.default.resources.memory": SourceProfile("java-service"),
		"workflows.default.image.apt":        SourceProfile("java-service"),
		"workflows.default.rebuild.check":    SourceDefault,
		"agent.first_line_rounds":            SourceDefault,
	} {
		if got := res.SourceOf(path); got != want {
			t.Errorf("SourceOf(%s) = %q, want %q", path, got, want)
		}
	}
	if res.LayerSHA256 != LayerSum([]byte(testLayer)) || res.ConfigSHA256 != c.SHA256() {
		t.Fatalf("sums = %+v", res)
	}
}

// The golden table: each row is one repository file over testLayer, and
// the values and sources it must resolve to.
func TestResolveTable(t *testing.T) {
	type want struct{ value, source string }
	for _, tc := range []struct {
		name string
		repo string
		get  map[string]func(*Config) string
		want map[string]want
	}{
		{
			name: "a repository scalar beats the project",
			repo: minimalRepo + "agent:\n  review_rounds: 1\n",
			get:  map[string]func(*Config) string{"agent.review_rounds": func(c *Config) string { return itoa(c.Agent.ReviewRounds) }},
			want: map[string]want{"agent.review_rounds": {"1", SourceRepo}},
		},
		{
			name: "false beats a project true, and the project's false beats the default",
			repo: minimalRepo + "git:\n  pr:\n    checkpoints: false\n",
			get: map[string]func(*Config) string{
				"git.pr.checkpoints": func(c *Config) string { return btoa(c.Git.PR.CheckpointsOn()) },
				"git.pr.early_draft": func(c *Config) string { return btoa(c.Git.PR.EarlyDraftOn()) },
			},
			want: map[string]want{"git.pr.checkpoints": {"false", SourceRepo}, "git.pr.early_draft": {"false", SourceProject}},
		},
		{
			name: "a list is replaced, never appended",
			repo: minimalRepo + "git:\n  pr:\n    labels: [mine]\n",
			get:  map[string]func(*Config) string{"git.pr.labels": func(c *Config) string { return strings.Join(c.Git.PR.Labels, ",") }},
			want: map[string]want{"git.pr.labels": {"mine", SourceRepo}},
		},
		{
			name: "an empty list clears",
			repo: minimalRepo + "git:\n  pr:\n    labels: []\n",
			get:  map[string]func(*Config) string{"git.pr.labels": func(c *Config) string { return strings.Join(c.Git.PR.Labels, ",") }},
			want: map[string]want{"git.pr.labels": {"", SourceRepo}},
		},
		{
			name: "null sets nothing",
			repo: minimalRepo + "git:\n  pr:\n    labels:\n",
			get:  map[string]func(*Config) string{"git.pr.labels": func(c *Config) string { return strings.Join(c.Git.PR.Labels, ",") }},
			want: map[string]want{"git.pr.labels": {"fugaro", SourceProject}},
		},
		{
			name: "profile: picks another profile",
			repo: minimalRepo + "profile: node-web\n",
			get: map[string]func(*Config) string{
				"workflows.default.base":       func(c *Config) string { return c.Workflows["default"].Base },
				"workflows.default.image.node": func(c *Config) string { return c.Workflows["default"].Image.Node },
				"workflows.default.profile":    func(c *Config) string { return c.Workflows["default"].Profile },
			},
			want: map[string]want{
				"workflows.default.base":       {"web-node", SourceProfile("node-web")},
				"workflows.default.image.node": {"20", SourceProfile("node-web")},
				"workflows.default.profile":    {"node-web", SourceRepo},
			},
		},
		{
			name: "a workflow overrides one field of its profile",
			repo: minimalRepo + "workflows:\n  api:\n    profile: java-service\n    commands:\n      test: ./gradlew test -x slow\n    resources: { memory: 8Gi }\n",
			get: map[string]func(*Config) string{
				"workflows.api.commands.test":    func(c *Config) string { return c.Workflows["api"].Commands.Test },
				"workflows.api.commands.build":   func(c *Config) string { return c.Workflows["api"].Commands.Build },
				"workflows.api.resources.cpu":    func(c *Config) string { return itoa(c.Workflows["api"].Resources.CPU) },
				"workflows.api.resources.memory": func(c *Config) string { return c.Workflows["api"].Resources.Memory },
			},
			want: map[string]want{
				"workflows.api.commands.test":    {"./gradlew test -x slow", SourceRepo},
				"workflows.api.commands.build":   {"./gradlew assemble", SourceProfile("java-service")},
				"workflows.api.resources.cpu":    {"4", SourceProfile("java-service")},
				"workflows.api.resources.memory": {"8Gi", SourceRepo},
			},
		},
		{
			name: "a workflow without profile: takes nothing from any profile",
			repo: minimalRepo + "workflows:\n  web:\n    base: go\n    commands: { build: make, test: make test }\n",
			get: map[string]func(*Config) string{
				"workflows.web.resources.memory": func(c *Config) string { return c.Workflows["web"].Resources.Memory },
				"workflows.web.timeouts.total":   func(c *Config) string { return c.Workflows["web"].Timeouts.Total.String() },
			},
			want: map[string]want{
				"workflows.web.resources.memory": {"8Gi", SourceDefault},
				"workflows.web.timeouts.total":   {"1h30m0s", SourceDefault},
			},
		},
		{
			name: "a repository Dockerfile replaces the profile's image settings",
			repo: minimalRepo + "workflows:\n  api:\n    profile: java-service\n    dockerfile: .fugaro/api.Dockerfile\n",
			get: map[string]func(*Config) string{
				"workflows.api.image.apt":  func(c *Config) string { return strings.Join(c.Workflows["api"].Image.Apt, ",") },
				"workflows.api.dockerfile": func(c *Config) string { return c.Workflows["api"].Dockerfile },
			},
			want: map[string]want{
				"workflows.api.image.apt":  {"", SourceDefault},
				"workflows.api.dockerfile": {".fugaro/api.Dockerfile", SourceRepo},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, res, ps := Resolve([]byte(tc.repo), mustLayer(t, testLayer))
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			for path, w := range tc.want {
				if got := tc.get[path](c); got != w.value {
					t.Errorf("%s = %q, want %q", path, got, w.value)
				}
				if got := res.SourceOf(path); got != w.source {
					t.Errorf("source of %s = %q, want %q", path, got, w.source)
				}
			}
		})
	}
}

func TestResolveProblemsNameTheLayer(t *testing.T) {
	l := mustLayer(t, strings.Replace(testLayer, "default_profile:", "  bare:\n    base: go\ndefault_profile:", 1))
	for _, tc := range []struct{ name, repo, want string }{
		{"an unknown profile", minimalRepo + "profile: nope\n", `profile: names profile "nope", which project acme's layer does not have (its profiles: bare, java-service, node-web)`},
		{"profile: beside workflows:", minimalRepo + "profile: node-web\nworkflows:\n  web: { base: go, commands: { build: a, test: b } }\n", "applies only to a fugaro.yaml with no workflows:"},
		{"a value the repository set says nothing more", minimalRepo + "workflows:\n  api:\n    profile: node-web\n    commands: { test: '' }\n", "workflows.api.commands.test: is required; "},
		{"a value the profile set names the profile", minimalRepo + "workflows:\n  api:\n    profile: node-web\n    base: go\n", "workflows.api.image.node: only applies to base web-node (set by profile node-web)"},
		{"a value neither set names the profile", minimalRepo + "workflows:\n  api:\n    profile: bare\n", "workflows.api.commands.build: is required (set neither by the repository nor by profile bare)"},
		{"a value the profile set names the profile for a bidi workflow name", minimalRepo + "workflows:\n  \"a\\u202eb\":\n    profile: bare\n", "commands.build: is required (set neither by the repository nor by profile bare)"},
		{"another project's layer", "version: 1\nproject: other\ngcp_project: acme-fugaro\n", `the project layer given is project "acme"'s`},
		{"an unanchored file", "version: 1\nproject: acme\n", "the project layer applies only to a fugaro.yaml whose gcp_project: names it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ps := Resolve([]byte(tc.repo), l)
			var msgs []string
			for _, p := range ps {
				msgs = append(msgs, p.String())
			}
			if got := strings.Join(msgs, "; ") + "; "; !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveWithoutLayerIsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", "valid", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no corpus")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		c, res, ps := Resolve(data, nil)
		if len(ps) > 0 || c == nil || res.LayerSHA256 != "" || res.ConfigSHA256 != c.SHA256() {
			t.Errorf("%s: %v", f, ps)
		}
	}
	_, _, ps := Resolve([]byte(minimalRepo+"profile: x\n"), nil)
	if len(ps) != 1 || ps[0].Code != CodeNeedsLayer {
		t.Fatalf("a profile with no layer: %v", ps)
	}
	_, _, ps = Resolve([]byte(minimalRepo+"git: { provider: github }\n"), nil)
	if len(ps) == 0 || !strings.Contains(ps[len(ps)-1].Message, "takes one from project acme's layer") || ps[len(ps)-1].Code != CodeNeedsLayer {
		t.Fatalf("an anchored file with no workflows: %v", ps)
	}
	// Without a layer every key the file sets is the repository's.
	_, res, ps := Resolve([]byte(minimalRepo+"git: { provider: github }\nworkflows:\n  api: { base: go, commands: { build: a, test: b } }\n"), nil)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for path, want := range map[string]string{"git.provider": SourceRepo, "workflows.api.commands.test": SourceRepo, "git.base_branch": SourceDefault} {
		if got := res.SourceOf(path); got != want {
			t.Errorf("SourceOf(%s) = %q, want %q", path, got, want)
		}
	}
}

// anchored is a corpus file anchored to GCP project acme-fugaro, and a
// project layer for it that sets nothing.
func anchored(t *testing.T, data []byte) ([]byte, *ProjectLayer) {
	t.Helper()
	project, err := ProjectOf(data)
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := GCPProjectOf(data); g == "" {
		data = append([]byte("gcp_project: acme-fugaro\n"), data...)
	}
	g, _ := GCPProjectOf(data)
	l, ps := ParseProjectLayer([]byte("version: 1\nproject: "+project+"\ngcp_project: "+g+"\n"), LayerAnchor{Project: project, GCPProject: g})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return data, l
}

// TestResolveOverAnEmptyLayerIsParse is the merge's property: a layer that
// sets nothing changes nothing, so every valid file resolves to the config
// Parse reads, byte for byte.
func TestResolveOverAnEmptyLayerIsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", "valid", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no corpus")
	}
	cases := map[string][]byte{
		"literals": []byte(literalsRepo),
		"anchors and merge keys": []byte(minimalRepo + "git: { provider: github }\nworkflows:\n" +
			"  api: &w\n    base: go\n    commands: &c { build: make, test: make test }\n    timeouts: { total: 1h }\n" +
			"  web:\n    <<: *w\n    commands: { <<: *c, test: make check }\n" +
			"  cli: *w\n"),
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		cases[filepath.Base(f)] = data
	}
	for name, data := range cases {
		data, l := anchored(t, data)
		want, ps := Parse(data)
		if len(ps) > 0 {
			t.Fatalf("%s: Parse: %v", name, ps)
		}
		c, res, ps := Resolve(data, l)
		if len(ps) > 0 {
			t.Errorf("%s: %v", name, ps)
			continue
		}
		if res.ConfigSHA256 != want.SHA256() {
			t.Errorf("%s: resolves to %+v, Parse reads %+v", name, *c, *want)
		}
	}
}

// literalsRepo holds values YAML would read as numbers, a date or a bool,
// and folded, literal and tagged scalars whose decoded value depends on
// chomping, a more-indented line or an explicit tag, not just its text.
const literalsRepo = minimalRepo + `git:
  provider: github
  base_branch: 1.10
  pr:
    labels: [1.10, 1e3, 0x10, yes]
    early_draft: !!bool >-
      true
agent:
  model: 0x10
  review_rounds: !!int |-
    2
workflows:
  api:
    base: go
    commands: { build: 1e3, test: 010 }
  lit1:
    base: go
    commands:
      build: >
        folded  text
          more
      test: >
        a
         b
        c
  lit2:
    base: go
    commands:
      build: >+
        a

      test: >-
        a

  lit3:
    base: go
    commands:
      build: |+
        a

      test: !!binary |
        SGVsbG8=
`

// TestResolveKeepsLiterals: a value reaches the resolved config as it was
// written, from either layer, never decoded and printed again.
func TestResolveKeepsLiterals(t *testing.T) {
	// want is Parse's own decode of literalsRepo: the oracle the merge must
	// match, since Parse never goes through the node merge at all.
	want, ps := Parse([]byte(literalsRepo))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	l := mustLayer(t, strings.Replace(strings.Replace(strings.Replace(testLayer, "labels: [fugaro]", "labels: [1.10, 0x10]", 1),
		"build: npm run build, test: npm test", "build: 1e3, test: 010", 1),
		"default_profile:", "  lit:\n    base: go\n    commands:\n      build: >\n        folded  text\n          more\n"+
			"      test: |+\n        a\n\ndefault_profile:", 1))
	c, _, ps := Resolve([]byte(literalsRepo), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	api := c.Workflows["api"]
	if c.Git.BaseBranch != "1.10" || c.Agent.Model != "0x10" || strings.Join(c.Git.PR.Labels, ",") != "1.10,1e3,0x10,yes" ||
		api.Commands.Build != "1e3" || api.Commands.Test != "010" {
		t.Fatalf("repository values changed: %q %q %q %+v", c.Git.BaseBranch, c.Agent.Model, c.Git.PR.Labels, api.Commands)
	}
	// review_rounds and early_draft are tagged (!!int, !!bool) block
	// scalars: a merge that drops the tag re-marshals them as plain
	// double-quoted strings, which fails to decode into an int or a bool.
	if c.Agent.ReviewRounds != want.Agent.ReviewRounds || c.Git.PR.EarlyDraftOn() != want.Git.PR.EarlyDraftOn() {
		t.Fatalf("tagged block scalars changed: review_rounds=%v (want %v) early_draft=%v (want %v)",
			c.Agent.ReviewRounds, want.Agent.ReviewRounds, c.Git.PR.EarlyDraftOn(), want.Git.PR.EarlyDraftOn())
	}
	// Folded, literal, chomping, a more-indented line and an explicit tag
	// (lit1-lit3 in literalsRepo) must reach the resolved config exactly as
	// Parse, which never merges nodes, reads them.
	for _, name := range []string{"lit1", "lit2", "lit3"} {
		got, w := c.Workflows[name].Commands, want.Workflows[name].Commands
		if got.Build != w.Build || got.Test != w.Test {
			t.Errorf("%s: commands = %+v, want %+v (Parse)", name, got, w)
		}
	}
	c, _, ps = Resolve([]byte(minimalRepo+"git:\n  base_branch: 2026-10-08\nprofile: node-web\n"), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	w := c.Workflows[ImplicitWorkflow]
	if c.Git.BaseBranch != "2026-10-08" || strings.Join(c.Git.PR.Labels, ",") != "1.10,0x10" || w.Commands.Build != "1e3" || w.Commands.Test != "010" {
		t.Fatalf("layer values changed: %q %q %+v", c.Git.BaseBranch, c.Git.PR.Labels, w.Commands)
	}
	// The layer side: a profile's own folded and literal scalars must reach
	// the resolved config exactly as the layer's own strict decode read
	// them (l.Profiles, which never merges nodes either).
	c, _, ps = Resolve([]byte(minimalRepo+"profile: lit\n"), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	got, wantLit := c.Workflows[ImplicitWorkflow].Commands, l.Profiles["lit"].Commands
	if got.Build != wantLit.Build || got.Test != wantLit.Test {
		t.Fatalf("layer profile commands = %+v, want %+v (the layer's own decode)", got, wantLit)
	}
}

// TestResolveRefusesKeysThatAreNotStrings: a workflow key YAML reads as a
// number, a bool or null would decode as another type and lose its
// workflow, so it is refused, with or without a layer.
func TestResolveRefusesKeysThatAreNotStrings(t *testing.T) {
	wf := "    base: go\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct{ name, repo, want string }{
		{"int", minimalRepo + "workflows:\n  1:\n" + wf, `line 5: has, under workflows, the key "1", which YAML reads as int, not a string; quote it`},
		{"bool", minimalRepo + "workflows:\n  true:\n" + wf, `line 5: has, under workflows, the key "true", which YAML reads as bool, not a string; quote it`},
		{"null", minimalRepo + "workflows:\n  ~:\n" + wf, `line 5: has, under workflows, the key "~", which YAML reads as null, not a string; quote it`},
		{"mixed", minimalRepo + "workflows:\n  api:\n" + wf + "  2:\n" + wf, `line 8: has, under workflows, the key "2", which YAML reads as int, not a string; quote it`},
	} {
		for _, l := range []*ProjectLayer{nil, mustLayer(t, testLayer)} {
			t.Run(tc.name, func(t *testing.T) {
				_, _, ps := Resolve([]byte(tc.repo), l)
				if len(ps) != 1 || !strings.HasPrefix(ps[0].String(), tc.want) {
					t.Fatalf("layer %v: problems %v, want %q", l != nil, ps, tc.want)
				}
			})
		}
	}
}

// TestResolveEscapesNames: a project or workflow name in a problem is
// printed with its control and formatting characters escaped.
func TestResolveEscapesNames(t *testing.T) {
	l := mustLayer(t, testLayer)
	for _, tc := range []struct {
		name, repo string
		l          *ProjectLayer
	}{
		{"no layer hint", "version: 1\nproject: \"ac\\e[31mme\\u202e\"\ngcp_project: acme-fugaro\n", nil},
		{"profile with no layer", minimalRepo + "workflows:\n  \"a\\e[2Jb\\u202e\":\n    profile: x\n", nil},
		{"unknown profile", minimalRepo + "workflows:\n  \"a\\e[2Jb\\u202e\":\n    profile: nope\n", l},
		{"workflow name", minimalRepo + "git: { provider: github }\nworkflows:\n  \"a\\e[2Jb\\u202e\": { base: go, commands: { build: a, test: b } }\n", nil},
		// yaml.v3 percent-decodes a verbatim tag, so a key's own tag (not
		// just its value) can carry control or formatting characters.
		{"a key's tag", minimalRepo + "workflows:\n  !<tag:x,2000:%1B%5B2J%E2%80%AE> api:\n    base: go\n    commands: { build: a, test: b }\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ps := Resolve([]byte(tc.repo), tc.l)
			if len(ps) == 0 {
				t.Fatal("no problems")
			}
			for _, p := range ps {
				if s := p.String(); strings.ContainsAny(s, "\x1b\u202e") {
					t.Fatalf("problem %q prints a control or formatting character", s)
				}
			}
		})
	}
}

func TestResolveSourceOfListsAndAncestors(t *testing.T) {
	l := mustLayer(t, testLayer)
	_, res, ps := Resolve([]byte(minimalRepo+"workflows:\n  api:\n    profile: java-service\n    cache: [{ key: [a], paths: [b] }]\n"), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for path, want := range map[string]string{
		"git.pr.labels[0]":              SourceProject,
		"workflows.api.cache[0].key[0]": SourceRepo,
		"workflows.api.image.apt[0]":    SourceProfile("java-service"),
		"workflows.api.commands.test.x": SourceProfile("java-service"),
		"workflows.api.cache":           SourceRepo,
		"workflows.api.timeouts.stage":  SourceDefault,
	} {
		if got := res.SourceOf(path); got != want {
			t.Errorf("SourceOf(%s) = %q, want %q", path, got, want)
		}
	}
}

// TestResolveEmptyDockerfileKeepsTheProfileImage: dockerfile: "" names no
// Dockerfile, so it does not drop the profile's image: block.
func TestResolveEmptyDockerfileKeepsTheProfileImage(t *testing.T) {
	c, res, ps := Resolve([]byte(minimalRepo+"workflows:\n  api:\n    profile: java-service\n    dockerfile: \"\"\n"), mustLayer(t, testLayer))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if got := strings.Join(c.Workflows["api"].Image.Apt, ","); got != "graphviz" || res.SourceOf("workflows.api.image.apt") != SourceProfile("java-service") {
		t.Fatalf("image.apt = %q from %s", got, res.SourceOf("workflows.api.image.apt"))
	}
}

// TestResolveWorkflowProfileAllDigits: an unquoted all-digit profile name
// (such as 12345) decodes as a YAML int in the plain tree, but is a valid
// profile name (ProjectNameRE); Resolve must read it through the
// strict-typed Config.Workflows[name].Profile, not an untyped assertion
// on the tree, so an unknown one is reported rather than silently treated
// as no profile (which would surface as unrelated "commands.test is
// required" errors instead).
func TestResolveWorkflowProfileAllDigits(t *testing.T) {
	l := mustLayer(t, testLayer)
	_, _, ps := Resolve([]byte(minimalRepo+"workflows:\n  api:\n    profile: 12345\n"), l)
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.String())
	}
	want := `workflows.api.profile: names profile "12345", which project acme's layer does not have (its profiles: java-service, node-web)`
	if got := strings.Join(msgs, "; "); !strings.Contains(got, want) {
		t.Fatalf("problems %q, want %q", got, want)
	}
}

// TestResolveEmptyWorkflowsBlockFallsBackToProfile: an explicit
// `workflows: {}` must be treated the same as omitting workflows: or
// writing `workflows:` with no value, taking the implicit workflow from
// profile: or the project's default_profile, not merged as a (valid but
// empty) workflows: map that only later fails Validate's generic
// "must define at least one workflow", losing the project-layer hint.
func TestResolveEmptyWorkflowsBlockFallsBackToProfile(t *testing.T) {
	l := mustLayer(t, testLayer)
	c, res, ps := Resolve([]byte(minimalRepo+"workflows: {}\n"), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	w, ok := c.Workflows[ImplicitWorkflow]
	if !ok || len(c.Workflows) != 1 || w.Profile != "java-service" {
		t.Fatalf("workflows = %v", c.Workflows)
	}
	if got := res.SourceOf("workflows.default.profile"); got != SourceProject {
		t.Fatalf("source = %q, want %q", got, SourceProject)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	l := mustLayer(t, testLayer)
	first, _, _ := Resolve([]byte(minimalRepo), l)
	for range 20 {
		c, _, _ := Resolve([]byte(minimalRepo), l)
		if c.SHA256() != first.SHA256() {
			t.Fatal("two resolutions of the same inputs differ")
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func btoa(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestLayeredCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "project-layer", "valid", "full.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	l, ps := ParseProjectLayer(data, LayerAnchor{Project: "aurora", GCPProject: "proj-1234"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, kind := range []string{"valid", "invalid"} {
		files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", "layered", kind, "*.yaml"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no layered %s corpus", kind)
		}
		for _, f := range files {
			repo, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			c, _, ps := Resolve(repo, l)
			switch {
			case kind == "valid" && len(ps) > 0:
				t.Errorf("%s: unexpected problems %v", f, ps)
			case kind == "invalid" && c != nil:
				t.Errorf("%s: resolved without problems, want invalid", f)
			}
		}
	}
}
