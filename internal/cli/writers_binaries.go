package cli

import "github.com/dimipaun/fugaro/internal/settings"

// This file registers the two rows `fugaro project binaries` writes
// (docs/contracts/config-ownership.md §2 and §4: the binaries family is K2's).
// Each row is registered as the command line that writes it, so that a refusal
// for one of them names the command that works and T1's conformance test can run
// it (D2609, D2610).
//
// The store is local for both: that is where the argv writes, and this machine's
// project config is what the conformance test asserts the key changed in. It is
// not the only place the block is served from — the published node is what a run
// reads, and the block is carried there by the publish the command's own hook
// runs — so both rows state Node as well, which is the fact a store cannot hold
// (D2615, docs/contracts/config-ownership.md §2). A hook reading the store alone
// would run no stage for a grant the owner states and the grant would never
// reach a run.
//
// The two value words are the caller's: a pattern and a byte count. Neither
// template carries a `<1>`: the keys have no varying segment, and the words the
// command takes are values rather than names of the key.
//
// The registration is a package init and its error is a programming error, as it
// is for every family's: a pattern another family already claimed, a store
// outside the vocabulary or an overlap with a pattern of the same shape is a
// diff to settle rather than a registration to win, so this panics and every
// command of the binary fails loudly instead of one of the two silently winning
// (contract §2).
func init() {
	for _, w := range binariesWriters {
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}

// binariesWriters is the table: every key `fugaro project binaries` writes.
var binariesWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	{binariesAllowKey, settings.Writer{
		Command: "fugaro project binaries",
		Argv:    []string{"project", "binaries", "allow", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
	{binariesMaxBytesKey, settings.Writer{
		Command: "fugaro project binaries",
		Argv:    []string{"project", "binaries", "max-bytes", settings.ValuePlaceholder},
		Store:   settings.StoreLocal,
		Node:    true,
	}},
}
