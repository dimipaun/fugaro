package orchestrator

import "time"

// The windows a spawn cap counts over. windowDay is RetentionWindow on
// purpose: retention never drops an intent younger than that, so the longest
// window a cap reads can never be shortened by an eviction — a count an
// eviction could lower is a cap an old run could lift.
const (
	windowMinute = time.Minute
	windowHour   = time.Hour
	windowDay    = RetentionWindow
)

// Caps are the guardrails one cycle plans under: how many runs may be going at
// once, how many one cycle may start, how many may start in the last minute,
// hour and day, and the switch that stops every start at once.
//
// A cap of zero is no room at all, and so is a negative one. A caller that
// never set a field, or set it to nonsense, gets a plan that starts nothing
// rather than one that starts without a bound; O8 fills these from the
// policy's orchestrator block, whose defaults are what a person who never
// touched them gets.
type Caps struct {
	MaxRunning     int
	MaxPerCycle    int
	SpawnPerMinute int
	SpawnPerHour   int
	SpawnPerDay    int
	// KillSwitch is the budget kill switch. Whoever makes a plan reads it and
	// hands the answer in here, because a plan is pure and asks no cloud: the
	// tick reads it over budget (`Kills.On`, which is IO) and a switch it could
	// not read is on.
	KillSwitch bool
}

// Counts is what the committed intents say has been spent: the runs still in
// flight, and the attempts committed in the windows the caps count over. It is
// derived from the state and the server time and never kept in memory, so a
// daemon that has just started counts what the node says rather than what it
// saw.
type Counts struct {
	Running int
	Minute  int
	Hour    int
	Day     int
}

// CountsOf derives the counts from the intents a state carries. An intent is
// the record of a run — the run id is committed before anything starts — so
// one with no outcome is a run in flight, and an attempt that never started
// counts too: it spent the same allowance, and counting it is the direction
// that stops a spawn.
func CountsOf(s State, now time.Time) Counts {
	var c Counts
	now = now.UTC()
	for _, it := range s.Intents {
		if it.Outcome == "" {
			c.Running++
		}
		// A stamp in the future counts in every window, because its age is
		// negative and so below each of them. The stamps come from the server's
		// clock and not this process's, so that is the safe direction: the one
		// reading that would matter is an odd stamp freeing a cap.
		age := now.Sub(it.At.UTC())
		if age < windowMinute {
			c.Minute++
		}
		if age < windowHour {
			c.Hour++
		}
		if age < windowDay {
			c.Day++
		}
	}
	return c
}

// Room reports whether one more run may start beside the ones this cycle has
// already planned. Every cap has to hold, and the planned runs count against
// all of them: a cycle plans a list, and it is the list the caps bound.
func (c Counts) Room(caps Caps, planned int) bool {
	return c.Running+planned+1 <= caps.MaxRunning &&
		planned+1 <= caps.MaxPerCycle &&
		c.Minute+planned+1 <= caps.SpawnPerMinute &&
		c.Hour+planned+1 <= caps.SpawnPerHour &&
		c.Day+planned+1 <= caps.SpawnPerDay
}
