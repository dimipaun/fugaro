// Package managed is the managed-project apply path: the gate a plan written
// in a managed project passes before anything is applied (the plan's Task
// 1.0), and the steps that follow it.
//
// The gate is deterministic, and it is the whole of the decision. tf.Guard
// refuses a plan that would delete or replace anything the owner's own list
// does not name; the managed instantiation of tf.Cover refuses a plan outside
// the resource types, the grants, the roles and the attributes a managed
// project admits (design D61) — and, for a delete or a replace, it refuses the
// resource the change removes or creates unless that resource is one the
// allowlist admits, so an address on the owner's list never admits a resource
// nothing judged (D72/D74 — design rows, not a task number: the plan carries
// no task of its own for this rule). The model's review is not part of it: it
// is advisory, recorded, and can only ever add a refusal (D60).
package managed

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The two gates, as a refusal records which half of the gate refused it.
const (
	guardGate = "guard"
	coverGate = "cover"
	// costGate is the cost list's (Task 1.3): the blocked attributes a plan
	// states, which tf.ManagedCostRefusals reads. The verdict is where they
	// are enforced, and nothing else consumes them: Task 3.1's apply reaches
	// them only through Gate.
	costGate = "cost"
)

// Cover is the scope-and-attribute gate a plan in a managed project runs
// behind: the managed-project instantiation of tf.Cover, whose own fields are
// the GCP projects the plan's resources may live in, the IAM members the
// review listed, the region, the buckets, the registry and the billing account
// the review showed (internal/infra/tf/cover.go:127-154). Gate does not
// restate them, and that is the point: the cover a managed apply hands Gate is
// the instantiation, so Task 1.1's managed allowlist replaces the
// installation's without this file changing. tf.Cover satisfies this interface
// today, and the tests below use it for the paths that need no managed list.
//
// A nil Cover refuses every plan (see Spec.Cover): a gate whose second half
// was left out must not admit.
type Cover interface {
	NotCovered(p *tf.Plan, imports []tf.ImportKey) []string
}

// Spec is the managed project a plan is judged for, and what the gate judges
// it with.
//
// The region, the buckets, the registry and the billing account the review
// showed — and the GCP projects the plan's resources may live in — are the
// Cover's own inputs, because they are tf.Cover's (cover.go:127-154); the
// Spec carries the cover rather than restating them. Project and GCPProject
// are the managed project's own names, and Gate uses them to say which
// project's gate it was when Gate itself refuses or cannot read the plan.
type Spec struct {
	// Project is the managed Fugaro project's name, GCPProject its GCP
	// project ID.
	Project, GCPProject string
	// Cover is the scope-and-attribute gate of that project. A nil Cover
	// refuses every plan: a managed plan is admitted by the managed cover, or
	// by nothing.
	Cover Cover
	// AllowDelete are the addresses the owner approved a delete or a replace
	// for. They come from the owner-written list at the root of the project's
	// runs bucket, read fail-closed by the caller (infra/managed.AllowDelete),
	// and never from the applying command's own arguments: in a managed
	// project an address the applying session passed is refused rather than
	// honoured, whatever the owner's list says (D72/D74, and
	// infra/managed.GuardAllowList is where that is enforced). An empty list
	// allows no delete and no replace at all.
	//
	// Gate hands the same list to both halves of the gate (tf.Guard and the
	// cover, which is told it through tf.Deletable), so the two cannot be given
	// different ones, and it is one half of the decision rather than the whole
	// of it: the resource is judged whatever its address is on this list
	// (D72/D74).
	AllowDelete []string
	// Imports are the import blocks the root's discovery wrote: an import is
	// admitted only when its address and ID are exactly one of them
	// (tf.Cover.NotCovered's own rule), so a stale imports.tf.json or a hand
	// edit in the workdir never adopts something discovery did not check.
	Imports []tf.ImportKey
	// TF is the terraform the saved plan is read with (tf.TF.Show). A nil TF
	// reads nothing, and a plan that cannot be read is never applied.
	TF *tf.TF
}

// Verdict is what the gate decided about one plan. OK is true only when the
// guard, the cover and the cost list admitted every change; Refusals carries
// every refusal from them, the guard's first, the cost list's last, each with the gate that made it, the address and the
// rule's own words for why.
//
// A Verdict is not bound to the plan's bytes: it says nothing about which file
// it judged. Task 3.1 must hash the saved plan at gate time and re-check that
// hash at apply, or a plan swapped in between is applied on this verdict.
//
// There is no "apply anyway", here or in the caller: the only action a false
// verdict permits is to fall back to a confirmation, which in the session a
// managed apply runs in means the step does not proceed.
type Verdict struct {
	OK       bool
	Refusals []Refusal
	// Flags are the findings of entries that only flag (Task 1.3, D62): recorded
	// here for whoever confirms, from the cost list's non-blocking findings. They
	// never affect OK.
	Flags []Refusal
	// Changes counts what the plan the verdict is about changes, for the
	// question a person is asked before the apply (Request.Ask). It never
	// affects OK.
	Changes Changes
	// Listable is the addresses the guard refused that the owner's list would
	// admit if it named them (tf.Listable): an unlisted delete or replace of
	// the address's own object. It is empty when the guard refused only what
	// no list clears — a moved object, an action it does not know, a change
	// with no actions — and then a refusal must not point at the owner's list
	// (Request.advice). It never affects OK.
	Listable []string
}

// Changes counts a plan's managed resource changes by kind: a replace is one
// change (delete and create of one address), and an import is counted beside
// whatever else the change does.
type Changes struct {
	Create, Update, Replace, Delete, Import int
}

// countChanges is the Changes of a plan.
func countChanges(p *tf.Plan) Changes {
	var c Changes
	for _, rc := range p.ResourceChanges {
		if rc.Mode != "managed" {
			continue
		}
		if rc.Change.Importing != nil {
			c.Import++
		}
		acts := rc.Change.Actions
		switch {
		case slices.Contains(acts, "delete") && slices.Contains(acts, "create"):
			c.Replace++
		case slices.Contains(acts, "create"):
			c.Create++
		case slices.Contains(acts, "delete"):
			c.Delete++
		case slices.Contains(acts, "update"):
			c.Update++
		}
	}
	return c
}

// costFindings is tf.ManagedCosts; a variable only so a test can supply a
// flag-only finding, which no entry of the real list produces.
var costFindings = tf.ManagedCosts

// guard is tf.GuardWith with no advice of its own; a variable only so a test
// can make it refuse in words the real guard never writes.
//
// No advice, because the guard's own (tf.AllowDeleteAdvice) names the flag a
// managed apply refuses, and the sentence a managed refusal ends with is the
// apply's (Request.advice, from Verdict.Listable), not the guard's: a header
// carrying it would survive into the verdict whenever no line of the refusal
// is shaped like an address (a moved object's line is not), and tell the
// reader to pass a flag that is refused (Task 3.2's review, finding 1).
var guard = func(p *tf.Plan, allowDelete []string) error { return tf.GuardWith(p, allowDelete, "") }

// Refusal is one refusal of one address: the gate that made it, the address it
// is about, and the rule's own words for why — the actions the guard would
// take ("delete", "delete, create") or the cover's reason. Address is empty
// for a refusal that is about the plan rather than about one address, which is
// how the guard refuses a plan it cannot read at all.
type Refusal struct {
	Gate    string
	Address string
	Why     string
}

// Gate is a managed apply's first step: it reads the saved plan at planPath
// and judges it with both deterministic gates, the guard first and the cover
// second, in that order — what a plan would delete or replace is decided
// before anything else is asked about it.
//
// Every check always runs, so the verdict carries every refusal rather than
// the first: the guard's list of refused addresses, the cover's list of refused
// changes and the cost list's blocked attributes (Task 1.3, owned here: the
// verdict is the only place a block can bite). OK is false when any of them
// refused anything, and nothing here
// applies anything — Gate reads a plan and returns, and applying the saved
// plan is a separate step with its own caller (Tasks 3.1 and 3.2).
//
// A plan that cannot be read is an error, never a verdict: the caller must not
// read "no verdict" as "admitted", and a returned Verdict is zero-valued
// (OK false) in that case as well.
func Gate(ctx context.Context, planPath string, spec Spec) (Verdict, error) {
	plan, err := spec.read(ctx, planPath)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Changes: countChanges(plan)}
	guardErr := guard(plan, spec.AllowDelete)
	if guardErr != nil {
		v.Refusals = append(v.Refusals, refusals(guardGate, guardLines(guardErr.Error()))...)
		v.Listable = tf.Listable(plan, spec.AllowDelete)
	}
	notCovered := spec.notCovered(plan)
	v.Refusals = append(v.Refusals, refusals(coverGate, notCovered)...)
	// The cost list is a third check on the same plan, run whatever the cover
	// said: its refusals are the plan's own words about what it states, and a
	// cover that admits a plan says nothing about what it costs.
	var costRefused, costFlagged []string
	for _, f := range costFindings(plan) {
		if f.Block {
			costRefused = append(costRefused, f.String())
		} else {
			costFlagged = append(costFlagged, f.String())
		}
	}
	v.Refusals = append(v.Refusals, refusals(costGate, costRefused)...)
	v.Flags = refusals(costGate, costFlagged)
	// Fail closed on the errors themselves, not on the refusals they were
	// turned into: a guard that errs with an empty message refuses all the same.
	v.OK = guardErr == nil && len(notCovered) == 0 && len(costRefused) == 0
	return v, nil
}

// read is the saved plan, or why there is none: a gate that read nothing has
// nothing to admit.
func (s Spec) read(ctx context.Context, planPath string) (*tf.Plan, error) {
	if s.TF == nil {
		return nil, errors.New("no terraform to read the saved plan with")
	}
	if planPath == "" {
		return nil, errors.New("no saved plan file to read")
	}
	p, err := s.TF.Show(ctx, planPath)
	if err != nil {
		return nil, fmt.Errorf("reading the saved plan %s of %s: %w", safetext.Field(planPath), s.what(), safetext.FieldErr(err))
	}
	return p, nil
}

// notCovered is the scope-and-attribute gate's own list, and a refusal when
// there is no cover at all: a managed plan is admitted by the managed cover of
// the project's own allowlist, or by nothing.
//
// The owner's delete list goes to the cover beside the plan (D72/D74,
// tf.Deletable): the list Gate hands tf.Guard is handed to the cover too, so
// the two halves of the gate cannot be given different ones — a delete admitted
// by the list one of them was told and not the other's is exactly the plan this
// rule exists to refuse. A cover that cannot be told it judges no delete, so
// nothing but the guard stands between a listed address and an unread resource:
// unjudgedDeletes refuses that case rather than letting the address alone admit
// it.
func (s Spec) notCovered(p *tf.Plan) []string {
	if s.Cover == nil {
		return []string{"no cover: a managed plan is admitted by the managed cover of " + s.what() + ", or by nothing"}
	}
	if d, ok := s.Cover.(tf.Deletable); ok {
		return d.WithDeleteList(s.AllowDelete).NotCovered(p, s.Imports)
	}
	return append(s.Cover.NotCovered(p, s.Imports), s.unjudgedDeletes(p)...)
}

// unjudgedDeletes lists every delete or replace of an address on the owner's
// list, for the cover that cannot be told the list and so cannot judge the
// resource: the address is the one thing that is not a judgement of it, and a
// cover that admits every delete would otherwise have a listed one admitted
// with nothing reading the resource at all. Fail closed, and named: a delete
// the owner approved still needs the resource it removes to be one a managed
// plan may carry.
func (s Spec) unjudgedDeletes(p *tf.Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if slices.Contains(rc.Change.Actions, "delete") && slices.Contains(s.AllowDelete, rc.Address) {
			out = append(out, safetext.Field(rc.Address)+" (a delete or a replace of an address on the owner's delete list, and the cover this gate was handed cannot be told the list, so the resource is not judged: the address alone never admits one)")
		}
	}
	return out
}

// what names the project in the refusals and errors Gate writes itself. Both
// halves are bounded (safetext.Field): they reach this Spec from a marker read
// and from a configuration, and this is the text of a refusal a person reads.
func (s Spec) what() string {
	switch {
	case s.Project != "" && s.GCPProject != "":
		return safetext.Field(s.Project) + " (" + safetext.Field(s.GCPProject) + ")"
	case s.Project != "":
		return safetext.Field(s.Project)
	case s.GCPProject != "":
		return safetext.Field(s.GCPProject)
	}
	return "this project"
}

// guardLines is the guard's refusal as the list refusals reads: one item per
// line, without the guard's own header when it has one. The header is the
// first line of a refusal that lists addresses under it ("the plan would delete
// or replace N resource(s) ...:"), and the verdict carries the fact per
// address rather than the guard's summary of it; a one-line refusal (a plan it
// could not read at all) is carried whole.
func guardLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 1 {
		return lines[1:]
	}
	return lines
}

// refusals turns a gate's own list into the verdict's: one Refusal per item.
// An item of the shape both gates write a refused change in, "<address>
// (<why>)" — the guard with the plan actions it would take, the cover with the
// rule that refused it — is carried as its address and its why, and so is the
// guard's moved-object line, "<address> (<actions>): <why>". Any other item (a
// gate that refused without listing an address, a plan it could not read at
// all) is carried whole rather than dropped: a refusal nobody can read is still
// a refusal.
//
// Each item is one refusal however many lines it holds, and each part of it is
// one bounded field (safetext.Field): an address and a rule's own words are read
// out of a plan and out of objects other people wrote, and neither may add a
// line, a control or a bidi override to the refusal a person reads.
func refusals(gate string, items []string) []Refusal {
	var out []Refusal
	for _, item := range items {
		item = strings.TrimSpace(item)
		if strings.TrimSpace(safetext.Field(item)) == "" {
			continue
		}
		// The first " (" is the one the gates write: a Terraform address holds
		// no parenthesis, while a rule's own words may.
		if i := strings.Index(item, " ("); i >= 0 {
			addr := safetext.Field(item[:i])
			if strings.HasSuffix(item, ")") {
				out = append(out, Refusal{Gate: gate, Address: addr, Why: safetext.Field(item[i+2 : len(item)-1])})
				continue
			}
			if j := strings.Index(item[i:], "): "); j >= 0 {
				out = append(out, Refusal{Gate: gate, Address: addr, Why: safetext.Field(item[i+2:i+j] + ": " + item[i+j+3:])})
				continue
			}
		}
		out = append(out, Refusal{Gate: gate, Why: safetext.Field(item)})
	}
	return out
}
