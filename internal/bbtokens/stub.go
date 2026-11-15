package bbtokens

import "context"

// Stub is the contract's placeholder for the real store. It answers the three
// interfaces with values a test sets, so that the tasks consuming them — the
// proxy's selection step (T8) and the reactions around it (T9) — compile and
// can be tested against the contract while the real selector (T4), resolver
// (T5) and status store (T6) are still being written.
//
// It matches nothing, looks nothing up and stores nothing: every answer is a
// field. I1 deletes this file and wires the real implementations.
type Stub struct {
	// Selected is the token Select answers with. Err is what it answers
	// instead, as a *Refusal for a refusal.
	Selected Name
	Err      error
	// ProjectUUID is the project UUID ProjectOf answers with for every
	// repository; Err above is what it answers instead when it is set.
	ProjectUUID string
	// Evicted are the repositories Evict was called for, in order.
	Evicted []string
	// Marks are the statuses the store holds, keyed by token.
	Marks map[Name]Status
	// Counted are the tokens CountRequest was called for, in order.
	Counted []Name
}

// Select answers the token the stub was set to, and Err when one is set.
func (s *Stub) Select(_ context.Context, _ Snapshot, _ string, _ bool) (Choice, error) {
	if s.Err != nil {
		return Choice{}, s.Err
	}
	return Choice{Name: s.Selected}, nil
}

// ProjectOf answers the UUID the stub was set to, and Err when one is set.
// It is one answer for every repository and every token, which is what makes
// it a stub and not a lookup.
func (s *Stub) ProjectOf(_ context.Context, _ string, _ Name) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	return s.ProjectUUID, nil
}

// Evict records the repository it was called for.
func (s *Stub) Evict(canonical string) { s.Evicted = append(s.Evicted, canonical) }

// Read answers the status the stub holds for the token, and StateAvailable for
// one it holds none for.
func (s *Stub) Read(_ context.Context, name Name) (Status, error) {
	return s.Marks[name], nil
}

// Write records the status the stub holds for the token.
func (s *Stub) Write(_ context.Context, name Name, st Status) error {
	if s.Marks == nil {
		s.Marks = make(map[Name]Status)
	}
	s.Marks[name] = st
	return nil
}

// CountRequest records the token it was called for. A later Read of it does
// not show the count: the stub counts calls rather than states, and the count
// a real store keeps in the Status is T6's to write.
func (s *Stub) CountRequest(_ context.Context, name Name) error {
	s.Counted = append(s.Counted, name)
	return nil
}
