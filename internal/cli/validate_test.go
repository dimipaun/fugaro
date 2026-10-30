package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"strconv"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

const cliMinimalYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
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

// cliBaseYAML is cliMinimalYAML's workflow with no base: (the default
// Fugaro base), and no image.tools, dockerfile: or mise config: exactly
// what CheckWarnings' "installs no language runtime" warning is for.
const cliBaseYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestValidateWarnsNoLanguageRuntime pins config.CheckWarnings' wiring into
// `fugaro validate` (internal/cli/validate.go's one call to it): a Fugaro-
// base workflow with no mise config and no image.tools is valid but prints
// the warning, in both the plain and --json forms.
func TestValidateWarnsNoLanguageRuntime(t *testing.T) {
	path := writeConfig(t, cliBaseYAML)
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("out=%q errOut=%q err=%v", out, errOut, err)
	}
	if !strings.Contains(errOut, "warning: workflows.app: no mise.toml, .tool-versions or image.tools") {
		t.Fatalf("errOut = %q", errOut)
	}
	out, _, err = execute(t, "validate", "--json", path)
	if err != nil {
		t.Fatal(err)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if !got.Valid || len(got.Warnings) != 1 || got.Warnings[0].Path != "workflows.app" {
		t.Fatalf("got %+v", got)
	}
}

// TestValidateRefusesLeftoverAllowForks (owner ruling 2026-10-12):
// review.allow_forks is removed outright, not merely undocumented — a
// fugaro.yaml still setting it is invalid, refused by the strict parser
// the same as any other unknown key, not a warning.
//
// Mutation (restore the Config.Review field): this test fails, finding
// the leftover key accepted instead of refused.
func TestValidateRefusesLeftoverAllowForks(t *testing.T) {
	path := writeConfig(t, cliMinimalYAML+"review:\n  allow_forks: true\n")
	_, _, err := execute(t, "validate", path)
	if err == nil {
		t.Fatal("want an error for the removed review.allow_forks key, got none")
	}
}

// TestValidateSkipsNoLanguageRuntimeWarning covers CheckWarnings' two skip
// conditions through the CLI: image.tools set and dockerfile: set both
// silence the warning that a plain Fugaro-base workflow gets.
func TestValidateSkipsNoLanguageRuntimeWarning(t *testing.T) {
	for name, body := range map[string]string{
		"image.tools set": strings.Replace(cliBaseYAML, "{ commands:", "{ image: { tools: { node: \"24\" } }, commands:", 1),
		"dockerfile set":  strings.Replace(cliBaseYAML, "{ commands:", "{ dockerfile: .fugaro/app.Dockerfile, commands:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, body)
			if name == "dockerfile set" {
				if err := os.MkdirAll(filepath.Join(filepath.Dir(path), ".fugaro"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".fugaro", "app.Dockerfile"), []byte("FROM x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, errOut, _ := execute(t, "validate", path)
			if strings.Contains(errOut, "installs no language runtime") {
				t.Fatalf("%s: unexpected warning: %q", name, errOut)
			}
		})
	}
}

// TestValidateDoesNotWarnCheckoutClone (0.7.0 safety fix, review finding,
// inverted by the cloneShipped flip): a checkout: clone workflow is valid
// config, and since 0.9.0 it is config that runs too — a clone job can be
// deployed and a clone launch passes the launch gate on a current base
// image. `fugaro validate` must therefore say nothing about it: the warning
// it used to print ("clone runs are refused at launch until fugaro 0.7.5;
// checkout: baked runs today") would send a reader away from an option that
// works.
//
// Mutation (run, restore): set cloneShipped back to false in
// recipes_skew.go — cloneWorkflowWarnings reports the workflow again, the
// errOut assertions fail, and the JSON's warnings carry
// workflows.app.checkout.
func TestValidateDoesNotWarnCheckoutClone(t *testing.T) {
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "{ commands:", "{ checkout: clone, commands:", 1))
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("out=%q errOut=%q err=%v", out, errOut, err)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("out = %q, want valid", out)
	}
	for _, not := range []string{"workflows.app.checkout", "has not shipped yet", "checkout: baked runs today"} {
		if strings.Contains(errOut, not) {
			t.Errorf("a shipped checkout: clone is still warned about with %q: %q", not, errOut)
		}
	}
	out, _, err = execute(t, "validate", "--json", path)
	if err != nil {
		t.Fatal(err)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if !got.Valid || slices.ContainsFunc(got.Warnings, func(p config.Problem) bool { return p.Path == "workflows.app.checkout" }) {
		t.Fatalf("got %+v", got)
	}
}

func TestConfigExample(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "config", "example")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(config.Example) {
		t.Fatal("config example does not print the embedded example")
	}
}

func TestValidateReportsCloudRunLimits(t *testing.T) {
	cfg := strings.Replace(cliMinimalYAML, "{ commands:", "{ resources: { cpu: 3, memory: 4Gi }, commands:", 1)
	out, _, err := execute(t, "validate", "--json", writeConfig(t, cfg))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d (%v), output %s", ExitCode(err), err, out)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Problems) != 1 || got.Problems[0].Path != "workflows.app.resources.cpu" || !strings.Contains(got.Problems[0].Message, "Cloud Run") {
		t.Fatalf("problems = %+v", got.Problems)
	}
}

// With no project to select, the example carries the placeholder and says
// where the real name comes from.
func TestConfigExampleWithoutProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "config", "example")
	if err != nil || !strings.Contains(out, "# the Fugaro project; fugaro update --config-only writes your project config\nproject: example\n") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestConfigExampleUsesSelectedProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	writeProject(t, "aurora", "proj-1234")
	t.Chdir(t.TempDir())
	t.Setenv("FUGARO_PROJECT", "aurora")
	out, _, err := execute(t, "config", "example")
	if err != nil || !strings.Contains(out, "\nproject: aurora\n") || strings.Contains(out, "project: example") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if _, ps := config.Parse([]byte(out)); len(ps) > 0 {
		t.Fatalf("the example is invalid: %v", ps)
	}
	// Inside a checkout, the checkout's project selects.
	isolateProjects(t, t.TempDir())
	writeProject(t, "borealis", "proj-5678")
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: borealis\n")
	t.Chdir(root)
	if out, _, err = execute(t, "config", "example"); err != nil || !strings.Contains(out, "\nproject: borealis\n") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestValidateHintNamesProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	noProject := writeConfig(t, strings.Replace(cliMinimalYAML, "project: aurora\n", "", 1))
	out, _, err := execute(t, "validate", noProject)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "project: is required") || !strings.Contains(out, "fugaro config example") {
		t.Fatalf("generic hint: out = %q, err = %v", out, err)
	}
	writeProject(t, "aurora", "proj-1234")
	t.Setenv("FUGARO_PROJECT", "aurora")
	out, _, err = execute(t, "validate", noProject)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "project: is required") || !strings.Contains(out, "add `project: aurora`") {
		t.Fatalf("named hint: out = %q, err = %v", out, err)
	}
	if out, _, err = execute(t, "validate", "--json", noProject); !strings.Contains(out, "add `project: aurora`") {
		t.Fatalf("json: out = %q, err = %v", out, err)
	}
}

// budgetProject makes aurora's project config with the given budget block
// and returns a fugaro.yaml with agent as its agent block.
func budgetProject(t *testing.T, budget, agent string) string {
	t.Helper()
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(budget); err != nil {
		t.Fatal(err)
	}
	return writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", "agent:\n"+agent+"\nworkflows:", 1))
}

func validateProblems(t *testing.T, path string) (string, error) {
	t.Helper()
	out, _, err := execute(t, "validate", path)
	return out, err
}

func TestValidateAppliesPinsWhenBudgetOn(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		path := budgetProject(t, "budget: { mode: "+mode+", per_run_usd: 5 }\n", "  auth: api-key\n  model: sonnet")
		out, err := validateProblems(t, path)
		if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.coder") || !strings.Contains(out, "agent.models.background") {
			t.Fatalf("%s: exit %d, out %q", mode, ExitCode(err), out)
		}
	}
	path := budgetProject(t, "budget: { mode: enforce, per_run_usd: 5 }\n", "  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }")
	if out, err := validateProblems(t, path); err != nil {
		t.Fatalf("pinned config refused: %v %s", err, out)
	}
	// An ID only the project's model_prices know passes.
	path = budgetProject(t, "budget: { mode: enforce, per_run_usd: 5 }\nmodel_prices:\n  claude-acme-1: { input_per_m: 1, output_per_m: 5 }\n",
		"  auth: api-key\n  model: claude-acme-1\n  models: { background: claude-haiku-4-5 }")
	if out, err := validateProblems(t, path); err != nil {
		t.Fatalf("override ID refused: %v %s", err, out)
	}
}

func TestValidateSkipsPinsWhenBudgetOff(t *testing.T) {
	for _, budget := range []string{"", "budget: { mode: off }\n"} {
		path := budgetProject(t, budget, "  auth: api-key\n  model: sonnet")
		if out, err := validateProblems(t, path); err != nil {
			t.Fatalf("budget %q: %v %s", budget, err, out)
		}
	}
	// No project config at all: nothing to say the budget is on.
	isolateProjects(t, t.TempDir())
	if out, err := validateProblems(t, writeConfig(t, cliMinimalYAML)); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func TestValidateRefusesVertexEnforce(t *testing.T) {
	pinned := "  auth: vertex\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }"
	path := budgetProject(t, "budget: { mode: enforce, per_run_usd: 5 }\n", pinned)
	out, err := validateProblems(t, path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "Vertex budgets are not supported yet: use budget.mode observe or off") {
		t.Fatalf("enforce: exit %d, out %q", ExitCode(err), out)
	}
	path = budgetProject(t, "budget: { mode: observe }\n", pinned)
	if out, err := validateProblems(t, path); err != nil {
		t.Fatalf("observe: %v %s", err, out)
	}
}

// TestValidateDoesNotClampTheProjectLayersGovernance: the ceiling a run's
// governance keys are merged under still carries the project layer's block
// (resolvePolicy puts it in the runner's first layer), but a build that links
// no ee/ features applies none of it, so `validate` has nothing to warn about:
// the merge records no dropped value, and a warning naming one would be about
// a clamp this build does not perform (D522).
//
// What this test has to have for the assertion to mean anything is a block
// that a tagged build *would* have clamped, which is how the file below is
// written (5m in the file against the layer's 200ms): the tagged arm is the
// merge ee/governance's TestTheGovernanceWarningsSourceIsTheMergesRecord pins —
// it asserts the Ignored entry the warning is rendered from — and the
// rendering itself is TestClampWarningRendersEachKeysWords below, which does
// not need a merge at all.
func TestValidateDoesNotClampTheProjectLayersGovernance(t *testing.T) {
	layer := func(d time.Duration) *config.Governance {
		return &config.Governance{StageTimeout: config.StageTimeout{Implement: config.Duration{Duration: d}}}
	}
	lc := &localcfg.Config{Name: "aurora"}
	cfg := &config.Config{Project: "aurora", Governance: layer(5 * time.Minute),
		Layer: &config.ProjectLayer{Governance: layer(200 * time.Millisecond)}}
	_, ws, _ := budgetProblems(context.Background(), cfg, lc, lc.Providers)
	for _, w := range ws {
		if strings.HasPrefix(w.Path, "governance.") {
			t.Errorf("an untagged build warned about governance key %q: %q", w.Path, w.Message)
		}
	}
}

// TestValidateAcceptsALooserGovernanceValueWithNoLayer: with no project layer
// the ceiling carries no governance at all in a build that links no ee/
// features (D522), so a file that raises a guardrail is neither clamped nor
// named: the command reads the block, accepts it and says nothing about it.
// Refusing it is what it must never do (G2).
//
// The tagged arm — the shipped figures as the ceiling, so a repository's 20 is
// warned about as looser than 5 — is the merge
// ee/governance's TestTheGovernanceWarningsSourceIsTheMergesRecord pins (E2.3),
// and the words that warning is rendered in are
// TestClampWarningRendersEachKeysWords below.
func TestValidateAcceptsALooserGovernanceValueWithNoLayer(t *testing.T) {
	path := fileWithBudget(t, "budget: { mode: off }\n",
		"governance: { review_ratio: 20, tiers: { small: { expected_usd: 2 } } }\n", "  auth: oauth")
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate refused the file: %v\n%s%s", err, out, errOut)
	}
	if strings.Contains(errOut, "is looser than") {
		t.Errorf("an untagged build clamped a governance value it does not enforce:\n%s", errOut)
	}
}

// TestClampWarningRendersEachKeysWords: the sentence a dropped value is
// reported in, key by key, and the path it is reported under (D16, T6.1).
//
// Every governance key takes clampWarning's default branch, which is why this
// test is in the core and not in ee/governance: the rule that *finds* a looser
// governance value moved with the policy, but the words it is reported in are
// this package's, they are rendered from a policy.Ignored and nothing else, and
// untagged there is no merge to produce one — so the entries are built by hand
// here rather than driven through a command. The governance case is the one the
// CLI's ceilling notes used to render end to end
// (TestValidateDoesNotClampTheProjectLayersGovernance says why they no longer
// do).
func TestClampWarningRendersEachKeysWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		ig   policy.Ignored
		path string
		want string
	}{
		{"a looser governance value", policy.Ignored{Key: policy.KeyStageTimeoutImplement,
			Value: "5m0s", Effective: "200ms", Source: policy.SourceCeiling},
			"governance.stage_timeout.implement",
			"5m0s is looser than 200ms; the runner will use 200ms"},
		{"a looser tier expectation", policy.Ignored{Key: policy.TierKey(tier.Small, policy.KeyExpectedUSD),
			Value: "2", Effective: "1", Source: policy.SourceCeiling},
			"governance.tiers.small.expected_usd",
			"2 is looser than 1; the runner will use 1"},
		{"a value above the per-run ceiling", policy.Ignored{Key: policy.KeyPerRunUSD,
			Value: "9", Effective: "5", Source: policy.SourceCeiling},
			"budget.per_run_usd",
			"9 is above the project's ceiling of 5; the runner will use 5, or less: a run's rigor mode only lowers the ceiling"},
		{"a value above the token ceiling", policy.Ignored{Key: policy.KeyMaxRunTokens,
			Value: "900000", Effective: "500000", Source: policy.SourceCeiling},
			"agent.max_run_tokens",
			"900000 is above the project's ceiling of 500000; the runner will use 500000"},
		{"a looser mode", policy.Ignored{Key: policy.KeyMode,
			Value: "observe", Effective: "enforce", Source: policy.SourceCeiling},
			"budget.mode",
			"observe is looser than the project's enforce; the runner will use enforce"},
		{"models outside the allow-list", policy.Ignored{Key: policy.KeyAllowedModels,
			Value: "sonnet,opus", Effective: "sonnet", Source: policy.SourceCeiling},
			"budget.allowed_models",
			"[sonnet, opus] names models outside the project's allow-list; the runner will use [sonnet]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clampWarning(tc.ig)
			if got.Path != tc.path || got.Message != tc.want {
				t.Errorf("clampWarning(%+v) = %q %q, want %q %q", tc.ig, got.Path, got.Message, tc.path, tc.want)
			}
		})
	}
}

// With unreadable model_prices the one problem is reported; models are not
// also called unpriced against a table that was never the file's.
func TestBudgetProblemsSkipPinsWhenPricesAreUnreadable(t *testing.T) {
	one := func(v float64) *float64 { return &v }
	lc := &localcfg.Config{
		Name:        "aurora",
		Budget:      &localcfg.Budget{Mode: localcfg.BudgetObserve},
		ModelPrices: map[string]localcfg.ModelPrice{"sonnet": {InputPerM: one(1), OutputPerM: one(5)}},
	}
	cfg := &config.Config{Project: "aurora", Agent: config.Agent{Auth: "api-key", Model: "claude-acme-1", Models: config.ModelRoles{Background: "claude-haiku-4-5"}}}
	ps, _, _ := budgetProblems(context.Background(), cfg, lc, lc.Providers)
	if len(ps) != 1 || ps[0].Path != "model_prices" {
		t.Fatalf("problems = %v, want only the model_prices one", ps)
	}
}

// An oauth run has no gateway, so the pin rules don't apply to it.
func TestBudgetProblemsSkipPinsForOAuth(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", Budget: &localcfg.Budget{Mode: localcfg.BudgetObserve}}
	cfg := &config.Config{Project: "aurora", Agent: config.Agent{Auth: "oauth"}}
	if ps, _, _ := budgetProblems(context.Background(), cfg, lc, lc.Providers); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
	cfg.Agent.Auth = "api-key"
	if ps, _, _ := budgetProblems(context.Background(), cfg, lc, lc.Providers); len(ps) == 0 {
		t.Fatal("api-key with no models passed")
	}
}

// fileWithBudget is budgetProject with a committed fugaro.yaml block (YAML
// at top level, e.g. "budget: {...}\n") added to the file.
func fileWithBudget(t *testing.T, projectBudget, fileBlock, agent string) string {
	t.Helper()
	path := budgetProject(t, projectBudget, agent)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, fileBlock...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const pinnedAgent = "  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }"

// TestValidatePerRunWarningNamesTheDatabaseCaps: with enforce and no
// budget.per_run_usd the warning says what actually halts a run (D6): the
// database's effective per-run cap bounds it when one is set anywhere, and
// only a database with no per-run cap on any of the three sides refuses the
// lease.
func TestValidatePerRunWarningNamesTheDatabaseCaps(t *testing.T) {
	// The project config can't hold enforce with no cap (localcfg refuses
	// it), so the reachable shape is a project that observes and a file that
	// tightens the mode to enforce without adding a cap.
	path := fileWithBudget(t, "budget: { mode: observe }\n", "budget: { mode: enforce }\n", pinnedAgent)
	out, errOut, err := execute(t, "validate", path)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("a missing per-run cap is a warning: %v %q", err, out)
	}
	want := "warning: budget.per_run_usd: the mode is enforce but no per-run cap is set in the project config or here; the database's effective per-run cap still bounds the run when one is set anywhere (the repository's own, the default for repositories without one, or the project's global one), and the run halts (no_cap) only when all three are unset"
	if !strings.Contains(errOut, want) {
		t.Errorf("stderr lacks %q:\n%s", want, errOut)
	}
	if strings.Contains(errOut, "the run would halt (no_cap)\n") {
		t.Errorf("the warning still claims the run halts:\n%s", errOut)
	}
}

func TestValidateWarnsOnLooserThanCeiling(t *testing.T) {
	path := fileWithBudget(t, "budget: { mode: observe, per_run_usd: 5, max_run_tokens: 1000 }\n",
		"budget: { mode: off, per_run_usd: 20 }\n", pinnedAgent+"\n  max_run_tokens: 9000")
	out, errOut, err := execute(t, "validate", path)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("looser values are not problems: %v %q", err, out)
	}
	for _, want := range []string{
		"warning: budget.per_run_usd: 20 is above the project's ceiling of 5; the runner will use 5",
		"warning: agent.max_run_tokens: 9000 is above the project's ceiling of 1000; the runner will use 1000",
		"warning: budget.mode: off is looser than the project's observe; the runner will use observe",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	// --json keeps warnings out of problems.
	out, _, err = execute(t, "validate", "--json", path)
	if err != nil {
		t.Fatal(err)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	// 4, not 3: workflows.app also warns that the image installs no
	// language runtime (no mise.toml, .tool-versions or image.tools),
	// unrelated to the ceiling checks this test is about.
	if !got.Valid || len(got.Problems) != 0 || len(got.Warnings) != 4 {
		t.Fatalf("json = %+v", got)
	}
	// A tighter file says nothing about the ceilings. Two ceiling-unrelated
	// lines still fire — the pinned Claude model needs a stored Anthropic key
	// (the file's enforce is the effective mode), which is a note, not a
	// warning, and cliMinimalYAML's workflow installs no language runtime
	// either way — and both are stripped out by their own exact text before
	// asserting no warning at all remains, so a ceiling-unrelated warning this
	// test does not expect (a clamp, say) would still be caught.
	path = fileWithBudget(t, "budget: { mode: observe, per_run_usd: 5 }\n", "budget: { mode: enforce, per_run_usd: 2 }\n", pinnedAgent)
	_, errOut, err = execute(t, "validate", path)
	stripped := strings.Replace(errOut, "note: agent.auth: "+anthropicKeyNeeded+"\n", "", 1)
	stripped = strings.Replace(stripped, "warning: workflows.app: no mise.toml, .tool-versions or image.tools: the image installs no language runtime (docs/base-image.md)\n", "", 1)
	if err != nil || strings.Contains(stripped, "warning") {
		t.Fatalf("tightening warned: %v %q", err, stripped)
	}
}

func TestValidateModelOutsideAllowListIsProblem(t *testing.T) {
	// Even with the budget off: the allow-list bounds the models.
	path := fileWithBudget(t, "budget: { allowed_models: [claude-haiku-4-5] }\n", "", pinnedAgent)
	out, errOut, err := execute(t, "validate", "--json", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Valid || len(got.Problems) != 2 || got.Problems[0].Path != "agent.models.coder" ||
		!strings.Contains(got.Problems[0].Message, "claude-sonnet-5-5") || !strings.Contains(got.Problems[0].Message, "ceiling") {
		t.Fatalf("got %+v (stderr %q)", got, errOut)
	}
	// The file's own list narrows the ceiling's; the model it drops is a problem.
	path = fileWithBudget(t, "budget: { allowed_models: [claude-sonnet-5-5, claude-haiku-4-5] }\n",
		"budget: { allowed_models: [claude-haiku-4-5] }\n", pinnedAgent)
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.coder") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	// A file that tries to add a model gets a warning, and still fails if it uses it.
	path = fileWithBudget(t, "budget: { allowed_models: [claude-haiku-4-5] }\n",
		"budget: { allowed_models: [claude-haiku-4-5, claude-sonnet-5-5] }\n", pinnedAgent)
	out, errOut, err = execute(t, "validate", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.coder") || !strings.Contains(errOut, "budget.allowed_models") {
		t.Fatalf("exit %d: %s / %s", ExitCode(err), out, errOut)
	}
}

func TestValidateWithoutProjectConfigChecksShapeOnly(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	// Nothing of the ceiling is known, so nothing is simulated: a clamp, a
	// missing pin or an allow-list is not judged. The one warning that does
	// fire (cliMinimalYAML's workflow has no mise config) is unrelated to
	// the ceiling.
	body := strings.Replace(cliMinimalYAML, "workflows:", "agent: { auth: api-key, model: sonnet }\nbudget: { mode: enforce, per_run_usd: 20, allowed_models: [claude-haiku-4-5] }\nworkflows:", 1)
	out, errOut, err := execute(t, "validate", writeConfig(t, body))
	if err != nil || errOut != "warning: workflows.app: no mise.toml, .tool-versions or image.tools: the image installs no language runtime (docs/base-image.md)\n" {
		t.Fatalf("no project config, nothing to simulate: %v %q %q", err, out, errOut)
	}
	// The shape is still checked.
	bad := strings.Replace(cliMinimalYAML, "workflows:", "budget: { mode: loud }\nworkflows:", 1)
	if out, _, err = execute(t, "validate", writeConfig(t, bad)); ExitCode(err) != ExitUserError || !strings.Contains(out, "budget.mode") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
}

func TestValidateEnforceWithoutCapWarnsNoCap(t *testing.T) {
	// Valid in the file; with a project config the run would halt no_cap.
	path := fileWithBudget(t, "budget: { mode: off }\n", "budget: { mode: enforce }\n", pinnedAgent)
	out, errOut, err := execute(t, "validate", path)
	if err != nil || !strings.Contains(errOut, "no_cap") {
		t.Fatalf("%v out %q stderr %q", err, out, errOut)
	}
	// A cap in either place silences it.
	for _, tc := range [][2]string{
		{"budget: { mode: off, per_run_usd: 5 }\n", "budget: { mode: enforce }\n"},
		{"budget: { mode: off }\n", "budget: { mode: enforce, per_run_usd: 5 }\n"},
	} {
		path = fileWithBudget(t, tc[0], tc[1], pinnedAgent)
		if _, errOut, err = execute(t, "validate", path); err != nil || strings.Contains(errOut, "no_cap") {
			t.Fatalf("%q: %v %q", tc, err, errOut)
		}
	}
}

func TestValidateNotOnDefaultBranchSaysSo(t *testing.T) {
	isolateProjects(t, t.TempDir())
	writeProject(t, "aurora", "proj-1234")
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), cliMinimalYAML)
	testutil.Git(t, root, "add", ".")
	testutil.Git(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
	testutil.Git(t, root, "branch", "-M", "main")
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	testutil.Git(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	file := filepath.Join(root, "fugaro.yaml")
	if _, errOut, err := execute(t, "validate", file); err != nil || strings.Contains(errOut, "default branch") {
		t.Fatalf("on the default branch: %v %q", err, errOut)
	}
	testutil.Git(t, root, "checkout", "-q", "-b", "feature")
	// A file with no budget or token key has no policy to bound: no branch
	// note. The one warning present (no mise config) is unrelated to it.
	out, errOut, err := execute(t, "validate", "--json", file)
	var noPolicy validateOutput
	if err != nil || strings.Contains(errOut, "default branch") || json.Unmarshal([]byte(out), &noPolicy) != nil ||
		len(noPolicy.Warnings) != 1 || noPolicy.Warnings[0].Path != "workflows.app" {
		t.Fatalf("no policy keys: %v out %q stderr %q", err, out, errOut)
	}
	withBudget := strings.Replace(cliMinimalYAML, "workflows:", "budget: { per_run_usd: 1 }\nworkflows:", 1)
	if err := os.WriteFile(file, []byte(withBudget), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = execute(t, "validate", file)
	if err != nil || !strings.Contains(out, "is valid") ||
		!strings.Contains(errOut, "branch feature, not the default branch (main)") || !strings.Contains(errOut, "can only tighten") {
		t.Fatalf("%v out %q stderr %q", err, out, errOut)
	}
	// The JSON warning names where it is about (alongside the unrelated
	// no-mise-config one every cliMinimalYAML-based fixture here gets).
	out, _, err = execute(t, "validate", "--json", file)
	var got validateOutput
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Warnings) != 2 || got.Warnings[1].Path != "branch" {
		t.Fatalf("%v %s", err, out)
	}
	// A detached HEAD (a CI checkout) is not a branch: no note.
	testutil.Git(t, root, "checkout", "-q", "--detach")
	if _, errOut, err := execute(t, "validate", file); err != nil || strings.Contains(errOut, "default branch") {
		t.Fatalf("detached HEAD: %v %q", err, errOut)
	}
}

func TestValidateRefusesVertexEnforceFromFile(t *testing.T) {
	// The ceiling is off; the committed enforce is what the runner would refuse.
	pinned := "  auth: vertex\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }"
	path := fileWithBudget(t, "", "budget: { mode: enforce, per_run_usd: 5 }\n", pinned)
	out, _, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "Vertex budgets are not supported yet") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	path = fileWithBudget(t, "", "budget: { mode: observe }\n", pinned)
	if out, _, err = execute(t, "validate", path); err != nil {
		t.Fatalf("observe: %v %s", err, out)
	}
	// A committed observe or enforce puts the pins on a repository whose ceiling is off.
	path = fileWithBudget(t, "", "budget: { mode: observe }\n", "  auth: api-key\n  model: sonnet")
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.coder") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
}

// A committed enforce under Vertex is refused by every run (the stricter of
// the layers is enforce whatever the ceiling), so validate says so from the
// file alone, with no project config selected, in the runner's words.
func TestValidateRefusesCommittedVertexEnforceWithoutProjectConfig(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	vertex := func(budget string) string {
		return strings.Replace(cliMinimalYAML, "workflows:", "agent: { auth: vertex, model: sonnet }\n"+budget+"workflows:", 1)
	}
	out, _, err := execute(t, "validate", writeConfig(t, vertex("budget: { mode: enforce, per_run_usd: 20 }\n")))
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.auth") || !strings.Contains(out, vertexBudgetRefusal) {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	for _, budget := range []string{"budget: { mode: observe }\n", "budget: { mode: off }\n", ""} {
		if out, _, err := execute(t, "validate", writeConfig(t, vertex(budget))); err != nil {
			t.Fatalf("%q: %v %s", budget, err, out)
		}
	}
	// Enforce under another auth is fine.
	body := strings.Replace(cliMinimalYAML, "workflows:", "agent: { auth: api-key, model: sonnet }\nbudget: { mode: enforce, per_run_usd: 20 }\nworkflows:", 1)
	if out, _, err := execute(t, "validate", writeConfig(t, body)); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func TestValidateAllowListProblemLabelsLayers(t *testing.T) {
	// Disjoint lists: the message blames the file and names the ceiling's list.
	path := fileWithBudget(t, "budget: { allowed_models: [claude-sonnet-5-5, claude-haiku-4-5] }\n",
		"budget: { allowed_models: [claude-opus-5-5] }\n", pinnedAgent)
	out, _, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "allowed_models from this file: none") ||
		!strings.Contains(out, "the project's allow-list: claude-sonnet-5-5, claude-haiku-4-5") || strings.Contains(out, "from default-branch") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	// A role with no model is one problem, not two.
	noBg := "  auth: api-key\n  model: claude-sonnet-5-5"
	path = fileWithBudget(t, "budget: { mode: observe, allowed_models: [claude-sonnet-5-5] }\n", "", noBg)
	out, _, _ = execute(t, "validate", path)
	if n := strings.Count(out, "agent.models.background"); n != 1 {
		t.Fatalf("%d problems for the background model:\n%s", n, out)
	}
}

// The committed day cap is client-enforced: validate says so, and that an
// RTDB cap lower than it wins. A file without one says nothing about it.
func TestValidateWarnsPerDayIsClientEnforced(t *testing.T) {
	const want = "warning: budget.per_day_usd: per_day_usd is enforced by the runner against this repository's day counter only while the project has the shared budget (update --firebase-project, mode observe or enforce); on a project without it nothing enforces it. A lower RTDB cap wins"
	path := fileWithBudget(t, "budget: { mode: observe }\n", "budget: { per_day_usd: 10 }\n", pinnedAgent)
	out, errOut, err := execute(t, "validate", path)
	if err != nil || !strings.Contains(out, "is valid") || !strings.Contains(errOut, want) {
		t.Fatalf("err %v out %q stderr %q", err, out, errOut)
	}
	// With no project config the warning stays.
	cfg := &config.Config{Project: "aurora", Agent: config.Agent{Auth: "api-key"}, Budget: &config.Budget{PerDayUSD: 10}}
	if _, ws, _ := budgetProblems(context.Background(), cfg, nil, nil); len(ws) != 1 || ws[0].Path != "budget.per_day_usd" {
		t.Fatalf("warnings = %v", ws)
	}
	path = fileWithBudget(t, "budget: { mode: observe }\n", "budget: { per_run_usd: 1 }\n", pinnedAgent)
	if _, errOut, err = execute(t, "validate", path); err != nil || strings.Contains(errOut, "per_day_usd") {
		t.Fatalf("no day cap, but %v %q", err, errOut)
	}
}

const validateProviderBlock = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/sandbox"]
`

// inCheckoutOf makes the working directory a checkout whose origin is repo.
func inCheckoutOf(t *testing.T, repo string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/" + repo + ".git"}} {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	t.Chdir(dir)
}

// inCheckoutWithNoOrigin makes the working directory a checkout git can read
// whose origin remote is not there: the state originRepo refuses and
// providerProblems reads as "the repository is unknown here".
func inCheckoutWithNoOrigin(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	c := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Chdir(dir)
}

// TestValidateDoesNotRefuseWhatTheRunnerPrices pins the failure this design
// exists to prevent (Review Focus 10): for one fixture route and model,
// `validate` has no problem while the run's own table charges the catalog's
// rate — the same row, from the same builder.
func TestValidateDoesNotRefuseWhatTheRunnerPrices(t *testing.T) {
	const id = "deepseek/deepseek-v4.1-flash"
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n" + validateProviderBlock
	const coder = "  auth: api-key\n  models: { coder: " + id + ", reviewer: claude-sonnet-5-5 }"
	path := budgetProject(t, budget, coder)
	inCheckoutOf(t, "acme/sandbox")
	if out, _, err := execute(t, "validate", path); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("validate refused a pin the run prices: %v\n%s", err, out)
	}
	// The run's table, built from the job's environment.
	p := cliRoute()["openrouter"]
	p.AllowDataTo = []string{"acme/sandbox"}
	env, err := runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": p}, "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	s, err := runner.SpendFromEnv(func(k string) (string, bool) {
		switch k {
		case runner.BudgetModeEnv:
			return "observe", true
		case runner.ModelProvidersEnv:
			return env, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := s.Prices.Lookup(id)
	if !ok {
		t.Fatal("the run's table does not price the model validate accepted")
	}
	cat := shippedCatalog(t)
	// The end of the tier's range the run charges is the one its own row
	// says it is on (min, or max once the catalog is past its 30 days), so
	// this reads it from the row under test: the catalog's age is a date,
	// and a test written against one end only would expire with the file
	// (catalog.StaleAfter).
	bound := cat.Models[id].ZDR["input_per_m"].Min
	if m.Catalog != nil && m.Catalog.Bound == "max" {
		bound = cat.Models[id].ZDR["input_per_m"].Max
	}
	want, err := strconv.ParseFloat(bound, 64)
	if err != nil {
		t.Fatal(err)
	}
	if m.Rates.InputPerM != want {
		t.Errorf("the run charges %v per million input tokens, want the catalog's %v", m.Rates.InputPerM, want)
	}
	if m.Catalog == nil {
		t.Error("the run's row is not catalog-priced")
	}
}

func TestValidateProviderModels(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n" + validateProviderBlock
	const coder = "  auth: api-key\n  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5 }"
	// An approved repository: valid, the background model defaults to the
	// coder's, and the coder's price comes from the built-in price catalog,
	// which is a warning (the tier and the end of the range charged). The
	// clock is pinned inside the catalog's own window, because the note
	// below names the end of the range this test expects (the cheapest, by
	// the default price_policy) and a stale catalog names the other one.
	cat := shippedCatalog(t)
	pinCatalogClock(t, cat, 24*time.Hour)
	path := budgetProject(t, budget, coder)
	inCheckoutOf(t, "acme/sandbox")
	out, errOut, err := execute(t, "validate", path)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("%v\nout: %s\nerr: %s", err, out, errOut)
	}
	// The note says which tier and which end of its range is charged, and
	// for the default (cheapest) row that the charge is the cheapest of the
	// tier's endpoints, with the switch that would charge the top (P11,
	// Task 5 step 2). The endpoint count is the row's own: the note names
	// the zdr tier's endpoints, and regenerating the catalog is a release
	// step (design §7), so a test written against today's number would
	// break on a regeneration that changed no rule.
	endpoints := strconv.Itoa(cat.Models["deepseek/deepseek-v4-flash"].ZDREndpoints) + " endpoint(s)"
	for _, want := range []string{
		"warning: agent.models.coder: deepseek/deepseek-v4-flash is priced from the built-in provider price catalog: the zdr tier",
		endpoints, "the cheapest of those endpoints", "price_policy: highest", "model_prices",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("validate's catalog note lacks %q:\n%s", want, errOut)
		}
	}
	// Another repository is refused, naming the rule.
	inCheckoutOf(t, "acme/other")
	out, _, err = execute(t, "validate", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "allow_data_to") || !strings.Contains(out, "acme/other") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	// oauth cannot carry a provider key.
	path = budgetProject(t, budget, strings.Replace(coder, "api-key", "oauth", 1))
	inCheckoutOf(t, "acme/sandbox")
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.auth: api-key") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	// A provider model with the gateway off (budget mode off) is refused, as
	// the runner refuses it.
	path = budgetProject(t, "budget: { mode: off }\n"+validateProviderBlock, coder)
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "needs the budget gateway") || !strings.Contains(out, "budget.mode to enforce") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
	// Observe never refuses a call: a provider's dollars need enforce.
	path = budgetProject(t, "budget: { mode: observe }\n"+validateProviderBlock, coder)
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "budget.mode enforce") || !strings.Contains(out, "fugaro budget set --global --mode enforce") {
		t.Fatalf("observe: exit %d: %s", ExitCode(err), out)
	}
	// A vendor/model no provider serves.
	path = budgetProject(t, "budget: { mode: observe }\n", coder)
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "no provider serves it") {
		t.Fatalf("exit %d: %s", ExitCode(err), out)
	}
}

// TestValidateReportsACatalogItCannotPrice pins the CLI half of the one
// builder's contract: a catalog row the compile refuses is reported where the
// model is pinned, naming what is missing, rather than the model quietly
// having no price. The row is reachable — the loader checks that a tier prices
// *something*, not that it prices input and output — and the shipped file is
// valid, so the seam stands in for a regenerated one whose row lost a
// dimension.
func TestValidateReportsACatalogItCannotPrice(t *testing.T) {
	const id = "deepseek/deepseek-v4.1-flash"
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n" + validateProviderBlock
	const coder = "  auth: api-key\n  models: { coder: " + id + ", reviewer: claude-sonnet-5-5 }"
	cat := shippedCatalog(t)
	// The shipped row with its input price taken out of both tiers: the
	// route prices the zdr tier (require_zdr is on unless it is set false),
	// and a tier the file prices nothing but a cache dimension for is what
	// Load accepts and the compile refuses.
	e := cat.Models[id]
	delete(e.All, "input_per_m")
	delete(e.ZDR, "input_per_m")
	cat.Models[id] = e
	old := loadCatalog
	loadCatalog = func() (*catalog.File, error) { return cat, nil }
	t.Cleanup(func() { loadCatalog = old })

	path := budgetProject(t, budget, coder)
	inCheckoutOf(t, "acme/sandbox")
	out, errOut, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d: %s%s", ExitCode(err), out, errOut)
	}
	for _, want := range []string{id, "prices no input_per_m"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate does not report %q:\n%s", want, out)
		}
	}
}

// TestValidateReportsACatalogThatWillNotLoad is the other half: a file the
// loader refuses is reported by the surface, never read as "no catalog" —
// which would price every model from the embedded table alone and let the
// run go unpriced.
func TestValidateReportsACatalogThatWillNotLoad(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n" + validateProviderBlock
	const coder = "  auth: api-key\n  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5 }"
	old := loadCatalog
	loadCatalog = func() (*catalog.File, error) {
		return nil, errors.New("the built-in price catalog is malformed: version is 2: this build reads version 1")
	}
	t.Cleanup(func() { loadCatalog = old })

	path := budgetProject(t, budget, coder)
	inCheckoutOf(t, "acme/sandbox")
	out, errOut, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d: %s%s", ExitCode(err), out, errOut)
	}
	if !strings.Contains(out, "malformed") || !strings.Contains(out, "version is 2") {
		t.Errorf("validate does not report the file it could not load:\n%s", out)
	}
}

// TestValidateNotesProvidersOpenToEveryRepository: a provider with no
// allow_data_to means every repository now (design §3a, the owner's ruling),
// where it used to mean nobody, so validate names it -- from any checkout,
// and whether the list was left out or written as ["*"]. It is a warning,
// never a problem: the file is valid, and the note is there so the widening
// is seen rather than inherited.
func TestValidateNotesProvidersOpenToEveryRepository(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n"
	const coder = "  auth: api-key\n  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5 }"
	const listed = `    allow_data_to: ["acme/sandbox"]`
	notes := map[string]struct {
		block string
		repo  string
		want  string
	}{
		"absent":   {strings.Replace(validateProviderBlock, listed+"\n", "", 1), "acme/other", `providers.openrouter: has no allow_data_to, so every repository of the project may mount its key and send it code (write allow_data_to: ["*"] to mean that on purpose`},
		"wildcard": {strings.Replace(validateProviderBlock, listed, `    allow_data_to: ["*"]`, 1), "acme/other", `providers.openrouter: has allow_data_to: ["*"]: every repository`},
		"listed":   {validateProviderBlock, "acme/sandbox", ""},
	}
	for name, tc := range notes {
		// The checkout of the open spellings is a repository on no
		// allow-list: it is allowed, which is exactly the widening the note
		// is about, and the note must not be reported from one repository's
		// point of view only.
		inCheckoutOf(t, tc.repo)
		path := budgetProject(t, budget+tc.block, coder)
		out, errOut, err := execute(t, "validate", path)
		if err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("%s: %v\nout: %s\nerr: %s", name, err, out, errOut)
		}
		if tc.want == "" {
			if strings.Contains(errOut, "providers.openrouter") {
				t.Fatalf("%s: noted though it lists its repositories: %s", name, errOut)
			}
			continue
		}
		if !strings.Contains(errOut, "warning: "+tc.want) {
			t.Fatalf("%s: no note naming the provider: %s", name, errOut)
		}
	}
}

// TestValidateWarnsProviderSecretOffConvention (owner ruling of 2026-10-18):
// a provider's key is named after its entry — <provider>-api-key — and a key
// stored under any other name is WARNED about, never refused. The file stays
// valid, the key keeps working, and nothing is renamed or migrated (which is
// why this is a warning and not a problem: an installation that named its key
// `openrouter-key` yesterday must not lose its validate exit code today).
func TestValidateWarnsProviderSecretOffConvention(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n"
	const coder = "  auth: api-key\n  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5 }"
	for name, tc := range map[string]struct {
		block, want string
	}{
		// The convention's own spelling: the example block every other
		// provider test uses, which must stay silent.
		"on convention": {validateProviderBlock, ""},
		"off convention": {strings.Replace(validateProviderBlock, "secret: openrouter-api-key", "secret: openrouter-key", 1),
			`providers.openrouter.secret: its key is stored under the name openrouter-key; the convention names a provider's key after its entry in the local config, so this one's is openrouter-api-key`},
	} {
		inCheckoutOf(t, "acme/sandbox")
		path := budgetProject(t, budget+tc.block, coder)
		out, errOut, err := execute(t, "validate", path)
		if err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("%s: validate must not refuse an off-convention name: err %v\nout: %s\nerr: %s", name, err, out, errOut)
		}
		if tc.want == "" {
			if strings.Contains(errOut, "providers.openrouter.secret") {
				t.Fatalf("%s: warned though the key is on convention: %s", name, errOut)
			}
			continue
		}
		if !strings.Contains(errOut, "warning: "+tc.want) {
			t.Fatalf("%s: no warning naming the entry and the convention's name:\n%s", name, errOut)
		}
		// The two halves that make it a warning: the key keeps working (so
		// nobody renames a live secret because of this line), and the
		// conventional name is reserved for that provider either way (so
		// nobody reads "not required" as "up for grabs").
		for _, want := range []string{"nothing is renamed", "stays reserved for it either way"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("%s: the warning does not say %q:\n%s", name, want, errOut)
			}
		}
	}
	// --json carries the same half: a warning, never a problem, so `valid`
	// stays true for a file a script gates on.
	inCheckoutOf(t, "acme/sandbox")
	off := strings.Replace(validateProviderBlock, "secret: openrouter-api-key", "secret: openrouter-key", 1)
	path := budgetProject(t, budget+off, coder)
	out, _, err := execute(t, "validate", path, "--json")
	if err != nil {
		t.Fatalf("validate --json: %v", err)
	}
	var doc validateOutput
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	found := false
	for _, w := range doc.Warnings {
		if w.Path == "providers.openrouter.secret" {
			found = true
		}
	}
	if !doc.Valid || len(doc.Problems) != 0 || !found {
		t.Fatalf("valid=%v problems=%+v warnings=%+v", doc.Valid, doc.Problems, doc.Warnings)
	}
}

// TestValidateNotesAnthropicKeyNeededOffline (design
// docs/design/project-centric.md §6.1): a pin that goes direct to Anthropic
// needs the Anthropic key, and validate says so offline -- whether the secret
// has a version is a fact only the cloud knows -- naming the fix. A run whose
// pins a provider may all serve gets no such note.
func TestValidateNotesAnthropicKeyNeededOffline(t *testing.T) {
	const enforce = "budget: { mode: enforce, per_run_usd: 2 }\n"
	const observe = "budget: { mode: observe, per_run_usd: 2 }\n"
	const allProvider = "  auth: api-key\n  models: { coder: deepseek/deepseek-v4-flash, reviewer: deepseek/deepseek-v4-flash, background: deepseek/deepseek-v4-flash }"
	const claudePins = "  auth: api-key\n  models: { coder: claude-sonnet-5-5, reviewer: claude-sonnet-5-5 }"
	for _, tc := range []struct {
		name   string
		budget string
		agent  string
		repo   string
		want   string
	}{
		{"a Claude pin", enforce, claudePins, "acme/sandbox", anthropicKeyNeeded},
		{"every pin served by a provider", enforce, allProvider, "acme/sandbox", ""},
		{"a provider pin this repository may not be sent to", enforce, allProvider, "acme/other", anthropicKeyNeeded},
		// Without a gateway the key is needed whatever the pins are: every
		// call goes direct to Anthropic (config.AnthropicKeyNeeded). The
		// same rule init asks, so the two can't disagree.
		{"a Claude pin, budget observe", observe, claudePins, "acme/sandbox", anthropicKeyNoEnforce},
		{"a Claude pin, budget off", "budget: { mode: off }\n", claudePins, "acme/sandbox", anthropicKeyNoEnforce},
		{"every pin served by a provider, budget observe", observe, allProvider, "acme/sandbox", anthropicKeyNoEnforce},
		// oauth brings its own token: no key, no note, whatever the mode.
		{"oauth, budget observe", observe, "  auth: oauth\n  model: claude-sonnet-5-5", "acme/sandbox", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inCheckoutOf(t, tc.repo)
			path := budgetProject(t, tc.budget+validateProviderBlock, tc.agent)
			_, errOut, _ := execute(t, "validate", path)
			got := ""
			for _, line := range strings.Split(errOut, "\n") {
				if rest, ok := strings.CutPrefix(line, "note: agent.auth: "); ok {
					got = rest
				}
			}
			if got != tc.want {
				t.Fatalf("note = %q, want %q:\n%s", got, tc.want, errOut)
			}
		})
	}
}

// routedProjectBlock is a project config whose one route claims a Claude
// model, with allow_data_to written: D1b's safeguard as the owner writes it.
// The switch itself is what a test adds or leaves out — an absent switch is
// direct (D1b).
//
// model_prices prices the route's own ID, and the block is not valid without
// it: a pin the switch routes names that ID, the embedded catalog
// prices the canonical ID only, and a run halts on the unpriced route
// ("`anthropic/claude-sonnet-5.5` has no price" — the runner's own
// TestRoutedClaudeIsPricedFromTheRouteRowNotTheDirectRate). A test that wants
// the unpriced state writes its own block: that is the false OK validate
// printed until the routed pins were judged by the run's own checks.
const routedProjectBlock = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/sandbox"]
model_prices:
  anthropic/claude-sonnet-5.5: { input_per_m: 3, output_per_m: 15 }
`

// routedSwitchBlock is the switch that opt-in writes: the explicit list of
// canonical Claude IDs (D1a), never a pattern.
const routedSwitchBlock = `routing:
  claude:
    via: openrouter
    models: [claude-sonnet-5-5]
`

// projectRouting appends body to the isolated project config writeProject
// wrote: validate reads the operator's own file, the one `fugaro project
// policy publish` seeds the node from.
func projectRouting(t *testing.T, body string) {
	t.Helper()
	path := writeProject(t, "aurora", "proj-1234")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

// TestValidateReportsAProjectConfigItCouldNotRead is the review finding on
// validate's project config and the unresolved-offline line: selectedProjectConfig
// answers nil both for a machine where nothing selects a project config and for
// one whose config is there and was refused at load, and the routing line read
// the second as the first — "the switch is the operator's project config for
// aurora and this command has none selected" for a config that is selected and
// unreadable — and validate exited 0, so a CI job saw a green run of a config
// nobody could read.
//
// A config that is there and refused is a real problem with the operator's
// config: validate says which it is, in the words of the error the loader
// itself gave, and fails. A machine with no config selected at all is the state
// that line was written for, and it stays a note with exit 0.
func TestValidateReportsAProjectConfigItCouldNotRead(t *testing.T) {
	const claude = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\nworkflows:"
	// The switch names a Claude model no provider entry claims, which is a
	// refusal at load (localcfg's own CheckRouting): the config exists, and
	// nothing can read it.
	//
	// The config directory is deliberately deep: the loader's reason is printed
	// first and the file last (unreadableProjectConfig), so a long path is what
	// exercises the order — the place is shortened to its last elements
	// (shortPath) and still has to come out after the reason, whole. Nothing is
	// capped any more (the reason is printed through pluginwire.PrintableN with
	// its rune cap lifted, since a cap anywhere near the old 200 took the half
	// the line exists for: TestValidateKeepsTheFixOfALongLoaderReason below is
	// that finding). The depth is written here rather than left to t.TempDir()
	// because TMPDIR is not the test's to choose: the reviewed form of this test
	// passed under /tmp and failed under macOS's own per-user TMPDIR, and a path
	// that is long on every machine is what makes this one say the same thing
	// wherever it runs.
	isolateProjects(t, filepath.Join(t.TempDir(), strings.Repeat("deep-config-directory/", 8)))
	projectRouting(t, routedSwitchBlock)
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	out, errOut, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit code = %d, want %d (err %v)\n%s", ExitCode(err), ExitUserError, err, out)
	}
	if !strings.Contains(out, "could not be read") {
		t.Fatalf("a project config that could not be read is not reported:\n%s\n%s", out, errOut)
	}
	// The reason leads, with the key and the model the loader named, and the
	// file it refused is still there to be read at the end.
	if !strings.Contains(out, "routing.claude.models[0]: claude-sonnet-5-5") {
		t.Errorf("the line does not lead with the loader's own reason:\n%s", out)
	}
	if !strings.Contains(out, "aurora.yaml") {
		t.Errorf("the line does not end with the file it could not read:\n%s", out)
	}
	if strings.Contains(errOut, "none selected") {
		t.Errorf("a config that is there is reported as none selected:\n%s", errOut)
	}
	// The state that line is for: no project config at all is a note, and
	// validate still passes the file (nothing here is wrong with it).
	isolateProjects(t, t.TempDir())
	path = writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	out, errOut, err = execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate with no project config at all: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "none selected") {
		t.Fatalf("no project config at all no longer says so:\n%s", errOut)
	}
	if strings.Contains(out, "could not be read") {
		t.Errorf("a machine with no project config reports one that could not be read:\n%s", out)
	}
}

// TestValidateKeepsTheFixOfALongLoaderReason is the second half of that
// finding, one layer down: the reason led and the place followed, but the
// reason was padded into pluginwire.MaxPrintable, and the loader's own reasons
// are longer than what is left of that cap once the place has its room — and
// they end in the instruction. So the line named the key, the model and the
// defect and then cut the fix off mid-word: "…whose allow_data_to is not
// written: name the repositories whose code may b… (truncated)" for a switch
// naming a model the entry claims as anthropic/claude-haiku-4.5, and "…name the
// IDs this switch carries, one explici… (truncated)" for a switch whose models
// list is empty. Both send the operator to a sentence they cannot read.
//
// Mutation that makes this fail: cap the reason into MaxPrintable (the previous
// PrintableN(reason, MaxPrintable-len(where)-truncLead)), with a config
// directory deep enough for the place to have its say — the shape the test
// above uses, and long TMPDIRs, where the reviewed test's own failure was first
// seen.
func TestValidateKeepsTheFixOfALongLoaderReason(t *testing.T) {
	const claude = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\nworkflows:"
	const route = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/app"]
`
	for _, tc := range []struct{ name, project, want string }{
		{
			// The entry claims anthropic/claude-haiku-4.5, which is not the
			// canonical map's route ID for claude-haiku-4-5 (anthropic/
			// claude-haiku-4.5 is a spelling the map does not carry).
			name: "a switch naming an ID the entry claims its own way",
			project: `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-haiku-4.5"]
    allow_data_to: ["acme/app"]
` + "routing:\n  claude:\n    via: openrouter\n    models: [claude-haiku-4-5]\n",
			want: "with the route's ID measured",
		},
		{
			name:    "a switch whose list is empty",
			project: route + "routing:\n  claude:\n    via: openrouter\n    models: []\n",
			want:    "one explicit Claude ID per entry",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A long TMPDIR and a deep config directory both: the place is the
			// half that is cut, and this is the machine shape it was cut on.
			isolateProjects(t, filepath.Join(t.TempDir(),
				strings.Repeat("a-fairly-long-tempdir-name/", 4), strings.Repeat("deep-config-directory/", 8)))
			projectRouting(t, tc.project)
			out, errOut, err := execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1)))
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, want %d\n%s%s", ExitCode(err), ExitUserError, out, errOut)
			}
			line := ""
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "could not be read") {
					line = l
				}
			}
			if line == "" {
				t.Fatalf("the loader's refusal is not reported:\n%s%s", out, errOut)
			}
			if !strings.Contains(line, tc.want) {
				t.Errorf("the reason's instruction is gone, so the operator cannot read the fix: %q", tc.want)
			}
			if strings.Contains(line, "(truncated)") {
				t.Errorf("the line is still cut: %s", line)
			}
			// The place is still last, and still the one thing that is short:
			// the operator can find the file the reason is about.
			if !strings.HasSuffix(line, "aurora.yaml)") || !strings.Contains(line, "…/fugaro/projects/aurora.yaml") {
				t.Errorf("the place is no longer the shortened tail of the line: %s", line)
			}
		})
	}
}

// TestValidatePrintsUnresolvedOffline is plan task 1b.3's first requirement
// (design §11): the routing resolution is printed when validate can read the
// switch — the operator's own project config — and the line says "unresolved
// offline" when it cannot. A resolution validate did not resolve is never
// printed as if it had been, so the two states read differently.
func TestValidatePrintsUnresolvedOffline(t *testing.T) {
	const claude = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\nworkflows:"
	// No project config for this file's project: there is no switch to read,
	// and the line says which it is.
	isolateProjects(t, t.TempDir())
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	_, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5") || !strings.Contains(errOut, "unresolved offline") {
		t.Fatalf("want a `routing.claude: ... unresolved offline` line:\n%s", errOut)
	}
	// The project config in hand — and a checkout the route admits, since the
	// resolution is over the routes *this repository's* run carries
	// (allow_data_to; routedProjectBlock names acme/sandbox): the same pin
	// resolves, and the line is the resolution.
	isolateProjects(t, t.TempDir())
	projectRouting(t, routedProjectBlock+routedSwitchBlock)
	inCheckoutOf(t, "acme/sandbox")
	path = writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	_, errOut, err = execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "unresolved offline") {
		t.Fatalf("the switch is readable here, yet the line says unresolved:\n%s", errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
		t.Fatalf("want the resolution line:\n%s", errOut)
	}
}

// TestValidateResolvesOverTheRoutesThisCheckoutCarries pins the review finding
// on validate.go: routingProblems resolved the switch over `claimed` — the
// operator's providers with the layer's claude_models claims — and never
// filtered it by allow_data_to, although it had the checkout's repository two
// lines above. A run does filter it: internal/infra/spec.go writes
// FUGARO_MODEL_PROVIDERS through runner.ProvidersEnv (runner.ProvidersFor) over
// exactly the entries whose allow_data_to admits the repository being run, and
// the run resolves the switch over that table (internal/runner/routing.go), so
// for a checkout of a repository the only claiming entry does not admit,
// validate printed `-> openrouter, sent as …` while the run halted with "the
// project's routing switch is not usable" — the failure ProvidersFor's own doc
// names, and the false confidence doctor's check stopped giving (finding 3).
func TestValidateResolvesOverTheRoutesThisCheckoutCarries(t *testing.T) {
	// The project config's budget is enforcing and the file's credential is a
	// model API's, so a run of this repository has a gateway and the switch is
	// something that acts on it: the halt the first case reports is one the run
	// really makes (internal/runner's gatewayOn).
	const budget = "budget: { mode: enforce, per_run_usd: 5 }\n"
	const claude = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	pinned := func() string {
		return writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	}
	// routedProjectBlock admits acme/sandbox only, and this checkout is
	// acme/other: the run carries no route, so validate may not print one, and
	// the state is the halt the run halts with.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock+routedSwitchBlock)
	inCheckoutOf(t, "acme/other")
	out, errOut, err := execute(t, "validate", pinned())
	if strings.Contains(out+errOut, "-> openrouter") {
		t.Fatalf("validate printed a route this run does not carry:\n%s%s", out, errOut)
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "routing.claude") || !strings.Contains(out, "the project's routing switch is not usable") {
		t.Fatalf("exit %d, out %q: want the halt named as a `routing.claude` problem", ExitCode(err), out)
	}
	// The checkout the route admits: the same file resolves, so the refusal
	// above is about the repository and not about the switch.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock+routedSwitchBlock)
	inCheckoutOf(t, "acme/sandbox")
	_, errOut, err = execute(t, "validate", pinned())
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
		t.Fatalf("want the resolution line for the checkout the route admits:\n%s", errOut)
	}
	// A checkout whose origin cannot be read is a third state, not a resolution
	// for a repository nobody named: which repositories a run carries the route
	// for is the checkout's own answer (providerProblems passes "" to
	// CheckProviderPolicy for the same reason), so the line says unresolved.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock+routedSwitchBlock)
	t.Chdir(t.TempDir())
	_, errOut, err = execute(t, "validate", pinned())
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 unresolved") || strings.Contains(errOut, "-> openrouter") {
		t.Fatalf("want an unresolved line and no resolution:\n%s", errOut)
	}
}

// TestValidateDoesNotClaimAHaltWithoutAGateway pins the review finding on the
// unresolved-switch rule: it printed a `routing.claude` problem — a user-error
// exit — saying "A run of this repository halts with \"the project's routing
// switch is not usable\"" in every state, while a run only reaches the switch
// when its gateway is on. internal/runner's applyRouting returns before
// CheckRouting when !gatewayOn() (the mutation
// TestRoutingDoesNothingWithTheGatewayOff exists to catch), and gatewayOn is
// the budget being on (observe or enforce) and the credential's being a model
// API's (api-key or vertex). The sibling refusal in the same function is gated
// on the mode, so the two halves of one function disagreed about whether
// routing exists without a gateway (D11, design §6.2).
//
// A configuration whose runs have no gateway is not a failing one: the switch
// is inert, the pins keep their Claude IDs, and the line is a note that says
// so.
//
// The rule is the runner's own decision about the switch, not only the
// gateway's, and the later round of that review is why: the state it reported a
// certain halt in was read off gatewayOn alone, and applyRouting returns before
// CheckRouting for two more states of its own — via: auto under a Vertex
// credential (TestAutoDoesNotRouteAVertexRun), and via: auto with an Anthropic
// API key stored in the job. The four states are one table here, and the
// difference between them is why the switch reaches no run.
func TestValidateDoesNotClaimAHaltWithoutAGateway(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 5 }\n"
	const observe = "budget: { mode: observe }\n"
	// The switch that is skipped for a credential rather than for the budget:
	// via: auto is the one via whose reach the credential decides.
	const autoSwitch = "routing:\n  claude:\n    via: auto\n    models: [claude-sonnet-5-5]\n"
	pin := func(auth string) string {
		return "agent:\n  auth: " + auth + "\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	}
	// routedProjectBlock admits acme/sandbox only and the switch names the
	// model, so a run of acme/other carries no route for it: the state the rule
	// is about, in each of the four ways a run of this configuration does not
	// reach the switch. Two of them are the gateway's (D11) and two are via:
	// auto's own — the Vertex credential, which auto never routes, and the
	// key-presence question, whose answer is a secret version in the cloud and
	// no line in this file. validate read the second as a certain halt and the
	// third as a route it could print (review finding on this rule).
	for _, tc := range []struct{ name, budget, auth, switchBlock, want string }{
		{"the budget is off", "", "api-key", routedSwitchBlock, "no run of this configuration has a gateway"},
		{"the credential is not a model API's", budget, "oauth", routedSwitchBlock, "no run of this configuration has a gateway"},
		{"the credential is a Vertex one", observe, "vertex", autoSwitch, "via: auto does not route a Vertex run"},
		{"via: auto under a model API's credential", budget, "api-key", autoSwitch, "key-presence driven"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateProjects(t, t.TempDir())
			projectRouting(t, tc.budget+routedProjectBlock+tc.switchBlock)
			inCheckoutOf(t, "acme/other")
			path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", pin(tc.auth), 1))
			out, errOut, err := execute(t, "validate", path)
			if err != nil {
				t.Fatalf("validate refused a configuration whose runs do not reach the switch: %v\n%s%s", err, out, errOut)
			}
			if strings.Contains(out, "the project's routing switch is not usable") {
				t.Fatalf("the halt is asserted for a run that does not reach the switch:\n%s", out)
			}
			if !strings.Contains(errOut, "routing.claude") || !strings.Contains(errOut, tc.want) {
				t.Fatalf("want the note %q saying why the switch reaches no run here:\n%s", tc.want, errOut)
			}
		})
	}
}

// TestValidateDoesNotPrintARouteNoVertexRunTakes is the other direction of the
// same finding: the switch here *does* resolve — routedProjectBlock's entry
// claims the model and admits this checkout — and the run is a Vertex one, so
// auto leaves its pins at Claude's own IDs and the run takes no route at all.
// validate printed `-> openrouter, sent as …` for it, which is a resolution no
// run of that configuration takes: the credentials decide via: auto and this
// command reads the file's credential, so there is nothing conditional left to
// say here (internal/runner's own log line for the same skip says the pins keep
// Claude's own IDs).
func TestValidateDoesNotPrintARouteNoVertexRunTakes(t *testing.T) {
	isolateProjects(t, t.TempDir())
	// observe: an enforcing Vertex budget is refused on its own (agent.auth),
	// and this test is about the switch, not about that refusal.
	projectRouting(t, "budget: { mode: observe }\n"+routedProjectBlock+"routing:\n  claude:\n    via: auto\n    models: [claude-sonnet-5-5]\n")
	inCheckoutOf(t, "acme/sandbox")
	claude := "agent:\n  auth: vertex\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate refused a Vertex run's configuration: %v\n%s%s", err, out, errOut)
	}
	if strings.Contains(out+errOut, "-> openrouter") {
		t.Fatalf("validate printed a route no Vertex run takes (via: auto never routes one):\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> direct") || !strings.Contains(errOut, "via: auto does not route a Vertex run") {
		t.Fatalf("want the line to say the pins stay direct and why:\n%s", errOut)
	}
}

// TestValidateJudgesTheSwitchOverTheModelsItNames pins the review finding the
// 216-case matrix of validate against the run's own steps found: this command
// asked the switch only about the pins. The run asks it about the list —
// applyRouting runs config.CheckRouting(block, r.modelProviders(), "routing")
// over every ID in routing.claude.models before it looks at a pin, and a switch
// it cannot carry out halts the run there — so a project whose switch names
// claude-sonnet-5-5 while the only entry claiming it is written for another
// repository halted every run of acme/sandbox, and validate printed
// "claude-opus-5-5 -> direct (no switch names it: D1b's default)" and exited 0.
// A file with no Claude pin at all was worse: claudePins being empty returned
// before this command had read the switch.
//
// The project config's own loader does not catch it either: localcfg holds the
// switch to CheckRouting over c.Providers, the operator's table unfiltered by
// allow_data_to, and an entry this repository's run does not carry is invisible
// there (runner.ProvidersFor).
//
// The rows are the states the halt was found in — the pin a different model,
// the same under a Vertex credential, no Claude pin at all — and the two where
// the same sentence has to stay a note: via: auto's reach hangs on the key the
// job holds, and a switch with no gateway reaches no run. Every row also counts
// the halt, so a halt said twice (the pin rule and the switch rule judging one
// model) is a failure: the dedupe the reviewer asked for on the resolution
// lines holds for the halt too.
func TestValidateJudgesTheSwitchOverTheModelsItNames(t *testing.T) {
	// The route the switch names, written for a repository that is not this
	// checkout: over the routes a run of acme/sandbox carries, no entry claims
	// the model the switch names at all. openrouter-deepseek is the second
	// entry, the one this checkout's run does carry (validateProviderBlock's
	// deepseek/* under another name, with the catalog-eligible base URL that
	// prices it) — in one providers block, since a second `providers:` key
	// makes a YAML document with two of them.
	const otherRoute = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/other"]
  openrouter-deepseek:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: deepseek-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/sandbox"]
`
	const namesSonnet = "routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n"
	const autoSonnet = "routing:\n  claude:\n    via: auto\n    models: [claude-sonnet-5-5]\n"
	const enforce = "budget: { mode: enforce, per_run_usd: 5 }\n"
	const observe = "budget: { mode: observe }\n"
	// A pin of another Claude model: what the repro printed as a resolution.
	claudePin := func(auth, model string) string {
		return "agent:\n  auth: " + auth + "\n  model: " + model + "\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	}
	// No Claude pin at all: the pins are provider routes the same project
	// config claims (openrouter-deepseek), so the file is valid on every other
	// count and the switch's own list is the only thing left to judge.
	providerPins := func(auth string) string {
		return "agent:\n  auth: " + auth + "\n  models: { coder: deepseek/deepseek-v4.1-flash, reviewer: deepseek/deepseek-v4.1-flash }\nworkflows:"
	}
	for _, tc := range []struct {
		name    string
		project string
		agent   string
		halt    bool
		halts   int
		want    string
	}{
		{
			name:    "the pins are another model",
			project: enforce + otherRoute + namesSonnet,
			agent:   claudePin("api-key", "claude-opus-5-5"),
			halt:    true, halts: 1,
			want: "A run of this repository halts with \"the project's routing switch is not usable\"",
		},
		{
			name:    "the same switch under a Vertex credential",
			project: observe + otherRoute + namesSonnet,
			agent:   claudePin("vertex", "claude-opus-5-5"),
			halt:    true, halts: 1,
			want: "A run of this repository halts with \"the project's routing switch is not usable\"",
		},
		{
			name:    "no pin is a Claude model",
			project: enforce + otherRoute + namesSonnet,
			agent:   providerPins("api-key"),
			halt:    true, halts: 1,
			want: "A run of this repository halts with \"the project's routing switch is not usable\"",
		},
		{
			name:    "the pin names the model too",
			project: enforce + otherRoute + namesSonnet,
			agent:   claudePin("api-key", "claude-sonnet-5-5"),
			halt:    true, halts: 1,
			want: "is named by routing.claude.models and does not resolve",
		},
		{
			name:    "via: auto, whose reach hangs on the job's key",
			project: enforce + otherRoute + autoSonnet,
			agent:   claudePin("api-key", "claude-opus-5-5"),
			want:    "via: auto is key-presence driven",
		},
		{
			name:    "a switch no run of this configuration has a gateway for",
			project: otherRoute + namesSonnet,
			agent:   claudePin("api-key", "claude-opus-5-5"),
			want:    "no run of this configuration has a gateway",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateProjects(t, t.TempDir())
			projectRouting(t, tc.project)
			inCheckoutOf(t, "acme/sandbox")
			path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", tc.agent, 1))
			out, errOut, err := execute(t, "validate", path)
			if !tc.halt {
				if err != nil {
					t.Fatalf("validate refused a configuration no run of which halts: %v\n%s%s", err, out, errOut)
				}
				if !strings.Contains(errOut, tc.want) {
					t.Fatalf("want the note %q:\n%s", tc.want, errOut)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d (%v), want %d: the switch's own list was not judged\n%s%s", ExitCode(err), err, ExitUserError, out, errOut)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want the halt %q in the problems:\n%s", tc.want, out)
			}
			if got := strings.Count(out+errOut, "the project's routing switch is not usable"); got != tc.halts {
				t.Fatalf("the halt is printed %d times, want %d:\n%s%s", got, tc.halts, out, errOut)
			}
			// The halt is the only problem this file has: every other count
			// of it is valid, so a problem beside it (a pin the run prices, a
			// claim nobody carries) is this test's fixture drifting rather
			// than the rule under it — and a row that passed on somebody
			// else's refusal would not be testing this rule at all.
			if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 1 {
				t.Fatalf("the halt is one of %d problems, want the only one:\n%s", len(lines), out)
			}
		})
	}
}

// TestValidatePointsAtAllowDataToWhenTheEntryIsThere is the review finding on
// the switch-list line "no provider entry claims it". The rule is asked of the
// routes a run of this repository carries — runner.ProvidersFor keeps the
// entries whose allow_data_to admits the repository and drops the rest — so an
// entry that claims the model for *other* repositories is not in that table at
// all, and the rule cannot tell that state from a model nobody claims. The
// operator reading the first sentence goes and writes a route entry that already
// exists; the fix is one word in its allow_data_to (and then update --repo, since
// the list a run reads is built into its job at deploy time).
//
// config.CheckRouting judges one table and cannot know which of the two it is
// holding; validate holds both, and this is the clause only a caller with both
// can write. The same fixture in the checkout the entry is written for resolves,
// which is the contrast that makes the clause about the filter rather than about
// the switch.
func TestValidatePointsAtAllowDataToWhenTheEntryIsThere(t *testing.T) {
	const project = "budget: { mode: enforce, per_run_usd: 5 }\n" + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/sandbox"]
` + "routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n"
	// The pins are another Claude model, so the switch's own list is the whole
	// of the question (the pin rule judges a model the file pins itself).
	const agent = "agent:\n  auth: api-key\n  model: claude-opus-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	run := func(t *testing.T, repo string) (string, string, error) {
		t.Helper()
		isolateProjects(t, t.TempDir())
		projectRouting(t, project)
		inCheckoutOf(t, repo)
		return execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", agent, 1)))
	}
	out, _, err := run(t, "acme/app")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want %d: a switch no run of this repository can carry out is a halt\n%s", ExitCode(err), ExitUserError, out)
	}
	// The rule's own sentence is still there — this clause is added to it, not
	// instead of it — and the entry, its list and the repository are named
	// beside it.
	if !strings.Contains(out, "no provider entry claims it") {
		t.Fatalf("the rule's own sentence is gone:\n%s", out)
	}
	for _, want := range []string{"openrouter", "allow_data_to", `"acme/sandbox"`, "acme/app", "fugaro update --repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("the line does not name %q, so the operator is sent to write an entry that is there:\n%s", want, out)
		}
	}
	// The entry it names is the one that claims the model, not any entry.
	if !strings.Contains(out, "serves anthropic/claude-sonnet-5.5") {
		t.Errorf("the line does not say what the entry serves:\n%s", out)
	}
	// The contrast: in the checkout that entry is written for, the switch
	// resolves and the file is valid.
	if out, errOut, err := run(t, "acme/sandbox"); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("the clause's own fixture is refused where it resolves: %v\n%s%s", err, out, errOut)
	}
	// The clause is empty for the states it is not about, which the CLI path
	// cannot all reach (a project config whose own table does not resolve the
	// model at all is refused at load, before this rule is asked).
	sw := &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}
	own := map[string]config.ModelProvider{"openrouter": {Models: []string{"anthropic/claude-sonnet-5.5"}, AllowDataTo: []string{"acme/sandbox"}}}
	for _, tc := range []struct {
		name, model, repo string
		own, routes       map[string]config.ModelProvider
	}{
		{"no repository named", "claude-sonnet-5-5", "", own, nil},
		{"the switch does not name the model", "claude-opus-5-5", "acme/app", own, nil},
		{"nothing claims it at all", "claude-sonnet-5-5", "acme/app", map[string]config.ModelProvider{}, nil},
		{"the run carries the entry", "claude-sonnet-5-5", "acme/app", own, own},
		{"the entry wrote no allow_data_to", "claude-sonnet-5-5", "acme/app",
			map[string]config.ModelProvider{"openrouter": {Models: []string{"anthropic/claude-sonnet-5.5"}}}, nil},
	} {
		if got := switchListFilteredClause(sw, tc.model, tc.repo, tc.own, tc.routes); got != "" {
			t.Errorf("%s: clause = %q, want empty", tc.name, got)
		}
	}
}

// TestValidateDoesNotJudgeTheSwitchOverARepositoryNobodyNamed pins the state
// this rule learned to leave alone. With no origin remote there is no repository
// to filter the routes by, and runner.ProvidersFor keeps only the entries
// written for every repository — so judging the switch's own list over that
// table read a model served by a repository-scoped entry as unresolvable, and
// the same project config and file that exit 0 in an acme/sandbox checkout
// exited 1 here, asserting a halt no run necessarily makes, in a run whose
// provider lines say allow_data_to was not checked at all.
func TestValidateDoesNotJudgeTheSwitchOverARepositoryNobodyNamed(t *testing.T) {
	// The switch's route is written for acme/sandbox, and the pins are another
	// Claude model, so the switch's own list is the whole of the question.
	const project = "budget: { mode: enforce, per_run_usd: 5 }\n" + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/sandbox"]
` + "routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n"
	const agent = "agent:\n  auth: api-key\n  model: claude-opus-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"

	run := func(t *testing.T, checkout func(*testing.T)) (string, string, error) {
		t.Helper()
		isolateProjects(t, t.TempDir())
		projectRouting(t, project)
		checkout(t)
		return execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", agent, 1)))
	}
	// In the checkout the route is written for, the switch resolves: valid.
	if out, errOut, err := run(t, func(t *testing.T) { inCheckoutOf(t, "acme/sandbox") }); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("validate refused the configuration in the checkout its route is written for: %v\n%s%s", err, out, errOut)
	}
	// Without an origin the repository is unknown, so the switch's list is not
	// judged: the line is a note, and the file stays valid.
	out, errOut, err := run(t, inCheckoutWithNoOrigin)
	if err != nil {
		t.Fatalf("validate refused a configuration whose repository it cannot read: %v\n%s%s", err, out, errOut)
	}
	if strings.Contains(out+errOut, "the project's routing switch is not usable") {
		t.Fatalf("validate claimed a halt no run necessarily makes:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "unresolved") || !strings.Contains(errOut, "no origin remote") {
		t.Fatalf("want an unresolved note naming the origin:\n%s%s", out, errOut)
	}
}

// TestValidateAgreesWithTheRunAboutTheSwitch is the same rule as a table over
// the states, with the answer computed from the runner's own steps rather than
// written down: for each cell the expectation comes from
// runner.RoutingInertReason over runner.GatewayOn — does any run of this
// configuration reach the switch? — and from config.CheckRouting over
// runner.ProvidersFor's table (the routes a run of this checkout carries) —
// would the switch carry out? Those two are what applyRouting asks before it
// halts, so a cell that drifts is a failure whichever side moved: validate
// printing a halt no run makes, or a run halting where validate exits 0.
//
// The cells are the axes the run's own decision has: the budget mode, the
// credential, the via, and whether the file has a Claude pin at all. Two
// combinations are left out because they are refused for reasons that are not
// this rule's: a Vertex credential under an enforcing budget is refused on its
// own (agent.auth), and a provider-route pin under an observing budget is
// refused by the gateway rule, which needs enforce.
func TestValidateAgreesWithTheRunAboutTheSwitch(t *testing.T) {
	const canonical = "claude-sonnet-5-5"
	// The one route the switch names, written for another repository: over the
	// routes a run of acme/sandbox carries it is dropped (allow_data_to), which
	// is the halt. openrouter-deepseek is there for the cells whose pins are
	// provider routes: it claims deepseek/* for this checkout, at the base URL
	// the catalog prices.
	projectBlock := func(mode, via string) string {
		b := ""
		switch mode {
		case "observe":
			b += "budget: { mode: observe }\n"
		case "enforce":
			b += "budget: { mode: enforce, per_run_usd: 5 }\n"
		}
		return b + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/other"]
  openrouter-deepseek:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: deepseek-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/sandbox"]
routing:
  claude:
    via: ` + via + `
    models: [` + canonical + `]
`
	}
	// The operator's providers as the run holds them, for the expectation: the
	// same table projectBlock writes, in the shape runner.ProvidersFor takes.
	claims := map[string]config.ModelProvider{"openrouter": {
		Kind:        config.ModelProviderKind,
		Models:      []string{"anthropic/claude-sonnet-5.5"},
		AllowDataTo: []string{"acme/other"},
	}}
	// The pins: another Claude model (the repro's), or none at all — provider
	// routes this checkout carries, so the switch's own list is all that is
	// left to judge.
	pins := map[string]string{
		"another Claude model": "agent:\n  auth: %s\n  model: claude-opus-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:",
		"no Claude pin":        "agent:\n  auth: %s\n  models: { coder: deepseek/deepseek-v4.1-flash, reviewer: deepseek/deepseek-v4.1-flash }\nworkflows:",
	}
	for _, mode := range []string{"", "observe", "enforce"} {
		for _, auth := range []string{"api-key", "vertex", "oauth"} {
			for _, via := range []string{"openrouter", "auto"} {
				for _, pin := range []string{"another Claude model", "no Claude pin"} {
					// Two combinations are refused before routing is read, and
					// neither is this rule's: a Vertex credential under an
					// enforcing budget (agent.auth), and a provider-route pin
					// anywhere but an enforcing api-key run (the gateway rule
					// and the credential rule both judge it).
					if auth == "vertex" && mode == "enforce" {
						continue
					}
					if pin == "no Claude pin" && !(auth == "api-key" && mode == "enforce") {
						continue
					}
					name := strings.Join([]string{mode, auth, via, pin}, "/")
					t.Run(name, func(t *testing.T) {
						// The run's own steps: does any run reach the switch,
						// and does the switch carry out over this checkout's
						// routes?
						sw := &config.RoutingClaude{Via: via, Models: []string{canonical}}
						gateway := runner.GatewayOn(auth, mode)
						acts := runner.RoutingInertReason(sw, auth, gateway, false) == runner.RoutingActs &&
							runner.RoutingInertReason(sw, auth, gateway, true) == runner.RoutingActs
						halts := acts && len(config.CheckRouting(&config.Routing{Claude: sw}, runner.ProvidersFor(claims, "acme/sandbox"), "routing")) > 0

						isolateProjects(t, t.TempDir())
						projectRouting(t, projectBlock(mode, via))
						inCheckoutOf(t, "acme/sandbox")
						path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", fmt.Sprintf(pins[pin], auth), 1))
						out, errOut, err := execute(t, "validate", path)
						// The problems only: the keyless note quotes the halt
						// phrase while saying which of the two runs halts, and
						// it is the note, not the verdict.
						halted := strings.Contains(out, "the project's routing switch is not usable")
						if halts != (ExitCode(err) == ExitUserError) || halts != halted {
							t.Fatalf("the run halts = %v, validate exits %d with the halt = %v:\n%s%s", halts, ExitCode(err), halted, out, errOut)
						}
						// Where the switch does not reach every run, the line is
						// still printed — as the note that says which runs it
						// does reach, or that none does.
						if !halts && !strings.Contains(errOut, "routing.claude") {
							t.Fatalf("a switch that names an unresolvable model is not spoken of at all:\n%s%s", out, errOut)
						}
					})
				}
			}
		}
	}
}

// TestValidateSaysWhichRunTheJobKeyDecides is the key-presence half: via: auto
// under an api-key credential routes when the job has no Anthropic API key
// stored and keeps the pins direct when it has one, and whether one is stored
// is a secret version in the cloud — no line in this file, and nothing this
// command reads. So neither the resolution nor a halt may be printed as the
// run's: the line names both runs, and it is a note rather than the problem the
// rule used to raise for the keyless one alone.
func TestValidateSaysWhichRunTheJobKeyDecides(t *testing.T) {
	// The mode is enforcing so that the keyless run really does take the route
	// (the provider gate below is the observe state's, and this test is about
	// which run validate may speak for).
	isolateProjects(t, t.TempDir())
	projectRouting(t, "budget: { mode: enforce, per_run_usd: 5 }\n"+routedProjectBlock+"routing:\n  claude:\n    via: auto\n    models: [claude-sonnet-5-5]\n")
	inCheckoutOf(t, "acme/sandbox")
	claude := "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate refused a configuration whose runs depend on a key this command cannot read: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5 with no Anthropic API key stored in the job, and -> direct with one") {
		t.Fatalf("want the resolution to name both runs:\n%s", errOut)
	}
	// Under observe the keyless run halts at the provider gate (a routed pin
	// needs budget.mode enforce), and the run with a key stored does not take
	// the route at all: a halt that depends on the same unread fact is a note,
	// never this command's user-error verdict.
	isolateProjects(t, t.TempDir())
	projectRouting(t, "budget: { mode: observe }\n"+routedProjectBlock+"routing:\n  claude:\n    via: auto\n    models: [claude-sonnet-5-5]\n")
	out, errOut, err = execute(t, "validate", path)
	if err != nil {
		t.Fatalf("validate refused a configuration whose keyless runs would halt: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "needs budget.mode enforce") || !strings.Contains(errOut, "no Anthropic API key stored takes the switch's route and halts there") {
		t.Fatalf("want a note naming the keyless run's halt at the provider gate:\n%s", errOut)
	}
}

// TestValidateSaysARoutedPinHaltsUnderAnObservingBudget pins the review finding
// on the resolution: validate read gatewayOn as "the run takes the route" and
// printed the resolution for an observe-mode project, exiting 0, while a run of
// it halts — applyRouting rewrites the pin to the route's own ID and
// internal/runner's checkProviderModels then asks config.CheckProviderGateway
// about exactly that pin, which refuses one outside enforce ("is served by a
// provider, which needs budget.mode enforce: observe never refuses a call and a
// provider's calls are real dollars"). The rule the run answers with is asked
// here, over the agent the switch produces.
func TestValidateSaysARoutedPinHaltsUnderAnObservingBudget(t *testing.T) {
	isolateProjects(t, t.TempDir())
	// The switch resolves here: the entry claims the model and admits this
	// checkout, so the resolution line is printed — with the halt beside it.
	projectRouting(t, "budget: { mode: observe }\n"+routedProjectBlock+routedSwitchBlock)
	inCheckoutOf(t, "acme/sandbox")
	claude := "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1))
	out, errOut, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want a user error: an observe-mode run of this configuration halts at its routed pin\n%s%s", ExitCode(err), out, errOut)
	}
	if !strings.Contains(out, "agent.models.coder") || !strings.Contains(out, "needs budget.mode enforce") {
		t.Fatalf("want the halt named at the pin the switch rewrote, with the rule's own reason:\n%s", out)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
		t.Fatalf("the resolution is not printed beside it:\n%s", errOut)
	}
	// Under enforce the same configuration is what the switch is for: the
	// refused pin is the observation mode's own rule and nothing else here
	// fails.
	isolateProjects(t, t.TempDir())
	projectRouting(t, "budget: { mode: enforce, per_run_usd: 5 }\n"+routedProjectBlock+routedSwitchBlock)
	if out, errOut, err := execute(t, "validate", path); err != nil {
		t.Fatalf("validate refused the enforcing configuration a routed pin is for: %v\n%s%s", err, out, errOut)
	}
}

// TestValidateRefusesARoutedPinTheRunCannotPrice pins the false OK the review
// found on the routed-pin block. The block asked two of the run's checks of the
// pins the switch produced — config.CheckProviderAuth and
// config.CheckProviderGateway — and left out config.CheckProviderPolicy, whose
// price rule is the one the pin then trips: applyRouting rewrites the pin to the
// route's own ID, the embedded catalog prices the canonical ID only, and nothing
// but the owner's model_prices prices "anthropic/claude-sonnet-5.5"
// (internal/runner's own TestRoutedClaudeIsPricedFromTheRouteRowNotTheDirectRate).
// So validate printed "claude-sonnet-5-5 -> openrouter, sent as
// anthropic/claude-sonnet-5.5" and exited 0 while every run of the configuration
// halted in checkProviderModels with "`anthropic/claude-sonnet-5.5` has no
// price: add it under model_prices in the project config".
//
// The two cells are the two reaches a routed pin has: via: openrouter, whose
// runs all take the route (a halt), and via: auto, whose reach hangs on the key
// the job holds — the line names the defect there and the run that halts, which
// is the sibling note rule for the same unread fact.
func TestValidateRefusesARoutedPinTheRunCannotPrice(t *testing.T) {
	const enforce = "budget: { mode: enforce, per_run_usd: 5 }\n"
	const claude = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	// routedProjectBlock's route with its model_prices row removed: the ID the
	// pin is rewritten to is then priced by nothing.
	unpriced := strings.Replace(routedProjectBlock, "model_prices:\n  anthropic/claude-sonnet-5.5: { input_per_m: 3, output_per_m: 15 }\n", "", 1)
	if unpriced == routedProjectBlock {
		t.Fatal("the model_prices row is not where this test removes it: routedProjectBlock moved")
	}
	const autoSwitch = "routing:\n  claude:\n    via: auto\n    models: [claude-sonnet-5-5]\n"
	for _, tc := range []struct {
		name, switchBlock, want string
		halt                    bool
	}{
		{"via: openrouter", routedSwitchBlock, "`anthropic/claude-sonnet-5.5` has no price", true},
		{"via: auto, whose reach is the job's key", autoSwitch, "via: auto is key-presence driven", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateProjects(t, t.TempDir())
			projectRouting(t, enforce+unpriced+tc.switchBlock)
			inCheckoutOf(t, "acme/sandbox")
			out, errOut, err := execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1)))
			if !tc.halt {
				if err != nil {
					t.Fatalf("validate refused a state whose halt hangs on the job's key: %v\n%s%s", err, out, errOut)
				}
				if !strings.Contains(errOut, "has no price") || !strings.Contains(errOut, tc.want) {
					t.Fatalf("the line does not name the price defect and the run that halts:\n%s%s", out, errOut)
				}
				return
			}
			if ExitCode(err) != ExitUserError || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d: want the run's own refusal %q\n%s%s", ExitCode(err), tc.want, out, errOut)
			}
			if !strings.Contains(out, "agent.models.coder") {
				t.Fatalf("the refusal does not name the pin the switch rewrote:\n%s", out)
			}
			if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> openrouter, sent as anthropic/claude-sonnet-5.5") {
				t.Fatalf("the resolution is not printed beside it:\n%s", errOut)
			}
		})
	}
}

// TestValidateAgreesWithTheRunAboutTheRoutedPins is the other half of the matrix
// TestValidateAgreesWithTheRunAboutTheSwitch holds: that one is the switch's own
// list and the question "may the run resolve it at all", and this one is the
// pins the switch produced, which the run judges with its own checks once
// applyRouting has rewritten them. The expectation is computed from those checks
// — config.CheckRouting over runner.ProvidersFor's table for the switch,
// then config.CheckProviderPolicy, config.CheckProviderAuth,
// config.CheckProviderGateway and (with the gateway on) config.CheckPins over
// the agent routedAgent produces — rather than written down, so a cell that
// drifts is a failure whichever side moved. validate asked two of the five, and
// a route ID with no price is exactly what the missing one caught.
//
// The axes are the route's price (the owner's model_prices row, or none) and the
// via, which is the difference between a halt and a note: via: auto's reach
// hangs on the key the job holds, so a defect under it is a note naming both
// runs. A variant or an alias needs no cell: a route's claim is judged by
// config.CheckClaudeClaim and config.CheckModelPattern when the project config
// loads, and the claim the resolver returns is the entry's own — a layer's
// spelling of it never displaces it (measured, and the loader's refusal is what
// validate prints when the entry's own claim is the variant).
func TestValidateAgreesWithTheRunAboutTheRoutedPins(t *testing.T) {
	const canonical, routed = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"
	const mode = "enforce"
	const claude = "agent:\n  auth: api-key\n  model: " + canonical + "\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	claims := map[string]config.ModelProvider{"openrouter": {
		Kind:        config.ModelProviderKind,
		Models:      []string{routed},
		AllowDataTo: []string{"acme/sandbox"},
	}}
	agent := config.Agent{Auth: "api-key", Model: canonical, Models: config.ModelRoles{Background: "claude-haiku-4-5"}}
	for _, price := range []string{"priced", "unpriced"} {
		for _, via := range []string{config.RouteOpenRouter, config.RouteAuto} {
			t.Run(price+"/"+via, func(t *testing.T) {
				overrides := pricing.Overrides{}
				project := "budget: { mode: enforce, per_run_usd: 5 }\n" + routedProjectBlock
				if price == "unpriced" {
					project = strings.Replace(project, "model_prices:\n  anthropic/claude-sonnet-5.5: { input_per_m: 3, output_per_m: 15 }\n", "", 1)
				} else {
					overrides[routed] = pricing.Rates{InputPerM: 3, OutputPerM: 15}
				}
				project += "routing:\n  claude:\n    via: " + via + "\n    models: [" + canonical + "]\n"
				// The run's own steps, in the run's own order: the switch's list
				// first (applyRouting refuses there before it looks at a pin),
				// then the pins it rewrote.
				prices, err := config.PriceTable(claims, overrides, nil, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				sw := &config.RoutingClaude{Via: via, Models: []string{canonical}}
				gateway := runner.GatewayOn("api-key", mode)
				// A run halts here only when the switch reaches it: every run of
				// this configuration for openrouter, and the keyless one for auto.
				always := runner.RoutingInertReason(sw, "api-key", gateway, false) == runner.RoutingActs &&
					runner.RoutingInertReason(sw, "api-key", gateway, true) == runner.RoutingActs
				keyless := !always && runner.RoutingInertReason(sw, "api-key", gateway, false) == runner.RoutingActs
				if !always && !keyless {
					t.Fatalf("the switch reaches no run here (%v/%v): this cell is not this rule's", always, keyless)
				}
				routes := runner.ProvidersFor(claims, "acme/sandbox")
				halts := len(config.CheckRouting(&config.Routing{Claude: sw}, routes, "routing")) > 0
				if !halts {
					a, paths := routedAgent(agent, sw, routes)
					ps := config.CheckProviderPolicy(a, nil, "acme/sandbox", routes, prices)
					ps = append(ps, config.CheckProviderAuth(a, routes)...)
					ps = append(ps, config.CheckProviderGateway(a, config.EffectiveBackground(a, routes), routes, mode)...)
					if gateway {
						ps = append(ps, config.CheckPins(a, prices)...)
					}
					for _, q := range ps {
						if slices.Contains(paths, q.Path) {
							halts = true
						}
					}
				}

				isolateProjects(t, t.TempDir())
				projectRouting(t, project)
				inCheckoutOf(t, "acme/sandbox")
				out, errOut, err := execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1)))
				// The run halts in every state of this cell, so it is a problem
				// where it halts every run, and a note where the run that halts
				// is the keyless one (the via: auto rule).
				wantExit := 0
				if halts && always {
					wantExit = ExitUserError
				}
				if ExitCode(err) != wantExit {
					t.Fatalf("exit %d, want %d (the run halts = %v, always = %v):\n%s%s", ExitCode(err), wantExit, halts, always, out, errOut)
				}
				if halts {
					// The defect is named either way, at the pin the switch rewrote.
					if !strings.Contains(out+errOut, "agent.models.coder") {
						t.Fatalf("the halt the run makes is not named at its pin:\n%s%s", out, errOut)
					}
					if !always && !strings.Contains(errOut, "via: auto is key-presence driven") {
						t.Fatalf("the keyless note does not say which run halts:\n%s", errOut)
					}
				} else if strings.Contains(out, "has no price") {
					t.Fatalf("validate refused a configuration the run prices:\n%s%s", out, errOut)
				}
			})
		}
	}
}

// TestValidateJudgesTheAllowListOfThePinsTheRunHas is the review finding on
// budgetProblems: it asked config.CheckAllowed of the pins the file wrote and
// derived the background pin before routing, while the run asks both of the pins
// applyRouting left. Two configurations a run admits were refused here:
//
//   - the allow-list spells the model as the route's own ID
//     (allowed_models: ["anthropic/claude-sonnet-5.5"]) while the file pins the
//     canonical ID: applyRouting rewrites that pin to the route's ID, which the
//     run admits by its own name, and validate reported "claude-sonnet-5-5 is
//     not an allowed model";
//   - the file names no background at all: the run derives it from the *routed*
//     pins over the routes it carries (checkProviderModels sets
//     agent.models.background before checkBudget reads the allow-list), so the
//     pin is the route's ID — on the list — and validate reported
//     "agent.models.background names no model, but the run has an allow-list".
//
// Both are the same fixture, because both are the same run: the file pins the
// canonical coder, names no background, and the switch routes that coder to the
// ID the allow-list spells.
//
// The two vias are the two reaches: with via: openrouter every run of the
// configuration takes the route and the pins judged are those, so validate has
// nothing to say; under via: auto the run with a stored Anthropic API key keeps
// the pins direct and *does* halt on the file's own spelling, a fact that hangs
// on the job's key, so the line is a note naming which run halts (the owner's
// ruling on the sibling no-key case) and validate still exits 0.
func TestValidateJudgesTheAllowListOfThePinsTheRunHas(t *testing.T) {
	const canonical, routed = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"
	const enforce = "budget: { mode: enforce, per_run_usd: 5, allowed_models: [\"" + routed + "\"] }\n"
	// The file: the canonical coder the switch routes, and no background model.
	const claude = "agent:\n  auth: api-key\n  model: " + canonical + "\nworkflows:"
	for _, via := range []string{config.RouteOpenRouter, config.RouteAuto} {
		t.Run("via: "+via, func(t *testing.T) {
			isolateProjects(t, t.TempDir())
			projectRouting(t, enforce+routedProjectBlock+"routing:\n  claude:\n    via: "+via+"\n    models: ["+canonical+"]\n")
			inCheckoutOf(t, "acme/sandbox")
			out, errOut, err := execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claude, 1)))
			if err != nil {
				t.Fatalf("validate refused a configuration the run admits (via: %s): %v\n%s%s", via, err, out, errOut)
			}
			// The two lines the file's own spelling produced, in the words
			// budgetProblems used: neither may be a problem in any state.
			for _, said := range []string{"is not an allowed model", "names no model, but the run has an allow-list"} {
				if strings.Contains(out, said) {
					t.Errorf("a pin the run admits is refused: %q\n%s", said, out)
				}
			}
			if via == config.RouteOpenRouter {
				// Every run takes the route: there is no state left to name.
				if strings.Contains(errOut, "is not an allowed model") || strings.Contains(errOut, "names no model") {
					t.Fatalf("a resolution every run takes is hedged:\n%s", errOut)
				}
				return
			}
			// via: auto: the run that keeps the pins direct refuses the
			// canonical spelling, which is a note and names that run.
			if !strings.Contains(errOut, "`"+canonical+"` is not an allowed model") {
				t.Fatalf("the run that keeps the pins direct is not named:\n%s", errOut)
			}
			if !strings.Contains(errOut, "with an Anthropic API key stored keeps the pins direct") {
				t.Fatalf("the note does not say which run halts:\n%s", errOut)
			}
		})
	}
}

// TestValidateNotesAClaudePinWithNoKeyAndNoSwitch is plan task 1b.3's second
// requirement, design §6.2's safeguard: a project that routes Claude through a
// provider — the owner docs/design/mixed-credentials.md §1 describes, an
// OpenRouter key and no Anthropic one — and pins a Claude model the switch
// leaves on the direct route is told so, and the line names the one write that
// sends the call to that route.
//
// It is a note and not a refusal, which is the review finding on this change:
// the refusal was gated on what validate can read (an enforcing api-key run and
// a route claiming the model) yet spoke as if it had read the job's secret, so
// it failed a configuration that is valid whenever an Anthropic key *is* stored
// — telling an operator who has one to send Claude prompts to a third party.
// The two states are named instead, which is what the sibling `!ok` path already
// does for the same unread fact (routingKeyless). Without the claim there is no
// route to send the call to, and there is nothing to say at all.
func TestValidateNotesAClaudePinWithNoKeyAndNoSwitch(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 5 }\n"
	const claudePin = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	pinned := func() string {
		return writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claudePin, 1))
	}
	// The route claims the model and its allow_data_to is written; the switch
	// does not name it, so the call goes direct and needs a key whenever the job
	// holds none. The checkout is the one the route admits: the note asks whether
	// this repository's run has a route to take at all.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock)
	inCheckoutOf(t, "acme/sandbox")
	out, errOut, err := execute(t, "validate", pinned())
	if err != nil {
		t.Fatalf("a configuration that works whenever a key is stored is refused (%v):\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: openrouter") {
		t.Fatalf("the note does not name the line that sends the call to that route:\n%s", errOut)
	}
	// The note is the pin's own, so its path is the key the owner fixes, and it
	// never rides the problem channel.
	if !strings.Contains(errOut, "agent.models.coder") || strings.Contains(out, "goes direct") {
		t.Fatalf("the note is not the pin's own, or it is on stdout:\nout %q\nerr %q", out, errOut)
	}
	// Both states, because which one holds is the fact validate cannot read: the
	// job with a key sends the call to Anthropic as it stands, and the job with
	// none halts with the gateway's own message.
	if !strings.Contains(errOut, "a job with a version of the anthropic-api-key secret") ||
		!strings.Contains(errOut, "a job with none halts") ||
		!strings.Contains(errOut, config.MissingAnthropicKey) {
		t.Fatalf("the note picks one state instead of naming both:\n%s", errOut)
	}
	// With the switch written the same file is valid: the route is one the
	// owner already claims, and the resolution is what the run takes.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock+routedSwitchBlock)
	if out, _, err := execute(t, "validate", pinned()); err != nil {
		t.Fatalf("a switch naming the pinned model is refused: %v\n%s", err, out)
	}
	// A Claude pin nothing claims has nothing to say: there is no route to send
	// it to, and the missing key stays the note it has always been.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget)
	if out, errOut, err := execute(t, "validate", pinned()); err != nil {
		t.Fatalf("a Claude pin with no route to take is refused: %v\n%s", err, out)
	} else if strings.Contains(errOut, "routing.claude: openrouter") {
		t.Fatalf("a pin no route claims is told to write a switch it cannot take:\n%s", errOut)
	}
}

// TestValidateAsksWhetherAllowDataToWasWritten pins the review finding on the
// refusal above: it decided "the route's allow_data_to is written" over
// runner.ProvidersFor's table, and that table rewrites every entry it keeps to
// `allow_data_to: [repo]` (runner.ProvidersFor's own doc — the run only needs
// to know it is allowed), so an entry whose allow_data_to was *never written*
// read as written: D1b's safeguard, decided by the filter that erases the
// difference. The note then stated a premise the warning validate prints
// for the same file contradicts ("has no allow_data_to, so every repository of
// the project may mount its key and send it code" — OpenToEveryRepoReason), and
// sent the owner to write a switch whose route is not ready. The question is
// asked of the entry the operator wrote, its own list, and the two states read
// as the two different things they are.
func TestValidateAsksWhetherAllowDataToWasWritten(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 5 }\n"
	const claudePin = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	pinned := func() string {
		return writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claudePin, 1))
	}
	// routedProjectBlock's route with its allow_data_to removed: the entry
	// claims the model, admits every repository of the project (an absent
	// allow_data_to already means that) and never wrote the list D1b is about.
	// A run of acme/sandbox carries it — ProvidersFor keeps it and rewrites its
	// allow_data_to — so the note fires: the call goes direct and needs a key
	// whenever the job holds none. What it may not say is that the route is ready.
	unwritten := strings.Replace(routedProjectBlock, `    allow_data_to: ["acme/sandbox"]`+"\n", "", 1)
	if unwritten == routedProjectBlock {
		t.Fatal("the fixture still carries an allow_data_to")
	}
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+unwritten)
	inCheckoutOf(t, "acme/sandbox")
	_, errOut, err := execute(t, "validate", pinned())
	if err != nil {
		t.Fatalf("a note was reported as a problem: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: openrouter") {
		t.Fatalf("the note does not name `routing.claude: openrouter`:\n%s", errOut)
	}
	if strings.Contains(errOut, "its allow_data_to is written") {
		t.Fatalf("the note reads allow_data_to as written over the table that rewrites it:\n%s", errOut)
	}
	// The truth about that entry, and what to write instead: the entry names no
	// repository, and the line names the entry, the missing list and the switch,
	// in that order.
	if !strings.Contains(errOut, "its allow_data_to names no repository") || !strings.Contains(errOut, "provider openrouter of this project") || !strings.Contains(errOut, `allow_data_to: ["acme/sandbox"]`) {
		t.Fatalf("the note does not say the entry names no repository and what to write:\n%s", errOut)
	}
	if !strings.Contains(errOut, "providers.openrouter: has no allow_data_to") {
		t.Fatalf("the note's premise must be the warning's too, and the warning is:\n%s", errOut)
	}
	// An entry that lists the repository is the other state, and it counts as
	// written: the same file with the list in place takes the other branch of the
	// note, which is what the line then says.
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock)
	inCheckoutOf(t, "acme/sandbox")
	_, errOut, err = execute(t, "validate", pinned())
	if err != nil || !strings.Contains(errOut, "its allow_data_to is written") {
		t.Fatalf("an entry that lists the repository must count as written: err %v\n%s", err, errOut)
	}
}

// TestValidatePrintsOneRoutingNotePerModel pins the count of the routing lines,
// which is the review finding on this change: claudePins enumerates the three pin
// paths and a file whose agent.model serves both the coder and the reviewer
// gives two of them one model — while the routing line names the model and not
// the pin, so the same sentence came back once per pin and the second copy said
// nothing. The lines are deduped by path and sentence (dedupeNotes) and the test
// counts them, because every other routing test only asks whether a substring is
// there at all.
func TestValidatePrintsOneRoutingNotePerModel(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 5 }\n"
	// agent.model serves the coder and the reviewer; only the background pin is
	// a key of its own. Both models are canonical Claude IDs, so the switch's own
	// line is printed for each.
	const shared = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	isolateProjects(t, t.TempDir())
	projectRouting(t, budget+routedProjectBlock)
	inCheckoutOf(t, "acme/sandbox")
	out, errOut, err := execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", shared, 1)))
	// The counts first: in the state this test is about — the line printed once
	// per pin, with the same sentence each time — these are what fail.
	for _, line := range []string{
		"routing.claude: claude-sonnet-5-5 -> direct",
		"routing.claude: claude-haiku-4-5 -> direct",
	} {
		if n := strings.Count(errOut, line); n != 1 {
			t.Errorf("the routing line %q is printed %d times, want once:\n%s", line, n, errOut)
		}
	}
	// The pin's own note is keyed by the pin and keeps its path, which is what
	// the run's own pin rules name (internal/config's pinPaths): both roles that
	// carry the model are told, each on the key the owner edits.
	if n := strings.Count(errOut, "agent.models.coder: claude-sonnet-5-5 is pinned and goes direct"); n != 1 {
		t.Errorf("the coder's own note is printed %d times, want once:\n%s", n, errOut)
	}
	if !strings.Contains(errOut, "agent.models.reviewer: claude-sonnet-5-5 is pinned and goes direct") {
		t.Errorf("the reviewer's own note is missing, and its path is the key the owner edits:\n%s", errOut)
	}
	if err != nil {
		t.Fatalf("validate failed: %v\n%s%s", err, out, errOut)
	}
}

// TestValidateJSONNotesTheAnthropicKey: the note is machine-readable too, in
// its own channel: never a problem, never a warning.
func TestValidateJSONNotesTheAnthropicKey(t *testing.T) {
	inCheckoutOf(t, "acme/sandbox")
	path := budgetProject(t, "budget: { mode: off }\n", "  auth: api-key\n  model: claude-sonnet-5-5")
	out, _, _ := execute(t, "validate", "--json", path)
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	var note bool
	for _, n := range got.Notes {
		if n.Path == "agent.auth" && n.Message == anthropicKeyNoEnforce {
			note = true
		}
	}
	if !note {
		t.Fatalf("notes = %+v, want the Anthropic key note", got.Notes)
	}
	for _, p := range append(append([]config.Problem{}, got.Problems...), got.Warnings...) {
		if strings.Contains(p.Message, "Anthropic API key") {
			t.Errorf("the key needs a note, not %v: %s", p.Path, p.Message)
		}
	}
}

// TestValidateJudgesProvidersOnlyWithAMatchingLocalConfig pins what the two
// surfaces actually do (P5), because "an unclaimed anthropic/<id> is refused
// at both" is true of the runner and **conditional** in the CLI. The runner's
// call is unconditional (internal/runner/gateway.go), and that is the
// enforcement surface; the CLI's provider half returns before it looks unless
// the checkout has a local config whose name matches the file's project, and
// its pins half returns early on the same condition. So a machine with no
// local config, or one for another installation, prints nothing at all about
// providers or pins -- and that is the truth about the pair, not a gap to
// close. The two halves are also not one rule: under agent.auth: oauth the
// pins half is skipped entirely (an oauth run has no gateway, so nothing is
// priced or pinned) while the provider half still runs -- the last sub-case
// is what pins that, with one config carrying both halves' subjects, an alias
// pin and a coder the fixture provider serves. The unclaimed claimed-shape
// pin in the first sub-case is refused by CheckProviderPolicy, the provider
// half, which is why it does not move with the auth.
func TestValidateJudgesProvidersOnlyWithAMatchingLocalConfig(t *testing.T) {
	const budget = "budget: { mode: enforce, per_run_usd: 2 }\n" + validateProviderBlock
	const agent = "  auth: api-key\n  models: { coder: deepseek/deepseek-v4-flash, reviewer: anthropic/claude-sonnet-5.5 }"
	// A matching local config: the unclaimed pin is printed at its own path,
	// with the fix the owner has to make.
	path := budgetProject(t, budget, agent)
	inCheckoutOf(t, "acme/sandbox")
	out, _, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.reviewer") || !strings.Contains(out, "no provider serves it") {
		t.Fatalf("a matching local config: exit %d, out %q", ExitCode(err), out)
	}
	for _, want := range []string{"is a Claude model", "go direct to Anthropic", "naming that exact ID in a route's models"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q: %q", want, out)
		}
	}
	// No local config at all: neither half looks, so nothing is said -- not
	// even the pin the runner would refuse on the next launch.
	isolateProjects(t, t.TempDir())
	fileOnly := writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:",
		"agent:\n"+agent+"\nbudget: { mode: enforce, per_run_usd: 2 }\nworkflows:", 1))
	out, errOut, err := execute(t, "validate", fileOnly)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("no local config: err %v, out %q, stderr %q", err, out, errOut)
	}
	for _, want := range []string{"agent.models.reviewer", "no provider serves it", "allow_data_to", "providers"} {
		if strings.Contains(out+errOut, want) {
			t.Errorf("validate said %q with no local config:\nout: %s\nstderr: %s", want, out, errOut)
		}
	}
	// A local config whose name is another project's: the same silence, from
	// the same two early returns. The control first: with a local config of
	// the file's own project, the same pin is refused here, so the silence
	// below is the name mismatch and not a selection that found nothing.
	isolateProjects(t, t.TempDir())
	lcPath := writeProject(t, "borealis", "proj-9999")
	f, err := os.OpenFile(lcPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(budget); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_PROJECT", "borealis")
	body, err := os.ReadFile(fileOnly)
	if err != nil {
		t.Fatal(err)
	}
	matching := writeConfig(t, strings.Replace(string(body), "project: aurora", "project: borealis", 1))
	if out, _, err := execute(t, "validate", matching); ExitCode(err) != ExitUserError || !strings.Contains(out, "no provider serves it") {
		t.Fatalf("control (a local config of the file's own project): exit %d, out %q", ExitCode(err), out)
	}
	if out, errOut, err = execute(t, "validate", fileOnly); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("another project's config: err %v, out %q, stderr %q", err, out, errOut)
	}
	if strings.Contains(out+errOut, "no provider serves it") {
		t.Errorf("another project's config judged the pin:\nout: %s\nstderr: %s", out, errOut)
	}
	// One config carrying both halves' subjects: an alias pin, which the
	// pins half refuses, and a coder the fixture provider serves, which the
	// provider half refuses under oauth. Under api-key the alias is named
	// (the provider half passes, its auth rule being api-key's own) and
	// under oauth it is not -- the pins half never looked -- while the
	// refusal at agent.models.coder is CheckProviderAuth's wording, which no
	// other rule writes. Skipping the provider half for oauth too would fail
	// both assertions below.
	const both = "  auth: api-key\n  model: sonnet\n  models: { coder: deepseek/deepseek-v4-flash }"
	path = budgetProject(t, budget, both)
	inCheckoutOf(t, "acme/sandbox")
	if out, _, err = execute(t, "validate", path); ExitCode(err) != ExitUserError || !strings.Contains(out, "sonnet is an alias") {
		t.Fatalf("api-key alias pin: exit %d, out %q", ExitCode(err), out)
	}
	path = budgetProject(t, budget, strings.Replace(both, "api-key", "oauth", 1))
	inCheckoutOf(t, "acme/sandbox")
	out, _, err = execute(t, "validate", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.coder") {
		t.Fatalf("oauth, a provider-served coder: exit %d, out %q", ExitCode(err), out)
	}
	if !strings.Contains(out, "an oauth token is a Claude subscription credential") {
		t.Errorf("the refusal under oauth is not CheckProviderAuth's: %q", out)
	}
	if strings.Contains(out, "sonnet is an alias") {
		t.Errorf("the pins half judged the alias under oauth: %q", out)
	}
}

// The layer's claim reaches the checkout-resolving surfaces, not only the
// run: `fugaro validate` judges the pins against the map the run will use,
// so a pin the layer's claude_models claims passes here, and the very same
// file without the claim is refused as it is today.
func TestValidateSeesTheCheckoutLayer(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	f := newCloudFixture(t)
	isolateCache(t)
	// The owner's route, and the claimed ID's price: the layer cannot carry
	// a price (P11), so it is the operator's model_prices that has one.
	f.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n"+
		"model_prices:\n  \""+id+"\": { input_per_m: 3, output_per_m: 15 }\n")
	repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: enforce, per_run_usd: 2 }\n" +
		"agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { reviewer: " + id + ", background: claude-haiku-4-5 }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	layerCheckout(t, f, repo)
	claim := "claude_models:\n  openrouter: [\"" + id + "\"]\n"
	publishedLayer(t, f, testProjectLayer+claim)
	if out, errOut, err := execute(t, "validate"); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("a layer-claimed pin was refused: %v\n%s%s", err, out, errOut)
	}
	// The same checkout, the claim dropped from the layer (the object
	// republished without it): the refusal is back, before any run, naming
	// the pin.
	writeBucketFile(t, f, config.LayerKey, testProjectLayer)
	out, _, err := execute(t, "validate")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "agent.models.reviewer") || !strings.Contains(out, "no provider serves it") {
		t.Fatalf("without the claim: exit %d, out %q", ExitCode(err), out)
	}
	// A claim naming a route the project config does not define is the
	// layer's own problem, reported as the key names it.
	writeBucketFile(t, f, config.LayerKey, testProjectLayer+"claude_models:\n  nosuch: [\""+id+"\"]\n")
	out, _, err = execute(t, "validate")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "claude_models.nosuch") || !strings.Contains(out, "does not define") {
		t.Fatalf("an unknown provider name: exit %d, out %q", ExitCode(err), out)
	}
}

// A layer-claimed pin is judged by the credential and mode rules exactly as
// any other provider model is (P10): the claim decides who may route the ID,
// and nothing else. validate judges the pins against the merged map the run
// will use, so a claimed pin under oauth is refused here with the auth rule's
// own wording, and one under observe with the mode rule's.
// Mutations that make this fail, one at a time: validate.go passing
// lc.Providers to config.CheckProviderAuth (or CheckProviderGateway, or
// CheckProviderPolicy) instead of the merged map.
func TestValidateRefusesAClaimedPinTheAuthAndModeRulesRefuse(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	// claimedCheckout is the fixture of TestValidateSeesTheCheckoutLayer with
	// the credential and the budget mode a case chooses.
	claimedCheckout := func(t *testing.T, auth, budget string) {
		t.Helper()
		f := newCloudFixture(t)
		isolateCache(t)
		f.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n"+
			"model_prices:\n  \""+id+"\": { input_per_m: 3, output_per_m: 15 }\n")
		repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\n" + budget +
			"agent:\n  auth: " + auth + "\n  model: claude-sonnet-5-5\n  models: { reviewer: " + id + ", background: claude-haiku-4-5 }\n" +
			"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
		layerCheckout(t, f, repo)
		publishedLayer(t, f, testProjectLayer+"claude_models:\n  openrouter: [\""+id+"\"]\n")
	}
	t.Run("api-key and enforce: the claim is the pin's passage", func(t *testing.T) {
		claimedCheckout(t, "api-key", "budget: { mode: enforce, per_run_usd: 2 }\n")
		if out, errOut, err := execute(t, "validate"); err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("a claimed pin under api-key and enforce was refused: %v\n%s%s", err, out, errOut)
		}
	})
	t.Run("oauth", func(t *testing.T) {
		claimedCheckout(t, "oauth", "budget: { mode: enforce, per_run_usd: 2 }\n")
		out, errOut, err := execute(t, "validate")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a claimed pin under oauth: exit %d\n%s%s", ExitCode(err), out, errOut)
		}
		if !strings.Contains(out, "an oauth token is a Claude subscription credential") {
			t.Errorf("the refusal is not config.CheckProviderAuth's:\n%s", out)
		}
		if !strings.Contains(out, "agent.models.reviewer") || !strings.Contains(out, "served by provider openrouter") {
			t.Errorf("the refusal does not name the pin and the route:\n%s", out)
		}
	})
	t.Run("observe", func(t *testing.T) {
		claimedCheckout(t, "api-key", "budget: { mode: observe }\n")
		out, errOut, err := execute(t, "validate")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a claimed pin under observe: exit %d\n%s%s", ExitCode(err), out, errOut)
		}
		if !strings.Contains(out, "is served by a provider, which needs budget.mode enforce") {
			t.Errorf("the refusal is not config.CheckProviderGateway's:\n%s", out)
		}
	})
}

// A layer is judged against its own project's config, never against whichever
// project config happens to be selected: with another project's config
// selected, its routes are that installation's, and a claim naming a route of
// the file's own installation would be refused here as "names provider X,
// which the project config does not define" — while the run, which reads the
// layer with its own FUGARO_MODEL_PROVIDERS, serves it. Mutation that makes
// this fail: append layerRoutes' problems unconditionally in validate.go
// (drop the `lc.Name == cfg.Project` guard around it).
func TestValidateJudgesALayerOnlyAgainstItsOwnProjectConfig(t *testing.T) {
	isolateProjects(t, t.TempDir())
	// The selected config is another project's, with a route of its own and
	// no openrouter at all.
	other := writeProject(t, "other", "proj-9999")
	f, err := os.OpenFile(other, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("providers:\n  vendorx:\n    kind: anthropic-compat\n    base_url: https://vendorx.example\n    auth: bearer\n    secret: vendorx-key\n    models: [\"qwen/*\"]\n    allow_data_to: [\"acme/app\"]\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", other)

	path := writeConfig(t, strings.Replace(cliMinimalYAML, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1))
	layer := filepath.Join(t.TempDir(), "layer.yaml")
	if err := os.WriteFile(layer, []byte(minimalAnchored+"claude_models:\n  openrouter: [\"anthropic/claude-sonnet-5.5\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := execute(t, "validate", "--project-layer", layer, path)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("a valid layer with another project's config selected: exit %d\nout: %s\nerr: %s", ExitCode(err), out, errOut)
	}
}

// The claim reaches the *routing* rules and never the price table (P4): the
// run's table is built from the job's FUGARO_MODEL_PROVIDERS, which never
// carries a claim, so a claimed ID has only the price the operator wrote under
// model_prices — the built-in catalog cannot price one, because it materializes
// a row only for a model some *route* matches (catalogRoutes) and the claim is
// not in any route the environment carries. validate must judge with that same
// pair, or it would accept a pin the run then refuses as unpriced: with the
// merged providers in its table (what this test was written against) the
// catalog row below would price the pin and validate would say the file is
// valid. Mutation that makes this fail: build validate's table from the merged
// map (`priceTableFor(claimed, ...)` in validate.go) — with the catalog row the
// pin is priced and the file comes out valid.
func TestValidateDoesNotPriceAClaimedIDFromTheCatalog(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	cat := shippedCatalog(t)
	// An `anthropic/<id>` row the way P11's catalog rows land: the shipped
	// row of another model, under this ID.
	row := cat.Models["deepseek/deepseek-v4.1-flash"]
	cat.Models[id] = row
	old := loadCatalog
	loadCatalog = func() (*catalog.File, error) { return cat, nil }
	t.Cleanup(func() { loadCatalog = old })

	f := newCloudFixture(t)
	isolateCache(t)
	// No model_prices for the claimed ID: that is the whole point.
	f.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n")
	repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: enforce, per_run_usd: 2 }\n" +
		"agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { reviewer: " + id + ", background: claude-haiku-4-5 }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	layerCheckout(t, f, repo)
	publishedLayer(t, f, testProjectLayer+"claude_models:\n  openrouter: [\""+id+"\"]\n")
	out, errOut, err := execute(t, "validate")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a claimed ID the catalog alone could price: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	if !strings.Contains(out, "has no price") || !strings.Contains(out, "model_prices") {
		t.Errorf("the refusal must send the operator to model_prices:\n%s", out)
	}
	// The run's own table, built from the environment the job gets, agrees:
	// the claim is not in it, so the same catalog row does not price the ID.
	p := cliRoute()["openrouter"]
	p.AllowDataTo = []string{"acme/other"}
	env, err := runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": p}, "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env, "anthropic/") {
		t.Errorf("the job's environment carries the claim: %s", env)
	}
	s, err := runner.SpendFromEnv(func(k string) (string, bool) {
		switch k {
		case runner.BudgetModeEnv:
			return "observe", true
		case runner.ModelProvidersEnv:
			return env, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := s.Prices.Lookup(id); ok {
		t.Errorf("the run's table prices the claimed ID (%+v), so validate must have accepted it", m)
	}
}

// The routes validate judges a pin by are the run's own (layerRoutes), and so
// is everything the run derives from them: the *effective background* (the
// coder's own model, when the file sets none and a route serves the coder) and
// whether an Anthropic key is needed at all (config.AnthropicKeyNeeded, which
// asks the same map about the same three pins). Judge either over lc.Providers
// instead and a claim the layer holds is invisible here: the background reads
// as unset, which CheckPins refuses ("is required with a budget"), and every
// pin reads as a direct Anthropic call, which asks init for a key the run never
// uses -- while the run itself, merging the layer, backgrounds those calls on
// the claimed route and never takes that path.
//
// Mutations that make this fail, one at a time: in validate.go take
// budgetProblems' agentPins background (:132), providerProblems' own agent
// copy's background (:251) or its config.AnthropicKeyNeeded (:262) from
// lc.Providers instead of the merged map.
func TestValidateResolvesTheBackgroundAndTheKeyOverTheClaimedMap(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	// checkout is a checkout whose coder and reviewer are the layer's claim (the
	// layer below claims exactly that ID) and whose agent.models holds pins; it
	// returns validate's two streams and its error.
	checkout := func(t *testing.T, mode, pins string) (string, string, error) {
		t.Helper()
		f := newCloudFixture(t)
		isolateCache(t)
		f.appendConfig(t, "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n"+
			"model_prices:\n  \""+id+"\": { input_per_m: 3, output_per_m: 15 }\n")
		repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: " + mode + ", per_run_usd: 2 }\n" +
			"agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { " + pins + " }\n" +
			"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
		layerCheckout(t, f, repo)
		publishedLayer(t, f, testProjectLayer+"claude_models:\n  openrouter: [\""+id+"\"]\n")
		out, errOut, err := execute(t, "validate")
		return out, errOut, err
	}
	t.Run("a claimed coder with no background of its own needs no key", func(t *testing.T) {
		out, errOut, err := checkout(t, "enforce", "coder: "+id+", reviewer: "+id)
		if err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("the claimed coder's own background was not resolved over the claim: exit %d\n%s%s", ExitCode(err), out, errOut)
		}
		if strings.Contains(errOut, "note:") {
			t.Errorf("every pin is served by the claimed route, so the run needs no Anthropic key:\n%s", errOut)
		}
	})
	// The control: the assertion above is worth nothing if validate never notes
	// anything at all. A background pinned to a Claude model is a call Anthropic
	// serves, so the key is needed and the note is there.
	t.Run("a direct background pin still needs the key", func(t *testing.T) {
		out, errOut, err := checkout(t, "enforce", "coder: "+id+", reviewer: "+id+", background: claude-haiku-4-5")
		if err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("exit %d\n%s%s", ExitCode(err), out, errOut)
		}
		if !strings.Contains(errOut, "note: agent.auth") {
			t.Errorf("an explicit Claude background goes to Anthropic, so the key is needed:\n%s", errOut)
		}
	})
	// The background is one of the three pins the gateway rule walks, so it is
	// the run's own resolved background that must appear there.
	t.Run("the claimed coder's own background is a provider pin", func(t *testing.T) {
		out, errOut, _ := checkout(t, "observe", "coder: "+id+", reviewer: "+id)
		if !strings.Contains(out, "agent.models.background") || !strings.Contains(out, "is served by a provider") {
			t.Errorf("the background the run resolves is the claimed coder's model, a provider model:\n%s%s", out, errOut)
		}
	})
}

// init asks for the Anthropic key from needsAnthropicKey, the one rule
// validate notes and the runner's gateway applies, so a claim the checkout's
// layer holds must turn that question off there too: with every pin on the
// claimed route a run never calls Anthropic, and init has no reason to prompt
// for a key it would not use (or to list it as still missing). The rule reads
// the layer the file resolved over (cfg.Layer).
//
// Mutation that makes this fail: pass lc.Providers to config.AnthropicKeyNeeded
// in needsAnthropicKey (the claim becomes invisible, so the pins read as direct
// Anthropic calls and init asks for a key that gates nothing).
func TestNeedsAnthropicKeySeesTheLayerClaim(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	layer, ps := config.ParseProjectLayer([]byte(testProjectLayer+"claude_models:\n  openrouter: [\""+id+"\"]\n"),
		config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"})
	if len(ps) > 0 {
		t.Fatalf("the fixture layer is invalid: %v", ps)
	}
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Providers: map[string]config.ModelProvider{
		"openrouter": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/other"}},
	}}
	cfg := &config.Config{Project: "aurora", Budget: &config.Budget{Mode: "enforce", PerRunUSD: 2},
		Agent: config.Agent{Auth: "api-key", Model: "claude-sonnet-5-5",
			Models: config.ModelRoles{Coder: id, Reviewer: id}}}
	if !needsAnthropicKey(lc, cfg, "acme/other") {
		t.Error("no layer: the pins name Claude models no route claims, so the run calls Anthropic")
	}
	cfg.Layer = layer
	if needsAnthropicKey(lc, cfg, "acme/other") {
		t.Error("the layer claims every pin: the run never calls Anthropic, so init must not ask for a key")
	}
}

// claudeRouteYAML is a project config that routes one Claude model through an
// OpenRouter entry: the allow-list names the canonical ID (claude-sonnet-5-5),
// the route claims the route's own IDs, and the route's row is priced under
// model_prices (the shipped catalog has no Claude row).
const claudeRouteYAML = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5", "anthropic/claude-opus-5.5"]
    allow_data_to: ["acme/app"]
model_prices:
  "anthropic/claude-sonnet-5.5": { input_per_m: 1, output_per_m: 2 }
  "anthropic/claude-opus-5.5": { input_per_m: 1, output_per_m: 2 }
`

// TestValidateResolvesARouteIDTheSameWayARunDoes (review finding on this
// change): a repository's pin written as the route's own ID
// (anthropic/claude-sonnet-5.5) is admitted by the owner's allow-list only when
// the routing switch actually routes that canonical ID. The allow-list names
// claude-sonnet-5-5, so admitting the route's spelling on its own would let a
// repository pin a model the owner never routed — past both the list and the
// switch — and charge the direct Anthropic table for it.
//
// validate resolves the route exactly as the run does (config.CheckAllowed
// takes the switch and the routes), which is the wiring this asserts: the
// switch is the operator's own local config, read offline.
//
// Hand mutation this fails on: dropping the switch argument at the validate
// call site (passing nil), which admits every anthropic/<id> pin.
func TestValidateResolvesARouteIDTheSameWayARunDoes(t *testing.T) {
	const canonical, routed = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"
	agent := "  auth: api-key\n  model: " + routed
	for _, tc := range []struct {
		name    string
		routing string
		ok      bool
	}{
		{"the switch routes the canonical ID", "routing: { claude: { via: openrouter, models: [" + canonical + "] } }\n", true},
		{"the switch routes another model", "routing: { claude: { via: openrouter, models: [claude-opus-5-5] } }\n", false},
		{"no switch at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := budgetProject(t, "budget: { mode: enforce, per_run_usd: 5, allowed_models: ["+canonical+"] }\n"+claudeRouteYAML+tc.routing, agent)
			// A checkout the route admits: claudeRouteYAML's allow_data_to is
			// acme/app, and without one this test ran in whatever repository the
			// test binary sits in — a checkout whose run carries no route at
			// all, which the switch rule now reports for the switch's own list
			// (the switch names claude-sonnet-5-5) as well as for a pin.
			inCheckoutOf(t, "acme/app")
			out, err := validateProblems(t, path)
			if tc.ok {
				if err != nil {
					t.Fatalf("a routed Claude pin was refused: %v\n%s", err, out)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d (%v), want %d: a route ID the switch does not route was admitted\n%s", ExitCode(err), err, ExitUserError, out)
			}
			if !strings.Contains(out, "agent.models.coder") || !strings.Contains(out, routed) {
				t.Fatalf("the refusal does not name the pin the switch did not route:\n%s", out)
			}
		})
	}
}

// TestValidateTellsAViaDirectSwitchFromAnUnnamedModel is the review of this
// task's resolution line. `routing.claude` with `via: direct` and a model in
// its list is a legal spelling of the switch — "this ID stays direct while the
// ones I route go through a provider" — and the resolver answers RouteDirect
// for it exactly as it does for a model the list does not name (D1b's default).
// validate printed "no switch names it: D1b's default" for both, which is
// untrue of the first: the switch does name it, and it says so. doctor already
// tells the two apart ("-> direct (via: ...)"), and both commands read one
// switch, so the two lines cannot say different things about it.
//
// The pin here is claude-sonnet-5-5 with a claude-haiku-4-5 background pin, so
// one run of validate carries both states at once and each line is asserted
// against the model it is about.
func TestValidateTellsAViaDirectSwitchFromAnUnnamedModel(t *testing.T) {
	const claudePin = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { background: claude-haiku-4-5 }\nworkflows:"
	pinned := func() string {
		return writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", claudePin, 1))
	}
	// The switch names the coder pin and says it stays direct.
	isolateProjects(t, t.TempDir())
	projectRouting(t, "routing:\n  claude:\n    via: direct\n    models: [claude-sonnet-5-5]\n")
	_, errOut, err := execute(t, "validate", pinned())
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> direct (via: direct)") {
		t.Errorf("a switch naming the pin with via: direct is not printed as the switch's own line:\n%s", errOut)
	}
	if strings.Contains(errOut, "claude-sonnet-5-5 -> direct (no switch names it") {
		t.Errorf("the switch names claude-sonnet-5-5, and the line says it does not:\n%s", errOut)
	}
	// The switch names one pin and not the other: each line is about its own
	// model, and the state each one is in.
	isolateProjects(t, t.TempDir())
	projectRouting(t, "routing:\n  claude:\n    via: direct\n    models: [claude-haiku-4-5]\n")
	_, errOut, err = execute(t, "validate", pinned())
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-haiku-4-5 -> direct (via: direct)") {
		t.Errorf("the named pin's line is missing or wrong:\n%s", errOut)
	}
	if !strings.Contains(errOut, "routing.claude: claude-sonnet-5-5 -> direct (no switch names it: D1b's default)") {
		t.Errorf("the unnamed pin's line is missing or wrong:\n%s", errOut)
	}
}
