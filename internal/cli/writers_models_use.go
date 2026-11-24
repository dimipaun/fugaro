package cli

import "github.com/dimipaun/fugaro/internal/settings"

// This file is `models use`'s half of the config-ownership writer contract
// (docs/contracts/config-ownership.md §4): the keys this command writes,
// registered as the command line that writes them. A refusal for one of these
// keys prints that line — the one that works — and T1's conformance test runs
// it against a hermetic config and asserts the key changed in the store beside
// it (D2609, D2610).
//
// The registration is a package init and its error is a programming error: a
// pattern another family already claimed, a store outside the vocabulary or a
// pattern that overlaps one of the same shape is a diff to settle rather than
// a registration to win, so this panics and every command of the binary fails
// loudly instead of one of the two silently winning (contract §2).
func init() {
	for _, w := range modelsUseWriters {
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}

// modelsUseWriters is the table: every key `models use` writes, and nothing
// wider than the keys it writes.
//
// The two narrowings are deliberate. `agent.levels.*.none` and `.focus` are
// the generic path's rows — a level's on/off and its focus are not a model and
// not an effort — so the two level patterns name the leaves this command
// writes rather than the whole `agent.levels.*.*` block. And
// `layer.defaults.agent.*` would claim `auth`, `review_rounds`,
// `max_budget_usd` and every other key of that block, which are `settings
// set`'s: what this command writes there is spelled out leaf by leaf.
//
// The store is where the argv's write lands and not the layer the value is
// served from: the repo rows land in a checkout's `fugaro.yaml`, the `layer.`
// rows in this machine's project layer file. The argv names the layer it
// writes, so the line a refusal prints runs as it stands.
//
// The model rows answer `--layer project` even under the repository's own
// spelling of the key (S4, R15): a model is the project's choice (D3237), so a
// repository's file states none of them and the line that writes
// `agent.models.coder` is the project-layer one. What stays at `--layer repo`
// is the two keys a repository still states — `agent.recipe` and a level's
// effort — and the write the line prints is the only one the command accepts.
//
// A template names the key itself with `<key>`, the only word
// settings.Writer.ArgvFor fills: the line a refusal prints has to run as it
// stands, and a `<1>` left for the pattern's varying segment is printed as
// written, which is a command line nobody can run. Naming the whole key costs
// nothing, because `models use` resolves the key's own spelling for every row
// it carries, `agent.models.coder` and `layer.defaults.agent.models.coder`
// included.
var modelsUseWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	{
		pattern: "agent.model",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "agent.recipe",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "repo"},
			Store:   settings.StoreRepo,
		},
	},
	{
		pattern: "agent.models.*",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "agent.levels.*.model",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "agent.levels.*.effort",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "repo"},
			Store:   settings.StoreRepo,
		},
	},
	{
		pattern: "layer.defaults.agent.model",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "layer.defaults.agent.recipe",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "layer.defaults.agent.models.*",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "layer.defaults.agent.levels.*.model",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "layer.defaults.agent.levels.*.effort",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	{
		pattern: "layer.claude_models",
		writer: settings.Writer{
			Command: "fugaro models use",
			Argv:    []string{"models", "use", settings.KeyPlaceholder, "<value>", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
}
