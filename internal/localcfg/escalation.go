package localcfg

import (
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// escalationModeNames is the mode vocabulary as one refusal spells it, from
// the package that owns the order.
func escalationModeNames() string {
	names := make([]string, 0, len(rigor.Modes))
	for _, m := range rigor.Modes {
		names = append(names, string(m))
	}
	return strings.Join(names, ", ")
}

// The outer loop's ladder, as the local config holds it
// (docs/outer-loop-escalation/implementation-plan.md E3, spec.md
// "Configuration").
//
// These keys are thresholds, and a threshold is authority rather than
// preference (D2032, F16, G3): they decide when a stalled pull request is
// taken off its normal recipe, what it runs on then, and at what cost it is
// merged under the deterministic gate. So the local config is their only
// store. A repository's `fugaro.yaml` and the project layer refuse the block
// by name — the strict decoders of both documents have no such field — and
// Shared drops it, so a launcher who can write the runs bucket's shared file
// cannot hand every teammate a ladder of their choosing. What the project
// layer may state is a model grade, because a grade is a model choice and not
// a threshold (D2039, E2).
//
// The keys are named for rungs and not for tiers (D2033): `internal/tier`
// already means trivial/small/medium/large, so the spec's `tier2_*`,
// `tier3_*` and `tier2_coder` are `r2_*`, `r3_*` and `r2_coder` here, and the
// reconciliation maps one vocabulary to the other.
//
// The block states an override, never a whole table: the shipped rows are
// escalation.DefaultThresholds, and a key the file writes replaces one figure
// of them.

// DefaultEscalationCriticalPathFactor is the scale an item that blocks other
// work has its time thresholds multiplied by: the spec's "Critical-path PRs
// halve all times". It is stated here rather than read out of
// escalation.DefaultThresholds so the settings registry can cite the constant
// that holds the value a file that sets none gets.
const DefaultEscalationCriticalPathFactor = 0.5

// DefaultEscalationReadyDrafts is whether the orchestrator may ready a stalled
// draft on its own: off (D2046, T3). The daemon readying a draft is new
// authority over work a person left alone, so it ships off and the owner
// switches it on once, in their own file.
const DefaultEscalationReadyDrafts = false

// Escalation is the `escalation:` block. Every field is a pointer for the
// Issues block's reason: a file that writes one key leaves the rest of the
// table the shipped one, and the loader can tell "wrote nothing" from "wrote
// the value the shipped row already carries".
//
// The ranges are validate's (validateEscalation below). Nothing here is a
// figure a run may read a guess out of (G5): a threshold the decision cannot
// compare, a scale of zero and a grade outside the vocabulary are refused
// where the file is loaded, not defaulted at.
type Escalation struct {
	// R2AfterHours is escalation.r2_after_hours: how long an item may wait
	// before rung 2 runs, in every mode. 0 is legal because Hotfix's shipped
	// row is 0 — "straight to Tier 2" — and a person may deliberately put a
	// mode there.
	R2AfterHours *float64 `yaml:"r2_after_hours,omitempty"`
	// R2AfterAttempts is escalation.r2_after_attempts: how many whole runs of
	// the item's normal recipe it spends before rung 2, in every mode. Rung 2
	// comes at the time or the attempt count, whichever is first (F18).
	R2AfterAttempts *int `yaml:"r2_after_attempts,omitempty"`
	// R3AfterHours is escalation.r3_after_hours: the hard ceiling, in every
	// mode. At or past it the ladder merges under the gate rather than
	// launching another pass, so zero is refused rather than read as no
	// ceiling: it would put every item at its ceiling the moment it opened.
	R3AfterHours *float64 `yaml:"r3_after_hours,omitempty"`
	// R2Coder is escalation.r2_coder: the model grade rung 2 runs at, in every
	// mode. It names a grade and never a model, because a grade is the
	// project's model choice and this file is not where a model is chosen
	// (G11, D2033). A critical-path item's rung 2 is raised to Strongest
	// whatever this says (spec.md "Urgency modifiers").
	R2Coder *string `yaml:"r2_coder,omitempty"`
	// MergeJudge is escalation.merge_judge: the grade the advisory judge runs
	// on. It can only block (D2031), and a file that names no grade leaves the
	// judge a question rather than a default — EscalationMergeJudge answers
	// false for it.
	MergeJudge *string `yaml:"merge_judge,omitempty"`
	// CriticalPathFactor is escalation.critical_path_factor: the scale every
	// time threshold of an item that blocks other work is multiplied by. The
	// value must be inside (0, 1], which escalation.Thresholds refuses
	// anything outside of anyway.
	CriticalPathFactor *float64 `yaml:"critical_path_factor,omitempty"`
	// CostCeiling is escalation.cost_ceiling: the optional ceiling over the
	// sum of the item's own run records (D2038, G9). It has no default and no
	// "off": a file that writes none gets no cost trigger, and a written zero
	// is refused rather than read as that absence — the value the type gives
	// zero is "no ceiling", so a file saying 0 would say nothing under a word
	// that looks like a figure.
	CostCeiling *float64 `yaml:"cost_ceiling,omitempty"`
	// ReadyDrafts is escalation.ready_drafts: whether the orchestrator may
	// ready a stalled draft itself (D2041, D2046). The only key here that is
	// not a figure, and the only one whose direction is widening rather than
	// tightening — the settings writer refuses turning it on from a coding
	// agent's session for that reason.
	ReadyDrafts *bool `yaml:"ready_drafts,omitempty"`
	// Modes is the per-mode override table: what one rigor mode's row is
	// instead of what the block's scalars above say. The mode is
	// rigor.Parse's to name, so a key outside the vocabulary is a load error
	// rather than a row nothing ever reads.
	Modes map[string]EscalationMode `yaml:"modes,omitempty"`
}

// EscalationMode is one mode's line of the override table: the same four
// figures the block states for every mode, for this one mode.
//
// AtCeiling is deliberately not here. It is the spec's "At ceiling" column —
// who is told and who must approve — and it is a decision about a person
// rather than a threshold a project tunes (D2030, F3): the shipped rows keep
// it.
type EscalationMode struct {
	R2AfterHours    *float64 `yaml:"r2_after_hours,omitempty"`
	R2AfterAttempts *int     `yaml:"r2_after_attempts,omitempty"`
	R3AfterHours    *float64 `yaml:"r3_after_hours,omitempty"`
	R2Coder         *string  `yaml:"r2_coder,omitempty"`
}

// validateEscalation holds the block's keys to the ranges the decision reads.
//
// The reading is the fail-closed one throughout (G5): a figure this code
// cannot compare, a scale that means either no scaling or a threshold of zero,
// and a grade outside the vocabulary are refused here, so no run meets one and
// has to choose between guessing and stopping mid-ladder.
func (c *Config) validateEscalation(bad func(string, ...any)) {
	e := c.Escalation
	if e == nil {
		return
	}
	if v := e.R2AfterHours; v != nil && *v < 0 {
		bad("escalation.r2_after_hours %v must be zero or more hours", *v)
	}
	if v := e.R2AfterAttempts; v != nil && *v < 0 {
		bad("escalation.r2_after_attempts %d must be zero or more runs", *v)
	}
	if v := e.R3AfterHours; v != nil && *v <= 0 {
		bad("escalation.r3_after_hours %v must be more than zero hours: it is the ceiling the ladder merges at, and zero would be every item's ceiling from the moment it opened", *v)
	}
	if v := e.CriticalPathFactor; v != nil && (*v <= 0 || *v > 1) {
		bad("escalation.critical_path_factor %v must be more than 0 and at most 1: it scales every time threshold of a critical-path item, and a scale outside that range is either no scaling at all or a threshold of zero", *v)
	}
	if v := e.CostCeiling; v != nil && *v <= 0 {
		bad("escalation.cost_ceiling %v must be more than zero: a file that sets none has no cost trigger, and a written zero would be that absence stated as a figure", *v)
	}
	badEscalationGrade(bad, "escalation.r2_coder", e.R2Coder)
	badEscalationGrade(bad, "escalation.merge_judge", e.MergeJudge)
	for _, name := range slices.Sorted(maps.Keys(e.Modes)) {
		if _, ok := rigor.Parse(name); !ok {
			bad("escalation.modes.%s is not a rigor mode (%s)", name, escalationModeNames())
			continue
		}
		row := e.Modes[name]
		if v := row.R2AfterHours; v != nil && *v < 0 {
			bad("escalation.modes.%s.r2_after_hours %v must be zero or more hours", name, *v)
		}
		if v := row.R2AfterAttempts; v != nil && *v < 0 {
			bad("escalation.modes.%s.r2_after_attempts %d must be zero or more runs", name, *v)
		}
		if v := row.R3AfterHours; v != nil && *v <= 0 {
			bad("escalation.modes.%s.r3_after_hours %v must be more than zero hours: it is the ceiling the ladder merges at, and zero would be every item's ceiling from the moment it opened", name, *v)
		}
		badEscalationGrade(bad, "escalation.modes."+name+".r2_coder", row.R2Coder)
	}
}

// EscalationThresholds is the ladder's table for this config: the shipped rows
// (escalation.DefaultThresholds) with this file's keys laid over them — the
// block's scalars first, because they are every mode's, and a mode's own
// members after them, because they are that mode's alone.
//
// It answers the shipped table for a file that states no block at all, which
// is the one place the ladder defaults rather than refuses: a threshold the
// owner never chose is the spec's own, where a grade nobody chose refuses and
// asks (G5).
func (c *Config) EscalationThresholds() escalation.Thresholds {
	t := escalation.DefaultThresholds()
	e := c.Escalation
	if e == nil {
		return t
	}
	if v := e.CriticalPathFactor; v != nil {
		t.CriticalPathFactor = *v
	}
	if v := e.CostCeiling; v != nil {
		t.CostCeiling = *v
	}
	for mode, row := range t.Rows {
		if v := e.R2AfterHours; v != nil {
			row.R2Hours = *v
		}
		if v := e.R2AfterAttempts; v != nil {
			row.RunsBeforeR2 = *v
		}
		if v := e.R3AfterHours; v != nil {
			row.CeilingHours = *v
		}
		if g, ok := escalationGrade(e.R2Coder); ok {
			row.Grade = g
		}
		if own, ok := e.Modes[string(mode)]; ok {
			if v := own.R2AfterHours; v != nil {
				row.R2Hours = *v
			}
			if v := own.R2AfterAttempts; v != nil {
				row.RunsBeforeR2 = *v
			}
			if v := own.R3AfterHours; v != nil {
				row.CeilingHours = *v
			}
			if g, ok := escalationGrade(own.R2Coder); ok {
				row.Grade = g
			}
		}
		t.Rows[mode] = row
	}
	return t
}

// EscalationReadyDrafts is escalation.ready_drafts: whether the orchestrator
// may ready a stalled draft itself, and DefaultEscalationReadyDrafts when the
// file sets no block.
func (c *Config) EscalationReadyDrafts() bool {
	if c.Escalation == nil || c.Escalation.ReadyDrafts == nil {
		return DefaultEscalationReadyDrafts
	}
	return *c.Escalation.ReadyDrafts
}

// EscalationMergeJudge is escalation.merge_judge: the grade the advisory judge
// runs on, and false when the file names none. There is no default grade here:
// a judge nobody appointed is a question for the owner, not a model this code
// picks for a call that can block a merge (G5, D2031).
func (c *Config) EscalationMergeJudge() (escalation.Grade, bool) {
	if c.Escalation == nil {
		return "", false
	}
	return escalationGrade(c.Escalation.MergeJudge)
}

// badEscalationGrade refuses a grade name outside the vocabulary. It names the
// vocabulary rather than the value's own spelling: the near misses a person
// writes here are model names ("sonnet") and the rungs' old word ("tier2"),
// and both are answered by saying what a grade is.
func badEscalationGrade(bad func(string, ...any), key string, v *string) {
	if v == nil || escalation.KnownGrade(escalation.Grade(*v)) {
		return
	}
	bad("%s %q is not a model grade (%s)", key, *v, escalationGradeNames())
}

// escalationGradeNames is the grade vocabulary as one refusal spells it, from
// the package that owns it.
func escalationGradeNames() string {
	names := make([]string, 0, len(escalation.Grades))
	for _, g := range escalation.Grades {
		names = append(names, string(g))
	}
	return strings.Join(names, ", ")
}

// escalationGrade is the grade a written value names, and false for one the
// vocabulary does not hold. validate refuses such a value at load, so this is
// the reading of a name that reached here without having been parsed.
func escalationGrade(s *string) (escalation.Grade, bool) {
	if s == nil {
		return "", false
	}
	g := escalation.Grade(*s)
	if !escalation.KnownGrade(g) {
		return "", false
	}
	return g, true
}
