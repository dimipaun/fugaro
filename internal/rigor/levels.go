package rigor

import "fmt"

// The review levels a mode runs by default (L2.2, D722): the bottom of the
// precedence the level resolver starts from, before the project layer, the
// repository, the task or the owner's floor is read.
//
// Level 1 is the first-line review, level 2 the senior review that decides
// readiness, level 3 the adversarial pass after it (docs/recipes.md §2).
// Nothing acts on this yet: the resolver consumes it in L3.1, and the runner
// in L4.2.

// LevelState is what one layer says about one review level: on, off, or
// nothing at all.
type LevelState string

const (
	// LevelUnset is a layer that says nothing about the level, so the answer
	// comes from a layer below it. It is the zero LevelState, so a Levels a
	// caller builds for one level leaves the others alone rather than
	// turning them off.
	LevelUnset LevelState = ""
	// LevelOn runs the level.
	LevelOn LevelState = "on"
	// LevelOff is the configuration's `none`: the level does not run, unless
	// the owner's floor requires it (D722).
	LevelOff LevelState = "off"
)

// Levels is one layer's answer about the three review levels.
type Levels struct {
	// L1 is the first-line review.
	L1 LevelState
	// L2 is the senior review.
	L2 LevelState
	// L3 is the adversarial pass.
	L3 LevelState
}

// defaultLevels is the mode's own answer, one row per mode, and what
// DefaultLevels reads.
//
// L1 is left unset everywhere except POC, because it is not the mode's to
// set: it follows `agent.first_line_review` (config.Agent.FirstLineOn), and
// this package may not read internal/config — the mode names a level on or
// off, it does not know about the providers that decide `auto`. POC turns L2
// off, the spec's "level 1 only", so without L1 the run would carry no review
// at all and D723 makes L1 run and decide readiness instead.
var defaultLevels = map[Mode]Levels{
	POC:      {L1: LevelOn, L2: LevelOff},
	Startup:  {L2: LevelOn},
	Pro:      {L2: LevelOn},
	Critical: {L2: LevelOn, L3: LevelOn},
	Hotfix:   {L2: LevelOn},
}

// DefaultLevels is the levels mode m runs when no layer sets any: POC level 1
// only, Startup and Pro level 2, Critical level 2 and level 3, and Hotfix
// Pro's set (O10, 2026-10-30 — the spec gives it no row of its own).
//
// A mode outside the vocabulary is an error rather than a silent default. The
// two answers this code could guess at are opposite and both wrong: the
// strictest row would run a review a typo never asked for, the loosest would
// be the fail-open direction D722 exists to close.
func DefaultLevels(mode Mode) (Levels, error) {
	ls, ok := defaultLevels[mode]
	if !ok {
		return Levels{}, fmt.Errorf("rigor: no default levels for mode %q", string(mode))
	}
	return ls, nil
}
