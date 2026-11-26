package cli

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file registers the leaves `fugaro accounts pool` writes
// (docs/contracts/config-ownership.md §2, D2609). A pool is a list item of the
// local config and its leaves are held under the item's own key, so each leaf
// is registered under its pattern with the pool's name as the pattern's varying
// segment — `pools.*.owner` is written at `pools.team.owner`, and `<1>` is
// where the name goes. The removal record beside the block is the one row here
// that is not a leaf of a pool, and it carries no varying segment of its own.
//
// The store of every one of them is local: that is where the argv writes, and
// the installation's own config is what the conformance test asserts the key
// changed in. It is not the only place the value is served from, so every row
// states Node as well: the published node carries a pool to every run, which is
// the one fact a store cannot hold (D2615, docs/contracts/config-ownership.md
// §2). A hook reading the store alone would run no stage for a pool the owner
// declares and the pool would never reach a run — the `policy` stage is owed
// exactly when the store is `node` or the key's writer says Node.
//
// The removal record is the same fact one step further out (F22, D2607): the
// node keeps a pool this config says nothing about, so the name written beside
// the block is what says the pool was taken away rather than never stated. The
// node's copy is what the reconcile changes, which is why that row states Node
// too — and no reconcile is in this release, so the record is a fact in the
// file and the node goes on serving the pool until one lands.
//
// A template carries the two words ArgvFor fills: settings.ValuePlaceholder
// where the value the caller supplies goes, and `<1>` where the key's own
// segment at the pattern's first `*` goes — the pool's name, which is the
// item's key rather than a word the command line repeats.
//
// `pools.*.legacy_open` is deliberately NOT registered. The marker says a pool
// predates owners and is open to every account of the installation (D1936,
// D1942), so the migration that grandfathers such a pool is its only writer and
// `accounts pool add` takes no flag for it (D2608): there is no command line
// that changes this leaf, and registering one that does something else would be
// a writer that lies.
//
// `pools.*.cascade` is the same case one marker over (CM32): only `fugaro
// update` writes it, for the pool it migrates out of a project whose cascade
// serves (D3064 as amended), and no command line may write it either — a marker
// an operator could set on any pool would be the way past the
// subscription-spread switch that F24 found. Both leaves stand in the
// conformance ledger as rows with no writer until the migration registers its
// own, which is CM4's half.
var accountsPoolWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	// The name is the item's identity, so the line that writes it is the one
	// that creates the pool. `vertex` is the member the line names because a
	// pool with no members serves nothing (config.ValidatePools) and the
	// built-in credential is the one name every config defines without
	// declaring it, so the line is one the pool rule admits.
	{"pools.*.name", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", config.CredentialVertex},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"pools.*.strategy", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--strategy", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"pools.*.members", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	// The rule R5 adds and the principal D3181 adds: `accounts pool add` takes
	// one flag for each, the rule in its own member and the principal as the
	// pair the one flag names (--service-account writes the kind and the
	// address together, which is what makes the two rows one command line).
	{"pools.*.rule", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--rule", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"pools.*.principal.kind", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--service-account", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"pools.*.principal.id", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--service-account", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"pools.*.owner", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--owner", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{"pools.*.allowed_users", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--allow-user", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	// The removal record a pool's name is written into (F22, D2607). It is not
	// a pool leaf — the name is an item of a list beside the pools block, not a
	// field of the pool — so it is registered under its own key, and the value
	// the template leaves is the name to take away, exactly as `pools.*.members`
	// leaves the name of a member to add.
	{"node_removals.pools", settings.Writer{
		Command: "fugaro accounts pool rm",
		Argv:    []string{"accounts", "pool", "rm", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
}

// The registration is the family's own and it is fatal: a pattern two families
// claim has to fail every command of this binary rather than let one of the two
// silently win (docs/contracts/config-ownership.md §2).
func init() {
	for _, w := range accountsPoolWriters {
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}
