package escalation

import "github.com/dimipaun/fugaro/internal/rigor"

// The threshold table (spec.md "Default thresholds per rigor mode"): what each
// rigor mode makes the ladder do, as data rather than as a condition inside
// the decision.
//
// Thresholds are authority and not preference, so this table is the local
// config's to override and nobody else's: a repository file and the project
// layer may not set one, which is why the keys live in internal/localcfg and
// are refused at load anywhere else (D2032, F16, G3). What the project layer
// may set is a grade, because a grade is a model choice (D2039).

// CeilingAction is what rung 3 does in one mode: the spec's "At ceiling"
// column, which is who is told and who must approve — never whether the gate
// applies, which is D1245's and the same everywhere (D2030, F3).
type CeilingAction string

const (
	// CeilingAutoMerge is POC's and Startup's row: merge under the gate and
	// tell nobody (spec.md "Human sign-off on Tier 3 merges"). "Auto" is the
	// daemon's own readiness write too when the local switch is on (D2041);
	// the gate itself is unchanged.
	CeilingAutoMerge CeilingAction = "auto_merge"
	// CeilingAutoMergeNotify is Pro's and Hotfix's row: the same merge, plus
	// one notification of kind `merged-needs-review` (E10b, F13).
	CeilingAutoMergeNotify CeilingAction = "auto_merge_notify"
	// CeilingWaitPerson is Critical's row and every non-Routine path set in
	// any mode: the verdict is computed and nothing merges without a person
	// (D2037, G10).
	CeilingWaitPerson CeilingAction = "wait_person"
)

// CeilingActions is the whole vocabulary, in the spec's table order. It is
// closed: a row that names anything else is a table this code refuses to read.
var CeilingActions = []CeilingAction{CeilingAutoMerge, CeilingAutoMergeNotify, CeilingWaitPerson}

// KnownCeilingAction reports whether a is one of CeilingActions. The zero
// action is not.
func KnownCeilingAction(a CeilingAction) bool {
	for _, known := range CeilingActions {
		if a == known {
			return true
		}
	}
	return false
}

// Row is one mode's line of the spec's table.
type Row struct {
	// RunsBeforeR2 is the "Normal runs before Tier 2" column: how many whole
	// runs of the item's normal recipe it spends before rung 2. It counts
	// runs, not review rounds, and only runs the item's own approval launched
	// (F18, G15; escalation.r2_after_attempts).
	RunsBeforeR2 int
	// R2Hours is the time half of the same cell — the "~3 h", "~6 h" and
	// "~9 h" beside the count: rung 2 applies past a time or an attempt
	// threshold, whichever comes first (escalation.r2_after_hours). It is 0
	// for Hotfix, whose row is "straight to Tier 2".
	R2Hours float64
	// Grade is the model grade rung 2 runs at (escalation.r2_coder). A
	// critical-path item's rung 2 is raised to Strongest whatever this says
	// (spec.md "Urgency modifiers").
	Grade Grade
	// CeilingHours is the "Hard ceiling" column: at or past it the ladder
	// merges under the gate rather than launching another pass
	// (escalation.r3_after_hours).
	CeilingHours float64
	// AtCeiling is what rung 3 does in this mode (the "At ceiling" column).
	AtCeiling CeilingAction
}

// Thresholds is everything the decision reads besides the item's own facts:
// one row per mode, the factor a critical-path item's times are scaled by, and
// the optional cost ceiling.
//
// The zero Thresholds refuses every decision rather than answering one: a
// caller that has not built a table has no thresholds to read, and a default
// here would be the fail-open direction (G5).
type Thresholds struct {
	// Rows is the per-mode table. A mode with no row refuses.
	Rows map[rigor.Mode]Row
	// CriticalPathFactor scales every time threshold of an item that blocks
	// other work: 0.5 by the spec ("Critical-path PRs halve all times",
	// escalation.critical_path_factor). A value outside (0, 1] refuses,
	// because a scale this code cannot read is either no scaling at all or a
	// threshold of zero, and both would escalate on a clock nobody set.
	CriticalPathFactor float64
	// CostCeiling is the optional ceiling over the sum of the item's own run
	// records (D2038, G9; escalation.cost_ceiling). Zero means no ceiling:
	// the key has no default, so a person who set none gets no cost trigger.
	// It is read against Elapsed's own clock only and never scaled by the
	// critical-path factor, which is the spec's "all times" and not "all
	// figures".
	CostCeiling float64
}

// DefaultThresholds is the spec's table: the rows the ladder ships with, one
// per mode, before any local key overrides one.
//
// It is Hotfix's row that is the odd one, and deliberately so: zero runs
// before rung 2 is "straight to Tier 2", which the decision answers with the
// hotfix reason rather than with the attempt count, and its ceiling is the
// shortest — three hours, about the length of the one normal run it never
// waits for (F18).
//
// Every call returns a fresh table, so a caller that edits its copy — E3's
// local keys do, one row at a time — cannot change what the next caller reads.
func DefaultThresholds() Thresholds {
	return Thresholds{
		Rows: map[rigor.Mode]Row{
			rigor.POC:      {RunsBeforeR2: 1, R2Hours: 3, Grade: Strong, CeilingHours: 5, AtCeiling: CeilingAutoMerge},
			rigor.Startup:  {RunsBeforeR2: 2, R2Hours: 6, Grade: Strong, CeilingHours: 9, AtCeiling: CeilingAutoMerge},
			rigor.Pro:      {RunsBeforeR2: 3, R2Hours: 9, Grade: Strong, CeilingHours: 12, AtCeiling: CeilingAutoMergeNotify},
			rigor.Critical: {RunsBeforeR2: 3, R2Hours: 9, Grade: Strongest, CeilingHours: 12, AtCeiling: CeilingWaitPerson},
			rigor.Hotfix:   {RunsBeforeR2: 0, R2Hours: 0, Grade: Strongest, CeilingHours: 3, AtCeiling: CeilingAutoMergeNotify},
		},
		CriticalPathFactor: 0.5,
	}
}
