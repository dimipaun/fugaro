package infra

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// peopleMinterRow and orchestratorMinterRow are the two rows the
// launcher-does-not-mint switch turns off, as their roots' plans spell them:
// the Firebase root's people's row keyed by the member, and the installation
// root's single orchestrator row.
func peopleMinterRow(member string) string {
	return `module.firebase.google_service_account_iam_member.minter["` + member + `"]`
}

const orchestratorMinterRow = "module.installation.google_service_account_iam_member.orchestrator_token_minter[0]"

// minterRowChange is one minter row as a plan carries it: a
// google_service_account_iam_member on the runs' signer, with the member and
// role the row holds. A delete is what turning the switch off plans.
func minterRowChange(addr, member string, actions ...string) tf.ResourceChange {
	return tf.ResourceChange{Address: addr, Type: "google_service_account_iam_member", Change: tf.Change{
		Actions: actions,
		Before: map[string]any{
			"member": member, "role": "projects/fp-1234/roles/fugaroTokenMinter",
			"service_account_id": "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com",
		},
	}}
}

// The switch's plan deletes the people's minter rows and the orchestrator's,
// and IdentityAllowDelete names exactly those. The red step: with no list the
// guard refuses the ordinary project's plan, and with the list it admits it.
func TestIdentityAllowDeleteNamesOnlyTheMinterRows(t *testing.T) {
	const a, o = "user:a@example.com", "user:o@example.com"
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		minterRowChange(peopleMinterRow(a), a, "delete"),
		minterRowChange(peopleMinterRow(o), o, "delete"),
		minterRowChange(orchestratorMinterRow, "serviceAccount:fugaro-orchestrator@proj-1234.iam.gserviceaccount.com", "delete"),
	}}
	want := []string{peopleMinterRow(a), peopleMinterRow(o), orchestratorMinterRow}
	got := IdentityAllowDelete(plan)
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if err := tf.Guard(plan, nil); err == nil {
		t.Fatal("the guard admitted the switch's deletes with no list")
	}
	if err := tf.Guard(plan, got); err != nil {
		t.Fatalf("the guard refused the update's own deletes: %v", err)
	}

	// The rows the switch does not own — the issuer's and the mobile service's
	// — and a minter row that is not being deleted: none of them is named, so
	// the list is the switch's own deletes and never a standing approval of the
	// address.
	near := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		minterRowChange(`module.firebase.google_service_account_iam_member.issuer_minter[0]`, "serviceAccount:fugaro-credentials@proj-1234.iam.gserviceaccount.com", "delete"),
		minterRowChange(`module.firebase.google_service_account_iam_member.mobile_token_minter[0]`, "serviceAccount:fugaro-mobile@proj-1234.iam.gserviceaccount.com", "delete"),
		minterRowChange(`module.firebase.google_service_account_iam_member.minter_extra[0]`, a, "delete"),
		minterRowChange(peopleMinterRow(a), a, "no-op"),
		minterRowChange(orchestratorMinterRow, "serviceAccount:fugaro-orchestrator@proj-1234.iam.gserviceaccount.com", "update"),
	}}
	if got := IdentityAllowDelete(near); len(got) != 0 {
		t.Fatalf("the list names %q, which the switch does not turn off", got)
	}
	if got := IdentityAllowDelete(nil); got != nil {
		t.Fatalf("a nil plan named %q", got)
	}
}

// The list is exactly the minter rows, so any other delete of the same plan is
// still refused: naming the update's own deletes must not be a way past the
// guard for anything beside them.
func TestGuardStillRefusesAnotherDelete(t *testing.T) {
	const other = "module.installation.google_service_account_iam_member.someone_else[0]"
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		minterRowChange(orchestratorMinterRow, "serviceAccount:fugaro-orchestrator@proj-1234.iam.gserviceaccount.com", "delete"),
		minterRowChange(other, "user:someone@example.com", "delete"),
	}}
	allow := IdentityAllowDelete(plan)
	if !slices.Equal(allow, []string{orchestratorMinterRow}) {
		t.Fatalf("the list is %q, want only the orchestrator's row", allow)
	}
	err := tf.Guard(plan, allow)
	if err == nil {
		t.Fatal("the guard admitted a delete the update does not make")
	}
	if !strings.Contains(err.Error(), other) {
		t.Fatalf("the refusal does not name %s: %v", other, err)
	}
	if strings.Contains(err.Error(), orchestratorMinterRow) {
		t.Fatalf("the refusal names the update's own delete: %v", err)
	}
}
