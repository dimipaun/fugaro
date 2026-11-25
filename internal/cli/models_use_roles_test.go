package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
)

// modelsUseProjectExtra is the project the project-layer tests run against: one
// route that claims a pattern and opens to both repositories the project
// onboards. The allow_data_to list is the point of the fixture — a
// project-layer default is what every repository of the project runs with, so
// the route has to cover all of them or the write is refused — and the
// repository's own two tests use modelsUseExtra, whose route names one of the
// two on purpose.
const modelsUseProjectExtra = `
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
      - owner/repo-b
budget:
  mode: enforce
  per_run_usd: 10
  allowed_models:
    - deepseek/deepseek-v4-pro
    - deepseek/deepseek-v4-flash
    - claude-sonnet-5-5
repos:
  owner/repo-a: {}
  owner/repo-b: {}
`

// modelsUseLayerAuth is the project layer a model write lands in: defaults.agent
// is the agent block a repository that names none of its own runs with, so a
// default that pins a provider model needs the auth that carries one — the same
// rule a repository's own file is held to (modelsUseModelCheck). A layer naming
// no auth is a default no launch takes, which
// TestModelsUseJudgesTheProjectLayerAsALaunchWould pins.
const modelsUseLayerAuth = "defaults:\n  agent:\n    auth: api-key\n"

// TestModelsUseWritesEveryRole is M1's own red step: the switch writes every
// key agent.models has, the diagnose slot included. The slot is the model
// `fugaro troubleshoot` runs on (D1607), it is written by hand until this
// command takes the role, and the role list it was refused by named the four
// slots the family had before it.
func TestModelsUseWritesEveryRole(t *testing.T) {
	for _, role := range []string{"coder", "reviewer", "background", "vision", "diagnose"} {
		t.Run(role, func(t *testing.T) {
			modelsWriteHomeWith(t, modelsUseProjectExtra)
			path := settingsWriteLayerFile(t, modelsUseLayerAuth)

			out, _, err := execute(t, "models", "use", role, "deepseek/deepseek-v4-pro")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if agent := modelsUseAgentBlock(t, path); !strings.Contains(agent, role+": deepseek/deepseek-v4-pro") {
				t.Errorf("agent.models.%s was not written:\n%s", role, agent)
			}
		})
	}
}

// TestModelsUseDiagnoseRole is the role the plan's M1 adds: `models use`
// refused it while the role list was the four keys the family started with, and
// the key is written now through the same edit and the same checks as the other
// four.
func TestModelsUseDiagnoseRole(t *testing.T) {
	path := modelsUseProjectHome(t, modelsUseLayerCommented)
	before := readFileString(t, path)

	out, _, err := execute(t, "models", "use", "diagnose", "deepseek/deepseek-v4-pro")
	if err != nil {
		t.Fatalf("the diagnose slot was refused: %v\n%s", err, out)
	}
	want := strings.Replace(before,
		"      reviewer: claude-sonnet-5-5\n",
		"      reviewer: claude-sonnet-5-5\n      diagnose: deepseek/deepseek-v4-pro\n", 1)
	if got := readFileString(t, path); got != want {
		t.Errorf("the edit is not the one line, under the key it names:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestModelsUseLayerFlag is T3's own rule at this command, with S4's default for
// a lifted key: --layer names where the write lands, the project layer with
// --layer project, and a layer the key does not have is refused naming the
// layers it does have. The default is the layer the key belongs to — the
// project's for a model (D3237), which no repository states any more, and a
// repository's own file for the keys a repository still states. No models key
// has a local layer — the four budget keys are the whole of what the local
// config carries — so --layer local is always that refusal.
func TestModelsUseLayerFlag(t *testing.T) {
	t.Run("project is the default for a model key", func(t *testing.T) {
		modelsWriteHomeWith(t, modelsUseProjectExtra)
		layer := settingsWriteLayerFile(t, modelsUseLayerAuth)
		repoFile := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)

		out, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		l, ps := config.ParseProjectLayer([]byte(readFileString(t, layer)), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
		if len(ps) > 0 {
			t.Fatalf("the layer does not read back after the write: %s\n%s", ps[0].String(), readFileString(t, layer))
		}
		if got := l.Defaults.Agent.Models.Coder; got != "deepseek/deepseek-v4-pro" {
			t.Errorf("defaults.agent.models.coder = %q, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
		}
		if got := readFileString(t, repoFile); got != modelsUseCommentedYAML {
			t.Errorf("the default write reached a repository's own file:\n%s", got)
		}
	})

	t.Run("repo is the default for a repository's own key", func(t *testing.T) {
		modelsUseHome(t)
		path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)

		out, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(readFileString(t, path), "recipe: solo") {
			t.Errorf("the default write did not land in the checkout:\n%s", readFileString(t, path))
		}
	})

	t.Run("project writes the layer", func(t *testing.T) {
		modelsWriteHomeWith(t, modelsUseProjectExtra)
		layer := settingsWriteLayerFile(t, modelsUseLayerAuth)

		out, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro", "--layer", "project")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Contains(out, "working tree") {
			t.Errorf("a project-layer write is not a checkout's working tree:\n%s", out)
		}
		l, ps := config.ParseProjectLayer([]byte(readFileString(t, layer)), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
		if len(ps) > 0 {
			t.Fatalf("the layer does not read back after the write: %s\n%s", ps[0].String(), readFileString(t, layer))
		}
		if got := l.Defaults.Agent.Models.Coder; got != "deepseek/deepseek-v4-pro" {
			t.Errorf("defaults.agent.models.coder = %q, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
		}
	})

	t.Run("project refuses a named repository", func(t *testing.T) {
		modelsWriteHomeWith(t, modelsUseProjectExtra)
		settingsWriteLayerFile(t, "")

		_, _, err := execute(t, "models", "use", "owner/repo-a", "agent.recipe", "solo", "--layer", "project")
		if err == nil {
			t.Fatal("a project-layer write took a repository")
		}
		if !strings.Contains(err.Error(), "goes with --layer repo") {
			t.Errorf("the refusal does not say which layer the repository goes with: %v", err)
		}
	})

	t.Run("local names the layers the key has", func(t *testing.T) {
		modelsUseHome(t)
		path := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)
		before := readFileString(t, path)

		_, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro", "--layer", "local")
		if err == nil {
			t.Fatal("a models key was written at --layer local")
		}
		for _, want := range []string{"may not be set at --layer local", "repo", "project"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("the refused write changed the file:\n%s", got)
		}
	})

	t.Run("the layer spelling names its own layer", func(t *testing.T) {
		modelsWriteHomeWith(t, modelsUseProjectExtra)
		layer := settingsWriteLayerFile(t, modelsUseLayerAuth)

		out, _, err := execute(t, "models", "use", "layer.defaults.agent.models.coder", "deepseek/deepseek-v4-pro", "--layer", "project")
		if err != nil {
			t.Fatalf("the layer's own spelling of the key was refused: %v\n%s", err, out)
		}
		l, _ := config.ParseProjectLayer([]byte(readFileString(t, layer)), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
		if got := l.Defaults.Agent.Models.Coder; got != "deepseek/deepseek-v4-pro" {
			t.Errorf("defaults.agent.models.coder = %q, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
		}
	})
}

// TestClaudeModelsWideningRefusedInAnAgentSession is D2617 at this command:
// layer.claude_models is a widening — it hands a route the ability to serve a
// Claude model to every repository that route lists — so the write is Typed.
// Typed is what makes it the owner's own step in every store and under every
// gate, and what a person reads at their own terminal instead of a prompt is
// the sentence saying what the write widens (D1540). The refusal a session
// reads names the command to run at that terminal, which is the whole of what
// it may produce beside the diff.
func TestClaudeModelsWideningRefusedInAnAgentSession(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	layer := settingsWriteLayerFile(t, "")
	before := readFileString(t, layer)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "models", "use", "layer.claude_models", "openrouter=anthropic/claude-sonnet-5-5", "--layer", "project")
	if err == nil {
		t.Fatalf("a widening write landed in a coding agent's session:\n%s", out)
	}
	if !strings.Contains(err.Error(), "refusing to write") || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal is not the session fence's: %v", err)
	}
	if !strings.Contains(err.Error(), "fugaro models use layer.claude_models openrouter=anthropic/claude-sonnet-5-5 --layer project") {
		t.Errorf("the refusal does not name the line to run at the owner's own terminal: %v", err)
	}
	if got := readFileString(t, layer); got != before {
		t.Errorf("the refused widening wrote the layer:\n%s", got)
	}

	t.Setenv("CLAUDECODE", "")
	out, _, err = execute(t, "models", "use", "layer.claude_models", "openrouter=anthropic/claude-sonnet-5-5", "--layer", "project")
	if err != nil {
		t.Fatalf("the owner's own terminal was refused the widening: %v\n%s", err, out)
	}
	if !strings.Contains(out, "widens what route openrouter may serve") {
		t.Errorf("the write does not say what it widens:\n%s", out)
	}
	l, ps := config.ParseProjectLayer([]byte(readFileString(t, layer)), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
	if len(ps) > 0 {
		t.Fatalf("the layer does not read back after the write: %s", ps[0].String())
	}
	if got := l.ClaudeModels["openrouter"]; !slices.Equal(got, []string{"anthropic/claude-sonnet-5-5"}) {
		t.Errorf("claude_models.openrouter = %v, want [anthropic/claude-sonnet-5-5]:\n%s", got, readFileString(t, layer))
	}
}

// TestModelsUseJudgesTheProjectLayerAsALaunchWould is the layer half of the
// same rule the repository's own file is held to: a project-layer default
// pinning a provider model under an auth the layer STATES and that cannot carry
// one is refused here, rather than at the launch of each repository that
// inherits it. A layer that states no auth is not judged as vertex (nobody
// chose it): the repositories state their own, and each launch judges that.
func TestModelsUseJudgesTheProjectLayerAsALaunchWould(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	layer := settingsWriteLayerFile(t, "defaults:\n  agent:\n    auth: oauth\n")
	before := readFileString(t, layer)

	_, _, err := execute(t, "models", "use", "coder", "deepseek/deepseek-v4-pro", "--layer", "project")
	if err == nil {
		t.Fatal("a provider model was accepted as a project-layer default under oauth")
	}
	if !strings.Contains(err.Error(), "api-key") {
		t.Errorf("the refusal does not say which auth the model needs: %v", err)
	}
	if got := readFileString(t, layer); got != before {
		t.Errorf("the refused default wrote the layer:\n%s", got)
	}
}

// TestClaudeModelsRefusesARouteTheProjectDoesNotDefine: a layer adds a Claude
// model to a route the owner has already opened and never creates one
// (config.ApplyClaudeClaims, P8), so a claim naming a route this project does
// not define is refused with the routes it does have rather than written as an
// entry no run can read.
func TestClaudeModelsRefusesARouteTheProjectDoesNotDefine(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	layer := settingsWriteLayerFile(t, "")
	before := readFileString(t, layer)

	_, _, err := execute(t, "models", "use", "layer.claude_models", "nowhere=anthropic/claude-sonnet-5-5", "--layer", "project")
	if err == nil {
		t.Fatal("a claim on a route the project does not define was written")
	}
	for _, want := range []string{"is not defined here", "openrouter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := readFileString(t, layer); got != before {
		t.Errorf("the refused claim wrote the layer:\n%s", got)
	}
}

// TestModelsUseReadsNoStdin: the command asks nothing (D1540), so it never
// reads the person's own stdin. The reader fails the assertion by recording the
// read, which is the only thing a question would leave behind.
func TestModelsUseReadsNoStdin(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	settingsWriteLayerFile(t, modelsUseLayerAuth)
	stdin := &unreadStdin{}

	out, _, err := executeStdinReader(t, stdin, "models", "use", "coder", "deepseek/deepseek-v4-pro")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if stdin.read {
		t.Errorf("the command read its stdin, and the only reader of it is a question:\n%s", out)
	}
	assertNoPrompt(t, out)
}

// modelsUseWriterKeys is every pattern `models use` registers and one concrete
// key each answers for, with the store the write lands in. The two patterns
// that vary are here like the rest: every one of these templates names the
// key, so every line the registry answers is complete on this base.
var modelsUseWriterKeys = []struct {
	pattern string
	key     string
	store   settings.Store
}{
	{"agent.model", "agent.model", settings.StoreProject},
	{"agent.recipe", "agent.recipe", settings.StoreRepo},
	{"agent.models.*", "agent.models.coder", settings.StoreProject},
	{"agent.levels.*.model", "agent.levels.l2.model", settings.StoreProject},
	{"agent.levels.*.effort", "agent.levels.l2.effort", settings.StoreRepo},
	{"layer.defaults.agent.model", "layer.defaults.agent.model", settings.StoreProject},
	{"layer.defaults.agent.recipe", "layer.defaults.agent.recipe", settings.StoreProject},
	{"layer.defaults.agent.models.*", "layer.defaults.agent.models.coder", settings.StoreProject},
	{"layer.defaults.agent.levels.*.model", "layer.defaults.agent.levels.l2.model", settings.StoreProject},
	{"layer.defaults.agent.levels.*.effort", "layer.defaults.agent.levels.l2.effort", settings.StoreProject},
	{"layer.claude_models", "layer.claude_models", settings.StoreProject},
}

// TestModelsUseRegistersAWriterForEveryKeyItWrites: the registry answers for
// every key this command writes, with this command's line and the store the
// write lands in. The two halves have to agree — a row whose writer names
// another command is a refusal that sends a person to a command that cannot
// make the write — so the table is read back through WriterOf rather than
// restated.
func TestModelsUseRegistersAWriterForEveryKeyItWrites(t *testing.T) {
	for _, c := range modelsUseWriterKeys {
		t.Run(c.pattern, func(t *testing.T) {
			w, ok := settings.WriterOf(c.key)
			if !ok {
				t.Fatalf("no writer answers for %s", c.key)
			}
			if w.Command != "fugaro models use" {
				t.Errorf("%s is written by %q, want fugaro models use", c.key, w.Command)
			}
			if w.Store != c.store {
				t.Errorf("%s lands in %q, want %q", c.key, w.Store, c.store)
			}
			if len(w.Argv) == 0 || w.Argv[0] != "models" || w.Argv[1] != "use" {
				t.Errorf("%s's argv is %v, which is not a `fugaro models use` line", c.key, w.Argv)
			}
			if !slices.Contains(w.Argv, "--layer") {
				t.Errorf("%s's argv %v names no layer, and the store it lands in is one of the layers", c.key, w.Argv)
			}
		})
	}
}

// TestModelsUseRegisteredArgvWritesTheKey is the conformance rule this command
// owes (D2610): the line a refusal prints really writes the key, in the store
// the row names. It is run here rather than left to T1 because the argv is this
// file's own, and a template that does not run is a refusal that wastes a
// person's time.
func TestModelsUseRegisteredArgvWritesTheKey(t *testing.T) {
	for _, c := range []struct {
		key   string
		value string
		store settings.Store
		path  []string
		want  string
	}{
		{"agent.model", "deepseek/deepseek-v4-pro", settings.StoreProject, []string{"defaults", "agent", "model"}, "deepseek/deepseek-v4-pro"},
		{"agent.recipe", "solo", settings.StoreRepo, []string{"agent", "recipe"}, "solo"},
		{"agent.models.coder", "deepseek/deepseek-v4-pro", settings.StoreProject, []string{"defaults", "agent", "models", "coder"}, "deepseek/deepseek-v4-pro"},
		{"agent.levels.l2.model", "deepseek/deepseek-v4-pro", settings.StoreProject, []string{"defaults", "agent", "levels", "l2", "model"}, "deepseek/deepseek-v4-pro"},
		{"agent.levels.l2.effort", "high", settings.StoreRepo, []string{"agent", "levels", "l2", "effort"}, "high"},
		{"layer.defaults.agent.model", "deepseek/deepseek-v4-pro", settings.StoreProject, []string{"defaults", "agent", "model"}, "deepseek/deepseek-v4-pro"},
		{"layer.defaults.agent.recipe", "solo", settings.StoreProject, []string{"defaults", "agent", "recipe"}, "solo"},
		{"layer.defaults.agent.models.coder", "deepseek/deepseek-v4-pro", settings.StoreProject, []string{"defaults", "agent", "models", "coder"}, "deepseek/deepseek-v4-pro"},
		{"layer.defaults.agent.levels.l2.model", "deepseek/deepseek-v4-pro", settings.StoreProject, []string{"defaults", "agent", "levels", "l2", "model"}, "deepseek/deepseek-v4-pro"},
		{"layer.defaults.agent.levels.l2.effort", "high", settings.StoreProject, []string{"defaults", "agent", "levels", "l2", "effort"}, "high"},
		// claude_models is a block and not a scalar, so the assertion is the
		// entry the argv's value names rather than a path's value.
		{"layer.claude_models", "openrouter=anthropic/claude-sonnet-5-5", settings.StoreProject, nil, "anthropic/claude-sonnet-5-5"},
	} {
		t.Run(c.key, func(t *testing.T) {
			modelsWriteHomeWith(t, modelsUseProjectExtra)
			layer := settingsWriteLayerFile(t, modelsUseLayerAuth)
			repoFile := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)

			w, ok := settings.WriterOf(c.key)
			if !ok {
				t.Fatalf("no writer answers for %s", c.key)
			}
			out, _, err := execute(t, modelsUseArgv(w.ArgvFor(c.key), c.value)...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			data := readFileString(t, repoFile)
			if c.store == settings.StoreProject {
				data = readFileString(t, layer)
			}
			if c.path == nil {
				if !strings.Contains(data, c.want) {
					t.Errorf("the argv wrote no %q into the %s store:\n%s", c.want, c.store, data)
				}
				return
			}
			if got := settingsValueIn([]byte(data), c.path); got != c.want {
				t.Errorf("%s = %q, want %q after the argv ran:\n%s", c.key, got, c.want, data)
			}
		})
	}
}

// TestModelsUseArgvNamesTheKeyInEveryRow: the two patterns that vary are held
// to the line that runs. settings.Writer.ArgvFor fills `<key>` and nothing
// else, so a template that named the pattern's segment alone answered
// `<1>` where the key goes, and `fugaro models use <1> <value> --layer repo` is
// a line neither a person nor the conformance test can run. The word the
// command takes there is the key's own spelling, which is what these rows
// register.
func TestModelsUseArgvNamesTheKeyInEveryRow(t *testing.T) {
	for _, c := range []struct {
		key  string
		want []string
	}{
		{"agent.models.coder", []string{"models", "use", "agent.models.coder", "<value>", "--layer", "project"}},
		{"agent.models.diagnose", []string{"models", "use", "agent.models.diagnose", "<value>", "--layer", "project"}},
		{"layer.defaults.agent.models.coder", []string{"models", "use", "layer.defaults.agent.models.coder", "<value>", "--layer", "project"}},
	} {
		t.Run(c.key, func(t *testing.T) {
			w, ok := settings.WriterOf(c.key)
			if !ok {
				t.Fatalf("no writer answers for %s", c.key)
			}
			if got := w.ArgvFor(c.key); !slices.Equal(got, c.want) {
				t.Errorf("ArgvFor(%s) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}

// TestModelsUseVaryingArgvWritesTheKeyItNames is the same rule end to end: the
// line a varying row answers runs as it stands, and the role it names is the
// key's own.
func TestModelsUseVaryingArgvWritesTheKeyItNames(t *testing.T) {
	modelsWriteHomeWith(t, modelsUseProjectExtra)
	layer := settingsWriteLayerFile(t, modelsUseLayerAuth)
	repoFile := modelsWriteGitCheckout(t, "owner/repo-a", modelsUseCommentedYAML)

	w, ok := settings.WriterOf("agent.models.coder")
	if !ok {
		t.Fatal("no writer answers for agent.models.coder")
	}
	out, _, err := execute(t, modelsUseArgv(w.ArgvFor("agent.models.coder"), "deepseek/deepseek-v4-pro")...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	l, ps := config.ParseProjectLayer([]byte(readFileString(t, layer)), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
	if len(ps) > 0 {
		t.Fatalf("the layer does not read back after the argv ran: %s\n%s", ps[0].String(), readFileString(t, layer))
	}
	if got := l.Defaults.Agent.Models.Coder; got != "deepseek/deepseek-v4-pro" {
		t.Errorf("the filled argv wrote defaults.agent.models.coder = %q, want deepseek/deepseek-v4-pro:\n%s", got, readFileString(t, layer))
	}
	if got := readFileString(t, repoFile); got != modelsUseCommentedYAML {
		t.Errorf("the model row's argv wrote a repository's own file:\n%s", got)
	}
}

// modelsUseArgv is a registered template with the value placeholder filled:
// what the conformance test runs.
func modelsUseArgv(argv []string, value string) []string {
	out := slices.Clone(argv)
	for i, word := range out {
		if word == "<value>" {
			out[i] = value
		}
	}
	return out
}
