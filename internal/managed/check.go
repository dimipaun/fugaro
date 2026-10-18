package managed

import "fmt"

// The inventory's answer as the apply takes it (Task 4.5, design D59).
//
// Why this is a type of its own rather than a Decision field: what the apply
// has to know is not only what the inventory decided, but WHETHER THE READ WAS
// MADE AT ALL — and the two questions have opposite zero values. A Decision's
// zero value is a clean project (Fallback false, no cause, nothing foreign),
// which is the right answer for "the read was made and found nothing", and it
// is the WRONG answer for "no read was made": a caller that forgot to thread
// the read through would hand the apply a clean project and the apply would
// remove the coding agent's refusal for a project nobody looked at. So the
// zero value of this type is the other branch — the fallback that keeps the
// confirmations — and the other one has to be asked for by name, with a
// Decision the caller made the read to get (CheckedInventory).
//
// It mirrors enable.go's Enable: unexported fields, one constructor, the safe
// answer at the zero value, and the whole of the behaviour in one predicate
// (Fallback) and one sentence (Why).

// InventoryCheck is the whole-project inventory of a managed apply: the read
// the caller made, and what it decided.
type InventoryCheck struct {
	// read is whether a whole-project inventory was made for this run. The
	// zero value is false, so the zero value falls back.
	read bool
	// decision is what that read decided (Inventory.Check's own contract).
	decision Decision
}

// CheckedInventory is the way to hold a check that reports a read: the caller
// that made the read passes what it decided, and this is what turns "no read"
// into "read, and here is the answer".
//
// It is not a proof that a read happened, and it does not try to be: Decision
// is exported, so a caller can build a clean one by hand and pass it here.
// What protects the apply is the zero value — a caller that never mentions the
// inventory at all holds the fallback — and the fact that the two callers that
// do mention it (internal/cli's managed apply, through Inventory.Check or
// Unread) are the ones that read. A caller that goes out of its way to
// construct a clean Decision is lying about a read, which no type in this
// package can prevent; the tests that pin the wiring are what catch it
// (TestApplyConsultsTheInventory in this package, and the command's own).
func CheckedInventory(d Decision) InventoryCheck { return InventoryCheck{read: true, decision: d} }

// Fallback reports whether the run keeps its confirmations: the decision fell
// back, or no read was made at all. It is D59's answer as a boolean — a
// resource the state does not account for, or an inventory that could not be
// read, keeps the confirmations of the run rather than halting it or applying
// beside it.
func (c InventoryCheck) Fallback() bool { return !c.read || c.decision.Fallback }

// Why is the sentence a person reads about the fallback: the inventory's own
// warning when there was a read, and this program's own sentence when there
// was none. It is empty when the run does not fall back, and it is never
// printed by this package's callers without being bounded (the apply renders
// it through safetext, since a foreign resource's name comes from the project
// and the cause's detail from an API).
func (c InventoryCheck) Why() string {
	if !c.read {
		return NotCheckedWhy
	}
	return c.decision.Why
}

// Cause is why the run fell back, or "" when it did not. It is the inventory's
// own cause when there was a read, and CauseNoReader when there was none.
func (c InventoryCheck) Cause() Cause {
	if !c.read {
		return CauseNoReader
	}
	return c.decision.Cause
}

// NotCheckedWhy is what an apply says when no whole-project inventory was made
// for the run at all. The sentence is here and not in the caller because it is
// the same fallback D59 answers with for a read that failed, said for the one
// failure that happens before the read: nothing was looked at, so the run
// keeps its confirmations.
const NotCheckedWhy = "no whole-project inventory was read for this run, so nothing was checked against the project's own resources; this run falls back to confirmations, and nothing is deleted (D59)"

// Unread is the decision for an inventory the caller could not read at all:
// the same fallback Inventory.Check produces when its read fails, said for the
// failures that happen outside it — no Cloud Asset client to read with, or the
// installation's own state not being readable, which is the accounted side of
// the comparison. The cause and the detail are the caller's, and both are
// rendered into one sentence here so there is one wording for a fallback.
//
// A caller that cannot read must pass this rather than an empty Decision: an
// empty Decision is a clean project, and reporting a read that did not happen
// as a project with nothing foreign in it is the one direction D59 and Global
// Constraint 3 forbid.
func Unread(project, gcpProject string, cause Cause, detail string) Decision {
	return Inventory{Project: project, GCPProject: gcpProject}.fallback(cause, detail)
}

// String is the check as a line for a test or a log: whether it was read, and
// what it decided. It exists so no test has to read the unexported fields.
func (c InventoryCheck) String() string {
	if !c.read {
		return fmt.Sprintf("inventory: not read (fallback: %s)", CauseNoReader)
	}
	return fmt.Sprintf("inventory: read, fallback=%v, cause=%q", c.decision.Fallback, c.decision.Cause)
}
