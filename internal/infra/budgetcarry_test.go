package infra

import (
	"encoding/json"
	"testing"
)

// The budget alert is the installation root's one resource whose figures live
// outside the work directory: the amount and the currency in the project's
// creation fact, the billing account on the alert's own resource in the
// installation's state. CarriedBudget is the half of that carry this package
// owns — a read-modify-write of ONE key (BudgetKey) with the figures the caller
// read out of the cloud, so a document rebuilt from a spec does not forget an
// alert the installation holds, and a plain `fugaro update` plans no change to
// google_billing_budget (R5, R7 of docs/managed-flag-ergonomics). It is the
// same shape of carry as CarriedLicence and CarriedOrchestrator; the one thing
// it does differently is take its figures from the caller rather than from a
// previous document, because neither of the two is in one.
//
// Mutation (run, restore): drop the setVarsKey write and the first half of this
// test reports a document with no budget in it — which is exactly the plan that
// deletes the alert, and the plan the guard then refuses.
func TestCarriedBudgetKeepsPrevious(t *testing.T) {
	const fresh = `{"project": "proj-1234", "region": "us-east5"}`
	carried := &Budget{BillingAccount: "0123AB-4567CD-89EF01", Amount: 250, CurrencyCode: "EUR"}

	got, err := CarriedBudget([]byte(fresh), carried)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Project string  `json:"project"`
		Region  string  `json:"region"`
		Budget  *Budget `json:"budget"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if doc.Budget == nil {
		t.Fatalf("the alert the installation holds is not in the document this run hands Terraform:\n%s. The amount, the currency and the account are STATE, and a document without the key is the plan of the alert's removal — the guard refuses it by name and a plain `fugaro update` stops, which is the HARD RULE (CLAUDE.md) this carry is for", got)
	}
	if *doc.Budget != *carried {
		t.Errorf("the carried alert is %+v, want the figures read out of the cloud (%+v): the carry is a read-modify-write of the cloud's own record, never a figure of its own", *doc.Budget, *carried)
	}

	// And nothing else moved: every other entry of the fresh document is still
	// there, and no key beside the one was added.
	var want, whole map[string]any
	if err := json.Unmarshal([]byte(fresh), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &whole); err != nil {
		t.Fatal(err)
	}
	delete(whole, BudgetKey)
	if !mapsEqual(want, whole) {
		t.Errorf("the carried document changed beyond the one key:\nwant %v\ngot  %v", want, whole)
	}
}

// A run whose flags or wizard answered the budget composes that answer into the
// document, and it stands: the carry never replaces a figure the run was given,
// which is D2215's rule — a default never overwrites a recorded amount — seen
// from the side that would otherwise let a stale carried amount win over what a
// person just typed. nil is the same answer twice over: a run that answered the
// budget itself (its spec carries one), and an installation that holds no alert
// at all (nothing was read to carry).
func TestCarriedBudgetExplicitReplaces(t *testing.T) {
	const own = `{"project": "proj-1234", "budget": {"billing_account": "ZZZZZZ-ZZZZZZ-ZZZZZZ", "amount": 7, "currency_code": "USD"}}`
	carried := &Budget{BillingAccount: "0123AB-4567CD-89EF01", Amount: 250, CurrencyCode: "EUR"}
	if got, err := CarriedBudget([]byte(own), carried); err != nil || string(got) != own {
		t.Errorf("a document that names its own budget = %s, %v, want it unchanged: the run's own answer is what the plan is made from", got, err)
	}

	const fresh = `{"project": "proj-1234"}`
	if got, err := CarriedBudget([]byte(fresh), nil); err != nil || string(got) != fresh {
		t.Errorf("a run that carries nothing = %s, %v, want the document unchanged", got, err)
	}

	// A document this program cannot read is an error and not "nothing to
	// carry": the direction that fails closed is the one that refuses to write
	// a document dropping the alert.
	if _, err := CarriedBudget([]byte("{not json"), carried); err == nil {
		t.Error("an unreadable document carried nothing forward without an error: a run that cannot read the installation's tfvars plans the alert's removal, and it has to stop instead")
	}
}

// The figures written are the caller's own — whatever the project's creation
// fact records, at the amount a rewrite by `fugaro managed flag` last left
// there — and never a default: a carry that fell back to one would raise an
// installation's alert to a figure nobody chose and rewrite a bill threshold
// silently. D2212's default amount belongs to the run that creates the alert;
// this one only repeats what the cloud already says.
func TestCarriedBudgetAmountFromMarker(t *testing.T) {
	const fresh = `{"project": "proj-1234"}`
	for _, tc := range []Budget{
		{BillingAccount: "0123AB-4567CD-89EF01", Amount: 7, CurrencyCode: "EUR"},
		{BillingAccount: "AAAAAA-BBBBBB-CCCCCC", Amount: 1000, CurrencyCode: "USD"},
	} {
		got, err := CarriedBudget([]byte(fresh), &tc)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Budget *Budget `json:"budget"`
		}
		if err := json.Unmarshal(got, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Budget == nil || *doc.Budget != tc {
			t.Errorf("carrying %+v wrote %v, want the recorded figures themselves", tc, doc.Budget)
		}
	}
}

// The billing account is the one figure no document holds: the alert sits on an
// account, and the account survives only in the installation root's state, which
// is what a machine that never ran the apply can still read. A state with no
// alert answers "no alert" and no error — the state of every installation before
// the alert's first apply, and an installation whose document this carry then
// leaves alone. A state that names the resource and no account, one of a format
// version this build does not read, and one that is not JSON at all are errors:
// "no account" answered about a state that holds an alert is the plan that
// deletes it.
func TestCarriedBudgetReadsTheAccountFromTheInstallationState(t *testing.T) {
	const account = "0123AB-4567CD-89EF01"
	state := func(resources string) []byte {
		return []byte(`{"version":4,"terraform_version":"1.9.8","serial":1,"lineage":"x","outputs":{},"resources":[` + resources + `]}`)
	}
	budget := `{"mode":"managed","type":"google_billing_budget","name":"this","instances":[{"attributes":{"billing_account":"` + account + `","amount":{"units":"250","nanos":0}}}]}`

	for _, tc := range []struct {
		name         string
		state        []byte
		want         string
		wantFound    bool
		wantErr      bool
		whyItMatters string
	}{
		{name: "the alert's own resource", state: state(budget), want: account, wantFound: true,
			whyItMatters: "the account is on the alert, which is the record a fresh machine can read"},
		{name: "an installation with no alert", state: state(`{"mode":"managed","type":"google_storage_bucket","name":"runs","instances":[{"attributes":{"name":"fugaro-runs-proj-1234"}}]}`),
			whyItMatters: "nothing to carry is an answer, not a failure: this is every installation before the alert's first apply"},
		{name: "a state that is not a state", state: []byte("<html>error</html>"), wantErr: true},
		{name: "a format version this build does not read", state: []byte(`{"version":5,"resources":[]}`), wantErr: true},
		{name: "an alert with no account", state: state(`{"mode":"managed","type":"google_billing_budget","name":"this","instances":[{"attributes":{"amount":{"units":"250"}}}]}`), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := budgetBillingAccountOf(tc.state)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("budgetBillingAccountOf(%s) = %q, %v, nil; want an error: a state this build cannot read as the thing it is must not be answered as a missing account, which plans the alert's deletion", tc.state, got, found)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || found != tc.wantFound {
				t.Errorf("budgetBillingAccountOf(%s) = %q, %v; want %q, %v. %s", tc.state, got, found, tc.want, tc.wantFound, tc.whyItMatters)
			}
		})
	}
}
