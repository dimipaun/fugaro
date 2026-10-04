package infra

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

type projectFakes struct {
	crm  *gcpfake.CRM
	fb   *gcpfake.FirebaseMgmt
	bill *gcpfake.Billing
	pc   *ProjectClients
}

func newProjectFakes(t *testing.T) *projectFakes {
	t.Helper()
	f := &projectFakes{crm: gcpfake.NewCRM(t), fb: gcpfake.NewFirebaseMgmt(t), bill: gcpfake.NewBilling(t)}
	f.fb.UseCRM(f.crm)
	f.bill.UseCRM(f.crm)
	pc, err := NewProjectClients(t.Context(), ProjectEndpoints{ResourceManager: f.crm.URL + "/", Firebase: f.fb.URL + "/", Billing: f.bill.URL + "/"}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.pc = pc
	old := Wait
	Wait.Interval, Wait.Tries = time.Millisecond, 5
	t.Cleanup(func() { Wait = old })
	return f
}

func TestValidateNewProjectID(t *testing.T) {
	for id, ok := range map[string]bool{
		"fugaro-dev": true, "abcdef": true, "a23456789012345678901234567890": true,
		"abcde": false, "a234567890123456789012345678901": false, "Abcdef": false, "1abcdef": false, "abcdef-": false,
		"abc_def": false, "abc.def": false, "my-google-app": false, "ssl-proxy": false, "": false, "abc def": false,
	} {
		if err := ValidateNewProjectID(id); (err == nil) != ok {
			t.Errorf("ValidateNewProjectID(%q) = %v, want ok=%v", id, err, ok)
		}
	}
	for a, ok := range map[string]bool{"0123AB-4567CD-89EF01": true, "0123ab-4567cd-89ef01": false, "billingAccounts/0123AB-4567CD-89EF01": false, "0123AB-4567CD": false, "": false} {
		if err := ValidateBillingAccount(a); (err == nil) != ok {
			t.Errorf("ValidateBillingAccount(%q) = %v", a, err)
		}
	}
	for p, ok := range map[string]bool{"organizations/1": true, "folders/123": true, "projects/1": false, "organizations/x": false, "organizations/": false, "folders/1/2": false} {
		if err := ValidateParent(p); (err == nil) != ok {
			t.Errorf("ValidateParent(%q) = %v", p, err)
		}
	}
}

// A create is polled to its end, the new project is read through the
// propagation delay, Firebase is added and polled, billing is linked and
// read back: the documented sequence, one create.
func TestCreateSequence(t *testing.T) {
	f := newProjectFakes(t)
	f.crm.PendingPolls, f.crm.PropagationReads = 2, 2
	f.fb.PendingPolls = 1
	ctx := context.Background()
	if err := CreateProject(ctx, f.pc, CreateSpec{ID: "fugaro-new-1", DisplayName: "Fugaro", Parent: "folders/77"}); err != nil {
		t.Fatal(err)
	}
	if err := WaitReadable(ctx, f.pc, "fugaro-new-1"); err != nil {
		t.Fatal(err)
	}
	if err := AddFirebase(ctx, f.pc, "fugaro-new-1"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFirebase(ctx, f.pc, "fugaro-new-1"); err != nil {
		t.Fatal(err)
	}
	info, err := ReadProject(ctx, f.pc, "fugaro-new-1")
	if err != nil || !info.Exists || info.State != "ACTIVE" || !info.Firebase || !info.BillingKnown || info.BillingEnabled {
		t.Fatalf("info %+v, err %v", info, err)
	}
	if err := LinkBilling(ctx, f.pc, "fugaro-new-1", "0123AB-4567CD-89EF01", false); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBilling(ctx, f.pc, "fugaro-new-1"); err != nil {
		t.Fatal(err)
	}
	cs := f.crm.Creates()
	if len(cs) != 1 || cs[0].Parent != "folders/77" || cs[0].DisplayName != "Fugaro" {
		t.Errorf("creates %+v", cs)
	}
}

// With no --parent no parent is sent: an organization is never guessed.
func TestCreateSendsNoParentUnlessGiven(t *testing.T) {
	f := newProjectFakes(t)
	if err := CreateProject(context.Background(), f.pc, CreateSpec{ID: "fugaro-new-2", DisplayName: "fugaro-new-2"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range f.crm.Requests() {
		if q.Method == "POST" && strings.Contains(string(q.Body), "parent") {
			t.Errorf("a parent was sent: %s", q.Body)
		}
	}
}

func TestCreateRefusesBadInputBeforeAnyCall(t *testing.T) {
	f := newProjectFakes(t)
	ctx := context.Background()
	for _, s := range []CreateSpec{{ID: "Bad_ID"}, {ID: "fugaro-new-3", Parent: "organizations/x"}} {
		if err := CreateProject(ctx, f.pc, s); err == nil {
			t.Errorf("%+v was accepted", s)
		}
	}
	if err := LinkBilling(ctx, f.pc, "fugaro-new-3", "nope", true); err == nil {
		t.Error("a malformed account was accepted")
	}
	if n := len(f.crm.Requests()) + len(f.bill.Requests()); n != 0 {
		t.Errorf("%d calls", n)
	}
}

func TestCreateTakenAndCannotReadLookAlike(t *testing.T) {
	f := newProjectFakes(t)
	f.crm.AddForeign("fugaro-held-1")
	ctx := context.Background()
	info, err := ReadProject(ctx, f.pc, "fugaro-held-1")
	if err != nil || info.Exists {
		t.Fatalf("info %+v err %v: a project the caller cannot read is reported absent", info, err)
	}
	err = CreateProject(ctx, f.pc, CreateSpec{ID: "fugaro-held-1"})
	var pe *ProjectError
	if !errors.As(err, &pe) || pe.Kind != ProjectTaken || !strings.Contains(pe.Fix, "such as fugaro-held-1-") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateGivesUpWhenNeverReadable(t *testing.T) {
	f := newProjectFakes(t)
	f.crm.PropagationReads = 100
	ctx := context.Background()
	if err := CreateProject(ctx, f.pc, CreateSpec{ID: "fugaro-slow-1"}); err != nil {
		t.Fatal(err)
	}
	err := WaitReadable(ctx, f.pc, "fugaro-slow-1")
	var pe *ProjectError
	if !errors.As(err, &pe) || !strings.Contains(pe.Fix, "rerun") {
		t.Errorf("err = %v", err)
	}
}

// A refusal's text is the service's, verbatim, with a likely cause.
func TestRefusalsKeepServiceText(t *testing.T) {
	f := newProjectFakes(t)
	ctx := context.Background()
	f.crm.FailCreates(403, "PERMISSION_DENIED", "Constraint constraints/x violated by policy")
	err := CreateProject(ctx, f.pc, CreateSpec{ID: "fugaro-pol-1"})
	if err == nil || !strings.Contains(err.Error(), "Constraint constraints/x violated by policy") || !strings.Contains(err.Error(), "organization policy") {
		t.Errorf("err = %v", err)
	}
	f.crm.FailCreates(403, "PERMISSION_DENIED", "The caller does not have permission")
	err = CreateProject(ctx, f.pc, CreateSpec{ID: "fugaro-pol-1"})
	if err == nil || !strings.Contains(err.Error(), "resourcemanager.projects.create") {
		t.Errorf("err = %v", err)
	}
}

// An API that is off on the quota project names the quota project and the
// one-line fix.
func TestAPIDisabledNamesQuotaProject(t *testing.T) {
	f := newProjectFakes(t)
	su := gcpfake.NewServiceUsage(t)
	su.Consumer = "projects/555"
	su.Disable("cloudresourcemanager.googleapis.com", f.crm.Server)
	_, err := ReadProject(context.Background(), f.pc, "fugaro-off-1")
	var pe *ProjectError
	if !errors.As(err, &pe) || pe.Kind != ProjectAPIOff || !strings.Contains(pe.Fix, "gcloud services enable cloudresourcemanager.googleapis.com --project 555") {
		t.Errorf("err = %v", err)
	}
}

// The credentials never reach an error.
func TestProjectErrorsNeverCarryTheToken(t *testing.T) {
	f := newProjectFakes(t)
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer ya29.SECRET-TOKEN")
		return http.DefaultTransport.RoundTrip(r)
	})}
	pc, err := NewProjectClients(context.Background(), ProjectEndpoints{ResourceManager: f.crm.URL + "/", Firebase: f.fb.URL + "/", Billing: f.bill.URL + "/"}, true, hc)
	if err != nil {
		t.Fatal(err)
	}
	f.crm.FailCreates(403, "PERMISSION_DENIED", "denied")
	err = CreateProject(context.Background(), pc, CreateSpec{ID: "fugaro-tok-1"})
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Errorf("err = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLinkBillingRefusals(t *testing.T) {
	f := newProjectFakes(t)
	f.crm.AddProject("fugaro-bill-1", 1)
	f.bill.AddAccount("0123AB-4567CD-89EF01", false)
	f.bill.AddAccount("AAAAAA-BBBBBB-CCCCCC", true)
	ctx := context.Background()
	if err := LinkBilling(ctx, f.pc, "fugaro-bill-1", "0123AB-4567CD-89EF01", false); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("a closed account: %v", err)
	}
	if err := LinkBilling(ctx, f.pc, "fugaro-bill-1", "DDDDDD-EEEEEE-FFFFFF", false); err == nil || !strings.Contains(err.Error(), "billing.resourceAssociations.create") {
		t.Errorf("an account the caller may not use: %v", err)
	}
	if n := len(f.bill.Links()); n != 0 {
		t.Errorf("links %d", n)
	}
}

// A 403 that is not "missing" says what it is.
func TestUserProjectDeniedIsNotMissing(t *testing.T) {
	f := newProjectFakes(t)
	f.crm.Refuse(403, "PERMISSION_DENIED", "USER_PROJECT_DENIED", "Caller does not have required permission to use project qp-1")
	_, err := ReadProject(context.Background(), f.pc, "fugaro-up-1")
	var pe *ProjectError
	if !errors.As(err, &pe) || pe.Kind != ProjectDenied || !strings.Contains(pe.Fix, "set-quota-project") || !strings.Contains(pe.Error(), "USER_PROJECT_DENIED") {
		t.Errorf("err = %v", err)
	}
}

// With expectNoLink the write is refused, and not even sent, when the
// project is linked or its billing cannot be read right before it.
func TestLinkBillingGuard(t *testing.T) {
	f := newProjectFakes(t)
	f.crm.AddProject("fugaro-g-1", 1)
	f.bill.SetBilling("fugaro-g-1", true)
	f.crm.AddProject("fugaro-g-2", 2)
	f.bill.SetSuspended("fugaro-g-2", "AAAAAA-BBBBBB-CCCCCC")
	f.crm.AddProject("fugaro-g-3", 3)
	f.bill.HideBilling("fugaro-g-3")
	ctx := context.Background()
	for _, id := range []string{"fugaro-g-1", "fugaro-g-2", "fugaro-g-3"} {
		err := LinkBilling(ctx, f.pc, id, "0123AB-4567CD-89EF01", true)
		var pe *ProjectError
		if !errors.As(err, &pe) || pe.Kind != ProjectLinkGuard {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if n := f.bill.Attempts(); n != 0 {
		t.Errorf("%d write(s) attempted", n)
	}
}
