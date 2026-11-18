package cli

import (
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file registers the leaves `fugaro accounts craft`, `fugaro accounts
// grade` and `fugaro accounts repo` write (docs/contracts/config-ownership.md
// §2, D2609): a craft's access, a grade's access and a repository's own
// claude_accounts list, with the two removal records beside them.
//
// The store of each row is the layer the command writes in, which is the layer
// the key has and not a choice the caller makes: a craft's access and a
// repository's list are the installation's own config and nothing else
// (locLocal), a grade's access is the repository's file at its unprefixed
// spelling and the project layer's at its `layer.` one, and each row states the
// store its own argv writes — the layer is in the command line the template
// carries, because the hook hands the store the write landed in and not the
// row's (contract §4).
//
// Three of the six rows state Node, for the pools' reason one block over: a
// write whose store is local and whose value a run reads out of the published
// node reaches no run at all unless the hook runs the policy stage, because a
// store of local names no stage of its own (contract §3). A craft's access is
// the first — the node carries the crafts block, and the credentials service
// admits a call by the credential the craft names (D2607) — and a repository's
// own list is the second: the key is stripped from the published copy, but what
// the node carries for it is the `accounts` block the publish derives from
// exactly this list (projectpolicy.DeriveAccounts, localcfg.PolicyRepoAccounts),
// so a repository's runs are admitted to the account only once the publish has
// run. The two removal records are the third (F22, D2607): with "silence keeps
// the node's value", a craft or a credential taken away without the record
// would go on being served by the node, and the credential it named would go on
// being spent.
//
// The grade rows state no Node, and the reason is not the crafts': the node's
// model_grades block is published from the local config's own model_grades
// carrier and from nothing else (init_policy_publish.go, `lc.ModelGrades`), and
// no command of this release writes that carrier. What a grade write touches is
// the project layer file or a checkout's own file, and each reaches runs by the
// road its own store names: `--layer project` owes the layer stage, whose copy
// the job carries, and the repository's file owes nothing, because the merge of
// the branch holding it is what makes it live (D1564).
var accountsCraftAccessWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	// A craft's name is a closed vocabulary (config.CraftNames), so the pattern
	// is the one key shape and `<1>` is the craft the command line repeats.
	{"crafts.*.access", settings.Writer{
		Command: "fugaro accounts craft set",
		Argv:    []string{"accounts", "craft", "set", "<1>", "--access", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"node_removals.crafts", settings.Writer{
		Command: "fugaro accounts craft rm",
		Argv:    []string{"accounts", "craft", "rm", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	// The credential's own record, written by `accounts rm` (F22, D2607): the
	// node keeps a credential this file says nothing about, so the name written
	// beside the block is what says it was taken away rather than never stated.
	{"node_removals.craft_credentials", settings.Writer{
		Command: "fugaro accounts rm",
		Argv:    []string{"accounts", "rm", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	// A grade's access, at the two layers the key has. The layer is in the argv
	// because the same sentence is the owner's repair for either of them
	// (contract §4): the unprefixed key is the repository's own file, and the
	// `layer.` one is this machine's project layer.
	{"model_grades.*.access", settings.Writer{
		Command: "fugaro accounts grade set",
		Argv:    []string{"accounts", "grade", "set", "<1>", "--access", settings.ValuePlaceholder, "--layer", settingsLayerRepo},
		Store:   settings.StoreRepo,
	}},
	{"layer.defaults.model_grades.*.access", settings.Writer{
		Command: "fugaro accounts grade set",
		Argv:    []string{"accounts", "grade", "set", "<1>", "--access", settings.ValuePlaceholder, "--layer", settingsLayerProject},
		Store:   settings.StoreProject,
	}},
	// A repository's own list of the accounts its runs may be served by
	// (docs/project-claude-accounts.md §3). `<1>` is the repository, which the
	// key's own segment holds and the command line repeats, and the row states
	// Node because the admission is served out of the node's derived `accounts`
	// block and from nowhere else.
	{"repos.*.claude_accounts", settings.Writer{
		Command: "fugaro accounts repo add",
		Argv:    []string{"accounts", "repo", "add", "<1>", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
}

// The registration is the family's own and it is fatal, exactly as the pools'
// is: a pattern two families claim has to fail every command of this binary
// rather than let one of the two silently win (docs/contracts/config-ownership.md
// §2).
func init() {
	for _, w := range accountsCraftAccessWriters {
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}
