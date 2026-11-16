package orchestrator

// This file is compiled into this package's tests and into nothing else. It
// exposes the two values a test asserts against the code's own: a copy of
// either in a test would go on passing after the code moved.

// TriageOrder is the fixed order Triage tries the four outcomes in, as a copy
// a test can compare or change without reaching into the package's slice.
func TriageOrder() []TriageKind { return append([]TriageKind(nil), triageOutcomeOrder...) }

// TriageTransition is the audit transition one run's triage is appended under
// (D1247).
const TriageTransition = triageTransition

// MergeAsIsGate is the merge's own decider, on its own. The rule tries it
// after the fail-closed outcome, so the checks it makes for itself are a
// defence in depth, and a test that drove only Triage could not tell whether
// the gate holds without the order.
func MergeAsIsGate(in TriageInput) (TriageOutcome, bool) { return triageMergeAsIs(in) }
