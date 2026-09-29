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
// in allowDelete. forget, from a removed block, destroys nothing and is
// allowed. The error lists every refused address with its actions.
func Guard(p *Plan, allowDelete []string) error {
	if p == nil {
		return errors.New("guard: no plan")
	}
	var refused []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if unknown := slices.IndexFunc(acts, func(a string) bool { return !knownActions[a] }); unknown >= 0 {
			refused = append(refused, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(acts, ", ")))
			continue
		}
		if slices.Contains(acts, "delete") && !slices.Contains(allowDelete, rc.Address) {
			refused = append(refused, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(acts, ", ")))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("the plan would delete or replace %d resource(s), or do something the guard doesn't know; nothing was applied. Name an address with --allow-delete only if deleting it is intended:\n  %s",
		len(refused), strings.Join(refused, "\n  "))
}
