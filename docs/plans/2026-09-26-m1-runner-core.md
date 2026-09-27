# M1 — Runner Core (local) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single Go binary, `fugaro`, that can execute a complete Fugaro run locally: `fugaro exec --local`-style flags against a local bare git remote, a `file://` bucket, a fake git provider, and a fake `claude` binary. The run goes bootstrap → implement → review/fix rounds → finalize (push and a ready or draft PR). It also provides `fugaro verify`, `fugaro validate`, and `fugaro config example`.

**Architecture:** One Go module with small internal packages. Each package has one job and depends only on the packages below it:

```
config ─┐
task ───┼─► runstore ─┐
procgroup ─► gitops ─► verify ─┤
agent (claude -p) ─────────────┼─► runner ─► cli (cobra)
gitprov (+fake) ───────────────┘
```

The runner reaches the outside world only through interfaces (`agent.Agent`, `gitprov.Provider`, `*blob.Bucket`) and real git. So the full lifecycle is tested hermetically, with no cloud and no model.

**Tech Stack:**
- Go 1.27
- `github.com/spf13/cobra` for the CLI
- `gopkg.in/yaml.v3` for YAML
- `github.com/bmatcuk/doublestar/v4` for report globs
- `gocloud.dev/blob` (memblob, fileblob) for storage
- `github.com/santhosh-tekuri/jsonschema/v6`, in schema tests only
- `git` and `sh` on PATH

**Spec:** [docs/design/v1.md](../design/v1.md). Read §4 (runner lifecycle) and §5 (configuration) before starting.

**Out of scope for M1** (each gets its own plan later):
- **M2:** real GitHub and Bitbucket providers, and the agent git token
- **M3:** container images
- **M4:** the GCP backend, the `run`/`ls`/`logs`/`diagnose`/`cancel` commands, caches, and branch locks
- **M5:** Terraform and `fugaro init`
- **M6:** follow-up runs. In M1, the runner rejects a task spec that has `branch` set.
- **M7:** the plugin and skills

## Global Constraints

- `go.mod` declares `go 1.27`; the module path is `github.com/dimipaun/fugaro`.
- The engine targets darwin and linux only. Process groups use `syscall.SysProcAttr{Setpgid: true}`.
- No company-, repo-, or cloud-resource-specific values in engine code, defaults, or examples (design §2).
- `fugaro.yaml` never holds cloud resource paths. Secrets are logical `name`s mapped to `env` variable names (design §5.1).
- CLI exit codes: `0` ok, `1` user error, `2` remote/infra failure (design §9.1).
- Run IDs are `YYYYMMDD-HHMMSS-<4 hex>` in UTC. The branch is `fugaro/<run-id>`. Run storage lives under `runs/<repo-slug>/<run-id>/` (design §3.3).
- The engine never runs build or test itself. A PR is ready **iff** both hold: there is a passing recorded `fugaro verify test` on the final HEAD with a clean tree, and the last review verdict is `ship`. Otherwise the PR is a draft with a stated reason (design §4.2).
- The runner's own commits use `--no-verify`. The state directory (`FUGARO_STATE_DIR`) must be outside the checkout.
- Dependencies are limited to those listed under Tech Stack. Anything else needs discussion first.
- Every test that runs git calls `testutil.IsolateGit(t)` first, so the developer's global git config (signing, hooks) can't leak in.
- Code style: `gofmt`, doc comments on exported identifiers, and errors wrapped with `%w` and context.

## Review Focus

These are the failure modes the design implies but that are easy to miss. Each one is pinned by a test in the task named.

1. **Stale test reports.** A JUnit file left over from an earlier run or build must not count as the current result (Task 7 `TestCollectReportsIgnoresStale`, Task 8 `TestTestIgnoresStaleReports`).
2. **A leaked daemon holding the output pipe.** A `gradle`-style background process that inherits stdout must not make a stage or verify hang until the process exits, and it must be killed (Task 5 `TestReapsLeakedDaemon`).
3. **Bootstrap wiping warm caches.** Resetting the checkout must remove untracked files but keep *ignored* ones (`node_modules/`, `build/`), which are the warm caches baked into the image (Task 6 `TestCheckoutNewBranchKeepsIgnored`).
4. **Finalize after cancel or timeout.** When the run context is cancelled or a stage times out, finalize must still push and open a draft PR (Task 13 `TestCancelledRunStillOpensDraft`, `TestStageTimeoutOpensDraft`; Task 14 e2e).
5. **A repo pre-commit hook blocking finalize.** Husky or lint hooks that fail must not stop the runner from committing leftover work (Task 6 `TestCommitAllIgnoresFailingHook`).

---

### Task 1: Module skeleton, CLI root, exit codes, CI

**Files:**
- Create: `go.mod`, `cmd/fugaro/main.go`, `internal/cli/root.go`, `internal/cli/root_test.go`, `.github/workflows/ci.yml`, `.gitignore`

**Interfaces:**
- Produces: `cli.NewRootCmd() *cobra.Command`, `cli.Version string`, `cli.ExitError{Code int; Err error}`, `cli.ExitCode(error) int`, and the constants `cli.ExitOK=0`, `cli.ExitUserError=1`, `cli.ExitRemoteError=2`. Test helper `execute(t, args...) (stdout, stderr string, err error)` in `package cli`.

- [ ] **Step 1: Initialize the module**

```bash
go mod init github.com/dimipaun/fugaro
go mod edit -go=1.27
go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write the failing test** — `internal/cli/root_test.go`

```go
package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// execute runs the fugaro command tree with args and captures its output.
func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestVersion(t *testing.T) {
	out, _, err := execute(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if out != "dev\n" {
		t.Fatalf("version output = %q, want %q", out, "dev\n")
	}
}

func TestExitCode(t *testing.T) {
	remote := &ExitError{Code: ExitRemoteError, Err: errors.New("boom")}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"plain error", errors.New("x"), ExitUserError},
		{"exit error", remote, ExitRemoteError},
		{"wrapped exit error", fmt.Errorf("wrap: %w", remote), ExitRemoteError},
	}
	for _, tc := range cases {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("%s: ExitCode = %d, want %d", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/cli/`
Expected: FAIL, a compile error (`undefined: NewRootCmd`).

- [ ] **Step 4: Implement** — `internal/cli/root.go`

```go
// Package cli implements the fugaro command-line interface.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time with
// -ldflags "-X github.com/dimipaun/fugaro/internal/cli.Version=<version>".
var Version = "dev"

// Exit codes are part of the CLI contract (design §9.1).
const (
	ExitOK          = 0
	ExitUserError   = 1
	ExitRemoteError = 2
)

// ExitError carries a specific process exit code out of a command.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error returned by a command to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitUserError
}

// NewRootCmd builds the fugaro command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fugaro",
		Short:         "Fleeting cloud workers for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fugaro version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), Version)
			return err
		},
	}
}
```

`cmd/fugaro/main.go`:

```go
// Command fugaro is the Fugaro CLI and in-container runner.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dimipaun/fugaro/internal/cli"
)

func main() {
	// SIGTERM is how Cloud Run asks a task to stop; cancelling the context lets
	// the runner abandon the current stage and still finalize.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := cli.NewRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fugaro:", err)
		os.Exit(cli.ExitCode(err))
	}
}
```

`.gitignore`:

```
/dist/
/fugaro
```

`.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: gofmt
        run: test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
      - run: go vet ./...
      - run: go test -race ./...
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go mod tidy && go test ./... && go run ./cmd/fugaro version`
Expected: PASS, and the last command prints `dev`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd internal .github .gitignore
git commit -m "feat: add module skeleton, CLI root and CI"
```

---

### Task 2: `fugaro.yaml` parsing, defaults, and validation

**Files:**
- Create: `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/validate.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Config{Version int; Git Git; Agent Agent; Workflows map[string]Workflow}`
  - `config.Git{Provider, BaseBranch string; PR PRSettings}`
  - `config.PRSettings{Labels, Reviewers []string}`
  - `config.Agent{Auth, Model string; ReviewRounds int; MaxBudgetUSD float64; Instructions, Review string}`
  - `config.Workflow{Base, Dockerfile string; Commands Commands; Cache []CacheEntry; Secrets []Secret; Resources Resources; Timeouts Timeouts}`
  - `config.Commands{Build, Test string; RerunFailed *RerunFailed; Reports []string}`
  - `config.RerunFailed{Command, Each string}`, with both yaml and json tags
  - `config.Secret{Name, Env string}`
  - `config.Resources{CPU int; Memory string}`
  - `config.Timeouts{Total, Stage, Verify, FinalizeReserve Duration}`
  - `config.Duration{time.Duration}`
  - `config.Problem{Path string; Line int; Message string}`, with `String()`
  - `config.Parse(data []byte) (*Config, []Problem)`
  - `config.Validate(*Config) []Problem`
  - `config.Check(cfg *Config, root string) []Problem`
  - `(*Config).SelectWorkflow(name string) (string, Workflow, error)`

- [ ] **Step 1: Add the dependency**

```bash
go get gopkg.in/yaml.v3@latest
```

- [ ] **Step 2: Write the failing tests** — `internal/config/config_test.go`

```go
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
	if err != nil || name != "web" || w.Resources.CPU != 4 || w.Resources.Memory != "16Gi" {
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL, a compile error (`undefined: Parse`).

- [ ] **Step 4: Implement the types and parsing** — `internal/config/config.go`

```go
// Package config loads and validates a repository's fugaro.yaml (design §5.1).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is a parsed fugaro.yaml.
type Config struct {
	Version   int                 `yaml:"version"`
	Git       Git                 `yaml:"git"`
	Agent     Agent               `yaml:"agent"`
	Workflows map[string]Workflow `yaml:"workflows"`
}

// Git describes the repository's git provider and pull request settings.
type Git struct {
	Provider   string     `yaml:"provider"`
	BaseBranch string     `yaml:"base_branch"`
	PR         PRSettings `yaml:"pr"`
}

// PRSettings are applied to every pull request Fugaro opens.
type PRSettings struct {
	Labels    []string `yaml:"labels"`
	Reviewers []string `yaml:"reviewers"`
}

// Agent configures the Claude Code agent.
type Agent struct {
	Auth         string  `yaml:"auth"`
	Model        string  `yaml:"model"`
	ReviewRounds int     `yaml:"review_rounds"`
	MaxBudgetUSD float64 `yaml:"max_budget_usd"`
	Instructions string  `yaml:"instructions"`
	Review       string  `yaml:"review"`
}

// Workflow is one buildable unit of the repository, such as a server or a web app.
type Workflow struct {
	Base       string       `yaml:"base"`
	Dockerfile string       `yaml:"dockerfile"`
	Commands   Commands     `yaml:"commands"`
	Cache      []CacheEntry `yaml:"cache"`
	Secrets    []Secret     `yaml:"secrets"`
	Resources  Resources    `yaml:"resources"`
	Timeouts   Timeouts     `yaml:"timeouts"`
}

// Commands are the repository's build and test commands, run through `sh -c`.
type Commands struct {
	Build       string       `yaml:"build"`
	Test        string       `yaml:"test"`
	RerunFailed *RerunFailed `yaml:"rerun_failed"`
	Reports     []string     `yaml:"reports"`
}

// RerunFailed builds a command that reruns specific tests: Command followed by
// Each once per failed test, with {id} replaced by the shell-quoted test ID.
type RerunFailed struct {
	Command string `yaml:"command" json:"command"`
	Each    string `yaml:"each" json:"each"`
}

// CacheEntry is a content-addressed dependency cache (restored and written back in M4).
type CacheEntry struct {
	Key   []string `yaml:"key"`
	Paths []string `yaml:"paths"`
}

// Secret maps a logical secret name to the environment variable it is exposed as.
type Secret struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

// Resources are the Cloud Run task resources for the workflow's job.
type Resources struct {
	CPU    int    `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// Timeouts bound a run (design §4.5).
type Timeouts struct {
	Total           Duration `yaml:"total"`
	Stage           Duration `yaml:"stage"`
	Verify          Duration `yaml:"verify"`
	FinalizeReserve Duration `yaml:"finalize_reserve"`
}

// Duration is a time.Duration written in Go syntax, such as "90m" or "1h30m".
type Duration struct{ time.Duration }

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q (use Go syntax such as 90m or 1h30m)", n.Line, n.Value)
	}
	d.Duration = v
	return nil
}

// Problem is one reason a fugaro.yaml is invalid.
type Problem struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	switch {
	case p.Path != "" && p.Line > 0:
		return fmt.Sprintf("%s (line %d): %s", p.Path, p.Line, p.Message)
	case p.Path != "":
		return p.Path + ": " + p.Message
	case p.Line > 0:
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	default:
		return p.Message
	}
}

// Parse decodes fugaro.yaml strictly, applies defaults and validates the result.
// It returns either a config or the problems that prevented one.
func Parse(data []byte) (*Config, []Problem) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "file is empty"}}
		}
		return nil, yamlProblems(err)
	}
	applyDefaults(&c)
	if ps := Validate(&c); len(ps) > 0 {
		return nil, ps
	}
	return &c, nil
}

var yamlLineRE = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

func yamlProblems(err error) []Problem {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		ps := make([]Problem, 0, len(te.Errors))
		for _, msg := range te.Errors {
			ps = append(ps, problemFromYAML(msg))
		}
		return ps
	}
	return []Problem{problemFromYAML(err.Error())}
}

func problemFromYAML(msg string) Problem {
	if m := yamlLineRE.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Problem{Line: line, Message: m[2]}
	}
	return Problem{Message: strings.TrimPrefix(msg, "yaml: ")}
}

// SelectWorkflow returns the named workflow, or the only one when name is empty.
func (c *Config) SelectWorkflow(name string) (string, Workflow, error) {
	names := sortedKeys(c.Workflows)
	if name == "" {
		if len(names) == 1 {
			return names[0], c.Workflows[names[0]], nil
		}
		return "", Workflow{}, fmt.Errorf("fugaro.yaml defines %d workflows; pick one of %s", len(names), strings.Join(names, ", "))
	}
	w, ok := c.Workflows[name]
	if !ok {
		return "", Workflow{}, fmt.Errorf("fugaro.yaml has no workflow %q (have %s)", name, strings.Join(names, ", "))
	}
	return name, w, nil
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
```

`internal/config/defaults.go`:

```go
package config

import "time"

type baseDefault struct {
	reports []string
	cpu     int
	memory  string
}

var baseDefaults = map[string]baseDefault{
	"server-jvm": {reports: []string{"**/build/test-results/**/*.xml"}, cpu: 8, memory: "32Gi"},
	"web-node":   {reports: []string{"**/junit*.xml"}, cpu: 4, memory: "16Gi"},
}

// applyDefaults fills in every field fugaro.yaml may omit. Cache defaults
// arrive with cache support in M4.
func applyDefaults(c *Config) {
	if c.Git.BaseBranch == "" {
		c.Git.BaseBranch = "main"
	}
	if c.Agent.Auth == "" {
		c.Agent.Auth = "vertex"
	}
	if c.Agent.ReviewRounds == 0 {
		c.Agent.ReviewRounds = 2
	}
	if c.Agent.MaxBudgetUSD == 0 {
		c.Agent.MaxBudgetUSD = 25
	}
	for name, w := range c.Workflows {
		if d, ok := baseDefaults[w.Base]; ok {
			if len(w.Commands.Reports) == 0 {
				w.Commands.Reports = d.reports
			}
			if w.Resources.CPU == 0 {
				w.Resources.CPU = d.cpu
			}
			if w.Resources.Memory == "" {
				w.Resources.Memory = d.memory
			}
		}
		t := &w.Timeouts
		if t.Total.Duration == 0 {
			t.Total.Duration = 90 * time.Minute
		}
		// Unset timeouts scale down with a short total so a small total alone is valid.
		if t.FinalizeReserve.Duration == 0 {
			t.FinalizeReserve.Duration = min(5*time.Minute, t.Total.Duration/5)
		}
		if t.Stage.Duration == 0 {
			t.Stage.Duration = min(40*time.Minute, t.Total.Duration)
		}
		if t.Verify.Duration == 0 {
			t.Verify.Duration = min(30*time.Minute, t.Total.Duration)
		}
		c.Workflows[name] = w
	}
}
```

`internal/config/validate.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	workflowNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	secretNameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	envNameRE      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	memoryRE       = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)
)

// reservedEnvPrefixes are set by Fugaro itself or change git and Claude Code behavior.
var reservedEnvPrefixes = []string{"FUGARO_", "ANTHROPIC_", "CLAUDE_CODE_", "GIT_"}

// Validate reports every rule a defaulted config breaks.
func Validate(c *Config) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if c.Version != 1 {
		add("version", "must be 1")
	}
	if !slices.Contains([]string{"github", "bitbucket"}, c.Git.Provider) {
		add("git.provider", "must be one of github, bitbucket")
	}
	if !slices.Contains([]string{"vertex", "api-key", "oauth"}, c.Agent.Auth) {
		add("agent.auth", "must be one of vertex, api-key, oauth")
	}
	if c.Agent.ReviewRounds < 1 || c.Agent.ReviewRounds > 10 {
		add("agent.review_rounds", "must be between 1 and 10")
	}
	if c.Agent.MaxBudgetUSD < 0 {
		add("agent.max_budget_usd", "must not be negative")
	}
	if len(c.Workflows) == 0 {
		add("workflows", "must define at least one workflow")
	}
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		p := "workflows." + name
		if !workflowNameRE.MatchString(name) {
			add(p, "workflow name must match %s", workflowNameRE)
		}
		if !slices.Contains([]string{"server-jvm", "web-node"}, w.Base) {
			add(p+".base", "must be one of server-jvm, web-node")
		}
		if strings.TrimSpace(w.Commands.Build) == "" {
			add(p+".commands.build", "is required")
		}
		if strings.TrimSpace(w.Commands.Test) == "" {
			add(p+".commands.test", "is required")
		}
		if rf := w.Commands.RerunFailed; rf != nil {
			if strings.TrimSpace(rf.Command) == "" {
				add(p+".commands.rerun_failed.command", "is required")
			}
			if !strings.Contains(rf.Each, "{id}") {
				add(p+".commands.rerun_failed.each", "must contain {id}")
			}
		}
		for i, ce := range w.Cache {
			cp := fmt.Sprintf("%s.cache[%d]", p, i)
			if len(ce.Key) == 0 {
				add(cp+".key", "must list at least one file")
			}
			if len(ce.Paths) == 0 {
				add(cp+".paths", "must list at least one path")
			}
		}
		seen := map[string]bool{}
		for i, s := range w.Secrets {
			sp := fmt.Sprintf("%s.secrets[%d]", p, i)
			if !secretNameRE.MatchString(s.Name) {
				add(sp+".name", "must be lower-case letters, digits and dashes")
			}
			switch {
			case !envNameRE.MatchString(s.Env):
				add(sp+".env", "must be an upper-case environment variable name")
			case reservedEnv(s.Env):
				add(sp+".env", "%s uses a reserved prefix (%s)", s.Env, strings.Join(reservedEnvPrefixes, ", "))
			case seen[s.Env]:
				add(sp+".env", "%s is declared twice", s.Env)
			}
			seen[s.Env] = true
		}
		if !slices.Contains([]int{1, 2, 4, 6, 8}, w.Resources.CPU) {
			add(p+".resources.cpu", "must be one of 1, 2, 4, 6, 8")
		}
		if !memoryRE.MatchString(w.Resources.Memory) {
			add(p+".resources.memory", "must look like 512Mi or 16Gi")
		}
		t := w.Timeouts
		for _, d := range []struct {
			name string
			v    Duration
		}{{"total", t.Total}, {"stage", t.Stage}, {"verify", t.Verify}, {"finalize_reserve", t.FinalizeReserve}} {
			if d.v.Duration <= 0 {
				add(p+".timeouts."+d.name, "must be positive")
			}
		}
		if t.FinalizeReserve.Duration >= t.Total.Duration {
			add(p+".timeouts.finalize_reserve", "must be shorter than timeouts.total")
		}
		if t.Stage.Duration > t.Total.Duration {
			add(p+".timeouts.stage", "must not exceed timeouts.total")
		}
	}
	return ps
}

func reservedEnv(name string) bool {
	for _, prefix := range reservedEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Check reports files a config refers to that do not exist under root, the
// repository checkout. `fugaro validate` runs it; the runner does not.
func Check(c *Config, root string) []Problem {
	var ps []Problem
	mustExist := func(path, rel string) {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			ps = append(ps, Problem{Path: path, Message: rel + " does not exist"})
		}
	}
	if c.Agent.Instructions != "" {
		mustExist("agent.instructions", c.Agent.Instructions)
	}
	// A review starting with "/" names a skill; anything else is a prompt file.
	if c.Agent.Review != "" && !strings.HasPrefix(c.Agent.Review, "/") {
		mustExist("agent.review", c.Agent.Review)
	}
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		p := "workflows." + name
		if w.Dockerfile != "" {
			mustExist(p+".dockerfile", w.Dockerfile)
		}
		for _, cmd := range []struct{ path, value string }{
			{p + ".commands.build", w.Commands.Build},
			{p + ".commands.test", w.Commands.Test},
		} {
			fields := strings.Fields(cmd.value)
			if len(fields) > 0 && strings.HasPrefix(fields[0], "./") {
				mustExist(cmd.path, fields[0])
			}
		}
	}
	return ps
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config
git commit -m "feat(config): parse, default and validate fugaro.yaml"
```

---

### Task 3: JSON Schema, annotated example, `fugaro validate`, and `fugaro config example`

**Files:**
- Create: `schemas/fugaro.schema.json`, `schemas/schemas_test.go`, `internal/config/example.yaml`, `internal/config/example.go`, `internal/config/corpus_test.go`, `testdata/config/valid/{minimal,two-workflows,full}.yaml`, `testdata/config/invalid/{bad-provider,unknown-field,missing-test,reserved-env,bad-rerun-each,bad-memory,no-workflows}.yaml`, `internal/cli/validate.go`, `internal/cli/configcmd.go`, `internal/cli/validate_test.go`
- Modify: `internal/cli/root.go` (register the commands)

**Interfaces:**
- Consumes: `config.Parse`, `config.Check`, `config.Problem`
- Produces: `config.Example []byte` (the embedded annotated template); the `fugaro validate [path] [--json]` command, whose JSON is `{"valid": bool, "problems": [Problem]}`; and `fugaro config example`

The schema is the published contract for editors and agents. Go-side validation is authoritative for messages. A shared corpus keeps the two in agreement.

- [ ] **Step 1: Add the test-only dependency**

```bash
go get github.com/santhosh-tekuri/jsonschema/v6@latest
```

- [ ] **Step 2: Write the corpus files**

`testdata/config/valid/minimal.yaml`:

```yaml
version: 1
git:
  provider: github
workflows:
  server:
    base: server-jvm
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
```

`testdata/config/valid/two-workflows.yaml`:

```yaml
version: 1
git:
  provider: bitbucket
  base_branch: develop
workflows:
  server:
    base: server-jvm
    commands: { build: ./gradlew assemble, test: ./gradlew test }
  web:
    base: web-node
    commands: { build: pnpm build, test: pnpm test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
```

`testdata/config/valid/full.yaml`:

```yaml
version: 1
git:
  provider: github
  base_branch: main
  pr: { labels: [fugaro], reviewers: [octocat] }
agent:
  auth: api-key
  model: claude-opus-5-5
  review_rounds: 3
  max_budget_usd: 40
  review: /review
workflows:
  server:
    base: server-jvm
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
      rerun_failed: { command: ./gradlew test, each: "--tests {id}" }
      reports: ["**/build/test-results/**/*.xml"]
    cache:
      - { key: [gradle/libs.versions.toml], paths: [~/.gradle/caches] }
    secrets:
      - { name: artifactory-token, env: ARTIFACTORY_TOKEN }
    resources: { cpu: 8, memory: 32Gi }
    timeouts: { total: 90m, stage: 40m, verify: 30m, finalize_reserve: 5m }
```

`testdata/config/invalid/bad-provider.yaml`:

```yaml
version: 1
git: { provider: gitlab }
workflows:
  server: { base: server-jvm, commands: { build: make, test: make test } }
```

`testdata/config/invalid/unknown-field.yaml`:

```yaml
version: 1
git: { provider: github }
workflows:
  server: { base: server-jvm, color: blue, commands: { build: make, test: make test } }
```

`testdata/config/invalid/missing-test.yaml`:

```yaml
version: 1
git: { provider: github }
workflows:
  server: { base: server-jvm, commands: { build: make } }
```

`testdata/config/invalid/reserved-env.yaml`:

```yaml
version: 1
git: { provider: github }
workflows:
  server:
    base: server-jvm
    commands: { build: make, test: make test }
    secrets: [{ name: tok, env: GIT_TOKEN }]
```

`testdata/config/invalid/bad-rerun-each.yaml`:

```yaml
version: 1
git: { provider: github }
workflows:
  server:
    base: server-jvm
    commands: { build: make, test: make test, rerun_failed: { command: make test, each: "--tests" } }
```

`testdata/config/invalid/bad-memory.yaml`:

```yaml
version: 1
git: { provider: github }
workflows:
  server:
    base: server-jvm
    commands: { build: make, test: make test }
    resources: { memory: 32GB }
```

`testdata/config/invalid/no-workflows.yaml`:

```yaml
version: 1
git: { provider: github }
```

- [ ] **Step 3: Write the failing tests**

`internal/config/corpus_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func corpus(t *testing.T, kind string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", kind, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no %s corpus files: %v", kind, err)
	}
	return files
}

func TestCorpus(t *testing.T) {
	for _, f := range corpus(t, "valid") {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, ps := Parse(data); len(ps) > 0 {
			t.Errorf("%s: unexpected problems %v", f, ps)
		}
	}
	for _, f := range corpus(t, "invalid") {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if cfg, _ := Parse(data); cfg != nil {
			t.Errorf("%s: parsed without problems, want invalid", f)
		}
	}
}

func TestExampleIsValid(t *testing.T) {
	if _, ps := Parse(Example); len(ps) > 0 {
		t.Fatalf("the embedded example has problems: %v", ps)
	}
}
```

`schemas/schemas_test.go`:

```go
package schemas_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
)

// compile loads a schema file and compiles it under its $id.
func compile(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	id := doc.(map[string]any)["$id"].(string)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// yamlInstance converts YAML to the JSON value model the validator expects.
func yamlInstance(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

func globAll(t *testing.T, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		t.Fatalf("no files match %s: %v", pattern, err)
	}
	return files
}

func TestFugaroSchemaCorpus(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	for _, f := range globAll(t, "../testdata/config/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid config: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/config/invalid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err == nil {
			t.Errorf("%s: schema accepts an invalid config", f)
		}
	}
	if err := sch.Validate(yamlInstance(t, config.Example)); err != nil {
		t.Errorf("schema rejects the embedded example: %v", err)
	}
}
```

`internal/cli/validate_test.go`:

```go
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

const cliMinimalYAML = `version: 1
git: { provider: github }
workflows:
  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fugaro.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateValid(t *testing.T) {
	out, _, err := execute(t, "validate", writeConfig(t, cliMinimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("output = %q", out)
	}
}

func TestValidateJSONProblems(t *testing.T) {
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "github", "gitlab", 1))
	out, _, err := execute(t, "validate", "--json", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit code = %d, want %d (err %v)", ExitCode(err), ExitUserError, err)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got.Valid || len(got.Problems) != 1 || got.Problems[0].Path != "git.provider" {
		t.Fatalf("got %+v", got)
	}
}

func TestValidateChecksFiles(t *testing.T) {
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "sh build.sh", "./build.sh", 1))
	out, _, err := execute(t, "validate", path)
	if err == nil || !strings.Contains(out, "workflows.app.commands.build: ./build.sh does not exist") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestConfigExample(t *testing.T) {
	out, _, err := execute(t, "config", "example")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(config.Example) {
		t.Fatal("config example does not print the embedded example")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/config/ ./internal/cli/ ./schemas/`
Expected: FAIL, compile errors (`undefined: Example`, `undefined: validateOutput`) and a missing `fugaro.schema.json`.

- [ ] **Step 5: Write the schema** — `schemas/fugaro.schema.json`

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/dimipaun/fugaro/main/schemas/fugaro.schema.json",
  "title": "fugaro.yaml",
  "description": "Per-repository Fugaro configuration. See docs/design/v1.md §5.1.",
  "type": "object",
  "additionalProperties": false,
  "required": ["version", "git", "workflows"],
  "properties": {
    "version": { "const": 1 },
    "git": {
      "type": "object",
      "additionalProperties": false,
      "required": ["provider"],
      "properties": {
        "provider": { "enum": ["github", "bitbucket"] },
        "base_branch": { "type": "string", "minLength": 1 },
        "pr": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "labels": { "type": "array", "items": { "type": "string" } },
            "reviewers": { "type": "array", "items": { "type": "string" } }
          }
        }
      }
    },
    "agent": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "auth": { "enum": ["vertex", "api-key", "oauth"] },
        "model": { "type": "string" },
        "review_rounds": { "type": "integer", "minimum": 1, "maximum": 10 },
        "max_budget_usd": { "type": "number", "minimum": 0 },
        "instructions": { "type": "string" },
        "review": { "type": "string" }
      }
    },
    "workflows": {
      "type": "object",
      "minProperties": 1,
      "propertyNames": { "pattern": "^[a-z][a-z0-9-]{0,19}$" },
      "additionalProperties": { "$ref": "#/$defs/workflow" }
    }
  },
  "$defs": {
    "duration": { "type": "string", "minLength": 2, "pattern": "^([0-9]+h)?([0-9]+m)?([0-9]+s)?$" },
    "workflow": {
      "type": "object",
      "additionalProperties": false,
      "required": ["base", "commands"],
      "properties": {
        "base": { "enum": ["server-jvm", "web-node"] },
        "dockerfile": { "type": "string" },
        "commands": {
          "type": "object",
          "additionalProperties": false,
          "required": ["build", "test"],
          "properties": {
            "build": { "type": "string", "minLength": 1 },
            "test": { "type": "string", "minLength": 1 },
            "rerun_failed": {
              "type": "object",
              "additionalProperties": false,
              "required": ["command", "each"],
              "properties": {
                "command": { "type": "string", "minLength": 1 },
                "each": { "type": "string", "pattern": "\\{id\\}" }
              }
            },
            "reports": { "type": "array", "items": { "type": "string" } }
          }
        },
        "cache": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["key", "paths"],
            "properties": {
              "key": { "type": "array", "minItems": 1, "items": { "type": "string" } },
              "paths": { "type": "array", "minItems": 1, "items": { "type": "string" } }
            }
          }
        },
        "secrets": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["name", "env"],
            "properties": {
              "name": { "type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,62}$" },
              "env": {
                "type": "string",
                "pattern": "^[A-Z_][A-Z0-9_]*$",
                "not": { "pattern": "^(FUGARO_|ANTHROPIC_|CLAUDE_CODE_|GIT_)" }
              }
            }
          }
        },
        "resources": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "cpu": { "enum": [1, 2, 4, 6, 8] },
            "memory": { "type": "string", "pattern": "^[1-9][0-9]*(Mi|Gi)$" }
          }
        },
        "timeouts": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "total": { "$ref": "#/$defs/duration" },
            "stage": { "$ref": "#/$defs/duration" },
            "verify": { "$ref": "#/$defs/duration" },
            "finalize_reserve": { "$ref": "#/$defs/duration" }
          }
        }
      }
    }
  }
}
```

- [ ] **Step 6: Write the example and the commands**

`internal/config/example.yaml`:

```yaml
# fugaro.yaml — how Fugaro builds and tests this repository.
# Schema: https://raw.githubusercontent.com/dimipaun/fugaro/main/schemas/fugaro.schema.json
# Check it with: fugaro validate
version: 1

git:
  provider: github            # github | bitbucket
  base_branch: main
  pr:
    labels: [fugaro]
    reviewers: []

agent:
  auth: vertex                # vertex | api-key | oauth
  # model: claude-opus-5-5    # defaults to Claude Code's default model
  review_rounds: 2            # review → fix rounds before the PR is opened
  max_budget_usd: 25          # per stage, passed to claude --max-budget-usd
  # instructions: .fugaro/instructions.md   # appended to the agent's system prompt
  # review: /review           # a skill name, or a repo-relative prompt file

workflows:
  server:                     # workflow name, chosen with `fugaro run --workflow`
    base: server-jvm          # server-jvm | web-node
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
      rerun_failed:           # optional; lets the agent prove a failure is flaky
        command: ./gradlew test
        each: "--tests {id}"  # appended once per failed test ID
      reports: ["**/build/test-results/**/*.xml"]   # JUnit XML
    secrets:                  # logical names; infrastructure maps them to Secret Manager
      - { name: artifactory-token, env: ARTIFACTORY_TOKEN }
    resources: { cpu: 8, memory: 32Gi }
    timeouts: { total: 90m, stage: 40m, verify: 30m, finalize_reserve: 5m }
```

`internal/config/example.go`:

```go
package config

import _ "embed"

// Example is the annotated fugaro.yaml template printed by `fugaro config example`.
//
//go:embed example.yaml
var Example []byte
```

`internal/cli/validate.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
)

type validateOutput struct {
	Valid    bool             `json:"valid"`
	Problems []config.Problem `json:"problems"`
}

func newValidateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Check a fugaro.yaml against the schema and the repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "fugaro.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			cfg, problems := config.Parse(data)
			if cfg != nil {
				problems = config.Check(cfg, filepath.Dir(path))
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(validateOutput{Valid: len(problems) == 0, Problems: append([]config.Problem{}, problems...)}); err != nil {
					return err
				}
			} else {
				for _, p := range problems {
					fmt.Fprintln(out, p)
				}
				if len(problems) == 0 {
					fmt.Fprintf(out, "%s is valid\n", path)
				}
			}
			if len(problems) > 0 {
				return &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s has %d problem(s)", path, len(problems))}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}
```

`internal/cli/configcmd.go`:

```go
package cli

import (
	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Work with fugaro.yaml"}
	cmd.AddCommand(&cobra.Command{
		Use:   "example",
		Short: "Print an annotated fugaro.yaml template",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(config.Example)
			return err
		},
	})
	return cmd
}
```

In `internal/cli/root.go`, replace `root.AddCommand(newVersionCmd())` with:

```go
	root.AddCommand(newVersionCmd(), newValidateCmd(), newConfigCmd())
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/config/ ./internal/cli/ ./schemas/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum schemas testdata/config internal/config internal/cli
git commit -m "feat(config): publish JSON Schema, example, validate and config example commands"
```

---

### Task 4: Task spec and run IDs

**Files:**
- Create: `internal/task/task.go`, `internal/task/task_test.go`, `schemas/task.schema.json`, `testdata/task/valid/{new,followup}.json`, `testdata/task/invalid/{no-task,partial-followup,bad-run-id,unknown-field}.json`
- Modify: `schemas/schemas_test.go` (add `TestTaskSchemaCorpus`)

**Interfaces:**
- Consumes: `config.Config`, `config.Workflow`
- Produces:
  - `task.Spec{Version int; RunID, Repo, Ref, Workflow, Task, Branch string; PR int; PreviousRun string; Overrides Overrides; RequestedBy string}`, with JSON tags as in design §5.3
  - `task.Overrides{ReviewRounds *int; MaxBudgetUSD *float64; Model string; TotalTimeout string}`
  - `task.Parse([]byte) (*Spec, error)`
  - `(*Spec).Validate() error`
  - `(*Spec).Marshal() ([]byte, error)`
  - `(*Spec).Apply(*config.Config, *config.Workflow) error`
  - `(*Spec).IsFollowUp() bool`
  - `task.NewRunID(now time.Time, r io.Reader) (string, error)`
  - `task.Slug(repo string) string`

- [ ] **Step 1: Write the corpus**

`testdata/task/valid/new.json`:

```json
{ "version": 1, "run_id": "20260926-221530-a1b2", "repo": "acme/server", "ref": "main", "workflow": "server", "task": "Add a health endpoint.", "overrides": { "review_rounds": 3 }, "requested_by": "someone@example.com" }
```

`testdata/task/valid/followup.json`:

```json
{ "version": 1, "run_id": "20260926-231530-c3d4", "repo": "acme/server", "ref": "fugaro/20260926-221530-a1b2", "branch": "fugaro/20260926-221530-a1b2", "pr": 12, "previous_run": "20260926-221530-a1b2" }
```

`testdata/task/invalid/no-task.json`:

```json
{ "version": 1, "run_id": "20260926-221530-a1b2", "repo": "acme/server", "ref": "main" }
```

`testdata/task/invalid/partial-followup.json`:

```json
{ "version": 1, "run_id": "20260926-221530-a1b2", "repo": "acme/server", "ref": "main", "task": "x", "pr": 12 }
```

`testdata/task/invalid/bad-run-id.json`:

```json
{ "version": 1, "run_id": "run-1", "repo": "acme/server", "ref": "main", "task": "x" }
```

`testdata/task/invalid/unknown-field.json`:

```json
{ "version": 1, "run_id": "20260926-221530-a1b2", "repo": "acme/server", "ref": "main", "task": "x", "priority": "high" }
```

- [ ] **Step 2: Write the failing tests** — `internal/task/task_test.go`

```go
package task

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestNewRunID(t *testing.T) {
	now := time.Date(2026, 9, 26, 22, 15, 30, 0, time.FixedZone("x", 3*3600))
	id, err := NewRunID(now, bytes.NewReader([]byte{0xab, 0xcd}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "20260926-191530-abcd" {
		t.Fatalf("id = %q (must be UTC)", id)
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("Acme/Server"); got != "acme-server" {
		t.Fatalf("Slug = %q", got)
	}
}

func TestCorpus(t *testing.T) {
	valid, _ := filepath.Glob("../../testdata/task/valid/*.json")
	invalid, _ := filepath.Glob("../../testdata/task/invalid/*.json")
	if len(valid) == 0 || len(invalid) == 0 {
		t.Fatal("missing corpus")
	}
	for _, f := range valid {
		data, _ := os.ReadFile(f)
		if _, err := Parse(data); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	for _, f := range invalid {
		data, _ := os.ReadFile(f)
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: parsed, want error", f)
		}
	}
}

func TestParseErrorMessages(t *testing.T) {
	cases := map[string]string{
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme/server","ref":"main"}`:                   "task is required",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"acme","ref":"main","task":"x"}`:               "repo",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"main","task":"x","pr":3}`:        "must be set together",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"","task":"x"}`:                    "ref is required",
		`{"version":1,"run_id":"20260926-221530-a1b2","repo":"a/b","ref":"m","task":"x","overrides":{"total_timeout":"soon"}}`: "total_timeout",
	}
	for in, want := range cases {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%s) err = %v, want it to mention %q", in, err, want)
		}
	}
}

func TestApply(t *testing.T) {
	rounds, budget := 4, 10.0
	s := &Spec{Overrides: Overrides{ReviewRounds: &rounds, MaxBudgetUSD: &budget, Model: "m", TotalTimeout: "45m"}}
	cfg := &config.Config{}
	w := &config.Workflow{Timeouts: config.Timeouts{FinalizeReserve: config.Duration{Duration: 5 * time.Minute}}}
	if err := s.Apply(cfg, w); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ReviewRounds != 4 || cfg.Agent.MaxBudgetUSD != 10 || cfg.Agent.Model != "m" || w.Timeouts.Total.Duration != 45*time.Minute {
		t.Fatalf("not applied: %+v %+v", cfg.Agent, w.Timeouts)
	}
	s.Overrides = Overrides{TotalTimeout: "4m"}
	if err := s.Apply(cfg, w); err == nil || !strings.Contains(err.Error(), "finalize_reserve") {
		t.Fatalf("Apply with total below reserve err = %v", err)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	data, _ := os.ReadFile("../../testdata/task/valid/new.json")
	s, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if *s2.Overrides.ReviewRounds != 3 || s2.Task != s.Task || s2.IsFollowUp() {
		t.Fatalf("round trip lost data: %+v", s2)
	}
}
```

Add to `schemas/schemas_test.go`:

```go
func TestTaskSchemaCorpus(t *testing.T) {
	sch := compile(t, "task.schema.json")
	jsonInstance := func(f string) any {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return inst
	}
	for _, f := range globAll(t, "../testdata/task/valid/*.json") {
		if err := sch.Validate(jsonInstance(f)); err != nil {
			t.Errorf("%s: schema rejects a valid task: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/task/invalid/*.json") {
		if err := sch.Validate(jsonInstance(f)); err == nil {
			t.Errorf("%s: schema accepts an invalid task", f)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/task/ ./schemas/`
Expected: FAIL, a compile error (`undefined: NewRunID`) and a missing `task.schema.json`.

- [ ] **Step 4: Implement** — `internal/task/task.go`

```go
// Package task defines the task spec handed to a Fugaro run (design §5.3).
package task

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

var (
	runIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)
)

// Spec is the task spec stored as runs/<repo-slug>/<run-id>/task.json.
type Spec struct {
	Version     int       `json:"version"`
	RunID       string    `json:"run_id"`
	Repo        string    `json:"repo"`
	Ref         string    `json:"ref"`
	Workflow    string    `json:"workflow,omitempty"`
	Task        string    `json:"task,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	PR          int       `json:"pr,omitempty"`
	PreviousRun string    `json:"previous_run,omitempty"`
	Overrides   Overrides `json:"overrides"`
	RequestedBy string    `json:"requested_by,omitempty"`
}

// Overrides are the only config values a single task may change.
type Overrides struct {
	ReviewRounds *int     `json:"review_rounds,omitempty"`
	MaxBudgetUSD *float64 `json:"max_budget_usd,omitempty"`
	Model        string   `json:"model,omitempty"`
	TotalTimeout string   `json:"total_timeout,omitempty"`
}

// NewRunID returns a run ID for now, in UTC, with 4 random hex digits from r.
func NewRunID(now time.Time, r io.Reader) (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generating run ID: %w", err)
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}

// Slug turns a repo path such as "Acme/Server" into its storage prefix "acme-server".
func Slug(repo string) string {
	return strings.ReplaceAll(strings.ToLower(repo), "/", "-")
}

// Parse decodes and validates a task spec, rejecting unknown fields.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("task spec: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// IsFollowUp reports whether the task continues an existing Fugaro PR.
func (s *Spec) IsFollowUp() bool { return s.Branch != "" }

// Validate checks the spec's own rules; it does not look at the repository.
func (s *Spec) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Version != 1 {
		bad("task spec: version must be 1")
	}
	if !runIDRE.MatchString(s.RunID) {
		bad("task spec: run_id %q must look like 20260926-221530-a1b2", s.RunID)
	}
	if !repoRE.MatchString(s.Repo) {
		bad("task spec: repo %q must look like owner/name", s.Repo)
	}
	if strings.TrimSpace(s.Ref) == "" {
		bad("task spec: ref is required")
	}
	anyFollowUp := s.Branch != "" || s.PR != 0 || s.PreviousRun != ""
	allFollowUp := s.Branch != "" && s.PR > 0 && s.PreviousRun != ""
	if anyFollowUp && !allFollowUp {
		bad("task spec: branch, pr and previous_run must be set together")
	}
	if !anyFollowUp && strings.TrimSpace(s.Task) == "" {
		bad("task spec: task is required")
	}
	if s.PreviousRun != "" && !runIDRE.MatchString(s.PreviousRun) {
		bad("task spec: previous_run %q is not a run ID", s.PreviousRun)
	}
	o := s.Overrides
	if o.ReviewRounds != nil && (*o.ReviewRounds < 1 || *o.ReviewRounds > 10) {
		bad("task spec: overrides.review_rounds must be between 1 and 10")
	}
	if o.MaxBudgetUSD != nil && *o.MaxBudgetUSD < 0 {
		bad("task spec: overrides.max_budget_usd must not be negative")
	}
	if o.TotalTimeout != "" {
		if d, err := time.ParseDuration(o.TotalTimeout); err != nil || d <= 0 {
			bad("task spec: overrides.total_timeout %q must be a positive duration such as 45m", o.TotalTimeout)
		}
	}
	return errors.Join(errs...)
}

// Marshal encodes the spec as indented JSON.
func (s *Spec) Marshal() ([]byte, error) { return json.MarshalIndent(s, "", "  ") }

// Apply writes the overrides into the repository config and selected workflow.
func (s *Spec) Apply(c *config.Config, w *config.Workflow) error {
	o := s.Overrides
	if o.ReviewRounds != nil {
		c.Agent.ReviewRounds = *o.ReviewRounds
	}
	if o.MaxBudgetUSD != nil {
		c.Agent.MaxBudgetUSD = *o.MaxBudgetUSD
	}
	if o.Model != "" {
		c.Agent.Model = o.Model
	}
	if o.TotalTimeout != "" {
		d, err := time.ParseDuration(o.TotalTimeout)
		if err != nil {
			return fmt.Errorf("overrides.total_timeout: %w", err)
		}
		if d <= w.Timeouts.FinalizeReserve.Duration {
			return fmt.Errorf("overrides.total_timeout %s must be longer than the workflow's timeouts.finalize_reserve %s", d, w.Timeouts.FinalizeReserve.Duration)
		}
		w.Timeouts.Total.Duration = d
		w.Timeouts.Stage.Duration = min(w.Timeouts.Stage.Duration, d)
	}
	return nil
}
```

`schemas/task.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/dimipaun/fugaro/main/schemas/task.schema.json",
  "title": "Fugaro task spec",
  "description": "runs/<repo-slug>/<run-id>/task.json. See docs/design/v1.md §5.3.",
  "type": "object",
  "additionalProperties": false,
  "required": ["version", "run_id", "repo", "ref"],
  "properties": {
    "version": { "const": 1 },
    "run_id": { "type": "string", "pattern": "^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$" },
    "repo": { "type": "string", "pattern": "^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$" },
    "ref": { "type": "string", "minLength": 1 },
    "workflow": { "type": "string" },
    "task": { "type": "string" },
    "branch": { "type": "string", "minLength": 1 },
    "pr": { "type": "integer", "minimum": 1 },
    "previous_run": { "type": "string", "pattern": "^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$" },
    "overrides": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "review_rounds": { "type": "integer", "minimum": 1, "maximum": 10 },
        "max_budget_usd": { "type": "number", "minimum": 0 },
        "model": { "type": "string" },
        "total_timeout": { "type": "string", "minLength": 2, "pattern": "^([0-9]+h)?([0-9]+m)?([0-9]+s)?$" }
      }
    },
    "requested_by": { "type": "string" }
  },
  "dependentRequired": {
    "branch": ["pr", "previous_run"],
    "pr": ["branch", "previous_run"],
    "previous_run": ["branch", "pr"]
  },
  "if": { "not": { "required": ["branch"] } },
  "then": { "required": ["task"], "properties": { "task": { "minLength": 1 } } }
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/task/ ./schemas/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/task schemas testdata/task
git commit -m "feat(task): task spec, run IDs and task schema"
```

---

### Task 5: Process groups (`procgroup`)

**Files:**
- Create: `internal/procgroup/procgroup.go`, `internal/procgroup/procgroup_test.go`

**Interfaces:**
- Produces:
  - `procgroup.Cmd{Name string; Args []string; Dir string; Env []string; Stdin io.Reader; Stdout, Stderr io.Writer; Grace time.Duration}`
  - `procgroup.Run(ctx context.Context, c Cmd) (exitCode int, err error)`. `err` is `ctx.Err()` when the context ended the command. A non-zero exit is **not** an error.

- [ ] **Step 1: Write the failing tests** — `internal/procgroup/procgroup_test.go`

```go
package procgroup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func sh(script string) Cmd { return Cmd{Name: "sh", Args: []string{"-c", script}} }

func TestExitCode(t *testing.T) {
	code, err := Run(context.Background(), sh("exit 3"))
	if err != nil || code != 3 {
		t.Fatalf("code=%d err=%v, want 3 <nil>", code, err)
	}
}

func TestCapturesOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	c := sh("echo out; echo err >&2")
	c.Stdout, c.Stderr = &out, &errOut
	if _, err := Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if out.String() != "out\n" || errOut.String() != "err\n" {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestContextKillsGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c := sh("sleep 30")
	c.Grace = 100 * time.Millisecond
	start := time.Now()
	_, err := Run(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Run did not return promptly after the deadline")
	}
}

// A background process that inherits stdout (like a Gradle daemon) must
// neither keep Run waiting nor survive it.
func TestReapsLeakedDaemon(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	var out bytes.Buffer
	c := sh("sleep 30 & echo $! > " + pidFile + "; echo started")
	c.Stdout = &out
	start := time.Now()
	code, err := Run(context.Background(), c)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Run waited for the leaked daemon")
	}
	if !strings.Contains(out.String(), "started") {
		t.Fatalf("stdout = %q", out.String())
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("leaked daemon %d is still alive", pid)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/procgroup/`
Expected: FAIL, a compile error (`undefined: Run`).

- [ ] **Step 3: Implement** — `internal/procgroup/procgroup.go`

```go
// Package procgroup runs a command in its own process group so that the whole
// tree, including background daemons it leaves behind, can be killed.
package procgroup

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Cmd describes a command to run.
type Cmd struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string // nil means the current process environment
	Stdin  io.Reader
	Stdout io.Writer // nil discards
	Stderr io.Writer // nil discards
	// Grace is how long to wait after SIGTERM before SIGKILL; zero means 10s.
	Grace time.Duration
}

// Run starts c in a new process group and waits for it. When ctx ends, the
// group gets SIGTERM, then SIGKILL after the grace period, and Run returns
// ctx.Err(). When the command exits, any processes it left in the group are
// killed. A non-zero exit status is reported through the exit code, not err.
func Run(ctx context.Context, c Cmd) (int, error) {
	grace := c.Grace
	if grace == 0 {
		grace = 10 * time.Second
	}
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = c.Dir, c.Env, c.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Our own pipes, rather than exec's, so Wait returns when the command
	// exits even if a leaked child still holds the write ends.
	outR, outW, err := os.Pipe()
	if err != nil {
		return -1, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return -1, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	startErr := cmd.Start()
	outW.Close()
	errW.Close()
	if startErr != nil {
		outR.Close()
		errR.Close()
		return -1, startErr
	}

	var copies sync.WaitGroup
	for _, p := range []struct {
		r *os.File
		w io.Writer
	}{{outR, c.Stdout}, {errR, c.Stderr}} {
		copies.Add(1)
		go func() {
			defer copies.Done()
			w := p.w
			if w == nil {
				w = io.Discard
			}
			_, _ = io.Copy(w, p.r)
		}()
	}

	pgid := cmd.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var waitErr, ctxErr error
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		ctxErr = ctx.Err()
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case waitErr = <-waited:
		case <-time.After(grace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			waitErr = <-waited
		}
	}
	// Reap whatever the command left behind in its group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	copied := make(chan struct{})
	go func() { copies.Wait(); close(copied) }()
	select {
	case <-copied:
	case <-time.After(2 * time.Second): // a child escaped the group and still holds a pipe
	}
	outR.Close()
	errR.Close()

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if ctxErr != nil {
		return code, ctxErr
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return code, waitErr
	}
	return code, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/procgroup/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/procgroup
git commit -m "feat(procgroup): run commands in killable process groups"
```

---

### Task 6: Git operations (`gitops`) and test git helpers

**Files:**
- Create: `internal/gitops/gitops.go`, `internal/gitops/gitops_test.go`, `internal/testutil/git.go`

**Interfaces:**
- Produces:
  - Git operations:
    - `gitops.Identity map[string]string` and `gitops.IdentityEnv() []string`
    - `gitops.Repo{Dir string; Env []string}`
    - `gitops.Open(dir string, env []string) (*Repo, error)`
    - `gitops.OpenOrClone(ctx, dir, remote string, env []string) (*Repo, error)`
  - Methods on `*Repo`:
    - `CheckoutNewBranch(ctx, ref, branch string) error`
    - `FetchBase(ctx, base string) error`
    - `HeadSHA(ctx) (string, error)`
    - `IsClean(ctx) (bool, error)`
    - `CommitAll(ctx, msg string) (bool, error)`
    - `CommitEmpty(ctx, msg string) error`
    - `AheadOf(ctx, base string) (int, error)`
    - `Push(ctx, branch string) error`
  - Test helpers:
    - `testutil.IsolateGit(t)`
    - `testutil.Git(t, dir string, args ...string) string`
    - `testutil.NewRemote(t, files map[string]string) string` (path of a bare repo)
    - `testutil.WriteFiles(t, dir string, files map[string]string)`
    - `testutil.ModuleRoot() string`

- [ ] **Step 1: Write the test helpers** — `internal/testutil/git.go`

```go
// Package testutil holds helpers shared by tests.
package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ModuleRoot returns the repository root.
func ModuleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// IsolateGit keeps the developer's global and system git config (signing,
// hooks, templates) out of the test, and sets a commit identity.
func IsolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}
}

// Git runs git in dir and returns its trimmed stdout, failing the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// WriteFiles writes files (path → content) under dir, creating directories.
// Every file is executable so fixture scripts can run directly.
func WriteFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// NewRemote creates a bare repository whose main branch holds files and returns its path.
func NewRemote(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	Git(t, root, "init", "--quiet", "--bare", "-b", "main", bare)
	seed := filepath.Join(root, "seed")
	Git(t, root, "clone", "--quiet", bare, seed)
	WriteFiles(t, seed, files)
	Git(t, seed, "add", "-A")
	Git(t, seed, "commit", "--quiet", "-m", "seed")
	Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return bare
}
```

- [ ] **Step 2: Write the failing tests** — `internal/gitops/gitops_test.go`

```go
package gitops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

var ctx = context.Background()

func setup(t *testing.T) (*Repo, string) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{
		"README.md":  "hello\n",
		".gitignore": "build/\nnode_modules/\n",
	})
	repo, err := OpenOrClone(ctx, filepath.Join(t.TempDir(), "work"), remote, IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	return repo, remote
}

func TestOpenOrCloneNeedsRemote(t *testing.T) {
	if _, err := OpenOrClone(ctx, t.TempDir(), "", nil); err == nil {
		t.Fatal("want an error when there is no checkout and no remote")
	}
}

func TestCheckoutNewBranchKeepsIgnored(t *testing.T) {
	repo, _ := setup(t)
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"node_modules/dep/index.js": "warm cache\n",
		"stray.txt":                 "untracked\n",
	})
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/x" {
		t.Fatalf("branch = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "stray.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file survived the reset")
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "node_modules", "dep", "index.js")); err != nil {
		t.Fatal("ignored warm cache was wiped:", err)
	}
}

func TestCommitAllAheadAndClean(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.CommitAll(ctx, "nothing")
	if err != nil || changed {
		t.Fatalf("CommitAll on a clean tree = %v, %v", changed, err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if clean, _ := repo.IsClean(ctx); clean {
		t.Fatal("IsClean = true with an untracked file")
	}
	changed, err = repo.CommitAll(ctx, "add a")
	if err != nil || !changed {
		t.Fatalf("CommitAll = %v, %v", changed, err)
	}
	if clean, _ := repo.IsClean(ctx); !clean {
		t.Fatal("IsClean = false after commit")
	}
	if n, err := repo.AheadOf(ctx, "main"); err != nil || n != 1 {
		t.Fatalf("AheadOf = %d, %v", n, err)
	}
	if err := repo.CommitEmpty(ctx, "empty"); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.AheadOf(ctx, "main"); n != 2 {
		t.Fatalf("AheadOf after empty commit = %d", n)
	}
	if got := testutil.Git(t, repo.Dir, "log", "-1", "--format=%an"); got != "Fugaro" {
		t.Fatalf("author = %q, want Fugaro", got)
	}
}

func TestCommitAllIgnoresFailingHook(t *testing.T) {
	repo, _ := setup(t)
	hook := filepath.Join(repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal("a failing pre-commit hook blocked the runner's commit:", err)
	}
}

func TestPushCreatesRemoteBranch(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.HeadSHA(ctx)
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != head {
		t.Fatalf("remote branch = %s, want %s", got, head)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/gitops/`
Expected: FAIL, a compile error (`undefined: OpenOrClone`).

- [ ] **Step 4: Implement** — `internal/gitops/gitops.go`

```go
// Package gitops wraps the git CLI operations a run needs.
package gitops

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Identity is the author and committer of commits made during a run.
var Identity = map[string]string{
	"GIT_AUTHOR_NAME":     "Fugaro",
	"GIT_AUTHOR_EMAIL":    "fugaro@users.noreply.invalid",
	"GIT_COMMITTER_NAME":  "Fugaro",
	"GIT_COMMITTER_EMAIL": "fugaro@users.noreply.invalid",
}

// IdentityEnv returns Identity as KEY=VALUE pairs.
func IdentityEnv() []string {
	out := make([]string, 0, len(Identity))
	for _, k := range slices.Sorted(maps.Keys(Identity)) {
		out = append(out, k+"="+Identity[k])
	}
	return out
}

// Repo is a git checkout. Env is added to the process environment for every git call.
type Repo struct {
	Dir string
	Env []string
}

// Open returns the checkout at dir.
func Open(dir string, env []string) (*Repo, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, fmt.Errorf("%s is not a git checkout: %w", dir, err)
	}
	return &Repo{Dir: dir, Env: env}, nil
}

// OpenOrClone returns the checkout at dir, cloning remote into it first if
// dir has none. In the container image the checkout is baked in.
func OpenOrClone(ctx context.Context, dir, remote string, env []string) (*Repo, error) {
	if r, err := Open(dir, env); err == nil {
		return r, nil
	}
	if remote == "" {
		return nil, fmt.Errorf("%s has no git checkout and no remote to clone", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	parent := &Repo{Dir: filepath.Dir(dir), Env: env}
	if _, err := parent.git(ctx, "clone", "--quiet", remote, dir); err != nil {
		return nil, err
	}
	return &Repo{Dir: dir, Env: env}, nil
}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), r.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CheckoutNewBranch fetches ref from origin and points a fresh branch at it.
// Untracked files are removed but ignored ones (warm build caches) are kept.
func (r *Repo) CheckoutNewBranch(ctx context.Context, ref, branch string) error {
	if _, err := r.git(ctx, "fetch", "--quiet", "origin", ref); err != nil {
		return err
	}
	if _, err := r.git(ctx, "checkout", "--quiet", "-f", "-B", branch, "FETCH_HEAD"); err != nil {
		return err
	}
	_, err := r.git(ctx, "clean", "-fd", "--quiet")
	return err
}

// FetchBase updates origin/<base>, which AheadOf and the reviewer diff against.
func (r *Repo) FetchBase(ctx context.Context, base string) error {
	_, err := r.git(ctx, "fetch", "--quiet", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base)
	return err
}

// HeadSHA returns the commit HEAD points at.
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	return r.git(ctx, "rev-parse", "HEAD")
}

// IsClean reports whether the working tree has no changes and no untracked files.
func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "status", "--porcelain")
	return out == "", err
}

// CommitAll commits every change, skipping repository hooks. It reports
// whether there was anything to commit.
func (r *Repo) CommitAll(ctx context.Context, msg string) (bool, error) {
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return false, err
	}
	if clean, err := r.IsClean(ctx); err != nil || clean {
		return false, err
	}
	_, err := r.git(ctx, "commit", "--quiet", "--no-verify", "-m", msg)
	return err == nil, err
}

// CommitEmpty makes a commit with no changes, so a PR can exist for a branch
// where the agent committed nothing.
func (r *Repo) CommitEmpty(ctx context.Context, msg string) error {
	_, err := r.git(ctx, "commit", "--quiet", "--no-verify", "--allow-empty", "-m", msg)
	return err
}

// AheadOf counts commits on HEAD that origin/<base> does not have.
func (r *Repo) AheadOf(ctx context.Context, base string) (int, error) {
	out, err := r.git(ctx, "rev-list", "--count", "origin/"+base+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// Push pushes HEAD to branch on origin.
func (r *Repo) Push(ctx context.Context, branch string) error {
	_, err := r.git(ctx, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return err
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/gitops/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gitops internal/testutil
git commit -m "feat(gitops): git operations for the run lifecycle"
```

---

### Task 7: JUnit parsing and report collection

**Files:**
- Create: `internal/verify/junit.go`, `internal/verify/reports.go`, `internal/verify/junit_test.go`, `internal/verify/reports_test.go`

**Interfaces:**
- Produces:
  - `verify.TestCase{ID string; Failed, Skipped bool}`. `ID` is `classname.name`, or `name` when there is no classname.
  - `verify.ParseJUnit(io.Reader) ([]TestCase, error)`
  - `verify.CollectReports(root string, globs []string, since time.Time) ([]string, error)`
  - `verify.ReadCases(root string, globs []string, since time.Time) ([]TestCase, error)`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/bmatcuk/doublestar/v4@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/verify/junit_test.go`:

```go
package verify

import (
	"strings"
	"testing"
)

func TestParseJUnitSuites(t *testing.T) {
	xml := `<?xml version="1.0"?>
<testsuites>
  <testsuite name="a">
    <testcase classname="pkg.A" name="ok"/>
    <testcase classname="pkg.A" name="bad"><failure message="x"/></testcase>
    <testcase classname="pkg.A" name="err"><error message="y"/></testcase>
    <testsuite name="nested">
      <testcase classname="pkg.B" name="skip"><skipped/></testcase>
    </testsuite>
  </testsuite>
</testsuites>`
	cases, err := ParseJUnit(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	want := []TestCase{
		{ID: "pkg.A.ok"},
		{ID: "pkg.A.bad", Failed: true},
		{ID: "pkg.A.err", Failed: true},
		{ID: "pkg.B.skip", Skipped: true},
	}
	if len(cases) != len(want) {
		t.Fatalf("got %d cases: %+v", len(cases), cases)
	}
	for i := range want {
		if cases[i] != want[i] {
			t.Errorf("case %d = %+v, want %+v", i, cases[i], want[i])
		}
	}
}

func TestParseJUnitSingleSuiteNoClassname(t *testing.T) {
	cases, err := ParseJUnit(strings.NewReader(`<testsuite><testcase name="renders"/></testsuite>`))
	if err != nil || len(cases) != 1 || cases[0].ID != "renders" {
		t.Fatalf("cases=%+v err=%v", cases, err)
	}
}

func TestParseJUnitMalformed(t *testing.T) {
	if _, err := ParseJUnit(strings.NewReader("<testsuite><testcase")); err == nil {
		t.Fatal("want an error for malformed XML")
	}
}
```

`internal/verify/reports_test.go`:

```go
package verify

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestCollectReportsIgnoresStale(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{
		"a/build/test-results/test/TEST-new.xml": "<testsuite/>",
		"a/build/test-results/test/TEST-old.xml": "<testsuite/>",
		"node_modules/x/build/test-results/TEST-dep.xml": "<testsuite/>",
		"a/build/other.xml": "<testsuite/>",
	})
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a/build/test-results/test/TEST-old.xml"), old, old); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	files, err := CollectReports(root, []string{"**/build/test-results/**/*.xml"}, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "TEST-new.xml" {
		t.Fatalf("files = %v, want only TEST-new.xml", files)
	}
}

func TestReadCasesReportsBadFile(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"junit.xml": "<testsuite><testcase"})
	if _, err := ReadCases(root, []string{"junit.xml"}, time.Time{}); err == nil {
		t.Fatal("want an error naming the malformed report")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/verify/`
Expected: FAIL, a compile error (`undefined: ParseJUnit`).

- [ ] **Step 4: Implement**

`internal/verify/junit.go`:

```go
package verify

import (
	"encoding/xml"
	"fmt"
	"io"
)

// TestCase is one test result from a JUnit report.
type TestCase struct {
	ID      string
	Failed  bool
	Skipped bool
}

type junitSuite struct {
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	Classname string    `xml:"classname,attr"`
	Name      string    `xml:"name,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

// ParseJUnit reads a JUnit XML report whose root is <testsuites> or <testsuite>.
func ParseJUnit(r io.Reader) ([]TestCase, error) {
	var root junitSuite
	if err := xml.NewDecoder(r).Decode(&root); err != nil {
		return nil, fmt.Errorf("parsing JUnit XML: %w", err)
	}
	var out []TestCase
	var walk func(junitSuite)
	walk = func(s junitSuite) {
		for _, c := range s.Cases {
			id := c.Name
			if c.Classname != "" {
				id = c.Classname + "." + c.Name
			}
			out = append(out, TestCase{ID: id, Failed: c.Failure != nil || c.Error != nil, Skipped: c.Skipped != nil})
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	walk(root)
	return out, nil
}
```

`internal/verify/reports.go`:

```go
package verify

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// skipDirs are never searched for reports: they are huge and never hold the
// repository's own results.
var skipDirs = map[string]bool{".git": true, "node_modules": true}

// CollectReports returns report files under root that match any glob
// (relative, slash-separated) and were modified at or after since, so reports
// left by earlier runs are ignored.
func CollectReports(root string, globs []string, since time.Time) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !matchAny(globs, filepath.ToSlash(rel)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Unix() < since.Unix() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

func matchAny(globs []string, rel string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// ReadCases parses every fresh report under root.
func ReadCases(root string, globs []string, since time.Time) ([]TestCase, error) {
	files, err := CollectReports(root, globs, since)
	if err != nil {
		return nil, err
	}
	var all []TestCase
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		cases, err := ParseJUnit(fh)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		all = append(all, cases...)
	}
	return all, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/verify/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/verify
git commit -m "feat(verify): JUnit parsing and fresh-report collection"
```

---

### Task 8: `fugaro verify` — recording build and test runs

**Files:**
- Create: `internal/verify/verify.go`, `internal/verify/verify_test.go`, `internal/cli/verify.go`, `internal/cli/verify_test.go`, `internal/testutil/fixture.go`, `testdata/fixture-repo/{fugaro.yaml,build.sh,test.sh,.gitignore,README.md}`
- Modify: `internal/cli/root.go` (register `newVerifyCmd()`)

**Interfaces:**
- Consumes: `procgroup.Run`, `gitops.Open`, `(*gitops.Repo).HeadSHA/IsClean`, `config.RerunFailed`, `verify.ReadCases`
- Produces:
  - `verify.Settings{RepoDir, Build, Test string; RerunFailed *config.RerunFailed; Reports []string; TimeoutS int}`
  - `verify.WriteSettings(stateDir string, s Settings) error` and `verify.LoadSettings(stateDir string) (Settings, error)`
  - `verify.Kind` (`KindBuild="build"`, `KindTest="test"`)
  - `verify.Record{N int; Kind Kind; Rerun bool; HeadSHA string; CleanTree bool; ExitCode int; TimedOut bool; Passed bool; Tests, Failures, Skipped int; Failed, Flaky []string; StartedAt time.Time; DurationS float64}`, with `Summary() string`
  - `verify.Options{StateDir string; Kind Kind; Rerun bool; Env []string; Stdout, Stderr io.Writer; Now func() time.Time}`
  - `verify.Run(ctx, Options) (Record, error)`
  - `verify.Records(stateDir string) ([]Record, error)`
  - `verify.RerunCommand(rf config.RerunFailed, ids []string) string`
  - `testutil.FixtureFiles(t) map[string]string`
  - The command `fugaro verify build|test [--rerun-failed]`, which reads `FUGARO_STATE_DIR` and exits 1 when not passed.
- State dir layout: `<state>/workflow.json` holds the settings, and `<state>/verify/NNNN.json` holds one record per run.

- [ ] **Step 1: Write the fixture repository** (it is reused by Tasks 13 and 14)

`testdata/fixture-repo/fugaro.yaml`:

```yaml
version: 1
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  review_rounds: 2
workflows:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed:
        command: sh test.sh
        each: "{id}"
      reports: ["build/test-results/*.xml"]
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
```

`testdata/fixture-repo/build.sh`:

```sh
#!/bin/sh
# Fixture build: exits with $FIXTURE_BUILD_EXIT (default 0).
exit "${FIXTURE_BUILD_EXIT:-0}"
```

`testdata/fixture-repo/test.sh`:

```sh
#!/bin/sh
# Fixture test suite: tests alpha, beta, gamma in class pkg.Suite.
# Tests named in the file $FIXTURE_FAILS_FILE fail. Arguments, if any, are test
# IDs (pkg.Suite.<name>) to run instead of the whole suite.
fails=""
if [ -n "$FIXTURE_FAILS_FILE" ] && [ -f "$FIXTURE_FAILS_FILE" ]; then
  fails=$(cat "$FIXTURE_FAILS_FILE")
fi
tests="alpha beta gamma"
if [ $# -gt 0 ]; then
  tests=""
  for id in "$@"; do tests="$tests ${id##*.}"; done
fi
mkdir -p build/test-results
out=build/test-results/TEST-suite.xml
status=0
echo '<testsuite name="suite">' > "$out"
for t in $tests; do
  case " $fails " in
    *" $t "*) echo "<testcase classname=\"pkg.Suite\" name=\"$t\"><failure message=\"boom\"/></testcase>" >> "$out"; status=1 ;;
    *) echo "<testcase classname=\"pkg.Suite\" name=\"$t\"/>" >> "$out" ;;
  esac
done
echo '</testsuite>' >> "$out"
exit $status
```

`testdata/fixture-repo/.gitignore`:

```
build/
node_modules/
```

`testdata/fixture-repo/README.md`:

```markdown
Fixture repository used by Fugaro's tests.
```

`internal/testutil/fixture.go`:

```go
package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// FixtureFiles returns testdata/fixture-repo as a path → content map.
func FixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join(ModuleRoot(), "testdata", "fixture-repo")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
```

- [ ] **Step 2: Write the failing tests** — `internal/verify/verify_test.go`

```go
package verify

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type fixture struct {
	stateDir, repoDir, failsFile string
}

func setup(t *testing.T) fixture {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, testutil.FixtureFiles(t))
	parent := t.TempDir()
	repoDir := filepath.Join(parent, "repo")
	testutil.Git(t, parent, "clone", "--quiet", remote, repoDir)
	f := fixture{stateDir: t.TempDir(), repoDir: repoDir, failsFile: filepath.Join(t.TempDir(), "fails")}
	t.Setenv("FIXTURE_FAILS_FILE", f.failsFile)
	err := WriteSettings(f.stateDir, Settings{
		RepoDir:     repoDir,
		Build:       "sh build.sh",
		Test:        "sh test.sh",
		RerunFailed: &config.RerunFailed{Command: "sh test.sh", Each: "{id}"},
		Reports:     []string{"build/test-results/*.xml"},
		TimeoutS:    60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) fails(t *testing.T, names string) {
	t.Helper()
	if err := os.WriteFile(f.failsFile, []byte(names), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, f fixture, kind Kind, rerun bool) Record {
	t.Helper()
	rec, err := Run(context.Background(), Options{StateDir: f.stateDir, Kind: kind, Rerun: rerun, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestBuildPassAndFail(t *testing.T) {
	f := setup(t)
	if rec := run(t, f, KindBuild, false); !rec.Passed || rec.N != 1 {
		t.Fatalf("passing build = %+v", rec)
	}
	t.Setenv("FIXTURE_BUILD_EXIT", "1")
	if rec := run(t, f, KindBuild, false); rec.Passed || rec.ExitCode != 1 || rec.N != 2 {
		t.Fatalf("failing build = %+v", rec)
	}
}

func TestTestParsesReports(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	rec := run(t, f, KindTest, false)
	if rec.Passed || rec.Tests != 3 || rec.Failures != 1 || !slices.Equal(rec.Failed, []string{"pkg.Suite.beta"}) {
		t.Fatalf("record = %+v", rec)
	}
	head := testutil.Git(t, f.repoDir, "rev-parse", "HEAD")
	if rec.HeadSHA != head || !rec.CleanTree {
		t.Fatalf("head=%s clean=%v, want %s true (build/ is ignored)", rec.HeadSHA, rec.CleanTree, head)
	}
}

func TestRerunMarksFlaky(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	f.fails(t, "")
	rec := run(t, f, KindTest, true)
	if !rec.Passed || !rec.Rerun || rec.Tests != 3 || len(rec.Failed) != 0 || !slices.Equal(rec.Flaky, []string{"pkg.Suite.beta"}) {
		t.Fatalf("rerun record = %+v", rec)
	}
}

func TestRerunStillFailing(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	rec := run(t, f, KindTest, true)
	if rec.Passed || len(rec.Flaky) != 0 || !slices.Equal(rec.Failed, []string{"pkg.Suite.beta"}) {
		t.Fatalf("rerun record = %+v", rec)
	}
}

func TestRerunRequiresSameCommit(t *testing.T) {
	f := setup(t)
	f.fails(t, "beta")
	run(t, f, KindTest, false)
	testutil.WriteFiles(t, f.repoDir, map[string]string{"new.txt": "x\n"})
	testutil.Git(t, f.repoDir, "add", "-A")
	testutil.Git(t, f.repoDir, "commit", "--quiet", "-m", "change")
	_, err := Run(context.Background(), Options{StateDir: f.stateDir, Kind: KindTest, Rerun: true, Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "current commit") {
		t.Fatalf("err = %v", err)
	}
}

func TestTestIgnoresStaleReports(t *testing.T) {
	f := setup(t)
	stale := filepath.Join(f.repoDir, "build", "test-results", "TEST-old.xml")
	testutil.WriteFiles(t, f.repoDir, map[string]string{
		"build/test-results/TEST-old.xml": `<testsuite><testcase classname="old" name="broken"><failure/></testcase></testsuite>`,
	})
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if rec := run(t, f, KindTest, false); !rec.Passed || rec.Tests != 3 {
		t.Fatalf("stale report was counted: %+v", rec)
	}
}

func TestDirtyTreeRecorded(t *testing.T) {
	f := setup(t)
	testutil.WriteFiles(t, f.repoDir, map[string]string{"untracked.txt": "x\n"})
	rec := run(t, f, KindTest, false)
	if rec.CleanTree {
		t.Fatal("CleanTree = true with an untracked file")
	}
	if !strings.Contains(rec.Summary(), "not clean") {
		t.Fatalf("summary does not warn about the dirty tree: %s", rec.Summary())
	}
}

func TestRecordsInOrder(t *testing.T) {
	f := setup(t)
	run(t, f, KindBuild, false)
	run(t, f, KindTest, false)
	recs, err := Records(f.stateDir)
	if err != nil || len(recs) != 2 || recs[0].N != 1 || recs[0].Kind != KindBuild || recs[1].Kind != KindTest {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestRerunCommandQuotes(t *testing.T) {
	got := RerunCommand(config.RerunFailed{Command: "./gradlew test", Each: "--tests {id}"}, []string{"a.B.c", "it's"})
	want := `./gradlew test --tests 'a.B.c' --tests 'it'\''s'`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
```

`internal/cli/verify_test.go`:

```go
package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

func TestVerifyNeedsStateDir(t *testing.T) {
	t.Setenv("FUGARO_STATE_DIR", "")
	_, _, err := execute(t, "verify", "build")
	if err == nil || !strings.Contains(err.Error(), "FUGARO_STATE_DIR") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyExitCodes(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "x\n"})
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	testutil.Git(t, parent, "clone", "--quiet", remote, repo)
	state := t.TempDir()
	if err := verify.WriteSettings(state, verify.Settings{RepoDir: repo, Build: "true", Test: "false"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_STATE_DIR", state)
	if _, _, err := execute(t, "verify", "build"); err != nil {
		t.Fatalf("passing build: %v", err)
	}
	_, stderr, err := execute(t, "verify", "test")
	if ExitCode(err) != ExitUserError || !strings.Contains(stderr, "FAILED") {
		t.Fatalf("failing test: exit %d stderr %q", ExitCode(err), stderr)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/verify/ ./internal/cli/`
Expected: FAIL, compile errors (`undefined: WriteSettings`, `undefined: newVerifyCmd`).

- [ ] **Step 4: Implement** — `internal/verify/verify.go`

```go
// Package verify runs a repository's build and test commands on behalf of the
// agent and records each result in the run's state directory (design §4.3).
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/procgroup"
)

const (
	settingsFile = "workflow.json"
	recordsDir   = "verify"
)

// Settings is what the runner tells `fugaro verify` about the workflow.
type Settings struct {
	RepoDir     string              `json:"repo_dir"`
	Build       string              `json:"build"`
	Test        string              `json:"test"`
	RerunFailed *config.RerunFailed `json:"rerun_failed,omitempty"`
	Reports     []string            `json:"reports"`
	TimeoutS    int                 `json:"timeout_s"`
}

// WriteSettings stores settings in stateDir and prepares the records directory.
func WriteSettings(stateDir string, s Settings) error {
	if err := os.MkdirAll(filepath.Join(stateDir, recordsDir), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, settingsFile), data, 0o644)
}

// LoadSettings reads the settings the runner wrote.
func LoadSettings(stateDir string) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(filepath.Join(stateDir, settingsFile))
	if err != nil {
		return s, fmt.Errorf("reading verify settings: %w", err)
	}
	return s, json.Unmarshal(data, &s)
}

// Kind is what a verify run executes.
type Kind string

const (
	KindBuild Kind = "build"
	KindTest  Kind = "test"
)

// Record is the result of one `fugaro verify` invocation.
type Record struct {
	N         int       `json:"n"`
	Kind      Kind      `json:"kind"`
	Rerun     bool      `json:"rerun,omitempty"`
	HeadSHA   string    `json:"head_sha"`
	CleanTree bool      `json:"clean_tree"`
	ExitCode  int       `json:"exit_code"`
	TimedOut  bool      `json:"timed_out,omitempty"`
	Passed    bool      `json:"passed"`
	Tests     int       `json:"tests"`
	Failures  int       `json:"failures"`
	Skipped   int       `json:"skipped"`
	Failed    []string  `json:"failed,omitempty"`
	Flaky     []string  `json:"flaky,omitempty"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
}

// Summary is the one-line result printed for the agent.
func (r Record) Summary() string {
	kind := string(r.Kind)
	if r.Rerun {
		kind += " --rerun-failed"
	}
	status := "passed"
	if !r.Passed {
		status = "FAILED"
	}
	s := fmt.Sprintf("fugaro verify %s #%d: %s (exit %d", kind, r.N, status, r.ExitCode)
	if r.Kind == KindTest {
		s += fmt.Sprintf(", %d tests, %d failures", r.Tests, r.Failures)
	}
	if len(r.Failed) > 0 {
		s += ": " + strings.Join(r.Failed, ", ")
	}
	if len(r.Flaky) > 0 {
		s += "; flaky: " + strings.Join(r.Flaky, ", ")
	}
	if r.TimedOut {
		s += "; timed out"
	}
	if !r.CleanTree {
		s += "; working tree not clean, so this run cannot mark the PR ready: commit first"
	}
	return s + ")"
}

// Options configure one verify run.
type Options struct {
	StateDir string
	Kind     Kind
	Rerun    bool
	Env      []string // nil means the current process environment
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
}

// Run executes the configured command, parses fresh reports and records the result.
func Run(ctx context.Context, o Options) (Record, error) {
	s, err := LoadSettings(o.StateDir)
	if err != nil {
		return Record{}, err
	}
	repo, err := gitops.Open(s.RepoDir, nil)
	if err != nil {
		return Record{}, err
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	sha, err := repo.HeadSHA(ctx)
	if err != nil {
		return Record{}, err
	}
	clean, err := repo.IsClean(ctx)
	if err != nil {
		return Record{}, err
	}

	var command string
	var prev *Record
	switch o.Kind {
	case KindBuild:
		if o.Rerun {
			return Record{}, errors.New("--rerun-failed only applies to test")
		}
		command = s.Build
	case KindTest:
		command = s.Test
		if o.Rerun {
			if prev, command, err = rerunCommand(o.StateDir, s, sha, clean); err != nil {
				return Record{}, err
			}
		}
	default:
		return Record{}, fmt.Errorf("unknown verify kind %q (want build or test)", o.Kind)
	}

	start := now()
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if s.TimeoutS > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(s.TimeoutS)*time.Second)
	}
	defer cancel()
	code, runErr := procgroup.Run(runCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: s.RepoDir, Env: env,
		Stdout: o.Stdout, Stderr: o.Stderr,
	})
	timedOut := errors.Is(runErr, context.DeadlineExceeded)
	if runErr != nil && !timedOut {
		return Record{}, runErr
	}

	rec := Record{
		Kind: o.Kind, Rerun: o.Rerun, HeadSHA: sha, CleanTree: clean, ExitCode: code,
		TimedOut: timedOut, StartedAt: start.UTC(), DurationS: now().Sub(start).Seconds(),
	}
	if o.Kind == KindTest {
		cases, err := ReadCases(s.RepoDir, s.Reports, start)
		if err != nil {
			return Record{}, err
		}
		if prev == nil {
			rec.Tests, rec.Skipped, rec.Failed = summarize(cases)
		} else {
			rec.Tests, rec.Skipped = prev.Tests, prev.Skipped
			rec.Flaky, rec.Failed = compareRerun(prev.Failed, cases, code)
		}
		rec.Failures = len(rec.Failed)
	}
	rec.Passed = code == 0 && !timedOut && rec.Failures == 0
	if err := writeRecord(o.StateDir, &rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func rerunCommand(stateDir string, s Settings, sha string, clean bool) (*Record, string, error) {
	if s.RerunFailed == nil {
		return nil, "", errors.New("this repository does not configure commands.rerun_failed")
	}
	prev, err := lastTest(stateDir)
	if err != nil {
		return nil, "", err
	}
	if prev == nil {
		return nil, "", errors.New("no previous `fugaro verify test` run to rerun")
	}
	if len(prev.Failed) == 0 {
		return nil, "", errors.New("the previous test run has no failed tests to rerun")
	}
	if prev.HeadSHA != sha || !prev.CleanTree || !clean {
		return nil, "", errors.New("--rerun-failed needs the previous test run to be on the current commit with a clean working tree; run `fugaro verify test` first")
	}
	return prev, RerunCommand(*s.RerunFailed, prev.Failed), nil
}

// RerunCommand builds rf.Command followed by rf.Each for every test ID.
func RerunCommand(rf config.RerunFailed, ids []string) string {
	parts := []string{rf.Command}
	for _, id := range ids {
		parts = append(parts, strings.ReplaceAll(rf.Each, "{id}", shellQuote(id)))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func summarize(cases []TestCase) (tests, skipped int, failed []string) {
	for _, c := range cases {
		tests++
		if c.Skipped {
			skipped++
		}
		if c.Failed {
			failed = append(failed, c.ID)
		}
	}
	return tests, skipped, failed
}

// compareRerun splits the previously failed tests into those that now pass
// (flaky) and those that still fail or did not run.
func compareRerun(prevFailed []string, cases []TestCase, exitCode int) (flaky, failed []string) {
	if len(cases) == 0 {
		if exitCode == 0 {
			return slices.Clone(prevFailed), nil
		}
		return nil, slices.Clone(prevFailed)
	}
	ran, failedNow := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		ran[c.ID] = true
		if c.Failed {
			failedNow[c.ID] = true
		}
	}
	for _, id := range prevFailed {
		if ran[id] && !failedNow[id] {
			flaky = append(flaky, id)
		} else {
			failed = append(failed, id)
		}
	}
	for _, c := range cases {
		if c.Failed && !slices.Contains(prevFailed, c.ID) {
			failed = append(failed, c.ID)
		}
	}
	return flaky, failed
}

func writeRecord(stateDir string, rec *Record) error {
	dir := filepath.Join(stateDir, recordsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for n := len(entries) + 1; ; n++ {
		rec.N = n
		data, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%04d.json", n)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue // a concurrent verify took this number
		}
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
}

// Records returns every verify record in stateDir, oldest first.
func Records(stateDir string) ([]Record, error) {
	dir := filepath.Join(stateDir, recordsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	recs := make([]Record, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		recs = append(recs, r)
	}
	return recs, nil
}

func lastTest(stateDir string) (*Record, error) {
	recs, err := Records(stateDir)
	if err != nil {
		return nil, err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == KindTest {
			return &recs[i], nil
		}
	}
	return nil, nil
}
```

`internal/cli/verify.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/verify"
)

func newVerifyCmd() *cobra.Command {
	var rerun bool
	cmd := &cobra.Command{
		Use:       "verify build|test",
		Short:     "Run and record this repository's build or tests (inside a Fugaro run)",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{string(verify.KindBuild), string(verify.KindTest)},
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir := os.Getenv("FUGARO_STATE_DIR")
			if stateDir == "" {
				return errors.New("FUGARO_STATE_DIR is not set: `fugaro verify` only works inside a Fugaro run")
			}
			rec, err := verify.Run(cmd.Context(), verify.Options{
				StateDir: stateDir, Kind: verify.Kind(args[0]), Rerun: rerun,
				Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), rec.Summary())
			if !rec.Passed {
				return &ExitError{Code: ExitUserError, Err: errors.New("verification failed")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&rerun, "rerun-failed", false, "rerun only the tests that failed in the previous test run, to detect flaky tests")
	return cmd
}
```

In `internal/cli/root.go`:

```go
	root.AddCommand(newVersionCmd(), newValidateCmd(), newConfigCmd(), newVerifyCmd())
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/verify/ ./internal/cli/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add testdata/fixture-repo internal/testutil internal/verify internal/cli
git commit -m "feat(verify): record build and test runs, with flaky-test reruns"
```

---

### Task 9: Run storage (`runstore`)

**Files:**
- Create: `internal/runstore/runstore.go`, `internal/runstore/runstore_test.go`

**Interfaces:**
- Consumes: `task.Spec`, `task.Parse`, `verify.Record`
- Produces:
  - `runstore.Status`: `StatusRunning`, `StatusSucceeded`, `StatusFailed`, `StatusInfraError`, `StatusCancelled`
  - `runstore.Outcome`: `OutcomeReady`, `OutcomeDraft`, `OutcomeNone`
  - `runstore.PRRef{Number int; URL string}`
  - `runstore.ReviewSummary{Round int; Verdict string; Findings int}`
  - `runstore.StageTiming{Name string; StartedAt time.Time; DurationS float64}`
  - `runstore.Record`, with fields as in design §4.6
  - `runstore.ErrNotFound`
  - `runstore.Open(b *blob.Bucket, repoSlug, runID string) *Store`
  - `runstore.ParseRef(ref string) (slug, runID string, err error)`
  - `(*Store).Prefix() string`
  - `(*Store).WriteTask` / `ReadTask`
  - `(*Store).WriteRecord` / `ReadRecord`
  - `(*Store).PutFile(ctx, name string, data []byte, contentType string) error`
  - `(*Store).RequestCancel(ctx) error` and `(*Store).CancelRequested(ctx) (bool, error)`

- [ ] **Step 1: Add the dependency**

```bash
go get gocloud.dev@latest
```

- [ ] **Step 2: Write the failing tests** — `internal/runstore/runstore_test.go`

```go
package runstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

var ctx = context.Background()

const runID = "20260926-221530-abcd"

func newStore(t *testing.T) *Store {
	t.Helper()
	b := memblob.OpenBucket(nil)
	t.Cleanup(func() { b.Close() })
	return Open(b, "acme-app", runID)
}

func TestTaskRoundTrip(t *testing.T) {
	s := newStore(t)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "do it"}
	if err := s.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadTask(ctx)
	if err != nil || got.Task != "do it" || got.Repo != "acme/app" {
		t.Fatalf("ReadTask = %+v, %v", got, err)
	}
}

func TestRecordRoundTripAndNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.ReadRecord(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadRecord on an empty store err = %v, want ErrNotFound", err)
	}
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, Passed: true}}, StartedAt: time.Now().UTC()}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.Status != StatusSucceeded || len(got.Verify) != 1 {
		t.Fatalf("ReadRecord = %+v, %v", got, err)
	}
}

func TestCancelMarker(t *testing.T) {
	s := newStore(t)
	if ok, err := s.CancelRequested(ctx); err != nil || ok {
		t.Fatalf("CancelRequested before = %v, %v", ok, err)
	}
	if err := s.RequestCancel(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CancelRequested(ctx); err != nil || !ok {
		t.Fatalf("CancelRequested after = %v, %v", ok, err)
	}
}

func TestPutFileUnderPrefix(t *testing.T) {
	b := memblob.OpenBucket(nil)
	defer b.Close()
	s := Open(b, "acme-app", runID)
	if err := s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte("{}\n"), "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
	key := "runs/acme-app/" + runID + "/transcripts/implement-1.jsonl"
	if ok, _ := b.Exists(ctx, key); !ok || s.Prefix() != "runs/acme-app/"+runID+"/" {
		t.Fatalf("object %s missing or prefix %q wrong", key, s.Prefix())
	}
}

func TestParseRef(t *testing.T) {
	if slug, id, err := ParseRef("acme-app/" + runID); err != nil || slug != "acme-app" || id != runID {
		t.Fatalf("ParseRef = %q %q %v", slug, id, err)
	}
	for _, bad := range []string{"", "acme-app", "a/b/c", "acme-app/not-a-run"} {
		if _, _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) succeeded", bad)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/runstore/`
Expected: FAIL, a compile error (`undefined: Open`).

- [ ] **Step 4: Implement** — `internal/runstore/runstore.go`

```go
// Package runstore reads and writes a run's objects in the runs bucket (design §3.3).
package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Status is the lifecycle state of a run.
type Status string

const (
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded" // a ready PR exists
	StatusFailed     Status = "failed"    // a draft PR exists
	StatusInfraError Status = "infra_error"
	StatusCancelled  Status = "cancelled"
)

// Outcome is the state of the run's pull request.
type Outcome string

const (
	OutcomeReady Outcome = "ready"
	OutcomeDraft Outcome = "draft"
	OutcomeNone  Outcome = "none"
)

// PRRef identifies the run's pull request.
type PRRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// ReviewSummary is one review round's verdict.
type ReviewSummary struct {
	Round    int    `json:"round"`
	Verdict  string `json:"verdict"`
	Findings int    `json:"findings"`
}

// StageTiming records how long one stage took.
type StageTiming struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
}

// Record is result.json, the run record (design §4.6).
type Record struct {
	Version    int             `json:"version"`
	RunID      string          `json:"run_id"`
	Repo       string          `json:"repo"`
	Workflow   string          `json:"workflow,omitempty"`
	Status     Status          `json:"status"`
	Stage      string          `json:"stage"`
	Outcome    Outcome         `json:"outcome"`
	Reason     string          `json:"reason,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	HeadSHA    string          `json:"head_sha,omitempty"`
	PR         *PRRef          `json:"pr,omitempty"`
	Reviews    []ReviewSummary `json:"reviews,omitempty"`
	Verify     []verify.Record `json:"verify,omitempty"`
	CostUSD    float64         `json:"cost_usd"`
	Stages     []StageTiming   `json:"stages,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// ErrNotFound means the requested object does not exist.
var ErrNotFound = errors.New("not found")

// Store addresses one run's objects: runs/<repo-slug>/<run-id>/...
type Store struct {
	bucket *blob.Bucket
	prefix string
}

// Open returns the store for one run.
func Open(b *blob.Bucket, repoSlug, runID string) *Store {
	return &Store{bucket: b, prefix: path.Join("runs", repoSlug, runID) + "/"}
}

var runRefRE = regexp.MustCompile(`^([a-z0-9._-]+)/([0-9]{8}-[0-9]{6}-[0-9a-f]{4})$`)

// ParseRef splits "<repo-slug>/<run-id>".
func ParseRef(ref string) (slug, runID string, err error) {
	m := runRefRE.FindStringSubmatch(ref)
	if m == nil {
		return "", "", fmt.Errorf("run %q must look like <repo-slug>/<run-id>", ref)
	}
	return m[1], m[2], nil
}

// Prefix is the object-name prefix of this run, ending in "/".
func (s *Store) Prefix() string { return s.prefix }

// PutFile writes a file under the run's prefix.
func (s *Store) PutFile(ctx context.Context, name string, data []byte, contentType string) error {
	key := s.prefix + strings.TrimPrefix(name, "/")
	if err := s.bucket.WriteAll(ctx, key, data, &blob.WriterOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

func (s *Store) read(ctx context.Context, name string) ([]byte, error) {
	data, err := s.bucket.ReadAll(ctx, s.prefix+name)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, fmt.Errorf("%s%s: %w", s.prefix, name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s%s: %w", s.prefix, name, err)
	}
	return data, nil
}

// WriteTask stores task.json.
func (s *Store) WriteTask(ctx context.Context, spec *task.Spec) error {
	data, err := spec.Marshal()
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "task.json", data, "application/json")
}

// ReadTask loads and validates task.json.
func (s *Store) ReadTask(ctx context.Context) (*task.Spec, error) {
	data, err := s.read(ctx, "task.json")
	if err != nil {
		return nil, err
	}
	return task.Parse(data)
}

// WriteRecord stores result.json.
func (s *Store) WriteRecord(ctx context.Context, r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return s.PutFile(ctx, "result.json", data, "application/json")
}

// ReadRecord loads result.json.
func (s *Store) ReadRecord(ctx context.Context) (*Record, error) {
	data, err := s.read(ctx, "result.json")
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decoding result.json: %w", err)
	}
	return &r, nil
}

// RequestCancel asks the running runner to stop and finalize.
func (s *Store) RequestCancel(ctx context.Context) error {
	return s.PutFile(ctx, "cancel", []byte(time.Now().UTC().Format(time.RFC3339)), "text/plain")
}

// CancelRequested reports whether a cancel marker exists.
func (s *Store) CancelRequested(ctx context.Context) (bool, error) {
	return s.bucket.Exists(ctx, s.prefix+"cancel")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/runstore/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/runstore
git commit -m "feat(runstore): task spec, run record and cancel marker storage"
```

---

### Task 10: Claude Code adapter (`agent`) and the fake `claude`

**Files:**
- Create: `internal/agent/agent.go`, `internal/agent/stream.go`, `internal/agent/env.go`, `internal/agent/redact.go`, `internal/agent/agent_test.go`, `internal/agent/testdata/stream-success.jsonl`, `internal/agent/fakeclaude/main.go`, `internal/testutil/build.go`

**Interfaces:**
- Consumes: `procgroup.Run`
- Produces:
  - Invocation:
    - `agent.Request{Prompt, SessionID string; Resume bool; AppendSystemPrompt, Model string; MaxBudgetUSD float64; JSONSchema, Dir string; Env []string; Transcript, Stderr io.Writer}`
    - `agent.Result{SessionID, Text string; Structured json.RawMessage; CostUSD float64; IsError bool; Subtype string; ExitCode int}`
    - `agent.Agent` interface: `Run(ctx, Request) (Result, error)`
    - `agent.Claude{Bin string; Grace time.Duration}`, which implements `Agent`
    - `agent.Args(Request) []string`
    - `agent.ParseStream(r io.Reader, transcript io.Writer) (Result, bool, error)`
    - `agent.NewSessionID() string`
  - Environment and redaction:
    - `agent.EnvSpec{Auth string; Secrets []string; Set map[string]string; PathPrepend string}`
    - `agent.BuildEnv(parent []string, spec EnvSpec) (env, secretValues []string, err error)`
    - `agent.Redactor`, with `NewRedactor(w io.Writer, secrets []string) *Redactor`, `Write`, and `Flush() error`
  - Test helpers:
    - `testutil.BuildFugaro(t) string`
    - `testutil.FakeClaude(t, script string) string`
    - `testutil.FakeCall{Args []string; Prompt string; Env []string; Dir string}`
    - `testutil.FakeClaudeCalls(t, bin string) []FakeCall`
- **Fake script format** (`script.json`, next to the fake binary): `{"calls":[{"shell":"...","text":"...","structured":{...},"cost":0.5,"is_error":false,"subtype":"","exit":0,"sleep_s":0,"no_result":false}]}`. Call *n* of the fake uses entry *n*. `text` goes through `$VAR` expansion. Shell output goes to stderr. Every invocation is appended to `calls.jsonl`, next to the binary.

- [ ] **Step 1: Write the stream fixture** — `internal/agent/testdata/stream-success.jsonl`. It follows the event shape captured from Claude Code 2.1.283, trimmed:

```
{"type":"system","subtype":"init","session_id":"3f2a9c1e-0000-4000-8000-000000000001","cwd":"/work/repo"}
{"type":"assistant","session_id":"3f2a9c1e-0000-4000-8000-000000000001","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}
{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.049912,"session_id":"3f2a9c1e-0000-4000-8000-000000000001","result":"{\"verdict\":\"ship\"}","structured_output":{"verdict":"ship"},"num_turns":2}
```

- [ ] **Step 2: Write the fake claude** — `internal/agent/fakeclaude/main.go`

```go
// Command fakeclaude stands in for the claude CLI in tests. It replays the
// calls in script.json (next to the binary), one per invocation, and logs each
// invocation to calls.jsonl.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type call struct {
	Shell      string          `json:"shell"`
	Text       string          `json:"text"`
	Structured json.RawMessage `json:"structured,omitempty"`
	Cost       float64         `json:"cost"`
	IsError    bool            `json:"is_error"`
	Subtype    string          `json:"subtype"`
	Exit       int             `json:"exit"`
	SleepS     float64         `json:"sleep_s"`
	NoResult   bool            `json:"no_result"`
}

type invocation struct {
	Args   []string `json:"args"`
	Prompt string   `json:"prompt"`
	Env    []string `json:"env"`
	Dir    string   `json:"dir"`
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail("%v", err)
	}
	dir := filepath.Dir(exe)
	var script struct {
		Calls []call `json:"calls"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "script.json"))
	if err != nil {
		fail("reading script: %v", err)
	}
	if err := json.Unmarshal(data, &script); err != nil {
		fail("parsing script: %v", err)
	}
	prompt, _ := io.ReadAll(os.Stdin)
	wd, _ := os.Getwd()
	callsPath := filepath.Join(dir, "calls.jsonl")
	n := countLines(callsPath)
	appendLine(callsPath, invocation{Args: os.Args[1:], Prompt: string(prompt), Env: os.Environ(), Dir: wd})
	if n >= len(script.Calls) {
		fail("unexpected call #%d (script has %d)", n+1, len(script.Calls))
	}
	c := script.Calls[n]
	sid := sessionID(os.Args[1:])
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	if c.Shell != "" {
		cmd := exec.Command("sh", "-c", c.Shell)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: shell: %v\n", err)
		}
	}
	if c.SleepS > 0 {
		time.Sleep(time.Duration(c.SleepS * float64(time.Second)))
	}
	if !c.NoResult {
		subtype := c.Subtype
		if subtype == "" {
			subtype = "success"
		}
		var structured any
		if len(c.Structured) > 0 {
			structured = c.Structured
		}
		emit(map[string]any{
			"type": "result", "subtype": subtype, "is_error": c.IsError, "total_cost_usd": c.Cost,
			"session_id": sid, "result": os.ExpandEnv(c.Text), "structured_output": structured,
		})
	}
	os.Exit(c.Exit)
}

func sessionID(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session-id" || args[i] == "--resume" {
			return args[i+1]
		}
	}
	return ""
}

func emit(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fail("%v", err)
	}
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		n++
	}
	return n
}

func appendLine(path string, v any) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		fail("%v", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(v); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fakeclaude: "+format+"\n", args...)
	os.Exit(3)
}
```

`internal/testutil/build.go`:

```go
package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

func buildBinaries() {
	buildDir, buildErr = os.MkdirTemp("", "fugaro-testbin-")
	if buildErr != nil {
		return
	}
	for name, pkg := range map[string]string{
		"fugaro": "github.com/dimipaun/fugaro/cmd/fugaro",
		"claude": "github.com/dimipaun/fugaro/internal/agent/fakeclaude",
	} {
		out, err := exec.Command("go", "build", "-o", filepath.Join(buildDir, name), pkg).CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
			return
		}
	}
}

// installBinary copies a built binary into a fresh directory of its own.
func installBinary(t *testing.T, name string) string {
	t.Helper()
	buildOnce.Do(buildBinaries)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	data, err := os.ReadFile(filepath.Join(buildDir, name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

// BuildFugaro returns the path of a fugaro binary alone in its directory.
func BuildFugaro(t *testing.T) string { return installBinary(t, "fugaro") }

// FakeClaude returns the path of a fake claude binary that replays script
// (see internal/agent/fakeclaude).
func FakeClaude(t *testing.T, script string) string {
	t.Helper()
	bin := installBinary(t, "claude")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "script.json"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}

// FakeCall is one recorded invocation of the fake claude.
type FakeCall struct {
	Args   []string `json:"args"`
	Prompt string   `json:"prompt"`
	Env    []string `json:"env"`
	Dir    string   `json:"dir"`
}

// FakeClaudeCalls returns the invocations the fake at bin has recorded.
func FakeClaudeCalls(t *testing.T, bin string) []FakeCall {
	t.Helper()
	f, err := os.Open(filepath.Join(filepath.Dir(bin), "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var calls []FakeCall
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		var c FakeCall
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}
```

- [ ] **Step 3: Write the failing tests** — `internal/agent/agent_test.go`

```go
package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestArgs(t *testing.T) {
	got := Args(Request{SessionID: "s1", AppendSystemPrompt: "rules", Model: "m", MaxBudgetUSD: 12.5, JSONSchema: `{}`})
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions",
		"--session-id", "s1", "--append-system-prompt", "rules", "--model", "m", "--max-budget-usd", "12.50", "--json-schema", "{}"}
	if !slices.Equal(got, want) {
		t.Fatalf("Args = %q\nwant   %q", got, want)
	}
	resumed := Args(Request{SessionID: "s1", Resume: true})
	if !slices.Contains(resumed, "--resume") || slices.Contains(resumed, "--session-id") {
		t.Fatalf("resume args = %q", resumed)
	}
}

func TestParseStream(t *testing.T) {
	f, err := os.Open("testdata/stream-success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var transcript bytes.Buffer
	res, found, err := ParseStream(f, &transcript)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if res.SessionID != "3f2a9c1e-0000-4000-8000-000000000001" || res.CostUSD != 0.049912 || res.IsError || res.Subtype != "success" {
		t.Fatalf("result = %+v", res)
	}
	if string(res.Structured) != `{"verdict":"ship"}` {
		t.Fatalf("structured = %s", res.Structured)
	}
	if strings.Count(transcript.String(), `"session_id"`) != 3 {
		t.Fatal("transcript did not receive every line")
	}
}

func TestParseStreamNoResult(t *testing.T) {
	_, found, err := ParseStream(strings.NewReader(`{"type":"system"}`+"\n"), nil)
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestClaudeRun(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"text":"hello","cost":0.5}]}`)
	var transcript bytes.Buffer
	res, err := Claude{Bin: bin}.Run(context.Background(), Request{
		Prompt: "do the thing", SessionID: "s-1", Dir: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")}, Transcript: &transcript,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello" || res.CostUSD != 0.5 || res.SessionID != "s-1" || res.ExitCode != 0 {
		t.Fatalf("result = %+v", res)
	}
	calls := testutil.FakeClaudeCalls(t, bin)
	if len(calls) != 1 || calls[0].Prompt != "do the thing" || !slices.Contains(calls[0].Args, "--session-id") {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestClaudeRunTimeout(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"sleep_s":30}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Claude{Bin: bin, Grace: 100 * time.Millisecond}.Run(ctx, Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

func TestClaudeRunNoResult(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"no_result":true,"exit":1}]}`)
	_, err := Claude{Bin: bin}.Run(context.Background(), Request{SessionID: "s", Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err == nil || !strings.Contains(err.Error(), "without a result event") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildEnvScrubs(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=key-123", "NPM_TOKEN=npm-456", "AWS_SECRET_ACCESS_KEY=nope", "GOOGLE_APPLICATION_CREDENTIALS=/sa.json"}
	env, secrets, err := BuildEnv(parent, EnvSpec{Auth: "api-key", Secrets: []string{"NPM_TOKEN"}, Set: map[string]string{"FUGARO_STATE_DIR": "/state"}, PathPrepend: "/fugaro/bin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PATH=/fugaro/bin:/bin", "HOME=/h", "ANTHROPIC_API_KEY=key-123", "NPM_TOKEN=npm-456", "FUGARO_STATE_DIR=/state"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %v", want, env)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "AWS_") || strings.HasPrefix(kv, "GOOGLE_APPLICATION_CREDENTIALS") {
			t.Errorf("undeclared variable leaked: %s", kv)
		}
	}
	if !slices.Equal(secrets, []string{"key-123", "npm-456"}) {
		t.Fatalf("secrets = %v", secrets)
	}
}

func TestBuildEnvAuthModes(t *testing.T) {
	env, _, err := BuildEnv([]string{"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=p"}, EnvSpec{Auth: "vertex"})
	if err != nil || !slices.Contains(env, "CLAUDE_CODE_USE_VERTEX=1") || !slices.Contains(env, "CLOUD_ML_REGION=us-east5") {
		t.Fatalf("vertex env = %v, %v", env, err)
	}
	if _, _, err := BuildEnv(nil, EnvSpec{Auth: "vertex"}); err == nil {
		t.Fatal("vertex without region/project should fail")
	}
	if _, _, err := BuildEnv(nil, EnvSpec{Auth: "oauth"}); err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("oauth without token err = %v", err)
	}
	if _, _, err := BuildEnv([]string{"ANTHROPIC_API_KEY=k"}, EnvSpec{Auth: "api-key", Secrets: []string{"MISSING"}}); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("missing secret err = %v", err)
	}
}

func TestRedactor(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(&out, []string{"s3cret-value", "ab"}) // secrets shorter than 4 bytes are ignored
	for _, chunk := range []string{"token=s3cr", "et-value ok\nab ", "tail s3cret-value"} {
		if _, err := r.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "token=[REDACTED] ok\nab tail [REDACTED]"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestNewSessionID(t *testing.T) {
	id := NewSessionID()
	if len(id) != 36 || id[14] != '4' || id == NewSessionID() {
		t.Fatalf("bad UUIDv4 %q", id)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/agent/`
Expected: FAIL, a compile error (`undefined: Args`).

- [ ] **Step 5: Implement**

`internal/agent/agent.go`:

```go
// Package agent runs Claude Code headless (`claude -p`) for one stage.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/procgroup"
)

// Request is one agent invocation.
type Request struct {
	Prompt             string
	SessionID          string // new session ID, or the one to resume
	Resume             bool
	AppendSystemPrompt string
	Model              string
	MaxBudgetUSD       float64
	JSONSchema         string // optional structured-output schema
	Dir                string
	Env                []string
	Transcript         io.Writer // receives the raw stream-json lines
	Stderr             io.Writer
}

// Result is what the agent's final result event reported.
type Result struct {
	SessionID  string
	Text       string
	Structured json.RawMessage
	CostUSD    float64
	IsError    bool
	Subtype    string
	ExitCode   int
}

// Agent runs one stage of agent work.
type Agent interface {
	Run(ctx context.Context, req Request) (Result, error)
}

// Claude runs the claude CLI.
type Claude struct {
	Bin   string        // default "claude"
	Grace time.Duration // SIGTERM → SIGKILL grace; zero means procgroup's default
}

// Args returns the claude command-line arguments for req. The prompt goes on stdin.
func Args(req Request) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if req.Resume {
		args = append(args, "--resume", req.SessionID)
	} else {
		args = append(args, "--session-id", req.SessionID)
	}
	if req.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.AppendSystemPrompt)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(req.MaxBudgetUSD, 'f', 2, 64))
	}
	if req.JSONSchema != "" {
		args = append(args, "--json-schema", req.JSONSchema)
	}
	return args
}

// Run invokes claude and returns its result event.
func (c Claude) Run(ctx context.Context, req Request) (Result, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	pr, pw := io.Pipe()
	type parsed struct {
		res   Result
		found bool
		err   error
	}
	done := make(chan parsed, 1)
	go func() {
		res, found, err := ParseStream(pr, req.Transcript)
		_, _ = io.Copy(io.Discard, pr) // keep draining if parsing stopped early
		done <- parsed{res, found, err}
	}()
	code, runErr := procgroup.Run(ctx, procgroup.Cmd{
		Name: bin, Args: Args(req), Dir: req.Dir, Env: req.Env,
		Stdin: strings.NewReader(req.Prompt), Stdout: pw, Stderr: req.Stderr, Grace: c.Grace,
	})
	pw.Close()
	p := <-done
	p.res.ExitCode = code
	switch {
	case runErr != nil:
		return p.res, runErr
	case p.err != nil:
		return p.res, fmt.Errorf("reading claude output: %w", p.err)
	case !p.found:
		return p.res, fmt.Errorf("claude exited with code %d without a result event", code)
	}
	return p.res, nil
}

// NewSessionID returns a random UUIDv4 for --session-id.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
```

`internal/agent/stream.go`:

```go
package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

type resultEvent struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	SessionID        string          `json:"session_id"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

// ParseStream copies claude's stream-json output to transcript (if not nil)
// and returns the last result event. found is false if there was none.
func ParseStream(r io.Reader, transcript io.Writer) (res Result, found bool, err error) {
	br := bufio.NewReader(r)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			if transcript != nil {
				if _, err := transcript.Write(line); err != nil {
					return res, found, err
				}
			}
			var ev resultEvent
			if json.Unmarshal(bytes.TrimSpace(line), &ev) == nil && ev.Type == "result" {
				structured := ev.StructuredOutput
				if string(structured) == "null" {
					structured = nil
				}
				res = Result{SessionID: ev.SessionID, Text: ev.Result, Structured: structured,
					CostUSD: ev.TotalCostUSD, IsError: ev.IsError, Subtype: ev.Subtype}
				found = true
			}
		}
		if readErr == io.EOF {
			return res, found, nil
		}
		if readErr != nil {
			return res, found, readErr
		}
	}
}
```

`internal/agent/env.go`:

```go
package agent

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// baseEnv is what the agent inherits from the runner besides auth and declared secrets.
var baseEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "TERM", "TMPDIR", "TZ",
	"JAVA_HOME", "GRADLE_USER_HOME", "NODE_OPTIONS", "npm_config_cache", "PNPM_HOME",
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
}

// EnvSpec describes the agent's environment.
type EnvSpec struct {
	Auth        string            // vertex | api-key | oauth
	Secrets     []string          // declared workflow secret variables, passed through
	Set         map[string]string // variables Fugaro sets, such as FUGARO_STATE_DIR
	PathPrepend string            // directory put first on PATH
}

// BuildEnv builds the agent's environment from the runner's (parent), keeping
// only allowlisted variables, the auth variables and declared secrets. It
// returns the secret values so transcripts can be redacted.
func BuildEnv(parent []string, spec EnvSpec) (env, secretValues []string, err error) {
	p := map[string]string{}
	for _, kv := range parent {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p[k] = v
		}
	}
	out := map[string]string{}
	for _, k := range baseEnv {
		if v, ok := p[k]; ok {
			out[k] = v
		}
	}
	secretNames := slices.Clone(spec.Secrets)
	switch spec.Auth {
	case "vertex":
		out["CLAUDE_CODE_USE_VERTEX"] = "1"
		for _, k := range []string{"CLOUD_ML_REGION", "ANTHROPIC_VERTEX_PROJECT_ID", "GOOGLE_CLOUD_PROJECT"} {
			if v, ok := p[k]; ok {
				out[k] = v
			}
		}
		if out["CLOUD_ML_REGION"] == "" || out["ANTHROPIC_VERTEX_PROJECT_ID"] == "" {
			return nil, nil, fmt.Errorf("vertex auth needs CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID in the runner environment")
		}
	case "api-key":
		secretNames = append([]string{"ANTHROPIC_API_KEY"}, secretNames...)
	case "oauth":
		secretNames = append([]string{"CLAUDE_CODE_OAUTH_TOKEN"}, secretNames...)
	default:
		return nil, nil, fmt.Errorf("unknown agent auth %q", spec.Auth)
	}
	for _, k := range secretNames {
		v := p[k]
		if v == "" {
			return nil, nil, fmt.Errorf("secret %s is not set in the runner environment", k)
		}
		out[k] = v
		secretValues = append(secretValues, v)
	}
	maps.Copy(out, spec.Set)
	if spec.PathPrepend != "" {
		out["PATH"] = spec.PathPrepend + string(os.PathListSeparator) + out["PATH"]
	}
	for _, k := range slices.Sorted(maps.Keys(out)) {
		env = append(env, k+"="+out[k])
	}
	return env, secretValues, nil
}
```

`internal/agent/redact.go`:

```go
package agent

import (
	"bytes"
	"io"
	"sort"
	"strings"
)

// Redactor replaces secret values with [REDACTED], line by line, before
// writing to w. Call Flush after the last Write.
type Redactor struct {
	w       io.Writer
	secrets []string
	buf     []byte
}

// NewRedactor returns a Redactor for secrets; values shorter than 4 bytes are
// ignored because redacting them would mangle ordinary text.
func NewRedactor(w io.Writer, secrets []string) *Redactor {
	var keep []string
	for _, s := range secrets {
		if len(s) >= 4 {
			keep = append(keep, s)
		}
	}
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return &Redactor{w: w, secrets: keep}
}

func (r *Redactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := r.emit(r.buf[:i+1]); err != nil {
			return 0, err
		}
		r.buf = r.buf[i+1:]
	}
}

// Flush writes any buffered partial line.
func (r *Redactor) Flush() error {
	if len(r.buf) == 0 {
		return nil
	}
	err := r.emit(r.buf)
	r.buf = nil
	return err
}

func (r *Redactor) emit(line []byte) error {
	s := string(line)
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	_, err := io.WriteString(r.w, s)
	return err
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/agent/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent internal/testutil
git commit -m "feat(agent): Claude Code headless adapter, env scrubbing, redaction and fake claude"
```

---

### Task 11: Runner — prompts, review verdicts, outcome rule, and report

**Files:**
- Create: `internal/runner/prompts.go`, `internal/runner/verdict.go`, `internal/runner/outcome.go`, `internal/runner/report.go`, `internal/runner/pure_test.go`

**Interfaces:**
- Consumes: `agent.Result`, `verify.Record`, `runstore.Record`, `runstore.ReviewSummary`
- Produces:
  - Prompts:
    - `runner.PromptData{Branch, Base, StateDir string}`
    - `runner.SystemPrompt(d PromptData, instructions string) string`
    - `runner.ReviewPrompt(review, promptFile, base string) string`
    - `runner.FixPrompt(v Verdict) string`
  - Review verdicts:
    - `runner.VerdictSchema` (a string constant)
    - `runner.Finding{Severity, File, Summary string}` and `runner.Verdict{Verdict string; Findings []Finding}`
    - `runner.ParseVerdict(agent.Result) Verdict`
  - Outcome and report:
    - `runner.Decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary) (ready bool, reason string)`
    - `runner.Report(rec *runstore.Record, location string) string`
    - `runner.Plural(n int, noun string) string`

- [ ] **Step 1: Write the failing tests** — `internal/runner/pure_test.go`

```go
package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

func TestParseVerdict(t *testing.T) {
	structured := agent.Result{Structured: json.RawMessage(`{"verdict":"changes","findings":[{"summary":"x"}]}`)}
	if v := ParseVerdict(structured); v.Verdict != "changes" || len(v.Findings) != 1 {
		t.Errorf("structured = %+v", v)
	}
	fenced := agent.Result{Text: "Looks good.\n```json\n{\"verdict\":\"ship\",\"findings\":[]}\n```\n"}
	if v := ParseVerdict(fenced); v.Verdict != "ship" {
		t.Errorf("fenced = %+v", v)
	}
	for _, bad := range []agent.Result{{Text: "no verdict here"}, {Structured: json.RawMessage(`{"verdict":"maybe"}`)}} {
		v := ParseVerdict(bad)
		if v.Verdict != "changes" || len(v.Findings) != 1 || !strings.Contains(v.Findings[0].Summary, "no parseable verdict") {
			t.Errorf("unparseable %+v gave %+v", bad, v)
		}
	}
}

func TestDecide(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	fail := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true}
	dirty := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", Passed: true}
	old := verify.Record{Kind: verify.KindTest, HeadSHA: "old", CleanTree: true, Passed: true}
	build := verify.Record{Kind: verify.KindBuild, HeadSHA: "abc", CleanTree: true, Passed: true}
	ship := &runstore.ReviewSummary{Round: 1, Verdict: "ship"}
	changes := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 3}
	cases := []struct {
		name    string
		records []verify.Record
		last    *runstore.ReviewSummary
		ready   bool
		reason  string
	}{
		{"ready", []verify.Record{pass}, ship, true, ""},
		{"latest matching run wins", []verify.Record{pass, fail}, ship, false, "tests failing on the final commit"},
		{"fixed after failing", []verify.Record{fail, pass}, ship, true, ""},
		{"dirty tree does not count", []verify.Record{dirty}, ship, false, "no verified test run on the final commit"},
		{"old commit does not count", []verify.Record{old}, ship, false, "no verified test run on the final commit"},
		{"build is not test", []verify.Record{build}, ship, false, "no verified test run on the final commit"},
		{"review findings", []verify.Record{pass}, changes, false, "review round 2 still has 3 findings"},
		{"no review", []verify.Record{pass}, nil, false, "no review verdict"},
	}
	for _, tc := range cases {
		ready, reason := Decide(tc.records, "abc", tc.last)
		if ready != tc.ready || reason != tc.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", tc.name, ready, reason, tc.ready, tc.reason)
		}
	}
}

func TestPrompts(t *testing.T) {
	sys := SystemPrompt(PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/work/state"}, "Use tabs.")
	for _, want := range []string{"fugaro/x", "main", "/work/state/pr.md", "fugaro verify test", "--rerun-failed", "Use tabs."} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	if got := ReviewPrompt("/review", "", "main"); !strings.HasPrefix(got, "/review ") || !strings.Contains(got, "origin/main") {
		t.Errorf("skill review prompt = %q", got)
	}
	if got := ReviewPrompt("", "Check the SQL.", "develop"); !strings.HasPrefix(got, "Check the SQL.") || !strings.Contains(got, "origin/develop") {
		t.Errorf("file review prompt = %q", got)
	}
	if got := ReviewPrompt("", "", "main"); !strings.Contains(got, "correctness bugs") {
		t.Errorf("default review prompt = %q", got)
	}
	fix := FixPrompt(Verdict{Verdict: "changes", Findings: []Finding{{Severity: "major", File: "a.go", Summary: "nil deref"}, {Summary: "add a test"}}})
	if !strings.Contains(fix, "- [major] a.go: nil deref\n- add a test\n") {
		t.Errorf("fix prompt = %q", fix)
	}
}

func TestReport(t *testing.T) {
	rec := &runstore.Record{
		RunID: "20260926-221530-abcd", Outcome: runstore.OutcomeDraft, Reason: "tests failing on the final commit",
		HeadSHA: "abcdef1234567", CostUSD: 4.126,
		Stages:  []runstore.StageTiming{{Name: "implement", DurationS: 61}},
		Reviews: []runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 1}, {Round: 2, Verdict: "ship"}},
		Verify:  []verify.Record{{Kind: verify.KindTest, HeadSHA: "abcdef1234567", Tests: 3, Failures: 1, Flaky: []string{"pkg.A.b"}}},
	}
	got := Report(rec, "runs/acme-app/20260926-221530-abcd/")
	for _, want := range []string{"draft — tests failing on the final commit", "| implement | 1m1s |", "round 1: changes (1 finding)", "round 2: ship", "failed on abcdef1 (3 tests, 1 failure, flaky: pkg.A.b)", "$4.13", "runs/acme-app/20260926-221530-abcd/"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runner/`
Expected: FAIL, a compile error (`undefined: ParseVerdict`).

- [ ] **Step 3: Implement**

`internal/runner/verdict.go`:

```go
package runner

import (
	"encoding/json"
	"regexp"

	"github.com/dimipaun/fugaro/internal/agent"
)

// VerdictSchema is passed to claude --json-schema for review stages.
const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["ship","changes"]},"findings":{"type":"array","items":{"type":"object","properties":{"severity":{"type":"string"},"file":{"type":"string"},"summary":{"type":"string"}},"required":["summary"]}}},"required":["verdict","findings"]}`

// Finding is one issue a review asks to fix.
type Finding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Summary  string `json:"summary"`
}

// Verdict is a review stage's structured result.
type Verdict struct {
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings"`
}

var fencedJSONRE = regexp.MustCompile("(?s)```json\\s*\\n(.*?)\\n```")

// ParseVerdict reads the verdict from structured output, falling back to the
// last fenced JSON block. Anything unparseable counts as a blocking finding.
func ParseVerdict(res agent.Result) Verdict {
	if v, ok := decodeVerdict(res.Structured); ok {
		return v
	}
	if m := fencedJSONRE.FindAllStringSubmatch(res.Text, -1); len(m) > 0 {
		if v, ok := decodeVerdict([]byte(m[len(m)-1][1])); ok {
			return v
		}
	}
	return Verdict{Verdict: "changes", Findings: []Finding{{Severity: "blocker", Summary: "review produced no parseable verdict"}}}
}

func decodeVerdict(b []byte) (Verdict, bool) {
	var v Verdict
	if len(b) == 0 || json.Unmarshal(b, &v) != nil {
		return v, false
	}
	return v, v.Verdict == "ship" || v.Verdict == "changes"
}
```

`internal/runner/outcome.go`:

```go
package runner

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Decide applies the PR outcome rule (design §4.2): ready only if the latest
// test record on finalSHA with a clean tree passed and the last review shipped.
func Decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary) (bool, string) {
	var latest *verify.Record
	for i := range records {
		r := &records[i]
		if r.Kind == verify.KindTest && r.HeadSHA == finalSHA && r.CleanTree {
			latest = r
		}
	}
	switch {
	case latest == nil:
		return false, "no verified test run on the final commit"
	case !latest.Passed:
		return false, "tests failing on the final commit"
	case last == nil:
		return false, "no review verdict"
	case last.Verdict != "ship":
		return false, fmt.Sprintf("review round %d still has %s", last.Round, Plural(last.Findings, "finding"))
	}
	return true, ""
}

// Plural formats "1 finding" / "3 findings".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
```

`internal/runner/prompts.go`:

```go
package runner

import (
	"fmt"
	"strings"
)

// PromptData fills the run contract in the system prompt.
type PromptData struct {
	Branch   string
	Base     string
	StateDir string
}

// SystemPrompt is appended to Claude Code's system prompt for implement and fix stages.
func SystemPrompt(d PromptData, instructions string) string {
	lines := []string{
		"You are running unattended inside Fugaro, an ephemeral cloud worker. Nobody will answer questions: make reasonable decisions and explain them in the pull request description.",
		"",
		"Rules for this run:",
		fmt.Sprintf("- You are on branch %s; the pull request will target %s. Commit your work to this branch with clear messages and do not switch branches. You do not need to push or open the pull request: Fugaro does both when you finish.", d.Branch, d.Base),
		"- Build and test only through `fugaro verify build` and `fugaro verify test`. They run this repository's configured commands and record the results. If a test failure looks flaky, run `fugaro verify test --rerun-failed`: tests that pass on the rerun are recorded as flaky.",
		"- The pull request is marked ready for review only if your final commit has a passing `fugaro verify test` run with a clean working tree. Commit first, then verify.",
		fmt.Sprintf("- Write the pull request title on the first line of %s/pr.md and the description below it.", d.StateDir),
	}
	s := strings.Join(lines, "\n") + "\n"
	if strings.TrimSpace(instructions) != "" {
		s += "\nRepository instructions:\n\n" + instructions
	}
	return s
}

const defaultReview = "You are reviewing a pull request written by another engineer. Look for correctness bugs, missing or weak tests, security problems, and deviations from this repository's conventions (see CLAUDE.md if present). Ignore pure style preferences."

// ReviewPrompt builds the review stage prompt. review is the configured
// agent.review: a skill (starting with "/") or empty; promptFile is the
// content of a configured review prompt file.
func ReviewPrompt(review, promptFile, base string) string {
	scope := fmt.Sprintf("Review the changes on this branch against origin/%s (see `git diff origin/%s...HEAD`). Do not modify any files.", base, base)
	tail := "\n\nWhen you are done, give your verdict: \"ship\" if nothing must change before this is merged, otherwise \"changes\" with one finding per issue that must be fixed."
	switch {
	case strings.HasPrefix(review, "/"):
		return review + " " + scope + tail
	case promptFile != "":
		return promptFile + "\n\n" + scope + tail
	default:
		return defaultReview + "\n\n" + scope + tail
	}
}

// FixPrompt asks the implementing session to address review findings.
func FixPrompt(v Verdict) string {
	var b strings.Builder
	b.WriteString("A code review of your changes asked for these fixes:\n\n")
	for _, f := range v.Findings {
		b.WriteString("- ")
		if f.Severity != "" {
			fmt.Fprintf(&b, "[%s] ", f.Severity)
		}
		if f.File != "" {
			fmt.Fprintf(&b, "%s: ", f.File)
		}
		b.WriteString(f.Summary)
		b.WriteString("\n")
	}
	b.WriteString("\nAddress each one, commit, and run `fugaro verify test` again.")
	return b.String()
}
```

`internal/runner/report.go`:

```go
package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Report renders the run report posted to the PR and stored as report.md.
func Report(rec *runstore.Record, location string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Fugaro run `%s`\n\n", rec.RunID)
	if rec.Outcome == runstore.OutcomeReady {
		b.WriteString("**Outcome:** ready for review\n\n")
	} else {
		fmt.Fprintf(&b, "**Outcome:** draft — %s\n\n", rec.Reason)
	}
	if len(rec.Stages) > 0 {
		b.WriteString("| Stage | Duration |\n|---|---|\n")
		for _, s := range rec.Stages {
			d := time.Duration(s.DurationS * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(&b, "| %s | %s |\n", s.Name, d)
		}
		b.WriteString("\n")
	}
	if len(rec.Reviews) > 0 {
		parts := make([]string, 0, len(rec.Reviews))
		for _, r := range rec.Reviews {
			p := fmt.Sprintf("round %d: %s", r.Round, r.Verdict)
			if r.Verdict != "ship" {
				p += " (" + Plural(r.Findings, "finding") + ")"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "**Reviews:** %s\n\n", strings.Join(parts, "; "))
	}
	b.WriteString(testsLine(rec))
	fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)
	fmt.Fprintf(&b, "Transcripts and verify records: `%s` in the runs bucket.\n", location)
	return b.String()
}

func testsLine(rec *runstore.Record) string {
	var last *verify.Record
	for i := range rec.Verify {
		if rec.Verify[i].Kind == verify.KindTest && rec.Verify[i].HeadSHA == rec.HeadSHA {
			last = &rec.Verify[i]
		}
	}
	if last == nil {
		return "**Tests:** no recorded test run on the final commit\n\n"
	}
	status := "passed"
	if !last.Passed {
		status = "failed"
	}
	sha := last.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	s := fmt.Sprintf("**Tests:** %s on %s (%s, %s", status, sha, Plural(last.Tests, "test"), Plural(last.Failures, "failure"))
	if len(last.Flaky) > 0 {
		s += ", flaky: " + strings.Join(last.Flaky, ", ")
	}
	return s + ")\n\n"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/runner/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner
git commit -m "feat(runner): prompts, review verdicts, PR outcome rule and report"
```

---

### Task 12: Runner — time budget, cancel watch, stage errors, and logging

**Files:**
- Create: `internal/runner/budget.go`, `internal/runner/log.go`, `internal/runner/budget_test.go`

**Interfaces:**
- Produces:
  - Budget and cancel:
    - `runner.Budget{Start time.Time; Total, Reserve, Stage time.Duration; Now func() time.Time}`
    - `(Budget).Exhausted() bool`
    - `(Budget).StageContext(parent) (context.Context, context.CancelFunc)`
    - `runner.ErrCancelled`
    - `runner.WatchCancel(parent context.Context, check func(context.Context) (bool, error), every time.Duration) (context.Context, func())`
    - `runner.StageError(stage string, stageCtx context.Context, b Budget, err error) string`
  - Logging:
    - `runner.NewLogger(w io.Writer, attrs ...any) *slog.Logger`, which writes Cloud Logging-shaped JSON (`severity`, `message`)
    - `runner.NewLineWriter(log *slog.Logger, stream string) *LineWriter`

- [ ] **Step 1: Write the failing tests** — `internal/runner/budget_test.go`

```go
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBudget(t *testing.T) {
	start := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	now := start
	b := Budget{Start: start, Total: 10 * time.Minute, Reserve: 2 * time.Minute, Stage: 5 * time.Minute, Now: func() time.Time { return now }}

	ctx, cancel := b.StageContext(context.Background())
	if dl, _ := ctx.Deadline(); !dl.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("first stage deadline = %s", dl)
	}
	cancel()

	now = start.Add(4 * time.Minute)
	ctx, cancel = b.StageContext(context.Background())
	if dl, _ := ctx.Deadline(); !dl.Equal(start.Add(8 * time.Minute)) {
		t.Errorf("late stage deadline = %s, want capped at total-reserve", dl)
	}
	cancel()

	if b.Exhausted() {
		t.Error("exhausted too early")
	}
	now = start.Add(8 * time.Minute)
	if !b.Exhausted() {
		t.Error("not exhausted at total-reserve")
	}
}

func TestWatchCancel(t *testing.T) {
	var flag atomic.Bool
	ctx, stop := WatchCancel(context.Background(), func(context.Context) (bool, error) { return flag.Load(), nil }, 10*time.Millisecond)
	defer stop()
	flag.Store(true)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancel marker not noticed")
	}
	if !errors.Is(context.Cause(ctx), ErrCancelled) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
}

func TestWatchCancelStop(t *testing.T) {
	ctx, stop := WatchCancel(context.Background(), func(context.Context) (bool, error) { return false, nil }, time.Hour)
	stop()
	<-ctx.Done()
	if errors.Is(context.Cause(ctx), ErrCancelled) {
		t.Fatal("stop must not look like a cancel request")
	}
}

func TestStageError(t *testing.T) {
	now := time.Now()
	fresh := Budget{Start: now, Total: time.Hour, Reserve: time.Minute, Stage: 40 * time.Minute, Now: time.Now}
	spent := Budget{Start: now.Add(-time.Hour), Total: time.Hour, Reserve: time.Minute, Stage: 40 * time.Minute, Now: time.Now}

	cancelled, cancel := context.WithCancelCause(context.Background())
	cancel(ErrCancelled)
	expired, cancel2 := context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel2()

	cases := []struct {
		ctx  context.Context
		b    Budget
		err  error
		want string
	}{
		{cancelled, fresh, context.Canceled, "cancelled during implement"},
		{expired, spent, context.DeadlineExceeded, "time budget exhausted during implement"},
		{expired, fresh, context.DeadlineExceeded, "stage implement timed out after 40m0s"},
		{context.Background(), fresh, errors.New("exec: claude not found"), "stage implement failed: exec: claude not found"},
	}
	for _, tc := range cases {
		if got := StageError("implement", tc.ctx, tc.b, tc.err); got != tc.want {
			t.Errorf("StageError = %q, want %q", got, tc.want)
		}
	}
}

func TestLoggerShape(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "run_id", "r1")
	log.Warn("careful", "stage", "implement")
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["severity"] != "WARNING" || entry["message"] != "careful" || entry["run_id"] != "r1" || entry["stage"] != "implement" {
		t.Fatalf("entry = %v", entry)
	}
}

func TestLineWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewLineWriter(NewLogger(&buf), "agent")
	w.Write([]byte("one\ntw"))
	w.Write([]byte("o\n"))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"message":"two"`) || !strings.Contains(lines[0], `"stream":"agent"`) {
		t.Fatalf("lines = %q", lines)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runner/`
Expected: FAIL, a compile error (`undefined: Budget`).

- [ ] **Step 3: Implement**

`internal/runner/budget.go`:

```go
package runner

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrCancelled is the context cause when a cancel marker was seen.
var ErrCancelled = errors.New("run cancelled")

// Budget tracks the run's time: every stage must end before Total-Reserve so
// finalize always has Reserve left (design §4.5).
type Budget struct {
	Start   time.Time
	Total   time.Duration
	Reserve time.Duration
	Stage   time.Duration
	Now     func() time.Time
}

func (b Budget) finalizeAt() time.Time { return b.Start.Add(b.Total - b.Reserve) }

// Exhausted reports whether only the finalize reserve is left.
func (b Budget) Exhausted() bool { return !b.Now().Before(b.finalizeAt()) }

// StageContext bounds one stage by the stage timeout and the finalize reserve.
func (b Budget) StageContext(parent context.Context) (context.Context, context.CancelFunc) {
	deadline := b.Now().Add(b.Stage)
	if f := b.finalizeAt(); f.Before(deadline) {
		deadline = f
	}
	return context.WithDeadline(parent, deadline)
}

// WatchCancel returns a context cancelled with ErrCancelled once check
// reports a cancel request. The returned stop function ends the watch.
func WatchCancel(parent context.Context, check func(context.Context) (bool, error), every time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if ok, err := check(ctx); err == nil && ok {
					cancel(ErrCancelled)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}

// StageError explains why a stage ended early, for the run record and draft PR.
func StageError(stage string, stageCtx context.Context, b Budget, err error) string {
	switch {
	case errors.Is(context.Cause(stageCtx), ErrCancelled):
		return "cancelled during " + stage
	case errors.Is(stageCtx.Err(), context.DeadlineExceeded) && b.Exhausted():
		return "time budget exhausted during " + stage
	case errors.Is(stageCtx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("stage %s timed out after %s", stage, b.Stage)
	default:
		return fmt.Sprintf("stage %s failed: %v", stage, err)
	}
}
```

`internal/runner/log.go`:

```go
package runner

import (
	"bytes"
	"io"
	"log/slog"
)

// NewLogger returns a JSON logger whose entries Cloud Logging understands
// (severity and message fields).
func NewLogger(w io.Writer, attrs ...any) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
	return slog.New(h).With(attrs...)
}

// LineWriter logs each line written to it as one entry tagged with stream.
type LineWriter struct {
	log    *slog.Logger
	stream string
	buf    []byte
}

// NewLineWriter returns a LineWriter for stream (such as "agent").
func NewLineWriter(log *slog.Logger, stream string) *LineWriter {
	return &LineWriter{log: log, stream: stream}
}

func (w *LineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.log.Info(string(w.buf[:i]), "stream", w.stream)
		w.buf = w.buf[i+1:]
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runner/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner
git commit -m "feat(runner): time budget, cancel watch, stage errors and structured logs"
```

---

### Task 13: `runner.Run` — the lifecycle — with the provider interface and fake provider

**Files:**
- Create: `internal/gitprov/gitprov.go`, `internal/gitprov/fake/fake.go`, `internal/gitprov/fake/fake_test.go`, `internal/runner/runner.go`, `internal/runner/runner_test.go`

**Interfaces:**
- Consumes:
  - From Tasks 11–12: prompts, verdicts, `Decide`, `Report`, `Budget`, `WatchCancel`, `StageError`, `NewLineWriter`
  - `agent.Agent`, `agent.BuildEnv`, `agent.NewRedactor`, `agent.NewSessionID`
  - `gitops.*`, `verify.WriteSettings`, `verify.Records`
  - `config.Parse`, `(*config.Config).SelectWorkflow`, `(*task.Spec).Apply`
  - `runstore.*`
- Produces:
  - Provider:
    - `gitprov.PRSpec{Branch, Base, Title, Body string; Draft bool; Labels, Reviewers []string}`
    - `gitprov.PR{Number int; URL string; Draft bool}`
    - The `gitprov.Provider` interface: `EnsurePR(ctx, PRSpec) (PR, error)` and `Comment(ctx, PR, body string) error`. `EnsurePR` creates the PR for `spec.Branch`, or, if one exists, updates only its draft state.
  - Fake provider:
    - `fake.Provider{Path string; State State}`, where `Path` is optional persistence to a JSON file
    - `fake.State{PRs []PRState}` and `fake.PRState{gitprov.PR; Spec gitprov.PRSpec; Comments []string}`
    - `fake.Load(path string) (State, error)`
  - Runner:
    - `runner.Deps{Store *runstore.Store; Provider gitprov.Provider; Agent agent.Agent; WorkDir, Remote, StateDir string; Env []string; PathPrepend string; Log *slog.Logger; Now func() time.Time; CancelPoll time.Duration}`
    - `runner.Run(ctx, Deps) (*runstore.Record, error)`. It always returns the final record, and a non-nil error only for `infra_error`.

- [ ] **Step 1: Write the provider interface and the failing fake test**

`internal/gitprov/gitprov.go`:

```go
// Package gitprov abstracts the git hosting provider (design §3.1). Real
// GitHub and Bitbucket implementations arrive in M2.
package gitprov

import "context"

// PRSpec describes the pull request a run wants.
type PRSpec struct {
	Branch    string   `json:"branch"`
	Base      string   `json:"base"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Draft     bool     `json:"draft"`
	Labels    []string `json:"labels,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

// PR is a pull request on the provider.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// Provider is what the runner needs from the git host.
type Provider interface {
	// EnsurePR creates the pull request for spec.Branch or, if one already
	// exists (the agent may have opened it), updates only its draft state.
	EnsurePR(ctx context.Context, spec PRSpec) (PR, error)
	// Comment posts a comment on the pull request.
	Comment(ctx context.Context, pr PR, body string) error
}
```

`internal/gitprov/fake/fake_test.go`:

```go
package fake

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

func TestEnsurePRCreatesThenUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provider.json")
	p := &Provider{Path: path}
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "T", Draft: true})
	if err != nil || pr.Number != 1 || !pr.Draft {
		t.Fatalf("create = %+v, %v", pr, err)
	}
	pr2, err := (&Provider{Path: path}).EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "ignored", Draft: false})
	if err != nil || pr2.Number != 1 || pr2.Draft {
		t.Fatalf("update = %+v, %v", pr2, err)
	}
	if err := p.Comment(ctx, pr2, "report"); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil || len(st.PRs) != 1 || st.PRs[0].Spec.Title != "T" || st.PRs[0].Draft || len(st.PRs[0].Comments) != 1 {
		t.Fatalf("state = %+v, %v", st, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gitprov/...`
Expected: FAIL, a compile error (`undefined: Provider`).

- [ ] **Step 3: Implement the fake** — `internal/gitprov/fake/fake.go`

```go
// Package fake is an in-memory gitprov.Provider, optionally persisted to a
// JSON file so separate processes (tests, `fugaro exec --provider fake`) share it.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// PRState is a pull request and everything done to it.
type PRState struct {
	gitprov.PR
	Spec     gitprov.PRSpec `json:"spec"`
	Comments []string       `json:"comments"`
}

// State is the fake provider's data.
type State struct {
	PRs []PRState `json:"prs"`
}

// Provider is a fake git host.
type Provider struct {
	Path  string // optional; when set, State is loaded and saved on every call
	mu    sync.Mutex
	State State
}

// Load reads a persisted state file; a missing file is an empty state.
func Load(path string) (State, error) {
	var st State
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(data, &st)
}

func (p *Provider) load() error {
	if p.Path == "" {
		return nil
	}
	st, err := Load(p.Path)
	p.State = st
	return err
}

func (p *Provider) save() error {
	if p.Path == "" {
		return nil
	}
	data, err := json.MarshalIndent(p.State, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.Path, data, 0o644)
}

// EnsurePR implements gitprov.Provider.
func (p *Provider) EnsurePR(_ context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.PR{}, err
	}
	for i := range p.State.PRs {
		if p.State.PRs[i].Spec.Branch == spec.Branch {
			p.State.PRs[i].Draft = spec.Draft
			return p.State.PRs[i].PR, p.save()
		}
	}
	n := len(p.State.PRs) + 1
	pr := gitprov.PR{Number: n, URL: fmt.Sprintf("https://example.invalid/pr/%d", n), Draft: spec.Draft}
	p.State.PRs = append(p.State.PRs, PRState{PR: pr, Spec: spec})
	return pr, p.save()
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(_ context.Context, pr gitprov.PR, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	for i := range p.State.PRs {
		if p.State.PRs[i].Number == pr.Number {
			p.State.PRs[i].Comments = append(p.State.PRs[i].Comments, body)
			return p.save()
		}
	}
	return fmt.Errorf("fake provider: no PR #%d", pr.Number)
}
```

Run: `go test ./internal/gitprov/...`
Expected: PASS.

- [ ] **Step 4: Write the failing runner tests** — `internal/runner/runner_test.go`

```go
package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

const runID = "20260926-221530-abcd"

type step func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error)

type scriptedAgent struct {
	t     *testing.T
	steps []step
	calls []agent.Request
}

func (a *scriptedAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	a.calls = append(a.calls, req)
	i := len(a.calls) - 1
	if i >= len(a.steps) {
		a.t.Errorf("unexpected agent call #%d", i+1)
		return agent.Result{}, errors.New("unexpected call")
	}
	return a.steps[i](a.t, ctx, req)
}

type harness struct {
	deps      runner.Deps
	store     *runstore.Store
	provider  *fake.Provider
	agent     *scriptedAgent
	remote    string
	failsFile string
}

// newHarness seeds a remote with the fixture repo (cfg replaces fugaro.yaml
// when non-empty) and stores a task spec.
func newHarness(t *testing.T, cfg string, spec *task.Spec) *harness {
	t.Helper()
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	if cfg != "" {
		files["fugaro.yaml"] = cfg
	}
	remote := testutil.NewRemote(t, files)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	if spec == nil {
		spec = &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature"}
	}
	store := runstore.Open(bucket, "acme-app", spec.RunID)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	failsFile := filepath.Join(tmp, "fails")
	a := &scriptedAgent{t: t}
	p := &fake.Provider{}
	return &harness{
		deps: runner.Deps{
			Store: store, Provider: p, Agent: a,
			WorkDir: filepath.Join(tmp, "work"), Remote: remote, StateDir: filepath.Join(tmp, "state"),
			Env:        []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "ANTHROPIC_API_KEY=test-key", "FIXTURE_FAILS_FILE=" + failsFile},
			CancelPoll: 20 * time.Millisecond,
		},
		store: store, provider: p, agent: a, remote: remote, failsFile: failsFile,
	}
}

func (h *harness) run(t *testing.T, steps ...step) (*runstore.Record, error) {
	t.Helper()
	h.agent.steps = steps
	return runner.Run(context.Background(), h.deps)
}

func (h *harness) fails(t *testing.T, names string) {
	t.Helper()
	if err := os.WriteFile(h.failsFile, []byte(names), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func shell(t *testing.T, req agent.Request, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
}

func verifyTest(t *testing.T, ctx context.Context, req agent.Request) {
	t.Helper()
	_, err := verify.Run(ctx, verify.Options{StateDir: envValue(req.Env, "FUGARO_STATE_DIR"), Kind: verify.KindTest, Env: req.Env, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
}

// implement commits a file, verifies, and writes pr.md.
func implement(name string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'Add "+name+"'")
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add "+name+"\n\nAdds "+name+".txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
}

func review(verdict string, findings int) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fs := make([]map[string]string, findings)
		for i := range fs {
			fs[i] = map[string]string{"summary": "fix it"}
		}
		b, _ := json.Marshal(map[string]any{"verdict": verdict, "findings": fs})
		return agent.Result{Structured: b, CostUSD: 0.5}, nil
	}
}

func blockUntilDone(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	<-ctx.Done()
	return agent.Result{}, ctx.Err()
}

func onlyPR(t *testing.T, p *fake.Provider) fake.PRState {
	t.Helper()
	if len(p.State.PRs) != 1 {
		t.Fatalf("want exactly one PR, got %+v", p.State.PRs)
	}
	return p.State.PRs[0]
}

func TestReadyPR(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
		t.Fatalf("record = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || pr.Spec.Title != "Add feature" || pr.Spec.Body != "Adds feature.txt." || pr.Spec.Base != "main" || pr.Spec.Branch != "fugaro/"+runID {
		t.Fatalf("PR = %+v", pr)
	}
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "ready for review") {
		t.Fatalf("comments = %q", pr.Comments)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s != record head %s", got, rec.HeadSHA)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Status != runstore.StatusSucceeded || stored.FinishedAt == nil || stored.CostUSD != 1.5 {
		t.Fatalf("stored record = %+v, %v", stored, err)
	}
	impl, rev := h.agent.calls[0], h.agent.calls[1]
	if impl.Resume || impl.Prompt != "Add a feature" || !strings.Contains(impl.AppendSystemPrompt, "fugaro/"+runID) {
		t.Fatalf("implement request = %+v", impl)
	}
	if rev.SessionID == impl.SessionID || rev.JSONSchema != runner.VerdictSchema || rev.AppendSystemPrompt != "" {
		t.Fatalf("review request = %+v", rev)
	}
}

func TestFailingTestsOpenDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "tests failing on the final commit" {
		t.Fatalf("record = %+v", rec)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

func TestFixRoundResumesImplementSession(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !strings.Contains(req.Prompt, "- fix it") {
			t.Errorf("fix prompt lacks the finding: %q", req.Prompt)
		}
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || len(rec.Reviews) != 2 {
		t.Fatalf("record = %+v", rec)
	}
	if f := h.agent.calls[2]; !f.Resume || f.SessionID != h.agent.calls[0].SessionID {
		t.Fatalf("fix did not resume the implement session: %+v", f)
	}
}

func TestReviewRoundsExhausted(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Try again'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review round 2 still has 1 finding" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestUncommittedWorkIsCommittedButNotVerified(t *testing.T) {
	h := newHarness(t, "", nil)
	leaveDirty := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		testutil.WriteFiles(t, req.Dir, map[string]string{"forgotten.txt": "x\n"})
		return res, err
	}
	rec, err := h.run(t, leaveDirty, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "no verified test run on the final commit" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID); got != "fugaro: uncommitted work at finalize" {
		t.Fatalf("last pushed commit = %q", got)
	}
}

func TestNoCommitsStillOpensDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	nothing := func(*testing.T, context.Context, agent.Request) (agent.Result, error) { return agent.Result{}, nil }
	rec, err := h.run(t, nothing, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "the agent made no commits" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "rev-list", "--count", "main..refs/heads/fugaro/"+runID); got != "1" {
		t.Fatalf("commits ahead = %s, want the one empty commit", got)
	}
}

func TestStageTimeoutOpensDraft(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 1m, stage: 1s, verify: 30s, finalize_reserve: 10s }", 1)
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, blockUntilDone)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 1s" || len(h.agent.calls) != 1 {
		t.Fatalf("record = %+v, calls = %d", rec, len(h.agent.calls))
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("PR is not a draft")
	}
}

func TestCancelledRunStillOpensDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	cancelThenBlock := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, cancelThenBlock)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "cancelled during implement" {
		t.Fatalf("record = %+v", rec)
	}
	onlyPR(t, h.provider)
}

func TestInvalidConfigIsInfraError(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "provider: github", "provider: gitlab", 1)
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "git.provider") {
		t.Fatalf("err = %v", err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || len(h.provider.State.PRs) != 0 {
		t.Fatalf("record = %+v", rec)
	}
	if stored, _ := h.store.ReadRecord(context.Background()); stored == nil || stored.Status != runstore.StatusInfraError {
		t.Fatalf("stored record = %+v", stored)
	}
}

func TestBootstrapRejections(t *testing.T) {
	t.Run("follow-up", func(t *testing.T) {
		spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Branch: "fugaro/20260925-000000-0000", PR: 3, PreviousRun: "20260925-000000-0000"}
		h := newHarness(t, "", spec)
		if rec, err := h.run(t); err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "follow-up") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
	t.Run("state dir inside checkout", func(t *testing.T) {
		h := newHarness(t, "", nil)
		h.deps.StateDir = filepath.Join(h.deps.WorkDir, "state")
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "outside the checkout") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
	t.Run("missing secret", func(t *testing.T) {
		h := newHarness(t, "", nil)
		h.deps.Env = h.deps.Env[:3] // drop FIXTURE_FAILS_FILE
		if rec, err := h.run(t); err == nil || !strings.Contains(rec.Reason, "FIXTURE_FAILS_FILE") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test ./internal/runner/`
Expected: FAIL, a compile error (`undefined: runner.Deps`).

- [ ] **Step 6: Implement** — `internal/runner/runner.go`

```go
// Package runner executes one Fugaro run: bootstrap, implement, review and
// fix rounds, finalize (design §4).
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Deps is everything a run touches.
type Deps struct {
	Store       *runstore.Store
	Provider    gitprov.Provider
	Agent       agent.Agent
	WorkDir     string   // the repository checkout (baked into the image)
	Remote      string   // cloned into WorkDir when it has no checkout
	StateDir    string   // FUGARO_STATE_DIR; must be outside WorkDir
	Env         []string // the runner's environment, usually os.Environ()
	PathPrepend string   // directory holding the fugaro binary, put first on the agent's PATH
	Log         *slog.Logger
	Now         func() time.Time
	CancelPoll  time.Duration
}

type run struct {
	d            Deps
	rec          *runstore.Record
	spec         *task.Spec
	cfg          *config.Config
	wf           config.Workflow
	repo         *gitops.Repo
	env          []string
	secrets      []string
	budget       Budget
	instructions string
	reviewFile   string
	stageN       map[string]int
	failReason   string
	cancelled    bool
}

// Run executes the run whose task spec is in d.Store. It always returns the
// final record; the error is non-nil only for an infra_error.
func Run(ctx context.Context, d Deps) (rec *runstore.Record, err error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.CancelPoll == 0 {
		d.CancelPoll = 30 * time.Second
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	r := &run{
		d:      d,
		stageN: map[string]int{},
		rec: &runstore.Record{Version: 1, Status: runstore.StatusRunning, Stage: "bootstrap",
			Outcome: runstore.OutcomeNone, StartedAt: d.Now().UTC()},
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
		if err != nil {
			r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, err.Error()
			d.Log.Error("run failed", "stage", r.rec.Stage, "err", err)
		}
		finished := d.Now().UTC()
		r.rec.FinishedAt = &finished
		if werr := d.Store.WriteRecord(context.WithoutCancel(ctx), r.rec); werr != nil && err == nil {
			err = fmt.Errorf("writing run record: %w", werr)
		}
		rec = r.rec
	}()

	runCtx, stopWatch := WatchCancel(ctx, d.Store.CancelRequested, d.CancelPoll)
	defer stopWatch()
	if err := r.bootstrap(runCtx); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	r.agentLoop(runCtx)
	finCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.wf.Timeouts.FinalizeReserve.Duration)
	defer cancel()
	if err := r.finalize(finCtx); err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}
	return r.rec, nil
}

func (r *run) save(ctx context.Context) {
	if err := r.d.Store.WriteRecord(context.WithoutCancel(ctx), r.rec); err != nil {
		r.d.Log.Warn("writing run record failed", "err", err)
	}
}

// fail records the first reason the run cannot produce a ready PR.
func (r *run) fail(reason string) {
	if r.failReason == "" {
		r.failReason = reason
	}
}

func (r *run) bootstrap(ctx context.Context) error {
	spec, err := r.d.Store.ReadTask(ctx)
	if err != nil {
		return err
	}
	r.spec = spec
	r.rec.RunID, r.rec.Repo = spec.RunID, spec.Repo
	r.save(ctx)
	if spec.IsFollowUp() {
		return errors.New("follow-up runs are not supported by this version of fugaro")
	}
	if rel, err := filepath.Rel(r.d.WorkDir, r.d.StateDir); err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("state dir %s must be outside the checkout %s", r.d.StateDir, r.d.WorkDir)
	}

	repo, err := gitops.OpenOrClone(ctx, r.d.WorkDir, r.d.Remote, gitops.IdentityEnv())
	if err != nil {
		return err
	}
	branch := "fugaro/" + spec.RunID
	if err := repo.CheckoutNewBranch(ctx, spec.Ref, branch); err != nil {
		return err
	}
	r.repo = repo

	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, "fugaro.yaml"))
	if err != nil {
		return fmt.Errorf("reading fugaro.yaml at %s: %w", spec.Ref, err)
	}
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.String()
		}
		return fmt.Errorf("fugaro.yaml is invalid: %s", strings.Join(msgs, "; "))
	}
	name, wf, err := cfg.SelectWorkflow(spec.Workflow)
	if err != nil {
		return err
	}
	if err := spec.Apply(cfg, &wf); err != nil {
		return err
	}
	r.cfg, r.wf, r.rec.Workflow = cfg, wf, name
	if err := repo.FetchBase(ctx, cfg.Git.BaseBranch); err != nil {
		return err
	}
	if r.instructions, err = r.readRepoFile(cfg.Agent.Instructions); err != nil {
		return fmt.Errorf("agent.instructions: %w", err)
	}
	if cfg.Agent.Review != "" && !strings.HasPrefix(cfg.Agent.Review, "/") {
		if r.reviewFile, err = r.readRepoFile(cfg.Agent.Review); err != nil {
			return fmt.Errorf("agent.review: %w", err)
		}
	}

	if err := os.RemoveAll(r.d.StateDir); err != nil {
		return err
	}
	if err := verify.WriteSettings(r.d.StateDir, verify.Settings{
		RepoDir: r.d.WorkDir, Build: wf.Commands.Build, Test: wf.Commands.Test,
		RerunFailed: wf.Commands.RerunFailed, Reports: wf.Commands.Reports,
		TimeoutS: int(wf.Timeouts.Verify.Seconds()),
	}); err != nil {
		return err
	}
	secretEnvs := make([]string, len(wf.Secrets))
	for i, s := range wf.Secrets {
		secretEnvs[i] = s.Env
	}
	set := map[string]string{"FUGARO_STATE_DIR": r.d.StateDir}
	for k, v := range gitops.Identity {
		set[k] = v
	}
	if r.env, r.secrets, err = agent.BuildEnv(r.d.Env, agent.EnvSpec{
		Auth: cfg.Agent.Auth, Secrets: secretEnvs, Set: set, PathPrepend: r.d.PathPrepend,
	}); err != nil {
		return err
	}
	r.budget = Budget{Start: r.rec.StartedAt, Total: wf.Timeouts.Total.Duration,
		Reserve: wf.Timeouts.FinalizeReserve.Duration, Stage: wf.Timeouts.Stage.Duration, Now: r.d.Now}
	r.rec.Branch = branch
	r.save(ctx)
	return nil
}

func (r *run) readRepoFile(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, rel))
	return string(data), err
}

func (r *run) agentLoop(ctx context.Context) {
	sessionID := agent.NewSessionID()
	sys := SystemPrompt(PromptData{Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, StateDir: r.d.StateDir}, r.instructions)
	if _, ok := r.stage(ctx, "implement", agent.Request{Prompt: r.spec.Task, SessionID: sessionID, AppendSystemPrompt: sys}); !ok {
		return
	}
	reviewPrompt := ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.cfg.Git.BaseBranch)
	rounds := r.cfg.Agent.ReviewRounds
	for round := 1; round <= rounds; round++ {
		res, ok := r.stage(ctx, "review", agent.Request{Prompt: reviewPrompt, SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema})
		if !ok {
			return
		}
		v := ParseVerdict(res)
		r.rec.Reviews = append(r.rec.Reviews, runstore.ReviewSummary{Round: round, Verdict: v.Verdict, Findings: len(v.Findings)})
		r.save(ctx)
		if v.Verdict == "ship" || round == rounds {
			return
		}
		if _, ok := r.stage(ctx, "fix", agent.Request{Prompt: FixPrompt(v), SessionID: sessionID, Resume: true, AppendSystemPrompt: sys}); !ok {
			return
		}
	}
}

// stage runs one agent stage and reports whether the loop may continue.
func (r *run) stage(ctx context.Context, name string, req agent.Request) (agent.Result, bool) {
	if ctx.Err() != nil {
		r.fail(StageError(name, ctx, r.budget, ctx.Err()))
		r.cancelled = errors.Is(context.Cause(ctx), ErrCancelled)
		return agent.Result{}, false
	}
	if r.budget.Exhausted() {
		r.fail("time budget exhausted before stage " + name)
		return agent.Result{}, false
	}
	r.stageN[name]++
	n := r.stageN[name]
	started := r.d.Now()
	r.rec.Stage = name
	r.save(ctx)
	log := r.d.Log.With("stage", name)
	log.Info("stage started", "n", n)

	var transcript bytes.Buffer
	tw := agent.NewRedactor(&transcript, r.secrets)
	sw := agent.NewRedactor(NewLineWriter(log, "agent"), r.secrets)
	req.Dir, req.Env, req.Transcript, req.Stderr = r.d.WorkDir, r.env, tw, sw
	req.Model, req.MaxBudgetUSD = r.cfg.Agent.Model, r.cfg.Agent.MaxBudgetUSD

	stageCtx, cancel := r.budget.StageContext(ctx)
	defer cancel()
	res, err := r.d.Agent.Run(stageCtx, req)
	_ = tw.Flush()
	_ = sw.Flush()
	if perr := r.d.Store.PutFile(context.WithoutCancel(ctx), fmt.Sprintf("transcripts/%s-%d.jsonl", name, n), transcript.Bytes(), "application/x-ndjson"); perr != nil {
		log.Warn("storing transcript failed", "err", perr)
	}
	r.rec.CostUSD += res.CostUSD
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{Name: name, StartedAt: started.UTC(), DurationS: r.d.Now().Sub(started).Seconds()})
	log.Info("stage finished", "n", n, "cost_usd", res.CostUSD, "err", err)

	switch {
	case err != nil:
		r.fail(StageError(name, stageCtx, r.budget, err))
		r.cancelled = errors.Is(context.Cause(stageCtx), ErrCancelled)
		return res, false
	case res.IsError:
		r.fail(fmt.Sprintf("stage %s: the agent reported an error (%s)", name, res.Subtype))
		return res, false
	}
	r.save(ctx)
	return res, true
}

func (r *run) finalize(ctx context.Context) error {
	r.rec.Stage = "finalize"
	r.save(ctx)
	base := r.cfg.Git.BaseBranch
	if _, err := r.repo.CommitAll(ctx, "fugaro: uncommitted work at finalize"); err != nil {
		return err
	}
	ahead, err := r.repo.AheadOf(ctx, base)
	if err != nil {
		return err
	}
	if ahead == 0 {
		r.fail("the agent made no commits")
		if err := r.repo.CommitEmpty(ctx, "fugaro: "+r.failReason); err != nil {
			return err
		}
	}
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		return err
	}
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		return err
	}
	r.rec.HeadSHA, r.rec.Verify = sha, records

	var last *runstore.ReviewSummary
	if n := len(r.rec.Reviews); n > 0 {
		last = &r.rec.Reviews[n-1]
	}
	ready, reason := Decide(records, sha, last)
	if r.failReason != "" {
		ready, reason = false, r.failReason
	}
	if err := r.repo.Push(ctx, r.rec.Branch); err != nil {
		return err
	}
	title, body := r.prText()
	pr, err := r.d.Provider.EnsurePR(ctx, gitprov.PRSpec{
		Branch: r.rec.Branch, Base: base, Title: title, Body: body, Draft: !ready,
		Labels: r.cfg.Git.PR.Labels, Reviewers: r.cfg.Git.PR.Reviewers,
	})
	if err != nil {
		return err
	}
	r.rec.PR = &runstore.PRRef{Number: pr.Number, URL: pr.URL}
	r.rec.Reason = reason
	switch {
	case ready:
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReady
	case r.cancelled:
		r.rec.Status, r.rec.Outcome = runstore.StatusCancelled, runstore.OutcomeDraft
	default:
		r.rec.Status, r.rec.Outcome = runstore.StatusFailed, runstore.OutcomeDraft
	}
	report := Report(r.rec, r.d.Store.Prefix())
	if err := r.d.Provider.Comment(ctx, pr, report); err != nil {
		r.d.Log.Warn("posting the run report failed", "err", err)
	}
	if err := r.d.Store.PutFile(ctx, "report.md", []byte(report), "text/markdown"); err != nil {
		r.d.Log.Warn("storing the run report failed", "err", err)
	}
	r.rec.Stage = "writeback" // cache write-back arrives in M4
	return nil
}

// prText returns the PR title and body the agent wrote to pr.md, or defaults
// built from the task.
func (r *run) prText() (string, string) {
	if data, err := os.ReadFile(filepath.Join(r.d.StateDir, "pr.md")); err == nil {
		title, body, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
		title = strings.TrimSpace(strings.TrimLeft(title, "# "))
		if title != "" {
			return title, strings.TrimSpace(body)
		}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(r.spec.Task), "\n")
	if runes := []rune(first); len(runes) > 72 {
		first = string(runes[:71]) + "…"
	}
	return first, fmt.Sprintf("Opened by Fugaro run `%s`.\n\n## Task\n\n%s", r.rec.RunID, r.spec.Task)
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./internal/runner/ ./internal/gitprov/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/gitprov internal/runner
git commit -m "feat(runner): full run lifecycle with always-PR finalize"
```

---

### Task 14: `fugaro exec` and the hermetic end-to-end test

**Files:**
- Create: `internal/cli/exec.go`, `internal/cli/exec_test.go`, `internal/e2e/e2e_test.go`
- Modify: `internal/cli/root.go` (register `newExecCmd()`)

**Interfaces:**
- Consumes: `runner.Run`, `runner.Deps`, `runner.NewLogger`, `runstore.Open/ParseRef`, `task.*`, `agent.Claude`, `fake.Provider`, `blob.OpenBucket` (fileblob)
- Produces: the hidden `fugaro exec` command, with flags `--bucket` (env `FUGARO_BUCKET`), `--run` (env `FUGARO_RUN`, `<slug>/<run-id>`), `--task-file` (a path, or `-` for stdin; a run ID is generated if missing), `--workdir` (default `/work/repo`), `--remote`, `--state-dir` (default `/work/state`), `--provider` (only `fake` until M2), `--provider-state`, `--claude` (default `claude`), and `--cancel-poll` (default 30s). It prints the final run record as JSON on stdout and exits 2 on `infra_error`.

- [ ] **Step 1: Write the failing tests**

`internal/cli/exec_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestExecNeedsBucket(t *testing.T) {
	t.Setenv("FUGARO_BUCKET", "")
	_, _, err := execute(t, "exec", "--bucket", "")
	if err == nil || !strings.Contains(err.Error(), "--bucket") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecNeedsFakeProviderUntilM2(t *testing.T) {
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--run", "acme-app/20260926-221530-abcd")
	if err == nil || !strings.Contains(err.Error(), "--provider fake") {
		t.Fatalf("err = %v", err)
	}
}
```

`internal/e2e/e2e_test.go`:

```go
// Package e2e runs the fugaro binary end to end against a local git remote,
// a file:// bucket, the fake provider and the fake claude.
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const runID = "20260926-221530-abcd"

const implementOK = `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nAdds feature.txt.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","text":"done, key ${ANTHROPIC_API_KEY}","cost":1}`
const reviewShip = `{"structured":{"verdict":"ship","findings":[]},"text":"LGTM","cost":0.5}`

type result struct {
	rec      runstore.Record
	provider fake.State
	calls    []testutil.FakeCall
	bucket   string
	exitCode int
	elapsed  time.Duration
}

type scenario struct {
	config   string // replaces the fixture fugaro.yaml when non-empty
	script   string // fake claude script
	fails    string // tests that fail
	cancelAt time.Duration
}

func runScenario(t *testing.T, sc scenario) result {
	t.Helper()
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	if sc.config != "" {
		files["fugaro.yaml"] = sc.config
	}
	remote := testutil.NewRemote(t, files)
	fugaro := testutil.BuildFugaro(t)
	claude := testutil.FakeClaude(t, sc.script)
	tmp := t.TempDir()
	bucket := filepath.Join(tmp, "bucket")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	failsFile := filepath.Join(tmp, "fails")
	if err := os.WriteFile(failsFile, []byte(sc.fails), 0o644); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(tmp, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"version":1,"run_id":"`+runID+`","repo":"acme/app","ref":"main","task":"Add a feature"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	providerState := filepath.Join(tmp, "provider.json")
	cmd := exec.Command(fugaro, "exec",
		"--bucket", "file://"+bucket, "--task-file", taskFile,
		"--workdir", filepath.Join(tmp, "work"), "--remote", remote, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "fake", "--provider-state", providerState, "--claude", claude, "--cancel-poll", "100ms")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"ANTHROPIC_API_KEY=test-key-1234", "FIXTURE_FAILS_FILE=" + failsFile, "UNDECLARED_SECRET=hunter2",
	}
	if sc.cancelAt > 0 {
		go func() {
			time.Sleep(sc.cancelAt)
			marker := filepath.Join(bucket, "runs", "acme-app", runID, "cancel")
			_ = os.WriteFile(marker, []byte("now"), 0o644)
		}()
	}
	start := time.Now()
	out, _ := cmd.CombinedOutput()
	res := result{bucket: bucket, exitCode: cmd.ProcessState.ExitCode(), elapsed: time.Since(start), calls: testutil.FakeClaudeCalls(t, claude)}
	data, err := os.ReadFile(filepath.Join(bucket, "runs", "acme-app", runID, "result.json"))
	if err != nil {
		t.Fatalf("no result.json: %v\n%s", err, out)
	}
	if err := json.Unmarshal(data, &res.rec); err != nil {
		t.Fatal(err)
	}
	if res.provider, err = fake.Load(providerState); err != nil {
		t.Fatal(err)
	}
	t.Logf("fugaro exec output:\n%s", out)
	return res
}

func TestReadyRun(t *testing.T) {
	r := runScenario(t, scenario{script: `{"calls":[` + implementOK + `,` + reviewShip + `]}`})
	if r.exitCode != 0 || r.rec.Status != runstore.StatusSucceeded || r.rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("exit %d, record %+v", r.exitCode, r.rec)
	}
	if len(r.provider.PRs) != 1 || r.provider.PRs[0].Draft || r.provider.PRs[0].Spec.Title != "Add feature" {
		t.Fatalf("provider = %+v", r.provider)
	}
	if len(r.calls) != 2 || r.calls[0].Prompt != "Add a feature" || !slices.Contains(r.calls[1].Args, "--json-schema") {
		t.Fatalf("calls = %+v", r.calls)
	}
	env := r.calls[0].Env
	if !slices.Contains(env, "ANTHROPIC_API_KEY=test-key-1234") || !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "FIXTURE_FAILS_FILE=") }) {
		t.Fatalf("agent env lacks declared variables: %v", env)
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "UNDECLARED_SECRET=") }) {
		t.Fatal("an undeclared variable reached the agent")
	}
	transcript, err := os.ReadFile(filepath.Join(r.bucket, "runs", "acme-app", runID, "transcripts", "implement-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(transcript), "test-key-1234") || !strings.Contains(string(transcript), "[REDACTED]") {
		t.Fatalf("transcript not redacted: %s", transcript)
	}
	if _, err := os.Stat(filepath.Join(r.bucket, "runs", "acme-app", runID, "report.md")); err != nil {
		t.Fatal("report.md missing")
	}
}

func TestFailingTestsDraft(t *testing.T) {
	r := runScenario(t, scenario{script: `{"calls":[` + implementOK + `,` + reviewShip + `]}`, fails: "beta"})
	if r.rec.Outcome != runstore.OutcomeDraft || r.rec.Reason != "tests failing on the final commit" || !r.provider.PRs[0].Draft {
		t.Fatalf("record %+v", r.rec)
	}
}

func TestFlakyRerunIsReady(t *testing.T) {
	flaky := `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature'; fugaro verify test; : > \"$FIXTURE_FAILS_FILE\"; fugaro verify test --rerun-failed","cost":1}`
	r := runScenario(t, scenario{script: `{"calls":[` + flaky + `,` + reviewShip + `]}`, fails: "beta"})
	if r.rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record %+v", r.rec)
	}
	last := r.rec.Verify[len(r.rec.Verify)-1]
	if !last.Rerun || !slices.Equal(last.Flaky, []string{"pkg.Suite.beta"}) {
		t.Fatalf("last verify = %+v", last)
	}
}

func TestFixRound(t *testing.T) {
	changes := `{"structured":{"verdict":"changes","findings":[{"summary":"add a test"}]}}`
	fix := `{"shell":"echo more >> feature.txt && git commit -qam 'Address review' && fugaro verify test"}`
	r := runScenario(t, scenario{script: `{"calls":[` + implementOK + `,` + changes + `,` + fix + `,` + reviewShip + `]}`})
	if r.rec.Outcome != runstore.OutcomeReady || len(r.rec.Reviews) != 2 || len(r.calls) != 4 {
		t.Fatalf("record %+v, %d calls", r.rec, len(r.calls))
	}
	implSession := r.calls[0].Args[slices.Index(r.calls[0].Args, "--session-id")+1]
	fixArgs := r.calls[2].Args
	if i := slices.Index(fixArgs, "--resume"); i < 0 || fixArgs[i+1] != implSession {
		t.Fatalf("fix args %q do not resume %s", fixArgs, implSession)
	}
}

func TestStageTimeout(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 1m, stage: 2s, verify: 30s, finalize_reserve: 10s }", 1)
	r := runScenario(t, scenario{config: cfg, script: `{"calls":[{"sleep_s":30}]}`})
	if r.rec.Status != runstore.StatusFailed || !strings.Contains(r.rec.Reason, "stage implement timed out") || len(r.provider.PRs) != 1 {
		t.Fatalf("record %+v", r.rec)
	}
	if r.elapsed > 20*time.Second {
		t.Fatalf("run took %s; the stage timeout did not stop the agent", r.elapsed)
	}
}

func TestCancel(t *testing.T) {
	r := runScenario(t, scenario{script: `{"calls":[{"sleep_s":30}]}`, cancelAt: time.Second})
	if r.rec.Status != runstore.StatusCancelled || r.rec.Reason != "cancelled during implement" || len(r.provider.PRs) != 1 || !r.provider.PRs[0].Draft {
		t.Fatalf("record %+v", r.rec)
	}
}

func TestInvalidConfigExitsTwo(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "provider: github", "provider: gitlab", 1)
	r := runScenario(t, scenario{config: cfg, script: `{"calls":[]}`})
	if r.exitCode != 2 || r.rec.Status != runstore.StatusInfraError || len(r.provider.PRs) != 0 {
		t.Fatalf("exit %d, record %+v", r.exitCode, r.rec)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ ./internal/e2e/`
Expected: FAIL. `exec` is an unknown command, so the e2e scenarios find no `result.json`.

- [ ] **Step 3: Implement** — `internal/cli/exec.go`

```go
package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // file:// buckets for local runs

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

type execOptions struct {
	bucket, run, taskFile     string
	workDir, remote, stateDir string
	provider, providerState   string
	claudeBin                 string
	cancelPoll                time.Duration
}

func newExecCmd() *cobra.Command {
	var o execOptions
	cmd := &cobra.Command{
		Use:    "exec",
		Short:  "Execute a run inside a Fugaro worker (the container entrypoint)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return runExec(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.bucket, "bucket", os.Getenv("FUGARO_BUCKET"), "runs bucket URL (gs://… or file://…)")
	f.StringVar(&o.run, "run", os.Getenv("FUGARO_RUN"), "run to execute, as <repo-slug>/<run-id>")
	f.StringVar(&o.taskFile, "task-file", "", "store this task spec (path, or - for stdin) and run it; a run ID is generated if missing")
	f.StringVar(&o.workDir, "workdir", "/work/repo", "repository checkout")
	f.StringVar(&o.remote, "remote", "", "clone this remote when --workdir has no checkout")
	f.StringVar(&o.stateDir, "state-dir", "/work/state", "run state directory, outside the checkout")
	f.StringVar(&o.provider, "provider", "", `git provider; only "fake" until real providers land`)
	f.StringVar(&o.providerState, "provider-state", "", "state file for --provider fake")
	f.StringVar(&o.claudeBin, "claude", "claude", "claude binary")
	f.DurationVar(&o.cancelPoll, "cancel-poll", 30*time.Second, "how often to check for a cancel request")
	return cmd
}

func runExec(cmd *cobra.Command, o execOptions) error {
	ctx := cmd.Context()
	if o.bucket == "" {
		return errors.New("--bucket (or FUGARO_BUCKET) is required")
	}
	provider, err := openProvider(o)
	if err != nil {
		return err
	}
	bucket, err := blob.OpenBucket(ctx, o.bucket)
	if err != nil {
		return fmt.Errorf("opening bucket %s: %w", o.bucket, err)
	}
	defer bucket.Close()

	var slug, runID string
	if o.taskFile != "" {
		spec, err := readTaskFile(o.taskFile, cmd.InOrStdin())
		if err != nil {
			return err
		}
		slug, runID = task.Slug(spec.Repo), spec.RunID
		if err := runstore.Open(bucket, slug, runID).WriteTask(ctx, spec); err != nil {
			return err
		}
	} else if slug, runID, err = runstore.ParseRef(o.run); err != nil {
		return err
	}

	workDir, err := filepath.Abs(o.workDir)
	if err != nil {
		return err
	}
	stateDir, err := filepath.Abs(o.stateDir)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	log := runner.NewLogger(cmd.ErrOrStderr(), "run_id", runID, "repo", slug)
	rec, runErr := runner.Run(ctx, runner.Deps{
		Store: runstore.Open(bucket, slug, runID), Provider: provider, Agent: agent.Claude{Bin: o.claudeBin},
		WorkDir: workDir, Remote: o.remote, StateDir: stateDir, Env: os.Environ(),
		PathPrepend: filepath.Dir(exe), Log: log, CancelPoll: o.cancelPoll,
	})
	if rec != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(rec)
	}
	if runErr != nil {
		return &ExitError{Code: ExitRemoteError, Err: runErr}
	}
	return nil
}

func openProvider(o execOptions) (gitprov.Provider, error) {
	switch o.provider {
	case "fake":
		return &fake.Provider{Path: o.providerState}, nil
	default:
		return nil, errors.New("real git providers are not implemented yet; use --provider fake")
	}
}

func readTaskFile(path string, stdin io.Reader) (*task.Spec, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var s task.Spec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("task file: %w", err)
	}
	if s.Version == 0 {
		s.Version = 1
	}
	if s.RunID == "" {
		if s.RunID, err = task.NewRunID(time.Now(), rand.Reader); err != nil {
			return nil, err
		}
	}
	return &s, s.Validate()
}
```

In `internal/cli/root.go`:

```go
	root.AddCommand(newVersionCmd(), newValidateCmd(), newConfigCmd(), newVerifyCmd(), newExecCmd())
```

- [ ] **Step 4: Run the full suite**

Run: `go mod tidy && gofmt -w . && git diff --stat && go vet ./... && go test -race ./...`
Expected: `gofmt -w` changes nothing significant (whitespace only), and every package passes. The e2e package takes roughly 10–20 seconds, most of it the timeout and cancel scenarios.

- [ ] **Step 5: Try it by hand**

```bash
go build -o /tmp/fugaro ./cmd/fugaro
/tmp/fugaro config example | head -5
/tmp/fugaro validate testdata/config/valid/full.yaml
```

Expected: the example header, then `testdata/config/valid/full.yaml is valid`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli internal/e2e go.mod go.sum
git commit -m "feat(cli): fugaro exec and hermetic end-to-end tests"
```

---

## After M1

Each of these milestones gets its own plan, written against the code M1 produces:

- **M2** replaces `--provider fake` with GitHub (App installation token) and Bitbucket Cloud adapters, implementing `gitprov.Provider`. It also adds the agent git token (design §6.2).
- **M3** adds the base images and the derived-image template. Use an init process (`tini`) as PID 1, so that processes `procgroup` kills are reaped.
- **M4** adds `internal/backend/gcp`, the `run`/`ls`/`logs`/`diagnose`/`cancel` commands, cache restore and write-back in bootstrap and writeback, and branch locks.
- **M5**, **M6**, and **M7** follow design §14.
