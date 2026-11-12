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
