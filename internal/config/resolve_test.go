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
	l := mustLayer(t, testLayer)
	for _, tc := range []struct{ name, repo, want string }{
		{"an unknown profile", minimalRepo + "profile: nope\n", `profile: names profile "nope", which project acme's layer does not have (its profiles: java-service, node-web)`},
		{"profile: beside workflows:", minimalRepo + "profile: node-web\nworkflows:\n  web: { base: go, commands: { build: a, test: b } }\n", "applies only to a fugaro.yaml with no workflows:"},
		{"a missing command names the profile", minimalRepo + "workflows:\n  api:\n    profile: node-web\n    commands: { test: '' }\n", "workflows.api.commands.test: is required"},
		{"a value the profile set", minimalRepo + "workflows:\n  api:\n    profile: java-service\n    timeouts: { stage: 3h }\n", "workflows.api.timeouts.stage: must not exceed timeouts.total"},
		{"another project's layer", "version: 1\nproject: other\ngcp_project: acme-fugaro\n", `the project layer given is project "acme"'s`},
		{"an unanchored file", "version: 1\nproject: acme\n", "the project layer applies only to a fugaro.yaml whose gcp_project: names it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ps := Resolve([]byte(tc.repo), l)
			var msgs []string
			for _, p := range ps {
				msgs = append(msgs, p.String())
			}
			if got := strings.Join(msgs, "; "); !strings.Contains(got, tc.want) {
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
	if len(ps) == 0 || !strings.Contains(ps[len(ps)-1].Message, "takes one from project acme's layer") {
		t.Fatalf("an anchored file with no workflows: %v", ps)
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
