package cli

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file registers the pool leaves `fugaro accounts pool` writes
// (docs/contracts/config-ownership.md §2, D2609). A pool is a list item of the
// local config and its leaves are held under the item's own key, so each leaf
// is registered under its pattern with the pool's name as the pattern's varying
// segment — `pools.*.owner` is written at `pools.team.owner`, and `<1>` is
// where the name goes.
//
// The store of every one of them is local: `accounts pool` writes the
// installation's own config and nothing else, and the node a pool is served
// from is not this command's to write.
//
// The value a template leaves for the caller is written as the literal
// "<value>" rather than as a constant: that is C1's own spelling of it, and
// CR1's settings.ValuePlaceholder is the same string. The pool's name in a
// template is `<1>`, the segment a `*` stands for — filled by ArgvFor once CR1
// lands, and left as written until then.
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
		Argv:    []string{"accounts", "pool", "add", "<1>", "--strategy", "<value>"},
		Store:   settings.StoreLocal,
	}},
	{"pools.*.members", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "<value>"},
		Store:   settings.StoreLocal,
	}},
	{"pools.*.owner", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--owner", "<value>"},
		Store:   settings.StoreLocal,
	}},
	{"pools.*.allowed_users", settings.Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--allow-user", "<value>"},
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
