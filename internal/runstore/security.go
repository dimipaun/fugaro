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
// and every record written before this field existed alike.

// The two moments the escalation is decided at (D1507). Both are the runner's
// own seams, not D22's rigor moments: the first reads the diff once implement
// has run, the second once a fix round has.
const (
	// SecurityAtImplement is the post-implement seam: the diff the implement
	// stage left.
	SecurityAtImplement = "implement"
	// SecurityAtFix is the post-fix seam, beside roundRigor: a fix round can
	// add a path the implement round did not touch.
	SecurityAtFix = "fix"
)

// SecurityEscalation is the escalation a run recorded: the moment it was
// decided at, and the row that decided it.
type SecurityEscalation struct {
	// At is the moment the run escalated at: SecurityAtImplement or
	// SecurityAtFix. The first one a run takes is kept — the escalation is
	// raise-only, so a later moment that matches again escalates nothing.
	At string `json:"at"`
	// Pattern is the matched row's name, as its layer wrote it — what the
	// record, the pull request body and fugaro watch print when they say which
	// rule decided (D1500).
	Pattern string `json:"pattern"`
	// Category is the matched row's category, the word printed beside the
	// name. It is carried on the record rather than looked up in the list
	// again, because the list is read from the base branch and the record has
	// to explain itself without it.
	Category sensitivity.Category `json:"category"`
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
