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

	"github.com/dimipaun/fugaro/internal/escalation"
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
	idRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	roleRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// headRE is a head as the ladder records one in its ledger: a git object name
// as a provider gives it, which is lower-hex and may be abbreviated — Bitbucket
// reports twelve characters. Seven is git's own abbreviation floor, which is
// what gitprov.SameCommit compares two heads down to, and sixty-four is a
// SHA-256 object name's length.
var headRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// specRE is a work item's spec name as a document stores it: the plan's folder
// under docs/, lower-cased. The bound is forty-four characters, so `spec:`
// plus the name is at most forty-nine of GitHub's fifty, and the shape is what
// keeps a name free text can be pasted into — a path, a colon, a space — from
// becoming a label name (R3, F7, F20).
//
// A patch may write the same name in either case (specInRE) and it is folded
// on the way in, so this pattern is the whole check on the way out of a
// document and the fold is what makes the two agree.
var specRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,43}$`)

// externalRE is a work item's external id: the ticket it came from outside
// Fugaro with, `github:<owner>/<repo>#<n>` (D2728). It is the dedupe key the
// whole backlog hangs on — one issue is one item, so a mirroring loop meets
// itself here — which is why the shape is one spelling and nothing else: the
// owner and the name are lower-case as the installation's own App answers
// them (gitprov/github's AppRepositories lower-cases them), they are the
// ASCII class a repository name is written with, and the number is the
// issue's, from one. It is a key the ingest builds and writes as built, not a
// spelling of something a person typed, so it is checked for shape and never
// folded: a fold here would make two spellings of one ticket one key in the
// ingest and two keys on the node.
//
// `github:` is the only host, because the Issues adapter is GitHub's alone
// (D2718): a second host here would have to mean a second ingest, and a key
// that named a host nothing reads is a key nothing can dedupe.
var externalRE = regexp.MustCompile(`^github:[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*#[1-9][0-9]{0,18}$`)

// bodySHA256RE is a ticket body's SHA-256 as an item stores it: sixty-four
// lower-case hex digits, the plain sum and not the `sha256:`-prefixed digest
// this package writes for a spec (D2720). It is a hash of text read from
// outside, so it is a shape and never free text: what a person approves is
// the body they were shown, and this is what says the two are one.
var bodySHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// specInRE is the same shape as a patch may write it, either case. Its class
// is ASCII and nothing else, so a name carrying a character outside it is
// refused rather than folded into one that passes — which is the whole of the
// Kelvin-sign rule: that sign lower-cases to an ASCII "k", so a name folded
// before it was checked would be read as another name than the one written
// (task.ASCIILower, and the two-step check task.CanonicalRepo makes).
var specInRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,43}$`)

// specName is R3 for a spec name as it is written: it reports the name this
// release stores — the ASCII fold of the one written, because that is how a
// host matches a label — and whether the name is one at all.
func specName(name string) (string, bool) {
	if !specInRE.MatchString(name) {
		return "", false
	}
	return task.ASCIILower(name), true
}

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
	if !ValidOrchestratorID(s.AgentID) {
		return fmt.Errorf("%w: the agent id %q is not an orchestrator id: an id is a project's name, or a project's name, a %s and a slug (naming.go)",
			ErrInvalid, safetext.OneLine(s.AgentID), NameSeparator)
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
	// An external id is unique across the project's nodes and not only within
	// this one (D2728): a mirroring loop is closed by the ingest finding the
	// item that already carries the key, and a second item with it anywhere
	// would be the duplicate the key exists to prevent. This node can see only
	// its own items, so it is here that one node refuses to hold two of them,
	// and the ingest is what holds the key unique across the rest (B4).
	seenExternal := make(map[string]string, len(s.WorkItems))
	for _, w := range s.WorkItems {
		if err := validateItem(s, w); err != nil {
			return err
		}
		if seen[w.ID] {
			return fmt.Errorf("%w: two work items share the id %q", ErrInvalid, safetext.OneLine(w.ID))
		}
		seen[w.ID] = true
		if w.External == "" {
			continue
		}
		if other, dup := seenExternal[w.External]; dup {
			return fmt.Errorf("%w: %q and %q carry the external id %s, and one ticket is one item", ErrInvalid, safetext.OneLine(other), safetext.OneLine(w.ID), safetext.OneLine(w.External))
		}
		seenExternal[w.External] = w.ID
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
	if w.Spec != "" && !specRE.MatchString(w.Spec) {
		return fmt.Errorf("%w: the spec of %q is %q, not a plan folder name", ErrInvalid, w.ID, safetext.OneLine(w.Spec))
	}
	if w.External != "" && !externalRE.MatchString(w.External) {
		return fmt.Errorf("%w: the external id of %q is %q, not github:<owner>/<repo>#<n>", ErrInvalid, w.ID, safetext.OneLine(w.External))
	}
	if w.BodySHA256 != "" && !bodySHA256RE.MatchString(w.BodySHA256) {
		return fmt.Errorf("%w: the body hash of %q is not a SHA-256 in lower-case hex", ErrInvalid, w.ID)
	}
	if w.PlanTask != "" {
		if p := task.PlanTaskProblem(w.PlanTask); p != "" {
			return fmt.Errorf("%w: the plan task of %q %s", ErrInvalid, w.ID, p)
		}
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
	if w.Escalation != nil {
		if err := validateEscalation(w.ID, *w.Escalation); err != nil {
			return err
		}
	}
	if len(w.DependsOn) > MaxDependsOn {
		return fmt.Errorf("%w: %q depends on %d items, at most %d", ErrTooLarge, w.ID, len(w.DependsOn), MaxDependsOn)
	}
	for i, dep := range w.DependsOn {
		ref, cross, err := depRef(dep)
		if err != nil {
			return err
		}
		if cross {
			// A reference names another orchestrator's item, or this node's by
			// its orchestrator (D3208). Whether the far end exists is not a
			// shape: a node sees only its own items, so the tick's reading is
			// what resolves one, and a reference nothing resolves holds the
			// item rather than being refused here. What is a shape is the item
			// depending on itself, which no reader could ever start.
			if ref.Orchestrator == s.AgentID && namesItself(ref.Item, w) {
				return fmt.Errorf("%w: %q depends on itself", ErrInvalid, w.ID)
			}
			if slices.Contains(w.DependsOn[:i], dep) {
				return fmt.Errorf("%w: %q depends on %q twice", ErrInvalid, w.ID, safetext.OneLine(dep))
			}
			continue
		}
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

// depRef reads one `depends_on` entry: the reference to an item it is, and
// whether that item is named from outside this node's own state (D3208).
//
// A work item's id carries no `/` anywhere (idRE), so the separator is the
// whole parse. An entry with none names an item of the state the document
// carries and is resolved there, which is F44 unchanged: a dependency never
// crosses the shards of one orchestrator by a bare name. An entry with a `/`
// is `<orchestrator>/<item>` or it is refused — a plan task's own `/`
// included, since the reference's item half may be the `<plan>/<task>` an item
// stands for.
func depRef(dep string) (ItemRef, bool, error) {
	if !strings.Contains(dep, "/") {
		if !idRE.MatchString(dep) {
			return ItemRef{}, false, fmt.Errorf("%w: the dependency %q is not a work item id", ErrInvalid, safetext.OneLine(dep))
		}
		return ItemRef{}, false, nil
	}
	ref, err := ParseItemRef(dep)
	if err != nil {
		return ItemRef{}, false, err
	}
	return ref, true, nil
}

// namesItself reports whether an item named from outside by name is w: the
// item's own id, or the plan task it stands for — the two identities one item
// is named by (validItem), either of which a reference may spell.
func namesItself(name string, w WorkItem) bool {
	return name == w.ID || (w.PlanTask != "" && name == w.PlanTask)
}

// validateEscalation is the ladder's ledger, as a document this release reads
// back. The rung, the reason and the head are values of closed vocabularies,
// so a ledger written by a later release — a rung this one has never heard of
// — is refused rather than read as the nearest thing to it, and the chain is
// what makes the ledger one thing rather than two: each transition leaves the
// rung the one before it entered, the last one is the rung the item is at, and
// its time is the time the item entered it. A block whose halves could
// disagree would let a reader draw a rung the transitions never reached.
//
// The two names the block carries for a reader that may read it and nothing
// above it — the item's repository and the run the move was about (E12, F27) —
// are held to the shapes those names have everywhere else, which is what keeps
// G8's "every string is a name of a known shape" true of this block too.
//
// The bounds are the ledger's own (MaxEscalationTransitions, MaxOuterAttempts)
// on top of the node's size target: the transitions are append-only, so
// nothing else stops an item the ladder keeps acting on from growing a node
// past the target every tick.
func validateEscalation(itemID string, e Escalation) error {
	if !escalation.KnownRung(e.Rung) {
		return fmt.Errorf("%w: the escalation of %q is at rung %q, which is not a rung", ErrInvalid, safetext.OneLine(itemID), safetext.OneLine(string(e.Rung)))
	}
	if e.EnteredAt.IsZero() {
		return fmt.Errorf("%w: the escalation of %q carries no time it entered its rung", ErrInvalid, safetext.OneLine(itemID))
	}
	if e.Repo != "" {
		if _, err := task.CanonicalRepo(e.Repo); err != nil {
			return fmt.Errorf("%w: the escalation of %q names the repository %q, which is not owner/name", ErrInvalid, safetext.OneLine(itemID), safetext.OneLine(e.Repo))
		}
	}
	if e.RunID != "" && !task.ValidRunID(e.RunID) {
		return fmt.Errorf("%w: the escalation of %q names the run %q, which is not a run id", ErrInvalid, safetext.OneLine(itemID), safetext.OneLine(e.RunID))
	}
	if e.OuterAttempts < 0 || e.OuterAttempts > MaxOuterAttempts {
		return fmt.Errorf("%w: the escalation of %q counts %d ladder runs, and at most %d may be spent", ErrTooLarge, safetext.OneLine(itemID), e.OuterAttempts, MaxOuterAttempts)
	}
	if len(e.Transitions) == 0 {
		return fmt.Errorf("%w: the escalation of %q carries no transition, and the rung it is at is what one records", ErrInvalid, safetext.OneLine(itemID))
	}
	if len(e.Transitions) > MaxEscalationTransitions {
		return fmt.Errorf("%w: the escalation of %q carries %d transitions, at most %d", ErrTooLarge, safetext.OneLine(itemID), len(e.Transitions), MaxEscalationTransitions)
	}
	previous := escalation.Rung("")
	for i, tr := range e.Transitions {
		if tr.From != previous {
			return fmt.Errorf("%w: the transition %d of %q leaves %q and the one before it entered %q", ErrInvalid, i+1, safetext.OneLine(itemID), safetext.OneLine(string(tr.From)), safetext.OneLine(string(previous)))
		}
		if !escalation.KnownRung(tr.To) {
			return fmt.Errorf("%w: the transition %d of %q enters rung %q, which is not a rung", ErrInvalid, i+1, safetext.OneLine(itemID), safetext.OneLine(string(tr.To)))
		}
		if !escalation.KnownReason(tr.Reason) {
			return fmt.Errorf("%w: the transition %d of %q is %q, which is not a reason this release has", ErrInvalid, i+1, safetext.OneLine(itemID), safetext.OneLine(string(tr.Reason)))
		}
		if tr.At.IsZero() {
			return fmt.Errorf("%w: the transition %d of %q carries no time", ErrInvalid, i+1, safetext.OneLine(itemID))
		}
		if tr.HeadSHA != "" && !headRE.MatchString(tr.HeadSHA) {
			return fmt.Errorf("%w: the transition %d of %q names the head %q, which is not a git object name", ErrInvalid, i+1, safetext.OneLine(itemID), safetext.OneLine(tr.HeadSHA))
		}
		previous = tr.To
	}
	last := e.Transitions[len(e.Transitions)-1]
	if last.To != e.Rung {
		return fmt.Errorf("%w: the escalation of %q is at %s and its last transition entered %s", ErrInvalid, safetext.OneLine(itemID), e.Rung, last.To)
	}
	if !last.At.Equal(e.EnteredAt) {
		return fmt.Errorf("%w: the escalation of %q entered %s at %s and its last transition is from %s", ErrInvalid, safetext.OneLine(itemID), e.Rung, e.EnteredAt.UTC().Format(time.RFC3339), last.At.UTC().Format(time.RFC3339))
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
	OpPlanApprove:     "plan_approve",
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
		{"plan_approve", op.PlanApprove != nil},
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
	case OpPlanApprove:
		if _, ok := planApproveNode(op.PlanApprove.Node); !ok {
			return fmt.Errorf("%w: the plan %q is not a plan folder or the backlog", ErrInvalid, safetext.OneLine(op.PlanApprove.Node))
		}
		if len(op.PlanApprove.Items) == 0 {
			return fmt.Errorf("%w: a plan approval carries the items it approves", ErrInvalid)
		}
		if len(op.PlanApprove.Items) > MaxPlanApproveItems {
			return fmt.Errorf("%w: %d items, at most %d", ErrTooLarge, len(op.PlanApprove.Items), MaxPlanApproveItems)
		}
		for _, it := range op.PlanApprove.Items {
			if !idRE.MatchString(it.ID) {
				return fmt.Errorf("%w: the approved item id %q is not a name", ErrInvalid, safetext.OneLine(it.ID))
			}
			if !DigestKnown(it.Digest) {
				return fmt.Errorf("%w: an approval carries the digest of the spec the person read, and %q carries none", ErrInvalid, safetext.OneLine(it.ID))
			}
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
	if op.Spec != nil {
		if _, ok := specName(*op.Spec); !ok {
			return fmt.Errorf("%w: the spec %q is not a plan folder name", ErrInvalid, safetext.OneLine(*op.Spec))
		}
	}
	if op.External != nil && !externalRE.MatchString(*op.External) {
		return fmt.Errorf("%w: the external id %q is not github:<owner>/<repo>#<n>", ErrInvalid, safetext.OneLine(*op.External))
	}
	if op.BodySHA256 != nil && !bodySHA256RE.MatchString(*op.BodySHA256) {
		return fmt.Errorf("%w: the body hash is not a SHA-256 in lower-case hex", ErrInvalid)
	}
	if op.PlanTask != nil {
		if p := task.PlanTaskProblem(*op.PlanTask); p != "" {
			return fmt.Errorf("%w: the plan task %s", ErrInvalid, p)
		}
	}
	if op.Phase != nil && !op.Phase.Known() {
		return fmt.Errorf("%w: %q is not a phase", ErrInvalid, safetext.OneLine(string(*op.Phase)))
	}
	if op.DependsOn != nil {
		if len(*op.DependsOn) > MaxDependsOn {
			return fmt.Errorf("%w: %d dependencies, at most %d", ErrTooLarge, len(*op.DependsOn), MaxDependsOn)
		}
		for _, dep := range *op.DependsOn {
			// The shape of one entry is the validator's own rule (depRef): a
			// work item id of this node, or a reference to another
			// orchestrator's item (D3208). Whether the reference resolves is
			// the tick's reading, so it is not asked here — and neither is
			// whether it names the item itself, which needs the item's plan
			// task and is asked of the state the patch lands on (validateItem).
			_, cross, err := depRef(dep)
			if err != nil {
				return err
			}
			if !cross && dep == op.ID {
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
	if op.Escalation != nil {
		// The same check the state gets, asked of the patch: a ledger the
		// validator would refuse is refused at the decode, before a daemon has
		// read a node it would land on. Which class may send it, and whether it
		// is an append to the item's own ledger, are Apply's and not a shape.
		if err := validateEscalation(op.ID, *op.Escalation); err != nil {
			return err
		}
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
