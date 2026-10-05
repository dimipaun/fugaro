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
	for role, perms := range map[string][]string{
		"roles/iam.serviceAccountTokenCreator": {"iam.serviceAccounts.signJwt", "iam.serviceAccounts.getAccessToken"},
		"roles/iam.serviceAccountAdmin":        {"iam.serviceAccounts.setIamPolicy"},
		"roles/owner":                          {"resourcemanager.projects.get"},
		"roles/firebase.sdkAdminServiceAgent":  {"firebase.projects.get"},
	} {
		r.iam.SetRolePermissions(role, perms...)
	}
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

// summary is the always-present partial-coverage line; signerChecks are the
// per-finding warnings.
func summary(o doctorOutput) (doctorCheck, bool) { return doctorCheckByID(o.Checks, "token-signers") }

func signerChecks(o doctorOutput) []doctorCheck {
	var out []doctorCheck
	for _, c := range o.Checks {
		if len(c.ID) > len("token-signers-") && strings.HasPrefix(c.ID, "token-signers-") && c.ID[len("token-signers-")] >= '0' && c.ID[len("token-signers-")] <= '9' {
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
	if _, ok := summary(o); ok || len(signerChecks(o)) != 0 {
		t.Fatalf("checks %+v", o.Checks)
	}
}

// Nothing found is still said to be partial: information, never "ok".
func TestDoctorTokenSignersNoneIsStillPartial(t *testing.T) {
	r := newSignerDoctorRig(t)
	o, err := r.doctor(t, "--strict")
	c, ok := summary(o)
	if err != nil || !ok || c.OK || c.Severity != "info" || len(signerChecks(o)) != 0 {
		t.Fatalf("err %v, checks %+v", err, o.Checks)
	}
	for _, want := range []string{"checked the project and service-account IAM policies of proj-1234", "not read:", "folder- and organization-inherited bindings", "deny policies", "Google service agents of this project are read and classified", "docs/gcp-setup.md#token-signers"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("summary %q lacks %q", c.Problem, want)
		}
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
	c, ok := summary(o)
	if err != nil || !ok || len(signerChecks(o)) != 0 {
		t.Fatalf("err %v, checks %+v", err, o.Checks)
	}
	if c.OK || c.Severity != "info" || c.Fix == "" ||
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
		"iam.serviceAccounts.getAccessToken", "resolves to include",
		"can sign sign-in tokens with any claims, i.e. act as any run against the budget database"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("problem %q lacks %q", c.Problem, want)
		}
	}
	if c.Severity != "warning" || c.OK || c.ID != "token-signers-1" {
		t.Fatalf("check %+v", c)
	}
	if want := "review before running: gcloud projects remove-iam-policy-binding proj-1234 --member=user:eve@example.com --role=roles/iam.serviceAccountTokenCreator"; c.Fix != want {
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
		"review before running: gcloud projects remove-iam-policy-binding proj-1234 --member=user:eve@example.com --role=projects/proj-1234/roles/mySigner",
		"review before running: gcloud iam service-accounts remove-iam-policy-binding " + dsSched + " --project proj-1234 --member=group:ops@example.com --role=roles/iam.serviceAccountTokenCreator",
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
	c, ok := summary(o)
	if err == nil || !ok || c.Severity != "warning" || c.OK || !strings.Contains(c.Problem, "other-fp") || c.Fix == "" {
		t.Fatalf("--strict must fail when nothing could be read: err %v, check %+v", err, c)
	}
	if _, err := r.doctor(t); err != nil {
		t.Fatalf("without --strict: %v", err)
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

// withClassRoles makes the roles the live fugaro-dev run resolved readable,
// with the permissions that matter.
func (r *signerRig) withClassRoles() {
	for role, perms := range map[string][]string{
		"roles/owner":                           {"resourcemanager.projects.get", "iam.serviceAccountKeys.create", "resourcemanager.projects.setIamPolicy"},
		"roles/editor":                          {"iam.serviceAccountKeys.create"},
		"roles/cloudbuild.serviceAgent":         {"iam.serviceAccounts.getAccessToken", "iam.serviceAccounts.signBlob"},
		"roles/cloudscheduler.serviceAgent":     {"iam.serviceAccounts.getAccessToken"},
		"roles/firebase.managementServiceAgent": {"iam.serviceAccounts.setIamPolicy"},
		"roles/run.serviceAgent":                {"iam.serviceAccounts.signBlob"},
	} {
		r.iam.SetRolePermissions(role, perms...)
	}
}

func checkByID(o doctorOutput, id string) (doctorCheck, bool) { return doctorCheckByID(o.Checks, id) }

const dsNum = "123456789012" // the doctor rig's project number

// The live fugaro-dev result: Google's service agents of the project and the
// owner are information (one line each, no remove command) and never fail
// --strict; only the unexpected power is a warning.
func TestDoctorTokenSignersClasses(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withClassRoles()
	agents := []string{
		"serviceAccount:service-" + dsNum + "@gcp-sa-cloudbuild.iam.gserviceaccount.com",
		"serviceAccount:service-" + dsNum + "@gcp-sa-cloudscheduler.iam.gserviceaccount.com",
		"serviceAccount:service-" + dsNum + "@gcp-sa-firebase.iam.gserviceaccount.com",
		"serviceAccount:service-" + dsNum + "@serverless-robot-prod.iam.gserviceaccount.com",
	}
	roles := []string{"roles/cloudbuild.serviceAgent", "roles/cloudscheduler.serviceAgent", "roles/firebase.managementServiceAgent", "roles/run.serviceAgent"}
	var bs []gcpfake.Binding
	for i, a := range agents {
		bs = append(bs, gcpfake.Binding{Role: roles[i], Members: []string{a}})
	}
	r.crm.SetPolicy("proj-1234", append(bs, gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com", "group:owners@example.com"}})...)
	o, err := r.doctor(t, "--strict")
	if err != nil || len(signerChecks(o)) != 0 {
		t.Fatalf("--strict must stay green: err %v, checks %+v", err, o.Checks)
	}
	g, ok := checkByID(o, "token-signers-google-agents")
	if !ok || g.OK || g.Severity != "info" || g.Fix != "" {
		t.Fatalf("agents check %+v", g)
	}
	for i, a := range agents {
		if !strings.Contains(g.Problem, strings.TrimPrefix(a, "serviceAccount:")) || !strings.Contains(g.Problem, roles[i]) {
			t.Errorf("agents line %q lacks %s / %s", g.Problem, a, roles[i])
		}
	}
	for _, want := range []string{"Google-operated service agents of this project hold roles that can sign tokens or change IAM", "Google-operated", "not removable without breaking the service", "the trust is the same as trusting Google", "no action"} {
		if !strings.Contains(g.Problem, want) {
			t.Errorf("agents line %q lacks %q", g.Problem, want)
		}
	}
	w, ok := checkByID(o, "token-signers-owners")
	if !ok || w.OK || w.Severity != "info" || w.Fix != "" {
		t.Fatalf("owners check %+v", w)
	}
	for _, want := range []string{"project owners can create service-account keys and therefore sign tokens", "the trust root", "2 owner(s)", "user:owner@example.com", "group:owners@example.com", "keep this list short"} {
		if !strings.Contains(w.Problem, want) {
			t.Errorf("owners line %q lacks %q", w.Problem, want)
		}
	}
	for _, c := range o.Checks {
		if strings.Contains(c.Fix, "remove-iam-policy-binding") {
			t.Errorf("a class A/B line carries a remove command: %+v", c)
		}
	}
}

// Look-alikes, editors and a non-user owner stay warnings with a remove command.
func TestDoctorTokenSignersLookAlikesAndEditorsWarn(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withClassRoles()
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/cloudbuild.serviceAgent", Members: []string{
			"serviceAccount:service-999@gcp-sa-cloudbuild.iam.gserviceaccount.com",
			"serviceAccount:service-" + dsNum + "@gcp-sa-cloudbuild.evil.com",
			"user:service-" + dsNum + "@gcp-sa-cloudbuild.iam.gserviceaccount.com",
			"serviceAccount:service-" + dsNum + "@other-proj.iam.gserviceaccount.com"}},
		gcpfake.Binding{Role: "roles/editor", Members: []string{"user:ed@example.com", "serviceAccount:" + dsNum + "-compute@developer.gserviceaccount.com"}},
		gcpfake.Binding{Role: "roles/owner", Members: []string{"allUsers"}})
	o, err := r.doctor(t, "--strict")
	cs := signerChecks(o)
	if err == nil || len(cs) != 7 {
		t.Fatalf("err %v, checks %+v", err, cs)
	}
	for _, c := range cs {
		if c.Severity != "warning" || !strings.HasPrefix(c.Fix, "review before running: gcloud projects remove-iam-policy-binding proj-1234 --member=") {
			t.Errorf("check %+v", c)
		}
	}
	if _, ok := checkByID(o, "token-signers-google-agents"); ok {
		t.Error("a look-alike produced an agents line")
	}
	if _, ok := checkByID(o, "token-signers-owners"); ok {
		t.Error("allUsers produced an owners line")
	}
}

// The project's number unreadable: no Google agent is recognised, so they
// are warnings again (never silently trusted), and the summary says why.
func TestDoctorTokenSignersProjectNumberUnreadable(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withClassRoles()
	r.crm.SetPolicy("proj-1234", gcpfake.Binding{Role: "roles/cloudbuild.serviceAgent",
		Members: []string{"serviceAccount:service-" + dsNum + "@gcp-sa-cloudbuild.iam.gserviceaccount.com"}})
	r.crm.DenyGet("proj-1234")
	o, _ := r.doctor(t)
	cs := signerChecks(o)
	if len(cs) != 1 || cs[0].Severity != "warning" {
		t.Fatalf("checks %+v", o.Checks)
	}
	if _, ok := checkByID(o, "token-signers-google-agents"); ok {
		t.Fatal("an agent was recognised without the project number")
	}
	if c, _ := summary(o); !strings.Contains(c.Problem, "project number") {
		t.Errorf("summary %q does not say the number was unreadable", c.Problem)
	}
}
