// Package managed is the managed-project apply path: the gate a plan written
// in a managed project passes before anything is applied (the plan's Task
// 1.0), and the steps that follow it.
//
// The gate is deterministic, and it is the whole of the decision. tf.Guard
// refuses a plan that would delete or replace anything the owner's own list
// does not name; the managed instantiation of tf.Cover refuses a plan outside
// the resource types, the grants, the roles and the attributes a managed
// project admits (design D61). The model's review is not part of it: it is
// advisory, recorded, and can only ever add a refusal (D60).
package managed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The two gates, as a refusal records which half of the gate refused it.
const (
	guardGate = "guard"
	coverGate = "cover"
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

// Verdict is what the gate decided about one plan. OK is true only when both
// gates admitted every change; Refusals carries every refusal from both gates,
// the guard's first, each with the gate that made it, the address and the
// rule's own words for why.
//
// There is no "apply anyway", here or in the caller: the only action a false
// verdict permits is to fall back to a confirmation, which in the session a
// managed apply runs in means the step does not proceed.
type Verdict struct {
	OK       bool
	Refusals []Refusal
}

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
// Both gates always run, so the verdict carries every refusal rather than the
// first: the guard's list of refused addresses and the cover's list of refused
// changes. OK is false when either gate refused anything, and nothing here
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
	var v Verdict
	if err := tf.Guard(plan, spec.AllowDelete); err != nil {
		v.Refusals = append(v.Refusals, refusals(guardGate, err.Error())...)
	}
	v.Refusals = append(v.Refusals, refusals(coverGate, strings.Join(spec.notCovered(plan), "\n"))...)
	v.OK = len(v.Refusals) == 0
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
		return nil, fmt.Errorf("reading the saved plan %s of %s: %w", planPath, s.what(), err)
	}
	return p, nil
}

// notCovered is the scope-and-attribute gate's own list, and a refusal when
// there is no cover at all: a managed plan is admitted by the managed cover of
// the project's own allowlist, or by nothing.
func (s Spec) notCovered(p *tf.Plan) []string {
	if s.Cover == nil {
		return []string{"no cover: a managed plan is admitted by the managed cover of " + s.what() + ", or by nothing"}
	}
	return s.Cover.NotCovered(p, s.Imports)
}

// what names the project in the refusals and errors Gate writes itself.
func (s Spec) what() string {
	switch {
	case s.Project != "" && s.GCPProject != "":
		return s.Project + " (" + s.GCPProject + ")"
	case s.Project != "":
		return s.Project
	case s.GCPProject != "":
		return s.GCPProject
	}
	return "this project"
}

// refusals turns a gate's own list into the verdict's: one Refusal per line of
// the shape both gates write a refused change in, "<address> (<why>)" — the
// guard with the plan actions it would take, the cover with the rule that
// refused it. A gate that refused without listing an address (a plan it could
// not read at all) is carried whole rather than dropped: a refusal nobody can
// read is still a refusal.
//
// The guard's own header is not carried, and that is deliberate: its advice is
// to "Name an address with --allow-delete", which in a managed project is the
// one thing the delete rule forbids — the addresses come from the owner's own
// list, and an address the applying session passed is refused (D74). The
// wording of the managed refusal is Task 3.2's; what the verdict carries here
// is the fact, per address.
func refusals(gate, text string) []Refusal {
	var shaped, rest []Refusal
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The first " (" is the one the gates write: a Terraform address holds
		// no parenthesis, while a rule's own words may.
		if i := strings.Index(line, " ("); i >= 0 && strings.HasSuffix(line, ")") {
			shaped = append(shaped, Refusal{Gate: gate, Address: line[:i], Why: line[i+2 : len(line)-1]})
			continue
		}
		rest = append(rest, Refusal{Gate: gate, Why: line})
	}
	if len(shaped) > 0 {
		return shaped
	}
	return rest
}
