package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// modelsUseExtra is the project the `models use` tests run against: one route
// that claims a pattern, a priced model and a model nothing prices, an
// allow-list that names the model the happy path switches to, and two recorded
// repositories.
const modelsUseExtra = `
# the owner's route
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - deepseek/*
      - vendor/model-x
      - vendor/model-q
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
    - claude-sonnet-5-5
    - deepseek/deepseek-v4-flash
    - deepseek/deepseek-v4-pro
    - vendor/model-x
repos:
  owner/repo-a: {}
  owner/repo-b: {}
`

// modelsUseCommentedYAML is a repository's file with a comment, a key above the
// agent block and a key below it: the three things an edit that is only the one
// role line has to leave exactly as they are.
const modelsUseCommentedYAML = `version: 1
project: aurora
git:
  provider: github
# the coder writes, the reviewer reads it back
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
rigor:
  default: pro
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseNoModelsYAML is the same file with no agent.models key at all: the
// command has to insert the block rather than fail on a missing mapping.
const modelsUseNoModelsYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseOAuthYAML pins a Claude model under oauth, where a provider model is
// refused: the file itself is fine, the switch is not.
const modelsUseOAuthYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: oauth
  models:
    reviewer: claude-sonnet-5-5
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseHome is the fixture: the project above, and no coding-agent marker
// set, so a write that is meant to land is not fenced by the session the test
// binary happens to run in.
func modelsUseHome(t *testing.T) string {
	t.Helper()
	_, path := modelsWriteHomeWith(t, modelsUseExtra)
	return path
}

// TestModelsUseEditsOnlyTheRoleLine is the red step: the command edits the
// repository's own fugaro.yaml in its working tree, and the only line that
// changes is the role's. The comment, the key above the agent block and the key
// below it are byte-for-byte what they were (D781, D790).
func TestModelsUseEditsOnlyTheRoleLine(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after := readFileString(t, path)
	was, now := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(was) != len(now) {
		t.Fatalf("the edit changed the line count:\n%s", after)
	}
	changed := []int{}
	for i := range was {
		if was[i] != now[i] {
			changed = append(changed, i)
		}
	}
	if len(changed) != 1 || now[changed[0]] != "    coder: deepseek/deepseek-v4-pro" {
		t.Errorf("the edit changed %d line(s), want only the coder line:\n%s", len(changed), after)
	}
	for _, want := range []string{"# the coder writes, the reviewer reads it back", "rigor:", "default: pro", "reviewer: claude-sonnet-5-5"} {
		if !strings.Contains(after, want) {
			t.Errorf("the edit dropped %q:\n%s", want, after)
		}
	}
	if !strings.Contains(out, "+ ") || !strings.Contains(out, "- ") {
		t.Errorf("the command printed no diff:\n%s", out)
	}
}

// TestModelsUseInsertsAgentModelsWhenAbsent: a file that names no
// agent.models gains the block, with the role inside it, rather than the
// command failing or writing the key in the wrong place.
func TestModelsUseInsertsAgentModelsWhenAbsent(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseNoModelsYAML)

	out, _, err := execute(t, "models", "use", "owner/repo-a", "reviewer", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "models:") || !strings.Contains(after, "reviewer: deepseek/deepseek-v4-pro") {
		t.Errorf("the agent.models block was not written:\n%s", after)
	}
	agent := strings.TrimSpace(modelsUseAgentBlock(t, path))
	if agent == "" || strings.Contains(agent, "coder:") {
		t.Errorf("the inserted block is not the one role:\n%s", agent)
	}
}

// TestModelsUseRefusesAModelNoRouteOpensToTheRepository: the switch is judged
// as a launch would judge it, so a model the route serves but whose
// allow_data_to does not name this repository is refused, and nothing is
// written.
func TestModelsUseRefusesAModelNoRouteOpensToTheRepository(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-b", modelsUseCommentedYAML)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "owner/repo-b", "coder", "vendor/model-x", "--yes")
	if err == nil {
		t.Fatal("a model the route does not open to the repository was accepted")
	}
	if !strings.Contains(err.Error(), "owner/repo-b") {
		t.Errorf("the refusal does not name the repository: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUseRefusesAnUnpricedModel: a model the price table cannot price is
// refused, and the refusal names the `models add` that would fix it rather than
// leaving the owner to guess (spec §6).
func TestModelsUseRefusesAnUnpricedModel(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "vendor/model-q", "--yes")
	if err == nil {
		t.Fatal("an unpriced model was accepted")
	}
	if !strings.Contains(err.Error(), "fugaro models add vendor/model-q") {
		t.Errorf("the refusal does not name the models add that fixes it: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUseRefusesAProviderModelUnderOAuth: a provider model is an api-key
// run's (config.CheckProviderAuth), so a repository whose file runs on oauth is
// refused the switch rather than handed a pin no launch would take.
func TestModelsUseRefusesAProviderModelUnderOAuth(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseOAuthYAML)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "deepseek/deepseek-v4-pro", "--yes")
	if err == nil {
		t.Fatal("a provider model was accepted under oauth")
	}
	if !strings.Contains(err.Error(), "api-key") {
		t.Errorf("the refusal does not say which auth the model needs: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUsePinsTheBackgroundRole: background is the third key
// agent.models has — Claude Code's own small requests — and it is a pin this
// command writes like the other two, as the one line it adds.
func TestModelsUsePinsTheBackgroundRole(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)

	out, _, err := execute(t, "models", "use", "owner/repo-a", "background", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := strings.Replace(modelsUseCommentedYAML,
		"    reviewer: claude-sonnet-5-5\n",
		"    reviewer: claude-sonnet-5-5\n    background: deepseek/deepseek-v4-pro\n", 1)
	if got := readFileString(t, path); got != want {
		t.Errorf("the edit is not the one line, under the role it names:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestModelsUseJudgesTheBackgroundRoleAsALaunchWould: the background pin is
// checked like the coder's and the reviewer's, so a repository whose auth
// cannot carry a provider model is refused it too — a background model that
// slipped through would reach another provider on the run's own credential.
func TestModelsUseJudgesTheBackgroundRoleAsALaunchWould(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseOAuthYAML)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "owner/repo-a", "background", "deepseek/deepseek-v4-pro", "--yes")
	if err == nil {
		t.Fatal("a provider model was accepted for background under oauth")
	}
	if !strings.Contains(err.Error(), "api-key") {
		t.Errorf("the refusal does not say which auth the model needs: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUsePinsAClaudeModelWithNoRoute: a Claude ID needs no route of the
// project config — it goes to Anthropic — so the missing-route refusal is for a
// vendor/model ID a route has not opened, never for one of these. The pin is
// written, and the price of the spelling the table carries is what lets it.
func TestModelsUsePinsAClaudeModelWithNoRoute(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)

	out, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "claude-sonnet-5-5", "--yes")
	if err != nil {
		t.Fatalf("a Claude model with no route was refused: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "coder: claude-sonnet-5-5") {
		t.Errorf("the pin was not written:\n%s", readFileString(t, path))
	}
}

// TestModelsUseRefusesAClaudeSpellingTheTableDoesNotPrice: the route's own
// spelling of a Claude ID is a Claude ID, so the refusal says so and names the
// price as the reason, rather than sending the owner to open a provider route
// for a model that never takes one.
func TestModelsUseRefusesAClaudeSpellingTheTableDoesNotPrice(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "anthropic/claude-sonnet-5-5", "--yes")
	if err == nil {
		t.Fatal("a spelling the price table does not carry was accepted")
	}
	if !strings.Contains(err.Error(), "Claude model") || !strings.Contains(err.Error(), "no row for that spelling") {
		t.Errorf("the refusal does not say it is a Claude model with no price: %v", err)
	}
	if strings.Contains(err.Error(), "no route of the project config serves") {
		t.Errorf("the refusal tells the owner to open a route for a Claude model: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUseNeverCommits: the change is prepared for the user to review, in
// the working tree. Nothing is committed, staged or pushed — the file reaches a
// run when the user's own branch is merged to the base branch (spec §6).
func TestModelsUseNeverCommits(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
	dir := filepath.Dir(path)
	head := modelsUseGit(t, dir, "rev-parse", "HEAD")

	out, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := modelsUseGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the command committed: HEAD moved from %s to %s", head, got)
	}
	if staged := modelsUseGit(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("the command staged %q", staged)
	}
	if modified := modelsUseGit(t, dir, "diff", "--name-only"); modified != "fugaro.yaml" {
		t.Errorf("the working tree's changed files are %q, want only fugaro.yaml", modified)
	}
	if !strings.Contains(readFileString(t, path), "coder: deepseek/deepseek-v4-pro") {
		t.Errorf("the working tree's file was not changed:\n%s", readFileString(t, path))
	}
}

// TestModelsUseRefusesWithoutACheckout: the write lands in a repository's own
// working tree, so a machine with no checkout of that repository has nowhere to
// put it and says so.
func TestModelsUseRefusesWithoutACheckout(t *testing.T) {
	modelsUseHome(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "deepseek/deepseek-v4-pro", "--yes")
	if err == nil {
		t.Fatal("a switch with no checkout was accepted")
	}
	if !strings.Contains(err.Error(), "owner/repo-a") {
		t.Errorf("the refusal does not name the repository: %v", err)
	}
}

// TestModelsUseRunsInAnAgentSession: the file is the repository's own
// fugaro.yaml in the working tree, which a coding agent may edit anyway, so
// this is the one models write a session is allowed to make (spec §8).
func TestModelsUseRunsInAnAgentSession(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "models", "use", "owner/repo-a", "coder", "deepseek/deepseek-v4-pro", "--yes")
	if err != nil {
		t.Fatalf("a repository's own file was refused in a coding agent's session: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "coder: deepseek/deepseek-v4-pro") {
		t.Errorf("the session's write did not land:\n%s", readFileString(t, path))
	}
}

// modelsUseAgentBlock is the agent: block of the file at path, so an assertion
// about the inserted role cannot be satisfied by a line elsewhere in the file.
func modelsUseAgentBlock(t *testing.T, path string) string {
	t.Helper()
	var out []string
	in := false
	for _, line := range strings.Split(readFileString(t, path), "\n") {
		if line == "agent:" {
			in = true
			continue
		}
		if in && line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// modelsUseGit is one git command read in dir: the tests about what a write did
// to the repository — never a commit, never a staged file — need the real
// thing rather than a sentence in the output.
func modelsUseGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitRead(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}
