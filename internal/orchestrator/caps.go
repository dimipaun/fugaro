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
// never set a field, or set it to nonsense, gets a plan that starts nothing new
// rather than one that starts without a bound: the launches and retries a cycle
// plans carry every one of these, and a number nobody set holds them all back.
// What no cap bounds is the other half of a plan — a re-issue, which starts the
// attempt that was already committed and counted, and an abandonment, which
// closes one that may not start: both are decided before the caps are read, and
// neither spends the allowance a cap exists to bound. O8 fills these from the
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
// derived from the states and the server time and never kept in memory, so a
// daemon that has just started counts what the nodes say rather than what it
// saw.
type Counts struct {
	Running int
	Minute  int
	Hour    int
	Day     int
}

// Plus is one reading of the caps added to another: one node's counts beside
// another's. It is what a project's count is made of (CountsOfAll).
func (c Counts) Plus(o Counts) Counts {
	return Counts{
		Running: c.Running + o.Running,
		Minute:  c.Minute + o.Minute,
		Hour:    c.Hour + o.Hour,
		Day:     c.Day + o.Day,
	}
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

// CountsOfAll derives the counts from every node of one project's subtree: its
// state node and, beside it, one node per imported plan and the standing
// backlog (D2735). D2743 counts max_running and the spawn caps over the whole
// project and never per node — a plan's node is one daemon's work like the
// state node is, and an allowance each node counted alone would multiply by
// however many plans the project imported. One node's reading is what it always
// was: the sum of one state is that state's own counts.
func CountsOfAll(states []State, now time.Time) Counts {
	var c Counts
	for _, s := range states {
		c = c.Plus(CountsOf(s, now))
	}
	return c
}

// Room reports whether one more run may start beside the ones this cycle has
// already planned. Every cap has to hold, and the planned runs count against
// all of them: a cycle plans a list, and it is the list the caps bound. planned
// is that list's length so far, and a cycle that works several nodes counts it
// across all of them (D2743) the way it counts the intents.
func (c Counts) Room(caps Caps, planned int) bool {
	return c.Running+planned+1 <= caps.MaxRunning &&
		planned+1 <= caps.MaxPerCycle &&
		c.Minute+planned+1 <= caps.SpawnPerMinute &&
		c.Hour+planned+1 <= caps.SpawnPerHour &&
		c.Day+planned+1 <= caps.SpawnPerDay
}
