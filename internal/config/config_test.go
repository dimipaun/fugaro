package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	// The two round knobs are 0 here on purpose: their default is per rigor
	// mode, which no layer of this file knows, so the unset value is what
	// travels to the run (applyDefaults, rigor.DefaultRounds).
	if cfg.Agent.Auth != "vertex" || cfg.Agent.ReviewRounds != 0 || cfg.Agent.FirstLineRounds != 0 || cfg.Agent.MaxBudgetUSD != 25 {
		t.Errorf("agent defaults = %+v", cfg.Agent)
	}
	w := cfg.Workflows["server"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" {
		t.Errorf("resources = %+v, want 4 / 8Gi", w.Resources)
	}
	if got := w.Commands.Reports; !slices.Equal(got, []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}) {
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
		// claude_accounts is a local-config-only key (design
		// docs/design/named-oauth-tokens.md §3, decision T1): a fugaro.yaml
		// may never define or widen the named-account allow-list, so the
		// repository layer's strict decoder (the same KnownFields(true)
		// decoder as the "unknown field" case above) must refuse it exactly
		// like any other field the Config struct does not have.
		{"claude_accounts is not a fugaro.yaml key", minimalYAML + "claude_accounts:\n  x: {}\n", "", "field claude_accounts not found", 0},
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
		// claude-oauth-token-<name> is the derived secret ID of a named
		// Claude account (config.ClaudeAccountSecret, design
		// docs/design/named-oauth-tokens.md §5): a workflow must never be
		// able to declare a secret of that shape as its own, since a name
		// that happens to collide with (or merely look like) a real or
		// fictitious account name would let fugaro.yaml itself steer where
		// the owner's pasted Claude token value goes, bypassing
		// claude_accounts' allow_data_to allow-list entirely (the local
		// config, never fugaro.yaml, controls that list — T1).
		{"claude-oauth-token-prefixed workflow secret reserved", minimalYAML + "    secrets:\n      - { name: claude-oauth-token-acme-personal, env: TOK }\n", "workflows.server.secrets[0].name", "reserved for named Claude accounts", 0},
		// Codex's OAuth credential names, reserved ahead of that harness
		// (config.CodexOAuthToken, owner ruling of 2026-10-18): Fugaro does
		// not drive Codex, so nothing mounts them and no account or provider
		// of it exists to allow one — every name of either shape in
		// fugaro.yaml is refused, whether or not a Codex provider ever names
		// it. Both rows so the bare name and the namespace are pinned: a
		// mutation that checked only the prefix would pass the first.
		{"codex-oauth-token workflow secret reserved", minimalYAML + "    secrets:\n      - { name: codex-oauth-token, env: TOK }\n", "workflows.server.secrets[0].name", "reserved for the Codex harness", 0},
		{"codex-oauth-token-prefixed workflow secret reserved", minimalYAML + "    secrets:\n      - { name: codex-oauth-token-acme-personal, env: TOK }\n", "workflows.server.secrets[0].name", "reserved for the Codex harness", 0},
		// The credentials service's bundle container (config.CredentialsBundleSecret,
		// C1.11's review finding n2): its document is every model credential the
		// installation holds, and the service is its only reader, so a workflow
		// that declared the name would mount that document into a job. Refused
		// here and again at the write path (internal/cli's
		// credentialsBundleRefusal).
		{"creds-bundle workflow secret reserved", minimalYAML + "    secrets:\n      - { name: creds-bundle, env: TOK }\n", "workflows.server.secrets[0].name", "credentials service's own bundle container", 0},
		{"cache key", minimalYAML + "    cache:\n      - { key: [], paths: [~/.gradle] }\n", "workflows.server.cache[0].key", "at least one file", 0},
		// A verbatim tag's own text stands in for !!str in yaml.v3's type
		// error, so a fugaro.yaml can put anything a YAML tag allows,
		// including an escape sequence or a bidi override character, into
		// that text; the message must escape it rather than print it raw.
		{"tagged value with control and bidi characters", strings.Replace(minimalYAML, "version: 1", "version: !<tag:x,%1B%5B2J%E2%80%AE> 1", 1), "", `cannot unmarshal tag:x,\u001b[2J\u202e`, 0},
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

// TestCommandsLintAndFix (generic-tool Task 9, G10): commands.lint and
// commands.fix parse like build and test, and fix without lint is refused
// (the check gate runs fix, then lint).
func TestCommandsLintAndFix(t *testing.T) {
	cfg, problems := Parse([]byte(minimalYAML + "      lint: make lint\n      fix: make fmt\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if got := cfg.Workflows["server"].Commands; got.Lint != "make lint" || got.Fix != "make fmt" {
		t.Fatalf("commands = %+v, want lint=make lint fix=make fmt", got)
	}
	_, problems = Parse([]byte(minimalYAML + "      fix: make fmt\n"))
	if !hasProblem(problems, "workflows.server.commands.fix", "needs commands.lint (the check step runs fix, then lint)", 0) {
		t.Fatalf("fix without lint: %v", problems)
	}
}

// TestReviewAllowForksRemoved (owner ruling 2026-10-12, generic-tool Task
// 12): review.allow_forks no longer exists — review mode is
// trusted-author-only, with no opt-in for untrusted ones — so a leftover
// review: key, from before the removal, is refused by the strict parser
// (KnownFields) the same as any other unknown key.
//
// Mutation (restore the Review field on Config): this test fails, finding
// no problem for a key that no longer exists.
func TestReviewAllowForksRemoved(t *testing.T) {
	_, problems := Parse([]byte(minimalYAML + "review:\n  allow_forks: true\n"))
	if len(problems) == 0 {
		t.Fatal("want a problem for the removed review.allow_forks key, got none")
	}
}

// TestParseVerbatimTagNeverEchoesValue: yamlValueRE strips the backtick-quoted
// value after a type error's tag, whatever the tag's own text looks like.
// yaml.v3 percent-decodes a verbatim tag, so that text can hold a space (not
// just a control or bidi character showKey alone would escape); a regex that
// only matched \S+ for the tag would then fail to match at all, leaving the
// value (truncated to 7 characters by yaml.v3, but still a leak) in the
// message.
func TestParseVerbatimTagNeverEchoesValue(t *testing.T) {
	const secret = "topsecretvalue1234567"
	yaml := strings.Replace(minimalYAML, "version: 1", "version: !<tag:x%20y> "+secret, 1)
	_, ps := Parse([]byte(yaml))
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.Message)
	}
	got := strings.Join(msgs, "; ")
	if !strings.Contains(got, "cannot unmarshal tag:x y into int") {
		t.Fatalf("problems %q, want one containing %q", got, "cannot unmarshal tag:x y into int")
	}
	if strings.Contains(got, secret[:7]) {
		t.Fatalf("problems echo the value: %q", got)
	}
}

// TestParseVerbatimTagWithFakeIntoNeverEchoesValue: a verbatim tag can
// itself decode to text containing "` into " (the exact text yamlValueRE
// looks for to find where the real value ends), so a lazy value group
// would stop at that first, fake occurrence inside the tag and leave the
// real value, after it, unstripped. yamlValueRE's value group must be
// greedy, matching through to the last "` into " in the message, which is
// always the real one (the type name after it is ours, never the
// writer's).
func TestParseVerbatimTagWithFakeIntoNeverEchoesValue(t *testing.T) {
	const secret = "topsecretvalue1234567"
	yaml := strings.Replace(minimalYAML, "version: 1", "version: !<tag:x%20%60y%60%20into%20z> "+secret, 1)
	_, ps := Parse([]byte(yaml))
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.Message)
	}
	got := strings.Join(msgs, "; ")
	if !strings.Contains(got, "cannot unmarshal tag:x into int") {
		t.Fatalf("problems %q, want one containing %q", got, "cannot unmarshal tag:x into int")
	}
	if strings.Contains(got, secret[:7]) {
		t.Fatalf("problems echo the value: %q", got)
	}
}

func TestSelectWorkflow(t *testing.T) {
	cfg, _ := Parse([]byte(minimalYAML))
	if name, _, err := cfg.SelectWorkflow(""); err != nil || name != "server" {
		t.Fatalf("SelectWorkflow(\"\") = %q, %v", name, err)
	}
	if _, _, err := cfg.SelectWorkflow("web"); err == nil || !strings.Contains(err.Error(), `no workflow "web"`) {
		t.Fatalf("SelectWorkflow(web) err = %v", err)
	}
	two := minimalYAML + "  web:\n    commands: { build: pnpm build, test: pnpm test }\n"
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

func TestPREarlyDraftDefaultsTrue(t *testing.T) {
	for _, tc := range []struct {
		pr   string
		want bool
	}{{"", true}, {"  pr: { early_draft: true }\n", true}, {"  pr: { early_draft: false }\n", false}} {
		cfg, problems := Parse([]byte(strings.Replace(earlyDraftBase(t), "git:\n", "git:\n"+tc.pr, 1)))
		if len(problems) > 0 {
			t.Fatalf("%q: %v", tc.pr, problems)
		}
		if got := cfg.Git.PR.EarlyDraftOn(); got != tc.want {
			t.Errorf("%q: EarlyDraftOn = %v, want %v", tc.pr, got, tc.want)
		}
	}
	if !(PRSettings{}).EarlyDraftOn() {
		t.Error("the zero PRSettings must default to early drafts")
	}
}

func earlyDraftBase(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../testdata/config/valid/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPRCheckpointsDefaultsTrue(t *testing.T) {
	for _, tc := range []struct {
		pr   string
		want bool
	}{{"", true}, {"  pr: { checkpoints: true }\n", true}, {"  pr: { checkpoints: false, early_draft: false }\n", false},
		{"  pr: { checkpoints: true, early_draft: false }\n", true},
		{"  pr: { checkpoints: false, early_draft: true }\n", false},
		{"  pr: { early_draft: false }\n", true},
		{"  pr: { early_draft: true }\n", true},
		{"  pr: { checkpoints: false }\n", false}} {
		cfg, problems := Parse([]byte(strings.Replace(earlyDraftBase(t), "git:\n", "git:\n"+tc.pr, 1)))
		if len(problems) > 0 {
			t.Fatalf("%q: %v", tc.pr, problems)
		}
		if got := cfg.Git.PR.CheckpointsOn(); got != tc.want {
			t.Errorf("%q: CheckpointsOn = %v, want %v", tc.pr, got, tc.want)
		}
	}
	if !(PRSettings{}).CheckpointsOn() {
		t.Error("the zero PRSettings must default to checkpoints on")
	}
}

func TestAgentRecipe(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: api-key\n%s" +
		"workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"
	cfg, ps := Parse([]byte(fmt.Sprintf(base, "  recipe: claude-solo\n")))
	if len(ps) > 0 || cfg.Agent.Recipe != "claude-solo" {
		t.Fatalf("cfg = %+v, problems %v", cfg, ps)
	}
	cfg, ps = Parse([]byte(fmt.Sprintf(base, "")))
	if len(ps) > 0 || cfg.Agent.Recipe != "" {
		t.Fatalf("unset: %+v, %v", cfg, ps)
	}
	_, ps = Parse([]byte(fmt.Sprintf(base, "  recipe: Claude_Solo\n")))
	if len(ps) != 1 || ps[0].Path != "agent.recipe" || !strings.Contains(ps[0].Message, "must be a recipe name") {
		t.Fatalf("bad name: %v", ps)
	}
	// "" is the default, the same as unset; 40 characters is the longest name.
	cfg, ps = Parse([]byte(fmt.Sprintf(base, "  recipe: \"\"\n")))
	if len(ps) > 0 || cfg.Agent.Recipe != "" {
		t.Fatalf("empty: %+v, %v", cfg, ps)
	}
	cfg, ps = Parse([]byte(fmt.Sprintf(base, "  recipe: "+strings.Repeat("a", 40)+"\n")))
	if len(ps) > 0 || len(cfg.Agent.Recipe) != 40 {
		t.Fatalf("40 chars: %+v, %v", cfg, ps)
	}
	_, ps = Parse([]byte(fmt.Sprintf(base, "  recipe: "+strings.Repeat("a", 41)+"\n")))
	if len(ps) != 1 || ps[0].Path != "agent.recipe" {
		t.Fatalf("41 chars: %v", ps)
	}
	for file, ok := range map[string]bool{"valid/agent-recipe-empty": true, "invalid/agent-recipe-long": false} {
		data, err := os.ReadFile("../../testdata/config/" + file + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		if cfg, ps := Parse(data); (cfg != nil) != ok {
			t.Errorf("%s: %+v, %v", file, cfg, ps)
		}
	}
}

// TestAgentOAuthToken is TestAgentRecipe's shape for agent.oauth_token
// (named oauth tokens plan Task 6): the field decodes, and Validate applies
// the same shape rules task.Spec.OAuthToken does (config.ClaudeAccountNameRE,
// MaxClaudeAccountNameLen, never "default" literally) — a membership check
// (config.AllowedClaudeAccounts) is not this function's job, since it never
// sees the local config.
func TestAgentOAuthToken(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: oauth\n%s" +
		"workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"
	cfg, ps := Parse([]byte(fmt.Sprintf(base, "  oauth_token: acme-personal\n")))
	if len(ps) > 0 || cfg.Agent.OAuthToken != "acme-personal" {
		t.Fatalf("cfg = %+v, problems %v", cfg, ps)
	}
	cfg, ps = Parse([]byte(fmt.Sprintf(base, "")))
	if len(ps) > 0 || cfg.Agent.OAuthToken != "" {
		t.Fatalf("unset: %+v, %v", cfg, ps)
	}
	_, ps = Parse([]byte(fmt.Sprintf(base, "  oauth_token: default\n")))
	if len(ps) != 1 || ps[0].Path != "agent.oauth_token" || !strings.Contains(ps[0].Message, `must not be "default"`) {
		t.Fatalf("literal default: %v", ps)
	}
	_, ps = Parse([]byte(fmt.Sprintf(base, "  oauth_token: Acme\n")))
	if len(ps) != 1 || ps[0].Path != "agent.oauth_token" || !strings.Contains(ps[0].Message, "must be a Claude account name") {
		t.Fatalf("bad shape: %v", ps)
	}
	_, ps = Parse([]byte(fmt.Sprintf(base, "  oauth_token: "+strings.Repeat("a", MaxClaudeAccountNameLen+1)+"\n")))
	if len(ps) != 1 || ps[0].Path != "agent.oauth_token" || !strings.Contains(ps[0].Message, "must be at most") {
		t.Fatalf("too long: %v", ps)
	}
}

// TestValidateCompressionReservedAndRefused pins decision P11: agent.compression
// is a known key from the moment it exists, so a repository that tries it gets
// a refusal naming it "not yet implemented" rather than a strict-parse
// "unknown field" error, and the only value Validate accepts is "" (off).
func TestValidateCompressionReservedAndRefused(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: api-key\n%s" +
		"workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"
	for _, val := range []string{"headroom", "anything"} {
		_, ps := Parse([]byte(fmt.Sprintf(base, "  compression: "+val+"\n")))
		if len(ps) == 0 {
			t.Fatalf("compression=%q was accepted before it is implemented", val)
		}
		if !slices.ContainsFunc(ps, func(p Problem) bool { return strings.Contains(p.Message, "not yet implemented") }) {
			t.Errorf("compression=%q: refusal message doesn't say why: %+v", val, ps)
		}
	}
	if cfg, ps := Parse([]byte(fmt.Sprintf(base, ""))); len(ps) > 0 || cfg.Agent.Compression != "" {
		t.Fatalf("unset compression was refused: %+v, %v", cfg, ps)
	}
	if cfg, ps := Parse([]byte(fmt.Sprintf(base, "  compression: \"\"\n"))); len(ps) > 0 || cfg.Agent.Compression != "" {
		t.Fatalf("the empty compression was refused: %+v, %v", cfg, ps)
	}
}

// TestOldConfigParserRefusesCompressionKey is the version-skew half of the
// strict-decode claim: a decoder shaped like this package's Agent struct
// before this task (every field it had then, none of the new one) refuses
// agent.compression outright, which is why a repository may not set the key
// against an image built before the feature shipped.
func TestOldConfigParserRefusesCompressionKey(t *testing.T) {
	type oldAgent struct {
		Auth            string     `yaml:"auth"`
		Model           string     `yaml:"model"`
		ReviewRounds    int        `yaml:"review_rounds"`
		MaxBudgetUSD    float64    `yaml:"max_budget_usd"`
		Instructions    string     `yaml:"instructions"`
		Review          string     `yaml:"review"`
		Models          ModelRoles `yaml:"models"`
		MaxOutputTokens RoleTokens `yaml:"max_output_tokens"`
		MaxRunTokens    int64      `yaml:"max_run_tokens"`
		FirstLineReview string     `yaml:"first_line_review"`
		FirstLineRounds int        `yaml:"first_line_rounds"`
		Recipe          string     `yaml:"recipe"`
		OAuthToken      string     `yaml:"oauth_token,omitempty"`
	}
	dec := yaml.NewDecoder(bytes.NewReader([]byte("auth: api-key\ncompression: headroom\n")))
	dec.KnownFields(true)
	var old oldAgent
	if err := dec.Decode(&old); err == nil {
		t.Fatal("an Agent decoder shaped like config.go before this task accepted agent.compression silently")
	}
}

// TestValidateRefusesCompressionWithOAuth and its vertex twin pin decision P9's
// Validate rule. Both cases are also refused as "not yet implemented" while the
// key is reserved (P11), so each asserts the auth reason specifically: the rule
// must already be there for the lift (Task 10) to leave it standing alone.
func TestValidateRefusesCompressionWithOAuth(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: oauth\n  compression: headroom\n" +
		"workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"
	_, ps := Parse([]byte(base))
	if !slices.ContainsFunc(ps, func(p Problem) bool { return p.Path == "agent.compression" && strings.Contains(p.Message, "oauth") }) {
		t.Fatalf("compression + oauth was not refused for the auth reason: %+v", ps)
	}
}

func TestValidateRefusesCompressionWithVertex(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: vertex\n  compression: headroom\n" +
		"workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"
	_, ps := Parse([]byte(base))
	if !slices.ContainsFunc(ps, func(p Problem) bool { return p.Path == "agent.compression" && strings.Contains(p.Message, "vertex") }) {
		t.Fatalf("compression + vertex was not refused for the auth reason: %+v", ps)
	}
}

// TestAgentCompressionCorpus is TestAgentRecipe's shape for the corpus half of
// P11: the fixture exercising the real value stays in invalid/ until the lift
// moves it, and is refused by Parse as well as by the schema.
func TestAgentCompressionCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/agent-compression-headroom.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ps := Parse(data)
	if cfg != nil || len(ps) == 0 {
		t.Fatalf("the reserved-value fixture parsed: %+v, %v", cfg, ps)
	}
}

// TestAgentOf is the reader internal/cli judges a launch's Claude account with
// (agentAtRef: the fugaro.yaml at the ref a run checks out, read out of the
// launcher's git objects). It reads the document's own agent block, applies the
// auth default the rest of Fugaro applies (DefaultAgentAuth — a resolved config
// says "vertex" for a file that names none, so this reader must not say ""),
// and answers "cannot tell" for bytes no reader of this Fugaro could resolve —
// which is not the same answer as "names no account".
//
// Mutation (run, restore): return the block as decoded, without the auth
// default — the no-auth cases read "" here while every resolved config says
// "vertex", and claudeAccountAllowed then stops refusing an account named for a
// workflow whose own file names no oauth auth.
func TestAgentOf(t *testing.T) {
	for _, tc := range []struct {
		why       string
		data      string
		wantAuth  string
		wantToken string
		wantOK    bool
	}{
		{
			why:       "both keys as written",
			data:      "version: 1\nproject: example\ngit: { provider: github }\nagent: { auth: oauth, oauth_token: acme-personal }\nworkflows:\n  app: { commands: { build: b } }\n",
			wantAuth:  "oauth",
			wantToken: "acme-personal",
			wantOK:    true,
		},
		{
			why:      "an agent block that names no auth carries the default",
			data:     "version: 1\nproject: example\ngit: { provider: github }\nagent: { oauth_token: acme-personal }\nworkflows:\n  app: { commands: { build: b } }\n",
			wantAuth: DefaultAgentAuth, wantToken: "acme-personal", wantOK: true,
		},
		{
			why:      "no agent block at all names no account",
			data:     "version: 1\nproject: example\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: b } }\n",
			wantAuth: DefaultAgentAuth, wantOK: true,
		},
		{
			// The file a launch most often reads: one that needs the project
			// layer for its workflow, which no plain Resolve could answer for.
			// agent.oauth_token is repository-scope, so it is here.
			why:      "a file that needs a layer still says which account it names",
			data:     "version: 1\nproject: example\ngcp_project: proj-1234\nagent: { auth: oauth, oauth_token: acme-personal }\nworkflows:\n  app: { profile: p, commands: { build: b } }\n",
			wantAuth: "oauth", wantToken: "acme-personal", wantOK: true,
		},
		{
			why:    "an unknown key is cannot tell, not names no account",
			data:   "version: 1\nproject: example\ngit: { provider: github }\nagent: { auth: oauth, oauth_token: acme-personal }\nnonsense: 1\nworkflows:\n  app: { commands: { build: b } }\n",
			wantOK: false,
		},
		{
			why:    "a duplicate key is cannot tell too",
			data:   "version: 1\nproject: example\ngit: { provider: github }\nagent: { auth: oauth, auth: api-key }\nworkflows:\n  app: { commands: { build: b } }\n",
			wantOK: false,
		},
		{
			why:    "an oauth_token that is not a string is cannot tell",
			data:   "version: 1\nproject: example\ngit: { provider: github }\nagent: { auth: oauth, oauth_token: [a, b] }\nworkflows:\n  app: { commands: { build: b } }\n",
			wantOK: false,
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			a, ok := AgentOf([]byte(tc.data))
			if ok != tc.wantOK {
				t.Fatalf("AgentOf ok = %v, want %v (%q)", ok, tc.wantOK, tc.data)
			}
			if !ok {
				return
			}
			if a.Auth != tc.wantAuth || a.OAuthToken != tc.wantToken {
				t.Errorf("AgentOf = {auth: %q, oauth_token: %q}, want {%q, %q}", a.Auth, a.OAuthToken, tc.wantAuth, tc.wantToken)
			}
		})
	}
}

const profileBase = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { commands: { build: make, test: make test } }\n"

func TestProfileKeysDecode(t *testing.T) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(strings.Replace(profileBase, "  web: {", "  web: { profile: java-service,", 1) + "profile: node-web\n"))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		t.Fatal(err)
	}
	if c.Profile != "node-web" || c.Workflows["web"].Profile != "java-service" {
		t.Fatalf("decoded %q, %q", c.Profile, c.Workflows["web"].Profile)
	}
}

func TestValidateProfileNames(t *testing.T) {
	c, ps := Parse([]byte(profileBase))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	c.Profile = "Bad_Name"
	w := c.Workflows["web"]
	w.Profile = "also bad!"
	c.Workflows["web"] = w
	var paths []string
	for _, p := range Validate(c) {
		if strings.Contains(p.Message, "must be a profile name") {
			paths = append(paths, p.Path)
		}
	}
	if strings.Join(paths, ",") != "profile,workflows.web.profile" {
		t.Fatalf("profile problems at %v", paths)
	}
}

func TestDurationMarshalsAsWritten(t *testing.T) {
	out, err := yaml.Marshal(Timeouts{Total: Duration{Duration: 90 * time.Minute, Set: true}})
	if err != nil || !strings.Contains(string(out), "total: 1h30m0s") {
		t.Fatalf("%s %v", out, err)
	}
}

// --- mode: artifact's two commands and the input caps (0.8.0) --------------

// TestRenderTimeoutDefaultsUnderTotal: timeouts.render bounds the render step
// alone and, like stage and verify, is derived from what is left of the run
// when nothing writes it: min(verify, total - finalize_reserve), which is
// never above total (docs/design/artifact-runs.md §3).
func TestRenderTimeoutDefaultsUnderTotal(t *testing.T) {
	cfg, problems := Parse([]byte(minimalYAML + "      render: sh render.sh\n      probe: sh probe.sh\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	w := cfg.Workflows["server"]
	if w.Commands.Render != "sh render.sh" || w.Commands.Probe != "sh probe.sh" {
		t.Fatalf("commands = %+v", w.Commands)
	}
	// 90m total: verify is min(30m, 90m) = 30m, the reserve is 5m, so render
	// is min(30m, 85m) = 30m.
	if got := w.Timeouts.Render.Duration; got != 30*time.Minute {
		t.Fatalf("render = %s, want 30m", got)
	}
	// A short total scales it down with the others, and never above total:
	// 10m total, reserve 2m, verify 10m, so render is 8m.
	cfg, problems = Parse([]byte(minimalYAML + "    timeouts: { total: 10m }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	to := cfg.Workflows["server"].Timeouts
	if to.Render.Duration != 8*time.Minute || to.Render.Duration > to.Total.Duration {
		t.Fatalf("render = %s, want 8m under total %s", to.Render.Duration, to.Total.Duration)
	}
	// A written render is kept as it is (the run's own deadline caps it
	// further, so it is not refused for exceeding the computed default).
	cfg, problems = Parse([]byte(minimalYAML + "    timeouts: { render: 12m }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if got := cfg.Workflows["server"].Timeouts.Render.Duration; got != 12*time.Minute {
		t.Fatalf("render = %s, want 12m", got)
	}
	// A negative one is refused like the other four.
	if _, problems := Parse([]byte(minimalYAML + "    timeouts: { render: -5m }\n")); !hasProblem(problems, "workflows.server.timeouts.render", "must be positive", 0) {
		t.Fatalf("negative render: %v", problems)
	}
}

// TestInputCapsHaveCeilings: workflows.<name>.inputs' three caps parse, each
// with a default behind 0 and a ceiling of its own (P6: max_files counts
// paths, max_bytes bounds ONE file, max_total_bytes bounds the set).
func TestInputCapsHaveCeilings(t *testing.T) {
	cfg, problems := Parse([]byte(minimalYAML + "    inputs: { max_files: 4, max_bytes: 1048576, max_total_bytes: 4194304 }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	in := cfg.Workflows["server"].Inputs
	if in.MaxFiles != 4 || in.MaxBytes != 1048576 || in.MaxTotalBytes != 4194304 {
		t.Fatalf("inputs = %+v", in)
	}
	if in.MaxFileCount() != 4 || in.MaxFileBytes() != 1048576 || in.MaxSetBytes() != 4194304 {
		t.Fatalf("effective inputs = %d %d %d", in.MaxFileCount(), in.MaxFileBytes(), in.MaxSetBytes())
	}
	// Written zeroes mean the default, not the ceiling.
	cfg, problems = Parse([]byte(minimalYAML + "    inputs: { max_files: 0, max_bytes: 0, max_total_bytes: 0 }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	in = cfg.Workflows["server"].Inputs
	if in.MaxFileCount() != DefaultInputFiles || in.MaxFileBytes() != DefaultInputBytes || in.MaxSetBytes() != DefaultInputTotalBytes {
		t.Fatalf("zeroes = %d %d %d", in.MaxFileCount(), in.MaxFileBytes(), in.MaxSetBytes())
	}
	if DefaultInputFiles != 16 || DefaultInputBytes != 256<<20 || DefaultInputTotalBytes != 1<<30 {
		t.Fatalf("defaults changed: %d files, %d and %d bytes", DefaultInputFiles, DefaultInputBytes, DefaultInputTotalBytes)
	}
	// A workflow that writes no inputs block gets the same defaults.
	cfg, problems = Parse([]byte(minimalYAML))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if in := cfg.Workflows["server"].Inputs; in.MaxFileCount() != 16 || in.MaxFileBytes() != 256<<20 || in.MaxSetBytes() != 1<<30 {
		t.Fatalf("defaults = %+v", in)
	}
	for _, tc := range []struct{ name, line, path, want string }{
		{"files over the ceiling", "inputs: { max_files: 65 }", "max_files", "must be at most 64"},
		{"negative files", "inputs: { max_files: -1 }", "max_files", "must not be negative"},
		{"bytes over the ceiling", "inputs: { max_bytes: 1073741825 }", "max_bytes", "must be at most 1073741824 bytes (1 GiB)"},
		{"negative bytes", "inputs: { max_bytes: -1 }", "max_bytes", "must not be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(minimalYAML + "    " + tc.line + "\n"))
			if !hasProblem(problems, "workflows.server.inputs."+tc.path, tc.want, 0) {
				t.Fatalf("%s: problems %v lack %s: %q", tc.line, problems, tc.path, tc.want)
			}
		})
	}
}

// TestInputTotalCapHasACeiling: the set cap's own pair of rules, the same two
// outputs.max_total_bytes has: above its ceiling it is refused, and a set cap
// below the per-file cap with max_files of 1 is refused too (the one file is
// the whole set, so the smaller cap silently governs).
func TestInputTotalCapHasACeiling(t *testing.T) {
	_, problems := Parse([]byte(minimalYAML + "    inputs: { max_total_bytes: 4294967297 }\n"))
	if !hasProblem(problems, "workflows.server.inputs.max_total_bytes", "must be at most 4294967296 bytes (4 GiB)", 0) {
		t.Fatalf("over the ceiling: %v", problems)
	}
	if _, problems := Parse([]byte(minimalYAML + "    inputs: { max_total_bytes: -1 }\n")); !hasProblem(problems, "workflows.server.inputs.max_total_bytes", "must not be negative", 0) {
		t.Fatalf("negative total: %v", problems)
	}
	one := "    inputs: { max_files: 1, max_bytes: 1048576, max_total_bytes: 262144 }\n"
	if _, problems := Parse([]byte(minimalYAML + one)); !hasProblem(problems, "workflows.server.inputs.max_total_bytes", "must be at least inputs.max_bytes", 0) {
		t.Fatalf("one legal input trips the set: %v", problems)
	}
	// With room for more than one file the pair is a set cap doing its job: a
	// set of files each under max_bytes may still be over the total.
	if _, problems := Parse([]byte(minimalYAML + "    inputs: { max_files: 2, max_bytes: 1048576, max_total_bytes: 262144 }\n")); len(problems) != 0 {
		t.Fatalf("two files: %v", problems)
	}
	// Only the two written caps are compared.
	if _, problems := Parse([]byte(minimalYAML + "    inputs: { max_files: 1, max_total_bytes: 262144 }\n")); len(problems) != 0 {
		t.Fatalf("defaulted max_bytes: %v", problems)
	}
}
