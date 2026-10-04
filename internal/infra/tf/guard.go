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
// allowed. A change with no actions, or one it doesn't know, is refused. The error lists every refused address with its actions.
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

// Beyond lists what a plan does that the run's one confirmation never covers
// (init's review screen): a delete or a replace of any resource, an action it
// does not know, and a removal from an IAM grant (an authoritative binding
// that loses a member, or one whose members cannot be compared, and any change
// to a whole IAM policy or audit config). Creating, importing and updating in
// place are what it covers; "forget" destroys nothing. The deletes are also
// Guard's, which stays in front of it: this is for a plan Guard let through
// with --allow-delete, and for the IAM removals Guard has no word on.
func Beyond(p *Plan) []string {
	if p == nil {
		return []string{"no plan"}
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if len(acts) == 0 {
			out = append(out, rc.Address+" (no actions)")
			continue
		}
		if unknown := slices.IndexFunc(acts, func(a string) bool { return !knownActions[a] }); unknown >= 0 {
			out = append(out, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(acts, ", ")))
			continue
		}
		switch {
		case slices.Contains(acts, "delete"):
			out = append(out, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(acts, ", ")))
		case !slices.Contains(acts, "update"):
		case strings.HasSuffix(rc.Type, "_iam_policy") || strings.HasSuffix(rc.Type, "_iam_audit_config"):
			out = append(out, rc.Address+" (rewrites a whole IAM policy)")
		case strings.HasSuffix(rc.Type, "_iam_binding") && removesMembers(rc.Change):
			out = append(out, rc.Address+" (removes IAM members)")
		}
	}
	return out
}

// removesMembers is true unless the binding's members after are known and
// include every member before, with the same role.
func removesMembers(c Change) bool {
	if c.Before["role"] != c.After["role"] {
		return true
	}
	if m, ok := c.AfterUnknown.(map[string]any); ok && m["members"] == true {
		return true
	}
	before, ok1 := c.Before["members"].([]any)
	after, ok2 := c.After["members"].([]any)
	if !ok1 || !ok2 {
		return true
	}
	for _, b := range before {
		if !slices.Contains(after, b) {
			return true
		}
	}
	return false
}
