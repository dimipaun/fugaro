package craft

// This file is the scaffolding of C1, and it is the one part of this package
// that goes away: I1 puts CM1's resolver (ByReach, reach.go) behind Resolver and
// deletes this file with the runner's own stub.
//
// What it is for is the sides of the contract being written at once. C1 lands
// before CM1 — the reach resolver is a task of its own — and before CM8, so a
// run's model path has to be answerable in the meantime: a side written against
// C1 compiles and behaves as TODAY, which is exactly what the migration's rule
// answers for a repository no update has migrated. So the stub answers
// LegacyAccess and nothing else, and it is deliberately not a policy of its own:
// a stub that answered a shape of its own would be a second rule, which is the
// drift the contract exists to prevent.
//
// It answers nothing where the rule answers nothing — a stage this vocabulary
// has no craft for, and a file that writes none of the retired keys — which is
// the fail-closed direction every stub in this tree takes, and the one F49
// requires: no answer is a run refused with the fix (LegacyNoAccessRefusal),
// never a run sent to Vertex.

// stubResolver answers the migration's rule from the retired vocabulary it was
// built with.
type stubResolver struct{ in LegacyInput }

// StubResolver is a Resolver that answers the migration's rule alone. Its
// argument is the retired vocabulary in force, read by the caller (the runner's
// access_resolve, CM8) and never by this package.
func StubResolver(in LegacyInput) Resolver { return stubResolver{in} }

// AccessFor answers what the retired keys say about this stage, and whether they
// say anything at all.
func (s stubResolver) AccessFor(stage, grade string) (Access, bool) {
	return LegacyAccess(s.in, stage, grade)
}
