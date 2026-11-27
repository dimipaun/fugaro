package cli

import (
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file registers the leaves `fugaro accounts craft`, `fugaro accounts
// grade` and `fugaro accounts repo` write (docs/contracts/config-ownership.md
// §2, D2609): a repository's own claude_accounts list and the two removal
// records beside it. A craft's access and a grade's access are registered here
// as well, and what they answer with is no writer of theirs: the three are
// retired (MG4 of docs/model-credentials-routing/), no command writes them, and
// each answers with retiredRoutingWriter — the command that replaced them, so
// that the line a refusal prints is one that runs.
//
// The store of each live row is the layer the command writes in, which is the
// layer the key has and not a choice the caller makes: these three are the
// installation's own config and nothing else (locLocal), and the row states the
// store its own argv writes, because the hook hands the store the write landed
// in and not the row's (contract §4).
//
// The three live rows state Node, for the pools' reason one block over: a write
// whose store is local and whose value a run reads out of the published node
// reaches no run at all unless the hook runs the policy stage, because a store
// of local names no stage of its own (contract §3). A repository's own list is
// the first: the key is stripped from the published copy, but what the node
// carries for it is the `accounts` block the publish derives from exactly this
// list (projectpolicy.DeriveAccounts, localcfg.PolicyRepoAccounts), so a
// repository's runs are admitted to the account only once the publish has run.
// The two removal records are the second (F22, D2607): with "silence keeps the
// node's value", a craft or a credential taken away without the record would go
// on being served by the node, and the credential it named would go on being
// spent.
var accountsCraftAccessWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	// A craft's access, retired with the grade's and the cascade's two retired
	// members: the credential a craft's calls go out on is the project's routing
	// table's entry for the craft's model (D3170).
	{"crafts.*.access", retiredRoutingWriter()},
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
	// A grade's access, retired the same way: the model a grade escalates to is
	// served by the routing table's entry for that model, so the key has no
	// write at either of the two layers it is spelled at.
	{"model_grades.*.access", retiredRoutingWriter()},
	{"layer.defaults.model_grades.*.access", retiredRoutingWriter()},
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

// retiredRoutingWriter is what a retired access row answers with (MG4): which
// source a model's calls go out on is the project's routing table, and
// `fugaro routing set` writes an entry of it. It exists so that the rows keep
// answering — a registered writer is what makes the refusal print a command
// line rather than a family a person would have to look up (W3) — and so that
// the line is one that runs: the head names the command, and the two words left
// in the argv are the entry's own, which ArgvFor leaves as written for the
// caller because the pattern and the chain are the table's (`<pattern>` and
// `<source>`, contract §2). The store and the node are the routing table's: the
// entry lands in this machine's config and reaches a run through the published
// policy, which is the node half of the write (D2615). No `<value>` word: a
// value of the retired key is a credential or a tier name nothing here writes.
func retiredRoutingWriter() settings.Writer {
	return settings.Writer{
		Command: "fugaro routing set",
		Argv:    []string{"routing", "set", "<pattern>", "<source>"},
		Store:   settings.StoreLocal,
		Node:    true,
	}
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
