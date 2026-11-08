package orchestrator

import (
	"fmt"
	"strings"
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
// second are the only collision, and a streak takes three cycles. The node
// never carries two at once: Decision declines while an unanswered one stands.
//
// It is the whole of what BreakerOpen recognises the pause by, and a read of
// the state cannot tell who wrote what it reads — so the class is settled at
// the write, and applyDecisionAdd refuses this prefix from every class but the
// daemon. Without that gate any writer able to add a decision, a session or a
// decision agent included, would stop every spawn on the node until a person
// resolved a question no breaker asked; the prefix is the daemon's guard, so
// the write is the daemon's. What the gate does not check is why: a daemon that
// wrote this id for something other than a trip would stop the spawns exactly
// as a trip does.
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
// cycles in a row that ended with nothing done, and the cycle that reaches the
// threshold raises a decision and stops spawning until a person answers.
//
// The count is this process's and nothing else — a daemon that crashed, that a
// supervisor restarted, or that exited for the version check starts with a
// fresh Breaker and has counted nothing. The pause is not the count: it is the
// question the trip left on the node, which is what BreakerOpen reads and what
// Plan refuses over, so a restart cannot lift it (the same rule the caps
// follow, whose counts are derived from the state rather than remembered).
type Breaker struct {
	consecutive int
}

// Observe records how one cycle ended and reports whether this is the cycle
// that reached the threshold. It is true exactly once per streak: a fourth
// failure in a row is the same question, already asked, and so is a cycle
// whose outcome this release does not know while the streak stands at the
// threshold — the report belongs to the cycle that reached it.
//
// An outcome this release does not know neither counts nor clears, so the
// streak stays where it was rather than being read as progress.
//
// What the cycle does with the report is Decision's: the question is the
// node's, and a tick commits one only when Decision hands one back.
//
// The count belongs to this process and the answer belongs to the node, so the
// loop that owns a Breaker has to re-arm it: Reset once BreakerOpen says the
// answer landed. Nothing here can do that itself — an answered question is not
// something the process can see without the state — and a breaker that is never
// re-armed reports once and then nothing, because the streak stands at the
// threshold from that cycle on and this reports only on the cycle that reaches
// it. A guard that can ask once per process is a guard a crash loop outlives
// (D1228).
func (b *Breaker) Observe(o CycleOutcome) bool {
	switch o {
	case CycleFailed, CycleEmpty:
		b.consecutive++
		return b.consecutive == BreakerThreshold
	case CycleProgress, CycleInFlight:
		// A run still going is not a failure. Counting the ticks of one long
		// run would stop a daemon that is working exactly as it should, which
		// is the opposite of what the breaker is for.
		b.consecutive = 0
	}
	return false
}

// Tripped reports whether this process's streak has reached the threshold. It
// is not the pause: the pause outlives the process, and BreakerOpen is what
// reads it off the node. It stays true until the streak is cleared — by a cycle
// that did work, or by the Reset the loop owes a breaker whose question a
// person has answered (see Observe) — so it is the thing to read before asking
// a question a second time, and not on its own a reason to hold a start back.
func (b *Breaker) Tripped() bool { return b.consecutive >= BreakerThreshold }

// Reset starts the count again, which is what a person's answer is.
func (b *Breaker) Reset() { b.consecutive = 0 }

// BreakerOpen reports whether the node carries the question a tripped breaker
// raised and nobody has answered, which is the pause itself. It is a read of
// the state and not of a Breaker, so a tick that has just started — after a
// crash, a supervisor restart, or the exit the version check takes — keeps the
// pause its predecessor's trip earned until a person answers. What ends it is
// that answer, the same write that resolves the decision: a cycle that did work
// is not one, and neither is anything else the daemon can do on its own.
//
// The id is what tells this question from every other one the loop asks a
// person, and it is the trip's own (see BreakerDecisionID). What it is read for
// is both halves of a trip: it is the pause a plan refuses over, and it is how
// Decision knows that the question a streak just reached is one the node
// already carries.
func BreakerOpen(s State) bool {
	for _, d := range s.Decisions {
		if !d.Resolved() && strings.HasPrefix(d.ID, BreakerDecisionID+"-") {
			return true
		}
	}
	return false
}

// Decision is the question a tripped breaker raises, for the tick to commit
// with the rest of its patch, and false when there is none to raise: the node
// already carries the breaker's question (see BreakerOpen), and a cycle that
// reached the threshold beside one has nothing to ask. now is the tick's
// server time, and it is what makes this trip's question its own rather than
// the first one's.
//
// A second question is the one thing this must not write. A daemon that
// restarted while a question stood counted from zero, and every cycle it ran
// since ended empty because the pause it inherited refuses every plan — so it
// reaches the threshold again with nothing new to ask. Writing the question
// anyway leaves two on the node, and the pause is every unanswered one, so a
// person answers the question they can see and the daemon stays stopped by the
// other. The count is the process's and the question is the node's, which is
// why the guard sits here and not in Observe: a report is a fact about the
// cycle, and the tick commits a question only when this says there is one.
func (b *Breaker) Decision(s State, now time.Time) (Op, bool) {
	if BreakerOpen(s) {
		return Op{}, false
	}
	return Op{Op: OpDecisionAdd, Decision: &DecisionOp{
		ID: BreakerDecisionID + "-" + now.UTC().Format("20060102T150405Z"),
		Question: fmt.Sprintf("The orchestrator has ended %d cycles in a row without starting or finishing anything. What should it do?",
			BreakerThreshold),
		WaitingOn: OnPerson,
	}}, true
}
