package infra

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// This file is one decision about the second kind of removal a repository
// root's ordinary plan can carry, beside the retirements and the flips: a
// grant fugaro itself wrote, which this release RE-POINTS. The container a
// job's secret accessor names is ForceNew, and so are the name and the region
// of the invoker grant on the credentials service, so an installation that
// resolves a secret to the project's container rather than the repository's —
// or that applies a repository root after the service moved — plans
// ["delete","create"] at an address the plan still declares on both sides.
//
// Why it needs naming at all: the guard refuses every change that carries a
// delete, a replace included, unless its address is on the list the caller
// holds. The retirement's own list (RetireAllowDelete) is bounded to the
// addresses this spec no longer produces, so a replace at an address it still
// produces can never be on it — and a managed project's refusal names no flag
// and no command. A plain `fugaro update` on such a project then stopped at a
// refusal with nothing to do next, which is the defect this file answers: the
// owner's run of 2026-11-27 read "Next command: fugaro update --repo .
// --retire", and --retire refused the same addresses, because they are not
// retirements at all.
//
// Why admitting them is not a widening of the fence: a replace destroys
// nothing. The create is in the same plan at the same address, so the object
// the run holds at the end is the object it held at the start, rebound to
// where the update moved it — and a binding holds nothing of its own, which is
// RetirableTypes' own reason for calling every grant retirable. Only grants are
// read here, only a replace (a pure delete at the address is refused whatever
// either half says), and only when the plan's own two halves still agree on
// what the grant IS: the same principal bound to the same role. A change that
// moves either is a different binding at that address and is judged exactly as
// it was, and so is every change at an address of any other type.
//
// It is LauncherGrantUpgrade's shape (internal/infra/hardening.go) and its
// reasoning: the caller read both halves of each change before naming it, so
// the list names exactly one change of this plan rather than "whatever is at
// this address" — which is what a caller handing this to the guard without
// having read them would be doing, and the reason the list is computed here
// and not taken from a command line.
//
// The credential families it was written for are the two the owner's run
// refused: the job's own accessor on the App key's container
// (modules/workflow/sa.tf's google_secret_manager_secret_iam_member.job, whose
// secret_id is the merged map's answer for the name) and the job account's
// invoker grant on the credentials service
// (google_cloud_run_v2_service_iam_member.credentials). It is stated over
// every grant type the retirement rules already call derived, so the next
// grant a release re-points is admitted by the rule rather than by an edit.

// RepointedGrantAllowDelete is the guard's allow-list for p when one of this
// program's own updates re-points a grant it wrote: each replace of a grant
// address — ["delete","create"] or ["create","delete"], and nothing else —
// whose before and after carry the same principal and the same role. Anything
// else is never allowed and the guard refuses it as before: a pure delete, a
// change at a type that is no grant, a replace that moves the principal or the
// role, a change with no before to read, and every removal a moved block
// carried here from another address (which the guard refuses whatever a list
// says).
func RepointedGrantAllowDelete(p *tf.Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if !isGrantType(rc.Type) || !isReplace(rc.Change.Actions) {
			continue
		}
		if !sameBinding(rc.Change.Before, rc.Change.After) {
			continue
		}
		out = append(out, rc.Address)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// isGrantType is whether this program's retirement rules already call a type
// of t a derived one AND it is a binding rather than the thing it is on: the
// two tables in tf/retire.go classify every type the modules declare, and the
// suffix is what leaves the service accounts, the jobs and the schedules to
// their own decisions. A type neither table carries is no grant here, which is
// the same fail-closed direction RetirableType answers with.
func isGrantType(t string) bool {
	if !strings.HasSuffix(t, "_iam_member") {
		return false
	}
	retirable, _ := tf.RetirableType(t)
	return retirable
}

// isReplace is a change terraform plans as a destroy and a create at one
// address, in either order, and nothing else. A delete alone is not one: only
// the create in the same change is what makes this removal safe to admit.
func isReplace(actions []string) bool {
	return slices.Equal(actions, []string{"delete", "create"}) ||
		slices.Equal(actions, []string{"create", "delete"})
}

// bindingKeys are the two attributes that ARE the grant: the principal it
// binds and the role it binds them to.
var bindingKeys = []string{"member", "role"}

// sameBinding is whether both halves of a replace carry the same principal
// bound to the same role, and a non-empty one on each side. A half that names
// neither — a before view terraform redacted, a resource type whose binding is
// spelled another way — is not a comparison this rule can make, so the change
// is refused rather than admitted on a reading of nothing.
func sameBinding(before, after map[string]any) bool {
	for _, k := range bindingKeys {
		b, bok := before[k].(string)
		a, aok := after[k].(string)
		if !bok || !aok || b == "" || b != a {
			return false
		}
	}
	return true
}
