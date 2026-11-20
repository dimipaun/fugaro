package creds

import (
	"reflect"
	"slices"
	"testing"
)

// TestASelectionReachesTheCascadeStateAndNotTheAuthRecord pins the split the
// scope contract leans on (C1's rule 2, docs/workspace-service/contract-scope.md):
// a selection reads the cascade's shared state — six methods, CascadeState —
// and the per-repository auth record is not one of them.
//
// It is asserted rather than left to one field's text because the text is all
// there is: reverting Selector.Store to the wide Store compiles, and both the
// creds and the credsvc suites stay green. What that would let in is a
// selection — or any caller handed the store a selection holds — reaching
// RepoAuth through the state, and in a workspace-mode process (D2980) that
// handle is the WORKSPACE's database, where a repository's own record is not
// written: the member's own `fugaro update` writes it into the member's, and
// narrowToRepoAuth reads an absent record as "the cascade, unchanged"
// (cascade.go, D2070 (a)), which serves a member's run the whole of the
// workspace's pool.
//
// Mutation (run, restore): `Store CascadeState` → `Store Store` in select.go.
func TestASelectionReachesTheCascadeStateAndNotTheAuthRecord(t *testing.T) {
	state := reflect.TypeOf((*CascadeState)(nil)).Elem()
	want := []string{"Current", "Leases", "Marks", "RoundRobin", "SetPlacement", "UpdateMark"}
	if got := methodNames(state); !slices.Equal(got, want) {
		t.Errorf("creds.CascadeState's methods are %v, want %v: these six are what a selection reads and writes, and the list IS the split — a method beside them is a reader a selection can reach", got, want)
	}
	if _, ok := state.MethodByName("RepoAuth"); ok {
		t.Error("creds.CascadeState carries RepoAuth: a repository's auth record is its own configuration and not the state a call is served out of (D2070 (a)), and a workspace's state store is over its own database, so a selection able to reach the record through its state would answer it out of the wrong one")
	}

	sel := reflect.TypeOf(Selector{})
	f, ok := sel.FieldByName("Store")
	if !ok {
		t.Fatal("creds.Selector has no Store field: the cascade's state is where a selection reads every mark, lease and placement from (select.go)")
	}
	if f.Type != state {
		t.Errorf("creds.Selector.Store is %v, want creds.CascadeState (%v): a selection reads the six methods above and never the auth record, and the field's type is what holds that (docs/workspace-service/contract-scope.md, rule 2)", f.Type, state)
	}

	// The other direction, so the split cannot drift by dropping the record:
	// the wide store keeps RepoAuth, which is what the service's own record
	// read is answered through (Config.State, cascade.go).
	wide := reflect.TypeOf((*Store)(nil)).Elem()
	wantWide := []string{"Current", "Leases", "Marks", "RepoAuth", "RoundRobin", "SetPlacement", "UpdateMark"}
	if got := methodNames(wide); !slices.Equal(got, wantWide) {
		t.Errorf("creds.Store's methods are %v, want %v: the wide store is the cascade's state PLUS the per-repository auth record, and a store that lost the record would leave the service unable to narrow a repository's reach at all", got, wantWide)
	}
}

// methodNames is the method set of one interface in the order reflect presents
// it, which is lexicographic for an interface type: the two lists above are
// therefore compared in one order, and a reader can check them against the
// declarations without sorting anything by hand.
func methodNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	return names
}
