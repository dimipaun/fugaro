package orchestrator

import (
	"fmt"
	"time"
)

// BreakerThreshold is how many cycles in a row may end with nothing done
// before the daemon stops starting work and asks a person. One failed cycle is
// a run that failed; three in a row is the daemon itself, and the alternative
// to asking is a loop that spends a budget on cycles that achieve nothing
// (D1228).
const BreakerThreshold = 3

// BreakerDecisionID is the prefix of the id of the question a tripped breaker
// raises, and what a caller recognises the question by. The id carries the
// trip's own stamp as well: an answered decision keeps its id in the list and a
// second write of the same id is refused, so one fixed id could ask this
// question once in the life of a node and never again — the next streak would
// pause the daemon with nothing for a person to look at. Two trips inside one
// second are the only collision, and a streak takes three cycles.
const BreakerDecisionID = "breaker-tripped"

// CycleOutcome is how one cycle ended, as the breaker reads it. It is the
// tick's own summary of itself and not a run's status: what the breaker counts
// is the daemon, not the work.
type CycleOutcome string

const (
	// CycleProgress is a cycle that moved something — a run started, an item
	// folded, a decision resolved. It clears whatever came before it.
	CycleProgress CycleOutcome = "progress"
	// CycleFailed is a cycle that tried and did not get there: a start the
	// platform refused, a report it could not read.
	CycleFailed CycleOutcome = "failed"
	// CycleEmpty is a cycle with nothing to do and nothing done.
	CycleEmpty CycleOutcome = "empty"
	// CycleInFlight is a cycle whose only news is a run still going.
	CycleInFlight CycleOutcome = "in-flight"
)

// Breaker is the daemon's guard against a loop that spins: it counts the
// cycles in a row that ended with nothing done, and the tick that reaches the
// threshold raises a decision and stops spawning until a person answers.
type Breaker struct {
	consecutive int
}

// Observe records how one cycle ended and reports whether this is the cycle
// that reached the threshold. It is true exactly once per streak: a fourth
// failure in a row is the same question, already asked.
//
// An outcome this release does not know neither counts nor clears, so the
// streak stays where it was rather than being read as progress.
func (b *Breaker) Observe(o CycleOutcome) bool {
	switch o {
	case CycleFailed, CycleEmpty:
		b.consecutive++
	case CycleProgress, CycleInFlight:
		// A run still going is not a failure. Counting the ticks of one long
		// run would stop a daemon that is working exactly as it should, which
		// is the opposite of what the breaker is for.
		b.consecutive = 0
	}
	return b.consecutive == BreakerThreshold
}

// Tripped reports whether the streak has reached the threshold: the daemon
// stops spawning until a person answers the decision it raised.
func (b *Breaker) Tripped() bool { return b.consecutive >= BreakerThreshold }

// Reset starts the count again, which is what a person's answer is.
func (b *Breaker) Reset() { b.consecutive = 0 }

// Decision is the question a tripped breaker raises, for the tick to commit
// with the rest of its patch. now is the tick's server time, and it is what
// makes this trip's question its own rather than the first one's.
func (b *Breaker) Decision(now time.Time) Op {
	return Op{Op: OpDecisionAdd, Decision: &DecisionOp{
		ID: BreakerDecisionID + "-" + now.UTC().Format("20060102T150405Z"),
		Question: fmt.Sprintf("The orchestrator has ended %d cycles in a row without starting or finishing anything. What should it do?",
			BreakerThreshold),
		WaitingOn: OnPerson,
	}}
}
