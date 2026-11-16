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
// The store of every one of them is local: `accounts pool` writes the
// installation's own config and nothing else, and the node a pool is served
// from is not this command's to write.
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
	}},
	{"pools.*.strategy", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--strategy", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
	}},
	{"pools.*.members", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
	}},
	{"pools.*.owner", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--owner", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
	}},
	{"pools.*.allowed_users", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--allow-user", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
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
