package runstore

import (
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// The security escalation a run took (docs/security-classification/implementation-plan.md
// SC6, D1507, D1509): the run's own diff matched a row of the list in force,
// so the runner turned review level 3 on for it. It is the run's account of
// *why* its review got harder, and it is the only place that answer is kept —
// the levels and the plan a reader can see say that level 3 ran, never what
// asked for it.
//
// It is a new member of the record and nothing else changes: no existing field
// changes meaning, so no version bumps (F7). Absent means "this run escalated
// nothing" — a diff that matched no row, a run that already carried level 3,
// and every record written before this field existed alike. A row that matched
// and whose level was refused is not absent: it is the same member with Refused
// set, so a reader can tell that run from one whose diff met nothing.

// The moments the escalation is decided at (D1507). The first two are the
// runner's own seams, not D22's rigor moments: the first reads the diff once
// implement has run, the second once a fix round has. The third is finalize,
// and it is the one moment the escalation is never *taken* at: it is where the
// run reads the whole diff one last time, after committing the work the agent
// left behind, and records a refusal for a row that only reached it there —
// with no review round left to turn a level on for.
const (
	// SecurityAtImplement is the post-implement seam: the diff the implement
	// stage left.
	SecurityAtImplement = "implement"
	// SecurityAtFix is the post-fix seam, beside roundRigor: a fix round can
	// add a path the implement round did not touch.
	SecurityAtFix = "fix"
	// SecurityAtFinalize is finalize's own reading of the run's whole diff,
	// after the leftover commit. It is always a refusal: the rounds are over,
	// so the only thing the escalation can leave here is the record that it
	// did not happen.
	SecurityAtFinalize = "finalize"
)

// SecurityEscalation is the escalation a run recorded: the moment it was
// decided at, and the row that decided it.
type SecurityEscalation struct {
	// At is the moment the escalation was decided at: SecurityAtImplement or
	// SecurityAtFix for the escalation itself, and SecurityAtFinalize for a
	// refusal the run's whole diff earned there. The first moment that has
	// anything to say is kept — the escalation is raise-only, so a later moment
	// that matches again escalates nothing, and a refusal is never overwritten
	// by a second one.
	At string `json:"at"`
	// Pattern is the matched row's name, as its layer wrote it — the word that
	// answers "why did this run a level-3 review?" for whoever reads the run
	// back. Nothing draws it yet: only the record carries it, and the reader
	// that shows a run's own escalation is a later slice's. D1500 took one row
	// per pattern, which is rigor.Area's shape, so drawing it is a formatting
	// job rather than a new contract.
	Pattern string `json:"pattern"`
	// Category is the matched row's category, the word carried beside the
	// name. It is on the record rather than looked up in the list
	// again, because the list is read from the base branch and the record has
	// to explain itself without it.
	Category sensitivity.Category `json:"category"`
	// Refused says the diff matched this row and the escalation did not
	// happen: the forced resolution refused level 3, or the plan would not
	// hold it (runner's escalateSecurity). The match is recorded either way,
	// because the alternative is a record that cannot be told apart from one
	// whose diff matched nothing — level 3 off, no adversarial step, nothing
	// in the member — and no account of a matched floor row survives only as a
	// log line (D1509). A refusal does not fail the run (F9): the levels stay
	// where they are, and finalize keeps the run out of ready for it.
	//
	// It is additive with the rest of the member, so it is not in the schema's
	// required list and version stays 1: absent and false both mean the
	// escalation ran, which is every record written before this field existed.
	Refused bool `json:"refused,omitempty"`
}

// NewSecurityEscalation is the escalation to record for a row that matched at
// a moment. The name is bounded as it is written, as every other text the job
// puts in the record is: the row arrives from a list a layer wrote, and the
// record is the object every listing reads.
func NewSecurityEscalation(at string, row sensitivity.Pattern) *SecurityEscalation {
	return &SecurityEscalation{
		At:       at,
		Pattern:  clipRunes(row.Name, sensitivity.MaxPatternName),
		Category: row.Category,
	}
}

// NewRefusedSecurityEscalation is the escalation to record for a row that
// matched at a moment and was refused: the same member, carrying the row the
// run's diff met, with Refused set. Every refusal path in the runner's
// escalateSecurity records through this one, so the record never has to be
// read back to work out which of them it was.
func NewRefusedSecurityEscalation(at string, row sensitivity.Pattern) *SecurityEscalation {
	esc := NewSecurityEscalation(at, row)
	esc.Refused = true
	return esc
}
