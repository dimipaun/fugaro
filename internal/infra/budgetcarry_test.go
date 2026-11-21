package infra

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The budget block a run carries between runs (R5, R7 of
// docs/managed-flag-ergonomics/reconciliation.md) is composed of the
// installation's own two records and never of the work directory: the amount
// and the currency are the project's creation fact's, and the billing account
// is the one the budget resource in the installation root's remote state was
// created with. The read of that state is what these tests hold: the parser
// takes the account out of terraform's own document and nothing else, the read
// of the object answers "nothing carried" for an installation that has no state
// yet, and Block is the one rule about what the run's variables get.

// testBillingAccount is a billing account ID of the shape the installation
// module's own variable validation takes.
const testBillingAccount = "0123AB-4567CD-89EF01"

// budgetState is a version-4 state document as terraform's GCS backend writes
// it, holding the installation module's budget resource with the attributes
// given. The resource's other attributes are terraform's, not this program's:
// the carry reads the billing account and no other.
func budgetState(attrs map[string]string) []byte {
	var b strings.Builder
	b.WriteString(`{"version":4,"terraform_version":"1.9.8","serial":7,"lineage":"a1","outputs":{},`)
	b.WriteString(`"resources":[{"mode":"managed","type":"google_project_service","name":"apis",`)
	b.WriteString(`"provider":"provider[\"registry.terraform.io/hashicorp/google\"]","instances":[{"schema_version":0,"attributes":{"service":"run.googleapis.com"}}]},`)
	b.WriteString(`{"mode":"managed","module":"module.installation","type":"google_billing_budget","name":"this",`)
	b.WriteString(`"provider":"provider[\"registry.terraform.io/hashicorp/google\"]","instances":[{"schema_version":0,"attributes":{`)
	first := true
	for _, k := range []string{"billing_account", "display_name", "amount", "currency_code"} {
		v, ok := attrs[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:%q", k, v)
	}
	b.WriteString(`},"sensitive_attributes":[],"private":"bnVuaA=="}]}],`)
	b.WriteString(`"check_results":null}`)
	return []byte(b.String())
}

// TestCarriedBudgetKeepsPrevious is the carry itself: the installation's own
// records — the creation fact's figures and the state's account — are the block
// an ordinary run's variables get when the run was given no budget flag, so a
// plain `fugaro update` after the run that created the alert plans no change to
// google_billing_budget. Nothing here reads a work directory: the two records
// are read from the cloud, which is what makes the carry work on a machine
// whose workdir holds no tfvars (F19).
func TestCarriedBudgetKeepsPrevious(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	if err := b.WriteAll(ctx, InstallationStateObject,
		budgetState(map[string]string{"billing_account": testBillingAccount}), nil); err != nil {
		t.Fatal(err)
	}
	account, ok, err := ReadInstallationBudgetAccount(ctx, f.c, testStateBucket)
	if err != nil || !ok || account != testBillingAccount {
		t.Fatalf("the state's billing account = %q, %v, %v, want %q", account, ok, err, testBillingAccount)
	}
	carried := CarriedBudget{Amount: 250, CurrencyCode: "USD", BillingAccount: account}
	got, err := carried.Block(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &Budget{BillingAccount: testBillingAccount, Amount: 250, CurrencyCode: "USD"}
	if got == nil || *got != *want {
		t.Fatalf("the carried block = %+v, want %+v", got, want)
	}
	// The same run before anything was applied: no state object at all is
	// "nothing carried", not an error, and the block is then absent — the plan
	// that deletes the alert is the delete guard's to refuse, not this read's.
	empty := newCloud(t)
	empty.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	account, ok, err = ReadInstallationBudgetAccount(ctx, empty.c, testStateBucket)
	if err != nil || ok || account != "" {
		t.Fatalf("an installation with no state answered %q, %v, %v, want nothing carried", account, ok, err)
	}
	if got, err := (CarriedBudget{Amount: 250, CurrencyCode: "USD"}).Block(nil); err != nil || got != nil {
		t.Fatalf("a half-carried block = %+v, %v, want no block", got, err)
	}
	if got, err := (CarriedBudget{}).Block(nil); err != nil || got != nil {
		t.Fatalf("an empty carry = %+v, %v, want no block", got, err)
	}
}

// TestCarriedBudgetExplicitReplaces: the flags win where they are given (R5),
// and an explicit --budget with no --billing-account takes the carried account
// (R7) — the one case where a flag and a record are one block. An explicit
// --budget with nothing carried to take an account from is a refusal and not a
// block with an empty account: the module cannot create an alert without one,
// so the run stops with the flag it needs.
func TestCarriedBudgetExplicitReplaces(t *testing.T) {
	carried := CarriedBudget{Amount: 100, CurrencyCode: "EUR", BillingAccount: testBillingAccount}
	explicit := &Budget{BillingAccount: "111111-222222-333333", Amount: 500, CurrencyCode: "USD"}
	got, err := carried.Block(explicit)
	if err != nil || got != explicit {
		t.Fatalf("an explicit trio = %+v, %v, want the flags' own block", got, err)
	}
	got, err = carried.Block(&Budget{Amount: 900, CurrencyCode: "GBP"})
	if err != nil {
		t.Fatal(err)
	}
	want := &Budget{BillingAccount: testBillingAccount, Amount: 900, CurrencyCode: "GBP"}
	if got == nil || *got != *want {
		t.Fatalf("an explicit --budget with no account = %+v, want the carried account %+v", got, want)
	}
	if _, err := (CarriedBudget{Amount: 100, CurrencyCode: "EUR"}).Block(&Budget{Amount: 900, CurrencyCode: "GBP"}); err == nil ||
		!strings.Contains(err.Error(), "--billing-account") {
		t.Fatalf("an explicit --budget with nothing carried = %v, want a refusal naming the flag", err)
	}
}

// TestCarriedBudgetAmountFromMarker: the amount and the currency are the
// project's creation fact's and not the state's (R7), which is what makes
// `fugaro managed flag --budget` — a rewrite of that fact — the thing the next
// run's alert follows. The state's own copy of both is deliberately different
// here: a carry that read them out of the state would answer the figures the
// alert was created with rather than the ones it was moved to.
func TestCarriedBudgetAmountFromMarker(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	state := budgetState(map[string]string{
		"billing_account": testBillingAccount, "amount": "999", "currency_code": "JPY",
		"display_name": "fugaro monthly budget",
	})
	if err := b.WriteAll(ctx, InstallationStateObject, state, nil); err != nil {
		t.Fatal(err)
	}
	account, ok, err := ReadInstallationBudgetAccount(ctx, f.c, testStateBucket)
	if err != nil || !ok {
		t.Fatalf("the state's account = %q, %v, %v", account, ok, err)
	}
	// The creation fact's figures, which is what internal/cli's budgetCarry
	// passes in: the amount the alert was moved to, in its own currency.
	got, err := CarriedBudget{Amount: 250, CurrencyCode: "USD", BillingAccount: account}.Block(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &Budget{BillingAccount: testBillingAccount, Amount: 250, CurrencyCode: "USD"}
	if got == nil || *got != *want {
		t.Fatalf("the carried block = %+v, want the creation fact's figures %+v", got, want)
	}
}

// The state's budget resource is found by its whole address and by nothing
// else: a resource of another module, type or name is not the installation's
// budget, and reading one as if it were would carry a billing account that
// belongs to something else — a wrong alert is worse than no alert, so every
// near miss here is "nothing carried". A document this build cannot read at all
// is an error and never "nothing carried": the answer for a read that could not
// be made is the run's refusal, not a plan that drops the alert.
func TestStateBudgetBillingAccountMatchesTheAddress(t *testing.T) {
	cases := []struct {
		name    string
		state   []byte
		want    string
		wantOK  bool
		wantErr string
	}{
		{"the installation's own budget", budgetState(map[string]string{"billing_account": testBillingAccount}), testBillingAccount, true, ""},
		{"another module", []byte(`{"version":4,"resources":[{"mode":"managed","module":"module.firebase","type":"google_billing_budget","name":"this","instances":[{"attributes":{"billing_account":"` + testBillingAccount + `"}}]}]}`), "", false, ""},
		{"another type", []byte(`{"version":4,"resources":[{"mode":"managed","module":"module.installation","type":"google_project","name":"this","instances":[{"attributes":{"billing_account":"` + testBillingAccount + `"}}]}]}`), "", false, ""},
		{"another name", []byte(`{"version":4,"resources":[{"mode":"managed","module":"module.installation","type":"google_billing_budget","name":"other","instances":[{"attributes":{"billing_account":"` + testBillingAccount + `"}}]}]}`), "", false, ""},
		{"a data source", []byte(`{"version":4,"resources":[{"mode":"data","module":"module.installation","type":"google_billing_budget","name":"this","instances":[{"attributes":{"billing_account":"` + testBillingAccount + `"}}]}]}`), "", false, ""},
		{"no resources at all", []byte(`{"version":4,"resources":[]}`), "", false, ""},
		{"no document at all", nil, "", false, ""},
		{"an account the state never recorded", budgetState(map[string]string{"display_name": "fugaro monthly budget"}), "", false, ""},
		{"an older state format", []byte(`{"version":3,"resources":[{"mode":"managed","module":"module.installation","type":"google_billing_budget","name":"this","instances":[{"attributes":{"billing_account":"` + testBillingAccount + `"}}]}]}`), "", false, "format version 3"},
		{"a document that is not JSON", []byte("not json at all"), "", false, "reading the installation's state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := StateBudgetBillingAccount(tc.state)
			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("StateBudgetBillingAccount = %q, %v, %v, want an error naming %q", got, ok, err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("StateBudgetBillingAccount = %q, %v, %v", got, ok, err)
			case got != tc.want || ok != tc.wantOK:
				t.Fatalf("StateBudgetBillingAccount = %q, %v, want %q, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// A state object this program cannot read is a failure of the run, not a carry
// of nothing: a bucket that refuses the read would otherwise be an installation
// whose alert the next plan deletes (Global Constraint 3).
func TestReadInstallationBudgetAccountRefusesAFailedRead(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	if err := b.WriteAll(ctx, InstallationStateObject,
		budgetState(map[string]string{"billing_account": testBillingAccount}), nil); err != nil {
		t.Fatal(err)
	}
	f.gcs.FailObjectGetsAfter(testStateBucket, InstallationStateObject, 0)
	if account, ok, err := ReadInstallationBudgetAccount(ctx, f.c, testStateBucket); err == nil || ok || account != "" {
		t.Fatalf("a refused read answered %q, %v, %v, want an error", account, ok, err)
	}
}
