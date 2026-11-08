package orchestrator

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// The refusals a patch meets. They are values so a caller can tell the cases
// apart: a stale revision is a re-read and a retry, a refusal by class is not,
// and a caller that treats the two the same either loops or gives up early.
var (
	ErrStaleRev        = errors.New("orchestrator: the patch was built from another revision")
	ErrUnknownOp       = errors.New("orchestrator: unknown op")
	ErrOpPayload       = errors.New("orchestrator: the op does not carry the payload its kind takes")
	ErrActorNotAllowed = errors.New("orchestrator: this class of actor may not do that")
	ErrNotFound        = errors.New("orchestrator: no such work item, decision or fact")
	ErrDigestMismatch  = errors.New("orchestrator: the digest is not the item's current spec digest")
	ErrSchemaVersion   = errors.New("orchestrator: the state carries a schema version this release does not write")
	ErrTooLarge        = errors.New("orchestrator: the state is over its size target")
	ErrInvalid         = errors.New("orchestrator: the state or the patch is not shaped as this release reads it")
	ErrIntentDeclared  = errors.New("orchestrator: the intent is already declared")
)

// MaxOps bounds one patch. A patch is a caller's list of changes, so its
// length is the caller's to choose, and this is where it stops being: the
// schema publishes the same number, and a patch of a hundred thousand ops is
// not a change anybody read.
const MaxOps = 64

// Patch is one typed change to a state. It carries the revision it was built
// from, so a patch built against a state somebody else has since changed is
// refused rather than landing on top of it, and the ops in order.
type Patch struct {
	BaseRev int64 `json:"base_rev"`
	Ops     []Op  `json:"ops"`
}

// OpKind is an op's name. The vocabulary is closed and small on purpose: an
// agent never writes state, it can only ask for one of these, and code
// decides whether it may (G1).
type OpKind string

const (
	OpWorkItemUpsert  OpKind = "work_item_upsert"
	OpWorkItemApprove OpKind = "work_item_approve"
	OpWorkItemArchive OpKind = "work_item_archive"
	OpDecisionAdd     OpKind = "decision_add"
	OpDecisionResolve OpKind = "decision_resolve"
	OpFactSet         OpKind = "fact_set"
	OpFactDelete      OpKind = "fact_delete"
	OpGoalSet         OpKind = "goal_set"
)

// OpKinds is the whole vocabulary.
var OpKinds = []OpKind{
	OpWorkItemUpsert, OpWorkItemApprove, OpWorkItemArchive,
	OpDecisionAdd, OpDecisionResolve, OpFactSet, OpFactDelete, OpGoalSet,
}

// Known reports whether k is one of the eight ops.
func (k OpKind) Known() bool {
	for _, o := range OpKinds {
		if o == k {
			return true
		}
	}
	return false
}

// Op is one op: its kind and the payload that kind takes. Every field is a
// pointer so that "absent" and "set to the zero value" are different things:
// an upsert changes what it carries and leaves the rest alone, which is how
// the daemon moves a phase without rewriting — and re-digesting — a brief.
type Op struct {
	Op       OpKind      `json:"op"`
	WorkItem *WorkItemOp `json:"work_item,omitempty"`
	Approve  *ApproveOp  `json:"approve,omitempty"`
	Archive  *ArchiveOp  `json:"archive,omitempty"`
	Decision *DecisionOp `json:"decision,omitempty"`
	Resolve  *ResolveOp  `json:"resolve,omitempty"`
	Fact     *FactOp     `json:"fact,omitempty"`
	Goal     *GoalOp     `json:"goal,omitempty"`
}

// WorkItemOp is what a work_item_upsert carries: the declaration a person or
// a session writes, and the bookkeeping only the daemon may write. There is
// no approval field: an approval comes from work_item_approve and from
// nothing else, because the digest it binds is computable by anyone who can
// read the item.
type WorkItemOp struct {
	ID        string    `json:"id"`
	Title     *string   `json:"title,omitempty"`
	Repo      *string   `json:"repo,omitempty"`
	Recipe    *string   `json:"recipe,omitempty"`
	RigorTier *string   `json:"rigor_tier,omitempty"`
	Brief     *string   `json:"brief,omitempty"`
	DependsOn *[]string `json:"depends_on,omitempty"`
	Note      *string   `json:"note,omitempty"`

	Phase           *Phase     `json:"phase,omitempty"`
	RunID           *string    `json:"run_id,omitempty"`
	SpentApprovalAt *time.Time `json:"spent_approval_at,omitempty"`
	Attempts        *int       `json:"attempts,omitempty"`
	Intent          *IntentOp  `json:"intent,omitempty"`
}

// IntentOp declares the attempt a launch is for, or records how one ended.
// The declaration carries the run id minted before the start, the digest of
// the spec being launched and the At of the approval it spends; a later
// upsert carrying only Outcome records what the run reported.
type IntentOp struct {
	ID         string     `json:"id"`
	RunID      *string    `json:"run_id,omitempty"`
	SpecDigest *Digest    `json:"spec_digest,omitempty"`
	ApprovalAt *time.Time `json:"approval_at,omitempty"`
	Outcome    *Outcome   `json:"outcome,omitempty"`
}

// ApproveOp is a person's go for one work item. It carries the digest the
// person was shown, and Apply refuses it unless that is still the item's
// digest: the approval covers the spec that was read, not whatever the item
// has become.
type ApproveOp struct {
	ID         string `json:"id"`
	SpecDigest Digest `json:"spec_digest"`
}

// ArchiveOp moves a work item out of the work list and into the archived
// list, with the outcome it ended with.
type ArchiveOp struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

// DecisionOp raises a question and names the class that has to answer it.
type DecisionOp struct {
	ID        string `json:"id"`
	Question  string `json:"question"`
	WaitingOn On     `json:"waiting_on"`
}

// ResolveOp answers a question. Who answered is the actor, not a field: the
// class in the patch is checked against the class the decision waits on.
type ResolveOp struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

// FactOp sets or deletes one fact. A set needs a value, a delete must not
// carry one, so a patch that means one and carries the other is refused
// instead of guessed at.
type FactOp struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// GoalOp is the orchestrator's goal, which only a person sets: it is the
// standing instruction every loop of the daemon is measured against.
type GoalOp struct {
	Goal string `json:"goal"`
}

// Actor is who sent a patch: the class that decides what the patch may do,
// a name for the record, and the server time the caller read before building
// the patch. Stamps come from here and never from an op's own fields, so a
// caller cannot backdate an approval or an intent.
type Actor struct {
	Class Class
	Name  string
	Now   time.Time
}

// Apply returns the state a patch makes of another one, or the refusal that
// stops it. It is a pure function — the same state, patch and actor always
// give the same answer — and it is the whole fence: the class of the actor
// decides which ops may land, what an op carries decides what it changes, and
// the result is validated before it is handed back, so a caller cannot write
// a state this release would not read.
func Apply(s State, p Patch, a Actor) (State, error) {
	if !a.Class.Known() {
		return State{}, fmt.Errorf("%w: %q is not a class of actor", ErrActorNotAllowed, safetext.OneLine(string(a.Class)))
	}
	if a.Now.IsZero() {
		return State{}, fmt.Errorf("%w: the actor carries no server time, and every stamp in the document comes from one", ErrActorNotAllowed)
	}
	if err := ValidateState(s); err != nil {
		return State{}, err
	}
	if p.BaseRev != s.Rev {
		return State{}, fmt.Errorf("%w: the patch is built from revision %d and the state is at %d", ErrStaleRev, p.BaseRev, s.Rev)
	}
	if err := ValidatePatch(p); err != nil {
		return State{}, err
	}
	next := s.Clone()
	for i, op := range p.Ops {
		if err := applyOp(&next, op, a); err != nil {
			return State{}, fmt.Errorf("op %d: %w", i+1, err)
		}
	}
	next.Rev = s.Rev + 1
	next.SchemaVersion = SchemaVersion
	next.Counters = DecisionCalls(next, a.Now)
	if err := ValidateState(next); err != nil {
		return State{}, err
	}
	return next, nil
}

func applyOp(s *State, op Op, a Actor) error {
	switch op.Op {
	case OpWorkItemUpsert:
		return applyUpsert(s, *op.WorkItem, a)
	case OpWorkItemApprove:
		return applyApprove(s, *op.Approve, a)
	case OpWorkItemArchive:
		return applyArchive(s, *op.Archive, a)
	case OpDecisionAdd:
		return applyDecisionAdd(s, *op.Decision, a)
	case OpDecisionResolve:
		return applyDecisionResolve(s, *op.Resolve, a)
	case OpFactSet:
		return applyFactSet(s, *op.Fact, a)
	case OpFactDelete:
		return applyFactDelete(s, *op.Fact)
	case OpGoalSet:
		return applyGoalSet(s, *op.Goal, a)
	}
	return fmt.Errorf("%w: %q", ErrUnknownOp, safetext.OneLine(string(op.Op)))
}

// applyUpsert changes what the op carries and leaves the rest of the item
// alone. The declaration is what a session and a person write; the phase, the
// run id, the spent approval, the attempt count and the intent are the
// daemon's, and an upsert carrying one of them from any other class is
// refused — otherwise a session could requeue an approved item, mark a
// dependency done or forge a spent approval.
func applyUpsert(s *State, op WorkItemOp, a Actor) error {
	daemonOnly := op.Phase != nil || op.RunID != nil || op.SpentApprovalAt != nil || op.Attempts != nil || op.Intent != nil
	if daemonOnly && a.Class != Daemon {
		return fmt.Errorf("%w: a %s may not write a work item's phase, run id, spent approval, attempts or intent", ErrActorNotAllowed, a.Class)
	}
	i, exists := s.indexOf(op.ID)
	w := WorkItem{ID: op.ID, Phase: PhaseQueued, DependsOn: []string{}}
	if exists {
		w = s.WorkItems[i]
	}
	before := SpecDigest(w)

	if op.Title != nil {
		w.Title = cleanLine(*op.Title, MaxTitleRunes)
	}
	if op.Repo != nil {
		repo, err := task.CanonicalRepo(*op.Repo)
		if err != nil {
			return fmt.Errorf("%w: the repository %q is not owner/name", ErrInvalid, safetext.OneLine(*op.Repo))
		}
		w.Repo = repo
	}
	if op.Recipe != nil {
		w.Recipe = *op.Recipe
	}
	if op.RigorTier != nil {
		w.RigorTier = *op.RigorTier
	}
	if op.Brief != nil {
		w.Brief = cleanText(*op.Brief, MaxBriefRunes)
	}
	if op.Note != nil {
		w.Note = cleanText(*op.Note, MaxNoteRunes)
	}
	if op.DependsOn != nil {
		w.DependsOn = copyOf(*op.DependsOn)
	}

	if SpecDigest(w) != before {
		// The person approved the spec they read. An edit makes this another
		// spec, so the approval is off — one digest covers every declarative
		// field, and this is the only place that has to remember it.
		w.Approved = nil
	}

	if op.Phase != nil {
		if exists && !w.Phase.CanMoveTo(*op.Phase) {
			return fmt.Errorf("%w: %s may not move to %s", ErrInvalid, w.Phase, *op.Phase)
		}
		w.Phase = *op.Phase
		if w.Phase.Done() {
			// A standing approval never outlives the item it approves: a
			// finished item left with one is one a later tick could relaunch.
			w.Approved = nil
		}
	}
	if op.RunID != nil {
		w.RunID = *op.RunID
	}
	if op.SpentApprovalAt != nil {
		at := stamp(*op.SpentApprovalAt)
		w.SpentApprovalAt = &at
	}
	if op.Attempts != nil {
		w.Attempts = *op.Attempts
	}
	if op.Intent != nil {
		if err := commitIntent(s, &w, *op.Intent, a); err != nil {
			return err
		}
	}

	if exists {
		s.WorkItems[i] = w
		return nil
	}
	if w.Title == "" || w.Repo == "" || w.Recipe == "" || w.RigorTier == "" || w.Brief == "" {
		return fmt.Errorf("%w: a new work item carries a title, a repository, a recipe, a rigor tier and a brief", ErrInvalid)
	}
	s.WorkItems = append(s.WorkItems, w)
	return nil
}

// commitIntent records the attempt a launch is for, or how an attempt ended.
// The declaration is the whole of intent-first (D1231): the run id is minted
// before anything starts and committed here with the digest of the spec being
// launched and the At of the approval it spends, so a daemon that dies before
// the start re-issues the same launch instead of minting a second one.
//
// It is also the whole of the retry rule. An attempt may only be the next one
// under the standing approval, the launch spec must be the one that approval
// covers, and the attempt the approval covers last — its retry — spends the
// allowance: Approved is cleared and SpentApprovalAt is set in this same
// write, while the first attempt clears nothing, so a second failure needs a
// person's new approval.
//
// A declaration is refused unless the item is queued and no attempt of it is
// still in flight: an intent is the record of a run, so one without an outcome
// is a run that has not reported, and a second attempt under it would spend an
// allowance, clear an approval and overwrite a run id that belong to the run
// already going.
func commitIntent(s *State, w *WorkItem, op IntentOp, a Actor) error {
	attempt := IntentAttempt(op.ID)
	if IntentItem(op.ID) != w.ID || attempt < 1 {
		return fmt.Errorf("%w: the intent id %q is not \"<item>:<attempt>\" for this item", ErrInvalid, safetext.OneLine(op.ID))
	}
	for j := range s.Intents {
		if s.Intents[j].ID != op.ID {
			continue
		}
		if op.RunID != nil || op.SpecDigest != nil || op.ApprovalAt != nil {
			return fmt.Errorf("%w: the intent %q is declared once, and re-declaring it would mint a second launch under it", ErrIntentDeclared, safetext.OneLine(op.ID))
		}
		if op.Outcome == nil {
			return fmt.Errorf("%w: the intent %q carries neither a declaration nor an outcome", ErrInvalid, safetext.OneLine(op.ID))
		}
		if s.Intents[j].Outcome != "" {
			return fmt.Errorf("%w: the intent %q already ended %s", ErrIntentDeclared, safetext.OneLine(op.ID), s.Intents[j].Outcome)
		}
		s.Intents[j].Outcome = *op.Outcome
		return nil
	}
	if op.RunID == nil || op.SpecDigest == nil || op.ApprovalAt == nil {
		return fmt.Errorf("%w: a declared intent carries the run id minted for it, the digest of the spec and the At of the approval it spends", ErrInvalid)
	}
	if op.Outcome != nil {
		return fmt.Errorf("%w: an intent is declared before the run reports anything", ErrInvalid)
	}
	if w.Approved == nil {
		return fmt.Errorf("%w: %q has no standing approval, and a cycle launches nothing else", ErrActorNotAllowed, w.ID)
	}
	approval := *w.Approved
	if !approval.At.Equal(*op.ApprovalAt) {
		return fmt.Errorf("%w: the intent spends the approval from %s and the one standing is from %s", ErrDigestMismatch, stamp(*op.ApprovalAt).Format(time.RFC3339), approval.At.Format(time.RFC3339))
	}
	if next := attemptsCommitted(*s, w.ID) + 1; attempt != next {
		return fmt.Errorf("%w: attempt %d is being declared and the item's next attempt is %d", ErrInvalid, attempt, next)
	}
	under := AttemptsUnder(*s, w.ID, approval.At)
	if under >= approval.Attempts {
		return fmt.Errorf("%w: the approval covers %d attempt(s) and %d of them are committed", ErrActorNotAllowed, approval.Attempts, under)
	}
	if approval.SpecDigest != *op.SpecDigest || SpecDigest(*w) != *op.SpecDigest {
		return fmt.Errorf("%w: the launch spec is not the one the approval covers, so nothing may start under it", ErrDigestMismatch)
	}
	// One attempt at a time, started from queued and nowhere else. The phase
	// table alone would not say so — running may move to itself — and an
	// attempt declared over a run in flight would take the retry's allowance,
	// clear the approval that covers it and overwrite the run id of a run that
	// is still going. failed and blocked reach queued first, which is the path
	// the retry takes.
	if w.Phase != PhaseQueued {
		return fmt.Errorf("%w: the item is %s, and an attempt starts from %s", ErrInvalid, w.Phase, PhaseQueued)
	}
	if open := openIntent(s.Intents, w.ID); open != "" {
		return fmt.Errorf("%w: the attempt %s has not reported how it went, and one run at a time carries an item", ErrInvalid, safetext.OneLine(open))
	}
	s.Intents = append(s.Intents, Intent{
		ID:         op.ID,
		RunID:      *op.RunID,
		SpecDigest: *op.SpecDigest,
		ApprovalAt: approval.At,
		At:         stamp(a.Now),
	})
	w.RunID = *op.RunID
	w.Attempts = attempt
	w.Phase = PhaseRunning
	if under+1 >= approval.Attempts {
		spent := approval.At
		w.SpentApprovalAt = &spent
		w.Approved = nil
	}
	return nil
}

// applyApprove is a person's go, and only a person's (G2). The op carries the
// digest the person was shown and it must be the item's digest now: the
// approval binds the spec that was read, not whatever the item has become
// since, and a session — which can read the item and compute a digest — can
// therefore not forge one through the CLI.
func applyApprove(s *State, op ApproveOp, a Actor) error {
	if a.Class != Person {
		return fmt.Errorf("%w: a %s may not approve a work item", ErrActorNotAllowed, a.Class)
	}
	if a.Name == "" {
		return fmt.Errorf("%w: an approval names the person who gave it", ErrInvalid)
	}
	i, ok := s.indexOf(op.ID)
	if !ok {
		return fmt.Errorf("%w: no work item %q", ErrNotFound, safetext.OneLine(op.ID))
	}
	if got := SpecDigest(s.WorkItems[i]); got != op.SpecDigest {
		return fmt.Errorf("%w: the item's spec digest is %s and the approval carries %s: read the item again and approve what it says now", ErrDigestMismatch, got, op.SpecDigest)
	}
	s.WorkItems[i].Approved = &Approval{
		By:         cleanLine(a.Name, MaxActorNameRunes),
		At:         stamp(a.Now),
		SpecDigest: op.SpecDigest,
		Attempts:   ApprovalAttempts,
	}
	// The spent marker belongs to the approval it was spent on, so a new one
	// clears it. What a new approval gets is a fresh allowance, which is read
	// off the intents stamped with its own At (AttemptsUnder) and not off the
	// item's count, which runs on.
	s.WorkItems[i].SpentApprovalAt = nil
	return nil
}

// applyArchive moves an item out of the work list. What may leave is what
// archivable says may, so retention and this op agree on every item, and the
// refusal a caller gets names the reason: a phase that is not finished, a run
// in flight, an approval still standing, or something still in the list that
// names it. The run in flight is not the same question as the phase — an item
// the daemon moved to cancelled under an attempt that has not reported is one
// a run is still going in, and archiving it would take the run id, and the
// phase the outcome lands in, away with it. It cannot start anything: what it
// can do is stop work, which is the direction a session should be able to
// push.
func applyArchive(s *State, op ArchiveOp, a Actor) error {
	i, ok := s.indexOf(op.ID)
	if !ok {
		return fmt.Errorf("%w: no work item %q", ErrNotFound, safetext.OneLine(op.ID))
	}
	outcome := cleanLine(op.Outcome, MaxOutcomeRunes)
	if outcome == "" {
		return fmt.Errorf("%w: an archived work item carries how it ended", ErrInvalid)
	}
	w := s.WorkItems[i]
	if err := archivable(s.WorkItems, s.Intents, w); err != nil {
		return err
	}
	s.Archived = append(s.Archived, Archived{ID: w.ID, Outcome: outcome, At: stamp(a.Now)})
	s.WorkItems = append(s.WorkItems[:i], s.WorkItems[i+1:]...)
	return nil
}

// applyDecisionAdd writes a question. Adding one takes no class — the daemon
// and its agents ask people things, and a person reads them — with one
// exception: the breaker's own prefix is the daemon's, because a decision
// carrying it stops every spawn on the node until a person resolves it
// (BreakerOpen, which the plan refuses over). A read of the state cannot tell
// who wrote a decision, so the class has to be settled here or the pause is
// anyone's to raise (D1241 leaves the node writable by whoever holds the
// person's credential, which is what makes the class the fence rather than the
// credential).
func applyDecisionAdd(s *State, op DecisionOp, a Actor) error {
	if _, ok := s.Decision(op.ID); ok {
		return fmt.Errorf("%w: the decision %q is already there", ErrInvalid, safetext.OneLine(op.ID))
	}
	if a.Class != Daemon && strings.HasPrefix(op.ID, BreakerDecisionID+"-") {
		return fmt.Errorf("%w: a %s actor asked the breaker's question in %q, and the pause it carries is the daemon's to raise", ErrActorNotAllowed, a.Class, safetext.OneLine(op.ID))
	}
	question := cleanText(op.Question, MaxQuestionRunes)
	if question == "" {
		return fmt.Errorf("%w: a decision carries the question it asks", ErrInvalid)
	}
	s.Decisions = append(s.Decisions, Decision{
		ID:        op.ID,
		Question:  question,
		WaitingOn: op.WaitingOn,
		At:        stamp(a.Now),
	})
	return nil
}

// applyDecisionResolve closes a question. The class the decision waits on is
// the class that may close it — a person may close any of them, since the
// others' answers are a person's to overrule — so the answer's actor is
// checked against the decision rather than taken on the patch's word.
func applyDecisionResolve(s *State, op ResolveOp, a Actor) error {
	i := -1
	for j, d := range s.Decisions {
		if d.ID == op.ID {
			i = j
			break
		}
	}
	if i < 0 {
		return fmt.Errorf("%w: no decision %q", ErrNotFound, safetext.OneLine(op.ID))
	}
	d := s.Decisions[i]
	if d.Resolved() {
		return fmt.Errorf("%w: the decision %q was closed at %s", ErrInvalid, safetext.OneLine(d.ID), d.ResolvedAt.Format(time.RFC3339))
	}
	if a.Class != Person && a.Class != d.WaitingOn.Class() {
		return fmt.Errorf("%w: the decision %q waits on a %s and a %s answered", ErrActorNotAllowed, safetext.OneLine(d.ID), d.WaitingOn, a.Class)
	}
	answer := cleanText(op.Answer, MaxAnswerRunes)
	if answer == "" {
		return fmt.Errorf("%w: a closed decision carries what was decided", ErrInvalid)
	}
	at := stamp(a.Now)
	d.Answer = answer
	d.By = a.Class
	d.ResolvedAt = &at
	s.Decisions[i] = d
	return nil
}

func applyFactSet(s *State, op FactOp, a Actor) error {
	value := cleanLine(op.Value, MaxFactValueRunes)
	if value == "" {
		return fmt.Errorf("%w: a fact carries a value, and deleting one is fact_delete", ErrInvalid)
	}
	if s.Facts == nil {
		s.Facts = map[string]Fact{}
	}
	s.Facts[op.Key] = Fact{Value: value, By: a.Class}
	return nil
}

func applyFactDelete(s *State, op FactOp) error {
	if _, ok := s.Facts[op.Key]; !ok {
		return fmt.Errorf("%w: no fact %q", ErrNotFound, safetext.OneLine(op.Key))
	}
	delete(s.Facts, op.Key)
	return nil
}

func applyGoalSet(s *State, op GoalOp, a Actor) error {
	if a.Class != Person {
		return fmt.Errorf("%w: a %s may not set the goal", ErrActorNotAllowed, a.Class)
	}
	goal := cleanText(op.Goal, MaxGoalRunes)
	if goal == "" {
		return fmt.Errorf("%w: a goal carries what the loop is for", ErrInvalid)
	}
	s.Goal = goal
	return nil
}

// indexOf finds a work item's place in the state.
func (s State) indexOf(id string) (int, bool) {
	for i, w := range s.WorkItems {
		if w.ID == id {
			return i, true
		}
	}
	return 0, false
}
