package orchestrator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The shapes the document's own names take. A name is also a database key: the
// state node is a JSON document in the Realtime Database, where a property name
// is a path segment and the transport refuses `. $ # [ ] /` and control
// characters anywhere in the tree (internal/gcpfake mirrors that rule:
// TestRTDBFakeRejectsKeysRealRTDBRejects). A fact key is written as a property
// name, so a name carrying a dot would validate, encode and then be refused by
// the node itself — the orchestrator id wedged, because every tick re-derives
// the same state, and the intent that would stop a second spawn never
// committed (D1231). No name carries a colon either: an intent's id is
// "<item>:<attempt>", so an item whose id carried one would name two attempts
// at once.
var (
	idRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	agentRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	roleRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// DecodeState reads a state node. The decode is strict — a field this release
// does not model is an error, not something to carry along — and a node
// written by an older release is raised to this one's schema version before
// its shape is checked.
//
// This is the whole shape and size check the design has. The RTDB rules
// refuse a token writer outright (O2), so a document that exists at all was
// written by the person's own credential, which IAM lets past those rules;
// what stops a malformed or oversized one from being read is here.
func DecodeState(raw []byte) (State, error) {
	s, err := decodeStrict[State](raw)
	if err != nil {
		return State{}, err
	}
	s, _ = Migrate(s)
	if err := ValidateState(s); err != nil {
		return State{}, err
	}
	return s, nil
}

// EncodeState writes a state node, refusing a document this release would not
// read back. It is checked on the way out as well as on the way in: the only
// writer is a program, and a document that fails its own validator is a bug
// worth stopping at rather than a node worth publishing.
//
// It marshals a clone, which is what keeps the published schema honest for a
// state a caller has just built: a slice or a map nobody has appended to is
// nil in Go, and a nil one marshals as null, which the schema — every
// collection an array or an object — refuses. Clone fills them in empty.
func EncodeState(s State) ([]byte, error) {
	if err := ValidateState(s); err != nil {
		return nil, err
	}
	return json.Marshal(s.Clone())
}

// DecodePatch reads the patch a caller sent. Strict, like DecodeState: an
// unknown field on an op is an error, which is how a patch carrying an
// approval is refused — there is no approval field to fill in, for any actor.
func DecodePatch(raw []byte) (Patch, error) {
	p, err := decodeStrict[Patch](raw)
	if err != nil {
		return Patch{}, err
	}
	if err := ValidatePatch(p); err != nil {
		return Patch{}, err
	}
	return p, nil
}

func decodeStrict[T any](raw []byte) (T, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("%w: the document carries something this release does not model: %v", ErrInvalid, err)
	}
	// One value and nothing after it. A decoder stops at the end of the first
	// value, so `{...}{...}` and `{...} junk` both decode to the document
	// checked above and leave the rest — bytes the node really carries, and
	// bytes a reader of the raw value sees — unexamined. The validator is the
	// whole shape check (D1241), so it reads the whole value.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return v, fmt.Errorf("%w: the document is not one JSON value, there is more after it", ErrInvalid)
	}
	return v, nil
}

// ValidateState is every rule a document must satisfy to be read: the shapes
// of its names, the caps on its free text, the invariants the phases and the
// approvals hold each other to, and the size target.
//
// One thing it deliberately does not check is that a standing approval's
// digest equals the item's current one. That is not a shape: an item whose
// digested fields a later release digests differently must still load, and the
// refusal belongs where it is a decision — the approve op refuses a digest
// that has moved, and the launcher refuses to start under one (O6) — rather
// than here, where it would take a whole daemon down over one stale approval.
func ValidateState(s State) error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: the document says %d, this release writes %d", ErrSchemaVersion, s.SchemaVersion, SchemaVersion)
	}
	if s.Rev < 0 {
		return fmt.Errorf("%w: the revision is %d", ErrInvalid, s.Rev)
	}
	if !agentRE.MatchString(s.AgentID) {
		return fmt.Errorf("%w: the agent id %q is not a name", ErrInvalid, safetext.OneLine(s.AgentID))
	}
	if !roleRE.MatchString(s.Role) {
		return fmt.Errorf("%w: the role %q is not a loop's name", ErrInvalid, safetext.OneLine(s.Role))
	}
	if over(s.Goal, MaxGoalRunes) {
		return fmt.Errorf("%w: the goal is over %d runes", ErrTooLarge, MaxGoalRunes)
	}
	if len(s.WorkItems) > MaxWorkItems {
		return fmt.Errorf("%w: %d work items, at most %d", ErrTooLarge, len(s.WorkItems), MaxWorkItems)
	}
	seen := make(map[string]bool, len(s.WorkItems))
	for _, w := range s.WorkItems {
		if err := validateItem(s, w); err != nil {
			return err
		}
		if seen[w.ID] {
			return fmt.Errorf("%w: two work items share the id %q", ErrInvalid, safetext.OneLine(w.ID))
		}
		seen[w.ID] = true
	}
	if len(s.Decisions) > MaxDecisions {
		return fmt.Errorf("%w: %d decisions, at most %d", ErrTooLarge, len(s.Decisions), MaxDecisions)
	}
	seenDecision := make(map[string]bool, len(s.Decisions))
	for _, d := range s.Decisions {
		if err := validateDecision(d); err != nil {
			return err
		}
		// A decision is looked up by id: resolving, reading and counting a
		// question all find the first entry that carries it, so a second one
		// with the same id is a question nobody can close and a call counted
		// twice.
		if seenDecision[d.ID] {
			return fmt.Errorf("%w: two decisions share the id %q", ErrInvalid, safetext.OneLine(d.ID))
		}
		seenDecision[d.ID] = true
	}
	if len(s.Facts) > MaxFacts {
		return fmt.Errorf("%w: %d facts, at most %d", ErrTooLarge, len(s.Facts), MaxFacts)
	}
	for _, k := range sortedKeys(s.Facts) {
		f := s.Facts[k]
		if !idRE.MatchString(k) {
			return fmt.Errorf("%w: the fact key %q is not a name", ErrInvalid, safetext.OneLine(k))
		}
		if f.Value == "" {
			return fmt.Errorf("%w: the fact %q carries no value, and setting one is how a fact is written", ErrInvalid, safetext.OneLine(k))
		}
		if over(f.Value, MaxFactValueRunes) {
			return fmt.Errorf("%w: the fact %q is over %d runes", ErrTooLarge, safetext.OneLine(k), MaxFactValueRunes)
		}
		if !f.By.Known() {
			return fmt.Errorf("%w: the fact %q is from %q", ErrInvalid, safetext.OneLine(k), safetext.OneLine(string(f.By)))
		}
	}
	if len(s.Archived) > MaxArchived {
		return fmt.Errorf("%w: %d archived items, at most %d", ErrTooLarge, len(s.Archived), MaxArchived)
	}
	for _, a := range s.Archived {
		if !idRE.MatchString(a.ID) {
			return fmt.Errorf("%w: the archived id %q is not a name", ErrInvalid, safetext.OneLine(a.ID))
		}
		if a.Outcome == "" {
			return fmt.Errorf("%w: the archived item %q carries no outcome", ErrInvalid, safetext.OneLine(a.ID))
		}
		if over(a.Outcome, MaxOutcomeRunes) {
			return fmt.Errorf("%w: an archived outcome is over %d runes", ErrTooLarge, MaxOutcomeRunes)
		}
	}
	if len(s.Intents) > MaxIntents {
		return fmt.Errorf("%w: %d intents, at most %d", ErrTooLarge, len(s.Intents), MaxIntents)
	}
	seenIntent := make(map[string]bool, len(s.Intents))
	for _, it := range s.Intents {
		if err := validateIntent(it); err != nil {
			return err
		}
		// An intent's id is the item and the attempt, and it is unique for the
		// item's whole life: the count an approval is spent against, the next
		// attempt number and the outcome a run reports all find an intent by
		// it, so two with one id would double-count the allowance and leave
		// the second attempt's outcome nowhere to land.
		if seenIntent[it.ID] {
			return fmt.Errorf("%w: two intents share the id %q", ErrInvalid, safetext.OneLine(it.ID))
		}
		seenIntent[it.ID] = true
	}
	if s.Counters.DecisionsHour < 0 || s.Counters.DecisionsDay < 0 || s.Counters.DecisionsHour > s.Counters.DecisionsDay {
		return fmt.Errorf("%w: the counters say %d calls this hour and %d today", ErrInvalid, s.Counters.DecisionsHour, s.Counters.DecisionsDay)
	}
	if OverCap(s) {
		return fmt.Errorf("%w: %d bytes, the target is %d", ErrTooLarge, stateBytes(s), MaxStateBytes)
	}
	return nil
}

func validateItem(s State, w WorkItem) error {
	if !idRE.MatchString(w.ID) {
		return fmt.Errorf("%w: the work item id %q is not a name", ErrInvalid, safetext.OneLine(w.ID))
	}
	if w.Title == "" {
		return fmt.Errorf("%w: the work item %q carries no title", ErrInvalid, w.ID)
	}
	if over(w.Title, MaxTitleRunes) {
		return fmt.Errorf("%w: the title of %q is over %d runes", ErrTooLarge, w.ID, MaxTitleRunes)
	}
	if _, err := task.CanonicalRepo(w.Repo); err != nil {
		return fmt.Errorf("%w: the repository of %q is not owner/name", ErrInvalid, w.ID)
	}
	if !recipe.NameRE.MatchString(w.Recipe) {
		return fmt.Errorf("%w: the recipe of %q is not a recipe name", ErrInvalid, w.ID)
	}
	if !tier.Known(tier.Tier(w.RigorTier)) {
		return fmt.Errorf("%w: the rigor tier of %q is %q, not one of %s", ErrInvalid, w.ID, safetext.OneLine(w.RigorTier), tierList())
	}
	if w.Brief == "" {
		return fmt.Errorf("%w: the work item %q carries no brief", ErrInvalid, w.ID)
	}
	if over(w.Brief, MaxBriefRunes) {
		return fmt.Errorf("%w: the brief of %q is over %d runes", ErrTooLarge, w.ID, MaxBriefRunes)
	}
	if over(w.Note, MaxNoteRunes) {
		return fmt.Errorf("%w: the note of %q is over %d runes", ErrTooLarge, w.ID, MaxNoteRunes)
	}
	if !w.Phase.Known() {
		return fmt.Errorf("%w: %q is not a phase", ErrInvalid, safetext.OneLine(string(w.Phase)))
	}
	if w.Phase == PhaseRunning && w.RunID == "" {
		return fmt.Errorf("%w: %q is running with no run id", ErrInvalid, w.ID)
	}
	if w.RunID != "" && !task.ValidRunID(w.RunID) {
		return fmt.Errorf("%w: the run id of %q is not a run id", ErrInvalid, w.ID)
	}
	if w.Phase.Done() && w.Approved != nil {
		return fmt.Errorf("%w: %q is %s and still carries an approval", ErrInvalid, w.ID, w.Phase)
	}
	if w.Attempts < attemptsCommitted(s, w.ID) {
		return fmt.Errorf("%w: %q says %d attempts and carries an intent for attempt %d", ErrInvalid, w.ID, w.Attempts, attemptsCommitted(s, w.ID))
	}
	if w.SpentApprovalAt != nil && w.SpentApprovalAt.IsZero() {
		return fmt.Errorf("%w: %q carries an empty spent approval time", ErrInvalid, w.ID)
	}
	if a := w.Approved; a != nil {
		if a.By == "" {
			return fmt.Errorf("%w: the approval of %q names nobody", ErrInvalid, w.ID)
		}
		if a.At.IsZero() {
			return fmt.Errorf("%w: the approval of %q carries no time", ErrInvalid, w.ID)
		}
		if !DigestKnown(a.SpecDigest) {
			return fmt.Errorf("%w: the approval of %q carries no spec digest", ErrInvalid, w.ID)
		}
		if a.Attempts != ApprovalAttempts {
			return fmt.Errorf("%w: the approval of %q covers %d attempts, and one covers %d", ErrInvalid, w.ID, a.Attempts, ApprovalAttempts)
		}
	}
	if w.Approved != nil && w.SpentApprovalAt != nil {
		// The marker names the approval a retry spent, and the write that
		// spends it clears the approval in the same one, so the two are never
		// true at once. Both at once is a document that says an item has an
		// allowance and has none, and the summary reads the approval and
		// reports the item as launchable.
		return fmt.Errorf("%w: %q carries a standing approval and a spent one at once", ErrInvalid, w.ID)
	}
	if len(w.DependsOn) > MaxDependsOn {
		return fmt.Errorf("%w: %q depends on %d items, at most %d", ErrTooLarge, w.ID, len(w.DependsOn), MaxDependsOn)
	}
	for i, dep := range w.DependsOn {
		if dep == w.ID {
			return fmt.Errorf("%w: %q depends on itself", ErrInvalid, w.ID)
		}
		if slices.Contains(w.DependsOn[:i], dep) {
			return fmt.Errorf("%w: %q depends on %q twice", ErrInvalid, w.ID, safetext.OneLine(dep))
		}
		if _, ok := s.Item(dep); !ok {
			return fmt.Errorf("%w: %q depends on %q, which is not a work item", ErrInvalid, w.ID, safetext.OneLine(dep))
		}
	}
	return nil
}

func validateDecision(d Decision) error {
	if !idRE.MatchString(d.ID) {
		return fmt.Errorf("%w: the decision id %q is not a name", ErrInvalid, safetext.OneLine(d.ID))
	}
	if d.Question == "" {
		return fmt.Errorf("%w: the decision %q carries no question", ErrInvalid, d.ID)
	}
	if over(d.Question, MaxQuestionRunes) {
		return fmt.Errorf("%w: the question of %q is over %d runes", ErrTooLarge, d.ID, MaxQuestionRunes)
	}
	if !d.WaitingOn.Known() {
		return fmt.Errorf("%w: the decision %q waits on %q", ErrInvalid, d.ID, safetext.OneLine(string(d.WaitingOn)))
	}
	if d.At.IsZero() {
		return fmt.Errorf("%w: the decision %q carries no time", ErrInvalid, d.ID)
	}
	if over(d.Answer, MaxAnswerRunes) {
		return fmt.Errorf("%w: the answer to %q is over %d runes", ErrTooLarge, d.ID, MaxAnswerRunes)
	}
	if !d.Resolved() {
		if d.Answer != "" || d.By != "" {
			return fmt.Errorf("%w: the decision %q carries an answer and no resolution time", ErrInvalid, d.ID)
		}
		return nil
	}
	if d.Answer == "" {
		return fmt.Errorf("%w: the decision %q is closed with no answer", ErrInvalid, d.ID)
	}
	if !d.By.Known() {
		return fmt.Errorf("%w: the decision %q was closed by %q", ErrInvalid, d.ID, safetext.OneLine(string(d.By)))
	}
	if d.ResolvedAt.IsZero() {
		return fmt.Errorf("%w: the decision %q carries an empty resolution time", ErrInvalid, d.ID)
	}
	return nil
}

func validateIntent(it Intent) error {
	item, attempt := IntentItem(it.ID), IntentAttempt(it.ID)
	if !idRE.MatchString(item) || attempt < 1 {
		return fmt.Errorf("%w: the intent id %q is not \"<item>:<attempt>\"", ErrInvalid, safetext.OneLine(it.ID))
	}
	if !task.ValidRunID(it.RunID) {
		return fmt.Errorf("%w: the intent %q carries no run id", ErrInvalid, safetext.OneLine(it.ID))
	}
	if !DigestKnown(it.SpecDigest) {
		return fmt.Errorf("%w: the intent %q carries no spec digest", ErrInvalid, safetext.OneLine(it.ID))
	}
	if it.ApprovalAt.IsZero() {
		return fmt.Errorf("%w: the intent %q names no approval", ErrInvalid, safetext.OneLine(it.ID))
	}
	if it.At.IsZero() {
		return fmt.Errorf("%w: the intent %q carries no time", ErrInvalid, safetext.OneLine(it.ID))
	}
	if it.Outcome != "" && !it.Outcome.Known() {
		return fmt.Errorf("%w: the intent %q ended %q", ErrInvalid, safetext.OneLine(it.ID), safetext.OneLine(string(it.Outcome)))
	}
	return nil
}

// ValidatePatch is every rule a patch satisfies before it is applied: it
// carries ops, it names ops this release has, each op carries exactly the
// payload its kind takes, and what the payload carries is shaped. Which class
// of actor may send which op is not a shape and lives in Apply, with the rest
// of the fence.
func ValidatePatch(p Patch) error {
	if p.BaseRev < 0 {
		return fmt.Errorf("%w: the base revision is %d", ErrInvalid, p.BaseRev)
	}
	if len(p.Ops) == 0 {
		return fmt.Errorf("%w: the patch carries no op, so it would change nothing", ErrInvalid)
	}
	if len(p.Ops) > MaxOps {
		return fmt.Errorf("%w: the patch carries %d ops, at most %d", ErrTooLarge, len(p.Ops), MaxOps)
	}
	for i, op := range p.Ops {
		if err := validateOp(op); err != nil {
			return fmt.Errorf("op %d: %w", i+1, err)
		}
	}
	return nil
}

// opPayloads names the payload each op kind takes.
var opPayloads = map[OpKind]string{
	OpWorkItemUpsert:  "work_item",
	OpWorkItemApprove: "approve",
	OpWorkItemArchive: "archive",
	OpDecisionAdd:     "decision",
	OpDecisionResolve: "resolve",
	OpFactSet:         "fact",
	OpFactDelete:      "fact",
	OpGoalSet:         "goal",
}

func validateOp(op Op) error {
	if !op.Op.Known() {
		return fmt.Errorf("%w: %q is not an op this release has (%s)", ErrUnknownOp, safetext.OneLine(string(op.Op)), opList())
	}
	carried := make([]string, 0, 1)
	for _, p := range []struct {
		name    string
		carried bool
	}{
		{"work_item", op.WorkItem != nil},
		{"approve", op.Approve != nil},
		{"archive", op.Archive != nil},
		{"decision", op.Decision != nil},
		{"resolve", op.Resolve != nil},
		{"fact", op.Fact != nil},
		{"goal", op.Goal != nil},
	} {
		if p.carried {
			carried = append(carried, p.name)
		}
	}
	want := opPayloads[op.Op]
	if len(carried) == 0 {
		return fmt.Errorf("%w: %s carries no payload", ErrOpPayload, op.Op)
	}
	if len(carried) > 1 {
		return fmt.Errorf("%w: %s carries %s", ErrOpPayload, op.Op, strings.Join(carried, " and "))
	}
	if carried[0] != want {
		return fmt.Errorf("%w: %s takes %s, not %s", ErrOpPayload, op.Op, want, carried[0])
	}

	switch op.Op {
	case OpWorkItemUpsert:
		return validateUpsert(*op.WorkItem)
	case OpWorkItemApprove:
		if !idRE.MatchString(op.Approve.ID) {
			return fmt.Errorf("%w: the approved item id %q is not a name", ErrInvalid, safetext.OneLine(op.Approve.ID))
		}
		if !DigestKnown(op.Approve.SpecDigest) {
			return fmt.Errorf("%w: an approval carries the digest of the spec the person read", ErrInvalid)
		}
	case OpWorkItemArchive:
		if !idRE.MatchString(op.Archive.ID) {
			return fmt.Errorf("%w: the archived item id %q is not a name", ErrInvalid, safetext.OneLine(op.Archive.ID))
		}
	case OpDecisionAdd:
		if !idRE.MatchString(op.Decision.ID) {
			return fmt.Errorf("%w: the decision id %q is not a name", ErrInvalid, safetext.OneLine(op.Decision.ID))
		}
		if !op.Decision.WaitingOn.Known() {
			return fmt.Errorf("%w: a decision waits on a person, a session or the daemon, not on %q", ErrInvalid, safetext.OneLine(string(op.Decision.WaitingOn)))
		}
	case OpDecisionResolve:
		if !idRE.MatchString(op.Resolve.ID) {
			return fmt.Errorf("%w: the decision id %q is not a name", ErrInvalid, safetext.OneLine(op.Resolve.ID))
		}
	case OpFactSet, OpFactDelete:
		if !idRE.MatchString(op.Fact.Key) {
			return fmt.Errorf("%w: the fact key %q is not a name", ErrInvalid, safetext.OneLine(op.Fact.Key))
		}
		if op.Op == OpFactDelete && op.Fact.Value != "" {
			return fmt.Errorf("%w: a delete of %q carries a value", ErrInvalid, safetext.OneLine(op.Fact.Key))
		}
	}
	return nil
}

func validateUpsert(op WorkItemOp) error {
	if !idRE.MatchString(op.ID) {
		return fmt.Errorf("%w: the work item id %q is not a name", ErrInvalid, safetext.OneLine(op.ID))
	}
	if op.Title != nil && *op.Title == "" {
		return fmt.Errorf("%w: a work item's title may not be emptied", ErrInvalid)
	}
	if op.Brief != nil && *op.Brief == "" {
		return fmt.Errorf("%w: a work item's brief may not be emptied", ErrInvalid)
	}
	if op.Repo != nil {
		if _, err := task.CanonicalRepo(*op.Repo); err != nil {
			return fmt.Errorf("%w: the repository %q is not owner/name", ErrInvalid, safetext.OneLine(*op.Repo))
		}
	}
	if op.Recipe != nil && !recipe.NameRE.MatchString(*op.Recipe) {
		return fmt.Errorf("%w: the recipe %q is not a recipe name", ErrInvalid, safetext.OneLine(*op.Recipe))
	}
	if op.RigorTier != nil && !tier.Known(tier.Tier(*op.RigorTier)) {
		return fmt.Errorf("%w: the rigor tier %q is not one of %s", ErrInvalid, safetext.OneLine(*op.RigorTier), tierList())
	}
	if op.Phase != nil && !op.Phase.Known() {
		return fmt.Errorf("%w: %q is not a phase", ErrInvalid, safetext.OneLine(string(*op.Phase)))
	}
	if op.DependsOn != nil {
		if len(*op.DependsOn) > MaxDependsOn {
			return fmt.Errorf("%w: %d dependencies, at most %d", ErrTooLarge, len(*op.DependsOn), MaxDependsOn)
		}
		for _, dep := range *op.DependsOn {
			if !idRE.MatchString(dep) {
				return fmt.Errorf("%w: the dependency %q is not a work item id", ErrInvalid, safetext.OneLine(dep))
			}
			if dep == op.ID {
				return fmt.Errorf("%w: %q depends on itself", ErrInvalid, safetext.OneLine(op.ID))
			}
		}
	}
	if op.RunID != nil && *op.RunID != "" && !task.ValidRunID(*op.RunID) {
		return fmt.Errorf("%w: the run id %q is not a run id", ErrInvalid, safetext.OneLine(*op.RunID))
	}
	if op.Attempts != nil && *op.Attempts < 0 {
		return fmt.Errorf("%w: %d attempts", ErrInvalid, *op.Attempts)
	}
	if op.SpentApprovalAt != nil && op.SpentApprovalAt.IsZero() {
		return fmt.Errorf("%w: the spent approval time is empty", ErrInvalid)
	}
	return nil
}

// cleanLine and cleanText are what free text goes through on its way into the
// document: the terminal-unsafe characters come out (internal/safetext) and
// the result is cut to its cap, so the document can never carry a string a
// surface has to defend itself against. Cutting rather than refusing is the
// house's rule for a cap the caller cannot see the other end of, and it is
// self-consistent here: the item a person reads, and approves, is the item as
// it is stored.
func cleanLine(s string, maxRunes int) string { return safetext.Clip(safetext.OneLine(s), maxRunes) }

func cleanText(s string, maxRunes int) string { return safetext.Clip(safetext.MultiLine(s), maxRunes) }

func over(s string, maxRunes int) bool { return utf8.RuneCountInString(s) > maxRunes }

// stamp is a time as the document records it: UTC, to the second, which is the
// resolution the server clock the daemon reads has (rtdb.ServerNow). A
// caller's sub-second time is not a reading that clock could have given, so
// nothing here keeps one.
func stamp(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func opList() string {
	names := make([]string, len(OpKinds))
	for i, k := range OpKinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

func tierList() string { return tier.List() }

func stateBytes(s State) int {
	raw, err := json.Marshal(s)
	if err != nil {
		return MaxStateBytes + 1
	}
	return len(raw)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
