package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

const (
	dsSigner = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
	dsSched  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
	dsMinter = "projects/proj-1234/roles/fugaroTokenMinter"
	// dsDeployer is the managed deployment's deployer account (tf.DeployerAccount)
	// and dsDeployerRole the conditioned custom role the module binds it under,
	// the one whose permissions resolve to iam.serviceAccounts.setIamPolicy.
	dsDeployer     = "fugaro-deployer@proj-1234.iam.gserviceaccount.com"
	dsDeployerRole = "projects/proj-1234/roles/fugaroDeployerAccount"
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
		"budget: { mode: observe, per_run_usd: 5, firebase_project: " + fp + ", token_signer: " + signer + " }\n" +
		"base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3 }\n"
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withOperators records the operators the installation names, as the local
// config `fugaro update` writes carries them.
func (r *signerRig) withOperators(t *testing.T, ops ...string) {
	t.Helper()
	b, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(ops))
	for i, o := range ops {
		quoted[i] = `"` + o + `"`
	}
	if err := os.WriteFile(r.cfgPath, append(b, []byte("terraform: { operators: ["+strings.Join(quoted, ", ")+"] }\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withDeployer gives the fake the managed deployment's deployer account and
// the conditioned role the module binds it under (deployer.tf), with that
// role's own four permissions.
func (r *signerRig) withDeployer(t *testing.T) {
	t.Helper()
	r.iam.AddServiceAccount("proj-1234", dsDeployer, "deployer")
	r.iam.AddRole("proj-1234", "fugaroDeployerAccount", "Fugaro managed deployer (prefixed accounts only)", false)
	r.iam.SetRolePermissions(dsDeployerRole,
		"iam.serviceAccounts.actAs", "iam.serviceAccounts.delete", "iam.serviceAccounts.setIamPolicy", "iam.serviceAccounts.update")
}

// deployerFence is the condition the module writes on that role
// (tf.DeployerConditionExpression), byte for byte.
func deployerFence(t *testing.T) *gcpfake.IAMCondition {
	t.Helper()
	expr, ok := tf.DeployerConditionExpression("proj-1234", tf.DeployerAccountRole)
	if !ok {
		t.Fatal("the module has no condition for " + tf.DeployerAccountRole)
	}
	return &gcpfake.IAMCondition{Title: tf.DeployerConditionTitle, Expression: expr}
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

// doctorText is the human view of the same rig, for the one thing --json
// cannot show: whether a line is on the screen a person reads at all.
func (r *signerRig) doctorText(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	out, _, err := execute(t, append([]string{"doctor", "--dir", root}, args...)...)
	return out, err
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
	for _, want := range []string{"checked the project and service-account IAM policies of proj-1234", "not read:", "folder- and organization-inherited bindings", "deny policies", "Google service agents of this project are read and classified", "docs/design/m9-budget-and-dashboard.md"} {
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

// A conditional binding that is classed information names its condition, so a
// temporary grant is not read as a standing one: a conditional owner, a
// conditional service agent, and a conditional Admin SDK binding.
func TestDoctorTokenSignersInfoLinesNameTheCondition(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withClassRoles()
	cond := &gcpfake.IAMCondition{Title: "temp", Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"}
	agent := "serviceAccount:service-" + dsNum + "@gcp-sa-cloudbuild.iam.gserviceaccount.com"
	r.iam.AddServiceAccount("proj-1234", "firebase-adminsdk-fbsvc@proj-1234.iam.gserviceaccount.com", "adminsdk")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:temp-owner@example.com"}, Condition: cond},
		gcpfake.Binding{Role: "roles/cloudbuild.serviceAgent", Members: []string{agent}},
		gcpfake.Binding{Role: "roles/cloudscheduler.serviceAgent", Members: []string{agent}, Condition: cond},
		gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"serviceAccount:firebase-adminsdk-fbsvc@proj-1234.iam.gserviceaccount.com"}, Condition: &gcpfake.IAMCondition{Title: "", Expression: "resource.name != 'x'"}})
	o, err := r.doctor(t, "--strict")
	if err != nil || len(signerChecks(o)) != 0 {
		t.Fatalf("--strict must stay green: err %v, checks %+v", err, o.Checks)
	}
	w, ok := checkByID(o, "token-signers-owners")
	if !ok || !strings.Contains(w.Problem, "user:temp-owner@example.com (only while temp)") || !strings.Contains(w.Problem, "2 owner(s)") {
		t.Errorf("owners line %q does not name the owner's condition", w.Problem)
	}
	if strings.Contains(w.Problem, "user:owner@example.com (only") {
		t.Errorf("the unconditional owner is shown as conditional: %q", w.Problem)
	}
	g, ok := checkByID(o, "token-signers-google-agents")
	if !ok || !strings.Contains(g.Problem, "roles/cloudscheduler.serviceAgent (only while temp)") || strings.Contains(g.Problem, "roles/cloudbuild.serviceAgent (only") {
		t.Errorf("agents line %q does not name the agent's condition", g.Problem)
	}
	s, _ := checkByID(o, "token-signers")
	if !strings.Contains(s.Problem, "firebase-adminsdk-fbsvc@proj-1234.iam.gserviceaccount.com (only while resource.name != 'x')") {
		t.Errorf("summary %q does not name the Admin SDK binding's condition (the expression stands in for an empty title)", s.Problem)
	}
}

// TestDoctorTokenSignersUnreadProjectNumberIsCounted: an unread project number
// is an unread state — no Google service agent is recognised, so the summary's
// counts are not what the project's policy says — and the summary is a counted
// finding rather than a measurement printed beside the ok line.
func TestDoctorTokenSignersUnreadProjectNumberIsCounted(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.project(gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"}})
	r.crm.FailGet("proj-1234", 403, "PERMISSION_DENIED", "no resourcemanager.projects.get")

	o, _ := r.doctor(t)
	c, ok := summary(o)
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("summary = %+v (found %v)", c, ok)
	}
	if !strings.Contains(c.Problem, "the project number could not be read") {
		t.Fatalf("the rig did not reach the unread project number: %s", c.Problem)
	}
	if c.Class != doctorClassAudit {
		t.Errorf("the signer summary with an unread project number is classed %q, want %q", c.Class, doctorClassAudit)
	}
}

// TestDoctorTokenSignersManagedDeploymentWarns: the two grants Fugaro's own
// Terraform makes on a managed installation are a WARNING, not information.
// The information class is what hid them: token-signers-managed was a
// measurement, so the default screen did not print it and --strict stayed
// green, while the deployer's unconditioned fugaroDeployer role holds
// iam.serviceAccounts.create (deployer.tf), so the account creates an fmgd-
// account of the project, the account role's own condition names exactly the
// accounts it creates, and its iam.serviceAccounts.setIamPolicy is how it
// grants itself the token-creator role on the one it made — and Firebase
// accepts a token signed by ANY account of the project (live Check 26, step
// 7). That the binding is on the deployer account rather than the signer is
// no defence: in the same-project layout the deployer IS an account of the
// Firebase project.
//
// What a service makes of the module's condition is Check 68's to measure and
// is not claimed here (docs/design/managed-deployer-fence.md: until it has
// run, the prefix condition is not a boundary).
//
// Mutations this test is the guard for: classifying the two as information
// again must fail the severity, the class and the --strict assertions, and
// dropping the create-and-grant sentence from the line must fail the text
// ones.
func TestDoctorTokenSignersManagedDeploymentWarns(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withDeployer(t)
	r.withOperators(t, "user:ops@example.com")
	r.project(gcpfake.Binding{Role: dsDeployerRole, Members: []string{"serviceAccount:" + dsDeployer}, Condition: deployerFence(t)})
	r.iam.SetServiceAccountPolicy("proj-1234", dsDeployer, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{"user:ops@example.com"}})

	o, err := r.doctor(t, "--strict")
	if err == nil {
		t.Fatalf("--strict must fail on the managed grants: %+v", o.Checks)
	}
	if cs := signerChecks(o); len(cs) != 0 {
		t.Fatalf("the module's own bindings are the class line, not numbered warnings: %+v", cs)
	}
	c, ok := checkByID(o, "token-signers-managed")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("managed check %+v (found %v)", c, ok)
	}
	for _, want := range []string{
		dsDeployer, dsDeployerRole, tf.DeployerConditionTitle, tf.ManagedPrefix,
		"user:ops@example.com", tf.DeployerTokenCreatorRole,
		// The path, not the condition alone: the deployer's own create, and the
		// account role's setIamPolicy on the account it made.
		tf.DeployerRole, "iam.serviceAccounts.create", "iam.serviceAccounts.setIamPolicy",
		// And the check that measures what the condition is worth.
		"Check 68",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("managed line %q lacks %q", c.Problem, want)
		}
	}
	if strings.Contains(c.Problem, "reaches no service account whose name does not carry it") {
		t.Errorf("the managed line claims the fence's enforcement, which no document may claim before Check 68 runs: %q", c.Problem)
	}
	if strings.Contains(c.Problem+c.Fix, "remove-iam-policy-binding") {
		t.Errorf("the module's own binding is offered a remove command: %q / %q", c.Problem, c.Fix)
	}
	if c.Class != doctorClassActionable {
		t.Errorf("the managed line is classed %q, want %q: a measurement is the class that hid it", c.Class, doctorClassActionable)
	}
	// The default view is the one that has to carry it: a person who runs
	// doctor after `fugaro update` reads the short line, and --strict fails
	// there too.
	text, err := r.doctorText(t, "--strict")
	if err == nil {
		t.Error("--strict must fail in the human view as well")
	}
	if !strings.Contains(text, "warning token-signers-managed: the managed deployer can sign here") {
		t.Errorf("the default view does not print the managed line:\n%s", text)
	}
}

// TestDoctorTokenSignersManagedBindingsAreInTheDeployerProject: the warning is
// about the deployer being inside the Firebase project. Doctor reads the
// Firebase project's policy and the policy of each of its accounts, so an
// installation whose Firebase project is a project of its own does not show
// these bindings at all: they are in the deployer's project, and neither is a
// binding of the project that signs. The same installation with the same two
// bindings then has nothing to warn about — and nothing hidden either.
func TestDoctorTokenSignersManagedBindingsAreInTheDeployerProject(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withDeployer(t)
	r.project(gcpfake.Binding{Role: dsDeployerRole, Members: []string{"serviceAccount:" + dsDeployer}, Condition: deployerFence(t)})
	r.iam.SetServiceAccountPolicy("proj-1234", dsDeployer, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{"user:ops@example.com"}})
	r.crm.AddProject("fp-5678", 567890123456)
	r.crm.SetPolicy("fp-5678", gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}})
	r.withBudget(t, "fp-5678", "fugaro-token-signer@fp-5678.iam.gserviceaccount.com")

	o, err := r.doctor(t, "--strict")
	if err != nil {
		t.Fatalf("--strict must stay green: %+v", o.Checks)
	}
	if c, ok := checkByID(o, "token-signers-managed"); ok {
		t.Errorf("the deployer's project is not the Firebase project, so its bindings are not read: %+v", c)
	}
}

// TestDoctorTokenSignersManagedDeployerNeedsTheFence: the recognition is the
// module's exact conditional grant, never the deployer account's name alone.
// The same role with no condition, under another expression, and granted to
// another principal is a warning like any other — an unconditional
// setIamPolicy on a project really can grant itself signing on the signer.
func TestDoctorTokenSignersManagedDeployerNeedsTheFence(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withDeployer(t)
	r.withOperators(t, "user:ops@example.com")
	r.project(
		gcpfake.Binding{Role: dsDeployerRole, Members: []string{"serviceAccount:" + dsDeployer}},
		gcpfake.Binding{Role: dsDeployerRole, Members: []string{"serviceAccount:" + dsDeployer},
			Condition: &gcpfake.IAMCondition{Title: tf.DeployerConditionTitle, Expression: `resource.type == "iam.googleapis.com/ServiceAccount"`}},
		gcpfake.Binding{Role: dsDeployerRole, Members: []string{"user:eve@example.com"}, Condition: deployerFence(t)},
	)
	o, err := r.doctor(t)
	cs := signerChecks(o)
	if err == nil && len(cs) == 0 {
		t.Fatal("an unconditional or foreign setIamPolicy grant must warn")
	}
	if len(cs) != 3 {
		t.Fatalf("checks %+v", cs)
	}
	for _, c := range cs {
		if c.Severity != "warning" || !strings.Contains(c.Problem, "can grant itself the right to sign") ||
			!strings.HasPrefix(c.Fix, "review before running: gcloud projects remove-iam-policy-binding proj-1234 --member=") {
			t.Errorf("check %+v", c)
		}
	}
	if _, ok := checkByID(o, "token-signers-managed"); ok {
		t.Error("a grant that is not the module's fence produced a managed line")
	}
}

// TestDoctorTokenSignersOperatorImpersonationWarns: the operator's
// token-creator grant on the deployer account is the managed mode's one-time
// setup (Task 1.5 step 2 of the managed-projects plan), and it warns for the
// same reason the deployer's own binding does: assuming the deployer reaches
// the create-and-grant path, and this check reads the Firebase project's
// accounts, so it sees the binding exactly where the Firebase project is the
// deployer's own project.
// Mutation: dropping the operator list from what doctor passes the detector
// must turn it into a numbered warning.
func TestDoctorTokenSignersOperatorImpersonationWarns(t *testing.T) {
	r := newSignerDoctorRig(t)
	r.withDeployer(t)
	r.withOperators(t, "user:ops@example.com")
	r.iam.SetServiceAccountPolicy("proj-1234", dsDeployer, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{"user:ops@example.com"}})
	o, err := r.doctor(t, "--strict")
	if err == nil {
		t.Fatalf("--strict must fail on the operator's impersonation grant: %+v", o.Checks)
	}
	if cs := signerChecks(o); len(cs) != 0 {
		t.Fatalf("the managed mode's own grant is the class line, not a numbered warning: %+v", cs)
	}
	c, ok := checkByID(o, "token-signers-managed")
	if !ok || c.OK || c.Severity != "warning" || !strings.Contains(c.Problem, "user:ops@example.com") {
		t.Fatalf("managed check %+v (found %v)", c, ok)
	}
	if strings.Contains(c.Problem+c.Fix, "remove-iam-policy-binding") {
		t.Errorf("the managed mode's own grant is offered a remove command: %q / %q", c.Problem, c.Fix)
	}
}

// TestDoctorTokenSignersOperatorGrantOutsideTheDesignWarns: the recognition is
// exactly the operator, exactly the token-creator role and exactly the
// deployer account. Anyone else on that account, an operator on the signer,
// and an operator with no operators in the installation's config all stay
// warnings with a remove command.
func TestDoctorTokenSignersOperatorGrantOutsideTheDesignWarns(t *testing.T) {
	t.Run("a principal the installation does not name", func(t *testing.T) {
		r := newSignerDoctorRig(t)
		r.withDeployer(t)
		r.withOperators(t, "user:ops@example.com")
		r.iam.SetServiceAccountPolicy("proj-1234", dsDeployer, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{"user:eve@example.com"}})
		o, _ := r.doctor(t)
		if cs := signerChecks(o); len(cs) != 1 || !strings.Contains(cs[0].Problem, "user:eve@example.com") {
			t.Fatalf("checks %+v", cs)
		}
		if _, ok := checkByID(o, "token-signers-managed"); ok {
			t.Error("a grant to a non-operator produced a managed line")
		}
	})
	t.Run("an operator on the signer account", func(t *testing.T) {
		r := newSignerDoctorRig(t)
		r.withDeployer(t)
		r.withOperators(t, "user:ops@example.com")
		r.iam.SetServiceAccountPolicy("proj-1234", dsSigner, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{"user:ops@example.com"}})
		o, _ := r.doctor(t)
		cs := signerChecks(o)
		if len(cs) != 1 || !strings.Contains(cs[0].Problem, "service account "+dsSigner) {
			t.Fatalf("checks %+v", cs)
		}
	})
	t.Run("no operators in the installation's config", func(t *testing.T) {
		r := newSignerDoctorRig(t)
		r.withDeployer(t)
		r.iam.SetServiceAccountPolicy("proj-1234", dsDeployer, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{"user:ops@example.com"}})
		o, _ := r.doctor(t)
		if cs := signerChecks(o); len(cs) != 1 || !strings.Contains(cs[0].Problem, "user:ops@example.com") {
			t.Fatalf("checks %+v", cs)
		}
	})
}
