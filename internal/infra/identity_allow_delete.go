package infra

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// identityMinterRowPrefixes are the addresses of the minter rows the
// launcher-does-not-mint switch turns off, one prefix per root: the people's
// row in the Firebase root (modules/firebase/iam.tf's
// google_service_account_iam_member.minter, one instance per launcher and
// operator, keyed by the member string) and the resident orchestrator's in the
// installation root (modules/installation/orchestrator.tf's
// google_service_account_iam_member.orchestrator_token_minter, one instance).
// Both are google_service_account_iam_member resources, so each address is the
// resource's own path with its instance key in brackets: ["<member>"] for the
// people's rows (a for_each over the member set) and [0] for the
// orchestrator's (a count).
//
// The opening bracket is part of the prefix and not a nicety: without it
// ...iam_member.minter would also prefix ...iam_member.minter_extra, and a
// prefix that named a row this switch does not turn off would be an address the
// guard admits that nobody decided to remove. The rows the design does NOT
// remove are named apart from these and never match either: the issuer's own
// row is issuer_minter (modules/firebase/iam.tf) and the mobile service's is
// mobile_token_minter.
var identityMinterRowPrefixes = []string{
	"module.firebase.google_service_account_iam_member.minter[",
	"module.installation.google_service_account_iam_member.orchestrator_token_minter[",
}

// peopleMinterVar is the launcher-does-not-mint switch's name, one name in both
// roots (modules/firebase/variables.tf and modules/installation/variables.tf):
// the plan that moves the grant to the credentials service turns both off at
// once (LM14).
const peopleMinterVar = "people_minter"

// PeopleMinterOff reports whether a root's tfvars document says the
// launcher-does-not-mint switch is off, and it is the read both carries make
// (CarriedPeopleMinter) and the one the update door makes to tell a grant that
// has moved from one still to move (LM14, R10). A document that carries no
// such key — every document this build writes before the grant moves, and one
// a machine with no workdir has none of — is ON, which is the root's own
// default.
//
// A document that cannot be read is an error and never a "no": the caller
// writes a root from what this answers, and answering "on" for a document this
// build cannot read is a plan that puts the people's rows back.
func PeopleMinterOff(vars []byte) (bool, error) {
	if len(vars) == 0 {
		return false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return false, fmt.Errorf("the terraform.tfvars.json is not readable: %w", err)
	}
	v, ok := doc[peopleMinterVar]
	if !ok || v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("the terraform.tfvars.json's %s is not a bool", peopleMinterVar)
	}
	return !b, nil
}

// SetPeopleMinterOff returns the tfvars document vars with the switch written
// off. It is what LM14's own stage writes into a root's document before it
// re-applies that root, and the document has to be one the root was already
// written with: a document this build cannot read is an error rather than a
// fresh `{"people_minter": false}`, which would hand Terraform a root with
// every other setting missing.
func SetPeopleMinterOff(vars []byte) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the terraform.tfvars.json is not readable: %w", err)
	}
	doc[peopleMinterVar] = false
	return sortedJSON(doc)
}

// CarriedPeopleMinter returns vars with the switch off when the document prev
// said so, and vars untouched otherwise: the carry both roots' writers make, so
// that the one run that moves the grant is the only one that writes the key and
// every later run of either root keeps it off.
//
// It is the same shape as CarriedOrchestrator and CarriedIntake — a key this
// run's spec does not compose, carried forward from the document the workdir
// held — and its direction is the one that must never be lost: a run that
// dropped the key would hand the root its default, and the default is the
// people's rows back on an installation whose service has already been proved
// to sign as the runs' signer. A document that already carries the key is
// returned untouched, so the run that turns it off (LM14's stage) always wins.
func CarriedPeopleMinter(vars, prev []byte) ([]byte, error) {
	if len(prev) == 0 {
		return vars, nil // no document held one: nothing to carry
	}
	off, err := PeopleMinterOff(prev)
	if err != nil || !off {
		// A document this build cannot read is not permission to write the
		// default: fail rather than carry nothing forward, as every carry here
		// does.
		if err != nil {
			return nil, err
		}
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the terraform.tfvars.json is not readable: %w", err)
	}
	if _, own := doc[peopleMinterVar]; own {
		return vars, nil // the caller's own answer stands
	}
	doc[peopleMinterVar] = false
	return sortedJSON(doc)
}

// PeopleMinter reports whether the launcher-does-not-mint switch is ON in p, as
// the plan's own variables carry it. The plan is the only place that value
// exists: the binary writes no key for the switch, so the roots' default (on)
// is what every plan has until the stage that moves the grant turns it off
// (LM14), and what it hands terraform then is what the plan echoes here.
//
// Everything that is not the plan saying `people_minter = false` is read as ON,
// and that is the answer that leaves the guard judging a delete as it always
// did: a document that does not carry the variable, one whose value is not a
// bool, and no plan at all. The switch-off plan is the one that says so, so a
// reader of this must never infer it from anything else about the plan.
func PeopleMinter(p *tf.Plan) bool {
	if p == nil {
		return true
	}
	v, ok := p.Variables[peopleMinterVar]
	if !ok {
		return true
	}
	b, ok := v.Value.(bool)
	return !ok || b
}

// IdentityAllowDelete is the guard's allow-list for p when the
// launcher-does-not-mint switch is turned off (plan tasks LM11, LM12, LM20):
// each delete in p of a minter row that switch owns — the people's rows in the
// Firebase root and the resident orchestrator's in the installation root — and
// nothing else.
//
// peopleMinter is the switch itself (PeopleMinter(p)), and it is the whole of
// what makes this list the switch-off plan's own: with the switch ON the rows
// are still planned, and a delete of one of them is a member dropped from the
// roster or an orchestrator turned off, which the guard and the managed gate
// must go on judging as they did before this list existed. Naming them here
// because they appear in the plan would be a standing approval of an address,
// which is what the owner's own --allow-delete is for.
//
// It exists because those deletes are this program's own decision and must not
// cost the operator a flag or a second command (CLAUDE.md's HARD RULE about
// `fugaro update`). `fugaro update` applies both roots with the switch off in
// the one plan that removes the rows, once the credentials service has proved
// it signs as the runs' signer (LM14, R8); without this list the guard would
// refuse that plan and point at --allow-delete, which is the step the hard rule
// forbids. For a managed project the same addresses reach the gate as
// managed.Spec.Upgrade, where GuardManaged admits a delete whose address it was
// given (LM20's own wiring; F25), and LM14 wires the Firebase root's own guard
// the same way at its level (F32).
//
// The list is the plan's own: a row p does not delete is not named, so a run
// whose state read failed still converges (it has no delete to admit), and a
// delete at any other address — another grant of either root, the issuer's row,
// the mobile service's — is never named here, so the guard judges it exactly as
// before.
func IdentityAllowDelete(p *tf.Plan, peopleMinter bool) []string {
	if p == nil || peopleMinter {
		return nil
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if !slices.Contains(rc.Change.Actions, "delete") {
			continue
		}
		if slices.ContainsFunc(identityMinterRowPrefixes, func(pre string) bool {
			return strings.HasPrefix(rc.Address, pre)
		}) {
			out = append(out, rc.Address)
		}
	}
	slices.Sort(out)
	return out
}
