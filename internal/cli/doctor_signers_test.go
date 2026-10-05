package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	dsSigner = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
	dsSched  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
	dsMinter = "projects/proj-1234/roles/fugaroTokenMinter"
)

// signerRig is a doctor rig whose installation has a budget backend in its
// own project (the same-project layout), with an IAM fake holding the signer
// and scheduler accounts and the minter role as the module writes it.
type signerRig struct {
	*doctorRig
	iam *gcpfake.IAM
}

func newSignerDoctorRig(t *testing.T) *signerRig {
	t.Helper()
	r := &signerRig{doctorRig: newDoctorRig(t), iam: gcpfake.NewIAM(t)}
	r.iam.AddServiceAccount("proj-1234", dsSigner, "signer")
	r.iam.AddServiceAccount("proj-1234", dsSched, "scheduler")
	r.iam.AddRole("proj-1234", "fugaroTokenMinter", "Fugaro token minter", false)
	r.iam.SetRolePermissions(dsMinter, "iam.serviceAccounts.signJwt")
	r.withBudget(t, "proj-1234", dsSigner)
	return r
}

// withBudget gives the installation's local config a budget backend in
// Firebase project fp, and an IAM endpoint at the fake.
func (r *signerRig) withBudget(t *testing.T, fp, signer string) {
	t.Helper()
	cfg := strings.Replace(r.cfg, "endpoints: { ", "endpoints: { iam: "+r.iam.URL+"/, ", 1) +
		"budget: { mode: observe, per_run_usd: 5, firebase_project: " + fp + ", token_signer: " + signer + " }\n"
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *signerRig) doctor(t *testing.T, args ...string) (doctorOutput, error) {
	t.Helper()
	root, _ := skillCheckout(t, wiredAt("v0.2.0")) // a wired checkout, so --strict fails only for the signers
	out, _, err := execute(t, append([]string{"doctor", "--json", "--dir", root}, args...)...)
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	return o, err
}

func signerChecks(o doctorOutput) []doctorCheck {
	var out []doctorCheck
	for _, c := range o.Checks {
		if strings.HasPrefix(c.ID, "token-signers") {
			out = append(out, c)
		}
	}
	return out
}

func (r *signerRig) project(bs ...gcpfake.Binding) {
	r.crm.SetPolicy("proj-1234", append([]gcpfake.Binding{{Role: "roles/owner", Members: []string{"user:owner@example.com"}}}, bs...)...)
}

func TestDoctorTokenSignersNotCheckedWithoutABudgetBackend(t *testing.T) {
	r := newDoctorRig(t)
	o, _ := (&signerRig{doctorRig: r}).doctor(t)
	if cs := signerChecks(o); len(cs) != 0 {
		t.Fatalf("checks %+v", cs)
	}
}

func TestDoctorTokenSignersNone(t *testing.T) {
	r := newSignerDoctorRig(t)
	o, err := r.doctor(t, "--strict")
	cs := signerChecks(o)
	if err != nil || len(cs) != 1 || !cs[0].OK || cs[0].ID != "token-signers" {
		t.Fatalf("err %v, checks %+v", err, cs)
	}
}

// Only the designed path and the Admin SDK account: informational, so it
// never fails doctor, even with --strict.
func TestDoctorTokenSignersOnlyExpectedIsInfo(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.iam.SetServiceAccountPolicy("proj-1234", dsSigner, gcpfake.Binding{Role: dsMinter, Members: []string{"user:launcher@example.com"}})
	r.iam.AddServiceAccount("proj-1234", "firebase-adminsdk-fbsvc@proj-1234.iam.gserviceaccount.com", "adminsdk")
	r.project(gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"serviceAccount:firebase-adminsdk-fbsvc@proj-1234.iam.gserviceaccount.com"}})
	o, err := r.doctor(t, "--strict")
	cs := signerChecks(o)
	if err != nil || len(cs) != 1 {
		t.Fatalf("err %v, checks %+v", err, cs)
	}
	c := cs[0]
	if c.ID != "token-signers" || c.OK || c.Severity != "info" || c.Fix == "" ||
		!strings.Contains(c.Problem, "user:launcher@example.com") || !strings.Contains(c.Problem, "firebase-adminsdk-fbsvc@") {
		t.Fatalf("check %+v", c)
	}
}

func TestDoctorTokenSignersUnexpectedWarns(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.iam.SetServiceAccountPolicy("proj-1234", dsSigner, gcpfake.Binding{Role: dsMinter, Members: []string{"user:launcher@example.com"}})
	r.project(gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"}})
	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("a warning failed doctor without --strict: %v", err)
	}
	cs := signerChecks(o)
	if len(cs) != 1 {
		t.Fatalf("checks %+v", cs)
	}
	c := cs[0]
	for _, want := range []string{"user:eve@example.com", "roles/iam.serviceAccountTokenCreator", "project proj-1234",
		"can sign sign-in tokens with any claims, i.e. act as any run against the budget database"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("problem %q lacks %q", c.Problem, want)
		}
	}
	if c.Severity != "warning" || c.OK || c.ID != "token-signers-1" {
		t.Fatalf("check %+v", c)
	}
	if want := "gcloud projects remove-iam-policy-binding proj-1234 --member=user:eve@example.com --role=roles/iam.serviceAccountTokenCreator"; c.Fix != want {
		t.Fatalf("fix %q, want %q", c.Fix, want)
	}
	if _, err := r.doctor(t, "--strict"); err == nil {
		t.Fatal("--strict: a warning must fail doctor")
	}
}

func TestDoctorTokenSignersServiceAccountLevelAndCustomRole(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.iam.SetServiceAccountPolicy("proj-1234", dsSched, gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"group:ops@example.com"}})
	r.iam.AddRole("proj-1234", "mySigner", "x", false)
	r.iam.SetRolePermissions("projects/proj-1234/roles/mySigner", "iam.serviceAccounts.signJwt")
	r.project(gcpfake.Binding{Role: "projects/proj-1234/roles/mySigner", Members: []string{"user:eve@example.com"}})
	o, _ := r.doctor(t)
	var fixes []string
	for _, c := range signerChecks(o) {
		fixes = append(fixes, c.Fix)
	}
	want := []string{
		"gcloud projects remove-iam-policy-binding proj-1234 --member=user:eve@example.com --role=projects/proj-1234/roles/mySigner",
		"gcloud iam service-accounts remove-iam-policy-binding " + dsSched + " --project proj-1234 --member=group:ops@example.com --role=roles/iam.serviceAccountTokenCreator",
	}
	if len(fixes) != 2 || fixes[0] != want[0] || fixes[1] != want[1] {
		t.Fatalf("fixes %q, want %q", fixes, want)
	}
}

func TestDoctorTokenSignersConditionalAndUnusualMembers(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.project(gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com", "allUsers", "user:ev\x1b[2Jil@example.com"},
		Condition: &gcpfake.IAMCondition{Title: "temp", Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"}})
	o, _ := r.doctor(t)
	cs := signerChecks(o)
	if len(cs) != 3 {
		t.Fatalf("checks %+v", cs)
	}
	for _, c := range cs {
		if strings.ContainsRune(c.Problem+c.Fix, 0x1b) {
			t.Errorf("a control character reached the output: %q", c.Problem+c.Fix)
		}
		if !strings.Contains(c.Problem, "only while temp") || !strings.Contains(c.Fix, "--condition='expression=request.time < timestamp(") {
			t.Errorf("the condition is not named: %+v", c)
		}
	}
}

func TestDoctorTokenSignersGrantsAndUnreadable(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.project(
		gcpfake.Binding{Role: "roles/iam.serviceAccountAdmin", Members: []string{"user:admin@example.com"}},
		gcpfake.Binding{Role: "projects/proj-1234/roles/mystery", Members: []string{"user:who@example.com"}})
	o, _ := r.doctor(t)
	cs := signerChecks(o)
	if len(cs) != 2 {
		t.Fatalf("checks %+v", cs)
	}
	var grant, unread *doctorCheck
	for i, c := range cs {
		switch {
		case strings.Contains(c.Problem, "user:admin@example.com"):
			grant = &cs[i]
		case strings.Contains(c.Problem, "user:who@example.com"):
			unread = &cs[i]
		}
	}
	if grant == nil || !strings.Contains(grant.Problem, "can grant itself the right to sign sign-in tokens") {
		t.Fatalf("grant %+v", cs)
	}
	if unread == nil || !strings.Contains(unread.Problem, "cannot tell") || unread.Fix == "" {
		t.Fatalf("unread %+v", cs)
	}
}

// A Firebase project of its own that the caller cannot read is said, as
// information (doctor is advisory and it is not the caller's fault), never
// "no signers".
func TestDoctorTokenSignersFirebaseProjectUnreadable(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withBudget(t, "other-fp", "fugaro-token-signer@other-fp.iam.gserviceaccount.com")
	o, err := r.doctor(t, "--strict")
	cs := signerChecks(o)
	if err != nil || len(cs) != 1 || cs[0].Severity != "info" || cs[0].OK || !strings.Contains(cs[0].Problem, "other-fp") || cs[0].Fix == "" {
		t.Fatalf("err %v, checks %+v", err, cs)
	}
}

// A separate Firebase project is read through the same fakes.
func TestDoctorTokenSignersSeparateFirebaseProject(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.crm.AddProject("fp-nine", 9)
	r.crm.SetPolicy("fp-nine", gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"}})
	r.iam.AddServiceAccount("fp-nine", "fugaro-token-signer@fp-nine.iam.gserviceaccount.com", "signer")
	r.withBudget(t, "fp-nine", "fugaro-token-signer@fp-nine.iam.gserviceaccount.com")
	o, _ := r.doctor(t)
	cs := signerChecks(o)
	if len(cs) != 1 || !strings.Contains(cs[0].Fix, "remove-iam-policy-binding fp-nine ") {
		t.Fatalf("checks %+v (error %q)", cs, o.Error)
	}
}

func TestDoctorTokenSignersReadOnly(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.project(gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"}})
	r.doctor(t)
	if len(r.iam.Requests()) == 0 {
		t.Fatal("the IAM fake was never called")
	}
	for _, req := range append(r.crm.Requests(), r.iam.Requests()...) {
		if req.Method != http.MethodGet && !(req.Method == http.MethodPost && strings.HasSuffix(req.Path, ":getIamPolicy")) {
			t.Errorf("doctor made a non-read call: %s %s", req.Method, req.Path)
		}
	}
}
