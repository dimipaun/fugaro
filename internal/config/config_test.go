package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const minimalYAML = `
version: 1
git:
  provider: github
workflows:
  server:
    base: server-jvm
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
`

func TestParseAppliesDefaults(t *testing.T) {
	cfg, problems := Parse([]byte(minimalYAML))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Git.BaseBranch != "main" {
		t.Errorf("base_branch = %q, want main", cfg.Git.BaseBranch)
	}
	if cfg.Agent.Auth != "vertex" || cfg.Agent.ReviewRounds != 2 || cfg.Agent.MaxBudgetUSD != 25 {
		t.Errorf("agent defaults = %+v", cfg.Agent)
	}
	w := cfg.Workflows["server"]
	if w.Resources.CPU != 8 || w.Resources.Memory != "32Gi" {
		t.Errorf("resources = %+v, want 8 / 32Gi", w.Resources)
	}
	if got := w.Commands.Reports; len(got) != 1 || got[0] != "**/build/test-results/**/*.xml" {
		t.Errorf("reports = %v", got)
	}
	to := w.Timeouts
	if to.Total.Duration != 90*time.Minute || to.Stage.Duration != 40*time.Minute ||
		to.Verify.Duration != 30*time.Minute || to.FinalizeReserve.Duration != 5*time.Minute {
		t.Errorf("timeouts = %+v", to)
	}
}

func TestShortTotalScalesDefaults(t *testing.T) {
	cfg, problems := Parse([]byte(minimalYAML + "    timeouts: { total: 10m }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	to := cfg.Workflows["server"].Timeouts
	if to.Stage.Duration != 10*time.Minute || to.Verify.Duration != 10*time.Minute || to.FinalizeReserve.Duration != 2*time.Minute {
		t.Errorf("timeouts = %+v, want stage 10m, verify 10m, reserve 2m", to)
	}
}

func TestParseProblems(t *testing.T) {
	cases := []struct {
		name, yaml, path, msg string
		line                  int
	}{
		{"empty", "", "", "file is empty", 0},
		{"unknown field", minimalYAML + "    color: blue\n", "", "field color not found", 11},
		{"bad duration", minimalYAML + "    timeouts: { total: soon }\n", "", "invalid duration", 11},
		{"version", strings.Replace(minimalYAML, "version: 1", "version: 2", 1), "version", "must be 1", 0},
		{"provider", strings.Replace(minimalYAML, "github", "gitlab", 1), "git.provider", "must be one of github, bitbucket", 0},
		{"missing test", strings.Replace(minimalYAML, "      test: ./gradlew test\n", "", 1), "workflows.server.commands.test", "is required", 0},
		{"no workflows", "version: 1\ngit: { provider: github }\n", "workflows", "at least one workflow", 0},
		{"workflow name", strings.Replace(minimalYAML, "  server:", "  Server:", 1), "workflows.Server", "workflow name must match", 0},
		{"reserved env", minimalYAML + "    secrets:\n      - { name: tok, env: GIT_TOKEN }\n", "workflows.server.secrets[0].env", "reserved prefix", 0},
		{"duplicate env", minimalYAML + "    secrets:\n      - { name: a, env: TOK }\n      - { name: b, env: TOK }\n", "workflows.server.secrets[1].env", "declared twice", 0},
		{"rerun each", minimalYAML + "      rerun_failed: { command: ./gradlew test, each: --tests }\n", "workflows.server.commands.rerun_failed.each", "must contain {id}", 0},
		{"reserve too long", minimalYAML + "    timeouts: { total: 10m, finalize_reserve: 10m }\n", "workflows.server.timeouts.finalize_reserve", "shorter than timeouts.total", 0},
		{"stage too long", minimalYAML + "    timeouts: { total: 10m, stage: 20m }\n", "workflows.server.timeouts.stage", "must not exceed timeouts.total", 0},
		{"memory", minimalYAML + "    resources: { memory: 32GB }\n", "workflows.server.resources.memory", "must look like", 0},
		{"cpu", minimalYAML + "    resources: { cpu: -1 }\n", "workflows.server.resources.cpu", "must be at least 1", 0},
		{"reserved secret", minimalYAML + "    secrets:\n      - { name: claude-oauth-token, env: TOK }\n", "workflows.server.secrets[0].name", "reserved", 0},
		{"cache key", minimalYAML + "    cache:\n      - { key: [], paths: [~/.gradle] }\n", "workflows.server.cache[0].key", "at least one file", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, problems := Parse([]byte(tc.yaml))
			if cfg != nil {
				t.Fatal("expected no config when there are problems")
			}
			if !hasProblem(problems, tc.path, tc.msg, tc.line) {
				t.Fatalf("want problem path=%q msg~%q line=%d, got %v", tc.path, tc.msg, tc.line, problems)
			}
		})
	}
}

func hasProblem(ps []Problem, path, msg string, line int) bool {
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, msg) && (line == 0 || p.Line == line) {
			return true
		}
	}
	return false
}

func TestSelectWorkflow(t *testing.T) {
	cfg, _ := Parse([]byte(minimalYAML))
	if name, _, err := cfg.SelectWorkflow(""); err != nil || name != "server" {
		t.Fatalf("SelectWorkflow(\"\") = %q, %v", name, err)
	}
	if _, _, err := cfg.SelectWorkflow("web"); err == nil || !strings.Contains(err.Error(), `no workflow "web"`) {
		t.Fatalf("SelectWorkflow(web) err = %v", err)
	}
	two := minimalYAML + "  web:\n    base: web-node\n    commands: { build: pnpm build, test: pnpm test }\n"
	cfg2, problems := Parse([]byte(two))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if _, _, err := cfg2.SelectWorkflow(""); err == nil || !strings.Contains(err.Error(), "server, web") {
		t.Fatalf("ambiguous SelectWorkflow err = %v", err)
	}
	name, w, err := cfg2.SelectWorkflow("web")
	if err != nil || name != "web" || w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" {
		t.Fatalf("SelectWorkflow(web) = %q %+v %v", name, w.Resources, err)
	}
}

func TestCheckMissingFiles(t *testing.T) {
	root := t.TempDir()
	cfg, problems := Parse([]byte(minimalYAML + "    dockerfile: .fugaro/server.Dockerfile\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	ps := Check(cfg, root)
	for _, path := range []string{"workflows.server.commands.build", "workflows.server.commands.test", "workflows.server.dockerfile"} {
		if !hasProblem(ps, path, "does not exist", 0) {
			t.Errorf("missing problem for %s in %v", path, ps)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "gradlew"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".fugaro", "server.Dockerfile"), []byte(validRepoDockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if ps := Check(cfg, root); len(ps) != 0 {
		t.Fatalf("unexpected problems after creating files: %v", ps)
	}
}

func TestRebuildDefaults(t *testing.T) {
	cfg, ps := Parse([]byte(minimalYAML))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	r := cfg.Workflows["server"].Rebuild
	if r.Check != "daily" || r.MaxAge.Duration != 14*24*time.Hour || r.Lockfiles == nil || !*r.Lockfiles || r.Base == nil || !*r.Base || r.Paths == nil || len(r.Paths) != 0 {
		t.Errorf("rebuild = %+v, want the defaults", r)
	}
	d := Rebuild{}.Defaults()
	if d.Check != "daily" || d.MaxAge.Duration != 14*24*time.Hour || !*d.Lockfiles || !*d.Base || d.Paths == nil {
		t.Errorf("Defaults() = %+v", d)
	}
}

func TestRebuildExplicitValues(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/valid/rebuild-full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ps := Parse(data)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	web, srv := cfg.Workflows["web"].Rebuild, cfg.Workflows["server"].Rebuild
	if web.MaxAge.Duration != 30*24*time.Hour || *web.Base != false || !*web.Lockfiles || len(web.Paths) != 2 {
		t.Errorf("web rebuild = %+v", web)
	}
	if srv.Check != "off" || srv.MaxAge.Duration != 0 {
		t.Errorf("an explicit max_age of 0 must stay 0: %+v", srv)
	}
}

func TestRebuildValidation(t *testing.T) {
	for file, path := range map[string]string{
		"rebuild-bad-check": "workflows.web.rebuild.check",
		"rebuild-bad-age":   "workflows.web.rebuild.max_age",
		"rebuild-bad-glob":  "workflows.web.rebuild.paths[0]",
		"rebuild-short-age": "workflows.web.rebuild.max_age",
	} {
		data, err := os.ReadFile("../../testdata/config/invalid/" + file + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		cfg, ps := Parse(data)
		if cfg != nil || !hasProblem(ps, path, "", 0) {
			t.Errorf("%s: want a problem at %s, got %v", file, path, ps)
		}
	}
	data, _ := os.ReadFile("../../testdata/config/invalid/rebuild-unknown-field.yaml")
	if cfg, ps := Parse(data); cfg != nil || !hasProblem(ps, "", "field smoke not found", 0) {
		t.Errorf("unknown field: got %v", ps)
	}
}

func TestRebuildPathAndAgeRules(t *testing.T) {
	for _, tc := range []struct {
		block string
		ok    bool
	}{
		{"{ max_age: 1h }", true}, {"{ max_age: 90d }", true}, {"{ max_age: 91d }", false}, {"{ max_age: -1h }", false},
		{"{ max_age: 0 }", true}, {"{ max_age: 1d12h }", true}, {"{ max_age: soon }", false},
		{"{ max_age: 0d }", true}, {"{ max_age: 2d-5h }", false}, {"{ max_age: 2d+5h }", false}, {"{ max_age: 1d-0s }", false},
		{`{ paths: ["a/**/b"] }`, true}, {`{ paths: ["/abs"] }`, false}, {`{ paths: [""] }`, false},
		{`{ paths: ["a/../b"] }`, false}, {`{ paths: ["[a"] }`, false}, {`{ paths: [".."] }`, false},
	} {
		_, ps := Parse([]byte(minimalYAML + "    rebuild: " + tc.block + "\n"))
		if (len(ps) == 0) != tc.ok {
			t.Errorf("%s: ok=%v, problems %v", tc.block, tc.ok, ps)
		}
	}
}

// Days lead a Go duration, whole and unsigned: 0d is zero, and a signed
// remainder after the days is refused rather than subtracted or added.
func TestDurationDays(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"0d", 0, true}, {"14d", 14 * 24 * time.Hour, true}, {"1d12h", 36 * time.Hour, true}, {"90m", 90 * time.Minute, true},
		{"1d-5h", 0, false}, {"1d+5h", 0, false}, {"-1d", 0, false}, {"d", 0, false}, {"1dx", 0, false},
	} {
		var d Duration
		err := yaml.Unmarshal([]byte(tc.in), &d)
		if (err == nil) != tc.ok || (tc.ok && (d.Duration != tc.want || !d.Set)) {
			t.Errorf("%q = %v (set %v), %v; want %v, ok %v", tc.in, d.Duration, d.Set, err, tc.want, tc.ok)
		}
	}
}

// Days are accepted wherever a duration is, as the schema's duration and
// max_age patterns allow them; a signed remainder is refused.
func TestDurationFixtures(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/valid/timeouts-days.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ps := Parse(data)
	if len(ps) != 0 || cfg.Workflows["web"].Timeouts.Total.Duration != 24*time.Hour || !cfg.Workflows["web"].Rebuild.MaxAge.Set {
		t.Fatalf("timeouts-days: %v", ps)
	}
	data, err = os.ReadFile("../../testdata/config/invalid/rebuild-mixed-sign-age.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ps := Parse(data); len(ps) == 0 || !strings.Contains(ps[0].Message, "invalid duration") {
		t.Fatalf("rebuild-mixed-sign-age: %v", ps)
	}
}
