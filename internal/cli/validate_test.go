package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const cliMinimalYAML = `version: 1
project: aurora
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
	cfg := strings.Replace(cliMinimalYAML, "base: web-node,", "base: web-node, resources: { cpu: 3, memory: 4Gi },", 1)
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
	ps, _ := budgetProblems(cfg, lc)
	if len(ps) != 1 || ps[0].Path != "model_prices" {
		t.Fatalf("problems = %v, want only the model_prices one", ps)
	}
}

// An oauth run has no gateway, so the pin rules don't apply to it.
func TestBudgetProblemsSkipPinsForOAuth(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", Budget: &localcfg.Budget{Mode: localcfg.BudgetObserve}}
	cfg := &config.Config{Project: "aurora", Agent: config.Agent{Auth: "oauth"}}
	if ps, _ := budgetProblems(cfg, lc); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
	cfg.Agent.Auth = "api-key"
	if ps, _ := budgetProblems(cfg, lc); len(ps) == 0 {
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
	if !got.Valid || len(got.Problems) != 0 || len(got.Warnings) != 3 {
		t.Fatalf("json = %+v", got)
	}
	// A tighter file says nothing.
	path = fileWithBudget(t, "budget: { mode: observe, per_run_usd: 5 }\n", "budget: { mode: enforce, per_run_usd: 2 }\n", pinnedAgent)
	if _, errOut, err = execute(t, "validate", path); err != nil || strings.Contains(errOut, "warning") {
		t.Fatalf("tightening warned: %v %q", err, errOut)
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
	// missing pin or an allow-list is not judged.
	body := strings.Replace(cliMinimalYAML, "workflows:", "agent: { auth: api-key, model: sonnet }\nbudget: { mode: enforce, per_run_usd: 20, allowed_models: [claude-haiku-4-5] }\nworkflows:", 1)
	out, errOut, err := execute(t, "validate", writeConfig(t, body))
	if err != nil || errOut != "" {
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
	// A file with no budget or token key has no policy to bound: no note.
	if out, errOut, err := execute(t, "validate", "--json", file); err != nil || strings.Contains(errOut, "default branch") ||
		strings.Contains(out, "branch") || !strings.Contains(out, `"warnings": []`) {
		t.Fatalf("no policy keys: %v out %q stderr %q", err, out, errOut)
	}
	withBudget := strings.Replace(cliMinimalYAML, "workflows:", "budget: { per_run_usd: 1 }\nworkflows:", 1)
	if err := os.WriteFile(file, []byte(withBudget), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := execute(t, "validate", file)
	if err != nil || !strings.Contains(out, "is valid") ||
		!strings.Contains(errOut, "branch feature, not the default branch (main)") || !strings.Contains(errOut, "can only tighten") {
		t.Fatalf("%v out %q stderr %q", err, out, errOut)
	}
	// The JSON warning names where it is about.
	out, _, err = execute(t, "validate", "--json", file)
	var got validateOutput
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Warnings) != 1 || got.Warnings[0].Path != "branch" {
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
	if _, ws := budgetProblems(cfg, nil); len(ws) != 1 || ws[0].Path != "budget.per_day_usd" {
		t.Fatalf("warnings = %v", ws)
	}
	path = fileWithBudget(t, "budget: { mode: observe }\n", "budget: { per_run_usd: 1 }\n", pinnedAgent)
	if _, errOut, err = execute(t, "validate", path); err != nil || strings.Contains(errOut, "per_day_usd") {
		t.Fatalf("no day cap, but %v %q", err, errOut)
	}
}
