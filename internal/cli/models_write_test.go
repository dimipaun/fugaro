package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// modelsWriteExtra is what the models writes' fixture appends to the project
// config: one route whose models and allow_data_to are block sequences (a flow
// collection is one yamledit refuses to splice — the same refusal `settings set
// --add` has — and these tests need edits that land), the owner's own
// model_prices, an allow-list, a comment above the route, and two recorded
// repositories.
const modelsWriteExtra = `
# the owner's route: the catalog prices the deepseek models it claims
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    route_fee_pct: 5
    models:
      - deepseek/*
      - vendor/model-x
    allow_data_to:
      - owner/repo-a
model_prices:
  vendor/model-x:
    input_per_m: 1
    output_per_m: 2
  vendor/model-y:
    input_per_m: 3
    output_per_m: 4
budget:
  mode: enforce
  per_run_usd: 10
  allowed_models:
    - claude-sonnet-5-5
    - deepseek/deepseek-v4-flash
    - vendor/model-x
repos:
  owner/repo-a: {}
  owner/repo-b: {}
`

// modelsWriteNoBudgetExtra is the same route with no budget block at all, for
// the rule that an absent budget.allowed_models stays absent (D786).
const modelsWriteNoBudgetExtra = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - deepseek/*
    allow_data_to:
      - owner/repo-a
repos:
  owner/repo-a: {}
`

// modelsWriteHome is the writes' fixture: project aurora in a config home of
// its own, with modelsWriteExtra appended, and no coding-agent marker set, so a
// write that is meant to land is not refused by the session the test binary
// happens to run in. It returns the config home and the selected project
// config's path.
func modelsWriteHome(t *testing.T) (home, path string) {
	t.Helper()
	return modelsWriteHomeWith(t, modelsWriteExtra)
}

// modelsWriteHomeWith is modelsWriteHome with the caller's own YAML appended to
// the project config.
func modelsWriteHomeWith(t *testing.T, extra string) (home, path string) {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	home = settingsFixture(t, extra)
	return home, os.Getenv("FUGARO_CONFIG")
}

// modelsWriteRepoYAML is a repository's own file: a provider coder, a Claude
// reviewer and an api-key run, the shape a `models use` write is judged on.
const modelsWriteRepoYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsWriteRepoPins is a repository's own file pinning the caller's models:
// the shape `models rm`'s warning is read off. An empty model leaves that key
// out, so a file may pin one role and nothing else.
func modelsWriteRepoPins(coder, reviewer, background string) string {
	models := ""
	for _, r := range []struct{ key, model string }{
		{"coder", coder}, {"reviewer", reviewer}, {"background", background},
	} {
		if r.model != "" {
			models += "    " + r.key + ": " + r.model + "\n"
		}
	}
	return `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
` + models + `workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`
}

// modelsWriteCheckout remembers a checkout of repo carrying fugaroYAML, the way
// this machine remembers a clone: the repository's own file under the checkout
// directory, recorded for the project. It is not a git repository — a test that
// needs one makes its own (modelsWriteGitCheckout).
func modelsWriteCheckout(t *testing.T, home, repo, fugaroYAML string) string {
	t.Helper()
	dir := filepath.Join(home, "checkouts", strings.ReplaceAll(repo, "/", "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fugaro.yaml")
	if err := os.WriteFile(path, []byte(fugaroYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "github", Repo: repo}, dir, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	return path
}

// modelsWriteGitCheckout is modelsWriteCheckout for a real git checkout, with
// the process's working directory moved into it: a `models use` about the
// checkout the working directory is in, and the test that the write is never a
// commit, both need one.
func modelsWriteGitCheckout(t *testing.T, repo, fugaroYAML string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/"+repo+".git")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(fugaroYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "the repository's files")
	t.Chdir(dir)
	return filepath.Join(dir, "fugaro.yaml")
}

// modelsWriteLocalConfig parses the fixture's project config as the loader
// reads it, so a test asserts on what the file means and not only on its text.
func modelsWriteLocalConfig(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("%s does not load after the write: %v", path, err)
	}
	return lc
}

// TestModelsCommandsPrintAHostileIDEscaped: a model ID is text from a command
// line or a branch, and the terminal must not obey it. Every models write
// refuses a hostile one — a newline is whitespace (config.CheckModelID) and an
// escape byte keeps the ID out of the provider grammar (pricing.IsAlias) — and
// what the refusal prints carries neither the escape byte nor a line of its
// own, so a session cannot be steered by a model name.
func TestModelsCommandsPrintAHostileIDEscaped(t *testing.T) {
	home, _ := modelsWriteHome(t)
	modelsWriteCheckout(t, home, "owner/repo-a", modelsWriteRepoPins("vendor/model-x", "", ""))
	// The newline is the shape every models write refuses on its own rule; the
	// escape byte is what no provider grammar has room for.
	hostile := "vendor/model-\x1b[31mred\nline`whoami`"
	// The same ID without the newline, so the escape byte alone makes it hostile
	// and the refusal is the one that quotes the ID back.
	csi := "vendor/model-\x1b[31mred`whoami`"
	for _, c := range []struct {
		what string
		args func(id string) []string
	}{
		{"add", func(id string) []string {
			return []string{"models", "add", id, "--price-in", "1", "--price-out", "2", "--yes"}
		}},
		{"use", func(id string) []string {
			return []string{"models", "use", "owner/repo-a", "coder", id, "--yes"}
		}},
		{"rm", func(id string) []string { return []string{"models", "rm", id, "--yes"} }},
	} {
		for _, id := range []string{hostile, csi} {
			out, errOut, err := execute(t, c.args(id)...)
			if err == nil {
				t.Errorf("%s accepted the hostile model ID %q:\n%s", c.what, id, out)
				continue
			}
			// ErrorText is what the top-level error line prints (the package's
			// own printer), so the safety is asserted where a person reads it
			// and not only in the error value.
			for _, printed := range []string{out, errOut, ErrorText(err)} {
				if strings.ContainsRune(printed, 0x1b) {
					t.Errorf("%s printed the escape byte raw:\n%s", c.what, printed)
				}
				if strings.Contains(printed, "\nline") {
					t.Errorf("%s let the ID open a line of its own:\n%s", c.what, printed)
				}
			}
		}
	}
	// The refusals that quote the ID escape it rather than drop the model: the
	// owner still reads which model was refused, with the escape sequence gone
	// and the rest of the text intact.
	for _, c := range []struct {
		what string
		args []string
	}{
		{"add", []string{"models", "add", csi, "--price-in", "1", "--price-out", "2", "--yes"}},
		{"rm", []string{"models", "rm", csi, "--yes"}},
		{"use", []string{"models", "use", "owner/repo-a", "coder", csi, "--yes"}},
	} {
		_, _, err := execute(t, c.args...)
		if err == nil || !strings.Contains(ErrorText(err), "vendor/model-") {
			t.Errorf("%s dropped the model ID instead of escaping it: %v", c.what, err)
		}
	}
}
