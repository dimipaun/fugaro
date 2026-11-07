package runner

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// recordEnd is the one writer of a run's terminal status, outcome, reason and
// failure class. The class is chosen by the same decision that chooses the
// status — the site that already knows why the run is stopping passes it
// beside the reason — so the four fields are written together and a class can
// never describe a status the record does not carry.
//
// The last call wins, and that is the only rule: a run that failed and was
// then halted ends halted with the halt's class, because the halt's call is
// the last one, while the run's first failure stays in r.failure(), which is
// what the pull request's own section reads. Nothing derives a class at read
// time, nothing caches one and nothing writes one back.
func (r *run) recordEnd(status runstore.Status, outcome runstore.Outcome, class failure.Class, reason string) {
	writeEnd(r.rec, status, outcome, class, reason)
}

// writeEnd is recordEnd's rule for a record the run does not own: the copy of
// the record storeUnposted renders into the stored report, which is the one
// other record a run's ending is written to. It is here rather than beside its
// caller because a status assigned anywhere but this file is a second writer,
// which TestRecordEndIsTheOnlyWriterOfATerminalStatus exists to catch.
//
// A succeeded run carries no class whatever the caller passes: there is no
// failure to name, and a value on it would be a classification of a success.
// The rule sits in the writer rather than at each succeeded site so that the
// next such site added is covered by it too.
func writeEnd(rec *runstore.Record, status runstore.Status, outcome runstore.Outcome, class failure.Class, reason string) {
	if status == runstore.StatusSucceeded {
		class = ""
	}
	rec.Status, rec.Outcome, rec.Reason, rec.FailureClass = status, outcome, reason, class
}

// reportCopy is the record a stored report carries: the run's own record as it
// stood when the report was rendered, ended as status, outcome, class and
// reason say. It is a copy, so the run's record is untouched — storeUnposted
// renders it when a follow-up stops before updating its pull request — and it
// is here rather than at its caller because writeEnd is reached from this file
// and no other: a call to it in another file is a second writer of a status,
// and one the guard over the tree cannot tell from a legitimate one.
func reportCopy(rec *runstore.Record, status runstore.Status, outcome runstore.Outcome, class failure.Class, reason string) runstore.Record {
	c := *rec
	writeEnd(&c, status, outcome, class, reason)
	return c
}

// haltClass is which family a report groups a halt by, read from the halt's
// own closed reason. It does not replace the reason and is not a copy of it:
// the status says halted, the halt says which cap, and the class says which
// family of cause that cap is. The governance arm reads the one slice the
// checkpoint schema and the digest are pinned to, so a tripwire added there is
// classed without a second list to keep in step.
func haltClass(reason runstore.HaltReason) failure.Class {
	switch reason {
	case runstore.HaltKillSwitch:
		return failure.HaltedKillSwitch
	case runstore.HaltRunCap, runstore.HaltRepoDailyCap, runstore.HaltGlobalDailyCap,
		runstore.HaltNoCap, runstore.HaltTokenCap, runstore.HaltBudgetUnavailable,
		runstore.HaltBudgetTokenExpired:
		return failure.HaltedBudget
	case runstore.HaltNoCredential, runstore.HaltCredsRulesDenied:
		return failure.HaltedCredential
	}
	if slices.Contains(runstore.GovernanceHaltReasons(), reason) {
		return failure.HaltedGovernance
	}
	return failure.Unclassified
}
