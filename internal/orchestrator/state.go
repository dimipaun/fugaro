// Package orchestrator is the stateless orchestrator's state: one JSON
// document per orchestrator id, under `orchestrator/<id>/state` in the
// project's Realtime Database, plus the typed patch that changes it and the
// rules that decide whether that patch may.
//
// Nothing here reads a clock, a network or a model of its own: a state is a
// value, Apply is a pure function of it, of a patch and of the actor that
// sent the patch, and every stamp is the server time the caller read
// (rtdb.ServerNow) rather than the caller's own. That is what lets the whole
// money path — who may spend an approval, how many attempts one approval
// covers, which phase may follow which — be tested without a cloud, and it is
// what makes the node the only place the daemon's decisions live: a daemon
// that restarts re-reads it and carries on.
//
// The state is written by the daemon alone (O2's rules close the subtree to
// every token writer; a person's own credential is what writes it, D1241) but
// it is read by people, sessions and the decision agent, so every string in
// it is untrusted by the time anything prints one: free text is cleaned and
// capped on the way in (internal/safetext), and every surface that prints it
// fences it as data — Summarize here, and O5's, O10's and O12's briefs.
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/tier"
)

// SchemaVersion is the state schema this release writes. A node carrying an
// older one is raised by Migrate before it is used; one carrying a newer one
// is refused, because this release cannot know what a later one meant by it.
const SchemaVersion = 1

// ApprovalAttempts is what one approval covers: the attempt the person
// approved and at most one automatic retry of that same spec (the owner's
// ruling of 2026-11-04). A second failure needs a new approval, which is why
// the allowance is spent by the retry's own intent and not by the first.
const ApprovalAttempts = 2

// RetentionWindow is the longest window any spawn cap counts over — the
// per-day cap. An intent younger than it is never evicted, because the caps
// are derived from the committed intents (O6) and an eviction would raise the
// allowance a cycle has left.
const RetentionWindow = 24 * time.Hour

// RoleOrchestrator and RoleCoordinator are the two loops that keep a state
// node: the orchestrator drives work items to merged pull requests, the
// coordinator does the same for a project-wide goal. They share this shape
// and differ in their recipe and their caps, so the role is a name in the
// document rather than a second schema.
const (
	RoleOrchestrator = "orchestrator"
	RoleCoordinator  = "coordinator"
)

// The node's bounds. They are the client-side half of D1241: the rules refuse
// a token writer outright, so a document that exists was written by the
// person's own credential — which IAM lets past the rules — and this is where
// its shape and its size are checked. The size target is about four thousand
// tokens of JSON; Retain is what brings a grown state back inside it.
const (
	MaxStateBytes = 16 << 10

	MaxWorkItems = 100
	MaxDecisions = 100
	MaxFacts     = 64
	MaxIntents   = 64
	MaxArchived  = 64
	MaxDependsOn = 32

	MaxGoalRunes      = 600
	MaxTitleRunes     = 200
	MaxBriefRunes     = 2000
	MaxNoteRunes      = 600
	MaxFactKeyRunes   = 64
	MaxFactValueRunes = 300
	MaxQuestionRunes  = 600
	MaxAnswerRunes    = 600
	MaxOutcomeRunes   = 80
	MaxActorNameRunes = 100
)

// Phase is where a work item stands. It is the item's own vocabulary and not
// the run record's `status` or `stage`: the run that carries an item reports
// its own state in its own record, and the daemon folds that record into this
// one, so a name shared between the two would be read as the wrong one
// (TestWorkItemFieldsAreNotTheRunRecordsFields).
type Phase string

// The phases, in the order the spec's table moves through them.
const (
	PhaseQueued    Phase = "queued"
	PhaseRunning   Phase = "running"
	PhaseReview    Phase = "review"
	PhaseDone      Phase = "done"
	PhaseFailed    Phase = "failed"
	PhaseBlocked   Phase = "blocked"
	PhaseCancelled Phase = "cancelled"
)

// Phases is the whole vocabulary, and it is closed: a document carrying
// anything else is refused rather than read as an unknown phase.
var Phases = []Phase{PhaseQueued, PhaseRunning, PhaseReview, PhaseDone, PhaseFailed, PhaseBlocked, PhaseCancelled}

// Known reports whether p is one of the seven phases.
func (p Phase) Known() bool {
	for _, q := range Phases {
		if p == q {
			return true
		}
	}
	return false
}

// Done reports whether p is a phase a work item never leaves.
func (p Phase) Done() bool { return p == PhaseDone || p == PhaseCancelled }

// active reports whether p is a phase a run may still be moving through.
func (p Phase) active() bool {
	return p == PhaseQueued || p == PhaseRunning || p == PhaseReview
}

// CanMoveTo reports whether an item may move from p to next, by the spec's
// table: queued -> running -> review -> done, any active phase -> failed,
// blocked or cancelled, and failed or blocked -> queued, which is the path a
// person's new approval re-opens. A phase may always move to itself, so a
// tick that rewrites what it already recorded changes nothing.
func (p Phase) CanMoveTo(next Phase) bool {
	if p == next {
		return true
	}
	switch p {
	case PhaseQueued:
		return next == PhaseRunning || next == PhaseFailed || next == PhaseBlocked || next == PhaseCancelled
	case PhaseRunning:
		return next == PhaseReview || next == PhaseDone || next == PhaseFailed || next == PhaseBlocked || next == PhaseCancelled
	case PhaseReview:
		return next == PhaseDone || next == PhaseFailed || next == PhaseBlocked || next == PhaseCancelled
	case PhaseFailed, PhaseBlocked:
		return next == PhaseQueued || next == PhaseCancelled
	}
	return false
}

// Class is who is acting on the state: the person at a terminal, the session
// that carries an item's work, the daemon's own tick, or the decision agent.
// It is a field's whole provenance as well as an actor's identity, so Fact.By
// and Approval.By read the same vocabulary the patch does.
type Class string

// The four classes. Only a person may close a decision that waits on a
// person, approve a work item or set the goal (G2); the other three are the
// ones that act with nobody watching.
const (
	Person        Class = "person"
	Session       Class = "session"
	Daemon        Class = "daemon"
	DecisionAgent Class = "decision-agent"
)

// Classes is the whole vocabulary, person first.
var Classes = []Class{Person, Session, Daemon, DecisionAgent}

// Known reports whether c is one of the four classes.
func (c Class) Known() bool {
	for _, k := range Classes {
		if c == k {
			return true
		}
	}
	return false
}

// On is what a decision waits on: who has to answer it. Its values are the
// classes that can answer — a person, a session or the daemon's own tick — so
// the answer's actor is checked against the decision rather than trusted.
type On string

const (
	OnPerson  On = "person"
	OnSession On = "session"
	OnDaemon  On = "daemon"
)

// Ons is the whole vocabulary.
var Ons = []On{OnPerson, OnSession, OnDaemon}

// Known reports whether o is one of the three.
func (o On) Known() bool {
	for _, k := range Ons {
		if o == k {
			return true
		}
	}
	return false
}

// Class is the acting class the same-named decision waits on.
func (o On) Class() Class { return Class(o) }

// Digest identifies the exact launch spec a person approved. It is unkeyed
// and covers the declarative fields alone (SpecDigest), so a session that can
// read an item can compute one — which is why an approval is never taken from
// a patch's word for it: the approve op carries the digest the person was
// shown and it must equal the item's current one.
type Digest string

// Outcome is how an attempt ended, in the orchestrator's own words. It is not
// the run record's status: O6 and O7 fold that record into one of these, and
// an attempt that has not ended yet carries none.
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	// OutcomeRefused is a run that started and then reported a refusal — a
	// spend cap, a gate. It is an attempt like any other, so the item is not
	// relaunched on the next tick. A refusal that never started a run leaves
	// no outcome at all and burns no attempt.
	OutcomeRefused Outcome = "refused"
	// OutcomeCancelled is a run a person stopped.
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeAbandoned is an intent recovery refused to re-issue: nothing was
	// ever started under its run id, and nothing will be.
	OutcomeAbandoned Outcome = "abandoned"
)

// Outcomes is the whole vocabulary, the empty outcome (declared, nothing
// reported yet) standing beside it rather than in it.
var Outcomes = []Outcome{OutcomeSucceeded, OutcomeFailed, OutcomeRefused, OutcomeCancelled, OutcomeAbandoned}

// Known reports whether o is one of the recorded outcomes.
func (o Outcome) Known() bool {
	for _, k := range Outcomes {
		if o == k {
			return true
		}
	}
	return false
}

// Approval is a person's go for one work item: the digest of the spec they
// were shown, who they are, when they said it, and how many attempts it
// covers. Nothing but the approve op writes one, for any actor (an upsert
// carrying an approval is refused, the digest being computable by anyone who
// can read the item).
type Approval struct {
	By         string    `json:"by"`
	At         time.Time `json:"at"`
	SpecDigest Digest    `json:"spec_digest"`
	Attempts   int       `json:"attempts"`
}

// WorkItem is one piece of work the orchestrator is carrying: what it is,
// where it goes, and the bookkeeping the daemon keeps about the attempts at
// it.
type WorkItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Repo      string    `json:"repo"`
	Recipe    string    `json:"recipe"`
	RigorTier string    `json:"rigor_tier"`
	Brief     string    `json:"brief"`
	DependsOn []string  `json:"depends_on"`
	Phase     Phase     `json:"phase"`
	RunID     string    `json:"run_id,omitempty"`
	Approved  *Approval `json:"approved,omitempty"`
	// SpentApprovalAt is the At of the approval a retry's intent spent. An
	// approval is cleared when it is spent, so this is the only thing left
	// that says the item already had its retry: recovery reads it to tell a
	// crashed retry from an attempt that was never approved.
	SpentApprovalAt *time.Time `json:"spent_approval_at,omitempty"`
	// Attempts is the number of the last attempt committed for this item,
	// counting every approval it has had. It is a mirror the daemon writes
	// with the intent; what a planner decides on is read off the intents
	// (AttemptsUnder), which the caps count from too.
	Attempts int    `json:"attempts"`
	Note     string `json:"note,omitempty"`
}

// Intent is a committed attempt: the launch is recorded before it is started,
// under a run id minted before the start, so money is spent at most once even
// if the daemon dies between the two. The id is the item and the attempt
// joined (IntentID), which is what makes "the item has no intent for the
// attempt being planned" the same test as "the attempt count is below what
// the approval covers".
type Intent struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	SpecDigest Digest    `json:"spec_digest"`
	ApprovalAt time.Time `json:"approval_at"`
	At         time.Time `json:"at"`
	Outcome    Outcome   `json:"outcome,omitempty"`
}

// Archived is a work item the state no longer carries: its id, how it ended
// and when it was archived out. The item's own history is the pull request
// and the run record, not this node.
type Archived struct {
	ID      string    `json:"id"`
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}

// Decision is a question the loop could not answer itself. It waits on the
// class that has to answer it, and only that class may close it (a person may
// close any of them: everyone else's answers are a person's to overrule).
type Decision struct {
	ID         string     `json:"id"`
	Question   string     `json:"question"`
	WaitingOn  On         `json:"waiting_on"`
	At         time.Time  `json:"at"`
	Answer     string     `json:"answer,omitempty"`
	By         Class      `json:"by,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Resolved reports whether the decision has been answered.
func (d Decision) Resolved() bool { return d.ResolvedAt != nil }

// Fact is one thing the loop has learned, with the class that learned it:
// provenance is what lets a brief show a fact a session wrote beside one the
// daemon derived.
type Fact struct {
	Value string `json:"value"`
	By    Class  `json:"by"`
}

// Counters is how many decision-agent calls the state records inside the
// current hour and the current day, which is the bound O12 holds its calls
// to. It is derived from the decisions and the server time (DecisionCalls),
// and Apply writes the reading into the document on every change, so a daemon
// that restarts reads the same numbers instead of a reset counter.
type Counters struct {
	DecisionsHour int `json:"decisions_hour"`
	DecisionsDay  int `json:"decisions_day"`
}

// State is the whole document.
type State struct {
	SchemaVersion int             `json:"schema_version"`
	Rev           int64           `json:"rev"`
	AgentID       string          `json:"agent_id"`
	Role          string          `json:"role"`
	Goal          string          `json:"goal"`
	WorkItems     []WorkItem      `json:"work_items"`
	Decisions     []Decision      `json:"decisions"`
	Facts         map[string]Fact `json:"facts"`
	Archived      []Archived      `json:"archived"`
	Intents       []Intent        `json:"intents"`
	Counters      Counters        `json:"counters"`
}

// Clone is a deep copy: Apply must not hand back a state whose slices or maps
// alias the one it was given, or a caller that kept the old state would see
// its own copy change under it.
func (s State) Clone() State {
	next := s
	next.WorkItems = make([]WorkItem, len(s.WorkItems))
	for i, w := range s.WorkItems {
		next.WorkItems[i] = w.clone()
	}
	next.Decisions = make([]Decision, len(s.Decisions))
	for i, d := range s.Decisions {
		if d.ResolvedAt != nil {
			at := *d.ResolvedAt
			d.ResolvedAt = &at
		}
		next.Decisions[i] = d
	}
	next.Facts = make(map[string]Fact, len(s.Facts))
	for k, f := range s.Facts {
		next.Facts[k] = f
	}
	next.Archived = copyOf(s.Archived)
	next.Intents = copyOf(s.Intents)
	return next
}

// copyOf is a slice's own copy, and never nil: a nil slice marshals as null,
// and every collection this document carries is an array in its schema, so
// an empty one — the state a caller has just built — is written as the empty
// list rather than as something the schema refuses.
func copyOf[T any](v []T) []T {
	out := make([]T, len(v))
	copy(out, v)
	return out
}

func (w WorkItem) clone() WorkItem {
	if w.Approved != nil {
		a := *w.Approved
		w.Approved = &a
	}
	if w.SpentApprovalAt != nil {
		at := *w.SpentApprovalAt
		w.SpentApprovalAt = &at
	}
	w.DependsOn = copyOf(w.DependsOn)
	return w
}

// Item returns the item with the given id.
func (s State) Item(id string) (WorkItem, bool) {
	for _, w := range s.WorkItems {
		if w.ID == id {
			return w, true
		}
	}
	return WorkItem{}, false
}

// Decision returns the decision with the given id.
func (s State) Decision(id string) (Decision, bool) {
	for _, d := range s.Decisions {
		if d.ID == id {
			return d, true
		}
	}
	return Decision{}, false
}

// IntentID is the id of the intent for one attempt at one item. It is the
// item, a colon, and the attempt number, so an attempt is named once and
// planning the same attempt twice is the same intent.
func IntentID(itemID string, attempt int) string {
	return fmt.Sprintf("%s:%d", itemID, attempt)
}

// IntentItem is the item an intent's id names.
func IntentItem(id string) string {
	item, _, _ := strings.Cut(id, ":")
	return item
}

// IntentAttempt is the attempt an intent's id names, or 0 when the id carries
// no attempt at all. The digits are read one by one rather than scanned: an id
// like "wi-1:1x" is not attempt 1, because two ids that mean one attempt would
// have the count an approval is spent against count it twice (the patch schema
// spells the same rule as a pattern).
func IntentAttempt(id string) int {
	_, rest, ok := strings.Cut(id, ":")
	if !ok || rest == "" || len(rest) > 9 {
		return 0
	}
	n := 0
	for i := range len(rest) {
		if c := rest[i]; c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(rest[i]-'0')
	}
	return n
}

// attemptsCommitted is the number of the last attempt committed for an item,
// or 0 when none has been. Attempt numbering runs on across approvals — a
// re-approved item starts a new allowance, not a new numbering — which is what
// keeps an intent's id, the item and the attempt, unique for the item's whole
// life.
func attemptsCommitted(s State, itemID string) int {
	n := 0
	for _, it := range s.Intents {
		if IntentItem(it.ID) == itemID {
			n = max(n, IntentAttempt(it.ID))
		}
	}
	return n
}

// AttemptsUnder is how many attempts have been committed against an item
// under the approval made at approvalAt. It is the count G4 reads: the first
// attempt and its retry carry the approval's own At, so a later approval
// cannot inherit an earlier one's attempts, and the plan compares it against
// Approval.Attempts rather than against a counter.
func AttemptsUnder(s State, itemID string, approvalAt time.Time) int {
	n := 0
	for _, it := range s.Intents {
		if IntentItem(it.ID) == itemID && it.ApprovalAt.Equal(approvalAt) {
			n++
		}
	}
	return n
}

// DecisionCalls derives the decision-agent call counts from the decisions the
// state carries and the server time, over the hour and the day now falls in.
// Every recorded decision counts, whoever recorded it: the node cannot tell a
// call the agent made from a question a person added by hand, and counting
// both is the safe direction — it stops calls, never starts one.
func DecisionCalls(s State, now time.Time) Counters {
	now = now.UTC()
	hour, day := now.Truncate(time.Hour), now.Truncate(24*time.Hour)
	var c Counters
	for _, d := range s.Decisions {
		at := d.At.UTC()
		if at.Before(day) {
			continue
		}
		c.DecisionsDay++
		if !at.Before(hour) {
			c.DecisionsHour++
		}
	}
	return c
}

// bookkeepingFields are the WorkItem fields SpecDigest leaves out: what the
// daemon records about the attempts at an item rather than what the person
// approves. It is a skip list and not an include list on purpose, so a field
// added to WorkItem later is digested without anyone remembering to add it
// here (TestSpecDigestCoversEveryFieldButTheBookkeepingList pins the six).
var bookkeepingFields = []string{"phase", "run_id", "spent_approval_at", "attempts", "note", "approved"}

// SpecDigest is the digest of everything a person approves when they approve
// a work item: every field but the bookkeeping list, hashed in declaration
// order as `name NUL json NUL`. The names are the wire names, so a field
// renamed on the wire — which is a change of meaning, not a spelling — is a
// different digest. The approve op and the launcher both use this one
// function, so what the person was shown and what is about to start cannot
// drift apart.
func SpecDigest(w WorkItem) Digest {
	// Over the item as it is written, so that the empty case has one spelling:
	// an item built in Go carries a nil slice where the document carries [].
	// A digest that read those as two specs would revoke an approval by
	// putting the item through the wire and back.
	w = w.clone()
	t := reflect.TypeOf(w)
	v := reflect.ValueOf(w)
	h := sha256.New()
	for i := range t.NumField() {
		f := t.Field(i)
		name := f.Name
		if tag := f.Tag.Get("json"); tag != "" {
			name = strings.Split(tag, ",")[0]
		}
		if name == "" || name == "-" || isBookkeeping(name) {
			continue
		}
		raw, err := json.Marshal(v.Field(i).Interface())
		if err != nil {
			// A WorkItem is marshalled on every save, so a field the encoder
			// cannot take is a mistake in this package, not in the input.
			panic(fmt.Sprintf("orchestrator: SpecDigest cannot marshal %s: %v", f.Name, err))
		}
		fmt.Fprintf(h, "%s\x00%s\x00", name, raw)
	}
	return Digest("sha256:" + hex.EncodeToString(h.Sum(nil)))
}

func isBookkeeping(name string) bool {
	for _, b := range bookkeepingFields {
		if b == name {
			return true
		}
	}
	return false
}

// DigestKnown reports whether a digest is shaped like one this package
// writes, so a document is refused for a malformed one rather than read as an
// approval that can never match.
func DigestKnown(d Digest) bool { return digestRE.MatchString(string(d)) }

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Migrate raises a state written by an older release to this one and reports
// whether it changed anything. It runs where a node is read — the store's
// Load and the daemon's start-up — so an installation that upgrades once
// (the HARD RULE: `fugaro update` is the whole upgrade) keeps working without
// an operator step. Every step keeps the fields it does not understand: v0
// was the node before the schema carried a version, so a v0 document that
// already carries a later field is stamped, not rewritten, and nothing here
// clears a field.
//
// A state newer than this release is left exactly as it is and reported as
// unchanged: the caller's validator refuses it, which is the fail-closed
// answer for a document a later release wrote.
func Migrate(s State) (State, bool) {
	if s.SchemaVersion != 0 {
		// This release's version, a later one, or a number that is not a
		// version at all: either there is nothing to raise or the number is
		// one this release must refuse rather than guess at, and the validator
		// is what refuses it.
		return s, false
	}
	s.SchemaVersion = SchemaVersion
	return s, true
}

// OverCap reports whether the node is over its size target. The daemon fails
// closed on a true answer (O6 plans no spawn) rather than writing a document
// the RTDB rules refuse or one a reader cannot afford.
func OverCap(s State) bool { return stateBytes(s) > MaxStateBytes }

// The retention rule's own bounds: what is kept of the parts of the node that
// are history rather than work. They sit below the caps ValidateState refuses
// at, because retention's job is to keep the node small, not to hold as much
// as the document is allowed to carry.
const (
	keptTerminal = 8
	keptArchived = 16
	keptResolved = 8
	keptFacts    = 32
)

// Retain returns s with its history aged out, so the node stays inside the
// size target: finished items, resolved decisions and old intents are
// archived out past the bounds above, and a brief, whose only job was to
// describe a launch, goes with the finished item it launched.
//
// Three things are never evicted, for one reason: the spawn caps are derived
// from the committed intents (O6), so an eviction would raise the allowance a
// cycle has left.
//
//   - an intent younger than RetentionWindow, the longest window a cap counts
//     over, however much the node needs the room;
//   - any intent of an item whose Approved still stands: a standing approval
//     is one the plan may still spend, so an item paused by a cap or by the
//     kill switch for longer than the window must not come back with its
//     count reset and spend the same retry again;
//   - any intent the item's SpentApprovalAt names, which is the retry that
//     was already charged, for the same reason.
//
// A resolved decision younger than the same window is never evicted either,
// and for the same reason: the decision-agent call counts are derived from
// the decisions the document carries, so dropping one inside the window a cap
// counts over would lower the count — the cap moving the other way, which is
// the one direction it must never move.
//
// An item that has left the work list protects nothing: nothing relaunches
// under its approval, so its intents age out with the window like any other.
//
// A session that inflates facts, items or decisions therefore cannot push an
// in-window intent out; it can only spend the room the node has. If the node
// is still over the target once everything droppable is gone, Retain leaves
// it over the target and OverCap says so — the daemon stops spawning, fail
// closed, rather than the count moving.
func Retain(s State, now time.Time) State {
	s.WorkItems, s.Archived = retainItems(s, now)
	s.Decisions = retainDecisions(s.Decisions, now)
	s.Facts = retainFacts(s.Facts)
	s.Intents = retainIntents(s, now)
	for OverCap(s) && shrink(&s, now) {
	}
	return s
}

// retainItems archives the finished items past keptTerminal and drops the
// oldest archived entries past keptArchived. An item another item still in the
// list depends on is never archived, and neither is one an approval still
// stands on: the first would leave the dependent naming an item the document
// no longer carries, the second would take the approval away with it.
func retainItems(s State, now time.Time) ([]WorkItem, []Archived) {
	work, archived := s.WorkItems, s.Archived
	finished := 0
	for _, w := range work {
		if w.Phase.Done() {
			finished++
		}
	}
	// One pass is not always enough: an item another finished item depends on
	// becomes archivable only once that dependent has gone, so a chain comes
	// apart from its end inwards one pass at a time. The loop stops as soon as
	// a pass drops nothing, which is a set of finished items that depend on
	// one another — those stay, and shrink and OverCap take it from there.
	for finished > keptTerminal {
		dropped := false
		kept := make([]WorkItem, 0, len(work))
		for _, w := range work {
			if finished > keptTerminal && archivable(work, w) {
				archived = append(archived, Archived{ID: w.ID, Outcome: string(w.Phase), At: now})
				finished--
				dropped = true
				continue
			}
			kept = append(kept, w)
		}
		work = kept
		if !dropped {
			break
		}
	}
	if len(archived) > keptArchived {
		archived = archived[len(archived)-keptArchived:]
	}
	return work, archived
}

// retainDecisions drops the oldest resolved decisions past keptResolved, but
// never more than the ones the call counts are not derived from. An open
// question is never dropped either: it is something a person still owes an
// answer to.
func retainDecisions(decisions []Decision, now time.Time) []Decision {
	drop := 0
	for _, d := range decisions {
		if d.Resolved() && !countedDecision(d, now) {
			drop++
		}
	}
	drop -= keptResolved
	if drop <= 0 {
		return decisions
	}
	kept := make([]Decision, 0, len(decisions))
	for _, d := range decisions {
		if drop > 0 && d.Resolved() && !countedDecision(d, now) {
			drop--
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// countedDecision reports whether DecisionCalls counts a decision at now,
// which is what makes it un-evictable: the count is derived from the
// decisions the document carries, so losing one inside the window would lower
// it. The window is RetentionWindow, the longest one a cap counts over, which
// covers the hour and the day both with room to spare.
func countedDecision(d Decision, now time.Time) bool {
	return d.Resolved() && now.Sub(d.At) < RetentionWindow
}

// retainFacts keeps the facts whose keys sort first past keptFacts. A fact has
// no age to sort by — the document keeps none, a fact being durable by
// definition — so the order is the key order, which is at least the same
// answer everywhere and at every run.
func retainFacts(facts map[string]Fact) map[string]Fact {
	if len(facts) <= keptFacts {
		return facts
	}
	keys := sortedKeys(facts)
	kept := make(map[string]Fact, keptFacts)
	for _, k := range keys[:keptFacts] {
		kept[k] = facts[k]
	}
	return kept
}

// retainIntents drops the oldest intents that may be dropped — the ones the
// three rules on Retain leave free — down to MaxIntents.
func retainIntents(s State, now time.Time) []Intent {
	if len(s.Intents) <= MaxIntents {
		return s.Intents
	}
	drop := len(s.Intents) - MaxIntents
	kept := make([]Intent, 0, len(s.Intents))
	for _, it := range s.Intents {
		if drop > 0 && !protectedIntent(s, it, now) {
			drop--
			continue
		}
		kept = append(kept, it)
	}
	return kept
}

// protectedIntent reports whether an intent may not be evicted, by the three
// rules Retain lists.
func protectedIntent(s State, it Intent, now time.Time) bool {
	if now.Sub(it.At) < RetentionWindow {
		return true
	}
	w, ok := s.Item(IntentItem(it.ID))
	if !ok {
		return false
	}
	if w.Approved != nil && w.Approved.At.Equal(it.ApprovalAt) {
		return true
	}
	return w.SpentApprovalAt != nil && w.SpentApprovalAt.Equal(it.ApprovalAt)
}

// shrink drops one more piece of history from a node that is still over the
// target after the passes above, and reports whether it dropped anything. The
// order is what each loss costs: entries that only record something already
// finished first, and the facts last, because a fact is the only thing here
// the loop still works from — a fact it needs again it can write again, but
// nothing will write it back on its own.
//
// It stops at what the node is for: an open decision, a resolved one inside
// the window a call count is derived over, a live item, a protected intent and
// the goal are never dropped, so a node over the target with nothing but those
// left stays over it and OverCap says so. That is the fail closed direction:
// the daemon stops spawning rather than a count moving.
//
// A finished item goes whole rather than losing its brief: an item with an
// empty brief is not a document this release reads back, and the entry it
// would leave in the archived list is room the node is short of.
func shrink(s *State, now time.Time) bool {
	if len(s.Archived) > 0 {
		s.Archived = s.Archived[1:]
		return true
	}
	for i, d := range s.Decisions {
		if d.Resolved() && !countedDecision(d, now) {
			s.Decisions = append(s.Decisions[:i], s.Decisions[i+1:]...)
			return true
		}
	}
	for i, w := range s.WorkItems {
		if archivable(s.WorkItems, w) {
			s.WorkItems = append(s.WorkItems[:i], s.WorkItems[i+1:]...)
			return true
		}
	}
	for i, it := range s.Intents {
		if !protectedIntent(*s, it, now) {
			s.Intents = append(s.Intents[:i], s.Intents[i+1:]...)
			return true
		}
	}
	if len(s.Facts) > 0 {
		keys := sortedKeys(s.Facts)
		delete(s.Facts, keys[len(keys)-1])
		return true
	}
	return false
}

// archivable reports whether an item may leave the list it is in: it is
// finished, no approval still stands on it, and nothing left in the list names
// it in DependsOn. A dependent that is still live would be stranded by the
// loss — the loop cannot start an item whose dependency is not there — and a
// dependent that is finished would be left holding a reference to an item the
// document no longer carries, which is a document this release does not read
// back.
func archivable(work []WorkItem, w WorkItem) bool {
	if !w.Phase.Done() || w.Approved != nil {
		return false
	}
	for _, o := range work {
		if o.ID == w.ID {
			continue
		}
		if slices.Contains(o.DependsOn, w.ID) {
			return false
		}
	}
	return true
}

// TierKnown reports whether a work item's rigor tier is one of the four
// internal/tier spells. The tier is an expectation of cost and time and never
// a cap, and the item carries the one the person approved, so the launch that
// comes out of the item is the one they read.
func TierKnown(name string) bool { return tier.Known(tier.Tier(name)) }
