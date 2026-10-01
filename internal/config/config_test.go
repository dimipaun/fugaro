package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const minimalYAML = `
version: 1
project: aurora
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
		{"unknown field", minimalYAML + "    color: blue\n", "", "field color not found", 12},
		{"bad duration", minimalYAML + "    timeouts: { total: soon }\n", "", "invalid duration", 12},
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

func TestFollowupDefaults(t *testing.T) {
	cfg, ps := Parse([]byte(minimalYAML))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(cfg.Followup.Trusted) != 0 || cfg.Followup.AllowPublic {
		t.Errorf("followup = %+v, want no trusted IDs and public repositories refused", cfg.Followup)
	}
	data, err := os.ReadFile("../../testdata/config/valid/followup-full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg, ps = Parse(data); len(ps) > 0 {
		t.Fatal(ps)
	}
	if got := cfg.Followup; len(got.Trusted) != 2 || got.Trusted[0] != "1234567" || got.Trusted[1] != "7654321" || !got.AllowPublic {
		t.Errorf("followup = %+v", got)
	}
}

// Who can steer a follow-up is a list of account IDs that a rename can't
// change, in the form the provider's adapter matches comment authors by.
func TestFollowupConfigValidation(t *testing.T) {
	for file, want := range map[string]struct{ path, msg string }{
		"followup-bad-github-id":    {"followup.trusted[0]", "gh api users/octocat --jq .id"},
		"followup-bad-bitbucket-id": {"followup.trusted[0]", "account_id"},
		"followup-bitbucket-uuid":   {"followup.trusted[0]", "UUID"},
		"followup-duplicate-id":     {"followup.trusted[1]", "listed twice"},
		"followup-unknown-field":    {"", "field trust_pr_author not found"},
	} {
		data, err := os.ReadFile("../../testdata/config/invalid/" + file + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		cfg, ps := Parse(data)
		if cfg != nil || !hasProblem(ps, want.path, want.msg, 0) {
			t.Errorf("%s: want a problem at %q mentioning %q, got %v", file, want.path, want.msg, ps)
		}
	}
	github := minimalYAML
	bitbucket := strings.Replace(minimalYAML, "provider: github", "provider: bitbucket", 1)
	for _, tc := range []struct {
		base, ids string
		ok        bool
	}{
		{github, `[1]`, true},
		{github, `["12345678901234567890"]`, true},
		{github, `["123456789012345678901"]`, false},
		{github, `[""]`, false},
		{github, `[0]`, false},
		{github, `["0"]`, false},
		{github, `["01234567"]`, false},
		{github, `["10"]`, true},
		{github, `["-1"]`, false},
		{github, `["557058:00000000-0000-0000-0000-000000000001"]`, false},
		{bitbucket, `["557058:00000000-0000-0000-0000-000000000001"]`, true},
		{bitbucket, `["5b10ac8d82e05b22cc7d4ef5"]`, true},
		{bitbucket, `["1234567"]`, false},
		{bitbucket, `["557058:00000000-0000-0000-0000-00000000000G"]`, false},
		{bitbucket, `["12345678901:00000000-0000-0000-0000-000000000001"]`, false},
		{bitbucket, `["5B10AC8D82E05B22CC7D4EF5"]`, false},
		{bitbucket, `["557058:ABCDEF00-0000-0000-0000-000000000001"]`, false},
		{bitbucket, `["00000000-0000-0000-0000-000000000001"]`, false},
	} {
		_, ps := Parse([]byte(tc.base + "followup: { trusted: " + tc.ids + " }\n"))
		if (len(ps) == 0) != tc.ok {
			t.Errorf("%s on %s: ok=%v, problems %v", tc.ids, tc.base[strings.Index(tc.base, "provider"):strings.Index(tc.base, "\nworkflows")], tc.ok, ps)
		}
	}
}

// An upper-case account_id would never match Bitbucket's lower-case one, and
// the message says the case is all that is wrong with it.
func TestFollowupBitbucketUpperCaseHint(t *testing.T) {
	bitbucket := strings.Replace(minimalYAML, "provider: github", "provider: bitbucket", 1)
	for _, id := range []string{"5B10AC8D82E05B22CC7D4EF5", "557058:ABCDEF00-0000-0000-0000-000000000001"} {
		_, ps := Parse([]byte(bitbucket + "followup: { trusted: [\"" + id + "\"] }\n"))
		if !hasProblem(ps, "followup.trusted[0]", "lower case", 0) {
			t.Errorf("%s: want a lower-case hint, got %v", id, ps)
		}
	}
}

func TestProjectRequired(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/project-missing.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ps := Parse(data)
	if cfg != nil || len(ps) != 1 || ps[0].Path != "project" ||
		ps[0].Message != "is required: the Fugaro project this repository belongs to (fugaro config example shows it)" {
		t.Fatalf("cfg = %v, problems = %v", cfg, ps)
	}
}

func TestProjectBadName(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/project-bad-name.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, ps := Parse(data)
	if len(ps) != 1 || ps[0].Path != "project" ||
		ps[0].Message != "must be a project name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit" {
		t.Fatalf("problems = %v", ps)
	}
}

func TestExampleForFillsProject(t *testing.T) {
	got := ExampleFor("borealis")
	cfg, ps := Parse(got)
	if len(ps) > 0 || cfg.Project != "borealis" {
		t.Fatalf("project = %v, problems = %v", cfg, ps)
	}
	if bytes.Contains(got, []byte("project: example")) {
		t.Error("the placeholder survived")
	}
	for _, bad := range []string{"", "Not A Name", "a\nversion: 2"} {
		if !bytes.Equal(ExampleFor(bad), Example) || !bytes.Contains(Example, []byte("\nproject: example\n")) {
			t.Errorf("ExampleFor(%q) did not fall back to the placeholder", bad)
		}
	}
}

func TestModelForFallsBack(t *testing.T) {
	a := Agent{Model: "claude-sonnet-5-5"}
	if a.ModelFor(RoleCoder) != "claude-sonnet-5-5" || a.ModelFor(RoleReviewer) != "claude-sonnet-5-5" {
		t.Fatalf("no models: %q %q", a.ModelFor(RoleCoder), a.ModelFor(RoleReviewer))
	}
	a.Models = ModelRoles{Coder: "claude-opus-5-5"}
	if a.ModelFor(RoleCoder) != "claude-opus-5-5" || a.ModelFor(RoleReviewer) != "claude-sonnet-5-5" {
		t.Fatalf("coder only: %q %q", a.ModelFor(RoleCoder), a.ModelFor(RoleReviewer))
	}
	if (Agent{}).ModelFor(RoleCoder) != "" {
		t.Fatal("no model at all must stay empty (Claude Code's default)")
	}
}

func TestStageRole(t *testing.T) {
	for stage, want := range map[string]Role{"implement": RoleCoder, "fix": RoleCoder, "review": RoleReviewer} {
		if got := StageRole(stage); got != want {
			t.Errorf("StageRole(%q) = %q, want %q", stage, got, want)
		}
	}
	// An unknown stage costs as the coder, the dearer role in practice.
	if StageRole("other") != RoleCoder {
		t.Error("an unknown stage should be the coder")
	}
}

func TestMaxOutputFor(t *testing.T) {
	a := Agent{MaxOutputTokens: RoleTokens{Coder: 64000, Reviewer: 8000}}
	if a.MaxOutputFor(RoleCoder) != 64000 || a.MaxOutputFor(RoleReviewer) != 8000 {
		t.Fatalf("got %d %d", a.MaxOutputFor(RoleCoder), a.MaxOutputFor(RoleReviewer))
	}
	if (Agent{}).MaxOutputFor(RoleCoder) != 0 {
		t.Fatal("unset must be 0, no limit")
	}
}

func TestAgentModelsValidation(t *testing.T) {
	for in, want := range map[string]string{
		"  models: { coder: has space }":                             "agent.models.coder",
		"  models: { reviewer: \"\" }":                               "",
		"  models: { background: " + strings.Repeat("x", 101) + " }": "agent.models.background",
		"  max_output_tokens: { reviewer: 128001 }":                  "agent.max_output_tokens.reviewer",
		"  max_output_tokens: { coder: -1 }":                         "agent.max_output_tokens.coder",
		"  max_run_tokens: -1":                                       "agent.max_run_tokens",
	} {
		_, ps := Parse([]byte(strings.Replace(minimalYAML, "workflows:", "agent:\n"+in+"\nworkflows:", 1)))
		switch {
		case want == "" && len(ps) != 0:
			t.Errorf("%s: problems %v", in, ps)
		case want != "" && !strings.Contains(paths(ps), want):
			t.Errorf("%s: problems %v, want one at %s", in, ps, want)
		}
	}
	_, ps := Parse([]byte(strings.Replace(minimalYAML, "workflows:", "agent:\n  max_output_tokens: { coder: 128000, reviewer: 0 }\n  max_run_tokens: 0\nworkflows:", 1)))
	if len(ps) != 0 {
		t.Fatalf("limits at the bounds: %v", ps)
	}
}
