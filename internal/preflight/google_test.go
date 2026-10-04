package preflight

import (
	"context"
	"strings"
	"testing"

	billing "google.golang.org/api/cloudbilling/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// newCRM connects a *crm.Service to a fresh CRM fake, with project known
// at number 123456789012.
func newCRM(t *testing.T) (*gcpfake.CRM, *crm.Service) {
	t.Helper()
	f := gcpfake.NewCRM(t)
	f.AddProject("proj-1234", 123456789012)
	svc, err := crm.NewService(context.Background(), option.WithEndpoint(f.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return f, svc
}

func newBilling(t *testing.T) (*gcpfake.Billing, *billing.APIService) {
	t.Helper()
	f := gcpfake.NewBilling(t)
	svc, err := billing.NewService(context.Background(), option.WithEndpoint(f.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return f, svc
}

func TestBillingAPIDisabledOnQuotaProject(t *testing.T) {
	f, svc := newBilling(t)
	su := gcpfake.NewServiceUsage(t)
	su.Consumer = "projects/999999999999"
	su.Disable("cloudbilling.googleapis.com", f.Server)

	cs := Billing(context.Background(), svc, "proj-1234")
	if len(cs) != 1 {
		t.Fatalf("checks = %+v", cs)
	}
	c := cs[0]
	if c.ID != "billing-api" || c.OK {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Problem, "999999999999") || !strings.Contains(c.Fix, "999999999999") || !strings.Contains(c.Fix, "cloudbilling.googleapis.com") {
		t.Fatalf("problem %q fix %q didn't name the quota project and the API", c.Problem, c.Fix)
	}
	if strings.Contains(c.Fix, "\n") {
		t.Fatalf("fix is not one line: %q", c.Fix)
	}
	if len(f.Requests()) == 0 {
		t.Fatal("the check never read billing info")
	}
}

func TestBillingLinked(t *testing.T) {
	f, svc := newBilling(t)
	f.SetBilling("proj-1234", true)
	cs := Billing(context.Background(), svc, "proj-1234")
	if len(cs) != 1 || cs[0].ID != "billing-linked" || !cs[0].OK {
		t.Fatalf("got %+v", cs)
	}
}

func TestBillingNotLinked(t *testing.T) {
	f, svc := newBilling(t)
	f.SetBilling("proj-1234", false)
	cs := Billing(context.Background(), svc, "proj-1234")
	if len(cs) != 1 {
		t.Fatalf("checks = %+v", cs)
	}
	c := cs[0]
	if c.ID != "billing-linked" || c.OK || !strings.Contains(c.Problem, "proj-1234") || !strings.Contains(c.Fix, "gcloud billing projects link proj-1234") {
		t.Fatalf("got %+v", c)
	}
}

func TestDefaultComputeSAEditorWarns(t *testing.T) {
	f, svc := newCRM(t)
	f.SetPolicy("proj-1234", gcpfake.Binding{
		Role:    "roles/editor",
		Members: []string{"serviceAccount:123456789012-compute@developer.gserviceaccount.com", "user:owner@example.com"},
	})

	// Not the same-project layout: the risk is not this layout's to fix.
	cs := IAMPolicy(context.Background(), svc, "proj-1234", false)
	if c, ok := checkByID(cs, "default-service-accounts"); ok {
		t.Fatalf("a risk check was reported outside the same-project layout: %+v", c)
	}

	cs = IAMPolicy(context.Background(), svc, "proj-1234", true)
	iam, ok := checkByID(cs, "iam-policy")
	if !ok || !iam.OK {
		t.Fatalf("iam-policy = %+v", iam)
	}
	risk, ok := checkByID(cs, "default-service-accounts")
	if !ok || risk.OK {
		t.Fatalf("default-service-accounts = %+v", risk)
	}
	if !strings.Contains(risk.Problem, "123456789012-compute@developer.gserviceaccount.com") {
		t.Fatalf("problem didn't name the account: %q", risk.Problem)
	}
	if !strings.Contains(risk.Fix, "roles/editor") || strings.Contains(risk.Fix, "\n") {
		t.Fatalf("fix = %q", risk.Fix)
	}
}

func TestDefaultComputeSAEditorOKWithoutRisk(t *testing.T) {
	f, svc := newCRM(t)
	f.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/editor", Members: []string{"user:owner@example.com"}})
	cs := IAMPolicy(context.Background(), svc, "proj-1234", true)
	risk, ok := checkByID(cs, "default-service-accounts")
	if !ok || !risk.OK {
		t.Fatalf("default-service-accounts = %+v", risk)
	}
}

// A caller who can't even read the IAM policy (an owner hasn't granted
// them anything yet) gets the role to ask for, named, in the fix: design
// §3.1 "the check for 'may I apply?' is the plan's first permission
// failure, reported ... with the roles named."
func TestMissingRoleNamedInFix(t *testing.T) {
	f, svc := newCRM(t)
	f.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "the caller does not have permission")

	cs := IAMPolicy(context.Background(), svc, "proj-1234", true)
	if len(cs) != 1 {
		t.Fatalf("checks = %+v", cs)
	}
	c := cs[0]
	if c.ID != "iam-policy" || c.OK {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Fix, "roles/") {
		t.Fatalf("fix didn't name a role: %q", c.Fix)
	}
	if strings.Contains(c.Fix, "\n") || strings.Contains(c.Problem, "\n") {
		t.Fatalf("not one line: problem %q fix %q", c.Problem, c.Fix)
	}
}

// Every check here only ever reads: run the whole Google-backed suite
// against fakes that fail the test on anything they don't implement
// (gcpfake's own rule, package doc), and check the calls it did make are
// exactly the reads each check documents.
func TestPreflightReadOnly(t *testing.T) {
	crmFake, crmSvc := newCRM(t)
	crmFake.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}})
	billingFake, billingSvc := newBilling(t)
	billingFake.SetBilling("proj-1234", true)

	iam := IAMPolicy(context.Background(), crmSvc, "proj-1234", true)
	bill := Billing(context.Background(), billingSvc, "proj-1234")

	for _, c := range append(iam, bill...) {
		if !c.OK {
			t.Errorf("%s: unexpectedly not ok: %s", c.ID, c.Problem)
		}
	}

	for _, r := range crmFake.Requests() {
		if r.Method != "GET" && !(r.Method == "POST" && strings.HasSuffix(r.Path, ":getIamPolicy")) {
			t.Errorf("preflight made a non-read call to the Resource Manager fake: %s %s", r.Method, r.Path)
		}
	}
	for _, r := range billingFake.Requests() {
		if r.Method != "GET" {
			t.Errorf("preflight made a non-read call to the Billing fake: %s %s", r.Method, r.Path)
		}
	}
}
