package cli

import (
	"context"
	"path/filepath"
	"slices"
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

// modelsUseLayerProject is the project the project-layer model writes run
// against: the route claims the same models the repository fixtures use, opened
// to both repositories the project onboards — a project-layer default is judged
// against every repository of the project rather than the one a `--layer repo`
// write named, so the route has to cover them both — and the allow-list admits
// them, so a pin the tests expect to land is judged on the model and not on the
// list.
//
// A model is the project's choice (D3237), so this is where the tests that used
// to write a repository's own file write now (S4, R15): `models use` refuses a
// lifted key in a repository, and the checks are otherwise the same ones.
const modelsUseLayerProject = `
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
      - owner/repo-b
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

// modelsUseLayerNoVendor is that project with the route's own model left out of
// the allow-list: the list is the tighter of the two the launch merges, so it is
// what refuses the pin (modelsUseModelCheck reads the project's list).
var modelsUseLayerNoVendor = strings.Replace(modelsUseLayerProject, "    - vendor/model-x\nrepos:", "repos:", 1)

// modelsUseProjectHome is the fixture for a project-layer model write: the
// project above and the layer file the write lands in, whose body is the
// caller's own `defaults:` block.
func modelsUseProjectHome(t *testing.T, layer string) string {
	t.Helper()
	modelsWriteHomeWith(t, modelsUseLayerProject)
	return settingsWriteLayerFile(t, layer)
}

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

// modelsUseFileAllowedYAML is the repository's file with an allow-list of its
// own: the project's list admits the model the switch names, this file's does
// not, and the file is the tighter of the two — the one a launch is judged
// under (policy.Merge).
const modelsUseFileAllowedYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
budget:
  mode: enforce
  per_run_usd: 10
  allowed_models:
    - deepseek/deepseek-v4-flash
    - deepseek/deepseek-v4-pro
    - claude-sonnet-5-5
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseClaudeCoderYAML is the commented file with a background pin: a
// Claude coder leaves the background requests with no model of the run's own
// (config.EffectiveBackground, which derives it only from a provider coder), and
// a run under an allow-list refuses a role that names none — so the file that
// pins a Claude coder names the background model itself.
const modelsUseClaudeCoderYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
    background: deepseek/deepseek-v4-flash
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// The layer fixtures are the repository ones under the layer's own `defaults:`
// block: defaults.agent is the block a repository that names none of its own
// runs with, which is the block a project-layer model write is judged over.

// modelsUseLayerCommented is modelsUseCommentedYAML under defaults:, comment and
// all.
const modelsUseLayerCommented = `defaults:
  # the coder writes, the reviewer reads it back
  agent:
    auth: api-key
    models:
      coder: deepseek/deepseek-v4-flash
      reviewer: claude-sonnet-5-5
`

// modelsUseLayerNoModels is modelsUseNoModelsYAML under defaults:.
const modelsUseLayerNoModels = "defaults:\n  agent:\n    auth: api-key\n"

// modelsUseLayerOAuth is modelsUseOAuthYAML under defaults:.
const modelsUseLayerOAuth = `defaults:
  agent:
    auth: oauth
    models:
      reviewer: claude-sonnet-5-5
`

// modelsUseLayerClaudeCoder is modelsUseClaudeCoderYAML under defaults:.
const modelsUseLayerClaudeCoder = `defaults:
  agent:
    auth: api-key
    models:
      coder: deepseek/deepseek-v4-flash
      reviewer: claude-sonnet-5-5
      background: deepseek/deepseek-v4-flash
`

// modelsUseHome is the fixture: the project above, and no coding-agent marker
// set, so a write that is meant to land is not fenced by the session the test
// binary happens to run in.
func modelsUseHome(t *testing.T) string {
	t.Helper()
	_, path := modelsWriteHomeWith(t, modelsUseExtra)
	return path
}

// TestModelsUseEditsOnlyTheRoleLine is the red step: the command edits the one
// key and nothing else, so the only line that changes is the role's and the
// comment above the block is byte-for-byte what it was (D781, D790). The file
// is the project layer's: a model is the project's choice (D3237), and S4
// refuses a write of one into a repository.
func TestModelsUseEditsOnlyTheRoleLine(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro")
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
	if len(changed) != 1 || now[changed[0]] != "      coder: deepseek/deepseek-v4-pro" {
		t.Errorf("the edit changed %d line(s), want only the coder line:\n%s", len(changed), after)
	}
	for _, want := range []string{"# the coder writes, the reviewer reads it back", "auth: api-key", "reviewer: claude-sonnet-5-5"} {
		if !strings.Contains(after, want) {
			t.Errorf("the edit dropped %q:\n%s", want, after)
		}
	}
	if !strings.Contains(out, "+ ") || !strings.Contains(out, "- ") {
		t.Errorf("the command printed no diff:\n%s", out)
	}
}

// TestModelsUseAsksNothing: switching a role's model is a diff the command
// prints and nothing else — no question, no --yes, no terminal (D1540). The
// sentence naming the file is printed beside the diff, and the write lands.
func TestModelsUseAsksNothing(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)

	out, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if after := readFileString(t, path); !strings.Contains(after, "coder: deepseek/deepseek-v4-pro") {
		t.Errorf("the switch did not land:\n%s", after)
	}
}

// TestModelsUseInsertsAgentModelsWhenAbsent: a layer that names no
// agent.models gains the block, with the role inside it, rather than the
// command failing or writing the key in the wrong place.
func TestModelsUseInsertsAgentModelsWhenAbsent(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerNoModels)

	out, _, err := execute(t, "models", "use", "reviewer", "deepseek/deepseek-v4-pro")
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
// allow_data_to does not name every repository this project onboards is
// refused, and nothing is written. The layer is the whole project's, so the
// repository the route misses is the one the refusal names.
func TestModelsUseRefusesAModelNoRouteOpensToTheRepository(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseExtra)
	path := settingsWriteLayerFile(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "coder", "vendor/model-x")
	if err == nil {
		t.Fatal("a model the route does not open to every repository was accepted")
	}
	if !strings.Contains(err.Error(), "owner/repo-b") {
		t.Errorf("the refusal does not name the repository the route misses: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUseRefusesAnUnpricedModel: a model the price table cannot price is
// refused, and the refusal names the `models add` that would fix it rather than
// leaving the owner to guess (spec §6).
func TestModelsUseRefusesAnUnpricedModel(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "coder", "vendor/model-q")
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

// TestModelsUseRefusesAModelTheAllowListForbids: the project's own
// budget.allowed_models is the tighter of the two lists the launch merges, so
// the switch it forbids is refused here rather than written into a layer every
// launch of every repository then refuses.
func TestModelsUseRefusesAModelTheAllowListForbids(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseLayerNoVendor)
	path := settingsWriteLayerFile(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "coder", "vendor/model-x")
	if err == nil {
		t.Fatal("a model the allow-list does not name was accepted")
	}
	if !strings.Contains(err.Error(), "allowed_models") {
		t.Errorf("the refusal does not name the list that forbids it: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUseWritesAModelTheAllowListAdmits: the list above is a list, not a
// wall: a model it admits is written as it was before, so the rule refuses the
// exclusions and nothing else.
func TestModelsUseWritesAModelTheAllowListAdmits(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)

	out, _, err := execute(t, "models", "use", "coder", "vendor/model-x")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "coder: vendor/model-x") {
		t.Errorf("the pin was not written:\n%s", readFileString(t, path))
	}
}

// modelsUseBrokenReviewerYAML is a file whose reviewer already fails validate:
// vendor/model-q is claimed by a route the price table cannot price. The switch
// under test has nothing to do with it.
const modelsUseBrokenReviewerYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: vendor/model-q
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// TestModelsUseWritesIntoAFileThatAlreadyFailsValidate: only the problem the
// edit introduces is a refusal. A file whose reviewer is already unpriced is its
// owner's to fix, and a write that has nothing to do with that reviewer refusing
// on its behalf would be a second, wrong verdict on the same file — the recipe
// written here is one a launch takes. The key is `agent.recipe`, a repository's
// own: every model key of that file is the project's now (S4, R15).
func TestModelsUseWritesIntoAFileThatAlreadyFailsValidate(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseBrokenReviewerYAML)

	out, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
	if err != nil {
		t.Fatalf("the write was refused over a problem the file already had: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "recipe: solo") {
		t.Errorf("the recipe was not written:\n%s", readFileString(t, path))
	}
	if !strings.Contains(readFileString(t, path), "reviewer: vendor/model-q") {
		t.Errorf("the edit rewrote a line it does not own:\n%s", readFileString(t, path))
	}
}

// modelsUseProviderLevelYAML is the repository file a level's effort is refused
// over: level 2 runs a model the project's route serves, and a provider stage
// runs with thinking off, so an effort set there is refused at every launch
// (D725). The effort is a key this command writes.
const modelsUseProviderLevelYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
  levels:
    l2:
      model: deepseek/deepseek-v4-pro
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseEffortLevelYAML is the other half of the same rule: the effort is
// already there and the model is one no route serves, so the pin of a provider
// model onto that level is what a launch would refuse (D725).
const modelsUseEffortLevelYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
  levels:
    l2:
      model: claude-sonnet-5-5
      effort: high
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseRefusingLevelYAML is a file no launch takes until its owner fixes
// level 2: the level's model is one the project's route serves and its effort is
// written, which is the D725 refusal the level rules make — a problem the file
// already has, so a switch that has nothing to do with it still lands.
const modelsUseRefusingLevelYAML = `version: 1
project: aurora
git:
  provider: github
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
  levels:
    l2:
      model: deepseek/deepseek-v4-pro
      effort: high
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// modelsUseLayerLevelYAML is the project layer the same two writes are judged
// in: defaults.agent is the block a repository that names none of its own runs
// with, so a level's model and effort written there are a launch's own reading
// of that repository too.
const modelsUseLayerLevelYAML = "defaults:\n  agent:\n    auth: api-key\n    levels:\n      l2:\n        model: deepseek/deepseek-v4-pro\n"

// modelsUseLayerEffortYAML is that layer with the effort already written and a
// model no route serves.
const modelsUseLayerEffortYAML = "defaults:\n  agent:\n    auth: api-key\n    levels:\n      l2:\n        model: claude-sonnet-5-5\n        effort: high\n"

// TestModelsUseRefusesALevelEffortALaunchWouldRefuse: the review levels are
// judged by the launch's own rules, at both layers, before the write — a level
// whose model a route serves is refused an effort rather than handed one that
// every launch of that repository refuses (D725, config.ResolveLevels through
// validate's reviewLevelProblems). The file is byte-for-byte what it was.
func TestModelsUseRefusesALevelEffortALaunchWouldRefuse(t *testing.T) {
	for _, c := range []struct {
		name string
		// file is the repository's own fugaro.yaml, empty for a layer write.
		file string
		// project is the project the layer belongs to, empty for a repo write.
		project string
		layer   string
	}{
		{name: "repo", file: modelsUseProviderLevelYAML},
		{name: "project layer", project: modelsUseProjectExtra, layer: modelsUseLayerLevelYAML},
	} {
		t.Run(c.name, func(t *testing.T) {
			var args []string
			var path string
			if c.project == "" {
				modelsUseHome(t)
				path = modelsWriteGitCheckout(t, "owner/repo-a", c.file)
				args = []string{"models", "use", "owner/repo-a", "agent.levels.l2.effort", "high"}
			} else {
				modelsWriteHomeWith(t, c.project)
				path = settingsWriteLayerFile(t, c.layer)
				args = []string{"models", "use", "agent.levels.l2.effort", "high", "--layer", "project"}
			}
			before := readFileString(t, path)

			_, _, err := execute(t, args...)
			if err == nil {
				t.Fatalf("a level's effort a launch refuses was written:\n%s", readFileString(t, path))
			}
			for _, want := range []string{"D725", "l2.effort", "openrouter", "deepseek/deepseek-v4-pro"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestModelsUseRefusesAProviderModelOnALevelThatHasAnEffort: the same rule from
// the other side — the level's effort is already written, so the model a route
// serves is the key that would be refused at every launch, and it is refused
// here instead, with the layer untouched (D725). A level's model is the
// project's (D3237), so the write is the layer's; `agent.levels.l2.effort` above
// is the key a repository still states, and its test keeps the repo half.
func TestModelsUseRefusesAProviderModelOnALevelThatHasAnEffort(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	path := settingsWriteLayerFile(t, modelsUseLayerEffortYAML)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "agent.levels.l2.model", "deepseek/deepseek-v4-pro")
	if err == nil {
		t.Fatalf("a provider model a launch refuses was pinned:\n%s", readFileString(t, path))
	}
	for _, want := range []string{"D725", "l2.effort", "openrouter", "deepseek/deepseek-v4-pro"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestModelsUseWritesIntoAFileWhoseLevelsAlreadyRefuse is the same rule as
// TestModelsUseWritesIntoAFileThatAlreadyFailsValidate, for the levels: only a
// level this write changes is a refusal, so a file whose level 2 already pairs a
// provider model with an effort — a file no launch takes until its owner fixes
// that level — still takes the recipe, which has nothing to do with it.
func TestModelsUseWritesIntoAFileWhoseLevelsAlreadyRefuse(t *testing.T) {
	modelsUseHome(t)
	path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseRefusingLevelYAML)

	out, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
	if err != nil {
		t.Fatalf("the write was refused over a level the file already had: %v\n%s", err, out)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "recipe: solo") {
		t.Errorf("the pin was not written:\n%s", after)
	}
	if !strings.Contains(after, "effort: high") || !strings.Contains(after, "model: deepseek/deepseek-v4-pro") {
		t.Errorf("the edit rewrote a line it does not own:\n%s", after)
	}
}

// TestModelsUseRefusesAProviderModelUnderOAuth: a provider model is an api-key
// run's (config.CheckProviderAuth), so a repository whose file runs on oauth is
// refused the switch rather than handed a pin no launch would take.
func TestModelsUseRefusesAProviderModelUnderOAuth(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerOAuth)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro")
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
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "use", "background", "deepseek/deepseek-v4-pro")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := strings.Replace(before,
		"      reviewer: claude-sonnet-5-5\n",
		"      reviewer: claude-sonnet-5-5\n      background: deepseek/deepseek-v4-pro\n", 1)
	if got := readFileString(t, path); got != want {
		t.Errorf("the edit is not the one line, under the role it names:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestModelsUseJudgesTheBackgroundRoleAsALaunchWould: the background pin is
// checked like the coder's and the reviewer's, so a repository whose auth
// cannot carry a provider model is refused it too — a background model that
// slipped through would reach another provider on the run's own credential.
func TestModelsUseJudgesTheBackgroundRoleAsALaunchWould(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerOAuth)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "background", "deepseek/deepseek-v4-pro")
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

// TestModelsUseWritesTheVisionSlot is V6's red step: agent.models has a fourth
// key — the model a stage runs instead when its own model cannot read an image
// (D2000) — and the switch writes it through the same edit and the same checks
// as the other three, as the one line it adds. It is red while modelsRoleNames
// has three entries: the role is refused, and the slot is not a name a
// repository may pin.
func TestModelsUseWritesTheVisionSlot(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	wantRoles := []string{"coder", "reviewer", "background", "vision"}
	if !slices.Equal(modelsRoleNames, wantRoles) {
		t.Fatalf("modelsRoleNames = %v, want %v: the vision slot is one of agent.models' keys, and every surface lists it beside the other three", modelsRoleNames, wantRoles)
	}

	out, _, err := execute(t, "models", "use", "vision", "deepseek/deepseek-v4-pro")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := strings.Replace(before,
		"      reviewer: claude-sonnet-5-5\n",
		"      reviewer: claude-sonnet-5-5\n      vision: deepseek/deepseek-v4-pro\n", 1)
	if got := readFileString(t, path); got != want {
		t.Errorf("the edit is not the one line, under the key it names:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestModelsUseRefusesAnUnpricedVisionModel: the slot is judged like every
// other pin, so a model the price table cannot price is refused — the `models
// add` that would fix it is named — and nothing is written. A slot that took
// one would be the failure the slot exists to prevent: the stage that reads an
// image dies at the gateway for want of a price.
func TestModelsUseRefusesAnUnpricedVisionModel(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "vision", "vendor/model-q")
	if err == nil {
		t.Fatal("an unpriced vision model was accepted")
	}
	if !strings.Contains(err.Error(), "fugaro models add vendor/model-q") {
		t.Errorf("the refusal does not name the models add that fixes it: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}
}

// TestModelsUsePinsAClaudeModelWithNoRoute: a Claude ID needs no route of the
// project config — it goes to Anthropic — so the missing-route refusal is for a
// vendor/model ID a route has not opened, never for one of these. The pin is
// written (the file names its background model, since a Claude coder derives
// none), and the price of the spelling the table carries is what lets it.
func TestModelsUsePinsAClaudeModelWithNoRoute(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerClaudeCoder)

	out, _, err := execute(t, "models", "use", "coder", "claude-sonnet-5-5")
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
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "coder", "anthropic/claude-sonnet-5-5")
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

	out, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
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
	if !strings.Contains(readFileString(t, path), "recipe: solo") {
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

	_, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
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

	out, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
	if err != nil {
		t.Fatalf("a repository's own file was refused in a coding agent's session: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "recipe: solo") {
		t.Errorf("the session's write did not land:\n%s", readFileString(t, path))
	}
}

// modelsUseAgentBlock is the agent: block of the file at path, so an assertion
// about the inserted role cannot be satisfied by a line elsewhere in the file.
func modelsUseAgentBlock(t *testing.T, path string) string {
	t.Helper()
	var out []string
	indent := -1
	for _, line := range strings.Split(readFileString(t, path), "\n") {
		lead := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent < 0:
			if strings.TrimSpace(line) == "agent:" {
				indent = lead
			}
		case line != "" && lead <= indent:
			return strings.Join(out, "\n")
		default:
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

// modelsUseRouted is the project the routing tests run against: the route that
// serves the models, and the project's own routing table — one entry that
// reaches the model a pin may take, admitted to both repositories the project
// onboards, and nothing at all for the model the refusal is about.
const modelsUseRouted = modelsUseLayerProject + `
model_routing:
  - match: vendor/model-x
    chain:
      - {kind: provider, provider: openrouter, admit: [owner/repo-a, owner/repo-b]}
`

// TestModelsUseRefusesAnUnroutedModel: a project that publishes a routing table
// reaches its models by that table and by nothing else (R1, R2), so a pin no
// entry of it matches is refused, and the refusal is the table's own — RT3's
// sentence with the `fugaro routing set` that writes the entry, which is the
// same one `fugaro validate` prints and the run's own gate refuses at — rather
// than the release-1 route answer this command would otherwise give the same
// pin. Nothing is written.
//
// The same command still writes a pin its entry routes, which is what keeps the
// rule the table's own answer and not a wall: a table project every pin of which
// were refused would be a table nobody can use.
func TestModelsUseRefusesAnUnroutedModel(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseRouted)
	path := settingsWriteLayerFile(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	_, _, err := execute(t, "models", "use", "coder", "qwen/qwen3-coder")
	if err == nil {
		t.Fatal("a model no entry of the table matches was accepted")
	}
	if !strings.Contains(err.Error(), `no entry of the table matches "qwen/qwen3-coder"`) {
		t.Errorf("the refusal is not the table's own: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro routing set qwen/qwen3-coder") {
		t.Errorf("the refusal does not name the routing set that fixes it: %v", err)
	}
	if strings.Contains(err.Error(), "no route of the project config serves") {
		t.Errorf("the release-1 route answer refused a table project's pin: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused switch wrote the file:\n%s", got)
	}

	out, _, err := execute(t, "models", "use", "coder", "vendor/model-x")
	if err != nil {
		t.Fatalf("a model the table routes was refused: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "coder: vendor/model-x") {
		t.Errorf("the pin was not written:\n%s", readFileString(t, path))
	}
}
