package tf

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// knownActions are the plan actions terraform reports. Any other one is
// refused, so an action a later terraform adds can't slip past the guard.
var knownActions = map[string]bool{
	"no-op": true, "create": true, "read": true, "update": true, "delete": true, "forget": true,
}

// Guard refuses a plan that would delete any resource, a replace included
// (["delete","create"] or ["create","delete"]), unless its exact address is
// in allowDelete — and, even then, unless the object it removes is that
// address's own: a delete or replace moved there from another address (a
// moved block) is refused whatever the list says (removesUnderAnotherAddress).
// forget, from a removed block, destroys nothing and is allowed. A change with
// no actions, or one it doesn't know, is refused. The error lists every
// refused address with its actions.
func Guard(p *Plan, allowDelete []string) error {
	if p == nil {
		return errors.New("guard: no plan")
	}
	var refused []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if len(acts) == 0 {
			refused = append(refused, rc.Address+" (no actions)")
			continue
		}
		if unknown := slices.IndexFunc(acts, func(a string) bool { return !knownActions[a] }); unknown >= 0 {
			refused = append(refused, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(acts, ", ")))
			continue
		}
		if !slices.Contains(acts, "delete") {
			continue
		}
		switch {
		case !slices.Contains(allowDelete, rc.Address):
			refused = append(refused, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(acts, ", ")))
		case removesUnderAnotherAddress(rc):
			// A listed address names what may be removed at that address,
			// never an object moved there from another one.
			refused = append(refused, fmt.Sprintf("%s (%s): %s", rc.Address, strings.Join(acts, ", "), movedRemovalReason(rc)))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("the plan would delete or replace %d resource(s), or do something the guard doesn't know; nothing was applied. Name an address with --allow-delete only if deleting it is intended:\n  %s",
		len(refused), strings.Join(refused, "\n  "))
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
	return "the object it removes was moved here from " + from + " (" + orNone(rc.ActionReason) + "), and the delete list names addresses, not moved objects"
}

func orNone(s string) string {
	if s == "" {
		return "no action_reason"
	}
	return s
}
