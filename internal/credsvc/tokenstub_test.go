package credsvc

// The token store this package's tests hold the ROUTE to, rather than the
// store: one value answering the contract's three interfaces with the values a
// test sets (C1).
//
// It is a copy of bbtokens.Stub, which I1 deleted: the contract's own stub
// existed so that the tasks consuming the contract — the selection step (T8)
// and the reactions around it (T9) — could be written before the real
// implementations were, and it lived in the contract's package because there
// was nothing else to build against. There is now: bbtokens.NewSelector,
// NewProjectLookup and this service's own wiring of them (tokens_wire.go), all
// of which these tests exercise beside the route. What remains here is the
// shape only those two tasks need — a store that answers whatever a test says,
// so that the route's own behaviour is what is measured and not the selector's.

import (
	"context"

	"github.com/dimipaun/fugaro/internal/bbtokens"
)

// tokenStub answers with the values a test sets: it matches nothing, looks
// nothing up and stores nothing.
type tokenStub struct {
	// Selected is the token Select answers with. Err is what it answers
	// instead, as a *Refusal for a refusal.
	Selected bbtokens.Name
	Err      error
	// ProjectUUID is the project UUID ProjectOf answers with for every
	// repository; Err above is what it answers instead when it is set.
	ProjectUUID string
	// Evicted are the repositories Evict was called for, in order.
	Evicted []string
	// Marks are the statuses the store holds, keyed by token.
	Marks map[bbtokens.Name]bbtokens.Status
	// Counted are the tokens CountRequest was called for, in order.
	Counted []bbtokens.Name
}

var (
	_ bbtokens.Selector        = (*tokenStub)(nil)
	_ bbtokens.ProjectResolver = (*tokenStub)(nil)
	_ bbtokens.StatusStore     = (*tokenStub)(nil)
)

// Select answers the token the stub was set to, and Err when one is set.
func (s *tokenStub) Select(_ context.Context, _ bbtokens.Snapshot, _ string, _ bool) (bbtokens.Choice, error) {
	if s.Err != nil {
		return bbtokens.Choice{}, s.Err
	}
	return bbtokens.Choice{Name: s.Selected}, nil
}

// ProjectOf answers the UUID the stub was set to, and Err when one is set. It
// is one answer for every repository and every token, which is what makes it a
// stub and not a lookup.
func (s *tokenStub) ProjectOf(_ context.Context, _ string, _ bbtokens.Name) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	return s.ProjectUUID, nil
}

// Evict records the repository it was called for.
func (s *tokenStub) Evict(canonical string) { s.Evicted = append(s.Evicted, canonical) }

// Read answers the status the stub holds for the token, and the zero status for
// one it holds none for.
func (s *tokenStub) Read(_ context.Context, name bbtokens.Name) (bbtokens.Status, error) {
	return s.Marks[name], nil
}

// Write records the status the stub holds for the token.
func (s *tokenStub) Write(_ context.Context, name bbtokens.Name, st bbtokens.Status) error {
	if s.Marks == nil {
		s.Marks = make(map[bbtokens.Name]bbtokens.Status)
	}
	s.Marks[name] = st
	return nil
}

// CountRequest records the token it was called for. A later Read of it does not
// show the count: the stub counts calls rather than states, and the count a real
// store keeps in the Status is the store's to write.
func (s *tokenStub) CountRequest(_ context.Context, name bbtokens.Name) error {
	s.Counted = append(s.Counted, name)
	return nil
}
