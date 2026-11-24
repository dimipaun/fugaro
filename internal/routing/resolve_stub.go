package routing

// This file is the one part of the table contract that goes away: the resolver
// C1 ships, which answers the table's first entry for every model until RT1
// supplies the real one. I1 deletes this file, so the deletion is a deletion
// and not an edit, and every test about the resolver's answer rather than about
// the stub lives in resolve_test.go, which stays.
//
// What it is for is the contract arriving before its implementation: RT3's
// checks, CS1's chain walk and MG1's migration are written against Resolve's
// signature, and the signature has to answer before RT1 teaches it to weigh
// patterns. The answer it gives in the meantime is the one a test can tell
// apart from a real one — the first entry, whatever the model — because RT1's
// own red step is exactly that difference (TestResolveMostSpecificWins fails
// against this stub). A stub that guessed the most specific match would let
// RT1's test pass before RT1 existed.
//
// **It decides nothing.** It matches nothing, weighs nothing and reads no
// pattern: two different models get the same entry. That is the stub rule the
// other scaffolding in this tree follows (internal/credsvc's
// provider_wire_stub.go, internal/cli's after_write_stub.go).

// stubResolve is the resolver's body until RT1 supplies the real one: the first
// entry, and ErrNoEntry for a table with no entries at all.
//
// The empty table is the one case it does answer correctly, and on purpose: it
// is the answer that serves nobody, which is the direction this contract fails
// in everywhere (a model no entry matches is refused, never served by whatever
// credential is around). An empty table answering "no entry" is also what makes
// a side written against C1 safe to run before RT1 lands — a project with no
// table yet reaches no model, rather than reaching one nobody routed.
func stubResolve(table Table, _ string) (Entry, error) {
	if len(table) == 0 {
		return Entry{}, ErrNoEntry
	}
	return table[0], nil
}

// The assignment stands only while nothing else has made one.
//
// The guard is not decoration, because the filename order that carries the same
// job in internal/cli's scope_stub.go carries it the other way here: this file
// sorts AFTER the real resolver's (resolve.go), so an unguarded init here would
// run last and put the stub back over the real one — RT1's assignment would be
// undone by C1's own scaffolding, silently, with every test still compiling.
// Asking whether Resolve is still nil makes this file what it is meant to be
// whatever the real one is called: the answer that stands until a real one
// exists, and never one that stands over a real one.
func init() {
	if Resolve == nil {
		Resolve = stubResolve
	}
}
