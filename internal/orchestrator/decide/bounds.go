package decide

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// ErrCallCap is a decision call the state's own counts refuse: the node has
// already recorded as many calls as the window allows. It is a refusal and not
// a failure — nothing was attempted — and it is the one dollar control a local
// daemon has, because a decision runs on the person's own Claude login and
// Fugaro's gateway budget does not cover it (D1228).
var ErrCallCap = errors.New("decide: the decision agent has made all the calls its caps allow")

// The bounds D1228 proposes. They are the proposals and not measurements
// (U4): Check 303 — one real decision call, the caps and the breaker proved,
// the cost of a cycle measured — is what confirms or replaces them, which is
// why they are fields of Bounds rather than constants the caller cannot move.
const (
	// DefaultCallsPerHour and DefaultCallsPerDay are the count caps. Most
	// cycles should not call a model at all, so six an hour is already a
	// daemon that is deciding far more often than it is working.
	DefaultCallsPerHour = 6
	DefaultCallsPerDay  = 40
	// DefaultMaxTurns is how many turns one call may take. A decision is a
	// judgement over a brief that is already in the prompt: there is nothing
	// to look up, and a call that needs turns is a call that is using tools
	// it should not have.
	DefaultMaxTurns = 3
	// DefaultMaxOutputTokens is the call's output pin (owner lesson 21: a
	// model whose maximum output is under 32000 broke every review until
	// agent.max_output_tokens.reviewer was set). A decision answers with a
	// reasoning and a handful of ops, so the pin is a bound and not a
	// budget.
	DefaultMaxOutputTokens = 8000
)

// Bounds is how far one decision call may go: how many of them the node may
// make in an hour and in a day, how many turns one may take, and how much
// output it may produce. Every field's zero value means "the default above",
// so a Runner that carries no bounds at all is bounded by the proposals.
type Bounds struct {
	CallsPerHour    int
	CallsPerDay     int
	MaxTurns        int
	MaxOutputTokens int64
}

// WithDefaults is b with every unset field replaced by the proposal above. A
// negative field is unset too: the numbers are a bound, and a caller that
// wrote a nonsensical one gets the bound rather than no bound.
func (b Bounds) WithDefaults() Bounds {
	if b.CallsPerHour <= 0 {
		b.CallsPerHour = DefaultCallsPerHour
	}
	if b.CallsPerDay <= 0 {
		b.CallsPerDay = DefaultCallsPerDay
	}
	if b.MaxTurns <= 0 {
		b.MaxTurns = DefaultMaxTurns
	}
	if b.MaxOutputTokens <= 0 {
		b.MaxOutputTokens = DefaultMaxOutputTokens
	}
	return b
}

// Allow reports whether one more decision call may be made at now, or the cap
// that refuses it.
//
// The counts are read off the state (orchestrator.DecisionCalls) and never
// kept in this process: every recorded decision counts, whoever recorded it —
// a session that asked a question, a person who answered one — because what
// the caps bound is the money a model call spends and the node is the only
// place that survives a restart. The derivation is taken at now rather than
// read from the document's counters field, which is the same number as of the
// last write: a decision an hour old is still in the day's count and no
// longer in the hour's, and only the derivation knows that.
//
// What the count is a count of is the calls themselves: this package writes
// one record per call into the state before the child is started (recordCall),
// and hands that state back as the decision's Next, so a caller that commits
// what it is handed spends calls these caps see. The one thing the caps cannot
// see is a caller that makes a call and then drops the state it came back
// with: the count lives in the document, and only the document survives the
// process.
func (b Bounds) Allow(s orchestrator.State, now time.Time) error {
	b = b.WithDefaults()
	c := orchestrator.DecisionCalls(s, now)
	if c.DecisionsHour >= b.CallsPerHour {
		return fmt.Errorf("%w: %d calls in the last hour, the cap is %d", ErrCallCap, c.DecisionsHour, b.CallsPerHour)
	}
	if c.DecisionsDay >= b.CallsPerDay {
		return fmt.Errorf("%w: %d calls today, the cap is %d", ErrCallCap, c.DecisionsDay, b.CallsPerDay)
	}
	return nil
}

// callRecordPrefix names the record one call leaves in the state. It is a name
// of its own rather than one a question would carry, so a reader — and a test —
// can tell the loop's own bookkeeping from a question somebody asked.
const callRecordPrefix = "decide-call-"

// recordCall is s with the record of one call in it: the decision the loop asks
// itself and closes in the same patch, which is what makes Allow count calls
// rather than questions.
//
// It is a decision and not a counter of its own because the document has one
// place for the loop's history. It is closed as it is written, by the class it
// waits on — the daemon's, since the daemon is what keeps the loop's books —
// because only a resolved decision ages out: an open one is a question somebody
// still owes an answer to and is never evicted (orchestrator.Retain), so a
// record per call would fill the document and stop the daemon through OverCap.
// Resolved, it is un-evictable only while the caps still count it
// (countedDecision), and it leaves the document with the window.
//
// What it costs is bounded by the caps themselves: at most CallsPerDay records
// can be written in a day, and each is a name, one line and two stamps.
func recordCall(s orchestrator.State, itemID string, at time.Time) (orchestrator.State, error) {
	id := freeCallID(s)
	p := orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{
		{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
			ID:        id,
			Question:  "the decision agent called about " + safetext.OneLine(itemID),
			WaitingOn: orchestrator.OnDaemon,
		}},
		{Op: orchestrator.OpDecisionResolve, Resolve: &orchestrator.ResolveOp{
			ID:     id,
			Answer: "counted",
		}},
	}}
	return orchestrator.Apply(s, p, orchestrator.Actor{Class: orchestrator.Daemon, Name: ActorName, Now: at})
}

// freeCallID is the first name of the record's own family this state does not
// carry. The stamp is deliberately not part of it: two calls can land inside
// one second — a test's clock does, and a machine's can — and Apply refuses a
// decision whose id is already there, which would turn a second call into a
// failure instead of a record.
func freeCallID(s orchestrator.State) string {
	for n := 1; ; n++ {
		id := callRecordPrefix + strconv.Itoa(n)
		if _, taken := s.Decision(id); !taken {
			return id
		}
	}
}
