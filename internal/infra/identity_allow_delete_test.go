package infra

import (
	"encoding/json"
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
	got := IdentityAllowDelete(plan, false)
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The same deletes with the switch on are a roster that shrank and an
	// orchestrator turned off, and the list is the switch-off plan's own: it
	// names nothing, so the guard and the gate judge them as they always did.
	if got := IdentityAllowDelete(plan, true); got != nil {
		t.Fatalf("the list named %q with the switch on", got)
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
	if got := IdentityAllowDelete(near, false); len(got) != 0 {
		t.Fatalf("the list names %q, which the switch does not turn off", got)
	}
	if got := IdentityAllowDelete(nil, false); got != nil {
		t.Fatalf("a nil plan named %q", got)
	}
}

// The switch is read out of the plan's own variables, and the switch-off plan
// is the one that says so: everything else — no plan, a plan carrying no
// variable, a value that is not a bool — is read as on, which is the answer
// that names nothing and leaves the guard judging the delete as before.
func TestPeopleMinterIsReadOutOfThePlansOwnVariables(t *testing.T) {
	plan := func(doc string) *tf.Plan {
		var p tf.Plan
		if err := json.Unmarshal([]byte(doc), &p); err != nil {
			t.Fatalf("the fixture is not a plan: %v", err)
		}
		return &p
	}
	for _, tc := range []struct {
		why  string
		plan *tf.Plan
		want bool
	}{
		{"no plan", nil, true},
		{"no variables", plan(`{"resource_changes":[]}`), true},
		{"the switch on", plan(`{"variables":{"people_minter":{"value":true}}}`), true},
		{"the switch off", plan(`{"variables":{"people_minter":{"value":false}}}`), false},
		{"another type", plan(`{"variables":{"people_minter":{"value":"false"}}}`), true},
		{"a null", plan(`{"variables":{"people_minter":{"value":null}}}`), true},
		{"another variable", plan(`{"variables":{"enable_mobile_service":{"value":false}}}`), true},
	} {
		if got := PeopleMinter(tc.plan); got != tc.want {
			t.Errorf("%s: PeopleMinter = %v, want %v", tc.why, got, tc.want)
		}
	}
}

// The carry both roots' writers make (prepare, applyFirebaseRoot) is the one
// direction that must never be lost: a later document that dropped the switch
// would hand the module its default — on — and plan the rows the switch owns
// back, which nothing refuses because they are creates. The switch the run that
// moved the grant turned off is carried into every later document, and a
// document that says nothing about it is carried as it stands.
func TestCarriedPeopleMinterKeepsTheSwitchOff(t *testing.T) {
	canon := func(b []byte) string {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s is not a document: %v", b, err)
		}
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, tc := range []struct {
		why              string
		vars, prev, want string
	}{
		{"no document held one", `{"region":"europe-west1"}`, ``, `{"region":"europe-west1"}`},
		{"the document before says on", `{"region":"europe-west1"}`, `{"people_minter":true}`, `{"region":"europe-west1"}`},
		{"the document before says off", `{"region":"europe-west1"}`, `{"people_minter":false}`, `{"people_minter":false,"region":"europe-west1"}`},
		{"the caller carries its own answer", `{"people_minter":true}`, `{"people_minter":false}`, `{"people_minter":true}`},
	} {
		got, err := CarriedPeopleMinter([]byte(tc.vars), []byte(tc.prev))
		if err != nil {
			t.Errorf("%s: %v", tc.why, err)
			continue
		}
		if canon(got) != canon([]byte(tc.want)) {
			t.Errorf("%s: got %s, want %s", tc.why, got, tc.want)
		}
	}
	// A document this build cannot read is not permission to carry nothing: the
	// caller writes a root from what this answers, and "nothing to carry" is a
	// plan that puts the rows back.
	if _, err := CarriedPeopleMinter([]byte(`{}`), []byte(`{"people_minter":"no"}`)); err == nil {
		t.Error("a document this build cannot read was carried as one that says nothing")
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
	allow := IdentityAllowDelete(plan, false)
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
