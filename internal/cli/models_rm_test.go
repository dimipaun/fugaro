package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modelsRmOnlyAllowExtra is a project whose budget.allowed_models holds exactly
// the model the refusal test removes: emptying it is the one shape the loader
// refuses outright.
const modelsRmOnlyAllowExtra = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - vendor/model-x
      - vendor/model-y
    allow_data_to:
      - owner/repo-a
model_prices:
  vendor/model-x:
    input_per_m: 1
    output_per_m: 2
budget:
  mode: enforce
  per_run_usd: 10
  allowed_models:
    - vendor/model-x
repos:
  owner/repo-a: {}
`

// TestModelsRmWarnsNamingEachPinningRepository is the red step: removing a
// model does not refuse when a repository pins it (D783, the owner's answer),
// so the warning is the whole of the protection. It names every repository and
// role this machine can see that runs the model, and says what the next run of
// each one does — fail at launch with no provider for the model — while the
// write still happens.
func TestModelsRmWarnsNamingEachPinningRepository(t *testing.T) {
	home, path := modelsWriteHome(t)
	modelsWriteCheckout(t, home, "owner/repo-a", modelsWriteRepoPins("vendor/model-x", "claude-sonnet-5-5", ""))
	modelsWriteCheckout(t, home, "owner/repo-b", modelsWriteRepoPins("deepseek/deepseek-v4-flash", "vendor/model-x", "claude-sonnet-5-5"))

	out, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"owner/repo-a", "coder", "owner/repo-b", "reviewer", "no provider serves it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not name %q:\n%s", want, out)
		}
	}
	// The role a file only runs through its background key is not reported as a
	// coder pin: one repository, one clause per role that names the model.
	if n := strings.Count(out, "owner/repo-b"); n != 1 {
		t.Errorf("owner/repo-b is named %d times, want once (background is not a second pin):\n%s", n, out)
	}
	// The write happened: the claim, the price and the allow-list entry are
	// gone.
	lc := modelsWriteLocalConfig(t, path)
	if models := lc.Providers["openrouter"].Models; !slices.Equal(models, []string{"deepseek/*"}) {
		t.Errorf("the route still claims the model: %v", models)
	}
	if _, ok := lc.ModelPrices["vendor/model-x"]; ok {
		t.Errorf("model_prices still prices the model: %v", lc.ModelPrices)
	}
	if containsString(lc.Budget.AllowedModels, "vendor/model-x") {
		t.Errorf("budget.allowed_models still names the model: %v", lc.Budget.AllowedModels)
	}
}

// TestModelsRmWarnsForRepositoriesWithoutACheckout: a repository this machine
// has no checkout for is named as one it cannot see, rather than left out. The
// pin it may carry is exactly the one the warning exists for, and a reader who
// is not told which repositories were not read would take silence for safety.
func TestModelsRmWarnsForRepositoriesWithoutACheckout(t *testing.T) {
	home, _ := modelsWriteHome(t)
	modelsWriteCheckout(t, home, "owner/repo-a", modelsWriteRepoPins("vendor/model-x", "claude-sonnet-5-5", ""))

	out, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "owner/repo-b") || !strings.Contains(out, "cannot") {
		t.Errorf("the warning does not name the repository it cannot read:\n%s", out)
	}
	if !strings.Contains(out, "owner/repo-a") {
		t.Errorf("the warning does not name the repository it read:\n%s", out)
	}
}

// TestModelsRmLeavesAClaimingPatternAndSaysSo: only the exact entry is removed
// (spec §5). A pattern that claims the model keeps claiming it, and the command
// says so rather than leaving the owner to think the model is unusable now.
func TestModelsRmLeavesAClaimingPatternAndSaysSo(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "rm", "deepseek/deepseek-v4-flash", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "deepseek/*") || !strings.Contains(out, "still claimed") {
		t.Errorf("the command does not say the pattern still claims the model:\n%s", out)
	}
	lc := modelsWriteLocalConfig(t, path)
	if models := lc.Providers["openrouter"].Models; !slices.Equal(models, []string{"deepseek/*", "vendor/model-x"}) {
		t.Errorf("the route's claims changed though only a pattern covers the model: %v", models)
	}
	if containsString(lc.Budget.AllowedModels, "deepseek/deepseek-v4-flash") {
		t.Errorf("the allow-list still names the model: %v", lc.Budget.AllowedModels)
	}
}

// TestModelsRmRefusesEmptyingTheAllowList: budget.allowed_models is never left
// as an empty list, which the strict loader refuses (spec §5), so removing the
// last entry is refused here with that reason and nothing is written.
func TestModelsRmRefusesEmptyingTheAllowList(t *testing.T) {
	_, path := modelsWriteHomeWith(t, modelsRmOnlyAllowExtra)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err == nil {
		t.Fatal("removing the last allowed model was accepted")
	}
	if !strings.Contains(err.Error(), "allowed_models") {
		t.Errorf("the refusal does not name the key: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused rm wrote the file:\n%s", got)
	}
}

// TestModelsRmRefusesInAnAgentSession: the file a models write edits is the
// owner's own project config, outside every repository, so a coding agent's
// session prints the diff and the command and writes nothing (D789) — the
// warning it would have printed included, because nothing was removed.
func TestModelsRmRefusesInAnAgentSession(t *testing.T) {
	_, path := modelsWriteHome(t)
	before := readFileString(t, path)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err == nil {
		t.Fatal("a models write ran in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") || !strings.Contains(err.Error(), "fugaro models rm vendor/model-x") {
		t.Errorf("the refusal does not name the marker and the command to run: %v", err)
	}
	if !strings.Contains(out, "+ ") && !strings.Contains(out, "- ") {
		t.Errorf("the refusal prints no diff:\n%s", out)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("the refusal warns about a removal that did not happen:\n%s", out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestModelsRmWarnsOnlyOfARemovalThatLanded: the warning describes what the
// removal breaks — a repository's next run has no provider for the model — so
// it is the write's own output and not the command's. A refused write (a coding
// agent's session here, D789) leaves every pin where it was, and a warning
// about it would tell the owner to repair a break nothing caused.
func TestModelsRmWarnsOnlyOfARemovalThatLanded(t *testing.T) {
	home, path := modelsWriteHome(t)
	modelsWriteCheckout(t, home, "owner/repo-a", modelsWriteRepoPins("vendor/model-x", "claude-sonnet-5-5", ""))

	t.Setenv("CLAUDECODE", "1")
	refused, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err == nil {
		t.Fatal("a models write ran in a coding agent's session")
	}
	if strings.Contains(refused, "warning:") {
		t.Errorf("the refused write warned about a removal that did not happen:\n%s", refused)
	}
	if got := readFileString(t, path); !strings.Contains(got, "vendor/model-x") {
		t.Fatalf("the refused write removed the model anyway:\n%s", got)
	}

	// The same command at the owner's own terminal does warn: the pin above is
	// exactly what the warning is for, so its absence in the refusal is the
	// order and not a fixture with nothing to say.
	t.Setenv("CLAUDECODE", "")
	out, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "owner/repo-a") {
		t.Errorf("the removal that landed printed no warning:\n%s", out)
	}
}

// TestModelsRmKeepsTheRestOfTheFile: only the keys the model appears in are
// rewritten. Every comment, every other price, the route's own keys and the
// recorded repositories survive byte for byte (D790, spec §9).
func TestModelsRmKeepsTheRestOfTheFile(t *testing.T) {
	_, path := modelsWriteHome(t)

	out, _, err := execute(t, "models", "rm", "vendor/model-x", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after := readFileString(t, path)
	for _, want := range []string{
		"# the owner's route: the catalog prices the deepseek models it claims",
		"route_fee_pct: 5",
		"vendor/model-y",
		"owner/repo-a: {}",
		"owner/repo-b: {}",
		"secret: openrouter-api-key",
		"per_run_usd: 10",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("the edit dropped %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, "vendor/model-x") {
		t.Errorf("the edit left the removed model behind:\n%s", after)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("%d backups, want one", len(backups))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the written config is %v, want 0600", info.Mode().Perm())
	}
}
