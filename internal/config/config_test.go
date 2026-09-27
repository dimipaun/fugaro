package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		{"cpu", minimalYAML + "    resources: { cpu: 3 }\n", "workflows.server.resources.cpu", "must be one of 1, 2, 4, 6, 8", 0},
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
	if err := os.WriteFile(filepath.Join(root, ".fugaro", "server.Dockerfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ps := Check(cfg, root); len(ps) != 0 {
		t.Fatalf("unexpected problems after creating files: %v", ps)
	}
}
