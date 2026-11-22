package infra

import (
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

// IdentityAllowDelete is the guard's allow-list for p when the
// launcher-does-not-mint switch is turned off (plan tasks LM11, LM12, LM20):
// each delete in p of a minter row that switch owns — the people's rows in the
// Firebase root and the resident orchestrator's in the installation root — and
// nothing else.
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
// The list is the plan's own: a row p does not delete is not named whatever the
// switch says, so a run whose state read failed still converges (it has no
// delete to admit), and a delete at any other address — another grant of either
// root, the issuer's row, the mobile service's — is never named here, so the
// guard judges it exactly as before.
//
// What this list does not distinguish is WHY a minter row is going: the switch
// turning off and a member dropped from the roster plan the same delete at the
// same address, and both are admitted. That is the accepted shape of the
// switch (the rows are fugaro's own either way, and an operator who removed a
// member meant it); naming the reason would need a field the plan does not
// carry.
func IdentityAllowDelete(p *tf.Plan) []string {
	if p == nil {
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
