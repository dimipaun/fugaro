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
	if p == nil {
		return errors.New("guard: no plan")
	}
	var refused []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		// The address and the actions are the plan's words, and a plan is
		// read out of a root someone else may have written: each is one
		// bounded field (safetext.Field) wherever it is printed, so neither
		// can add a line, a control or a bidi override to this refusal. An
		// ordinary address and action are unchanged by it.
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
		switch {
		case !slices.Contains(allowDelete, rc.Address):
			refused = append(refused, fmt.Sprintf("%s (%s)", addr, said))
		case removesUnderAnotherAddress(rc):
			// A listed address names what may be removed at that address,
			// never an object moved there from another one.
			refused = append(refused, fmt.Sprintf("%s (%s): %s", addr, said, movedRemovalReason(rc)))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	head := fmt.Sprintf("the plan would delete or replace %d resource(s), or do something the guard doesn't know; nothing was applied", len(refused))
	if advice == "" {
		return fmt.Errorf("%s:\n  %s", head, strings.Join(refused, "\n  "))
	}
	return fmt.Errorf("%s. %s:\n  %s", head, advice, strings.Join(refused, "\n  "))
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
