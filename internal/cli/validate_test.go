package cli

import (
	"encoding/json"
	"errors"
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
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/testutil"
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

// TestValidateWarnsCheckoutClone (0.7.0 safety fix, review finding): a
// checkout: clone workflow is valid config — the config shape does not
// change whether fugaro can actually run it — but a launch of it is
// refused until fugaro cloneSince, since plan Task 17 (the runner's own
// FUGARO_CHECKOUT support) has not shipped yet. fugaro validate must warn
// about that, not error, and name checkout: baked as what runs today.
func TestValidateWarnsCheckoutClone(t *testing.T) {
	path := writeConfig(t, strings.Replace(cliMinimalYAML, "{ commands:", "{ checkout: clone, commands:", 1))
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("out=%q errOut=%q err=%v", out, errOut, err)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("out = %q, want valid", out)
	}
	if !strings.Contains(errOut, "warning: workflows.app.checkout: clone runs are refused at launch until fugaro "+cloneSince) ||
		!strings.Contains(errOut, "generic-tool plan Task 17") || !strings.Contains(errOut, "checkout: baked runs today") {
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
	if !got.Valid || !slices.ContainsFunc(got.Warnings, func(p config.Problem) bool { return p.Path == "workflows.app.checkout" }) {
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
	if err != nil || !strings.Contains(out, "# the Fugaro project; fugaro init --config-only writes your project config\nproject: example\n") {
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
	ps, _ := budgetProblems(cfg, lc, lc.Providers)
	if len(ps) != 1 || ps[0].Path != "model_prices" {
		t.Fatalf("problems = %v, want only the model_prices one", ps)
	}
}

// An oauth run has no gateway, so the pin rules don't apply to it.
func TestBudgetProblemsSkipPinsForOAuth(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", Budget: &localcfg.Budget{Mode: localcfg.BudgetObserve}}
	cfg := &config.Config{Project: "aurora", Agent: config.Agent{Auth: "oauth"}}
	if ps, _ := budgetProblems(cfg, lc, lc.Providers); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
	cfg.Agent.Auth = "api-key"
	if ps, _ := budgetProblems(cfg, lc, lc.Providers); len(ps) == 0 {
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
	const want = "warning: budget.per_day_usd: per_day_usd is enforced by the runner against this repository's day counter only while the project has the shared budget (init --firebase, mode observe or enforce); on a project without it nothing enforces it. A lower RTDB cap wins"
	path := fileWithBudget(t, "budget: { mode: observe }\n", "budget: { per_day_usd: 10 }\n", pinnedAgent)
	out, errOut, err := execute(t, "validate", path)
	if err != nil || !strings.Contains(out, "is valid") || !strings.Contains(errOut, want) {
		t.Fatalf("err %v out %q stderr %q", err, out, errOut)
	}
	// With no project config the warning stays.
	cfg := &config.Config{Project: "aurora", Agent: config.Agent{Auth: "api-key"}, Budget: &config.Budget{PerDayUSD: 10}}
	if _, ws := budgetProblems(cfg, nil, nil); len(ws) != 1 || ws[0].Path != "budget.per_day_usd" {
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
