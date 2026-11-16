package tf

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// knownActions are the plan actions terraform reports. Any other one is
// refused, so an action a later terraform adds can't slip past the guard.
var knownActions = map[string]bool{
	"no-op": true, "create": true, "read": true, "update": true, "delete": true, "forget": true,
}

// ActionsText reports whether s is the text guardWith writes for a change's
// actions: a comma-joined list of the plan actions terraform reports, the same
// words knownActions holds joined the same way.
//
// It exists so a reader of a guard refusal can tell the two shapes that refusal
// is written in apart by the actions rather than by the item's last character:
// the guard writes "<address> (<actions>): <why>" and a cover writes
// "<address> (<why>)", and a why may itself end in ")" — the entries rule's
// does (tf.Approves) — so an item that ends in ")" is not always a cover's
// wrapper (internal/managed's refusals). The one list of action names stays
// here, so the two cannot drift about what an actions text is.
func ActionsText(s string) bool {
	if s == "" {
		return false
	}
	for _, a := range strings.Split(s, ", ") {
		if !knownActions[a] {
			return false
		}
	}
	return true
}

// AllowDeleteAdvice is the sentence Guard's refusal ends with when the caller
// chose none: the advice for a command whose own --allow-delete is a flag it
// accepts. It is today's text kept as a value rather than a line inside the
// format string, so that a caller for whom it is false passes its own sentence
// through GuardWith instead of adding a second refusal beside this one.
const AllowDeleteAdvice = "Name an address with --allow-delete only if deleting it is intended"

// Guard refuses a plan that would delete any resource, a replace included
// (["delete","create"] or ["create","delete"]), unless its exact address is
// in allowDelete — and, even then, unless the object it removes is that
// address's own: a delete or replace moved there from another address (a
// moved block) is refused whatever the list says (removesUnderAnotherAddress).
// forget, from a removed block, destroys nothing and is allowed. A change with
// no actions, or one it doesn't know, is refused. The error lists every
// refused address with its actions and ends with AllowDeleteAdvice.
func Guard(p *Plan, allowDelete []string) error {
	return GuardWith(p, allowDelete, AllowDeleteAdvice)
}

// GuardWith is Guard with the advice its refusal ends with chosen by the
// caller, because in one mode the advice above is false and dangerous: a
// managed project's apply takes no such flag of its own — the addresses come
// from the owner-written list the read is fail-closed on (D72/D74), and an
// address the applying command named is refused rather than honoured — so a
// refusal that told its reader to pass one would be an instruction the session
// it refuses would follow. The managed sentence is managed.DeleteAdvice's; an
// empty advice is no advice, and the refusal then ends at what it refused.
//
// Everything else is Guard's, and the plan's own words are the same in both
// modes: the advice is one sentence, and the sentences beside it are not
// advice but the reason.
func GuardWith(p *Plan, allowDelete []string, advice string) error {
	return guardWith(p, advice, func(rc ResourceChange) verdict {
		return verdict{ok: slices.Contains(allowDelete, rc.Address)}
	})
}

// GuardEntries is Guard over the owner's own delete list: an entries list
// holds, for each approved address, the identity of the object that was there
// when the owner approved it, and a removal is admitted only when the entry's
// identity is the identity of the object the plan removes (Approves) — so an
// address that is listed for one object is not an approval of another one that
// a later plan carries at it (design §6 item 5, plan Task 6.3).
//
// It is GuardWith for the managed apply's own list and nothing else: the
// addresses a project's owner approves, read from fugaro/allow-delete.json.
// Every other caller keeps Guard's address list, where the address alone is
// the approval — those are the sessions' own --allow-delete flags, given at a
// terminal about a root the person is looking at, and a plan's before view is
// not what they are approving.
//
// Everything else is Guard's: the same walk, the same threshold for what is a
// removal, the same refusal of a moved object whatever the list says, and the
// same items — the entry's own answer is added as the reason beside the
// actions, so the person reads what the plan removes.
func GuardEntries(p *Plan, entries []DeleteEntry, advice string) error {
	return guardWith(p, advice, func(rc ResourceChange) verdict {
		ok, why := Approves(entries, rc)
		return verdict{ok: ok, why: why}
	})
}

// GuardEntriesWithAddresses is GuardEntries with a second list of removals,
// named by address alone: the replaces one of this program's own upgrades makes
// of a grant an earlier release wrote, whose two halves the caller read and
// compared before naming them (infra.LauncherGrantUpgrade is the one caller).
//
// An address is enough here and nowhere else, and what makes it enough is the
// caller's own reading rather than anything this function can check: every
// other removal this program admits is one a person approved, and a person
// approves an OBJECT (an entry, GuardEntries) — while an upgrade's replaces are
// computed out of the plan itself, both sides of each change compared, so the
// address names exactly one change of that plan rather than "whatever is at
// this address". A caller that named an address it had not read would be
// handing this function the one thing it cannot judge, which is why the list is
// the caller's own and not a command line's.
//
// Everything else is GuardEntries': the entries are tried first, so an entry
// naming the address and holding another object's identity is still the refusal
// it is (an address list does not overrule the owner's own list), a removal
// moved here from another address is refused whatever either list says, and the
// advice is the caller's.
func GuardEntriesWithAddresses(p *Plan, entries []DeleteEntry, addresses []string, advice string) error {
	return guardWith(p, advice, func(rc ResourceChange) verdict {
		ok, why := Approves(entries, rc)
		if ok || why != "" {
			return verdict{ok: ok, why: why}
		}
		return verdict{ok: slices.Contains(addresses, rc.Address)}
	})
}

// verdict is what one removal's rule answers about a change that removes an
// object: whether the list the caller holds admits it (ok), the reason to print
// beside the change's actions when it does not (why, "" for the list's own "the
// address is not on it" — the item shape Guard has always written for that
// case), and whether nothing admitted it but it is TAKEN rather than refused
// (warn, which D1545 sets for a content removal the managed gate now records
// and warns about instead of stopping).
type verdict struct {
	ok   bool
	why  string
	warn bool
}

// judge is one removal's rule: what it answers about a change that removes an
// object. Guard, GuardWith, GuardEntries and GuardEntriesWithAddresses each
// build one over their own list, and GuardManaged builds the one D1545 and
// D1551 leave the managed apply.
type judge func(rc ResourceChange) verdict

// guardWith is the refusal Guard, GuardWith and GuardEntries share, so the
// three cannot drift about what a removal is, which removals no list clears, or
// how a refusal reads. It is guardWalk's refusals with the head and the advice
// wrapped around them, unchanged; a nil plan is the one refusal guardWalk does
// not wrap.
func guardWith(p *Plan, advice string, judges judge) error {
	if p == nil {
		return errors.New("guard: no plan")
	}
	refused, _ := guardWalk(p, judges)
	if len(refused) == 0 {
		return nil
	}
	head := fmt.Sprintf("the plan would delete or replace %d resource(s), or do something the guard doesn't know; nothing was applied", len(refused))
	if advice == "" {
		return fmt.Errorf("%s:\n  %s", head, strings.Join(refused, "\n  "))
	}
	return fmt.Errorf("%s. %s:\n  %s", head, advice, strings.Join(refused, "\n  "))
}

// guardWalk is the walk Guard, GuardWith and GuardEntries share, and the one
// GuardManaged reads for both of its answers: every removal the judges refuse,
// and — beside them — the ones a judge neither admits nor refuses but takes
// with a warning (verdict.warn, D1545). The refused items are the plan's own
// words in the shapes Guard has always written: "<addr> (<actions>)" for a
// removal no list names, and "<addr> (<actions>): <why>" when a rule gives a
// reason. A warned item is written in those same two shapes, so a reader parses
// it exactly as a refusal and only its half of the answer says which it is. A
// nil plan is one refusal, "guard: no plan", and no warnings.
func guardWalk(p *Plan, judges judge) (refused, warned []string) {
	if p == nil {
		return []string{"guard: no plan"}, nil
	}
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		// The address and the actions are the plan's words, and a plan is
		// read out of a root someone else may have written: each is one
		// bounded field (safetext.Field) wherever it is printed, so neither
		// can add a line, a control or a bidi override to this refusal. An
		// ordinary address and action are unchanged by it.
		//
		// The bound is safetext.FieldMax, and for the ADDRESS of an entry the
		// owner's delete list approves (tf.Approves) it is the one part of this
		// loop that is not paste-exact: an address longer than that is printed
		// cut, with Field's own marker, so the entry recorded from what the
		// refusal showed names nothing the plan carries and the delete stays
		// refused — the fail-closed direction, and the reason the identity half
		// of the same line is printed uncut instead (identityText) while this
		// half is not. A managed root is the owner's own root, so the depth of
		// addresses in it is theirs to write; the limit is recorded in
		// docs/design/managed-projects.md §6 item 5 beside the identity's own.
		addr, said := safetext.Field(rc.Address), safetext.Field(strings.Join(acts, ", "))
		if len(acts) == 0 {
			refused = append(refused, addr+" (no actions)")
			continue
		}
		if unknown := slices.IndexFunc(acts, func(a string) bool { return !knownActions[a] }); unknown >= 0 {
			refused = append(refused, fmt.Sprintf("%s (%s)", addr, said))
			continue
		}
		if !slices.Contains(acts, "delete") {
			continue
		}
		r := judges(rc)
		switch {
		case r.ok && removesUnderAnotherAddress(rc):
			// A listed address names what may be removed at that address,
			// never an object moved there from another one.
			refused = append(refused, fmt.Sprintf("%s (%s): %s", addr, said, movedRemovalReason(rc)))
		case r.ok:
		case r.warn && !removesUnderAnotherAddress(rc):
			// Nothing admits this removal, and it is a content one D1545 takes
			// rather than refuses — a delete or a replace of a classified,
			// non-data-bearing type (D1551): recorded and warned, not stopped.
			// It is written in the same two shapes a refusal is: the actions
			// alone when no list names the address, and the actions with the
			// delete rule's own sentence beside them when a list does name it
			// and its entry does not approve the object the plan removes (an
			// entry bound to another object, a before view that identifies
			// nothing). The sentence is the reader's — it is what the owner has
			// to change — and the cover writes it for the same removal too, so
			// the two halves of the gate say one thing about one change.
			item := fmt.Sprintf("%s (%s)", addr, said)
			if r.why != "" {
				item = fmt.Sprintf("%s (%s): %s", addr, said, r.why)
			}
			warned = append(warned, item)
		case r.why == "":
			// The list does not name this address: the item Guard has always
			// written, with the actions and nothing beside them.
			refused = append(refused, fmt.Sprintf("%s (%s)", addr, said))
		case removesUnderAnotherAddress(rc):
			// The list names this address, and the object the change removes
			// is not the one it was bound to — and it was moved here from
			// another address (or the plan says the move has no target). The
			// item about that change is the moved-object one, exactly as the
			// managed cover prints it (managed_cover.go), and not the list's
			// own sentence about the identity it holds: the two items are
			// about one change, and a reader who saw the cover's reason and
			// this one would be reading two rules for it. Approves' sentence
			// is still the refusal for a listed address whose entry was bound
			// to another object and whose removal was not moved (below).
			refused = append(refused, fmt.Sprintf("%s (%s): %s", addr, said, movedRemovalReason(rc)))
		default:
			refused = append(refused, fmt.Sprintf("%s (%s): %s", addr, said, r.why))
		}
	}
	return refused, warned
}

// GuardManaged is the managed apply's delete rule as D1545 and D1551 leave it:
// the removals it returns are the ones that still REFUSE — a data-bearing type,
// an unclassified type, an object moved here from another address, a change
// with no actions or an action the guard does not know — and the removals it
// returns as warnings are the ones the gate now takes: a delete or a replace of
// a classified, non-data-bearing type that neither the owner's delete list nor
// the caller's addresses admit. It is GuardEntriesWithAddresses' own rule — the
// owner's entries first (Approves), then the caller's addresses — with D1545's
// ruling on top: a removal neither admits is no longer refused for what the
// plan contains, so it is warned instead, while D1551 keeps the two directions
// that do refuse. A data-bearing type's objects are data a delete would destroy
// (reconciliation's table), and an unclassified type is one nobody has decided
// — a type added to allowedTypes later, which RefusesRemovalType refuses until
// somebody classifies it. warn is set only when neither list admits the
// removal, so an entry or an address that admits one still admits it whatever
// its type — and a list that NAMES the address without approving the object the
// plan removes (an entry bound to another object, a before view that identifies
// nothing) does not admit it either, which is why the delete rule's own sentence
// is a warning for a type D1551 leaves to D1545 and a refusal for a type it
// protects. The cover routes the same sentence the same way (managed_cover.go's
// addRemoval), so one removal is never refused by this half of the gate and
// warned about by the other.
//
// The warning is not silent, and that is the point of the two answers: a warned
// removal is printed beside the refusals and recorded (D1545's AuditRecord), so
// the cover tables stay a tripwire that tells the owner what left them rather
// than vanishing. The caller prints both lists and stops only for the refusals.
//
// A nil plan is one refusal, "guard: no plan", and no warnings — the same one
// line guardWith has always written, so a caller that carries it whole is
// unchanged.
func GuardManaged(p *Plan, entries []DeleteEntry, addresses []string) (refused, warned []string) {
	return guardWalk(p, func(rc ResourceChange) verdict {
		ok, why := Approves(entries, rc)
		if ok {
			return verdict{ok: true}
		}
		// why is the delete rule's own sentence — the list names this address
		// for another object, or the plan's before view identifies nothing —
		// and where it lands follows the type, exactly as it does in the cover
		// (managed_cover.go's addRemoval): a refusal for a type D1551 protects,
		// and one of D1545's warnings, the sentence beside the actions, for a
		// type it does not. Refusing it whatever the type is what put one
		// removal in the verdict's refusals AND in its warnings. The caller's
		// own addresses are read below and not before it, which is the order
		// this rule has always had: an entry that names the address is an
		// answer about the object the plan removes, and a list of addresses is
		// not one.
		if why != "" {
			if RefusesRemovalType(rc.Type) {
				return verdict{why: why}
			}
			return verdict{warn: true, why: why}
		}
		if slices.Contains(addresses, rc.Address) {
			return verdict{ok: true}
		}
		// Neither list names the address at all: the delete rule has no
		// sentence about the object, so the item is the bare actions of a
		// refusal — and D1545's warning, with no reason beside it, for a type
		// D1551 does not protect.
		return verdict{warn: !RefusesRemovalType(rc.Type)}
	})
}

// Listable is the addresses Guard refuses only because allowDelete does not
// name them: a delete or a replace, every action one the guard knows, of an
// object that is the address's own. Naming one of these is what admits it.
// Every other refusal — no actions, an action the guard does not know, an
// object moved here from another address (refused whatever the list says) —
// is not one any list clears, and a refusal made only of those must not tell
// its reader that naming an address would help: that advice is false, and an
// instruction to loop. The addresses are the plan's own, unbounded, for the
// caller to compare and not to print.
func Listable(p *Plan, allowDelete []string) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if !slices.Contains(acts, "delete") || slices.ContainsFunc(acts, func(a string) bool { return !knownActions[a] }) {
			continue
		}
		if slices.Contains(allowDelete, rc.Address) || removesUnderAnotherAddress(rc) {
			continue
		}
		out = append(out, rc.Address)
	}
	return out
}

// WithoutRemovals is the plan with the removal half of each change at addrs
// taken out: what a delete takes away, and what a replace takes away before it
// makes another. What the same change CREATES stays in, as the create it is,
// and that is the whole reason this exists — the cover refuses a change that
// carries a delete as one fact ("actions delete, create") and never reads the
// half that creates (tf.Cover.NotCovered), so a caller asking whether
// everything a plan does BESIDES the removals a list approves is judged has to
// ask about that create itself.
//
// addrs are the addresses a delete approval reaches (tf.Listable for a
// session's --allow-delete, infra.RetireAllowDelete for a retirement's own
// set), and a change at any other address is kept as the plan carries it. The
// result is a question asked of a plan a caller already holds and never a
// document to digest: it shares the changes it keeps.
func WithoutRemovals(p *Plan, addrs []string) *Plan {
	if p == nil {
		return nil
	}
	out := *p
	out.ResourceChanges = nil
	for _, rc := range p.ResourceChanges {
		if !slices.Contains(addrs, rc.Address) {
			out.ResourceChanges = append(out.ResourceChanges, rc)
			continue
		}
		if !slices.Contains(rc.Change.Actions, "create") {
			continue
		}
		rc.Change = Change{Actions: []string{"create"}, After: rc.Change.After,
			AfterUnknown: rc.Change.AfterUnknown, Importing: rc.Change.Importing}
		out.ResourceChanges = append(out.ResourceChanges, rc)
	}
	return &out
}

// ListableEntries is Listable for the owner's delete list: the addresses the
// guard refused that a new entry would admit — one naming the address AND the
// identity of the object this plan removes (Approves). An entry that names the
// address for another object is one of them: the removal is refused because
// the entry is about a different object, and an entry about this one clears it.
// What no entry clears is what Listable already excludes — a moved object, an
// action the guard does not know, a change with no actions — plus one case the
// entries rule adds: a removal whose before view identifies nothing, which no
// entry can be matched against (an entry always carries at least its type, so
// it cannot equal an identity with no parts). Approves refuses that one with
// its own sentence, and listing its address would tell the owner to approve
// something approving cannot clear.
//
// It exists so the advice a refusal ends with is still true under the entries
// rule: sending the owner to record an ENTRY at an address (managed.DeleteAdvice)
// leads somewhere only when a new entry would clear that address's removal, and
// an address alone is no longer an approval — so a removal no entry can be
// matched against is not one to offer.
func ListableEntries(p *Plan, entries []DeleteEntry) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if !slices.Contains(acts, "delete") || slices.ContainsFunc(acts, func(a string) bool { return !knownActions[a] }) {
			continue
		}
		if removesUnderAnotherAddress(rc) {
			continue
		}
		// A before view that identifies no object is a refusal no entry can
		// clear (see the doc above): the address is not one to offer.
		if !RemovedIdentity(rc).Identifies() {
			continue
		}
		if ok, _ := Approves(entries, rc); ok {
			continue
		}
		out = append(out, rc.Address)
	}
	return out
}

// removesUnderAnotherAddress: a change whose actions remove an object (a
// delete, or a replace in either order) is listed by its own address, but the
// object it removes is the one in before — and with a moved block that object
// was at previous_address. `moved { from = A, to = B }` with B in neither the
// state nor the configuration plans address=B, previous_address=A,
// actions=[delete], action_reason=delete_because_no_move_target, with A's
// object in before: an approval of B would delete A. So the delete rule refuses
// a removal whose previous_address is set and differs from its address, and
// one whose reason is delete_because_no_move_target on its own, so either
// signal alone refuses it. A deposed object of the address keeps its address
// and is the address's own; it is judged on its own before like any other.
func removesUnderAnotherAddress(rc ResourceChange) bool {
	return (rc.PreviousAddress != "" && rc.PreviousAddress != rc.Address) ||
		rc.ActionReason == "delete_because_no_move_target"
}

// movedRemovalReason is the refusal removesUnderAnotherAddress gives.
func movedRemovalReason(rc ResourceChange) string {
	from := rc.PreviousAddress
	if from == "" || from == rc.Address {
		from = "another address"
	}
	return "the object it removes was moved here from " + safetext.Field(from) + " (" + safetext.Field(orNone(rc.ActionReason)) + "), and the delete list names addresses, not moved objects"
}

func orNone(s string) string {
	if s == "" {
		return "no action_reason"
	}
	return s
}
